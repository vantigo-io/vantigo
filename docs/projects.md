# Projects module

The Projects module is where work is organised: a project is a named, coded piece of
work for one customer (or none, which means internal), the people who may act on it,
and the commercial rules invoicing will later read. It is a vertical-slice module
inside the single Vantigo binary (`apps/server/internal/projects`), owns the
`projects` schema in the shared PostgreSQL database, and is the thing later modules —
**Time tracking** first, then tasks, invoicing and documents — attach work to.

It deliberately stops there. Projects stores no hours, issues no invoice and
**calculates no money**: it records the rule (billing type, fixed price, budget, and
billing lines pinned to product variants) and leaves resolving amounts to the module
that bills.

## Domain model

- **Project** — the unit of work: a unique `code`, `name`, plain-text `description`,
  optional `customerId` (absent means *internal*), `status`, `startDate`/`endDate`,
  `billingType`, and the financial fields `currency`, `fixedPriceAmount`,
  `budgetAmount` plus the non-financial `budgetHours`. The integer `id` is generated
  and never changes; `revision` guards concurrent edits.
- **Project role** — one row per (project, user): `manager`, `member` or `viewer`.
  One role per user per project; the creator of a project becomes its manager.
- **Billing line** — a short code within the project pinned to a product variant plus
  one pricing rule (`list`, `fixed` or `discount`). A line can be deactivated, never
  deleted.
- **Timeline entry** — one generated record per state change, written in the same
  transaction as the change: `project-created`, `code-changed`, `status-changed`,
  `details-changed`, `customer-changed`, `billing-changed`, `role-added`,
  `role-changed`, `role-removed`, `line-added`, `line-changed`, `line-deactivated`,
  `line-reactivated`. There are no manual notes.

`status` is one of `planned`, `active`, `on-hold`, `completed`, `cancelled`, and
**every transition is allowed**, reopening included. Only `active` means "open for
work" — the one question Time tracking will ask, exposed as `OpenForWork` on the
contract. Changing status is its own operation, so it gets its own timeline entry and
can grow its own rules later.

`billingType` is one of `time-and-materials`, `fixed-price`, `non-billable`. A
project with no customer is internal and **must** be `non-billable` — there is nobody
to invoice. A customer project may be any of the three. `fixed-price` requires a
`fixedPriceAmount` greater than zero, and any other billing type must not carry one.

**Projects are cancelled, never deleted**, and billing lines are deactivated, never
deleted: later modules hold project and line IDs, and a project cannot know what
refers to it. There is no `DELETE` on a project or a line anywhere in the contract.
Cancelled projects and inactive lines still resolve through the contract below, so
old hours stay readable.

**One currency per project.** `currency` (ISO 4217 shape, `^[A-Z]{3}$`) is required
as soon as any amount is set — a fixed price, a budget amount, or a `fixed` billing
line — and while a `fixed` line exists it can be neither cleared nor changed to
another currency (deactivated lines count: their amount is still denominated in the
currency it was typed in, and the line can be reactivated). Reprice or remove those
lines first. `list` and `discount` lines resolve in the project's currency, so they
never hold it back.

The `projects` schema holds no foreign key that leaves it: `customer_id`,
`variant_id` and every user ID are opaque, per
[module boundaries](module-boundaries.md) rule 4.

## Project codes

The ID is the identity; the **code is a label**. A code is free text, editable at any
time, and looked up for display, so renaming one can never orphan anything — and the
rename is recorded on the timeline, so an old code on a printed timesheet can still
be traced.

- A project code is upper-cased on input and must match `^[A-Z0-9]{2,20}$`. It is
  unique across the installation; because input is upper-cased, uniqueness is
  case-insensitive with a plain unique index. A taken code is an ordinary validation
  error on the `code` field.
- A billing line code must match `^[A-Z0-9]{1,10}$` and is unique within its project.
- The **trackable code** is `<project>-<line>`, for example `KVEM1000-PM`. Neither
  part can contain a hyphen, so it always splits cleanly.

