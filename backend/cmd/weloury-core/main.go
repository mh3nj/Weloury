// Command weloury-core is Weloury's Go backend process. It holds every
// piece of application business logic; the Rust/Tauri shell that spawns
// it is intentionally a thin process-lifecycle wrapper (see
// apps/desktop/src/main.rs).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"weloury/internal/app"
	"weloury/internal/supervisor"
)

func main() {
	// --parent <pid> is passed by the desktop shell on every spawn (see
	// apps/desktop/src/main.rs): if that process dies without its usual
	// graceful POST /shutdown - a crash, a force-kill from Task Manager -
	// the shell's RunEvent::Exit handler never runs, and this process
	// would otherwise linger forever holding the SQLite database open and
	// listening on its port, breaking the next launch ("database is
	// locked" from a zombie backend is exactly the kind of bug that makes
	// an app feel dead). The supervisor watches the parent PID and
	// triggers the same graceful path a shutdown request takes. A direct
	// (human) start has no --parent and no watchdog, as before.
	parentPID := flag.Int("parent", 0, "PID of the parent process to monitor; 0 disables the watchdog (direct/development start)")
	trayStart := flag.Bool("tray", false, "marks a start-hidden-to-tray session (informational, logged at startup)")
	flag.Parse()

	if err := run(*parentPID, *trayStart); err != nil {
		fmt.Fprintln(os.Stderr, "weloury-core: fatal:", err)
		os.Exit(1)
	}
}

func run(parentPID int, trayStart bool) error {
	// The watchdog is wired in before anything else so even a startup
	// failure can't orphan the process behind a dead parent.
	var stopWatchdog func()
	var watchdogDone <-chan struct{}
	if parentPID > 0 {
		var watchCtx context.Context
		watchCtx, stopWatchdog = context.WithCancel(context.Background())
		parentGone, err := supervisor.WatchParent(watchCtx, parentPID)
		if err != nil {
			// A stale/invalid --parent must never prevent startup: the
			// flag is an optimization over "graceful shutdown usually
			// arrives", not a precondition. Without a live signal the
			// process simply behaves as if no watchdog existed.
			stopWatchdog()
			fmt.Fprintf(os.Stderr, "weloury-core: parent watchdog disabled (%v)\n", err)
		} else {
			watchdogDone = parentGone
		}
		defer stopWatchdog()
	}

	// os.Interrupt is delivered consistently across Linux, macOS, and
	// Windows (as CTRL_C_EVENT). syscall.SIGTERM is meaningful on
	// Linux/macOS; it is defined but not delivered the same way on
	// Windows, which has no POSIX signal semantics. Neither is actually
	// how the desktop shell asks the core to stop in normal operation -
	// that happens over IPC via POST /shutdown (see
	// ipc.Server.ShutdownRequested and ARCHITECTURE.md) specifically
	// because it is one code path that behaves identically on every
	// platform. This signal handling exists as defense in depth for
	// direct process termination, e.g. Ctrl+C while running weloury-core
	// standalone during development.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New()
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	defer func() {
		if closeErr := a.Close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, "weloury-core: cleanup error:", closeErr)
		}
	}()

	// The watchdog races nothing here: parentGone firing only closes the
	// merged context below, and a.Run's own select is the single place
	// that reacts - the same shutdown sequence either way, never two.
	if watchdogDone != nil {
		merged, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-watchdogDone:
				cancel()
			case <-merged.Done():
			}
		}()
		ctx = merged
	}

	if trayStart {
		a.LogStartupContext("tray session")
	}

	// The handshake line is the core's entire startup contract with its
	// parent process: exactly one line of JSON on stdout, emitted the
	// moment the IPC listener is ready to accept connections. See
	// ipc.Server.PrintHandshake and ARCHITECTURE.md "IPC handshake".
	if err := a.Server().PrintHandshake(os.Stdout, os.Getpid()); err != nil {
		return fmt.Errorf("write startup handshake: %w", err)
	}

	return a.Run(ctx)
}
