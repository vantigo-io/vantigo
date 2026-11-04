# Orders module — research

A new module, **Orders** (module key `orders`, permissions `orders:*`, package
`@vantigo/orders-ui`, Norwegian **Ordre**), for the commercial agreement between a
business and its counterparty: what was offered (**tilbud**), what was agreed
(**ordre**), what has been delivered against it and what has been invoiced. The
roadmap has named Orders as a coming module since the Products phases. The owner
points at Odoo Purchase as the nearest product. The module must also be the core that
**Subscriptions**, **eCommerce** and **Point of sale** later feed or reuse without
reshaping it. This note settles three things from the products a Norwegian small
business compares against and from the codebase as it stands:

- what the first version is;
- what it keeps for those channels;
- what the owner must decide.

A design spec starts from §9 and does not reopen it.

Code references are against `main` at `0bae1d8c`. Abbreviations:
- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- "MB" is `docs/src/content/docs/en/contributing/module-boundaries.md`.

## 1. What is decided, and what this note adds

### 1.1 Decisions on the record

- **2026-11-04, the owner:**
  - Orders is the next module.
  - Odoo Purchase (<https://www.odoo.com/app/purchase>) is the nearest comparison.
  - The model must be compatible with later Subscriptions, eCommerce and Point of
    sale modules.
- **From the roadmap and earlier designs:**
  - **The domain-events outbox is built with Orders.** It is in-process, transactional,
    with Postgres as the queue and no broker. The named first events are
    `OrderPlaced` → Communications sends a confirmation, and Inventory reserves stock
    (ROADMAP.md "Cross-module domain events").
  - **Order lines reference variants** and snapshot the price and VAT rate (ROADMAP
    Products phase 2 and 3; `reference/products.md`).
  - **Products phase 4**, pricing depth (price lists, customer-group prices, quantity
    breaks), is "designed together with the Orders service". **Products phase 5**
    (units, supplier records per variant) is "designed with Orders".
  - **Inventory waits for Orders** for four things: transfers between sites,
    reservations (`contracts.Stock`, `contracts.StockLedger`), supplier records on
    receipts, and the store flag *fulfils orders*.
    - Inventory's phase 1 kept the seam: a source reference on every movement, an
      open set of movement kinds, and reservations per site with no `reserved` column
      (inventory spec §11–§12; inventory research §6).
  - **Point of sale is its own module.** A cash sale is kontantsalg in a kassasystem
    and never an invoice. Point of sale is "a candidate first consumer of the deferred
    Orders model" (ROADMAP "Point of sale").
  - **Customers keeps quotes out:** "a sales pipeline or quotes (its own module, if
    ever)". A supplier register is "Later", "once purchasing … arrive[s]".
  - **Subscriptions** appear only as "Recurring invoices and retainers" under Invoices
    Later. eCommerce is not mentioned.

### 1.2 What this note settles

1. **What the products do:** Odoo, ERPNext, Business Central, the Nordic field and the
   commerce platforms (§2).
2. **Sales, purchasing or both**, and what Odoo Purchase means for the scope (§3).
3. **One order core that channels feed:** what goes in it and what stays out (§4).
4. **The order's life:** states, line quantities, edits, quotes (§5).
5. **The seams to the modules that exist:** Products, Inventory, Invoices, Customers,
   Communications, Projects (§6).
6. **The platform work Orders carries**, the outbox first (§7).
7. **The Norwegian rules** that touch orders (§8).
8. **The must-have list**, the phases and deliveries, and the readings for the owner
   (§9–§11).

## 2. What the products do

Read 2026-11-04 from public documentation. For Odoo and ERPNext, also from their
source. "(unverified)" marks a claim taken from a search summary or from memory, not
from a fetched page.

### 2.1 Odoo Sales and Purchase — one record from quote to order, separate per side

Sources:
- <https://raw.githubusercontent.com/odoo/odoo/18.0/addons/sale/models/sale_order.py>
  and `sale_order_line.py`
- <https://raw.githubusercontent.com/odoo/odoo/18.0/addons/purchase/models/purchase_order.py>
  and `purchase_order_line.py`
- the 19.0 versions of the same files
- <https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/purchase/manage_deals/control_bills.html>

