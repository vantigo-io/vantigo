<div align="center">
  <img src="assets/logo.png" alt="Vantigo" width="140" />

  # Vantigo

  **A collection of essential applications that helps you run your business.**

  [![CI](https://img.shields.io/github/actions/workflow/status/vantigo-io/vantigo/ci.yml?branch=main&label=CI&logo=githubactions&logoColor=white)](https://github.com/vantigo-io/vantigo/actions/workflows/ci.yml)
  [![Release](https://img.shields.io/github/v/release/vantigo-io/vantigo?logo=github&sort=semver)](https://github.com/vantigo-io/vantigo/releases)
  [![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
  [![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

  [![TypeScript](https://img.shields.io/badge/TypeScript-3178C6?logo=typescript&logoColor=white)](https://www.typescriptlang.org/)
  [![Next.js](https://img.shields.io/badge/Next.js_16-000000?logo=nextdotjs&logoColor=white)](https://nextjs.org/)
  [![React](https://img.shields.io/badge/React_19-087EA4?logo=react&logoColor=white)](https://react.dev/)
  [![Mantine](https://img.shields.io/badge/Mantine-339AF0?logo=mantine&logoColor=white)](https://mantine.dev/)
  [![PostgreSQL](https://img.shields.io/badge/PostgreSQL_18-4169E1?logo=postgresql&logoColor=white)](https://www.postgresql.org/)
  [![Drizzle ORM](https://img.shields.io/badge/Drizzle-C5F74F?logo=drizzle&logoColor=black)](https://orm.drizzle.team/)
  [![Docker](https://img.shields.io/badge/Docker-2496ED?logo=docker&logoColor=white)](https://www.docker.com/)
  [![pnpm](https://img.shields.io/badge/pnpm-F69220?logo=pnpm&logoColor=white)](https://pnpm.io/)
  [![Turborepo](https://img.shields.io/badge/Turborepo-EF4444?logo=turborepo&logoColor=white)](https://turborepo.com/)
</div>

---

## 🤔 What is Vantigo?

Vantigo is a **free, open-source suite of business applications** that you run on your own
infrastructure — one instance, one database, one enterprise. No SaaS lock-in, no per-seat
pricing, no third party holding your customer data hostage.

- 🆓 **Free & open source** — licensed under AGPL-3.0. Use it, read it, improve it.
- 🔑 **Your data, your servers** — a single self-hosted instance per enterprise. Your customer
  database never leaves your infrastructure.
- 🔒 **Secure by default** — hardened distroless container images ([Chainguard](https://www.chainguard.dev/)),
  non-root runtime, fail-fast configuration validation at boot, session-based authentication
  with [better-auth](https://better-auth.com/).
- 🌍 **Multilingual** — English and Norwegian out of the box, designed so new languages are a
  single JSON file away.
- 🧪 **Seriously tested** — unit tests plus full end-to-end suites running against a real
  PostgreSQL (via testcontainers) on every change.
- 🐳 **Multi-arch containers** — `linux/amd64` and `linux/arm64` images published to GHCR.
- 🔌 **API-first** — everything the UI does goes through a REST API you can integrate with.

## 📦 Applications

| App | Description | Status |
| --- | --- | --- |
| 👥 [**Customers**](apps/customers) | Customer registry with dashboard, datagrid, Norwegian legal-id validation and Brønnøysundregistrene autofill | ✅ Active |

More applications are on the way — Vantigo is built as a monorepo where each app shares the
same foundation (auth, i18n, tooling, CI) so the suite can grow.

## 🚀 Getting started

### Prerequisites

- [Docker](https://www.docker.com/) (for PostgreSQL and end-to-end tests)
- [mise](https://mise.jdx.dev/) — manages Node.js, pnpm and environment variables
  *(alternatively: Node.js 26 + pnpm 11 manually)*

### Run locally

```bash
# 1. Clone and enter the repo
git clone https://github.com/vantigo-io/vantigo.git
cd vantigo

# 2. Install toolchain and trust the environment
mise trust && mise install

# 3. Create your local environment file (see apps/*/README.md for variables)
cp .env.example .env   # or create .env manually

# 4. Start PostgreSQL
docker compose up -d

# 5. Install dependencies and run migrations
pnpm install
pnpm --filter @vantigo/customers db migrate

# 6. Start everything
pnpm dev
```

The customers app is now running at [http://localhost:10010](http://localhost:10010).

### Run from containers

Every release publishes multi-arch images to GHCR:

```bash
docker run -p 10010:10010 \
  -e VANTIGO_CUSTOMERS_DATABASE_URL="postgresql://user:pass@host:5432/vantigo" \
  -e VANTIGO_CUSTOMERS_AUTH_SECRET="a-long-random-secret" \
  -e VANTIGO_CUSTOMERS_BASE_URL="https://customers.example.com" \
  ghcr.io/vantigo-io/customers:latest
```

All configuration is provided at runtime as environment variables — images contain no baked-in
config and fail fast with a readable error if something is missing.

## 🛠️ Development

```
vantigo/
├── apps/
│   └── customers/     # Customer management (Next.js)
├── assets/            # Brand assets
├── compose.yaml       # Local PostgreSQL
├── turbo.json         # Task orchestration
└── pnpm-workspace.yaml
```

Common commands (from the repo root):

| Command | What it does |
| --- | --- |
| `pnpm dev` | Start all apps in dev mode |
| `pnpm build` | Production build of all apps |
| `pnpm test` | Unit tests (Vitest) |
| `pnpm lint` | Lint & format check (Biome) |
| `pnpm --filter @vantigo/customers test:e2e` | End-to-end tests (Playwright + testcontainers) |

Releases are cut by pushing a semver tag (`git tag v1.2.3 && git push --tags`) — CI validates,
builds, pushes images and generates release notes from
[conventional commits](https://www.conventionalcommits.org/).

## 🤝 Contributing

Contributions are very welcome! Please read the [contributing guide](CONTRIBUTING.md) for the
workflow, commit conventions and how to get your environment running.

Found a bug or have an idea? [Open an issue](https://github.com/vantigo-io/vantigo/issues).

## 📄 License

Vantigo is licensed under the [GNU Affero General Public License v3.0](LICENSE).

In short: you are free to use, modify and self-host Vantigo. If you offer a modified version
as a network service, you must make your modifications available under the same license.
