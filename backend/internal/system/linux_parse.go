//go:build linux

package system

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// clockTicksPerSecond is the kernel's USER_HZ, used to convert the
// utime/stime/starttime fields in /proc/[pid]/stat (reported in clock
// ticks) into seconds. The true value is available via
// sysconf(_SC_CLK_TCK), but every mainstream Linux distribution on every
// architecture Weloury targets has used 100 since the early 2000s;
// hardcoding it avoids a cgo dependency for a value this stable. If a
// platform is ever found where this doesn't hold, golang.org/x/sys/unix
// exposes Sysconf.
const clockTicksPerSecond = 100

// cpuSample is one snapshot of /proc/stat's cumulative jiffie counters,
// either the aggregate "cpu" line or one "cpuN" line.
type cpuSample struct {
	name                                                  string // "cpu" or "cpu0", "cpu1", ...
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (s cpuSample) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

// idleTicks follows the common convention (used by mpstat/top) of
// treating both idle and iowait as non-busy time.
func (s cpuSample) idleTicks() uint64 {
	return s.idle + s.iowait
}

// parseProcStat parses the full content of /proc/stat, returning the
// aggregate "cpu" sample and one sample per logical core ("cpu0", "cpu1",
// ...), in core order. Verified against this format (from a real
// /proc/stat): "cpu  359 0 841 60479 32 0 11 36 0 0" - fields after the
// label are: user nice system idle iowait irq softirq steal guest
// guest_nice. Only the first 8 are used; guest/guest_nice are already
// included in user/nice by the kernel's accounting and double-counting
// them would overstate total ticks.
func parseProcStat(content string) (aggregate cpuSample, perCore []cpuSample, err error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		sample, parseErr := parseCPUFields(fields)
		if parseErr != nil {
			return cpuSample{}, nil, fmt.Errorf("parse %q: %w", line, parseErr)
		}
		if sample.name == "cpu" {
			aggregate = sample
		} else {
			perCore = append(perCore, sample)
		}
	}
	if err := scanner.Err(); err != nil {
		return cpuSample{}, nil, fmt.Errorf("scan /proc/stat: %w", err)
	}
	if aggregate.name == "" {
		return cpuSample{}, nil, fmt.Errorf("no aggregate \"cpu\" line found")
	}
	return aggregate, perCore, nil
}

func parseCPUFields(fields []string) (cpuSample, error) {
	vals := make([]uint64, 8)
	for i := 0; i < 8; i++ {
		v, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			return cpuSample{}, fmt.Errorf("field %d (%q): %w", i+1, fields[i+1], err)
		}
		vals[i] = v
	}
	return cpuSample{
		name: fields[0],
		user: vals[0], nice: vals[1], system: vals[2], idle: vals[3],
		iowait: vals[4], irq: vals[5], softirq: vals[6], steal: vals[7],
	}, nil
}

// cpuUsagePercent computes utilization between two samples of the same
// CPU (aggregate-vs-aggregate or coreN-vs-coreN). Returns 0 if no time
// elapsed between samples (guards a division by zero rather than
// returning NaN/Inf to a caller that will JSON-encode this).
func cpuUsagePercent(prev, curr cpuSample) float64 {
	totalDelta := curr.total() - prev.total()
	if totalDelta == 0 {
		return 0
	}
	idleDelta := curr.idleTicks() - prev.idleTicks()
	busyDelta := totalDelta - idleDelta
	return (float64(busyDelta) / float64(totalDelta)) * 100
}

