//go:build !windows

package supervisor

import (
	"fmt"
	"syscall"
)

// parentIdentity is the Unix counterpart of the Windows creation-time
// token: a PID is considered the parent if a kill(pid, 0) probe succeeds
// or fails with EPERM. Unix PID reuse is not guarded against here - the
// probe is re-checked every second and a recycled PID would have to be
// another process owned by this user to delay shutdown, an acceptable
// tradeoff for a zero-dependency probe (see WatchParent's doc comment).
type parentIdentity struct{}

// processCreationTime exists for API parity with the Windows build (whose
// watchdog needs a creation-time token to defeat PID reuse). On Unix a
// quick existence check is captured once so a bad --parent value fails
// loudly (and disables the watchdog) instead of silently watching
// nothing.
func processCreationTime(pid int) (parentIdentity, error) {
	if err := syscall.Kill(pid, 0); err != nil {
		return parentIdentity{}, fmt.Errorf("probe parent pid %d: %w", pid, err)
	}
	return parentIdentity{}, nil
}

// parentStillAlive reports whether the parent's PID still refers to a
// live process. kill(pid, 0) sends no signal - it is purely an
// existence-and-permission check, documented in signal(7): ESRCH means
// gone; EPERM means the process IS alive but not ours to signal, which
// for a directly spawned child is treated as alive and re-checked on the
// next tick rather than acting on a quirk.
func parentStillAlive(pid int, _ parentIdentity) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true, nil
	}
	if err == syscall.ESRCH {
		return false, nil
	}
	if err == syscall.EPERM {
		return true, nil
	}
	return false, fmt.Errorf("probe parent pid %d: %w", pid, err)
}
