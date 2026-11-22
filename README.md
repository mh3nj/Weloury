# FORGE

FORGE is a local-first system operations platform: one desktop application
to monitor, inspect, manage, debug, and automate the infrastructure running
on your own machine — processes, services, containers, network, storage,
logs, health checks, and automation — instead of switching between Activity
Monitor / Task Manager, `systemctl`, `docker`, `netstat`, half a dozen log
files, and a text editor full of half-remembered shell one-liners.

It is not a Docker Desktop clone, a generic SaaS dashboard, or an AI
wrapper. It's systems software: a Go backend doing real OS-level work,
talking over a documented protocol to a React/Tauri desktop shell.

## Status

**Phase 8 (Hardening) — substantially complete.** Phase 7's UX
refinement pass is done in substance: keyboard navigation across every
dialog and list/detail screen, a real performance fix for large
process/log lists built from actual measurement (not assumption — an
initial hypothesis about *where* the slowness was turned out to be
wrong, and the fix targets what measurement actually found), a
bounded-memory fix for long-running log sessions, and one real
information-hierarchy fix on the Overview screen (a raw
connection-diagnostics panel that had grown to the same visual weight
as the CPU/memory panels next to it, moved behind an explicit toggle).

Phase 8's security review traced every claim SECURITY.md makes against
the actual code — not just read and trusted — and, across the whole
hardening pass, found six real, distinct bugs, each found, fixed, and
empirically re-verified rather than assumed fixed from the diff: a
Windows command-injection vulnerability (verified by cross-compiling
for the real target from this Linux-only environment); two
security-critical tests that didn't actually prove what they claimed to
(verified by temporarily breaking what they were meant to guard and
confirming they now catch it); a gap in automation's self-triggering
protection that left spec's own flagship example unprotected against a
crash-restart loop; a container-CLI argument-injection issue verified
against a real Docker daemon this project's own sandbox stood up
mid-session (not assumed from convention); a stale-pointer race in the
scheduler's scan loop, found by reading `Start`/`scanOnce`/`fire`
together as a whole rather than each function in isolation, capable of
silently resurrecting a deleted task or reverting a concurrent update
in the database; and a cooldown-vs-conditions ordering bug in
automation, found by crash-testing multiple rules against real
concurrent events rather than any unit test (which only ever exercises
one rule against one event at a time), that produced misleading —
though never actually incorrect — reasons for why a rule didn't fire.

Also added: a real circuit breaker for repeatedly-failing automation
rules (auto-disable after 5 consecutive failures), closing a gap
SECURITY.md's cooldown-floor fix had explicitly named as a follow-up
but not yet built — the difference between a crash-restart loop running
at a *bounded rate* and one that eventually *stops*.

Phase 8 also ran real crash/recovery testing against the compiled
binary (`kill -9`, not just a graceful shutdown) — including crash
timing during automation rule execution and event-bus delivery
specifically, which is what surfaced the ordering bug above — real
goroutine-leak and CPU/memory profiling under idle load, sustained
request volume, a large real process list, and sustained (not just
bursty) scheduler/automation activity, and a deliberate
concurrency-pattern review (lock ordering, close/publish races,
semaphore panic-safety) beyond what the race detector alone catches,
now covering every package's scan/event loops as a cohesive whole, not
just their individual functions. See CHANGELOG.md's Phase 7 and Phase 8
entries (including the three most recent entries at the top of the
file, added after Phase 8's own section but closing out nearly
everything it once left open) for the full, detailed account of all of
this — it's extensive, and worth reading if you want to know exactly
what's been checked and how, not just that it has been.

**What's still genuinely open, stated plainly:** a broader "database
review"/"error-path review" beyond the paths the security review and
`internal/app/retention.go`'s now-complete test coverage happened to
touch, and gateway/DNS network configuration (deferred within Phase 6
itself). And the one gap that's been open since Phase 2 and that no
amount of sandbox cleverness closes: this project has never run on real
Windows or macOS hardware. Phase 8 narrowed that gap somewhat —
cross-compiling for both targets is now a standard verification step,
and it's how the Windows vulnerability above was confirmed fixed — but
cross-compiling isn't running. See "The honest gap" below.

## Why FORGE exists

