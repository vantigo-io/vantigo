---
title: "Orders module"
description: "The quotes and sales orders made for customers: the module and the modules it requires, the nine permissions, the document header and its customer snapshot, lines, pricing and the arithmetic, the concurrency token, number series, the quote's states and revisions, accept, copy and templates, the order's life, versions and change log, delivery marked by hand, invoicing from orders — the allocations, the billable read, the invoiced-work holder and the draft-invoice rules —, PDFs, sending and the cover mail, the events, the handler and the four workers, the holders, retention, the customer summary, the sales warnings and their override, packagings on lines, the variant grid, the analysis views and their CSV files, the refusal codes, the assist targets and every endpoint with its permission and status codes."
sources:
  - apps/server/internal/orders
  - apps/orders/frontend
  - openapi/orders.yaml
  - apps/server/internal/openapi/specs/orders.yaml
  - apps/server/internal/db/migrations/00119_orders_baseline.sql
  - apps/server/internal/db/migrations/00120_orders_warnings.sql
  - apps/server/internal/db/migrations/00121_orders_allocations.sql
  - apps/server/internal/contracts/billable_orders.go
  - apps/server/internal/contracts/events_orders.go
  - apps/host/frontend/src/routes/orders
  - apps/host/frontend/src/routes/customers/-customer-orders-tab.tsx
  - apps/host/frontend/src/lib/orders-access.ts
