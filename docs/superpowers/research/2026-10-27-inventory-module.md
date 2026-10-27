# Inventory module — research

A new module, **Inventory** (module key `inventory`, permissions `inventory:*`, package
`@vantigo/inventory-ui`, Norwegian **Lager**), for keeping stock of the catalog's goods in
warehouses and in stores: a tenant builds its sites, divides each into the places an item
can sit — zones, aisles, racks, shelves, bins, a sales floor and a back room — and records
how many of each variant lie where. The roadmap's earlier name for it, *Warehouse*
(ROADMAP.md "Cross-module domain events", Products phase 5, the Products reference), is
replaced: a shelf in a shop is not a warehouse. This note settles what the first version
is, from the products a Norwegian small business would otherwise buy and from the codebase
as it stands. A design spec starts from §8 and does not reopen it. Code references are
against `main` at `98617107`; `srv/` abbreviates `apps/server/internal/`, `mig/`
`srv/db/migrations/`, and "the Products reference"
`docs/src/content/docs/en/reference/products.md`.

## 1. What is decided, and what this note adds

### 1.1 Decisions on the record

- **2026-10-27, the owner:**
  - The module is **Inventory / Lager**, `inventory` in every identifier. It is a module
    of its own, not a part of Products.
  - It serves **both traditional warehouses and store shelves** with one model.
  - The first version must:
    - show the **total stock on the product page** (Products),
    - list **every location where the item lies**,
    - let a user **look up a location and see what is in it**.
  - Stock enters and leaves through four records, all in the first version:
    - **manual adjustments**,
    - **moves between locations**,
    - **stock counts** (varetelling),
    - **goods receipts**.
  - The first tenant is a **generic small business**: plain quantities, with **lots,
    serials and expiry left for later**.
  - A site is divided as a **free tree of typed locations**, not fixed levels.
  - **Orders comes later.** Nothing integrates with it now, but the design must leave
    the seam ready.
- **From the roadmap:**
  - Products phase 5 (units of measure, supplier records per variant) is "designed
    together with the Warehouse service".
  - The domain-events outbox is "deferred until Orders", with "Warehouse reserves stock"
    as the named example (ROADMAP.md, "Cross-module domain events").
  - Point of sale is a candidate first consumer of Orders, and so a later consumer of
    this module.

### 1.2 What this note settles

1. How the market models a place, a quantity and a movement, and which of it a small
   first version cannot lack (§2, §3).
2. One location model that serves a pallet rack and a shop shelf (§4).
3. Where the quantity lives and what is the truth (§5).
4. What the Orders seam needs from day one so it is not a migration later (§6).
5. What the codebase gives and lacks: the Products contract, the variant that can be
   hard-deleted, composition into the product page, labels (§7).
6. The must-have list, three deliveries and the readings for the owner (§8, §9).

## 2. What the products do

Read 2026-10-26/27 from public documentation and help centres, and for Odoo and ERPNext
from their source. Where a vendor page refused the fetch, the claim rests on the search
engine's summary of that page and says so. "Not stated" means the pages did not say.

### 2.1 Odoo Inventory — a free tree where every change is a move

Sources:
- <https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/inventory/warehouses_storage/inventory_management/use_locations.html>
- <https://raw.githubusercontent.com/odoo/odoo/18.0/addons/stock/models/stock_location.py>
- `stock_quant.py` and `stock_move.py` in the same directory

**Locations.**
- Locations form a **free tree of any depth**, and a warehouse is the subtree under a
  view location. A full name looks like `WH/Stock/Zone A/Refrigerator 1`.
- Each location has a `usage`:
  - **internal**, which counts as stock;
  - **view**, which groups other locations and "should not contain products";
  - **transit**;
  - **supplier** and **customer**;
  - **inventory**, the "Inventory Loss" counterpart of adjustments;
  - **production**.
- Every stock change, an adjustment included, is a move from one location to another,
  and the virtual locations stand in for the outside world.
- Each location can carry:
  - an optional **barcode**, unique per company;
  - informational corridor, shelf and height positions;
  - a cycle-count interval;
  - a **storage category**, which sets weight and quantity capacity and whether the
    location accepts any product, only the product already there, or only goods when it
    is empty.

**Quantities.**
- The live balance is `stock.quant`: one row per **product × location × lot × package ×
  owner**, holding `quantity`, `reserved_quantity` and the derived available quantity.
- Done move lines are the history.
- A count is recorded on the quant itself, as a counted quantity and a difference. An
  "outdated" flag says stock moved after the count, and applying the count posts moves
  to or from Inventory Loss.

**Orders.** A sales order lowers the *forecast* when it is confirmed. *Reservation* is a
separate step, which allocates quants at confirmation, by hand or N days before the
scheduled date, depending on the operation type. Validating the delivery lowers on hand.

