#!/usr/bin/env bash
#
# Builds Weloury core and runs the Tauri dev shell against it.
#
# Usage: ./scripts/dev.sh
#
# Requires: Go, Rust + Cargo, and Node (the Tauri CLI comes from
# frontend/node_modules, so run `npm install` in frontend first, which this
# script does if it is missing). It deliberately uses that local CLI instead
# of a globally installed `cargo tauri`, which can be an incompatible 1.x.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOEXE="$(cd "$ROOT_DIR/backend" && go env GOEXE)"

echo "==> Building Weloury core"
(cd "$ROOT_DIR/backend" && go build -o "$ROOT_DIR/apps/desktop/weloury-core${GOEXE}" ./cmd/weloury-core)

echo "==> Installing frontend dependencies (skipped if already installed)"
(cd "$ROOT_DIR/frontend" && [ -d node_modules ] || npm install)

echo "==> Starting Tauri dev shell"
TAURI_JS="$ROOT_DIR/frontend/node_modules/@tauri-apps/cli/tauri.js"
# The dev shell only needs the core beside the app; smartctl is bundled at
# release time. Override resources so this works on every OS regardless of
# the Windows-oriented name in the base config.
(cd "$ROOT_DIR/apps/desktop" && node "$TAURI_JS" dev --config "{\"bundle\":{\"resources\":[\"weloury-core${GOEXE}\"]}}")
