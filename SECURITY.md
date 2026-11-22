# Security

FORGE is local-first system operations software. It will eventually be
capable of terminating processes, stopping services, and executing
automation actions on the machine it runs on — and, in the future, on
remote machines through the planned Agent architecture (see
ARCHITECTURE.md). It's designed with that responsibility in mind from
Phase 1, even though most of what it could do damage with doesn't exist
yet.

## Reporting a vulnerability

Open an issue describing the problem. For anything you believe is
actively exploitable rather than a hardening gap, say so explicitly in the
report so it can be triaged accordingly. This project does not yet have a
dedicated security contact or disclosure program; that's expected to
change before a 1.0 release.

## Current security model (as of Phase 8)

### Local IPC

- FORGE Core listens **only** on a loopback address
  (`internal/ipc.NewServer` refuses to bind anything else, by code, not
  just by default configuration).
- The listening port is OS-assigned and ephemeral by default — there is
  no fixed, predictable port for another local process to guess and probe.
- Every request must present a bearer token that is generated fresh on
  every process start, held only in memory, and never written to disk.
  Comparison uses `crypto/subtle.ConstantTimeCompare` rather than `==`, to
  avoid a timing side-channel on the comparison itself.
- The token is handed to the desktop shell exactly once, via one line of
  JSON on `forge-core`'s stdout at startup, and from the shell to the
  frontend via a single Tauri command. It is not logged, and
  `internal/logging` writes application logs, not request bodies, so it
  cannot end up in a log file that way either.
- `handleRPC` runs every request with `r.Context()` and nothing else —
  no server-side per-request timeout, deliberately, documented as
  "Phase 1's cancellation mechanism" from the start: a long-running
  method that respects `ctx` stops when the client disconnects, and
  that's the only bound. Investigated properly (not just noted) while
  auditing `internal/containers`: a handler that shells out to
  something that genuinely hangs (an unresponsive `docker` daemon; a
  stuck service-control call) could in principle block its handler
  goroutine indefinitely absent that disconnect. In practice this is
  narrower than it sounds for a same-machine, single-user tool: every
  frontend data-fetching `useEffect` in this codebase pairs its
  `client.request` call with an `AbortController` that aborts on
  unmount or on re-fetch — verified by auditing every call site, which
  is what caught the one file (`ServiceDetailPanel.tsx`) that didn't
  follow this and fixed it — so a hung background fetch is cleaned up
  the moment the user navigates away or the component re-fetches, not
  left to accumulate. What this doesn't cover: a direct, user-triggered
  action (run now, delete, start/stop/restart) is meant to run to
  completion once clicked, by design, so a hang there shows as a stuck
  "busy" spinner until it resolves, times out at the automation-action
  layer if the call happens to route through one (see "Automation
  actions" below), or the user restarts FORGE — not elegant, but bounded
  to the one action, not a shared resource. A genuine gap for the
  planned remote Agent architecture, where "the client" won't always be
  this project's own disciplined frontend: worth a real per-request
  server-side timeout before that lands, not before.

This does **not** protect against another process running as the *same
OS user* that can read `forge-core`'s stdout as it starts, or attach a
debugger — that's not a threat model loopback+token auth can address, and
FORGE doesn't claim to. What it does address: another local process (or a
malicious web page, if such a thing could somehow reach a loopback port)
cannot drive FORGE Core's API without first observing the token through a
channel that requires meaningfully more access than "can open a TCP
socket."

### What FORGE never logs

`internal/logging` and every call site that logs an error are expected to
never include: passwords, API tokens, private keys, authentication
cookies, or any other secret. The one credential FORGE Core itself
generates — the IPC bearer token — is deliberately never passed to the
logger; grep for `s.token` in `internal/ipc` and you will not find it
anywhere near a `logger.*` call. If you find a place where this
principle is violated, that's a bug worth reporting even if it isn't
independently exploitable.

### Process handling

