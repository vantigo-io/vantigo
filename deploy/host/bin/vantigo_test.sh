#!/usr/bin/env bash
# Unit tests for the pure helpers in bin/vantigo — the ones whose bugs would
# corrupt a tenant's .env or its connection string. Sources the CLI (which
# then defines its functions without dispatching) and runs against a temp
# directory; needs no docker and no PostgreSQL.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

VANTIGO_SRV_DIR="$work/srv"
export VANTIGO_SRV_DIR
# shellcheck disable=SC1091
source "$here/vantigo"

fails=0
check() {  # check <description> <expected> <actual>
  if [[ "$2" == "$3" ]]; then
    echo "ok   $1"
  else
    echo "FAIL $1" >&2
    echo "     expected: $2" >&2
    echo "     actual:   $3" >&2
    fails=$((fails + 1))
  fi
}

# --- set_env_var / unset_env_var / get_env_var ------------------------------

env="$work/.env"
printf 'A=1\n# B=commented\nB=old\nBB=keep\n' > "$env"
chmod 600 "$env"

set_env_var "$env" B "new"
check "replaces the first uncommented line and leaves the prefix-sibling alone" \
  $'A=1\n# B=commented\nB=new\nBB=keep' "$(cat "$env")"

set_env_var "$env" C "appended"
check "appends a key that is absent" "appended" "$(get_env_var "$env" C)"

url='postgresql://r:p+w/x@postgres:5432/db?sslmode=verify-full&sslrootcert=/certs/ca.crt&pool_max_conns=5'
set_env_var "$env" DATABASE_URL "$url"
check "keeps &, /, + and = in a connection string verbatim" "$url" "$(get_env_var "$env" DATABASE_URL)"

# shellcheck disable=SC2016  # the literal $ is the point
set_env_var "$env" S 'a|b\c$d'
# shellcheck disable=SC2016
check "keeps |, backslash and \$ in a value verbatim" 'a|b\c$d' "$(get_env_var "$env" S)"

check "preserves the file mode" "600" "$(stat -c %a "$env")"

unset_env_var "$env" B
check "unset comments the line out" "# B=new" "$(grep -m1 '^# B=new' "$env")"
check "unset leaves the prefix-sibling alone" "keep" "$(get_env_var "$env" BB)"
check "get returns empty for a commented key" "" "$(get_env_var "$env" B)"

# --- names ----------------------------------------------------------------

check "db_name maps hyphens to underscores" "vantigo_acme_corp" "$(db_name acme-corp)"
check "tenant_dir is under the tenants dir" "$work/srv/tenants/acme" "$(tenant_dir acme)"

for bad in "Acme" "-acme" "acme_corp" "" "a.b"; do
  if (validate_name "$bad" 2>/dev/null); then
    check "validate_name rejects '$bad'" "rejected" "accepted"
  else
    check "validate_name rejects '$bad'" "rejected" "rejected"
  fi
done
(validate_name "acme-1") && check "validate_name accepts 'acme-1'" "ok" "ok"

# --- psql URL rewrite (the substitutions cmd_psql applies) -----------------

PG_SERVICE_HOST=postgres PGHOST=127.0.0.1 PGPORT=5432
PG_CA_CONTAINER_PATH=/certs/postgres-ca.crt PG_CA_HOST_PATH=/srv/postgres/certs/server.crt
in='postgresql://vantigo_acme:pw@postgres:5432/vantigo_acme?sslmode=verify-full&sslrootcert=/certs/postgres-ca.crt&pool_max_conns=5'
out="${in/@${PG_SERVICE_HOST}:5432/@${PGHOST}:${PGPORT}}"
out="${out/sslrootcert=${PG_CA_CONTAINER_PATH}/sslrootcert=${PG_CA_HOST_PATH}}"
check "psql rewrite targets the host port and the certificate's host path" \
  'postgresql://vantigo_acme:pw@127.0.0.1:5432/vantigo_acme?sslmode=verify-full&sslrootcert=/srv/postgres/certs/server.crt&pool_max_conns=5' \
  "$out"

# --- secrets --------------------------------------------------------------

pw="$(gen_password)"
check "gen_password is 32 URL-safe characters" "ok" "$([[ "$pw" =~ ^[A-Za-z0-9]{32}$ ]] && echo ok || echo "$pw")"
check "gen_secret decodes to 32 bytes" "32" "$(gen_secret | base64 -d | wc -c)"

# --- add refuses a bad APP_URL before touching anything -------------------

mkdir -p "$work/srv/_template"
: > "$work/srv/_template/compose.yaml"
echo 'DATABASE_URL=' > "$work/srv/_template/.env.example"
if (cmd_add acme --url 'http://acme.example.com' 2>/dev/null); then
  check "add rejects a non-https APP_URL" "rejected" "accepted"
else
  check "add rejects a non-https APP_URL" "rejected" "rejected"
fi
if (cmd_add acme --url 'https://acme.example.com/app' 2>/dev/null); then
  check "add rejects an APP_URL with a path" "rejected" "accepted"
else
  check "add rejects an APP_URL with a path" "rejected" "rejected"
fi
check "a rejected add creates no tenant directory" "absent" "$([[ -e "$work/srv/tenants/acme" ]] && echo present || echo absent)"

if (( fails )); then
  echo "$fails test(s) failed" >&2
  exit 1
fi
echo "all tests passed"