### 2.2 Microsoft Dynamics 365 Business Central — fixed levels, a ladder of process

Sources:
- <https://learn.microsoft.com/en-us/dynamics365/business-central/design-details-warehouse-management>
- <https://learn.microsoft.com/en-us/dynamics365/business-central/design-details-availability-in-the-warehouse>
- <https://learn.microsoft.com/en-us/dynamics365/business-central/warehouse-how-to-set-up-bin-contents>

**Locations.**
- The shape is fixed: **Location → (Zone, only in the advanced setup) → Bin**.
- Bin codes are up to 20 characters. A worksheet generates them from rack, section and
  level ranges with a separator, for example `A01-01-03`.
- A bin that has been used cannot be deleted until it is empty.

**One location at a time opts into more process**, rung by rung:
1. Post straight from the order.
2. Bins, at first optional, then mandatory.
3. Receipts and shipments.
4. Picks and put-aways.
5. *Directed put-away and pick*, which adds bin types, ranking and capacity.

**Bin content.** A bin-content row for an item can be **Fixed** (it survives at zero) or
the **Default** bin, one per item per location. Both are *preferences*: other items may
still go there.

**Quantities.**
- Two ledgers keep the quantities. Item ledger entries hold location, cost and
  reservation. Warehouse entries hold bin, and bin content is a sum over them.
- Availability is computed from reservations and picks, never stored.
- A count posts its difference to an **adjustment bin** that inventory totals ignore.

### 2.3 ERPNext — the warehouse tree is the bin tree

Sources:
- <https://docs.frappe.io/erpnext/user/manual/en/warehouse>
- <https://raw.githubusercontent.com/frappe/erpnext/develop/erpnext/stock/doctype/stock_ledger_entry/stock_ledger_entry.py>
- `bin.json` and `bin.py` in the same tree
- <https://docs.frappe.io/erpnext/user/manual/en/stock-reconciliation>

**Locations.** Warehouse is the only location concept: a free tree whose *group*
warehouses ("Warehouse > Room > Row > Shelf > Bin") hold no stock. Transactions against a
group are blocked.

**Quantities.**
- The **stock ledger entry** is the append-only truth. It holds the delta, the running
  `qty_after_transaction`, the value, and `voucher_type` / `voucher_no` /
  `voucher_detail_no` pointing at the document that caused it.
- A ledger entry is never cancelled on its own. The document is.
- The doctype called `Bin` is not a physical bin. It is a cached balance per item ×
  warehouse, holding actual, ordered, requested, reserved and projected quantities.
- A **stock reconciliation** sets the absolute quantity per item × warehouse. It doubles
  as opening stock and accepts CSV and barcode scanning.
- Since v15, a **Stock Reservation Entry** hard-reserves a warehouse and quantity against
  a sales order line.

### 2.4 NetSuite — a lesson about bins

Sources:
- <https://docs.oracle.com/en/cloud/saas/netsuite/ns-online-help/section_N2270284.html>
- <https://docs.oracle.com/en/cloud/saas/netsuite/ns-online-help/section_N2271791.html>
- <https://docs.oracle.com/en/cloud/saas/netsuite/ns-online-help/section_N263263.html>

- Locations nest ("Sublocation of"). Bins sit one level below a location and carry a
  type, a pick sequence and replenishment minimum and maximum.
- *Basic* bin management required every item to be associated with a bin before it could
  be stored there. *Advanced* bin management relaxed this to "any bin for any item", and
  kept the association only as a filter and a **preferred bin** per location.
- A location flag, *Make Inventory Available*, decides whether its stock can be sold.

### 2.5 The Nordic field — what a small business compares against

Sources:
- Tripletex, through search summaries of hjelp.tripletex.no (the help centre refused the
  fetch), and <https://rackbeat.com/en/product_features/locations/>
- PowerOffice: <https://hjelpesenter.poweroffice.no/opprett-produkter-manuelt>
- Visma Net: <https://docs.vismasoftware.no/visma-net-erp/help/inventory/manage-warehouses/about-warehouses-and-warehouse-locations/>
- Fortnox: <https://support.fortnox.se/produkthjalp/lager/lagerlista-och-lagerstatus>

**Tripletex, Logistikk Basis.**
- Supports several warehouses and a *lagerlokasjon* linked to a product.
- Counts are made per warehouse, by hand or with a barcode reader.
- Stock is placed on a location *by counting it there*.
- A purchase order never moves stock. Only a *varemottak* (goods receipt) or a
  *lagerkorreksjon* (correction) does.
- **Logistikk Komplett** is Rackbeat, a separate product. It allows one location level on
  its basic plan and three on premium, and refuses to delete a location that has
  products.

**PowerOffice Go.**
- Tracks stock **per item only**, with *Tilgjengelig* = on hand − confirmed orders.
- For anything more, it points to Rackbeat or a WMS integration.

