# Run Vantigo with Docker Compose

Run the pre-built Vantigo container image from
[GHCR](https://github.com/orgs/vantigo-io/packages). Only Docker (or a
Compose-compatible runtime) is required.

## Quick start

Download the files in this folder, or grab them directly:

```bash
mkdir vantigo && cd vantigo
base=https://raw.githubusercontent.com/vantigo-io/vantigo/main/deploy/compose
curl -fsSLO "$base/compose.yaml"
curl -fsSLO "$base/.env.example"
curl -fsSLO "$base/vantigo.env.example"
```

Create the two local configuration files:

```bash
cp .env.example .env
cp vantigo.env.example vantigo.env
```

Then edit them. The application runs outside development and its configuration
is fail-closed — it reports every missing value at once and refuses to start,
and because the `vantigo-migrate` job loads the same configuration, a missing
value stops the whole stack rather than just the API. Before the first
`docker compose up -d`:

In `.env`, set the database passwords:

- `POSTGRES_PASSWORD` — the owner role that runs migrations.
- `POSTGRES_APP_PASSWORD` and `VANTIGO_DB_PASSWORD` — the least-privilege
  runtime role the API connects as. Both default to `change-me-too`; they must
  agree, because the first creates the role and the second authenticates as it.

In `vantigo.env`, set `APP_SECRET`: at least 32 bytes, generated with
`openssl rand -base64 32`, never generated or logged for you. Every key this
process uses (CSRF tokens, cookie signing, TOTP secret encryption) is derived
from it.

`vantigo.env` also ships `SMTP_HOST` and `SMTP_FROM` already set to
placeholders. Leave them in place to start the stack — outside development the
mail driver defaults to `smtp`, which makes both mandatory *for startup*, not
merely for sending — but replace them with a real relay before anyone else
uses the deployment. Until you do, the stack runs normally and delivers no
mail, so no user can be invited and no password can be recovered.

Start the stack:

```bash
docker compose up -d
```

Compose starts PostgreSQL, runs the single application's database migrations as
a one-shot job, and then starts Vantigo:

- **Vantigo** — <http://localhost:8080>
- **API contract** — `GET http://localhost:8080/api/openapi.json` (requires a session)

The Customers, Products, Energy, Communications, Projects, Time and Expenses
modules are enabled by default (`MODULES` in `vantigo.env`). They share one
PostgreSQL database named `vantigo`, with independent `identity`, `customers`,
`products`, `energy`, `communications`, `projects`, `time` and `expenses`
schemas and migration histories.

## First sign-in

Visit <http://localhost:8080/setup> to create the first Owner account. It is the
installation's full administrator — Owner and SystemAdmin — and no secret guards
the page: whoever completes setup first owns the installation, so do it before
the address is reachable by anyone else. Setup closes once the Owner exists, and
the API logs a warning at every start until then. Owners can invite further
users from `/settings`.

If the stack will be reachable before an operator can complete `/setup`, set
`BOOTSTRAP_OWNER_EMAIL` in `vantigo.env` instead: `/setup` is closed from the
start, and that address is mailed an Owner invitation once; if it expires
unused, the next start issues a fresh one. See
[the management listener](../../docs/management.md).

## Configuration

| File | Purpose |
| --- | --- |
| `.env` | Image tag, PostgreSQL credentials and host port |
| `vantigo.env` | Application, identity, email, module and proxy settings |

Every key `vantigo.env` accepts is documented in
`apps/server/internal/config/config.go`'s field comments — the authoritative
reference — and summarized in
[docs/customers-authentication.md](../../docs/customers-authentication.md).
For startup-configured workforce OIDC, static SCIM provisioning, and recovery
procedures, see the
[SSO and SCIM operations guide](../../docs/sso-scim-operations.md).

Pin a specific release with `VANTIGO_TAG=0.16.1` in `.env` — published image
tags drop the `v` that git release tags keep (`v0.16.1` the git tag,
`0.16.1` the image tag; `latest` is unaffected). For reproducible production
deployments, pin by digest instead of tag — tags are mutable, digests are
not:

