//go:build linux

package system

import (
	"testing"
	"time"
)

// Fixture data below was captured verbatim from a real Ubuntu 24.04
// (kernel 6.18) sandbox while developing this parser, specifically so
// these tests exercise the actual format the kernel produces rather than
// a hand-guessed approximation of it.

const fixtureProcStat = `cpu  359 0 841 60479 32 0 11 36 0 0
cpu0 359 0 841 60479 32 0 11 36 0 0
intr 23017 0 0 0
ctxt 48630
btime 1786427359
processes 730
`

func TestParseProcStat(t *testing.T) {
	aggregate, perCore, err := parseProcStat(fixtureProcStat)
	if err != nil {
		t.Fatalf("parseProcStat: %v", err)
	}
	if aggregate.name != "cpu" {
		t.Fatalf("aggregate.name = %q, want %q", aggregate.name, "cpu")
	}
	if aggregate.user != 359 || aggregate.system != 841 || aggregate.idle != 60479 {
		t.Fatalf("aggregate = %+v, want user=359 system=841 idle=60479", aggregate)
	}
	if len(perCore) != 1 || perCore[0].name != "cpu0" {
		t.Fatalf("perCore = %+v, want exactly one entry named cpu0", perCore)
	}
}

func TestParseProcStatRejectsMissingAggregate(t *testing.T) {
	_, _, err := parseProcStat("cpu0 1 2 3 4 5 6 7 8 9 10\n")
	if err == nil {
		t.Fatal("expected an error when no aggregate \"cpu\" line is present")
	}
}

func TestCPUUsagePercent(t *testing.T) {
	// total delta = 1000, idle delta = 800 -> 200 busy ticks -> 20% busy.
	prev := cpuSample{name: "cpu", user: 100, idle: 900}
	curr := cpuSample{name: "cpu", user: 300, idle: 1700}

	got := cpuUsagePercent(prev, curr)
	want := 20.0
	if got != want {
		t.Fatalf("cpuUsagePercent = %v, want %v", got, want)
	}
}

func TestCPUUsagePercentNoElapsedTimeReturnsZero(t *testing.T) {
	s := cpuSample{name: "cpu", user: 100, idle: 900}
	if got := cpuUsagePercent(s, s); got != 0 {
		t.Fatalf("cpuUsagePercent with identical samples = %v, want 0", got)
	}
}

const fixtureMemInfo = `MemTotal:        4093756 kB
MemFree:         3828416 kB
MemAvailable:    3812752 kB
Buffers:           14732 kB
Cached:           176284 kB
SwapCached:            0 kB
SwapTotal:             0 kB
SwapFree:              0 kB
`

func TestParseMemInfo(t *testing.T) {
	mem, err := parseMemInfo(fixtureMemInfo)
	if err != nil {
		t.Fatalf("parseMemInfo: %v", err)
	}
	wantTotal := uint64(4093756) * 1024
	if mem.TotalBytes != wantTotal {
		t.Fatalf("TotalBytes = %d, want %d", mem.TotalBytes, wantTotal)
	}
	wantAvailable := uint64(3812752) * 1024
	if mem.AvailableBytes != wantAvailable {
		t.Fatalf("AvailableBytes = %d, want %d", mem.AvailableBytes, wantAvailable)
	}
	wantUsed := wantTotal - wantAvailable
	if mem.UsedBytes != wantUsed {
		t.Fatalf("UsedBytes = %d, want %d", mem.UsedBytes, wantUsed)
	}
	if mem.SwapTotalBytes != 0 || mem.SwapUsedBytes != 0 {
		t.Fatalf("swap = %d/%d, want 0/0 for this fixture", mem.SwapUsedBytes, mem.SwapTotalBytes)
	}
}

func TestParseMemInfoMissingTotalIsError(t *testing.T) {
	if _, err := parseMemInfo("MemFree: 100 kB\n"); err == nil {
		t.Fatal("expected an error when MemTotal is absent")
	}
}

const fixtureCPUInfo = `processor	: 0
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) Processor @ 2.80GHz
cpu MHz		: 2800.206
physical id	: 0
core id		: 0
cpu cores	: 1

`

func TestParseCPUInfo(t *testing.T) {
	model, logical, physical, mhz := parseCPUInfo(fixtureCPUInfo)
	if model != "Intel(R) Xeon(R) Processor @ 2.80GHz" {
		t.Fatalf("model = %q", model)
	}
	if logical != 1 {
		t.Fatalf("logical = %d, want 1", logical)
	}
	if physical != 1 {
		t.Fatalf("physical = %d, want 1", physical)
	}
	if mhz != 2800.206 {
		t.Fatalf("mhz = %v, want 2800.206", mhz)
	}
}

