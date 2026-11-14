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

The Customers, Products, Energy, Communications, Projects, Time, Expenses, Invoices,
Staff, Planning, Inventory, Orders, Manufacturing, Knowledge, Calendar, Microsoft Entra ID, Google Workspace and Assistant modules are enabled by default
(`MODULES` in `vantigo.env`). They share one PostgreSQL database named `vantigo`, with
independent `identity`, `customers`, `products`, `energy`, `communications`,
`projects`, `time`, `expenses`, `invoices`, `staff`, `planning`, `inventory`, `orders`, `manufacturing`, `knowledge`, `calendar`, `entra`, `google` and `assistant`
schemas and migration histories.

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

`seed` also exists for local development and demo environments (`APP_ENV=development`; it
exits 2 outside it): it applies migrations and populates an idempotent Norwegian demo
dataset across all modules (20 users across 7 permission roles, 200 companies with 400
contacts, 50 products, 75 projects, time entries, expenses, invoices, energy metering
points, and customer conversations). No command, or an unknown one, prints usage on
stderr and exits 2, so a typo in a job definition fails loudly rather than quietly
becoming a web server.

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
[Identity, authentication and deployment](/en/admin/authentication/). Workforce
OIDC and SCIM provisioning are not among them: an Owner sets both on the **Single
sign-on** card, and [SSO and SCIM operations](/en/admin/sso-scim/) has the procedures
and the recovery.

### The settings you must decide on

