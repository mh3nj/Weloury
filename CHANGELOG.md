# Changelog

This log tracks what shipped in each build phase, not individual commits.
Format is loosely [Keep a Changelog](https://keepachangelog.com/); version
numbers will start once there's a tagged release.

## [Unreleased] — A cohesive-reading pass on `internal/containers`, and where it actually led

Set out to give `internal/containers` the same whole-package read
`scheduler`'s scan loop and `automation`'s cooldown/conditions ordering
already got — it shells out to `docker`, the same class of
security-sensitive surface, and hadn't had that treatment yet. Reading
it raised a question about request timeouts that turned out to matter
more for `automation` than for `containers` itself; documenting that
distinction precisely, rather than either overclaiming or dropping it,
is most of what this entry is about.

### Fixed — automation actions had no per-action timeout wired to anything testable

`actions.go`'s `executeAction` doc comment claimed "ctx should already
carry a per-action deadline — `Engine.executeActions` owns that
policy." True, on inspection — `context.WithTimeout(ctx,
defaultActionTimeout)` (30s) really does wrap every action call — but
nothing tested it, for either half of what actually matters: that a
genuinely hanging command really gets killed (not just that a
`context.Context` reports itself expired — a real OS child process has
to actually die), and that this is what stands between a burst of
hanging actions and permanently exhausting
`DefaultMaxConcurrentExecutions`'s 8-slot semaphore. Without it working,
as few as 8 rules whose actions hang (a `run_command` pointed at
something that never exits; a
`restart_process`/`start_service`/`restart_service` against something
unresponsive rather than fast-failing) would each hold a semaphore slot
forever — silently exhausting automation's entire execution capacity
for every *other* rule too, with no error and no visible cause short of
reading goroutine dumps.

Made `defaultActionTimeout` injectable as `Engine.actionTimeout` (seeded
from the constant in `New`, same pattern as `clock`) specifically so
this could be tested without waiting out a real 30 seconds. Two new
tests, at two levels: `TestActionRunCommandRespectsContextDeadline`
proves a real `sleep 30` given a 100ms context deadline returns in
~100ms, not 30s — a genuine OS process actually getting killed, not an
assumption about `exec.CommandContext`'s documented behavior.
`TestEngineActionTimeoutPreventsSemaphoreStarvation` is the integration
proof this whole investigation was actually about: 9 rules (one more
than the semaphore has slots for), each with an action that hangs for
30 real seconds, all fire at once against a 100ms `actionTimeout` — and
every single one completes (as a timeout failure, correctly) within 3
seconds. If the semaphore could starve this way, that test would hang
for 30+ seconds instead. Full backend suite clean after.

### Fixed — one frontend data-fetching call missing the `AbortController` convention every other one follows

While tracing how much the IPC layer's lack of a server-side per-request
timeout (see below) actually matters in practice, audited every frontend
file calling `client.request()` for whether it pairs the call with an
`AbortController` — the mechanism that cancels a stale request on
unmount or re-fetch, and (via context cancellation propagating to the
Go handler) is what actually bounds a hung backend call from a
same-machine, single-user client. Found the convention followed
consistently everywhere *except* `ServiceDetailPanel.tsx`'s `load()`: a
real `useEffect`-driven data fetch with no `signal` at all, unlike its
siblings (`ContainersPage.tsx`, `ProcessesPage.tsx`, `HealthPage.tsx`,
`AutomationPage.tsx`, `SchedulesPage.tsx`) which all already did this
correctly. Fixed to match the established pattern exactly. The two other
files with zero `AbortController` usage
(`AutomationRuleDetailPanel.tsx`, `HealthCheckDetailPanel.tsx`) were
checked and are a different, lower-risk category: one-shot,
user-triggered actions (delete, run now) that receive their data as
props rather than fetching it themselves — meant to run to completion
once clicked, not something a background re-fetch could accumulate.
Verified with a full `tsc --noEmit`/`eslint .` pass.

### Documented, not fixed — the IPC server itself has no per-request timeout

`handleRPC` runs every request with bare `r.Context()`, explicitly
documented from Phase 1 as the cancellation mechanism: a request stops
when the client disconnects, and that's the only bound — there is no
independent server-side cap. Investigated properly rather than left as
an assumption while auditing `containers`, and then scoped precisely
rather than either dismissed or over-fixed: for a local, same-machine,
single-user tool where the one real client is this project's own
frontend, the practical exposure is narrower than the finding sounds,
*given* the frontend's `AbortController` discipline is actually
consistent — which is exactly what the fix above went and confirmed,
not assumed. What remains genuinely uncovered: a direct user-triggered
action (run now, delete, start/stop/restart) is meant to run to
completion once clicked, so a hang there is a stuck "busy" spinner, not
elegant but bounded to that one action — and the planned remote Agent
architecture, where "the client" won't always be this project's own
disciplined frontend, is where this stops being narrow and needs a real
server-side timeout before it lands. Documented precisely, at both the
right level of confidence and the right scope, in SECURITY.md's "Local
IPC" section and ARCHITECTURE.md's automation section (the
action-timeout half) rather than either silently left unrecorded or
overstated into a fix this pass didn't actually make.

## [Unreleased] — Re-verified the two hard sandbox blockers; both unchanged

Periodic re-check, not new work: confirmed neither of the two
environment blockers documented elsewhere in this project (README's
"The honest gap," DEVELOPMENT.md's Phase 9 toolchain entry) has become
newly reachable.

- **Rust 1.85+ for `cargo tauri build`.** Retried via a different route
  than the original investigation: installed `rustup` itself (packaged
  in the Ubuntu archive, unlike a newer `rustc`) on the theory it might
  reach a toolchain the archive's own fixed 1.75.0 package can't.
  `rustup toolchain install stable` fails identically to the original
  finding — `-v` output shows it correctly attempting
  `https://static.rust-lang.org/dist/...`, which is not in this
  sandbox's network allowlist. Confirms the original diagnosis was
  precise (the blocker really is toolchain distribution access, not
  some fixable detail of how the archive package was installed) rather
  than superseding it. Uninstalled `rustup` afterward; no lasting change
  to the environment.
