# Manufacturing phase 2 — research: trace it and keep it right

Phase 1 of Manufacturing (work centers, BoMs, manufacturing orders, the live shop floor,
the production schedule, cost, make-to-order) is built as one stack. On **2026-11-12 the
owner said: start on phase 2, without waiting for input.** This note sets out what phase
2 builds and why, against what the products do and what a small Norwegian manufacturer
needs, and proposes the deliveries. The design spec
(`specs/2026-11-12-manufacturing-phase2-design.md`, "D") starts from §7 and §8 and does
not reopen them.

Code references are against the phase 1 stack's top, `claude/manufacturing-p2-plan` at
`c3b9a5a7b`, whose latest migration is `00161_orders_mto_ready.sql`. `main` is at
`949cef025` and its latest migration is `00155`: the phase 1 layers (`00156`–`00161`) have
not landed there yet, and phase 2 stacks on top of them. `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`. Phase 1's research is "R1", its
design "D1" (decisions M1–M48).

## 1. What is decided, and what this note adds

### 1.1 On the record

- **ROADMAP.md, Manufacturing** (kept since Inventory phase 1), three rules for lots:
  - lots belong to Inventory; the tracking mode (none, lot, serial) is a per-variant
    Products field that may change only at zero stock;
  - the balance key gains a nullable `lot_id`;
  - a production is one movement whose source names the producing document — built in
    phase 1 (D1 M1, M2).
- **Inventory research §6.1** (`research/2026-10-27-inventory-module.md`): *the trace
  screens are Manufacturing's; the records are Inventory's.* This note revisits the first
  half (§9 reading 6): the screen moves to Inventory, because receipts → deliveries is
  traceability for a wholesaler with no production at all.
- **R1 §7, phase 2:** lots and serials, the MO's finished and consumed lots, the trace
  screens; quality checks on operations; operation dependencies; alternative work
  centers and overlap; auto-computed standards; reorder rules creating MOs; by-products;
  kits; unbuild; split and merge; subcontracted operations; a shared-tablet kiosk with
  operator PIN.
- **R1 §9 reading 4:** *the operator is the signed-in user; a shared tablet with operator
  PINs is phase 2: an identity feature (a second credential), not a screen.*
- **R1 §9 reading 5:** a food or EN 1090 customer needs lots before going live.
- **2026-11-08, the owner** (Inventory phase 2): *do not stray far from what our
  competitors do.*
- **2026-11-12, the owner:** start phase 2 now, as a stack, no waiting. The readings in §9
  are therefore **answered by default**; the owner can overturn any in review, and D marks
  what each overturn would change.

### 1.2 What this note settles

- what the products do for each phase 2 topic (§2);
- what a 5–100 person Norwegian manufacturer, and the rules it lives under, need (§3);
- the domain model for lots, checks and replenishment (§4);
- the seams in the code (§5);
- the deliveries and their order (§6), and what waits for phase 3 (§7);
- the words (§8);
- the readings, with the default taken for each (§9).

## 2. What the products do

The lanes are R1's: **Odoo 18**, **Business Central** (BC), **MRPeasy**, **Katana**, and
the Nordic field (**Monitor G5**, **Rackbeat / Tripletex Logistikk Komplett**).

### 2.1 Lots and serials

| | Odoo 18 | BC | MRPeasy | Katana | Monitor G5 | Rackbeat |
| --- | --- | --- | --- | --- | --- | --- |
| Where it is set | per product: none / by lots / by unique serial | Item Tracking Code: SN-specific, lot-specific, warehouse tracking | per item; lots always exist, tracking decides if they are shown | batch tracking per item | traceability per part: batch or serial | batch add-on per product |
| One table for both | yes, `stock.lot`; a serial is a lot of quantity 1 | no: serial no. and lot no. are separate fields on the entry | lots only | batches; serials separate | batch / serial on one record | batches |
| Receipt without a lot | refused, cannot validate | refused when the code says so | lot is created automatically | batch required on tracked items | refused | partial batching allowed |
| Generating numbers | *Generate Serials/Lots*: first number + count | number series per code | auto, per receipt / production | manual | batch = PO number + position, or MO number | manual |
| Supplier's number | *lot reference* field | *Lot No.* on the vendor's line | vendor lot field | — | supplier charge number linked | — |
| Dates | expiration, best before, removal, alert — from per-product day offsets | expiration date; *strict expiration posting* | expiry date per lot | expiry per batch | — | best-before |
| Files on a lot | via chatter | — | **files on the lot** (certificates) | — | documents on the batch | — |
| Statuses | — (quality hold through locations) | blocked lot (lot info card) | **On hold, Rejected**, Available | — | blocked | — |
| Changing tracking | allowed, with a warning | blocked while entries exist | — | — | **refuses batch → serial with balances > 1** | — |

