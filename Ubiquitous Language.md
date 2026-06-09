# 📖 Ubiquitous Language

This document establishes a shared, unambiguous vocabulary (Ubiquitous Language) for the **Vantigo Cloud** ecosystem. Both human developers and AI coding agents (LLMs) **MUST** strictly adhere to these definitions and terminology to prevent domain drift, design confusion, or architectural inconsistencies.

---

## 🔐 Authentication & Identity Domain

### User
* **Definition**: A single human actor who interacts with the Vantigo ecosystem.
* **Context**: A user has a unique ID, core profile attributes (name, email, avatar), and acts as the root owner for all sessions and credential accounts.
* **Database Mapping**: Represented by the `user` table.

### Account
* **Definition**: A specific credential mechanism or login method associated with a **User**.
* **Context**: A User can have multiple Accounts linked to them (e.g., a username/password credential account, a Google OAuth account, a GitHub OAuth account). It stores access tokens, refresh tokens, and encrypted passwords.
* **Database Mapping**: Represented by the `account` table.

### Session
* **Definition**: A temporal state of active authorization representing a validated login instance for a **User**.
* **Context**: Each session has an expiration time, a secure token, and metadata tracking the login origin (IP address, User Agent). Sessions are cascade-deleted if the owner User is deleted.
* **Database Mapping**: Represented by the `session` table.

### Verification
* **Definition**: A short-lived cryptographic token or secret challenge used to verify a User's identity or action out-of-band.
* **Context**: Used for flows such as email validation (verifying a User owns an email address) or password reset flows.
* **Database Mapping**: Represented by the `verification` table.

### Identity Provider (IDP)
* **Definition**: The centralized authentication service (`apps/idp`) responsible for managing users, sessions, and federating authentication to other Vantigo applications.
* **Context**: Operates as a native OAuth 2.1 / OIDC provider using Better-Auth.

---

## 🏗️ Architectural & Monorepo Domain

### Workspace
* **Definition**: An independent, self-contained project directory located under `apps/` or `packages/` that participates in the monorepo.
* **Context**: Managed collectively via Bun Workspaces and Turborepo. Workspaces can depend on each other (e.g., `apps/idp` depends on `packages/ui`).

### Portability (Ports & Adapters / Dependency Injection)
* **Definition**: The architectural pattern that ensures Vantigo code can run natively in serverless edge environments (Cloudflare Workers) or containerized environments (Docker) without code modification.
* **Context**: Business logic interacts with abstract service interfaces (Ports). Concrete bindings (Adapters) are injected dynamically at the entry points (`entry.cloudflare.ts` or `entry.docker.ts`).

### Central Catalog
* **Definition**: The single source of truth for package dependency versions defined in the root `package.json` under the `"catalog"` key.
* **Context**: Sub-workspaces import these dependencies using the `catalog:` protocol (e.g., `"react": "catalog:"`) to prevent version drift across the monorepo.
