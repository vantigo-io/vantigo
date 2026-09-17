# Projects module — design

## 1. Scope

A new business module, `projects`, that sets up projects for customers. A
project is the thing later modules attach work to: **Time tracking** first,
then tasks / work orders, invoicing, materials (Orders / Warehouse) and
documents. None of those exist yet; this module is their groundwork, scoped
to projects only.

Goals, in priority order:

1. Every project has a stable identity other modules can key on, and a
   human-facing **code** (`KVEM1000`) employees will later track time on.
2. A user's **role in a project** decides what they may see and do there.
3. A project carries the **commercial rules** invoicing will later read:
   billing type, fixed price, budget, and billing lines pinned to products.
4. Projects is a full **app** in the app switcher, with a Projects tab on the
   customer page.
5. Without Products enabled, Projects still works as a planning tool: codes,
   customers, status, dates, budgets, people — no priced lines.

Explicitly **not** in scope:

- Time entries, timesheets, approval, invoicing, or any money calculation.
- Milestones, tasks, work orders (`KVEM1000-MIL1`). The code format leaves
  room for them; nothing else is built.
- Rates per person or per project role.
- Configurable or per-project role definitions. Roles are a fixed set.
- A project shared between several customers.
- Asynchronous events. The roadmap defers the event bus until Orders; later
  modules read through `contracts.ProjectDirectory`.
- Manual notes on the project timeline. It records generated events only.

## 2. Decisions

**D1 — The ID is the identity; the code is a label.** A project's integer ID
never changes and is what every other module stores. The code is free text,
editable at any time, unique, and looked up for display. A rename therefore
cannot orphan anything. Code changes are recorded on the project timeline so
an old code on a printed timesheet can be traced.

**D2 — Codes are uppercase letters and digits, never a hyphen.** A project
code matches `^[A-Z0-9]{2,20}$`; input is upper-cased before validation and
storage, which also makes uniqueness case-insensitive with a plain unique
index. A billing line code matches `^[A-Z0-9]{1,10}$` and is unique within
its project. The trackable code is `<project>-<line>` (`KVEM1000-PM`); because
neither part can contain a hyphen it always splits cleanly. Milestones and
tasks are a *separate* dimension from billing lines (what the work is *for*
versus what *kind* of work it is) and their format is not decided here.

**D3 — The code is suggested, never imposed.** The server suggests customer
letters + project letters + a running number (§4.2). The user may type
anything valid instead. A taken code is an ordinary validation error on the
`code` field.

**D4 — A project has at most one customer; none means internal.** An
internal project must be `non-billable` (there is nobody to invoice). A
customer project may be any billing type, including `non-billable`.

**D5 — Projects are cancelled, never deleted.** Later modules hold project
IDs and projects cannot know what refers to it. Billing lines are likewise
deactivated, never deleted.

**D6 — Fixed project roles, above them global permissions.** Roles are
`manager`, `member`, `viewer`, one per user per project, defined in code as
named sets of capabilities so a role editor could be added later without
changing the data model. Global permissions sit above them (§5). The creator
of a project becomes its manager. "At least one manager" is not enforced:
`projects:manage-all` can always step in, and enforcing it gets awkward when
an account is disabled.

**D7 — Outsiders get "not found", not "forbidden".** Reading a project you
hold no role in (and have no `view-all`/`manage-all`) answers 404, so project
codes and existence do not leak. Acting on a project you *can* see but may
not manage answers 403.

**D8 — A baseline `projects:access` permission gates the app.** Project
roles are per project, but the switcher tile, sidebar and permission guard
work on global permissions. Every operation in the contract requires
`projects:access`; it means "may use the Projects app and see the projects
they hold a role in". Without it a project member would never see the tile.

