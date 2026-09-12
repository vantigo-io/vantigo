# Customers, Products and Energy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the Customers, Products and Energy modules from .NET to Go — 75 operations, 215 ported tests (corrected from 222; see Global Constraints) — served by `cmd/vantigo` behind the platform sub-project 3 built.

**Architecture:** Three modules, each an island: its own PostgreSQL schema, its own sqlc queries, its own permission catalog, mounted by `module.Compose` through a router that enforces the contract's `x-vantigo-access` rule. The platform gains three things first: precedence-aware routing, `MODULES` enablement, and `contracts.CustomerDirectory` as the only sanctioned cross-module read.

**Tech Stack:** Go 1.27, stdlib `net/http`, pgx v5, sqlc v1.31.1 (via mise), goose, kin-openapi, oapi-codegen (strict + std-http), `internal/openapi/contracttest`.

**Spec:** `docs/superpowers/specs/2026-09-12-business-modules-design.md`

**Behavioural authority** (the .NET code is the specification; these inventories index it with file:line):

- `docs/superpowers/specs/2026-09-12-customers-inventory.md`
- `docs/superpowers/specs/2026-09-12-products-inventory.md`
- `docs/superpowers/specs/2026-09-12-energy-inventory.md`

## Global Constraints

- **The .NET test is the specification.** Every ported test carries `// Ported from <Class>.<Method>`. Tenancy-only tests are dropped. Targets: customers 136, products 72, energy 31 — **239 total**.

  **The metric is .NET test *methods*: a `[Theory]` counts as one method regardless of how many `InlineData` cases it carries.** This is stated here and in each inventory's census because the three modules previously mixed metrics — products counted theory methods while customers and energy counted theory *cases* — which made the figures unaddable. Never mix them again. Counted by `[Fact]`/`[Theory]` attributes across each module's `*.Tests` project, then applying that inventory's own port/drop dispositions:

  | module | raw methods | dropped | portable | how the drop is composed |
  |---|---|---|---|---|
  | customers | 211 | 75 (12 classes) | **136** | tenancy-only (`TenantIsolation` 3, `LeastPrivilegeDatabaseRole` 8) plus unrelated host/identity infra (`Auth` 25, `AuthSecurityUnit` 2, `Phase3Mfa` 4, `WorkforceOidc` 7, `WorkforceOidcCompletion` 6, `PublicOrigin` 3, `SecurityHeaders` 5, `SpaFallback` 8, `DevelopmentSeed` 3, `DatabaseContextRegistration` 1) |
  | products | 79 | 7 (1 class) | **72** | all of `ProductCatalogContractTests`: `IProductCatalog` is out of scope (below) |
  | energy | 32 | 1 (1 class) | **31** | `EnergyTenancyIntegrationTests`, tenancy-only |

  136 + 72 + 31 = **239**, replacing the 222 this plan first committed to and the 215 Task 16's first round computed. Three independent errors produced the old numbers:

  1. **Customers 100 → 136.** Its inventory's "Portable total" addend list included only one of its seven "port" domain classes, omitting 36 methods (the seven domain classes sum to 48, of which the list carried 12). Cross-checked: 211 raw − 75 dropped = 136.
  2. **Energy 43 → 31.** Its inventory counted theory *cases*, not methods, and separately undercounted `EnergyEndpointsTests` as 13 where the file carries 15 `[Fact]`s.
  3. **Products 79 → 72.** `IProductCatalog` is listed in products inventory §5 as "published for other modules", but the design doc makes `contracts.CustomerDirectory` the only cross-module read in this sub-project, so nothing here consumes `IProductCatalog` — the interface is deliberately not built and `ProductCatalogContractTests`' 7 methods are out of scope. The inventory's census was also off by one: `CategoriesEndpointsTests` has 13 facts, not 14, so the suite is 79 methods, not 80.
