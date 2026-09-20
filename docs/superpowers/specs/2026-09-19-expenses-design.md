# Expenses — outlays, mileage, travel claims and project costs

A new module, `expenses`. Phased plan: `2026-09-18-project-management-plan.md`
(phase 3, "expenses and supplier costs") — widened on purpose: expenses are a
thing of their own that *may* belong to a project, not a part of Projects.

## 1. Purpose

Let an employee get money back, and let a company know what a project cost and
what it passes on to the customer:

- an employee records what they paid (a receipt), what they drove (mileage) and —
  for a trip — their per diem, submits it, a manager approves, payroll
  reimburses;
- a project manager records a supplier cost against a project, with a markup;
- the project's Economy tab gets its cost side.

Vantigo still keeps no ledger: no VAT codes, no vouchers, no tax treatment.

Three deliveries, three PRs:

- **A — The module and single expenses**: outlays and mileage, categories, VAT,
  the optional project link with markup, receipts, approval, the reimbursed and
  invoiced tracks, settings and rates, the app.
- **B — Travel claims**: the claim container and per diem.
- **C — Project integration**: `contracts.ProjectExpenses`, an Expenses tab on the
  project page, the Economy tab's cost side, the portfolio.

Out of scope: OCR of receipts, a general documents module, supplier-invoice
inbox, payroll/accounting integration beyond a CSV export, tax calculations, a
country table for per diem abroad, an expense budget per project, automatic rate
updates.

## 2. Decisions

**X1 — Its own module, depending on nobody but identity.** `MODULES` may enable
`expenses` without `projects`. Projects is an *optional* read (`deps.Projects`,
the existing `contracts.ProjectDirectory`), exactly as Products is for Projects.
The coupling is optional in the other direction too (delivery C).

**X2 — The server says whether projects exist.** `GET /expenses/meta` reports
`projectsAvailable`. Without it there is no project field, billing line,
billable flag or markup anywhere, and the API refuses them. With it, a project is
optional on every expense and claim. If Projects is switched off later, stored
project ids stay and are simply not shown: an edit keeps the project, the billing
line, `billable`, the markup and the customer rate as stored and only recomputes
the bill amount when the net or the distance changed; every project-side
capability is false.

**X3 — Three kinds of line**: `outlay`, `mileage`, `per_diem`. Outlay and mileage
stand alone or sit inside a travel claim; per diem only inside a claim.

**X4 — The unit of approval** is a standalone line or a whole claim:
`draft → submitted → approved | rejected`; rejected returns to the owner with a
reason; unapprove produces a fresh draft. Amounts and rates are live while draft
and **frozen on submit**.

**X5 — Two tracks after approval**, both manual for now: **reimbursed** (per unit;
only what the employee is owed) and **invoiced** (per billable line on a project).

**X6 — Gross plus optional VAT.** Net = gross − VAT is the project cost and the
markup base; reimbursement is gross. No VAT codes. Mileage and per diem carry no
VAT.

**X7 — Markup on outlays, a customer rate on mileage.** Bill amount = net ×
(1 + markup %) for outlays; km × customer rate per km for mileage. Per diem is
never billable. Billable requires a project. **Pricing belongs to the project
side:** markup and customer rate are visible *and settable* only with financial
rights on the project — `expenses:manage` does not imply them. The employee ticks
"billable"; the server applies the default markup (or the dated customer rate, if
one applies); whoever has the rights prices the line through
`PUT /entries/{id}/billing`, open in every status except invoiced. An edit by
someone without the rights keeps the stored figures.

**X8 — Rates are admin-managed.** A dated table (mileage reimbursement, passenger
supplement, customer rate per km, the domestic per diem rates, the meal
deduction percentages). Seeded rows are ordinary rows labelled with their source
("State rate"); `expenses:manage` adds, edits, removes and resets them. An
approver or admin may override the rate on one line before approval; the line
records that, by whom, and what the table said — for the km rate and for the
passenger supplement. Employees cannot. "Reset to default" restores missing and
edited seeded rows; a company's own dated rows are untouched. Unapprove forgets
an override: a fresh draft reprices from the table. Everyone with access may read
the reimbursement rates (the form previews them); the customer rate per km is for
`expenses:manage`.

