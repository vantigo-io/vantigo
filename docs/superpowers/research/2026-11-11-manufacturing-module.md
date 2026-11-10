# Manufacturing module — research

The owner asked on 2026-11-11 for **the next big app: Manufacturing** — *a system that can
plan, schedule and track manufacturing processes in real time*, modelled on Odoo
Manufacturing and similar software. This note surveys what the products do, sets it
against what Vantigo already has, and proposes phases. The design spec
(`specs/2026-11-11-manufacturing-design.md`) starts from §8 and does not reopen it.

The module is **Manufacturing** (`manufacturing`; Norwegian **Produksjon**; package
`@vantigo/manufacturing-ui`; permissions `manufacturing:*`). Code references are against
`main` at `430e73f33`, whose latest migration is `00155`. `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`.

## 1. What is decided, and what this note adds

### 1.1 On the record

- **ROADMAP.md, "Manufacturing"** (kept since Inventory phase 1): *a production consumes
  components and produces a finished item*. Inventory keeps three rules for it:
  - lots belong to Inventory: the tracking mode (none, lot, serial) is a per-variant
    Products field that may change only at zero stock;
  - the balance key gains a nullable `lot_id`;
  - **a production is one movement** with consume and produce lines, whose source
    reference names the production order.
- **Inventory research §6.1** (`research/2026-10-27-inventory-module.md`): the trace
  screens are Manufacturing's; the records are Inventory's. Lots, serials, expiry and
  FEFO wait for Manufacturing.
- **2026-11-08, the owner** (Inventory phase 2): *do not stray far from what our
  competitors do*.
- **2026-11-11, the owner:** research, plan, then implement as a stack of PRs without
  waiting for input. So the readings in §9 are **answered by default** in this note; the
  owner can overturn any of them in review, and the spec marks what each overturn would
  change.

### 1.2 What this note settles

- what manufacturing software does, and what a 5–100 person Norwegian manufacturer uses
  (§2, §3);
- the domain model and its fit with Inventory, Products, Orders and Staff (§4, §5);
- where the codebase stands, and the seams to cut (§6);
- the phases (§7) and the must-have list for phase 1 (§8);
- the readings, with the default taken for each (§9).

## 2. What the products do

Four lanes: **Odoo 18** (documentation and source), **Katana**, **MRPeasy**, **Business
Central**, and the Nordic field (**Monitor ERP**; Visma, Jeeves and 24SevenOffice
briefly). The Norwegian SMB accounting suites (Tripletex, Fiken, PowerOffice) have no
real manufacturing; that is the gap Vantigo fills.

### 2.1 Odoo Manufacturing

Odoo splits the area into Community `mrp` (BoMs, manufacturing orders, work orders, work
centers, unbuild, scrap, by-products) and Enterprise add-ons (Shop Floor, MPS, PLM,
Quality, Maintenance, Subcontracting). The split is a good proxy for what every
manufacturer needs versus what a small shop adopts late.

**Bills of materials** (`mrp.bom`, `mrp.bom.line`, `mrp.bom.byproduct`):
- kinds `normal` (manufacture), `phantom` (kit, exploded at sale) and `subcontract`;
- a product (template, optionally one variant) and an output quantity: lines are *per
  that quantity*;
- lines: component, quantity, *apply on variants*, *consumed in operation*, manual
  consumption;
- `consumption`: flexible / warning / strict, when actual differs from planned;
- `produce_delay` (manufacturing lead time) and `days_to_prepare_mo`;
- multi-level by nesting: a component with its own BoM gets its own MO (through MTO or
  reorder rules); kits explode inline;
- by-products with a cost share;
- **no per-line scrap %** — losses go through flexible consumption or scrap orders. A gap
  against Business Central.

**Operations and work centers** (`mrp.routing.workcenter`, `mrp.workcenter`):
- operations belong to the BoM (no separate routing since Odoo 14): sequence, work
  center, duration, dependencies (`blocked_by_operation_ids`), worksheet;
- duration *manual* (minutes per cycle) or *computed from tracked time* over the last N
  work orders — self-calibrating standards;
- a work center: capacity (units in parallel per cycle), time efficiency %, OEE target,
  setup and cleanup minutes, cost per hour (machine) and employee cost per hour, a
  working calendar, alternative work centers, `working_state` (normal / blocked);
- expected duration = setup + cleanup + ⌈qty / capacity⌉ × cycle × 100 / efficiency.

**Manufacturing orders** (`mrp.production`):
- states `draft → confirmed → progress → to_close → done`, or `cancel`, computed from
  the stock moves and work orders;
- components as reserved stock moves; `reservation_state` waiting / ready;
- produce: set the quantity producing, record actual consumption, *Produce all*;
  less than planned offers a **backorder** MO (`…-002`);
