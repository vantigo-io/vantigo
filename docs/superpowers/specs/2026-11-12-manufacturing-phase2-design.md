# Manufacturing phase 2 — lots, traceability, quality, replenishment, routing, BoM depth, stations

Revision 1, 2026-11-12. This spec builds on the research
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
| **L1** | Tracking mode (Products); lots, the lot-aware ledger, receipts, moves, adjustments, lots list and page, attachments, lookup, FEFO, expiry; lot-aware contracts | Products, Inventory | — |
| **L2** | Counts, transfers and the opening import with lots; the expiring list; lot labels | Inventory | L1 |
| **O** | Deliveries pick lots, FEFO-proposed; lots on the pick list and packing slip | Orders | L1 |
| **M** | The output lot of a production, serials, consumed lots per component | Manufacturing | L1 |
| **T** | The trace screen, export, blocking with the downstream list, GS1-128 labels | Inventory (screen), Orders (a read) | L1, O, M |
| **Q** | Quality checks on operations, results, deviations, quarantine; inspection on receipt | Manufacturing, Inventory | M |
| **R** | Replenishment: reorder rules, projected stock, suggestions, MOs from them | Manufacturing, Inventory (a read) | — |
| **F** | Operation dependencies, send-ahead, work center groups | Manufacturing | — |
| **G** | By-products, unbuild, split and merge | Manufacturing | M |
| **S** | Shared stations: devices, operator PIN and badge, the station scope | Identity, Manufacturing | — |
| **K** | Kits, optional | Manufacturing, Orders | O |

**Not in this phase** (R §7): subcontracting, MPS, random or periodic sampling, packages
and SSCC, per-bin lot rules, ZPL, recall campaign objects, parallelising across a group,
auto-computed standards; kits if K does not fit.

### 1.1 Who owns what

- **Records of stock and lots are Inventory's**: lots, their dates, status and files, the
  lot on every balance and movement line, the trace screen (R §9 reading 6) and lot
  labels.
- **Capture is the document's**: Orders chooses the lots a delivery takes; Manufacturing
  chooses the lots a production consumes and names the lot it produces. Both pass the lot
  to Inventory through `StockLedger` on their own transaction (rule 24).
- **The tracking mode is Products'**, changed only when Inventory's holder says the
  variant holds nothing (rule 20).
- **Quality and suggestions are Manufacturing's**; quarantining a lot is a `StockLedger`
  write Manufacturing makes on its own transaction.
- **Stations and PINs are Identity's**; which work centers a station serves is
  Manufacturing's.

### 1.2 The rules this spec keeps

- **Rule 24** (Inventory on the caller's transaction): every new ledger call — `EnsureLots`,
  `SetLotStatus` — runs on the caller's `tx`, after `LockVariants` and the caller's rows,
  refuses only through `*StockRefusal`, and is composed whatever `MODULES` says.
- **Rule 12** (the provider is the gatekeeper): `contracts.DeliveryTrace` (T) is
  provider-gated on `orders:access`; Inventory shows customer names only to a caller who
  holds `customers:view`.
- **Rule 23** (handlers write their own schema): phase 2 adds no event handler that
  reads a contract. The replenishment job (R) is a lease worker, not a handler.
- **Rule 20** (holders inside Products' transaction): the tracking change is a third
  holder purpose.
- **Rule 21** (one locking read): `LockVariants` stays the only locking contract read; the
  new ledger calls lock Inventory's rows inside Inventory.

## 2. Decisions — L1, lots in Products and Inventory

### Products

- **M49 — The tracking mode.** `products.product_variants` gains:
  - `tracking text NOT NULL DEFAULT 'none' CHECK (tracking IN ('none','lot','serial'))`;
  - `expiry_days integer NULL CHECK (expiry_days BETWEEN 1 AND 3650)`;
  - `best_before_days integer NULL CHECK (best_before_days BETWEEN 1 AND 3650)`.

  The variant request and response gain `tracking`, `expiryDays` and `bestBeforeDays`.
  Only a **goods** product's variant may be tracked (`tracking_requires_goods`, 400). A
  product's change to Service sets every variant's tracking to `none` in the same
  transaction: the holders have already said it holds no stock.
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
    (R reading 1);
  - `supplier_lot varchar(40) NULL`: the supplier's lot or the heat number of a 3.1
    certificate;
  - `production_date date NULL`, `best_before date NULL`, `expiry date NULL`;
  - `received_at timestamptz NOT NULL`: when stock first entered under the lot, the FIFO
    key;
  - `origin_module`, `origin_kind`, `origin_id` (text, NULL): the document that created
    it — a receipt movement, a production, an adjustment, an import;
  - `status text NOT NULL DEFAULT 'available' CHECK (status IN
    ('available','quarantine','blocked'))`, `status_reason text NULL`, `status_note
    varchar(1000) NULL`, `status_changed_at`, `status_changed_by_user_id`,
    `status_changed_by_display` (R reading 2);
  - `note varchar(2000) NULL`;
  - `sku`, `product_name` snapshots; `revision integer`; `created_by_user_id`,
    `created_by_display`, `created_at`, `updated_at`.

  Indexes: `(variant_id, expiry NULLS LAST, received_at, id)` (FEFO), `(status) WHERE
  status <> 'available'`, `(expiry) WHERE expiry IS NOT NULL`, `(lower(number))` (lookup).
  A lot is never deleted.
- **M53 — The serial rule.** A serial lot holds **at most 1** in total over every location
  (`serial_quantity`, a 400 line failure). A serial line's quantity is exactly 1; a line of
  quantity 3 for a serial variant is refused, and the receipt form generates three lines
  instead (M57).
- **M54 — Attachments** (`inventory.lot_attachments`): `id`, `lot_id`, `object_key unique`,
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

- **M55 — The balance key.** Migration `00163` (§10):
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
  | an issue or consume line takes from a lot in quarantine or blocked | `lot_blocked` (409) |

  - **Moves, adjustments, counts and transfers ignore the status and expiry**: stock on hold
    can be moved to a hold shelf, counted, written off or sent back. Only `issue` and the
    consume lines of a `production` are refused. A receipt into a blocked lot is refused
    too (`lot_blocked`): goods do not join a recalled lot.
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
    a supplier's lot number adds to it. Its dates are not changed; a body whose dates differ
    from the stored ones is refused (`lot_dates_differ`, 400) so a typo is seen.
  - **Default dates:** `expiry` = the receipt's date (in the workspace zone) + the
    variant's `ExpiryDays`; `bestBefore` likewise with `BestBeforeDays`; each only when
    the variant has the offset and the body gives no date.
  - **Serials:** a receipt line may carry `serials: {first, count}` (count 1–500) or
    `serials: {numbers: [...]}`. The server expands it into one engine line per serial of
    quantity 1, numbered by incrementing the trailing digits of `first` and keeping their
    width (`SN0099` → `SN0100`); a `first` with no trailing digits is
    `serial_first_invalid` (400). The expansion counts against the 500-line cap.
  - The lots are inserted at step **4a** of the lock order (M58), each with `INSERT … ON
    CONFLICT (variant_id, lower(number)) DO NOTHING RETURNING id`, then read back on a
    conflict. A new serial whose number exists and holds stock is `serial_quantity`.
- **M58 — The lock order**, Inventory's posting order (`posting.go`'s comment) extended;
  the comment is rewritten with it:
  1. the `clientRequestId` read (unchanged);
  2. `ProductCatalog.LockVariants` (unchanged; a count's or transfer's own row first, as
     before);
  3. the movement's header claimed (unchanged);
  4. the locations `FOR SHARE`, in id order (unchanged);
  - **4a.** new lots inserted, in (variant id, lower(number)) order;
  - **4b.** every lot the posting names `FOR SHARE`, in id order — so a status change,
    which takes the lot `FOR UPDATE` (M64), waits for the posting or is waited for;
  5. the balances, created at zero and locked, in **(variant, location, lot)** order, with
     no-lot first.

  `StockLedger`'s doc comment states the extended order. Callers outside Inventory never
  lock lots: they hold `LockVariants`, then their own rows, then call the ledger, as rule
  24 says.
- **M59 — Receipts, moves and adjustments.**
  - A **receipt** line takes `lotId` or `lot` (M57), or `serials`.
  - A **move** line takes `lotId`: the lot leaves `from` and lands on `to` unchanged.
  - An **adjustment** line takes `lotId`, or `lot` for an increase. A set-mode line sets
    one (location, lot).
  - The forms (`inv/pages/new-receipt.tsx`, `new-move.tsx`, `new-adjustment.tsx` and the
    shared `LineEditor`) show a **Lot** column for tracked variants only: a combobox of the
    variant's lots at the site (from M63's read), *New lot…* on a receipt and an increase,
    and *Serial numbers…* on a receipt of a serial variant.
  - The movement page and list show the lot on each line, linked to the lot page.

