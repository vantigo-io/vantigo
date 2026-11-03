---
title: "Inventory module"
description: "The stock of the catalog's goods in warehouses and stores: sites, each the root of a tree of typed locations with codes and full codes, created one at a time or in bulk, re-parented, archived and deleted under their rules; the ledger of immutable movements — adjustments, moves, goods receipts and counts — whose lines keep a balance per variant and location, exact to three decimals, never below zero, goods only, within one site, idempotent by a client key, and may be entered in packagings; counts that freeze the expected quantities, are counted and post their differences as one movement; location labels; the opening-balance import; the catalog's variants read and locked through contracts.ProductCatalog on the posting's own transaction, and the VariantReferenceHolder that makes Products refuse to delete a stocked variant or make its product a service; the reads — a location's contents, a product's and a variant's stock, the stock totals, the movements, the lookup by code and the dashboard's figures and attention item; the six permissions and the both-views rule; the lock orders; the personal data kept as ledger records; the refusals, the switch, what comes next and the endpoints."
sources:
  - apps/server/internal/inventory
  - apps/inventory/frontend
  - openapi/inventory.yaml
  - apps/server/internal/openapi/specs/inventory.yaml
  - apps/server/internal/db/migrations/00108_inventory_baseline.sql
  - apps/server/internal/db/migrations/00110_inventory_counts.sql
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
a variant that still lies in stock.

