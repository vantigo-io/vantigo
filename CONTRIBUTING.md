# Contributing to Vantigo

Thank you for your interest in contributing! This document covers everything you need
to get productive in the codebase.

## Getting the stack running

From the repository root:

```bash
bun install --frozen-lockfile
dotnet tool restore
dotnet run --project orchestration/AppHost
```

Aspire starts PostgreSQL and the shared `vantigo` database, then runs the host's
`migrate`, Development-only `seed`, and long-running `api` profiles. The host serves
the single Vite frontend and all enabled modules. In production, wait for the
terminating `migrate` job before starting `api`; never run `seed` in production.

The host executable accepts one command: `api`, `migrate`, or `seed`:

```bash
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile migrate
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile seed
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile api
```

## Project layout

```
vantigo/
├── apps/
│   ├── host/
│   │   ├── backend/Vantigo.Host/      # Modular monolith host and API
│   │   └── frontend/                  # Single React SPA (Vite), owns all routes
│   ├── identity/
│   │   └── backend/
│   │       ├── Identity.Module/       # Authentication and Identity module
│   │       └── Identity.Module.Tests/
│   ├── customers/
│   │   ├── backend/
│   │   │   ├── Customers.Module/      # Customers vertical slice
│   │   │   └── Customers.Module.Tests/
│   │   └── frontend/                  # @vantigo/customers-ui (pages, api clients)
│   ├── communications/
│   │   ├── backend/
│   │   │   ├── Communications.Module/ # Communications vertical slice
│   │   │   └── Communications.Module.Tests/
│   │   └── frontend/                  # @vantigo/communications-ui
│   ├── products/
│   │   ├── backend/
│   │   │   ├── Products.Module/       # Products vertical slice
│   │   │   └── Products.Module.Tests/
│   │   └── frontend/                  # @vantigo/products-ui
│   └── frontend-shell/                # Shared app shell, theme and branding
├── packages/
│   ├── architecture/Vantigo.Architecture.Tests/ # Module boundary tests
│   ├── configuration/Vantigo.Configuration/ # Shared configuration options
│   ├── contracts/Vantigo.Contracts/   # In-process module contracts
│   └── dataprotection-postgresql/Vantigo.DataProtection.PostgreSql/
├── orchestration/AppHost/             # .NET Aspire composition root
└── assets/                            # Shared branding assets
```

Vantigo is a modular monolith: Customers, Communications and Products are modules
loaded by `Vantigo.Host`, and the host publishes as one container with the API and
the built frontend. The next section describes the architecture in detail.

## Architecture

Vantigo is a **modular monolith**: one process, one container, one PostgreSQL
database — with strict module boundaries so any module can later be extracted
into its own deployable without a rewrite.

### Module boundaries

- A module is a class library (`*.Module`) exposing exactly three integration
  points to the host: `Add<Name>Module(IServiceCollection, IConfiguration)`,
  `Map<Name>Module(IEndpointRouteBuilder)`, and migrate/seed helpers.
- Modules **never reference each other's projects**. The only shared code paths
  are `Vantigo.Contracts` (cross-module interfaces, DTOs, authorization policy
  names and email contracts) and `Vantigo.Configuration` (shared options). The
  `Identity` app module owns the authentication implementation; hosting
  infrastructure (telemetry, SPA serving, command-line parsing) lives in
  `Vantigo.Host`.
- Every module can be turned off per deployment with
  `Modules:<Name>:Enabled`. The flag is one immutable startup decision: a
  disabled module registers no services and no workers, maps no endpoints,
  contributes no permissions, and is neither migrated nor seeded. Combinations
  whose enabled modules would leave a required contract unimplemented are
  rejected at startup, so code consuming another module's *optional* contract
  must tolerate the implementation being absent, and code that cannot work
  without one declares it in `ModuleCompositionValidator`.

### Cross-module communication

- **Synchronous queries** use contract interfaces from `Vantigo.Contracts`
  (e.g. `ICustomerDirectory`): DTOs only, never EF entities, implemented by the
  owning module and consumed through DI. If a module is extracted later, the
  interface gets an HTTP client implementation and consumers stay unchanged.
