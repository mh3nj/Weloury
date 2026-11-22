# Contributing

## Before you start

Read [ARCHITECTURE.md](ARCHITECTURE.md) first, specifically the "why"
behind the package boundaries — `internal/ipc` knowing nothing about
domain concepts, method registration living in `internal/app`, event
types owned by the domain package that emits them. Most review feedback
on a first change tends to be "this belongs in a different package," and
that section explains why the boundaries are where they are so you can
place new code correctly the first time.

## Where things go

| Adding... | Goes in |
|---|---|
| A new FWP method | A `internal/app/methods_<domain>.go` file (or a new domain package's own `Register(*ipc.Router)`, called from `internal/app.New`, once a domain has enough methods to warrant its own file/package) |
| A new event type | The domain package that emits it, as a `Type` constant — not `internal/eventbus`, which stays domain-agnostic |
| OS-specific behavior | `internal/system/<provider>` behind an interface, with build-tagged per-OS implementation files (Phase 2+) |
| A new SQLite table | A new numbered migration file, added in the same change as the code that uses the table — not ahead of time |
| Frontend UI shared across features | `frontend/src/components/ui` |
| A new nav section's page | `frontend/src/features/<name>`, registered in `frontend/src/types/nav.ts` |

## What a good change looks like

- **Compiles and passes tests on its own.** `go build ./...` and
  `go test -race ./...` from `backend/` before every commit that touches
  Go code. As of Phase 8, this is a genuinely well-exercised standard,
  not an aspiration: the full backend suite (232+ tests) has been run
  under `-race` repeatedly across every phase, alongside real crash
  testing against the compiled binary, real load profiling, and
  cross-compilation checks for Windows and Darwin — see DEVELOPMENT.md's
  "Toolchain status" sections for the specifics of what's been verified
  and how.
- **Has a test that would fail without it**, for anything with real logic
  — see `tests/README.md`'s four questions (concurrency, persistence,
  failure boundary, adversarial input) for what "real logic" means here.
  A change that's pure UI layout or documentation doesn't need one.
- **Explains a non-obvious decision in a comment, not just in the PR
  description.** The code is what future readers see; the reasoning
  should travel with it. ARCHITECTURE.md is for decisions that shape a
  whole subsystem, not for "why this variable is named this."
- **Doesn't add a dependency without the same justification standard
  ARCHITECTURE.md applies to `modernc.org/sqlite`**: what does it provide
  that the standard library or ~50 lines of our own code doesn't, and
  what's the cost (build complexity, cross-compilation, supply chain
  surface) in exchange.
- **Doesn't build ahead of the phase it belongs to.** If a change adds a
  database table, an interface, or a UI screen for a capability that isn't
  actually implemented yet "because we'll need it eventually," that's
  speculative architecture the spec this project follows explicitly warns
  against (section 28, "avoid premature abstraction... do not add
  features merely because they sound impressive"). `ConfirmDialog` in
  Phase 1 is the one deliberate exception, and its comment explains why:
  it's shared UI infrastructure multiple *already-planned* Phase 3/5
  features need, not a guess about a feature that might not happen.

## Commit and PR scope

Prefer one architectural concern per change. "Add process listing" and
"add process termination with confirmation" are two changes even if they
touch the same files, because they have different risk profiles (read-only
vs. destructive) and someone reviewing "just the listing" shouldn't have
to also reason about termination confirmation UX to approve it.