### Inventory: screens and reads

- **M60 — The lots list** (`/inventory/lots`, nb *Partier*), in the navigation after
  *Stock*: number, SKU and product, supplier lot, expiry, best before, status, on hand in
  total. Filters: `q` (lot number, supplier lot or SKU), `variantId`, `siteId`, `status`,
  `expiresBefore`, `withStock` (default true). Cursor-paginated by (variant, number).
- **M61 — The lot page** (`/inventory/lots/{id}`):
  - the header: number, product, supplier lot, the three dates, status with reason, the
    origin linked to its document, note;
  - **Where it lies**: on hand per site and location;
  - **Movements**: every movement line of the lot, newest first, linked;
  - **Files**: the attachments, with upload and delete (`inventory:lots-manage`);
  - **Edit** (If-Match): supplier lot, the three dates and the note. The number and the
    variant never change;
  - **Set status** (M64);
  - from T, **Trace** and **Labels**.
- **M62 — Expiry.** A lot is **expired** when `expiry < today` in the workspace zone
  (`contracts.BusinessCalendar`). Best-before alone never refuses anything (food past its
  best-before may still be sold with a warning); it is shown.
  - An issue or consume line from an expired lot is refused `lot_expired` unless the line
    carries `allowExpired: true` **and** the principal in `ctx` holds
    `inventory:expired-override`. Inventory checks the permission itself
    (`contracts.HasPermission`), so no caller restates it; a call with no request (a
    worker) never overrides.
  - The movement records the override: `movement_lines.expired_override boolean NOT NULL
    DEFAULT false`.
- **M63 — Stock per lot.** 
  - `InventorySiteStock` rows (the product and variant stock reads) gain
    `lots: [{lotId, number, expiry, bestBefore, status, onHand}]` per location, in FEFO
    order (M65).
  - The product page's stock panel (`host/routes/products/$productId.tsx`) shows a lot table
    under each site for a tracked variant, with expired and on-hold lots marked.
  - The variant page (`inv/pages/variant.tsx`) shows the same and links each lot.
- **M64 — Lot status.** `POST /lots/{id}/status` (If-Match) `{status, reason, note}`:
  - `reason` is one of `inspection`, `deviation`, `complaint`, `recall`, `other`, required
    for `quarantine` and `blocked`;
  - takes the lot `FOR UPDATE`, writes the status columns and an `inventory.events` row
    (`entity_kind = 'lot'`, kinds `lot_created`, `lot_updated`, `lot_status_changed`,
    `lot_attachment_added`, `lot_attachment_removed`);
  - needs `inventory:lots-manage`.
