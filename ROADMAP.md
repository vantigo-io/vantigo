# Roadmap

Planned evolution per module. Each phase notes why it exists and what it unblocks.
Other modules add their own sections as their roadmaps solidify.

## Platform

### Cross-module domain events (deferred until Orders)

Synchronous cross-module queries use in-process contracts from
`internal/contracts` (e.g. `contracts.CustomerDirectory`) and need nothing more.
For asynchronous "something happened" notifications the decided pattern is
**in-process domain events dispatched through a transactional outbox**: the
publishing module writes the event in the same transaction as its state change,
and a background worker dispatches to handlers with retries (the Communications
outbox worker already proves the pattern). **No message broker** — Postgres is
the queue, and every infrastructure piece multiplies per dedicated customer
deployment. Build the event bus together with its first real consumer, most
likely Orders (`OrderPlaced` → Communications sends confirmation, Warehouse
reserves stock). If a module is ever extracted, the outbox dispatcher targets a
transport instead of in-process handlers; event contracts stay unchanged.

## Customers

Customers is the hub Energy, Projects, Time, Expenses and (eventually) Invoices all
hang off — a project has a `customerId`, a supply period has a customer, an invoice
will too — not a sales pipeline of its own. Research and priorities are in
[`docs/superpowers/research/2026-09-21-customers-module-next.md`](docs/superpowers/research/2026-09-21-customers-module-next.md),
comparing this module against the Nordic ERP/accounting field (Tripletex, PowerOffice,
Visma, Fiken, Fortnox, e-conomic) and international CRM/PSA tools (HubSpot, Pipedrive,
Attio, Odoo, Business Central, Productive).

### Phase 1 — Foundation (done)

