//go:build darwin

package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Darwin and Windows: os/exec over syscalls
//
// Linux's implementation (provider_linux.go) reads /proc directly - a
// stable, well-documented text format this codebase's author could
// verify against a real machine. Neither macOS nor Windows has an
// equivalent (macOS's real equivalents are Mach/IOKit calls, typically
// reached via cgo or hand-written syscall bindings; Windows's are Win32
// API calls via syscall.NewLazyDLL). Both are meaningfully harder to get
// right without a compiler to check the work against, which this
// environment did not have - see DEVELOPMENT.md.
//
// Instead, this file shells out to long-stable, well-documented
// command-line tools (sysctl, vm_stat, df, ps) via os/exec with fixed
// argument lists - never a shell string built by concatenation, per
// SECURITY.md. The tradeoff is real (process-spawn overhead per call,
// output parsing that could in principle drift across macOS versions)
// and made deliberately: every one of these commands' output format is
// documented in Apple's own man pages and has been stable for decades,
// which is a stronger correctness guarantee, given how this codebase was
// authored, than a syscall binding nobody has compiled yet.
//
// CPUUsage is the one exception: macOS has no simple stable-format
// command for aggregate CPU utilization (the traditional `top -l 2`
// technique's output has changed shape across macOS versions in ways
// this author could not verify), so it returns ErrUnavailable rather than
// ship parsing logic with materially lower confidence than everything
// else in this file - see spec section 4's "gracefully degrade... UI
// must clearly communicate unavailable capabilities."

type darwinProvider struct{}

func newPlatformProvider() Provider { return &darwinProvider{} }

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run %s %v: %w", name, args, err)
	}
	return string(out), nil
}

func (p *darwinProvider) SystemInfo(ctx context.Context) (SystemInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return SystemInfo{}, fmt.Errorf("read hostname: %w", err)
	}

	kernelVersion, _ := runCommand(ctx, "uname", "-r")
	kernelVersion = strings.TrimSpace(kernelVersion)

	productName, _ := runCommand(ctx, "sw_vers", "-productName")
	productVersion, _ := runCommand(ctx, "sw_vers", "-productVersion")
	osVersion := strings.TrimSpace(strings.TrimSpace(productName) + " " + strings.TrimSpace(productVersion))

	bootTimeOutput, err := runCommand(ctx, "sysctl", "-n", "kern.boottime")
	if err != nil {
		return SystemInfo{}, fmt.Errorf("get kern.boottime: %w", err)
	}
	bootTime, err := parseSysctlBootTime(bootTimeOutput)
	if err != nil {
		return SystemInfo{}, fmt.Errorf("parse kern.boottime: %w", err)
	}

	return SystemInfo{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		OSVersion:     osVersion,
		KernelVersion: kernelVersion,
		Architecture:  runtime.GOARCH,
		UptimeSeconds: time.Since(bootTime).Seconds(),
		BootTime:      bootTime,
	}, nil
}

func (p *darwinProvider) CPUInfo(ctx context.Context) (CPUInfo, error) {
	model, _ := runCommand(ctx, "sysctl", "-n", "machdep.cpu.brand_string")

	logical := runtime.NumCPU() // always correct, no shell-out needed

	physical := logical
	if out, err := runCommand(ctx, "sysctl", "-n", "hw.physicalcpu"); err == nil {
		if v, perr := parseUint64Sysctl(out); perr == nil {
			physical = int(v)
		}
	}

	var mhz float64
	// hw.cpufrequency does not exist on Apple Silicon (ARM64) Macs - that
	// sysctl is Intel-specific, and its absence is expected, not an
	// error worth surfacing.
	if out, err := runCommand(ctx, "sysctl", "-n", "hw.cpufrequency"); err == nil {
		if v, perr := parseUint64Sysctl(out); perr == nil {
			mhz = float64(v) / 1_000_000
		}
	}

	return CPUInfo{
		ModelName:     strings.TrimSpace(model),
		PhysicalCores: physical,
		LogicalCores:  logical,
		ClockMHz:      mhz,
	}, nil
}

func (p *darwinProvider) CPUUsage(_ context.Context, _ time.Duration) (CPUUsage, error) {
	return CPUUsage{}, fmt.Errorf("cpu usage sampling: %w", ErrUnavailable)
}

func (p *darwinProvider) MemoryInfo(ctx context.Context) (MemoryInfo, error) {
	totalOut, err := runCommand(ctx, "sysctl", "-n", "hw.memsize")
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("get hw.memsize: %w", err)
	}
	total, err := parseUint64Sysctl(totalOut)
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("parse hw.memsize: %w", err)
	}

	vmStatOut, err := runCommand(ctx, "vm_stat")
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("run vm_stat: %w", err)
	}
	pageSize, counts, err := parseVMStat(vmStatOut)
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("parse vm_stat: %w", err)
	}
	// "Used" is approximated as active + wired: pages the kernel
	// considers in active use or unswappable. Free + inactive (inactive
	// pages are reclaimable) are treated as available, mirroring how
	// Activity Monitor's "Memory Used" figure is commonly explained.
	used := (counts["Pages active"] + counts["Pages wired down"]) * pageSize

	var swapTotal, swapUsed uint64
	if swapOut, err := runCommand(ctx, "sysctl", "-n", "vm.swapusage"); err == nil {
		swapTotal, swapUsed, _ = parseSwapUsage(swapOut)
	}

	return MemoryInfo{
		TotalBytes:     total,
		UsedBytes:      used,
		AvailableBytes: total - used,
		SwapTotalBytes: swapTotal,
		SwapUsedBytes:  swapUsed,
	}, nil
}

