package health

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"weloury/internal/hidewin"
	"weloury/internal/system"
)

// The following interfaces are the minimal slices of system.Provider
// each checker needs, not the full Provider - narrower than what
// internal/app's handlers depend on. This is deliberate: it makes every
// checker (and Engine itself) trivially fakeable in tests without
// implementing system.Provider's entire ~15-method surface, and it
// documents exactly what capability each check type actually touches.
// system.Provider satisfies sysCapabilities structurally; no adapter
// code is needed to pass a real Provider in.
type processLister interface {
	ListProcesses(ctx context.Context) ([]system.ProcessInfo, error)
}

type serviceInspector interface {
	ServiceDetail(ctx context.Context, name string) (system.ServiceDetail, error)
}

type diskInspector interface {
	Disks(ctx context.Context) ([]system.DiskInfo, error)
}

type memoryInspector interface {
	MemoryInfo(ctx context.Context) (system.MemoryInfo, error)
}

type cpuInspector interface {
	CPUUsage(ctx context.Context, sampleWindow time.Duration) (system.CPUUsage, error)
}

// sysCapabilities is every capability the built-in checkers collectively
// need. system.Provider satisfies it automatically.
type sysCapabilities interface {
	processLister
	serviceInspector
	diskInspector
	memoryInspector
	cpuInspector
}

// outcome is a checker's raw result before Engine applies retry policy
// and state-transition/event bookkeeping.
type outcome struct {
	state   State
	message string
}

func healthy(format string, args ...any) outcome {
	return outcome{state: StateHealthy, message: fmt.Sprintf(format, args...)}
}

func unhealthy(format string, args ...any) outcome {
	return outcome{state: StateUnhealthy, message: fmt.Sprintf(format, args...)}
}

func unknown(format string, args ...any) outcome {
	return outcome{state: StateUnknown, message: fmt.Sprintf(format, args...)}
}

// cpuUsageSampleWindow bounds how long a cpu_threshold check blocks
// waiting for a CPU utilization sample - see
// system.CPUProvider.CPUUsage's doc comment. Matches
// internal/app/methods.go's cpuUsageSampleWindow constant in spirit
// (same tradeoff, independently chosen since this package doesn't
// depend on internal/app).
const cpuUsageSampleWindow = 300 * time.Millisecond

// evaluate dispatches c to the checker for c.Type. ctx should already
// carry the per-attempt timeout (see engine.go's runWithRetries) - no
// checker here constructs its own timeout independently.
func evaluate(ctx context.Context, sys sysCapabilities, c Check) outcome {
	switch c.Type {
	case CheckProcessExists:
		return checkProcessExists(ctx, sys, c)
	case CheckServiceState:
		return checkServiceState(ctx, sys, c)
	case CheckTCPPort:
		return checkTCPPort(ctx, c)
	case CheckHTTPEndpoint:
		return checkHTTPEndpoint(ctx, c)
	case CheckCommand:
		return checkCommand(ctx, c)
	case CheckDiskThreshold:
		return checkDiskThreshold(ctx, sys, c)
	case CheckMemoryThreshold:
		return checkMemoryThreshold(ctx, sys, c)
	case CheckCPUThreshold:
		return checkCPUThreshold(ctx, sys, c)
	default:
		return unknown("unrecognized check type %q", c.Type)
	}
}

func checkProcessExists(ctx context.Context, sys processLister, c Check) outcome {
	procs, err := sys.ListProcesses(ctx)
	if err != nil {
		return unknown("list processes: %v", err)
	}
	target := strings.ToLower(strings.TrimSpace(c.Target))
	for _, p := range procs {
		if strings.ToLower(p.Name) == target {
			return healthy("process %q running (pid %d)", c.Target, p.PID)
		}
	}
	return unhealthy("no running process named %q", c.Target)
}

func checkServiceState(ctx context.Context, sys serviceInspector, c Check) outcome {
	detail, err := sys.ServiceDetail(ctx, c.Target)
	if err != nil {
		if errors.Is(err, system.ErrUnavailable) {
			return unknown("service management unavailable on this platform")
		}
		if errors.Is(err, system.ErrNotFound) {
			return unhealthy("service %q not found", c.Target)
		}
		return unknown("service detail: %v", err)
	}
	if detail.State == "running" {
		return healthy("service %q running", c.Target)
	}
	return unhealthy("service %q is %s", c.Target, detail.State)
}

func checkTCPPort(ctx context.Context, c Check) outcome {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.Target)
	if err != nil {
		return unhealthy("dial %s: %v", c.Target, err)
	}
	_ = conn.Close()
	return healthy("%s accepted a connection", c.Target)
}

