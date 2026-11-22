//go:build linux

package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type linuxProvider struct{}

func newPlatformProvider() Provider { return &linuxProvider{} }

func (p *linuxProvider) SystemInfo(_ context.Context) (SystemInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return SystemInfo{}, fmt.Errorf("read hostname: %w", err)
	}

	uptimeContent, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return SystemInfo{}, fmt.Errorf("read /proc/uptime: %w", err)
	}
	uptimeSeconds, bootTime, err := parseUptime(string(uptimeContent), time.Now())
	if err != nil {
		return SystemInfo{}, fmt.Errorf("parse /proc/uptime: %w", err)
	}

	kernelVersion := ""
	if content, err := os.ReadFile("/proc/version"); err == nil {
		kernelVersion = parseKernelVersion(string(content))
	}

	osVersion := ""
	if content, err := os.ReadFile("/etc/os-release"); err == nil {
		osVersion = parseOSRelease(string(content))
	}

	return SystemInfo{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		OSVersion:     osVersion,
		KernelVersion: kernelVersion,
		Architecture:  runtime.GOARCH,
		UptimeSeconds: uptimeSeconds,
		BootTime:      bootTime,
	}, nil
}

func (p *linuxProvider) CPUInfo(_ context.Context) (CPUInfo, error) {
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return CPUInfo{}, fmt.Errorf("read /proc/cpuinfo: %w", err)
	}
	model, logical, physical, mhz := parseCPUInfo(string(content))
	return CPUInfo{
		ModelName:     model,
		PhysicalCores: physical,
		LogicalCores:  logical,
		ClockMHz:      mhz,
	}, nil
}

func (p *linuxProvider) CPUUsage(ctx context.Context, sampleWindow time.Duration) (CPUUsage, error) {
	first, err := readCPUStat()
	if err != nil {
		return CPUUsage{}, err
	}

	select {
	case <-ctx.Done():
		return CPUUsage{}, ctx.Err()
	case <-time.After(sampleWindow):
	}

	second, err := readCPUStat()
	if err != nil {
		return CPUUsage{}, err
	}

	overall := cpuUsagePercent(first.aggregate, second.aggregate)

	perCore := make([]float64, 0, len(second.perCore))
	firstByName := make(map[string]cpuSample, len(first.perCore))
	for _, s := range first.perCore {
		firstByName[s.name] = s
	}
	for _, s := range second.perCore {
		if prev, ok := firstByName[s.name]; ok {
			perCore = append(perCore, cpuUsagePercent(prev, s))
		}
	}

	return CPUUsage{OverallPercent: overall, PerCorePercent: perCore}, nil
}

type statSnapshot struct {
	aggregate cpuSample
	perCore   []cpuSample
}

func readCPUStat() (statSnapshot, error) {
	content, err := os.ReadFile("/proc/stat")
	if err != nil {
		return statSnapshot{}, fmt.Errorf("read /proc/stat: %w", err)
	}
	aggregate, perCore, err := parseProcStat(string(content))
	if err != nil {
		return statSnapshot{}, fmt.Errorf("parse /proc/stat: %w", err)
	}
	return statSnapshot{aggregate: aggregate, perCore: perCore}, nil
}

func (p *linuxProvider) MemoryInfo(_ context.Context) (MemoryInfo, error) {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	return parseMemInfo(string(content))
}

func (p *linuxProvider) Disks(_ context.Context) ([]DiskInfo, error) {
	content, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("read /proc/mounts: %w", err)
	}
	mounts, err := parseMounts(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse /proc/mounts: %w", err)
	}

	out := make([]DiskInfo, 0, len(mounts))
	for _, m := range mounts {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(m.mountPoint, &stat); err != nil {
			// A mount that can't be statfs'd (permission, a stale NFS
			// handle, ...) is skipped rather than failing the whole
			// listing - the same principle as a single network
			// interface failing to report addresses in network.go.
			continue
		}

		blockSize := uint64(stat.Bsize) // #nosec G115 -- Bsize is always small and positive in practice
		total := stat.Blocks * blockSize
		availableToUser := stat.Bavail * blockSize
		freeIncludingReserved := stat.Bfree * blockSize
		used := total - freeIncludingReserved

		out = append(out, DiskInfo{
			Device:     m.device,
			MountPoint: m.mountPoint,
			Filesystem: m.filesystem,
			TotalBytes: total,
			FreeBytes:  availableToUser,
			UsedBytes:  used,
		})
	}
	return out, nil
}

func (p *linuxProvider) NetworkInterfaces(_ context.Context) ([]NetworkInterface, error) {
	return listNetworkInterfaces()
}

// NetworkIOCounters reads /proc/net/dev directly - the same verified
// source and parser (parseNetDev, linux_parse.go) as everything else in
// this file, tested against a fixture captured from a real machine (see
// linux_parse_test.go).
func (p *linuxProvider) NetworkIOCounters(_ context.Context) ([]NetIOCounters, error) {
	content, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return nil, fmt.Errorf("read /proc/net/dev: %w", err)
	}
	samples, err := parseNetDev(string(content))
	if err != nil {
		return nil, err
	}
	out := make([]NetIOCounters, 0, len(samples))
	for _, s := range samples {
		out = append(out, NetIOCounters{Name: s.name, RxBytes: s.rxBytes, TxBytes: s.txBytes})
	}
	return out, nil
}