**Visma.**
- eAccounting tracks stock per item, using FIFO.
- Visma Net ERP has warehouse → location as a *flat* level, with a segmented code
  (aisle-rack-bin) instead of a tree. Each location can:
  - be included in, or left out of, the available quantity;
  - allow or refuse each kind of operation;
  - carry a pick priority.

**Fortnox Lager.**
- Two levels: *lagerställe* → *lagerplats*.
- The stock list shows, per article: *I lager*, *Väntas*, *Reserverat*, *Disponibelt*
  and *Orderbrist*.
- An arrow expands **article → warehouse → location**, which is the per-location
  breakdown the owner asks for.
- Orders reserve stock automatically, up to what is available.
- A count can be scoped to a warehouse, a location, a supplier's articles or a sample.

**International SMB tools.**
- **Zoho Inventory** (warehouse → zone → bins with *levels*, from the Premium plan up)
  generates bin codes with a delimiter and an alias, and locks a bin once it has been
  used.
- **inFlow** has location → a free-text sublocation, with one starred default per
  location.
- **Sortly** treats nested folders as locations. **Scanning a folder's QR label opens
  that folder's contents**, the strongest location-contents screen found.
- **Katana** shows in stock, committed and expected. Its stocktake runs *draft → work in
  progress (expected quantities frozen) → counted → completed*, and completing it posts
  one adjustment. A multi-bin add-on shipped in September 2026.
- **Cin7 Core** shows a bin as `Location:Bin` in a single picker.

**Pattern: locations are what the higher plans sell.**
- PowerOffice and Visma eAccounting have none.
- Zoho puts bins on Premium.
- Katana puts multiple locations on Standard and up, and multi-bin in an add-on.
- inFlow puts multiple locations on Pro.
- Rackbeat puts deeper trees on Premium.

### 2.6 Retail — stores and shelves

Sources:
- Shopify: <https://help.shopify.com/en/manual/products/inventory/managing-inventory-quantities/inventory-states> and <https://help.shopify.com/en/manual/products/inventory/setup/bin-locations>
- Square: <https://developer.squareup.com/docs/inventory-api/what-it-does>
- Lightspeed X-Series: search summary of the product-locations article (the page refused
  the fetch)
- LS Central: <https://help.lscentral.lsretail.com/Content/Fields/T_99001533.htm>
- Oracle Retail SIM: <https://docs.oracle.com/cd/E12454_01/sim/pdf/160/html/store_user_guide/sequencing.htm>

**A store is a location.** In every product, a store is a stock-holding location like a
warehouse, distinguished by flags (sells at the till, fulfils online orders), never by a
separate model.

**How shelves are modelled.**
- **As a label.** Shopify's bin is a text label, limited to one per variant per location,
  with no quantity. Lightspeed allows several aisle/bay/shelf/bin labels per product per
  outlet, also without quantity. Square has no bins at all, and its users file the
  shelf in the description.
- **As a layout separate from the quantity.** LS Central has store section → section
  shelf → *item section location*: an item may sit on several shelves, one of which is
  flagged for statistics, while quantity stays per store. Oracle SIM splits a store into
  **shop floor and back room**: an item maps to several shelf locations, one of them
  primary, and a capacity per item and location drives a *shelf replenishment pick list*
  from the back room.
- **As real places holding quantity.** Odoo.

**States of a quantity.**
- Shopify has the cleanest vocabulary: **on hand = available + committed + reserved +
  damaged + safety stock + quality control**, with **incoming** outside on hand.
- *Committed* is written only by the order system. A draft order *reserves*; it does not
  commit.
- Square records a move as a change from one state to another (`IN_STOCK`,
  `RESERVED_FOR_SALE`, `SOLD`, `WASTE`, `IN_TRANSIT`). Since 2026-07 a transfer is simply
  a change whose from and to locations differ.

**The seam.** In every product, placing an order commits stock without changing on hand,
and fulfilling it lowers on hand and releases the commitment. A till sale does both at
once.

### 2.7 What every one of them has

- **Sites:** several warehouses or stores, each with an address, distinguished from one
  another by what they do, not by a separate model.
- **Places inside a site** with a code that is **unique within the site** and printable
  as a label: bin codes such as `A-01-03`, generated in bulk by Zoho and Business
  Central.
- **On hand per item per place**, kept as a balance and fed by a record of movements
  that is never rewritten. Odoo keeps quants and move lines, ERPNext stock ledger entries
  and a cached `Bin`, and Business Central entries summed into bin content.
- **Adjustment with a reason.** **Transfer between places.** **A count** that compares a
  counted quantity with the expected one and posts the difference.