- split and merge; **unbuild** reverses a done MO; **scrap** from an MO or work order;
- one-, two- or three-step manufacturing (pick → produce → store) per warehouse.

**Work orders and the shop floor** (`mrp.workorder`, `mrp.workcenter.productivity`):
- states pending (waiting for another WO), waiting (components), ready, progress, done,
  cancel;
- **every start/stop is a productivity row** (work order, work center, employee, start,
  end, loss type productive / performance / availability / quality). Several employees
  can run timers on one work order. One table yields labour cost, duration deviation,
  auto standards and OEE;
- *block work center* with a loss reason;
- the **Shop Floor** app (Enterprise): a tablet kiosk, pages per work center, an
  operator panel with several signed-in employees, a work order card with timer,
  instructions, *Register production*, *Mark as done*, scrap, quality alert.

**Planning:** MO dates backward from the need by lead time (no capacity); *Plan*
schedules work orders forward from the MO start, each in the **first free slot of its
work center's calendar** (`_get_first_available_slot`), trying alternatives; Gantt by
production and by work center with drag. Finite capacity, one work center at a time, no
global optimisation.

**Replenishment:** MTO route (a sales line creates a linked MO, cascading to
sub-assemblies) and reordering rules (min/max → MO); MPS (Enterprise) for forecast-driven
make-to-stock.

**Lots and traceability:** tracking per product; the finished lot on the MO, component
lots on the consume moves; the traceability report upstream and downstream.

**Quality, maintenance, costing:**
- control points per product and operation; check types pass/fail, measure, picture,
  instructions — inside a work order the checks *are* the step sequence;
- equipment on work centers, preventive requests, MTBF/MTTR;
- real MO cost = components at valuation + work center time × rate + employee time ×
  hourly cost; the MO overview shows BoM, expected and real cost side by side.

**OEE:** Odoo's is a time ratio (productive ÷ logged), not availability × performance ×
quality.

### 2.2 Katana

Cloud MRP for small makers and e-commerce.
- **Make-to-order first-class:** an MO is either *make-to-order*, linked to one sales
  line, or *make-to-stock*. The sales order list shows sales-item availability,
  **ingredient availability** (in stock, expected, not available) and production status
  side by side; one click makes the MO from the line.
- **No hard reservations:** materials are allocated by order priority (drag to rank).
- **Shop Floor app:** a task list per operator or resource, start/stop with time capture,
  report consumption, finish.
- Recipes plus operations with resource, time and rate; costs roll up live.
- Weak: no finite-capacity calendar, thin multi-level, no quality or maintenance.

### 2.3 MRPeasy

Cloud MRP for 10–200 staff.
- **Finite capacity scheduling** forward (earliest slot with materials and workstation
  free) and backward from a due date; per-workstation calendars; one-minute resolution;
  parallelisation across workstations.
- Gantt by MO and by workstation, drag to reschedule.
- Workstation **groups** (schedule on any member), routings reusable across BoMs,
  **overlap** (the next operation starts after N units).
- **Delivery date from capacity** on a quote — valued by small shops.

### 2.4 Business Central

- **Two tiers:** light *assembly orders* (one level, no routing) in Essentials; full
  *production orders* with routings, work and machine centers in Premium.
- Statuses Simulated → Planned → Firm Planned → Released → Finished; only Released takes
  postings.
- **Scrap % on BoM lines and routing lines**; flushing (manual, forward, backward);
  BoM and routing versions with starting dates; subcontracted routing operations.
- The planning worksheet and journal types are enterprise surface small users find heavy.

### 2.5 The Nordic field

- **Monitor ERP** (Swedish, common in Norwegian workshops, e.g. Vestre): BoMs and work
  plans, production orders, *check delivery times* on capacity, picking, **operation and
  material reporting from shop-floor terminals**, machine integration for OEE.
- The Nordic small-shop pattern: **per-operation time reporting on a terminal against a
  production order, with job costing**; planning often a whiteboard or a load view.
- Traceability matters for food (Mattilsynet, EU 178/2002) and welded steel (EN 1090 / CE
  marking).

### 2.6 What every one of them has

BoMs with multi-level; MOs with component availability; operations with time per unit
on work centers; start/stop time capture per operation; consumption and output posted to
stock in one step; estimated versus actual cost; a load or Gantt view; lots on
regulated products.

### 2.7 What one or two have

Kits (Odoo, BC); scrap % per line (BC); auto-computed standards (Odoo); priority
allocation instead of reservation (Katana); delivery date from capacity (MRPeasy,
Monitor); workstation groups and overlap (MRPeasy); MPS (Odoo, BC); quality checks in
the step sequence (Odoo); maintenance (Odoo); subcontracting (Odoo, BC).

### 2.8 What nobody does well

- **Subcontracted operations** inside one's own order (galvanising, powder coating):
  Odoo needs a separate semi-finished product with a subcontract BoM.
