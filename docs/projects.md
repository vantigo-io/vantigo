# Projects module

The Projects module is where work is organised: a project is a named, coded piece of
work for one customer (or none, which means internal), the people who may act on it,
and the commercial rules invoicing will later read. It is a vertical-slice module
inside the single Vantigo binary (`apps/server/internal/projects`), owns the
`projects` schema in the shared PostgreSQL database, and is the thing later modules —
**Time tracking** first, then invoicing and documents — attach work to. Tasks live
here too: they are how a project's work is broken down, and they follow the project's
own roles rather than permissions of their own.

It deliberately stops there. Projects stores no hours and issues no invoice: it
records the rule (billing type, fixed price, budget, and billing lines pinned to
product variants) and leaves resolving amounts to the module that bills. It computes
exactly one thing itself — a billing milestone's share of the fixed price, the
percent-of-price arithmetic behind the invoice plan below — and nothing more.

## Domain model

- **Project** — the unit of work: a unique `code`, `name`, plain-text `description`,
  optional `customerId` (absent means *internal*), `status`, `startDate`/`endDate`,
  `billingType`, and the financial fields `currency`, `fixedPriceAmount`,
  `budgetAmount`, `defaultBillRate` plus the non-financial `budgetHours`. The integer
  `id` is generated and never changes; `revision` guards concurrent edits.
- **Project role** — one row per (project, user): `manager`, `member` or `viewer`.
  One role per user per project; the creator of a project becomes its manager.
