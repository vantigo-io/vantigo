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
│   └── products/
│       ├── backend/
│       │   ├── Products.Module/       # Products vertical slice
│       │   └── Products.Module.Tests/
│       └── frontend/                  # @vantigo/products-ui
├── packages/
│   ├── configuration/Vantigo.Configuration/ # Shared configuration options
│   ├── contracts/Vantigo.Contracts/   # In-process module contracts
│   ├── identity/Vantigo.Identity/     # Shared authentication and Identity
│   │   └── Vantigo.Identity.Tests/
│   └── frontend-shell/                # Shared app shell, theme and branding
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
  are `Vantigo.Contracts` (cross-module interfaces and DTOs), `Vantigo.Identity`
  (authentication) and `Vantigo.Configuration` (shared options). Hosting
  infrastructure (telemetry, SPA serving, command-line parsing) lives in
  `Vantigo.Host`.
- Every module can be turned off per deployment with
  `Modules:<Name>:Enabled` — code consuming another module's contract must
  tolerate the implementation being absent.

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
`communications` and `products`. Schemas are hard boundaries:

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
  `/api/v1/customers`, `/api/v1/communications` and `/api/v1/products`.
- **In-process contracts** — module collaboration uses contracts such as
  `ICustomerDirectory` from `Vantigo.Contracts`, not service-to-service API keys.
- **Form-friendly errors** — validation errors use camelCase JSON field paths.
- **AOT-friendly by default** — registration and entity configuration are explicit.
- **One container** — the host serves every enabled module and the Vite production
  build from one process.

## API conventions

All endpoints are versioned by URL segment and documented per version at
`/openapi/v1.json` (browsable through Scalar when running AppHost). Module prefixes
are `/api/v1/identity`, `/api/v1/customers`, `/api/v1/communications` and
`/api/v1/products`.

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
packages/identity/Vantigo.Identity/Database/Accounts/
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

## Commit conventions

Commits follow [Conventional Commits](https://www.conventionalcommits.org/), scoped to
the affected module where applicable:

```
feat(customers): add customer create, get and list endpoints
chore(init): add biome config and run on solution
```

Pre-commit hooks (Husky + lint-staged) run Biome on frontend files and `dotnet format`
on C# files.