Every technical person ends up with the same drawer of tools for
understanding their own machine, and none of them talk to each other or
share a data model: a process viewer, a service manager, `tail -f` in six
terminal tabs, `docker ps`, a health-check script someone wrote three jobs
ago, and a text file of "if X happens, do Y" that only exists in someone's
head. FORGE's premise is that these are all views onto the same
underlying reality — your machine's current and recent state — and belong
in one tool with one data model, one event stream, and one place to wire
"when X happens, do Y" into something that actually runs.

## Architecture at a glance

```
Desktop UI (React + TypeScript, in a Tauri/Rust shell)
        │
        │  FWP — FORGE Wire Protocol (JSON envelopes over local HTTP)
        │
FORGE Core (Go) — all application logic lives here
        │
   OS-abstraction interfaces (ProcessProvider, ServiceProvider, ...)
        │
   Platform-specific implementations (Linux / macOS / Windows)
        │
SQLite (local, no server, no account, no cloud)
```

Rust's job is exactly two things: spawn the Go backend and supervise its
process lifecycle, and hand the frontend the connection details it needs
to talk to that backend directly. Every other line of application logic —
protocol handling, persistence, the event bus, and (starting Phase 2) all
system monitoring — lives in Go. See [ARCHITECTURE.md](ARCHITECTURE.md)
for the full picture, including *why* it's built this way and what the
real tradeoffs were.

## Supported platforms

Windows, Linux, and macOS are all first-class targets. FORGE does not
pretend OS capabilities are identical: platform differences are modeled
as explicit interfaces (`ProcessProvider`, `ServiceProvider`, ...) with
per-OS implementations behind them (`internal/system/provider_linux.go`,
`provider_darwin.go`, `provider_windows.go`, and similarly in
`internal/containers`), and the UI is designed to clearly communicate
when a capability isn't available on the current platform rather than
silently failing or crashing. Linux is the only platform any of this has
actually run on — see "The honest gap" below for exactly what that
means and what Phase 8 has done to at least partially verify the
other two.

## Build plan

| Phase | Focus | Status |
|---|---|---|
| 1 | Foundation: repo structure, IPC, config, logging, SQLite, lifecycle | **Done** |
| 2 | System core: CPU/memory/storage/network info, process discovery | **Done** |
| 3 | Process & service management | **Done** |
| 4 | Observability: metrics, event bus (persisted history), logs, health checks | **Done** |
| 5 | Automation: rules, triggers, actions, scheduler | **Done** |
| 6 | Containers & network diagnostics | **Done** |
| 7 | UX refinement pass | **Substantively done** — keyboard nav, measured performance fixes, bounded log memory, Overview cleanup; see Status above |
| 8 | Hardening: race detection, profiling, security review | **Substantially advanced** — four real bugs found/fixed/verified, crash/recovery testing, load profiling, concurrency review; database/error-path review and same-depth concurrency review on remaining packages still open |
| 9 | Documentation & release builds | In progress (this update) |

## What actually works right now

