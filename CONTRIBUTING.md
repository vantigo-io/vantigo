# Contributing to Vantigo

Thank you for your interest in contributing! This document covers everything you need
to get productive in the codebase.

## Getting the stack running

The toolchain is pinned in [`mise.toml`](mise.toml) — Go, Bun and the lint and release
tools — and developers and CI install it the same way. From the repository root:

```bash
mise install              # Go, Bun, lint and release tools
bun install --frozen-lockfile

mise run server:db        # PostgreSQL for tests (55432) and development (55433)
mise run server:test      # go test against a real PostgreSQL
mise run server:check     # golangci-lint, govulncheck, shellcheck, actionlint, goreleaser check
mise run frontend:check   # format, lint, typecheck and tests for every frontend workspace
mise run server:dev       # the api command on http://localhost:8080
mise run smoke            # build the image and smoke-test it end to end
```

`mise run server:dev` runs the `api` command, which applies migrations under the
advisory lock and then serves. It sets a development-only `APP_SECRET`; development
needs no SMTP configuration.

If `bun` or `go` is not on your `PATH`, prefix the command with `mise exec --`. The
workspace pins Bun 1.3.14, and a bare `bun` may resolve to a different installation.

The binary is one image with a dispatch table — the command is the first argument:

| Command | What it does |
| --- | --- |
| `api` | Migrates under the advisory lock, then serves the SPA, the API and health — plus every enabled module's background workers when `WORKERS_IN_PROCESS=1` (the default). |
| `server` | Serves only: never migrates, never runs workers, regardless of `WORKERS_IN_PROCESS`. What a fleet of stateless replicas runs. |
| `worker` | Every enabled module's background workers, plus health for probes. |
| `migrate` | Applies migrations and exits 0/1. |
| `seed` | Development-only (`APP_ENV=development`; exits 2 outside it). Today it does nothing but log `nothing to seed: identity has no development seed`. |
| `healthcheck` | Probes this container's own `/health/ready` on `127.0.0.1:$PORT` and exits 0/1. It constructs nothing, so a probe never fails on a bad `DATABASE_URL` — that is `/health/ready`'s job to report. |

No command, or an unknown one, prints usage on stderr and exits 2.

Rules the code relies on:

- `cmd/vantigo` is the only composition root. Nothing below it reads
  `os.Getenv`; new settings go in `internal/config`, which reports every
  problem at once.
- Tests hit a real PostgreSQL. `internal/testdb` gives each test its own
  database, so tests never share tables and packages run in parallel.
- Error responses are RFC 7807 problems from `internal/httpx`. Nothing from an
  internal error reaches a response body.
- The image is COPY-only: `scripts/build-artifacts.sh` compiles natively and
  embeds the SPA; the Dockerfile never compiles anything.

## Project layout

