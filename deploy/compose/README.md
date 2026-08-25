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

Create local configuration and set a database password:

```bash
cp .env.example .env
cp vantigo.env.example vantigo.env
```

Start the stack:

```bash
docker compose up -d
```

Compose starts PostgreSQL, runs the single application's database migrations as
a one-shot job, and then starts Vantigo:

- **Vantigo** — <http://localhost:8080>
- **API reference** — <http://localhost:8080/openapi/v1.json>

The Customers, Communications and Products modules are enabled by default. They
share one PostgreSQL database named `vantigo`, with independent `identity`,
`customers`, `communications` and `products` schemas and migration histories.

## First sign-in

This Compose stack runs Vantigo outside Development, so `vantigo.env` must set
`Authentication__Bootstrap__Secret` before you first run `docker compose up -d`; the
API refuses to start without it rather than generating and logging one for you.
Generate a high-entropy value yourself, for example:

```bash
openssl rand -base64 32
```

Visit <http://localhost:8080/setup> to create the first Owner account, and enter
that same secret value. After the Owner account is created, remove or rotate the
bootstrap secret. Owners can invite further users from `/settings`.

## Configuration

| File | Purpose |
| --- | --- |
| `.env` | Image tag, PostgreSQL credentials and host port |
| `vantigo.env` | Application, identity, email, module and proxy settings |

The complete configuration reference is in
[docs/customers-authentication.md](../../docs/customers-authentication.md).
Tenancy modes and the database roles below are described in
[docs/tenancy.md](../../docs/tenancy.md). Data Protection key wrapping — why
`vantigo.env.example` ships with `DataProtection__AllowUnwrappedKeys=true`,
and how to move to an Azure Key Vault key instead — is described in
[docs/data-protection-key-wrapping.md](../../docs/data-protection-key-wrapping.md).
For startup-configured workforce OIDC, static SCIM provisioning, and recovery procedures, see
the [SSO and SCIM operations guide](../../docs/sso-scim-operations.md).
Pin a specific release with `VANTIGO_TAG=v1.2.3` in `.env`. For reproducible
production deployments, pin by digest instead of tag — tags are mutable,
digests are not:

```dotenv
VANTIGO_TAG=v1.2.3@sha256:<digest from the release>
```

Release images are cosign-signed; verify a digest before deploying it:

```bash
cosign verify ghcr.io/vantigo-io/vantigo@sha256:<digest> \
    --certificate-identity-regexp 'https://github.com/vantigo-io/vantigo/.*' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Database roles and tenant isolation

The stack creates two PostgreSQL roles on first initialization:

| Variable | Default | Role |
| --- | --- | --- |
| `POSTGRES_USER` | `vantigo` | Superuser. Owns every schema and table and runs the `vantigo-migrate` job. |
| `POSTGRES_APP_USER` | `vantigo_app` | `NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS`, table DML only, owns nothing. |

`VANTIGO_DB_USER` and `VANTIGO_DB_PASSWORD` choose the role the API connects as.
They **default to the runtime role**, so the tenant row-level security policies
apply to every application query: the application sets the session-scoped
`app.tenant_id` setting on each pooled connection it opens, and the policies
match rows against it. The `vantigo-migrate` job keeps running as the owner
role. Point `VANTIGO_DB_USER` at the owner role only for debugging — doing so
turns the database-level tenant isolation off. Details are in
[docs/tenancy.md](../../docs/tenancy.md).

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
-- the modules enabled in your installation).
GRANT USAGE ON SCHEMA identity, customers, communications, products, energy TO "vantigo_app";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity, customers, communications, products, energy TO "vantigo_app";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity, customers, communications, products, energy TO "vantigo_app";
```

Multi-tenant mode (`Tenancy__Mode=multi`) is not production-ready and refuses to
start outside Development. See [docs/tenancy.md](../../docs/tenancy.md).

## Background workers

The single-container default hosts the Communications background workers
(outbox, inbound, retention, attachment cleanup and scanning) inside the API
process. Deployments that scale the API horizontally should move them to one
dedicated worker container so every extra HTTP replica does not multiply the
pollers:

1. Set `Workers__InProcess=false` in `vantigo.env` (applies to the API).
2. Run one extra container from the same image with the `worker` command; it
   ignores `Workers__InProcess` and serves only `/health/live` and
   `/health/ready` for probes.

Whichever topology is used, retention cleanup takes an installation-wide
advisory lease, so it runs on exactly one instance per cycle.

On shutdown the host drains for up to 30 seconds
(`Host__ShutdownTimeoutSeconds`), enough for the longest single worker
operation (an SMTP send is capped at 20 s) to finish and commit. Give the
container platform a termination grace period **above** that value —
`stop_grace_period` in Compose, `terminationGracePeriodSeconds` on ACA —
or an in-flight send can be killed mid-way and resent after restart.

## Database connection budget

Each API replica caps its PostgreSQL pool at 25 connections unless the
connection string sets `Maximum Pool Size` explicitly (Npgsql's own default of
100 per replica exhausts a modest `max_connections` once you scale out). Size
the budget so that

