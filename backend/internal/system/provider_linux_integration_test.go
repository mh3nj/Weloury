//go:build linux

package system

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

// These tests exercise linuxProvider against the real, running OS rather
// than fixture strings - meaningful in a way the fixture-based tests in
// linux_parse_test.go aren't, since they'd catch a mismatch between this
// package's assumptions and an actual kernel/proc filesystem that no
// amount of fixture data can. They're intentionally light on specific
// value assertions (a CI runner's CPU model, core count, and disk layout
// are unknown ahead of time) and instead assert the shape and basic
// sanity of what comes back.

func TestLinuxProviderSystemInfo(t *testing.T) {
	p := newPlatformProvider()
	info, err := p.SystemInfo(context.Background())
	if err != nil {
		t.Fatalf("SystemInfo: %v", err)
	}
	if info.Hostname == "" {
		t.Error("expected a non-empty hostname")
	}
	if info.OS != "linux" {
		t.Errorf("OS = %q, want %q", info.OS, "linux")
	}
	if info.UptimeSeconds <= 0 {
		t.Errorf("UptimeSeconds = %v, want > 0", info.UptimeSeconds)
	}
	if info.BootTime.After(time.Now()) {
		t.Errorf("BootTime %v is in the future", info.BootTime)
	}
}

func TestLinuxProviderCPUInfo(t *testing.T) {
	p := newPlatformProvider()
	info, err := p.CPUInfo(context.Background())
	if err != nil {
		t.Fatalf("CPUInfo: %v", err)
	}
	if info.LogicalCores < 1 {
		t.Errorf("LogicalCores = %d, want >= 1", info.LogicalCores)
	}
	if info.PhysicalCores < 1 {
		t.Errorf("PhysicalCores = %d, want >= 1", info.PhysicalCores)
	}
}

func TestLinuxProviderCPUUsage(t *testing.T) {
	p := newPlatformProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	usage, err := p.CPUUsage(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("CPUUsage: %v", err)
	}
	if usage.OverallPercent < 0 || usage.OverallPercent > 100 {
		t.Errorf("OverallPercent = %v, want in [0, 100]", usage.OverallPercent)
	}
	for i, pct := range usage.PerCorePercent {
		if pct < 0 || pct > 100 {
			t.Errorf("PerCorePercent[%d] = %v, want in [0, 100]", i, pct)
		}
	}
}

func TestLinuxProviderCPUUsageRespectsContextCancellation(t *testing.T) {
	p := newPlatformProvider()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before the call

	_, err := p.CPUUsage(ctx, time.Second)
	if err == nil {
		t.Fatal("expected an error from an already-canceled context")
	}
}

func TestLinuxProviderMemoryInfo(t *testing.T) {
	p := newPlatformProvider()
	mem, err := p.MemoryInfo(context.Background())
	if err != nil {
		t.Fatalf("MemoryInfo: %v", err)
	}
	if mem.TotalBytes == 0 {
		t.Error("expected non-zero TotalBytes")
	}
	if mem.UsedBytes > mem.TotalBytes {
		t.Errorf("UsedBytes (%d) > TotalBytes (%d)", mem.UsedBytes, mem.TotalBytes)
	}
}

func TestLinuxProviderDisks(t *testing.T) {
	p := newPlatformProvider()
	disks, err := p.Disks(context.Background())
	if err != nil {
		t.Fatalf("Disks: %v", err)
	}
	// Every real container/VM has at least a root filesystem that isn't
	// one of the excluded pseudo-filesystems.
	if len(disks) == 0 {
		t.Error("expected at least one real mounted filesystem")
	}
	for _, d := range disks {
		if d.UsedBytes > d.TotalBytes {
			t.Errorf("disk %s: UsedBytes (%d) > TotalBytes (%d)", d.MountPoint, d.UsedBytes, d.TotalBytes)
		}
	}
}

func TestLinuxProviderNetworkInterfaces(t *testing.T) {
	p := newPlatformProvider()
	ifaces, err := p.NetworkInterfaces(context.Background())
	if err != nil {
		t.Fatalf("NetworkInterfaces: %v", err)
	}
	// Every Linux system has at least a loopback interface.
	foundLoopback := false
	for _, iface := range ifaces {
		if iface.IsLoopback {
			foundLoopback = true
		}
	}
	if !foundLoopback {
		t.Error("expected at least one loopback interface")
	}
}

func TestLinuxProviderListProcesses(t *testing.T) {
	p := newPlatformProvider()
	procs, err := p.ListProcesses(context.Background())
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	// The test process itself is always running.
	if len(procs) == 0 {
		t.Fatal("expected at least one process (this test's own process)")
	}
	for _, proc := range procs {
		if proc.PID <= 0 {
			t.Errorf("process with non-positive PID: %+v", proc)
		}
	}
}

