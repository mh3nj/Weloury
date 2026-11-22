# Installing FORGE

This document is for someone who wants to *run* FORGE, not build or
modify it — see [DEVELOPMENT.md](DEVELOPMENT.md) for that instead.

## There is no release build yet

Be direct about this rather than write installation instructions for
something that doesn't exist: FORGE has no packaged installer, no
`.msi`, `.dmg`, `.AppImage`, or any other distributable artifact, for
any platform, as of this writing. Phase 9's own attempt at producing
one (see CHANGELOG.md's Phase 9 entry) got the Go backend and the React
frontend building cleanly, but stopped at the Rust/Tauri half over a
toolchain version gap, not a design problem — the desktop shell's own
code has never been compiled at all, on any platform, by anyone.
Nothing below is aspirational marketing for a product that exists; it's
an accurate description of what installation *will* look like once a
release exists, written now so it doesn't have to be reconstructed
later, and clearly labeled as not-yet-true where it isn't.

Until a release exists, "installing" FORGE means building it from
source — follow [DEVELOPMENT.md](DEVELOPMENT.md)'s "Development setup"
and "Building and running" sections.

## What installation will look like, once it exists

FORGE ships as a single native desktop application per platform (Tauri
bundles the Go backend as a sidecar binary and the React frontend as
static assets into one installable package — see
[ARCHITECTURE.md](ARCHITECTURE.md) for why the split exists). No
separate backend/frontend install steps, no runtime dependency to
install first (no separately-installed Node, Go, or browser runtime —
Tauri uses the OS's own WebView, not a bundled Chromium the way
Electron would).

- **Windows:** an `.msi` or `.exe` installer (Tauri supports both; which
  one ships is a decision for whenever a release is actually cut, not
  made here). Standard Windows install flow — no elevation required for
  the install itself. Elevation *may* be required later for specific
  in-app actions (starting/stopping a Windows service, for example) —
  see SECURITY.md's "Service management" section for exactly why FORGE
  itself never requests or escalates privileges on your behalf.
- **macOS:** a `.dmg` containing a standard `.app` bundle — drag to
  Applications, like any other Mac app. Not code-signed or notarized
  yet (no Apple Developer account has been part of this project's setup
  at any point) — until that changes, macOS's Gatekeeper will refuse to
  open it normally; the standard workaround (right-click → Open, or
  `xattr -d com.apple.quarantine` on the `.app`) will be necessary and
  will be documented here plainly, not hidden, once there's a real
  `.dmg` to test that workaround against.
- **Linux:** most likely an `.AppImage` (no distro-specific packaging
  format has been chosen yet, and Tauri's own default build targets
  vary by distro tooling available at build time — see DEVELOPMENT.md's
  Phase 9 entry for exactly what's been verified so far).

No installer requests network access during setup, no account, no sign
in — matching spec section 2's "no mandatory external service" and
"no authentication requirement for local operation." The one exception,
already true today and unrelated to installation: Docker container
discovery and control (spec section 15) needs a Docker daemon already
running and reachable if you want that feature; FORGE detects its
absence and stays fully useful without it (see the Containers feature
description in [README.md](README.md)).

## First run

The first time FORGE Core starts, it creates its data directory if one
doesn't already exist, using Go's `os.UserConfigDir()` (verified
directly against the actual running binary, not just read from the
source — see CHANGELOG.md's Phase 8 crash-testing entry for how):

| Platform | Location |
|---|---|
| Linux | `$XDG_CONFIG_HOME/forge` (usually `~/.config/forge`) |
| macOS | `~/Library/Application Support/forge` |
| Windows | `%AppData%\forge` |

Inside that directory, FORGE creates:

- `config.json` — your settings (log level, theme, retention windows),
  written with sensible defaults on first run:
  ```json
  {
      "schema_version": 1,
      "ipc_host": "127.0.0.1",
      "ipc_port": 0,
      "log_level": "info",
      "log_format": "text",
      "theme": "dark",
      "metrics_retention_days": 14,
      "events_retention_days": 30
  }
  ```
  (real output from a real running instance, not a hand-written
  example — copied directly from a test run this project's own
  hardening work produced).
- `forge.db`, `forge.db-wal`, `forge.db-shm` — your local SQLite
  database (WAL mode; see SECURITY.md and CHANGELOG.md's Phase 8 crash-
  testing entry for what's been verified about its crash-safety) holding
  health checks, automation rules, schedules, and historical event data.
  Nothing here ever leaves your machine — see SECURITY.md's IPC section
  for the full local-only model.
- `forge-core.log` — FORGE Core's own structured log, the same content
  the in-app **Logs** screen streams live.

No installer-level "first-run wizard," no data collected before you've
touched anything, no telemetry — consistent with spec section 2's "no
account system" and SECURITY.md's stated privacy model throughout.

## Uninstalling

Once a real installer exists: use your OS's normal uninstall path (the
Windows/macOS installer's own uninstaller, or removing the `.AppImage`
directly on Linux) to remove the application itself. That alone does
**not** delete the data directory described above — consistent with
how most desktop applications behave, and deliberate: an uninstall
shouldn't silently destroy your health-check/automation/schedule
configuration and event history if you're about to reinstall a newer
version. To fully remove everything FORGE has ever written, also delete
the platform-specific directory from the table above.

## Getting help

There's no support channel or issue tracker linked here yet — this is
still pre-release, actively-developed software. See
[CONTRIBUTING.md](CONTRIBUTING.md) for how the project is organized if
you're looking to get involved, or [SECURITY.md](SECURITY.md) if what
you've found is a security issue specifically.