- **No tenancy.** No `tenant_id` column, index component, or parameter anywhere. Where a .NET key is `(tenant_id, x)`, the Go key is `(x)`.
- **One schema per module**, no cross-schema references in migrations or queries. `internal/db/schema_test.go` already scans all five schemas.
- **Contract-driven access.** Every operation's `x-vantigo-access` is enforced by `module.Router`. Handlers never re-check what the router already checked, with **exactly two exceptions**, both the same shape: a permission whose requirement depends on request-body content the router cannot see, checked through `Access.Check` and refused with the byte-identical 403 the router would have written.
  1. **Products' pricing gate** (Task 11, `internal/products/server.go`): `postProducts` and `postProductsByIdVariants` additionally require `products:pricing-view` **and** `products:pricing-manage` when the payload carries pricing (a non-null `standardCost`, or any `prices` entry on any variant).
  2. **Customers' legal-identity gate** (Tasks 6/8, `internal/customers/server.go`): `postCustomers` and `putCustomersById` additionally require `customers:legal-identity-manage` when the body carries an `identity`.

  Both fail closed (any error, infrastructure included, reads as "no permission"). Neither is "the one exception", and neither module's comment may claim to be. A third would need its own entry here.
- **Time** comes from `Deps.Clock()` and is passed to SQL as a parameter. No `time.Now()` outside tests, no SQL `now()` in decisions.
- **Money and quantities** are `numeric` columns with .NET's scales; Postgres rounds, as .NET relied on. The wire keeps `format: double`; the contract does not change.
- **Errors** keep each module's .NET shapes: bare problems and validation text, never identity's `{code, message}`. `httpx.WriteError` already maps 23505/23P01 to 409.
- **Secrets, tokens and addresses** are never logged.
- **No new direct dependencies** without saying why in the task's report.
- Green before every commit, from `apps/server`: `mise exec -- go test ./... -count=1`, `mise exec -- golangci-lint run` (`0 issues.`), `mise exec -- go generate ./...` with no drift. `-race` runs in CI only (this host has no C compiler); reproduce CI's pool sizing locally with `taskset -c 0-3` when a test uses concurrency.

## File structure

| Path | Responsibility |
|---|---|
| `internal/module/router.go` | Precedence-aware matcher replacing the inner `http.ServeMux` |
| `internal/module/compose.go` | Enablement, catalog composition, directory injection |
| `internal/contracts/directory.go` | `CustomerDirectory` and its entry types |
| `internal/config/config.go` | `MODULES`, `BRREG_BASE_URL`, `BRREG_TIMEOUT` |
| `internal/db/migrations/0000{3,4,5}_*_baseline.sql` | One hand-written baseline per module |
| `internal/{customers,products,energy}/module.go` | `Module()`, `Limits`, `mount` |
| `internal/{customers,products,energy}/*.go` | Handlers grouped by area, one file per area |
| `internal/{customers,products,energy}/queries/*.sql` | sqlc sources; generated into `store/` |
| `internal/{customers,products,energy}/harness_test.go` | Per-module harness in identity's shape |

---

### Task 1: Precedence-aware routing

**Files:** Modify `internal/module/router.go`; test `internal/module/router_test.go`.

**Interfaces:**
- Produces: `module.Router` unchanged in surface — `HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request))`, `ServeHTTP`, `Err() error`.

Go's `http.ServeMux` refuses eight pairs the contract needs, pinned in `internal/openapi/openapi_test.go`'s `knownServeMuxConflicts`, because it has no literal-before-parameter precedence across differing segments. .NET separated them with `{id:int}` route constraints.

- [ ] **Step 1: Write the failing tests.** In `router_test.go`, build a router over a small in-memory contract holding both members of a real conflicting pair and assert:
  - `GET /api/v1/x/contacts/7` reaches the literal route, and `GET /api/v1/x/7/contacts` reaches the parameter route;
  - a wrong method on a known path answers the 404 problem (unchanged behaviour);
  - a true duplicate (same method, identical pattern) is reported by `Err()`;
  - an unknown in-module path answers the 404 problem;
  - `HEAD` on a `GET` operation runs `Access.Check` with that operation's rule;
  - a path with a `%2F` in a segment does not match a two-segment route.