**Removal / picking.** Odoo sets a removal strategy (FIFO, LIFO, FEFO, closest) per
location or product category; FEFO needs expiry dates. BC picks *according to FEFO* when
the location says so. MRPeasy books by FIFO or FEFO. Every one proposes the lot and lets
the user change it.

**On the MO.** Odoo puts the finished lot on the MO (`lot_producing_id`), which *Produce
All* assigns automatically, and the consumed lots on the component moves; *Batch
Production* produces serials one by one. Monitor's batch for a produced item is the MO
number by default. Rackbeat refuses a production without batches for batch-tracked
components.

### 2.2 Traceability and recall

- **BC Item Tracing:** *Usage → Origin* and *Origin → Usage*, with *Show components*
  (all / item-tracked only / none) and a tree that marks lines already traced.
- **Odoo:** the *Traceability* report on a lot, upstream and downstream, through
  receipts, MOs and deliveries.
- **Katana:** a traceability explorer listing customers who received a batch and the
  batch genealogy.
- **Monitor:** *find using traceability* — *consists of* and *included in*.
- **Recall:** none of them has a recall object; each lists the downstream customers and
  leaves the contact to the user. Odoo and BC block a lot by status or quality hold.

### 2.3 Quality

- **Odoo Quality:** *control points* per operation, product or quantity; frequency all,
  random or periodic; check types *instructions, picture, pass–fail, measure* (norm and
  tolerance), *worksheet*; a failed check moves the item to a **failure location** and
  raises a **quality alert** (team, root cause, corrective action). In a work order the
  checks are the steps.
- **BC:** no native quality control (partners sell it).
- **MRPeasy:** a QC flag per item, an **on-hold period** after receipt or production, and
  inspections that approve or reject the lot.
- **Katana:** none built in.

### 2.4 Routing

- **Odoo:** *Blocked By* dependencies between operations (a DAG), off by default;
  **alternative work centers** tried when planning.
- **BC:** routings *serial* or *parallel*, with **send-ahead quantity** (the next operation
  starts after N units).
- **MRPeasy:** **overlap** quantity, *special sequences*, *parallelize* an operation over
  several workstations of a **workstation group**.

### 2.5 Replenishment

- **Odoo:** reordering rules *min / max / multiple*, trigger *Auto* (the scheduler creates
  MOs or POs) or *Manual*, and a **Replenishment** report that proposes and orders.
- **BC:** the planning worksheet with **action messages** (new, change quantity,
  reschedule, cancel).
- **MRPeasy:** *Critical on-hand* per item; a list of items below it with *Create MO*; no
  automatic MO.
- **Katana:** *make or buy* suggestions from the sales demand; never automatic.

### 2.6 BoM depth

- **By-products** with a **cost share** (Odoo); MRPeasy has *by-products* too.
- **Kits** (Odoo *phantom*, BC assembly-to-order): exploded at sale or delivery.
- **Unbuild** (Odoo): reverses a done MO, restoring the consumed lots; also free-standing
  with a BoM.
- **Split and merge** (Odoo): split an MO by quantities; merge MOs of the same product and
  BoM.

### 2.7 The shared tablet

- **Odoo Shop Floor:** an operator panel with **several active employees**, each switched
  in by picking their name and typing their **employee PIN**.