Milestones and tasks are a *separate* dimension from billing lines (what the work is
*for* versus what *kind* of work it is). The format leaves room for them
(`KVEM1000-MIL1`); nothing else about them is decided.

### The suggestion

`GET /api/v1/projects/code-suggestion?customerId=&name=` answers `{"code":"KVEM1000"}`.
It is a suggestion, never imposed — the user may type anything valid instead.

- **Letters from a name**: split on anything that is not a letter, spell out `Æ→AE`,
  `Ø→O`, `Å→A`, strip other diacritics, drop what is not A–Z. Several words → the
  first letter of each of the first three; one word → its first two letters.
- **Customer letters**: derived from the customer name the same way. If the customer
  already has projects and none of their codes' leading letter runs starts with the
  derived letters, but those runs share a common prefix of two or more letters, that
  prefix (cut to the derived length) is used instead — so a hand-chosen customer
  prefix sticks. An internal project uses `INT`.
- **Number**: the current value of the `project_code` counter, which starts at 1000
  and is read *without* allocating, so calling the endpoint any number of times never
  changes what the next real project gets. The counter advances by one on every
  project create, whatever code was actually used.
- A taken candidate is skipped by bumping the number, bounded at 50 tries; past that
  the endpoint answers its last candidate and create's own uniqueness validation
  speaks.
- A candidate longer than 20 characters is shortened by trimming the project letters
  first, then the customer prefix. The number is never touched.

- A `customerId` that resolves to no customer contributes no letters, exactly as a
  customer whose name yields none would: the suggestion is then the project letters
  and the number. The endpoint never confirms whether a customer exists.

The endpoint needs only `projects:access`, not `projects:create`: editing an existing
project's code uses it too.

## Roles and permissions

Five permission keys, all delegable, in the category **Projects**:

| Key | Meaning | Sensitive |
| --- | --- | --- |
| `projects:access` | Use the Projects app and see the projects you hold a role in. **Required by every operation.** | no |
| `projects:create` | Create projects. | no |
| `projects:view-all` | See every project, not only the ones you hold a role in. | no |
| `projects:manage-all` | Manage every project, which also means seeing it and its financial fields. | yes |
| `projects:view-financials` | See fixed prices, budget amounts and line pricing on every project you can see. | yes |

`projects:access` is the baseline that gates the app: project roles are per project,
but the switcher tile, the sidebar and the permission guard work on global
permissions, so without it a project member would never see the tile.

Above the global permissions sit the three fixed project roles, defined in code
(`internal/projects/roles.go`) as named sets of capabilities, so a role editor could
be added later without changing the data model:

| Capability | manager | member | viewer |
| --- | --- | --- | --- |
| See the project, its people, its lines (unpriced) and its timeline | ✓ | ✓ | ✓ |
| See financial fields | ✓ | – | – |
| Edit the project, change status, manage roles, manage lines | ✓ | – | – |

`member` and `viewer` are **identical inside this module**; they differ in what later
modules grant them (members log time, viewers do not). Both ship now so the
assignment data is right before its first consumer arrives.

"At least one manager" is not enforced: `projects:manage-all` can always step in, and
enforcing it gets awkward when an account is disabled.

Everything per-project is decided by one function, `authorize(ctx, projectID)`, which
loads the caller's role and global permissions once and widens the global access by
the role; handlers never consult roles ad hoc. A role only ever *widens* — a narrow
role never takes away what a global permission granted.

**Outsiders get "not found", not "forbidden".** Reading a project you hold no role in,
without `view-all` or `manage-all`, answers **404**, so project codes and existence do
not leak. Acting on a project you *can* see but may not manage answers **403**.

**What `projects:create` can learn about customers.** Creating or updating a project
with a `customerId` that does not exist answers 400 "customer does not exist", and a
project response carries the customer's name — so a holder of `projects:create` can
probe the customer list without holding `customers:view`. That is an accepted
trade-off: the create form's customer picker needs `customers:view` to work at all,
so whoever may create projects is expected to hold both, and a project list that hid
customer names would be unreadable. The code suggestion is the one exception, because
it asks for nothing but `projects:access` — an id nobody can resolve is treated there
as no customer letters rather than refused (above).

