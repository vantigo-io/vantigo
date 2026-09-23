# Customer 360 — design (delivery A: the overview panel)

Phase 5 of the Customers roadmap ("Customer 360"), delivery **A**: an overview panel
on the customer page that answers, in one glance, what is going on with this customer
across the modules that know — open projects, work approved but not yet billed,
expenses ready to invoice, when anything last happened. Delivery **B** (own spec) is
the customer default bill rate in the rate chain. Invoiced revenue and outstanding
wait for Invoices; other modules writing to the customer timeline wait for the outbox
(ROADMAP, Platform).

## Decisions

### D1 — One aggregator endpoint on Customers, composed server-side from module contracts

The roadmap imagined the panel "host-composed the same way the Energy and Projects
tabs are", but those tabs are each one module's own panel, and no module endpoint takes
a `customerId` for hours or expenses. What does exist is the server-side batch shape:
`contracts.ProjectActuals.ActualsForProjects` and
`contracts.ProjectExpenses.ExpensesForProjects`, both keyed by project ids and capped
together at `MaxActualsRequests` (2000). So the panel is **`GET
/customers/{id}/overview`** (`customers:view`), and the customers module composes it in
Go from `Deps.Projects`, `Deps.Actuals` and `Deps.Expenses` — the way expenses reads
projects: a nil dependency is a legitimate state (the module is off), never an error.
No directory call happens inside a transaction; the endpoint holds none.

`contracts.ProjectDirectory` gains **`ProjectsForCustomer(ctx, customerID int32)
([]ProjectEntry, error)`** — every project whose `CustomerID` is the id, any status,
ascending id, at most `MaxActualsRequests` (a customer with more has a portfolio, not a
panel; the response says it was cut — `projects.truncated: true`). Implemented by the
projects module beside `ProjectsForUser`; the fakes gain it.

`contracts.ActualsTotals` gains **`Invoiced ActualsBucket`** — the part of `Approved`
whose status is `invoiced` (Time's actuals SQL already keys the status; it grows a
fourth bucket key, and `Total` is unchanged). Additive: every provider fills it, the
projects consumer ignores it. It is what makes "unbilled" a figure rather than a guess:
**unbilled = Approved − Invoiced**, hours and bill amount, per project, summed.

### D2 — The response, and what each caller may see (shaped, never 403)

`CustomerOverviewResponse`:

- `projects?: {openCount, totalCount, truncated, open: [{id, code, name, status,
  lastWorkOn?}]}` — `open` is every project with `OpenForWork` (status `active`),
  newest work first then id, at most **10**; `lastWorkOn` is the actuals'
  `LastEntryDate` when Time is on. Present when the Projects module is on and the
  caller holds `projects:access`. **Which projects**: all of the customer's for
  `projects:view-all` or `projects:manage-all`; otherwise only those the caller holds
  a role on (`ProjectsForUser` ∩ `ProjectsForCustomer`) — the list endpoint's own
  visibility rule, restated with two directory calls instead of one per project.
  Counts are over the visible set, and the response says so once in the description.
- `work?: {unbilledHoursHundredths, unbilledAmounts?: [{currency, amount}],
  unpricedHoursHundredths (billable hours with no rate in the project's currency, across every bucket — final review I2),
  approvedHoursHundredths, submittedHoursHundredths, draftHoursHundredths,
  lastWorkOn?}` — from `ActualsForProjects` over the visible projects (each request
  quoted in its project's currency; a project with none contributes hours only).
  Present when Time is on and `projects` is present. **`unbilledAmounts` only for
  financial rights** — `projects:view-financials` or `projects:manage-all` — one entry
  per currency, no total across currencies (the expenses contract's rule). Cost never
  appears here; the panel is about the customer, not the margin.
- `expenses?: {readyCount, readyAmounts: [{currency, amount}], lastExpenseOn?}` —
  from `ExpensesForProjects`' `Ready*` figures (approved, billable, priced, not yet
  invoiced — the contract's own "ready to invoice"), per currency. Present when
  Expenses is on, `projects` is present, **and** the caller has financial rights.
  This is only the global half of the rule projects and the expenses summary grant
  money by: both also show a project's managers their own project's money, and the
  overview does not, because `ProjectsForUser` carries no role and each money figure
  is one sum over the visible set — matching them needs each project's role and money
  shaped per project, a delivery of its own (final review I1; a decision for the
  user).
- `lastActivity: {timelineOn?, workOn?, expenseOn?}` — the customer's own
  `timelineSummary.latestOccurredOn`, the latest `lastWorkOn`, the latest
  `lastExpenseOn`; each absent when unknown or not visible. Always present.

Permissions are read through `s.hasPermission(ctx, key)` for the other modules' keys
exactly as `customers:*` keys are — a permission key is a string the access contract
answers for any module. A section a caller may not see is **absent**, never a 403,
never an empty object: absent means "not for you or not installed", and the panel
hides the tile. A customer that does not exist is 404; an archived one answers
normally (the page shows it).

### D3 — The panel

A **Customer 360** panel at the top of the Overview tab, owned by the **host**
(`apps/host/frontend/src/routes/customers/-customer-360-panel.tsx`): it links across
modules (a project row links to `/projects/{id}`), which is the host's business as the
dashboard shows. It fetches `GET /customers/{id}/overview` with the host's query
pattern and renders `KpiCard` tiles — **Open projects** (count, "of N"), **Unbilled
hours** (hours; the amounts as a second line per currency when present), **Expenses
ready to invoice** (count; amounts per currency), **Last activity** (the latest of the
three dates, with which it was) — and, under the tiles, the open-projects rows (code,
name, status, last work). A tile whose section is absent is not rendered; when only
`lastActivity` is present the panel shows that one tile; the panel never explains why a
tile is missing (the dashboard's own rule). Loading and error states as the dashboard's
cards; an error hides the panel rather than the page. No new host props: the host
reads nothing but the response. Strings in `catalogs/customer.ts` (en + nb).

### D4 — Docs

`docs/customers.md`: a **Customer 360** section (the endpoint, the visibility rules,
what "unbilled" and "ready" mean and where each number comes from, the caps), the API
list, the frontend bullet (host-owned panel), a phase 5 paragraph in "what comes next";
`docs/projects.md`'s contract notes (`ProjectsForCustomer`); `docs/time.md`'s actuals
section (`Invoiced` bucket); `ROADMAP.md` phase 5 (A delivered; B, timeline writers and
revenue still ahead).

## Out of scope

Invoiced revenue / outstanding (no Invoices module), other modules writing to the
timeline (outbox), the customer default bill rate (delivery B), cost or margin figures,
a per-customer project list beyond the ten open rows (the Projects tab has it),
currency conversion, a new permission key.

## Testing

Backend through `modtest` with the harness's `WithProjects`/`WithActuals`/`WithExpenses`
fakes: the shaping matrix (module off → section absent; `projects:access` alone → only
role projects; view-all → all; financials → amounts; expenses only with financials),
unbilled = approved − invoiced across two projects in two currencies (no cross-currency
total), the ten-row cut and `truncated`, `lastActivity` from each source and none, 404,
archived answers. Projects: `ProjectsForCustomer` contract test (statuses, order, cap,
unknown customer → empty). Time: the `Invoiced` bucket in the actuals tests (an invoiced
entry lands in both Approved and Invoiced, `Total` unchanged). Contract: corpus still
validates. Host: the panel's tiles per section present/absent, links, loading/error,
both catalogs.