**D9 — Billing lines are pinned to product variants, and exist only when
Products is enabled.** A line is "variant + pricing rule" — `list` (the
variant's list price), `fixed` (a negotiated amount in the project currency)
or `discount` (a percentage off list price). "Project manager hour" is a
`Service` product, not a role: access and billing stay separate, one person
can log two kinds of work on one project, and an invoice line later gets its
product, unit and tax category for free. There is one pricing path; no
hand-typed rate. **Projects never calculates money** — it stores the rule and
Invoices resolves amounts when it bills, which avoids a second pricing engine
next to Products' planned price lists (ROADMAP, Products phase 4).

**D10 — Products is an optional dependency, Customers a required one.**
`contracts.ProductCatalog` is present on `Deps` when products is enabled and
nil otherwise — the first optional contract in the codebase. With it absent,
billing line operations answer 409 with a "products module not enabled"
problem, project responses carry no lines, and the frontend hides the lines
section. Lines stored before Products was switched off stay stored and
hidden. `MODULES` with `projects` but without `customers` fails startup
("projects requires customers"), like energy and communications.

**D11 — Users are resolved through a new `contracts.UserDirectory`.** The
only user listing today requires the authorization-management policy, so a
project manager who is not an administrator could not pick a colleague.
Identity implements a read-only directory returning ID, display name and
active flag — no email, roles or MFA state. Projects exposes its own
"assignable users" search, callable by a project's managers and
`manage-all`. Time tracking and tasks will reuse the contract.

**D12 — Financial fields are shaped out of the response, not forbidden.**
Fixed price, budget amount, currency and line pricing are visible to the
project's managers and to `projects:view-financials` / `projects:manage-all`.
Everyone else gets the same resource with those fields **absent** (absent ≠
`null`, the convention communications already uses). Budget *hours* stay
visible to everyone who sees the project; they are planning data. Timeline
entries for billing changes record *which* fields changed, never amounts, so
the timeline needs no shaping.

**D13 — One currency per project.** `currency` is required as soon as any
amount is set (fixed price, budget amount, or a `fixed` line) and cannot be
cleared while one is. List and discount lines resolve in that currency.

**D14 — Status transitions are unrestricted and separately recorded.**
`planned`, `active`, `on-hold`, `completed`, `cancelled`; any transition is
allowed, including reopening. Only `active` means "open for work" — the one
question Time tracking will ask. Changing status is its own operation so it
gets its own timeline entry and, later, its own rules.

**D15 — Variant picking uses the Products API from the frontend.** This is
the existing cross-module frontend rule (energy calls the customers API the
same way). A manager who lacks a products view permission sees the lines but
gets a hint instead of a picker. Line *responses* embed the variant's product
name, SKU and unit through `ProductCatalog`, so members never need a products
permission to read "Project manager hour".

## 3. Data model

Schema `projects`, migration `00008_projects_baseline.sql`. No foreign key
leaves the schema; `customer_id`, `variant_id` and user IDs are opaque.

```
projects.projects(
  id                  integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
  code                varchar(20)   NOT NULL,            -- unique index ux_projects_code
  name                varchar(200)  NOT NULL,
  description         varchar(4000),
  customer_id         integer,                           -- NULL = internal
  status              varchar(20)   NOT NULL DEFAULT 'planned',
  start_date          date,
  end_date            date,                              -- >= start_date when both set
  billing_type        varchar(20)   NOT NULL,            -- time-and-materials | fixed-price | non-billable
  currency            char(3),
  fixed_price_amount  numeric(12,2),
  budget_hours        numeric(10,2),
  budget_amount       numeric(12,2),
  revision            integer       NOT NULL DEFAULT 1,
  created_by_user_id  uuid          NOT NULL,
  created_at          timestamptz   NOT NULL,
  updated_at          timestamptz   NOT NULL)
  -- indexes: customer_id, status

projects.project_roles(
  project_id integer NOT NULL REFERENCES projects.projects(id),
  user_id    uuid    NOT NULL,
  role       varchar(20) NOT NULL,                       -- manager | member | viewer
  created_at timestamptz NOT NULL,
  PRIMARY KEY (project_id, user_id))
  -- index: user_id

projects.billing_lines(
  id               integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
  project_id       integer NOT NULL REFERENCES projects.projects(id),
  code             varchar(10) NOT NULL,                 -- unique (project_id, code)
  variant_id       integer NOT NULL,
  pricing_mode     varchar(20) NOT NULL,                 -- list | fixed | discount
  fixed_amount     numeric(12,2),                        -- set iff mode = fixed
  discount_percent numeric(5,2),                         -- set iff mode = discount, 0 < x <= 100
  active           boolean NOT NULL DEFAULT true,
  created_at       timestamptz NOT NULL,
  updated_at       timestamptz NOT NULL)

projects.timeline_entries(
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  project_id    integer NOT NULL REFERENCES projects.projects(id),
  event_type    varchar(50) NOT NULL,
  payload       jsonb NOT NULL,
  actor_user_id uuid,
  actor_display text NOT NULL,
  occurred_at   timestamptz NOT NULL)
  -- index: (project_id, occurred_at DESC, id DESC)

projects.counters(counter_name text PRIMARY KEY, next_value bigint NOT NULL)
```

Enumerations are validated in Go and carry no CHECK constraint, as in
customers. Edits to a project carry the `revision` they were based on; a
stale revision answers 409. Every state change writes its timeline entry in
the same transaction.

Timeline event types: `project-created`, `code-changed` (old, new),
`status-changed` (old, new), `details-changed` (field names),
`customer-changed` (old, new IDs), `billing-changed` (field names only),
`role-added` / `role-changed` / `role-removed` (user ID, display name, roles),
`line-added` / `line-changed` / `line-deactivated` / `line-reactivated`
(line code, field names).

## 4. Behaviour

### 4.1 Validation

Field errors are keyed by camelCase JSON path, as everywhere else.

- `code`: required, upper-cased, `^[A-Z0-9]{2,20}$`, unique → "already in use".
- `name`: required, ≤ 200. `description`: ≤ 4000.
- `customerId`: when set, must resolve through `CustomerDirectory`. Archived
  customers resolve and are allowed (a project can outlive the relationship).
- `billingType`: required; `non-billable` when `customerId` is absent.
- `fixedPriceAmount`: required and > 0 when `fixed-price`; must be absent
  otherwise.
- `budgetHours`, `budgetAmount`: optional, > 0.
- `currency`: ISO-4217 shape (`^[A-Z]{3}$`); required per D13.
- `endDate` ≥ `startDate`.
- Lines: `code` per D2 and unique in the project; `variantId` must resolve
  through `ProductCatalog`; `fixedAmount` / `discountPercent` per mode; a
  `fixed` line needs the project to have a currency.
- Roles: the user must resolve through `UserDirectory` and be active when
  *added*; an existing assignment of a since-disabled user stays readable and
  removable.

Writing financial fields needs no rule of its own: only managers and
`manage-all` can update a project, both see its financials, and whoever
creates a project becomes its manager.

### 4.2 Code suggestion

`GET /projects/code-suggestion?customerId=&name=` → `{ "code": "KVEM1000" }`.

- **Letters from a name:** split on anything that is not a letter; map
  `Æ→AE`, `Ø→O`, `Å→A`, strip other diacritics, drop what is not A–Z. Several
  words → the first letter of each of the first three; one word → its first
  two letters.
- **Customer letters:** derived from the customer name as above. If the
  customer already has projects, and none of their codes' leading letter runs
  starts with the derived letters, but those runs share a common prefix of two
  or more letters, that prefix (cut to the derived length) is used instead —
  so a hand-chosen customer prefix sticks. Internal project → `INT`.
- **Project letters:** derived from the project name; empty name → none.
- **Number:** the current value of the `project_code` counter (starts at
  1000), read without allocating. If the resulting code is taken, the number
  is bumped until free (bounded at 50 tries, then the endpoint answers the
  last candidate and the create validation speaks). The counter advances by
  one on every project create, whatever code was used.

### 4.3 Stats

For the list page KPI row and the dashboard, mirroring the other modules:

- `GET /projects/stats` — counts per status, over the projects the caller may see.
- `GET /projects/stats/summary?from=&to=` — `newProjects`, `activeProjects`.
- `GET /projects/stats/timeseries?metric=newProjects&from=&to=` — daily points.
- `GET /projects/stats/attention` — active projects past their end date.

All four respect visibility (D7): a member's numbers cover their projects.

## 5. Authorization

Permissions, all in category "Projects":

| Key | Meaning |
|---|---|
| `projects:access` | Use the Projects app; see projects you hold a role in. Required by every operation. |
| `projects:create` | Create projects. |
| `projects:view-all` | See every project. |
| `projects:manage-all` | Manage every project (implies seeing it, and its financials). |
| `projects:view-financials` | See financial fields on every project you can see. |

Role capabilities (code-defined, `internal/projects/roles.go`):

| Capability | manager | member | viewer |
|---|---|---|---|
| see the project, its people, lines (unpriced), timeline | ✓ | ✓ | ✓ |
| see financial fields | ✓ | – | – |
| edit project, change status, manage roles, manage lines | ✓ | – | – |

`member` and `viewer` are identical inside this module; they differ in what
later modules grant them (members log time, viewers do not). That is the
point of shipping both now: the assignment data is right before its first
consumer arrives.

The contract declares `permission:projects:access` (plus `projects:create` on
create). Everything per-project is decided in handlers by one function,
`authorize(ctx, projectID) → (capabilities, error)`, which loads the caller's
role and global permissions once; handlers never consult roles ad hoc. The
list query filters in SQL (`view-all`/`manage-all` → all; otherwise a join on
`project_roles`), so paging and counts are correct.

## 6. Contracts

All in `internal/contracts`, DTOs only, a missing row is `(nil, nil)`.

```go
// Provided by identity. Always present.
type UserDirectory interface {
    User(ctx, id uuid.UUID) (*UserEntry, error)
    Users(ctx, ids []uuid.UUID) ([]UserEntry, error)          // batch, for lists
    SearchUsers(ctx, query string, limit int) ([]UserEntry, error) // active users only
}
type UserEntry struct { ID uuid.UUID; DisplayName string; Active bool }

// Provided by products. nil on Deps when products is disabled.
type ProductCatalog interface {
    Variant(ctx, id int32) (*VariantEntry, error)
    Variants(ctx, ids []int32) ([]VariantEntry, error)
    ListPrice(ctx, variantID int32, currency string, at time.Time) (*Money, error)
}
type VariantEntry struct { ID, ProductID int32; ProductName, SKU, Unit, ProductType, ProductStatus string }
type Money struct { Amount float64; Currency string } // float64 like products' own price DTOs; stored numeric(12,2)

// Provided by projects. No consumer yet; this is what Time tracking builds on.
type ProjectDirectory interface {
    Project(ctx, id int32) (*ProjectEntry, error)
    Role(ctx, projectID int32, userID uuid.UUID) (string, error)  // "" = none
    BillingLine(ctx, projectID, lineID int32) (*BillingLineEntry, error)
    ProjectsForUser(ctx, userID uuid.UUID) ([]ProjectEntry, error)
}
type ProjectEntry struct { ID int32; Code, Name string; CustomerID *int32; Status string; OpenForWork bool; BillingType string }
type BillingLineEntry struct { ID, ProjectID int32; Code string; VariantID int32; PricingMode string; FixedAmount, DiscountPercent *float64; Active bool }
```

`ListPrice` reuses Products' existing active-price resolution (campaign price
beats the open-ended base price) rather than restating it. Cancelled and
completed projects, and inactive lines, still resolve, so old hours stay
readable.