Visibility is one SQL definition, the `projects.visible(project_id, user_id, see_all)`
function, shared by the list, its count and every stats query — so rows, totals and
dashboard numbers can never disagree about who sees what.

## Financial shaping

Financial fields are **shaped out of the response, not forbidden**. Fixed price,
budget amount, currency and line pricing are visible to a project's managers and to
holders of `projects:view-financials` or `projects:manage-all`. Everyone else gets
the same resource with those fields **absent** — absent, not `null`, the convention
Communications already uses.

Concretely:

- `ProjectResponse.financials` (`currency`, `fixedPriceAmount`, `budgetAmount`) is
  present exactly when the caller may see the money, and then always present even if
  empty, so a client can tell "may see, nothing entered" from "may not see".
  `currency` lives only inside `financials`: a caller who may not see the money sees
  no currency either.
- `BillingLineResponse.pricing` (`mode`, `fixedAmount`, `discountPercent`,
  `listPrice`) is absent the same way.
- `budgetHours` is deliberately outside `financials`: hours are planning data,
  visible to everyone who sees the project.
- `ProjectSummaryResponse`, the list row, carries no financial field at all — there
  is nothing to shape and no caller who could be shown a price by mistake.
- Timeline payloads for billing changes record **which field names changed, never
  amounts**, so the timeline needs no shaping.

Writing financial fields needs no rule of its own: only managers and `manage-all` can
update a project, both see its financials, and whoever creates a project becomes its
manager.

Every project response also carries `capabilities` (`canManage`, `canSeeFinancials`)
and `billingLinesAvailable`, so the frontend never re-derives authorization.

## Billing lines and the optional Products dependency

