# Products module — behavioural inventory for the Go port

Input to the Go port design/plan for sub-project 4 (Customers, Products, Energy). Describes what the .NET Products
module *does* (beyond `openapi/products.yaml`) so the Go port reproduces it. Tenancy is dropped; every tenancy
touchpoint is noted below and marked "drop", not described in depth.

Path abbreviations: `EP/` = `apps/products/backend/Products.Module/Endpoints/`, `DM/` = `…/Products.Module/Domain/Products/`,
`DB/` = `…/Products.Module/Database/`, `AZ/` = `…/Products.Module/Authorization/`, `SV/` = `…/Products.Module/Services/`,
`TS/` = `apps/products/backend/Products.Module.Tests/`, `CT/` = `packages/contracts/Vantigo.Contracts/`,
`HOST/` = `apps/host/backend/Vantigo.Host/`. "unverified" = not provable from repo source (usually ASP.NET/EF/Npgsql
framework internals).

Contract: `apps/server/internal/openapi/specs/products.yaml`, 26 operations, all `x-vantigo-access: permission:…`
(no `policy:`, `anonymous`, `session` or `scim` ops in this module). Every route maps 1:1 to a `Vantigo.Products`
minimal-API endpoint (`EP/ProductsEndpoints.cs`, `EP/CategoriesEndpoints.cs`, `EP/TaxCategoriesEndpoints.cs`,
`EP/ProductStatsEndpoints.cs`) — **13 product/variant/price ops + 5 category ops + 5 tax-category ops + 3 stats
ops = 26**, an exact match in both directions. No orphaned .NET route, no contract operation without code.

## 1. Endpoints

All routes are mounted under `/api/v{version}/products` (`v1` only) via `NewVersionedApi().MapTenantGroup(...)`
with a bare `RequireAuthorization()` on the group (`EP/VersionedBusinessEndpointExtensions.cs:9-14`) — every route
also requires at least one `permission:` policy, added per-route with `.RequirePermission(key)`
(`EP/ProductsEndpoints.cs`, `EP/CategoriesEndpoints.cs`, `EP/TaxCategoriesEndpoints.cs`,
`EP/ProductStatsEndpoints.cs:172-178`). `RequirePermission` calls `RequireAuthorization(policyName)` per key
(`packages/contracts/Vantigo.Contracts.AspNetCore/Authorization/PermissionEndpointConventionExtensions.cs:16-21`);
ASP.NET Core combines multiple `RequireAuthorization` calls on one endpoint with **AND** semantics — every listed
key must succeed, matching the contract's `+`-joined `x-vantigo-access` value. The actual per-permission check is
identity's shared handler (`identity-inventory.md` §1, `PermissionAuthorization.cs:41-100`): catalog membership,
fresh `IsDisabled`/`LockoutEnd` read, `Owner` short-circuits to allow, otherwise direct + active-group roles →
`role_permissions`. **Every** `permission:` policy also requires `ActiveAccount` + `Business`
(`PermissionPolicyProvider.GetPolicyAsync`, identity `PermissionAuthorization.cs:35-37`), so a disabled/locked
account gets 401 (session invalidated) rather than reaching the Products handler at all — Products contributes no
authorization logic of its own beyond the permission catalog.

Unauthenticated → 401 `AuthErrorResponse` (cookie redirect to `OnRedirectToLogin`, per identity §1); authenticated
but missing a permission → 403 `AuthErrorResponse`. Both happen in ASP.NET's authorization middleware, **before**
the endpoint handler or its model binding runs.

### 1.1 Products (`EP/ProductsEndpoints.cs`)

| Method & path | operationId | Route permissions (AND) | Handler-conditional check | Success | Notes |
|---|---|---|---|---|---|
| GET `/` | getProducts | products-view, variants-view, pricing-view, categories-view, tax-categories-view | — | 200 `PaginatedResponseOfProductResponse` | |
| POST `/` | postProducts | products-manage, variants-manage, variants-view, pricing-view, categories-view, tax-categories-view | if any variant carries pricing data (non-null `standardCost` or non-empty `prices`): also **pricing-view AND pricing-manage**, else 403 (`EP/Products/CreateProductEndpoint.cs:24-37`) | 201 `ProductResponse`, `Location` to `GetProduct` | conditional check is invisible in the contract's static `x-vantigo-access` (§7 oddity 1) |
| GET `/{id}` | getProduct | products-view, variants-view, pricing-view, categories-view, tax-categories-view | — | 200 / 404 | |
| PUT `/{id}` | putProductsById | products-manage, variants-view, pricing-view, categories-view, tax-categories-view | — | 200 / 400 / 404 | **`request.Variants` is silently ignored** (§7 oddity 2) |
| DELETE `/{id}` | deleteProductsById | products-manage | — | 204 (idempotent) / 404 | archives, never deletes |
| GET `/{id}/variants` | getProductsByIdVariants | variants-view, pricing-view | — | 200 / 404 | |
| POST `/{id}/variants` | postProductsByIdVariants | variants-manage, variants-view, pricing-view | same pricing-view+pricing-manage conditional as POST `/` (`EP/Products/Variants/AddProductVariantEndpoint.cs:24-31`) | 201 / 400 / 404 / 409 | |
| PUT `/{id}/variants/{variantId}` | putProductsByIdVariantsByVariantId | variants-manage, variants-view, pricing-view, **pricing-manage** (unconditional) | — | 200 / 400 / 404 / 409 | pricing-manage always required even if the request carries no price changes |
| DELETE `/{id}/variants/{variantId}` | deleteProductsByIdVariantsByVariantId | variants-manage, **pricing-manage** | — | 204 / 404 / 409 | pricing-manage required because delete cascades the variant's prices (tested explicitly, `TS/Integration/ProductsAuthorizationEndpointsTests.cs:181-199`) |
| GET `/{id}/variants/{variantId}/prices` | getProductsByIdVariantsByVariantIdPrices | pricing-view | — | 200 / 404 | |
| POST `/{id}/variants/{variantId}/prices` | postProductsByIdVariantsByVariantIdPrices | pricing-manage, pricing-view | — | 201 / 400 / 404 / 409 | `Location` header is **wrong** — points at the collection, not the new price (§7 oddity 3) |
| PUT `/{id}/variants/{variantId}/prices/{priceId}` | putProductsByIdVariantsByVariantIdPricesByPriceId | pricing-manage, pricing-view | — | 200 / 400 / 404 / 409 | |
| DELETE `/{id}/variants/{variantId}/prices/{priceId}` | deleteProductsByIdVariantsByVariantIdPricesByPriceId | pricing-manage | — | 204 / 404 | no "last price" rule (a variant may end up with zero prices) |

