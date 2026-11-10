# Manufacturing — work centers, bills of materials, manufacturing orders, the shop floor (phase 1)

Revision 1, 2026-11-11. This spec builds on the research
`docs/superpowers/research/2026-11-11-manufacturing-module.md` ("R"). It does not reopen
R §8, and it does not reopen the defaults R §9 took for the readings. Decisions are
numbered M*n*. The implementation plan is
`docs/superpowers/plans/2026-11-11-manufacturing-plan.md`. Abbreviations:

- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/manufacturing/frontend/src/`.

## 1. Purpose

Manufacturing is a new module: **Manufacturing** (`manufacturing`; Norwegian
**Produksjon**; package `@vantigo/manufacturing-ui`). It plans, schedules and tracks
production in real time:

- **Planning:** *what* to make, using bills of materials (BoMs).
- **Scheduling:** *where and when*, using work centers and their calendars.
- **Tracking:** what is happening *now*, on a live shop floor that operators work from.

Stock always moves through Inventory's ledger. A production consumes components and
produces the finished item in **one movement**.

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **0** | The seams: `StockLedger.Produce`, reserving for production, the `production` kind, standard cost on the catalog contract | — |
| **A** | The module, work centers, BoMs with lines and operations, the BoM overview, the holder | 0 |
| **B** | Manufacturing orders: snapshot, reservations, availability, productions, scrap, close and backorder, cancel | A |
| **C** | Work orders, time logs, the live shop floor, downtime | B |
| **D** | The production schedule (planning work orders on calendars, the board, load) and cost (estimate, actual, deviation) | C |
| **E** | Make-to-order from Orders, and child MOs for sub-assemblies | B (and D for dates) |

**Not in this phase.** R §7 lists these for phase 2 and later. §13 says what each waits
for.

- lots and serials;
- quality checks;
- operation dependencies;
- alternative work centers;
- reorder rules;
- by-products;
- kits;
- unbuild;
- split and merge;
- subcontracting;
- the PIN kiosk;
- OEE;
- maintenance;
- MPS;
- BoM change control;
- valuation.

## 2. Decisions — delivery 0, the seams

- **M1 — The `production` kind.** Inventory gains the kind `production`.
  - A production movement's lines are of two sorts:
    - *consume* lines have only *from*;
    - *produce* lines have only *to*.
  - Every location lies in the movement's one site, as every movement's does (the
    existing `move_crosses_sites` rule).
  - The movement's source is the producing document. Lines keep the existing snapshots.
  - A produce line of a variant that is not goods is refused (`variant_not_stockable`, as
    on a receipt). A Discontinued variant may still be produced: the item is being used
    up, not bought.
  - The movements list, its filter and the movement page show the kind as **Production**
    / **Produksjon**.
- **M2 — `StockLedger.Produce`.**
  ```go
  Produce(ctx, tx, src StockSource, p StockProduction) (movementID int64, err error)
  type StockProduction struct {
      Document        StockSource      // the production record; src when zero
      SiteID          int64
      Consume         []ConsumeLine    // VariantID, LineID, FromLocationID, Quantity
      Produce         []ProduceLine    // VariantID, LineID, ToLocationID, Quantity
      PostedByUserID  *uuid.UUID
      PostedByDisplay string
  }
  ```
  - It posts one movement of kind `production`.
  - Consuming a line uses up as much of `src`'s reservation for that line as it consumes,
    never going below zero, exactly as `Issue` does.
  - Posting is idempotent by `Document`. A second call returns the first movement.
  - Stock the location does not hold is refused with `insufficient_stock`, naming every
    failing line. Other documents' reservations never stop a production.
  - At least one line must be given. A production with only consume lines is allowed: it
    is a scrap of components.
  - It keeps every rule in the `StockLedger` doc comment, including the lock order:
    `LockVariants` first, then the caller's rows, then the balances.
- **M3 — Reserving for production.** `StockLedger.ReserveForProduction(ctx, tx, src, siteID,
  lines)` is `Reserve` without the *fulfils orders* check.
  - A plant that sells nothing still reserves components.
  - Every other check stays: the variant is goods, and the site is active and has the
    stock available.
  - Release, `ReservationsFor` and the product page's *Reserved* figure treat these
    reservations like any other. The figure names the reserving module (`manufacturing`)
    as it names `orders`.
- **M4 — Standard cost on the catalog.** `contracts.VariantEntry` gains `StandardCost
  string`: exact decimal text, or `""` when not set. Products fills it in `Variant`,
  `Variants` and `LockVariants`. It is the only cost Manufacturing reads (R §9 reading 6).
- **M5 — A movement's source on screen.** When a movement's source module is
  `manufacturing`, the movement page links its source to `/manufacturing/orders/{id}`.
  The link uses the order id carried on the production record's source (M22). This
  follows the Orders link that already exists.

## 3. Decisions — delivery A, the module, work centers and BoMs

### The module

- **M6 — Registration.**
  - `MODULES` names `manufacturing`. It **requires `products` and `inventory`**
    (R §9 reading 2). The config check refuses it otherwise, with the rationale in the
    `modules()` doc comment.
  - `orders` is an **optional** partner. Delivery E reads Orders through a contract when
    the contract is there.
  - The module is a full participant in every registry AGENTS.md lists:
    - the integration test's `everyModule()`;
    - the Assistant's route prefixes, with a bump to `promptVersion`;
    - every depguard set;
    - `modulesEnv`;
    - the host's `moduleKeys`, `apps.ts`, layout route, `i18n.ts`, permission labels,
      dashboard card, spotlight and `sessionClients`.
- **M7 — Screens.** The app's navigation, in order:
  1. **Overview** (`/manufacturing`): KPI cards for MOs to start today, in progress,
     late, and work centers blocked. Below them, the list of MOs that are late or due
     this week.
  2. **Manufacturing orders** (`/manufacturing/orders`).
  3. **Shop floor** (`/manufacturing/shop-floor`), from delivery C.
  4. **Production schedule** (`/manufacturing/schedule`), from delivery D.
  5. **Bills of materials** (`/manufacturing/boms`).
  6. **Work centers** (`/manufacturing/work-centers`).

  The Norwegian labels are *Oversikt, Produksjonsordrer, Verksted, Produksjonsplan,
  Stykklister, Arbeidsstasjoner*.

### Work centers

- **M8 — A work center** (nb *arbeidsstasjon*) is a machine, a cell or a team where an
  operation is done. It has:
  - a `code`: 1–16 characters from `[A-Z0-9]`, unique, stored upper-case;
  - a `name`;
  - a `siteId`: the Inventory site it stands at, checked through `Stock.Sites`;
  - a `capacity`: the units processed in parallel per cycle, an integer of 1 or more,
    default 1;
  - a `timeEfficiency`: a percentage from 1 to 500, default 100. At 50 the work takes
    twice as long;
  - `setupMinutes` and `cleanupMinutes`: decimals of 0 or more, applied once per work
    order;
  - `machineCostPerHour` and `labourCostPerHour`: exact decimals in the workspace
    currency (NOK), both optional;
  - a **weekly working pattern** (M9);
  - a `status`: `active` or `archived`;
  - a `revision`, for optimistic concurrency (`revision_conflict`, 409);
  - a `working state`: `normal` or `blocked` (M30), changed only through block and
    unblock, never through an edit.

  A work center is never deleted once an operation or a work order names it. It is
  archived instead (`work_center_in_use`). Archiving is refused while an open work order
  names it (`work_center_has_open_work`).
- **M9 — The working pattern.** This is the work center's calendar.
  - It holds up to two intervals per weekday, as local times in the workspace zone
    (`contracts.BusinessCalendar`), on a 15-minute grid. The default is Monday to Friday,
    07:00–15:00.
  - Norwegian public holidays (`holidays.PublicHoliday`) are closed days. So are
    **closures**: date ranges with a note, at most 100 per work center, such as an
    industrial holiday or a breakdown.
  - The **capacity of a day** is the length of its open intervals.
  - A shift pattern that rotates week by week is later (§13).

### Bills of materials

- **M10 — A BoM** (nb *stykkliste*) tells how to make one variant. It has:
  - a `variantId`: goods only, checked through `ProductCatalog`, refused otherwise
    (`variant_not_goods`);
  - a `code`: optional, 1–32 characters, unique when given, such as *Rev B*;
  - an `outputQuantity`: more than 0, with at most three decimals. Lines and operation
    times are stated per that quantity;
  - `isDefault`: exactly one active BoM per variant is the default. The first one becomes
    the default; making another the default unmarks the old one in the same transaction;
  - `makeToOrder`: false by default (delivery E, M44);
  - `leadTimeDays`: an integer of 0 or more, default 0. It is used when the variant has
    no operations, and for the start date of a parent MO (M37);
  - a `note` of at most 2,000 characters;
  - a `status`: `active` or `archived`;
  - a `revision`.

  A variant may have several active BoMs, for alternative recipes. An MO picks one; the
  default is preselected.
- **M11 — BoM lines.** At most 200 per BoM. Each has:
  - a component `variantId`, which must be goods;
  - a `quantity` per `outputQuantity`: more than 0, with at most three decimals;
  - a `scrapPercent` from 0 to 99.99, default 0. It is the expected loss, added to the
    requirement (M18). Business Central has this and Odoo lacks it (R §2.4);
  - an optional `operationId`: the operation that consumes the component. It is shown
    on that work order; consumption is still posted per production (M22);
  - a `sortOrder` and a `note`.

  The same component may appear on two lines.
- **M12 — Operations.** At most 50 per BoM. Each has:
  - a `sequence`;
  - a `name`;
  - a `workCenterId` for an active work center;
  - `setupMinutes`: optional. When empty, the work center's setup applies;
  - `minutesPerCycle`: more than 0. A cycle is one run of the work center's capacity;
  - `instructions`: Markdown text of at most 20,000 characters, shown at the station.

  Operations run **in sequence order** (M27). They are edited with the BoM in the same
  request, and lines refer to them by client keys so that both can be created at once.
- **M13 — Editing a BoM** replaces its lines and operations in one request, under its
  `revision`.
  - Open MOs keep their snapshot (M17), so an edit is always allowed.
  - Removing an operation that a line refers to clears the line's `operationId` in the
    same request.
- **M14 — No cycles.** A BoM is refused (`bom_cycle`, naming the path) when any
  component, at any depth, through each component's default active BoM, is the BoM's
  own variant. The walk stops at **10 levels** (`bom_too_deep`), which also bounds the
  overview.
- **M15 — The BoM overview** (`GET /boms/{id}/overview?quantity=`) explodes the BoM.
  - It recurses through each component's default active BoM, up to 10 levels.
  - Per node it gives:
    - the quantity required, including scrap;
    - the standard cost (M4) and its extended value;
    - the available quantity at a chosen site, from `Stock.Available`;
    - the operations' expected time (M28);
    - whether the component is **made** (it has a BoM) or **bought** (it has none).
  - The **rolled-up cost** has two parts:
    - the components at standard cost, using the leaf's standard cost when a made
      component has none of its own;
    - each operation's expected hours × (the machine rate + the labour rate).

    A component with no standard cost is counted as 0 and flagged
    (`costIncomplete: true`).
  - Writing the rolled-up cost back to the product's standard cost is later (§13).
- **M16 — The holder.** Manufacturing's `VariantReferenceHolder` answers:
  - for **delete**: a variant named by an active BoM (as product or component), or by an
    MO that is not done or cancelled, is held, with kind `open_documents` and the
    documents listed;
  - for **type change** (Goods → Service): the same.

  An archived BoM holds nothing.

## 4. Decisions — delivery B, manufacturing orders

- **M17 — A manufacturing order** (nb *produksjonsordre*) is numbered `MO-<n>` from the
  module's own counter, assigned at creation. It has:
  - a `variantId` and a `bomId`;
  - a `quantity`: more than 0, with at most three decimals;
  - a `siteId`: by default the BoM's work centers' site, else the first active site;
  - a `componentLocationId`: optional. When empty, each component is taken from its
    primary location (Inventory placement), else from where it lies;
  - an `outputLocationId`: default the site's root location;
  - a `dueDate`;
  - `plannedStart` and `plannedFinish`: timestamps, set by planning (M35) or by hand;
  - a `priority`: `normal` or `urgent`;
  - a `responsibleUserId`;
  - a `note`;
  - an `origin`: `manual`, `sales_order` (E) or `parent` (E). It carries the origin's ids
    and a display reference;
  - a `status` (M19);
  - a `revision`.

  **The snapshot:** on **confirm**, the BoM's lines and operations are copied onto the
  order as **component lines** and **work orders** (delivery C). Later BoM edits never
  reach a confirmed MO. A draft follows its BoM until it is confirmed.
- **M18 — Component lines.** A component line has:
  - a variant;
  - a `required` quantity: `line quantity × MO quantity / outputQuantity ×
    (1 + scrapPercent/100)`, rounded **up** to three decimals;
  - `consumed` and `scrapped`: running totals from productions;
  - an optional `operationId`;
  - snapshots of the SKU, name and unit.

  On an MO that is in progress, lines can be **added** (an extra component) or have their
  `required` changed. This is Odoo's *Add component*. Each such edit adjusts the
  reservation (M20).
- **M19 — States.**
  ```
  draft ──confirm──▶ confirmed ──first start or production──▶ in_progress ──close──▶ done
    │                    │                                         │
    └──cancel────────────┴──cancel (nothing produced) ─────────────┴─▶ cancelled
  ```
  - Only `draft` is freely editable. `confirmed` and `in_progress` allow:
    - editing the dates, the priority, the responsible user, the note and the locations;
    - editing the component lines (M18).
  - `in_progress` is entered by the first work-order start (C) or the first production.
  - `cancel` is allowed while nothing has been produced or consumed
    (`order_has_production` otherwise). Cancelling releases every reservation and
    cancels the work orders. A cancelled MO keeps its number.
  - `done` and `cancelled` are final. Undoing a production is **unbuild**, which is
    phase 2. Until then, an Inventory adjustment corrects a mistake.
- **M20 — Reservations.**
  - **Confirm** reserves each component line's `required` at the MO's site through
    `ReserveForProduction` (M3). The source is `manufacturing/order/<id>` and the line id
    is the component line.
  - If the site does not have enough available, confirm still succeeds **without
    reserving the short lines**. The response and the order page name the short lines,
    and **Reserve now** retries them later. Orders behaves the same way with *Confirm
    without reserving*: confirming never fails for stock alone.
  - A production consumes from the reservation (M2).
  - Editing a line adjusts its reservation.
  - Close and cancel release whatever remains.
- **M21 — Availability.** An MO's component status is shown on the list, the order page
  and the overview card. It is one of:
  - **Ready**: every line's remaining requirement is reserved, or available at the site;
  - **Partly available**: some lines are covered and some are not;
  - **Waiting**: no line is covered.

  Each line shows its required, reserved, available at the site, consumed and missing
  quantities. **Missing** is the reason, said plainly — R §2.8's gap.
- **M22 — A production** (nb *produksjonsmelding*) records output against the MO. Its
  request body has:
  - `quantity`: what was produced, of 0 or more. 0 is allowed only with scrap or
    consumption;
  - `consumed`: per component line, the quantity used. It is **prefilled
    proportionally**: `required × quantity / MO quantity`, less nothing, rounded to
    three decimals. It is editable, and lines may be omitted;
  - `scrap`: per component line, a quantity lost, consumed with no output;
  - `outputScrap`: finished units produced and discarded. These are counted in
    `produced` for cost but not put into stock;
  - optional `fromLocationId` per line and an optional `toLocationId`, overriding the
    MO's defaults;
  - a `clientRequestId` for idempotency.

  Posting it:
  - stores the production record (`manufacturing.productions` with its lines);
  - calls `StockLedger.Produce`. The document is `manufacturing/production/<id>` and
    `src` is the MO;
  - updates the line totals and the MO's `produced`;
  - moves the MO to `in_progress`.

  **The consumption warning** (R §9 reading 10). When any consumed quantity differs from
  the proportional figure by more than 10 % of it, the response is
  `consumption_differs` (409), listing each deviating line. The same request with
  `acceptDeviation: true` posts it. Nothing is ever blocked outright.
- **M23 — Closing.**
  - **Close** finishes the MO:
    - it releases the remaining reservations;
    - it cancels any work orders not done;
    - it sets `closedAt` and `done`, and fixes the actual cost (M41).
  - When `produced` is less than `quantity`, close asks **Create backorder?** It answers
    with `quantity_short` (409) unless `backorder` is `true` or `false`.
    - `true` creates a new draft MO for the rest. It is numbered `MO-<n>-2` (then `-3`)
      from the original, has the same BoM, origin and due date, and is linked as
      `backorderOf`. The new MO is confirmed at once when the original was, so it
      reserves again.
    - `false` closes short.
  - Producing more than `quantity` is allowed. The overrun shows on the order page.
- **M24 — Lists and filters.** The MO list:
  - filters by status (default: the open statuses), site, work center (any of its work
    orders), variant, due date range, `late` (due before today and not done), origin and
    responsible user;
  - sorts by due date, number or priority;
  - is cursor-paginated, as Orders is.
- **M25 — Live.** The topic `manufacturing.orders` (keyless, the list) and
  `manufacturing.order:<id>` (one order). Each write notifies once per aggregate on the
  writing transaction (the K-D rules). The list and the page refetch on the event.
- **M26 — The timeline.** Each MO keeps an append-only list of its events: created,
  confirmed, reserved, production posted (with the quantities), closed, cancelled,
  backorder created, line added. Each event records who and when. The order page shows
  it under *History*.

## 5. Decisions — delivery C, work orders and the shop floor

- **M27 — Work orders** (nb *arbeidsordre*). There is one per snapshotted operation. A
  work order has:
  - the operation's sequence, name, work center, setup, minutes per cycle and
    instructions, all as snapshots;
  - an expected duration (M28);
  - `plannedStart` and `plannedFinish` (D);
  - a `status`:
    - `pending`: an earlier work order is not done;
    - `ready`;
    - `in_progress`: at least one open time log;
    - `paused`: started before, with no open log;
    - `done`;
    - `cancelled`.

  The first work order is `ready` on confirm. When a work order finishes, the next by
  sequence becomes `ready`. Operations do **not** run in parallel in phase 1; a
  dependency graph is phase 2. A work order may be started out of order with
  `force: true`, and the time log is marked so. The research found that real shops
  overlap operations, and refusing would push them off the system.

  An MO with no operations has no work orders. Productions are posted on the order page.
- **M28 — Expected duration**, in minutes:
  `setup + cleanup + ⌈remaining quantity / capacity⌉ × minutesPerCycle × 100 /
  timeEfficiency`. Here, remaining quantity is the MO quantity scaled to the BoM's
  output, in cycles. This is Odoo's formula (R §2.1). It is computed at snapshot and
  recomputed when the MO quantity changes.
- **M29 — Time logs** (nb *tidsregistrering*). Each row has:
  - the work order, the work center and the user;
  - `startedAt` and `endedAt`; `endedAt` is null while the log is running;
  - a `kind`: `productive` or `downtime`;
  - for downtime, a `reason` from `breakdown`, `material_missing`, `setup`,
    `waiting_for_operator`, `quality` or `other`, and an optional note;
  - an optional `quantity` registered at finish.

  The actions, each a request:
  - **Start** opens a productive log for the caller. One caller has at most one open
    log in the whole workspace: starting elsewhere closes the open log first, in the
    same transaction.
  - **Pause** closes the caller's open log on that work order.
  - **Finish** closes every open log on the work order, sets it `done`, and readies the
    next one. On the **last** work order, finish offers to **register the production**
    (M22) in the same screen. The request may carry a production body, posted in the
    same transaction.
  - Several operators may have open logs on one work order at once.

  Edits: a supervisor (`manufacturing:orders-manage`) may correct a closed log's times,
  or add a missing one, with a note. The correction is kept in the MO timeline. A log
  older than **60 days** cannot be corrected (`log_locked`). An open log left running
  past the end of the work center's day is **not** closed automatically; the shop floor
  shows it with a warning after 12 hours.
- **M30 — Downtime and blocking.**
  - **Block** a work center, with a reason and a note:
    - it sets `workingState = blocked`;
    - it closes every productive log at the center, turning the operators' time into
      one downtime log per operator with that reason;
    - it opens a center-level downtime log with no user, so the stopped time is counted.
  - **Unblock** closes those logs.
  - While a center is blocked, its work orders cannot be started (`work_center_blocked`).
  - Phase 3's OEE reads these rows; phase 1 only reports the downtime per reason (D).
- **M31 — The shop floor** (`/manufacturing/shop-floor`, nb *Verksted*) is a screen built
  for a tablet: large touch targets, no hover, readable at arm's length.
  - **Work center picker.** The choice is remembered on the device, in localStorage
    inside try/catch, as a convenience only.
  - **The work center page** shows:
    - a queue of the center's `ready`, `in_progress` and `paused` work orders, ordered by
      planned start, then MO priority, then due date;
    - for each work order: the MO number, the product, the quantity remaining, the due
      date, the component status (M21), and who is running it;
    - a **Block** / **Unblock** button.
  - **The work order view** has:
    - large Start, Pause and Finish buttons, with the caller's running timer;
    - the instructions, rendered as Markdown;
    - the components consumed at this operation (M11);
    - **Register production** on the last operation;
    - **Report scrap**;
    - the other operators on it, live through presence.
  - **My work** (`/manufacturing/shop-floor/mine`) shows the caller's open log and the
    work orders they touched today.
  - The operator is the signed-in user (R §9 reading 4). Switching operator means
    switching account.
- **M32 — Live.**
  - The topic `manufacturing.work-center:<id>` carries the queue and blocking events.
  - Work-order and time-log writes notify the work center, the MO, and the list.
  - The work order view uses `usePresence` on `manufacturing.work-order:<id>`.
  - Running timers count on the client from `startedAt`, so the server sends no ticks.
- **M33 — Permissions on the floor.** Start, pause, finish, scrap and register need
  `manufacturing:shop-floor` **or** `manufacturing:orders-manage`. The operation's access
  rule requires `manufacturing:access`, and the handler checks the either-or. Block and
  unblock need the same permissions.

## 6. Decisions — delivery D, the schedule and cost

### The production schedule

- **M34 — Dates.**
  - An MO's `dueDate` is when the finished goods must be ready.
  - Its **planned window** is the earliest planned start and the latest planned finish
    of its work orders. With no work orders, the window is
    `dueDate − leadTimeDays … dueDate`, counted in business days.
- **M35 — Plan.** **Plan** on an MO (or **Plan all** on the confirmed, unplanned MOs,
  taken by priority and then due date) places its unfinished work orders **forward**.
  - The search starts from `max(now, the MO's requested start)`. The requested start
    defaults to
    `dueDate − (Σ expected durations converted to working days on the work centers) −
    1 day of slack`, or now, whichever is later.
  - Each work order goes into the **first free slot** of its work center's calendar (M9)
    that is long enough to hold it. A work order may be **split across open intervals
    and days**, as a slot is the working time it needs, not one contiguous block.
  - A slot is free when no planned, not-done work order overlaps it.
  - Work orders run after their predecessor's planned finish.
  - The search looks at most **365 days** ahead (`no_capacity`).
  - The result sets each work order's `plannedStart` and `plannedFinish`, and the MO's
    window. A finish after `dueDate` is flagged **Late by n days** on the MO.
  - Planning takes an advisory lock per work center, in id order, so two plans cannot
    double-book a center.
  - **Unplan** clears the dates.
  - This is Odoo's first-free-slot (R §2.1, §9 reading 7): finite capacity, one center at
    a time, no solver.
- **M36 — The board** (`/manufacturing/schedule`).
  - **Layout:**
    - one row per active work center at the chosen site;
    - one column per day, for a week (the default) or two weeks;
    - work orders drawn as bars at their planned times, labelled with the MO and the
      product;
    - urgent work orders marked; late ones (finish after due) in the danger colour;
    - blocked centers and closures greyed.
  - **Load:** each row shows the **load per day**, the planned minutes against the
    day's capacity, as a percentage. Above 100 % is marked.
  - **Dragging** a bar to another day or time re-plans **that work order** to start
    there:
    - it takes that day's first free slot from that time;
    - the work orders after it on the same MO move after it, when they would otherwise
      overlap;
    - other MOs never move.

    The request is `POST /work-orders/{id}/reschedule {start}` under the work order's
    revision.
  - **Unscheduled** lists the confirmed MOs with no plan, with **Plan** buttons.
  - A drag to another work center is phase 2 (alternative centers).
  - The board is live on `manufacturing.schedule:<siteId>`.
- **M37 — Lead time for sub-assembly MOs (E).** A child MO's `dueDate` is its parent's
  planned start, or the parent's requested start when the parent is unplanned.

### Cost

- **M38 — Who sees cost.** Every cost figure needs `manufacturing:costs-view` (a
  sensitive permission). Without it, cost fields are **left out of the response**, not
  refused, as Projects shapes financial fields.
- **M39 — The estimate** is fixed at confirm. It has three parts:
  - **components**: Σ required × the standard cost at confirm;
  - **machine**: Σ the work orders' expected hours × the center's machine rate;
  - **labour**: Σ the expected hours × the center's labour rate, assuming one operator.

  The rates and standard costs used are snapshotted, so a later price change never
  alters an estimate.
- **M40 — The running actual** is shown while the MO is open:
  - **components**: Σ (consumed + scrapped) × the standard cost snapshotted at confirm;
  - **machine**: the hours the work center ran on this MO's work orders, meaning the
    union of the productive logs per work order, × the machine rate;
  - **labour**: Σ every operator's productive log hours × the labour rate.

  Downtime is listed separately and not charged to the MO.
- **M41 — At close,** the actual is fixed together with:
  - the **cost per unit**: actual ÷ produced, where `produced` excludes output scrap;
  - the **deviation** from the estimate, in amount and %, per part;
  - the **duration deviation** per work order: actual minutes against expected.
- **M42 — Reports** (`/manufacturing/reports`, under Overview), all filterable by period
  and site, with CSV export:
  - **Production cost:** done MOs, with estimate, actual, deviation and cost per unit,
    grouped by product.
  - **Work-center utilisation:** per center and week, the capacity, the planned time, the
    productive time and the downtime by reason.
  - **Time deviation:** per operation (BoM and sequence), the average of actual against
    expected over the last 10 done work orders. This is the input for phase 2's
    auto-computed standards.

## 7. Decisions — delivery E, make-to-order and sub-assemblies

- **M43 — `contracts.SalesOrderLines`**, provided by Orders (optional; nil when Orders is
  off):
  ```go
  Lines(ctx, lineIDs []int64) ([]SalesOrderLine, error)
  OrderLines(ctx, orderID int64) ([]SalesOrderLine, error)
  // SalesOrderLine: LineID, OrderID, OrderNumber, CustomerID, CustomerName,
  // VariantID, Quantity (open: ordered − cancelled − delivered), DeliveryDate,
  // SiteID, OrderStatus
  ```
  It reads on the pool and is never called inside a transaction that holds a lock. No
  personal data is carried beyond the customer's name, which Orders' own lists show to
  `orders:access`.
- **M44 — Automatic MTO.** An event handler on `orders.sales_order.confirmed`
  (`manufacturing.mto`) reads the order's lines.
  - For each goods line whose variant's **default active BoM** has `makeToOrder = true`,
    and which no MO already names, it creates a **confirmed** MO with:
    - origin `sales_order`, carrying the order id, the line id and `SO-<number>`;
    - the line's open quantity, its delivery date as `dueDate`, and the order's site;
    - the system as poster.
  - It is idempotent by (order line) through a unique index.
  - Shortage never stops it (M20).
  - `orders.sales_order.cancelled` does **not** cancel the MOs. It flags them **Origin
    cancelled** on the list and the order page, because goods may already be half made
    and a person decides.
- **M45 — Manual MTO.** On a sales order's page, each goods line that has a BoM shows:
  - **Create manufacturing order**, which opens the new-MO form prefilled from the line,
    with `origin=sales_order` (needs `manufacturing:orders-manage`);
  - the **production status** of the MOs that name it: number, status, produced and
    quantity, planned finish. This comes from `contracts.Production` (M46).
- **M46 — `contracts.Production`**, provided by Manufacturing (optional for Orders):
  ```go
  ForOrderLines(ctx, lineIDs []int64) ([]LineProduction, error)
  // LineProduction: LineID, OrderID (MO), Number, Status, Quantity, Produced,
  // PlannedFinish, DueDate
  HasBOM(ctx, variantIDs []int32) (map[int32]bool, error)
  ```
  Orders calls it on the pool for its order page, and only when the caller holds
  `manufacturing:access`. Orders passes the caller's permissions, so the contract
  answers nothing to a caller without it.
- **M47 — Child MOs.** On a confirmed MO, **Create sub-assembly orders** lists each
  component line whose variant has a default active BoM and whose missing quantity
  (M21) is more than 0. It creates one confirmed child MO per chosen line, with:
  - origin `parent`;
  - the missing quantity;
  - `dueDate` as M37.

  The parent shows its children, and each child links to its parent. The action is
  never automatic, so make-to-stock shops keep control. A child's output is not reserved
  to the parent automatically; the parent's **Reserve now** picks it up once it lands.
- **M48 — Done goods and the sales order.** When an MO with origin `sales_order` closes,
  Manufacturing publishes `manufacturing.order.done` with the MO id, the origin line
  and the produced quantity. Orders handles it (`orders.mto-reserve`) by trying to
  reserve the line's open, unreserved quantity at the order's site. If that fails it
  does nothing more: the line already shows the shortage.

## 8. Permissions

| Key | Display (en / nb) | Gives |
| --- | --- | --- |
| `manufacturing:access` | Use Manufacturing / Bruke Produksjon | The app: reading work centers, BoMs, MOs, the shop floor and the schedule |
| `manufacturing:boms-manage` | Manage bills of materials and work centers / Administrere stykklister og arbeidsstasjoner | Creating, editing and archiving BoMs and work centers, closures |
| `manufacturing:orders-manage` | Manage manufacturing orders / Administrere produksjonsordrer | Creating, confirming, editing, cancelling and closing MOs, reservations, planning, rescheduling, correcting time logs |
| `manufacturing:shop-floor` | Work on the shop floor / Arbeide i verkstedet | Starting, pausing and finishing work orders, registering production and scrap, blocking a work center |
| `manufacturing:costs-view` (sensitive) | See manufacturing costs / Se produksjonskostnader | Every cost figure: overview, estimate, actual, reports |

Every operation also requires `manufacturing:access`. Stock figures shown in
Manufacturing are Manufacturing's own reads of `contracts.Stock`, so they need no
Inventory permission. Following the Orders precedent, the screen shows only quantities.

## 9. Data model (schema `manufacturing`)

```
counters(name pk, next)
work_centers(id, code uq, name, site_id, capacity, time_efficiency, setup_minutes,
  cleanup_minutes, machine_cost_per_hour, labour_cost_per_hour, pattern jsonb,
  working_state, status, revision, created_at, updated_at)
work_center_closures(id, work_center_id, from_date, to_date, note)
boms(id, variant_id, code uq-partial, output_quantity, is_default, make_to_order,
  lead_time_days, note, status, revision, …)  + partial unique (variant_id) where is_default and active
bom_operations(id, bom_id, sequence, name, work_center_id, setup_minutes, minutes_per_cycle, instructions)
bom_lines(id, bom_id, variant_id, quantity, scrap_percent, operation_id null, sort_order, note)
orders(id, number uq, variant_id, bom_id, quantity, produced, site_id, component_location_id,
  output_location_id, due_date, planned_start, planned_finish, priority, responsible_user_id,
  note, origin_kind, origin_order_id, origin_line_id, origin_reference, parent_order_id,
  backorder_of_id, status, confirmed_at, closed_at, estimate jsonb, actual jsonb,
  origin_cancelled, revision, …)  + unique (origin_line_id) where origin_kind='sales_order' and status<>'cancelled'
order_components(id, order_id, variant_id, sku, product_name, unit, required, consumed,
  scrapped, operation_id null, standard_cost, sort_order)
work_orders(id, order_id, sequence, name, work_center_id, setup_minutes, cleanup_minutes,
  minutes_per_cycle, capacity, time_efficiency, machine_rate, labour_rate, instructions,
  expected_minutes, planned_start, planned_finish, status, started_at, finished_at, revision)
work_order_slots(work_order_id, work_center_id, starts_at, ends_at)   -- the planned pieces (M35)
time_logs(id, work_order_id null, work_center_id, user_id null, kind, reason, note,
  started_at, ended_at, quantity, forced, corrected_by, corrected_at)
  + unique (user_id) where ended_at is null
productions(id, order_id, client_request_id uq, quantity, output_scrap, to_location_id,
  movement_id, posted_by_user_id, posted_by_display, posted_at)
production_lines(production_id, component_id, consumed, scrapped, from_location_id)
order_events(id, order_id, kind, payload jsonb, actor_user_id, actor_display, at)
```

Each delivery's migration adds only its own tables:

- 0: none in this schema. The `production` kind is an application constant.
- A: the baseline, with counters, work centers, closures and BoMs.
- B: orders, components, productions and events.
- C: work orders and time logs.
- D: slots.
- E: the origin columns' index and `origin_cancelled`.

Exact numbers are claimed at each branch cut (the plan).

**Erasure (rule 19).** User ids on time logs, productions and events stay, with the
display snapshot. This is the Inventory ledger precedent, decided for the poster. The
module registers no `PersonalData` slot in phase 1, and the reference page says so.

## 10. Platform, registries and documentation

- The OpenAPI contract is `openapi/manufacturing.yaml`. Every operation has a `summary`.
  Writes are `confirm` for the Assistant, apart from the shop-floor actions, which are
  `never`: an agent does not clock in.
- Live topics are declared through `Module.LiveTopics`. Event handlers are
  `manufacturing.mto` (E), and `orders.mto-reserve` on the Orders side (E).
- **Documentation**, in both `en` and `nb` where the section is bilingual:
  - `user/manufacturing.md`, with the tasks: work centers, BoMs, MOs, the shop floor,
    the schedule and costs;
  - `admin/manufacturing.md`, covering enabling the module, its requirements, the
    permissions and the tablets on the floor;
  - `reference/manufacturing.md` (English only), with every rule, code and permission;
  - updates to `reference/inventory.md` (the production kind, `Produce`, the production
    reserve), `reference/products.md` (the standard cost on the contract) and
    `reference/orders.md` with `user/orders.md` (E);
  - `contributing/module-boundaries.md`: the new contracts.

  Then `go generate ./...`.
- **ROADMAP.md:** a *Manufacturing* section with phase 1 marked as each delivery lands,
  and the corrected Later list.

## 11. Edge cases

- **Quantities** are exact decimal text with at most three decimals, end to end. They
  are never floats; the frontend uses the decimal helpers that Inventory's line editor
  uses.
- **Units:** a BoM line is in the component's own unit. Units cannot be converted until
  Products phase 5.
- **A component that is also the output** (rework): refused by M14.
- **A variant discontinued after its BoM was made:** the BoM stays. A new MO warns but
  is allowed.
- **A site archived with open MOs:** Inventory refuses movements on archived locations.
  The production then fails with Inventory's refusal, which is shown as is.
- **Inventory switched off after MOs exist:** the configuration refuses it, because
  Manufacturing requires Inventory. Release still works, because `StockLedger` is
  composed whatever `MODULES` says.
- **Two productions at once on one MO:** the MO row is locked `FOR UPDATE` after
  `LockVariants`, and component lines are updated under it.
- **A clock change:** time logs are UTC instants. Patterns are local times. Plans are
  computed in the workspace zone, day by day.

## 12. Testing

- **Server:** each rule M*n* with a status code has a test.
- **Ledger:**
  - `Produce` idempotency, refusal with no partial write, reservation consumption, the
    one-site rule;
  - `ReserveForProduction` at a site that does not fulfil orders;
  - Orders' `Reserve` there still refused.
- **BoMs:** the cycle across three levels; the depth limit; the default swap; the
  overview's numbers on a two-level fixture.
- **MOs:** required with scrap rounding; confirm with a short line; production
  proportional prefill; the deviation warning; backorder numbering; cancel after
  production refused; concurrency of two productions (pool of four).
- **Time:** one open log per user across centers; block converting logs; correction
  limits.
- **Planning:** first free slot across a lunch gap, a holiday and a closure; predecessor
  ordering; two concurrent plans not double-booking; `no_capacity`.
- **Cost:** estimate snapshot; actual with two operators in parallel (labour counts both,
  machine counts the union).
- **E:** the handler idempotent on redelivery; a cancelled origin flagged; reserving
  when the order is done.
- **Frontend:** the shop floor's work order view, with timers from `startedAt`, the
  buttons by status and permission; the production dialog's prefill and warning; the
  board's drag request; the BoM editor's client keys.
- **Integration:** `everyModule()` includes manufacturing.

## 13. Later, and what each waits for

| Item | Waits for |
| --- | --- |
| Lots and serials, the trace screens | Phase 2 delivery: Products tracking mode, Inventory lots and the balance key (ROADMAP Manufacturing rules) |
| Quality checks on operations | Phase 2 |
| Operation dependencies (parallel operations) | Phase 2 |
| Alternative work centers, a drag to another center, overlap | Phase 2 |
| Auto-computed standard times | Phase 2, reading M42's deviation |
| Reorder rules creating MOs | Phase 2, using Inventory placement's minimum |
| By-products, kits, unbuild, split and merge | Phase 2 |
| Subcontracted operations | Purchasing |
| A shared tablet with operator PIN | An identity feature (a second credential) |
| Rotating shift patterns | Demand |
| Writing the rolled-up cost to the standard cost | Products (a write contract) |
| Feeding work-order time into Time | Time without a project, or a project per MO |
| OEE, maintenance, MPS, BoM change control | Phase 3 |
| WIP valuation | A ledger |

## 14. Open questions

All readings were defaulted (R §9). The ones whose overturn would cost the most:

- R §9 reading 2 (requiring Inventory);
- R §9 reading 5 (lots in phase 2).