A billing line is "variant + pricing rule": `list` (the variant's list price), `fixed`
(a negotiated amount in the project's currency) or `discount` (a percentage off list
price, `0 < x <= 100`). "Project manager hour" is a `Service` **product**, not a role
— access and billing stay separate, one person can log two kinds of work on one
project, and an invoice line later gets its product, unit and tax category for free.
There is one pricing path and no hand-typed rate.

Line responses embed the variant's product name, SKU and unit through the product
catalog contract, so a member never needs a products permission to read "Project
manager hour". A line whose variant the catalog has since forgotten comes back with
`variantMissing: true` and stays editable — a request carrying the variant the line
is already pinned to is not asking for that variant to exist today, so a manager can
still deactivate or otherwise edit it. Moving a line to a variant nobody has is
refused.

**Products is an optional dependency; Customers is a required one.** When the
`products` module is not enabled, `Deps.Products` is nil and:

- every billing-line operation answers **409** with the problem title
  *"Products module not enabled"* — after the caller's own access gates, so a 409
  says "this installation has no catalog", never "this project exists";
- `billingLinesAvailable` is `false` on the project resource and the frontend hides
  the lines section;
- lines **stored before** Products was switched off stay stored and hidden, and the
  currency rule that counts `fixed` lines still counts them.

Without Products, Projects is still a working planning tool: codes, customers,
status, dates, budgets and people — just no priced lines.

`MODULES` carrying `projects` without `customers` fails startup with
`projects requires customers`, exactly as `energy` and `communications` do.

## Contracts for other modules

Cross-module reads go through `internal/contracts` — never another module's schema or
HTTP endpoints. Three contracts meet here; all are DTOs only, and for all of them a
missing row is `(nil, nil)`, never an error.

- **`contracts.UserDirectory`** — provided by *identity*, **always present** once
  composed (identity is always mounted, so there is no "disabled" case to handle).
  `User`, `Users` (batch, for lists) and `SearchUsers`. A `UserEntry` carries only ID,
  display name and an `Active` flag — no email, roles or MFA state. `SearchUsers`
  returns **active users only**, while `User`/`Users` still resolve a disabled user so
  an existing assignment stays readable and removable. Projects needs it because the
  only user listing before it required the authorization-management policy, which
  would have stopped a project manager who is not an administrator from picking a
  colleague.
- **`contracts.ProductCatalog`** — provided by *products*, **nil when products is
  disabled** (the first optional contract in the codebase). `Variant`, `Variants` and
  `ListPrice(variantID, currency, at)`, which reuses Products' own active-price
  resolution (a bounded campaign price beats the open-ended base row) rather than
  restating it. A variant with no price in that currency at that moment is
  `(nil, nil)`.
- **`contracts.ProjectDirectory`** — provided by *projects*. `Project`,
  `Role(projectID, userID)` (`""` means no role), `BillingLine` and
  `ProjectsForUser`. **Cancelled projects and inactive lines still resolve**, so a
  consumer can read old work.

On the platform side, `module.Module` has provider fields `Users`, `Products` and
`Projects` beside `Directory`, and `module.Deps` has the matching consumer fields.
`Compose` resolves **at most one enabled provider per slot** — two providers is a
startup failure naming both — and builds them before any `Mount` runs. An *optional*
provider whose module is disabled simply leaves its `Deps` field nil, and the
consumer must handle that (see the 409 above). `modtest` has matching options so a
module test can run against a fake or a real provider.

### What Time tracking should build on

Time tracking is the first consumer, and the seam is already in place:

- Store the **project ID**, never the code. Read the current code through
  `ProjectDirectory.Project` for display; a rename then costs nothing.
- Ask `OpenForWork` (`status == "active"`) before accepting hours. Do not
  re-enumerate statuses.
- Hang hours off the **trackable code** `<project>-<line>` (`KVEM1000-PM`): the line
  is what says *what kind of work* this was, and it carries the variant that prices
  it. `ProjectDirectory.BillingLine` resolves inactive lines too, so hours already
  logged stay priceable after a line is retired.
- Use `ProjectDirectory.Role` for "may this person log time here", and
  `ProjectsForUser` for "which projects can I pick". `member` and `viewer` are the
  same inside Projects — **Time tracking is the module that gives them different
  meanings** (members log time, viewers do not).
- Resolve amounts yourself, or leave it to invoicing. Projects stores the rule; it
  never multiplies anything.

There is deliberately no event: the roadmap defers the event bus until Orders, and a
synchronous contract is enough for everything Time tracking needs.

## API

Versioned REST endpoints live under `/api/v1/projects`, authenticated with the shared
identity session cookie. Every operation requires `permission:projects:access`;
create additionally requires `projects:create`.

| Endpoint | Description |
| --- | --- |
| `GET /api/v1/projects` | List with `page`, `pageSize`, `search` (code or name), `status`, `customerId`, `internal`, `mine`. Visibility-filtered; embeds customer name and manager display names |
| `POST /api/v1/projects` | Create. The creator becomes the project's manager. Needs `projects:create` |
| `GET /api/v1/projects/code-suggestion` | Suggest a code from a customer and a name |
| `GET /api/v1/projects/{id}` | One project; 404 for outsiders, financial fields shaped |
| `PUT /api/v1/projects/{id}` | Update. Manager only. Carries `revision`; a stale one answers 409 |
| `PUT /api/v1/projects/{id}/status` | Change status. Manager only |
| `GET /api/v1/projects/{id}/roles` | The project's people, managers first. Anyone who sees the project |
| `PUT /api/v1/projects/{id}/roles/{userId}` | Add or change an assignment. Manager only |
| `DELETE /api/v1/projects/{id}/roles/{userId}` | Remove an assignment. Manager only |
| `GET /api/v1/projects/{id}/assignable-users` | Active users not already assigned, for the picker. Manager only |
| `GET /api/v1/projects/{id}/billing-lines` | The project's lines, pricing shaped. 409 when products is off |
| `POST /api/v1/projects/{id}/billing-lines` | Add a line. Manager only |
| `PUT /api/v1/projects/{id}/billing-lines/{lineId}` | Update a line, `active` included. Manager only |
| `GET /api/v1/projects/{id}/timeline` | The project's generated events, newest first, paged |
| `GET /api/v1/projects/stats` | Counts per status over the projects the caller may see |
| `GET /api/v1/projects/stats/summary` | `newProjects` and `activeProjects` over a period, with deltas |
| `GET /api/v1/projects/stats/timeseries` | Daily buckets for one metric |
| `GET /api/v1/projects/stats/attention` | Active projects past their end date |

All four stats endpoints respect visibility: a member's numbers cover their projects.

`openapi/projects.yaml` is the contract and the source of truth; the router enforces
each operation's access rule from it at runtime. The running server serves the merged
contract of its enabled modules at **`GET /api/openapi.json`, which requires a
session**.

Validation errors are RFC 7807 problems keyed by camelCase JSON path, as everywhere
else. A role assignment requires the user to resolve through `UserDirectory` and to
be active when *added*; an existing assignment of a since-disabled user stays
readable and removable. An archived customer resolves and is allowed — a project can
outlive the relationship.

## The Projects app

The UI is `@vantigo/projects-ui` (`apps/projects/frontend`), composed by the host SPA
like every other module package; module packages never import each other.

| Route | Page |
| --- | --- |
| `/projects` | List, with the KPI row, filters in the URL and the create modal |
| `/projects/$projectId` | Detail → Overview tab (details, budget hours, timeline) |
| `/projects/$projectId/people` | People tab (assignments, role badges, add/change/remove for managers) |
| `/projects/$projectId/billing` | Billing tab, gated on `capabilities.canSeeFinancials` |
| `/customers/$customerId/projects` | Projects tab on the customer page |

The project header — code, name, customer, status badge and the status control a
manager changes it with — is `ProjectDetailHeader`, above the tab row, so it stands on
every tab of the detail page rather than on the Overview tab alone.

The app is registered in `apps/host/frontend/src/apps.ts` and shows in the switcher
for anyone with `projects:access`, greying out with "Not enabled" when the module is
off. The host also owns the detail tab list, so Time tracking and Tasks can add tabs
later exactly as Energy does on the customer page. Spotlight has a **Create project**
quick action and a Projects result group searching by code or name; the dashboard has
a Projects card, the `newProjects` metric and the overdue-project attention items.

The billing-line variant picker calls the **products** API from the browser — the
existing cross-module frontend rule — filtered to `Service` products. A manager who
lacks a products view permission sees the lines but gets a hint instead of a picker.

## Enabling and disabling

`MODULES` is a positive allowlist of the business modules a deployment serves:

```text
MODULES=customers,projects
```

Unset enables every module the binary can mount. Omitting `projects` means the module
contributes no route, no permission and no contract path, its paths answer the `/api`
catch-all 404, the switcher tile greys out, and `Deps.Projects` is nil for everyone
else. The `projects` schema is migrated regardless, so enabling it later needs no
migration.

Two startup rules to know:

- `projects` without `customers` fails configuration with `projects requires
  customers` — projects resolve customers through `contracts.CustomerDirectory`.
- `products` is optional. Enabling `projects` without it is a supported configuration;
  billing-line operations then answer 409 and the lines section disappears from the
  UI.

Projects contributes **no background worker** and **no rate-limited operation**.

## Development

The backend is `apps/server/internal/projects`, with typed queries generated by sqlc
and the server interface generated by oapi-codegen from `openapi/projects.yaml`. The
UI lives in `apps/projects/frontend` (`@vantigo/projects-ui`) and is composed by the
host SPA in `apps/host/frontend`.

The Go package's tests run every HTTP exchange through a contract-validating client
and gate on operation coverage: every operation in `openapi/projects.yaml` must have
been exercised by at least one successful exchange, with no allow-list. Billing lines
are tested against a fake `ProductCatalog` — depguard forbids importing products even
in tests — and again with no catalog at all, which is the products-off behaviour.

```bash
bun run --cwd apps/projects/frontend test
cd apps/server && go test ./internal/projects/
```
