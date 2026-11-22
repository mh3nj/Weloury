package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"weloury/internal/eventbus"
	"weloury/internal/hidewin"
	"weloury/internal/system"
)

// sysCapabilities is the minimal slice of system.Provider actions need -
// the same interface-segregation pattern internal/health's and
// internal/metrics' sysCapabilities use, for the same reason: it keeps
// Engine's dependency honest and makes tests able to fake exactly four
// methods instead of system.Provider's full surface.
type sysCapabilities interface {
	ListProcesses(ctx context.Context) ([]system.ProcessInfo, error)
	TerminateProcess(ctx context.Context, pid int32, force bool) error
	StartService(ctx context.Context, name string) error
	RestartService(ctx context.Context, name string) error
}

// processLauncher starts a new, independent process - abstracted purely
// for testability; execLauncher (below) is the real implementation.
type processLauncher interface {
	Launch(args []string) error
}

// execLauncher runs args directly via exec.Command - argv only, never a
// shell, exactly the discipline internal/health's checkCommand uses (see
// checkers.go there) - and immediately releases it from Go's
// process-tracking so it becomes a fully independent process rather than
// a zombie once Weloury exits or this action returns. See Launch's doc
// comment for what "fire and forget" means here.
type execLauncher struct{}

// Launch starts args[0] with args[1:] as arguments and detaches it via
// cmd.Process.Release(). Weloury deliberately does not supervise what it
// launches this way: no restart-on-crash of the newly launched process,
// no captured stdout/stderr, no wait for its exit. It launches once and
// moves on - genuine process supervision (health-checking the
// replacement, retrying a failed launch, ...) is a meaningfully bigger
// feature than "restart" as spec section 11 lists it, and the
// combination of this action plus a process_exists health check against
// the same ProcessName already gives an operator the pieces to build
// that supervision themselves at the rule level if they want it.
func (execLauncher) Launch(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no launch command configured")
	}
	cmd := exec.Command(args[0], args[1:]...)
	hidewin.Configure(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", args[0], err)
	}
	return cmd.Process.Release()
}

// terminationGracePeriod is how long restart_process waits after
// requesting graceful termination of matching processes before launching
// a replacement - long enough for a well-behaved process to actually
// exit on SIGTERM, short enough that a restart doesn't feel broken. A
// fixed heuristic, not real process supervision: this action does not
// wait for the OS to confirm the old process has actually exited before
// launching the new one. See CHANGELOG.md's "Known limitations."
const terminationGracePeriod = 2 * time.Second

// actionResult is one action's outcome within a rule's execution,
// aggregated by Engine.executeActions into the run's overall RunStatus.
type actionResult struct {
	ok      bool
	message string
}

func succeeded(msg string) actionResult { return actionResult{ok: true, message: msg} }
func failed(err error) actionResult     { return actionResult{ok: false, message: err.Error()} }

// executeAction dispatches a to its implementation by Type. ctx should
// already carry a per-action deadline - Engine.executeActions owns that
// policy so it stays in one place, the same division of responsibility
// internal/health's evaluate/runWithRetries split uses.
func executeAction(ctx context.Context, sys sysCapabilities, launcher processLauncher, bus *eventbus.Bus, rule Rule, a Action) actionResult {
	switch a.Type {
	case ActionRestartProcess:
		return actionRestartProcess(ctx, sys, launcher, a)
	case ActionStopProcess:
		return actionStopProcess(ctx, sys, a)
	case ActionStartService:
		if err := sys.StartService(ctx, a.ServiceName); err != nil {
			return failed(fmt.Errorf("start service %q: %w", a.ServiceName, err))
		}
		return succeeded(fmt.Sprintf("started service %q", a.ServiceName))
	case ActionRestartService:
		if err := sys.RestartService(ctx, a.ServiceName); err != nil {
			return failed(fmt.Errorf("restart service %q: %w", a.ServiceName, err))
		}
		return succeeded(fmt.Sprintf("restarted service %q", a.ServiceName))
	case ActionRunCommand:
		return actionRunCommand(ctx, a)
	case ActionCreateEvent:
		publishCustom(bus, TypeCustomEvent, eventbus.SeverityInfo, rule, a.Message)
		return succeeded("event published")
	case ActionNotify:
		publishCustom(bus, TypeNotification, eventbus.SeverityWarning, rule, a.Message)
		return succeeded("notification published")
	default:
		return failed(fmt.Errorf("unrecognized action type %q", a.Type))
	}
}