```
vantigo/
├── apps/
│   ├── server/                      # The Go server — the whole backend
│   │   ├── cmd/vantigo/             # The only composition root and the command dispatch table
│   │   ├── generate.go              # go:generate directives: contract copies, oapi-codegen, sqlc
│   │   └── internal/
│   │       ├── identity/            # Accounts, sessions, MFA, RBAC, OIDC, SCIM
│   │       ├── customers/           # Customers vertical slice
│   │       ├── communications/      # Communications vertical slice (outbound email)
│   │       ├── products/            # Products vertical slice
│   │       ├── energy/              # Energy vertical slice
│   │       ├── projects/            # Projects vertical slice
│   │       ├── time/                # Time vertical slice (package timetracking)
│   │       ├── expenses/            # Expenses vertical slice
│   │       ├── module/              # The platform modules mount through
│   │       ├── contracts/           # Cross-module interfaces, permissions, access rules
│   │       ├── config/              # The environment reference: one struct, one validation pass
│   │       ├── db/                  # Pool, transactions and the embedded goose migrations
│   │       ├── httpx/               # RFC 7807 problem responses
│   │       ├── openapi/             # Embedded contract copies, lint, validation, the corpus test
│   │       ├── server/              # The outer HTTP server: security, telemetry, routing
│   │       ├── testdb/              # A fresh migrated database per test
│   │       ├── web/                 # The embedded SPA
│   │       └── worker/              # The background-worker runner
│   ├── host/frontend/               # @vantigo/app — the single React SPA (Vite), owns all routes
│   ├── customers/frontend/          # @vantigo/customers-ui (pages, API clients)
│   ├── communications/frontend/     # @vantigo/communications-ui
│   ├── products/frontend/           # @vantigo/products-ui
│   ├── energy/frontend/             # @vantigo/energy-ui
│   ├── projects/frontend/           # @vantigo/projects-ui
│   ├── time/frontend/               # @vantigo/time-ui
│   └── expenses/frontend/           # @vantigo/expenses-ui
├── packages/
│   ├── frontend-shell/              # @vantigo/frontend-shell — shared app shell, theme, branding
│   └── frontend-api-client/         # @vantigo/frontend-api-client — generated types and client
├── openapi/                         # The API contract: one OpenAPI file per module
├── deploy/compose/                  # Ready-made Docker Compose stack
├── scripts/                         # Native artifact, image and smoke-test scripts
├── tools/                           # The OpenAPI client generator and the i18n checks
└── assets/                          # Shared branding assets
```

## Architecture

Vantigo is a **modular monolith**: one process, one container, one PostgreSQL
database — with strict module boundaries so any module can later be extracted into its
own deployable without a rewrite.

### Module boundaries

- A module is a package under `internal/<name>` that exposes one `Module()`
  returning a `module.Module`: its name, its `Mount`, the permissions it
  contributes, the background workers it contributes, and the cross-module
  contracts it provides — a customer directory (customers), a user directory
  (identity), a product catalog (products) or a project directory (projects), at
  most one provider per slot. A module may provide none and only consume, as time
  does. `module.Compose` mounts each at `/api/v1/<name>/`.
- Modules **never import each other**. That is enforced by depguard
  (`apps/server/.golangci.yml`): `internal/<module>/...` may import platform
  packages and its own subpackages, never another module's, and `internal/module`
  and `internal/contracts` — the platform they mount through — may import no
  module at all. No module queries another's schema either, enforced by
  `internal/db/schema_test.go`.
- `MODULES` chooses which modules a deployment serves (see below). Identity is
  always mounted and is never listed.

### Cross-module communication

- **Synchronous queries** use contract interfaces from `internal/contracts`
  (`contracts.CustomerDirectory`, `UserDirectory`, `ProductCatalog`,
  `ProjectDirectory`): DTOs only, never store types, implemented by the owning module
  and resolved by `module.Compose`. If a module is extracted later, the interface gets
  an HTTP client implementation and consumers stay unchanged. A contract whose
  provider may be disabled is **optional**: its `Deps` field is nil and the consumer
  handles that (Projects answers 409 on billing lines when products is off).
- **Asynchronous notifications** ("something happened, others may care") use a
  transactional outbox: the publishing module stores the event in the same
  transaction as its state change, and a background worker dispatches it with
  retries. Communications' outbox is the working example. Do not introduce a
  message broker: Postgres is the queue, and every infrastructure piece is
  multiplied per dedicated customer deployment.
- Never call another module's HTTP endpoints from inside the process, and never
  reach into another module's database schema.

### Database

One PostgreSQL database, one schema per module: `identity`, `customers`, `products`,
`energy`, `communications`, `projects`, `time` and `expenses`. Schemas are hard boundaries:

- **No cross-schema foreign keys or joins.** Reference other modules' data by
  opaque ID only. This is what keeps a future "move this schema to its own
  server" a connection-string change instead of a data migration.
- Every schema is migrated regardless of which modules `MODULES` enables, so
  enabling a module later needs no migration.

### Frontend