func TestParseCPUInfoMultiCoreCountsDistinctPhysicalCoreIDs(t *testing.T) {
	// Two logical processors sharing one physical core (hyperthreading)
	// plus one on a distinct core: 3 logical, 2 physical.
	content := `processor	: 0
model name	: Test CPU
physical id	: 0
core id		: 0

processor	: 1
physical id	: 0
core id		: 0

processor	: 2
physical id	: 0
core id		: 1
`
	_, logical, physical, _ := parseCPUInfo(content)
	if logical != 3 {
		t.Fatalf("logical = %d, want 3", logical)
	}
	if physical != 2 {
		t.Fatalf("physical = %d, want 2", physical)
	}
}

func TestParseUptime(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	uptime, bootTime, err := parseUptime("619.28 604.79\n", now)
	if err != nil {
		t.Fatalf("parseUptime: %v", err)
	}
	if uptime != 619.28 {
		t.Fatalf("uptime = %v, want 619.28", uptime)
	}
	wantBoot := now.Add(-619*time.Second - 280*time.Millisecond)
	if diff := bootTime.Sub(wantBoot); diff > time.Millisecond || diff < -time.Millisecond {
		t.Fatalf("bootTime = %v, want approximately %v", bootTime, wantBoot)
	}
}

func TestParseKernelVersion(t *testing.T) {
	content := "Linux version 6.18.5-fc-v20 (builder@sandboxing) (gcc (GCC) 15.2.0, GNU ld (GNU Binutils) 2.46) #1 SMP PREEMPT_DYNAMIC @0"
	got := parseKernelVersion(content)
	want := "6.18.5-fc-v20"
	if got != want {
		t.Fatalf("parseKernelVersion = %q, want %q", got, want)
	}
}

const fixtureOSRelease = `PRETTY_NAME="Ubuntu 24.04.4 LTS"
NAME="Ubuntu"
VERSION_ID="24.04"
ID=ubuntu
`

func TestParseOSRelease(t *testing.T) {
	got := parseOSRelease(fixtureOSRelease)
	want := "Ubuntu 24.04.4 LTS"
	if got != want {
		t.Fatalf("parseOSRelease = %q, want %q", got, want)
	}
}

func TestParseOSReleaseMissingPrettyNameReturnsEmpty(t *testing.T) {
	if got := parseOSRelease("NAME=\"Ubuntu\"\n"); got != "" {
		t.Fatalf("parseOSRelease = %q, want empty string", got)
	}
}

const fixtureProcPidStat = `740 (cat) R 737 737 0 0 -1 4194304 60 0 0 0 0 0 0 0 20 0 1 0 62777 2916352 367 18446744073709551615 94141759049728 94141759067313 140732514420176 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 94141759081104 94141759082600 94142466445312 140732514426159 140732514426179 140732514426179 140732514426859 0`

func TestParseProcPidStat(t *testing.T) {
	got, err := parseProcPidStat(fixtureProcPidStat)
	if err != nil {
		t.Fatalf("parseProcPidStat: %v", err)
	}
	if got.pid != 740 {
		t.Fatalf("pid = %d, want 740", got.pid)
	}
	if got.comm != "cat" {
		t.Fatalf("comm = %q, want %q", got.comm, "cat")
	}
	if got.state != "R" {
		t.Fatalf("state = %q, want %q", got.state, "R")
	}
	if got.ppid != 737 {
		t.Fatalf("ppid = %d, want 737", got.ppid)
	}
	if got.starttimeTicks != 62777 {
		t.Fatalf("starttimeTicks = %d, want 62777", got.starttimeTicks)
	}
	if got.vsizeBytes != 2916352 {
		t.Fatalf("vsizeBytes = %d, want 2916352", got.vsizeBytes)
	}
	if got.rssPages != 367 {
		t.Fatalf("rssPages = %d, want 367", got.rssPages)
	}
	// utime and stime are both 0 in this fixture (a freshly-started `cat`
	// process that had used essentially no CPU time yet).
	if got.utimeTicks != 0 || got.stimeTicks != 0 {
		t.Fatalf("utimeTicks=%d stimeTicks=%d, want 0/0 for this fixture", got.utimeTicks, got.stimeTicks)
	}
}

