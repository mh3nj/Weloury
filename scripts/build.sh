#!/usr/bin/env bash
#
# Produces a release build of Weloury core and the desktop shell.
#
# This stays a simple, readable script rather than a Makefile/task-runner
# abstraction, in keeping with the rest of the project's preference for
# stdlib/plain tooling over incidental complexity.
#
# Builds on the machine running it: it produces the installer for whatever
# OS executes it (the GitHub Actions release workflow runs one job per OS).
# Windows release builds go through scripts/build.ps1 instead, which also
# fetches and bundles a real smartctl.exe.
#
# Notes:
#   - No cross-compilation: run this on each target (locally or in CI).
#   - No code signing yet.
#   - weloury-core is bundled as a Tauri resource and lands beside the app
#     executable, where the shell's resolve_backend_path looks for it. On
#     Windows the same mechanism bundles smartctl.exe so the Storage tab can
#     read real drive health with no admin rights; Linux and macOS use a
#     system smartmontools on PATH.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

echo "==> Building Weloury core ${VERSION} (${COMMIT})"
(
  cd "$ROOT_DIR/backend"
  go build \
    -ldflags "-X weloury/internal/version.Version=${VERSION} -X weloury/internal/version.Commit=${COMMIT} -X weloury/internal/version.BuildDate=${BUILD_DATE}" \
    -o "$ROOT_DIR/apps/desktop/weloury-core" \
    ./cmd/weloury-core
)

echo "==> Building frontend"
(cd "$ROOT_DIR/frontend" && npm install && npm run build)

echo "==> Building desktop shell"
# Use the project's own Tauri CLI (matched to the tauri crate) rather than a
# globally installed `cargo tauri`, which may be an incompatible 1.x. The
# bundled resources differ per OS: the Windows installer ships its own
# smartctl.exe, while Linux and macOS rely on a system smartmontools on PATH,
# so their bundle carries only the core.
TAURI_JS="$ROOT_DIR/frontend/node_modules/@tauri-apps/cli/tauri.js"
OS="$(go env GOOS)"
if [ ! -f "$TAURI_JS" ]; then
  echo "Tauri CLI not found at $TAURI_JS. Run npm install in frontend first." >&2
  exit 1
fi
if [ "$OS" = "windows" ]; then
  # Windows release builds belong to scripts/build.ps1, which also bundles a
  # real smartctl.exe when it can find one. If this bash path is ever taken on
  # Windows, bundle just the core so it never aborts on a missing smartctl.
  (cd "$ROOT_DIR/apps/desktop" && node "$TAURI_JS" build --config '{"bundle":{"resources":["weloury-core.exe"]}}')
else
  (cd "$ROOT_DIR/apps/desktop" && node "$TAURI_JS" build --config '{"bundle":{"resources":["weloury-core"]}}')
fi

echo "==> Done. See apps/desktop/target/release/bundle for build output."
