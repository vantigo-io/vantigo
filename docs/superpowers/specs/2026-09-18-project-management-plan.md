# Project management and planning — research and phased plan

## 1. Purpose

Projects (phase 1, PR #98) gave Vantigo a stable project identity, codes,
roles and billing lines. This document decides what project management and
planning should exist on top of it — tasks, milestones, time, budgets,
timelines, capacity — and in what order, for the customers Vantigo targets
first: **small contractors and consultancies**.

It is a plan, not a spec. Each phase gets its own spec when it starts.

The evidence is three surveys of comparable software, kept as reference under
`docs/superpowers/research/`:

- `2026-09-18-nordic-erp-project-modules.md` — Tripletex, PowerOffice Go,
  Visma.net / Business NXT / eAccounting / Severa, 24SevenOffice, Xledger, Uni,
  and the installer tools (Ordrestyring, SpeedyCraft, Cordel, Handyman, Moment).
- `2026-09-18-erp-psa-project-modules.md` — Odoo, ERPNext, Business Central,
  D365 Project Operations, SAP B1, NetSuite, SuiteProjects, Scoro, Productive,
  Teamwork, Kantata, Harvest, Forecast, Float, Runn.
- `2026-09-18-pm-tools-and-planning-ux.md` — Jira, Asana, Monday, ClickUp,
  Linear, Basecamp, Notion, Trello, Smartsheet/MS Project, Fieldwire/Procore,
  and the React Gantt component landscape.

## 2. What the market does

**Table stakes in the Norwegian SMB tier.** A project linked to a customer;
time logged against the project plus one work dimension (activity, work type or
task); a billable flag; approval before invoicing; invoicing from hours (T&M)
and from a fixed price; hours/amount budget against actuals; supplier costs and
expenses passed through with markup; EHF delivery; mobile time entry.

**Rare in that tier.** Gantt (24SevenOffice, Severa, Handyman, Moment only),
capacity planning (Severa, Xledger, 24SevenOffice), kanban (Severa), customer
portals (almost nobody), WIP as a system figure (Xledger, Severa).

**The dominant hierarchy** is *project → one flat layer* (Tripletex activities,
PowerOffice activities, Xledger activities). Full phase/task/milestone trees
exist only in the PSA tools (Severa) and the international ERPs. Invoicing
attaches either to that layer (Severa, Visma.net) or to a dated invoice plan
detached from the work structure (Tripletex and 24SevenOffice *fakturaplan*).

**Norwegian gaps nobody serves well.** A-konto is fully modelled only in
Tripletex; NS 8405/8406/8407 milestone billing and *innestående* (retention)
are essentially unserved; overtime billing multipliers are manual everywhere;
Boligmappa integration exists only in Tripletex and Ordrestyring.

**Patterns the good products converge on.**

- Cost rate and bill rate are two numbers everywhere, and the bill rate
  resolves through an explicit precedence chain (Teamwork: project rate → client
  role rate → role rate → person default) and is **snapshotted onto the time
  entry**. Products that changed rates retroactively regret it (NetSuite, Runn
  caveats).
- Approval is a state machine on the time entry (draft → submitted →
  approved, often with recall) with period locking; the financial "actual" is
  created on approval. It is foundational in every serious PSA/ERP tool and
  expensive to retrofit once invoices reference raw entries.
- Wherever milestones **drive money**, the vendor ended up with a **billing
  milestone that is a separate object from the delivery milestone** (Odoo
  `project.milestone` + SO line, D365 Project Operations contract-line
  milestones, Kantata billing milestones). Task-level milestones are
  decorative; the invoicing milestone lives on the contract/billing side.
- Every product with a good planning reputation keeps **three separate
  layers** — allocation (person → project, forward), assignment (person → task),
  actual (time entry) — and reports utilisation in exactly those layers. The
  ones that bolted planning onto the task table (Business Central, SAP B1)
  stalled for a decade.
- The **minimal task model** shared by every PM tool: title, status, one
  assignee, start/due dates, a grouping, comments, attachments. Custom status
  labels are the norm, but the tools that do it well keep a canonical category
  (todo / in progress / done) underneath. Asana rejects multiple assignees by
  design (one directly responsible person; split into subtasks otherwise), and
  that avoids a class of notification and "who marks it done" problems.
- Milestones are either a task flag (Asana, cheapest) or a lightweight
  separate entity with derived progress (Linear: name + optional target date,
  % complete from linked issues). Linear shipped milestones as data years
  before it drew them on a timeline.
- **Gantt splits into two tiers.** A lightweight *timeline* (drag bars,
  finish-to-start dependencies, no critical path) is what Jira, Asana and
  Notion ship; a *full Gantt* (critical path, baselines, lag, resource
  levelling) is Smartsheet/MS Project territory, expensive to build (dependency
  cascades, DAG algorithms, virtualisation, drag accessibility), and the
  category leaders skip or paywall it. Basecamp rejects it outright.
- Vendors shipped, in this order: tasks + time → approval + budgets →
  milestones/billing → resource planning → Gantt/WIP last.

## 3. Decisions

**D1 — Hours first, with a lean task model alongside.** Of the three
orderings considered — PSA order (time → approval → budgets → milestones →
planning), PM-tool order (tasks + board → milestones → time → timeline) and
installer order (work orders → scheduling → time → a-konto) — Vantigo follows
the PSA order and pulls the task model forward so tasks and time land together.
Projects already carries the commercial frame, so finishing the money loop is
the shortest path to something invoiceable, and every surveyed vendor built
planning after time and approval. The installer order is not lost: work orders
are tasks with checklists and materials, and a-konto is a billing milestone.

**D2 — Time is its own module; tasks live inside Projects.** `time` requires
`projects` (as `projects` requires `customers`) and reads
`contracts.ProjectDirectory`, never the `projects` schema. Tasks are
project-scoped and small, so they belong to the projects module; a later
`work` module (work orders, field service) can reference them.

**D3 — A time entry references a project, optionally a billing line, and
optionally a task.** Time against a task is a convenience for the person
logging, not a requirement. The timesheet lists, under each project code, the
project's active billing lines and the open tasks assigned to the caller
("KVEM1000 › PM › Migrate meter data"). `ProjectDirectory` grows the read
methods Time needs (`BillingLines(projectID)`, `OpenTasksForUser(userID)`,
lookup by code, batch `Projects(ids)`); nothing else changes in Projects.

**D4 — Rates resolve through an explicit chain and are snapshotted.** Bill
rate: billing-line rule → project default rate → person default. Cost rate:
always the person's. Both are copied onto the time entry at entry time.
Changing a rate never rewrites history. This is the answer to the "rates per
person" item left open in the Projects roadmap.

**D5 — Approval is a state machine from day one.** `draft → submitted →
approved → invoiced`, with rejection back to draft, recall before invoicing,
and period locking. Only approved time counts towards budgets and invoices.

**D6 — Single assignee, canonical status category, one level of subtasks.**
Labels may be customised per deployment later; the category (`todo`,
`in-progress`, `done`) is fixed so rollups and "open tasks" never depend on a
label. Checklist items inside a task are lightweight (text + done), not
assignable, not reported — the contractor's punch list.

**D7 — Billing milestones are a billing-side object, delivery milestones a
delivery-side one.** A billing milestone hangs off the project's commercial
frame (date, amount or % of the fixed price, status) and is what Invoices
turns into an invoice line; a-konto is one kind. A delivery milestone is a
lightweight entity (name, optional target date) that tasks link to, with %
complete derived from them. The two may be related later but are never the
same row.

**D8 — Timeline before Gantt; Gantt only on proven demand.** The first
visual plan is a lightweight timeline (bars, drag, finish-to-start
dependencies, opt-in cascade, day/week/month zoom). Critical path, baselines,
lag and resource levelling are not built in-house; if a segment ever needs
them, evaluate DHTMLX PRO or SVAR.

**D9 — Capacity planning is an allocation table, never a task attribute.**
Person × project × date range × hours-or-percent, tentative/confirmed, with
placeholders for unfilled roles, compared against assignments and actuals.

**D10 — Milestone codes stay out of the line-code namespace.** Billing lines
own `<project>-<code>`. If delivery milestones ever get codes, they use a
distinct shape decided with that phase — never a bare suffix that could
collide with a line code.

## 4. Phases

Each phase is one spec and one PR-sized delivery, usable on its own.

### Phase 2 — Time tracking and tasks

*Modules:* new `time`; `projects` grows tasks and the directory methods.

- **Tasks** (projects): title, description, one assignee, status category +
  label, start/due date, estimate hours, one level of subtasks, ordering,
  checklist items, comments (project timeline entries), attachments deferred to
  the documents decision. Views: project task list and board, "my tasks" across
  projects.
- **Time entries** (time): project, optional billing line, optional task,
  date, duration, note, billable flag, snapshotted cost and bill rate, state
  machine per D5, period locking. Person default rates and cost rates live in
  `time` (a small rate card per person), project default rate on the project.
- **Views** (time): my week (timesheet grid grouped by project code with lines
  and assigned tasks), a stopwatch/quick entry, manager approval queue,
  per-project hours summary and a Time tab on the project page (host-composed).
- **Permissions:** `time:access`, `time:log`, `time:approve`,
  `time:view-all`; project roles decide which projects appear (members log,
  viewers look, per Projects phase 1).

*Unblocks:* hours that can be invoiced; a place to log time that survives the
first month of real use; the first real consumer of billing lines.

### Phase 3 — Budgets, billing milestones and costs

- Budget vs actual per project and per billing line: hours, cost, revenue;
  expected → to invoice → invoiced.
- **Billing milestones** per D7, including a-konto; a per-project invoice plan
  view. Invoices (a later module) consumes approved time and milestones.
- Expenses and supplier costs on a project with markup; material consumption
  via Products/Orders when those exist.
- Overtime and work-type multipliers as rules on the billing line, so the
  Norwegian overtime case stops being a manual duplicate line.

*Unblocks:* fixed-price and a-konto invoicing; profitability per project;
budget alerts.

### Phase 4 — Delivery milestones, timeline and templates

- Delivery milestones per D7 (Linear model), rendered as diamonds.
- Lightweight timeline per D8, built on a component spike (`mantine-gantt`
  or DHTMLX Community, MIT) with a bespoke bar view as the fallback.
- Project templates: tasks and milestones with relative dates; task
  templates for recurring checklists.

*Unblocks:* planning a project at kickoff; the "are we on track" view.

### Phase 5 — Capacity planning

- Allocations per D9; availability and utilisation per person and team;
  allocation vs assigned vs actual side by side.
- Placeholders for unfilled roles.

*Unblocks:* "who is free in week 42"; forecasting revenue from planned hours.

### Later, only on proven demand

Full Gantt (critical path, baselines, lag), WIP / earned-value accounting,
multiple assignees, a customer portal, and a Norwegian construction layer
(NS 8405–8407 milestone rules, *innestående*, kontrollskjema, Boligmappa) as
a vertical of its own.

## 5. Open questions to settle in the Phase 2 spec

- Whether the person rate card lives in `time` or in identity (leaning `time`:
  it is billing data, not account data).
- The exact timesheet UX for contractors versus consultants (day list versus
  week grid); both are cheap, the default matters.
- Whether comments on tasks reuse the project timeline table or get their own.
- How the Time tab on the project page is gated (module enabled + `time:access`
  + project role), following the customer-page Projects tab.
