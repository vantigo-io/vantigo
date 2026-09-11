#!/usr/bin/env bash
# Smoke-tests a built image end to end. `go test` exercises source; this is
# the only check that runs the linked binary inside the shipped image:
# migrations apply from the image, `api` serves the real SPA, health, the API
# catch-all and the security middleware, the container HEALTHCHECK turns
# healthy, and `worker` starts. It needs no fixtures.
#
#   bash scripts/smoke-image.sh vantigo:dev
#   SMOKE_EXPECT_VERSION=0.11.0-pr.7.abc1234 bash scripts/smoke-image.sh vantigo:ci
set -euo pipefail

IMAGE="${1:?usage: scripts/smoke-image.sh <image>}"
APP_PORT="${SMOKE_APP_PORT:-18080}"
WORKER_PORT="${SMOKE_WORKER_PORT:-18081}"
ID="$$"
NET="vantigo-smoke-$ID"
PG="vantigo-smoke-pg-$ID"
APP="vantigo-smoke-app-$ID"
WORKER="vantigo-smoke-worker-$ID"
TMP="$(mktemp -d)"

cleanup() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    for c in "$APP" "$WORKER"; do
      echo "--- logs: $c"
      docker logs "$c" 2>&1 | tail -n 40 || true
    done
  fi
  docker rm -f "$APP" "$WORKER" "$PG" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$TMP"
  exit "$status"
}
trap cleanup EXIT

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }
pass() { echo "ok   $*"; }

wait_for() {
  for _ in $(seq 1 30); do
    curl -fsS -o /dev/null "$1" 2>/dev/null && return 0
    sleep 1
  done
  fail "$1 never answered"
}

# status prints only the HTTP status code of a curl request.
status() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

# --- the image itself --------------------------------------------------------

user="$(docker image inspect --format '{{.Config.User}}' "$IMAGE")"
case "$user" in "" | root | 0 | 0:*) fail "image User is '$user'; it must be non-root" ;; esac
pass "runs as non-root user $user"

set +e
docker run --rm "$IMAGE" bogus >/dev/null 2>&1
code=$?
set -e
[ "$code" -eq 2 ] || fail "an unknown command exited $code, want 2"
pass "an unknown command exits 2"

# --- a database --------------------------------------------------------------

docker network create "$NET" >/dev/null
docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_USER=vantigo -e POSTGRES_PASSWORD=vantigo -e POSTGRES_DB=vantigo \
  postgres:18-alpine >/dev/null
# -h forces TCP: the unix socket accepts during initdb's first start, before TCP does.
for _ in $(seq 1 30); do
  docker exec "$PG" pg_isready -h 127.0.0.1 -U vantigo -d vantigo >/dev/null 2>&1 && break
  sleep 1
done

env_args=(
  -e "DATABASE_URL=postgres://vantigo:vantigo@$PG:5432/vantigo"
  -e "APP_URL=http://localhost:$APP_PORT"
  -e ALLOW_INSECURE_TRANSPORT=1
  # Presence only: this run is production mode and never sends mail or
  # exercises bootstrap, but config.Load requires all of these to start.
  -e APP_SECRET=smoke-test-app-secret-at-least-32-bytes-long
  -e BOOTSTRAP_SECRET=smoke-test-bootstrap-secret
  -e SMTP_HOST=smtp.example.invalid
  -e SMTP_FROM=noreply@example.invalid
)

# --- migrate -------------------------------------------------------------------

docker run --rm --network "$NET" "${env_args[@]}" "$IMAGE" migrate >/dev/null
docker exec "$PG" psql -U vantigo -d vantigo -tAc "SELECT to_regclass('platform.rate_limit') IS NOT NULL" | grep -qx t ||
  fail "migrate did not create platform.rate_limit"
pass "migrate applies the schema"

# --- api -------------------------------------------------------------------------

docker run -d --name "$APP" --network "$NET" -p "127.0.0.1:$APP_PORT:8080" "${env_args[@]}" "$IMAGE" api >/dev/null
base="http://localhost:$APP_PORT"
wait_for "$base/health/live"