### 1.2 Categories / Tax categories / Stats

| Method & path | operationId | Permissions | Notes |
|---|---|---|---|
| GET `/categories` | getProductsCategories | categories-view | flat adjacency list, ordered by name then id |
| POST `/categories` | postProductsCategories | categories-manage, categories-view | |
| GET `/categories/{id}` | getCategory | categories-view | |
| PUT `/categories/{id}` | putProductsCategoriesById | categories-manage, categories-view | rename/re-parent |
| DELETE `/categories/{id}` | deleteProductsCategoriesById | categories-manage | |
| GET `/tax-categories` | getProductsTaxCategories | tax-categories-view | ordered by name |
| POST `/tax-categories` | postProductsTaxCategories | tax-categories-manage, tax-categories-view | |
| GET `/tax-categories/{id}` | getTaxCategory | tax-categories-view | |
| PUT `/tax-categories/{id}` | putProductsTaxCategoriesById | tax-categories-manage, tax-categories-view | |
| DELETE `/tax-categories/{id}` | deleteProductsTaxCategoriesById | tax-categories-manage | |
| GET `/stats/summary` | getProductsStatsSummary | products-view, variants-view, pricing-view, categories-view, tax-categories-view | `from`/`to` optional, default last 30 days |
| GET `/stats/timeseries` | getProductsStatsTimeseries | same 5 | **only `metric=newProducts` is implemented**; anything else → 400 (`EP/ProductStatsEndpoints.cs:84-90`) |
| GET `/stats/attention` | getProductsStatsAttention | same 5 | **stub**: always returns `[]` regardless of DB state (`EP/ProductStatsEndpoints.cs:103-104`) — §7 oddity 4 |

### 1.3 Validation/refusal order (mutating endpoints)

Order matters because early checks short-circuit and the response envelope differs (`ValidationProblem`
`{errors}` for field errors vs. `ProblemDetails` `{title,detail}` for named conflicts, both under
`application/problem+json`; see `HttpValidationProblemDetails`/`ProblemDetails` in `common.yaml:26-45,78-93`).

- **CreateProductEndpoint** (`EP/Products/CreateProductEndpoint.cs`): route permissions → conditional
  variants-view re-check (redundant, `:24-28`) → conditional pricing-view+pricing-manage (`:30-37`) →
  `ProductRequest.Validate(requireVariants:true)` (name/type/status/taxCategoryId/description, then per-variant:
  sku/barcode/unit/non-negatives, then per-price validate, then pairwise `ProductPricing.Conflicts`) → duplicate
  SKU within the request or already in DB → 409 → duplicate barcode within request or in DB → 409 → categoryId
  existence → 400 field error → taxCategoryId existence → 400 field error → insert → 201.
- **UpdateProductEndpoint** (`EP/Products/UpdateProductEndpoint.cs`): `Validate(requireVariants:false,
  validateVariants:false)` (so `variants` in the body is **not even validated**) → product exists (404) →
  categoryId existence (400) → taxCategoryId existence (400) → apply Name/Type/Status/TaxCategory/Description/
  CategoryId → save. `Variants` in the request body is read nowhere in the handler.
- **AddProductVariantEndpoint**: conditional pricing check → `VariantRequest.Validate` → product exists (404) →
  duplicate SKU in DB (409) → duplicate barcode in DB (409) → insert (`:56-59`) → 201.
- **UpdateProductVariantEndpoint**: `Validate` → variant exists scoped to `(id, variantId)` (404) → load product
  status → if SKU changed: product must be `Draft` else 409 "SKU is immutable"; then duplicate-SKU check (409) →
  duplicate-barcode check unconditionally (409, even when SKU unchanged) → apply fields → save.