- **Billing line** — a short code within the project pinned to a product variant plus
  one pricing rule (`list`, `fixed` or `discount`). A line can be deactivated, never
  deleted. It may also carry a `budgetHours` and a `budgetAmount` — see
  [Billing lines and the optional Products dependency](#billing-lines-and-the-optional-products-dependency).
- **Billing milestone** — a named step of a project's invoice plan, priced as a flat
  amount or a share of the fixed price, moving through `planned → ready → invoiced`
  with `cancelled` off to the side — see
  [Billing milestones and the invoice plan](#billing-milestones-and-the-invoice-plan).
- **Task** — a piece of the project's work: `title`, `description`, `status`, one
  optional `assigneeUserId`, `startDate`/`dueDate`, `estimateHours`, a `position`
  among its siblings and an optional `parentTaskId` (see [Tasks](#tasks)). Each task
  carries **checklist items** and **comments**. Unlike projects and lines, tasks *are*
  deleted, cascading to their subtasks, checklist and comments — nothing outside
  Projects pins a task the way it pins a project or a line.
- **Timeline entry** — one generated record per state change, written in the same
  transaction as the change: `project-created`, `code-changed`, `status-changed`,
  `details-changed`, `customer-changed`, `billing-changed`, `role-added`,
  `role-changed`, `role-removed`, `line-added`, `line-changed`, `line-deactivated`,
  `line-reactivated`, `milestone-added`, `milestone-changed`, `milestone-removed`,
  `milestone-ready`, `milestone-planned`, `milestone-invoiced`,
  `milestone-invoice-undone`, `milestone-cancelled`, `milestone-reopened`. There are
  no manual notes.

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
as soon as any amount is set — a fixed price, a budget amount, a default bill rate, a
`fixed` billing line, a line's `budgetAmount`, or a billing milestone — and it can
never be **cleared** while any of those is set. **Changing** it to another currency is
narrower: it is additionally refused only while a `fixed` billing line, a line's
`budgetAmount`, or a non-cancelled billing milestone exists (deactivated lines count:
their amount is still denominated in the currency it was typed in, and the line can be
reactivated; a **cancelled** milestone does not count — it bills nothing, so it cannot
hold the currency back). The project's own fixed price, budget amount and default bill
rate do not hold a currency *change* back the same way — only clearing the currency
while they are set is refused; changing it moves them to the new currency's meaning
along with the rest of the project. Reprice, remove or cancel the three that do hold a
change back, first. `list` and `discount` lines resolve in the project's currency, so
they never hold it back either way. See [Locking](#locking) for how this is decided
safely under concurrent writes.

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
| See the project, its people, its lines (unpriced), its timeline and its tasks | ✓ | ✓ | ✓ |
| Write tasks, checklist items and comments ([Tasks](#tasks)) | ✓ | ✓ | – |
| See financial fields | ✓ | – | – |
| Edit the project, change status, manage roles, manage lines | ✓ | – | – |

Writing tasks is where `member` and `viewer` stop being the same thing; Time tracking
will draw the same line again (members log time, viewers do not).

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

- `ProjectResponse.financials` (`currency`, `fixedPriceAmount`, `budgetAmount`,
  `defaultBillRate`) is present exactly when the caller may see the money, and then
  always present even if empty, so a client can tell "may see, nothing entered" from
  "may not see". `currency` lives only inside `financials`: a caller who may not see
  the money sees no currency either. `defaultBillRate` — the rate Time tracking bills
  an entry at when no billing line sets one — is an amount for D13's purposes: it
  requires `currency` exactly as the other amounts do, and clearing the currency while
  it is set fails validation rather than silently dropping it.
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

Every project response also carries `capabilities` (`canManage`, `canSeeFinancials`,
`canManageMilestones`) and `billingLinesAvailable`, so the frontend never re-derives
authorization. `canManageMilestones` is true for the project's managers and
`projects:manage-all`, and false for a `projects:view-financials` holder who is not
one — see [Billing milestones and the invoice plan](#billing-milestones-and-the-invoice-plan).

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

### Budgets on billing lines

A line may carry `budgetHours` and `budgetAmount`, both optional, both `> 0` when set
and both bounded by the column that holds them — `budgetAmount` at 9 999 999 999.99
(`numeric(12,2)`) and `budgetHours` at 99 999 999.99 (`numeric(10,2)`), so a number
too wide is a field error rather than a database overflow the handler can only answer
500 to. The project's own `fixedPriceAmount`, `budgetAmount`, `defaultBillRate` and
`budgetHours` carry the same bounds, for the same reason. `budgetHours` is planning data — visible with the line to everyone who sees the
project, deliberately outside the financial shaping above, exactly like the project's
own `budgetHours`. `budgetAmount` is financial data: it lives inside the line's
`pricing` object, so it is present only for a caller who may see the money, and it
requires the project to have a `currency` (reported as a field error on
`budgetAmount` itself). Setting either is optional and independent of the other; a
line's relation to the project's own budget is not enforced, only shown, in the
Economy tab's later delivery. A `line-changed` timeline entry names `budgetHours` and
`budgetAmount` alongside the line's other fields — never a value, as every line-change
entry works.

## Billing milestones and the invoice plan

A **billing milestone** is a named step of a project's invoice plan: `name`, an
optional `description` and `plannedDate`, and either a flat `amount` or a `percent`
of the project's fixed price — exactly one of the two, never both, never neither.
A flat amount **remembers the currency it was entered in** (`amount_currency`,
stamped from the project's currency when the amount is written and cleared when the
milestone becomes a percent one); a percent has none of its own, because it resolves
against a fixed price that is always in the project's current currency. Any project that carries a currency may have
milestones, whatever its billing type; a `percent` milestone additionally needs the
project to be `fixed-price` with a `fixedPriceAmount` set, because a percent is a
share of that number. `amount` is greater than zero, at most 9 999 999 999.99 and at
most two decimals; `percent` is greater than zero, at most 100 and at most two
decimals — both column-bounded, so a number too wide or too precise is a field error
rather than a database overflow or a silent rounding.

**Effective amount** is what the plan actually counts for a milestone, computed on
every read rather than stored: the amount frozen when it was invoiced, else the flat
amount as entered, else the project's fixed price times the percent. The
percent-of-price arithmetic is **exact decimal** (`math/big.Rat` over the numeric
columns' text, never `float64`), rounded half up to two places — 300 000.00 at
33.33 % is exactly 99 990.00, and 100 000.01 at 12.5 % is exactly 12 500.00 (the true
value, 12 500.00125, rounds down). Because it is computed on read, an open (not yet
invoiced) percent milestone follows a later change to the fixed price; freezing is
what stops an invoiced one from moving — the number an invoice was actually raised
for must never drift under it. `effectiveAmount` is absent, never zero, when the milestone
cannot be priced at all, and such a milestone is left out of the totals too. The
expected case is a **cancelled** milestone priced as a percent of a fixed price the
project has since dropped — there is nothing left to compute it from, and a milestone
that bills nothing should not read as "0.00" either. Any *other* status in that state
is a row the module's own rules forbid, so it renders the same way and is logged at
warning rather than failing the read: one bad row, which only data written outside
the API can produce, must not take the whole plan down.

**Status moves.** A milestone is `planned`, `ready`, `invoiced` or `cancelled`. Every
allowed move, and who may make it:

| From → to | Who |
| --- | --- |
| `planned → ready` | project manager, `projects:manage-all` |
| `ready → planned` | project manager, `projects:manage-all` |
| `ready → invoiced` | financial rights on the project (manager, `manage-all`, or `projects:view-financials`) |
| `invoiced → ready` (undo) | financial rights |
| `planned → cancelled`, `ready → cancelled` | project manager, `projects:manage-all` |
| `cancelled → planned` (reopen) | project manager, `projects:manage-all` |

Any other pair — including a move to the status the milestone already has — is a 400
on `status` naming both statuses; it is not a "no-op", so it writes no timeline entry.

A move whose target is not `cancelled` **re-asks the project's own rules** against the
row the write locks: it is refused if the project has no currency, if the milestone is
a percent one and the project has no fixed price, or if the milestone's flat amount is
in a currency the project has since moved off — that last one can only be a reopen,
and the refusal says to add a new milestone rather than bringing an old number back
into a different currency. Cancelling is never
refused this way — it is how a milestone the project can no longer support is got rid
of. **The one exception** is undoing an invoicing (`invoiced → ready`): the
missing-fixed-price refusal never applies to it, because crediting an invoice is a
real event that must not be blocked — the currency check still applies to an undo,
though a project with an invoiced milestone cannot in practice have lost its currency
(an invoiced milestone is non-cancelled, and the currency guard above refuses clearing
the currency while one exists). If the milestone was a percent of a fixed price the
project has since dropped, the undo instead **converts** it to an amount milestone — `amount` becomes
the amount that was frozen when it was invoiced, stamped with the currency that
invoice was raised in, and `percent` is cleared — so the number that was actually
billed survives even though the share it once was no longer means anything. The timeline entry for that undo carries `convertedToAmount: true`.

Marking a milestone `→ ready` stamps who did it and when; `ready → planned` and
`cancelled → planned` clear those stamps. `→ invoiced` stamps who and when, stores an
optional `invoiceReference` (≤ 100 characters, trimmed) and `invoiceDate`, and
freezes the effective amount into `invoicedAmount`; `invoiced → ready` clears all five
of those columns (who, when, reference, date, frozen amount). A reference or a date
sent on any other move is refused, naming the field, rather than silently ignored.

**Editing and deleting.** A milestone's content (name, dates, amount or percent) can
be edited by the project's manager only while it is `planned` or `ready`; an
`invoiced` or a `cancelled` milestone is read-only until moved back — a 400 on
`status` says so. `PUT` decides in a fixed order once it holds both locks: a stale
`revision` answers 409 first, then the read-only rule (400 on `status`), and only then
the project-dependent rules on the body itself (currency, percent-needs-price) —
so a caller two states behind is told to re-read the plan rather than being sent to
fix a project field they may no longer be able to touch. An edit that changes nothing writes no timeline entry; one that does
writes `milestone-changed` naming the fields that moved (`name`, `description`,
`plannedDate`, `amount`, `percent` — names only, never the values). **Delete** is
narrower still: only while the milestone is still `planned` **and has never changed
status** (`ever_moved`) — anything else is part of what the plan says happened, and
is cancelled instead. A delete renumbers the remaining milestones 1..n so no gap is
left.

**Ordering.** A new milestone is appended last. `PUT .../position` renumbers the
whole project's milestones 1..n in one transaction, so a reorder and a delete can
never leave a gap or a duplicate; a position past the end means last. The listing
always sorts cancelled milestones last, whatever position they are renumbered to —
they keep their number, only the display order moves, so `position` and the array
index diverge as soon as a cancelled milestone holds an early number. `position` in a
request is always the stored numbering, never an index into the returned list. There is no separate ordering
lock: every milestone write already locks the project row first (see
[Locking](#locking)), which serialises every write against one project, ordering
included.

**Access.** Reading the plan or one milestone needs financial rights on the project —
not merely seeing it — because every row is an amount. An outsider to the project
gets the same bare **404** an unknown id gets, resolved by loading the milestone
first and then its project, exactly as a task is. A caller who can see the project
but holds no financial rights gets **403** on every milestone operation, reads
included — the milestone's existence is not the secret, its amount is. Writing
(create, edit, delete, reorder) needs the project's manager or `manage-all`; marking
invoiced and undoing it need only financial rights, because whoever may see the money
may say it was billed.

`currency` and `effectiveAmount` are both **optional** on a milestone, and
`totals.currency` is optional on the plan: a cancelled milestone can outlive the
project's currency or fixed price (the currency guard in
[One currency per project](#domain-model) only counts non-cancelled milestones), so a
required field would sometimes have to lie. A milestone's `currency` is its
`amount_currency` when it has a flat amount and the project's current currency when it
has a percent; it is absent only for a percent milestone on a project with no currency
at all. Nothing is ever `null` in place of a real value, or `0` in place of absent.

**Guarding the fixed price.** Removing a project's fixed price — clearing
`fixedPriceAmount`, or changing `billingType` away from `fixed-price`, which requires
clearing it — is refused while open (non-cancelled, non-invoiced) percent milestones
still price themselves from it. The refusal lands on `billingType`, because that is
the only field a caller can actually change to reach this state (a `fixed-price`
project's own validation already refuses clearing the amount on its own), and it
names up to five of the milestones by name, then "and N more". Changing the price to
a different number is always allowed; every open percent milestone simply follows it.

The plan's `GET` also returns **totals** — `planned`, `ready` and `invoiced` sums of
effective amounts and, against a fixed price, `unplanned` or `overPlanned` (never
both): the difference between the fixed price and what is planned, ready and
invoiced, whichever way it runs. There is **no cancelled sum**: a cancelled milestone
bills nothing, and its flat amount may still be denominated in a currency the project
has moved off, so summing it would mix two currencies into one figure. Every
milestone that *is* summed is in the project's current currency, because the guard
will not let that currency move while one exists. Totals never block a save — a plan
may be over- or under-planned and still saved.

The timeline gained nine event types: `milestone-added`, `milestone-changed`,
`milestone-removed`, `milestone-ready`, `milestone-planned`, `milestone-invoiced`,
`milestone-invoice-undone`, `milestone-cancelled`, `milestone-reopened`. As with a
billing line's own entries, the payload never carries an amount — only the
milestone's id and name (and, for `milestone-changed`, the field names that moved; for
`milestone-invoice-undone`, the `convertedToAmount` flag) — because the timeline is
read by everyone who can see the project, financial rights or not.

The seven milestone operations are in the [API](#api) table below, alongside the
rest of the module's endpoints.

## Locking

Every transaction that changes a project's currency, fixed price or billing type — or
that writes a row whose validity depends on one of those (a billing line's `fixed`
pricing or `budgetAmount`, a billing milestone) — **locks the project row first**
(`SELECT ... FOR NO KEY UPDATE`) and decides its rule against the row that lock
returns, not against a read taken before the transaction opened. This closes the race
between, for example, a line getting a `budgetAmount` and the project's currency being
cleared in the same instant: whichever transaction locks the row first wins, and the
other re-validates against what the winner left behind. One lock mode everywhere, and
always taken in the same order relative to any other lock a transaction needs (the
project row, then a line's or a milestone's own row) — so nothing can deadlock and
nothing needs to upgrade.

`FOR NO KEY UPDATE` rather than `FOR UPDATE` on purpose. The weaker mode still
conflicts with itself and with the row lock the project's own `UPDATE` takes, so every
guarded writer still serialises against every other; what it does *not* conflict with
is the `FOR KEY SHARE` Postgres takes on the project row for each insert that
references it. Under `FOR UPDATE`, creating an unrelated task, role, comment, billing
line, milestone or timeline entry on the same project would wait behind any guarded
write. Nothing in this module changes a project's key, which is what makes the weaker
mode correct and not merely cheaper.

A cross-module call — asking the product catalog whether a variant exists — is always
made **before** the project's lock is taken, never inside the locked transaction:
a slow or blocked call into another module must never stall every other writer of the
project. The transaction then only decides whether that prefetched answer matters,
against the row its own lock returns.

## Tasks

A task is one piece of a project's work. The model is deliberately small: **one
assignee**, a **fixed three-value status** (`todo`, `in-progress`, `done`), **one
level of subtasks**, a checklist and a comment thread. There are no labels, no
dependencies, no custom fields and no per-project workflow — a project tool's task
list, not an issue tracker.

- **Subtasks are one level deep.** A task's `parentTaskId` must name a *top-level*
  task of the same project, so a subtask can never itself be a parent. A parent's
  status is independent of its children: nothing rolls up in this phase.
- **Ordering** is the `position` among siblings. New tasks append;
  `PUT /tasks/{taskId}/position` takes `{ parentTaskId?, position }` and renumbers
  the whole sibling set in one transaction, so re-parenting and reordering are the
  same operation and no two siblings can end up sharing a slot.
- **`status → done`** stamps `completed_at`; leaving `done` clears it again.
- **Checklist items** are `text` (≤ 500) plus `done`, ordered by `position` — the
  small steps inside one task, not tasks in their own right. The task resource
  carries the `done`/`total` counts so a list row needs no extra request.
- **Comments** are a flat, paged thread of `body` (≤ 4000) with an `edited_at` stamp.
- **Validation:** `title` required and ≤ 200, `description` ≤ 4000,
  `dueDate >= startDate` when both are set, `0 < estimateHours <= 999999.99` (the
  column's own width) when set, and an `assigneeUserId` that resolves through
  `contracts.UserDirectory` and is **active when assigned**. An assignee who is later
  disabled keeps the task readable and the field editable, exactly as a project role
  does.
- **Concurrency:** every task update is a full replace carrying the `revision` the
  task was read at; a stale one answers **409**, as a project update does. `status` is
  **required** on the update for the same reason: a full replace that left it out
  would silently reopen a finished task. A create may omit it and gets `todo`.
- **Deleting** a task cascades to its subtasks, checklist items and comments.
- **Task writes are not gated on the project's status.** A cancelled or completed
  project's tasks can still be created, edited, moved and deleted — only *time
  logging* needs an `active` project (`CanLogTime`, `OpenForWork`). The task rules ask
  the caller's role and nothing else.

### Authorization

Tasks add **no permission key**. They follow the project's roles, which is what makes
`member` and `viewer` different for the first time:

| Who | May |
| --- | --- |
| Sees the project (any role, or `projects:view-all`/`manage-all`) | Read tasks, checklists and comments |
| `member` or `manager` | Create, edit, delete tasks; edit checklist items; add comments |
| Comment author | Edit and delete their own comment |
| `manager` | Delete anyone's comment |
| Outsider | **404**, indistinguishable from an unknown id |

`GET /api/v1/projects/my-tasks` is the one task endpoint that is not scoped to a
project: it answers the caller's own open (not `done`) tasks across every project
they can see, with the project code and name embedded, ordered by due date and then
project. It needs nothing beyond `projects:access`.

### In the app

The Tasks tab (`/projects/$projectId/tasks`) shows the tree as either a list grouped
by status, with subtasks folded under their parent and checklist progress on the row,
or a board of three columns, one per status, where a card moves from its own menu.
The task drawer holds the description, assignee, dates, estimate, subtasks, checklist
and comments. A caller who may only read gets the same views without the actions.

One task is deep-linkable: **`/projects/{projectId}/tasks?task={taskId}`**, which the
package's `taskUrl` builds and "My tasks" links to. The host route validates `task`,
opens the drawer on it, and drops it from the URL again when the drawer closes, so
neither a refresh nor Back reopens a drawer the caller just shut.

`/projects/my-tasks` is the cross-project list of the caller's own open tasks, a
sidebar entry beside Projects. Spotlight's **Create task** action lands there with
`?create=true`, which opens a project picker over the caller's own projects and then
the ordinary task form on the project chosen — nothing in the spotlight knows which
project a new task belongs to.

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
  `Projects` (batch, for a list of ids), `ProjectByCode` (case-insensitive),
  `Role(projectID, userID)` (`""` means no role), `BillingLine`, `BillingLines`
  (every line on a project, active and inactive), `ProjectsForUser`, `Task`,
  `OpenTasksForUser` and `CanLogTime(projectID, userID)`. **Cancelled projects and
  inactive lines still resolve**, so a consumer can read old work. `ProjectEntry`
  carries `Currency` and `DefaultBillRate`, which are financial fields: only a
  consumer that gates on a financial-viewer permission of its own should surface
  them. `TaskEntry` is deliberately thin — id, project, title, status, assignee and
  due date — enough to name a task on a timesheet row, never enough to manage one.

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
- Ask **`CanLogTime(projectID, userID)`** for "may this person log time here": it
  answers the project-is-active *and* member-or-manager question in one place, so the
  rule is not restated per consumer. `Role` is still there for anything finer, and
  `ProjectsForUser` answers "which projects can I pick".
- Hang a time entry off a **task** with `Task(id)` for its title and project, and
  offer the timesheet's "my open tasks" rows from **`OpenTasksForUser(userID)`**.
  Snapshot the title onto the entry: a task can be renamed or deleted, and old hours
  must stay readable. ⚠️ **`OpenTasksForUser` is not visibility-filtered** — it
  answers every open task assigned to the user, including ones on projects they have
  since lost their role on, which is *not* what `/projects/my-tasks` renders (that
  endpoint adds the project-visibility predicate). Combine it with
  `CanLogTime(projectID, userID)` — or `Role` — before showing or accepting a row.
- Rates: `ProjectEntry.DefaultBillRate` is the project's step of the rate chain
  (billing-line rule → project default → person default). It is a financial field —
  surface it only behind a financial-viewer permission of your own.
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
| `GET /api/v1/projects/{id}/billing-lines` | The project's lines, pricing shaped (`budgetAmount` included). 409 when products is off |
| `POST /api/v1/projects/{id}/billing-lines` | Add a line, `budgetHours`/`budgetAmount` included. Manager only |
| `PUT /api/v1/projects/{id}/billing-lines/{lineId}` | Update a line, `active` and budgets included. Manager only |
| `GET /api/v1/projects/{id}/milestones` | The invoice plan, position order, cancelled last, with totals. Financial rights |
| `POST /api/v1/projects/{id}/milestones` | Add a milestone; appended last. Manager only |
| `GET /api/v1/projects/milestones/{milestoneId}` | One milestone. Financial rights |
| `PUT /api/v1/projects/milestones/{milestoneId}` | Full replace, carrying `revision`; a stale one answers 409. Manager only |
| `DELETE /api/v1/projects/milestones/{milestoneId}` | Only `planned` and never moved; otherwise 400. Manager only |
| `PUT /api/v1/projects/milestones/{milestoneId}/position` | Renumber the plan 1..n; carries `revision` (checked, not bumped). Manager only |
| `POST /api/v1/projects/milestones/{milestoneId}/status` | One move through the status flow — see [Billing milestones and the invoice plan](#billing-milestones-and-the-invoice-plan) |
| `GET /api/v1/projects/{id}/tasks` | The project's task tree, with checklist counts and comment counts. Anyone who sees the project |
| `POST /api/v1/projects/{id}/tasks` | Add a task. Member or manager |
| `PUT /api/v1/projects/tasks/{taskId}` | Replace a task, carrying `revision`; a stale one answers 409. Member or manager |
| `DELETE /api/v1/projects/tasks/{taskId}` | Delete a task and everything under it. Member or manager |
| `PUT /api/v1/projects/tasks/{taskId}/position` | Reorder and re-parent among siblings. Member or manager |
| `GET/POST /api/v1/projects/tasks/{taskId}/checklist` | The task's checklist. GET: sees the project; POST: member or manager |
| `PUT/DELETE /api/v1/projects/tasks/{taskId}/checklist/{itemId}` | Tick, rename or remove an item. Member or manager |
| `GET/POST /api/v1/projects/tasks/{taskId}/comments` | The task's thread, paged. GET: sees the project; POST: member or manager |
| `PUT/DELETE /api/v1/projects/tasks/{taskId}/comments/{commentId}` | The author, or a manager for DELETE |
| `GET /api/v1/projects/my-tasks` | The caller's open tasks across every project they see, with project code and name |
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
| `/projects/my-tasks` | The caller's open tasks across projects; sidebar entry beside Projects |
| `/projects/$projectId` | Detail → Overview tab (details, budget hours, timeline) |
| `/projects/$projectId/tasks` | Tasks tab (list and board, task drawer); `?task={id}` deep-links one task |
| `/projects/$projectId/people` | People tab (assignments, role badges, add/change/remove for managers) |
| `/projects/$projectId/billing` | Billing tab, gated on `capabilities.canSeeFinancials` |
| `/projects/$projectId/economy` | Economy tab (the invoice plan), between Billing and Time; gated on `capabilities.canSeeFinancials` like Billing — the next delivery widens it to everyone who sees the project once it has an hours-only half to show them |
| `/customers/$customerId/projects` | Projects tab on the customer page |

The project header — code, name, customer, status badge and the status control a
manager changes it with — is `ProjectDetailHeader`, above the tab row, so it stands on
every tab of the detail page rather than on the Overview tab alone.

The app is registered in `apps/host/frontend/src/apps.ts` and shows in the switcher
for anyone with `projects:access`, greying out with "Not enabled" when the module is
off. The host also owns the detail tab list, so Time tracking can add a tab later
exactly as Energy does on the customer page — the Tasks tab is one entry in that same
list, gated on nothing but seeing the project. Spotlight has **Create project** and
**Create task** quick actions and a Projects result group searching by code or name;
the dashboard has a Projects card, the `newProjects` metric and the overdue-project
attention items.

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