// parseMemInfo parses /proc/meminfo. Verified format: "MemTotal:
// 4093756 kB" - key, colon, whitespace-padded number, unit. Values are
// always reported in kB by the kernel regardless of the padding shown in
// the raw file.
func parseMemInfo(content string) (MemoryInfo, error) {
	fields := make(map[string]uint64)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := line[:colon]
		rest := strings.Fields(line[colon+1:])
		if len(rest) == 0 {
			continue
		}
		v, err := strconv.ParseUint(rest[0], 10, 64)
		if err != nil {
			continue // unparseable fields (rare, non-numeric entries) are skipped, not fatal
		}
		fields[key] = v * 1024 // kB -> bytes
	}
	if err := scanner.Err(); err != nil {
		return MemoryInfo{}, fmt.Errorf("scan /proc/meminfo: %w", err)
	}

	total, ok := fields["MemTotal"]
	if !ok {
		return MemoryInfo{}, fmt.Errorf("MemTotal not found in /proc/meminfo")
	}
	// MemAvailable (kernel-estimated, accounts for reclaimable cache) is a
	// far better "how much can I actually use" number than MemFree, and
	// has been present since Linux 3.14 (2014) - old enough that Weloury
	// doesn't fall back to a MemFree-based estimate for its absence.
	available := fields["MemAvailable"]
	swapTotal := fields["SwapTotal"]
	swapFree := fields["SwapFree"]

	var used uint64
	if available <= total {
		used = total - available
	}
	var swapUsed uint64
	if swapFree <= swapTotal {
		swapUsed = swapTotal - swapFree
	}

	return MemoryInfo{
		TotalBytes:     total,
		UsedBytes:      used,
		AvailableBytes: available,
		SwapTotalBytes: swapTotal,
		SwapUsedBytes:  swapUsed,
	}, nil
}

// parseCPUInfo parses /proc/cpuinfo, returning the model name from the
// first processor block, the logical core count (number of "processor"
// blocks), and the physical core count (number of distinct
// physical-id/core-id pairs - falling back to the logical count if that
// topology information isn't present, e.g. inside some containers/VMs).
func parseCPUInfo(content string) (modelName string, logicalCores, physicalCores int, clockMHz float64) {
	type coreKey struct{ physicalID, coreID string }
	seen := make(map[coreKey]struct{})

	var currentPhysID, currentCoreID string
	for _, line := range strings.Split(content, "\n") {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])

		switch key {
		case "processor":
			logicalCores++
		case "model name":
			if modelName == "" {
				modelName = value
			}
		case "cpu MHz":
			if clockMHz == 0 {
				if v, err := strconv.ParseFloat(value, 64); err == nil {
					clockMHz = v
				}
			}
		case "physical id":
			currentPhysID = value
		case "core id":
			currentCoreID = value
			seen[coreKey{currentPhysID, currentCoreID}] = struct{}{}
		}
	}

	physicalCores = len(seen)
	if physicalCores == 0 {
		physicalCores = logicalCores
	}
	return modelName, logicalCores, physicalCores, clockMHz
}

// parseUptime parses /proc/uptime ("619.28 604.79" - uptime seconds,
// idle seconds summed across cores) and derives a boot time from it
// relative to now.
func parseUptime(content string, now time.Time) (uptimeSeconds float64, bootTime time.Time, err error) {
	fields := strings.Fields(content)
	if len(fields) == 0 {
		return 0, time.Time{}, fmt.Errorf("empty /proc/uptime content")
	}
	uptimeSeconds, err = strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parse uptime seconds: %w", err)
	}
	bootTime = now.Add(-time.Duration(uptimeSeconds * float64(time.Second)))
	return uptimeSeconds, bootTime, nil
}

// parseKernelVersion extracts the kernel release from /proc/version's
// content, e.g. "Linux version 6.18.5-fc-v20 (builder@...) ..." ->
// "6.18.5-fc-v20".
func parseKernelVersion(content string) string {
	fields := strings.Fields(content)
	for i, f := range fields {
		if f == "version" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// parseOSRelease extracts PRETTY_NAME from /etc/os-release-formatted
// content (KEY=VALUE lines, values optionally double-quoted - the
// freedesktop.org os-release format).
func parseOSRelease(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "PRETTY_NAME=") {
			continue
		}
		value := strings.TrimPrefix(line, "PRETTY_NAME=")
		return strings.Trim(value, `"`)
	}
	return ""
}

// procPidStat is the subset of /proc/[pid]/stat fields Weloury uses.
type procPidStat struct {
	pid, ppid      int32
	comm           string
	state          string
	utimeTicks     uint64
	stimeTicks     uint64
	starttimeTicks uint64
	vsizeBytes     uint64
	rssPages       uint64
}

