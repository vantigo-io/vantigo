# Inventory — sites, locations and stock (phase 1)

Revision 2, 2026-10-27 (after a critic review; §14 lists what changed). From the research
`docs/superpowers/research/2026-10-27-inventory-module.md` ("N"), whose §8 and the owner's
answers in §9.1 this spec does not reopen. Decisions are numbered I*n*. The implementation
plan is `docs/superpowers/plans/2026-10-27-inventory-plan.md`. `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`.

## 1. Purpose

A module, **Inventory** (`inventory`; Norwegian **Lager**; package
`@vantigo/inventory-ui`), that keeps stock of the catalog's goods in warehouses and
stores. Each site is divided into the places an item can lie, from a pallet rack's bin to
a shop's shelf. Three things are asked for first:

- the **total stock on the product page**;
- **every location where the item lies**;
- a **location lookup** that shows what lies there.

Stock changes through four records: **adjustments**, **moves**, **goods receipts** and
**counts**. Goods can be entered and scanned in **packagings** (a box of 15, a Euro pallet
of 40 boxes). Packagings are a Products feature, pulled forward from Products phase 5.

**Two deliveries:**
- **A**: sites, locations, the ledger, adjustments, moves, receipts, the product panel,
  the lookup, and the Products seam.
- **B**: packagings, counts, labels, the opening-balance import, and the product list's
  stock column.

**Not in this phase:**
- transfers between sites;
- reservations;
- lots, serials and expiry;
- placement and min/max;
- negative stock;
- valuation;
- camera scanning.

§11 says what each waits for, and §12 the rules this phase keeps so that Orders and
Manufacturing join without reshaping it.

## 2. Decisions

### Sites and locations

