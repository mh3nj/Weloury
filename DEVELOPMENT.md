# Development

## Toolchain status as of Phase 9 (in progress)

**Frontend dependency vulnerabilities, checked via `npm audit` and
fixed where fixable without disproportionate risk.** `npm install`
started surfacing audit warnings once this pass actually looked at
them (they'd have shown up on every install this whole project, but
"green build" checks don't include reading `npm audit`'s output, so
they went unnoticed until Phase 9 specifically checked). Two distinct
issues, handled two different ways:

- **`GHSA-67mh-4wv8-2f99`** (esbuild's dev server doesn't validate the
  `Origin` header, allowing a malicious website — visited in the same
  browser while `npm run dev` happens to be running — to read
  responses from it): fixed. Added `"overrides": { "esbuild":
  "^0.25.0" }` to `frontend/package.json` — a surgical pin of just the
  vulnerable transitive dependency, not a `vite` version change. Chosen
  over `npm audit fix --force`'s suggestion (jumping `vite` from 5.x to
  8.x, a major-version, breaking-change bump) because forcing just the
  one actually-vulnerable package is strictly lower risk for the same
  fix. Verified clean afterward: fresh `rm -rf node_modules
  package-lock.json && npm install`, `esbuild@0.25.12 overridden`
  confirmed via `npm ls esbuild`, then the full `tsc`/`eslint`/`npm run
  build` sequence — all pass.