```dotenv
VANTIGO_TAG=0.16.1@sha256:<digest from the release>
```

Release images are cosign-signed; verify a digest before deploying it:

```bash
cosign verify ghcr.io/vantigo-io/vantigo@sha256:<digest> \
    --certificate-identity-regexp 'https://github.com/vantigo-io/vantigo/.*' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Database roles

The stack creates two PostgreSQL roles on first initialization:

| Variable | Default | Role |
| --- | --- | --- |
| `POSTGRES_USER` | `vantigo` | Superuser. Owns every schema and table and runs the `vantigo-migrate` job. |
| `POSTGRES_APP_USER` | `vantigo_app` | `NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS`, table DML only, owns nothing. |

`VANTIGO_DB_USER` and `VANTIGO_DB_PASSWORD` choose the role the API connects
as (`compose.yaml` builds `DATABASE_URL` from them). They **default to the
runtime role** — plain defense in depth, so that a compromised API process
cannot alter the schema, create objects, or otherwise act as the database
owner. This is a single-tenant application: there is no `tenant_id` column
and no row-level security policy anywhere in the schema, so unlike the
retired .NET host's compose file, this is not a tenant-isolation boundary,
only a privilege-separation one. Point `VANTIGO_DB_USER` at the owner role
only for debugging.

`api` mode also re-checks migrations itself before it starts serving
(`cmd/vantigo/main.go`, `case modeAPI:`), but that check runs as the same
least-privilege `DATABASE_URL` — the `vantigo` service never gets the owner
credential. It only needs read access to conclude there is nothing to do:
goose's `tryEnsureVersionTable` (the library `internal/db` uses) probes for
`goose_db_version` with a plain `SELECT ... FROM pg_tables`, returns
immediately when it already exists, and never issues `CREATE TABLE`. The
one case that would need DDL — starting with migrations genuinely pending —
never reaches `api` at all: `vantigo-migrate` runs first, under the owner
role, and `vantigo`'s `depends_on: condition: service_completed_successfully`
keeps the API container from starting until it succeeds, upgrades included.

The role statements run from the PostgreSQL init script, which executes **only
when the `postgres-data` volume is created**. An existing installation can add
the role by hand:

```sql
\connect vantigo

CREATE ROLE "vantigo_app"
    LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT
    PASSWORD '<password>';

GRANT CONNECT ON DATABASE vantigo TO "vantigo_app";

ALTER DEFAULT PRIVILEGES FOR ROLE "vantigo" GRANT USAGE ON SCHEMAS TO "vantigo_app";
ALTER DEFAULT PRIVILEGES FOR ROLE "vantigo" GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "vantigo_app";
ALTER DEFAULT PRIVILEGES FOR ROLE "vantigo" GRANT USAGE, SELECT ON SEQUENCES TO "vantigo_app";