func TestLinuxProviderProcessDetailOnSelf(t *testing.T) {
	p := newPlatformProvider()
	detail, err := p.ProcessDetail(context.Background(), int32(os.Getpid()), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("ProcessDetail: %v", err)
	}
	if detail.PID != int32(os.Getpid()) {
		t.Errorf("PID = %d, want %d", detail.PID, os.Getpid())
	}
	if detail.Executable == "" {
		t.Error("expected a non-empty Executable for our own process")
	}
	if len(detail.CommandLine) == 0 {
		t.Error("expected a non-empty CommandLine for our own process")
	}
	if detail.CPUPercent < 0 {
		t.Errorf("CPUPercent = %v, want >= 0", detail.CPUPercent)
	}
}

func TestLinuxProviderProcessDetailNotFound(t *testing.T) {
	p := newPlatformProvider()
	// PID 2^30 is exceedingly unlikely to exist on any real system
	// (Linux's default pid_max is far lower) without depending on any
	// specific unused PID, which would be flaky.
	_, err := p.ProcessDetail(context.Background(), 1<<30, 10*time.Millisecond)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLinuxProviderTerminateProcessGraceful(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start a disposable `sleep` child process: %v", err)
	}
	pid := int32(cmd.Process.Pid)

	p := newPlatformProvider()
	if err := p.TerminateProcess(context.Background(), pid, false); err != nil {
		t.Fatalf("TerminateProcess (graceful): %v", err)
	}

	waitForExit(t, cmd)
}

func TestLinuxProviderTerminateProcessForce(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start a disposable `sleep` child process: %v", err)
	}
	pid := int32(cmd.Process.Pid)

	p := newPlatformProvider()
	if err := p.TerminateProcess(context.Background(), pid, true); err != nil {
		t.Fatalf("TerminateProcess (force): %v", err)
	}

	waitForExit(t, cmd)
}

func TestLinuxProviderTerminateProcessNotFound(t *testing.T) {
	p := newPlatformProvider()
	err := p.TerminateProcess(context.Background(), 1<<30, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestLinuxProviderNetworkIOCounters exercises NetworkIOCounters (added
// for the metrics engine, Phase 4) against the real /proc/net/dev on
// whatever machine runs this test - a loopback interface always exists
// on Linux, giving one entry we can assert against without depending on
// this environment having any particular real NIC.
func TestLinuxProviderNetworkIOCounters(t *testing.T) {
	p := newPlatformProvider()
	counters, err := p.NetworkIOCounters(context.Background())
	if err != nil {
		t.Fatalf("NetworkIOCounters: %v", err)
	}
	if len(counters) == 0 {
		t.Fatal("expected at least one interface (loopback), got none")
	}
	foundLoopback := false
	for _, c := range counters {
		if c.Name == "lo" {
			foundLoopback = true
		}
	}
	if !foundLoopback {
		t.Errorf("expected an entry named %q among %d interfaces", "lo", len(counters))
	}
}

// TestLinuxProviderNetworkConnectionsFindsRealListener opens a real TCP
// listener on this test process (so its owning PID is guaranteed to be
// this test binary's own - no permission boundary to worry about, since
// Weloury core can always read its own /proc/self/fd) and confirms
// NetworkConnections finds it with the right port, state, and PID. This
// is the integration-level counterpart to
// TestParseProcNetTCPListeningSockets: that test proves the fixture
// parses correctly; this one proves the whole call - real /proc/net/tcp,
// real /proc/*/fd inode cross-reference - actually works end to end
// against the real running kernel.
func TestLinuxProviderNetworkConnectionsFindsRealListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	selfPID := int32(os.Getpid())

	p := newPlatformProvider()
	conns, err := p.NetworkConnections(context.Background())
	if err != nil {
		t.Fatalf("NetworkConnections: %v", err)
	}

	var found *NetworkConnection
	for i, c := range conns {
		if c.Protocol == "tcp" && c.LocalPort == port {
			found = &conns[i]
		}
	}
	if found == nil {
		t.Fatalf("expected an entry for the real listener on port %d among %d connections", port, len(conns))
	}
	if found.LocalAddress != "127.0.0.1" {
		t.Errorf("LocalAddress = %q, want 127.0.0.1", found.LocalAddress)
	}
	if found.State != "LISTEN" {
		t.Errorf("State = %q, want LISTEN", found.State)
	}
	if found.PID != selfPID {
		t.Errorf("PID = %d, want %d (this test process's own PID - should always be attributable, no permission boundary)", found.PID, selfPID)
	}
	if found.ProcessName == "" {
		t.Error("expected a non-empty ProcessName for this test process's own listener")
	}
}

// waitForExit waits for cmd's process to actually exit (confirming the
// signal sent by TerminateProcess really took effect, not just that the
// syscall didn't error) with a generous but bounded timeout so a signal-
// delivery failure fails the test instead of hanging the suite forever.
func waitForExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
		// cmd.Wait() returning at all (regardless of the exit error,
		// which will reflect the signal) confirms the process exited.
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit within 5s of TerminateProcess")
	}
}
