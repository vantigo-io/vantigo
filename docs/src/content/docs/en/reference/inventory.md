---
title: "Inventory module"
description: "The stock of the catalog's goods in warehouses and stores: sites, each the root of a tree of typed locations with codes and full codes, created one at a time or in bulk, re-parented, archived and deleted under their rules; the ledger of immutable movements — adjustments, moves, goods receipts and counts — whose lines keep a balance per variant and location, exact to three decimals, never below zero, goods only, within one site, idempotent by a client key, and may be entered in packagings; counts that freeze the expected quantities, are counted and post their differences as one movement; location labels; the opening-balance import; the catalog's variants read and locked through contracts.ProductCatalog on the posting's own transaction, and the VariantReferenceHolder that makes Products refuse to delete a stocked or reserved variant or make its product a service; reservations per site and the issue movement kind behind contracts.Stock and contracts.StockLedger, which Orders reserves and delivers through, and the site's Fulfils orders flag; the production movement kind and the reserve for production, which Manufacturing consumes and produces through; transfers between sites — drafts with their own number series, the send, the receive in parts, the difference closed with a reason, the send and receive in one, the cancel — written as actions in one order, posted as transfer_out and transfer_in movements each within its site, idempotent by derived and client keys, their stock in transit as a quantity on the line, the in_transit hold and the følgeseddel; placements — a variant's primary location, reorder point and maximum per site, the primary location as every form's default and contracts.StockLocation.Primary, the low-stock rule and the rounding up to whole packagings, the low-stock list, its CSV and Create transfer from it, and the placements import, idempotent by the file; lots (manufacturing phase 2 L1a) — numbered quantities of a tracked variant, a balance per (variant, location, lot), holds owned by the documents that put them and the status they give, expiry against the workspace's today, FEFO and AutoLot, usable on hand, the lot-aware ledger contracts; and their screens (L1b) — the lots list, a lot's page and its edit, the lot page's holds, a lot's files in the inventory-lots storage scope and their deletion worker, stock per lot and held, the lookup's LOT token and lot numbers, and Inventory's live topics; and the rest of Inventory with lots (L2) — counts per lot, a lot on each transfer line, the import's lot columns, the expiring list and its CSV, the lots' attention items and lot labels; and traceability (T) — the trace of a lot both ways over its movement lines, unbuilds as reverse edges, who received it through Orders' contracts.DeliveryTrace, its CSV and PDF, the block with its downstream list walked again inside the block, and GS1-128 lot labels; the reads — a location's contents, a product's and a variant's stock with what is incoming, the stock totals with what is in transit, the stock at sites, the movements, the transfers, the lookup by code and the dashboard's figures and attention items, low stock among them; inspection on receipt — a variant's rules and the receipt's inspection hold; a placement's multiple and contracts.Stock.Placements for replenishment; the nine permissions and the both-views rule; the lock orders; the personal data kept as ledger records; the refusals, the switch, what comes next and the endpoints."
sources:
  - apps/server/internal/inventory
  - apps/inventory/frontend
  - openapi/inventory.yaml
  - apps/server/internal/openapi/specs/inventory.yaml
  - apps/server/internal/db/migrations/00108_inventory_baseline.sql
  - apps/server/internal/db/migrations/00110_inventory_counts.sql
  - apps/server/internal/db/migrations/00125_inventory_reservations.sql
  - apps/server/internal/db/migrations/00152_inventory_transfers.sql
  - apps/server/internal/db/migrations/00155_inventory_placements.sql
  - apps/server/internal/db/migrations/00163_inventory_lots.sql
  - apps/server/internal/db/migrations/00164_inventory_lot_attachments.sql
  - apps/server/internal/db/migrations/00165_inventory_lot_counts_transfers.sql
  - apps/server/internal/db/migrations/00169_inventory_variant_rules.sql
  - apps/server/internal/db/migrations/00170_inventory_placement_multiple.sql
  - apps/server/internal/contracts/stock.go
  - apps/host/frontend/src/routes/inventory
---
The Inventory module keeps the stock of the catalog's goods in the company's warehouses
and stores. Each **site** is the root of a tree of **locations** — a zone, an aisle, a
rack, a shelf, a bin, a shop's sales floor — and stock lies on locations. Every change of
stock is a **movement**, posted once and never edited, and the module keeps one
**balance** per variant and location from the movements' lines in the same transaction
that writes them. In Norwegian the app is **Lager**. The research is
[`2026-10-27-inventory-module.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/research/2026-10-27-inventory-module.md)
("N") and the design
[`2026-10-27-inventory-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-27-inventory-design.md)
(decisions I*n*). It is a vertical-slice module inside the single Vantigo binary
(`apps/server/internal/inventory`), owns the `inventory` schema in the shared PostgreSQL
database, and serves `openapi/inventory.yaml` under `/api/v1/inventory`. Its app is
`apps/inventory/frontend` (`@vantigo/inventory-ui`), the tile **Inventory**, after
Products in the switcher.