- **DeleteProductVariantEndpoint**: variant exists (404) → "is this the last variant of the product?" (409 "Last
  variant") → delete.
- **AddProductPriceEndpoint**: `Validate` → variant exists scoped to product (404) → `ProductPricing.Conflicts`
  against every existing price of that variant (409 "Overlapping price") → append → 201.
- **UpdateProductPriceEndpoint**: `Validate` → variant AND price (scoped) exist (404 if either missing) →
  `Conflicts` against every other price (excluding itself) (409) → apply → 200.
- **DeleteProductPriceEndpoint**: single scoped existence query (404) → delete → 204.
- **CreateCategoryEndpoint**: `Validate` (name) → parentId existence (400 field) → duplicate sibling name (409) →
  insert.
- **UpdateCategoryEndpoint**: `Validate` → category exists (404) → `parentId == id` self-parent (400 field,
  checked **before** parent-existence) → parent exists (400 field) → cycle check via
  `ProductCategoryHierarchy.WouldCreateCycle` (409) → duplicate sibling name excluding self (409) → apply.
- **DeleteCategoryEndpoint**: exists (404) → has subcategories (409) → has assigned products (409) → delete.
- **CreateTaxCategoryEndpoint** / **UpdateTaxCategoryEndpoint** / **DeleteTaxCategoryEndpoint**: `Validate` →
  [exists (404) for update/delete] → duplicate name excluding self (409) → [products reference it (409) for
  delete] → apply.
- **GetProductsEndpoint** (list): query validated first (page ≥1, pageSize 1-100, sortBy ∈ {id,name,sku}
  case-sensitive, sortDirection ∈ {asc,desc} case-sensitive, status enum case-insensitive, categoryId ≥1,
  categoryId+uncategorized mutually exclusive) → 400 `ProblemDetails` (not `HttpValidationProblemDetails` — this
  list endpoint uses a single joined `detail` string, not a field-keyed `errors` map, unlike every mutating
  endpoint) → filter (search/status/category) → count → sort → page.

## 2. Domain rules

Entities (`DM/Product.cs`, `DM/ProductVariant.cs`, `DM/ProductPrice.cs`, `DM/ProductCategory.cs`,
`DM/TaxCategory.cs`) are plain mutable classes; there is no aggregate boundary enforcement in code beyond what the
endpoints check by hand (e.g. nothing stops direct DbContext misuse from leaving a product with zero variants).

- **Product** (`DM/Product.cs:9-59`): `Name` (≤200, `Product.NameMaxLength:12`), `Description` (≤4000,
  `DescriptionMaxLength:15`, plain text, trimmed, empty/whitespace → null), `CategoryId` (0 or 1 category),
  `Type` ∈ {Goods, Service} (`DM/ProductType.cs`), `Status` ∈ {Draft, Active, Discontinued}
  (`DM/ProductStatus.cs`, default Draft), `TaxCategoryId` required, `Variants` (≥1, enforced only on create).
  Products are **never hard-deleted**; DELETE archives (sets `Discontinued`), idempotently
  (`EP/Products/ArchiveProductEndpoint.cs:28-32`).
- **ProductVariant** (`DM/ProductVariant.cs:9-64`): `Sku` (≤64, `SkuMaxLength:12`, required, unique across the
  whole catalog — not scoped to the product), `Barcode` (optional, GTIN, unique when set), `Unit` (≤20,
  `UnitMaxLength:15`, default `"pcs"`, `DefaultUnit:18`), `StandardCost`/`WeightKg`/`LengthCm`/`WidthCm`/
  `HeightCm` all optional decimals validated only as "≥0" (no upper bound), `OptionValues` free-form
  `Dictionary<string,string>` compared case-insensitively at the C# level (`StringComparer.OrdinalIgnoreCase`,
  `DM/ProductVariant.cs:54` and DTOs) but stored/serialized with whatever casing the client sent (see §7 oddity 5
  for what happens to that casing on the wire).
  - **SKU immutability**: once the *product's* status is not `Draft`, the SKU on any of its variants cannot
    change (409 "SKU is immutable", `EP/Products/Variants/UpdateProductVariantEndpoint.cs:37-42`). Status is
    re-read fresh per request, not cached.
  - **Last-variant rule**: a product must always keep ≥1 variant; deleting the last one is 409
    (`EP/Products/Variants/DeleteProductVariantEndpoint.cs:20-23`). This is app-level only — see §4.
- **Gtin** (`DM/Gtin.cs`): GTIN-8/12/13/14 only (digit count exactly 8, 12, 13 or 14), digits-only, and the
  rightmost digit is a check digit computed with alternating 3-1 weights counted from the right
  (`DM/Gtin.cs:16-40`). Rejects any non-digit character, including spaces or dashes inside an otherwise valid
  digit string (tested `TS/Domain/Products/GtinTests.cs:38-45`).
- **ProductCategory** (`DM/ProductCategory.cs`, `DM/ProductCategoryHierarchy.cs`): adjacency list via nullable
  `ParentId`. `WouldCreateCycle` walks the ancestor chain of the *proposed* new parent looking for the category
  being moved (`ProductCategoryHierarchy.cs:14-31`) — O(depth) using a full in-memory `id → parentId` map (the
  catalog is assumed small, no pagination anywhere in categories). `GetSelfAndDescendantIds` builds a
  parent→children lookup and DFS/stack-walks to collect a whole subtree (`:37-60`), used to filter product lists
  by category+descendants. Sibling names must be unique **including among roots** (parent_id IS NULL) — see §3's
  `NULLS NOT DISTINCT` index. Name equality is exact/ordinal (no normalization), so "Furniture" and "furniture"
  can coexist as distinct siblings.