The host SPA (`@vantigo/app`) owns routing, auth, navigation and the shell; module
packages (`@vantigo/customers-ui`, …) export pages, API clients and components. Route
files in `apps/host/frontend/src/routes/` are thin wrappers around module pages;
the router plugin's `autoCodeSplitting` makes each route its own chunk. Module packages never import
from each other; shared UI lives in `@vantigo/frontend-shell` and the generated API
types and client in `@vantigo/frontend-api-client`.

Frontend app packages never import each other. Composition happens only in host routes,
and each package's exported surface (`src/index.ts` and subpath exports) is its contract —
the frontend parallel of the Go modules' contracts. Host-owned composition points, such
as the customer detail tab list, are extended by adding entries in the host.

Error handling — *the router is the boundary*. `createRouter` in `main.tsx`
sets `defaultErrorComponent`, and TanStack wraps every matched route in its
own catch boundary: a crash in a page renders the error page in that page's
slot with the shell intact, and a crash in an app layout renders it in the
layout's slot. Do not add boundaries per module or per page; they would only
duplicate this. Two boundaries live outside the router on purpose:
`AppErrorBoundary` around everything in `main.tsx` (a static fallback for a
crash in a provider above the router, reading i18n through the instance
because no provider can be assumed) and the one inside `WidgetCard`, which
isolates each dashboard widget because widgets from several modules share one
route. Add a boundary only at a composition point like that — where one
module's component is embedded in another module's page outside a route of
its own.

SPA URL convention — *one prefix per app*: every route of a business module
lives under its module's name, which is also its API prefix and its `MODULES`
entry (`/customers`, `/customers/contacts`, `/communications/inbox`,
`/products/categories`, `/energy/metering-points`, `/projects`, `/time`). Nesting inside the prefix
means *belonging* (`/customers/:id`). The dashboard (`/dashboard`) is the
"Home" app; `/settings`, `/workspace` and `/admin` are *areas*: declared in
`apps.ts` like apps, with their own sidebar and header title, but reached from
the avatar menu and without a switcher tile. Each app is declared in
`apps/host/frontend/src/apps.ts` (label, icon, home, sidebar entries) and has a
layout route at its prefix (`routes/<app>.tsx`) that tags the subtree with the
app key and renders the not-enabled page when the module is off. Backend API
routes keep the same module prefix (`/api/v1/customers/contacts`).

Navigation — *one pattern per level*, so every page reads the same way:

- **Sidebar** = the pages within the area you are in. Always the shell's
  sidebar, declared in `apps.ts`; never an in-content side menu.
- **Tabs** = views of one page (a customer's Overview, Energy and Projects, a
  project's Overview, People and Billing, the roles page's Roles, Assignments
  and Delegations). Always `PageTabs` from
  `@vantigo/frontend-shell`, directly under the page header, and always in
  the URL — a child route or a validated search param — so every view is a
  link. A tab never leaves the page; something that does is a header action.
- **Segmented controls** = filters and form modes (a date range, a
  resolution), never navigation.
- **`PageHeader`** on every page: `breadcrumbs` (the area's list page, then
  the entity) on detail pages, nothing above the title on sidebar
  destinations — the shell header already names the area. Text titles, with
  badges where useful, no icons.
- **Pages are full width.** The shell owns the gutter; no page caps its own
  width.
