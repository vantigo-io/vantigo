#!/usr/bin/env bash
# What a pull request's CI has to test, given the commit it is compared with.
#
#   bash scripts/ci-affected.sh go <shard> [base]   # Go import paths to test, one per line
#   bash scripts/ci-affected.sh frontend [base]     # "all" or "changed"
#
# Without a base — a merge queue run, a push to main, a manual run — the answer
# is everything. With one, a Go package is tested when a file in it changed or
# when anything it or its tests import does (go list -test -deps), and the
# frontend runs Vitest's own --changed selection over its module graph. Some
# changes reach every test without going through an import, and run
# everything: see run_everything. A miss here is caught by the merge queue,
# which always runs the full suite; it only ever makes a pull request's own
# run too small, never wrong.
#
# Go shards: "a" is internal/invoices and internal/entra, "b" every other
# package, so a new package always lands in b.
set -euo pipefail
cd "$(dirname "$0")/.."

kind="${1:?usage: ci-affected.sh go <shard> [base] | frontend [base]}"
shift
shard=""
if [ "$kind" = "go" ]; then
  shard="${1:?go needs a shard: a or b}"
  shift
fi
base="${1:-}"

changed=""
if [ -n "$base" ]; then
  # HEAD_REF is for trying the selection against an older commit by hand.
  changed="$(git diff --name-only "$base" "${HEAD_REF:-HEAD}")"
fi

# Changes whose reach does not follow imports: the toolchain and the module's
# dependencies, the test database, CI itself, and this script.
run_everything() {
  [ -z "$base" ] && return 0
  local common='^(\.github/workflows/|scripts/ci-affected\.sh$|mise\.toml$)'
  case "$kind" in
    go) grep -Eq "$common|^apps/server/go\.(mod|sum)$|^docker-compose\.test\.yml$|^openapi/" <<<"$changed" ;;
    frontend) grep -Eq "$common|^bun\.lock$|^package\.json$|^vitest\.config\.mts$" <<<"$changed" ;;
  esac
}

in_shard() {
  case "$shard" in
    a) grep -E '/internal/(invoices|entra)(/|$)' || true ;;
    b) grep -Ev '/internal/(invoices|entra)(/|$)' || true ;;
    *) echo "ci-affected.sh: unknown shard $shard" >&2; exit 1 ;;
  esac
}

if [ "$kind" = "frontend" ]; then
  if run_everything; then echo all; else echo changed; fi
  exit 0
fi
[ "$kind" = "go" ] || { echo "ci-affected.sh: unknown kind $kind" >&2; exit 1; }

cd apps/server
if run_everything; then
  go list ./... | in_shard
  exit 0
fi

# The packages a changed file belongs to: its own directory, or the nearest
# one above it that is a package (testdata, embedded files). A file under
# apps/server that belongs to no package changes something unknown, so it
# runs everything.
root="$(pwd)"
# "<import path> <dir>" for every package, then each changed file as "@<file>":
# awk walks each file's directory up to the nearest package, printing that
# package, or "!" when it reaches the module root without finding one.
touched="$(
  {
    go list -f '{{.ImportPath}} {{.Dir}}' ./...
    while read -r file; do
      case "$file" in
        (apps/server/*) echo "@$root/${file#apps/server/}" ;;
        # Read by tests through a relative path, not an import.
        (docs/src/content/docs/*) echo "=$(go list ./internal/assistant)"; echo "=$(go list ./internal/staff)" ;;
      esac
    done <<<"$changed"
  } | awk -v root="$root" '
    /^=/ { print substr($0, 2); next }
    /^@/ {
      dir = substr($0, 2); sub(/\/[^\/]*$/, "", dir)
      while (!(dir in pkg) && dir != root && dir != "") sub(/\/[^\/]*$/, "", dir)
      print ((dir in pkg) ? pkg[dir] : "!"); next
    }
    { pkg[$2] = $1 }' | sort -u
)"
if grep -qx '!' <<<"$touched"; then
  go list ./... | in_shard
  exit 0
fi
[ -n "$touched" ] || exit 0

# Every package whose own files or test binary's dependencies include a
# touched package. go list -test names test variants "p [p.test]",
# "p_test [p.test]" and "p.test"; all of them count as p.
go list -test -f '{{.ImportPath}}|{{join .Deps " "}}' ./... |
  awk -F'|' -v touched="$(tr '\n' ' ' <<<"$touched")" '
    BEGIN { n = split(touched, t, " "); for (i = 1; i <= n; i++) hit[t[i]] = 1 }
    {
      p = $1; sub(/ \[.*\]$/, "", p); sub(/\.test$/, "", p); sub(/_test$/, "", p)
      if (p in hit) { out[p] = 1; next }
      m = split($2, d, " ")
      for (i = 1; i <= m; i++) if (d[i] in hit) { out[p] = 1; break }
    }
    END { for (p in out) print p }' |
  sort | { grep -Fxf <(go list ./...) || true; } | in_shard
