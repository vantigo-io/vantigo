---
title: "Purchasing module"
description: "The buy side: the module and the three modules it requires, the seven permissions, the settings, supplier items — one per supplier and variant, the preferred item, the currency flag and the price-list import —, purchase orders — the header and its defaults, lines and their arithmetic, the states and every transition with its refusal, the concurrency token, the change log, Late and Unconfirmed —, the number series, Send, Mark as sent and Send again with their idempotency, the PDFs and the mail, Close, Close line, Reopen, Cancel, the confirmation and Copy; the proposals — the purchase demand's one source, the netting of drafts and of what was sent since, the rounding once to the supplier item — and Create orders with its idempotency and lock order; the variant and supplier holders and the supplier's personal data; the events; contracts.PurchaseSupply; the two workers; the contracts read; the dashboard figures; receipts — the receive screen and the scanned code, Receive on one transaction with StockLedger.Receive, its lock order, retry and idempotency, more and less than ordered, the order's status, the receipts read, Undo through StockLedger.ReverseReceipt, and the delivered mark on a text or service line —, the receipt events and Manufacturing's handler of them; the invoice match — the match tables, what is billable, the suggestion by reference, allocate, Match all received and unmatch with their refusals, the panel and the order's invoices with what accounting:read shows, the differences, Register invoice through SupplierInvoices.CreateDraft with its idempotency, the handlers of Accounting's posted, reversed and deleted events, the last price, received not invoiced and its CSV, and the SupplierInvoiceMatches badge —; the refusal codes and every endpoint with its permission and status codes."
sources:
  - apps/server/internal/purchasing
  - openapi/purchasing.yaml
  - apps/server/internal/openapi/specs/purchasing.yaml
  - apps/server/internal/db/migrations/00189_purchasing_baseline.sql
  - apps/server/internal/db/migrations/00190_purchasing_receipts.sql
  - apps/server/internal/db/migrations/00191_purchasing_proposal_requests.sql
  - apps/server/internal/db/migrations/00192_purchasing_matches.sql
  - apps/server/internal/contracts/events_purchasing.go