**X9 — Approval follows what is there.** `expenses:approve` approves anything;
with a project, that project's managers approve too. Booking on a project needs
what logging time needs (member or manager, project active —
`ProjectDirectory.CanLogTime`). Self-approval is allowed (as in Time).

**X10 — Time's conventions on purpose**: statuses, 404 for outsiders, 403 for a
caller who can approve nothing, all-or-nothing batches of at most 500 ids with a
per-id explanation, a period lock, revision-guarded full-replace PUT, exact
decimals with a 400 on a third decimal, absent-not-null shaping, no call to
another module — nor to the object store — inside a locked transaction. One rule
for refusals: 404 when the caller cannot see it, 403 when it is not theirs to
change, and a 400 naming the reason when the expense's own state forbids it (its
status, or the period lock).

**X11 — Receipts are attachments scoped to expenses**, stored through
`internal/storage`, served only through the API with the expense's own
visibility, removable only while the expense is editable. The pattern (owner
row → attachment rows → object keys) is the one later modules reuse.

**X12 — Expenses never eat the budget.** On the Economy tab "budget used" stays
about the work; expenses appear as a Costs section and in the margin.

## 3. Data model (schema `expenses`)

### 3.1 `expenses.entries` — one money line

| Column | Type | Notes |
|---|---|---|
| `id` | bigint identity | |
| `user_id` | uuid not null | the owner — the person it concerns |
| `created_by_user_id` | uuid not null | who entered it |
| `claim_id` | bigint null → `expenses.claims` | null = standalone (delivery B adds the table and the FK) |
| `kind` | varchar(20) | `outlay`, `mileage`, `per_diem` |
| `entry_date` | date not null | |
| `description` | varchar(500) not null | |
| `category_id` | integer null → `expenses.categories` | required for outlays |
| `supplier` | varchar(200) | outlays |
| `paid_by` | varchar(20) | `employee` or `company`; outlays. Mileage and per diem are always owed to the employee |
| `currency` | char(3) not null | |
| `gross_amount` | numeric(12,2) not null | outlay: entered; mileage/per diem: calculated |
| `vat_amount` | numeric(12,2) | outlays only; `0 ≤ vat ≤ gross` |
| `distance_km` | numeric(8,1) | mileage; `> 0` |
| `from_place`, `to_place` | varchar(200) | mileage |
| `passengers` | smallint not null default 0 | mileage; 0–8 |
| `rate` | numeric(10,2) | the reimbursement rate per km (or the per diem day rate) used |
| `passenger_rate` | numeric(10,2) | mileage |
| `rate_overridden_by_user_id`, `rate_table_value` | uuid, numeric(10,2) | set when an approver/admin overrode the rate |
| `project_id`, `billing_line_id` | integer null | opaque; no cross-schema FK |
| `billable` | boolean not null default false | requires `project_id` |
| `markup_percent` | numeric(6,2) | outlays; `0–1000` |
| `bill_rate_per_km` | numeric(10,2) | billable mileage |
| `bill_amount` | numeric(12,2) | stored; frozen on submit |
| `status` | varchar(20) not null default `draft` | for standalone lines; a claim's lines follow the claim |
| `submitted_at`, `decided_at`, `decided_by_user_id`, `rejection_reason` (varchar 1000) | | |
| `reimbursed_at`, `reimbursed_by_user_id`, `reimbursement_reference` (varchar 100), `reimbursement_date` | | standalone lines |
| `invoiced_at`, `invoiced_by_user_id`, `invoice_reference` (varchar 100) | | billable lines |
| `revision`, `created_at`, `updated_at` | | |

