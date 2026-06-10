# Vantigo - AI Agent Guidelines

> **IMPORTANT FOR ALL FUTURE AI AGENTS:**
> This project follows strict architectural patterns. Do not deviate from these rules without explicit user permission.
> All domain and vocabulary terms (e.g., User, Account, Session) MUST strictly align with the definitions in [Ubiquitous Language.md](./Ubiquitous%20Language.md).

## 1. Project Overview
Vantigo is a suite of independent, deeply integrated business applications (similar to Odoo). The project is a monorepo containing multiple apps that all share the same UI system, configurations, and core tooling, but operate independently.

## 2. Technology Stack
- **Runtime & Package Manager:** Bun
- **Monorepo Orchestration:** Turborepo
- **Linter & Formatter:** Biome (Do NOT use ESLint or Prettier)
- **Database:** Neon (PostgreSQL) connected via Hyperdrive
- **ORM:** Drizzle
- **Backend:** Hono (Cloudflare Workers native)
- **Frontend:** React (SPA) + React Router (Bundled via Bun's native bundler)
- **Testing:** `bun test` (Use native Bun test runner, do not use Vitest or Jest)
- **UI Framework:** Mantine UI (Vanilla CSS preferred for custom styling, do NOT use Tailwind unless requested)
- **Authentication:** Better-Auth (Using OAuth 2.1 Provider Plugin for the central IDP)
- **Feature Flags:** Cloudflare Flagship

## 3. Core Architectural Rules

### 3.1. Dependency Injection (Ports & Adapters)
To ensure 100% portability between Cloudflare and Docker, **business logic must NEVER directly reference Cloudflare-specific bindings** (like `env.KV_NAMESPACE` or `env.FLAGSHIP`).
- Define generic interfaces for services.
- Use Hono's Context (`c.var`) to inject the correct implementation at the entry point.
- **Entry Points:** Each app has `src/entry.cloudflare.ts` (injects Cloudflare services) and `src/entry.docker.ts` (injects generic/fallback services).

### 3.2. Docker & Compilation Strategy
- The Docker version of the application must be compiled to a native binary using `bun build --compile`.
- Dockerfiles should use the highly secure `gcr.io/distroless/cc-debian12:nonroot` base image.

### 3.3. Database Isolation
- There is **NO shared database package** (`@vantigo/db`). 
- Each application (e.g., `apps/idp`) MUST have its own independent database schema, migrations, and Neon project.
- Migrations are orchestrated globally via Turborepo (`bun turbo run db:migrate`).
- Local development relies on Neon database branching or a local Docker Postgres fallback.

### 3.4. Local Development Scripts
- `bun run dev:workerd`: Runs the Cloudflare emulator via `wrangler` (Primary dev environment).
- `bun run dev:docker`: Runs the Bun server (Secondary dev environment, used to test Docker portability).
- `bun run dev`: MUST default to running `dev:workerd`.

### 3.5. Git Hooks & Code Quality
- **Husky** and **lint-staged** are configured to enforce `biome check --write` before any commit.
- **Commitlint** is configured to enforce the Conventional Commits specification.
- Do not bypass these hooks. Ensure all generated code passes Biome formatting checks.

### 3.6. Testing Guidelines
- **Test Importance**: High-quality tests are critical to verify that core business logic remains correct across edge (Cloudflare) and container (Docker) environments.
- **Feature Coverage**: Every new domain feature or application route must be accompanied by corresponding unit or integration tests under `bun test`.
- **Database Safeguard**: Any schema migration change must pass the automated `TEST_MIGRATIONS` validation step before it can be merged.