- **Loading and empty.** A page or card body that is loading shows
  `ContentSkeleton`; a spinner (`Loader`) is for inline waits only (a
  button, an input's right section). A list, table or card with nothing to
  show renders `EmptyState`, never a bare dimmed line.
- **Confirmations** go through the shared confirm modal
  (`modals.openConfirmModal`); a change that must re-authenticate uses the
  settings area's `ReauthModal`, which has the same shape plus the password
  field.

## Design principles

- **Vertical-slice modules** — a module owns its endpoints, its store and its
  schema, and nothing outside it reaches in.
- **A rich domain model** — value objects own validation, and handlers only work
  with valid domain values.
- **Contract first** — `openapi/*.yaml` is the source of truth; the router enforces
  each operation's access rule and rate limit from the contract at runtime.
- **Versioned APIs** — Identity lives under `/api/v1/identity`; business modules use
  `/api/v1/customers`, `/api/v1/products`, `/api/v1/energy`,
  `/api/v1/communications`, `/api/v1/projects`, `/api/v1/time` and `/api/v1/expenses`.
- **In-process contracts** — module collaboration uses `internal/contracts`, not
  service-to-service API keys.
- **Form-friendly errors** — validation errors use camelCase JSON field paths.
- **One configuration pass** — `internal/config` parses and validates everything at
  startup and reports every problem at once.
- **One container** — one binary serves every enabled module and the embedded SPA.

## API conventions

All endpoints are versioned by URL segment. Each module's own contract lives in
`openapi/<module>.yaml`, and the running server serves the merged contract of the
enabled modules at `GET /api/openapi.json` (session required). Module prefixes are
`/api/v1/identity`, `/api/v1/customers`, `/api/v1/products`, `/api/v1/energy`,
`/api/v1/communications`, `/api/v1/projects`, `/api/v1/time` and `/api/v1/expenses`.
Every other `/api` path answers the catch-all 404 problem.

Errors are RFC 7807 problem responses written by `internal/httpx`; validation errors
carry keys matching the JSON field path:

```json
{
  "title": "Invalid customer",
  "status": 400,
  "errors": {
    "name": ["A friendly name cannot be null or empty"],
    "identity.country": ["A country code cannot be null or empty"]
  }
}
```

## Go server

### Running the tests

```bash
mise run server:db        # once, to have PostgreSQL on 55432
mise run server:test      # go test -count=1 ./... (CI adds -race)
```

**On a many-core machine, run the Go tests pinned to four CPUs:**

```bash
cd apps/server && taskset -c 0-3 go test ./... -count=1     # or: -p 4
```

`-p` is the knob, not `-parallel`. `-p` bounds how many **packages** run at
once, which is what bounds how many test binaries migrate at the same time.
`-parallel` bounds parallel tests **within** one package and caps nothing
across packages, so it is not a substitute.

`internal/testdb` migrates **one template database per test binary** and gives
each test a `CREATE DATABASE … TEMPLATE` copy of it, so a test costs a copy
(tens of milliseconds), not a migration run. The doc comment in
`internal/testdb/testdb.go` is the authority on the mechanism, on why the
template is sealed against connections before it is copied, and on the lock
arithmetic that made the old one-migration-per-test design fail with
`out of shared memory (SQLSTATE 53200)` on many-core hosts. Pinning to four
CPUs matches CI and keeps the run's PostgreSQL footprint predictable.

**One production note on the same table:** `EnsureConsumptionPartition`
(`internal/energy/consumption.go`) runs `CREATE TABLE IF NOT EXISTS … PARTITION
OF` *inside the request transaction*, so the first consumption write into a
month that has no partition yet holds an `ACCESS EXCLUSIVE` lock on
`energy.consumption_intervals` for the rest of that transaction. It is one short
transaction once per month, but it is why the first write after a month boundary
can show a latency spike the next one does not.

## Database migrations

Migrations are plain SQL files under `apps/server/internal/db/migrations/`, numbered
and applied in order by [goose](https://github.com/pressly/goose) — one baseline per
module, then one file per change. The second name segment (`NNNNN_<module>_…`)
names the owning module, which is what ties the file to that module's `sqlc.yaml`:

```
00001_platform_init.sql
00002_identity_baseline.sql
00003_customers_baseline.sql
00004_products_baseline.sql
00005_energy_baseline.sql
00006_communications_baseline.sql
00007_customers_type.sql
00008_projects_baseline.sql
00009_projects_tasks.sql
00010_time_baseline.sql
```

They are embedded into the binary (`//go:embed migrations/*.sql`), so the image needs
no migration tooling and no SQL files on disk. Add a migration by adding the next
numbered file; never edit one that has shipped.

`db.ApplyMigrations` takes a PostgreSQL **advisory lock** on a fixed key before it
applies anything, so two binaries starting at once — an old and a new one mid-rollout
included — serialize instead of racing. The key must never change, and the runner
refuses to continue if its session is lost rather than carrying on believing it still
holds the lock.

Every schema is migrated regardless of which modules `MODULES` enables.

Two ways to apply them, and **both** of them migrate:

- The `migrate` command applies them and exits. It connects as
  `MIGRATIONS_DATABASE_URL` when that is set — the owner role, where a deployment
  separates the two — and otherwise as `DATABASE_URL`.
- The `api` command **applies pending migrations before it starts serving**
  (`cmd/vantigo/main.go`, `case modeAPI:` calls `migrate` first) and only serves
  once they succeed. `server` mode never migrates.

Running a `migrate` job first and waiting for it is still the right deployment shape:
the point is to see a migration failure before any serving replica starts, not that
`api` would otherwise leave the schema behind.

Typed queries are generated by [sqlc](https://sqlc.dev/) from each module's
`internal/<module>/sqlc.yaml` and its SQL files. sqlc comes from mise, not a separate
install, and runs as part of the usual generate step:

```bash
cd apps/server && mise exec -- go generate ./...
```

### The API contract

`openapi/*.yaml` is the single source of truth for the API: one OpenAPI 3.0 file per module plus `common.yaml` for shared components. Every operation needs an `operationId` and an `x-vantigo-access` rule (`anonymous`, `session`, `scim`, `policy:<Name>[+<Name>…]` or `permission:<module>:<verb>[+<module>:<verb>…]`, every listed name required).

To change the API, edit the YAML, then regenerate and test:

```bash
cd apps/server && go generate ./... && go test ./internal/openapi/... && cd ../..
bun run gen:client
```

`go generate` refreshes the embedded copies and the oapi-codegen server interfaces; `gen:client` refreshes each frontend package's `api-schema.d.ts`. CI fails when either is stale.

`internal/openapi` validates every exchange in `openapi/testdata/exchanges/` against
the contract. **That corpus is frozen historical evidence and cannot be
regenerated.** It was recorded from the .NET integration suites that this server
replaced; those suites no longer exist, so there is nothing left to re-record from.
It therefore covers only the ported modules (identity, customers, products, energy,
communications). A module with no .NET ancestor has no corpus file, and both the
corpus test and the coverage tool treat that as zero recorded exchanges — the
module's own operation-coverage gate is what proves its endpoints are exercised.
It is kept because it is the only record of what the replaced implementation actually
served, and it is what the contract was validated against. Treat a failure there as
"the contract or the Go implementation has drifted from what the API used to do", and
change the corpus only with a deliberate, documented reason.

`openapi/COVERAGE.md` lists the operations no recorded exchange exercises. Those
operations' contracts come from the endpoint code alone; each module's own tests are
what gate them (see below).

### Identity

The identity module (`internal/identity`) serves `/api/v1/identity/*` from `openapi/identity.yaml`:
sign-in and sessions, users and invitations, MFA and passkeys, RBAC, OIDC and SCIM,
single-tenant, on the platform every later module mounts on.

Development needs nothing beyond `APP_SECRET` (32+ bytes; `mise run server:dev` sets one).

To bootstrap the installation's first Owner, who is also its SystemAdmin, either open
`/setup` in the SPA or call the endpoint directly; no secret is involved, and it works
only until the first Owner exists:

```bash
curl -X POST http://localhost:8080/api/v1/identity/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.test","displayName":"Owner","password":"a strong password"}'
```

`GET /api/v1/identity/bootstrap-status` reports whether bootstrap is still available
(`{"available":true}`) before you call it.

Behind a reverse proxy, set `TRUSTED_PROXY_HOPS` to the number of proxies in front of the
server. Outside development it also needs `TRUSTED_PROXY_CIDRS`, the proxies' own addresses,
or configuration fails: forwarded headers are honoured only from a peer inside that list.

When rotating the SCIM token, `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` must still be in the future
at every start, so a restart after the overlap window fails configuration: remove
`SCIM_PREVIOUS_TOKEN` and its expiry once the window has passed.

Identity's tests run every HTTP exchange through a contract-validating client: a response
that does not match `identity.yaml` fails the test that produced it. `go test
./internal/identity/` also gates on operation coverage — every operation in the contract
must have been exercised by at least one successful exchange, with no allow-list, so a newly
added operation without a passing test fails the whole package.

### Customers, products, energy, communications, projects, time and expenses

Seven business modules mount on that platform, each serving its own contract and
owning its own schema:

- `internal/customers` → `/api/v1/customers/*` from `openapi/customers.yaml`:
  customers, contacts, customer-contact associations, legal identity, contact
  info, typed addresses, a billing profile, the customer timeline, the
  Brønnøysundregisteret (Brreg) lookup, the customer's own registry record kept
  beside it and refreshed on a click, and a Peppol EHF-capability lookup
  (`internal/peppol`, shared with the future Invoices module).
- `internal/products` → `/api/v1/products/*` from `openapi/products.yaml`:
  products, variants, prices, categories and tax categories.
- `internal/energy` → `/api/v1/energy/*` from `openapi/energy.yaml`: metering
  points, meters, supply periods, consumption and the two aggregations.
- `internal/communications` → `/api/v1/communications/*` from
  `openapi/communications.yaml`: channels (SMTP only), conversations and their
  messages, the composer, staged attachments on object storage, delivery and
  its event log, tags, suppressions, and the AI draft and customer-suggestion
  features. It also owns the outbox and the module's three background workers.
- `internal/projects` → `/api/v1/projects/*` from `openapi/projects.yaml`:
  projects and their codes, the roles users hold on them, billing lines pinned to
  product variants, the project timeline and the stats. It provides
  `contracts.ProjectDirectory` and consumes `contracts.UserDirectory` (identity,
  always present) and `contracts.ProductCatalog` (products, **optional** — nil when
  products is off, which makes billing-line operations answer 409). See
  [`docs/projects.md`](docs/projects.md).
- `internal/time` (package `timetracking`) → `/api/v1/time/*` from `openapi/time.yaml`:
  time entries with snapshotted bill and cost rates, weekly submission, approval and
  the period lock, person rate cards, and the dashboard and project hours summaries.
  It provides no contract and consumes `contracts.ProjectDirectory` (projects,
  **required**), `contracts.UserDirectory` and `contracts.ProductCatalog` (products,
  optional). See [`docs/time.md`](docs/time.md).
- `internal/expenses` → `/api/v1/expenses/*` from `openapi/expenses.yaml`: outlays
  and mileage with receipts, an approval flow, two independent tracks after
  approval (reimbursed by `expenses:manage`, invoiced by financial rights on the
  project), dated rates and categories. It depends on **nobody but identity** —
  `MODULES=expenses` alone is valid — and consumes `contracts.ProjectDirectory`
  purely optionally: with `projects` off, `GET /meta` answers
  `projectsAvailable: false` and every project-shaped field is refused on its own
  field. See [`docs/expenses.md`](docs/expenses.md).

`MODULES` chooses which of them a deployment serves: a comma-separated list,
parsed once at startup, defaulting to
`customers,products,energy,communications,projects,time,expenses` — every module
this binary can mount. Identity is always mounted and is never
listed. A name the binary does not know fails
startup, naming the name and the known set. `energy`, `communications` and
`projects` read customer data through `contracts.CustomerDirectory`, so any of
them without `customers` fails startup naming both; `time` reads projects through
`contracts.ProjectDirectory`, so `time` without `projects` fails the same way.
`expenses` has no such dependency check — it starts with or without any other
module. A disabled module
contributes no route, no permission and no contract path, and its paths answer
the `/api` catch-all 404 — but every schema is migrated regardless, so enabling
a module later needs no migration.

**The one thing to know before enabling communications: this module is
outbound-only, and that is a deliberate consequence of the port's scope cut, not
a gap to fix in passing.** There is no Mailgun inbound webhook, no inbound
worker, and no IMAP/inbound synchronisation, so **no path in this codebase
writes a `direction='inbound'` message**. Three user-visible behaviours follow,
all of them contract-complete and all of them permanently in their negative
state: a conversation created through `POST /conversations` cannot be replied
to (reply answers 422 `recipients_missing`, because recipients resolve from the
latest inbound message), `canReplyAll` is always false with an empty
`replyAllCc`, and the AI draft builds its context from inbound messages and so
drafts against an empty one, recording `product_data=false`. Attachments can be
staged but never sent, and suppression is enforced only by the outbox worker,
reply's own check being unreachable. The endpoints are kept in full, so the
existing frontend works and an inbound provider is additive.

The module's deliberate divergences from the original implementation are listed in
`docs/superpowers/specs/2026-09-13-communications-design.md` §6, and the
behaviour they diverge from is pinned in
`docs/superpowers/specs/2026-09-13-communications-inventory.md`. Two worth
knowing without opening either: new attachment uploads are created `clean`
rather than `pending`, because the ClamAV scanner that would have advanced them
is out of scope and every attachment reply would otherwise 409 forever; and the
outbox's documented 3600 s backoff cap is unreachable — the expression clamps
to 1024 s and `max_attempts = 8` makes 128 s the largest backoff a live job
ever waits.

`docs/communications.md` is the module's deployment and integration guide.

The `internal/contracts` interfaces are the only sanctioned cross-module reads.
No module imports another (enforced by depguard) and no module queries another's
schema (enforced by `internal/db/schema_test.go`).

A module can also contribute background workers (`internal/worker`: a `Worker`
is `Run(ctx) error` plus a name and a poll interval, resolved from every
enabled module's `Module.Workers` the same way `Module.Directory` is, and
started by `internal/worker.Runner`). `WORKERS_IN_PROCESS` (`0`/`1`, **default
`1`**) controls whether the `api` command also runs them in-process alongside
serving; `worker` mode always runs them and `server` mode never does,
regardless of this setting — `server` is what a fleet of stateless replicas
runs, and every replica racing to claim the same background job is exactly
what `server` must not do. Communications contributes three — outbox
delivery, retention, and attachment cleanup — and Customers two: the Brreg
registry-feed worker and the Peppol re-check worker, each registered only
when its own configuration switch is on, so the runner's startup log names
exactly what is running.

Communications' retention worker and both of Customers' workers take an
advisory lease, so each runs on one replica at a time. Communications' other
two rely on a conditional-update claim — the
`UPDATE ... WHERE <the same predicate the candidate select used>` *is* the
lock — so every replica runs those every cycle and the database decides who
wins each row. Adding an advisory lock to either would be a defect, not a
hardening: it is not how the original behaves and the claim already provides
the exclusion. A lease is what the three lease-holders need because they have
no per-row claim to fall back on: a retention batch, a feed cursor and a
network re-check are each one indivisible unit of work for the whole
installation.

The module emits four counters on an OpenTelemetry meter named
`Vantigo.Communications`, all of them the outbox's:
`communications.outbox.possible_duplicate_sends` (a job re-claimed after a
crash between the external send and its completion commit — each one is a
possibly duplicated email), `.jobs_completed`, `.jobs_retried` and
`.jobs_failed`. Alert on the last one: every increment is undelivered customer
email. There are deliberately no others — no retention, cleanup, storage, SMTP
or AI metrics, and no histograms — because the module being replaced had none,
and a test fails if a fifth instrument appears under that meter.

Two settings configure the Brreg lookup:

- `BRREG_BASE_URL` (default `https://data.brreg.no`) — the upstream origin.
- `BRREG_TIMEOUT` (default `15s`) — the budget for one lookup end to end,
  retries included. Within it a failed GET is retried up to 3 times with a 4 s
  per-attempt timeout and a jittered backoff. An upstream failure answers 502.

Four more configure the Peppol lookup (`POST .../peppol-lookup`,
[`docs/customers.md`](docs/customers.md#peppol-lookup)) — whether a customer can
receive an EHF invoice:

- `PEPPOL_LOOKUP_ENABLED` (default `1`) — `0` answers the operation 503
  instead of ever reaching the network.
- `PEPPOL_SML_ZONE` (default `participant.sml.prod.tech.peppol.org`) — the SML
  zone a participant identifier is hashed into; the test network's own zone is
  `participant.sml.test.tech.peppol.org`.
- `PEPPOL_DNS_SERVER` (default unset, meaning the server's own name servers from
  `/etc/resolv.conf`) — `host:port` of a resolver to use instead.
- `PEPPOL_TIMEOUT` (default `10s`) — the budget for one lookup end to end, the
  NAPTR query and the SMP request together.

Each module's tests work like identity's: every HTTP exchange runs through a
contract-validating client, so a response that does not match the module's YAML
fails the test that produced it, and the package gates on operation coverage —
every operation in the contract must have been exercised by at least one
successful exchange, with no allow-list, so a newly added operation without a
passing test fails the whole package. No test touches the network: the Brreg
client dials a fake transport, and the Peppol client is not even built in
customers' tests — `modtest.WithPeppolLookup` replaces the whole lookup
function seam instead.

## Frontend development

The frontends are one Bun workspace, managed from the repository root:

```bash
bun install --frozen-lockfile
bun run --cwd apps/host/frontend dev     # http://localhost:10011, proxying /api to :8080

bun run frontend:lint
bun run frontend:test
bun run frontend:build
```

The Vite dev server proxies `/api` to the Go server on `http://localhost:8080`, which
is where `mise run server:dev` listens — run both.

Module UI packages have their own Vitest suites, run from their directories:

```bash
bun run --cwd apps/customers/frontend test
bun run --cwd apps/communications/frontend test
bun run --cwd apps/products/frontend test
bun run --cwd apps/energy/frontend test
bun run --cwd apps/projects/frontend test
bun run --cwd apps/time/frontend test
bun run --cwd apps/expenses/frontend test
```

The SPA is **not** served by the dev server in production: `scripts/build-artifacts.sh`
builds it with Vite and overlays it into `apps/server/internal/web/dist`, where
`go:embed` picks it up at compile time, and then restores the committed placeholder so
the working tree stays clean. That is why plain `go build` and `go test` work without a
frontend build.

Translations are checked separately, and the pre-commit hook runs both:

```bash
bun run translations:check
bun run i18n:test
```

## Ported-from comments

Some Go files cite a `.cs` path in a comment (`.../SomeEndpoints.cs:117`). Those paths
refer to the pre-cutover .NET tree, which was deleted at the cutover and is retrievable
from git history. They are kept deliberately: the citation is often the only record of
*why* a behaviour is shaped the way it is.

## Commit conventions

Commits follow [Conventional Commits](https://www.conventionalcommits.org/), scoped to
the affected module where applicable:

```
feat(customers): add customer create, get and list endpoints
chore(init): add biome config and run on solution
```

The Husky pre-commit hook (`.husky/pre-commit`) runs unconditionally on every commit —
there is no staged-file filter, so it checks the whole tree: `bun run
translations:check`, `bun run i18n:test`, `bunx biome check .`, and `gofmt -l
apps/server`, failing the commit and naming the files if anything needs `gofmt -w`. It
routes each command through `mise exec --` when mise is available, so it works in a
shell that has not activated the toolchain.
