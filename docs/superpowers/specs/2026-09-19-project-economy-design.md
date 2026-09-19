# Project economy — budgets, billing milestones and the portfolio

Projects phase 3, first delivery. Phased plan:
`2026-09-18-project-management-plan.md` (phase 3, decisions D7 and D10).
Builds on the projects module (`docs/projects.md`), tasks and the `time` module
(`docs/time.md`).

## 1. Purpose

Answer two questions for small contractors and consultancies: **how is this
project doing against its budget**, and **what do I invoice when**. Vantigo
still keeps no ledger: it plans, compares and tracks status; an accounting system
(or a later Invoices module) does the invoicing.

Two deliveries, two PRs:

- **A — Billing milestones and line budgets** (projects only): the invoice plan
  and the budget fields, the Economy tab's plan half.
- **B — Budget vs actual, portfolio and alerts**: a new contract through which
  Time supplies actuals to Projects, the Economy tab's budget half, the
  portfolio page and the dashboard signals. Builds on A.

Out of scope: forecast / estimate-to-complete, original vs revised budgets,
configurable thresholds, email notifications, expenses and supplier costs,
overtime multipliers, anything ledger-side (a-konto accounts, retention, WIP),
marking *time* as invoiced by hand.

## 2. Decisions

**E1 — Milestones are free-form, informed not ruled.** Any project that may
carry amounts (it has a currency) can have billing milestones, whatever its
billing type. A milestone is a name, an optional planned date, an amount and a
status. On a project with a fixed price a milestone may instead be a percent of
that price. The plan reports what is planned, ready, invoiced and — against a
fixed price — unplanned or over-planned. Totals never block a save.

**E2 — Actual is everything logged, always shown in three buckets.**
`approved` (approved and invoiced entries), `submitted`, `draft` (draft and
rejected entries). Every surface that shows an actual shows the split; alerts
compare the sum with the budget and say how much is approved.

**E3 — Budgets per project and, optionally, per billing line.** The project's
budget and the lines' budgets are independent numbers; when both exist their
relation is shown ("lines add up to 360 h of the project's 400 h"), never
enforced. The sum of task estimates is shown as a secondary figure.

**E4 — Budget amount is the budgeted value of the work**: hours × bill rate. It
is compared with the bill amount of logged time. Cost and margin are a separate
view behind their own permission (E7).

**E5 — Manual "mark as invoiced" now; Invoices later.** `planned → ready →
invoiced`, plus `cancelled`. Marking invoiced takes an optional reference and
date and freezes the amount. A later Invoices module sets the same status; the
manual step stays for installations that invoice elsewhere.

**E6 — Projects owns the economy view; Time supplies actuals through an
optional contract** (`contracts.ProjectActuals`), the mirror of
`ProductCatalog`: resolved by `module.Compose` before any mount, absent when
`time` is disabled. Projects never reads the `time` schema; Time never learns
about budgets. A later expenses module becomes a second source for the same
view.

**E7 — Cost and margin need `projects:view-costs`.** On a small project total
cost ÷ hours reveals a person's cost rate. The permission is sensitive, granted
to no default role, not implied by being a project's manager nor by
`projects:manage-all`.

**E8 — One definition of "budget used".** Basis, in order: the project's budget
amount → the fixed price (fixed-price projects) → the project's budget hours.
Used = total (three buckets) bill amount ÷ amount basis, or total hours ÷ hours
basis. No basis → no percentage; such projects sort last. A caller who may not
see amounts only ever gets the hours basis.

**E9 — Nothing is cached.** Actuals are read live through the contract; alerts
are computed when the dashboard loads. No totals table, no job, no dismiss
state.

## 3. Data model (one migration per delivery, `projects` schema)

### 3.1 Line budgets (delivery A)

