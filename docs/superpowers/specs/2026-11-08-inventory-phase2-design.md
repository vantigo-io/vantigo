# Inventory phase 2 — transfers between sites, and placement

Revision 2, 2026-11-08 (after a critic review and its re-review; §12 lists what changed). From the research
`docs/superpowers/research/2026-11-08-inventory-phase2.md` ("R"), whose answers in §9.2
this spec does not reopen. It extends the phase 1 design
`docs/superpowers/specs/2026-10-27-inventory-design.md` ("D"); decisions continue D's
numbering from **I35**, and a phase 1 rule this spec does not name stands. `srv/`
abbreviates `apps/server/internal/`, `mig/` `srv/db/migrations/`, `ui/`
`apps/inventory/frontend/src/`. Code references are against `main` at `4dcc14cd3`.

## 1. Purpose

Two deliveries:

- **A — transfers.** Stock sent from one site, in transit, received at another: a
  document of its own, sent, received in parts, closed with a difference, or cancelled;
  a one-step *send and receive*; a printed *følgeseddel*.
- **B — placement.** Per variant and site, a **primary location**, a **reorder point**
  and a **maximum**; the **low-stock list**, whose suggestion counts what is in transit and
  rounds up to whole packagings, saying so; *Create transfer* and *Export CSV* from it; a
  placements import.

A goes first: B's *incoming* and *Create transfer* read A.

**Not in this phase** (R §9.2): purchase orders and automatic ordering, snooze or dismiss,
a daily mail, domain events, transfer lines from a spreadsheet, a shelf-level minimum, the
stale-label notice (D I7, dropped), per-site permissions, valuation.

## 2. Decisions

### Transfers (A)

- **I35 — Inventory owns transfers** (R reading 1). ROADMAP.md says so in A; the Orders
  design's §13 line stays as written.
- **I36 — A transfer** is a document with:
  - a **number** from the module's own series (`inventory.counters`, `transfer`, starting
    at 1001), the Orders pattern (`srv/orders/settings.go`:25-36); shown as *Transfer
    1042* / *Overføring 1042*, and as the token `TRF-1042` on its note;
  - a **from site** and a **to site**: active, different (`transfer_same_site`);
  - an optional **reference** (≤ 100) and **note** (≤ 2000), and an optional **expected
    arrival** date;
  - **lines**, 1–500, each a variant and a quantity, or packagings entered as any line
    (D I25), with the SKU and name snapshotted when the line is written and again at send
    (so the list's search by SKU finds drafts); a variant may appear on several lines,
    which is how stock from two source locations travels;
  - a **status**: `draft`, `sent`, `partly_received`, `received`, `cancelled`;
  - a **revision** for If-Match on every write but receive (I41);
  - who created, sent and closed it, and when, as snapshots (D rule 19).
- **I37 — A draft holds nothing** (R reading 4). No reservation, no balance, no hold on the
  variant (I47). It may be edited and deleted freely. A draft line whose variant has gone
  from the catalog, or turned into a service, is shown as such and refused at send.
- **I38 — The actions and their order.** Send, receive and cancel each write one
  **action** (`inventory.transfer_actions`, §3.4) and, per line touched, an **action
  line** — the quantity, its kind (`sent`, `received`, `missing`, `returned`) and, for a
  received line, its destination location. The action is the record of who did what and
  where each part went; the line's four running totals (§3.3) are what is read. Every
  action runs in this order, in one transaction, inside the engine's
  serialization-and-deadlock retry — its own `db.RetrySerializable` around the locked
  transaction, as `postMovement` (`srv/inventory/posting.go`:518-535) wraps a single
  posting:
  1. the transfer row `FOR UPDATE`;
  2. **the replay check**: the action's client request id (I39–I43 say where it comes
     from) looked up in `transfer_actions`. The same request hash answers the stored
     result — the transfer as it is now — 200; another hash is `client_request_reused`
     for a client's id, and for a derived id the status refusal of step 3;
  3. If-Match (send, cancel) and the status;
  4. the sites `FOR SHARE` (from and to), and only then their status — `site_archived` is
     judged here, so an archive that committed first is seen and one racing the send
     waits;
  5. the engine's posting — `LockVariants`, the movement claim, locations, balances — with
     the action's per-line judgement (outstanding, I39) in `postOptions.check`, after the
     packagings are resolved to units;
  6. the action, its lines, the line totals and the status.

  **A movement's `client_request_id` is its action's** — derived or the client's — so the
  movement and the action are claimed by one key, never two.

  Taking the transfer row before Products' variant rows departs from `posting.go`'s
  *Products' rows before any of Inventory's* (:36-38). There is no cycle: Products never
  waits on a transfer row, and the count post already takes its count row first. The
  comment is amended to name both.