// netProtocolSources lists which /proc/net/* files NetworkConnections
// reads and the protocol label each corresponds to. IPv6 files
// (tcp6/udp6) simply don't exist on a machine without IPv6 configured -
// os.IsNotExist on those specific paths is treated as "no rows from this
// source," not an error, since the absence of IPv6 support is a wholly
// unremarkable, common configuration, not something wrong to surface as
// a failure.
var netProtocolSources = []struct{ path, protocol string }{
	{"/proc/net/tcp", "tcp"},
	{"/proc/net/tcp6", "tcp6"},
	{"/proc/net/udp", "udp"},
	{"/proc/net/udp6", "udp6"},
}

// NetworkConnections reads the kernel's own socket tables
// (/proc/net/{tcp,tcp6,udp,udp6}, parsed by parseProcNet) and attributes
// each socket to an owning process by cross-referencing its inode
// against every process's open file descriptors
// (/proc/*/fd/*, each a symlink named "socket:[INODE]" for a socket fd -
// see `man 5 proc`'s "/proc/[pid]/fd" section). That second scan is
// necessarily best-effort and asymmetric: Weloury core can read its own
// process's fd directory and any process owned by the same user, but
// `/proc/<pid>/fd` for a process owned by a *different* user returns
// EACCES without elevated privileges - the identical permission boundary
// `ps`/`lsof` hit as an unprivileged user. A socket whose owning process
// couldn't be determined this way is still returned, just with PID/
// ProcessName left zero/empty, exactly as NetworkConnection's doc
// comment describes; this is normal operation for e.g. another user's
// or root's sockets, not a partial failure of the call.
func (p *linuxProvider) NetworkConnections(_ context.Context) ([]NetworkConnection, error) {
	var rows []procNetEntry
	for _, src := range netProtocolSources {
		content, err := os.ReadFile(src.path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", src.path, err)
		}
		entries, err := parseProcNet(string(content), src.protocol)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", src.path, err)
		}
		rows = append(rows, entries...)
	}

	inodeToPID := socketInodeOwners()

	out := make([]NetworkConnection, 0, len(rows))
	for _, r := range rows {
		conn := NetworkConnection{
			Protocol: r.protocol, LocalAddress: r.localAddress, LocalPort: r.localPort,
			RemoteAddress: r.remoteAddress, RemotePort: r.remotePort, State: r.state,
		}
		if pid, ok := inodeToPID[r.inode]; ok {
			conn.PID = pid
			if name, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
				conn.ProcessName = strings.TrimSpace(string(name))
			}
		}
		out = append(out, conn)
	}
	return out, nil
}

// socketInodeOwners scans every process's /proc/<pid>/fd directory and
// returns a map from socket inode number to owning PID. Errors reading
// any individual process's fd directory (permission denied for another
// user's process, or the process exited mid-scan - both routine) are
// silently skipped rather than failing the whole call - see
// NetworkConnections' doc comment.
func socketInodeOwners() map[uint64]int32 {
	owners := make(map[uint64]int32)

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return owners
	}
	for _, e := range entries {
		pid, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil {
			continue // not a PID directory (e.g. "self", "net", "cpuinfo")
		}
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(fdDir + "/" + fd.Name())
			if err != nil {
				continue
			}
			if !strings.HasPrefix(link, "socket:[") {
				continue
			}
			inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), 10, 64)
			if err != nil {
				continue
			}
			owners[inode] = int32(pid)
		}
	}
	return owners
}

