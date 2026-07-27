# Contributing to Vantigo

Thank you for your interest in contributing! This document covers everything you need
to get productive in the codebase.

## Getting the stack running

Follow the [Getting started](README.md#getting-started) section in the README —
`dotnet tool restore` plus `dotnet run --project orchestration/AppHost` gives you the
full environment: PostgreSQL, the APIs (with migrations applied automatically), the
frontends and the Scalar API reference.

## Project layout

```
vantigo/
├── apps/
│   └── customers/
│       ├── backend/
│       │   ├── Customers.Api/         # ASP.NET Core minimal API
│       │   └── Customers.Api.Tests/   # Unit + integration tests
│       └── frontend/                  # React SPA (Vite, TanStack Router, Mantine)
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
  Entity configurations are applied explicitly in `AppDbContext.OnModelCreating`,
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

EF Core migrations live next to the owning API (e.g.
`apps/customers/backend/Customers.Api/Database/Migrations`). The `dotnet-ef` tool is
pinned in the repo's tool manifest, so everyone uses the same version:

```bash
dotnet tool restore

cd apps/customers/backend/Customers.Api
dotnet ef migrations add <MigrationName> -o Database/Migrations
```

Migrations are applied automatically when the API starts in development. If you change
the EF model, verify nothing is pending with:

```bash
dotnet ef migrations has-pending-model-changes
```

## Frontend development

Aspire runs the frontend for you, but it can also be run standalone. Dependencies are
managed with [Bun](https://bun.sh) workspaces from the repository root:

```bash
bun install
cd apps/customers/frontend
bun run dev
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