- **M65 — FEFO and FIFO.** One ordering everywhere a lot is proposed or listed:
  `expiry ASC NULLS LAST, received_at ASC, id ASC` (R reading 5: with no expiry on any
  lot, it is FIFO by received date). The proposal skips lots that are not `available` and
  lots that are expired; it never splits a serial.
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
- **M66 — Lookup.** The lookup (`srv/inventory/lookup.go`) matches, after the location and
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
      Status     string     // "available" | "quarantine" | "blocked"
  }

  // IssueLine and ConsumeLine gain:
  LotID        int64 // 0 for none; required for a tracked variant
  AllowExpired bool  // M62: honoured only for a principal holding inventory:expired-override

  // ProduceLine gains:
  LotID int64 // 0 for none; a lot made by EnsureLots, required for a tracked variant

  // StockLedger gains (M80 uses it; Inventory's own receipts use the same code):
  // EnsureLots finds or creates each lot, inside tx, after the caller's rows and
  // before Produce, and answers the lot ids in the order given. A lot that exists
  // for the variant is answered as is (its dates must agree, StockLotDatesDiffer);
  // a new serial whose number holds stock is StockSerialQuantity. After a refusal
  // the caller rolls back, as after Produce.
  EnsureLots(ctx context.Context, tx pgx.Tx, src StockSource, lots []NewLot) ([]int64, error)
  // SetLotStatus sets a lot's status for another module's document (Q's deviation),
  // taking the lot FOR UPDATE inside tx, and records the source on the lot's event.
  SetLotStatus(ctx context.Context, tx pgx.Tx, src StockSource, change LotStatusChange) error

  type NewLot struct {
      VariantID      int32
      Number         string     // "" to generate L<n>
      SupplierLot    string
      ProductionDate *time.Time
      BestBefore     *time.Time // nil: production date + BestBeforeDays, when set
      Expiry         *time.Time // nil: production date + ExpiryDays, when set
  }
  type LotStatusChange struct {
      LotID          int64
      Status, Reason string
      Note           string
      ByUserID       *uuid.UUID
      ByDisplay      string
  }
  ```
  New codes in the refusal block: `StockLotRequired = "lot_required"`,
  `StockLotUnknown = "lot_unknown"`, `StockLotNotAllowed = "lot_not_allowed"`,
  `StockSerialQuantity = "serial_quantity"`, `StockLotExpired = "lot_expired"`,
  `StockLotBlocked = "lot_blocked"`, `StockLotDatesDiffer = "lot_dates_differ"`.
  - **Reservations stay per site, with no lot** (R reading 3): `Reserve`,
    `ReserveForProduction`, `Release` and `ReservationsFor` do not change.
  - **The callers change in L1** so that the stack compiles: Orders' `PostDelivery` and
    Manufacturing's `postProduction` pass `LotID: 0`, and a tracked variant on either is
    refused `lot_required` until O and M land. Phase 1's tests keep untracked fixtures.
  - The fakes (`srv/manufacturing/fakes_ledger_test.go`, Orders' tests, `modtest`) gain
    the two methods.
- **M68 — Permissions (L1).**

  | Key | Display (en / nb) | Gives |
  | --- | --- | --- |
  | `inventory:lots-manage` | Manage lots / Administrere partier | Editing a lot, its files and its status |
  | `inventory:expired-override` | Use expired lots / Bruke utgåtte partier | Issuing or consuming an expired lot, with the line's `allowExpired` |

  Reading lots needs `locations-view` and `stock-view`, as every stock read. Both keys are
  delegable; neither is sensitive. `GET /meta` gains `canManageLots` and
  `canOverrideExpired`.

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
  quantity numeric(12,3) > 0, allow_expired boolean default false
  unique (delivery_line_id, lot_id, location_id)
  ```
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
- **M78 — Posting.** `PostDelivery` builds **one `IssueLine` per lot row**, each with the
  order line's id as `LineID` (so the reservation is consumed as before), the row's
  location and `LotID`, and `AllowExpired`. Refusals map back to the delivery line and
  the lot row (`lot_expired`, `lot_blocked`, `insufficient_stock`). The posted delivery
  keeps the rows; nothing else changes in Orders' buckets.
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
    before `Produce`, then passes the lot id on the `ProduceLine`.
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
    request's `fromLocationId` or the MO's component location when set.
  - `splitConsumption` (`productions.go`:594) keeps its location split for untracked
    components and uses the lot rows for tracked ones: one `ConsumeLine` per row.
  - A tracked component sent without rows is proposed by the server at post (the same
    FEFO), so an API caller that does not care still posts; the production answer names the
    lots used.
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
- **M86 — The lock order** is D1 M26a with one step: after the MO (step 2) and the
  component re-read (step 3), Manufacturing calls `EnsureLots` (Inventory locks the lots
  inside, M58 4a–4b), then `Produce` (step 7). Two productions of one MO are already
  serialised by the MO row, so the shared `{MO}` lot is never inserted twice.

  The lots `EnsureLots` takes come before the locations `Produce` shares: no other writer
  holds a location and then waits for a lot (a status change takes only the lot, a
  location's archive takes no lot), so the order does not cycle.

## 6. Decisions — T, traceability, blocking and labels

- **M87 — The trace screen is Inventory's** (R reading 6): `/inventory/trace` (nb
  *Sporing*), reached from the lot page, the lots list, the lookup, Manufacturing's MO
  *Lots* tab and Orders' delivery page. It needs `locations-view` and `stock-view`.
- **M88 — The graph.** Built from Inventory's own records, read on the pool:
  - **nodes** are lots;
  - **edges** are production movements: in one movement of kind `production`, every lot on
    a consume line is a **parent** of every lot on a produce line. No genealogy table is
    kept; the movement lines with a lot are the record (R §4);
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
  // it answers nothing, not an error, to a principal without orders:access, and
  // nothing to a call with no request.
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
    movements (at most 1,000 a call). Without Orders, or without `orders:access`, the
    trace shows *Delivery* with the movement only.
  - Customer **names and contacts** come from `contracts.CustomerDirectory`
    (`Customers`, `ContactsForCustomer`) and are shown only when the caller also holds
    `customers:view`, which Inventory checks — the CustomerDirectory is a
    single-provider read with no gate of its own, so the consumer gates it as D1 M43a does.