func (p *linuxProvider) ListProcesses(_ context.Context) ([]ProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}

	uptimeContent, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return nil, fmt.Errorf("read /proc/uptime: %w", err)
	}
	_, bootTime, err := parseUptime(string(uptimeContent), time.Now())
	if err != nil {
		return nil, fmt.Errorf("parse /proc/uptime: %w", err)
	}

	pageSize := uint64(os.Getpagesize())
	userCache := make(map[string]string)

	out := make([]ProcessInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue // not a PID directory
		}

		info, ok := readProcessInfo(entry.Name(), bootTime, pageSize, userCache)
		if !ok {
			// The process exited between os.ReadDir and reading its
			// files (extremely common - process listing is inherently a
			// race against reality) or we lack permission to read it.
			// Either way, skip it rather than failing the whole listing.
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

func readProcessInfo(pidStr string, bootTime time.Time, pageSize uint64, userCache map[string]string) (ProcessInfo, bool) {
	statContent, err := os.ReadFile("/proc/" + pidStr + "/stat")
	if err != nil {
		return ProcessInfo{}, false
	}
	stat, err := parseProcPidStat(string(statContent))
	if err != nil {
		return ProcessInfo{}, false
	}

	var uidStr string
	threads := 1
	if statusContent, err := os.ReadFile("/proc/" + pidStr + "/status"); err == nil {
		if uid, t, ok := parseProcStatus(string(statusContent)); ok {
			uidStr, threads = uid, t
		}
	}

	username := resolveUsername(uidStr, userCache)

	// Divide by clockTicksPerSecond before converting to a Duration
	// (rather than multiplying by time.Second first and dividing after)
	// to avoid an intermediate overflow of the int64 nanosecond range for
	// a machine with a very long uptime; this only costs sub-second
	// precision on startedAt, which is not needed here.
	startedAt := bootTime.Add(time.Duration(stat.starttimeTicks/clockTicksPerSecond) * time.Second)

	return ProcessInfo{
		PID:  stat.pid,
		PPID: stat.ppid,
		// comm is truncated to 15 characters by the kernel in
		// /proc/[pid]/stat; the full command line (Phase 3) comes from
		// /proc/[pid]/cmdline instead, which has no such limit.
		Name:        stat.comm,
		User:        username,
		Status:      processStateLabel(stat.state),
		StartedAt:   startedAt,
		MemoryBytes: stat.rssPages * pageSize,
		Threads:     threads,
	}, true
}

func resolveUsername(uid string, cache map[string]string) string {
	if uid == "" {
		return ""
	}
	if name, ok := cache[uid]; ok {
		return name
	}
	name := ""
	if u, err := user.LookupId(uid); err == nil {
		name = u.Username
	}
	cache[uid] = name
	return name
}

// defaultProcessSampleWindow is used when the caller passes sampleWindow
// <= 0 to ProcessDetail, matching CPUUsage's own short-window philosophy
// (spec section 5: "do not constantly poll... at extremely high
// frequency", balanced against a request/response caller wanting an
// answer that feels immediate).
const defaultProcessSampleWindow = 200 * time.Millisecond

func (p *linuxProvider) ProcessDetail(ctx context.Context, pid int32, sampleWindow time.Duration) (ProcessDetail, error) {
	if sampleWindow <= 0 {
		sampleWindow = defaultProcessSampleWindow
	}
	pidStr := strconv.Itoa(int(pid))

	uptimeContent, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return ProcessDetail{}, fmt.Errorf("read /proc/uptime: %w", err)
	}
	_, bootTime, err := parseUptime(string(uptimeContent), time.Now())
	if err != nil {
		return ProcessDetail{}, fmt.Errorf("parse /proc/uptime: %w", err)
	}

	info, ok := readProcessInfo(pidStr, bootTime, uint64(os.Getpagesize()), make(map[string]string))
	if !ok {
		return ProcessDetail{}, fmt.Errorf("process %d: %w", pid, ErrNotFound)
	}

	var cmdLine []string
	if content, err := os.ReadFile("/proc/" + pidStr + "/cmdline"); err == nil {
		cmdLine = parseCmdline(content)
	}
	// Best-effort: a process we don't own, or one that has since exited,
	// simply yields an empty Executable rather than failing the whole
	// detail request over one optional field.
	executable, _ := os.Readlink("/proc/" + pidStr + "/exe")

	cpuPercent, err := processCPUPercent(ctx, pidStr, sampleWindow)
	if err != nil {
		return ProcessDetail{}, err
	}

	return ProcessDetail{
		ProcessInfo: info,
		Executable:  executable,
		CommandLine: cmdLine,
		CPUPercent:  cpuPercent,
	}, nil
}

// processCPUPercent samples one process's utime+stime twice, sampleWindow
// apart, the same two-reading methodology as the aggregate CPUUsage. The
// result follows `top`/`ps` convention: a process fully occupying two
// cores reports ~200%, not normalized to total system capacity.
//
// A process that exits between the two samples (routine - process
// listing and inspection are inherently a race against reality) yields
// 0%, not an error: it is, definitionally, no longer consuming any CPU.
// Only ctx cancellation is treated as an error.
func processCPUPercent(ctx context.Context, pidStr string, sampleWindow time.Duration) (float64, error) {
	first, ok := readPidTimes(pidStr)
	if !ok {
		return 0, nil
	}

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(sampleWindow):
	}

	second, ok := readPidTimes(pidStr)
	if !ok {
		return 0, nil
	}

	deltaTicks := (second.utimeTicks + second.stimeTicks) - (first.utimeTicks + first.stimeTicks)
	if second.utimeTicks+second.stimeTicks < first.utimeTicks+first.stimeTicks {
		deltaTicks = 0 // defensive guard; a real counter decrease should never happen
	}
	seconds := float64(deltaTicks) / clockTicksPerSecond
	return (seconds / sampleWindow.Seconds()) * 100, nil
}

func readPidTimes(pidStr string) (procPidStat, bool) {
	content, err := os.ReadFile("/proc/" + pidStr + "/stat")
	if err != nil {
		return procPidStat{}, false
	}
	stat, err := parseProcPidStat(string(content))
	if err != nil {
		return procPidStat{}, false
	}
	return stat, true
}

func (p *linuxProvider) TerminateProcess(_ context.Context, pid int32, force bool) error {
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		// On Unix, os.FindProcess essentially always succeeds regardless
		// of whether the PID currently exists; a non-nil error here is
		// unusual, but handled rather than assumed impossible.
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