- **TaxCategory** (`DM/TaxCategory.cs`, `DM/TaxCategoryKind.cs`): `Kind` ∈ {Standard, Reduced, Zero, Exempt},
  `Rate` a fraction validated `0 ≤ rate ≤ 1` (`EP/TaxCategories/Dtos/TaxCategoryRequest.cs:30-33`) with **no
  rounding at the app level** — Postgres `numeric(5,4)` (§3) silently rounds/truncates to 4 decimal places on
  insert. Products reference a tax category by id rather than copying the rate, so a rate edit changes what
  *future* reads compute — there is no snapshot/versioning of historical rates in this module (the module's own
  docs say consumers must snapshot the rate at transaction time, `docs/products.md:33-35`).
- **Money / pricing** (`DM/ProductPrice.cs`, `DM/ProductPricing.cs`):
  - No dedicated Money type — `Amount` is a plain `decimal`, `Currency` a 3-letter ISO 4217 string, uppercased on
    input (`EP/Products/Dtos/ProductPriceRequest.cs:41`), validated as exactly 3 ASCII letters
    (`ProductPriceRequest.cs:20-25`) but **not checked against a real currency list** — "ZZZ" passes.
  - Storage: `numeric(12,2)` for `amount` and `standard_cost` (2 decimal places — VAT-exclusive minor-unit
    currencies like JPY are not special-cased); `numeric(10,3)` for weight; `numeric(10,1)` for the three
    dimensions (`DB/Products/Configurations/ProductPriceEntityTypeConfiguration.cs:41-45`,
    `ProductVariantEntityTypeConfiguration.cs:62-85`). The app only validates `amount ≥ 0`; **more than 2
    decimal digits is not rejected — Postgres silently rounds on write** (framework/DB default, not app logic).
    A Go port must decide and implement that rounding explicitly (e.g. round-half-away-from-zero to match
    Postgres `numeric` casting) since Go has no implicit column-scale rounding.
  - `Currency` column is `character(3)` (fixed-length, blank-padded bpchar) rather than `varchar(3)`
    (`ProductPriceEntityTypeConfiguration.cs:33-39`) — semantically irrelevant here since the value is always
    exactly 3 letters, but Postgres `bpchar` comparison/equality ignores trailing spaces, a behavior a plain Go
    `string`/`CHAR` column choice should replicate deliberately or avoid by using `varchar`/`text`.
  - Tax handling: prices are stored **excluding VAT** everywhere (doc comment, `DM/ProductPrice.cs:6-11`); VAT is
    applied by whichever consumer reads the `TaxCategory.Rate`. Nothing in Products computes a gross price.
  - **Effective price resolution** (`ProductPricing.GetEffectivePrices`, `:14-28`): group prices by currency
    (ordinal-ignore-case), keep only rows valid at `moment` (`IsValidAt`: `ValidFrom ≤ moment < ValidTo`, either
    bound nullable/open), then pick per group: bounded (campaign) rows beat open-ended (base) rows, ties broken
    by latest `ValidFrom` (nulls treated as `DateTimeOffset.MinValue`), ties on that broken by highest `Id`
    (insertion order tiebreak) — output sorted by currency.
  - **Conflict rule** (`ProductPricing.Conflicts`, `:51-64`): two prices in the same currency, of the **same
    boundedness** (both open-ended, or both bounded), whose windows overlap (`Overlaps`, half-open interval
    intersection with `MinValue`/`MaxValue` substituted for null) conflict. A bounded campaign price never
    conflicts with the open-ended base price of the same currency — that's the intended "campaign overrides
    base" mechanism. This rule is enforced **only in application code**, at request time, with no DB backstop
    (§3, §4) — a race can create two overlapping same-kind prices; the effective-price tiebreak above then
    silently picks one deterministically rather than erroring at read time.
  - `moment` is always `DateTimeOffset.UtcNow` captured once per response construction — not client-suppliable,
    so "effective now" cannot be queried for a past/future instant through the API.

## 3. Persistence

Schema `products` (`DB/Products/ProductsDbContext.cs:27`), 5 tables, all configured explicitly (no assembly
scanning, `ProductsDbContext.cs:28-34`) from `DB/Products/Configurations/*.cs`. Every table's primary key and
every foreign key is currently **`(tenant_id, id)`** composite because of `ITenantOwned`/`ApplyTenantOwnership`
(`ProductsDbContext.cs:35`) — every `tenant_id` column, every composite key/FK, and Postgres **Row-Level
Security** enabled per table (`Migrations/20260816005310_Initial.cs:230-234`, made null-safe by
`Migrations/20260825105929_TenantRlsPolicyNullSafe.cs`) are **tenancy and are dropped**: the Go port collapses
every composite `(tenant_id, id)` key/FK back to a plain `id`, drops RLS entirely (per the parent design doc,
"No RLS... no longer load-bearing"), and drops the `tenant_id` column from all 5 tables.

