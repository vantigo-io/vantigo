#!/usr/bin/env bash
# Builds everything the container image COPYs, natively — nothing compiles
# inside Docker:
#
#   dist/server/linux/amd64/vantigo
#   dist/server/linux/arm64/vantigo
#
# The SPA is embedded into both binaries (scripts/spa-embed-overlay.sh) and
# the placeholder is restored afterwards, even on failure. The layout mirrors
# GoReleaser's dockers_v2 build context (linux/<arch>/vantigo), so one
# Dockerfile COPY line serves both.
#
# Prerequisites: `mise install` and `bun install --frozen-lockfile`.
# VANTIGO_VERSION stamps the binary (default "dev").
set -euo pipefail
cd "$(dirname "$0")/.."

trap 'bash scripts/restore-embed-overlay.sh' EXIT
bash scripts/spa-embed-overlay.sh

VERSION="${VANTIGO_VERSION:-dev}"
echo "==> server binaries ($VERSION)"
rm -rf dist/server
for arch in amd64 arm64; do
  mkdir -p "dist/server/linux/$arch"
  (cd apps/server && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
    -ldflags="-s -w -X github.com/vantigo-io/vantigo/server/internal/buildinfo.Version=$VERSION" \
    -o "../../dist/server/linux/$arch/vantigo" ./cmd/vantigo)
  echo "    dist/server/linux/$arch/vantigo"
done
ls -lh dist/server/linux/*/