**Platform change (`internal/module`).** `Module` gains provider fields
`Users`, `Products`, `Projects` beside `Directory`; `Deps` gains `Users`,
`Products`, `Projects`. `Compose` resolves each exactly as it resolves the
customer directory today — at most one enabled provider each (two → startup
failure naming both), built before any `Mount`. `modtest` gains the matching
options so a module test can run with a fake or a real provider.
`config` gains the "projects requires customers" check.

## 7. API

`openapi/projects.yaml`, mounted at `/api/v1/projects`. Every operation is
`permission:projects:access` unless noted.

| Operation | Notes |
|---|---|
| `GET /projects` | `search` (code or name), `status`, `customerId`, `mine`, `page`, `pageSize`. Visibility-filtered. Embeds customer name and manager display names. |
| `POST /projects` | `permission:projects:access+projects:create`. Creator becomes manager. |
| `GET /projects/{id}` | 404 for outsiders. Financial fields shaped (D12). |
| `PUT /projects/{id}` | Manager. Carries `revision`. |
| `PUT /projects/{id}/status` | Manager. |
| `GET /projects/code-suggestion` | Needs only `projects:access`, not `projects:create`: editing an existing project's code uses it too. |
| `GET /projects/{id}/roles` | Anyone who sees the project. Embeds display name and active flag. |
| `PUT /projects/{id}/roles/{userId}` | Manager. Adds or changes. |
| `DELETE /projects/{id}/roles/{userId}` | Manager. |
| `GET /projects/{id}/assignable-users?search=` | Manager. Active users, minus those already assigned. |
| `GET /projects/{id}/billing-lines` | Anyone who sees the project; pricing shaped. 409 when products is off. |
| `POST /projects/{id}/billing-lines` | Manager. |
| `PUT /projects/{id}/billing-lines/{lineId}` | Manager. Includes `active`. |
| `GET /projects/{id}/timeline` | Anyone who sees the project. Paged, newest first. |
| `GET /projects/stats`, `/stats/summary`, `/stats/timeseries`, `/stats/attention` | §4.3. |

