# Module boundaries

Vantigo is a modular monolith: one Go binary, one container, one PostgreSQL
database — with strict module boundaries so any module can later be extracted into
its own deployable without a rewrite. Customers, Products, Energy, Communications and
Projects are vertical-slice modules composed only by `cmd/vantigo`, on the Identity
platform.

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
   schema (`identity`, `customers`, `products`, `energy`, `communications`,
   `projects`, `time`, `expenses`), and no module's migrations or queries reference
   another's. **No cross-schema foreign keys or joins** — reference other modules'
   data by opaque ID only. That is what keeps a future "move this schema to its own
   server" a connection-string change instead of a data migration.
5. **One module, one mount.** A module exposes one `Module()` returning a
   `module.Module`: its name, its `Mount`, the permissions it contributes, the
   background workers it contributes, and the cross-module contracts it *provides*.
   There are six provider slots, each filled by at most one enabled module:
   `Directory` (`contracts.CustomerDirectory`, customers), `Users`
   (`contracts.UserDirectory`, identity), `Products` (`contracts.ProductCatalog`,
   products), `Projects` (`contracts.ProjectDirectory`, projects), `Actuals`
   (`contracts.ProjectActuals`, time) and `Expenses`
   (`contracts.ProjectExpenses`, expenses). `module.Compose` mounts each module at
   `/api/v1/<name>/` and builds every provider before any `Mount` runs, so a
   module's `Deps` already carries what it consumes; `Actuals` and `Expenses` are
   resolved last, after `Projects`, so those providers' *constructors* are allowed
   to read the project directory (neither does, but a future provider could).
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
  modules both declaring the same provider — a customer directory, a user directory,
  a product catalog, a project directory, project actuals or project expenses —
  naming both.
- **Rule 7**: `no-restricted-imports` in each module frontend's `eslint.config.js`,
  run by `bun run frontend:lint` locally and in CI.

## Turning a module off

`MODULES` is a **positive allowlist** — a comma-separated list of the business
modules a deployment serves, parsed once at startup:

```text
MODULES=customers,products
```

Unset enables every module this binary can mount
(`customers,products,energy,communications,projects,time,expenses`). Identity is always mounted and
is never listed. Entries are trimmed and lower-cased, empty entries are ignored, and a
name the binary does not know fails startup naming both the value and the known set.

A disabled module contributes no route, no permission, no contract path and no
background worker; its paths fall through to the `/api` catch-all and answer the 404
problem. The decision is taken once, before anything mounts, so a module can never
end up with endpoints mapped whose permissions were never contributed.

**Every schema is migrated regardless of what `MODULES` enables**, so enabling a
module later needs no migration.

Some modules cannot be hosted alone. Energy, Communications and Projects read
customer data through `contracts.CustomerDirectory`, which only Customers implements,
so enabling any of them without `customers` is rejected at startup with a message
naming both (`projects requires customers`). Time hangs its hours off projects and
reads them through `contracts.ProjectDirectory`, so `time` without `projects` is
rejected the same way (`time requires projects`). A module that starts requiring
another module's contract adds its own check in `internal/config`.

A contract can also be **optional**. `contracts.ProductCatalog` is the first:
Projects prices its billing lines through it, but when `products` is disabled
`Deps.Products` is simply nil and Projects answers 409 on its billing-line operations
instead of failing startup. That is the shape to copy — a required dependency gets a
config check, an optional one leaves the `Deps` field nil and **the consumer must
handle nil**. The one exception is `contracts.UserDirectory`: identity is always
mounted, so `Deps.Users` is always set once composed.

`contracts.ProjectActuals` is the second optional contract, and the first to run in
the *opposite* direction from the module that needs it: **Time provides it, Projects
optionally consumes it.** Time's own dependency on Projects is required — it hangs
hours off `contracts.ProjectDirectory` and cannot start without it (`time requires
projects`, below) — but Projects' economy view does not require Time back: with
`time` disabled `Deps.Actuals` is nil, Projects answers `timeTracking: false` and
shows budgets with nothing to compare them against, and nothing fails startup. Both
directions are resolved by `Compose` before any module mounts, so there is no runtime
call in either direction that could cycle — `internal/projects` still imports no
other module (depguard) and no SQL of either module crosses into the other's schema.

`contracts.ProjectExpenses` is the third optional contract and the mirror of the
second: **Expenses provides it, Projects optionally consumes it** — what a
project's expenses cost and bill, beside the hours Time already reports. It is the
first contract whose provider requires *nothing* in return: Expenses depends on
nobody but identity, so a `MODULES=expenses` installation provides a contract
nothing consumes, and a `MODULES=customers,projects,time` one consumes nothing of
it. Where `ProjectActuals` takes the project's currency in on the request, this one reports
**per currency** and never converts: an expense carries its own currency per line,
and a provider that folded them into the project's would have had to ask the
project directory while serving — the module cycle at request time both contracts
are shaped to avoid. A consumer with `Deps.Expenses` nil must say "expense
tracking is off" the way it says `timeTracking: false` today — never zeroes
standing in for "not enabled".

