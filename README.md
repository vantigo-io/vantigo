<div align="center">

<img src="assets/logo.png" alt="Vantigo logo" width="140" />

# Vantigo

**The open-source, all-in-one platform for running your business.**

<br />

![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-4169E1?style=for-the-badge&logo=postgresql&logoColor=white)
![OpenAPI](https://img.shields.io/badge/OpenAPI-6BA539?style=for-the-badge&logo=openapiinitiative&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-2496ED?style=for-the-badge&logo=docker&logoColor=white)

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
| **Communications** | Send and archive business email across shared mailboxes.             | 🚧 In development |
| **Products**       | The catalog of goods and services the company sells, with prices.    | 🚧 In development |
| **Energy**         | Metering points, meters, supply periods and consumption.             | 🚧 In development |

Identity — accounts, sign-in, MFA, RBAC, OIDC and SCIM — is always part of the
application and is never listed as an optional module. Which business modules a
deployment serves is chosen with `MODULES`; more modules are on the way, each one
landing as a new package under [`apps/server/internal/`](apps/server/internal/) with
its own contract in [`openapi/`](openapi/) and its own database schema.

## Quick start with Docker

The fastest way to try Vantigo is with the pre-built container images and the
ready-made Docker Compose stack in [`deploy/compose/`](deploy/compose/). All you
need is Docker — no toolchain, no build tools.

```bash
mkdir vantigo && cd vantigo
base=https://raw.githubusercontent.com/vantigo-io/vantigo/main/deploy/compose
curl -fsSLO "$base/compose.yaml"
curl -fsSLO "$base/.env.example"
curl -fsSLO "$base/vantigo.env.example"

cp .env.example .env                              # set a database password here
cp vantigo.env.example vantigo.env                # set APP_SECRET and BOOTSTRAP_SECRET here

docker compose up -d
```

Compose starts PostgreSQL, applies the database migrations as a one-shot `migrate`
job, and then brings up the single Vantigo application on <http://localhost:8080>.
Customers, Communications, Products and Energy are modules in that one application.

The stack runs outside development, so `vantigo.env` must carry two values before
the first start — the application refuses to boot without them, and neither is ever
generated or logged for you:

- `APP_SECRET` — at least 32 bytes of key material; generate with `openssl rand -base64 32`.
- `BOOTSTRAP_SECRET` — authenticates the one-time first-Owner bootstrap.

Then visit <http://localhost:8080/setup>, enter that same bootstrap secret, and
create your first Owner account. Remove or rotate the secret afterwards.

The [compose guide](deploy/compose/README.md) covers configuration, first sign-in,
production notes and upgrades in more detail.

## Architecture

Vantigo is a modular monolith: **one Go binary** serving the Identity platform, the
enabled business modules and the built React SPA from one process, against one
PostgreSQL database with one schema per module.

```
vantigo/
├── apps/
│   ├── server/                      # The Go server — the whole backend
│   │   ├── cmd/vantigo/             # The only composition root and the command dispatch table
│   │   └── internal/
│   │       ├── identity/            # Accounts, sessions, MFA, RBAC, OIDC, SCIM
│   │       ├── customers/           # Customers vertical slice
│   │       ├── communications/      # Communications vertical slice (outbound email)
│   │       ├── products/            # Products vertical slice
│   │       ├── energy/              # Energy vertical slice
│   │       ├── module/              # The platform modules mount through
│   │       ├── db/                  # Pool and the embedded goose migrations
│   │       └── web/                 # The embedded SPA
│   ├── host/frontend/               # @vantigo/app — the single React SPA (Vite)
│   ├── customers/frontend/          # @vantigo/customers-ui
│   ├── communications/frontend/     # @vantigo/communications-ui
│   ├── products/frontend/           # @vantigo/products-ui
│   └── energy/frontend/             # @vantigo/energy-ui
├── packages/
│   ├── frontend-shell/              # @vantigo/frontend-shell — shared shell, theme, branding
│   └── frontend-api-client/         # @vantigo/frontend-api-client — generated types and client
├── openapi/                         # The API contract: one OpenAPI file per module
├── deploy/compose/                  # Ready-made Docker Compose stack
├── scripts/                         # Native artifact, image and smoke-test scripts
└── assets/                          # Shared branding assets
```

The server ships as one container image. The SPA is embedded into the binary at build
time, so the running container serves the frontend and every enabled module itself.
Curious about the design principles, module boundaries and API conventions behind the
codebase? They're covered in the [contributing guide](CONTRIBUTING.md).

## Developing from source

### Prerequisites

- [mise](https://mise.jdx.dev/) — it installs the pinned Go, Bun and tool versions
  from [`mise.toml`](mise.toml), which is the single source of truth for the toolchain
- A Docker-compatible container runtime (Docker Desktop, [Colima](https://github.com/abiosoft/colima), Podman, ...)

### Run the stack

```bash
git clone https://github.com/vantigo-io/vantigo.git
cd vantigo

mise install                          # Go, Bun and the lint/release tools
bun install --frozen-lockfile         # frontend workspace dependencies

mise run server:db                    # PostgreSQL for tests (55432) and development (55433)
mise run server:dev                   # the api command on http://localhost:8080
```

`mise run server:dev` runs the `api` command against the development database with a
development-only `APP_SECRET`, so it migrates and then serves. In development
`BOOTSTRAP_SECRET` may be left unset: the process generates one and logs it at WARN on
startup — copy it from the log and use it at `/setup`.

If `bun` or `go` is not on your `PATH`, prefix the command with `mise exec --`
(`mise exec -- bun install --frozen-lockfile`).

### Frontend development

The SPA runs against the Go server through the Vite dev server, which proxies `/api`
to <http://localhost:8080>:

```bash
bun run --cwd apps/host/frontend dev   # http://localhost:10011
```

The root convenience scripts validate every frontend package:

```bash
bun run frontend:lint
bun run frontend:test
bun run frontend:build
```

### Tests and checks

```bash
mise run server:test                  # go test against a real PostgreSQL
mise run server:check                 # golangci-lint, govulncheck, shellcheck, actionlint, goreleaser check
```

On a many-core machine, pin the Go tests to four CPUs — see the
[contributing guide](CONTRIBUTING.md) for why a full-parallelism run is not a valid
gate.

## Commands

The image is one binary with a dispatch table: the command is the first argument.
`api` is the image's default. Running the binary with no command, or with a command
it does not know, prints usage on stderr and exits 2 — a typo in a deployment job
fails loudly instead of quietly becoming a web server that never completes.

| Command | What it does |
| --- | --- |
| `api` | Applies pending migrations under the advisory lock, then serves the SPA, the API and the health endpoints — plus every enabled module's background workers when `WORKERS_IN_PROCESS=1` (the default). |
| `server` | Serves only: never migrates and never runs workers, regardless of `WORKERS_IN_PROCESS`. This is what a fleet of stateless replicas runs. |
| `worker` | Runs every enabled module's background workers, plus the health endpoints for probes. |
| `migrate` | Applies all migrations and exits 0 or 1. Uses `MIGRATIONS_DATABASE_URL` when set, otherwise `DATABASE_URL`. |
| `seed` | Development-only (`APP_ENV=development`; exits 2 outside it). It currently does nothing — it logs `nothing to seed: identity has no development seed` and exits 0. |
| `healthcheck` | Probes this container's own `/health/ready` on `127.0.0.1:$PORT` and exits 0 or 1. It constructs nothing and reads no database configuration, so a liveness probe never fails because of a misconfigured `DATABASE_URL`. |

Because the image has no shell, container and orchestrator probes must use the exec
form (`["/app/vantigo", "healthcheck"]`). An HTTP-style probe that connects from
outside sends its own address as the `Host` header, which the host filter rejects.

For a controlled release, run `migrate` as a terminating job, wait for it to succeed,
and only then start `api` or `server` — the point is to see a migration failure before
any serving replica starts, not to skip `api`'s own startup migration.

## Self-hosting

Vantigo ships as one multi-architecture container image that serves the API, the
production frontend and all enabled modules from the same process. The image is
published to GHCR on every release and signed with
[Cosign](https://docs.sigstore.dev/cosign/):

| Application | Image |
| --- | --- |
| Vantigo | `ghcr.io/vantigo-io/vantigo` |

Available tags: `latest`, `X`, `X.Y`, `X.Y.Z` and `sha-<commit>` — note that the
published image tags drop the `v` the git release tags keep. Pin `X.Y.Z`, or a digest,
for reproducible deployments. Verify a signature with:

```bash
cosign verify ghcr.io/vantigo-io/vantigo:latest \
  --certificate-identity-regexp 'https://github.com/vantigo-io/vantigo' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The easiest deployment is the [Docker Compose stack](deploy/compose/) from the quick
start. To integrate with your own infrastructure instead, bring your own PostgreSQL
database and run the image with the terminating `migrate` command first, then the
long-running `api` command:

```bash
docker run --rm \
  -e DATABASE_URL="postgresql://vantigo:...@your-postgres:5432/vantigo?sslmode=verify-full" \
  ghcr.io/vantigo-io/vantigo migrate

docker run -d \
  --name vantigo \
  -p 8080:8080 \
  -e DATABASE_URL="postgresql://vantigo_app:...@your-postgres:5432/vantigo?sslmode=verify-full" \
  -e APP_URL="https://vantigo.example.com" \
  -e APP_SECRET="..." \
  -e BOOTSTRAP_SECRET="..." \
  ghcr.io/vantigo-io/vantigo api
```

Outside development the configuration is fail-closed: `APP_URL` must be `https`, the
database connection must require certificate-verified TLS, and `APP_SECRET` and
`BOOTSTRAP_SECRET` must be set. `ALLOW_INSECURE_TRANSPORT=1` knowingly relaxes the
transport rules for local and evaluation use only — see
[transport security](docs/transport-security.md).

`APP_SECRET` derives every key the process uses (CSRF tokens, cookie signing, TOTP
secret encryption) through HKDF-SHA256. There is no external key vault to provision,
and losing it is equivalent to losing a signing key: every open session and every
stored TOTP secret becomes unrecoverable. All replicas must share it, and the
database.

Do not run `seed` in production; it is development-only. Authentication,
reverse-proxy and full configuration guidance lives in
[Vantigo identity](docs/customers-authentication.md); every setting the process reads
is documented in `apps/server/internal/config/config.go`'s field comments, which are
the authoritative reference. For static workforce OIDC, static SCIM provisioning and
the operator runbook, see the
[SSO and SCIM operations guide](docs/sso-scim-operations.md) and the
[documentation index](docs/README.md). The Products domain model, pricing rules and
cross-module contracts are documented in [Products](docs/products.md).

Prefer not to host anything at all? The managed **Vantigo SaaS** runs the exact same
open-source stack for you.

Ready to dig into the code? Head over to the
[contributing guide](CONTRIBUTING.md) for the design principles, API conventions,
testing and database migrations.

## License

Vantigo is licensed under the [GNU Affero General Public License v3.0](LICENSE)
(AGPL-3.0). You are free to use, modify and self-host it — if you offer a modified
version of Vantigo to others over a network, you must make your modifications
available under the same license.
