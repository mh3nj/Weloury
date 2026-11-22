# Testing

FORGE's tests live next to the code they test (Go's standard convention:
`foo.go` + `foo_test.go` in the same package), not in this directory. This
file explains the strategy; it deliberately does not contain placeholder
test files with no real assertions.

## Backend (Go)

Run everything with race detection, which matters a great deal for this
codebase - see `internal/eventbus` and `internal/app` in particular:

```bash
cd backend
go test -race ./...
```

Coverage as of Phase 8 (see each phase's CHANGELOG entry for exact test
counts when first added):

| Package                  | What's tested |
|---------------------------|----------------|
| `pkg/fwp`                  | Envelope JSON round-tripping, error envelope shape, that request-only fields are omitted from event envelopes. |
| `internal/eventbus`        | Delivery, filtering, context-scoped unsubscribe, the no-leak guarantee on the watcher goroutine, non-blocking drop behavior under a slow subscriber, `Close` semantics, and a `-race`-oriented concurrent publish/subscribe stress test. |
| `internal/config`          | Default-writing on first load, partial-file merge over defaults, round-trip, malformed JSON rejection, `Dir()` on Unix. |
| `internal/logging`         | Rotating writer: basic write, rotation at the size threshold, oldest-backup pruning; `Options.Tap` (Phase 4): every record reaches the tap, a panicking tap doesn't take down logging, a tap survives `WithAttrs`. |
| `internal/logs` (Phase 4)  | Capture-and-query ordering (newest first), capacity-bounded eviction, level/search filtering, `Before`-cursor pagination, limit clamping, bus publication, and a `-race` concurrent-capture stress test. |
| `internal/health` (Phase 4) | Every checker (`process_exists`, `service_state`, `tcp_port` against a real listener, `http_endpoint` against `httptest.Server`, `command`, `threshold` band math) against fakes; the scheduler's bounded concurrency, transition-only event publishing, retry policy, cooldown-free-by-design semantics, `RunNow`, and full CRUD, all under `-race`. The `command` checker's shell-injection regression test (Phase 8) is a genuine behavioral proof, not just a structural comment: it crafts an argument that would flip the result if shell execution were ever reintroduced, verified by temporarily reintroducing it and confirming the test actually fails - see CHANGELOG.md's Phase 8 entry. |
| `internal/metrics` (Phase 4) | Collector correctness (CPU/memory/per-mount-disk/computed network rate, including counter-reset handling), the rolling-window-vs-flush-accumulator split staying independent, flush batching and a real shutdown-triggers-final-flush test, and `Query` against a fake `Repository`. |
| `internal/scheduler` (Phase 5) | One-time and interval task firing, the missed-execution catch-up-once policy (both for a `Once` task and an `Interval` task, confirming no burst-refire), `SetEnabled` pause/resume rescheduling, full CRUD and validation, all under `-race`. |
| `internal/automation` (Phase 5) | Condition evaluation (match/mismatch/missing-field-fails-closed/AND semantics); every action type against fakes, including `run_command`'s shell-injection regression test (Phase 8, same genuine-behavioral-proof standard as `internal/health`'s - see above) and a real-clock-based grace-period assertion for `restart_process`; trigger matching for both `event` and `schedule` triggers; cooldown suppression, including the 1-second cooldown floor applied even when `CooldownSeconds` is left unset (Phase 8 - `TestEngineZeroCooldownStillHasAFloor`, verified to actually fail without the floor before confirming it passes with it); the self-triggering-loop-prevention check at both validation and dispatch time; full CRUD; all under `-race`. |
| `internal/containers` (Phase 6) | Docker CLI output parsing (`docker ps`/`docker stats`, including binary-unit size parsing) against fixtures captured from a real, running Docker daemon; `Provider` logic (list/stats merging, graceful degradation when `docker stats` fails, logs, start/stop/restart) against a fake command runner, including the Phase 8 argument-injection fix (`TestProviderIDsAlwaysFollowADoubleDash` - every docker invocation that takes an id puts `--` immediately before it, verified against a crafted id designed to look like a flag); plus a real integration test that talks to a live Docker daemon directly when one is available and skips cleanly otherwise - see DEVELOPMENT.md for how this was actually run against a live daemon in this sandbox, twice (Phase 6's authorship and Phase 8's security review). |
| `internal/diagnostics` (Phase 6) | Report generation against fully in-memory fakes (including a real `health.Engine` wired to a fake system-capabilities struct, exercising an actual health check evaluation, not a stub result): the happy path across every section, independent per-section degradation when a data source errors, health-section filtering to only non-healthy checks, and recent-issue severity filtering. |
| `internal/storage`         | Migration idempotency and table creation (including a Phase 4 and a Phase 5 "did the new tables actually get embedded" regression test) against a real SQLite database, event/health/metrics/scheduler/automation repository insert/query/prune/round-trip behavior - see DEVELOPMENT.md's "Toolchain status" for how this package's tests actually run in a sandbox that can't reach `modernc.org/sqlite` directly. |
| `internal/ipc`             | Router dispatch (success, unknown method, plain-error wrapping), duplicate-registration panic. |
| `internal/app`             | FWP method registration and error translation (`system.ErrUnavailable`/`ErrNotFound` -> `fwp` error codes) against a fake `system.Provider`, independent of any real OS; process termination's and service actions' event-on-every-outcome behavior (success and failure both publish their respective events, verified by subscribing to the bus in the test); `containers.start`/`stop`/`restart`'s empty-id rejection (Phase 8 - `TestRegisterMethodsContainersActionsRejectEmptyID`, matching the pattern `service.detail`'s own empty-name rejection already established); `app_test.go`'s full-stack end-to-end test (Phase 4, extended in Phases 5 and 6) - builds a real `App`, runs it, and drives it over actual HTTP/FWP, covering the health check lifecycle, live metrics, log capture, event persistence, a scheduled task firing an automation rule whose action's event reaches `events.recent`, a full `diagnostics.run` report, and `containers.availability`/`containers.list` asserted correctly against whichever way Docker happens to be available in the environment running the suite. |
| `internal/system` (cross-platform) | `BuildProcessTree`: simple hierarchies, multiple roots, a defensive self-parent-cycle guard, determinism. |
| `internal/system` (Linux)  | Pure `/proc`-format parsers tested against fixture data captured from a real machine (`linux_parse_test.go`, including the utime/stime/cmdline additions for Phase 3, and Phase 6's `/proc/net/tcp` hex address/port decoding - verified against a real captured 127.0.0.1 listener, not just documentation), *plus* an integration suite (`provider_linux_integration_test.go`) exercising the real implementation against the real running OS - including `TerminateProcess` against a real disposable child process (`sleep`, spawned and killed by the test itself), (Phase 4) `NetworkIOCounters` against the real `/proc/net/dev`, and (Phase 6) `NetworkConnections` against a real TCP listener the test opens itself, confirming the full call - socket table read plus the `/proc/*/fd` inode-to-PID cross-reference - works end to end. `systemctl` output parsers (`linux_service_parse_test.go`) are tested against fixtures written from systemd's documented format, *not* captured - see the note below. |
| `internal/system` (Darwin/Windows) | Pure parsers tested against fixture data written from documented command/CIM/launchctl output formats - not captured from a real machine, since none was available while authoring this. No integration suite exists for these platforms yet; see DEVELOPMENT.md. |

**A note on Linux service management specifically**: every other piece of
this codebase's Linux support was checked against this sandbox's real
`/proc` filesystem and real running processes. `systemctl` is installed
in this sandbox but non-functional (the container isn't booted with
systemd as PID 1), so `linux_service_parse_test.go` sits at the same
"fixtures from documentation, not captured output" confidence tier as the
Darwin/Windows tests, not the higher tier the rest of this package's
Linux tests earn. See ARCHITECTURE.md's "Service management" and
DEVELOPMENT.md.

Not yet covered by an automated test, and why:

- **`apps/desktop` (Rust)** has no tests. Its logic (spawn a process, parse
  one line of JSON, make one HTTP request on exit) is thin enough that the
  highest-value verification is a manual run through `scripts/dev.sh`
  against a real `forge-core` binary - see DEVELOPMENT.md. A `#[test]`
  around `resolve_backend_path` would be reasonable to add early in Phase
  2 once there's a second contributor relying on it. This code has never
  been compiled at all, let alone tested - Phase 9 got a real Rust
  toolchain and every system dependency working in this sandbox for the
  first time, but stopped at a Cargo `edition2024` version requirement
  before reaching this file; see DEVELOPMENT.md's Phase 9 entry.
- **Frontend** has no test runner configured yet. `lib/ipc/client.ts` is
  the piece of frontend code with real logic (NDJSON line framing across
  arbitrary chunk boundaries, abort-vs-error disambiguation) and is the
  right first target once a test runner (Vitest is the natural fit given
  the Vite build) is added - deferred rather than added speculatively
  while the page count kept growing each phase. `tsc --noEmit`/`eslint`
  (both now run and passing as of Phase 4 - see DEVELOPMENT.md's
  "Toolchain status") catch a real class of bug on their own, but neither
  is a substitute for behavioral tests once a runner exists.

## What "done" looks like for a subsystem's tests

Every subsequent phase should ask the same questions this one did:

1. Does this have a concurrency story (goroutines, channels, shared
   state)? If so, is there a test that would fail under `-race` if that
   story were wrong?
2. Does this touch persistence? Is there a test against a real (in-memory)
   database, not a mock?
3. Does this cross a failure boundary (a syscall, a network call, a
   subprocess)? Is the failure path tested, not just the happy path?
4. Would a malformed or adversarial input (bad JSON, a canceled context, a
   duplicate registration) be caught, or silently accepted?

Tests that don't answer "yes, and here's the assertion" to at least one of
these for the code they cover are usually testing something that didn't
need testing yet, at the cost of maintenance burden on something that
does. See spec section 27, "do not write meaningless tests just to
increase coverage."