- [ ] **Step 2: Run them and watch them fail.** `mise exec -- go test ./internal/module/ -run Router -count=1`
- [ ] **Step 3: Implement the matcher.** Replace the inner `http.ServeMux` with a table built at registration: split each pattern into method plus segments; at dispatch, walk segments and prefer an exact literal match over a parameter match at the same index; a trailing-slash or path-cleaning redirect is not reproduced (the contract has no such routes). Keep `Err()`'s existing problems: unknown pattern, missing rule, permission not in the catalog, never-registered operation, nil `Access`, nil `Limiter` with non-empty `Limits`. Replace the recovered-panic conflict problem with a direct duplicate check.
- [ ] **Step 4: Green.** The tests above plus `mise exec -- go test ./internal/module/ ./internal/identity/ -count=1`: identity's routes must be unaffected.
- [ ] **Step 5: Prove the conflicts are gone.** Extend `internal/openapi/openapi_test.go` so the pinned pairs are asserted to mount cleanly on `module.NewRouter` (keep the `http.ServeMux` pinning test as the record of why the matcher exists).
- [ ] **Step 6: Commit.** `fix(module): route literals before parameters`

---

### Task 2: MODULES enablement

**Files:** Modify `internal/config/config.go`, `internal/module/compose.go`, `cmd/vantigo/main.go`; tests `internal/config/config_test.go`, `internal/module/compose_test.go`.

**Interfaces:**
- Produces: `cfg.Modules []string` (enabled names, identity excluded); `module.Compose(deps, mods ...Module)` mounting only enabled modules.

- [ ] **Step 1: Write the failing tests.** `MODULES` unset gives the default `customers,products,energy`; whitespace and case are trimmed and lowered; an unknown name is a configuration problem naming the name and the known set; `energy` without `customers` is a problem naming both; so is `communications` without `customers` once it exists. In `compose_test.go`: a disabled module contributes no route, no catalog entry, and its paths answer the `/api` 404 problem.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.** Parse in `config.Load` in the collect-all-problems style. `Compose` takes the enabled set from `Deps.Config` and skips the rest. Identity is always mounted and never appears in `MODULES`. Every schema still migrates, disabled or not.
- [ ] **Step 4: Green**, including `internal/identity` and `cmd/vantigo`.
- [ ] **Step 5: Commit.** `feat(config): enable modules from MODULES`

---

### Task 3: The customer directory port

**Files:** Create `internal/contracts/directory.go`; modify `internal/module/module.go` (add `Deps.Directory`), `internal/module/compose.go`; test `internal/contracts/directory_test.go`.

**Interfaces:**
- Produces:

```go
type CustomerEntry struct {
    ID       int32
    Name     string
    Archived bool
}

type ContactEntry struct {
    ID        int32
    FirstName string
    LastName  string
    Email     *string
}

type ContactMatch struct {
    ContactID            int32
    CandidateCustomerIDs []int32
}

// CustomerDirectory is the only way one module reads another's data.
// Archived customers still resolve: consumers hold historical references.
// More than one match is ambiguous; a caller must not choose for the user.
type CustomerDirectory interface {
    Customer(ctx context.Context, id int32) (*CustomerEntry, error)
    Contact(ctx context.Context, id int32) (*ContactEntry, error)
    ContactsByEmail(ctx context.Context, email string) ([]ContactMatch, error)
}
```

  A missing row is `(nil, nil)`, not an error. `Deps.Directory CustomerDirectory`, set by `Compose` from whichever enabled module provides one.
- [ ] **Step 1: Write the failing test.** A fake directory injected through `Deps` reaches a module's `Mount`; with customers disabled, `Deps.Directory` is nil and the enablement rule (Task 2) has already refused the combination.
- [ ] **Step 2: Run it and watch it fail.**
- [ ] **Step 3: Implement.** `Module` gains an optional `Directory func(Deps) CustomerDirectory`; `Compose` calls it for the module that declares it and injects the result into every module's `Deps` copy before `Mount`.
- [ ] **Step 4: Green.**
- [ ] **Step 5: Commit.** `feat(module): add the customer directory port`

---

### Task 4: Customers schema

**Files:** Create `internal/db/migrations/00003_customers_baseline.sql`, `internal/customers/sqlc.yaml`, `internal/customers/queries/customers.sql`; modify `generate.go`; test `internal/db/schema_test.go`.