The project resource carries `capabilities` for the caller
(`canManage`, `canSeeFinancials`) and `billingLinesAvailable` (whether
Products is enabled), so the frontend never re-derives authorization.

## 8. Frontend

New package `@vantigo/projects-ui` in `apps/projects/frontend`, laid out like
`apps/customers/frontend` (`api/`, `pages/`, `components/`, `i18n.ts`, tests
colocated). Generated types via a new target in `tools/openapi/gen-client.ts`.

### 8.1 The Projects app

Everything the app switcher design asks of a new app:

- `moduleKeys` gains `"projects"`; `apps.ts` registers
  `moduleApp("projects", "navigation.projects", IconBriefcase, "/projects", …)`
  with one sidebar entry, **Projects** → `/projects`, requiring
  `projects:access`, `searchStrategy: "projects-list"` (added to the union and
  `navSearchFor`). The tile therefore shows for anyone with `projects:access`,
  greys out with "Not enabled" when the module is off, and sits after
  Customers in switcher order.
- Layout route `routes/projects.tsx` =
  `createFileRoute("/projects")(appLayoutOptions("projects"))`, which gives
  the header title, the sidebar, and `ModuleNotEnabledPage` on deep links.
- The permission guard's prefix rules cover `/projects`.
- Dashboard: a Projects module card, the `newProjects` metric, and attention
  items, gated like the other cards.
