package automation

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"

	"weloury/internal/eventbus"
	"weloury/internal/system"
)

// fakeSys implements sysCapabilities entirely in memory - mirrors
// internal/health's fakeSys, letting every action be tested without a
// real OS provider.
type fakeSys struct {
	mu sync.Mutex

	processes    []system.ProcessInfo
	listErr      error
	terminated   []int32
	terminateErr map[int32]error

	startedServices   []string
	restartedServices []string
	serviceErr        error
}

func (f *fakeSys) ListProcesses(context.Context) ([]system.ProcessInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.processes, f.listErr
}

func (f *fakeSys) TerminateProcess(_ context.Context, pid int32, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminated = append(f.terminated, pid)
	if f.terminateErr != nil {
		if err, ok := f.terminateErr[pid]; ok {
			return err
		}
	}
	return nil
}

func (f *fakeSys) StartService(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startedServices = append(f.startedServices, name)
	return f.serviceErr
}

func (f *fakeSys) RestartService(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restartedServices = append(f.restartedServices, name)
	return f.serviceErr
}

// startedServicesSnapshot/restartedServicesSnapshot/terminatedSnapshot
// are locked reads for tests that poll fakeSys's state from a different
// goroutine than the one calling StartService/TerminateProcess/etc. -
// i.e. any engine_test.go test using waitFor against an Engine that
// executes actions on its own background goroutine. Reading the slice
// fields directly from such a test would be a genuine data race (caught
// by `go test -race`, and it did - this isn't hypothetical).
func (f *fakeSys) startedServicesSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.startedServices...)
}

func (f *fakeSys) restartedServicesSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.restartedServices...)
}

func (f *fakeSys) terminatedSnapshot() []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int32(nil), f.terminated...)
}

// fakeLauncher implements processLauncher without touching the real OS.
type fakeLauncher struct {
	mu       sync.Mutex
	launched [][]string
	err      error
}

func (f *fakeLauncher) Launch(args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launched = append(f.launched, args)
	return f.err
}

func testRule() Rule {
	return Rule{ID: "rule_1", Name: "test rule"}
}

func TestActionStopProcessTerminatesAllMatches(t *testing.T) {
	sys := &fakeSys{processes: []system.ProcessInfo{
		{PID: 1, Name: "worker"},
		{PID: 2, Name: "WORKER"}, // case-insensitive match, same as health's process_exists
		{PID: 3, Name: "other"},
	}}
	result := actionStopProcess(context.Background(), sys, Action{ProcessName: "worker"})
	if !result.ok {
		t.Fatalf("expected success, got: %s", result.message)
	}
	if len(sys.terminated) != 2 {
		t.Fatalf("terminated = %v, want 2 PIDs (1 and 2, not 3)", sys.terminated)
	}
}

func TestActionStopProcessNoMatchFails(t *testing.T) {
	sys := &fakeSys{processes: []system.ProcessInfo{{PID: 1, Name: "other"}}}
	result := actionStopProcess(context.Background(), sys, Action{ProcessName: "worker"})
	if result.ok {
		t.Error("expected failure when no process matches")
	}
}

func TestActionRestartProcessTerminatesAndLaunches(t *testing.T) {
	sys := &fakeSys{processes: []system.ProcessInfo{{PID: 1, Name: "worker"}}}
	launcher := &fakeLauncher{}

	start := time.Now()
	result := actionRestartProcess(context.Background(), sys, launcher, Action{
		ProcessName:   "worker",
		LaunchCommand: []string{"/usr/bin/worker", "--flag"},
	})
	elapsed := time.Since(start)

	if !result.ok {
		t.Fatalf("expected success, got: %s", result.message)
	}
	if len(sys.terminated) != 1 || sys.terminated[0] != 1 {
		t.Errorf("terminated = %v, want [1]", sys.terminated)
	}
	if len(launcher.launched) != 1 {
		t.Fatalf("launched = %v, want one launch", launcher.launched)
	}
	if launcher.launched[0][0] != "/usr/bin/worker" {
		t.Errorf("launched command = %v, want to start with /usr/bin/worker", launcher.launched[0])
	}
	if elapsed < terminationGracePeriod {
		t.Errorf("elapsed = %v, want at least the termination grace period (%v)", elapsed, terminationGracePeriod)
	}
}

func TestActionRestartProcessNoExistingInstanceStillLaunches(t *testing.T) {
	sys := &fakeSys{}
	launcher := &fakeLauncher{}
	result := actionRestartProcess(context.Background(), sys, launcher, Action{
		ProcessName:   "worker",
		LaunchCommand: []string{"/usr/bin/worker"},
	})
	if !result.ok {
		t.Fatalf("expected success even with nothing to terminate, got: %s", result.message)
	}
	if len(launcher.launched) != 1 {
		t.Error("expected the launch to still happen")
	}
}