- **I39 — Send.** One request per transfer, naming each line's **source location** at the
  from site (`from_location_required` for a line without one). Each line is resolved —
  packagings to units, snapshotted — and checked as a move's line is (goods, location
  active and in the from site, on hand); one movement of kind **`transfer_out`** at the from
  site, its lines with a *from* and no *to*; action lines `sent`; the status `sent`.
  Discontinued variants may be sent, as they may be moved.
  - **The client request id is derived from the transfer** as `Issue`'s is
    (`srv/inventory/ledger.go`:550), `…\x00send`, so a retried send replays even though the
    transfer is no longer a draft and its revision has moved.
  - **Reserved stock is a warning, not a refusal** (R reading 5). The send dialog reads the
    from site's stock (I44) and warns when a line takes more than *available*, naming the
    reserved quantity. On hand never goes below zero (D I10).
- **I40 — Receive.** A request carries a **client request id** and names, per line, the
  quantity that arrived now — units or packagings as they are on the day — and a
  **destination location** at the to site. Each quantity is at most the line's outstanding
  (`transfer_over_received` beyond it); one movement of kind **`transfer_in`** at the to
  site, its lines with no *from* and a *to*; action lines `received`; the status
  `partly_received`, or `received` when nothing is outstanding. A receive of zero on every
  line with no close is `nothing_to_post`. Receiving a discontinued variant is allowed: it
  is not a goods receipt.
  - A transfer is received in parts, so the id is the client's (D I14): a key per document
    would replay the second part. The scanner retries; the same id and body replay — a
    close-only receive too, since the action, not a movement, holds the id.
- **I41 — Receive takes no If-Match.** Two people scanning one delivery at once must both
  land: the row lock serialises them, and each is judged against the other's result
  (§9.3). Every other write takes If-Match.
- **I42 — Closing the difference** (R reading 3). A receive may carry `close: {reason,
  note}`, `reason` one of `lost`, `damaged`, `not_packed`, `other`, `note` ≤ 500. After the
  quantities, every line's remaining outstanding is written as a `missing` action line,
  the reason and note are kept on the transfer, and the status is `received`. Missing
  stock is posted nowhere: it left the from site when sent. A close with nothing
  outstanding is `nothing_to_close`, unless it is a replay (step 2 comes first).
- **I43 — Send and receive** (R reading 2). `POST /transfers/{id}/send` may carry
  `receive: {lines: [{lineId, toLocationId}]}` naming a destination for **every** line
  (`receive_incomplete` otherwise): the send and a receive of everything sent, in one
  transaction — two movements at two sites, two actions, both ids derived (`…\x00send`,
  `…\x00receive`), the whole inside the deadlock retry. The status is `received`. A
  send-and-receive arriving after a plain send of the same transfer finds the derived send
  id with another hash and answers `transfer_not_draft` (step 2), never
  `client_request_reused` for an id the caller did not choose.
- **I44 — Cancel.** Only a `sent` transfer with nothing received or missing on any line
  may be cancelled (`transfer_not_cancellable`); a draft is deleted instead. A client
  request id and If-Match; one `transfer_in` movement **at the from site**, each line back
  to the location it left; action lines `returned` for everything sent; the status
  `cancelled`. The source locations cannot have been archived meanwhile (I48).
- **I45 — The ledger's source reference.** Both kinds carry, on the movement,
  `source_module = inventory`, `source_kind = transfer`, `source_id` = the transfer and
  `source_line_id` = the **action** (`mig/00108`:85-96 puts the reference on the movement
  header, not its lines). This changes a documented rule: the reference was *nil for the
  module's own postings* (`posting.go`:94-95), and is now nil for every posting **but** a
  transfer's. The movement page links back to the transfer; the transfer page reads where
  each part went from its action lines.
- **I46 — In transit is a quantity on the line, not a balance.** `inTransit = sent −
  received − missing − returned`, ≥ 0, held by a check constraint; it is 0 on every line of
  a received or cancelled transfer. It is read:
  - on the transfer, per line and in total;
  - as **incoming** per **to site** on the product's and the variant's stock (A) — a site
    with incoming and nothing on hand has a row;
  - on the **stock totals** (`GET /stock`) as `inTransit` beside `onHand`, which stays
    what lies on shelves; a variant wholly in transit keeps its row; the product list's
    *On hand* column is unchanged;
  - on the **variant page**, beside its movements: the closed differences, by
    `GET /transfers?variantId&withMissing=true` (`listInventoryTransfers`, §6).

  `contracts.Stock` is unchanged: Orders reserves and issues only what lies at a site.
- **I47 — The holder knows `in_transit`.** `contracts.VariantHoldKindInTransit =
  "in_transit"` joins `stock`, `reserved` and `open_documents`
  (`srv/contracts/references.go`:96-98). The holder answers it, after `stock` and
  `reserved`, when a variant lies nowhere and nothing is reserved but a line holds it in
  transit — for **both** purposes, delete and the change to Service — with `Quantity` the
  sum in transit and `Locations` the number of transfers. Products' `holdText` and
  `holdRemedy` (`srv/products/variants.go`:379-405, English as every Products refusal)
  gain the branch: *… is on its way between sites in a transfer; receive or cancel it
  first.* A send and a receive hold the variant `FOR SHARE` (`LockVariants`), as every
  posting does.
