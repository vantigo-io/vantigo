# Purchasing — supplier items, purchase orders, receipts, proposals and the invoice match (phase 1)

Revision 10, 2026-11-17, after delivery D's code review. **§15 amends earlier
decisions and wins where they differ** (A22 to A28 record what delivery 0 built, A29 to
A36 what delivery A built, A37 to A46 what delivery B built and A47 to A49 its review, A50
to A59 what delivery C built and its review, and A70 to A78 what delivery D built and A79
to A85 its review); §16 lists what changed. This spec builds on the research
`docs/superpowers/research/2026-11-17-purchasing-module.md` ("R"). It does not reopen
R §8, and it takes the defaults R §9 took for the readings; §14 says what an overturn of
each would change. Decisions are numbered P*n*. The implementation plan is
`docs/superpowers/plans/2026-11-17-purchasing-plan.md`. Abbreviations:

- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/purchasing/frontend/src/`.

## 1. Purpose

Purchasing is a new module: **Purchasing** (`purchasing`; Norwegian **Innkjøp**; package
`@vantigo/purchasing-ui`). It is the buy side of the chain Orders is the sell side of:

- **Order:** what is bought from whom, at what price, expected when.
- **Receive:** what arrived, into which location, through Inventory's ledger.
- **Match:** which order a supplier invoice bills, and what differs.

A purchase order moves neither stock nor the ledger. Stock moves when goods are received,
through Inventory; the books move when the supplier invoice is posted, in Accounting.

Purchasing **requires Accounting, Inventory and Products** (P1, P3). Manufacturing and
Communications are optional.

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **0** | The seams: the supplier directory, `StockLedger.Receive`, the open-purchases slot, Accounting's supplier-invoice events and its match slot | — |
| **A** | The module, its settings, supplier items, purchase orders with their PDF and mail | 0 |
| **B** | Receipts against an order, the remainder, a receipt taken back, *incoming* | A |
| **C** | Proposals from low stock and from Manufacturing's buy suggestions | B |
| **D** | The supplier invoice matched to the order, the differences, received not invoiced | B |

C and D do not depend on each other.

**Not in this phase** (R §7; §13 says what each waits for): inbound EHF, payment files,
supplier returns, subcontracting, drop shipment, an approval limit, freight spread,
purchase units other than packagings, requests for quotation, requisitions, blanket
orders, EHF Ordering, quantity-break prices, valuation and accruals.

## 2. Decisions — delivery 0, the seams

- **P1 — The supplier directory.** `contracts.SupplierDirectory`, a single-provider slot
  Accounting fills (rule 5):
  - `Supplier(ctx, id)`, `Suppliers(ctx, ids)` and `SearchSuppliers(ctx, query, limit)`
    answer `SupplierEntry`: id, number, name, organisation number, country, address,
    email, currency, payment terms, `Active`, `IsPerson`, `Anonymised`.
  - The bank account, the default account and the default tax code are **not** on the
    entry: they are the books', and Purchasing has no use for them.
  - It reads with no permission of its own, as every directory does; Purchasing's
    endpoints decide who sees a supplier's name.
- **P2 — The register comes out from behind the setup gate.** The supplier endpoints and
  the directory answer before *Set up the books* is done. A supplier's default account
  and tax code stay empty until then, and are refused with `books_not_set_up` if sent.
  Everything else in Accounting stays gated. The plan confirms that no supplier column
  needs a seeded row.
- **P3 — `StockLedger.Receive`.** The ledger gains a receive on the caller's transaction,
  as `Produce` is (rule 24, whose text is widened):
  - it takes the site, the lines — variant, location, quantity in the variant's unit, the
    lot or serials of a tracked variant — the source (`StockSource`, with a line id), the
    supplier's name and reference, and an idempotency key;
  - it posts one movement of the existing kind `receipt`, with the same refusals as the
    receipt endpoint (`variant_not_stockable`, the tracking rules, the site's locations);
  - it sets `source_module`, `source_kind`, `source_id` and `source_line_id`, which the
    movements table has carried unused since `mig/00108`;
  - a second call, `ReverseReceipt`, takes a receipt movement's lines back out of the
    locations they went into, refused when the stock is no longer there
    (`insufficient_stock`) or a lot is on hold.
  - The movement page links a movement with a source to its document, as a production's
    does.
- **P4 — Open purchases.** `contracts.PurchaseSupply`, a single-provider slot Purchasing
  fills, optional for its readers:
  - `OpenByVariants(ctx, siteID, variantIDs)` answers, per variant, the quantity ordered
    and not yet received on sent orders, in the variant's unit, and the earliest expected
    day.
  - Inventory's `IncomingOfVariants` adds it to the transfers in transit, on the
    low-stock list, its CSV and the product's stock card, each showing the two parts.
  - Manufacturing's replenishment adds it to *incoming* in its projection.
  - With no provider composed, both behave as today.
- **P5 — Accounting's events.** Accounting publishes its first domain events, on the
  posting's own transaction: `accounting.supplier_invoice.posted.v1` and
  `accounting.supplier_invoice.reversed.v1`, each with the invoice id, the supplier id,
  the kind, the number, the dates, the currency, the total and the reference.
- **P6 — The match slot on a supplier invoice.** Accounting cannot hold a quantity or a
  variant on its lines, and must not learn what an order is. So:
  - `contracts.SupplierInvoices`, filled by Accounting, answers a supplier invoice's
    header and lines to another module (`Invoice(ctx, id)`), and creates a **draft**
    (`CreateDraft`) from a supplier, a reference, and lines of text and amount — the
    account and tax code left for the person, the supplier's defaults filled as on any
    draft. This is a write in Accounting's own schema behind its own contract, not the
    cross-module write rule 8 forbids.
  - `contracts.SupplierInvoiceMatches`, an optional single-provider slot Purchasing
    fills: `Summary(ctx, invoiceIDs)` answers, per invoice, the orders it is matched to
    and the count of differences. Accounting's supplier-invoice list and page show it as
    a badge and a link. No provider, no badge.
- **P7 — Erasure.** Accounting's anonymisation of a person supplier calls a new
  many-provider slot, `contracts.SupplierPersonalData`, before it marks the supplier:
  Purchasing blanks the name and address it snapshotted on that supplier's orders and
  deletes their stored PDFs. The plan reads how Accounting schedules its anonymisation
  and follows Customers' ledger pattern if a deletion can fail.

## 3. Decisions — delivery A, the module, supplier items and purchase orders

### 3.1 The module

- **P8 — Registration.** `srv/purchasing` with `Module()`, schema `purchasing`, required
  modules `accounting`, `inventory`, `products` in `internal/config`, and every registry
  of *Adding a module* and AGENTS.md's list (§10).
- **P9 — Settings**, one row, behind `purchasing:settings`: the order series' start, the
  default receiving site, the mail channel orders are sent from (judged through
  Communications before the transaction, as Orders' is), the text printed under an order,
  the delivery address per site when it differs from the site's, and the price
  tolerance of P38 (default 2 %).
- **P10 — A variant holder.** Purchasing registers with `VariantReferences` (rule 20): a
  variant on a supplier item or an order line cannot be deleted. A supplier with an order
  cannot be deleted in Accounting either, through a `SupplierReferences` holder slot
  added with P1; it can be deactivated.

### 3.2 Supplier items

- **P11 — The record.** `purchasing.supplier_items`, one per (supplier, variant):
  - the supplier's item number (60 characters) and its name for the item, both optional;
  - a price per the purchase packaging, in the supplier's currency, four decimals, with
    the day it was last set and by what — a person, or an invoice (P39);
  - the purchase packaging: none (the variant's unit) or one of the variant's packagings
    by id;
  - a lead time in days, a minimum quantity and a multiple, each optional, in purchase
    packagings;
  - `preferred`: at most one per variant, kept by a partial unique index; the first
    supplier item of a variant is preferred whatever the request said.
- **P12 — Only goods and services of the catalog.** A supplier item names a variant. A
  purchase with no variant is a text line on the order (P16).
- **P13 — Where it is edited.** On Purchasing's **Suppliers** page, per supplier, and on
  the product page as a host-composed **Suppliers** card. Purchasing's supplier page
  shows the register's entry read-only, with a link to Accounting's page for whoever may
  open it, and *New supplier* through Accounting's endpoint for a holder of
  `accounting:post`.
- **P14 — Import.** Supplier items from a file through `internal/fileimport`, matched on
  SKU or barcode, dry run first, every row or nothing. A supplier's price list is the
  commonest file a buyer holds.

### 3.3 Purchase orders

- **P15 — The header.** A supplier; a receiving site; the currency, the supplier's by
  default and fixed once a line has a price; the order day and an expected day; our
  reference (a user) and the supplier's contact as text; a note printed on the order and
  an internal note; the language of the document, `nb` or `en`, defaulted from the
  supplier's country. On sending, the supplier's name and address and the delivery
  address are snapshotted.
- **P16 — Lines.** At most 500, ordered. A line is one of:
  - a **variant line**: the variant, the quantity entered in the purchase packaging and
    kept in the variant's unit too, the unit price, a discount in percent, an expected
    day of its own. Picking a variant fills the supplier's item number, price, packaging
    and expected day (today plus the lead time) from the supplier item; a quantity under
    the minimum or off the multiple is a warning, never a refusal;
  - a **text line**: a description, a quantity, a unit, a unit price — freight, a
    service, something outside the catalog. It is never received into stock; it is
    marked delivered by hand.
  - A goods variant that is Discontinued is refused on a new line
    (`variant_discontinued`). A service variant is ordered and marked delivered like a
    text line.
- **P17 — Amounts.** Net of VAT throughout, in the order's currency, two decimals per
  line, summed. No tax code, no VAT amount and no conversion to NOK on an order (R §9.5).
  The PDF says *Amounts exclude VAT*.
- **P18 — States.** `draft` → `sent` → `partially_received` → `received`; `closed` and
  `cancelled` beside them.
  - `draft`: everything may change; it has no number; it may be deleted.
  - **Send** allocates the number from the series, stores the PDF and queues the mail.
    *Mark as sent* does the same without a mail, for an order placed by phone or in a
    supplier's web shop. Both need `purchasing:orders-send`.
  - `sent` and `partially_received`: lines may be added; a line's quantity may change
    down to what is received; price and expected day may change. Every change after
    sending is written to `order_changes` with who and when. **Send again** stores a new
    PDF, marked *Revised*, and mails it.
  - `received` is reached when every goods line is received in full or closed, and every
    other line marked delivered; it is derived on each receipt, not set.
  - **Close** ends an order with a remainder: the open quantities stop counting as
    incoming. **Reopen** undoes it. `closed` and `received` differ only in whether
    anything was left.
  - **Cancel** is for a sent order with nothing received; a draft is deleted instead.
- **P19 — The supplier's confirmation.** On a sent order a person records the supplier's
  order reference and the confirmed day, per order and optionally per line. It is a fact
  on the order and no state. An order sent more than three business days ago with no
  confirmation is an attention item for our reference.
- **P20 — Numbers.** One gap-free series, from `purchasing.counters`, allocated on
  sending as Orders' is. The number is what the supplier is asked to quote on the
  invoice, and the PDF says so in a line of its own: it is the match's key (P36).
- **P21 — The PDF and the mail.** As Orders' documents: rendered once, stored under the
  `purchasing` scope keyed by hash, sent from the settings' channel through
  `contracts.OutboundMail` by a leased queue with `queued`, `sent`, `failed`. The mail
  goes to the supplier's address in the register, or one typed for this order. With no
  channel chosen, *Send* is not offered and *Mark as sent* is.
- **P22 — Copy.** *Copy* makes a draft from any order with today's prices from the
  supplier items; a recurring purchase is a copy.
- **P23 — The screens.** **Purchase orders** (the list with state, supplier, expected
  day, received and invoiced as fractions, filters *Late* and *Mine*), the order page,
  **Suppliers**, **Settings**. The dashboard card counts orders late and orders
  unconfirmed. The spotlight finds an order by number and by the supplier's reference.

## 4. Decisions — delivery B, receipts

- **P24 — Receiving.** *Receive* on a sent order lists the open goods lines with the
  remainder prefilled. Per line: the quantity, in the purchase packaging or the unit; the
  location, defaulted from the variant's placement in the site; the lot — number, dates,
  supplier lot — or serials of a tracked variant. A line can be split over locations and
  lots. One submission is one receipt and one Inventory movement.
- **P25 — One transaction.** The receipt row, the lines' received quantities and
  `StockLedger.Receive` commit together on Purchasing's transaction (P3). Contract reads
  go before it; `LockVariants` is the one call inside it (rule 21). The order row is
  locked first, so two receipts of one order queue.
- **P26 — More than ordered.** A quantity over the remainder is refused with
  `over_receipt` unless the request says `acceptOver: true`, which the screen sets after
  asking. The line's received quantity then exceeds its ordered one, and the order is
  `received`.
- **P27 — Less than ordered.** The order stays `partially_received` with its remainder
  (R §9.8). No back-order document is made.
- **P28 — What a receipt keeps.** Its number in a series of its own, the day, who, the
  supplier's delivery note number as text, a note, and per line the order line's price
  then (R §9.4): the figure a later valuation would start from, used by nothing in this
  phase.
- **P29 — Taking a receipt back.** *Undo* on a receipt calls `ReverseReceipt` and lowers
  the lines' received quantities, in one transaction; refused when Inventory refuses, and
  when a line of it is matched to an invoice (`receipt_invoiced`). The receipt is kept,
  marked undone. Goods sent back to a supplier for credit are a return, which is phase 2.
- **P30 — Permissions and scanning.** Receiving needs `purchasing:receive` alone:
  Purchasing writes stock through the ledger as Orders' deliveries do, and the locations
  it offers are read through `contracts.Stock`. The receive screen takes a scanned code
  through `ProductCatalog.LookupCode` — a variant's or a packaging's barcode selects the
  line and adds one of that packaging.

P4's readers come alive with this delivery: *incoming* on the low-stock list, the stock
card and Manufacturing's replenishment.

## 5. Decisions — delivery C, proposals

- **P31 — Demand through a slot.** `contracts.PurchaseDemand`, a many-provider slot:
  `Demand(ctx, siteID)` answers rows of variant, site, quantity in the variant's unit,
  the day needed and a source label. Inventory provides its low-stock suggestions;
  Manufacturing its *buy* suggestions. Both already subtract incoming, which after P4
  includes open purchase orders, so a proposal never orders twice.
- **P32 — One row per variant and site.** The two providers project the same stock, so
  their rows are not summed: the larger quantity wins and the row names both sources.
- **P33 — From a row to an order.** The **Proposals** page lists the rows with the
  preferred supplier, its price, the quantity rounded up to the minimum and the multiple,
  and the day to order by (the day needed less the lead time). A person ticks rows,
  changes a supplier or a quantity, and **Create orders** makes one draft per supplier
  and site — or adds lines to an existing draft of that supplier and site. A row with no
  supplier item is listed under *No supplier* and cannot be ticked.
- **P34 — Nothing is ordered by itself.** A proposal is made into a draft by a person
  and sent by a person. Automatic ordering is not planned.
- **P35 — Links in.** Inventory's low-stock page and Manufacturing's replenishment page
  show **Order…** on a row, a host-composed link to the proposal filtered to that
  variant, when Purchasing is enabled and the person holds `purchasing:orders-manage`.
  Manufacturing's refusal of a *buy* row (`replenishment.go:664`) stays: it is bought
  here.

## 6. Decisions — delivery D, the invoice match

- **P36 — The key.** A supplier invoice is suggested for an order when its `reference`
  contains an order number of that supplier, read as whole digit runs. Accounting's
  `reference` is typed by a person today; an inbound EHF's order reference will land in
  the same field (§13), and the match needs no change then.
- **P37 — The match.** `purchasing.invoice_matches`: the supplier invoice's id, an order
  line, a quantity and a net amount in the order's currency. One invoice may bill several
  orders of its supplier, and one order several invoices. A match is made:
  - **from the order**: *Register invoice* creates Accounting's draft through
    `SupplierInvoices.CreateDraft` (P6), with one line per order line of what is received
    and not yet invoiced, the order number as its reference, and the match rows with it.
    It needs `accounting:post` as well as `purchasing:invoices-match`;
  - **from the invoice**: on Accounting's supplier-invoice page, a host-composed
    **Purchase order** panel suggests orders by P36 and lets a person pick one and
    allocate quantities; *Match all received* fills what is received and not invoiced.
  - A draft's match is provisional. It is fixed when Purchasing handles
    `accounting.supplier_invoice.posted.v1`, and released by `…reversed.v1` (P5). A
    credit note matched to an order lowers its invoiced quantities.
- **P38 — Differences are warnings.** Per matched line, on the order and on the panel:
  - *invoiced more than received*;
  - *invoiced more than ordered*;
  - *price differs*: the invoice's unit price against the order's by more than the
    settings' tolerance;
  - *currency differs* between invoice and order.
  Posting is never held (R §9.6): Accounting posts what the person posts.
- **P39 — The last price.** When a matched invoice is posted, each matched variant line's
  unit price becomes the supplier item's price, marked as set by that invoice — unless a
  person set the price after the invoice's date.
- **P40 — Ordered, received, invoiced.** The order page shows the three per line and for
  the order; the list shows them as fractions. An order `received` and invoiced in full
  drops from the default list view.
- **P41 — Received, not invoiced.** A list and a CSV, per supplier and order, of received
  quantities with no posted invoice, at the order's prices, as of a chosen day. It is
  the accountant's basis for a manual accrual at a period's end; nothing is posted from
  it.

## 7. Events

Published by Purchasing through the outbox, each on the transaction of its change:
`purchasing.order.sent.v1`, `purchasing.order.closed.v1`,
`purchasing.receipt.posted.v1`, `purchasing.receipt.undone.v1`. None has a consumer in
this phase; they are the seam for a supplier timeline, and for Manufacturing's
subcontracting.

## 8. Permissions

| Key | Lets a person | Sensitive |
| --- | --- | --- |
| `purchasing:access` | open the app; read orders, receipts, supplier items and the proposals | no |
| `purchasing:orders-manage` | write drafts, change sent orders, record confirmations, close and reopen, make orders from proposals | no |
| `purchasing:orders-send` | send, mark as sent, send again, cancel | no |
| `purchasing:receive` | receive, undo a receipt, mark a text line delivered | no |
| `purchasing:supplier-items-manage` | write and import supplier items | no |
| `purchasing:invoices-match` | match and unmatch supplier invoices, read received not invoiced | no |
| `purchasing:settings` | the settings | no |

Prices are shown to every holder of `purchasing:access`; a holder of `purchasing:receive`
alone receives without seeing them (A47). A price-view key beside access is §14's
question 5.

## 9. Data model (schema `purchasing`)

`settings` · `counters` · `supplier_items` · `orders` · `order_lines` · `order_changes` ·
`documents` · `mailings` · `receipts` · `receipt_lines` · `invoice_matches` ·
`import_jobs`.

- Suppliers, variants, sites, locations, users and supplier invoices are referenced by id
  with no foreign key across schemas (rule 4).
- `order_lines` keeps `quantity`, `received` and `invoiced` in the variant's unit, with
  `CHECK (received >= 0 AND invoiced >= 0)`; `received` and `invoiced` are written only
  by a receipt, its undo, and the match's handlers.
- One baseline migration with delivery A, numbered from `main` on the day it is opened,
  and checked again right before merging: two stacks have collided on a number before.

## 10. Platform, registries and documentation

- **Registries**, with delivery A: `businessModules`, `openapi.Modules`, the codegen
  config, the depguard sets (the hyphenated ones too), `moduleSchemas`, the config's
  known set and dependency check, `modulesEnv` in `modtest` (Purchasing requires three
  modules), `everyModule()` in the Assistant's integration test, `APP_ROUTE_PREFIXES` and
  `appRoutes` with `promptVersion` bumped, the host's `moduleKeys`, `apps.ts`, layout
  route, `i18n.ts`, admin permission labels, dashboard, spotlight and `sessionClients`,
  the deployment lists and `GRANT` lines.
- **Module boundaries**: rule 24 widened to *receives*; the new slots named under rule 5;
  Accounting's first events under rule 23.
- **Documentation**, in the same commit series as the code:
  - `user/purchasing.md` in `en/` and `nb/`, growing with each delivery, listed in the
    user guide's overview;
  - `reference/purchasing.md`;
  - `user/inventory.md` (en, nb) and `reference/inventory.md`: receipts with a source,
    *incoming* in two parts, **Order…** on the low-stock list;
  - `user/accounting.md` (en, nb) and `reference/accounting.md`: the register before the
    books are set up, the **Purchase order** panel, the events, the contracts;
  - `user/manufacturing.md` (en, nb) and `reference/manufacturing.md`: *incoming* and
    **Order…** on a buy suggestion;
  - `user/products.md` (en, nb): the **Suppliers** card;
  - `contributing/module-boundaries.md` and `contributing/platform-packages.md`;
  - `go generate` in `apps/server` after each documentation change, for the Assistant's
    embedded copy.
- **The Assistant**: every operation carries a `summary` and its mode — reads `auto`,
  writes `confirm`, *Send* and *Cancel* `confirm`. Tour targets for *New purchase order*
  and *Receive*.
- **The public site**: `vantigo.io/apps/purchasing` in `vantigo-landing`, written from
  the two user guide pages once delivery B has landed, its pull request linked to that
  delivery's; the Inventory and Manufacturing pages there gain a line each.
- **ROADMAP.md**: the Purchasing section; Products phase 5, Inventory's and
  Manufacturing's *Later*, and the bank feed's line amended (R §9.2, §9.3).

## 11. Edge cases

- **A supplier is deactivated** with orders open: the orders stay and can be received;
  a new order for it is refused (`supplier_inactive`).
- **A supplier changes currency** in the register: sent orders keep theirs; supplier
  items keep their price and are flagged *currency differs* until a person sets it.
- **A variant's packaging is deleted** while a supplier item names it: Products refuses
  through the holder, or — the plan decides which the holder supports — the supplier item
  falls back to the unit and is flagged.
- **A variant changes tracking mode**: possible only at zero stock (Inventory's rule);
  an open order line is unaffected, and the receipt asks for what the variant needs then.
- **The receiving site is archived**: receiving is refused by Inventory; the order's site
  may be changed while nothing is received.
- **An invoice is matched, then its supplier changed on the draft**: the match is
  released when the supplier no longer owns the order.
- **Two people receive one order at once**: the second waits on the order's lock and then
  sees the first's quantities; a remainder gone is `over_receipt`.
- **The mail fails**: the order is `sent` with a failed mailing and an attention item;
  *Send again* retries.
- **Purchasing is switched off** in an installation: Inventory and Manufacturing lose the
  provider of P4 and behave as before; Accounting's badge disappears.

## 12. Testing

- **Delivery 0:** the directory against the register; the register before and after the
  setup; `Receive` and `ReverseReceipt` against the receipt endpoint's own cases, with
  lots and serials, on a pool of one; `OpenByVariants` with no provider.
- **A:** the order's states as a table; the series under concurrent sends; the PDF in
  both languages; the mail queue with Communications' fake; the import's dry run; the
  holder.
- **B:** partial, exact and over receipts; two concurrent receipts; an undo refused by
  stock and by a match; *incoming* on the low-stock list and in replenishment.
- **C:** the two providers' rows merged; rounding to minimum and multiple; one draft per
  supplier and site; a proposal after an order is sent no longer lists the row.
- **D:** the suggestion by reference; a match over two orders; posted and reversed
  through the dispatcher; each warning; the last price and its guard; the
  received-not-invoiced list as of a day.
- **Integration:** low stock → proposal → order sent → received → invoice registered and
  posted, and the stock, the open items and the order agree.
- **Frontend:** the order editor, the receive screen with a scanned code, the proposals
  page and the match panel.

## 13. Later, and what each waits for

| What | Waits for |
| --- | --- |
| Inbound EHF, landing as a draft with the order reference in `reference` | Accounting phase 2, and receiving on the access-point port |
| Payment files (pain.001 with KID), Folio payment drafts | Accounting phase 2 |
| A second person's approval of a supplier invoice | Accounting phase 2, with inbound EHF |
| Supplier returns and the credit note | phase 2 |
| Subcontracted operations | phase 2, with Manufacturing |
| Drop shipment from a sales order, and an order line bought for a named sales order | phase 2, with Orders |
| An approval limit on sending | phase 2 |
| Freight spread over a receipt's lines | valuation |
| Purchase units that are not packagings | Products phase 5 |
| Prices per quantity break and per period | demand |
| Requests for quotation, requisitions, blanket orders | demand |
| EHF Ordering and Despatch Advice | demand from a customer who sells to the public sector |
| Valuation, the received-not-invoiced accrual, price variance | a decision on stock in the ledger |
| A supplier's timeline, customer-is-also-supplier | the register as its own module |

## 14. Open questions

Each is answered by default; the plan is written from the defaults unless overturned.

**Answered 2026-11-17, the owner:** the first customer keeps its books in Vantigo, so
question 1's default stands and delivery D matches against Accounting's supplier
invoices alone. Questions 2 to 8 stand at their defaults until the owner says otherwise.

1. **Purchasing requires Accounting** (P1, P2) — the riskiest default. Accounting
   requires Invoices, and delivery D works only once the books are set up. A business
   that keeps its books in Tripletex, Fiken or PowerOffice gets A to C and no match.
   **Who is the first customer?** If it keeps its books elsewhere, D gains an *invoice
   received* record of Purchasing's own — number, date, amount, file — that the match
   runs against when the books are not set up. A register of Purchasing's own, or the
   register as a new module, would change delivery 0 and P13, and nothing after.
2. **Supplier items live in Purchasing, not Products** (P11). Overturned, they move to
   Products with a read of the supplier directory, and Purchasing reads them through the
   catalog contract.
3. **Inbound EHF and payment files are Accounting's phase 2**, next after this phase.
   Overturned, inbound EHF becomes a delivery E here and needs the access-point port
   moved out of Invoices first.
4. **No valuation** (P28, P41). Overturned, delivery B gains a cost per receipt line in
   NOK and a write contract for the variant's cost, and Accounting a stock posting.
5. **Prices are visible to everyone with access.** A `purchasing:prices-view` key would
   let a warehouse worker receive without seeing what was paid.
6. **The Norwegian name is Innkjøp.**
7. **A buyer may add a supplier** through a new, narrower Accounting key,
   `accounting:suppliers-manage` (A12). Overturned, only a holder of `accounting:post`
   adds suppliers, and a buyer asks the bookkeeper.
8. **A person supplier is not anonymised before the books are set up.** Anonymisation
   waits for the bookkeeping's retention, which is counted from the books' fiscal year,
   so before the setup it is refused (409 `books_not_set_up`, delivery 0). A workspace
   that keeps a buyer's register but never sets up its books therefore cannot meet an
   erasure request for a sole proprietor it buys from. **Default: leave it as is** — a
   workspace with Purchasing on keeps its books in Vantigo (question 1). Overturned,
   anonymisation before the books takes the supplier out at once when no voucher can
   name it, which is every supplier then.
9. **A variant's only supplier item and `preferred: false`** (P11, A33). Delivery A keeps
   the variant's only item preferred whatever the request says, and answers it so.
   **Default: accept and ignore it.** Overturned, `preferred: false` on a variant's only
   item is refused (400), so a client learns the flag did not take.
10. **Send and Discontinued variants** (P16, A16). A Discontinued goods variant is refused
    on a new line only; a draft written before the product was discontinued still sends.
    **Default: Send does not judge it again.** Overturned, Send refuses a draft with a
    line of a Discontinued goods variant (400 `variant_discontinued` naming the line),
    and the buyer removes or replaces it.
11. **The agents' role template for purchasing work** (A25). Delivery A gives virtual
    employees no template for Purchasing: no agent writes orders or supplier items yet.
    **Default: none until the owner asks.** A template would name the operations an agent
    may call — the order and supplier-item writes, never Send — and the review threshold.
12. **A replenishment run on every sent order and receipt** (A45). Manufacturing's
    handler queues a `manual` replenishment run for the site on every sent order, receipt
    and undo, unless one is pending. With auto-create rules on, such runs may create
    manufacturing orders more often than the nightly run would. **Default: keep.**
    Overturned, the handler only marks the site's projection stale, and the nightly run —
    or a person — makes the orders.
13. **The proposals show Inventory's need and purchase prices to anyone with
    `purchasing:access`** (P33, A58). A holder of `purchasing:access` with no Inventory
    permission sees each product's need per site — Inventory's low stock, or
    Manufacturing's buy suggestions — and the supplier items' purchase prices on the
    proposals. **Default: keep** — a buyer needs both to order. Overturned, the proposals
    ask for an Inventory read permission as well, or leave the need out for a caller
    without one.

14. **A draft Register invoice orphaned by a crash** (A73, A78). Accounting commits the
    draft before Purchasing records the request and the matches; a process that dies in
    between leaves a draft with no match, and a replay is refused as
    `duplicate_supplier_invoice`. **Default: the person matches that draft from its
    Purchase order panel.** Overturned, Register invoice reserves its request before the
    draft and adopts a duplicate of its own number.

15. **What the Purchase order panel tells a reader without `accounting:read`** (A20, A83).
    The panel is read under `purchasing:access`, and its id is Accounting's. **Default: such
    a reader is answered from the matches alone** — the number, date, currency and kind they
    were made with, the matched quantities, no invoice lines, amounts, reference or
    suggestions — and an invoice with no match is not found, so the read tells nothing of
    an invoice Purchasing has not matched. A matcher without `accounting:read` therefore
    cannot allocate an unmatched invoice's lines by position. Overturned, the panel answers
    such a reader the invoice's header and line positions for any invoice id, as delivery D
    first built it.

## 15. Amendments (revision 2)

From the critic review. Each amendment names the decisions it changes and wins over
them. Code references are against `main` at `64829c4e2`.

### Critical

- **A1 — `CreateDraft` sends what a draft must have** (P6, P37). A supplier-invoice line
  needs an account (`mig/00185:146`), a draft needs the supplier's invoice number, unique
  per supplier, and an invoice date, and a draft in another currency needs its rate or
  its NOK total (`srv/accounting/supplier_invoices.go:118, 173, 204`). So:
  - *Register invoice* opens a dialog that asks for the supplier's invoice number, the
    invoice date, the due date and, for another currency, the rate or the NOK total. The
    file may follow on the draft; posting still refuses a draft without one.
  - Each line is sent `entry: net` with the supplier's default account. A supplier with
    none is refused (`supplier_account_missing`), with a link to set it.
  - A draft holds 200 lines (`maxSupplierInvoiceLines`). An order with more open lines
    than that is invoiced in parts; the dialog says so and takes the first 200.
  - The action is offered only when the books are set up.
- **A2 — The register before the books, without breaking the SAF-T setup** (P2).
  `ApplyAccountingSaftImport` inserts every supplier of the file as new
  (`saft_import.go:1416`), so a supplier a buyer made first would fail the import on its
  organisation number, or be duplicated without one. Delivery 0 therefore:
  - matches the file's suppliers to existing ones by organisation number, then by exact
    name, shown in the import's preview and picked by the person where it is unsure, as
    the import links customers today;
  - makes the supplier reads work with no settings row: `supplier()` and the list read
    the retention date from settings (`suppliers.go:224, 258`) and would answer 500
    before the setup. With no settings, a supplier is simply not yet anonymisable.

### Major

- **A3 — *Incoming* has four readers, and the low-stock filter is SQL** (P4). The list
  filters `on_hand − reserved + incoming ≤ reorder_point` in one statement
  (`srv/inventory/queries/placements.sql:109`), its dashboard count in another (`:127`);
  `IncomingOfVariants` feeds only the stock card; Manufacturing reads
  `Stock.Placements`. So:
  - `PurchaseSupply` gains `OpenAll(ctx)`: every (variant, site) with an open quantity
    and its earliest expected day. `OpenByVariants` takes an optional site.
  - Inventory reads `OpenAll` before the low-stock statement and its count, and hands the
    rows in as arrays the statement joins, so the filter, the keyset paging, the CSV and
    the dashboard count agree. The list and the stock card show *in transit* and *on
    order* as two figures.
  - `Stock.Placements` stays transfers only. Manufacturing adds open purchases itself,
    from `PurchaseSupply`; otherwise it would count them twice.
- **A4 — One source of proposals, not the larger of two** (P31 to P33). The two
  projections differ: Manufacturing nets production and unreserved sales demand
  (`srv/manufacturing/replenishment.go:8`), stores its suggestions from a nightly or
  requested run (`:396`), answers *make* for a variant with a default BoM (`:163`), and
  does not round to packagings as Inventory does (`low_rule.go:72`). So:
  - where Manufacturing is composed, **its *buy* rows are the proposals** and Inventory's
    provider is not asked. A manufactured item is never proposed for purchase;
  - where it is not, Inventory's low-stock rows are;
  - each provider answers the need **before** its own rounding and the time it was
    computed. Purchasing rounds once, to the supplier item's packaging, minimum and
    multiple;
  - Purchasing subtracts what is on its own **drafts**, and what was sent after the
    provider's computed time, so a stored suggestion never orders twice;
  - Manufacturing handles `purchasing.order.sent.v1` and
    `purchasing.receipt.posted.v1` by requesting a replenishment run for the site.
- **A5 — Taking a receipt back is its own movement kind** (P3, P29). A receipt of an
  inspected variant puts a quarantine hold on its lots (`srv/inventory/posting.go:626`),
  and a failed inspection is the commonest reason to undo. `ReverseReceipt` posts a new
  kind, `receipt_reversal`, on the unbuild's precedent (`ledger.go:56, 1030`): it takes
  the receipt's own lots whatever their holds, releases the holds that receipt set, and
  is idempotent by its source. It is refused only when the stock is no longer in the
  location it was received into.
- **A6 — A Discontinued variant is received** (P3, P16). The receipt endpoint refuses one
  (`posting.go:311`); `Receive` does not, as `Produce` does not: discontinuing a product
  and receiving its last open order is the normal sequence. A new line is still refused.
  The ledger's refusal is `variant_not_stocked`, and the receive's codes join
  `StockRefusal`.
- **A7 — The lock order of rules 21 and 24** (P25). Products' rows first, then the
  caller's, then Inventory's. A receipt reads the order's variants on the pool, calls
  `LockVariants`, then locks the order row and checks that its lines are as read. The
  engine's deadlock retry is not available on a caller's transaction
  (`posting.go:1035`), so Purchasing retries the whole transaction, three times, as
  Manufacturing does (`replenishment.go:686`).
- **A8 — Three new directions get rules of their own** (P6, P7, P10), numbered from the
  next free in module boundaries (26 today):
  - **Purchasing drafts a supplier invoice through Accounting's contract.** Rule 8 says
    another write direction needs a design of its own; this is it, bounded to a draft a
    person must still complete and post.
  - **A supplier's personal data.** `SupplierPersonalData` has an *export* half, for the
    access request Accounting answers (`ExportAccountingSupplier`), and an *erase* half
    inside Accounting's transaction that blanks Purchasing's snapshots and writes the
    stored PDFs to a deletion ledger a worker finishes — an object is never deleted
    inside that transaction. Accounting's anonymisation is a person's click gated on
    retention, not a schedule (`suppliers.go:363`); a supplier with an open order is
    refused through the holder (`supplier_has_open_orders`).
  - **Locking a supplier.** `DeleteAccountingSupplier` does not lock the row
    (`suppliers.go:426`), so an order made at the same moment would survive the delete.
    `SupplierDirectory.LockSuppliers(tx, ids)` takes them `FOR SHARE` when an order is
    created or sent, and Accounting's delete and anonymisation take the row `FOR UPDATE`
    before asking the holders.
- **A9 — A match names the invoice's line** (P37 to P39). A match row is (supplier
  invoice line, order line, quantity). `SupplierInvoices.Invoice` answers each line's
  net amount as Accounting computes it. *Price differs* and the last price use the
  **posted line's net over the matched quantity** — never an amount typed in Purchasing,
  which for a draft made by *Register invoice* would only be the order's own price
  written back. The last price is set only when the invoice's currency is the supplier
  item's.
- **A10 — `receipt_invoiced`** (P29). An undo is refused when it would take a line's
  received quantity below what is invoiced on it, posted or provisional.
- **A11 — Idempotency** (P24, P33, P37). *Receive*, *Register invoice*, *Create orders*
  and *Send* each take a `clientRequestId` with a request hash, as Inventory's receipt
  does: a replay answers the first result, another body under the same id is `409`.
  Provisional matches count as invoiced when a draft is prefilled.
- **A12 — Adding a supplier without the right to post** (P13). Creating a supplier needs
  `accounting:post` today (`openapi/accounting.yaml:8895`). Delivery 0 adds
  `accounting:suppliers-manage`: create and edit a supplier's name, addresses, contact
  and terms — not its bank account, default account or tax code, which stay with
  `accounting:post`. §14's question 7.

### Minor

- **A13 — P3's wording.** The source columns are set by `Issue` and `Produce` today
  (`posting.go:463`); a receipt is what leaves them empty. `source_line_id` is on the
  movement's header. `Receive` is idempotent by its source, not by a separate key.
- **A14 — Credit notes** (P37). A credit matched for more than a line's invoiced
  quantity is refused when matched (`credit_exceeds_invoiced`); the handler never fails
  on the `CHECK`.
- **A15 — Reverse and copy** (P37). Accounting's usual correction reverses an invoice and
  copies it (`supplier_invoices.go:993`). The copy takes the reversed invoice's matches,
  provisional, by `copied_from_id`.
- **A16 — P18's gaps.**
  - **Close line** ends one line's remainder, for `purchasing:orders-manage`; the order's
    *Close* closes every open line.
  - **Cancel** of an order that was mailed sends the supplier a cancellation — the PDF
    marked *Cancelled* — unless the person unticks it.
  - An undo on a `received` order makes it `partially_received`; on a `closed` order it
    stays closed, and *Reopen* is the way to receive again.
  - A price may be changed after an invoice is matched; it is written to `order_changes`
    and the warnings are judged again.
- **A17 — The holder's purposes** (P10). A variant on any supplier item or order line is
  not deleted; a product with an open goods line does not become a service; a tracking
  change is not held.
- **A18 — Registries left out of §10.** The contracts entry in `docs/astro.config.mjs`,
  the `api-schema.d.ts` entry in `gen-client.ts`, and `modulesEnv` cascading purchasing
  → accounting → invoices.
- **A19 — The PDF and the send** (P21). The PDF names our company from
  `SellerProfile`, as Orders' does (`srv/orders/pdfdocs.go:190`), and is stored after
  the commit. A person's *Send* mails synchronously outside the transaction, as Orders'
  does; the leased queue is for a retry of a failed mailing.
- **A20 — Reading an invoice's lines.** The match panel and the order's invoice figures
  show an invoice's lines only to a holder of `accounting:read`; others see the matched
  quantities alone.
- **A21 — The receive screen** (P30) shows no prices, and reads a GS1 code's lot and
  expiry as Inventory's receipt does. The plan finds where that parsing lives
  (`srv/inventory/gs1.go`) and moves it to where Purchasing may use it.

### Delivery 0, as built (revision 3)

What delivery 0 built where it differs from the decisions above, after its code review.
Each wins over what it names.

- **A22 — The events carry no reference** (P5). `accounting.supplier_invoice.posted.v1`
  and `…reversed.v1` carry the ids, the kind, the number, the dates, the currency and the
  total — no `reference` and no description: a payload holds no free text (module
  boundaries, rule 23). Purchasing's handlers read the reference through
  `SupplierInvoices.Invoice` when they need it.
- **A23 — The holders' codes** (P7, A8). A module's documents naming a supplier refuse its
  delete with 409 `supplier_has_documents`, and its open documents refuse its
  anonymisation with 409 `supplier_has_open_documents` — Accounting's codes, whatever the
  holder's documents are; `supplier_has_open_orders` is not a code.
- **A24 — The SAF-T import's supplier picks** (A2). The picks travel in the apply's body
  (`suppliers`, per file supplier id the register's id or 0 for a new one). A supplier
  of the file that could be several of the register's — several of one name, or one
  another of the file's suppliers is matched to as well — is `ambiguous`, and Apply is
  refused (409 `saft_import_ambiguous_suppliers`) until it is picked. A pick names a
  supplier of the file (`saft_import_file_supplier_unknown`) and of the register
  (`saft_import_supplier_unknown`), and a register supplier once
  (`saft_import_supplier_picked_twice`); an inactive one may be picked. The register's
  suppliers taken are held `FOR SHARE` in the apply's transaction
  (`saft_import_supplier_gone` when one was deleted since), and an empty bank account,
  IBAN or BIC of theirs is filled from the file — never one they have.
- **A25 — The supplier writes are `session`, open to agents by a mark** (A12).
  `POST /suppliers`, `PUT /suppliers/{id}` and the Brønnøysund lookup are
  `x-vantigo-access: session` with the handler's rule: `accounting:suppliers-manage`, or
  `accounting:post` with `accounting:read` (403 `suppliers_manage_required`). They carry
  `x-vantigo-agent: allowed`, a new extension the OpenAPI lint accepts only beside
  `session`, which the router carries on `Rule.Agent` and identity's agent check needs
  to meet a session rule; these three are the only operations marked. The agents' gate
  still allows no operation whose rule names no permission, so a virtual employee calls
  them once a role's template and the gate say so — Purchasing's business, not delivery
  0's.
- **A26 — The buyer's field scope** (A12). Without `accounting:post` a caller writes every
  field but the bank account, IBAN, BIC, default account and default tax code, and on a
  change the person flag and the active flag (403 `supplier_fields_need_post`); a field
  left out keeps its value. A buyer's new supplier may be a person's and in another
  currency, and the currency stays a term the buyer changes. A books field kept as it
  stands is not checked again, so a buyer's rename is not refused because the default
  account was made inactive since. The register's screen in Accounting stays behind
  `accounting:read`; the buyer's screen is Purchasing's (delivery A).
- **A27 — A receipt's document is required, and its reversal idempotent by it alone**
  (P3, A5, A13). `StockReceipt.Document` is required — one per receipt — and Receive reads
  its lines with the receipt endpoint's own code: its lot, date and serial rules are the
  line's refusals (`lot_number_invalid`, `lot_dates_invalid`, `serial_quantity`, and the
  new `line_invalid`). `ReverseReceipt` hashes the receipt's document alone: a retry with
  another note answers the first reversal. It checks on hand only, not reservations. The
  lot trace ends a usage path at the reversal, and the dashboard's receipts this week and
  a site's last receipt leave a reversed receipt out. No migration came with it:
  `movements.kind` has no check, and `receipt_reversal` fills its varchar(16) exactly.
- **A28 — `PurchaseDemand` has no slot until delivery C** (P31). Delivery 0 declares the
  contract type and composes nothing; delivery C adds the slot to `module.Deps` with its
  providers.

### Delivery A, as built (revision 4)

What delivery A built where it differs from the decisions above, after its code review.
Each wins over what it names.

- **A29 — No import job table** (P14, §9). The price-list import runs synchronously, at
  most 5000 rows, dry run first and every row or none; `import_jobs` is not created. A
  price takes a decimal point, or a decimal comma with the Norwegian groupings
  `1 234,50` and `1.234,50` where they read one way only.
- **A30 — The deletion ledger is `purchasing.deletions`** (A8). The erase writes each
  stored PDF's key there inside Accounting's transaction and the `purchasing.deletions`
  worker deletes the objects; a PDF recorded while an erase runs is refused for an
  anonymised order (the insert holds the order `FOR SHARE`) and its object written to the
  ledger instead. The erase also blanks the mailings' last errors and the change log's
  values of the contact and the notes; the export carries the change log.
- **A31 — The delivery address** (P9, P15). A site's delivery address is the settings'
  for the site when set, else the company's address from `SalesTax.Seller` under the
  site's name — Inventory's sites keep no address. A draft computes it live; Send
  snapshots it. A settings address is bounded by the snapshot's columns, its country a
  two-letter code.
- **A32 — A supplier item's price only in its packaging and currency** (P11, P16, P22).
  A line takes the item's price only when the item's currency is the order's, and in the
  item's packaging as it is; a line in another packaging takes it converted
  (`round4(price × line factor / item factor)`), 0 when a factor is not known. Copy
  judges it the same way.
- **A33 — A sent order's lines are not removed** (P18). A save of an open order names
  every saved line (`line_not_found` for one left out); a line ends by Close line.
  Closing the last open line of an order received in full makes it `received`, with its
  change-log row and no closed event.
- **A34 — The send is an outbox** (P21, A11, A19). Send, Send again and Cancel's notice
  write their mailing `queued` inside their own transaction, held by the request for the
  lease; after the commit the request claims it and delivers it as the worker does, the
  PDF stored first. A send is never acknowledged without its mail; a replay answers the
  mailing as it stands. Send again fails the earlier versions' still-queued mailings as
  superseded, and the worker refuses a version below the order's. Attempts are counted at
  the claim.
- **A35 — The buyer edits through the directory's revision** (A12, A26).
  `contracts.SupplierEntry` carries the register row's revision, Purchasing's supplier
  read answers it, and the buyer's form takes its values and revision from that read, so
  `accounting:suppliers-manage` alone edits a supplier from Purchasing.
- **A36 — The agents' role template is deferred** (A25). Delivery A adds no template for
  purchasing work (§14, question 11).

### Delivery B, as built (revision 5)

What delivery B built where it differs from the decisions above or where they were silent,
chosen closest to Inventory's own receipt screen. Each wins over what it names.

- **A37 — The GS1 reader is Purchasing's** (A21). Inventory reads no GS1 code:
  `srv/inventory/gs1.go` only prints GS1-128 labels, and its receipt screen scans item codes
  through the catalog. Nothing is moved: the receive screen reads the element string itself
  (`fe/lib/gs1.ts` — AIs 01, 10, 21, 11, 15, 17, raw with FNC1 or human-readable), the GTIN
  is looked up as the code, and the lookup tries a GTIN-14 without its leading zeros. The
  reader moves to the shell when a second screen reads one.
- **A38 — Scanning counts from zero** (P24, P30). The lines start prefilled with their
  remainders; the first scan clears every quantity and counts from there, as Inventory's
  line editor counts. A packaging's barcode adds that packaging's quantity, a GS1 code fills
  the lot and its dates, a different lot opening a split row.
- **A39 — The default location** (P24). `Stock.Locations` names a primary location only
  where the variant lies, so the default is the primary location holding it, else the
  location holding most of it, else the site's own node.
- **A40 — The order's state from its lines** (P18, P27, A16). An open order is received
  when every line is closed, marked delivered, or — a goods line — received in full, a
  closed line's remainder included; partially received when anything is received or
  delivered; sent otherwise. So an undo that leaves nothing received makes a received order
  sent, not partially received. A closed order stays closed.
- **A41 — The delivered mark is built with receipts** (P16, §8). *Mark delivered* and *Not
  delivered* on a text or service line, behind `purchasing:receive`, with the order's
  `expectedUpdatedAt`; a goods line is refused `line_not_deliverable`. The change log gains
  `receipt_posted`, `receipt_undone`, `line_delivered` and `line_undelivered`.
- **A42 — A receipt's idempotency and its retry** (A7, A11). The request hash is the order
  and the body without its id; a refused receipt records nothing, so the screen resends an
  `over_receipt` with the same id and `acceptOver`. A replay answers 201 with the first
  receipt. The whole receipt — the pool read, the lock and the ledger — is tried three times
  on a deadlock, a serialization failure, or a line's variant changed since it was read, then
  answers 409 `version_conflict`. No `expectedUpdatedAt`: two receipts queue on the order's
  row lock (§11). The undo retries the same way.
- **A43 — The receipt's facts** (P28). The number is a series of its own from 1, in
  `purchasing.counters` (`receipt`); the day is today in the business zone, never set by
  hand; the ledger's supplier reference is the delivery note. A receipt line keeps the order
  line's price and the order's currency; no endpoint answers them.
- **A44 — Inventory's refusals** (P29, A27). They keep Inventory's codes, each line by its
  index in the request — 400 for a line's input (`lot_required`, `lot_not_allowed`,
  `lot_unknown`, `lot_number_invalid`, `lot_dates_invalid`, `serial_quantity`,
  `line_invalid`, `location_unknown`, `quantity_invalid`, `lot_dates_differ`), 409 for the
  stock. An undo's are mapped from the reversal's movement lines, each serial expanded, back
  to the receipt's lines.
- **A45 — The events and Manufacturing's handler** (§7, A4). Both receipt events carry the
  order id as their aggregate, published under the order's lock, with the order line,
  variant and quantity per line and no text. `manufacturing.purchasing-replenish` handles
  `purchasing.order.sent.v1`, `purchasing.receipt.posted.v1` and — beyond A4 —
  `purchasing.receipt.undone.v1`, which changes incoming as much: under the run requests'
  advisory lock it inserts a `manual` run for the site, requested by *Purchasing*, unless a
  pending run covers the site. No migration of Manufacturing's.
- **A46 — The receive screen's reads** (P30). The screen's read and the code lookup are
  `purchasing:receive`; the receipts' reads are `purchasing:access`. None answers a price.

### Delivery B, reviewed (revision 6)

From the review of delivery B. Each wins over what it names.

- **A47 — Receiving under `purchasing:receive` alone** (P30, §8, A46). Receiving needs no
  other key, and nothing it answers carries a price to a caller without
  `purchasing:access`: Receive and Undo answer `PurchasingReceiptResult` with the receipt,
  the order's `progress` (`PurchasingOrderProgress`: id, number, status, `updatedAt`, each
  line's quantity, received, open and delivered and closed marks) and `order` — the full
  view with its prices — only for a holder of `purchasing:access`; the delivered mark
  answers `PurchasingDeliveredResult` (`progress`, and `order` likewise). A read of its own,
  `GET /receiving` (`listPurchasingOrdersToReceive`), lists the orders to receive: sent and
  partially received orders with a goods line still to come, with the supplier's name, the
  site, the earliest expected day of what is to come, `late` and the lines left — no
  prices. The app gets a sidebar entry **Receive** (nb *Motta*) for that key alone; the
  receiving list is `/purchasing/receive` and the receive screen moves under it,
  `/purchasing/receive/{orderId}`, so the route guard's rule is the key's; the app's tile
  is offered to either key, and its prefix sends a receiver who cannot read orders to the
  list, where the receive screen also returns after a receipt.
- **A48 — One rule ends an order** (P18, A16, A40). Whichever action leaves nothing open —
  a receipt, a delivered mark, Close line, a Reopen that opened nothing — the order is
  `received` when every line came in full or was marked delivered, and `closed` when a
  closed line kept a remainder (less received than ordered, no delivered mark), with the
  change log's `closed` and `purchasing.order.closed.v1`, as Close. Its `closed_at` is the
  latest such line's, so Reopen opens that line again. Otherwise it is
  `partially_received` when anything is received or delivered, else `sent`; Reopen derives
  its state the same way, delivered marks counted. The end — `received` or `closed` — is a
  change-log row of its own after the action's, in every path. A40's received order with a
  closed line's remainder is closed instead.
- **A49 — The receipt line's price** (P28, A43). `receipt_lines.unit_price` is per the
  variant's unit — the unit its `quantity` is in —: the order line's price per its purchase
  packaging divided by the packaging's quantity, net of the line's discount, at four places,
  in the order's currency. Delivery D's invoice match and a later valuation start from it.


### Delivery C, as built (revision 7)

What delivery C built where it differs from the decisions above. Each wins over what it
names. Numbered from A50, so delivery B's amendments, written beside it, cannot collide.

- **A50 — The need before Purchasing's rounding** (P31, A4). Each provider answers its need
  rounded to the placement's multiple — Inventory's `needed` taken up to the multiple,
  Manufacturing's stored quantity, which its rule already rounded so — and never to a
  packaging. The multiple is the placement's own, which both providers apply alike;
  Purchasing then rounds once to the supplier item: whole purchase packagings when the item
  names one the variant still has (else three decimals of the unit), then up to the minimum,
  then up to a whole number of the multiple.
- **A51 — One source by a supersede list, and the time a run started** (P31, P32, A4,
  A28). `Deps.PurchaseDemand` is one value, not a list: `Module.PurchaseDemandSupersedes`
  lets Manufacturing name Inventory, and of the enabled declarers less the superseded at
  most one remains. P32's "the larger quantity wins" applies only to two rows one source
  answers for one (variant, site). Manufacturing's computed time is its run's `started_at`
  — before the run read the open purchases — not the suggestion's insert time, so an order
  sent while a run reads is subtracted twice at worst, never missed; Inventory's is the
  clock read before its open purchases.
- **A52 — Create orders' idempotency on the change log** (A11). *Superseded by A58:* the
  request table below replaces what follows. No migration: the
  `clientRequestId` and the request's hash are kept on the `from_proposals` action of the
  change-log row each draft it touched is given, never answered in the history. A replay is
  found by a JSON containment read of `order_changes`, before the transaction and again
  under the advisory lock that serialises Create orders. A draft deleted since takes its
  rows with it, and a replay then creates again. The read has no index of its own — one
  (a GIN index on `order_changes.actions`, or a request table) needs a migration, which
  delivery C was not to take; it is an owner's call once the change log grows. The screen
  keeps one request id per set of rows sent, so a click again after a lost answer
  replays rather than refusing.
- **A53 — A second click is refused, not trimmed** (P33, A4). Inside the transaction —
  variants locked, then suppliers, then the advisory lock — every row is judged again on the
  drafts as they stand; a row no longer proposed refuses the whole request with 409
  `proposal_gone`, its rows in `lines`, and the screen loads the list again. A person's
  quantity is theirs: a row still proposed may be ordered for more or less than proposed.
  *Amended by A58:* "still proposed" is now "remaining exactly what the screen showed".
- **A54 — Adding to a draft** (P33). The lines go to the oldest draft of the supplier and
  site; a variant line of the draft in the item's packaging takes the added quantity, and
  any other row is a new line planned as the editor's (`planLines`), so its price follows
  A32. `sentSince` counts, per order sent to the site after the computed time and not
  cancelled, a closed line's received quantity and an open line's larger of ordered and
  received; drafts count their lines' quantity whoever the supplier.
- **A55 — The options are the active suppliers' items** (P33). An item of a supplier
  inactive in the register is no option; a variant with none is listed under *No supplier*.
  The day to order by is the day needed, or today in the business zone, less the lead time.
- **A56 — Order… is the host's link to the filtered proposals** (P35). Inventory's
  `LowStockPage` and Manufacturing's `SuggestionsPage` take a render slot the host fills with
  Purchasing's `OrderFromProposalsLink`, shown when Purchasing is enabled and the caller
  holds `purchasing:access` with `purchasing:orders-manage`.
- **A57 — The demo's proposals** (plan, *The demo seed*). Three of the workshop's raw
  materials get a reorder point above what is held and on order, and a replenishment run
  for every site runs after the purchase orders are sent, so the proposals have rows from
  the first seed. No `assistTargets` entry is added: the plan names none for C.
- **A58 — The review's fixes to Create orders** (A11, A32, A53). *Stale screens:* each row
  carries the `remaining` the screen showed (required); under the advisory lock a row
  whose remaining, from the demand read for the request and the drafts and sent orders as
  they stand, is not exactly that is refused 409 `proposal_gone`, the rows in `lines`, and
  the screen loads again — so a screen loaded before another person's order, or before the
  demand moved, orders nothing more than it showed. *Replays:* migration
  `00191_purchasing_proposal_requests` adds `purchasing.proposal_requests` — the
  `clientRequestId` as key, the body's hash, the result (per draft its order, supplier and
  site ids, `created`, `linesAdded`, `unpricedRows`; no names), the caller's user id and
  the time — written in the Create orders transaction. A replay answers the stored result
  as it was, whatever happened to the drafts since, and creates nothing, whole or partial;
  the same id with another body stays 409 `client_request_reused`. The hidden
  `requestId`, `requestHash` and `created` fields of the change log's `from_proposals`
  action are gone. The table holds nothing personal of a supplier's, so the supplier's
  erase and export do not read it; its rows are kept. *Prices:* a line takes the item's
  price only in a draft of the item's currency — a new draft is in the supplier's, an
  existing one keeps its own — and the result's `unpricedRows` names, per draft, the rows
  whose new line so got none. *Sent since:* counts goods lines only, as `OpenPurchases`
  does, and from the computed time less `sentSinceMargin` (five seconds), so a replica's
  clock skew cannot hide an order sent just after a run read the supply. *Also:* a
  Discontinued goods variant's row has no option; the business calendar failing answers
  503; the list is unpaged and Create orders reads the demand again (known limits).
- **A59 — Manufacturing's run on send arrives with delivery B** (A4, A45, A51). The
  `manufacturing.purchasing-replenish` handler — which queues a site's replenishment run on
  `purchasing.order.sent.v1` and on a receipt posted or undone, so the stored buy
  suggestions follow a purchase rather than wait for the next run — is delivery B's, on
  which delivery C is built. Until the run it queues has finished, an order sent after the
  last run is netted by `sentSince` (A54, A58) alone.

### Delivery D, as built (revision 9)

What delivery D built where it differs from the decisions above or where they were silent,
chosen so Accounting stays the owner of the invoice and Purchasing the owner of the match.
Each wins over what it names.

- **A70 — A match is keyed by the invoice line's position** (P37, A9). Accounting writes a
  draft's lines anew on every save (`writeDraftLines`) and Reverse and copy copies them in
  order, so a line's id does not last and its position does: `invoice_matches` is unique on
  (supplier invoice, line position). An invoice line bills one order line; an invoice
  whose one line bills several order lines is entered with a line per order line, as
  *Register invoice* makes it. A match whose position the posted invoice no longer has is
  released by the worker of A72.
- **A71 — The allocation's refusals** (P37, P38, A14). More than a line's billable quantity
  — a goods line's received, a text or service line's quantity once marked delivered — less
  what is invoiced is refused with `over_invoiced` unless the request says `acceptOver`,
  as a receipt's `over_receipt` (P26); the match then warns *invoiced more than received*.
  An invoice line matched to another order line is `invoice_line_matched` (the same order
  line again takes the new quantity); another supplier's order is
  `invoice_supplier_differs`; another currency than the order's is
  `invoice_currency_differs` — so *currency differs* (P38) shows only when a draft's
  currency changes after its match; a reversed invoice is `invoice_reversed`; a draft or
  cancelled order `order_not_sent`. *Match all received* pairs the invoice's unmatched lines
  in order with the order's lines to invoice in order; a credit note is matched line by
  line.
- **A72 — The handlers read no contract; a worker reads the posted lines** (P5, P37, P39,
  A9, A15, A22; rule 23). `purchasing.invoice-posted` fixes the matches from the event
  alone — its supplier, number, date and currency —, releases those whose order is no
  longer the invoice's supplier's (§11), and queues the invoice in `invoice_followups`;
  the worker `purchasing.invoice-posted` reads the posted invoice through `Invoice` on the
  pool, writes each match's posted net, releases a match whose line is gone, and sets the
  last prices. `purchasing.invoice-reversed` releases the matches and gives the copy the
  same ones, provisional — posted when the copy's posting was handled first. Each handler
  takes an event once by its id (`invoice_events`), and an invoice's row in
  `invoice_states` is its lock and its known state, so a posting delivered after its
  reversal changes nothing and a match made while the invoice is posted cannot be left
  provisional.
- **A73 — Register invoice's total and the books' setup** (A1). Accounting posts a draft
  only when its lines' gross makes its total, and the supplier's default tax code adds VAT
  Purchasing does not know: the dialog takes the invoice's total as printed, and without it
  the draft's total is the lines' net, for the person to correct. The action is offered
  whatever the books' state — Purchasing reads no setup flag — and refused with
  `books_not_set_up`. A replay answers 201 with the first draft; two replays racing to one
  supplier number are told apart by Accounting's `duplicate_supplier_invoice`, the loser
  answering the winner's draft. The draft's description is *Innkjøpsordre N* or *Purchase
  order N* by the order's language; each line's text is the order line's name with the
  quantity and unit.
- **A74 — The differences are judged from what Purchasing keeps** (P38, A9, A20). A match
  keeps the invoice line's net and the invoice's number, date and currency as last read —
  at the match, then the posted line's — so the badge's `Summary`, the panel and the
  order's invoices judge without calling Accounting back. *Price differs* compares the
  invoice line's net with what the order makes of the same quantity, so a unit price's
  rounding is no difference. The panel and the order's invoices are read under
  `purchasing:access`, and `Summary` answers no matches to a caller without it. A posted
  invoice's match may be unmatched.
- **A75 — The last price's guards** (P39, A9). The price is the posted line's net over the
  matched quantity in the supplier item's packaging, read from the catalog. It is not set
  when the invoice is in another currency than the item, or when the item's price was set
  after the invoice's date — by a person, an import or a later invoice —, nor by a credit
  note; an item that does not exist is not created.
- **A76 — Received, not invoiced** (P41). Received as of a day is what receipts dated on or
  before it brought in less what was undone before it ended; a text or service line counts
  when marked delivered by then; invoiced is what posted matches of invoices dated by the
  day bill, credit notes taken off. A reversal deletes the reversed invoice's matches, so a
  list as of a day before a later reversal reads as the matches stand. The CSV's columns
  are named in English, as Inventory's.
- **A78 — `invoiced` is never floored** (A10, A14, A15; from the code review). Every write
  judges, under the order's lock, that no line is left invoiced below nothing: lowering or
  unmatching an invoice's match a credit note offsets is `credit_exceeds_invoiced`; Reverse
  and copy moves the matches to the copy without moving `invoiced`; a draft whose kind
  changed after its match is released at its posting; and a handler or the worker that
  cannot refuse leaves a match it cannot release. Register invoice judges its plan again
  under the lock and matches each draft line for what is still to invoice. Not done: a
  draft orphaned by a crash between Accounting's commit and Purchasing's has no matches,
  and a replay of its request is refused as `duplicate_supplier_invoice` (§14, question 14).
- **A77 — The demo's match comes after the books** (plan, *The demo seed*). A supplier
  invoice is Accounting's and the books take none before their setup, so delivery D's part
  of the seed runs after Accounting's, not before it.

### Delivery D, after its review (revision 10)

From the independent code review of delivery D. Each wins over what it names.

- **A79 — A deleted draft lets its matches go** (P5, P37, A15, A72). Deleting a draft
  supplier invoice published nothing, so its provisional matches stood for ever: the
  order stayed invoiced, Register invoice and Match all found nothing, the receipt's undo
  was refused and Unmatch answered 404. Accounting now publishes
  `accounting.supplier_invoice.deleted.v1` from the delete, on its transaction under the
  draft's row lock, carrying what the posted event carries and no free text (rule 23).
  Purchasing's `purchasing.invoice-deleted` marks the invoice `deleted` in `invoice_states`
  and releases its matches — a person's draft, Register invoice's, and the copy Reverse
  and copy made alike. A match a credit note holds stays (A78); the panel, the order's
  invoices and Unmatch then work from Purchasing's own rows, the panel's state `deleted`,
  until none is left. A match of an invoice Purchasing knows deleted is 404.
- **A80 — A posting Purchasing has not taken is read forward** (P37, A72). A match decided
  posted or provisional from `invoice_states` alone, made `open` on its first lock, and the
  dispatcher gives a handler no event fanned out before it: an invoice posted before
  delivery D, or before its event was handled, was matched provisional for ever — counted
  as not invoiced and never followed up. A match of an invoice Accounting reads as posted
  while its row is `open` now does what the posted handler does — the row `posted`, the
  matches fixed, the invoice queued — under the same lock, and the event taken later
  changes nothing. A reversed invoice needs no such step: it is refused
  (`invoice_reversed`) and its event releases what it has.
- **A81 — A reversal follows the copy's own copy** (A15, A72). The dispatcher orders events
  per aggregate, the invoice, so a copy's reversal may be taken before its original's. The
  reversed handler now follows `invoice_states.copy_id` from a copy already reversed to its
  copy, and on, and moves the matches to the first that is not; a deleted copy, or a chain
  that ends reversed, takes none and the matches are released. No match is lost to the
  order events are taken in.
- **A82 — Register invoice's replay and its locked order** (A11, A73). Two replays of one
  request racing: the loser's draft is refused by Accounting as a duplicate before the
  winner has recorded the request, and it answered 409. The loser now waits — up to five
  seconds — for the winner's record and answers its draft. No advisory lock spans the
  operation: it would hold a Purchasing connection while Accounting's `CreateDraft` takes
  another, and a pool of one would wait on itself. A replay answers the figures the first
  answer gave, recorded with the request (`invoice_registrations.total`), not a sum of the
  matches as they stand. The order is judged again on its locked row: an order cancelled
  since — or a draft whose deletion Purchasing has taken — gets the request recorded and no
  match, the draft being the person's to match or delete. §14's question 14 stands for a
  replay whose first died between the two commits.
- **A83 — The panel without `accounting:read`** (A20, A74; §14, question 15). The panel's
  read is `purchasing:access`, yet it answered Accounting's header, line count and the
  suggested orders' totals for any invoice id. A caller without `accounting:read` is now
  answered without asking Accounting: the header from what Purchasing keeps of the matches,
  the matched quantities, no lines, amounts, reference or suggestions, and 404 for an
  invoice with no match. The writes answer the panel by the same rule.
- **A84 — An invoiced line keeps its delivered mark** (P16, A10, A71). Taking the delivered
  mark off a text or service line leaves nothing of it billable; while `invoiced` is above
  0 it is refused `receipt_invoiced`, as a receipt's undo is.
- **A85 — The last price's guard on the invoice's own day, and the rest of the review**
  (P39, A75). A price a person or an import set on the invoice's own date once Purchasing
  took the posting — the item's `updated_at` later than the matches' `posted_at` — stands,
  so a second run of the worker changes nothing a person did after the first; one set that
  day before the posting, or by an invoice, is replaced, as P39 has it. The worker logs its last failed attempt at ERROR. `differences`, on the panel and in
  the badge, counts a line's own warnings — invoiced more than received or ordered — once
  per order line, however many matches bill it. The panel is read again after a refused
  write, a reversed or deleted invoice still offers Unmatch, and the allocation's quantity
  and Register invoice's amounts take a decimal comma. No index is added: the review's
  queries read what they need by the existing keys, or all of a small table.

## 16. Revision log

- **Revision 10, 2026-11-17** — delivery D's code review: A79 to A85 (§15, *Delivery D,
  after its review*); §14's question 15 added.

- **Revision 9, 2026-11-17** — delivery D built and reviewed: A70 to A78 (§15, *Delivery
  D, as built*); §14's question 14 added.

- **Revision 8, 2026-11-17** — delivery C reviewed: A58 and A59 added; A52 superseded and
  A53 amended by A58; §8's `purchasing:access` row names the proposals; §14's question 13
  added.

- **Revision 7, 2026-11-17** — delivery C built: A50 to A57 (§15, *Delivery C, as built*); numbered apart from delivery B's, which was built beside it.

- **Revision 6, 2026-11-17** — delivery B reviewed: A47 to A49 (§15, *Delivery B,
  reviewed*); §14's question 12 added.

- **Revision 5, 2026-11-17** — delivery B built: A37 to A46 (§15, *Delivery B, as built*).

- **Revision 4, 2026-11-17** — delivery A built and reviewed: A29 to A36 (§15, *Delivery
  A, as built*); §14's questions 9 to 11 added.

- **Revision 3, 2026-11-17** — delivery 0 built and reviewed: A22 to A28 (§15, *Delivery 0,
  as built*); §14's question 8 added.

- **Revision 2, 2026-11-17** — the critic review: A1 to A21 (§15); §14's question 1
  sharpened and question 7 added; §13's drop-shipment row widened.
- **2026-11-17, the owner** — question 1 answered: the books are kept in Vantigo.
- **Revision 1, 2026-11-17** — the first draft.
