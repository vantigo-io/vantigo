---
title: "Inventory module"
description: "The stock of the catalog's goods in warehouses and stores: sites, each the root of a tree of typed locations with codes and full codes, created one at a time or in bulk, re-parented, archived and deleted under their rules; the ledger of immutable movements — adjustments, moves and goods receipts — whose lines keep a balance per variant and location, exact to three decimals, never below zero, goods only, within one site, idempotent by a client key; the catalog's variants read and locked through contracts.ProductCatalog on the posting's own transaction, and the VariantReferenceHolder that makes Products refuse to delete a stocked variant or make its product a service; the reads — a location's contents, a product's and a variant's stock, the movements, the lookup by code and the dashboard's figures; the six permissions and the both-views rule; the lock orders; the personal data kept as ledger records; the refusals, the switch, what comes next and the endpoints."
sources:
  - apps/server/internal/inventory
  - apps/inventory/frontend
  - openapi/inventory.yaml
  - apps/server/internal/openapi/specs/inventory.yaml
  - apps/server/internal/db/migrations/00108_inventory_baseline.sql
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

This page describes delivery A of phase 1: the [contracts](#contracts-consumed),
[the schema](#the-schema), the [permissions](#permissions),
[sites and locations](#sites-and-locations), [the ledger](#the-ledger),
[the locks](#locks), [the reads](#reads), the [personal data](#personal-data) it keeps,
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
  answers their entries: the SKU, the product's name, type and status, the unit. It is
  the first lock every posting takes after reading its key ([locks](#locks)), so
  Products' rows are locked before any of Inventory's, everywhere. A variant absent from
  the answer is the line's `variant_not_found`. Because the product row is shared too,
  a receipt that read *Goods* cannot commit beside a type change to *Service*, and a
  variant's delete waits for a posting in flight and then sees its stock.
- **`Variants(ctx, ids)`** — the batch read naming the variants a response carries.
- **`VariantsOfProduct(ctx, productID)`** — every variant of a product, for the
  product's [stock](#reads).
- **`LookupCode(ctx, code)`** — the [lookup](#the-lookup)'s variant half: a SKU,
  trimmed and matched case-insensitively and exactly, or a variant barcode matched
  exactly.
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
- **`events`** — the record of every structural write (I31): `entity_kind` (`site` or
  `location`), `entity_id`, `site_id`, `kind`, `payload` (`jsonb`), `actor_kind`,
  `actor_user_id`, `actor_display`, `occurred_at`. Movements are their own record and
  are not repeated here; a deleted location's events stay. The table has no read
  endpoint in this phase.

## Permissions

| Key | Display | Grants |
| --- | --- | --- |
| `inventory:locations-view` | View locations | "See the sites and their locations, and find a location by its code, without quantities." |
| `inventory:locations-manage` | Manage locations | "Create, change, archive and delete sites and locations, one by one or in bulk." |
| `inventory:stock-view` | View stock | "See what lies where: the quantities on hand, a product's Stock card, a location's contents and the movements." |
| `inventory:stock-adjust` | Adjust stock | "Post adjustments and moves of stock between the locations of a site." |
| `inventory:receipts-manage` | Receive goods | "Post goods receipts, putting delivered goods on the locations of a site." |
| `inventory:counts-manage` | Count stock | "Open, count, post and cancel stock counts." — **reserved for delivery B**: no operation of delivery A asks for it. |

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
home are offered with either view; Movements and Receipts in the sidebar, the
product page's Stock card and the dashboard's figures with both.

| Reads and writes | `locations-view` | + `stock-view` | + `locations-manage` | + `stock-adjust` | + `receipts-manage` |
| --- | --- | --- | --- | --- | --- |
| Sites, their location trees, a location, the lookup's location matches | ✓ | ✓ | ✓ | ✓ | ✓ |
| Contents, a product's and a variant's stock, the movements, the variant search, the stats, the lookup's item matches | – | ✓ | – | ✓ | ✓ |
| Create, change, archive, reactivate and delete sites and locations; bulk | – | – | ✓ | – | – |
| Adjustments and moves | – | – | – | ✓ | – |
| Goods receipts | – | – | – | – | ✓ |

Each column adds to `locations-view`; the write columns assume `stock-view` too.

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
- **Archiving** (`status: archived`) is refused while anything in the site holds stock
  (`location_has_stock`; [the detail](#refusals) gives the quantity and the number of
  stock lines to a caller with `stock-view`), and archives every location of the site.
- **Reactivating** restores the site and its root **only**; the locations below stay
  archived until each is reactivated.
- **Deleting** is refused when any movement was ever posted in the site
  (`location_has_history`, *Site has history*: archive it instead); otherwise its
  locations and the site go in one transaction. A site with no movement has no stock,
  since only a movement puts stock down.

`GET /sites` lists the active sites, or those of the `status` asked for.

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
  untouched**: the location moved, with its stock.
- A **recode or a re-parent** rewrites the full codes, the ancestors and the depths of
  the whole subtree in one statement, refused with `full_code_taken` if any new full
  code is held; every rewritten row's revision is bumped.
- **Archive** cascades to the subtree and is refused while stock lies anywhere in it
  (`location_has_stock`, with the quantity and the number of stock lines to a caller
  with `stock-view`). An archived
  location takes no stock (`location_archived` on a line).
- **Reactivate** restores the location **alone**, and only under an active parent.
- **The root** takes only a name and a sort order: a `PUT` changing its `parentId`,
  `kind`, `code` or `status` is `root_location`, since the site's status governs it.

**Delete** takes the whole subtree, and is refused when any movement line ever named any
of it, from or to (`location_has_history`: archive it instead), and for a site's root
(`root_location`).

`GET /sites/{id}/locations` answers the site's tree flat, by full code, every status
unless one is asked for, **capped at 10,000 rows**; `GET /locations/{id}` answers one
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
on the parent listing every full code the bulk made.

## The ledger

### Movements and lines

Every change of stock is a **movement** (I8): a kind — `adjustment`, `move` or
`receipt` in this delivery; the set is open, `count` comes with B and `issue`,
`production` and `transfer` with Orders and Manufacturing — a site, an adjustment's
reason, an optional note, a receipt's optional supplier and supplier's reference, the
poster and the time. **A movement is never edited or deleted**; a mistake is corrected
by a new adjustment.

A **line** (I9) is a variant, an optional *from* and an optional *to* — a receipt's line
has only *to*, an adjustment's increase only *to* and its decrease only *from*, a move
both — and a quantity more than 0, with the variant's SKU, product name and unit
snapshotted, so history reads after the variant is gone.

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
the engine's internal callers — B's count post and import — are not capped. **Every
failing line is answered at once**, in the problem's `lines: [{index, code, detail}]`,
and nothing is posted. The shape's, the catalog's and the locations' refusals are
judged together first — `variant_not_found`, `variant_not_stockable`,
`variant_discontinued`, `location_not_found`, `location_archived`,
`move_crosses_sites`, `same_location`, `quantity_invalid` — and only when none failed
are the balances judged, for `insufficient_stock`; after an insufficient line the rest
are still judged against the balance as the earlier lines left it. The problem is 409
with the code `insufficient_stock` whenever a line has it, and 400 with the first
line's code otherwise.

## Locks

**A posting** (`posting.go`), in this order:

1. The `clientRequestId` is read with a plain `SELECT`; a hit answers the
   [replay or the reuse](#idempotency).
2. The request as a whole: `too_many_lines`, `nothing_to_post`, and an unknown site as
   a 400 field error on `siteId`.
3. **`ProductCatalog.LockVariants`** on the posting's transaction: `FOR SHARE` on the
   sorted distinct variants and their products.
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
8. The lines are applied in order, the balance checked after each.
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

**With Products.** Products' rows are locked before Inventory's everywhere: a posting
shares the variants first, and a variant's delete or a type change locks them
`FOR UPDATE` and then asks the [holder](#contracts-provided), which reads under them.

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
  `limit` up to 50 (20 by default), through the catalog's `SearchVariants`.

Each reads the balances in one snapshot and names the variants through the catalog
outside it.

### Movements

`GET /movements` lists the movements **newest first** by `(posted_at, id)` (I30),
filtered by `siteId`, `locationId` (a line from or to the location **or below it**),
`variantId`, `kind`, and `from` and `to` — **business days** in the workspace's zone,
`to` inclusive. `limit` is 100 by default and at most 500; `nextCursor` is an opaque
base64url cursor. A filter or cursor that does not parse, or `to` before `from`, is a
400. `GET /movements/{id}` answers one movement with its site, poster, source
reference when set, and its lines with their variants — a variant the catalog no longer
knows named by the line's own snapshot.

### The lookup

`GET /lookup?q=` is one field a keyboard-wedge scanner types into (I28). The text is
trimmed and matched, in order:

1. a **`LOC` token** — `LOC` and a location's id, case-insensitive (the label of
   delivery B; the lookup already reads it);
2. a location's **full code**, case-insensitive;
3. a variant's **SKU or barcode**, through `LookupCode`.

The access is the **session**, and each kind of match is shown by its own keys: the
location matches to `locations-view`, the item matches to `locations-view` with
`stock-view`. A caller holding neither gets no matches rather than a 403. An empty
text, or one longer than the limit, is a 400 on `q`.

### Stats

`GET /stats` answers the dashboard card's figures: the active sites, the active
locations below the roots, the **balances** (one per variant and location holding
stock, the *Stock lines*), and **`receiptsThisWeek`**, the receipts posted since
**Monday 00:00** of the business week in the workspace's zone. `openCounts` is 0 until
delivery B. `GET /stats/attention` is the session's and answers **no item** in this
delivery; B adds a count left open too long.

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
`lines[i].quantity`); a posting's refusal carries `lines`.

| Code | Status | When |
| --- | --- | --- |
| *(field error)* | 400 | A required field missing, a text too long, a kind or a reason that is none of its list, an address field out of its rule, an unknown `siteId` on a posting; any property the schema does not name, `lines[i].x` and `levels[i].x` included; a missing or invalid `If-Match`; a query filter, cursor or limit that does not parse. |
| `code_invalid` | 400 | A code not 1–16 `A–Z0-9` after upper-casing, or `LOC` and digits. |
| `depth_exceeded` | 400 | A location, or a re-parented subtree's deepest, more than 8 levels below the root. |
| `parent_invalid` | 400 | A parent that is unknown or vanished — deleted concurrently included, for a single create and a bulk alike — in another site, archived, or the location itself or below it. |
| `bulk_too_large` | 400 | A bulk create of more than 1000 locations. |
| `too_many_lines` | 400 | A posting through the API with more than 500 lines. |
| `nothing_to_post` | 400 | No lines, or every `set` line already matching what lies there. |
| `variant_not_found` | 400 (line) | A line's variant the catalog does not answer. |
| `variant_not_stockable` | 400 (line) | A Service product's variant on a line that increases stock. |
| `variant_discontinued` | 400 (line) | A Discontinued product's variant on a receipt. |
| `location_not_found` | 400 (line) | A line's location that does not exist. |
| `location_archived` | 400 (line) | A line's location that is archived. |
| `move_crosses_sites` | 400 (line) | A line naming a location outside the movement's site. |
| `same_location` | 400 (line) | A move's from and to the same location. |
| `quantity_invalid` | 400 (line) | A quantity not more than 0 (a `set` below 0), a `change` of 0, more than three decimals, or a line or balance above 99,999,999,999.999. |
| `site_code_taken` | 409 | A site code another site holds. |
| `location_code_taken` | 409 | A code a sibling holds; a bulk names every colliding full code. |
| `full_code_taken` | 409 | A full code — of a recode, a re-parent's subtree, a site's new code or a bulk's deeper level — another location holds. |
| `location_has_stock` | 409 | Archiving a site or a location while stock lies in it or below. The detail gives the quantity and the number of stock lines only to a caller holding `inventory:stock-view`; to anyone else it says only that the location, or something below it, holds stock and must be emptied first. |
| `location_has_history` | 409 | Deleting a location whose subtree a movement line ever named, or a site with any movement. |
| `root_location` | 409 | A root's `PUT` changing `parentId`, `kind`, `code` or `status`, or its delete. |
| `insufficient_stock` | 409 (line) | A line taking a balance below zero; the problem's code whenever any line has it. |
| `client_request_reused` | 409 | A `clientRequestId` posted before with a different body. |
| `revision_conflict` | 409 | The `If-Match` revision of a site or a location has moved on. |
| `dependency_unavailable` | 503 | The catalog is not composed — never so in a running `inventory`, which `MODULES` refuses without `products`. |

An unknown id is a bare 404, and a caller without the operation's permission the
access layer's uniform 403.

**400 and 409, not 422 and 412.** The design's §4 gives its field and line rules 422
and a stale `If-Match` 412 (`revision_mismatch`). The house answers a rule about the
request 400 and a rule about the state of things 409, as every other module does, and so
does this one: 400 for the field and line rules, 409 `revision_conflict` for a stale
revision.

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
setting.

## What comes next

**Delivery B** completes phase 1:

- **Packagings** — a box of 15, a pallet of 40 boxes — a Products feature pulled
  forward, so goods are entered and scanned in packagings and a packaging's barcode
  joins the lookup.
- **Counts** — a count of a site or a subtree that freezes the expected quantities,
  is counted, flags a line whose balance moved meanwhile and posts one `count` movement
  of the differences; `inventory:counts-manage`, `location_has_open_count` on an
  archive, and an attention item for a count left open.
- **Labels** — a location's Code 128 label carrying its `LOC` token, the full code and
  the name.
- **The opening-balance import** — a CSV of full code, SKU and quantity for one site,
  dry run then one `opening_balance` adjustment, all or nothing.
- **The product list's stock column** — an optional *On hand* column on Products' list.

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
| `GET /sites/{id}/locations` | `locations-view` | The site's tree, flat, by full code, at most 10,000. |
| `POST /locations` | `locations-manage` + `locations-view` | Create a location under an active parent. |
| `POST /locations/bulk` | `locations-manage` + `locations-view` | Create up to 1000 locations in up to three levels, all or nothing. |
| `GET /locations/{id}` | `locations-view` | One location with its site, path and children. |
| `PUT /locations/{id}` | `locations-manage` + `locations-view` | Change, re-parent, archive or reactivate a location, with `If-Match`. |
| `DELETE /locations/{id}` | `locations-manage` + `locations-view` | Delete a location and its subtree, with no history. |
| `GET /locations/{id}/contents` | `locations-view` + `stock-view` | What lies on the location, and below it unless `subtree=false`. |
| `GET /products/{productId}/stock` | `locations-view` + `stock-view` | Every goods variant of a product with its stock. |
| `GET /variants` | `locations-view` + `stock-view` | Goods variants by SKU, name or barcode. |
| `GET /variants/{variantId}/stock` | `locations-view` + `stock-view` | One variant's stock, a deleted one by its snapshot. |
| `GET /movements` | `locations-view` + `stock-view` | Movements by site, location, variant, kind and business day, newest first. |
| `GET /movements/{id}` | `locations-view` + `stock-view` | One movement with its lines. |
| `POST /movements/adjustments` | `locations-view` + `stock-view` + `stock-adjust` | Post an adjustment; 201, or 200 for a replay. |
| `POST /movements/moves` | `locations-view` + `stock-view` + `stock-adjust` | Post a move; 201, or 200 for a replay. |
| `POST /movements/receipts` | `locations-view` + `stock-view` + `receipts-manage` | Post a goods receipt; 201, or 200 for a replay. |
| `GET /stats` | `locations-view` + `stock-view` | The dashboard card's figures. |
| `GET /stats/attention` | session | The attention items; none in this delivery. |

Every key is `inventory:`'s; the table leaves the prefix out.
