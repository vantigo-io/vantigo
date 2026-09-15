# Run several tenants on one host

One VM, Docker Compose, one Vantigo instance per tenant. The tenant boundary is
the deployment itself: Vantigo has no multi-tenancy inside the application, so
every tenant gets its own container, database, role, data directory and
configuration, all created and retired by one command. Sized for a handful to a
few dozen tenants.

For a single deployment, [`deploy/compose`](../compose/README.md) is simpler.
This directory is the reference layout for the shared-host shape; copy it onto
the host and adjust the host-wide values, everything per tenant is generated.

## Layout on the host

```text
/srv/_template/compose.yaml    shared tenant compose file — symlinked, never copied
/srv/_template/.env.example    template for a tenant's .env
/srv/_template/host.env        optional host-wide settings for the CLI
/srv/_template/bin/vantigo     the CLI (symlink it to /usr/local/bin/vantigo)
/srv/tenants/<name>/           per tenant:
  compose.yaml                   -> /srv/_template/compose.yaml
  .env                           tenant config, mode 600, never in git
  data/                          bind-mounted persistent files (attachments)
/srv/postgres/                 the shared PostgreSQL 18 server
  compose.yaml, make-certs.sh, certs/, data/
/srv/backups/                  dumps, data tarballs, retired tenant directories
```

`template/`, `postgres/` and `bin/` in this directory map onto `/srv/_template`,
`/srv/postgres` and `/srv/_template/bin`.

## How a tenant runs

- **One container**, `api` mode, which applies pending migrations under an
  advisory lock and then serves. The tenant's role owns its database, so it has
  the DDL rights the migrations need. Background workers run in-process
  (`WORKERS_IN_PROCESS` defaults to 1): with one replica per tenant that is the
  right shape and halves the container count on the host.
- **No host port.** A cloudflared tunnel container on the shared `cloudflare`
  network routes `<name>.example → http://vantigo-<name>:8080`. Every tenant's
  service is called `vantigo`, so the tunnel targets the per-tenant network alias.
- **Forwarded headers are trusted only from the tunnel.** `TRUSTED_PROXY_HOPS=1`
  with `TRUSTED_PROXY_CIDRS` set to the `cloudflare` network's subnet, so the
  client address (rate limiting, audit) and the `https` scheme (HSTS, cookies)
  come from Cloudflare's headers and from nothing else. The tunnel preserves the
  `Host` header, which must match `APP_URL`, or the request is rejected with 400.