**Sales (`sale.order`)**
- **The quotation and the order are one record.** States are `draft` (Quotation),
  `sent`, `sale` (Sales Order) and `cancel`.
  - `done` was removed in v17 and replaced by a separate `locked` boolean ("Locked
    orders cannot be modified").
  - Removing it untangled "fully processed" from "frozen".
- **Delivery and invoicing are derived statuses, not states.** The order's
  `invoice_status` (`no`, `to invoice`, `invoiced`, `upselling`) is rolled up from the
  lines.
- **Each line holds** ordered quantity, `qty_delivered`, `qty_invoiced` and
  `qty_to_invoice`.
  - `qty_delivered_method` (`manual`, `stock_move`, `timesheet`, `analytic`) says what
    feeds the delivered quantity.
  - The invoice policy is per product:
    - **ordered**: `to_invoice = ordered − invoiced`;
    - **delivered**: `to_invoice = delivered − invoiced`.
- **Sections and notes are lines** (`display_type`).
- **Down payments are fake lines** (`is_downpayment`) that every rollup must skip. This
  is commonly regretted.
- **Online quotes:** `require_signature`, `require_payment` with `prepayment_percent`,
  `validity_date`, and the portal confirming on signature or payment.
  - Quote templates and optional products live in `sale_management` (unverified detail).
- **The pricelist** is resolved from the customer **only while in draft**.
  `technical_price_unit` remembers a manual price so recomputation does not overwrite it.

**Purchase (`purchase.order`)**
- **The shape mirrors Sales but is a separate model.** States are `draft` (RFQ),
  `sent`, `to approve`, `purchase` and `cancel`. v19 replaced `done` with `locked`, as
  Sales had, and added `acknowledged` for the vendor's confirmation.
- **Two-step approval** above an amount (`po_double_validation_amount`).
- **Vendor prices** per product, quantity and date (`product.supplierinfo`) price the
  lines. Confirming adds the vendor to the product.
- **Receipts:** confirming creates a `stock.picking`, and its moves feed `qty_received`.
- **Bill control** is per product: ordered or received quantities.
  - **3-way matching** compares the bill with the PO and the receipt.
  - It sets a bill's "Should be paid" to Yes, No or Exception.
- **Purchase agreements:**
  - blanket orders, with `qty_ordered` summed from POs;
  - alternative RFQs, compared side by side.
- **Reorder rules** create or extend a draft RFQ per vendor.
- **Dropship:** a PO delivered straight to the sales order's customer.

**Channels**
- **eCommerce's cart is a draft `sale.order`** with `website_id`. The cost is tables
  full of abandoned drafts and cleanup jobs.
- **Subscriptions (v16+) are `sale.order`s with a recurring plan.** The order carries
  `next_invoice_date`, MRR and a seven-value `subscription_state`. Renewals and upsells
  are new linked quotations.
  - Merging the old `sale.subscription` model into the order is what makes the order
    model heavy.
- **POS has its own `pos.order`** inside a `pos.session`, posted in bulk at session
  close.
  - `pos_sale` lets the till load a sale order and settle it.
  - POS lines then point at `sale_order_line_id`.

### 2.1a Odoo Sales as a product — what the app sells, beyond the model

Sources:
- <https://www.odoo.com/app/sales>
- <https://www.odoo.com/app/sales-features>

Both were read 2026-11-04. §2.1 covers the model from the source code. This section
covers what the app page promises a buyer, sorted by what Vantigo has, what this note
plans, and what it leaves.

**Already in Vantigo, or in this note's phases**
- Quote → order → invoice in one click (§4.3, §6.4).
- Invoicing by ordered or delivered quantity (§5.2), down payments (§8.1 prepayment
  request), partial delivery (§5.4).
- Variants, product types, line discounts (Products; §4.1).
- Pricelists by segment, region, quantity or time (Products phase 4, §6.1).
- Online signature and payment of quotes (phase 3).
- Dropship and replenish-on-order routes from a line (phase 2 F).
- Units of measure with conversion (Products phase 5).
- Email attached to the order (Communications already threads mail to customers).
- Activities and follow-ups (Customers' follow-ups, `specs/2026-09-23-customers-follow-ups-design.md`).

**Not in the note before, worth a place in phase 1**
- **Quote templates:** a reusable set of lines, texts and validity.
- **Optional products on a quote:** lines the customer may add, which are not counted
  in the total until chosen.
  - Both are cheap once quotes exist.
  - They are what makes "professional quotations" the app's headline.
  - Projects already has templates as a pattern.
- **Discount visibility:** whether the customer sees the discount or only the net
  price, set per quote or order.
- **Sales warnings:** a note on a customer or a product shown, or blocking, when it is
  put on a quote or order. Examples: "prepayment only", "discontinued, offer X".
- **Salesperson** on quote and order (a user), with lists and filters by it.
- **Variant grid entry:** a size × colour matrix to enter quantities of many variants
  at once. Products already stores the option values.
- **Pro forma:** a quote or order confirmation rendered as "Proforma" for customs or
  prepayment. It is a print variant, not a record. Tripletex omits it; §11 asks.
- **Reporting:**
  - quotes sent vs accepted (hit rate);
  - order intake by period, salesperson, customer and product;
  - order backlog (ordered, not delivered);
  - delivered not invoiced.
  - The note listed only working lists (§9.6). The app's analysis views are what a
    manager asks for first.

**Later, with another module or never**
- **CRM** (leads, pipeline, lead scoring):
  - Customers' roadmap keeps "a sales pipeline … its own module, if ever".
  - A quote is where a CRM would hand over. `source_*` on the quote (§4.1) leaves room.
- **Customer portal** (see quotes, orders, deliveries, invoices; confirm or modify):
  - a platform question, since Vantigo has no external-user surface yet;
  - phase 3's "accept through a link" is the narrow first step.
- **Coupons, promotions, loyalty, gift cards, eWallet:**
  - these belong to eCommerce and Point of sale (§4.2);
  - a gift card is also a liability that Invoices or Point of sale must account for.
  - The order stores only their result as a discount or payment line.
- **Shipping cost calculation and carrier labels** (weight, volume, address; UPS,
  DHL, Bring/Posten for Norway):
  - on the delivery, later (§4.2);
  - Products already stores weight and dimensions.
- **Kits:** Products Later (bundles). The line's `parent_line_id` is kept (§4.1).
- **Incoterms:** a header field on the order and the invoice, relevant to export.
  Small, but the EHF has a slot for it. Phase 3.
- **Product-specific onboarding e-mails after purchase:** a later outbox handler.
- **Subscriptions, MRR and churn dashboard:** the Subscriptions module (§4.2).
- **Inter-company mirroring of sales and purchase orders:** not relevant. A Vantigo
  deployment is one company.
- **Marketplace connectors (Amazon):** eCommerce or integrations, through `channel`
  and `external_id`.
- **Mobile selling:** the host is responsive; no separate app.

### 2.2 ERPNext — a document per stage, linked line to line

Sources:
- `erpnext/selling/doctype/sales_order/sales_order.json`, `sales_order_item.json`
- `erpnext/buying/doctype/purchase_order/purchase_order.json`
- `erpnext/stock/doctype/delivery_note_item/delivery_note_item.json`
- `erpnext/controllers/status_updater.py`
- `erpnext/accounts/doctype/subscription/subscription.json`

All on the `develop` branch of <https://github.com/frappe/erpnext>.

- **The chain** is Quotation → Sales Order → Delivery Note and Sales Invoice. Buying
  mirrors it: Material Request → (RFQ, Supplier Quotation) → Purchase Order → Purchase
  Receipt and Purchase Invoice.
- **A submitted document is immutable.** Changing one means cancel and **amend**, which
  creates `-1`, `-2` copies. This is widely felt to be heavy.
- **Every downstream line points at its source line:** `against_sales_order` +
  `so_detail`, and `purchase_order` + `purchase_order_item`.
  - `status_updater` re-sums `delivered_qty` and `billed_amt` into the source on submit
    and cancel, and derives `per_delivered`, `per_billed` and a status from them.
  - The statuses are To Deliver and Bill, To Bill, To Deliver, Completed, Closed and
    On Hold.
- **Over-delivery and over-billing allowances** are set per item, with a bypass role.
- **Billing is tracked as an amount** (`billed_amt`), not a quantity. It drifts when a
  price changes after billing.
- **Blanket Order** is one doctype for both sides (`Selling` or `Purchasing`).
- **The webshop** is a Sales Order with `order_type = Shopping Cart`.
- **Subscription is separate** and generates Sales Invoices directly, with no order.
- **POS** is a separate POS Invoice consolidated into Sales Invoices at closing.
  - The merge has known bugs at session boundaries
    (<https://github.com/frappe/erpnext/issues/41514>).

### 2.3 Microsoft Dynamics 365 Business Central — post to ship, post to invoice

Sources:
- <https://learn.microsoft.com/en-us/dynamics365/business-central/sales-how-sell-products>
- `…/finance-invoice-prepayments`
- `…/sales-how-drop-shipment`
- `…/across-how-use-approval-workflows`

- **Sales documents share one header/line table keyed by Document Type:** Quote, Order,
  Blanket Order, Return Order, Invoice, Credit Memo. Purchase has a parallel pair.
- **Quote → *Make Order***. The quote is archived, and document archiving keeps
  versions.
- **The order line carries editable "to" quantities.**
  - `Qty. to Ship` defaults to the outstanding quantity.
  - `Qty. to Invoice` is capped by what is shipped plus to-ship.
  - Posting writes separate **Posted Shipment** and **Posted Invoice** documents, and
    the order stays open until fully shipped and invoiced.
  - "Shipping Advice = Complete" forbids partial deliveries.
- **Status:** Open, Released, Pending Approval, Pending Prepayment. *Release* locks the
  order for the warehouse, and *Reopen* unlocks it.
- **Prepayment %** is set per customer, item or line. Prepayment invoices are posted
  from the order, and the final invoice deducts them.
- **Drop shipment:**
  - The sales line is flagged.
  - A linked PO is created by a worksheet.
  - Posting the sales shipment posts the purchase receipt.
- **Return orders** create a return receipt and a credit memo, with lines fetched from
  posted documents.
- **Requisition worksheet:** reorder policies propose POs, and *Carry Out Action
  Message* creates them.

### 2.4 The Nordic field — what a small business compares against

Sources: hjelp.tripletex.no articles 15989034598289, 22289692779281, 16025090602769,
48224442210577, 4406376505489, 4408491579537, 4408491926289; hjelpesenter.poweroffice.no
(aksepter-tilbud-konverter-faktura, bulkfakturering-ordrebekreftelser);
help.visma.net eAccounting sales-order pages; hjelp.unimicro.no
flyten-mellom-tilbud-ordre-og-faktura; support.unimicro.no del-fakturering-av-ordre;
hjelp.fiken.no lage-og-sende-tilbud-til-kunde.

**Tripletex** (the reference for Vantigo's segment)
- **Every invoice starts as an order.** A quick invoice is an order invoiced at once.
- **The quote (tilbud) is its own record**, with its own number series, valid-until
  date, a price display with or without VAT, and a link to the order or project made
  from it.
- **The order confirmation (ordrebekreftelse) is a rendering** of the order (PDF and
  e-mail), not a record. Tripletex has no proforma invoice.
- **Subscriptions are repeating orders:**
  - interval, start and end;
  - billed in advance or in arrears;
  - sent automatically;
  - "Fakturert t.o.m." on the order.
- **Samlefaktura** is a per-customer switch. Orders sharing customer, currency and terms
  are invoiced together, and the EHF merges their references.
- **The *Logistikk* add-on** has the pick list (plukkliste, which also drives back
  orders), the packing slip (pakkseddel / følgeseddel), the waybill and purchasing
  (innkjøp).
  - **A purchase order never moves stock.** Only the goods receipt (varemottak) does.
  - PO statuses: Åpen, Sendt, Bekreftet, Varemottak påbegynt, Avvikshåndtering ikke
    fullført, Avsluttet, Kansellert.
- **Webshops and POS connect through third parties** in three styles:
  - an invoice per order (Cloudify for Shopify);
  - one aggregated journal entry per settlement (Sharespine);
  - a Z report per day (Acendy, Zettle connectors).

**PowerOffice Go**
- Quote → *Akseptert* → invoice draft, or straight to an invoice.
- **Orders live inside invoicing.** *Bekreft* sends the order confirmation and parks
  the order for **bulk invoicing**.
- A price and discount matrix per product.
- No native purchasing.

**Visma eAccounting**
- Quotes and order confirmations are switched on as an add-on.
- **An order must be set *Completed* (delivered) before it can be invoiced.**
- A back order (restordre) can be split off, and a delivery note printed.
- **The larger Visma ERP** adds On hold, Credit hold, Back order and Shipping, several
  shipments per order, and a sales order line linked to a purchase order so the goods
  received are reserved for it.

**Unimicro**
- The flow is Tilbud → Ordre → Faktura by *Overfør*.
- **Partial invoicing is a checkbox per line**, or "invoice only delivered goods", and
  the order shows **Del-fakturert**.
- Samlefaktura is in the higher tiers only.
- Webshop orders can be imported.

**Fiken** (the low end)
- Quotes cannot be edited once created; a copy becomes the new draft.
- Fiken says outright that an **order confirmation is not sales documentation**. The
  invoice is.

### 2.5 Commerce platforms — one order core, many channels

Sources:
- <https://shopify.dev/docs/api/admin-graphql/latest/objects/Order>, `…/DraftOrder`,
  `…/FulfillmentOrder`
- <https://shopify.dev/docs/apps/build/orders-fulfillment/order-management-apps/edit-orders>
- <https://shopify.dev/docs/apps/build/purchase-options/subscriptions/contracts>
- <https://docs.medusajs.com/resources/commerce-modules/order/order-versioning>,
  `…/order-change`
- <https://docs.commercetools.com/api/projects/orders>,
  <https://docs.commercetools.com/api/quotes-overview>
- <https://developer.squareup.com/reference/square/objects/Order>,
  `…/docs/orders-api/manage-orders/update-orders`
- <https://docs.stripe.com/billing/subscriptions/overview>
- the Chargebee order documentation

**Shopify**
- **One `Order` for every channel**, with its origin on it: `app`, `sourceName`
  (`web`, `pos`), `sourceIdentifier` and `retailLocation`. A POS sale is an ordinary
  order with a location.
- **Status is several independent dimensions:** financial, fulfilment, return, and
  `closed` / `cancelledAt`.
  - It keeps the original total as a snapshot and the current total as derived.
- **`DraftOrder` is the quote, phone or B2B stage.** Completing it creates the Order,
  and stock commits only then.
- **`FulfillmentOrder`** is the planned work per location, mutable and holdable.
  **`Fulfillment`** is the work done.
  - The split exists so routing, holds and moves never disturb the commercial order.
- **Edits are staged and committed** (`orderEditBegin` → `CalculatedOrder` →
  `orderEditCommit`). Only unfulfilled quantity can change, and the money difference
  becomes a balance due or a refund.
- **A subscription contract lives outside the order.**
  - Each successful billing attempt creates an ordinary order.
  - An idempotency key protects the retries.

**Medusa v2**
- **The cart is a separate module.** Completing a cart creates an order.
- **The order is versioned:**
  - the line item is an immutable snapshot of what was bought;
  - the order item, holding current quantity and price, is copied per version;
  - every edit, return, claim or exchange is an `OrderChange` with typed actions,
    applied on confirm.

**commercetools**
- Orders come from a cart, a quote, or an import.
- **Three states move independently:** `orderState`, `shipmentState` and
  `paymentState`, plus an optional merchant-defined state machine.
- **B2B quotes run in three stages:** QuoteRequest → StagedQuote → Quote. The quote is
  immutable once sent.

**Square**
- **One Orders API for POS, online and invoices.**
  - States: DRAFT, OPEN, COMPLETED, CANCELED.
  - A required `location_id`, `source`, `reference_id`.
  - An optimistic `version`.
  - An idempotency key on every write.
- **Once an invoice references the order, its lines are locked.**

**Stripe and Chargebee**
- **Stripe has no order.** A subscription produces an invoice per period.
- **Chargebee generates shipment "orders" from subscription invoices** for physical
  goods.

### 2.6 What every one of them has

1. A quote stage, either a state of the order (Odoo, Shopify draft) or its own document
   (ERPNext, Business Central, commercetools, Tripletex, Unimicro). It has a valid-until
   date, a PDF and e-mail, and is accepted into an order with a link back.
2. An order header: the counterparty and its reference, dates, currency, payment terms,
   addresses snapshotted, and the person responsible.
3. Lines with product, description, quantity, unit, price, discount and tax, plus
   non-product lines (section and note).
4. Per-line **delivered and invoiced quantities**, with the order's delivery and invoice
   status derived from them.
5. Partial delivery, partial invoicing, and cancel. A lock or close separate from the
   lifecycle.
6. On the purchase side: vendor prices, a PO that does not move stock, receipts against
   the PO, and a bill checked against ordered or received quantities.
7. Order confirmation and purchase order documents sent as PDF by e-mail.

### 2.7 What one or two have

- **Online acceptance:** signature or payment of a quote in a portal (Odoo, Shopify
  draft invoice link).
- **Blanket orders** (Odoo, ERPNext, Business Central).
- **Approvals** above an amount (Odoo Purchase, Business Central, Xledger).
- **Prepayment:** Odoo's down-payment lines, Business Central's prepayment %.
- **Drop shipment and special orders** (Odoo, ERPNext, Business Central, Visma).
- **Returns as their own aggregate** (Shopify, Medusa, Business Central return orders).
- **Versioned edits after confirmation** (Medusa, Shopify).
- **Peppol ordering** (none of the SMB products natively; §8.2).

### 2.8 What nobody does well

- **Abandoned carts in the order table** (Odoo).
- **States that mix lifecycle with progress** (Odoo's `done`, since removed).
- **Down payments as fake lines** (Odoo).
- **Amount-based billing progress** (ERPNext).
- **Cancel-and-amend for every change** (ERPNext).
- **Subscriptions folded into the order** (Odoo).
- **POS consolidation** (ERPNext).

### 2.9 Carriers — Bring, PostNord, Helthjem, nShift, Porterbuddy

Read 2026-11-05 from the carriers' developer documentation.

**Bring** (<https://developer.bring.com/api/>)
- **Authentication.**
  - Three headers: `X-Mybring-API-Uid` (the Mybring login), `X-Mybring-API-Key` and
    `X-Bring-Client-URL`.
  - The key is a user's, not a company's. Customer numbers hang on that user.
  - Prices need the user's *financial* role on the customer number.
  - Every call must come from the backend: since 2024, Bring refuses a browser call
    with a customer number.
  - Do not pin certificates; Bring moved to Let's Encrypt in October 2025.
- **Test mode.**
  - Test customer numbers: 5 (parcel Norway), 6 (Nordic) and 7 (cargo), requested
    from Bring.
  - On a real customer number, the header `X-Bring-Test-Indicator: true` marks a
    test. From 12 October 2026 the header is the only way.
  - **Real phone numbers and e-mails still get notices in test mode**, so tests use
    dummy contacts.
- **Shipping Guide API v2** (`POST /shippingguide/api/v2/products`).
  - Inputs: postal codes, date, packages in grams and cm, and products with a
    customer number.
  - It answers `listPrice`, and `netPrice` when the user has financial rights, each
    with and without VAT. `netPrice` is an estimate, not the invoice.
  - 120 requests per second.
- **Booking API** (`POST /booking/api/create`).
  - The consignment carries a `correlationId`, the parties, the product and its
    additional services, and packages **in kg**.
  - It answers a consignment number, a package number per package, and links to the
    labels (PDF, behind a redirect), the waybill and tracking.
  - **Labels are PDF only.** ZPL exists only for 3570 with an RFID printer.
  - **There is no cancel endpoint** and **no idempotency key**.
    - Modify Delivery can stop a shipment in transit, which is a return, not a
      cancel.
    - An unused label appears not to be charged: billing follows hand-in (inferred,
      unverified).
  - 18 requests per second.
  - The API changes in place, with no v2, so the adapter tolerates unknown fields.
- **Domestic products:**

  | Code | Product | Segment |
  | --- | --- | --- |
  | 5800 | Pakke til hentested | B2C |
  | 5600 | Pakke levert hjem | B2C |
  | 3584 | Pakke i postkassen | B2C |
  | 5801 | Pakkeboks | B2C |
  | 5000 | Pakke til bedrift | B2B |
  | 4850 | Bedrift ekspress | B2B |
  | 5100 / 5300 / 5400 | Stykkgods, partigods, pall | Cargo |
  | 9300 / 9350 / 9000 | Returns | — |

  - Products are withdrawn yearly (9650 in 2025, 0335 in 2026), so the list is data,
    not constants.
- **Tracking API** (`/tracking/api/v2/tracking.json?q=`).
  - It takes a consignment number, a package number **or a reference**.
  - Statuses: PRE_NOTIFIED … IN_TRANSIT … READY_FOR_PICKUP … DELIVERED, DEVIATION,
    RETURN, DELIVERED_SENDER.
- **Event Cast webhooks.**
  - Per customer number (365 days) or per tracking number (30 days).
  - **Unsigned**: a secret header is configured.
  - Few retries, and no history.
- **Pickup Point API:** by postal code and street. It answers opening hours, manned or
  locker, and maximum dimensions.
- **Invoice and Reports APIs:** the invoiced cost per shipment comes from the reports.
  That is the later freight-invoice check.

**PostNord Norway** (<https://developer.postnord.no>)
- Two portals: the Norwegian one with client id and secret, and the group one with an
  API key and a separate sandbox key.
- `transport-booking-v2` offers products with prices, validation, booking with a
  label (ZPL or PDF), and pickups.
- PostNord certifies an integration before production. There is no fee per label.
- Product codes: 19 (service point), 92 (locker), 17 (home), 18 (business), 52 and 83.
  Home delivery was reshaped in 2026.

**Helthjem** (<https://developer.helthjem.no>; public docs of good quality)
- **Authentication:** OAuth2 client credentials, with a 23-hour token. Preprod
  credentials come from Helthjem.
- **Coverage:** a check per address.
- **Booking:**
  - an ordered list of transport solutions with fallback (1 home, 62 service point),
    and `freightProductId` says which one was used;
  - an **`idempotency-key` header**;
  - a tracking reference per item.
- **Labels:** PDF, PNG, SVG or **ZPL**, chosen by `Accept`.
- **Tracking:** polling only, with numeric event codes.
- Cancel is mentioned but not documented. No rate API: prices come from the agreement.

**Porterbuddy** (<https://developer.porterbuddy.com>; owned by Instabee)
- **Coverage:** evening home delivery (17:30–21:30, two windows, every day) in greater
  Oslo, Vestfold, Østfold, Bergen, Stavanger, Trondheim and Kristiansand, about
  2.5 million people.
  - Same evening in Oslo for an order before 12:00, otherwise next evening.
  - Coverage is decided per address by the availability call.
- **Access:** an `x-api-key` header. The test host is `api.porterbuddy-test.com`.
  Production is by invitation.
- **`POST /availability` is the rate call.**
  - It answers delivery windows, each with start, end, product, **price in øre**,
    `expiresAt` and a **token**.
  - The answer is empty when the address is not covered or capacity is full.
- **`POST /order`:**
  - takes a window token, a consolidated window, or `bestAvailableWindow`;
  - takes verification options (PIN, ID, age);
  - takes an **`Idempotency-Key` header**.
- **Labels:** PDF, PNG or **ZPL**, through tokenised, unauthenticated URLs. Porterbuddy
  advises not to store them.
- **Tracking:** polling, or a per-order webhook **signed with HMAC-SHA256** (keyed
  with the API key). No cancel endpoint is documented.
- **Through nShift:** reachable via nShift Delivery and Ship, but slot choice usually
  goes directly to Porterbuddy.

**nShift** (Delivery, Ship, Checkout, Track, Go)
- **What it is:** one label and print flow for 380+ carriers in Norway. The customer
  keeps their own carrier agreements.
- **Price:** a paid subscription per tenant, quote-based, from about €31 to €54 a
  month plus volume tiers. Onboarding is through sales or partners.
- **For a platform,** nShift Go (a headless API with self-service activation of
  carriers) is the likely fit. Partner terms need confirming.

**What one interface must cover:**
- **Rates:** options with a price excluding VAT plus the VAT, an *estimate* flag, an
  expected date **or a time window**, an expiry and an opaque token. Helthjem has no
  rate call, so it answers with none.
- **Booking:**
  - one consignment and many packages, or one order id (Porterbuddy);
  - the product actually booked (Helthjem's fallback);
  - an idempotency key where the carrier takes one. Where it does not (Bring), a
    reference to look up before a retry.
- **Labels:** in a format per carrier capability, PDF everywhere and ZPL where offered.
- **Tracking:** polling everywhere, plus an optional parsed webhook. Bring's has a
  secret header; Porterbuddy's an HMAC.
- **Cancel:** three outcomes, not one. *cancelled*, *not supported* (void locally) or
  *too late*.
- **Optional capabilities:** pickup points, a coverage check and delivery windows,
  declared through `Capabilities()`, not discovered by errors.
- **Units:** grams and millimetres inside. Adapters convert, since Bring's booking
  takes kg.

**Recommendation:** Bring direct first. It is public, self-serve, and tenants already
hold Mybring credentials. Then Helthjem, then Porterbuddy (which adds the delivery
window). nShift comes after as an aggregator adapter for tenants who already pay for
it, with partner terms to settle.

## 3. Sales, purchasing, or both

The owner's comparison is **Odoo Purchase**, which is the buying side: a request for
quotation to a supplier, a purchase order, receipts, and bill control. The channels the
owner names are all **selling**: Subscriptions, eCommerce and Point of sale. Every
product in §2 keeps **sales and purchase as two models with a mirrored shape** and
never one table:
- Odoo: `sale.order` and `purchase.order`;
- ERPNext: Sales Order and Purchase Order;
- Business Central: a Sales Header and a Purchase Header;
- Tripletex: Ordre and Innkjøp.

The shapes match closely: header, lines, a quote stage (RFQ), confirm, fulfil
(deliver or receive) and invoice (sales invoice or supplier bill). The rules differ:
- **Pricing:** a price list for sales, a vendor price for purchasing.
- **VAT:** output VAT on sales, input VAT on purchasing.
- **Stock:** a sale reserves and issues stock; a purchase receives it.
- **Documents:** a sale ends in Invoices' outgoing invoice. A purchase ends in Expenses'
  supplier invoice (`mig/00033_expenses_supplier_invoices.sql`).
- **Counterparty:** a customer for sales, a supplier for purchasing. Vantigo has no
  supplier register: a receipt and a supplier invoice both carry the supplier as free
  text.
- **Channels:** sales has many (web, till, subscription, EHF order in). Purchasing has
  one, plus a reorder suggestion.

**Recommendation:**
- **One module, Orders, with two sides that do not share tables:**
  - `sales_orders` with `sales_order_lines`;
  - `purchase_orders` with `purchase_order_lines`.
- **The shared code:** line arithmetic, snapshots, numbering, the PDF frame, the state
  helpers and the quantity rollups.
- **Sales comes first**, because every channel the owner names consumes it, and it is
  what the outbox's named first events need.
- **Purchasing is phase 2**, because it needs a supplier register (§6.5) and Products
  phase 5 (supplier records per variant).
- If the owner reads "Odoo Purchase" as *purchasing first*, the order of the phases
  swaps and nothing else changes. §11 reading 1 asks.

The module could instead be split into Sales and Purchasing. One module is preferred:
- one permission area;
- one app in the switcher with a *Salg* and an *Innkjøp* section;
- a drop shipment can link a sales line to a purchase line without a cross-module
  contract;
- the shared code needs no `contracts` package.

The cost is a module larger than the others. Inventory and Invoices show that size is
manageable inside one module.

## 4. One order core that channels feed

The market agrees on the split (§2.5, §2.1 Channels, §2.2): an order is the frozen,
agreed record. The things that make a channel different stay outside it.

### 4.1 What lives in the order core

1. **Origin on every order, from the first version, even with only one value in use:**
   - `channel` (an open set of application constants, as Inventory's movement kinds
     are: `manual`, `quote`, later `peppol`, `webshop`, `pos`, `subscription`,
     `import`);
   - `source_module`, `source_kind`, `source_id`: the shape Inventory's movements
     already carry;
   - `external_id` and `external_number`, with a **unique (channel, external_id)**, so
     a webhook delivered twice or a channel import run twice is a no-op;
   - a request id for idempotent creates, as Inventory's `clientRequestId` and Invoices'
     quick invoice do.
2. **A snapshot of everything that can change elsewhere:**
   - the customer's name, organisation number, invoice address, delivery address,
     contact and reference;
   - on each line: SKU, name, unit, unit price, discount, VAT code and rate.
   - The variant id stays as a nullable reference, the way Inventory's lines keep the
     SKU and name next to the id (I8).
3. **The price mode:** `prices_include_vat` on the order. B2C prices must be shown
   including VAT (prisopplysningsforskriften § 3), and B2B prices are shown without it.
   - Each line stores the net amount, VAT and gross amount, and the order stores a VAT
     breakdown per rate, so a total is reproduced exactly.
   - This is a currency amount in exact decimals, never a float (§6.1).
4. **Lines that may carry an externally computed price.**
   - A webshop's promotion, a till's manual discount or an EHF order's agreed price is
     accepted as given, with a price source on the line (`catalog`, `manual`,
     `channel`).
   - The core stores the result of pricing. It does not insist on computing it.
5. **Line kinds:** `product`, `service`, `text` (section or note), and later
   `shipping`, `fee`, `rounding`.
   - A `parent_line_id` is kept now for bundles and modifiers (Products Later: bundles
     and kits; a till's modifiers).
6. **Quantities per line**, kept by the core:
   - ordered;
   - cancelled;
   - delivered;
   - invoiced;
   - later returned.
   The order's delivery and invoice statuses are derived from them (§5.2).
7. **A site** (`site_id`, nullable): where the order is fulfilled from or sold at.
   Point of sale and eCommerce need it, and Inventory reserves per site (inventory
   §12.6).
8. **A version number** with optimistic locking, and an append-only **change log** of
   what changed after confirmation (§5.3).
9. **Domain events through the outbox** (§7): `orders.sales_order.confirmed`,
   `…changed`, `…cancelled`, `…delivered`, `…invoiced`.
10. **One create command** that every channel calls (`contracts.SalesOrders.Create`,
    or the equivalent). It takes a snapshot DTO with lines, origin and request id, and
    returns the order.

### 4.2 What stays out of the core

| Kept out | Where it lives | How it reaches Orders |
| --- | --- | --- |
| **Cart and checkout** (anonymous, short-lived, promotions, shipping rates) | eCommerce | Checkout calls `Create` with `channel=webshop` and the frozen cart. No carts in the order table (Odoo's regret) |
| **Till session, drawer, tenders, receipts, electronic journal, X/Z, SAF-T Cash Register** | Point of sale | The till owns the legal journal (kassasystemforskriften). A completed sale can be pushed as an order that is delivered and paid at once, or not at all for a plain counter sale. Click-and-collect, special orders and an order settled at the till use the core. Never a synchronous write from the till |
| **Subscription contract, plan, cadence, proration, pause, dunning** | Subscriptions | Each period either invoices directly (services, as Stripe and ERPNext do) or creates an order (physical goods, as Shopify and Chargebee do), with `channel=subscription`, `source_id` = contract + period, idempotent. Not Odoo's merge, but see §11 reading 5: the Nordic field does subscriptions as repeating orders |
| **Pricing engine, promotions** | Products phase 4, eCommerce | The core asks Products for a price on manual entry and stores the result |
| **Payments and payment state** | Invoices and the payments port, Point of sale | An order shows what was paid against its invoices or its prepayment request. It keeps no transaction table of its own in phase 1 |
| **Carrier labels, shipping integrations** | Later, on the delivery | Not on the order |

### 4.3 Is a quote an order?

There are two models:
- **A state of the same record:** Odoo, a Shopify draft.
- **Its own document:** Business Central, ERPNext, commercetools, Tripletex, Unimicro.

The Norwegian SMB field is unanimous for **its own document with its own number
series**, as the customer sees "Tilbud 1042" and then "Ordre 2210". Three things favour
the separate document:
- A quote can lead to several orders (commercetools' "a pending quote can be ordered
  more than once"), or to a project (Tripletex's *Opprett prosjekt*).
- A quote that is sent is frozen (Fiken, commercetools): a change is a new **revision**.
- An order that is not from a quote (web, EHF, POS) never passes through quote states.

**Recommendation:** `quotes` and `quote_lines` are their own tables in Orders. They
share the line shape and code with sales orders. Accepting a quote creates an order
with `channel=quote` and `source_id=quote@revision`.

## 5. The order's life

### 5.1 States

The convergent shape (Odoo v17–v19, Square, commercetools): a **small lifecycle**, a
**lock** kept apart from it, and **progress derived from the lines**.

**Quote**
- `draft` → `sent` → `accepted` | `declined` | `expired`;
- plus `cancelled`;
- revisions while `sent`.

**Sales order**
- `draft` → `confirmed` → `completed`;
- plus `cancelled`, allowed before anything is delivered or invoiced;
- `on_hold` as a flag on a confirmed order. It stops deliveries and invoicing, as
  Visma's On hold and Credit hold do and as Shopify places holds on fulfilment;
- **Completed is reached** when every line is delivered and invoiced, or closed short
  (the rest cancelled).

**Purchase order (phase 2)**
- `draft` → `sent` → `confirmed` (supplier's acknowledgement) → `completed`;
- plus `cancelled`;
- `to_approve` only if approvals are taken (§11).

**Derived on the sales order, stored for lists and filters**
- Delivery: `none`, `partial`, `delivered`.
- Invoice: `none`, `partial`, `invoiced`.
- What Unimicro shows as *Del-fakturert* is invoice = `partial`.

### 5.2 Line quantities and the invoice policy

- Each line holds:
  - `qty_ordered`;
  - `qty_cancelled`;
  - `qty_delivered`;
  - `qty_invoiced`;
  - later `qty_returned`.
- Quantities are counted, not amounts, so a price change cannot make billing progress
  drift (ERPNext's regret).
- **The invoice policy is per line**, defaulted from the product type:
  - **goods: on delivery**, so only delivered and not yet invoiced quantity is offered
    to Invoices (Visma's "Completed before invoiced", Unimicro's "fakturer kun leverte
    varer");
  - **services: on order**.
  - The owner may prefer *on order* for everything in phase 1 (§11).
- **Delivered quantity is fed by a method per line**, as Odoo's `qty_delivered_method`:
  - `manual`: the user marks a service or a non-stock item delivered;
  - `delivery`: an Orders delivery document that issues stock through Inventory;
  - later `time` / `project`: hours or milestones.
  - Vantigo's Time and Projects already bill hours and milestones directly through
    Invoices. Phase 1 does not route them through orders.
- **Over-delivery** is refused by default, and an allowance is a later setting
  (ERPNext).

### 5.3 Editing after confirmation

There are three positions:
- Odoo edits freely.
- ERPNext cancels and amends.
- Shopify and Medusa stage a change and commit it.

**Recommendation, the middle path:** a confirmed order stays editable in the open
quantity only.
- **Allowed:** add a line, raise or lower a quantity down to what is delivered or
  invoiced, change a price on what is not invoiced, change the delivery date or
  address.
- **Each save writes a change-log row and bumps `version`.**
- **Refused:** a change to anything delivered or invoiced. That is a return or a credit
  note.
- **The order confirmation as sent is kept as a PDF** (§8.1), so the log plus the
  stored PDFs are the history. No full copies per version, as Medusa makes.

### 5.4 Deliveries

- **A delivery is its own document** in Orders: *Leveranse* in Norwegian, with the pick
  list and packing slip as its printouts. It is per site, with lines against order
  lines.
- This matches Shopify's FulfillmentOrder → Fulfillment, Business Central's posted
  shipment and Tripletex's pick list.
- **Posting it** sets `qty_delivered` and, for goods, issues the stock through
  `contracts.StockLedger.Issue` with the delivery as the source.
- **The rest stays open** as a back order (restordre) on the same order. Visma's
  separate back-order document is not needed when the line holds what remains.
- **Reservation** happens on confirm, per site, through `StockLedger.Reserve`, and is
  released by delivery, cancel or edit. The owner may defer reservations (§11).

## 6. The seams to the modules that exist

### 6.1 Products

What exists (`contracts/catalog.go:83-113`):
- `ProductCatalog` with `Variant`, `Variants`, `ListPrice`, `SearchVariants` (goods
  only), `LookupCode` and `LookupSKUs`;
- `LockVariants(tx)`, the one locking read on a caller's transaction (MB rule 21);
- product `type` Goods or Service;
- dated `ProductPrice` rows per variant and currency, excluding VAT;
- `TaxCategory` with a rate;
- packagings (`mig/00109`).

What Orders needs:
- **`ListPrice` returns a `float64`** (`*Money{Amount float64}`). Orders needs an exact
  decimal, so the contract must change. The change is small, and Projects is the only
  other caller.
- **The tax category and rate are not on `VariantEntry`.** Orders needs them to
  snapshot VAT on a line.
- **`SearchVariants` excludes services.** An order line sells both.
- **Pricing depth (phase 4):** price lists, customer-group prices, quantity breaks.
  - Phase 1 can live with the list price, a manual override and a line discount, as
    Tripletex and Fiken do.
  - Price lists come as their own delivery, designed in Products with the resolution
    order documented (§11).
- **Units and supplier records (phase 5):** these are needed by purchasing, not by
  phase 1.

### 6.2 Two VAT models

Products has tax categories: Standard, Reduced, Zero and Exempt, each with a rate.
Invoices has its own `vat_codes`, with SAF-T codes, UNCL5305 categories and dated rates.
- **Nothing maps one to the other**, and Invoices never reads the catalog.
- An invoice drafted from an order must carry the right Invoices VAT code per line.

Options:
1. **Map each tax category to an Invoices VAT code**, as a setting in Invoices, and
   have Orders snapshot both.
2. Have Orders hold the Invoices VAT code itself.

**Recommendation: (1)**, done in delivery 0 (§10). It also fixes Projects' billing
lines, which have the same gap.

### 6.3 Inventory

What exists:
- the movement source reference, all four columns NULL today (`mig/00108:72-96`);
- the open set of movement kinds;
- receipts with a free-text supplier;
- sites of kind `warehouse` and `store`.

What Orders builds, as Inventory's spec §11 left it:
- **`contracts.Stock`**, the read: on hand and available per variant, in total and per
  site. The product page and the order line both show it.
- **`contracts.StockLedger`**, the write, on the caller's transaction, with a fixed lock
  order:
  - Products' `LockVariants` first;
  - Orders' rows next;
  - Inventory's balances last.
  - Every call carries the source reference.
  - Calls:
    - `Reserve`, `Release` and `Issue` for sales;
    - `Receive` for purchase receipts;
    - later `Transfer`.
  - Each needs a new MB rule, like rule 10 and rule 20.
- **Reservations** as rows against an order line, per site (inventory §12.6). Available
  = on hand − reserved.
- **Movement kinds** `issue`, then `transfer` (sent, in transit, received) in phase 2
  with purchasing.
- **The store flag *fulfils orders*** on a site, used in the site picker on an order.

### 6.4 Invoices

What exists:
- **Pull, not push.** Invoices drafts from single-provider reads (`BillableHours`,
  `BillableExpenses`, `BillableMilestones`, `ConsumptionProvider`) and records each
  line's origin in `invoices.line_sources`. `source_kind` matches `^[a-z]+\.[a-z_]+$`
  since `mig/00078`.
- **At issue**, it calls every `contracts.InvoicedWorkHolder` in `InvoicedWorkOrder`
  inside the issue transaction (MB rule 10). A credit releases.
- **The invoice header has `order_reference`**, and the EHF renders
  `cac:OrderReference`.
- **Credit notes, payments (OCR, camt.054, Vipps), reminders, KID, billing runs** and
  the quick invoice already exist.
- **No contract lets another module create an invoice.**

**Recommendation:** Orders becomes one more provider in the same pattern.
- **A new single-provider slot, `contracts.BillableOrders`:** what each order offers to
  invoice per line (to-invoice quantity, snapshot price, VAT code, the order's
  reference, its project).
- **A new source kind** `orders.sales_line`.
- **Orders implements `InvoicedWorkHolder`**, appended to `InvoicedWorkOrder`, so issue
  stamps `qty_invoiced` and a credit releases it.
- **An Invoices wizard, *From orders*,** for one order, several orders of one customer
  (**samlefaktura**), or all due (**billing run**, reusing the energy run's lease
  worker).
- **The order's *Invoice* button** opens the wizard pre-filled. It does not write an
  invoice itself.
- This keeps "only Invoices writes invoices", and it is how hours, expenses, milestones
  and energy already reach an invoice.

### 6.5 Customers, and the supplier register

What exists:
- typed addresses: postal, invoice, delivery, visiting;
- the billing profile (terms, currency, language, Peppol id, buyer reference, invoice
  e-mail);
- groups with default terms;
- contact roles;
- merge, export and erase holders.

What Orders needs:
- **A delivery address in the contract.** Only the resolved invoice address crosses
  today.
- **Orders fills `CustomerReferenceHolder`**: a merge re-points orders, and an erase
  anonymises the snapshot. It also fills `CustomerPersonalData`.
- **The timeline:** "order confirmed" and "quote accepted" on the customer, riding the
  outbox (ROADMAP Customers phase 5, waiting on this).

**Suppliers** (purchasing, phase 2):
- Vantigo has none, and the roadmap lists "customer-is-also-supplier" for when
  purchasing arrives.
- Three places it could live:
  1. **Customers grows a supplier role**: one party register with a role per party, as
     Odoo's `res.partner` and Tripletex's shared contact list do. Brreg lookup, merge,
     GDPR and import come for free.
  2. **A Suppliers module of its own.**
  3. **Inside Orders.**
- **Recommendation: (1).** Expenses' supplier invoices and Inventory's receipts can
  then move from free text to a reference. This is a Customers delivery, before
  purchasing (§11).

### 6.6 Communications, PDF, numbering

- **Sending the quote, the order confirmation and the purchase order:**
  - through `contracts.OutboundMail` with a stored `send_channel` and a delivery log,
    copying `invoices/send.go` and `invoices.deliveries`;
  - nil Communications means no sending, only downloads.
- **PDFs:** maroto v2 as in `invoices/pdf.go`, with Orders' own fonts, and stored
  through the storage port as `invoices/pdfstore.go` does.
- **Numbers:**
  - Orders' own `counters` table, gapless by upsert under the row lock, as Invoices and
    Projects keep theirs;
  - one series each for quotes, orders, deliveries and purchase orders, each with a
    configurable start;
  - a channel's own number is the `external_number`, never the order number.

### 6.7 Projects

- Projects' design names "materials (Orders / Warehouse)". A **project on an order**
  (`project_id`, like the invoice header's) lets an order's lines reach the project
  economy.
- A quote can make a project (Tripletex).
- Both are small and optional. §11 asks whether phase 1 includes them.

## 7. The platform work Orders carries

### 7.1 The domain-events outbox

It is decided (ROADMAP), and it is built with its first real consumer. Nothing of it
exists. `srv/worker` has no locking, every lease worker rolls its own claim, and no
`DomainEvent` type exists anywhere. The minimum:

- **A platform package**, `srv/events`:
  - one `events.outbox` table: id, name, version, payload JSONB, occurred_at, the
    publishing module, and lease and attempt columns copied from
    `communications.outbox_jobs`;
  - a `Publish(tx, event)` that inserts on the publisher's transaction.
- **A handler registry** filled by modules at boot (a many-provider slot):
  - each handler is keyed by event name and runs at least once;
  - a handler is idempotent by event id, through a per-handler `processed` table or the
    handler's own unique key.
- **One leased dispatcher worker** with retries, backoff, a dead-letter state and an
  admin view of stuck events.
- **Event contracts are DTOs in `srv/contracts`**, versioned by name
  (`orders.sales_order.confirmed.v1`).
- **First consumers:**
  - Communications sends the confirmation, if the owner wants it automatic;
  - Customers writes the timeline;
  - the Assistant may later listen.
- **What stays synchronous:** reservation and stock issue are writes that must succeed
  or refuse with the order, so they stay synchronous contract calls on the
  transaction. The outbox carries what may lag.
  - This deviates from the roadmap's example ("Inventory reserves stock" as an event),
    and §11 asks.
  - An asynchronous reserve cannot refuse a confirm for lack of stock, and every
    product in §2 decides availability at confirm.

### 7.2 Other platform touches

- **Every registry in AGENTS.md "Adding a module":**
  - `everyModule()`;
  - `APP_ROUTE_PREFIXES` and `appRoutes`, with a `promptVersion` bump;
  - depguard;
  - `modulesEnv`;
  - `MODULES` dependencies (Orders depends on Products and Customers; Inventory and
    Invoices are optional);
  - the host's registrations and `sessionClients`.
- **Docs in both languages** for the user and admin guides, and `go generate` for the
  Assistant's embedded docs.

## 8. The Norwegian rules that touch orders

### 8.1 Bookkeeping

- **An order or quote is not a sales document.** The invoice is (Fiken says so
  outright).
- **An accepted order is secondary documentation** under bokføringsloven § 13, kept
  **3 years and 6 months**. The same holds for packing slips.
  - Regnskapsstiftelsen's GBS 6, revised March 2026, says so (unverified: the PDF did
    not extract, and this rests on the search summary).
  - **Design consequence:** what was sent and what was agreed is kept immutable. That
    means stored PDFs of each sent confirmation and the change log, and no hard delete
    of a confirmed order while its retention runs. This matches the erase rule
    Invoices already follows.
- **The invoice is due within a month of delivery** (bokføringsforskriften § 5-2-2).
  - A delivered-but-uninvoiced list with its age is worth having in phase 1.
  - Billing runs already exist to clear it.
- **Prepayment:**
  - **A request for prepayment is not an invoice** and carries no VAT (Skatteetaten's
    principle statement on financial advances).
  - **Advance invoicing** is allowed only for listed services such as subscriptions,
    rent and fees, up to a year ahead (§ 5-2-6; GBS 13, revised August 2026).
  - So an order needs a **prepayment request** (*forskuddsforespørsel*) with a KID,
    whose payment the final invoice deducts. It must not become an Odoo down-payment
    line or an advance invoice. Later, not phase 1 (§11).
- **E-invoicing B2B is law:**
  - **from 1 January 2027**, an e-invoice to every buyer registered in ELMA;
  - **from 1 January 2028**, the buyer's organisation number on the sales document
    (the regulations of 29 September 2026; Skatteetaten).
  - Orders snapshots the organisation number and the buyer reference from day one, so
    the invoice drafted from it complies.

### 8.2 Peppol ordering

- **What it is:** Peppol BIS 3 post-award (<https://docs.peppol.eu/poacc/upgrade-3/>).
  - Order (T01) and Order Response (T76) make the Ordering profile.
  - There are also Order Change and Cancellation (Advanced Ordering), Order Agreement,
    Despatch Advice and Catalogue.
  - EHF Ordre 3.0 is DFØ's Norwegian guide:
    <https://anskaffelser.dev/postaward/g3/spec/current/ordering-3.0/guide>.
  - The response codes are AB (received), AP (accepted), RE (rejected) and CA
    (accepted with changes, with line codes).
- **No law mandates it.** Buyers' contracts require it: the health regions and
  Sykehusinnkjøp, and the state's e-commerce platform.
- **Vantigo has none of it.** `srv/peppol` only looks up whether a participant receives
  BIS Billing. Storecove, the access point, carries other document types (unverified
  for ordering).
- **The minimum, later:**
  1. register receiving capability for Order;
  2. turn an incoming T01 into a draft order with `channel=peppol`;
  3. answer with T76 AP or RE, then CA;
  4. carry the buyer's order reference to the invoice, which already happens.
- **For purchasing**, a PO sent as T01 to a supplier that can receive it is the mirror
  image.
- **A later delivery**, sized for the customers who supply the public sector.

### 8.3 What the channels will need, kept in view now

- **eCommerce** (angrerettloven):
  - § 18: confirmation on a durable medium, by delivery at the latest. E-mail of the
    order confirmation is the confirmation;
  - § 16: the order button text "bestilling med forpliktelse til å betale";
  - 14-day withdrawal and refund within 14 days, so **returns** become a core aggregate
    when eCommerce arrives;
  - prices including VAT (§4.1, item 3);
  - internet sales are **not** cash sales (bokføringsforskriften § 5-3-1), so a webshop
    needs no kassasystem.
- **Point of sale:** the till's journal, receipts and X/Z reports stay in Point of sale
  (§4.2). What Orders gives the till:
  - an order that can be settled at the till;
  - click-and-collect;
  - the site.
- **Subscriptions:** § 5-1-6a lets a fixed periodic fee under a written agreement be
  documented by the payment record. § 5-2-6 lets it be invoiced a year ahead.

## 9. The must-have list (sales, phase 1)

1. **Quotes (tilbud):**
   - their own series;
   - valid-until date;
   - customer and contact;
   - lines from the catalog or free text, with sections and notes, discount, and price
     with or without VAT;
   - PDF and e-mail;
   - revisions while sent;
   - accept, decline, expire;
   - *Make order*, keeping the link;
   - **quote templates**;
   - **optional lines** the customer may choose;
   - **discount shown or hidden**;
   - a **salesperson** (§2.1a).
2. **Sales orders (ordre):**
   - their own series;
   - customer snapshot with organisation number, invoice and delivery address,
     *Deres referanse* and buyer reference;
   - delivery date, site, payment terms, currency, price mode;
   - lines as above with an invoice policy;
   - confirm, hold, cancel, complete;
   - the order confirmation as PDF and e-mail, stored as sent;
   - the change log after confirmation.
3. **Invoicing from orders** through Invoices:
   - the whole order;
   - **delfakturering** by line or quantity, or delivered only;
   - **samlefaktura** across a customer's orders;
   - a billing run;
   - the order reference on the EHF;
   - credits release the quantity.
4. **Deliveries:**
   - a delivery per site with pick list and packing slip;
   - partial delivery with the rest as back order;
   - stock issued through Inventory when it is on, manual marking when it is off or
     for services;
   - stock shown on the line.
5. **The channel-ready core:** origin, external id and idempotency, snapshots, the price
   mode, exact decimals, one create contract (§4.1).
6. **Lists** that answer the daily questions:
   - quotes awaiting an answer;
   - orders to deliver;
   - orders delivered but not invoiced, with age (§ 5-2-2);
   - orders on hold.
   - **Analysis:**
     - quote hit rate;
     - order intake by period, salesperson, customer and product;
     - the backlog (§2.1a).
7. **Sales warnings** on customers and products, and **variant grid entry** on lines
   (§2.1a).
8. **Platform:** the outbox (§7.1), the Products contract fixes (§6.1), the VAT mapping
   (§6.2), the customer delivery address (§6.5).

## 10. Phases and deliveries, as first proposed

**Phase 1: Sales**
- **0: Platform and seams.**
  - The outbox package and dispatcher, with one event and handler end to end.
  - `ListPrice` as an exact decimal.
  - Tax category and rate on the variant, and services in search.
  - The tax category → VAT code mapping in Invoices.
  - The delivery address on `CustomerBillingProfile`.
- **A: Quotes and orders.**
  - The module scaffold with every registry.
  - Quotes, orders, lines, numbering.
  - States, hold, cancel, the change log.
  - PDF and e-mail.
  - The create contract and origin fields.
  - Customer merge and erase holders.
  - The timeline event.
  - Quote templates, optional lines, discount visibility, salesperson.
- **A2: Selling comfort.**
  - Sales warnings on customers and products.
  - Variant grid entry.
  - The analysis views: hit rate, intake, backlog.
- **B: Invoicing from orders.**
  (Delivery **S** follows B; see the end of this list.)
  - `BillableOrders`, the holder and the source kind.
  - The *From orders* wizard with samlefaktura.
  - The billing run.
  - Invoice policy and status.
- **C: Deliveries and stock.**
  - `contracts.Stock`, `contracts.StockLedger` (Reserve, Release, Issue).
  - Reservations per site, the `issue` kind.
  - Deliveries with pick list and packing slip, back orders.
  - The site flag *fulfils orders*.
  - Stock on the line and the product page's *Reserved* figure.

- **S: Subscriptions** (owner, 2026-11-04; §11.2 (b)). It comes after B and can run
  in parallel with C.
  - **Agreements (*abonnement*)** with lines, a plan (interval, in advance or in
    arrears, start, end, notice), and a status (`active`, `paused`, `ended`).
  - **Started by hand or from an accepted quote.**
  - **Due periods offered to Invoices** as `orders.subscription_period` through B's
    seam, using B's billing run.
  - **"Invoiced through" on each agreement.**
  - **A line change takes effect from the next period.** No proration.
  - **Goods periods** that create orders with `channel=subscription` come only if S3 is
    answered *goods too*.

**Phase 2: Purchasing** (*superseded: purchasing becomes its own app, §11.1*)
- **D: Suppliers** (a Customers delivery: the supplier role, §6.5) and **supplier
  records per variant** (Products phase 5: vendor SKU, price, lead time, minimum
  quantity).
- **E: Purchase orders.**
  - RFQ → PO.
  - PDF and e-mail.
  - **Receipts against a PO** (Inventory's receipt gains the source reference).
  - Partial receipts and deviations.
  - Received and billed status.
  - Expenses' supplier invoice matched to the PO and receipt.
- **F: Replenishment and transfers.**
  - A reorder suggestion from min/max (Inventory placement, which was waiting for the
    owner) creating draft POs per supplier.
  - Two-step transfers between sites.
  - Drop shipment linking a sales line to a PO line.

**Phase 3: Depth**
- Price lists, customer-group prices and quantity breaks (Products phase 4).
- Online acceptance of a quote through a link.
- The prepayment request.
- Returns (RMA) with credit note and restock.
- Peppol Order in and Order Response out.
- Purchase approvals.
- Blanket orders.

**Later modules that consume the core:** Subscriptions, eCommerce, Point of sale (§4.2).

## 11. Readings for the owner

### 11.1 The owner's answers, 2026-11-04

| Reading | Answer |
| --- | --- |
| 1, 2 | **Sales first.** Purchasing comes later, **probably as its own app**. This overrides §3's one-module recommendation. Orders is the sales module: quotes, sales orders, deliveries. |
| 4 | **Deferred until purchasing starts.** Nothing in the sales phases needs a supplier. |
| 5 | **(b): a subscription aggregate inside Orders, in its own tables** (§11.2). It is **in this phase**, as a delivery after B. A line change takes effect **from the next period**, with no proration. S3 (services only, or goods too) is not yet answered. |
| 8 | **Reserve stock directly:** a synchronous `StockLedger.Reserve` on the confirm transaction, able to refuse. The outbox carries only what may lag. |
| — | **Shipping is delivery D, after C, inside Orders** (2026-11-05). It covers:<br>• shipments on the delivery;<br>• the `shipping` line kind with a carrier estimate;<br>• a platform port `srv/shipping` shaped like `srv/payments`, with Bring first;<br>• carrier accounts under Integrations;<br>• the waybill and the tracking link on the packing slip and in the e-mail.<br>Research first (§2.9): Bring, PostNord, Helthjem, nShift and Porterbuddy. Shipping becomes its own app only if it grows beyond orders. |
| S3 | **Services only at first.** Each period becomes an invoice line, and no orders are created per period. |
| 3, 6, 7, 9–17 | **As recommended** (owner, 2026-11-04: "Agree"). |

**What "as recommended" settles:**
- **Readings 3, 6 and 7:**
  - a quote is its own document;
  - the invoice policy is per line, defaulted from the product type, and *on order*
    only until C;
  - stock is reserved at confirm, per site, and the user may confirm without
    reserving.
- **Readings 9–14:**
  - the open quantity stays editable, with a change log and a *Lock* action;
  - the confirmation is sent by a button, with a setting to send it on confirm;
  - price lists come later;
  - `project_id` is on the order in A;
  - prepayment, Peppol ordering and returns are in phase 3;
  - the permissions are as listed, with confirm separate from edit.
- **Readings 15–17:**
  - templates, optional lines, discount visibility and the salesperson are in A;
    warnings, the variant grid and analysis are in A2;
  - pro forma is a print variant;
  - a portal and CRM stay outside Orders.

**Consequences of answer 1:**
- The module stays `orders` / *Ordre*, and is about sales.
- The code that sales and purchasing would share (line arithmetic, snapshots, the PDF
  frame, numbering):
  - is kept in Orders now;
  - is lifted into a platform package when purchasing is designed, not built
    speculatively.
- **Drop shipment** (a sales line linked to a purchase line) becomes a cross-module
  contract when purchasing arrives. Its source reference on the order line (§4.1)
  already has the shape.
- **Phase 2 of §10 (D, E, F) leaves this module.** It becomes the purchasing app's own
  research, starting with reading 4.

### 11.2 Subscriptions — the choice still open

What the research says:
- **Placing it and modelling it are separate questions.**
  - §11 reading 5 argued against **modelling** a subscription as an order with
    recurrence fields on the order row. That is Odoo's choice, which loads the order
    with cadence, renewal and churn.
  - It did not argue strongly about which module owns the subscription.
- **The Nordic field expects recurring billing from day one** (§2.4): Tripletex and
  PowerOffice do it as repeating orders.
  - Most of what a Vantigo tenant bills recurringly is a **service**: support
    agreements, rent, licences, maintenance. Hours and energy already have their own
    paths.
- **A service subscription produces invoices, not deliveries** (Stripe, ERPNext).
  - It reaches Invoices the way energy does: a provider read, a holder, a billing run
    (§6.4).
  - Only a subscription of **goods** needs an order per period to deliver against
    (Shopify, Chargebee).

There are three options.

**(a) Repeating orders in Orders, as Tripletex does**
- An order gets an interval, start, end, in advance or in arrears, and "invoiced
  through".
- Cheapest. A user who knows Tripletex recognises it.
- Each order row carries schedule fields that most orders do not use.
- Price changes, pausing and ending mid-period are edits of a live order.

**(b) A subscription aggregate inside Orders, its own tables**
- An agreement (*abonnement*): customer, lines, plan (interval, in advance or arrears,
  start, end, notice), status (`active`, `paused`, `ended`), and the periods invoiced.
- It may start from a quote ("accept" creates the agreement instead of an order).
- Each due period:
  - goes to Invoices as `orders.subscription_period` through the same
    `BillableOrders`-style read and holder;
  - or, for goods, creates an order with `channel=subscription` and a source of
    agreement + period.
- It shares the line shape, snapshots, numbering, PDF and the customer seams with
  orders.
- **This is the shape a separate module would have, without a module boundary.**

**(c) A Subscriptions module of its own**
- The same model as (b), behind contracts.
- Clean, but it duplicates the line and snapshot code, or forces the platform package
  early.
- It needs every registry, and a contract for "create an order" and "start from a
  quote".

**Recommendation: (b).**
- It keeps the order free of recurrence, which was the real point of reading 5.
- It avoids a second module, which was the owner's instinct.
- It can be lifted out later, as purchasing will be, if it grows (MRR, proration,
  dunning, self-service).
- It is not in phase 1. It is a **delivery after B**, because it needs B's invoicing
  seam.

**What phase 1 must keep for it:**
- the `subscription` channel value;
- the source reference on the order;
- a quote whose acceptance can create something other than an order;
- an Invoices source kind pattern (`orders.*`) wide enough for a second kind.

Questions to settle:
- **S1:** (a), (b) or (c)?
- **S2:** Is recurring billing needed soon, so a delivery right after B, or after
  deliveries (C)?
- **S3:** Services only at first (invoices per period), or goods too (orders per
  period)?
- **S4:** Proration when a line changes mid-period, or changes take effect from the
  next period only?
  - **Recommended:** next period only at first. Tripletex does not prorate either.

### 11.3 The readings as first put

1. **Sales first, or purchasing first?**
   - Odoo Purchase is the buying side. The channels named (subscriptions, eCommerce,
     POS) are the selling side.
   - **Recommended:** one module with both sides, **sales first** (§3).
2. **One module, or Sales and Purchasing as two?**
   - **Recommended:** one, *Orders / Ordre*, with *Salg* and *Innkjøp* sections (§3).
3. **A quote as its own document** with its own series, or a state of the order as in
   Odoo?
   - **Recommended:** its own document, as every Norwegian product does (§4.3).
4. **Where suppliers live:**
   - a supplier role in Customers (one party register),
   - a Suppliers module,
   - or inside Orders?
   - **Recommended:** a role in Customers (§6.5).
5. **Subscriptions:**
   - its own module that invoices per period and creates orders only for physical
     goods (Shopify, Stripe, ERPNext),
   - or repeating orders as Tripletex, PowerOffice and Odoo do?
   - **Recommended:** its own module. The order stays free of cadence, renewal and
     churn. A subscription can still start from a quote.
6. **Invoice policy in phase 1:**
   - goods on delivery and services on order,
   - or everything on order until deliveries (C) land?
   - **Recommended:** per line, defaulted from the product type, with *on order* the
     only option until C.
7. **Reservations at confirm** (in C), or deliveries that issue stock without
   reserving?
   - **Recommended:** reserve at confirm, per site. Synchronous, refusing a confirm the
     stock cannot cover unless the user confirms without reserving.
8. **Reserve synchronously or through the outbox?**
   - The roadmap's example was an event.
   - **Recommended:** synchronous for stock, the outbox for confirmations, the timeline
     and everything that may lag (§7.1).
9. **Editing a confirmed order:**
   - open quantity editable with a change log,
   - or locked once confirmed (reopen to edit, as Business Central does)?
   - **Recommended:** editable in the open quantity, with a *Lock* action for orders
     that must not change (§5.3).
10. **The order confirmation sent automatically** on confirm, or by a button?
    - **Recommended:** a button, with a per-tenant setting to send on confirm.
11. **Price lists** in phase 1, or list price + manual price + discount until phase 3?
    - **Recommended:** later, as the Nordic SMB field manages without them.
12. **Projects on orders** (`project_id`, quote → project) in phase 1?
    - **Recommended:** `project_id` on the order in A. Quote → project later.
13. **Prepayment request, Peppol ordering, returns:** confirm they wait for phase 3.
14. **Permissions.** A first cut:
    - `orders:access`;
    - `orders:quotes-manage`;
    - `orders:sales-manage`;
    - `orders:sales-confirm` (if confirming is separate from editing);
    - `orders:deliveries-manage`;
    - `orders:purchases-manage`;
    - `orders:purchases-approve`;
    - `orders:settings`.
    - Is confirming separate from editing?
15. **Odoo Sales' quotation features in phase 1** (§2.1a):
    - quote templates, optional lines, discount visibility, salesperson, sales warnings,
      variant grid, analysis;
    - which are in delivery A, and which wait?
    - **Recommended:** templates, optional lines, discount visibility and salesperson
      in A. Warnings, the variant grid and analysis in a delivery A2 after A.
16. **Pro forma** as a print variant of the quote and order confirmation, or left out
    as Tripletex does?
    - **Recommended:** include it. It is a title on the same PDF.
17. **A customer portal:** confirm that it is a platform decision outside Orders, with
    phase 3's acceptance link as the first step.
    - **And CRM:** confirm that it stays out of Orders.