// parseProcPidStat parses one /proc/[pid]/stat line. The tricky part,
// verified against a real sample ("740 (cat) R 737 737 0 0 -1 4194304 60
// 0 0 0 0 0 0 0 20 0 1 0 62777 2916352 367 ..."), is that the comm field
// (2nd field) is parenthesized and may itself contain spaces or
// parentheses, so it cannot be split on whitespace naively - this finds
// the outermost parens by taking the first "(" and the *last* ")" in the
// line, which is safe because every field after comm is numeric or a
// single state letter, none of which contain ")".
//
// Field positions below are 0-indexed into the whitespace-split remainder
// after the closing paren, cross-checked against `man proc`'s 1-indexed
// field list (field 3 = state = remainder index 0, so remainder index N
// = field N+3):
//
//	remainder[0]  = state          (field 3)
//	remainder[1]  = ppid           (field 4)
//	remainder[11] = utime          (field 14)
//	remainder[12] = stime          (field 15)
//	remainder[19] = starttime      (field 22)
//	remainder[20] = vsize          (field 23)
//	remainder[21] = rss (pages)    (field 24)
func parseProcPidStat(content string) (procPidStat, error) {
	openParen := strings.IndexByte(content, '(')
	closeParen := strings.LastIndexByte(content, ')')
	if openParen < 0 || closeParen < 0 || closeParen < openParen {
		return procPidStat{}, fmt.Errorf("malformed stat line: no comm parentheses found")
	}

	pidStr := strings.TrimSpace(content[:openParen])
	pid, err := strconv.ParseInt(pidStr, 10, 32)
	if err != nil {
		return procPidStat{}, fmt.Errorf("parse pid %q: %w", pidStr, err)
	}
	comm := content[openParen+1 : closeParen]

	remainder := strings.Fields(content[closeParen+1:])
	if len(remainder) < 22 {
		return procPidStat{}, fmt.Errorf("stat line has %d fields after comm, want at least 22", len(remainder))
	}

	ppid, err := strconv.ParseInt(remainder[1], 10, 32)
	if err != nil {
		return procPidStat{}, fmt.Errorf("parse ppid %q: %w", remainder[1], err)
	}
	utime, err := strconv.ParseUint(remainder[11], 10, 64)
	if err != nil {
		return procPidStat{}, fmt.Errorf("parse utime %q: %w", remainder[11], err)
	}
	stime, err := strconv.ParseUint(remainder[12], 10, 64)
	if err != nil {
		return procPidStat{}, fmt.Errorf("parse stime %q: %w", remainder[12], err)
	}
	starttime, err := strconv.ParseUint(remainder[19], 10, 64)
	if err != nil {
		return procPidStat{}, fmt.Errorf("parse starttime %q: %w", remainder[19], err)
	}
	vsize, err := strconv.ParseUint(remainder[20], 10, 64)
	if err != nil {
		return procPidStat{}, fmt.Errorf("parse vsize %q: %w", remainder[20], err)
	}
	rss, err := strconv.ParseUint(remainder[21], 10, 64)
	if err != nil {
		return procPidStat{}, fmt.Errorf("parse rss %q: %w", remainder[21], err)
	}

	return procPidStat{
		pid:            int32(pid),
		ppid:           int32(ppid),
		comm:           comm,
		state:          remainder[0],
		utimeTicks:     utime,
		stimeTicks:     stime,
		starttimeTicks: starttime,
		vsizeBytes:     vsize,
		rssPages:       rss,
	}, nil
}

// parseCmdline parses /proc/[pid]/cmdline's content: NUL-separated
// arguments with a trailing NUL (sometimes more than one, or none, for a
// process with an empty argv - e.g. some kernel threads). Returns nil for
// empty content rather than a slice containing one empty string.
func parseCmdline(content []byte) []string {
	trimmed := strings.TrimRight(string(content), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}

// processStateLabel translates /proc/[pid]/stat's single-letter state
// code into Weloury's small, documented status vocabulary (see
// ProcessInfo.Status's doc comment). Codes per `man proc`.
func processStateLabel(code string) string {
	switch code {
	case "R":
		return "running"
	case "S":
		return "sleeping"
	case "D":
		return "disk_sleep"
	case "T", "t":
		return "stopped"
	case "Z":
		return "zombie"
	case "X":
		return "dead"
	case "I":
		return "idle"
	default:
		return "unknown"
	}
}

// parseProcStatus extracts the real UID and thread count from
// /proc/[pid]/status content. Verified format: "Uid:\treal\teffective\t
// saved\tfilesystem" and "Threads:\tN".
func parseProcStatus(content string) (uid string, threads int, ok bool) {
	threads = 1 // status always describes at least the main thread
	for _, line := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(line, "Uid:"):
			fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
			if len(fields) > 0 {
				uid = fields[0]
				ok = true
			}
		case strings.HasPrefix(line, "Threads:"):
			if v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Threads:"))); err == nil {
				threads = v
			}
		}
	}
	return uid, threads, ok
}

