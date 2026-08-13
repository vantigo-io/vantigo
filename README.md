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
modules a company needs — and, crucially, they are **deeply integrated with each
other**. When you run your business on Vantigo, you are not stitching together a dozen
disconnected systems: everything speaks the same language out of the box.

The entire stack is **open source and free to run yourself**. If self-hosting isn't
your thing, a managed **SaaS offering** is available where we run the platform for you.

## Modules

| Module             | Description                                                          | Status            |
| ------------------ | -------------------------------------------------------------------- | ----------------- |
| **Customers**      | Manage your customers and their legal identities across countries.   | 🚧 In development |
| **Communications** | Send, receive and archive business email across shared mailboxes.    | 🚧 In development |
| **Products**       | The catalog of goods and services the company sells, with prices.    | 🚧 In development |

More modules are on the way — each one lands as a new vertical slice in
[`apps/`](apps/) and plugs into the same host application and platform conventions.

## Quick start with Docker

The fastest way to try Vantigo is with the pre-built container images and the
ready-made Docker Compose stack in [`deploy/compose/`](deploy/compose/). All you
need is Docker — no SDKs or build tools.

```bash
mkdir vantigo && cd vantigo
base=https://raw.githubusercontent.com/vantigo-io/vantigo/main/deploy/compose
curl -fsSLO "$base/compose.yaml"
curl -fsSLO "$base/.env.example"
curl -fsSLO "$base/vantigo.env.example"

cp .env.example .env                              # set a database password here
cp vantigo.env.example vantigo.env

docker compose up -d
```

Compose starts PostgreSQL, applies the shared database migrations, and brings up
the single Vantigo application on <http://localhost:8080>. Customers,
Communications and Products are modules in that application. Visit
<http://localhost:8080/setup> to create your first Owner account — the one-time
bootstrap secret is printed in the Vantigo logs (`docker compose logs vantigo`)
unless you configured one yourself.

The [compose guide](deploy/compose/README.md) covers configuration, first
sign-in, production notes and upgrades in more detail.

## Architecture