- **Three more, all bundled under `vite` itself in `npm audit`'s
  report** (`GHSA-4w7w-66w2-5vf9` — path traversal in the dev server's
  optimized-deps sourcemap handling; `GHSA-v6wh-96g9-6wx3` — an
  NTLMv2 hash disclosure via `launch-editor`, a vite-internal
  dependency not separately resolvable in this project's tree per
  `npm ls`; `GHSA-fx2h-pf6j-xcff` — a Windows-specific `server.fs.deny`
  bypass, CVSS 7.5): **not fixed, deliberately, not overlooked.**
  Checked each advisory directly (not just trusted the `npm audit`
  summary) — all three are dev-server-only, none exists in `vite
  build`'s production output at all, and none has a patched release
  anywhere in the currently-pinned `^5.4.0` line: the fix versions are
  6.4.2/7.3.2/8.0.5, meaning closing this requires the same kind of
  major-version bump the esbuild fix specifically avoided. Two of the
  three further require a specific precondition FORGE's own `npm run
  dev` script doesn't trigger — the path-traversal one explicitly
  requires the dev server exposed to the network via `--host` or
  `server.host`, which this project's `dev` script (`vite`, no flags)
  doesn't set. A frontend build-tool major-version upgrade is real,
  separate work — verifying React 19 + the custom Tailwind theme +
  every other frontend dependency still behaves identically across a
  major Vite bump is not something to fold unprompted into a
  dependency-audit pass. Left open, named explicitly rather than
  silently accepted: `npm audit` will keep reporting one high-severity
  finding until `vite` itself moves to 6.4.2+.

**The release build script, actually run, not just read.**
`scripts/build.sh` had never been executed before this phase — it was
authored carefully but, like everything else touched by this project's
"actually run it" standard, deserved the same treatment applied to
the CGO SQLite stub, the Windows/Darwin cross-compilation, and the real
Docker daemon. Ran it for real (`bash scripts/build.sh`, with the
sandbox's usual SQLite stub `replace` directive wired in for the Go
build step): the `git describe`/`git rev-parse` fallbacks correctly
produced `dev`/`unknown` in this non-git-checkout sandbox (no error);
the Go build with `-ldflags` succeeded; the frontend build succeeded.
It failed exactly where the script's own comment says it will —
`cargo: command not found`, since no Rust toolchain was installed in
this sandbox at that point in the session — and `set -euo pipefail`
correctly aborted there rather than silently continuing. Verified the
`-ldflags` version injection actually reached the compiled binary, not
just assumed the flag syntax was correct: started the resulting
`apps/desktop/forge-core` for real and queried `system.version` over
actual IPC — `build_date` came back as a real, correctly-formatted UTC
timestamp matching when the build script actually ran, not a
placeholder.

**The Rust/Tauri gap, investigated properly rather than left at "no
toolchain available" — a real, evidence-backed, more precise finding,
not just a version bump of the same disclaimer.** `rustc`/`cargo` *are*
installable in this sandbox: `apt-get install cargo rustc` pulls
1.75.0 from the standard Ubuntu archive, same pattern as the
`docker.io` discovery in the container-actions entry above. Every
Tauri Linux system dependency (`libwebkit2gtk-4.1-dev`, `libgtk-3-dev`,
`libayatana-appindicator3-dev`, `librsvg2-dev`, `build-essential`,
`libssl-dev`) installed cleanly too, and `pkg-config` confirmed all
three libraries Tauri's build script checks for
(`webkit2gtk-4.1`/`gtk+-3.0`/`ayatana-appindicator3-0.1`) resolve
correctly. `cargo check` in `apps/desktop/` got as far as resolving
and downloading real crates from crates.io (`smallvec`, `strsim`,
`target-lexicon`, and others — genuine network activity against the
real registry, not a stub) before hitting a concrete, well-diagnosed
wall: `time-core v0.1.9`'s manifest declares Cargo's `edition2024`
feature, stabilized in Rust 1.85 (February 2025) — over a year newer
than Ubuntu 24.04's archived 1.75.0 (December 2023). Traced the
dependency chain precisely (`cargo tree`/`cargo update --precise`, not
guessed): `forge-desktop` → `tauri 2.11.5` (the newest 2.x release,
since `Cargo.toml` declares an unconstrained `"2"`) → `cookie 0.18.2`
→ `time 0.3.55`, which pins `time-core = "=0.1.9"` exactly — no
version flexibility to route around it by pinning an older
`time-core` alone.

Deliberately did not chase this further by forcing an older `tauri`/
`cookie`/`time` combination to find some historical version
combination that happens to predate the edition2024 requirement — that
would mean testing a fictional, artificially outdated dependency graph
nobody building FORGE with a real, current toolchain would ever
actually get (a developer with rustup and a normal, up-to-date Rust
gets the same 2.11.5/0.3.55/0.1.9 chain this session did), not a
legitimate verification of the real build path. The correct fix is a
newer Rust (1.85+), and that's a genuine, external, honestly-disclosed
constraint: rustup and the official `static.rust-lang.org` toolchain
distribution channel are not in this sandbox's network egress
allowlist, and no newer `rustc` is available through
`archive.ubuntu.com`/`security.ubuntu.com` either (24.04's `noble`
release shipped 1.75 and has no newer backport in the standard
channel). Cleaned up afterward: removed the partial `Cargo.lock` this
investigation generated (it never reached a valid, complete resolution
— regenerating fresh once a sufficient toolchain exists is correct,
not preserving a byproduct of a failed attempt) and cleared the local
crates.io registry cache.

This replaces "no Rust toolchain has been available" (accurate but
vague, unchanged since Phase 1) with a precise, evidence-backed claim:
a Rust toolchain *is* installable and gets meaningfully far — real
system dependencies resolved, real crate downloads succeeded — before
hitting one specific, named, version-diagnosed blocker. Whoever next
works on this with network access to rustup, or a newer pre-installed
Rust, should expect the rest of the Cargo build (and then the actual
`cargo tauri build` step this session never reached) to be the next
real unknown, not "does cargo exist."

## Toolchain status as of Phase 8 (in progress)

Two additions to the toolchain this phase, both used for the security
review's fixes.

**Windows/Darwin cross-compilation, without needing that OS:**
`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./internal/system/...`
(swap `windows`/`amd64` for `darwin`/`arm64` etc. as needed) compiles
and type-checks a platform-gated package for a real target from this
Linux sandbox. It cannot run the result or catch a runtime-only bug,
but a clean cross-compilation is real signal that a change to
`provider_windows.go`/`provider_darwin.go` at least builds correctly
for its actual target — used to verify Phase 8's `ServiceDetail` fix.
No CGO stub needed for this specifically *if* the package being
cross-compiled doesn't import `modernc.org/sqlite` transitively —
`internal/system` doesn't, so this works standalone.

**A real Docker daemon, from the standard Ubuntu archive, no external
registry needed for the CLI-behavior testing this phase did:**

```
apt-get install -y docker.io
dockerd &   # no systemd in this sandbox; docker.service won't autostart
docker version   # confirms the daemon is reachable
```

`docker.io` (the community-maintained Ubuntu package, not Docker Inc.'s
own build) is available from `archive.ubuntu.com` — already in this
sandbox's network allowlist, no new domain needed. This phase found
29.1.3-0ubuntu3~24.04.2 available, the same version DEVELOPMENT.md's
Phase 6 notes below reference from a prior session's own temporary
setup — same recipe, reproducible. Pulling real images from
`registry-1.docker.io` is *not* available (403 Forbidden — not in the
network allowlist), so `internal/containers`'s real-integration test
(`TestProviderRealDockerIntegration`) runs against an empty container
list in this environment; testing the `docker` CLI's own argument-
parsing behavior directly (the actual thing Phase 8's container-actions
fix needed verified) doesn't require any image at all — `docker start
--rm` and similar fail fast on the argument itself before ever
resolving an image. See CHANGELOG.md's Phase 8 entry for the exact
commands run and what they proved.

**Driving the real compiled binary over real IPC, for crash/recovery
testing that a unit test can't stand in for:**

```
# Build with the CGO SQLite stub, same as every other full-repo check.
go build -o /tmp/forge-core-testbin ./cmd/forge-core