This page describes phase 1, deliveries A and B: the [contracts](#contracts-consumed),
[the schema](#the-schema), the [permissions](#permissions),
[sites and locations](#sites-and-locations), [the ledger](#the-ledger),
[entering in packagings](#entering-in-packagings), [counts](#counts),
[labels](#labels), [the opening-balance import](#the-opening-balance-import),
[the locks](#locks),
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
The module reads five of its methods:

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
  with the packaging that matched. The [import](#the-opening-balance-import) uses it
  too, and takes only a SKU match.
- **`SearchVariants(ctx, q, limit)`** — the line editor's picker: goods variants by SKU,
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

### Contracts provided

The module fills one many-provider slot, **`Module.VariantReferences`** with a
**`contracts.VariantReferenceHolder`** (I23, rule 20 of the
[module boundaries](/en/contributing/module-boundaries/)). Products asks every holder
**inside its own transaction**, after it has locked the variants `FOR UPDATE`:

- when it **deletes a variant** (`DELETE /api/v1/products/{id}/variants/{variantId}`),
  for that variant; and
- when a `PUT` turns a **Goods** product into a **Service**, for every variant of the
  product in id order.

`VariantHeld(ctx, tx, variantID)` sums the variant's balances on Products' transaction
and answers `nil` when it lies nowhere, or a `VariantHold`: the quantity as the shortest
decimal that says it (`"34"`, `"2.5"`, `"0.125"`) and the number of locations it lies
on. The first hold refuses Products' write with 409 — *Variant has stock* or *Product
has stock* — with the unit from Products' own row. The holder locks nothing of its own:
every posting that could add to the variant holds `FOR SHARE` on it through
`LockVariants`, so it has committed before Products' `FOR UPDATE` was granted, or waits
for Products' transaction. The holder needs nothing of `Deps` and is collected
**whether or not `inventory` is enabled** ([enabling and disabling](#enabling-and-disabling)).

The module provides no single-provider contract: no other module reads stock in this
phase (I34).

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
  (`adjustment`, `move` or `receipt`; an open set kept as text), `reason` (an
  adjustment's), `note` (1000), `supplier_name` (200) and `supplier_reference` (100) (a
  receipt's), the **source reference** `source_module`, `source_kind`, `source_id`,
  `source_line_id` — null in this phase and indexed for the modules that will set it —
  `posted_by_user_id`, `posted_by_display` and `posted_at`.
- **`movement_lines`** — `movement_id`, `line_no`, `variant_id`, the **snapshots**
  `sku`, `product_name` and `unit` as they were at posting, `from_location_id` and
  `to_location_id` (either may be null), and `quantity` (`numeric(14,3)`), indexed by
  variant and by each location.
- **`balances`** — `variant_id`, `location_id`, `on_hand` (`numeric(14,3)`),
  `updated_at`; the key is `(variant_id, location_id)`. A row reaching zero is deleted,
  so the table lists only where stock lies.
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
  counter (`counted_by_user_id`, `counted_by_display`, `counted_at`). One line per
  `(count_id, variant_id, location_id)` (`ux_count_lines_count_variant_location`),
  indexed by location.
- **`movement_lines.entered`** (`jsonb`) — the snapshot of a line entered in
  packagings.
- **`movements.count_id`** — the count a `count` movement posted, indexed where set.

Products' packagings are Products' table, `products.variant_packagings`
(`00109_products_packagings.sql`); Inventory reads them only through the catalog.

## Permissions

| Key | Display | Grants |
| --- | --- | --- |
| `inventory:locations-view` | View locations | "See the sites and their locations, and find a location by its code, without quantities." |
| `inventory:locations-manage` | Manage locations | "Create, change, archive and delete sites and locations, one by one or in bulk." |
| `inventory:stock-view` | View stock | "See what lies where: the quantities on hand, a product's Stock card, a location's contents and the movements." |
| `inventory:stock-adjust` | Adjust stock | "Post adjustments and moves of stock between the locations of a site." |
| `inventory:receipts-manage` | Receive goods | "Post goods receipts, putting delivered goods on the locations of a site." |
| `inventory:counts-manage` | Count stock | "Open, count, post and cancel stock counts." |

All six are in the category "Inventory", **none is sensitive** and all are delegable;
the Owner holds them through the wildcard. In Norwegian they read *Se lagersteder*,
*Administrere lagersteder*, *Se beholdning*, *Justere beholdning*, *Motta varer* and
*Telle varer*.

Access is **all-of**: an operation's `x-vantigo-access` names every key it needs,
fully qualified (`permission:inventory:locations-view+inventory:stock-view`), and a
caller missing any gets the access layer's uniform 403.

**Both views.** Every stock read and every write also requires `locations-view` and
`stock-view`, because each shows location codes and each form picks locations. So a
stock role is `locations-view` + `stock-view` (+ `stock-adjust`, `receipts-manage`), and
`locations-view` alone gives the structure without quantities. The tile and the app's
home are offered with either view; Movements, Receipts, Counts and Import opening
balances in the sidebar, the product page's Stock card, the product list's *On hand*
column and the dashboard's figures with both.

| Reads and writes | `locations-view` | + `stock-view` | + `locations-manage` | + `stock-adjust` | + `receipts-manage` | + `counts-manage` |
| --- | --- | --- | --- | --- | --- | --- |
| Sites, their location trees, a location, the lookup's location matches, [labels](#labels) | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Contents, a product's and a variant's stock, the stock totals, the movements, the variant search, the stats, the lookup's item matches | – | ✓ | – | ✓ | ✓ | ✓ |
| Create, change, archive, reactivate and delete sites and locations; bulk | – | – | ✓ | – | – | – |
| Adjustments and moves; the opening-balance import | – | – | – | ✓ | – | – |
| Goods receipts | – | – | – | – | ✓ | – |
| Counts: list, open, count, post, cancel; the attention item | – | – | – | – | – | ✓ |

Each column adds to `locations-view`; the write columns assume `stock-view` too.
**Labels need `locations-view` alone**: a label prints a location's token, code and
name, which that key already shows. The design's §5 table put labels under
`locations-manage`; its contract and §6 say view, and view was kept — printing changes
nothing.

**`GET /meta`** is the session's: it answers the caller's six capabilities —
`canViewLocations`, `canManageLocations`, `canViewStock`, `canAdjust`, `canReceive`,
`canCount` — so the app shows only what works and a caller with none of them learns
that rather than a 403.

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

A **site** is a `warehouse` or a `store` (I1), with a name, an optional address and a
status. The kind changes the words on screen and no rule. **Creating a site creates its
root location** — kind `site`, coded as the site — in the same transaction. `PUT` takes
`If-Match` with the site's revision and the whole site:

- **A new code** is checked against the other sites, then followed through the root and
  every full code below it in one statement (I4). Printed labels keep scanning, since
  they carry the token, not the code.
- **Archiving** (`status: archived`) is refused while any [count](#counts) in the site
  is still counting (`location_has_open_count`), then while anything in the site holds
  stock (`location_has_stock`; [the detail](#refusals) gives the quantity and the
  number of stock lines to a caller with `stock-view`), and archives every location of
  the site.
- **Reactivating** restores the site and its root **only**; the locations below stay
  archived until each is reactivated.
- **Deleting** is refused when any movement was ever posted, or any count opened, in
  the site (`location_has_history`, *Site has history*: archive it instead); otherwise
  its locations and the site go in one transaction. A site with no movement has no
  stock, since only a movement puts stock down.

`GET /sites` lists the active sites, or those of the `status` asked for. Every site it
answers — and `GET` and `PUT /sites/{id}` — carries its **figures**, read in one
query per request: `locationCount`, the active locations below the root, to everyone;
and to a caller holding `stock-view`, `stockLines` (the balances in the site) and
`lastReceiptAt` (the last receipt posted there, absent when there was none). The app's
home draws its site cards from them with no request per card.

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
  the number of stock lines to a caller with `stock-view`). An archived location takes
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
`count_opened`, `count_posted` and `count_cancelled`, with entity kind `count`.

## The ledger

### Movements and lines

Every change of stock is a **movement** (I8): a kind — `adjustment`, `move`,
`receipt` or `count` in this phase; the set is open, and `issue`, `production` and
`transfer` come with Orders and Manufacturing — a site, an adjustment's reason, an
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
outside it is `move_crosses_sites`, whatever the movement's kind. A move between sites
waits for Orders' transfer.

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
are still judged against the balance as the earlier lines left it. The problem is 409
with the code `insufficient_stock` whenever a line has it, and 400 with the first
line's code otherwise.

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
   that moment. A scope with no balances opens with no lines: every line it gets is
   added, which is how stock is first placed (I20).

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

- every **counted** line is posted as a **`set`** of its location's on hand to the
  counted quantity, so the engine computes the difference against the balance it has
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

## The opening-balance import

`POST /imports/opening-balances?siteId=…&dryRun=…` takes a **CSV** body (`text/csv`) and
places a site's first stock (I32). It needs `stock-adjust` and both views.

- **The file** is read by `srv/fileimport` from its bytes as sent: UTF-8 or
  Windows-1252 (as Excel saves it), the separator `;`,
  `,` or tab detected, at most **4 MiB** and **5000 rows**. Its header names **exactly**
  the columns `location`, `sku` and `quantity`, in any order and any case. A file that
  does not read, another header, too many rows or an unknown site is a 400 field error
  (*Invalid import*) on `file` or `siteId`.
- **Each row** is resolved before any transaction, in the order location, variant,
  stockable, quantity, the first failure being its outcome. Checked before all of them:
  a row with a non-blank cell beyond the header's three columns is `quantity_invalid`,
  *The row has more cells than the header. Put a quantity with a decimal comma in
  quotes, such as "2,5", or separate the columns with ;* — a comma-separated file
  splits an unquoted `2,5` in two. Blank trailing cells are ignored.
  - `location` — a **full code**, upper-cased, among the site's locations; one that is
    not there, or is archived, is `location_not_found`;
  - `sku` — through the catalog's `LookupCode`, a **SKU match only** (a barcode, the
    variant's or a packaging's, does not count): `variant_not_found`;
  - a Service product's variant: `variant_not_stockable`;
  - `quantity` — a decimal more than 0, at most three decimals, `.` or `,` as the
    decimal separator, **no** thousands separator, sign or exponent, at most
    99,999,999,999.999: `quantity_invalid`.

  Each row answers its `row` (the header being row 1), the `location`, `sku` and
  `quantity` as written, its `outcome` and a `detail`.
- **A dry run** — `dryRun` defaults to **true** — answers 200 with every row and `ok`,
  true when every row is `ok`. Nothing is written.
- **A commit** (`dryRun=false`) with any failing row posts **nothing** and answers
  **200 with `ok: false`** and the rows. With every row ok it posts **one `adjustment`**,
  reason `opening_balance`, every row a `change` adding its quantity, through the
  engine with **no line cap**, and answers **201** with `movementId`.
- **Its key** is a UUIDv5 of the SHA-256 of the file's bytes and the site's id, so the
  **same file posted again to the same site** is the same movement: it answers **200**
  with the same `movementId` and posts nothing. A different file — even one row changed
  — is a new movement.

The import reads the catalog once per distinct SKU.

## Locks

**A posting** (`posting.go`), in this order:

1. The `clientRequestId` is read with a plain `SELECT`; a hit answers the
   [replay or the reuse](#idempotency).
2. The request as a whole: `too_many_lines`, `nothing_to_post`, and an unknown site as
   a 400 field error on `siteId`.
3. **`ProductCatalog.LockVariants`** on the posting's transaction: `FOR SHARE` on the
   sorted distinct variants and their products. Every [entry in
   packagings](#the-entry) is resolved right after, against the packagings it answered;
   a posting's lines are cloned per attempt, so a rerun resolves afresh.
4. The key is **claimed** by writing the movement's header, `INSERT … ON CONFLICT DO
   NOTHING RETURNING`; a conflict re-reads it, as in step 1.
5. The locations `FOR SHARE`, in id order. A posting writes nothing to a location, so
   two postings never wait for each other there.
6. Every line failure found so far is refused at once.
7. Every balance the posting touches is created at zero where missing **and** locked by
   **one** `INSERT … ON CONFLICT DO UPDATE … RETURNING`, in (variant, location) order. Two
   postings to a new pair serialise on the row rather than collide on its key, and a
   balance a concurrent posting deleted at zero is inserted again by the same statement,
   so no lock is held across a second attempt.
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
count row `FOR UPDATE` first**: a `PUT` of lines then `LockVariants`, then the lines'
locations `FOR SHARE` in id order, as a posting takes them, then the lines it writes; a post then the posting's own order above, from step 3, on a savepoint of the
same transaction; a cancel nothing more. So two writes to one count are serial, and a
post never runs beside a `PUT` of the same count. A tree archive reads the open counts
under its site row's lock; an open takes the same lock, so neither misses the other.

**The import** resolves every row outside any transaction, then posts as an API posting
does, from step 1, with its derived key.

**With Products.** Products' rows are locked before Inventory's everywhere: a posting
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
  by site (by code) and location (by full code), with the totals summed exactly. A
  product the catalog does not know is the 404; a Service product answers no variants.
- **`GET /variants/{variantId}/stock`** — one variant's stock by site and location. A
  variant the catalog **no longer knows** is named by its latest movement line's
  snapshot and marked deleted; one neither the catalog nor the ledger knows is the 404.
  Stock cannot outlive its variant (I23), so the fallback names history.
- **`GET /variants`** — the line editor's picker, `q` of 1 to 100 characters and
  `limit` up to 50 (20 by default), through the catalog's `SearchVariants`, which does
  not search packaging barcodes.
- **`GET /stock?variantIds=`** — the product list's *On hand* column (I33): each
  variant's on hand across every location, summed exactly, for at most **100** ids
  (comma-separated, de-duplicated); none or more than 100 is a 400 on `variantIds`. A
  variant with no stock is absent from `totals`. Products reads nothing of Inventory on
  the server: the host composes the column into Products' list through the list's
  `extraColumns`, with one request per page, or per 100 of its variants.

Each reads the balances in one snapshot and names the variants through the catalog
outside it.

### Movements

`GET /movements` lists the movements **newest first** by `(posted_at, id)` (I30),
filtered by `siteId`, `locationId` (a line from or to the location **or below it**),
`variantId`, `kind` (`count` among them), and `from` and `to` — **business days** in the workspace's zone,
`to` inclusive. `limit` is 100 by default and at most 500; `nextCursor` is an opaque
base64url cursor. A filter or cursor that does not parse, or `to` before `from`, is a
400. `GET /movements/{id}` answers one movement with its site, poster, source
reference when set, `countId` for a count movement, and its lines with their variants
and `entered` when they were entered in packagings — a variant the catalog no longer
knows named by the line's own snapshot.

### The lookup

`GET /lookup?q=` is one field a keyboard-wedge scanner types into (I28). The text is
trimmed and matched, in order:

1. a **`LOC` token** — `LOC` and a location's id, case-insensitive, as a
   [label](#labels) carries it;
2. a location's **full code**, case-insensitive;
3. a variant's **SKU or barcode**, or a **packaging's barcode**, through `LookupCode`;
   a packaging's barcode answers its variant, once.

The access is the **session**, and each kind of match is shown by its own keys: the
location matches to `locations-view`, the item matches to `locations-view` with
`stock-view`. A caller holding neither gets no matches rather than a 403. An empty
text, or one longer than the limit, is a 400 on `q`.

### Stats

`GET /stats` answers the dashboard card's figures: the active sites, the active
locations below the roots, the **balances** (one per variant and location holding
stock, the *Stock lines*), and **`receiptsThisWeek`**, the receipts posted since
**Monday 00:00** of the business week in the workspace's zone, and **`openCounts`**,
the counts still counting.

`GET /stats/attention` is the session's. It raises one item type, **`countOpenLong`**:
one item per count still counting **more than seven days** after it was opened —
by which a count meant for a day or a weekend has been forgotten while its expected
quantities drift — oldest first, at most 20. Its `id` is `countOpenLong/<countId>`,
`entityId` the count's id, `occurredAt` when it was opened, and `title` *The count of
{scope's full code} has been open for N days*; the dashboard shows it as *A count has
been open for a long time* and links to the count. It is raised only to a caller who
could post or cancel it — `counts-manage` with both views; anyone else gets none.

## Personal data

The module stores about a person their **user id and display name** as a movement's
poster (`posted_by_user_id`, `posted_by_display`) and as an event's actor
(`actor_user_id`, `actor_display`), and nothing else. A movement is an **immutable
record of the stock ledger** and an event the record of a structural write; both keep
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
their 409s.

| Code | Status | When |
| --- | --- | --- |
| *(field error)* | 400 | A required field missing, a text too long, a kind or a reason that is none of its list, an address field out of its rule, an unknown `siteId` on a posting; any property the schema does not name, `lines[i].x` and `levels[i].x` included; a missing or invalid `If-Match`; a query filter, cursor or limit that does not parse. |
| `code_invalid` | 400 | A code not 1–16 `A–Z0-9` after upper-casing, or `LOC` and digits. |
| `depth_exceeded` | 400 | A location, or a re-parented subtree's deepest, more than 8 levels below the root. |
| `parent_invalid` | 400 | A parent that is unknown or vanished — deleted concurrently included, for a single create and a bulk alike — in another site, archived, or the location itself or below it. |
| `bulk_too_large` | 400 | A bulk create of more than 1000 locations. |
| `too_many_lines` | 400 | A posting through the API, or a count's `PUT`, with more than 500 lines. |
| `labels_too_many` | 400 | More than 1000 labels: the ids asked for (the detail names the number), or with their subtrees (the detail says more than 1000). |
| `entered_invalid` | 400 (line) | Both or neither of a quantity and `entered`; a packaging named twice; a count below 1; more than ten packagings; `decrease` with a quantity, or under `set`. |
| `packaging_not_found` | 400 (line) | An entry naming a packaging the variant does not have at posting (or when the count line is recorded). |
| `nothing_to_post` | 400 | No lines, or every `set` line already matching what lies there. |
| `variant_not_found` | 400 (line) | A line's variant the catalog does not answer. |
| `variant_not_stockable` | 400 (line) | A Service product's variant on a line that increases stock, or added to a count. |
| `variant_discontinued` | 400 (line) | A Discontinued product's variant on a receipt. |
| `location_not_found` | 400 (line) | A line's location that does not exist, or a count line's location outside the count's scope; as a field error, a count's scope or a label's location that does not exist. |
| `location_archived` | 400 (line) | A line's location that is archived. |
| `move_crosses_sites` | 400 (line) | A line naming a location outside the movement's site. |
| `same_location` | 400 (line) | A move's from and to the same location. |
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
| `insufficient_stock` | 409 (line) | A line taking a balance below zero; the problem's code whenever any line has it. Never raised by an import or a count, whose lines only add or set. |
| `client_request_reused` | 409 | A `clientRequestId` posted before with a different body. |
| `revision_conflict` | 409 | The `If-Match` revision of a site or a location has moved on. |
| `dependency_unavailable` | 503 | The catalog is not composed — never so in a running `inventory`, which `MODULES` refuses without `products`. |

An unknown id is a bare 404, and a caller without the operation's permission the
access layer's uniform 403.

An import's rows are not refusals: a row that fails is its outcome — `location_not_found`,
`variant_not_found`, `variant_not_stockable` or `quantity_invalid` — in a 200 with
`ok: false`.

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
the schema holds stock of it. Inventory contributes **no background worker** and no
setting — a count left open is raised on the dashboard when it is read, not by a job.

## What comes next

**Phase 1 is complete.** Delivery A built sites, locations, the ledger and the reads;
delivery B built [entering in packagings](#entering-in-packagings) and the breakdown,
[counts](#counts), [labels](#labels), [the opening-balance
import](#the-opening-balance-import), the [stock totals](#stock) for the product list's
*On hand* column, the site figures and the server-side search of a site's locations.
One thing of the design was left out: the notice that a printed label's text is stale
after a rename or a recode (I7, §9); the print dialog says instead that a label keeps
scanning and needs reprinting only when its text should change. Labels are chosen in the
print dialog from a searchable list, not by ticking locations in the tree, and the count
screen draws every line of the scope it shows, without virtualisation.

**Later** (§11), each waiting for what it names: transfers between sites (Orders);
reservations and `contracts.Stock` (Orders, the domain-events outbox); lots, serials and
expiry (Manufacturing); negative stock by a setting (point of sale); placement and
min/max (the owner's call); units beyond packagings and supplier records (Products
phase 5); camera scanning; thermal label printers; valuation; store flags.

The phase keeps what Orders and Manufacturing need (§12): the source reference on every
movement, null and indexed; the kinds as an open set; lines with no *from* or no *to*;
balances as rows no read treats as the whole key, so a lot joins it later; lots in
Inventory, asked through the same holder; reservations per site, not a column.

## Endpoints

All under `/api/v1/inventory`. The permission column names every key the operation
requires, all of them.

| Operation | Permission | |
| --- | --- | --- |
| `GET /meta` | session | The caller's six capabilities. |
| `GET /lookup` | session | A location by token or full code (`locations-view`), an item by SKU or barcode (+ `stock-view`). |
| `GET /sites` | `locations-view` | The active sites, or those of a status. |
| `POST /sites` | `locations-manage` + `locations-view` | Create a site and its root. |
| `GET /sites/{id}` | `locations-view` | One site with its root's id. |
| `PUT /sites/{id}` | `locations-manage` + `locations-view` | Change, recode, archive or reactivate a site, with `If-Match`. |
| `DELETE /sites/{id}` | `locations-manage` + `locations-view` | Delete a site with no movement. |
| `GET /sites/{id}/locations` | `locations-view` | The site's tree, flat, by full code, at most 10,000 (`truncated` beyond), searched with `q`. |
| `POST /locations` | `locations-manage` + `locations-view` | Create a location under an active parent. |
| `POST /locations/bulk` | `locations-manage` + `locations-view` | Create up to 1000 locations in up to three levels, all or nothing. |
| `GET /locations/{id}` | `locations-view` | One location with its site, path and children. |
| `PUT /locations/{id}` | `locations-manage` + `locations-view` | Change, re-parent, archive or reactivate a location, with `If-Match`. |
| `DELETE /locations/{id}` | `locations-manage` + `locations-view` | Delete a location and its subtree, with no history. |
| `GET /locations/{id}/contents` | `locations-view` + `stock-view` | What lies on the location, and below it unless `subtree=false`. |
| `GET /products/{productId}/stock` | `locations-view` + `stock-view` | Every goods variant of a product with its stock. |
| `GET /variants` | `locations-view` + `stock-view` | Goods variants by SKU, name or barcode. |
| `GET /stock` | `locations-view` + `stock-view` | On hand of up to 100 variants across every location. |
| `GET /variants/{variantId}/stock` | `locations-view` + `stock-view` | One variant's stock, a deleted one by its snapshot. |
| `GET /movements` | `locations-view` + `stock-view` | Movements by site, location, variant, kind and business day, newest first. |
| `GET /movements/{id}` | `locations-view` + `stock-view` | One movement with its lines. |
| `POST /movements/adjustments` | `locations-view` + `stock-view` + `stock-adjust` | Post an adjustment; 201, or 200 for a replay. |
| `POST /movements/moves` | `locations-view` + `stock-view` + `stock-adjust` | Post a move; 201, or 200 for a replay. |
| `POST /movements/receipts` | `locations-view` + `stock-view` + `receipts-manage` | Post a goods receipt; 201, or 200 for a replay. |
| `GET /stats` | `locations-view` + `stock-view` | The dashboard card's figures. |
| `GET /stats/attention` | session | `countOpenLong`, to `counts-manage` + both views. |
| `GET /counts` | `counts-manage` + `locations-view` + `stock-view` | Counts newest first, at most 200, by status and site. |
| `POST /counts` | `counts-manage` + `locations-view` + `stock-view` | Open a count of a site or a subtree; 201. |
| `GET /counts/{id}` | `counts-manage` + `locations-view` + `stock-view` | One count with its lines. |
| `PUT /counts/{id}/lines` | `counts-manage` + `locations-view` + `stock-view` | Record up to 500 counted lines. |
| `POST /counts/{id}/post` | `counts-manage` + `locations-view` + `stock-view` | Post the count as one `count` movement. |
| `POST /counts/{id}/cancel` | `counts-manage` + `locations-view` + `stock-view` | Cancel a count still counting. |
| `POST /labels` | `locations-view` | A PDF of up to 1000 location labels. |
| `POST /imports/opening-balances` | `locations-view` + `stock-view` + `stock-adjust` | Dry run or commit a CSV of opening balances; 201, or 200 for a dry run, a refused commit or a replay. |

Every key is `inventory:`'s; the table leaves the prefix out.