- Explaining **why** an MO is not ready: availability states are terse.
- **Real time** without a kiosk licence: Odoo's live shop floor is Enterprise-only.

## 3. What a 5–100 person manufacturer uses

**Daily:** BoMs (multi-level, variants); MOs linked to sales orders; component
availability; operations with time per unit; operator start / pause / finish; posting
consumption and output; lots on regulated goods; actual versus estimated cost; a load
view; reorder points on make-to-stock items.

**Sometimes:** backorders, split; scrap; by-products (food, wood); subcontracted
operations; checklists; drawings at the station.

**Enterprise surface:** MPS with forecasts; ECO/PLM approvals; the full OEE loss
taxonomy and IoT; preventive maintenance with MTBF; statistical sampling;
pick-produce-store routes; simulated and planned order layers; a WIP account; portals;
global optimisation.

**Make-to-order dominates** Norwegian SMB manufacturing (job shops, furniture, steel,
food to order): the Orders → MO link and the availability view on the sales order matter
more than MPS.

## 4. The domain model

```
Work center ── calendar (weekly hours, the workspace's holidays)
     │
BoM (for a variant) ── lines (component, qty per output, scrap %)
     │               └─ operations (seq, work center, setup, minutes per unit, instructions)
     ▼ snapshot at confirm
Manufacturing order (MO-n) ── component lines (required, reserved, consumed)
     │                     ├─ work orders (one per operation) ── time logs (who, start, end, kind)
     │                     └─ productions (posted outputs) ──▶ Inventory movement "production"
     └─ origin: manual, a sales order line, or a parent MO
```

- **The BoM is snapshotted on the MO at confirm.** Later BoM edits never change an open
  order (every product does this; Odoo implicitly, through the generated moves).
- **A production is a posting**: one Inventory movement of kind `production` with
  consume lines (from-only) and produce lines (to-only). An MO may post several (partial
  output); each has its own id, so posting is idempotent by that source.
- **Time is rows**, one per person and interval, with a kind (`productive`, `downtime`
  with a reason). Labour cost, deviation and a later OEE come from the same table.
- **Work orders run in sequence** by operation order in phase 1; a dependency graph is
  phase 2.

## 5. Seams to the modules that exist

### 5.1 Inventory

- Movements, lines with optional from/to, a source reference on every movement, and kinds
  as an open set already exist (`mig/00108`, `srv/inventory/posting.go`). The `production`
  kind joins with a contract write.
- `contracts.StockLedger` (`srv/contracts/stock.go:127`) has `Reserve`, `Release`, `Issue`,
  `ReservationsFor`. Missing:
  - **`Produce`**: one movement with consume and produce lines;
  - reserving at a site that does not *fulfil orders*: `Reserve` refuses it
    (`srv/inventory/ledger.go:241`). Production stock often sits at a plant that sells
    nothing. A reserve for production must skip that check.