- **On the item:** a total, broken down by site and then by place (Fortnox's drill-down,
  Zoho's *Stock Locations*, inFlow's quantity tile, Katana's bin icon).
- **On hand, reserved and available** as the minimum vocabulary, with *available = on
  hand − reserved*.

### 2.8 What one or two have

- **A location-contents screen opened by scanning the location's label:** Sortly. Every
  other product offers a filtered stock list or a report.
- A **preferred or default place per item per site**: Business Central's default bin,
  NetSuite's preferred bin, inFlow's starred sublocation, Oracle's primary location.
- **One-product or empty-only places, and capacity:** Odoo's storage categories and
  Business Central's directed mode. Both sit in the advanced tiers.
- **Shop floor and back room with shelf replenishment:** Oracle SIM, and Odoo through
  reordering rules.
- **Opting into more process per site:** Business Central's ladder.

### 2.9 What nobody does well for a small business

- **Serving the shop and the warehouse with one model.** The retail products keep the
  shelf as a label without quantity. The ERPs keep it as a bin, which a shop finds heavy
  (a till cannot say which shelf a sale came from).
- **The location-contents lookup** is a report almost everywhere.
- **Removing a product while it still holds stock.** Most products guard the location
  (Rackbeat, Zoho, Business Central refuse to delete one that is used), but few say what
  happens to the item.

## 3. What the first version keeps, and what it leaves

**In:**
- Sites and their location tree.
- Location codes and labels.
- On hand per variant per location.
- The four records the owner named: adjustment, move, count, goods receipt.
- The stock panel on the product page, with the per-location breakdown.
- The location lookup.
- A movement history per variant and per location.

**Left for later**, with the reason:

| Left out | Why it can wait |
| --- | --- |
| Lots, serials, expiry, FEFO | The owner's first tenant counts plain quantities; Manufacturing brings them, under the rules of §6.1 |
| Reservations, committed, incoming | Nothing reserves until Orders exists; the seam is shaped now (§6) |
| Valuation, cost layers, posting to the ledger | Vantigo has no general ledger; `standardCost` on the variant is enough to show a value |
| Transfers between sites | Not offered in v1 (owner, 2026-10-27): an instant move between buildings is wrong, and the two-step transfer with *in transit* arrives with Orders |
| Preferred location, min/max, replenishment | Useful, not asked for; shaped as a *placement* row (§4.4) for delivery C or later |
| Capacity and one-product locations | Advanced tier everywhere |
| Units of measure other than packagings (weight, length, purchase units) | Products phase 5; the packaging levels of §5.1 are pulled forward |
| Camera scanning in the browser | A USB or Bluetooth scanner types into a field like a keyboard and works on day one; `BarcodeDetector` is not in every browser |
| Pick lists, put-away, multi-step receipt | Warehouse-management process; the Business Central ladder says these are opt-in rungs above v1 |

## 4. One location model for a rack and a shelf

### 4.1 Sites

A **site** is the root of a tree.
- It has a name, a short **code** (`OSL`, `BUTIKK1`), an optional address, and a
  **kind**: `warehouse` or `store`.
- The kind changes defaults and words, not rules. A store's suggested location kinds are
  *sales floor, back room, aisle, shelf, display*; a warehouse's are *zone, aisle, rack,
  shelf, bin, dock*.
- Every product reviewed distinguishes stores from warehouses by flags. Vantigo's flags
  (*sells at the till*, *fulfils orders*) arrive with Point of sale and Orders, and are
  not built now.

### 4.2 Locations

A **location** has:
- a parent: the site, or another location;
- a **kind** from a fixed list, with `other` as the escape;
- a **code**, unique among its siblings;
- an optional name;
- a sort order;
- a status: `active` or `archived`.

Its **full code** joins the codes along the path with a separator (`OSL-A-03-2`). The full
code is unique within the deployment, because site codes are, and it is what the label
prints and what the lookup matches.

The tree is free, as the owner chose, and that answers each case:
- A small shop with no shelves keeps stock **on the site itself**.
- A shop that cares about floor against back room has two locations.
- A warehouse has zone → aisle → rack → shelf → bin.

Recommended rules:
- **Stock may lie on any location, the site included.** ERPNext forbids stock on a group
  and Odoo merely advises against it. Allowing it is what lets a shop that starts
  without shelves add them later and move the stock down. Totals of a location always
  **include its subtree**.
- **A location holding stock, or with stock below it, cannot be archived**: Rackbeat's
  and Business Central's rule. An empty location can be deleted only if no movement ever
  touched it; otherwise it is archived.
- **Bulk creation.** "Create aisle A, racks 01–12, shelves 1–5" generates 60 shelf
  locations in one request. Zoho and Business Central both offer this, and nobody types
  sixty shelves.
- **Re-parenting** an empty or populated location is allowed, because moving a rack is a
  real event. The move is recorded in the history, and full codes change with it. Labels
  print the full code, and a reprint is offered.

### 4.3 Codes and labels

- A label carries the full code as text and as a **Code 128** barcode. The barcode
  encodes a stable token (`L` + the location's id), not the code, so a reprint is needed
  only when the human-readable text changes. Encoding the code itself is the
  alternative: it reads better on a phone camera and breaks on re-parenting (reading 4).
- The server already has `github.com/boombuler/barcode` (QR and Code 128 capable, used in
  `srv/payments/qrsvg`) and maroto PDF generation (`srv/invoices/pdf.go`). A label sheet
  is a PDF on A4 sheets of standard sticker sizes. Thermal-printer formats are later.

### 4.4 Placement — the row that is not a quantity

The retail research's main lesson is that *where an item belongs* and *how many lie
there* are different facts. LS Central, Oracle SIM, Business Central's fixed bin and
NetSuite's preferred bin all keep a **placement** (variant × location: primary, minimum,
maximum) apart from the quantity.

The first version needs only the quantity. The placement is named here so that delivery C,
or later, adds it as its own table, not as columns on the balance. Until then, "where
does it lie" is answered by the balances.

## 5. Where the quantity lives

Every ERP reviewed keeps **a record of movements that is never rewritten, plus a fast
balance**. Vantigo does the same, in the shape the codebase already uses for its append-only
logs (`planning.events`, `staff.events`).

**A movement** is a posted document.
- Its **kind** is one of:
  - `adjustment` (with a reason: `correction`, `damaged`, `lost`, `found`,
    `opening_balance`, `other`);
  - `move`;
  - `receipt`;
  - `count`.
- It also carries a free-text note, the posting user and time, and **lines**.
- Each line holds:
  - the variant;
  - an optional *from* location and an optional *to* location (a receipt has no
    *from*, a write-off no *to*);
  - a quantity greater than zero.

  This is Square's and Odoo's model, every change being a move between two places, with
  *nowhere* standing in for Odoo's virtual locations. Vantigo needs no virtual locations.
- Movements are **never edited or deleted**. A mistake is corrected by a new adjustment,
  as in ERPNext.

**A balance** is one row per **(variant, location)** holding `on_hand`.
- It is updated in the same transaction as the movement. A line that would take a
  balance below zero is refused (`insufficient_stock`, naming the variant, the location
  and the quantity there). An administrator setting to allow negative stock comes later
  (owner, 2026-10-27).
- A rebuild check derives balances from the movements and compares them. That is a test
  and an operator command, not a worker.
- When lots arrive, the key becomes (variant, location, lot) with a null lot. The table
  is designed so that this is one column and a new unique index.

**Quantities** are `numeric(14,3)` in the variant's own `unit`, as Invoices' quantities
are, and are carried as decimals on the wire. A piece-counted item uses whole numbers by
convention. Enforcing that waits for Products phase 5's units.

**The four records:**
- **Adjustment:** set or change the quantity of variants at a location, with a reason.
  "Set to N" is posted as the difference.
- **Move:** N of a variant from one location to another **within one site**. A move
  between sites is refused in v1 (`move_crosses_sites`). An instant transfer between two
  buildings would misstate where the goods are while they travel, so the two-step
  transfer (*sent → received*, with *in transit*) arrives with Orders (owner, 2026-10-27).
- **Goods receipt:** lines into a location, with an optional supplier name and reference
  as free text. Products phase 5 brings supplier records, and Orders brings the purchase
  order a receipt will later answer. Tripletex and Fortnox agree that a purchase order
  never moves stock; only the receipt does.
- **Count:** a document with a scope (a site or a location and its subtree) and a status
  `draft → counting → posted` (or `cancelled`).
  - Opening it **freezes the expected quantities** (Katana's *work in progress*).
  - Lines are counted by typing or scanning.
  - Posting writes one `count` movement of the differences.
  - A line whose balance moved after the freeze is flagged before posting, which is
    Odoo's "outdated" (reading 3).
  - An uncounted line is either left alone or set to zero, chosen when the count is
    posted.
  - A count of a location with no stock record is how stock is first placed, as
    Tripletex advises.

### 5.1 Packagings — boxes and pallets (owner, 2026-10-27)

The owner's case: *product X is packed in boxes of 15 pieces, and 40 boxes fit on one
Euro pallet.* This is the packaging hierarchy that Business Central calls item units of
measure, Odoo calls packagings, and GS1 calls trade item levels (each → case → pallet,
each level with its own GTIN-14). It is the part of Products phase 5's units of measure
that Inventory needs, so it is pulled forward. Supplier records stay in phase 5.

**It belongs to Products, not Inventory.** "A box holds 15" is a fact about the goods,
true in every warehouse, and Orders and Point of sale will read it too.

A variant gains **packagings**. Each one has:
- a name (*Box*, *Euro pallet*);
- a kind (`inner`, `case`, `pallet`, `other`);
- the quantity it holds, **in the variant's base unit** (15, and 600);
- optionally, the packaging it is built from and how many of them (*40 × Box*), kept so
  the screen can say "40 boxes" and so the two numbers cannot drift apart. The base
  quantity is derived from it;
- an optional GTIN (usually a GTIN-14), unique across variant and packaging barcodes;
- optional weight and dimensions, as the variant has. A Euro pallet is 1200 × 800 mm;
  its height depends on what is stacked on it.

**Stock is always kept in the base unit.** Packagings change how quantities are
**entered and shown**, never how they are stored:
- A receipt, an adjustment, a move or a count line can be entered as "3 Euro pallets +
  2 boxes", and is stored as 1,830 pieces.
- **Scanning a packaging's GTIN** adds that packaging's quantity: scanning a box at
  receipt adds 15.
- Quantities can be **shown broken down**: 1,830 pieces reads as "3 pallets, 2 boxes"
  on the product panel and the location page, with the base quantity always beside it.

Tracking a *physical* pallet as a thing with an identity (Odoo's packages, a warehouse's
SSCC license plates: "this pallet, on this shelf, holding these goods") is a different
feature, and it is later.

## 6. The Orders seam, shaped now and not built

The products agree on the shape:
- An order **commits or reserves** without changing on hand.
- Fulfilment **issues** against the commitment.
- A receipt **answers** a purchase order.
- Every movement **points back to the document that caused it** (ERPNext's
  `voucher_type` / `voucher_no` / `voucher_detail_no`, Odoo's `origin`).
- Only the order system writes *committed* (Shopify).

What the first version builds so that Orders adds without migrating:

1. **A source reference on every movement**: `source_module`, `source_kind`, `source_id`
   and `source_line_id`, all null for manual records. Orders' issue and receive calls
   fill them.
2. **A read contract**, `contracts.Stock`:
   - on hand for a list of variants, in total and by site;
   - the balances of one variant;
   - the contents of a location.
   Products reads it for its panel, and Orders and Point of sale read it later. Nil when
   Inventory is disabled, as `contracts.ProductCatalog` is for Projects
   (`srv/projects/contracts.go`).
3. **The write port is named, not built.** `contracts.StockLedger` would offer
   `Reserve`, `Release`, `Issue` and `Receive`, each with a source reference, and with
   reservations as rows against an order line. Available = on hand − reserved, per site.
   The spec records the shape. Orders' own design builds it, together with the
   domain-events outbox (ROADMAP.md).
4. **Reservations are per site, not per shelf.** Every retail product commits at the
   site. Shelf allocation happens at picking time, which is the two-level pattern. The
   balance table therefore gains no `reserved` column now.

### 6.1 Manufacturing — the second consumer, shaped now (owner, 2026-10-27)

The owner plans a **Manufacturing** module later:
- the same product made in several **batches**;
- each batch with an **expiry date**;
- each batch **traceable to the raw ingredients and components** that built it.

The first tenant still counts plain quantities, so none of this is built in v1. It sets
five rules the v1 design must not contradict.

1. **Inventory owns lots, not Manufacturing.**
   - A lot (batch) is a fact about stock. A purchased raw ingredient arrives with the
     supplier's batch number and best-before date long before anything is manufactured.
   - Odoo's `stock.lot` and ERPNext's `Batch` both sit in the stock module, and the
     manufacturing order only creates them.
   - A later `inventory.lots` holds:
     - the variant;
     - a lot number, unique per variant;
     - an expiry date and an optional best-before date;
     - an optional production date;
     - the origin: received from a supplier, or produced by a source document.
   - A serial number is a lot that may hold at most one unit, so the same table serves
     both.
2. **Tracking is a property of the variant, and changing it needs zero stock.**
   - Products gains a tracking mode per variant: `none`, `lot` or `serial`, together with
     packagings or with Manufacturing.
   - A variant cannot switch mode while any stock of it exists, because untracked stock
     cannot honestly be split into lots.
   - This is the one rule that would be painful to add after the fact, so the spec
     states it now.
3. **The balance key grows by one column.**
   - The key becomes (variant, location, lot), with the lot null for untracked variants
     and `UNIQUE … NULLS NOT DISTINCT`, which the database already uses (PostgreSQL 18;
     `mig/00004_products_baseline.sql:21`).
   - Movement lines gain the same nullable lot.
   - v1's queries, contract shapes and screens group by variant and location and must
     not assume that pair is the whole key. The contract returns *rows*, never a map
     keyed by (variant, location).
4. **A production is one movement, so the trace is a query.**
   - A manufacturing order posts **one movement**, of kind `production`, with its source
     reference set to the order:
     - *consume* lines take each component lot from a location to nowhere;
     - *produce* lines put each finished lot from nowhere into a location.
   - **Backward trace** (what is in finished lot F): the produce line of F → its
     movement → that movement's consume lines and their lots → recursively.
   - **Forward trace** (where did raw lot R go, i.e. the recall): the consume lines of R
     → their movements → the produce lines → recursively, and on to every later issue.
   - This needs nothing beyond what §5 and §6 already give: lines with a nullable
     from/to, an open list of movement kinds, and the source reference.
   - When one order produces several lots and must say which components went into which,
     an optional line-to-line link joins then. It is not needed for one-lot orders.
5. **Food traceability is one step up and one step down.**
   - EU 178/2002 article 18, as applied in Norway, requires a food business to know who
     supplied each lot and to whom it went.
   - A goods receipt therefore records the lot and the supplier, which is free text in v1
     and a supplier record after Products phase 5.
   - An Orders issue records the lot and the customer through the source reference.
   - The trace screens are Manufacturing's. The records are Inventory's.

**Consequences for Manufacturing's own design**, noted so they are not forgotten:
- Raw ingredients are measured in kg and litres, and are consumed in fractions. That is
  Products phase 5's units of measure beyond packagings, and Manufacturing needs it.
- Expiry-aware picking (FEFO) and a "soon to expire" list come with lots.
- Components that are bought and never sold may want a *purchasable / sellable*
  distinction on the product, a Products question.

## 7. Where the codebase stands

### 7.1 Products: the variant, the contract, the delete

- **Variants.** A variant is an `integer` id with a unique `sku`, an optional GTIN
  `barcode` and a `unit` (`mig/00004_products_baseline.sql`). The SKU is the stable
  business key, and the Products reference already tells "future Orders, Warehouse" to
  store the id with a SKU snapshot. Inventory's movement lines snapshot the SKU and the
  product name, so history survives catalog changes.
- **`contracts.ProductCatalog`** (`srv/contracts/catalog.go:43-53`) answers `Variant`,
  `Variants` and `ListPrice`. It lacks three things Inventory needs:
  - a variant's **barcode**, for scanning an item at a count or a receipt;
  - a **lookup by SKU or barcode**;
  - **the variants of a product**, for the product panel. The panel can be fed the ids by
    the host instead (reading 6).

  Adding them is a Products change and belongs in the same delivery.
- **Variants are hard-deleted**, with their prices cascading (`srv/products/variants.go:246-277`).
  The only guard is the last-variant 409. **Nothing stops a variant that holds stock from
  disappearing.**
  - The codebase has the shape for this: a many-provider slot in the form of
    `CustomerReferenceHolders` (`srv/module/module.go:118-127`, which the customers
    merge fills to re-point ids).
  - A `VariantReferenceHolders` slot, asked before a delete, lets Inventory refuse the
    delete of a variant with **non-zero stock** (409 `variant_has_stock`).
  - A variant whose stock is zero but which has history may still be deleted. Its
    movements keep the SKU snapshot.
- **Only goods are stocked.** A `Service` variant is refused on every stock line, and so
  is a `Discontinued` product's variant on a receipt. Adjustments, moves and counts
  still accept discontinued variants, so remaining stock can be cleared.

### 7.2 The product page

- `apps/products/frontend/src/pages/products.$productId.tsx` is a stack of cards with no
  tabs and no extension point.
- The established composition is the host's: Staff's `PersonPage` takes `extraTabs`
  (`apps/staff/frontend/src/pages/person.tsx`), and the host fills them from Planning
  when the module is on (`apps/host/frontend/src/routes/staff/people.$id.tsx`).
- The product page gains an `extraPanels` prop. The host route passes Inventory's
  `<ProductStock>` card when `inventory` is enabled and the user holds
  `inventory:stock-view`.
- The card shows:
  - the total on hand;
  - per site, expandable to locations (Fortnox's drill-down);
  - per variant, when there are several;
  - a link to the variant's history.
- The product list's "On hand" column needs a batch read. It follows in delivery B, if
  wanted (reading 7).

### 7.3 Module scaffold

- **Migrations.** One shared goose directory; the latest is
  `mig/00075_energy_spot_price_service.sql`. The `inventory` schema joins `moduleSchemas`
  in `srv/db/schema_test.go`, and no SQL crosses schemas.
- **Module wiring.** The new-module checklist is
  `docs/src/content/docs/en/contributing/module-boundaries.md` (§"Adding a module").
  `config.go` gains "inventory requires products", and Inventory refuses to mount
  without `ProductCatalog`, as Planning does with `Workforce`.
- **Permissions:**

  | Key | Allows |
  | --- | --- |
  | `inventory:locations-view` | See sites and locations |
  | `inventory:locations-manage` | Create and change sites and locations |
  | `inventory:stock-view` | See quantities, history and the panel |
  | `inventory:stock-adjust` | Adjustments and moves |
  | `inventory:receipts-manage` | Goods receipts |
  | `inventory:counts-manage` | Open, count and post counts |

  All six are delegable and none is sensitive. A stock count is not personal data.
  Products' keys are split the same way. Posting a count could be split from counting it
  (reading 8).
- **Deployment.** Vantigo is single-tenant per deployment (no `tenant_id`;
  `admin/installation.md`). "A tenant builds its warehouses" means one deployment's
  sites.

## 8. The must-have list

1. Sites (warehouse or store) with a code, and a free tree of typed locations whose codes
   are unique among siblings. Full codes, bulk creation, archive and re-parent.
2. Printable location labels (PDF, Code 128) and a **location lookup**: one field
   accepting a typed or scanned code that opens the location page, which shows:
   - the path;
   - the contents, with subtree totals and a "this location only" toggle;
   - the movements.
3. On hand per variant × location, kept by an immutable movement record and a balance
   updated in the same transaction. No negative stock unless the owner says otherwise.
4. Adjustments with reasons, moves, goods receipts, and counts with frozen expected
   quantities and an outdated flag.
5. A **stock panel on the product page**: the total, by site, by location, by variant.
   A movement history per variant and per location.
6. The Products seam:
   - barcode, SKU/barcode lookup and variants-of-product on `ProductCatalog`;
   - a `VariantReferenceHolder` refusing to delete a variant that holds stock;
   - goods only.
7. The Orders seam: a source reference on every movement, a `contracts.Stock` read
   contract, and the `StockLedger` write port described in the spec but not built.

**Deliveries:**

| Delivery | Contents | What the owner can see after it |
| --- | --- | --- |
| **A** | Sites, locations and bulk creation; the movement record and balances; adjustments, moves and receipts; the product panel; the location lookup; the Products seam | The three things asked for |
| **B** | Packagings on the variant (Products) and entering, scanning and showing quantities in them; counts; location labels (PDF); an opening-balance CSV import; the list's "On hand" column | Bring existing stock in, in boxes and pallets, and keep it right |
| *Later* | Placement: a primary location per variant per site, and min/max with a low-stock list (owner, 2026-10-27: not this phase) | The first replenishment signal |

## 9. Readings for the owner

### 9.1 The owner's answers, 2026-10-27

| # | Answer |
| --- | --- |
| 1 | **Stock on any location**, the site included |
| 2 | **Refuse negative stock.** An administrator setting to allow it comes later |
| 3 | **Freeze and flag**, as recommended |
| 4 | **The label encodes a stable id token**, with the full code printed as text |
| 5 | **No moves between sites until Orders** brings the two-step transfer; an instant one "would be very weird" |
| 6 | **Refuse a delete only while the variant holds stock**, and say why in the error the user sees |
| 7 | Not answered; the product list's "On hand" column stays in delivery B |
| 8 | **Six keys**, as in §7.3 |
| 9 | **A fixed list of location kinds**, with `other` |
| 10 | **Placement and min/max later**, not in this phase |
| 11 | **Free text** supplier name and reference on a receipt. Supplier records stay in Products phase 5, designed with Orders |
| — | 1, 4 and 9 as recommended: stock on any location; the label encodes a stable id; a fixed list of kinds with `other` |
| — | **Packagings** (box of 15, Euro pallet of 40 boxes) are pulled forward from Products phase 5 into this work (§5.1) |
| — | **Manufacturing comes later** (batches with expiry, traceability to components); v1 keeps the five rules of §6.1 so lots and production join without reshaping the ledger |

### 9.2 The readings as first put

1. **Stock on any location, the site included** (§4.2). The alternative is ERPNext's
   leaves-only rule. Recommended: anywhere, with subtree totals.
2. **Negative stock.** Refuse a line that would take a balance below zero, or allow it
   with a warning, as many shops need when sales outrun the counting? Recommended:
   refuse in v1. Point of sale may need a per-site "allow negative" later.
3. **Counts:** freeze the expected quantities when a count opens and flag lines that
   moved since, or compare at posting time only? Recommended: freeze and flag.
4. **What the label's barcode encodes:** a stable id token (survives re-parenting) or the
   readable full code (any phone camera app shows something meaningful)? Recommended:
   the id token, with the code printed as text.
5. **A move between sites:** immediate in v1, or a two-step *sent → received* transfer
   now? Recommended: immediate, with the two-step transfer arriving with Orders.
6. **Deleting a variant:** refuse only while it holds stock, or also while it has
   history? Recommended: stock only; history keeps the SKU snapshot.
7. **The product list's "On hand" column:** in delivery B, or not at all?
8. **Permissions:** six keys as in §7.3, or one `inventory:stock-adjust` covering
   receipts and counts too? And should posting a count be separate from counting?
9. **Location kinds:** a fixed list with `other`, or kinds the tenant defines?
   Recommended: a fixed list, for translations and icons.
10. **Delivery C** (placement and min/max): in this phase or later?
11. **Supplier on a goods receipt:** free text until Products phase 5, or pull phase 5's
    supplier records forward into this work?