| Table | EF config | Columns (non-tenancy) | Constraints/indexes | Notable |
|---|---|---|---|---|
| `product_categories` | `ProductCategoryEntityTypeConfiguration.cs` | `id` (identity, start 1001), `name` varchar(200), `parent_id` int? | FK `parent_id→id` **Restrict**; unique `(parent_id, name)` with **`NULLS NOT DISTINCT`** (`:47-49`, i.e. Postgres 15+ `UNIQUE NULLS NOT DISTINCT` — two root categories, both `parent_id IS NULL`, with the same name collide); index `(parent_id)` | no `created_at`/`updated_at` columns at all |
| `tax_categories` | `TaxCategoryEntityTypeConfiguration.cs` | `id`, `name` varchar(100), `kind` varchar(20) (enum-as-string), `rate` numeric(5,4), `created_at`, `updated_at` | unique `(name)` | |
| `products` | `ProductEntityTypeConfiguration.cs` | `id`, `name` varchar(200), `description` varchar(4000)?, `category_id` int?, `type` varchar(20), `status` varchar(20), `tax_category_id` int, `created_at`, `updated_at` | FK `category_id→product_categories.id` **Restrict**; FK `tax_category_id→tax_categories.id` **Restrict**; index `(category_id)`, index `(tax_category_id)` | variants cascade-delete from here (`:91-95`) though products are never actually hard-deleted in normal operation |
| `product_variants` | `ProductVariantEntityTypeConfiguration.cs` | `id`, `product_id`, `sku` varchar(64) non-unicode, `barcode` varchar(14)?, `unit` varchar(20), `standard_cost` numeric(12,2)?, `weight_kg` numeric(10,3)?, `length_cm`/`width_cm`/`height_cm` numeric(10,1)?, `option_values` **jsonb**, `created_at`, `updated_at` | unique `(sku)`; unique **partial** `(barcode) WHERE barcode IS NOT NULL`; FK `product_id→products.id` **Cascade**; index `(product_id)` | `option_values` uses an explicit `HasConversion` to/from JSON text with a custom `ValueComparer` (case-insensitive key equality for EF change-tracking) rather than Npgsql's dynamic JSON, because the host owns the shared `NpgsqlDataSource` (`:91-100`) |
| `product_prices` | `ProductPriceEntityTypeConfiguration.cs` | `id`, `variant_id`, `currency` **char(3)**, `amount` numeric(12,2), `valid_from`/`valid_to` timestamptz? | FK `variant_id→product_variants.id` **Cascade**; index `(variant_id, currency)` and `(variant_id)`, **both non-unique** | **no exclusion/GiST constraint and no unique constraint over overlapping validity windows** — see below |

**No GiST/exclusion constraint exists anywhere in Products.** The parent design doc's "`btree_gist` and the GiST
exclusion constraint" (`docs/superpowers/specs/2026-09-10-go-backend-port-design.md:186-187`) refers to the
**Energy** module's `energy.supply_periods` table
(`apps/energy/backend/Energy.Module/Database/Energy/Migrations/20260816005144_Initial.cs:22,176`:
`EXCLUDE USING gist (tenant_id WITH =, metering_point_id WITH =, tstzrange(start, COALESCE("end",'infinity'),'[)') WITH &&) WHERE (status <> 'Cancelled')`),
not to Products — verified by grepping every Products migration and EF configuration for `gist`/`exclu`/
`btree_gist` (no hits). Products' equivalent temporal-overlap rule (§2, `ProductPricing.Conflicts`) is **pure
application code with no database-level enforcement at all**, unlike Energy's DB-enforced version — a real gap,
not a naming/porting artifact (§4, §7 oddity 6).

Migration files: `Migrations/20260816005310_Initial.cs` (all 5 tables + RLS) and
`Migrations/20260825105929_TenantRlsPolicyNullSafe.cs` (RLS-only, tenancy, drop). `ProductsDatabaseConfiguration.cs`
wires one shared `NpgsqlDataSource` per process (`:26-36`) used by both the runtime pool and (via a separate
data source built from a migrations connection string) the migrator (`:49-70`) — a least-privilege runtime role
distinct from the migration/schema-owner role, per `docs/tenancy.md`; this role split has nothing to do with
tenancy and should be kept in spirit in Go (a migration-only DB role vs. an app DML-only role).

## 4. Concurrency

No optimistic-concurrency column exists anywhere in Products — no `concurrency_stamp`/`xmin`/version field on any
of the 5 entities or in any response DTO (contrast Identity's `role.concurrencyStamp`,
`identity-inventory.md:499`). No explicit transactions, isolation-level overrides, or advisory locks are taken by
Products code (contrast Identity's `Serializable` + `pg_advisory_xact_lock` for bootstrap/owner mutations,
`identity-inventory.md §4,7`). Every handler does at most one `SaveChangesAsync` call, wrapped in EF Core's
implicit per-`SaveChanges` transaction at Npgsql's default isolation (Read Committed) — unverified beyond "no
code overrides it".

What actually answers a conflict:
- **Real, DB-enforced conflicts** (SKU, barcode, category sibling name, tax-category name): the app does a
  friendly `AnyAsync` pre-check (race-prone, TOCTOU) that returns a specific 409 with a helpful title/detail
  *and* the table carries the matching Postgres unique index/constraint as the actual backstop. When two
  requests race past the pre-check, the loser's `DbUpdateException`/`PostgresException` with SQL state
  `23505` (unique_violation) is caught by the **host-wide** exception handler
  (`HOST/Diagnostics/VantigoExceptionHandler.cs:83-98`) and turned into a generic 409 `ProblemDetails` (`title`
  omitted, `detail` = the fixed string `"The request conflicts with data that already exists. Verify the values
  and try again."`) — **not** the endpoint's specific title/detail. `TS/Integration/ProductConcurrencyConflictTests.cs:21-38`
  proves this end-to-end: 4 concurrent `POST /products` with the same SKU yield exactly one 201 and three 409s.
  `23503` (foreign_key_violation, e.g. `Restrict` FKs on `category_id`/`tax_category_id`) is **not** in
  `IsConstraintConflict` (`VantigoExceptionHandler.cs:97-98`) — only `23505` and `23P01` (exclusion_violation)
  are mapped to 409. So a race where a category/tax-category is deleted concurrently with a product being
  assigned to it (or vice versa) can surface as an **unmapped 500**, not a 409, because there is no unique or
  exclusion constraint backing those particular checks, only `Restrict` FKs. Whether this is intentional or a
  latent gap is unverified; the Go port should decide deliberately rather than inherit it silently.
