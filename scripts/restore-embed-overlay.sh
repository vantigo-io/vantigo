#!/usr/bin/env bash
# Puts the committed placeholder back into the go:embed directory after an
# overlay. Idempotent; a no-op outside a git checkout.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED_DIR=apps/server/internal/web/dist

if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git clean -qfdx -- "$EMBED_DIR"
  git checkout -q -- "$EMBED_DIR"
fi