// matchingProcessPIDs returns the PIDs of every running process whose
// name case-insensitively equals name - the same matching rule
// internal/health's process_exists checker uses, deliberately kept
// consistent so "does this process exist" and "act on this process"
// agree on what "this process" means.
func matchingProcessPIDs(ctx context.Context, sys sysCapabilities, name string) ([]int32, error) {
	procs, err := sys.ListProcesses(ctx)
	if err != nil {
		return nil, err
	}
	target := strings.ToLower(strings.TrimSpace(name))
	var pids []int32
	for _, p := range procs {
		if strings.ToLower(p.Name) == target {
			pids = append(pids, p.PID)
		}
	}
	return pids, nil
}

func actionStopProcess(ctx context.Context, sys sysCapabilities, a Action) actionResult {
	pids, err := matchingProcessPIDs(ctx, sys, a.ProcessName)
	if err != nil {
		return failed(fmt.Errorf("list processes: %w", err))
	}
	if len(pids) == 0 {
		return failed(fmt.Errorf("no running process named %q", a.ProcessName))
	}
	var errs []string
	for _, pid := range pids {
		if err := sys.TerminateProcess(ctx, pid, false); err != nil {
			errs = append(errs, fmt.Sprintf("pid %d: %v", pid, err))
		}
	}
	if len(errs) > 0 {
		return failed(fmt.Errorf("terminated %d/%d matching process(es); failures: %s", len(pids)-len(errs), len(pids), strings.Join(errs, "; ")))
	}
	return succeeded(fmt.Sprintf("terminated %d process(es) named %q", len(pids), a.ProcessName))
}

func actionRestartProcess(ctx context.Context, sys sysCapabilities, launcher processLauncher, a Action) actionResult {
	pids, err := matchingProcessPIDs(ctx, sys, a.ProcessName)
	if err != nil {
		return failed(fmt.Errorf("list processes: %w", err))
	}

	var termWarnings []string
	for _, pid := range pids {
		if err := sys.TerminateProcess(ctx, pid, false); err != nil {
			termWarnings = append(termWarnings, fmt.Sprintf("pid %d: %v", pid, err))
		}
	}

	if len(pids) > 0 {
		select {
		case <-time.After(terminationGracePeriod):
		case <-ctx.Done():
			return failed(fmt.Errorf("timed out waiting for %q to terminate before relaunching", a.ProcessName))
		}
	}

	if err := launcher.Launch(a.LaunchCommand); err != nil {
		return failed(fmt.Errorf("launch replacement process: %w", err))
	}

	msg := fmt.Sprintf("terminated %d existing instance(s) of %q and launched a replacement", len(pids), a.ProcessName)
	if len(termWarnings) > 0 {
		msg += fmt.Sprintf(" (termination warnings: %s)", strings.Join(termWarnings, "; "))
	}
	return succeeded(msg)
}

// actionRunCommand runs a.CommandArgs directly via exec.CommandContext -
// never through a shell - identical discipline to internal/health's
// checkCommand (see checkers.go there); see SECURITY.md's "Automation
// actions" section.
func actionRunCommand(ctx context.Context, a Action) actionResult {
	cmd := exec.CommandContext(ctx, a.CommandArgs[0], a.CommandArgs[1:]...)
	hidewin.Configure(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		return failed(fmt.Errorf("%s", msg))
	}
	return succeeded("exit 0")
}

func publishCustom(bus *eventbus.Bus, eventType eventbus.Type, severity eventbus.Severity, rule Rule, message string) {
	if bus == nil {
		return
	}
	payload, err := json.Marshal(customEventPayload{RuleID: rule.ID, Name: rule.Name, Message: message})
	if err != nil {
		return
	}
	bus.Publish(eventbus.Event{Type: eventType, Source: "automation.engine", Severity: severity, Payload: payload})
}
