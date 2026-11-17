---
title: "Purchasing module"
description: "The buy side: the module and the three modules it requires, the seven permissions, the settings, supplier items — one per supplier and variant, the preferred item, the currency flag and the price-list import —, purchase orders — the header and its defaults, lines and their arithmetic, the states and every transition with its refusal, the concurrency token, the change log, Late and Unconfirmed —, the number series, Send, Mark as sent and Send again with their idempotency, the PDFs and the mail, Close, Close line, Reopen, Cancel, the confirmation and Copy; the variant and supplier holders and the supplier's personal data; the events; contracts.PurchaseSupply; the two workers; the contracts read; the dashboard figures; the refusal codes and every endpoint with its permission and status codes."
sources:
  - apps/server/internal/purchasing
  - openapi/purchasing.yaml
  - apps/server/internal/openapi/specs/purchasing.yaml
  - apps/server/internal/db/migrations/00189_purchasing_baseline.sql
  - apps/server/internal/contracts/events_purchasing.go
---
The Purchasing module keeps what the business buys: what each supplier sells of the
catalog's variants (supplier items), and purchase orders with their lines, their PDF,
their mail, the supplier's confirmation and every change made after sending. In
Norwegian the app is **Innkjøp**. The design is
[`2026-11-17-purchasing-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-17-purchasing-design.md)
(decisions P1–P41, amendments A1–A36); this page describes delivery A, the module,
supplier items and purchase orders (P8–P23, A11, A16–A19). Receipts (delivery B),
proposals (C) and the supplier-invoice match (D) come later: nothing in delivery A
receives, so `partially_received` and `received` are not reached yet.

A purchase order moves neither stock nor the ledger. Stock moves when goods are received,
through Inventory's `StockLedger.Receive`; the books when the supplier invoice is posted,
in Accounting.

## The module

The module is `purchasing`, its schema `purchasing` (migration
`00189_purchasing_baseline.sql`), its API `/api/v1/purchasing` from
`openapi/purchasing.yaml` and its frontend `@vantigo/purchasing-ui`. It reads:

- the suppliers through `contracts.SupplierDirectory` (Accounting's register),
- the catalog through `contracts.ProductCatalog` (Products),
- the sites through `contracts.Stock` (Inventory),
- the company — the seller's name, address and profile for the PDF and the default
  delivery address — through `contracts.SalesTax`,
- the business time zone through the calendar contract, falling back to UTC when it is
  not composed, and Norway's public holidays for a business day when it is,
- the mail channel and the send through `contracts.OutboundMail` (Communications),
  optional,
- the holders of `purchasing:orders-manage` (the buyers) through the users contract.

`MODULES` refuses `purchasing` without `accounting`, `inventory` or `products`, naming
the one missing, and the module refuses to mount without the supplier directory, the
catalog or the stock. Should one be missing at run time, an operation that reads it
answers **503** `dependency_unavailable` and changes nothing. Communications is optional:
without it, or without a channel that can send, an order is marked as sent instead.
PDFs are kept in the object store, under the scope `purchasing`, keyed by their hash.

The schema holds `settings` (one row), `counters` (one per number series),
`supplier_items`, `orders`, `order_lines`, `order_changes` (the change log), `documents`
(the stored PDFs), `mailings` (every send) and `deletions` (the stored PDFs an erase
wrote off, for the [deletions worker](#workers)). Suppliers, variants, packagings, sites
and users are referenced by id, with no foreign key across schemas.

## Permissions

Seven keys, all in the category *Purchasing*, none sensitive, every one delegable but the
settings'. An endpoint is checked against the key named in [Endpoints](#endpoints); a
write does not also ask for `purchasing:access`.

| Key | Display | Lets a person |
| --- | --- | --- |
| `purchasing:access` | Use Purchasing | Read everything: orders, their lines and prices, the PDFs, the history, supplier items, the suppliers, the sites, the buyers, the meta and the figures. |
| `purchasing:orders-manage` | Manage purchase orders | Create, save and delete drafts; save sent orders; record the confirmation; Close, Close line, Reopen; Copy. |
| `purchasing:orders-send` | Send purchase orders | Send, Mark as sent, Send again, Cancel. |
| `purchasing:receive` | Receive goods (coming) | Declared for delivery B; no operation asks for it yet, and its description says so. |
| `purchasing:supplier-items-manage` | Manage supplier items | Create, save, delete and import supplier items. |
| `purchasing:invoices-match` | Match supplier invoices (coming) | Declared for delivery D; no operation asks for it yet, and its description says so. |
| `purchasing:settings` | Manage Purchasing settings | Read and save the settings. Not delegable. |

Every holder of `purchasing:access` sees the prices (design §14, question 5).

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
| `priceTolerancePercent` | 0–100, two decimals; for the match of delivery D. | 2 |

A save sends the `revision` it read; another is 409 `version_conflict`.

## Supplier items

`supplier_items`: what one supplier sells of one variant, one row per (supplier,
variant) — a second is 409 `supplier_item_exists`.

- `supplierItemNumber` (60 characters) and `supplierItemName`, optional.
- `price` per the purchase packaging, at most four places, ≥ 0, in `currency` — the
  supplier's currency in the register when the price was written. `priceSetOn` is the
  day, `priceSetBy` `person` (with the user), `import` or, in delivery D, `invoice`.
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
| open | Close line | same, `closed` or `received` | 404 for a line not on the order; 409 `line_closed` |
| `closed` | Reopen | `sent` or `partially_received` | 409 `order_not_closed` from any other state |
| `sent` | Cancel | `cancelled` | 409 `order_has_receipts` when anything is received or delivered; 503 `sending_disabled` when the supplier is to be notified and nothing can send |

An operation that needs an open order answers 409 `order_not_sent` from any other state:
Send again, the confirmation, Close, Close line and Cancel (a draft is deleted, not
cancelled). Send and Mark as sent of an order that is not a draft is 409
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
- **Close line** (`orders-manage`) ends one line. When no line stays open the order ends:
  `received` when every line was received in full or marked delivered, with the change
  log's `received` and no event — nothing was left to stop counting —, else `closed` with
  what was left, the change log's `closed` and the close event, as Close.
- **Reopen** (`orders-manage`) of a closed order opens again the lines that close ended —
  those with the order's `closed_at` — and sets `sent`, or `partially_received` when any
  line has something received.
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
  with what each snapshotted of it, where each was mailed, and each order's change log.
- *Erase*, inside Accounting's anonymisation transaction, blanks the name, address,
  e-mail and contact snapshotted on the supplier's orders, the mailings' addresses,
  messages and last errors, and the from and to values of the change log's actions on
  `supplierContact`, `note` and `internalNote` (keeping that the field changed); marks the
  orders `anonymised_at`, sets `documents.erased_at` and writes each stored PDF's key to
  `deletions`. The objects are deleted after the commit by the
  [`purchasing.deletions`](#workers) worker, never inside the transaction.

## Events

Published through the outbox on the change's own transaction, while it holds the order's
row lock (`contracts/events_purchasing.go`). A payload carries ids, numbers, dates, the
currency and the net total — no name, address or note (rule 23). None has a consumer in
this phase.

| Event | When | Payload |
| --- | --- | --- |
| `purchasing.order.sent.v1` | Send, Mark as sent | `orderId`, `number`, `supplierId`, `siteId`, `currency`, `netTotal`, `orderDate`, `expectedOn` (optional), `sendMethod` (`mail` or `manual`), `sentAt` |
| `purchasing.order.closed.v1` | Close, and a last Close line that leaves the order closed | `orderId`, `number`, `supplierId`, `siteId`, `closedAt` |

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

Quantities are exact decimal text in the variant's unit. It reads on the module's pool,
outside any transaction, and checks no user permission. With Purchasing off the readers
see nothing on order.

## Workers

| Worker | Every | What it does |
| --- | --- | --- |
| `purchasing.mail` | 30 s | Sends the mailings a request queued and did not get out — refused, or the request died after its commit: claims one due row at a time under a two-minute lease (a `SKIP LOCKED` pick), counting the attempt; stores the order's PDF and points the row at it when it names none yet; reads it; sends it through `OutboundMail` on the row's channel — else the settings' — and marks it `sent`. A failure waits a minute, doubled per attempt, at most an hour; the eighth attempt, or a row that can never be sent (no channel, no recipient, an order's mailing whose order is cancelled, a version below the order's — superseded —, an erased PDF), is `failed`. Up to 10 rows a cycle. |
| `purchasing.deletions` | 1 min | Deletes from the object store the stored PDFs an erase wrote to `deletions`, oldest first, up to 50 a cycle; an object already gone counts as deleted, and a row that failed 8 times is left, logged as an error. |

Every statement runs on its own pooled connection, and no call out of the module is made
while one is held: both are safe on a pool of one, which a test runs them on, built as
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
| 400 | `supplier_not_found`, `site_not_found`, `site_archived`, `currency_invalid`, `series_start_too_low`, `send_channel_unknown`, `recipient_missing`, `order_empty`, `import_invalid`, `amount_out_of_range` (the total); a line's `too_many_lines`, `line_invalid`, `line_not_found`, `variant_not_found`, `variant_discontinued`, `packaging_not_found`, `quantity_invalid`, `price_invalid`, `discount_invalid`, `quantity_below_received`, `amount_out_of_range` |
| 404 | `not_found` |
| 409 | `version_conflict`, `order_not_draft`, `order_not_sent`, `order_not_closed`, `order_has_receipts`, `line_closed`, `line_kind_changed`, `currency_fixed`, `supplier_inactive`, `supplier_changed`, `supplier_item_exists`, `client_request_reused` |
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
| `GET /orders/{orderId}/history` | access | 200; 404 |
| `GET /orders/{orderId}/pdf` | access | 200; 404; 503 |
| `GET /orders/{orderId}/documents/{documentId}` | access | 200; 404 |
| `GET /supplier-items` | access | 200; 400 |
| `POST /supplier-items` | supplier-items-manage | 201; 400; 404; 409; 503 |
| `GET /supplier-items/{itemId}` | access | 200; 404 |
| `PUT /supplier-items/{itemId}` | supplier-items-manage | 200; 400; 404; 409; 503 |
| `DELETE /supplier-items/{itemId}` | supplier-items-manage | 204; 404 |
| `POST /supplier-items/import` | supplier-items-manage | 200; 400; 503 |
| `GET /settings` | settings | 200 |
| `PUT /settings` | settings | 200; 400; 409 |

The two PDF downloads and the import are not offered to the Assistant
(`x-vantigo-assistant: never`). Every operation, with its request, its responses and the permission it is checked
against, is in the generated API reference (`/en/reference/api/purchasing/`), built from
the contract.