Indexes: `(user_id, entry_date)`, `(project_id, entry_date)`, `(status)` partial on
`submitted`, `(claim_id)`.

**Net** = `gross_amount − COALESCE(vat_amount, 0)`. **Owed to the employee** =
gross for an employee-paid outlay, the amount for mileage and per diem, zero for
a company-paid outlay.

### 3.2 `expenses.attachments`

`id`, `entry_id → entries`, `object_key`, `file_name` (255), `content_type`,
`size_bytes`, `uploaded_by_user_id`, `created_at`. JPEG, PNG, HEIC, PDF; 10 MB
each; at most 10 per entry.

### 3.3 `expenses.categories`

`id`, `name` (100, unique case-insensitively), `active`, `position`. Seeded:
Materials, Subcontractor, Equipment hire, Travel, Accommodation, Meals, Phone and
internet, Other. Deactivated, never deleted, once used.

### 3.4 `expenses.rates`

`id`, `kind` (`mileage`, `mileage_passenger`, `mileage_customer`,
`per_diem_6_12`, `per_diem_over_12`, `per_diem_overnight_hotel`,
`per_diem_overnight_other`, `meal_breakfast_percent`, `meal_lunch_percent`,
`meal_dinner_percent`), `valid_from` date, `value` numeric(10,2), `currency`
char(3) null (null for percentages), `source` varchar(100) (the label; null = the
company's own), unique `(kind, valid_from)`. The rate for a date is the row with
the greatest `valid_from ≤ date`.

Seeds (delivery A): `mileage` 5.30 NOK and `mileage_passenger` 1.00 NOK, source
"State rate", verified against Skatteetaten's published rates on 2026-09-19;
`mileage_customer` is not seeded (a company's own price).

Seeds (delivery B), verified on 2026-09-20 against the state's *Særavtale om
dekning av utgifter til reise og kost innenlands* (in force 2026-01-01 to
2027-12-31, §§ 6 and 9): `per_diem_6_12` 397 NOK, `per_diem_over_12` 736 NOK,
`per_diem_overnight_hotel` 1012 NOK, `meal_breakfast_percent` 20,
`meal_lunch_percent` 30, `meal_dinner_percent` 50 — all `valid_from` 2026-01-01,
source "State rate". The agreement knows ONE overnight rate; the
`per_diem_overnight_other` kind is therefore not seeded — it is for a company's
own rate (the tax authority's lower figures for a boarding house or barracks, for
instance), and a day of that type cannot be priced until one exists. The
agreement's unreceipted night supplement (§ 10, 452 NOK) and its conditions on
distance (over 15 km) are not modelled: an approver judges them.

### 3.5 `expenses.settings` (single row)

`locked_before` date, `default_currency` char(3) default `NOK`,
`default_markup_percent` numeric(6,2) default 0, `receipt_required_over`
numeric(12,2) null.

### 3.6 `expenses.claims` (delivery B)

`id`, `user_id`, `created_by_user_id`, `purpose` (200), `destination` (200),
`abroad` boolean, `abroad_day_rate` numeric(10,2), `departure_at`, `return_at`
timestamptz, `project_id` null, the status / decision / reimbursement columns of
§3.1, `revision`, timestamps. A claim's lines carry its project unless a line
says otherwise for `billable` only.

## 4. Rules

**Mileage.** Amount = km × rate(date) + km × passenger rate(date) × passengers,
half-up to two places. Billable: bill amount = km × customer rate per km
(defaults from the table, editable on the line).

**Per diem (delivery B).** No overnight: one day line when the trip lasts ≥ 6 h —
`6_12` up to 12 h, `over_12` beyond. With overnight: one line per started 24-hour
period from departure; a final part-period counts when it exceeds 6 h. The
employee picks the overnight type per day and ticks covered meals; each tick
deducts its percent of that day's rate; never below zero. Abroad: the claim's
entered day rate, same percentages. Suggestions are computed by the server.

**Outlays.** Bill amount = net × (1 + markup/100). Markup defaults from settings.

**Freezing.** On submit the rate(s), amounts and bill amount are stored as they
are and no longer follow the table. Rejected → editable → refrozen on the next
submit. Unapprove → draft.

**Receipt rule.** When `receipt_required_over` is set, an employee-paid outlay
whose gross exceeds it cannot be submitted without an attachment (400 naming the
entry).

**Period lock.** Nothing dated before `locked_before` is created, edited,
deleted, given or stripped of a receipt, submitted, approved, rejected,
unapproved or rate-overridden — except by `expenses:manage`. It protects what the
employee submitted and what was approved. It deliberately does not reach
reimbursing, pricing or invoicing: that is bookkeeping done after a period
closes.

**Kind.** A draft outlay that carries receipts cannot become a mileage line
(mileage takes no receipts); the receipts are removed first.

**Currency.** One per line; never converted. Mileage and per diem use the default
currency. A line counts towards a project's figures only when its currency is the
project's; otherwise it is reported as "in another currency", never dropped.

## 5. Permissions and visibility

`expenses:access`, `expenses:approve`, `expenses:view-all`, `expenses:manage`
(sensitive). An expense is visible to its owner, to a manager of its project, and
to holders of `view-all` / `approve` / `manage`; anyone else gets a bare 404.
The payroll reference on a reimbursement is for the owner and for `view-all` /
`approve` / `manage`, not for a project manager as such. Markup, customer rate
and bill amount need financial rights on the project
(manager role, `projects:manage-all`, or `projects:view-financials` on a project
the caller can see); the owner always sees their own gross, VAT and what they are
owed. Marking reimbursed, overriding via settings, recording for a colleague and
working past the lock need `manage`; overriding a rate on a line needs `approve`
(for that line) or `manage`; marking invoiced needs financial rights on the
project.

## 6. API (`/api/v1/expenses`, contract `openapi/expenses.yaml`)

| Area | Operations |
|---|---|
| Meta | `GET /meta` — `projectsAvailable`, default currency, default markup (for `expenses:manage` only, here and in `/settings` — the server applies it, no form needs it), categories, `lockedBefore`, `receiptRequiredOver`, capabilities (`canApprove`, `canViewAll`, `canManage`) |
| Entries | `GET /entries` (userId, projectId, status, kind, from, to, reimbursed, paged; the claim filters arrive with delivery B) · `POST /entries` · `GET|PUT|DELETE /entries/{id}` |
| Project options | `GET /projects` — the projects the caller (or, for `expenses:manage`, `?userId=` a colleague) may book on, with their billing lines — so the form needs no Projects UI code · `GET /entries/{id}/billing-lines` — the lines of an expense's project for whoever may *price* it, which is not the same people |
| Receipts | `POST /entries/{id}/attachments` (multipart) · `GET /attachments/{id}` · `DELETE /attachments/{id}` |
| Flow | `POST /submit` · `/approve` · `/reject` (a reason is required) · `/unapprove` (whoever could approve it, or `expenses:manage`) — `{entryIds, claimIds?}`, `claimIds` refused until delivery B · `GET /approvals` (grouped per person, longest-waiting first, paged by group) · `PUT /entries/{id}/rate` (override) · `PUT /entries/{id}/billing` (pricing) |
| After approval | `POST /reimbursed` · `/reimbursed/undo` · `GET /reimbursements` (approved, owed, per person) · `GET /reimbursements/export.csv` · `POST /entries/{id}/invoiced` · `/invoiced/undo` |
| Settings | `GET|PUT /settings` · `GET|POST /rates` · `PUT|DELETE /rates/{id}` · `POST /rates/reset` · `GET|POST /categories` · `PUT /categories/{id}` |
| Dashboard | `GET /stats` · `/stats/summary` · `/stats/timeseries` · `/stats/attention` (`approvalWaiting`, `expenseRejected`, `reimbursementWaiting`) |
| Claims (B) | `GET|POST /claims` · `GET|PUT|DELETE /claims/{id}` · `POST /claims/{id}/per-diem-suggestion` |
| Per project (C) | `GET /projects/{projectId}/summary` |

## 7. Frontend

A new app "Expenses" / "Utlegg" (`apps/expenses/frontend`,
`@vantigo/expenses-ui`). Sidebar: **My expenses** (`expenses:access`),
**Expense approvals** (`expenses:approve`; route open to `expenses:access` via
`guardPermissions`, as Time — named so because the spotlight lists every app's
actions together), **Reimbursements** and **Settings**
(`expenses:manage`).

- **My expenses**: own lines and claims, status filters, a strip with draft /
  awaiting approval / approved-not-reimbursed totals, *New expense* (one form; the
  kind switches fields; VAT helper; "I paid / the company paid"; receipt drop zone
  with camera capture; project block only when `projectsAvailable`), *New travel
  claim* (B). Rejection reason shown on the item.
- **Approvals**: per person; each unit with owner, project, total, receipt
  indicator, overridden rates flagged; receipt viewer; approve / reject; only
  approvable units selectable; rate override on a line.
- **Reimbursements**: approved units owed, per person with totals; mark
  reimbursed (date, reference); undo; CSV export.
- **Settings**: general; the rates table grouped by rate with source labels, add /
  edit / remove / reset; categories.
- **Travel claim page** (B): header, per diem days with "Suggest days" and meal
  ticks, mileage legs, outlays with receipts, totals per currency, one submit.
- **Project page** (C, both modules on): an Expenses tab and "Record a cost"; the
  Economy tab's Costs section, margin including expenses, "ready to invoice"
  including billable lines; the portfolio's ready column.
- Dashboard card and attention items; spotlight "New expense" (and "New travel
  claim" in B). English and Norwegian. Receipt thumbnails have alt text; the
  viewer is keyboard-operable; nothing by colour alone.

## 8. Edge cases

- Projects off: `projectId`, `billingLineId`, `billable`, `markupPercent`,
  `billRatePerKm` on a request → 400 on the field; `GET /projects` → 404.
- A project the owner may not book on → 400 on `projectId` (one message whatever
  the reason). Recording for a colleague checks the *colleague's* right.
- A project that is no longer active: existing lines stay; new ones are refused.
- A deactivated category stays on existing lines and is refused on new ones.
- An attachment upload to a submitted/approved entry → 400; a download by someone
  who cannot see the entry → 404.
- Marking reimbursed something with nothing owed → 400 naming it.
- Rate rows: removing the only row of a kind that lines need is allowed (lines
  then say "no rate for this date" and cannot be submitted).

## 9. Testing

Backend: shaping per caller kind (owner, colleague, project manager, approver,
view-all, manage, outsider); Projects on / off (fake directory via
`modtest.WithProjects`, and none); every status move allowed and refused; freezing
on submit and refreezing after rejection; mileage arithmetic incl. passengers and
half-up; VAT bounds and net; markup and bill amount; rate lookup by date, seeds,
reset, override and its audit fields; the receipt rule; the period lock on every
mutating path; attachments (types, size, count, visibility, lifecycle, the object
store fake); batches (500 cap, all-or-nothing, 403 for non-approvers); the two
tracks and their undo; the CSV; stats and attention; no directory call inside a
locked transaction (harness-wide check, as Projects and Time); contract coverage;
the cross-schema scan; config: `expenses` enabled without `projects`.

Frontend: the form per kind; VAT helper; the project block present / absent by
`projectsAvailable`; receipt upload and viewer; status strip; approvals incl.
selection rules and override; reimbursements incl. export; settings incl. rates
with source labels and reset; permission-gated navigation; dashboard items;
catalog parity.