- **M91 — Export.** `GET /trace.csv` and `GET /trace.pdf` with the same parameters: one row
  per node and end — depth, lot, SKU, product, quantity, date, document, supplier or
  customer (names only under M90's gate). The PDF uses the house PDF style
  (`labels.go`'s fonts) and states the date, the user and the direction, for
  Mattilsynet's *within hours* answer.
- **M92 — Block with the downstream list.** The lot page's **Block** (status `blocked`,
  reason `recall`, `complaint` or `other`, note) opens a dialog that first shows the usage
  trace's summary: the lots made from it still in stock, and the customers and deliveries
  that received it.
  - *Also block the lots made from it* (default on) blocks every downstream lot still
    holding stock, in one transaction, the lots `FOR UPDATE` in id order, each with its
    own event naming the root lot.
  - *Release* sets `available` again, one lot at a time.
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
  - Photos: `POST /work-orders/{id}/photos` (multipart, JPEG or PNG, ≤ 10 MB, metadata
    stripped with `srv/imagemeta` as Products' images are) into the storage scope
    `manufacturing-quality`, answering a `photoId` (`manufacturing.check_photos`: id,
    work_order_id, object_key, content_type, size_bytes, uploaded_by…, created_at). A
    photo is a quality record: it is kept when its result is superseded, and nothing
    deletes it in phase 2 (an upload never attached to a result within 24 hours is
    removed by the module's `object_deletions` ledger, Customers' shape). The admin page
    says what storage they take.
- **M98 — Finishing needs the checks.** **Finish** (D1 M29) and **Register production** at
  a work order are refused while a check of the right frequency has no result:
  `checks_incomplete` (409, naming them). A `fail` result does not block: it has made a
  deviation (M99), and the supervisor decides. `first_piece` checks block registering
  output, not starting.
- **M99 — Deviations** (`manufacturing.deviations`; nb *avvik*), numbered `DEV-<n>`:
  ```
  id, number, title varchar(200), description text (≤ 5000), order_id null, work_order_id null,
  check_result_id null, lot_id bigint null, variant_id int null,
  disposition text null (use_as_is | rework | scrap | return), status (open | closed),
  quarantined_lot boolean default false, opened_by…, opened_at, closed_by…, closed_at, closing_note,
  revision
  ```
  - **A failed check opens one** automatically, in the result's transaction, linking the
    MO, the work order, the result and the MO's output lot when the product is tracked.
  - A person opens one by hand from the MO page, the work order view (*Report a
    deviation*) or a lot page link (`orders-manage` or `shop-floor`).
  - **Quarantine:** when the check says `quarantine_on_fail`, or the person ticks *Put the
    lot in quarantine*, Manufacturing calls `StockLedger.SetLotStatus(quarantine,
    reason deviation)` on the same transaction, after its own rows (rule 24), and sets
    `quarantined_lot`.
  - **Close** needs `manufacturing:quality-manage`, a disposition and a closing note.
    Closing with `use_as_is` or `rework` releases a lot the deviation quarantined
    (`SetLotStatus(available)`) unless another open deviation also quarantined it;
    `scrap` and `return` leave it in quarantine and the page links to Inventory's
    adjustment form to write it off. Rework MOs are Later.
  - The **deviations list** (`/manufacturing/deviations`, nb *Avvik*, under Overview):
    filters by status, MO, product, lot, period; CSV.
  - Live: `manufacturing.deviations` (the list) and the MO's topic.
- **M100 — Inspection on receipt.** `inventory.variant_rules(variant_id pk,
  inspect_on_receipt boolean, updated_by…, updated_at, revision)`, Inventory's (R's brief:
  an Inventory-side flag), set on the variant page (`inventory:stock-adjust`).
  - Only a tracked variant may require it (`inspection_requires_tracking`, 400).
  - A receipt of such a variant creates (or adds to) its lots in **`quarantine`** with
    reason `inspection`. A lot that already exists and is `available` is not changed; the
    receipt's response warns.
  - **Release** is the lot status action (M64), `inventory:lots-manage`. The lots list
    filter `status=quarantine&reason=inspection` is the inspection queue, linked from the
    Inventory home page as *Awaiting inspection*.
- **M101 — The shop floor.** The work order view (D1 M31) gains a **Checks** panel, in
  sequence: first-piece checks at the top until recorded, each with large Pass / Fail
  buttons, a numeric pad for measures (the limits shown), or *Take photo* (the tablet
  camera through `<input type=file capture>`). The MO page's work orders tab lists the
  results.
- **M102 — Permissions (Q).**

  | Key | Display (en / nb) | Gives |
  | --- | --- | --- |
  | `manufacturing:quality-manage` | Manage quality / Administrere kvalitet | Closing deviations, releasing the lots they quarantined, editing a closed deviation's note |

  Recording results needs `shop-floor` or `orders-manage`; editing templates needs
  `boms-manage`.
- **M103 — Codes.** `checks_incomplete`, `check_not_open` (409: the work order is done or
  cancelled), `check_limits_invalid`, `photo_required` (400), `deviation_closed` (409).
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
  // low-stock rule reads (inventory phase 2 I56): on hand, reserved and incoming by
  // transfer. Manufacturing adds its own orders to it (manufacturing phase 2 M107).
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
  projected = onHand − reserved + incoming
            + Σ open MOs of the variant at the site (remaining = quantity − produced, ≥ 0)
            − Σ open MOs' unreserved component demand of it at the site
              (required − consumed − scrapped − reserved, ≥ 0)
  ```
  Reserved already counts the MO reservations, so only the unreserved part of MO demand
  is subtracted. Open is `confirmed` or `in_progress`; drafts are not counted. Sales demand
  is what Orders has reserved; unreserved sales lines are not counted (Katana's
  *expected* is Later).
- **M108 — The suggestion**, by I56's rule over `projected`:
  - low when `projected ≤ reorderPoint`; `target = maximum`, or `reorderPoint + 1`;
    `needed = target − projected`, rounded up to `multiple` when set;
  - **make** when the variant has a default active BoM, else **buy**;
  - **need-by** is today when `onHand − reserved` is already at or below the reorder point,
    else today + the BoM's `leadTimeDays` business days (buy: today);
  - **reason** is stored as figures and shown as a sentence: *Projected 12 is at or below
    the reorder point 20: on hand 30, reserved 25, incoming 0, in production 7, needed by
    orders 0.*
- **M109 — The job.** A lease worker `manufacturing.replenishment` (built through
  `module.Workers`, as `manufacturing.mto-worker` is):
  - runs nightly at 02:00 in the workspace zone, and on demand from `POST
    /replenishment/runs {siteId?}` (`orders-manage`, rate-limited to one a minute per
    workspace, 202 with the run id);
  - per active site: reads `Stock.Placements` and its own MOs on the pool, then in **its
    own transaction** replaces the site's open suggestions (`manufacturing.suggestions`)
    with the new ones; a suggestion acted on is kept;
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
  the member changes.
- **M119 — Choosing the member.**
  - **Plan** tries each active, unblocked member and takes the one with the **earliest
    finish**; ties go to the higher time efficiency, then the code. It takes the advisory
    locks of every member in id order (D1 M35a).
  - **The board** lets a group work order be dragged to another member's row
    (`work_center_not_in_group`, 400, for any other row).
  - **On the floor**, an unplanned group work order shows in every member's queue under
    *Group work*; starting it at a member sets that member. Once a log exists the member
    is fixed (`work_order_started`, 409, on a later drag).
  - **Cost** uses the actual member's snapshotted rates (D1 M39–M40).

## 10. Decisions — G, BoM depth

### By-products

- **M120 — Output lines.** `manufacturing.bom_outputs (id, bom_id cascade, variant_id,
  quantity numeric(14,3) > 0, cost_share numeric(5,2) ≥ 0, sort_order)`, at most 20 per
  BoM, goods only, never the BoM's own variant (`output_is_product`, 400). The shares sum
  to at most 100 (`cost_share_over`, 400); the main product takes the rest.
- **M121 — Snapshot and posting.** Confirm copies them to `manufacturing.order_outputs`
  (with `expected = quantity × MO quantity / outputQuantity`, rounded half-up to three
  decimals, `produced` 0). A production carries `outputs: [{outputId, quantity, lot?}]`,
  prefilled proportionally to the good quantity; each becomes another `ProduceLine` with
  `LineID` the output's id. A tracked by-product's lot follows M80 with the BoM's pattern.
- **M122 — Cost** (R reading 9). At close, the MO's actual cost (D1 M41: materials,
  machine and labour together) is split: each by-product carries `cost_share %` of it, the
  main product the rest; each part's cost per unit is its share ÷ its produced quantity.
  The estimate is split the same way. The production cost report gains a by-products
  section.

### Unbuild

- **M123 — An unbuild** (`manufacturing.unbuilds`, numbered `UB-<n>`; nb *demontering*)
  takes finished goods apart:
  ```
  id, number, order_id null, bom_id, variant_id, quantity > 0, lot_id null, site_id,
  from_location_id, to_location_id, movement_id, note, posted_by…, posted_at, client_request_id unique
  ```
  - **From a done MO:** quantity ≤ produced − already unbuilt (`unbuild_too_much`, 400).
    It consumes the finished lot(s) the MO produced and **restores the components the MO
    consumed, proportionally, to the same lots** (from `production_line_lots`), each
    component's figure rounded half-up to three decimals, the remainder on its last lot.
  - **Free-standing, with a BoM:** components by the BoM's lines (without scrap) for the
    quantity; a tracked component needs a lot named, existing or new
    (`lot_required`).
  - Posting is one `Produce` with `Document` `manufacturing/unbuild/<id>`: the finished
    item as a consume line, the components as produce lines. The movement kind stays
    `production`; the movement page shows the source as *Unbuild UB-n*.
  - Cost is not recomputed; the MO shows *Unbuilt n*. Needs `orders-manage`.
  - `/manufacturing/unbuilds` lists them; the MO page has **Unbuild**.

### Split and merge

- **M124 — Split.** An MO that is `draft` or `confirmed`, with **no production and no time
  log** (`order_has_work`, 409), splits into quantities summing to its own (`split_sum`,
  400; 2–20 parts):
  - the original keeps the first quantity; each other part is a new MO numbered as a
    backorder (`MO-12-2`, D1 M17b), with `split_from_id` set, the same BoM, origin, due date
    and locations;
  - a confirmed original releases its reservations and every part reserves again (D1
    M20a's two-pass), and its work orders are re-snapshotted for the new quantities;
  - the MTO unique index (`mig/00160`, `ux_orders_origin_line`) becomes `WHERE origin_kind
    = 'sales_order' AND backorder_of_id IS NULL AND split_from_id IS NULL AND status <>
    'cancelled'`, and `ForOrderLines` returns the split parts with the chain.
- **M125 — Merge.** MOs of the same variant, BoM and site, each `draft` or `confirmed`, with
  no production and no time log, and the same origin line or none (`merge_mismatch`, 400):
  - the one with the lowest number survives with the summed quantity and the earliest due
    date; the others are cancelled with `merged_into_id`;
  - their reservations are released and the survivor reserves again; its work orders are
    re-snapshotted.
- **M126 — Expected minutes are recomputed** on a split or merge. This amends D1 M28a's
  *never recomputed*, which assumed only a draft's quantity could change; here no time is
  logged, so nothing measured is lost.
- **M127 — Locking.** Split and merge lock the MOs `FOR UPDATE` in id order after
  `LockVariants` on every variant involved (D1 M26a), then their work orders, then call
  the ledger.
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
  pairing_hash bytea null, pairing_expires_at null, idle_minutes integer default 5
  CHECK (1..60), require_pin_with_badge boolean default false, created_by…, created_at,
  last_seen_at, last_seen_ip, revoked_at null, revoked_by…
  ```
  - **Registering** (`POST /identity/stations`, `identity:manage`) returns a one-time
    **pairing code**: 8 characters from Crockford base32, valid 15 minutes, stored as
    SHA-256 (`tokens.go`'s `hashToken`). It is shown once.
  - **Pairing:** the tablet opens `/station/pair` and enters the code; `POST
    /identity/station/pair {code}` (anonymous, rate-limited per IP: 10 per 15 minutes)
    checks the hash and the expiry, clears the pairing hash, mints a 256-bit **station
    token** (`newToken`), stores its hash, and sets the cookie `vantigo_station`:
    `HttpOnly`, `Secure`, `SameSite=Strict`, path `/`, max age 400 days.
  - **Revoking** (`identity:manage`) clears the token hash, sets `revoked_at` and ends every
    operator session of the station at once.
  - **Re-pairing** issues a new code and replaces the token.
- **M130 — What a station may do alone: nothing but sign operators in.** A request with
  only the station cookie may call the station's own endpoints (`GET
  /identity/station`, its operators list, operator sign-in and sign-out). Every module
  operation needs an operator (M133).
- **M131 — Which work centers.** Manufacturing keeps
  `manufacturing.station_work_centers (station_id uuid, work_center_id)`, set on the work
  center's page (`boms-manage`): the station's shop floor opens on those centers and its
  picker offers only them. Identity knows nothing of work centers.

### The operator credential

- **M132 — PIN and badge.** `identity.user_pins (user_id pk, pin_hash text, set_at,
  failed_count integer default 0, locked_until null, disabled_at null)` and
  `identity.user_badges (user_id pk, badge_hash bytea unique, set_at, set_by…)`.
  - **The PIN** is 6 digits, hashed with **argon2id with the password parameters**
    (`passwords.go`). Refused: all one digit, and ascending or descending runs such as
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
  - **Lockout per user:** 5 consecutive failures set `locked_until = now + 15 min`; 10
    failures within 24 hours disable the PIN (`disabled_at`) until the user sets a new one.
    A success resets `failed_count`.
  - The argon2id verification runs against a fixed dummy hash for a user with no PIN, a
    locked PIN or a disabled one, so time does not tell them apart; the answer for every
    failure is the same 401 `operator_sign_in_failed`.
  - With a badge, `require_pin_with_badge` on the station asks for the PIN too.
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
  - `Access.Check` resolves both hashes, refuses a revoked station or an ended session,
    and ends a session idle longer than the station's `idle_minutes` (`end_reason = idle`)
    or older than **12 hours** (`end_reason = expired`);
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
  - the shop floor's reads: work centers, a center's queue, a work order, my work, the
    production draft, `meta`;
  - start, pause, finish, register quantity (F), register production, report scrap, block
    and unblock;
  - check results and photos, *Report a deviation* (Q).
  
  Cost fields stay out (no `costs-view`); the Assistant's tools are never offered to a
  station principal.
- **M136 — Signing out.** **Switch out** on the operator's chip ends their session; the
  station's idle timer ends it too. Closing a session never touches the user's time logs:
  a log left running shows on the floor as today (D1 M29).
- **M137 — Time logs record the device.** `manufacturing.time_logs`, `productions`,
  `check_results` and `deviations` gain `station_id uuid NULL`; D1 M29's *one open log per
  user* holds across stations and the user's own browser.
- **M138 — CSRF and cookies.** The station cookie is `SameSite=Strict`; operator requests
  carry the operator token in a header, which a cross-site form cannot send, so the
  station needs no CSRF token. The session cookie and the station cookie are independent:
  a person signed in on the same browser still acts as themself through their session on
  every other page.
- **M139 — Audit.** Identity's audit trail (`audit.go`, `writeAudit`) records, on the
  writing transaction: `station_registered`, `station_paired`, `station_revoked`,
  `station_repaired`, `pin_set`, `pin_cleared`, `pin_disabled`, `badge_enrolled`,
  `badge_cleared`, `operator_signed_in`, `operator_sign_in_failed` (station, the user id
  chosen, never the PIN), `operator_locked_out`, `operator_signed_out` (with the reason).
  The station list shows each station's last activity and active operators.
- **M140 — The station screen** (`host/routes/station`, outside the app layout, as
  sign-in is):
  - unpaired: the pairing form;
  - paired: a bar with the station's name and the **operator chips** (each with a
    *Switch out*), **Switch in** opening the operators grid with a PIN pad, or a badge
    scan field that is always focused;
  - below it, the shop floor (`@vantigo/manufacturing-ui`'s shop-floor pages), rendered for
    the **active operator** chosen by tapping a chip; each action goes out with that
    operator's token;
  - an idle warning 30 seconds before a session ends.
- **M141 — Erasure.** Erasing a user deletes their PIN, badge and operator sessions'
  token hashes; the sessions' rows keep the user id for the audit, as sessions do.
- **M142 — Permissions (S).** Registering, pairing, revoking stations and clearing PINs or
  enrolling badges need `identity:manage`. No new key: the station acts within
  `manufacturing:shop-floor`.
- **M143 — Codes.** `operator_sign_in_failed` (401), `station_unknown` (401: no or a revoked
  station cookie), `operator_session_ended` (401), `station_scope` (403),
  `pairing_code_invalid` (400), `pin_too_simple` (400), `too_many_operators` (409).

## 12. Decisions — K, kits (optional)

Built only if the stack has room; otherwise the three decisions move to §14 unchanged.

- **M144 — A kit BoM.** `boms.kind text default 'normal' CHECK (kind IN ('normal','kit'))`.
  A kit has lines, no operations, no by-products; it cannot be the default for MTO
  (`kit_not_manufactured`, 400). Kits nest at most 3 levels.
- **M145 — Exploded on delivery.** `contracts.Production` gains `Kits(ctx, variantIDs)
  (map[int32][]KitLine, error)`. Orders reserves and delivers a kit line's components:
  the order line keeps the kit, the reservation and the delivery lines are per component
  (`delivery_lines.kit_line_of`), lots per component as O. The packing slip prints the kit
  with its contents.
- **M146 — Availability** of a kit is the minimum over its components of available ÷ per
  kit, shown on the order line.

## 13. Permissions, all of phase 2

| Key | Display (en / nb) | Delivery |
| --- | --- | --- |
| `inventory:lots-manage` | Manage lots / Administrere partier | L1 |
| `inventory:expired-override` | Use expired lots / Bruke utgåtte partier | L1 |
| `manufacturing:quality-manage` | Manage quality / Administrere kvalitet | Q |

Every new screen's reads use the existing keys (`locations-view` + `stock-view` in
Inventory; `manufacturing:access`). The host's permission labels (`apps.ts`'s admin
labels) gain the three, in both languages.

## 14. Data model, by migration

One migration per module per delivery; numbers as the plan claims them, from `00162`.

| Migration | Delivery | Schema changes |
| --- | --- | --- |
| `00162_products_variant_tracking` | L1 | `product_variants`: `tracking`, `expiry_days`, `best_before_days` (M49) |
| `00163_inventory_lots` | L1 | `lots` (M52), `lot_attachments`, `object_deletions` (M54); `balances.lot_id` and the new key, `movement_lines.lot_id`, `movement_lines.expired_override` (M55, M62); `counters` row `lot` |
| `00164_inventory_lot_counts_transfers` | L2 | `count_lines.lot_id` and its key (M69), `transfer_lines.lot_id` (M70) |
| `00165_orders_delivery_lots` | O | `delivery_line_lots` (M75) |
| `00166_manufacturing_lots` | M | `boms.lot_pattern`, `orders.lot_pattern` (M80), `production_line_lots` (M82) |
| `00167_manufacturing_quality` | Q | `bom_operation_checks`, `work_order_checks`, `check_results`, `check_photos`, `deviations`, `object_deletions`; counter `deviation` |
| `00168_inventory_variant_rules` | Q | `variant_rules` (M100) |
| `00169_inventory_placement_multiple` | R | `placements.multiple` (M105) |
| `00170_manufacturing_replenishment` | R | `suggestions`, `replenishment_runs`, `replenishment_rules`; `ck_orders_origin_kind` gains `reorder` |
| `00171_manufacturing_routing` | F | `bom_operation_dependencies` (filled from the sequences), `work_order_dependencies` (filled likewise for open MOs), `send_ahead_quantity`, `work_orders.quantity_done`, `work_center_groups`, members, `bom_operations.work_center_group_id`, `work_orders.work_center_group_id` |
| `00172_manufacturing_bom_depth` | G | `bom_outputs`, `order_outputs`, `unbuilds`, `orders.split_from_id`, `orders.merged_into_id`, the amended `ux_orders_origin_line`; counter `unbuild` |
| `00173_identity_stations` | S | `stations`, `user_pins`, `user_badges`, `station_operator_sessions` |
| `00174_manufacturing_stations` | S | `station_work_centers`; `station_id` on `time_logs`, `productions`, `check_results`, `deviations` |
| `00175_manufacturing_kits` | K | `boms.kind` |
| `00176_orders_kits` | K | `delivery_lines.kit_line_of` |

T needs no migration: the trace reads L1's indexes.

Every `down` drops what its `up` made; `00163`'s down restores the `(variant_id,
location_id)` primary key and fails if any balance has a lot (a down is for development,
not for data with lots).

## 15. Refusal codes, all of phase 2

| Code | Status | Rule |
| --- | --- | --- |
| `tracking_requires_goods`, `lot_required`, `lot_not_allowed`, `lot_unknown`, `serial_quantity`, `serial_first_invalid`, `lot_dates_differ`, `lot_pattern_invalid` | 400 | M49, M56, M57, M80 |
| `tracking_held` | 409 | M51 |
| `lot_expired`, `lot_blocked` | 409 | M56, M62 |
| `attachment_too_large`, `too_many_attachments` | 400 | M54 |
| `attachment_type` | 415 | M54 |
| `storage_not_configured` | 503 | M54 |
| `picks_mismatch` | 400 | M75 |
| `trace_too_large` | 400 | M89 |
| `check_limits_invalid`, `photo_required`, `inspection_requires_tracking` | 400 | M95, M97, M100 |
| `checks_incomplete`, `check_not_open`, `deviation_closed` | 409 | M98, M103 |
| `operation_cycle`, `operation_unknown`, `work_center_not_in_group` | 400 | M112, M119 |
| `work_order_started` | 409 | M119 |
| `output_is_product`, `cost_share_over`, `unbuild_too_much`, `split_sum`, `merge_mismatch` | 400 | M120, M123–M125 |
| `order_has_work` | 409 | M124 |
| `operator_sign_in_failed`, `station_unknown`, `operator_session_ended` | 401 | M133, M134 |
| `station_scope` | 403 | M135 |
| `pairing_code_invalid`, `pin_too_simple` | 400 | M129, M132 |
| `too_many_operators` | 409 | M133 |
| `kit_not_manufactured` | 400 | M144 |

A line's code is a line failure in the module's problem shape (Inventory's `lines`,
Orders' line failures, Manufacturing's `lines`); a `StockRefusal`'s code is passed through
by Orders and Manufacturing as they pass phase 1's.

## 16. Rounding

D1 §15 stands: quantities half-up to three decimals, amounts exact and rounded once to two
decimals, percentages to one. Additions:

- FEFO picks never split a quantity finer than three decimals; a serial pick is 1.
- Proportional splits (unbuild, by-products, M121, M123) round each part half-up and put
  the remainder on the last part, so the parts sum exactly.
- Measures are kept to four decimals and judged against the limits unrounded.
- Projected stock (M107) is computed exactly from the text figures.

## 17. Live topics

- Inventory declares its first topics in L1: `inventory.lot:<id>` (a lot's status, files,
  stock) and `inventory.lots` (the list), notified on the writing transaction (the K-D
  rules). The lot page and list refetch on them.
- Manufacturing: `manufacturing.deviations` (Q), `manufacturing.suggestions:<siteId>` (R);
  the existing work-center, order and schedule topics carry F's readying and G's splits.
- Stations use the existing topics; a station principal subscribes as its operator.

## 18. Documentation, per delivery

Every bilingual page changes in `en` and `nb` together; then `go generate ./...` in
`apps/server`.

| Delivery | Pages |
| --- | --- |
| L1 | `user/products.md` (tracking and the offsets); `user/inventory.md` (lots on receipts, moves, adjustments; the lots list and page; files; status; expired lots); `reference/inventory.md` (the lot, the key, the engine's rules and lock order, the contract changes, codes); `reference/products.md` (the fields, the third purpose); `contributing/module-boundaries.md` (rule 20's third purpose, rule 24's new calls and order); `admin/object-storage.md` (the `inventory-lots` scope) |
| L2 | `user/inventory.md` (counts, transfers, import with lots; expiring; labels); `reference/inventory.md` |
| O | `user/orders.md` (picking lots, the PDFs); `reference/orders.md` |
| M | `user/manufacturing.md` (output lots, serials, consumed lots, the pattern); `reference/manufacturing.md` |
| T | `user/inventory.md` (tracing, blocking, recall, GS1 labels); `reference/inventory.md` (the graph, `DeliveryTrace` use, the gates); `reference/orders.md` (`DeliveryTrace`); `user/manufacturing.md` (the *Trace* link); `contributing/module-boundaries.md` (rule 12's new provider) |
| Q | `user/manufacturing.md` (checks, the floor, deviations); `user/inventory.md` (inspection on receipt); `reference/manufacturing.md`, `reference/inventory.md`; `admin/manufacturing.md` (the quality permission, photo storage); `admin/object-storage.md` |
| R | `user/manufacturing.md` (suggestions); `user/inventory.md` (the multiple); `reference/manufacturing.md` (projected, the job); `reference/inventory.md` (`Placements`); `admin/manufacturing.md` (the nightly job, automatic creation) |
| F | `user/manufacturing.md` (dependencies, send-ahead, groups); `reference/manufacturing.md` (readying, planning) |
| G | `user/manufacturing.md` (by-products, unbuild, split, merge); `reference/manufacturing.md` |
| S | `admin/authentication.md` (stations, pairing, PIN and badge policy, lockout, idle, audit); `user/manufacturing.md` (working at a station); `user/workspace-administration.md` (registering a station); `user/getting-started.md` (setting a PIN); `reference/manufacturing.md` (the station scope); `contributing/module-boundaries.md` (the `x-vantigo-station` extension, `ViaStation`) |
| K | `user/orders.md`, `user/manufacturing.md`, `reference/orders.md`, `reference/manufacturing.md` |

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
  at step 4a; the second reads the first's lot after it commits.
- **A blocked lot partly reserved by an order:** the reservation is per site and stays; the
  delivery's proposal skips the lot; if no other stock exists the line is short.
- **The expiry passes between proposal and post:** the post refuses `lot_expired`; the
  delivery page proposes again.
- **Day boundaries:** *today* is the workspace zone's date at the posting's clock; the
  frontend shows the same date from `GET /meta`.
- **A trace through a by-product (G):** a production movement with two produce lots gives
  both the same parents.
- **A trace through an unbuild:** an unbuild movement is a production whose consume line
  is the finished lot and whose produce lines are the component lots: the graph shows the
  components as made *from* the finished lot. The node is labelled *Unbuild*.
- **Kits (K) and lots:** components carry lots; the kit itself never does.
- **A station revoked while operators are mid-action:** their next request is 401
  `station_unknown`; running time logs stay running (M136).
- **An operator whose `shop-floor` permission is removed** while signed in at a station: the
  next request is 403, as a session's would be; the chip shows *Signed out*.
- **Clock skew on tablets:** idle time is the server's `last_activity_at`.

## 20. Testing

- **Ledger (L1):** the key with and without a lot (two concurrent postings to a new
  no-lot pair still serialise, `NULLS NOT DISTINCT`); every M56 code; serial at most 1
  across locations; a new lot created twice concurrently; the extended lock order under a
  status change racing a posting (pool of four); `EnsureLots` replay; `allowExpired` with
  and without the permission and without a request.
- **Products:** a tracking change refused with stock, reserved and in transit, allowed at
  zero; Service resets tracking.
- **Contracts:** `ProposeLots` table tests (FEFO, FIFO, skips, shortage, serials).
- **L2:** a count with two lots of one variant; a transfer round trip per lot; the import's
  lot columns; the expiring list's boundary day.
- **O:** the proposal over two lots and two locations; a post with an expired lot,
  refused and overridden; the PDFs' lot columns (golden text).
- **M:** `{MO}` adding to one lot across two productions; `L{YYMMDD}-{N}` making two;
  serials one per unit; consumed rows summing; the server-side proposal when rows are
  omitted.
- **T:** a three-level graph both ways; *already traced*; the 2,000-node bound; the
  delivery read gated (no `orders:access` → no deliveries; no `customers:view` → no names);
  GS1 element strings (GTIN-13 padded, FNC1 placement, no GTIN fallback) decoded back with
  the encoder's table.
- **Q:** a failed measure opening a deviation and quarantining the lot; finish refused with
  a missing check; close releasing the lot only when no other deviation holds it.
- **R:** projected with MO output and unreserved demand; rounding to the multiple; the
  worker on a pool of one; automatic creation off by default.
- **F:** readying a diamond graph; send-ahead readying and planning; group choice by
  earliest finish, ties; a drag outside the group refused.
- **G:** by-product cost split; unbuild restoring lots proportionally; split re-reserving;
  the amended MTO index.
- **S:** pairing code single use and expiry; PIN hashing and the dummy-hash timing path;
  per-station rate limit; per-user lockout and disable; idle and absolute expiry; the
  permission intersection (an Owner at a station cannot read customers); `station_scope` on
  every non-allowed operation (a test walks the whole contract); the cookie flags; audit
  rows on each event; the lint rule.
- **Frontend:** the lot column in the line editor; the receipt's serial generator; the
  delivery's lot table; the production dialog's lots; the trace tree's collapse; the checks
  panel; the suggestions selection; the station's chips and PIN pad, with the token never
  written to storage.
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

## 22. Open questions

All readings were defaulted (R §9). The ones whose overturn would cost the most:

- **Reading 2** (quarantine as a status): a failure location would change M56's refusal
  into a location rule and M99's quarantine into a move.
- **Reading 3** (no lot on reservations): lot-level reservation would add `lot_id` to
  `inventory.reservations` and change `Reserve`'s signature.
- **Reading 6** (the trace in Inventory): moving it to Manufacturing would leave
  wholesalers without it.
- **M133's badge default** (a badge alone switches an operator in, as Monitor's card
  terminals do; `require_pin_with_badge` is off): a stricter default is one column
  default.
