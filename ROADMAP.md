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

### Phase 2 — Time tracking and tasks (next)

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
Tasks: single assignee, a fixed status category under customisable labels, one
level of subtasks, checklist items, dates and estimates; list, board and "my
tasks". This is where `member` and `viewer` stop being the same thing.

*Unblocks:* hours that can be invoiced, the first real consumer of billing
lines, and a task list contractors and consultants will actually keep.

### Phase 3 — Budgets, billing milestones and costs

Budget vs actual per project and billing line (hours, cost, revenue: expected
→ to invoice → invoiced); **billing milestones** on the commercial side (date,
amount or % of the fixed price, a-konto as one kind), separate from delivery
milestones; expenses and supplier costs with markup; overtime and work-type
multipliers as billing-line rules.

*Unblocks:* fixed-price and a-konto invoicing, profitability, budget alerts.

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