- **App-only conflicts with no DB backstop at all**: overlapping price windows (§2, §3) and the
  "product must keep ≥1 variant" rule. Nothing prevents two concurrent variant-delete calls from both passing
  the "not last" check when exactly two variants remain, leaving zero; nothing prevents two concurrent
  same-currency/same-kind price adds from both passing `Conflicts` and creating an ambiguous pair (the read-side
  tiebreak in `ProductPricing.GetEffectivePrices` then silently and deterministically resolves which one "wins"
  rather than erroring). A Go port that wants stronger guarantees here needs new DB constraints (e.g. a
  `btree_gist` exclusion constraint on `(variant_id, currency, is_bounded, tstzrange(valid_from, valid_to))`
  filtered appropriately) that the .NET code never had.
- The **SKU-immutable-once-Active** and **update-vs-archive** races are similarly unenforced beyond a
  read-then-write check inside one `SaveChangesAsync` (no row lock, no `SELECT … FOR UPDATE`).

## 5. Cross-cutting

- **Rate limiting**: none. No `RequireRateLimiting` call anywhere in the module (grep across
  `apps/products/backend/Products.Module` is empty), unlike Identity's nine named policies.
  `IsConstraintConflict`/global rate limiting (if any) is host-level and untouched by Products.
  **unverified** whether the host's global default limiter (if one exists) applies; nothing in Products
  configures per-route limits.
- **Audit / events**: none. No audit table, no domain events, no outbox writes originate from Products (grep for
  `Audit|IEvent|Publish|OutboxMessage` in the module is empty) — contrast Identity's transactional
  `authorization_audit_events` and Communications' outbox.
- **Permission catalog** (`AZ/ProductsPermissionCatalog.cs`, contributed via
  `IPermissionCatalogContributor` → `AddSingleton`, `DB/ProductsDatabaseConfiguration.cs:25`): 10 permissions, all
  `Delegable: true`, all `Sensitive: false` (the constructor omits both flags, and
  `PermissionDescriptor`'s defaults are `Sensitive=false, Delegable=true`,
  `packages/contracts/Vantigo.Contracts/Authorization/PermissionDescriptor.cs:9-15`).

  | Key | Display | Description | Category |
  |---|---|---|---|
  | `products:products-view` | View products | View products and their details. | Products |
  | `products:products-manage` | Manage products | Create, update, and archive products. | Products |
  | `products:variants-view` | View variants | View product variants. | Variants |
  | `products:variants-manage` | Manage variants | Create, update, and delete product variants. | Variants |
  | `products:pricing-view` | View pricing | View product variant prices. | Pricing |
  | `products:pricing-manage` | Manage pricing | Create, update, and delete product variant prices. | Pricing |
  | `products:categories-view` | View categories | View product categories. | Categories |
  | `products:categories-manage` | Manage categories | Create, update, and delete product categories. | Categories |
  | `products:tax-categories-view` | View tax categories | View product tax categories. | Tax categories |
  | `products:tax-categories-manage` | Manage tax categories | Create, update, and delete product tax categories. | Tax categories |

  `TS/Integration/ProductsAuthorizationEndpointsTests.cs:27-37` asserts the catalog contains exactly these 10
  keys for module `products` — a startup drift test the Go port should keep an equivalent of.
- **Published for other modules**: `IProductCatalog` (`CT/Products/IProductCatalog.cs`), implemented by
  `SV/ProductCatalog.cs`, registered `AddScoped` (`DB/ProductsDatabaseConfiguration.cs:43`). Two methods:
  `SearchAsync(query, take≤10, ct)` and `GetByIdAsync(productId, ct)`. Both hard-filter to **`Status == Active`**
  only (Draft/Discontinued are invisible to other modules regardless of caller permissions) and both deliberately
  omit `StandardCost`, tax configuration, and timestamps from the returned `ProductCatalogProduct`/
  `ProductCatalogVariant`/`ProductCatalogSearchResult` records (`SV/ProductCatalog.cs:10-13`, asserted by
  reflection in `TS/Integration/ProductCatalogContractTests.cs:92-95`). `SearchAsync` matches name/description/
  category name (`ILIKE %query%`) or variant SKU/barcode (`ILIKE`) case-insensitively, ranks exact-name matches
  first, then name-prefix matches, then alphabetical, clamps `take` to `[1,10]`
  (`SV/ProductCatalog.cs:19-52,`), and throws `ArgumentException` on a null/blank query (framework
  `ArgumentException.ThrowIfNullOrWhiteSpace`, `:24`) — a thrown exception, not a typed error result; a Go port
  exposing the same contract internally should decide its own equivalent (panic vs. error return) since Go has
  no framework-level "throws" convention to copy. `GetByIdAsync` additionally requires the product have at least
  one variant belonging to the same tenant (`:79`, tenancy — drop that half of the predicate).