- **Asynchronous notifications** ("something happened, others may care") should
  use in-process domain events dispatched through a transactional outbox — the
  publishing module stores the event in the same transaction as its state
  change, and a hosted worker dispatches it to handlers with retries.
  This is not built yet; build it together with the first real consumer
  (see ROADMAP). Do not introduce a message broker: Postgres is the queue, and
  every infrastructure piece is multiplied per dedicated customer deployment.
- Never call another module's HTTP endpoints from inside the process, and never
  reach into another module's database schema.

### Database

One PostgreSQL database, one schema per module: `identity`, `customers`,
`products` and `energy`. (`communications` is declared in the contract but has
no schema or Go module yet — see the module list below.) Schemas are hard
boundaries:

- **No cross-schema foreign keys or joins.** Reference other modules' data by
  opaque ID only. This is what keeps a future "move this schema to its own
  server" a connection-string change instead of a data migration.
- Each context has its own `__EFMigrationsHistory` inside its schema.

### Frontend

The host SPA (`@vantigo/app`) owns routing, auth, navigation and the shell;
module packages (`@vantigo/customers-ui`, …) export pages, API clients and
components. Route files in `apps/host/frontend/src/routes/` are thin wrappers
that lazy-import module pages, so each module becomes its own code-split chunk.
Module packages never import from each other; shared UI lives in
`@vantigo/frontend-shell`.

Frontend app packages never import each other. Composition happens only in host routes,
and each package's exported surface (`src/index.ts` and subpath exports) is its contract —
the frontend parallel of `packages/contracts`. Host-owned composition points, such as the
customer detail tab list, are extended by adding entries in the host.

SPA URL convention — *flat primary resources, module-qualified secondary ones*:
primary business nouns users work with daily are top-level (`/customers`,
`/contacts`, `/messages`, `/products`), while supporting or admin concepts stay
qualified by their module (`/products/categories`, `/communications/mailboxes`,
`/communications/suppressions`). Nesting in URLs means *belonging*
(`/customers/:id`), not module bundling. Backend API routes always keep the
module prefix (`/api/v1/customers/contacts`) — that symmetry is what matters
for extraction, not the SPA paths.

## Design principles

- **Vertical-slice modules** — endpoints remain self-contained static classes under
  each module's `Endpoints/<Feature>/` directory.
- **A rich domain model** — value objects own validation and request handlers only
  work with valid domain values.
- **Versioned APIs** — Identity lives under `/api/v1/identity`; business modules use
  `/api/v1/customers`, `/api/v1/products` and `/api/v1/energy`.
- **In-process contracts** — module collaboration uses contracts such as
  `ICustomerDirectory` from `Vantigo.Contracts`, not service-to-service API keys.
- **Form-friendly errors** — validation errors use camelCase JSON field paths.
- **AOT-friendly by default** — registration and entity configuration are explicit.
- **One container** — the host serves every enabled module and the Vite production
  build from one process.

## API conventions

All endpoints are versioned by URL segment and documented per version at
`/openapi/v1.json` (browsable through Scalar when running AppHost). Module prefixes
are `/api/v1/identity`, `/api/v1/customers`, `/api/v1/products` and
`/api/v1/energy`.

Validation errors use RFC 9457 problem details with keys matching the JSON field path:

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

## Running tests

```bash
dotnet build Vantigo.slnx
dotnet test Vantigo.slnx
```