Tables, from customers inventory §3 (drop every `tenant_id`): `customers` (identity from 1001, `customer_number` bigint unique alone, `name` varchar(255), `status` varchar(20) default `active`, the five nullable legal-identity columns, timestamps); `contacts`; `customers_contacts` (PK `(customer_id, contact_id)`, both FKs cascade, index on `contact_id`); `customers_timeline_entries` (`payload_json` jsonb, `current_revision` int as the concurrency token, index on `customer_id`); `customers_timeline_entries_revisions` (unique `(customer_timeline_entry_id, revision_number)`); and `counters` (`counter_name` text primary key, `next_value` bigint) replacing `tenant_counters` — one row per counter, e.g. `customer-number`.

- [ ] **Step 1: Write the failing test.** Extend `schema_test.go` with `TestCustomersBaseline_AppliesAndIsIdempotent`: apply up, down, up; assert the five tables, the `customer_number` unique index, the revisions unique index, and that no column is named `tenant_id`.
- [ ] **Step 2: Run it and watch it fail.**
- [ ] **Step 3: Write the migration and the sqlc config.** Follow `00002_identity_baseline.sql`'s style; no GRANTs.
- [ ] **Step 4: Generate and green.** `mise exec -- go generate ./...` then the tests.
- [ ] **Step 5: Commit.** `feat(customers): add the customers schema`

---

### Task 5: Customers module skeleton, catalog and harness

**Files:** Create `internal/customers/{module.go,server.go,errors.go}`, `internal/customers/{harness_test.go,main_test.go}`.

**Interfaces:**
- Produces: `customers.Module() module.Module` with `Name: "customers"`, `Mount`, `Directory`, and these 13 permissions, all `Delegable: true`, `Sensitive` as marked (customers inventory §6):

| Key | Display | Description | Category | Sensitive |
|---|---|---|---|---|
| `customers:view` | View customers | View customer names, identifiers, and a sanitized activity summary. | Customers | false |
| `customers:create` | Create customers | Create customers without legal identity data. | Customers | false |
| `customers:update` | Update customers | Update customer names and basic non-sensitive details. | Customers | false |
| `customers:delete` | Delete customers | Delete customers and their customer-owned records. | Customers | true |
| `customers:legal-identity-view` | View legal identities | View customer legal identity and registry attribution. | Legal identity | true |
| `customers:legal-identity-manage` | Manage legal identities | Add, replace, or remove customer legal identity data. | Legal identity | true |
| `customers:contacts-view` | View contacts | View contact names and contact details. | Contacts | true |
| `customers:contacts-manage` | Manage contacts | Create, update, and delete contacts. | Contacts | true |
| `customers:associations-view` | View customer associations | View links between customers and contacts. | Associations | true |
| `customers:associations-manage` | Manage customer associations | Create, update, and remove customer-contact links. | Associations | true |
| `customers:timeline-view` | View customer timeline | View customer timeline entries, notes, provenance, and revisions. | Timeline | true |
| `customers:timeline-manage` | Manage customer timeline | Create, update, and delete customer timeline entries. | Timeline | true |
| `customers:lookup-view` | Use registry lookup | Search the external business registry for legal identities. | Lookup | true |

- [ ] **Step 1: Build the harness.** Copy the shape of `internal/identity/harness_test.go`: one migrated database per test, the real `server.New` stack over `module.Compose`, a settable clock, a package-level `contracttest.Recorder`, `h.client(t)` bound to the caller's `t`, and a seeded signed-in principal with a chosen permission set (identity is mounted alongside, so sign-in is real).
- [ ] **Step 2: Stub every operation.** Generate `unimplemented.go` from the strict interface with a throwaway script (not committed); each method returns `module.ErrNotImplemented`. `main_test.go` calls `contracttest.RequireCoverage(m, recorder, pendingOperations...)` with all 29 pending.
- [ ] **Step 3: Mount test.** `module.Compose` with customers succeeds, and `GET /api/v1/customers` without a session answers 401.
- [ ] **Step 4: Green and lint.**
- [ ] **Step 5: Commit.** `feat(customers): add the module skeleton and harness`

---

### Task 6: Customers CRUD and stats