- No branding/localization, no SMTP/email, no maintenance-mode interaction, no OIDC/SCIM touchpoints in this
  module.

## 6. Tests

`TS/Integration/ProductsApiFactory.cs` boots the whole host (`WebApplicationFactory<Program>`) against a real
Postgres in Testcontainers, with only the Products module enabled (`Modules:Products:Enabled=true`, others
false, `:163-166`), bootstraps an Owner via the Identity module's `/bootstrap` endpoint, and exposes helpers to
create scoped-permission users through Identity's role/access-group endpoints
(`CreateAuthenticatedClientAsync`, `CreateUserWithCredentialsAsync`, `UserConcurrencyStampAsync`). This means
several "Products" tests actually exercise Identity's permission plumbing end-to-end; they are still Products
behavior tests (which permissions gate which routes) and must be ported, just via whatever the Go port's
equivalent test harness is.

| Class | F | T (cases) | Covers | Drop? |
|---|---|---|---|---|
| `Domain/Products/GtinTests` | 0 | 4 (16) | GTIN-8/12/13/14 check-digit validation: correct, wrong, unsupported length, non-digit | port |
| `Domain/Products/ProductCategoryHierarchyTests` | 6 | 0 | cycle detection (descendant, self, unrelated, root) and self+descendant id collection | port |
| `Domain/Products/ProductPricingTests` | 13 | 0 | effective-price resolution (base/campaign/expired/future/none/other-currency/multi-currency/overlapping-campaign-tiebreak) and `Conflicts` (same/diff currency, campaign-vs-base, overlapping/disjoint campaigns) | port |
| `Integration/CategoriesEndpointsTests` | 14 | 0 | category CRUD, root/subcategory creation, product counts, missing-name/unknown-parent field errors, duplicate-sibling conflict, rename/reparent, cycle rejection, self-parent field error, delete guards (children, products), delete happy path, unknown-id 404 | port |
| `Integration/DatabaseContextRegistrationTests` | 1 | 0 | asserts `ProductsDbContext` uses the process's shared `NpgsqlDataSource` (not per-context pools) | port — re-express as "one connection pool is shared", not tenancy |
| `Integration/ProductCatalogContractTests` | 7 | 0 | `IProductCatalog`: active-only visibility, name/sku/barcode/category matching, take clamp + blank-query throw, safe projection (no cost field, reflection-asserted), missing/draft/discontinued → null, cancellation honored, **tenant isolation + same-SKU-across-tenants** | 1 of 7 (`Catalog_isolates_products_and_allows_the_same_sku_per_tenant`) is **tenancy-only, drop**; 6 port |
| `Integration/ProductCatalogFieldsTests` | 9 | 0 | catalog fields round-trip (weight/dims/barcode) on create/update, clearing fields, invalid-GTIN field error, duplicate-barcode conflict (create and update-onto-another-product), unknown-category field error, search by exact barcode / SKU / description | port |
| `Integration/ProductConcurrencyConflictTests` | 1 | 0 | 4 concurrent same-SKU creates → exactly 1×201 + rest 409 (DB unique index is the real backstop) | port |
| `Integration/ProductsAuthorizationEndpointsTests` | 6 | 0 | catalog-completeness assertion; Owner vs. no-permission vs. view-only vs. disabled-user matrix; nested variant/pricing permission requirements on aggregate reads/writes; category+tax-category view requirements on reads; composite-mutation view-permission requirements; variant-delete requiring pricing-manage | port |
| `Integration/ProductsEndpointsTests` | 15 | 0 | create (flattened single variant, missing-variants error, prices→effective-price resolution, duplicate SKU conflict), update-variant duplicate SKU conflict, conflicting base-price field error, update-shared-fields-only, SKU change allowed on Draft / conflict on Active, last-variant delete guard, multi-variant non-flattening, price sub-resource scoping, overlapping open-ended price conflict, invalid validity-window field error, unknown-id 404 | port |
| `Integration/TaxCategoriesEndpointsTests` | 4 | 0 | CRUD round trip, delete-in-use conflict, unknown-tax-category field error on product create, tax category embedded with rate on product response | port |

**Totals**: 80 test methods (76 `[Fact]` + 4 `[Theory]` covering 16 cases). **1 is tenancy-only and dropped**
(`ProductCatalogContractTests.Catalog_isolates_products_and_allows_the_same_sku_per_tenant`). **79 methods carry
over** (75 Facts + 4 Theories/16 cases), the large majority verbatim; `DatabaseContextRegistrationTests` needs
re-expression for Go's connection-pooling model rather than a literal port.

## 7. Oddities (likely port mistakes)

1. **Conditional pricing-manage checks are invisible in the contract.** `POST /products` and
   `POST /{id}/variants` additionally require `products:pricing-view` **and** `products:pricing-manage` only
   when the submitted variant(s) carry pricing data (`standardCost` set or any `prices` entries) — checked
   imperatively inside the handler (`CreateProductEndpoint.cs:30-37`, `AddProductVariantEndpoint.cs:24-31`), not
   via route metadata. The contract's static `x-vantigo-access` for both operations lists only `pricing-view`,
   never `pricing-manage`. A Go port whose central router only consults the static `x-vantigo-access` string
   will under-authorize these two operations unless it also implements this conditional, in-handler check.
