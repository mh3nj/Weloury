//go:build darwin

package system

import "testing"

// Fixtures here follow the documented, stable output shape of each macOS
// command (per Apple's own man pages), not a real captured sample - see
// this file's package-level comment in darwin_parse.go for why.

func TestParseSysctlBootTime(t *testing.T) {
	got, err := parseSysctlBootTime("{ sec = 1723400000, usec = 123456 } Mon Jan  1 00:00:00 2024\n")
	if err != nil {
		t.Fatalf("parseSysctlBootTime: %v", err)
	}
	if got.Unix() != 1723400000 {
		t.Fatalf("Unix() = %d, want 1723400000", got.Unix())
	}
}

func TestParseSysctlBootTimeRejectsMalformedInput(t *testing.T) {
	if _, err := parseSysctlBootTime("garbage"); err == nil {
		t.Fatal("expected an error for malformed kern.boottime output")
	}
}

func TestParseUint64Sysctl(t *testing.T) {
	got, err := parseUint64Sysctl("17179869184\n")
	if err != nil {
		t.Fatalf("parseUint64Sysctl: %v", err)
	}
	if got != 17179869184 {
		t.Fatalf("got %d, want 17179869184", got)
	}
}

func TestParseUint64SysctlRejectsNonNumeric(t *testing.T) {
	if _, err := parseUint64Sysctl("not a number\n"); err == nil {
		t.Fatal("expected an error for non-numeric sysctl output")
	}
}

func TestParseSwapUsage(t *testing.T) {
	total, used, err := parseSwapUsage("total = 2048.00M  used = 512.00M  free = 1536.00M  (encrypted)\n")
	if err != nil {
		t.Fatalf("parseSwapUsage: %v", err)
	}
	wantTotal := uint64(2048) * 1024 * 1024
	wantUsed := uint64(512) * 1024 * 1024
	if total != wantTotal || used != wantUsed {
		t.Fatalf("got total=%d used=%d, want total=%d used=%d", total, used, wantTotal, wantUsed)
	}
}

const fixtureVMStat = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               50000.
Pages active:                            100000.
Pages inactive:                           80000.
Pages wired down:                         30000.
`

func TestParseVMStat(t *testing.T) {
	pageSize, counts, err := parseVMStat(fixtureVMStat)
	if err != nil {
		t.Fatalf("parseVMStat: %v", err)
	}
	if pageSize != 16384 {
		t.Fatalf("pageSize = %d, want 16384", pageSize)
	}
	if counts["Pages free"] != 50000 {
		t.Fatalf("Pages free = %d, want 50000", counts["Pages free"])
	}
	if counts["Pages wired down"] != 30000 {
		t.Fatalf("Pages wired down = %d, want 30000", counts["Pages wired down"])
	}
}

const fixtureDfOutput = `Filesystem   1024-blocks      Used Available Capacity  Mounted on
/dev/disk3s1   488245288 412345678  70000000    86%    /
/dev/disk3s4   488245288    500000 470000000     1%    /System/Volumes/VM
`

func TestParseDfOutput(t *testing.T) {
	disks, err := parseDfOutput(fixtureDfOutput)
	if err != nil {
		t.Fatalf("parseDfOutput: %v", err)
	}
	if len(disks) != 2 {
		t.Fatalf("got %d disks, want 2", len(disks))
	}
	if disks[0].Device != "/dev/disk3s1" || disks[0].MountPoint != "/" {
		t.Fatalf("disks[0] = %+v", disks[0])
	}
	wantTotal := uint64(488245288) * 1024
	if disks[0].TotalBytes != wantTotal {
		t.Fatalf("TotalBytes = %d, want %d", disks[0].TotalBytes, wantTotal)
	}
}

func TestParseDfOutputHandlesMountPointsWithSpaces(t *testing.T) {
	// "Mounted on" values containing spaces (rare, but valid paths) must
	// not be truncated at the first space.
	line := "Filesystem   1024-blocks      Used Available Capacity  Mounted on\n" +
		"/dev/disk5s1      100000     50000     50000    50%    /Volumes/My Backup Disk\n"
	disks, err := parseDfOutput(line)
	if err != nil {
		t.Fatalf("parseDfOutput: %v", err)
	}
	if len(disks) != 1 || disks[0].MountPoint != "/Volumes/My Backup Disk" {
		t.Fatalf("disks = %+v, want a single disk mounted at \"/Volumes/My Backup Disk\"", disks)
	}
}

const fixturePSOutput = `  PID  PPID S USER            RSS COMM
    1     0 S root           2048 /sbin/launchd
  740   737 R devuser        4096 cat
`

const fixturePSDetailOutput = `  PID  PPID S USER            RSS %CPU COMM
  740   737 R devuser        4096  2.3 cat
`

func TestParsePSDetailOutput(t *testing.T) {
	row, err := parsePSDetailOutput(fixturePSDetailOutput)
	if err != nil {
		t.Fatalf("parsePSDetailOutput: %v", err)
	}
	if row.pid != 740 || row.ppid != 737 {
		t.Fatalf("pid=%d ppid=%d, want 740/737", row.pid, row.ppid)
	}
	if row.cpuPercent != 2.3 {
		t.Fatalf("cpuPercent = %v, want 2.3", row.cpuPercent)
	}
	if row.comm != "cat" {
		t.Fatalf("comm = %q, want %q", row.comm, "cat")
	}
}

func TestParsePSDetailOutputNoDataRowIsError(t *testing.T) {
	if _, err := parsePSDetailOutput("  PID  PPID S USER            RSS %CPU COMM\n"); err == nil {
		t.Fatal("expected an error when ps produced no data row")
	}
}

func TestParsePSOutput(t *testing.T) {
	rows, err := parsePSOutput(fixturePSOutput)
	if err != nil {
		t.Fatalf("parsePSOutput: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[1].pid != 740 || rows[1].ppid != 737 || rows[1].user != "devuser" || rows[1].comm != "cat" {
		t.Fatalf("rows[1] = %+v", rows[1])
	}
	if rows[1].rssKB != 4096 {
		t.Fatalf("rssKB = %d, want 4096", rows[1].rssKB)
	}
}

func TestPSStateLabel(t *testing.T) {
	cases := map[string]string{
		"R": "running", "S": "sleeping", "T": "stopped",
		"Z": "zombie", "I": "idle", "?": "unknown", "": "unknown",
	}
	for code, want := range cases {
		if got := psStateLabel(code); got != want {
			t.Errorf("psStateLabel(%q) = %q, want %q", code, got, want)
		}
	}
}

const fixtureLaunchctlList = `PID	Status	Label
1234	0	com.apple.something
-	0	com.apple.otherthing
-	78	com.apple.failedthing
`

func TestParseLaunchctlList(t *testing.T) {
	rows := parseLaunchctlList(fixtureLaunchctlList)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0].label != "com.apple.something" || rows[0].pidRaw != "1234" {
		t.Fatalf("rows[0] = %+v", rows[0])
	}
	if rows[1].pidRaw != "-" {
		t.Fatalf("rows[1].pidRaw = %q, want \"-\"", rows[1].pidRaw)
	}
}

func TestLaunchctlPID(t *testing.T) {
	if got := launchctlPID("1234"); got != 1234 {
		t.Fatalf("got %d, want 1234", got)
	}
	if got := launchctlPID("-"); got != 0 {
		t.Fatalf("got %d, want 0 for \"-\"", got)
	}
	if got := launchctlPID(""); got != 0 {
		t.Fatalf("got %d, want 0 for empty input", got)
	}
}
