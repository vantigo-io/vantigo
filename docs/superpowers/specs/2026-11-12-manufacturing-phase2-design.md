# Manufacturing phase 2 — lots, traceability, quality, replenishment, routing, BoM depth, stations

Revision 2, 2026-11-12: revised after a critic review of revision 1 the same day; §23
lists every change with the finding it answers. This spec builds on the research
`docs/superpowers/research/2026-11-12-manufacturing-phase2.md` ("R"), whose deliveries
(§6) and default readings (§9) it does not reopen. It extends the phase 1 design
`docs/superpowers/specs/2026-11-11-manufacturing-design.md` ("D1"): decisions continue
D1's numbering from **M49**, and a phase 1 rule this spec does not name stands — D1's §15
amendments included. It also extends Inventory's designs (phase 1 "I", phase 2 I35–I65).
The implementation plan is `docs/superpowers/plans/2026-11-12-manufacturing-phase2-plan.md`.

Code references are against `claude/manufacturing-p2-plan` at `c3b9a5a7b` (latest
migration `00161`). Abbreviations:

- `srv/` is `apps/server/internal/`; `mig/` is `srv/db/migrations/`;
- `inv/`, `mfg/`, `ord/`, `prd/` and `host/` are the frontends under
  `apps/<module>/frontend/src/`.

## 1. Purpose

Phase 2 makes production **traceable** and **right**: every tracked item carries a lot
from its receipt or production to its delivery, and a recall can be answered in minutes;
the work itself is checked at the station; the shop decides what to make from what is
running low; routings branch; BoMs give by-products and can be reversed; and one tablet
serves a whole cell.

| Delivery | What it brings | Owner of the records | Depends on |
| --- | --- | --- | --- |
| **L1a** | Tracking mode (Products, behind a guard until M); lots and their holds, the lot-aware ledger and engine, expiry, FEFO, the lot-aware contracts; receipts, moves and adjustments with lots in the API; the callers pass no lot | Products, Inventory | — |
| **L1b** | The lot screens: lots list and page, holds, attachments and their deletion worker, the lot column on the forms, stock per lot, the host's stock panel, lookup, Inventory's live topics | Inventory, host | L1a |
| **L2** | Counts, transfers and the opening import with lots; the expiring list; lot labels | Inventory | L1b |
| **O** | Deliveries pick lots, FEFO-proposed; lots on the pick list and packing slip | Orders | L1a |
| **M** | The output lot of a production, serials, consumed lots per component; the tracking guard removed | Manufacturing, Products | L1a, O |
| **T** | The trace screen, export, blocking with the downstream list, GS1-128 labels | Inventory (screen), Orders (a read) | L1b, O, M |
| **Q** | Quality checks on operations, results, deviations, quarantine; inspection on receipt | Manufacturing, Inventory | M |
| **R** | Replenishment: reorder rules, projected stock, suggestions, MOs from them | Manufacturing, Inventory (a read) | — |
| **F** | Operation dependencies, send-ahead, work center groups | Manufacturing | — |
| **G** | By-products, unbuild, split and merge | Manufacturing | M |
| **S** | Shared stations: devices, operator PIN and badge, the station scope | Identity, Manufacturing | — |
| **K** | Kits, optional: a kit line's components as child order lines | Manufacturing, Orders | O |

**The interim states.** The stack lands in this order, and each layer is merged on its
own, so a state with L1a but without O and M must not let a tracked variant exist: the
deliveries and productions would refuse it `lot_required`. L1a therefore ships a
**server-side guard** in Products' variant write that refuses `tracking` other than
`none` (`tracking_unavailable`, 409), and the *Tracking* select is not shown; the
ledger's tests create tracked variants through the store. Layer **M** removes the guard
and shows the select (M49).

**Not in this phase** (R §7): subcontracting, MPS, random or periodic sampling, packages
and SSCC, per-bin lot rules, ZPL, recall campaign objects, parallelising across a group,
auto-computed standards; kits if K does not fit.

### 1.1 Who owns what

- **Records of stock and lots are Inventory's**: lots, their dates, holds and files, the
  lot on every balance and movement line, the trace screen (R §9 reading 6) and lot
  labels.
- **Capture is the document's**: Orders chooses the lots a delivery takes; Manufacturing
  chooses the lots a production consumes and names the lot it produces. Both pass the lot
  to Inventory through `StockLedger` on their own transaction (rule 24).
- **The tracking mode is Products'**, changed only when Inventory's holder says the
  variant holds nothing (rule 20).
- **Quality and suggestions are Manufacturing's**; quarantining a lot is a hold Manufacturing
  puts through `StockLedger` on its own transaction, and releases only its own (M64).
- **Stations and PINs are Identity's**; which work centers a station serves is
  Manufacturing's.

### 1.2 The rules this spec keeps