- **I1 — A site is a warehouse or a store, and it is the root of a location tree.**
  - A site has:
    - a `code` of 1–16 characters `[A-Z0-9]`, unique in the deployment;
    - a `name`;
    - a `kind`, `warehouse` or `store`;
    - an optional address (line 1, line 2, postal code, city, country ISO 3166 alpha-2);
    - a `status`, `active` or `archived`.
  - Creating a site creates its **root location** (kind `site`, code = the site's code)
    in the same transaction. Renaming the site's code renames the root's code and every
    full code below it (I4).
  - The kind changes the suggested location kinds and the words on screen. It changes no
    rule (N §4.1).
- **I2 — Locations form a free tree of typed nodes.**
  - A location has:
    - a parent, which is another location of the same site;
    - a `kind`, from the fixed list `zone`, `area`, `sales_floor`, `back_room`, `aisle`,
      `rack`, `shelf`, `bin`, `display`, `dock`, `other` (`site` is the root's alone);
    - a `code` of 1–16 characters `[A-Z0-9]`, unique among its siblings and
      case-insensitive (stored upper-case);
    - an optional `name`;
    - a `sortOrder`;
    - a `status`, `active` or `archived`.
  - The tree is at most **8 levels** below the root.
  - A location never changes site: re-parenting stays inside its site (I21).
- **I3 — Stock may lie on any location, the root included, and totals include the
  subtree.** A shop with no shelves keeps stock on the site itself; adding shelves later
  is a move down (N §9.1 reading 1).
- **I4 — The full code** joins the codes from the root down with `-`
  (`OSL-A-03-2`).
  - It is unique in the deployment, because site codes are and sibling codes are, and
    codes cannot contain `-`.
  - It is stored, and rewritten for the whole subtree in the transaction that changes a
    code or a parent.
  - The lookup matches it case-insensitively.
- **I5 — Bulk creation.** One request creates a grid under a parent: up to three levels,
  each a kind, a code pattern and a range.
  - Example: an `aisle` coded `A`, then `rack` `01`–`12`, then `shelf` `1`–`5`. The
    width of a range's start (`01`) sets the zero padding.
  - At most **1000** locations per request. All-or-nothing: one sibling collision refuses
    the whole request with the colliding full codes listed.
- **I6 — Archive and delete.**
  - A location (or site) that holds stock, or has stock anywhere below it, **cannot be
    archived** (`location_has_stock`, with the quantity and the number of lines).
  - An archived location takes no stock: every movement line naming it is refused
    (`location_archived`), and it is hidden from pickers unless asked for. Archiving a
    location archives its subtree. Reactivating one reactivates only itself and requires
    an active parent.
  - A location or site can be **deleted** only if no movement line has ever named it or
    anything below it (`location_has_history`), checked by an `EXISTS` on the lines'
    from and to indexes over the locked subtree; otherwise it is archived instead.
  - The root location cannot be deleted or archived on its own; the site's status
    governs it.
- **I7 — Labels encode a stable token, not the code (B).**
  - A location's barcode is **`LOC` + its id** (`LOC1042`) in Code 128, with the full
    code and the name printed as text.
  - The token uses only upper-case letters and digits, because keyboard-wedge scanners
    emulate a US layout: on a Norwegian layout a scanned `-` arrives as `+`, so a full
    code would not scan back.
  - A site or location code matching `^LOC\d+$` is refused (`code_invalid`), so a full
    code can never equal a token.
  - The token survives renames and re-parenting. A label needs reprinting only when its
    printed text changes, and the location page says so.

### The ledger

- **I8 — Every change is a posted movement, and movements are immutable.**
  - A movement has:
    - a `kind`: `adjustment`, `move` or `receipt` (A), or `count` (B). The set is open:
      `issue`, `production` and `transfer` join with Orders and Manufacturing (§12);
    - a `siteId`, since every line of a v1 movement lies in that one site;
    - a `reason`, for adjustments only: `correction`, `damaged`, `lost`, `found`,
      `opening_balance` or `other`;
    - an optional `note` (≤ 1000 characters);
    - the receipt's optional `supplierName` (≤ 200) and `supplierReference` (≤ 100);
    - the **source reference**: `sourceModule`, `sourceKind`, `sourceId`,
      `sourceLineId`, all null in this phase (§12);
    - the poster (user id and display snapshot) and `postedAt`.
  - A movement is never edited or deleted. A mistake is corrected by a new adjustment.
- **I9 — A movement line** has:
  - a variant;
  - an optional `fromLocationId` and an optional `toLocationId`, at least one of them
    set and never the same location;
  - a `quantity` greater than 0;
  - snapshots of the variant's SKU, product name and unit at posting.

  A receipt line has only *to*; a write-off only *from*; a move both. An adjustment line
  that increases has only *to*, one that decreases only *from*.
- **I10 — The balance.** One row per (variant, location) holding `onHand`.
  - It is updated in the posting transaction. A missing row is created first, with
    `INSERT … ON CONFLICT DO NOTHING` at zero, before the sorted `FOR UPDATE`, so two
    postings to a new pair serialise on the row instead of colliding on the key. A row whose `onHand` reaches zero is
    deleted, so "where does it lie" lists only places with stock.
  - A line that would take a balance **below zero is refused** (`insufficient_stock`,
    naming the SKU, the location's full code and the quantity there). An administrator
    setting to allow negative stock is later (N §9.1 reading 2).
- **I11 — Quantities** are `numeric(14,3)` in the variant's own unit.
  - On the wire they are JSON numbers with at most three decimals, greater than 0 and at
    most 99,999,999,999.999. More decimals are refused (`quantity_invalid`), never
    rounded.
  - Numbers arrive as `float64` (`number/double`, the house rule). The server turns each
    one into an exact decimal through `strconv.FormatFloat(v, 'f', -1, 64)` and
    `big.Rat.SetString`, as Invoices' `ratFromFloat` does (`srv/invoices/decimal.go`).
    It never uses `SetFloat64` or float arithmetic. The domain's 14 significant digits
    round-trip exactly.
- **I12 — Goods only.**
  - A line whose variant belongs to a `Service` product is refused
    (`variant_not_stockable`).
  - A receipt line whose product is `Discontinued` is refused (`variant_discontinued`).
  - Adjustments and moves accept discontinued variants, so the last stock can be cleared.
  - `Draft` products are accepted everywhere: goods arrive before they go on sale.
- **I13 — Within one site.** Every line of a movement must name locations of the
  movement's site. A move between sites is refused (`move_crosses_sites`) until Orders
  brings the two-step transfer (N §9.1 reading 5).
- **I14 — Posting is idempotent by a client key.**
  - Every posting request carries a `clientRequestId` (UUID), unique among movements.
  - The key is looked up **first**, before any validation, so a replay answers the
    original even if the variant has since been discontinued. A repeat with the same key
    and the same body answers the first movement with 200, not a second one. A repeat with a different body is refused
    (`client_request_reused`).
  - This exists because scanners and phones retry, and a double receipt is invisible
    until the next count.
- **I15 — The four records.**
  - **Adjustment** (`stock-adjust`): lines of variant, location, `mode` and quantity.
    - `mode: change` takes a signed `delta` (non-zero).
    - `mode: set` takes the new on-hand and is posted as the difference. A line whose
      difference is zero is dropped, and a movement left with no lines is refused
      (`nothing_to_post`).
    - A reason is required.
  - **Move** (`stock-adjust`): lines of variant, from, to and quantity.
  - **Receipt** (`receipts-manage`): a default `toLocationId`; lines of variant, an
    optional location overriding the default, and quantity; the optional supplier fields
    and note.
  - **Count** (`counts-manage`, B): §2 "Counts".
  - A movement posted through the API carries **at most 500 lines**. Internal callers
    (the import, a count's post) are not capped.

### Counts (B)

- **I16 — A count** has:
  - a scope: a site, or one location and its subtree;
  - a status, `counting`, `posted` or `cancelled`;
  - a note;
  - who opened it and when.
- **I17 — Opening a count freezes the expected quantities.**
  - Opening one creates a line per (variant, location) with a balance in the scope,
    holding `expected` = the balance at that moment.
  - A count whose scope overlaps another count still `counting` is refused
    (`count_scope_overlaps`).
  - Movements are **not** blocked while counting.
- **I18 — Counting.**
  - Lines take a `counted` quantity (≥ 0), typed or scanned, optionally entered in
    packagings (I24).
  - A variant found at a location with no line gets a new line (`expected` 0, `added`).
  - Counted values can be changed until posting. Each change records who and when.
- **I19 — Posting a count** writes one `count` movement.
  - For each counted line the movement records the difference **between `counted` and
    the balance at posting**.
  - A line whose balance changed since the count opened is **flagged `moved`**. Posting
    with flagged lines requires `acceptMoved: true`; otherwise it is refused
    (`count_lines_moved`, listing them). This is Odoo's "outdated" (N §9.1 reading 3).
  - Uncounted lines are treated by the poster's choice, `keep` or `zero`.
  - The count becomes `posted` and keeps the movement's id. A cancelled count posts
    nothing.
- **I20 — A count is how stock is first placed.** A count of a location with no
  balances, whose lines are all `added`, is a legitimate first count, as Tripletex
  advises.

### Re-parenting and the code

- **I21 — Re-parenting.**
  - A location (with its subtree and its stock) may get a new parent in the same site,
    provided that:
    - it is not moved under itself or a descendant;
    - the depth limit holds;
    - no full code collides.
  - The stock stays where it is: it is the location that moved.
  - The change is recorded in `inventory.events`, and the location page then offers a
    label reprint.

### Products: the seam and packagings

- **I22 — `contracts.ProductCatalog` grows** (`srv/contracts/catalog.go`), in A.
  `LockVariants` is the codebase's first **locking read run inside the caller's
  transaction**: until now a contract call runs before a transaction opens
  (module-boundaries rule 8 and the Planning harness). It gets a rule of its own in
  `module-boundaries.md`: it runs on the caller's `tx`, never on the pool; the lock order
  is Products' rows, then the caller's; and the provider never calls back.
  `catalog.go`'s doc comment changes to match. Inventory's `contractscalls.go` keeps
  every other call before the transaction and routes this one separately.
  - `VariantEntry.Barcode *string`, and in B `VariantEntry.Packagings []PackagingEntry`.
  - `VariantsOfProduct(ctx, productID) ([]VariantEntry, error)`.
  - `LookupCode(ctx, code string) ([]CodeMatch, error)`. A `CodeMatch` holds a variant
    and an optional packaging. It matches a SKU case-insensitively and exactly, a variant
    barcode exactly, and (B) a packaging barcode exactly.
  - `SearchVariants(ctx, q string, limit int) ([]VariantEntry, error)`, a search by SKU,
    product name and barcode for Inventory's variant picker. Products' own list needs
    five Products keys, `pricing-view` among them, which a warehouse worker should not
    need.
  - `LockVariants(ctx, tx pgx.Tx, ids []int32) ([]VariantEntry, error)`, which takes
    `FOR SHARE OF` both the `product_variants` rows and their `products` rows (type and
    status live there) in id order, inside the caller's transaction, and answers the
    entries.

    Inventory calls it first in every posting. A variant absent from the answer is
    refused (`variant_not_found`). `tx` is a platform type, as in `InvoicedWork`
    (`srv/contracts/invoiced_work.go:43`), so contracts still carry no store types.
- **I23 — A variant that holds stock cannot be deleted.**
  - A new many-provider slot, `module.Module.VariantReferences func(Deps)
    contracts.VariantReferenceHolder`, is collected **whether or not the module is on**,
    like `CustomerReferences` (boundaries rule 8). Stock in the schema is still stock.
  - Its single method, `VariantHeld(ctx, tx, variantID) (*VariantHold, error)`,
    answers nil or `{Quantity string (a decimal), Locations int}`. The unit comes from
    Products' own row.
  - Products' delete becomes a transaction:
    1. Lock the variant row `FOR UPDATE`.
    2. Check the last-variant 409.
    3. Ask every holder.
    4. Delete.
  - Because a posting holds `FOR SHARE` on the same row (I22), a receipt cannot land
    between the check and the delete. The lock order is Products' variant row before any
    of Inventory's rows, everywhere.
  - The refusal is 409 with the title *Variant has stock*. Products' problems carry no
    code (`srv/products/errors.go`: "Nothing here may grow an error code"), so the app
    tells it from the existing *Last variant* 409 by its title. The detail is the one the
    screen shows as written: *"This variant still has 34 pcs in stock at 3
    locations. Move the stock or adjust it to zero before you delete the variant."* The
    variant's stock panel is linked from the dialog.
  - A variant with history but no stock may be deleted, because its movement lines keep
    the snapshots (N §9.1 reading 6).
  - Archiving a product (`Discontinued`) is unaffected.
- **I24 — Packagings (B, Products).**
  - A variant has 0–10 packagings, each with:
    - a `name` (≤ 40);
    - a `kind`: `inner`, `case`, `pallet` or `other`;
    - either `containsPackagingId` plus `containsCount` (an integer of at least 2),
      **or** a `quantity` in the base unit;
    - an optional GTIN `barcode` (GTIN-8/12/13/14, check digit valid), unique across
      every variant and packaging barcode;
    - optional `weightKg`, `lengthCm`, `widthCm` and `heightCm`;
    - a `sortOrder`.
  - The stored base `quantity` is derived when the packaging is built from another one:
    a Euro pallet of 40 × a box of 15 holds 600.
  - Changing the box's quantity recomputes every packaging built from it in the same
    transaction.
  - Cycles are refused, as is deleting a packaging another one is built from. These
    are titled Products problems (*Packaging cycle*, *Packaging in use*, *Barcode
    taken*), not coded ones.
  - Changing a quantity never touches stock, which is always in the base unit.
  - Barcode uniqueness across the two tables is checked under one transaction-scoped
    advisory lock, `products:barcodes`, taken by both the variant writes and the
    packaging writes.
- **I25 — Entering in packagings (B).**
  - Every quantity input of Inventory (adjustment, move, receipt and count lines) may
    instead carry `entered: {packagings: [{packagingId, count}], loose}`. `quantity` and
    `entered` are two optional fields, exactly one of them required by a Go check, not a
    `oneOf` (which oapi-codegen turns into union types).
  - The server computes the base quantity from the packagings **as they are at
    posting**, and stores the entry on the line as a snapshot (names, counts, quantities
    per packaging) so history reads "3 Euro pallet + 2 Box".
  - Scanning a packaging's GTIN adds one of that packaging.
- **I26 — Showing in packagings (B).**
  - Quantities on the product panel, the location contents and the count screen can be
    shown broken down greedily, largest packaging first: 1,830 with a pallet of 600 and
    a box of 15 reads "3 Euro pallet, 2 Box".
  - The base quantity is always shown beside it.
  - The breakdown is computed in the app from the packagings Inventory returns with each
    variant. The server sends both the base quantity and the variant's packagings.

### Views

- **I27 — The product panel.**
  - The product page gets a **Stock** card, composed by the host when `inventory` is
    enabled and the user holds `inventory:stock-view`.
  - The card shows:
    - the total on hand, per unit;
    - by site, each expanding to its locations by full code and name, sorted by full
      code;
    - per variant, when the product has more than one;
    - a link to each variant's movements.
  - With no stock it says so and offers *Receive* (with `receipts-manage`) or *Adjust*
    (with `stock-adjust`).
  - For a `Service` product the card is not drawn.
- **I28 — The location lookup.**
  - One field, autofocused, that submits on Enter, which is what a keyboard-wedge
    scanner sends.
  - It matches in order:
    1. a `LOC` token;
    2. a full code;
    3. a SKU, variant barcode or packaging barcode, through `LookupCode`.
  - One match opens it: the location page, or the variant's stock view. Several matches
    are listed. None says so and keeps the text.
  - The lookup is on the Inventory home page and in the spotlight, as a quick action. No
    separate locations result group is added.
- **I29 — The location page.**
  - It shows:
    - the path as a breadcrumb;
    - kind, code and full code, and status;
    - the children;
    - the **contents**: variant, SKU, product name, on hand and the location it lies on.
      These are subtree totals by default; *This location only* restricts them to the
      location itself;
    - the movements touching the location or its subtree, newest first.
  - Actions offered by permission:
    - *Adjust*, *Move from here* and *Receive here* (A);
    - *Count*, *Print label* (B);
    - edit, add child, bulk add, archive.
- **I30 — The movements.**
  - A list filtered by site, location (subtree), variant, kind and date, at most 500 rows
    per page by cursor, newest first.
  - The movement page shows the header, the source reference when set, and the lines.

### Records and the rest

- **I31 — `inventory.events`.** An append-only log in the shape of `planning.events`,
  but with a `site_id` where Planning has `person_id`/`project_id`. It has no read
  endpoint in this phase; the location and site pages show their movements, and the
  log serves the reference and later audits:
  - entity kind and id, kind, a JSON payload, actor and time;
  - for site and location created, changed, re-parented, archived, reactivated and
    deleted, and for count opened, posted and cancelled;
  - movements are their own record and are not repeated here.
- **I32 — The opening-balance import (B).**
  - A CSV with columns `location` (full code), `sku` and `quantity`, for one site.
  - Two steps:
    1. A dry run answers each row's outcome: `ok`, `location_not_found`,
       `variant_not_found`, `variant_not_stockable` or `quantity_invalid`.
    2. The commit posts one `adjustment` movement with reason `opening_balance` and
       `mode: change`, all-or-nothing. It is refused when any row fails.
  - At most 5000 rows, posted as **one** movement through the engine's internal entry
    (I15's cap is the API's). It has one client key, derived from the file's hash and
    the site, so the same file posted twice is one movement.
  - The importer reuses `srv/fileimport`'s CSV reading.
- **I33 — The product list's stock column (B).** The product list gets an optional
  `On hand` column. The host composes it through an `extraColumns` prop, fed by one
  `GET /api/v1/inventory/stock?variantIds=` per page (≤ 100 ids). Products reads
  nothing of Inventory on the server.
- **I34 — No `contracts.Stock` in this phase.** The research proposed a read contract.
  Every v1 reader is Inventory's own app composed by the host, so the contract would
  have no consumer. It is built with its first server-side reader, Orders or Point of
  sale (§12).

## 3. Data model (schema `inventory`)

The house style of `00042`/`00072`: identity `bigint` ids, `timestamptz`, no `CHECK`
constraints beyond those the database must hold (the rules are Go's), and no SQL outside
the module's schema (`TestNoModuleReferencesAnotherModulesSchema`). Variant ids are
`integer` because Products' are, and carry no foreign key across schemas.

### 3.1 `inventory.sites` (A)

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `bigint identity` | |
| `code` | `varchar(16)` | unique |
| `name` | `varchar(200)` | |
| `kind` | `text` | `warehouse`, `store` |
| `address_line1`, `address_line2`, `postal_code`, `city` | `text` null | |
| `country` | `char(2)` null | |
| `status` | `text` | `active`, `archived` |
| `revision` | `integer` | If-Match |
| `created_at`, `updated_at` | `timestamptz` | |

### 3.2 `inventory.locations` (A)

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `bigint identity` | |
| `site_id` | `bigint` | FK `sites` |
| `parent_id` | `bigint` null | FK `locations`; null only for the root; a partial unique index `(site_id) WHERE parent_id IS NULL` makes one root per site |
| `kind` | `text` | I2 |
| `code` | `varchar(16)` | unique `(parent_id, code) NULLS NOT DISTINCT` (the root's null parent is covered by the root index) |
| `name` | `varchar(200)` null | |
| `sort_order` | `integer` | default 0 |
| `status` | `text` | |
| `full_code` | `text` | unique |
| `ancestors` | `bigint[]` | the ids from the root to the parent; GIN index; the subtree of X is `X = id OR ancestors @> ARRAY[X]` |
| `depth` | `smallint` | 0 at the root |
| `revision`, `created_at`, `updated_at` | | |

### 3.3 `inventory.movements` (A)

`id`, `client_request_id uuid unique`, `request_hash bytea` (I14), `site_id`, `kind`,
`reason` null, `note` null, `supplier_name` null, `supplier_reference` null, `count_id`
null (B), `source_module`, `source_kind`, `source_id`, `source_line_id` (all null, text),
`posted_by_user_id uuid`, `posted_by_display text`, `posted_at`. Indexes: `(site_id,
posted_at desc)`, `(kind, posted_at desc)`, and `(source_module, source_kind, source_id)`
where `source_id` is not null.

### 3.4 `inventory.movement_lines` (A)

`id`, `movement_id`, `line_no`, `variant_id integer`, `sku text`, `product_name text`,
`unit text`, `from_location_id` null, `to_location_id` null, `quantity numeric(14,3)`,
`entered jsonb` null (B, I25). Indexes: `(variant_id, movement_id)`, `(from_location_id)`,
`(to_location_id)`.

### 3.5 `inventory.balances` (A)

`variant_id integer`, `location_id bigint`, `on_hand numeric(14,3)`, `updated_at`.
**Primary key `(variant_id, location_id)`**. This key gains `lot_id` under §12; v1 code
treats a balance as a row and never as a map keyed by the pair. Index `(location_id)`.

### 3.6 `inventory.counts` and `inventory.count_lines` (B)

- `counts`: `id`, `site_id`, `scope_location_id`, `status`, `note`, `opened_by_*`,
  `opened_at`, `posted_by_*` null, `posted_at` null, `uncounted` null (`keep` or
  `zero`), `movement_id` null, `revision`.
- `count_lines`: `id`, `count_id`, `variant_id`, `location_id`, `expected
  numeric(14,3)`, `counted numeric(14,3)` null, `entered jsonb` null, `added boolean`,
  `counted_by_*` null, `counted_at` null. Unique `(count_id, variant_id, location_id)`.

### 3.7 `inventory.events` (A)

The columns of `planning.events` (`mig/00072_planning_baseline.sql`), with `site_id`
in place of its `person_id` and `project_id`.

### 3.8 `products.variant_packagings` (B, Products' migration)

`id integer identity`, `variant_id` FK `ON DELETE CASCADE`, `name`, `kind`,
`contains_packaging_id` null (FK, same variant, checked in Go), `contains_count integer`
null, `quantity numeric(14,3)`, `barcode varchar(14)` null with a partial unique index,
`weight_kg`, `length_cm`, `width_cm`, `height_cm`, `sort_order`, timestamps.

## 4. Rules and refusals

Problems use apicommon's shape with a `code`. The status for each code:

| Code | Status | When |
| --- | --- | --- |
| `site_code_taken`, `location_code_taken`, `full_code_taken` | 409 | I1, I2, I4, I5 |
| `code_invalid` | 422 | not `[A-Z0-9]{1,16}` after upper-casing |
| `depth_exceeded` | 422 | I2 |
| `parent_invalid` | 422 | another site, itself, a descendant, archived |
| `bulk_too_large` | 422 | > 1000 |
| `location_has_stock` | 409 | archive (I6) |
| `location_has_history` | 409 | delete (I6) |
| `location_archived` | 422 | a line names one (I6) |
| `location_not_found`, `variant_not_found` | 422 | a line names one that does not exist |
| `variant_not_stockable`, `variant_discontinued` | 422 | I12 |
| `move_crosses_sites` | 422 | I13 |
| `same_location` | 422 | from = to |
| `insufficient_stock` | 409 | I10 |
| `quantity_invalid` | 422 | I11 |
| `nothing_to_post` | 422 | I15 |
| `too_many_lines` | 422 | > 500 |
| `client_request_reused` | 409 | I14 |
| `count_scope_overlaps` | 409 | I17 |
| `count_not_open` | 409 | a write to a posted or cancelled count |
| `count_lines_moved` | 409 | I19 |
| `revision_mismatch` | 412 | If-Match |
| `location_has_open_count` | 409 | archive with an open count in the subtree (B) |

Products' refusals (I23, I24) are titled problems without a code, as all of Products' are.

A posting checks every line before writing any and answers **all** failing lines at once:
the problem's `lines: [{index, code, detail}]`.

**The posting engine** is `post(ctx, tx, …)`: the API's handlers open the transaction,
and the count post and the import call the same function inside theirs.

**Lock order in a posting:**
1. Look up the `clientRequestId` with a plain `SELECT`. A hit answers the original
   (I14).
2. `ProductCatalog.LockVariants`, in variant id order.
3. Claim the key with `INSERT … ON CONFLICT DO NOTHING RETURNING`. A conflict means a
   concurrent first attempt committed; read it back.
4. The locations, `FOR SHARE`, in id order. Postings write nothing to locations; the
   first review's `ever_used` column deadlocked two postings on one location and is
   gone.
5. The missing balances `INSERT … ON CONFLICT DO NOTHING`, then all the balances `FOR
   UPDATE`, in (variant, location) order.

**Lock order in a tree write:**
- Every structural write (create, bulk, update, re-parent, archive, reactivate, delete,
  and a site's code change) first locks **the site row `FOR UPDATE`**, so tree changes in
  one site are serial.
- Then it locks the affected locations `FOR UPDATE ORDER BY id`.
- Then it reads the subtree's balances (archive) or line existence (delete), and writes.
- A create locks its parent `FOR SHARE` and checks it is active.

A posting takes no site lock, so tree writes and postings meet only on location rows,
which both take in id order.

**Counts (B):**
- Opening a count locks the site row `FOR UPDATE`, so two overlapping opens cannot both
  pass the overlap check.
- Posting a count locks the count row `FOR UPDATE`, then follows the posting order.

## 5. Permissions

| Key | Display (en / nb) | Gives |
| --- | --- | --- |
| `inventory:locations-view` | View locations / Se lagersteder | Sites, locations, the lookup's location matches |
| `inventory:locations-manage` | Manage locations / Administrere lagersteder | Create, change, bulk, archive, delete; labels (B) |
| `inventory:stock-view` | View stock / Se beholdning | Quantities, the product panel, contents, movements |
| `inventory:stock-adjust` | Adjust stock / Justere beholdning | Adjustments and moves; the import (B) |
| `inventory:receipts-manage` | Receive goods / Motta varer | Goods receipts |
| `inventory:counts-manage` | Count stock / Telle varer | Open, count, post, cancel counts (B) |

All six are delegable and none is sensitive. Access is all-of
(`x-vantigo-access: permission:inventory:a+inventory:b`, every key fully qualified).
**Every stock read and every write also requires `locations-view` and `stock-view`**,
because they show location codes and their forms pick locations. So a stock role is
`locations-view + stock-view (+ the write keys)`, and `locations-view` alone gives the
structure without quantities. The app's home needs `locations-view` or
`stock-view`. A route that more callers may open than the sidebar entry declares its
`guardPermissions` (boundaries step 9).

## 6. API (`/api/v1/inventory`, contract `openapi/inventory.yaml`)

| Operation | Op id | Access | D |
| --- | --- | --- | --- |
| `GET /meta` → `{canViewLocations, canManageLocations, canViewStock, canAdjust, canReceive, canCount}` | `getInventoryMeta` | session | A |
| `GET /sites?status` → `{sites: [InventorySite]}` | `listInventorySites` | locations-view | A |
| `POST /sites` | `createInventorySite` | locations-manage | A |
| `GET /sites/{id}` | `getInventorySite` | locations-view | A |
| `PUT /sites/{id}` (If-Match) | `putInventorySite` | locations-manage | A |
| `DELETE /sites/{id}` | `deleteInventorySite` | locations-manage | A |
| `GET /sites/{id}/locations?status` → `{locations: [InventoryLocation]}` (flat, ≤ 10000) | `listInventorySiteLocations` | locations-view | A |
| `POST /locations` | `createInventoryLocation` | locations-manage | A |
| `POST /locations/bulk` → `{created: [InventoryLocation]}` | `bulkCreateInventoryLocations` | locations-manage | A |
| `GET /locations/{id}` → with `path`, `children` | `getInventoryLocation` | locations-view | A |
| `PUT /locations/{id}` (If-Match) | `putInventoryLocation` | locations-manage | A |
| `DELETE /locations/{id}` | `deleteInventoryLocation` | locations-manage | A |
| `GET /locations/{id}/contents?subtree=true` → `{rows: [InventoryBalanceRow]}` | `getInventoryLocationContents` | locations-view+stock-view | A |
| `GET /variants?q&limit` → `{variants: [InventoryVariantRef]}` (≤ 50) | `searchInventoryVariants` | locations-view+stock-view | A |
| `GET /lookup?q` → `{matches: [InventoryLookupMatch]}` | `lookupInventory` | session (each match kind by its own key) | A |
| `GET /products/{productId}/stock` → `InventoryProductStock {variants: [{variant, totalOnHand, sites: [{site, onHand, rows}]}]}` | `getInventoryProductStock` | locations-view+stock-view | A |
| `GET /variants/{variantId}/stock` | `getInventoryVariantStock` | locations-view+stock-view | A |
| `GET /stock?variantIds` → `{totals: [{variantId, onHand}]}` (≤ 100) | `listInventoryStockTotals` | locations-view+stock-view | B |
| `GET /movements?siteId&locationId&variantId&kind&from&to&cursor` | `listInventoryMovements` | locations-view+stock-view | A |
| `GET /movements/{id}` | `getInventoryMovement` | locations-view+stock-view | A |
| `POST /movements/adjustments` | `postInventoryAdjustment` | locations-view+stock-view+stock-adjust | A |
| `POST /movements/moves` | `postInventoryMove` | locations-view+stock-view+stock-adjust | A |
| `POST /movements/receipts` | `postInventoryReceipt` | locations-view+stock-view+receipts-manage | A |
| `GET /counts?status&siteId` | `listInventoryCounts` | locations-view+stock-view+counts-manage | B |
| `POST /counts` | `openInventoryCount` | locations-view+stock-view+counts-manage | B |
| `GET /counts/{id}` | `getInventoryCount` | locations-view+stock-view+counts-manage | B |
| `PUT /counts/{id}/lines` (upsert ≤ 500) | `putInventoryCountLines` | locations-view+stock-view+counts-manage | B |
| `POST /counts/{id}/post` `{uncounted, acceptMoved}` | `postInventoryCount` | locations-view+stock-view+counts-manage | B |
| `POST /counts/{id}/cancel` | `cancelInventoryCount` | locations-view+stock-view+counts-manage | B |
| `POST /labels` `{locationIds, layout}` → `application/pdf` | `printInventoryLabels` | locations-view | B |
| `POST /imports/opening-balances?siteId&dryRun` (text/csv) | `importInventoryOpeningBalances` | locations-view+stock-view+stock-adjust | B |
| `GET /stats` → `{sites, locations, stockLines, receiptsThisWeek, openCounts}` | `getInventoryStats` | locations-view+stock-view | A (`openCounts` 0 until B) |
| `GET /stats/attention` → items `countOpenLong` (B) | `getInventoryStatsAttention` | session | A (empty until B) |

**Shapes:**
- `InventoryBalanceRow {variant: InventoryVariantRef, location: InventoryLocationRef,
  onHand}`.
- `InventoryVariantRef {id, productId, sku, productName, unit, packagings?: [...] (B)}`,
  read through the catalog. A variant the catalog no longer knows shows its last
  movement's snapshot with `deleted: true`. That cannot happen while stock exists (I23);
  it applies to history.

Products changes:
- A: `DELETE …/variants/{variantId}` gains the 409 *Variant has stock*.
- B: `GET|POST /products/{id}/variants/{variantId}/packagings`, plus `PUT|DELETE
  …/packagings/{packagingId}`, under `products:variants-view` / `variants-manage`. The
  variant response embeds `packagings`.

## 7. Frontend

**`@vantigo/inventory-ui`, routes under the host's `/inventory`:**

| Route | Page | D |
| --- | --- | --- |
| `/inventory` | Home: the lookup, the sites as cards (locations, stock lines, the last receipt) | A |
| `/inventory/lookup?q` | Lookup results | A |
| `/inventory/sites/$siteId` | The site: header, the location tree (expand, collapse, search by code), add, bulk add, edit, archive | A |
| `/inventory/locations/$locationId` | I29 | A |
| `/inventory/variants/$variantId` | One variant's stock by site and location, and its movements | A |
| `/inventory/movements` | I30 | A |
| `/inventory/movements/$movementId` | One movement | A |
| `/inventory/movements/new/{adjustment,move,receipt}` | The line editor | A |
| `/inventory/counts`, `/inventory/counts/$countId` | Counts list; the counting screen, then review and post | B |
| `/inventory/import` | The opening-balance import | B |

**The line editor**, shared by the three forms and the count screen:
- A row has a variant picker searching SKU, name and barcode; a scan field that adds a
  line, or increments one, on Enter; location pickers by full code with the site's tree;
  and a quantity.
- B adds packaging steppers and the breakdown.
- The form posts with a `clientRequestId` generated when the form opens and kept across
  retries.
- Failing lines are marked from the problem's `lines`.

**Exports for the host:**
- `<ProductStock productId>` (I27).
- `<InventoryLookupField>` (the spotlight).
- B: `useStockTotals(variantIds)` for I33.

**Products' frontend:**
- A: `ProductDetailsPage` takes `extraPanels?: ReactNode`, drawn after the variants card;
  the delete dialog shows the 409's detail when its title is *Variant has stock*, and a
  link.
- B: the packagings editor wherever a variant is edited (the inline single-variant card,
  and the multi-variant editing the B1 dossier locates); the product list's
  `extraColumns`.

**The host:**
- `moduleKeys`, `apps.ts` (Inventory / Lager, `IconBuildingWarehouse`, sidebar: Overview,
  Sites, Movements, Counts (B)), and `routes/inventory.tsx`.
- `routes/products/$productId.tsx` passes `<ProductStock>`.
- The dashboard card (stock lines and receipts this week) and its attention items (B).
- The spotlight: the lookup as a quick action.
- `catalogs/admin.ts`: the six keys in en and nb.

## 8. Platform, contracts and documentation

- **Contracts and slots:**
  - `contracts/catalog.go` (I22).
  - `contracts/references.go` (the new `VariantReferenceHolder`).
  - `module/module.go`, `module/compose.go` and `modtest` (the `VariantReferences` slot
    and `modtest.WithVariantReferenceHolders`).
  - `module-boundaries.md`: a rule for the new slot, in the words of rule 8, and a rule
    for the locking read run on the caller's transaction (I22).
- **Config:** `config.go` — `inventory requires products`; Inventory refuses to mount on
  a nil `Products`.
- **Docs, A:**
  - `reference/inventory.md`;
  - `user/inventory.md` in en and nb;
  - `admin/installation.md` and the deployment lists (boundaries step 13), in en and nb
    where bilingual;
  - `reference/products.md` and `user/products.md` en and nb (the panel, the delete
    refusal);
  - the overviews listing modules;
  - `docs/astro.config.mjs`.
- **Docs, B:** the same pages for packagings, counts, labels and the import.
- **ROADMAP.md, with delivery A:**
  - an **Inventory** section (phase 1 = A and B; *Later* = §11);
  - "Warehouse" becomes "Inventory" in the domain-events note and Products;
  - Products phase 5 loses packagings to Inventory phase 1 and keeps the other units and
    the supplier records, "designed with Orders";
  - a **Manufacturing** entry naming §12's rules.

## 9. Edge cases

- **A variant deleted by an old binary while stock exists.** It cannot happen once A
  ships. If it happened before (no stock existed before A), it is moot.
- **A product turned from `Goods` to `Service` while it holds stock.**
  - Products allows changing the type. Inventory keeps the stock and shows it, but
    refuses new receipts and increases (I12); moves and decreases still post.
  - If the type change is refused (§13.1), it locks every variant of the product `FOR
    UPDATE ORDER BY id` before asking the holders. Because `LockVariants` also shares
    the product row, a receipt that read `Goods` cannot commit alongside it.
  - Products refuses the type change while any variant holds stock, asking `VariantHeld`
    per variant of the product (§13.1, answered yes).
- **A site's code renamed.** Every full code below it changes in one transaction. Printed
  labels still scan (I7). The text on them is stale, and the site page says so once.
- **Two scans of the same item in one form** increment the line, never add a duplicate.
- **The same `(variant, location)` twice in one posting.** Lines are applied in order and
  the balance check runs after each, so `-5, +5` against a balance of 0 fails at the
  first line.
- **A count's scope location archived while counting.** Refused: archive requires no
  open count in the subtree (`location_has_open_count`, 409).
- **Bulk creation under an archived parent.** Refused (`parent_invalid`).
- **A huge site.** The tree endpoint caps at 10,000 locations. The site page renders
  lazily by level, and search by code goes to the server past 2,000 rows.

## 10. Testing

- **Pure:**
  - the code and full-code rules;
  - bulk expansion and its padding;
  - the quantity parser (I11);
  - the packaging derivation and its cycles;
  - the greedy breakdown (app);
  - the count difference and flag.
- **Store and handlers** (testdb, `contracttest.RequireCoverage`):
  - every refusal of §4;
  - the lock order under concurrency: a receipt racing a variant delete, a posting racing
    an archive, two postings of one `clientRequestId`;
  - the rebuild check: the balances equal the sum of the lines (a test helper run at the
    end of every ledger test).
- **Products:** the delete in a transaction with a fake holder; the packaging endpoints
  (B); the barcode uniqueness across the two tables under the advisory lock.
- **The app:** the line editor (scan increments, the problem's `lines` marking rows),
  the tree, the lookup's one-match navigation, and the product panel with and without
  stock and with several variants.
- **The host:** the panel composed only when the module is on and the key held; the
  `ModuleTabGate`-style guard on a pasted URL.

## 11. Later, and what each waits for

| Later | Waits for |
| --- | --- |
| Transfers between sites (*sent → in transit → received*) | Orders |
| Reservations (*available = on hand − reserved*, per site), `contracts.Stock` and `StockLedger` | Orders, the domain-events outbox |
| Lots, serials, expiry, FEFO, traceability | Manufacturing (§12) |
| Allow negative stock (administrator setting, per site) | Point of sale |
| Placement: primary location, min/max, a low-stock list | Owner's call (N §9.1 reading 10) |
| Units other than packagings (kg, litre, purchase units) | Products phase 5 |
| Supplier records on receipts | Products phase 5 with Orders |
| Camera scanning (`BarcodeDetector`) | Browser support |
| Thermal label printers (ZPL) | Demand |
| Valuation | A ledger |
| Store flags (*sells at the till*, *fulfils orders*) | Point of sale, Orders |

## 12. What this phase keeps for Orders and Manufacturing

From N §6 and §6.1, held by this design:

1. **The source reference** is on every movement from A (I8), indexed, and null.
2. **The movement kinds are an open set.** The kinds are application constants, not a
   database enum, and nothing switches exhaustively on them without a default.
3. **Lines may have no *from* or no *to*.** A production's consume and produce lines need
   nothing new.
4. **Balances are rows.** No contract, response or query treats (variant, location) as
   the whole key, so `lot_id` joins the primary key with `NULLS NOT DISTINCT`, and the
   lines, as two nullable columns and an index change.
5. **Lots belong to Inventory.** Tracking (`none`, `lot`, `serial`) is a per-variant
   Products field that may change only at zero stock, asked through the same
   `VariantReferenceHolder` as I23.
6. **Reservations are per site.** The balance table gains no `reserved` column.

## 13. Open questions (answered by the owner, 2026-10-27)

1. Should Products refuse turning a `Goods` product with stock into a `Service` (§9)?
   Recommended yes, in A. **Answered: yes, block it, in A.** The refusal is a 409
   titled *Product has stock*, built as I23's, and the type change locks the variants as
   §9 says.
2. Should the label layout support Avery L7160 (21 per A4) and L7163 (14), or one
   generic 3 × 8 grid? Recommended both, since they are the common Nordic office sheets.
   **Answered: both Avery sheets, L7160 and L7163, to start.**
3. Should the receipts list be its own page, or the movements list filtered by
   `kind=receipt`? Recommended filtered, with a sidebar entry *Receipts* that opens it.
   **Answered: the filtered list, with its own sidebar entry.**
4. After a person is erased, should the ledger keep a movement's
   `posted_by_user_id`/`posted_by_display` and an event's actor, or should a sweep blank
   the display name? Recommended keep, as rule 19's exception for ledger records.
   **Answered (2026-11-07): keep both; the module has no erasure worker.**
5. Should printing labels need `locations-view` or `locations-manage`? Recommended
   view, since printing changes nothing. **Answered (2026-11-07): `locations-view`, as
   built.**

## 14. Revision 2 — the critic review, 2026-10-27

| # | Finding | Change |
| --- | --- | --- |
| 1 | `ever_used`, written by postings that hold `FOR SHARE` on the same location, deadlocks two postings to one location | The column is gone; delete checks `EXISTS` on the lines over the locked subtree (I6, §3.2, §4) |
| 2 | Strict handlers receive `float64`, not `json.Number` | I11 follows Invoices' `ratFromFloat` |
| 3 | `FOR UPDATE` cannot lock a balance row that does not exist yet | Insert missing rows at zero first (I10, §4) |
| 4 | Tree writes had no fixed lock order and could deadlock with postings | The site row serialises tree writes, then `ORDER BY id` (§4) |
| 5 | A locking contract read inside the caller's transaction is new to the codebase | A boundaries rule of its own (I22, §8) |
| 6 | Products may not grow error codes | Its refusals are titled problems (I23, I24, §4) |
| 7 | An Inventory user cannot search variants without Products' five keys | `SearchVariants` and `GET /variants?q` (I22, §6) |
| 8 | Stock reads showed location codes without `locations-view`; writes' pickers needed it | Every stock read and write requires both views (§5, §6) |
| 9 | The import was ten movements in one transaction under one key | One movement through the engine's internal entry (I15, I32) |
| 10 | A Goods→Service guard could race a receipt | `LockVariants` shares the product row; the type change locks the variants (I22, §9) |
| 11 | A replay validated before reading its key | The key is read first (I14, §4) |
| — | Minor | No deferrable root FK (a partial unique index instead); events carry `site_id`; `^LOC\d+$` codes reserved; `location_has_open_count` in §4; the count open and post locks; `entered` as two optional fields; `VariantHold` without a unit; fully qualified access strings; `GET /events` and the spotlight locations group cut |

Kept against the review's suggestion: the opening-balance import. A count can place
opening stock (I20), but a CSV of a few thousand lines is how an existing shop moves in,
and it costs one endpoint on the engine. The owner may cut it.

