# Architecture

This document explains how FORGE is put together and, more importantly,
*why* — the tradeoffs behind each non-obvious decision, so a future
contributor (including future us) doesn't have to reverse-engineer intent
from code. It reflects the system as of Phase 8; sections marked
**(future)** describe the design later phases are expected to fill in, not
code that exists yet.

## Layers

```
┌─────────────────────────────────────────────────────────────┐
│ Desktop UI — React + TypeScript                              │
│   apps/desktop (Rust/Tauri)  ←→  frontend (React)             │
└───────────────────────────────┬───────────────────────────────┘
                                 │ FWP over HTTP (loopback)
┌───────────────────────────────▼───────────────────────────────┐
│ FORGE Core — Go                                                │
│                                                                 │
│  internal/ipc      transport: FWP envelopes, routing, auth     │
│  internal/app      composition root, lifecycle                 │
│  internal/eventbus in-process pub/sub                          │
│  internal/storage  SQLite, migrations, repositories             │
│  internal/config   on-disk configuration                        │
│  internal/logging  structured logging                           │
│  internal/system   OS abstraction interfaces + linux/darwin/    │
│                     windows implementations                     │
│  internal/health   health check engine (spec section 9)         │
│  internal/metrics  metrics engine (spec section 17)             │
│  internal/logs     FORGE Core's own log, queryable/streamable   │
│  pkg/fwp           the wire protocol (shared, reusable)         │
│                                                                 │
│  (future) internal/automation  rule engine + scheduler          │
│  (future) internal/containers  container discovery               │
└─────────────────────────────────────────────────────────────┘
```

The rule that shapes every package boundary above: **`internal/ipc` knows
nothing about what any method does.** It is pure transport — envelope
encode/decode, routing by method name, auth, streaming. Method
*registration* (what `system.ping` does) lives in `internal/app`, the
composition root, so a future `internal/process` or `internal/service`
package can register its own methods without `internal/ipc` ever importing
or knowing about process or service concepts. This is the same reason
`internal/eventbus` defines no event types beyond its own three lifecycle
events (`system.backend_started`, `system.backend_stopping`,
`system.heartbeat`) — domain packages own their own event vocabulary.

## Why Rust stays thin

The spec for this project is explicit that business logic belongs in Go,
not Rust, and Phase 1's architecture makes that close to structurally
enforced rather than just a convention to remember. `apps/desktop/src/main.rs`
does exactly four things: spawn the `forge-core` binary, read one line of
JSON from its stdout, expose that connection info to the frontend through
a single Tauri command, and — on exit — make one HTTP request asking it to
shut down gracefully. There is no protocol logic, no business logic, and
deliberately no HTTP client dependency (the one outbound request Rust ever
makes is a hand-written raw HTTP/1.1 request over a `TcpStream` — see
"IPC handshake and shutdown" below). If a future change finds itself
adding a second Tauri command that does anything beyond "ask FORGE Core"
and "relay the answer," that's a signal the logic belongs in Go instead.

## The protocol: FWP (FORGE Wire Protocol)

`pkg/fwp` defines a single message shape, `Envelope`, used for every kind
of communication FORGE needs:

```go
type Envelope struct {
    V       int
    Type    EnvelopeType // request | response | error | event | cancel
    ID      string       // correlates request ↔ response/error/cancel
    Method  string        // request only
    Params  json.RawMessage
    Result  json.RawMessage
    Error   *ErrorObject
    Channel string        // event only
    Seq     uint64        // event only
    Payload json.RawMessage
}
```

It's deliberately transport-agnostic at the type level and has zero
dependencies beyond the standard library. That matters for two reasons:
today it's carried over local HTTP; **(future)** the same `Envelope` shape
is meant to be carried over a TLS connection to a remote FORGE Agent
without any change to method contracts — see "Looking ahead: remote
agents" below.

### Why HTTP instead of a WebSocket

A single persistent WebSocket is the more obvious transport for a protocol
with commands, streaming events, and cancellation, and an earlier draft of
this system used one. We changed course for a concrete reason: Go's
standard library has no WebSocket implementation. Building FWP on a raw
WS transport meant either

1. hand-writing an RFC 6455 frame parser (masking, control frames,
   fragmentation, the close handshake) — real, well-understood, and also
   exactly the kind of intricate binary-protocol code that's easy to get
   subtly wrong in ways that only surface under load, with no way to
   verify it by actually compiling and running it in the environment this
   codebase was authored in (see [DEVELOPMENT.md](DEVELOPMENT.md)); or
2. taking a dependency on a WebSocket library chosen and pinned without
   the ability to compile against it.

Instead, FWP in Phase 1 is carried over two plain `net/http` endpoints:

- **`POST /rpc`** — one request `Envelope` in, one response/error
  `Envelope` back, synchronously, in the HTTP response body.
- **`GET /events?channel=a,b,c`** — a long-lived response streaming
  newline-delimited JSON (`application/x-ndjson`) event `Envelope`s,
  flushed as they occur, for as long as the connection stays open.

This maps every functional requirement FWP needs onto something we could
write with full confidence in its correctness:

| Requirement | How it's met |
|---|---|
| Commands | `POST /rpc`, `Envelope.Type = request` |
| Responses | The synchronous `POST /rpc` response body |
| Streaming events | `GET /events`, one flushed NDJSON line per event |
| Subscriptions | Opening the `GET /events` connection *is* subscribing (optionally filtered by `?channel=`); closing it is unsubscribing |
| Errors | `Envelope.Type = error`, carrying a structured `ErrorObject` |
| Cancellation | The client aborts the underlying HTTP request (e.g. a JS `AbortController`); Go's `net/http` cancels the handler's `r.Context()` automatically when the client disconnects mid-request — see [`http.Request.Context`](https://pkg.go.dev/net/http#Request.Context) |
| Versioning | `Envelope.V`; unknown methods return a structured `UNSUPPORTED_METHOD` error rather than a transport failure |

The cost is real: two connections instead of one, and cancellation is
implicit (an aborted HTTP request) rather than an explicit protocol
message in the common case — though `Envelope.Type = cancel` is defined in
`pkg/fwp` precisely so a future persistent-connection transport can
express that intent explicitly without changing the wire format. Nothing
in `pkg/fwp.Envelope` or in how `internal/app` registers methods assumes
HTTP; a WebSocket (or QUIC, or whatever a remote-agent transport turns out
to need) transport can be added in `internal/ipc` alongside the HTTP one
later without touching method implementations.

### IPC handshake and shutdown

FORGE Core has no fixed port and no persisted credential. On startup it
binds an OS-assigned ephemeral loopback port, generates a fresh random
bearer token, and prints exactly one line of JSON to stdout the instant
it's ready to accept connections:

```json
{"port": 54231, "token": "…64 hex chars…", "pid": 48213}
```

The Tauri shell reads that line, stores it, and exposes it to the frontend
via a single `get_backend_endpoint` command. The frontend then talks to
FORGE Core directly — Rust is not a relay for every request, only the
initial handoff. Every request must present the token as a `Bearer` header
(constant-time compared, via `crypto/subtle`) or a `?token=` query
parameter for the streaming `GET /events` connection where setting a
header isn't convenient from `fetch`. `internal/ipc.NewServer` refuses to
bind anything but a loopback address, full stop — this is a local-only
listener by construction, not by configuration discipline.

Shutdown uses the same mechanism on every platform: the desktop shell
issues `POST /shutdown` (authenticated the same way as everything else)
when the app window closes. FORGE Core's HTTP handler closes a channel
that `internal/app.Run`'s main select loop is watching, which then drives
the same coordinated goroutine teardown as an OS signal would. We chose
this over OS-level process signaling (`SIGTERM` on Unix, closing a console
handle on Windows) specifically because it is *one* code path with
identical behavior on Linux, macOS, and Windows, rather than three
platform-specific ones — see the comment in `cmd/forge-core/main.go` for
why OS signal handling still exists too, as defense in depth for directly
running `forge-core` outside the desktop shell (e.g. during development).

## Concurrency and lifecycle

Every background goroutine in FORGE Core is owned by exactly one
`context.Context`, created and canceled in exactly one place:
`internal/app.App.Run`. It derives `runCtx` from the `ctx` it's given (an
OS-signal context from `cmd/forge-core`, in normal operation), starts
every engine and background loop with `runCtx`, and does not return from
`Run` until a `sync.WaitGroup` confirms every one of them has actually
exited. This is what "no goroutine leaks" means concretely in this
codebase: not "we believe nothing leaks," but "`Run` cannot return while
something is still running unaccounted for."

`Run`'s subsystem *start order* is deliberate, not incidental, and grew a
real constraint in Phase 5: `internal/automation.Engine.Start` runs
first, before `scheduler.Engine.Start` or `health.Engine.Start`, because
either of the latter two can publish a bus event from a background
goroutine immediately upon starting (an overdue scheduled task, an
immediate first health check result), and `automation.Engine` needs its
own bus subscription active before that can happen or it silently misses
the event. This is the same category of fix Phase 4 applied to
`persistence.go` below, generalized: *any* subscriber whose job is to
observe another subsystem's very first event must finish subscribing
before that subsystem starts, and `Engine.Start`'s doc comment in each
package says so explicitly rather than leaving the ordering constraint
implicit in `Run`'s call sequence alone.

The one place this gets subtle is `internal/app/persistence.go`, which
bridges the event bus to SQLite. It intentionally subscribes with
`context.Background()` rather than `runCtx`, so *it* controls exactly when
it unsubscribes rather than racing the bus's own auto-unsubscribe-on-
cancel — and then does a final non-blocking drain of its subscription
channel after `ctx.Done()` fires, so an event published in the same
instant as shutdown (specifically: `system.backend_stopping`, which `Run`
publishes immediately before canceling `runCtx`) isn't lost to Go's
pseudo-random `select` case ordering. The comment at the top of
`persistence.go` explains this in full; it's the kind of race that's easy
to introduce accidentally and worth calling out explicitly when it's
handled on purpose.

`internal/eventbus.Bus.Subscribe` has its own small lifecycle detail worth
noting: it always spawns one goroutine to watch `ctx.Done()` (so a
subscription can auto-expire when its context is canceled), but that
goroutine also watches an internal `stopWatch` channel closed by the
returned `cancel` func — so calling `cancel()` directly (as
`persistence.go` does, with `context.Background()` which never cancels on
its own) still terminates the watcher goroutine immediately rather than
leaking it for the life of the process. See `bus_test.go`'s
`TestManualUnsubscribeDoesNotLeakWatcherGoroutine` for the regression test
this fixed during development.

## Persistence

