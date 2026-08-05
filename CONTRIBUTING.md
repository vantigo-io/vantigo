# Contributing to Vantigo

Thank you for your interest in contributing! This document covers everything you need
to get productive in the codebase.

## Getting the stack running

Follow the [Getting started](README.md#getting-started) section in the README —
`bun install --frozen-lockfile`, `dotnet tool restore`, plus
`dotnet run --project orchestration/AppHost` gives you the full environment: PostgreSQL,
the APIs, the frontends and the Scalar API reference.
Aspire starts the database and explicitly selects the `migrate`, `seed`, and `api`
profiles for each API in that order. In production, `migrate` is a terminating job:
wait for it to succeed before starting the API with the explicit `api` command. Do not
run `seed` in production; it is Development-only.

Each API executable requires one of `api`, `migrate`, or `seed`; no command prints usage
and exits nonzero. `migrate` applies migrations and exits, `seed` runs deterministic
Development-only fixtures and exits, and `api` only hosts the API. The `api` command
does not automatically migrate or seed. To run a command directly, use the matching
Development launch profile, for example:

```bash
dotnet run --project apps/customers/backend/Customers.Api --launch-profile migrate
dotnet run --project apps/customers/backend/Customers.Api --launch-profile seed
dotnet run --project apps/customers/backend/Customers.Api --launch-profile api

dotnet run --project apps/communications/backend/Communications.Api --launch-profile migrate
dotnet run --project apps/communications/backend/Communications.Api --launch-profile seed
dotnet run --project apps/communications/backend/Communications.Api --launch-profile api
```

## Project layout

```
vantigo/
├── apps/
│   ├── customers/
│   │   ├── backend/
│   │   │   ├── Customers.Api/         # ASP.NET Core minimal API
│   │   │   └── Customers.Api.Tests/   # Unit + integration tests
│   │   └── frontend/                  # React SPA (Vite, TanStack Router, Mantine)
│   └── communications/
│       ├── backend/
│       │   ├── Communications.Api/       # ASP.NET Core minimal API
│       │   └── Communications.Api.Tests/ # Unit + integration tests
│       └── frontend/                    # React SPA (Vite)
├── orchestration/
│   └── AppHost/                       # .NET Aspire composition root
└── assets/                            # Shared branding assets
```

Each application is a vertical slice with its own backend and frontend. New endpoints
are expected to follow the design principles and API conventions below.

## Design principles

- **Vertical-slice endpoints** — every endpoint is a self-contained static class
  (request, handler, response in one file), grouped per feature under
  `Endpoints/<Feature>/`. Shared response DTOs live in `Endpoints/<Feature>/Dtos/`
  (feature-scoped) or `Endpoints/Dtos/` (cross-feature, e.g. `PaginatedResponse<T>`).
- **A rich domain model** — values like `CountryCode`, `LegalId`, and `FriendlyName`
  are validated value objects that own their validation in one place. The constructor
  throws `DomainException` as an invariant guard, while the non-throwing
  `TryCreate(...)` overload powers request validation — so handlers only ever work
  with provably valid data and never need try/catch.
- **Versioned APIs** — all endpoints live under a URL version prefix (`/api/v1/...`)
  backed by [Asp.Versioning](https://github.com/dotnet/aspnet-api-versioning), with a
  separate OpenAPI document generated per version.
- **Form-friendly errors** — collect all validation errors and return
  `TypedResults.ValidationProblem` with keys matching the request's camelCase JSON
  paths (`name`, `identity.country`, ...), so frontends can map them directly onto
  form libraries like TanStack Form and Mantine Form.
- **AOT-friendly by default** — no assembly scanning or reflection-based registration.
  Entity configurations are applied explicitly in `CustomersDbContext.OnModelCreating`,
  and sorting/filtering uses whitelisted switch expressions rather than dynamic LINQ.
- **One container per app** — in production, each application ships as a single
  container image in which the .NET API also serves the built frontend. In
  development, Aspire runs the API and the Vite dev server side by side.

## API conventions

All APIs are versioned by URL segment and documented per version at
`/openapi/v1.json` (browsable through Scalar when running the AppHost).

**Validation errors** are returned as RFC 9457 problem details with errors keyed by
the JSON field path of the offending request field:

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

**List endpoints** share a single pagination envelope, with `page`, `pageSize`,
`sortBy`, `sortDirection` and `search` query parameters:

```json
{
  "data": [{ "id": 1001, "name": "Acme", "identity": null }],
  "pagination": {
    "page": 1,
    "pageSize": 25,
    "totalCount": 137,
    "totalPages": 6,
    "hasNextPage": true,
    "hasPreviousPage": false
  }
}
```

## Running tests

```bash
dotnet test
```

Unit tests run in-process. Integration tests boot the real API against a real
PostgreSQL instance using [Testcontainers](https://dotnet.testcontainers.org/) and
`WebApplicationFactory`, so a container runtime must be running.

<details>
<summary>Using Colima instead of Docker Desktop?</summary>

Testcontainers' resource reaper needs the Docker socket at its in-VM path. Either run
tests with:

```bash
TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock dotnet test
```

or make it permanent by adding this line to `~/.testcontainers.properties`:

```properties
docker.socket.override=/var/run/docker.sock
```

</details>

New endpoints should come with both unit tests (for any new domain logic) and
integration tests that exercise the endpoint over HTTP.

## Database migrations

The Customers API keeps two EF Core contexts in the same assembly and PostgreSQL
database:

```
Customers.Api/Database/
├── Customers/
│   ├── CustomersDbContext.cs
│   ├── Configurations/
│   └── Migrations/
└── Accounts/
    ├── AccountsDbContext.cs
    ├── ApplicationUser.cs, BootstrapState.cs, Invitation.cs
    └── Migrations/
```

`CustomersDbContext` owns the customer, contact, relationship, and timeline tables
in the default `public` schema. Its migration history is the default
`public.__EFMigrationsHistory`. `AccountsDbContext` owns Identity and account
entities in the `accounts` schema, with its separate
`accounts.__EFMigrationsHistory`. The histories must never be mixed: always select
the context explicitly when using `dotnet ef`.

Both contexts use the same `NpgsqlDataSource`, and therefore the same underlying
ADO.NET physical connection pool. This is not EF `DbContext` pooling. Context
instances still have independent lifetimes, change tracking, and transactions; they
do not share tracked entities or a transaction automatically.

The `dotnet-ef` tool is pinned in the repo's tool manifest, so everyone uses the same
version:

```bash
dotnet tool restore

cd apps/customers/backend/Customers.Api
```

Run each command with the context and output directory that own the change.

### Customers context

```bash
dotnet ef migrations add <MigrationName> --context CustomersDbContext --output-dir Database/Customers/Migrations
dotnet ef migrations list --context CustomersDbContext
dotnet ef migrations script --context CustomersDbContext
dotnet ef database update --context CustomersDbContext
dotnet ef migrations has-pending-model-changes --context CustomersDbContext
```

### Accounts context

```bash
dotnet ef migrations add <MigrationName> --context AccountsDbContext --output-dir Database/Accounts/Migrations
dotnet ef migrations list --context AccountsDbContext
dotnet ef migrations script --context AccountsDbContext
dotnet ef database update --context AccountsDbContext
dotnet ef migrations has-pending-model-changes --context AccountsDbContext
```

Use the `migrate` command to apply both contexts; it exits when complete. The `api`
command does not run migrations. Migrations run only through `migrate`: in production,
wait for that terminating job to succeed before starting `api`. Development Aspire
also runs `seed` after `migrate` and before `api`; `seed` must not be used in production.
If you change either EF model, run that context's pending-model check:

```bash
dotnet ef migrations has-pending-model-changes --context CustomersDbContext
dotnet ef migrations has-pending-model-changes --context AccountsDbContext
```

## Development seed data

Deterministic seed code lives under each API's `Database/DevelopmentSeed/` and uses the
local Bogus `UseSeed`. Changes to seed data or behavior must include coverage for
determinism and restart idempotency.

The seeded `admin` password and relaxed password policy are deliberately weak and apply
only to Development/local use; production retains the normal password requirements.

The `seed` command is only available in Development and exits after seeding. Aspire
explicitly selects `migrate`, then `seed`, then `api` automatically. In production, use
only the terminating `migrate` job followed by `api`; `seed` must not be run. Development
fixture counts are configured under
`Development:Seed:Data`: `Customers` and `Contacts` default to 6 each, and `Messages`
defaults to 2. Counts range from 0 to 100; higher values add deterministic data, while
lowering a value does not delete existing local seed data. Environment variables use the
matching form, such as `Development__Seed__Data__Customers=12`.

## Frontend development

Aspire runs the frontend for you, but it can also be run standalone. Dependencies are
managed with [Bun](https://bun.sh) workspaces from the repository root:

```bash
bun install --frozen-lockfile

# Run either frontend from the repository root
bun run --cwd apps/customers/frontend dev
bun run --cwd apps/communications/frontend dev

# Validate both frontends from the repository root
bun run frontend:lint
bun run frontend:test
bun run frontend:build
```

The SPA is built and embedded into the API's `wwwroot` **only on `dotnet publish`**
(see the `BuildFrontend` target in `Customers.Api.csproj`). Plain `dotnet build` and
`dotnet run` never touch the frontend — in development the Vite dev server serves the
SPA and proxies `/api` to the API.

## Commit conventions

Commits follow [Conventional Commits](https://www.conventionalcommits.org/), scoped to
the affected application where applicable:

```
feat(customers): add customer create, get and list endpoints
chore(init): add biome config and run on solution
```

Pre-commit hooks (Husky + lint-staged) automatically run Biome on frontend files and
`dotnet format` on C# files, so formatting takes care of itself.