- Spotlight: navigation is automatic from the registry; hand-added are a
  **Create project** quick action (navigates to `/projects?create=true`) and a
  Projects result group searching by code or name.
- Host i18n imports `@vantigo/projects-ui/i18n`; navigation, dashboard and
  permission labels (the five keys, in `catalogs/admin.ts`) in en and nb.
- ESLint isolation: the new package gets the `no-restricted-imports` block,
  and every other module package adds `@vantigo/projects-ui` to its list.

URL map:

| Route | Page |
|---|---|
| `/projects` | List |
| `/projects/$projectId` | Detail → Overview tab |
| `/projects/$projectId/people` | People tab |
| `/projects/$projectId/billing` | Billing tab |
| `/customers/$customerId/projects` | Projects tab on the customer page |

### 8.2 Pages

**List.** `PageHeader` with a create action (shown with `projects:create`); a
KPI row (active, planned, on hold) from `/projects/stats`; a toolbar with
debounced search and filters for status, customer and "My projects" (the
placement the inbox filters use); a table of code, name, customer or
"Internal", status badge, manager, dates; pagination. Search, filters and page
live in validated URL search params. Skeleton, empty state and error alert per
the CONTRIBUTING patterns.

**Create / edit modal** (`-project-form-modal.tsx`, one component, two modes).
Customer picker with an "Internal project" choice; name; code; dates; billing
type; fixed price, budget and currency shown only when relevant and only when
the caller may see financials. The code field follows the suggestion as
customer and name change, and **stops following the moment the user edits it
by hand**; a small "use suggestion" action restores it. Choosing "Internal"
forces `non-billable` and disables the billing type. In edit mode, changing
the code asks for confirmation (the pattern customer type uses) because
timesheets quote it. Server field errors land on their fields.

**Detail.** The package exports `ProjectDetailHeader`, `ProjectOverview`,
`ProjectPeople`, `ProjectBilling`; the **host** owns the tab list
(`routes/projects/-project-detail-layout.tsx`), each tab gated by
`{module, requiredPermissions}` plus the project's own `capabilities`, so
Time tracking and Tasks can add tabs later exactly as Energy does on the
customer page.

