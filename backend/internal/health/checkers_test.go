package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"weloury/internal/system"
)

// fakeSys implements sysCapabilities entirely in memory, letting every
// checker be tested without a real OS provider and without needing
// internal/system.Provider's full ~15-method surface - see
// sysCapabilities's doc comment in checkers.go.
type fakeSys struct {
	processes  []system.ProcessInfo
	processErr error

	serviceDetail system.ServiceDetail
	serviceErr    error

	disks    []system.DiskInfo
	disksErr error

	memory    system.MemoryInfo
	memoryErr error

	cpuUsage system.CPUUsage
	cpuErr   error
}

func (f *fakeSys) ListProcesses(context.Context) ([]system.ProcessInfo, error) {
	return f.processes, f.processErr
}
func (f *fakeSys) ServiceDetail(context.Context, string) (system.ServiceDetail, error) {
	return f.serviceDetail, f.serviceErr
}
func (f *fakeSys) Disks(context.Context) ([]system.DiskInfo, error) { return f.disks, f.disksErr }
func (f *fakeSys) MemoryInfo(context.Context) (system.MemoryInfo, error) {
	return f.memory, f.memoryErr
}
func (f *fakeSys) CPUUsage(context.Context, time.Duration) (system.CPUUsage, error) {
	return f.cpuUsage, f.cpuErr
}

func TestCheckProcessExists(t *testing.T) {
	sys := &fakeSys{processes: []system.ProcessInfo{{PID: 42, Name: "my-worker"}}}

	got := evaluate(context.Background(), sys, Check{Type: CheckProcessExists, Target: "my-worker"})
	if got.state != StateHealthy {
		t.Errorf("state = %v, want HEALTHY for a running process", got.state)
	}

	got = evaluate(context.Background(), sys, Check{Type: CheckProcessExists, Target: "MY-WORKER"})
	if got.state != StateHealthy {
		t.Errorf("expected case-insensitive match, got state = %v", got.state)
	}

	got = evaluate(context.Background(), sys, Check{Type: CheckProcessExists, Target: "nonexistent"})
	if got.state != StateUnhealthy {
		t.Errorf("state = %v, want UNHEALTHY for a missing process", got.state)
	}
}

func TestCheckServiceState(t *testing.T) {
	cases := []struct {
		name   string
		detail system.ServiceDetail
		err    error
		want   State
	}{
		{"running", system.ServiceDetail{ServiceInfo: system.ServiceInfo{State: "running"}}, nil, StateHealthy},
		{"stopped", system.ServiceDetail{ServiceInfo: system.ServiceInfo{State: "stopped"}}, nil, StateUnhealthy},
		{"not found", system.ServiceDetail{}, system.ErrNotFound, StateUnhealthy},
		{"unavailable", system.ServiceDetail{}, system.ErrUnavailable, StateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys := &fakeSys{serviceDetail: tc.detail, serviceErr: tc.err}
			got := evaluate(context.Background(), sys, Check{Type: CheckServiceState, Target: "svc"})
			if got.state != tc.want {
				t.Errorf("state = %v, want %v (message: %s)", got.state, tc.want, got.message)
			}
		})
	}
}

func TestCheckTCPPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	got := evaluate(context.Background(), &fakeSys{}, Check{Type: CheckTCPPort, Target: ln.Addr().String()})
	if got.state != StateHealthy {
		t.Errorf("state = %v, want HEALTHY for an open port (%s)", got.state, got.message)
	}

	// A port nothing is listening on.
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln2.Addr().String()
	ln2.Close()

	got = evaluate(context.Background(), &fakeSys{}, Check{Type: CheckTCPPort, Target: addr})
	if got.state != StateUnhealthy {
		t.Errorf("state = %v, want UNHEALTHY for a closed port", got.state)
	}
}

func TestCheckHTTPEndpoint(t *testing.T) {
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()

	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errServer.Close()

	notFoundServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFoundServer.Close()

	got := evaluate(context.Background(), &fakeSys{}, Check{Type: CheckHTTPEndpoint, Target: okServer.URL})
	if got.state != StateHealthy {
		t.Errorf("state = %v, want HEALTHY for a 200 response", got.state)
	}

	got = evaluate(context.Background(), &fakeSys{}, Check{Type: CheckHTTPEndpoint, Target: errServer.URL})
	if got.state != StateUnhealthy {
		t.Errorf("state = %v, want UNHEALTHY for a 500 response", got.state)
	}

	got = evaluate(context.Background(), &fakeSys{}, Check{Type: CheckHTTPEndpoint, Target: notFoundServer.URL})
	if got.state != StateUnhealthy {
		t.Errorf("state = %v, want UNHEALTHY for a 404 response", got.state)
	}

	got = evaluate(context.Background(), &fakeSys{}, Check{Type: CheckHTTPEndpoint, Target: "not-a-url"})
	if got.state != StateUnknown {
		t.Errorf("state = %v, want UNKNOWN for a malformed target", got.state)
	}
}

