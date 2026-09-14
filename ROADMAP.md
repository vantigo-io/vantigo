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