func TestParseProcPidStatExtractsUtimeStime(t *testing.T) {
	// Field 14 (utime) = 123, field 15 (stime) = 45 - positioned exactly
	// where remainder[11] and remainder[12] land after the comm paren.
	line := `50 (worker) R 1 1 1 0 -1 0 0 0 0 0 123 45 0 0 20 0 1 0 100 1000 10 0 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0`
	got, err := parseProcPidStat(line)
	if err != nil {
		t.Fatalf("parseProcPidStat: %v", err)
	}
	if got.utimeTicks != 123 {
		t.Fatalf("utimeTicks = %d, want 123", got.utimeTicks)
	}
	if got.stimeTicks != 45 {
		t.Fatalf("stimeTicks = %d, want 45", got.stimeTicks)
	}
}

func TestParseProcPidStatHandlesParensAndSpacesInCommandName(t *testing.T) {
	// A process named "weird (name) here" is legal (any bytes except NUL
	// and '/' are valid in a Linux process name) and is exactly the case
	// naive whitespace-splitting would get wrong.
	line := `99 (weird (name) here) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100 1000 10 0 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0`
	got, err := parseProcPidStat(line)
	if err != nil {
		t.Fatalf("parseProcPidStat: %v", err)
	}
	if got.comm != "weird (name) here" {
		t.Fatalf("comm = %q, want %q", got.comm, "weird (name) here")
	}
	if got.state != "S" {
		t.Fatalf("state = %q, want %q", got.state, "S")
	}
}

func TestParseProcPidStatRejectsTruncatedLine(t *testing.T) {
	if _, err := parseProcPidStat("1 (init) R 0 0"); err == nil {
		t.Fatal("expected an error for a truncated stat line")
	}
}

func TestProcessStateLabel(t *testing.T) {
	cases := map[string]string{
		"R": "running", "S": "sleeping", "D": "disk_sleep",
		"T": "stopped", "Z": "zombie", "?": "unknown",
	}
	for code, want := range cases {
		if got := processStateLabel(code); got != want {
			t.Errorf("processStateLabel(%q) = %q, want %q", code, got, want)
		}
	}
}

const fixtureProcStatus = `Name:	head
State:	R (running)
Pid:	741
PPid:	737
Uid:	0	0	0	0
Gid:	0	0	0	0
Threads:	3
`

func TestParseProcStatus(t *testing.T) {
	uid, threads, ok := parseProcStatus(fixtureProcStatus)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uid != "0" {
		t.Fatalf("uid = %q, want %q", uid, "0")
	}
	if threads != 3 {
		t.Fatalf("threads = %d, want 3", threads)
	}
}

func TestParseProcStatusDefaultsThreadsToOneWhenAbsent(t *testing.T) {
	_, threads, _ := parseProcStatus("Uid:\t1000\t1000\t1000\t1000\n")
	if threads != 1 {
		t.Fatalf("threads = %d, want default of 1", threads)
	}
}