- **Katana Shop Floor app:** a **6-digit code** per operator and a device QR to sign the
  tablet in.
- **Monitor:** *recording terminals* with **card login**.
- Common to all: the PIN is a convenience credential, good only on the floor device.

## 3. What a 5–100 person Norwegian manufacturer needs

### 3.1 Food (Mattilsynet)

- **EU 178/2002 article 18** (*matloven* brings it into Norwegian law): a food business
  must identify **one step back** (who supplied it) and **one step forward** (which
  business it supplied), not to consumers. **Article 19:** withdraw, inform, recall.
- **Implementing regulation 931/2011** for food of animal origin: for each lot, the
  volume or quantity, the **lot identification**, the **dispatch date**, and the supplier
  and recipient.
- **Mattilsynet's 2024 campaign** audited *varsling, sporing og tilbaketrekking*: whether
  a business can say, quickly, where a lot came from and where it went.
- So the minimum is: a lot on every receipt, production and delivery of a tracked item,
  its expiry or best-before, the supplier on the receipt and the customer on the
  delivery, and a trace both ways within minutes.

### 3.2 Welded steel (EN 1090)

- **EN 1090-2** requires the constituent products to have **EN 10204 inspection
  documents**, typically **3.1** for structural steel. The heat (*charge*) number on the
  certificate must follow the material into the welded component.
- So the lot needs a **supplier lot / heat number** field and an **attached certificate
  PDF**, and the trace must reach from the finished assembly back to the heat.

### 3.3 Machinery and serial products

- A serial number per unit, on the delivery and the label; CE documentation per serial.
- Serials generated at production (one per unit), printed on a label.

### 3.4 Everyone

- **Labels**: GS1-128 with **(01)** GTIN, **(17)** expiry, **(10)** lot, **(21)** serial
  — what a wholesaler's goods-in scans. Variable-length fields need an **FNC1** separator
  unless they come last.
- **Quality at the point of work**: a measure with tolerance on the first piece, a
  photo, a sign-off; a deviation (*avvik*) when it fails — ISO 9001 shops already keep a
  deviation log.
- **Shared tablets**: one tablet per cell, operators switching in and out. Signing in to a
  full account per switch is what pushes shops back to paper.
- **Replenishment**: make-to-stock items with a reorder point, a *make these* list each
  morning.

## 4. The domain model

```
Products: variant.tracking (none | lot | serial), expiry_days, best_before_days
                      │
Inventory: lot (variant, number, supplier lot, dates, status, origin, files)
  balances (variant, location, lot) ── movement lines carry the lot
  count lines, transfer lines carry the lot
                      │
Manufacturing: production ── consumed per component per lot
                          └─ output lot(s) (pattern, serials one per unit)
Orders: delivery line ── picks per lot (FEFO proposed)
                      │
Trace (Inventory): receipts → productions (consume → produce in one movement) → deliveries
```

- **A serial is a lot** whose balance is at most 1, summed over every location.
- **A lot's status** is `available`, `quarantine` or `blocked`. Quarantined and blocked
  stock stays where it lies and is refused for issue and consumption.
- **Genealogy is the production movement**: within one movement, each consumed lot is a
  parent of each produced lot. No separate genealogy table.
- **Reservations stay per site, without a lot**: the lot is chosen at issue or consume.

## 5. Seams in the code

### 5.1 Inventory

- **The posting engine** (`srv/inventory/posting.go`). `balanceKey{variantID,
  locationID}` (:154) carries the comment *a lot joins it later*. `engineLine` (:115),
  `balanceKeys` (:426), `keyColumns` (:442), `lockBalances` (:455), `writeLines` (:474) and
  `writeBalances` (:500) each take the pair; the lot is a third member of each. `post`
  (:162) already holds a `VariantEntry` per variant after `LockVariants`, so the tracking
  judgement has its data.
- **The queries** (`srv/inventory/queries/ledger.sql`): `LockBalances` (`INSERT … ON
  CONFLICT (variant_id, location_id) DO UPDATE`), `SetBalances`, `DeleteZeroBalances` and
  `InsertMovementLines` unnest parallel arrays; each takes a lot array.
