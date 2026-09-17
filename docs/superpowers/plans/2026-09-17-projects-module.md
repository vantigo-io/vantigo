# Projects Module Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `projects` business module — projects with editable codes, per-project roles, commercial rules and product-pinned billing lines — as a full app in the switcher, plus the three cross-module contracts it needs.

**Architecture:** A vertical slice like `customers`: Go package `internal/projects`, Postgres schema `projects`, contract `openapi/projects.yaml`, frontend package `@vantigo/projects-ui`, composed by the host. It reads customers, users and (optionally) products through in-process contracts in `internal/contracts`, and publishes `contracts.ProjectDirectory` for future modules. No module imports another; no SQL crosses a schema.

**Tech Stack:** Go (pgx v5, sqlc, goose, oapi-codegen strict server, kin-openapi), PostgreSQL, React 19, Mantine 9, TanStack Router + Query, Vitest, Bun workspaces, mise.

**Spec:** `docs/superpowers/specs/2026-09-17-projects-design.md` — read it first; this plan argues from it. Section references (§, D-numbers) point there.

## Global Constraints

- **Pattern files.** This codebase is heavily conventional. Where a task says *mirror X*, open X and copy its structure, comment density, naming and error handling. The canonical patterns: backend `apps/server/internal/customers/` (module.go, server.go, customers.go, customer_type.go, values.go, errors.go, directory.go, harness_test.go, main_test.go, gen/gen_test.go, sqlc.yaml, queries/); contract `openapi/customers.yaml`; frontend `apps/customers/frontend/`; host routes `apps/host/frontend/src/routes/customers*`.
- **Boundaries (enforced by lint and tests):** `internal/projects/**` may not import any other module package, **even in tests** — use fakes against `internal/contracts`. No SQL in `projects` migrations or queries may name another schema. `@vantigo/projects-ui` may not import another module package or the host.
- **Toolchain:** `go`, `sqlc`, `golangci-lint` are mise shims — always `mise exec -- <cmd>`. `bun` via `mise exec -- bun`. Run `golangci-lint` from `apps/server` (from the repo root it prints `0 issues.` having linted nothing). Correct form is `bun run --cwd <dir> test`, never `bun --cwd <dir> run test`. Capture exit codes before any pipe (`cmd; echo "exit=$?"`).
- **Generated code:** after any `openapi/*.yaml` change run `cd apps/server && mise exec -- go generate ./...` **and** `mise exec -- bun run gen:client`; commit the results. After any host route change run `mise exec -- bun run --cwd apps/host/frontend build` and commit the regenerated `src/routeTree.gen.ts`.
- **After any exported Go signature change** run `mise exec -- go vet ./...` from `apps/server` (it compiles other packages' test files; `go build` does not).
- **Tests run against real Postgres.** `mise run server:db` starts it. Go tests: `cd apps/server && mise exec -- go test -count=1 ./internal/<pkg>/...`.
- **Contract coverage gate:** every operation in `projects.yaml` must be exercised by a passing recorded exchange (`contracttest.RequireCoverage` in `main_test.go`). Add an operation and its tests in the same task.
- **Money:** `numeric(12,2)` in SQL, `float64` in Go DTOs and JSON numbers in the API, as products does. Currency `char(3)`.
- **IDs:** project, line and customer IDs are `int32`; user IDs are `uuid.UUID`.
- **Enumerations (exact strings):** status `planned|active|on-hold|completed|cancelled`; billing type `time-and-materials|fixed-price|non-billable`; role `manager|member|viewer`; pricing mode `list|fixed|discount`.
- **Codes:** project `^[A-Z0-9]{2,20}$`, line `^[A-Z0-9]{1,10}$`, input trimmed and upper-cased before validation.
- **Permissions (exact keys):** `projects:access`, `projects:create`, `projects:view-all`, `projects:manage-all`, `projects:view-financials`.
- **i18n:** every user-facing string in `en` and `nb`; `bun run translations:check` must pass (runs in the pre-commit hook).
- **Commits:** Conventional Commits scoped to the area (`feat(projects): …`, `feat(contracts): …`), each ending with the trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`. The pre-commit hook runs translations, i18n tests, biome and gofmt over the whole tree — a failing hook means fix, not `--no-verify`.
- **Branch:** `feat/projects-module` (already created). Never commit to `main`.

## File Structure

```
apps/server/internal/contracts/
  users.go            UserDirectory, UserEntry                         (new)
  catalog.go          ProductCatalog, VariantEntry, Money              (new)
  projects.go         ProjectDirectory, ProjectEntry, BillingLineEntry (new)
apps/server/internal/module/module.go, compose.go   provider slots      (modify)
apps/server/internal/modtest/modtest.go             WithUsers/WithProducts, SignInUser (modify)
apps/server/internal/identity/userdirectory.go      UserDirectory impl  (new)
apps/server/internal/products/catalog.go            ProductCatalog impl (new)
apps/server/internal/projects/
  module.go           manifest: permissions, Module(), mount
  server.go           server struct, newServer
  roles.go            role → capability sets
  authorize.go        authorize(): caller's capabilities on a project
  values.go           code/enum/currency parsing and validation
  suggestion.go       code suggestion (pure letter derivation + handler)
  projects.go         create / get / list / update handlers
  status.go           PUT status
  people.go           roles + assignable users
  lines.go            billing lines
  timeline.go         timeline read + event writers
  stats.go            stats endpoints
  responses.go        row → gen response mapping, financial shaping
  directory.go        contracts.ProjectDirectory impl
  errors.go           problem helpers
  queries/*.sql, store/ (sqlc), gen/ (oapi-codegen), sqlc.yaml
apps/server/internal/db/migrations/00008_projects_baseline.sql
openapi/projects.yaml
apps/projects/frontend/           @vantigo/projects-ui (mirror apps/customers/frontend)
apps/host/frontend/src/           registry, routes/projects*, customer tab, spotlight, dashboard, catalogs
docs/projects.md, docs/module-boundaries.md, CONTRIBUTING.md, ROADMAP.md
```

---

## Slice 1 — Platform contracts

### Task 1: Contract types and provider slots

**Files:**
- Create: `apps/server/internal/contracts/users.go`, `catalog.go`, `projects.go`
- Modify: `apps/server/internal/module/module.go` (Deps + Module), `apps/server/internal/module/compose.go` (provider resolution), `apps/server/internal/modtest/modtest.go`
- Test: `apps/server/internal/module/compose_test.go`, `apps/server/internal/modtest/` (existing tests keep passing)

**Interfaces — Produces:**

```go
// contracts/users.go
type UserEntry struct {
	ID          uuid.UUID
	DisplayName string
	Active      bool // not disabled
}
type UserDirectory interface {
	User(ctx context.Context, id uuid.UUID) (*UserEntry, error)         // missing → (nil, nil)
	Users(ctx context.Context, ids []uuid.UUID) ([]UserEntry, error)   // missing ids simply absent
	SearchUsers(ctx context.Context, query string, limit int) ([]UserEntry, error) // active only, by display name, ordered by display name
}

// contracts/catalog.go
type VariantEntry struct {
	ID, ProductID int32
	ProductName   string
	SKU           string
	Unit          string
	ProductType   string // "Goods" | "Service"
	ProductStatus string
}
type Money struct {
	Amount   float64
	Currency string
}
type ProductCatalog interface {
	Variant(ctx context.Context, id int32) (*VariantEntry, error)
	Variants(ctx context.Context, ids []int32) ([]VariantEntry, error)
	// ListPrice is the variant's effective price in currency at the given moment, nil when it has none.
	ListPrice(ctx context.Context, variantID int32, currency string, at time.Time) (*Money, error)
}

// contracts/projects.go
type ProjectEntry struct {
	ID          int32
	Code, Name  string
	CustomerID  *int32
	Status      string
	OpenForWork bool // Status == "active"
	BillingType string
}
type BillingLineEntry struct {
	ID, ProjectID   int32
	Code            string
	VariantID       int32
	PricingMode     string
	FixedAmount     *float64
	DiscountPercent *float64
	Active          bool
}
type ProjectDirectory interface {
	Project(ctx context.Context, id int32) (*ProjectEntry, error)
	Role(ctx context.Context, projectID int32, userID uuid.UUID) (string, error) // "" = none
	BillingLine(ctx context.Context, projectID, lineID int32) (*BillingLineEntry, error)
	ProjectsForUser(ctx context.Context, userID uuid.UUID) ([]ProjectEntry, error)
}
```

```go
// module.Deps gains (beside Directory):
Users    contracts.UserDirectory   // identity provides it; always set once composed
Products contracts.ProductCatalog  // nil when no enabled module provides one (products disabled)
Projects contracts.ProjectDirectory // nil when projects is disabled

// module.Module gains (beside Directory):
Users    func(Deps) contracts.UserDirectory
Products func(Deps) contracts.ProductCatalog
Projects func(Deps) contracts.ProjectDirectory

// modtest gains:
func WithUsers(u contracts.UserDirectory) Option
func WithProducts(p contracts.ProductCatalog) Option
func (h *Harness) SignInUser(t testing.TB, permissions ...string) (*Client, uuid.UUID) // SignIn delegates to it
```

- [ ] **Step 1: Write the failing Compose tests.** In `compose_test.go`, mirroring the existing customer-directory tests there (find them by searching `Directory`), add:
  - `TestCompose_InjectsUsersProductsProjectsProviders`: three fake modules each declaring one provider; a fourth module's `Mount` captures its `Deps`; assert all three fields are the providers' values.
  - `TestCompose_DuplicateUsersProvider_Fails` (and the same for Products, Projects): error names both modules, message `multiple modules declare a user directory` / `a product catalog` / `a project directory`.
  - `TestCompose_DisabledProvider_LeavesNil`: with `MODULES` excluding the products provider module, the consumer's `Deps.Products` is nil.
  - `TestCompose_PresetDepsSurviveWhenNoProvider`: `Deps.Products` preset to a fake and no provider module → consumer still sees the fake (the seam `modtest.WithProducts` relies on).

- [ ] **Step 2: Run to verify failure.** `cd apps/server && mise exec -- go test -count=1 ./internal/module/...` — expect compile errors (unknown fields).

- [ ] **Step 3: Implement.** Add the three contract files (doc comments in the style of `contracts/directory.go`: what it is for, the `(nil, nil)` rule, DTOs only). Add the fields to `Deps` and `Module` with comments modelled on `Directory`'s. In `compose.go`, generalise the directory block without changing its behaviour or message — a small generic helper keeps it to one place:

```go
// soleProvider returns the one module among mods for which declares reports
// true, nil when none does, and an error naming both when two do.
func soleProvider(mods []Module, what string, declares func(Module) bool) (*Module, error) {
	var provider *Module
	for i := range mods {
		if !declares(mods[i]) {
			continue
		}
		if provider != nil {
			return nil, fmt.Errorf("module: multiple modules declare %s: %q and %q", what, provider.Name, mods[i].Name)
		}
		provider = &mods[i]
	}
	return provider, nil
}
```

  Use it four times (`"a customer directory"`, `"a user directory"`, `"a product catalog"`, `"a project directory"`), each assigning onto `deps` only when a provider exists. Resolution order: Directory, Users, Products, Projects — a provider func receives the `deps` built so far, so `Projects` may use `deps.Directory`. Update the `Compose` doc comment's failure list. In `modtest`, add `users`/`products` to `setup`, the two options (doc comments like `WithDirectory`'s), set `Users`/`Products` on `h.deps`, and refactor `SignIn` to call a new `SignInUser` that returns the seeded user's ID too.

- [ ] **Step 4: Run.** `mise exec -- go test -count=1 ./internal/module/... ./internal/modtest/... ./internal/contracts/...` → PASS. `mise exec -- go vet ./...` → clean.

- [ ] **Step 5: Commit.** `feat(contracts): user, product and project directory contracts with compose slots`

### Task 2: Identity implements `UserDirectory`

**Files:**
- Create: `apps/server/internal/identity/userdirectory.go`, `apps/server/internal/identity/queries/directory.sql`
- Modify: `apps/server/internal/identity/module.go` (`Users: newUserDirectory`), generated `identity/store/*`
- Test: `apps/server/internal/identity/userdirectory_test.go`

**Interfaces — Consumes:** Task 1's `contracts.UserDirectory`, `module.Module.Users`. **Produces:** `identity.Module(a)` sets `Users`.

- [ ] **Step 1: Failing tests.** Identity has its own bespoke harness — read two existing `identity/*_test.go` files first and use the same helpers to seed users. Tests (call the directory directly, built from the harness's `module.Deps`/pool, not over HTTP):
  - `User` returns ID, display name, `Active: true`; a disabled user → `Active: false`; unknown ID → `(nil, nil)`.
  - `Users` with two known and one unknown ID → two entries; empty slice in → empty out, no query error.
  - `SearchUsers("ola", 10)` matches display name case-insensitively as a substring, excludes disabled users, orders by display name, honours the limit; `%` and `_` in the query are matched literally.
- [ ] **Step 2: Verify failure** (`./internal/identity/...` with `-run UserDirectory`).
- [ ] **Step 3: Implement.** Queries in `queries/directory.sql`:

```sql
-- name: DirectoryUser :one
SELECT id, display_name, is_disabled FROM identity.users WHERE id = $1;

-- name: DirectoryUsers :many
SELECT id, display_name, is_disabled FROM identity.users WHERE id = ANY($1::uuid[]);

-- name: DirectorySearchUsers :many
SELECT id, display_name, is_disabled FROM identity.users
WHERE NOT is_disabled AND display_name ILIKE '%' || sqlc.arg(pattern)::text || '%' ESCAPE '\'
ORDER BY display_name, id
LIMIT sqlc.arg(max_rows);
```

  `userdirectory.go` mirrors `customers/directory.go` (struct holding `*store.Queries`, `var _ contracts.UserDirectory`, `pgx.ErrNoRows → (nil, nil)`, errors wrapped `identity: user directory …`). Escape `\`, `%`, `_` in the query before passing it as `pattern`; clamp `limit` to 1–50. Run `go generate ./...`.
- [ ] **Step 4: Run** identity directory tests → PASS; `go vet ./...`.
- [ ] **Step 5: Commit.** `feat(identity): publish a read-only user directory to other modules`

### Task 3: Products implements `ProductCatalog`

**Files:**
- Create: `apps/server/internal/products/catalog.go`; add queries to `apps/server/internal/products/queries/products.sql`
- Modify: `apps/server/internal/products/module.go` (`Products: newCatalog`)
- Test: `apps/server/internal/products/catalog_test.go`

- [ ] **Step 1: Failing tests** (products' `harness_test.go` helpers seed products/variants/prices; call the catalog directly via `newCatalog(h.Deps())` from an internal test file `catalog_internal_test.go` if the constructor is unexported — follow how customers tests its directory):
  - `Variant` returns product name, SKU, unit, type, status; unknown → `(nil, nil)`.
  - `Variants` batch, unknown IDs absent.
  - `ListPrice`: base price only → base; a campaign price valid *now* beats the base; an expired campaign → base; other currency → nil; currency compared case-insensitively. **Assert it equals what the module's own variant response reports as the effective price for the same moment** (find the effective-price function in `products/pricing.go` — the port of `ProductPricing` — and reuse it; do not restate the rule).
- [ ] **Step 2: Verify failure.**
- [ ] **Step 3: Implement** `catalog.go` mirroring `customers/directory.go`; `ListPrice` loads the variant's price rows with the existing query, maps them with `domainPriceFromRow`, and calls the existing effective-price resolver. `go generate ./...`.
- [ ] **Step 4: Run** `./internal/products/...` → PASS; `go vet ./...`.
- [ ] **Step 5: Commit.** `feat(products): publish a product catalog contract to other modules`

---

## Slice 2 — Projects backend core

### Task 4: Module scaffold, schema, create and get

**Files:**
- Create: `apps/server/internal/db/migrations/00008_projects_baseline.sql`, `openapi/projects.yaml`, `apps/server/internal/openapi/gen/cfg-projects.yaml`, `apps/server/internal/projects/{module,server,roles,authorize,values,projects,responses,timeline,errors}.go`, `sqlc.yaml`, `queries/{projects,roles,timeline,counters}.sql`, `gen/gen_test.go`, `harness_test.go`, `main_test.go`, `projects_test.go`, `authorize_test.go`
- Modify: `apps/server/generate.go` (oapi-codegen + sqlc lines for projects), `apps/server/internal/openapi/openapi.go` (`Modules` += `"projects"`), `apps/server/internal/config/config.go` + `config_test.go` (`projects requires customers`), `apps/server/cmd/vantigo/main.go` (`projects.Module()` in `businessModules`), `apps/server/.golangci.yml` (depguard rule set for projects + projects in every other module's deny list), `apps/server/internal/db/schema_test.go` (`moduleSchemas`), any test asserting the known-module list (`grep -rn '"communications"' apps/server --include=*_test.go`)

**Migration (complete):**

```sql
-- +goose Up
CREATE SCHEMA projects;

-- customer_id, created_by_user_id and every other foreign identifier here is
-- opaque: customers, identity and products live in other schemas, so there is
-- no foreign key to them (docs/module-boundaries.md rule 4).
CREATE TABLE projects.projects (
    id                 integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    code               varchar(20)   NOT NULL,
    name               varchar(200)  NOT NULL,
    description        varchar(4000),
    customer_id        integer,
    status             varchar(20)   NOT NULL DEFAULT 'planned',
    start_date         date,
    end_date           date,
    billing_type       varchar(20)   NOT NULL,
    currency           char(3),
    fixed_price_amount numeric(12,2),
    budget_hours       numeric(10,2),
    budget_amount      numeric(12,2),
    revision           integer       NOT NULL DEFAULT 1,
    created_by_user_id uuid          NOT NULL,
    created_at         timestamptz   NOT NULL,
    updated_at         timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_projects_code ON projects.projects (code);
CREATE INDEX ix_projects_customer_id ON projects.projects (customer_id);
CREATE INDEX ix_projects_status ON projects.projects (status);

CREATE TABLE projects.project_roles (
    project_id integer     NOT NULL REFERENCES projects.projects (id),
    user_id    uuid        NOT NULL,
    role       varchar(20) NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX ix_project_roles_user_id ON projects.project_roles (user_id);

CREATE TABLE projects.billing_lines (
    id               integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id       integer       NOT NULL REFERENCES projects.projects (id),
    code             varchar(10)   NOT NULL,
    variant_id       integer       NOT NULL,
    pricing_mode     varchar(20)   NOT NULL,
    fixed_amount     numeric(12,2),
    discount_percent numeric(5,2),
    active           boolean       NOT NULL DEFAULT true,
    created_at       timestamptz   NOT NULL,
    updated_at       timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_billing_lines_project_id_code ON projects.billing_lines (project_id, code);

CREATE TABLE projects.timeline_entries (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id    integer     NOT NULL REFERENCES projects.projects (id),
    event_type    varchar(50) NOT NULL,
    payload       jsonb       NOT NULL,
    actor_user_id uuid,
    actor_display text        NOT NULL,
    occurred_at   timestamptz NOT NULL
);
CREATE INDEX ix_timeline_entries_project_id_occurred_at ON projects.timeline_entries (project_id, occurred_at DESC, id DESC);

CREATE TABLE projects.counters (
    counter_name text   PRIMARY KEY,
    next_value   bigint NOT NULL
);

-- +goose Down
DROP SCHEMA projects CASCADE;
```

(Check the Down of `00003_customers_baseline.sql` and match its form.)

**Contract resource shapes** (write them as OpenAPI schemas in `projects.yaml`, copying `customers.yaml`'s response/problem/`x-vantigo-access` conventions and `common.yaml` refs verbatim):

```
ProjectResponse {
  id int32, code, name, description?, customerId? int32, customerName?, internal bool,
  status, startDate? (date), endDate? (date), billingType, budgetHours? number,
  revision int32, createdAt, updatedAt (date-time),
  managers: [ProjectPersonSummary{userId uuid, displayName}],
  capabilities: {canManage bool, canSeeFinancials bool},
  billingLinesAvailable bool,
  financials?: {currency?, fixedPriceAmount? number, budgetAmount? number}   // ABSENT unless canSeeFinancials
}
ProjectCreateRequest { code, name, description?, customerId?, startDate?, endDate?, billingType,
                       budgetHours?, currency?, fixedPriceAmount?, budgetAmount? }
ProjectUpdateRequest = ProjectCreateRequest + revision int32 (required)
```

This task declares only `POST /api/v1/projects` (`permission:projects:access+projects:create`, 201 + `ProjectResponse`, 400, 401, 403) and `GET /api/v1/projects/{id}` (`permission:projects:access`, 200, 401, 403, 404).

**Authorization core (complete):**

```go
// roles.go
type capability string

const (
	capSee           capability = "see"
	capSeeFinancials capability = "see-financials"
	capManage        capability = "manage"
)

// roleCapabilities is what each project role grants inside this module.
// member and viewer are identical here on purpose: they differ in what later
// modules grant them (spec §5).
var roleCapabilities = map[string][]capability{
	"manager": {capSee, capSeeFinancials, capManage},
	"member":  {capSee},
	"viewer":  {capSee},
}

func validRole(role string) bool { _, ok := roleCapabilities[role]; return ok }
```

```go
// authorize.go
type access struct {
	Role             string // "" when the caller holds none
	CanSee           bool
	CanSeeFinancials bool
	CanManage        bool
}

// globalAccess is what the caller's global permissions grant on every project.
func (s *server) globalAccess(ctx context.Context) access {
	manageAll := contracts.HasPermission(ctx, s.deps.Access, "projects:manage-all")
	viewAll := contracts.HasPermission(ctx, s.deps.Access, "projects:view-all")
	financials := contracts.HasPermission(ctx, s.deps.Access, "projects:view-financials")
	return access{
		CanSee:           manageAll || viewAll,
		CanSeeFinancials: manageAll || financials,
		CanManage:        manageAll,
	}
}

// authorize resolves the caller's access to one project: global permissions
// widened by the caller's role there. view-financials applies only to
// projects the caller can see. The caller answers 404 when !CanSee (D7) and
// 403 when it needs CanManage and lacks it.
func (s *server) authorize(ctx context.Context, q *store.Queries, projectID int32) (access, error) {
	a := s.globalAccess(ctx)
	p, _ := contracts.PrincipalFrom(ctx)
	role, err := q.RoleForUser(ctx, store.RoleForUserParams{ProjectID: projectID, UserID: p.UserID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return access{}, fmt.Errorf("projects: load role: %w", err)
	}
	a.Role = role
	for _, c := range roleCapabilities[role] {
		switch c {
		case capSee:
			a.CanSee = true
		case capSeeFinancials:
			a.CanSeeFinancials = true
		case capManage:
			a.CanManage = true
		}
	}
	if !a.CanSee {
		a.CanSeeFinancials = false
	}
	return a, nil
}
```

**Validation (`values.go`)** — one function per rule, each returning a field error appended to a `map[string][]string` keyed by camelCase field, mirroring `customers/values.go`. Rules exactly as spec §4.1: code (trim, upper, regex, then unique), name ≤200, description ≤4000, customer resolves via `s.deps.Directory.Customer` (archived allowed), billing type enum, internal ⇒ `non-billable`, `fixedPriceAmount` required >0 iff `fixed-price` else must be absent, budgets >0, currency `^[A-Z]{3}$` (upper-cased) and required when any amount is set, `endDate >= startDate`. A unique-violation (`23505` on `ux_projects_code`) from the insert maps to the `code` field error `"already in use"` — this is what makes two racing creates safe.

**Create flow:** one transaction (`db` helpers as customers uses): insert project → insert `project_roles(manager)` for the caller → advance the `project_code` counter (upsert starting at 1000, same shape as customers' `NextCounterValue`) → write `project-created` timeline entry. Actor display: resolve the caller through `s.deps.Users.User`; fall back to `"Unknown user"` if nil.

**Response shaping (`responses.go`):** one `projectResponse(row, access, customerName, managers, linesAvailable)`; `financials` is set only when `access.CanSeeFinancials` — and then always set (possibly all-empty), so clients can tell "may see, nothing entered" from "may not see". `billingLinesAvailable = s.deps.Products != nil`.

**Test harness (`harness_test.go`):** `newHarness` = `modtest.New` with `WithRecorder(recorder)`, `WithModule(projects.Module())`, `WithDirectory(fakeDirectory{})` (customers 1001 "Kraft-Verket", 1002 "Acme Industrier AS", 1003 archived; others nil), and **by default** `WithProducts(newFakeCatalog())`; a `newHarnessWithoutProducts` omits it. The real identity `UserDirectory` is composed automatically. Helpers: `signIn(t, h, perms...) (*modtest.Client, uuid.UUID)` always adding `projects:access`; `createProject(t, c, overrides map[string]any) projectJSON`.

- [ ] **Step 1: Scaffold with no operations passing yet.** Migration; registrations listed under *Modify*; `projects.yaml` with the two operations; `cfg-projects.yaml` (copy `cfg-customers.yaml`, change package paths); `sqlc.yaml` (copy customers', schema = `../db/migrations/00008_projects_baseline.sql`); `generate.go` lines; depguard. Run `go generate ./...`. Write `gen/gen_test.go` and `main_test.go` by copying customers'.
- [ ] **Step 2: Write failing tests** — `authorize_test.go` + `projects_test.go`:
  - create with minimal valid body → 201; code stored upper-cased (`" kvem1000 "` → `KVEM1000`); creator listed in `managers`; `capabilities` both true; `financials` present.
  - each §4.1 rule → 400 with the error on the right field (table-driven: body override → field).
  - duplicate code, differing only in case → 400 on `code`.
  - internal project with `time-and-materials` → 400 on `billingType`; internal + `non-billable` → 201 with `internal: true`, no `customerId`.
  - unknown customer → 400 on `customerId`; archived customer → 201.
  - without `projects:create` → 403; without `projects:access` → 403.
  - get: creator → 200; another user with only `projects:access` → **404**; with `projects:view-all` → 200, `canManage: false`, **no `financials` key in the raw JSON** (assert on a `map[string]any`, not a struct); with `view-all` + `view-financials` → `financials` present; with `manage-all` → `canManage` and `canSeeFinancials` true; unknown ID → 404.
  - `billingLinesAvailable` true with the fake catalog, false under `newHarnessWithoutProducts`.
  - timeline row `project-created` exists (assert via `h.Count`).
  - `TestConfig…`: `MODULES=projects` → problem `projects requires customers`.
- [ ] **Step 3: Verify failure.** `mise exec -- go test -count=1 ./internal/projects/... ./internal/config/... ./internal/db/...`
- [ ] **Step 4: Implement** module.go (five permissions, Category `"Projects"`, `Sensitive: true` for `view-financials` and `manage-all`, all `Delegable: true`; `Module()` sets Name/Permissions/Mount — `Projects` provider comes in Task 9), server.go, and the files above.
- [ ] **Step 5: Run** the three packages + `./internal/module/... ./internal/openapi/...` → PASS. From `apps/server`: `mise exec -- golangci-lint run` → `0 issues.` with no stderr. `mise exec -- go vet ./...`.
- [ ] **Step 6: Commit.** `feat(projects): module scaffold with create and get`

### Task 5: List, update, status and timeline

**Files:** Modify `openapi/projects.yaml`, `projects/projects.go`, `queries/projects.sql`, `queries/timeline.sql`, `responses.go`, `timeline.go`; create `status.go`, `projects_list_test.go`, `projects_update_test.go`, `status_test.go`, `timeline_test.go`, `projects_concurrency_test.go`.

**Operations:**
- `GET /api/v1/projects` — query `page`, `pageSize`, `search`, `status`, `customerId`, `internal` (bool), `mine` (bool) → `PaginatedResponseOfProjectSummaryResponse` (same envelope as customers' paginated response: copy its schema shape). `ProjectSummaryResponse` = `ProjectResponse` without `financials`, `capabilities`, `billingLinesAvailable`, `description`.
- `PUT /api/v1/projects/{id}` — `ProjectUpdateRequest` → 200 `ProjectResponse`; 400, 403, 404, 409 (stale revision).
- `PUT /api/v1/projects/{id}/status` — `{status}` → 200 `ProjectResponse`; 400, 403, 404.
- `GET /api/v1/projects/{id}/timeline` — `page`, `pageSize` → paginated `TimelineEntryResponse {id int64, eventType, payload object, actorUserId? uuid, actorDisplay, occurredAt}`, newest first.

**List SQL (visibility in SQL so counts are right):**

```sql
-- name: ListProjects :many
SELECT p.* FROM projects.projects p
WHERE (sqlc.arg(see_all)::boolean OR EXISTS (
         SELECT 1 FROM projects.project_roles r WHERE r.project_id = p.id AND r.user_id = sqlc.arg(user_id)))
  AND (NOT sqlc.arg(mine)::boolean OR EXISTS (
         SELECT 1 FROM projects.project_roles r WHERE r.project_id = p.id AND r.user_id = sqlc.arg(user_id)))
  AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status))
  AND (sqlc.narg(customer_id)::integer IS NULL OR p.customer_id = sqlc.narg(customer_id))
  AND (sqlc.narg(internal)::boolean IS NULL OR (p.customer_id IS NULL) = sqlc.narg(internal))
  AND (sqlc.arg(search)::text = '' OR p.code ILIKE '%' || sqlc.arg(search) || '%' ESCAPE '\'
                                   OR p.name ILIKE '%' || sqlc.arg(search) || '%' ESCAPE '\')
ORDER BY p.code, p.id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);
-- name: CountProjects :one   -- identical WHERE
```

Customer names: resolve each distinct `customer_id` through `s.deps.Directory.Customer` (memoise per request). Managers: one `ManagersForProjects(project_ids)` query + one `s.deps.Users.Users(ids)` call. Paging defaults and clamps: copy customers' list handler.

**Update:** authorize → 404 / 403; validate as create; `UPDATE … SET …, revision = revision + 1 WHERE id = $1 AND revision = $2`; zero rows → 409 problem (copy how customers reports a stale revision — search `revision` in `customers/timeline.go`). Diff old vs new and write, in the same transaction: `code-changed {old,new}` if the code changed; `customer-changed {oldCustomerId,newCustomerId}`; `billing-changed {fields:[…]}` for any of billingType/currency/fixedPriceAmount/budgetAmount (**names only, never values**); `details-changed {fields:[…]}` for name/description/dates/budgetHours. No entry when nothing changed. Moving a project to internal requires `non-billable` (same rule).

**Status:** validate enum; no-op when unchanged (200, no timeline entry); else update + `status-changed {old,new}`; bumps `revision`.

- [ ] **Step 1: Failing tests.**
  - list: a member sees only their projects and `totalCount` matches; `view-all` sees all; `mine=true` narrows a `view-all` caller to their own; `status`, `customerId`, `internal`, `search` (by code and by name, `%` literal) each filter; ordered by code; summaries carry `customerName` and `managers`; **no `financials` key on any item** even for a manager.
  - update: manager → 200 and `revision` incremented; member → 403; outsider → 404; stale revision → 409; renaming the code writes `code-changed` with old and new; changing `fixedPriceAmount` writes `billing-changed` whose payload contains the field name and **not** the amount (assert the serialised payload does not contain the number); unchanged body → no new timeline rows.
  - status: each of the five values accepted from any other; invalid → 400; reopening `completed → active` allowed; same status → no entry.
  - timeline: newest first, paged, visible to a viewer, 404 to an outsider.
  - concurrency (`*_concurrency_test.go`, mirror customers'): two simultaneous creates with the same code → exactly one 201 and one 400 on `code`; two simultaneous updates on the same revision → one 200, one 409.
- [ ] **Step 2: Verify failure.** **Step 3: Implement.** **Step 4: Run** `./internal/projects/...` (and once pinned: `taskset -c 0-3 mise exec -- go test -count=1 ./internal/projects/...`; if `taskset` is refused use `GOMAXPROCS=4`). lint + vet.
- [ ] **Step 5: Commit.** `feat(projects): list, update, status changes and the project timeline`

### Task 6: Code suggestion

**Files:** Create `projects/suggestion.go`, `suggestion_internal_test.go` (pure functions), `suggestion_test.go` (endpoint); modify `projects.yaml`, `queries/projects.sql`, `queries/counters.sql`.

**Operation:** `GET /api/v1/projects/code-suggestion?customerId=&name=` (`permission:projects:access`) → `{code}`; 400 when `customerId` does not resolve.

**Pure functions (complete):**

```go
// letterReplacer spells out the Norwegian letters before diacritics are stripped.
var letterReplacer = strings.NewReplacer("Æ", "AE", "Ø", "O", "Å", "A", "æ", "AE", "ø", "O", "å", "A")

// words splits name into upper-case A–Z words: Norwegian letters spelled out,
// other diacritics stripped (NFD, drop marks), anything else a separator.
func words(name string) []string {
	decomposed := norm.NFD.String(letterReplacer.Replace(name))
	var out []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	for _, r := range decomposed {
		switch {
		case unicode.Is(unicode.Mn, r):
			// a combining mark left by NFD: drop it, stay in the word
		case r >= 'a' && r <= 'z':
			current.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z':
			current.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// lettersFor is the mnemonic for a name: the first letter of each of its
// first three words, or the first two letters of a one-word name.
func lettersFor(name string) string {
	w := words(name)
	switch len(w) {
	case 0:
		return ""
	case 1:
		if len(w[0]) < 2 {
			return w[0]
		}
		return w[0][:2]
	}
	if len(w) > 3 {
		w = w[:3]
	}
	var b strings.Builder
	for _, word := range w {
		b.WriteByte(word[0])
	}
	return b.String()
}

// leadingLetters is the run of letters a code starts with ("KVEM1000" → "KVEM").
func leadingLetters(code string) string {
	i := 0
	for i < len(code) && code[i] >= 'A' && code[i] <= 'Z' {
		i++
	}
	return code[:i]
}

// customerLetters keeps a hand-chosen customer prefix sticky: the derived
// letters, unless the customer's existing codes agree on a different prefix.
func customerLetters(derived string, existingCodes []string) string {
	if len(existingCodes) == 0 || derived == "" {
		return derived
	}
	runs := make([]string, 0, len(existingCodes))
	for _, code := range existingCodes {
		run := leadingLetters(code)
		if strings.HasPrefix(run, derived) {
			return derived
		}
		runs = append(runs, run)
	}
	common := runs[0]
	for _, run := range runs[1:] {
		for !strings.HasPrefix(run, common) {
			common = common[:len(common)-1]
		}
	}
	if len(common) < 2 {
		return derived
	}
	if len(common) > len(derived) {
		common = common[:len(derived)]
	}
	return common
}
```

(`golang.org/x/text/unicode/norm` — check `go.mod`; it is a transitive dependency of most Go web stacks. If absent, `mise exec -- go get golang.org/x/text` and commit `go.mod`/`go.sum`.)

**Handler:** prefix = `INT` for internal, else `customerLetters(lettersFor(customer.Name), <that customer's 20 most recent project codes>)`; + `lettersFor(name)`; number = current `project_code` counter value (**read, never advanced**; 1000 when the row is missing); candidate = prefix+letters+number truncated so the whole fits 20 chars (trim letters, never the number); while the candidate exists, number++ (max 50 tries).

- [ ] **Step 1: Failing tests.** Table tests for the pure functions: `"Kraft-Verket" → KV`, `"Energy migration" → EM`, `"Website" → WE`, `"Ærlig Østlig Åpen" → AOA`, `"Émile Zola" → EZ`, `"A" → A`, `"" → ""`, `"One Two Three Four" → OTT`, `"123 Go" → GO`; `customerLetters("KV", nil) → KV`, `("KV", ["KVEM1000"]) → KV`, `("KV", ["KRVEM1000","KRVSO1001"]) → KR` (common `KRV` cut to the derived length 2), `("KV", ["AB1000","CD1001"]) → KV`. Endpoint: first suggestion for customer 1001 + "Energy migration" → `KVEM1000`; after creating it, the same request → `KVEM1001`; internal + "Competence" → `INTCO…`; calling the endpoint ten times does not advance the counter (assert counter row unchanged); a taken candidate is skipped; unknown customer → 400.
- [ ] **Step 2–4:** verify failure, implement, run.
- [ ] **Step 5: Commit.** `feat(projects): suggest a project code from the customer and project name`

### Task 7: People — roles and assignable users

**Files:** Create `projects/people.go`, `people_test.go`; modify `projects.yaml`, `queries/roles.sql`.

**Operations:**
- `GET /projects/{id}/roles` → `[ProjectRoleResponse {userId, displayName, active bool, role, createdAt}]`, managers first then by display name. Anyone who sees the project.
- `PUT /projects/{id}/roles/{userId}` body `{role}` → 200 `ProjectRoleResponse` (adds or changes). Manager.
- `DELETE /projects/{id}/roles/{userId}` → 204. Manager. 404 when no such assignment.
- `GET /projects/{id}/assignable-users?search=` → `[{userId, displayName}]`, max 20, excluding users already assigned. Manager.

**Rules:** role must be `validRole`; on *add* the user must resolve via `s.deps.Users.User` and be `Active` (else 400 on `userId`); changing or removing a disabled user's assignment is allowed. A user resolved as nil on list renders `displayName: "Unknown user", active: false`. Timeline: `role-added {userId, displayName, role}`, `role-changed {userId, displayName, oldRole, newRole}`, `role-removed {userId, displayName, role}`; unchanged role → no entry. Managers may remove themselves (D6: no last-manager rule).

- [ ] **Step 1: Failing tests** (use `h.SignInUser` to get real user IDs): add member → visible in list and that user can now GET the project (was 404); change to viewer; remove → 404 again for that user; invalid role → 400; unknown user ID → 400; disabled user cannot be added but an existing assignment of a user disabled afterwards (`h.Exec` flips `is_disabled`) lists with `active: false` and can be removed; member calling PUT → 403; outsider → 404; assignable-users excludes assigned and disabled users, matches by name, member → 403; each mutation writes its timeline entry; the last manager can remove themself and `manage-all` can then add a new one.
- [ ] **Step 2–4.** **Step 5: Commit.** `feat(projects): per-project roles and an assignable-user search`

### Task 8: Stats

**Files:** Create `projects/stats.go`, `stats_test.go`, `queries/stats.sql`; modify `projects.yaml`.

**Operations** — open `openapi/customers.yaml` and `customers/stats.go` and copy the **exact** request parameters and response schemas of `/customers/stats/summary`, `/stats/timeseries` and `/stats/attention` (the dashboard consumes every module through one generic shape — `apps/host/frontend/src/routes/dashboard.tsx` lines 187–205 build the URLs): same param names, same `DailyPoint` and `AttentionItem` shapes.
- `GET /projects/stats` → `{planned, active, onHold, completed, cancelled int32}`.
- `GET /projects/stats/summary` → `{newProjects, activeProjects}` in the customers summary's envelope.
- `GET /projects/stats/timeseries?metric=newProjects` → daily points; unknown metric → 400 as customers does.
- `GET /projects/stats/attention` → active projects whose `end_date` is before today (harness clock), linking to `/projects/{id}`.

All four apply the same visibility predicate as the list (extract the `EXISTS` predicate's arguments into a shared helper so list and stats cannot drift).

- [ ] **Step 1: Failing tests:** counts per status; a member's counts cover only their projects while `view-all` covers all; summary/timeseries over a range using `h.Advance`; attention lists an overdue active project and not an overdue completed one; unknown metric → 400.
- [ ] **Step 2–4.** **Step 5: Commit.** `feat(projects): status counts and dashboard statistics`

### Task 9: `ProjectDirectory`

**Files:** Create `projects/directory.go`, `directory_test.go` (or `_internal_test.go` — follow how customers tests its directory); modify `projects/module.go` (`Projects: newDirectory`), `queries/directory.sql`.

- [ ] **Step 1: Failing tests:** `Project` returns code, name, customer, status, `OpenForWork` true only for `active`, billing type; cancelled and completed projects still resolve; unknown → `(nil, nil)`; internal → `CustomerID == nil`. `Role` → the role, `""` for none. `ProjectsForUser` → every project the user holds a role in, ordered by code, regardless of status. (`BillingLine` is added in Task 10; here implement it returning rows from `billing_lines` and test it with rows inserted via `h.Exec`: active and inactive lines resolve; a line of another project → `(nil, nil)`.) Also assert `h.Deps()`-composed `Deps.Projects` is non-nil via a Compose-level check: `TestModule_DeclaresProjectDirectory` asserting `projects.Module().Projects != nil`.
- [ ] **Step 2–4.** **Step 5: Commit.** `feat(projects): publish the project directory future modules build on`

---

## Slice 3 — Billing lines

### Task 10: Billing lines, with Products on and off

**Files:** Create `projects/lines.go`, `lines_test.go`, `queries/lines.sql`; modify `projects.yaml`, `harness_test.go` (fake catalog), `responses.go`.

**Operations:**
- `GET /projects/{id}/billing-lines` → `[BillingLineResponse]`. Anyone who sees the project.
- `POST /projects/{id}/billing-lines` → 201. Manager.
- `PUT /projects/{id}/billing-lines/{lineId}` → 200. Manager.
- All three answer **409** with problem title `Products module not enabled` when `s.deps.Products == nil`, *after* the 404/403 checks.

```
BillingLineResponse {
  id int32, code, trackableCode ("KVEM1000-PM"), variantId int32,
  productName?, sku?, unit?, variantMissing bool,     // from ProductCatalog.Variants; missing variant → variantMissing: true
  active bool, createdAt, updatedAt,
  pricing?: { mode, fixedAmount? number, discountPercent? number,
              listPrice?: {amount number, currency} }  // ABSENT unless canSeeFinancials
}
BillingLineRequest { code, variantId, pricingMode, fixedAmount?, discountPercent?, active? (PUT only, default unchanged) }
```

**Rules:** code trim/upper/`^[A-Z0-9]{1,10}$`, unique per project (`23505` on the unique index → field error `code`); `variantId` must resolve (400 on `variantId`); `pricingMode` enum; `fixed` ⇒ `fixedAmount > 0` required and `discountPercent` absent, and the project must have a `currency` (else 400 on `pricingMode`: "set a project currency first"); `discount` ⇒ `0 < discountPercent <= 100` and `fixedAmount` absent; `list` ⇒ both absent. `listPrice` = `ProductCatalog.ListPrice(variantID, project.currency, now)` when the project has a currency, else absent. Also enforce D13 in Task 5's update: clearing `currency` while a `fixed` line exists → 400 on `currency` (add that check and its test here). Timeline: `line-added {code, fields}`, `line-changed {code, fields}`, `line-deactivated {code}`, `line-reactivated {code}` — never amounts. There is no DELETE.

**Fake catalog (`harness_test.go`):** variants `2001` ("Project manager hour", `PM-H`, `hour`, `Service`) with list price 1600 NOK, `2002` ("Developer hour", `DEV-H`, `hour`, `Service`) with 1250 NOK; everything else nil; `ListPrice` returns nil for other currencies.

- [ ] **Step 1: Failing tests:** add `PM` on `KVEM1000` → `trackableCode == "KVEM1000-PM"`, product name/SKU/unit embedded; renaming the project updates `trackableCode`; each validation rule (table-driven); duplicate line code in the project → 400, same code in another project → 201; deactivate then reactivate via PUT `active`, each with its timeline entry; member sees lines with **no `pricing` key**; manager and `view-financials` see `pricing` with `listPrice {1600, "NOK"}` when the project currency is NOK and no `listPrice` when it is EUR or unset; a line whose variant the catalog no longer knows → `variantMissing: true`, still listed; member POST → 403, outsider → 404; **without products** (`newHarnessWithoutProducts`): all three operations → 409, and rows inserted directly with `h.Exec` stay in the table (assert count) while GET project shows `billingLinesAvailable: false`; clearing the currency with a fixed line → 400. `ProjectDirectory.BillingLine` returns a line created through the API.
- [ ] **Step 2–4** (run the whole package pinned to four CPUs once; lint; vet).
- [ ] **Step 5: Commit.** `feat(projects): billing lines pinned to product variants, optional on products`

---

## Slice 4 — Frontend

### Task 11: `@vantigo/projects-ui` package and API layer

**Files:** Create `apps/projects/frontend/` by copying the *configuration* of `apps/customers/frontend/` (package.json with name `@vantigo/projects-ui` and the same `exports` map, tsconfig*, vite/vitest config, eslint.config.js, `src/test/{setup.ts,fetch.ts,route-tree.tsx}`, `src/api/request.ts`, README). Create `src/api/{projects,people,lines,customers,products}.ts` (+ `.test.ts`), `src/i18n.ts`, `src/index.ts`, `src/lib/{status,billing}.ts`. Modify `tools/openapi/gen-client.ts` (+ its test's expected targets), `apps/host/frontend/package.json` (workspace dependency), every other module package's `eslint.config.js` `forbiddenModuleImports` (add `@vantigo/projects-ui`; the new package forbids all five others — note customers' list currently omits `@vantigo/energy-ui`; add it while there), root lockfile via `mise exec -- bun install`.

**Interfaces — Produces** (`src/api/projects.ts`; types come from the generated `api-schema.d.ts`, re-exported under these names):

```ts
export type Project, ProjectSummary, ProjectInput, ProjectStatus, BillingType, ProjectRole, BillingLine, BillingLineInput, TimelineEntry, ProjectStatusCounts;
export interface ProjectListParams { page: number; search: string; status: ProjectStatus | ""; customerId?: number; internal?: boolean; mine: boolean }
export const projectsQueryOptions = (params: ProjectListParams) => queryOptions({ queryKey: ["projects", "list", params], … });
export const projectQueryOptions = (id: number) => queryOptions({ queryKey: ["projects", "detail", id], … });
export const projectStatsQueryOptions, projectTimelineQueryOptions(id, page), codeSuggestionQueryOptions({customerId, name});
export const createProject(input), updateProject(id, input & {revision}), setProjectStatus(id, status);
// people.ts
export const projectRolesQueryOptions(id), assignableUsersQueryOptions(id, search), setProjectRole(id, userId, role), removeProjectRole(id, userId);
// lines.ts
export const billingLinesQueryOptions(id), createBillingLine(id, input), updateBillingLine(id, lineId, input);
// customers.ts / products.ts — cross-module reads over HTTP with LOCALLY declared types
export const customerSearchQueryOptions(search)      // GET /api/v1/customers?search=  → {id, name}[]
export const customerQueryOptions(id)                // GET /api/v1/customers/{id}
export const serviceVariantSearchQueryOptions(search) // GET /api/v1/products?search=&… → flattened {variantId, productName, sku, unit}[]; filter type === "Service"
```

Mirror `apps/energy/frontend/src/api/customers.ts` for the cross-module files (that is the sanctioned pattern). `lib/status.ts`: status → i18n key + Mantine badge colour (`planned` gray, `active` green, `on-hold` yellow, `completed` blue, `cancelled` red). i18n catalog `projects` with `en` and `nb` for every string used in Tasks 12–13.

- [ ] **Step 1:** scaffold, `bun install`, `mise exec -- bun run gen:client` (commit `api-schema.d.ts`).
- [ ] **Step 2: Failing tests** for the api layer using the copied `test/fetch.ts` helper, mirroring `apps/customers/frontend/src/api/customers.test.ts`: list builds the right query string (omits empty filters, sends `mine=true`), 404 → `NotFoundError`, 400 → `ApiValidationError` with `fieldErrors.code`, products search keeps only `Service` items.
- [ ] **Step 3–4:** implement; `mise exec -- bun run --cwd apps/projects/frontend test`, `… typecheck`, `… lint`; `mise exec -- bun run translations:check`.
- [ ] **Step 5: Commit.** `feat(projects-ui): package scaffold and api layer`

### Task 12: List page and project form

**Files:** Create `src/pages/projects.index.tsx` (+test), `src/pages/-project-form-modal.tsx` (+test), `src/components/{project-status-badge,customer-picker}.tsx`, `src/lib/use-code-suggestion.ts` (+test).

Mirror `apps/customers/frontend/src/pages/customers.index.tsx` and `-customer-form-modal.tsx` for structure, loading/empty/error states, URL-driven search and the `create` search param that opens the modal.

**List:** `PageHeader` (title, description, create button only with `projects:create` — read permissions the way the customers page does); KPI row of three `KpiCard`s (active, planned, on hold) from `projectStatsQueryOptions`; toolbar: debounced search (`useDebouncedListSearch`), status `Select`, customer picker, "My projects" `Switch` — placed like the inbox toolbar filters (`apps/communications/frontend/src/pages/` — find the toolbar from commit `2870692`); table columns code (monospace, links to detail), name, customer or "Internal" badge, status badge, first manager (+N), start–end; `Pagination`. Props: `{ search: ProjectListParams & {create?: boolean}, onSearchChange, permissions }` matching how the customers page receives them from its host route.

**Form modal:** `state: {mode:"create", customerId?: number} | {mode:"edit", project}`; `@mantine/form`. Fields: customer picker with a first option "Internal project"; name; code (uppercased on input); dates; billing type `SegmentedControl` (disabled and forced to `non-billable` when internal); `fixedPriceAmount` only when `fixed-price`; `budgetHours`; `budgetAmount`; `currency` (shown when any amount field is shown; default `NOK`) — amount fields rendered only when creating or when `project.capabilities.canSeeFinancials`. On submit: `createProject`/`updateProject` via `useMutation`, invalidate `["projects"]`, notify, `form.setErrors(error.fieldErrors)` on `ApiValidationError`, and on 409 show "changed by someone else — reload" (copy the customers wording pattern). **Edit mode, code changed:** open a `modals.openConfirmModal` first (mirror the customer-type change confirmation from commit `deb2256`).

**`useCodeSuggestion(customerId, internal, name, enabled)`:** debounced (300 ms) query; returns `{suggestion, isFollowing, setManual(value), useSuggestion()}`. While `isFollowing`, the form's `code` is set to each new suggestion; the first manual edit sets `isFollowing = false`; `useSuggestion()` restores following. In edit mode it starts **not** following.

- [ ] **Step 1: Failing tests.** Hook: follows → stops after `setManual` → resumes after `useSuggestion`; edit mode never overwrites. Modal: suggestion appears in the code field as customer and name are filled; typing a code keeps it when the name changes again; "Internal project" forces and disables billing type; fixed price field appears only for `fixed-price`; server `fieldErrors.code` shows under the code input; editing the code prompts for confirmation and cancelling does not submit. List: renders rows, "Internal" badge, empty state, error alert, create button hidden without `projects:create`, toggling "My projects" calls `onSearchChange({mine:true, page:1})`. Test provider must be `<MantineProvider env="test">`.
- [ ] **Step 2–4:** implement; test, typecheck, lint, translations check.
- [ ] **Step 5: Commit.** `feat(projects-ui): project list and the create/edit form with code suggestions`

### Task 13: Detail — overview, people, billing, customer panel

**Files:** Create `src/pages/projects.$projectId.tsx` (exports `ProjectDetailHeader`, `ProjectOverview`), `src/pages/-project-timeline.tsx`, `src/pages/project-people.tsx` (`ProjectPeople`), `src/pages/project-billing.tsx` (`ProjectBilling`), `src/pages/-billing-line-form-modal.tsx`, `src/components/customer-projects-panel.tsx` (`CustomerProjectsPanel`), tests for each; extend `src/index.ts` exports.

Mirror `apps/customers/frontend/src/pages/customers.$customerId.tsx` (header + overview split, host owns tabs) and `-customer-timeline.tsx` (timeline rendering; ours is read-only: one line per event from an i18n template per `eventType`, actor and relative time).

- **Header:** code (monospace) · name, status badge, customer link (`/customers/$id`) or "Internal"; actions for `canManage`: Edit (opens the form modal) and a status `Menu` with the five statuses (current disabled) calling `setProjectStatus`.
- **Overview:** details card (description, dates, billing type, budget hours, managers), timeline card.
- **People:** table (name, role badge, inactive marker); with `canManage`: "Add person" (`Select` searchable via `assignableUsersQueryOptions`, role select, submit), role change `Select` per row, remove with a confirm modal.
- **Billing:** rendered only with `canSeeFinancials` (otherwise a forbidden-style `EmptyState`); financial summary (billing type, fixed price, budget amount, currency); when `project.billingLinesAvailable`: lines table — trackable code (monospace), product, unit, pricing (`List price` / `Fixed 1 450,00 NOK` / `10 % off list`), current list price, active badge; with `canManage`: add/edit modal (line code, variant picker via `serviceVariantSearchQueryOptions` — on a 403 from products show the hint "You need access to Products to pick a product", D15), pricing mode `SegmentedControl` with its conditional field, active `Switch` in edit mode. When lines are unavailable: a dimmed note "Billing lines need the Products module".
- **CustomerProjectsPanel({customerId, canCreate}):** compact table of that customer's projects via `projectsQueryOptions({customerId, …})`, row links to `/projects/$id`, create button opening the form modal with `customerId` pre-filled, empty state.

- [ ] **Step 1: Failing tests:** header hides Edit/status for `canManage: false`; status menu calls the mutation; People hides actions for members, adds a person, confirms removal; Billing renders the forbidden state without `canSeeFinancials`, hides the lines section when `billingLinesAvailable` is false, shows the products-permission hint on 403, formats the three pricing modes; timeline renders `code-changed` with old and new codes; customer panel lists, links, and pre-fills the customer on create.
- [ ] **Step 2–4.** **Step 5: Commit.** `feat(projects-ui): project detail with people, billing lines and the customer panel`

### Task 14: Host — the Projects app and everything the switcher entails

**Files (all under `apps/host/frontend/src/` unless noted):**
- Modify `navigation.ts`: `moduleKeys` += `"projects"` (keep alphabetical: `communications, customers, energy, products, projects`); `searchStrategy` union += `"projects-list"`; `navSearchFor` case returning `{ page: 1, search: "", status: "", mine: false }`.
- Modify `apps.ts`: after the customers entry,
  ```ts
  moduleApp("projects", "navigation.projects", IconBriefcase, "/projects", [
    { label: "navigation.projects", to: "/projects", icon: IconBriefcase,
      requiredPermissions: ["projects:access"], searchStrategy: "projects-list" },
  ]),
  ```
- Create `routes/projects.tsx`: `export const Route = createFileRoute("/projects")(appLayoutOptions("projects"));`
- Create `routes/projects/index.tsx`, `routes/projects/$projectId.tsx` (layout: loader `ensureQueryData(projectQueryOptions(id))`, `NotFoundError → notFound()`), `routes/projects/$projectId.index.tsx`, `$projectId.people.tsx`, `$projectId.billing.tsx`, `routes/projects/-project-detail-layout.tsx` with `projectDetailTabs` — mirror `routes/customers/index.tsx`, `$customerId.tsx`, `$customerId.index.tsx` and `-customer-detail-layout.tsx` (tabs gated with `passesGate`; the Billing tab additionally hidden unless `project.capabilities.canSeeFinancials`). Deep-linking `/billing` without the capability renders the package's forbidden state, not a crash.
- Create `routes/customers/$customerId.projects.tsx` mirroring `$customerId.energy.tsx` (inline `ModuleNotEnabledPage` when projects is off); add the tab to `customerDetailTabs` gated `{ module: "projects", requiredPermissions: ["projects:access"] }`.
- Modify `components/module-access-guard.tsx`: `/projects` prefix rule requiring `projects:access` (follow how existing prefixes are declared; they may be derived from `allNavSections` — if so only a test is needed).
- Modify `components/app-spotlight.tsx`: quick action "Create project" (`/projects`, `search: { create: true }`, requires `projects:create`, module `projects`); a Projects result group calling the list endpoint with `search=` showing `code — name`, linking to the detail.
- Modify `routes/dashboard.tsx`: module card (`module: "projects"`, `requiredPermissions: ["projects:access"]`, icon, link `/projects`, its summary numbers) and metric `{ module: "projects", metric: "newProjects", color: "grape.6", label: "dashboard.newProjects" }`.
- Modify `i18n.ts` (import `@vantigo/projects-ui/i18n`), `catalogs/*.ts`: `navigation.projects`, dashboard labels, spotlight strings, customer tab label, and the five permission labels + category in `catalogs/admin.ts` — en and nb.
- Tests: extend `apps.test.ts`/navigation tests (tile present with `projects:access`, absent without, disabled when the module is off), `routes/customers/customer-detail-tabs.test.ts` (Projects tab gate), `module-access-guard.test.tsx`, `app-spotlight.test.tsx`, a new `routes/projects/project-detail-tabs.test.ts`, and `cutover-sweep.test.ts` / any test enumerating module keys (`grep -rn '"energy"' apps/host/frontend/src --include=*.test.*`).

- [ ] **Step 1: Failing tests** as listed. **Step 2:** verify failure.
- [ ] **Step 3: Implement.** Then regenerate the route tree: `mise exec -- bun run --cwd apps/host/frontend build` (run it twice if `tsc` fails first on a stale tree — the Vite plugin regenerates during `vite build`); commit `routeTree.gen.ts`.
- [ ] **Step 4: Run** `mise exec -- bun run frontend:lint`, `frontend:typecheck`, `frontend:test`, `translations:check`, `mise exec -- bunx biome check .`.
- [ ] **Step 5: Commit.** `feat(frontend): projects app in the switcher, customer projects tab, spotlight and dashboard`

### Task 15: Documentation

**Files:** Create `docs/projects.md`; modify `docs/module-boundaries.md`, `docs/README.md` (index), `CONTRIBUTING.md` (URL map, module lists), `README.md` (module list), `ROADMAP.md`, `deploy/` env examples if they enumerate `MODULES` values (`grep -rn "communications" deploy/ README.md CONTRIBUTING.md docs/*.md`).

- `docs/projects.md`: model, codes (D1–D3), roles and the permission table, financial shaping, billing lines and the optional Products dependency, the three contracts, "what Time tracking should build on" — match the tone and depth of `docs/products.md`.
- `docs/module-boundaries.md`: rule 4's schema list; rule 5 (a module may also provide a user directory, product catalog or project directory); "Turning a module off" (known set, `projects requires customers`, optional providers leave `Deps` nil and the consumer must handle it); "Adding a module" additions: `openapi.Modules`, `generate.go`, `cfg-<name>.yaml`, `tools/openapi/gen-client.ts`, host i18n import, permission labels.
- `ROADMAP.md` Projects section: Phase 1 (done) as built; Phase 2 Time tracking (hours on project + line codes, reads `ProjectDirectory`); Later: milestones/tasks as a separate code dimension, rates per person, configurable project roles, project documents, domain events once the bus exists. Each with its *Unblocks* line like the other sections.

- [ ] **Step 1:** write; `mise exec -- bunx biome check .`. **Step 2: Commit.** `docs(projects): module guide, boundaries and roadmap`

### Task 16: Final gates and PR

- [ ] `cd apps/server && mise exec -- go generate ./... ; git status --porcelain` → no drift. `mise exec -- bun run gen:client ; git status --porcelain` → no drift.
- [ ] `cd apps/server && mise exec -- go vet ./... && mise exec -- golangci-lint run` (0 issues, no stderr).
- [ ] `taskset -c 0-3 mise exec -- go test -count=1 -timeout 40m ./...` (or `GOMAXPROCS=4`). If a C toolchain is available via zig (`mise exec zig@0.15 -- zig cc`), run `./internal/projects/...` once with `-race`.
- [ ] `mise exec -- bun run frontend:check` (or lint + typecheck + test + build individually).
- [ ] `mise run smoke` (needs the image build; if Docker is unavailable say so in the PR rather than skipping silently).
- [ ] Push, open the PR against `main` with `gh pr create`; body: summary per slice, the decisions made during implementation, test evidence; ends with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`. If the body needs editing afterwards use `gh api -X PATCH repos/vantigo-io/vantigo/pulls/<n> -F body=@<file>` (`gh pr edit` is broken here).
- [ ] Watch checks with the Monitor tool parsing `gh pr checks <n>` default output (no `--json`). On red: diagnose the root cause first, fix on the same branch, push, repeat until green. Never merge.