- **I48 — Archiving with a transfer open.** Today archiving is refused only while stock
  lies in the site (`srv/inventory/sites.go`:20-21). Now also:
  - a **site** that is the from or to site of a `sent` or `partly_received` transfer
    (`site_has_open_transfers`, 409); a draft does not stop it — its send is refused
    (`site_archived`);
  - a **location** that is the source of a line still in transit
    (`location_in_transit`, 409) — the cancel's way home.

  A site's **delete** is refused while any transfer names it (`site_has_transfers`).
- **I49 — Counts.** Movements are not blocked while counting (D I17). A send out of a
  counted location or a receive into one changes its balance, and the count's post flags
  the line moved (D I19, `srv/inventory/count_post.go`:28-30) as for any movement.
- **I50 — Disabling Inventory** with a transfer in transit freezes it: nothing can be
  received until the module is on again; the holder still answers (D §8). The reference's
  *Enabling and disabling* says so.
- **I51 — The følgeseddel** (R reading 14). `GET /transfers/{id}/note.pdf`, for a transfer
  sent or later: the number, from and to site with addresses, sent date and by whom,
  expected arrival, reference, the lines (SKU, name, quantity with its entered packagings,
  source location full code), the token `TRF-1042` as Code 128, and a line for the
  receiver's signature. Built as Orders' delivery printouts are (`srv/orders/deliverypdf.go`,
  maroto, the module's fonts), never stored.
- **I52 — The lookup knows a transfer.** `TRF-<number>` matches the transfer (`kind:
  transfer`) for a caller holding both views, so scanning the følgeseddel opens it, and
  the receive page's scan field takes it.

### Stock at sites (A)

- **I53 — One batch read of stock at sites.** `GET /stock/sites?variantIds&siteIds`
  (≤ 100 variants, ≤ 20 sites) answers, per variant and site: onHand, reserved, available,
  incoming, and the locations holding it with their quantities (and, in B, the placement
  and `primary`). The draft form's two columns read it before any draft is saved, and the
  send dialog's defaults — the primary location (B), else the location holding most —
  read it per line. The transfer's own read carries no stock.

### Placement (B)

- **I54 — A placement** is one row per variant × site: `primaryLocationId`, an active
  location of that site, or null; `reorderPoint`, a quantity (D I11) ≥ 0, or null;
  `maximum`, a quantity > 0, or null, never below the reorder point
  (`maximum_below_reorder_point`). A placement with all three null is deleted, never
  stored: a PUT with all three null is `placement_empty` (use DELETE). Kept apart from the
  balances (D §12.4). It snapshots the variant's **SKU and name** when written, for the
  low-stock list's order and search (I58); the list shows the catalog's current values.
- **I55 — The primary location is a suggestion, never a rule.** It is the default:
  - *to* location of a receipt line and of a received transfer line at that site;
  - *from* location of a transfer line sent from that site — else the location holding
    most of the variant;
  - first row of the variant's stock at that site, marked *primary*;
  - first location of Orders' pick list: `contracts.StockLocation` gains `Primary bool`,
    and Orders sorts it first (`deliveryLocations`, `srv/orders/deliveries.go`:222-237).

  Archiving the location clears it, with a `placement.cleared` event; deleting it sets it
  null by the foreign key and the same event.
- **I56 — The rule.** Per variant and **active** site:
  ```
  available = onHand − reserved                       (as today, srv/inventory/ledger.go:118-178)
  incoming  = Σ inTransit of the site's incoming transfer lines
  projected = available + incoming
  low       = reorderPoint is set and projected ≤ reorderPoint
  target    = maximum, or reorderPoint + 1 when there is no maximum
  needed    = target − projected                     (≥ 1 whenever low)
  ```
  Without a maximum the target is one above the reorder point, so filling it takes the row
  off the list; filling *to* the reorder point would leave it low.
- **I57 — Rounding up to whole packagings** (R reading 13). When the variant has
  packagings (D I24), the **suggested** quantity is `needed` rounded up to a whole number
  of its **smallest** packaging — fewest units, then `sort_order`, then id
  (`products.variant_packagings`, `mig/00109`); otherwise it is `needed`. The row carries
  both and the packaging, and **the screen says so** wherever a suggestion appears (the
  owner, 2026-11-08):
  - *21 needed, rounded up to 2 boxes of 15 = 30* / *21 trengs, rundet opp til 2 esker à 15
    = 30*, the packaging named by its **kind's** label (box, pallet, …), not its free name;
  - and, when the rounding passes the maximum, *… 5 above the maximum of 40*.

  A transfer line drafted from the list keeps a `suggestion {needed, packagingId, count}`
  and shows the same sentence until the line is edited, which clears it.
- **I58 — The low-stock list.** `GET /low-stock?siteId&q&cursor`: every low placement at
  active sites, ordered by site code then the placement's SKU snapshot, `q` matching the
  snapshot's SKU or name, paged by a cursor on `(site code, sku, variant id)`, the statement ordering by the
  joined site code — the index serves the per-site filter, not the order. The SKU and
  name snapshot is refreshed whenever a page's catalog read finds them changed, so a
  rename in Products is searchable from the next read after one shows it. One SQL
  statement computes it within the schema — the placements with a reorder point
  (`ix (site_id) where reorder_point is not null`), joined to the sums of balances over
  the site's locations, of reservations at the site, and of in-transit lines to the site,
  each grouped on the placement's `(variant_id, site_id)` — so its cost is the number of
  placements with a reorder point, not the catalog. Each row carries the variant (read
  through `ProductCatalog.Variants` for the page), onHand, reserved, available, incoming,
  reorderPoint, maximum, needed, suggested, the packaging, `elsewhere` — every other
  active site's available, from a second aggregate per variant over all active sites,
  restricted to the page's variants, since a site without a placement has none of the
  first's sums — and `openDrafts` — the numbers of draft
  transfers to the site holding the variant. Variants gone from the catalog are skipped;
  their placements are left (variant ids are never reused).
- **I59 — Create transfer from the list.** In the UI: rows at **one** site are selected and
  a from site chosen (default: the site with most available across the selection);
  `POST /transfers` makes the draft with the suggested quantities and their suggestions.
  A row with an open draft says *Draft 1043 open*, and *Create transfer* asks before making
  a second. Nothing is capped: the draft shows the source's stock beside each line (I53),
  and the send refuses what the location does not hold. Hidden without
  `transfers-manage`.
- **I60 — Export CSV.** `GET /low-stock.csv`, the list's filter: `site`, `sku`, `name`,
  `barcode`, `available`, `incoming`, `reorder_point`, `maximum`, `needed`, `suggested`,
  `packaging`, `packaging_count`. No supplier column: suppliers are free text on receipts
  until Products phase 5.
- **I61 — Setting placements.** On the variant's stock page per site, in a dialog:
  `PUT /variants/{variantId}/placements/{siteId}` with **`If-None-Match: *`** to create —
  a placement already there is `revision_conflict` — or **`If-Match`** to change; neither is
  400, as a bad If-Match is today (`srv/inventory/sites.go`:182-184). In bulk, by **the
  placements import**: `POST /imports/placements` with a CSV of `sku`, `site` (code),
  `primary_location` (full code), `reorder_point`, `maximum`; a dry run first, as the
  opening-balance import (D I32, `srv/inventory/import.go`); ≤ 5,000 rows, as that import
  (`import.go`:41); the SKUs resolved in one `LookupSKUs`. Each row sets all three fields,
  an empty cell clearing its field, and a row clearing all three deletes the placement.
  The commit writes last-wins, bumping each revision; it is idempotent by the file and
  its hash, as the opening-balance import is by file and site.
- **I62 — The dashboard** (R reading 10). The attention endpoint
  (`srv/inventory/stats.go`:55-74) moves its gate from the endpoint to each item, and
  gains:
  - `transferOverdue` (A) — a transfer past its expected arrival, or without one and sent
    or partly received more than 7 days ago; `entityId` the transfer, `occurredAt` its
    expected arrival or sent date; for `transfers-manage` with both views;
  - `lowStock` (B) — one per site, *N items below their reorder point at {site}*;
    `entityId` the site, `count` the items, `occurredAt` the time of the read (no
    placement records when it went low); for both views. The count is I58's statement,
    counted per site.

  **The item keeps the shape every module shares** (`id`, `type`, `title`, `count`,
  `entityId`, `occurredAt`, all but `count` required; `openapi/inventory.yaml`:1320-1344):
  the enum gains the two types and nothing else, and each `title` is composed as
  `countOpenLong`'s is. `countOpenLong` keeps its own gate. Still at most 20 items. The host
  renders the new types as it does the others (`apps/host/frontend/src/routes/dashboard.tsx`,
  `lib/dashboard-helpers.ts`, `catalogs/dashboard.ts`).

### Shared

- **I63 — Permissions.** One new key, **`inventory:transfers-manage`** (*Transfer stock*
  / *Overføre varer*): create, edit, delete, send, receive, close, cancel, with both views
  (R reading 6). Seeing transfers and the følgeseddel needs both views alone, so the
  sidebar's *Transfers* needs both views; *New transfer*, *Send*, *Receive*, *Cancel* and
  the low-stock list's *Create transfer* are hidden without the key. Placements and their
  import need **`stock-adjust`** with both views (R reading 7). Seven keys.
- **I64 — Events.** `inventory.events` (D I31) gains the entity kinds `transfer`
  (`created`, `updated`, `deleted`, `sent`, `received`, `closed`, `cancelled`) and
  `placement` (`set`, `cleared`). No outbox events (R reading 11).
- **I65 — Movement kinds.** `transfer_out` and `transfer_in` are application constants
  (`mig/00108`:68-70); but `InventoryMovementKind` is a closed enum in the contract
  (`srv/inventory/gen/api.gen.go`:205-209), so the contract, the movements filter and the
  kind labels change with them. The movements table itself does not change; the new
  tables are §3's.

## 3. Data model (schema `inventory`)

One migration per delivery, numbered at the cut (main's latest is `00139`).

### 3.1 `inventory.counters` (A)

`counter_name text primary key`, `next_value bigint`. The Orders shape
(`mig/00119_orders_baseline.sql`:42-45); allocated by upsert in the creating transaction.

### 3.2 `inventory.transfers` (A)

`id bigserial`, `number bigint unique`, `from_site_id`, `to_site_id` (both references
`sites`, `check (from_site_id <> to_site_id)`), `status text` (check the five), `reference`
null, `note` null, `expected_arrival date` null, `close_reason text` null (check the
four), `close_note` null, `revision integer`, `created_by_user_id uuid`,
`created_by_display`, `created_at`, `sent_at` null, `sent_by_user_id` null,
`sent_by_display` null, `closed_at` null, `closed_by_user_id` null, `closed_by_display`
null, `updated_at`. Indexes: `(status, created_at desc)`, `(from_site_id, status)`,
`(to_site_id, status)`.

### 3.3 `inventory.transfer_lines` (A)

`id bigserial`, `transfer_id` (on delete cascade — only a draft is deleted), `line_no`,
`variant_id integer`, `sku`, `product_name` (snapshots, I36), `unit` null (set at send),
`quantity numeric(14,3)` (> 0), `entered jsonb` null, `suggestion jsonb` null (B, I57),
`from_location_id` null (set at send), and the running totals `sent`, `received`,
`missing`, `returned`, each `numeric(14,3)` default 0, with `check (received + missing +
returned <= sent)`. Indexes: `(transfer_id, line_no)` unique; `(variant_id) where sent >
received + missing + returned`; `(from_location_id) where sent > received + missing +
returned`; `(sku)`.

### 3.4 `inventory.transfer_actions` and `transfer_action_lines` (A)

`transfer_actions`: `id bigserial`, `transfer_id`, `kind` (`send`, `receive`, `cancel`),
`client_request_id uuid unique`, `request_hash bytea`, `movement_id` null (a close-only
receive posts none), `close_reason` null, `close_note` null, `posted_by_user_id`,
`posted_by_display`, `posted_at`. Index `(transfer_id, posted_at)`.

`transfer_action_lines`: `action_id`, `transfer_line_id`, `kind` (`sent`, `received`,
`missing`, `returned`), `quantity numeric(14,3)` (> 0), `location_id` null — the source
for `sent` and `returned`, the destination for `received`. Primary key `(action_id,
transfer_line_id, kind)`; index `(transfer_line_id)`.

### 3.5 `inventory.placements` (B)

`variant_id integer`, `site_id bigint references sites on delete cascade` (a site is
deleted only when it never held stock), primary key `(variant_id, site_id)`;
`primary_location_id bigint` null `references locations on delete set null`;
`reorder_point numeric(14,3)` null; `maximum numeric(14,3)` null; `sku`, `product_name`
(snapshots, I54); `check (reorder_point is null or reorder_point >= 0)`, `check (maximum
is null or maximum > 0)`, `check (maximum is null or reorder_point is null or maximum >=
reorder_point)`, `check (coalesce(primary_location_id, reorder_point, maximum) is not
null)` — the last as `num_nonnulls(…) > 0`; `revision`, `updated_by_user_id`,
`updated_by_display`, `updated_at`. Indexes `(site_id, sku, variant_id) where
reorder_point is not null`; `(primary_location_id)`.

## 4. Rules and refusals

The house shapes (D §4 as built): a line's code is a line failure in a 400 problem, or
409 when any is `insufficient_stock`; a stale If-Match is 409 `revision_conflict`.

| Code | Status | When |
| --- | --- | --- |
| `transfer_same_site` | 400 | from = to (I36) |
| `site_archived` | 400 | a write names an archived site (I36, I48) |
| `transfer_not_draft` | 409 | edit, delete or send of a transfer not a draft (I37, I39, I43) |
| `transfer_not_receivable` | 409 | receive of a draft, received or cancelled transfer |
| `transfer_not_cancellable` | 409 | I44 |
| `transfer_over_received` | 400 (line) | more than outstanding (I40) |
| `from_location_required` | 400 (line) | a send line without a source location (I39) |
| `transfer_line_unknown` | 400 (line) | a request names a line not on the transfer |
| `nothing_to_close` | 409 | I42 |
| `receive_incomplete` | 400 | a send-and-receive missing a line's destination (I43) |
| `site_has_open_transfers`, `location_in_transit`, `site_has_transfers` | 409 | I48 |
| `maximum_below_reorder_point`, `placement_empty` | 400 | I54 |
| `placement_location_invalid` | 400 | primary location archived, or in another site (I54) |
| the phase 1 codes | as built | `insufficient_stock`, `location_archived`, `variant_not_stockable`, `quantity_invalid`, `too_many_lines`, `client_request_reused`, `revision_conflict`, … |

`move_crosses_sites` stays: a transfer's movements each stay in one site (R §5).

## 5. Permissions

| Key | Display (en / nb) | Gives |
| --- | --- | --- |
| `inventory:transfers-manage` (new) | Transfer stock / Overføre varer | Create, edit, delete, send, receive, close, cancel transfers |
| `inventory:stock-adjust` | Adjust stock / Justere beholdning | … and placements, and their import (B) |

Every transfer, stock and placement endpoint also requires `locations-view` and
`stock-view` (D §5). The new key is delegable and not sensitive. `GET /meta` gains
`canTransfer`.

## 6. API (`/api/v1/inventory`, contract `openapi/inventory.yaml`)

Every operation has a `summary`; the Assistant's modes are derived (reads auto, writes
confirm, files never).

| Operation | Op id | Access | D |
| --- | --- | --- | --- |
| `GET /transfers?status&fromSiteId&toSiteId&variantId&withMissing&q&cursor` → `{transfers: [InventoryTransferSummary], next}`; `status=open` = draft, sent, partly received; `q` a number or a line's SKU | `listInventoryTransfers` | locations-view+stock-view | A |
| `POST /transfers` `{fromSiteId, toSiteId, reference, note, expectedArrival, lines: [{variantId, quantity \| entered, suggestion?}]}` → 201 | `createInventoryTransfer` | +transfers-manage | A |
| `GET /transfers/{id}` → `InventoryTransfer`: lines with `sent, received, missing, returned, inTransit, suggestion`; actions with their lines (who, when, where each part went) | `getInventoryTransfer` | locations-view+stock-view | A |
| `PUT /transfers/{id}` (If-Match) — a draft's header and lines | `putInventoryTransfer` | +transfers-manage | A |
| `DELETE /transfers/{id}` (If-Match) — a draft | `deleteInventoryTransfer` | +transfers-manage | A |
| `POST /transfers/{id}/send` (If-Match) `{lines: [{lineId, fromLocationId}], receive?: {lines: [{lineId, toLocationId}]}}` | `sendInventoryTransfer` | +transfers-manage | A |
| `POST /transfers/{id}/receive` `{clientRequestId, lines: [{lineId, quantity \| entered, toLocationId}], close?: {reason, note}}` | `receiveInventoryTransfer` | +transfers-manage | A |
| `POST /transfers/{id}/cancel` (If-Match) `{clientRequestId}` | `cancelInventoryTransfer` | +transfers-manage | A |
| `GET /transfers/{id}/note.pdf` | `getInventoryTransferNote` | locations-view+stock-view | A |
| `GET /stock/sites?variantIds&siteIds` → `{stock: [{variantId, siteId, onHand, reserved, available, incoming, locations: [{location, onHand, primary}], placement}]}` (I53) | `listInventoryStockAtSites` | locations-view+stock-view | A (placement, primary: B) |
| `PUT /variants/{variantId}/placements/{siteId}` (If-None-Match: * \| If-Match) `{primaryLocationId, reorderPoint, maximum}` | `putInventoryPlacement` | +stock-adjust | B |
| `DELETE /variants/{variantId}/placements/{siteId}` (If-Match) | `deleteInventoryPlacement` | +stock-adjust | B |
| `GET /low-stock?siteId&q&cursor` → `{rows: [InventoryLowStockRow], next}` | `listInventoryLowStock` | locations-view+stock-view | B |
| `GET /low-stock.csv?siteId&q` | `exportInventoryLowStock` | locations-view+stock-view | B |
| `POST /imports/placements` (multipart, `dryRun`) | `importInventoryPlacements` | +stock-adjust | B |

Changed reads:

- `InventorySiteStock` (product and variant stock) gains `incoming`, and a site with
  incoming and nothing on hand gets a row (A); `placement {primaryLocationId,
  reorderPoint, maximum, revision, low}` and rows' `primary` (B).
- `GET /stock` totals gain `inTransit`, and keep a variant wholly in transit (A).
- `InventorySite` gains `incomingTransfers` and `outgoingTransfers`, the counts of open
  ones (A).
- `GET /variants?q&limit` gains `siteId`, and `InventoryVariantRef` gains
  `primaryLocationId` at that site (B) — the line editor's default.
- `InventoryMovementKind` gains `transfer_out` and `transfer_in`; `InventoryMovement`
  gains `transfer {id, number}` (A).
- `InventoryLookupMatch` gains `kind: transfer` (A).
- The attention item types gain `transferOverdue` (A) and `lowStock` (B), in the shared
  shape (I62).

## 7. Frontend (`@vantigo/inventory-ui`, R §6–§7 sketch it)

**Routes** (`ui/lib/routes.ts`, mirrored in `apps/host/frontend/src/routes/inventory/`):

| Path | Page | D |
| --- | --- | --- |
| `/inventory/transfers` | the list, filtered by status (default *open*), from and to site; search by number or SKU | A |
| `/inventory/transfers/new?fromSiteId&toSiteId` | the draft form: header, the shared `LineEditor` with two read-only columns from I53 (*at {from}*: available; *at {to}*: available · incoming · reorder point), a warning per short line, a line's rounding sentence (B) | A |
| `/inventory/transfers/$transferId` | the transfer: lines with sent, received, missing, in transit; its actions; *Edit*, *Send…*, *Send and receive…*, *Receive*, *Cancel transfer*, *Print følgeseddel*, *Delete draft* by status and permission | A |
| `/inventory/transfers/$transferId/receive` | the receive page: a scan field (+1 per scan of a SKU or barcode; `TRF-…` opens the transfer), *Accept all*, per line *Now* and *To location*, *Receive*, *Receive and close the difference…* | A |
| `/inventory/low-stock?siteId` | the list, the selection, *Create transfer to {site} from [site]*, *Export CSV*; each suggestion with its rounding sentence; *Draft N open* | B |

**Dialogs:** *Send* (a source location per line, defaulted from I53; the reserved warning;
*Print the følgeseddel*); *Close the difference* (one reason, an optional note);
*Placement at {site}* on the variant page (primary location, reorder point, maximum, each
with its one-line help; *Remove*).

**Elsewhere:**
- the sidebar gains **Transfers** (after Overview) and **Low stock** (after Counts), both
  for both views (`ui/lib/navigation.ts`);
- a site's page shows **Incoming** and **Outgoing** open transfers, linking to the list;
- the movements list and page name the kinds *Transfer out* / *Transfer in* and link to
  the transfer;
- the variant page's site rows show *+N incoming*, the placement, *low* and *primary*, and
  below its movements the transfer differences (I46);
- the Stock card on the product page shows *In transit N* in its header and *+N incoming*
  and *reorder ≤ N* per site (`ui/components/product-stock.tsx`);
- the receipt form defaults each line's location to the primary location (B);
- the import page gains a second kind, *Placements* (B);
- the dashboard renders the two new items (`apps/host/frontend/src/routes/dashboard.tsx`,
  `lib/dashboard-helpers.ts`, `catalogs/dashboard.ts`).

**Words** (R §8): *Overføring(er)*, *Fra lagersted / Til lagersted*, *Utkast / Sendt /
Delvis mottatt / Mottatt / Avbrutt*, *Under transport*, *Innkommende*, *Send / Motta /
Send og motta*, *Avslutt med avvik*, *Ikke mottatt*, *Følgeseddel*, *Forventet ankomst*,
*Hovedlokasjon*, *Bestillingspunkt*, *Maksbeholdning*, *Lav beholdning*, *Foreslått*,
*Påfylling* for the dialog of levels.

**Assistant tour targets** (`ui/assist-targets.json`): the transfers list, *New
transfer*, the receive page's scan field, the low-stock list.

## 8. Platform, contracts and documentation

- **Contracts:** `contracts.VariantHoldKindInTransit` and Products' `holdText`/`holdRemedy`
  branch (A); `contracts.StockLocation.Primary` and Orders' pick-list order (B).
- **Module registries:** none new — no module is added. The host mirrors the new routes,
  the sidebar labels and the dashboard items.
- **ROADMAP.md:** transfers are Inventory's and done (A); placement done (B); what stays
  Later.
- **Documentation**, in each delivery's commit series:
  - `reference/inventory.md` — the schema, permissions, *within one site* (how a transfer
    keeps it), the kinds, transfers and their actions, placement and the rule, reads,
    locks (I38), the refusals, the dashboard, *enabling and disabling*, *what comes next*,
    endpoints; its `sources` frontmatter gains the new migrations;
  - `user/inventory.md`, **en and nb** — the sidebar; *Transfer stock between sites*;
    *Receive a transfer*; *Close a difference*; *Cancel a transfer*; *Set an item's
    primary location and reorder point*; *The low-stock list*; *Import placements*; the
    Stock card; the dashboard; *Who sees what*;
  - `contributing/module-boundaries.md` — `contracts/references.go` (A) and
    `contracts/stock.go` (B) change;
  - `reference/products.md`, and `user/products.md` in en and nb — the new refusal (A);
  - `reference/orders.md`, and `user/orders.md` in en and nb — the pick list's primary
    first (B);
  - `reference/assistant.md` — the tour targets;
  - `go generate` in `apps/server` for the Assistant's embedded copy.

## 9. Edge cases

1. **A send racing an edit of the draft:** both take the transfer `FOR UPDATE`; the second
   is `revision_conflict`, or `transfer_not_draft` — or, for the send's own retry, its
   replay (I38 step 2 before step 3).
2. **A receive racing a cancel:** the row lock orders them; a cancel after any receipt is
   `transfer_not_cancellable`, a receive after a cancel `transfer_not_receivable`.
3. **Two receivers at once** (I41): serial, each against the other's result; one may be
   refused `transfer_over_received` for a line the other finished. The receive page
   reloads the transfer after a refusal and keeps the scanner's counts.
4. **A retried receive after its own success:** step 2 answers the replay before the
   outstanding is judged, so it is never `transfer_over_received`.
5. **A packaging changed in transit:** the line's units were snapshotted at send; the
   receiver's packagings resolve as they are that day.
6. **The variant deleted in a draft:** the draft line shows *no longer in the catalog*;
   the send is refused `variant_not_found` for it.
7. **The destination site archived with a draft to it:** allowed; the draft's send is
   `site_archived`. An archive racing a send waits on the sites' `FOR SHARE` (I38 step 4)
   and is then refused `site_has_open_transfers`.
8. **Two opposite send-and-receives** (A→B and B→A at once) may deadlock on balances; the
   retry reruns the loser (I43).
9. **A transfer to a site that does not fulfil orders:** allowed; the flag is about Orders.
10. **Receiving into the to site's root:** allowed, as any line may land on the root (D I3).
11. **A placement's primary location re-parented within the site:** kept.
12. **A low row whose suggestion rounds past the maximum:** shown, and said (I57).
13. **A low row with the reorder point at 0:** low only when projected ≤ 0 — *out of stock
    and nothing coming*; the target is 1.
14. **A draft made from the list does not take the row off it:** a draft holds nothing
    (I37); the row says *Draft N open* until it is sent (I59).
15. **Deleting a variant with placements:** not held — a placement is not stock. Its rows
    are skipped by every read and left (I58).

## 10. Testing

- **Ledger:** send, receive in parts, close, close-only, cancel, send-and-receive — the
  balances at both sites, `inTransit` (0 after cancel and close), the movements' kinds and
  source references, the action lines; replays of each, including a send replayed after
  its revision moved and a close-only receive replayed; a send-and-receive after a plain
  send; the receive's second part not a replay; over-receipt.
- **Concurrency:** receive vs cancel, two receives, send vs edit, send vs site archive,
  opposite send-and-receives, on a pool of four.
- **Holder:** `in_transit` for delete and type change, and none after cancel; the Products
  integration test.
- **Archiving:** the three refusals of I48.
- **Reads:** product and variant stock with `incoming` (an empty destination site has its
  row); totals with `inTransit` (a variant wholly in transit kept); I53's batch; the
  lookup's `TRF-` match; the transfer list's `variantId`/`withMissing`.
- **Placement:** the rule's table (low at `=`, the target without a maximum, rounding with
  and without packagings and its tie-break, past the maximum, reorder point 0, archived
  sites skipped, a cancelled transfer not incoming); the list's paging and search; the
  If-None-Match create and its race; the import's dry run, commit, clearing and replay;
  the pick-list order in Orders.