- **The tables** (`mig/00108`): `balances` has `PRIMARY KEY (variant_id, location_id)`;
  `movement_lines` has no lot. A nullable lot cannot sit in a primary key: the key becomes
  a unique index `(variant_id, location_id, lot_id) NULLS NOT DISTINCT` (the house already
  uses `NULLS NOT DISTINCT`, `mig/00004`, `mig/00108`).
- **Counts** (`mig/00110`): `count_lines` unique `(count_id, variant_id, location_id)`.
- **Transfers** (`mig/00152`): `transfer_lines.from_location_id` set at send;
  `transfer_action_lines` keyed `(action_id, transfer_line_id, kind)`; in transit derived
  from line totals. A line carrying one lot keeps every one of these as it is.
- **Reservations** (`mig/00125`): per site and document line, no location, no lot.
- **Placements** (`mig/00155`): reorder point and maximum per (variant, site); the rule
  is `srv/inventory/low_rule.go` (projected = available + incoming; target = maximum, or
  reorder point + 1).
- **Labels** (`srv/inventory/labels.go`): LOC tokens as Code 128 through maroto, which
  hands the text to boombuler's `code128.Encode`. That encoder takes **FNC1 as the rune
  `'ñ'`** (`code128/encodingtable.go`), so GS1-128 needs no new library.
- **No live topics** in Inventory today.

### 5.2 Contracts

- `contracts.Stock` (`srv/contracts/stock.go`:32): `OnHand`, `Available`, `Sites`,
  `Locations`, `SiteLocations`.
- `contracts.StockLedger` (:150): `Reserve`, `Release`, `Issue`, `ReservationsFor`,
  `ReserveForProduction`, `Produce`. `IssueLine` (:257), `ConsumeLine` (:283) and
  `ProduceLine` (:293) carry no lot. The refusal codes are the `const` block at :308–340.
- Callers of the ledger: Orders' `PostDelivery` builds `IssueLine`s
  (`srv/orders/deliveries.go`:794) and calls `Issue` (:807); Manufacturing's
  `postProduction` builds the produce line (`srv/manufacturing/productions.go`:501), calls
  `Produce` (:508), and splits consumption over locations in `splitConsumption`
  (:594–663).
- `contracts.VariantReferenceHolder` (`srv/contracts/references.go`): the purposes
  `delete` and `type_change` (:87–92). **Inventory's holder ignores the purpose**
  (`srv/inventory/holder.go`:36) and answers stock, then reserved, then in transit.
  Manufacturing's (`srv/manufacturing/holder.go`:36) answers only `delete` and
  `type_change`; Orders' (`srv/orders/holders.go`:470) only `delete`. A third purpose is
  therefore held by Inventory alone, which is what tracking needs.
- `contracts.VariantEntry` (`srv/contracts/catalog.go`) has no tracking field.

### 5.3 Products

- `product_variants` (`mig/00004`) has no tracking column. The variant's `PUT` is
  `srv/products/variants.go`:175; it locks the variant `FOR UPDATE` and does not ask the
  holders today. `variantHeld` (:358) asks them for a delete and a type change.

### 5.4 Orders

- `delivery_lines` (`mig/00126`): one location per line, and **one line per order line
  per delivery** (`ux_delivery_lines_order_line (delivery_id, order_line_id)`).
- `CreateDelivery` picks the first location holding the whole quantity
  (`deliveries.go`:461–467).
- PDFs: `srv/orders/deliverypdf.go`, the pick list rows (:138–166) and the packing slip
  rows (:167–184).

### 5.5 Manufacturing

- `production_lines` (`mig/00157`): `PRIMARY KEY (production_id, component_id)`, one
  location column. A lot needs a child row per (component, location, lot).
- Work orders run in a line: `snapshotWorkOrders` (`workorders.go`:81),
  `ReadyNextWorkOrder` (`queries/work_orders.sql`:87, called at `workorders.go`:557), and
  the planner's single cursor in `planChain` (`schedule.go`:105).