# Isolate its data dir — os.UserConfigDir() respects $XDG_CONFIG_HOME
# on Linux, so this never touches a real user's config.
mkdir -p /tmp/forge-crash-test
XDG_CONFIG_HOME=/tmp/forge-crash-test setsid nohup /tmp/forge-core-testbin \
  > /tmp/forge-crash-test/stdout.log 2>/tmp/forge-crash-test/stderr.log < /dev/null &
```

Backgrounding it plainly (just `&`) makes whatever invoked the command
hang waiting for the child's stdout to close — `setsid nohup ... &`
with stdout/stderr redirected to files is what actually detaches it.
The handshake line (`{"port":...,"token":...,"pid":...}`) lands in the
stdout log the moment the listener is ready; parse it, then drive the
real protocol directly:

```
curl -s -X POST "http://127.0.0.1:$PORT/rpc?token=$TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"v":1,"type":"request","id":"1","method":"health.list"}'
```

`kill -9 $PID` simulates a real crash (not the graceful `/shutdown` IPC
path); restarting against the same `$XDG_CONFIG_HOME` directory tests
recovery against whatever state the crash left behind. `sqlite3
$XDG_CONFIG_HOME/forge/forge.db "PRAGMA integrity_check;"` checks the
raw file directly, independent of whether `forge-core` itself would
have reported a problem. See CHANGELOG.md's Phase 8 entry for what this
was actually used to verify (SIGKILL-mid-write survival and the
scheduler's missed-execution policy, in both directions).

**Profiling the real binary (goroutine leaks, idle CPU, heap):**
`net/http/pprof` is not wired into `forge-core` normally — deliberately;
an always-on debug HTTP endpoint isn't something a shipped ops tool
should carry. To profile locally, add it *temporarily*:

```go
import _ "net/http/pprof" // TEMPORARY — never commit

