#!/usr/bin/env bash
# Puts the committed placeholders back into the go:embed directories after an
# overlay. Idempotent; a no-op outside a git checkout.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED_DIRS=(apps/server/internal/web/dist apps/server/internal/docs/dist)

if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git clean -qfdx -- "${EMBED_DIRS[@]}"
  git checkout -q -- "${EMBED_DIRS[@]}"
fi