- **Frontend:** the draft's columns and warnings, the send dialog's defaults and reserved
  warning, the receive page's scan, *Accept all* and reload after a refusal, the close
  dialog, the low-stock selection, *Draft N open* and *Create transfer*, the rounding
  sentence in en and nb.
- **The integration test:** the new file operations (`note.pdf`, `low-stock.csv`, the
  placements import) in `filesOrRedirects`.

## 11. Later

| Later | Waits for |
| --- | --- |
| Purchase orders from the low-stock list; *incoming* from open purchase orders; automatic ordering, snooze, dismiss | Purchasing |
| A daily low-stock mail | Demand |
| `inventory.transfer.*` domain events | A consumer |
| Transfer lines from a spreadsheet | Demand |
| A shelf-level minimum (restock the shop floor from the back room) | Demand |
| Per-site permissions (only the destination receives) | The platform |
| Lots and serials on a transfer | Manufacturing (D §12) |

## 12. Revision 2 — the critic review, 2026-11-08

- **Cancel left stock in transit forever:** lines gain `returned`; `inTransit` subtracts
  it; the check and the partial indexes include it (I44, I46, §3.3).
- **`source_line_id` is a movement header column, not a line's:** the actions table
  records each send, receive and cancel and its lines with their locations; the movement's
  `source_line_id` names the action (I38, I45, §3.4).