- `bom_operations.work_center_id` is one center (`mig/00156`).
- Placements have no contract.
- The MTO unique index `ux_orders_origin_line` (`mig/00160`) is the one a split must
  respect.

### 5.6 Identity and the platform

- Identity has sessions, passwords (**argon2id**, `srv/identity/passwords.go`), passkeys,
  TOTP and SSO; tokens are stored as **SHA-256 hashes** (`srv/identity/tokens.go`). It
  has no PIN, kiosk or device concept.
- `contracts.Principal` (`srv/contracts/access.go`) has `Via` for the assistant and
  agents: a station is a third `Via` kind.
- The router (`srv/module/router.go`, `wrap`) has the operation in hand after
  `Access.Check`: a station scope check fits there, keyed on an OpenAPI extension as
  `x-vantigo-access` is.
- `srv/ratelimit` is a fixed-window limiter in PostgreSQL, across replicas.
- **Object storage** exists: `srv/storage` (`ObjectStore`, `NewScope`, the `fs` driver,
  failing closed when unconfigured). Customers keeps attachments with an
  `object_deletions` ledger (`mig/00044`); Products and Invoices have their own scopes.
  Lot certificates and check photos follow that pattern.

## 6. The deliveries

| # | Delivery | What it brings | Depends on |
| --- | --- | --- | --- |
| L1 | Lots, core | Products tracking mode and offsets; Inventory lots, the balance key, receipts, moves, adjustments, the lots list and page, attachments, lookup, the product panel per lot, FEFO, expiry rules, lot-aware contracts | — |
| L2 | Lots, the rest of Inventory | counts, transfers and the opening import with lots; the expiring list; lot labels | L1 |
| O | Orders deliveries with lots | FEFO picks per lot, lots on the pick list and packing slip | L1 |
| M | Manufacturing lot capture | the output lot (pattern, serials), consumed lots per component | L1 |
| T | Traceability, block, recall, GS1 labels | the trace screen in Inventory, export, blocking with the downstream list | L1, O, M |
| Q | Quality checks and deviations | checks on operations, results, deviations, quarantine, inspection on receipt | M |
| R | Replenishment suggestions | reorder rules over placements, projected stock, the suggestions list | — (after M in the stack) |
| F | Routing upgrades | operation dependencies, send-ahead, work center groups | — |
| G | BoM depth | by-products, unbuild, split and merge | M |
| S | Shared stations | station devices, operator PIN and badge, the station scope | — |
| K | Kits (optional) | phantom BoMs exploded on Orders' deliveries | O |

L comes first because O, M, T and Q all stand on lots, and because it changes the
ledger's key: everything after it is written against the new key. S goes last: it is the
only identity work, it needs the mandatory security review, and it changes no stock rule.

## 7. Phase 3 and later

