---
title: Installation
description: Running Vantigo with Docker Compose, from the first start to production and upgrades.
sidebar:
  order: 10
sources:
  - deploy
  - Dockerfile
  - apps/server/cmd/vantigo
---

Vantigo ships as one container image and runs against one PostgreSQL database. The
image holds a single Go binary with the React front end embedded in it; which business
modules a deployment serves is chosen with the `MODULES` setting, and identity
(accounts, sign-in, MFA, RBAC, OIDC and SCIM) is always part of the application. This
page takes you from an empty directory to a running installation, then through what a
production deployment needs and how to upgrade.

The ready-made stack lives in
[deploy/compose](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose) in the
repository and runs the pre-built image from
[GHCR](https://github.com/orgs/vantigo-io/packages).

## What you need

- Docker with the Compose plugin, or a Compose-compatible runtime. Nothing else: no
  toolchain and no build tools, since the image is pre-built.
- A host with a free port for the application, `8080` by default.
- An `APP_SECRET` of at least 32 bytes, which you generate yourself
  (`openssl rand -base64 32`). The application never generates or logs one for you.
- An SMTP relay before anyone but you uses the installation. Invitations and password
  recovery are delivered as mail, and the stack starts with placeholder values that
  deliver nothing.

## Quick start

1. Download the three files that make up the stack:

   ```bash
   mkdir vantigo && cd vantigo
   base=https://raw.githubusercontent.com/vantigo-io/vantigo/main/deploy/compose
   curl -fsSLO "$base/compose.yaml"
   curl -fsSLO "$base/.env.example"
   curl -fsSLO "$base/vantigo.env.example"
   ```

2. Create the two local configuration files from the examples:

   ```bash
   cp .env.example .env
   cp vantigo.env.example vantigo.env
   ```

3. Edit them. The application runs outside development and its configuration is
   fail-closed: it reports every missing value at once and refuses to start. Because
   the `vantigo-migrate` job loads the same configuration, a missing value stops the
   whole stack rather than just the API. Before the first start:

   - In `.env`, set the database passwords. `POSTGRES_PASSWORD` is the owner role that
     runs migrations. `POSTGRES_APP_PASSWORD` and `VANTIGO_DB_PASSWORD` are the
     least-privilege runtime role the API connects as; both default to
     `change-me-too` and must agree, because the first creates the role and the
     second authenticates as it.
   - In `vantigo.env`, set `APP_SECRET` to at least 32 bytes generated with
     `openssl rand -base64 32`. Every key the process uses (CSRF tokens, cookie
     signing, TOTP secret encryption) is derived from it.
   - `vantigo.env` ships `SMTP_HOST` and `SMTP_FROM` set to placeholders. Leave them
     in place to start the stack — outside development the mail driver defaults to
     `smtp`, which makes both mandatory for startup, not merely for sending — but
     replace them with a real relay before anyone else uses the deployment. Until you
     do, the stack runs normally and delivers no mail, so no user can be invited and
     no password can be recovered.

4. Start the stack:

   ```bash
   docker compose up -d
   ```

Compose starts PostgreSQL, runs the application's database migrations as a one-shot
job, and then starts Vantigo:

- **Vantigo** — <http://localhost:8080>
- **API contract** — `GET http://localhost:8080/api/openapi.json` (requires a session)

The Customers, Products, Energy, Communications, Projects, Time, Expenses and Invoices
modules are enabled by default (`MODULES` in `vantigo.env`). They share one PostgreSQL
database named `vantigo`, with independent `identity`, `customers`, `products`,
`energy`, `communications`, `projects`, `time`, `expenses` and `invoices` schemas and
migration histories.

## What the stack contains

`compose.yaml` defines three services:

| Service | Image | What it does |
| --- | --- | --- |
| `postgres` | `postgres:18` | The database. On the first start, when the `postgres-data` volume is created, an init script creates the `vantigo` database and the least-privilege runtime role. |
| `vantigo-migrate` | `ghcr.io/vantigo-io/vantigo` | A one-shot job that runs the `migrate` command as the owner role and exits. It waits for `postgres` to be healthy and never restarts. |
| `vantigo` | `ghcr.io/vantigo-io/vantigo` | The application: the `api` command, published on `VANTIGO_PORT` (8080). It starts only once `vantigo-migrate` has completed successfully. |

The image is one binary with a dispatch table; the command is the first argument:

| Command | What it does | When to use it |
| --- | --- | --- |
| `api` | Applies migrations under an advisory lock, then serves the SPA, the API and health — plus every enabled module's background workers when `WORKERS_IN_PROCESS=1` (the default). | The single-container deployment the quick-start stack runs. |
| `server` | Serves only: never migrates and never runs workers, regardless of `WORKERS_IN_PROCESS`. | A fleet of stateless replicas behind a load balancer, with migrations and workers run elsewhere. |
| `worker` | Every enabled module's background workers, plus `/health/live` and `/health/ready` for probes. | One dedicated worker container when the API is scaled horizontally. |
| `migrate` | Applies migrations and exits 0 or 1. | The one-shot job before the application starts, on first start and on every upgrade. |
| `healthcheck` | Probes this container's own `/health/ready` on `127.0.0.1:$PORT` and exits 0 or 1. It constructs nothing, so a probe never fails on a bad `DATABASE_URL` — reporting that is `/health/ready`'s job. | The container `HEALTHCHECK` and any liveness or readiness probe. |

`seed` also exists but is development-only (`APP_ENV=development`; it exits 2 outside
it). No command, or an unknown one, prints usage on stderr and exits 2, so a typo in a
job definition fails loudly rather than quietly becoming a web server.

## First sign-in

Visit <http://localhost:8080/setup> to create the first Owner account. It is the
installation's full administrator — Owner and SystemAdmin — and no secret guards the
page: whoever completes setup first owns the installation, so do it before the address
is reachable by anyone else. Setup closes once the Owner exists, and the API logs a
warning at every start until then. Owners can invite further users from `/settings`.

If the stack will be reachable before an operator can complete `/setup`, set
`BOOTSTRAP_OWNER_EMAIL` in `vantigo.env` instead. `/setup` is then closed from the
start, and that address is mailed an Owner invitation once; if it expires unused, the
next start issues a fresh one. The account the invitation creates is the Owner and
SystemAdmin, exactly like `/setup`'s. This path needs a working SMTP relay, since the
invitation is delivered as mail. See the
[management listener](/en/admin/management-listener/) for the bootstrap state a control
plane can poll and the full invitation behaviour.

## Configuration

Settings live in two files next to `compose.yaml`:

| File | Purpose |
| --- | --- |
| `.env` | Image tag, PostgreSQL credentials and host port |
| `vantigo.env` | Application, identity, email, module and proxy settings |

Every key `vantigo.env` accepts is documented in the field comments of
[apps/server/internal/config/config.go](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
the authoritative reference, and summarised in
[Identity, authentication and deployment](/en/admin/authentication/). For
startup-configured workforce OIDC, static SCIM provisioning and recovery procedures,
see [SSO and SCIM operations](/en/admin/sso-scim/).

### The settings you must decide on

- **`APP_SECRET`** (`vantigo.env`) — required, at least 32 bytes. Every encryption key
  the process uses (CSRF tokens, cookie signing, the AES-256-GCM key for TOTP
  secrets) is derived from it via HKDF-SHA256, one derived key per purpose. Generate
  it once with `openssl rand -base64 32`, store it in a secret manager, and never
  rotate it without a migration plan: a rotation invalidates every open session and
  every TOTP secret encrypted under the old value.
- **`APP_URL`** (`vantigo.env`) — the public origin only: scheme and host, with a
  port if needed, no path and no trailing slash. Mailed links, the accepted `Host`
  header values, the cookie `Secure` attribute and the static OIDC callback are all
  derived from it. Requests carrying any other `Host` than this one or loopback
  (`localhost`, `127.0.0.1`, `[::1]`) are rejected with 400. The port must match
  `VANTIGO_PORT` in `.env`; nothing reconciles the two, and a mismatch raises no
  error — the stack serves, but every invitation and password-reset link it mails
  points at a port nobody is listening on.
- **`DATABASE_URL`** — built by `compose.yaml` from the values in `.env`, with a
  different role per service, so do not set it in `vantigo.env` too. See
  [Database roles](#database-roles).
- **`SMTP_HOST`, `SMTP_FROM`** (`vantigo.env`) — required for the server to start at
  all. `SMTP_FROM` is parsed as a plain address: `no-reply@example.com` is accepted,
  `Vantigo <no-reply@example.com>` is rejected at startup. `SMTP_PORT` defaults to
  587, `SMTP_USERNAME` and `SMTP_PASSWORD` are optional, and `SMTP_TLS` is `starttls`
  by default (`implicit` also negotiates TLS; `none` is plaintext). Leave
  `MAIL_DRIVER` unset: it already defaults to `smtp` here, and `log` is rejected
  outside development because those mails carry bearer links.
- **`MODULES`** (`vantigo.env`) — the comma-separated list of business modules this
  deployment enables. `identity` is always mounted and never listed. Unset enables
  every module the binary can mount: `customers`, `products`, `energy`,
  `communications`, `projects`, `time`, `expenses` and `invoices`. `energy`,
  `communications`, `projects` and `invoices` each require `customers`, and `time`
  requires `projects`; `projects` also uses `products` when it is enabled and answers
  409 on its billing-line endpoints when it is not; `expenses` requires nothing and
  reads `projects` only when it happens to be enabled. To run a smaller install, list
  only what you want, for example `MODULES=customers,products`.
- **`OWNERS_REQUIRE_MFA=0` and `OWNERS_ALLOW_INSECURE_NO_MFA=1`** (`vantigo.env`) —
  shipped in the example because a fresh installation has nobody around to enrol an
  authenticator right after `/setup`. Remove them before production; see
  [Production notes](#production-notes).

### Pinning a release

Pin a specific release with `VANTIGO_TAG=0.16.1` in `.env`. Published image tags drop
the `v` that git release tags keep (`v0.16.1` is the git tag, `0.16.1` the image tag;
`latest` is unaffected). For reproducible production deployments, pin by digest
instead of tag — tags are mutable, digests are not:

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

`VANTIGO_DB_USER` and `VANTIGO_DB_PASSWORD` choose the role the API connects as
(`compose.yaml` builds `DATABASE_URL` from them). They default to the runtime role —
plain defence in depth, so that a compromised API process cannot alter the schema,
create objects, or otherwise act as the database owner. This is a single-tenant
application: there is no `tenant_id` column and no row-level security policy anywhere
in the schema, so this is not a tenant-isolation boundary, only a privilege-separation
one. Point `VANTIGO_DB_USER` at the owner role only for debugging.

`api` mode also re-checks migrations itself before it starts serving, but that check
runs as the same least-privilege `DATABASE_URL` — the `vantigo` service never gets the
owner credential. It only needs read access to conclude there is nothing to do: the
migration library probes for its version table with a plain `SELECT`, returns
immediately when it already exists, and never issues `CREATE TABLE`. The one case
that would need DDL — starting with migrations genuinely pending — never reaches `api`
at all: `vantigo-migrate` runs first, under the owner role, and `vantigo`'s
`depends_on: condition: service_completed_successfully` keeps the API container from
starting until it succeeds, upgrades included.

The role statements run from the PostgreSQL init script, which executes only when the
`postgres-data` volume is created. An existing installation can add the role by hand
(adjust the schema lists to match the `MODULES` enabled in your installation, adding
`invoices` when it is enabled):

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

## Background workers and scaling

The single-container default hosts every enabled module's background workers (outbox
delivery, retention, attachment cleanup, the customers registry workers, and the
invoices EHF workers `invoices-ehf` and `invoices-ehf-events` when
`INVOICES_EHF_ENABLED` is on) inside the
API process: `WORKERS_IN_PROCESS=1`, the default. Deployments that scale the API
horizontally should move them to one dedicated worker container, so that every extra
HTTP replica does not multiply the pollers:

1. Set `WORKERS_IN_PROCESS=0` in `vantigo.env`. It applies to the API replicas.
2. Run one extra container from the same image with the `worker` command. It always
   runs the workers regardless of `WORKERS_IN_PROCESS`, and serves only `/health/live`
   and `/health/ready` for probes.

Replicas that should neither migrate nor run workers can run the `server` command
instead of `api`; it serves only, whatever `WORKERS_IN_PROCESS` says. Whichever
topology you use, retention cleanup, the registry workers and the EHF events worker
take a PostgreSQL advisory lock, so each runs on exactly one instance per cycle; the
outbox and the `invoices-ehf` worker lease one row at a time instead, so any number of
instances share the work without sending anything twice.

On shutdown the process drains for up to `SHUTDOWN_TIMEOUT` (30 seconds by default),
enough for the longest single worker operation to finish and commit. `compose.yaml`
sets `stop_grace_period: 35s` on the `vantigo` service so Compose's SIGKILL never
arrives before that drain can complete. Whatever you set `SHUTDOWN_TIMEOUT` to, keep
`stop_grace_period` (Compose) or `terminationGracePeriodSeconds` (Kubernetes) comfortably
above it, or an in-flight operation can be killed mid-way and retried after restart.
A SIGTERM that arrives during a migration waits for it to finish; a second signal
terminates immediately.

## Health probes

The image has no shell, so the container `HEALTHCHECK` execs the binary itself
(`/app/vantigo healthcheck`) rather than running a `CMD-SHELL` curl or wget one-liner.
It probes `/health/ready`: startup complete, PostgreSQL reachable, and object storage
reachable if configured. Keep any probe you configure — Compose's `healthcheck.test`,
a Kubernetes liveness or readiness probe, anything else — in that same exec form. An
HTTP-style probe that connects directly, rather than execing inside the container,
sends its own address as the `Host` header, and the host filter only accepts
`APP_URL`'s host plus loopback (`localhost`, `127.0.0.1`, `::1`); every other `Host`
is rejected with 400, so such a probe always fails.

The stack's probe runs every 10 seconds with a 5-second timeout, 6 retries and a
30-second start period.

## Database connection budget

Each API replica's PostgreSQL pool defaults to `max(4, NumCPU)` connections, which ties
the pool size to the host the replica happens to land on. Pin it explicitly with
`pool_max_conns` on `DATABASE_URL` in `compose.yaml` instead of relying on that
default, especially once you run more than one replica. Size the budget so that

```
replicas × pool size  ≤  max_connections − headroom (reserve ~10 for
                          migrations, monitoring, and manual sessions)
```

Migrations run without the pool cap, since schema changes may legitimately run longer.

## Base path and reverse proxy

The whole application can be served below one configurable path prefix. Set
`APP_BASE_PATH` in `vantigo.env` and set `APP_URL` to the public scheme and host — no
path, since the path prefix is `APP_BASE_PATH`, not part of `APP_URL`. The API
prefixes remain `/api/v1/identity`, `/api/v1/customers`, `/api/v1/products`,
`/api/v1/energy`, `/api/v1/communications`, `/api/v1/projects`, `/api/v1/time`,
`/api/v1/expenses` and `/api/v1/invoices`.

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

With `APP_BASE_PATH=/vantigo`, generated invitation, password-reset and OIDC callback
URLs include that prefix. The base path is a runtime setting; the single image serves
the matching SPA without a rebuild. Trust exactly the proxy in front of you with
`TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS`, or the forwarded scheme, host and
client-address headers are ignored.

## The built-in documentation

Every image carries this documentation, built for exactly the version it ships, and
serves it at `/docs/` beside the application (`/docs/en/` and `/docs/nb/`, under the
base path when one is set). It needs no network and no sign-in, and the avatar menu's
**Help** opens it in the reader's language. The public site at
<https://docs.vantigo.io> describes the newest development version instead; a line
under the header of every page says which Vantigo it describes.

The pages are static files inside the binary: no setting enables or disables them,
and they add a few megabytes to the image. The generated API reference is left out of
the embedded copy and lives on the public site only. Because the site's own scripts
run inline and its search runs WebAssembly, responses under `/docs/` carry the site's
own content security policy instead of the application's; see
[Transport security and browser hardening](/en/admin/transport-security/#browser-security-headers).

## Email and observability

Identity invitations and password recovery use the `MAIL_DRIVER` and `SMTP_*`
settings. Per-mailbox delivery credentials for the Communications module are
configured through the Communications API, where the host, port and a protected
password are stored as mailbox credentials — there is no environment variable for
them. Those channels are SMTP-only: the API refuses to create or update a channel
naming any other provider, and rejects a Mailgun credential outright. See
[Communications](/en/reference/communications/).

The Projects and Time modules need no environment variable of their own; see
[Projects](/en/reference/projects/) and [Time](/en/reference/time/). Expenses needs the
object store configured (`STORAGE_PROVIDER` in `vantigo.env`, the same setting
Communications attachments use) the moment anybody tries to attach a receipt, whether
or not the receipt rule requires one: unconfigured, a receipt upload or download
answers 503 rather than the process failing to start. See
[Expenses](/en/reference/expenses/) and [Object storage](/en/admin/object-storage/).

Telemetry is off by default. The standard OTLP variables can be set in `vantigo.env`:

```dotenv
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
# OTEL_EXPORTER_OTLP_PROTOCOL=grpc
# OTEL_EXPORTER_OTLP_HEADERS=x-api-key=secret
```

## Production notes

- Put the application behind a TLS-terminating reverse proxy and configure
  `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` to trust exactly that proxy. The
  quick-start stack serves `http://localhost:8080`; on a network you do not control,
  an http origin sends session cookies and the invitation and password-reset bearer
  links in the clear. See [Transport security](/en/admin/transport-security/).
- Set `APP_URL` to the public `https://` origin. Mailed links, the accepted `Host`
  header values, the cookie `Secure` attribute and the static OIDC callback are all
  derived from it. The callback is fixed at `/api/v1/identity/oidc/callback`.
- The bundled PostgreSQL is reached over the private Compose network with no
  certificate authority, so `DATABASE_URL` carries no `sslmode` and pgx connects the
  way libpq's `prefer` does. If you point the stack at a PostgreSQL across a network
  instead, add `sslmode=verify-full` (or `verify-ca` when the server certificate does
  not name the host) on both the `vantigo-migrate` and `vantigo` services — edit
  `compose.yaml`, not `vantigo.env`, since `compose.yaml` is what builds the
  connection URL.
- `vantigo.env.example` also documents the e-invoicing settings, `INVOICES_EHF_ENABLED`
  and `INVOICES_STORECOVE_BASE_URL`, beside the Peppol lookup's; setting up sending as
  EHF — the Storecove account, the credentials and the KID agreement — is its own
  procedure ([E-invoicing](/en/admin/e-invoicing/)).
- `vantigo.env.example` documents the static OIDC and SCIM settings. Configuration is
  deployment-bound and changes require a restart. Do not put provider or SCIM secrets
  in source-controlled files — inject `OIDC_CLIENT_SECRET`, `SCIM_TOKEN`,
  `SCIM_PREVIOUS_TOKEN` and `COMMUNICATIONS_AI_API_KEY` from a secret store instead.
- SCIM uses `/api/v1/identity/scim/v2`, bearer tokens and `application/scim+json`.
  Set `SCIM_TOKEN`; during rotation, optionally set `SCIM_PREVIOUS_TOKEN` together
  with `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` (in the future, and no more than 24 hours
  after startup). See [SSO and SCIM operations](/en/admin/sso-scim/).
- `MANAGEMENT_PORT` and `MANAGEMENT_TOKEN` enable a private status endpoint for a
  control plane; see the [management listener](/en/admin/management-listener/). The
  Compose stack deliberately does not publish that port: reach it from another
  container on the Compose network, never from the host's public interface.
- **Remove `OWNERS_REQUIRE_MFA=0` and `OWNERS_ALLOW_INSECURE_NO_MFA=1` from
  `vantigo.env`.** The quick-start stack ships with both because a fresh installation
  has no enrolled authenticator, and with the requirement on an administrator is held
  at `/settings/security` until one is enrolled. The `=0` is what turns the
  requirement off; the acknowledgement alone does not, it only lets the API start with
  it off. Left in place, a compromised Owner or SystemAdmin password alone is enough
  for full control of identity and the tenant control plane. Enrol an authenticator
  for every privileged account, then set `OWNERS_REQUIRE_MFA=1` and drop the
  acknowledgement — see [Identity, authentication and deployment](/en/admin/authentication/).
- Never run `seed` in production; it is development-only (`APP_ENV=development`).
- `APP_SECRET` derives every encryption key this process uses (CSRF tokens, cookie
  signing, TOTP secret encryption) via HKDF-SHA256 — there is no external key vault to
  provision and nothing to wrap. Generate it once with `openssl rand -base64 32`,
  store it in a secret manager, and treat losing it the same as losing a signing key:
  every open session and every stored TOTP secret becomes unrecoverable.

`api` mode applies pending migrations before it starts serving, in addition to the
one-shot `vantigo-migrate` job this stack runs first. For a controlled release, still
run exactly one migration job, wait for successful completion, and then start the API.
The point is to see a migration failure before any API replica starts, not to skip
`api`'s own startup check. Take and verify a PostgreSQL backup before deploying a
release that contains a destructive migration. Migrations are forward-only; do not
plan an application rollback across one.

## Upgrading

```bash
docker compose pull
docker compose up -d
```

Compose pulls the new image, runs `vantigo-migrate` again as a one-shot job, and
restarts `vantigo` only once the migrations have completed successfully. If you pinned
`VANTIGO_TAG` in `.env`, change it to the new release first.

**The release with Invoices.** An installation that leaves `MODULES` unset enables every
module this binary knows, so it gets the Invoices app on upgrade. Nobody can use it
until a role grants `invoices:access` (Owner holds every permission already). Issuing
needs an object store (`STORAGE_PROVIDER`): without one the app opens and every issue
answers 503. An installation that lists `MODULES` explicitly gets Invoices only once
`invoices` is added, beside `customers`. See [Invoices](/en/reference/invoices/).

**The release with invoice payments and sending.** One new permission,
`invoices:payments` — registering and removing payments — which no built-in role
holds; Owner has it through the wildcard, and everyone else needs a role that grants
it. Sending a document by e-mail is under `invoices:issue`, so every role holding
`invoices:issue` can send from this release. It goes through the same `SMTP_*`
configuration as identity's mail ([Email and observability](#email-and-observability)):
an installation without a working SMTP server cannot send invoices, and one on
`MAIL_DRIVER=log` (development only) answers every send with 503. A bounce goes to
the envelope sender, `SMTP_FROM`, not to the seller's Reply-To, and Vantigo records
none: point `SMTP_FROM` at a mailbox someone reads if bounces matter.

Documents issued before this release to a Norwegian business carry no organisation
number in their buyer snapshot — `buyer_foreign_id` reads `no…` instead — because the
issue compared the directory's country case-sensitively. Those snapshots are
immutable: where it matters, credit such a document and issue it again. Documents
issued from this release on are right.

For a complete backup, one-migrator, token rotation and Owner break-glass runbook, see
[SSO and SCIM operations](/en/admin/sso-scim/).