- *Overview*: details, a status control (managers), budget hours, timeline.
- *People*: assignments with role badges; managers get add (assignable-user
  search), change role and remove.
- *Billing*: only with `canSeeFinancials`. Billing type, fixed price, budget
  amount; and, when `billingLinesAvailable`, the lines table — trackable code
  (`KVEM1000-PM`), product, unit, pricing rule, current list price, active —
  with add / edit / deactivate for managers. The variant picker calls the
  Products API filtered to `Service` products (D15).

**Customer Projects tab.** A host route composing the package's
`CustomerProjectsPanel`: that customer's projects (visibility-filtered) and a
create button with the customer pre-filled. Added to `customerDetailTabs`,
gated on the projects module and `projects:access`; renders
`ModuleNotEnabledPage` inline when the module is off, like the Energy tab.

## 9. Testing

Backend, `package projects_test` against real Postgres via `modtest`:

- `main_test.go` contract recorder with `RequireCoverage` — every operation in
  `projects.yaml` exercised by a passing exchange; `gen/gen_test.go`.
- Projects CRUD and every §4.1 rule; code uniqueness regardless of case;
  internal ⇒ non-billable; currency rules.
- Code suggestion: letter derivation (including Æ/Ø/Å and one-word names),
  the sticky customer prefix, `INT`, number bumping.
- Visibility: role vs `view-all` vs `manage-all`; 404 for outsiders and 403
  for non-managers; list, counts and stats filtered; `mine`.
- Financial shaping: fields absent for members and viewers, present for
  managers, `view-financials` and `manage-all`, on get, list and lines.
- Roles: add / change / remove, disabled users, assignable-user search.
- Billing lines against a fake `ProductCatalog` (depguard forbids importing
  products, even in tests — the same seam energy uses for customers), and with
  no catalog at all, i.e. products **off** (409, `billingLinesAvailable: false`).
- Concurrency: stale revision → 409; two creates racing for one code → one
  wins, one gets the field error.
- Timeline: one entry per state change, written in the same transaction;
  billing entries carry no amounts.
- `ProjectDirectory`: every method, including cancelled projects and inactive
  lines resolving.

Platform and providers: `Compose` tests for the three new slots (provider
wiring, duplicate provider failure, nil when the provider is disabled);
`config` test for "projects requires customers"; `UserDirectory` tests in
identity; `ProductCatalog` tests in products, including `ListPrice` matching
the module's own price resolution; depguard and `schema_test` cover the new
module.

Frontend, Vitest + jsdom: the form (suggestion follows, stops on manual edit,
restores; internal forces non-billable; field errors; code-change
confirmation); the list (filters in URL, empty and error states); People and
Billing (capabilities hide actions; lines hidden when unavailable); the
customer panel; host tests for the registry entry, the layout route, the
detail tabs gate, the customer tab gate, spotlight and the dashboard card.

Gates before the PR: `go generate` and `bun run gen:client` with no drift,
regenerated `routeTree.gen.ts`, `mise run server:check`, `go vet ./...`, the Go
suite pinned to four CPUs, `bun run frontend:check`, translations check, and
`mise run smoke`.

## 10. Documentation

- `docs/projects.md` — the module: model, codes, roles, financial shaping,
  the optional Products dependency, the three contracts.
- `docs/module-boundaries.md` — the three new contracts, optional versus
  required providers, `projects` in the schema and `MODULES` lists.
- `CONTRIBUTING.md` — URL map gains the Projects app.
- `ROADMAP.md` — a Projects section: phase 1 (this); later: Time tracking on
  project and line codes, milestones / tasks, rates per person, configurable
  project roles, project documents, the event bus consumer.

## 11. Delivery

One branch, one PR, commits in four reviewable slices that each build and
pass on their own:

1. **Platform contracts** — `UserDirectory` (identity), `ProductCatalog`
   (products), the new `Module`/`Deps` slots, `modtest` options.
2. **Projects backend core** — schema, contract, projects, code suggestion,
   status, roles, timeline, stats, authorization, `ProjectDirectory`.
3. **Billing lines** — backend, including the products-off behaviour.
4. **Frontend** — package, app registration, pages, customer tab, spotlight,
   dashboard, translations, docs.