---
The Purchasing module keeps what the business buys: what each supplier sells of the
catalog's variants (supplier items), and purchase orders with their lines, their PDF,
their mail, the supplier's confirmation and every change made after sending. In
Norwegian the app is **Innkjøp**. The design is
[`2026-11-17-purchasing-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-17-purchasing-design.md)
(decisions P1–P41, amendments A1–A49 and from A50); this page describes delivery A, the
module, supplier items and purchase orders (P8–P23, A11, A16–A19), delivery B,
[receipts](#receipts) (P24–P30, A4–A7, A10, A11, A21, A27, A37–A49), delivery C, the
[proposals](#proposals) (P31–P35, A4, A11), and delivery D, [the invoice
match](#the-invoice-match) (P36–P41, A1, A9–A11, A14, A15, A20, A70–A76).

A purchase order moves neither stock nor the ledger. Stock moves when goods are received,
through Inventory's `StockLedger.Receive`; the books when the supplier invoice is posted,
in Accounting.

## The module

The module is `purchasing`, its schema `purchasing` (migration
`00189_purchasing_baseline.sql`), its API `/api/v1/purchasing` from
`openapi/purchasing.yaml` and its frontend `@vantigo/purchasing-ui`. It reads:

- the suppliers through `contracts.SupplierDirectory` (Accounting's register),
- the catalog through `contracts.ProductCatalog` (Products),
- the sites, their locations and where a variant lies through `contracts.Stock`
  (Inventory), and the stock ledger — `Receive` and `ReverseReceipt` — through
  `contracts.StockLedger`, on Purchasing's own transaction (rule 24),
- the company — the seller's name, address and profile for the PDF and the default
  delivery address — through `contracts.SalesTax`,
- the business time zone through the calendar contract, falling back to UTC when it is
  not composed, and Norway's public holidays for a business day when it is,
- the mail channel and the send through `contracts.OutboundMail` (Communications),
  optional,
- the holders of `purchasing:orders-manage` (the buyers) through the users contract,
- what should be bought through `contracts.PurchaseDemand` ([Proposals](#proposals)).
- Accounting's supplier invoices — read, and a draft made from an order — through
  `contracts.SupplierInvoices` ([the invoice match](#the-invoice-match)).

It fills `contracts.PurchaseSupply`, `contracts.SupplierInvoiceMatches` (the badge on
Accounting's supplier invoices) and the supplier and variant holders, and handles
Accounting's `accounting.supplier_invoice.posted.v1`, `…reversed.v1` and `…deleted.v1`.

`MODULES` refuses `purchasing` without `accounting`, `inventory` or `products`, naming
the one missing, and the module refuses to mount without the supplier directory, the
catalog or the stock. Should one be missing at run time, an operation that reads it
answers **503** `dependency_unavailable` and changes nothing. Communications is optional:
without it, or without a channel that can send, an order is marked as sent instead.
PDFs are kept in the object store, under the scope `purchasing`, keyed by their hash.

The schema holds `settings` (one row), `counters` (one per number series),
`supplier_items`, `orders`, `order_lines`, `order_changes` (the change log), `documents`
(the stored PDFs), `mailings` (every send), `deletions` (the stored PDFs an erase
wrote off, for the [deletions worker](#workers)), `receipts` and `receipt_lines`
(migration `00190_purchasing_receipts.sql`, [below](#the-receipt-tables)),
`proposal_requests` (Create orders' requests and their results, [Create
orders](#create-orders)), and `invoice_matches`, `invoice_states`, `invoice_events`,
`invoice_followups` and `invoice_registrations` (migration
`00192_purchasing_matches.sql`, [the match tables](#the-match-tables)). Suppliers, variants, packagings, sites
and users are referenced by id, with no foreign key across schemas.

## Permissions

Seven keys, all in the category *Purchasing*, none sensitive, every one delegable but the
settings'. An endpoint is checked against the key named in [Endpoints](#endpoints); a
write does not also ask for `purchasing:access`.

| Key | Display | Lets a person |
| --- | --- | --- |
| `purchasing:access` | Use Purchasing | Read everything: orders, their lines and prices, the PDFs, the history, supplier items, the suppliers, the sites, the buyers, the meta, the figures and the proposals. |
| `purchasing:orders-manage` | Manage purchase orders | Create, save and delete drafts; Create orders from proposals; save sent orders; record the confirmation; Close, Close line, Reopen; Copy. |
| `purchasing:orders-send` | Send purchase orders | Send, Mark as sent, Send again, Cancel. |
| `purchasing:receive` | Receive goods | Receive goods against purchase orders, undo a receipt, and mark a text or service line delivered: the receiving list, the receive screen, the code lookup, Receive, Undo and the delivered mark. It needs nothing else — no `access`, no `orders-manage`, and no Inventory permission — and none of these answers a price to a caller without `access` (A47). |
| `purchasing:supplier-items-manage` | Manage supplier items | Create, save, delete and import supplier items. |
| `purchasing:invoices-match` | Match supplier invoices | Allocate, Match all received and unmatch; Register invoice, which also needs `accounting:post`; received not invoiced and its CSV. Reading a match needs only `purchasing:access`. |
| `purchasing:settings` | Manage Purchasing settings | Read and save the settings. Not delegable. |

Every holder of `purchasing:access` sees the prices (design §14, question 5). A holder of
`purchasing:receive` alone sees none: the receiving list and the receive screen carry no
price, and Receive, Undo and the delivered mark answer the order's `progress` — its
state, `updatedAt` and each line's quantities — and the full order, with its prices,
only to a holder of `purchasing:access` (A47).

`GET /meta` answers what the screens need without the settings key: `sendingAvailable`
(Communications composed and the settings' channel able to send), the base currency
(`NOK`), the default site, the price tolerance, `canManageSuppliers`
(`accounting:suppliers-manage`, or `accounting:post` with `accounting:read` — the rule
of Accounting's supplier writes, A12, A25) and `canOpenAccountingSuppliers`
(`accounting:read`).

## Settings

One row, created by the first save; before it the defaults answer at revision 0.

| Field | Rule | Default |
| --- | --- | --- |
| `orderSeriesStart` | ≥ 1. Once a number is used it may only be set at or above the next number (400 `series_start_too_low`, judged under the settings' lock). | 1 |
| `defaultSiteId` | An Inventory site, active (400 `site_not_found`, `site_archived`). | none |
| `sendChannelId` | A Communications channel to send from, judged before the transaction (400 `send_channel_unknown`). | none |
| `orderFooter` | The text under every order, at most 4000 characters. | empty |
| `deliveryAddresses` | Per site, at most 200, one per site (`site_not_found` for an unknown one): name and two lines (255 characters each), postal code (20), city (100), and country, a two-letter code — what the order's delivery snapshot holds (400 with the field). | none |
| `priceTolerancePercent` | 0–100, two decimals: how far an invoice line's net may differ from the order's before the match warns `price_differs` ([the differences](#the-differences)). | 2 |

A save sends the `revision` it read; another is 409 `version_conflict`.

## Supplier items

`supplier_items`: what one supplier sells of one variant, one row per (supplier,
variant) — a second is 409 `supplier_item_exists`.

- `supplierItemNumber` (60 characters) and `supplierItemName`, optional.
- `price` per the purchase packaging, at most four places, ≥ 0, in `currency` — the
  supplier's currency in the register when the price was written. `priceSetOn` is the
  day, `priceSetBy` `person` (with the user), `import` or `invoice` ([the last
  price](#the-last-price)).
- `packagingId`: none (the variant's unit) or one of the variant's packagings (400
  `packaging_not_found`). When the packaging is deleted later the item answers
  `packagingMissing` and is read in the unit.
- `leadTimeDays` (0–3650), `minimumQuantity` and `multiple` (each > 0, in purchase
  packagings), optional.
- `preferred`: at most one per variant, kept by a partial unique index. The variant's
  first item is preferred whatever the request says. `preferred: true` on another item
  takes the preference from the one that had it. `false` never removes it: the preference
  moves only when another item is made preferred. Deleting the preferred item makes the
  variant's oldest remaining item preferred.
- `currencyDiffers`: the supplier's currency in the register is no longer the price's
  (§11). The price stays until a person writes it, which sets it in the supplier's
  currency then.

Every write locks the variant first through `ProductCatalog.LockVariants` (rule 21);
creating an item holds the supplier `FOR SHARE` through `SupplierDirectory.LockSuppliers`
(rule 28); the variant's items are serialised by an advisory lock while the preference is
judged. A save sends the item's `revision` (409 `version_conflict`). Deleting an item does
not touch the order lines that took from it.

### The price-list import

`POST /supplier-items/import?supplierId=…&dryRun=…` reads a CSV of one supplier's items
through `internal/fileimport` — UTF-8 or Windows-1252, the separator `;`, `,` or tab
detected — synchronously, with no job table. At most 5000 rows. A price takes a decimal
point, or a decimal comma with the Norwegian groupings `1 234,50` (a space, a
non-breaking or a narrow one) and `1.234,50`; `1.234` stays a decimal point. The header
names, in any order and case:

| Column | |
| --- | --- |
| `sku` or `barcode` | required: a SKU, or a variant's or a packaging's barcode — a packaging's barcode also sets the packaging |
| `supplier_item_number`, `supplier_item_name` | text |
| `price` | per the purchase packaging, four places |
| `packaging` | a packaging's name |
| `lead_time_days`, `minimum_quantity`, `multiple` | numbers |

A file without a `sku` or `barcode` column, or that cannot be read, is 400
`import_invalid`; an unknown supplier 400 `supplier_not_found`. Every row is resolved
before any transaction, and answers its outcome: `created` or `updated`, or
`variant_not_found`, `variant_ambiguous`, `packaging_not_found`, `price_invalid`,
`quantity_invalid`, `lead_time_invalid`, `duplicate_row` or `line_invalid`. `dryRun`
defaults to true and writes nothing. A commit writes every row, or none when any row
fails. A row creates the supplier's item for the variant or updates it; an empty cell
keeps the item's value; a price written is set by `import`.

## Purchase orders

### The header

- **Supplier**: an active supplier of the register (400 `supplier_not_found`, 409
  `supplier_inactive`). While the order is a draft the supplier's name and address follow
  the register; Send snapshots them.
- **Site** (`siteId`): the receiving site — the request's, else the settings' default,
  else the only active site, else 400 `site_not_found`; an inactive site is 400
  `site_archived`.
- **Currency**: the supplier's by default, ISO 4217 (400 `currency_invalid`). It may
  change on a draft until a line has a unit price above 0 (409 `currency_fixed`); a sent
  order keeps its own (409 `currency_fixed`).
- **Language**: `nb` when the supplier's country is `NO` or empty, else `en`; it may be
  changed.
- **Buyer** (*our reference*): the creator by default; `GET /buyers` lists the active
  holders of `purchasing:orders-manage`. The order date, an expected day, the supplier's
  contact as text, a note printed on the order and an internal note.
- **Delivery address**: the settings' address for the site when set, otherwise the
  company's address (`SalesTax.Seller`, `SellerProfile`) under the site's name. A draft
  answers it computed live; Send snapshots it.

Create locks the supplier `FOR SHARE` (`SupplierDirectory.LockSuppliers`, A8) after
`ProductCatalog.LockVariants` and before the module's own rows; so does a save that
changes a draft's supplier, and Send.

### Lines

At most 500 per order (400 `too_many_lines`). A line's refusal names its index in
`lines` with its code, every failing line at once, and nothing is saved; the problem's
own `code` is the first line's.

- **A variant line** names a variant (400 `variant_not_found`). A Goods variant whose
  product is Discontinued is refused on a new line (400 `variant_discontinued`); a
  Service variant is ordered. Every field the request leaves out comes from the supplier
  item of (supplier, variant): the supplier's item number, the price — only when the
  item's currency is the order's —, the packaging while it is still one of the
  variant's (400 `packaging_not_found` for one that is not), and the expected day, the
  order date plus the lead time. The item's price is per its own packaging: a line in
  another packaging takes `round4(price × the line's packaging quantity / the item's)`
  (the unit counting 1), and a price of 0 when either quantity is not known or the result
  is above `numeric(18,4)`. A line's `supplierItemNumber` is at most 60 characters (400
  `line_invalid`).
- **A text line**: description, unit (at most 20 characters, on a new line and a saved
  one alike, else 400 `line_invalid`), quantity, price.

`orderQuantity` is entered in the purchase packaging; `quantity`, in the variant's unit,
is `orderQuantity × packaging quantity`, to three places. `lineNet` is
`round2(orderQuantity × unitPrice × (1 − discount / 100))`; the order's net total is the
sum. Quantities > 0 (400 `quantity_invalid`), prices ≥ 0 (`price_invalid`), discounts
0–100 (`discount_invalid`); a malformed line is `line_invalid`. A line's net and the
order's total are `numeric(14,2)`: a line above 999 999 999 999.99 is the line refusal
`amount_out_of_range`, and a total above it the order's 400 `amount_out_of_range`. Every
amount is net of VAT in the order's currency: no tax code, no VAT and no conversion to NOK
(P17).

A line answers the warnings `below_minimum` (under the supplier item's minimum) and
`off_multiple` (not a whole number of its multiple) — never a refusal.

### States

`draft` → `sent` → `partially_received` → `received`; `closed` and `cancelled` beside them.
*Open* is `sent` or `partially_received`.

| From | Operation | To | Refused with |
| --- | --- | --- | --- |
| — | Create | `draft` | 400 `supplier_not_found`, `site_not_found`, `site_archived`, `currency_invalid`, a line's code; 409 `supplier_inactive` |
| `draft` | Save (`PUT`) | `draft` | as Create; 409 `currency_fixed` |
| `draft` | Delete | — | 409 `order_not_draft` for any other state |
| `draft` | Send | `sent` | 400 `order_empty`, `recipient_missing`; 503 `sending_disabled`, `mail_failed` (the order is sent, its mailing queued) |
| `draft` | Mark as sent | `sent` | 400 `order_empty` |
| `sent`, `partially_received` | Save | same | 409 `supplier_changed`, `currency_fixed`, `line_closed`, `line_kind_changed`, `order_has_receipts` (the site, once something is received); 400 `line_not_found`, `quantity_below_received` |
| open | Send again | same | 503 `sending_disabled`, `mail_failed` (the version is counted, its mailing queued) |
| open | Record confirmation | same | — |
| open | Close | `closed` | — |
| open | Close line | same, `closed` or `received` ([the end](#the-end-of-an-order)) | 404 for a line not on the order; 409 `line_closed` |
| `closed` | Reopen | `sent` or `partially_received` (the lines' state, [the end](#the-end-of-an-order)) | 409 `order_not_closed` from any other state |
| `sent` | Cancel | `cancelled` | 409 `order_has_receipts` when anything is received or delivered; 503 `sending_disabled` when the supplier is to be notified and nothing can send |
| open | Receive | `partially_received`, `received` or `closed` ([the end](#the-end-of-an-order)) | 400 `line_not_receivable`, `quantity_invalid`, `packaging_not_found`, Inventory's line codes; 409 `over_receipt`, `client_request_reused`, `version_conflict`, Inventory's codes ([Receive](#receive)) |
| `partially_received`, `received`, `closed` | Undo a receipt | `sent` or `partially_received`; `closed` stays | 409 `receipt_undone`, `receipt_invoiced`, `insufficient_stock`, `location_unknown` |
| open | Mark delivered | same, `received` or `closed` ([the end](#the-end-of-an-order)) | 409 `line_not_deliverable`, `line_closed` |
| open, `received` | Not delivered | `sent` or `partially_received` | 409 `line_not_deliverable`, `line_closed` |

An operation that needs an open order answers 409 `order_not_sent` from any other state:
Send again, the confirmation, Close, Close line, Cancel (a draft is deleted, not
cancelled), the receive screen, Receive and the delivered mark (`received` too for taking
it off). Send and Mark as sent of an order that is not a draft is 409
`order_not_draft`. A save of a `closed`, `received` or `cancelled` order is 409
`order_not_sent`.

### Saving a sent order

A save sends the header and every line whole. On an open order it may add lines; change a
line's quantity, down to what is received (400 `quantity_below_received`), its price, its
discount and its expected day; and change the header's dates, the contact, the notes, the
buyer, the language and — while nothing is received — the site. It may not change the
supplier (409 `supplier_changed`) or the currency (409 `currency_fixed`); a line's kind,
variant and packaging stay (409 `line_kind_changed`); a closed line does not change (409
`line_closed`); a line of the order left out of the request is refused (400
`line_not_found`).

### Concurrency

Every order write sends `expectedUpdatedAt`, the `updatedAt` it read; every write moves
it, and one made from an older read is 409 `version_conflict`. Supplier items and the
settings carry a `revision` instead.

### The change log

`order_changes` keeps one row per change of a sent order and per send, mark as sent, send
again, confirmation, close line, close, reopen and cancel — the actor snapshotted, the
time, and a list of actions: `created`, `updated`, `line_added`, `line_changed`,
`line_removed`, `sent`, `marked_sent`, `sent_again`, `confirmed`,
`confirmation_cleared` (a confirmation saved with no reference and no day),
`line_closed`, `closed`, `received` (the last open line closed with everything received),
`reopened`, `cancelled`, each with its field, line, from and to where it has them. `GET /orders/{id}/history` answers it with the stored PDFs and the mailings.

### Late and Unconfirmed

Both are judged in the business time zone, on the order list, the order and the figures.

- **Late**: open, and an open line's expected day — its own, else the order's — is before
  today.
- **Unconfirmed**: open, no supplier's reference and no confirmed day, and sent before the
  start of the fourth business day back counting today — more than three business days
  ago. A business day is Monday to Friday, and with the business calendar composed, not a
  Norwegian public holiday. It is the buyer's attention item (P19).

### The list

`GET /orders` answers newest first, paged, filtered by `status` (any of them), `supplierId`,
`siteId`, `late`, `unconfirmed`, `mine` (the caller is the buyer), `open` (draft, sent,
partially received) and `q`, which matches the number, the supplier's name and the
supplier's reference — `%` and `_` as the characters they are.

## Numbers

One gap-free series, `order`, in `counters`, allocated on Send or Mark as sent under the
row's lock, from the settings' `orderSeriesStart`. A draft has no number; a number is
unique. The PDF asks the supplier to quote it on the invoice, in a line of its own: it is
the key of delivery D's match (P20).

## Send, Mark as sent and Send again

**Send** (`purchasing:orders-send`) needs a line (400 `order_empty`), a channel that can
send — Communications composed and the settings' channel able to (503 `sending_disabled`)
— and a recipient: the request's, else the supplier's e-mail in the register (400
`recipient_missing`). In one transaction: `LockVariants`, `LockSuppliers`, the order row,
the number, status `sent`, version 1, the supplier's and the delivery address's
snapshots, `send_method` `mail`, the change log, `purchasing.order.sent.v1` — and the
mailing, written `queued` with no document yet: a transactional outbox, so a send is
never acknowledged without its mail (A19). The row is held by the request for the
two-minute lease (`next_attempt_at`). After the commit the request claims it under that
hold and delivers it as the [`purchasing.mail`](#workers) worker does, outside any
transaction: the PDF rendered from the committed rows, stored and recorded in
`documents`, the mailing pointed at it, then the mail sent through
`contracts.OutboundMail` from the row's channel. A mail that does not go out — the
provider refuses it, or the store fails — leaves the order sent and its mailing `queued`
with a backoff, and answers 503 `mail_failed`; a request that dies after its commit
leaves the row for the worker once the hold runs out.

**Mark as sent** does the same with no mail: `send_method` `manual`, the number, the
snapshots, the stored PDF and the event.

**Idempotency** (A11): both take a `clientRequestId` and hash the operation, the order's
`expectedUpdatedAt` and the dialog's fields. A replay with the same id and body answers
the first result (200) with the mailing as it now stands — `sent`, or still `queued` for
the worker —, never sending again; another body under the same id is 409
`client_request_reused`.

**Send again** on an open order stores a new PDF at version + 1, titled *Revised purchase
order* / *Revidert innkjøpsordre*, and mails it unless `mail: false`, its mailing queued
in the version's transaction as Send's is. The same transaction fails, for good, the
order's still-queued mailings of earlier versions — `failed`, last error *Superseded* —,
so a superseded version is never mailed. It answers 503 `sending_disabled` when it is to
mail and nothing can send, and 503 `mail_failed` when the mail does not go out, with the
version counted and the mailing queued.

Every send takes an optional recipient, copies, a subject and a message. The cover mail
is plain text in the order's language: *Purchase order 4501 from …* / *Innkjøpsordre
4501 fra …*, *Revised purchase order …* / *Revidert innkjøpsordre …* with the version,
and *Cancelled purchase order …* / *Kansellert innkjøpsordre …*; the dialog's subject
replaces the subject and its message goes before the closing. The mail names our company
beside the channel's address and replies to its e-mail.

`mailings` keeps every send: kind (`order` or `cancellation`), version, recipient,
copies, subject, status `queued`, `sent` or `failed`, attempts (counted when a row is
claimed), the last error (the recipient's and the copies' addresses taken out) and who
sent it.

## PDFs

The PDF (`pdf.go`, maroto v2 with Noto Sans) is in Norwegian or English by the order's
language: our company from `SellerProfile`, as Orders' documents name it; the supplier's
snapshot; the delivery address; the number, date, our reference and the supplier's
contact; the lines net of VAT and the total; the line asking the supplier to quote our
order number on the invoice; *Amounts exclude VAT* / *Beløpene er uten mva.*; the note on
the order, the settings' text and the seller's footer.

`documents` holds one row per (order, kind, version), each stored once through the
storage port after the commit, keyed by hash. `GET /orders/{id}/pdf` answers the current
version's stored PDF; for a draft it renders one now, marked *DRAFT* / *UTKAST*, and stores
nothing (503 `dependency_unavailable` when a module it reads is missing).
`GET /orders/{id}/documents/{documentId}` answers a stored one. A revised order is titled
*Revised purchase order*; a cancellation *Cancelled purchase order* / *Kansellert
innkjøpsordre*, marked *CANCELLED* / *KANSELLERT*. A document whose supplier was erased
answers 404. Recording a PDF holds its order `FOR SHARE` and writes nothing for an
anonymised order: a PDF put while the supplier's erase ran is written to `deletions`
instead, answers 404, and is deleted by the worker.

## Close, Close line, Reopen and Cancel

- **Close** (`orders-manage`) of an open order sets `closed_at` on every open line,
  status `closed`, and publishes `purchasing.order.closed.v1`. What was left stops counting
  in [PurchaseSupply](#purchasesupply).
- **Close line** (`orders-manage`) ends one line. When no line stays open the order ends
  by [the end of an order](#the-end-of-an-order).
- **Reopen** (`orders-manage`) of a closed order opens again the lines that close ended —
  those with the order's `closed_at` — and sets the state the lines make: `sent`, or
  `partially_received` when any line has something received or is marked delivered.

### The end of an order

One rule ends an open order, whichever action leaves nothing open — a receipt, a
delivered mark, Close line, or a Reopen that opened nothing (A48). A line is done when it
is closed, marked delivered, or — a goods line — received in full. When every line is
done the order is:

- `received` when no closed line kept a remainder — every line came in full or was marked
  delivered —, with the change log's `received` and no event: nothing was left to stop
  counting;
- `closed` when a closed line kept a remainder (less received than ordered, no delivered
  mark), with the change log's `closed` and `purchasing.order.closed.v1`, as Close. Its
  `closed_at` is the latest such line's, so Reopen opens that line again.

Otherwise it is `partially_received` when anything is received or delivered, and `sent`
when nothing is. The end is a change-log row of its own, after the row of the action that
made it.
- **Cancel** (`orders-send`) of a `sent` order with nothing received or delivered (409
  `order_has_receipts`; a draft is deleted, 409 `order_not_sent`): status `cancelled`. An
  order that was mailed (`send_method` `mail`) mails the supplier its cancellation — the
  PDF marked *CANCELLED* — unless `notifySupplier` is false; one marked as sent is
  cancelled without a mail. The cancellation's mailing is queued in the cancel's
  transaction, as Send's is; a mail that does not go out answers 503 `mail_failed` with
  the order cancelled and the mailing queued.

## The confirmation

`PUT /orders/{id}/confirmation` (`orders-manage`) on an open order records the supplier's
order reference and the confirmed day, for the order and optionally per line. It is a
fact, no state; it ends Unconfirmed; empty values clear it. It is written to the change
log, as `confirmed`, or `confirmation_cleared` when it clears it.

## Copy

`POST /orders/{id}/copy` (`orders-manage`) makes a new draft from an order in any state,
for the same supplier and site, in the supplier's currency now. A line keeps its packaging
while the variant still has it and takes the supplier item's price in the currency —
converted to the line's packaging as a new line's is, when it can be —, else its own
when the currency is the same. The supplier's currency is read upper-cased. A goods line whose variant is gone or Discontinued
is left out; text lines are copied as they are. An inactive supplier is 409
`supplier_inactive`.

## Receipts

Delivery B (P24–P30): goods received against a sent order, into Inventory through
`contracts.StockLedger` on Purchasing's own transaction. One receipt is one Inventory
movement of kind `receipt`; an undo is one of kind `receipt_reversal`. Purchasing never
touches the general ledger: the books move when the supplier invoice is posted, in
Accounting.

### The receipt tables

`receipts` — one per receipt (P28):

| Column | |
| --- | --- |
| `number` | In a series of its own, `receipt` in `counters`, gap-free from 1, unique (`ux_receipts_number`). The first receipt makes the counter's row; the migration's down deletes it. |
| `order_id`, `site_id` | The order and its receiving site. |
| `received_on` | The day received. |
| `delivery_note` (100), `note` (1 000) | The supplier's delivery note number, as text, and a note. |
| `movement_id` | Inventory's movement of kind `receipt`. |
| `received_by_user_id`, `received_by_display` | Who received, the name snapshotted. |
| `client_request_id`, `request_hash` | The idempotency key, unique (`ux_receipts_client_request`), and the hash of the body (A11). |
| `undone_at`, `undone_by_user_id`, `undone_by_display`, `undo_note` (1 000), `reversal_movement_id` | Set by Undo; `undone_at` and `reversal_movement_id` are set together (`ck_receipts_undone`). |

`receipt_lines` — one per line of the request, by `position`:

| Column | |
| --- | --- |
| `order_line_id`, `variant_id` | The order line and its variant. |
| `location_id` | Where it was put. |
| `quantity`, `entered_quantity`, `in_packaging`, `packaging_name` | The quantity in the variant's unit, and as entered — in the line's purchase packaging when `in_packaging`, with the packaging's name then. Both > 0, three places. |
| `lot_id`, `lot_number`, `supplier_lot`, `production_date`, `best_before`, `expiry` | The lot as the request named it: by id, or by number with its dates and supplier lot; `lot_number` null when Inventory numbered it. |
| `serials` | A serial variant's serials. |
| `unit_price`, `currency` | The order line's price at the time, per the variant's unit — the line's price per its purchase packaging divided by the packaging's quantity —, net of the line's discount, four places, and the order's currency (P28, A49): the figure a later valuation, and the invoice match, start from. Nothing reads it in this phase and no endpoint answers it (A21). |

Sites, locations, variants, lots and movements are Inventory's and Products' ids, with no
foreign key across schemas; a receipt references its order and a line its order line,
`ON DELETE RESTRICT`.

### The receiving list

`GET /receiving` (`receive`) is the orders to receive (P30, A47): every `sent` or
`partially_received` order with a goods line still to come — not closed, less received
than ordered —, the earliest expected first (the line's expected day, else the order's;
none last), at most 500. Each carries the order's id, number, status, supplier's name,
site and its name, `expectedOn`, `late` (a line still to come expected before today in the
business zone), `goodsLines` and `openLines`. No price is read.

### The receive screen

`GET /orders/{orderId}/receive` (`receive`) answers what the screen needs, with no prices
(A21): the order's number, status, supplier's name, site and `updatedAt`; the site's
active locations; and each open goods line — not closed, of a Goods variant — with its
SKU, name, unit, ordered, received and `open` (the remainder, in the variant's unit),
its purchase packaging and the packaging's quantity, the variant's tracking, its lot
days (`bestBeforeDays`, `expiryDays`) and `defaultLocationId`: the variant's primary
location at the site when it holds stock there, else the location holding most of it,
else the site's own node. An order that is not open is 409 `order_not_sent`.

`GET /orders/{orderId}/receive/code?code=` (`receive`, 1–100 characters) reads a scanned
code through `ProductCatalog.LookupCode` (P30): a variant's or a packaging's barcode, or
the SKU; a GTIN-14 is tried again without its leading zeros. It answers the order's open
goods lines of the variant the code names, by position, each with the packaging matched
(none for the variant's own code) and `quantity`, what one scan adds in the variant's
unit — the packaging's quantity, or 1. A code that names nothing on the order answers an
empty list, which the screen shows as *Not on this order*. The screen reads a GS1-128
string itself (`apps/purchasing/frontend/src/lib/gs1.ts`, A37): its GTIN is looked up this
way, and its lot, serial and dates fill the row. It skips the known AIs it does not use —
fixed-length ones such as 00, 02, 13, 20, 310n–369n and 410–417, variable ones such as 30,
37 and 90–99. A lot or serial that runs to the end of a scan without FNC1, where what
follows could be further fields, or one after an AI the reader does not know, is reported
unread, and the screen asks for it rather than guess.

### Receive

`POST /orders/{orderId}/receipts` (`receive`) with `clientRequestId`, `lines` (1–500),
`deliveryNote`, `note` and `acceptOver`. Each line names `orderLineId`, `quantity` — at
most three places, in the line's purchase packaging when `inPackaging`, else in the
variant's unit —, `locationId`, and for a tracked variant its lot one way: `lotId`, an
existing lot; `lot`, a lot by number found or made, with its supplier lot, production
date, best-before and expiry (an empty number lets Inventory number it, `L<n>`; a date
left out defaults from the product's days; a `lot` with nothing in it is no lot, which a
tracked variant refuses `lot_required`); or `serials` (at most 500, 20 characters each)
for a serial variant. An order line may appear on several lines — a split by
location or lot.

Purchasing's own refusals, each line by its index in `lines`: 400 `line_not_receivable`
(not an open goods line of the order — a text or service line, a closed line, another
order's), `quantity_invalid` (not above 0, more than three places), `packaging_not_found`
(`inPackaging` on a line with no purchase packaging), and `quantity_invalid` again for a
line whose received quantity would pass 999 999 999.999 — `acceptOver` or not —; 409
`order_not_sent` for an order that is not open.

**The rule of more** (P26): a line whose quantities add up to more than its remainder,
`quantity − received`, is refused 409 `over_receipt`, the lines named, unless
`acceptOver` is true. With it the line's `received` may exceed its `quantity`.

**The rule of less** (P27): the order stays `partially_received` with its remainder; no
back-order document is made.

**The order's status** after a receipt is [the end of an order](#the-end-of-an-order)'s:
`received` when every goods line is received in full — `received ≥ quantity` — and every
text and service line delivered; `closed` when everything is done and a closed line kept
a remainder; else `partially_received`.

**One transaction** (P25, A7). Before it, on the pool, the order's lines and variants and
the screen's contract reads. Inside it, in the lock order of rules 21 and 24: Products'
`LockVariants` on the order's variants; the order row `FOR UPDATE`, its lines checked
against what was read; the receipt counter's row; the receipt and its lines written, the
order lines' `received` raised, the order's status set; then Inventory —
`StockLedger.Receive`, one movement of kind `receipt` at the order's site whose document
is the receipt (`purchasing/receipt/<id>`) and whose source line is the order, which
Inventory's movement page links to; the movement's id written to the receipt; and the
[event](#events). Two receipts of one order queue on the order's row lock, and the second
sees the first's quantities. The engine's deadlock retry is not available on a caller's
transaction, so Purchasing retries the whole transaction on a deadlock, up to three
times, as Manufacturing's replenishment does; one that still fails is 409
`version_conflict` — the order kept changing while it was received.

What Inventory does with it is [Inventory's](/en/reference/inventory/#receiving-for-another-module):
a variant inspected on receipt has its lots quarantined, as on Inventory's own receipt
(A5); a **Discontinued** variant's open order is still received (A6). Inventory's
refusals keep Inventory's codes, each line by its index in the request's `lines`: 400
`lot_required`, `lot_not_allowed`, `lot_unknown`, `lot_number_invalid`,
`lot_dates_invalid`, `serial_quantity`, `line_invalid`, `location_unknown`,
`quantity_invalid`, `lot_dates_differ` (a lot by number that exists with other dates); 409
`variant_not_stocked`, `lot_blocked`, `site_archived`,
`site_unknown`, `document_reused`.

**Idempotency** (A11, A42): `clientRequestId` with the hash of the body, an `acceptOver`
of false hashed as none. A replay with the same id and body answers the first receipt —
201, with the receipt and its order as they now stand, whatever the order became since:
received, closed, or the receipt undone — and posts nothing; a refusal judged before the
lock is answered only once the request is known to be no replay. Another body under the
same id is 409 `client_request_reused`. A refused request records nothing, so the screen, asked about
`over_receipt`, sends the same receipt again under the same id with `acceptOver`.

A receipt answers **201** with the receipt, the order's `progress` and — to a holder of
`purchasing:access` — the `order` (`PurchasingReceiptResult`, A47). It lowers
[PurchaseSupply](#purchasesupply) at once.

**The supplier on the movement** (P7): Inventory's movement names the supplier and the
delivery note only when the supplier is not a person; a sole proprietor's receipt posts
a movement without either, since Inventory's movements are not erased with a supplier's
personal data.

### The receipts read

`GET /orders/{orderId}/receipts` (`access`) answers the order's receipts newest first,
each with its lines; `GET /receipts/{receiptId}` (`access`) one. A receipt carries its
number, the order and its number, the site, `status` (`posted` or `undone`), the day,
who, the delivery note, the note, `movementId`, and once undone `undoneAt`, `undoneBy`,
`undoNote` and `reversalMovementId`; a line its order line, variant, SKU, name and unit,
the quantity and as entered, the location with its full code as Inventory answers it now
(empty when it no longer does), the lot and its dates, and the serials. No prices. The
order page links the movements to Inventory for a caller with `inventory:stock-view`
while Inventory is on.

### Undo

`POST /receipts/{receiptId}/undo` (`receive`) with an optional `note` (1 000) takes a
posted receipt back (P29, A5, A10, A16), in one transaction in Receive's lock order —
`LockVariants`, the order row, then Inventory — with the receipt's row locked:
`StockLedger.ReverseReceipt`
posts one movement of kind `receipt_reversal`, taking each of the receipt's lines out of
the location and lot it went into whatever the lots' holds, and releasing the holds that
receipt set (its inspection's quarantine); the lines' `received` go down by what the
receipt put on them; the receipt is kept, `undone_at`, who, the note and
`reversal_movement_id` written; and the event is published. The order becomes
`partially_received` when anything else on it is received or delivered, else `sent`; a
`closed` order stays `closed` (Reopen to receive again). It answers **200** with the
receipt, the order's `progress` and, to a holder of `purchasing:access`, the order.

Refused: 409 `receipt_undone` for a receipt undone already; `receipt_invoiced` when a
line would be left with less received than is invoiced on it, posted or provisional
(A10) — unmatch the invoice first —; and Inventory's `insufficient_stock` (the stock is no longer where it was
received), `location_unknown` (a location archived since) and `receipt_unknown` (no
movement of the receipt), each by its index in the receipt's lines — the reversal's
movement lines, a serial line expanded into one per serial, mapped back to them. `ReverseReceipt` is idempotent by the receipt's document, so there is
one reversal a receipt. An undo has no request id: a second undo of the same receipt is
`receipt_undone` and changes nothing.

### Delivered

A text line, or a line of a Service variant, is never received into stock (P16).
`POST /orders/{orderId}/lines/{lineId}/delivered` (`receive`) with `delivered` and the
order's `expectedUpdatedAt` sets or clears the line's delivered mark (`delivered_at`).
Marking needs an open order; taking the mark off, an open or `received` one (409
`order_not_sent`). When nothing stays open the order ends by [the end of an
order](#the-end-of-an-order), its `received` or `closed` logged as after a receipt;
taking the mark off a received order makes it `partially_received`, or `sent` when nothing
else has come. It answers **200** with the order's `progress` and, to a holder of
`purchasing:access`, the order (`PurchasingDeliveredResult`, A47).
Refused 409 `line_not_deliverable` on a goods line — it is received instead —,
`line_closed` on a closed line, `receipt_invoiced` for taking the mark off a line whose
`invoiced` is above 0 — nothing of it would be billable (A84) —, and `version_conflict`
for a stale `expectedUpdatedAt`.

## Proposals

What should be bought comes through `contracts.PurchaseDemand`, a slot with **one
source** (A4): where Manufacturing is enabled, its open *buy* suggestions — the last
replenishment run's, each its stored quantity, needed by its need-by day and computed at
the run's start; where it is not, Inventory's low-stock rows — each its need rounded to
the placement's multiple and not to a packaging, needed now, computed when it is read.
`module.Module.PurchaseDemandSupersedes` carries the precedence: Manufacturing's
provider supersedes Inventory's, and Compose and Workers resolve the one left (two left is
a failure). A make suggestion is never proposed. With no provider the list is empty and
Create orders answers 503 `dependency_unavailable`. A source answering two needs of one
(variant, site) keeps the larger.

`GET /proposals` (optionally `siteId`, `variantId`) answers one row per (variant, site)
at an active site whose variant the catalog knows, by site code, SKU and variant id:

- `need`, the source's quantity, and `computedAt`, `neededOn` and `source`
  (`replenishment` or `low_stock`);
- `onDrafts`, the quantity of the variant on Purchasing's drafts for the site, and
  `sentSince`, what orders sent to the site after `computedAt` hold of it in goods lines,
  as `PurchaseSupply` counts them — a closed line what it received, an open one the larger
  of ordered and received, a cancelled order nothing; an order sent before is already in
  the source's own figure, through `PurchaseSupply`. `computedAt` is taken back by
  `sentSinceMargin`, five seconds, before the read (A58), so a replica's clock a little
  ahead of the provider's cannot hide an order sent just after a run read the supply: an
  order sent in those seconds before is subtracted twice at worst, and less is proposed,
  never more;
- `remaining`, `need − onDrafts − sentSince`; a row with nothing remaining is left out;
- `options`, every supplier item of the variant whose supplier is active in the register,
  the preferred first, then by id. Each rounds `remaining` **once**: to whole purchase
  packagings when the item names a packaging the variant still has, else to three decimals
  of the unit; then up to the item's minimum; then up to a whole number of its multiple —
  `orderQuantity` in packagings and `quantity` in the unit. `price` is the item's in that
  packaging (`itemPriceFor`, A32), `currencyDiffers` when the item's currency is not the
  supplier's, and `orderBy` the day needed — or today in the business zone — less the
  lead time. A Discontinued goods variant has no option, whatever items it has (P16).

A row with no option is shown under *No supplier* and cannot be ordered. The list is not
paged: it answers every row of the demand at once, which is a known limit of this phase.
The business calendar failing is 503 `dependency_unavailable`.

### Create orders

`POST /proposals/orders` takes a `clientRequestId` and 1 to 500 rows of `variantId`,
`siteId`, `supplierItemId`, `orderQuantity` (in the item's packaging) and `remaining`, the
row's remaining as the screen showed it. Before the transaction it reads the items, the
sites, the demand again — the whole demand, or one site's when every row names the same
site, which is a known cost of this phase —, today and the caller; a row is refused in
`lines` by its index with `supplier_item_not_found` (not the variant's item),
`site_not_found`, `site_archived`, `quantity_invalid` (the quantity, or the `remaining`
sent), or `line_invalid` (a variant twice at a site). Inside one transaction it locks the
variants (`LockVariants`), then the suppliers (`LockSuppliers`, FOR SHARE), then takes an
advisory lock that serialises every Create orders, and then reads its own rows: each row's
remaining is computed again from the demand just read and the drafts and sent orders as
they stand, and a row whose remaining is not exactly the `remaining` sent — not in the
demand, nothing remaining, or another figure (A58) — refuses the whole request with 409
`proposal_gone`, its rows in `lines`. So a second click, a colleague's click, or a screen
loaded before the demand changed orders nothing twice and nothing more than was shown. A
person's quantity is still theirs: `orderQuantity` may be more or less than proposed. An
inactive supplier is 409 `supplier_inactive`.

The rows are grouped by supplier and site. Each group's lines go to the oldest draft of
that supplier and site, locked, or to a new draft in the supplier's currency and language,
dated today, with the caller as buyer and the delivery address computed as a draft's is.
A line of the variant already on the draft in the item's packaging takes the added
quantity; any other row is a new line, planned as the editor plans one — the item's
number, packaging, price in the order's currency only, and expected day — so a row's
refusal is a line's (`variant_discontinued`, `too_many_lines` past 500 lines, the draft's
lines counted). A new line takes the item's price only when the item's currency is the
draft's (A32): a new draft is in the supplier's currency, an existing one keeps its own,
and a row whose new line so got no price is named in its draft's `unpricedRows`. The
total is summed again, and the draft's `updatedAt` moves, so an editor holding the draft
as read before is refused `version_conflict` on save. Each draft is given a change-log
row: `created` for a new one, and `from_proposals` with the number of lines it touched.

It is idempotent by its `clientRequestId` (A11, A58). In the same transaction it writes a
row of `purchasing.proposal_requests`: the id (its key), the request's hash, the result
— per draft its order, supplier and site ids, `created`, `linesAdded` and
`unpricedRows`, no names —, the caller's user id and the time. A replay with the same
rows answers that stored result as it was answered, whatever happened to the drafts
since — one deleted or sent is still named, and nothing is created again, whole or in
part —, each supplier named as the register names it now; the same id with other rows is
409 `client_request_reused`. A replay is checked before the transaction and again under
its advisory lock. The table holds nothing personal about a supplier, so the supplier's
erase and export do not read it. The answer, 200, is `orders` — per draft its id,
supplier, site, `created`, `linesAdded` and `unpricedRows`.

## The invoice match

A supplier invoice is Accounting's; the match is Purchasing's (P36–P41, A1, A9–A11, A14,
A15, A20, A70–A85). Purchasing reads an invoice through `contracts.SupplierInvoices.Invoice`
on the pool — its header, its state, its reference and each line's net as Accounting
computes it — and makes one only through `CreateDraft` ([module-boundaries rule
26](/en/contributing/module-boundaries/)). Migration `00192_purchasing_matches.sql` adds the
tables.

### The match tables

| Table | What it holds |
| --- | --- |
| `invoice_matches` | One row per supplier-invoice line matched: the invoice's id and the line's **position** — unique together —, the order and the order line, `kind` (`invoice` or `credit_note`), the quantity in the order line's unit (more than 0), the invoice line's net, the invoice's number, date and currency as last read, `state` (`provisional` or `posted`, with `posted_at`) and who matched it. |
| `invoice_states` | What Purchasing learnt of an invoice from Accounting's events — or from a match that read it posted (A80): `open`, `posted`, `reversed` or `deleted`, and the copy a reversal made. Its row is the invoice's lock. |
| `invoice_events` | Each handled event by its id. |
| `invoice_followups` | The posted invoices the [worker](#workers) has to finish. |
| `invoice_registrations` | Register invoice's `clientRequestId`, its request hash, the order, the draft it made, its line counts and the net total the first answer gave. |

A match is keyed by the line's position, not its id: Accounting writes a draft's lines anew
on every save and Reverse and copy copies them in order (A70). An invoice line bills one
order line; an invoice may bill several orders of its supplier, and an order line several
invoice lines.

`order_lines.invoiced` is the sum of the line's matches, an invoice's adding and a credit
note's taking away, provisional and posted alike (A10, A11), written only under the order's
row lock by the match, its handlers and its worker; each judges first that no line is left
below 0, so it always equals the sum of the line's matches. Every write of an
invoice's matches locks the invoice's `invoice_states` row first, then the orders in id
order, then their lines.

### What is billable

A goods line's billable quantity is its `received`; a text or service line's is its
quantity once it is marked delivered, else nothing. *To invoice* is billable less
`invoiced`, never below 0. The order's net per unit is the line's net over its quantity,
the discount taken; what a quantity comes to at it is rounded to two places, and is the
line's net exactly when the quantity is the whole line's.

### The suggestion

An invoice suggests the orders of its supplier — `sent`, `partially_received`,
`received` or `closed` — whose number is one of the whole digit runs of its `reference`
(P36), each with its count of lines to invoice.

### Allocate, Match all received and unmatch

`POST /supplier-invoices/{invoiceId}/matches` (`invoices-match`) takes an `orderId` and up
to 200 `lines` — the invoice line's `linePosition`, the `orderLineId` and a `quantity` with
at most three decimals — and `acceptOver`. Judged on the invoice as read and the order
under its lock:

- 404 `not_found` for an unknown invoice or order; 400 in `lines` `invoice_line_unknown`
  (no line at the position, or named twice), `quantity_invalid`, `line_not_found` (not the
  order's);
- 409 `invoice_reversed` (also when Purchasing has seen its reversal), `order_not_sent` (a
  draft or a cancelled order), `invoice_supplier_differs`, `invoice_currency_differs`;
- 409 `invoice_line_matched`, the lines named, for a position matched to another order line
  — the same order line again takes the new quantity;
- 409 `credit_exceeds_invoiced`, the lines named, for a credit note — or an invoice's
  lower quantity — that would take a line's `invoiced` below 0, whatever `acceptOver` says
  (A14, A78);
- 409 `over_invoiced`, the lines named, for an invoice that would take a line's `invoiced`
  above its billable quantity, unless `acceptOver`.

An invoice Accounting reads as `posted` while its `invoice_states` row is still `open` —
posted before delivery D, or its event not yet handled — is moved forward first, as the
posted handler would ([below](#accountings-events)) (A80). A match of an invoice Purchasing
knows to be posted is written `posted` and the invoice queued for the worker; any other is
`provisional`. An invoice Purchasing knows to be deleted is 404. It answers the panel.

`POST /supplier-invoices/{invoiceId}/match-all` (`invoices-match`) with an `orderId` pairs
the invoice's unmatched positions in order with the order's lines that have something to
invoice, in position order, each for all of it, and allocates them as above without
`acceptOver`. A credit note, or nothing to pair, is 409 `nothing_to_match`.

`POST /supplier-invoices/{invoiceId}/unmatch` (`invoices-match`) with `orderId` and
`linePosition`, each optional, deletes the matches they name — all of the invoice's when
neither is given — and takes them off `invoiced`; none named is 409 `nothing_to_match`, and
an invoice's match a credit note offsets is 409 `credit_exceeds_invoiced` until the credit
note is unmatched. A
posted invoice's match may be unmatched too; a last price it set stays. An invoice
Accounting no longer has — a deleted draft whose match a credit note held (A79) — is
unmatched from Purchasing's own rows, and is 404 once none is left.

### The panel and the order's invoices

`GET /supplier-invoices/{invoiceId}/match` (`access`) answers, to a holder of
`accounting:read`, Accounting's invoice — its supplier, kind, number, date, currency and
state, its lines with their texts and nets, its reference —, its matches, the suggestions
and the count of the differences; 404 for an unknown invoice, 503 without Accounting.
A caller without `accounting:read` is answered without asking Accounting (A20, A83): the
header from what Purchasing keeps of the matches — the number, date, currency and kind they
were made with, the supplier of the first match's order, the state from `invoice_states`
(`draft` while `open`) —, the matches without amounts, no lines, no reference and no
suggestions; an invoice with no match is 404, so the read tells nothing of an invoice that
is not matched. An invoice Accounting no longer has but Purchasing still holds a match of
is answered the same way to anyone, its state `deleted` (A79). The writes answer the panel
by the same rule.
`GET /orders/{orderId}/invoices` (`access`) answers the order's matches. Each match carries
the order line, the quantity, the order's net per unit (four places), the state and its
warnings; a match's `lineNet` and `invoiceUnitNet` only to a holder of `accounting:read`
(`linesVisible`).

### The differences

Per match, judged on its order line as it stands (P38, A9):

| Warning | When |
| --- | --- |
| `invoiced_over_received` | the line's `invoiced` is above its billable quantity |
| `invoiced_over_ordered` | above its quantity |
| `currency_differs` | the invoice's currency is not the order's; the price is then not judged |
| `price_differs` | the invoice line's net differs from what the order makes of the matched quantity by more than the settings' `priceTolerancePercent` of it |

`differences` counts the warnings of the invoice's matches, a line's own —
`invoiced_over_received`, `invoiced_over_ordered` — once per order line however many
matches bill it; the badge counts the same.

The invoice line's net is Accounting's as last read: at the match, and the posted line's
once the worker has finished the posting — never an amount typed in Purchasing (A9). The
warnings hold nothing; Accounting posts what the person posts.

### Register invoice

`POST /orders/{orderId}/register-invoice` checks `purchasing:invoices-match` at the router
and `accounting:post` in the handler (403 `accounting_post_required`, A1). The body:
`clientRequestId`; `invoiceNumber`, 1–60 characters; `invoiceDate`; `dueDate`, not before
it; `exchangeRate` (more than 0, six decimals) or `totalNok`; and `total`, the invoice's
total as printed (more than 0, two decimals). 400 names a field.

1. A `clientRequestId` seen before answers its draft again — **201** — for the same order
   and body (the hash is the order and the body without the id), and is 409
   `client_request_reused` for another (A11).
2. The order must be sent, received or closed (409 `order_not_sent`). Its lines with
   something to invoice, in position order, at most 200 (A1), each with that quantity at the
   order's net; none is 409 `nothing_to_invoice`. Provisional matches count, so a second
   registration takes only what the first left.
3. `CreateDraft` on the pool, before Purchasing's transaction: the order's supplier,
   `invoice`, the number and the dates, the order's currency with the rate or the NOK
   total, the `total` — the lines' net when none is given (A73) —, the order number as
   `reference`, *Innkjøpsordre N* (or *Purchase order N* for an English order) as the
   description, and per line its name with the quantity and unit as text and its net,
   entered net with the supplier's default account and tax code. Accounting's refusals
   become 409 `books_not_set_up`, `supplier_account_missing` and
   `duplicate_supplier_invoice`, and a field rule 400 `supplier_invoice_invalid` with
   Accounting's fields — the same names as the request's.
4. One transaction: the draft's `invoice_states` row and the order locked, the request
   recorded, the lines read again, and one provisional match per draft line at its position
   for what is still to invoice — less when a match, an undo or another registration took
   part of it since, none when it took all. The order is judged again on its locked row: an
   order cancelled since, or a draft whose deletion Purchasing has handled, gets no match —
   the draft is the person's to match or delete (A82).

It answers **201** with the draft's id, its line count, `linesLeft` — the lines with
something to invoice beyond the 200 —, its lines' net total and the currency; a replay
answers the same figures, recorded with the request, whatever was matched or unmatched
since (A82). Two replays racing to one invoice number: the loser's `CreateDraft` is refused
as a duplicate, and it waits — up to five seconds — for the winner to record the request
and answers its draft. A Purchasing transaction is never held open across Accounting's,
so no advisory lock spans the two: a pool of one would wait on itself. A replay whose winner
died between the two commits is refused as `duplicate_supplier_invoice` (§14, question 14
of the design).

### Accounting's events

Three handlers, each on the delivery's own transaction, writing only this module's schema
and reading no contract (rule 23). Each records the event's id in `invoice_events` first
and does nothing for an id seen before.

- **`purchasing.invoice-posted`** on `accounting.supplier_invoice.posted.v1`: when the
  invoice's `invoice_states` row is `open`, marks it `posted`; releases the matches whose
  order's supplier is no longer the event's (§11), or made while the draft was of the other
  kind; fixes the rest — `posted`, with the
  event's number, date and currency —; and queues the invoice for the worker. A row already
  `posted` or `reversed` changes nothing, so a posting delivered after its reversal is
  ignored.
- **`purchasing.invoice-reversed`** on `accounting.supplier_invoice.reversed.v1`: marks the
  invoice `reversed` with its copy and moves its matches to the copy (`copyId`) at the same
  positions, `provisional` — `posted`, and queued, when the copy's own posting was handled
  first (A15) —, leaving `invoiced` as it was; a match at a position the copy has matched
  already, or with no copy, is released. Events of different invoices are taken in any
  order, so when the copy was reversed first its own copy takes the matches, and so on
  down the chain (A81); a copy Purchasing knows to be deleted, or a chain that ends
  reversed, takes none and the matches are released.
- **`purchasing.invoice-deleted`** on `accounting.supplier_invoice.deleted.v1`: marks the
  invoice `deleted` and releases its matches, provisional all — a draft's —, so what they
  counted is to invoice again (A79).

A handler or the worker never refuses: a release that would leave a line invoiced below
0 — an invoice's match a credit note offsets — is not made, and the match stays for a
person to unmatch (A78).

### The last price

The worker `purchasing.invoice-posted` reads the posted invoice through `Invoice` and the
matched variants through the catalog on the pool, then in one transaction under the
invoice's and the orders' locks: releases a match whose position the posted invoice no
longer has, writes each other match's posted net, and — for an invoice, not a credit note
— sets each matched variant's supplier item of the invoice's supplier to the posted line's
net over the matched quantity, times the item's packaging factor, four places, with
`priceSetBy` `invoice` and `priceSetOn` the invoice's date (P39, A9). An item in another
currency than the invoice's, whose price was set after the invoice's date, or set by a
person or an import on that date after the posting — the item's `updated_at` later than
the matches' `posted_at` (A85) —, is left; a price of the same date set before the posting,
or by an invoice, is replaced. Of two lines of one variant the later on the invoice wins. A missing item is not created.

### Received, not invoiced

`GET /received-not-invoiced` (`invoices-match`), with `asOf` — today in the business zone
when left out — and an optional `supplierId`, answers per order line of an order that is
not a draft or cancelled: what its receipts brought in on or before the day and had not
been taken back by the end of it — a text or service line's quantity when it was marked
delivered before the day ended —, less what posted matches of invoices dated on or before
the day bill, credit notes taken off, when that is more than 0; at the order's net per
unit, with the totals per currency (P41). Provisional matches do not count. The supplier's
name is the order's snapshot. `GET /received-not-invoiced/csv` answers the same as a file:
semicolons, a byte order mark, CRLF, the decimal comma and the formula guard on text, with
the columns `supplier_id`, `supplier`, `order`, `line`, `item`, `unit`, `received`,
`invoiced`, `not_invoiced`, `unit_net`, `amount`, `currency`.

### The badge

Purchasing fills `contracts.SupplierInvoiceMatches`: `Summary(ctx, invoiceIDs)` answers,
per invoice with a match, its orders — id and number — and the count of the differences,
a line's own warnings once per order line, read on the pool. It is provider-gated: a caller without `purchasing:access` gets
no matches; more than `contracts.MaxSuppliers` ids is a failure. Accounting shows it as
the [badge on its supplier invoices](/en/reference/accounting/#supplier-invoices).

## The holders

Purchasing answers inside other modules' transactions, on the caller's `tx`, with its own
SQL, composed whatever `MODULES` says (rules 20, 27, 28).

**`VariantReferences`** — Products' variant writes:

| Purpose | Held by | Kind |
| --- | --- | --- |
| delete | any supplier item or order line naming the variant | `open_documents`, with the count |
| type change (Goods → Service) | an open goods line of a draft, sent or partially received order | `open_documents` |
| tracking change | never | — |

**`SupplierReferences`** — Accounting's supplier writes, which lock the supplier `FOR
UPDATE` first:

| Purpose | Held by | Kind | Accounting answers |
| --- | --- | --- | --- |
| delete | any order, drafts included, or supplier item naming the supplier | `documents` | 409 `supplier_has_documents` |
| anonymise | a draft, sent or partially received order | `open_documents` | 409 `supplier_has_open_documents` |

**`SupplierPersonalData`** (A8):

- *Export* answers, for Accounting's export of a supplier, the orders naming the supplier
  with what each snapshotted of it, where each was mailed, each order's change log, and
  its receipts' delivery notes, notes and undo notes.
- *Erase*, inside Accounting's anonymisation transaction, blanks the name, address,
  e-mail and contact snapshotted on the supplier's orders, the mailings' addresses,
  messages and last errors, the receipts' `delivery_note`, `note` and `undo_note`
  (`purchasing.receipts` in its report), and the from and to values of the change log's
  actions on `supplierContact`, `note` and `internalNote` (keeping that the field
  changed); marks the
  orders `anonymised_at`, sets `documents.erased_at` and writes each stored PDF's key to
  `deletions`. The objects are deleted after the commit by the
  [`purchasing.deletions`](#workers) worker, never inside the transaction.

## Events

Published through the outbox on the change's own transaction, while it holds the order's
row lock (`contracts/events_purchasing.go`), and aggregated by the order's id. A payload
carries ids, numbers, dates, quantities, the currency and the net total — no name,
address, delivery note or note (rule 23). Manufacturing handles three of them
([below](#manufacturings-handler)).

| Event | When | Payload |
| --- | --- | --- |
| `purchasing.order.sent.v1` | Send, Mark as sent | `orderId`, `number`, `supplierId`, `siteId`, `currency`, `netTotal`, `orderDate`, `expectedOn` (optional), `sendMethod` (`mail` or `manual`), `sentAt` |
| `purchasing.order.closed.v1` | Close, and whatever leaves the order closed by [the end of an order](#the-end-of-an-order) — a last Close line, receipt or delivered mark | `orderId`, `number`, `supplierId`, `siteId`, `closedAt` |
| `purchasing.receipt.posted.v1` | Receive | the receipt's id and number, the order's id and number, the supplier, the site, the movement, the received day, and per line the order line, the variant and the quantity in the variant's unit |
| `purchasing.receipt.undone.v1` | Undo | the same, with the reversal's movement |

### Manufacturing's handler

Manufacturing's handler **`manufacturing.purchasing-replenish`** receives
`purchasing.order.sent.v1`, `purchasing.receipt.posted.v1` and
`purchasing.receipt.undone.v1` and queues a [replenishment
run](/en/reference/manufacturing/#replenishment) for the order's site when none is pending
for it, so the projection's open purchases follow the order. The run is of kind `manual`,
requested by *Purchasing*; with auto-create rules on, such runs may make manufacturing
orders more often than the nightly run would (design §14, question 12). It writes only
Manufacturing's own schema (rule 23); with Manufacturing off nothing handles them.

## PurchaseSupply

Purchasing fills `contracts.PurchaseSupply`, a single-provider slot that Inventory's
low-stock list, its count and the product's stock card read as *on order*, and
Manufacturing's replenishment as incoming. Per (variant, site): the sum of
`quantity − received` over the Goods variant lines of `sent` and `partially_received`
orders whose line is not closed and whose quantity is above what is received, with the
earliest expected day — the line's, else the order's. Drafts, closed and cancelled orders
count nothing.

- `OpenByVariants(ctx, siteID, variantIDs)`: the variants given, at the site when one is
  given; more than `contracts.MaxStockVariants` is a failure.
- `OpenAll(ctx)`: every (variant, site) with anything open.

A receipt lowers it at once, in its own transaction — `received` rises —; an undo raises
it again; Close and Close line end what was left. Quantities are exact decimal text in the variant's unit. It reads on the module's pool,
outside any transaction, and checks no user permission. With Purchasing off the readers
see nothing on order.

## Workers

| Worker | Every | What it does |
| --- | --- | --- |
| `purchasing.mail` | 30 s | Sends the mailings a request queued and did not get out — refused, or the request died after its commit: claims one due row at a time under a two-minute lease (a `SKIP LOCKED` pick), counting the attempt; stores the order's PDF and points the row at it when it names none yet; reads it; sends it through `OutboundMail` on the row's channel — else the settings' — and marks it `sent`. A failure waits a minute, doubled per attempt, at most an hour; the eighth attempt, or a row that can never be sent (no channel, no recipient, an order's mailing whose order is cancelled, a version below the order's — superseded —, an erased PDF), is `failed`. Up to 10 rows a cycle. |
| `purchasing.invoice-posted` | 30 s | Finishes the posted invoices the handler queued ([the last price](#the-last-price)): claims one due row at a time under a two-minute lease, counting the attempt; a failure waits a minute, doubled per attempt, at most an hour, and the eighth is left, logged at ERROR — its matches keep the draft's nets and no last price is set. Up to 20 a cycle. |
| `purchasing.deletions` | 1 min | Deletes from the object store the stored PDFs an erase wrote to `deletions`, oldest first, up to 50 a cycle; an object already gone counts as deleted, and a row that failed 8 times is left, logged as an error. |

Every statement runs on its own pooled connection, and no call out of the module is made
while one is held: they are safe on a pool of one, which a test runs them on, built as
`module.Workers` builds them.

## The dashboard figures

`GET /summary` answers `drafts`, `open` (sent or partially received), `late`,
`unconfirmed`, `mineUnconfirmed` (unconfirmed orders whose buyer is the caller) and
`failedMailings` (open orders whose last mailing failed).

## Refusals

Every refusal is an RFC 9457 problem, `PurchasingProblem`, with a `code`; a 400 carries
the field errors in `errors`, keyed by the request's camelCase field, and an order's line
refusals in `lines`. A request the server could not decode is a bare 400.

| Status | Codes |
| --- | --- |
| 400 | `supplier_not_found`, `site_not_found`, `site_archived`, `currency_invalid`, `series_start_too_low`, `send_channel_unknown`, `recipient_missing`, `order_empty`, `import_invalid`, `amount_out_of_range` (the total); a line's `too_many_lines`, `line_invalid`, `line_not_found`, `variant_not_found`, `variant_discontinued`, `packaging_not_found`, `quantity_invalid`, `price_invalid`, `discount_invalid`, `quantity_below_received`, `amount_out_of_range`; a receipt line's `line_not_receivable` and `quantity_invalid`, and Inventory's `lot_required`, `lot_not_allowed`, `lot_unknown`, `lot_number_invalid`, `lot_dates_invalid`, `serial_quantity`, `line_invalid`, `location_unknown`, `quantity_invalid`, `lot_dates_differ`; a proposal row's `supplier_item_not_found`; a match line's `invoice_line_unknown`; Register invoice's `supplier_invoice_invalid` |
| 403 | `accounting_post_required` (Register invoice) |
| 404 | `not_found` |
| 409 | `version_conflict`, `order_not_draft`, `order_not_sent`, `order_not_closed`, `order_has_receipts`, `line_closed`, `line_kind_changed`, `currency_fixed`, `supplier_inactive`, `supplier_changed`, `supplier_item_exists`, `client_request_reused`, `over_receipt`, `receipt_undone`, `receipt_invoiced`, `line_not_deliverable`, `proposal_gone`; Inventory's `variant_not_stocked`, `lot_blocked`, `site_archived`, `site_unknown`, `document_reused` on a receipt and `insufficient_stock`, `location_unknown` on an undo, each line by its index; the match's `invoice_reversed`, `invoice_supplier_differs`, `invoice_currency_differs`, `invoice_line_matched`, `over_invoiced`, `credit_exceeds_invoiced`, `nothing_to_match`, `nothing_to_invoice`, and Accounting's `books_not_set_up`, `supplier_account_missing`, `duplicate_supplier_invoice` |
| 503 | `dependency_unavailable`, `sending_disabled`, `mail_failed` |

## Endpoints

All under `/api/v1/purchasing`. 401 and 403 are the platform's. Every order write also
answers 409 `version_conflict` for a stale `expectedUpdatedAt`.

| Operation | Permission | Answers |
| --- | --- | --- |
| `GET /meta` | access | 200 |
| `GET /summary` | access | 200 |
| `GET /buyers` | access | 200 |
| `GET /sites` | access | 200; 503 |
| `GET /suppliers` | access | 200 — the register's suppliers with their supplier items and open orders counted; 503 |
| `GET /suppliers/{supplierId}` | access | 200 — the entry, read-only, with the register row's `revision`, which a buyer's change through Accounting's `PUT /accounting/suppliers/{id}` carries (A26); 404; 503 |
| `GET /variants` | access | 200 — a catalog search for a line, `onlySupplierItems` for the supplier's; 503 |
| `GET /orders` | access | 200; 400 |
| `POST /orders` | orders-manage | 201; 400; 404; 409; 503 |
| `GET /orders/{orderId}` | access | 200; 404 |
| `PUT /orders/{orderId}` | orders-manage | 200; 400; 404; 409; 503 |
| `DELETE /orders/{orderId}` | orders-manage | 204; 404; 409 |
| `POST /orders/{orderId}/send` | orders-send | 200; 400; 404; 409; 503 |
| `POST /orders/{orderId}/mark-sent` | orders-send | 200; 400; 404; 409; 503 |
| `POST /orders/{orderId}/send-again` | orders-send | 200; 400; 404; 409; 503 |
| `POST /orders/{orderId}/cancel` | orders-send | 200; 400; 404; 409; 503 |
| `POST /orders/{orderId}/close` | orders-manage | 200; 400; 404; 409 |
| `POST /orders/{orderId}/lines/{lineId}/close` | orders-manage | 200; 400; 404; 409 |
| `POST /orders/{orderId}/reopen` | orders-manage | 200; 400; 404; 409 |
| `PUT /orders/{orderId}/confirmation` | orders-manage | 200; 400; 404; 409 |
| `POST /orders/{orderId}/copy` | orders-manage | 201; 400; 404; 409; 503 |
| `POST /orders/{orderId}/lines/{lineId}/delivered` | receive | 200; 400; 404; 409 |
| `GET /receiving` | receive | 200; 503 |
| `GET /orders/{orderId}/receive` | receive | 200; 404; 409; 503 |
| `GET /orders/{orderId}/receive/code` | receive | 200; 400; 404; 503 |
| `GET /orders/{orderId}/receipts` | access | 200; 404 |
| `POST /orders/{orderId}/receipts` | receive | 201; 400; 404; 409; 503 |
| `GET /receipts/{receiptId}` | access | 200; 404 |
| `POST /receipts/{receiptId}/undo` | receive | 200; 400; 404; 409; 503 |
| `GET /orders/{orderId}/history` | access | 200; 404 |
| `GET /orders/{orderId}/pdf` | access | 200; 404; 503 |
| `GET /orders/{orderId}/documents/{documentId}` | access | 200; 404 |
| `GET /proposals` | access | 200; 503 |
| `POST /proposals/orders` | orders-manage | 200; 400; 409; 503 |
| `GET /supplier-items` | access | 200; 400 |
| `POST /supplier-items` | supplier-items-manage | 201; 400; 404; 409; 503 |
| `GET /supplier-items/{itemId}` | access | 200; 404 |
| `PUT /supplier-items/{itemId}` | supplier-items-manage | 200; 400; 404; 409; 503 |
| `DELETE /supplier-items/{itemId}` | supplier-items-manage | 204; 404 |
| `POST /supplier-items/import` | supplier-items-manage | 200; 400; 503 |
| `GET /orders/{orderId}/invoices` | access | 200; 404 |
| `POST /orders/{orderId}/register-invoice` | invoices-match, and `accounting:post` | 201; 400; 403; 404; 409; 503 |
| `GET /supplier-invoices/{invoiceId}/match` | access | 200; 404; 503 |
| `POST /supplier-invoices/{invoiceId}/matches` | invoices-match | 200; 400; 404; 409; 503 |
| `POST /supplier-invoices/{invoiceId}/match-all` | invoices-match | 200; 400; 404; 409; 503 |
| `POST /supplier-invoices/{invoiceId}/unmatch` | invoices-match | 200; 400; 404; 409; 503 |
| `GET /received-not-invoiced` | invoices-match | 200 |
| `GET /received-not-invoiced/csv` | invoices-match | 200 |
| `GET /settings` | settings | 200 |
| `PUT /settings` | settings | 200; 400; 409 |

The two PDF downloads, the import and the received-not-invoiced CSV are not offered to the Assistant
(`x-vantigo-assistant: never`). Every operation, with its request, its responses and the permission it is checked
against, is in the generated API reference (`/en/reference/api/purchasing/`), built from
the contract.

## API keys

`purchasing:settings` cannot be delegated, so no API key holds it, and the settings
(`getPurchasingSettings`, `putPurchasingSettings`) are `x-vantigo-api: never`. See
[integration users](/en/admin/authentication/#integration-users), and the marks' rules in
[module boundaries](/en/contributing/module-boundaries/#the-rules).