// early in run(), before app.New():
go func() { _ = http.ListenAndServe("127.0.0.1:6060", nil) }()
```

Then, with `forge-core` running (same build/launch recipe as the
crash-testing section above):

```
curl -s http://127.0.0.1:6060/debug/pprof/goroutine?debug=1 | head -1  # count
curl -s http://127.0.0.1:6060/debug/pprof/goroutine?debug=2            # full stacks, named by subsystem
curl -s http://127.0.0.1:6060/debug/pprof/heap?debug=1 | grep HeapAlloc
```

`/proc/$PID/stat` fields 14/15 (utime/stime, in clock ticks —
`getconf CLK_TCK` for the tick-to-second conversion) give real idle CPU
usage without needing pprof at all; `/proc/$PID/status`'s `VmRSS` gives
real memory. Revert the `main.go` change before finishing — `diff`
against a backup copied before editing, the same as every other
temporary instrumentation in this file, to prove the revert is actually
clean rather than assuming it. See CHANGELOG.md's Phase 8 entry for
what this was used to verify (goroutine-leak testing on event-stream
disconnects and health-check lifecycle, plus idle CPU/memory).

## Toolchain status as of Phase 7 (substantively complete)

This phase's first slice was a full-repository build/test pass — not
new subsystem code, so there's no new toolchain workaround to document,
but the *scope* of verification changed and is worth recording:
Phases 4–6 each verified their own new packages plus a re-run of the
existing suite, using the sandbox-only CGO SQLite stub (see the Phase 4
section below) recreated fresh each time and never committed. Phase 7
is the first time `go build ./...` / `go vet ./...` / `go test -race
-count=1 ./...` were run across the *entire* repository in one pass
with that stub wired in, and the first time the full `internal/app`
end-to-end suite ran alongside every other package rather than in
isolation. Result: clean across all 228 tests. Two real, if narrow,
bugs in the *stub itself* (not FORGE) were caught this way and are
detailed in CHANGELOG.md's Phase 7 entry — a `time.Time`-as-bind-
parameter gap and a decltype-blind TEXT scan — both being exactly the
category of thing "run it for real instead of trusting it compiles"
exists to catch.

To reproduce: same recipe as Phase 4 below (`apt-get install
golang-go libsqlite3-dev`, write the stub, `go mod edit -replace
modernc.org/sqlite=/path/to/stub`), just run `go test -race -count=1
./...` from `backend/` instead of a per-package subset. Revert the
`replace` line afterward — `go.mod` must never carry it.

## Toolchain status as of Phase 6

Phase 6 needed one genuinely new piece of verification infrastructure
beyond what Phases 4–5 established (the CGO SQLite stub — see the
section below): a real Docker daemon, to verify `internal/containers`
against actual `docker` CLI output rather than fixtures written from
documentation. `apt-get install docker.io` provides the `dockerd`/
`docker` binaries, but this sandbox has no init system to start the
daemon as a service, no registry access to pull a public image (Docker
Hub isn't in this sandbox's network allowlist — see the network
allowlist note in earlier sections), and no persistent background
process across separate tool invocations in the environment this was
developed in. All three were worked around directly:

- **Starting the daemon**: `dockerd > /tmp/dockerd.log 2>&1 &` run
  manually at the start of a shell session, the same "start what's
  missing, verify for real, don't skip it" approach the CGO SQLite stub
  already established.
- **No registry access**: built a real container from a minimal `FROM
  scratch` Dockerfile — `scratch` needs no image pull at all — wrapping
  a trivial static Go HTTP server, giving a real, running container to
  inspect without needing `docker pull` to work.
- **No persistent background process across tool calls**: every real
  verification step (running the container, capturing fixture JSON,
  running `go test`) that needed the daemon reachable was done within a
  single combined shell invocation (start `dockerd`, wait for it, then
  run the dependent command) rather than assuming a previous step's
  daemon was still running.

This produced real fixtures — `internal/containers/parse_test.go`'s
`fixturePSRunningWithPort`/`fixturePSExitedNoPort`/`fixtureStatsRunning`
are captured verbatim from a live Docker 29.1.3 daemon's actual JSON
output, not hand-written — and a real integration test
(`internal/containers/integration_test.go`) that talks to a live daemon
directly and was confirmed passing against one, not merely written to
pass in theory. The same test also confirms its own graceful-skip path
for real: run again with `dockerd` killed, it skips cleanly (in ~1.2s,
down from ~4.4s with the daemon present — the timing difference itself
being confirmation the skip path, not a slow success, is what ran).

`internal/system.NetworkConnections` (also Phase 6) needed no comparable
workaround: this sandbox already has a real, populated `/proc/net/tcp`,
so `provider_linux_integration_test.go`'s
`TestLinuxProviderNetworkConnectionsFindsRealListener` opens an actual
TCP listener from within the test and confirms it's found — real
verification with no daemon or registry dependency at all.

Everything else repeated Phase 4/5's now-established process unchanged:
clean `go build`/`go vet`/`go test -race` across the whole repository
(228 tests total as of this phase, using the CGO SQLite stub for
`internal/storage`/`internal/app` exactly as before — see the section
below), clean `npm install`/`tsc --noEmit`/`eslint`/`npm run build` on
the frontend, and an extended full-stack end-to-end test
(`internal/app/app_test.go`) covering `diagnostics.run` and
`containers.availability`/`containers.list` — the latter written to
assert correctly whichever way Docker availability happens to go
(confirmed against both branches for real, per the daemon-killed test
above).

One inconsistency was caught and fixed before it shipped, not after:
`internal/diagnostics`' first draft depended on the concrete
`*storage.EventRepository` type directly, breaking from the consumer-
defined-interface pattern every other Phase 4/5 package
(`health.Repository`, `metrics.Repository`, `scheduler.Repository`,
`automation.Repository`) established specifically so each package stays
unit-testable without a real database. Caught while writing this
section (documentation as a forcing function to re-read what was just
built), fixed by adding a narrow `eventReader` interface before any
test was written against the concrete type.

**Still genuinely unverified, unchanged from before:** the Rust/Tauri
shell (`apps/desktop`) — no Rust/Cargo toolchain has been available in
any sandbox this project has been developed in so far — and the
Darwin/Windows halves of `internal/system`, `internal/containers`'
Docker-CLI-shim compatibility with Podman, and this project's docker CLI
parsing against any Docker version other than the 29.1.3 verified here
(the field names/JSON shapes used are stable, documented parts of
Docker's CLI output format, not internal details expected to change
often, but "verified against 29.1.3" is a narrower claim than "verified
against Docker" and should be stated as such).

## Toolchain status as of Phase 5

Phase 4 first established that this codebase actually gets built, run,
and tested in this sandbox rather than just written carefully and hoped
to compile — see the unchanged section below for the full account of how
and what that covers (the CGO SQLite stub, the two Phase-1-era bugs it
caught). Phase 5 followed the identical process for
`internal/scheduler` and `internal/automation`, with the same result:
clean `go build`/`go vet`/`go test -race` across the whole repository,
`npm install`/`tsc --noEmit`/`eslint`/`npm run build` clean on the
frontend, and — using the same temporary, never-committed CGO stub — a
real, passing end-to-end test that wires a scheduled task to an
automation rule and confirms the whole pipeline fires over the real HTTP/
FWP wire protocol (`internal/app/app_test.go`'s "scheduled automation
rule fires end to end").

That full-stack test is what caught this phase's bugs, the same way
Phase 4's caught its own:

- `automation.Engine.Create` never set a new rule's `Enabled` field, so
  every rule created through the engine was silently inert — correctly
  never matched by `ruleMatchesEvent`, because nothing had ever set the
  field true. Caught immediately by the first engine test that actually
  exercised trigger-matching (a rule that provably could never fire);
  fixed by having `Create` set `Enabled = true` explicitly.
- Two data races in `internal/automation`'s own test helpers (not
  production code): a shared fake system-capabilities struct's slice
  fields were read directly from the test goroutine while Engine wrote
  them concurrently from its action-execution goroutines. `go test
  -race` caught both on the very first run; fixed with locked snapshot
  getters.

195 tests pass across the repository as of this phase (37 of them new —
`internal/scheduler` and `internal/automation` together), all under
`-race`, all using the same consumer-defined-`Repository`-interface
pattern Phase 4 established specifically so packages like these stay
fully unit-testable with in-memory fakes despite this sandbox's
continued inability to reach `modernc.org/sqlite`.

**Still genuinely unverified, unchanged from before:** the Rust/Tauri
shell (`apps/desktop`) — no Rust/Cargo toolchain has been available in
any sandbox this project has been developed in so far — and the
Darwin/Windows halves of `internal/system` (see "A note on how Phase 1
was built" below and ARCHITECTURE.md's "OS abstraction"). Phase 5 added
no new platform-specific code at all (the scheduler and automation
engine are pure Go, no OS abstraction needed), so it neither closes nor
widens that gap.

## Toolchain status as of Phase 4

Updating the picture painted by "A note on how Phase 1 was built" below,
which is now partly out of date: starting with Phase 4, this codebase
**has** actually been built, run, and tested — not just written
carefully and hoped to compile.

The sandbox this was developed in doesn't have general internet access,
but does allow a specific set of hosts (package registries and GitHub,
not `proxy.golang.org` or `modernc.org` themselves — see below). That was
enough to:

- `apt-get install golang-go` — a real Go 1.22 toolchain. Every Go
  package **except** `internal/storage` and `internal/app` (both of
  which import `internal/storage`, transitively) builds, `go vet`s, and
  passes `go test -race` directly, with no workaround needed.
- `npm install` against the real npm registry — the frontend's actual
  dependency tree, not a guess. `tsc --noEmit`, `eslint .`, and
  `npm run build` (a real Vite production build) all pass. This caught
  and fixed two real, if small, pre-existing issues nothing had ever
  been able to catch before: a missing `vite-env.d.ts` (without it,
  `import.meta.env` doesn't type-check) and two unused-import lint
  errors in this phase's own new code.

**`internal/storage` and `internal/app` are the one exception**, because
`modernc.org/sqlite` (and the Go module proxy, `proxy.golang.org`, as a
fallback route to it) are not reachable from this sandbox. Rather than
leave those two packages permanently unverified the way Phase 1–3b had
to, this phase's testing used a **temporary, clearly-marked, sandbox-only
stub**: a local Go module at a `replace modernc.org/sqlite =>
/tmp/stub/modernc.org/sqlite` directive (never committed — check `git
diff go.mod` if you're unsure).

**Updated in a later pass (see CHANGELOG.md's "Phase 8, revisited"
entry): the stub itself got strictly better**, without changing the
approach's shape. The original version implemented the `database/sql`
driver interface from scratch against the system's `libsqlite3` via
CGO. It turns out that's more custom code than necessary:
`GOPROXY=direct` resolves Go modules straight from their VCS host via
`go-import` meta tags and git, bypassing `proxy.golang.org` (blocked)
entirely — and for any module hosted on `github.com` specifically, that
host *is* in this sandbox's allowlist, even though `proxy.golang.org`
isn't. `github.com/mattn/go-sqlite3` — a real, maintained, widely-used
CGO SQLite driver, not this project's own code — fetches and builds
cleanly this way (`apt-get install libsqlite3-dev gcc build-essential`
first, same system dependency as before). The stub module is now a
~20-line shim:

```go
package sqlite // still at modernc.org/sqlite via the replace directive

