// Package supervisor implements the core's parent-process watchdog: when
// spawned by the desktop shell (which passes --parent <pid>), the core
// must not outlive that shell. A crashed or force-killed shell never runs
// its graceful POST /shutdown, and an orphaned core would keep the SQLite
// database locked and its port bound - the next app launch would then
// start a second core against a locked database, which is exactly the
// kind of failure that makes an application feel dead. The watchdog turns
// "parent disappeared" into the same graceful shutdown path every other
// exit takes.
package supervisor

import (
	"context"
	"fmt"
	"time"
)

// pollInterval is how often the parent's liveness is checked. The check
// is a zero-cost process-existence probe, and a dead-parent window of a
// second or two is invisible to a user who would otherwise have to notice
// and kill a zombie process by hand; 1s keeps worst-case orphan lifetime
// negligible without any measurable idle cost (a single probe per second,
// no handles held on the parent).
const pollInterval = time.Second

// WatchParent reports (via the returned channel, closed once) when the
// process with the given PID is no longer running.
//
// Windows-specific behavior lives in checkParentAlive (below): the
// OpenProcess-based probe distinguishes "PID not running" from "PID
// recycled by an unrelated process" using the process creation time
// captured at watch start - the same approach Task Manager's "go to
// details" relies on - so a PID reuse cannot keep a zombie core alive
// waiting on an unrelated process, and cannot kill it either (only a
// genuine parent death triggers).
//
// The returned channel is closed exactly once, when the parent is
// observed gone or the probe itself fails in a way that makes liveness
// undecidable (fail-safe: shut down rather than linger).
func WatchParent(ctx context.Context, parentPID int) (<-chan struct{}, error) {
	// Capture the parent's identity up front: if the parent dies and its
	// PID is immediately recycled by another process, an existence-only
	// check would keep this core alive forever, watching an impostor.
	createdAt, err := processCreationTime(parentPID)
	if err != nil {
		return nil, fmt.Errorf("resolve parent pid %d: %w", parentPID, err)
	}

	ch := make(chan struct{})
	go func() {
		defer close(ch)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				alive, err := parentStillAlive(parentPID, createdAt)
				if err != nil || !alive {
					// An unresolvable probe (permissions changed, handle
					// errors) is treated as "parent gone": the core's
					// job is to serve its shell, not to outlive it.
					return
				}
			}
		}
	}()
	return ch, nil
}