-- Default privileges only cover objects created afterwards; grant on the
-- schemas the enabled modules already created (add/remove schemas to match
-- the MODULES enabled in your installation).
GRANT USAGE ON SCHEMA identity, customers, products, energy, communications, projects, time, expenses TO "vantigo_app";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses TO "vantigo_app";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses TO "vantigo_app";
```

## Background workers

The single-container default hosts the Communications background workers
(outbox delivery, retention, attachment cleanup) inside the API process
(`WORKERS_IN_PROCESS=1`, the default). Deployments that scale the API
horizontally should move them to one dedicated worker container so every
extra HTTP replica does not multiply the pollers:

1. Set `WORKERS_IN_PROCESS=0` in `vantigo.env` (applies to the API replicas).
2. Run one extra container from the same image with the `worker` command; it
   always runs the workers regardless of `WORKERS_IN_PROCESS` and serves only
   `/health/live` and `/health/ready` for probes.

Whichever topology is used, retention cleanup takes a PostgreSQL advisory
lock, so it runs on exactly one instance per cycle.

On shutdown the process drains for up to `SHUTDOWN_TIMEOUT` (30 seconds by
default), enough for the longest single worker operation to finish and
commit. `compose.yaml` sets `stop_grace_period: 35s` on the `vantigo`
service so Compose's SIGKILL never arrives before that drain can complete —
whatever you set `SHUTDOWN_TIMEOUT` to, keep `stop_grace_period` (Compose) or
`terminationGracePeriodSeconds` (Kubernetes/ACA) comfortably above it, or an
in-flight operation can be killed mid-way and retried after restart.

## Health probes

The image has no shell, so the container `HEALTHCHECK` execs the binary
itself (`/app/vantigo healthcheck`) rather than running a `CMD-SHELL` curl or
wget one-liner. Keep any probe you configure — Compose's `healthcheck.test`,
a Kubernetes liveness/readiness probe, anything else — in that same exec
form. An HTTP-style probe that connects directly (rather than execing inside
the container) sends its own address as the `Host` header, and
`internal/security/hostfilter.go` only accepts `APP_URL`'s host plus
loopback (`localhost`, `127.0.0.1`, `::1`); every other `Host` is rejected
with 400, so such a probe always fails.

## Database connection budget

Each API replica's PostgreSQL pool defaults to `max(4, NumCPU)` connections
(`apps/server/internal/db/db.go`), which ties pool size to the host the
replica happens to land on. Pin it explicitly with `pool_max_conns` on
`DATABASE_URL` in `compose.yaml` instead of relying on that default,
especially once you run more than one replica. Size the budget so that

```
replicas × pool size  ≤  max_connections − headroom (reserve ~10 for
                          migrations, monitoring, and manual sessions)
