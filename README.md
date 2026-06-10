<div align="center">
  <img src="./assets/logo.png" alt="Vantigo Logo" width="200" style="border-radius: 12px; margin-bottom: 20px;" />
  
  # 🚀 Vantigo

  **The blazing-fast, open-source Enterprise Resource Planning (ERP) platform.**<br>
  Built from the ground up for modern businesses, self-hosters, and enterprise scale.

  [![Bun](https://img.shields.io/badge/Bun-%23000000.svg?style=for-the-badge&logo=bun&logoColor=white)](https://bun.sh/)
  [![React](https://img.shields.io/badge/react-%2320232a.svg?style=for-the-badge&logo=react&logoColor=%2361DAFB)](https://reactjs.org/)
  [![Hono](https://img.shields.io/badge/Hono-%23E36002.svg?style=for-the-badge&logo=hono&logoColor=white)](https://hono.dev/)
  [![Docker](https://img.shields.io/badge/Docker-Distroless-blue?style=for-the-badge&logo=docker)](https://docker.com)
  [![Cloudflare](https://img.shields.io/badge/Cloudflare-F38020?style=for-the-badge&logo=Cloudflare&logoColor=white)](https://cloudflare.com)
</div>

---

## ⚡ Why Vantigo?

Legacy ERP systems are notoriously slow, bloated, and a nightmare to deploy. **Vantigo changes that.** 

We built Vantigo as an **Edge-Native Micro-application Suite**. By combining the raw speed of the Bun runtime, the end-to-end type safety of Hono & Drizzle, and the global distribution of Cloudflare Workers, Vantigo delivers an uncompromisingly premium experience.

## ✨ Core Features

- **🚀 Blazing Fast:** Powered natively by Cloudflare Workers and Bun.
- **🔌 Dependency Injection:** 100% portable. Runs natively on the Cloudflare Edge, or compiles to a standalone Distroless Docker image for self-hosting.
- **💅 Premium UI:** A beautiful, responsive Single Page Application built on React, React Router, and Mantine UI.
- **🔐 Centralized IDP:** Includes a fully-fledged OAuth 2.1 Identity Provider powered by Better-Auth.
- **🗄️ Database Isolation:** Every application gets its own dedicated database schema and Neon project.

---

## 🏗️ Architecture for Developers

Vantigo is a modern developer's dream. If you want to contribute or build custom modules, here is our stack:

- **Runtime & Package Manager:** [Bun](https://bun.sh/)
- **Monorepo:** [Turborepo](https://turbo.build/)
- **Backend:** [Hono](https://hono.dev/)
- **Database:** Neon PostgreSQL + [Drizzle ORM](https://orm.drizzle.team/).
- **Frontend:** React, React Router, [Mantine](https://mantine.dev/).

*Note for AI Agents & Contributors: Please read the [`AGENTS.md`](./AGENTS.md) file in the root directory for strict architectural guidelines and consult the [`Ubiquitous Language.md`](./Ubiquitous%20Language.md) glossary for domain terminology before contributing.*

---

## 🛠️ Build & Package Pipeline

Vantigo uses a highly optimized compile-on-host build pipeline to ensure lightning-fast Docker image builds (under 3 seconds) and minimal image sizes (~90MB). The workflow consists of three main phases:

1. **Build (`bun run build`)**: Bundles and builds the frontend React/Mantine static assets inside `apps/idp/dist/web`.
2. **Compile (`bun run compile` / `bun run compile:all`)**: Bundles the Hono backend entry point and compiles it along with the Bun runtime into a single, standalone native binary. 
   - `bun run compile`: Compiles for the current host architecture (Darwin, Linux, Windows).
   - `bun run compile:all`: Cross-compiles binaries for all supported targets (`linux-amd64`, `linux-arm64`, `mac-arm64`, `windows-amd64`).
3. **Docker (`bun run docker:package`)**: Packages the pre-compiled native Linux binaries into a secure, minimal `gcr.io/distroless/cc-debian12:nonroot` Docker image using `docker buildx` for both `linux/amd64` and `linux/arm64` architectures.

---

## 🧪 Testing Strategy

Testing is a first-class citizen in Vantigo. All new features and routes are required to have associated unit/integration tests:

- **Unit Tests**: Run using `bun test` inside workspace directories.
- **Verification Tests**: Run using `bun run type-check` for TS validation and `bun run lint` for Biome formatting checks.
- **Database Migration Verification**: An opt-in integration test suite spins up a dynamic PostgreSQL instance using **Testcontainers** to verify that all SQL migration schemas apply successfully. 
  - Run locally: `TEST_MIGRATIONS=true bun test`
  - Automated: Executed conditionally on every PR build in GitHub Actions.

  > [!TIP]
  > **Running with Colima on macOS**
  > If you use **Colima** as your local container runtime, `testcontainers` needs to be pointed to the Colima socket rather than the default Docker socket. Run the tests with the following environment variables:
  > ```bash
  > TEST_MIGRATIONS=true \
  > DOCKER_HOST="unix://${HOME}/.colima/default/docker.sock" \
  > TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE="/var/run/docker.sock" \
  > bun test
  > ```
  > Alternatively, you can add these export statements directly to your shell configuration (`.zshrc` or `.bashrc`).