SQLite via [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite), a
pure-Go, CGO-free driver, rather than `mattn/go-sqlite3` (which wraps the
real C library via CGO). The tradeoff is real — CGO-backed SQLite is
faster under heavy concurrent write load — but FORGE's write volume is
modest even with Phase 4/5's metrics/health/log/scheduler/automation
engines all running (batched metric aggregates on a 60s flush cycle, not
raw per-sample writes; health check results and scheduler task state at
each item's own multi-second-or-longer interval; automation run results
only on an actual execution, never a skip; log entries held only in
memory, never written to SQLite at all — see `internal/logs`'s package
doc comment). In exchange, every developer and every release build gets
trivial cross-compilation to Windows/Linux/macOS with no C toolchain
requirement on the build machine, which matters far more for a
cross-platform desktop tool built by (potentially) contributors on three
different operating systems than write throughput headroom we don't
currently need.

`internal/storage.Open` uses **exactly one physical connection**
(`SetMaxOpenConns(1)`), not a pool — a Phase 4 correction, not the
original design. The original reasoning ("SQLite serializes writers
regardless of pool size, so a bigger pool only helps concurrent reads")
was true but incomplete: it missed that `PRAGMA busy_timeout` (applied as
plain SQL rather than a DSN parameter, precisely to avoid depending on
any particular driver's DSN pragma syntax) only reaches whichever single
connection happens to execute that statement, not every connection a
pool might open later under load. Phase 4's own end-to-end test
(`internal/app/app_test.go`) caught this directly — a second pooled
connection opened under concurrent load from the health/metrics/
event-persistence subsystems failed instantly with "database is locked"
instead of waiting, because it never received the timeout. A single
connection makes that failure mode structurally impossible. WAL mode
(also applied at `Open`) is still worth keeping even with one connection:
it's more crash-resilient than the default rollback journal, and it's
what would let a future short-lived read-only connection (e.g. a
diagnostics tool inspecting the database file directly) avoid blocking
on a writer.

Migrations are hand-rolled rather than a dependency
(`golang-migrate`/`goose`/etc.): `internal/storage/migrate.go` embeds
`migrations/*.sql` via `go:embed`, tracks applied versions in a
`schema_migrations` table, and applies anything new in filename order
inside its own transaction. It's about 80 lines, has full test coverage,
and the spec's stated preference for the standard library over
dependencies "unless they provide substantial value" didn't feel met here
— a migration runner this small doesn't earn a dependency. Phase 4 added
two more migrations this way (`0002_health_checks.sql`,
`0003_metric_aggregates.sql`) and Phase 5 two more still
(`0004_scheduler_tasks.sql`, `0005_automation_rules.sql`), each
introduced in the same change as the code that uses it, exactly as
`0001_init.sql`'s comment always said later phases would.

## Retention

`internal/app/retention.go` runs an hourly sweep (plus once at startup)
that deletes events older than `Config.EventsRetentionDays` (30 by
default) and, as of Phase 4, metric aggregates older than
`Config.MetricsRetentionDays` (14 by default, a config field that existed
since Phase 1's scaffold but did nothing until now). Both are pure
deletion, not downsampling — an earlier draft of this document predicted
the metrics engine would need "rolling averages, downsampling older data
to coarser granularity"; what actually got built is simpler than that.
The downsampling need that prediction was gesturing at is already met a
different way: `internal/metrics` never stores raw per-sample data in the
first place (see "Observability engine" below) — every row already *is*
a min/avg/max bucket, aggregated in memory before it ever reaches SQLite.
Retention's job is only ever "delete what's old," on both tables, which
is what let it stay one small, shared piece of code rather than growing
metric-specific logic. Phase 5's `scheduler_tasks` and
`automation_rules` tables need no equivalent sweep: like
`health_checks`, they're small, user-configured collections (one row per
task/rule someone deliberately created), not unbounded time-series data
— there's nothing in either table that grows on its own the way events
and metric aggregates do just from FORGE running.

## Observability engine (Phase 4)

Spec sections 8–10 and 17 (log engine, health check engine, event bus
vocabulary, metrics engine) share one architectural decision worth
calling out explicitly: **`internal/health` and `internal/metrics` each
define their own `Repository` interface** (in `health/repository.go` and
`metrics/repository.go` respectively) rather than depending on
`internal/storage` or `database/sql` directly. `internal/storage`
implements both interfaces (`HealthRepository`, `MetricsRepository`)
using SQLite, but the engines themselves only ever see the interface.
This is the same dependency-inversion shape `internal/system`'s
`Provider` interfaces use for OS-specifics, applied here for a different
reason: it's what made both engines fully unit-testable — including
concurrency and event-publishing behavior under `-race` — with an
in-memory fake, in a development sandbox that (see DEVELOPMENT.md)
could not reach `modernc.org/sqlite` to build `internal/storage` at all
for most of this phase's authoring. `internal/logs` needed no such
interface: it never persists anything (see below), so it has nothing to
invert.

**Metrics** (`internal/metrics`, spec section 17): three independent
collection loops — CPU+memory every 5s, disk every 30s, network
throughput every 5s — each on its own goroutine, each writing into a
per-series in-memory structure with two distinct roles that were
originally one and had to be split apart: a bounded *rolling window*
(~30 minutes at these intervals) that `metrics.current`/`metrics.rolling`
read directly with no database involved, and a *pending accumulator*
that a separate 60-second flush loop aggregates to one min/avg/max row
per active series and clears — deliberately never the same buffer,
because early on it was, and every flush wiped out the rolling window a
chart would be reading from. Network throughput is computed from two
time-separated cumulative counter readings (`system.NetworkIOCounters`,
new this phase) rather than the OS handing over a rate directly — see
that method's doc comment on why that's the right layer for a rate to
be computed at.

**Health checks** (`internal/health`, spec section 9): a scheduler
bounded to 8 concurrently executing checks via a counting semaphore
(`chan struct{}` sized 8), regardless of how many checks are configured
— the concrete answer to "must run concurrently but must have bounded
resource usage." Each check gets its own goroutine ticking at its own
configured interval; `Engine.Create`/`Update`/`Delete` start/stop these
dynamically while the scheduler as a whole keeps running, guarded by a
two-level locking scheme (`Engine.mu` for the *set* of checks, changed
rarely; each `trackedCheck`'s own mutex for its *live status*, changed on
every evaluation) rather than one lock for both, since those two things
change at very different frequencies. Bus events fire only on state
*transitions*, not every evaluation — a check re-confirming HEALTHY every
30 seconds forever would otherwise flood the event history with rows
carrying no new information.

**Logs** (`internal/logs`, spec section 8): deliberately scoped to
FORGE Core's own structured log output only — not the OS system log,
not other services' logs, not arbitrary processes' stdout/stderr. Each
of those needs a genuinely different, platform-specific reader
(journald, the macOS Unified Logging System, the Windows Event Log, a
per-process pipe capture), and bundling that scope creep into this phase
would have meant either doing all of it shallowly or none of it well.
What exists is a bounded (5000-entry) in-memory ring buffer, fed by a
new hook on `internal/logging` — `Options.Tap`, called with the original
`slog.Record` on every log call, in addition to (never instead of) the
normal handler chain. A `Tap` observes structured data directly rather
than a `internal/logs` re-parsing formatted text back out of the rotated
log file, which would be strictly lossier for no benefit. Nothing here
is persisted to SQLite — the ring buffer's bound is the whole retention
story, and it resets on restart, which is an accepted tradeoff for a
tool whose durable audit trail already lives in `events`, not `logs`.

## Automation and scheduling engine (Phase 5)

Spec sections 11–12 (rule engine, scheduler) split into two packages —
`internal/scheduler` and `internal/automation` — with a specific,
one-directional dependency: automation depends on scheduler, never the
reverse. `internal/scheduler` publishes `scheduler.task_due` on the bus
and otherwise knows nothing about rules, triggers, or actions; it would
be exactly as complete a package if automation didn't exist. This
mirrors the health/metrics `Repository`-interface split from Phase 4 in
spirit — decoupling through an interface (or, here, through the event
bus) rather than a direct call — for the same payoff: each package is
independently testable, and `internal/scheduler`'s tests never had to
know `internal/automation` exists.

**Scheduler** (`internal/scheduler`, spec section 12): one shared 1-second
scan loop rather than a goroutine per task — a deliberate contrast with
`internal/health`'s one-goroutine-per-check design. A health check's
execution has variable duration and its own timeout/retry policy, which
genuinely warrants its own goroutine; a scheduled task becoming due is
just an in-memory timestamp comparison, cheap enough at any realistic
task count that one shared loop is simpler and equally correct. The
missed-execution policy (spec: "handle missed executions
deterministically," "prevent duplicate execution after application
restart") comes down to two rules, both load-bearing: `Start`'s first
scan happens immediately, before the regular ticker begins, so a task
overdue because FORGE wasn't running fires exactly once at startup, not
once per interval that elapsed while offline; and every `NextRunAt`
advance is persisted *before* `scheduler.task_due` is published, not
after — durability-before-side-effect, so the only way a crash between
the two can go wrong is one missed publish, never a duplicate one. This
"prefer a miss to a duplicate" tradeoff shows up again, deliberately
consistently, in automation's own execution recording below.

`scanOnce` collects the `*Task` pointers it finds due under `e.mu`, then
releases the lock before `fire` runs each one — actions shouldn't run
under the lock. `fire` re-validates, under `e.mu`, that its pointer is
still `e.tasks[id]`'s live entry before touching anything, and skips
silently otherwise. This matters because a concurrent `Delete` or
`Update` can land in the window `scanOnce` released the lock: without
the check, a raced `Delete` would have `fire` silently resurrect the
task in the repository (`Upsert` is an unconditional `INSERT ... ON
CONFLICT DO UPDATE`, with nothing to tell it the row was deleted on
purpose), and a raced `Update` would have `fire` persist a snapshot
computed from the configuration `Update` just replaced, racing
`Update`'s own write for whichever lands last. `Update` installing a
brand-new `*Task` rather than mutating the old one in place (unlike
`SetEnabled`) is what makes the second case detectable by pointer
identity at all. Found by reading `Start`/`scanOnce`/`fire` together as
a whole rather than each function in isolation — every individual
function was already safe on its own (see CHANGELOG.md).

**Automation** (`internal/automation`, spec section 11): Trigger →
Conditions → Actions, matched against every event on the bus. A
`TriggerEvent` rule matches by comparing its configured `EventType`
against each incoming event's `Type` directly — which is *why* spec
section 11's "health-check results" and "thresholds" trigger sources
needed no special handling here: by the time a health check fails, it's
already `health.check_failed` on the bus (Phase 4), indistinguishable
from any other event `Engine.handleEvent` sees. `TriggerSchedule`
matches `scheduler.task_due` events whose `task_id` payload field equals
the rule's configured `ScheduleID`. Conditions are intentionally just
flat `field == value` checks, AND-ed together, evaluated against
whichever payload the triggering event carried (a health event's
payload, or `scheduler.task_due`'s `{task_id, name}` — the same code
path either way, no special-casing by trigger type): every event
payload in this codebase is a flat struct, so a richer expression
language would be complexity with nothing to apply it to.

Actions run sequentially, in the order configured, and a failed action
does not abort the ones after it — the motivating case is a rule shaped
like `[restart_process, create_event]`, where the `create_event` action
exists specifically to report what happened, including a failure;
aborting before it ran would silence the one signal a failure most
needs. Whole-rule execution is bounded to 8 concurrent rules via a
counting semaphore, the same pattern and the same number as
`internal/health`'s check-execution bound, for the same reason: a burst
of simultaneously-triggering events (a real incident setting off several
health checks at once) must not be able to spawn unbounded goroutines —
but a semaphore only bounds concurrency, it doesn't bound *duration*;
what makes the bound actually load-bearing is
`executeActions` wrapping every individual action in
`context.WithTimeout(ctx, e.actionTimeout)` (30 seconds by default,
injectable — the same pattern as `clock` — specifically so a test can
prove a genuinely hung command gets killed without waiting out a real
30 seconds). Without it, a single action that hangs rather than fails
(a `run_command` pointed at something that never exits; a
`restart_process`/`start_service`/`restart_service` against something
unresponsive) would hold its semaphore slot forever, and as few as 8
such rules firing would silently exhaust the semaphore for every other
rule — found by reading `actions.go` and `engine.go` together, the same
way the scheduler's own stale-pointer race above was found (see
CHANGELOG.md).

`tryRun` evaluates Conditions before checking cooldown, not the
reverse — a deliberate ordering, and not the original one. `handleEvent`
filters its matched-rule list by `Trigger.Type` alone; Conditions are
only evaluated once a rule reaches `tryRun`. Checking cooldown first
meant a rule already cooling down from its own prior, unrelated
execution would report `RunSkippedCooldown` for an event its Conditions
never matched in the first place — true in the narrow sense that it
*was* cooling down, but wrong about why *that event* didn't fire it,
and only surfaces with multiple rules sharing one `Trigger.EventType`
but different `Conditions` racing real concurrent events — found by
crash-testing several check-specific automation rules together, not by
any unit test exercising one rule against one event at a time (see
CHANGELOG.md). Reordering was confirmed safe before making it:
`checkCooldown` and Conditions evaluation are both pure reads, and
`recordOutcome` never touches tracked state for either skip status — so
the fix changes only which accurate reason gets reported, never whether
a rule executes.

A per-rule `CooldownSeconds`, checked against the timestamp of the
rule's last *actual execution* specifically (never a skip — see
`trackedRule.checkCooldown`'s doc comment), guards against a rapidly
repeating trigger turning one rule into a storm of restarts — see
SECURITY.md's "Automation actions" for the `minRuleCooldownSeconds`
floor this alone still needed. That floor bounds the *rate* of a
crash-restart loop, never *whether* it runs forever; `MaxConsecutiveFailures`
(`internal/automation/validate.go`) is the rest of that fix: 5
consecutive `RunFailed` outcomes, no success in between, auto-disables
the rule (`trackedRule.recordOutcome`) rather than leaving it retrying
indefinitely. The trip and the run result it happened alongside persist
in one atomic write (`automation.RunResult`, replacing what used to be
four separate positional parameters to `Repository.UpdateRunResult`) —
deliberately, so a crash between "run recorded" and "the auto-disable it
triggered" can't happen. `Update` resets the streak to 0: editing a
rule that tripped the breaker is almost always an attempt to fix and
re-enable it, and a near-tripped streak surviving that edit would
silently re-trip it on the next hiccup instead of the fresh grace
period an operator turning it back on would expect.

**Self-triggering loop prevention** is the one safety property worth
naming on its own: every event `internal/automation` publishes
(`automation.rule_executed`, `automation.custom_event`,
`automation.notification`) shares the `automation.` prefix, and no
`Trigger.EventType` may target that prefix — checked once at
`Rule.applyDefaultsAndValidate` (so a self- or cross-rule trigger loop
can never be *configured*), and checked again, defensively, in
`Engine.handleEvent` itself (so a hypothetical future bypass of
validation — a direct database edit, a migration that skips
revalidation — still can't reintroduce one). Both checks are the same
one-line prefix comparison; the redundancy is intentional, cheap
insurance against a class of bug this project's spec explicitly warned
against building.

`restart_process`/`stop_process` match running processes by
case-insensitive exact name — the identical matching rule
`internal/health`'s `process_exists` checker uses, kept deliberately
consistent so "does this process exist" (a health check) and "act on
this process" (an automation action) always agree on what "this
process" means. `restart_process` additionally launches a replacement
via `os/exec` + `cmd.Process.Release()` — a genuine fire-and-forget
launch (FORGE does not supervise the new process afterward: no captured
output, no restart-on-crash of *that* process, no confirmation the old
one actually exited before the new one starts) — see
`CHANGELOG.md`'s "Known limitations" for what that does and doesn't
cover. `run_command` execs argv directly via `exec.CommandContext`,
never a shell, identical to `internal/health`'s `command` check type —
see `SECURITY.md`'s "Automation actions."

## Containers and diagnostics (Phase 6)

Two additions, deliberately not one bigger subsystem — `internal/containers`
(spec section 15) and `internal/diagnostics` (spec section 23) solve
different problems and have almost nothing in common beyond both being
new in the same phase.

**Containers** talks to Docker exclusively through the `docker` CLI —
`exec.CommandContext`, parsing `--format '{{json .}}'` output — never
the Docker Engine API over its Unix socket, and never a Docker SDK
dependency. This is the same architectural choice `internal/system`
already made for `systemctl`/`launchctl`/PowerShell: shell out to a
well-documented, stable CLI and parse its output, rather than depend on
a client library for that system's native protocol. The payoff repeats
exactly: zero new third-party dependencies, and Docker's own,
already-versioned wire protocol is something the `docker` binary already
handles correctly on FORGE's behalf. Every invocation is argv-only,
never a shell, the same discipline `internal/health`'s `command` check
type and `internal/automation`'s `run_command` action use.

The parsers (`docker ps`, `docker stats`) are tested against fixtures
captured from a *real, running* Docker 29.1.3 daemon in this project's
own sandbox, not hand-written from documentation — including the exact
binary-unit (`MiB`/`GiB`) parsing `docker stats`' `MemUsage` field needs,
verified against real output rather than assumed. A real integration
test (`internal/containers/integration_test.go`) talks to a live daemon
directly when one is available and skips cleanly otherwise, the same
`t.Skip`-when-unavailable pattern used throughout this codebase for
environment-dependent verification (see `internal/system`'s Linux
integration suite). See DEVELOPMENT.md for exactly how this was run for
real, including building a test container from `FROM scratch` since this
sandbox has no registry access to pull a public image.

Docker's absence is treated as ordinary operation, not a degraded state
— `Provider.Availability` is checked and surfaced explicitly (spec
section 15: "show runtime availability"), and the frontend's Containers
page checks it before ever calling `containers.list`, showing a plain
explanation rather than an empty table that looks broken. This mirrors
spec section 15's own framing ("the application must remain useful on
systems without containers") applied concretely: most machines running
FORGE won't have Docker running, and that has to look like normal
software, not a failure.

**Diagnostics** is deliberately *not* an engine: `diagnostics.Generator`
has no background goroutine, no persistence, and does no work beyond
calling into subsystems that already exist — `internal/system` for
machine facts, `internal/health` for check results, a narrow
`eventReader` interface (implemented by `storage.EventRepository`) for
recent event history — and assembling the results into one `Report`.
Every section of that report degrades independently: `CPUInfo` being
briefly unavailable never prevents the storage or network sections from
populating, recorded as that section's own `Error` field rather than
failing the whole report. This is spec section 4's "gracefully degrade"
principle, which the rest of this document already describes for
`internal/system`'s OS abstraction, applied here to the one feature
whose entire purpose is being trustworthy precisely when something else
has already gone wrong — a diagnostics tool that itself throws an error
because one signal was unavailable would defeat its own point.

## What's built as of Phase 8, and what still isn't

Phases 1–5 (foundation through automation/scheduling) plus Phase 6's
containers/network/diagnostics complete spec sections 5–13, 15, 17, and
23. Phase 7's UX pass (spec sections 20–22) is substantively done —
keyboard navigation across every dialog and list/detail screen, a
measured (not assumed) performance fix for large process/log lists, a
bounded-memory fix for long log-streaming sessions, and one real
information-hierarchy fix on Overview. Phase 8's hardening pass (spec
sections 18, 25–27) traced every claim `SECURITY.md` makes against the
actual code and, across the whole pass, found six real, distinct,
independently-verified bugs — a Windows command-injection
vulnerability, two security tests that didn't prove what they claimed
to, a gap in automation's self-triggering protection, a container-CLI
argument-injection issue, a stale-pointer race in the scheduler's scan
loop capable of resurrecting a deleted task or reverting a concurrent
update, and a cooldown-vs-conditions ordering bug in automation that
produced misleading (though never incorrect) skip reasons — plus ran
real crash/recovery testing (including crash timing during automation
rule execution and event-bus delivery specifically, which is what
surfaced the last bug above), goroutine-leak and CPU/memory profiling
under idle load, sustained request volume, a large real process list,
and sustained (not just bursty) scheduler/automation activity, and a
deliberate concurrency-pattern review — now covering `scheduler`'s scan
loop internals as a cohesive whole, not just its individual functions
in isolation, alongside the depth already given to `eventbus` and
`automation` — beyond what `-race` alone catches. Also added in this
pass: a real circuit breaker for repeatedly-failing automation rules
(`automation.MaxConsecutiveFailures`), the follow-up `SECURITY.md`'s
cooldown-floor fix had explicitly named but not yet built, closing the
gap between "bounded rate" and "eventually stops." See `CHANGELOG.md`'s
Phase 7 and Phase 8 entries for the full account of each, including
what each verification method could and couldn't prove and where the
limits of this sandbox (no real Windows/macOS hardware, no external
Docker registry access) shaped what "verified" means for a given claim.

Still genuinely open, not just formally unstarted: a broader "database
review"/"error-path review" beyond the paths the security review and
`internal/app/retention.go`'s now-complete test coverage happened to
touch; gateway/DNS network configuration (deferred within Phase 6
itself — see `CHANGELOG.md`'s "Known limitations"); and Phase 9's
remaining release-build and installation-process work. The one gap that
predates all of this and that no amount of sandbox diligence closes:
nothing in this codebase has ever executed on real Windows or macOS
hardware, or through the Rust/Tauri shell at all — see the README's
"The honest gap" for exactly what cross-compilation checking can and
can't stand in for.

## OS abstraction

`internal/system` defines the interfaces spec section 4 asks for —
`SystemInfoProvider`, `CPUProvider`, `MemoryProvider`, `StorageProvider`,
`NetworkProvider`, `ProcessProvider`, composed into one `Provider` — and
one concrete implementation per platform, selected at compile time via Go
build constraints (`provider_linux.go`, `provider_darwin.go`,
`provider_windows.go`, each `//go:build <os>`). `internal/app` holds the
only call to `system.New()`; every FWP `system.*`/`process.*` method
handler receives the resulting `Provider` and is otherwise unaware of
which platform it's actually running against — see `internal/app/methods.go`.

A capability that's genuinely unavailable is `system.ErrUnavailable`
(package-level sentinel, no dependency on the protocol layer — see
`internal/system/errors.go`), translated by `internal/app/methods.go`'s
`toFWPError` into `fwp.ErrUnavailable` at the transport boundary. Phase
2's one real instance: `CPUUsage` returns it on both Darwin and Windows
(see "Darwin and Windows" below for why), so the frontend can show "not
available on this platform" rather than a spinner that never resolves or,
worse, a silently wrong number.

### Linux: direct `/proc` parsing

`provider_linux.go` and `linux_parse.go` read `/proc` directly — no
dependency, no subprocess, no cgo. This was also the one platform whose
exact file formats this codebase's author could check against a real
machine while writing it (this repository's own sandbox environment is
Linux); `linux_parse_test.go`'s fixtures are captured verbatim from real
`/proc/stat`, `/proc/meminfo`, `/proc/cpuinfo`, `/proc/[pid]/stat`,
`/proc/net/dev`, and `/proc/mounts` content, not hand-guessed
approximations, and `provider_linux_integration_test.go` additionally
exercises the real implementation against the real running OS rather than
fixtures. The trickiest single piece of this format: `/proc/[pid]/stat`'s
second field (the process name, `comm`) is parenthesized and may itself
contain spaces or parentheses, so it cannot be split on whitespace
naively — `parseProcPidStat`'s doc comment and
`TestParseProcPidStatHandlesParensAndSpacesInCommandName` cover exactly
this.

All of the real I/O-touching logic (open a file, read it) is kept in
`provider_linux.go`; all of the parsing (turn that content into FORGE's
types) is in `linux_parse.go` as pure functions taking a `string` and
returning a value or error — the split exists specifically so parsing
correctness can be verified with fixture data independent of whether a
test happens to be running on a machine with the exact process/mount/CPU
layout a value-specific assertion would need.

### Darwin and Windows: `os/exec` over syscalls

Neither macOS nor Windows has an equivalent to `/proc` — their real
low-level equivalents are Mach/IOKit calls (macOS) and Win32 API calls
(Windows), both normally reached via cgo or hand-written syscall
bindings. Both are meaningfully harder to get right without a compiler to
check the work against, which authoring this codebase did not have (see
DEVELOPMENT.md). `provider_darwin.go` and `provider_windows.go` instead
lean on `os/exec` with fixed argument lists (never a shell string built
by concatenation — SECURITY.md) against long-stable, well-documented
command-line tools: `sysctl`, `vm_stat`, `df`, `ps` on macOS;
PowerShell's `Get-CimInstance` CIM cmdlets on Windows (not the older
`wmic`, which is deprecated and absent by default on newer Windows
installs). Every one of these commands' output format is documented in
the respective platform's own reference material and has been stable for
a very long time — a materially stronger correctness guarantee, given how
this codebase was authored, than a syscall struct binding nobody has
compiled yet.

Windows makes a narrow, explicitly-reasoned exception for three syscalls
via `syscall.NewLazyDLL("kernel32.dll")`: `GetTickCount64` (uptime — a
bare scalar return, zero struct risk), `GetDiskFreeSpaceExW` (disk space —
three independent output pointers, not a packed struct), and
`GlobalMemoryStatusEx` (memory — the one genuine struct-layout call in
this codebase, accepted because `MEMORYSTATUSEX` is about as
widely-reproduced and stable a Win32 struct as exists, essentially
unchanged since Windows 2000). `provider_windows.go`'s package comment
explains the risk model behind each choice in detail; it's worth reading
in full before trusting `MemoryInfo` on a real Windows build.

Both platforms' CPU utilization (`CPUUsage`) returns `ErrUnavailable`
rather than shipping parsing logic against a command output shape
(`top -l 2` on macOS; PDH counters or `NtQuerySystemInformation` on
Windows) that this codebase's author had materially lower confidence in
than everything else in these two files — an honest scope cut, not an
oversight; see each provider file's `CPUUsage` doc comment.

**Known gap, disclosed rather than hidden:** none of Phase 2's Darwin or
Windows code has been run against a real macOS or Windows machine. The
fixtures in `darwin_parse_test.go` and `windows_parse_test.go` are
written from documented output formats, not captured samples, unlike
Linux's fixtures. Treat both as "written carefully to the documented
pattern, not yet verified" — precisely the same disclosure this document
already makes about the Rust/Tauri shell in Phase 1 — until someone runs
`go test ./...` on the respective OS and reports back.

## Looking ahead: remote agents

**(future — not implemented; documented now because it constrains Phase
1's design choices.)** The eventual architecture is:

```
FORGE Desktop  ──secure RPC──▶  FORGE Agent  ──OS APIs──▶  remote machine
```

carrying the same `pkg/fwp.Envelope` shape over a TLS-secured connection
instead of loopback HTTP, with real authentication instead of a
generate-once local token. This is why `pkg/fwp` has zero dependencies and
no HTTP-specific concepts baked into its types, why method registration is
kept out of `internal/ipc` (a remote agent's method set and a local
core's method set can differ), and why `internal/system`'s interfaces
(once they exist) are the boundary a remote implementation would sit
behind rather than local system calls being sprinkled through method
handlers directly. None of this is built yet, and the spec is explicit
that it shouldn't be until the local architecture — what this document
mostly describes — is solid.

## Process actions: termination

`system.ProcessProvider.TerminateProcess(ctx, pid, force)` is the one
capability added in Phase 3 that changes something on the machine rather
than only reading it, and it's treated with the caution that implies at
every layer:

- **Backend**: every platform implementation uses a direct syscall
  (`os.Process.Signal`/`.Kill()`) — never a shell string, never `taskkill`/
  `kill` invoked via `os/exec`. `force=false` (graceful) sends `SIGTERM`
  on Linux/macOS; Windows has no general equivalent (see
  `provider_windows.go`'s `TerminateProcess`) and returns
  `ErrUnavailable` for that case rather than silently behaving like
  force. `internal/app`'s `process.terminate` handler publishes a
  `TypeProcessTerminationRequested` event on every attempt — success or
  failure — so a termination is always part of FORGE's observable
  history, not a silent side effect.
- **Frontend**: `process.terminate` is only ever called from
  `ProcessDetailPanel`, and only after `ConfirmDialog` — built in Phase 1
  specifically so every later feature needing a "are you sure?" gate
  would already have a correct, tested one to use, rather than each
  feature growing its own slightly-different confirmation UI.

## Process tree

`internal/system.BuildProcessTree` is pure data transformation over
whatever `ListProcesses` already returned — grouping by PPID into a
forest of `*ProcessTreeNode`. It has no build tag and no OS-specific code
(see `tree.go`'s doc comment on why: parent/child structure is a property
of the data, not the platform that gathered it), which also means it's
exercised by ordinary unit tests on every platform, not just an
integration suite on whichever OS happens to be running CI.

## Service management

`system.ServiceProvider` projects three genuinely different platform
service models onto one interface — see `ServiceInfo`'s doc comment for
why this is a projection, not a claim of equivalence. The three
implementations, in decreasing order of how cleanly the platform's model
maps onto FORGE's:

- **Linux (systemd via `systemctl`)**: the fullest implementation.
  `systemctl show <unit>` is explicitly documented as intended for
  scripting (stable `KEY=VALUE` output), which is why service *detail* —
  state, startup mode, main PID, dependencies via `Requires` — is
  reliable here in a way it isn't on the other two platforms. Listing
  merges two calls (`list-units` for running state, `list-unit-files`
  for enabled/disabled) rather than shelling out per-unit, which would
  not scale to a system with hundreds of units.
- **Windows (`Get-CimInstance Win32_Service` + PowerShell service
  cmdlets)**: reuses the CIM-CSV parsing pipeline Phase 2 already built
  for process/CPU identity, which is why this is the second-most
  complete implementation despite Windows service semantics differing
  from systemd's — the *mechanism* for querying was already proven out.
  `Start-Service`/`Stop-Service`/`Restart-Service`/`Set-Service` are
  clean, stable, long-documented cmdlets with materially higher
  confidence than the raw Win32 syscalls flagged elsewhere in
  `provider_windows.go`. Dependencies are left empty — Win32_Service
  doesn't expose them via a simple property select.
- **macOS (`launchctl`)**: deliberately the thinnest. launchd addresses
  jobs by label, loaded from plist files whose paths `launchctl list`
  doesn't report, and modern `launchctl`'s `enable`/`disable`/`kickstart`
  subcommands need a domain-target (`system/<label>`, `gui/<uid>/<label>`,
  ...) that a bare listing doesn't disambiguate either. Rather than guess
  at either, `ServiceDetail` is thin (no description, no dependencies,
  `StartupMode` always `"unknown"`) and `SetServiceEnabled` returns
  `ErrUnavailable` outright — see `provider_darwin.go`'s package comment
  for the full reasoning. This is a materially higher-uncertainty area
  than anything else in this codebase's macOS support, itself already
  unverified against a real Mac (see DEVELOPMENT.md).

**Privileges**: starting, stopping, restarting, or changing autostart for
a service commonly needs root/Administrator. FORGE never attempts to
acquire or escalate privileges itself — see SECURITY.md's "Service
management" section for the full statement of this, which matters enough
to repeat in both documents rather than reference once.

One more disclosure specific to this subsystem: unlike this codebase's
`/proc`-based Linux system info, Linux service management could **not**
be checked against a live system while writing it — the sandbox it was
authored in is a container, not booted with systemd as PID 1, so
`systemctl` itself reports "Host is down." Every fixture in
`linux_service_parse_test.go` is written from systemd's documented output
formats, the same tier of confidence as the Darwin/Windows system-info
code, not the higher tier the rest of this codebase's Linux support
earns. See DEVELOPMENT.md.