```
replicas × Maximum Pool Size  ≤  max_connections − headroom (reserve ~10 for
                                 migrations, monitoring, and manual sessions)
```

Connect and command timeouts keep Npgsql's defaults (15 s / 30 s); override
them in the connection string (`Timeout`, `Command Timeout`) if your
environment needs different bounds. Migrations run without the pool cap, since
schema changes may legitimately run longer.

## Base path and reverse proxy

The whole application can be served below one configurable path prefix. Set
`App__BasePath` in `vantigo.env` and set `App__PublicOrigin` to the public
scheme and host. The API prefixes remain `/api/v1/identity`,
`/api/v1/customers`, `/api/v1/communications` and `/api/v1/products`.

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

With `App__BasePath=/vantigo`, generated invitation, password-reset and OIDC
callback URLs include that prefix. The base path is a runtime setting; the
single image serves the matching SPA without a rebuild.

## Email and observability

Identity invitations and password recovery use the `Email__*` settings. The
Communications module uses `Smtp__*` for its default mailbox delivery. Mailgun
mailboxes are configured through the Communications API, where the provider,
domain, region and API key are stored as protected mailbox credentials; there is
no service-to-service API-key environment variable.

Telemetry is off by default. Standard OTLP variables can be set in `vantigo.env`:

```dotenv
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
# OTEL_EXPORTER_OTLP_PROTOCOL=grpc
# OTEL_EXPORTER_OTLP_HEADERS=x-api-key=secret
```

## Production notes

- **Remove `Security__AllowInsecureTransport=true` from `vantigo.env`.** The
  quick-start stack ships with it because it serves `http://localhost:8080` and
  reaches the bundled PostgreSQL container with no certificate authority
  available. Left in place on a network you do not control, session cookies,
  invitation and password-reset bearer links, and database credentials all travel
  in the clear. See [transport security](../../docs/transport-security.md).
- Put the application behind a TLS-terminating reverse proxy and configure the
  `ForwardedHeaders__*` settings to trust exactly that proxy.
- Set `App__PublicOrigin` to the public `https://` origin; mailed links, the
  accepted `Host` header values and the static OIDC callback are all derived from
  it. The callback is fixed at `/api/v1/identity/oidc/callback`.
- Point `ConnectionStrings__vantigo` at a PostgreSQL server that presents a
  certificate and add `SSL Mode=VerifyFull`. `compose.yaml` sets this variable
  for both the `vantigo-migrate` and `vantigo` services, so it must be edited
  there rather than only in `vantigo.env`.
- The `vantigo.env.example` file documents the static OIDC and SCIM settings.
  Configuration is deployment-bound and changes require a restart. Do not put
  provider or SCIM secrets in source-controlled files.
- SCIM uses `/api/v1/identity/scim/v2`, bearer tokens, and
  `application/scim+json`. Set `Authentication__Scim__Enabled=true` and either
  `Authentication__Scim__BearerToken` or
  `Authentication__Scim__BearerTokenFile`. During rotation, optionally set
  `Authentication__Scim__PreviousBearerToken` or
  `Authentication__Scim__PreviousBearerTokenFile` together with
  `Authentication__Scim__PreviousBearerTokenExpiresAtUtc` (future and no more
  than 24 hours after startup).
- The Compose `postgres-data` volume contains the PostgreSQL-backed Data Protection
  keys as well as application data. `vantigo.env.example` ships with
  `DataProtection__AllowUnwrappedKeys=true` so the stack can start without an
  Azure Key Vault (Vantigo does not yet provision one), but that means the keys
  are stored **unwrapped**: anyone with the volume or a database dump has both
  the encrypted secrets and the keys to decrypt them. Back up the database
  before upgrades, keep one migration job only, and provision a Key Vault key
  and set `DataProtection__KeyVaultKeyUri` instead as soon as one is available
  — see [Data Protection key wrapping](../../docs/data-protection-key-wrapping.md).
- **Remove `Authentication__Owners__AllowInsecureNoMfa=true` from `vantigo.env`.**
  The quick-start stack ships with it because a fresh installation has no enrolled
  authenticator, and requiring MFA before one exists would leave no way to sign in
  at all. Left in place, a compromised Owner or SystemAdmin password alone is enough
  for full control of identity and the tenant control plane. Enrol an authenticator
  for every privileged account, then set `Authentication__Owners__RequireMfa=true`
  and drop this flag — see
  [customer authentication](../../docs/customers-authentication.md).
- Never run `seed` in production; it is Development-only.

The static identity cleanup migration is intentionally destructive for old
database-managed federation/SCIM configuration and non-static connection data.
Before deploying a release that contains it, confirm that the database has no
real use of that removed configuration/data, take and verify a PostgreSQL backup,
then let the one-shot `vantigo-migrate` service complete before starting the API.
The migration is forward-only; do not plan an application rollback across it.

## Upgrading

```bash
docker compose pull
docker compose up -d
```

The migration job runs before the application starts. For a controlled release,
run exactly one migration job, wait for successful completion, and then start the
API. The API does not apply migrations at startup.

For a complete backup, one-migrator, token rotation, and Owner break-glass runbook,
see [SSO and SCIM operations](../../docs/sso-scim-operations.md).