- **`APP_SECRET`** (`vantigo.env`) — required, at least 32 bytes. Every encryption key
  the process uses (CSRF tokens, cookie signing, the AES-256-GCM key for TOTP
  secrets) is derived from it via HKDF-SHA256, one derived key per purpose. Generate
  it once with `openssl rand -base64 32`, store it in a secret manager, and never
  rotate it without a migration plan: a rotation invalidates every open session and
  every TOTP secret encrypted under the old value. With Staff, a rotation also makes
  every sealed national identity number and bank account unopenable, and leaves the
  national identity numbers' index unable to find a number or refuse a duplicate of
  one written before it — see
  [Rotating APP_SECRET](/en/contributing/platform-packages/#rotating-app_secret).
- **`APP_URL`** (`vantigo.env`) — the public origin only: scheme and host, with a
  port if needed, no path and no trailing slash. Mailed links, the accepted `Host`
  header values, the cookie `Secure` attribute, the static OIDC callback and the
  Microsoft Entra ID Outlook connection's redirect URI are all derived from it. Requests carrying any other `Host` than this one or loopback
  (`localhost`, `127.0.0.1`, `[::1]`) are rejected with 400. The port must match
  `VANTIGO_PORT` in `.env`; nothing reconciles the two, and a mismatch raises no
  error — the stack serves, but every invitation and password-reset link it mails
  points at a port nobody is listening on.
- **`DATABASE_URL`** — built by `compose.yaml` from the values in `.env`, with a
  different role per service, so do not set it in `vantigo.env` too. See
  [Database roles](#database-roles).
- **`SMTP_HOST`, `SMTP_FROM`** (`vantigo.env`) — the system mailbox, which writes to your
  own employees; required for the server to start at all. `SMTP_FROM` is parsed as a plain address: `no-reply@example.com` is accepted,
  `Vantigo <no-reply@example.com>` is rejected at startup. `SMTP_PORT` defaults to
  587, `SMTP_USERNAME` and `SMTP_PASSWORD` are optional, and `SMTP_TLS` is `starttls`
  by default (`implicit` also negotiates TLS; `none` is plaintext). Leave
  `MAIL_DRIVER` unset: it already defaults to `smtp` here, and `log` is rejected
  outside development because those mails carry bearer links.
  To send through Cloudflare Email Service instead of an SMTP server, set
  `MAIL_DRIVER=cloudflare` and the `CLOUDFLARE_EMAIL_*` settings; the `SMTP_*` ones
  are then not read ([Cloudflare Email Service](/en/admin/authentication/#cloudflare-email-service)).
- **`MODULES`** (`vantigo.env`) — the comma-separated list of business modules this
  deployment enables. `identity` is always mounted and never listed. Unset enables
  every module the binary can mount: `customers`, `products`, `energy`,
  `communications`, `projects`, `time`, `expenses`, `invoices`, `staff`, `planning`,
  `inventory`, `orders`, `manufacturing`, `knowledge`, `calendar`, `entra`, `google` and `assistant`. `energy`, `communications`, `projects` and `invoices` each
  require `customers`, `time` requires `projects`, `planning` requires both `projects`
  and `staff`, `inventory` requires `products`, `orders` requires `products`, `customers`
  and `invoices`, `manufacturing` requires `products` and `inventory`, and `entra` and `google` each require `staff`; `projects` also
  uses `products` when it is enabled and answers 409 on its billing-line endpoints when
  it is not; `expenses` requires nothing and reads `projects` only when it happens to be
  enabled; `staff`, `calendar` and `assistant` require nothing (the assistant is of
  use only while `AI_PROVIDER` is set). To run
  a smaller install, list only what you want, for example `MODULES=customers,products`.
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
(adjust the schema lists to match the `MODULES` enabled in your installation):

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
GRANT USAGE ON SCHEMA identity, customers, products, energy, communications, projects, time, expenses, invoices, staff, planning, inventory, orders, manufacturing, knowledge, calendar, entra, google, assistant TO "vantigo_app";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses, invoices, staff, planning, inventory, orders, manufacturing, knowledge, calendar, entra, google, assistant TO "vantigo_app";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses, invoices, staff, planning, inventory, orders, manufacturing, knowledge, calendar, entra, google, assistant TO "vantigo_app";
```

## Background workers and scaling

The single-container default hosts every enabled module's background workers (outbox
delivery, retention, attachment cleanup, Communications' mailbox sync
`communications-mailbox-sync`, always, which reads each Microsoft 365 channel's Inbox
every 30 seconds — the object store and outbound https to `graph.microsoft.com` are what
it needs —, Communications' messenger sync `communications-messenger-sync`, always, which
asks the messaging providers every 2 seconds which Pages have new events and reads each
Messenger channel's inbox of events — it needs the object store, and reaches nothing on the
network itself: the messaging provider's own module holds the connection —, the customers registry workers, the
customers import worker `customers-import`, the energy import worker `energy-import`, the energy spot price worker
`energy-spot-prices` when `ENERGY_SPOT_PRICE_SERVICE_URL` is set or `ENERGY_SPOT_PRICE_FEED_ENABLED=1`, the invoices EHF workers `invoices-ehf` and `invoices-ehf-events` when
`INVOICES_EHF_ENABLED` is on, the invoices reminder worker `invoices-reminders`,
always, the invoices billing worker `invoices-billing`, always, the invoices order allocations reconcile
`invoices.allocation_reconcile`, always, the invoices payments worker `invoices-payments` when
`INVOICES_VIPPS_ENABLED` is on, the invoices bank feed worker `invoices-bank-feed` unless
`INVOICES_BANK_FEED_ENABLED=0`, the calendar cancellations worker
`calendar-cancellations` unless `CALENDAR_CANCELLATIONS_ENABLED=0`, the calendar
subscriptions worker `calendar-subscriptions` unless `CALENDAR_SUBSCRIPTIONS_ENABLED=0`, and the
calendar mailbox sync worker `calendar-mailbox-sync` unless `CALENDAR_MAILBOX_SYNC_ENABLED=0`, and the
Microsoft Entra ID module's `entra-outlook-sweep`, always, which tidies the people's
Outlook connections every hour, the Google Workspace module's `google-sweep`, always,
which does the same for the people's Google connections, the Meta module's `meta-split`,
always, which splits Meta's stored webhook events into the Messenger channels' inbox and
fetches the files people sent from Meta's servers, and `meta-retention`, always, which
deletes what the module keeps past its periods every hour, and the Assistant's `assistant-runs`, always, which fails a run
a restart left running, and `assistant-retention`, always, which deletes conversations past
their retention once a day — and the platform's events dispatcher `events.dispatch`,
always, whatever `MODULES` says) inside the
API process: `WORKERS_IN_PROCESS=1`, the default. Deployments that scale the API
horizontally should move them to one dedicated worker container, so that every extra
HTTP replica does not multiply the pollers:

1. Set `WORKERS_IN_PROCESS=0` in `vantigo.env`. It applies to the API replicas.
2. Run one extra container from the same image with the `worker` command. It always
   runs the workers regardless of `WORKERS_IN_PROCESS`, and serves only `/health/live`
   and `/health/ready` for probes.
3. The worker needs no OIDC or SCIM settings of its own. Who holds a permission (a
   directory's deactivation counts only while SCIM is on) and the accounts mode (local
   or federated) both follow the single sign-on settings in the database, which the
   worker reads like the API, within 20 seconds of an Owner's change (see
   [the authority mode](/en/admin/authentication/#the-authority-mode)). Nor does it
   need anything for Microsoft Entra ID: with the module on, the Staff accounts worker
   reaches the tenant through it, with the connection, the secrets and the account
   rules it reads from the database — so the worker container, like the API, needs
   outbound https to `login.microsoftonline.com` and `graph.microsoft.com`, without a
   proxy ([Microsoft Entra ID](/en/admin/entra/#before-you-start)).
4. Keep the clocks of the API and worker hosts in sync (NTP). The Staff accounts
   worker's claims are leases that each process compares with its own clock, so a
   clock that runs ahead can take over a link another process is still working on.

Replicas that should neither migrate nor run workers can run the `server` command
instead of `api`; it serves only, whatever `WORKERS_IN_PROCESS` says. Whichever topology
you use, retention cleanup, the registry workers, the EHF events worker, the
calendar cancellations worker, `entra-outlook-sweep` and `assistant-retention` take a PostgreSQL advisory lock, so each runs on exactly one instance per cycle; the outbox,
the `invoices-ehf` worker, the `invoices-reminders` worker, the `invoices-billing` worker,
the `invoices-payments` worker, the `invoices-bank-feed` worker, `communications-mailbox-sync`, `communications-messenger-sync`, the calendar subscriptions and mailbox sync workers and `events.dispatch` instead take one row at a time under a time-limited lease, so any number of
instances share the work without sending anything twice. `meta-split` takes the stored
events under a lease and `meta-retention` deletes in batches, so both are safe on any
number of instances. `google-sweep` and
`invoices.allocation_reconcile` take neither: each of their steps claims its rows in one
statement, so two instances never handle the same row. The reminder worker sends one letter at a time, at most one a
second per instance, and each send must finish well inside its 60-second lease
([The reminder worker and mail](/en/admin/payments/#the-reminder-worker-and-mail)).
The payments worker polls every second and handles every Vipps payment that is due in
each cycle
([The payments worker](/en/admin/payments/#the-payments-worker)).
The billing worker has no settings: it carries the billing runs people make under
**Invoices → Billing runs** — of consumption, or of orders while Orders is on —, polling every 5 seconds and drafting — and, when the run
asks, issuing — one customer's invoices at a time, at most five customers a cycle, each
under a 10-minute lease; it sends nothing
([Billing runs](/en/reference/invoices/#billing-runs)).
The events dispatcher has no settings either and needs no `MODULES` entry: every
5 seconds it hands the events modules publish to the modules that take them, at most
100 at a time, each under a 2-minute lease, retrying a failure with backoff and leaving
a delivery that failed eight times dead for the operator to retry or skip on the
[management listener](/en/admin/management-listener/#the-events-outbox), where its log
lines are listed too.

The customers import worker checks and runs the customer files people upload. It
polls every `CUSTOMERS_IMPORT_POLL` (5 seconds by default), and in the single-container
default the API also wakes it the moment a check or an import is asked for. It takes
an advisory lock, so one instance works on one import at a time.
`CUSTOMERS_IMPORT_ENABLED=0` turns it off; imports then wait, and the import page says
it is waiting for the background worker. Finished and abandoned imports, with the rows
they uploaded, are removed after `CUSTOMERS_IMPORT_RETENTION` (`720h`, 30 days); the
customers they created stay. See
[Export, and import from a file](/en/reference/customers/#export-and-import-from-a-file).

The energy import worker `energy-import` checks, runs and attaches the metering-point
files people upload, in the same shape: it polls every `ENERGY_IMPORT_POLL` (5 seconds
by default), the API wakes it the moment a check, an import or an attach is asked for,
and it takes an advisory lock of its own. `ENERGY_IMPORT_ENABLED=0` turns it off;
imports then wait. Finished and abandoned imports, with their rows, are removed after
`ENERGY_IMPORT_RETENTION` (`720h`, 30 days); the metering points and supply periods
they wrote stay. In worker mode it reads customers through the customers module, so
run it with `customers` among the `MODULES` when imports name customers. See
[Importing metering points](/en/reference/energy/#importing-metering-points).

The energy spot price worker `energy-spot-prices` fills the day-ahead prices of each
price area from **one** source: the shared spot-price service, or a public feed. It is
an outbound call the installation opts into, so it is **off** unless one of them is
set; without either, prices are imported from a file on the Prices page. Setting both
`ENERGY_SPOT_PRICE_SERVICE_URL` and `ENERGY_SPOT_PRICE_FEED_ENABLED=1` stops the
start-up ("set one spot price source"). `ENERGY_SPOT_PRICE_FEED_POLL` (15 minutes by
default, at least `1m`) paces either source, and either runs under the same advisory
lock, so one instance fetches, with at most twenty requests a cycle.

**With the spot-price service** (`ENERGY_SPOT_PRICE_SERVICE_URL` and
`ENERGY_SPOT_PRICE_SERVICE_KEY`, the installation's bearer key — on our hosts
`http://vantigo-spotprices:8080` and the key the host's tooling writes), the worker
copies into the installation's own database the prices of every area the service
follows (NO1–NO5 on our hosts), so the Prices page fills before the first metering
point exists; an area in use that the service does not follow is not asked for (see
below). Each cycle first asks the service which areas it follows, then reads each
area's live window — fourteen days back to tomorrow — in one request, when one of its
days is due or the window was last read more than a day ago (a re-read that fails with
no day due records nothing and is tried again the next cycle). When the worker was
away for more than two weeks, it first reads the days it missed between the last live
window and today's, oldest first, as history, so a downtime leaves no hole. It then
walks each area's
history backwards, 35 days a request (34 across the autumn clock change), down to the
oldest day the service holds, or to `ENERGY_SPOT_PRICE_HISTORY_FROM` when that is
later. The twenty requests include the one for the areas. **While history is being
copied the worker runs a cycle every minute** rather than every poll; five areas from
2015 take about half an hour, after which the poll paces it again. Prices the service
marks preliminary (tomorrow's NOK figure until the exchange rate is final) are stored as
preliminary, asked for again every hour, and replaced when they turn final. The service
never overwrites a price imported from a file. The service is reached at a private
address through the outbound guard's one exception
([Transport security](/en/admin/transport-security/#the-private-network-exception)):
an `http` URL only to a private address, `https` to any routable one, and never a
redirect, a proxy or another host.

To verify, open the Prices page: the Coverage card's title says **Spot-price service**,
each area shows its resolution, and the History column says how far back it has
copied ("Copying… back to …", then "Complete from …"). Or ask
`GET /api/v1/energy/spot-prices/coverage` with a session holding `energy:prices-view`:
it answers `"source": "service"`, and per area `followedByService` and `history`
once the first cycle has run.

What can go wrong — the live window's failure is shown in the Coverage card's **Last
fetch** column, the history's under **History**, and either is logged at warning:

- **"the spot-price service refused the key"** — a `401`: the key is wrong, revoked or
  rotated without restarting Vantigo. Set the current key and restart.
- **"the spot-price service's address is public; use https"** — the URL is `http` and
  its host resolves to a public address. Use the internal address, or `https`.
- **"the spot-price service is rate limiting this installation"** — a `429`: the
  cycle makes no further request, and the next attempt waits for the backoff or the
  service's `Retry-After`, whichever is later, but never more than a day.
- **"the spot-price service answered 400 Bad Request"**, **"… did not answer a list of
  prices"**, **"… larger than 4 MiB"** or an interval time it cannot read — the service
  refused a history window or answered one Vantigo cannot read. Such a window is asked
  three times, backed off between; after the third it is **skipped**, its days counted
  as skipped, and the walk goes on, the sentence staying under History until the next
  window is copied. Fill those days from a file. A timeout, a `5xx`, a `401` or a `429`
  never skips a window: the walk waits and asks again.
- **The service's areas cannot be read** — logged at warning with an empty area; the
  History column is left as it was, nothing of history is copied that cycle, and the
  live window still runs for the areas in use.
- **An area in use the service does not follow** — a metering point in, say, `SE3` on a
  host whose service follows NO1–NO5 only. The History column says "Not followed by
  the spot-price service" and the worker asks nothing for it: ask the service's operator to
  follow the area, or import its prices from a file. An area the service **stops**
  following says the same from the next cycle and keeps what was copied; once it is
  followed again, the days it missed meanwhile are read as above.
- **Days skipped** — a day the service holds incompletely or without a NOK price, or one
  over a day imported from a file at another resolution, is counted in the History
  column and left for a file; the worker never overwrites an imported price.

**With the public feed** (`ENERGY_SPOT_PRICE_FEED_ENABLED=1`), the worker fetches the
prices of each price area in use — that of a metering point with a non-cancelled supply
period, or one already priced — from seven days back to tomorrow, from
`ENERGY_SPOT_PRICE_FEED_URL` — by default the public feed at `www.hvakosterstrommen.no`.
A day older than a week is filled in from a file on the Prices page. The default feed
serves the Norwegian areas NO1–NO5 only: a metering point in another area (a Swedish
`SE3`, say) makes the worker ask for days the feed never has, so give it a feed that
serves them or import that area's prices from a file. The feed is fetched through the
outbound address guard, as the calendar subscriptions are: `https` only, no proxy, no redirect, at most 1 MiB, and never a host that resolves
to a private, loopback, link-local, carrier-grade NAT, cloud metadata or otherwise
non-routable address — so the API or worker container needs outbound https to the
feed's host, without a proxy. A bad `ENERGY_SPOT_PRICE_FEED_URL` or an `ENERGY_SPOT_PRICE_FEED_POLL` under `1m`
stops the start-up even with the worker off. A failed fetch is logged at warning with
the area, the day and the feed's host, never the address's path or query. See
[Spot prices](/en/reference/energy/#spot-prices).

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

**Add two connections per HTTP replica for live updates.** Each replica that serves HTTP
(`api` and `server` mode) holds two more PostgreSQL connections that are not part of the
pool and are not capped by `pool_max_conns`: the listener that hears changes, and the
publisher that shares who is looking at what. A `worker`-only process opens neither.
Count them in the formula:

```
replicas × (pool size + 2)  ≤  max_connections − headroom
```

They connect to `LIVE_DATABASE_URL`, or to `DATABASE_URL` when that is unset
([Live updates and your proxy](#live-updates-and-your-proxy)).

## Base path and reverse proxy

The whole application can be served below one configurable path prefix. Set
`APP_BASE_PATH` in `vantigo.env` and set `APP_URL` to the public scheme and host — no
path, since the path prefix is `APP_BASE_PATH`, not part of `APP_URL`. Redirect URIs
registered elsewhere follow both: the Microsoft Entra ID module's Outlook connection
answers Microsoft at `APP_URL` + `APP_BASE_PATH` + `/api/v1/entra/outlook/callback` —
never on `PUBLIC_BASE_URL` — so a change of either means adding the new URI to the app
registration ([Microsoft Entra ID](/en/admin/entra/#outlook-connection)). Likewise the
Google Workspace module's Connection for people answers Google at `APP_URL` +
`APP_BASE_PATH` + `/api/v1/google/connect/callback`, never on `PUBLIC_BASE_URL`, which
must be registered on the Internal web client
([Google Workspace](/en/admin/google-workspace/#connection-for-people)). The API
prefixes remain `/api/v1/identity`, `/api/v1/customers`, `/api/v1/products`,
`/api/v1/energy`, `/api/v1/communications`, `/api/v1/projects`, `/api/v1/time`,
`/api/v1/expenses`, `/api/v1/invoices`, `/api/v1/staff`, `/api/v1/planning`,
`/api/v1/inventory`, `/api/v1/orders`, `/api/v1/manufacturing`, `/api/v1/knowledge`, `/api/v1/calendar`, `/api/v1/entra`, `/api/v1/google` and `/api/v1/assistant`.

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

## Live updates and your proxy

Vantigo keeps screens current without reloading: the Communications inbox and an open
conversation update by themselves, and show who else is in a conversation. The browser
opens one WebSocket to `GET /api/v1/live` (under `APP_BASE_PATH` when you set one), and the
server tells it what changed. If the connection cannot be made, nothing breaks: each screen
falls back to refreshing about once a minute. Work through this section so that it can be
made.

### Preconditions

- The reverse proxy must pass WebSocket upgrades for `/api/v1/live`.
- The proxy's read timeout for that path must be at least **75 seconds**. The server sends
  a ping every 25 seconds and drops a connection that is silent for 60, so a proxy that cuts
  idle connections at 60 seconds is also fine, but one that cuts at 30 is not.
- The proxy must pass **binary** WebSocket messages as it passes text ones, and allow messages
  of at least **4 MiB**: Knowledge's co-editing sends whole documents in one message. A proxy
  that limits the message size (nginx has no such limit by default) must be raised for this
  path.
- The proxy must forward `Origin` unchanged, and `APP_URL` must be the address people open
  in the browser: the server accepts the handshake only from that origin.
- `LISTEN` must work on the database connection ([Poolers](#poolers-and-live_database_url)).

### Configure the proxy

nginx, inside the `server` block of [Base path and reverse proxy](#base-path-and-reverse-proxy):

```nginx
location /vantigo/api/v1/live {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 75s;
}
```

Without `APP_BASE_PATH`, the location is `/api/v1/live`. It must come before, or be more
specific than, the location that serves the rest of the application.

Caddy passes upgrades by itself, so the ordinary block is enough:

```
vantigo.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy sets no read timeout. If you set one on the transport, keep it at 75 seconds or more.
Other proxies and load balancers: look for "WebSocket", "upgrade" or "idle timeout" in the
settings, and raise the idle timeout to at least 75 seconds.

### Poolers and LIVE_DATABASE_URL

Live updates use PostgreSQL's `LISTEN`, which does not work through a pooler in
**transaction mode** (PgBouncer in transaction mode, or a managed provider's built-in
pooler). If `DATABASE_URL` goes through one, set `LIVE_DATABASE_URL` to the database's
direct port, with the same role. It is used only for the two live connections; the pool
still uses `DATABASE_URL`. When it is unset, the live connections use `DATABASE_URL`. The
value is redacted when the configuration is printed.

### Turn it off

`LIVE_ENABLED=0` makes `/api/v1/live` answer `404`. Every screen then refreshes about once a
minute, as when the connection cannot be made. It is on by default.

### Verify

1. Sign in, open **Inbox**, and open the browser's developer tools on the Network tab.
2. Filter by WS. A request to `live?v=1` should have status `101 Switching Protocols` and
   stay open.
3. Ask for the status of a replica ([Management listener](/en/admin/management-listener/#the-endpoint)):
   the `live` block should read `"listener": "up"`, and `connections` should count the open
   tabs.

### What can go wrong

| You see | Likely cause |
| --- | --- |
| A `4xx` (typically `426`) or no `101`, the request ends at once | The proxy does not pass `Upgrade` and `Connection`. |
| `101`, but closed after exactly 30 or 60 seconds, and reopened over and over | The proxy's idle timeout is shorter than the server's ping; raise it to 75 seconds or more. |
| `403` | `Origin` does not match `APP_URL`: a proxy that rewrites it, or people opening Vantigo at another address. |
| `404` | `LIVE_ENABLED=0`, or the replica serves only workers. |
| `401` | The session has ended; sign in again. |
| `429` | More than 30 connection attempts a minute for one person, usually a loop in front of the proxy. |
| `"listener": "down"` and screens refresh every minute | The database connection for `LISTEN` fails: a transaction-mode pooler (set `LIVE_DATABASE_URL`), a role without the right to connect, or `max_connections` reached because the two extra connections were not counted. The replica reconnects by itself; it also tests a silent listener every 30 seconds. |
| `queueUsage` near `1` | PostgreSQL's notification queue is filling because a listener is stuck; restart the replica that reports it. |

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

**Two kinds of mail, two routes.** The installation's own mail — `MAIL_DRIVER` and
`SMTP_*` — is the **system mailbox**: it writes to the tenant's **own employees**, never
to its customers. Identity's invitations and password recovery, Staff's notices and the
notices Calendar sends a colleague go out that way, and nothing else does. **No channel,
document or invitation ever uses it.**

Mail to **customers** goes through a channel an Owner chose. Invoices' documents and
reminder letters, and Calendar's invitations to people outside the company, each go out
from a Communications channel named in that module's own settings — **Sending** in
Invoice settings, **Invitations to people outside the company** in the Calendar settings —,
or not at all while the setting is **Disabled**, which is where an installation starts
([Customer mail from other modules](/en/reference/communications/#customer-mail-from-other-modules)).
A channel's own credentials are configured through the Communications API, where the host,
port and a protected password are stored as mailbox credentials — there is no environment
variable for them — and a channel with none is **Unavailable** and sends nothing: it does
not fall back on `SMTP_*`. A channel can instead send from a Microsoft 365 shared mailbox,
with no credential of its own, once the Microsoft Entra ID module's **Mail channels** are
set up ([Mail channels](/en/admin/entra/#mail-channels)); there is no other provider. See
[Communications](/en/reference/communications/#providers).

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
  header values, the cookie `Secure` attribute, the static OIDC callback and the
  Microsoft Entra ID Outlook connection's redirect URI are all derived from it. The callback is fixed at `/api/v1/identity/oidc/callback`.
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
- `vantigo.env.example` also documents, commented out, the bank feed's settings,
  `INVOICES_BANK_FEED_ENABLED` (default on), `INVOICES_FOLIO_BASE_URL` (default
  `https://api.folio.no/v2`, https only) and `INVOICES_BANK_FEED_INTERVAL` (default `30m`,
  `5m` to `24h`). With a workspace's Folio key stored, the server — the `invoices-bank-feed`
  worker every interval, and the Bank page — calls that host over outbound HTTPS — let it through any egress filter; it needs no
  public host, since Folio sends nothing in ([The bank feed](/en/admin/payments/#the-bank-feed)).
- `vantigo.env.example` also documents, commented out, the settings for Vipps MobilePay
  payments of invoices: `PUBLIC_BASE_URL`, where the pay page is reachable from the
  internet, and `INVOICES_VIPPS_ENABLED` (default on) and `INVOICES_VIPPS_BASE_URL`
  (default `https://api.vipps.no`). Publishing the pay page — its host, the proxy and
  keeping the pay link's token out of the proxy's logs — is its own procedure
  ([The public host](/en/admin/payments/#the-public-host)); the proxy in front of the
  public host must send `Host` as the public host name, a constant, never the client's.
  The same public host now also carries Microsoft's change notifications for Calendar's
  Outlook sync, `POST /api/v1/entra/mailbox-notifications`, which the proxy must pass
  through, and `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` (default on; `0` for a public
  host Microsoft cannot reach) switches them off
  ([Microsoft Entra ID](/en/admin/entra/#change-notifications),
  [Calendar](/en/admin/calendar/#the-mailbox-sync-worker)). It carries Google Calendar's
  push notifications for the Google Calendar sync too,
  `POST /api/v1/google/calendar-notifications`, which the proxy must pass through as
  well: Google posts them over HTTPS to `PUBLIC_BASE_URL`, with a certificate it trusts,
  and the same switch turns them off
  ([Google Workspace](/en/admin/google-workspace/#calendar-sync),
  [Calendar](/en/admin/calendar/#google-calendar)). It carries the Meta module's webhook
  for Facebook Messenger as well, `GET` and `POST /api/v1/meta/webhook`, which the proxy
  must pass through as two routes: Meta's handshake and its events, whose signature is
  checked over the body exactly as Meta sent it, so the proxy must not re-write the body
  ([Meta](/en/admin/meta/#the-public-hosts-routes)). It also carries the images of the cards
  Communications sends on Messenger, `GET` and `HEAD`
  `/api/v1/communications/public-media/{token}`, which Meta fetches from the internet under a
  token that holds for 7 days; the proxy must pass it, as exactly one path segment
  ([Meta](/en/admin/meta/#the-public-hosts-routes)). Without `PUBLIC_BASE_URL`, a reply with
  cards is refused. The token is in the path, so the proxy's access log should record that
  path without the token, or not at all.
- `vantigo.env.example` also documents `STAFF_IMPORT_MAX_ROWS`, how many people one
  Staff import takes and one Staff export writes
  ([Staff](/en/admin/staff/#import-and-export-the-register)), and the Staff retention
  worker's `STAFF_RETENTION_ENABLED` (default on) and `STAFF_WORKERS_POLL` (default
  `1h`, at least `1m`), which erase what the retention rules no longer let the register
  keep and finish the deletion of documents' files ([Staff](/en/admin/staff/#retention)),
  and the Staff accounts worker's `STAFF_ACCOUNTS_ENABLED` (default on), which prepares,
  enables and disables the users the account flag asks for on the poll it shares
  ([Staff](/en/admin/staff/#the-accounts-worker)). It reads the accounts mode from the
  single sign-on settings in the database, as the API does. The Staff competence
  worker's `STAFF_COMPETENCE_ENABLED` (default on) mails a person and their managers
  ahead of a certificate's end, and when it has passed, on the same poll
  ([Staff](/en/admin/staff/#the-competence-worker)).
- There is **no `ENTRA_*` variable**. The Microsoft Entra ID module's tenant and
  client, its client secrets (sealed under `APP_SECRET`) and its account rules are in
  the database, set by an Owner and `entra:manage` on the module's page; the module
  reaches two fixed hosts that no setting changes
  ([Microsoft Entra ID](/en/admin/entra/)).
- There is **no `GOOGLE_*` variable** either. The Google Workspace module's service
  account keys and web client secret (sealed under `APP_SECRET`), the admin to
  impersonate and the web client's id are in the database, set by an Owner on the
  module's page; the module reaches fixed Google hosts that no setting changes
  ([Google Workspace](/en/admin/google-workspace/)).
- The workforce OIDC provider and the SCIM token are set on the **Single sign-on**
  card, not in `vantigo.env`: `OIDC_*` and `SCIM_*` there are imported into the
  database once, at the first start of the release that brought the card, and then
  only warned about — though still validated at startup — until you remove them
  ([Before the card](/en/admin/sso-scim/#before-the-card-the-environment-variables)).
  One OIDC variable stays live: `OIDC_WORKLOAD_IDENTITY_TOKEN_DIR`, the directory a
  workload identity token file saved on the card must lie in, the webhook's
  `/var/run/secrets/azure/tokens` by default
  ([Workload identity](/en/admin/sso-scim/#microsoft-entra-id-workload-identity)).
  Do not put secrets in source-controlled files — inject `AI_API_KEY`
  from a secret store instead. The AI plan (`AI_PROVIDER`, `AI_API_KEY`, `AI_BASE_URL`,
  `AI_MODELS`) is instance configuration too, and an Owner chooses the model of each AI
  task under **AI models**: see [AI](/en/admin/configuration/#ai).
- SCIM uses `/api/v1/identity/scim/v2`, bearer tokens and `application/scim+json`.
  An Owner issues the token with **New token** on the card, and rotates it there with
  an overlap of at most 24 hours. See [SSO and SCIM operations](/en/admin/sso-scim/#scim).
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
`invoices:issue` can send from this release. It first went out through the same `SMTP_*`
configuration as identity's mail; it no longer does — a document goes out from a
channel an Owner chose ([the release where customer mail takes a channel](#upgrading),
below). A bounce goes to the channel's address, not to the seller's Reply-To, and
Vantigo records none: point the channel at a mailbox someone reads if bounces matter.

Documents issued before this release to a Norwegian business carry no organisation
number in their buyer snapshot — `buyer_foreign_id` reads `no…` instead — because the
issue compared the directory's country case-sensitively. Those snapshots are
immutable: where it matters, credit such a document and issue it again. Documents
issued from this release on are right.

**The release with Staff.** An installation that leaves `MODULES` unset enables every
module this binary knows, so it gets the Staff app on upgrade, and the migration
creates the `staff` schema. Only the Owner can use it — Owner holds every permission
already — until a role grants `staff:access`; the other four permissions sit above it.
If the database runs a least-privilege application role, grant it the `staff` schema as
above. An installation that lists `MODULES` explicitly gets Staff only once `staff` is
added; it needs no other module. See [Staff](/en/admin/staff/).

**The release with Staff's accounts.** It brings two migrations, one per schema:
identity's `00050` (Staff-managed users, the employee roles, the recorded authority
mode) and Staff's `00051`. Staff's brings, in full: a link that names no user yet (the
accounts worker's link while it is being prepared), the account flag's own revision
(`people.account_revision`, 1 for everyone), the link's synced employee number, the
disable *Disable now* asked for (`identity_links.disable_requested_at`), the employee
number unique under the `C` collation (below), the seed `ammefri` paid, the reserved
hidden types `absent_hours`, `absent_hr_only` and `absent_hr_only_hours`, and the two
erase settings (`erase_pregnancy_health_types`, `erase_sick_case_dates`, both off). It
also starts the accounts worker,
`staff-accounts`, on by default. It does nothing until HR turns a person's account flag
on; from then it creates that person's pending, disabled user ahead of the start even
before an Owner has named the employee roles, and enables nobody until they are named.
`STAFF_ACCOUNTS_ENABLED=0` keeps it off. The
Staff migration (`00051`) makes a person's
employee number unique by upper case under the `C` collation, so case is judged alike
whatever locale the database was created with. Under a Turkish or Azeri locale the old
index could have admitted two numbers that differ only by case (`i-1` beside `I-1`),
and the migration then fails on the new index. Before upgrading such an installation,
look for them:

```sql
SELECT upper(employee_number COLLATE "C"), count(*) FROM staff.people GROUP BY 1 HAVING count(*) > 1;
```

No row means the upgrade is safe; for each row, give one of the people another number
in Staff first. The migration also refuses, naming the key, a company absence type
that already holds one of the three reserved keys; look for them too:

```sql
SELECT key FROM staff.absence_types WHERE key IN ('absent_hours','absent_hr_only','absent_hr_only_hours') AND NOT seeded;
```

No row means the upgrade is safe; otherwise rename each type's key first.

Rolling `00051` back is an operator's last resort. It deletes every link still being
prepared, with or without a user: the pending users those links named stay in identity,
managed by Staff, for an Owner to stop managing and delete. It refuses, naming the
types, while an absence names one of the reserved types or an event's `typeIds` does —
delete or retype those rows, and rewrite or delete those events, by hand first.
Rolling identity's `00050` back is a last resort too: it drops who manages a user,
their employee number and whether they are pending, so a pending user Staff made reads
as an ordinary disabled user afterwards, and rolling forward again leaves them
orphaned — managed by nobody, never invited. Before rolling it back, have an Owner stop
managing and delete those pending users on the **Users** page.

**The release with the Single sign-on card.** It brings identity's
`00058_identity_sso_settings.sql`, the row the workforce OIDC provider and the SCIM
credential live in from now on. Keep `OIDC_*` and `SCIM_*` in place for its first
start: the API imports them into the row, audits `sso-settings.imported` with their
names and logs a warning; then remove them from every container. A worker no longer
needs them. Rolling the migration back drops the row with the sealed client secret
and the token digests, so set the variables again first, with a new `SCIM_TOKEN` when
the card issued the current one. The procedure is in
[Before the card](/en/admin/sso-scim/#before-the-card-the-environment-variables).

**The release with Microsoft Entra ID.** It brings `00061_entra_baseline.sql`, the
`entra` schema. An installation that leaves `MODULES` unset gets the module, and its
tile for Owners, on upgrade; one that lists `MODULES` gets it once `entra` is added
beside `staff`, which it requires — `entra` without `staff` refuses to start. Nothing
reaches a tenant until an Owner registers one on the module's page, and Staff makes
accounts there only for people of the **Microsoft Entra ID** kind. A least-privilege
role needs the `entra` schema granted ([Database roles](#database-roles)). Rolling the
migration back drops the schema with the sealed secrets and any pass not yet shown, and
touches nothing in the tenant. See [Microsoft Entra ID](/en/admin/entra/).

**The release with Planning.** It brings `00072_planning_baseline.sql`, the `planning`
schema with its settings at their defaults and no allocations. An installation that
leaves `MODULES` unset gets the module, and its **Planning** tile for everyone given
`planning:access`, on upgrade; one that lists `MODULES` gets it once `planning` is added
beside `projects` and `staff`, both of which it requires — `planning` without either
refuses to start. The three permissions are new and held by no custom role until an
Owner adds them. A least-privilege role needs the `planning` schema granted
([Database roles](#database-roles)). It starts no worker and needs no setting; there is
nothing else to do. Rolling the migration back drops the schema with every
allocation. See [Planning](/en/user/planning/).

**The release with the Assistant.** It brings `00103_assistant_baseline.sql`, the `assistant`
schema: the settings (everyone has the assistant, conversations kept 12 months, sensitive
data not available to it), each person's switch, the conversations and their messages, runs
and action log, and the Owner's permission levels, all empty. An installation that leaves
`MODULES` unset gets the module, and the **Assistant** tile for everyone, on upgrade; one that
lists `MODULES` gets it once `assistant` is added. It needs no permission and no other module,
but it is of use only while `AI_PROVIDER` is set, with a model that supports tool calls
([Configuration](/en/admin/configuration/#ai)); without one, the assistant is not offered to
anyone. A least-privilege role needs the `assistant` schema granted ([Database
roles](#database-roles)). It starts two workers: `assistant-runs`, which fails a run left
running by a restart, and `assistant-retention`, which deletes conversations past their
retention and those of people identity has erased. Rolling the migration back drops the
schema with every conversation. See [Assistant](/en/user/assistant/) and the [Assistant
reference](/en/reference/assistant/).

**The release with Inventory.** It brings `00108_inventory_baseline.sql`, the `inventory`
schema with no sites, no locations and no stock. An installation that leaves `MODULES`
unset gets the module, and its **Inventory** tile for everyone given
`inventory:locations-view` or `inventory:stock-view`, on upgrade; one that lists
`MODULES` gets it once `inventory` is added beside `products`, which it requires —
`inventory` without `products` refuses to start. The six permissions are new and held
by no custom role until an Owner adds them. A least-privilege role needs the
`inventory` schema granted ([Database roles](#database-roles)). It starts no worker and
needs no setting; there is nothing else to do. Rolling the migration back drops the
schema with every site, location and movement. See [Inventory](/en/user/inventory/).

**The release with Inventory's counts and Products' packagings.** It brings two
migrations. `00109_products_packagings.sql` creates `products.variant_packagings`, empty:
the boxes, cases and pallets a variant is stored and counted in, edited on the product
page. `00110_inventory_counts.sql` creates `inventory.counts` and
`inventory.count_lines`, empty, and adds two nullable columns to the ledger —
`movement_lines.entered` and `movements.count_id` — that existing movements leave null.
No stock and no movement is changed. No permission is new — `inventory:counts-manage`
existed and is now used — and there is no setting and no worker; the default
privileges in [Database roles](#database-roles) cover the new tables. Rolling back drops
the new tables and columns with every packaging, count and count movement's link to its
count, and what was entered in packagings; the movements and the stock stay. See
[Count stock](/en/user/inventory/#count-stock) and
[Packagings](/en/user/products/#packagings).

**The release with Google Workspace.** It brings `00114_google_baseline.sql`, the
`google` schema with its one connection row, not set up. An installation that leaves
`MODULES` unset gets the module, and its page under **Integrations** for Owners, on
upgrade; one that lists `MODULES` gets it once `google` is added beside `staff`, which it
requires — `google` without `staff` refuses to start. The permission `google:manage` is
new and held by no custom role until an Owner adds it. Nothing reaches Google until an
Owner uploads a key and tests the connection. A least-privilege role needs the `google`
schema granted ([Database roles](#database-roles)). It starts no worker. Rolling the
migration back drops the schema with the sealed keys and secret, and touches nothing at
Google. See [Google Workspace](/en/admin/google-workspace/).

**The release with the personal Google connection.** It brings
`00116_google_connections.sql`: the people's own Google connections, none yet, and
**Connection for people**, off. It starts `google-sweep`, which has nothing to do until
an Owner turns the switch on and people connect. Before turning it on, register
`APP_URL` + `APP_BASE_PATH` + `/api/v1/google/connect/callback` on the Internal web
client. Rolling the migration back drops every person's connection with its sealed
token, unrevoked: Google lets an unused token lapse, and a person can remove Vantigo in
their Google account. See [Google Workspace](/en/admin/google-workspace/#connection-for-people).

**The release with Orders.** It brings `00119_orders_baseline.sql`, the `orders` schema
with no quotes, no orders and default settings. An installation that leaves `MODULES` unset
gets the module, its **Orders** tile for everyone given `orders:access` and an **Orders**
tab on the customer page, on upgrade; one that lists `MODULES` gets it once `orders` is
added beside `products`, `customers` and `invoices`, which it requires — `orders` without
one of them refuses to start, naming it. The nine permissions are new and held by no
custom role until an Owner adds them. A least-privilege role needs the `orders` schema
granted ([Database roles](#database-roles)). It starts three workers — `orders.mail`,
`orders.retention` and `orders.quote_expiry` — and one event handler,
`orders.queue_confirmation`, and keeps its PDFs in the object store under `orders/`.
Before people quote, map Products' tax categories to VAT codes in Invoices and choose a
send channel ([Orders](/en/admin/orders/)). Rolling the migration back drops the schema
with every quote, order and template; the stored PDFs stay in the object store.

**The release with invoicing from orders.** It brings `00121_orders_allocations.sql`, the
table of what draft and issued invoices hold of each order line, empty, and
`00122_invoices_order_sources.sql`, which lets an invoice line name an order line's
allocation and gives a billing run its source — every run made before it invoices
consumption. No permission and no setting is new: the **Invoice** button on an order and
Invoices' **From orders** wizard ask Invoices' `invoices:access` and `invoices:create`.
`00123_invoices_order_allocation_ledger.sql` adds Invoices' record of the order
quantities it took, empty, and Invoices starts one more worker, always,
`invoices.allocation_reconcile`: every 10 minutes it gives back to Orders what Invoices
took over an hour ago and no draft invoice holds — the backstop for a draft deleted while
Orders could not be told, a customer's drafts deleted by an anonymisation, or a wizard
or billing run interrupted halfway. A draft invoice that still exists keeps what it holds
however old it is. The worker claims its rows in one statement, so any number of
instances can run it, needs nothing of its own and sends nothing; with Orders off it does
nothing. Rolling `00123` back drops the record, and what it would have given back stays
held until someone releases it on the order; rolling `00122` back is refused while an
invoice line names an order line or a billing run invoices orders; rolling `00121` back
drops what the invoices held.

**The release with shipping.** It brings `00127_orders_shipping.sql`: the carrier accounts,
the default account per site, the shipments with their packages, stored labels and
waybills and tracking events, all empty; three settings of Orders' — no freight product, no
markup, and the shipping notice on — and the shipping notice's mail kind. Orders starts one
more worker, always, `orders.shipment_tracking`: every 30 minutes it asks the carriers for
the shipments booked in the last 60 days, claiming each by moving its last-tracked time
(`SKIP LOCKED`), so any number of instances share it without asking twice; with no carrier
account it does nothing. The API and the worker container need outbound https to
`api.bring.com` (no certificate pinning: Bring uses Let's Encrypt). `SHIPPING_CARRIERS`
(default `bring`) withholds a carrier. The keys are sealed with `APP_SECRET`: changing it
makes them unreadable, and each account's keys must be entered again. Rolling the migration
back drops the shipments and the accounts with their keys; the labels stay in the object
store under `orders/shipment/`. See [Shipping](/en/admin/shipping/).

**The release with Knowledge.** It brings `00135_knowledge_baseline.sql`, the `knowledge`
schema with no articles. An installation that leaves `MODULES` unset gets the module, its
**Knowledge** tile for everyone given `knowledge:articles`, on upgrade; one that lists
`MODULES` gets it once `knowledge` is added. It requires no other module. The two
permissions, `knowledge:articles` and `knowledge:manage`, are new and held by no role until
an Owner adds them — no role has Knowledge by default. A least-privilege role needs the
`knowledge` schema granted ([Database roles](#database-roles)). It starts one worker,
`knowledge.retention`, which once a day purges articles 30 days after they were trashed,
prunes old versions and sweeps erased users, and it adds two live topics
(`knowledge.article` and `knowledge.tree`) to the
[live updates](#live-updates-and-your-proxy) a proxy must let through. The Assistant gains
Knowledge as a source. Rolling the migration back drops the schema with every article and
its history. See [Knowledge](/en/user/knowledge/).

**The release with Manufacturing.** It brings `00156_manufacturing_baseline.sql`, the
`manufacturing` schema with no work centers and no bills of materials. An installation
that leaves `MODULES` unset gets the module, its **Manufacturing** tile for everyone given
`manufacturing:access`, on upgrade; one that lists `MODULES` gets it once `manufacturing`
is added beside `products` and `inventory`, which it requires — `manufacturing` without
either refuses to start, naming it. The five permissions are new and held by no custom
role until an Owner adds them. A least-privilege role needs the `manufacturing` schema
granted ([Database roles](#database-roles)). It starts no worker and needs no setting.
From this release Products also refuses to delete a variant, or to make its product a
service, while an active bill of materials names it. Rolling the migration back drops the
schema with every work center and bill of materials. See
[Manufacturing](/en/admin/manufacturing/).

**The release with Staff competence.** It brings `00066_staff_competence.sql`: the
competence catalogue — five categories and fifteen Norwegian certificates seeded — and
people's competences, empty. No permission is new: `staff:approve` now also verifies
competences and `staff:manage` keeps the catalogue. It also starts the competence
worker, `staff-competence`, on by default; it mails nobody until someone holds a
certificate with an end date, and `STAFF_COMPETENCE_ENABLED=0` keeps it off. Rolling
the migration back drops the three tables with every person's competence rows; the
evidence stays among the people's documents. See
[Staff](/en/admin/staff/#the-competence-worker).

**The release with the competence one-to-one.** It brings
`00067_staff_one_to_one.sql`: four empty tables for what each person wants — their
interests, preferences and availability declaration — and for competence reviews; the
Seen stamp on people's competence rows; and the competence review interval in the
settings, 12 months by default, so the dashboard starts showing an overdue review for
anyone in employment who has not had one within the interval of their latest review or
of the start of their current employment — set it to 0 under
[the settings](/en/admin/staff/#the-settings) first to keep the item off. No permission
is new: `staff:approve` now also records competence reviews. Rolling the migration back
drops the four tables with every person's wishes and reviews, the Seen stamps on the
competence rows and the interval setting.

**The release with competence profiles.** It brings
`00068_staff_competence_profiles.sql`: three empty tables for the competence profiles —
the profiles, their items and the profiles assigned to people — and a nullable column
on the departments for a department's default profile, empty. No setting and no
permission is new: `staff:manage` keeps the profiles with the competence catalogue. The
dashboard starts showing that someone does not meet their competence profile only once
HR has created a profile and assigned it to someone or made it a department's default.
Rolling the migration back drops the profiles, their items, the assignments and the
departments' defaults.

**The release with people's photos.** It brings `00069_staff_person_photo.sql`: three
nullable columns on Staff's people — the photo's key in the object store, its type and
when it was set — empty. No setting and no permission is new: the person, `staff:manage`
and `staff:personal-data` set a person's photo, and everyone who sees the person sees
it. The photos themselves are kept in the object store under the `staff` scope, as
people's documents are, so an installation without object storage
(`STORAGE_PROVIDER`) answers a photo upload *storage unavailable*; a replaced or
removed photo, and the photo of a person erased or deleted, is removed from the store
by Staff's deletion ledger, which the retention worker runs. The same release shows a
person's hours this week on their page when the Time module is on, read from Time with
no new permission; with `time` off the card says time tracking is off. Rolling the
migration back drops the three columns, but not the objects: the photos people have at
that moment stay in the store, named by nothing, and objects already queued are still
removed by the ledger. To leave no photo behind, have every photo removed — or delete
the `people/*/photo/` keys under the `staff` scope by hand — and let the ledger run
before rolling back.

**The release where the photo follows the account.** It brings
`00070_identity_managed_avatars.sql`, an empty table in identity for the **managed
avatars** — HR's photo of a person, standing as the linked user's picture in front of the
one they chose — and `00071_staff_photo_push.sql`, one empty column on Staff's provider
links, the stamp of the picture last written to Microsoft Entra ID. No setting and no
permission is new, and the Entra application needs no further consent:
`User.ReadWrite.All` covers the photo. After the upgrade HR alone sets a person's photo
in Staff; a person changes their own picture through their Vantigo avatar, on My page or
under **Settings › Profile**, which is disabled while HR's photo stands. Staff's accounts
worker then makes each existing HR photo the linked user's picture at the person's next
run, and writes each enabled Entra account's picture to the tenant once; for a person
with no picture in Vantigo it writes nothing, so a photo already in the tenant stays
until Vantigo writes one. Rolling back 00071 drops the stamps, so every picture is
written to the tenant once more after the next upgrade; rolling back 00070 drops the
managed avatars, so every user's own avatar is their picture again until Staff sets
HR's photos anew. The photos in Staff's object store are untouched either way.

**The release where customer mail takes a channel.** It brings
`00096_invoices_send_channel.sql`, one empty column on Invoices' settings, and
`00097_calendar_settings.sql`, Calendar's first workspace setting, one row with no channel,
and one new permission, `calendar:manage` ("Manage Calendar settings", sensitive and
delegable, which Owner holds through the wildcard). **After the upgrade Invoices and
Calendar send nothing to customers until an Owner chooses a channel**: documents and
reminder letters were sent from `SMTP_FROM` before, and invitations too; now **Sending**
in Invoice settings and **Invitations to people outside the company** in the Calendar
settings start at **Disabled**. Until then the **Send** button is not there, a customer
who wants reminders by e-mail gets paper letters, and an outside attendee is stored as
**Not sent**. An Owner sets up a channel in Communications and chooses it on both
([Invoices](/en/user/invoices/#choosing-where-documents-are-sent-from),
[Calendar](/en/user/calendar/#where-invitations-are-sent-from)); a reminder letter that
could not go meanwhile is tried again within its 48 hours. A Communications **SMTP
channel with no stored credentials** no longer sends on `SMTP_*` as a fall-back: it is
**Unavailable**, and a message queued on it fails at once. Nothing is deleted by the
upgrade, `SMTP_*` stays required for the system mailbox, and rolling the migrations back
drops the two settings (the choices, not any mail).

For a complete backup, one-migrator, token rotation and Owner break-glass runbook, see
[SSO and SCIM operations](/en/admin/sso-scim/).