Implemented in Phase 3. `system.ProcessProvider.TerminateProcess` uses a
direct syscall on every platform (`os.Process.Signal`/`.Kill()`) — never
`os/exec` invoking `kill`/`taskkill`, and never a shell string built by
concatenation. Destructive actions require explicit user confirmation in
the UI (see `frontend/src/components/ui/ConfirmDialog.tsx`, built in
Phase 1 specifically so every subsequent feature needing it already had
correct, tested UI to route through) — every path to
`process.terminate` in the frontend goes through it, and the backend
additionally records every termination attempt, success or failure, as
an event (`TypeProcessTerminationRequested`) for observability. The spec
explicitly rules out an unrestricted arbitrary-command automation engine;
that principle will continue to apply as Phase 5's automation engine is
built.

### Service management

Implemented in Phase 3, with a real command-injection bug found and
fixed in Phase 8 — see CHANGELOG.md's Phase 8 entry for the full
account; summarized here because it's exactly the kind of thing this
document exists to be honest about.

`system.ServiceProvider`'s mutating operations (`StartService`,
`StopService`, `RestartService`, `SetServiceEnabled`) use `os/exec`
with fixed argument lists against each platform's own service-management
tool (`systemctl` on Linux, `launchctl` on macOS, PowerShell's
`Start-Service`/`Stop-Service`/`Restart-Service`/`Set-Service` cmdlets on
Windows) — the same discipline as process handling: no shell string
concatenation, and every service name is treated as untrusted data. On
Linux and macOS this is structurally enforced: `exec.CommandContext(ctx,
name, args...)` passes the service name as one argv element with no
shell involved at any point, so shell metacharacters in a service name
are inert regardless of escaping.

On Windows, PowerShell itself *is* effectively a shell, and there is no
argv-only escape from that — every PowerShell interaction here is
necessarily a script string. `StartService`/`StopService`/
`RestartService`/`SetServiceEnabled` embed the service name inside a
PowerShell *single-quoted* string (`Start-Service -Name '%s'`), escaped
with `escapePowerShellSingleQuoted` (doubles embedded `'`) — correct,
because PowerShell single-quoted strings perform no interpolation of
any kind; the only character that needs escaping is the quote itself.

**The bug, found in Phase 8's security review, not by an external
report:** `ServiceDetail` used the identical `escapePowerShellSingleQuoted`
function, but embedded the result inside a PowerShell *double-quoted*
string instead (`-Filter "Name='%s'"` — double-quoted because the
double quotes were WQL filter syntax, not a considered choice about
PowerShell string type). PowerShell double-quoted strings *do*
interpolate `$(...)` subexpressions during parsing, regardless of what
single-quote escaping is nested inside them. A service name containing
`$(...)` would have PowerShell evaluate that subexpression while
parsing the script — arbitrary command execution as whatever FORGE
Core is running as — reachable by any authenticated IPC caller through
`service.detail`'s `name` parameter, which was (and still is) validated
only for non-emptiness. The escaping wasn't missing; it was correct for
a context the code wasn't actually in.

