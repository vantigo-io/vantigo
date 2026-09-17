# Module boundaries

Vantigo is a modular monolith: one Go binary, one container, one PostgreSQL
database — with strict module boundaries so any module can later be extracted into
its own deployable without a rewrite. Customers, Products, Energy and Communications
are vertical-slice modules composed only by `cmd/vantigo`, on the Identity platform.

Modules never reference each other directly. Synchronous cross-module needs go
through the in-process contracts in `internal/contracts`; asynchronous ones use the
transactional-outbox pattern described in [`ROADMAP.md`](../ROADMAP.md), of which
Communications' outbox is the working example.

## The rules

1. **No module→module dependencies.** A package under `internal/<module>/…` may
   import platform packages (`internal/config`, `internal/db`, `internal/httpx`,
   `internal/module`, `internal/contracts`, …) and its own subpackages, never
   another module's.
2. **The platform imports no module.** `internal/module` and `internal/contracts` —
   the platform modules mount through — may not import any business module, so the
   composition machinery never depends on what it composes.
3. **Contracts stay pure.** `internal/contracts` holds cross-module interfaces,
   permissions and access rules as DTOs only, never store types, so any module or a
   future extracted service can implement or consume them.
4. **One schema per module.** Each module maps tables only into its own PostgreSQL
   schema (`identity`, `customers`, `products`, `energy`, `communications`), and no
   module's migrations or queries reference another's. **No cross-schema foreign keys
   or joins** — reference other modules' data by opaque ID only. That is what keeps a
   future "move this schema to its own server" a connection-string change instead of
   a data migration.
5. **One module, one mount.** A module exposes one `Module()` returning a
   `module.Module`: its name, its `Mount`, the permissions it contributes, the
   background workers it contributes, and — for the one module that owns customer
   data — its `contracts.CustomerDirectory` implementation. `module.Compose` mounts
   each at `/api/v1/<name>/`.
6. **Never reach around the boundary.** Do not call another module's HTTP endpoints
   from inside the process, and do not reach into another module's schema.
7. **Frontend packages are isolated too.** A module frontend package (for instance
   `@vantigo/products-ui`) never imports from another module's package or from the
   host app; only the host composes them. Shared UI lives in
   `@vantigo/frontend-shell` and the generated API types and client in
   `@vantigo/frontend-api-client`.

## How they are enforced

- **Rules 1–3**: [depguard](https://github.com/OpenPeeDeeB/depguard) in
  [`apps/server/.golangci.yml`](../apps/server/.golangci.yml), run by
  `mise run server:check` and in CI. There is one rule set per boundary, scoped to
  its directory, and each forbidden module gets two deny entries — an exact,
  `$`-anchored match so it can never prefix-match a sibling, and a `…/<mod>/` match
  covering every subpackage including generated ones.
- **Rule 4**: `apps/server/internal/db/schema_test.go` scans every migration and
  query file and fails when one module's SQL names another module's schema.
- **Rule 5**: `module.Compose` itself. It fails on a duplicate module name, an
  invalid or duplicate permission key, a `Mount` error, a path two modules both
  declare, a component two modules declare differently under the same name, or two
  modules both declaring a customer directory — naming both.
- **Rule 7**: `no-restricted-imports` in each module frontend's `eslint.config.js`,
  run by `bun run frontend:lint` locally and in CI.

## Turning a module off

`MODULES` is a **positive allowlist** — a comma-separated list of the business
modules a deployment serves, parsed once at startup:

```text
MODULES=customers,products
```

Unset enables every module this binary can mount
(`customers,products,energy,communications`). Identity is always mounted and is never
listed. Entries are trimmed and lower-cased, empty entries are ignored, and a name
the binary does not know fails startup naming both the value and the known set.

A disabled module contributes no route, no permission, no contract path and no
background worker; its paths fall through to the `/api` catch-all and answer the 404
problem. The decision is taken once, before anything mounts, so a module can never
end up with endpoints mapped whose permissions were never contributed.

**Every schema is migrated regardless of what `MODULES` enables**, so enabling a
module later needs no migration.

Some modules cannot be hosted alone. Energy and Communications read customer data
through `contracts.CustomerDirectory`, which only Customers implements, so enabling
either without `customers` is rejected at startup with a message naming both. A
module that starts requiring another module's contract adds its own check there.

## Adding a module

1. Create `internal/<name>` exposing `Module()`, and add it to `businessModules` in
   `cmd/vantigo/main.go`.
2. Add its contract as `openapi/<name>.yaml`; the name is also its mount prefix and
   its schema name.
3. Add a `NNNNN_<name>_baseline.sql` migration owning only its own schema.
4. Add a depguard rule set for it in `apps/server/.golangci.yml`, and add its import
   paths to every other module's deny list, so every pairwise isolation rule covers
   it.
5. Add its schema to `moduleSchemas` in `internal/db/schema_test.go`.
6. For a frontend package, copy the `no-restricted-imports` block into its
   `eslint.config.js`, add the module key to `moduleKeys` in
   `apps/host/frontend/src/navigation.ts`, register the app (label, icon,
   home path, sidebar entries) in `apps/host/frontend/src/apps.ts`, and add a
   layout route `apps/host/frontend/src/routes/<name>.tsx` that renders
   `createFileRoute("/<name>")(appLayoutOptions("<name>"))`.