2. **`PUT /products/{id}` silently ignores `variants` in the request body.** The DTO (`ProductRequest`) has a
   `Variants` field and the contract's `ProductRequest` schema includes it, but `UpdateProductEndpoint.cs:19`
   calls `Validate(requireVariants:false, validateVariants:false)` and the handler never reads
   `request.Variants` at all — a client that PUTs a full product including changed variants gets a 200 with the
   variants completely unchanged, no error, no warning.
3. **Wrong `Location` header on price creation.** `AddProductPriceEndpoint.cs:52-54` returns
   `TypedResults.Created($"/api/v1/products/{id}/variants/{variantId}/prices", …)` — the URL of the *collection*,
   not `.../prices/{price.Id}`. Every other `Created`/`CreatedAtRoute` response in the module points at the
   actual new resource.
4. **`GET /stats/attention` is a stub.** It unconditionally returns `[]` (`ProductStatsEndpoints.cs:103-104`);
   nothing in the DB is queried. Anyone porting "attention items" behavior needs a spec beyond this code — there
   isn't one; the .NET behavior to preserve *is* "always empty."
5. **`OptionValues` keys are silently camelCased on the wire.** Internally the key casing supplied by the client
   is preserved verbatim (comparisons are case-insensitive via `StringComparer.OrdinalIgnoreCase`, but the
   dictionary's actual key strings are not rewritten anywhere in Products code). Yet
   `TS/Integration/ProductsEndpointsTests.cs:157,213` index the JSON response with a **lowercase** key
   (`optionValues["color"]`) after posting `Color` — proof that ASP.NET Core's default `JsonSerializerDefaults.Web`
   options (no explicit `DictionaryKeyPolicy` is set anywhere in the host — grep is empty) apply
   `PropertyNamingPolicy = CamelCase` to `Dictionary<string,string>` **keys**, not just POCO property names. A
   multi-word key like `ShoeSize` would come back as `shoeSize`. Go's `encoding/json` has no such implicit
   behavior for map keys — the port must pick and implement a deliberate policy (preserve-as-typed vs. explicit
   camelCase) rather than assume "JSON serialization" carries this over for free.
6. **No DB-level protection against overlapping prices, ever** (§3, §4) — unlike Energy's GiST exclusion
   constraint on `supply_periods`, Products relies entirely on an app-level, race-prone pre-check for the
   "campaign vs. base price" invariant. The parent design doc's GiST mention is about Energy, not Products; a
   porter should not go looking for one here, and should decide anew whether to add DB enforcement.
7. **`Restrict`-FK races are unmapped to 409.** The global exception handler only special-cases Postgres
   `unique_violation`/`exclusion_violation` (`23505`/`23P01`); `foreign_key_violation` (`23503`) — which is what
   actually backs the "category/tax-category still referenced" and "duplicate name" `Restrict` FKs — falls
   through to a generic 500. The friendly app-level pre-checks make this rare in practice, but it is not
   race-proof.
8. **List validation uses one error shape, mutation validation uses another.** `GET /products`'s query-parameter
   errors return `ProblemDetails` with a single joined `detail` sentence (`GetProductsEndpoint.cs:130-133`)
   matching the contract's declared 400 schema for that operation (`ProblemDetails`, not
   `HttpValidationProblemDetails`) — but every mutating endpoint's 400 is a field-keyed `errors` map
   (`ValidationProblem`/`HttpValidationProblemDetails`). This split is intentional and matches the contract per
   operation, but a porter building one generic "validation error" helper for the whole module will get this
   wrong for the list endpoint specifically.
9. **Case-sensitivity is inconsistent within a single endpoint.** In `GET /products`, `status` is parsed with
   `Enum.TryParse(ignoreCase:true)` (case-insensitive) while `sortBy`/`sortDirection` are compared with C#
   pattern-match equality against string constants (case-**sensitive** — `?sortBy=ID` is rejected,
   `?sortBy=id` is not). Category/tax-category name uniqueness is exact/ordinal (case-sensitive) with no
   normalization, unlike Identity's normalized role/user names.
10. **SKU is unique catalog-wide, not per-product.** Two different products can never use the same SKU; the
    unique index is `(sku)` alone on `product_variants` (tenancy-adjusted from `(tenant_id, sku)`), and the
    duplicate-SKU checks in `CreateProductEndpoint`/`AddProductVariantEndpoint`/`UpdateProductVariantEndpoint`
    all query across the whole table, not scoped to the product being edited.
11. **Decimal precision has no app-level rounding, only DB-column-scale rounding.** `amount`, `standardCost`
    (2dp), `weightKg` (3dp), and the three dimension fields (1dp) accept any decimal ≥0 from the client; Postgres
    `numeric(p,s)` truncates/rounds on write. Since the Go port has no implicit column-scale coercion the way EF
    + Postgres provide it, this rounding must be implemented explicitly (and a rounding-mode choice made) or the
    stored/returned values will silently differ from .NET's behavior at the boundary.
12. **Framework-default query-parameter binding is unverified.** `GetProductsEndpoint`'s `[AsParameters] Request`
    uses nullable `int?`/`bool?`/`string?` properties bound from the query string by ASP.NET's minimal-API model
    binder; what happens for a syntactically invalid value (e.g. `?page=abc`) is a framework default
    (unverified from this repo whether it 400s or silently binds `null`) that the Go port has no equivalent
    default for and must decide explicitly.