func TestParseCmdline(t *testing.T) {
	// /proc/[pid]/cmdline is NUL-separated with a trailing NUL.
	content := []byte("cat\x00-n\x00somefile.txt\x00")
	got := parseCmdline(content)
	want := []string{"cat", "-n", "somefile.txt"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseCmdlineEmptyContentReturnsNil(t *testing.T) {
	// Some kernel threads have a genuinely empty cmdline.
	if got := parseCmdline([]byte{}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := parseCmdline([]byte("\x00")); got != nil {
		t.Fatalf("got %v, want nil for a single trailing NUL", got)
	}
}

const fixtureNetDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
  eth0:  156102     707    0   19    0     0          0         0   274590     663    0    0    0     0       0          0
`

// fixtureProcNetTCP is captured from this sandbox's real /proc/net/tcp:
// two listening sockets (an internal daemon on ports 0x07E8/0x07E9, i.e.
// 2024/2025) plus a real 127.0.0.1 listener this test suite's own setup
// briefly opened on port 60747 (0xED4B) - the third row is what proved
// the little-endian address decoding in parseHexIPv4 is right, not
// assumed: "0100007F" really does mean 127.0.0.1 on this real kernel.
const fixtureProcNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:07E8 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 676 1 00000000672c7b86 100 0 0 10 0
   1: 00000000:07E9 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 677 1 000000004f1c317b 100 0 0 10 0
   2: 0100007F:ED4B 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 856 1 0000000065a5f371 100 0 0 10 0
`

func TestParseProcNetTCPListeningSockets(t *testing.T) {
	entries, err := parseProcNet(fixtureProcNetTCP, "tcp")
	if err != nil {
		t.Fatalf("parseProcNet: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}

	var loopback *procNetEntry
	for i, e := range entries {
		if e.localPort == 60747 {
			loopback = &entries[i]
		}
		if e.state != "LISTEN" {
			t.Errorf("entry %+v: state = %q, want LISTEN (all fixture rows are listening sockets)", e, e.state)
		}
		if e.protocol != "tcp" {
			t.Errorf("entry %+v: protocol = %q, want tcp", e, e.protocol)
		}
	}
	if loopback == nil {
		t.Fatal("expected an entry for port 60747")
	}
	if loopback.localAddress != "127.0.0.1" {
		t.Errorf("localAddress = %q, want 127.0.0.1 (this is the real captured-fixture regression check for the little-endian hex decode)", loopback.localAddress)
	}
	if loopback.inode != 856 {
		t.Errorf("inode = %d, want 856", loopback.inode)
	}
}

func TestParseProcNetUDPHasNoState(t *testing.T) {
	// UDP's `st` field exists in the file format but carries no
	// meaningful connection state - see NetworkConnection.State's doc
	// comment. Reuse the TCP fixture's rows (same column layout) purely
	// to exercise the isUDP branch; the actual state values in this
	// fixture are irrelevant to this assertion.
	entries, err := parseProcNet(fixtureProcNetTCP, "udp")
	if err != nil {
		t.Fatalf("parseProcNet: %v", err)
	}
	for _, e := range entries {
		if e.state != "" {
			t.Errorf("entry %+v: state = %q, want empty for udp", e, e.state)
		}
		if e.protocol != "udp" {
			t.Errorf("entry %+v: protocol = %q, want udp", e, e.protocol)
		}
	}
}

func TestParseHexIPv4(t *testing.T) {
	cases := map[string]string{
		"0100007F": "127.0.0.1",
		"00000000": "0.0.0.0",
		"0101A8C0": "192.168.1.1",
	}
	for hex, want := range cases {
		got, err := parseHexIPv4(hex)
		if err != nil {
			t.Errorf("parseHexIPv4(%q): %v", hex, err)
			continue
		}
		if got != want {
			t.Errorf("parseHexIPv4(%q) = %q, want %q", hex, got, want)
		}
	}
}

func TestParseHexIPv4InvalidLength(t *testing.T) {
	if _, err := parseHexIPv4("ABCD"); err == nil {
		t.Error("expected an error for a too-short hex address")
	}
}

func TestParseHexIPv6Loopback(t *testing.T) {
	// ::1 stored per-32-bit-word little-endian, per parseHexIPv6's doc
	// comment: the last raw byte holding the address's trailing 0x01
	// lands at raw[12], not raw[15] - computed independently in Python
	// (bytearray(16) with byte 12 set to 1, then .hex()) rather than
	// hand-derived, to avoid the exact off-by-one-word error this
	// function exists to get right.
	got, err := parseHexIPv6("00000000000000000000000001000000")
	if err != nil {
		t.Fatalf("parseHexIPv6: %v", err)
	}
	if got != "::1" {
		t.Errorf("got %q, want ::1", got)
	}
}

func TestParseHexIPv6WrongLength(t *testing.T) {
	if _, err := parseHexIPv6("ABCD"); err == nil {
		t.Error("expected an error for a too-short hex address")
	}
}

func TestParseHexAddrPortMalformed(t *testing.T) {
	if _, _, err := parseHexAddrPort("no-colon-here", false); err == nil {
		t.Error("expected an error for a field with no ':' separator")
	}
}

func TestParseNetDev(t *testing.T) {
	samples, err := parseNetDev(fixtureNetDev)
	if err != nil {
		t.Fatalf("parseNetDev: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}
	byName := map[string]netDevSample{}
	for _, s := range samples {
		byName[s.name] = s
	}
	eth0, ok := byName["eth0"]
	if !ok {
		t.Fatal("expected an eth0 entry")
	}
	if eth0.rxBytes != 156102 || eth0.txBytes != 274590 {
		t.Fatalf("eth0 = %+v, want rx=156102 tx=274590", eth0)
	}
}

const fixtureMounts = `proc /proc proc rw,relatime 0 0
sysfs /sys sysfs rw,relatime 0 0
devtmpfs /dev devtmpfs rw,relatime,size=2041812k 0 0
tmpfs /dev/shm tmpfs rw,relatime,size=4093756k 0 0
/dev/vda1 / ext4 rw,relatime 0 0
`

func TestParseMountsExcludesPseudoFilesystems(t *testing.T) {
	mounts, err := parseMounts(fixtureMounts)
	if err != nil {
		t.Fatalf("parseMounts: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("got %d real mounts, want 1 (proc/sysfs/devtmpfs/tmpfs should be excluded): %+v", len(mounts), mounts)
	}
	if mounts[0].mountPoint != "/" || mounts[0].filesystem != "ext4" {
		t.Fatalf("mounts[0] = %+v, want mountPoint=/ filesystem=ext4", mounts[0])
	}
}