**Operations:** `getCustomers`, `postCustomers`, `getCustomer`, `putCustomersById`, `deleteCustomersById`, `getCustomersStats`, `getCustomersStatsAttention`, `getCustomersStatsSummary`, `getCustomersStatsTimeseries`.

**Behaviour:** customers inventory §1.1, §1.4 (per-endpoint validation-versus-404 order), §2.1–2.2 (value objects and their exact messages), §3 (`customer_number` from the `counters` upsert, not a sequence).

- [ ] **Step 1: Port the tests** named in customers inventory §7 for these endpoints, each with its `// Ported from` marker, including the search test that asserts search matches `Name` only.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement** the queries and handlers, following the per-endpoint ordering table exactly.
- [ ] **Step 4: Green**, then remove these operations from `unimplemented.go` and `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(customers): customer CRUD and dashboard stats`

---

### Task 7: Contacts and associations

**Operations:** `getCustomersContacts`, `postCustomersContacts`, `getContact`, `putCustomersContactsById`, `deleteCustomersContactsById`, `getCustomersContactsByIdCustomers`, `getCustomersByIdContacts`, `postCustomersByIdContacts`, `putCustomersByIdContactsByContactId`, `deleteCustomersByIdContactsByContactId`.

**Behaviour:** customers inventory §1.1–1.2, §1.4, §4 (pessimistic row locks on contacts).

- [ ] **Step 1: Port the tests** for these endpoints from §7.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement**, keeping `UpdateCustomerContact`'s 404-before-validation order and `Attach`'s validation-before-existence order.
- [ ] **Step 4: Green**, shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(customers): contacts and customer associations`

---

### Task 8: Legal identity and Brreg lookup

**Operations:** `getCustomersByIdLegalIdentity`, `putCustomersByIdLegalIdentity`, `deleteCustomersByIdLegalIdentity`, `getCustomersLookupBrreg`.

**Files also:** `internal/customers/brreg.go`; config gains `BRREG_BASE_URL` (default `https://data.brreg.no`) and `BRREG_TIMEOUT` (default `15s`).

**Behaviour:** customers inventory §2.2 (legal identity validation), §5 (the upstream paths, response shapes, 502 mapping). Within the timeout, retry a failed GET up to 3 times with a 4 s per-attempt timeout. No circuit breaker (spec, recorded divergence). No response cache, as .NET had none.