- **Rule 24** (Inventory on the caller's transaction): every new ledger call — `EnsureLots`,
  `HoldLots`, `ReleaseLotHolds` — runs on the caller's `tx`, after `LockVariants` and the
  caller's rows, refuses only through `*StockRefusal`, and is composed whatever `MODULES`
  says. **No ledger call makes a contract call of its own** inside the caller's
  transaction: what it needs from outside Inventory — today's date in the workspace zone,
  whether the principal may use an expired lot — the caller reads on the pool before its
  transaction and passes as data (M62). Inventory's `noteContractCall` harness would
  refuse such a call inside Inventory's own transactions; inside a caller's it would hold
  a pooled connection while waiting for another.
- **Rule 12** (the provider is the gatekeeper): `contracts.DeliveryTrace` (T) is
  provider-gated on `orders:access` and refuses (`ErrForbidden`) a principal without it;
  Inventory shows customer names only to a caller who holds `customers:view`.
  `contracts.KitStructure` (K) and `SalesOrderLines.Unreserved` (R) carry no personal
  data and are consumer-gated.
- **Rule 23** (handlers write their own schema): phase 2 adds no event handler that
  reads a contract. The replenishment job (R) is a lease worker, not a handler.
- **Rule 20** (holders inside Products' transaction): the tracking change is a third
  holder purpose.
- **Phase 1's schedule locks** (D1 M35a): every write that changes a work order's planned
  slots — F's drags and plans, G's split and merge — takes the advisory locks of every
  work center it touches through `withScheduledOrders` (`srv/manufacturing/orders.go`),
  before any row lock (M115, M119, M127).
- **Rule 21** (one locking read): `LockVariants` stays the only locking contract read; the
  new ledger calls lock Inventory's rows inside Inventory.

## 2. Decisions — L1a and L1b, lots in Products and Inventory

L1a is the schema, the engine, the contracts and the receipt, move and adjustment API;
L1b is everything a person sees of lots. Each decision below names its layer where it is
not L1a.

### Products

- **M49 — The tracking mode.** `products.product_variants` gains:
  - `tracking text NOT NULL DEFAULT 'none' CHECK (tracking IN ('none','lot','serial'))`;
  - `expiry_days integer NULL CHECK (expiry_days BETWEEN 1 AND 3650)`;
  - `best_before_days integer NULL CHECK (best_before_days BETWEEN 1 AND 3650)`.

  The variant request and response gain `tracking`, `expiryDays` and `bestBeforeDays`.
  Only a **goods** product's variant may be tracked (`tracking_requires_goods`, 400). A
  product's change to Service sets every variant's tracking to `none` in the same
  transaction: the holders have already said it holds no stock.
  - **Until layer M** the variant write refuses `tracking` other than `none`
    (`tracking_unavailable`, 409; §1's interim states) and the variant form does not show
    the select. Layer M deletes the guard and its test, and adds the select.
- **M50 — `VariantEntry` carries it.** `contracts.VariantEntry` gains:
  ```go
  // Tracking is "none", "lot" or "serial" (manufacturing phase 2 M49).
  Tracking string
  // ExpiryDays and BestBeforeDays are the days a new lot's expiry and best-before
  // date lie after its production or receipt date, nil when unset.
  ExpiryDays, BestBeforeDays *int32
  ```
  Products fills them in `Variant`, `Variants`, `VariantsOfProduct`, `LookupCode`,
  `LookupSKUs`, `SearchVariants` and `LockVariants`.
- **M51 — Changing the tracking mode is held.** A variant `PUT`
  (`srv/products/variants.go`:175) whose `tracking` differs from the stored one asks every
  holder, inside its transaction and after the variant's `FOR UPDATE`, with a new purpose:
  ```go
  // VariantHoldTrackingChange is a change of the variant's tracking mode
  // (manufacturing phase 2 M51): any stock, reservation or stock in transit holds it.
  VariantHoldTrackingChange VariantHoldPurpose = "tracking_change"
  ```
  - Inventory's holder already ignores the purpose (`srv/inventory/holder.go`:36), so
    stock, reservations and stock in transit hold the change. The doc comment names the
    third purpose.
  - Manufacturing's and Orders' holders answer nil for it (they answer only `delete`, and
    `type_change` for Manufacturing): open documents never stop a tracking change.
  - The refusal is 409 `tracking_held`, with the existing `holdText` ("34 pcs in stock at
    3 locations") and remedy.
  - Changing `expiryDays` or `bestBeforeDays` is never held; it applies to lots created
    afterwards.
  - Reading 4: there is no guided conversion. The opening import (M72) is the path for
    existing stock.

### Inventory: the lot

- **M52 — A lot** (`inventory.lots`; nb *parti*) is a numbered quantity of one variant:
  - `variant_id integer NOT NULL`;
  - `number varchar(20) NOT NULL`, 1–20 characters from `[A-Za-z0-9._/-]`, stored as
    typed, **unique per variant ignoring case** (`ux_lots_variant_number ON (variant_id,
    lower(number))`). Twenty characters is GS1's limit for AI (10) and (21), so every lot
    prints (M93);
  - `serial boolean NOT NULL`: the variant's tracking was `serial` when the lot was made
    (R reading 1). It is **history**: a lot keeps it when the variant's tracking later
    changes, a new lot follows the variant, and the serial rule (M53) follows the
    variant's tracking now;
  - `supplier_lot varchar(40) NULL`: the supplier's lot or the heat number of a 3.1
    certificate;
  - `production_date date NULL`, `best_before date NULL`, `expiry date NULL`;
  - `received_at timestamptz NOT NULL`: when stock first entered under the lot, the FIFO
    key;
  - `origin_module`, `origin_kind`, `origin_id` (text, NULL): the document that created
    it — a receipt movement, a production, an adjustment, an import;
  - `status text NOT NULL DEFAULT 'available' CHECK (status IN
    ('available','quarantine','blocked'))`: **derived from the lot's active holds** (M64)
    and rewritten in the same transaction as every hold change, under the lot's row lock.
    It is stored only so the lists filter and index it; no write sets it directly (R
    reading 2, as revised);
  - `note varchar(2000) NULL`;
  - `sku`, `product_name` snapshots; `revision integer`; `created_by_user_id`,
    `created_by_display`, `created_at`, `updated_at`.

  Indexes: `(variant_id, expiry NULLS LAST, received_at, id)` (FEFO), `(status) WHERE
  status <> 'available'`, `(expiry) WHERE expiry IS NOT NULL`, `(lower(number))` (lookup).
  A lot is never deleted.
- **M53 — The serial rule.** A lot of a variant whose tracking is `serial` holds **at most
  1** in total over every location (`serial_quantity`, a 400 line failure). A serial
  line's quantity is exactly 1; a line of quantity 3 for a serial variant is refused, and
  the receipt form generates three lines instead (M57).
  - **Judged under the lot's lock.** Two postings can raise one serial lot at two
    locations at once, and each balance row alone stays at 1. So any line that
    **increases** a lot of a serial variant takes the lot's row `FOR NO KEY UPDATE` at
    step 6 of the lock order (M58), and the engine sums **all** the lot's balances, every
    location, after its own balances are locked and before it writes them. The second
    posting waits for the lot and then sees the first's 1. Decreases need no lot lock: they
    can only lower the sum.
- **M54 — Attachments** (L1b; `inventory.lot_attachments`): `id`, `lot_id`, `object_key unique`,
  `file_name`, `content_type`, `size_bytes`, `uploaded_by_user_id`,
  `uploaded_by_display`, `created_at`.
  - Through `srv/storage` under the scope `inventory-lots`, built as Customers builds its
    store (`srv/customers/objects.go`), `Deps.ObjectStore` first.
  - PDF, JPEG or PNG; at most **20 MB** each and **20** per lot (`attachment_too_large`,
    `too_many_attachments`, 400; `attachment_type`, 415).
  - Deleting an attachment queues its key in `inventory.object_deletions` (Customers'
    ledger shape, `mig/00044`), drained by a lease worker `inventory.object-deletions`.
  - Storage unconfigured answers 503 `storage_not_configured`, as Customers does.
  - Downloads stream through `GET /lots/{id}/attachments/{attachmentId}`; no path is ever
    returned.

### Inventory: the ledger

- **M55 — The balance key.** Migration `00163` (§14):
  - `balances` gains `lot_id bigint NULL REFERENCES inventory.lots`. Its primary key is
    dropped and replaced by `ux_balances_key ON (variant_id, location_id, lot_id) NULLS
    NOT DISTINCT`, plus `ix_balances_lot (lot_id) WHERE lot_id IS NOT NULL`.
  - `movement_lines` gains `lot_id bigint NULL REFERENCES inventory.lots`, with
    `ix_movement_lines_lot (lot_id, movement_id) WHERE lot_id IS NOT NULL`.
  - `LockBalances` becomes `ON CONFLICT (variant_id, location_id, lot_id) DO UPDATE`, which
    PostgreSQL infers from the `NULLS NOT DISTINCT` index; a test proves two postings to a
    new (variant, location, no lot) key still serialise on one row.
  - `SetBalances`, `DeleteZeroBalances` and `InsertMovementLines` take a `lot_ids bigint[]`
    unnested beside the others, `0` for none (`nullif(…, 0)`), and match with `IS NOT
    DISTINCT FROM`.
  - In Go, `balanceKey` gains `lotID int64` (0 for none); `balanceKeys` sorts by (variant,
    location, lot); `keyColumns` answers three arrays; `engineLine` gains `lotID int64` and
    `newLot *newLot` (M57). The comment on `balanceKey` that *a lot joins it later* is
    replaced.
- **M56 — The engine's lot rules**, judged in `post` after `LockVariants` (which now
  answers `Tracking`) and after the lots are locked:

  | Case | Code (line failure) |
  | --- | --- |
  | a tracked variant's line names no lot | `lot_required` (400) |
  | an untracked variant's line names a lot | `lot_not_allowed` (400) |
  | the lot is unknown or belongs to another variant | `lot_unknown` (400) |
  | a serial line's quantity is not 1, or a serial would hold more than 1 | `serial_quantity` (400) |
  | an issue or consume line takes from an expired lot without the override (M62) | `lot_expired` (409) |
  | an issue or consume line takes from a lot with an active hold (`quarantine` or `blocked`) | `lot_blocked` (409) |

  - **Moves, adjustments, counts and transfers ignore holds and expiry**: stock on hold
    can be moved to a hold shelf, counted, written off or sent back. Only `issue` and the
    consume lines of a `production` are refused, and an unbuild's consume lines are not
    (M123). A receipt into a blocked lot is refused too (`lot_blocked`): goods do not join
    a recalled lot.
  - A line of a variant whose tracking changed while it was drafted is judged as the
    variant is now.
- **M57 — New lots are created by the posting.** A receipt or an adjustment up may name a
  new lot instead of a lot id:
  ```
  lot: {number?, supplierLot?, productionDate?, bestBefore?, expiry?, note?}
  ```
  - **The number is optional**: when empty, Inventory generates `L<n>` from
    `inventory.counters` (`lot`, starting at 1001), allocated in the posting transaction.
  - **A number that already exists for the variant** names that lot: receiving more under
    a supplier's lot number adds to it. Its dates are not changed. Only a date the body
    **states** is compared: a stated date that differs from the stored one is refused
    (`lot_dates_differ`, 400) so a typo is seen; a date the body leaves out is not
    compared, so an ordinary repeat receipt under the same number never trips it.
  - **Default dates apply only when the lot is created:** `expiry` = the production date
    when given, else the receipt's date (in the workspace zone, the `Today` of M62) + the
    variant's `ExpiryDays`; `bestBefore` likewise with `BestBeforeDays`; each only when
    the variant has the offset and the body gives no date. A default is never computed
    for, or compared against, a lot that exists.
  - **Serials:** a receipt line may carry `serials: {first, count}` (count 1–500) or
    `serials: {numbers: [...]}`. The server expands it into one engine line per serial of
    quantity 1, numbered by incrementing the trailing digits of `first` and keeping their
    width (`SN0099` → `SN0100`); a `first` with no trailing digits is
    `serial_first_invalid` (400). The expansion counts against the 500-line cap.
  - The lots are inserted at step **5** of the lock order (M58), each with `INSERT … ON
    CONFLICT (variant_id, lower(number)) DO NOTHING RETURNING id`, then read back on a
    conflict. A new serial whose number exists and holds stock is `serial_quantity`.
- **M58 — The lock order**, Inventory's posting order (`posting.go`'s comment) extended;
  the comment is rewritten with it. **The lots come before the header and the
  locations, for every writer**: Inventory's own receipts and adjustments and a caller's
  `EnsureLots` alike, so no posting holds a location and then waits for a lot.
  1. the `clientRequestId` read (unchanged);
  2. `ProductCatalog.LockVariants` (unchanged; a count's or transfer's own row first, as
     before);
  3. **the lot counter**: the `inventory.counters` row `lot`, taken by its upsert, only
     when a number is to be generated (`L<n>`). It serialises the postings that generate
     a number, workspace-wide, for the rest of their transaction — a few milliseconds for
     an SME's receipts, and named here so no writer takes it after a lot;
  4. **new lots** inserted, in (variant id, lower(number)) order (M57);
  5. **every lot the posting names**, in id order: `FOR SHARE`, or `FOR NO KEY UPDATE` for a
     lot that a line increases and whose variant is serial (M53). A hold change takes the
     lot `FOR NO KEY UPDATE` (M64), so it waits for the posting or is waited for;
  6. the movement's header claimed (was step 3);
  7. the locations `FOR SHARE`, in id order (unchanged);
  8. the balances, created at zero and locked, in **(variant, location, lot)** order, the
     lot `NULLS FIRST`; `LockSiteBalances` (the reserve's lock) orders the same way.

  A caller's `EnsureLots` runs steps 3–5 and its `Produce` runs 2 and 5–8 (it already holds
  the lots, so step 5 waits for nothing). `StockLedger`'s doc comment states the extended
  order. Callers outside Inventory never lock lots: they hold `LockVariants`, then their
  own rows, then call the ledger, as rule 24 says. A deadlock the order does not foresee
  (40P01) is retried once by Manufacturing's production write (M86); Inventory's own
  writes answer 409 `revision_conflict` as today.
- **M59 — Receipts, moves and adjustments.**
  - A **receipt** line takes `lotId` or `lot` (M57), or `serials`.
  - A **move** line takes `lotId`: the lot leaves `from` and lands on `to` unchanged.
  - An **adjustment** line takes `lotId`, or `lot` for an increase. A set-mode line sets
    one (location, lot).
  - Each handler reads `Today` (`contracts.BusinessCalendar`) on the pool before its
    transaction (M62); none of them needs the override.
  - The API is L1a; the forms are L1b. The forms (`inv/pages/new-receipt.tsx`, `new-move.tsx`, `new-adjustment.tsx` and the
    shared `LineEditor`) show a **Lot** column for tracked variants only: a combobox of the
    variant's lots at the site (from M63's read), *New lot…* on a receipt and an increase,
    and *Serial numbers…* on a receipt of a serial variant.
  - The movement page and list show the lot on each line, linked to the lot page.

### Inventory: screens and reads

- **M60 — The lots list** (L1b; `/inventory/lots`, nb *Partier*), in the navigation after
  *Stock*: number, SKU and product, supplier lot, expiry, best before, status, on hand in
  total. Filters: `q` (lot number, supplier lot or SKU), `variantId`, `siteId`, `status`,
  `holdCause` (M64), `expiresBefore`, `withStock` (default true). Cursor-paginated by
  (variant, number).
- **M61 — The lot page** (L1b; `/inventory/lots/{id}`):
  - the header: number, product, supplier lot, the three dates, status, the origin linked
    to its document, note;
  - **Holds**: every hold, active ones first, each with its kind, cause, note, source
    (linked: a deviation, a receipt, the lot page itself), who put it and who released it;
  - **Where it lies**: on hand per site and location;
  - **Movements**: every movement line of the lot, newest first, linked;
  - **Files**: the attachments, with upload and delete (`inventory:lots-manage`);
  - **Edit** (If-Match): supplier lot, the three dates and the note. The number and the
    variant never change;
  - **Quarantine**, **Block** and, on each of Inventory's own active holds, **Release**
    (M64);
  - from T, **Trace** and **Labels**.
- **M62 — Expiry.** A lot is **expired** when `expiry < Today`, where `Today` is the
  workspace zone's date. Best-before alone never refuses anything (food past its
  best-before may still be sold with a warning); it is shown.
  - **The caller decides, before its transaction** (rule 24). `StockIssue`,
    `StockProduction` and `EnsureLots` carry `Today` (a date) and `ExpiredOverride bool`.
    The caller reads `Today` from `contracts.BusinessCalendar`, and `ExpiredOverride` as
    `contracts.HasPermission(ctx, "inventory:expired-override")`, both **on the pool
    before** it opens its transaction; a worker with no request passes `false`. The ledger
    makes no contract call (the earlier text had Inventory check the permission inside the
    caller's transaction, which breaks rule 24).
  - An issue or consume line from an expired lot is refused `lot_expired` unless the line
    carries `AllowExpired` (the person chose that lot) **and** the call's
    `ExpiredOverride` is true. Inventory trusts the flag as it trusts `PostedByUserID`: the
    caller is a module, and its contract tests prove it reads the permission.
  - Inventory's own handlers read the zone and the permission the same way, on the pool
    before their transactions.
  - A zero `Today` is a programming error (`contracts: a stock call without today`), not a
    refusal.
  - The movement records the override: `movement_lines.expired_override boolean NOT NULL
    DEFAULT false`.
- **M63 — Stock per lot, and usable on hand.**
  - **Usable on hand** at a site is the balances whose lot is none, plus the balances of
    lots that have no active hold and are not expired on `Today`. It is what can be
    promised. **On hand** stays everything that lies there.
  - **Usable is what promises read** (L1a): `Reserve` and `ReserveForProduction` judge
    available as usable on hand less reserved (their `SiteStockOf` read, `ledger.go`,
    sums usable); `Stock.Available` answers usable less reserved, `Stock.Placements`
    (M106) answers usable as its `OnHand`, and the low-stock list (I56) reads the same
    figure, so the three agree.
    `Stock.OnHand` and the stock pages' on hand do not change. `Reserve` and
    `ReserveForProduction` gain a `today` argument after `siteID`, which the callers read
    on the pool as M62; the pool reads read the zone themselves.
  - A hold put on stock already reserved lowers available, which may go negative: the
    reservations stay (reading 3), and the delivery's or production's proposal skips the
    held lot (§19).
  - **The screens** (L1b): `InventorySiteStock` rows (the product and variant stock reads)
    gain `held` (on hand in lots on hold or expired) and `lots: [{lotId, number, expiry,
    bestBefore, status, onHand}]` per location, in FEFO order (M65). Every stock table
    shows *of which on hold or expired* beside on hand when it is not zero.
  - The product page's stock panel (`host/routes/products/$productId.tsx`) shows a lot table
    under each site for a tracked variant, with expired and on-hold lots marked.
  - The variant page (`inv/pages/variant.tsx`) shows the same and links each lot.
- **M64 — Holds, and the status they give.** A single status column let one document undo
  another's: closing a deviation set *available* on a lot a recall had blocked. A lot's
  restrictions are therefore **holds**, each owned by the document that put it:
  ```
  inventory.lot_holds: id bigserial, lot_id (references lots), kind text CHECK (kind IN
  ('quarantine','block')), cause text CHECK (cause IN ('inspection','deviation',
  'complaint','recall','other')), note varchar(1000) null, source_module, source_kind,
  source_id (the StockSource that put it: inventory/lot/<lot id> for the lot page,
  inventory/movement/<id> for a receipt's inspection, manufacturing/deviation/<id>,
  manufacturing/unbuild/<id>), created_by_user_id, created_by_display, created_at,
  released_at null, released_by_user_id, released_by_display, release_note varchar(1000) null
  index (lot_id) WHERE released_at IS NULL
  ```
  - **The status is derived**: `blocked` when any active hold is a `block`, else
    `quarantine` when any active hold is a `quarantine`, else `available`. Every hold
    change rewrites `lots.status` in its transaction.
  - **A release removes only its own hold.** Closing a deviation releases the holds whose
    source is that deviation; a recall's block stays until the recall's own hold is
    released. Two sources may hold one lot; the lot is free when neither does.
  - **Every hold change takes the lot `FOR NO KEY UPDATE`**, in lot id order, and writes an
    `inventory.events` row (`entity_kind = 'lot'`, kinds `lot_created`, `lot_updated`,
    `lot_held`, `lot_released`, `lot_attachment_added`, `lot_attachment_removed`), naming
    the source.
  - **The lot page's own holds** (L1b): `POST /lots/{id}/holds {kind, cause, note}` and
    `POST /lots/{id}/holds/{holdId}/release {note}`, both needing `inventory:lots-manage`.
    The page releases only holds whose source is Inventory (the lot page's, a receipt's
    inspection); a hold of another module's document is shown with its source linked and
    is released there (`hold_not_inventorys`, 409). The block of M92 is such a hold.
  - **Another module's holds** go through `StockLedger.HoldLots` and `ReleaseLotHolds`
    (M67), on its own transaction.
- **M65 — FEFO and FIFO.** One ordering everywhere a lot is proposed or listed:
  `expiry ASC NULLS LAST, received_at ASC, id ASC` (R reading 5: with no expiry on any
  lot, it is FIFO by received date). The proposal skips lots that are not `available`
  (any active hold) and lots that are expired; it never splits a serial.
  ```go
  // ProposeLots is the FEFO proposal (manufacturing phase 2 M65): it takes
  // quantity from rows in their order, skipping rows not available or expired on
  // today, and answers the picks and what it could not cover. rows are one
  // variant's at one site, as Stock.Lots answers them.
  func ProposeLots(rows []StockLot, quantity string, today time.Time) (picks []LotPick, short string, err error)
  type LotPick struct {
      LotID      int64
      LocationID int64
      Quantity   string
  }
  ```
  It is a pure function in `srv/contracts/stock.go`, beside `ValidateStockVariants`, so
  Orders and Manufacturing propose the same lots (rule 3 allows a pure helper).
- **M65a — Proposals that race, and *auto*.** A proposal read on the pool can be stale by
  the post: another delivery took the lot.
  - **`AutoLot`** on an issue or consume line lets Inventory pick the lots itself, FEFO at
    the line's `FromLocationID`, **under its own locks**: it reads the candidate lots of
    (variant, location) on the transaction, locks them at step 5 (M58) and their balances
    at step 8, then takes the quantity in M65's order, skipping held and expired lots
    (and expired ones even with `AllowExpired`: auto never uses an expired lot). A lot
    that appeared after the candidate read is not used. The picks come back in
    `StockPosted.Picks`. Manufacturing uses it for a tracked component the request sends
    without rows (M82); a person's explicit choice is never auto.
  - **Re-propose once.** A caller whose post of **proposed** (not person-chosen) picks is
    refused `insufficient_stock` or `lot_blocked` on those picks re-reads `Stock.Lots`,
    proposes again and posts once more, outside the first transaction. A second refusal
    is answered to the person.
- **M65b — Reserving against many serials.** A serial variant has one balance row per
  serial, and `LockSiteBalances` locks every one of a variant's rows at the site on each
  reserve. L1a's tests include a reserve against **10,000 serial balances** at one site;
  if its lock takes more than 50 ms on the CI database, the reserve takes a transaction
  advisory lock per (variant, site) instead of the rows, and the engine takes the same
  lock for any posting that lowers that variant's stock at that site, so a reserve and an
  issue still serialise. The reference page records which was built.
- **M66 — Lookup** (L1b). The lookup (`srv/inventory/lookup.go`) matches, after the location and
  transfer tokens and before the catalog: a **`LOT<id>` token** (L2's labels), then a lot
  **number** equal to the text ignoring case (every variant's), as `kind: lot`. A serial
  number that is also an SKU shows both.

### Contracts

- **M67 — Lot-aware contracts** (`srv/contracts/stock.go`):
  ```go
  // Stock gains:
  // Lots is, per variant, every lot holding more than 0 at an active location of
  // the site, with its location, in FEFO order (M65). A variant with none has no row.
  Lots(ctx context.Context, variantIDs []int32, siteID int64) ([]StockLot, error)

  type StockLot struct {
      VariantID  int32
      LotID      int64
      Number     string
      Serial     bool
      Expiry     *time.Time // a date, nil when none
      BestBefore *time.Time
      ReceivedAt time.Time
      LocationID int64
      FullCode   string
      Quantity   string     // exact decimal text, > 0
      Status     string     // "available" | "quarantine" | "blocked", derived (M64)
  }

  // StockIssue and StockProduction gain (M62; decided by the caller on the pool):
  Today           time.Time // the workspace zone's date; zero is a programming error
  ExpiredOverride bool      // the principal holds inventory:expired-override
  // StockProduction also gains (M123):
  Unbuild bool // the movement is an unbuild: kind "unbuild", consume lines may take held lots

  // IssueLine and ConsumeLine gain:
  LotID        int64 // 0 for none; required for a tracked variant unless AutoLot
  AutoLot      bool  // M65a: Inventory picks the lots FEFO at FromLocationID under its locks
  AllowExpired bool  // M62: honoured only when the call's ExpiredOverride is true

  // ProduceLine gains:
  LotID int64 // 0 for none; a lot made by EnsureLots, required for a tracked variant
  // A production's produce lines are keyed by (VariantID, LineID): a by-product's
  // LineID is its order_outputs id, which may equal a component's (M121).

  // Issue and Produce answer what they posted, with the lots AutoLot chose:
  Issue(ctx, tx, src, issue StockIssue) (StockPosted, error)
  Produce(ctx, tx, src, p StockProduction) (StockPosted, error)
  type StockPosted struct {
      MovementID int64
      Picks      []StockPick // one per lot an AutoLot line took, by line index
  }
  type StockPick struct {
      Index      int // the line's index in Lines or Consume
      LotID      int64
      LocationID int64
      Quantity   string
  }

  // Reserve and ReserveForProduction gain today (M63), after siteID:
  Reserve(ctx, tx, src, siteID int64, today time.Time, lines []StockLine) error

  // StockLedger gains (M80 uses it; Inventory's own receipts use the same code):
  // EnsureLots finds or creates each lot, inside tx, after the caller's rows and
  // before Produce, and answers the lot ids in the order given. A lot that exists
  // for the variant is answered as is: only the dates the NewLot states are
  // compared (StockLotDatesDiffer); nil dates are ignored for it. A new serial
  // whose number holds stock is StockSerialQuantity. After a refusal the caller
  // rolls back, as after Produce.
  EnsureLots(ctx context.Context, tx pgx.Tx, src StockSource, today time.Time, lots []NewLot) ([]int64, error)
  // HoldLots puts one hold per entry for src's document (Q's deviation), inside
  // tx, taking the lots FOR NO KEY UPDATE in id order. A lot that already holds
  // an active hold of src with the same kind is left as is.
  HoldLots(ctx context.Context, tx pgx.Tx, src StockSource, holds []LotHold) error
  // ReleaseLotHolds releases every active hold src's document put on the lots
  // named (on every lot when lotIDs is nil). Holds of any other source stay.
  ReleaseLotHolds(ctx context.Context, tx pgx.Tx, src StockSource, lotIDs []int64, by LotActor) error

  type NewLot struct {
      VariantID      int32
      Number         string     // "" to generate L<n>
      SupplierLot    string
      ProductionDate time.Time  // required: today for a production, unless the person gave one
      BestBefore     *time.Time // nil: production date + BestBeforeDays, on creation only
      Expiry         *time.Time // nil: production date + ExpiryDays, on creation only
  }
  type LotHold struct {
      LotID       int64
      Kind, Cause string // "quarantine" | "block"; M64's causes
      Note        string
      By          LotActor
  }
  type LotActor struct {
      UserID  *uuid.UUID
      Display string
  }
  ```
  New codes in the refusal block: `StockLotRequired = "lot_required"`,
  `StockLotUnknown = "lot_unknown"`, `StockLotNotAllowed = "lot_not_allowed"`,
  `StockSerialQuantity = "serial_quantity"`, `StockLotExpired = "lot_expired"`,
  `StockLotBlocked = "lot_blocked"`, `StockLotDatesDiffer = "lot_dates_differ"`.
  - **`StockLineRefusal` gains `Index int`, `LotID int64` and `LocationID int64`**, so a
    caller maps a refusal to the exact line and lot row when one document line became
    several ledger lines (O's picks, M's lot rows, serial expansion); `LineID` alone no
    longer names one line.
  - **Reservations stay per site, with no lot** (R reading 3): `Release` and
    `ReservationsFor` do not change; `Reserve` and `ReserveForProduction` gain only
    `today` (M63). Reserve keeps its one-variant-per-line rule (`ledger.go`: a line id
    names one variant), which K must respect (M145).
  - **The callers change in L1a** so that the stack compiles: Orders' `PostDelivery` and
    Manufacturing's `postProduction` pass `LotID: 0`, `Today` and `ExpiredOverride` read
    on the pool, and take `StockPosted.MovementID`; their reserve calls pass `today`.
    Phase 1's tests keep untracked fixtures; the guard (§1) keeps tracked variants out of
    the product until M.
  - The fakes (`srv/manufacturing/fakes_ledger_test.go`, Orders' tests, `modtest`) gain
    the three methods and the new shapes.
- **M68 — Permissions (L1a, L1b).**

  | Key | Display (en / nb) | Gives |
  | --- | --- | --- |
  | `inventory:lots-manage` | Manage lots / Administrere partier | Editing a lot, its files, putting and releasing Inventory's own holds (L1b) |
  | `inventory:expired-override` | Use expired lots / Bruke utgåtte partier | Issuing or consuming an expired lot, with the line's `allowExpired`; read by the caller on the pool (M62) (L1a) |

  Reading lots needs `locations-view` and `stock-view`, as every stock read. Both keys are
  delegable; neither is sensitive. `GET /meta` gains `canOverrideExpired` (L1a) and
  `canManageLots` (L1b).

## 3. Decisions — L2, the rest of Inventory

- **M69 — Counts with lots.** `count_lines` gains `lot_id bigint NULL`; its unique index
  becomes `(count_id, variant_id, location_id, lot_id) NULLS NOT DISTINCT`.
  - Opening a count writes one expected line per balance row, so per lot.
  - An added line of a tracked variant must name a lot, existing or new (`lot` as M57);
    the count post sets each (location, lot).
  - A count opened before the variant became tracked is posted as is only if every line of
    that variant has zero expected and zero counted; otherwise the post refuses the lines
    `lot_required` and the count must be recounted.
  - The count screen groups a tracked variant's lines under it, a row per lot.
- **M70 — Transfers with lots.** `transfer_lines` gains `lot_id bigint NULL`:
  - one line carries one lot, so `transfer_action_lines` and the line totals do not change
    (a variant may already appear on several lines, which is how two lots travel, I36);
  - a draft line may name a lot; **send** requires one for a tracked variant
    (`lot_required`) and proposes FEFO at the source location;
  - **receive** and **cancel** post the same lot: receive puts it on the destination,
    cancel back where it left;
  - in transit per lot is the line's `inTransit` of the line's lot;
  - the transfer note PDF prints the lot and expiry.
- **M71 — The opening import with lots** (`srv/inventory/import.go`, `inv/pages/import.tsx`):
  the file gains optional columns `lot`, `supplier_lot`, `production_date`, `best_before`,
  `expiry`.
  - A row of a tracked variant without `lot` is refused (`lot_required`, row failure); an
    untracked variant's row with one is `lot_not_allowed`.
  - A serial variant's row has quantity 1.
  - The rows create lots by number as M57, with origin `inventory/import/<import id>`.
  - This is reading 4's migration path: set the tracking at zero stock, then import the
    opening balance with lots.
- **M72 — The expiring list** (`/inventory/expiring`, nb *Utløper snart*): lots with stock
  whose expiry falls within `days` (default 30; 1–365) or has passed, by expiry, filtered
  by site and status; CSV export. The dashboard attention items gain `lotsExpiring` (the
  count within 14 days) and `lotsExpired` (with stock), in I62's shared shape.
- **M73 — Lot labels** (`GET /lots/labels.pdf?ids=…&layout=L7160|L7163`), as location
  labels (`labels.go`): the **`LOT<id>`** token as Code 128 across the top, the number
  large, the SKU and name, the expiry or best-before. At most 1000. Serials print one
  label each. These are internal labels; GS1 labels are T's (M93).
- **M74 — The lot in the movement's CSV** export and in `GET /stock` filters
  (`lotId`).

## 4. Decisions — O, Orders deliveries with lots

- **M75 — Picks per lot.** A new child table `orders.delivery_line_lots`:
  ```
  id, delivery_line_id (cascade), lot_id bigint, lot_number varchar(20),
  expiry date null, location_id bigint, location_code varchar(100),
  quantity numeric(12,3) > 0, allow_expired boolean default false,
  chosen boolean default false
  unique (delivery_line_id, lot_id, location_id)
  ```
  `chosen` is set when a person edits the line's picks (M77); a line whose picks are all
  proposed may be proposed again at post (M65a).
  - A **tracked** variant's delivery line holds its quantity in these rows; its own
    `from_location_id` stays null. An untracked line is unchanged.
  - The sum of a line's rows must equal the line's quantity at post (`picks_mismatch`,
    400 line failure).
  - The lot number, expiry and location code are snapshots for the PDFs.
- **M76 — The proposal.** `CreateDelivery` (`deliveries.go`:334) reads `Stock.Lots` for the
  tracked variants at the order's site on the pool, before its transaction, and fills each
  tracked line's rows with `contracts.ProposeLots` (M65). A line it cannot cover gets the
  rows it can and the line keeps its quantity; the delivery page marks it *Short by n*,
  and the post refuses until the picks add up.
- **M77 — Editing picks.** `UpdateDelivery` takes, per line, `lots: [{lotId, locationId,
  quantity, allowExpired}]`. The delivery page shows a lot table per tracked line: the
  proposed lots, *Change lot* (a picker of the site's lots with expiry, status and on
  hand, FEFO order), and *Use expired lot* only for a caller with
  `inventory:expired-override` (from `GET /meta`).
- **M78 — Posting.** `PostDelivery` reads `Today` and `ExpiredOverride` on the pool
  before its transaction (M62), then builds **one `IssueLine` per lot row**, each with the
  order line's id as `LineID` (so the reservation is consumed as before), the row's
  location and `LotID`, and `AllowExpired`. Refusals map back to the delivery line and
  the lot row through the refusal's `Index` (`lot_expired`, `lot_blocked`,
  `insufficient_stock`). A refusal on a line whose picks are all proposed re-proposes
  that line once and posts again (M65a), and the delivery keeps the new rows. The posted
  delivery keeps the rows; nothing else changes in Orders' buckets.
- **M79 — The PDFs** (`deliverypdf.go`). The **pick list** prints a row per lot row, by
  location, with the lot number and expiry in a *Lot* column (nb *Parti*). The **packing
  slip** prints the lot numbers (and expiry) under each line, so the customer receives
  the one-step-forward record of 931/2011 on paper. Serial numbers are listed, wrapped.
  The order page's delivery history shows the lots.

## 5. Decisions — M, Manufacturing lot capture

- **M80 — The output lot.** `manufacturing.boms` gains `lot_pattern varchar(40) NOT NULL
  DEFAULT '{MO}'`, snapshotted onto `orders.lot_pattern` at confirm.
  - Tokens: `{MO}` (the order's display number, `MO-12-2`), `{YYYY}`, `{YY}`, `{MM}`,
    `{DD}` (the production date in the workspace zone), `{SKU}`, `{N}` (a per-pattern
    counter in `manufacturing.counters`, keyed `lot:<rendered prefix>`). Anything else is
    literal. A rendered number must pass M52's rule (`lot_pattern_invalid`, 400, judged on
    the BoM save with a sample render).
  - With the default `{MO}`, every production of one MO adds to one lot; `L{YYMMDD}-{N}`
    makes a lot per production.
  - A production of a tracked product carries `outputLot: {number?, supplierLot?,
    productionDate?, expiry?, bestBefore?}`; the number defaults to the rendered pattern,
    the production date to today, the dates to M57's defaults from the variant's offsets.
  - Manufacturing calls `StockLedger.EnsureLots` after the MO lock (D1 M26a step 2) and
    before `Produce`, then passes the lot id on the `ProduceLine`. `NewLot.ProductionDate`
    is always set: the request's, else `Today` (read on the pool before the transaction,
    M62). On a lot that exists — the `{MO}` lot's second production — only the dates the
    request states are compared (M57), so the defaults never refuse a repeat.
  - **An open quarantining deviation reaches later lots** (Q, M99): after `EnsureLots`,
    Manufacturing puts a quarantine hold from each open deviation of the MO that holds its
    output (`deviations.quarantine`) on every lot `EnsureLots` answered, in the same
    transaction (`HoldLots`, source the deviation). A first-piece failure that happens
    before any output therefore still holds every lot the MO makes afterwards.
- **M81 — Serial output.** For a serial product, a production's good quantity must be a
  whole number (`serial_quantity`, 400); `outputSerials: {first}` or `{numbers: [...]}`
  gives one number per unit, default `{MO}-0001` upwards continuing from the MO's
  serials so far. One `ProduceLine` per serial of quantity 1. Output scrap is never
  serialised.
- **M82 — Consumed lots.** A new table `manufacturing.production_line_lots`:
  ```
  production_id, component_id, lot_id bigint, location_id bigint,
  consumed numeric(14,3) default 0, scrapped numeric(14,3) default 0,
  allow_expired boolean default false,
  primary key (production_id, component_id, lot_id, location_id)
  ```
  - `production_lines` stays the per-component total (its primary key unchanged);
    `production_line_lots` is the split, written only for tracked components. The sum of a
    component's rows equals its line's consumed and scrapped.
  - The production request's line gains `lots: [{lotId, locationId, consumed, scrapped,
    allowExpired}]`.
  - **The production draft** (`GET …/production-draft?quantity=`) proposes them with
    `ProposeLots` per component from `Stock.Lots`, read on the pool, preferring the
    request's `fromLocationId` or the MO's component location when set. It also carries,
    per tracked component, **every lot at the site** with expiry, status and quantity
    (the same `Stock.Lots` read), which *Change lot* offers, so neither the MO page nor a
    station (M135) calls Inventory for the picker.
  - `splitConsumption` (`productions.go`:594) keeps its location split for untracked
    components and uses the lot rows for tracked ones: one `ConsumeLine` per row.
  - A tracked component sent without rows is posted with **`AutoLot`** (M65a): Inventory
    picks FEFO under its own locks, `StockPosted.Picks` names the lots, and Manufacturing
    writes them to `production_line_lots` in the same transaction. An API caller that does
    not care still posts; the production answer names the lots used. Rows the draft
    proposed and the person did not change are re-proposed once on `insufficient_stock` or
    `lot_blocked` (M65a).
- **M83 — The screens.**
  - The **production dialog** (MO page) and **Register production** (shop floor, D1 M31)
    show, per tracked component, the proposed lots with expiry and quantity, *Change lot*,
    and for the output the lot number (editable, prefilled from the pattern), dates, or the
    serial range.
  - The **MO page** gains a *Lots* tab: the output lots with links, and per component the
    lots consumed, each linked to Inventory's lot page and, from T, to the trace.
  - The BoM editor gains the *Lot number pattern* field with a preview.
- **M84 — Expiry of the output** comes from the product variant's offsets, counted from
  the production date. Taking the shortest component expiry instead is Later (§13).
- **M85 — Live and timeline.** The production-posted event's payload gains the output lot
  ids and the count of consumed lot rows; no new topic.
- **M86 — The lock order** is D1 M26a with the ledger step split: after the MO (step 2),
  the component re-read (step 3) and the work orders (step 4, and Q's check rows), step 7
  becomes
  - **7a.** `EnsureLots` (Inventory's M58 steps 3–5: the counter, new lots, the lots);
  - **7b.** `HoldLots` for the MO's open quarantining deviations (Q);
  - **7c.** `Produce` (M58 steps 2 and 5–8).

  Two productions of one MO are already serialised by the MO row, so the shared `{MO}` lot
  is never inserted twice. The lots come before the locations `Produce` shares, for every
  writer (M58): no writer holds a location and then waits for a lot (a hold takes only
  lots, a location's archive takes no lot), so the order does not cycle.

  **A deadlock is retried once.** A production write that fails with 40P01 runs again from
  `withOrders`' first attempt, once; a second deadlock is a 409 `revision_conflict`. The
  test drives two productions and a hold across shared lots on a pool of four.

## 6. Decisions — T, traceability, blocking and labels

- **M87 — The trace screen is Inventory's** (R reading 6): `/inventory/trace` (nb
  *Sporing*), reached from the lot page, the lots list, the lookup, Manufacturing's MO
  *Lots* tab and Orders' delivery page. It needs `locations-view` and `stock-view`.
- **M88 — The graph.** Built from Inventory's own records, read on the pool:
  - **nodes** are lots;
  - **edges** are production movements: in one movement of kind `production`, every lot on
    a consume line is a **parent** of every lot on a produce line. No genealogy table is
    kept; the movement lines with a lot are the record (R §4);
  - **unbuilds are reverse edges** (G, M123): a movement of kind `unbuild` returns
    components from a finished lot. It is shown on both lots as an end (*Unbuilt by
    UB-n*, *Returned by unbuild UB-n*), and **never expanded**: the usage walk from the
    finished lot does not follow it into the restored component lots, the origin walk from
    a component lot does not follow it into the finished lot, and the block cascade (M92)
    does not cross it;
  - **ends** are the movements with a lot that start or end its path: receipts (the
    movement's `supplier_name` and `supplier_reference`, I15), adjustments and imports on
    the origin side; issues (deliveries, their `source` = `orders/delivery/<id>`),
    adjustments down and transfers out on the usage side.
- **M89 — Two directions.** `GET /trace?lotId&direction=usage|origin&components=all|tracked&maxDepth`:
  - **usage** (*where did it go*, Origin → Usage in BC's words): the lot's own issues and
    the lots made from it, recursively, with their issues;
  - **origin** (*where did it come from*, Usage → Origin): the lot's receipts and the lots
    it was made from, recursively;
  - `components=all` also lists untracked components consumed by each production (as
    leaves without a lot); `tracked`, the default, lists lots only;
  - `maxDepth` 1–10, default 10; at most **2,000 nodes** (`trace_too_large`, 400), D1
    M15a's bound;
  - **already traced**: a lot reached twice is expanded once; later references carry
    `seenAt` (the first node's path) and render collapsed with *Already shown above*;
  - every node carries its quantities: produced or received under the lot, and, on each
    edge, the quantity consumed of the parent.
- **M90 — Who received it** (rule 12). `contracts.DeliveryTrace`, provided by Orders:
  ```go
  // DeliveryTrace answers what a recall needs of Orders' deliveries: the delivery
  // and order numbers, the customer id and when it was posted. It is provider-gated:
  // it refuses with ErrForbidden a principal without orders:access, and a call with
  // no request.
  type DeliveryTrace interface {
      Deliveries(ctx context.Context, deliveryIDs []int64) ([]TracedDelivery, error)
  }
  type TracedDelivery struct {
      DeliveryID, OrderID int64
      DeliveryNumber      int64
      OrderNumber         int64
      CustomerID          int32
      PostedAt            time.Time
  }
  ```
  - Inventory calls it on the pool, once per trace, with the delivery ids of the issue
    movements (at most 1,000 a call). Without Orders (a nil slot), or on `ErrForbidden`,
    the trace shows *Delivery* with the movement only; any other error fails the trace.
    The refusal is explicit, not an empty answer, so Inventory cannot mistake *no
    deliveries* for *not allowed to see them*.
  - Customer **names and contacts** come from `contracts.CustomerDirectory`
    (`Customers`, `ContactsForCustomer`) and are shown only when the caller also holds
    `customers:view`, which Inventory checks — the CustomerDirectory is a
    single-provider read with no gate of its own, so the consumer gates it as D1 M43a does.
- **M91 — Export.** `GET /trace.csv` and `GET /trace.pdf` with the same parameters: one row
  per node and end — depth, lot, SKU, product, quantity, date, document, supplier or
  customer (names only under M90's gate). The PDF uses the house PDF style
  (`labels.go`'s fonts) and states the date, the user and the direction, for
  Mattilsynet's *within hours* answer.
- **M92 — Block with the downstream list.** The lot page's **Block** (a `block` hold,
  cause `recall`, `complaint` or `other`, note) opens a dialog that first shows the usage
  trace's summary: the lots made from it still in stock, and the customers and deliveries
  that received it.
  - *Also block the lots made from it* (default on) blocks every downstream lot still
    holding stock, in one transaction: one `block` hold per lot (M64), source
    `inventory/lot/<root lot id>`, cause as chosen, the lots `FOR NO KEY UPDATE` in id
    order, each with its own event naming the root lot.
  - **The walk is run again inside the block's transaction**, after the root lot's lock:
    the dialog's summary is advice read on the pool, and a production posted since it was
    read may have made another lot from the root. The request blocks what the walk finds
    then and answers the list, which the dialog shows as *Blocked n lots* (with any that
    the summary did not show marked *new*). The walk's 2,000-node bound applies; a block
    beyond it is `trace_too_large` and nothing is blocked.
  - *Release* releases that recall's holds, on the root lot alone or on it and every lot
    it blocked; a hold of another source on any of them stays (M64).
  - Recall campaign objects (contact logs, returns) are phase 3 (R §7).
  - Needs `inventory:lots-manage`.
- **M93 — GS1-128 labels** (`GET /lots/labels.pdf?ids=…&format=gs1`):
  - the element string is FNC1, then **(01)** the GTIN-14 when the variant's barcode is a
    valid GTIN-8, -12, -13 or -14 (left-padded with zeros, check digit verified), **(11)**
    the production date when set, **(17)** the expiry when set, **(15)** the best-before
    when set and no expiry, then **(10)** the lot number, or **(21)** the serial, **last**,
    so no separator follows it. Fixed-length fields (01, 11, 15, 17) need no FNC1;
  - FNC1 is the rune `'ñ'` in the text maroto passes to boombuler's `code128.Encode`
    (R §5.1); the human-readable line below prints the AIs in brackets;
  - without a GTIN, the label falls back to M73's internal LOT token and says *No GTIN —
    not a GS1 label*, because (10) and (21) are only valid with an item identifier;
  - layouts L7160 and L7163, as M73; ZPL is phase 3.
- **M94 — Links.** The movement page links each line's lot; Manufacturing's *Lots* tab
  links *Trace*; Orders' delivery page links each lot to Inventory's lot page when the host
  has Inventory and the caller holds `stock-view` (the link is hidden otherwise).

## 7. Decisions — Q, quality checks and deviations

- **M95 — Check templates on operations.** `manufacturing.bom_operation_checks`:
  ```
  id, operation_id (cascade), sequence, kind, title varchar(200), instructions text (≤ 5000, Markdown),
  nominal numeric(14,4) null, min numeric(14,4) null, max numeric(14,4) null, unit varchar(20) null,
  frequency text, quarantine_on_fail boolean default false
  ```
  - `kind`: `instruction` (read and acknowledge), `pass_fail`, `measure`, `photo`;
  - a `measure` needs `min` or `max` (or both), `min ≤ nominal ≤ max` when given
    (`check_limits_invalid`, 400);
  - `frequency`: `each_work_order` (recorded once before the work order finishes) or
    `first_piece` (recorded before any output is registered at that work order, and shown
    first at **Start**);
  - at most 20 checks per operation; edited in the BoM editor with the operation, in the
    BoM's one request (D1 M13), needing `manufacturing:boms-manage`.
- **M96 — The snapshot.** Confirm copies the checks onto `manufacturing.work_order_checks`
  (`work_order_id`, the template's fields, `template_id`), as D1 M17 copies operations. Later
  BoM edits never reach an open MO.
- **M97 — Results.** `manufacturing.check_results`:
  ```
  id, work_order_check_id, result text (done | pass | fail), value numeric(14,4) null,
  note varchar(1000) null, photo_key varchar(200) null, user_id uuid, user_display,
  station_id uuid null (S), recorded_at; unique (work_order_check_id) where superseded_by is null,
  superseded_by bigint null
  ```
  - `POST /work-orders/{id}/checks/{checkId}/result` `{result | value, note, photoId}` needs
    `manufacturing:shop-floor` or `orders-manage` (D1 M33).
  - A `measure` is judged by the server: `pass` within `[min, max]`, `fail` outside. An
    `instruction` is `done`. A `photo` needs a photo.
  - Recording again supersedes the previous result (kept, linked); the latest counts.
  - **Locks.** Recording runs through `withOrders` (D1 M26a), because a fail may put a hold
    through the ledger (rule 24): `LockVariants` on the MO's variants, the MO `FOR
    UPDATE`, the work order, then **the `work_order_checks` row `FOR UPDATE`** — so two
    results recorded at once for one check serialise and the second supersedes the first
    — then the result, the deviation (M99), and last `HoldLots`.
  - Photos: `POST /work-orders/{id}/photos` (multipart, JPEG or PNG, ≤ 10 MB, metadata
    stripped with `srv/imagemeta` as Products' images are) into the storage scope
    `manufacturing-quality`, answering a `photoId` (`manufacturing.check_photos`: id,
    work_order_id, object_key, content_type, size_bytes, uploaded_by…, created_at). A
    photo is a quality record: it is kept when its result is superseded, and nothing
    deletes it in phase 2 (an upload never attached to a result within 24 hours is
    removed by the module's `object_deletions` ledger, Customers' shape). The admin page
    says what storage they take.
- **M98 — What the checks gate.** Productions are posted per MO
  (`PostManufacturingProduction`, `productions.go`), not per work order, so the gate is
  stated against the MO's work orders. The **output work order** is the last one, which
  `postProduction` already treats as the one whose output is registered.
  - **A production** is refused `checks_incomplete` (409, naming them) while any of these
    has no result: **every** check (both frequencies) of every work order that precedes
    the output work order, and the **`first_piece`** checks of the output work order
    itself.
  - **Finish** (D1 M29) of any work order is refused while any of **its own** checks has no
    result, either frequency.
  - **`first_piece`** checks of a work order also gate its **Register quantity** once F
    exists (M116): the first piece is checked before quantity is passed on.
  - *Precedes* is by sequence until F lands, and by the dependency graph after it (M114):
    the work orders from which the output work order is reachable. Q stays before F in the
    stack; F's layer changes this one predicate and its test.
  - A `fail` result does not block: it has made a deviation (M99), and the supervisor
    decides. Starting is never gated.
- **M99 — Deviations** (`manufacturing.deviations`; nb *avvik*), numbered `DEV-<n>`:
  ```
  id, number, title varchar(200), description text (≤ 5000), order_id null, work_order_id null,
  check_result_id null, lot_id bigint null, variant_id int null,
  disposition text null (use_as_is | rework | scrap | return), status (open | closed),
  quarantine boolean default false, opened_by…, opened_at, closed_by…, closed_at, closing_note,
  revision
  ```
  - **A failed check opens one** automatically, in the result's transaction, linking the
    MO, the work order and the result, and the MO's output lots so far when the product
    is tracked.
  - A person opens one by hand from the MO page, the work order view (*Report a
    deviation*) or a lot page link (`orders-manage` or `shop-floor`).
  - **Quarantine:** when the check says `quarantine_on_fail`, or the person ticks *Put the
    output in quarantine*, the deviation sets `quarantine` and Manufacturing calls
    `StockLedger.HoldLots` (kind `quarantine`, cause `deviation`, source
    `manufacturing/deviation/<id>`) on the same transaction, after its own rows (rule
    24), for **every lot the MO has produced so far**. A deviation opened from a lot page
    holds that lot.
  - **Lots made later are held too.** At a first-piece failure there may be no output lot
    yet. While an MO has an open deviation with `quarantine`, every lot a production of
    that MO makes or adds to gets a quarantine hold from that deviation, in the
    production's transaction (M80, M86 step 7b).
  - **Close** needs `manufacturing:quality-manage`, a disposition and a closing note.
    Closing with `use_as_is` or `rework` calls `ReleaseLotHolds` for the deviation's own
    source: only its holds go, and a lot another deviation or a recall also holds stays
    held (M64). `scrap` and `return` keep the holds when the deviation closes — what is left
    of the lot is not to be used — and the page links to Inventory's adjustment form to
    write it off; the closed deviation offers *Release its holds* (`quality-manage`) for
    a lot returned or written off. Rework MOs are Later.
  - **Locks** (rule 24, D1 M26a): opening, quarantining and closing take `LockVariants` on
    the MO's variants (or the lot's variant, for one opened from a lot page) first, then
    the MO `FOR UPDATE`, the deviation `FOR UPDATE`, and call the ledger last; Inventory
    takes the lots `FOR NO KEY UPDATE` in id order inside.
  - The **deviations list** (`/manufacturing/deviations`, nb *Avvik*, under Overview):
    filters by status, MO, product, lot, period; CSV.
  - Live: `manufacturing.deviations` (the list) and the MO's topic.
- **M100 — Inspection on receipt.** `inventory.variant_rules(variant_id pk,
  inspect_on_receipt boolean, updated_by…, updated_at, revision)`, Inventory's (R's brief:
  an Inventory-side flag), set on the variant page (`inventory:stock-adjust`).
  - Only a tracked variant may require it (`inspection_requires_tracking`, 400).
  - A receipt of such a variant creates its lots with a **quarantine hold** (cause
    `inspection`, source the receipt's movement). Receiving into a lot that already exists
    puts **a new inspection hold** on it from this receipt, so the added quantity is never
    used before it is inspected; the whole lot is in quarantine until that hold is
    released (an earlier receipt's accepted quantity with it — a lot is one quantity, and
    the person is told so on the receipt's answer).
  - **Release** is the lot page's release of that receipt's hold (M64),
    `inventory:lots-manage`. The lots list filter `status=quarantine&holdCause=inspection`
    is the inspection queue, linked from the
    Inventory home page as *Awaiting inspection*.
- **M101 — The shop floor.** The work order view (D1 M31) gains a **Checks** panel, in
  sequence: first-piece checks at the top until recorded, each with large Pass / Fail
  buttons, a numeric pad for measures (the limits shown), or *Take photo* (the tablet
  camera through `<input type=file capture>`). The MO page's work orders tab lists the
  results.
- **M102 — Permissions (Q).**

  | Key | Display (en / nb) | Gives |
  | --- | --- | --- |
  | `manufacturing:quality-manage` | Manage quality / Administrere kvalitet | Closing deviations, which releases their own holds; editing a closed deviation's note |

  Recording results needs `shop-floor` or `orders-manage`; editing templates needs
  `boms-manage`.
- **M103 — Codes.** `checks_incomplete`, `check_not_open` (409: the work order is done or
  cancelled), `check_limits_invalid`, `photo_required` (400), `deviation_closed` (409).
  `hold_not_inventorys` (409, M64) is Inventory's, answered when the lot page tries to
  release a deviation's hold.
- **M104 — Erasure.** Results and deviations keep user ids with display snapshots (D1's
  rule 19 amendment); a deviation's free text is not personal data by intent, and the
  reference page says so.

## 8. Decisions — R, replenishment suggestions

- **M105 — The rule is Inventory's placement.** Inventory's placement (I54) already keeps
  the reorder point and maximum per (variant, site). R adds `inventory.placements.multiple
  numeric(14,3) NULL CHECK (multiple > 0)`: the quantity a suggestion is rounded up to,
  set on the variant page beside the others and in the placements import. The low-stock
  list rounds to it too when set, before the packaging rounding (I57).
- **M106 — The read.** `contracts.Stock` gains:
  ```go
  // Placements is every placement at the site with a reorder point, with what the
  // low-stock rule reads (inventory phase 2 I56): usable on hand (M63: held and
  // expired lots left out), reserved and incoming by transfer. Manufacturing adds
  // its own orders and Orders' unreserved demand to it (manufacturing phase 2 M107).
  Placements(ctx context.Context, siteID int64) ([]StockPlacement, error)

  type StockPlacement struct {
      VariantID                      int32
      ReorderPoint                   string
      Maximum, Multiple              string // "" when unset
      OnHand, Reserved, Incoming     string
  }
  ```
  On the pool, one snapshot; at most 5,000 rows a site (more is a failure the job logs).
- **M107 — Projected stock** for a variant at a site:
  ```
  projected = usable onHand − reserved + incoming
            + Σ supply MOs of the variant at the site (remaining = quantity − produced, ≥ 0)
            − Σ open MOs' unreserved component demand of it at the site
              (required − consumed − scrapped − reserved, ≥ 0)
            − Σ unreserved open sales demand of it at the site (M107a)
  ```
  - **Supply MOs** are the open ones (`confirmed`, `in_progress`) and the **drafts whose
    origin is `reorder`**: a suggestion turned into a draft must not be suggested again
    the next night. Other drafts are not counted.
  - **Make-to-order MOs' output is left out** (origin `sales_order`): it is promised to its
    sales line. That line's demand is left out too — Manufacturing drops from M107a's
    answer every sales line that an open MTO MO serves (its `origin_line_id`) — so an MTO
    pair neither adds nor removes stock. Sub-assembly MOs (origin `parent`) count: their
    parent's component demand is subtracted above.
  - Reserved already counts the MO and sales reservations, so only the unreserved parts
    of demand are subtracted.
- **M107a — Unreserved sales demand** is Orders' to say. A sales line can be open and
  unreserved: confirmed without reserving (`orders/stock.go`, `confirmed_without_reserving`),
  or reserved short. Orders already provides Manufacturing a consumer-gated read of its
  lines, `contracts.SalesOrderLines` (`srv/contracts/production.go`, phase 1's MTO), whose
  rules fit (pool, one snapshot, no personal data, no authorization); it gains a third
  method rather than a new slot:
  ```go
  // Unreserved is what open sales order lines of the variants, fulfilled from
  // the site, still need that they do not hold reserved (manufacturing phase 2
  // M107a): per line, the open quantity less what it holds reserved, > 0 only.
  Unreserved(ctx context.Context, variantIDs []int32, siteID int64) ([]UnreservedDemand, error)

  type UnreservedDemand struct {
      OrderID, LineID int64
      VariantID       int32
      Quantity        string // exact decimal text, > 0
  }
  ```
  - Without Orders (a nil slot) the term is zero and the suggestions page says *Sales
    orders not counted*.
  - Consumer-gated: the job reads it as the system; the suggestion's reason shows the sum
    as *needed by sales orders n*, never a customer or an order number, to holders of
    `manufacturing:access`.
  - At most 5,000 variants a call, as `Placements`; the job calls it once per site.
- **M108 — The suggestion**, by I56's rule over `projected`:
  - low when `projected ≤ reorderPoint`; `target = maximum`, or `reorderPoint + 1`;
    `needed = target − projected`, rounded up to `multiple` when set;
  - **make** when the variant has a default active BoM, else **buy**;
  - **need-by** is today when usable `onHand − reserved` is already at or below the reorder point,
    else today + the BoM's `leadTimeDays` business days (buy: today);
  - **reason** is stored as figures and shown as a sentence: *Projected 12 is at or below
    the reorder point 20: usable on hand 30, reserved 25, incoming 0, in production 7,
    needed by production 0, needed by sales orders 0.*
- **M109 — The job.** A lease worker `manufacturing.replenishment` (built through
  `module.Workers`, as `manufacturing.mto-worker` is):
  - runs nightly at 02:00 in the workspace zone, and on demand from `POST
    /replenishment/runs {siteId?}` (`orders-manage`, rate-limited to one a minute per
    workspace, 202 with the run id);
  - per active site: reads `Stock.Placements`, `SalesOrderLines.Unreserved` for the placements'
    variants, and its own MOs on the pool, then in **its own transaction** replaces the
    site's open suggestions (`manufacturing.suggestions`) with the new ones; a suggestion
    acted on is kept;
  - holds no pooled connection while waiting for another (the lease-worker lesson);
  - `manufacturing.replenishment_runs(id, site_id null, started_at, finished_at, status,
    suggestions, error)` keeps the last 90 days.
- **M110 — `manufacturing.suggestions`**: `id, run_id, site_id, variant_id, sku,
  product_name, kind (make | buy), quantity, need_by, reason jsonb, bom_id null, status
  (open | done | dismissed | superseded), order_id null, acted_by…, acted_at`; unique
  `(site_id, variant_id) WHERE status = 'open'`.
- **M111 — The screen** (`/manufacturing/suggestions`, nb *Forslag*, under Manufacturing
  orders):
  - one table per site: product, projected, reorder point, quantity (editable), need-by,
    reason; make rows first;
  - select rows → **Create draft MOs** or **Create and confirm** (`orders-manage`): one MO
    per row, origin `reorder` (a new origin kind beside D1's `manual`,
    `sales_order` and `parent`; `origin_kind` is `varchar(12)`), due date the need-by, the default BoM; confirm runs D1
    M20a's two-pass reserve. The suggestion becomes `done` with the MO id;
  - **Dismiss** until the next run;
  - **buy** rows are read-only with *Export CSV*: there is no Purchasing module;
  - **Run now**, and the last run's time;
  - a dashboard card count *Suggestions to make*.
  - **Automatic creation** (R reading 7): `manufacturing.replenishment_rules(variant_id,
    site_id, auto_create boolean default false, auto_confirm boolean default false)`, set
    on the BoM page per site. When on, the nightly run creates the MOs itself, as the
    system, and records them on the suggestion. Off by default; the admin page warns.
    **It skips a variant that already has an open or draft MO with origin `reorder`** at
    the site, so a night whose MO has not started yet does not add another; the
    suggestion is still listed, marked *An MO from an earlier suggestion is open*.

## 9. Decisions — F, routing upgrades

### Dependencies

- **M112 — Operations form a graph.** `manufacturing.bom_operation_dependencies
  (operation_id, depends_on_id)`, primary key both, both referencing `bom_operations` on
  delete cascade, `CHECK (operation_id <> depends_on_id)`.
  - The BoM request's operation gains `dependsOn: [clientKey…]`. **Omitted** means *the
    previous operation by sequence* (D1's line, so every phase 1 BoM keeps its meaning and
    the migration writes those rows); `[]` means *can start at once*.
  - A cycle is refused `operation_cycle` (400, naming the path); a dependency on another
    BoM's operation is `operation_unknown` (400).
  - The BoM editor shows a *Depends on* multi-select per operation and a small graph
    preview.
- **M113 — The snapshot.** Confirm copies the edges to
  `manufacturing.work_order_dependencies (work_order_id, depends_on_id)` between the
  order's work orders.
- **M114 — Readying by the graph.** A work order is `ready` when every predecessor is
  `done` (or `cancelled`), or when each unfinished predecessor has reached its send-ahead
  quantity (M116). `ReadyNextWorkOrder` (`queries/work_orders.sql`:87) becomes
  `ReadyWorkOrders(order_id)`, run after every finish and quantity registration, readying
  every `pending` work order whose predecessors allow it. At confirm, the work orders with
  no predecessor are `ready`. `force: true` (D1 M27) still starts a pending one.
- **M115 — Planning by the graph.** The planner (`schedule.go`:105, `planChain`) walks the
  work orders in a topological order (by sequence among equals) and starts each at
  `max(predecessors' planned finish)`, or at the send-ahead offset (M116), instead of one
  cursor. Each still takes its center's first free slot (D1 M35, M35a). Dragging a bar
  (D1 M36) moves its successors by the graph.
  - **The pre-lock set grows with the graph.** `withScheduledOrders`' `centersOf` names,
    for a plan or a drag, the work center of the work order **and of every successor it
    may move**, and for a group work order every active member of the group (M119), all
    taken in id order before any row lock. A successor found under the locks on a center
    not locked returns `errRelock` and the write starts again, as phase 1 does.

### Send-ahead

- **M116 — Send-ahead quantity.** `bom_operations.send_ahead_quantity numeric(14,3)
  NULL CHECK (> 0)`, snapshotted on the work order (BC's send-ahead, MRPeasy's overlap).
  - **On the floor:** a work order gains `quantity_done numeric(14,3) default 0`, raised
    by **Register quantity** (`POST /work-orders/{id}/quantity {quantity, clientRequestId}`,
    `shop-floor`), which posts nothing to stock — only the last work order's production
    does. A successor of a work order with a send-ahead quantity becomes `ready` once the
    predecessor's `quantity_done ≥ send_ahead_quantity`.
  - **In the plan:** the successor may start at the predecessor's planned start + the
    working time the predecessor needs for the send-ahead quantity (setup + its cycles,
    D1 M28a), and may not finish before the predecessor's planned finish + the successor's
    time for the last send-ahead batch.
  - A send-ahead quantity at or above the MO quantity behaves as none.

### Work center groups

- **M117 — A group** (`manufacturing.work_center_groups`: `id, code [A-Z0-9]{1,16} unique,
  name, site_id, status, revision`; `work_center_group_members (group_id, work_center_id)`)
  holds active centers of one site (`work_center_other_site`). Edited under *Work
  centers* (`boms-manage`).
- **M118 — An operation targets a center or a group.** `bom_operations` gains
  `work_center_group_id bigint NULL`, `work_center_id` becomes NULL-able, with `CHECK
  (num_nonnulls(work_center_id, work_center_group_id) = 1)`. The work order snapshots the
  group in `work_center_group_id` and keeps `work_center_id NOT NULL`: at confirm the
  group's first active member by code; planning chooses again (M119). Setup, capacity,
  efficiency and rates are snapshotted from the **chosen** member and re-snapshotted when
  the member changes, and the expected minutes with them. **This amends D1 M28a** (*the
  snapshot is never recomputed*) for a work order with no time logged: a member change
  happens only before a log exists (M119), so nothing measured is lost. M126 amends it
  likewise for split and merge.
- **M119 — Choosing the member.**
  - **Plan** tries each active, unblocked member and takes the one with the **earliest
    finish**; ties go to the higher time efficiency, then the code. It takes the advisory
    locks of every member in id order (D1 M35a).
  - **The board** lets a group work order be dragged to another member's row
    (`work_center_not_in_group`, 400, for any other row).
  - **On the floor**, an unplanned group work order with no log shows in every member's
    queue under *Group work*; starting it at a member sets that member (re-snapshotted,
    M118). Once a log exists the member is **fixed**: the work order shows only in that
    member's queue, planned or not; a later drag is `work_order_started` (409), and a
    start at another member — a stale *Group work* list — is `work_order_member_fixed`
    (409, naming the member).
  - **Cost** uses the actual member's snapshotted rates (D1 M39–M40).

## 10. Decisions — G, BoM depth

### By-products

- **M120 — Output lines.** `manufacturing.bom_outputs (id, bom_id cascade, variant_id,
  quantity numeric(14,3) > 0, cost_share numeric(5,2) ≥ 0, sort_order)`, at most 20 per
  BoM, goods only, never the BoM's own variant (`output_is_product`, 400) and **never a
  component of the same BoM** (`output_is_component`, 400). The shares sum to at most
  100 (`cost_share_over`, 400); the main product takes the rest.
  - **The cycle check covers outputs** (D1 M14a): the BoM graph gains an edge from each
    by-product to the BoM, so a BoM whose by-product is, through its sub-BoMs, one of its
    own components is `bom_cycle`, as a component cycle is.
- **M121 — Snapshot and posting.** Confirm copies them to `manufacturing.order_outputs`
  (with `expected = quantity × MO quantity / outputQuantity`, rounded half-up to three
  decimals, `produced` 0). A production carries `outputs: [{outputId, quantity, lot?}]`,
  prefilled proportionally to the good quantity; each becomes another `ProduceLine` with
  `LineID` the output's id. A tracked by-product's lot follows M80 with the BoM's pattern.
  - **Line ids.** `order_outputs` ids come from their own table, so one may equal a
    component's or the main line's id. Produce lines are keyed by **(VariantID, LineID)**
    (M67), and a refusal names the line by its `Index`; Manufacturing never relies on
    `LineID` alone to find a produce line.
  - **Where it lands:** the MO's output location (D1 M2), as the main product; a
    production may not name another.
- **M122 — Cost** (R reading 9). At close, the MO's actual cost (D1 M41: materials,
  machine and labour together) is split: each by-product carries `cost_share %` of it, the
  main product the rest; each part's cost per unit is its share ÷ its produced quantity.
  The estimate is split the same way. The production cost report gains a by-products
  section.
  - **A by-product that produced nothing** has no cost per unit: its share moves to the
    main product, and the report shows *Not produced* with the share it would have had.