Deferred, as agreed in the brief: subcontracting (needs Purchasing); MPS and forecasts;
random and periodic QC sampling; packages and SSCC; lot rules per bin; ZPL printing;
recall campaign objects; parallelising one operation across a group; auto-computed
standard times (needs M42's report history to settle); kits if K does not fit.

## 8. Words

| English | Norsk | Note |
| --- | --- | --- |
| lot | parti | Tripletex and Rackbeat say *batch*; Mattilsynet says *parti*. The screen says *Parti*; the reference notes *batch*. |
| serial number | serienummer | |
| lot number | partinummer | |
| supplier lot / heat number | leverandørens parti / charge-nr. | |
| best before | best før | |
| expiry (use by) | siste forbruksdag | |
| quarantine | karantene | |
| blocked | sperret | |
| trace | sporing | |
| deviation | avvik | |
| quality check | kvalitetskontroll | |
| replenishment suggestion | påfyllingsforslag | |
| by-product | biprodukt | |
| unbuild | demontering | |
| station | stasjon | |
| operator PIN | operatør-PIN | |

## 9. Readings, with the defaults taken

The owner asked for no waiting (2026-11-12). Each reading states the default built; D
says what an overturn changes.

1. **A serial is a lot with quantity at most 1** — one table (Odoo, Monitor). The
   alternative, BC's separate serial and lot fields, doubles every key.
2. **Quarantine is a lot status, not a location.** Stock on hold stays where it lies;
   moving it to a quarantine shelf is still possible with a move. (Overturn: Odoo's
   failure location, a location flag that refuses issue.)
3. **Lots are chosen at issue and consume time.** Reservations stay per site without a
   lot; FEFO proposes the lot when the stock leaves.
4. **Tracking changes only at zero stock.** No guided *assign lots to existing stock* in
   phase 2; the opening import with a lot column is the migration path (Monitor refuses
   the same change).
5. **FEFO** when the variant has an expiry offset or its lots carry an expiry, else **FIFO**
   by received date. Expired lots are refused for issue and consumption without a new
   permission, `inventory:expired-override`.
6. **The trace screen lives in Inventory**, because the records are Inventory's and a
   wholesaler traces receipts to deliveries without Manufacturing. Manufacturing and
   Orders link to it. (This changes Inventory research §6.1's *the trace screens are
   Manufacturing's*.)
7. **Suggestions, not autonomous MOs.** A per-rule *create automatically* exists and is off
   by default (MRPeasy, Katana).
8. **The PIN is for the shop floor only**, never a credential anywhere else.
9. **A by-product's cost share applies to all cost**: materials, machine and labour (Odoo).

## Sources

Odoo 18 — [lots](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/inventory/product_management/product_tracking/lots.html),
[serial numbers](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/inventory/product_management/product_tracking/serial_numbers.html),
[expiration dates](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/inventory/product_management/product_tracking/expiration_dates.html),
[FEFO](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/inventory/shipping_receiving/removal_strategies/fefo.html),
[quality control points](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/quality/quality_management/quality_control_points.html),
[quality alerts](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/quality/quality_management/quality_alerts.html),
[work order dependencies](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/manufacturing/advanced_configuration/work_order_dependencies.html),
[work centers](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/manufacturing/advanced_configuration/using_work_centers.html),
[by-products](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/manufacturing/workflows/byproducts.html),
[bill of materials](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/manufacturing/basic_setup/bill_configuration.html),
[unbuild orders](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/manufacturing/workflows/unbuild_orders.html),
[split and merge](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/manufacturing/workflows/split_merge.html),
[shop floor](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/manufacturing/shop_floor/shop_floor_overview.html),
[reordering rules](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/inventory/warehouses_storage/replenishment/reordering_rules.html),
[kits](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/manufacturing/advanced_configuration/kit_shipping.html).

Business Central — [item tracking](https://learn.microsoft.com/en-us/dynamics365/business-central/inventory-how-work-item-tracking),
[item tracing](https://learn.microsoft.com/en-us/dynamics365/business-central/inventory-how-to-trace-item-tracked-items),
[picking by FEFO](https://learn.microsoft.com/en-us/dynamics365/business-central/warehouse-picking-by-fefo),
[routings](https://learn.microsoft.com/en-us/dynamics365/business-central/production-how-to-create-routings).

MRPeasy — [stock lots](https://www.mrpeasy.com/resources/user-manual/stock/lots/).
Katana (batch tracking, shop floor app, make-or-buy), Monitor G5 (traceability, recording
terminals) and Rackbeat / Tripletex Logistikk Komplett (batch add-on) are from their help
centres as summarised in the decisions brief of 2026-11-12; their article URLs were not
re-checked on that date *(u)*.

Regulation and standards — [Regulation (EC) 178/2002](https://eur-lex.europa.eu/eli/reg/2002/178/oj),
[Implementing Regulation (EU) 931/2011](https://eur-lex.europa.eu/eli/reg_impl/2011/931/oj),
[GS1 Application Identifiers](https://ref.gs1.org/ai/); EN 1090-2 and EN 10204 are paid
standards, cited by clause as the industry uses them; Mattilsynet's 2024 campaign on
*varsling, sporing og tilbaketrekking* is as summarised in the brief *(u)*.

*(u)*: not re-fetched on 2026-11-12. Every other link answered 200 that day.