func TestCheckCommand(t *testing.T) {
	trueBin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no `true` binary available in this environment")
	}
	falseBin, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no `false` binary available in this environment")
	}

	got := evaluate(context.Background(), &fakeSys{}, Check{Type: CheckCommand, CommandArgs: []string{trueBin}})
	if got.state != StateHealthy {
		t.Errorf("state = %v, want HEALTHY for exit 0 (%s)", got.state, got.message)
	}

	got = evaluate(context.Background(), &fakeSys{}, Check{Type: CheckCommand, CommandArgs: []string{falseBin}})
	if got.state != StateUnhealthy {
		t.Errorf("state = %v, want UNHEALTHY for a nonzero exit", got.state)
	}

	got = evaluate(context.Background(), &fakeSys{}, Check{Type: CheckCommand, CommandArgs: nil})
	if got.state != StateUnknown {
		t.Errorf("state = %v, want UNKNOWN for no configured command", got.state)
	}
}

// TestCheckCommandNeverUsesAShell is the key security property from
// SECURITY.md/spec section 18: shell metacharacters in an argument must
// be passed through literally to the executed program, never
// interpreted. This is a genuine behavioral proof, not just a
// structural comment: `false` always exits 1 regardless of what
// arguments it's given. If CommandArgs were ever concatenated into a
// shell string instead of passed as argv (a regression this test
// exists to catch), the argument below - a command separator followed
// by a command that exits 0 - would make the *shell's own* exit
// status 0 (a shell script's exit code is its last command's), and
// this check would incorrectly report HEALTHY. Under real argv
// execution, the whole string is one literal argument `false` ignores,
// so the check is always UNHEALTHY. A shell-execution regression flips
// this test's result, not just its comment.
func TestCheckCommandNeverUsesAShell(t *testing.T) {
	falseBin, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no `false` binary available in this environment")
	}
	got := evaluate(context.Background(), &fakeSys{}, Check{
		Type:        CheckCommand,
		CommandArgs: []string{falseBin, "; true"},
	})
	if got.state != StateUnhealthy {
		t.Fatalf("state = %v, want UNHEALTHY - a shell-interpreted argument would incorrectly report HEALTHY here", got.state)
	}
}

func TestEvaluateThresholdBands(t *testing.T) {
	cases := []struct {
		used, threshold float64
		want            State
	}{
		{50, 90, StateHealthy},
		{80, 90, StateDegraded}, // 80 >= 90*0.85 (76.5)
		{95, 90, StateUnhealthy},
		{90, 90, StateUnhealthy}, // exactly at threshold counts as unhealthy
	}
	for _, tc := range cases {
		got := evaluateThreshold(tc.used, tc.threshold, "test")
		if got.state != tc.want {
			t.Errorf("evaluateThreshold(%v, %v) = %v, want %v", tc.used, tc.threshold, got.state, tc.want)
		}
	}
}

func TestCheckDiskThreshold(t *testing.T) {
	sys := &fakeSys{disks: []system.DiskInfo{
		{MountPoint: "/", TotalBytes: 100, UsedBytes: 95},
		{MountPoint: "/data", TotalBytes: 100, UsedBytes: 10},
	}}

	got := evaluate(context.Background(), sys, Check{Type: CheckDiskThreshold, Target: "/", Threshold: 90})
	if got.state != StateUnhealthy {
		t.Errorf("state = %v, want UNHEALTHY at 95%% used with a 90%% threshold", got.state)
	}

	got = evaluate(context.Background(), sys, Check{Type: CheckDiskThreshold, Target: "/data", Threshold: 90})
	if got.state != StateHealthy {
		t.Errorf("state = %v, want HEALTHY at 10%% used", got.state)
	}

	got = evaluate(context.Background(), sys, Check{Type: CheckDiskThreshold, Target: "/nonexistent", Threshold: 90})
	if got.state != StateUnknown {
		t.Errorf("state = %v, want UNKNOWN for a mount point that doesn't exist", got.state)
	}
}

func TestCheckMemoryThreshold(t *testing.T) {
	sys := &fakeSys{memory: system.MemoryInfo{TotalBytes: 100, UsedBytes: 95}}
	got := evaluate(context.Background(), sys, Check{Type: CheckMemoryThreshold, Threshold: 90})
	if got.state != StateUnhealthy {
		t.Errorf("state = %v, want UNHEALTHY", got.state)
	}
}

func TestCheckCPUThreshold(t *testing.T) {
	sys := &fakeSys{cpuUsage: system.CPUUsage{OverallPercent: 20}}
	got := evaluate(context.Background(), sys, Check{Type: CheckCPUThreshold, Threshold: 90})
	if got.state != StateHealthy {
		t.Errorf("state = %v, want HEALTHY", got.state)
	}

	sys = &fakeSys{cpuErr: system.ErrUnavailable}
	got = evaluate(context.Background(), sys, Check{Type: CheckCPUThreshold, Threshold: 90})
	if got.state != StateUnknown {
		t.Errorf("state = %v, want UNKNOWN when the platform can't sample CPU usage", got.state)
	}
}

func TestEvaluateUnrecognizedType(t *testing.T) {
	got := evaluate(context.Background(), &fakeSys{}, Check{Type: "bogus"})
	if got.state != StateUnknown {
		t.Errorf("state = %v, want UNKNOWN for an unrecognized check type", got.state)
	}
}