```

Migrations run without the pool cap, since schema changes may legitimately
run longer.

## Base path and reverse proxy

The whole application can be served below one configurable path prefix. Set
`APP_BASE_PATH` in `vantigo.env` and set `APP_URL` to the public scheme and
host (no path — the path prefix is `APP_BASE_PATH`, not part of `APP_URL`).
The API prefixes remain `/api/v1/identity`, `/api/v1/customers`,
`/api/v1/products`, `/api/v1/energy`, `/api/v1/communications`,
`/api/v1/projects`, `/api/v1/time` and `/api/v1/expenses`.

For example, with nginx:

```nginx
server {
    listen 443 ssl;
    server_name vantigo.example.com;

    location /vantigo/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

With `APP_BASE_PATH=/vantigo`, generated invitation, password-reset and OIDC
callback URLs include that prefix. The base path is a runtime setting; the
single image serves the matching SPA without a rebuild. Trust exactly the
proxy in front of you with `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS`, or
the forwarded scheme/host/client-address headers are ignored.

## Email and observability

Identity invitations and password recovery use the `MAIL_DRIVER`/`SMTP_*`
settings. Per-mailbox delivery credentials for the Communications module are
configured through the Communications API, where the host, port and a protected
password are stored as mailbox credentials — there is no environment variable
for them. Those channels are **SMTP-only**: the API refuses to create or update
a channel naming any other provider, and rejects a Mailgun credential outright.
See [communications](../../docs/communications.md). The Projects and Time
modules need no environment variable of their own; see
[projects](../../docs/projects.md) and [time](../../docs/time.md). **Expenses
needs the object store configured** (`STORAGE_PROVIDER` in `vantigo.env.example`,
the same setting Communications attachments use) the moment anybody tries to
attach a receipt, whether or not the receipt rule requires one: unconfigured, a
receipt upload or download answers 503 rather than the process failing to start.
See [expenses](../../docs/expenses.md).

Telemetry is off by default. Standard OTLP variables can be set in
`vantigo.env`:

```dotenv
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
# OTEL_EXPORTER_OTLP_PROTOCOL=grpc
# OTEL_EXPORTER_OTLP_HEADERS=x-api-key=secret
```

## Production notes

- Put the application behind a TLS-terminating reverse proxy and configure
  `TRUSTED_PROXY_HOPS`/`TRUSTED_PROXY_CIDRS` to trust exactly that proxy.
  The quick-start stack serves `http://localhost:8080`; on a network you do
  not control, an http origin sends session cookies and the invitation and
  password-reset bearer links in the clear. See
  [transport security](../../docs/transport-security.md).
- Set `APP_URL` to the public `https://` origin; mailed links, the accepted
  `Host` header values, the cookie `Secure` attribute and the static OIDC
  callback are all derived from it. The callback is fixed at
  `/api/v1/identity/oidc/callback`.
- The bundled PostgreSQL is reached over the private compose network with no
  certificate authority, so `DATABASE_URL` carries no `sslmode` and pgx
  connects the way libpq's `prefer` does. If you point the stack at a
  PostgreSQL across a network instead, add `sslmode=verify-full` (or
  `verify-ca` when the server certificate does not name the host) on both
  the `vantigo-migrate` and `vantigo` services — edit `compose.yaml`, not
  `vantigo.env`, since `compose.yaml` is what builds the connection URL.
- The `vantigo.env.example` file documents the static OIDC and SCIM
  settings. Configuration is deployment-bound and changes require a restart.
  Do not put provider or SCIM secrets in source-controlled files — inject
  `OIDC_CLIENT_SECRET`, `SCIM_TOKEN`, `SCIM_PREVIOUS_TOKEN` and
  `COMMUNICATIONS_AI_API_KEY` from a secret store instead.
- SCIM uses `/api/v1/identity/scim/v2`, bearer tokens, and
  `application/scim+json`. Set `SCIM_TOKEN`; during rotation, optionally set
  `SCIM_PREVIOUS_TOKEN` together with `SCIM_PREVIOUS_TOKEN_EXPIRES_AT`
  (future, and no more than 24 hours after startup).
- `MANAGEMENT_PORT` and `MANAGEMENT_TOKEN` enable a private status endpoint
  for a control plane ([docs/management.md](../../docs/management.md)). The
  Compose stack deliberately does **not** publish that port: reach it from
  another container on the Compose network, never from the host's public
  interface.
- **Remove `OWNERS_REQUIRE_MFA=0` and `OWNERS_ALLOW_INSECURE_NO_MFA=1` from
  `vantigo.env`.** The quick-start stack ships with both because a fresh
  installation has no enrolled authenticator, and with the requirement on an
  administrator is held at `/settings/security` until one is enrolled. The
  `=0` is what turns the requirement off; the acknowledgement alone does not,
  it only lets the API start with it off. Left in place, a compromised Owner
  or SystemAdmin password alone is enough for full control of identity and
  the tenant control plane. Enroll an authenticator for every privileged
  account, then set `OWNERS_REQUIRE_MFA=1` and drop the acknowledgement — see
  [customer authentication](../../docs/customers-authentication.md).
- Never run `seed` in production; it is development-only (`APP_ENV=development`).
- `APP_SECRET` derives every encryption key this process uses (CSRF tokens,
  cookie signing, TOTP secret encryption) via HKDF-SHA256 — there is no
  external key vault to provision and nothing to wrap. Generate it once with
  `openssl rand -base64 32`, store it in a secret manager, and treat losing
  it the same as losing a signing key: every open session and every stored
  TOTP secret becomes unrecoverable.

`api` mode applies pending migrations before it starts serving, in addition
to the one-shot `vantigo-migrate` job this stack runs first. For a
controlled release, still run exactly one migration job, wait for successful
completion, and then start the API — the point is to see a migration failure
before any API replica starts, not to skip `api`'s own startup check. Take
and verify a PostgreSQL backup before deploying a release that contains a
destructive migration. Migrations are forward-only; do not plan an
application rollback across one.

## Upgrading

```bash
docker compose pull
docker compose up -d
```

For a complete backup, one-migrator, token rotation, and Owner break-glass runbook,
see [SSO and SCIM operations](../../docs/sso-scim-operations.md).
