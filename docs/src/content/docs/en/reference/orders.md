---
title: "Orders module"
description: "The quotes and sales orders made for customers: the module and the modules it requires, the nine permissions, the document header and its customer snapshot, lines, pricing and the arithmetic, the concurrency token, number series, the quote's states and revisions, accept, copy and templates, the order's life, versions and change log, delivery marked by hand, invoicing from orders — the allocations, the billable read, the invoiced-work holder and the draft-invoice rules —, subscriptions — the plan, the period calendar, proration, the due rule and the twelve-month limit, the per-period allocations, billed through, dated lines and changes, pause, resume, end and cancel —, PDFs, sending and the cover mail, the events, the handler and the five workers, the holders, retention, the customer summary, the sales warnings and their override, packagings on lines, the variant grid, the analysis views and their CSV files, deliveries and stock — the site, reservations at confirm and on edits, the delivery, its lots proposed FEFO, chosen and posted per lot, its pick list and packing slip, the back order and invoicing on delivery —, shipping — carrier accounts and their sealed keys, shipments booked once under their key, labels and waybills, the tracking worker, the shipping notice and the shipping line with its estimate —, the refusal codes, the assist targets and every endpoint with its permission and status codes.; who received a lot — contracts.DeliveryTrace for Inventory's trace, provider-gated on orders:access; make-to-order — contracts.SalesOrderLines for Manufacturing, its Unreserved demand for replenishment, a goods line's production through contracts.Production, and the orders.mto-reserve handler's produced, ready to reserve; kits — a kit line's child lines copied from contracts.KitStructure, rescaled with it, reserved, delivered and invoiced through it, and its availability"
sources:
  - apps/server/internal/orders
  - apps/orders/frontend
  - openapi/orders.yaml
  - apps/server/internal/openapi/specs/orders.yaml
  - apps/server/internal/db/migrations/00119_orders_baseline.sql
  - apps/server/internal/db/migrations/00120_orders_warnings.sql
  - apps/server/internal/db/migrations/00121_orders_allocations.sql
  - apps/server/internal/db/migrations/00124_orders_subscriptions.sql
  - apps/server/internal/db/migrations/00126_orders_deliveries.sql
  - apps/server/internal/db/migrations/00127_orders_shipping.sql
  - apps/server/internal/db/migrations/00161_orders_mto_ready.sql
  - apps/server/internal/db/migrations/00166_orders_delivery_lots.sql
  - apps/server/internal/db/migrations/00177_orders_kits.sql
  - apps/server/internal/contracts/production.go
  - apps/server/internal/contracts/kit_structure.go
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
views — delivery B, invoicing from orders ([Invoicing from orders](#invoicing-from-orders)),
delivery S, subscriptions ([Subscriptions](#subscriptions)), and delivery C, deliveries and
stock ([Deliveries and stock](#deliveries-and-stock)), and delivery D, shipping
([Shipping](#shipping)).

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
[Invoicing from orders](#invoicing-from-orders)) — and `subscriptions` and
`subscription_skips` (migration `00124`, [Subscriptions](#subscriptions)), which also lets
`lines` belong to a subscription, dated by `valid_from` and `valid_to`, the change log
hold a subscription's, and an allocation bill a subscription's period.

## Permissions

| Key | Display | Covers | Sensitive | Delegable |
| --- | --- | --- | --- | --- |
| `orders:access` | Use Orders | See the app; read quotes, orders, templates, the customer summary, the salespeople and the catalog search | no | yes |
| `orders:quotes-manage` | Manage quotes | Create, edit, send, mark as sent, revise, accept, decline, cancel, copy and delete quotes; apply a template | no | yes |
| `orders:sales-manage` | Manage orders | Create and edit orders, hold and release, cancel and cancel what remains, register and undo delivered, send the confirmation, release the draft allocations, delete a draft | no | yes |
| `orders:sales-confirm` | Confirm orders | Confirm, lock and unlock; accept a quote into a confirmed order | no | yes |
| `orders:settings` | Manage Orders settings | The settings, the number series, the quote templates and the sales warnings; overriding a block warning | no | no |
| `orders:subscriptions-manage` | Manage subscriptions | Create, save, activate, pause, resume, end and cancel subscriptions; accept a quote into a subscription | no | yes |
| `orders:deliveries-manage` | Manage deliveries | Create, save, post and cancel deliveries (delivery C); create, save, price, book and cancel shipments, and list pickup points (delivery D) | no | yes |
| `orders:carriers-manage` | Manage carriers | Carrier accounts and their keys, Verify and the default account per site, under Integrations › Shipping (delivery D) | yes | no |
| `orders:analysis-view` | View sales analysis | The analysis views and their CSV files | no | yes |

Every operation asks for `orders:access` **and** its own key, so a role that gives
`orders:quotes-manage` without `orders:access` gives nothing.

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
  request's — any of the customer's addresses, of any type; an id the customer does not have
  is a field error on `deliveryAddressId` — else the customer's primary *delivery* address,
  else none: a visiting, postal or invoice address is never chosen by itself) and the **contact** (the request's,
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
kinds (O15): **product**, **section** (a heading), **note** (text printed between the
lines) or, on a quote or an order, **shipping** ([The shipping line](#the-shipping-line)). A save replaces the lines whole; a line that names its `id` is kept as that line.
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

An order's **kit line** has **child lines**, one per component, which the server writes
from it (`parentLineId`, `perKit`): see [Kits](#kits).

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
`locked`, `unlocked`, `delivered_marked`, `delivered_undone`, `delivered_beyond_kits` (a
cancel of the rest left a kit's component delivered beyond the whole kits kept: to is the
excess, delivered and never invoiced — see [Kits](#kits)), `warning_overridden` (a
block warning overridden at confirm: from is the warning, to the reason) and
`draft_allocations_released` (to is how many were released). An invoice's issue and a
credit note's release are not logged: the order's **Invoices** list is their record.

### Delivered by hand

Delivery is registered by hand (O32) on a line no [delivery](#deliveries-and-stock) takes —
a service, a free-text line, any line of an order without a site or while Inventory keeps no
stock — and, as a correction, on any line of a confirmed order: **mark-delivered** adds per-line quantities, never beyond what is
ordered less cancelled (`over_delivery`, 400), and **undo-delivered** takes them back,
never below 0 (`undo_exceeds_delivered`) nor below what is invoiced
(`delivered_below_invoiced`). A line not on the order is `line_not_found`; a quantity that
is not a positive number with at most three decimals is `quantity_invalid`. Every line of
the request is judged at once. Each marking logs `delivered_marked` or `delivered_undone`
and recomputes the delivery status. A locked order still takes it; an order on hold takes
neither (`order_on_hold`, 409) — release it first. Marking and undoing lock the lines'
variants, since an open line holds its variant against a delete. A marking releases what a
line holds reserved beyond its open quantity — as much as it marked — and an undo reserves
nothing again ([Reservations](#reservations)). On a line invoiced on delivery, undo stops at
what is invoiced (`delivered_below_invoiced`, 400) and, beyond that, at what is invoiced plus
what draft invoices hold of it (`line_in_draft_invoice`, 409): the draft is issued or deleted,
or its allocations released, first.

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
carries its line and its order — or, for a subscription period, its subscription
([Subscriptions](#subscriptions)) — its `kind` (`order_line`, or `subscription_period` with
its `period_start` and `period_end`), its `quantity` (three decimals), the line's `unit_price`
(eight decimals) and `discount_percent` and `vat_code_id` when it was made, its `amount` —
docmath's line net of those three numbers (O37), so the invoice line built from them
always nets the same —, its `state` and `revision` (1: a row is never rewritten), and an
invoice's stamp.

| State | Means | Moves to |
| --- | --- | --- |
| `draft` | A draft invoice holds the quantity; it is not offered again — for as long as the draft exists | `invoiced` by the issue; `released` by Invoices after a draft is deleted or its line removed — or by its [reconcile](/en/reference/invoices/#releases) when that call never arrived — or by the order's [release](#release-draft-allocations) |
| `invoiced` | Stamped by an issued invoice: `invoice_id`, `invoice_number`, `issue_date`, `invoiced_at`, `invoiced_by` and `invoiced_by_display` | `released` by the credit note that returns its invoice line in full |
| `released` | Its quantity went back to the line; a credited one keeps the invoice's stamp as the order's history | — |

What a line has **to invoice** is, for a line invoiced on order (`invoice_policy`
`order`, every line in B), **ordered − cancelled − invoiced − draft-allocated**, never
below 0; on delivery (`delivery`) it will be delivered − invoiced − draft-allocated,
delivery C's. The order answers each line's `qtyDraftAllocated`.

**The lock order.** Every writer of an allocation takes the order's row first, then the
subscription's, then the lines, then the allocations, each `FOR UPDATE` in id order: `Allocate` (orders, lines),
`ReleaseAllocations`, the holder's `MarkInvoiced` and `ReleaseInvoiced`, and the order
page's release (the order, its lines, its draft allocations) — so no two of them cross.
A save of the order takes the order first and writes no allocation but the released ones
of a removed line. The table is indexed by line and state, by order, and — for
`DueOrders`' and `Allocate`'s sums of what draft allocations hold per line — by line over
the drafts alone (`ix_invoice_allocations_draft`).

### The billable read

`contracts.BillableOrders` (`billable_orders.go`, `Module().BillableOrders`,
`Deps.BillableOrders`) is a single-provider slot resolved after `Consumption`, only from
the enabled modules — nil when Orders is off. It reads and writes on the pool and the
clock and never reads a directory; its caller never calls it under a lock of its own.

- **`DueOrders`** — the documents a request names (`CustomerIDs`, the documents —
  `OrderIDs` and `SubscriptionIDs` — or `Every`, exactly one), each with its lines left to
  invoice; a `DueOrder`'s `Kind` is `order` or `subscription`, and an order and a
  subscription are numbered apart, so a caller keys a document by its kind and id. An
  order answers number, customer, currency, project,
  your reference, delivery date, delivery name and address, payment terms and, per line,
  its position, text, unit, `ToInvoice`, unit price, discount, VAT code, invoice policy and
  amount. `Until` bounds the orders by their delivery date, on or before; an order with
  none is always due. A draft order and one on hold are answered with `Blocked` (`draft`,
  `on_hold`), so the caller can say why. A subscription answers its due periods' lines
  ([Billing a period](#billing-a-period)), each with its `PeriodStart` and `PeriodEnd` —
  `Until` is then the date the periods are due through, today in the business time zone
  when zero; the twelve-month limit is judged from that day too.
  At most 5 000 lines (`MaxBillableRows`), `More` set when there were more. It writes
  nothing.
- **`Allocate`** — a provider command (module boundaries rule 5): a new draft allocation
  per line named, on one transaction of its own. It reads the lines' orders, then locks
  the orders `FOR UPDATE` in id order, then the lines, so two Allocates serialise on the
  orders and the second sees what the first allocated; it locks no variant (MB rule 21
  does not apply — an allocation writes no line). Each line is judged under the locks, the
  first refusal answering and nothing written — an `AllocationRefusal` with the line and
  a code: **`line_not_invoiceable`** for a line that is not a priced product line with a
  VAT code invoiced on order, is a kit's child line ([Kits](#kits)), or whose order is a
  draft, on hold or cancelled;
  **`allocation_exceeds_open`** for a quantity above what the line has to invoice. The
  allocation names its order's project only while Projects is composed. A request may
  name `Subscriptions` too, each with a `Through` date: [Billing a
  period](#billing-a-period).
- **`Allocations`** — the allocations named, as they stand; a missing id is absent.
  Every allocation `Allocate` and `Allocations` answer carries its line's `VariantID`
  (nil for a line of free text), from which Invoices reads the product's tax category
  through the catalog, so an issued line remembers it
  ([What a line was made from](/en/reference/invoices/#what-a-line-was-made-from)).
- **`ReleaseAllocations`** — a provider command: the named drafts released on one
  transaction of its own, after it has locked their orders, their lines and the
  allocations in that order ([the lock order](#allocations)); one invoiced, released or
  missing is left as it is, so a repeat is harmless.

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
  a revision, currency or exact amount other than the source's. A subscription period's
  allocation is judged by its subscription instead — `source_not_invoiceable` when it is
  a draft or cancelled — and has no line progress. The first refusal rolls the issue
  back. Otherwise every allocation is stamped with the `InvoiceRef`, its
  quantity added to its line's `qty_invoiced`, each order's invoice status recomputed and
  its `updated_at` moved to `IssuedAt` — its version is not bumped and nothing is logged,
  as for a delivery marking — and **`orders.sales_order.invoiced`** published once per
  order ([Events](#events)): the one holder that publishes (module boundaries rule 10).
  A subscription's `billed_through` is derived again ([Billed
  through](#billed-through)) and stored, with its `updated_at` at `IssuedAt`.
- **`ReleaseInvoiced`**, on the credit note that returns an allocation's invoice line in
  full — the last of several partial credits included — locks the same way and moves each
  allocation carrying that invoice's stamp to `released`, the stamp kept; its quantity is
  taken off the line's invoiced, so it is to invoice again, and the invoice status
  recomputed. One without the stamp is left as it is, with a warning logged: a credit
  note is never blocked. Nothing is published. A subscription period released so is due
  again, alone, and the subscription's `billed_through` is derived again.

### Release draft allocations

`POST /orders/{id}/release-draft-allocations` (`orders:sales-manage`, the version and
`expectedUpdatedAt` as every write) releases every draft allocation of the order at once —
under the order's row, then its lines, then those allocations — so its quantities are
offered to invoice again. A draft invoice still holding one is then
refused at its issue (`source_not_invoiceable`). It is logged as
`draft_allocations_released` with how many, at the version it found, and moves the
order's `updatedAt`; an order with none is answered as it is, and nothing is logged.

Invoices owns the cleanup of what it allocated: it releases the allocations a draft held
after the delete — or the save that dropped a line — has committed, best effort, and what
a failed release, a customer's anonymisation or an interrupted wizard leaves, its
`invoices.allocation_reconcile` worker releases once no draft holds it
([Releases](/en/reference/invoices/#releases)). Orders releases nothing by age: a draft
invoice holds its allocations for as long as it exists.

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
`amount` (the net it still bills of this order — its allocations still `invoiced` — in
the order's currency), `releasedAmount` (the net credit notes gave back of it — its
allocations `released` since), `currency` and `state`, `invoiced`, or `released` once
every allocation of it was given back by a credit note, its `amount` then 0. The page shows it as the **Invoices** card, and **Invoice** — needing
`invoices:access` and `invoices:create`, disabled on hold, with nothing left to invoice or
with unsaved changes — opens Invoices' wizard at `/invoices/new/from-orders?orderId=`.

## Subscriptions

Delivery S (O42–O44). A **subscription** (*abonnement*) is its own aggregate, in
`orders.subscriptions` (migration `00124_orders_subscriptions.sql`): services billed per
period, through the same allocations, billable read and holder as an order's lines
([Invoicing from orders](#invoicing-from-orders)). This phase sells services only.

### The model

A subscription has the [document header](#the-document-header) of a quote and an order
— the customer and its snapshot, your reference, salesperson, project, currency, VAT
mode, payment terms, discount visibility, language and texts — without a delivery date;
a delivery address is taken as an order's but is never required. Its **number** comes
from the `subscription` series, which has no setting of its own and starts at 1. Its
origin is O27's: `manual` by hand, `quote` when a quote was accepted into it.

**The plan**:

| Field | Values | Rule |
| --- | --- | --- |
| `interval` | `monthly`, `quarterly`, `half_yearly`, `yearly` | Fixed once active |
| `billing` | `in_advance`, `in_arrears` | Fixed once active |
| `startDate` | a day | Fixed once active |
| `endDate` | a day or none | Set by [End](#pause-resume-end-and-cancel) once active; on or after the start |
| `noticeMonths` | 0–12 | Informational, printed |
| `invoiceDay` | 1–28 or none (the period's first day) | Informational, printed: it does not change what is due |
| `finalPeriod` | `full`, `prorated` | How the period the end falls in is billed ([The final period](#the-final-period)); a new subscription takes the settings' `subscriptionFinalPeriodDefault` unless its create or plan names one; editable while active, paused or ended |

**The states:** `draft`, `active`, `paused` (with `pausedFrom`), `ended` (with an
`endDate`; its periods up to the end are still billed) and `cancelled`. `version` is the
concurrency token's version: a draft's saves leave it, every lifecycle step and every
change of an activated subscription bumps it; every write sends it with
`expectedUpdatedAt` ([Concurrency](#concurrency)).

**The lines** are `orders.lines` with `owner_kind` `subscription`: product lines whose
variant must be a **Service** product (`subscription_goods_not_allowed`), free-text
lines, sections and notes; no optional lines (`optional_not_allowed`). They are **dated**:
`valid_from` and `valid_to`, both inclusive and NULL open, and a line bills a period when
it is in force on the period's first day. A draft's lines are undated. A subscription's
lines share positions across their dated versions, so `ux_lines_owner_position` holds
for the other owners only. A line's versions form a **chain**: `chain_id` is NULL on the
first and the first's id on every later one, so a change finds every version of the line
it changes, and at any date exactly one version of a line is in force. The answer's
`lines` are those in force from the latest change on (`valid_to` NULL), its `totals` a
full period's at them, and `earlierLines` the ones a change ended.

### The period calendar

The periods run from the **first of the start's month** by the interval: period *k*
covers [anchor + *k*·*n* months, anchor + (*k*+1)·*n* months − 1 day], so a month ends on
its last day, February on the 28th or, in a leap year, the 29th. The first period begins
on the start date itself. A period starting after the end date is not one of the
subscription's; the period the end falls in is billed as `finalPeriod` says ([The final
period](#the-final-period)).

**Proration.** When the start is not the first of a month, the first period is
**partial**, and each line bills docmath's net of the full period times the period's days
over the full period's days, rounded to øre — with a pro-rated final period, the only
pro-rating of this phase. It is
billed as the line's quantity at a unit price of that amount ÷ the quantity at eight
decimals and no discount, so docmath, which Invoices builds its line by, reproduces the
amount exactly (O37); where eight decimals cannot carry it, as a quantity of 1 at the
amount. A monthly subscription started on 31 January bills 1/31 of the month for the
period 31 January–31 January, then whole months.

### The final period

When the end is not the last day of the period it falls in, `finalPeriod` says how that
period is billed (owner, 2026-11-06):

- **`full`** — the whole period, to its last day, as any other;
- **`prorated`** — the period **ends on the end date**, and each line bills its days from
  the period's start to the end, inclusive, over the full period's days, to øre, at the
  same eight-decimal unit price as a partial first period (a quantity of 1 when eight
  decimals cannot carry it). A period both first and final pro-rates over its actual
  span, from the start to the end. The period's `end`, its allocation's `period_end` and
  the invoice line's period are the end date, and it is answered `partial`.

A new subscription takes the settings' `subscriptionFinalPeriodDefault` (`full` until it
is set) unless its create, or the plan of a quote accepted into it, names one.
[End](#pause-resume-end-and-cancel) may name one; the subscription's own applies when it
does not, and the one used is stored on the subscription and logged (`final_period`).
`prorated` is refused with `final_period_billed` (409) while the final period has a live
allocation — draft or invoiced — and the end is not its last day: the user credits it
first. `full` is always allowed. A save of the plan that makes an ended subscription's
final period `prorated` is judged the same way.

### Billing a period

A period is **due through** a date when its first day is on or before it (`in_advance`)
or its last day is (`in_arrears`); and never when it starts more than **twelve months**
after today (bokføringsforskriften § 5-2-6), however far ahead the date asked. A
subscription bills its periods while it is `active` or `ended`, and while `paused` its
periods before `pausedFrom`; a period recorded in `orders.subscription_skips` is never
billed.

**The per-period allocation is the truth** (O43). `Allocate` with `{SubscriptionID,
Through}` locks the subscription (after the orders, before any line) and writes one draft
allocation per period due through the date and per line in force on its first day that
has no live allocation — `kind` `subscription_period`, `period_start`, `period_end`, the
period's quantity, unit price, discount and amount. A subscription gone, a draft or
cancelled is refused `subscription_not_invoiceable`; one with no such period
`nothing_due` (`AllocationRefusal` with `SubscriptionID`). The unique index
`ux_invoice_allocations_live_period` on `(line_id, period_start) WHERE kind =
'subscription_period' AND state IN ('draft', 'invoiced')` makes billing one line's period
twice impossible, whatever writes it. A period is due only while a line in force has no
live allocation for it, so a credit note that returns January in full makes January due
again — and nothing else: February stays invoiced.

`OrderAllocation.Kind` is `subscription_period` for one, its `OrderID` and `OrderNumber`
the subscription's, and its text the line's description; Invoices adds the period to the
line ([Invoicing orders](/en/reference/invoices/#invoicing-orders)).

### Billed through

`billed_through` is derived, never written by hand: the last day of the unbroken run,
from the first period, of periods **invoiced** — every line in force on the period's
first day has an invoiced allocation for it — or **skipped**; NULL when the first is
neither. It is stored for the lists and derived again by the holder at every issue and
credit, at a resume and at the end of a paused subscription.

The subscription's page answers `periods`: from the first through the later of today and
the last period billed or skipped, and three more, bounded by the end (at most 120, the
latest kept) — each with its days, whether it is `partial`, its `amount` (the live
allocations', else what it would bill) and its `state`: `invoiced`, `draft` (a draft
allocation), `skipped`, `paused` (from `pausedFrom` on), `due` (today) or `upcoming`, and
the numbers of the invoices that billed it.

### Changes

A draft's `PUT` replaces its header, plan and lines whole. An activated subscription's
(`active`, `paused` or `ended`) takes the header's texts, references, contact, delivery
address, salesperson, project, payment terms, language and discount visibility, and
`noticeMonths` and `invoiceDay`; the customer, the currency, the VAT mode
(`subscription_not_editable`, 400) and the plan's interval, billing, start and end are
fixed; `finalPeriod` is not ([The final period](#the-final-period)). Its lines are as
they are to be from **`effectiveFrom`** on — a period start
(`not_a_period_start`, 400) no earlier than `nextChangeFrom`, the period after the last
with a live allocation (`change_in_billed_period`, 409), which is the default:

- a kept line unchanged keeps its row, and takes its new position;
- a line changed — its item, quantity, price, discount, text, unit or VAT code — ends the
  day before (`valid_to`) and a new row starts on `effectiveFrom` in its place, with what
  the old one was valid to;
- a line removed ends the day before;
- a line added starts on `effectiveFrom`;
- a changed or removed line's other versions still in force on or after `effectiveFrom`
  — a version an earlier-saved change scheduled for a later period start, and the version
  before it, which that change ended later — end the day before too, so a change saved
  from a date before one already scheduled replaces it.

A save that changes no line judges no `effectiveFrom`. A line that would end before it
started — changed again from its own first period — is
deleted, or, when an allocation once named it, kept with `valid_to` the day before its
`valid_from`. Kept lines are priced from their own rows, as a confirmed order's are. A
change is never pro-rated. It is logged with `effective_from` and the lines' actions,
bumps the version and publishes `orders.subscription.changed`.

### Pause, resume, end and cancel

| Step | From | Does |
| --- | --- | --- |
| `activate` | `draft` (`subscription_not_draft`) | Judges the customer (`customer_disabled`), a product line (`subscription_empty`), every product line priced (`line_unpriced`) with a VAT code (`vat_code_unmapped`) and still a service (`subscription_goods_not_allowed`); records who and when; publishes `orders.subscription.started` |
| `pause` | `active` (`subscription_not_active`) | Billing stops from `from` — a period start with no live allocation in it or after it — else the first period starting after today, or the first without a live allocation when later; publishes `orders.subscription.changed` |
| `resume` | `paused` (`subscription_not_paused`) | Billing restarts from `from`, a period start on or after the pause; every period between is written to `orders.subscription_skips`; publishes `orders.subscription.changed` |
| `end` | `active`, `paused` or `ended` (`subscription_not_active`) | Sets `endDate`, with no period with a live allocation starting after it (`end_before_billed`), and `finalPeriod` — the one asked, else the subscription's own — refused `final_period_billed` when it is `prorated` and the final period has a live allocation ([The final period](#the-final-period)); a paused one's periods from the pause to the end are skipped and billed through derived again; status `ended`; publishes `orders.subscription.ended` |
| `cancel` | `draft`; `active` or `paused` with no live allocation (`subscription_billed`) | Status `cancelled`; an activated one publishes `orders.subscription.ended` with `status` `cancelled` (`subscription_not_cancellable` otherwise) |

Each is logged on the change log (`activated`, `paused`, `resumed`, `ended`,
`cancelled`); an end also logs `final_period`, from the subscription's to the one used.

### Accept a quote as a subscription

`POST /quotes/{id}/accept` with `into` `subscription` (O20) also needs
`orders:subscriptions-manage` (403) and a `plan` (400 without one, or with one for
another `into`). The quote's chosen lines must be services or free text
(`subscription_goods_not_allowed`, judged against the catalog before the transaction);
they become a draft subscription's lines with their frozen prices, its origin `quote` and
`quoteId@revision`. The quote records `accepted_into_kind` `subscription`, and
`orders.quote.accepted` names it. The answer carries `subscription` instead of `order`.

## Make-to-order

Manufacturing makes goods for sales orders (manufacturing design M43–M48;
[Manufacturing](/en/reference/manufacturing/#make-to-order-and-sub-assemblies)). Orders
gives it the lines and shows on the order what is made for them.

### The sales order lines

Orders provides **`contracts.SalesOrderLines`** (`contracts/production.go`), resolved
from the enabled modules only. It is **consumer-gated** (module-boundaries rule 12, M43a):
Orders authorises nothing, because Manufacturing's worker calls it with no principal and
Manufacturing decides who may see what. `Lines(ids)` and `OrderLines(orderID)` answer the
product lines naming a variant of sales orders — never a quote's, a template's or a
subscription's — in line order: the line and order ids, the order's number, the
customer's **id** (never the name), the variant, the **open quantity** (ordered less
cancelled less delivered, at least 0, exact decimal text), the order's delivery date,
site and status. An id that is not such a line is absent. It reads on the pool and refuses
more than 500 ids.

### Unreserved demand

`SalesOrderLines` has a third call for Manufacturing's
[replenishment](/en/reference/manufacturing/#replenishment) (manufacturing phase 2
M107a): **`Unreserved(variantIDs, siteID)`** answers, per open goods line of a
**confirmed** sales order fulfilled from the site — a product line of one of the variants,
delivered by delivery documents, with something open — the open quantity less what the
line holds reserved at the site, **more than 0 only**: an `UnreservedDemand` with the
order id, the line id, the variant and the quantity as exact decimal text. A line
confirmed without reserving counts whole; one reserved short counts what is short; a
draft's never. What a line holds is Inventory's to say: the lines and their orders'
reservations are read in **one read-only, repeatable-read snapshot** on the pool, the
reservations through `StockLedger.ReservationsForSources` on that transaction, one
statement per 1000 orders — the one connection the call holds. It is consumer-gated like the other two and carries no
personal data; Manufacturing shows only the sum. More than 5000 variants is a failure.

### Production on the order

Every answer of one order (`GET /orders/{id}` and every write's) adds, after the order's
transaction and on the pool, for each product line naming a variant:

- **`hasBom`** — whether the variant has a default active bill of materials in
  Manufacturing — and **`production`** — the manufacturing orders made for the line,
  backorders included, each with its `number`, `status`, `quantity`, `produced`, `siteId`,
  `plannedFinish` and `dueDate` — both read through **`contracts.Production`**. It is
  **provider-gated** (M46a): Manufacturing checks `manufacturing:access` on the request
  and answers nothing to a caller without it, and Orders checks nothing itself. With
  Manufacturing off, or nothing answered, the two fields are left out. A failure fails
  the answer;
- **`notMade`** — `{code, detail}` — on a line with no manufacturing order, when the
  production is answered: why make-to-order made none, read through
  `Production.NotMade` for the order (M44a). It is the line's own reason — `no_site`,
  `nothing_open`, `variant_not_goods`, `sales_order_line_has_order` or the code of the
  Manufacturing rule that refused the order —, else, on a line whose variant has a bill
  of materials, `failed` when the order's request failed after its last attempt. Left
  out otherwise;
- **`readyToReserve`** — `{manufacturingOrderId, produced, siteId, otherSite}` — once a
  manufacturing order made for the line closed: the latest one, what was produced for the
  line in all, the site it was made at, and `otherSite` when that is not the order's site.
  `manufacturingOrderId` is Manufacturing's id, named only to a holder of
  `manufacturing:access`; the rest is there for every caller.

The order page shows each manufacturing order, linking to it for a holder of
`manufacturing:access`, and offers **Create manufacturing order** — Manufacturing's
new-order form prefilled from the line — on a line with a bill of materials and no order
yet, to a holder of `manufacturing:access` and `manufacturing:orders-manage`. On a line
with `notMade` it says *No manufacturing order made:* and the reason, to a holder of
`manufacturing:access`.

### Produced, ready to reserve

The handler **`orders.mto-reserve`** takes `manufacturing.order.done` v1 and only marks
the line in Orders' own schema (rule 23, M48a): a row of `line_production_ready` per
line and manufacturing order closed for it — a table rather than a column of `lines`,
because a save replaces a document's lines whole — with the order's site and what it
produced. The row is keyed by the two, so one order's event handled twice counts its
goods once; a backorder chain's orders are a row each. The line reads as one mark: the
latest order closed and its site, and every row's produced summed. A line that is no
longer an order's, and a close that produced nothing, mark nothing. The handler reserves nothing: the order page
then says the goods are ready and offers its existing **Reserve now**
([Reservations](#reservations)), which reserves at the order's site. When the goods were
made at another site, Reserve now cannot pick them up, and the page says a transfer is
needed first. Migration `00161_orders_mto_ready.sql` adds the table.

## Kits

A kit is goods sold together under one variant and delivered as its components
(manufacturing phase 2 M145, M146; [Manufacturing](/en/reference/manufacturing/#kits)).
A reservation names one variant — the ledger's reserve refuses a line id with two — so
each component is an order line of its own, a **child line** of the kit line:
`orders.lines.parent_line_id` names the kit line (cascading: a kit line deleted takes its
children) and `per_kit numeric(14,3)` how much of it one kit holds, set together or not at
all (migration `00177_orders_kits`). `OrdersLine` answers both as `parentLineId` and
`perKit`, absent on every other line. Kits are an order's: a quote, a template and a
subscription have none.

### The copy

The composition is copied onto the order when the kit line is **added** — a new product
line with a variant and a quantity, or a kept line whose variant changed — from
Manufacturing's **`contracts.KitStructure`** (`contracts/kit_structure.go`), read on the
pool before the save's transaction (`withKits`, from `priceOrderInputs` on a create and on
a draft's or a confirmed order's save, and on an accept into an order). A variant
KitStructure answers is a kit; each component becomes a child line after its kit:

- its catalog snapshot (SKU, name, unit, product) from the catalog, read with it — a
  component the catalog no longer answers is **400** `variant_not_found` on the kit's
  line;
- its quantity the kit's × per kit, half-up at three decimals — 0 is **400**
  `quantity_invalid` on the kit's line;
- a kit KitStructure answers with **no** component (present, empty — absent is "not a
  kit") is **400** `line_invalid` on the kit's line, never sold as an ordinary line;
- price **0**, price source `manual`, no discount or packaging, the kit line's VAT code;
- its delivery method and invoice policy settled as any goods line's.

A **kept** kit line keeps its stored children under their ids, rescaled to its quantity
the same way, in the same transaction — so a confirmed order's reservations follow each
child as any edited line's ([Reservations](#reservations)), and the change log names each
child's quantity change. KitStructure is not read again for it: a later BoM change never
reaches an order's existing kit lines, and a line added before its variant became a kit
stays an ordinary line. A save sends the kit line alone; an input line naming one of the
order's child lines by id is **left out** before pricing, so a client sending the lines
back as read changes nothing. A removed kit line takes its children with it, under the
removal's rules (a child with progress refuses it, `line_has_progress`). The children count
towards the 500 lines.

Without Manufacturing (a nil slot) nothing is copied: a kit variant is an ordinary goods
line. KitStructure is ungated, so the author needs no Manufacturing permission.

A refusal naming a line names it by its **row in the request** — the editor holds a kit
line and not its children — on a create, a save, a confirm, Reserve now and an accept into
an order: every line-indexed refusal (`OrdersProblemLine.index`), whether it is judged on
the request's lines, on the lines with the children added, or on the stored lines (a
reservation's `insufficient_available`, `quantity_below_progress`, `variant_not_found`,
`line_unpriced`, …), is mapped back to the request's index, a child's to its kit's
(`atInputs`, `atTopLevel`). On a confirm and Reserve now, which have no request lines, the
index is the line's among the order's lines that are no kit's child.

### Stock, delivery and invoicing

- **Reservations.** A line with children is not **reservable** (`reservable(l, kitsOf(lines))`);
  its children reserve, are released and follow edits as any goods line.
- **Deliveries.** The kit line is never proposed, named on a delivery
  (`line_not_found`) or picked; its children are, with their lots. When a delivery is
  posted, the kit line's delivered becomes the **whole kits** its children make — the least,
  over them, of delivered ÷ per kit, rounded down to three decimals, at most ordered less
  cancelled (`syncKits`) — so the order's delivery status and the kit's invoicing on
  delivery follow.
- **Delivered by hand** ([Delivered by hand](#delivered-by-hand)). A kit line delivered by
  delivery documents is never marked by hand: **mark-delivered** and **undo-delivered**
  refuse it, **400** `line_invalid` on its row; a child of it marked by hand brings the kit
  to the whole kits its children make, as a posted delivery does. An **undo** of a child
  holds the kit, which carries the invoice, to the rule of a line of its own: invoiced on
  delivery, the whole kits left may not fall below what is invoiced of the kit (**400**
  `delivered_below_invoiced` on the child's row) nor below that and what a draft invoice
  holds of it (**409** `line_in_draft_invoice`). A kit line marked by hand
  (an order without a site, or Inventory off) marks its children **in step** in the same
  transaction — each child's delivered becomes the kit's × per kit, half-up at three
  decimals, within 0 and its ordered less cancelled, logged `delivered_marked` or
  `delivered_undone` on the child — and a child of such a kit is not marked on its own
  (`line_invalid`).
- **Invoicing.** The kit line carries the price and is the only one offered to invoice: the
  billable read leaves child lines out, and **Allocate** refuses one
  (`line_not_invoiceable`). When the kit's invoiced quantity moves — an issue or a credit
  through the invoiced-work holder — each child's invoiced becomes the kit's × per kit, at
  most its quantity. A child line **never counts toward the order's invoicing** — its kit
  does: the invoice status leaves children out; **done** — on the order, in the list's
  `done` and `open` filters and in the retention sweep — asks of a child only that
  cancelled and the larger of delivered and invoiced cover it; and the customer summary's
  delivered-not-invoiced, the backlog's delivered-not-invoiced and the variant holder's open
  orders leave a child's invoicing out.
- **Cancel the rest.** Each line is cancelled as any line, a kit at what is not delivered
  of whole kits and invoiced. A component delivered **beyond the whole kits** the kit keeps —
  its delivered over the kit's ordered less cancelled × per kit, as when one component was
  delivered and another not — is logged `delivered_beyond_kits` on the component's line, to
  the excess, in the cancellation's change log entry: that much was delivered and is
  **never invoiced**, as only whole kits carry a price. The order still reaches invoiced and
  done when the kept kits are invoiced.
- **Availability** (M146). On the order's read, a kit line's `available` is the least, over
  its children, of what is available of the child's variant at the site ÷ per kit, rounded
  down to three decimals; null when one has no figure.
- **Manufacturing.** `SalesOrderLines` — its lines and its unreserved demand — leaves the kit
  line out and answers its children, so make-to-order and replenishment see the components;
  `Production.HasBOM` answers false for a kit, so the line offers no manufacturing order.
- **Printing.** The pick list prints the children only; the packing slip prints a row of the
  kit — its description and what was ordered of it — with its children under it, indented;
  the order's and quote's PDF prints the children under their kit, with no price.

## Deliveries and stock

Delivery C (O45–O48) brings stock into an order: the site it is fulfilled from, the
reservations its goods lines hold there, the deliveries that issue the stock, the back
order and invoicing on delivery. Inventory serves it through two contracts
([the stock contracts](/en/reference/inventory/#reservations-and-issues)): `contracts.Stock`,
a read of sites, on hand, available and the locations holding a variant, nil when Inventory
is off; and `contracts.StockLedger`, which reserves, releases and issues on Orders' own
transaction (module-boundaries rule 24) and is composed whatever `MODULES` says.

**Today and the override travel as data** (manufacturing phase 2 M62, M63). The ledger
makes no contract call inside Orders' transaction, so every write that reserves — a
confirm, an accept and confirm, *Reserve now*, a confirmed order's save — reads the
business day on the pool before its transaction and passes it to `Reserve` as `today`,
the day a lot's expiry is judged on; a delivery's post reads the day and whether the
poster holds `inventory:expired-override` the same way and passes them as the issue's
`Today` and `ExpiredOverride`. A tracked variant's delivery line issues one ledger line per
lot it picks ([Lots on a delivery](#lots-on-a-delivery)); an untracked line names none
(`LotID` 0). Orders never sets `AutoLot`: every lot a delivery takes is on the delivery,
proposed or chosen, before the post. `Issue` answers a `StockPosted`, whose `MovementID`
the delivery keeps.

### The site and the line's delivery method

An order carries `site_id`, Inventory's site, opaque here. A new order — the API's create,
an accepted quote — takes the settings' `defaultSiteId` while Inventory keeps stock
(`Deps.Stock`); the API's create may name one. A draft's save sets it from the body (absent
or null is none); a confirmed order's save keeps it unless the body names another, which is
refused once the order has a draft or posted delivery (`order_has_deliveries`, 409). Every
site a request names is checked through `Stock.Sites`: an active site that fulfils orders,
else `site_not_fulfilling` (400).

Every save settles each line's **delivery method** under the variants' locks: `delivery` for
a product line whose variant the catalog calls Goods on an order with a site while Inventory
keeps stock, `manual` for every other line. A line whose variant the catalog no longer
answers keeps what it had. The order answers the site as `siteId`, and each line, after
the transaction, its `qtyReserved` — what Inventory holds for it, summed over sites,
read through `ReservationsFor` — and `available`, `Stock.Available` of its variant at the
site (null without stock, a site or a goods variant; negative after Inventory's own
corrections).

### Reservations

What a line holds is Inventory's to say; Orders keeps no copy. The ledger names an order
`{orders, order, <id>}` and a line by its id, and every call runs after
`ProductCatalog.LockVariants` and the order's row lock (MB rule 21, rule 24).

- **Confirm** reserves every delivery-method line's open quantity — ordered less cancelled
  less delivered — at the site in one `Reserve`. A shortfall refuses the confirm with 409
  `insufficient_available`, naming every short line in `lines` by its index in the order.
  `reserve: false` in the confirm body confirms without reserving and logs
  `confirmed_without_reserving`. Accepting a quote into a confirmed order reserves.
- **Reserve now** (`POST /orders/{id}/reserve`, sales-manage) reserves what of each line's
  open quantity is not held yet, logging `reserved`; a shortfall refuses it all, 409
  `insufficient_available`. Without stock it is 503 `dependency_unavailable`; without a site
  409 `order_has_no_site`.
- **A confirmed order's save**, while the order holds any reservation, releases a removed
  line's reservation and what a line holds beyond its new open quantity, and reserves what a
  kept line's open quantity grew by and a new line's whole — refused as confirm is. An order
  holding none — confirmed without reserving — reserves nothing on a save. A kept line whose
  item changed releases all it holds and reserves its whole open quantity of the new
  variant, refused as confirm is. While Inventory keeps no stock (`Deps.Stock` nil) a kept
  line keeps its stored delivery method — unless its item changed — so a save releases only
  a removed line's and what a line holds beyond its open quantity, never because Inventory
  is off, and reserves nothing; the order is as Inventory left it when it is switched on
  again. A new site
  releases everything at the old one and reserves the open quantities at the new one when
  the order held anything; it is logged `site_changed`.
- **Cancel** releases every reservation and cancels the order's draft deliveries.
- **Cancel remaining** releases what each line holds beyond what is still open — a line
  invoiced beyond what is delivered keeps that much open — removes from the draft
  deliveries the lines of order lines with nothing open left, bumping those drafts'
  `version`, and cancels a draft left with no line.
- **Mark delivered** releases what a line holds beyond what is left open; **undo
  delivered** reserves nothing.
- **Posting a delivery** issues against the reservations; Inventory consumes what it
  issued of each line's.

Releasing never asks `Deps.Stock`: a reservation made while Inventory was on is released
after it is switched off.

### Deliveries

A delivery (`orders.deliveries`, *leveranse*) has a number from the `delivery` series
(starting at 1), the order, the site, `status` `draft`, `posted` or `cancelled`, the movement
Inventory posted, who posted it and when, the stored packing slip, a `version` and its
lines (`orders.delivery_lines`): the order line, its variant, the quantity (more than 0, at
most three decimals) and the location it is picked from with its full code, null until
picked. One delivery names an order line once.

- **Create** (`POST /deliveries`, deliveries-manage) needs a confirmed order not on hold
  (`order_on_hold`, 409) with a site while stock is kept (`order_has_no_site`, 409; 503
  without stock). It proposes every delivery-method line open beyond what other draft
  deliveries hold, at what can go from the site — the line's own reservation less what its
  other drafts take, plus what is available, shared between the lines of one variant in line
  order — never more than is
  open; each proposed line is picked from the first location holding all of it — the
  variant's primary location at the site first (`contracts.StockLocation.Primary`,
  Inventory phase 2 I55), then by full code. Nothing to propose is `nothing_to_deliver` (409). Several drafts may exist.
- **Save** (`PUT /deliveries/{id}`) replaces a draft's lines: each names a delivery-method
  line of the order (`line_not_found`), once (`line_invalid`), at most what is open outside
  this delivery (`over_delivery`), and a location among `Stock.Locations` of the variant at
  the site (`location_unknown`), all 400 with `lines`. A delivery no longer a draft is
  `delivery_not_draft` (409); a stale `version` or `expectedUpdatedAt` `version_conflict`.
- **Post** (`POST /deliveries/{id}/post`) locks the variants — of the order's delivery-method
  lines and of the delivery's lines, again under the order's lock for any a change between
  the read and the lock named — the order and the delivery;
  every line must be picked (`location_missing`, 400) and within what the order line has
  left (`over_delivery`, 400); the order must be confirmed and not on hold. It calls
  `StockLedger.Issue` with the order as source and the delivery `{orders, delivery, <id>}` as
  the movement's document, one line per delivery line — idempotent by that document. A
  refusal is 409 with Inventory's code — `insufficient_stock` above all,
  `location_unknown`, `site_not_fulfilling`, `variant_not_stocked` — naming each failing line
  by its index in the delivery. The order lines' delivered quantities grow, the delivery
  status is recomputed (the version is not bumped), `delivery_posted` is logged per line,
  `orders.delivery.posted` is published and the packing slip is printed — and stored
  after the commit; a store that fails is retried by the first download.
- **Cancel** (`POST /deliveries/{id}/cancel`) cancels a draft; its quantities are open again.
  A posted delivery is immutable: a mistake is a stock correction in Inventory and **undo
  delivered** on the line.

A confirmed order's save refuses to remove a line a draft delivery names, or change its
item (`line_has_progress`, 409).

### Lots on a delivery

Manufacturing phase 2's layer O (M75–M79). A variant whose catalog `Tracking` is `lot` or
`serial` is **tracked**: its delivery line is picked by lot, not by location. Its quantity
is held in **picks** (`orders.delivery_line_lots`): one row per lot and location, with the
quantity (more than 0, three decimals), `allow_expired`, `chosen`, and the lot's number,
its expiry and the location's full code as snapshots for the PDFs — the lot and the location
are Inventory's, named by id only. The line's own `from_location_id` stays null. The
delivery answers per line `tracked`, `lots`, `short` (what the picks leave uncovered of the
quantity, 0 when none) and, on a draft, `lotChoices` — `Stock.Lots` for the variant at the
site, in FEFO order, each with its expiry, best-before, status, quantity, whether it is
expired on the workspace's today and whether it is a serial number's (`serial`).

- **The proposal** (M76). Create reads `Stock.Lots` for the order's delivery-method
  variants on the pool before its transaction, with the business day, and under the
  variants' locks fills each tracked line with `contracts.ProposeLots`: expiry first (none
  last), then received, then lot id, skipping a lot on hold and an expired one, a serial
  whole or not at all. The lines of one variant draw on the lots in line order. A line
  the lots cannot cover keeps its quantity with the picks they give; it is `short`, and
  the post refuses it until the picks add up. Proposed picks are `chosen` false.
- **Editing** (M77). Save takes, per line, `lots: [{lotId, locationId, quantity,
  allowExpired}]`. Each must be a lot of `Stock.Lots` holding the variant at that location
  (`lot_unknown`) — a pick the line already had stands even when its lot no longer shows
  there — named once per location (`line_invalid`), at a valid quantity
  (`quantity_invalid`) — exactly 1 for a variant tracked by serial number
  (`serial_quantity`); lots on an untracked line are `lot_not_allowed`. A pick with
  `allowExpired` that is new, or whose `allowExpired` or quantity changed, needs
  `inventory:expired-override`, read on the pool before the transaction
  (`expired_override_required`); a pick the line already had so, sent unchanged, stands
  for anyone. All are 400 line failures; a failure about one pick carries its `lotIndex`
  (its place in the line's `lots`) and `lotId`. Lots sent as the line had them keep their
  `chosen`; any change marks them all chosen. A tracked line sent **without** `lots` keeps
  its picks when any is chosen — a quantity changed alone leaves them, and the line is
  short or over until they add up — or while they add up to its quantity, and is proposed
  again otherwise. `proposeAgain: true` (*Propose again*, §19) proposes the line again from
  `Stock.Lots` whatever its picks, as not chosen; sent together with `lots` it is
  `line_invalid`.
- **Posting** (M78). The post reads `Today` and whether the poster holds
  `inventory:expired-override` on the pool before its transaction (rule 24). Under the
  locks, a tracked line with no pick is `lot_required` and one whose picks do not add up
  to its quantity `picks_mismatch` (both 400). Each pick is one `IssueLine` with the order
  line's id as `LineID` — so the reservation is consumed as before — its location, its
  `LotID` and its `AllowExpired`. Inventory's refusals come back through the refusal's
  `Index`: a line failure names the delivery line, and for a pick its `lotIndex` and
  `lotId` — `lot_expired` (an expired lot without both `allowExpired` and the override),
  `lot_blocked` (a lot on hold), `insufficient_stock`. A variant the post finds under the
  order's and the delivery's locks that it had not locked — a save between its read and
  its locks — is never locked after them: the transaction rolls back and the post starts
  again with every variant locked first (rule 21), at most twice more, then
  `version_conflict`.
- **Proposed again, once** (M65a). When every failing pick is `insufficient_stock` or
  `lot_blocked` on a line whose picks are all proposed, the post — after its transaction
  rolled back — reads `Stock.Lots` again, proposes those lines anew from what is there
  less what the delivery's other lines pick, and posts once more, replacing their picks
  inside that transaction; the delivery keeps the new picks. A line the lots no longer
  cover answers the first refusal; so does a refusal of the second post, whose new picks
  roll back with it — the first refusal's `lotIndex` names the picks the delivery still
  holds. A person's chosen picks are never proposed again.
- **After the post** the picks stay with the delivery; nothing else changes in the
  order's buckets. `GET /deliveries` answers each delivery's `lots`, its lot numbers once
  each in line order — a posted delivery's, a draft's as proposed or chosen so far, none
  for a cancelled one — which the order page's deliveries card shows.

### Who received a lot: DeliveryTrace

Orders provides **`contracts.DeliveryTrace`** (`contracts/delivery_trace.go`,
manufacturing phase 2 M90), the slot `Module.DeliveryTrace`, resolved from the enabled
modules only, for Inventory's [trace](/en/reference/inventory/#who-received-it).
`Deliveries(ctx, ids)` answers the **posted** deliveries among the ids, by id: the
delivery's id and number, its order's id and number, the order's customer **id** — never
the name — and when it was posted. A draft or a cancelled delivery, or an id that is none,
is absent. It reads on the pool in one statement and refuses more than
`contracts.MaxTracedDeliveries` (1,000) ids as a failure.

It is **provider-gated** (module-boundaries rule 12): Orders checks **`orders:access`**
against the principal in the context — the request's, or the Assistant's `AsUser` — and
refuses a caller without it, **and a context with no principal**, with
**`contracts.ErrForbidden`**. Unlike the production read Orders consumes, it refuses
rather than answering nothing, so Inventory tells *no deliveries* from *not allowed to see
them* and says so on the trace. Customer names stay Customers' to give: Inventory reads
them through the customer directory, only for a caller holding `customers:view`.

On the delivery page, each lot of a tracked line links to its page and its trace in
Inventory when the meta's **`canTraceLots`** says so (M94): Inventory keeps stock and the
caller holds `inventory:locations-view` and `inventory:stock-view`; otherwise the lots are
named without links.

### Back orders

What is not delivered stays open on the order; no back-order document is made. `GET
/orders?toDeliver=true` lists the confirmed orders with a product line whose ordered less
cancelled less delivered is above 0.

### The pick list and the packing slip

Both are A4 PDFs in the module's style, in the order's language. The **pick list**
(`GET /deliveries/{id}/pick-list`, *plukkliste*) is printed on demand and never stored:
the lines sorted by the picked location's full code, those not picked last, each with the
site's locations holding its variant and how much — the variant's
[primary location](/en/reference/inventory/#placement) first, then by full code, as the
draft's location picker lists them (`stockLocations` sorts on `Primary`, stably). A tracked
line prints a row per pick, under the pick's location, with the lot number and expiry in
the *Lot* column (*Parti*), and a *Not chosen* row for what its picks leave short. The **packing slip** (`GET
/deliveries/{id}/packing-slip`, *pakkseddel*) prints the seller, the order's delivery
address, contact and references, and per line what was ordered, what this delivery takes
and what is left as back order, and under a tracked line its lots — each lot once, what it
takes summed over its locations, with its expiry and, when the line takes more than one
lot, that quantity; a variant tracked by serial number (its catalog `Tracking`) lists its
numbers without quantities, wrapped — so the stored slip is the customer's record of which lots went to them
(M79); a posted delivery's is the stored one (`orders.documents`,
owner kind `delivery`, kind `packing_slip`), a draft's is printed now with the draft mark.
Once a shipment of the delivery is booked, the packing slip is printed now — dated the day
the delivery was posted — with each booked shipment's carrier, service and consignment
number as text and as Code 128, and its tracking URL as a QR code (O52); a cancelled or
draft shipment is left out.

### Invoicing on delivery

A line's `invoice_policy` is `order` or `delivery` (O48). A goods line on an order with a
site defaults to `delivery`, every other line to `order`; a line's own `invoicePolicy` in a
save wins, a kept line keeps its policy otherwise, and a draft whose site changed takes the
defaults again. A confirmed order's save changing the policy of a line with anything
invoiced or in a draft invoice is `line_invoiced` (409), logged `policy_changed` otherwise.
For a line on delivery, [the billable read](#the-billable-read) offers delivered less
invoiced less draft-allocated, `Allocate` refuses beyond it (`exceeds_open`), and the
invoiced-work holder judges an allocation against delivered less invoiced.

## Shipping

Delivery D (O49–O53) ships a posted delivery with a carrier. Carriers sit behind the platform
port `internal/shipping` ([adding a carrier](/en/contributing/shipping-carriers/)); this
phase has one adapter, **Bring** (`shipping/bring`). Every carrier call is made outside any
transaction; amounts cross the port in øre, weights in grams and dimensions in millimetres,
and the adapter converts (Bring's booking takes kilograms, its Shipping Guide grams and
centimetres).

### Carrier accounts

A carrier account (`orders.carrier_accounts`, O51) has the carrier (`bring`), a name unique
among the active accounts (`carrier_account_name_taken`, 409), one to five customer numbers
— Bring's first books parcels, a second, when given, cargo (5100, 5300, 5400) — the
**credentials**, a **test** flag, an optional sender address, a default service, `active`
or `archived`, when Verify last passed and a `version`.

- **The credentials** — the Mybring user id and API key — are sealed together by the
  secrets box under the purpose `orders/carrier-credentials` and stored as bytes; they are
  never answered (`hasCredentials` says whether they are stored), logged or sent anywhere
  but to the carrier. A save without them keeps the stored bytes as they are; a save with
  them replaces them. New keys, other customer numbers or another test flag clear
  `verifiedAt`. Keys the box cannot open — `APP_SECRET` changed — are 503
  `carrier_unavailable`, and must be entered again.
- **The test flag** sends `X-Bring-Test-Indicator: true` on every call (and
  `testIndicator` in the booking): Bring's test mode. Bring notifies real phone numbers and
  e-mails even in test mode, so the booking card warns.
- **The sender** is the account's address, each empty field taking the seller's from
  Invoices (`contracts.SalesTax.Seller`).
- **SHIPPING_CARRIERS** ([the configuration](/en/admin/configuration/)) lists the adapters
  the installation allows. A carrier it withholds cannot be added, and every call on its
  accounts is 503 `carrier_unavailable`; the account answers `carrierAllowed: false`.
- **Verify** (`POST /carrier-accounts/{id}/verify`) asks Bring's Shipping Guide for product
  5000 between two Oslo postal codes on the first customer number: `ok`,
  `unauthorized` (refused keys), `refused` (Bring's message, for example a customer number
  the user may not use) or `unreachable`. With `ok` it says whether **prices** are
  available — Bring answers the net price only when the Mybring user has the *financial*
  role on the customer number — and lists the services. A passed Verify records
  `verifiedAt`.
- **Services** are the adapter's data file (`shipping/bring/products.json`): 5800 parcel to
  pickup point, 5600 delivered home, 3584 in the mailbox, 5801 parcel locker, 5000 business
  parcel, 4850 business express, 5100 groupage, 5300 part load and 5400 pallet — never Go
  constants, since Bring withdraws products yearly. A cargo product has a waybill of its
  own; for the others the label serves.
- **The default account per site** (`orders.site_carrier_defaults`, `PUT
  /shipping/site-defaults`) names, per Inventory site, the account its shipments and
  estimates use; each site must be one Inventory has that fulfils orders, named once, and
  each account active. A save changes only the sites it names — `accountId: null` clears a
  site's default, and a site not named keeps its own — so two people saving different
  sites do not undo each other; the settings page sends every site it shows. A site
  without one uses the only active account when there is exactly one.

The accounts are managed under Workspace admin › Integrations › Shipping
(`/workspace/shipping`), behind the orders module and `orders:carriers-manage`, the
sensitive key that is not delegable. `GET /carrier-accounts`, the services and the site
defaults are read by every `orders:access` holder, to ship.

### Shipments

A shipment (`orders.shipments`, O50) of a posted delivery has the account, the service and
value-added services, `status` `draft`, `booked`, `in_transit`, `delivered` or `cancelled`,
an `exception` flag beside it, `voided`, the recipient as it was made, its packages
(`orders.shipment_packages`: weight in grams, length, width and height in millimetres, 0 when
not given, a content text and the carrier's package number once booked), and once booked the
consignment number, the tracking URL, the product actually booked, the booked cost and the
carrier's document references. `planned_window_start` and `planned_window_end` are kept for
a carrier that sells delivery windows; Bring leaves them empty. A delivery may carry several
shipments.

- **Create** (`POST /deliveries/{id}/shipments`, deliveries-manage) needs a posted delivery
  (`delivery_not_posted`, 409). It proposes **one package** weighing the delivered
  quantities times their variants' weights (`contracts.VariantEntry.WeightGrams`), rounded
  up to the gram — a kilogram when no variant has a weight —, the recipient as the order's
  delivery address, else its invoice address, with the contact's name and phone and the
  contact's e-mail, else the customer's invoice e-mail; the account named, else the site's
  default (`no_carrier_account`, 409, when there is none; `carrier_account_archived`, 409);
  the service named, else the account's default, else the first product of the carrier
  (`service_unknown`, 400). The **idempotency key** — a UUID — is stored on the shipment
  here.
- **Save** (`PUT /shipments/{id}`) replaces a draft's account, service, value-added
  services, pickup point, the recipient's contact, phone and e-mail, and its packages (1 to
  20; 1 g to 1 tonne; dimensions 0–10 000 mm). A shipment no longer a draft is
  `shipment_not_draft` (409). A draft with a booking attempt whose outcome is unknown
  (`bookAttempted`) is `booking_unresolved` (409): a retry books what was attempted, so it
  is booked again to resolve it, never changed.
- **Prices** (`GET /shipments/{id}/rates`) ask the carrier for every product of the account
  for the draft's packages, from the sender to the recipient, cheapest first: each with the
  price excluding VAT, the VAT, the currency, whether it is an estimate and the expected
  delivery day. Bring's net price is preferred, else its list price; both are estimates.
- **Book** (`POST /shipments/{id}/book`, the version and `expectedUpdatedAt`) records the
  attempt (`book_attempted_at`) under the shipment's lock, moving its version, and commits
  it before Bring is called, then books under the stored key: Bring has no idempotency key,
  so the key travels as the consignment's `correlationId` and the sender's reference.
  - **The attempt is a lease.** While it is under **two minutes** old — longer than the
    carrier call's own 30-second deadline — another book of the shipment is
    `booking_in_progress` (409), and so is a cancel; a client holding the version it read
    before the attempt is `version_conflict`. Two books at once make one booking.
  - **An unknown outcome.** When an earlier attempt got no answer, a book after the lease
    asks Bring's tracking for the key first and records the booking it finds — so a retried
    request books once. Bring shows a booking by reference only after a lag, so while the
    earlier attempt is under **fifteen minutes** old a retry that finds none books nothing
    and is `booking_outcome_unknown` (409): check Mybring, or wait. After fifteen minutes a
    retry that still finds none books again. A lookup that fails leaves the outcome
    unknown.
  - **Label in Mybring only.** A booking recovered by its reference has no label link:
    Bring's tracking does not give it, and Bring documents no label link by consignment
    number. The shipment is marked `labelInMybringOnly` (`label_in_mybring_only`), has no
    label to download, and its label is printed in Mybring.
  - Bring's refusal is 422 `carrier_refused` with Bring's own message; refused keys 503
    `carrier_unavailable`. After such a definitive refusal nothing was booked under the
    key: the draft gets a fresh key and its attempt is taken back, so it can be changed and
    booked anew. No answer is 503 `carrier_unreachable` — book again in a couple of
    minutes.
  On one transaction, under the shipping lines' variants', the order's and the shipment's
  locks: the consignment number, package numbers, tracking URL, product booked and cost
  (marked an estimate) are stored, `orders.shipment.booked` is published, the order's
  [shipping lines](#the-shipping-line) count delivered, and — with the setting
  `shippingNoticeOnBook` — the [shipping notice](#the-shipping-notice) is queued. After the
  commit the label and the waybill are fetched, each within 10 seconds, — Bring's label
  behind its redirect, PDF only — and stored (`orders.shipment_documents`, under
  `shipment/<id>/` in orders' storage scope); a fetch that fails or runs out of time is made
  again by the first download. The booked cost is the
  business's, never invoiced: the customer pays the shipping line.
- **Cancel** (`POST /shipments/{id}/cancel`) cancels a draft at once — but not while a
  booking attempt holds it (`booking_in_progress`, 409). A booked shipment is cancelled with
  the carrier, whose answer is one of three: `cancelled`; `not_supported` — Bring, which
  has no cancel call and bills on hand-in — and the shipment is **voided** in Vantigo, its
  label must not be used; or too late, refused with `shipment_past_cancel` (409). A carrier
  whose capabilities list no cancel (Bring) is voided without a call, so without reading
  the account's keys: a void works even when the keys cannot be opened. A shipment in transit or delivered is `shipment_past_cancel`; one already cancelled
  `shipment_not_cancellable`. Cancelling changes nothing on the order's lines.
- **The label and the waybill** (`GET /shipments/{id}/label`, `/waybill`,
  deliveries-manage) are the stored PDFs as Bring issued them; a service without a waybill
  of its own answers its label for the waybill. 404 when there is none, and for a shipment
  of an anonymised order.
- `GET /deliveries/{id}/shipments` and `GET /orders/{id}/shipments` list a delivery's and
  an order's shipments with their packages, documents and tracking events. Both take an
  optional `customerId`: when it is not the order's customer the answer is a 404. A shipment
  carries its order's `customerId`.

### Tracking

`orders.shipment_tracking` polls the carriers every 30 minutes for the shipments booked or in
transit, booked in the last 60 days. A cycle claims up to 50 shipments at a time with a
`SKIP LOCKED` pick, the least recently tracked first, that moves `last_tracked_at` to now —
the lease — asks each account's carrier once for its shipments in the batch, outside any
transaction, and records each on a short transaction of its own: every event once
(`orders.shipment_events`: Bring's code, text, place and time, and the state it folds
onto), the status moved and the exception flag set or cleared. Bring is asked one reference
at a time, each within 10 seconds: one that fails or hangs is skipped, not the rest. A
cycle's carrier calls end after 10 minutes, well inside the lease; a shipment not yet asked
about then gets its `last_tracked_at` back, so it is due — and first — in the next cycle,
and a stuck tail rotates. Bring's statuses fold so: `PRE_NOTIFIED` booked;
`READY_FOR_PICKUP` and in-transit codes in transit; `DELIVERED` delivered, publishing
`orders.shipment.delivered`; `DEVIATION`, `RETURN` and `DELIVERED_SENDER` set the exception
flag; `CANCELLED` sets the exception flag too and is recorded as an event, but the shipment
is not cancelled on the carrier's word — a person looks, and cancels it in Vantigo. Bring's Event Cast webhooks are not
used: they are unsigned and retry little.

### The shipping notice

With the setting `shippingNoticeOnBook` (on by default), a booking queues a mailing of kind
`shipping_notice` (owner `shipment`) to the shipment's recipient e-mail, else the order's
contact's, else the customer's invoice e-mail — once per shipment, its trigger the
shipment's idempotency key. `orders.mail` sends it on the settings' channel, without an
attachment: "Ordre 5001 er sendt – …" / "Your order 5001 has shipped – …", naming the
carrier, the consignment number and the tracking link. The settings may override it per
language; an override may name `{number}`, `{seller}`, `{carrier}`, `{consignment}` and
`{trackingUrl}`. A shipment cancelled before it went is not notified.

### The shipping line

A line of kind `shipping` (O53) is the freight the customer pays, on a quote or an order
(a template or a subscription refuses it, `line_invalid`). It is priced like a product line
with **quantity 1** (`quantity_invalid` otherwise), is never optional and has no packaging;
its invoice policy is `order` and its delivery method `manual`. It names the settings'
**freight product** (a variant of a Service product), taking its name, or is free text.
Freight follows the VAT of the goods it carries: see the estimate's VAT code below.

- **The estimate** (`POST /shipping-estimate` with `quoteId` or `orderId`) writes nothing.
  Besides orders:access it needs the right to edit the document: orders:quotes-manage for a
  quote, orders:sales-manage for an order (403 otherwise). It asks the default account of the order's site — a quote's: the
  settings' default site — for its default service, for one package weighing the document's
  goods lines (catalog lines, optional ones left out) by their variants' weights, to the
  document's delivery address, else its invoice address (`address_missing`, 409). It answers
  the carrier's price excluding VAT, the settings' markup percent, the price with the markup
  rounded to øre, and `unitPrice`, that price as the line enters it — including VAT when the
  document's prices do —, with the freight product's variant and name. **The VAT code**
  is the document's priced goods lines' (product lines, optional ones left out) when they
  all share one; when they are mixed, the code carrying the largest net amount of them
  (the lowest code id on a tie) — a simplification of "freight follows the main supply" that
  a person may override on the line; with no goods line, the freight product's code, else
  the standard rate's. A carrier pricing in another currency than the document's is
  `currency_mismatch` (409).
- A save naming `priceSource: carrier` with a typed `unitPrice` on a shipping line stores
  `price_source = carrier`; a price typed over it without that is `manual`.
- **Delivered** when a shipment of the order is booked: every shipping line's delivered
  quantity becomes what is ordered less cancelled, the order's delivery status is recomputed
  and `shipping_delivered` is logged with the consignment number. It is invoiced like any
  line ([the billable read](#the-billable-read)). Manual delivery marking does not take a
  shipping line.

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
kind (`quote`, `confirmation`, and `shipping_notice`, [the shipping notice](#the-shipping-notice)) and language (`nb`, `en`), up to 300 and 4000 characters;
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
| `orders.quote.sent` | A quote is sent or marked as sent. The virtual employees' sales follow-up wakes on it, after the days its role chose | `quoteId`, `number`, `revision`, `customerId`, `currency`, `netTotal`, `sentAt` |
| `orders.quote.accepted` | A quote is accepted | `quoteId`, `number`, `revision`, `customerId`, `acceptedIntoKind` (`order` or `subscription`), `acceptedIntoId`, `acceptedAt` |
| `orders.sales_order.confirmed` | An order is confirmed | `orderId`, `number`, `version`, `customerId`, `currency`, `netTotal`, `confirmedAt` |
| `orders.sales_order.cancelled` | A confirmed order is cancelled | `orderId`, `number`, `customerId`, `cancelledAt` |
| `orders.sales_order.changed` | A confirmed order is edited, or the rest cancelled, at a new version | `orderId`, `number`, `version`, `customerId`, `changedAt` |
| `orders.sales_order.invoiced` | An invoice's issue stamped allocations of the order — once per order the invoice bills, by the [holder](#the-invoiced-work-holder) on the issue's transaction | `orderId`, `number`, `customerId`, `invoiceId`, `invoiceNumber`, `invoicedAt` (the issue's time) |
| `orders.delivery.posted` | A delivery is posted; its aggregate is the order | `deliveryId`, `number`, `orderId`, `siteId`, `postedAt` |
| `orders.shipment.booked` | A shipment is booked with its carrier | `shipmentId`, `deliveryId`, `orderId`, `carrier`, `service`, `consignmentNumber`, `bookedAt` |
| `orders.shipment.delivered` | Tracking reports a shipment delivered | `shipmentId`, `deliveryId`, `orderId`, `consignmentNumber`, `deliveredAt` |
| `orders.subscription.started` | A subscription is activated | `subscriptionId`, `number`, `version`, `customerId`, `currency`, `interval`, `startDate`, `startedAt` |
| `orders.subscription.changed` | An activated subscription's lines are changed from a period on, or it is paused or resumed | `subscriptionId`, `number`, `version`, `customerId`, `status`, `changedAt` |
| `orders.subscription.ended` | A subscription is given its end, or an activated one is cancelled | `subscriptionId`, `number`, `version`, `customerId`, `status` (`ended` or `cancelled`), `endDate`, `endedAt` |

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

`orders.mto-reserve` takes Manufacturing's `manufacturing.order.done` v1 and marks the
sales order line **produced, ready to reserve** ([Make-to-order](#make-to-order)).

## Workers

| Worker | Runs | Does |
| --- | --- | --- |
| `orders.mail` | every 10 seconds, up to 10 rows a cycle | Sends the queued mailings. A row is claimed under a two-minute lease (`SKIP LOCKED`), so two replicas never take one row; the stored PDF is read and mailed outside any transaction. A failure counts the attempt and waits 30 seconds, doubled per attempt, at most an hour; the eighth failure fails the row. A row that can never go — no recipient, sending disabled, the document gone, the order cancelled — fails at once. |
| `orders.retention` | daily, one replica under an advisory lease | Anonymises the kept confirmed orders of erased customers once their retention has run, blanks their sends, and deletes the stored PDFs of every anonymised quote and order of theirs ([Retention](#retention)). |
| `orders.quote_expiry` | daily | Moves every sent quote whose valid-until day is before today, in the business time zone, to `expired`, in one statement. It publishes no event. |
| `orders.shipment_tracking` | every 30 minutes, 50 shipments a claim | Polls the carriers for the shipments booked or in transit in the last 60 days, records their events and moves them ([Tracking](#tracking)). A shipment is claimed by moving its `last_tracked_at` (`SKIP LOCKED`), so two replicas never poll one. |

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
  the merged-away one's on a new line, so a merge never lifts a block. The subscriptions
  are re-pointed as the orders are. Reported as `orders.quotes`, `orders.orders`,
  `orders.warnings` and `orders.subscriptions`.
- **Customer export**: every quote, order and subscription of the customer, each with its
  snapshot, status, revision or version, totals, texts, lines — a subscription's with their
  validity, and its plan — and sends, an order's deliveries with their number, state,
  posting time and lines, and the customer's sales warning, in one read-only snapshot;
  nothing for a customer with none of them. A delivery holds no personal data of its own:
  the address it ships to is the order's.
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
  `orders.quotes`, `orders.draftOrders`, `orders.orders` (always 0), `orders.mailings`,
  `orders.warnings` — the customer's sales warning is deleted — and
  `orders.subscriptions`: a subscription never activated is anonymised at once, an
  activated one is kept as bookkeeping material and recorded as a confirmed order is;
  run again, it reports zeros.
- **Invoiced work** (O38): [the invoiced-work holder](#the-invoiced-work-holder) stamps
  and releases the allocations inside an invoice's issue and a credit note's.
- **Variant delete** (O30): Products' delete of a variant is refused while an open
  document names it — a draft or sent quote, an order not cancelled with a line of it
  that is not delivered or not invoiced in full, or a subscription still to bill — draft,
  active or paused, or ended on or after today — with a line of it in force on or after
  today — or a draft delivery naming it, as an `open_documents` hold
  with the count. A change of a product to a service is never held. A template is no document: a
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
so within about a day, and an order's, with its deliveries' packing slips and its
shipments' stored labels and waybills, once it has been anonymised after its retention —
each object from the store first, then its row; the send rows stay, blanked, with their
link to the PDF removed. An activated subscription is kept the same way until 3 years and
6 months after the end of the financial year it ended in — its end date, or the day it was
cancelled — and then anonymised by the same worker; an active one is kept. An anonymised
order's shipments lose their recipient, their label and waybill links and their tracking
page link, so no label is fetched from the carrier again, and their label and waybill
answer 404; their consignment numbers, packages and tracking events stay. An erasure is
marked complete only when no order or subscription of the customer is left unanonymised
and no stored PDF of theirs remains.

## The customer summary

`GET /customers/{customerId}/summary` (O31a) answers, per currency and net of VAT:

- **Open quotes** — the draft and sent quotes, counted, at their net totals;
- **Ordered, not delivered** — the confirmed orders with something left to deliver,
  counted, each line's open quantity valued at its net amount over its quantity;
- **Delivered, not invoiced** — delivered less what issued invoices stamped invoiced,
  valued the same way;
- **Active subscriptions** (`activeSubscriptions`) — the active and paused subscriptions,
  counted, at their value per year: a full period's net at the lines in force from the
  latest change on, times the periods in a year.

The customer page's **Orders** tab shows it above the customer's open quotes, orders and
subscriptions.
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
in `lines` — every failing line at once, each with its `index`, `code` and `detail`, and on
a delivery's tracked line the `lotIndex` and `lotId` of the pick it is about —
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
| `line_invalid` | 400 | Not a product, section or note; a free-text line with no name or description; an id not one of the document's; a confirmed order's kept line given another unit or VAT code; a kit KitStructure answers with no component; a kit line delivered by delivery documents, or a child of a kit marked by hand, named to mark-delivered or undo-delivered ([Kits](#kits)) |
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
| `lot_unknown` | 400 | A delivery's pick names a lot that holds none of the item at that location of the site |
| `lot_not_allowed` | 400 | A delivery's untracked line names lots |
| `serial_quantity` | 400 | A delivery's pick of a serial-tracked variant takes other than 1 |
| `expired_override_required` | 400 | A delivery's new or changed pick allows an expired lot, from a caller without `inventory:expired-override` |
| `lot_required` | 400 | Posting a tracked delivery line with no lot |
| `picks_mismatch` | 400 | Posting a tracked delivery line whose lots do not add up to its quantity |
| `lot_expired`, `lot_blocked` | 409 | Inventory refused a delivery's pick: the lot is expired (without *Use expired lot* and `inventory:expired-override`), or on hold |
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
| `subscription_goods_not_allowed` | 400 | A subscription's catalog line is goods, not a service |
| `subscription_not_editable` | 400 | An activated subscription given another customer, currency, VAT mode, interval, billing, start or end |
| `not_a_period_start` | 400 | `effectiveFrom`, or a pause's or a resume's `from`, is not the first day of one of the subscription's periods |
| `subscription_not_draft` | 409 | Only a draft subscription is activated |
| `subscription_not_active` | 409 | Pause needs an active subscription; end an active, paused or ended one |
| `subscription_not_paused` | 409 | Only a paused subscription is resumed |
| `subscription_not_cancellable` | 409 | An ended or cancelled subscription is not cancelled |
| `subscription_billed` | 409 | Cancelling a subscription with a live allocation: end it instead |
| `subscription_empty` | 409 | Activating a subscription with no product line |
| `change_in_billed_period` | 409 | A change or a pause from a period that, or a later one, has a live allocation |
| `end_before_billed` | 409 | An end before the first day of a period with a live allocation: credit it first |
| `final_period_billed` | 409 | A `prorated` final period, at End or in a save of the plan, while the period the end falls in has a live allocation: bill it in full, or credit it first |
| `delivery_not_posted` | 409 | A shipment of a delivery that is not posted |
| `no_carrier_account` | 409 | No carrier account is the site's default and there is not exactly one active account |
| `carrier_account_archived` | 409 | The shipment's carrier account is archived |
| `carrier_account_name_taken` | 409 | Another active carrier account has the name |
| `service_unknown` | 400 | Not one of the carrier's services |
| `shipment_not_draft` | 409 | A save, a price or a booking of a shipment no longer a draft |
| `shipment_past_cancel` | 409 | The carrier has the parcel: in transit, delivered, or the carrier answered too late |
| `shipment_not_cancellable` | 409 | The shipment is already cancelled |
| `booking_in_progress` | 409 | A book or a cancel of a draft whose booking attempt is under two minutes old |
| `booking_outcome_unknown` | 409 | A retry after a lost answer, under fifteen minutes after it, while the carrier shows no booking under the key: check Mybring, or wait |
| `booking_unresolved` | 409 | A save of a draft whose booking's outcome is unknown: book it again first |
| `address_missing` | 409 | An estimate of a document with no delivery or invoice postal code |
| `currency_mismatch` | 409 | The carrier prices in another currency than the document |
| `freight_product_invalid` | 400 | The settings' freight product is not a variant of a Service product |
| `carrier_refused` | 422 | The carrier refused the request; `detail` is its own message |
| `carrier_unavailable` | 503 | `SHIPPING_CARRIERS` withholds the carrier, the keys cannot be opened, or the carrier refused them |
| `carrier_unreachable` | 503 | The carrier did not answer; a booking's outcome is unknown, and booking again books once |
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
| `defaultSiteId` | none | The site a new order takes while Inventory keeps stock: an active site that fulfils orders (`site_not_fulfilling`); none names none |
| `subscriptionFinalPeriodDefault` | `full` | The final period a new subscription takes, `full` or `prorated` ([The final period](#the-final-period)); a save that leaves it out keeps it |
| `freightVariantId` | none | The freight product a shipping line takes: a variant of a Service product (`freight_product_invalid`); null names none, a save that leaves it out keeps it |
| `freightMarkupPercent` | 0 | 0–500 % with at most two decimals, added to a carrier's estimate ([The shipping line](#the-shipping-line)); a save that leaves it out keeps it |
| `shippingNoticeOnBook` | on | Queue [the shipping notice](#the-shipping-notice) when a shipment is booked; a save that leaves it out keeps it |

The answer also lists the send channels Communications offers and whether sending is
available. `GET /meta` gives every holder of `orders:access` what the screens need of it:
`sendingAvailable`, `quoteValidityDays`, `defaultPaymentTermsDays`, `baseCurrency`,
`stockKept` — whether Inventory keeps stock —, `canOverrideExpired` — whether the caller
holds `inventory:expired-override`, which offers a delivery's *Use expired lot* — and
`canTraceLots` — whether Inventory keeps stock and the caller holds both of Inventory's
views, which links a delivery's lots to their page and trace there. `GET /sites` lists Inventory's sites that are
not archived, through `contracts.Stock`, for the order's and the default site's pickers;
empty without Inventory.

## Agents and the customer scope

An agent (a virtual employee) reads orders through the same API, and the operations dispatcher ties each
call to the customer of its run ([module boundaries rule 18](/en/contributing/module-boundaries/)).
Nine operations declare `x-vantigo-customer-scope`:

| Operation | Scope |
| --- | --- |
| `GET /orders` (the list) | `param: customerId`: the query filter is the run's customer, filled in when left out and refused when it names another |
| `GET /orders/{id}` (one order) | `resource: customer.customerId`: the answer is dropped, as a `customer_scope` error, when the order's customer is not the run's |
| `GET /quotes` (`listQuotes`) | `param: customerId`, as the order list's |
| `GET /quotes/{id}` (`getQuote`) | `resource: customer.customerId`, as one order's |
| `GET /shipments/{id}` (`getShipment`) | `resource: customerId`: a shipment names its order's customer (`customerId`), and the answer is dropped when it is not the run's |
| `GET /deliveries/{id}` (`getDelivery`) | `resource: customerId`, which a delivery already carries |
| `GET /deliveries` (`listDeliveries`) | `param: customerId`: a query filter on the order's customer, filled in when left out and refused when it names another |
| `GET /orders/{id}/shipments` (`listOrderShipments`), `GET /deliveries/{id}/shipments` (`listDeliveryShipments`) | `param: customerId`: an optional query filter the handler enforces, so the order or the delivery of another customer is a 404 and never its shipments |

The two quote reads are what the *Sales follow-up* template of the virtual employees allows its role, so that a
follow-up reads the quote of the customer it writes to and no other. A run with no customer reads none of the nine.
The front desk reads `listOrderShipments`, `getShipment` and `listDeliveries` to answer "where is my parcel?": the
shipment's status, its consignment number and its tracking link and events. `getDelivery` stays off its
allowlist because it carries a delivery's pick locations, and `listDeliveryShipments` because `listOrderShipments`
answers the same.

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
| `orders.settings.final-period` | `/orders/settings` | Final period when a subscription ends |
| `orders.customer.new-quote` | `/customers/{customerId}/orders` | New quote on the customer's Orders tab |
| `orders.customer.new-order` | `/customers/{customerId}/orders` | New order on the customer's Orders tab |
| `orders.lines.add-from-grid` | `/orders/quotes/{quoteId}` | Add from grid, under a quote's or an order's lines |
| `orders.document.warnings` | `/orders/quotes/{quoteId}` | The warnings' override and the customer's warning, on a quote or an order |
| `orders.settings.warnings` | `/orders/settings` | The sales warnings in the settings |
| `orders.analysis.views` | `/orders/analysis` | The analysis's three views |
| `orders.analysis.csv` | `/orders/analysis` | Download CSV of the open view |
| `orders.order.site` | `/orders/orders/{orderId}` | The order's site, while Inventory keeps stock |
| `orders.order.create-delivery` | `/orders/orders/{orderId}` | Create delivery, on the Deliveries card |
| `orders.deliveries.view` | `/orders/deliveries` | To deliver or Deliveries |
| `orders.delivery.pick-list` | `/orders/deliveries/{deliveryId}` | Pick list |
| `orders.delivery.post` | `/orders/deliveries/{deliveryId}` | Post delivery |
| `orders.settings.default-site` | `/orders/settings` | Default site |
| `orders.lines.add-shipping` | `/orders/quotes/{quoteId}` | Add shipping, under a quote's or an order's lines |
| `orders.lines.estimate-shipping` | `/orders/quotes/{quoteId}` | Estimate, on a shipping line |
| `orders.lines.create-manufacturing-order` | `/orders/orders/{orderId}` | Create manufacturing order, on a goods line with a bill of materials |
| `orders.delivery.ship` | `/orders/deliveries/{deliveryId}` | Ship, on a posted delivery's Shipments card |
| `orders.shipment.service` | `/orders/deliveries/{deliveryId}` | A draft shipment's service |
| `orders.shipment.book` | `/orders/deliveries/{deliveryId}` | Book shipment |
| `orders.settings.freight-markup` | `/orders/settings` | Markup on the carrier's estimate |
| `orders.shipping.add-account` | `/workspace/shipping` | Add carrier account, under Integrations › Shipping |
| `orders.shipping.verify` | `/workspace/shipping` | Verify a carrier account |
| `orders.subscriptions.new` | `/orders/subscriptions` | New subscription |
| `orders.new-subscription.customer` | `/orders/subscriptions/new` | The customer picker |
| `orders.new-subscription.create` | `/orders/subscriptions/new` | Create the subscription |
| `orders.subscription.save` | `/orders/subscriptions/{subscriptionId}` | Save |
| `orders.subscription.activate` | `/orders/subscriptions/{subscriptionId}` | Activate |
| `orders.subscription.pause` | `/orders/subscriptions/{subscriptionId}` | Pause |
| `orders.subscription.resume` | `/orders/subscriptions/{subscriptionId}` | Resume |
| `orders.subscription.end` | `/orders/subscriptions/{subscriptionId}` | End |
| `orders.subscription.effective-from` | `/orders/subscriptions/{subscriptionId}` | Changes take effect from |
| `orders.subscription.final-period` | `/orders/subscriptions/{subscriptionId}` | Final period when a subscription ends, in the plan |
| `orders.subscription.end-final-period` | `/orders/subscriptions/{subscriptionId}` | The final period, in the End dialog |

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
| `GET /meta` | access | 200 — sending available, default validity and payment terms, base currency, stock kept |
| `GET /sites` | access | 200 — Inventory's sites not archived, by code; empty without Inventory |
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
| `POST /quotes/{id}/accept` | quotes-manage (+ sales-confirm for a confirmed order, + subscriptions-manage for a subscription, + settings to override) | 200 the quote and the order or the subscription; 400 fields, `plan`, `subscription_goods_not_allowed` and confirm's codes; 403; 404; 409 `quote_not_sent`, `quote_already_accepted`, `blocked_by_warning` |
| `POST /quotes/{id}/decline` | quotes-manage | 200; 400; 404; 409 |
| `POST /quotes/{id}/cancel` | quotes-manage | 200; 404; 409 |
| `POST /quotes/{id}/copy` | quotes-manage | 201; 404; 503 |
| `GET /quotes/{id}/pdf` | access | 200 the stored PDF of `revision`, or a draft's printed now; 404 |
| `GET /quotes/{id}/proforma` | access | 200; 404 |
| `GET /quotes/{id}/documents` | access | 200 the stored PDFs; 404 |
| `GET /quotes/{id}/mailings` | access | 200 every send; 404 |
| `GET /orders` | access | 200 — newest first, by customer, status, delivery and invoice status, open or done, to deliver, salesperson and a search; 400 paging |
| `POST /orders` | sales-manage | 201; 200 a repeat; 400; 409 `client_request_reused`; 503 |
| `GET /orders/{id}` | access | 200 with lines, buckets and totals, and each goods line's [production](#production-on-the-order); 404 |
| `PUT /orders/{id}` | sales-manage | 200; 400 incl. `quantity_below_progress`, `line_invalid`, `site_not_fulfilling`; 404; 409 `version_conflict`, `order_locked`, `line_invoiced`, `line_in_draft_invoice`, `line_has_progress`, `line_kind_changed`, `order_has_deliveries`, `insufficient_available`; 503 |
| `DELETE /orders/{id}` | sales-manage | 204 — a draft accepted from a quote puts the quote back to `sent`; 404; 409 `order_not_draft`, `version_conflict` (`expectedUpdatedAt` in the query) |
| `POST /orders/{id}/confirm` | sales-confirm (+ settings to override) | 200; 400 `customer_disabled`, `order_empty`, `line_unpriced`, `vat_code_unmapped`, `overrideReason`; 404; 409 `order_not_draft`, `blocked_by_warning`, `insufficient_available`, `site_not_fulfilling`, `variant_not_stocked` |
| `POST /orders/{id}/reserve` | sales-manage | 200; 404; 409 `order_not_confirmed`, `order_has_no_site`, `insufficient_available`; 503 without stock |
| `POST /orders/{id}/hold` | sales-manage | 200; 400 reason; 404; 409 `order_not_confirmed` |
| `POST /orders/{id}/release` | sales-manage | 200; 404; 409 |
| `POST /orders/{id}/lock` | sales-confirm | 200; 404; 409 `order_not_confirmed` |
| `POST /orders/{id}/unlock` | sales-confirm | 200; 404; 409 |
| `POST /orders/{id}/cancel` | sales-manage | 200; 404; 409 `order_locked`, `order_has_progress`, `order_in_draft_invoice` |
| `POST /orders/{id}/cancel-remaining` | sales-manage | 200; 404; 409 `order_not_confirmed`, `order_locked`, `order_in_draft_invoice` |
| `POST /orders/{id}/release-draft-allocations` | sales-manage | 200 — [every draft allocation released](#release-draft-allocations); 404; 409 `version_conflict` |
| `POST /orders/{id}/mark-delivered` | sales-manage | 200; 400 `over_delivery` and the line codes; 404; 409 `order_not_confirmed`, `order_on_hold` |
| `POST /orders/{id}/undo-delivered` | sales-manage | 200; 400; 404; 409 `order_on_hold`, `line_in_draft_invoice` |
| `POST /orders/{id}/send` | sales-manage | 200; 400; 404; 409 `order_not_confirmed`; 503 `sending_disabled`, `mail_failed` |
| `GET /orders/{id}/pdf` | access | 200 the stored confirmation of `version`, or the current one printed now; 404 |
| `GET /orders/{id}/proforma` | access | 200; 404 |
| `GET /orders/{id}/documents` | access | 200 the stored confirmations; 404 |
| `GET /orders/{id}/mailings` | access | 200 every send, with the version that went out; 404 |
| `GET /orders/{id}/changes` | access | 200 the change log; 404 |
| `GET /deliveries` | access | 200 — newest first, by status, order, customer, site and a search, each with its lots; 400 paging |
| `POST /deliveries` | deliveries-manage | 201; 400; 404 the order; 409 `order_not_confirmed`, `order_on_hold`, `order_has_no_site`, `nothing_to_deliver`; 503 without stock |
| `GET /deliveries/{id}` | access | 200 with lines, their lots and, on a draft, the locations and lots holding each variant; 404 |
| `PUT /deliveries/{id}` | deliveries-manage | 200; 400 `line_not_found`, `line_invalid`, `quantity_invalid`, `over_delivery`, `location_unknown`, `lot_unknown`, `lot_not_allowed`, `serial_quantity`, `expired_override_required`; 404; 409 `version_conflict`, `delivery_not_draft`; 503 |
| `POST /deliveries/{id}/post` | deliveries-manage | 200; 400 `location_missing`, `over_delivery`, `lot_required`, `picks_mismatch`; 404; 409 `version_conflict`, `delivery_not_draft`, `order_on_hold`, `insufficient_stock`, `location_unknown`, `site_not_fulfilling`, `variant_not_stocked`, `lot_expired`, `lot_blocked`, `lot_unknown`, `lot_required`, `lot_not_allowed`, `serial_quantity`; 503 |
| `POST /deliveries/{id}/cancel` | deliveries-manage | 200; 404; 409 `version_conflict`, `delivery_not_draft` |
| `GET /deliveries/{id}/pick-list` | access | 200 printed now; 404 |
| `GET /deliveries/{id}/packing-slip` | access | 200 the stored one of a posted delivery, a draft's printed now; 404 |
| `GET /subscriptions` | access | 200 — newest first, by customer, status and a search; 400 paging |
| `POST /subscriptions` | subscriptions-manage | 201; 200 a repeat; 400 fields, plan, lines, `subscription_goods_not_allowed`, `customer_disabled`; 409 `client_request_reused`; 503 |
| `GET /subscriptions/{id}` | access | 200 with lines, earlier lines, periods and billed through; 404 |
| `PUT /subscriptions/{id}` | subscriptions-manage | 200; 400 incl. `subscription_not_editable`, `not_a_period_start`; 404; 409 `version_conflict`, `change_in_billed_period`, `subscription_not_editable` (cancelled), `line_kind_changed`, `final_period_billed`; 503 |
| `POST /subscriptions/{id}/activate` | subscriptions-manage | 200; 400 `customer_disabled`, `line_unpriced`, `vat_code_unmapped`, `subscription_goods_not_allowed`; 404; 409 `subscription_not_draft`, `subscription_empty` |
| `POST /subscriptions/{id}/pause` | subscriptions-manage | 200; 400 `not_a_period_start`; 404; 409 `subscription_not_active`, `change_in_billed_period` |
| `POST /subscriptions/{id}/resume` | subscriptions-manage | 200; 400 `not_a_period_start`, before the pause; 404; 409 `subscription_not_paused` |
| `POST /subscriptions/{id}/end` | subscriptions-manage | 200; 400 before the start; 404; 409 `subscription_not_active`, `end_before_billed`, `final_period_billed` |
| `POST /subscriptions/{id}/cancel` | subscriptions-manage | 200; 404; 409 `subscription_billed`, `subscription_not_cancellable` |
| `GET /subscriptions/{id}/changes` | access | 200 the change log; 404 |
| `GET /templates` | access | 200 by name; `includeArchived` |
| `POST /templates` | settings | 201; 400; 409 `template_name_taken` |
| `GET /templates/{id}` | access | 200; 404 |
| `PUT /templates/{id}` | settings | 200; 400; 404; 409 `version_conflict`, `template_name_taken` |
| `DELETE /templates/{id}` | settings | 204; 404 |
| `GET /settings` | settings | 200 |
| `PUT /settings` | settings | 200; 400 `series_start_too_low`, `send_channel_unknown`, `site_not_fulfilling`, `freight_product_invalid`; 409 `version_conflict` |
| `GET /carrier-accounts` | access | 200 — every account, the active first, never its keys, and the carriers `SHIPPING_CARRIERS` allows |
| `POST /carrier-accounts` | carriers-manage | 201; 400 fields, keys required; 409 `carrier_account_name_taken`; 503 `carrier_unavailable` |
| `GET /carrier-accounts/{id}` | carriers-manage | 200; 404 |
| `PUT /carrier-accounts/{id}` | carriers-manage | 200; 400; 404; 409 `version_conflict`, `carrier_account_name_taken` |
| `POST /carrier-accounts/{id}/verify` | carriers-manage | 200 `ok`, `unauthorized`, `refused` or `unreachable`, prices available, services; 404; 503 `carrier_unavailable` |
| `GET /carrier-accounts/{id}/services` | access | 200 the carrier's products; 404 |
| `GET /carrier-accounts/{id}/pickup-points` | deliveries-manage | 200 near `postalCode` (`country`, default NO); 400; 404; 422 `carrier_refused`; 503 |
| `GET /shipping/site-defaults` | access | 200 the default account per site |
| `PUT /shipping/site-defaults` | carriers-manage | 200 the sites named saved, a null cleared, the others kept; 400 a site twice, `site_not_fulfilling`, an account not active |
| `GET /deliveries/{id}/shipments` | access | 200; 404, also when `customerId` is not the order's |
| `POST /deliveries/{id}/shipments` | deliveries-manage | 201; 400 `service_unknown`; 404; 409 `delivery_not_posted`, `no_carrier_account`, `carrier_account_archived`; 503 |
| `GET /orders/{id}/shipments` | access | 200 the order's shipments; 404, also when `customerId` is not the order's |
| `GET /shipments/{id}` | access | 200; 404 |
| `PUT /shipments/{id}` | deliveries-manage | 200; 400 `service_unknown`; 404; 409 `version_conflict`, `shipment_not_draft`, `carrier_account_archived`, `booking_unresolved` |
| `GET /shipments/{id}/rates` | deliveries-manage | 200 cheapest first; 404; 409 `shipment_not_draft`; 422 `carrier_refused`; 503 |
| `POST /shipments/{id}/book` | deliveries-manage | 200; 404; 409 `version_conflict`, `shipment_not_draft`, `carrier_account_archived`, `booking_in_progress`, `booking_outcome_unknown`; 422 `carrier_refused`; 503 `carrier_unavailable`, `carrier_unreachable` |
| `POST /shipments/{id}/cancel` | deliveries-manage | 200 cancelled or voided; 404; 409 `version_conflict`, `shipment_past_cancel`, `shipment_not_cancellable`, `booking_in_progress`; 503 |
| `GET /shipments/{id}/label` | deliveries-manage | 200 the carrier's PDF; 404 none, or the order is anonymised |
| `GET /shipments/{id}/waybill` | deliveries-manage | 200 the waybill, else the label; 404 none, or the order is anonymised |
| `POST /shipping-estimate` | access + quotes-manage for a quote, sales-manage for an order | 200; 403; 400 neither or both ids; 404; 409 `no_carrier_account`, `address_missing`, `currency_mismatch`; 422 `carrier_refused`; 503 |
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