func checkHTTPEndpoint(ctx context.Context, c Check) outcome {
	u, err := url.Parse(c.Target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return unknown("target must be an http(s) URL, got %q", c.Target)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Target, nil)
	if err != nil {
		return unknown("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return unhealthy("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return healthy("%s -> HTTP %d", c.Target, resp.StatusCode)
	}
	return unhealthy("%s -> HTTP %d", c.Target, resp.StatusCode)
}

// checkCommand runs c.CommandArgs directly via exec.CommandContext -
// never through a shell. This is the one check type spec section 18/11
// singles out for caution ("do not build an unrestricted
// arbitrary-command automation engine"); see SECURITY.md's "Health
// checks" section for the full reasoning. A zero exit code is HEALTHY;
// any other exit (including a timeout, which surfaces as ctx.Err()
// wrapping the process kill) is UNHEALTHY.
func checkCommand(ctx context.Context, c Check) outcome {
	if len(c.CommandArgs) == 0 {
		return unknown("command check has no command configured")
	}
	cmd := exec.CommandContext(ctx, c.CommandArgs[0], c.CommandArgs[1:]...)
	hidewin.Configure(cmd) // a user-configured command must not flash a console either
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := lastLine(string(out))
		if tail != "" {
			return unhealthy("command failed: %v (%s)", err, tail)
		}
		return unhealthy("command failed: %v", err)
	}
	return healthy("command exited 0")
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	const maxLen = 200 // a runaway command's output must not bloat Result.Message/persisted state indefinitely
	if len(line) > maxLen {
		line = line[:maxLen] + "..."
	}
	return line
}

// degradedBandFraction is how close to Threshold a resource-threshold
// check reports DEGRADED rather than HEALTHY - a deliberate heuristic
// (see Check.Threshold's doc comment) giving DEGRADED genuine meaning
// (spec section 9 lists it as a distinct state) from a single configured
// number rather than requiring two separate threshold fields the spec
// doesn't ask for.
const degradedBandFraction = 0.85

func evaluateThreshold(usedPercent, threshold float64, subject string) outcome {
	switch {
	case usedPercent >= threshold:
		return unhealthy("%s usage %.1f%% >= threshold %.1f%%", subject, usedPercent, threshold)
	case usedPercent >= threshold*degradedBandFraction:
		return outcome{state: StateDegraded, message: fmt.Sprintf("%s usage %.1f%% approaching threshold %.1f%%", subject, usedPercent, threshold)}
	default:
		return healthy("%s usage %.1f%% below threshold %.1f%%", subject, usedPercent, threshold)
	}
}

func checkDiskThreshold(ctx context.Context, sys diskInspector, c Check) outcome {
	if c.Threshold <= 0 {
		return unknown("disk threshold check has no threshold configured")
	}
	disks, err := sys.Disks(ctx)
	if err != nil {
		return unknown("list disks: %v", err)
	}
	for _, d := range disks {
		if d.MountPoint == c.Target {
			if d.TotalBytes == 0 {
				return unknown("mount %q reports zero total capacity", c.Target)
			}
			usedPercent := float64(d.UsedBytes) / float64(d.TotalBytes) * 100
			return evaluateThreshold(usedPercent, c.Threshold, fmt.Sprintf("disk %s", c.Target))
		}
	}
	return unknown("no mounted filesystem at %q", c.Target)
}

func checkMemoryThreshold(ctx context.Context, sys memoryInspector, c Check) outcome {
	if c.Threshold <= 0 {
		return unknown("memory threshold check has no threshold configured")
	}
	mem, err := sys.MemoryInfo(ctx)
	if err != nil {
		return unknown("memory info: %v", err)
	}
	if mem.TotalBytes == 0 {
		return unknown("memory info reports zero total capacity")
	}
	usedPercent := float64(mem.UsedBytes) / float64(mem.TotalBytes) * 100
	return evaluateThreshold(usedPercent, c.Threshold, "memory")
}

func checkCPUThreshold(ctx context.Context, sys cpuInspector, c Check) outcome {
	if c.Threshold <= 0 {
		return unknown("cpu threshold check has no threshold configured")
	}
	usage, err := sys.CPUUsage(ctx, cpuUsageSampleWindow)
	if err != nil {
		if errors.Is(err, system.ErrUnavailable) {
			return unknown("cpu usage sampling unavailable on this platform")
		}
		return unknown("cpu usage: %v", err)
	}
	return evaluateThreshold(usage.OverallPercent, c.Threshold, "cpu")
}