// netDevSample is one interface's counters from /proc/net/dev.
type netDevSample struct {
	name    string
	rxBytes uint64
	txBytes uint64
}

// parseNetDev parses /proc/net/dev. Verified format has a two-line
// header followed by "iface: rx... tx..." rows where the interface name
// is directly adjacent to the colon with no guaranteed space (e.g.
// "  eth0:  156102     707 ..."), so this splits on ':' before
// whitespace-splitting each side.
func parseNetDev(content string) ([]netDevSample, error) {
	var out []netDevSample
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue // header lines
		}
		line := scanner.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		rx, errRx := strconv.ParseUint(fields[0], 10, 64)
		tx, errTx := strconv.ParseUint(fields[8], 10, 64)
		if errRx != nil || errTx != nil {
			continue
		}
		out = append(out, netDevSample{name: name, rxBytes: rx, txBytes: tx})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan /proc/net/dev: %w", err)
	}
	return out, nil
}

// mountEntry is one parsed line of /proc/mounts.
type mountEntry struct {
	device     string
	mountPoint string
	filesystem string
}

// pseudoFilesystems is excluded from Disks() results - virtual
// filesystems that don't represent real storage a user would think of as
// "a disk". This is a heuristic allowlist-by-exclusion, not exhaustive;
// refining it based on real-world mount tables is expected as this
// feature gets used, not a one-time perfect list.
var pseudoFilesystems = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true,
	"tmpfs": true, "cgroup": true, "cgroup2": true, "pstore": true,
	"bpf": true, "tracefs": true, "debugfs": true, "mqueue": true,
	"hugetlbfs": true, "securityfs": true, "configfs": true,
	"fusectl": true, "autofs": true, "binfmt_misc": true, "rpc_pipefs": true,
}

// parseMounts parses /proc/mounts, filtering out pseudoFilesystems.
// Format verified: "device mountpoint fstype options dump pass" - a
// straightforward whitespace-split table.
func parseMounts(content string) ([]mountEntry, error) {
	var out []mountEntry
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		device, mountPoint, fstype := fields[0], fields[1], fields[2]
		if pseudoFilesystems[fstype] {
			continue
		}
		out = append(out, mountEntry{device: device, mountPoint: mountPoint, filesystem: fstype})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan /proc/mounts: %w", err)
	}
	return out, nil
}

// procNetEntry is one parsed row of /proc/net/{tcp,tcp6,udp,udp6} - the
// kernel's own socket table, before any process attribution (which
// needs a separate /proc/*/fd inode scan - see provider_linux.go's
// NetworkConnections).
type procNetEntry struct {
	protocol      string
	localAddress  string
	localPort     int
	remoteAddress string
	remotePort    int
	state         string
	inode         uint64
}

// tcpStateNames maps /proc/net/tcp's hex `st` field to the names any
// admin reading `netstat`/`ss` output would recognize - verbatim from
// the kernel's include/net/tcp_states.h (TCP_ESTABLISHED=1 through
// TCP_CLOSING=11), a stable, long-documented ABI, not something that
// varies by kernel version or architecture.
var tcpStateNames = map[string]string{
	"01": "ESTABLISHED",
	"02": "SYN_SENT",
	"03": "SYN_RECV",
	"04": "FIN_WAIT1",
	"05": "FIN_WAIT2",
	"06": "TIME_WAIT",
	"07": "CLOSE",
	"08": "CLOSE_WAIT",
	"09": "LAST_ACK",
	"0A": "LISTEN",
	"0B": "CLOSING",
}