Run it (see [Development setup](#development-setup)) and you get a desktop
window with a sidebar of every planned section. **Overview**, **Storage**,
**Processes**, **Services**, **Health**, **Logs**, **Events**,
**Automation**, **Schedules**, **Network**, **Containers**, and
**Settings** are live; everything else shows an honest "arrives in
Phase N" placeholder instead of a broken or fake screen.

- **Overview** shows real system identity (hostname, OS, kernel, uptime),
  CPU identity, and memory usage, plus — on Linux — live, correctly
  time-sampled CPU utilization. A compact connection-status line (live
  or reconnecting, plus the backend's version) sits below that; Phase 1's
  original manual `system.ping` round-trip and raw heartbeat event
  ticker are still there for anyone who wants them, behind an explicit
  "Connection details" toggle rather than shown by default — they were
  originally always-visible and had grown to the same visual weight as
  the CPU/memory panels above them, which read as unfinished debug
  output on the one screen every session opens to; see CHANGELOG.md's
  Phase 7 entry.
- **Storage** lists mounted filesystems with usage.
- **Processes** lists running processes with client-side search, toggles
  between a flat list and a real parent/child tree, and — click any row —
  opens a detail panel with executable path, command line, live CPU%, and
  memory. From there you can terminate a process (gracefully or by force),
  always behind a confirmation dialog: FORGE never lets a single click end
  a process. The list virtualizes past a few hundred rows — built after
  real measurement showed the actual cost wasn't scrolling (that's
  essentially free) but mounting and per-keystroke filtering, both
  scaling linearly with row count; see CHANGELOG.md's Phase 7 entry for
  the numbers. Every row and the tree are keyboard-navigable, not just
  clickable.
- **Services** lists OS-managed services with search; click one to see
  state, startup mode, dependencies (where the platform exposes them),
  and start/stop/restart/enable-disable actions — Stop and Restart behind
  a confirmation dialog, since either can disrupt something currently
  depending on the service. FORGE never attempts to elevate its own
  privileges to perform a service action that needs them; see
  SECURITY.md.
- **Health** lets you define checks against a process, a service, a TCP
  port, an HTTP endpoint, a command's exit code, or a disk/memory/CPU
  usage threshold — each with its own interval, timeout, and retry
  policy. State transitions (a check going healthy or unhealthy) update
  live and are recorded in FORGE's event history.
- **Logs** shows FORGE Core's own structured log, streaming live, with
  level and text search filters, pause/resume, and export to a text
  file — see SECURITY.md/ARCHITECTURE.md for why this is FORGE's own
  log specifically, not yet the OS system log or other services' logs.
  Bounded to the most recent 5,000 entries even across a long-running
  session with "Follow newest" on — an earlier version grew this
  without limit; see CHANGELOG.md's Phase 7 entry.
- **Events** browses everything FORGE has recorded across every
  subsystem — process terminations, service actions, health check
  transitions, automation runs, and more — with a live tail of anything
  new.
- **Schedules** lets you define one-time or repeating tasks — persisted
  across restarts, with a deterministic policy for what happens to a
  task that was due while FORGE wasn't running (see
  ARCHITECTURE.md/CHANGELOG.md).
- **Automation** lets you wire a trigger (a bus event, or a schedule
  firing) through optional conditions to an ordered list of actions —
  restart a process or service, run an approved command, log an event,
  or send a notification. A rule can never trigger on its own output,
  structurally, not just by convention; every rule also has a minimum
  1-second cooldown floor even if none is configured, closing a gap
  where a rule built exactly like spec's own "restart on crash" example
  had nothing bounding how fast a restart loop could spin. A rule that
  fails 5 times in a row, with no success in between, auto-disables
  itself rather than retrying forever at whatever rate the cooldown
  floor still allows — visible on the rule itself, not a silent pause —
  see SECURITY.md.
- **Network** shows every interface with its addresses and up/down
  state, plus a filterable, searchable table of the machine's actual
  socket table — every listening port and active connection, with the
  owning process where FORGE has permission to see it. Real
  `/proc/net/tcp` parsing on Linux, not a wrapper around `netstat`.
- **Containers** discovers Docker containers (via the `docker` CLI —
  see ARCHITECTURE.md) with live-ish CPU/memory from `docker stats`,
  lets you start/stop/restart one and read its recent logs, and shows a
  plain explanation instead of an empty table when Docker isn't
  installed or running — which is normal on most machines, not an error.
- A **Diagnostics** panel on Settings snapshots system/disk/network/
  service/health state on demand and exports it as JSON — useful to
  attach when reporting a problem.

**The honest gap:** everything in this build was written without access
to a real Mac or Windows machine; this development environment only had
Linux available. That already meant macOS/Windows system info (Phase 2),
process management (Phase 3), and Linux service management (see
[ARCHITECTURE.md's "Service management"](ARCHITECTURE.md#service-management))
were unverified against real hardware/a real running instance when
first written, disclosed each time. Phase 8 narrowed that gap without
closing it: cross-compiling `internal/system` for `GOOS=windows` and
`GOOS=darwin` from this Linux sandbox is now a standard verification
step for any change to those files, and it's how Phase 8's Windows
command-injection fix was confirmed to actually build and vet clean for
its real target — a genuine, if partial, check (it proves the code
compiles correctly for that platform; it cannot run it, and cannot
catch a bug that only manifests at runtime). Real Windows and macOS
execution remains the one gap no amount of sandbox cleverness closes.
Phases 4 through 8 haven't added a new platform gap of that specific
kind: Phase 4's network throughput metrics and Phase 6's network
connection inspection are both Linux-only by design rather than
unverified guesses elsewhere (see `system.NetIOCounters`'s and
`NetworkConnections`' doc comments), and Phase 5 added no
platform-specific code at all. Every phase since 4 has kept to the same
practice: this codebase is actually compiled, run, and tested, not just
written carefully against documentation. Phase 6 built and ran a real
Docker container in this project's own sandbox to capture actual
`docker ps`/`docker stats` output for its parser tests rather than
writing fixtures from documentation; Phase 8 went further still —
installing a real Docker daemon from the standard Ubuntu archive
specifically to empirically verify a container-CLI argument-injection
fix (`docker start --rm` really does fail with `unknown flag: --rm`
without the fix, and correctly fails with `No such container: --rm`
with it — checked directly, not assumed from CLI convention), running
real crash tests against the compiled binary (`kill -9`, not a graceful
shutdown, followed by checking `PRAGMA integrity_check` on the raw
SQLite file and confirming the scheduler's missed-execution policy
holds across a real restart), a second round of those same real crash
tests specifically timed against in-flight automation rule execution
and event-bus delivery (which is what found the cooldown-vs-conditions
ordering bug mentioned above), and profiling the real binary's
goroutine count and CPU/memory under idle load, sustained request
volume, a large real process list, and sustained scheduler/automation
activity. See DEVELOPMENT.md's "Toolchain status" sections for
exactly what verification has and hasn't covered across every phase.
The Rust/Tauri shell specifically: Phase 9 investigated this properly
rather than repeating the same disclaimer — a Rust toolchain (1.75, via
the standard Ubuntu archive) and every Tauri Linux system library both
installed and resolved cleanly, and dependency resolution reached real
crates.io downloads before hitting one concrete, diagnosed blocker: a
transitive dependency (`tauri` → `cookie` → `time` → `time-core`)
requires Cargo's `edition2024` feature, stabilized in Rust 1.85 — newer
than what's reachable via this sandbox's network allowlist (no
`rust-lang.org`/rustup access, and no newer `rustc` in the Ubuntu
archive). See DEVELOPMENT.md's Phase 9 entry for the full trace. The
actual `cargo tauri build` step remains untested either way. Please
report back if you're the first to test any of the still-unverified
pieces for real, starting with actual Windows or macOS hardware and a
Rust 1.85+ toolchain for the Tauri shell.

## Development setup

Requires Go 1.22+, Rust 1.85+ + Cargo, Node 18+, and the Tauri CLI (the
1.85+ specifically because of a transitive Tauri dependency's Cargo
edition2024 requirement — see "The honest gap" above; an older Rust
will fail partway through `cargo` dependency resolution, not at the
final build step). See [DEVELOPMENT.md](DEVELOPMENT.md) for exact
steps, platform notes, and — importantly — what's actually been
compiled, run, and tested in this project's various sandbox
environments across every phase (the backend extensively, including
full-suite `-race` runs, real crash testing, and real load profiling;
the frontend extensively; the Rust/Tauri shell partially — a sufficient
toolchain has never been available, but Phase 9 got real system
dependencies and real crate resolution working before hitting that
specific version wall). You would be the first person to build and run
the complete desktop application end to end.

```bash
./scripts/dev.sh
```

## Documentation

- [INSTALL.md](INSTALL.md) — end-user installation (there's no release
  build yet — see that document for exactly what that means and what
  installation will look like once one exists).
- [ARCHITECTURE.md](ARCHITECTURE.md) — system design, the protocol, the
  event bus, persistence, OS abstraction, and the future remote-agent
  architecture.
- [DEVELOPMENT.md](DEVELOPMENT.md) — environment setup, build/run/test
  instructions, and project conventions.
- [SECURITY.md](SECURITY.md) — the security model and how to report an
  issue.
- [CONTRIBUTING.md](CONTRIBUTING.md) — how the codebase is organized and
  what a good change looks like.
- [CHANGELOG.md](CHANGELOG.md) — what shipped in each phase.
