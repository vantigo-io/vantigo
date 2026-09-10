#!/usr/bin/env bash
# Builds the image for linux/amd64 and linux/arm64 with buildx.
#
#   bash scripts/build-image.sh                                   # verify only
#   TAG=ghcr.io/vantigo-io/vantigo:dev PUSH=1 bash scripts/build-image.sh
#
# Binaries are compiled natively first (scripts/build-artifacts.sh; skipped
# with SKIP_ARTIFACTS=1 when dist/server is already populated). A
# multi-platform result is a manifest list the local image store cannot hold,
# so without PUSH=1 the build is verified and discarded. For single-arch
# iteration use `docker build -t vantigo:dev .` after build-artifacts.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

TAG="${TAG:-vantigo:dev}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"

if [ "${SKIP_ARTIFACTS:-0}" != "1" ] || [ ! -e dist/server/linux/amd64/vantigo ]; then
  bash scripts/build-artifacts.sh
fi

# Only the docker-container driver builds several platforms in one invocation.
BUILDER="${BUILDER:-vantigo}"
if ! docker buildx inspect "$BUILDER" >/dev/null 2>&1; then
  echo "==> creating buildx builder '$BUILDER' (docker-container driver)"
  docker buildx create --name "$BUILDER" --driver docker-container >/dev/null
fi

if [ "${PUSH:-0}" = "1" ]; then
  output=(--push)
  echo "==> building $TAG for $PLATFORMS and pushing"
else
  output=(--output=type=cacheonly)
  echo "==> building $TAG for $PLATFORMS (verify only; PUSH=1 publishes)"
fi

docker buildx build --builder "$BUILDER" --platform "$PLATFORMS" --tag "$TAG" "${output[@]}" .
