# Module boundaries

Vantigo is a modular monolith: Customers, Communications and Products are
vertical-slice modules composed only by `Vantigo.Host`. Modules must never
reference each other directly — synchronous cross-module needs go through the
in-process contracts in `Vantigo.Contracts`, and future asynchronous needs will
use the transactional-outbox event pattern described in `ROADMAP.md`.

## The rules

1. **No module→module dependencies.** A module project references only
   `Vantigo.Contracts` and `Vantigo.Configuration` (plus BCL, ASP.NET Core and
   EF Core). Only the host references the modules. Shared authorization policy
   names and email contracts live in `Vantigo.Contracts`; their implementations
   live in the `Identity` app module. Hosting runtime concerns (telemetry, SPA
   serving, command-line parsing) live in `Vantigo.Host`.
2. **Contracts stay pure.** `Vantigo.Contracts` depends on no module types, no
   EF Core and no ASP.NET Core, so any module (or a future extracted service)
   can implement or consume them.
3. **Internal by default.** Endpoint and database types are `internal`; a
   module's public surface is its contract implementations and its host
   registration extensions.
4. **Domain stays out of the web.** `*.Domain.*` namespaces do not depend on
   ASP.NET Core.
5. **One schema per module.** Each module's DbContext maps tables only into its
   own PostgreSQL schema, and its migrations never reference another module's
   schema — the shared database does not make it a shared data model.
6. **`InternalsVisibleTo` only for the module's own tests.**
7. **Frontend packages are isolated too.** A module frontend package (for
   instance `@vantigo/products-ui`) never imports from another module's package
   or from the host app; only the host composes them.

## Turning a module off

`Modules:<Name>:Enabled` (`Modules__<Name>__Enabled` as an environment
variable) is a single startup decision, taken once before any module registers
anything and then read back out of the container by every later stage. A
disabled module registers no services and no background workers, maps no
endpoints, contributes no permissions to the authorization catalog, and is
neither migrated nor seeded. Its routes fall through to the host's `/api`
catch-all and answer `404`.

Because the decision is taken once, a module can never end up with endpoints
mapped whose permissions were never contributed — that combination fails the
host's `ValidatePermissionCatalog` check and refuses to start. The flags are read
while the service collection is still being populated, so a test host supplies
them as host configuration (`IWebHostBuilder.UseSetting`); a `Modules` section
that only arrives with `ConfigureAppConfiguration` is applied after the modules
are composed and the host rejects the mismatch instead of acting on it.

Some modules cannot be hosted alone. Communications and Energy read customer and
contact data through `ICustomerDirectory`, which only Customers implements, so
enabling either of them with `Modules:Customers:Enabled=false` is rejected at
startup with a message naming both flags. `ModuleCompositionValidator` in
`Vantigo.Host` owns that list; a module that starts requiring another module's
contract adds itself there. Optional cross-module reads (Communications asking
`IProductCatalog` for product context) stay optional and must tolerate the
implementation being absent.

## How they are enforced

- **Backend rules (1–6)**: `packages/architecture/Vantigo.Architecture.Tests`
  (NetArchTest + EF model inspection + migration source scans) runs with every
  `dotnet test Vantigo.slnx`, locally and in CI.
- **Frontend rule (7)**: `no-restricted-imports` in each module frontend's
  ESLint config, run by `bun run frontend:lint` locally and in CI.

When adding a new module, add it to the module list in
`Vantigo.Architecture.Tests` so every pairwise isolation rule covers it
automatically, and copy the ESLint restriction block into its frontend package.