- [ ] **Step 1: Port the 6 lookup tests** (`Integration/LookupEndpointsTests.cs`) plus the legal-identity tests, with a fake `http.RoundTripper` in place of `StubBrregHandler`. No test touches the network.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement** the client and handlers; only transport failures and timeouts map to 502.
- [ ] **Step 4: Green**, shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(customers): legal identity and registry lookup`

---

### Task 9: Customer timeline

**Operations:** `getCustomersByIdTimeline`, `postCustomersByIdTimeline`, `getCustomersByIdTimelineByEntryId`, `putCustomersByIdTimelineByEntryId`, `deleteCustomersByIdTimelineByEntryId`, `getCustomersByIdTimelineByEntryIdRevisions`.

**Behaviour:** customers inventory §2.3–2.4 (manual-entry validation and the state machine) and §4 (three overlapping concurrency guards, all answering the same 409).

- [ ] **Step 1: Port the timeline tests** from §7, and add a gated concurrency test: two concurrent updates of one entry, exactly one succeeding, the other 409, proven with a lock gate as identity's tests do.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement** the revision check, the `current_revision` token and the unique-index backstop.
- [ ] **Step 4: Green** (run the gated test with `-count=10`), shrink `pendingOperations` to empty for customers.
- [ ] **Step 5: Commit.** `feat(customers): customer timeline and revisions`

---

### Task 10: Products schema, skeleton and catalog

**Files:** Create `internal/db/migrations/00004_products_baseline.sql`, `internal/products/{sqlc.yaml,queries/,module.go,server.go,errors.go,harness_test.go,main_test.go}`.

Tables, from products inventory §3: `product_categories` (FK `parent_id` RESTRICT, unique `(parent_id, name)` **NULLS NOT DISTINCT**, index on `parent_id`, no timestamps); `tax_categories` (unique `name`, `rate numeric(5,4)`); `products` (FKs to category and tax category, both RESTRICT); `product_variants` (unique `sku`, partial unique `barcode WHERE barcode IS NOT NULL`, `option_values jsonb`, `standard_cost numeric(12,2)`, `weight_kg numeric(10,3)`, dimensions `numeric(10,1)`, FK cascade); `product_prices` (`currency char(3)`, `amount numeric(12,2)`, FK cascade, two non-unique indexes, and deliberately no exclusion constraint).

The 10 permissions, all `Delegable: true`, none sensitive (products inventory §5): `products:products-view` "View products" / "View products, variants, and pricing." / Products; `products:products-manage` "Manage products" / "Create, update, and archive products." / Products; `products:variants-view` "View variants" / "View product variants." / Variants; `products:variants-manage` "Manage variants" / "Create, update, and delete product variants." / Variants; `products:pricing-view` "View pricing" / "View product variant prices." / Pricing; `products:pricing-manage` "Manage pricing" / "Create, update, and delete product variant prices." / Pricing; `products:categories-view` "View categories" / "View product categories." / Categories; `products:categories-manage` "Manage categories" / "Create, update, and delete product categories." / Categories; `products:tax-categories-view` "View tax categories" / "View product tax categories." / Tax categories; `products:tax-categories-manage` "Manage tax categories" / "Create, update, and delete product tax categories." / Tax categories.

- [ ] **Step 1: Write the failing schema test**, as Task 4's: up, down, up; the NULLS NOT DISTINCT unique, the partial barcode unique, no `tenant_id`.
- [ ] **Step 2: Run it and watch it fail.**
- [ ] **Step 3: Write the migration, sqlc config, module skeleton, catalog and harness**, with all 26 operations stubbed and pending. Port `ProductsAuthorizationEndpointsTests`' catalog assertion as a Go test that the module declares exactly these 10 keys.
- [ ] **Step 4: Green and lint.**
- [ ] **Step 5: Commit.** `feat(products): add the products schema, skeleton and catalog`

---

### Task 11: Products, variants and pricing

**Operations:** `getProducts`, `postProducts`, `getProduct`, `putProductsById`, `deleteProductsById`, `getProductsByIdVariants`, `postProductsByIdVariants`, `putProductsByIdVariantsByVariantId`, `deleteProductsByIdVariantsByVariantId`, `getProductsByIdVariantsByVariantIdPrices`, `postProductsByIdVariantsByVariantIdPrices`, `putProductsByIdVariantsByVariantIdPricesByPriceId`, `deleteProductsByIdVariantsByVariantIdPricesByPriceId`.

**Behaviour:** products inventory §1.1, §1.3 (validation order), §2 (GTIN, pricing, money).

- [ ] **Step 1: Port the tests** for these endpoints from §6, including the one proving option-map keys come back camelCased, and one proving `PUT /products/{id}` ignores `variants`.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.** The conditional permission lives here: when a create-product or create-variant payload carries pricing (`standardCost` set or any `prices` entry), the handler additionally requires `products:pricing-manage` via `Access.Check` with a permission rule, refusing exactly as the router would. Option-map keys are camelCased on the way out. Money relies on the column scales.
- [ ] **Step 4: Green**, shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(products): products, variants and pricing`

---

### Task 12: Categories, tax categories and stats

**Operations:** `getProductsCategories`, `postProductsCategories`, `getCategory`, `putProductsCategoriesById`, `deleteProductsCategoriesById`, `getProductsTaxCategories`, `postProductsTaxCategories`, `getTaxCategory`, `putProductsTaxCategoriesById`, `deleteProductsTaxCategoriesById`, `getProductsStatsAttention`, `getProductsStatsSummary`, `getProductsStatsTimeseries`.

**Behaviour:** products inventory §1.2, §1.3, §7. `getProductsStatsAttention` stays a stub returning `[]`, commented as parity.