- **Real Windows/macOS hardware.** No new virtualization or emulation
  path checked (there wasn't one to begin with) — confirmed this
  sandbox is a single Linux container (`uname -a`: Linux, x86_64) with
  no `wine`, `qemu`, or equivalent installed. Unchanged.

## [Unreleased] — Load profiling beyond request-volume + concurrent health checks

Phase 8's "Still to do" list named this precisely: prior profiling
covered idle state and sustained request volume against concurrent
health checks, but not "any scenario beyond request-volume + concurrent
health checks (e.g., a large process list under Overview's live metrics
stream, or automation/scheduler under sustained real-world-shaped load
rather than the synthetic bursts the goroutine-leak tests use)." Ran
both named scenarios, against the compiled binary, with the same
temporary `net/http/pprof` instrumentation discipline as every earlier
profiling entry: added to `cmd/forge-core/main.go`, used, then removed
and verified byte-for-byte identical to the original afterward (no
`git` available in this checkout to `diff` against, so verified by
direct comparison against the file's original content instead — same
standard, different mechanism).

**A test-methodology pitfall hit immediately, worth naming**: the first
attempt at the large-process-list scenario spawned 500 background
`sleep` processes with plain `&`, which this sandbox's tool boundaries
silently kill at the end of the shell call that spawned them (the same
lesson already learned once this session getting `forge-core` itself to
survive across calls — forgotten and re-learned here). `process.list`
correctly reported the *unchanged* original process count afterward,
which is what caught it — not a red flag on its own, but exactly the
kind of "confirm the load genuinely ran" check the previous profiling
entry's `$SECONDS` bug already established as necessary. Fixed by
spawning all 500 under one `setsid`-detached parent shell instead.

**Scenario 1 — a large real process list, polled continuously.** Spawned
500 real background processes (surviving this time), confirmed
`process.list` reported 551 processes (up from the baseline 51), then
ran a corrected (`date +%s`-based, explicit `bash`) 60-second load
generator hitting `process.list` in a tight loop — 3,111 requests
actually sent and confirmed via the generator's own logged count, ~52
requests/second sustained (lower than health.list's earlier-measured
~173/s, expectedly: enumerating 551 real OS processes per call — reading
each one's `/proc` entry — is inherently more expensive than listing an
in-memory health-check map).

- Goroutines: 23 idle → 23 immediately after spawning 500 processes (no
  per-process goroutine, confirming `process.list` collects
  synchronously per-request rather than maintaining a live per-process
  watcher) → 24 momentarily mid-burst → 23 again at the end and 15s
  after the load stopped. No growth from either the larger process
  count or the request volume.
- CPU: roughly 43.6 seconds of accumulated CPU time (`utime`+`stime`)
  over the 60-second, 3,111-request burst — about 72% of one core
  average, ~14ms of CPU per request. Essentially flat (1 tick, 20ms)
  over the following 15 seconds once the load stopped, confirming the
  cost was genuinely request-driven, not a lingering background cost.
  Meaningfully higher per-request than the health-check-list scenario,
  which is the real, expected cost of `process.list`'s design: it
  scales with how many real processes exist on the machine, not with a
  fixed internal count — worth knowing as a documented characteristic
  of the current implementation rather than a surprise if a user with a
  genuinely busy machine polls the Process Manager aggressively.
- Memory: `VmRSS` grew from 11.7MB idle to 17.9MB at the end of the
  burst and stayed exactly there 15 seconds after load stopped — a
  one-time ramp, not a continuing climb. `HeapAlloc` stayed small
  (~3.5MB) throughout, with the garbage collector cycling continuously
  (7,905 collections over the run) to keep it there rather than letting
  it accumulate.

**Scenario 2 — scheduler and automation under sustained, staggered load,
not a synthetic burst.** The goroutine-leak tests (see the "Phase 8,
revisited" entry above) intentionally use short, synthetic all-at-once
bursts — the right shape for a fast, deterministic unit test, but not
representative of how these engines actually get used. This scenario
built the realistic version instead: 20 real `scheduler.Task`s (interval,
10-second — the minimum allowed), each wired to its own
`automation.Rule` via `TriggerSchedule`, left running against real wall-clock
time for over 100 seconds. With 20 independently-created tasks each
firing roughly every 10 seconds, this produces genuinely staggered
activity spread across the whole run, not one coordinated spike.

- Confirmed real, not assumed: every one of the 20 tasks had
  `LastRunAt` set, and every one of the 20 automation rules had
  executed exactly 12 times by the end of the run — 240 real, individually
  tracked executions total, matching the expected ~10-second cadence
  over the run's duration.
- Goroutines: flat at 23 across five samples taken every 20 seconds for
  the full 100+ second run — no growth from either the scheduler's
  single shared scan loop or automation's bounded execution pool, under
  real sustained (not bursty) conditions.
- CPU: essentially negligible — 8 clock ticks (80ms) of accumulated
  `utime`+`stime` across the middle 80 seconds of the run, for 20
  tasks' worth of continuous scan-loop ticking plus ~180 real
  scheduler/automation executions in that window.
- Memory: `VmRSS` grew from 13.0MB to 15.5MB over the first ~60 seconds,
  then plateaued (15.3MB → 15.4MB → 15.5MB across the last three
  20-second samples) — consistent with a one-time warm-up, not a slow
  leak a short synthetic burst test wouldn't have run long enough to
  reveal.

Between this entry and the two earlier profiling entries, spec section
26's performance targets have now been verified against the real
compiled binary across every scenario Phase 8's own "Still to do" list
named: idle, sustained request volume, a large real process list, and
sustained (not just bursty) scheduler/automation activity. No further
known profiling gaps.

Phase 8's "Still to do" list named this precisely: crash/recovery
testing had covered SQLite crash safety and the scheduler's
missed-execution policy, but "crash timing during automation rule
execution or event-bus delivery specifically" hadn't been tried. Ran
three real crash tests against the compiled binary — not unit tests —
using the same method as Phase 8's earlier scheduler crash test: real
`SIGKILL`, real concurrent IPC traffic, `PRAGMA integrity_check`
against the raw file afterward, then a real restart and inspection of
what actually persisted.

**Setup, and a sequencing lesson from the first attempt.** The scenario
needed multiple health checks failing concurrently, each wired to its
own automation rule via a `check_id` condition, so a burst of
`health.check_failed` events would drive genuinely concurrent rule
executions. The first attempt created the health checks first, then the
matching automation rules — and got zero executions, crash or no crash:
`health.Engine.Create` runs a check's first evaluation immediately, in
a background goroutine, and `TypeCheckFailed` only fires on a
UNKNOWN→UNHEALTHY *transition* (edge-triggered, by design) — so by the
time the automation rules existed, the one transition each check would
ever fire had already happened with nothing listening. Not a product
bug, a test-sequencing one: fixed by pointing every check at
`forge-core`'s own (real, open) listening port first, so they settle to
`HEALTHY` before any rules exist, registering the rules, then using
`health.update` to flip each check's target to a closed port — `Update`
also re-evaluates immediately on its own restart-the-goroutine path,
so this reliably produces a fresh transition *after* the rules are
already registered to catch it.

**Two runs (8 and 12 concurrent checks/rules) killed the instant the
concurrent requests were fired** — aggressive, but it meant `SIGKILL`
landed before any request got far enough server-side to prove anything
beyond "nothing happened, cleanly." Useful for confirming atomicity by
absence (see below) but not for catching genuine in-flight work.

**A third run added a deliberate ~15ms delay between firing 12
concurrent `health.update` calls and the kill**, and did catch real
work mid-flight: 3 of 12 requests got a response before the kill (config
persisted), and — checked directly against the raw event log — the
*entire* subsequent cascade (async re-evaluation → `health.check_failed`
→ automation match → action execution → `UpdateRunResult` persist →
`TypeRuleExecuted` publish) completed and durably committed for at
least one of those three within that same ~15ms window, entirely
asynchronously to the RPC response that had already been sent. The
other 9 requests got no response at all; their checks' configuration
remained completely unchanged (never partially applied — the target
field either fully updated or, for the dropped ones, exactly as it was
before) — no torn writes at either the config-persist or the
run-result-persist layer, across all three runs. `PRAGMA integrity_check`
returned `ok` on the raw database file after every kill, and every
restart came up clean with no error output and no manual repair,
resuming health/automation/event state exactly where the last durable
write had left it. Restart itself independently confirmed the same
"run once immediately" pattern for health checks that automation and
scheduler already had documented for themselves — checks whose config
survived the crash re-evaluated fresh on the new process, generating
another real, legitimate burst of concurrent `health.check_failed`
events entirely apart from the crash itself.

**That third run's event log also surfaced, and independently
corroborated, the conditions-vs-cooldown bug below** — found by
crash-testing, not by unit testing, which only ever exercises one rule
against one event at a time and would never notice two rules racing
each other's cooldown windows.

### Fixed — `automation.Engine.tryRun` checked cooldown before conditions, producing misleading skip reasons

Not a data-corruption bug — the actual run/no-run decision was always
correct — but a real observability bug, and one that unit tests
(which exercise one rule against one event at a time) structurally
couldn't have caught: it only shows up with multiple rules sharing a
`Trigger.EventType` but different `Conditions`, racing real concurrent
events, which is exactly what the crash-test setup above happened to
build.

`handleEvent` filters its `matched` rule list by `Trigger.Type` alone
(see `ruleMatchesEvent`) — `Conditions` were checked later, inside
`tryRun`, and *after* the cooldown check. So for any event whose type
matched, every rule sharing that event type reached `tryRun`, even ones
whose `Conditions` could never match that specific event's payload.
Concretely, in the crash-test scenario: 8–12 rules, each conditioned on
one specific `check_id`, all shared `Trigger.EventType:
"health.check_failed"`. The first time a rule's own check failed, it
executed for real and started its own cooldown window. For every *other*
check's failure event after that — which its `check_id` condition was
never going to match regardless — the old ordering reported
`RunSkippedCooldown` ("cooldown active") instead of `RunSkippedConditions`,
because cooldown was checked first and short-circuited before the
conditions mismatch that was the actual, sole reason that event was
never going to fire the rule.

Fixed by evaluating conditions first, cooldown second — confirmed safe
before touching anything: `checkCooldown` and `evaluateConditions` are
both pure reads with no side effects, and `recordOutcome` never touches
`lastRunAt`/`runCount`/`consecutiveFailures` for *either* skip status,
so reordering changes only which accurate reason gets reported when
both would independently apply, never whether a rule executes. Kept to
one lock cycle (a single `checkCooldown` call still fetches both the
rule snapshot and cooldown state together; only the order the two
resulting checks are applied in changed) rather than adding a second
`tr.mu` acquisition. New regression test
`TestEngineCooldownDoesNotMaskAMismatchedConditionsSkip` — checked, not
assumed, to fail against the original ordering and pass against the
fix, by literally reverting the reorder, confirming the failure
message, and restoring it. Full backend suite (`go build`/`go vet`/
`go test -race`, all packages) clean after.

Worth being precise about scope: all three crash-test runs above were
executed against a binary built *before* this fix (confirmed after the
fact by comparing binary checksums — the crash-test binary predates the
`engine.go` change). They remain valid evidence for what they were
testing — SQLite/event persistence atomicity under a real kill — and
the third run's mixed skip/succeeded statuses are fully consistent with
the *already-diagnosed* bug, not independent confirmation of the fix
itself; the fix's own correctness rests on the regression test's
explicit fail-then-pass check above, not on re-running the crash
scenario against a rebuilt binary (a fourth run, decided against as
diminishing return once the same root cause had already been confirmed
via three independent routes: the live event log, the code reading,
and the isolated regression test).

## [Unreleased] — Phase 8, revisited: the goroutine-leak and scan-loop gaps Phase 8 named, closed

Phase 8's own "Still to do" list (below) named several specific,
unverified gaps rather than a vague "more hardening needed." This entry
closes four of them for real, with the same evidence standard the rest
of Phase 8 used — read the code as a whole, not function-by-function;
write a test that fails against the original code before calling
anything fixed; run everything under `-race`, repeatedly.

### Fixed — a real concurrency bug in `scheduler.Engine.fire`, found exactly where Phase 8 said it hadn't been checked

Phase 8's concurrency review (below) checked `scheduler.Engine`'s
`Create`/`Update`/`SetEnabled`/`Delete`/`List`/`Get` individually and
found each safe, but named one explicit gap: "`scheduler`'s scan loop
internals themselves (`Start`, `scanOnce`, `fire`) as a cohesive whole,
versus the individual functions already checked." Reading exactly that
found a real bug two individually-safe functions produce together.

`scanOnce` collects `*Task` pointers into a `due` slice while holding
`e.mu`, then releases the lock before calling `fire` on each one
(deliberately — actions shouldn't run under the lock). Before this fix,
`fire` re-locked and unconditionally mutated-then-persisted whatever
pointer it was handed, with no check that the pointer was still
`e.tasks[id]`'s live entry. Two concrete failure modes follow from
that gap, both confirmed real (not just theoretical) by tracing the
actual SQL: `storage.SchedulerRepository.Upsert` is an unconditional
`INSERT ... ON CONFLICT(id) DO UPDATE`, with no existence check.

- **A concurrent `Delete` racing a scan silently resurrects the task.**
  `Delete` removes the map entry and the DB row; a `fire` call already
  in flight against the pointer `scanOnce` collected just before the
  `Delete` would still call `repo.Upsert` with that task's ID —
  recreating the row, permanently, since nothing in `e.tasks` is left
  to ever delete it again.
- **A concurrent `Update` racing a scan can silently revert it.**
  `Update` installs a brand-new `*Task` at `e.tasks[id]` rather than
  mutating the old one in place (unlike `SetEnabled`) — deliberately,
  it turns out, since that's exactly what makes this fixable by pointer
  identity. A `fire` call holding the *old* pointer would persist a
  snapshot computed from the pre-`Update` configuration, racing
  `Update`'s own `Upsert` call for which write lands last.

Fixed by having `fire` re-validate, under `e.mu`, that its pointer is
still `e.tasks[t.ID]`'s live entry before touching anything — skipping
silently (not an error; the task either no longer exists or `Update`'s
fresher state is authoritative) otherwise. Two new deterministic
regression tests
(`TestEngineFireSkipsTaskDeletedDuringScan`,
`TestEngineFireDoesNotClobberConcurrentUpdate`) reproduce both bugs
directly — not via `-race`, since every access here was already
mutex-protected; this is a logic race, not a data race, so the fix had
to be verified by forcing the exact interleaving (calling `scanOnce`'s
and `fire`'s unexported internals directly, white-box) rather than
hoping timing cooperates. Both tests fail against the pre-fix code and
pass after. Full backend suite, `-race`, unaffected.

### Verified — goroutine-leak checks for automation and scheduler; container polling turned out not to apply

Phase 8's "Still to do" list named this precisely: goroutine-leak
verification had been done for event streaming and the health-check
lifecycle, but not "automation/scheduler/container polling
specifically." Investigated all three.

**`internal/containers` has no backend goroutine lifecycle at all** —
read `provider.go` in full to confirm: it's a stateless wrapper that
shells out to `docker` per-request and returns; polling, if it happens,
is the frontend calling `containers.list` on an interval, entirely
outside the Go backend's goroutine surface. There is structurally
nothing here to leak, so there's nothing to test — worth stating
precisely rather than leaving the original phrasing's implication that
a polling loop exists unconfirmed either way.

**`internal/automation` and `internal/scheduler` both got real,
committed regression tests** (`TestEngineGoroutinesDoNotLeakAcross-
CreateExecuteDeleteCycles` and `...CreateFireDeleteCycles`
respectively) using `runtime.NumGoroutine()` with a stabilization
helper (wait for three consecutive equal readings, not one raw
sample — a single reading is flaky against goroutines merely mid-exit,
not genuinely leaked). Chosen over a one-off manual `pprof` session
(Phase 8's method for the health-check case, described as "temporary,
never committed, reverted") specifically because these run in CI
forever rather than needing to be redone by hand each time.

Two genuine surprises building these, both left as durable evidence in
the tests' own comments rather than quietly worked around:

- **`automation`'s cooldown floor (Phase 8's own fix, above) correctly
  blocked the test's own repeated bursts.** A first draft fired
  multiple bursts back-to-back against a real clock; every burst after
  the first got silently cooldown-skipped, and the test hung waiting
  for a run count that was never coming — not a bug, the floor working
  exactly as designed, but it meant the test needed a `fakeClock`
  (new to `automation`'s test file, mirroring `scheduler`'s existing
  one) to advance time deterministically between bursts rather than
  race real wall-clock time or sleep through it for real.
- **`eventbus.Bus.Subscribe`'s internal watcher goroutine isn't
  covered by `Engine.Wait()`.** `automation.Engine.Start` subscribes to
  the bus, which spawns a small internal goroutine (in `eventbus`, not
  `automation`) that calls `unsubscribe` when `ctx` is canceled;
  `Engine.Wait()` only waits on `Engine`'s own `WaitGroup`
  (`consumeEvents` and per-execution goroutines), not this one. Not a
  bug — nothing was ever documented to claim `Wait()` covers it, and it
  reliably exits on its own — but the test's post-`Wait()` goroutine
  count had to account for a drop of exactly two (both `consumeEvents`
  and this watcher), not one, or it would have failed for the wrong
  reason. `scheduler.Engine` has no equivalent — it never calls
  `Subscribe`, only `Publish` — so its own version of this test
  expects a drop of exactly one.

Structural note worth recording: `scheduler.Engine` has no per-task or
per-firing goroutine at all (one shared `runLoop` services every task,
`fire` runs synchronously inside it), so its leak test is really
confirming an architectural property (nothing *can* leak beyond
`runLoop` itself) rather than probing a bounded-concurrency pool the
way `automation`'s test does against its `DefaultMaxConcurrentExecutions`
semaphore.

### Added — `internal/app/retention.go`: 0% test coverage to fully tested

Named in Phase 8's "database review"/"error-path review" follow-up:
the hourly sweep orchestration, error handling, and the `<=0` disables
guard had zero test coverage, even though the `storage.PruneOlderThan`
functions it calls were already well-tested independently. New
`retention_test.go` covers: normal pruning of expired events and
metric aggregates; the `<=0` guard, independently for
`EventsRetentionDays` and `MetricsRetentionDays` and combined; a
forced database error mid-sweep (closing the DB out from under
`pruneOnce`) logging a warning without panicking, since this runs
unattended from a background ticker with nothing else watching for a
panic; and `runRetentionPruner`'s documented "sweep once at startup"
behavior plus prompt context-cancellation, without waiting anywhere
near the real one-hour interval. `pruneOnce` is now at 100% statement
coverage; `runRetentionPruner` at 85.7% — the one uncovered line is the
real `<-ticker.C:` branch itself, not worth waiting an hour (or adding
a test-only interval override to production code) to close.

### Added — a real circuit breaker for repeatedly-failing automation rules

Named explicitly in Phase 8's "Still to do" list: the cooldown floor
(Phase 8's earlier fix, above) bounds the *rate* a crash-restart loop
can spin at, never *whether* it runs forever. Implemented the
follow-up that entry named but didn't build.

- **`automation.MaxConsecutiveFailures`** (5) — a rule with this many
  consecutive `RunFailed` outcomes, no `RunSucceeded` in between, is
  auto-disabled (`Enabled` flips to `false`) rather than left retrying
  indefinitely at whatever rate the cooldown floor still permits. A
  skip (`RunSkippedCooldown`/`RunSkippedConditions`) neither extends
  nor resets the streak — mirrors `checkCooldown`'s identical existing
  reasoning that a skip isn't evidence the action itself is failing.
  Five is deliberately generous: this stops an indefinite loop, not a
  rule that had one or two transient bad runs.
- **New `Status.ConsecutiveFailures` field**, a new `automation.RunResult`
  type replacing `UpdateRunResult`'s four positional parameters (so
  the run outcome and any resulting `Enabled`/`ConsecutiveFailures`
  change persist as one atomic write — no crash window between "run
  recorded" and "the auto-disable it triggered"), and a new
  `TypeRuleAutoDisabled` event, published once per trip, distinct from
  `TypeRuleExecuted` because "this rule just stopped running itself"
  is materially different from "this rule's action failed once more."
  New migration `0006_automation_circuit_breaker.sql` adds the backing
  `consecutive_failures` column.
- **`Update` resets `ConsecutiveFailures` to 0** — a deliberate design
  choice, not an oversight: unlike `LastRunAt`/`LastRunStatus`/
  `RunCount` (which a config edit never touches, by existing design —
  they're persisted history), the whole reason to edit a rule that
  tripped the breaker is almost always to fix and re-enable it, and
  leaving a near-tripped streak in place across that edit would
  silently re-trip it on the very next hiccup instead of giving the
  operator's re-enable the fresh grace period they'd reasonably expect.
- Frontend: `types/automation.ts` gained `consecutive_failures` and a
  hand-synced `MAX_CONSECUTIVE_FAILURES` constant;
  `AutomationRuleDetailPanel.tsx` shows "N of 5" (in the unhealthy
  color once tripped) and, specifically when a rule is disabled *and*
  tripped, an explanatory note — otherwise a rule an operator didn't
  manually pause would just show "Paused" with no visible reason why.
  Verified with a full `tsc --noEmit`/`eslint .` pass, not just visual
  inspection of the diff.

Test coverage across all three layers: a storage-level round-trip test
(`TestAutomationRepositoryUpdateRunResultPersistsCircuitBreakerState`)
confirming the new columns actually persist rather than being silently
dropped by a repository built before `RunResult` existed to carry
them; and two engine-level behavioral tests
(`TestEngineCircuitBreakerAutoDisablesAfterConsecutiveFailures`,
`...ResetsOnUpdateAndOnSuccess`) covering the trip itself, the event
publish, a disabled rule correctly no-longer-matching, and both reset
paths (`Update`, and a genuine success one step short of tripping).
Two test-authoring bugs caught and fixed while building these — worth
naming since they're easy to mistake for production bugs: `checkCooldown`
never blocks a rule's *first* execution (`lastRunAt` is `nil`), so a
"same-instant re-fire should be skipped" assertion has to come after
one real execution, not before any; and `Update`, unlike `Create`,
does not force `Enabled: true` — it persists whatever the caller
passes, so a test reusing a zero-value `Rule` literal across both
calls silently disabled the rule via `Update` and every assertion
after that looked like a hang.

### Verified — a better route to a real CGO SQLite driver, and the frontend toolchain, both confirmed working again

Phase 4 (see below) established a **hand-written** CGO SQLite driver
stub as the way to build/test `internal/storage` and `internal/app` in
this network-restricted sandbox. This session found a strictly better
version of the same trick: `GOPROXY=direct` (bypassing
`proxy.golang.org`, which is blocked, without needing it) resolves Go
modules straight from their VCS host via `go-import` meta tags and
git — and for any module hosted on `github.com` specifically, that
host *is* in this sandbox's allowlist. `github.com/mattn/go-sqlite3` —
a real, maintained, widely-used CGO driver — fetches and builds
cleanly this way against the system's `libsqlite3`
(`apt-get install libsqlite3-dev gcc build-essential`). The sandbox-only
`replace` module was rewritten to a ~20-line shim that registers
`go-sqlite3`'s driver under the driver name `"sqlite"`
(`db.go` expects that exact name) instead of implementing CGO bindings
from scratch — same non-committed, clearly-marked, `DEVELOPMENT.md`-
documented pattern, meaningfully less custom code to trust. Whole
backend (`go build`/`go vet`/`go test -race`, all packages) confirmed
clean with it, repeatedly, across every entry above.

`modernc.org` itself remains genuinely unreachable (`go-sqlite3` is
GitHub-hosted; `modernc.org/sqlite` is not) — this doesn't close that
gap, it routes around it more cleanly for local testing purposes only.
See `DEVELOPMENT.md`'s Phase 4 toolchain section for the full updated
account.

Also re-confirmed the frontend toolchain independently: fresh `npm
install` against the real registry, `tsc --noEmit`, and `eslint .` all
still pass clean, including through every frontend change this entry
made.

## [Unreleased] — Phase 9: Documentation & Release (in progress)

Spec section 31's Phase 9 brief: documentation, installation process,
development instructions, architecture documentation, release builds,
changelog.

### Fixed — README.md and ARCHITECTURE.md were badly out of date

Both still described the project as of Phase 6 — "Phase 6 complete,"
Phase 7/8/9 "Not started" — despite Phase 7 being substantively done
and Phase 8 covering four real, fixed, and verified bugs plus extensive
crash/recovery, profiling, and concurrency-review work. Rewrote both to
accurately reflect current status, with specifics rather than a vague
"done": the Status section and build-plan table now name what Phase 7
and 8 actually found and fixed, not just that work happened. Feature
bullets updated where Phase 7 changed real behavior (Overview's
connection-status redesign, process-list virtualization, bounded log
memory, automation's cooldown floor). "The honest gap" section rewritten
to distinguish what's now genuinely verified (cross-compilation, a real
Docker daemon, real crash testing) from what still isn't (actual
Windows/macOS hardware execution) — not blurring that line the way a
less careful update might.

Two real accuracy bugs caught in the process, not just the obvious
"Status" section staleness — both had survived seven-plus phases of
otherwise careful documentation uncorrected:
- README's "Supported platforms" section claimed "As of Phase 1, no
  platform-specific system code exists yet" — flatly wrong since Phase
  2/3 added exactly that (`provider_linux.go`/`provider_darwin.go`/
  `provider_windows.go`).
- README's build-instructions section said readers would be "the first
  person to actually compile" the codebase — also long stale; this
  project's various sandbox environments have compiled and run it
  extensively across every phase since Phase 4.

`ARCHITECTURE.md`'s top-of-file "reflects the system as of Phase 6"
claim and its own stale "What's built as of Phase 6" section header
fixed the same way, with the same specificity about what's actually
done versus what's formally still open.

No source code touched this entry — verified with a full backend
build/test pass and a frontend `tsc`/`eslint` check anyway rather than
assuming a docs-only change carries zero risk of, say, a stray syntax
error in a code-fenced example.

### Fixed — a real, if narrow, frontend dependency vulnerability, and one deliberately left open

Ran `npm audit` for the first time this session actually looked at its
output (it would have reported the same thing on every install this
whole project, but a green `npm install` doesn't surface it — Phase 9
specifically checked). Found `GHSA-67mh-4wv8-2f99`: esbuild's dev
server doesn't validate the `Origin` header, letting a malicious
website read responses from it if visited in the same browser while
`npm run dev` happens to be running. Fixed with a surgical
`"overrides": { "esbuild": "^0.25.0" }` in `frontend/package.json` —
pinning just the vulnerable transitive dependency, not bumping `vite`
itself, since `npm audit fix --force`'s suggested fix (`vite` 5.x → 8.x,
a breaking major-version jump) is meaningfully more change than this
one issue warrants. Verified clean afterward with a full reinstall from
scratch (`rm -rf node_modules package-lock.json && npm install`) and
the complete `tsc`/`eslint`/`npm run build` sequence.

Found three more advisories in the same audit, all bundled under `vite`
itself — a dev-server path-traversal issue, an NTLMv2 hash-disclosure
issue via a vite-internal dependency, and a Windows-specific
`server.fs.deny` bypass (CVSS 7.5, the one driving the overall "high"
severity rating). Checked each directly rather than trusting `npm
audit`'s summary: all three are dev-server-only (none reachable from
`vite build`'s actual production output), two require a specific
precondition this project's own `dev` script doesn't trigger, and none
has a patch anywhere in the currently-pinned `vite` 5.x line — closing
this requires the same major-version bump the esbuild fix specifically
avoided. **Deliberately not fixed this pass**, and said so plainly
rather than leaving it looking like an oversight: a frontend
build-tool major-version upgrade needs its own verification pass (React
19, the custom Tailwind theme, and every other frontend dependency,
confirmed to behave identically across it) and doesn't belong folded
unprompted into a dependency-audit check. Full account, including exact
GHSA IDs and CVSS scores: DEVELOPMENT.md's "Toolchain status as of
Phase 9."

### Verified — the release build script, run for real for the first time

`scripts/build.sh` had been authored carefully (Phase 6, per its own
comments) but never actually executed — an omission worth closing given
this project's standing practice of running things for real rather than
trusting that careful authorship is the same as correctness. Ran it:
the `git describe`/`git rev-parse` version-detection fallbacks correctly
produced `dev`/`unknown` in this non-git-checkout sandbox with no error;
the Go build (with the sandbox's usual CGO SQLite stub wired in) and the
frontend build both succeeded; it failed exactly where its own comment
says it will — `cargo: command not found`, since no Rust toolchain was
installed in this sandbox at that point — with `set -euo pipefail`
correctly aborting there rather than continuing past a failed step.
Verified the one part of this that could have silently been wrong
despite everything else working — the `-ldflags` version injection —
by starting the actual compiled `forge-core` binary and querying
`system.version` over real IPC: `build_date` came back as a real,
correctly-formatted UTC timestamp matching when the build script had
actually just run, not a placeholder or a zero value.

### Verified — the Rust/Tauri gap, investigated properly instead of re-stating the same disclaimer

"No Rust toolchain has been available" had gone unchanged, unquestioned,
since Phase 1. Checked whether that was still actually true, the same
way the Docker daemon assumption got checked in Phase 8 and turned out
to be wrong: it wasn't fully true either. `apt-get install cargo rustc`
pulls a real 1.75.0 toolchain from the standard Ubuntu archive; every
Tauri Linux system library (`libwebkit2gtk-4.1-dev`, `libgtk-3-dev`,
`libayatana-appindicator3-dev`, `librsvg2-dev`, plus `build-essential`/
`libssl-dev`) installed and resolved via `pkg-config` cleanly.
`cargo check` in `apps/desktop/` got as far as real crate downloads
from crates.io before hitting one specific, precisely diagnosed wall:
`time-core v0.1.9` (pulled in transitively via `tauri 2.11.5` →
`cookie 0.18.2` → `time 0.3.55`, which pins `time-core` to exactly that
version) declares Cargo's `edition2024` feature — stabilized in Rust
1.85 (Feb 2025), over a year newer than Ubuntu 24.04's archived 1.75.0.

Traced the chain with `cargo tree`/`cargo update --precise`, not
guessed, and deliberately did not chase a workaround by forcing an
older `tauri`/`cookie`/`time` combination — that would test a fictional
dependency graph no real developer with an up-to-date toolchain would
ever actually get, not a legitimate check of the real build path. The
real fix is Rust 1.85+, and that's a genuine, disclosed constraint:
rustup and `static.rust-lang.org` aren't reachable through this
sandbox's network allowlist, and no newer `rustc` exists in the Ubuntu
archive either. Cleaned up the partial `Cargo.lock` and registry cache
this investigation generated (an incomplete byproduct of a failed
resolution, not something worth preserving) before moving on.

This replaces a vague, seven-plus-phases-unchanged disclaimer with a
precise one: the gap isn't "no Rust toolchain," it's "no Rust toolchain
new enough for one specific transitive dependency, for one specific,
named reason." README.md and DEVELOPMENT.md updated to match — see
DEVELOPMENT.md's Phase 9 entry for the full trace.

### Added — INSTALL.md, the end-user installation documentation spec section 31 names as a distinct Phase 9 deliverable

Distinct from DEVELOPMENT.md's developer-facing build instructions —
this is for someone who wants to *run* FORGE, not build it. Honest
about the actual current state rather than either skipping the
deliverable or writing installation steps for a release that doesn't
exist: opens by stating plainly that no packaged installer exists yet
for any platform (Phase 9's own build-script investigation got the
Go/frontend halves working but stopped at the Rust/Tauri gap — see
above), then documents what installation *will* look like once one
does (per-platform packaging expectations, why no runtime dependencies
need separate installation, the "no account, no network access during
setup" model spec section 2 requires) clearly labeled as forward-looking
rather than implying it's already true.

First-run behavior and data-directory documentation uses real,
previously-verified information rather than reading the code fresh and
guessing: the per-platform `os.UserConfigDir()` paths, the exact file
set (`config.json`, `forge.db`/`-wal`/`-shm`, `forge-core.log`), and
the `config.json` sample shown are all copied directly from real
running-instance output captured during Phase 8's crash-testing entry,
not hand-written from reading `internal/config/config.go` in isolation.
Also covers uninstalling (deliberately not deleting the data directory
automatically — consistent with how most desktop applications behave)
and where to go for security reports vs. general contribution.

Caught and fixed a citation error while writing the CHANGELOG entry for
this: the "installation process" deliverable is named in spec section
31 (the Phase 9 entry in "Development Phases"), not section 29 (the
"Documentation" section, which lists the six standard docs but doesn't
mention installation process as a separate item at all) — an earlier
draft of this changelog cited section 29; corrected before this entry
was finalized rather than left to propagate.

### Fixed — CONTRIBUTING.md and tests/README.md, the two remaining docs with real staleness

Went back and gave `CONTRIBUTING.md` the same line-edit pass README.md
and ARCHITECTURE.md got two entries ago, rather than leave it at "read
and found fine" — that earlier assessment was itself worth checking
more carefully. Found the exact stale claim flagged then: "see the note
in DEVELOPMENT.md about this repository's first build not having been
verified against a real toolchain yet — that applies to the initial
Phase 1 commit specifically." True once, unchanged since Phase 1,
increasingly misleading given how extensively the backend's actually
been exercised since. Replaced with a precise, current claim: 232+
tests run under `-race` repeatedly, real crash testing, real load
profiling, cross-compilation checks — pointing to DEVELOPMENT.md's
"Toolchain status" sections for specifics rather than restating them.

`tests/README.md` hadn't been checked at all until this pass looked for
more of the same problem — found it: the coverage table's header still
said "as of Phase 6," and several rows described tests Phase 8 had
since strengthened or added without updating the description.
Specifically: the `internal/health` and `internal/automation` rows'
"shell-metacharacter-is-literal regression test" description predates
Phase 8 rewriting those exact tests into genuine behavioral proofs (see
that phase's entry above) — the old description wasn't wrong, but it
undersold what the test now actually verifies. Added the
`internal/containers` and `internal/app` rows' missing mention of
Phase 8's argument-injection and empty-id-validation tests. Added a
cross-reference to Phase 9's Rust/Tauri investigation in the "not yet
covered" section, since "no tests exist for `apps/desktop`" now has a
more specific, useful companion fact: a real toolchain got further than
ever before before stopping at a named, diagnosed version gap.

### Still to do (rest of Phase 9)

- A real, complete release build (through the Tauri/Rust half) remains
  blocked — narrowed this phase to a precise, named cause (Rust 1.85+
  needed for one transitive dependency's Cargo edition2024 requirement;
  see above) rather than the vague "no toolchain" it was before, but
  still blocked, and the actual `cargo tauri build` step remains
  completely untested either way.

## [Unreleased] — Phase 8: Hardening (substantially complete)

Spec section 31's Phase 8 brief: race detection, profiling, memory
checks, concurrency review, security review, database review, error-path
review, crash testing, recovery testing, cross-platform testing. Started
with the security review, since SECURITY.md makes a set of specific,
falsifiable claims — the kind of document that should be checked against
the actual code, not trusted because it reads well.

*(Three entries above this one in the file — "Load profiling beyond
request-volume + concurrent health checks," "Crash-timing tests for
automation rule execution and event-bus delivery," and "Phase 8,
revisited" — were written later but close out nearly everything this
section's own "Still to do" list once named. They're placed
chronologically, at the top, rather than folded in here, but belong to
this phase's scope as much as anything below.)*

### Fixed — real command-injection vulnerability, found and fixed, not a false alarm

`internal/system/provider_windows.go`'s `ServiceDetail` built a
PowerShell script embedding the requested service name inside a
**double-quoted** string (`-Filter "Name='%s'"`), escaped with
`escapePowerShellSingleQuoted` — which doubles embedded `'` characters,
the correct escaping for a PowerShell *single-quoted* string context.
The escaping was correct for a context the code wasn't actually in.
PowerShell double-quoted strings interpolate `$(...)` subexpressions
during parsing regardless of what's nested inside them, so a service
name containing e.g. `$(some-command)` would have PowerShell evaluate
that subexpression while parsing the script — arbitrary command
execution as whatever FORGE Core is running as. Reachable by any
authenticated IPC caller: `service.detail`'s `name` parameter
(`internal/app/methods_service.go`) is validated only for
non-emptiness, decoded straight from the request into the vulnerable
call.

Found by tracing SECURITY.md's "every service name is treated as
untrusted data" claim into the actual code rather than accepting it —
the claim was true for four of the five service-name-interpolating call
sites (`StartService`/`StopService`/`RestartService`/`SetServiceEnabled`,
which all correctly use single-quoted PowerShell strings) and false for
the fifth. `ProcessDetail`'s structurally similar `-Filter
"ProcessId=%d"` was checked against the same pattern on suspicion, not
assumed safe by resemblance, and found genuinely safe: `pid` is an
`int32` formatted with `%d`, which can only ever produce digit
characters — there's no string value here for PowerShell to interpolate
anything from, so the vulnerability's precondition (attacker-controlled
*string* content) doesn't exist for that call site regardless of
quoting.

**Fix:** rather than adding a second, double-quote-aware escape function
layered under the existing one — correct in principle, but exactly the
kind of nested-quoting-context reasoning that produced this bug in the
first place and could easily produce another — `ServiceDetail` was
rewritten to remove the interpolation surface entirely. It now reuses
`ListServices`'s identical, already-argument-free CIM query (no name, no
`-Filter`, no string interpolation of any kind) and matches the
requested name in Go. Every field `ServiceDetail` returns comes from
that same query's columns; `Dependencies` was already left empty
regardless (a documented, pre-existing gap, unrelated to this fix), so
nothing was lost by routing through the bulk query instead.

**Verified, not just reasoned about:**
- Cross-compiled `internal/system` for `GOOS=windows GOARCH=amd64` from
  this Linux sandbox — `go build` and `go vet` both clean. This sandbox
  cannot run the result (the standing Windows/Darwin caveat — see
  DEVELOPMENT.md and this project's other Phase 7/8 notes on that), but
  a real cross-compilation check against the actual target is more than
  reasoning about a diff in isolation, and it's what was actually
  available here. Darwin and Linux cross-compiled clean too, confirming
  no regression to the platforms that share this file's build
  constraints.
- The full backend suite (`go build`/`go vet`/`go test -race
  -count=1 ./...`, same sandbox CGO SQLite stub as every prior phase's
  full-repo check) still passes all 228 tests after the change.

### Fixed — two security-critical tests that didn't actually prove what they claimed to

While verifying the vulnerability above, re-read
`TestCheckCommandNeverUsesAShell` (`internal/health/checkers_test.go`)
and its mirror `TestActionRunCommandNeverUsesAShell`
(`internal/automation/actions_test.go`) — both cited directly in
SECURITY.md as evidence for "the key security property" of the two
places FORGE executes user-configured commands. The tests' own code
comments admitted the gap: they ran `echo` with a `$(id)`-looking
argument and asserted the check reported success — which an
shell-interpreting regression would *also* produce, since `echo`
succeeds either way and the tests never observed actual output. A real
regression to shell execution would not have failed either test.

**Fix:** replaced both with a genuine behavioral proof. `false` always
exits 1 regardless of its arguments; the crafted argument `"; true"`
is a no-op literal string under real argv execution (so the check
stays UNHEALTHY, as `false` always produces) but would flip a
shell-interpreting implementation's result to HEALTHY, since a
POSIX shell's `sh -c` exit status is its *last* command's — `; true`
would make that `true`, not `false`. **Verified the test actually
catches what it claims to**, not just that it passes: temporarily
reintroduced shell execution into `checkCommand` (wrapping the argv in
`/bin/sh -c` with the arguments joined into a string), confirmed
`TestCheckCommandNeverUsesAShell` failed exactly as designed, then
reverted the change and confirmed the diff was byte-for-byte clean
again. A test that hasn't been shown to fail when the property it
guards is actually broken hasn't been shown to test anything.

### Fixed — self-triggering loop prevention was narrower than SECURITY.md's original wording implied

Continued tracing SECURITY.md's remaining claims rather than treating
the two fixes above as the whole review. The "self-triggering loop
prevention" claim — that no rule can be configured whose action
re-triggers itself into an unbounded cascade — checked out completely
for what it actually covers (an `automation.`-prefixed event can never
be a rule's trigger target, verified at both cited check sites), but
the claim as originally worded implied broader coverage than that
specific mechanism provides. Traced it against spec section 11's own
flagship automation example, not a contrived case: "WHEN process X
crashes THEN restart process X" triggers on a real OS-level
process-lifecycle event, not an `automation.`-prefixed one, so the
prefix check has nothing to do with it — and `restart_process` can
cause that exact same event to fire again if the restarted process
crashes immediately. `CooldownSeconds` — the field that would rate-limit
this — defaults to 0 in both the backend struct and the frontend form,
and 0 means "no cooldown at all" by that field's own explicit,
legitimate design (some rules genuinely want to fire on every single
occurrence). A rule built exactly as the spec's own example suggests
had nothing bounding how fast a restart loop could spin if the target
process were persistently broken.

**Fix:** `minRuleCooldownSeconds` — a 1-second floor applied to every
rule's effective cooldown regardless of configured value, including 0.
Checked first whether this could break any existing test's assumption
of true-zero-gap rapid refiring: none did (the two tests that publish
twice in one test function both use the second publish to test a
*different* rule/condition match, not a rapid re-fire of the same
already-fired rule). Explicitly not a complete fix: a genuinely
crash-looping process is still restarted forever, just at a bounded
rate instead of an unbounded one — a real circuit breaker
(auto-disable after N consecutive failures) would close that
completely and is a larger, separate change, not made here.

**Verified the same way as the shell-execution tests above, not just
asserted:** added `TestEngineZeroCooldownStillHasAFloor`, confirmed it
passes with the fix, then temporarily removed the floor and confirmed
the test fails with the exact symptom the fix prevents (`startedServices
= [nginx nginx]` — the action ran twice), then restored the fix and
confirmed the diff was byte-for-byte clean again. Full suite
(`go build`/`go vet`/`go test -race -count=1 ./...`, same sandbox
stub as every prior full-repo check) passes after the change.

### Fixed — container-action argument injection, found and empirically verified against a real docker daemon

Continued tracing SECURITY.md's remaining claims. "Container actions"
already checked out for what it explicitly claimed (argv-only
execution, no shell, no `docker exec`/`run`, no code-execution
capability at all) — but argv-only execution rules out *shell*
injection, not *argument* injection, a distinct class this document
hadn't addressed: a `docker` CLI flag disguised as a container `id`.
`containers.start`/`stop`/`restart`/`logs` all passed `id` straight
through with no format validation and no `--` end-of-flags marker
separating it from `docker`'s own options — an `id` beginning with `-`
risked being parsed by `docker`'s own argument parser as a flag instead
of a positional container reference.

**Verified empirically, not assumed from convention — the more
interesting part of this entry.** This sandbox had no `docker` binary
when the earlier fixes in this phase were made, so the initial version
of this fix was written with an honest "can't verify this against a
real binary" caveat, the same shape as the standing Windows/Darwin
limitation. That turned out to be fixable: `docker.io` 29.1.3 is
available from the standard Ubuntu archive (`archive.ubuntu.com`,
already reachable) — the *exact* version DEVELOPMENT.md's Phase 6 notes
say a prior phase used for its own real-Docker testing, installed and
run the same way (`apt-get install docker.io`, `dockerd` started
manually — no systemd in this sandbox). With a real daemon up:

```
$ docker start --rm
unknown flag: --rm
$ docker start -- --rm
Error response from daemon: No such container: --rm
```

The first proves the vulnerability was real: docker's CLI genuinely
does attempt to parse a leading-dash `id` as a flag when nothing stops
it. The second proves the fix works exactly as intended. Repeated
across every affected subcommand, including the one case worth checking
specifically — an `id` matching a *genuinely valid* flag rather than an
invalid one (`docker logs --tail 200 -f`, since `-f`/`--follow` is
real) — and every single case produced a safe usage or "no such
container" error, never a successful unintended action. That's an
honest severity finding, not just a fix confirmation: the original
gap's real-world exploitability, given Docker's actual flag surface for
these four subcommands specifically, was consistently low. Worth fixing
on correctness grounds and as defense against a future Docker CLI
version adding a more consequential flag — not worth overstating as
more dangerous than what was actually observed.

**Fix:** `--` inserted before `id` in all four `docker` invocations
(`internal/containers/provider.go`) — the same "change how the argument
is interpreted, don't filter the value" structural approach as the
PowerShell fix earlier in this phase, one layer up (a CLI's own flag
parser instead of a shell). Also added the missing empty-`id` rejection
to `containers.start`/`stop`/`restart`'s IPC handlers
(`internal/app/methods_containers.go`) — `containers.logs` already had
this check; the other three didn't, an inconsistency independent of the
injection issue but found while in the same code.

**Test coverage, at both the level that can run everywhere and the
level that needed the real daemon:** `TestProviderIDsAlwaysFollowADoubleDash`
proves FORGE's own side structurally — that `--` always appears before
`id` in the constructed argv — using the existing `fakeRunner`, so it
runs in any environment regardless of whether Docker is installed.
`TestProviderRealDockerIntegration` (pre-existing, previously always
skipped in this sandbox for lack of a daemon) ran for real for the
first time this phase and passed. Also added
`TestRegisterMethodsContainersActionsRejectEmptyID` for the empty-id
validation fix, following the exact pattern
`TestRegisterMethodsServiceDetailRejectsEmptyName` already established.
Full suite (`go build`/`go vet`/`go test -race -count=1 ./...`, same
sandbox stub as every prior full-repo check) passes after the change,
run with the real Docker daemon still up so `internal/containers`'s
suite exercised the real-daemon path too, not just the faked one.

### Verified — real crash/recovery testing against the compiled binary, not just unit tests

Spec section 27/31 explicitly names "crash testing" and "recovery
testing" as their own Phase 8 items, distinct from unit tests (which
already cover `internal/scheduler`'s missed-execution policy
deterministically with fakes — this was about the real thing: the
actual compiled `forge-core` binary, a real SQLite file, real `SIGKILL`,
real process restarts). Unlike every other entry in this phase, this
one didn't find a bug — it's a genuine verification result, documented
with the same rigor as a fix would be rather than left unrecorded just
because nothing was wrong.

**Setup:** built the real `cmd/forge-core` binary (with the same
sandbox CGO SQLite stub as every other full-repo check this session),
ran it against an isolated `XDG_CONFIG_HOME`, and drove it over its
real IPC protocol with `curl` — the actual `POST /rpc` endpoint,
`fwp.Envelope` JSON, the real bearer token parsed from the real
handshake line on stdout. Not `router.Dispatch` in a test — the
literal binary a user would run, reached the way the Tauri shell
actually reaches it.

**SIGKILL mid-write:** created 5 health checks over real IPC
(confirmed via a follow-up `health.list` that all 5 landed), then fired
8 more concurrent schedule-creation requests and `kill -9`'d the
process with no delay — no grace period, maximizing the chance of
catching a write in flight. `PRAGMA integrity_check` against the raw
`.db` file immediately after: `ok`. All 8 in-flight schedule requests
got no response at all (killed before or during the request) and
correctly landed **zero** partial rows — consistent with SQLite's WAL
transaction atomicity, not a partial-write bug. Restarted `forge-core`
against the same (crashed) data directory: clean startup, no errors: on
stderr, and all 5 previously-committed health checks were still present
and visible through a real `health.list` call — genuinely committed
data survives a raw kill, not just a graceful shutdown.
`schema_migrations` timestamps were identical before and after the
restart, confirming migrations don't re-run destructively.

**Missed-execution policy, tested for real, both directions:** created
a real interval task (10-second interval — the minimum the API
allows; itself a nice small finding, an enforced floor in the same
spirit as this phase's automation cooldown floor, already correct and
already in place, not something this phase needed to add). Let it fire
normally once to confirm baseline behavior (`next_run_at` correctly
recomputed as firing-time + interval, not schedule-time + interval,
matching `internal/scheduler/engine.go`'s documented policy exactly).
Then `SIGKILL`'d `forge-core` and restarted it *after* the task's next
due time had already passed while it was down — `last_run_at` on
restart showed the task fired exactly once, at the restart moment, not
zero times (permanently stuck) and not multiple (a burst through
every missed interval). Then tested the other direction: killed and
restarted again *before* the task's newly-computed next due time —
`last_run_at` was unchanged from its last real firing, not bumped to
the new restart's timestamp, confirming a restart that isn't actually
overdue doesn't spuriously re-fire. Both directions of "deterministic,
no duplicates" from spec section 12's own wording, both confirmed
against the real binary, not just trusted from the code comment or the
existing unit tests.

**Honest about what this didn't test:** the exact sub-millisecond race
between persisting `NextRunAt` and publishing `TypeTaskDue` (the actual
mechanism the "persist before publish" comment in `engine.go`
describes) isn't something external `kill -9` timing can reliably hit —
`internal/scheduler`'s own scan interval is a full second, and hitting
a race narrower than that from outside the process isn't realistic
without kernel-level breakpoints. That precise boundary is exactly what
the package's existing deterministic unit tests (fakes, not real time)
are for, and they already pass under `-race`. What this crash-testing
session verified is the broader, user-visible property — a task that
missed its window during real downtime fires once, not zero or many —
which the unit tests can't fully stand in for since they don't exercise
process death and a real SQLite file's WAL recovery.

### Verified — real goroutine-leak and idle-CPU profiling against the compiled binary

Spec section 25 names goroutine leaks explicitly as something to avoid
and requires "every goroutine must have a clear lifecycle"; section 26
sets a "low idle CPU usage" target. Neither had been measured against
the real binary before this. Added a temporary `net/http/pprof` import
and a loopback-only debug listener directly to `cmd/forge-core/main.go`
— never committed, reverted (and confirmed byte-for-byte identical to
its original via `diff`) before this entry was written — the same
"instrument locally, verify, revert, prove the revert is clean"
discipline as every other temporary sandbox tool this phase.

**Idle CPU:** measured real `utime`+`stime` deltas from `/proc/$PID/stat`
across settled 10s and 30s windows with nothing else happening. Result:
1 clock tick (10ms) of CPU time over 30 seconds wall-clock — about
0.03% of one core. Genuinely low, not just claimed.

**Goroutine leaks, tested at the two places most likely to have one:**

- **Event-stream disconnects** (`internal/ipc`'s `/events` handler) —
  the classic leak vector: a goroutine blocked forever writing to a
  client that vanished without closing cleanly. Baseline: 23 goroutines
  at idle, all named and accounted for (one per subsystem's run loop —
  metrics ×4, scheduler, automation, event persistence, retention
  pruner, heartbeat, IPC server — each correctly blocked in `[select]`
  or `[semacquire]`, none busy-looping). Opened and abruptly killed
  (`curl --max-time`, a hard mid-stream abort, not a clean close) 20
  connections, then 100 more across five batches: goroutine count
  returned to exactly 23 every time, no drift.
- **Health check lifecycle** (spec section 9's "must run concurrently
  but must have bounded resource usage") — each check gets its own
  `runLoop` goroutine (confirmed via the goroutine dump, not assumed):
  creating 15 checks took the count from 23 to 38, exactly matching;
  deleting all 15 returned it to exactly 23. Ran five more full
  create-10/delete-10 cycles (50 additional checks total) specifically
  to catch a slow, cumulative leak a single cycle might not reveal:
  still exactly 23 afterward.
- Memory tracked alongside all of this via the real heap profile
  (`HeapAlloc` ~3MB) and RSS (~17MB, `/proc/$PID/status`) — both
  identical before and after the full stress sequence.

**Honest about scope:** this tested the two subsystems with the
clearest per-resource goroutine lifecycle (event streaming, health
checks) and idle-state CPU/memory. It did not profile CPU or memory
*under sustained heavy load* (spec section 26 also targets "UI should
remain responsive under heavy process/event activity" and "backend
should have low idle CPU usage" specifically, which this covers, but
not a full load-test); it did not check for goroutine leaks in
automation rule execution, scheduler task lifecycle, or container
polling specifically — those are structurally similar to the health
check case (same `Engine.startLocked`/`Stop` pattern per
ARCHITECTURE.md) but weren't independently profiled the same way.

### Verified — the last untraced SECURITY.md claim, input validation, checked and one wording correction made

The final remaining SECURITY.md claim from this phase's security
review, traced with the same rigor as every other one — this time the
claim held up almost entirely, with one precision gap worth correcting
rather than a behavioral bug worth fixing.

**Checked, not assumed:** all 27 `json.Unmarshal(params, &p)` call
sites across `internal/app`'s method handlers, via a script rather than
spot-reading a sample — every single one follows the identical
`if err != nil { return nil, fwp.NewError(fwp.ErrInvalidParams, ...) }`
shape, zero deviations. Confirmed no handler anywhere uses
`map[string]interface{}`/`map[string]any` for params — "typed structs,
not loosely-typed maps" holds throughout, not just in the examples the
original claim implicitly gestured at.

**Verified empirically against the real running binary**, the same
"don't just read the code" standard as every other claim this phase:
sent a type-mismatched value (`interval_seconds` as a string) to
`health.create` — correctly returned `INVALID_PARAMS` with Go's own
descriptive type-mismatch message; sent genuinely malformed JSON —
correctly failed the same way, even earlier, at envelope parsing.

**The one gap:** sent a fully valid `health.create` payload plus one
extra, meaningless field. It succeeded normally — the extra field was
silently dropped, not rejected. `DisallowUnknownFields` isn't called
anywhere, so `encoding/json`'s default permissive behavior applies.
SECURITY.md's original wording ("a malformed or unexpected shape
fails") technically overstated this — "unexpected shape" reads as
covering extra fields too, and it doesn't. **Not fixed, because it
isn't a bug**: permissively ignoring unknown fields is the standard,
deliberate choice for a protocol expected to gain fields across
versions — a client sending one fewer or one more field than a handler
expects should degrade gracefully, not hard-fail on shape alone.
Rejecting unknown fields would be a real, if minor, forward-compatibility
regression for no actual security benefit, since accepting harmless
extra JSON gives an attacker no capability they didn't already have
with a well-formed request. SECURITY.md's wording was corrected
instead — "fails on a type mismatch or malformed JSON" rather than
"fails on any unexpected shape."

This closes out every SECURITY.md claim flagged as untraced across this
phase's security review — the review itself (as opposed to the rest of
Phase 8's still-open profiling-under-load and full concurrency-pattern
work) is now complete.

### Verified — profiling under real sustained load, not just idle

The earlier profiling entry explicitly named "profiling under sustained
heavy load" as not done. Closed that specifically: same temporary
`net/http/pprof` instrumentation (added, used, reverted, `diff`-confirmed
clean afterward — identical discipline to every other entry this
phase), this time with real concurrent work running throughout instead
of an idle process.

**Load generated:** 30 health checks at the minimum allowed interval
(10s — all firing concurrently, continuously, for the whole test) plus
a 60-second burst of continuous real IPC request traffic
(`health.list` in a tight loop) — 10,409 requests actually sent and
processed (confirmed by the load generator's own count, not estimated),
about 173 requests/second sustained.

**First attempt had a real bug worth noting rather than quietly
fixing:** the load-generator script used bash's `$SECONDS` in a `while`
loop launched via `setsid nohup ... &`, which silently ran under `sh`
(not `bash`) in that context and doesn't support `$SECONDS` — the loop
condition failed immediately (`[: -lt: unexpected operator`) and the
script exited having sent zero requests, without the wrapping shell
reporting it as an error. Caught because the goroutine/CPU numbers over
that window looked identical to the health-checks-only baseline, which
prompted checking the load generator's own log rather than trusting
that "the numbers looked stable" meant "the load actually ran" — a
useful reminder that a profiling result is only as good as the
confirmation that the load it's supposed to be measuring genuinely
happened. Fixed by using `date +%s`-based timing (POSIX-portable)
instead of `$SECONDS`, and running it explicitly under `bash` — reran
for real, with the request count logged so this run couldn't have the
same silent-no-op failure mode uncaught.

**Results, real load confirmed via the corrected script:**
- Goroutines: 23 at idle → 53 with 30 concurrent health checks running
  (one goroutine per check, exactly as expected — same relationship
  confirmed in the idle-profiling entry above) → still exactly 53 after
  10,409 real requests over 60 seconds. No growth from request volume.
- CPU: ~5.4 seconds of actual CPU time to process the full 60-second,
  10,409-request burst — roughly 9% of one core average while under
  load. Settled back to within 110ms of CPU time over the next 15
  seconds once the load stopped — confirms the cost was genuinely
  request-driven work, not a runaway background process that kept
  spinning after load ended.
- Heap: stayed small and stable throughout (~2.7MB `HeapAlloc`,
  comparable to the idle baseline), with the garbage collector running
  237 times during the load — correctly reclaiming memory continuously
  rather than letting it accumulate. RSS grew by roughly 300KB under
  the full load, not a meaningful leak.

Between this entry and the idle-state one, spec section 26's
performance targets — low idle CPU, bounded resource usage under
activity, no goroutine leaks — are now verified against the real
compiled binary under both idle and real sustained load, not assumed
from either the code reading alone or a partial (idle-only) test.

### Verified — deliberate concurrency-pattern review, beyond what the race detector catches

`-race` has run on every full-suite check all session, but it only
catches races that actually manifest during a specific test run — it
says nothing about deadlock potential, unbounded resource growth, or
panic-safety in cleanup paths. Spec section 25 names several of these
explicitly ("avoid... goroutine leaks... unbounded channels...
unnecessary locks," "every goroutine must have a clear lifecycle").
Did a deliberate, traced-not-skimmed review of the two most
concurrency-heavy packages plus a codebase-wide sweep for one specific
pattern. Result: everything checked out. No bugs found this entry —
recorded with the same rigor as a fix regardless, per this phase's
running standard.

**`internal/eventbus`'s close/publish race, traced explicitly:** does
`cancel()` closing a subscriber's channel ever race with `Publish()`
sending on it (a send-on-closed-channel panic waiting to happen)?
Traced through the actual lock scopes: `cancel()` performs the map
deletion *and* the channel close inside the same `b.mu.Lock()` critical
section; `Publish()` performs the map iteration *and* the send inside
the same `b.mu.RLock()` critical section. Since Go's `RWMutex`
guarantees mutual exclusion between a writer and any reader, `Publish()`
can never observe a subscriber mid-removal — by the time its `RLock()`
succeeds, that subscriber is either still fully valid or already fully
gone from the map, never in between. Also traced the double-close risk
between `cancel()` and `Close()` racing each other: both guard their
close with an `if _, ok := b.subscribers[sub.id]; ok` check performed
under the same lock as the close itself, so whichever one runs second
finds the subscriber already removed and skips closing it again.

**`internal/automation`'s two-mutex lock ordering, traced across every
call site that touches both:** `Engine` has `e.mu` (the rule-map lock)
and each `trackedRule` has its own `tr.mu` (per-rule state). Two
distinct mutex types nested together is exactly the shape that produces
AB-BA deadlocks if any code path acquires them in inconsistent order.
Checked all five `tr.mu`-holding methods (`snapshot`, `snapshotRule`,
`setRule`, `checkCooldown`, `recordOutcome`): every one is a small,
self-contained field accessor that never calls back into anything that
touches `e.mu`. Checked every `e.mu`-holding method that also touches a
`trackedRule`: all of them (`List`, `Update`, and `handleEvent`'s match
loop) nest in the same direction, `e.mu` outer → `tr.mu` inner, never
reversed. A consistent global ordering is sufficient to rule out
lock-ordering deadlocks between these two mutexes.

**Unbounded channels, swept for codebase-wide, not just where a bug
happened to already be suspected:** every `make(chan ...)` in
`internal/`+`pkg/` — seven sites total — is either explicitly bounded
with a named constant (`DefaultMaxConcurrentExecutions`,
`DefaultMaxConcurrentChecks`, `DefaultSubscriberBuffer`) or a
signal-only `chan struct{}`/`chan error` with buffer ≤1 that never
queues arbitrary data. Zero unbounded channels anywhere.

**Semaphore panic-safety, traced in both places a concurrency-limiting
semaphore exists** (`internal/automation` and `internal/health`, both
using a buffered `chan struct{}` as a counting semaphore): in both,
`defer func() { <-e.sem }()` is registered immediately after acquiring,
before any actual work runs — Go's defer-on-panic guarantee means the
permit releases even if the work panics, so a leaked permit (and a
permanently-reduced effective concurrency limit) isn't possible from
this path in either package.

**`internal/automation`'s nested `wg.Add(1)`, traced for the classic
`Add`-races-`Wait` hazard:** the event-consumer goroutine calls
`wg.Add(1)` once at startup; each spawned action-execution goroutine
calls it again, nested inside the same synchronous call chain
(`consumeEvents` → `handleEvent` → `tryRun`) that the *first* `Add(1)`'s
goroutine is still running. Since that outer goroutine's own count is
always still held (not yet `Done()`'d) at the exact moment the nested
`Add(1)` fires — they're the same still-executing call stack — the
total counter can never be observed transiently at zero at that point,
which is exactly the property needed to rule out the hazard.

**Scope, stated honestly at the time this entry was first written:**
this covered `eventbus` and `automation` in depth, plus one targeted
codebase-wide sweep (unbounded channels) and one targeted pattern check
repeated across two packages (semaphore panic-safety). It did not do
the same depth of review on `internal/scheduler`, `internal/health`'s
broader locking (beyond the semaphore), `internal/metrics`, or
`internal/logs` — at the time, those weren't found to have a specific
reason for suspicion the way the two-mutex shape in `automation` did.
**That "not reviewed at this depth" distinction — deliberately kept
separate from "confirmed clean" — turned out to matter for exactly the
package named first in that list**: see the very next entry below for
a real, genuine data race found in `internal/scheduler` shortly after
this one was written, by routine verification rather than the deferred
review this paragraph describes. Left this paragraph's original wording
intact rather than editing it after the fact to look more prescient
than it was — the honest record is that `scheduler` was named as
lower-confidence, not that a specific bug was predicted there.

### Fixed — a real, genuine data race in `internal/scheduler.Engine.Create`, found by the race detector during routine verification, not a deliberate hunt

While re-running the standard full-suite verification after an
unrelated documentation change, `TestAppEndToEnd`'s
"scheduled automation rule fires end to end" subtest — a test that had
passed cleanly on every single one of dozens of full-suite runs across
this entire session — failed with `WARNING: DATA RACE`. Did not
dismiss this as flakiness and re-run past it: Go's race detector does
not produce false positives, so a race that fires even once, however
rarely, means a genuine bug exists, whether or not it reproduces on
demand.

**Reproducing it took real effort, which is itself informative about
its rarity.** Fifteen separate re-runs of the single failing subtest
all passed clean. `-count=20` in a single process invocation caught it
on the first attempt; a follow-up `-count=30` run caught it twice.
Added temporary `fmt.Printf` debug logging (pointer addresses, task
IDs) to the two functions the race report named, reproduced again, and
read the actual runtime values directly rather than continuing to
reason from static code alone.

**Root cause, confirmed empirically:** `scheduler.Engine.Create`
released `e.mu.Unlock()` *before* its `return t, nil` statement. That
return statement copies every field of `t` — but `&t` had already been
stored into `e.tasks` (the shared, concurrently-accessed map) earlier
in the *same* locked section. The instant that pointer is reachable
from the map, the scheduler's own background scan loop can pick it up
on a separate goroutine and start mutating its fields via `fire` — if
the new task's `NextRunAt` is already due by the very next scan tick,
which a task created with a near-immediate run time (exactly what the
failing subtest does: a schedule due 500ms out) makes into a real,
if narrow, window rather than a theoretical one. `Create`'s sibling
functions in the same file — `Update`, `SetEnabled` — do not have this
bug: both use `defer e.mu.Unlock()`, so their own `return`-time field
reads happen *before* the deferred unlock fires. `Create` was the one
function using an explicit `Unlock()` ahead of its return instead,
making it the one place this exact bug could exist.

**Fix:** changed `Create` to use `defer e.mu.Unlock()`, matching the
pattern its siblings already use — a two-line change (move `Unlock()`
into a `defer` placed right after `Lock()`) plus a doc comment
explaining why, for whoever next touches this function and might be
tempted to "simplify" it back to an explicit unlock.

**Verified the fix actually closes the window, not just that the
original failure stopped recurring by chance:** ran the exact
originally-failing test 50 consecutive times under `-race` with the
fix in place — clean every time, a meaningful sample given the bug
reproduced twice within a single 30-iteration run beforehand.

**Added a real regression test, and was honest in its own comment
about what a race-condition regression test can and can't promise.**
`TestEngineCreateRacingConcurrentScan` — the first attempt (a single
scanner goroutine racing a single serial loop of `Create` calls)
essentially never caught the bug even across ten separate runs with it
deliberately reintroduced; genuine concurrent parallelism (four
scanner goroutines racing ten concurrent creator goroutines, repeated
across 30 attempts per test run) caught it 5/5 times once
reintroduced, and passed cleanly 5/5 times with the real fix restored.
Tuned this empirically by actually testing weaker designs and watching
them fail to catch the bug, not by assuming a design was sufficient.
The test's own doc comment states plainly that it cannot guarantee
catching a future reintroduction, only make it likely — an honest
limit of race-detector-based regression testing, not a gap specific to
this test.

Full suite (`go build`/`go vet`/`go test -race -count=1 ./...`, same
sandbox stub as every other full-repo check this session) passes clean
after the fix, including the new test.

### Verified — swept every explicit mutex unlock in the backend for the exact same bug shape, found nowhere else

Once the `scheduler.Create` bug's precise mechanism was understood —
an explicit `Unlock()` followed by code that reads memory already
aliased into shared state during the locked section — that became a
concrete, mechanically searchable signature, not just a one-off fix to
move on from. Found and individually checked every explicit
(non-`defer`) mutex unlock in `internal/` — 20 sites total across
`automation`, `scheduler`, `logs`, `health`, and `eventbus` (found via
`grep -rn "\.mu\.Unlock()"` filtered to exclude `defer`, then read each
one in context, not just counted).

Result: `scheduler.Create` was the only instance. Every other site is
safe, for one of three distinct, confirmed reasons — not assumed
identical, since three genuinely different mechanisms are at play:

- **Correct `defer` usage** — most of `scheduler`'s own siblings
  (`Update`, `SetEnabled`) and several sites in `automation`/`health`
  already use `defer e.mu.Unlock()`, which keeps the lock held until
  after any return-value read completes.
- **The read afterward touches only an independent local copy, not the
  aliased shared memory** — `logs.go`'s single site builds `entry` as a
  plain local value, copies it *into* the shared slice (not aliases it
  via a pointer), and only reads the local `entry` afterward. This is
  the structurally safe version of what `scheduler.Create` got wrong:
  copying a value into shared state is fine to read from afterward;
  aliasing your own local variable into shared state and continuing to
  read through that same local is not.
- **The object read afterward has its own independent lock** —
  `automation`'s `trackedRule` and `health`'s `trackedCheck` both wrap
  their own `sync.Mutex` and expose self-locking accessors
  (`snapshot()`, `snapshotCheck()`). Calling `tr.snapshot()` or
  `tc.snapshot()` after releasing the engine-level lock is safe because
  those methods acquire their *own* lock before touching anything —
  this is exactly the structural difference from `scheduler.Task`,
  which has no per-task lock of its own at all and is protected
  *entirely* by `Engine.mu`, with nothing to fall back on once that
  lock was released early.

This confirms the `scheduler.Create` bug was a genuine one-off
inconsistency — the one function using an unsafe pattern its own
siblings didn't — rather than a systemic issue replicated elsewhere in
the codebase. Worth stating plainly rather than leaving it implied:
finding one instance of a bug is a reason to check for the same shape
elsewhere, and "checked and found clean" is itself worth recording with
the same rigor as a fix, not left unstated because nothing else turned up.

### Verified — the remaining concurrency-review gap closed: `metrics`, `logs`, and `health` all given the same whole-package depth as `eventbus`/`automation`

Every prior concurrency-review entry this phase carefully distinguished
"not reviewed at this depth" from "confirmed clean" — a distinction
that turned out to matter once for `scheduler`. Rather than let that
distinction stand indefinitely as an open gap for the three packages
still named in it, closed it: read `internal/metrics` and
`internal/logs` in full, and `internal/health/engine.go`'s complete
locking structure, the same way `eventbus`/`automation` were read
earlier.

**`internal/metrics`** — clean throughout. `buffer.go`'s four methods
all correctly use `defer`, and its two read-returning methods
(`current`, `rolling`) each build a fresh slice with copied values
rather than ever aliasing internal state — `rolling` in particular
does an explicit `copy()` into a freshly `make()`'d slice, precisely
avoiding the trap `scheduler.Create` fell into. `engine.go`'s one other
mutex (`netMu`, guarding the previous-network-sample state used for
throughput deltas) has exactly one Lock/Unlock pair, already
`defer`-protected. All four of `Engine`'s background goroutines
(`Start` calls `wg.Add(4)` once, synchronously, before spawning any of
them — trivially safe, no nested-`Add` reasoning required the way
`automation` needed) use the same correct `select` -on-`ctx.Done()`-vs-
`ticker.C` shape, with one worthwhile design detail: on shutdown, the
flush loop deliberately flushes once more using a *fresh*
`context.Background()` rather than the just-canceled `ctx`, so the
final partial interval of samples isn't silently dropped.

**`internal/logs`** — clean throughout (212 lines, read in full,
not sampled). `Capture`'s one explicit-unlock site was already checked
in the prior sweep; `Query` uses `RLock`/`defer RUnlock` and builds its
result by copying each `Entry` *value* out of the ring buffer into a
fresh slice while still holding the lock — the internal buffer's
backing array is never exposed to a caller in any form.

**`internal/health/engine.go`** — the same `Engine.mu`/
`trackedCheck.mu` two-mutex shape as `automation`'s
`Engine.mu`/`trackedRule.mu`, already established safe there via
consistent lock-ordering (outer engine lock never nested inside the
per-item lock). `Get`/`Create`/`Update`/`Delete`/`RunNow`'s explicit
unlocks were checked in the prior sweep and found safe (each reads
only through `trackedCheck`'s own self-locking accessors after
releasing `e.mu`); `Start`'s two Lock/Unlock pairs — not previously
checked individually — read only the local `ctx` parameter and use
`defer` respectively, both safe.

This closes every concurrency-review gap this phase's earlier entries
left open by name. Not a claim that every line of Go in this backend
has been read for concurrency correctness — `internal/app`,
`internal/system`'s platform providers, and `internal/containers`
weren't part of this sweep, mostly because none of them hold
significant shared mutable state protected by their own locks the way
the six packages above do — but the specific, named gap ("not reviewed
at this depth") that six earlier entries collectively pointed at is now
fully addressed, one package at a time, with a stated reason for each.

### Still to do (rest of Phase 8)

This phase is not complete. Explicitly still open, per spec section
31's brief — updated to reflect the "Phase 8, revisited" entry above,
which closed four of the items this section used to list:

- ~~Profiling, memory checks, idle-CPU, and under-load verification~~ —
  **done**, including both specific scenarios this section used to name
  as not yet covered: a large real process list under continuous
  polling, and scheduler/automation under sustained, staggered
  real-world-shaped load rather than a synthetic burst — see the
  "Load profiling beyond request-volume + concurrent health checks"
  entry above. Combined with the goroutine-leak, idle, and
  request-volume profiling already done, no further known profiling
  gaps.
- A deliberate concurrency-pattern review — done in full, including the
  one piece this section used to list as the sole remaining gap:
  `scheduler`'s scan loop internals (`Start`, `scanOnce`, `fire`) read
  as a cohesive whole, which is exactly what surfaced the real bug
  fixed above (a stale-pointer race between `fire` and a concurrent
  `Delete`/`Update`). `eventbus` and `automation` covered in depth
  (lock ordering, close/publish races, semaphore panic-safety,
  WaitGroup nesting); `scheduler`'s `Create`/`Update`/`SetEnabled`/
  `Delete`/`List`/`Get` individually confirmed safe; `metrics`, `logs`,
  and `health`'s locking given the same whole-package depth, plus two
  codebase-wide sweeps. No further known gaps at this level.
- ~~Crash testing and recovery testing~~ — **done**, including the
  specific gap this section used to name: crash timing during
  automation rule execution and event-bus delivery, tested against the
  compiled binary with real `SIGKILL`s — see the crash-testing entry
  above, which also turned up a real (non-corruption) bug along the
  way. Combined with the SQLite crash safety and missed-execution
  policy work already done, no further known gaps at this level.
- The rest of the "database review" and "error-path review" —
  `internal/app/retention.go`'s orchestration layer (the hourly sweep,
  error handling, the `<=0` disables guard) is now fully tested — see
  the "Phase 8, revisited" entry above; it was the one specific,
  named gap in this item. Beyond that: not systematically done: only
  the paths touched by the security review above, plus retention.go,
  were examined.
- The broader cross-platform testing gap remains exactly what it's been
  since Phase 6: Windows and Darwin code is spec-accurate and
  cross-compilation-verified, but has never executed on real hardware.
  Notably, the docker-daemon and CGO-SQLite-driver workarounds this
  project has relied on (see above) have no equivalent for
  Windows/Darwin — neither trick translates to "a real Windows or
  macOS kernel" — so this gap remains exactly as open as it's been
  since Phase 6.
- ~~A real circuit breaker for repeatedly-failing automation rules~~ —
  **done**, see the "Phase 8, revisited" entry above
  (`automation.MaxConsecutiveFailures`).
- SECURITY.md's own security-claim review is complete (see above) —
  every claim the document makes has been independently traced against
  the code and, where relevant, against the real running binary, not
  just read and trusted. What SECURITY.md does *not* cover — the
  broader "database review"/"error-path review" items above, general
  concurrency correctness beyond the race detector, and load-testing —
  remains open, but that's genuinely different scope from "did the
  document's own claims hold up."

## [Unreleased] — Phase 7: UX Refinement (substantively complete)

Spec section 31's Phase 7 brief is "fix inconsistent spacing, weak
hierarchy, unnecessary information, confusing workflows, loading
states, error states, empty states, accessibility, keyboard navigation,
performance" — explicitly not a re-skin. This entry covers what was
actually done across the phase; see its final "Still to do" section
below for the honest remainder (formal sign-off, not undiscovered
work) before Phase 8 began.

### Added

- **First full, real build/test verification of the entire repository**,
  not just the incremental per-package verification each prior phase
  did as it went. Using the same sandbox-only CGO SQLite stub Phases
  4–6 established (a local, uncommitted `replace modernc.org/sqlite =>
  ...` pointed at a driver backed by the system's `libsqlite3`, deleted
  before this phase's changes were considered done — check `go.mod` has
  no `replace` line), this phase ran `go build ./...`, `go vet ./...`,
  and `go test -race -count=1 ./...` across every package at once for
  the first time, plus `npm install && tsc --noEmit && eslint . && npm
  run build` on the frontend. All green: **228 tests pass under
  `-race`**, including the full-stack `internal/app` end-to-end tests
  that were previously only run in isolation from `internal/storage`.
  The stub itself had two real gaps, both found and fixed by this
  process (the entire point of running it for real rather than trusting
  it compiles): it didn't accept `time.Time` as a bind parameter
  (`database/sql/driver.Value` permits it; the stub's `bind` switch
  didn't), and it returned plain Go strings for every TEXT column
  instead of parsing DATE/TIME-affinity columns into `time.Time` on
  read, which is what `modernc.org/sqlite` actually does and what
  `internal/storage`'s `Scan(&someTime)` calls depend on — fixed by
  reading each column's declared type (`sqlite3_column_decltype`) and
  parsing accordingly.
- **`useDialogKeyboard`** (`frontend/src/hooks/`) — a shared keyboard/
  focus hook now used by all five modal overlays in FORGE
  (`ConfirmDialog`, `HealthCheckFormDialog`, `AutomationRuleFormDialog`,
  `ScheduleFormDialog`, `CommandPalette`). Before this phase, each of
  the five independently reimplemented its own open/Escape/initial-
  focus effect, and **none of them trapped Tab** — tabbing through an
  open dialog could walk focus out into the page behind it, a real
  WCAG 2.1 modal-dialog violation, not a cosmetic gap. The shared hook
  now gives every dialog: focus moves into the dialog on open (to a
  given `initialFocusRef`, or the first focusable element), Tab/
  Shift+Tab cycle within the dialog instead of escaping it, Escape
  closes it, and focus returns to whatever triggered the dialog on
  close. `CommandPalette` keeps its own Arrow/Enter handling as a
  separate effect (a second `keydown` listener checking different
  keys — no conflict) since that behavior is specific to the palette,
  not shared dialog chrome; `ConfirmDialog` keeps its existing
  "focus starts on the primary action button" behavior via
  `initialFocusRef`, unchanged from before this phase.
- **Keyboard access to every list→detail selection row** — spot-
  checking beyond the dialogs surfaced a second, larger instance of the
  same underlying problem: the selectable rows in the list half of all
  five list/detail screens (Processes, Services, Health, Automation,
  Containers) were `<tr onClick=...>` with no `tabIndex`, no `Enter`/
  `Space` activation, and no focus indicator — mouse-only, despite spec
  section 20 calling for keyboard-first operation of "common
  workflows," and selecting an item to see its detail panel is about
  as common as workflows get. Same root cause as the dialogs: each
  screen reimplemented row selection independently, so the gap was
  systemic rather than a one-off oversight. Fixed with two small shared
  helpers (`frontend/src/lib/a11y.ts`): `onActivateKey` (Enter/Space
  activation, matching what a native `<button>` gives for free) and
  `FOCUS_RING` (a consistent visible-focus style, since none of these
  rows had ever needed one before). Applied to all five tables, plus
  `ProcessTree.tsx`'s tree rows, which had the identical gap in a
  slightly different shape — those became `role="treeitem"` inside a
  `role="tree"`/`role="group"` structure (matching the ARIA Authoring
  Practices tree pattern at the "keep the existing expand/collapse
  button in the tab sequence" level, not the fuller roving-tabindex/
  arrow-key-navigation version of that pattern — see the scope note
  below). The five table rows use `aria-selected` rather than
  `role="button"`/`aria-pressed`, deliberately: overriding a `<tr>`'s
  role would strip its native table semantics (row/column
  announcements) for screen reader users navigating the table, and
  `aria-selected` is the ARIA-correct attribute for "this row is the
  current selection" rather than "this is a toggle button," which is
  what `aria-pressed` actually means.

### Scope note: tree keyboard navigation

`ProcessTree.tsx` rows are now individually focusable and
Enter/Space-activatable, and the tree has correct `tree`/`treeitem`/
`group` roles — but arrow-key navigation between tree items (Down to
next visible row, Right to expand, Left to collapse/move to parent)
is not implemented; each row is a separate Tab stop instead. The full
ARIA Authoring Practices roving-tabindex tree pattern is a real,
scoped follow-up, not done here — flagging it explicitly rather than
letting "keyboard accessible now" quietly overstate how complete it
is.

### Added — process list virtualization (measured, not assumed)

Spec section 26 is explicit: *"Do not optimize blindly. Measure
first."* Phase 3's deferred-virtualization decision was itself
reasoning from that same principle ("premature virtualization... before
measuring") — so before touching `ProcessesPage.tsx`, this phase built
a real measurement harness rather than assuming virtualization was
either needed or unneeded.

**Method:** a standalone Vite + Playwright harness
(`/home/claude/perf-harness`, not part of the repo — a throwaway
measurement tool, not a shippable artifact) reproducing `ListView`'s
exact table markup with the real compiled FORGE stylesheet linked in
(a first attempt without the real CSS gave meaningless numbers — an
unstyled `<tr>` measured 20px tall against the real 35px, which would
have invalidated every layout-cost measurement downstream; worth
recording since it's exactly the kind of silent-fidelity-gap that
makes a "measurement" worthless without noticing). Real Chromium via
Playwright, not jsdom — this is layout/paint/reconciliation cost,
which jsdom doesn't model. Tested at N = 200 / 2,000 / 5,000 / 10,000
/ 20,000 synthetic processes.

**Results — initial hypothesis (scroll jank) was wrong; measurement
corrected it:**

| N | render (ms) | DOM nodes | scroll step (ms) | filter keystroke (ms) |
|---|---|---|---|---|
| 200 | 80 | 1,224 | ~0 | 36 |
| 2,000 | 246 | 12,024 | ~0 | 117 |
| 5,000 | 361 | 30,024 | ~0 | 335 |
| 10,000 | 621 | 60,024 | ~0 | 395 |
| 20,000 | 1,081 | 120,024 | ~0 | **1,650** |

Scrolling an already-laid-out table turned out to be essentially free
at every N tested — browsers don't re-flow rows just because the
viewport moved over already-computed content. The real, measured cost
is mount and **every filter keystroke**, both scaling linearly with
total row count because React reconciles the *entire* filtered set
into real DOM nodes regardless of what's actually visible. At 20,000
rows that's a 1.65-second freeze per keystroke — a genuine, demonstrated
violation of section 26's responsiveness target, not a hypothetical
one, but the mechanism was mount/reconciliation cost, not scroll —
worth stating plainly since a fix aimed at scroll jank (the
intuitive guess) would have solved the wrong problem.

**Fix:** `frontend/src/hooks/useVirtualRows.ts` — a ~60-line
fixed-row-height virtualization hook, written from scratch rather than
adding a dependency (`frontend/package.json` carries zero runtime deps
beyond `react`/`react-dom` by design; a windowing library would be the
first). Renders only rows within the scrolled viewport (+ 8 rows
overscan) plus two spacer `<tr>` elements to keep scrollbar size/
position correct. Wired into `ProcessesPage.tsx`'s `ListView`, the one
screen spec section 6 explicitly names ("must handle thousands of
processes"); the other four list/detail screens were left alone since
none of them can realistically reach four-digit row counts on a real
machine, and adding the same machinery there would be complexity
without a demonstrated problem to fix — same "don't optimize without a
measured reason" principle, applied in the other direction this time.

**Result, same harness, same methodology:** DOM node count flat at
~122 regardless of N (was up to 120,024); render flat at 28–43ms (was
up to 1,081ms); filter keystroke flat at 4–33ms (was up to 1,650ms).

**Verified, not assumed, after building it:**
- Scrolling to the bottom of a 5,000-row list renders the real last
  rows (PIDs 5997–5999), not a stuck first page.
- Keyboard selection (focus + Enter) on a row scrolled into the middle
  of a 5,000-row list correctly sets `aria-selected="true"` — checked
  by tracking the specific row's PID across the interaction. An
  earlier check that tracked "the first matching row" instead of a
  specific PID produced a false negative here: focusing a row near the
  edge of the virtualized window triggers the browser's native
  focus-scrolls-into-view behavior, which can shift the window enough
  that "first match" resolves to a different row *after* the action
  than *before* it — the selection was correct the whole time, the
  test was checking the wrong node. Recording the false alarm and its
  actual cause rather than quietly fixing the test and moving on,
  since "I checked and it broke, then I checked again and it didn't"
  deserves an explanation, not a silent retry.

**Known limitation, explicit:** Tab only reaches currently-rendered
rows; a keyboard-only user tabbing past the last rendered row moves to
the next focusable element after the table, not further down the
list. Full virtualized-list keyboard traversal needs arrow-key
roving-tabindex navigation, which isn't implemented — the same scope
boundary already noted for `ProcessTree.tsx`'s tree navigation above.
In practice the filter box, not Tab, is how a keyboard user would
narrow a list too large to Tab through in the first place, but this is
a real gap, not a solved one.

### Added — bounded log memory, found and fixed (the other half of section 26)

Went looking for the process-list-style problem in `LogsPage.tsx` next,
since spec section 8 names it explicitly ("Do not load millions of log
entries into the frontend... log streaming should not accumulate
unlimited memory") and it hadn't been checked yet. Found a real,
live bug: `entries` grew **without any cap** while "Follow newest" was
on — every streamed entry got prepended with `[...matching, ...prev]`
and nothing ever trimmed it. A long-running session under real log
volume would grow this indefinitely. The paused-buffer path
(`pendingRef.current`) and the resume-merge path had the identical gap.

Backend-side, this was already handled correctly — `internal/logs`'s
`Engine.Query` clamps to `DefaultCapacity` (5,000) regardless of what's
requested, so the *initial* backlog load was always safe. The bug was
frontend-only: nothing re-applied any limit as new entries streamed in
after that first load.

**Fix:** capped all three growth paths (`follow`, `paused`, `resume`)
at `MAX_DISPLAYED_ENTRIES = 5000`, matching the backend's own
`DefaultCapacity` deliberately — holding more on the frontend than the
backend itself remembers wouldn't represent anything real. Verified
the capping logic directly (not just by reading it): simulated 8,000
sequential single-entry streaming updates against the exact reducer
used in `follow` mode, confirmed the array holds at exactly 5,000,
keeps the newest entries (seq 3001–8000, not an arbitrary subset), and
preserves newest-first order; same check for the `paused` and
`resume` paths.

Once entries could legitimately sit at 5,000, that's large enough to
hit the same DOM-reconciliation cost `ProcessesPage.tsx` had — so the
same `useVirtualRows` hook (already measured and verified working
there) was applied to `LogsPage.tsx` too, generalizing its doc comment
since it now backs both a `<table>`/`<tbody>` and a plain `<div>` list
without any change to the hook itself. Row height for the cap
(`ROW_HEIGHT_PX = 25`) was measured against the real compiled markup
the same way as the process table's, not guessed — a static-file
Playwright check against the real compiled CSS, since guessing wrongly
was exactly the mistake caught earlier in this same phase.

While in there: the log rows were click-to-copy but keyboard-inaccessible
(no `tabIndex`, the same gap already fixed everywhere else this phase) —
fixed alongside, using `role="button"` (not `aria-selected`, since a
log row isn't a member of a selectable set the way a process/service
row is — it's a one-shot action, so `role="button"` plus an
`aria-label` describing what gets copied is the correct fit here,
unlike the table-row case earlier in this changelog where `role="button"`
was deliberately avoided).

**Verified, not assumed:** a standalone harness reusing the actual
`useVirtualRows.ts` file (not a reimplementation) confirmed DOM node
count flat at 77 regardless of whether the list held 200 or 5,000
entries; scrolling to the bottom of a 5,000-entry virtualized list
renders the real oldest entries (seq 1/2/3), not a stuck view; and
keyboard activation (focus + Enter) on a row scrolled to the middle of
the list correctly fires for that exact tracked entry — checked by
following a specific `seq`, not "first match by position," after that
exact distinction turned out to matter for the process-list check
above.

### Added — "unnecessary information / confusing workflows" fix on Overview

Found a real, concrete issue while doing this review, not a manufactured
one: `OverviewPage.tsx` — the one screen a user sees every time they
open FORGE — had a full-width panel showing a raw "Send ping" button
and an unformatted JSON event ticker (`{"sequence":47}`) at the exact
same visual weight as the CPU and Memory panels next to it. Two
concrete problems, not just a vibe:

1. **A naming collision with a real feature.** The local component was
   named `DiagnosticsPanel` — but spec section 23's actual Diagnostics
   feature is a different component entirely
   (`features/settings/DiagnosticsPanel.tsx`, correctly living in
   Settings per that section's own scope note), with a genuinely
   different purpose (a system-wide diagnostic snapshot: disks,
   network, services, health checks, exportable as JSON). Two
   components sharing a name for unrelated purposes is exactly the
   kind of thing that causes a future change to the wrong one.
2. **Real visual clutter on the primary dashboard**, contradicting
   spec section 20 ("avoid... visual clutter," "prioritize information
   hierarchy") and section 21 ("restrained, precise, trustworthy").
   Raw JSON on the one screen every session opens to reads as
   unfinished debug output, not "software written by someone who
   understands computers deeply" (section 35).

The underlying liveness check is genuinely useful and was kept — spec
section 34 doesn't say to delete real value, just to not clutter with
it. Renamed to `ConnectionPanel` (no more collision), and restructured
so the always-visible state is one compact line (a status dot mapped
from the real stream state — `open`/`connecting`/`reconnecting` — plus
the version string), with the ping button and raw event ticker moved
behind an explicit "Connection details" toggle, collapsed by default.
Verified visually, not just by reading the diff: a static reproduction
of both states against the real compiled CSS (screenshot taken, not
just described) confirms the collapsed state is a single unobtrusive
line and the expanded state preserves every bit of the original
diagnostic content, just opt-in instead of permanent.

Scoped narrowly to what was actually found: this was one page, found
by inspection while doing this review — but the pass continued past
it. See below: the systematic sweep over the remaining eleven pages
is now done too, not left open.

### Systematic pass, completed: the other eleven pages

Read all eleven remaining pages in full — Network, Storage, Events,
Health, Settings, Services, the Automation rule form, Containers,
Schedules, plus ProcessDetailPanel — looking for the same category of
issue Overview had: clutter, redundant or out-of-place information,
workflows that don't explain themselves. Result, stated plainly rather
than manufactured to fill out this checklist: **no further issues
found.** Specific things checked and found already right, not just
skimmed:

- `EventsPage.tsx` shows raw JSON payloads per event — this looked at
  first like the same problem as Overview's raw JSON ticker, but it
  isn't: an event-history browser's entire purpose is showing
  structured event detail to someone investigating "what happened,"
  the same way a Kubernetes or CloudTrail event log shows raw payload
  data inline. Overview's problem was raw JSON on a glanceable primary
  dashboard; this is raw JSON in a tool built specifically for reading
  raw event data. Same-looking pattern, opposite verdict, because the
  context is what made Overview's version wrong — noting the
  distinction explicitly so "no raw JSON anywhere" doesn't get
  cargo-culted out of this as a rule it never was.
- `ContainersPage.tsx` and `HealthPage.tsx` both have deliberate,
  already-documented empty-state design decisions (Containers: no
  Docker installed is the common case, not an error, so it explains
  itself instead of showing a broken-looking empty table; Health: an
  empty-state that explains *what a health check is for*, not just
  "no checks yet") — exactly what section 20 asks for ("excellent
  empty states"), already done.
- `AutomationRuleFormDialog.tsx`'s trigger/condition/action model is
  inherently the most complex workflow in the app, and it already
  explains itself inline (a datalist of common event types so users
  aren't guessing exact strings; explicit section hints like "Run in
  order. A failed action doesn't stop the rest from running.") —
  exactly the self-documenting-workflow bar section 20 sets, already
  met.
- `SchedulesPage.tsx`'s choice not to live-subscribe to schedule
  ticks is deliberate and documented (routine, expected, frequent
  churn — not the kind of transition worth a live feed the way a
  health check's pass/fail is) rather than a missing feature.

This isn't a claim that all twelve pages are perfect — it's a
record of what was actually read and actually found, so this item
can close out honestly instead of sitting open indefinitely on a
codebase that, on inspection, already largely got this right.

### Still to do (rest of Phase 7)

This phase is not complete. Explicitly still open, per spec section
31's brief:

- **Cross-page spacing/hierarchy consistency** — audited (heading
  sizes, page-wrapper layout patterns, table cell padding across all
  eight table-bearing pages). Found already consistent: every page
  heading is `text-sm font-semibold`, list/detail pages share the same
  `flex h-full gap-4` wrapper, and 84 of 85 padded table cells use the
  same `px-4 py-2` — the one exception (`LogsPage.tsx`'s `py-1` log
  rows) is a deliberate density choice for a scrolling log stream, not
  a miss. No changes made here since none were needed; noting the
  audit happened rather than leaving this looking unaddressed.
- Loading/error/empty states were **audited, not rewritten** — every
  data page already had reasonable coverage of all three from the
  phase that introduced it (spot-checked directly, not assumed), so no
  changes were needed there this slice. This should be re-verified once
  the spacing/hierarchy pass touches those same components.
- The live event ticker (`useEventStream.ts`, used by Automation/Health
  status views) was already correctly bounded before this phase (its
  own `MAX_BUFFERED_EVENTS = 200` ring buffer, with a comment already
  citing spec section 26) — checked, not something this phase needed
  to fix, noted here so it doesn't read like an oversight.
- **"Unnecessary information" / "confusing workflows" review is now
  complete** (see above) — removed from this list. Phase 7's only
  genuinely open item left is the spacing/hierarchy pass, which was
  already audited and found clean, so in practice Phase 7's substantive
  work is done; what remains is formal sign-off, not undiscovered work.

## [Unreleased] — Phase 6: Containers & Network

Completes spec sections 13, 15, and 23 ("Network Inspection," "Container
Discovery," "Diagnostics"). Section 14 (Storage) was already done in
Phase 2; this is the phase 6 the build plan named "Containers &
Network."

### Added

- **`system.NetworkConnections`** (spec section 13: listening ports,
  active connections, protocol, local/remote endpoint, owning process):
  Linux reads the kernel's own socket tables
  (`/proc/net/{tcp,tcp6,udp,udp6}`) via a from-scratch parser handling
  the kernel's little-endian-per-32-bit-word hex address encoding —
  verified against this sandbox's real `/proc/net/tcp`, not guessed from
  documentation (the fixture literally contains a socket this test
  suite's own setup opened, decoded correctly back to `127.0.0.1`).
  Owning-process attribution cross-references each socket's inode
  against every process's `/proc/*/fd` — best-effort and asymmetric by
  design (FORGE can attribute its own and same-user sockets; another
  user's socket is left unattributed, not an error, the identical
  permission boundary `ps`/`lsof` hit unprivileged). Darwin/Windows:
  `ErrUnavailable`, matching `CPUUsage`/`NetworkIOCounters`'s established
  precedent. Explicitly socket-table inspection, not packet capture —
  spec section 13's own "do not turn this into a packet-sniffing
  application."
- **`internal/containers`** — container discovery and control (spec
  section 15), talking to Docker exclusively through the `docker` CLI
  (shelling out, parsing `--format '{{json .}}'` output) rather than the
  Docker Engine API or an SDK — the same "shell out to a well-documented
  CLI, parse its output" pattern `internal/system` already uses for
  `systemctl`/`launchctl`/PowerShell, chosen for the identical reason:
  zero new third-party dependencies. Every parser (`docker ps`, `docker
  stats`, including the `MemUsage`/`CPUPerc` unit parsing) is tested
  against fixtures captured from a real, running Docker 29.1.3 daemon in
  this sandbox — not hand-written — plus a real integration test that
  talks to that live daemon directly when one is available and skips
  cleanly otherwise (see DEVELOPMENT.md). Runtime availability is
  checked explicitly and surfaced as its own thing, not inferred from an
  error: spec section 15's "the application must remain useful on
  systems without containers" means a machine with no Docker installed
  is normal operation, not a degraded state.
- **`internal/diagnostics`** — the diagnostics area (spec section 23):
  an on-demand snapshot composed entirely from subsystems that already
  exist (system info, disk usage, network interfaces/listening ports,
  non-running services, failing health checks, recent warning/critical
  events), not a new engine with its own state or background work. Every
  section degrades independently — one section's data source being
  unavailable never prevents the rest of the report from generating,
  the identical "gracefully degrade" principle spec section 4 states for
  system information generally, applied here to the feature whose whole
  job is being trustworthy when something else is already wrong. Follows
  the same consumer-defined-interface pattern as `internal/health`/
  `internal/metrics`/`internal/scheduler`/`internal/automation` (a
  narrowed `sysCapabilities` and a new `eventReader` interface — the
  latter fixing what would otherwise have been this phase's one
  inconsistency: depending on the concrete `*storage.EventRepository`
  type directly, caught and corrected before it shipped, not after), so
  it's fully unit-tested with in-memory fakes.
- **FWP methods**: `system.network_connections`, `containers.availability`,
  `containers.list`, `containers.logs`, `containers.start`,
  `containers.stop`, `containers.restart`, `diagnostics.run`.
- **Frontend**: two new pages — **Network** (interface inventory +
  filterable connection/listening-port table) and **Containers**
  (availability-aware list, detail panel with logs and start/stop/
  restart) — plus a **Diagnostics** panel added to Settings (not its own
  top-level nav item, since spec section 20's suggested navigation list
  doesn't include one).
- **`internal/app/app_test.go`**: extended with `diagnostics.run` and
  `containers.availability`/`containers.list` subtests, the latter
  deliberately written to assert correctly whichever way Docker
  availability happens to go in whatever environment runs the suite
  (confirmed against both branches for real in this sandbox — see
  DEVELOPMENT.md).

### Known limitations

- **Docker only** — spec section 15 says "Docker or another supported
  container runtime"; Podman's Docker-CLI-compatible shim (where present)
  already works unmodified since `internal/containers` just shells out to
  a binary named `docker`, but genuine multi-runtime detection (preferring
  a native `podman` binary, for instance) is future work.
- **No gateway/DNS configuration** — spec section 13 lists these among
  "network configuration"; deferred in favor of fully verifying listening
  ports/connections (the more operationally valuable, more rigorously
  testable piece) rather than partially covering four different network
  facts. `/etc/resolv.conf`/route-table parsing would be a natural,
  contained follow-up.
- **No SMART/hardware disk health** — spec section 23's "disk health
  indicators where accessible" is scoped to disk *usage* (already
  covered since Phase 2), not real hardware health telemetry, which is
  platform-specific, typically privilege-requiring, and a poor fit for a
  diagnostics feature whose main job is being trustworthy.
- **`restart`/`stop` container actions are unconfirmed**, matching every
  other direct action in FORGE (`process.terminate`,
  `service.start`/`stop`/`restart`) — the confirmation point is the
  user's own click in the UI, not a second server-side gate; see
  SECURITY.md.
- No new third-party Go dependencies. No new frontend dependencies.

## [Unreleased] — Phase 5: Automation

Completes spec sections 11–12 ("Automation Engine," "Scheduler"), plus
the Automation/Schedules frontend pages spec section 20 named for this
phase.

### Added

- **`internal/scheduler`** — the task scheduler (spec section 12):
  one-time (`once`) and recurring-interval (`interval`) tasks, persisted
  across restarts, with a documented, deterministic missed-execution
  policy — a task overdue because FORGE wasn't running fires exactly
  once on startup (not once per missed interval), and every `NextRunAt`
  advance is persisted *before* the corresponding `scheduler.task_due`
  event is published, so a crash between the two can at worst cause one
  missed firing, never a duplicate one. Deliberately narrow in scope:
  this package knows nothing about automation rules or any other FORGE
  subsystem — it tracks `Task`s and publishes `scheduler.task_due`, full
  stop. One shared scan loop (1s resolution) rather than a goroutine per
  task, matching spec section 12's "lightweight scheduler" framing —
  internal/health and internal/metrics each already have their own
  purpose-built interval scheduling that a shared generic scheduler
  would only complicate.
- **`internal/automation`** — the rule engine (spec section 11):
  Trigger → Conditions → Actions. Two trigger kinds — `event` (matches
  any bus event `Type`, which is how health-check results and
  thresholds reach a rule too, since they already arrive as bus events)
  and `schedule` (matches `scheduler.task_due` for a specific task,
  delegating all timing to `internal/scheduler`). Conditions are flat
  `field = value` equality checks against the triggering event's JSON
  payload, AND-ed together, deliberately not a richer expression
  language (spec section 11 doesn't ask for one, and every event
  payload in this codebase is a flat struct with nothing to nest).
  Seven action types: `restart_process`, `stop_process`,
  `start_service`, `restart_service`, `run_command`, `create_event`,
  `notify` — `run_command` execs argv directly, never a shell, the same
  discipline `internal/health`'s `command` check type uses (see
  SECURITY.md's "Automation actions"). Actions in a rule run in order,
  sequentially, and a failed action does not abort the remaining ones
  (so e.g. a `create_event` action after a failed `restart_process`
  still runs, reporting the failure). Execution is bounded to 8
  concurrent rule-runs regardless of how many trigger at once (the same
  semaphore pattern `internal/health` uses), and a per-rule
  `CooldownSeconds` guards against a rapidly repeating trigger (a
  flapping health check) turning one rule into an action storm.
- **Self-triggering loop prevention** (spec section 11's explicit
  warning against "an obvious security disaster"): every event this
  package publishes uses the `automation.` prefix, and no `Trigger` may
  target that prefix — enforced both at validation time (`Rule.Create`/
  `Update` reject it) and again, defensively, at dispatch time, so a
  rule's own output (or another rule's) can never trigger any rule,
  including itself, directly or through a chain.
- **FWP methods**: `scheduler.list`, `scheduler.get`,
  `scheduler.create`, `scheduler.update`, `scheduler.delete`,
  `scheduler.set_enabled`, `automation.list`, `automation.get`,
  `automation.create`, `automation.update`, `automation.delete`.
- **Storage**: `SchedulerRepository` and `AutomationRepository`
  (migrations `0004_scheduler_tasks.sql`, `0005_automation_rules.sql`),
  following the same consumer-defined-`Repository`-interface pattern
  Phase 4 established — `internal/scheduler` and `internal/automation`
  have no `database/sql` dependency at all, which is what let both be
  fully unit-tested (37 new tests) with in-memory fakes despite this
  sandbox's continued inability to reach `modernc.org/sqlite`.
- **Frontend**: two new pages — **Schedules** (list, create/edit,
  pause/resume, delete) and **Automation** (list, a create/edit form
  covering trigger/conditions/ordered-actions/cooldown, a detail panel
  with run history, live-updating via `automation.rule_executed`).
- **`internal/app`**: `Run`'s subsystem start order now matters in a way
  it didn't before, and is documented as such —
  `automation.Engine.Start` (which subscribes to the bus synchronously
  before returning) runs *first*, before `scheduler.Engine.Start` or
  `health.Engine.Start`, either of which can publish an event from a
  background goroutine immediately upon starting. This is the same
  ordering-guarantee fix Phase 4 applied to the event-persistence
  bridge, generalized and applied proactively here rather than
  discovered by a failing test after the fact — though a new end-to-end
  subtest in `app_test.go` ("scheduled automation rule fires end to
  end") exercises the full pipeline for real anyway: a task due almost
  immediately, wired to a rule via `TriggerSchedule`, whose `notify`
  action's event is confirmed to reach `events.recent` over the real
  wire protocol.

### Fixed

Found by this phase's own tests, not carried over from an earlier
phase:

- `automation.Engine.Create` never set a new rule's `Enabled` field,
  so every rule created through the engine was silently inert —
  `ruleMatchesEvent` correctly refuses to match a disabled rule, but
  nothing ever set the field true. The very first engine test written
  against real trigger-matching caught this immediately (a rule that
  provably could not fire); fixed by having `Create` set `Enabled =
  true` explicitly, matching `scheduler.Engine.Create`'s identical
  default.
- Two data races in this phase's own test helpers (not production
  code): `internal/automation`'s test suite read a shared `fakeSys`'s
  slice fields directly from the test goroutine while Engine wrote them
  concurrently from its own action-execution goroutines. Caught by
  `go test -race` on the first run; fixed with locked snapshot getters.

### Known limitations

- **`restart_process` is a fixed heuristic, not real process
  supervision.** It terminates matching processes gracefully, waits a
  fixed 2-second grace period, then launches the replacement — it does
  not confirm the old process actually exited, does not retry a failed
  launch, and does not supervise the replacement afterward (no captured
  output, no restart-on-crash of *that* process). Combining this action
  with a `process_exists` health check against the same name gives an
  operator the pieces to build further supervision at the rule level,
  but the action itself does not do that.
- **`notify` has no OS-level desktop notification yet.** Both `notify`
  and `create_event` currently do the same thing — publish a bus event
  (`automation.notification` vs. `automation.custom_event`
  respectively) that reaches the Events screen — kept as distinct event
  types specifically so a future frontend can treat them differently
  (e.g. a transient toast for `notify`) without a backend change.
- **No wall-clock-recurring scheduling** ("every day at 9am local time,
  correctly through DST transitions") — `internal/scheduler`'s
  `interval` type is a fixed number of seconds from the last run, full
  stop. `RunAt` for a `once` task is accepted as RFC3339 with an
  explicit offset, which is what spec section 12's "proper timezone
  handling" is scoped to mean here: never silently losing or
  misinterpreting an offset, not full calendar-aware recurrence rules.
- **Conditions are flat top-level equality only** — no nesting, no
  operators besides equality, no OR (only implicit AND across multiple
  conditions). See `internal/automation/conditions.go`'s doc comment.
- No new third-party Go dependencies. No new frontend dependencies.

## [Unreleased] — Phase 4: Observability

Completes spec sections 8–10 and 17 ("Log Engine," "Health Check
Engine," "Event Bus" extensions, "Metrics Engine"), plus the Events/
Health/Logs frontend pages spec section 20 named for this phase.

### Added

- **`internal/health`** — the health check engine (spec section 9): 8
  check types (`process_exists`, `service_state`, `tcp_port`,
  `http_endpoint`, `command`, `disk_threshold`, `memory_threshold`,
  `cpu_threshold`), a `UNKNOWN`/`HEALTHY`/`DEGRADED`/`UNHEALTHY` state
  machine, per-check retry policy, and a scheduler bounded to 8
  concurrently executing checks regardless of how many are configured
  (a counting semaphore) — see spec section 9's "must run concurrently
  but must have bounded resource usage." `command` checks run via
  `exec.CommandContext` directly (argv, never a shell) — see
  SECURITY.md's new "Health checks" section. Publishes
  `health.check_passed`/`health.check_failed` on the bus **only on
  state transitions**, not every evaluation, plus
  `health.disk_threshold_exceeded`/`health.memory_threshold_exceeded`
  for those two check types specifically — satisfying spec section 10's
  event vocabulary without flooding the bus. `Repository` is a
  consumer-defined interface (implemented by
  `storage.HealthRepository`), so the engine itself has no
  `database/sql` dependency and is fully unit-testable with an in-memory
  fake.
- **`internal/metrics`** — the metrics engine (spec section 17):
  independent collection loops for CPU+memory (5s), disk (30s), and
  network throughput (5s, computed from two time-separated cumulative
  counter readings — see `system.NetIOCounters`'s doc comment), each
  series held in a bounded in-memory rolling window (~30 min) for
  `metrics.current`/`metrics.rolling`, periodically (60s) aggregated to
  min/avg/max buckets and flushed to SQLite as one batched insert — spec
  section 17's explicit "do not write every metric sample directly to
  SQLite independently." Same consumer-defined-`Repository` pattern as
  `internal/health`.
- **`internal/logs`** — the log engine (spec section 8), scoped
  deliberately to FORGE Core's own structured log output (not the OS
  system log or arbitrary services'/processes' logs — see its package
  doc comment for why that's a phase boundary, not an oversight): a
  bounded (5000-entry) in-memory ring buffer, fed via a new
  `logging.Options.Tap` hook so it observes the original
  `slog.Record` directly rather than re-parsing the rotated log file;
  query/filter (level, case-insensitive substring search)/pagination
  (`Before` cursor); live entries published on the bus as `log.entry`.
- **`system.NetworkIOCounters`** — cumulative per-interface byte
  counters, the raw material `internal/metrics`' network collector
  diffs into a rate. Linux: real, verified against `/proc/net/dev`
  (reusing the same parser/fixture-tested code Phase 2's
  `NetworkInterfaces` already used). Darwin/Windows: `ErrUnavailable`,
  for the same reasons `CPUUsage` is on those platforms — see the doc
  comments in `provider_darwin.go`/`provider_windows.go`.
- **Storage**: `HealthRepository` and `MetricsRepository` (migrations
  `0002_health_checks.sql`, `0003_metric_aggregates.sql`), and
  `Config.MetricsRetentionDays` (already present since Phase 1's config
  scaffold, unused until now) now actually drives an hourly prune
  alongside the existing events retention sweep.
- **FWP methods**: `health.list`, `health.get`, `health.create`,
  `health.update`, `health.delete`, `health.run_now`, `metrics.current`,
  `metrics.rolling`, `metrics.history`, `logs.query`, and
  `events.recent` (browsing the event history Phase 1's
  event-persistence bridge has been recording all along, with no read
  API until now).
- **Frontend**: three new pages — **Health** (list + create/edit form +
  detail panel with Run Now/Delete, live-updating via the transition
  events above), **Logs** (level/search filters, pause/resume, follow
  newest, click-to-copy, export to a downloaded `.txt`), **Events**
  (persisted history + live tail, filterable by type/source). A new
  `src/vite-env.d.ts` (see "Known limitations").
- **`internal/app/app_test.go`** — a new full-stack end-to-end test:
  builds a real `App`, runs it, and drives it over actual HTTP/FWP the
  way the Tauri shell would, exercising the health check lifecycle,
  live metrics, log capture, and event persistence together. See
  DEVELOPMENT.md's "Toolchain status as of Phase 4" for how this — and
  every other `internal/storage`/`internal/app` test — actually got run
  in a sandbox that can't reach `modernc.org/sqlite`.

### Fixed

Both found by the end-to-end test above, both pre-existing since Phase
1, neither specific to this phase's new code:

- `storage.Open` pooled up to 8 SQLite connections, but `PRAGMA
  busy_timeout` only reached whichever single connection happened to
  execute that statement; any connection the pool opened later under
  concurrent load had no busy timeout and failed instantly with
  "database is locked" instead of waiting. Fixed by using exactly one
  connection (`SetMaxOpenConns(1)`), which makes the failure mode
  structurally impossible rather than merely less likely.
- `internal/app`'s event-persistence subscriber subscribed to the bus
  from inside its own goroutine, which could lose a race against `Run`
  publishing `TypeBackendStarted` a few lines later — silently dropping
  that event, every session, with no error anywhere. Fixed by
  subscribing synchronously on `Run`'s own goroutine before publishing
  anything.
- A missing `frontend/src/vite-env.d.ts` meant `import.meta.env` never
  type-checked — invisible until this phase's `npm install`/`tsc` was
  the first time this project had a working TypeScript toolchain to
  catch it.
- A hardcoded `schema_migrations` row-count assertion in
  `migrate_test.go` (accurate for Phase 1's one migration, stale the
  moment Phase 4 added two more) now counts the embedded migration files
  instead.

### Known limitations

- **Network throughput metrics are Linux-only**, matching `CPUUsage`'s
  existing platform split — Darwin/Windows report no network series
  rather than a guess. See `system.NetIOCounters`'s doc comment.
- **The log engine covers only FORGE Core's own log**, not OS system
  logs, service logs, or arbitrary processes' output — each needs a
  genuinely different, platform-specific reader and is left for a later
  pass (mirrors the same incremental-scope decision already made for
  container discovery in Phase 6).
- **No automatic, system-wide process/service start/stop/crash
  detection** — spec section 10 lists `ProcessStarted`/
  `ProcessStopped`/`ProcessCrashed`/`ServiceStarted`/`ServiceStopped` as
  event types, but watching *every* process/service on the machine
  system-wide would be extremely noisy by default. What exists instead:
  targeted, user-configured `process_exists`/`service_state` health
  checks, which cover the same operational need ("tell me when this
  specific thing stops running") without the noise. True system-wide
  watching is deferred to when Phase 5's automation engine needs it as a
  trigger source.
- **Health check results are not individually historized** — only the
  single latest `Status` per check is persisted (`UpdateResult`
  overwrites, doesn't append). Every state *transition* is still durably
  recorded, just via the existing event-persistence bridge rather than a
  second, parallel history table — see `health.Repository`'s doc
  comment for the reasoning.
- No new third-party Go dependencies. No new frontend dependencies
  beyond what Phase 1 already had.

## [Unreleased] — Phase 3b: Service Management

Completes spec section 7 ("Process & Service Management"), following
Phase 3's process-management half.

### Added

- **`system.ServiceInfo`/`ServiceDetail`** and **`system.ServiceProvider`**
  (`ListServices`, `ServiceDetail`, `StartService`, `StopService`,
  `RestartService`, `SetServiceEnabled`) — see `ServiceInfo`'s doc comment
  on why this is a deliberate projection of three different platform
  service models, not a claim they're equivalent.
- **Linux**: via `systemctl`. Listing merges `list-units` (running state)
  and `list-unit-files` (enabled/disabled) rather than querying per-unit.
  Detail uses `systemctl show`'s scripting-oriented `KEY=VALUE` output,
  including `Requires` for dependencies. The fullest of the three
  implementations — but see "Known limitations" below for an important
  caveat on how verified this one actually is.
- **Windows**: via `Get-CimInstance Win32_Service` (reusing Phase 2's
  CIM-CSV parsing pipeline) plus `Start-Service`/`Stop-Service`/
  `Restart-Service`/`Set-Service`. Service names are escaped
  (`escapePowerShellSingleQuoted`) before being interpolated into a
  PowerShell script.
- **Darwin**: via `launchctl list` for listing/detail (deliberately thin
  — no description, no dependencies, `StartupMode` always `"unknown"`)
  and `launchctl start`/`stop` for actions. `SetServiceEnabled` returns
  `UNAVAILABLE` — see ARCHITECTURE.md's "Service management" for why
  launchd's addressing scheme made this the honest choice over a guess.
- **FWP methods**: `service.list`, `service.detail`, `service.start`,
  `service.stop`, `service.restart`, `service.set_enabled`. All four
  mutating methods publish a `TypeServiceActionRequested` event on every
  attempt, success or failure — the same audit-trail principle
  `process.terminate` already followed.
- **Frontend**: a new **Services** page (list + search) and
  `ServiceDetailPanel` (state, startup mode, dependencies, and
  Start/Stop/Restart/toggle-autostart actions). Stop and Restart route
  through `ConfirmDialog`; Start and the autostart toggle don't, since
  neither immediately disrupts something already running.

### Known limitations

- **Linux service management could not be verified against a live
  system**, unlike the rest of this codebase's Linux support. This
  sandbox's `systemctl` binary is present but non-functional — the
  container isn't booted with systemd as PID 1 — so
  `linux_service_parse_test.go`'s fixtures follow systemd's documented
  output formats rather than captured real output. This is a genuine
  step down in confidence from Phase 2/3a's `/proc`-based Linux code and
  is called out explicitly in ARCHITECTURE.md and DEVELOPMENT.md.
- Darwin's service management remains the thinnest of the three
  platforms — no description, no dependencies, no reliable startup-mode,
  and `SetServiceEnabled` unavailable. This reflects genuine ambiguity in
  `launchctl`'s addressing scheme, not an oversight.
- Windows and Linux dependency listings differ: Windows' `Win32_Service`
  has no simple property-select path to a dependency list (would need a
  separate WMI associator query) and is left empty, matching Darwin.
- FORGE never attempts to acquire or escalate OS privileges for a service
  action — see SECURITY.md's new "Service management" section. A
  permission-denied failure is surfaced as-is, not retried.
- No new third-party Go dependencies.

## [Unreleased] — Phase 3: Process Management

Scoped deliberately at the time: this covers the *process* half of spec
section 7's "Process & Service Management" phase, with service management
(systemd/launchd/Windows SCM) held for a separate follow-up rather than
compounded into the same pass — see the "Phase 3b: Service Management"
entry above, completed immediately after this one.

### Added

- **`system.ProcessProvider` extended**: `ProcessDetail` (executable,
  command line, live CPU%, plus everything `ProcessInfo` already had) and
  `TerminateProcess` (graceful/force), implemented on all three platforms.
- **`system.BuildProcessTree`** — pure, OS-independent parent/child tree
  construction from a flat process list. No build tag: it's a property of
  the data, not the platform. Full unit test coverage including multiple
  roots, a defensive self-parent guard, and a determinism check.
- **Linux**: `ProcessDetail` reads `/proc/[pid]/cmdline` and
  `/proc/[pid]/exe`, and samples CPU% via the same two-reading
  methodology as the aggregate `CPUUsage`. `TerminateProcess` uses
  `os.FindProcess` + `Signal(SIGTERM/SIGKILL)` — verified end-to-end in
  this environment, including a direct shell-level confirmation that
  `SIGTERM` actually terminates a real process here, ahead of writing the
  Go integration tests that exercise the same path (spawn a real
  disposable `sleep` child, terminate it, confirm it exits).
- **Darwin**: `ProcessDetail` via `ps`'s own `%cpu` column (no manual
  sampling needed — macOS's `ps` computes it for us) plus a second `ps`
  call for the full command line and start time. `TerminateProcess` is
  identical to Linux's — `os.Process.Signal` is standard-library,
  cross-Unix functionality with no macOS-specific syscall risk.
- **Windows**: `ProcessDetail` via a `Get-CimInstance Win32_Process`
  query (`ExecutablePath`, `CommandLine`, `CreationDate` — the last
  requiring a WMI datetime parser, `parseWMIDateTime`, tested against the
  documented format). `TerminateProcess` uses `os.Process.Kill()`
  (`force=true` only — Windows has no reliable general-purpose graceful
  signal; `force=false` returns `UNAVAILABLE` rather than silently
  behaving like force).
- **FWP methods**: `process.detail`, `process.tree`, `process.terminate`.
  `process.terminate` publishes a new `TypeProcessTerminationRequested`
  event on every attempt, success or failure, giving every termination an
  audit trail in FORGE's event history regardless of outcome.
- **Frontend**: clicking a process row opens `ProcessDetailPanel` (full
  detail, live CPU%, Terminate/Force kill actions); a List/Tree toggle
  renders `ProcessTree`, a recursive, collapsible tree view. Both
  termination actions route through `ConfirmDialog` — no path to
  `process.terminate` skips confirmation.

### Known limitations

- Process CPU% is unavailable in `ProcessDetail` on Windows, matching the
  aggregate `CPUUsage` gap from Phase 2, for the same reason (no
  sufficiently verifiable data source without a real machine to test
  against).
- The process list view remains a plain (non-virtualized) table; spec
  section 6's "thousands of processes" requirement is deferred until a
  real large-process-count scenario shows it's actually needed —
  premature virtualization would be optimizing before measuring.
- Darwin's `CommandLine` is a naive whitespace split of `ps`'s `command`
  column and will mis-parse an argument containing a literal space;
  Windows's `CommandLine` avoids this by keeping the OS's own single
  command-line string intact instead of guessing a tokenization.
- No new third-party Go dependencies.

## [Unreleased] — Phase 2: System Core

### Added

- **`internal/system`** — the OS abstraction layer: `SystemInfoProvider`,
  `CPUProvider`, `MemoryProvider`, `StorageProvider`, `NetworkProvider`,
  `ProcessProvider` interfaces composed into one `Provider`, selected per
  platform at compile time via Go build constraints. `system.ErrUnavailable`
  is the shared sentinel for a capability that's real but not available on
  the current platform.
- **Linux provider** — direct `/proc` parsing (`/proc/stat`,
  `/proc/meminfo`, `/proc/cpuinfo`, `/proc/uptime`, `/proc/version`,
  `/etc/os-release`, `/proc/[pid]/stat`, `/proc/[pid]/status`,
  `/proc/net/dev`, `/proc/mounts`) plus `syscall.Statfs` for real disk
  usage. Pure parsing functions are split from file I/O specifically so
  they're unit-testable with fixture data — captured verbatim from a real
  machine while writing this, not hand-guessed — and a separate
  integration test suite exercises the real implementation against the
  real running OS.
- **Darwin provider** — `os/exec` over `sysctl`, `vm_stat`, `df`, `ps`
  with fixed argument lists, chosen deliberately over raw Mach/syscall
  bindings this environment couldn't compile-check (see ARCHITECTURE.md
  "Darwin and Windows"). `CPUUsage` returns `ErrUnavailable` — no
  sufficiently stable, verifiable output format was available for
  aggregate CPU utilization.
- **Windows provider** — PowerShell `Get-CimInstance` CIM queries for OS/
  CPU identity and process listing, plus three narrowly-scoped
  `kernel32.dll` syscalls for uptime, memory, and disk space (see
  `provider_windows.go`'s package comment for the specific risk reasoning
  behind each one). `CPUUsage` returns `ErrUnavailable` for the same
  reason as Darwin.
- **FWP methods**: `system.info`, `system.cpu_info`, `system.cpu_usage`,
  `system.memory`, `system.disks`, `system.network_interfaces`,
  `process.list` — registered in `internal/app/methods.go`, which also
  translates `system.ErrUnavailable` into the wire-level
  `fwp.ErrUnavailable` error code.
- **Frontend**: Overview now shows real system/CPU/memory data instead of
  only the Phase 1 IPC demo (which remains, lower on the page). New
  **Storage** and **Processes** pages (disk usage table; process list
  with client-side search). Both consume the new FWP methods through the
  same `FwpClient` built in Phase 1.

### Known limitations

- Process listing is discovery only — no detail view, no process tree, no
  termination (Phase 3).
- Disk and network listings are inventory only — no throughput, no
  listening ports or active connections (Phase 4's metrics engine and
  Phase 6 respectively).
- `CPUUsage` is Linux-only for now; Darwin and Windows report
  `UNAVAILABLE` until a sufficiently verifiable implementation exists.
- **The Darwin and Windows providers have not been run on a real Mac or
  Windows machine.** This development environment had only Linux
  available. Their fixture-based tests are written from documented
  command output formats, not captured samples — see README.md's "What
  actually works right now" and ARCHITECTURE.md's "OS abstraction" for
  the full disclosure.
- No new third-party Go dependencies were needed for Phase 2 — the
  `os/exec`-based approach on Darwin/Windows and direct `/proc` reads on
  Linux stayed within the standard library.

## [Unreleased] — Phase 1: Foundation

### Added

- Repository structure: `backend` (Go), `apps/desktop` (Rust/Tauri shell),
  `frontend` (React/TypeScript), plus `docs`, `scripts`, `tests`.
- **FWP (FORGE Wire Protocol)** — `pkg/fwp`: the `Envelope` wire format
  (request/response/error/event/cancel), structured error codes. See
  ARCHITECTURE.md.
- **IPC transport** — `internal/ipc`: FWP carried over local HTTP
  (`POST /rpc`, `GET /events` as NDJSON streaming, `POST /shutdown`),
  loopback-only binding, bearer-token auth with constant-time comparison,
  a method router with panic-on-duplicate-registration safety.
- **Event bus** — `internal/eventbus`: bounded, non-blocking, in-process
  pub/sub with context-scoped subscriptions and a leak-free watcher
  goroutine, verified under `-race`.
- **Configuration** — `internal/config`: JSON config with OS-appropriate
  paths (`os.UserConfigDir`), default-merge-on-load, atomic-enough
  save-then-load round-tripping.
- **Logging** — `internal/logging`: `log/slog`-based structured logging
  with a dependency-free size-based rotating file writer.
- **Persistence** — `internal/storage`: SQLite via `modernc.org/sqlite`
  (CGO-free), an embedded/hand-rolled migration runner, an event
  repository with retention pruning. Initial schema: `events`.
- **Application core** — `internal/app`: the composition root; startup
  wiring, coordinated goroutine lifecycle, graceful shutdown triggered
  either by OS signal or by the desktop shell's `POST /shutdown`, a
  heartbeat demo worker, an event-persistence bridge from the bus to
  SQLite, an hourly retention pruner.
- **`system.ping` / `system.version`** — the only two FWP methods this
  build exposes, used to prove the protocol end to end.
- **Desktop shell** — `apps/desktop`: spawns `forge-core`, reads its
  startup handshake, exposes connection details to the frontend via one
  Tauri command, performs graceful shutdown on exit via a hand-written
  HTTP request (no HTTP client dependency).
- **Frontend** — React 19 + TypeScript (strict) + Tailwind, Vite build.
  App shell (sidebar, top bar, connection status), a keyboard-first
  command palette (Cmd/Ctrl+K), dark/light theming via CSS custom
  properties, a shared `ConfirmDialog` primitive for future destructive
  actions, an `FwpClient` IPC library (request/response with
  cancellation, NDJSON event streaming with reconnect/backoff), and two
  live pages: **Overview** (version info, a manual ping round-trip,
  a live heartbeat event ticker) and **Settings** (theme toggle). Every
  other nav item shows an honest "arrives in Phase N" placeholder.
- Documentation: this file, README, ARCHITECTURE, DEVELOPMENT, SECURITY,
  CONTRIBUTING, `tests/README.md`.

### Known limitations

- No system monitoring, process/service/container/network inspection,
  metrics, health checks, automation, or scheduling — all later phases.
- No OS-specific code anywhere yet; `internal/system` doesn't exist until
  Phase 2.
- `backend/go.sum` is not generated (no network access during
  authorship — see DEVELOPMENT.md); run `go mod tidy` before first build.
- Rust/Tauri code has not been compiled against a real `tauri` crate; the
  API usage should be treated as "written to the documented v2 pattern,
  not yet verified" — see DEVELOPMENT.md.
- No CI configuration yet.