func (p *darwinProvider) Disks(ctx context.Context) ([]DiskInfo, error) {
	out, err := runCommand(ctx, "df", "-k")
	if err != nil {
		return nil, fmt.Errorf("run df -k: %w", err)
	}
	return parseDfOutput(out)
}

func (p *darwinProvider) NetworkInterfaces(_ context.Context) ([]NetworkInterface, error) {
	return listNetworkInterfaces()
}

// NetworkIOCounters returns ErrUnavailable for the same reason CPUUsage
// does above: `netstat -ib`, the traditional source for per-interface
// byte counters on macOS, emits one row per address family per
// interface (so naive per-interface summation double- or triple-counts)
// and its exact column layout has drifted across macOS releases. Shipping
// a parser for that with no real Mac to verify it against would be a
// materially lower-confidence guess than the rest of this file - the same
// bar CPUUsage was held to.
func (p *darwinProvider) NetworkIOCounters(_ context.Context) ([]NetIOCounters, error) {
	return nil, fmt.Errorf("network io counters: %w", ErrUnavailable)
}

// NetworkConnections returns ErrUnavailable for the same reason
// CPUUsage/NetworkIOCounters do above: macOS has no /proc filesystem, so
// the equivalent source is `netstat -anv` or `lsof -i` output, both of
// which need real-machine verification against their actual column
// layout (which, per those tools' own history, has drifted across macOS
// releases) that this author could not do without a Mac to test against
// - the same bar every other Darwin capability in this file is held to.
func (p *darwinProvider) NetworkConnections(_ context.Context) ([]NetworkConnection, error) {
	return nil, fmt.Errorf("network connections: %w", ErrUnavailable)
}

func (p *darwinProvider) ListProcesses(ctx context.Context) ([]ProcessInfo, error) {
	out, err := runCommand(ctx, "ps", "-axo", "pid,ppid,state,user,rss,comm")
	if err != nil {
		return nil, fmt.Errorf("run ps: %w", err)
	}
	rows, err := parsePSOutput(out)
	if err != nil {
		return nil, fmt.Errorf("parse ps output: %w", err)
	}

	result := make([]ProcessInfo, 0, len(rows))
	for _, r := range rows {
		result = append(result, ProcessInfo{
			PID:  r.pid,
			PPID: r.ppid,
			// ps -o comm reports the executable's base name, matching
			// Linux's comm-field behavior in ProcessInfo.Name - see
			// provider_linux.go's readProcessInfo for the same tradeoff.
			Name:        r.comm,
			User:        r.user,
			Status:      psStateLabel(r.state),
			MemoryBytes: r.rssKB * 1024,
			Threads:     0, // ps -o doesn't expose a stable thread-count column; left as 0
			// StartedAt is left zero here: ps's "lstart" column exists
			// but wasn't included in this fixed column set, to keep the
			// full-listing exec call's output simpler to parse reliably
			// across every process at once. ProcessDetail below fetches
			// it for a single process instead, where the cost of one
			// more field is negligible.
		})
	}
	return result, nil
}

func (p *darwinProvider) ProcessDetail(ctx context.Context, pid int32, _ time.Duration) (ProcessDetail, error) {
	pidStr := strconv.Itoa(int(pid))

	out, err := runCommand(ctx, "ps", "-axo", "pid,ppid,state,user,rss,pcpu,comm", "-p", pidStr)
	if err != nil {
		// macOS's ps exits non-zero when the given PID doesn't exist,
		// which is by far the most likely cause of a failure here.
		return ProcessDetail{}, fmt.Errorf("process %d: %w", pid, ErrNotFound)
	}
	row, err := parsePSDetailOutput(out)
	if err != nil {
		return ProcessDetail{}, fmt.Errorf("parse ps detail output: %w", err)
	}

	// A second, separate call for the full command line: ps's "command"
	// column can contain spaces, so it needs to be the only column
	// requested to be parsed unambiguously (the "=" suffix suppresses the
	// header, a standard BSD ps idiom for script-friendly single-column
	// output).
	var cmdLine []string
	if cmdOut, err := runCommand(ctx, "ps", "-o", "command=", "-p", pidStr); err == nil {
		trimmed := strings.TrimSpace(cmdOut)
		if trimmed != "" {
			// Best-effort: naive whitespace splitting, which mis-parses
			// an argument containing a literal space. Getting this
			// byte-exact requires reading the process's real argv via
			// the KERN_PROCARGS2 sysctl, a raw-bytes API this codebase's
			// author did not have a real Mac to verify against - see
			// this file's package comment. Good enough for display.
			cmdLine = strings.Fields(trimmed)
		}
	}

	var startedAt time.Time
	if lstartOut, err := runCommand(ctx, "ps", "-o", "lstart=", "-p", pidStr); err == nil {
		// "lstart" is in the local system's own date command format
		// (e.g. "Mon Jan  2 15:04:05 2006"), which Go's reference layout
		// happens to spell out almost verbatim.
		if t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.TrimSpace(lstartOut), time.Local); err == nil {
			startedAt = t
		}
	}

	return ProcessDetail{
		ProcessInfo: ProcessInfo{
			PID:         row.pid,
			PPID:        row.ppid,
			Name:        row.comm,
			User:        row.user,
			Status:      psStateLabel(row.state),
			StartedAt:   startedAt,
			MemoryBytes: row.rssKB * 1024,
		},
		CommandLine: cmdLine,
		CPUPercent:  row.cpuPercent,
	}, nil
}

