<div align="center">
  <img src="./assets/logo.png" alt="Vantigo Logo" width="200" style="border-radius: 12px; margin-bottom: 20px;" />
  
  # 🚀 Vantigo Cloud

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

*Note for AI Agents & Contributors: Please read the [`AGENTS.md`](./AGENTS.md) file in the root directory for strict architectural guidelines before submitting Pull Requests.*
