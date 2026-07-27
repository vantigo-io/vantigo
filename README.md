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

More applications are on the way — each one lands as a new vertical slice in
[`apps/`](apps/) and plugs into the same platform conventions.

## Architecture

Vantigo is a monorepo of independently packaged applications, composed locally by a
single [.NET Aspire](https://learn.microsoft.com/dotnet/aspire/) AppHost:

```
vantigo/
├── apps/
│   └── customers/
│       ├── backend/
│       │   ├── Customers.Api/         # ASP.NET Core minimal API
│       │   └── Customers.Api.Tests/   # Unit + integration tests
│       └── frontend/                  # React SPA (Vite, TanStack Router, Mantine)
├── orchestration/
│   └── AppHost/                       # .NET Aspire composition root
└── assets/                            # Shared branding assets
```

Each application ships as a single container image in production, where the .NET API
also serves the built frontend. In development, .NET Aspire runs everything side by
side with one command. Curious about the design principles and API conventions behind
the codebase? They're covered in the [contributing guide](CONTRIBUTING.md).

## Getting started

### Prerequisites

- [.NET 10 SDK](https://dotnet.microsoft.com/download/dotnet/10.0)
- [Node.js](https://nodejs.org/) 20+
- A Docker-compatible container runtime (Docker Desktop, [Colima](https://github.com/abiosoft/colima), Podman, ...)

### Run the full stack

```bash
git clone https://github.com/vantigo-io/vantigo.git
cd vantigo

# Restore pinned local tools (dotnet-ef)
dotnet tool restore

# Start everything: PostgreSQL, APIs, frontends and the Scalar API reference
dotnet run --project orchestration/AppHost
```

The Aspire dashboard opens automatically and shows every running resource with logs,
traces and endpoints:

- **customers-api** — the Customers API (migrations are applied automatically on startup)
- **customers-frontend** — the Customers SPA served by the Vite dev server
- **scalar** — interactive API reference for every registered API
- **postgres** — the PostgreSQL instance backing the applications

That's it — no manual database setup, connection strings or environment files needed.

Ready to dig into the code? Head over to the
[contributing guide](CONTRIBUTING.md) for the design principles, API conventions,
testing and database migrations.

## Self-hosting

> [!NOTE]
> Published container images are **on the roadmap** and not available quite yet. The
> section below describes the intended deployment model so you know what to expect.

Each Vantigo application will ship as a single container image that runs the .NET API
and serves the production frontend build from the same process:

```bash
docker run -d \
  --name vantigo-customers \
  -p 8080:8080 \
  -e ConnectionStrings__Postgresql="Host=your-postgres;Database=customers;Username=...;Password=..." \
  ghcr.io/vantigo-io/customers
```

Bring your own PostgreSQL, point the connection string at it, and you have a running
application — one container per app, nothing else required.

Until images are published, you can run from source: `dotnet publish` the API projects
and build the frontends with `npm run build`, or simply use the Aspire AppHost.

Prefer not to host anything at all? The managed **Vantigo SaaS** runs the exact same
open-source stack for you.

## License

Vantigo is licensed under the [GNU Affero General Public License v3.0](LICENSE)
(AGPL-3.0). You are free to use, modify and self-host it — if you offer a modified
version of Vantigo to others over a network, you must make your modifications
available under the same license.