Decided in
[`docs/superpowers/specs/2026-09-21-customers-foundation-design.md`](docs/superpowers/specs/2026-09-21-customers-foundation-design.md).
Closed the gaps that would otherwise be built upon, adding no new customer data: timeline
entries — manual ones, each of their revisions, and the generated events a user's action
causes — now name who wrote them, snapshotted from the user directory at write time; the customer row gained `revision`, an optimistic-concurrency
token an update or a type change may carry and get refused (409) against; a legal
identity another customer already has — archived included — is a 409 a caller can
overrule with `allowDuplicateIdentity: true`, since there is no unique index; legal
identities are now validated where a rule actually exists (ISO 3166-1 alpha-2 for
`country`, Norwegian organisasjonsnummer mod-11 for `no` + `business`, a Norwegian
person's id deliberately left unvalidated pending the GDPR question in phase 6); and
the customer list now searches by customer number and — permission-gated, so search is
never an oracle for data the response would withhold — legal name/id and contact
name/email, filters by status and type, sorts by more than id/name, and can show
archived customers with a restore path. `disabled` stays a label that blocks nothing
yet, on purpose: Projects already accepts a project against an archived customer, so
inventing a blocking rule for `disabled` ahead of Invoices would contradict that
standing decision. See [`docs/customers.md`](docs/customers.md).

*Unblocks:* every later phase below, and a customer list, duplicate guard and timeline
worth building the invoice-ready customer on top of.

### Phase 2 — The invoice-ready customer (align with Invoices) (done)

Decided in
[`docs/superpowers/specs/2026-09-21-customers-invoice-ready-design.md`](docs/superpowers/specs/2026-09-21-customers-invoice-ready-design.md),
split into two deliveries.

**Delivery A (done)** — the data, its API, the UI and the directory. Addresses
(postal, invoice, delivery, visiting — table stakes in every Nordic system
compared), any number of each, one primary per type. Customer-level contact info:
email, phone, website, on the customer itself. A billing profile behind its own
permission (`customers:billing-manage`, deliberately narrower than
`customers:update`): payment terms (days), currency, document language, invoice
delivery method, reminder delivery method, a Peppol participant id/GLN, and a
default buyer reference ("deres referanse") — with an **invoice email** and a
**reminder email** kept separate from the general contact address, since Norwegian
eFaktura/EHF and dunning correspondence route differently. `contracts.CustomerDirectory`
grew a `BillingProfile` read (every field resolved once, for Invoices) and a batch
`Customers(ids)` lookup — Projects' project list now makes one directory call per
page instead of one per distinct customer. See [`docs/customers.md`](docs/customers.md).

**Delivery B (done)** — decided in
[`docs/superpowers/specs/2026-09-21-customers-peppol-lookup-design.md`](docs/superpowers/specs/2026-09-21-customers-peppol-lookup-design.md):
a Peppol capability lookup (SML DNS → SMP → BIS Billing 3.0 support), asked on a
person's click (`POST .../peppol-lookup`) and remembered on its own table, off the
customer row. It deliberately does **not** set the invoice delivery method to EHF
automatically the way every Nordic competitor surveyed but Fortnox does — the
billing card offers **Use EHF** instead of switching silently, so nobody's billing
decision changes without a click. Delivery A did not wait for it: `peppolId` and
`invoiceDelivery` were, and remain, plain fields a person can fill in by hand, and
the billing profile's `ehf_without_recipient` warning already flagged a customer
set to `ehf` with no Peppol id to send to; two new warnings
(`ehf_recipient_not_registered`, `ehf_available`) now read the stored lookup
answer too. See [`docs/customers.md`](docs/customers.md#peppol-lookup).

*Unblocks:* Invoices can now read a resolved billing profile through the
directory — delivery A gave a customer somewhere to send an invoice and terms to
put on it; delivery B tells a person, with one click, whether EHF will actually
reach that customer.

### Phase 3 — Brreg in full

Decided in
[`docs/superpowers/specs/2026-09-22-customers-brreg-full-design.md`](docs/superpowers/specs/2026-09-22-customers-brreg-full-design.md),
split into two deliveries.

**Delivery A (done)** — the fuller Brreg record: org form, NACE code, employee
count, addresses, MVA-registration, website, bankruptcy/dissolution flags, parent
entity, instead of the earlier `legalId`/`legalName` pair alone — kept beside the
customer (`customers.customer_registry_records`), never on it, so a fetch never
bumps `revision`. Fetched on a Brreg pick (create or a Brreg-sourced legal-identity
PUT) and re-read on a **Refresh** click (`POST .../registry-refresh`); a struck-off
entity (`SlettetEnhet`) and one removed from open data (410) are both real answers,
not failures, the second deleting the copy on file. What differs from the record on
file is written as a `registry.change` timeline event — the event type existed,
unused, since the port, and now has its first producer (`customers.brreg`) — and
bankruptcy/dissolution/deletion/a name change surface on `/stats/attention`, also no
longer a stub. Addresses from the record are offered to the address book, never
written to it. See [`docs/customers.md`](docs/customers.md#registry-record).

**Delivery B**, not yet built — a scheduled refresh from Brreg's incremental update
feed (`GET /oppdateringer/enheter`, cursor `oppdateringsid`), driving the same
fetch-and-store delivery A built and filling `registry_updated_hint`, the column
delivery A's own migration already carries but leaves untouched. Beside it,
**scheduled re-checks of Peppol registration**: today's
[Peppol lookup](docs/customers.md#peppol-lookup) is only ever a person's click, on
purpose (design D4) — a background worker asking again periodically, on the same
`ehf_available`/`ehf_recipient_not_registered` warnings a manual check already
raises, is this delivery's job.

*Unblocks:* registry data worth relying on instead of a name and a number typed once,
and more behind the one endpoint (`/stats/attention`) and the one event type
(`registry.change`) this module already declared.

### Phase 4 — Light CRM

An owner/account manager (single user) and a "my customers" filter. Tags, then
customer groups that can carry defaults (payment terms, later the customer-group
prices Products phase 4 already plans for). Typed contact roles with a primary
contact (billing, project, decision maker), replacing today's free-text `role` —
answers "who gets the invoice" and "who approves", the way Business Central's and
Salesforce's contact-role models do. Follow-ups: a timeline entry that can carry a
follow-up date and assignee, feeding `/stats/attention` and a "my follow-ups" view —
no task engine beyond that. Attachments on a customer and its timeline entries
(contracts, NDAs), once the storage module has a model for it.

*Unblocks:* answering "who owns this relationship and what happens next" without
building a deals pipeline.

### Phase 5 — Customer 360

An overview panel per customer — open projects, unbilled hours and expenses, invoiced
revenue and outstanding once Invoices exists, last activity — host-composed from
module contracts the same way the Energy and Projects tabs already are. Other
modules writing to the customer timeline (project created/closed, invoice sent, supply
period started), riding on the domain-events outbox deferred until Orders (see
[Platform](#platform)). A customer default bill rate, slotted into the chain Projects
and Time already resolve rates through (billing line → project → **customer** →
person).

*Unblocks:* the reason the customer page is meant to be the hub, not just a card.

### Phase 6 — Data operations and compliance

CSV import (create and update, with error-row re-run) and export, for onboarding away
from Tripletex/Fiken/PowerOffice. Merging duplicate customers — moving contacts,
timeline entries, and re-pointing whatever other modules hold a customer id, through a
contract — far cheaper to build now, before invoices reference customers, and a
differentiator in the Norwegian field: of the systems compared only SuperOffice documents
a merge, and Tripletex states customers cannot be merged at all. GDPR handling for person customers: data export and scheduled
anonymisation that leaves bookkeeping retention intact — and, as phase 1 already
insists, never a fødselsnummer field.

*Unblocks:* clean onboarding and offboarding, and a merge path that only gets more
expensive the longer it waits.

### Later

Parent company (`parentId`, seedable from Brreg's `overordnetEnhet`) · customer-is-also-supplier
(once purchasing/supplier invoices arrive) · custom fields · credit check integration
(Proff/Creditsafe) · credit limit and credit hold (needs receivables to mean anything)
· per-customer dunning settings (belongs with Invoices' own dunning) · a customer
portal (after Invoices) · saved list views.

**Deliberately not planned inside Customers:** a sales pipeline or quotes (its own
module, if ever), email/calendar sync, marketing automation and a consent centre,
lead/health scoring, account teams, and territories. A Norwegian B2B e-invoicing
mandate has been discussed for 2027-01-01, but that date is indicative — sourced from
advisory-firm summaries, not a published regulation — and nothing here is scheduled
against it.

## Energy

### Phase 1 — Core metering model (done)

Metering points (målepunkt) keyed by the 18-digit GSRN with Nordic-compatible
attributes (price area, grid area, address, coordinates, expected annual
consumption), consumption as revisioned kWh intervals (corrections supersede,
never overwrite; arbitrary interval length supports PT1H today and PT15M later),
and supply periods linking customers to metering points over half-open time
ranges (overlap-proof via a Postgres exclusion constraint). Consumption access
is authorized through supply periods, so a customer never sees readings outside
their own ownership window.

*Unblocks:* registering meters, manual readings, and the privacy model required
by the Norwegian market (and GDPR) before any Elhub data flows.

### Phase 2 — Market operations (done)

Physical meters split from metering points (installation history + swap
endpoint, since AMS meters get replaced while the målepunkt persists),
server-side consumption aggregation (hour/day/month) bucketed in the metering
point's market timezone derived from its price area (NO/SE/DK → CET, FI → EET,
DST-correct), and atomic customer switch (move-in/move-out in one transaction
producing contiguous supply periods).

*Unblocks:* correct daily/monthly figures for charts and invoicing, meter swap
workflows, and leverandørbytte-style customer changes.

### Phase 3 — Customer context (done)

Customer detail page gets an Energy tab (host-composed, reusing the energy
module's meter listing) so day-to-day work happens in the customer's context:
consumption stats over the last twelve months, the customer's metering points
with attach/detach, all behind the deployment's enabled modules and the caller's
permissions (the tab row follows the same visibility rules as the sidebar).
Metering points are also searchable from the global spotlight by GSRN, meter
number, or address.

*Unblocks:* the composition pattern future modules (Invoices) reuse to extend
the customer page.

### Phase 4 — Invoicing groundwork (align with Invoices)

An `internal/contracts` interface (e.g. `contracts.ConsumptionProvider`) returning
billable consumption per customer/metering point/supply period, keeping the
privacy boundary enforced in one place. Price dimension: spot prices per price
area (NO1–NO5) and grid tariffs, decided together with the Invoices module.

*Unblocks:* the Invoices module generating invoice lines from consumption
without touching `energy` tables.

### Phase 5 — Elhub integration (when API access is ready)

Sync worker following the Communications outbox pattern: ingest metered values
as `Source=Elhub` revisions (the supersede model already handles corrections),
derive supply-period events from market processes (leverandørbytte,
innflytting/utflytting), and reconcile master data (grid area, price area,
connection status) against Elhub with drift flagging. Adds
`SourceMessageId`/event audit columns.

*Unblocks:* automated consumption import and market-event-driven supply periods.

### Later

- **PT15M scale** — monthly partitioning of `consumption_intervals` when
  quarter-hourly Elhub data starts flowing; no schema changes required.
- **Data retention & GDPR** — retention policy for consumption tied to former
  customers; audit logging of back-office access to consumption data.
- **Customer self-service** — the supply-period authorization query is the
  foundation if end customers ever get a portal.

## Products

### Phase 1 — Catalog enrichment (done)

Table-stakes catalog fields identified from industry research (ERPNext, Odoo,
Business Central, Shopify, Medusa, Akeneo): plain-text description, a multi-level
category hierarchy (single category per product), GTIN barcodes with check-digit
validation and uniqueness, and logistics fields (weight and dimensions).

*Unblocks:* a usable catalog UI, category-based navigation and reporting, barcode
lookup, and shipping-cost estimation groundwork for Orders/Warehouse.

### Phase 2 — Variants (done)

Split the model into **Product** (shared identity: name, description, category,
type, status, tax category) and **ProductVariant** (sellable identity: SKU, barcode,
unit, standard cost, prices, weight/dimensions, and option values such as
`Color=Red`, stored as a JSONB map). Every product has at least one variant;
single-variant products keep an inline UX (flattened API responses).
Migration: each existing product became a product with one default variant;
`ProductPrice.ProductId` moved to `VariantId`.

*Unblocks:* selling size/colour assortments without SKU duplication. Order lines
in the future Orders service reference variants.

### Phase 3 — Tax categories (done)

Replaced the per-product `VatRate` value with a reference to a **TaxCategory**
(Standard/Reduced/Zero/Exempt) whose rates are configured centrally in the
Products module. Migration seeded categories from the distinct existing rates.

*Unblocks:* rate changes without touching every product, differentiated goods vs.
food vs. exempt handling, and correct tax snapshots for Orders (responses embed
the resolved rate for snapshotting).

### Phase 4 — Pricing depth (align with Orders)

Extend `ProductPrice` with price lists, customer-group prices and quantity breaks,
with an explicit, documented precedence order.

*Unblocks:* B2B negotiated pricing and volume discounts; designed together with
the Orders service so order lines resolve prices the same way the catalog does.

### Phase 5 — Operational readiness (align with Warehouse)

Unit-of-measure conversions (base unit + conversion factor) and supplier records
per product/variant (vendor SKU, lead time, minimum order quantity).

*Unblocks:* purchasing and warehouse receiving; designed together with the
Warehouse service.

### Later

- **Bundles/kits** — explicit composition relationships between products.
- **Media/images** — needs a platform file-storage decision first.
- **Localization** — translated names/descriptions.
- **Typed custom attributes (PIM)** — schema-defined attributes per category.
- **Tags/collections** — the designated answer for cross-cutting, multi-assignment
  grouping (e.g. "Summer sale"), deliberately separate from the single-assignment
  category hierarchy.

## Projects

### Phase 1 — Projects, roles and billing lines (done)

A project per customer (or none, which means internal) with a unique, editable
code (`KVEM1000`) suggested from the customer and project names plus a running
number, a status the module never restricts (`planned`, `active`, `on-hold`,
`completed`, `cancelled` — only `active` means "open for work"), dates, budget
hours and the commercial rules invoicing will later read: billing type, fixed
price, budget amount and one currency per project. Projects are cancelled, never
deleted. Fixed roles per project (`manager`, `member`, `viewer`) sit under five
global permissions, with `projects:access` gating the app; an outsider gets 404
rather than 403, and financial fields are shaped out of the response rather than
forbidden. Billing lines pin a product variant to a pricing rule (`list`,
`fixed`, `discount`) under a short code, giving the trackable `KVEM1000-PM`.
Products is an **optional** dependency — without it the module is a full planning
tool and billing-line operations answer 409. Three contracts landed with it:
`contracts.UserDirectory` (identity), `contracts.ProductCatalog` (products) and
`contracts.ProjectDirectory` (projects), plus the platform's provider slots for
them.

*Unblocks:* Time tracking — a stable project identity, a code employees can quote,
per-project authorization, and a priced line to book hours against.

### Phase 2 — Time tracking and tasks (done)

Decided in `docs/superpowers/specs/2026-09-18-project-management-plan.md`
after surveying the Nordic ERPs, the international PSA tools and the dedicated
PM tools. Time is its own module (`time`) that requires Projects and reads
`contracts.ProjectDirectory`, never the `projects` schema; tasks live inside
Projects. A time entry references a project, optionally a billing line
(`KVEM1000-PM`) and optionally a task — the timesheet lists, under each project
code, the active lines and the caller's open tasks. Rates resolve through an
explicit chain (billing-line rule → project default → person default; cost
always from the person) and are snapshotted onto the entry; approval is a
state machine (`draft → submitted → approved → invoiced`) with period locking.

**Tasks.** Inside Projects, following the project's own roles and adding
no permission key: single assignee, the fixed statuses `todo`/`in-progress`/
`done`, one level of subtasks, checklist items, comments, dates and estimates;
a Tasks tab with list and board and a task drawer, plus `/projects/my-tasks`
across projects. This is where `member` and `viewer` stop being the same thing.
`ProjectDirectory` grew `Projects`, `ProjectByCode`, `BillingLines`, `Task`,
`OpenTasksForUser` and `CanLogTime` for Time to build on, and the project gained
`defaultBillRate`.

**Time tracking.** The `time` module and the `@vantigo/time-ui` app: entries with
hours or start/end times, the rate chain resolved at every save and frozen at
submission, effective-dated person rate cards, weekly submission from a grid,
batch approval and rejection by project managers and `time:approve` holders, a
period lock for `time:manage`, a people overview, the dashboard figures and a
Time tab on the project page. `invoiced` is terminal and waits for the module
that writes it. See [`docs/time.md`](docs/time.md).

*Unblocks:* hours that can be invoiced, the first real consumer of billing
lines, and a task list contractors and consultants will actually keep.

### Phase 3 — Budgets, billing milestones and costs (first delivery done)

Decided in `docs/superpowers/specs/2026-09-19-project-economy-design.md`, two
deliveries.

**Billing milestones and line budgets (done).** A project's invoice plan: a
billing milestone (name, optional planned date, a flat amount or a percent of
the fixed price) moving through `planned → ready → invoiced` with `cancelled`
off to the side, manual ordering, and a manual "mark as invoiced" step that
freezes the amount (a later Invoices module will set the same status). The
percent-of-price amount is computed exact-decimal, on read, so an open
milestone follows a later change to the fixed price; project guards refuse
clearing the currency or the fixed price while amounts still depend on them.
Line budgets: `budgetHours` (planning data) and `budgetAmount` (financial,
needs a currency) on a billing line, shown beside the line's pricing. Every
write that depends on the project's currency, fixed price or billing type
locks the project row first and decides under it — no separate ordering lock
turned out to be needed once that held. See
[`docs/projects.md`](docs/projects.md#billing-milestones-and-the-invoice-plan)
for the model, the status table and the guards.

*Unblocks:* fixed-price milestone invoicing recorded in Vantigo, and the
Economy tab's first half (`/projects/$projectId/economy`).

**Budget vs actual, portfolio and alerts (done).** A new optional contract
(`contracts.ProjectActuals`) through which Time supplies logged hours and
amounts, in three buckets (approved, submitted, draft), to Projects without
Projects ever reading the `time` schema; "budget used" resolved from one
basis per project (budget amount → fixed price → budget hours), on the exact
ratio, never the rounded percentage; the Economy tab's budget half (bars,
per-line table), now shown to everyone who sees the project rather than only
to its financial viewers; a project portfolio page (`/projects/economy`,
sidebar entry "Project economy"), filtered, sorted and capped at 2 000
projects; dashboard signals — a "ready to invoice" hint on the Projects card
and four attention types (a budget nearing or past 100 %, a ready or an
overdue milestone) linking to the Economy tab; `projects:view-costs`, a new
sensitive permission for cost and margin, granted to nobody by default. See
[`docs/projects.md`](docs/projects.md#project-economy) for the model, the
shaping rules and the dashboard signals, and
[`docs/time.md`](docs/time.md#what-time-reports-to-other-modules) for the
contract Time implements.

*Unblocks:* profitability and budget alerts, a project portfolio view.

**Next: supplier costs, overtime and work-type multipliers.** Expenses landed
separately — see [Expenses phase 3](#phase-3--expenses-on-the-project-page-done)
— and the rest were out of scope for this delivery and stay the concrete next
steps for project economics. Forecast / estimate-to-complete and
original-vs-revised budgets (tracking a budget's own history rather than
only its current value) are candidates worth deciding on once those land,
not committed work yet.

### Phase 4 — Delivery milestones, timeline and templates

Delivery milestones as a lightweight entity (name, optional target date, %
complete derived from linked tasks); a lightweight timeline (bars, drag,
finish-to-start dependencies, opt-in cascade) — never a full Gantt in-house;
project and task templates with relative dates.

*Unblocks:* planning at kickoff; the "are we on track" view.

### Phase 5 — Capacity planning

Allocations (person × project × date range × hours-or-percent,
tentative/confirmed, placeholders) as a table of their own, compared with
assignments and actuals; availability and utilisation.

*Unblocks:* "who is free in week 42"; revenue forecasts from planned hours.

### Later

- **Full Gantt, WIP / earned value, multiple assignees, a customer portal** —
  only on proven demand; the plan explains why every comparable product shipped
  these last or bought them in.
- **A Norwegian construction layer** — NS 8405–8407 milestone rules,
  *innestående* (retention), kontrollskjema, Boligmappa: unserved in the SMB
  tier today and a vertical of its own on top of phases 2–3.
- **Milestone codes** — if delivery milestones ever get codes they use a shape
  decided with phase 4, never a bare `<project>-<code>` suffix, which billing
  lines own.
- **Configurable project roles** — the three roles are named capability sets in
  code, so a role editor can be added without a schema change. It needs a real
  demand for a fourth role first.
- **Project documents** — files on a project, once the object-storage scopes and a
  document model are settled (Communications' attachments are the working example).
- **Domain events** — `ProjectCancelled` and friends once the event bus exists; see
  [Platform](#cross-module-domain-events-deferred-until-orders). Until then every
  consumer reads `contracts.ProjectDirectory` synchronously, which is enough.
- **Assigning users who lack `projects:access`** — a manager can today only pick
  from the user directory, and a colleague without the permission would be assigned
  a role they cannot use. Whether to filter the picker, warn, or grant on
  assignment is a product decision, not a technical one.

## Expenses

### Phase 1 — Foundation: entries, approval, receipts, reimbursement and invoicing (done)

The `expenses` module and `@vantigo/expenses-ui`: outlays and mileage with receipts
(JPEG/PNG/HEIC/PDF, sniffed rather than trusted, served only through the API), a
`draft → submitted → approved`/`rejected` flow with an admin-configurable receipt
rule and a rate override an approver can make on a submitted mileage line, then two
independent tracks after approval — reimbursed per expense by `expenses:manage`,
with a payroll CSV, and invoiced per billable line by whoever holds financial
rights on its project — either, both or neither, in any order. Dated rates
(mileage, its passenger supplement, and a customer rate per kilometre) and
categories are admin-managed and effective-dated the same way Time's person rates
are. Unlike every other module here, Expenses **depends on nobody but identity**:
Projects is read only optionally, through `contracts.ProjectDirectory`, for the
project a line is booked on and the billing figures its side prices — with
`projects` disabled every project-shaped field is refused on its own field rather
than accepted and dropped. The period lock protects what was submitted and
approved; it deliberately does not reach reimbursing, pricing or invoicing, which
are bookkeeping done once a period has closed. See [`docs/expenses.md`](docs/expenses.md).

*Unblocks:* a company's non-hours costs recorded and paid back, and a customer's
project a step closer to fully costed with Time's hours already in.

### Phase 2 — Travel claims and per diem (done)

A **travel claim** is the unit a trip's outlays, mileage and per-diem days are
grouped, submitted, approved and paid under: it carries the status, the
decision, the reimbursement and the project, while each line keeps its own
amounts, receipts and invoicing. **Per diem** is a day of the trip per line,
priced at the rate in force on that day less the percentage of every meal
somebody else paid for, with the days a trip's own times imply suggested by the
server and ticked off by the traveller. The per-diem and meal rate kinds phase 1
reserved are seeded from the state's *Særavtale om dekning av utgifter til reise
og kost innenlands* (in force 2026-01-01 to 2027-12-31), verified against the
source rather than written from memory, and are the administrator's to change
from there. Because a claim stores two instants, the installation gained a
**business time zone** — the one calendar the period lock, the per-diem window
and the payroll file are judged by. In the app: the trip's own page, travel
claims as units in the approval queue and the payroll list (one selection, one
request, across both kinds), and the rate kinds and time zone in settings. See
[`docs/expenses.md`](docs/expenses.md).

*Unblocks:* a whole trip recorded, approved and paid as one, and per diem
priced by the agreement instead of by hand in a spreadsheet.

### Phase 3 — Expenses on the project page (done)

Expenses joins Time's hours on the Economy tab (`/projects/$projectId/economy`)
as the other half of a project's actual cost, through an optional contract of
its own — `contracts.ProjectExpenses`, the same shape Time already provides
`contracts.ProjectActuals` through, so Projects never reads the `expenses`
schema and either module can be left out of an installation. It answers **per
currency and converts nothing**: a line carries its own currency, which may be
neither its travel claim's nor its project's. On the Economy tab that is a
costs section (the three buckets with counts, what they cost, what they pass on
to the customer), expenses inside the margin, and billable lines in "ready to
invoice"; expenses stay out of `budgetUsed` and the per-line budgets, which
remain about work. Beside it, the project page's own **Expenses** tab
(`/projects/$projectId/expenses`) shows the same money from the other side —
the totals per currency, the expenses behind them that the caller may open, a
"ready to invoice" filter that is exactly the figure above it, and **Record a
cost** with the project fixed. The totals and the list are gated differently on
purpose — the aggregate is the project's money, the rows are a colleague's
receipts — and the tab says so rather than showing an empty table. See
[`docs/expenses.md`](docs/expenses.md#on-the-project-page) and
[`docs/projects.md`](docs/projects.md#the-optional-expenses-dependency).

*Unblocks:* a project fully costed — hours and money side by side — and one
list of everything waiting to go on an invoice.

**Next: invoicing.** `invoiced_at` is still set by hand, per line, by whoever
holds financial rights on the project; "ready to invoice" is the list an
Invoices module would build from, and that module owns the stamp when it
arrives — the same thing [`docs/time.md`](docs/time.md#what-invoicing-will-read)
has said of an hour since phase 2. Supplier costs are the other gap already
named, under [Projects](#projects).