- [ ] **Step 1: Port the tests** for these endpoints, plus one proving a delete blocked by a `RESTRICT` foreign key answers the documented 409 rather than .NET's 500.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement**, mapping 23503 and 23001 to that 409.
- [ ] **Step 4: Green**, `pendingOperations` empty for products.
- [ ] **Step 5: Commit.** `feat(products): categories, tax categories and stats`

---

### Task 13: Energy schema, skeleton and catalog

**Files:** Create `internal/db/migrations/00005_energy_baseline.sql`, `internal/energy/{sqlc.yaml,queries/,module.go,server.go,errors.go,harness_test.go,main_test.go}`.

Tables, from energy inventory §3: `metering_points` (unique `gsrn`, address columns owned in-table, `expected_annual_consumption_kwh numeric(14,3)`, lat/long `double precision`); `meters` (unique `(metering_point_id) WHERE removed_at IS NULL`); `supply_periods` (`customer_id` opaque, no FK) with `CREATE EXTENSION IF NOT EXISTS btree_gist` and the exclusion constraint quoted in §3.2 — `EXCLUDE USING gist (metering_point_id WITH =, tstzrange(start, COALESCE(end, 'infinity'), '[)') WITH &&) WHERE (status <> 'Cancelled')`; `consumption_intervals` range-partitioned on `start` with `start` in the primary key, the self-referencing supersedes FK, and the unique `(metering_point_id, start, end) WHERE is_current`. Port the monthly-partition function from §3.3 verbatim.

The 8 permissions, all `Category: "Energy"`, `Delegable: true`, none sensitive (energy inventory §6): `energy:metering-points-view` "View energy metering points" / "View energy metering point details and listings."; `energy:metering-points-manage` "Manage energy metering points" / "Create and update energy metering points."; `energy:meters-view` "View energy meters" / "View energy meter history for metering points."; `energy:meters-manage` "Manage energy meters" / "Replace meters installed at energy metering points."; `energy:consumption-view` "View energy consumption" / "View energy consumption intervals and aggregates."; `energy:consumption-manage` "Manage energy consumption" / "Add and replace manual energy consumption intervals."; `energy:supply-periods-view` "View energy supply periods" / "View energy supply periods for metering points."; `energy:supply-periods-manage` "Manage energy supply periods" / "Create, switch, end, and cancel energy supply periods."

- [ ] **Step 1: Write the failing schema test:** up, down, up; the extension; the exclusion constraint rejects an overlapping non-cancelled period and allows one that is cancelled; a partition exists for a written month.
- [ ] **Step 2: Run it and watch it fail.**
- [ ] **Step 3: Write the migration, sqlc config, skeleton, catalog and harness**, all 20 operations stubbed and pending. Port `EnergyPermissionCatalogTests`' alphabetical-order assertion.
- [ ] **Step 4: Green and lint.**
- [ ] **Step 5: Commit.** `feat(energy): add the energy schema, skeleton and catalog`

---

### Task 14: Metering points, meters and supply periods

**Operations:** `getEnergyMeteringPoints`, `postEnergyMeteringPoints`, `getEnergyMeteringPoint`, `putEnergyMeteringPointsById`, `getEnergyMeteringPointsByIdMeters`, `postEnergyMeteringPointsByIdMeters`, `getEnergyMeteringPointsByIdSupplyPeriods`, `postEnergyMeteringPointsByIdSupplyPeriods`, `postEnergyMeteringPointsByIdSupplyPeriodsSwitch`, `postEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd`, `deleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId`.

**Behaviour:** energy inventory §1.1, §2.1–2.3 (GSRN, meters, and the supply-period state machine with its overlap maths and open-ended periods). Create and switch resolve the customer through `Deps.Directory`, never through the customers schema.