// parseProcNet parses /proc/net/tcp, /proc/net/tcp6, /proc/net/udp, or
// /proc/net/udp6 - all four share one column layout (a one-line header
// then whitespace-separated fields; see `man 5 proc`'s "/proc/net/tcp"
// section): `sl local_address rem_address st ... uid timeout inode ...`.
// protocol selects address decoding (parseHexIPv4 vs parseHexIPv6 - IPv4
// files use 8 hex chars per address, IPv6 files 32) and whether `state`
// is populated at all (UDP is connectionless; its `st` field exists in
// the file but carries no meaningful connection state to surface - see
// NetworkConnection.State's doc comment).
func parseProcNet(content, protocol string) ([]procNetEntry, error) {
	isIPv6 := protocol == "tcp6" || protocol == "udp6"
	isUDP := protocol == "udp" || protocol == "udp6"

	var out []procNetEntry
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum == 1 {
			continue // header
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		localAddr, localPort, err := parseHexAddrPort(fields[1], isIPv6)
		if err != nil {
			continue
		}
		remoteAddr, remotePort, err := parseHexAddrPort(fields[2], isIPv6)
		if err != nil {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}

		state := ""
		if !isUDP {
			state = tcpStateNames[strings.ToUpper(fields[3])]
		}

		out = append(out, procNetEntry{
			protocol: protocol, localAddress: localAddr, localPort: localPort,
			remoteAddress: remoteAddr, remotePort: remotePort, state: state, inode: inode,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan /proc/net/%s: %w", protocol, err)
	}
	return out, nil
}

// parseHexAddrPort decodes one "ADDR:PORT" field from /proc/net/{tcp,udp}*
// (e.g. "0100007F:0277") into a textual address and decimal port.
func parseHexAddrPort(field string, isIPv6 bool) (address string, port int, err error) {
	parts := strings.SplitN(field, ":", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("malformed address:port field %q", field)
	}
	if isIPv6 {
		address, err = parseHexIPv6(parts[0])
	} else {
		address, err = parseHexIPv4(parts[0])
	}
	if err != nil {
		return "", 0, err
	}
	portVal, err := strconv.ParseUint(parts[1], 16, 32)
	if err != nil {
		return "", 0, fmt.Errorf("malformed port %q: %w", parts[1], err)
	}
	return address, int(portVal), nil
}

// parseHexIPv4 decodes /proc/net/tcp's 8-hex-character address encoding.
// The kernel writes each address as a native-endian uint32, which on
// every architecture Weloury targets (all little-endian: amd64, arm64) means
// the byte order is reversed relative to normal dotted-quad notation -
// "0100007F" is 127.0.0.1, not 1.0.0.127. This is a well-documented
// kernel quirk (see `man 5 proc`), not a guess: verified against this
// sandbox's own /proc/net/tcp in linux_parse_test.go's fixture, which
// contains a real 127.0.0.1 listening socket captured exactly this way.
func parseHexIPv4(s string) (string, error) {
	if len(s) != 8 {
		return "", fmt.Errorf("expected 8 hex chars for an IPv4 address, got %q", s)
	}
	var b [4]byte
	for i := range b {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return "", fmt.Errorf("decode IPv4 hex %q: %w", s, err)
		}
		b[i] = byte(v)
	}
	return fmt.Sprintf("%d.%d.%d.%d", b[3], b[2], b[1], b[0]), nil
}

// parseHexIPv6 decodes /proc/net/tcp6's 32-hex-character address
// encoding - the same little-endian-per-32-bit-word layout as
// parseHexIPv4, applied four times (IPv6 is 128 bits = four uint32
// words).
func parseHexIPv6(s string) (string, error) {
	if len(s) != 32 {
		return "", fmt.Errorf("expected 32 hex chars for an IPv6 address, got %q", s)
	}
	raw := make([]byte, 16)
	for i := range raw {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return "", fmt.Errorf("decode IPv6 hex %q: %w", s, err)
		}
		raw[i] = byte(v)
	}
	addr := make(net.IP, 16)
	for word := 0; word < 4; word++ {
		addr[word*4+0] = raw[word*4+3]
		addr[word*4+1] = raw[word*4+2]
		addr[word*4+2] = raw[word*4+1]
		addr[word*4+3] = raw[word*4+0]
	}
	return addr.String(), nil
}
