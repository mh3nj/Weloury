//go:build windows

package supervisor

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// stillActive is Windows' STILL_ACTIVE pseudo exit code (259): GetExit-
// CodeProcess reports it for a process that has not terminated. x/sys
// doesn't export the constant, so it's defined here once, next to its
// only use, with the SDK value.
const stillActive = 259

// processCreationTime captures the parent's process creation time at
// watch start; parentStillAlive compares it against a fresh reading so a
// recycled PID (a new, unrelated process with the same ID) is correctly
// treated as "parent gone" instead of keeping the core alive against an
// impostor.
func processCreationTime(pid int) (time.Time, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return time.Time{}, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, fmt.Errorf("get process times for %d: %w", pid, err)
	}
	return time.Unix(0, creation.Nanoseconds()), nil
}

// parentStillAlive reports whether the process with the given PID is
// running AND is the same process whose creation time was captured at
// watch start. Err is returned only for probe failures that should be
// surfaced (the caller treats any error as "parent gone" - fail-safe).
func parentStillAlive(pid int, createdAt time.Time) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER / ERROR_NOT_FOUND mean the PID has no
		// live process: parent is gone. Any other error is a probe
		// failure; both paths lead to shutdown (err non-nil or alive
		// false), which is the fail-safe direction for a watchdog.
		return false, nil
	}
	defer windows.CloseHandle(h)

	// Existence is not liveness on Windows: a TERMINATED process whose
	// object is still referenced by any open handle (a debugger, a
	// process-lister, our own previous poll racing a close, the spawner's
	// os/exec-style handle) still answers OpenProcess and
	// GetProcessTimes with the original creation time - existence alone
	// would therefore report a dead parent as alive for as long as any
	// handle lingers (found by TestWatchParentFiresWhenParentGone, whose
	// helper process's exit went unnoticed for exactly this reason). The
	// exit-code check is the discriminating signal: a terminated process
	// reports its exit status instead of STILL_ACTIVE.
	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err != nil {
		return false, fmt.Errorf("get exit code for %d: %w", pid, err)
	}
	if exitCode != stillActive {
		return false, nil
	}

	// The PID could still have been recycled between the exit-code read
	// and now (a brand-new live process with this PID): verify identity
	// against the creation time captured at watch start.
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return false, fmt.Errorf("get process times for %d: %w", pid, err)
	}
	current := time.Unix(0, creation.Nanoseconds())
	return current.Equal(createdAt), nil
}