Unit tests run in-process. Integration tests boot the host against PostgreSQL using
[Testcontainers](https://dotnet.testcontainers.org/) and `WebApplicationFactory`,
so a container runtime must be running.

## Database migrations

Each module owns its schema and migration history in the shared database:

```
apps/customers/backend/Customers.Module/Database/Customers/
apps/communications/backend/Communications.Module/Database/Communications/
apps/products/backend/Products.Module/Database/Products/
apps/identity/backend/Identity.Module/Database/Accounts/
```

The host registers one Npgsql data source. EF contexts use the `customers`,
`communications`, `products` and `identity` schemas, each with its own
`__EFMigrationsHistory`. Never mix histories or change a module's schema from
another module.

Use the pinned `dotnet-ef` tool from the repository root. For example, from a module
project directory:

```bash
dotnet tool restore
dotnet ef migrations add <MigrationName> --context CustomersDbContext --output-dir Database/Customers/Migrations
dotnet ef migrations list --context CustomersDbContext
dotnet ef migrations has-pending-model-changes --context CustomersDbContext
```

Use the host's `migrate` command to apply all enabled module and Identity migrations.
The `api` command does not migrate automatically. AppHost runs `seed` after migration
only in Development.

## Development seed data

Deterministic seed code lives under each module's `Database/DevelopmentSeed/`. Changes
to seed data or behavior must include coverage for determinism and restart idempotency.
The seeded `admin@vantigo.local` / `admin` account and relaxed password policy are
Development-only.

## Frontend development

The single frontend is managed with Bun from the repository root:

```bash
bun install --frozen-lockfile
bun run --cwd apps/host/frontend dev

bun run --cwd apps/host/frontend lint
bun run --cwd apps/host/frontend test
bun run --cwd apps/host/frontend build
```

The SPA is embedded into the host's `wwwroot` on `dotnet publish` through the
`BuildFrontend` target in `Vantigo.Host.csproj`. Plain `dotnet build` and `dotnet run`
leave frontend development to Vite.

Module UI packages have their own Vitest suites, run from their directories:

```bash
bun run --cwd apps/customers/frontend test
bun run --cwd apps/communications/frontend test
bun run --cwd apps/products/frontend test
```

## Go server (port in progress)

The .NET backend is being replaced by a single Go binary in `apps/server`
(design: `docs/superpowers/specs/2026-09-10-go-backend-port-design.md`). Until the
cutover the .NET host is what ships; the Go server is built, smoke-tested and
published as PR preview images by the `server-*.yml` workflows.

```bash
mise install              # Go, lint and release tools
mise run server:db        # PostgreSQL for tests (55432) and development (55433)
mise run server:test      # go test against a real PostgreSQL
mise run server:check     # golangci-lint, govulncheck, shellcheck, actionlint, goreleaser check
mise run server:dev       # the api command on http://localhost:8080
mise run smoke            # build the image and smoke-test it end to end
```

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

**Run the Go tests pinned to four CPUs on a many-core machine:**

```bash
cd apps/server && taskset -c 0-3 go test ./... -count=1     # or: -p 4
```

`-p` is the knob, not `-parallel`. `-p` bounds how many **packages** run at
once, which is what bounds how many migrators apply migration 5 at the same
time. `-parallel` bounds parallel tests **within** one package and caps nothing
across packages, so it does not limit concurrent migrators and is not a
substitute.

A full-parallelism run on a many-core host is **not** a valid gate: it fails
with `ERROR: out of shared memory (SQLSTATE 53200)` while applying migration 5,
and it fails in a way that looks like a flaky test rather than a resource limit.

`internal/testdb` gives every test its own database, and each one applies all
migrations. `00005_energy_baseline.sql` creates the range-partitioned
`consumption_intervals` and takes about **648 locks in one transaction** (145 of
them `AccessExclusive`; 25 monthly partitions times roughly 5 relations each).
`max_locks_per_transaction` is not a per-transaction cap but an *aggregate
sizing* parameter: the shared lock table holds roughly
`max_locks_per_transaction × (max_connections + max_prepared_transactions)`
slots, so the default 64 × 100 ≈ 6400. One migrator uses about a tenth of that
and cannot exhaust it; the ceiling is around **9 concurrent migrators**. Eight
pass (8 × 648 = 5184) and forty-four do not (44 × 648 = 28512). The migration
advisory lock does not help, because advisory locks are per-database and every
test has its own database — which is exactly why production, migrating one
database, never sees this.

**The same table, in production: a cold month's first write takes an exclusive
lock.** `EnsureConsumptionPartition` (`internal/energy/consumption.go`) runs
`CREATE TABLE ... PARTITION OF` *inside the request transaction*, so the first
consumption write into a month that has no partition yet holds an `ACCESS
EXCLUSIVE` lock on `energy.consumption_intervals` for the remainder of that
transaction, and every concurrent read or write of the table waits behind it.
This mirrors .NET and needs no code change — the lock is held for one short
transaction, once per month — but it is why the first write after a month
boundary can show a latency spike the next one does not. An operator chasing
that spike should look here rather than at the query plan.

Because the failure strikes whichever tests happen to be creating a database at
that moment, the set of failing tests differs on every run. CI uses four CPUs
and so never hits it.

One caveat: with `max_connections ≤ 10` the shared table holds only ~640 slots
and a **single** migrator would fail. `docker-compose.test.yml` leaves the
default of 100.

### The API contract

`openapi/*.yaml` is the single source of truth for the API: one OpenAPI 3.0 file per module plus `common.yaml` for shared components. Every operation needs an `operationId` and an `x-vantigo-access` rule (`anonymous`, `session`, `scim`, `policy:<Name>[+<Name>…]` or `permission:<module>:<verb>[+<module>:<verb>…]`, every listed name required).

To change the API, edit the YAML, then regenerate and test:

```bash
cd apps/server && go generate ./... && go test ./internal/openapi/... && cd ../..
bun run gen:client
```

`go generate` refreshes the embedded copies and the oapi-codegen server interfaces; `gen:client` refreshes each frontend package's `api-schema.d.ts`. CI fails when either is stale.

`internal/openapi` validates every exchange in `openapi/testdata/exchanges/`, recorded from the .NET integration suites, against the contract. While the .NET host still exists, re-record after changing a .NET endpoint:

```bash
VANTIGO_CONTRACT_RECORD=/tmp/vantigo-exchanges dotnet test Vantigo.slnx
cd apps/server && go run ./internal/openapi/cmd/contract corpus -in /tmp/vantigo-exchanges -out ../../openapi/testdata/exchanges
```

`contract corpus` validates every raw exchange against the contract before it writes anything — the same rules as the test, over the full recording rather than the committed sample — so a re-record that exposes a contract gap fails there, listing each failing operation and reason, and leaves the committed corpus untouched. Only then does it write the sample: at most three exchanges per operation, status and response shape (content type and top-level JSON keys).

`openapi/COVERAGE.md` lists the operations no recorded exchange exercises.

### Identity

The identity module (`internal/identity`) serves `/api/v1/identity/*` from `openapi/identity.yaml`:
sign-in and sessions, users and invitations, MFA and passkeys, RBAC, OIDC and SCIM,
single-tenant, on the platform every later module mounts on.

Development needs nothing beyond `APP_SECRET` (32+ bytes; `mise run server:dev` sets one).
`BOOTSTRAP_SECRET` is optional there: leave it unset and the process generates one and logs
it at WARN on startup — copy it from the log instead of choosing your own.

To bootstrap the installation's first Owner, either open `/setup` in the SPA once it points
at the Go server, or call the endpoint directly with the logged secret:

```bash
curl -X POST http://localhost:8080/api/v1/identity/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"secret":"<the logged bootstrap secret>","email":"owner@example.test","displayName":"Owner","password":"a strong password"}'
```

`GET /api/v1/identity/bootstrap-status` reports whether bootstrap is still available
(`{"available":true}`) before you call it.

Behind a reverse proxy, set `TRUSTED_PROXY_HOPS` to the number of proxies in front of the
server. Outside development it also needs `TRUSTED_PROXY_CIDRS`, the proxies' own addresses,
or configuration fails: forwarded headers are honoured only from a peer inside that list.

When rotating the SCIM token, `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` must still be in the future
at every start, so a restart after the overlap window fails configuration: remove
`SCIM_PREVIOUS_TOKEN` and its expiry once the window has passed, as .NET required.

`sqlc` (the identity schema's query generator) comes from mise, not a separate install; it
runs as part of the usual generate step:

```bash
cd apps/server && mise exec -- go generate ./...
```

Identity's tests run every HTTP exchange through a contract-validating client: a response
that does not match `identity.yaml` fails the test that produced it. `go test
./internal/identity/` also gates on operation coverage — every operation in the contract
must have been exercised by at least one successful exchange, with no allow-list, so a newly
added operation without a passing test fails the whole package.

### Customers, products, energy and communications

Four business modules mount on that platform, each serving its own contract and
owning its own schema:

- `internal/customers` → `/api/v1/customers/*` from `openapi/customers.yaml`:
  customers, contacts, customer-contact associations, legal identity, the
  customer timeline and the Brønnøysundregisteret (Brreg) lookup.
- `internal/products` → `/api/v1/products/*` from `openapi/products.yaml`:
  products, variants, prices, categories and tax categories.
- `internal/energy` → `/api/v1/energy/*` from `openapi/energy.yaml`: metering
  points, meters, supply periods, consumption and the two aggregations.
- `internal/communications` → `/api/v1/communications/*` from
  `openapi/communications.yaml`: channels (SMTP only), conversations and their
  messages, the composer, staged attachments on object storage, delivery and
  its event log, tags, suppressions, and the AI draft and customer-suggestion
  features. It also owns the outbox and the module's three background workers.

`MODULES` chooses which of them a deployment serves: a comma-separated list,
parsed once at startup, defaulting to `customers,products,energy`. Identity is
always mounted and is never listed. A name the binary does not know fails
startup, naming the name and the known set. `energy` (and, later,
`communications`) reads customer data through `contracts.CustomerDirectory`, so
either without `customers` fails startup naming both. A disabled module
contributes no route, no permission and no contract path, and its paths answer
the `/api` catch-all 404 — but every schema is migrated regardless, so enabling
a module later needs no migration.

`communications` is now a real module and is on by default. Until its own
sub-project's final task it was contract-only: the name passed validation
(`knownModules` is derived from `openapi.Modules`, the single place the
contract names are written) while no `Module()` existed for `module.Compose`
to mount, so `MODULES=communications` started cleanly and served nothing. That
gap is closed — `communications.Module()` mounts
`/api/v1/communications/*` from `openapi/communications.yaml` and contributes
three background workers — and the name has joined `defaultModules`.

**The one thing to know before enabling it: this port is outbound-only, and
that is a deliberate consequence of the scope cut, not a gap to fix in
passing.** The Mailgun inbound webhook and the inbound worker are removed, and
IMAP/inbound synchronisation never existed, so **no path in this codebase
writes a `direction='inbound'` message**. Three user-visible behaviours follow,
all of them contract-complete and all of them permanently in their negative
state: a conversation created through `POST /conversations` cannot be replied
to (reply answers 422 `recipients_missing`, because recipients resolve from the
latest inbound message), `canReplyAll` is always false with an empty
`replyAllCc`, and the AI draft builds its context from inbound messages and so
drafts against an empty one, recording `product_data=false`. Attachments can be
staged but never sent, and suppression is enforced only by the outbox worker,
reply's own check being unreachable. The endpoints are ported faithfully and
kept, so the existing frontend works and an inbound provider is additive.

The module's deliberate divergences from the .NET original are listed in
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

The customer directory is the only sanctioned cross-module read. No module
imports another (enforced by depguard) and no module queries another's schema
(enforced by `internal/db/schema_test.go`).

A module can also contribute background workers (`internal/worker`: a `Worker`
is `Run(ctx) error` plus a name and a poll interval, resolved from every
enabled module's `Module.Workers` the same way `Module.Directory` is, and
started by `internal/worker.Runner`). `WORKERS_IN_PROCESS` (`0`/`1`, **default
`1`**) controls whether the `api` command also runs them in-process alongside
serving; `worker` mode always runs them and `server` mode never does,
regardless of this setting — `server` is what a fleet of stateless replicas
runs, and every replica racing to claim the same background job is exactly
what `server` must not do. Communications contributes the only three today:
outbox delivery, retention, and attachment cleanup.

Only the retention worker takes an advisory lease, so only it runs on one
replica at a time. The other two rely on a conditional-update claim — the
`UPDATE ... WHERE <the same predicate the candidate select used>` *is* the
lock — so every replica runs them every cycle and the database decides who
wins each row. Adding an advisory lock to either would be a defect, not a
hardening: it is not how the original behaves and the claim already provides
the exclusion.

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

Each module's tests work like identity's: every HTTP exchange runs through a
contract-validating client, so a response that does not match the module's YAML
fails the test that produced it, and the package gates on operation coverage —
every operation in the contract must have been exercised by at least one
successful exchange, with no allow-list, so a newly added operation without a
passing test fails the whole package. No test touches the network; the Brreg
client dials a fake transport.

## Commit conventions

Commits follow [Conventional Commits](https://www.conventionalcommits.org/), scoped to
the affected module where applicable:

```
feat(customers): add customer create, get and list endpoints
chore(init): add biome config and run on solution
```

Pre-commit hooks (Husky + lint-staged) run Biome on frontend files and `dotnet format`
on C# files.