func TestActionRestartProcessLaunchFailure(t *testing.T) {
	sys := &fakeSys{}
	launcher := &fakeLauncher{err: errors.New("boom")}
	result := actionRestartProcess(context.Background(), sys, launcher, Action{
		ProcessName:   "worker",
		LaunchCommand: []string{"/usr/bin/worker"},
	})
	if result.ok {
		t.Error("expected failure when launch fails")
	}
}

func TestActionStartAndRestartService(t *testing.T) {
	sys := &fakeSys{}
	if r := executeAction(context.Background(), sys, &fakeLauncher{}, nil, testRule(), Action{Type: ActionStartService, ServiceName: "nginx"}); !r.ok {
		t.Errorf("start_service failed: %s", r.message)
	}
	if r := executeAction(context.Background(), sys, &fakeLauncher{}, nil, testRule(), Action{Type: ActionRestartService, ServiceName: "nginx"}); !r.ok {
		t.Errorf("restart_service failed: %s", r.message)
	}
	if len(sys.startedServices) != 1 || sys.startedServices[0] != "nginx" {
		t.Errorf("startedServices = %v", sys.startedServices)
	}
	if len(sys.restartedServices) != 1 || sys.restartedServices[0] != "nginx" {
		t.Errorf("restartedServices = %v", sys.restartedServices)
	}
}

func TestActionServiceFailure(t *testing.T) {
	sys := &fakeSys{serviceErr: errors.New("service manager unavailable")}
	result := executeAction(context.Background(), sys, &fakeLauncher{}, nil, testRule(), Action{Type: ActionStartService, ServiceName: "nginx"})
	if result.ok {
		t.Error("expected failure when the service provider errors")
	}
}

// TestActionRunCommandNeverUsesAShell mirrors
// internal/health/checkers_test.go's TestCheckCommandNeverUsesAShell -
// same property, same reasoning, same behavioral-proof technique (see
// that test's doc comment for the full explanation of why `false "; true"`
// is a real regression trap and not just a documented assumption).
func TestActionRunCommandNeverUsesAShell(t *testing.T) {
	falseBin, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no `false` binary available in this environment")
	}
	result := actionRunCommand(context.Background(), Action{CommandArgs: []string{falseBin, "; true"}})
	if result.ok {
		t.Fatalf("expected failure - a shell-interpreted argument would incorrectly report success here")
	}
}

func TestActionRunCommandFailureExitCode(t *testing.T) {
	falseBin, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no `false` binary available in this environment")
	}
	result := actionRunCommand(context.Background(), Action{CommandArgs: []string{falseBin}})
	if result.ok {
		t.Error("expected failure for a nonzero exit code")
	}
}

func TestPublishCustomEventUsesFixedType(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeCustomEvent), 4)
	defer unsub()

	publishCustom(bus, TypeCustomEvent, eventbus.SeverityInfo, testRule(), "hello")

	select {
	case evt := <-ch:
		if evt.Type != TypeCustomEvent {
			t.Errorf("evt.Type = %q, want %q", evt.Type, TypeCustomEvent)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published event")
	}
}

func TestExecuteActionUnrecognizedType(t *testing.T) {
	result := executeAction(context.Background(), &fakeSys{}, &fakeLauncher{}, nil, testRule(), Action{Type: "bogus"})
	if result.ok {
		t.Error("expected failure for an unrecognized action type")
	}
}

// TestActionRunCommandRespectsContextDeadline is a regression test for
// a real, previously-unverified gap found while reading actions.go and
// engine.go together as a whole: actionRunCommand runs
// exec.CommandContext(ctx, ...) with whatever ctx it's handed and
// nothing else - no internal timeout of its own - so the only thing
// standing between a hanging command and blocking its execution
// goroutine forever is the caller actually supplying a bounded ctx.
// Engine.executeActions does exactly that (context.WithTimeout(ctx,
// e.actionTimeout) around every action - see its doc comment), but
// until this test, nothing proved that wiring actually stops a
// genuinely hanging command rather than just documenting the intent:
// exec.CommandContext's kill-on-cancellation behavior is a real
// property of a real child process, not something a fake or a code
// reading can confirm on its own.
//
// Uses a real `sleep` process, not a fake - this property is
// specifically about OS process lifecycle, which no fake can stand in
// for.
func TestActionRunCommandRespectsContextDeadline(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no `sleep` binary available in this environment")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := actionRunCommand(ctx, Action{CommandArgs: []string{sleepBin, "30"}})
	elapsed := time.Since(start)

	if result.ok {
		t.Error("expected failure - a command killed by a context deadline is not a success")
	}
	// Generous upper bound (2s against a 100ms deadline) to absorb
	// scheduler/CI jitter without weakening what this actually proves:
	// the real question is "did it return in roughly its deadline, not
	// its full 30-second sleep," and 2s vs 30s answers that
	// unambiguously either way.
	if elapsed > 2*time.Second {
		t.Errorf("actionRunCommand took %v to return against a 100ms context deadline - the command was not actually killed on cancellation (it ran closer to its full 30s sleep)", elapsed)
	}
}