`projects.billing_lines` gains `budget_hours numeric(10,2)` and
`budget_amount numeric(12,2)`, both nullable, both `> 0` when set (validated in
Go like the project's). `budget_amount` requires the project to have a currency
and is financial data (absent without financial rights); `budget_hours` is
planning data, visible with the line.

### 3.2 Billing milestones (delivery A)

`projects.billing_milestones`:

| Column | Type | Notes |
|---|---|---|
| `id` | integer identity (from 1001) | |
| `project_id` | integer → `projects.projects` | |
| `name` | varchar(200) not null | trimmed, 1–200 |
| `description` | varchar(2000) | optional |
| `planned_date` | date | optional |
| `amount` | numeric(12,2) | exactly one of `amount` / `percent` |
| `amount_currency` | char(3) | set iff `amount` is; the currency the amount was entered in, so a cancelled milestone (exempt from the currency guard) cannot be silently redenominated |
| `percent` | numeric(5,2) | `0 < percent ≤ 100`; only while the project has a fixed price |
| `status` | varchar(20) not null default `planned` | `planned`, `ready`, `invoiced`, `cancelled` |
| `position` | integer not null | manual order within the project |
| `ready_at`, `ready_by_user_id` | timestamptz, uuid | set on → ready, cleared on ready → planned |
| `invoiced_at`, `invoiced_by_user_id` | timestamptz, uuid | set on → invoiced, cleared on undo |
| `invoice_reference` | varchar(100) | optional, only while invoiced |
| `invoice_date` | date | optional, only while invoiced |
| `invoiced_amount` | numeric(12,2) | frozen on → invoiced, cleared on undo |
| `ever_moved` | boolean not null default false | true after the first status change; gates delete |
| `revision` | integer not null default 1 | |
| `created_by_user_id`, `created_at`, `updated_at` | | |

Index `(project_id, position)`; partial index on `status IN ('planned','ready')`
with `planned_date` for the alert and portfolio reads.

**Effective amount**: `invoiced_amount` when invoiced; else `amount`; else
`fixed price × percent / 100`, exact decimal, rounded half-up to two places
(the rule `time` uses for discounts). Computed on read.

**Totals** are `planned`, `ready` and `invoiced` only. Cancelled milestones
bill nothing and may still carry an `amount_currency` the project has moved
off, so they are left out of the plan's arithmetic entirely; reopening such a
milestone is refused, naming both currencies.

**Status moves** (anything else is a 400 naming the move):

| From → to | Who |
|---|---|
| planned → ready, ready → planned | project manager, `projects:manage-all` |
| ready → invoiced, invoiced → ready (undo) | whoever has financial rights on the project |
| planned/ready → cancelled, cancelled → planned | project manager, `projects:manage-all` |

**A move re-asks the project's rules** against the locked project row. Any move
whose target is not `cancelled` is refused (400 on `status`) when the project has
no currency, or when the milestone is a percent and the project has no fixed
price — with one exception: undoing an invoiced percent milestone on a project
that no longer has a fixed price is never blocked (a credited invoice is a real
event); the milestone is converted to an amount equal to what was invoiced.
`capabilities` mirror the same function, so the API never offers a move it would
refuse.

Every move writes a project timeline entry (`milestone-ready`,
`milestone-planned`, `milestone-invoiced`, `milestone-invoice-undone` — flagged
when it converted —, `milestone-cancelled`, `milestone-reopened`; also
`milestone-added`, `milestone-removed`, and `milestone-changed` with the changed
field names for a content edit). Payloads carry names, never amounts; a reorder
writes nothing. Content edits (name, date, amount) are allowed while
`planned` or `ready`; an invoiced or cancelled milestone is read-only until
moved back. Delete only while `planned` and `ever_moved = false`.

Milestones do not block a project being completed or cancelled. A cancelled
milestone does not hold the currency or the fixed price in place, so it can
outlive them: `currency` and `effectiveAmount` are optional on the response and
absent exactly then. A row that cannot be priced never takes the plan down — it
renders without an amount and stays out of the totals.

### 3.3 Guards on the project

- Removing the fixed price (or changing billing type away from fixed price) is
  refused while non-cancelled, non-invoiced percent milestones exist — field
  error naming them. Changing the price is allowed; open percent milestones
  follow it.
- The existing "currency cannot change/clear while amounts exist" check also
  counts non-cancelled milestones and line budget amounts.
- **Locking.** Every transaction that changes a project's currency, fixed price
  or billing type, or writes a row whose validity depends on them (billing
  lines, milestones), locks the project row (`FOR NO KEY UPDATE`) first and
  decides under it; other modules' directories are asked before the lock is
  taken, never under it. `FOR NO KEY UPDATE` still conflicts with itself and
  with the project UPDATE's own row lock, so the guarantee is the same as
  `FOR UPDATE`'s, but it does not block the `FOR KEY SHARE` every insert that
  references the project takes. That lock also serialises milestone ordering,
  so milestones need no advisory lock of their own (tasks keep theirs).
- The fixed-price refusal lands on `billingType` — the only reachable way to
  leave a fixed price behind.

## 4. The actuals contract (delivery B)

```go
package contracts

// ProjectActuals is what has been logged against projects, as the module that
// owns the hours reports it. It performs no authorization.
type ProjectActuals interface {
    Actuals(ctx context.Context, req ActualsRequest) (ProjectActualsEntry, error)
    ActualsForProjects(ctx context.Context, reqs []ActualsRequest) (map[int32]ActualsTotals, error)
}

// ActualsRequest names a project and the currency its amounts are wanted in.
type ActualsRequest struct {
    ProjectID int32
    Currency  *string // nil: the project carries no amounts
}

type ActualsBucket struct {
    HoursHundredths int64  // exact
    BillAmount      string // decimal text, project currency only
    CostAmount      string // decimal text, project currency only
}

type ActualsTotals struct {
    Approved, Submitted, Draft ActualsBucket
    UnpricedHoursHundredths    int64   // no bill rate, or a rate in another currency
    BillableHoursHundredths    int64
    NonBillableHoursHundredths int64
    LastEntryDate              *string // YYYY-MM-DD
}

type ProjectActualsEntry struct {
    Totals ActualsTotals
    Lines  []LineActuals // BillingLineID nil = logged without a line
}

type LineActuals struct {
    BillingLineID *int32
    Totals        ActualsTotals
}
```

- Implemented in `time` from `time.entries` with grouped SQL in `numeric`; the
  batch call is one query. Both calls take the project's currency from the
  caller: Projects owns that fact, and Time must not call back into
  `ProjectDirectory` to serve Projects. An entry's amounts count
  when its currency equals the given one; with no currency given no amounts are
  summed and only entries without a rate are "unpriced".
- `module.Module` gains an `Actuals func(Deps) contracts.ProjectActuals` slot and
  `Deps.Actuals`; `Compose` resolves it after `Projects`, at most one provider.
- Projects treats a nil `Deps.Actuals` as "time tracking is off".
- A failing actuals call fails the request (500 problem), never zeros.
- depguard, `modtest` (a `WithActuals` fake) and the compose tests follow the
  `ProductCatalog` precedent.

## 5. API (all under `/api/v1/projects`, contract `openapi/projects.yaml`)

Conventions as in the module today: 404 for outsiders, revision-guarded writes
(409 on a stale revision), fields the caller may not see are **absent**.

### Delivery A

| Operation | Notes | Access |
|---|---|---|
| `GET /{id}/milestones` | the plan in `position` order plus totals: planned / ready / invoiced amounts, and against a fixed price `unplanned` or `overPlanned` | financial rights on the project |
| `POST /{id}/milestones` | appended last | manager, `projects:manage-all` |
| `GET /milestones/{milestoneId}` | | financial rights |
| `PUT /milestones/{milestoneId}` | full replace with `revision` | manager, `projects:manage-all` |
| `DELETE /milestones/{milestoneId}` | only `planned` and never moved; else 400 | manager, `projects:manage-all` |
| `PUT /milestones/{milestoneId}/position` | `{position, revision}`; checks the revision, does not bump it (a reorder must not stale every open form) | manager, `projects:manage-all` |
| `POST /milestones/{milestoneId}/status` | `{status, revision, invoiceReference?, invoiceDate?}` | per the move table |
| billing line create/update/read | gain `budgetHours`, `budgetAmount` (the latter inside the line's financial shaping) | unchanged |

Each milestone response carries `capabilities` (`canEdit`, `canDelete`,
`canMarkReady`, `canMarkInvoiced`, `canUndoInvoiced`, `canCancel`, `canReopen`)
so the UI never re-derives the rules. The project response's `capabilities`
gains `canManageMilestones`.

Access is a ladder: an outsider gets the bare 404 of an unknown id; a caller who
sees the project without financial rights gets 403 on every milestone operation,
reads included (the plan's existence is no secret to them, its amounts are);
financial rights without managing allow reads and the invoiced step; managers do
everything.

"Financial rights on the project" is the module's existing rule: the project's
manager, `projects:manage-all`, or `projects:view-financials` on a project the
caller can see.

### Delivery B

| Operation | Notes | Access |
|---|---|---|
| `GET /{id}/economy` | `timeTracking` flag; budget (project + lines, line sums); actuals per bucket, per line, unpriced hours, last entry date; task estimate total; milestone totals; `budgetUsed {basis, percent, approvedPercent}`; `cost` block | hours for anyone who sees the project; amounts need financial rights; `cost` (cost per bucket, margin = bill − cost) needs financial rights **and** `projects:view-costs` |
| `GET /economy` | portfolio rows: project (id, code, name, status, customer), `budgetUsed`, buckets (hours + bill amount), pending hours, next open milestone, ready amount, `overBudget`; totals for the filtered set (ready amount, over-budget count); filters `status` (default `active`), `customerId`, `overBudget`, `hasReady`; sort `budgetUsed` (default, desc), `readyAmount`, `nextMilestone`, `code`; paged | `projects:access`; only projects the caller has financial rights on |

Portfolio mechanics: read the matching visible projects (cap 2 000 — beyond it a
400 asking for a narrower filter), one `ActualsForProjects` call, one grouped
milestone read, compute, sort and page in Go. Without Time, rows carry budgets
and milestones and no actuals; `budgetUsed` is absent.

### Stats

`GET /stats/summary` gains `readyMilestones`, the number of ready milestones the
caller may see (a count, not an amount: projects may be in several currencies).
`GET /stats/attention` gains:

| Type | For | entityId |
|---|---|---|
| `budgetWarning` (80 % ≤ used ≤ 100 %) | the project's managers | project id |
| `budgetExceeded` (used > 100 %) | the project's managers | project id |
| `milestoneReady` | managers and financial-rights holders who see the project | milestone id (project id in the link) |
| `milestoneOverdue` (`planned`, date passed) | the project's managers | milestone id |

Only `active` projects raise budget items; cancelled projects raise none. The
host translates titles from the type, as it does for Time.

## 6. Permissions

One new permission: `projects:view-costs` — "See what the work costs the company
and the margin on projects whose financials you can see. On a small project this
can reveal a person's cost rate." Sensitive, delegable, in no default role.

## 7. Frontend

### Economy tab (`/projects/$projectId/economy`, between Billing and Time)

Shown to everyone who sees the project once delivery B lands; in delivery A, which
has only the plan, the tab is shown to callers with `canSeeFinancials`. Content is
shaped by what the API returns.

1. **Headline figures** (A: ready to invoice, planned, invoiced; B adds budget
   used with its basis, value of work beside the fixed price, margin).
2. **Budget bar** (B): one stacked bar — approved solid, submitted lighter,
   draft hatched — with a budget marker, red overflow past it, a legend with
   hours and amount per segment, the task-estimate figure and the unpriced-hours
   note. Distinguishable without colour; every figure reachable by keyboard and
   screen reader.
3. **Per-line table** (B): code, name, budget, mini bar, used %, remaining,
   over-budget flag; a "no line" row when it has hours. Line budgets are edited
   in the Billing tab's line form (A).
4. **Invoice plan** (A): rows in manual order (managers reorder with up/down actions in the row
   menu — keyboard-accessible, no new dependency; drag is later polish),
   name, planned date, amount ("30 % · 300 000 kr" for percent), status badge,
   overdue marker, actions from `capabilities`; mark-as-invoiced dialog
   (reference, date); cancelled rows struck through and last; footer totals with
   the unplanned / over-planned line; add/edit modal with an amount-or-percent
   toggle (only with a fixed price).
5. **Time off** (B): budgets and plan work; a note replaces the bars.

A caller without financial rights sees the hours budget and bars only, and no
invoice plan.

### Portfolio (`/projects/economy`, sidebar item "Economy")

Visible to `projects:view-financials`, `projects:manage-all` and anyone who
manages a project (the API decides the rows). Table per §5, filter chips, sort,
totals line, URL-held state, row → the project's Economy tab.

### Dashboard

Projects card metric for ready milestones; the four attention types link to the
project's Economy tab. English and Norwegian throughout.

## 8. Edge cases

- Hours with no rate or another currency count in hours, not in amounts, and
  surface as "unpriced".
- A deactivated line, or one without a budget, shows actuals and no bar.
- Project without currency: no milestone or line budget amounts; hour budgets and
  bars work.
- Concurrent edits: revision conflicts; reordering under the per-project lock.
- Portfolio totals sum only rows the caller may see.
- Percent milestones are refused on a project without a fixed price (400 on the
  field); the amount-or-percent rule is validated server-side.

## 9. Testing

Backend: field shaping per caller kind (member, viewer, manager,
`view-financials`, `view-costs`, outsider); every allowed and refused status
move; exact percent amounts and freezing; fixed-price and currency guards;
delete rules; ordering under concurrency; the contract implementation against
entries in every state and currency case; Projects with Time absent and with a
failing provider; portfolio filters, sorts, no-basis-last, cap, paging; alert
thresholds at 79.99 / 80 / 100 / 100.01 %; contract coverage and the
cross-schema scan.

Frontend: bar segment combinations and overflow; the tab per permission level
(absent, not zero); milestone modal, status actions and dialogs per capability;
reorder; portfolio filters and sort in the URL; Time-off state; dashboard items
and links; catalog parity.