**Fix:** rather than adding a second, double-quote-aware escape
function layered under the existing one (correct in principle, but
exactly the kind of nested-quoting-context reasoning that's easy to get
subtly wrong again later), `ServiceDetail` was rewritten to remove the
interpolation surface entirely: it now reuses `ListServices`'s
identical, already-argument-free CIM query (no name, no `-Filter`, no
string interpolation of any kind) and matches the requested name in Go.
Verified by cross-compiling `internal/system` for `GOOS=windows` from
this Linux sandbox (this sandbox cannot run the result, only confirm it
builds and vets clean for the real target — see DEVELOPMENT.md and this
project's standing Windows/Darwin caveat) — a real, if partial, check
against the actual platform rather than reasoning about the diff alone.

`ProcessDetail`'s analogous `-Filter "ProcessId=%d"` was checked against
the same pattern and found safe on inspection, not assumed safe by
resemblance: `pid` is an `int32` formatted with `%d`, which can only
ever produce digit characters (and possibly a leading `-`) — the type
system itself rules out the injection this bug depended on, since there
is no string value here for PowerShell to interpolate anything from.

**Privilege model, stated explicitly because it matters here more than
almost anywhere else in FORGE:** starting, stopping, restarting, or
changing a service's autostart setting commonly requires OS-level
elevated privileges — root on Linux/macOS, Administrator on Windows.
**FORGE Core never attempts to acquire or escalate privileges itself** —
no `sudo`, no polkit prompt, no UAC elevation request, no setuid anything.
If FORGE Core is running as an unprivileged user and a service action
fails because of that, the underlying command's permission-denied error
is surfaced to the user as-is (via the same event-emission and error
paths as any other failure — see `internal/app/methods_service.go`); it
is not retried, hidden, or worked around. Running FORGE with sufficient
privileges to manage services (or not) is entirely the user's decision,
made outside FORGE, the same way it would be for running `systemctl`
directly in a terminal.

Like process termination, every service action UI path (Stop and Restart
specifically, which can disrupt something currently depending on the
service) routes through `ConfirmDialog` before the request is sent — see
`frontend/src/features/services/ServiceDetailPanel.tsx`.

### Health checks

Implemented in Phase 4. `internal/health`'s `command` check type is the
first place in FORGE, outside process/service management, where user
configuration results in something being executed on the machine — worth
being explicit about exactly what that does and doesn't allow.

A `command` check's `command_args` is argv — index 0 the executable,
the rest its literal arguments — passed directly to
`exec.CommandContext(ctx, args[0], args[1:]...)`. **There is no shell
anywhere in this path.** No `sh -c`, no string concatenation, no
interpolation of one field into another. A configured argument
containing `$(...)`, backticks, `&&`, `|`, or any other shell
metacharacter is passed to the executed program as a literal string —
these have no special meaning outside a shell, and FORGE never invokes
one to give them meaning. `internal/health/checkers_test.go`'s
`TestCheckCommandNeverUsesAShell` exercises exactly this — as of Phase
8, with a genuine behavioral proof (a crafted argument that would flip
the result if shell execution were ever reintroduced), not just a
comment asserting the property; see CHANGELOG.md's Phase 8 entry for
why the original version of this test didn't actually prove what it
claimed to.

This does **not** mean a `command` check is safe to point at just
anything. FORGE runs with the privileges of whoever started it, the same
as any other locally-run tool — see "Privilege model" under "Service
management" above, which applies identically here. A check configured to
run `rm -rf /some/path` will do exactly that, on schedule, with no
additional confirmation beyond the one already required to *create* the
check in the first place (`HealthCheckFormDialog`, frontend). This is a
deliberate design choice, not an oversight: spec section 11 explicitly
rules out "an unrestricted arbitrary-command automation engine that
creates an obvious security disaster," and the way `internal/health`
honors that isn't by trying to sandbox or allow-list what a command can
do (which FORGE has no reliable way to enforce on three different
operating systems) — it's by making the *execution path itself* narrow
and auditable: argv-only, no shell, every configured check visible and
editable in the UI, nothing hidden or dynamically generated. Phase 5's
automation engine (below) applies the identical discipline to its own
"execute approved command" action.

The other seven check types (`process_exists`, `service_state`,
`tcp_port`, `http_endpoint`, `disk_threshold`, `memory_threshold`,
`cpu_threshold`) are all read-only against the system — none of them can
change anything on the machine, only observe it.

### Automation actions

Implemented in Phase 5. `internal/automation` is the second (and
currently last) place user configuration results in something being
executed — its `run_command` action is `internal/health`'s `command`
check type with a different name, argv passed directly to
`exec.CommandContext`, no shell, same reasoning, same test discipline
(`internal/automation/actions_test.go`'s
`TestActionRunCommandNeverUsesAShell` mirrors `internal/health`'s
identically-purposed test). Everything said above about a `command`
health check applies unchanged to `run_command`: FORGE runs with the
privileges of whoever started it, a configured command does exactly
what it says, and the confirmation point is creating/editing the rule
in the UI, not each individual firing.

Two more action types touch process lifecycle directly and are worth
naming specifically:

- **`stop_process`/`restart_process`** terminate every running process
  whose name case-insensitively matches the configured `process_name` —
  the identical matching rule `internal/health`'s `process_exists`
  checker uses. `restart_process` additionally launches a replacement
  process via `os/exec` (`cmd.Start()` + `cmd.Process.Release()` — a
  genuine, intentional fire-and-forget launch: FORGE does not supervise
  what it starts this way afterward, does not capture its output, and
  does not confirm the terminated process actually exited before
  starting the new one; see CHANGELOG.md's "Known limitations"). The
  launch command is argv, configured the same way `run_command`'s is:
  never a shell string.
- **`start_service`/`restart_service`** call directly into the same
  `system.Provider` methods (and therefore the identical
  privilege model — see "Service management" above) the Services
  screen's Start/Restart buttons use. An automation action bypasses that
  screen's per-click `ConfirmDialog` by design, for the same reason a
  scheduled or triggered `command` health check does: the confirmation
  point for something that runs unattended, possibly repeatedly, has to
  be *configuring the rule*, not a dialog nothing is present to click
  through at 3am when it actually fires.

**Self-triggering loop prevention**, structurally rather than by
convention: every event `internal/automation` publishes uses the
`automation.` prefix, and no rule's trigger may target an event type
under that prefix — checked at rule validation time and defensively
again at event-dispatch time (see ARCHITECTURE.md's "Automation and
scheduling engine" for both check sites). This makes it impossible to
configure a rule — accidentally or otherwise — whose trigger directly
targets one of this package's own published event types.

**Bounded execution, two ways.** `Engine.executeActions` wraps every
individual action in `context.WithTimeout(ctx, e.actionTimeout)` (30
seconds by default) before calling it — not just documented as a policy
but proven against a real hanging process:
`TestActionRunCommandRespectsContextDeadline` confirms a genuinely
hung `sleep` gets killed by the deadline rather than blocking for its
full duration, and `TestEngineActionTimeoutPreventsSemaphoreStarvation`
confirms the property this actually exists to guarantee — that a burst
of rules whose actions all hang can never permanently exhaust
`DefaultMaxConcurrentExecutions`'s 8-slot semaphore. Without this, a
single misconfigured `run_command` action pointed at something that
never exits (or `restart_process`/`start_service`/`restart_service`
against something that hangs rather than fails fast) would hold its
semaphore slot forever; as few as 8 such rules firing would silently
exhaust automation's entire execution capacity for every other rule,
with no error and no visible cause short of reading goroutine dumps —
found and closed by reading `actions.go` and `engine.go` together
rather than either in isolation, the same way the scheduler's
stale-pointer race was found (see CHANGELOG.md).

**What that check does not cover, found in Phase 8's security review by
tracing the claim above against spec section 11's own flagship
automation example rather than accepting the scope it was originally
written to describe:** "WHEN process X crashes THEN restart process X"
doesn't trigger on an `automation.`-prefixed event at all — it triggers
on a real OS-level process-lifecycle event, and its action
(`restart_process`) can cause that *same* real event to fire again if
the restarted process crashes immediately. The prefix check has nothing
to say about this, because no `automation.` event is involved anywhere
in the cycle — it's a real gap in what "self-triggering loop
prevention" actually covers, not a flaw in the check itself, which does
exactly what it was built to do. With `CooldownSeconds` left at its
default of 0 in both the backend and the frontend form — 0 meaning "no
cooldown at all" by that field's own explicit design, a legitimate
choice for a rule with no side effect worth rate-limiting — nothing
bounded how fast that specific cycle could spin.

**Fix:** two layers, added at different times as the gap was understood
more completely.

`minRuleCooldownSeconds` (`internal/automation/validate.go`) — a
1-second floor applied to every rule's effective cooldown regardless of
its configured value, including 0 — closes the *unbounded-rate* version
of the problem (limited only by how fast the OS can fork) into a
*bounded-rate* one. `TestEngineZeroCooldownStillHasAFloor`
(`internal/automation/engine_test.go`) proves this with
`CooldownSeconds` left unset, not just documents it — verified, while
building the fix, to actually fail without the floor in place before
confirming it passes with it.

That floor alone still doesn't stop a genuinely crash-looping process
from being restarted forever — only slower. `automation.MaxConsecutiveFailures`
(`internal/automation/validate.go`) is the circuit breaker this
section originally named as a follow-up not yet built: a rule with 5
consecutive `RunFailed` outcomes, no `RunSucceeded` in between, is
auto-disabled (`Enabled` flips to `false`) rather than left retrying
indefinitely at whatever rate the cooldown floor still permits. A
skipped attempt (cooldown or a conditions mismatch) neither extends nor
resets the streak — only a real execution attempt counts, matching
`checkCooldown`'s identical reasoning that a skip isn't evidence the
action itself is failing. The trip is durable (persisted alongside the
run result in the same atomic write — see `automation.RunResult`) and
visible (a dedicated `TypeRuleAutoDisabled` event, and the frontend
surfaces "N of 5" plus an explanatory note once a rule is both disabled
and tripped, so it doesn't read as a mystery pause). Re-enabling a
tripped rule via `Update` resets the streak — deliberately: the whole
reason to edit a rule that tripped the breaker is almost always to fix
and re-enable it, and leaving a near-tripped streak in place across
that edit would silently re-trip it on the very next hiccup instead of
giving the operator's re-enable the fresh grace period they'd
reasonably expect. `TestEngineCircuitBreakerAutoDisablesAfterConsecutiveFailures`
and `TestEngineCircuitBreakerResetsOnUpdateAndOnSuccess`
(`internal/automation/engine_test.go`) cover the trip itself, the
event, a disabled rule correctly no longer matching, and both reset
paths.

### Container actions

Implemented in Phase 6, with a real gap found and closed in Phase 8's
security review — summarized here, full account in CHANGELOG.md's
Phase 8 entry. `internal/containers` never runs a container image or a
command inside one on FORGE's behalf — it lists, starts, stops, and
restarts containers Docker already knows about, and fetches their
existing log output, exclusively via the `docker` CLI (argv only, never
a shell — see ARCHITECTURE.md's "Containers and diagnostics" for why
shelling out to the CLI rather than a Docker SDK). There is no `docker
exec`, `docker run` with an arbitrary image, or any other capability
that would let FORGE (or, transitively, an automation rule — none of
Phase 5's action types touch containers) execute new code inside a
container. Start/stop/restart bypass no confirmation FORGE itself would
otherwise show: they're the same "the user's own click in the UI is the
confirmation" model `process.terminate` and
`service.start`/`stop`/`restart` already use.

**The gap:** argv-only execution rules out *shell* injection (already
true, unaffected by what follows), but says nothing about *argument*
injection — a container `id` beginning with `-` being interpreted by
`docker`'s own CLI argument parser as a flag rather than a positional
container reference. Phase 8 found `id` reached every `docker`
invocation with no format validation and no `--` end-of-flags marker
separating it from `docker`'s own options.

**Verified empirically against a real `docker` binary and daemon**
(`docker.io` 29.1.3 from the standard Ubuntu archive, matching the
version a prior phase's own sandbox testing used — see DEVELOPMENT.md),
not assumed from general CLI convention: `docker start --rm` (no `--`)
fails with `unknown flag: --rm`, proving the CLI does attempt to parse
a leading-dash `id` as a flag; `docker start -- --rm` (with `--`)
instead fails with `No such container: --rm`, proving `--` forces
positional interpretation as intended. Tested across every affected
subcommand (`start`, `stop`, `restart`, `logs`), including the case
where the crafted `id` matches a genuinely valid flag rather than an
invalid one (`docker logs --tail 200 -f`, since `-f`/`--follow` is real)
— every case produced a safe usage or "no such container" error, never
a successful unintended action. **Honest severity calibration, not
overstatement:** given Docker's actual flag set for these four
subcommands, the original gap's real-world exploitability was
consistently low — confusing errors, not working exploits — but it was
a real gap worth closing on correctness grounds and as defense against
future Docker CLI versions adding a more consequential single-argument
flag to one of these subcommands.

**Fix:** `--` inserted before `id` in every `docker` invocation that
takes one (`internal/containers/provider.go`) — structural, the same
"change how the argument is interpreted rather than filter the value"
approach this document uses throughout, one layer up (the CLI's own
flag parsing, not a shell's). `containers.start`/`stop`/`restart`'s IPC
handlers also gained the same empty-`id` rejection `containers.logs`
already had, for consistent `INVALID_PARAMS` errors instead of an
opaque failure reaching `docker` with nothing to act on.

The one privilege note worth being explicit about: FORGE Core talks to
whatever Docker daemon is reachable through the `docker` CLI's normal
resolution (typically `/var/run/docker.sock`), with whatever privileges
the user running FORGE Core already has for that socket — typically
membership in a `docker` group, which is itself equivalent to root on
most systems (a well-known property of Docker's own access model, not
something FORGE changes or works around). FORGE does not elevate
privileges to reach Docker and does not run as root itself.

### Input validation

FWP request parameters are decoded with `encoding/json` into typed Go
structs, never loosely-typed maps traversed ad hoc — confirmed across
all 27 call sites in `internal/app` (Phase 8's security review; every
one follows the identical `json.Unmarshal(params, &p); if err != nil {
return ..., fwp.ErrInvalidParams }` shape, checked with a script rather
than by spot-reading a few and assuming the rest match). A type
mismatch or malformed JSON body fails at `json.Unmarshal` and returns
a structured `INVALID_PARAMS` error rather than reaching handler logic
with partially-populated data — verified against the real running
binary, not just the code: `{"interval_seconds":"not-a-number"}`
against `health.create` returns `INVALID_PARAMS` with Go's own
type-mismatch message; a genuinely malformed JSON body fails the same
way even earlier, at envelope parsing.

**One precision correction to this section's original wording, found
in the same review:** "unexpected shape... fails" is only true for
type mismatches, not for *extra* fields. `encoding/json.Unmarshal`'s
default behavior (used everywhere here — `DisallowUnknownFields` is
not called anywhere) silently ignores JSON fields that don't match any
struct field. Verified empirically: a `health.create` request with a
genuinely valid payload plus one additional, meaningless field
succeeds normally, the extra field simply dropped. This is not a
vulnerability — an attacker sending harmless extra JSON can't do
anything they couldn't already do with a well-formed request — and
it's not being changed: permissive handling of unknown fields is the
standard, deliberate choice for a protocol like FWP that's expected to
gain fields over time (a client built against an older schema sending
one fewer field, or a newer one sending one more, should degrade
gracefully rather than hard-fail on shape alone). The correction here
is to this document's precision, not to the code: "fails on a type
mismatch or malformed JSON" is the accurate claim; "fails on any
unexpected shape" overstated it.

### Dependencies

As of Phase 8, FORGE has exactly one third-party Go dependency:
`modernc.org/sqlite`, chosen and justified in ARCHITECTURE.md, confirmed
still true this phase (checked directly against `go.mod`, not assumed
carried over from Phase 6's version of this claim). Phases 2 through 8
added substantial new functionality (the entire `internal/system`
package; the health check, metrics, and log engines; the scheduler and
automation rule engines; container discovery via the Docker CLI; the
diagnostics report) without adding any — see each phase's CHANGELOG
entry for why. Keeping the dependency surface small is a deliberate,
ongoing choice, not an accident of how little has been built; each
phase's write-up in CHANGELOG.md should be able to justify any
dependency it adds the same way.

The frontend's dependency surface is equally small by design (`react`/
`react-dom` as the only runtime dependencies — see `frontend/package.json`)
but is not currently at zero known vulnerabilities: `npm audit` reports
one remaining high-severity finding in `vite`'s dev server (three
bundled advisories, none reachable from the production build output
`vite build` produces, none exploitable by FORGE's own `dev` script
configuration) that requires a major-version bump to fully close — a
verification-heavy change judged out of scope for this hardening pass.
Full detail, including which advisory was fixed in-place (a `package.json`
`overrides` pin, not a version bump) and exactly why the rest weren't:
DEVELOPMENT.md's "Toolchain status as of Phase 9."

## What's explicitly out of scope for now

- **Remote access.** There is none. The remote Agent architecture
  described in ARCHITECTURE.md is unbuilt, and Phase 1 has no network
  listener beyond the loopback-only IPC port.
- **Multi-user / authentication beyond the local IPC token.** FORGE has no
  concept of user accounts; it operates as whichever OS user runs it, with
  that user's OS-level permissions. This is intentional per the spec ("no
  account system... no authentication requirement for local operation")
  and is expected to remain true even once remote agents exist — remote
  auth secures the *connection* to an agent, not a login system inside
  FORGE itself.
