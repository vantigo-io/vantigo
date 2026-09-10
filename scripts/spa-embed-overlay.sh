#!/usr/bin/env bash
# Builds the SPA and overlays it into apps/server/internal/web/dist, where
# go:embed picks it up at compile time. A committed placeholder index.html
# normally sits there so `go build` and `go test` work without a frontend
# build. Callers restore it afterwards (scripts/restore-embed-overlay.sh) so
# the working tree stays clean.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED_DIR=apps/server/internal/web/dist

echo "==> SPA (vite)"
bun run --cwd apps/host/frontend build

echo "==> embed overlay"
rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -R apps/host/frontend/dist/. "$EMBED_DIR/"