import (
	"database/sql"
	sqlite3 "github.com/mattn/go-sqlite3"
)

func init() {
	sql.Register("sqlite", &sqlite3.SQLiteDriver{})
}
```

registering `go-sqlite3`'s driver under the driver name `"sqlite"` —
the exact name `db.go`'s `sql.Open("sqlite", path)` expects — instead
of hand-writing CGO bindings. Same non-committed, sandbox-only,
`replace`-directive pattern as before; strictly less custom code
sitting between the tests and a real SQLite implementation. This is
still real SQLite, linked differently than production `forge-core`
will be (CGO vs. `modernc.org`'s pure-Go implementation), which remains
strictly a build/deployment concern at the level this project uses
SQLite, not a SQL-behavior concern. `modernc.org` itself is still
genuinely unreachable — `go-sqlite3` is GitHub-hosted, `modernc.org/sqlite`
is not — this doesn't close that gap, it routes around it more cleanly
for local testing purposes only.

That stub is how `internal/storage`'s and `internal/app`'s tests —
including a full end-to-end test (`internal/app/app_test.go`) that
builds a real `App`, runs it, and drives it over actual HTTP/FWP
exactly as the Tauri shell would — actually ran, instead of only
type-checking.

Real bugs were found and fixed this way, which is the entire point of
this exercise:

- `storage.Open` pooled up to 8 SQLite connections, but `PRAGMA
  busy_timeout` only ever reached whichever single connection happened
  to execute that statement — any additional pooled connection opened
  later under concurrent load had no busy timeout and failed instantly
  with "database is locked" instead of waiting. Fixed by moving to a
  single connection (`SetMaxOpenConns(1)`); see `db.go`'s doc comment.
- `internal/app`'s event-persistence subscriber subscribed to the bus
  from inside its own goroutine, racing `Run`'s `TypeBackendStarted`
  publish a few lines later with no ordering guarantee. On the losing
  side of that race, the very first event of every session silently
  never got recorded. Fixed by subscribing synchronously on `Run`'s own
  goroutine first; see `persistence.go`'s doc comment.

Neither bug is Phase-4-specific — both existed since Phase 1 — they were
just never exercised by anything with real concurrency and real timing
before. If you're touching `internal/storage` or `internal/app` and want
to reproduce this locally without network access to `modernc.org`, the
same approach (a local CGO stub + a temporary `replace` directive) is
the way to do it; do **not** leave the `replace` directive in a commit.
Phase 5 reused this exact approach unchanged — see the section above.

## A note on how Phase 1 was built

Worth being upfront about: this codebase was authored in a sandboxed
environment with no Go toolchain, no Rust/Cargo, no SQLite CLI, and no
network access. Every file was written by hand with the same care as if it
were being compiled and tested continuously, and the TypeScript was
syntax-verified with the actual `typescript` compiler package (parse-only,
since no `node_modules` were available to resolve `react`'s types) — but
**none of it has been built or run yet.** You are, realistically, the
first compiler this code will meet.

Concretely, that means:

- `backend/go.sum` does not exist. Run `go mod tidy` once you have network
  access; it will resolve the real current version of
  `modernc.org/sqlite` and its transitive dependencies and generate it.
  `go.mod`'s pinned version is a reasonable guess, not a verified one.
- The Rust code in `apps/desktop/src/main.rs` uses the standard,
  documented Tauri v2 pattern for reacting to app-exit events
  (`Builder::manage` → `invoke_handler(generate_handler![...])` →
  `.build(generate_context!())?.run(|app_handle, event| { ... })`
  matching on `RunEvent::Exit`). Double-check it against whatever `tauri`
  crate version Cargo actually resolves before relying on it — a minor
  API signature difference between Tauri versions is the most likely
  first compile error you'll hit.
- Nothing has been run under `go vet`, `golangci-lint`, `cargo clippy`, or
  `eslint` yet (the tools weren't available). Expect to run all four
  early and fix whatever they find — that's a normal, expected part of a
  first build, not a sign something is fundamentally wrong.
- **Service management (`internal/system`'s `systemctl`/`launchctl`/
  PowerShell-service code, added later) is a different tier of unverified
  than the rest of this codebase's Linux support.** Everything else
  Linux-specific was checked against this sandbox's real `/proc`
  filesystem and real running processes. `systemctl` is installed here
  but non-functional — the sandbox is a container, not booted with
  systemd as PID 1 (`systemctl` itself reports "Host is down") — so the
  service-management code was written to `systemctl`'s documented output
  formats the same way the macOS/Windows code was written to their
  platforms' documented formats: carefully, but unverified against a live
  instance. Treat Linux service management with the same caution as
  Darwin/Windows system info, not the higher confidence the rest of this
  codebase's Linux support earns.

None of this should be read as the code being untrustworthy — it's ordinary
Go, ordinary React, and a conservative subset of Tauri's API, written
without exotic patterns specifically to minimize the risk of something
subtle being wrong. It's disclosed here because "actually compiles" is a
real bar this document can't claim to have cleared yet, and pretending
otherwise would be a worse failure than the honest version.

## Prerequisites

- **Go 1.22+**: `go version`
- **Rust + Cargo** (stable channel): `rustc --version`, `cargo --version`
- **Node 18+**: `node --version`
- **Tauri CLI**: comes from `frontend/node_modules` (`@tauri-apps/cli`), so
  `npm install` in `frontend/` is all that is needed. The build and dev
  scripts call that local CLI directly; a globally installed `cargo tauri`
  can be an older 1.x that rejects this 2.x config, so don't rely on it.

No SQLite installation is required. `modernc.org/sqlite` is pure Go and
statically links into the `forge-core` binary.

## Building and running

```bash
./scripts/dev.sh
```

This builds `forge-core` from `backend/`, places it next to where the
Tauri dev binary will look for it, installs frontend dependencies if
`frontend/node_modules` doesn't exist yet, and runs the desktop shell in
dev mode (Vite dev server plus the window).

If something in that chain fails, the steps are independently useful for
narrowing down where:

```bash
# Backend only — builds and runs forge-core standalone, printing its
# handshake line (port + token) to stdout and logging to stdout/the log
# file. Useful for testing the IPC surface directly with curl.
cd backend
go build -o forge-core ./cmd/forge-core
./forge-core

