#!/usr/bin/env bash
# Write the shared PostgreSQL server's self-signed TLS certificate to
# ./certs, next to this file, so tenants can connect with sslmode=verify-full.
#
# One certificate acts as its own authority: tenants mount server.crt as
# their sslrootcert and verify the server against it. It names both hosts
# the server is reached on — `postgres` from inside docker (the tenants)
# and 127.0.0.1 from the host (`vantigo psql`, backups) — so verify-full
# passes on either path.
#
# Run as root (or with sudo): the key must be owned by the postgres user
# inside the image (uid 999) and mode 0600, or the server refuses to load
# it. Re-running replaces the certificate; restart postgres afterwards, and
# nothing on the tenant side changes because they mount the same file.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
certs="$here/certs"
days="${CERT_DAYS:-3650}"
postgres_uid="${POSTGRES_UID:-999}"

mkdir -p "$certs"
umask 077
openssl req -x509 -newkey rsa:4096 -nodes -sha256 -days "$days" \
  -keyout "$certs/server.key" -out "$certs/server.crt" \
  -subj "/CN=postgres" \
  -addext "subjectAltName=DNS:postgres,DNS:localhost,IP:127.0.0.1" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,digitalSignature,keyEncipherment,keyCertSign" \
  -addext "extendedKeyUsage=serverAuth"

chown "$postgres_uid:$postgres_uid" "$certs/server.key"
chmod 0600 "$certs/server.key"
# The certificate is public: every tenant container (uid 65532) reads it.
chmod 0644 "$certs/server.crt"
chmod 0755 "$certs"

echo "wrote $certs/server.crt (valid $days days) and $certs/server.key"
echo "restart the server to load it: docker compose -f $here/compose.yaml up -d"
