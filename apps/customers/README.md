<div align="center">
  <img src="../../assets/logo.png" alt="Vantigo" width="100" />

  # Vantigo Customers

  **Your connection with your customers.**

  [![Next.js](https://img.shields.io/badge/Next.js_16-000000?logo=nextdotjs&logoColor=white)](https://nextjs.org/)
  [![Mantine](https://img.shields.io/badge/Mantine-339AF0?logo=mantine&logoColor=white)](https://mantine.dev/)
  [![TanStack](https://img.shields.io/badge/TanStack_Query_+_Table-FF4154?logo=reactquery&logoColor=white)](https://tanstack.com/)
  [![better-auth](https://img.shields.io/badge/better--auth-000000)](https://better-auth.com/)
  [![Drizzle ORM](https://img.shields.io/badge/Drizzle-C5F74F?logo=drizzle&logoColor=black)](https://orm.drizzle.team/)
  [![Vitest](https://img.shields.io/badge/Vitest-6E9F18?logo=vitest&logoColor=white)](https://vitest.dev/)
  [![Playwright](https://img.shields.io/badge/Playwright-2EAD33?logo=playwright&logoColor=white)](https://playwright.dev/)
  [![Docker](https://img.shields.io/badge/Docker-2496ED?logo=docker&logoColor=white)](https://www.docker.com/)
</div>

---

The customer management application of the [Vantigo](../../README.md) suite: a fast,
localized customer registry with a REST API designed for integrations.

## ✨ Features

- 📊 **Dashboard** — key figures at a glance (totals, new this month, business/private split)
- 🔍 **Datagrid** — server-driven search, filtering, sorting and pagination (25/50/100)
- ⚡ **Quick create & edit** — modal forms without leaving the overview
- 🪪 **Legal-id validation** — country-aware rules; Norwegian organisasjonsnummer and
  fødselsnummer are verified with mod11 checksums
- 🏢 **Brønnøysundregistrene autofill** — search the Norwegian company register by name or
  org.nr and autofill name and contact details
- ⚠️ **Duplicate detection** — live, advisory warnings while typing (never blocking)
- 🗄️ **Archive, don't delete** — customers are archived and can be reactivated
- 🌍 **Localized** — English and Norwegian, including country names and flags
- 🔑 **Full auth flow** — sign-up, sign-in, password reset (better-auth)

## 🚀 Getting started

From the repository root (see the [root README](../../README.md) for prerequisites):

```bash
docker compose up -d                              # PostgreSQL
pnpm install
pnpm --filter @vantigo/customers db migrate       # apply migrations
pnpm --filter @vantigo/customers dev              # http://localhost:10010
```

### ⚙️ Configuration

All configuration is provided as environment variables — locally via `.env` (loaded by mise),
in production at container start. Validation happens at boot and the app fails fast with a
readable error when something is missing.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `VANTIGO_CUSTOMERS_DATABASE_URL` | ✅ | — | PostgreSQL connection string |
| `VANTIGO_CUSTOMERS_AUTH_SECRET` | ✅ | — | Secret used to sign sessions (long & random) |
| `VANTIGO_CUSTOMERS_BASE_URL` | | `http://localhost:10010` | Public base URL of the app |
| `VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED` | | `false` | Enable email + password authentication |
| `VANTIGO_CUSTOMERS_RATE_LIMIT_ENABLED` | | `true` | better-auth rate limiting |

### 🗄️ Database

Schema lives in [`database/schema`](database/schema), managed with
[drizzle-kit](https://orm.drizzle.team/docs/kit-overview):

```bash
pnpm run db generate --name my_change   # create a migration from schema changes
pnpm run db migrate                     # apply pending migrations
```

Customer numbers are user-facing and start at `1001`.

## 🔌 API

Everything the UI does goes through the REST API — session-authenticated today, designed to
support API keys for external integrations later.

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/customers` | List with `page`, `pageSize`, `sortBy`, `sortDir`, `search`, `legalType`, `status`, `legalId`, `email` |
| `POST` | `/api/customers` | Create — returns `201` with the customer and duplicate `warnings[]` |
| `GET` | `/api/customers/{id}` | Fetch a single customer |
| `PATCH` | `/api/customers/{id}` | Partial update — same warning semantics |
| `DELETE` | `/api/customers/{id}` | Archive (soft delete) |
| `GET` | `/api/customers/stats` | Aggregates for the dashboard |
| `GET` | `/api/brreg/search?q=` | Proxy search against Brønnøysundregistrene |

Duplicate legal ids/emails are **warnings, not constraints** — writes always succeed and the
response tells you about possible duplicates.

Errors follow a consistent shape:

```json
{ "error": { "code": "VALIDATION_ERROR", "message": "...", "details": [] } }
```

## 🧪 Testing

```bash
pnpm test        # unit tests (Vitest + Testing Library)
pnpm test:e2e    # end-to-end (Playwright) — needs Docker
```

The e2e suite builds the app, boots a **fresh PostgreSQL testcontainer**, applies all
migrations and exercises real user flows — auth, i18n, customer CRUD and the brreg search
(mocked upstream). Nothing touches your development database.

## 🐳 Container

The image is **runtime-only**: the app is built natively and copied into a hardened,
distroless [Chainguard](https://www.chainguard.dev/) Node image — no shell, non-root, and a
single build serves both `linux/amd64` and `linux/arm64`.

```bash
pnpm --filter @vantigo/customers build
docker build -f apps/customers/Dockerfile -t vantigo-customers .   # from the repo root
```

Released images: `ghcr.io/vantigo-io/customers` (`latest`, `X.Y`, `vX.Y.Z`, `sha-*`).