Vantigo is a modular monolith: one host application contains the Customers,
Communications, Products and Identity modules, with shared contracts. A single
[.NET Aspire](https://learn.microsoft.com/dotnet/aspire/) AppHost composes the local
development environment:

```
vantigo/
├── apps/
│   ├── host/
│   │   ├── backend/Vantigo.Host/      # ASP.NET Core host and API
│   │   └── frontend/                  # Single React SPA (Vite)
│   ├── identity/backend/
│   │   ├── Identity.Module/           # Authentication and Identity module
│   │   └── Identity.Module.Tests/
│   ├── customers/backend/
│   │   ├── Customers.Module/          # Customers vertical slice
│   │   └── Customers.Module.Tests/
│   ├── communications/backend/
│   │   ├── Communications.Module/     # Communications vertical slice
│   │   └── Communications.Module.Tests/
│   └── products/backend/
│       ├── Products.Module/           # Products vertical slice
│       └── Products.Module.Tests/
├── packages/
│   ├── contracts/Vantigo.Contracts/   # In-process module contracts
│   ├── configuration/Vantigo.Configuration/ # Shared configuration options
│   └── dataprotection-postgresql/Vantigo.DataProtection.PostgreSql/
├── orchestration/
│   └── AppHost/                       # .NET Aspire composition root
├── deploy/
│   └── compose/                       # Ready-made Docker Compose stack
└── assets/                            # Shared branding assets
```

The host ships as one container image in production, where ASP.NET Core serves the
built frontend and all enabled modules. One PostgreSQL database is split into the
`identity`, `customers`, `communications` and `products` schemas. Curious about the
design principles and API conventions behind the codebase? They're covered in the
[contributing guide](CONTRIBUTING.md).

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

# Start everything: PostgreSQL, the host, frontend and Scalar API reference
dotnet run --project orchestration/AppHost
```

The Aspire dashboard opens automatically and shows every running resource with logs,
traces and endpoints. The AppHost provisions PostgreSQL and the shared `vantigo`
database, runs the root Bun installer, and explicitly selects the host's `migrate`,
`seed`, and `api` profiles in that order:

- **bun-install** — root Bun workspace dependency installation
- **postgres** and **vantigo-db** — PostgreSQL and the shared application database
- **vantigo-migrate**, **vantigo-seed**, and **vantigo-api** — the host lifecycle and API
- **vantigo-frontend** — the single SPA served by the Vite dev server
- **scalar** — interactive API reference for every registered API

That's it — no manual database setup, connection strings or environment files needed.

### Aspire troubleshooting

- **Root Bun installer fails:** From the repository root, run `command -v bun` and
  `bun --version` to verify that the Bun version pinned in `.bun-version` is
  available, then retry `bun install --frozen-lockfile`. In the Aspire dashboard,
  open the `bun-install` resource and inspect its logs for the installer error.
- **The Vite frontend fails:** Inspect the `vantigo-frontend` logs. You can also
  reproduce it from the repository root with `bun run --cwd apps/host/frontend dev`.
- **The host is not ready:** Aspire runs `migrate`, then `seed`, then `api`. Check
  those resource logs and wait for the preceding profile to complete.
- **Database or container failures:** Check that the Docker-compatible runtime is
  running and inspect the `postgres` resource logs in the Aspire dashboard.

### Frontend development

From the repository root, start any frontend without changing directories:

```bash
bun run --cwd apps/host/frontend dev
```

The root convenience scripts validate all frontends:

```bash
bun run frontend:lint
bun run frontend:test
bun run frontend:build
```

### Direct API commands

The host executable requires exactly one command: `api`, `migrate`, or `seed`. Running
it without a command prints usage and exits nonzero. `migrate` applies all enabled
module and Identity migrations and exits; `seed` runs the deterministic
Development-only seed and exits; `api` hosts the application and does not
automatically migrate or seed the database.

For example:

```bash
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile migrate
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile seed
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile api
```

Aspire does not use the no-argument `dev` profile for lifecycle ordering. That profile
remains a Development convenience profile with empty command arguments. For production,
run the same image as a terminating `migrate` job, wait for it to succeed, and then run
the image with the long-running `api` command. `seed` is Development-only and must not
be used as a production deployment job.

### Development-only seed data

In `Development`, the `seed` command creates deterministic fixtures for the enabled
Customers, Communications and Products modules. Aspire runs it after migrations.
Seed data is Development-only and includes synthetic data. Use `admin@vantigo.local`
/ `admin` to sign in as `Administrator`.
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
variable form, for example `Development__Seed__Data__Customers=12`. The Products API seeds
a small fixed catalog and takes no counts.

To opt out, set `Development__Seed__Enabled=false`. Seeding is never enabled outside
`Development`.

For self-hosted Vantigo authentication, deployment configuration, and production
migration guidance, see [Vantigo identity](docs/customers-authentication.md). For
persisted enterprise SSO, SCIM provisioning, and the operator runbook, see the
[SSO and SCIM operations guide](docs/sso-scim-operations.md) and the
[documentation index](docs/README.md).
The Products domain model, pricing rules and cross-service contracts are documented
in [Products](docs/products.md).

Ready to dig into the code? Head over to the
[contributing guide](CONTRIBUTING.md) for the design principles, API conventions,
testing and database migrations.

## Self-hosting

Vantigo ships as one multi-architecture container image that runs the host API and
serves the production frontend and all enabled modules from the same process. The
image is published to GHCR on every release and signed with
[Cosign](https://docs.sigstore.dev/cosign/):

| Application | Image |
| --- | --- |
| Vantigo | `ghcr.io/vantigo-io/vantigo` |

Available tags: `latest`, `vX`, `vX.Y`, and `vX.Y.Z` — pin `vX.Y.Z` for
reproducible deployments. Verify a signature with:

```bash
cosign verify ghcr.io/vantigo-io/vantigo:latest \
  --certificate-identity-regexp 'https://github.com/vantigo-io/vantigo' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The easiest deployment is the [Docker Compose stack](deploy/compose/) from the
quick start. To integrate with your own infrastructure instead, bring your own
PostgreSQL database and run the image with the terminating `migrate` command first,
then the long-running `api` command:

```bash
docker run --rm \
  -e ConnectionStrings__vantigo="Host=your-postgres;Database=vantigo;Username=...;Password=..." \
  ghcr.io/vantigo-io/vantigo migrate

docker run -d \
  --name vantigo \
  -p 8080:8080 \
  -e ConnectionStrings__vantigo="Host=your-postgres;Database=vantigo;Username=...;Password=..." \
  ghcr.io/vantigo-io/vantigo api
```

A persistent Data Protection key ring is stored in PostgreSQL, so the database
connection and backup must be shared by all replicas. Do not run `seed` in
production; it is only for Development. Authentication, reverse-proxy and full
configuration guidance lives in
[Vantigo identity](docs/customers-authentication.md).

Prefer not to host anything at all? The managed **Vantigo SaaS** runs the exact same
open-source stack for you.

## License

Vantigo is licensed under the [GNU Affero General Public License v3.0](LICENSE)
(AGPL-3.0). You are free to use, modify and self-host it — if you offer a modified
version of Vantigo to others over a network, you must make your modifications
available under the same license.
