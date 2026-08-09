<div align="center">

<img src="assets/logo.png" alt="Vantigo logo" width="140" />

# Vantigo

**The open-source, all-in-one platform for running your business.**

<br />

![.NET 10](https://img.shields.io/badge/.NET%2010-512BD4?style=for-the-badge&logo=dotnet&logoColor=white)
![C# 14](https://img.shields.io/badge/C%23%2014-239120?style=for-the-badge&logo=sharp&logoColor=white)
![ASP.NET Core](https://img.shields.io/badge/ASP.NET%20Core-512BD4?style=for-the-badge&logo=dotnet&logoColor=white)
![EF Core](https://img.shields.io/badge/EF%20Core-6C3483?style=for-the-badge&logo=dotnet&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-4169E1?style=for-the-badge&logo=postgresql&logoColor=white)
![.NET Aspire](https://img.shields.io/badge/.NET%20Aspire-B23BEF?style=for-the-badge&logo=dotnet&logoColor=white)
![OpenAPI](https://img.shields.io/badge/OpenAPI-6BA539?style=for-the-badge&logo=openapiinitiative&logoColor=white)
![xUnit](https://img.shields.io/badge/xUnit-5E1F87?style=for-the-badge&logo=dotnet&logoColor=white)
![Testcontainers](https://img.shields.io/badge/Testcontainers-2496ED?style=for-the-badge&logo=docker&logoColor=white)

![TypeScript](https://img.shields.io/badge/TypeScript-3178C6?style=for-the-badge&logo=typescript&logoColor=white)
![React 19](https://img.shields.io/badge/React%2019-087EA4?style=for-the-badge&logo=react&logoColor=white)
![Vite](https://img.shields.io/badge/Vite-646CFF?style=for-the-badge&logo=vite&logoColor=white)
![TanStack Router](https://img.shields.io/badge/TanStack%20Router-FF4154?style=for-the-badge&logo=reactquery&logoColor=white)
![Mantine](https://img.shields.io/badge/Mantine-339AF0?style=for-the-badge&logo=mantine&logoColor=white)
![Biome](https://img.shields.io/badge/Biome-60A5FA?style=for-the-badge&logo=biome&logoColor=white)

![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue?style=for-the-badge)

</div>

---

## About

Vantigo is an **all-in-one solution for running a business**. It bundles the essential
applications a company needs — and, crucially, they are **deeply integrated with each
other**. When you run your business on Vantigo, you are not stitching together a dozen
disconnected systems: everything speaks the same language out of the box.

The entire stack is **open source and free to run yourself**. If self-hosting isn't
your thing, a managed **SaaS offering** is available where we run the platform for you.

## Applications

| Application   | Description                                                        | Status            |
| ------------- | ------------------------------------------------------------------ | ----------------- |
| **Customers** | Manage your customers and their legal identities across countries. | 🚧 In development |
| **Products**  | The catalog of goods and services the company sells, with prices.   | 🚧 In development |

More applications are on the way — each one lands as a new vertical slice in
[`apps/`](apps/) and plugs into the same platform conventions.

## Quick start with Docker

The fastest way to try Vantigo is with the pre-built container images and the
ready-made Docker Compose stack in [`deploy/compose/`](deploy/compose/). All you
need is Docker — no SDKs or build tools.

```bash
mkdir vantigo && cd vantigo
base=https://raw.githubusercontent.com/vantigo-io/vantigo/main/deploy/compose
curl -fsSLO "$base/compose.yaml"
curl -fsSLO "$base/.env.example"
curl -fsSLO "$base/customers.env.example"
curl -fsSLO "$base/communications.env.example"
curl -fsSLO "$base/products.env.example"

cp .env.example .env                              # set a database password here
cp customers.env.example customers.env
cp communications.env.example communications.env
cp products.env.example products.env

docker compose up -d
```

Compose starts PostgreSQL, applies each application's database migrations, and
brings up **Customers** on <http://localhost:8080>, **Communications** on
<http://localhost:8081>, and **Products** on <http://localhost:8082>. Then visit <http://localhost:8080/setup> to create your
first Owner account — the one-time bootstrap secret is printed in the Customers
logs (`docker compose logs customers`) unless you configured one yourself.

The [compose guide](deploy/compose/README.md) covers configuration, first
sign-in, production notes and upgrades in more detail.

## Architecture

Vantigo is a monorepo of independently packaged applications, composed locally by a
single [.NET Aspire](https://learn.microsoft.com/dotnet/aspire/) AppHost:

```
vantigo/
├── apps/
│   ├── customers/
│   │   ├── backend/
│   │   │   ├── Customers.Api/         # ASP.NET Core minimal API
│   │   │   └── Customers.Api.Tests/   # Unit + integration tests
│   │   └── frontend/                  # React SPA (Vite, TanStack Router, Mantine)
│   ├── communications/
│   │   ├── backend/
│   │   │   ├── Communications.Api/       # ASP.NET Core minimal API
│   │   │   └── Communications.Api.Tests/ # Unit + integration tests
│   │   └── frontend/                    # React SPA (Vite)
│   └── products/
│       ├── backend/
│       │   ├── Products.Api/          # ASP.NET Core minimal API
│       │   └── Products.Api.Tests/    # Unit + integration tests
│       └── frontend/                  # React SPA (Vite)
├── orchestration/
│   └── AppHost/                       # .NET Aspire composition root
├── deploy/
│   └── compose/                       # Ready-made Docker Compose stack
└── assets/                            # Shared branding assets
```

Each application ships as a single container image in production, where the .NET API
also serves the built frontend. In development, .NET Aspire runs everything side by
side with one command. Curious about the design principles and API conventions behind
the codebase? They're covered in the [contributing guide](CONTRIBUTING.md).

## Developing from source

### Prerequisites

- [.NET SDK](https://dotnet.microsoft.com/download) matching the baseline pinned
  in [`global.json`](global.json)
- [Bun](https://bun.sh), with the version pinned in [`.bun-version`](.bun-version)
- A Docker-compatible container runtime (Docker Desktop, [Colima](https://github.com/abiosoft/colima), Podman, ...)

### Run the full stack

```bash
git clone https://github.com/vantigo-io/vantigo.git
cd vantigo

# Install the root Bun workspace dependencies
bun install --frozen-lockfile

# Restore pinned local tools (dotnet-ef)
dotnet tool restore

# Start everything: PostgreSQL, APIs, frontends and the Scalar API reference
dotnet run --project orchestration/AppHost
```

The Aspire dashboard opens automatically and shows every running resource with logs,
traces and endpoints. The AppHost provisions PostgreSQL and its application databases,
runs the root Bun installer for both frontends, and explicitly selects each API's
`migrate`, `seed`, and `api` profiles in that order:

- **bun-install** — root Bun workspace dependency installation
- **postgres**, **customers-db**, and **communications-db** — PostgreSQL and application databases
- **customers-migrate**, **customers-seed**, and **customers-api** — the Customers lifecycle and API
- **customers-frontend** — the Customers SPA served by the Vite dev server
- **communications-migrate**, **communications-seed**, and **communications-api** — the Communications lifecycle and API
- **communications-frontend** — the Communications SPA served by the Vite dev server
- **products-migrate**, **products-seed**, and **products-api** — the Products lifecycle and API
- **products-frontend** — the Products SPA served by the Vite dev server
- **scalar** — interactive API reference for every registered API

That's it — no manual database setup, connection strings or environment files needed.

### Aspire troubleshooting

- **Root Bun installer fails:** From the repository root, run `command -v bun` and
  `bun --version` to verify that the Bun version pinned in `.bun-version` is
  available, then retry `bun install --frozen-lockfile`. In the Aspire dashboard,
  open the `bun-install` resource and inspect its logs for the installer error.
- **A Vite frontend fails:** Inspect the logs for the affected `customers-frontend` or
  `communications-frontend` resource. You can also reproduce it from the repository
  root with `bun run --cwd apps/<application>/frontend dev`.
- **An API is not ready:** Aspire runs each API's `migrate`, then `seed`, then `api`
  profile. Check those resource logs and wait for the preceding profile to complete.
- **Database or container failures:** Check that the Docker-compatible runtime is
  running and inspect the `postgres` resource logs in the Aspire dashboard.

### Standalone frontend development

From the repository root, start either frontend without changing directories:

```bash
bun run --cwd apps/customers/frontend dev
bun run --cwd apps/communications/frontend dev
bun run --cwd apps/products/frontend dev
```

The root convenience scripts validate both frontends:

```bash
bun run frontend:lint
bun run frontend:test
bun run frontend:build
```

### Direct API commands

Each API executable requires exactly one command: `api`, `migrate`, or `seed`. Running
the executable without a command prints usage and exits nonzero. `migrate` applies the
database migrations and exits; `seed` runs the deterministic Development-only seed and
exits; `api` only hosts the API and does not automatically migrate or seed the database.

For example:

```bash
dotnet run --project apps/customers/backend/Customers.Api --launch-profile migrate
dotnet run --project apps/customers/backend/Customers.Api --launch-profile seed
dotnet run --project apps/customers/backend/Customers.Api --launch-profile api

dotnet run --project apps/communications/backend/Communications.Api --launch-profile migrate
dotnet run --project apps/communications/backend/Communications.Api --launch-profile seed
dotnet run --project apps/communications/backend/Communications.Api --launch-profile api

dotnet run --project apps/products/backend/Products.Api --launch-profile migrate
dotnet run --project apps/products/backend/Products.Api --launch-profile seed
dotnet run --project apps/products/backend/Products.Api --launch-profile api
```

Aspire does not use the no-argument `dev` profile for lifecycle ordering. That profile
remains a Development convenience profile with empty command arguments. For production,
run the same image as a terminating `migrate` job, wait for it to succeed, and then run
the image with the long-running `api` command. `seed` is Development-only and must not
be used as a production deployment job.

### Development-only seed data

In `Development`, the `seed` command creates deterministic fixtures for Customers and
Communications. Aspire runs this command automatically after migrations. Seed data is
Development-only and includes synthetic data. Use `admin@vantigo.local` / `admin` to
sign in as `Administrator`.
This deliberately weak password and relaxed password policy are for Development/local use
only; production retains the normal password requirements. In a real deployment there
is no seeded account — the first Owner is created through the `/setup` bootstrap flow
described in the [quick start](#quick-start-with-docker).

The JSON configuration section is `Development:Seed`:

```json
{
  "Development": {
    "Seed": {
      "Enabled": true,
      "Data": {
        "Customers": 6,
        "Contacts": 6,
        "Messages": 2
      }
    }
  }
}
```

`Development:Seed:Data` controls deterministic fixture counts. The Customers API supports
`Customers` and `Contacts` (6 each by default); Communications supports `Messages` (2 by
default). Each count must be between 0 and 100. Higher counts add deterministic data, while
lowering a value does not delete existing local seed data. Use the matching environment
variable form, for example `Development__Seed__Data__Customers=12`.

To opt out, set `Development__Seed__Enabled=false`. Seeding is never enabled outside
`Development`.

For self-hosted Customers authentication, deployment configuration, and production
migration guidance, see [Customers authentication](docs/customers-authentication.md).

Ready to dig into the code? Head over to the
[contributing guide](CONTRIBUTING.md) for the design principles, API conventions,
testing and database migrations.

## Self-hosting

Each Vantigo application ships as a single, multi-architecture container image that
runs the .NET API and serves the production frontend build from the same process.
Images are published to GHCR on every release and signed with
[Cosign](https://docs.sigstore.dev/cosign/):

| Application    | Image                              |
| -------------- | ---------------------------------- |
| Customers      | `ghcr.io/vantigo-io/customers`      |
| Communications | `ghcr.io/vantigo-io/communications` |

Available tags: `latest`, `vX`, `vX.Y`, and `vX.Y.Z` — pin `vX.Y.Z` for
reproducible deployments. Verify a signature with:

```bash
cosign verify ghcr.io/vantigo-io/customers:latest \
  --certificate-identity-regexp 'https://github.com/vantigo-io/vantigo' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The easiest deployment is the [Docker Compose stack](deploy/compose/) from the
quick start. To integrate with your own infrastructure instead, bring your own
PostgreSQL and run each image with the terminating `migrate` command first, then
the long-running `api` command:

```bash
docker run --rm \
  -e ConnectionStrings__Postgresql="Host=your-postgres;Database=customers;Username=...;Password=..." \
  ghcr.io/vantigo-io/customers migrate

docker run -d \
  --name vantigo-customers \
  -p 8080:8080 \
  -e ConnectionStrings__Postgresql="Host=your-postgres;Database=customers;Username=...;Password=..." \
  -e DataProtection__KeysPath=/var/lib/vantigo/dataprotection \
  -v vantigo-customers-dataprotection:/var/lib/vantigo/dataprotection \
  ghcr.io/vantigo-io/customers api
```

A persistent `DataProtection__KeysPath` volume is required for the Customers app so
sign-in cookies and account tokens survive restarts. Do not run `seed` in
production; it is only for Development. Authentication, reverse-proxy and full
configuration guidance lives in
[Customers authentication](docs/customers-authentication.md).

Prefer not to host anything at all? The managed **Vantigo SaaS** runs the exact same
open-source stack for you.

## License

Vantigo is licensed under the [GNU Affero General Public License v3.0](LICENSE)
(AGPL-3.0). You are free to use, modify and self-host it — if you offer a modified
version of Vantigo to others over a network, you must make your modifications
available under the same license.