- **The replay path:** one order for every action — row lock, replay check, If-Match and
  status, sites, posting with the outstanding judged after units are resolved (I38); a
  close-only receive replays by its action; a send-and-receive after a send is
  `transfer_not_draft` (I43).
- **The low-stock list's order and search:** SKU and name snapshotted on the placement;
  one statement within the schema; the cost stated (I54, I58); the dashboard's count is
  the same statement (I62).
- **Invented names:** no `InventoryVariantHistory` — the differences come from the
  transfer list read; `searchInventoryVariants` gains `siteId` and the ref
  `primaryLocationId` explicitly (I46, §6).
- **Data for the draft form and the send dialog:** a batch read of stock at sites (I53);
  totals keep a variant wholly in transit, and site stock a site with only incoming (I46).
- **The rounding sentence after a reload:** the line keeps a `suggestion`; the smallest
  packaging's tie-break; the packaging named by its kind (I57).
- **Rows staying low:** the target without a maximum is one above the reorder point; a row
  names its open drafts and *Create transfer* asks before a second (I56, I59).
- **Smaller:** the lock-order comment amended (I38); send-and-receive inside the deadlock
  retry and sites `FOR SHARE` at send (I38, I43); the placements' foreign keys (§3.5);
  the import capped at 5,000 and idempotent by file (I61); PUT with `If-None-Match: *` to
  create and `placement_empty` (I54, I61); the attention items' fields and the host helper
  (I62); Products' refusals are English, with `holdText`/`holdRemedy` named (I47); the
  sidebar's condition and the hidden write actions (I63); `from_location_required` (I39);
  SKU snapshotted on draft lines (I36); the docs list gains `module-boundaries.md` and the
  reference's `sources` (§8).
- **The re-review:** the attention item keeps the shared shape — no `params`, `occurredAt`
  always set (I62); `site_archived` judged after the sites' lock (I38 step 4); the
  transfer handlers' own retry; one key per action and movement (I38); `elsewhere` from
  its own aggregate, the low-stock order and the snapshot's refresh (I58).