- [ ] **Step 1: Port the tests** for these endpoints from §7, and add a gated race: two concurrent overlapping supply-period creates, exactly one succeeding, the loser answering 409 from the exclusion constraint.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.** Keep the friendly pre-check for its message; the constraint stays authoritative.
- [ ] **Step 4: Green** (the gated test at `-count=10`), shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(energy): metering points, meters and supply periods`

---

### Task 15: Consumption, aggregation and stats

**Operations:** `getEnergyMeteringPointsByIdConsumption`, `postEnergyMeteringPointsByIdConsumption`, `getEnergyMeteringPointsByIdConsumptionAggregate`, `getEnergyCustomersByCustomerIdConsumption`, `getEnergyCustomersByCustomerIdConsumptionAggregate`, `getEnergyCustomersByCustomerIdMeteringPoints`, `getEnergyStatsAttention`, `getEnergyStatsSummary`, `getEnergyStatsTimeseries`.

**Behaviour:** energy inventory §2.4 (exact-tuple dedup and the supersede chain, with no overlap protection), §4 (both aggregation queries quoted in full, with bucketing, time zone, gaps and rounding) and §4.1 (`MarketTimeZone`).

- [ ] **Step 1: Port the tests** from §7, including the aggregation cases across a DST boundary, and add one asserting that `from`/`to` without a UTC offset answer the operation's 400 (the recorded divergence from .NET's server-local fallback).
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement**, porting the SQL verbatim and keeping quantities `numeric(14,3)` until the response boundary.
- [ ] **Step 4: Green**, `pendingOperations` empty for energy.
- [ ] **Step 5: Commit.** `feat(energy): consumption, aggregation and stats`

---

### Task 16: Composition, smoke and docs

**Files:** Modify `cmd/vantigo/main.go`, `scripts/smoke-image.sh`, `CONTRIBUTING.md`; delete each module's `unimplemented.go` and `pendingOperations`.

- [ ] **Step 1: Wire the modules.** `serve` composes identity plus every module `MODULES` enables, passing the directory through `Deps`. A composition error exits 1 with the error, never a secret.
- [ ] **Step 2: Close the coverage gates.** Delete the three `unimplemented.go` files and the three `pendingOperations` vars; each `TestMain` becomes `os.Exit(contracttest.RequireCoverage(m, recorder))`. The build then proves every strict interface is complete.
- [ ] **Step 3: Extend the smoke test** with one probe per module, in the script's existing style: `GET /api/v1/customers` → 401 without a session, and the same for `/api/v1/products` and `/api/v1/energy/metering-points`. Run `mise run smoke` and `shellcheck scripts/smoke-image.sh`.
- [ ] **Step 4: Document.** In `CONTRIBUTING.md`'s Go server section, extend the module list, document `MODULES`, `BRREG_BASE_URL` and `BRREG_TIMEOUT`, and note that each module's tests validate every exchange and require every operation to be exercised successfully.
- [ ] **Step 5: Verify everything.** From `apps/server`: `go test ./... -count=1`, `golangci-lint run`, `go generate ./...` with no drift. From the root: `mise run server:check` and `mise run smoke`. Run the identity and module suites pinned with `taskset -c 0-3` to reproduce CI's pool sizing.
- [ ] **Step 6: Commit.** `feat(server): serve the customers, products and energy modules`

---

## Self-review against the spec

- **Spec coverage.** Precedence routing → Task 1. `MODULES` → Task 2. Customer directory → Task 3, consumed in Task 14. Schemas and baselines → Tasks 4, 10, 13. Conditional pricing permission → Task 11. Money and column scales → Tasks 10–12. RFC 3339 timestamps → Task 15. FK `RESTRICT` → 409 → Task 12. Parity quirks (`PUT` ignoring variants, the attention stub, camelCased option keys) → Tasks 11–12. Brreg, including its config and the dropped circuit breaker → Task 8. GiST constraint and partitioning → Task 13, raced in Task 14. Aggregation SQL → Task 15. Smoke, docs and coverage closure → Task 16.
- **Test coverage.** Customers 100 across Tasks 6–9; products 72 across Tasks 10–12 and 16; energy 43 across Tasks 13–15; 215 total, each marked. (Corrected from 79/222 in Task 16, which also ported the last five owed products tests: the re-expressed `DatabaseContextRegistrationTests` fact and the four `ProductsAuthorizationEndpointsTests` matrix facts.)
- **Type consistency.** `CustomerDirectory`, `CustomerEntry`, `ContactEntry` and `ContactMatch` are declared once in Task 3 and used unchanged in Task 14. `Deps.Directory` is the only new `Deps` field. `Module.Directory` is the only new `Module` field.
