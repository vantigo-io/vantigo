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

Six permission keys, all delegable, in the category **Projects**:

| Key | Meaning | Sensitive |
| --- | --- | --- |
| `projects:access` | Use the Projects app and see the projects you hold a role in. **Required by every operation.** | no |
| `projects:create` | Create projects. | no |
| `projects:view-all` | See every project, not only the ones you hold a role in. | no |
| `projects:manage-all` | Manage every project, which also means seeing it and its financial fields. | yes |
| `projects:view-financials` | See fixed prices, budget amounts and line pricing on every project you can see. | yes |
| `projects:view-costs` | See what the work costs the company and the margin, on projects whose financials you can see. On a small project this can reveal a person's cost rate. | yes |

`projects:view-costs` is in **no default role** — the three built-in roles are seeded
with no permission keys at all (migration `00002_identity_baseline.sql`), which is
what every new permission starts as without needing an opt-out. It widens nothing on
its own: holding it adds the cost and margin block only to a project whose money the
caller can already see through some other right, and it is deliberately **not**
implied by `projects:manage-all` or by being a project's manager — seeing what the
company pays its people is not part of running a project. See
[Project economy](#project-economy).

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

Every project response also carries `capabilities` (`canManage`, `canContribute`,
`canSeeFinancials`, `canManageMilestones`, `canSeeCosts`) and `billingLinesAvailable`,
so the frontend never re-derives authorization. `canManageMilestones` is true for the
project's managers and `projects:manage-all`, and false for a `projects:view-financials`
holder who is not one — see [Billing milestones and the invoice plan](#billing-milestones-and-the-invoice-plan).
`canSeeCosts` is the permission half of [Project economy](#project-economy)'s cost
block alone (financial rights on the project **and** `projects:view-costs`); a
surface should still draw the block from its presence in the response, not from this
flag, since the block is also absent without a currency or without time tracking.

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

**A catalog that *errors* never fails a line's rendering.** "The catalog could not be
read" and "the catalog no longer knows this variant" are different answers, and only
the second is an answer at all. When a catalog call made while building a response
fails, the affected lines come back with `catalogUnavailable: true` — and with
everything the catalog *did* supply still in place. Whatever the line itself stores
(code, `variantId`, `active`, budgets and the pricing rule) is unaffected either way,
and one warning is logged for the whole request rather than one per line. This holds
for the list read and for the line a create or a change answers with. It closes a real
hole: deactivating a line while products was degraded used to be *written*, committed,
and then answered 500 by the renderer, so the client retried a change that had already
been made and got a 409 for it.

`catalogUnavailable` means **"at least one catalog-derived field on this line is
missing because the catalog could not be read"** — not "none of them is here". The two
catalog-derived parts of a line are resolved by different calls and fail
independently: the names (`productName`, `sku`, `unit`) come from one lookup for the
whole list, and `pricing.listPrice` from one per line. So:

| names | list price | result |
|---|---|---|
| ok | ok | `catalogUnavailable: false`; `variantMissing` is whatever the catalog said |
| ok | failed | flag `true`, **names still present**, only `listPrice` absent; `variantMissing` still trustworthy and may be `true` |
| failed | either | flag `true`, names absent, `variantMissing: false` |

`variantMissing` is `true` **only** when the names lookup itself succeeded and
answered that the variant is gone. It is therefore reliable whenever it is true — it
is never a guess — and it may legitimately coexist with `catalogUnavailable`. When the
names lookup is the thing that failed, `variantMissing` is `false`, because nobody was
asked. A caller with no financial rights, or a project with no currency, is never
asked for a list price at all, so for them the flag can only ever come from the names.

A client therefore shows the product name whenever `productName` is present, falls
back to "product details unavailable" only when `catalogUnavailable` is true *and*
`productName` is absent, and shows the variant-missing badge whenever `variantMissing`
is true.

The rule stops at rendering. A catalog error while **validating** a variant the
request is actually moving the line to is still a 5xx: degrading an answer is honest,
degrading a decision is not, and a write must not proceed on a question nobody
answered. Products being *absent* (the module disabled, `Deps.Products` nil) is
unchanged and unrelated — that is still a 409 on every billing-line operation.

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
`budgetHours` carry the same bounds, for the same reason.

**At most two decimals, everywhere.** Every decimal column in this module is scale 2
— `numeric(12,2)` for money, `numeric(10,2)` for hours, `numeric(5,2)` for a percent
— so a third decimal is a digit the database would round away without saying so, and
a project or a line priced at something the caller did not type is worse than a
refusal. A milestone's `amount` and `percent` have always refused it; the project's
`fixedPriceAmount`, `budgetAmount`, `defaultBillRate` and `budgetHours` and a line's
`fixedAmount`, `discountPercent`, `budgetAmount` and `budgetHours` now do too, as a
400 on the field ("A budget amount cannot have more than two decimals"). Two decimals
and whole numbers are unaffected: the rule is about precision the column cannot keep.

`budgetHours` is planning data — visible with the line to everyone who sees the
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

## Project economy

The Economy tab's second half: a project's budget against what has actually been
logged on it, what its expenses cost and will bill, an across-project portfolio for
whoever is responsible for several, and dashboard signals for both. Projects owns
this view end to end; the hours come from Time tracking and the expenses from
Expenses, each through one optional contract — see
[The optional actuals dependency](#the-optional-actuals-dependency) and
[The optional expenses dependency](#the-optional-expenses-dependency) below.
**Reads take no lock at all**: `GET /{id}/economy`, the portfolio and the
dashboard's budget alerts call each contract at most once per request,
outside any transaction, so nothing another writer wants is ever held while another
module's pool is waited on.

**Expenses are never work.** Nothing the expenses contract reports reaches
`budgetUsed`, `overBudget`, a line's `usedPercent`, the per-line table, the
dashboard's budget alerts or the logged work any of those is computed from: a
receipt is not hours measured against a budget, and a project that is inside its
budget stays inside it however much it has spent on travel. Expenses appear in
exactly three places — the `expenses` block, the margin, and what is ready to
invoice — and the dashboard's budget alerts never even ask the provider.

### Budget used

One definition, used by the project's own economy, by the portfolio's rows and by
the dashboard's budget alerts alike, so no surface of this feature can disagree with
another about how much of a budget has been used:

1. The project's **budget amount**, if a caller who may see amounts is asking and it
   is set.
2. Else the project's **fixed price**, on a fixed-price project, same condition.
3. Else the project's **budget hours** — the one basis a caller without financial
   rights on the project ever gets, and the one every caller gets when nothing above
   applies.

An amount basis compares the three buckets' bill amount against it; the hours basis
compares their hours. **No basis at all** means `budgetUsed` is **absent** — not a
percentage of 0 — and such projects sort last wherever the portfolio orders by it.
That covers nothing being set, the caller not seeing amounts with no budget hours
either, and also the number that would otherwise apply being exactly **zero**: a
basis must be strictly greater than zero to count. The zero case is not reachable
through today's API — both the project's own validation and a billing line's refuse
a zero budget — so this is a completeness note rather than a behaviour anyone can
trigger.

The ratio used ÷ basis is kept and compared **exactly** (`math/big.Rat`, never a
float), and `overBudget` is decided on that exact ratio (`> 1`) — a project at
100.04 % is over budget even though its printed percentage rounds to 100.0. The
printed `percent` and `approvedPercent` (the approved bucket alone against the same
basis) are the same ratio rounded **half up to one decimal**, for display only; a
comparison must never be made against them. The dashboard's own thresholds —
**warning** at `80 % ≤ ratio ≤ 100 %`, **exceeded** past `100 %` — are decided the
same exact way, so a project sitting on precisely 100 % is a warning, not an
exceedance, whatever its rounded percentage happens to print.

### The three buckets, unpriced hours and uncosted hours

Every actual is the same three buckets Time tracking's own entry statuses fold into
(`approved` — approved and invoiced entries, `submitted`, `draft` — draft and
rejected entries; see [Time's state machine](time.md#the-state-machine)), with
figures that span all three:

- **`unpricedHours`** — **billable** hours with no bill amount in the project's
  currency: no bill rate, or a rate in another currency. They count in hours and in
  no amount. Non-billable hours are never unpriced — they were never meant to carry a
  price, and they are in `nonBillableHours`; a surface that told somebody to "add the
  missing rate" for them would be sending them after a rate that must not exist. It
  is not unconditionally the figure Time tracking's own project summary shows: that
  summary covers only submitted, approved and invoiced work, so a billable draft with
  no rate is counted here and not there, and it infers a currency for a project that
  carries none where this reports no amounts at all. See
  [what Time reports to other modules](time.md#what-time-reports-to-other-modules).
- **`uncostedHours`** (inside the `cost` block only) — hours, billable or not, whose
  cost is not counted: no cost rate, or a cost rate in another currency. `margin` is
  short by exactly what these hours would have cost, which is why the block always
  states how many there are rather than presenting a margin as complete.

### What the expenses cost and bill

`expenses` is the same idea for money somebody spent: the three buckets
(`approved`, `submitted`, `draft`, each `{count, cost, amount}`), the two
across-bucket totals, and the five invoicing figures. The bucket a line falls in is
its **unit's** status — a travel claim's line is judged through the claim somebody
approved or sent back — so `approved` carries the lines already invoiced (invoicing
is a stamp here, not a status) and `draft` carries the rejected ones, exactly as the
three buckets of logged work do. `cost` is the **net**, the gross less the VAT,
whoever paid; `amount` is what the *billable* lines will charge.

- **`unpricedCount`** — billable lines, in any bucket, carrying no bill amount:
  billable mileage with no customer rate, an outlay nobody has priced yet. Per diem
  days are out of it — they are never billable, so they are not a price somebody
  forgot to enter. They are
  counted and are in **no amount**, because a missing price is not a price of
  nothing — a surface showing what the project will bill has to say how many lines
  the figure is short by, exactly as `uncostedHours` does for the margin.
- **`readyCount` / `readyAmount`** — what can go on an invoice today, on **five**
  clauses: the unit approved, the line billable, a bill amount present,
  `invoiced_at` not set, and **never a per diem day** — a subsistence allowance is
  the company's to pay and never the customer's to be charged, so it is not ready
  and never will be.
- **`invoicedCount` / `invoicedAmount`** — how much of it has already left the
  building.
- **`lastEntryDate`** — the most recently dated line **across every currency**, so
  on a project with a foreign receipt it may be the day of a line reported under
  `otherCurrencies`.

**The currency rule.** A line counts towards the project's own figures only when it
was recorded in the project's currency. Everything else is reported as what it is,
per currency, in `otherCurrencies` (`{currency, count, cost, amount, readyAmount}`)
— never converted, never dropped and never added to anything, because a sum across
currencies is a number in neither. A project that carries **no currency** therefore
has no figures of its own at all: the ten own-currency figures are absent together
and every currency is in `otherCurrencies`. `otherCurrencies` itself is absent when
empty.

**Tracked with nothing recorded is zeroes.** The provider leaves a project with
nothing recorded out of its answer entirely, and "no key" means "nothing recorded":
the block is then present with zero counts and zero amounts, exactly as `actuals` is
present with zeroes for a project nobody has logged against. Only a missing
*module* makes the block absent, and `expenseTracking: false` is how that is said.

### The margin

`margin` (inside the `cost` block) is what is left over once both halves are
counted:

```
margin = (value of the work + what the expenses will bill)
       − (what the work cost + what the expenses cost)
```

Every term is the subject's own **across-bucket total** — approved, submitted and
draft together, the basis the labour half has always used — never the approved
bucket alone and never two different bases mixed; what is approved and what is not
is shown by the buckets themselves (`actuals.approved/submitted/draft` and
`expenses.approved/submitted/draft`). All four terms are in the project's own
currency: **four across-bucket totals, each rounded once by the module that owns
it**, added **exactly** in decimal, and **published once**. The addition itself
never rounds — which is the claim worth making, and the only one that is literally
true. (With today's providers the four totals already arrive at two decimals, so
nothing is lost either way; the arithmetic is exact because a provider whose totals
carry more places must not have them silently dropped.)

That is why `cost.expenseCost` exists beside `cost.total`: it lets a surface show
the two halves of the cost without subtracting them from the bill itself, which
would go a cent out whenever a provider's own total was rounded on the way. The
published `margin` is not those figures recombined — it adds the same four totals
exactly and publishes the result once. With `expenseTracking: false` there is no
`expenseCost` and the margin is exactly the one it has always been.

### Per-line rows

`lines` carries one row per billing line the project has, **inactive lines
included**, in the same order the Billing tab lists them — a line switched off last
month still has hours that were measured against its budget. A line nothing has been
logged against still gets a row, all zeroes: the provider only knows what was
logged, the project knows which lines exist. One further row, without a
`billingLineId`, appears **only when something was actually logged without a
line** — an empty one would read as a line somebody created. Work the provider
attributes to a billing line id this project does not have (which should never
happen) folds into that same no-line row rather than being dropped, because an hour
somebody logged must appear somewhere.

### Shaping — who sees what

| | Hours | Amounts (budget, bucket, line, milestone totals, currency) | The `expenses` block | Cost and margin |
| --- | --- | --- | --- | --- |
| Anyone who sees the project | ✓ | – | – | – |
| Financial rights on the project (manager, `manage-all`, or `view-financials` on a project they can see) | ✓ | ✓ | ✓ | – |
| The above **and** `projects:view-costs` | ✓ | ✓ | ✓ | ✓ |

**No `expenses:access` is needed, and none is checked.** Whoever has financial
rights on a project sees its expense aggregates here and on the portfolio without
holding a single Expenses permission — which is also why the Economy tab's link to
the Expenses tab is filled in only for a caller who holds `expenses:access`, and the
figures stand there without one — exactly as they have always seen Time's
hours and amounts without `time:access`. The aggregate is *the project's money*,
which is what financial rights on the project are rights to; `expenses:access` is
the right to use the Expenses app, where the same caller is answered 403 for the
module's own endpoints. `internal/projects` names neither permission anywhere.

**Expense cost is not labour cost.** The whole `expenses` block, `cost` figures
included, needs financial rights and *not* `projects:view-costs`: what a receipt
cost the company is what somebody paid a supplier, and nothing in it can be divided
by somebody's hours to recover their rate. The **margin** is the other way round —
it contains the labour cost, so it stays inside the `cost` block behind the
permission. A caller without financial rights is not asked about at all: the
provider is never called for them, the way the invoice plan is not read for them.

`unpricedHours` is on the hours side of that table, and it is worth saying out loud:
it is an **hours** figure, so everyone who can see the project gets it, although Time
tracking keeps its own copy inside the project summary's `billing` block behind
financial rights. It is the one figure the economy read gives a project member that
Time's own API would not — deliberately, because "some of these hours carry no rate"
is a fact about the work rather than about its price.

Two more things about this endpoint's reach. It is gated on **`projects:access`**,
not `time:access`, so a caller with a project role and no access to Time tracking at
all still gets the project's aggregate hours here; that is [E6](#project-economy)'s
point — Projects owns this view. And it exposes **no per-person and no per-entry
data** of any kind, while Time's own project summary already gives project members
per-status, per-line *and per-person* hours, so the economy read is a strict subset
of it in every respect but the one above.

The **whole Amounts column needs a currency too**, not only cost: `seesAmounts` is
financial rights **and** the project carrying a currency (`economy.go`'s
`CanSeeFinancials && project.Currency != nil`), so a currency-less project is an
hours-only answer for *everybody*, financial rights or not — no `currency`,
`budget.amount`, `budget.fixedPrice`, `budget.linesAmount`, bucket amounts,
`actuals.totalAmount` or line `budgetAmount`. The `cost` block additionally needs
`timeTracking` to be on, on top of that same currency requirement — a cost in no
currency is a number nobody can read either way.
`ProjectCapabilities.canSeeCosts` answers the permission half alone; a surface should
*offer* the cost view from that flag and *draw* it from the `cost` block itself, since
the block is also absent without a currency or without time tracking even when the
flag is true. Nothing a caller may not see is ever `null` or `0` — it is absent, the
module's rule everywhere else. An outsider gets the same bare **404** as everywhere
else in this module, and there is no dedicated 403: a caller who may see the project
but not its money is shown the hours-only half rather than refused.

### Without Time tracking, without Expenses

The two modules are **independent slots**, and all four combinations are real
installations. `timeTracking` and `expenseTracking` are both required booleans on
the per-project economy and on the portfolio, each `true` exactly when its contract
is composed — a fact about the installation, never about the caller, so a member
whose answer carries neither block still sees both flags.

When `expenses` is not enabled, `Deps.Expenses` is nil and `expenseTracking` is
`false`: there is no `expenses` block, no `cost.expenseCost`, the margin is the
labour one alone, and no portfolio row or total carries an expense figure. Every
other figure is byte-identical to what it was before the module existed — pinned by
a golden-body test on both endpoints.

When `time` is not enabled, `Deps.Actuals` is nil and `timeTracking` is `false`:
the budget, the per-line budgets, the task estimate total and the milestone totals
are all still there, but there is nothing to compare them with — no `actuals` on the
project or on any line, no `budgetUsed`, no `cost`, and no `usedPercent` or
`remainingHours` on a line. This is a different answer from "nothing has been
logged": a project this installation cannot see the hours of and a project nobody
has touched are not the same claim, so the response never fakes the second to avoid
admitting the first. The `cost` block needs `timeTracking` on either way — it is
the labour cost block, and `expenses.totalCost` is where an expenses-only
installation reads what its receipts cost.

### When the provider fails

A failing call into Time tracking's actuals contract is a wrong answer waiting to
happen — a budget compared against zeroes — so it is a **500 problem, never zeroes**,
everywhere except one place: `GET /stats/attention`. There, the two budget alert
types are dropped and a warning is logged (`projects: the dashboard's budget alerts
were left out`), while the other three kinds of attention item — the overdue-project,
`milestoneReady` and `milestoneOverdue` items, none of which need the provider — are
still returned. That endpoint is one of six the dashboard merges into one list, and
emptying the whole thing over one degraded module would hide everything else worth
looking at; nowhere else in this feature does that trade-off apply, because a
comparison is the very thing being asked for. `GET /stats/summary`'s `readyMilestones`
never touches the provider at all — it is a plain count of `ready` milestones, no
comparison involved.

### The portfolio

`GET /api/v1/projects/economy` (`projects:access`) lists **every project whose
money the caller has financial rights on** — the project's manager, `manage-all`, or
`view-financials` on a project they can see — and nothing else: a caller who may not
see a project's money gets no row for it at all, not a shaped-down one. There is no
`cost` in a portfolio row; that block stays behind `projects:view-costs` on the
per-project read.

- **Filters**: `status` (defaults to `active` when omitted **or empty** — unlike
  `GET /projects`, where an empty value filters nothing at all, so a shared status
  control has to send `status=all` explicitly to clear this one), `customerId`,
  `search` (project code or name, case-insensitive), `overBudget=true` and
  `hasReady=true` (both "keep only"; `false` and absent mean the same thing).
- **Sorts**: `budgetUsed` (default, most-used first on the exact ratio, no-basis rows
  last), `readyAmount` (**by currency code first, then the largest amount** —
  amounts in different currencies are not comparable, so a mixed portfolio is
  grouped by currency rather than interleaved — **with rows that have nothing
  priced and ready last, whatever their currency**), `nextMilestone` (soonest
  planned date first, undated open milestones after dated ones, projects with
  nothing open last), `code`. Every order breaks its ties by project code, which is
  unique, so a page is the same page however many times it is turned to.
- **The cap.** More than 2 000 matching projects (the actuals contract's own batch
  limit, which the expenses contract's `MaxExpensesProjects` deliberately equals, so
  one cap covers both batches) is a **400** naming `status` and asking to narrow with
  `status`, `customerId` or `search`, rather than answering a partial portfolio — a
  total over part of a filtered set is a wrong number, not a missing one. The
  dashboard's budget alerts hit the same cap and answer differently — see
  [The dashboard signals](#the-dashboard-signals).
- **Totals are taken over the whole filtered set, before the page is cut** —
  project count, over-budget count, ready count, ready expense count, the count of
  projects with something ready in another currency, and `readyAmounts` (one entry
  per currency, by currency code) — so paging never changes the headline figures. Rows are shaped once per read; customer names are
  resolved for the **page only**, once per distinct customer on it.

**Ready to invoice, both halves.** A row's `readyAmount` and `readyCount` keep
meaning **milestones**. Beside them, `readyExpenseCount` and `readyExpenseAmount`
are the project's ready expense lines **in its own currency only** — a portfolio row
is one line of a table, so another currency is not folded in and no per-row currency
list is offered; that is the per-project read's business — and `readyTotalAmount` is
the two halves together, which is what the `readyAmount` **sort** orders by and what
a project whose receipts outweigh another's milestone is ranked on.

**What is ready in another currency is flagged, never dropped.** A line whose
currency is not the project's — and *every* ready line of a project that carries no
currency at all — is in none of those figures, so the row carries
**`readyExpenseOtherCurrency`**: present and `true` when there is such money, absent
otherwise, and never `false`. It carries **no amount on purpose**: a row cannot hold
a second currency without inviting somebody to add two figures that do not add up.
The amounts are on the project's own Economy tab, under `expenses.otherCurrencies`,
per currency and never converted. `hasReady=true` **keeps a flagged row** even though
its `readyExpenseCount` is `0` — the filter asks "is there anything to invoice here",
and there is. The UI reads the flag as one dimmed line under the figures ("More ready
in another currency") and the project's own code beside it is the way to the amounts.

In the totals, each `readyAmounts` entry carries `amount` (milestones),
`expenseAmount` and `totalAmount`, each rounded once from the exact sum across rows,
and a currency appears when **either** half has something in it — a currency in the
list only for its expenses reports `amount: 0`, which is a sum over no milestones
rather than a missing figure. Beside them **`readyExpenseOtherCurrencyCount`** counts
**projects, not lines**: how many of them have something ready in a currency that is
not their own. Projects is the only honest unit — amounts in different currencies do
not add up, and a count of lines would invite a headline figure that mixes them.

All **eight** of those fields — the row's `readyExpenseCount`, `readyExpenseAmount`,
`readyTotalAmount` and `readyExpenseOtherCurrency`, the totals'
`readyExpenseCount` and `readyExpenseOtherCurrencyCount`, and each `readyAmounts`
entry's `expenseAmount` and `totalAmount` — are absent when `expenseTracking` is
`false`.

### The dashboard signals

`GET /stats/summary` gains `readyMilestones`: how many `ready` milestones sit on
projects whose money the caller has financial rights on, **cancelled and completed
projects left out** — their invoicing is over, and a card saying "4 ready to invoice"
above an attention list offering 2 would be two numbers for one question. It counts
exactly the milestones `milestoneReady` below lists. It is a count, not an amount
(the milestones may be in several currencies), a state now rather than a figure over
the period like `activeProjects`, and it has no delta for the same reason a
currency-mixed amount has no meaning.

**The dashboard and the portfolio mean different things by "ready", deliberately.**
`readyMilestones` and the `milestoneReady` attention items count ready
**milestones** and nothing else, while the portfolio's `readyTotalAmount` and its
KPI count **both halves**. The reason is the rule right above: the stats endpoints
never call the expenses contract at all — nothing in them may, since a budget alert
that counted receipts would be X12 broken — and a count that asked a provider would
also have to answer for that provider being down, which an attention list merged
from six modules must never do. So the dashboard says "there are milestones to
invoice" and the portfolio says "here is everything there is to invoice"; whoever
invoices works from the portfolio.

`GET /stats/attention` gains four types, alongside the existing `projectOverdue`:

| Type | Raised for | Recipients | `entityId` | `occurredAt` |
| --- | --- | --- | --- | --- |
| `budgetWarning` | `80 % ≤` used `≤ 100 %` (exact ratio) on an **active** project | the project's **manager role** holders | the project id | the day work was last logged, midnight UTC, clamped to never be in the future; else now |
| `budgetExceeded` | used `> 100 %` on an **active** project | same | same | same |
| `milestoneReady` | a `ready` milestone on a project that is not cancelled or completed | anyone with **financial rights** on the project | `<projectId>/<milestoneId>` | the milestone's `ready_at`, falling back to `updated_at` |
| `milestoneOverdue` | a `planned` milestone whose planned date has passed, on an **active** project | the project's **manager role** holders | `<projectId>/<milestoneId>` | the planned date, midnight UTC |

The two budget types and `milestoneOverdue` go to the project's **manager role**
specifically, never to a holder of `projects:manage-all` who is not also a manager —
otherwise an administrator who can manage every project would be sent every
project's alerts and would read none of them. `milestoneReady` goes the other way,
to anyone with financial rights, because whoever may see a project's money is who
invoices it. A project never raises both budget types at once, because the exact
ratio falls in exactly one of the three bands (below 80 %, the warning band, or
exceeded).

`id` on these four types is `<type>:<entityId>` (unlike `projectOverdue`, whose id
stays the bare project id) — a project raising more than one kind at once would
otherwise be one row on the dashboard's merged list, keyed as it is on module and
id. The host builds the link from `entityId` (`/projects/<projectId>/economy` for
all four, the two milestone types split on `/` for the project id rather than
URL-encoded whole) and a translated sentence naming the project or the milestone,
exactly as it already does for Time's own attention items.

**The budget alerts hit the portfolio's own 2 000-project cap, and answer
differently when they do.** Where the portfolio refuses with a 400 past the cap —
a total over part of a filtered set would be a wrong number — the two budget alert
types instead **keep the first 2 000 projects the caller manages and log a
warning** naming the caller and the cap: an attention list totals nothing and offers
no filter to narrow it, so dropping every alert over one truncation would be worse
than showing an incomplete one. The other three kinds of item are unaffected.

### The optional actuals dependency

Everything above depends on **`contracts.ProjectActuals`**, the one contract Time
tracking provides and Projects optionally consumes — the mirror image of
[Products](#billing-lines-and-the-optional-products-dependency): there, Projects
consumes an optional contract from another module; here, Projects is still the
consumer, but the provider is the module that in every other respect *depends on*
Projects. `Compose` resolves both directions before any module mounts, so neither
ever calls the other over HTTP or reads the other's schema, and there is no cycle at
request time — see [module boundaries](module-boundaries.md). With `time` disabled,
`Deps.Actuals` is nil and this whole feature degrades to "budgets and plans, nothing
to compare them with" (`timeTracking: false`), never to zeroes.

### The optional expenses dependency

**`contracts.ProjectExpenses`** is the same seam again, with Expenses as the
provider: `ExpensesForProjects(ctx, projectIDs)` answers, per project and **per
currency**, what the project's expenses cost and bill. Where the actuals contract
takes the project's currency *in* on the request, this one reports every currency
and lets Projects — which owns the project's currency — decide which of them is the
project's and how to show the rest; nothing is ever converted, and the provider
never has to ask the project directory anything while serving.

Two things a consumer must not get wrong, both pinned by tests here:

- **A project with nothing recorded is absent from the map** — unlike
  `ActualsForProjects`, which answers a zero-valued entry for every project it was
  asked about. "No key" means "nothing recorded", so it becomes zeroes in the
  response; only a nil `Deps.Expenses` makes the block absent.
- **Never add buckets or currencies.** Each bucket is rounded on its own, so the
  `Total` the contract carries is the figure to publish and to compute from —
  three buckets of half a cent are `0.01` apiece and `0.02` altogether.

Both calls go through `projects/contracts.go`'s `expensesForProjects` accessor,
wrapped in `noteContractCall`, and neither is ever made under `withProjectLock` —
the harness-wide check fails the test if one ever is. A failing call is a **500** on
both endpoints, exactly as a failing actuals call is: a margin or a "ready to
invoice" column short by everything one module knows is a wrong number, not a
missing one.

**The two endpoints read the answer to different depths, deliberately.** The
per-project economy publishes every figure of every currency, so it parses every
one and refuses a figure it cannot read — including a `Total` whose line count
contradicts its own three buckets, the guard the actuals side has on its hours. The
portfolio publishes two figures per row (the ready count and amount, in the row's
own currency), so it reads exactly those two: at the 2 000-project cap that saves
tens of thousands of discarded decimal parses, and it means a malformed figure in a
currency the table would never have printed cannot take the whole page down with it.

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
project row, then a line's or a milestone's own row) — so nothing can deadlock.

`FOR NO KEY UPDATE` rather than `FOR UPDATE` on purpose. The weaker mode still
conflicts with itself and with the row lock the project's own `UPDATE` takes, so every
guarded writer still serialises against every other; what it does *not* conflict with
is the `FOR KEY SHARE` Postgres takes on the project row for each insert that
references it. Under `FOR UPDATE`, creating an unrelated task, role, comment, billing
line, milestone or timeline entry on the same project would wait behind any guarded
write. The one edit that does change a key is a new project code (it is unique): that
update runs in the transaction already holding the lock, which Postgres upgrades in
place — still mutually exclusive, just not cheaper for that one edit.

A cross-module call — asking the product catalog whether a variant exists, naming the
caller through the user directory, resolving a customer — is always made **before**
the project's lock is taken, never inside the locked transaction: these are in-process
calls into another module that read through the same connection pool, so a transaction
that holds row locks and then waits for one of them stalls every other writer of the
project, and under enough load starves the pool outright. The transaction then only
decides whether that prefetched answer matters, against the row its own lock returns.

**This is enforced for the whole module, not documented and hoped for.** Every guarded
write goes through one helper, `withProjectLock`, which takes the lock as its first
statement and hands the body a context marked as holding it; and every call this
module makes into `Deps.Products`, `Deps.Users`, `Deps.Directory` or `Deps.Actuals`
goes through a thin accessor in `contracts.go` and through nowhere else. The
accessors report each call to a hook that is nil in production — one nil comparison,
no behaviour change — and that the test suite installs for every test, failing it when
the context is a marked one. A new write path, or a new cross-module call on an
existing one, is covered the moment it is written rather than when someone remembers
to add a probe for it. One such probe remains beside the billing-line tests, checking
the complementary thing the mark cannot: that Postgres really is holding the row at
the moment of the call.

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
HTTP endpoints. Four contracts meet here; all are DTOs only, and for all of them a
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
  (every line on a project, active and inactive), `ProjectsForUser`,
  `ProjectsForCustomer` (every project billed to one customer, in any status, by id
  ascending and at most `contracts.MaxActualsRequests` — the batch a consumer asks
  the time and expenses providers about next; the customer page's
  [Customer 360](customers.md#customer-360) is its reader), `Task`,
  `OpenTasksForUser` and `CanLogTime(projectID, userID)`. **Cancelled projects and
  inactive lines still resolve**, so a consumer can read old work. `ProjectEntry`
  carries `Currency` and `DefaultBillRate`, which are financial fields: only a
  consumer that gates on a financial-viewer permission of its own should surface
  them. `TaskEntry` is deliberately thin — id, project, title, status, assignee and
  due date — enough to name a task on a timesheet row, never enough to manage one.
- **`contracts.ProjectActuals`** — provided by *time*, **nil when time is disabled**
  (the second optional contract, and the first of the two Projects *consumes*
  rather than provides). `Actuals(projectID, currency)` and `ActualsForProjects`
  (batch, capped at `contracts.MaxActualsRequests`). It performs no authorization —
  Projects has already decided who may see the project and its money — and reports
  hours in three buckets (approved, submitted, draft) — plus `Invoiced`, the part of
  approved already billed, which Projects' economy does not read — and bill and cost amounts,
  each counted only when logged in the currency Projects asked for. See
  [Project economy](#project-economy) and [what Time reports](time.md#what-time-reports-to-other-modules).
- **`contracts.ProjectExpenses`** — provided by *expenses*, **nil when expenses is
  disabled** (the third optional contract, and the second Projects consumes).
  `ExpensesForProjects(projectIDs)` (batch only, capped at
  `contracts.MaxExpensesProjects`, which is `MaxActualsRequests`). It performs no
  authorization either, and reports **per currency** rather than in a currency
  Projects asks for — an expense carries its own currency per line. A project with
  nothing recorded is **absent from its map**, which is the one way it differs from
  the actuals contract. See
  [The optional expenses dependency](#the-optional-expenses-dependency) and
  [expenses](expenses.md).

On the platform side, `module.Module` has provider fields `Users`, `Products`,
`Projects`, `Actuals` and `Expenses` beside `Directory`, and `module.Deps` has the
matching consumer fields. `Compose` resolves **at most one enabled provider per
slot** — two providers is a startup failure naming both — and builds them before any
`Mount` runs, `Actuals` and `Expenses` last of all so their providers' constructors
may themselves read the project directory. An *optional* provider whose module is
disabled simply leaves its `Deps` field nil, and the consumer must handle that (see
the 409 above, and `timeTracking: false` / `expenseTracking: false` in
[Project economy](#project-economy)). `modtest` has matching options so a module
test can run against a fake or a real provider.

Projects also **holds customer references** — `contracts.CustomerReferenceHolder`,
the one sanctioned cross-module write ([module boundaries rule 8](module-boundaries.md#the-rules)).
When the customers module merges two customers, `RepointProjectsCustomer` moves every
project of the absorbed customer to the survivor inside the merge's own transaction,
advancing each moved project's revision like any other change to the row, so an edit
form still holding the old customer answers the stale-revision 409. No project
timeline entry is written: the merge is recorded on the survivor's customer timeline,
and `customerName` reads the survivor's from then on. Time and expenses hold no
customer id of their own, so the move keeps them right too. See
[Merging duplicates](customers.md#merging-duplicates).

It hands a private person's projects over too — `contracts.CustomerPersonalData`
([module boundaries rule 9](module-boundaries.md#the-rules)): code, name, status and
dates of every project billed to them, in their export's `modules.projects`. Their
anonymisation keeps every project: invoiced work stays, a project stores no customer
name to blank, and `customerName` reads the anonymised customer's through the
directory from then on. A project named after the person is free text this module
does not rewrite. See [Personal data and anonymisation](customers.md#personal-data-and-anonymisation).

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
  (billing-line rule → project default → customer default → person default). It is
  a financial field — surface it only behind a financial-viewer permission of your
  own.
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
| `GET /api/v1/projects/{id}/economy` | Budget vs. logged, per line and in total, plus what the expenses cost and will bill; hours for anyone who sees the project, amounts and the `expenses` block need financial rights, cost and margin need `projects:view-costs` too — see [Project economy](#project-economy) |
| `GET /api/v1/projects/economy` | The portfolio: one row per project the caller has financial rights on. `projects:access`; paged, filtered and sorted — see [Project economy](#project-economy) |
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
| `GET /api/v1/projects/stats/summary` | `newProjects` and `activeProjects` over a period, with deltas, plus `readyMilestones` (financial rights) |
| `GET /api/v1/projects/stats/timeseries` | Daily buckets for one metric |
| `GET /api/v1/projects/stats/attention` | Active projects past their end date, plus the four economy signals — see [The dashboard signals](#the-dashboard-signals) |

All four stats endpoints respect visibility: a member's numbers cover their
projects. The economy figures inside them narrow that further — `readyMilestones`
and `milestoneReady` to financial rights, the two budget types and
`milestoneOverdue` to the manager role specifically — see
[The dashboard signals](#the-dashboard-signals).

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
| `/projects/$projectId/economy` | Economy tab (invoice plan and budget vs. logged), between Billing and Time; shown to everyone who sees the project — unlike Billing, it carries no capability gate, because it has an hours-only half for a caller without financial rights |
| `/projects/$projectId/expenses` | **Expenses** tab, from `@vantigo/expenses-ui`; last, after Time, and only when the expenses module is mounted and the caller holds `expenses:access`. It carries no project capability: a plain member sees their own expenses on the project, and the expenses API decides who reads the totals above the list |
| `/projects/economy` | The economy portfolio, one row per project the caller has financial rights on; sidebar entry "Project economy" behind `projects:access` (the page's own empty state covers a caller with nothing to see) |
| `/customers/$customerId/projects` | Projects tab on the customer page |

The project header — code, name, customer, status badge and the status control a
manager changes it with — is `ProjectDetailHeader`, above the tab row, so it stands on
every tab of the detail page rather than on the Overview tab alone.

The app is registered in `apps/host/frontend/src/apps.ts` and shows in the switcher
for anyone with `projects:access`, greying out with "Not enabled" when the module is
off. The host also owns the detail tab list, which is how Time and Expenses each add
a tab exactly as Energy does on the customer page — the Tasks and Economy tabs are
two entries in that same list, gated on nothing but seeing the project.

**What the Economy tab shows of the expenses.** Under the budget, a **Costs**
section: the project's recorded expenses in three labelled rows — approved,
submitted and awaiting approval, and draft (which is where a rejected one sits) —
each with how many lines it holds, what they cost the company and what of them is
passed on to the customer, over a total the server sends rather than the three rows
added up. Under the table, what the figures leave out: billable lines nobody has
priced yet, and one line per currency the project is not in, written in **that**
currency and never converted. The section says in so many words that none of it is
measured against the budget ([X12](#project-economy)), because it sits directly under
"Budget used". Where the caller may also see costs, the **margin** now counts the
expenses and says so, with the labour and expense halves of its cost side beside it.
The invoice plan gains one row — the billable expenses ready to invoice, with their
count and amount. An installation without the expenses module shows **none** of this,
not a zero anywhere. On the **portfolio**, the ready column is what is ready
altogether, with the milestone and expense halves named under it when there is one of
each, and a line saying when more is ready in a currency the row cannot report; the
ready card counts milestones and expense lines as two labelled numbers, gives each
currency's total, and says how many projects have money waiting in another currency.

The Economy tab's **costs** section and its "billable expenses ready to invoice" row
are the project's own reading of what Expenses reports; the Expenses tab beside it is
the same money from the other module's side, with the individual expenses the caller
may open and a **Record a cost** button. The link between them is the host's:
`ProjectEconomy` takes an optional `expensesHref`, which the host fills in with
`/projects/{id}/expenses` **only when the Expenses tab itself is open to the
caller** — the module mounted *and* `expenses:access` — from
`visibleProjectDetailTabs`, the same function the tab row is built from, so the link
and the tab can never give two different answers. A manager who may see the figures
but does not use the Expenses app (see
[Shaping — who sees what](#shaping--who-sees-what): no `expenses:access` is needed
for the figures) therefore reads the row as plain text with no link, which is the
right answer — the page it would lead to would refuse them. This package knows no
route of the Expenses app and imports nothing from it. Both tabs read the
same figures, so the host refreshes the economy queries when something on the
Expenses tab changes them. See [docs/expenses.md](expenses.md#on-the-project-page). Spotlight has
**Create project** and **Create task** quick actions and a Projects result group
searching by code or name; the dashboard has a Projects card (the `newProjects`
metric and, once anything is ready to invoice, a `readyMilestones` hint), the
overdue-project attention items, and the four economy signals — see
[The dashboard signals](#the-dashboard-signals).

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
