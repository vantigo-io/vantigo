---
title: "Manufacturing module"
description: "How the business makes its goods: the module and the two modules it requires; the six permissions and the cost shaping; work centers — code, site, capacity, time efficiency, setup and cleanup, the rates, the weekly working pattern on a 15-minute grid and the closures, archive, reactivate and delete; bills of materials — the product, the output quantity, lines with scrap, operations at work centers on one site, the client keys that link them, the replace-on-edit under a revision, the default and the alternatives, archive and the promotion of the next default; the lock order — LockVariants, then the BoM graph's advisory lock; the cycle and depth walk; the overview — requirements rounded up, made and bought, availability, M28a's expected minutes and the rolled-up cost; manufacturing orders — the number and the backorder suffix, the draft and its defaults, the states and what each lets change, the confirm that snapshots the BoM into component lines and reserves them in two passes, Reserve now, adding and changing component lines, availability computed live on the order page and the stored reservation status, the discontinued-variant warning, the list and its filters, the production with its proportional prefill, the 10 % consumption warning, the split over locations and the idempotent post through StockLedger.Produce, its lots — the lot pattern and its counter, the output lot through EnsureLots, serials, consumed lots named or taken with AutoLot, the one re-proposal, the deadlock retried once, the draft's lots and the order's lots read — close with or without a backorder, cancel, the history; work orders — the snapshot at confirm, the states, M28a's expected minutes fixed at confirm; time logs — start with one open log per user and force, pause, finish with an optional production, scrap, block and unblock with the downtime logs, the 12-hour flag, the supervisor's corrections and additions under the 60-day lock; the queue and My work; the shop-floor permission rule; the order lock order with the work orders, the work center and the time logs; the live topics and presence; the VariantReferenceHolder; personal data and the erasure sweep; the data model; the production schedule — the calendar in the business time zone, planning forward into slots from the requested start or its default, plan all, unplan, reschedule, the window and lateness, the board and its live topic, the schedule's advisory lock class 19783 before every row lock; the cost — the estimate at confirm, the running actual, the actual fixed at close with the cost per unit, the deviation and its rounding, the duration deviation; the reports — production cost, work-center utilisation and time deviation, in JSON or CSV; make-to-order — the manufacturing.mto handler and its requests, the manufacturing.mto-worker and its lease, the one live order per sales order line, the origin-cancelled flag, the manual order for a line, contracts.SalesOrderLines and contracts.Production with their classes, manufacturing.order.done; sub-assembly orders and their due date; quality — check templates on operations, their snapshot, results judged by the server and superseded, photos and their deletion worker, what the checks gate, deviations with their quarantine holds through the stock ledger, close and release, the lock order and the live topic; replenishment — projected stock from contracts.Stock.Placements, the module's own orders and contracts.SalesOrderLines.Unreserved, the suggestion rule with the multiple and the need-by date, the manufacturing.replenishment lease worker nightly and on demand, the suggestions and their actions, origin reorder and automatic creation; routing — operations as a graph with dependsOn, only on earlier operations and every branch joining the last, readying and planning by the graph, the send-ahead quantity and Register quantity, work center groups, the member planning or the shop floor chooses, its re-snapshot and when it is fixed, and the drags between members; BoM depth — by-products with their cost share, their snapshot, production and lots and the cost split, unbuilds from a chosen lot or by a BoM with the reverse movement and the quarantine of what a held lot restores, and the split and merge of orders under the schedule's locks; shared shop-floor stations — the operations open to them, the station's work centers checked before every handler, the lists cut down, the station recorded on what is done there and the live topics a station hears; the refusals, the switch and the endpoints.; kits — a kit BoM with lines only, never made, nesting three levels, and contracts.KitStructure for Orders"
sources:
  - apps/server/internal/manufacturing
  - apps/manufacturing/frontend
  - openapi/manufacturing.yaml
  - apps/server/internal/openapi/specs/manufacturing.yaml
  - apps/server/internal/db/migrations/00156_manufacturing_baseline.sql
  - apps/server/internal/db/migrations/00157_manufacturing_orders.sql
  - apps/server/internal/db/migrations/00158_manufacturing_work_orders.sql
  - apps/server/internal/db/migrations/00159_manufacturing_schedule.sql
  - apps/server/internal/db/migrations/00160_manufacturing_mto.sql
  - apps/server/internal/db/migrations/00167_manufacturing_lots.sql
  - apps/server/internal/db/migrations/00168_manufacturing_quality.sql
  - apps/server/internal/db/migrations/00171_manufacturing_replenishment.sql
  - apps/server/internal/db/migrations/00172_manufacturing_routing.sql
  - apps/server/internal/db/migrations/00173_manufacturing_bom_depth.sql
  - apps/server/internal/db/migrations/00175_manufacturing_stations.sql
  - apps/server/internal/db/migrations/00176_manufacturing_kits.sql
  - apps/server/internal/contracts/production.go
  - apps/server/internal/contracts/kit_structure.go
  - apps/server/internal/contracts/events_manufacturing.go
  - apps/host/frontend/src/routes/manufacturing