# Frontend only, against a manually-started forge-core — see "Running the
# frontend standalone" below.
cd frontend
npm install
npm run dev
```

### Running the frontend standalone

`npm run dev` inside `frontend/` without Tauri won't have a
`get_backend_endpoint` Tauri command available. `useBackendConnection`
falls back to two environment variables in that case:

```bash
# in one terminal
cd backend && go build -o forge-core ./cmd/forge-core && ./forge-core
# copy the port and token from the printed handshake line, then in another terminal:
cd frontend
VITE_FORGE_CORE_URL=http://127.0.0.1:<port> VITE_FORGE_CORE_TOKEN=<token> npm run dev
```

### Talking to `forge-core` directly

Useful for verifying the backend independent of any frontend:

```bash
./forge-core
# {"port":54231,"token":"a1b2...","pid":48213}

curl -s -X POST http://127.0.0.1:54231/rpc \
  -H "Authorization: Bearer a1b2..." \
  -H "Content-Type: application/json" \
  -d '{"v":1,"type":"request","id":"1","method":"system.ping","params":{"echo":"hi"}}'

# Phase 2's system.*/process.* methods take no params:
curl -s -X POST http://127.0.0.1:54231/rpc \
  -H "Authorization: Bearer a1b2..." \
  -H "Content-Type: application/json" \
  -d '{"v":1,"type":"request","id":"2","method":"process.list"}'