func (p *darwinProvider) TerminateProcess(_ context.Context, pid int32, force bool) error {
	// Identical to provider_linux.go's implementation: os.FindProcess and
	// os.Process.Signal are standard-library, cross-Unix functionality
	// with no macOS-specific syscall risk, unlike the identity/memory
	// calls elsewhere in this file.
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}

	if err := proc.Signal(sig); err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("process %d: %w", pid, ErrNotFound)
		}
		return fmt.Errorf("signal process %d: %w", pid, err)
	}
	return nil
}

func (p *darwinProvider) ListServices(ctx context.Context) ([]ServiceInfo, error) {
	out, err := runCommand(ctx, "launchctl", "list")
	if err != nil {
		return nil, fmt.Errorf("run launchctl list: %w", err)
	}
	rows := parseLaunchctlList(out)

	result := make([]ServiceInfo, 0, len(rows))
	for _, r := range rows {
		pid := launchctlPID(r.pidRaw)
		state := "stopped"
		switch {
		case pid > 0:
			state = "running"
		case r.status != "0" && r.status != "-" && r.status != "":
			// A nonzero last-exit status for a job that isn't currently
			// running is launchd's way of saying "this failed" - there's
			// no separate "failed" state the way systemd has one.
			state = "failed"
		}

		result = append(result, ServiceInfo{
			Name:        r.label,
			DisplayName: r.label, // launchd has no separate display name concept
			// StartupMode: "unknown" rather than guessed - see this
			// file's package comment on why launchd's boot-enablement
			// state isn't reliably readable from `launchctl list` alone.
			StartupMode: "unknown",
			State:       state,
			PID:         pid,
		})
	}
	return result, nil
}

func (p *darwinProvider) ServiceDetail(ctx context.Context, name string) (ServiceDetail, error) {
	// Reuses the bulk listing rather than a separate `launchctl list
	// <label>` call: that form's output is a NeXTSTEP/OpenStep-style
	// property list dump, materially more complex to parse correctly
	// than the tabular form and not something this author could verify
	// against a real Mac - see this file's package comment. The
	// consequence is a thinner ServiceDetail on Darwin (no description,
	// no dependencies) than Linux provides.
	services, err := p.ListServices(ctx)
	if err != nil {
		return ServiceDetail{}, err
	}
	for _, s := range services {
		if s.Name == name {
			return ServiceDetail{ServiceInfo: s}, nil
		}
	}
	return ServiceDetail{}, fmt.Errorf("service %s: %w", name, ErrNotFound)
}

func (p *darwinProvider) StartService(ctx context.Context, name string) error {
	if _, err := runCommand(ctx, "launchctl", "start", name); err != nil {
		return fmt.Errorf("service %s: %w", name, err)
	}
	return nil
}

func (p *darwinProvider) StopService(ctx context.Context, name string) error {
	if _, err := runCommand(ctx, "launchctl", "stop", name); err != nil {
		return fmt.Errorf("service %s: %w", name, err)
	}
	return nil
}

func (p *darwinProvider) RestartService(ctx context.Context, name string) error {
	// launchctl has no dedicated restart subcommand for an already-loaded
	// job; stop-then-start is the standard workaround for this case.
	if err := p.StopService(ctx, name); err != nil {
		return err
	}
	return p.StartService(ctx, name)
}

func (p *darwinProvider) SetServiceEnabled(_ context.Context, name string, _ bool) error {
	// Reliably toggling boot-time enablement requires the job's plist
	// path, which `launchctl list` does not report, and modern
	// launchctl's `enable`/`disable` subcommands need a domain-target
	// (system/<label>, gui/<uid>/<label>, ...) this data doesn't
	// disambiguate either - see this file's package comment. Honestly
	// unavailable rather than guessed.
	return fmt.Errorf("set enabled for %s: %w", name, ErrUnavailable)
}