### Unbuild

- **M123 — An unbuild** (`manufacturing.unbuilds`, numbered `UB-<n>`; nb *demontering*)
  takes finished goods apart:
  ```
  id, number, order_id null, bom_id, variant_id, quantity > 0, lot_id null, site_id,
  from_location_id, to_location_id, movement_id, note, posted_by…, posted_at, client_request_id unique
  ```
  - **From a done MO:** for a tracked product the person **chooses the finished lot**
    (`lot_id`) from those the MO produced; quantity ≤ what that lot received from the MO's
    productions − already unbuilt from it (`unbuild_too_much`, 400). It consumes that lot
    and **restores only what the productions that produced the chosen lot consumed**,
    proportionally within them (from their `production_line_lots`), each component's
    figure rounded half-up to three decimals, the remainder on its last lot. An untracked
    product's unbuild restores from all the MO's productions, as before. Restoring from
    productions that made a different lot would invent genealogy.
  - **Free-standing, with a BoM:** components by the BoM's lines (without scrap) for the
    quantity; a tracked component needs a lot named, existing or new
    (`lot_required`).
  - Posting is one `Produce` with `Unbuild: true` and `Document`
    `manufacturing/unbuild/<id>`: the finished item as a consume line, the components as
    produce lines. Inventory posts it as movement kind **`unbuild`** (the kinds are an open
    set of constants, no migration), which the trace treats as a reverse edge, never
    expanded (M88); the movement page shows the source as *Unbuild UB-n*.
  - **Held lots.** An unbuild's consume lines may take a lot with an active hold or an
    expired one: taking a recalled item apart is how it is dealt with. When any consumed
    lot holds an active hold, Inventory puts a **quarantine hold** (cause as the consumed
    lot's, source the unbuild) on every component lot the unbuild restores to, inside
    the same `Produce`, so held material does not come back usable.
  - Cost is not recomputed; the MO shows *Unbuilt n*. Needs `orders-manage`.
  - `/manufacturing/unbuilds` lists them; the MO page has **Unbuild**.

### Split and merge

- **M124 — Split.** An MO that is `draft` or `confirmed`, with **no production and no time
  log** (`order_has_work`, 409), splits into quantities summing to its own (`split_sum`,
  400; 2–20 parts). **A planned original is unplanned** first: its slots are removed in
  the same transaction, and every part starts unplanned (the board shows them under
  *Unplanned*):
  - the original keeps the first quantity; each other part is a new MO numbered as a
    backorder (`MO-12-2`, D1 M17b), with `split_from_id` set, the same BoM, origin, due date
    and locations;
  - a confirmed original releases its reservations and every part reserves again (D1
    M20a's two-pass), and its work orders are re-snapshotted for the new quantities;
  - the MTO unique index (`mig/00160`, `ux_orders_origin_line`) becomes `WHERE origin_kind
    = 'sales_order' AND backorder_of_id IS NULL AND split_from_id IS NULL AND status <>
    'cancelled'`, and `ForOrderLines` returns the split parts with the chain.
- **M125 — Merge.** MOs of the same variant, BoM and site, each `draft` or `confirmed`, with
  no production and no time log, and **the same origin**: all `manual`, or all `reorder`,
  or all serving the same origin line (`merge_mismatch`, 400). Mixing a make-to-order MO
  with a manual one is refused, not resolved: the survivor would carry one origin and
  the other's promise would be lost.
  - the one with the lowest number survives with the summed quantity and the earliest due
    date; the others are cancelled with `merged_into_id`;
  - every MO merged is unplanned, as a split's parts are (M124);
  - their reservations are released and the survivor reserves again; its work orders are
    re-snapshotted.
- **M126 — Expected minutes are recomputed** on a split or merge. This amends D1 M28a's
  *never recomputed*, which assumed only a draft's quantity could change; here no time is
  logged, so nothing measured is lost.
- **M127 — Locking.** Split and merge run through **`withScheduledOrders`**
  (`srv/manufacturing/orders.go`): `centersOf` names every work center of every MO
  involved (and every member of a group work order's group, F), whose schedule advisory
  locks are taken in id order before any row lock (D1 M35a), because unplanning removes
  their slots. Then the canonical order: `LockVariants` on every variant involved, the
  MOs `FOR UPDATE` in id order, their work orders, then the ledger (release, then the
  two-pass reserve).
- **M128 — Timeline.** Events `split`, `split_from`, `merged`, `merged_into`,
  `unbuilt`, each with the other MOs' ids and numbers.

## 11. Decisions — S, shared shop-floor stations

This is identity work (R1 §9 reading 4) and the only delivery that adds a credential. A
**security review by an opus security reviewer is part of the delivery**, before its PR
is opened.

### The station

- **M129 — A station** (`identity.stations`; nb *stasjon*) is a registered device:
  ```
  id uuid pk, name varchar(100), token_hash bytea unique null, paired_at null,
  pairing_hash bytea null, pairing_expires_at null, token_expires_at null,
  idle_minutes integer default 5 CHECK (1..60), require_pin_with_badge boolean default true,
  allowed_networks cidr[] null, created_by…, created_at, last_seen_at, last_seen_ip,
  revoked_at null, revoked_by…
  ```
  - **Registering** (`POST /identity/stations`, `identity:manage`) returns a one-time
    **pairing code**: 8 characters from Crockford base32, valid 15 minutes, stored as
    SHA-256 (`tokens.go`'s `hashToken`). It is shown once.
  - **Pairing:** the tablet opens `/station/pair` and enters the code; `POST
    /identity/station/pair {code}` (anonymous, rate-limited per IP: 10 per 15 minutes)
    checks the hash and the expiry, clears the pairing hash, mints a 256-bit **station
    token** (`newToken`), stores its hash and `token_expires_at = now + 90 days`, and sets
    the cookie `vantigo_station`: `HttpOnly`, `Secure`, `SameSite=Strict`, path `/`, max
    age **90 days**. After that the station answers `station_unknown` and the page shows
    the pairing form: an administrator issues a new code (re-pairing).
  - **Pairing signs the browser's session out.** If the pairing request carries a session
    cookie, that session is ended and its cookie cleared in the same answer, so a station
    browser never holds both (M138).
  - **An optional network allowlist** (`allowed_networks`, set by `identity:manage`): when
    set, every station request from an address outside it is `station_unknown`, using the
    client address as the platform's rate limiter resolves it behind the proxy.
  - **Revoking** (`identity:manage`) clears the token hash, sets `revoked_at` and ends every
    operator session of the station at once.
  - **Re-pairing** issues a new code and replaces the token.
- **M130 — What a station may do alone: nothing but sign operators in, and listen.** A
  request with only the station cookie may call the station's own endpoints (`GET
  /identity/station`, its operators list, operator sign-in and sign-out) and open the
  live socket on the station path (§17). Every module operation needs an operator (M133).
- **M131 — Which work centers.** Manufacturing keeps
  `manufacturing.station_work_centers (station_id uuid, work_center_id)`, set on the work
  center's page (`boms-manage`): the station's shop floor opens on those centers and its
  picker offers only them. Identity knows nothing of work centers.
  - **The handlers enforce it**, not only the picker. For a station principal
    (`Via.Kind == ViaStation`), every allowed operation (M135) that names a work center or
    a work order — directly, or through the work order's MO for a production — checks the
    center against `station_work_centers` for `Via.StationID`, and refuses any other
    with 403 `station_scope`. For a group work order (F) the check is on the member it
    is started or registered at. A station with no centers set reaches none.

### The operator credential

- **M132 — PIN and badge.** `identity.user_pins (user_id pk, pin_hash text, set_at)`,
  `identity.user_badges (user_id pk, badge_hash bytea unique, set_at, set_by…)` and
  `identity.operator_backoff (station_id, user_id, failures integer, last_failure_at,
  next_attempt_at, primary key (station_id, user_id))`.
  - **The PIN** is 6 digits, hashed with **argon2id with the password parameters**
    (`passwords.go`) over the PIN **peppered**: HMAC-SHA-256 of the PIN under a key from
    `srv/secrets`, so a leaked table alone cannot be brute-forced over its million
    values. Refused: all one digit, and ascending or descending runs such as
    `123456` (`pin_too_simple`, 400). A user sets
    their own PIN under *Settings → Security* (`PUT /identity/me/pin {pin, currentPassword
    | a fresh session}`: a session whose sign-in is under 10 minutes old, or the current
    password). An administrator can **clear** a user's PIN (`identity:manage`), never set
    or read it.
  - **The badge** is the code a keyboard-wedge card reader types. It is stored as
    HMAC-SHA-256 under a key from `srv/secrets` (the code has less entropy than a token, so
    a plain hash would be enumerable from a leaked table). An administrator enrols it by
    scanning on the user's page (`identity:manage`).
  - **Neither is ever a sign-in credential**: the sign-in, MFA, passkey and SSO endpoints
    never read these tables (R reading 8).
- **M133 — Switching in.** `POST /identity/station/operators {userId, pin} | {badge}` with
  the station cookie:
  - The operators list (`GET /identity/station/operators`) is the users who hold
    `manufacturing:shop-floor` and have a PIN or a badge, by display name, with initials;
    nothing else about them.
  - **Rate limit per station:** 10 failed attempts per 5 minutes (`srv/ratelimit`,
    `NoRetryAfter`), then 429 for the station.
  - **Escalating backoff per (station, user)**, not a disable: disabling a PIN after
    failures would let anyone at the tablet lock a colleague out of the floor. From the
    5th consecutive failure for a user at a station, `next_attempt_at` is now + 30
    seconds, doubling with each further failure up to 15 minutes; an attempt before it is
    refused without verifying. A success clears the row. The same user at another station
    is not slowed.
  - **Alert.** The 10th failure within an hour for one user, or the station's rate limit
    tripping, writes `operator_backoff` to the audit (M139) and mails the holders of
    `identity:manage` through the system mail, at most once an hour per station; the
    station list marks the station.
  - The argon2id verification runs against a fixed dummy hash for a user with no PIN or
    in backoff, so time does not tell them apart; the answer for every failure is the same
    401 `operator_sign_in_failed`.
  - **A badge alone is not the default:** `require_pin_with_badge` defaults to **true**, so
    a found card is not enough; an administrator may turn it off per station (Monitor's
    card-only terminals) and the admin page says what that means.
  - A user who is disabled, pending, deactivated by the directory or an agent is refused
    as a session would be (rule 12's person checks).
  - Success creates `identity.station_operator_sessions (id uuid, station_id, user_id,
    token_hash unique, started_at, last_activity_at, ended_at null, end_reason null)` and
    answers an **operator token** (256 bits). The station page keeps it **in memory only**:
    a reload asks the operators to switch in again.
  - At most **10** active operators per station; signing in an operator already active
    replaces their session.
- **M134 — The operator's requests.** A request with the station cookie and
  `Authorization: Station <operator token>`:
  - **Station credentials take precedence.** A request carrying `Authorization: Station`
    or made to a station endpoint is judged as a station request only, never by a session
    cookie it also carries. And a station request **refuses while a session cookie is
    present** (`station_has_session`, 409, with the remedy *Sign out of Vantigo in this
    browser*), so the two never mix in one browser; pairing has already signed the
    browser out (M129).
  - `Access.Check` resolves both hashes, refuses a revoked or expired station or an ended
    session, and **verifies that the operator session's `station_id` is the cookie's
    station**: a token copied to another station's browser is `operator_session_ended`
    (401). It ends a session idle longer than the station's `idle_minutes` (`end_reason =
    idle`) or older than **12 hours** (`end_reason = expired`);
  - **Only the router's `wrap` gets a station principal**, and only for an operation marked
    `x-vantigo-station: allowed` (M135): `wrap` passes the rule with a station flag, and
    `Access.Check` called with any other rule — a handler of its own, a platform endpoint,
    the docs handler — refuses station credentials as unauthenticated (401). The live
    socket has its own station path (§17);
  - the principal is the operator: `UserID` the user, `SessionID` the operator session,
    `Roles` theirs, `MFAVerified` false, and `Via{Kind: "station", StationID}` — a new
    `contracts.ViaStation` kind and `Via.StationID uuid.UUID`;
  - **the permissions are intersected** with `manufacturing:access` and
    `manufacturing:shop-floor`: an Owner at a station holds those two and nothing more;
  - `last_activity_at` is touched at most once a minute.
- **M135 — The station scope.** An operation is reachable from a station only when its
  contract says `x-vantigo-station: allowed`. The router (`srv/module/router.go`, `wrap`)
  refuses any other operation for a station principal with 403 `station_scope`, after
  `Access.Check` and before the handler. The OpenAPI lint requires the extension's value to
  be `allowed` when present, and the extension only on operations whose access rule is
  `permission:manufacturing:…`. The allowed set (Manufacturing):
  - the shop floor's reads: work centers (the station's only, M131), a center's queue, a
    work order, my work, the production draft, Manufacturing's `meta`;
  - start, pause, finish, register quantity (F), register production, report scrap, block
    and unblock;
  - check results and photos, *Report a deviation* (Q).

  **Every request the station page makes is in that set or is Identity's station
  endpoints**, and a test proves it (§20): it renders the station page as a station
  principal against the real router and fails on any request outside the set. What the
  shop-floor pages read elsewhere in a person's browser is replaced on a station:
  - **the user** (`GET /me`): the operator comes from the operator sign-in's answer, held
    with the token in memory;
  - **settings and labels**: the workspace language and zone come from `GET
    /identity/station`; the catalogs are bundled, not fetched;
  - **product images**: omitted on stations in phase 2 (signed short-lived image URLs are
    Later);
  - **the lot picker** (M83's *Change lot*): served from Manufacturing's production draft,
    which carries each tracked component's site lots (`Stock.Lots`, read by Manufacturing
    on the pool) with their expiry, status and quantity, so the page never calls
    Inventory.

  Cost fields stay out (no `costs-view`); the Assistant's tools are never offered to a
  station principal.
- **M136 — Signing out.** **Switch out** on the operator's chip ends their session: `POST
  /identity/station/operators/{sessionId}/end` with the station cookie. The operator's
  token is **not** required: after a reload the tokens are gone (M133) and the chips must
  still be closable. A session id that is not one of this station's active sessions is
  `operator_session_unknown` (404). The station's idle timer ends sessions too. Closing a
  session never touches the user's time logs: a log left running shows on the floor as
  today (D1 M29).
- **M137 — Time logs record the device.** `manufacturing.time_logs`, `productions`,
  `check_results` and `deviations` gain `station_id uuid NULL`; D1 M29's *one open log per
  user* holds across stations and the user's own browser.
- **M138 — CSRF and cookies.** The station cookie is `SameSite=Strict`; operator requests
  carry the operator token in a header, which a cross-site form cannot send, so the
  station needs no CSRF token. Station endpoints that take the cookie alone (sign-in,
  switch out) are POSTs checked by the platform's cross-origin protection, as sign-in is.
  **A browser is either a station or a person's**: pairing signs the session out
  (M129), and a station request with a session cookie is refused (M134). A person who
  signs in on a station tablet uses it as their own browser until they sign out; the
  station page says so.
- **M139 — Audit.** Identity's audit trail (`audit.go`, `writeAudit`) records, on the
  writing transaction: `station_registered`, `station_paired`, `station_revoked`,
  `station_repaired`, `pin_set`, `pin_cleared`, `pin_disabled`, `badge_enrolled`,
  `badge_cleared`, `operator_signed_in`, `operator_sign_in_failed` (station, the user id
  chosen, never the PIN), `operator_backoff` (M133's alert), `operator_signed_out` (with
  the reason), `station_session_signed_out` (pairing ended a session),
  `station_networks_changed`.
  The station list shows each station's last activity and active operators.
- **M140 — The station screen** (`host/routes/station`, outside the app layout, as
  sign-in is):
  - unpaired: the pairing form;
  - paired: a bar with the station's name and the **operator chips** (each with a
    *Switch out*), **Switch in** opening the operators grid with a PIN pad, or a badge
    scan field that is always focused;
  - below it, the shop floor (`@vantigo/manufacturing-ui`'s shop-floor pages), rendered for
    the **active operator** chosen by tapping a chip; each action goes out with that
    operator's token, and the pages take the station context of M135 instead of the
    host's user, settings and image requests;
  - live updates on the station's own socket (§17), one socket for the station whichever
    operator is active; a refetch goes out with the active operator's token;
  - an idle warning 30 seconds before a session ends.
- **M141 — Erasure.** Erasing a user deletes their PIN, badge and operator sessions'
  token hashes; the sessions' rows keep the user id for the audit, as sessions do.
- **M142 — Permissions (S).** Registering, pairing, revoking stations and clearing PINs or
  enrolling badges need `identity:manage`. No new key: the station acts within
  `manufacturing:shop-floor`.
- **M143 — Codes.** `operator_sign_in_failed` (401), `station_unknown` (401: no, a revoked
  or an expired station cookie, or an address outside the allowlist),
  `operator_session_ended` (401: also a token of another station), `station_scope` (403:
  an operation not allowed, or a work center not the station's), `station_has_session`
  (409), `operator_session_unknown` (404), `pairing_code_invalid` (400), `pin_too_simple`
  (400), `too_many_operators` (409).

## 12. Decisions — K, kits (optional)

Built only if the stack has room; otherwise the three decisions move to §14 unchanged.

- **M144 — A kit BoM.** `boms.kind text default 'normal' CHECK (kind IN ('normal','kit'))`.
  A kit has lines, no operations, no by-products; it cannot be the default for MTO
  (`kit_not_manufactured`, 400). Kits nest at most 3 levels.
- **M145 — A kit line has child lines.** Revision 1 kept the kit on one order line and
  reserved its components under it, which the ledger cannot do: a reservation line names
  one variant (`ledger.go`'s reserve refuses a line id with two), and
  `contracts.Production` is gated on `manufacturing:access`, which an order's author need
  not hold. So:
  - **Each component is its own Orders line**: `orders.lines` gains `parent_line_id bigint
    NULL REFERENCES orders.lines ON DELETE CASCADE` and `per_kit numeric(14,3) NULL`. A
    child line has its own id, so it reserves, is delivered, picks lots (O) and is
    cancelled like any goods line; its quantity is the kit line's × `per_kit`. The kit
    line itself is not reservable (`reservable` answers false for a line with children)
    and carries the price; the children are priced 0 and not invoiced on their own.
  - **The composition is copied onto the order** when the kit line is added, from a new
    ungated structural read provided by Manufacturing:
    ```go
    // KitStructure answers, per kit variant, its default active kit BoM's
    // components exploded through nested kits (at most 3 levels): the variant
    // and the quantity per kit. It carries no cost or personal data: it is
    // ungated, and the consumer decides what to show. Read on the pool.
    type KitStructure interface {
        Kits(ctx context.Context, variantIDs []int32) (map[int32][]KitComponent, error)
    }
    type KitComponent struct {
        VariantID int32
        PerKit    string // exact decimal text, > 0
    }
    ```
    A later BoM edit does not reach an order's existing kit lines (the copy is the
    order's, as an MO's snapshot is). Changing the kit line's quantity rescales its
    children in the same transaction, and the reservation follows each child as an edit of
    a confirmed line does (`orders/stock.go`).
  - Without Manufacturing (a nil slot) a kit variant is an ordinary goods line.
  - The packing slip prints the kit with its children under it; the pick list prints the
    children only.
- **M146 — Availability** of a kit is the minimum over its child lines of available ÷ per
  kit, shown on the kit line.

## 13. Permissions, all of phase 2

| Key | Display (en / nb) | Delivery |
| --- | --- | --- |
| `inventory:expired-override` | Use expired lots / Bruke utgåtte partier | L1a |
| `inventory:lots-manage` | Manage lots / Administrere partier | L1b |
| `manufacturing:quality-manage` | Manage quality / Administrere kvalitet | Q |

Every new screen's reads use the existing keys (`locations-view` + `stock-view` in
Inventory; `manufacturing:access`). The host's permission labels (`apps.ts`'s admin
labels) gain the three, in both languages, each in its layer.

How the permissions are read, where revision 1 left it open:

- `inventory:expired-override` is read **by the caller, on the pool, before its
  transaction** (M62), never by the ledger.
- A **station principal** holds only `manufacturing:access` and `manufacturing:shop-floor`
  (M134), only through the router's `wrap` for operations marked `x-vantigo-station:
  allowed` (M135), and only for its own work centers (M131).
- `contracts.DeliveryTrace` refuses (`ErrForbidden`) a principal without `orders:access`
  (M90); `KitStructure` and `SalesOrderLines.Unreserved` are ungated reads whose consumers
  show only quantities.

## 14. Data model, by migration

**One schema per migration**, numbered from `00162` in the stack's order (the plan claims
them); a delivery that changes two schemas has two migrations.

| Migration | Delivery | Schema changes |
| --- | --- | --- |
| `00162_products_variant_tracking` | L1a | `products.product_variants`: `tracking`, `expiry_days`, `best_before_days` (M49) |
| `00163_inventory_lots` | L1a | `lots` (M52, with the derived `status`), `lot_holds` (M64); `balances.lot_id` and the new key, `movement_lines.lot_id`, `movement_lines.expired_override` (M55, M62); `counters` row `lot` |
| `00164_inventory_lot_attachments` | L1b | `lot_attachments`, `object_deletions` (M54) |
| `00165_inventory_lot_counts_transfers` | L2 | `count_lines.lot_id` and its key (M69), `transfer_lines.lot_id` (M70) |
| `00166_orders_delivery_lots` | O | `delivery_line_lots` with `chosen` (M75) |
| `00167_manufacturing_lots` | M | `boms.lot_pattern`, `orders.lot_pattern` (M80), `production_line_lots` (M82) |
| `00168_manufacturing_quality` | Q | `bom_operation_checks`, `work_order_checks`, `check_results`, `check_photos`, `deviations` (with `quarantine`), `object_deletions`; counter `deviation` |
| `00169_inventory_variant_rules` | Q | `variant_rules` (M100) |
| `00170_inventory_placement_multiple` | R | `placements.multiple` (M105) |
| `00171_manufacturing_replenishment` | R | `suggestions`, `replenishment_runs`, `replenishment_rules`; `ck_orders_origin_kind` gains `reorder` |
| `00172_manufacturing_routing` | F | `bom_operation_dependencies` (filled from the sequences), `work_order_dependencies` (filled likewise for open MOs), `send_ahead_quantity`, `work_orders.quantity_done`, `work_center_groups`, members, `bom_operations.work_center_group_id`, `work_orders.work_center_group_id` |
| `00173_manufacturing_bom_depth` | G | `bom_outputs`, `order_outputs`, `unbuilds` (with the chosen `lot_id`), `orders.split_from_id`, `orders.merged_into_id`, the amended `ux_orders_origin_line`; counter `unbuild` |
| `00174_identity_stations` | S | `stations` (with `token_expires_at`, `allowed_networks`, `require_pin_with_badge` default true), `user_pins`, `user_badges`, `operator_backoff`, `station_operator_sessions` |
| `00175_manufacturing_stations` | S | `station_work_centers`; `station_id` on `time_logs`, `productions`, `check_results`, `deviations` |
| `00176_manufacturing_kits` | K | `boms.kind` |
| `00177_orders_kits` | K | `lines.parent_line_id`, `lines.per_kit` (M145) |

T needs no migration: the trace reads L1a's indexes, and G's `unbuild` movement kind is
an application constant.

Every `down` drops what its `up` made; `00163`'s down restores the `(variant_id,
location_id)` primary key and fails if any balance has a lot (a down is for development,
not for data with lots).

## 15. Refusal codes, all of phase 2

| Code | Status | Rule |
| --- | --- | --- |
| `tracking_requires_goods`, `lot_required`, `lot_not_allowed`, `lot_unknown`, `serial_quantity`, `serial_first_invalid`, `lot_dates_differ`, `lot_pattern_invalid` | 400 | M49, M56, M57, M80 |
| `tracking_held` | 409 | M51 |
| `tracking_unavailable` | 409 | M49; L1a to M only |
| `lot_expired`, `lot_blocked` | 409 | M56, M62 |
| `hold_not_inventorys` | 409 | M64 |
| `attachment_too_large`, `too_many_attachments` | 400 | M54 |
| `attachment_type` | 415 | M54 |
| `storage_not_configured` | 503 | M54 |
| `picks_mismatch` | 400 | M75 |
| `trace_too_large` | 400 | M89, M92 |
| `check_limits_invalid`, `photo_required`, `inspection_requires_tracking` | 400 | M95, M97, M100 |
| `checks_incomplete`, `check_not_open`, `deviation_closed` | 409 | M98, M103 |
| `operation_cycle`, `operation_unknown`, `work_center_not_in_group` | 400 | M112, M119 |
| `work_order_started`, `work_order_member_fixed` | 409 | M119 |
| `output_is_product`, `output_is_component`, `cost_share_over`, `unbuild_too_much`, `split_sum`, `merge_mismatch` | 400 | M120, M123–M125 |
| `order_has_work` | 409 | M124 |
| `operator_sign_in_failed`, `station_unknown`, `operator_session_ended` | 401 | M133, M134 |
| `station_scope` | 403 | M131, M135 |
| `operator_session_unknown` | 404 | M136 |
| `pairing_code_invalid`, `pin_too_simple` | 400 | M129, M132 |
| `too_many_operators`, `station_has_session` | 409 | M133, M134 |
| `kit_not_manufactured` | 400 | M144 |

A line's code is a line failure in the module's problem shape (Inventory's `lines`,
Orders' line failures, Manufacturing's `lines`); a `StockRefusal`'s code is passed through
by Orders and Manufacturing as they pass phase 1's, mapped to the document's line by the
refusal's `Index` (M67).

**What is allowed without a code**, where a reader might expect one: an unbuild consuming
a held or expired lot (M123); a move, count, adjustment or transfer of a held lot (M56);
switching an operator out with the station cookie alone (M136).

### Lock orders, all of phase 2

| Writer | Order |
| --- | --- |
| Inventory's postings (receipt, move, adjustment, count, transfer) | key read; `LockVariants`; lot counter; new lots; named lots (`FOR SHARE`, `FOR NO KEY UPDATE` for a serial increase); header; locations `FOR SHARE`; balances (variant, location, lot `NULLS FIRST`) — M58 |
| A hold or release (lot page, block with downstream, a module's `HoldLots`/`ReleaseLotHolds`) | the caller's rows; the lots `FOR NO KEY UPDATE` in id order; the holds — M64, M92 |
| A reserve | `LockVariants`; `LockSiteBalances` (lot `NULLS FIRST`), or the (variant, site) advisory lock if M65b chooses it; reservations |
| A production (M) | D1 M26a 1–6 (variants, MO, re-read, work orders, center, logs); 7a `EnsureLots`; 7b `HoldLots`; 7c `Produce`; once retried on 40P01 — M86 |
| A check result, a deviation (Q) | `LockVariants`; the MO; the work order; the check row `FOR UPDATE`; the deviation; the ledger — M97, M99 |
| A plan, a drag (F), a split or merge (G) | the schedule advisory locks of every center touched (successors' and group members' included), in id order; then D1 M26a — M115, M119, M127 |

## 16. Rounding

D1 §15 stands: quantities half-up to three decimals, amounts exact and rounded once to two
decimals, percentages to one. Additions:

- FEFO picks never split a quantity finer than three decimals; a serial pick is 1.
- Proportional splits (unbuild, by-products, M121, M123) round each part half-up and put
  the remainder on the last part, so the parts sum exactly.
- Measures are kept to four decimals and judged against the limits unrounded.
- Projected stock (M107) is computed exactly from the text figures.

## 17. Live topics

- Inventory declares its first topics in L1b: `inventory.lot:<id>` (a lot's holds, files,
  stock) and `inventory.lots` (the list), notified on the writing transaction (the K-D
  rules). The lot page and list refetch on them.
- Manufacturing: `manufacturing.deviations` (Q), `manufacturing.suggestions:<siteId>` (R);
  the existing work-center, order and schedule topics carry F's readying and G's splits.
- **Stations have their own path (S).** The live handler (`srv/live/handler.go`)
  authenticates only a session today (`Access.Check` with `RuleSession`), and a browser
  WebSocket cannot set `Authorization`, so the operator token cannot reach it. The
  handler gains a **station path**: a handshake with the station cookie and no session
  cookie is resolved by a new `Access.CheckStation(r)` into the station (revoked,
  expired or outside its allowlist is 401 `station_unknown`).
  - A station socket may subscribe **only** to `manufacturing.work-center:<id>` for a
    center of its own (`station_work_centers`) and `manufacturing.work-order:<id>` for a
    work order at one of them; Manufacturing's `Authorize` for those two kinds reads the
    station from the context (`live.StationID(ctx)`) instead of a user's permission, and
    every other topic refuses a station.
  - The 60-second re-check (L11) re-checks the station instead of a session: a revoked or
    expired station closes the socket with 4401, and the subscriptions are authorised
    again against the current work centers.
  - Presence on a work order from a station socket shows the station's name, not an
    operator; events carry ids only, as everywhere, and the page refetches with the
    active operator's token.
  - The connect rate limit and the per-socket cap are kept per station id.

## 18. Documentation, per delivery

Every bilingual page changes in `en` and `nb` together; then `go generate ./...` in
`apps/server`.

| Delivery | Pages |
| --- | --- |
| L1a | `reference/inventory.md` (the lot, holds and the derived status, the key, the engine's rules and lock order, usable on hand, `Today` and the override, `AutoLot`, the contract changes, codes; which M65b lock was built); `reference/products.md` (the fields, the third purpose, the temporary `tracking_unavailable` guard); `contributing/module-boundaries.md` (rule 20's third purpose, rule 24's new calls, order and *no contract call inside the caller's transaction*); `reference/orders.md` and `reference/manufacturing.md` (the callers pass `Today`, `ExpiredOverride`, no lot; reserve's `today`); `user/inventory.md` (available now leaves out held and expired lots — what a person sees change in L1a) |
| L1b | `user/inventory.md` (lots on receipts, moves, adjustments; the lots list and page; files; holds; expired lots; *of which on hold*); `reference/inventory.md` (the lot reads, holds endpoints, attachments, live topics, lookup); `admin/object-storage.md` (the `inventory-lots` scope); `user/products.md` (the stock panel's lots) |
| L2 | `user/inventory.md` (counts, transfers, import with lots; expiring; labels); `reference/inventory.md` |
| O | `user/orders.md` (picking lots, the PDFs); `reference/orders.md` |
| M | `user/manufacturing.md` (output lots, serials, consumed lots, the pattern); `reference/manufacturing.md`; `user/products.md` (tracking and the offsets: the select appears); `reference/products.md` (the guard removed) |
| T | `user/inventory.md` (tracing, blocking, recall, GS1 labels); `reference/inventory.md` (the graph, `DeliveryTrace` use, the gates); `reference/orders.md` (`DeliveryTrace`); `user/manufacturing.md` (the *Trace* link); `contributing/module-boundaries.md` (rule 12's new provider) |
| Q | `user/manufacturing.md` (checks, the floor, deviations); `user/inventory.md` (inspection on receipt); `reference/manufacturing.md`, `reference/inventory.md`; `admin/manufacturing.md` (the quality permission, photo storage); `admin/object-storage.md` |
| R | `user/manufacturing.md` (suggestions); `user/inventory.md` (the multiple); `reference/manufacturing.md` (projected, the job); `reference/inventory.md` (`Placements`); `reference/orders.md` (`SalesOrderLines.Unreserved`); `admin/manufacturing.md` (the nightly job, automatic creation) |
| F | `user/manufacturing.md` (dependencies, send-ahead, groups); `reference/manufacturing.md` (readying, planning) |
| G | `user/manufacturing.md` (by-products, unbuild, split, merge); `reference/manufacturing.md` |
| S | `admin/authentication.md` (stations, pairing, the 90-day token, the network allowlist, PIN pepper and badge policy, backoff and alerts, idle, audit, *a browser is a station or a person's*); `user/manufacturing.md` (working at a station); `user/workspace-administration.md` (registering a station); `user/getting-started.md` (setting a PIN); `reference/manufacturing.md` (the station scope, the work-center check); `contributing/module-boundaries.md` (the `x-vantigo-station` extension, `ViaStation`, *Check answers a station only to `wrap`*); `contributing/platform-packages.md` (the live handler's station path, which documents `srv/live`) |
| K | `user/orders.md` (kit lines and their children), `user/manufacturing.md`, `reference/orders.md` (child lines), `reference/manufacturing.md` (`KitStructure`), `contributing/module-boundaries.md` (the new ungated provider) |

A delivery that adds a navigation entry or a spotlight action also updates `user/index.md`
(en, nb) — the lesson of phase 1 C. ROADMAP.md's Manufacturing section marks each delivery
as it lands.

## 19. Edge cases

- **Tracking turned on with a draft document holding untracked lines** (a delivery, a
  transfer, a count): the post refuses `lot_required`; the screen offers the lot column
  once the line is edited. Nothing converts silently.
- **A lot of a serial variant received again after it left** (returned goods): the lot
  exists with zero stock, so the receipt adds 1 to it; a second receipt while it holds 1 is
  `serial_quantity`.
- **Two receipts creating the same lot number at once:** the unique index serialises them
  at step 4 (M58); the second reads the first's lot after it commits.
- **Two receipts of one serial at two locations at once:** both take the lot `FOR NO KEY
  UPDATE` (M53); the second sees the first's 1 and is `serial_quantity`.
- **A blocked lot partly reserved by an order:** the reservation is per site and stays;
  available drops by the held quantity and may go negative (M63); the delivery's
  proposal skips the lot; if no other stock exists the line is short.
- **A lot held by a deviation and blocked by a recall:** closing the deviation releases
  its hold; the lot stays `blocked` until the recall's hold is released (M64).
- **A first-piece check fails before any output:** the deviation holds nothing yet; the
  MO's first production makes its lot under a quarantine hold from it (M99).
- **The expiry passes between proposal and post:** the post refuses `lot_expired`; the
  delivery page proposes again.
- **Day boundaries:** *today* is the workspace zone's date at the posting's clock; the
  frontend shows the same date from `GET /meta`.
- **A trace through a by-product (G):** a production movement with two produce lots gives
  both the same parents.
- **A trace through an unbuild:** an unbuild movement (kind `unbuild`) is a reverse edge
  and is never expanded (M88): the finished lot's usage shows *Unbuilt by UB-n* as an
  end, and the restored component lots show *Returned by unbuild UB-n*; a recall of the
  finished lot does not cascade into them, and the components' own origin is unchanged.
- **Unbuilding a recalled item:** allowed; the restored components get a quarantine hold
  from the unbuild (M123).
- **Kits (K) and lots:** components carry lots; the kit itself never does.
- **A station revoked while operators are mid-action:** their next request is 401
  `station_unknown`, and the station's socket closes at the next re-check; running time
  logs stay running (M136).
- **A person signs in on a paired tablet:** the station page answers `station_has_session`
  and asks them to sign out; their own pages work as in any browser (M138).
- **A merge of a make-to-order MO with a manual one:** refused `merge_mismatch` (M125).
- **An operator whose `shop-floor` permission is removed** while signed in at a station: the
  next request is 403, as a session's would be; the chip shows *Signed out*.
- **Clock skew on tablets:** idle time is the server's `last_activity_at`.

## 20. Testing

- **Ledger (L1a):** the key with and without a lot (two concurrent postings to a new
  no-lot pair still serialise, `NULLS NOT DISTINCT`); every M56 code; serial at most 1
  across locations, **including two concurrent increases of one serial lot at two
  locations** (the second refused); a new lot created twice concurrently; the extended
  lock order under a hold racing a posting (pool of four); `EnsureLots` replay; a repeat
  `EnsureLots` and a repeat receipt under one number with no dates stated (accepted) and
  with a differing stated date (`lot_dates_differ`); `allowExpired` with and without
  `ExpiredOverride`; a zero `Today` failing.
- **Rule 24 (L1a):** with Inventory's contract-call hook installed, a `StockLedger` call of
  every kind (Issue, Produce, Reserve, EnsureLots, HoldLots, ReleaseLotHolds) made inside
  another module's transaction records **no** contract call; Orders' and Manufacturing's
  harnesses prove they read `Today` and the override before their transactions.
- **Holds (L1a):** two sources on one lot, one released, the status stays; derived status
  for every mix; `HoldLots` idempotent per source and kind.
- **Usable on hand (L1a):** a reserve refused by a held lot and by an expired one; a hold on
  reserved stock leaving available negative; `Stock.Available` and the low-stock list
  agreeing; `Stock.OnHand` unchanged.
- **`AutoLot` and re-proposal (L1a, O, M):** auto picks FEFO under the locks, skips held
  and expired lots, answers its picks; a stale proposal re-proposed once, a second
  refusal answered.
- **Serial reserve load (L1a, M65b):** a reserve against 10,000 serial balances at one
  site, timed; the advisory-lock variant's reserve/issue race if it is chosen.
- **Products:** a tracking change refused with stock, reserved and in transit, allowed at
  zero; Service resets tracking; **L1a's guard** refusing `lot` and `serial`
  (`tracking_unavailable`), and M's removal of it.
- **Contracts:** `ProposeLots` table tests (FEFO, FIFO, skips of held and expired lots,
  shortage, serials); `StockLineRefusal.Index` mapped back on a multi-pick line.
- **L1b:** the lots list filters (status, hold cause), the lot page's holds and release
  (an Inventory hold released; a deviation's refused `hold_not_inventorys`); attachments
  and the deletion worker on a pool of one; live topics.
- **L2:** a count with two lots of one variant; a transfer round trip per lot; the import's
  lot columns; the expiring list's boundary day.
- **O:** the proposal over two lots and two locations; a post with an expired lot,
  refused and overridden; a proposal made stale by another delivery, re-proposed; the
  PDFs' lot columns (golden text).
- **M:** `{MO}` adding to one lot across two productions with defaulted dates; `L{YYMMDD}-{N}`
  making two; serials one per unit; consumed rows summing; `AutoLot` when rows are
  omitted; a deadlock retried once (two productions and a hold across shared lots, pool
  of four).
- **T:** a three-level graph both ways; *already traced*; the 2,000-node bound; the
  delivery read refused (`ErrForbidden` without `orders:access` → no deliveries shown; no
  `customers:view` → no names); the block re-walking inside its transaction and catching
  a lot produced after the summary; GS1 element strings (GTIN-13 padded, FNC1 placement,
  no GTIN fallback) decoded back with the encoder's table.
- **Q:** a failed measure opening a deviation and holding the lots so far; **a first-piece
  failure before any output, then a production whose new lot is held**; a production
  refused while a predecessor's check or the output work order's first-piece check is
  missing; Finish refused on a work order's own missing check; two results recorded at
  once for one check (the check row lock); close releasing only the deviation's holds
  (a recall's block stays); an inspection receipt into an existing lot adding a hold.
- **R:** projected with MO output, a `reorder` draft counted, MTO output and its sales line
  both left out, unreserved MO demand and unreserved sales demand (confirmed without
  reserving); rounding to the multiple; the worker on a pool of one; automatic creation
  off by default and skipping a variant with an open `reorder` MO.
- **F:** readying a diamond graph; send-ahead readying and planning; group choice by
  earliest finish, ties; a drag outside the group refused; a drag that moves a successor
  on another center taking that center's advisory lock (relock when it was missed); a
  start at another member after the member was fixed (`work_order_member_fixed`).
- **G:** by-product cost split, and a by-product that produced nothing; a by-product that
  is a component refused, and a cycle through outputs; produce lines with equal `LineID`s
  for two variants; unbuild of one chosen lot restoring only from its productions; the
  trace not expanding an unbuild; an unbuild of a blocked lot quarantining the restored
  lots; split and merge of a planned MO unplanning it under the advisory locks;
  re-reserving; mixed origins refused; the amended MTO index.
- **S:** pairing code single use and expiry; pairing signing a session out; the 90-day
  token expiry and the network allowlist; PIN hashing with the pepper and the dummy-hash
  timing path; per-station rate limit; per-(station, user) backoff, its doubling and its
  reset, and the alert; idle and absolute expiry; an operator token used with another
  station's cookie refused; `station_has_session`; the permission intersection (an Owner
  at a station cannot read customers); `station_scope` on every non-allowed operation (a
  test walks the whole contract) and on every allowed one for a work center not the
  station's; `Access.Check` refusing station credentials outside `wrap`; **the station
  page rendered as a station principal against the real router, failing on any request
  outside the allowed set**; the live station path (subscribe to own center allowed,
  another center and any other topic refused, revocation closing the socket); switch out
  with the cookie alone; the cookie flags; audit rows on each event; the lint rule.
- **K:** a kit line creating child lines from `KitStructure`; each child reserving and
  delivering with lots; a quantity change rescaling the children; a nested kit.
- **Frontend:** the lot column in the line editor; the receipt's serial generator; the
  delivery's lot table; the production dialog's lots; the trace tree's collapse; the holds
  panel; the checks panel; the suggestions selection; the station's chips and PIN pad,
  with the token never written to storage.
- **Integration:** `everyModule()` unchanged (no new module); the Assistant's prompt
  `promptVersion` bumped if new routes join `appRoutes` (`/inventory/lots`,
  `/inventory/trace`, `/manufacturing/deviations`, `/manufacturing/suggestions`,
  `/manufacturing/unbuilds`).

## 21. Later, and what each waits for

| Item | Waits for |
| --- | --- |
| Assigning lots to existing stock in place | Demand; the import is the path (reading 4) |
| Output expiry from the shortest component expiry | Demand (food) |
| Random and periodic sampling, quality teams, root cause | Phase 3 |
| Rework MOs from a deviation | Phase 3 |
| Recall campaigns with contact logs and returns | Phase 3 |
| Packages and SSCC, ZPL printing | Phase 3 |
| Lot rules per bin, removal strategy per location | Phase 3 |
| Parallelising one operation across a group | Phase 3 |
| Auto-computed standard times | M42's history over a season |
| Purchase suggestions that order | Purchasing |
| Subcontracting | Purchasing |
| MPS and forecasts | Phase 3 |
| Kits, if K is not built | After phase 2 |
| Product images on stations (short-lived signed URLs) | Demand from the floor |

## 22. Open questions

All readings were defaulted (R §9). The ones whose overturn would cost the most:

- **Reading 2** (quarantine as a hold on the lot, revision 2): a failure location would
  change M56's refusal into a location rule and M99's holds into moves.
- **Reading 3** (no lot on reservations): lot-level reservation would add `lot_id` to
  `inventory.reservations` and change `Reserve`'s signature.
- **Reading 6** (the trace in Inventory): moving it to Manufacturing would leave
  wholesalers without it.
- **M133's badge default** is now the stricter one (a badge needs the PIN too); a shop that
  wants Monitor's card-only terminals turns it off per station.
- **M98's gate on the output work order** (revision 2's reading of the critic's
  resolution): its `each_work_order` checks gate its Finish, not the first production; a
  stricter reading would make them first-piece checks in effect.

## 23. Revision log

### Revision 2, 2026-11-12 — after a critic review of revision 1

The critic's verdict on revision 1 was *revise*: one critical finding, fourteen major, and
fifteen minor, plus two open points. Every finding is answered below by its number; the
decisions named were rewritten in place.

| # | Finding | What changed |
| --- | --- | --- |
| 1 | Critical: the ledger called contracts (`HasPermission`, `BusinessCalendar`) inside the caller's transaction, breaking rule 24 and Inventory's `noteContractCall` rule | `StockIssue`, `StockProduction` and `EnsureLots` carry `Today` and `ExpiredOverride`, decided by the caller on the pool before its transaction; `Reserve` gains `today`; `NewLot.ProductionDate` is required; Inventory's own handlers read the zone and the permission before their transactions; rule 24 restated (§1.2, M62, M63, M67, M78, M80); a test proves no contract call inside a caller's transaction (§20) |
| 2 | Serial ≤ 1 broken by two concurrent increases at two locations | A line increasing a serial lot takes the lot `FOR NO KEY UPDATE` and sums all its balances under it (M53, M58 step 5); concurrency test |
| 3 | `lot_dates_differ` on ordinary repeats with defaulted dates | Only dates the body states are compared; defaults apply on creation only; `EnsureLots` ignores nil dates for an existing lot (M57, M67, M80) |
| 4 | Held and expired stock still counted as available | *Usable on hand* defined and used by `Reserve`, `ReserveForProduction`, `Stock.Available`, `Stock.Placements` and the low-stock list; on hand unchanged; screens show the held quantity (M63, M106, M107) |
| 5 | The quality gate had nothing to attach to (productions are per MO) | The production is gated on every check of the output work order's predecessors and the output work order's first-piece checks; Finish on a work order's own checks; first-piece also gates F's Register quantity; Q stays before F, *precedes* by sequence until F (M98) |
| 6 | Closing a deviation could undo a recall's block | A single status replaced by `inventory.lot_holds`; status derived; a release removes only its own hold; `SetLotStatus` replaced by `HoldLots` and `ReleaseLotHolds` (M52, M61, M64, M67, M92, M99) |
| 7 | No output lot to quarantine at a first-piece failure | An open quarantining deviation holds every lot the MO makes or adds to afterwards (M80, M86 step 7b, M99) |
| 8 | Unbuild broke genealogy | Restore only from the productions of the chosen finished lot; unbuild movements (kind `unbuild`) are reverse edges, not expanded and not cascaded; unbuild may consume held lots and quarantines the restored components when it does (M67, M88, M123, §19) |
| 9 | Stations could not receive live updates | The live handler gains a station path: the station cookie alone subscribes to its own centers' and work orders' topics; re-check per station (§17, M130, M140) |
| 10 | Station pages called endpoints outside the allowed set | Every request enumerated; user, settings and catalogs from the station context; images omitted; the lot picker served by the production draft; a test renders the station page as a station principal (M82, M135, M140, §20) |
| 11 | Credential precedence; operator token not tied to its station | Station credentials take precedence; station requests refuse while a session cookie is present (`station_has_session`); pairing signs the session out; `Check` verifies the operator session's station (M129, M134, M138) |
| 12 | Split, merge and new drags skipped the schedule advisory locks | Split and merge run through `withScheduledOrders` with every center involved and unplan the MOs; plans and drags pre-lock successors' centers and group members (§1.2, M115, M119, M124, M125, M127) |
| 13 | Replenishment counted wrongly | `reorder` drafts count as supply; MTO output and its line's demand both left out; automatic creation skips a variant with an open `reorder` MO; unreserved sales demand read from Orders (M107, M107a, M109, M111) |
| 14 | L1 too big | Split into L1a (schema, engine, contracts, receipt/move/adjustment API, callers pass no lot) and L1b (lot screens, attachments and the deletion worker, live topics, lookup, the host's stock panel); migrations renumbered, one schema each (§1, §2, §14, §18) |
| 15 | Kit reservations impossible (one variant per reservation line; `Production` gated) | Each kit component is its own child order line; the composition is copied from an ungated `contracts.KitStructure` (M145, M146, §14) |
| 16 | Refusals could not name the line | `StockLineRefusal` gains `Index`, `LotID`, `LocationID` (L1a, M67) |
| 17 | Lock-order gaps | The lot counter, lots before header and locations for every writer, `NULLS FIRST` in `LockSiteBalances`, a production retried once on 40P01 (M58, M86, §15's lock orders) |
| 18 | FEFO proposals race | `AutoLot` lines picked by Inventory under its locks; proposed picks re-proposed once (M65a, M78, M82) |
| 19 | Serial balance rows multiply under reserve's lock | A 10,000-serial reserve test, and the (variant, site) advisory lock if it is too slow (M65b) |
| 20 | PIN brute force and lock-out DoS | Escalating backoff per (station, user) instead of disabling; an admin alert; a pepper; an optional network allowlist; PIN required with a badge by default; a 90-day station token (M129, M132, M133) |
| 21 | Stations' work centers enforced only by the picker | The handlers check the center for a station principal (M131) |
| 22 | `Check` answered station principals everywhere | Only to the router's `wrap`, for operations marked `x-vantigo-station` (M134) |
| 23 | Check results and holds unlocked | The check row `FOR UPDATE`; the result → deviation → hold path under `LockVariants` first, order stated (M97, M99, §15) |
| 24 | By-product gaps | Zero produced moves the share to the main product; the MO's output location; a by-product may not be a component, the cycle check covers outputs; produce lines keyed by (VariantID, LineID) with ids from `order_outputs` (M67, M120–M122) |
| 25 | Merging mixed origins | Refused (`merge_mismatch`) (M125) |
| 26 | The interim state after L1 without O and M | A server-side guard (`tracking_unavailable`) and no select until M, which removes both (§1, M49) |
| 27 | Receiving into an existing lot skipped inspection; the serial flag after a tracking change | A new inspection hold on the existing lot; a lot's `serial` is history, the serial rule follows the variant (M52, M53, M100) |
| 28 | The re-snapshot on a member change amended D1 M28a silently | Said so (M118) |
| 29 | The block's downstream walk could be stale | Run again inside the block's transaction (M92) |
| 30 | Missing codes | An unbuild from a held lot is allowed (no code); switching out takes the station cookie alone (`operator_session_unknown`, 404); a start at another member once fixed (`work_order_member_fixed`, 409) (M119, M136, §15) |
| open | Hold changes' lock; `DeliveryTrace`'s refusal | Holds take the lot `FOR NO KEY UPDATE` (M64); `DeliveryTrace` refuses with `ErrForbidden` (M90) |

**Where revision 2 chose among the critic's options or read a resolution narrowly:**

- **#4** needed `today` in `Reserve` and `ReserveForProduction` too, since the reserve judges
  expiry inside the caller's transaction and #1 forbids a calendar call there.
- **#5**: the output work order's `each_work_order` checks gate its Finish, not the first
  production (§22).
- **#6**: the holds table's columns are `kind` (`quarantine`, `block`) and `cause` (the
  former reasons), so the two are not one overloaded *reason*.
- **#8**: the unbuild's movement kind is `unbuild`, set by a `StockProduction.Unbuild` flag,
  rather than Inventory reading the document's kind.
- **#10**: images are omitted on stations; signed URLs are Later.
- **#13**: the unreserved demand is a third method on the existing consumer-gated
  `contracts.SalesOrderLines`, not a new `SalesDemand` slot: the rules are the same and no
  new `Deps` slot or registry entry is needed.
- **#30**: switch out needs the station cookie only, because the operator tokens are gone
  after a reload and a chip must still close.