---
The Manufacturing module describes how the business makes its goods: the **work centers**
where the work is done, with their working hours, and the **bills of materials** (BoMs)
that say which components and which **operations** make one variant. In Norwegian the app
is **Produksjon**. The research is
[`2026-11-11-manufacturing-module.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/research/2026-11-11-manufacturing-module.md)
and the design
[`2026-11-11-manufacturing-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-11-manufacturing-design.md)
(decisions M*n*; its §15 amendments, such as M10a, win where they differ). This page
describes delivery A — the module, work centers, bills of materials, the BoM overview and
the holder — delivery B, [manufacturing orders](#manufacturing-orders), and delivery C,
[work orders and the shop floor](#work-orders-and-the-shop-floor), and delivery D,
[the production schedule](#the-production-schedule), [the cost](#cost) and
[the reports](#reports), and delivery E,
[make-to-order and sub-assembly orders](#make-to-order-and-sub-assemblies), which completes
phase 1. Of phase 2
([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-12-manufacturing-phase2-design.md),
decisions M49–M146), layer M adds [lots on productions](#lots-on-productions), Q [quality](#quality), R [replenishment](#replenishment) and F [routing](#routing): operation dependencies, the send-ahead quantity and work center groups, and K [kits](#kits). Delivery 0, the seams in Inventory and Products this module builds on, is
described in [Inventory](/en/reference/inventory/#production) and
[Products](/en/reference/products/).

## The module

The module is `manufacturing` (M6), its schema `manufacturing` (migration
`00156_manufacturing_baseline.sql`), its API `/api/v1/manufacturing` from
`openapi/manufacturing.yaml` and its frontend `@vantigo/manufacturing-ui`
(`apps/manufacturing/frontend`). It is a vertical-slice module inside the single Vantigo
binary (`apps/server/internal/manufacturing`).

It reads the catalog through `contracts.ProductCatalog` — searching goods variants, and
locking every variant a BoM write names (M10a) — and the sites and stock through
`contracts.Stock`: `Sites` and `SiteLocations` for the site a work center stands at, and
`Available` for the overview and an order's availability; `Locations` and `SiteLocations`
for an order's locations; `contracts.BusinessCalendar` for the business time zone the
[calendar](#the-calendar) runs in; and `contracts.StockLedger` — `ReserveForProduction`,
`ReservationsFor`, `Release` and `Produce` — for an order's reservations and productions.
`MODULES` therefore refuses `manufacturing` without
`products` or `inventory` (`manufacturing requires inventory`, naming the one missing).
Orders is an optional partner (delivery E): Manufacturing reads its sales order lines
through `contracts.SalesOrderLines`, and the customer's name through
`contracts.CustomerDirectory`, and gives it `contracts.Production`; with Orders off,
make-to-order does nothing and everything else works as before
([Make-to-order](#make-to-order-and-sub-assemblies)).

The schema holds `counters`, `work_centers`, `work_center_closures`, `boms`,
`bom_operations` and `bom_lines` (§9, migration `00156`), and, from delivery B, `orders`,
`order_components`, `productions`, `production_lines` and `order_events` (migration
`00157_manufacturing_orders.sql`; see [the data model](#the-data-model-of-orders)), and,
from delivery C, `work_orders` and `time_logs` (migration
`00158_manufacturing_work_orders.sql`; see
[the data model of the shop floor](#the-data-model-of-the-shop-floor)), and, from
delivery D, `work_order_slots` and the order's `requested_start`, `estimate` and `actual`
(migration `00159_manufacturing_schedule.sql`; see
[the data model of the schedule](#the-data-model-of-the-schedule)), and, from delivery E,
`mto_requests`, the order's `origin_cancelled` and the one-live-order-per-line index
(migration `00160_manufacturing_mto.sql`; see
[the data model of make-to-order](#the-data-model-of-make-to-order)). `boms` carries a partial unique index on
`variant_id` where the BoM is the default and active, which makes "one default per
variant" a database fact.

The app's sidebar has, in order (M7), **Overview** (`/manufacturing`), **Manufacturing
orders** (`/manufacturing/orders`), **Shop floor** (`/manufacturing/shop-floor`, with
**My work** at `/manufacturing/shop-floor/mine`), **Production schedule**
(`/manufacturing/schedule`), **Bills of materials** (`/manufacturing/boms`) and **Work
centers** (`/manufacturing/work-centers`). Its Norwegian labels are *Oversikt*,
*Produksjonsordrer*, *Verksted*, *Produksjonsplan*, *Stykklister* and *Arbeidsstasjoner*.
**Reports** (`/manufacturing/reports`, nb *Rapporter*) is reached from the Overview's
header, not the sidebar. The spotlight offers **Open the production schedule** and
**Manufacturing reports** to every holder of `manufacturing:access`. The Overview reads
`GET /stats`: the active work centers, the blocked ones and the active BoMs, and
`ordersToStartToday` (confirmed orders not started whose planned start is today or
earlier), `ordersInProgress` and `ordersLate` (open orders due before today), with
`today`, the business calendar's day they are counted on. Its list of orders late or due
this week is a read of `GET /orders` with `dueTo` the Sunday on or after that `today`, so
the week is the business calendar's, not the viewer's. It shows each order's due date and
status, not its availability, which only the order page computes
([Availability](#availability)). The counts and the list are read again on the
`manufacturing.orders` live event ([Live](#live)).

## Permissions

| Key | Display (en / nb) | Covers | Sensitive | Delegable |
| --- | --- | --- | --- | --- |
| `manufacturing:access` | Use Manufacturing / Bruke Produksjon | See the app; read the meta, the stats, the sites and their locations, the variant and people searches, work centers and their closures, BoMs and their overview, manufacturing orders, their history, their production prefill and their productions; the work orders, the time logs, a work center's queue and My work; the schedule board; the utilisation and time-deviation reports | no | yes |
| `manufacturing:boms-manage` | Manage bills of materials and work centers / Administrere stykklister og arbeidsstasjoner | Create, update, archive, reactivate and delete work centers; add and delete closures; create, update, archive and reactivate work center groups; create, update, archive and reactivate BoMs and make one the default | no | yes |
| `manufacturing:orders-manage` | Manage manufacturing orders / Administrere produksjonsordrer | Create, update, confirm, reserve, close, cancel, split and merge manufacturing orders; unbuild finished goods; add and change their component lines; register a production; every shop-floor action; correct a time log and add a missing one; plan, plan all, unplan and reschedule, and set an order's requested start | no | yes |
| `manufacturing:shop-floor` | Work on the shop floor / Arbeide i verkstedet | Register a production; start, pause and finish a work order, report scrap, block and unblock a work center | no | yes |
| `manufacturing:costs-view` | See manufacturing costs / Se produksjonskostnader | Every cost field in a response — an order's `cost` among them — writing a work center's rates, and the production-cost report | **yes** | **no** |
| `manufacturing:quality-manage` | Manage quality / Administrere kvalitet | Close a deviation, which releases its own holds unless it is closed as scrap or return, and release the holds of one closed so (M102); release the quarantine an unbuild of a held lot put on what it restored (M123) | no | yes |

Every read is checked against `manufacturing:access`; every write of a work center or a
BoM against `manufacturing:access` **and** `manufacturing:boms-manage`, and every write
of a manufacturing order against `manufacturing:access` **and**
`manufacturing:orders-manage`, so a role that gives either without `access` gives
nothing. **Registering a production and the shop-floor actions** are the exception
(M33): the route of `postManufacturingProduction`, `startManufacturingWorkOrder`,
`pauseManufacturingWorkOrder`, `finishManufacturingWorkOrder`,
`reportManufacturingWorkOrderScrap`, `blockManufacturingWorkCenter` and
`unblockManufacturingWorkCenter` asks for `manufacturing:access`, and the handler then
requires `manufacturing:shop-floor` **or** `manufacturing:orders-manage`, refusing **403**
`shop_floor_required` with neither. Correcting a time log and adding a missing one are
ordinary order writes: `access` **and** `orders-manage`. The Assistant calls the writes in
its confirm mode, except those seven: each is `x-vantigo-assistant: never`, because its
route names only `manufacturing:access` and the Assistant's gate cannot see the handler's
either-or — and an agent does not clock in (§10). The stock figures are Manufacturing's own reads of `contracts.Stock` and need no
Inventory key (§8); the variant search is Manufacturing's own (`GET /variants`) and needs
no Products key. `GET /meta` answers the caller's `canManageBoms`, `canSeeCosts`,
`canManageOrders`, `canWorkShopFloor` and `canManageQuality`, and the currency, `NOK`.
Recording a check's result, uploading its photo and reporting a deviation follow the
shop floor's rule — `access`, then `shop-floor` or `orders-manage` in the handler — and
the first two are `x-vantigo-assistant: never`, as is reading a photo; a deviation on a
lot needs Inventory's `locations-view` and `stock-view` too; closing a deviation and releasing its
holds are checked against `access` **and** `quality-manage`. The suggestions, the runs and
a variant's replenishment rules are read with `access`; creating orders from suggestions,
dismissing one, starting a run and setting a rule with `access` **and** `orders-manage`.

### Cost shaping

Without `manufacturing:costs-view` every cost field is **left out** of the response —
absent, not `null` (M38): a work center's `machineCostPerHour` and `labourCostPerHour`, a
variant's `standardCost`, an overview node's `standardCost` and `extendedValue`, an
operation's `cost`, and the overview's `rolledUpCost` and `costIncomplete`; from delivery
D an order's `cost` object and every component line's `standardCost` (M38). The
production-cost report is all cost: its route asks for `manufacturing:access` **and**
`manufacturing:costs-view`, and refuses the platform's **403** without it. A caller
without the key who sends either rate on a work-center write — a value or `null` — is
refused **403** `costs_view_required`, before any other field is judged. On an update a rate left out keeps the stored value, and `null`
clears it — so a person without the key saves a work center and its rates stay.

## Work centers

A work center (M8; nb *arbeidsstasjon*) is a machine, a cell or a team where an operation
is done.

| Field | Rule |
| --- | --- |
| `code` | 1–16 characters of `[A-Z0-9]` after upper-casing, stored upper-case, unique (`work_center_code_taken`, 409) |
| `name` | 1–200 characters |
| `siteId` | An active Inventory site, read through `Stock.Sites` (`site_unknown`, 400, on `siteId`); the response adds `siteCode` and `siteName`. It does not change while an active BoM has an operation at the work center ([One site](#operations)) |
| `capacity` | Units processed in parallel per cycle; an integer of 1 or more, default 1 |
| `timeEfficiency` | A percentage from 1 to 500, default 100; at 50 the work takes twice as long |
| `setupMinutes`, `cleanupMinutes` | Decimals of 0 or more, default `"0"`, applied once per work order |
| `machineCostPerHour`, `labourCostPerHour` | Exact decimals in NOK, optional and nullable; [cost-shaped](#cost-shaping) |
| `pattern` | The weekly working pattern (M9) |
| `workingState` | `normal` or `blocked`; changed only through [block and unblock](#block-and-unblock), never by an edit (M30) |
| `status` | `active` or `archived` |
| `revision` | The optimistic-concurrency token; a stale one is `revision_conflict` (409) |

A work center is ordered by code in its list, which filters by `status` (`active`, the
default, `archived` or `all`), `siteId` and a search `q` on its code or name.

### The weekly working pattern

The pattern (M9) is the work center's calendar: per weekday, `monday` to `sunday`, at most
**two intervals** `{start, end}` as `HH:MM` local times in the workspace zone
(`contracts.BusinessCalendar`).

- Every time is on a **15-minute grid**.
- `start` is before `end`; `24:00` is allowed as an `end`.
- A day's two intervals do not overlap.
- The default is Monday to Friday `[{07:00, 15:00}]`, Saturday and Sunday `[]`.

The capacity of a day is the length of its open intervals. Norwegian public holidays
(`holidays.PublicHoliday`) are closed days, and so are the work center's closures. A
pattern that rotates week by week is later (§13).

### Closures

A closure is a date range `{fromDate, toDate, note}` with `toDate` on or after `fromDate`
(a field error otherwise), listed by `fromDate`. A work center keeps **at most 100**
(`closures_too_many`, 400). Closures are added and deleted, never edited.

### Archive, reactivate, delete

- **Archive** (`POST /work-centers/{id}/archive {revision}`) sets `archived`. An archived
  work center is not offered to a new operation (`work_center_unknown` names it on a BoM
  write); the BoMs whose operations name it keep it. Archiving is refused while a work
  order that is neither `done` nor `cancelled` names it, and while the work center is
  [blocked](#block-and-unblock) (`work_center_has_open_work`, 409). A confirm takes the
  work centers of the work orders it creates `FOR SHARE`, so an archive at the same moment
  waits for it and counts them.
- **Reactivate** sets `active` again, and is refused **409** `site_archived` when its site
  is no longer an active Inventory site. An update keeps a site that was archived since;
  only a change of `siteId` must name an active one (`site_unknown`, 400).
- **Delete** (`DELETE /work-centers/{id}?revision=`) is allowed only while no BoM
  operation, no work order and no time log names the work center; otherwise **409**
  `work_center_in_use`, and the answer is to archive it. It takes the BoM graph's lock before the work center's row, so it
  cannot pass a BoM write that is about to name the work center.

## Bills of materials

A BoM (M10; nb *stykkliste*) tells how to make one variant.

| Field | Rule |
| --- | --- |
| `variantId` | The variant made: goods, judged from `LockVariants` (`variant_not_goods`, `variant_not_found`, 400); fixed at create |
| `code` | Optional, 1–32 characters after trimming, unique among BoMs when given (`bom_code_taken`, 409); an empty or blank code is no code, `null` |
| `outputQuantity` | More than 0, at most three decimals (`quantity_invalid`); lines and operation times are stated per it |
| `isDefault` | Exactly one active BoM per variant is the default (M14a); asked for at create only, and the variant's first active BoM is the default whatever it says |
| `makeToOrder` | False by default; read by delivery E |
| `leadTimeDays` | An integer of 0 or more, default 0; read by later deliveries |
| `lotPattern` | The number of the lot its productions make, 1–40 characters, default `{MO}`; a sample render must be a lot number (`lot_pattern_invalid`, 400); an update that leaves it out keeps it ([lots on productions](#lots-on-productions)) |
| `note` | At most 2,000 characters |
| `status` | `active` or `archived` |
| `revision` | The concurrency token (`revision_conflict`, 409) |

A response also carries `siteId`: the site of its operations' work centers, or `null`
for a BoM without operations. A variant may have several active BoMs, for alternative
recipes. The list filters by `variantId`, `status` (`active` by default), `q`, `limit`
(default 50) and `offset`, newest change first, and answers the `total`. `q` matches a
BoM's code or note, or its variant's SKU or product name in the catalog (the first 200
catalog matches, inactive products included). A `limit` outside 1–200 is clamped to that
range rather than refused, and a negative `offset` is 0.

### Lines

At most **200** per BoM (M11; `too_many_lines`, 400). Each has:

- a component `variantId`, which must be goods (`variant_not_goods`, `variant_not_found`,
  the line's code);
- a `quantity` per `outputQuantity`, more than 0 with at most three decimals
  (`quantity_invalid`), in the component's own unit;
- a `scrapPercent` from 0 to 99.99, default `"0"`: the expected loss, added to the
  requirement;
- an optional `operationKey`, naming the operation that consumes the component
  (`operation_key_unknown` when no operation in the request has that key); the response
  carries `operationId` and `operationKey`;
- a `sortOrder` and a `note` of at most 2,000 characters.

The same component may appear on two lines.

### Operations

At most **50** per BoM (M12; `too_many_operations`, 400). Each has:

- a `key`, 1–40 characters and unique in the request: the client key lines refer to, so
  that lines and operations are created in one request. The response gives each
  operation its `id` as text for its `key`; an input operation that names an existing
  operation's `id` keeps it;
- a `sequence` — operations run in sequence order; two operations of one BoM with the
  same `sequence`, or the same `id`, are refused as field errors on the second;
- a `name`, 1–200 characters;
- a `workCenterId` for an **active** work center (`work_center_unknown`, 400), or instead a
  `workCenterGroupId` for an active [work center group](#work-center-groups) with an
  active member (`work_center_unknown`, 400 on it); exactly one of the two, else a field
  error on `workCenterId`;
- `dependsOn`, the keys of the operations it runs after ([Routing](#routing)), and
  `sendAheadQuantity`, nullable ([the send-ahead quantity](#the-send-ahead-quantity));
- `setupMinutes`, optional and nullable: when empty, the work center's setup applies;
- `minutesPerCycle`, more than 0: the time of one cycle of the work center's capacity;
- `instructions`, Markdown of at most 20,000 characters.

**One site (M12a).** A BoM whose operations name work centers at more than one site is
refused **400** `work_center_other_site`. The rule holds from the work center's side too:
an update that changes a work center's `siteId` while any **active** BoM has an operation
at it is refused **400** `work_center_other_site` on `siteId`. Archive those BoMs, or move
their operations to another work center, first; an archived BoM does not hold the site.
The update takes the BoM graph's lock before the work center's row, as every BoM write
does before it reads the work centers its operations name, so a site change and a BoM
write cannot pass each other.

### Editing

An update (M13; `PUT /boms/{id}`) **replaces** the BoM's lines and operations in one
request, under its `revision`. `variantId` and `isDefault` are not part of it: the product
is fixed, and the default changes only through make-default and archive. Removing an
operation a line referred to clears that line's operation. An archived BoM is not edited
(`bom_not_active`, 409).

A 400 names the failing fields in `errors`, keyed by camelCase path —
`lines[3].quantity`, `operations[1].workCenterId` — and a line's own refusals in `lines`,
each with its `index`, `code` and `detail`.

### The default, archive and reactivate

- The first active BoM of a variant becomes its **default**, whatever `isDefault` says. A
  create with `isDefault` true makes it the default and unmarks the old one in the same
  transaction.
- **Make default** (`POST /boms/{id}/make-default {revision}`) does the same for an
  existing BoM; it runs the [cycle and depth walk](#the-cycle-and-depth-walk) again
  (M14a), and is refused for an archived BoM (`bom_not_active`, 409).
- **Archive** sets `archived`. Archiving the default **promotes the oldest other active
  BoM** of the variant to the default, or leaves the variant with none (M14a). The
  candidate is walked first ([the cycle and depth walk](#the-cycle-and-depth-walk)): when
  it would now close a cycle or go more than 10 levels deep, it is not promoted and the
  variant is left with no default — the archive itself is not refused, and no younger BoM
  is tried. Make one the default by hand once its components are put right.
- **Reactivate** sets `active` again. It judges the product and the components again
  (`variant_not_found`, `variant_not_goods`), and the operations' work centers again: one
  archived while the BoM was is `work_center_unknown`, and work centers moved meanwhile
  so that they stand at more than one site are `work_center_other_site` (both 400). It
  re-runs the cycle and depth walk, and makes the BoM the default when the variant has no
  active default.

A unique-index violation on the default — two writers promoting at once — is answered
as **409** `revision_conflict` (M14a), and the client reloads.

## Locks

Manufacturing orders take [their own lock order](#the-order-lock-order). Every BoM write — create, update, archive, reactivate and make default — takes its locks
in one order, on its own transaction:

1. **`ProductCatalog.LockVariants`** first (M10a), with the BoM's variant and every
   component variant id. The goods-or-service judgement comes from what `LockVariants`
   answers, so a Products write that would turn a variant into a service waits for the
   BoM write or is seen by it, which keeps the [holder's](#the-variant-holder) answer
   true (rule 20 of the [module boundaries](/en/contributing/module-boundaries/)).
2. **`pg_advisory_xact_lock(19782, 1)`** (M14a): 19782 is `0x4D46`, "MF", the
   manufacturing class key, and key 1 is **the BoM graph**. It serialises the structural
   writes, so two writes cannot each pass the cycle walk and together close a cycle. The
   schedule has a class of its own, `19783` keyed by the work center
   ([The schedule's locks](#the-schedules-locks)).
3. Then the module's own rows.

The lock is released when the transaction ends.

## The cycle and depth walk

M14 refuses a BoM when any component, at any depth, is the BoM's own variant. The walk
starts from the BoM's lines and follows each component through **its default active BoM**;
a component with none is a leaf.

- Meeting the BoM's own variant is **400** `bom_cycle`, and the problem's `path` lists the
  cycle as `[{variantId, sku}]`, from the BoM's variant back to itself.
- Walking past **10 levels** is **400** `bom_too_deep`.

The walk runs on create, update, make default and reactivate, and on the candidate an
archive would promote, under the graph's lock.

**The levels above count for a default.** Other BoMs explode through a variant's
default BoM only, so when the BoM written is, or becomes, its variant's default, the
walk also counts the default active BoMs **above** it: the longest chain of variants
whose default BoM consumes the variant, read upward a level at a time. The levels above,
the BoM itself and the levels below together go at most 10 deep; past that is **400**
`bom_too_deep`. It applies to a create that becomes the default (the variant's first
active BoM, or `isDefault` true), an update of the default, a reactivate that becomes the
default, a make default, and the promotion on archive. An alternative that is not the
default counts only the levels below it, since no other BoM's tree passes through it. So
under a chain of ten, a BoM for the bought variant at its bottom is refused, though it is
one level on its own.

## The overview

`GET /boms/{id}/overview?quantity=&siteId=` explodes a BoM (M15, M15a). `quantity`
defaults to the BoM's `outputQuantity`; `siteId`, when given, adds availability.

**The tree.** The root node is the BoM's variant at `quantity`. Each child's requirement
is

```text
quantityRequired = line.quantity × parentQuantity / outputQuantity × (1 + scrapPercent / 100)
```

rounded **up** (ceil) to three decimals (M18), where `parentQuantity` is the parent
node's `quantityRequired` and `outputQuantity` the parent's BoM's. A component with a
**default active BoM** is **made** (`made: true`, `bomId` set) and the walk recurses
through that BoM; one with none is **bought**, a leaf. Each node carries its `depth`, and
a made node below the 10th level is **400** `bom_too_deep` — which the
[walk](#the-cycle-and-depth-walk) keeps from happening on every write; the overview checks
again. More than **2,000 nodes** is **400** `bom_too_large`
(M15a); `nodeCount` answers the size. A `siteId` that is not an active Inventory site is
**400** `site_unknown`.

**Availability.** With `siteId`, every node answers `available` from
`Stock.Available(variantIDs, &siteID)`, with the variant ids deduplicated and batched at
most 1,000 per call (M15a); without it, `available` is `null`.

**Expected minutes (M28a).** Each operation of a made node, and of the root, answers

```text
cycles  = ⌈(quantity / outputQuantity) / capacity⌉
minutes = setup + cleanup + cycles × minutesPerCycle × 100 / timeEfficiency
```

rounded half-up to the minute, where `quantity` is the node's, `setup` the operation's
`setupMinutes` or else the work center's, and `cleanup`, `capacity` and `timeEfficiency`
the work center's.

**Operation cost.** `cost = expectedMinutes / 60 × (machineCostPerHour +
labourCostPerHour)`, a `null` rate counting as 0.

**Standard cost and extended value.** Every node answers its own `standardCost`, from the
catalog (delivery 0, M4), and `extendedValue = quantityRequired × standardCost`, `null`
when it has none.

**The rolled-up cost.** M15 reads "the components at standard cost, using the leaf's
standard cost when a made component has none of its own". The module reads it this way,
walking from the root's children:

- a **bought** node adds its `extendedValue`;
- a **made** node **with** a standard cost of its own adds its `extendedValue`, and the
  walk does not descend into it for cost: its standard cost stands for its subtree,
  operations included;
- a **made** node **without** one is descended into: its children and its own operations
  count.

`rolledUpCost.components` is the sum of those values; `rolledUpCost.operations` is the
cost of the root BoM's operations plus the operations of every made node the walk
descended into; `total = components + operations`. A bought node with no standard cost
counts 0 and sets `costIncomplete: true`. Writing the rolled-up cost back to the
product's standard cost is later (§13).

**Rounding and display.** Quantities are exact decimal strings, rounded half-up to three
decimals except the requirement's ceiling; amounts are computed exactly and rounded
half-up to two decimals at display, in NOK. On the wire a quantity, a time in minutes
and a scrap percentage are the shortest text that says the value — `"34"`, `"1.5"`,
`"0.125"` — while an amount (a rate, a standard cost, an extended value, an operation's
cost and the rolled-up cost) always carries two decimals: `"12.50"`. `expectedMinutes` is
an integer, at most 10^12; an operation whose expected time at the quantity passes it is
**400** `bom_too_large`. Every cost field is [cost-shaped](#cost-shaping).

## Manufacturing orders

A manufacturing order (M17; nb *produksjonsordre*) makes a quantity of one variant by one
of its BoMs, at one site. Delivery B gives it its component lines, reservations,
productions, close, backorder, cancel and history; delivery C its
[work orders](#work-orders-and-the-shop-floor); its schedule and its cost are D's, and the
origins `sales_order` and `parent` [E's](#make-to-order-and-sub-assemblies), and `reorder` an order made from a [suggestion](#replenishment).

### The number

An order stores a `number` and a `suffix` (M17b). A new order takes the next `number`
from the module's counter (`counters` row `orders`) at creation, with suffix 0, and shows
as **`MO-<n>`**. A backorder keeps the root's number and takes the **next suffix of the
root's chain**, starting at 2: the first backorder of `MO-12` is `MO-12-2`, the next
`MO-12-3` — a backorder of `MO-12-2` is `MO-12-3`, never `MO-12-2-2`. `(number, suffix)`
is unique. A cancelled order keeps its number.

### Creating an order

`POST /orders` creates a **draft**.

| Field | Rule |
| --- | --- |
| `variantId` | A variant with an active BoM: **400** `bom_required` otherwise |
| `bomId` | An active BoM of the variant; default its default BoM |
| `quantity` | More than 0, at most three decimals (`quantity_invalid`) |
| `siteId` | An active Inventory site (`site_unknown`); default the site of the BoM's work centers, else the first active site by code (M17a). A site other than the one the BoM's operations stand at is **400** `work_center_other_site` |
| `outputLocationId` | Where the finished goods are put; default the site's root location (`RootLocationID`, M4a) |
| `componentLocationId` | Optional. Empty, each component is taken from its primary location (Inventory placement), then from the other locations holding it, by full code ([where stock is taken from](#where-stock-is-taken-from)) |
| `dueDate` | Optional, the day the goods must be ready |
| `requestedStart` | Optional, the day [planning](#planning) may start from (M35a); empty takes the default, `requestedStartDefault` |
| `priority` | `normal` (default) or `urgent` |
| `responsibleUserId` | Optional, a user found through `GET /people`, known to the directory and not disabled (400 on `responsibleUserId`) |
| `note` | At most 2,000 characters |
| `salesOrderLineId` | Optional: the order is made for this sales order line, origin `sales_order` ([A manual order for a line](#a-manual-order-for-a-line)); `manual` otherwise |

Both locations must be **active locations of the order's site**, read through
`Stock.SiteLocations`: **400** `location_unknown` otherwise (M17a). A draft's component
lines are not stored: it answers the BoM's lines as they are now, without ids, and follows
its BoM until it is confirmed.

**A discontinued variant** (§11). An order whose product, or a component line's variant,
is of a `Discontinued` product is created, confirmed and produced as any other — the item
is being used up — and every answer of the order while it is not done or cancelled,
the create's and the confirm's among them, carries `warnings: ["variant_discontinued"]`;
`warnings` is empty otherwise. The order page shows the warning as a notice.

### States

```text
draft ──confirm──▶ confirmed ──first production──▶ in_progress ──close──▶ done
  │                    │                                │
  └──cancel────────────┴──cancel (nothing produced) ────┴─▶ cancelled
```

An update (`PUT /orders/{id}`) replaces the order's fields under its `revision`
(`revision_conflict`, 409, on a stale one); a nullable field left out is cleared.

- A **draft** changes everything.
- A **confirmed** or **in-progress** order changes only its dates, priority, responsible
  user, note and locations. A change to a draft-only field — the variant, the BoM, the
  quantity, the site — is **409** `order_not_draft`. Its site and BoM are not judged
  again: an order whose BoM's work centers have moved to another site, or whose site has
  been archived, since it was confirmed still takes the edit. Only its two locations are
  judged, against `Stock.SiteLocations` of its own site (`location_unknown`, 400).
- A responsible user who changes must be known to the directory and not disabled; one
  disabled after being named stays through an edit that leaves them.
- `requestedStart`, a date or `null`, is the day [planning](#planning) may start from
  (M35a); like the other nullable fields, one left out is cleared. The order answers
  `requestedStartDefault` beside it, what planning uses when it is `null`, on a confirmed
  or in-progress order.
- **`done`** and **`cancelled`** are final: any write is **409** `order_not_open`.
  Undoing a production is *unbuild*, phase 2; until then an Inventory adjustment corrects
  a mistake.

`in_progress` is entered by the first production, or by the first
[work-order start](#start).

### Confirm and the snapshot

`POST /orders/{id}/confirm {revision}` is allowed on a draft only (`order_not_draft`,
409), and refused **409** `bom_not_active` when its BoM has been archived since. It
copies the BoM's lines onto the order as **component lines** (M18), so that later BoM
edits never reach it. Each line's requirement is

```text
required = line.quantity × order.quantity / bom.outputQuantity × (1 + scrapPercent / 100)
```

rounded **up** to three decimals. The line snapshots the variant's SKU, product name and
unit, the BoM line's operation, and the catalog's **standard cost** at confirm, which
[the cost](#cost) reads (M23a). The order records `confirmedAt`, creates its
[work orders](#work-orders), fixes its [estimate](#the-estimate) and reserves. A backorder's confirm, at close, creates its own
work orders the same way.

### Reservations

An order's components are reserved at its site through Inventory's
`StockLedger.ReserveForProduction` (M3, M20), with the order as the document —
`manufacturing/order/<id>` — and the component line's id as the line. Every write that
reserves — a confirm, *Reserve now*, a component line added or changed, a close's
backorder, the sub-assembly orders and the make-to-order worker — reads the business day
on the pool before its transaction and passes it as `today`, the day Inventory judges a
lot's expiry on: no ledger call makes a contract call inside the order's transaction
(manufacturing phase 2 M62). The reserve runs in
**two passes** (M20a):

1. every line that needs a reservation in **one** call, which is all-or-nothing;
2. when that is refused `insufficient_available`, a second call **without the lines it
   named**.

The quantity a line asks for is `required − consumed − scrapped − held`, where `held` is
what the line already holds, from `ReservationsFor`. Each line is therefore reserved in
full or not at all. Manufacturing keeps its own copy of each line's `reserved`, updated
under the order's lock whenever it reserves, consumes or releases.

**Confirm never fails for stock alone** (M20a), unlike a sales order's confirm: it
succeeds, and the answer — `ManufacturingOrderWithShortLines` — lists in `shortLines`
every line left unreserved with its `missing` quantity. **Reserve now**
(`POST /orders/{id}/reserve {revision}`) retries the same two passes on a confirmed or
in-progress order (`order_not_open`, 409, otherwise) and answers the same way.

Close and cancel **release** whatever an order still holds; a production consumes from
it (M2).

### Component lines after confirm

On a **confirmed** or **in-progress** order (`order_not_open`, 409, otherwise):

- **Add a component** (`POST /orders/{id}/components`): a goods variant
  (`variant_not_found`, `variant_not_goods`, 400), a `required` of more than 0, an
  optional BoM `operationId` and a note. The line is marked `added`.
- **Change a line** (`PUT /orders/{id}/components/{componentId}`): its `required`, of 0 or
  more but never below `consumed + scrapped` (**400** `required_below_used`, M18a), and
  its note.

Both carry the order's `revision`, and both adjust the line's reservation: a raised
requirement reserves the difference by the same two passes, a lowered one **releases**
the excess. Both answer `ManufacturingOrderWithShortLines`.

### Availability

On the **order page** — `GET /orders/{id}` and every answer of an order write — the
availability is computed **live** (M21, M21a); neither the overview nor the list computes
it. For each line:

- `remaining = required − consumed − scrapped`, at least 0;
- `available` is `Stock.Available` of the variant at the order's site;
- the line is **covered** when `reserved + available ≥ remaining`, the site's available
  being shared across the lines of one variant **in line order**;
- `missing` is what neither covers.

The order's `availability` is `ready` (every line covered), `partly_available` (some) or
`waiting` (none).

The **list** shows only the **reservation status** (`reservation`), from the stored
copies, on a confirmed or in-progress order (`null` otherwise): `reserved` (every line's
remaining reserved), `partly_reserved` or `not_reserved`. The list can be filtered by it.

### The list

`GET /orders` (M24) filters by:

- `status`, any of the five; default `draft`, `confirmed` and `in_progress`;
- `siteId`; `variantId`;
- `workCenterId`: the orders with a **work order** at the work center; a draft, which
  has none, matches when its BoM has an operation there;
- `dueFrom` and `dueTo`, a due-date range;
- `late`: due before today and not done or cancelled — every order also answers `late`;
- `origin`; `responsibleUserId`; `reservation` (only confirmed and in-progress orders);
- `q`, on the number (`MO-12`), the product's SKU and its name.

It sorts by `dueDate` (default), `number` or `priority`, and answers a page —
`page` from 1 to 100,000, `pageSize` 1–200, default 50 — with the total, as Orders' lists
do; a page out of range is 400.

### Productions

A **production** (M22; nb *produksjonsmelding*) records output against an order:
`POST /orders/{id}/productions`. It is allowed on a **confirmed** or **in-progress**
order only (`order_not_open`, 409), and needs `manufacturing:shop-floor` or
`manufacturing:orders-manage` beside `access` (`shop_floor_required`, 403).

| Field | Rule |
| --- | --- |
| `quantity` | Good output, 0 or more; 0 only with consumption, scrap or output scrap |
| `outputScrap` | Finished units produced and discarded, 0 or more, default 0 |
| `lines[]` | Per component line (`componentId`): `consumed` and `scrap`, 0 or more with at most three decimals, and an optional `fromLocationId` |
| `toLocationId` | Where the output is put; default the order's output location |
| `clientRequestId` | Required; makes the post idempotent |
| `acceptDeviation` | Post although a consumed quantity is more than 10 % off |

**The prefill** (M22a). `GET /orders/{id}/production-draft?quantity=&outputScrap=`
answers each line's proportional figure: with `base = quantity + outputScrap`,

```text
proposed = required × base / order.quantity
```

rounded half-up to three decimals, beside the line's `required`, `reserved` and
`remaining`. `quantity` defaults to what remains to produce. Lines left out of a post are
not judged and consume nothing.

**The consumption warning.** When `base` is more than 0 and any **sent** `consumed`
differs from its proportional figure by more than 10 % of that figure, the post is
**409** `consumption_differs`, and the problem's `deviations` list each such line with
its `expected` and `consumed`. The same request with `acceptDeviation: true` posts it.
Nothing is blocked outright.

#### Where stock is taken from

A line's `consumed + scrap` is taken from its `fromLocationId`; else from the order's
component location; else it is **split**: the variant's primary location at the site
first, then the other locations holding it, by full code, read through `Stock.Locations`
before the transaction. **`insufficient_stock`** is answered only when the site's total
falls short, or when Inventory refuses a location named. When Inventory refuses
`insufficient_stock` on a split line — another production took from those locations
between the read and the post — the transaction is rolled back and the post reads
`Stock.Locations` again and splits anew, at most three times in all; so two productions
at once both post while the site's total suffices.

#### Posting

Posting, in one transaction:

1. stores the production (`productions`, with its `production_lines`), with the poster's
   id and display name;
2. calls `StockLedger.Produce` with the production as its document —
   `manufacturing/production/<id>` — and the order as the reserving document: one
   movement of kind **Production** that takes the components off their locations and
   puts `quantity` on the to-location. Inventory's movement page links it to the order.
   `outputScrap` never goes into stock. The call carries `Today` and `ExpiredOverride`
   — the business day and whether the poster holds `inventory:expired-override` — read
   on the pool when the request was judged, before the transaction (manufacturing phase
   2 M62); a tracked product's produce line names the lot `EnsureLots` answered, and a
   tracked component's consume lines name their lots or ask for `AutoLot`
   ([lots on productions](#lots-on-productions)); the production keeps the answer's
   `MovementID`;
3. consumes from the order's reservations, as `Produce` does, and lowers the stored
   copies;
4. adds to each line's `consumed` and `scrapped`, and to the order's `produced` (good
   output only) and `outputScrap`;
5. moves the order to `in_progress`.

A production with nothing to move — output scrap only — stores no movement
(`movementId` `null`).

**Idempotence** (M22a). A first post answers **201** with the production. The same
`clientRequestId` with the same body is a replay: **200** with the first production, and
nothing posted again. The same id with another body is **409** `client_request_reused`.

**Inventory's refusals** — `insufficient_stock`, `site_archived`, `variant_not_stocked`,
`document_reused` — pass through as **409** under their own code, with `lines` naming the
component lines, and **nothing** is posted: the whole transaction is rolled back. A
refused line names a component line only when both its variant and its line id are that
line's; a refused produce line, whose line id is the order's, names none.

Producing **more** than the order's quantity is allowed; the order answers the overrun in
`overrun`, and the page shows it. `GET /productions/{id}` reads one production, with its
`outputLots` and each line's `lots`.

### Lots on productions

Phase 2's layer M (M80–M86) records lots on a production of a variant whose `tracking`
is `lot` or `serial` ([Products](/en/reference/products/#tracking)), as the catalog
answers it under `LockVariants`. Inventory keeps the lots
([Inventory](/en/reference/inventory/#lots)); Manufacturing chooses them and passes them
through `StockLedger` on its own transaction (rule 24).

**The lot pattern** (M80). A BoM's `lotPattern` (`boms.lot_pattern`, default `{MO}`) is
kept on the order at confirm (`orders.lot_pattern`; a draft answers the default). Its
tokens: `{MO}` the order's display number; `{YYYY}`, `{YY}`, `{MM}`, `{DD}` and `{YYMMDD}`
the production date; `{SKU}` the product's SKU; `{N}` a counter per **rendered prefix** —
the pattern rendered with `{N}` left in it — kept in `manufacturing.counters` as
`lot:<prefix>`, from 1, allocated under the counter row's lock after the order's lock.
Anything else is literal. A BoM save renders a sample (`MO-99999-9`, 31 December 2026, the
SKU, `{N}` 9999) and refuses one that is not a lot number — 1–20 characters `A–Z a–z 0–9 .
_ / -` — `lot_pattern_invalid` (400); a production whose rendered number is none is
refused the same, on `outputLot.number`, before any counter is taken when the prefix alone
is too long. The BoM editor judges the same sample, so a pattern whose preview
fits today but a later order's number outgrows is flagged before the save.

**The output lot.** A production of a lot-tracked product with good output more than 0
names one lot: `outputLot.number`, else the pattern rendered on the production date.
`outputLot.productionDate` defaults to `Today` (read on the pool before the transaction,
M62); `expiry` and `bestBefore`, when not sent, are Inventory's defaults on creation — the
production date plus the variant's `expiryDays` and `bestBeforeDays` (M84). Manufacturing
calls `StockLedger.EnsureLots` with source `manufacturing/production/<id>` after the
order's lock and before `Produce`; a lot that exists — the `{MO}` lot's second production,
or a number sent — is answered as it is, and only the dates the request **states** are
compared (`lot_dates_differ`, 400 on `outputLot`), never the production date, so
productions on different days add to one lot. A number the `{N}` counter hands out is
always a **new** lot: it is asked with `NewLot.MustCreate`, and when another document's lot
has it already — a number someone typed in the counter's shape — Inventory refuses it
`lot_exists`, the try is rolled back to a savepoint taken before it, and the next counter
value is taken under the counter's row lock, up to 100 times before the production is
refused `lot_exists` (400 on `outputLot.number`). `outputSerials` or an output lot for an
untracked product is `lot_not_allowed` (400). Output scrap never makes a lot.

**Serial output** (M81). A serial product's good quantity must be a whole number of at
most 500 (`serial_quantity`, 400). Each unit is its own lot and its own produce line of 1:
`outputSerials.numbers`, one per unit (`serial_quantity` otherwise; an empty list is a
field error); or `outputSerials.first`, counted up by its trailing digits keeping their
width; by default `{MO}-0001` upwards, continuing after the **highest** `{MO}-<digits>`
serial the order's productions made — not after their count, which a serial typed in that
shape would make collide. Every serial is a **new** lot (`NewLot.MustCreate`): one that
exists already, in stock or delivered, is `lot_exists` (400, on
`outputSerials.numbers[i]`, or on `outputSerials.first` for a counted or default one), so a
new unit never takes a shipped unit's identity.

**Consumed lots** (M82). A line of a tracked component may send `lots: [{lotId,
locationId, consumed, scrapped, allowExpired}]`, each lot and location once, the rows
summing to the line's `consumed` and `scrap` (`picks_mismatch`, 400; the line's
`fromLocationId` is then ignored). Each row is one `ConsumeLine` with its `LotID` and
`AllowExpired`, which Inventory honours only when the poster holds
`inventory:expired-override` (`ExpiredOverride`, read on the pool); a lot with an active
hold is `lot_blocked`, an expired one `lot_expired`, a lot that is not the variant's or
not at the location `lot_unknown`. A tracked line sent **without** lots is posted with
**`AutoLot`**: at its location, the order's component location, or split over the
locations whose **usable** lots — no hold, not expired on `Today` — hold it, Inventory
takes the lots first-expiry-first under its own locks and answers them in
`StockPosted.Picks`, which the production writes back, consumed first, then scrapped.
`production_lines` stays the per-component total; `production_line_lots` keeps the split,
with each lot's number and expiry as `Stock.Lots` read them before the transaction (a lot
taken that was not read starts the post again, as a split does; on the third attempt it is
**409** `revision_conflict`, so a row is never kept without its number). Before
`Produce`, a line's rows must still sum to its consumed and scrap, else
`insufficient_stock` (409). Refusals carry the component line and, in `lines[].lotId`,
the lot, mapped by the refusal's `Index`; a field refusal raised inside the transaction
names its field under `production.` when the production comes with a finish.

**Proposed again once** (M65a). A line sent with `lotsProposed: true` — the draft's
proposal as it was — that Inventory refuses `insufficient_stock` or `lot_blocked` on its
rows only (every failing line such a row) is proposed again from `Stock.Lots` read then —
at the line's location, else the order's component location, else the whole site — and
posted once more, outside the first transaction — unless the stock then covers a line only
in part, when the first refusal is answered: a production never posts part of a line for
the whole. A second refusal is answered; chosen lots are never proposed again. The request's hash is the request's, so a replay of it still
matches.

**The draft** (M82). `GET /orders/{id}/production-draft` answers, per line, its
`tracking`; for a tracked component the `lots` proposed for `proposed` with
`contracts.ProposeLots` from `Stock.Lots` read on the pool — at the order's component
location when it has one — what they leave `short`, and `lotChoices`, every lot of the
variant at the site's active locations, FEFO, with expiry, best-before, `status`,
`expired` and quantity, so neither the order page nor the shop floor reads Inventory for
the picker. Its `output` answers the product's `tracking`, the order's `lotPattern`, the
`lotNumber` it renders today with `{N}` as the next the counter would hand out (nothing is
taken), the production date, the default `expiry` and `bestBefore`, and a serial
product's `serialFirst`. The production dialog leaves an unchanged proposal to `AutoLot`
and sends lots a person chose; when the person picks the line's location, it proposes again
from `lotChoices` at that location, as `AutoLot` will take them there, and it refuses a lot
row of 0 or a lot and location chosen twice before posting.

**The order's lots** (M83). `GET /orders/{id}/lots` (`access`) answers, from
Manufacturing's own rows, the `outputs` — each lot made with its quantity over the
productions and how many put output in it — and per component line the lots consumed with
their expiry, consumed and scrapped. The order page's **Lots** tab and the work order's
page on the shop floor show them, each linked to Inventory's lot page and to its trace
(M94) through the host's router — only when the meta's **`canTraceLots`** says so:
Inventory keeps stock (`contracts.Stock` is composed) and the caller holds both of its
views, `inventory:locations-view` and `inventory:stock-view`, which those pages need, as
Orders' delivery does. Otherwise the lot number is shown alone.

**The event** (M85). `production_posted` carries `outputLots`, the lot ids made, and
`lotRows`, the count of consumed lot rows.

**Deadlocks** (M86). The lock order is [the order lock order](#the-order-lock-order)'s,
with the ledger step split: **7a** `EnsureLots` (Inventory's lot counter, new lots, the
lots), **7b** Q's holds, **7c** `Produce`. Two productions of one order are serialised by
the order row, so the shared `{MO}` lot is never inserted twice. A production that
PostgreSQL chooses as a deadlock's victim (`40P01`) runs again once from the start; a
second deadlock is **409** `revision_conflict`. The integration suite races two
productions and a hold on one lot on a pool of four, and closes a cycle on purpose: a
writer taking the consumed lot and then the output lot `FOR NO KEY UPDATE` around a
production, which answers 201 having consumed and produced once.

### Close and the backorder

`POST /orders/{id}/close {revision, backorder}` (M23) on a confirmed or in-progress order
(`order_not_open`, 409):

- releases every remaining reservation;
- ends every open time log of its work orders at now, and cancels every work order that
  is not `done` (M23, M29a);
- sets `done` and `closedAt`.

An order with good output is first [gated on its checks](#what-the-checks-gate): every
check of a work order not cancelled has a result, or it is **409** `checks_incomplete`
naming them. When `produced` is less than `quantity`, the request must say `backorder`:
absent, it is **409** `quantity_short`.

- `true` creates a **backorder** for `quantity − produced`: the same BoM, site, locations,
  origin, due date, priority and responsible user, numbered with the root's next suffix
  and linked by `backorderOfId`. As the original was confirmed, the backorder is
  **confirmed at once** and reserves by the two passes; the answer's `shortLines` name its
  lines left short. This is the one confirm that does not refuse an archived BoM: the
  backorder keeps its original's BoM even when it has been archived since, while a new
  order or a draft's confirm on it is `bom_not_active`.
- `false` closes short.

The answer is `{order, backorder, shortLines}`. Close fixes the order's
[actual cost](#the-actual-and-the-deviation) (M23a, M41). A backorder does not carry its
original's plan: its work orders are new, and it is planned on its own; its estimate is
fixed at its own confirm.

### Cancel

`POST /orders/{id}/cancel {revision}` cancels a draft, a confirmed or an in-progress order
while **nothing** has been produced, consumed or scrapped — **409**
`order_has_production` otherwise — releases every reservation, ends every open time log of
its work orders at now and cancels every work order that is not `done` (M29a). A done or
cancelled order is `order_not_open`; an order split into parts that are still open is
**409** `order_has_open_parts` ([split](#split-and-merge)). The number is kept.

### History

Every order keeps an append-only timeline (M26), `GET /orders/{id}/events`, oldest first.
Each event has its `kind`, `at`, the actor's `actorUserId` and `actorDisplay`, and a
`payload` with its figures as exact decimal text:

| Kind | When |
| --- | --- |
| `created` | The draft is created, or a backorder made from its original |
| `updated` | The order's fields change |
| `confirmed` | Confirm |
| `reserved` | Confirm and Reserve now, with the number of short lines and their SKUs |
| `production_posted` | A production, with its quantity, output scrap and lines |
| `line_added` | Add component |
| `line_changed` | A line's requirement changes |
| `closed` | Close |
| `cancelled` | Cancel |
| `backorder_created` | Close with a backorder, naming it |
| `time_log_corrected` | A supervisor's [correction](#corrections) of a time log |
| `time_log_added` | A supervisor's addition of a missing time log |
| `planned` | Plan, or plan all, with the planned window and the number of work orders placed |
| `unplanned` | Unplan |
| `rescheduled` | A work order moved on the board |
| `children_created` | [Sub-assembly orders](#sub-assembly-orders) made from it, naming them |
| `origin_cancelled` | Its sales order was cancelled ([the flag](#a-cancelled-sales-order)) |

### The order lock order

Every order, work-order and time-log write takes its locks in one order (M26a), on its
own transaction:

0. for a schedule write — plan, plan all, unplan, reschedule — **the schedule's advisory
   lock** of every work center it touches, in id order, before anything else
   ([The schedule's locks](#the-schedules-locks));
1. **`ProductCatalog.LockVariants`** on the orders' variants and their component
   variants, the set taken from a plain read first;
2. the orders' rows **`FOR UPDATE`**, in id order, then — for a draft, and for a write that names
   variants of its own, such as close's backorder — its BoM's row **`FOR SHARE`**, so a
   BoM edit, which takes that row `FOR UPDATE`, either commits before the re-read or
   waits for the order write's commit;
3. a re-read of the component variants — a draft's and a backorder's from its BoM's
   lines; when the set grew since step 1 — a line added meanwhile — the transaction
   starts again from step 1. The snapshot takes every line's SKU, name, unit and cost
   from what step 1 locked, and a line whose variant step 1 did not lock starts it again
   too. After four attempts the write is **409** `revision_conflict`;
4. the **work orders**, in id order;
5. the **work center**: `FOR SHARE` for a start, and for a confirm's work orders their
   work centers in id order, `FOR UPDATE` for a block or an unblock;
6. the **time logs**, in id order, then — for a correction or an addition — the person's
   advisory lock, **`pg_advisory_xact_lock(19784, hashtext(user_id))`**, before the
   overlap check, so two supervisors adding the same person's time on different orders at
   once cannot both pass it;
7. the ledger — `ReserveForProduction`, `Release`, `Produce`; a production of tracked
   goods calls `EnsureLots` first (7a), then `HoldLots` for each open quarantining
   [deviation](#deviations) of the order on the lots it answered (7b), and `Produce` last
   (7c), and is retried once on a deadlock ([lots on productions](#lots-on-productions)).
   Before it, a production with good output is [gated](#what-the-checks-gate) on the
   checks, read under the order's lock; a result takes the work order's check
   `FOR UPDATE` at step 4.

A shop-floor action that starts from a work order first reads that work order's order id,
then follows the order above. A **start** that ends the caller's open log on another
order's work order locks both orders, ids sorted, and both work orders the same way, so
two operators starting across each other's orders never deadlock. **Block** and
**unblock** first read the open logs at the center, lock their orders and work orders by
the order above, then the center `FOR UPDATE`; when the open logs at the center changed in
between, they start again, at most four times, and then answer **409**
`revision_conflict`.

Stock reads — `Stock.Available`, `Stock.Locations`, `Stock.SiteLocations` and
`Stock.Sites` — are made on the pool **before or after** the transaction, never inside
it. After a refusal from `Produce` the transaction is rolled back, not continued. Two
productions at once on one order therefore run one after the other.

### Live

Six live topics (M25, M32, M36, phase 2 §17), declared through `Module.LiveTopics`:

- **`manufacturing.orders`**, keyless: the list;
- **`manufacturing.order:<id>`**: one order;
- **`manufacturing.work-center:<id>`**: a work center's queue and its blocking, event
  **`work-center.changed`**;
- **`manufacturing.work-order:<id>`**: one work order, event **`work-order.changed`**.
  Presence is allowed on it: the work order view shows the others who have it open
  through presence;
- **`manufacturing.schedule:<siteId>`**: one site's [board](#the-board), event
  **`schedule.changed`**, told by plan, plan all, unplan and reschedule, and by what the
  board draws of a work center: block and unblock, a closure added or removed, and a work
  center's update (its pattern among it) — both sites when it moves;
- **`manufacturing.deviations`**, keyless: the [deviations](#deviations), event
  **`deviation.changed`**.

Every order write notifies the list's topic and the order's own with the event
**`order.changed`** carrying the order's id; a work-order or time-log write notifies its
work center, its work order, its order and the list. Each topic is notified **once per
aggregate** on the writing transaction, so it is heard only if the transaction commits. A
subscription is authorised by `manufacturing:access`, the GETs' rule. The screens refetch
on the event, and the list and the order page poll only while the live connection is
down. A running timer counts on the client from the log's `startedAt`: the server sends
no ticks.

### The data model of orders

Migration `00157_manufacturing_orders.sql` adds, in the `manufacturing` schema:

| Table | Holds |
| --- | --- |
| `orders` | The order: `number` and `suffix` (unique together), `variant_id`, `bom_id`, `quantity`, `produced`, `output_scrap`, `site_id`, `component_location_id`, `output_location_id`, `due_date`, `planned_start`, `planned_finish`, `priority`, `responsible_user_id`, `note`, the `origin_*` columns, `parent_order_id`, `backorder_of_id`, `status`, `confirmed_at`, `closed_at`, `revision` |
| `order_components` | A component line: `variant_id` with the `sku`, `product_name` and `unit` snapshots, `required`, `reserved` (Manufacturing's copy), `consumed`, `scrapped`, `operation_id`, `standard_cost` at confirm, `sort_order`, `added`, `note` |
| `productions` | A production: `client_request_id` (unique) and `request_hash`, `quantity`, `output_scrap`, `to_location_id`, `movement_id` (Inventory's movement, or null), `posted_by_user_id`, `posted_by_display`, `posted_at` |
| `production_lines` | What a production consumed and scrapped of a component line, and the `from_location_id` the request named |
| `production_line_lots` | Phase 2 (`00167`): what a production consumed and scrapped of a tracked component line per lot and location, with the lot's number and expiry as read, and `allow_expired`; the rows of a line sum to it |
| `production_outputs` | Phase 2 (`00167`): the lots a production made, with the number, whether a serial, and the quantity |
| `order_events` | The timeline: `kind`, `payload`, `actor_user_id`, `actor_display`, `at` |

Quantities are `numeric(14,3)`. A variant, a site, a location and a user are opaque ids
of their own modules, never a foreign key across a schema (rule 4 of the
[module boundaries](/en/contributing/module-boundaries/)).

## Work orders and the shop floor

Delivery C (M27–M33) gives a confirmed order its **work orders**, one per operation, and
records the time operators spend on them as **time logs**. The shop floor
(`/manufacturing/shop-floor`, nb *Verksted*, M31) is the tablet screen they work from:
the work center picker, a work center's queue, the work order view and My work.

### Work orders

A work order (M27, M27a; nb *arbeidsordre*) is one operation of the order's BoM as it was
at confirm. The confirm — and a backorder's confirm at close — creates one per BoM
operation, snapshotting:

- the operation's `sequence`, `name`, work center and `instructions` (Markdown);
- `setupMinutes`: the operation's setup, else the work center's; `cleanupMinutes`: the
  work center's; `minutesPerCycle`: the operation's;
- the work center's `capacity`, `timeEfficiency` and both cost rates (M27a), which
  delivery D's estimate reads;
- `expectedMinutes` ([below](#expected-minutes)).

Later edits of the BoM or the work center never reach a work order — but for a
[group work order's member](#the-member) changed before any time is logged. An order whose
BoM has no operations has no work orders, and a draft has none. The operations' graph is
copied between the work orders ([Routing](#routing)): those with no predecessor are
`ready`, the others `pending`.

| Status | Meaning |
| --- | --- |
| `pending` | A work order it depends on is not done, nor has passed on its send-ahead quantity |
| `ready` | It may be started |
| `in_progress` | A productive time log is open on it |
| `paused` | It was started, and no productive log is open; a block's downtime logs leave it `paused` (M29a) |
| `done` | Finished |
| `cancelled` | Its order was closed or cancelled before it was done |

A work order answers its order's number, product, quantity, `produced` and `remaining`
(`quantity − produced`, at least 0), due date, priority and reservation status; its
`loggedMinutes` — the productive minutes logged on it, open logs counted to now, rounded
down; `operators`, who has an open log on it; and `last`, true for the order's last work
order that is not cancelled. `GET /orders/{id}/work-orders` lists an order's work orders by
sequence. From delivery D a work order also answers its `plannedStart` and `plannedFinish`,
and its [duration deviation](#duration-deviation): `actualMinutes`, `deviationMinutes`
and `deviationPercent`.

### Expected minutes

M28a fixes the expected duration of a work order:

```text
cycles   = ⌈(order.quantity / bom.outputQuantity) / capacity⌉
expected = setup + cleanup + cycles × minutesPerCycle × 100 / timeEfficiency
```

rounded half-up to the minute, from the snapshot's figures. It is computed at confirm and
not recomputed: an order's quantity does not change after confirm. Phase 2 amends M28a in
one case (M118): a [group work order](#the-member) moved to another member before any time
log exists takes that member's setup (unless the operation set its own), cleanup,
capacity, efficiency and rates, and its expected minutes are computed again with them,
from the order's quantity and the BoM's output quantity as they were at confirm. Nothing
measured is lost: the member is fixed once a log exists. It is the same
formula the [overview](#the-overview) answers for an operation, with one difference: where
the overview refuses a figure past 10¹² minutes with `bom_too_large`, the snapshot at
confirm caps it at 10¹² and confirms.

### Time logs

A time log (M29, M30; nb *tidsregistrering*) is time at a work center from `startedAt` to
`endedAt`, which is `null` while it runs. Times are UTC instants (§11).

| Field | Rule |
| --- | --- |
| `kind` | `productive` — an operator's work — or `downtime` |
| `reason` | For downtime: `breakdown`, `material_missing`, `setup`, `waiting_for_operator`, `quality` or `other`; `null` for productive time |
| `user` | The operator, with their display name at the time; `null` on the work center's own downtime log, which has no work order either |
| `quantity` | What the operator registered at finish, exact decimal, or `null` |
| `forced` | The work order was started out of sequence (`force`) |
| `note` | At most 2,000 characters |
| `correctedBy`, `correctedAt` | The supervisor's last correction or addition |
| `longRunning` | Open for more than **12 hours**: the shop floor warns |

**One open log per user**, in the whole workspace: a partial unique index on the user
where the log is open. Logs are **not** closed automatically at the end of the work
center's day; one left running is flagged `longRunning` after 12 hours and stays open
until someone pauses, finishes or corrects it. `GET /work-orders/{id}/time-logs` and
`GET /orders/{id}/time-logs` list the logs by start.

### Start

`POST /work-orders/{id}/start {force?, workCenterId?}` opens a productive log for the
caller (M29, M29a). `workCenterId` starts a group work order at that member
([The member](#the-member)).

- The caller's open log **elsewhere** — on another work order, at any work center — is
  ended at now first, in the same transaction; the answer's `closedLog` names it.
- On a work order the caller **already runs**, it is a no-op that answers **200**.
- A **`pending`** work order is **409** `work_order_pending` without `force`; with
  `force: true` it starts, and the log is marked `forced`.
- A **`done`** or **`cancelled`** work order is **409** `work_order_closed`.
- At a **blocked** work center it is **409** `work_center_blocked`.
- On an order that is not `confirmed` or `in_progress` it is **409** `order_not_open`.
- Two starts by one user at the same instant trip the unique index: **409**
  `log_conflict`.

The work order becomes `in_progress`, and the order's first start moves it from
`confirmed` to `in_progress`. Several operators may have open logs on one work order.

### Pause

`POST /work-orders/{id}/pause` ends the caller's open log on that work order, productive
or a block's downtime. With no productive log left open on it, the work order is
`paused`. With no open log of the caller's there, it changes nothing and answers **200**.

### Finish

`POST /work-orders/{id}/finish {quantity?, production?}` ends **every** open log on the
work order and sets it `done`. Then every `pending` work order of the order whose
predecessors now allow it becomes `ready` ([Readying](#readying)). One started out of turn
with `force` is left as it is, and the ones after it wait until it is finished too.

- `quantity`, optional, is what the caller made at this operation, kept on the caller's
  open log. With no open log of the caller's on the work order it would be lost, and is
  **400** on `quantity`.
- `production`, optional, is a production body as `POST /orders/{id}/productions` takes
  it, posted in the **same transaction** through the same rules
  ([Productions](#productions)): the prefill, `consumption_differs`,
  `client_request_reused`, Inventory's refusals. On a refusal nothing changes — the work
  order is not finished and the logs stay open. The shop floor's **Register production**
  on the work order whose `last` is true posts to `POST /orders/{id}/productions` and
  leaves the work order running; only when the operator also ticks **Also finish this
  operation** does it send the production with the finish, which stops every operator's
  time on it.
- Finishing a `done` work order again with the same production's `clientRequestId`
  answers **200** with that production; otherwise a `done` or `cancelled` work order is
  **409** `work_order_closed`. A `pending` one is **409** `work_order_pending`.

The answer of start, pause and finish is `{workOrder, closedLog, production}`: the work
order view after the action, the log a start closed elsewhere, and the production a
finish posted.

### Report scrap

`POST /work-orders/{id}/scrap {clientRequestId, lines, outputScrap?}` posts a production
of the work order's order with **no good output**: the components' scrap, per component
line, and the output scrap (M22, M31). It goes through
[the production's post](#posting), idempotent by `clientRequestId`: **201** first, **200**
with the first production on a replay, `client_request_reused` with another body.

### Block and unblock

`POST /work-centers/{id}/block {reason, note}` (M30, M29a), with a downtime `reason` and a
note of at most 2,000 characters:

- sets the work center's `workingState` to `blocked`;
- ends every **productive** log at the center at now, and opens one **downtime** log per
  operator from now with that reason, on the same work order — which is left `paused`;
- opens the center's own downtime log, with no user and no work order, so the stopped
  time is counted.

The `note` is the blocker's words, not the operators': every log the block opens records
the blocker as its `note_by`, and the center's own log thereby records who blocked it,
noted or not.

Blocking a blocked center changes nothing and answers **200**. While it is blocked,
starts there are **409** `work_center_blocked`, and pause and finish end the caller's
downtime logs.

`POST /work-centers/{id}/unblock` sets `workingState` back to `normal` and ends every open
downtime log at the center, the operators' and its own; the work orders stay `paused`
until an operator starts again. Unblocking a center that is not blocked answers **200**.

Both answer the work center's queue. The block — its reason, note, `by` (who blocked it,
with their display name at the time) and since — is answered on the queue and the work
order view while it lasts. `GET /stats` counts the
active work centers that are blocked.

### Corrections

A supervisor corrects the record (M29, M29a); both writes need `manufacturing:access`
**and** `manufacturing:orders-manage`.

- **Correct** (`PATCH /time-logs/{id} {startedAt, endedAt, quantity?, note}`) changes a
  log's start, end and quantity (absent keeps it, `null` clears it); the `note`, 1–2,000
  characters, is required, and replaces the log's note as the supervisor's words
  (`note_by`). Only a work order's log is corrected: a work center's own
  downtime log, which has no work order, answers **404** as an id that does not exist. A
  log still open is closed at the given `endedAt`, and its work order is settled again —
  `paused` when no productive log is left open on it.
- **Add** (`POST /work-orders/{id}/time-logs {userId, startedAt, endedAt, kind?, reason?,
  quantity?, note}`) adds a missing closed log for a user: productive, or downtime with
  a `reason`, its note the supervisor's words (`note_by`). It changes no work order's
  state: only start, pause and finish on the floor do.

Either is refused:

- **409** `log_locked` when the log's start, or the new start, is more than **60 days**
  before now — a correction checks both, an addition its start;
- **409** `order_closed` when the order is `done` or `cancelled`;
- **400** `log_overlaps` when it overlaps the user's other logs;
- **400** on the field when the end is not after the start, or lies in the future.

Each correction is an event `time_log_corrected`, and each addition `time_log_added`, on
the order's [timeline](#history), with who and when — the times and the person, not the
note, which stays on the log alone; the log answers `correctedBy` and `correctedAt`.

### The queue and My work

- **The queue** (`GET /work-centers/{id}/queue`) is the work center page (M31): the
  center's `ready`, `in_progress` and `paused` work orders of open orders, ordered by
  `plannedStart`, then the order's priority (`urgent` first), then its due date. Each
  carries the order's number, the product, the remaining quantity, the due date, the
  reservation status and who runs it. While the center is blocked the answer carries its
  `block`. A group work order whose member is not fixed is not among the `items` but in
  `groupWork` ([The member](#the-member)).
- **The work order view** (`GET /work-orders/{id}`) is the work order with its
  instructions, the order's component lines consumed at its operation (M11) with their
  live [availability](#availability), every open log on it, the caller's own open log
  wherever it is (`myOpenLog`) and the center's block.
- **My work** (`GET /shop-floor/mine`) is the caller's open log with its work order, and
  the work orders they logged time on today — the business calendar's day — or still run,
  the latest first.

The work center picker keeps the device's choice in the browser's `localStorage`, as a
convenience only. The operator is the signed-in user: switching operator is switching
account.

### The data model of the shop floor

Migration `00158_manufacturing_work_orders.sql` adds, in the `manufacturing` schema:

| Table | Holds |
| --- | --- |
| `work_orders` | A work order: `order_id`, `operation_id` (the BoM operation it was copied from, which its component lines name), `sequence`, `name`, `work_center_id`, `setup_minutes`, `cleanup_minutes`, `minutes_per_cycle`, `capacity`, `time_efficiency`, `machine_rate`, `labour_rate`, `instructions`, `expected_minutes`, `planned_start`, `planned_finish` (delivery D), `status`, `started_at`, `finished_at`, `revision` |
| `time_logs` | A time log: `work_order_id` (null on the center's own), `work_center_id`, `user_id` and `user_display` (null and empty on the center's own), `kind`, `reason`, `note`, `started_at`, `ended_at`, `quantity`, `forced`, `corrected_by`, `corrected_by_display`, `corrected_at`, `note_by` and `note_by_display` (whose words the note is: the supervisor's for a correction or an addition, the blocker's for a block's logs); a partial unique index on `user_id` where `ended_at` is null |

A work order and a time log name their work center with a restricting foreign key inside
the schema, which is why a work center they name cannot be deleted.

## The production schedule

Delivery D (M34–M37, M35a) places the work orders of open orders on their work centers'
calendars. The board is **Production schedule** (`/manufacturing/schedule`, nb
*Produksjonsplan*).

### The calendar

A work center's calendar (M34) is computed, never stored:

- each day's **open intervals** are its [weekly pattern](#the-weekly-working-pattern) for
  that weekday — at most two intervals on the 15-minute grid, as local wall times in the
  business time zone of `contracts.BusinessCalendar` — converted to UTC instants **for
  that day**. A day on which daylight saving time changes therefore has its real length: a
  pattern of a whole day is 23 hours on the spring day and 25 hours on the autumn day;
- a Norwegian public holiday (`holidays.PublicHoliday`) is closed, whatever the pattern;
- a day inside one of the work center's [closures](#closures) is closed;
- the day's **capacity** is the length of its open intervals.

The work center's `capacity` and `timeEfficiency` are already in each work order's
[expected minutes](#expected-minutes); the calendar counts time, not units.

### Slots

A planned work order holds its time as **slots** (`work_order_slots`): pieces
`[startsAt, endsAt)` each inside one open interval of its work center. A work order cut
around a lunch break, a night, a closed day or other bookings has several. The **free
time** at a work center is its open intervals less the slots of its work orders that are
neither `done` nor `cancelled` — read from the slots, never from a work order's
`plannedStart … plannedFinish`, which spans the gaps between its pieces. A work order's
`plannedStart` and `plannedFinish` are its first slot's start and its last slot's end.

### Planning

`POST /orders/{id}/plan {revision}` (M35, M35a) plans a `confirmed` or `in_progress` order
(`order_not_open`, 409, otherwise):

1. **The search start** is the later of now and the start of the requested start day in
   the business time zone. The requested start is the order's `requestedStart`, or, when
   that is `null`, `requestedStartDefault`:

   ```text
   default = dueDate − ⌈Σ expectedMinutes of the unfinished work orders ÷ 450⌉ business days
                     − 1 business day,
             clamped to today; today when there is no due date
   ```

   450 minutes is a 7.5-hour day, used for this estimate only; a business day skips
   weekends and Norwegian public holidays.
2. **The work orders** that are not `done` or `cancelled` are placed forward in a
   topological order of the [graph](#routing), by sequence among those ready together
   (M115). Each is cut into pieces that fill the free time of its work center in order,
   starting no earlier than the latest of its predecessors' finishes — or their
   [send-ahead offsets](#the-send-ahead-quantity) — and never before the search start. A
   [group work order](#the-member) is tried at each member and placed at the one that
   finishes first. A `ready` or
   `pending` work order needs its `expectedMinutes`. One that is `in_progress` or `paused`
   needs only its **remaining** minutes — expected less the productive minutes logged on
   it, the union of its productive logs so time two operators logged at once counts once
   (as [the machine cost](#the-running-actual) counts it), at least 0 — and starts from
   now. A `done` work order is never planned.
3. **The horizon** is 365 days from the search start. A work order that does not fit
   inside it is **409** `no_capacity`, and nothing is written.
4. The order's earlier plan is **replaced**: its unfinished work orders' slots are deleted
   before the free time is read, and the new slots written.

The order's **window** is the earliest planned start to the latest planned finish of its
work orders, written to its `plannedStart` and `plannedFinish`. An order with no work
orders gets `dueDate −` its BoM's lead time in business days `… dueDate` (M34). Planning
is the only writer of the window: `plannedStart` and `plannedFinish` are no fields of the
create or the update, and naming one is **400** (an unknown property); an update keeps the
window as planning left it. An order is **planned** when one of its work orders that is
not done or cancelled holds a slot; one with no such work order, when it has a window. The order
answers `lateByDays`: the calendar days from the due date to the day of the planned finish
in the business time zone, a finish at midnight belonging to the day it ends; the order
page shows it when it is more than 0. Each plan is an event `planned` on the
[timeline](#history).

### Plan all

`POST /schedule/plan-all {siteId}` plans every open order at the site — `confirmed` or
`in_progress` — that has no plan, judged again under the locks: **urgent** first, then by
due date, those without one last, then by number. Each is planned as above, against every
booking at the site's work centers and the free time the ones before it left; the orders
it plans hold no slots, so none is left out. An order that does not fit within its horizon
is left unplanned, keeps what it had and is named in `noCapacity`; the others are planned.
The answer is `{planned, noCapacity}`.

### Unplan

`POST /orders/{id}/unplan {revision}` clears the planned dates of the order and of its
work orders that are not done or cancelled, and deletes their slots, so their time is free
again. It is an event `unplanned`.

### Reschedule

`POST /work-orders/{id}/reschedule {start, revision}` (M36), under the **work order's**
revision, moves one work order on the board:

- the work order takes the first free time of its work center from `start` — from now
  when `start` is past — cut into pieces as planning does;
- its successors in the [graph](#routing), direct or not, that are planned move — in a
  topological order, each on its own work center — **only** when they would otherwise
  start before their predecessors' finish (or [send-ahead offset](#the-send-ahead-quantity))
  or finish before their last send-ahead batch; those that already fit stay, and one that
  moves goes around them. A successor on the same work center that the moved work order
  now overlaps — a send-ahead successor at its new member — moves too;
- no other order's work order ever moves.

With `workCenterId`, a group work order moves to that member ([The member](#the-member)):
a center that is not an active member of its group, or any other center once the group is
archived, is **400** `work_center_not_in_group`, a blocked member is **409**
`work_center_blocked`, and a work order with a time log is **409** `work_order_started`.

It is refused **409** `revision_conflict` on a stale revision, `order_not_open` when the
order is not open, `work_order_closed` when the work order is done or cancelled, and
`no_capacity` when it does not fit within 365 days; nothing is then changed. The answer is
the order's work orders by sequence — **503** when a module they are read with is
unavailable, the move being made — and the move an event `rescheduled`. `start` is the
**work order's** start: the board sends, for a bar dragged that is a later piece, the work
order's planned start moved as far as the bar was, and **Move to…** starts from the work
order's planned start, reading the day and time typed in the viewer's own time zone.

For planning, plan all and reschedule, now is cut to the second, so a piece that starts
at now is never shorter than the microsecond Postgres keeps.

### The schedule's locks

Free time is read from the slots, so two plans that read it at once could book the same
time. M35a prevents it with an advisory lock of its own: every plan, plan all, unplan and
reschedule takes **`pg_advisory_xact_lock(19783, workCenterID)`** for every work center
it touches, in id order, **before any row lock** — before `LockVariants` too. The centers
touched are those of every work order of the orders — so a drag holds the centers of all
the successors it may move — and every member of the groups their work orders target
(M115, M119). A work order, or a group member, found under the locks on a center not
locked starts the write again (`errRelock`). `19783` is
`0x4D47`, the class after the BoM graph's `19782` (whose key 1 is
[the BoM graph](#locks)). Then it follows the canonical
[order lock order](#the-order-lock-order): `LockVariants`, the orders `FOR UPDATE` in id
order, their work orders in id order. Plan all takes the locks of **every** work center at
the site first, then the orders' rows in id order, and only then plans them one by one in
priority order, in memory. Every writer of a center's slots holds that center's lock, so
two concurrent plans never double-book a work center.

### The board

`GET /schedule?siteId&from&days` (M36) is the board of one site: `from` defaults to today
in the business time zone, and `days` is `7` (the default) or `14` — anything else is
**400** on `days`. It answers, per **active** work center at the site, its blocked state,
its closures and, per day, the open intervals, whether the day is closed (`holiday` or
`closure`), the capacity, the planned minutes and the **load** — planned minutes ÷ the
day's capacity, in per cent, above 100 when overbooked — and the **bars**: each slot with
its work order, the order's number, the product, whether the order is urgent and whether
it is late (its planned finish after its due date), and for a group work order its
`workCenterGroupId` and the `memberIds` it may be dragged to — the group's other active,
unblocked members; empty once a time log fixes its member, or when the group is archived. A `done` work order's slots end at
its `finishedAt`: those after it are left out, and the one it finished in is cut there, in
the bars, the planned minutes and the load alike — the free time already ignores a done
work order's slots. It also answers the site's **unscheduled** orders: open orders with no
plan ([as planning judges it](#planning)), with their due date, requested start and
expected time. A day nothing is open on is drawn over 06:00–22:00 in the business time
zone. The board reads again on the `manufacturing.schedule:<siteId>` event
([Live](#live)); its site choice is kept in the browser's `localStorage` as a convenience.

### The data model of the schedule

Migration `00159_manufacturing_schedule.sql` adds, in the `manufacturing` schema:

| Table or column | Holds |
| --- | --- |
| `work_order_slots` | One piece of a planned work order: `work_order_id` (cascading), `work_center_id` (the work order's, restricting), `starts_at`, `ends_at` after it; indexed by work center and start, and by work order and start |
| `orders.requested_start` | The day planning may start from, a civil date; `null` takes the default |
| `orders.estimate` | The estimate fixed at confirm, `jsonb` `{components, machine, labour, total, incomplete}` with amounts as exact decimal text |
| `orders.actual` | The actual fixed at close, the same shape with `costPerUnit` |

## Cost

An order's cost (M38–M41) is answered as its `cost` object, only to a holder of
`manufacturing:costs-view` ([Cost shaping](#cost-shaping)): `{estimate, actual,
actualFixed, deviation, costPerUnit, downtime}`. Each of `estimate` and `actual` is
`{components, machine, labour, total, incomplete}`.

### The estimate

Fixed at confirm (M39), and at a backorder's confirm, and stored on the order:

```text
components = Σ line.required × line.standardCost        (the snapshot at confirm)
machine    = Σ workOrder.expectedMinutes / 60 × workOrder.machineRate
labour     = Σ workOrder.expectedMinutes / 60 × workOrder.labourRate   (one operator)
total      = components + machine + labour
```

The rates are the work order's snapshot at confirm ([Work orders](#work-orders)). A draft's
estimate is `null`.

### The running actual

While the order is open the actual runs (M40), open logs counted to now:

```text
components = Σ (line.consumed + line.scrapped) × line.standardCost
machine    = Σ per work order: the union of its productive logs, in hours × its machine rate
labour     = Σ every productive log, in hours × its work order's labour rate
```

The machine's time is the **union** of a work order's productive logs: time counts once
however many operators logged it. Labour counts every operator. **Downtime** — the union
of the order's downtime logs — is listed by reason in `downtime` and never charged.

### The actual and the deviation

At [close](#close-and-the-backorder) the actual is fixed (`actualFixed: true`) and stored,
with **`costPerUnit`** = the actual total ÷ the good output, output scrap excluded; `null`
when nothing good was produced. The **deviation** is answered per part: `amount = actual −
estimate`, and `percent` of the estimate, `null` when the estimate is 0.

`incomplete` is true when a component had no standard cost, or a work order no rate,
counted as 0.

**Rounding.** Amounts are computed exactly and rounded half-up to two decimals **once**:
at the snapshot (the estimate at confirm, the actual at close) or at display (the running
actual). The total is the exact sum rounded once, so the rounded parts may differ from the
total by 0.01. Percentages are rounded half-up to one decimal.

### Duration deviation

Every work order answers its `actualMinutes` — the union of its productive logs, open logs
counted to now, in whole minutes rounded down — `deviationMinutes` = `actualMinutes −
expectedMinutes`, and `deviationPercent` of the expected, half-up to one decimal, `null`
when nothing is expected (M41). It is not a cost and needs no cost key.

## Reports

Three reports (M42) under `/reports`, each with a period: `from` and `to`, days in the
business time zone, both included; `to` defaults to today and `from` to 29 days before
`to`. `from` after `to` is **400** on `from`; `to` more than 366 days after `from` is **400**
`period_too_long` on `to`.
An optional `siteId` limits the report to one site. `format` is `json` (the default) or
`csv`: semicolon-separated, UTF-8 with a byte order mark, the decimal comma, each cell
guarded against formulas, served as an attachment named
`manufacturing-<report>-<from>-<to>.csv`.

| Report | Operation | Permission | What |
| --- | --- | --- | --- |
| Production cost | `GET /reports/production-cost` | access **and** costs-view | The done orders closed in the period, grouped by product: per order the estimate, actual, deviation, deviation % and cost per unit as fixed at close, with each product's totals. A total's `produced` is the good output of the orders with an actual, so its cost per unit is the actual total over the output it paid for |
| Work-center utilisation | `GET /reports/utilisation` | access | Per work center and ISO week, counting only the period's days: the calendar's capacity, the planned time (its slots, a done work order's cut at its finish), the productive time (the union of the productive logs at the center), the downtime (their union) and by reason, and utilisation = productive ÷ capacity. An archived work center has a row only for a week with planned, productive or down time |
| Time deviation | `GET /reports/time-deviation` | access | Per BoM operation (its BoM and sequence): the average expected and actual minutes over the last 10 done work orders finished in the period, and the deviation % |

The reports page (`/manufacturing/reports`) shows the production-cost report only to a
holder of `manufacturing:costs-view`.

## Make-to-order and sub-assemblies

Delivery E (M43–M48, with the §15 amendments M43a, M44a, M44b, M46a and M48a) makes
orders for sales orders, and orders for the made components of an order.

### The two contracts

| Contract | Provider → consumer | Class (rule 12) | Calls |
| --- | --- | --- | --- |
| `contracts.SalesOrderLines` (`contracts/production.go`) | Orders → Manufacturing | **Consumer-gated**: Orders authorises nothing; Manufacturing decides before it calls | `Lines(ids)`, `OrderLines(orderID)` |
| `contracts.Production` (`contracts/production.go`) | Manufacturing → Orders | **Provider-gated** on `manufacturing:access` from `ctx`; nothing, not an error, without it or with no request | `ForOrderLines(lineIDs)`, `HasBOM(variantIDs)`, `NotMade(salesOrderID)` |

Both are single-provider slots on `module.Deps`, resolved from the enabled modules only
(nil when the provider is off), and both are read on the pool and never inside a
transaction, through the accessors in `contractscalls.go`. A `SalesOrderLine` carries
the line, its order's id and number, the customer's id, the variant, the **open quantity**
— ordered less cancelled less delivered, exact decimal text — the order's delivery date,
its site and its status; never the customer's name (M43a). `Production.ForOrderLines`
answers every order whose origin is one of the lines, **backorders included** and
cancelled ones left out (M44b), with its number, status, quantity, produced, site,
planned finish and due date. `Production.NotMade` answers why make-to-order made no
order for lines of one sales order: each line its request left out, with the reason's
code and detail — but `no_bom`, `not_make_to_order` and `has_order`, lines that were
never this module's to make or already have an order — and, when the request ended
`failed`, one entry for the whole sales order (line 0, code `failed`). A request still
pending, or cancelled, answers none. Each read names at most
`contracts.MaxProductionLines` (500) ids.

### Automatic make-to-order

The handler **`manufacturing.mto`** receives `orders.sales_order.confirmed` v1 and only
inserts one row of `mto_requests` for the sales order, on the delivery's transaction
(rule 23, M44a): a redelivery, or a second confirm of the same order, finds the row by its
unique sales order and adds nothing. It does nothing while `MODULES` leaves Manufacturing
out.

The worker **`manufacturing.mto-worker`** (every 15 seconds) takes the pending requests
due, oldest first, at most 20 a cycle, and for each:

1. reads the order's lines through `SalesOrderLines.OrderLines` on the pool, holding no
   connection. No lines — the order is gone, or Orders is off — or a cancelled order
   finishes the request **skipped**;
2. judges each line, on the pool: the variant's **default active BoM** must have
   `makeToOrder` (`no_bom`, `not_make_to_order` otherwise); the line must have something
   open (`nothing_open`) and no live order (`has_order`). The order's **site** is the
   site of the BoM's work centers; a BoM with no operations takes the sales order's site,
   and with none on the order either the line is skipped `no_site`. The site and its
   locations are judged as a new order's ([Creating an order](#creating-an-order)); a
   refusal is recorded under its code;
3. opens **one transaction for the sales order**: the make-to-order lease
   `pg_advisory_xact_lock(0x4D46474D544F3031)` ("MFGMTO01", the bigint form, a key space
   apart from the schedule's), then `LockVariants` on every product and component, then
   the request read again — finished meanwhile, nothing is done — then, per line on a
   savepoint, a draft created with origin `sales_order`, the sales order's id, the line's
   id and the reference `SO-<number>`, the line's open quantity and its delivery date as
   `dueDate`, by the system, and **confirmed** by the path a person's confirm takes: the
   snapshot, the work orders, the estimate, and the two-pass reserve through
   `ReserveForProduction`. **Shortage never stops it** (M20a): a short line is left
   unreserved. A line Inventory refuses for another reason is rolled back to its savepoint
   and recorded under the refusal's code, and so is a line a person made an order for by
   hand meanwhile — the new-order form takes no lease, and the
   [one-live-order index](#one-live-order-per-line) refuses the worker's insert
   (`sales_order_line_has_order`);
4. finishes the request **done** — some order made — or **skipped** — none — with every
   line it left out in `reasons` (`[{lineId, code, detail}]`).

A failure — a contract that cannot be read, a lost connection — counts an attempt and
backs off a minute doubled per attempt, at most an hour; the eighth leaves the request
`failed`. The worker holds no connection while it reads a contract and runs every
transaction on one pooled connection, so it runs on a pool of one. A quantity raised on
the sales order after confirm (`orders.sales_order.changed`) is **not** followed in phase
1: the order page's production status shows the gap.

The worker of each replica takes the same lease, so two never make one request's orders:
the second finds the request finished when it reads it again under the lease, and ends
without making or failing anything. With the module disabled its workers do not run, and
in worker mode they are built by `module.Workers`, which resolves the product catalog,
the stock, the stock ledger and the sales order lines from the enabled modules as
`Compose` does.

### One live order per line

The partial unique index `ux_orders_origin_line` is on `origin_line_id` where
`origin_kind = 'sales_order'`, `backorder_of_id IS NULL` and the status is not
`cancelled` (M44b). A backorder carries its original's origin and is left out, so a
line's chain is one root and its backorders; a cancelled order frees the line. The
worker skips a line with a live order; a person's create for one is **409**
`sales_order_line_has_order`.

### A manual order for a line

`GET /sales-order-lines/{id}` (orders-manage) reads one line through
`SalesOrderLines.Lines` — the route has already decided, as the consumer-gated class asks
— and answers its variant, open quantity, delivery date, site, reference, the variant's
default BoM and the orders already made for it: the new-order form's prefill (M45). A
`POST /orders` with `salesOrderLineId` reads the line again: a goods line of a sales
order that is not cancelled, of the order's variant, or **400**
`sales_order_line_unknown`; with Orders off, **503**. The draft then carries origin
`sales_order` with its ids and reference, and is confirmed as any other.

### A cancelled sales order

The handler **`manufacturing.mto-cancelled`** receives `orders.sales_order.cancelled` v1
and, on the delivery's transaction, writing only this schema (M44):

1. takes the make-to-order lease, as the worker's transaction does;
2. with the module on, marks the sales order's request **`cancelled`** while it is still
   pending — or inserts it cancelled when the cancel is handled before the confirm, so the
   later `manufacturing.mto` finds it and adds nothing. The worker reads the request again
   under the same lease and makes nothing for a cancelled one, so a sales order cancelled
   while the worker judges it gets no order; a request already finished is left as it is;
3. sets **`origin_cancelled`** on every order made for the sales order that is not yet
   flagged — locking them in id order first, as every write of several orders does —,
   adding an `origin_cancelled` event to each one's timeline and telling its live topics.

It never cancels the orders: the goods may be half made, and a person decides. The flag
is `origin.cancelled` on the list and the order. A **backorder** carries its original's
flag, made before or after the cancel. **Sub-assembly orders** of a flagged order are
not flagged: their origin is the parent, not the sales order, and the parent's flag is
the place a person sees it.

### What the order shows of its origin

`origin` carries `kind`, `orderId`, `lineId`, `reference` and `cancelled`. On one
order's answer, and only to a caller who also holds **`orders:access`**, it carries the
sales order's `customerName`, read after the order's snapshot: the line through
`SalesOrderLines.Lines` for its customer's id, then `CustomerDirectory.Customer` (M43a).
It is left out otherwise, and never on the list. A read that fails leaves it out too,
logged; the order's answer never fails over it.

### Done goods and the sales order

Closing an order whose origin is `sales_order` publishes **`manufacturing.order.done`**
v1 (`contracts/events_manufacturing.go`) on the closing transaction, while it holds the
order's row (rule 23, M48): `manufacturingOrderId`, `salesOrderId`, `salesOrderLineId`,
`siteId` and `produced` (exact decimal text) — ids and quantities only. It is published
for a close short too, whether or not a backorder is made; an order of another origin
publishes nothing. Orders' handler `orders.mto-reserve` marks the line
([Orders](/en/reference/orders/#make-to-order)).

### Sub-assembly orders

`POST /orders/{id}/children {revision, componentIds}` (orders-manage, M47) on a confirmed
or in-progress order (`order_not_open`) makes one **confirmed** child order per chosen
component line, each:

- by the component variant's **default active BoM**, which each component line answers
  as `defaultBomId` (`bom_required` without one);
- for the line's **missing** quantity (M21a) less what the parent's **open sub-assembly
  orders** of the variant — draft, confirmed or in progress — still make (their quantity
  less their produced), which must be more than 0 (`nothing_missing`). It is judged
  before the transaction and again under the parent's lock, which every sub-assembly
  create takes, so two creates at once never make one shortage twice;
- with origin `parent`, `parentOrderId` the parent, the parent's priority, and the site of
  its BoM's work centers, else the parent's site;
- **due** on the parent's planned start, as a day of the business time zone; when the
  parent is unplanned, its requested start, else the requested start planning would take
  (M37).

Every child is created and confirmed in one transaction under the parent's
[lock order](#the-order-lock-order), with every child's variants locked with the
parent's; each reserves as a confirm does. The parent's timeline gets
`children_created`. The answer is `{order, children}`. It is never automatic. A child's
output is not reserved to the parent: the parent's **Reserve now** picks it up once it
lands. The parent answers its `children`, each child its `parent`; an order link names
its `variantId`, so a screen can match a child to its component line.

### The data model of make-to-order

Migration `00160_manufacturing_mto.sql` adds:

| Table or column | Holds |
| --- | --- |
| `mto_requests` | One per sales order (`sales_order_id` unique): the `event_id` that queued it, `status` (`pending`, `done`, `skipped`, `failed`, `cancelled`), `reasons`, `attempts`, `last_error`, `next_attempt_at`, `created_at`, `done_at` |
| `orders.origin_cancelled` | The sales order it was made for was cancelled |
| `ux_orders_origin_line` | [One live order per line](#one-live-order-per-line) |

The sales order and its line are Orders' ids, opaque here (rule 4).

## Quality

Checks on a BoM's operations, recorded at the station; and deviations, which can hold
lots in quarantine through Inventory's ledger (manufacturing phase 2 M95–M104).

### Check templates

`manufacturing.bom_operation_checks`: an operation's checks, edited in the BoM's one
request as `operations[i].checks` — at most 20 an operation, replaced as a whole on every
save, under `boms-manage`. An operation sent without `checks` keeps none: a BoM saved
without them deletes the checks it had (orders already confirmed keep their snapshot).
Each has a `kind`:

| Kind | Recorded as |
| --- | --- |
| `instruction` | `done` |
| `pass_fail` | `pass` or `fail`, as sent |
| `measure` | a `value`, judged by the server: `pass` within `[min, max]`, `fail` outside |
| `photo` | `done`, with its `photoId` (`photo_required` without one) |

a `title` (1–200), `instructions` (Markdown, ≤ 5,000), a `frequency` —
`each_work_order` (recorded once before the work order finishes) or `first_piece`
(recorded before any output is registered past the work order) — and
`quarantineOnFail`. A measure has `min`, `max` or both, and `nominal` when given lies
between them (`check_limits_invalid`, 400); limits are exact decimals with at most four
places, of either sign, and are dropped from a check of any other kind. A measure is
judged against its limits unrounded.

### The snapshot

Confirm copies each operation's checks onto its work order
(`manufacturing.work_order_checks`, with `template_id`), as it copies the operation. A
later BoM edit never reaches an open order. Every work order answer carries `checks`, by
sequence, each with its `result` in force or `null`; the shop floor sorts the first-piece
checks without a result first (M101).

### Results

`POST /work-orders/{id}/checks/{checkId}/result` with `result`, `value`, `note` (≤ 1,000)
and `photoId`, as the kind asks. A result is refused `check_not_open` (409) on a work
order that is done or cancelled, and `order_not_open` on an order that is not open.
Recording again supersedes the result in force: it is kept, its `superseded_by` naming
its successor; `ux_check_results_current` keeps one result in force per check. A `value`
is kept only on a measure's result; sent with any other kind it is judged as a decimal
and then dropped. A result keeps its recorder's user id and display name (rule 19). It
answers the work order view.

The lock order (M97, §15): `LockVariants` on the order's variants; the order `FOR
UPDATE`; the work order; **the check `FOR UPDATE`** — two results recorded at once for
one check serialise, and the second supersedes the first —; the photo `FOR SHARE`; the
result; the deviation a `fail` opens, or the open one it adds to; the ledger last.

### Photos

`POST /work-orders/{id}/photos` (multipart, field `file`): a JPEG or PNG by its own
bytes (`attachment_type`, 415), at most 10 MB (`attachment_too_large`, 400) — the
router's body cap for this one operation is 10 MiB and 64 KiB for the framing
(`RouterOptions.BodyLimits`), every other operation keeping the platform's 1 MiB, and the
body is not copied for the closed-schema check, which judges only JSON —, its
metadata stripped with `srv/imagemeta`, kept in the object store's scope
`manufacturing-quality` under `checks/<uuid>` before its row
(`manufacturing.check_photos`) is written — a store that cannot write is 503
(`storage_not_configured` when none is configured), and a row that is refused removes
the object again. It answers the `photoId` a result names; refused `check_not_open` on a
work order done or cancelled. `GET /work-orders/{id}/photos/{photoId}` streams it, never
cached or sniffed, under `default-src 'none'; sandbox`; a photo of another work order is
404. Both are `x-vantigo-assistant: never`: a photo is a file the Assistant does not
fetch. The shop floor draws a picture whose longest edge is past 2,560 px down to it and
sends it as a JPEG at quality 0.85 before the upload.

A photo is a quality record: it stays when its result is superseded, and nothing deletes
one a result names. One no result names 24 hours after its upload is deleted, its object
queued on `manufacturing.object_deletions` in the same transaction, by the worker
**`manufacturing.object-deletions`** (every five minutes, built through `module.Workers`):
it sweeps those photos `FOR UPDATE SKIP LOCKED` — a result being recorded with one holds
it `FOR SHARE`, so the sweep skips it — and deletes each with `NOT EXISTS` judged again
by the delete's own statement, queuing the object only when a row went: a result
committed after the sweep read the photo keeps it. Then it gives each due ledger row one
attempt,
claimed by a conditional update with a five-minute window, retried with a backoff from
two seconds to an hour. No statement holds a connection while the store is asked: it
runs on a pool of one.

### What the checks gate

Productions are posted per order, so the gate is stated against its work orders (M98).
The **output work order** is the last that is not cancelled.

- A production with good output (`quantity` more than 0) is refused
  **`checks_incomplete`** (409, `checks` naming each check) while any check, of either
  frequency, of a work order before the output work order has no result, or any
  `first_piece` check of the output work order has none. A scrap-only production is not
  gated. *Before* is by the [graph](#routing) (M114): the work orders from which the
  output work order is reachable — since every branch joins the last operation
  ([The graph](#the-graph)), every other work order that is not cancelled.
- **Finish** of a work order is refused `checks_incomplete` while any of its own checks
  has no result.
- **Register quantity** on a work order is refused `checks_incomplete` while any of its
  own `first_piece` checks has no result (M116).
- **Close** of an order with good output (`produced` more than 0) is refused
  `checks_incomplete`, naming them, while any check of a work order that is not
  cancelled has no result — read before the close cancels the work orders not done. A
  check is never closed over: record it (a `fail` is a result). An order with no good
  output closes without the gate.
- Starting is never gated, and a `fail` does not block: it has opened a deviation.

The gate reads the checks under the order's lock, which every result takes too.

### Deviations

`manufacturing.deviations`, numbered **DEV-<n>** from the counter `deviation`: a
`title`, a `description` (Markdown, ≤ 5,000), what it links to — `order_id`,
`work_order_id`, `check_result_id`, `lot_id` with its number and variant — a `status`
`open` or `closed`, a `disposition` (`use_as_is`, `rework`, `scrap`, `return`) and a
closing note once closed, `quarantine`, `holds_released`, who opened and closed it with
their display names, and a `revision`.

- **A failed check opens one** in the result's transaction, linking the order, the work
  order and the result, its description the measured value against the limits and the
  note; `quarantine` is the check's `quarantineOnFail`. **A fail of the same check while
  that deviation is open opens no other**: the deviation is taken `FOR UPDATE`, pointed
  at the latest failed result (`check_result_id`, indexed by `ix_deviations_check_result`),
  and its description gains the new reading — *Failed again*, when, by whom — within its
  5,000 characters, under a new revision. Once it is closed, the next fail opens a new
  one.
- **A person reports one** with `POST /deviations` (`shop-floor` or `orders-manage`):
  from an order (not a draft), a work order (its order taken), or a lot — read on the pool
  before the transaction through `contracts.Stock.LotRefs`, which answers its variant and
  number — with `quarantine` as ticked. A lot is Inventory's: naming one needs
  `inventory:locations-view` and `inventory:stock-view` as well, else
  **`lots_view_required`** (403). Named with an order, the lot must be one the order's
  productions made (`production_outputs`) or consumed (`production_line_lots`), read
  under the order's lock, else **`lot_not_of_order`** (400 at `lotId`).
- **Quarantine.** A quarantining deviation calls `StockLedger.HoldLots` — kind
  `quarantine`, cause `deviation`, source `manufacturing/deviation/<id>` — on the same
  transaction, after its own rows (rule 24), for every lot the order has made so far
  (`production_outputs`) and for its lot. **While it is open, every lot a production of
  its order makes or adds to is held too**, in the production's transaction: step 7b of
  [the order lock order](#the-order-lock-order), after `EnsureLots` and before `Produce`.
  Each lot held is recorded in `manufacturing.deviation_lots`; `HoldLots` leaves a lot it
  holds already as it is.
- **Close** (`POST /deviations/{id}/close`, `quality-manage`) needs a disposition and a
  closing note (1–2,000) under the revision read; a closed one is `deviation_closed`
  (409). `use_as_is` and `rework` call `ReleaseLotHolds` for the deviation's own source:
  only its holds go, and a lot another deviation, the lot page or a recall holds stays
  held (M64). `scrap` and `return` keep them; **release**
  (`POST /deviations/{id}/release`, `quality-manage`) lets them go later, once the lot is
  written off or returned — refused `deviation_closed` on an open deviation, and
  answering a released one as it is.
- **The lock order** (M99): `LockVariants` on the order's variants, with the lot's
  variant (or the lot's alone, for a deviation of a lot); the order `FOR UPDATE`; the
  deviation `FOR UPDATE`; the ledger last, which takes the lots `FOR NO KEY UPDATE` in id
  order.
- **Reads.** `GET /deviations` lists them newest first, by `status`, `orderId`,
  `variantId` (the deviation's lot's, else its order's product), `lotId` (the one it was
  opened from or one it holds) and the days it was opened in the business time zone,
  with `limit` (≤ 200) and `offset`; `format=csv` gives every match (up to 10,000) as
  `manufacturing-deviations.csv`. `GET /deviations/{id}` answers one, with the lots it
  holds.
- **Live.** The keyless topic **`manufacturing.deviations`**, event
  **`deviation.changed`** with the id, on every write of one; one of an order also tells
  the order's topics. The order's history records `check_recorded`,
  `deviation_opened` and `deviation_closed`.

A deviation's free text is not personal data by intent; its opener's and closer's user
ids stay with their display names (M104).

## Routing

Layer F (M112–M119) makes a BoM's operations a **graph**, lets a successor start before
its predecessor is done with a **send-ahead quantity**, and lets an operation target a
**work center group**. Migration `00172_manufacturing_routing.sql` fills the graph of
every existing BoM and work order from the sequences — each after the one before it — so
phase 1's line keeps its meaning.

### The graph

`bom_operation_dependencies (operation_id, depends_on_id)` holds the edges (M112). In the
BoM request each operation's `dependsOn` names the **keys** of operations of the same
request:

- absent, the operation depends on the one before it by `sequence` — the first on none;
- `[]` means it can start at once;
- a key no operation of the request has is **400** `operation_unknown` on
  `operations[i].dependsOn`;
- an operation depends only on operations **earlier by `sequence`**: a key of one with the
  same or a later sequence — itself included — is **400** `operation_order` on
  `operations[i].dependsOn`. The sequence is then an order the graph keeps, and no cycle
  can be;
- every operation but the last by `sequence` must have one that depends on it, so that
  every branch joins and leads to the last operation, the one that makes the output: an
  operation nothing depends on is **400** `operation_unjoined` on its own
  `operations[i].dependsOn`.

The answer gives each operation's `dependsOn` as keys (the ids as text). An edit replaces
the graph with the request's. The confirm copies the edges onto
`work_order_dependencies (work_order_id, depends_on_id)` between the order's work orders
(M113); each work order answers `dependsOn`, the ids of the work orders it runs after.

### Readying

A work order is `ready` when every predecessor is `done` or `cancelled`, or — for one that
is not — has reached its send-ahead quantity (M114). At confirm the work orders with no
predecessor are `ready`. After every finish and every Register quantity,
`ReadyWorkOrders` readies every `pending` work order of the order whose predecessors now
allow it, in the same transaction; two branches of a fork become ready together, and a
join only when all its predecessors allow it. `force` (M27) still starts a pending one.

Readying looks only at a work order's **direct** predecessors. After a forced start
the order can run ahead of phase 1's line: in A → B → C with B started by `force` and
finished while A is still open, C becomes `ready` — B is done — though A is not. Phase 1
waited for every work order before C. Starting out of turn is the supervisor's call, and
its log is marked `forced`.

### The send-ahead quantity

`bom_operations.send_ahead_quantity` (exact decimal, more than 0, nullable) is copied onto
the work order (M116). A work order also keeps `quantityDone`, the good units passed on.

- **Register quantity**, `POST /work-orders/{id}/quantity {quantity, clientRequestId}`,
  needs `manufacturing:shop-floor` or `orders-manage` (`shop_floor_required`, 403). It adds
  `quantity` (more than 0, at most three decimals; `quantity_invalid`) to `quantityDone`,
  posts nothing to stock, and readies the successors. It is refused `work_order_closed`
  on a done or cancelled work order, `work_order_pending` on a pending one,
  `order_not_open`, **409** `work_order_is_output` on the output work order — the last
  that is not cancelled, whose good units are a production — **400**
  `quantity_over_order` on `quantity` when the work order's `quantityDone` would exceed the
  order's quantity, and `checks_incomplete` while a first-piece check of the work order
  has no result. Each registration is kept in `work_order_quantities` with who made it; sent
  again with its `clientRequestId` for the same work order and quantity it registers
  nothing more and answers 200, and with anything else is **409**
  `client_request_reused`.
- A successor of a work order with a send-ahead quantity becomes `ready` once that work
  order's `quantityDone` reaches it.
- **In the plan**, the successor may start once its predecessor has worked the send-ahead
  quantity from its planned start — its setup and the cycles of that quantity, less the
  time already logged — and may not finish before the predecessor's planned finish plus
  the successor's own time for the last batch (the cycles of the send-ahead quantity at
  its center, without setup).
- A send-ahead quantity at or above the order's quantity behaves as none.

### Work center groups

A group (M117; `work_center_groups`, `work_center_group_members`) has a `code` of 1–16
characters A–Z and 0–9, unique among the groups (`work_center_code_taken`, 409), a name, a
site and 1–50 members, every one a work center of the group's site
(`work_center_other_site`, 400) and, when added, active (`work_center_unknown`, 400).
`GET /work-center-groups` lists them (active by default), and
`POST /work-center-groups`, `PUT /work-center-groups/{id}` under the revision,
`POST …/archive` and `POST …/reactivate` write them; the writes need
`manufacturing:boms-manage`. A group's site does not change while an active BoM targets it,
nor does a member's while it is in a group (`work_center_other_site`); archiving is
refused while an active BoM targets the group (`work_center_in_use`, 409), and
reactivating while its site is archived (`site_archived`, 409). A group write takes the
BoM graph's lock, then the group's row, then its members `FOR SHARE` in id order. A member
is never deleted (`work_center_in_use`).

An operation targets a center or a group (M118): `bom_operations.work_center_id` is
nullable beside `work_center_group_id`, exactly one of them set. The BoM answers a group
operation with `workCenterGroupId`, `workCenterId` null and the group's code and name.

### The member

A group work order keeps its group in `work_center_group_id` and its member in
`work_center_id` (M118, M119):

- **At confirm** the member is the group's first active member by code, and the figures
  are snapshotted from it. A group with no active member is **409**
  `work_center_group_empty`, the detail naming the operation and the group; the order
  stays a draft.
- **Planning** tries each member that is active and not blocked, with that member's
  figures, and places the work order at the one with the **earliest finish**; ties go to
  the higher time efficiency, then the lower code. Each member's schedule lock is taken
  before any row lock.
- **The board** lets a group work order be dragged to another member's row
  ([Reschedule](#reschedule)).
- **On the floor**, a `ready` group work order that is unplanned and has no time log is in
  every active member's queue under `groupWork`; `POST /work-orders/{id}/start {workCenterId}`
  at a member moves it there. A center outside the group is **400**
  `work_center_not_in_group`.
- **Fixed.** Once a time log exists the member is fixed: the work order is in that
  member's queue only, planning keeps it there, a drag to another member is **409**
  `work_order_started`, and a start at another member is **409**
  `work_order_member_fixed`, the detail naming the member. A start at another member of a
  planned work order is refused the same way: move it on the board instead.
- **Archived group.** The work orders of an archived group keep their member: planning
  keeps them there, they are in that member's own queue rather than under `groupWork`, and
  a drag or a start at another center is `work_center_not_in_group`.
- **Moving** sets the member and takes its setup (unless the operation set its own),
  cleanup, capacity, efficiency and both rates again, recomputing the expected minutes
  ([Expected minutes](#expected-minutes)); the cost
  ([the running actual](#the-running-actual)) uses the member's rates. The estimate fixed at
  confirm keeps the first member's.

A group work order's changes are heard on the work center topic of every member
(`manufacturing.work-center:<id>`), since its Group work changes with it.

### The data model of routing

Migration `00172_manufacturing_routing.sql` adds, in the `manufacturing` schema:

| Table or column | Holds |
| --- | --- |
| `bom_operation_dependencies` | The operations' graph: `operation_id` after `depends_on_id`, both cascading, never the same |
| `work_order_dependencies` | The work orders' graph, copied at confirm (filled from the sequences for every existing order) |
| `bom_operations.work_center_group_id`, `work_center_id` nullable | The group an operation targets; exactly one of the two set |
| `bom_operations.send_ahead_quantity`, `work_orders.send_ahead_quantity` | The send-ahead quantity, more than 0 or `null` |
| `work_orders.quantity_done` | The good units passed on by Register quantity |
| `work_orders.work_center_group_id`, `bom_output_quantity`, `own_setup` | The group, the BoM's output quantity at confirm and whether the setup is the operation's own — what a member change computes the figures again from |
| `work_order_quantities` | Each registration: the work order, the `client_request_id` (unique), the quantity, who and when |
| `work_center_groups`, `work_center_group_members` | The groups — code unique, site, status, revision — and their members (a member's work center restricting) |

## Replenishment

Phase 2's R (M105–M111) suggests what to make or buy before a placed variant runs short.
The rule is Inventory's placement: its reorder point, maximum and multiple per (variant,
site) (M105, X).

### Projected stock

For each variant with a reorder point at an active site (M107):

```
projected = usable onHand − reserved + incoming        (contracts.Stock.Placements)
          + Σ supply orders' (quantity − produced), ≥ 0
          − Σ open orders' (required − consumed − scrapped − reserved), ≥ 0, per component line
          − Σ reorder drafts' BoM lines, scaled to the draft's quantity with the scrap
          − Σ unreserved open sales demand              (contracts.SalesOrderLines.Unreserved)
              less, per line, what live MTO orders still make for it, ≥ 0
```

- **Supply orders** are the site's `confirmed` and `in_progress` orders of the variant,
  and its **drafts of origin `reorder`** — a suggestion made into a draft is not suggested
  again the next night. Other drafts count for nothing.
- **Make-to-order output is left out** (origin `sales_order`): it is promised to its
  line. So is the part of that line's demand it will cover: for every sales order line a
  live (draft, confirmed, in progress) make-to-order order names as `origin_line_id`, the
  line's unreserved quantity counts less what those orders still make (`quantity −
  produced`, ≥ 0, summed), at least 0. What they have produced already is in on hand,
  promised to the line, so it stays the line's demand and is not counted twice; a line
  larger than its order counts the rest. Sub-assembly orders (origin `parent`) count;
  their parent's component demand is subtracted.
- **Component demand** is that of the site's open orders, make-to-order ones too: they
  still consume their components. Reserved stock is in Inventory's `reserved` already, so
  only what a line does not hold reserved — Manufacturing's own copy, `reserved` on the
  line — is subtracted. A **reorder draft**, counted as supply, counts its demand too:
  it has no component lines before its confirm, so its BoM's lines are scaled to its
  quantity with their scrap and rounded up to three decimals, as the confirm will copy
  them.
- **Sales demand** is Orders' to say: per open goods line of a confirmed sales order at the
  site, its open quantity less what it holds reserved there, more than 0 — a line
  confirmed without reserving, or reserved short ([Orders](/en/reference/orders/#unreserved-demand)).
  With Orders off the slot is nil, the term is 0, and the run's `salesCounted` is false.

Figures are exact decimals; nothing is rounded until the quantity.

### The suggestion

By Inventory's low-stock rule (M108): **low** when `projected ≤ reorderPoint`; the
target is the maximum, or `reorderPoint + 1`; `needed = target − projected`, at least 1
— as Inventory's `judgeLow`, whatever the unit, so a variant counted in decimals is
suggested its true quantity only above 1 — rounded **up** to a whole number of the
multiple when one is set, then up to three decimals. **Make** when the variant has a default active BoM (`bom_id` names it), else
**buy**. **Need-by** is today (the business zone's) when usable on hand less reserved is
already at or below the reorder point, else today plus the BoM's `leadTimeDays` business
days (weekends and Norwegian public holidays skipped); a buy is needed today. The
**reason** keeps every term (`projected`, `reorderPoint`, `maximum`, `multiple`, `onHand`,
`reserved`, `incoming`, `inProduction`, `neededByProduction`, `neededBySales`); sales
demand is only ever a sum, never a customer or an order, so it is shown to every holder of
`manufacturing:access` (rule 12, consumer-gated). A variant the catalog no longer knows is
skipped.

### The job

The lease worker **`manufacturing.replenishment`** (every 30 seconds, built through
`module.Workers`, M109):

- **Nightly**: once 02:00 has passed in the business zone it queues the day's run, once
  per day however many workers see it (`ux_replenishment_runs_nightly`).
- **On demand**: `POST /replenishment/runs {siteId?}` (`orders-manage`) answers **202**
  with the run. At most one a minute per site: a person's run of the same site — or of
  every site, with no `siteId` — asked for less than a minute ago is answered instead of
  a new one (`site_id IS NOT DISTINCT FROM`), under a transaction-scoped advisory lock.
- **The lease.** A run is claimed by one statement that moves `status` to `running`,
  `lease_until` ten minutes on and `attempts` up (`FOR UPDATE SKIP LOCKED`); a running
  run whose lease has run out — its worker died, or is stuck — is claimed again, and one
  claimed more than three times fails. The claim is its `attempts`: before each site the
  worker renews the lease `WHERE attempts = @attempts`, and a run claimed again since
  answers no row, so the worker stops before that site; the end is written the same way,
  so only the claim holding the run finishes it. No session lock and no connection is
  held between steps.
- **First**, the open suggestions of every site no longer active are `superseded`.
- **Per active site** (or the one asked for): `Stock.Placements`, then
  `SalesOrderLines.Unreserved` for the placements' variants, then the module's own orders
  and default BoMs, every one on the pool and none inside a transaction; then **one
  transaction** takes the site's advisory lock, marks its open suggestions `superseded`
  and inserts the new ones. A suggestion acted on — `done` or `dismissed` — is kept. So a
  pool of one finishes a run.
- **Automatic creation** follows, one transaction per order (below).
- **A site that fails** — a contract or a statement refusing — is logged and the run
  goes on to the next; the run is then `failed`, its `error` naming each failed site
  (`site MAIN: …; site BRANCH: …`), its tallies the other sites'.
- A run records `suggestions`, `orders_created`, `sales_counted` and, failed, its
  `error`; runs finished more than 90 days ago are pruned with their suggestions that are
  no longer open. `GET /replenishment/runs` lists the newest 20.

### Suggestions and their actions

`manufacturing.suggestions` (M110): `run_id`, `site_id`, `variant_id` with the SKU and
name snapshotted, `kind`, `quantity`, `need_by`, `reason`, `bom_id`, `status` (`open`,
`done`, `dismissed`, `superseded`), `reorder_open`, `order_id`, who acted with the display
name, and when. One open suggestion per (site, variant) (`ux_suggestions_open`).

- `GET /replenishment/suggestions?siteId=` — the open ones per active site, make before
  buy, then by SKU, with the last run and `salesCounted`.
- `POST /replenishment/suggestions/orders {items: [{suggestionId, quantity?}], confirm}`
  (`orders-manage`, 1–200 items, none twice): per item, in **its own transaction**, an
  order of origin **`reorder`** at the suggestion's site for its quantity or the one sent,
  due on its need-by date, from the variant's default active BoM. Judged on the pool first
  (the BoM — `bom_required` for a buy suggestion — and the site through `judgePlace`), then
  under the lock order of a new order: `LockVariants`, the suggestion `FOR UPDATE` (no
  longer open is `suggestion_not_open`), **no order of origin `reorder` of the variant
  still `draft`, `confirmed` or `in_progress` at the site** — else the suggestion is
  marked `reorder_open`, that committed, and the item fails `reorder_open`; read under
  `LockVariants`, two creations of one variant see each other's order — the BoM
  re-read, the counter; with `confirm` the
  order is confirmed as `Confirm` does, M20a's two passes, where shortage never refuses.
  The suggestion becomes `done` with the order. The answer lists `made` and `failed`, each
  failure with its code; one failure never stops the others. A quantity that is not more
  than 0 with at most three decimals is **400** `quantity_invalid` at
  `items[i].quantity`, nothing made.
- `POST /replenishment/suggestions/{id}/dismiss` (`orders-manage`) — `dismissed` until
  the next run; one not open is **409** `suggestion_not_open`.
- `GET /stats` answers `suggestionsToMake`, the open make suggestions of the active
  sites (0 with Inventory off).

### Automatic creation

`manufacturing.replenishment_rules (variant_id, site_id, auto_create, auto_confirm)`, set
with `PUT /replenishment/rules/{variantId}/{siteId}` (`orders-manage`; both off removes
the row; `autoConfirm` needs `autoCreate`) and read with
`GET /replenishment/rules?variantId=` — every active site, off when it has no row. Off by
default. When on, the run makes the order of each new make suggestion of the variant
itself, as the system, through the same path as the action above, and confirms it with
`auto_confirm`. **It skips a variant with an order of origin `reorder` still `draft`,
`confirmed` or `in_progress` at the site**, and marks the suggestion `reorder_open`
(*An MO from an earlier suggestion is open*), so a night whose order has not started yet
adds no second one. The skip is read on the pool; one made since — by a person, or a run
at once — is caught under `LockVariants` by the create above (`reorder_open`). A refusal
is logged and leaves the suggestion open.

### Live

The topic **`manufacturing.suggestions:<siteId>`** (`manufacturing:access`), event
**`suggestions.changed`** with the site id, on a run's replacement and on every action;
the suggestions page refetches.

### The data model of replenishment

Migration `00171_manufacturing_replenishment`: `replenishment_runs` (`kind` `nightly` or
`manual`, `nightly_for` unique, `status`, `lease_until`, `attempts`, the tallies),
`suggestions`, `replenishment_rules`, and `ck_orders_origin_kind` gaining `reorder`.

## BoM depth

Layer G (M120–M128), migration `00173_manufacturing_bom_depth.sql`: a BoM's
**by-products** with their share of the cost, **unbuilds** that take finished goods
apart, and the **split** and **merge** of orders.

### By-products

`bom_outputs (bom_id, variant_id, quantity, cost_share, sort_order)` holds a BoM's
by-products (M120), written by the BoM request's `outputs` and answered as the BoM's
`outputs`; an update that leaves `outputs` out removes them. Each is judged with the
lines, after `LockVariants` locked the by-products' variants with the product and the
components:

- at most 20 (a field error on `outputs`), each variant once;
- `quantity` more than 0 with at most three decimals, per the BoM's output quantity;
  `costShare` 0–100 with at most two decimals, default 0;
- goods only (`variant_not_found`, `variant_not_goods` on `outputs[i].variantId`);
- never the BoM's own variant — **400** `output_is_product` — nor one of its components —
  **400** `output_is_component`;
- the shares together at most 100 — **400** `cost_share_over` on `outputs`; the product
  carries the rest.

**The cycle walk covers outputs.** A by-product stops the walk as the product does: a
component that is, or is made through its default BoMs from, one of the BoM's
by-products is `bom_cycle`. The graph also gains an edge from each by-product of a
default active BoM to that BoM's components, so a BoM consuming another's by-product
walks into the BoM that makes it.

Confirm copies the by-products onto `order_outputs` (M121) with the SKU, name and unit,
the share, and `expected` = quantity × the order's quantity ÷ the BoM's output quantity,
half-up to three decimals; a draft answers them as its BoM makes them now, without ids.
The order answers `outputs` with `expected`, `produced`, `costShare` and the variant's
tracking. The variants an order write locks include its by-products' (a draft's BoM's).

**A production** sends `outputs: [{outputId, quantity, lot?}]`; one left out makes
nothing, `quantity` 0 likewise. An `outputId` that is not one of the order's is a field
error on `outputs[i].outputId`. Each by-product made is another **produce line** of the
one `Produce`: its variant, its `order_outputs` id as the line, the order's **output
location** — never the production's `toLocationId` — and, for a lot-tracked one, the lot
named by `lot.number` or the order's pattern rendered for the by-product's SKU, found or
made by `EnsureLots` at step 7a with the counted-number skips the product's lot has. A
by-product tracked by serial number is **400** `serial_quantity`; `lot` on an untracked
one `lot_not_allowed`; a bad `lot.number` or `lot.supplierLot` is a field error at
`outputs[i].lot.number` or `outputs[i].lot.supplierLot`. Every output's lot — the
product's and the by-products' — is found or made before any is held; then the order's
quarantining deviations hold the by-products' lots with the product's (M99). Inventory's
refusal of a by-product's produce line answers a line with its `outputId` and its index
in the request's `outputs`, never a component line whose id it shares. `order_outputs` ids are their own table's, so one may
equal the order's id or a component line's: produce lines are keyed by (variant, line
id). `production_byproducts` keeps what each production made of each by-product, with
its lot; the production answers `byProducts`, the order's lots `byProducts` (those with a
lot), and the production draft `byProducts`, each proposed as its expected × the
quantity ÷ the order's quantity, with a lot-tracked one's previewed number.

**The cost split** (M122). At close the actual total is split: each by-product carries
`costShare` % of it, rounded half-up to two decimals once, and its cost per unit is that
part ÷ its produced quantity; the product carries the rounded total less the parts the
produced by-products carry, so the parts shown sum to the total shown, and the order's
`costPerUnit` is the product's part ÷ the good output.
A by-product that produced nothing has no cost per unit — `notProduced` — and its share
stays with the product; its `actual` is the part it would have had. The estimate is split
by the same shares. The order's cost answers `byProducts` (`estimate`, `actual`,
`costPerUnit` once fixed, `notProduced`) and `mainActual`; the split is fixed with the
actual at close. The production-cost report answers `byProducts`, one row per by-product
of each order, and a product's cost per unit over its orders is their main parts ÷ their
good output. Its CSV adds the by-products as a section of their own after an empty row,
with its own header: `byProductOrder;sku;product;costShare;produced;actual;costPerUnit;notProduced`.

### Unbuilds

An **unbuild** (`unbuilds`, numbered **UB-<n>** from the counter row `unbuild`; nb
*demontering*) takes finished goods apart (M123). `POST /unbuilds` names a done order
(`orderId`) or a BoM (`bomId`) — exactly one — the `quantity`, a `clientRequestId` and
optionally a `note`, the locations and, free-standing, a `siteId`:

- **From a done order** (otherwise 409 `order_not_open`): its BoM, product and site. An
  order whose productions made any of its by-products is refused, **409**
  `unbuild_has_byproducts`: what its consumption made was not the product alone, so no
  share of it is the product's to restore; such goods are taken apart free-standing, by
  the BoM. For a tracked product `lotId` is required (`lot_required`) and must be a lot
  the order's productions made (`lot_not_of_order`); the quantity is at most what that
  lot received from them less what was unbuilt from it, or for an untracked product the
  order's good output less what was unbuilt (`unbuild_too_much`, 400). It restores only
  what the **productions that put output in the chosen lot** consumed (scrap left out),
  each production in proportion to what it put in the lot: per production, its
  consumption per component and lot × the quantity × what it put in the lot ÷ (what they
  put there together × what its consumption made — its good output **and its output
  scrap**). Each component's figure is rounded half-up to three decimals, each lot's part
  too but the last, which takes the remainder. A component goes back into the lot it was
  taken from; an untracked one without a lot. A serial component is restored whole: an
  unbuild that would restore a fraction of a serial is **400** `serial_quantity` at
  `quantity`, naming the component and the serial — unbuild whole units. An untracked
  product's unbuild restores from every production of the order.
- **Free-standing, by a BoM**: the BoM's lines without scrap for the quantity, half-up to
  three decimals, one line per variant. A tracked component needs `components[]` naming
  its lot — an existing `lotId` of the variant, or a new `lot` with its `number` —
  else `lot_required`; an untracked one names none (`lot_not_allowed`); an entry for a
  variant the BoM does not have is a field error at `components[i].variantId`. A tracked
  product needs `lotId` — any lot of the product, an order's included: a free-standing
  unbuild is not held to that order's limit, nor counted in its `unbuilt`. The site is
  `siteId`, else the BoM's work centers', else the first active one.

The finished goods are taken from `fromLocationId` (default the order's output location,
else the site's root) and the components put on `toLocationId` (default the order's
component location, else the root), active locations of the site (`location_unknown`).

The lock order: `LockVariants` on the product and every component; the order `FOR
UPDATE` — so two unbuilds of one order judge what is left one after the other — or, free
standing, the BoM `FOR SHARE` with its lines read again; the counter; the unbuild's row;
`EnsureLots` for the new lots; then **one `Produce` with `Unbuild: true`** under the
document `manufacturing/unbuild/<id>`: the finished item a consume line, the components
produce lines. Inventory posts it as a movement of kind **`unbuild`**, whose consume line
may take a held or expired lot; when the lot taken holds an active hold, Inventory
quarantines every lot restored to, in the same `Produce` (see
[Inventory](/en/reference/inventory/#production)). The trace treats the movement as a
reverse edge, never expanded, and the block does not cross it (M88). Inventory's refusals
pass through as 409. A deadlock is retried once, as a production's is; a second is 409
`revision_conflict`. A `clientRequestId` sent again with the same body answers the first
unbuild, 200; with another, 409 `client_request_reused`. The unbuild snapshots the
product's SKU, name and unit, answered when the catalog no longer has it.

**The quarantine and its release.** Inventory answers the lots it quarantined; the
unbuild records it (`quarantined`). The holds are the unbuild's own, on the whole of each
lot restored to — restored stock mixes with what the lot held before, and the hold is a
lot's. `POST /unbuilds/{id}/release` (manufacturing:quality-manage, as a deviation's
holds) releases them once the lots are inspected: `LockVariants` on the components
restored into lots, the unbuild `FOR UPDATE`, then `ReleaseLotHolds` of the document
`manufacturing/unbuild/<id>` — a hold of any other source stays. It records
`releasedAt` and `releasedByDisplay`; a release done already answers the unbuild as it
is, and an unbuild that quarantined nothing is 409 `unbuild_not_quarantined`.

The order's cost is not recomputed. The order answers `unbuilt`, and its lots each
output lot's `unbuilt` and the product's `tracking` as the catalog has it now, for the
unbuild dialog; its timeline gains `unbuilt`. `GET /unbuilds` pages them newest first, of
one order with `orderId`; `GET /unbuilds/{id}` answers one with its `lines`.

### Split and merge

**Split** (`POST /orders/{id}/split`, M124) takes `quantities`, 2–20, each more than 0,
summing to the order's quantity (`split_sum`, 400), under the order's `revision`. The
order must be a draft or confirmed with no production, no time log and no registered
quantity (Register quantity, M116) — in progress, or any of them, is 409
`order_has_work`; done or cancelled `order_not_open`. The order keeps the
first quantity; each other is a new order numbered as a backorder of its root (MO-12-2,
MO-12-3) with `split_from_id` set, the same BoM, origin, due date, requested start,
locations, priority, responsible, note and lot pattern. The original is unplanned — its
open work orders' slots removed, its window cleared — and every part starts unplanned. A
confirmed original releases its reservations; its component lines and by-products are
split by the quantities (each part half-up to three decimals, the last the remainder),
its work orders' expected minutes recomputed for its quantity (M126), each part gets
copies of the lines, by-products, work orders with their graph and checks — their
expected minutes for the part's — its estimate, and is confirmed; then the original and
every part reserve again, two-pass. A draft's parts are drafts, and each part's work
orders notify their own groups' work centers. The answer is the order, its `parts` and
the lines left short. The original is not cancelled while a part split from it is open
(**409** `order_has_open_parts`): the parts are numbered from it and carry its promise.

**Merge** (`POST /orders/merge`, M125) takes 2–20 `orders`, each `{id, revision}`. Each
must be a draft or confirmed with no production, no time log and no registered quantity
(`order_has_work`, `order_not_open`); all of one variant, BoM and site, all drafts or all confirmed, and of
the **same origin** — all `manual`, all `reorder`, all for the same sales order line, or
all children of the same parent — else **400** `merge_mismatch`: mixing a make-to-order
order with a manual one is refused, so neither promise is lost. The one with the lowest
number (then suffix) survives with the summed quantity and the earliest due date; the
others end their work orders and are `cancelled` with `merged_into_id`. Every order is
unplanned. Confirmed ones release their reservations; the survivor gains each merged
order's lines — the n-th BoM line of a variant and operation, by sort order, added to
its own n-th of the same, an added line or one it has not as a line of its own — and
by-products, its work orders' expected
minutes are recomputed and its estimate fixed again, and it reserves again. The answer is
the survivor, the `merged` and the lines left short. The order list answers each order's
`revision`, which a merge chosen from the list sends: an order changed since the list was
read is `revision_conflict`.

**The locks** (M127). Both run through `withScheduledOrders`: the schedule's advisory
lock of every work center of every order involved — the members of a group work order's
group too — in id order before any row lock, because unplanning removes their slots; then
`LockVariants`, the orders `FOR UPDATE` in id order, their work orders in id order, and
last the ledger — release, then reserve. A work order at a center not locked starts the
write again.

**Make-to-order** (M124). `ux_orders_origin_line` leaves out split parts as it leaves out
backorders: `WHERE origin_kind = 'sales_order' AND backorder_of_id IS NULL AND
split_from_id IS NULL AND status <> 'cancelled'`. A sales order line keeps one live
order; its parts are that order's chain, and `ForOrderLines` returns them with it.

The order answers `splitFromId` and `splitFrom`, `mergedIntoId` and `mergedInto`, and the
`splitParts` and `mergedFrom` made from it. The timeline gains `split` and `split_from`,
`merged` and `merged_into` (M128), each naming the other orders.

### The data model of BoM depth

Migration `00173_manufacturing_bom_depth`: `bom_outputs`, `order_outputs`,
`production_byproducts`, `unbuilds` (with the product's snapshot, the chosen `lot_id` and
its number, the `client_request_id` unique, `quarantined` and the release's
`released_at`, `released_by_user_id` and `released_by_display`), `unbuild_lines`, `orders.split_from_id` and
`orders.merged_into_id`, and the amended `ux_orders_origin_line`. The `unbuild` counter
row is made by its first use.

## Shared stations

A **station** is a shared tablet Identity pairs and signs operators in at (manufacturing
phase 2 M129–M143; the credential side is
[Shop-floor stations](/en/admin/authentication/#shop-floor-stations)). Identity owns the
station; this module knows only its id, from the principal's `Via` (`Kind` `station`,
`StationID`).

### The operations open to stations

An operation is reachable from a station only when its contract carries
`x-vantigo-station: allowed` (M135); the router refuses every other one with `403
station_scope` before the handler, and the operator's permissions are intersected with
`manufacturing:access` and `manufacturing:shop-floor`, so `HasPermission` inside a handler
answers no other key (no `costs-view`: no cost reaches a station). The marked operations:

- reads: `listManufacturingWorkCenters`, `getManufacturingWorkCenterQueue`,
  `getManufacturingWorkOrder`, `getManufacturingMyWork`, `getManufacturingProductionDraft`,
  `getManufacturingMeta`;
- actions: `startManufacturingWorkOrder`, `pauseManufacturingWorkOrder`,
  `finishManufacturingWorkOrder`, `registerManufacturingWorkOrderQuantity`,
  `postManufacturingProduction`, `reportManufacturingWorkOrderScrap`,
  `blockManufacturingWorkCenter`, `unblockManufacturingWorkCenter`;
- quality: `recordManufacturingCheckResult`, `uploadManufacturingCheckPhoto`,
  `createManufacturingDeviation`.

The station page's own list of what it may request,
`apps/manufacturing/frontend/src/station-operations.json`, is held to this set (and to
Identity's station endpoints) by a server test, and the page's test fails on any request
outside it. The production draft carries each tracked component's site lots, so the lot
picker never calls Inventory; the page shows no product image and no other page's link.

### The station's work centers

`manufacturing.station_work_centers (station_id, work_center_id)` holds the work centers a
station serves, read and replaced with `GET`/`PUT /stations/{stationId}/work-centers`
(`boms-manage` to replace; an unknown work center is `work_center_unknown`; at most 100). A
station that serves none reaches none. The replace asks Identity whether the station exists
(`contracts.StationAccess.StationKnown`) and answers `404` for one it does not know, so no
centers are kept for a station that was never registered. A replace that changes the set
writes a row to `manufacturing.station_work_center_changes`: the station, the centers before
and after, and who made it (user id and name, kept after an erasure as an order's events
are); one that changes nothing writes nothing.

The module's strict middleware judges every station request **before its handler**
(M131), by what the request names, and answers `403 station_scope` (a problem) when it is
outside the station's centers:

| Request | Judged by |
| --- | --- |
| The queue, a block, an unblock | The work center named |
| A work order read | Its work center, or for a group's work order not yet started (pending or ready) a member of its group |
| A start | The member named (and the work order reachable), else the work order's work center |
| Pause, finish, register quantity, scrap, a check result, a photo | The work order's work center |
| The production draft, a production | The order's last work order that is not cancelled |
| A deviation | The work order it names, which a station must name |
| The work centers, My work, the meta | Nothing: the lists are cut down to the station's centers |

An id that names nothing is `station_scope` too, so a station learns nothing of what lies
outside its centers; any request type the middleware does not know is refused.

### What is recorded

`time_logs`, `productions`, `check_results` and `deviations` carry `station_id`: the station
a start, a block's downtime log, a production, a result or a deviation was made at, `null`
from a person's own browser. On a block, each operator's productive log at the center ends
and their downtime log carries the station their productive log ran at, wherever the
block was made. The operator is the principal's user, as anywhere; one open log
per user holds across stations and the user's own browser.

### Live

The topics `manufacturing.work-center` and `manufacturing.work-order` are marked for
stations: a station's socket (the live hub's station path, opened with the station cookie
alone) may subscribe to a work center it serves and a work order at one of them, decided
from `station_work_centers` instead of a person's permission; every other topic refuses a
station. The minute's re-check authorises the subscriptions again against the current work
centers.

### The data model of stations

Migration `00175_manufacturing_stations`: `station_work_centers` (no foreign key to Identity's
`stations`, one schema per module), `station_work_center_changes` (the record of each
change) and `station_id` on `time_logs`, `productions`, `check_results` and `deviations`.

## Kits

Layer K (manufacturing phase 2 M144–M146): a **kit** is goods sold together under one
variant and picked as its components. It is a BoM whose `kind` is `kit`
(`boms.kind`, migration `00176_manufacturing_kits`, `normal` by default; a CHECK keeps a
kit from `make_to_order`). The kind is the create's (`kind` on `ManufacturingBomInput`,
default `normal`); an update has no `kind` and keeps the stored one, so a BoM's kind never
changes. `ManufacturingBom` and `ManufacturingBomSummary` answer it.

### What a kit is not

A kit is never made (M144). Its write is judged by `judgeKit` beside the BoM's shape, each
failure **400** `kit_not_manufactured` on its field, in the one 400 with the others:

- `operations` — a kit has none;
- `outputs` — it has no by-products;
- `makeToOrder` — it is never made to order, so never the default MTO reads.

And a line whose quantity **per kit** — its quantity over the output quantity — is 0 at
three decimals, half-up, is **400** `quantity_invalid` on the line: KitStructure would leave
it out, and a kit of nothing but such lines would explode to nothing. A line's quantity and
the output quantity change only through a save, which judges them, so a reactivation, a
make default or the promotion on archive never brings such a line in.

A **manufacturing order** of a kit BoM — the variant's default or one named by `bomId` —
is **400** `kit_not_manufactured` on `variantId` or `bomId` (`orderBom`, which every
create, draft change and suggestion's action goes through). Replenishment leaves a
variant whose default BoM is a kit out entirely — no *make* and no *buy* suggestion: it is
sold as its components, each judged for itself — the sub-assembly read gives a kit
component no `defaultBomId`, and `Production.HasBOM` answers false for a kit variant, so
Orders offers no manufacturing order on a kit line. The cycle and depth walk counts a kit
as any BoM.

### How deep kits nest

Kits nest at most **`contracts.MaxKitLevels`** (3) levels: the kit is level 1, a component
whose default active BoM is a kit level 2, one among its components level 3. `walkKit`
runs after `walkBom`, under the graph lock, on every write that walks the graph — create,
update, reactivate, make default and the promotion on archive (which skips a BoM that
would fail, as for the depth walk). It counts the kits below through the components'
default active kit BoMs and, for a kit that is or becomes its variant's default, the kits
above it — the variants whose default active BoM is a kit naming it
(`DefaultKitsNamingVariants`), through theirs. More than three in a chain is **400**
`bom_too_deep` with the kit's own title. A normal BoM nests no kit and is not walked.

### KitStructure

Manufacturing provides **`contracts.KitStructure`** (`contracts/kit_structure.go`), the
slot `Module.KitStructure`, resolved from the enabled modules only — nil when
Manufacturing is off — for Orders' kit lines
([Orders](/en/reference/orders/#kits)). `Kits(ctx, variantIDs)` answers, per variant
among the ids whose **default active BoM is a kit**, its components as
`KitComponent{VariantID, PerKit}`:

- each line's quantity per the BoM's output quantity, multiplied through;
- a component whose own default active BoM is a kit is exploded into its components, down
  to three levels (a deeper one, which the writes never allow, would be answered as a
  component);
- a variant reached twice is answered once, the quantities summed, in the order first
  reached (lines by sort order);
- `PerKit` is exact decimal text half-up at three decimals; a component that rounds to 0
  is left out.

A variant with no kit, or a normal default BoM, is **absent**: absent means "not a kit".
A kit is present with its components; present with none — every component rounded away,
which the line rule above prevents — means "a kit with no components", and Orders refuses
it rather than sell it as an ordinary line. The read is **ungated**: it
carries no cost and no person, only which variants make up a kit and how many, and the
consumer decides what to show (module-boundaries rule 12's third kind). It reads in one
read-only, repeatable-read transaction on the pool. Orders calls it before its own
transaction, never inside one.

## The variant holder

The module fills the many-provider slot **`Module.VariantReferences`** with a
`contracts.VariantReferenceHolder` (M16, M16a, rule 20 of the
[module boundaries](/en/contributing/module-boundaries/)). Products asks it inside its own
transaction, after locking the variants:

- for **delete** of a variant; and
- for **type change**, a Goods product turned into a Service, for every variant.

For either purpose, a variant named by one or more **active** BoMs, as their product or a
component, is held: `VariantHold{Kind: open_documents, Documents: n}`, where `n` is the
number of such BoMs. `VariantHold` carries no document list, so the answer is a count
(M16a). An archived BoM holds nothing. Holding on a change to Service departs from Orders'
O30 on purpose: a service can be neither consumed nor produced.

From delivery B the holder also counts the **manufacturing orders** that are not `done` or
`cancelled` and name the variant, as their product or on a component line; `n` is the
BoMs and the orders together. A done or cancelled order holds nothing. From layer G a
by-product counts as a line does: an active BoM making the variant as a by-product, and
an open order with it among its by-products, hold it.

## Personal data

Work centers, closures, BoMs and work orders hold no personal data. A check's result
keeps its recorder (`user_id`, `user_display`), a photo its uploader, and a deviation its
opener and closer, each with the display name as it was — records, kept after the person
is erased like the others below. A result's note and a deviation's text are about the
work, not personal data by intent, and stay (M104). A manufacturing order
names its `responsibleUserId`; a production its poster (`posted_by_user_id`,
`posted_by_display`), an event its actor (`actor_user_id`, `actor_display`), and a time
log its operator (`user_id`, `user_display`), the supervisor who corrected it
(`corrected_by`, `corrected_by_display`) and whose words its note is (`note_by`,
`note_by_display`), and an unbuild its poster (`posted_by_user_id`, `posted_by_display`). These user ids stay with their display snapshot
as records after identity erases the person, as Inventory's ledger keeps its poster
(rule 19 of the [module boundaries](/en/contributing/module-boundaries/)).

A time log's free-text `note` is its writer's own words and does not stay. The writer is
not the operator: it is the supervisor who corrected or added the log, or the person who
blocked the work center, kept as the log's `note_by` (`note_by_display`). An unbuild's
`note` is its poster's words likewise, and the sweep blanks it too. No module is
told of an erasure, so the module asks (rule 19): its background worker
`manufacturing-erasure`, once a day pages through the writers of the notes, 200 user ids
at a time, asks `contracts.UserDirectory.Users` which still exist, and blanks every note
written by those that do not. It takes the logs in id order and skips any another
transaction holds — a block or a correction under way — so it never waits on the shop
floor; a skipped note goes in the next cycle. The log, its times, its kind, its operator's
id and display name and the writer's are kept. The notes are not copied into the order's
timeline, so nothing else is swept. The price is the sweep's cycle: a note can
outlive the erasure by up to a day. A disabled user is still known, and keeps their
notes. The sweep is idempotent, needs no lease, and holds no connection while it asks the
directory. The module registers no customer `PersonalData` slot: it holds nothing about a
customer.

## Refusals

Every refusal is an RFC 9457 problem with this module's `code`; a client keys off the code,
never the words. 404 is a bare body for an id that does not exist. A 400 names the failing
fields in `errors` and a BoM's line refusals in `lines`; `bom_cycle` adds `path`. Request
bodies are strict: an unknown property is a 400.

| Code | Status | Rule |
| --- | --- | --- |
| `revision_conflict` | 409 | A stale `revision` of a work center, a BoM or an order; also a unique-index race on the default (M14a), an order write whose variants kept changing under its lock, and a block or unblock whose center's open logs kept changing ([the order lock order](#the-order-lock-order)) |
| `work_center_code_taken` | 409 | The code is another work center's (M8) |
| `work_center_in_use` | 409 | Delete of a work center a BoM operation, a work order or a time log names; archive it instead (M8) |
| `work_center_has_open_work` | 409 | Archive while a work order that is neither done nor cancelled names it (M8), or while the work center is blocked |
| `closures_too_many` | 400 | More than 100 closures (M9) |
| `site_unknown` | 400 | `siteId` is not an active Inventory site (M8) |
| `site_archived` | 409 | Reactivating a work center whose site is archived |
| `costs_view_required` | 403 | Writing a rate without `manufacturing:costs-view` (M38) |
| `variant_not_goods` | 400 | The product or a component is not goods, judged from `LockVariants` (M10, M10a); a line's code on a line |
| `variant_not_found` | 400 | An unknown variant; a line's code on a line |
| `bom_code_taken` | 409 | The code is another BoM's (M10) |
| `too_many_lines` | 400 | More than 200 lines (M11) |
| `too_many_operations` | 400 | More than 50 operations (M12) |
| `operation_key_unknown` | 400 | A line names a key no operation in the request has (M12); a line's code |
| `work_center_unknown` | 400 | An operation's work center is missing or archived (M12) |
| `work_center_other_site` | 400 | Operations at more than one site; changing a work center's `siteId` while an active BoM has an operation at it, on `siteId` (M12a); an order at another site than its BoM's operations (M17a) |
| `bom_cycle` | 400 | A component is, at some depth, the BoM's own variant; `path` names the cycle (M14) |
| `bom_too_deep` | 400 | The walk passes 10 levels, counting the default BoMs above a default, or the overview meets a made node below the 10th (M14); a kit nesting more than three kits deep, counting the default kits above a default (M144) |
| `bom_too_large` | 400 | The overview passes 2,000 nodes (M15a), or an operation's expected time passes 10^12 minutes |
| `bom_not_active` | 409 | Make default or edit of an archived BoM (M14a); confirm of an order whose BoM is archived |
| `quantity_invalid` | 400 | Not more than 0, or more than three decimals; a line's code on a line; a production's quantities; a kit's line whose quantity per kit is 0 at three decimals (M144) |
| `bom_required` | 400 | A new order for a variant with no active BoM (M17) |
| `kit_not_manufactured` | 400 | A kit BoM with operations, by-products or make-to-order, on its field; a manufacturing order of a kit BoM (M144) |
| `location_unknown` | 400 | An order's location, or a production's, is not an active location of the order's site (M17a) |
| `required_below_used` | 400 | A component line's `required` below its `consumed + scrapped` (M18a) |
| `order_not_draft` | 409 | Confirm of an order that is not a draft, or a change of a draft-only field on a confirmed or in-progress order (M19) |
| `order_not_open` | 409 | A write to a done or cancelled order; Reserve now, a line's add or change, a production or a close on an order that is not confirmed or in progress (M19, M22a) |
| `order_has_production` | 409 | Cancel after something was produced, consumed or scrapped (M19) |
| `consumption_differs` | 409 | A sent `consumed` more than 10 % off its proportional figure; `deviations` names each line; send `acceptDeviation` (M22) |
| `quantity_short` | 409 | Close of less than the quantity without `backorder` (M23) |
| `client_request_reused` | 409 | A production's `clientRequestId` sent before with another body (M22a) |
| `shop_floor_required` | 403 | Registering a production, a shop-floor action (start, pause, finish, scrap) or a block or unblock without `manufacturing:shop-floor` or `manufacturing:orders-manage` (M33) |
| `station_scope` | 403 | A station's operator at an operation not open to stations (the platform's), or at a work center, a work order or an order's production that is not the station's (M131, M135) |
| `work_center_blocked` | 409 | A start at a blocked work center (M30), or a drag of a group work order to a blocked member (M119) |
| `work_order_pending` | 409 | A start without `force`, or a finish, of a work order an earlier one has not finished (M27, M29a) |
| `work_order_closed` | 409 | A start or finish of a work order that is done or cancelled; a finish again without the production it posted (M29) |
| `log_conflict` | 409 | Two starts by one user at the same instant, tripping the one-open-log index (M29a) |
| `log_locked` | 409 | A correction or addition whose log, or new start, is more than 60 days old (M29) |
| `order_closed` | 409 | A correction or addition on an order that is done or cancelled (M29a) |
| `log_overlaps` | 400 | A correction or addition overlapping the user's other logs (M29a) |
| `no_capacity` | 409 | A plan or a reschedule whose work order does not fit on its work center's calendar within 365 days of the start; nothing is written (M35). Plan all names such orders in `noCapacity` instead |
| `period_too_long` | 400 | A report whose `to` is more than 366 days after its `from` (M42) |
| `sales_order_line_unknown` | 400 | A new order's `salesOrderLineId` is not a goods line of a sales order that is not cancelled, or names another variant (M45) |
| `sales_order_line_has_order` | 409 | The sales order line already has its one live order (M44b) |
| `component_unknown` | 400 | A sub-assembly order for an id that is not a component line of the order (M47) |
| `nothing_missing` | 400 | A sub-assembly order for a component line whose missing quantity, less what the open sub-assembly orders of its variant still make, is 0 (M47) |
| `lot_pattern_invalid` | 400 | A BoM's lot pattern whose sample render is no lot number; a production's rendered number that is none (M80) |
| `picks_mismatch` | 400 | A component line's lots do not sum to its consumed and scrap (M82) |
| `serial_quantity` | 400 | A serial product's good output that is not a whole number up to 500, or serial numbers not one per unit (M81) |
| `lot_exists` | 400 | A serial that exists already, on `outputSerials.numbers[i]` or `outputSerials.first`; a counted lot number with no free value in 100, on `outputLot.number` (M57, M80, M81) |
| `lot_not_allowed` | 400 | An output lot or serials for an untracked product; from Inventory, 409, a lot on an untracked component |
| `lot_dates_differ` | 400 | A date sent for the output lot that differs from the existing lot's, on `outputLot` (M57, M80) |
| `lot_required`, `lot_unknown`, `lot_expired`, `lot_blocked` | 409 | Inventory's lot refusals of a production, passed through with `lines[].lotId` (M56, M57, M62) |
| `insufficient_stock`, `insufficient_available`, `site_archived`, `variant_not_stocked`, `document_reused` | 409 | Inventory's refusal of a production or a reserve, passed through under its own code, `lines` naming the component lines; nothing is posted. A reserve's `insufficient_available` is not answered: it starts the second pass |
| `check_limits_invalid` | 400 | A measure check with neither `min` nor `max`, a `max` below its `min`, or a `nominal` outside them (M95) |
| `photo_required` | 400 | A photo check recorded without its `photoId` (M97) |
| `checks_incomplete` | 409 | A production with good output while a check before the output work order, or a first-piece check of it, has no result; a finish while one of the work order's own has none; a close of an order with good output while a check of a work order not cancelled has none; `checks` names them (M98) |
| `check_not_open` | 409 | A result or a photo on a work order that is done or cancelled (M103) |
| `deviation_closed` | 409 | Closing a closed deviation; releasing the holds of an open one (M103) |
| `lot_not_of_order` | 400 | A deviation reported on an order and a lot the order neither made nor consumed, at `lotId` (M99) |
| `lots_view_required` | 403 | A deviation reported on a lot without `inventory:locations-view` and `inventory:stock-view` (M99) |
| `suggestion_not_open` | 409 | Dismissing a suggestion acted on, dismissed or replaced by a later run; among `failed` when an order is asked of one (M111) |
| `reorder_open` | 409 | Among `failed` when an order is asked of a suggestion whose variant has an order from an earlier suggestion still `draft`, `confirmed` or `in_progress` at the site; the suggestion is marked `reorder_open` (M111) |
| `operation_order` | 400 | A `dependsOn` naming an operation that does not come before it by sequence, itself included; the field error is on `operations[i].dependsOn` (M112) |
| `operation_unjoined` | 400 | An operation other than the last by sequence that no operation depends on; the field error is on its own `operations[i].dependsOn` (M112) |
| `operation_unknown` | 400 | A `dependsOn` naming a key no operation of the request has (M112) |
| `work_center_not_in_group` | 400 | A drag or a start of a group work order at a work center that is not an active member of its group, or of a work order of no group at another center (M119) |
| `work_order_started` | 409 | A drag of a group work order to another member once a time log exists (M119) |
| `work_order_member_fixed` | 409 | A start of a group work order at another member than the one a time log or its plan fixed; the detail names it (M119) |
| `work_order_is_output` | 409 | Register quantity on the output work order, the last that is not cancelled (M116) |
| `quantity_over_order` | 400 | Register quantity that would take the work order's passed-on total past the order's quantity (M116) |
| `work_center_group_empty` | 409 | A confirm of an order whose operation targets a group with no active work center (M118) |
| `output_is_product` | 400 | A by-product that is the BoM's own variant (M120) |
| `output_is_component` | 400 | A by-product that is one of the BoM's components (M120) |
| `cost_share_over` | 400 | By-products' cost shares summing to more than 100 (M120) |
| `unbuild_too_much` | 400 | An unbuild of more than the order — or the chosen lot — has left to take apart (M123) |
| `split_sum` | 400 | A split's quantities that do not sum to the order's (M124) |
| `merge_mismatch` | 400 | A merge of orders of another variant, BoM or site, of drafts with confirmed orders, or of different origins (M125) |
| `order_has_work` | 409 | A split or a merge of an order in progress, or with a production, a time log or a registered quantity (M124, M125) |
| `order_has_open_parts` | 409 | A cancel of an order split into parts that are still open (M124) |
| `unbuild_has_byproducts` | 409 | An unbuild from an order whose productions made by-products (M123) |
| `unbuild_not_quarantined` | 409 | A release of an unbuild that quarantined nothing (M123) |
| `attachment_too_large` | 400 | A photo over 10 MB |
| `attachment_type` | 415 | A photo that is not a JPEG or PNG by its own bytes |
| `storage_not_configured` | 503 | A photo with no object store configured |
| `dependency_unavailable` | 503 | The catalog or the stock contract is missing at run time; nothing is changed |

## Enabling and disabling

The smallest installation with Manufacturing is

```text
MODULES=products,inventory,manufacturing
```

Omitting `manufacturing` means the module contributes no route and no permission; its
paths answer the `/api` catch-all 404 and the switcher tile is not shown. The schema is
migrated regardless.

## Endpoints

All under `/api/v1/manufacturing`. Reads need `access`; writes of work centers and BoMs
need `access` and `boms-manage`, writes of orders, time-log corrections and the schedule
`access` and `orders-manage`, the production-cost report `access` and `costs-view`, and a production and every shop-floor action `access` and either
`shop-floor` or `orders-manage`; 401 and 403 are the platform's. Every write that takes a `revision` also answers 409 `revision_conflict`, and
every order write may answer 503 `dependency_unavailable`.

| Operation | Permission | Answers |
| --- | --- | --- |
| `GET /meta` (`getManufacturingMeta`) | access | 200 — the caller's abilities (`canTraceLots` among them) and the currency |
| `GET /stations/{stationId}/work-centers` (`getManufacturingStationWorkCenters`) | access; never the Assistant | 200 the work center ids a station serves |
| `PUT /stations/{stationId}/work-centers` (`putManufacturingStationWorkCenters`) | boms-manage; never the Assistant | 200; 400 `work_center_unknown`, more than 100; 404 a station Identity does not know |
| `GET /stats` (`getManufacturingStats`) | access | 200 — active and blocked work centers, active BoMs; orders to start today, in progress and late; the business calendar's `today` |
| `GET /sites` (`listManufacturingSites`) | access | 200 — Inventory's active sites: `id`, `code`, `name`, `rootLocationId` |
| `GET /variants` (`searchManufacturingVariants`) | access | 200 — goods variants of active products matching `q`, each with `hasBom`; `limit` clamped to 1–50, default 20 |
| `GET /work-centers` (`listManufacturingWorkCenters`) | access | 200 — by code; `status`, `siteId`, `q` |
| `POST /work-centers` (`createManufacturingWorkCenter`) | boms-manage | 201; 400 fields, `site_unknown`; 403 `costs_view_required`; 409 `work_center_code_taken` |
| `GET /work-centers/{id}` (`getManufacturingWorkCenter`) | access | 200; 404 |
| `PUT /work-centers/{id}` (`updateManufacturingWorkCenter`) | boms-manage | 200; 400 fields, `site_unknown`, `work_center_other_site`; 403 `costs_view_required`; 404; 409 `work_center_code_taken`, `revision_conflict` |
| `DELETE /work-centers/{id}` (`deleteManufacturingWorkCenter`) | boms-manage | 204; 404; 409 `work_center_in_use`, `revision_conflict` (`revision` in the query) |
| `POST /work-centers/{id}/archive` (`archiveManufacturingWorkCenter`) | boms-manage | 200; 404; 409 `work_center_has_open_work`, `revision_conflict`; 503 `dependency_unavailable` |
| `POST /work-centers/{id}/reactivate` (`reactivateManufacturingWorkCenter`) | boms-manage | 200; 404; 409 `site_archived`, `revision_conflict` |
| `GET /work-center-groups` (`listManufacturingWorkCenterGroups`) | access | 200 the [groups](#work-center-groups) by code, `status` (active by default) and `siteId` |
| `POST /work-center-groups` (`createManufacturingWorkCenterGroup`) | boms-manage | 201; 400 fields, `site_unknown`, `work_center_unknown`, `work_center_other_site`; 409 `work_center_code_taken` |
| `GET /work-center-groups/{id}` (`getManufacturingWorkCenterGroup`) | access | 200; 404 |
| `PUT /work-center-groups/{id}` (`updateManufacturingWorkCenterGroup`) | boms-manage | 200; 400 fields, `site_unknown`, `work_center_unknown`, `work_center_other_site`; 404; 409 `work_center_code_taken`, `revision_conflict` |
| `POST /work-center-groups/{id}/archive` (`archiveManufacturingWorkCenterGroup`) | boms-manage | 200; 404; 409 `work_center_in_use`, `revision_conflict` |
| `POST /work-center-groups/{id}/reactivate` (`reactivateManufacturingWorkCenterGroup`) | boms-manage | 200; 404; 409 `site_archived`, `revision_conflict` |
| `GET /work-centers/{id}/closures` (`listManufacturingClosures`) | access | 200 — by `fromDate`; 404 |
| `POST /work-centers/{id}/closures` (`createManufacturingClosure`) | boms-manage | 201; 400 dates, `closures_too_many`; 404 |
| `DELETE /work-centers/{id}/closures/{closureId}` (`deleteManufacturingClosure`) | boms-manage | 204; 404 |
| `GET /boms` (`listManufacturingBoms`) | access | 200 — `items` and `total`; `variantId`, `status`, `q`, `limit` clamped to 1–200, `offset` |
| `POST /boms` (`createManufacturingBom`) | boms-manage | 201; 400 fields, lines, `variant_not_goods`, `variant_not_found`, `too_many_lines`, `too_many_operations`, `operation_key_unknown`, `work_center_unknown`, `work_center_other_site`, `bom_cycle`, `bom_too_deep`, `kit_not_manufactured`; 409 `bom_code_taken`, `revision_conflict` |
| `GET /boms/{id}` (`getManufacturingBom`) | access | 200 with operations and lines; 404 |
| `PUT /boms/{id}` (`updateManufacturingBom`) | boms-manage | 200; 400 as create; 404; 409 `bom_code_taken`, `bom_not_active`, `revision_conflict` |
| `POST /boms/{id}/archive` (`archiveManufacturingBom`) | boms-manage | 200; 404; 409 |
| `POST /boms/{id}/reactivate` (`reactivateManufacturingBom`) | boms-manage | 200; 400 `variant_not_goods`, `variant_not_found`, `work_center_unknown`, `work_center_other_site`, `bom_cycle`, `bom_too_deep`; 404; 409 |
| `POST /boms/{id}/make-default` (`makeDefaultManufacturingBom`) | boms-manage | 200; 400 `bom_cycle`, `bom_too_deep`; 404; 409 `bom_not_active`, `revision_conflict` |
| `GET /boms/{id}/overview` (`getManufacturingBomOverview`) | access | 200 [the overview](#the-overview); 400 `quantity_invalid`, `site_unknown`, `bom_too_deep`, `bom_too_large`; 404 |
| `GET /sites/{id}/locations` (`listManufacturingSiteLocations`) | access | 200 — every location of the site, archived ones too, by full code |
| `GET /people` (`searchManufacturingPeople`) | access | 200 — users matching `q`, by display name, for an order's responsible |
| `GET /orders` (`listManufacturingOrders`) | access | 200 — a page of [the list](#the-list); 400 a filter or the paging out of range |
| `POST /orders` (`createManufacturingOrder`) | orders-manage | 201 a draft; 400 fields, `bom_required`, `kit_not_manufactured`, `site_unknown`, `location_unknown`, `work_center_other_site`, `quantity_invalid`, `sales_order_line_unknown`; 409 `bom_not_active`, `sales_order_line_has_order`; 503 Orders off with a `salesOrderLineId` |
| `POST /orders/{id}/children` (`createManufacturingChildOrders`) | orders-manage | 200 `{order, children}`; 400 `component_unknown`, `bom_required`, `nothing_missing`, `work_center_other_site`, `site_unknown`; 404; 409 `order_not_open`, `revision_conflict`, Inventory's refusals |
| `GET /sales-order-lines/{id}` (`getManufacturingSalesOrderLine`) | orders-manage | 200 the line read through Orders, its default BoM and the orders made for it; 404; 503 Orders off |
| `GET /orders/{id}` (`getManufacturingOrder`) | access | 200 with its lines and live availability; 404 |
| `PUT /orders/{id}` (`updateManufacturingOrder`) | orders-manage | 200; 400 as create; 404; 409 `order_not_draft`, `order_not_open`, `revision_conflict` |
| `POST /orders/{id}/confirm` (`confirmManufacturingOrder`) | orders-manage | 200 the order and its `shortLines`; 404; 409 `order_not_draft`, `bom_not_active`, `work_center_group_empty`, Inventory's refusals |
| `POST /orders/{id}/reserve` (`reserveManufacturingOrder`) | orders-manage | 200 the order and its `shortLines`; 404; 409 `order_not_open` |
| `POST /orders/{id}/components` (`addManufacturingOrderComponent`) | orders-manage | 200 the order and its `shortLines`; 400 `variant_not_found`, `variant_not_goods`, `quantity_invalid`; 404; 409 `order_not_open` |
| `PUT /orders/{id}/components/{componentId}` (`updateManufacturingOrderComponent`) | orders-manage | 200 the order and its `shortLines`; 400 `required_below_used`, `quantity_invalid`; 404; 409 `order_not_open` |
| `GET /orders/{id}/production-draft` (`getManufacturingProductionDraft`) | access | 200 [the prefill](#productions) with the lots proposed and offered and the output's lot ([lots on productions](#lots-on-productions)); 400 `quantity_invalid`; 404; 409 `order_not_open` |
| `POST /orders/{id}/productions` (`postManufacturingProduction`) | shop-floor or orders-manage; never the Assistant | 201 the production; 200 a replay; 400 fields, `quantity_invalid`, `location_unknown`, `lot_pattern_invalid`, `picks_mismatch`, `serial_quantity`, `lot_not_allowed`, `lot_dates_differ`, `lot_exists`; 403 `shop_floor_required`; 404; 409 `order_not_open`, `checks_incomplete`, `consumption_differs`, `client_request_reused`, `revision_conflict` (two deadlocks, or lots that kept moving), Inventory's refusals |
| `GET /orders/{id}/lots` (`getManufacturingOrderLots`) | access | 200 [the lots made and consumed](#lots-on-productions), with the product's `tracking`; 404; 503 the catalog |
| `POST /orders/{id}/close` (`closeManufacturingOrder`) | orders-manage | 200 `{order, backorder, shortLines}`; 404; 409 `order_not_open`, `checks_incomplete`, `quantity_short` |
| `POST /orders/{id}/cancel` (`cancelManufacturingOrder`) | orders-manage | 200; 404; 409 `order_not_open`, `order_has_production`, `order_has_open_parts` |
| `POST /orders/{id}/split` (`splitManufacturingOrder`) | orders-manage | 200 `{order, parts, shortLines}` ([split](#split-and-merge)); 400 fields, `quantity_invalid`, `split_sum`; 404; 409 `order_has_work`, `order_not_open`, `revision_conflict`, Inventory's refusals |
| `POST /orders/merge` (`mergeManufacturingOrders`) | orders-manage | 200 `{order, merged, shortLines}` ([merge](#split-and-merge)); 400 fields, `merge_mismatch`; 404; 409 `order_has_work`, `order_not_open`, `revision_conflict`, Inventory's refusals |
| `GET /unbuilds` (`listManufacturingUnbuilds`) | access | 200 a page of [unbuilds](#unbuilds), newest first; `orderId`, `page`, `pageSize`; 400 the paging out of range |
| `POST /unbuilds` (`createManufacturingUnbuild`) | orders-manage | 201 the unbuild; 200 a replay; 400 fields, `quantity_invalid`, `unbuild_too_much`, `lot_required`, `lot_not_of_order`, `lot_not_allowed`, `lot_unknown`, `serial_quantity`, `location_unknown`, `site_unknown`; 404; 409 `order_not_open`, `unbuild_has_byproducts`, `client_request_reused`, `revision_conflict`, Inventory's refusals |
| `GET /unbuilds/{id}` (`getManufacturingUnbuild`) | access | 200 one unbuild with its lines; 404 |
| `POST /unbuilds/{id}/release` (`releaseManufacturingUnbuild`) | quality-manage | 200 the unbuild, its [quarantine released](#unbuilds); 404; 409 `unbuild_not_quarantined` |
| `GET /orders/{id}/events` (`listManufacturingOrderEvents`) | access | 200 [the history](#history), oldest first; 404 |
| `GET /productions/{id}` (`getManufacturingProduction`) | access | 200 one production with its lines; 404 |
| `GET /orders/{id}/work-orders` (`listManufacturingOrderWorkOrders`) | access | 200 the order's [work orders](#work-orders) by sequence; 404 |
| `GET /orders/{id}/time-logs` (`listManufacturingOrderTimeLogs`) | access | 200 every [time log](#time-logs) on its work orders, by start; 404 |
| `GET /work-centers/{id}/queue` (`getManufacturingWorkCenterQueue`) | access | 200 [the queue](#the-queue-and-my-work) and the block; 404 |
| `POST /work-centers/{id}/block` (`blockManufacturingWorkCenter`) | shop-floor or orders-manage; never the Assistant | 200 the queue; 400 fields; 403 `shop_floor_required`; 404; 409 `revision_conflict` |
| `POST /work-centers/{id}/unblock` (`unblockManufacturingWorkCenter`) | shop-floor or orders-manage; never the Assistant | 200 the queue; 403 `shop_floor_required`; 404; 409 `revision_conflict` |
| `GET /work-orders/{id}` (`getManufacturingWorkOrder`) | access | 200 the work order view; 404 |
| `POST /work-orders/{id}/start` (`startManufacturingWorkOrder`) | shop-floor or orders-manage; never the Assistant | 200 `{workOrder, closedLog, production}`; 400 `work_center_not_in_group`; 403 `shop_floor_required`; 404; 409 `order_not_open`, `work_order_closed`, `work_order_pending`, `work_center_blocked`, `log_conflict`, `revision_conflict`, `work_order_member_fixed` |
| `POST /work-orders/{id}/quantity` (`registerManufacturingWorkOrderQuantity`) | shop-floor or orders-manage; never the Assistant | 200 `{workOrder, closedLog, production}`; 400 fields, `quantity_invalid`, `quantity_over_order`; 403 `shop_floor_required`; 404; 409 `order_not_open`, `work_order_closed`, `work_order_pending`, `work_order_is_output`, `checks_incomplete`, `client_request_reused` ([the send-ahead quantity](#the-send-ahead-quantity)) |
| `POST /work-orders/{id}/pause` (`pauseManufacturingWorkOrder`) | shop-floor or orders-manage; never the Assistant | 200 `{workOrder, closedLog, production}`; 403 `shop_floor_required`; 404; 409 `order_not_open`, `revision_conflict` |
| `POST /work-orders/{id}/finish` (`finishManufacturingWorkOrder`) | shop-floor or orders-manage; never the Assistant | 200 `{workOrder, closedLog, production}`; 400 fields, `quantity_invalid`, `location_unknown`; 403 `shop_floor_required`; 404; 409 `order_not_open`, `work_order_closed`, `work_order_pending`, `checks_incomplete`, `revision_conflict`, the production's `consumption_differs`, `client_request_reused` and Inventory's refusals |
| `POST /work-orders/{id}/scrap` (`reportManufacturingWorkOrderScrap`) | shop-floor or orders-manage; never the Assistant | 201 the production; 200 a replay; 400 fields, `quantity_invalid`; 403 `shop_floor_required`; 404; 409 `order_not_open`, `client_request_reused`, Inventory's refusals |
| `GET /work-orders/{id}/time-logs` (`listManufacturingWorkOrderTimeLogs`) | access | 200 the work order's time logs, by start; 404 |
| `POST /work-orders/{id}/time-logs` (`addManufacturingTimeLog`) | orders-manage | 201 the log; 400 fields, `log_overlaps`; 404; 409 `order_closed`, `log_locked`, `revision_conflict` |
| `PATCH /time-logs/{id}` (`correctManufacturingTimeLog`) | orders-manage | 200 the log; 400 fields, `log_overlaps`; 404; 409 `order_closed`, `log_locked`, `revision_conflict` |
| `GET /shop-floor/mine` (`getManufacturingMyWork`) | access | 200 [My work](#the-queue-and-my-work) |
| `POST /orders/{id}/plan` (`planManufacturingOrder`) | orders-manage | 200 the order; 404; 409 `revision_conflict`, `order_not_open`, `no_capacity` |
| `POST /orders/{id}/unplan` (`unplanManufacturingOrder`) | orders-manage | 200 the order; 404; 409 `revision_conflict`, `order_not_open` |
| `POST /schedule/plan-all` (`planAllManufacturingOrders`) | orders-manage | 200 `{planned, noCapacity}`; 409 `revision_conflict` |
| `POST /work-orders/{id}/reschedule` (`rescheduleManufacturingWorkOrder`) | orders-manage | 200 the order's work orders; 400 `work_center_not_in_group`; 404; 409 `revision_conflict`, `order_not_open`, `work_order_closed`, `work_order_started`, `work_center_blocked`, `no_capacity` |
| `GET /schedule` (`getManufacturingSchedule`) | access | 200 [the board](#the-board); 400 `days` |
| `GET /reports/production-cost` (`getManufacturingProductionCostReport`) | access and costs-view | 200 JSON or CSV; 400 the period, `period_too_long` |
| `GET /reports/utilisation` (`getManufacturingUtilisationReport`) | access | 200 JSON or CSV; 400 the period, `period_too_long` |
| `GET /reports/time-deviation` (`getManufacturingTimeDeviationReport`) | access | 200 JSON or CSV; 400 the period, `period_too_long` |
| `POST /work-orders/{id}/checks/{checkId}/result` (`recordManufacturingCheckResult`) | shop-floor or orders-manage; never the Assistant | 200 the work order view; 400 fields, `photo_required`; 403 `shop_floor_required`; 404; 409 `order_not_open`, `check_not_open`, `revision_conflict`, Inventory's refusal of a hold |
| `POST /work-orders/{id}/photos` (`uploadManufacturingCheckPhoto`) | shop-floor or orders-manage; never the Assistant | 201 the photo; 400 `attachment_too_large`, `file`; 403 `shop_floor_required`; 404; 409 `check_not_open`; 415 `attachment_type`; 503 `storage_not_configured` |
| `GET /work-orders/{id}/photos/{photoId}` (`getManufacturingCheckPhoto`) | access; never the Assistant | 200 the picture; 404; 503 |
| `GET /deviations` (`listManufacturingDeviations`) | access | 200 JSON or CSV; 400 `from` after `to` |
| `POST /deviations` (`createManufacturingDeviation`) | shop-floor or orders-manage; with a lot, Inventory's locations-view and stock-view too | 201 the deviation; 400 fields, `lot_not_of_order`; 403 `shop_floor_required`, `lots_view_required`; 409 `revision_conflict`, Inventory's refusal of a hold |
| `GET /deviations/{id}` (`getManufacturingDeviation`) | access | 200; 404 |
| `POST /deviations/{id}/close` (`closeManufacturingDeviation`) | quality-manage | 200; 400 fields; 404; 409 `deviation_closed`, `revision_conflict` |
| `POST /deviations/{id}/release` (`releaseManufacturingDeviationHolds`) | quality-manage | 200; 404; 409 `deviation_closed`, `revision_conflict` |

The full contract, with every schema, is `openapi/manufacturing.yaml`, published in the
generated API reference (`/en/reference/api/manufacturing/`).