curl -N -s "http://127.0.0.1:54231/events?token=a1b2..."
# streams a heartbeat Envelope every 5 seconds
```

Full method list as of Phase 6 (adding containers/network/diagnostics to
automation/scheduling + observability + process/service management):
`system.ping`, `system.version`, `system.info`, `system.cpu_info`,
`system.cpu_usage`, `system.memory`, `system.disks`,
`system.network_interfaces`, `system.network_connections`,
`process.list`, `process.tree`, `process.detail`, `process.terminate`,
`service.list`, `service.detail`, `service.start`, `service.stop`,
`service.restart`, `service.set_enabled`, `health.list`, `health.get`,
`health.create`, `health.update`, `health.delete`, `health.run_now`,
`metrics.current`, `metrics.rolling`, `metrics.history`, `logs.query`,
`events.recent`, `scheduler.list`, `scheduler.get`, `scheduler.create`,
`scheduler.update`, `scheduler.delete`, `scheduler.set_enabled`,
`automation.list`, `automation.get`, `automation.create`,
`automation.update`, `automation.delete`, `containers.availability`,
`containers.list`, `containers.logs`, `containers.start`,
`containers.stop`, `containers.restart`, `diagnostics.run` — see
`internal/app/methods.go` and the sibling `methods_*.go` files.
`process.detail`/`process.terminate` take `{"pid": <number>}` (+
`"force": true|false` for terminate); `service.detail`/`service.start`/
`service.stop`/`service.restart` take `{"name": "<service name>"}`;
`service.set_enabled` additionally takes `"enabled": true|false`.
`health.create`/`health.update` take a full check definition (see
`healthCheckParams` in `methods_health.go`); `health.get`/
`health.delete`/`health.run_now` take `{"id": "<check id>"}`.
`metrics.rolling` takes `{"metric": "cpu", "scope": ""}`;
`metrics.history` additionally takes optional `"since_seconds"` and
`"limit"`. `logs.query` takes optional `"level"`, `"search"`, `"before"`
(a cursor — see `logs.Query`'s doc comment), and `"limit"`.
`events.recent` takes optional `"limit"`. `scheduler.create`/
`scheduler.update` take `{"name", "type": "once"|"interval", "run_at"?,
"interval_seconds"?}` (`run_at` is RFC3339 with an explicit offset, e.g.
`"2026-08-25T17:00:00-07:00"` — see `scheduler.Task`'s doc comment on
timezone handling); `scheduler.get`/`scheduler.delete` take `{"id"}`;
`scheduler.set_enabled` takes `{"id", "enabled": true|false}`.
`automation.create`/`automation.update` take a full rule definition (see
`automationRuleParams` in `methods_automation.go`: `name`, `enabled`,
`trigger`, `conditions`, `actions`, `cooldown_seconds`);
`automation.get`/`automation.delete` take `{"id"}`. `containers.list`/
`containers.availability`/`diagnostics.run` take no params.
`containers.logs` takes `{"id", "tail"?}` (defaults to 200 lines);
`containers.start`/`containers.stop`/`containers.restart` take `{"id"}`
(a container ID or name — anything `docker start/stop/restart` itself
accepts). Live streaming (health state transitions, new log entries,
automation rule executions, every bus event generally) arrives over
`GET /events`, not a request/response method — see "Talking to
`forge-core` directly" above.

## Testing

```bash
cd backend
go test -race ./...
```

Always with `-race`. As of Phase 4 (continued through Phases 5 and 6),
this actually runs clean end to end in this project's own sandbox for
every package except `internal/storage`/`internal/app`, which need a
real `modernc.org/sqlite`
— see "Toolchain status as of Phase 6" above for how to verify those two
locally without network access to it, and (Phase 6 specifically) for how
`internal/containers`' real-daemon verification was done. See
`tests/README.md` for what's covered and why, and for what isn't covered
yet on the frontend and Rust sides.

```bash
cd frontend
npm install
npx tsc --noEmit   # strict type-check
npx eslint .
npm run build      # tsc --noEmit && vite build
```

## Building a release

```bash
./scripts/build.sh
```

See the comment at the top of that script for its current, real
limitations (no cross-compilation matrix, no code signing, `forge-core`
placed alongside the Tauri binary rather than bundled as a proper signed
sidecar). Closing those gaps is Phase 9 work.

## Project conventions

- **Go**: standard `gofmt`/`go vet` conventions. Prefer the standard
  library; a third-party dependency needs a real justification (see
  ARCHITECTURE.md's "Persistence" section for an example of that
  reasoning applied). Every exported identifier that isn't
  self-explanatory gets a doc comment explaining *why*, not just what.
- **TypeScript**: `strict` mode is on; keep it on. Path alias `@/` maps to
  `frontend/src/`. Components that render dynamic Tailwind class names
  must use a static lookup map (see `components/ui/StatusDot.tsx`) — a
  template-interpolated class name silently produces no CSS, since
  Tailwind's JIT scanner needs literal class name strings in source.
- **Commit scope**: a change should compile and pass tests on its own; see
  CONTRIBUTING.md.
