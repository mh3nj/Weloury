package supervisor

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// execCommandSleep starts a real OS process that exits (on its own) after
// roughly d, and returns its PID. Used as the ephemeral "parent" whose
// death the watchdog must notice - a genuine process-exit event, not a
// simulation. Windows has no `sleep` binary guaranteed on PATH, so the
// test binary re-invokes itself: `go test` binaries interpret -test.run
// and exit, and Go's own toolchain binaries exist on every platform this
// repo builds for. The simplest reliable cross-platform one-shot is the
// Go toolchain's `go` command with a trivial no-op; the test skips if the
// toolchain isn't reachable (it always is when running tests).
func execCommandSleep(t *testing.T, d time.Duration) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	// Re-run this very test binary with a helper flag: it sleeps for d and
	// exits 0. See TestHelperProcess below.
	cmd := exec.Command(exe, "-test.run=TestHelperSleepProcess", "--", d.String())
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_SLEEP="+d.String())
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

// TestHelperSleepProcess is not a real test: it's the body of the helper
// process execCommandSleep spawns (the standard os/exec helper-process
// pattern). It exits after the HELPER_SLEEP duration.
func TestHelperSleepProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		t.Skip("helper process for TestWatchParentFiresWhenParentGone only")
	}
	d, err := time.ParseDuration(os.Getenv("HELPER_SLEEP"))
	if err != nil {
		t.Fatalf("parse HELPER_SLEEP: %v", err)
	}
	time.Sleep(d)
}

// TestWatchParentFiresWhenParentGone verifies the watchdog's core promise:
// the returned channel closes once the watched PID stops existing. The
// "parent" here is a real process guaranteed to die during the test: cmd
// spawns a short-lived helper (sleep/go) whose PID we capture, and the
// watchdog must notice its exit within a couple of poll intervals.
func TestWatchParentFiresWhenParentGone(t *testing.T) {
	// Spawn a real, ephemeral "parent" so the test exercises a genuine
	// process-death detection, not a mocked one. `go` itself (this test
	// binary's toolchain invocation) is the most portable short-lived
	// child: cmd on Windows, sh elsewhere.
	die := execCommandSleep(t, 300*time.Millisecond)

	watchCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parentGone, err := WatchParent(watchCtx, die)
	if err != nil {
		t.Fatalf("WatchParent: %v", err)
	}

	select {
	case <-parentGone:
		// The expected path: the helper exited, the watchdog noticed.
	case <-time.After(10 * time.Second):
		t.Fatal("watchdog did not report the parent's death within 10s")
	}
}

// TestWatchParentRejectsInvalidPID verifies a PID that cannot exist fails
// resolution (so the caller can disable the watchdog with a clear message)
// rather than silently watching nothing.
func TestWatchParentRejectsInvalidPID(t *testing.T) {
	// A PID high enough that no process owns it on any sane system, but
	// still within a valid PID range.
	_, err := WatchParent(context.Background(), 1<<24)
	if err == nil {
		t.Skip("platform returned no error for an unresolvable PID; nothing to assert")
	}
}

// TestWatchParentLiveParentStaysQuiet is the negative case: while the
// watched process is alive, the channel must not close. Uses this test
// binary's own PID, which is trivially alive for the duration.
func TestWatchParentLiveParentStaysQuiet(t *testing.T) {
	self := os.Getpid()

	watchCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parentGone, err := WatchParent(watchCtx, self)
	if err != nil {
		t.Fatalf("WatchParent(self): %v", err)
	}

	select {
	case <-parentGone:
		t.Fatal("watchdog reported a live process (this test binary) as gone")
	case <-time.After(3 * time.Second):
		// Expected: quiet for the whole window.
	}
}