curl -fsS "$base/health/ready" >"$TMP/ready" || fail "/health/ready is not 200"
grep -q '"status":"healthy"' "$TMP/ready" || fail "/health/ready: $(cat "$TMP/ready")"
if [ -n "${SMOKE_EXPECT_VERSION:-}" ]; then
  grep -q "\"version\":\"$SMOKE_EXPECT_VERSION\"" "$TMP/ready" || fail "version is not $SMOKE_EXPECT_VERSION: $(cat "$TMP/ready")"
fi
pass "health $(cat "$TMP/ready")"

curl -fsS -D "$TMP/headers" -o "$TMP/index" "$base/customers"
grep -q 'window.__VANTIGO_APP__' "$TMP/index" || fail "the SPA index has no runtime config"
if grep -q 'built without the frontend' "$TMP/index"; then fail "the image embeds the placeholder index.html"; fi
grep -qi "^content-security-policy: .*'sha256-" "$TMP/headers" || fail "no CSP carrying the script hash"
pass "SPA deep link with runtime config and CSP"

asset="$(grep -o 'src="/assets/[^"]*\.js"' "$TMP/index" | head -n1 | cut -d'"' -f2)"
[ -n "$asset" ] || fail "no module script in the index"
curl -fsS -D "$TMP/asset-headers" -o /dev/null "$base$asset"
grep -qi '^cache-control: public, max-age=31536000, immutable' "$TMP/asset-headers" || fail "$asset is not cached as immutable"
pass "hashed asset $asset"

[ "$(curl -s -D "$TMP/api-headers" -o /dev/null -w '%{http_code}' "$base/api/v1/does-not-exist")" = 404 ] ||
  fail "an unknown API path is not 404"
grep -qi '^content-type: application/problem+json' "$TMP/api-headers" || fail "an unknown API path is not a problem document"
pass "API catch-all answers a 404 problem"

[ "$(status -X POST -H 'Origin: https://evil.example' -H 'Sec-Fetch-Site: cross-site' "$base/api/v1/anything")" = 403 ] ||
  fail "a cross-site POST was not rejected"
pass "cross-site POST rejected"

[ "$(status -H 'Host: evil.example' "$base/")" = 400 ] || fail "a foreign Host header was not rejected"
pass "foreign Host rejected"

[ "$(status "$base/api/v1/identity/system/status")" = 200 ] || fail "identity system/status is not 200"
curl -fsS "$base/api/v1/identity/system/status" | grep -q '"maintenance":false' || fail "identity system/status has no maintenance:false"
pass "identity system/status"

[ "$(status "$base/api/v1/identity/session")" = 401 ] || fail "identity session is not 401 without a cookie"
curl -s "$base/api/v1/identity/session" | grep -q unauthenticated || fail "identity session 401 body has no unauthenticated"
pass "identity session demands authentication"

[ "$(status "$base/api/v1/identity/bootstrap-status")" = 200 ] || fail "identity bootstrap-status is not 200"
curl -fsS "$base/api/v1/identity/bootstrap-status" | grep -q '"available":true' || fail "identity bootstrap-status has no available:true"
pass "identity bootstrap-status is available"

docker exec "$APP" /app/vantigo healthcheck || fail "the healthcheck command failed inside the container"
health=""
for _ in $(seq 1 30); do
  health="$(docker inspect --format '{{.State.Health.Status}}' "$APP")"
  [ "$health" = healthy ] && break
  sleep 1
done
[ "$health" = healthy ] || fail "container health is '$health'"
pass "container HEALTHCHECK is healthy"

# --- worker ------------------------------------------------------------------------

docker run -d --name "$WORKER" --network "$NET" -p "127.0.0.1:$WORKER_PORT:8080" "${env_args[@]}" "$IMAGE" worker >/dev/null
wait_for "http://localhost:$WORKER_PORT/health/ready"
[ "$(status "http://localhost:$WORKER_PORT/")" = 404 ] || fail "worker serves more than health"
pass "worker serves health only"

echo "smoke test passed: $IMAGE"