- **No valuation**: there is no average or FIFO cost (ROADMAP "Valuation waits for a
  ledger"). Cost in phase 1 is the variant's **standard cost**.
- No lots yet: no tracking mode, no `lot_id` (grep finds none).

### 5.2 Products

- `products.product_variants.standard_cost numeric(12,2)` exists but
  `contracts.VariantEntry` does not carry it. Manufacturing needs it for estimates.
- `unit` is free text; no conversions (Products phase 5). Phase 1 keeps quantities in the
  variant's own unit, as Inventory does.
- `VariantReferenceHolder` guards a delete or a Goods → Service change; Manufacturing adds
  a holder for BoMs and open MOs naming a variant.

### 5.3 Orders

- Orders reserves goods at confirm and issues on delivery (`srv/orders/stock.go`). Make-to-
  order does not exist.
- Events carry ids only (`orders.sales_order.confirmed`); a handler must read the lines
  through a contract — none exposes sales order lines today.

### 5.4 Staff, Planning, Time

- `contracts.Workforce` gives people, capacity and competences. Operators are users; the
  signed-in user is the operator in phase 1.
- Planning allocates people to **projects** by day or week: no fit for work-center hours.
  The manufacturing schedule is its own (**Produksjonsplan**), not Planning (**Bemanning**).
- Time entries require a project (`time.entries.project_id NOT NULL`): work-order time is
  kept in Manufacturing's own time logs. Feeding Time is later.

### 5.5 Platform

- **Live updates** (`srv/live`): topics, `live.Notify` on the writing transaction,
  `useLiveTopic` in the browser — the real-time shop floor and MO page come from here.
- **Domain events** (`srv/events`): handlers for `orders.sales_order.confirmed`.
- **Holidays** (`srv/holidays`): Norway's public holidays; `contracts.BusinessCalendar`
  gives the workspace zone. Work-center calendars build on them.
- **Operations dispatcher**: every OpenAPI operation is a tool for the Assistant and the
  agents; no module code needed.
- **Timeline UI**: Projects' timeline (`apps/projects/frontend/src/lib/timeline.ts`) and
  Planning's week grids are package-local; the schedule draws its own.

## 6. Where the codebase stands

- Latest migration `00155_inventory_placements.sql`.
- Inventory: sites, location trees, the ledger, reservations, transfers, placement.
- Products: variants with SKU, unit, standard cost, packagings; no tracking mode, no BoM,
  no kits.
- Orders: sales only; reservations and deliveries through `StockLedger`.
- The module checklist (`contributing/module-boundaries.md#adding-a-module`) and the
  registries in AGENTS.md.

## 7. Phases

**Phase 1 — make it, track it live, know what it cost.**
Work centers; BoMs with operations; manufacturing orders that reserve components and
post productions to Inventory; work orders on a live shop floor with time capture;
scheduling on work-center calendars with a load view; estimated versus actual cost;
make-to-order from Orders and sub-assembly MOs.

**Phase 2 — trace it and keep it right.**
Lots and serials (Products tracking mode, Inventory lots, the MO's finished and consumed
lots, the trace screens); quality checks on operations; operation dependencies;
alternative work centers and overlap; auto-computed standards; reorder rules creating
MOs; by-products; kits; unbuild; split and merge; subcontracted operations (with
Purchasing); a shared-tablet kiosk with operator PIN.

**Phase 3 — grow into it.**
OEE with a loss taxonomy and machine signals; preventive maintenance; an MPS grid; BoM
change control; WIP valuation when a ledger exists; portals.

**Left out:** simulated and planned order layers; pick-produce-store route configuration;
statistical sampling; IoT trigger configuration; global optimisation; strict consumption
blocking.

## 8. The must-have list (phase 1)

1. **Work centers:** code, name, site, capacity, efficiency, setup and cleanup minutes,
   machine and labour rates, a weekly working pattern, block and unblock with a reason.
2. **BoMs:** per variant, output quantity, lines with scrap %, operations with work
   center, setup and minutes per unit, instructions; multi-level; one default per variant;
   cycle refusal; the exploded overview with cost and availability.
3. **Manufacturing orders:** numbered `MO-n`; draft, confirmed, in progress, done,
   cancelled; the snapshot; components reserved; availability; produce partially;
   consumption that may differ with a warning; scrap; close short with or without a
   backorder; cancel releasing reservations.
4. **Work orders and the shop floor:** one per operation, in sequence; start, pause,
   finish per operator, several at once; register output at the last operation; downtime
   with a reason; a tablet screen per work center, live.
5. **Schedule:** the MO planned backward from its due date; *Plan* places work orders
   forward in each work center's first free slot; a board per work center and day with
   drag; load against capacity.
6. **Cost:** estimate at confirm (components at standard cost, expected time at rates)
   and actual at close, with the deviation; per MO and per product.
7. **Make-to-order:** an MO from a sales order line, automatic on confirm for BoMs that
   say so; production status on the order; child MOs for short sub-assemblies.
8. **Platform:** `StockLedger.Produce` and a production reserve; standard cost on the
   catalog contract; the holder; live topics; the docs in both languages.

## 9. Readings, with the defaults taken

The owner asked for no waiting (2026-11-11). Each reading states the default built; an
overturn in review is cheap where noted.

1. **Name.** *Manufacturing* / **Produksjon**; the schedule is **Production schedule** /
   **Produksjonsplan** so it never collides with Planning (*Bemanning*).
2. **Manufacturing requires Products and Inventory.** A manufacturer without stock
   records is not served in phase 1; an MO always posts to the ledger. (Overturn: a
   "no stock" mode marks output by hand, as Orders does when Inventory is off.)
3. **Components are reserved at confirm**, through Inventory as Orders does, not
   Katana's priority allocation. One rule for Available across modules. Reservation at a
   site not fulfilling orders is allowed for production.
4. **The operator is the signed-in user.** A shared tablet with operator PINs is phase
   2: it is an identity feature (a second credential), not a screen.
5. **Lots and serials are phase 2's first delivery.** They reshape Inventory's balance key
   and Orders' deliveries; phase 1 ships without them. (A food or EN 1090 customer would
   need phase 2 before going live.)
6. **Cost is standard cost**, since Inventory has no valuation; a production does not
   write a cost into the ledger.
7. **Scheduling is finite and forward per work center** (Odoo's first-free-slot), with the
   MO's start derived backward from the due date by lead time. No solver.
8. **Make-to-order is per BoM**: a flag *create manufacturing orders when a sales order is
   confirmed*. Otherwise a button on the order line.
9. **Work-order time stays in Manufacturing.** Feeding Time needs a project on every entry;
   later, if asked.
10. **Consumption:** *flexible with a warning* — no strict blocking.