- **PostgreSQL over TLS, certificate-verified.** `DATABASE_URL` carries
  `sslmode=verify-full` against the shared server's certificate, mounted into
  the container. That is what lets a tenant run without
  `ALLOW_INSECURE_TRANSPORT`, which would otherwise also strip the `Secure`
  attribute from session cookies. The URL also carries `pool_max_conns`, so the
  host's connection budget is `tenants × pool size` instead of `tenants ×
  NumCPU`.
- **One database and one role per tenant**, both `vantigo_<name>` (hyphens
  become underscores). The role owns its database and nothing else, and
  `PUBLIC` loses `CONNECT`, so one tenant's credentials cannot reach another's
  database.
- **Persistent files under one mount**, `./data:/app/data`, with attachments at
  `/app/data/storage`. The image runs as uid 65532 and the storage driver refuses
  a group- or world-writable root, so `vantigo add` chowns the directory.
- **Nothing assumes it is alone on the host**: no container names, no host
  ports, no named volumes. Compose derives everything from
  `COMPOSE_PROJECT_NAME`.

## One-time host setup

1. Install Docker with the Compose plugin, `psql`/`pg_dump` (the `postgresql-client`
   package) and `openssl`. Log in to the image registry once, with a GitHub token
   that has `read:packages`:

   ```bash
   docker login ghcr.io
   ```

2. Copy this directory into place and link the CLI:

   ```bash
   cp -r template /srv/_template && cp -r postgres /srv/postgres
   mkdir -p /srv/_template/bin && cp bin/vantigo /srv/_template/bin/
   ln -s /srv/_template/bin/vantigo /usr/local/bin/vantigo
   ```

3. Create the tunnel network with a fixed subnet, and run cloudflared on it. The
   subnet is what tenants trust forwarded headers from; the CLI reads it off the
   network when it creates a tenant.

   ```bash
   docker network create --subnet 172.30.1.0/24 cloudflare
   ```

4. Start the shared PostgreSQL server with TLS:

   ```bash
   cd /srv/postgres
   echo "POSTGRES_PASSWORD=$(openssl rand -base64 24)" > .env && chmod 600 .env
   ./make-certs.sh
   docker compose up -d --wait
   ```

   `make-certs.sh` writes a self-signed certificate naming both `postgres` (the
   docker hostname tenants use) and `127.0.0.1` (the host port admin tooling
   uses), so `verify-full` passes on either path. It creates the `postgres`
   network tenants attach to.

   Put the admin password where the CLI finds it, either `~/.pgpass` with a line
   `127.0.0.1:5432:*:postgres:<password>` (mode 600) or `PGPASSWORD` exported.

5. Set the host-wide values in `/srv/_template/.env.example`: the SMTP relay
   every tenant sends identity mail through, and anything else all tenants share.
   The relay must resolve to a public address, since the SMTP guard refuses
   private, loopback and link-local destinations; a relay container on this host
   will not work.

6. Optionally, write `/srv/_template/host.env` so `vantigo add` needs no flags:

   ```dotenv
   VANTIGO_DOMAIN=example.com          # tenants default to https://<name>.example.com
   # VANTIGO_POOL_MAX_CONNS=5          # per tenant; postgres max_connections is 200
   # TRUSTED_PROXY_CIDRS=172.30.1.0/24 # pin instead of reading the docker network
   ```

## Operations

```text
vantigo add <name> [--url URL] [--modules LIST] [--tag TAG] [--no-start]
vantigo harden <name>
vantigo remove <name> [--yes] [--keep-data]
vantigo list
vantigo up|down|restart|ps|logs <name> [...]
vantigo upgrade <name>|--all [--tag TAG] [--no-backup]
vantigo backup <name>|--all
vantigo psql <name>
```

**Adding a tenant.** `vantigo add acme` creates the directory, the database and
role, and a `.env` with generated `APP_SECRET` and `BOOTSTRAP_SECRET`,
`APP_URL`, the trusted proxy subnet and the connection string. It then starts
the container, waits for it to become healthy, and prints the URL, the tunnel
ingress line to add to cloudflared, and the bootstrap secret. Open
`https://acme.example.com/setup`, enter the secret, create the Owner account,
and enrol MFA for it from `/settings`.

**Hardening.** `vantigo harden acme` requires MFA for Owners and SystemAdmins
and rotates the bootstrap secret. The secret is required at startup outside
development, so it is replaced rather than removed. Run it right after the Owner
has enrolled an authenticator; before that, the flip would lock the Owner out,
which is why a fresh tenant starts with `OWNERS_ALLOW_INSECURE_NO_MFA=1`.

**Upgrading.** Image updates are explicit; there is no auto-updater. `vantigo
upgrade --all --tag 0.16.4` handles one tenant at a time: dump the database,
pull, recreate, wait for healthy, and stop at the first failure so a bad release
reaches one tenant rather than all of them. Migrations are forward-only and run
at startup, so the dump taken just before is the rollback. Image tags carry no
leading `v` (`0.16.4`, not `v0.16.4`). PostgreSQL is never auto-updated; major
upgrades are manual with `pg_upgrade`.

**Backups.** `vantigo backup --all` writes a `pg_dump` custom-format archive plus
a tarball of `data/` and `.env` per tenant under `/srv/backups`. The `.env` is
included on purpose: `APP_SECRET` is the key every stored TOTP secret is
encrypted under, so a restore without it produces a database no MFA-enrolled
user can sign in to. Ship `/srv/backups` off the host.

**Removing.** `vantigo remove acme` backs up first, stops the container, drops the
database and role, and deletes the directory. `--keep-data` moves the directory
to `/srv/backups` and leaves the database in place instead.

**Config changes.** Edit `/srv/tenants/<name>/.env` and run `vantigo up <name>`;
Compose recreates the container when its environment changed. A literal `$` in a
value must be written `$$`, since Compose interpolates the file.

## Sizing

Each tenant's pool is capped by `pool_max_conns` on its connection string (5 by
default) and the shared server allows 200 connections, which covers 20 tenants
with headroom for migrations, backups and admin sessions. Raise the cap for a
busy tenant in its `.env` and lower `max_connections` pressure by keeping idle
tenants down. Memory is capped per tenant by `VANTIGO_MEM_LIMIT` (1 GiB by
default). Container logs rotate at five 20 MiB files per tenant.