---
The Orders module keeps the quotes and the sales orders the business makes for its
customers: their header and lines, their prices and totals, their PDFs, their sending, and
an order's life from draft to confirmed, delivered and cancelled. In Norwegian the app is
**Ordre**. The design is
[`2026-11-05-orders-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-05-orders-design.md)
(decisions O1–O54); this page describes delivery A, quotes and orders, delivery A2,
selling comfort — sales warnings, packagings and the variant grid, and the analysis
views — and delivery B, invoicing from orders ([Invoicing from orders](#invoicing-from-orders)).
Subscriptions (S), deliveries from stock (C) and shipping (D) come later and are
described as they land.

## The module

The module is `orders`, its schema `orders` (migration `00119_orders_baseline.sql`), its
API `/api/v1/orders` from `openapi/orders.yaml` and its frontend `@vantigo/orders-ui`. It
reads the customer through `contracts.CustomerDirectory`, the catalog through
`contracts.ProductCatalog`, and the VAT codes, the tax-category mapping and the seller
through `contracts.SalesTax`. `MODULES` therefore refuses `orders` without `products`,
`customers` or `invoices` (`orders requires products`, and so on), and the module
refuses to mount without any of the three contracts. Should one be missing at run time,
an operation that reads it answers **503** `dependency_unavailable` and changes nothing.
Communications is optional: without it, or without a send channel, nothing is e-mailed
and a document is downloaded and marked as sent instead. PDFs are kept in the object
store, under the scope `orders`.

The schema holds `settings` (one row), `counters` (one per number series), `quotes`,
`orders`, `templates`, `lines` (every document's and template's lines, by `owner_kind`
`quote`, `order` or `template`), `order_changes` (the change log), `documents` (the stored
PDFs), `mailings` (every send) and `erasures` (the customers whose confirmed orders wait for
their retention to run) — migration `00119` — and `customer_warnings` and
`product_warnings` (one sales warning per customer and per product, migration `00120`,
which also gives every line its `product_id` and `packaging_name` snapshots) — and
`invoice_allocations`, what invoices hold of each order line (migration `00121`,
[Invoicing from orders](#invoicing-from-orders)).

## Permissions

| Key | Display | Covers | Sensitive | Delegable |
| --- | --- | --- | --- | --- |
| `orders:access` | Use Orders | See the app; read quotes, orders, templates, the customer summary, the salespeople and the catalog search | no | yes |
| `orders:quotes-manage` | Manage quotes | Create, edit, send, mark as sent, revise, accept, decline, cancel, copy and delete quotes; apply a template | no | yes |
| `orders:sales-manage` | Manage orders | Create and edit orders, hold and release, cancel and cancel what remains, register and undo delivered, send the confirmation, release the draft allocations, delete a draft | no | yes |
| `orders:sales-confirm` | Confirm orders | Confirm, lock and unlock; accept a quote into a confirmed order | no | yes |
| `orders:settings` | Manage Orders settings | The settings, the number series, the quote templates and the sales warnings; overriding a block warning | no | no |
| `orders:subscriptions-manage` | Manage subscriptions | Subscriptions — first asked for by delivery S | no | yes |
| `orders:deliveries-manage` | Manage deliveries | Deliveries (delivery C) and shipments (delivery D) | no | yes |
| `orders:carriers-manage` | Manage carriers | Carrier accounts and their keys under Integrations — delivery D | yes | no |
| `orders:analysis-view` | View sales analysis | The analysis views and their CSV files | no | yes |

Every operation asks for `orders:access` **and** its own key, so a role that gives
`orders:quotes-manage` without `orders:access` gives nothing. The subscriptions,
deliveries and carriers keys are declared now so a role can be built once; no operation
asks for them yet.

The screens read other modules over HTTP and need their keys too: the customer picker
`customers:view`, a customer's contacts `customers:contacts-view` with
`customers:associations-view`, a free-text line's VAT codes `invoices:access`, the
project picker `projects:access`, the order page's **Invoice** — Invoices' From orders
wizard — `invoices:access` and `invoices:create`, and the links of its **Invoices** card
`invoices:access`. The catalog search a line is picked from is Orders' own
(`GET /variants`) and needs no Products key.

## The document header

A quote and an order share one header (O13). It is made from the customer's billing
profile:

- **The customer snapshot** — number, name, type, organisation number or foreign id,
  country and invoice address, the invoice e-mail — and the **delivery address** (the
  request's, else the customer's primary address) and the **contact** (the request's,
  else none), each copied in.
- **Your reference** defaults to the customer's buyer reference; the **currency** to the
  customer's, else the deployment's base currency; **prices include VAT** to true for a
  customer of type person and false otherwise; the **payment terms** to the customer's,
  else the settings' default; the **language** (`nb` or `en`) to the customer's, else
  `nb`.
- **The salesperson** is the request's, else the caller; the **project**, when named,
  must be the customer's (`project_customer_mismatch`, 400).
- The texts — intro, closing and the internal note, which is never printed — and, for a
  quote, the valid-until day; for both, a delivery date; whether the discount is printed.

While a document is a draft its snapshot **follows the customer**: every save takes it
afresh from the profile (O14). Sending a quote or confirming an order **freezes** it, as
an issued invoice's. A customer disabled, archived or merged away takes no new document
and no change of customer (`customer_disabled`, 400); a draft whose customer was disabled
since can still be saved and is refused at send or confirm.

## Lines

A document has at most **500** lines (`too_many_lines`). A line is one of an open set of
kinds (O15): **product**, **section** (a heading) or **note** (text printed between the
lines). A save replaces the lines whole; a line that names its `id` is kept as that line.
A kept line keeps its kind, on a quote and an order alike: a save that names a kept line
with another kind is refused with `line_kind_changed` (409), and the client removes the
line and adds a new one instead.

A product line is a **catalog line** — a `variantId` from the catalog, taking its SKU,
name, unit and the VAT code its tax category maps to — or a **free-text line** with a name
or a description of its own and a VAT code: the one it names (`vat_code_unknown` when it is
not active), else the active code in category `S` with the highest rate. Quantities are
greater than 0 with at most three decimals; a unit price is 0 or more with at most four
decimals; a discount is 0–100 % with at most two. A quote's product line may be
**optional** (O21): it is printed, marked as optional, and left out of the totals; an
order or a section or note line never is (`optional_not_allowed`).

### Pricing

A catalog line takes the variant's **list price** in the document's currency on the day
(O16). A unit price given on the line makes it **manual**, and a manual price is kept on
every later save while the line keeps its variant. A kept catalog line is priced again
only when the currency changed or the business day moved since the last save; otherwise
it keeps its price. A catalog line with no list price in the currency has no price
until one is typed: it may be saved, and is refused at send and at confirm
(`line_unpriced`). A line whose tax category maps to no VAT code in Invoices is refused at
the same points (`vat_code_unmapped`) — the mapping is kept on Invoices' VAT settings
([Products' tax categories](/en/user/invoices/#products-tax-categories)).

A catalog line also keeps the **product** its variant belongs to (`productId`), as it was
when the line was written: the warnings a document carries and the intake by product are
read from it.

### Packagings

A catalog line may be entered in one of the variant's **packagings** (O15), from
`VariantEntry.Packagings`: the line names `packagingId` and `packagingQuantity` — how many
of it — and the server stores the quantity in the variant's unit, exactly:
`packagingQuantity` × the packaging's quantity, so 3 of a box of 15 is 45. The line keeps
the packaging's id, the count and its **name** (`packagingName`), as it was then. A
packaging that is not the variant's, or one named on a free-text line, is
`packaging_not_found` (400); a count that is not more than 0 with at most three decimals is
`quantity_invalid`, and so is a count whose quantity would need more than the quantity's
three decimals (1.5 of a packaging of 1.555), so the stored quantity is always the count
times the size and the size can be read back from the line. A confirmed order's kept line
in the same packaging takes a new count at the size it was confirmed with. A kept line
that names the packaging it was saved in after the catalog has removed it keeps that
packaging, its size and name from the line; a new line cannot name it. The PDF prints the line's packaging after its
description: "3 × Eske à 15 (45 stk)" in Norwegian, "3 × Box of 15 (45 pcs)" in English.

Prices are **stored net** of VAT. "Prices include VAT" is how they are entered and shown
(O19): a price typed on such a document is divided by its VAT rate before it is stored.

### The arithmetic

Every amount is [`internal/docmath`](/en/contributing/platform-packages/#the-document-arithmetic)'s,
Invoices' own arithmetic: `docmath.Line` gives a line's gross, allowance and net, and the
totals are the VAT rows and the net, VAT and gross totals of the lines that are not
optional, rounded as an invoice is. A line is therefore the same number on the order and
on the invoice made from it.

## Concurrency

Every quote and order the API answers carries its **`updatedAt`**, and every write of one
sends it back as **`expectedUpdatedAt`** — in the body, and as a query parameter on the
`DELETE`s. A write whose `expectedUpdatedAt` is not the document's current `updatedAt` is
refused with `version_conflict` (409) and changes nothing, even when the revision or
version did not move: a hold, a release, a lock, an unlock and a delivery marking move
`updatedAt` and leave the version. The token is asked of the saves, send, mark as sent,
revise, accept, decline, cancel, apply-template, confirm, hold, release, lock, unlock,
cancel the rest, mark and undo delivered, and the deletes. A customer merge or erase that
touches a document moves its `updatedAt` too ([The holders](#the-holders)), so an editor
opened before it is stale.

## Number series

Quotes and orders each have a series (O12). A number is taken **when the document is
created**, so a deleted draft leaves a gap; neither is a sales document under the
bookkeeping rules, and a gap needs no explanation. Each series starts at 1 by default.
The settings may move a start: the new start must be at least the next number the series
would hand out, so a series can only ever go up (`series_start_too_low`, 400). A quote
keeps its number across revisions.

## Quotes

### States

| State | Means | Out of it |
| --- | --- | --- |
| `draft` | Being written | send, mark as sent, cancel, delete (revision 1 only) |
| `sent` | With the customer, frozen at its revision | accept, decline, revise, cancel |
| `expired` | Sent, and its valid-until day has passed | accept, decline, revise |
| `accepted` | Accepted into an order, once | — |
| `declined` | The customer said no, with an optional reason | — |
| `cancelled` | Withdrawn | — |

Any quote can be **copied** into a new draft.

- **Send** and **mark as sent** freeze a draft (O20): its lines must all be priced and
  mapped, its PDF of this revision is printed and stored, its valid-until day is fixed —
  the request's, else the draft's own if not past, else today plus the template's
  validity or the settings' (30 days by default); a day before today is refused — and it
  moves to `sent` and publishes `orders.quote.sent`. Send also e-mails it ([Sending](#sending)).
- **Revise** moves a sent or expired quote back to `draft` with the next **revision**;
  the PDF of every earlier revision is kept.
- **Delete** takes a quote that was never sent — a draft at revision 1 — and leaves its
  number as a gap (`quote_not_draft`, 409, otherwise).
- **Decline** records the customer's no on a sent or expired quote, with a reason of up to
  200 characters.
- **Cancel** withdraws a draft or a sent quote.
- **The quote expiry worker** moves a sent quote past its valid-until day to `expired`
  once a day ([Workers](#workers)). An expired quote can still be accepted, declined or
  revised.

A quote's writes name the revision they were made from, and the `updatedAt` they read
([Concurrency](#concurrency)); either stale is `version_conflict` (409). An answered quote — accepted, declined or cancelled — is
refused with `quote_not_sent` or `quote_already_accepted`.

### Accept

Accept (O20, O21) takes a sent or expired quote **once**, with the optional lines the
customer took (`optionalLineIds`), and makes either a **draft order**
(`into: draft_order`) or a **confirmed order** (`into: confirmed_order`), which also needs
`orders:sales-confirm` (403 without it) and runs every check of a confirm. The order is
made by the one create command (O28) from the quote's frozen header and the chosen lines
**with their frozen prices**; the optional lines not chosen are left out. Its origin
channel is `quote` and its source `<quoteId>@<revision>` (O27). The quote records what it
became and moves to `accepted`, and `orders.quote.accepted` is published — on one
transaction that locks the variants first, then the quote. A second accept is
`quote_already_accepted` (409).

### Copy

Copy makes a new draft quote of any quote, in any state, with its origin channel `copy`
and its source the quote at its revision. As a new draft it takes the customer's
snapshot afresh and prices the catalog lines again; a manual price is kept, and a line
whose variant is gone from the catalog is left out.

### Templates

A quote template (O23) holds a name (unique, `template_name_taken`, 409), the intro and
closing texts, a validity in days (empty takes the settings'), whether the discount is
printed, an archived flag, and lines: product lines with a variant, a quantity, the
optional flag and an optional **net** price override; sections; and notes. A template is
priced only when it is applied.

A template is applied when a quote is created from it (`templateId`) or to a draft with
**apply-template**, whose lines are appended or, with `replace`, replace the draft's;
empty texts are filled from the template. A line whose product was discontinued is skipped
as `archived`, and one whose variant was deleted as `deleted`; the answer names every
skipped line. Deleting a template leaves the quotes made from it as they are.

## Orders

### Create

An order is created directly (`POST /orders`) or by accepting a quote, both through the
one create command (O28): a draft, with the next order number, at version 1. A create
names a `clientRequestId` (O27): a repeat with the same id answers the first document
with **200**; the same id for another customer is `client_request_reused` (409). Quotes
are created the same way.

### States and flags

An order is `draft`, `confirmed` or `cancelled`. Beside the state it has two flags and
two derived statuses:

- **On hold**, with a reason of up to 200 characters: a confirmed order that offers
  nothing to invoice and takes no delivery, and can still be edited.
- **Locked**: a confirmed order that takes no edit, and still delivers and invoices.
- The **delivery status** and **invoice status**, each `none`, `partial` or `delivered`
  (`invoiced`), from the lines' buckets: a line has an ordered quantity and a delivered,
  an invoiced and a cancelled quantity; it is complete when delivered (invoiced) plus
  cancelled covers what was ordered.
- **Done**: confirmed, with at least one product line, and every product line
  satisfied — its cancelled plus invoiced quantity covers what was ordered, and so does
  its cancelled plus the larger of delivered and invoiced. A remainder cancelled whole is
  done; a line delivered and not invoiced is not. The invoiced quantity is what issued
  invoices stamped ([Invoicing from orders](#invoicing-from-orders)), so an order becomes
  done at an invoice's issue. The list's `done` and `open` filters and the retention clock
  use the same rule.

| Operation | Takes | Refused with |
| --- | --- | --- |
| Confirm | a draft | `order_not_draft` (409); `customer_disabled`, `order_empty` (no product line), `line_unpriced`, `vat_code_unmapped` (400); `blocked_by_warning` (409, [Sales warnings](#sales-warnings)) |
| Hold, release | a confirmed order | `order_not_confirmed` (409). Release of an order not on hold answers it unchanged |
| Lock, unlock | a confirmed order | `order_not_confirmed` (409) |
| Cancel | a draft, or a confirmed, unlocked order with nothing delivered or invoiced | `order_locked` (409) for a locked one — unlock it first; `order_has_progress` (409) once anything is delivered or invoiced; `order_not_confirmed` (409) for one already cancelled; `order_in_draft_invoice` (409) while a draft allocation holds part of it |
| Cancel the rest | a confirmed, unlocked order | `order_not_confirmed`, `order_locked`, `order_in_draft_invoice` (409) |
| Delete | a draft | `order_not_draft` (409). A draft accepted from a quote puts that quote back to `sent` |

**Confirm** freezes the snapshot and the prices, prints the confirmation inside its
transaction from the rows it confirmed, stores that PDF after the commit at the version it
produced, and publishes `orders.sales_order.confirmed`. **Cancel the rest** sets each product line's cancelled
quantity to what was ordered less the larger of what is delivered and what is invoiced;
when that changes a line it logs `cancelled_remaining` per line, bumps the version and
publishes `orders.sales_order.changed`, and otherwise writes nothing. **Cancel** publishes
`orders.sales_order.cancelled` only for an order that had been confirmed: a draft was
never on the customer's timeline. Every write names the version it was made from and the
`updatedAt` it read ([Concurrency](#concurrency)); either stale is `version_conflict` (409).

**Deleting** a draft accepted from a quote puts that quote back to `sent`, on the same
transaction, so it can be accepted again: only the quote's `updatedAt` moves, nothing is
logged on it and nothing is published. The quote expiry worker treats it as any sent
quote, so one whose valid-until day has passed becomes `expired` on its next run.

### Editing a confirmed order

A draft order saves as a draft quote does: the snapshot follows the customer and kept
catalog lines are priced again when the currency or the day moved. A confirmed, unlocked
order (`order_locked`, 409, when locked) takes only what O26 allows:

- lines added;
- a line's quantity, down to the larger of what is delivered and what is invoiced plus
  draft-allocated, plus what is cancelled (`quantity_below_progress`, 400);
- a line's price, discount and text, while nothing of it is invoiced (`line_invoiced`,
  409) — and its price and discount while no draft allocation holds part of it
  (`line_in_draft_invoice`, 409);
- a line with no delivery, no invoice and no allocation that keeps it — a draft, an
  invoiced one, or a released one an invoice stamped — removed (`line_has_progress`, 409,
  otherwise); a removed line's released allocations no invoice stamped are deleted with
  it;
- the delivery date, the delivery address, the contact, your reference and the internal
  note.

Everything else of the header is **frozen** — the customer, the currency, the VAT mode,
the payment terms, whether the discount is printed, the language, the intro and closing
texts, the project and the salesperson — and a save that changes one is refused naming the
field ("This is frozen once the order is confirmed", 400).

A **kept line** — a product line naming the same variant, or none for free text, in the
same packaging — is priced from its own row: it keeps the item, SKU, name, unit and VAT
code, rate and category it was confirmed with, and the catalog is not read for it, so a
variant renamed, re-categorised or deleted since changes nothing and refuses nothing. A
save that gives such a line another unit or VAT code is `line_invalid` (400): add a new
line instead. Only its quantity, price, discount and text change, and a catalog line keeps
its frozen price unless the save names a new one.

### Versions and the change log

The **version** is the confirmation's (O26). A draft stays at 1. An edit that changes a
confirmed order, and a cancel of the rest that changes a line, bump it, log one entry and
publish `orders.sales_order.changed`; a confirmation printed or sent at version 2 or later
is titled **Revised order confirmation** ("Revidert ordrebekreftelse – versjon N" in
Norwegian). A hold, a release, a lock, an unlock and a delivery marking change no printed
word: they are logged at the version they found and do not bump it.

Each change log entry has the version, the time, the actor (a user or the system) and a
list of typed actions, each with the line it is about where it is about one and the value
before and after as text: `line_added`, `line_removed`, `quantity_changed`,
`price_changed`, `discount_changed`, `text_changed`, `delivery_changed`,
`contact_changed`, `reference_changed`, `cancelled_remaining`, `held`, `released`,
`locked`, `unlocked`, `delivered_marked`, `delivered_undone`, `warning_overridden` (a
block warning overridden at confirm: from is the warning, to the reason) and
`draft_allocations_released` (to is how many were released). An invoice's issue and a
credit note's release are not logged: the order's **Invoices** list is their record.

### Delivered by hand

Until deliveries from stock come with delivery C, delivery is registered by hand (O32) on
a confirmed order: **mark-delivered** adds per-line quantities, never beyond what is
ordered less cancelled (`over_delivery`, 400), and **undo-delivered** takes them back,
never below 0 (`undo_exceeds_delivered`) nor below what is invoiced
(`delivered_below_invoiced`). A line not on the order is `line_not_found`; a quantity that
is not a positive number with at most three decimals is `quantity_invalid`. Every line of
the request is judged at once. Each marking logs `delivered_marked` or `delivered_undone`
and recomputes the delivery status. A locked order still takes it; an order on hold takes
neither (`order_on_hold`, 409) — release it first. Marking and undoing lock the lines'
variants, since an open line holds its variant against a delete.

## Invoicing from orders

Delivery B (O36–O41) invoices orders in **Invoices**: its From orders wizard and its
billing run of orders ([Invoicing orders](/en/reference/invoices/#invoicing-orders)) read
what is due through this module's **`contracts.BillableOrders`**, hold what they put on a
draft as **allocations**, and the issue stamps them through this module's
**invoiced-work holder**. Orders never reads Invoices for it, and no permission of this
module is asked: the wizard needs `invoices:access` and `invoices:create`.

### Allocations

`orders.invoice_allocations` (migration `00121_orders_allocations.sql`) holds what part of
an order line an invoice bills (O36). An invoice line holds one live source, and a line
may be invoiced in several parts, so the source an invoice line names is an allocation —
kind `orders.allocation` (`contracts.WorkSourceOrderAllocation`) — never the line. A row
carries its line and order, its `kind` (`order_line` in B; `subscription_period`, with
its period, is delivery S's), its `quantity` (three decimals), the line's `unit_price`
(eight decimals) and `discount_percent` and `vat_code_id` when it was made, its `amount` —
docmath's line net of those three numbers (O37), so the invoice line built from them
always nets the same —, its `state` and `revision` (1: a row is never rewritten), and an
invoice's stamp.

| State | Means | Moves to |
| --- | --- | --- |
| `draft` | A draft invoice holds the quantity; it is not offered again | `invoiced` by the issue; `released` by the release after a draft is deleted or its line removed, by the order's [release](#release-draft-allocations), or by the [sweep](#workers) |
| `invoiced` | Stamped by an issued invoice: `invoice_id`, `invoice_number`, `issue_date`, `invoiced_at`, `invoiced_by` and `invoiced_by_display` | `released` by the credit note that returns its invoice line in full |
| `released` | Its quantity went back to the line; a credited one keeps the invoice's stamp as the order's history | — |

What a line has **to invoice** is, for a line invoiced on order (`invoice_policy`
`order`, every line in B), **ordered − cancelled − invoiced − draft-allocated**, never
below 0; on delivery (`delivery`) it will be delivered − invoiced − draft-allocated,
delivery C's. The order answers each line's `qtyDraftAllocated`.

### The billable read

`contracts.BillableOrders` (`billable_orders.go`, `Module().BillableOrders`,
`Deps.BillableOrders`) is a single-provider slot resolved after `Consumption`, only from
the enabled modules — nil when Orders is off. It reads and writes on the pool and the
clock and never reads a directory; its caller never calls it under a lock of its own.

- **`DueOrders`** — the orders a request names (`CustomerIDs`, `OrderIDs` or `Every`,
  exactly one), each with its lines left to invoice: number, customer, currency, project,
  your reference, delivery date, delivery name and address, payment terms and, per line,
  its position, text, unit, `ToInvoice`, unit price, discount, VAT code, invoice policy and
  amount. `Until` bounds the orders by their delivery date, on or before; an order with
  none is always due. A draft order and one on hold are answered with `Blocked` (`draft`,
  `on_hold`), so the caller can say why. At most 5 000 lines (`MaxBillableRows`), `More`
  set when there were more. It writes nothing.
- **`Allocate`** — a provider command (module boundaries rule 5): a new draft allocation
  per line named, on one transaction of its own. It reads the lines' orders, then locks
  the orders `FOR UPDATE` in id order, then the lines, so two Allocates serialise on the
  orders and the second sees what the first allocated; it locks no variant (MB rule 21
  does not apply — an allocation writes no line). Each line is judged under the locks, the
  first refusal answering and nothing written — an `AllocationRefusal` with the line and
  a code: **`line_not_invoiceable`** for a line that is not a priced product line with a
  VAT code invoiced on order, or whose order is a draft, on hold or cancelled;
  **`allocation_exceeds_open`** for a quantity above what the line has to invoice. The
  allocation names its order's project only while Projects is composed.
- **`Allocations`** — the allocations named, as they stand; a missing id is absent.
- **`ReleaseAllocations`** — a provider command: the named drafts released in one
  statement; one invoiced, released or missing is left as it is, so a repeat is harmless.

### The invoiced-work holder

Kind `orders.allocation`, last in `contracts.InvoicedWorkOrder` (after Projects, Expenses,
Time and Energy: an invoice may mix a project's hours and an order's lines). It runs on
the issue's transaction, and on a credit note's, with `Deps.Logger` and nothing else, and
is composed whatever `MODULES` says.

- **`MarkInvoiced`** locks the sources' orders by id, then their lines, then the
  allocations, each in id order, and judges each allocation as it stands, in the
  contract's order: **`source_already_invoiced`**; then **`source_not_invoiceable`** —
  missing, released, its order no longer confirmed or on hold, its line no longer
  invoiceable, or its quantity beyond what the line has open (ordered − cancelled −
  invoiced, the allocations before it in this issue counted); then **`source_changed`** —
  a revision, currency or exact amount other than the source's. The first refusal rolls
  the issue back. Otherwise every allocation is stamped with the `InvoiceRef`, its
  quantity added to its line's `qty_invoiced`, each order's invoice status recomputed and
  its `updated_at` moved to `IssuedAt` — its version is not bumped and nothing is logged,
  as for a delivery marking — and **`orders.sales_order.invoiced`** published once per
  order ([Events](#events)): the one holder that publishes (module boundaries rule 10).
- **`ReleaseInvoiced`**, on the credit note that returns an allocation's invoice line in
  full — the last of several partial credits included — locks the same way and moves each
  allocation carrying that invoice's stamp to `released`, the stamp kept; its quantity is
  taken off the line's invoiced, so it is to invoice again, and the invoice status
  recomputed. One without the stamp is left as it is, with a warning logged: a credit
  note is never blocked. Nothing is published.

### Release draft allocations

`POST /orders/{id}/release-draft-allocations` (`orders:sales-manage`, the version and
`expectedUpdatedAt` as every write) releases every draft allocation of the order at once,
so its quantities are offered to invoice again. A draft invoice still holding one is then
refused at its issue (`source_not_invoiceable`). It is logged as
`draft_allocations_released` with how many, at the version it found, and moves the
order's `updatedAt`; an order with none is answered as it is, and nothing is logged.

Invoices releases the allocations a draft held after the delete — or the save that
dropped a line — has committed, best effort; it does not when a customer's anonymisation
deletes the drafts. What a failed release leaves, [`orders.allocation_sweep`](#workers)
releases.

### The rules a draft allocation sets

A draft allocation is progress on its line, and the order keeps to it (O26): a confirmed
line's quantity goes no lower than the larger of delivered and invoiced + draft-allocated,
plus cancelled (`quantity_below_progress`); its price and discount do not change
(`line_in_draft_invoice`, 409); the line is not removed (`line_has_progress`); and the
order is neither cancelled nor its rest cancelled (`order_in_draft_invoice`, 409). Each
answer says how to proceed: issue or delete the draft, or release the allocations.

### The order page

`GET /orders/{id}` answers `invoices[]`: every invoice that stamped an allocation of the
order, in the order they were first stamped — `invoiceId`, `invoiceNumber`, `issueDate`,
`amount` (the net it billed of this order, in the order's currency), `currency` and
`state`, `invoiced`, or `released` once every allocation of it was given back by a credit
note. The page shows it as the **Invoices** card, and **Invoice** — needing
`invoices:access` and `invoices:create`, disabled on hold, with nothing left to invoice or
with unsaved changes — opens Invoices' wizard at `/invoices/new/from-orders?orderId=`.

## PDFs

There are three prints (O24), each in the document's language, laid out from the seller as
Invoices holds it:

- **The quote** — "Tilbud" / "Quote", subtitled with its number and, from revision 2,
  its revision. A draft's is printed on demand with an **UTKAST** / **DRAFT** watermark.
- **The order confirmation** — "Ordrebekreftelse" / "Order confirmation", or "Revidert
  ordrebekreftelse" / "Revised order confirmation" with "versjon N" from version 2. A
  draft order's is printed with the watermark.
- **The pro forma** of either — "Proforma – ikke en faktura" / "Pro forma – not an
  invoice" — printed now, never stored and never sent.

Each sent quote revision and each confirmed or sent order version is **stored once**,
immutable, under the key `{quote|order}/{id}/{quote|confirmation}-{number}-v{revision or
version}-{sha256}.pdf` in the `orders` scope; a download reads it back and checks its hash,
and never prints it again. A stored PDF that is missing or no longer matches its hash
answers 500: an operator problem, never papered over. The object is put before the
transaction that records it, so a failed transaction leaves at most an orphan nothing
serves. A confirmation that could not be stored at confirm is printed and stored by the
first download or send while the order is still at that version; the download reads the
order, its lines and the stored row in one snapshot.

## Sending

A document is sent by e-mail through `contracts.OutboundMail` on the settings' **send
channel** (O25). Sending works when Communications is on and the chosen channel can send
now; `GET /meta` says so in `sendingAvailable`. Otherwise a send is
`sending_disabled` (503), and a quote is downloaded and marked as sent instead.

A send from the dialog is **synchronous**: the recipient (by default the contact's
e-mail, else the customer's invoice e-mail), up to ten copies, an optional subject and a
message; for a quote also the valid-until day. The PDF is stored, the mail sent and the
document moved on one request, and every send is logged in `mailings` with its status
(`queued`, `sent`, `failed`), its subject, recipient, copies, who sent it and any error.
When the mail provider does not take the e-mail the answer is `mail_failed` (503):
nothing changed, and the failed send is listed. A send of an order needs a confirmed
order and sends the current version.

**The cover mail** is plain text in the document's language: a greeting, a line naming
the document, its number and its total including VAT — a quote's with its valid-until
day, a revised confirmation's with its version — the dialog's message, and the closing
with the seller's name. The subject is "Tilbud 1042 fra …" / "Quote 1042 from …",
"Ordrebekreftelse …" / "Order confirmation …" or "Revidert ordrebekreftelse …" /
"Revised order confirmation …". The settings may override the subject and the body per
kind (`quote`, `confirmation`) and language (`nb`, `en`), up to 300 and 4000 characters;
an override may name `{number}`, `{seller}`, `{total}` and `{validUntil}`, which are
filled in. An empty override uses the built-in text.

**Queued sends** are the automatic ones — the confirmation [the handler](#the-handler)
queues — sent by the `orders.mail` worker. A queued confirmation sends the stored PDF of
its version, printing and storing it first when none is stored and the order is still at
that version. When the order has moved on — a confirmed edit bumped the version before the
mail went — the confirmation of the current version is sent instead, logged, and the
mailing records the version that went out, which the order's mailings list shows. A
cancelled order's queued confirmation fails without sending, and a queued send with no
recipient fails at once.

## Events

The module publishes onto the [domain-events outbox](/en/contributing/platform-packages/#the-domain-events-outbox),
each on the transaction that makes the change and while it holds the document's row
(O2, O3). A payload carries ids, numbers, amounts and states, never personal data;
`netTotal` is the exact decimal text of the net total.

| Event (v1) | When | Payload |
| --- | --- | --- |
| `orders.quote.sent` | A quote is sent or marked as sent | `quoteId`, `number`, `revision`, `customerId`, `currency`, `netTotal`, `sentAt` |
| `orders.quote.accepted` | A quote is accepted | `quoteId`, `number`, `revision`, `customerId`, `acceptedIntoKind` (`order`), `acceptedIntoId`, `acceptedAt` |
| `orders.sales_order.confirmed` | An order is confirmed | `orderId`, `number`, `version`, `customerId`, `currency`, `netTotal`, `confirmedAt` |
| `orders.sales_order.cancelled` | A confirmed order is cancelled | `orderId`, `number`, `customerId`, `cancelledAt` |
| `orders.sales_order.changed` | A confirmed order is edited, or the rest cancelled, at a new version | `orderId`, `number`, `version`, `customerId`, `changedAt` |
| `orders.sales_order.invoiced` | An invoice's issue stamped allocations of the order — once per order the invoice bills, by the [holder](#the-invoiced-work-holder) on the issue's transaction | `orderId`, `number`, `customerId`, `invoiceId`, `invoiceNumber`, `invoicedAt` (the issue's time) |

Customers writes the first four on the customer's timeline
([the timeline](/en/reference/customers/#the-timeline)).

### The handler

`orders.queue_confirmation` takes `orders.sales_order.confirmed` v1. When the setting
**confirmationOnConfirm** is on, it queues the confirmation's mailing of that version to
the order's contact's e-mail, else the customer's invoice e-mail; an order with neither is
skipped with an info log, and so is one gone or no longer confirmed at the event's
version: an order cancelled before the event was handled gets no confirmation. It runs on the delivery's own transaction,
writes only `orders.mailings`, and the mailing is unique per order, version and triggering
event, so a retried delivery queues nothing twice. The mail itself is sent by
`orders.mail`.

## Workers

| Worker | Runs | Does |
| --- | --- | --- |
| `orders.mail` | every 10 seconds, up to 10 rows a cycle | Sends the queued mailings. A row is claimed under a two-minute lease (`SKIP LOCKED`), so two replicas never take one row; the stored PDF is read and mailed outside any transaction. A failure counts the attempt and waits 30 seconds, doubled per attempt, at most an hour; the eighth failure fails the row. A row that can never go — no recipient, sending disabled, the document gone, the order cancelled — fails at once. |
| `orders.retention` | daily, one replica under an advisory lease | Anonymises the kept confirmed orders of erased customers once their retention has run, blanks their sends, and deletes the stored PDFs of every anonymised quote and order of theirs ([Retention](#retention)). |
| `orders.quote_expiry` | daily | Moves every sent quote whose valid-until day is before today, in the business time zone, to `expired`, in one statement. It publishes no event. |
| `orders.allocation_sweep` | daily, one replica under an advisory lease | Releases every draft allocation not written for 30 days — Energy's horizon for a draft settlement —, the backstop for a release that never arrived after a draft invoice was deleted. A draft invoice still holding one is refused at issue (`source_not_invoiceable`) and drafted again. Its one statement locks only what it releases: an issue stamping one holds it, so the sweep waits and skips it. |

Each is safe on a pool of one connection.

## The holders

Each runs on this module's schema, inside the caller's transaction where it is handed
one, and is composed whether or not `orders` is enabled.

- **Customer merge** (O29): every quote and order of the merged-away customer is
  re-pointed to the survivor; the snapshot is not touched, so a frozen document keeps the
  customer as it was. A quote's and a draft order's `updatedAt` moves, so an editor
  holding one from before the merge is stale; a confirmed or cancelled order's does not,
  since it is the moment a done order's retention runs from. The merged-away customer's
  sales warning moves to the survivor when the survivor has none, its kept name blanked
  (the settings show the customer's number until the warning is next set); when both have
  one the survivor's is kept and the other deleted — except that a merged-away `block`
  over the survivor's `info` makes the survivor's a `block`, its text the survivor's then
  the merged-away one's on a new line, so a merge never lifts a block. Reported as `orders.quotes`, `orders.orders`
  and `orders.warnings`.
- **Customer export**: every quote and order of the customer, each with its snapshot,
  status, revision or version, totals, texts, lines and sends, and the customer's sales
  warning, in one read-only snapshot; nothing for a customer with none of them.
- **Customer erase** (O29, O54), inside Customers' anonymisation: blanks the name, the
  legal ids, the addresses, the contact, the invoice e-mail, your reference and the
  internal note **at once** on every quote — a quote is offer text, no bookkeeping record
  — and on every order never confirmed, moving their `updatedAt` so an open editor is
  stale; blanks the recipient and the copies of every send and fails a queued one. A
  **confirmed** order is bookkeeping material and keeps its snapshot and its stored PDFs:
  the order numbers kept are logged, and `orders.retention` anonymises them when their
  time comes. The stored PDFs print the person, but deleting an object is a call the
  transaction may not make, so the erasure is recorded in `orders.erasures` whenever a
  confirmed order is kept **or** a stored PDF of the customer's exists, and the retention
  worker deletes them ([Retention](#retention)). Reported as
  `orders.quotes`, `orders.draftOrders`, `orders.orders` (always 0), `orders.mailings` and
  `orders.warnings` — the customer's sales warning is deleted; run again, it reports
  zeros.
- **Invoiced work** (O38): [the invoiced-work holder](#the-invoiced-work-holder) stamps
  and releases the allocations inside an invoice's issue and a credit note's.
- **Variant delete** (O30): Products' delete of a variant is refused while an open
  document names it — a draft or sent quote, or an order not cancelled with a line of it
  that is not delivered or not invoiced in full — as an `open_documents` hold with the
  count. A change of a product to a service is never held. A template is no document: a
  variant gone from it is skipped when it is applied.

A **user's erasure** (MB rule 19) leaves Orders as it is: the salesperson and every actor
are kept on quotes, orders, sends and the change log with their id and display name, as
records of what was agreed.

### Retention

A confirmed order is kept under the Bookkeeping Act (bokføringsloven § 13) until
**3 years and 6 months after the end of the financial year** — the calendar year in the
business time zone — in which it was cancelled or became done. "Became done" is the
order's last change once it is confirmed, delivered and invoiced. A confirmed order not
done is kept. Each day `orders.retention` works through the recorded erasures and
anonymises every such order whose year has run. It then deletes the stored PDFs of the
customer's anonymised quotes and orders — the quotes' on the first cycle after the erase,
so within about a day, and an order's once it has been anonymised after its retention —
each object from the store first, then its row; the send rows stay, blanked, with their
link to the PDF removed. An erasure is marked complete only when no order of the customer
is left unanonymised and no stored PDF of theirs remains.

## The customer summary

`GET /customers/{customerId}/summary` (O31a) answers, per currency and net of VAT:

- **Open quotes** — the draft and sent quotes, counted, at their net totals;
- **Ordered, not delivered** — the confirmed orders with something left to deliver,
  counted, each line's open quantity valued at its net amount over its quantity;
- **Delivered, not invoiced** — delivered less what issued invoices stamped invoiced,
  valued the same way.

The customer page's **Orders** tab shows it above the customer's open quotes and orders.
The tab needs the `orders` module and `orders:access`; **New quote** needs
`orders:quotes-manage` and **New order** `orders:sales-manage`, each only on a customer
that is neither merged away nor anonymised.

## Sales warnings

A **sales warning** (O33) is kept on a customer, in `orders.customer_warnings` keyed by the
customer id, or on a product, in `orders.product_warnings` keyed by the product id —
Orders' own tables, since a warning is a selling rule neither Customers nor Products needs.
A warning has a **level**, `info` or `block`, a **text** of 1–500 characters, the name of
its customer or product as it was when it was written (read through the directory or the
catalog, so the settings list it without either), who wrote it last and when. A customer
or product has at most one; a `PUT` replaces it. They are written under `orders:settings`
and read under `orders:access`.

Every quote and order the API answers carries its **`warnings`**: its customer's, then
those of the products on its product lines, by product id — read from the lines' product
snapshots, so a line written before the warning existed counts as well.

- An `info` warning is shown and never refuses anything.
- A `block` warning refuses **send** and **mark as sent** of a quote, **confirm**, and
  **accept** into a confirmed order with `blocked_by_warning` (409), whose detail names each
  blocking warning and its text. A quote's send is judged before its PDF is printed or its
  mail sent; a send whose mail went out is not judged again, since the customer has the
  quote — a block set while the mail was going out does not undo it. Mark as sent is
  judged again under the quote's lock, and a confirm under the order's lock. The warnings
  are read without a lock of their own, so the check is best effort: a warning set by a
  commit that lands while a confirm is running does not refuse that confirm. Saving a
  draft, accepting into a draft order and sending a confirmed order's confirmation are
  never refused.
- The request **overrides** the blocks with `overrideWarnings: true` and an
  `overrideReason` (required, at most 500 characters; 400 without it). Only a caller who
  holds `orders:settings` overrides; anyone else is refused with `blocked_by_warning` as
  if no override were asked. An override at confirm — accept into a confirmed order
  included — is logged on the order's change log at the confirmed version, one
  `warning_overridden` action per block, from the warning to the reason; an override at
  send or mark as sent is put first in the quote's internal note, which has no change log:
  "Advarsel overstyrt <day> av <name> (<warnings>): <reason>", in the quote's language,
  then the note as it was, cut to the note's 4000 characters — so the cut takes the old
  note's end, never the override.

A product warning is not removed when the product is: Products deletes it without asking
Orders to write. `GET /warnings` reads each product warning's product through the catalog,
outside any transaction, and marks one whose product is gone **`orphaned: true`**; it can
no longer be set (`PUT` is 404) but is removed with `DELETE`. A catalog that cannot be read
leaves the list unmarked rather than failing it. [The holders](#the-holders) re-point a customer's warning on a
merge and delete it on an erase.

## The variant grid

`GET /products/{productId}/grid` (O34) answers a product's variants for *Add from grid*,
read through `ProductCatalog.VariantsOfProduct`, each with its SKU, unit, option values and
list price in `currency` (the base currency when absent) today. The options are the option
names the variants' `OptionValues` use, by name; each option's values are in the order the
variants, by id, first name them.

- **`mode: grid`** — one or two options, every variant valued on each, and at most 30
  values an option: `options` names them with their values and `cells` holds the variants.
  A combination no variant has is simply absent.
- **`mode: list`** — more than two options, an option with more than 30 values, a
  variant without a value on one of them, or none at all: `variants` holds them.

A product with no variants, or none that exists, is 404. The editor adds a line per
quantity typed, or sets the quantity of the variant's line in its unit when there is one;
the server prices it on the next save as any catalog line.

## Analysis

The three views (O35) need `orders:analysis-view`, read Orders' own tables — no
warehouse — and sum exactly, **per currency and net of VAT**: amounts in two currencies are
never added. A period is `from` and `to`, both included, days in the business time zone, at
most 3660 days long (400 otherwise). Each answers JSON, or with `format=csv` the same table
as a file: semicolon-separated, UTF-8 with a byte order mark, CRLF, the decimal comma,
cells guarded against formulas, the headers the API's own names, and named
`orders-hit-rate-<from>-<to>.csv`, `orders-intake-<groupBy>-<from>-<to>.csv` or
`orders-backlog-<day>.csv`.

- **Hit rate** (`GET /analysis/hit-rate`) — every quote whose `sent_at` (its last send) is
  in the period, optionally of one `salespersonId`, counted and summed at its net total
  by where it stands now: `accepted`, `declined`, `expired`, `cancelled`, or `open` — sent,
  or revised back to a draft. `rows` are per salesperson and currency, by name; `totals`
  per currency.
- **Intake** (`GET /analysis/intake`) — every order confirmed in the period and not
  cancelled since, by `groupBy`: `month` (`YYYY-MM` in the business time zone),
  `salesperson`, `customer` (the snapshot's name) at the orders' net totals, or `product`
  and `category` at the product lines' net amounts. The category is read through
  `ProductCatalog.Variants` for the period's variants (`CategoryID`, `CategoryName`); a
  free-text line, a variant gone from the catalog and a product with no category each
  group under an empty key. `count` is the orders in the group; `totals` per currency.
- **Backlog** (`GET /analysis/backlog`) — as of today, over every confirmed order's
  product lines, each line's quantity valued at its net amount over its quantity, rounded
  to øre, as [the customer summary](#the-customer-summary) does:
  - `orderedNotDelivered` — ordered less cancelled less delivered, and the orders with
    some;
  - `deliveredNotInvoiced` — delivered less invoiced, and the orders with some, aged from
    the latest `delivered_marked` of the line in the change log (the confirmation when
    there is none) into `days0To7`, `days8To30` and `over30Days`, since
    bokføringsforskriften § 5-2-2 asks an invoice within a month of delivery.

## Refusals

Every refusal is an RFC 9457 problem with this module's `code`; a client keys off the code,
never the words. 404 is a bare body for an id that does not exist. A 400 names the failing
fields in `errors` (keyed by the request's camelCase field), and a document's line refusals
in `lines` — every failing line at once, each with its `index`, `code` and `detail` —
the first of which is also the problem's `code`. A request the server could not decode is
a bare 400.

| Code | Status | Means |
| --- | --- | --- |
| `too_many_lines` | 400 | More than 500 lines |
| `optional_not_allowed` | 400 | An optional line on an order, or on a line that is not a product |
| `variant_not_found` | 400 | The line's variant is not in the catalog |
| `packaging_not_found` | 400 | The packaging is not one of the variant's |
| `quantity_invalid` | 400 | Not more than 0 with at most three decimals |
| `price_invalid` | 400 | Not 0 or more with at most four decimals |
| `discount_invalid` | 400 | Not 0–100 with at most two decimals |
| `vat_code_unknown` | 400 | The free-text line's VAT code is not active |
| `line_invalid` | 400 | Not a product, section or note; a free-text line with no name or description; an id not one of the document's; a confirmed order's kept line given another unit or VAT code |
| `line_unpriced` | 400 | At send, mark as sent or confirm: a line has no price in the currency |
| `vat_code_unmapped` | 400 | At send, mark as sent or confirm: a line's tax category maps to no VAT code |
| `order_empty` | 400 | Confirm: no product line |
| `customer_disabled` | 400 | The customer is gone, merged away, disabled or archived |
| `project_customer_mismatch` | 400 | The project belongs to another customer |
| `quantity_below_progress` | 400 | A confirmed line's quantity below what is delivered, or invoiced and draft-allocated, plus cancelled |
| `over_delivery` | 400 | More delivered than ordered less cancelled |
| `undo_exceeds_delivered` | 400 | More taken back than delivered |
| `delivered_below_invoiced` | 400 | Delivered taken below invoiced |
| `line_not_found` | 400 | A delivery line not on the order |
| `series_start_too_low` | 400 | A series start below the next number |
| `send_channel_unknown` | 400 | No such channel to send from |
| `version_conflict` | 409 | Made from a stale revision or version, or from an `updatedAt` older than the document's last write — reload |
| `quote_not_draft` | 409 | Only a draft quote takes it; delete only one never sent |
| `quote_not_sent` | 409 | Only a sent (or expired) quote takes it |
| `quote_already_accepted` | 409 | The quote is accepted |
| `order_not_draft` | 409 | Only a draft order takes it |
| `order_not_confirmed` | 409 | Only a confirmed order takes it |
| `order_locked` | 409 | The order is locked: unlock it before editing, cancelling or cancelling the rest |
| `order_on_hold` | 409 | The order is on hold: release it before marking or undoing delivery |
| `line_kind_changed` | 409 | A kept line given another kind: remove it and add a new line |
| `order_has_progress` | 409 | Something is delivered or invoiced: cancel the rest instead |
| `line_invoiced` | 409 | The price, discount or text of an invoiced line |
| `line_has_progress` | 409 | Removing a line with deliveries, invoices or an allocation that keeps it |
| `line_in_draft_invoice` | 409 | The price or discount of a line a draft allocation holds part of |
| `order_in_draft_invoice` | 409 | Cancelling an order, or its rest, while a draft allocation holds part of it |
| `client_request_reused` | 409 | The `clientRequestId` made a document for another customer |
| `template_name_taken` | 409 | Another template has the name |
| `blocked_by_warning` | 409 | A block warning on the customer or a product refuses send, mark as sent, confirm or accept into a confirmed order; the detail names it ([Sales warnings](#sales-warnings)) |
| `dependency_unavailable` | 503 | Customers, Products or Invoices is not composed |
| `sending_disabled` | 503 | Communications is off, or no channel that can send is chosen |
| `mail_failed` | 503 | The mail provider did not take the e-mail; nothing changed, the failed send is listed |

## Settings

`GET`/`PUT /settings` (`orders:settings`), replaced whole and guarded by a `revision`:

| Setting | Default | Rule |
| --- | --- | --- |
| `quoteSeriesStart`, `orderSeriesStart` | 1 | At least 1, and never below the next number once the series is used (`series_start_too_low`); the answer carries `nextQuoteNumber` and `nextOrderNumber` |
| `quoteValidityDays` | 30 | 1–365 |
| `defaultPaymentTermsDays` | 14 | 0–365; a customer's own terms win |
| `sendChannelId` | none | A Communications channel (`send_channel_unknown`); none sends nothing |
| `confirmationOnConfirm` | off | Queue the confirmation when an order is confirmed ([The handler](#the-handler)) |
| `combineByDefault` | off | Not read by Invoices: the From orders wizard starts at one invoice per order and a billing run of orders carries its own `combine`, so it has no effect yet |
| `mailTexts` | none | One subject and body per kind and language ([Sending](#sending)) |

The answer also lists the send channels Communications offers and whether sending is
available. `GET /meta` gives every holder of `orders:access` what the screens need of it:
`sendingAvailable`, `quoteValidityDays`, `defaultPaymentTermsDays` and `baseCurrency`.

## Assistant tour targets

The Assistant can point at these controls in a guided tour ([the registry](/en/reference/assistant/#guided-tours)). The package registers them in `apps/orders/frontend/src/assist-targets.json`, exports them as `assistTargets` (`@vantigo/orders-ui/assist-targets`) and marks each control with `data-assist="<key>"`. A route with `{…}` is a record's screen.

| Key | Route | Control |
| --- | --- | --- |
| `orders.quotes.new` | `/orders/quotes` | New quote |
| `orders.new-quote.customer` | `/orders/quotes/new` | The customer picker |
| `orders.new-quote.create` | `/orders/quotes/new` | Create the quote |
| `orders.quote.save` | `/orders/quotes/{quoteId}` | Save |
| `orders.quote.send` | `/orders/quotes/{quoteId}` | Send |
| `orders.quote.accept` | `/orders/quotes/{quoteId}` | Accept |
| `orders.quote.accept-into` | `/orders/quotes/{quoteId}` | The accept dialog |
| `orders.orders.new` | `/orders/orders` | New order |
| `orders.new-order.customer` | `/orders/orders/new` | The customer picker |
| `orders.new-order.create` | `/orders/orders/new` | Create the order |
| `orders.order.save` | `/orders/orders/{orderId}` | Save |
| `orders.order.confirm` | `/orders/orders/{orderId}` | Confirm |
| `orders.templates.new` | `/orders/templates` | New template |
| `orders.settings.save` | `/orders/settings` | Save the settings |
| `orders.customer.new-quote` | `/customers/{customerId}/orders` | New quote on the customer's Orders tab |
| `orders.customer.new-order` | `/customers/{customerId}/orders` | New order on the customer's Orders tab |
| `orders.lines.add-from-grid` | `/orders/quotes/{quoteId}` | Add from grid, under a quote's or an order's lines |
| `orders.document.warnings` | `/orders/quotes/{quoteId}` | The warnings' override and the customer's warning, on a quote or an order |
| `orders.settings.warnings` | `/orders/settings` | The sales warnings in the settings |
| `orders.analysis.views` | `/orders/analysis` | The analysis's three views |
| `orders.analysis.csv` | `/orders/analysis` | Download CSV of the open view |

## Enabling and disabling

The smallest installation with Orders is

```text
MODULES=customers,products,invoices,orders
```

Omitting `orders` means the module contributes no route and no permission; its paths
answer the `/api` catch-all 404 and the switcher tile and the customer's Orders tab are
not shown. The schema is migrated regardless, and the holders run whatever `MODULES`
says: a merge, an export or an erasure of a customer still reaches their quotes and
orders, and Products still refuses to delete a variant an open document names. The
invoiced-work holder is composed whatever `MODULES` says, so a credit note still releases
the allocations of an invoice issued while Orders was on; `contracts.BillableOrders` is
nil without the module, and Invoices' From orders answers `orders_unavailable`.

## Endpoints

All under `/api/v1/orders`. Every operation needs `orders:access` and the key named; 401
and 403 are the platform's. Every write of a quote or an order also answers 409
`version_conflict` for a stale `expectedUpdatedAt` ([Concurrency](#concurrency)).

| Operation | Permission | Answers |
| --- | --- | --- |
| `GET /meta` | access | 200 — sending available, default validity and payment terms, base currency |
| `GET /salespeople` | access | 200 — the active users holding `quotes-manage` or `sales-manage`, by display name; `q` narrows |
| `GET /variants` | access | 200 — active products' variants whose SKU, name or barcode contains `q`, each with its list price in `currency` today and its VAT code; 400 currency not three letters; 503 |
| `GET /customers/{customerId}/summary` | access | 200 — [the customer summary](#the-customer-summary) |
| `GET /quotes` | access | 200 — newest first, by customer, status, salesperson and a search; 400 paging |
| `POST /quotes` | quotes-manage | 201; 200 a repeat; 400 fields, lines; 409 `client_request_reused`; 503 |
| `GET /quotes/{id}` | access | 200; 404 |
| `PUT /quotes/{id}` | quotes-manage | 200; 400; 404; 409 `version_conflict`, `quote_not_draft`, `line_kind_changed`; 503 |
| `DELETE /quotes/{id}` | quotes-manage | 204; 404; 409 `quote_not_draft`, `version_conflict` (`expectedUpdatedAt` in the query) |
| `POST /quotes/{id}/apply-template` | quotes-manage | 200 with the skipped lines; 400; 404 quote or template; 409 |
| `POST /quotes/{id}/send` | quotes-manage (+ settings to override) | 200; 400 fields, `line_unpriced`, `vat_code_unmapped`; 404; 409 `blocked_by_warning`; 503 `sending_disabled`, `mail_failed` |
| `POST /quotes/{id}/mark-sent` | quotes-manage (+ settings to override) | 200; 400; 404; 409 `blocked_by_warning` |
| `POST /quotes/{id}/revise` | quotes-manage | 200; 404; 409 `version_conflict`, `quote_not_sent` |
| `POST /quotes/{id}/accept` | quotes-manage (+ sales-confirm for a confirmed order, + settings to override) | 200 the quote and the order; 400 fields and confirm's codes; 403; 404; 409 `quote_not_sent`, `quote_already_accepted`, `blocked_by_warning` |
| `POST /quotes/{id}/decline` | quotes-manage | 200; 400; 404; 409 |
| `POST /quotes/{id}/cancel` | quotes-manage | 200; 404; 409 |
| `POST /quotes/{id}/copy` | quotes-manage | 201; 404; 503 |
| `GET /quotes/{id}/pdf` | access | 200 the stored PDF of `revision`, or a draft's printed now; 404 |
| `GET /quotes/{id}/proforma` | access | 200; 404 |
| `GET /quotes/{id}/documents` | access | 200 the stored PDFs; 404 |
| `GET /quotes/{id}/mailings` | access | 200 every send; 404 |
| `GET /orders` | access | 200 — newest first, by customer, status, delivery and invoice status, open or done, salesperson and a search; 400 paging |
| `POST /orders` | sales-manage | 201; 200 a repeat; 400; 409 `client_request_reused`; 503 |
| `GET /orders/{id}` | access | 200 with lines, buckets and totals; 404 |
| `PUT /orders/{id}` | sales-manage | 200; 400 incl. `quantity_below_progress`, `line_invalid`; 404; 409 `version_conflict`, `order_locked`, `line_invoiced`, `line_in_draft_invoice`, `line_has_progress`, `line_kind_changed`; 503 |
| `DELETE /orders/{id}` | sales-manage | 204 — a draft accepted from a quote puts the quote back to `sent`; 404; 409 `order_not_draft`, `version_conflict` (`expectedUpdatedAt` in the query) |
| `POST /orders/{id}/confirm` | sales-confirm (+ settings to override) | 200; 400 `customer_disabled`, `order_empty`, `line_unpriced`, `vat_code_unmapped`, `overrideReason`; 404; 409 `order_not_draft`, `blocked_by_warning` |
| `POST /orders/{id}/hold` | sales-manage | 200; 400 reason; 404; 409 `order_not_confirmed` |
| `POST /orders/{id}/release` | sales-manage | 200; 404; 409 |
| `POST /orders/{id}/lock` | sales-confirm | 200; 404; 409 `order_not_confirmed` |
| `POST /orders/{id}/unlock` | sales-confirm | 200; 404; 409 |
| `POST /orders/{id}/cancel` | sales-manage | 200; 404; 409 `order_locked`, `order_has_progress`, `order_in_draft_invoice` |
| `POST /orders/{id}/cancel-remaining` | sales-manage | 200; 404; 409 `order_not_confirmed`, `order_locked`, `order_in_draft_invoice` |
| `POST /orders/{id}/release-draft-allocations` | sales-manage | 200 — [every draft allocation released](#release-draft-allocations); 404; 409 `version_conflict` |
| `POST /orders/{id}/mark-delivered` | sales-manage | 200; 400 `over_delivery` and the line codes; 404; 409 `order_not_confirmed`, `order_on_hold` |
| `POST /orders/{id}/undo-delivered` | sales-manage | 200; 400; 404; 409 `order_on_hold` |
| `POST /orders/{id}/send` | sales-manage | 200; 400; 404; 409 `order_not_confirmed`; 503 `sending_disabled`, `mail_failed` |
| `GET /orders/{id}/pdf` | access | 200 the stored confirmation of `version`, or the current one printed now; 404 |
| `GET /orders/{id}/proforma` | access | 200; 404 |
| `GET /orders/{id}/documents` | access | 200 the stored confirmations; 404 |
| `GET /orders/{id}/mailings` | access | 200 every send, with the version that went out; 404 |
| `GET /orders/{id}/changes` | access | 200 the change log; 404 |
| `GET /templates` | access | 200 by name; `includeArchived` |
| `POST /templates` | settings | 201; 400; 409 `template_name_taken` |
| `GET /templates/{id}` | access | 200; 404 |
| `PUT /templates/{id}` | settings | 200; 400; 404; 409 `version_conflict`, `template_name_taken` |
| `DELETE /templates/{id}` | settings | 204; 404 |
| `GET /settings` | settings | 200 |
| `PUT /settings` | settings | 200; 400 `series_start_too_low`, `send_channel_unknown`; 409 `version_conflict` |
| `GET /warnings` | access | 200 — every warning, customers' first, each by name, a deleted product's `orphaned`; `scope` narrows |
| `GET /warnings/customers/{customerId}` | access | 200; 404 none |
| `PUT /warnings/customers/{customerId}` | settings | 200; 400 level or text; 404 no such customer; 503 |
| `DELETE /warnings/customers/{customerId}` | settings | 204; 404 none |
| `GET /warnings/products/{productId}` | access | 200; 404 none |
| `PUT /warnings/products/{productId}` | settings | 200; 400; 404 no such product; 503 |
| `DELETE /warnings/products/{productId}` | settings | 204; 404 none |
| `GET /products/{productId}/grid` | access | 200 [the grid](#the-variant-grid); 400 currency; 404; 503 |
| `GET /analysis/hit-rate` | analysis-view | 200 JSON or CSV; 400 the period |
| `GET /analysis/intake` | analysis-view | 200 JSON or CSV; 400 the period or `groupBy`; 503 by category without the catalog |
| `GET /analysis/backlog` | analysis-view | 200 JSON or CSV |

The full contract, with every schema, is `openapi/orders.yaml`, published in the API reference.
