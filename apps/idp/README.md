# 🔐 Vantigo Identity Provider (IDP)

This is the central authentication and authorization hub for the **Vantigo** ecosystem. It is designed to act as a secure, edge-native Single Sign-On (SSO) and Identity Provider using **OAuth 2.1** and **OIDC** standards.

## 🚀 Features & Purpose

- **Centralized Authentication**: Houses all registration, login, session validation, and credential management for Vantigo.
- **OIDC/OAuth 2.1 Provider**: Powered by **Better-Auth** (utilizing the OAuth 2.1 Provider Plugin) to safely federate sessions to other Vantigo applications (e.g. CRM, ERP modules).
- **Edge-Ready & Container-Portable**: Implements dependency injection (Ports & Adapters) allowing it to be compiled to a standalone minimal Docker container or run natively on Cloudflare Workers.
- **Strict Validation**: Performs schema-based environment variable parsing on boot via **Zod** to prevent runtime crashes.
- **High Performance Logging**: Implements structured edge-safe logging via **Pino** (with pretty-printed logs in development mode).

---

## ⚙️ Environment Variables

The application is configured using environment variables. When running in a containerized Docker build, Zod validates these variables at startup.

| Variable Name | Type | Default | Required | Description |
| :--- | :--- | :--- | :--- | :--- |
| `DATABASE_URL` | String (URL) | - | **Yes** | Connection URL for the Neon PostgreSQL database (e.g., `postgres://user:password@host:port/db`). |
| `AUTH_SECRET` | String | - | **Yes** | Cryptographic key used by Better-Auth to sign sessions, cookies, and tokens. |
| `AUTH_URL` | String (URL) | - | No | Root public URL of the IDP service (required in production self-hosted mode for OAuth redirects). |
| `PORT` | Integer | `3000` | No | Port on which the standalone server listens (self-hosted Docker mode only). |
| `NODE_ENV` | Enum | `development` | No | Application environment stage: `development`, `production`, or `test`. |
| `LOG_LEVEL` | Enum | `info` | No | Filter level for Pino logging: `fatal`, `error`, `warn`, `info`, `debug`, or `trace`. |
| `MIGRATIONS_PATH` | String | - | No | Explicit path override to target programmatic migrations at boot (useful in testing/dev environments). |

---

## ⚡ Cloudflare Deployment Guide

To deploy `vantigo-idp` onto Cloudflare Workers, follow these steps:

### 1. Prerequisite Setup
Ensure you have the Cloudflare CLI `wrangler` logged in to your account:
```bash
bun x wrangler login
```

### 2. Configure Environment Secrets
Deploying to Cloudflare requires uploading secrets securely. Do **NOT** store raw database credentials or cryptographic keys in `wrangler.json`. Inject them using the CLI:
```bash
# Add the database connection string
bun x wrangler secret put DATABASE_URL

# Add the Better-Auth cryptographic signing secret
bun x wrangler secret put AUTH_SECRET

# (Optional) Add the root AUTH_URL if needed
bun x wrangler secret put AUTH_URL
```

### 3. Run Build & Compilation
Build the React/Mantine static assets:
```bash
bun run build
```

### 4. Deploy to the Edge
Run the deployment command inside the `apps/idp` directory (or use Turborepo from the root):
```bash
# From apps/idp/
bun x wrangler deploy

# Or from the monorepo root via wrangler directly
bun x wrangler deploy -c apps/idp/wrangler.json
```

Cloudflare will deploy the worker, bind the compatibility flags, and output the live edge URL (e.g. `https://vantigo-idp.<your-subdomain>.workers.dev`).
