#!/usr/bin/env bash
# Builds the SPA, the documentation site and the API reference page and
# overlays them into the three go:embed directories,
# apps/server/internal/web/dist, apps/server/internal/docs/dist and
# apps/server/internal/apiref/dist, where the compiler picks them up. Each holds
# a committed placeholder index.html so `go build` and `go test` work without
# a frontend build. Callers restore the placeholders afterwards
# (scripts/restore-embed-overlay.sh) so the working tree stays clean.
#
#   bash scripts/spa-embed-overlay.sh [version]
#
# The version (argument, else VANTIGO_VERSION, else "dev") labels the
# documentation's banner; it is the same string the binary is stamped with.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED_DIR=apps/server/internal/web/dist
DOCS_EMBED_DIR=apps/server/internal/docs/dist
APIREF_EMBED_DIR=apps/server/internal/apiref/dist
VERSION="${1:-${VANTIGO_VERSION:-dev}}"

echo "==> SPA (vite)"
bun run --cwd apps/host/frontend build

echo "==> embed overlay"
rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -R apps/host/frontend/dist/. "$EMBED_DIR/"

echo "==> documentation site (astro, served under /docs, $VERSION)"
(cd docs && DOCS_BASE=/docs DOCS_EMBED=1 DOCS_VERSION="$VERSION" bun run build)

echo "==> documentation embed overlay"
rm -rf "$DOCS_EMBED_DIR"
mkdir -p "$DOCS_EMBED_DIR"
cp -R docs/dist/. "$DOCS_EMBED_DIR/"

echo "==> API reference (vite, served at /api)"
bun run --cwd apps/apiref/frontend build

echo "==> API reference embed overlay"
rm -rf "$APIREF_EMBED_DIR"
mkdir -p "$APIREF_EMBED_DIR"
cp -R apps/apiref/frontend/dist/. "$APIREF_EMBED_DIR/"