It depends on one business module: Products, for the variants it keeps stock of,
through `contracts.ProductCatalog` ([contracts consumed](#contracts-consumed)).
`MODULES` refuses `inventory` without it (`inventory requires products`), and the module
refuses to mount without a catalog (*inventory: no product catalog; the inventory module
requires the products module*). It provides one many-provider slot to Products, the
[variant reference holder](#contracts-provided), which makes Products refuse to delete
a variant that still lies in stock, and two contracts to Orders: **`contracts.Stock`**,
the read of on hand and available, and **`contracts.StockLedger`**, the write of
[reservations and issues](#reservations-and-issues) on Orders' own transaction.

This page describes phase 1, deliveries A and B, the reservations Orders' delivery C
added, and phase 2 — delivery A, [transfers between sites](#transfers), and delivery B,
[placement and the low-stock list](#placement) — designed in
[`2026-11-08-inventory-phase2-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-08-inventory-phase2-design.md)
(decisions I35–I65): the [contracts](#contracts-consumed),
[the schema](#the-schema), the [permissions](#permissions),
[sites and locations](#sites-and-locations), [the ledger](#the-ledger),
[entering in packagings](#entering-in-packagings), [counts](#counts),
[labels](#labels), [the opening-balance import](#the-opening-balance-import),
[reservations and issues](#reservations-and-issues), [transfers](#transfers),
[placement](#placement), [the locks](#locks),
[the reads](#reads), the [personal data](#personal-data) it keeps,
[how a refusal looks](#refusals), [the switch](#enabling-and-disabling),
[what comes next](#what-comes-next) and the [endpoints](#endpoints).

## Contracts consumed

Every call goes through one accessor in `contractscalls.go`, with the request's context,
**before** any transaction of the module opens, and the accessor refuses a call made
inside one — with one exception by design, `LockVariants`, which runs **on** the
posting's transaction (rule 21 of the
[module boundaries](/en/contributing/module-boundaries/)).

**`contracts.ProductCatalog`** (`Deps.Products`), Products' catalog
([contracts for other modules](/en/reference/products/#contracts-for-other-modules)).
The module reads six of its methods:

- **`LockVariants(ctx, tx, ids)`** — takes `FOR SHARE` on the variants **and their
  products' rows**, in variant id order, **on the posting's own transaction**, and
  answers their entries: the SKU, the product's name, type and status, the unit, and
  the variant's **packagings** as they stand under the lock, from which an
  [entry in packagings](#entering-in-packagings) is resolved. It is
  the first lock every posting takes after reading its key ([locks](#locks)), so
  Products' rows are locked before any of Inventory's, everywhere. A variant absent from
  the answer is the line's `variant_not_found`. Because the product row is shared too,
  a receipt that read *Goods* cannot commit beside a type change to *Service*, and a
  variant's delete waits for a posting in flight and then sees its stock.
- **`Variants(ctx, ids)`** — the batch read naming the variants a response carries,
  with their packagings, which every variant a response names carries for the app's
  [breakdown](#the-breakdown).
- **`VariantsOfProduct(ctx, productID)`** — every variant of a product, for the
  product's [stock](#reads).
- **`LookupCode(ctx, code)`** — the [lookup](#the-lookup)'s variant half: a SKU,
  trimmed and matched case-insensitively and exactly, a variant barcode matched
  exactly, or — since delivery B — a **packaging's barcode**, answered as the variant
  with the packaging that matched.
- **`LookupSKUs(ctx, skus)`** — the [import](#the-opening-balance-import)'s SKU
  resolution: every distinct SKU of the file in one call, each matched
  case-insensitively against variant SKUs only.
- **`SearchVariants(ctx, q, VariantSearch{Types: ["Goods"], Limit, IncludeInactive: true})`**
  — the line editor's picker: goods variants of products of every status by SKU,
  product name or barcode, case-insensitive, by SKU, at most 50, wildcards taken
  literally. It goes through the catalog so that a warehouse worker needs no Products
  permission to pick what they receive.

**`contracts.UserDirectory`** (`Deps.Users`), identity's: the display name of the
person posting a movement or writing the tree, snapshotted on the row. A user the
directory no longer knows is recorded as *Unknown user*; a lookup that fails fails the
write.

**`contracts.BusinessCalendar`** (`Deps.Calendar`): the workspace's business time zone,
in which the movement list's [day filters](#movements) and the dashboard's
[week](#stats) are judged.

**`contracts.DeliveryTrace`** (`Deps.DeliveryTrace`), Orders' read of its posted
deliveries ([Orders' DeliveryTrace](/en/reference/orders/#who-received-a-lot-deliverytrace)), for the
[trace](#who-received-it): `Deliveries(ctx, ids)`, called once per trace on the pool with
the delivery ids of the issues the trace reached, in batches of at most
`contracts.MaxTracedDeliveries` (1,000). It is **provider-gated** on `orders:access`
(rule 12): a caller without it is refused with `contracts.ErrForbidden`, which the trace
answers as `deliveries: hidden` — each delivery its movement alone; a nil slot, Orders
off, as `unavailable`; any other error fails the trace.

**`contracts.CustomerDirectory`** (`Deps.Directory`): `Customers(ctx, ids)`, the names of
the customers a trace's deliveries went to — read only when the deliveries are shown and
the caller holds **`customers:view`**, which Inventory checks itself, since the directory
is a single-provider read with no gate of its own (manufacturing phase 2 M90, as
Manufacturing's D1 M43a).

### Contracts provided

The module fills one many-provider slot, **`Module.VariantReferences`** with a
**`contracts.VariantReferenceHolder`** (I23, rule 20 of the
[module boundaries](/en/contributing/module-boundaries/)). Products asks every holder
**inside its own transaction**, after it has locked the variants `FOR UPDATE`:

- when it **deletes a variant** (`DELETE /api/v1/products/{id}/variants/{variantId}`),
  for that variant; and
- when a `PUT` turns a **Goods** product into a **Service**, for every variant of the
  product in id order.

- when a variant `PUT` changes the variant's **tracking mode** (manufacturing phase 2
  M51), for that variant, with the third purpose `tracking_change`.

`VariantHeld(ctx, tx, variantID, purpose)` sums the variant's balances — every lot
of it — on Products' transaction and answers a `VariantHold` of kind `stock`
— the same for every purpose, since stock stops a delete, a change to Service and a
change of the tracking mode alike: the quantity as the shortest
decimal that says it (`"34"`, `"2.5"`, `"0.125"`) and the number of locations it lies
on — a location holding it in several lots counted once. When it lies nowhere but is [reserved](#reservations-and-issues), it answers kind
`reserved`: the quantity reserved and, in `Locations`, the number of sites. When it
lies nowhere and nothing is reserved but a [transfer](#transfers) carries it — sent and
neither received, recorded missing nor returned — it answers kind **`in_transit`**
(`contracts.VariantHoldKindInTransit`, phase 2 I47): the quantity in transit and, in
`Locations`, the number of transfers; `nil` when none of the three. The first hold
refuses Products' write with 409 — *Variant has stock* or *Product has stock* — with the
unit from Products' own row; the detail reads *5 pcs reserved for orders at 1 site* and
asks for the orders to be delivered or cancelled first, or *12 pcs on its way between
sites in 1 transfer* and asks for the transfer to be received or cancelled. A draft holds
nothing and is no hold. The holder locks nothing of its own:
every posting that could add to the variant — a transfer's send and receive among
them — holds `FOR SHARE` on it through
`LockVariants`, so it has committed before Products' `FOR UPDATE` was granted, or waits
for Products' transaction. The holder needs nothing of `Deps` and is collected
**whether or not `inventory` is enabled** ([enabling and disabling](#enabling-and-disabling)).

It also provides the two stock contracts of `contracts/stock.go` (orders design O45,
rule 24 of the [module boundaries](/en/contributing/module-boundaries/)):

- **`Module.Stock`** — **`contracts.Stock`**, a single-provider slot set from the
  enabled modules, so `Deps.Stock` is nil when `inventory` is off, which Orders reads
  as "stock is not kept":
  - `OnHand(ctx, variantIDs, siteID)` — each variant's on hand, in total and per site
    holding it; with `siteID` set, at that site only;
  - `Available(ctx, variantIDs, siteID)` — **usable** on hand less reserved, the same
    way, per site where the variant lies or is reserved; it may be negative. Usable
    leaves out the [lots](#lots) on hold and those expired on the workspace's today
    (manufacturing phase 2 M63); `OnHand` is everything that lies there.
  - `Sites(ctx)` — every site that is not archived, by code, with its id, code, name,
    whether it fulfils orders and whether it is active: what Orders offers as an order's
    site and checks one against;
  - `Locations(ctx, variantIDs, siteID)` — per variant, every active location of the site
    holding more than 0 of it, by full code, with its id, full code and on hand, and
    **`Primary`** — whether it is the variant's [primary location](#placement) at the
    site (phase 2 I55): where Orders' pick list and delivery editor take a line from,
    the primary first.
  - `SiteLocations(ctx, siteID)` — every location of the site, archived ones too, by
    full code: a `StockSiteLocation` with its id, parent id (nil for the root), kind, full
    code, whether it is active and whether it is the root (manufacturing design M2) —
    what Manufacturing offers as a production's from- and to-locations. An unknown site
    has none. `Sites` answers each site's root as `RootLocationID`.
  - `Lots(ctx, variantIDs, siteID)` — per variant, every [lot](#lots) holding more than
    0 at an active location of the site, one `StockLot` per lot and location — `LotID`,
    `Number`, `Serial`, `Expiry`, `BestBefore`, `ReceivedAt`, `LocationID`, `FullCode`,
    `Quantity`, `Status` — in FEFO order: expiry, none last, then received, then lot id
    (manufacturing phase 2 M65, M67). A variant with none has no row. The pure
    `contracts.ProposeLots(rows, quantity, today)` proposes from them as Inventory's
    AutoLot does, so Orders and Manufacturing propose the same lots.
  - `LotRefs(ctx, lotIDs)` — the lots named, by id, whatever they hold: `ID`,
    `VariantID`, `Number`, `Status` (manufacturing phase 2 M99) — what Manufacturing
    reads on the pool before it opens a deviation of a lot, so it can lock the lot's
    variant first. An unknown id has no entry; more than 1000 ids is a failure.
  - `Placements(ctx, siteID)` — every [placement](#placement) with a reorder point at
    the site, by variant id, with what the low-stock rule reads: a `StockPlacement` with
    `VariantID`, `ReorderPoint`, `Maximum` and `Multiple` (`""` when unset), and
    `OnHand` — **usable** on hand at the site, held and expired lots left out — `Reserved`
    and `Incoming` by transfer (manufacturing phase 2 M106). One statement on the pool, the
    low-stock list's figures; none for a site that is unknown or not active. More than
    `MaxStockPlacements` (5000) is a failure. Manufacturing's
    [replenishment](/en/reference/manufacturing/#replenishment) adds its own orders and
    Orders' unreserved demand to it.

  `OnHand` and `Available` read on the pool in one snapshot, answer every variant asked
  in the order asked (one with nothing at `"0"` and no sites), as exact decimal text,
  and refuse none or more than `MaxStockVariants` (1000) variants, or one named twice;
  `Locations` refuses the same, and answers no entry for a variant held nowhere at the
  site.
- **`Module.StockLedger`** — **`contracts.StockLedger`**, resolved from **every module
  given, enabled or not**, as the holders are, so `Deps.StockLedger` is there even when
  `inventory` is off and a reservation made while it was on is still released. Its
  nine calls run on the caller's transaction and are described under
  [reservations and issues](#reservations-and-issues), [production](#production) and
  [lots](#lots):
  - `Reserve(ctx, tx, src, siteID, today, lines []StockLine{VariantID, LineID, Quantity})`
    — `today` is the business day the caller read on the pool, what expiry is judged
    on;
  - `Release(ctx, tx, src, releases []StockRelease{LineID, Quantity})` — none releases
    everything `src` holds, a `Quantity` of `""` a line's whole reservation;
  - `Issue(ctx, tx, src, StockIssue{Document, SiteID, Lines []IssueLine{VariantID,
    LineID, FromLocationID, Quantity, LotID, AutoLot, AllowExpired}, PostedByUserID,
    PostedByDisplay, Today, ExpiredOverride}) (StockPosted{MovementID, Picks}, error)`;
  - `ReservationsFor(ctx, tx, src) ([]Reservation{VariantID, LineID, SiteID, Quantity},
    error)`;
  - `ReservationsForSources(ctx, tx, srcs) ([]SourceReservation{Source, Reservation},
    error)` — `ReservationsFor` of at most `MaxStockSources` (1000) documents in one
    statement;
  - `ReserveForProduction(ctx, tx, src, siteID, today, lines []StockLine)` — `Reserve`
    without the *fulfils orders* check;
  - `Produce(ctx, tx, src, StockProduction{Document, SiteID, Consume []ConsumeLine{VariantID,
    LineID, FromLocationID, Quantity, LotID, AutoLot, AllowExpired}, Produce
    []ProduceLine{VariantID, LineID, ToLocationID, Quantity, LotID}, PostedByUserID,
    PostedByDisplay, Today, ExpiredOverride, Unbuild}) (StockPosted, error)`;
  - `EnsureLots(ctx, tx, src, today, lots []NewLot{VariantID, Number, SupplierLot,
    ProductionDate, BestBefore, Expiry, MustCreate}) ([]lotID, error)`;
  - `HoldLots(ctx, tx, src, holds []LotHold{LotID, Kind, Cause, Note, By})`;
  - `ReleaseLotHolds(ctx, tx, src, lotIDs, by LotActor)`.

  A document is a `StockSource{Module, Kind, ID}` — `{"orders", "order", 42}`. A
  quantity is decimal text with at most three decimals, more than 0; a bad one is the
  caller's bug, answered as a failure — but by `Produce`, which refuses it
  (`quantity_invalid`). Every refusal is a **`*contracts.StockRefusal`**
  — `Code`, `VariantID`, `LineID` and `Detail` of the first failing line, and `Lines`,
  every one, each with its **`Index`** in the call's lines (`Produce`'s consume lines,
  then its produce lines after them), and the **`LotID`** and **`LocationID`** it failed
  at, so a caller maps a refusal to the exact row when one document line became
  several ledger lines (manufacturing phase 2 M67). `Reserve`, `ReserveForProduction` and
  `Release` write nothing when they refuse. After a refusal from `Issue` or `Produce`
  the caller must roll back its transaction (or a savepoint it took before the call):
  the posting may already have claimed its movement and created balances. Its codes:
  `insufficient_available`, `insufficient_stock`, `site_not_fulfilling`,
  `variant_not_stocked`, `site_unknown`, `location_unknown`, `document_reused`, for
  a production `site_archived`, `quantity_invalid` and `nothing_to_post`, and the [lot
  rules](#the-lot-rules)' `lot_required`, `lot_not_allowed`, `lot_unknown`,
  `serial_quantity`, `lot_expired`, `lot_blocked` and `lot_dates_differ`, and
  `EnsureLots`' `lot_exists`. A zero
  `today` or `Today` is a programming error, answered as a failure. A production
  line that would take a balance beyond 99,999,999,999.999 is `quantity_invalid`.

## The schema

`00108_inventory_baseline.sql` creates the `inventory` schema with six tables. No table
references another schema: a variant id is Products', a user id identity's, both
opaque. **No CHECK constraints** — the module's validation owns the rules and reports
them per field and per line; the unique indexes state facts about the tables. Every id
is an identity from 1001.

- **`sites`** — `code` (16, unique: `ux_sites_code`), `name` (200), `kind` (`warehouse`
  or `store`), the optional address (`address_line1` and `address_line2` 200,
  `postal_code` 20, `city` 100, `country` ISO 3166-1 alpha-2), `status` (`active` or
  `archived`), `revision`, `created_at`, `updated_at`.
- **`locations`** — `site_id`, `parent_id` (null for the root), `kind`, `code` (16),
  `name` (200), `sort_order`, `status`, `full_code`, `ancestors` (the ids from the root
  down to the parent), `depth`, `revision` and the stamps. One root per site is the
  partial unique index `ux_locations_root`; a code is unique among its siblings
  (`ux_locations_parent_code`) and a full code in the deployment
  (`ux_locations_full_code`). A GIN index on `ancestors` finds a subtree:
  `id = X OR ancestors @> {X}`.
- **`movements`** — `client_request_id` (unique) and `request_hash`, `site_id`, `kind`
  (`adjustment`, `move`, `receipt`, `count`, `issue`, since phase 2 `transfer_out` and
  `transfer_in`, and `production`; an open set kept as text), `reason` (an
  adjustment's), `note` (1000), `supplier_name` (200) and `supplier_reference` (100) (a
  receipt's), the **source reference** `source_module`, `source_kind`, `source_id`,
  `source_line_id` — set by an [issue](#reservations-and-issues) to the document it was
  posted for, by a [production](#production) to the production record with the
  reserving order's id as the line, and by a [transfer's movement](#transfers) to the
  transfer and its action;
  null on the module's other movements, and indexed —
  `posted_by_user_id`, `posted_by_display` and `posted_at`.
- **`movement_lines`** — `movement_id`, `line_no`, `variant_id`, the **snapshots**
  `sku`, `product_name` and `unit` as they were at posting, `from_location_id` and
  `to_location_id` (either may be null), and `quantity` (`numeric(14,3)`), indexed by
  variant and by each location.
- **`balances`** — `variant_id`, `location_id`, `on_hand` (`numeric(14,3)`),
  `updated_at`; the key was `(variant_id, location_id)` until `00163` gave it the lot
  (below). A row reaching zero is deleted, so the table lists only where stock lies.
- **`events`** — the record of every structural write (I31): `entity_kind` (`site`,
  `location` or, since B, `count`), `entity_id`, `site_id`, `kind`, `payload` (`jsonb`),
  `actor_kind`, `actor_user_id`, `actor_display`, `occurred_at`. Movements are their
  own record and are not repeated here; a deleted location's events stay. The table has
  no read endpoint in this phase.

`00110_inventory_counts.sql` (delivery B) adds two tables and two columns:

- **`counts`** — `site_id`, `scope_location_id` (the location counted with its
  subtree; the site's root for a whole site), `status` (`counting`, `posted` or
  `cancelled`), `note` (1000), the opener (`opened_by_user_id`, `opened_by_display`,
  `opened_at`), the poster and the canceller likewise, `uncounted` (`keep` or `zero`,
  set at posting), `movement_id` (the count movement a post wrote, null when it wrote
  none) and `revision`; indexed by `(site_id, status)` and `(status, opened_at)`.
- **`count_lines`** — `count_id`, `variant_id`, `location_id`, `expected` and
  `counted` (`numeric(14,3)`, `counted` null until someone counts the line), `entered`
  (`jsonb`, the [entry](#entering-in-packagings) it was counted in), `added`, and the
  counter (`counted_by_user_id`, `counted_by_display`, `counted_at`), and **`lot_id`**
  (manufacturing phase 2 M69, `00165`; null for an untracked variant). One line per
  balance — `(count_id, variant_id, location_id, lot_id)` **`NULLS NOT DISTINCT`**
  (`ux_count_lines_key`), as the balances' own key — indexed by location.
- **`movement_lines.entered`** (`jsonb`) — the snapshot of a line entered in
  packagings.
- **`movements.count_id`** — the count a `count` movement posted, indexed where set.

Products' packagings are Products' table, `products.variant_packagings`
(`00109_products_packagings.sql`); Inventory reads them only through the catalog.

`00125_inventory_reservations.sql` (Orders' delivery C) adds a table and a column:

- **`reservations`** — `variant_id`, `site_id`, `quantity` (`numeric(14,3)`, the one
  `CHECK`: more than 0; a reservation brought to zero is deleted), the source
  `source_module`, `source_kind`, `source_id` and `source_line_id` (text, as a
  movement's), `created_at` and `updated_at`. One row per source line and site
  (`ux_reservations_source_line_site`), indexed by `(variant_id, site_id)`. The balances
  gain no reserved column: reservations are per site, never per location (design §12).
- **`sites.fulfils_orders`** (`boolean`) — whether orders reserve and deliver from the
  site; set to true for every warehouse and false for every store when the migration
  ran.

`00152_inventory_transfers.sql` (phase 2, delivery A) adds five tables for
[transfers](#transfers); the movements table does not change. Unlike the baseline, these
tables carry CHECK constraints that state facts the code also keeps:

- **`counters`** — `counter_name` (the key) and `next_value`, the number the series hands
  out next: one row, `transfer`, allocated by upsert under the row's lock in the
  creating transaction (the Orders shape).
- **`transfers`** — `number` (unique: `ux_transfers_number`), `from_site_id` and
  `to_site_id` (references to `sites`, `CHECK` different), `status` (`draft`, `sent`,
  `partly_received`, `received` or `cancelled`), `reference` (100), `note` (2000),
  `expected_arrival` (a date), `close_reason` (`lost`, `damaged`, `not_packed` or
  `other`) and `close_note` (500), `revision`, the creator, the sender and the closer
  (`*_by_user_id`, `*_by_display`, `created_at`, `sent_at`, `closed_at`) and
  `updated_at`; indexed by `(status, created_at desc)` and by each site with the status.
- **`transfer_lines`** — `transfer_id` (on delete cascade: only a draft is deleted),
  `line_no` (unique per transfer), `variant_id`, the snapshots `sku` and `product_name`
  (written with the line and again at send) and `unit` (at send), `quantity`
  (`numeric(14,3)`, more than 0), `entered` (the [entry](#the-entry) it was given in),
  `suggestion` (kept for placement's low-stock list, stored and answered as given),
  `from_location_id` (set at send), **`lot_id`** (M70, `00165`: the line's one lot, named
  on the draft or at send, indexed where set), and the four running totals **`sent`**, **`received`**,
  **`missing`** and **`returned`**, `numeric(14,3)` from 0, with
  `CHECK (received + missing + returned <= sent)`. Partial indexes on `variant_id` and on
  `from_location_id` cover the lines still in transit; one on `sku` serves the list's
  search.
- **`transfer_actions`** — one row per send, receive or cancel: `transfer_id`, `kind`
  (`send`, `receive` or `cancel`), `client_request_id` (unique:
  `ux_transfer_actions_client_request`) and `request_hash`, `movement_id` (null for a
  receive that only closes), `close_reason` and `close_note`, the poster and
  `posted_at`; indexed by `(transfer_id, posted_at)`.
- **`transfer_action_lines`** — what one action did to one line: `action_id`,
  `transfer_line_id`, `kind` (`sent`, `received`, `missing` or `returned`), `quantity`
  (more than 0) and `location_id` — the source for `sent` and `returned`, the
  destination for `received`, none for `missing`. The key is
  `(action_id, transfer_line_id, kind)`.

`00155_inventory_placements.sql` (phase 2, delivery B) adds one table for
[placements](#placement), kept apart from the balances: a placement is a setting, never
stock.

- **`placements`** — keyed by **`(variant_id, site_id)`**: `site_id` (references `sites`,
  on delete cascade — a site is deleted only when it never held stock),
  `primary_location_id` (references `locations`, **on delete set null**, the backstop
  behind [clearPrimaries](#the-primary-location)), `reorder_point` and `maximum`
  (`numeric(14,3)`, each nullable), the snapshots **`sku`** and **`product_name`**
  (written with the placement and refreshed by the list, for its order and search),
  `revision`, the last writer (`updated_by_user_id`, `updated_by_display`) and
  `updated_at`. Four CHECKs: `reorder_point >= 0`, `maximum > 0`,
  `maximum >= reorder_point` when both are set, and **`num_nonnulls(primary_location_id,
  reorder_point, maximum) > 0`** — a placement with nothing set is never stored. Two
  indexes: **`ix_placements_site_sku`** on `(site_id, sku, variant_id) WHERE
  reorder_point IS NOT NULL`, which serves the low-stock statement's per-site filter and
  the dashboard's count, and `ix_placements_primary_location` on `primary_location_id`,
  which serves clearing the primaries of an archived or deleted location.

`00163_inventory_lots.sql` (manufacturing phase 2 L1a) adds the [lots](#lots):

- **`lots`** — `variant_id`, `number` (1–20 characters `[A-Za-z0-9._/-]`, stored as
  typed, **unique per variant ignoring case**: `ux_lots_variant_number` on
  `(variant_id, lower(number))`), `serial` (the variant was serial when the lot was made:
  history), `supplier_lot` (40), `production_date`, `best_before` and `expiry` (dates),
  `received_at` (when stock first entered under it, the FIFO key), `origin_module`,
  `origin_kind`, `origin_id` (the document that made it: `inventory/movement/<id>` for a
  receipt's or an adjustment's, the caller's source for `EnsureLots`), **`status`**
  (`available`, `quarantine` or `blocked`, derived from its holds), `note` (2000), the
  `sku` and `product_name` snapshots, `revision`, the creator and the stamps. Indexed for
  FEFO on `(variant_id, expiry NULLS LAST, received_at, id)`, by status where not
  available, by expiry where set and by `lower(number)`. A lot is never deleted.
- **`lot_holds`** — `lot_id`, `kind` (`quarantine` or `block`), `cause` (`inspection`,
  `deviation`, `complaint`, `recall` or `other`), `note` (1000), the source
  `source_module`, `source_kind`, `source_id` of the document that put it, the creator,
  `created_at`, and `released_at`, the releaser and `release_note`; indexed by lot and
  by source where active.
- **`balances.lot_id`** — the primary key `(variant_id, location_id)` is dropped and
  replaced by **`ux_balances_key` on `(variant_id, location_id, lot_id) NULLS NOT
  DISTINCT`**: no lot is one key of its own, so two postings to a new (variant,
  location, no lot) still serialise on one row. Indexed by lot where set. **The table
  has no primary key** from then on: a primary key's columns are `NOT NULL` and the lot
  is null for untracked stock, and the unique index cannot be the replica identity for
  the same reason. Were the table ever published for logical replication, it would need
  a surrogate id as its key, or `REPLICA IDENTITY FULL`. A posting's writes match the
  lot as an equality or both none, so the index serves them.
- **`movement_lines.lot_id`** (indexed with the movement where set) and
  **`movement_lines.expired_override`** (`boolean`, false) — an expired lot taken on the
  poster's [override](#expiry-and-today).
- The **`counters`** row `lot`, from 1001, for generated numbers.
- **`variant_rules`** (`00169`, Q) — `variant_id` (primary key), `inspect_on_receipt`,
  `updated_by_user_id` and its display, `updated_at`, `revision`: a variant's rules in
  Inventory. A variant with no row has none.

Its down restores the old key and fails while any balance or line has a lot.

## Permissions

| Key | Display | Grants |
| --- | --- | --- |
| `inventory:locations-view` | View locations | "See the sites and their locations, and find a location by its code, without quantities." |
| `inventory:locations-manage` | Manage locations | "Create, change, archive and delete sites and locations, one by one or in bulk." |
| `inventory:stock-view` | View stock | "See what lies where: the quantities on hand, a product's Stock card, a location's contents and the movements." |
| `inventory:stock-adjust` | Adjust stock | "Post adjustments and moves of stock between the locations of a site." |
| `inventory:receipts-manage` | Receive goods | "Post goods receipts, putting delivered goods on the locations of a site." |
| `inventory:counts-manage` | Count stock | "Open, count, post and cancel stock counts." |
| `inventory:transfers-manage` | Transfer stock | "Create, send, receive and cancel transfers of stock between sites, and close a transfer's difference." |
| `inventory:expired-override` | Use expired lots | "Issue or consume a lot past its expiry date, when the lot is chosen by hand on a delivery or a production." |
| `inventory:lots-manage` | Manage lots | "Change a lot's dates, supplier lot and note, add and remove its files, and put and release its holds on the lot page." |

All nine are in the category "Inventory", **none is sensitive** and all are delegable;
the Owner holds them through the wildcard. In Norwegian they read *Se lagersteder*,
*Administrere lagersteder*, *Se beholdning*, *Justere beholdning*, *Motta varer*,
*Telle varer*, *Overføre varer*, *Bruke utgåtte partier* and *Administrere partier*.
`transfers-manage` came with phase 2 (I63), `expired-override` and `lots-manage` with
manufacturing phase 2 (M68). Inventory
checks `expired-override` in no ledger call: the caller reads it on the pool before its
transaction and passes it as `ExpiredOverride` ([expiry](#expiry-and-today)).

Access is **all-of**: an operation's `x-vantigo-access` names every key it needs,
fully qualified (`permission:inventory:locations-view+inventory:stock-view`), and a
caller missing any gets the access layer's uniform 403.

**Both views.** Every stock read and every write also requires `locations-view` and
`stock-view`, because each shows location codes and each form picks locations. So a
stock role is `locations-view` + `stock-view` (+ `stock-adjust`, `receipts-manage`), and
`locations-view` alone gives the structure without quantities. The tile and the app's
home are offered with either view; Movements, Receipts, Counts, Transfers and Import
opening balances in the sidebar, the product page's Stock card, the product list's
*On hand* column and the dashboard's figures with both — and **Low stock**, a read, with
both views alone; its *Create transfer* is hidden without `transfers-manage`, and
*Edit placement* and the placements import without `stock-adjust`. **The lots**, a lot's
page and its files' downloads need both views alone; *Edit*, *Quarantine*, *Block*,
*Release* and the files' writes are hidden without `lots-manage`. **The trace**, its CSV and
its PDF need both views alone; a delivery's order and customer on it need `orders:access`,
checked by Orders, and the customer's name `customers:view`, checked by Inventory
([who received it](#who-received-it)). **Seeing a transfer and printing
its følgeseddel need both views alone**; *New transfer*, *Send*, *Receive* and *Cancel*
are hidden without `transfers-manage`.

| Reads and writes | `locations-view` | + `stock-view` | + `locations-manage` | + `stock-adjust` | + `receipts-manage` | + `counts-manage` | + `transfers-manage` | + `lots-manage` |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Sites, their location trees, a location, the lookup's location matches, [labels](#labels) | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Contents, a product's and a variant's stock, the stock totals, the stock at sites, the movements, the variant search, the stats, the lookup's item, transfer and lot matches; the lots, a lot's page and its files; the transfers, one transfer and its følgeseddel; the low-stock list, its CSV and the `lowStock` item | – | ✓ | – | ✓ | ✓ | ✓ | ✓ | ✓ |
| Create, change, archive, reactivate and delete sites and locations; bulk | – | – | ✓ | – | – | – | – | – |
| Adjustments and moves; the opening-balance import; placements — set, remove, import | – | – | – | ✓ | – | – | – | – |
| Goods receipts | – | – | – | – | ✓ | – | – | – |
| Counts: list, open, count, post, cancel; the `countOpenLong` item | – | – | – | – | – | ✓ | – | – |
| Transfers: create, change and delete a draft, send, receive, close, cancel; the `transferOverdue` item | – | – | – | – | – | – | ✓ | – |
| The trace, its CSV and its PDF | – | ✓ | – | ✓ | ✓ | ✓ | ✓ | ✓ |
| Lots: change a lot, put and release the lot page's holds, block a lot with its downstream list, add and remove its files | – | – | – | – | – | – | – | ✓ |

Each column adds to `locations-view`; the write columns assume `stock-view` too.
**Labels need `locations-view` alone**: a label prints a location's token, code and
name, which that key already shows. The design's §5 table put labels under
`locations-manage`; its contract and §6 say view, and view was kept — printing changes
nothing.

**`GET /meta`** is the session's: it answers the caller's nine capabilities —
`canViewLocations`, `canManageLocations`, `canViewStock`, `canAdjust`, `canReceive`,
`canCount`, `canTransfer`, `canOverrideExpired`, `canManageLots` — and **`today`**, the workspace zone's
date that a lot's expiry is judged on, so the app shows only what works and a caller with
none of them learns that rather than a 403.

## Sites and locations

### Codes

A site's and a location's **code** is trimmed and upper-cased, then must be 1–16
characters `A–Z` and `0–9` (`code_invalid`), and never `LOC` followed by digits — the
form of a location label's token (I7) — so a full code can never equal a token. Codes
cannot contain `-`, which joins them: a location's **full code** is the codes from the
root down, `OSL-A-03-2`. A site code is unique in the deployment (`site_code_taken`), a
location code among its siblings (`location_code_taken`), and so a full code is unique
in the deployment (`full_code_taken` guards the rest).

### Sites

A **site** is a `warehouse` or a `store` (I1), with a name, an optional address, a
status and **`fulfilsOrders`**. The kind changes the words on screen and no rule but
one default: a site created without `fulfilsOrders` fulfils orders when it is a
warehouse and not when it is a store. `fulfilsOrders` decides whether Orders may
[reserve and issue](#reservations-and-issues) there; a `PUT` without it keeps it as it
is, and turning it off leaves the site's reservations as they are. A change is recorded
in `site_changed`. **Creating a site creates its
root location** — kind `site`, coded as the site — in the same transaction. `PUT` takes
`If-Match` with the site's revision and the whole site:

- **A new code** is checked against the other sites, then followed through the root and
  every full code below it in one statement (I4). Printed labels keep scanning, since
  they carry the token, not the code.
- **Archiving** (`status: archived`) is refused while any [count](#counts) in the site
  is still counting (`location_has_open_count`), then while anything in the site holds
  stock (`location_has_stock`; [the detail](#refusals) gives the quantity and the
  number of stock lines to a caller with `stock-view`), then while the site is the from
  or the to site of a [transfer](#transfers) sent or partly received
  (`site_has_open_transfers`; a draft does not stop it — its send is refused instead),
  and archives every location of the site.
- **Reactivating** restores the site and its root **only**; the locations below stay
  archived until each is reactivated.
- **Deleting** is refused when any movement was ever posted, or any count opened, in
  the site (`location_has_history`, *Site has history*: archive it instead); otherwise
  its locations and the site go in one transaction. A site with no movement has no
  stock, since only a movement puts stock down. A site any transfer names, a draft
  included, is refused too (`site_has_transfers`: delete the draft or archive the site).

`GET /sites` lists the active sites, or those of the `status` asked for. Every site it
answers — and `GET` and `PUT /sites/{id}` — carries its **figures**, read in one
query per request: `locationCount`, the active locations below the root, to everyone;
and to a caller holding `stock-view`, `stockLines` (the stock lines in the site: a
variant on a location, its lots counted once) and
`lastReceiptAt` (the last receipt posted there, absent when there was none), and
`incomingTransfers` and `outgoingTransfers`, the number of transfers sent or partly
received to and from it. The app's home draws its site cards from them with no request
per card.

### The tree

A **location** has a parent in the same site, a kind from the fixed list `zone`,
`area`, `sales_floor`, `back_room`, `aisle`, `rack`, `shelf`, `bin`, `display`, `dock`,
`other` (`site` is the root's alone and is never written), a code, an optional name, a
sort order and a status (I2). Stock may lie on any location, the root included, and
every total includes the subtree (I3). The tree is at most **8 levels below the root**
(`depth_exceeded`). A location never changes site.

**Create** names an active parent (`parent_invalid` for an unknown, vanished or
archived one). **`PUT`** takes `If-Match` with the location's revision and may rename,
recode, re-kind, re-sort, **re-parent** and change the status:

- **Re-parent** (I21) moves the location and its subtree under another location of the
  same site that is active and is not the location or below it (`parent_invalid`), as
  long as the subtree's deepest location stays within the depth. The **balances are
  untouched**: the location moved, with its stock. A re-parent is refused 409
  `location_has_open_count` while a count still counting has its scope in the moved
  subtree, at one of the location's current ancestors, or at or above the new parent —
  so a whole-site count blocks every move in its site: *The location X is being
  counted, or something below it, above it or above where it would go is. Post or
  cancel the count before moving it.* Changing only the code or the name is not
  affected.
- A **recode or a re-parent** rewrites the full codes, the ancestors and the depths of
  the whole subtree in one statement, refused with `full_code_taken` if any new full
  code is held; every rewritten row's revision is bumped.
- **Archive** cascades to the subtree. It is refused while a count still counting has
  for its scope the location, a location below it or one above it
  (`location_has_open_count` — the count's lines would name archived locations), then
  while stock lies anywhere in the subtree (`location_has_stock`, with the quantity and
  the number of stock lines to a caller with `stock-view`), then while the location or
  one below it is the source of a transfer line still in transit
  (`location_in_transit`) — the cancel's way home. An archived location takes
  no stock (`location_archived` on a line).
- **Reactivate** restores the location **alone**, and only under an active parent.
- **The root** takes only a name and a sort order: a `PUT` changing its `parentId`,
  `kind`, `code` or `status` is `root_location`, since the site's status governs it.

**Delete** takes the whole subtree, and is refused when any movement line ever named any
of it, from or to, or any count had any of it for its scope or a line on it
(`location_has_history`: archive it instead), and for a site's root
(`root_location`).

`GET /sites/{id}/locations` answers the site's tree flat, by full code, every status
unless one is asked for, **capped at 10,000 rows**, with `truncated: true` when more
matched. `q` (up to 100 characters) keeps the locations whose full code or name contains
it, ignoring case; the app's tree searches the server with it on Enter when the site's
list came back truncated or holds more than 2000 locations, and filters in the browser
otherwise. `GET /locations/{id}` answers one
location with its site, its path from the root and its children by sort order then
code, archived ones included.

### Bulk

`POST /locations/bulk` creates a grid under one parent (I5): **one to three levels**,
each a kind, an optional prefix and a range **From**–**To** — digits padded to the
width of From (`01`–`12` gives `01, 02 … 12`), or one letter to another (`A`–`D`); an
empty To is one code. Every code of a level is created under every location of the
level before. An aisle `A`, racks `01`–`12` and shelves `1`–`5` make 1 + 12 + 60 = 73
locations, 60 of them shelves (`OSL-A-01-1` … `OSL-A-12-5`). At most **1000** per
request (`bulk_too_large`); a parent that is archived, unknown or deleted concurrently
is `parent_invalid`, as for a single create; and the bulk is **all or nothing**: one collision refuses the grid,
naming every colliding full code — `location_code_taken` when a first-level code is
taken among the parent's children, `full_code_taken` otherwise.

### Events

Every structural write is one row in `inventory.events` with the actor: `site_created`
(with the root's id), `site_changed` (the fields that changed), `site_archived`,
`site_reactivated`, `site_deleted`; `location_created`, `location_changed`,
`location_reparented` (with the old and new full codes, so a label can be reprinted),
`location_archived` (with how many locations the archive reached),
`location_reactivated`, `location_deleted`; and **`locations_bulk_created`** — one event
on the parent listing every full code the bulk made. A [count](#counts) records
`count_opened`, `count_posted` and `count_cancelled`, with entity kind `count`. A
[transfer](#transfers) records, with entity kind `transfer`, `transfer_created`,
`transfer_updated`, `transfer_deleted`, `transfer_sent` and `transfer_cancelled` at its
from site and `transfer_received` and `transfer_closed` at its to site, each with the
transfer's number; a send, receive or cancel names its movement.

## The ledger

### Movements and lines

Every change of stock is a **movement** (I8): a kind — `adjustment`, `move`,
`receipt`, `count`, `issue`, posted only through
[`StockLedger.Issue`](#reservations-and-issues), `transfer_out` and `transfer_in`,
posted only by a [transfer](#transfers), or `production` and `unbuild`, posted only through
[`StockLedger.Produce`](#production); the set is open — a site, an adjustment's reason, an
optional note, a receipt's optional supplier and supplier's reference, a count
movement's `countId`, the poster and the time. **A movement is never edited or deleted**; a mistake is corrected
by a new adjustment.

A **line** (I9) is a variant, an optional *from* and an optional *to* — a receipt's line
has only *to*, an adjustment's increase only *to* and its decrease only *from*, a move
both — and a quantity more than 0, with the variant's SKU, product name and unit
snapshotted, so history reads after the variant is gone. A line may be given in
[packagings](#entering-in-packagings) instead of a quantity; it then keeps the entry as
`entered`.

- **Adjustment** (`stock-adjust`) — a required **reason**: `correction`, `damaged`,
  `lost`, `found`, `opening_balance` or `other`; lines of a variant, a location, a
  **mode** and a quantity. `change` takes a signed delta, not 0; `set` takes the new on
  hand, 0 or more, and is posted as the difference against the balance the posting has
  locked. A line whose difference is 0 is dropped; a movement left with none is
  `nothing_to_post`.
- **Move** (`stock-adjust`) — lines of a variant, from, to and a quantity; never from a
  location to itself (`same_location`).
- **Receipt** (`receipts-manage`) — a default location, lines of a variant, an optional
  location overriding it, and a quantity; the supplier, the supplier's reference and a
  note.
- **Count** — posted only by [a count's post](#posting), never through
  `/movements`: one line per counted location and variant that changes, posted as a
  `set`.
- **Issue** — posted only by another module through
  [`StockLedger.Issue`](#reservations-and-issues), never through `/movements`: lines
  with only *from*, and the source reference of the document it was posted for.
- **Production** (`production`) — posted only by another module through
  [`StockLedger.Produce`](#production), never through `/movements`: consume lines with
  only *from*, then produce lines with only *to*, and the source reference of the
  production record, with the manufacturing order's id as the line.
- **Transfer out** (`transfer_out`) — posted only by a transfer's
  [send](#send), at the from site: lines with only *from*, each a line's source location.
- **Transfer in** (`transfer_in`) — posted only by a transfer's [receive](#receive), at
  the to site, lines with only *to*; or by its [cancel](#cancel), at the **from** site,
  each line back to the location it left. A transfer's movements carry the source
  reference `inventory/transfer/<transfer id>`, with the action's id as the line.

### Balances and quantities

One balance per (variant, location) holds `onHand` (I10), written by the posting's own
transaction. Lines are applied **in order** and the balance checked after each, so
−5 then +5 against 0 fails at the first; a line that would take a balance **below zero**
is `insufficient_stock`, naming the SKU, the location's full code, what lies there and
what the line takes. A balance left at zero is deleted.

**Quantities are exact** (I11): `numeric(14,3)` in the variant's own unit, at most three
decimals — more are refused, never rounded — more than 0 (a `set` 0 or more), and at
most 99,999,999,999.999 on a line and on a balance (`quantity_invalid`). A JSON number
becomes an exact decimal through its shortest text (`strconv` `'f', -1`) and
`big.Rat.SetString`, never `SetFloat64`; no arithmetic on a quantity is done in
floating point, and the number a response carries is the decimal's nearest double, made
for the wire.

### Goods only

As built (I12, design §9):

- A **Service** product's variant is refused `variant_not_stockable` **only on a line
  that increases stock**: a receipt's line, an adjustment up, a `set` above what lies
  there. **Moves and decreases still post**, so stock left on a product turned into a
  service — which Products refuses while stock lies, but which the order of a type
  change and a posting cannot make impossible before this delivery — can be cleared.
- A **Discontinued** product's variant is refused `variant_discontinued` **on a receipt
  only**; adjustments and moves accept it, so the last stock can be cleared.
- A **Draft** product's variant is accepted everywhere: goods arrive before they go on
  sale.

### Within one site

Every location a line names must be in the movement's site (I13): a line naming one
outside it is `move_crosses_sites`, whatever the movement's kind. Stock crosses sites
only through a [transfer](#transfers), which keeps the rule: its send posts a
`transfer_out` at the from site, its receive a `transfer_in` at the to site, and its
cancel a `transfer_in` back at the from site, so each movement stays in one site. Between
the two, what was sent lies on no location — it is [in transit](#in-transit), a quantity
on the transfer's lines.

### Idempotency

Every posting carries a **`clientRequestId`** (a UUID made when the form opens and
kept across retries; I14). It is read **first**, before any validation: the same key
with the same body — the SHA-256 of the kind and the typed body as `encoding/json`
writes it — answers the **first movement with 200**, even if its variant has since been
discontinued; the same key with a different body is **409 `client_request_reused`**. A
new movement answers 201. Two first attempts racing are settled by the key's claim
(`INSERT … ON CONFLICT DO NOTHING`): the loser reads the winner back and answers as a
replay.

### Lines and their refusals

A movement posted through the API carries at most **500 lines** (`too_many_lines`);
the engine's internal callers — a count's post and the import — are not capped. **Every
failing line is answered at once**, in the problem's `lines: [{index, code, detail}]`,
and nothing is posted. The shape's, the catalog's and the locations' refusals are
judged together first — `entered_invalid`, `packaging_not_found`,
`variant_not_found`, `variant_not_stockable`, `variant_discontinued`,
`location_not_found`, `location_archived`, `move_crosses_sites`, `same_location`,
`quantity_invalid` — and only when none failed
are the balances judged, for `insufficient_stock`; after an insufficient line the rest
are still judged against the balance as the earlier lines left it. The [lot
rules](#the-lot-rules) are judged with the shape's. The problem is 409 with the code of
the first line about the state of things — `insufficient_stock`, `lot_expired` or
`lot_blocked` — whenever a line has one, and 400 with the first line's code otherwise.

## Lots

Manufacturing phase 2's delivery L1a ([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-12-manufacturing-phase2-design.md),
decisions M49–M67) makes the ledger lot-aware. A variant's **tracking** is Products'
(`none`, `lot` or `serial`, answered as `VariantEntry.Tracking` by every catalog read
and by `LockVariants`), changed only while the variant holds nothing
([Products](/en/reference/products/#tracking)). Orders' deliveries pick lots (layer O) and
Manufacturing's productions name and consume them (layer M,
[Manufacturing](/en/reference/manufacturing/#lots-on-productions)), so since layer M every
mode is open. An untracked variant posts exactly as before: its balances and lines carry
no lot.

### The lot

A **lot** (`inventory.lots`; nb *parti*) is a numbered quantity of one variant. Its
number is 1–20 characters `A–Z a–z 0–9 . _ / -` (GS1's limit for AI 10 and 21), stored
as typed and **unique per variant ignoring case**. It has the supplier's lot, a
production date, a best-before date and an expiry date, the moment stock first entered
under it (`received_at`, the FIFO key), the document that made it, a note and its
**status**. `serial` records that the variant was serial when the lot was made; the
serial rule follows the variant's tracking now. A lot is never deleted.

### The balance key

A balance is per **(variant, location, lot)**, the lot `NULL` for none, under one unique
index **`NULLS NOT DISTINCT`**: the engine's `INSERT … ON CONFLICT (variant_id,
location_id, lot_id) DO UPDATE` infers it, so two postings to a new no-lot key still
serialise on one row (tested). The writes unnest a lot array beside the variants and
locations, `0` for none, and match with `IS NOT DISTINCT FROM`. A movement line carries
its lot. Every read that showed a balance per (variant, location) — a location's
contents, a product's and a variant's stock, `Stock.Locations`, a count's lines — sums
it over the lots on purpose; the lots themselves are on screen from L1b ([stock per
lot](#stock-per-lot)).

### The lot rules

Judged in the engine after `LockVariants` and after the lots are locked, as line
failures:

| Case | Code |
| --- | --- |
| a tracked variant's line names no lot (and is not `AutoLot`) | `lot_required` (400) |
| an untracked variant's line names a lot | `lot_not_allowed` (400) |
| the lot is unknown or another variant's | `lot_unknown` (400) |
| a serial line's quantity is not 1, a serial set to anything but 0 or 1, or a serial that would hold more than 1 over every location | `serial_quantity` (400) |
| a receipt's first serial ending in no digits | `serial_first_invalid` (400) |
| a stated date differs from the existing lot's | `lot_dates_differ` (400) |
| a new lot's dates stated out of order: an expiry before the production date, or a best-before date after the expiry | `lot_dates_invalid` (400, a field error on `lines[i].lot.expiry` or `.bestBefore`) |
| an issue or consume line takes an expired lot without the override | `lot_expired` (409) |
| an issue or consume line takes a lot on hold; a receipt into a blocked lot | `lot_blocked` (409) |

**Moves, adjustments, counts and transfers ignore holds and expiry**: held stock can be
moved to a hold shelf, counted or written off. Only an issue and a production's consume
lines are refused, and an unbuild's consume lines are not. A line of a variant whose
tracking changed since it was drafted is judged as the variant is now.

**The serial rule is judged under the lot's lock.** Two postings can raise one serial
lot at two locations at once and each balance alone stays at 1, so a line that
increases a lot of a serial variant takes the lot `FOR NO KEY UPDATE` and the engine
sums all the lot's balances, every location, after locking its own: the second posting
waits for the lot and then sees the first's 1 (tested with two receipts racing, by
number and by id).

### Receipts, moves and adjustments with lots

The API's lines name lots (M57, M59); the forms gain the lot column in L1b.

- A **receipt** line takes **`lotId`**, an existing lot; or **`lot`** — `{number?,
  supplierLot?, productionDate?, bestBefore?, expiry?, note?}` — a lot by number, found
  or made; or, for a serial variant, **`serials`** — `{first, count}` (1–500) or
  `{numbers: [...]}` — expanded into one line of 1 per serial, numbered by counting up
  the trailing digits of `first` and keeping their width (`SN0099`, `SN0100`), each a
  lot by number with the line's `lot` giving their dates. The expansion counts against
  the 500-line cap; a line's quantity, when given beside serials, must equal their
  number.
- A **move** line takes `lotId`: the lot leaves *from* and lands on *to* unchanged.
- An **adjustment** line takes `lotId`, or `lot` for an increase; a `set` line sets one
  (location, lot).

**A lot by number.** An empty number generates **`L<n>`** from the counter row `lot`,
from 1001, allocated in the posting's transaction. **A generated number never joins a
lot**: when the variant has that number already — typed by a receipt, `L1001` say, in any
case — the posting takes the next number, under the counter row's lock, until one is
free, and a lot of its own is made. A number the line types names that lot: receiving more under a supplier's lot adds to it and **its dates are not changed**.
Only a date the line **states** is compared — a stated date that differs is
`lot_dates_differ`, so a typo is seen — and a date left out is not, so an ordinary repeat
receipt never trips it. Dates the line states must be **in order** — an expiry not before
the production date, a best-before not after the expiry — or the request is refused
`lot_dates_invalid`; a date left out is not compared. **Defaults apply only when the lot is made**: `expiry` is the
production date when given, else the receipt's day in the workspace zone, plus the
variant's `expiryDays`; `bestBefore` likewise with `bestBeforeDays`; each only when the
variant has the offset and the line gives no date. A lot made by a receipt or an
adjustment names its movement as its origin (`inventory/movement/<id>`), and its
`lot_created` event is recorded once that movement is claimed — so a posting that loses
its key to a concurrent first attempt, and removes the lots it made, leaves no event
behind. Two receipts making one number at once serialise on the unique index; the
second reads the first's lot once it commits (tested). The movement's lines answer their **`lot`** (`id`,
`number`, `expiry`) and, when an expired lot was taken on the override,
`expiredOverride`.

### Expiry and today

A lot is **expired** when its expiry lies before **today**, the workspace zone's date.
Best-before alone never refuses anything. **No ledger call makes a contract call of its
own** (rule 24): `StockIssue`, `StockProduction`, `EnsureLots`, `Reserve` and
`ReserveForProduction` carry **`Today`**, which the caller reads from the business
calendar on the pool before its transaction, and an issue and a production carry
**`ExpiredOverride`**, which the caller reads as `inventory:expired-override` on the pool
the same way — a worker with no request passes false. An issue or consume line takes an
expired lot only when it carries **`AllowExpired`** (the person chose that lot) **and**
the call's `ExpiredOverride` is true; the movement line records it
(`expired_override`). A zero `Today` is a programming error. Inventory's own handlers
read the zone the same way, before their transactions, and only when a line names a
lot by number. A test proves that every ledger call made inside another module's
transaction records no contract call. **A date is its own year, month and day**:
`Today`, a `NewLot`'s dates and `StockLot`'s are calendar dates, by convention at
midnight UTC, and the expiry rule reads each as given, never moved to UTC first.

### Usable on hand

**Usable** on hand is the balances whose lot is none, plus those of lots with no active
hold that are not expired today. It is what can be promised: `Reserve` and
`ReserveForProduction` judge available as usable less reserved, `Stock.Available`
answers usable less reserved, the low-stock list reads the same figure, and so do the
stock pages' **available** and their placement's **low**; on hand — `Stock.OnHand` and
every stock page's on hand — stays everything that lies there.

**The reserve's lock (M65b).** A serial variant has one balance row per serial, and a
reserve locks every one of the variant's rows at the site. The test reserving against
**10,000 serial balances** at one site takes about 23 ms locally, under the design's
50 ms, so **the row locks were kept**: no (variant, site) advisory lock was built. The
test logs its runs, so CI's log carries the figure the 50 ms is judged against; it fails
only an order of magnitude beyond it, for a loaded run under the race detector.

### Holds and their status

A lot's restrictions are **holds** (`inventory.lot_holds`), each owned by the document
that put it — `kind` `quarantine` or `block`, a `cause` (`inspection`, `deviation`,
`complaint`, `recall`, `other`), a note and the source. The lot's **status is
derived**: `blocked` while any active hold is a block, else `quarantine` while any is a
quarantine, else `available`; every hold change rewrites it in its transaction, under
the lot's `FOR NO KEY UPDATE`, and writes an event (`lot_held`, `lot_released`; a lot's
creation is `lot_created`). **A release removes only its own hold**: two sources may hold
one lot, and it is free when neither does. Another module's holds go through
`StockLedger.HoldLots` and `ReleaseLotHolds` on its own transaction; the lot page puts and
releases its own ([the lot page's holds](#the-lot-pages-holds)).

### FEFO and AutoLot

One order everywhere a lot is proposed or listed: **expiry ascending, none last, then
received, then lot id** — FIFO when no lot expires. **`AutoLot`** on an issue or consume
line lets Inventory take the lots itself at the line's location, **under its own
locks**: the candidates are read plainly on the transaction in that order, and only
those that look usable — available and not expired — until they cover what the
location's AutoLot lines take, **and two more**, are locked with the named lots and
their balances. Should they fall short under the locks — a lot held or emptied between
the read and the lock — the posting, run on a savepoint, is rolled back to it, its locks
with it, and runs again with every candidate. The lots are taken in that order, skipping lots on hold and expired lots — even
with `AllowExpired`: auto never uses an expired lot — and never splitting a serial. A lot
that appeared after the candidates were read is not used, and what they do not cover is
`insufficient_stock`. The lots taken come back in **`StockPosted.Picks`** —
`{Index, LotID, LocationID, Quantity}` per lot, by the line's index — and a replay
answers the same picks, read back from its movement's lines; an `AutoLot` line of an
untracked variant posts as one line with no lot and picks nothing.
`contracts.ProposeLots` is the same order as a pure function for a proposal read on the
pool.

### Lots of other modules

**`EnsureLots(ctx, tx, src, today, []NewLot)`** finds or creates each lot inside the
caller's transaction, after its rows and before the `Produce` that uses them, and
answers the lot ids in the order given; the lots made name `src` as their origin. A lot
that exists for the variant is answered as is: only the dates the `NewLot` states are
compared (`lot_dates_differ`), its production date never — unless the `NewLot` sets
**`MustCreate`**, when a number the variant has already, or one an earlier `NewLot` of
the call names, is `lot_exists` instead: a production's counted lot number and its
serials name a new lot or none, never join one. A new serial whose number holds stock
is `serial_quantity`; an untracked variant is `lot_not_allowed`. Each refusal's line
`Index` is the `NewLot`'s. A
`ProduceLine` of a tracked variant names such a lot by `LotID`. The production's lines
are keyed by (variant, line id). After any refusal the caller rolls back, the whole
transaction or to a savepoint it took before the call. **It takes
every lot it answers `FOR NO KEY UPDATE`**, not only a serial's: a production's
`HoldLots` on its output in the same transaction (design M86, step 7b) then holds the
lock it needs already, where upgrading a `FOR SHARE` would deadlock two productions of
one lot.

### The lot screens

L1b (M54, M60–M66) puts the lots on screen. Reading a lot needs **both views**, as every
stock read; changing one needs **`inventory:lots-manage`** beside them (M68).

- **`GET /lots`** — the lots list, by **(variant, lower(number), id)**, `limit` 100 by
  default and at most 500, with an opaque `nextCursor`. `q` (up to 100 characters)
  matches the lot number, the supplier lot or the SKU snapshot, ignoring case; `variantId`;
  `siteId`, which makes `onHand` what lies at that site; `status`; `holdCause`, a lot with
  an **active** hold of that cause; `expiresBefore`, an expiry before that day; and
  `withStock`, **true by default**: only lots holding more than 0 — at the site when one
  is named. Each row carries the variant, the number, `serial`, the supplier lot, the
  expiry and best-before dates, the status, **`expired`** — the expiry before today in
  the workspace zone, read on the pool first — `onHand`, and **`receivedAt`**, FEFO's
  second key after the expiry. A filter or cursor that does not parse is a 400.
- **`GET /lots/{id}`** — a lot's page, in one snapshot: the header (number, variant,
  supplier lot, the three dates, `receivedAt`, the status, `expired`, the origin as a
  source reference — `inventory/movement/<id>` for a receipt's or an adjustment's lot —
  the note, the creator and the **revision**); **`holds`**, every hold, the active ones
  first, newest first within each, each with its source and **`inventorys`** — whether
  its source module is Inventory, so the page may release it; **`locations`**, what lies
  of it per location, by site code then full code, and `onHand` over them; **`movements`**,
  its movement lines, newest first, the latest 200 — the page says so when 200 come back,
  and the older ones are on the movements list; and **`attachments`**, its files,
  newest first. An unknown lot is the 404.
- **`PUT /lots/{id}`** with **`If-Match`** replaces the supplier lot (≤ 40), the
  production, best-before and expiry dates and the note (≤ 2000), **whole**: a field left
  out is cleared. The number and the variant never change, and no other property is
  taken. It takes the lot `FOR NO KEY UPDATE`, answers `revision_conflict` (409) on a
  stale revision, moves the revision on and records `lot_updated` with the fields that
  changed. The dates must be in order — an expiry before the production date, or a
  best-before date after the expiry, is **`lot_dates_invalid`** (400, a field error on
  `expiry` or `bestBefore`); a date left out is not compared. A changed expiry is judged
  from the next posting on; nothing already posted is undone.

### The lot page's holds

**`POST /lots/{id}/holds`** `{kind, cause, note?}` puts a hold whose source is the lot
itself, **`inventory/lot/<id>`**, through the same code as `HoldLots`: the lot
`FOR NO KEY UPDATE`, the hold, the `lot_held` event and the status derived again. The lot
page holds a lot **at most once per kind**: a second quarantine, or a second block, while
the page's own is active is **`hold_exists`** (409) and nothing is written — another
module's holds do not count — and the page disables that kind's button, saying why, until
the hold is released. (`HoldLots` itself leaves a repeat of its own source as is.) **`POST
/lots/{id}/holds/{holdId}/release`** `{note?}` releases **one** hold, with its
`release_note`, under the lot's lock, records `lot_released` and derives the status
again. It releases only a hold whose source module is Inventory — the lot page's, or a
receipt's inspection (Q): a hold another module's document put is shown with its source
and released there, **`hold_not_inventorys`** (409). A hold released already answers the
lot as it is; another lot's hold is the 404. Both need `lots-manage` and answer the lot's
page. Since T the release takes **`downstream`**: on the lot page's own **block**, it also
releases every active block of the same source — `inventory/lot/<id>`, which is what a
[block with its downstream list](#block-with-the-downstream-list) put on the lots made
from the lot — every lot taken `FOR NO KEY UPDATE` in id order, each hold with the note
and its own `lot_released`. Holds of any other source stay; on any other hold
`downstream` is ignored.

### Inspection on receipt

A variant's rules (manufacturing phase 2 M100): **`GET /variants/{variantId}/rules`**
(both views) answers `inspectOnReceipt` and the `revision`, `0` while there is no row;
**`PUT`** `{inspectOnReceipt, revision}` (both views and `stock-adjust`) locks the variant
through `LockVariants` first — so its tracking is judged as it is, and a tracking change
waits — and refuses `inspection_requires_tracking` (400) for a variant that is not
tracked; a stale revision is `revision_conflict`, and a variant the catalog does not know
is the 404.

A **receipt** reads, under the variants' lock, which of its tracked variants are
inspected. Their lots are locked `FOR NO KEY UPDATE` at step 5, as a hold takes them,
and after the balances every lot a posted line received into gets a hold through the
same work as `HoldLots`: kind `quarantine`, cause `inspection`, source
**`inventory/receipt/<movement id>`**, its `lot_held` event, the status derived again.
Received into a lot that exists, the hold is a new one, from this receipt: the whole lot
is in quarantine until it is released, an earlier receipt's accepted quantity with it — a
lot is one quantity. The movement answers **`inspectionLots`**, the lots its receipt put
on hold, and the screen says so. Moves, adjustments, counts, transfers and imports are not
receipts and put none. The hold is Inventory's, so the lot page releases it
(`lots-manage`). The inspection queue is every lot with an active inspection hold,
whatever its status (a recall's block on top makes it `blocked`) or stock: `GET /stats`
counts it as `lotsAwaitingInspection`, and the home page's link opens the lots list with
`holdCause=inspection&stock=all` — the same predicate. A reservation of the item stays
while the lot is held; a delivery's FEFO proposal and AutoLot skip the held lot.

### A lot's files

A lot keeps up to **20** files (`inventory.lot_attachments`, migration 00164): an EN
10204 3.1 certificate, an analysis, a photo. Customers' attachments are the model
([Customers](/en/reference/customers/#attachments)), copied because no module imports
another.

- **`POST /lots/{id}/attachments`**, multipart with one part `file`: a **PDF, JPEG or
  PNG** whose own bytes decide its type and must not be contradicted by the type or the
  extension it is sent under (`attachment_type`, 415), at most **20 MiB**
  (`attachment_too_large`, 400; the router caps the request just above), and at most 20 a
  lot, counted under the lot's lock (`too_many_attachments`, 400). The object is written
  to the store **before** the row's transaction and removed again by whatever refuses
  after it; the row keeps the file name — its path and control characters stripped, at
  most 255 characters — the type, the size and the uploader, moves the lot's revision on
  and records `lot_attachment_added`. It answers 201 with the file. The assistant never
  calls it (`x-vantigo-assistant: never`).
- **`GET /lots/{id}/attachments/{attachmentId}`** reads the row on the pool and streams
  the object with its stored type, as an **attachment** named as uploaded (RFC 5987) —
  saved, never shown in place, since a browser's PDF viewer does not run under the
  policy — never cached, never sniffed, under `default-src 'none'; sandbox`. A file of another lot is the 404 an
  unknown id is. No path is ever returned.
- **`DELETE /lots/{id}/attachments/{attachmentId}`** removes the row and queues its
  object in **`inventory.object_deletions`** in one transaction under the lot's lock,
  records `lot_attachment_removed`, then makes one attempt at the object once that has
  committed; 204.

The store is `Deps.ObjectStore` when a harness set one, else the one the configuration
builds, under the scope **`inventory-lots`**, keys `lots/<uuid>` that name no lot
([object storage](/en/admin/object-storage/)). An upload or a download with no store
configured answers **`storage_not_configured`** (503) and records nothing; a store that
fails answers 503 without a code. The store is never reached inside a transaction.

**The deletion worker.** A removal the store refuses stays on the ledger, its attempt
counted and its reason recorded, for the worker **`inventory.object-deletions`**
(every five minutes, in worker mode through `module.Workers`): each pass selects the due
rows, a hundred at a time, claims each by a conditional update with a five-minute window
— so two replicas never attempt one row at once — and makes one attempt, retrying an
object two seconds after its first failure, doubling, at most an hour apart, until the
store confirms; a key the store no longer has is confirmed too. Every statement takes a
pooled connection for itself, none is held while the store is asked, and a test drains it
built through `module.Workers` on a pool of one.

### Stock per lot

The stock reads carry the lots (M63). On **`GET /products/{productId}/stock`** and
**`GET /variants/{variantId}/stock`**, each site's row has **`held`** — what lies there in
lots on hold or expired today — and **`available`** is **usable** on hand less reserved,
`totalAvailable` likewise; each location row of a tracked variant carries **`lots`**,
`{lotId, number, expiry, bestBefore, status, expired, onHand, receivedAt}` per lot on it,
in FEFO order. These rows are what a form's lot column offers: a receipt the variant's lots
at the site, a move the lots on its *from* location, an adjustment those on its location,
every one of them; a move or an adjustment proposes the first usable lot there in FEFO
order — expiry, none last, then `receivedAt`, then the id — once its location is chosen. **`GET /stock/sites`** answers `available` as usable less reserved too, and a
placement's **`low`** everywhere it is judged on usable on hand. `onHand` stays everything
that lies there. Today is read on the pool before the snapshot.

The variant reference every answer names a variant with carries its **`tracking`** —
`none`, `lot` or `serial` — so the forms show the lot column for a tracked variant only.

### The expiring list

`GET /lots/expiring?days=…&siteId=…&status=…` (manufacturing phase 2 M72) lists every lot
**holding more than 0** — at `siteId` when it is given, where `onHand` is counted too —
whose **expiry is on or before today + `days`** in the workspace zone (`days` 1–365,
30 by default), so the lots already expired are always in it; filtered by `status`; by
expiry, then number and id; at most **1000**, `more` saying there were more. It answers
`today` and `until` (the last expiry day it reaches) beside the lots, each in the lots
list's shape with `expired`. A lot with no expiry is never listed; best-before alone
expires nothing (M62). `GET /lots/expiring.csv` answers the same rows as
`expiring-<today>.csv` — `lot`, `sku`, `product`, `supplier_lot`, `expiry`, `best_before`,
`days_left` (the expiry less today, negative once passed), `status`, `on_hand` — the
house's CSV: semicolons, CRLF, a byte-order mark, decimal commas and the formula guard.
Both need both views. The dashboard's **`lotsExpiring`** and **`lotsExpired`** read the
same rows ([stats](#stats)).

### The lots' live topics

Inventory's first live topics (§17; [live updates](/en/contributing/platform-packages/#live-updates)):
**`inventory.lots`**, keyless, and **`inventory.lot:<id>`**, both authorised by both views,
a key that is not an id refused alike whether the lot exists or not. Each carries
`lot.changed`: the list's **once per transaction, without an id**, and each lot's page's
with the lot's id. **Every posting** tells the list once and each lot its lines touch, in
**one statement** (`live.NotifyAll`) — a receipt of 500 serials is one round trip and 501
events, not a thousand — on the posting's own transaction — whichever module's it is, since a notification
is a statement on that transaction and not a call out — and so does every hold or release
(the lot page's and `HoldLots`/`ReleaseLotHolds`), an edit and a file added or removed.
The list and a lot's page refetch on them and poll every 30 seconds while the socket is
down.

### The lock order

Inventory's postings take, in order (`posting.go`): the key's read; `LockVariants`;
**the lot counter**, only to generate a number; **the new lots**, in (variant id,
lower(number)) order; **every lot named**, AutoLot's candidates included, in id order —
`FOR SHARE`, or `FOR NO KEY UPDATE` for a serial lot a line increases; then the
movement's header, the locations `FOR SHARE` and the balances in **(variant, location,
lot)** order, no lot first. The lots come before the header and the locations for every
writer, so no posting holds a location and then waits for a lot. A caller's
`EnsureLots` runs the counter, the new lots and the lot locks — every lot `FOR NO KEY
UPDATE` — and its `Produce` runs the rest. A hold or a release — another module's or the
lot page's — takes the lots `FOR NO KEY UPDATE` in id order, then the holds; an edit of a
lot and a change of its files take the lot the same way. A reserve's `LockSiteBalances`
orders as the engine does, no lot first. A test
races issues, receipts into a lot and holds on it on a pool of four.

## Traceability

Manufacturing phase 2's layer T (M87–M94): where a lot went, where it came from, who
received it, and the recall that blocks it. **No migration**: the graph is read from the
movement lines that carry a lot, over L1a's indexes.

### The graph

- **Nodes** are lots. **Edges** are production movements: in one movement of kind
  `production`, every lot on a **consume** line (a line with only a *from*) is a parent of
  every lot on a **produce** line (only a *to*). A by-product's lot (G) gets the same
  parents. No genealogy table is kept: the movement lines are the record (R §4).
- **An unbuild is a reverse edge** (kind `unbuild`, M123): it is shown on both lots as an
  end — on the finished lot's usage as *Unbuilt by unbuild …*, on a restored component
  lot's origin as *Returned by unbuild …* — and is **never expanded**: the usage walk does
  not follow it into the restored lots, the origin walk not into the finished lot, and a
  [block](#block-with-the-downstream-list) does not cross it.
- **Ends** are the movements that start or end a lot's path. On the **origin** side: a
  receipt (with its `supplierName` and `supplierReference`), an adjustment or a count up
  (the opening-balance import among them), and an unbuild that restored it; a production
  that made the lot of nothing traced is an end too. On the **usage** side: an issue — a
  delivery, `source` `orders/delivery/<id>` —, an adjustment or a count down, a
  `transfer_out`, an unbuild that took it apart, and a production that consumed it but
  made no lot. A move, which has both a *from* and a *to*, changes nothing of a lot's
  path and is not read; a `transfer_in` neither.

### The trace

**`GET /trace?lotId&direction=usage|origin&components=tracked|all&maxDepth`** answers the
tree, read on the pool in **one snapshot** (`REPEATABLE READ`, read-only).

- **`usage`** (the default; *where it went*) follows a lot's consume lines into the lots
  produced beside them, recursively, with every lot's ends. **`origin`** (*where it came
  from*) follows a lot's produce lines back to the lots consumed beside them, with their
  ends. **`components=all`** also lists, on the origin side, the untracked lines each
  production consumed, as `component` leaves without a lot (summed per variant);
  `tracked`, the default, lists lots only.
- **The walk** reads one level at a time: the lines of the level's lots
  (`ix_movement_lines_lot`), then every line of the productions they cross
  (`ux_movement_lines_movement_line`), then the next level's lots — two statements a
  level, at most `maxDepth` levels (1–10, default 10). Then the lots themselves and what
  each holds now (`ix_balances_lot`).
- **The tree** is laid out depth first, each node after its parent, keyed by its path
  (`0`, `0.2`, `0.2.1`), its movements in the order they were posted. **A lot reached
  twice is expanded once**, where it is first laid out; every later reference carries
  **`seenAt`**, the first one's key, and no children — so a cycle cannot loop either. A
  lot at `maxDepth` still shows its ends but no production; it carries `depthLimit` only
  when a production on the walk's side — one that consumed it (usage) or made it
  (origin) — was left unexpanded, so a lot at the limit with nothing beyond it is not
  marked.
- **At most 2,000 lots** (D1 M15a's bound, M88) — found by the walk, and laid out as lot
  nodes, a `seenAt` reference counted — and **at most 20,000 other nodes**, the ends and
  the components, so a lot delivered thousands of times still traces: beyond either
  **`trace_too_large`** (400).
- **Every node** carries its variant as the lines snapshotted it. A **lot** node carries
  the lot — number, serial, status, dates, supplier lot — with **`quantityIn`** (what was
  received, produced or adjusted in under it, every movement; a transfer's arrival not)
  and **`onHand`** (now, every location); a lot or component reached through a production
  carries its **`edge`**: the movement, when it was posted, its source (the production and
  its manufacturing order), **`consumed`** (the parent's quantity used in it) and
  **`produced`** (the made lot's quantity in it). An **end** carries its **`movement`** —
  id, kind, when, reason, supplier, source, and for a `transfer_out` **`toSiteName`**,
  the site its transfer was sent to — and **`quantity`**, the lot's on it. A lot sent to
  another site and delivered there shows both: the `transfer_out` and, beside it, the
  issue at the other site, for a lot's lines are read at every site.

### Who received it

The trace's issue ends for Orders' deliveries are read through
**`contracts.DeliveryTrace`** once per trace (M90). With **`orders:access`** each carries
its **`delivery`** — the delivery's and the order's ids and numbers, the customer's id,
when it was posted — and the trace says **`deliveries: shown`**; without it Orders refuses
with `ErrForbidden` and the trace says **`hidden`**: the delivery is its movement alone,
`source` `orders/delivery/<id>`. The refusal is explicit, not an empty answer, so
Inventory never shows *no deliveries* to someone not allowed to see them. Without Orders
the trace says **`unavailable`**. A customer's **name** is added only when the deliveries
are shown and the caller holds **`customers:view`** (**`customerNames`**); without it the
exports print *Customer #<id>*.

### The exports

**`GET /trace.csv`** and **`GET /trace.pdf`** take the trace's parameters and answer the
same nodes, as `trace-<lot number>-<direction>.csv` and `.pdf`, never cached (M91). The CSV
has one row per node — `key`, `depth`, `kind`, `lot` (an end's lot is the lot it ends),
`sku`, `product`, `quantity` (the root's put in, a lot's produced on a usage edge or
consumed on an origin edge, an end's or a component's own), `unit`, `date` (in the
workspace's zone), `document` (*Delivery 3001, order 2001*; *Delivery, movement 6001* when
hidden; *Receipt, movement …, ref …*; *Production 1, manufacturing order 5*; *Unbuild …*),
`party` (the supplier, the customer — named only under `customers:view` — or, for a
transfer out, *Sent to <site>*) and `note`
(*Already shown at 0.0.0*, *Not traced further: depth limit*) — the house's CSV:
semicolons, CRLF, a byte-order mark, decimal commas and the formula guard. The PDF is the
house style — maroto and the labels' Noto Sans — in Norwegian and English together, and
states the lot, the direction, when it was made and by whom, and when the deliveries were
hidden or unknown, for an authority's *within hours* answer.

### Block with the downstream list

**`POST /lots/{id}/block`** `{cause: recall|complaint|other, note?, downstream?}` (M92),
`lots-manage`:

1. Without `downstream`, the lot is taken **`FOR NO KEY UPDATE`**.
2. With **`downstream`** (the default), the **usage walk runs again on the block's own
   transaction**: the dialog's summary was a trace read on the pool and may be stale,
   and a production posted since may have made another lot of it. The block's walk reads
   lots only — it lays no tree out, so a lot's deliveries do not count — and has **no
   depth limit**: every lot made from it however deep, at most 2,000, beyond which
   **`trace_too_large`** and nothing is blocked. It is read **before any lock**; then
   every lot it found, the lot itself among them, is taken **`FOR NO KEY UPDATE` in id
   order** — the lock a production consuming or making one of them takes — and the walk
   is read again under those locks. A lot made meanwhile by a production that held its
   locks first is locked too and the walk read once more, at most three times; past
   that, **`trace_changed`** (409) and nothing is blocked. A block that loses a deadlock
   runs once more.
3. One **`block`** hold of the lot page — source **`inventory/lot/<id>`**, the cause and
   note as given — is put on the lot and on **every lot the walk reached that still holds
   stock**, through the code of `HoldLots`: the lots `FOR NO KEY UPDATE` in id order, a
   `lot_held` event per lot naming the source, the statuses derived again, the lots' live
   topics told. A lot already blocked by this source is left as is, so a block may be
   asked again to catch the lots made since. A lot that holds nothing is not blocked.
   Nothing crosses an unbuild.

It answers **`InventoryLotBlock`**: the lot's page and **`blocked`**, the lots it holds —
the lot first (`root`), then the others in id order — which the dialog compares with its
summary to mark a lot it did not show as new. The walk's lots in id order, and those
made meanwhile after them, are the block's [lock order](#the-lock-order). The
[release with `downstream`](#the-lot-pages-holds) undoes it; a hold of any other source
stays. Recall campaigns — contact logs, returns — are phase 3.

## Entering in packagings

A variant may have up to ten **packagings** in Products — a box of 15, a Euro pallet of
40 boxes — each holding a quantity of the variant's unit, derived when it is built from
another ([Products' packagings](/en/reference/products/#packagings)). **Stock is always
kept in the variant's own unit** (I24); packagings are a way to enter it and to read it.

### The entry

Every quantity input — an adjustment's, a move's, a receipt's and a count's line —
carries **exactly one** of `quantity` (a count's `counted`) and **`entered`** (I25):

```json
{ "entered": { "packagings": [{ "packagingId": 1003, "count": 3 }, { "packagingId": 1001, "count": 2 }], "loose": 4 } }
```

- **Read with the request**: both or neither of the two, a packaging named twice, a
  `count` below 1 or more than ten packagings is `entered_invalid` on the line; a
  `loose` rest below 0 or with more than three decimals is `quantity_invalid`.
- **Resolved at posting**, inside the posting's transaction, against the packagings
  `LockVariants` answers — so a packaging's quantity cannot change between the
  resolution and the commit, since Products' packaging writes take the variant's row
  `FOR UPDATE`. The base quantity is Σ `count` × the packaging's quantity + `loose`,
  exact. A packaging the variant does not have (any more) is `packaging_not_found`; a
  total above 99,999,999,999.999 is `quantity_invalid`, and so is a total of 0 where
  more than 0 is needed.
- **Kept as a snapshot** on the line — `movement_lines.entered`, `count_lines.entered` —
  with each packaging's id, name, count and quantity as they were, and the loose rest,
  so a movement still reads *2 Euro pallet + 2 Box* after the packagings change. A
  movement line and a count line answer it as `entered`.

How each kind takes it: a **receipt**'s and a **move**'s line take the total as the
quantity; an **adjustment**'s `change` line adds it, or takes it away with
**`decrease: true`** (a `change` given as a quantity carries its own sign, so
`decrease` with a quantity is `entered_invalid`); a `set` line makes the total the new
on hand (`decrease` under `set` is `entered_invalid`); a **count**'s line records the
total as counted, resolved when the line is recorded rather than at posting.

### The breakdown

Every variant a response names carries its `packagings` — id, name, kind, quantity
and barcode — and the app breaks a base quantity down from them (I26), greedily, the
largest packaging first: 1,830 with a pallet of 600 and a box of 15 reads
*3 Euro pallet, 2 Box*, beside the base quantity, which is always shown. The breakdown
is the app's: the server answers the base quantity only. It is drawn in the stock
tables (a location's contents, a variant's stock), the product page's Stock card and
the count screen.

## Counts

A **count** (I16) covers a site — its root's subtree — or one location and everything
below it, and has a status, `counting`, `posted` or `cancelled`, an optional note, and
who opened it and when. Every operation on counts needs `counts-manage` and both views.

### Opening

`POST /counts` takes `siteId`, an optional `locationId` (the site's root when absent)
and a `note`, and answers 201 with the count and its lines. Under **the site row
`FOR UPDATE`**, so two opens in one site are serial:

1. The scope must be an **active location of the site** — `location_not_found` or
   `location_archived` as a field error on `locationId`; an unknown site is a field
   error on `siteId`.
2. A count still `counting` whose scope is the same location, a location **above** it
   or one **below** it refuses the open: 409 **`count_scope_overlaps`**, the detail
   naming the other count and its scope (I17).
3. One line is **frozen** per balance in the scope, `expected` being the balance at
   that moment — **per lot** for a tracked variant (manufacturing phase 2 M69), the
   line naming its lot. A scope with no balances opens with no lines: every line it gets
   is added, which is how stock is first placed (I20).

**Movements are not blocked while counting**: stock may still be received, moved and
adjusted in the scope; what moved is caught at [posting](#posting).

### Counting

`PUT /counts/{id}/lines` records up to **500** counted lines a request
(`too_many_lines` beyond), each a variant, a location and **`counted`** (0 or more, at
most three decimals) or an [entry](#the-entry) (I18):

- a pair that has a line changes its `counted`; a pair with none gets a new line,
  `expected` 0 and **`added`**;
- each line written records **who counted it and when**; a pair named twice in one
  request takes its last value;
- `counted` stays changeable until the count is posted — a `PUT` is not a submission;
- the request is refused whole when any line fails, every failing line answered: a
  location outside the count's scope is `location_not_found`, an archived one
  `location_archived`, an unknown variant `variant_not_found`, a Service variant on an
  **added** line `variant_not_stockable`, plus the entry's codes;
- a line whose location is deleted while the request runs is `location_not_found`;
- a count no longer counting is 409 **`count_not_open`**.

**Lots (M69).** A line is one balance, `(variant, location, lot)`. A line of a tracked
variant names its lot: **`lotId`**, one of the variant's (`lot_unknown` otherwise), or
**`lot`** — `{number, supplierLot, productionDate, bestBefore, expiry, note}` — found or
made by number as a [receipt's](#receipts-moves-and-adjustments-with-lots) is, in the
`PUT`'s own transaction, with origin `inventory/count/<count id>`; both is a field
error. A lot a count makes takes **only the dates the line states**: the variant's
best-before and expiry days are not applied, since nobody knows the day a counted lot
was made. A new lot that does not read refuses **its line**, not the request:
**`lot_invalid`** for a number that is not 1–20 characters `A–Z a–z 0–9 . _ / -` (or a
supplier lot or note too long), `lot_dates_invalid` for dates out of order — each with
the line's index, so the screen marks the row. An untracked variant's line naming one
is `lot_not_allowed`; a tracked variant's line naming none is `lot_required`, unless the
count already has that line without a lot (frozen before the variant became tracked),
which may still be counted. A serial is counted **0 or 1 exactly** — 2 or 0.5 is
`serial_quantity` — and the post's set to a serial's lot is held to the same. The lots
are taken after `LockVariants`: the lot
counter and the new lots, then every lot named `FOR SHARE` in id order (M58), then the
locations.

No event is recorded per `PUT`; the count's `revision` is bumped by every `PUT`, post
and cancel.

`GET /counts/{id}` answers the count with its lines **by the location's full code,
then SKU**: each line's `expected`, `counted` when set, `current` — the balance now —
`added`, `entered`, the counter, and **`moved`**: the balance now differs from what the
line expected. `moved` is reported **only while the count is counting**; a posted or
cancelled count reports none. The count carries `lineCount`, `countedLines` and
`movedLines`. `GET /counts` lists the counts newest first, at most 200, without their
lines, filtered by `status` and `siteId`.

### Posting

`POST /counts/{id}/post` takes **`uncounted`** — `keep` or `zero`, required — and
**`acceptMoved`** (I19). It posts **one `count` movement**, with no line cap:

- every **counted** line is posted as a **`set`** of its location's on hand — of its
  lot, for a tracked variant (M69) — to the counted quantity, so the engine computes the difference against the balance it has
  locked — the difference between counted and the balance **at posting**, not
  `expected`;
- an **uncounted** line is left out under `keep`, and set to 0 under `zero`;
- a line that **posts** and whose locked balance differs from its `expected` is
  **moved**: the post is refused 409 **`count_lines_moved`**, every such line in
  `lines` — its `index` is its place in `GET /counts/{id}`'s order, the detail its SKU,
  full code, expected and on hand now — unless `acceptMoved` is true. An uncounted line
  under `keep` posts nothing and is not judged;
- the movement's `clientRequestId` is a UUIDv5 of the count's id, and the count row's
  lock and status make a second post `count_not_open` anyway;
- a line taking a balance below zero is `insufficient_stock` (a `set` never does), and
  a counted variant the catalog no longer knows is `variant_not_found` — rare, since
  Products refuses to delete a variant in stock.
- **A count opened before its variant became tracked** (M69): its lines of that variant
  have no lot. When every such line expected 0 and counted 0 (or is uncounted) they are
  left out — posted as they are, which is nothing; otherwise the engine refuses them
  **`lot_required`** and the variant must be counted again in a new count. The post takes
  the variants (`LockVariants`) after the count row to judge this, as the engine takes
  them again.

The count becomes `posted`, keeping `uncounted`, the poster and **`movementId`**. A
count whose lines change nothing — every counted quantity equals its balance, or nothing
was counted under `keep` — is **posted with no movement**: `movementId` is absent, and
the count records that the stock was right. (The engine's attempt runs under a
savepoint, so its claimed key is rolled back with it.)

### Cancelling

`POST /counts/{id}/cancel` cancels a count still counting (`count_not_open`
otherwise); nothing is posted and its lines stay as the record of what was counted. The
scope can be counted again. When the catalog cannot be read for the answer, the cancel
answers 503 `dependency_unavailable` — the count is cancelled even then.

### Events and history

Each count records **`count_opened`** (the scope and the number of frozen lines),
**`count_posted`** (`movementId`, `uncounted`, `acceptMoved` and the number of lines)
and **`count_cancelled`**. While a count is counting, an archive of its scope, of a
location below it or of one above it is `location_has_open_count`, and so are a
re-parent that moves a location into, out of or within its scope and a site's archive
while any count in it is counting. A count names its scope and its lines'
locations for ever: such a location, and a site with any count, is
`location_has_history` on delete.

## Labels

`POST /labels` (I7) answers a **PDF** of location labels — `Content-Disposition:
attachment; filename="location-labels-<layout>.pdf"` — for `locationIds` (at least one)
on a `layout`:

| Layout | Sheet | Label | Barcode width |
| --- | --- | --- | --- |
| `l7160` | Avery L7160, 3 × 7 = 21 per A4 | 63.5 × 38.1 mm | 80 % of the label |
| `l7163` | Avery L7163, 2 × 7 = 14 per A4 | 99.1 × 38.1 mm | 60 % of the label |

- Each label carries the location's **token**, `LOC` and its id (`LOC1042`), as a
  **Code 128** barcode across its top, the **full code** in bold below it, and the
  **name** under that when it has one. The token survives renames, recodes and
  re-parenting, uses only capitals and digits so it scans back on any keyboard layout,
  and is what the [lookup](#the-lookup) and the count screen read.
- The labels are laid out row by row in **full-code order**; the ids are
  de-duplicated. A location asked for is printed **whatever its status**; with
  `subtree: true`, every **active** location below each one asked for is printed too.
- At most **1000** labels: `labels_too_many` (400) is checked on the ids asked for —
  the detail names how many they would print — and again after the subtree is added,
  when it says the request would print more than 1000. A location that does not exist is a field error on
  `locationIds` with the code `location_not_found`.
- It needs **`locations-view` only** ([permissions](#permissions)).

The PDF is drawn with maroto and the Noto Sans fonts Invoices embeds, with fixed dates.
The notice that a label's printed text is stale after a rename (I7, §9) is not built.

### Lot labels

`GET /lots/labels.pdf?ids=…&layout=…&copies=…` (manufacturing phase 2 M73) answers a PDF
of **lot labels** on the same sheets — `layout` `l7160` by default — as
`lot-labels-<layout>.pdf`. Each label carries the lot's **token**, `LOT` and its id
(`LOT1001`), as the Code 128 across its top — which the [lookup](#the-lookup) reads back
— the lot **number** in bold below it, the SKU and product name (`MILK-1 · Milk`), and
the **expiry** (`EXP 2026-12-31`), else the **best-before** date (`BB …`). The SKU and
name are cut to **one line** with an ellipsis, measured in the label's font, so they
never run into the date below. The labels are
in the order the ids are given, `copies` (1 by default) of each in a run; a serial is a
lot of its own, so serials print one label each. At most **1000** labels — `ids` × `copies`
— else `labels_too_many`; a lot that does not exist is a field error on `ids` with the
code `lot_unknown`. It needs both views.

**`format=gs1`** (M93) prints **GS1-128** labels instead, as `gs1-lot-labels-<layout>.pdf`.
The barcode's element string is **FNC1** — the rune `ñ` (`code128.FNC1`) in the text maroto
hands boombuler's encoder, which opens the code in set C — then **(01)** the GTIN-14, when
the variant's barcode (read through the catalog) is a valid GTIN-8, -12, -13 or -14,
left-padded with zeros and its check digit verified; **(11)** the production date when set;
**(17)** the expiry when set; **(15)** the best-before when set and there is no expiry; and
**(10)** the lot number, or **(21)** the serial for a serial lot, **last**, so no separator
follows it. The fixed-length fields need no FNC1 after them, so the string holds one, at
its start. Under the barcode the label prints the AIs in brackets —
*(01)04006381333931(17)261231(10)B1* — then the lot number, the SKU and name and the date
as the internal label. **Without a valid GTIN** (10) and (21) are not valid, so the lot
gets the internal label, marked *No GTIN — not a GS1 label*. A lot number of up to 20
characters always fits (M52). The **bars span the label** but for a quiet zone of ten
modules on each side, so the module — the X dimension a scanner must resolve — is as wide
as the label allows, and stand up to 15 mm tall as the lines under them leave room (about
14.7 mm on L7160, 13 mm on L7163). The module follows from the element string's length:
a lot with two dates and a short number gets about 0.35 mm on L7163 and 0.22 mm on L7160,
a 20-character number much less; GS1's general minimum is 0.25 mm, so L7163 is the sheet
for scanning partners. Without the catalog, `dependency_unavailable` (503). ZPL is
phase 3. A test decodes the bars boombuler draws back to the string, with a table read
from the encoder's own symbols.

## The opening-balance import

`POST /imports/opening-balances?siteId=…&dryRun=…` takes a **CSV** body (`text/csv`) and
places a site's first stock (I32). It needs `stock-adjust` and both views.

- **The file** is read by `srv/fileimport` from its bytes as sent: UTF-8 or
  Windows-1252 (as Excel saves it), the separator `;`,
  `,` or tab detected, at most **4 MiB** and **5000 rows**. Its header names **exactly**
  the columns `location`, `sku` and `quantity`, in any order and any case. A file that
  does not read, another header, too many rows or an unknown site is a 400 field error
  (*Invalid import*) on `file` or `siteId`. The header **may** also name the lot columns
  `lot`, `supplier_lot`, `production_date`, `best_before` and `expiry` (manufacturing
  phase 2 M71), and nothing else.
- **Each row** is resolved before any transaction, in the order location, variant,
  stockable, quantity, the first failure being its outcome. Checked before all of them:
  a row with a non-blank cell beyond the header's three columns is `quantity_invalid`,
  *The row has more cells than the header. Put a quantity with a decimal comma in
  quotes, such as "2,5", or separate the columns with ;* — a comma-separated file
  splits an unquoted `2,5` in two. Blank trailing cells are ignored.
  - `location` — a **full code**, upper-cased, among the site's locations; one that is
    not there, or is archived, is `location_not_found`;
  - `sku` — through the catalog's `LookupSKUs`, a **SKU match only** (a barcode, the
    variant's or a packaging's, does not count): `variant_not_found`;
  - a Service product's variant: `variant_not_stockable`;
  - `quantity` — a decimal more than 0, at most three decimals, `.` or `,` as the
    decimal separator, **no** thousands separator, sign or exponent, at most
    99,999,999,999.999: `quantity_invalid`;
  - **the lot** (M71): a tracked variant's row without a `lot` is **`lot_required`**,
    an untracked variant's row with any lot cell filled **`lot_not_allowed`**; a lot
    number not 1–20 of `[A-Za-z0-9._/-]`, a supplier lot over 40 characters, a date that
    is not `YYYY-MM-DD` or `DD.MM.YYYY`, details without a number, or dates out of order
    (an expiry before the production date, a best-before after the expiry) are
    **`lot_invalid`**; a serial variant's row of a quantity other than 1, or naming a
    serial an earlier row of the file names already, is **`serial_quantity`** — judged
    in the dry run too, on the later row.

  Each row answers its `row` (the header being row 1), the `location`, `sku` and
  `quantity` as written — and `lot` when the file has the column — its `outcome` and a
  `detail`.
- **A dry run** — `dryRun` defaults to **true** — answers 200 with every row and `ok`,
  true when every row is `ok`. Nothing is written.
- **A commit** (`dryRun=false`) with any failing row posts **nothing** and answers
  **200 with `ok: false`** and the rows. With every row ok it posts **one `adjustment`**,
  reason `opening_balance`, every row a `change` adding its quantity, through the
  engine with **no line cap**, and answers **201** with `movementId`. A row's lot is
  found or made by number as a [receipt's](#receipts-moves-and-adjustments-with-lots)
  is — its dates defaulted from the variant's days on creation, today read on the pool
  first — with origin **`inventory/import/<movement id>`**; a lot the engine refuses
  (`lot_dates_differ`, or a serial already in stock, `serial_quantity`) refuses the
  commit, 400 with the row's index in `lines`. This is reading 4's path for stock
  already on hand: set the variant's tracking at zero stock, then import it with lots.
- **Its key** is a UUIDv5 of the SHA-256 of the file's bytes and the site's id, so the
  **same file posted again to the same site** is the same movement: it answers **200**
  with the same `movementId` and posts nothing. A different file — even one row changed
  — is a new movement.

The import resolves its SKUs in **one batch**: a single `LookupSKUs` call for every
distinct SKU of the file, before any transaction, on the dry run and the commit alike.
A SKU two variants match ignoring case resolves to the first by id.

## Reservations and issues

Orders reserves stock for a confirmed order's goods lines at the order's site and
delivers it with an issue (orders design O45–O47), through
[`contracts.StockLedger`](#contracts-provided) (`ledger.go`), always **on Orders' own
transaction**: Inventory begins, commits and rolls back nothing. A reservation is held
by a **source line** — `orders/order/42`, line 7 — at a **site**.

**Available** at a site is its **usable** on hand, summed over every location of the
site, less every reservation there (manufacturing phase 2 M63): the balances with no
lot, and those of [lots](#lots) with no active hold that are not expired on the `today`
the caller passes. Across sites it is the sum of the sites'. Only a reserve checks it;
`Stock.Available` and the [low-stock list](#the-low-stock-list) read the same figure, on
the workspace's today. Reservations stay per site, with no lot: a hold put on stock
already reserved lowers available, which may go negative, and the reservations stay.

- **`Reserve`** refuses, naming every failing line, a site that does not exist
  (`site_unknown`), one archived or not fulfilling orders (`site_not_fulfilling`), a
  variant the catalog does not answer or that is not goods (`variant_not_stocked`),
  and a line whose quantity the site's available — less what the call's earlier lines
  of the same variant took — does not cover (`insufficient_available`, naming the SKU,
  the site and what is available). Otherwise it adds each line's quantity to the line's
  reservation at the site, creating it when there is none. A line id names one variant: a
  line already holding another variant at the site is a failure, not a refusal, and
  nothing is written — the caller releases the old variant's reservation first.
- **`Release`** gives back reservations of the document: all of them when no release
  is named; a line's whole reservation for a release without a quantity; or that
  quantity of it, site by site in site id order. Never below zero: releasing more than
  is held releases what is held, and a line holding nothing is no refusal.
- **`Issue`** posts one movement of kind `issue` at the site through the
  [posting engine](#locks), one line per issue line — taken off its from-location — and
  records the document it was posted for (`StockIssue.Document`, the reserving document
  when it is zero) as its source reference and the poster given. It refuses a site as
  `Reserve` does, a variant not goods (`variant_not_stocked`), a from-location that is
  unknown, archived or in another site (`location_unknown`), and stock the location does
  not hold (`insufficient_stock`, Inventory's own rule: a balance never goes below
  zero). Reservations of other documents never stop an issue. Then it consumes, of each
  issued line's reservation at the site of the variant issued, as much as the line issued,
  never below zero; an issue of another variant than the line holds consumes nothing.
  **An issue is idempotent by its document**: its key is a UUID derived from the
  document, so a second `Issue` for the same document answers the first movement and
  consumes nothing again when the body is the same (the same lines in the same order, the
  same quantities as text); one with another body is refused `document_reused`.
- **`ReservationsFor`** reads what the document holds, by line id then site id;
  **`ReservationsForSources`** reads it for many documents in one statement, by source,
  line id then site id, a document named twice answered once, more than 1000 a failure.
  Both **take no lock**, and must not: Orders reads them in a read-only transaction
  ([Unreserved demand](/en/reference/orders/#unreserved-demand)).

**Inventory's own decreases ignore reservations.** An adjustment, a move and a count are
corrections of fact: they may take on hand below what is reserved, and available then
shows negative — on the reads, and in red with a warning on the product page's Stock
card and the variant page.

## Production

Manufacturing reserves a manufacturing order's components and posts what a production
consumed and produced (manufacturing design M1–M3) through the same ledger, on its own
transaction. The reserving document is the order — `manufacturing/order/12` — and the
production is its own document, `manufacturing/production/30`.

- **`ReserveForProduction`** is `Reserve` without the *fulfils orders* check: a plant
  that sells nothing still reserves its components. Every other check stays — the
  variant is goods and the site's available covers the line — and the site need only
  exist (`site_unknown`) and be active: an archived one is **`site_archived`**.
  `Release`, `ReservationsFor` and the product page's *Reserved* figure treat these
  reservations as any other.
- **`Produce`** posts one movement of kind `production` at the site through the
  [posting engine](#locks): each consume line taken off its from-location, then each
  produce line put on its to-location, every location in the movement's one site
  (`location_unknown` for one unknown, archived or elsewhere). Its source reference is
  the production record (`StockProduction.Document`, the reserving document when it is
  zero), with **the reserving document's id as its source line id** — the order the
  record produces for, which the movement page links to (M5). The site is judged as
  `ReserveForProduction` judges it. It refuses a production of no lines
  (`nothing_to_post`), a line whose quantity is not more than 0 with at most three
  decimals (`quantity_invalid`, every such line named), a variant unknown or not goods
  on either side (`variant_not_stocked`) — a **Discontinued** variant is still consumed
  and produced — and stock a location does not hold (`insufficient_stock`). A
  production of consume lines only is allowed: a scrap of components. Reservations of
  other documents never stop it. Then it consumes, of each consume line's reservation
  at the site of the variant consumed, as much as the line consumed, never below zero,
  as `Issue` does. **A production is idempotent by its record**: its key is a UUID
  derived from the record in a space of its own, so a second `Produce` for the same
  record answers the first movement and consumes nothing again, provided the body is
  the same: the same lines in the same order, the same quantities as text, the same
  reserving document. One with another body is refused `document_reused`; a production
  posted in parts gives each posting its own record.

The lock order is the issue's: `LockVariants`, the caller's rows, then the engine's
lots and balances in (variant, location, lot) order, and last the reserving document's
reservations. With `Unbuild` the movement is of kind **`unbuild`** and its consume
lines may take held and expired lots (manufacturing phase 2 M123; Manufacturing's
unbuild is a later delivery). A produce line of a tracked variant names a lot made by
[`EnsureLots`](#lots-of-other-modules); a consume line names one, or is `AutoLot`.

## Transfers

A **transfer** moves stock from one site to another (phase 2, I35–I53; `transfers.go`,
`transfer_actions.go`, `transfer_note.go`). It is Inventory's own document — not Orders',
as phase 1's design had it — that sends stock out of its **from site**, holds it
[in transit](#in-transit) on its lines, and receives it at its **to site**: in parts,
with the difference closed, or cancelled back to where it left. Every write needs
`transfers-manage` with [both views](#permissions).

### The document

A transfer has a **number** from the module's own series (`inventory.counters`,
`transfer`, from 1001), taken in the creating transaction — a create that rolls back
gives its number back, a deleted draft's is never handed out again. The app shows it as
*Transfer 1042*, and the [følgeseddel](#the-følgeseddel) carries it as the token
`TRF-1042`. It names a from site and a to site — known, active and different
(`transfer_same_site` on `toSiteId`; an archived one is `site_archived` on its field) —
an optional `reference` (100), `note` (2000) and `expectedArrival` (a date), and
**1 to 500 lines** (none is a field error on `lines`, more `too_many_lines`). A line is a
variant and a quantity, or an [entry in packagings](#the-entry); a variant may appear on
several lines, which is how stock from two source locations travels.

Its **status** is `draft`, `sent`, `partly_received`, `received` or `cancelled`. Its
`revision` is checked by `If-Match` on every write but a [receive](#receive). Who
created, sent and closed it, and when, are snapshotted on the row.

`GET /transfers/{id}` answers the transfer with its sites, every line — its variant (one
the catalog no longer knows named by the line's snapshot), quantity, `entered`,
`suggestion`, source location, and `sent`, `received`, `missing`, `returned` and
`inTransit` — the transfer's `inTransit` summed, and its **actions**, each with its kind,
movement, poster, time, close reason and note, and its lines: what it did to each line,
of which kind, and where.

### Drafts

**A draft holds nothing** (I37): no balance, no reservation, no hold on the variant. It
is created (`POST /transfers`, 201), replaced whole (`PUT /transfers/{id}`, 200) and
deleted (`DELETE /transfers/{id}`, 204) freely, each but the create under `If-Match`. Its
lines are judged against the catalog as it is when they are written, read before the
transaction, every failing line at once: a variant the catalog does not answer
(`variant_not_found`), a Service product's (`variant_not_stockable`), an entry
`entered_invalid`, `packaging_not_found` or coming to 0 (`quantity_invalid`) — an entry is
resolved to units now and kept as its snapshot, with the line's SKU and product name. A
line may name a **`lotId`** (manufacturing phase 2 M70), read on the pool with the
catalog: only for a tracked variant (`lot_not_allowed`) and one of its own lots
(`lot_unknown`); a draft line without one leaves it to the send. A
`PUT` replaces the header and every line, so the lines get new ids. A write to a transfer
that is no longer a draft is `transfer_not_draft` (409); the status is judged **before**
`If-Match`, so a stale revision of a sent transfer reads as what it is. The sites are
taken `FOR SHARE` after the transfer's row and must be active.

### The actions and their order

A **send**, a **receive** and a **cancel** each write one **action**
(`transfer_actions`) and, per line touched, an **action line** with its kind — `sent`,
`received`, `missing` or `returned` — its quantity and its location (I38). The action is
the record of who did what and where each part went; the line's four running totals are
what every read uses. Every action runs these six steps, in one transaction, inside its
own serialization-and-deadlock retry (up to five attempts, as a [posting](#locks)):

1. **The transfer row `FOR UPDATE`** — an unknown id is the 404.
2. **The replay check**: the action's client request id looked up in `transfer_actions`.
   The same transfer and the same request hash answer the transfer as it is now, 200. For
   a client's id, anything else is `client_request_reused`; for a [derived
   id](#idempotency-of-an-action), the status refusal of step 3 first, and only then
   `client_request_reused`.
3. **The status, then `If-Match`** (send and cancel): a cancel after a receive is
   `transfer_not_cancellable` whatever revision it read.
4. **The two sites `FOR SHARE`**, and only then their status: **every action needs both
   sites active** (`site_archived`). An archive that committed first is seen; one racing
   the action waits for it and then finds the transfer open.
5. **The engine's posting** on the same transaction — `LockVariants`, the movement's
   claim, the locations, the balances — with the action's per-line rules judged once the
   [entries](#the-entry) are resolved to units and before any line is applied.
6. **The action**, its lines, the lines' running totals and the status.

**Why the transfer row comes before Products' rows.** Everywhere else Products' rows are
locked before any of Inventory's ([locks](#locks)). A transfer's action takes its own row
first, and its sites, so two actions on one transfer are serial before either touches a
variant, as a [count's post](#posting) takes its count row first. There is no cycle:
Products never waits on a transfer, a count or a site.

A failing line's `index` in the problem's `lines` is, for a send and a cancel, the line's
place on the transfer by line number; for a receive, its place in the request. A request
naming a line not on the transfer (`transfer_line_unknown`) or one line twice is a
**field error** on `lines[i].lineId` (or `receive.lines[i].lineId`), not a line failure.

### Send

`POST /transfers/{id}/send` (I39), with `If-Match`, names each line's **source
location** at the from site: `{lines: [{lineId, fromLocationId, lotId}]}`. A line without one is
`from_location_required`. **The lot** (manufacturing phase 2 M70): a line carries one,
the send's `lotId` or else the draft's; a tracked variant's line with none is the
engine's **`lot_required`**, a lot of another variant `lot_unknown`. The send takes the
lot from the source location as a move does — holds and expiry do not stop it (M56) —
and records it on the line (`SendTransferLines`); the app proposes the first usable lot
there in FEFO order. Each line is resolved again — its entry against the
packagings as they are locked, its SKU, name and unit snapshotted — and checked as a
move's line is: goods (`variant_not_stockable`), the location known, active and in the
from site (`location_not_found`, `location_archived`, `move_crosses_sites`), and on hand
enough (`insufficient_stock`). A Discontinued product's variant may be sent, as it may
be moved. It posts one **`transfer_out`** at the from site, records `sent` for each line
in full with its source location, and the transfer is `sent`, with who sent it and when.

**Reserved stock is a warning, not a refusal**: the send takes on hand, never below zero,
and may take it below what orders have reserved there; the app warns when a line takes
more than is available.

### Receive

The receive and the cancel post **the line's lot** (M70): a receive puts it on the
destination, a cancel back on the location it left. A line's lot never changes once sent.

`POST /transfers/{id}/receive` (I40) carries a **`clientRequestId`** and, per line, the
quantity that arrived now — in units or packagings as they are that day — and a
**destination** at the to site: `{clientRequestId, lines: [{lineId, quantity | entered,
toLocationId}], close?}`. A line's `quantity` may be 0, which skips it; below 0 is
`quantity_invalid`. More than 500 lines is `too_many_lines`. Only a `sent` or
`partly_received` transfer is received (`transfer_not_receivable`, 409). Each line's
quantity is at most what of it is outstanding — in transit, in units — and more is
`transfer_over_received`. The destination is judged as a receipt's (`location_not_found`,
`location_archived`, `move_crosses_sites`); a Discontinued product's variant is received,
since this is not a goods receipt. It posts one **`transfer_in`** at the to site and
records `received` per line with its destination. The transfer is `received` when
nothing is outstanding, and `partly_received` otherwise. No line with a quantity and no
close is `nothing_to_post`.

**A receive takes no `If-Match`** (I41): two people scanning one delivery must both land.
The transfer's row lock makes them serial, and each is judged against the other's
result — one may be refused `transfer_over_received` for a line the other finished.

### Closing the difference

A receive may carry **`close: {reason, note}`** (I42): `reason` one of `lost`,
`damaged`, `not_packed` or `other`, `note` up to 500 characters. After the receive's own
quantities, every line's remaining outstanding is recorded as a `missing` action line, the
reason and note are kept on the action and the transfer, and the transfer is `received`,
with who closed it. A receive may close with no quantities at all — it then posts **no
movement**, and its action holds the key. Missing stock is posted nowhere: it left the
from site when it was sent.

A close that leaves **nothing outstanding** once the receive's quantities are counted is
**`nothing_to_close`** (409), and **the whole receive rolls back** — its quantities too:
receive it without closing. A replay of a close is answered at step 2, before this.

### Send and receive

A send may carry **`receive: {lines: [{lineId, toLocationId}]}`** naming a destination for
**every** line (I43) — `receive_incomplete` (400) names the line numbers that have none.
The send and the receive of everything sent then run in one transaction, inside the same
retry: two movements at two sites, two actions, both with derived ids. The transfer is
`received`.

### Cancel

`POST /transfers/{id}/cancel` (I44), with `If-Match` and `{clientRequestId}`, takes back
a `sent` transfer with nothing received or recorded missing on any line — anything else is
`transfer_not_cancellable` (409; a draft's detail says to delete it instead). It posts one
**`transfer_in` at the from site**, each line's whole `sent` back to the location it left
(which cannot have been archived meanwhile: [`location_in_transit`](#the-tree)), records
`returned` per line, and the transfer is `cancelled`.

### Idempotency of an action

**A movement's `client_request_id` is its action's**, so one key claims both, never two.
Where the key comes from:

- **Derived from the transfer** for a send, and for a send and receive's receive: a UUID
  made from the transfer's id and the action's kind (SHA-1, in a namespace of the
  module's), as an [issue's](#reservations-and-issues) is from its document. A retried
  send replays although its transfer is no longer a draft and its revision has moved on.
  A send and receive arriving after a plain send of the same transfer finds the derived
  send id with another hash and answers `transfer_not_draft` — never
  `client_request_reused` for an id the caller did not choose.
- **The client's** for a receive and a cancel: a transfer is received in parts, so a key
  per document would replay the second part. A scanner retries with the same id.

The **request hash** is the SHA-256 of the action's kind, the transfer's id and the body.
The same id and the same body replay — a close-only receive too, since the action holds
the id — and answer the transfer as it is now, 200; a retried receive after its own
success is answered at step 2 and is never `transfer_over_received`. Every action
answers the transfer, 200.

### In transit

**In transit is a quantity on the line, not a balance** (I46): `sent − received −
missing − returned`, never below 0 (the table's check), and 0 on every line of a received
or cancelled transfer. A line carries one lot, so a line's in transit is of its lot (M70)
and the transfer answers each line's `lot`. Stock in transit lies on no location and is part of no site's on
hand. It is read on the transfer, per line and in total; as **`incoming`** per to site on
a product's and a variant's stock and on the [stock at sites](#stock); as **`inTransit`**
on the stock totals; and by the [holder](#contracts-provided) as kind `in_transit`.
`contracts.Stock` is unchanged: Orders reserves and issues only what lies at a site.

A send out of a location being [counted](#counts), or a receive into one, changes its
balance as any movement does, and the count's post flags the line moved (I49).

### The følgeseddel

`GET /transfers/{id}/note.pdf` (I51) is the paper that travels with the goods, for a
transfer sent or later — **a draft's answers 404**. It prints, in Norwegian and English
together, the number and the token `TRF-<number>` as Code 128; the from and to site with
their addresses; when it was sent, in the business time zone, and by whom; the expected
arrival and the reference; the lines — SKU, name, the quantity sent with what was entered
in packagings, the lot with its expiry (M70), and the source location's full code; and a line for the receiver's
signature. It is built on demand with maroto, in the labels' fonts, never stored, and
served as `folgeseddel-TRF-<number>.pdf`.

**The `TRF-` token.** The [lookup](#the-lookup) matches `TRF-<number>`, ignoring case, as
the transfer (`kind: transfer`) for a caller holding both views, so scanning the
følgeseddel opens it; the receive page's scan field takes it too, and the
[transfer list's](#the-transfer-list) search takes a number with or without it.

## Placement

A **placement** is one row per variant and site (phase 2 delivery B, I54–I61;
`placements.go`, `low_rule.go`, `low_stock.go`, `import_placements.go`): where at the site
the variant belongs and when it has run low there. It is a setting, kept apart from the
balances, and changes no stock.

### The placement

Its four fields are each optional (I54, manufacturing phase 2 M105):

- **`primaryLocationId`** — an **active location of the site**, or null; anything else
  is `placement_location_invalid` (400);
- **`reorderPoint`** — a quantity (I11) of 0 or more, or null;
- **`maximum`** — a quantity of more than 0, or null, and **never below the reorder
  point**: `maximum_below_reorder_point` (400);
- **`multiple`** — a quantity of more than 0, or null (absent in the body is null): what
  a suggestion is rounded up to a whole number of (migration
  `00170_inventory_placement_multiple`, `ck_placements_multiple`). It is not one of the
  fields that keep a placement: with only a multiple it is `placement_empty`, and a
  placement whose primary location is cleared with nothing else set is removed with it.

A placement with the first three null is **deleted, never stored**: a `PUT` of three nulls is
`placement_empty` (400) — `DELETE` removes it — and the table's CHECK holds it. The write
snapshots the variant's **SKU and name** for the [low-stock list](#the-low-stock-list)'s
order and search; the list shows the catalog's current values.

**`PUT /variants/{variantId}/placements/{siteId}`** (`stock-adjust` with both views)
writes it, its body `{primaryLocationId, reorderPoint, maximum, multiple}`, an unknown property
refused (I61):

- with **`If-None-Match: *`** it **creates** the placement and answers **201**; one
  already there — or created concurrently, the insert waiting on the key — is
  `revision_conflict` (409);
- with **`If-Match`** it **changes** it, moving the revision on, and answers **200**; a
  revision that has moved on is `revision_conflict`, a placement that is not there 404;
- **neither header, both, or one that does not parse** is the 400 a bad `If-Match`
  answers everywhere.

The variant is read from the catalog **before** the transaction: one the catalog does not
know is `variant_not_found` and a Service's `variant_not_stockable`, both 400 on
`variantId`. Inside it the site is taken `FOR SHARE` — none is the 404, an archived one
`site_archived` (400) — and the primary location `FOR SHARE`, which must be an active
location of that site. The answer is the placement with **`low`** judged on what lies at
the site after the write. **`DELETE`** (`If-Match`, `stock-adjust` with both views)
removes it and answers 204; 404 when there is none.

### The primary location

The primary location is **a suggestion, never a rule** (I55): nothing refuses taking
stock elsewhere. It is the default

- **to** location of a receipt line at the site — `GET /variants?siteId=` answers each
  variant's `primaryLocationId` there, which the line editor reads;
- **from** location of a transfer line sent from the site, when it holds some of the
  variant — else the location holding most;
- **to** location of a transfer line received at the site, and of a send and receive's
  line — else the site's root;
- **first row** of the variant's stock at the site, marked `primary`;
- **first location of Orders' pick list** and of a delivery's proposal: each
  `contracts.StockLocation` that [`Stock.Locations`](#contracts-provided) answers
  carries `Primary`, and Orders sorts it first
  ([the pick list](/en/reference/orders/#the-pick-list-and-the-packing-slip)).

**Archiving or deleting a location clears it** (`clearPrimaries`), inside the write's
transaction and before a delete's rows go, for the location and every location below it:
a placement whose only field was that primary location is **removed**, every other one
**loses** its primary location, and each is a `placement_cleared` event naming why
(`location_archived`, `location_deleted` or `site_archived`). Archiving a site clears
the primaries of all its locations and **keeps** the reorder points and maximums; the
list reads active sites only. The foreign key's `ON DELETE SET NULL` is only the
backstop: alone it would leave a primary-only placement empty and fail the table's
CHECK.

### The rule

Per variant and **active** site (I56; `judgeLow`, one pure function the list, its CSV,
the dashboard's count and a site's stock row all judge by — the statement filters on the
same inequality):

```
available = onHand − reserved                 onHand over the site's locations,
                                              usable only (no lot held or expired
                                              today), reserved at the site
incoming  = Σ inTransit of the transfer lines to the site
projected = available + incoming
low       = reorderPoint is set and projected ≤ reorderPoint
target    = maximum, or reorderPoint + 1 when there is no maximum
needed    = target − projected, at least 1 when low
```

The list, its CSV, the dashboard's count and a site's stock row on the product and
variant pages and the stock at sites all read **usable** on hand (manufacturing phase 2
M63), on the workspace's today, so a placement the stock page calls low is on the list.

Without a maximum the target is **one above** the reorder point, so filling it takes the
row off the list; filling *to* the reorder point would leave it low. A placement with no
reorder point is never low.

### Rounding to whole packagings

When the placement has a **multiple** (manufacturing phase 2 M105), `needed` is first
rounded **up** to a whole number of it, `⌈needed ÷ multiple⌉ × multiple`. When the
variant has [packagings](/en/reference/products/#packagings) (I57), the
**suggested** quantity is that rounded **up** to a whole number of its **smallest**
packaging — the fewest units, then `sort_order`, then id, among those holding more than
0 — that is, `⌈needed ÷ size⌉ × size`; without one it is `needed`. The row carries
`needed`, `suggested` and the packaging with its count, and the screen says so wherever a
suggestion appears, the packaging named by its **kind**'s label, not its free name:

- *21 needed, rounded up to 2 cases of 15 = 30* / *21 trengs, rundet opp til 2
  kartonger à 15 = 30*;
- and when `projected + suggested` passes the maximum, *…, 5 above the maximum of 40* /
  *…, 5 over maksbeholdningen på 40*.

A transfer line drafted from the list keeps its `suggestion {needed, packagingId,
count}` (stored and answered as given, `transfer_lines.suggestion`) and shows the same
sentence until the line is edited, which clears it.

### The low-stock list

**`GET /low-stock?siteId&q&cursor`** (both views) answers every low placement at active
sites (I58), **by site code, then the placement's SKU snapshot, then the variant id**,
50 a page, with an opaque `next` cursor on that triple. `siteId` restricts it to one
site; `q` (trimmed) matches a part of the snapshot's SKU or name, ignoring case. A filter
or cursor that does not parse is a 400.

One SQL statement within the schema computes it (`LowStock`): the placements with a
reorder point — `ix_placements_site_sku` serves the per-site filter, while the order is
by the joined site code — joined to the sums of balances over the site's locations, of
reservations at the site and of in-transit lines to the site, each grouped on the
placement's `(variant_id, site_id)`; its cost is the number of placements with a reorder
point, not the catalog. Then, per page:

- the variants through **`ProductCatalog.Variants`** in one call; a variant gone from
  the catalog is **skipped** and its placement left (variant ids are never reused);
- the **snapshots refreshed**: a page whose catalog read finds a SKU or name changed
  writes the catalog's over it, so a rename in Products is searchable from the next read
  after one shows it;
- **`elsewhere`** — every other active site's available for the page's variants, where
  the variant lies or is reserved (`AvailableAtActiveSites`), a site without a
  placement too;
- **`openDrafts`** — the numbers of the draft transfers to the site with a line of the
  variant (`OpenDraftsTo`).

Each row carries the site, the variant, `onHand`, `reserved`, `available`, `incoming`,
`reorderPoint`, `maximum`, `multiple`, `needed`, `suggested`, the `packaging` (`{id, name, kind,
quantity, count}`), `elsewhere` and `openDrafts`. A missing catalog is 503.

**Create transfer** is the UI's (I59): rows at **one** site are chosen and a from site —
by default the site with most available across the selection — and `POST /transfers`
makes the draft with the suggested quantities and their suggestions. A row with an open
draft says *Draft 1043 open*, and *Create transfer* asks before making a second. Nothing
is capped: the draft shows the source's stock beside each line ([I53](#stock)), and the
send refuses what a location does not hold. It is hidden without `transfers-manage`.

### The CSV

**`GET /low-stock.csv?siteId&q`** (both views) is the list with its filter, **every row,
unpaged** — the statement read in pages of a thousand, each judged as the list's is
(I60). It is UTF-8 with a byte-order mark, **semicolon-separated** with CRLF, quantities
with a **decimal comma**, each cell guarded against a spreadsheet reading it as a formula
— the customers export's way — and served as the attachment `low-stock-<YYYY-MM-DD>.csv`
(today in the business zone), `Cache-Control: private, no-store`. Its columns:

`site` (code), `sku`, `name`, `barcode`, `available`, `incoming`, `reorder_point`,
`maximum`, `needed`, `suggested`, `packaging` (the packaging's name), `packaging_count`.

No supplier column: suppliers are free text on receipts until Products phase 5.

### The placements import

**`POST /imports/placements?dryRun=…`** (`stock-adjust` with both views) takes a **CSV**
body (`text/csv`) and sets placements across every site (I61):

- **The file** is read by `srv/fileimport` as the
  [opening-balance import](#the-opening-balance-import)'s is — encoding, separator,
  4 MiB, **5000 rows** — and its header names **exactly** `sku`, `site`,
  `primary_location`, `reorder_point` and `maximum`, and optionally `multiple`
  (manufacturing phase 2 M105), in any order and any case; anything else is a 400 on
  `file`.
- **Each row** is resolved before any transaction — the SKUs in **one `LookupSKUs`**, the
  sites by code and the locations by full code in one read each — and judged as a `PUT`
  would judge it, the first failure its outcome: a cell beyond the header
  `quantity_invalid`; the `site` code `site_not_found` or `site_archived`; the `sku`
  `variant_not_found` or `variant_not_stockable` (a SKU two variants match ignoring case
  resolves to the first by id); `primary_location`, when given, a full code of an
  active location of that site, else `placement_location_invalid`; `reorder_point` 0 or
  more and `maximum` and `multiple` more than 0, at most three decimals, else
  `quantity_invalid`; and
  `maximum_below_reorder_point`.
- **Each row sets every field the file has**, an empty cell clearing its field — a file
  without the `multiple` column leaves every placement's multiple as it is (the upsert's
  `keep_multiple`), and a placement it creates has none — and a row clearing the primary location, the
  reorder point and the maximum **deletes** the placement. A variant and site named twice: the last
  row wins.
- **A dry run** — `dryRun` defaults to **true** — answers 200 with every row and `ok`.
  A commit with any failing row writes **nothing** and answers 200 with `ok: false`.
- **A commit** with every row ok writes them all and answers **201** with `set` and
  `cleared`. In one lock order: a transaction-scoped **advisory lock**, so commits run
  one at a time; every site the file names `FOR SHARE` in id order, each still there and
  active; the primary locations `FOR SHARE` in id order, each still active; then the
  removals and the upsert, sorted by (variant, site), each bumping the placement's
  revision with its `placement_set` or `placement_cleared` event. A site or location
  **archived or deleted since the rows were resolved** refuses the whole file —
  `site_archived` or `placement_location_invalid` (400), nothing written.
- **Idempotent by the file**: the commit records one **`placements_imported`** event
  keyed by the file's **SHA-256** (the first eight bytes as its entity id, the full hash
  in its payload), and a file committed before answers **200** with what that commit set
  and cleared, writing nothing. A different file — one cell changed — is a new import.

### Events

A placement's events carry the variant as their entity (`entity_kind` `placement`) and its
site: **`placement_set`** on a create or a change that changed something, with the SKU
and the changed fields; **`placement_cleared`** on a `DELETE` (`removed: true`), on an
import row clearing it, and on `clearPrimaries` with the reason; and the import's one
**`placements_imported`**, with the file's hash and what it set and cleared.

## Locks

**A posting** (`posting.go`), in this order:

1. The `clientRequestId` is read with a plain `SELECT`; a hit answers the
   [replay or the reuse](#idempotency).
2. The request as a whole: `too_many_lines`, `nothing_to_post`, and an unknown site as
   a 400 field error on `siteId`.
3. **`ProductCatalog.LockVariants`** on the posting's transaction: `FOR SHARE` on the
   sorted distinct variants and their products. Every [entry in
   packagings](#the-entry) is resolved right after, against the packagings it answered;
   a posting's lines are cloned per attempt, so a rerun resolves afresh. Then, when
   lines name lots, the lot counter, the new lots and every lot named, in the
   [lots' order](#the-lock-order), and the [lot rules](#the-lot-rules).
4. The key is **claimed** by writing the movement's header, `INSERT … ON CONFLICT DO
   NOTHING RETURNING`; a conflict re-reads it, as in step 1.
5. The locations `FOR SHARE`, in id order. A posting writes nothing to a location, so
   two postings never wait for each other there.
6. Every line failure found so far is refused at once.
7. Every balance the posting touches is created at zero where missing **and** locked by
   **one** `INSERT … ON CONFLICT DO UPDATE … RETURNING`, in (variant, location, lot)
   order, no lot first. Two postings to a new key serialise on the row rather than
   collide on it, and a balance a concurrent posting deleted at zero is inserted again by
   the same statement, so no lock is held across a second attempt.
8. The lines are applied in order, the balance checked after each. A count's post
   judges its [moved lines](#posting) here, against the balances just locked.
9. The lines are written with their snapshots, the balances written, and the zeros
   deleted. The response is read back after the commit; a replay answers the stored
   movement.

**A lost race is rerun.** An API posting that fails on a deadlock (`40P01`) or a
serialization failure (`40001`) is rolled back and run again **from step 1** in a new
transaction, up to five times; the key is then claimed afresh, or — when a concurrent
attempt with it committed — answered as a [replay](#idempotency), so I14 holds
unchanged.

**A tree write** — a create, a bulk create, a change, a re-parent, an archive, a
reactivation, a delete, a site's new code — first takes **the site row `FOR UPDATE`**,
so tree changes in one site are serial; then the subtree, and a re-parent's new
parent, `FOR UPDATE` in id order (a site's recode, archive or delete: all the site's
locations). A create, single or bulk, takes the parent `FOR SHARE` instead and requires
it active. An archive then reads the subtree's balances; a delete runs an `EXISTS` on
the lines' from and to over the locked ids.

**Where they meet.** A posting takes no lock on the site row of its own, but its
movement's foreign key to the site takes **`FOR KEY SHARE`** at the key's claim, which a
`FOR UPDATE` waits for. So a tree write begins once the postings in flight in its site
have committed, and a posting arriving meanwhile waits at its claim until the tree write
commits. Neither holds what the other waits for, and the two otherwise meet only on
location rows, which both take in id order — no deadlock (tested in `tree.go`'s
suite).

**A count** (`counts.go`, `count_lines.go`, `count_post.go`). Opening one takes **the
site row `FOR UPDATE`** — serialising opens in a site, so the overlap check sees every
count counting — and then reads the balances to freeze. Every later write takes **the
count row `FOR UPDATE` first**: a `PUT` of lines then `LockVariants`, then — for lines
naming lots — the lot counter, the new lots and every lot named `FOR SHARE` in id order
(M58's steps 3–5), then the lines'
locations `FOR SHARE` in id order, as a posting takes them, then the lines it writes; a post then the posting's own order above, from step 3, on a savepoint of the
same transaction; a cancel nothing more. So two writes to one count are serial, and a
post never runs beside a `PUT` of the same count. A tree archive reads the open counts
under its site row's lock; an open takes the same lock, so neither misses the other.

**The import** resolves every row outside any transaction, then posts as an API posting
does, from step 1, with its derived key.

**A transfer** (`transfers.go`, `transfer_actions.go`). Every write takes **the transfer
row `FOR UPDATE` first**, then its two sites `FOR SHARE` in id order — a create, which
has no row yet, the sites alone, then the number's counter row by its upsert. A send, a
receive and a cancel then post through the engine on the same transaction, from step 2
above (`LockVariants`), in the [six steps](#the-actions-and-their-order); a send and
receive posts its two movements one after the other. So two actions on one transfer are
serial, and a receive racing a cancel is judged against whichever committed first. The
whole action is rerun on a deadlock or a serialization failure, as a posting is: two
opposite sends and receives (A to B and B to A at once) may deadlock on balances, and the
loser runs again. A site's archive takes the site row `FOR UPDATE` and then reads its open
transfers, which an action's `FOR SHARE` on the site makes it wait for; a location's
archive takes the subtree `FOR UPDATE` and reads the lines in transit sent from it, which
a send's `FOR SHARE` on its source locations makes it wait for. Neither misses a send.

**A placement** (`placements.go`, `import_placements.go`) keeps one order — sites, then
locations, then placements. A `PUT` takes the site `FOR SHARE`, then the primary location
`FOR SHARE`, then the placement row (its insert, or `FOR UPDATE` for a change); a
`DELETE` the placement row alone. The import's commit takes its advisory lock first, then
the sites and the primary locations `FOR SHARE` in id order, then the placements sorted
by (variant, site). An archive of a site or a location takes them `FOR UPDATE` and then
[clears the primaries](#the-primary-location) naming them, so it is ordered against a
write that named them: whichever commits second sees the other.

**A ledger call** (`ledger.go`) runs on Orders' transaction, after Orders has called
`ProductCatalog.LockVariants` and locked its own rows (rule 24). It takes
`LockVariants` again for the variants' types — already held, so it waits for nothing.
A **reserve** then reads the site plainly, takes the variants' balances at the site's
locations `FOR UPDATE` in (variant, location) order, reads on hand and reserved in a
statement of its own, and upserts the reservations: two reserves of one variant at one
site — two orders confirmed for the last unit — serialise on the balances, and the
second sees what the first reserved. An **issue** follows the posting's order above
from step 1, with its derived key, and then locks the document's reservations
`FOR UPDATE` in id order. A **release** locks only the document's reservations: it can
only make more available. So the order everywhere is Products, then Orders' rows, then
Inventory's balances, then its reservations, and a reserve takes no location or site
lock a tree write could hold.

**With Products.** Products' rows are locked before Inventory's everywhere — but for a
count's row at its post and a transfer's row and sites at its actions, which come first
and which Products never waits on: a posting
shares the variants first, and a variant's delete or a type change locks them
`FOR UPDATE` and then asks the [holder](#contracts-provided), which reads under them. A
packaging's write takes its variant's row `FOR UPDATE` first, so it waits for a posting
that shares the variant and a posting waits for it: an entry is always resolved
against packagings that hold until the commit.

## Reads

### Stock

- **`GET /locations/{id}/contents`** — what lies on the location **and below it**
  (I29), by full code then SKU; `subtree=false` restricts it to the location itself.
- **`GET /products/{productId}/stock`** — the product page's Stock card (I27): every
  **goods** variant of the product in the catalog's order, with or without stock, each
  by site (by code) and location (by full code), with the totals summed exactly. Each
  site and the variant's totals carry `reserved` — what orders hold there — and
  `available`, [usable](#usable-on-hand) on hand less reserved, which may be negative,
  and each site `held`, what lies in lots on hold or expired; a tracked variant's rows
  carry their `lots` ([stock per lot](#stock-per-lot)); each site carries
  `incoming`, what is [in transit](#in-transit) to it on transfers. A site where the
  variant is reserved, or incoming, but lies nowhere is listed with no rows, and so is an
  active site where it only has a [placement](#placement). Each site carries its
  **`placement`** — `{primaryLocationId, reorderPoint, maximum, revision, low}`, `low`
  judged by [the rule](#the-rule) — and its rows the **`primary`** flag, the primary
  location's row first. A product the catalog does not
  know is the 404; a Service product answers no variants.
- **`GET /variants/{variantId}/stock`** — one variant's stock by site and location,
  with `reserved` and `available` per site and in total, `incoming`, the placement and
  the primary location first per site, as above. A
  variant the catalog **no longer knows** is named by its latest movement line's
  snapshot and marked deleted; one neither the catalog nor the ledger knows is the 404.
  Stock cannot outlive its variant (I23), so the fallback names history.
- **`GET /variants`** — the line editor's picker, `q` of 1 to 100 characters and
  `limit` up to 50 (20 by default), through the catalog's `SearchVariants`, which does
  not search packaging barcodes. With **`siteId`**, each variant carries its
  **`primaryLocationId`** at that site, or null: the receipt line's default.
- **`GET /stock?variantIds=`** — the product list's *On hand* column (I33): each
  variant's on hand across every location, summed exactly, with `reserved` across every
  site and `available`, and **`inTransit`** — what is on its way between sites, which is
  not part of `onHand` — for at most **100** ids
  (comma-separated, de-duplicated); none or more than 100 is a 400 on `variantIds`.
  With **`lotId`** (manufacturing phase 2 M74) only that lot's stock counts — its on hand
  and what of it is in transit; a reservation names no lot, so `reserved` is 0 and
  `available` the lot's on hand; a `lotId` below 1 is a 400. A
  variant with no stock, no reservation and nothing in transit is absent from `totals`;
  one wholly in transit keeps its row. Products reads nothing of Inventory on
  the server: the host composes the column into Products' list through the list's
  `extraColumns`, with one request per page, or per 100 of its variants.

- **`GET /stock/sites?variantIds=&siteIds=`** — the stock of variants at sites (I53),
  for the transfer form's columns and the send dialog's defaults: at most **100**
  variants and **20** sites (none, more, or an unknown site is a 400 on its field), and
  one row per variant and site asked, in the order asked, whether or not anything lies
  there — `onHand`, `reserved`, `available` (usable on hand less reserved), `incoming`, the locations holding it, the
  most first, then by full code, each with **`primary`**, and the **`placement`** there,
  or none: the send dialog's default source and a send and receive's default
  destination.

Each reads the balances, the reservations and what is incoming in one snapshot and names
the variants through the catalog outside it.

### Movements

`GET /movements` lists the movements **newest first** by `(posted_at, id)` (I30),
filtered by `siteId`, `locationId` (a line from or to the location **or below it**),
`variantId`, **`lotId`** (a line of the lot, manufacturing phase 2 M74), `kind` (`count`, `issue`, `transfer_out` and `transfer_in` among them), and `from` and `to` — **business days** in the workspace's zone,
`to` inclusive. `limit` is 100 by default and at most 500; `nextCursor` is an opaque
base64url cursor. A filter or cursor that does not parse, or `to` before `from`, is a
400. `GET /movements/{id}` answers one movement with its site, poster, source
reference when set, `countId` for a count movement, **`transfer`** (`{id, number}`) for
a movement a transfer posted — the list's summaries carry it too — and its lines with their variants
and `entered` when they were entered in packagings — a variant the catalog no longer
knows named by the line's own snapshot.

### The transfer list

`GET /transfers` lists the transfers **newest first** by `(created_at, id)`, 50 a page,
with an opaque `next` cursor, filtered by `status` — one of the five, or **`open`**: draft,
sent and partly received together; none is every status — `fromSiteId`, `toSiteId`,
`variantId` (a line of the variant) and **`withMissing`** (something recorded missing, of
that variant when one is given: the variant page's closed differences). **`q`** (up to
100 characters, trimmed) matches a transfer's **number**, given as `1042` or `TRF-1042`,
or a line's **exact SKU** as snapshotted. Each summary carries its sites, status,
reference, expected arrival, `lineCount`, `inTransit`, `missing`, when it was sent, and
**`overdue`**: sent or partly received and past its expected arrival — judged against
today in the business time zone — or, without one, sent more than seven days ago. A
filter or cursor that does not parse is a 400.

### The lookup

`GET /lookup?q=` is one field a keyboard-wedge scanner types into (I28). The text is
trimmed and matched, in order:

1. a **`LOC` token** — `LOC` and a location's id, case-insensitive, as a
   [label](#labels) carries it;
2. a location's **full code**, case-insensitive;
3. a transfer's **`TRF-<number>`** token, case-insensitive, as its
   [følgeseddel](#the-følgeseddel) carries it (`kind: transfer`, with the transfer's id
   and number);
4. a lot's **`LOT<id>` token**, case-insensitive, as the [lot labels](#lot-labels) carry it, then
   every variant's lot whose **number** is the text, ignoring case (`kind: lot`, with the
   lot's id, number and expiry and its variant; manufacturing phase 2 M66);
5. a variant's **SKU or barcode**, or a **packaging's barcode**, through `LookupCode`;
   a packaging's barcode answers its variant, once. A serial number that is also an SKU
   shows both, the lot first.

The access is the **session**, and each kind of match is shown by its own keys: the
location matches to `locations-view`, the transfer, lot and item matches to `locations-view`
with `stock-view`. A caller holding neither gets no matches rather than a 403. An empty
text, or one longer than the limit, is a 400 on `q`.

### Stats

`GET /stats` answers the dashboard card's figures: the active sites, the active
locations below the roots, the **stock lines** (one per variant and location holding
stock, its lots counted once, the *Stock lines*), and **`receiptsThisWeek`**, the receipts posted since
**Monday 00:00** of the business week in the workspace's zone, and **`openCounts`**,
the counts still counting; to a caller with `stock-view`, **`lotsAwaitingInspection`**, the
lots with an active inspection hold ([inspection on receipt](#inspection-on-receipt)).

`GET /stats/attention` is the session's. Each item type is **gated on its own**, so a
caller sees only the items they could act on, at most 20 in all, the counts first. It
raises **`countOpenLong`**:
one item per count still counting **more than seven days** after it was opened —
by which a count meant for a day or a weekend has been forgotten while its expected
quantities drift — oldest first, at most 20. Its `id` is `countOpenLong/<countId>`,
`entityId` the count's id, `occurredAt` when it was opened, and `title` *The count of
{scope's full code} has been open for N days*; the dashboard shows it as *A count has
been open for a long time* and links to the count. It is raised only to a caller who
could post or cancel it — `counts-manage` with both views.

And **`transferOverdue`** (I62): one item per transfer sent or partly received past its
expected arrival — judged against today in the business time zone — or, without one,
sent more than **seven days** ago, oldest first. Its `id` is
`transferOverdue/<transferId>`, `entityId` the transfer's id, `occurredAt` the start of
the expected day in the business zone or the moment it was sent, and `title`
*Transfer 1042 from OSL to BGO was expected 2026-11-05* or *… has been on its way for N
days*; the dashboard shows it as *A transfer is past its expected arrival* and links to
the transfer. It is raised only to a caller who could
receive it — `transfers-manage` with both views.

And **`lowStock`** (I62): one item per **active site** with any placement low by
[the rule](#the-rule), by site code — the count is [the list](#the-low-stock-list)'s
statement counted per site (`LowStockCounts`), so the two never disagree. Its `id` is
`lowStock/<siteId>`, `entityId` the site's id, **`count`** the items, `occurredAt` the
time of the read — a placement does not record when it went low — and `title` *4 items
at or below their reorder point at Bergen store*; the dashboard shows it as *4 items at
or below their reorder point at a site* and links to the low-stock list filtered to the
site. It is raised to a caller with **both views**, after the other types, the whole
answer still cut at 20 items.

And **`lotsExpired`** and **`lotsExpiring`** (manufacturing phase 2 M72): one item each,
when any, counting the lots holding stock whose expiry has passed, and those whose expiry
falls from today to 14 days ahead, in the business zone ([the expiring
list](#the-expiring-list)'s rule). Each has `id` its type, `entityId` `lots`,
**`count`** the lots, `occurredAt` the time of the read, and `title` *3 lots in stock have
expired* or *… expire within 14 days*; the dashboard words them with the count in the
reader's language and links to the expiring list. They are raised to a caller with **both
views**, last. When the business day cannot be read — the calendar fails — the two items
are left out and the failure logged, and the rest of the list still answers. Every item
keeps the shape every module shares; the enum gained the two
phase 2 types and the two lot types and nothing else.

## Personal data

The module stores about a person their **user id and display name** as a movement's
poster (`posted_by_user_id`, `posted_by_display`) and as an event's actor
(`actor_user_id`, `actor_display`), and since phase 2 as a transfer's creator, sender
and closer and an action's poster (`*_by_user_id`, `*_by_display`), and a placement's
last writer (`updated_by_user_id`, `updated_by_display`), and since manufacturing phase 2
a lot's creator, a hold's putter and releaser, and a lot file's uploader, and nothing else. A movement is an **immutable
record of the stock ledger**, an event the record of a structural write, and a
transfer and its actions the record of who sent and received what; all keep
the poster's or the actor's id and name **after the person is erased** — they are
**not swept**, and the module has no erasure worker. This is the exception rule 19 of
the [module boundaries](/en/contributing/module-boundaries/) names; that rule describes
the alternative a module holding a person's data may take — asking identity which users
are gone and deleting their rows on a worker of its own. A user the directory no longer
knows when a write is made is recorded as *Unknown user*.

## Refusals

Every refusal is an RFC 9457 problem, `InventoryProblem`: `title`, `status`, `detail`,
and — when a rule refused — `code`, which is what a client keys off, never the words. A
400 carries `errors`, the field errors keyed by the request's field (`levels[i].from`,
`lines[i].quantity`, `lines[i].entered.packagings[j].count`); a posting's refusal, a
count's `PUT` and `count_lines_moved` carry `lines`. Delivery B's titles are *Invalid
count*, *Invalid labels* and *Invalid import* for their 400s, and *Count scope
overlaps*, *Count not open*, *Count lines moved* and *Location has an open count* for
their 409s. Transfers answer *Invalid transfer* for their 400s, and *Transfer is not a
draft*, *Transfer is not receivable*, *Transfer is not cancellable*, *Nothing to close*,
*Site has open transfers*, *Site has transfers* and *Location has stock in transit* for
their 409s. A placement's write answers *Invalid placement* for its 400s, and the
placements import *Invalid import*, as the opening-balance import does.

| Code | Status | When |
| --- | --- | --- |
| *(field error)* | 400 | A required field missing, a text too long, a kind or a reason that is none of its list, an address field out of its rule, an unknown `siteId` on a posting, or an unknown from or to site on a transfer; any property the schema does not name, `lines[i].x` and `levels[i].x` included; a missing or invalid `If-Match`; a query filter, cursor or limit that does not parse. |
| `code_invalid` | 400 | A code not 1–16 `A–Z0-9` after upper-casing, or `LOC` and digits. |
| `depth_exceeded` | 400 | A location, or a re-parented subtree's deepest, more than 8 levels below the root. |
| `parent_invalid` | 400 | A parent that is unknown or vanished — deleted concurrently included, for a single create and a bulk alike — in another site, archived, or the location itself or below it. |
| `bulk_too_large` | 400 | A bulk create of more than 1000 locations. |
| `too_many_lines` | 400 | A posting through the API, a count's `PUT`, a transfer or a transfer's receive with more than 500 lines. |
| `labels_too_many` | 400 | More than 1000 labels: the ids asked for (the detail names the number), or with their subtrees (the detail says more than 1000). |
| `entered_invalid` | 400 (line) | Both or neither of a quantity and `entered`; a packaging named twice; a count below 1; more than ten packagings; `decrease` with a quantity, or under `set`. |
| `packaging_not_found` | 400 (line) | An entry naming a packaging the variant does not have at posting (or when the count line is recorded). |
| `nothing_to_post` | 400 | No lines, or every `set` line already matching what lies there; a transfer's receive with no line above 0 and no close. |
| `variant_not_found` | 400 (line) | A line's variant the catalog does not answer. |
| `variant_not_stockable` | 400 (line) | A Service product's variant on a line that increases stock, or added to a count. |
| `variant_discontinued` | 400 (line) | A Discontinued product's variant on a receipt. |
| `location_not_found` | 400 (line) | A line's location that does not exist, or a count line's location outside the count's scope; as a field error, a count's scope or a label's location that does not exist. |
| `location_archived` | 400 (line) | A line's location that is archived. |
| `move_crosses_sites` | 400 (line) | A line naming a location outside the movement's site. |
| `same_location` | 400 (line) | A move's from and to the same location. |
| `transfer_same_site` | 400 | A transfer's `toSiteId` the same as its `fromSiteId` (a field error with the code). |
| `site_archived` | 400 | A transfer's write — a draft's create or change, a send, a receive, a cancel — naming a from or to site that is archived (a field error on `fromSiteId` or `toSiteId` with the code); a placement's `PUT` at an archived site; a placements import's commit when a site the file names was archived or deleted after it was read. |
| `maximum_below_reorder_point` | 400 | A placement's `maximum` below its `reorderPoint` (a field error on `maximum` with the code). |
| `placement_empty` | 400 | A placement's `PUT` with no primary location, reorder point or maximum: remove it with `DELETE` instead (a field error on `primaryLocationId` with the code). |
| `placement_location_invalid` | 400 | A placement's primary location that is archived, or not a location of its site (a field error on `primaryLocationId` with the code); a placements import's commit when a primary location was archived or deleted after the file was read. |
| `transfer_line_unknown` | 400 | A send's, a send and receive's or a receive's `lineId` that is not on the transfer (a field error on `lines[i].lineId` with the code; a line named twice is a field error too). |
| `receive_incomplete` | 400 | A send and receive that names no destination for some line; the detail names their line numbers. |
| `from_location_required` | 400 (line) | A send's line with no source location; the index is the line's place on the transfer. |
| `transfer_over_received` | 400 (line) | A receive's line taking more than the line has outstanding; the index is the line's place in the request. |
| `lot_required`, `lot_not_allowed`, `lot_unknown`, `serial_quantity`, `serial_first_invalid`, `lot_dates_differ` | 400 (line) | The [lot rules](#the-lot-rules) (manufacturing phase 2 M56, M57) — on a posting, a count's `PUT` and post, a transfer's draft and send (L2, M69, M70), and as the opening-balance import's row outcomes `lot_required`, `lot_not_allowed`, `lot_invalid` and `serial_quantity` (M71). |
| `lot_invalid`, `lot_dates_invalid` | 400 (line) | A count line's new lot whose number does not read (or whose supplier lot or note is too long), or whose dates are out of order (M69); the line's index names the row. |
| `lot_expired`, `lot_blocked` | 409 (line) | An issue or consume line taking an expired lot without the override, or a lot on hold; a receipt into a blocked lot. The problem's code is the first such line's. |
| `hold_not_inventorys` | 409 | The lot page releasing a hold another module's document put; it is released there. |
| `hold_exists` | 409 | The lot page putting a quarantine or a block while its own of that kind is active on the lot. |
| `inspection_requires_tracking` | 400 | Inspect on receipt for a variant that is not tracked in lots or serials (manufacturing phase 2 M100; a field error on `inspectOnReceipt` with the code). |
| `trace_too_large` | 400 | A trace beyond 2,000 lots or 20,000 other nodes, or a block's downstream walk beyond 2,000 lots; the block then blocks nothing. |
| `trace_changed` | 409 | Lots kept being made from the lot while its block locked them; nothing was blocked, and the block may be tried again. |
| `lot_dates_invalid` | 400 | A lot's dates out of order — an expiry before the production date, or a best-before date after the expiry — on `PUT /lots/{id}` or a receipt or adjustment line's new lot (a field error with the code). |
| `attachment_too_large`, `too_many_attachments` | 400 | A lot's file over 20 MiB, or a 21st. |
| `attachment_type` | 415 | A lot's file that is not a PDF, JPEG or PNG, or whose bytes disagree with the type or extension it is sent under. |
| `storage_not_configured` | 503 | A lot's file uploaded or downloaded with no object store configured. |
| `quantity_invalid` | 400 (line) | A quantity not more than 0 (a `set` or a count below 0), a `change` of 0, more than three decimals, or a line or balance above 99,999,999,999.999; an entry's loose rest out of its rule, an entry coming to 0 where more is needed, or to more than the maximum. |
| `site_code_taken` | 409 | A site code another site holds. |
| `location_code_taken` | 409 | A code a sibling holds; a bulk names every colliding full code. |
| `full_code_taken` | 409 | A full code — of a recode, a re-parent's subtree, a site's new code or a bulk's deeper level — another location holds. |
| `location_has_stock` | 409 | Archiving a site or a location while stock lies in it or below. The detail gives the quantity and the number of stock lines only to a caller holding `inventory:stock-view`; to anyone else it says only that the location, or something below it, holds stock and must be emptied first. |
| `location_has_history` | 409 | Deleting a location whose subtree a movement line or a count ever named, or a site with any movement or count. |
| `location_has_open_count` | 409 | Archiving a location while a count still counting has it, a location below it or one above it as its scope; re-parenting one while a count still counting has its scope in the moved subtree, at one of its current ancestors, or at or above the new parent; archiving a site while any count in it is counting. |
| `count_scope_overlaps` | 409 | Opening a count whose scope is, contains or lies inside the scope of a count still counting; the detail names that count and its scope. |
| `count_not_open` | 409 | A `PUT` of lines, a post or a cancel of a count already posted or cancelled. |
| `count_lines_moved` | 409 (lines) | Posting a count without `acceptMoved` while a line that posts has a balance other than it expected; `lines` names every one. |
| `root_location` | 409 | A root's `PUT` changing `parentId`, `kind`, `code` or `status`, or its delete. |
| `insufficient_stock` | 409 (line) | A line taking a balance below zero — a transfer's send among them; the problem's code whenever any line has it. Never raised by an import or a count, whose lines only add or set. |
| `client_request_reused` | 409 | A `clientRequestId` posted before with a different body — a movement's, or a transfer's receive or cancel, on this transfer or another. |
| `transfer_not_draft` | 409 | A change, a delete or a send of a transfer that is not a draft; a send and receive after a plain send of the same transfer. |
| `transfer_not_receivable` | 409 | A receive of a draft, a received or a cancelled transfer. |
| `transfer_not_cancellable` | 409 | A cancel of a transfer not `sent` — a draft (delete it), one partly received, received or cancelled — or with anything received or missing on a line. |
| `nothing_to_close` | 409 | A receive with `close` that leaves nothing outstanding once its quantities are counted; the whole receive rolls back. |
| `site_has_open_transfers` | 409 | Archiving a site that is the from or to site of a transfer sent or partly received. |
| `site_has_transfers` | 409 | Deleting a site any transfer names, a draft included. |
| `location_in_transit` | 409 | Archiving a location when it, or one below it, is the source of a transfer line still in transit. |
| `revision_conflict` | 409 | The `If-Match` revision of a site, a location, a transfer or a placement has moved on; for a transfer, judged after its status. A placement's `PUT` with `If-None-Match: *` when one is already there. |
| `dependency_unavailable` | 503 | The catalog is not composed — never so in a running `inventory`, which `MODULES` refuses without `products`. |

An unknown id is a bare 404, and a caller without the operation's permission the
access layer's uniform 403.

The [stock ledger](#reservations-and-issues) answers no HTTP: its refusals are
`*contracts.StockRefusal` values for Orders to turn into its own problems.

An import's rows are not refusals: a row that fails is its outcome — `location_not_found`,
`variant_not_found`, `variant_not_stockable` or `quantity_invalid` for the opening
balances; `site_not_found`, `site_archived`, `placement_location_invalid`,
`variant_not_found`, `variant_not_stockable`, `quantity_invalid` or
`maximum_below_reorder_point` for the placements — in a 200 with `ok: false`.

**400 and 409, not 422 and 412.** The design's §4 gives its field and line rules 422
and a stale `If-Match` 412 (`revision_mismatch`). The house answers a rule about the
request 400 and a rule about the state of things 409, as every other module does, and so
does this one: 400 for the field and line rules, 409 `revision_conflict` for a stale
revision.

## Assistant tour targets

The Assistant can point at these controls in a guided tour ([the registry](/en/reference/assistant/#guided-tours)). The package registers them in `apps/inventory/frontend/src/assist-targets.json`, exports them as `assistTargets` (`@vantigo/inventory-ui/assist-targets`) and marks each control with `data-assist="<key>"`; a test of the package fails when a key has no marker or a marker has no key. A route with `{…}` is a record's screen.

| Key | Route | Control |
| --- | --- | --- |
| `inventory.home.lookup` | `/inventory` | The lookup field (Find location or item) |
| `inventory.home.new-site` | `/inventory` | New site |
| `inventory.home.new-receipt` | `/inventory` | New receipt |
| `inventory.home.new-adjustment` | `/inventory` | New adjustment |
| `inventory.home.new-move` | `/inventory` | New move |
| `inventory.site.new-location` | `/inventory/sites/{siteId}` | New location |
| `inventory.site.bulk-add` | `/inventory/sites/{siteId}` | Bulk add |
| `inventory.site.print-labels` | `/inventory/sites/{siteId}` | Print labels |
| `inventory.counts.new` | `/inventory/counts` | Open count |
| `inventory.import.upload` | `/inventory/import` | The CSV file |
| `inventory.transfers.list` | `/inventory/transfers` | The transfer list |
| `inventory.transfers.new` | `/inventory/transfers` | New transfer |
| `inventory.transfer-receive.scan` | `/inventory/transfers/{transferId}/receive` | The scan field (Scan an item or the transfer note) |
| `inventory.low-stock.list` | `/inventory/low-stock` | The low-stock list, its filters and Create transfer |
| `inventory.lots.list` | `/inventory/lots` | The lots list and its filters |
| `inventory.expiring.list` | `/inventory/expiring` | The expiring list, its filters, Export CSV and Print labels |
| `inventory.lot.holds` | `/inventory/lots/{lotId}` | A lot's Holds panel: Quarantine, Block and Release |
| `inventory.trace.tree` | `/inventory/trace` | A lot's trace: its direction, the tree, Export CSV and Download PDF |

## Enabling and disabling

`MODULES` is a positive allowlist; the smallest installation with Inventory is

```text
MODULES=products,inventory
```

`inventory` without `products` fails configuration with `inventory requires products`,
checked in `internal/config`. `Deps.Products` is therefore never nil in a running
`inventory`; the module refuses to mount without it, and answers
`dependency_unavailable` (503) rather than panic if a read finds it missing.

Omitting `inventory` means the module contributes no route, no permission and no
contract path; its paths answer the `/api` catch-all 404, the switcher tile greys out,
and the product page draws no Stock card. The `inventory` schema is **migrated
regardless**, so enabling it later needs no migration — and because stock in the schema
is still stock, the [holder](#contracts-provided) **runs even when `inventory` is off**:
Products still refuses to delete a variant, or to make its product a service, while
the schema holds stock of it. The **stock ledger** is composed whatever `MODULES` says,
for the same reason: Orders' cancel, edits and manual marking still release the
reservations made while Inventory was on, and turning it back on shows true figures.
**A transfer in transit is frozen** while the module is off (I50): nothing can be
received or cancelled until it is on again, and the holder still answers `in_transit`
for what it carries, so Products still refuses to delete the variant.
`Deps.Stock` is nil while it is off, and Orders then reserves and issues nothing. Inventory contributes **one background worker**, `inventory.object-deletions`, which
removes the objects of [deleted lot files](#a-lots-files), and no setting — a count left
open, or a transfer overdue, is raised on the dashboard when it is read, not by a job.

## What comes next

**Phase 1 is complete.** Delivery A built sites, locations, the ledger and the reads;
delivery B built [entering in packagings](#entering-in-packagings) and the breakdown,
[counts](#counts), [labels](#labels), [the opening-balance
import](#the-opening-balance-import), the [stock totals](#stock) for the product list's
*On hand* column, the site figures and the server-side search of a site's locations.
One thing of the design was left out: the notice that a printed label's text is stale
after a rename or a recode (I7, §9); the print dialog says instead that a label keeps
scanning and needs reprinting only when its text should change. Labels are chosen in the
print dialog from a searchable list, not by ticking locations in the tree. The count
screen holds every line of the scope it shows but draws only those near the window, so a
whole site's count of thousands of lines scrolls with the page.

**Reservations**, `contracts.Stock` and `contracts.StockLedger`, the `issue` kind and
the *fulfils orders* flag came with Orders' delivery C ([reservations and
issues](#reservations-and-issues)).

**Transfers between sites** came with phase 2's delivery A ([transfers](#transfers)):
Inventory's own document, not Orders' as phase 1's §11 had it, with its draft, send,
receive in parts, closed difference, send and receive and cancel, its stock in transit,
the `in_transit` hold, the stock at sites, the følgeseddel and the `TRF-` lookup, and the
overdue item. **Placement** came with delivery B ([placement](#placement)): a variant's
primary location, reorder point and maximum per site, the primary location as every
form's and the pick list's default, the low-stock rule and its rounding to whole
packagings, the low-stock list and its CSV, *Create transfer* from it, the placements
import and the `lowStock` item. **Phase 2 is complete.**

**Lots** came with manufacturing phase 2's L1a ([lots](#lots)): the schema, the
lot-aware key and engine, holds and the derived status, expiry, FEFO and AutoLot,
usable on hand, and the contracts. Their screens came with L1b: the lots list and a
lot's page, its edit and holds, its files and their deletion worker, stock per lot, the
lookup and Inventory's live topics. L2 brought counts per lot, the lot on transfer
lines, the import's lot columns, the expiring list with its CSV and attention items, lot
labels with the `LOT` token and the movements' lot filter. There is no movements CSV for
M74's lot column to join; the lot page's movements and the filter answer it. Layer T
brought [traceability](#traceability): the trace both ways with its CSV and PDF, who
received a lot through Orders' `DeliveryTrace`, the block with its downstream list and
GS1-128 lot labels; the trace is linked from the lot page, the lots list, the lookup,
Manufacturing's *Lots* tab and Orders' delivery page, and the spotlight's *Trace a lot*
opens it.

**Later** (phase 1 §11 and phase 2 §11), each waiting for what it names:
negative stock by a setting
(point of sale); purchase orders from the low-stock list and incoming from them
(Purchasing); a supplier column in the CSV (supplier records, Products phase 5);
`inventory.transfer.*` and low-stock domain events (a consumer); transfer lines from a
spreadsheet, a shelf-level minimum, a reorder point per location and a daily low-stock
mail (demand); when an item went low, which a placement does not record; per-site
permissions, so only the destination receives (the platform); units beyond packagings and supplier records
(Products phase 5); camera scanning; thermal label printers; valuation; the store flag
*sells at the till* (point of sale).

The phase keeps what Orders and Manufacturing need (§12): the source reference on every
movement, null and indexed; the kinds as an open set; lines with no *from* or no *to*;
balances as rows no read treats as the whole key, which let the lot join it; lots in
Inventory, asked through the same holder; reservations per site, not a column.

## Endpoints

All under `/api/v1/inventory`. The permission column names every key the operation
requires, all of them.

| Operation | Permission | |
| --- | --- | --- |
| `GET /meta` | session | The caller's nine capabilities and today. |
| `GET /lookup` | session | A location by token or full code (`locations-view`), a transfer by `TRF-` token, a lot by `LOT` token or number, or an item by SKU or barcode (+ `stock-view`). |
| `GET /sites` | `locations-view` | The active sites, or those of a status. |
| `POST /sites` | `locations-manage` + `locations-view` | Create a site and its root. |
| `GET /sites/{id}` | `locations-view` | One site with its root's id. |
| `PUT /sites/{id}` | `locations-manage` + `locations-view` | Change, recode, archive or reactivate a site, or whether it fulfils orders, with `If-Match`. |
| `DELETE /sites/{id}` | `locations-manage` + `locations-view` | Delete a site with no movement. |
| `GET /sites/{id}/locations` | `locations-view` | The site's tree, flat, by full code, at most 10,000 (`truncated` beyond), searched with `q`. |
| `POST /locations` | `locations-manage` + `locations-view` | Create a location under an active parent. |
| `POST /locations/bulk` | `locations-manage` + `locations-view` | Create up to 1000 locations in up to three levels, all or nothing. |
| `GET /locations/{id}` | `locations-view` | One location with its site, path and children. |
| `PUT /locations/{id}` | `locations-manage` + `locations-view` | Change, re-parent, archive or reactivate a location, with `If-Match`. |
| `DELETE /locations/{id}` | `locations-manage` + `locations-view` | Delete a location and its subtree, with no history. |
| `GET /locations/{id}/contents` | `locations-view` + `stock-view` | What lies on the location, and below it unless `subtree=false`. |
| `GET /products/{productId}/stock` | `locations-view` + `stock-view` | Every goods variant of a product with its stock. |
| `GET /variants` | `locations-view` + `stock-view` | Goods variants by SKU, name or barcode; with `siteId`, each with its primary location there. |
| `GET /stock` | `locations-view` + `stock-view` | On hand of up to 100 variants across every location, or of one lot (`lotId`). |
| `GET /variants/{variantId}/stock` | `locations-view` + `stock-view` | One variant's stock, a deleted one by its snapshot. |
| `GET /variants/{variantId}/rules` | `locations-view` + `stock-view` | The variant's [rules](#inspection-on-receipt), off at revision 0 when it has none. |
| `PUT /variants/{variantId}/rules` | `locations-view` + `stock-adjust` + `stock-view` | Sets inspect on receipt; 400 `inspection_requires_tracking`; 404; 409 `revision_conflict`. |
| `GET /lots` | `locations-view` + `stock-view` | The lots, filtered, by variant then number. |
| `GET /lots/expiring` | `locations-view` + `stock-view` | The lots with stock expiring within `days` (30) or expired, by expiry, at most 1000. |
| `GET /lots/expiring.csv` | `locations-view` + `stock-view` | The expiring list as CSV, with its filter. |
| `GET /lots/labels.pdf` | `locations-view` + `stock-view` | A PDF of up to 1000 lot labels, `copies` of each. |
| `GET /lots/{id}` | `locations-view` + `stock-view` | A lot's page: its holds, where it lies, its movement lines and its files. |
| `PUT /lots/{id}` | `locations-view` + `lots-manage` + `stock-view` | Change a lot's supplier lot, dates and note, with `If-Match`. |
| `POST /lots/{id}/holds` | `locations-view` + `lots-manage` + `stock-view` | Put a hold of the lot page. |
| `POST /lots/{id}/holds/{holdId}/release` | `locations-view` + `lots-manage` + `stock-view` | Release one of Inventory's own holds. |
| `POST /lots/{id}/attachments` | `locations-view` + `lots-manage` + `stock-view` | Add a file; 201. |
| `GET /lots/{id}/attachments/{attachmentId}` | `locations-view` + `stock-view` | Download a file. |
| `DELETE /lots/{id}/attachments/{attachmentId}` | `locations-view` + `lots-manage` + `stock-view` | Remove a file; 204. |
| `GET /stock/sites` | `locations-view` + `stock-view` | Up to 100 variants' stock at up to 20 sites, with incoming, the locations holding it — the primary marked — and the placement. |
| `PUT /variants/{variantId}/placements/{siteId}` | `locations-view` + `stock-view` + `stock-adjust` | Create a placement with `If-None-Match: *` (201) or change it with `If-Match` (200). |
| `DELETE /variants/{variantId}/placements/{siteId}` | `locations-view` + `stock-view` + `stock-adjust` | Remove a placement, with `If-Match`; 204. |
| `GET /low-stock` | `locations-view` + `stock-view` | The low placements at active sites, 50 a page, by site code and SKU, by `siteId` and `q`. |
| `GET /low-stock.csv` | `locations-view` + `stock-view` | The low-stock list as CSV, every row, with the list's filter. |
| `GET /movements` | `locations-view` + `stock-view` | Movements by site, location, variant, lot, kind and business day, newest first. |
| `GET /movements/{id}` | `locations-view` + `stock-view` | One movement with its lines. |
| `POST /movements/adjustments` | `locations-view` + `stock-view` + `stock-adjust` | Post an adjustment; 201, or 200 for a replay. |
| `POST /movements/moves` | `locations-view` + `stock-view` + `stock-adjust` | Post a move; 201, or 200 for a replay. |
| `POST /movements/receipts` | `locations-view` + `stock-view` + `receipts-manage` | Post a goods receipt; 201, or 200 for a replay. |
| `GET /stats` | `locations-view` + `stock-view` | The dashboard card's figures. |
| `GET /stats/attention` | session | `countOpenLong`, to `counts-manage` + both views; `transferOverdue`, to `transfers-manage` + both views; `lowStock`, `lotsExpired` and `lotsExpiring`, to both views. |
| `GET /counts` | `counts-manage` + `locations-view` + `stock-view` | Counts newest first, at most 200, by status and site. |
| `POST /counts` | `counts-manage` + `locations-view` + `stock-view` | Open a count of a site or a subtree; 201. |
| `GET /counts/{id}` | `counts-manage` + `locations-view` + `stock-view` | One count with its lines. |
| `PUT /counts/{id}/lines` | `counts-manage` + `locations-view` + `stock-view` | Record up to 500 counted lines. |
| `POST /counts/{id}/post` | `counts-manage` + `locations-view` + `stock-view` | Post the count as one `count` movement. |
| `POST /counts/{id}/cancel` | `counts-manage` + `locations-view` + `stock-view` | Cancel a count still counting. |
| `POST /labels` | `locations-view` | A PDF of up to 1000 location labels. |
| `POST /imports/opening-balances` | `locations-view` + `stock-view` + `stock-adjust` | Dry run or commit a CSV of opening balances; 201, or 200 for a dry run, a refused commit or a replay. |
| `POST /imports/placements` | `locations-view` + `stock-view` + `stock-adjust` | Dry run or commit a CSV of placements across every site; 201, or 200 for a dry run, a refused commit or a replay of the same file. |
| `GET /transfers` | `locations-view` + `stock-view` | Transfers newest first, 50 a page, by status (`open` among them), sites, variant, missing and `q`. |
| `POST /transfers` | `locations-view` + `stock-view` + `transfers-manage` | Create a draft transfer; 201. |
| `GET /transfers/{id}` | `locations-view` + `stock-view` | One transfer with its lines and actions. |
| `PUT /transfers/{id}` | `locations-view` + `stock-view` + `transfers-manage` | Replace a draft's header and lines, with `If-Match`. |
| `DELETE /transfers/{id}` | `locations-view` + `stock-view` + `transfers-manage` | Delete a draft, with `If-Match`; 204. |
| `POST /transfers/{id}/send` | `locations-view` + `stock-view` + `transfers-manage` | Send a draft, or send and receive it, with `If-Match`; 200, a replay too. |
| `POST /transfers/{id}/receive` | `locations-view` + `stock-view` + `transfers-manage` | Receive what arrived, and close the difference; no `If-Match`; 200, a replay too. |
| `POST /transfers/{id}/cancel` | `locations-view` + `stock-view` + `transfers-manage` | Cancel a sent transfer back to its sources, with `If-Match`; 200, a replay too. |
| `GET /transfers/{id}/note.pdf` | `locations-view` + `stock-view` | The følgeseddel of a transfer sent or later; a draft's is 404. |
| `GET /trace` | `locations-view` + `stock-view` | A lot's trace, usage or origin, up to 10 productions, 2,000 lots and 20,000 other nodes. |
| `GET /trace.csv` | `locations-view` + `stock-view` | The trace as CSV. |
| `GET /trace.pdf` | `locations-view` + `stock-view` | The trace as a PDF. |
| `POST /lots/{id}/block` | `locations-view` + `stock-view` + `lots-manage` | Block a lot and the lots made from it still in stock. |
| `POST /lots/{id}/holds/{holdId}/release` | `locations-view` + `stock-view` + `lots-manage` | Release one of Inventory's holds; with `downstream`, the lot page's block on the lots made from it too. |
| `GET /lots/labels.pdf` | `locations-view` + `stock-view` | Lot labels, internal or (`format=gs1`) GS1-128. |

Every key is `inventory:`'s; the table leaves the prefix out.