A consumed contract can also narrow **who** may reach the data it exposes, not only
which module can. Projects' economy reads (`GET /{id}/economy`, the portfolio, the
dashboard's budget alerts) sit behind Projects' own `projects:access` and, for
amounts, its own financial-rights rule — never behind `time:access` — because the
contract hands the caller's currency and rights decision to Time and gets back only
what those already allow: **aggregate hours and amounts per project and per billing
line, never per person or per entry.** That is deliberately less than Time's own
project summary already gives a project member (which is per-status, per-line
**and** per-person), so a caller who could never open Time tracking at all still
gets a strict subset of it through Projects. Cost and margin sit behind a second,
Projects-owned gate on top of that (`projects:view-costs`, sensitive, granted to no
default role) — a permission the consuming module defines and enforces itself,
which a provider contract cannot see or grant on its behalf. See
[`docs/projects.md`](projects.md#project-economy) for what each shaping level
returns.

Time is the first module to consume three contracts and provide one:
`contracts.ProjectDirectory` (required — hence the config check),
`contracts.UserDirectory` (always there, for names and for the rate card's user
search), `contracts.ProductCatalog` (optional — with products disabled a `list` or
`discount` billing line simply has no price, and the rate chain falls through to the
project default) — and it provides `contracts.ProjectActuals` (optional for its
consumer, above), reading its own `time` tables and never calling back into
`contracts.ProjectDirectory` to serve it: the currency an amount is measured in
arrives in the request, because Projects is the module that owns that fact. It also
keeps a rule worth copying: **no contract call inside a transaction that holds a
lock**, enforced by fakes that record any call made under one — the economy reads on
both sides take no lock at all.

**Expenses depends on nobody but identity.** Unlike every other business module,
it needs no config-checked dependency at all: `MODULES=expenses` alone is a valid
installation, and so is `MODULES=customers,expenses`. It fills one provider slot —
`Expenses` (`contracts.ProjectExpenses`, above), served from its own tables by
`internal/expenses/projectexpenses.go`, which takes the pool and nothing else and
asks the project directory nothing while it serves — and that is not a dependency
either way: an installation with no `projects` provides it to nobody, and one
without `expenses` leaves the consumer's slot nil. `contracts.ProjectDirectory` is
read, but purely *optionally* — `Deps.Projects` is nil when `projects` is not
enabled, `GET /meta` answers `projectsAvailable: false`, and every project-shaped
request field (a project id, a billing line, `billable`, a markup, a customer rate
per kilometre) is refused on its own field rather than silently accepted. A stored
project id an expense already carries is not cleared when the module is switched
off — the column is data, not a fact this module owns the right to delete — but
nothing can be judged against a project nobody can ask about any more, so a save on
such a row carries those six columns through untouched instead of refusing the
edit. See [Expenses](expenses.md) for the model, and for what changes with and
without Projects.

## Adding a module

**Backend**

1. Create `internal/<name>` exposing `Module()`, and add it to `businessModules` in
   `cmd/vantigo/main.go`.
2. Add its contract as `openapi/<name>.yaml`; the name is also its mount prefix and
   its schema name. Add it to `Modules` in `internal/openapi/openapi.go` — the list
   the merge, the lint and the coverage report iterate.
3. Add `internal/openapi/gen/cfg-<name>.yaml` (the oapi-codegen config, which names
   the generated package `internal/<name>/gen`) and the two matching
   `//go:generate` lines in `apps/server/generate.go`: one oapi-codegen line and one
   `sqlc generate -f internal/<name>/sqlc.yaml`.
4. Add a `NNNNN_<name>_baseline.sql` migration owning only its own schema.
5. Add a depguard rule set for it in `apps/server/.golangci.yml`, and add its import
   paths to every other module's deny list, so every pairwise isolation rule covers
   it.
6. Add its schema to `moduleSchemas` in `internal/db/schema_test.go`, and its name to
   the known set and any `MODULES` dependency check in `internal/config`.

A module contributing no recorded exchanges needs nothing in
`openapi/testdata/exchanges/`: that corpus is frozen evidence from the retired .NET
suites, and the coverage tool treats a module with no corpus file as zero recorded
exchanges. The module's own `contracttest.RequireCoverage` gate is what proves its
operations are exercised.

**Frontend**

7. Add the package under `apps/<name>/frontend` (`@vantigo/<name>-ui`), copy the
   `no-restricted-imports` block into its `eslint.config.js`, and add it to every
   other module package's deny list.
8. Add `{ module: "<name>", output: "apps/<name>/frontend/src/api-schema.d.ts" }` to
   `tools/openapi/gen-client.ts`, then run `bun run gen:client`.
9. Add the module key to `moduleKeys` in `apps/host/frontend/src/navigation.ts`
   (plus a `searchStrategy` and its `navSearchFor` case if the app has a searchable
   list), register the app (label, icon, home path, sidebar entries) in
   `apps/host/frontend/src/apps.ts`, and add a layout route
   `apps/host/frontend/src/routes/<name>.tsx` that renders
   `createFileRoute("/<name>")(appLayoutOptions("<name>"))`. The route guard derives
   its rules from those same sidebar entries, so a page that more callers may open
   than the entry is offered to says so with `guardPermissions` (Time's approval
   queue: the entry needs `time:approve`, the URL needs only `time:access`).
10. Import the package's catalog in `apps/host/frontend/src/i18n.ts`
    (`import "@vantigo/<name>-ui/i18n";`), and add the host-owned labels in every
    language: navigation and dashboard strings, and **one entry per permission key in
    `apps/host/frontend/src/catalogs/admin.ts`** — without it the admin role editor
    shows a raw key.
11. Wire the dashboard (`routes/dashboard.tsx`: the module card, its metric and its
    attention items, all gated on the module and its permissions) and the spotlight
    (`components/app-spotlight.tsx`: navigation comes from the registry, but a result
    group and any quick action are hand-added).
