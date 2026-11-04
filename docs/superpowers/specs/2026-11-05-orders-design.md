# Orders — quotes, sales orders, invoicing, subscriptions and deliveries (phase 1)

Revision 2, 2026-11-05 (after a critic review and the owner's shipping addition; §16 lists what changed). This spec is drawn from the research
`docs/superpowers/research/2026-11-04-orders-module.md` ("R"), whose §11.1 answers it
does not reopen. Decisions are numbered O*n*. The implementation plan will be
`docs/superpowers/plans/2026-11-05-orders-plan.md`. Abbreviations:

- `srv/` stands for `apps/server/internal/`;
- `mig/` stands for `srv/db/migrations/`;
- "MB" stands for `docs/src/content/docs/en/contributing/module-boundaries.md`.

## 1. Purpose

A module, **Orders** (`orders`; Norwegian **Ordre**; package `@vantigo/orders-ui`), for
the selling side of a business:

- **Quotes** (*tilbud*) are offered to a customer, revised while sent, and accepted
  into an order or an agreement.
- **Sales orders** (*ordre*) record what was agreed. They carry delivered and invoiced
  quantities per line, so it is always visible what remains to deliver and to invoice.
- **Invoicing from orders** goes through Invoices' own wizard, including partial
  invoicing (*delfakturering*) and combined invoicing across a customer's orders
  (*samlefaktura*).
- **Subscriptions** (*abonnement*) are agreements that Invoices bills period by period.
- **Deliveries** (*leveranser*) issue stock through Inventory, with stock reserved when
  an order is confirmed.

The module is also the core that later channels feed: eCommerce, Peppol orders, and
Point of sale's click-and-collect. It therefore carries origin, external references and
snapshots from its first version (R §4.1).

Purchasing is **not** in this module (R §11.1). It becomes its own app with its own
research.

**Seven deliveries, in this order:**

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **0** | **Platform and seams.** The domain-events outbox; exact-decimal prices, VAT and services on the catalog contract; Invoices' tax-category → VAT-code mapping and the reads Orders needs (VAT codes, the seller); the customer's delivery addresses and contacts on the customer contract | — |
| **A** | **Quotes and orders.** The scaffold, quotes with revisions, templates and optional lines, orders with confirm, hold, lock, cancel and the change log, PDFs and e-mail, manual delivery marking, the customer and variant holders, and the first events | 0 |
| **A2** | **Selling comfort.** Sales warnings, variant-grid entry, the analysis views | A |
| **B** | **Invoicing from orders.** `contracts.BillableOrders` with allocations, the holder, Invoices' *From orders* wizard with samlefaktura, and the billing run | A |
| **S** | **Subscriptions** for services, billed per period through B's seam | B |
| **C** | **Deliveries and stock.** `contracts.Stock` and `contracts.StockLedger`, reservations, the delivery document with pick list and packing slip, back orders, and the *on delivery* invoice policy | B (the policy), Inventory |
| **D** | **Shipping.** Shipments on a delivery (carrier, service, packages, tracking, label, booked cost, status), the `shipping` line kind with a carrier estimate, the platform port `srv/shipping` with Bring as the first adapter, carrier accounts under Integrations, the waybill and the tracking link on the packing slip and in the e-mail | C, and the research of R §2.9 |

A2 may run beside B. S and C may run beside each other. D follows C.

**Not in this phase** (R §10 phase 3, R §11.1):
- price lists, customer-group prices and quantity breaks (Products phase 4);
- online acceptance of a quote through a link, and a customer portal;
- prepayment requests;
- returns;
- Peppol Order and Order Response;
- quote → project;
- goods subscriptions;
- Incoterms;
- CRM.

§13 says what each waits for, and §14 what this phase keeps for them.

## 2. Decisions — delivery 0, platform and seams

### The domain-events outbox

- **O1 — A platform package, `srv/events`, with tables in the `platform` schema.**
  `platform` already exists and holds `rate_limit`.
  - **`platform.events`** has:
    - `id` (bigint identity);
    - `name` (`orders.sales_order.confirmed`);
    - `version` (smallint);
    - `module` (the publisher);
    - `aggregate_id` (text, for ordering and lookup);
    - `payload` (jsonb);
    - `occurred_at`.
  - **`events.Publish(ctx, tx, ev)`** inserts the event on the publisher's own
    transaction. It never begins, commits or takes a pool, as a holder does not (MB
    rule 10's discipline). An event commits together with the state change that caused
    it, or not at all.
  - **Event types are DTOs in `srv/contracts/events_<module>.go`.** Each has a
    `Name()`, a `Version()`, and JSON tags that are its wire form. A breaking change is
    a new version, and handlers name the versions they accept.
- **O2 — Handlers are a many-provider slot.**
  - `module.Module.EventHandlers` is a list of `events.Handler{Name, Events, Handle}`:
    - `Name` is unique across the composition, `<module>.<what>`, and Compose refuses a
      duplicate;
    - `Events` are the event names and versions the handler takes;
    - `Handle(ctx, tx, ev) error`.
  - **`tx` is the delivery's own transaction**, and the handler writes only its own
    schema on it:
    - a handler whose effects stay in the database therefore runs **exactly once**;
    - **a handler never leaves the process.** Nothing calls `OutboundMail` or any
      other external service inside a transaction (MB rule 17).
    - A handler that must cause mail only queues it in its own table. Its module's
      own worker sends it (O25).
  - This becomes **MB rule 23**, the twelfth sanctioned cross-module write: a handler
    writes its own schema inside a platform transaction. It locks only its own rows,
    never another module's, and reads no contract while `tx` holds locks.
  - **Handlers run whatever `MODULES` says.** As with holders, a module switched off
    still has its handlers composed.
    - A handler that needs its module on checks the setting itself and does nothing.
- **O3 — Delivery and retry.**
  - **`platform.event_deliveries`** holds one row per (event, handler). The dispatcher
    writes these rows when it first claims the event, so the handler list is fixed then.
  - Each row has a `state`:
    - `pending`;
    - `done`;
    - `failed`, which is retrying;
    - `dead`.
  - Each row also has:
    - `attempts`;
    - `next_attempt_at`;
    - `lease_id` and `lease_until`, the columns and claim of
      `communications.outbox_jobs`;
    - `last_error` (≤ 2000 characters).
  - **One leased worker**, `events.dispatch`, runs every 5 seconds with a batch of 100.
    - It handles each delivery in its own transaction.
    - **Backoff** between attempts: 1 min, 5 min, 30 min, 2 h, 12 h.
    - **After 8 attempts** the delivery is `dead`. It is logged at error, and the
      operator sees it (O4).
  - **Order:** deliveries to one handler are tried in event id order per
    `aggregate_id`.
    - **A publisher publishes an aggregate's events only while it holds that
      aggregate's row lock.** Event ids then follow commit order per aggregate.
      Identity ids alone do not.
    - A later event for the same aggregate and handler waits while an earlier one is
      `failed` **or `dead`**. The timeline therefore never shows *changed* before
      *confirmed*.
    - A dead delivery is unblocked only by the operator's *retry* or *skip* (O4).
    - Other aggregates are not held up.
  - **Payloads carry ids, numbers, amounts and states, never personal data:** no names,
    addresses, e-mails or phones.
    - A handler that needs them reads its own copy, or a contract, after its
      transaction.
    - A customer's erasure then needs no sweep of `platform.events`.
  - **Retention:** events whose deliveries are all `done` are pruned after **30 days**
    by the same worker.
- **O4 — The operator view.**
  - **On the management listener**, `srv/management`, the operator's private control
    plane. It is not the tenant API:
    - `GET /events/deliveries?state=dead|failed` lists the stuck deliveries;
    - `POST /events/deliveries/{id}/retry` resets one to `pending`;
    - `POST /events/deliveries/{id}/skip` marks one `done` with a note.
  - **No permission key is needed.** `ValidatePermission` ties a key's prefix to a
    module, and no `platform` module exists. The listener's own authentication guards
    it.
  - The admin guide's operations page documents the three calls.
- **O5 — The outbox carries what may lag, never what must refuse** (R §11.1 reading 8).
  - These stay synchronous contract calls on the order's transaction:
    - stock reservation and issue (C);
    - the invoice stamp (B, MB rule 10).
  - The roadmap's sentence "Inventory reserves stock" on an event is corrected in
    ROADMAP.md when 0 lands.

### The catalog

- **O6 — `contracts.ProductCatalog` grows**, in Products' own delivery inside 0.
  - **`Money.Amount` becomes exact-decimal text**, the house form that `WorkSource.Amount`
    already uses.
  - **Two callers move:** Projects' billing-line pricing and Time's rate chain
    (`srv/time/rates.go:179-186`, which applies `discounted()` to the float).
  - **The rule from now on is "no *new* float carries money across a contract".**
    `CustomerBillingProfile.DefaultBillRate *float64` stays as it is (§13).
  - **`VariantEntry` gains `TaxCategoryID int32` and `Description`.** Every read fills
    them, `LockVariants` included.
    - It does not gain the category's kind or rate. VAT comes from Invoices' dated
      codes (O7, O8), and Products' undated rate would be a second, wrong source.
  - **`SearchVariants` gains a type filter.**
    - It becomes `SearchVariants(ctx, q, VariantSearch{Types []string, Limit int})`.
    - Inventory passes `Goods`, and Orders passes both types.
    - Only active products are answered unless the filter says otherwise.

### VAT and the seller, from Invoices

- **O7 — Invoices maps each Products tax category to one of its VAT codes.**
  - A table `invoices.tax_category_vat_codes` (`tax_category_id` → `vat_code_id`),
    edited on Invoices' VAT settings page under `invoices:settings`.
  - **A category with no mapping uses no default.** A product line in that category is
    refused at an order's confirm (`vat_code_unmapped`, naming the category) and shown
    as a warning on a draft.
  - Projects' billing lines get the same mapping for free. That is a follow-up there,
    not in this phase.
- **O8 — `contracts.SalesTax`**, a new single-provider read served by Invoices:
  - `VATCodes(ctx, at)` returns every active code with `{ID, Code, Name, RatePercent,
    Category, SAFT}` and the rate valid at `at`;
  - `ForTaxCategories(ctx, ids, at)` returns `map[categoryID]VATCodeEntry`; an unmapped
    id is absent;
  - `Seller(ctx)` returns the seller block Invoices prints: name, organisation number
    with MVA, address, e-mail, phone, bank account, logo key in storage. It is read
    from Invoices' settings as its PDF reads it.
- **O9 — Orders requires Invoices.**
  - `MODULES` dependencies become: **orders depends on products, customers and
    invoices** (`srv/config`).
  - Inventory and Communications stay optional.
  - Reason: VAT codes, the seller, and the whole of B and S live there, and every
    Norwegian product puts orders inside invoicing (R §2.4).
  - Because of this, Orders reads `SalesTax` as always present.

### The customer

- **O10 — `CustomerBillingProfile` gains:**
  - `DeliveryAddresses []CustomerAddressEntry`: the primary delivery address first, then
    the other delivery addresses, empty when the customer has none.
    - `CustomerAddressEntry` gains `ID int32`, zero on the resolved `InvoiceAddress`.
      A draft's chosen delivery address is then found again on refresh, and falls back
      to the primary one when its id has gone;
  - `Contacts []CustomerContactEntry{ID, Name, Email, Phone, Roles}`, without archived
    contacts.
  Both are read only by a consumer that asks: `BillingProfile` gains an options argument
  `{WithDelivery, WithContacts bool}`, so Invoices' existing calls pay nothing.

## 3. Decisions — delivery A, quotes and orders

### The module

- **O11 — Scaffold.**
  - Module key `orders`, schema `orders`, route `/orders`, API `/api/v1/orders`,
    contract `openapi/orders.yaml`, frontend `apps/orders/frontend`.
  - App sections:
    - **Tilbud / Quotes**;
    - **Ordre / Orders**;
    - **Abonnement / Subscriptions** (S);
    - **Leveranser / Deliveries** (C);
    - **Analyse / Analysis** (A2);
    - **Innstillinger / Settings**.
  - Every registry in AGENTS.md "Adding a module" is joined in A (§10).
- **O12 — Ids and numbers.**
  - Row ids are `bigint` identity starting at 1001, as Inventory's are.
  - **Display numbers come from Orders' own `orders.counters`**, one row per series:
    `quote`, `order`, `subscription` (S) and `delivery` (C). The series are allocated by
    upsert under the row lock, as `invoices/queries/counters.sql` does.
  - **Each series has a configurable start** (settings, ≥ 1). A start can only rise
    above the highest number used.
  - **A number is taken when the document is created**, not when it is sent.
    - A deleted draft leaves a gap.
    - That is allowed: a quote or an order is not a sales document (R §8.1). The
      reference says so.

### The document header (quote and order)

- **O13 — A quote and an order share a header shape.**
  - **The customer:**
    - `customer_id` (int32), and the **customer snapshot**: customer number, name, type,
      legal id and country, invoice address, the chosen **delivery address**, the
      chosen **contact** (name, e-mail, phone).
    - `your_reference` (*Deres referanse*, ≤ 100).
      - It defaults from the customer's `BuyerReference`.
      - It is the **one** reference that reaches the invoice's `your_reference`, which
        is EHF's `BuyerReference` (`srv/invoices/ehfdoc.go:61`). Invoices has no
        second field.
      - A user who types one on the order overrides the customer's.
  - **Our side:**
    - the **salesperson** (`salesperson_id` uuid, and a display snapshot), defaulting
      to the user who creates the document;
    - `project_id` (int32, optional). It is checked through `contracts.ProjectDirectory`
      to be the same customer's project, or internal. A mismatch is refused
      (`project_customer_mismatch`).
  - **Money:**
    - `currency`: the customer's currency, else the deployment's base currency;
    - `prices_include_vat` (O19): defaults to true for a `person` customer and false
      for a `business`;
    - `payment_terms_days`: the customer's resolved terms, else the setting's default;
    - `discount_visible`, default true (O22).
  - **Text and dates:**
    - `language` (`nb` or `en`, from the customer, for the PDF);
    - `intro_text` and `closing_text` (≤ 4000 each, for the PDF);
    - `internal_note` (≤ 4000, never printed);
    - `delivery_date` (optional; an order's date when the customer expects the goods).
  - **Origin** (O27).
- **O14 — The snapshot follows the customer while the document is a draft, and freezes
  after.**
  - Saving a draft re-reads the billing profile and refreshes the snapshot.
  - **Sending a quote, or confirming an order, freezes it.**
  - After that, only the delivery address, the contact and `your_reference` can change.
    On an order such a change is logged (O18).
  - A customer merged away is re-pointed by the holder (O29), and the snapshot is not
    touched.

### Lines

- **O15 — One line shape, `orders.lines`**, owned by a quote, an order or a template
  (S adds the subscription).
  - **What every line has:**
    - `position`;
    - `kind`: `product`, `section` or `note`. The set is open: `shipping`, `fee` and
      `rounding` join with channels, as Inventory's movement kinds do;
    - `parent_line_id`: kept null in this phase, reserved for kits and modifiers.
  - **What a product line has:**
    - `variant_id` (int32, nullable). Null means a free-text line with no catalog item;
    - snapshots of SKU, name and unit;
    - `description` (≤ 2000);
    - `quantity` `numeric(12,3)` > 0;
    - `unit_price` `numeric(18,8)` ≥ 0, net of VAT (O19), the column `invoices.lines`
      has had since `mig/00078:53`. The editor accepts four decimals. Eight are written
      only by a subscription's pro-rated period (O43);
    - `discount_percent` `numeric(5,2)` 0–100;
    - optional `packaging_id` and `packaging_quantity`, from `VariantEntry.Packagings`.
      A line entered as 3 cases of 15 stores quantity 45 and remembers the case for
      the PDF.
    - **Pricing:**
      - `price_source`: `catalog`, `manual`, `channel` or `carrier` (O53);
      - `vat_code_id` and snapshots of code, rate and category;
      - the computed amounts in Invoices' own terms (O17), each `numeric(14,2)`:
        - `line_gross` = quantity × price before discount;
        - `line_allowance` = the discount;
        - `line_net`.
      - **VAT is computed per document only**, never per line.
    - **Channel fields:** `external_line_id` (≤ 50, null). It holds a channel's own line
      id, which a Peppol Order Response must echo.
    - **Invoicing:**
      - `invoice_policy`: `order`, or `delivery` from C (O48);
      - `optional` (quotes only, O21).
  - **What an order's product line also has:** the quantity buckets
    `qty_cancelled`, `qty_delivered` and `qty_invoiced`, each `numeric(12,3)` and
    starting at 0, plus `delivery_method`, which is `manual`, or `delivery` from C.
  - **A section or note line** has only text and position.
  - **Up to 500 lines** per document (`too_many_lines`).
  - **Every write that touches catalog lines calls `ProductCatalog.LockVariants`** on
    its transaction **before** locking or writing Orders' own rows (MB rule 21). This
    covers:
    - a draft save;
    - send and confirm;
    - accept and accept-and-confirm;
    - an edit after confirm;
    - cancel and cancel remaining;
    - the create command.
    A line can therefore never commit for a variant that Products is deleting, and
    O30's answer to Products is true when given.
- **O16 — Pricing a line.**
  - Picking a variant fills:
    - the SKU, name, unit and description;
    - the price, from `ListPrice(variant, currency, today)`, as `price_source = catalog`;
    - the VAT code, from `ForTaxCategories` on the variant's category.
  - **No list price in the currency** leaves the price empty and the line unconfirmable
    (`line_unpriced`).
  - **Typing a price** sets `price_source = manual`.
  - **A free-text line** takes its VAT code from a picker, defaulting to the code mapped
    from the `standard` category.
  - **While the document is a draft**, a catalog-priced line is re-priced on save if the
    currency or the date moved. A manual price is kept (Odoo's lesson, R §2.1).
  - **Sending or confirming freezes every line's price and VAT.**
- **O17 — The arithmetic is Invoices' own, lifted into a platform package.**
  - **What moves.** `computeLine` and `summarize` (`srv/invoices/money.go:13-121`)
    move **verbatim** to `srv/docmath` in delivery 0, and Invoices' tests pass
    unchanged:
    - `line_gross = round2(quantity × price)`;
    - `line_allowance = round2(line_gross × discount ÷ 100)`;
    - `line_net = line_gross − line_allowance`;
    - rounding is **half away from zero**, on purpose, for PEPPOL-R120;
    - VAT is per (category, rate) on the sum of the nets.
  - **Where the totals match.** Invoices and Orders both call this code, so lines are
    the same number by construction.
    - **Line amounts always match.**
    - **The document total matches when one invoice covers the whole order.**
    - Partial invoicing and samlefaktura round VAT on other sums, so their VAT may
      legitimately differ by øre from the order's.
    - The order page's *Invoiced* figure is the invoices' own total.
  - **The test** checks line equality always, and document equality for one full
    invoice per order.

### The order's life

- **O18 — States.**
  - **The lifecycle** is `status`: `draft`, then `confirmed`; or `cancelled`.
  - **Two flags sit beside it:**
    - `on_hold`, with a reason of up to 200 characters;
    - `locked`.
  - **Two derived values** are stored for lists and filters, and recomputed in every
    transaction that changes a bucket:
    - **delivery status**:
      - `none`;
      - `partial`;
      - `delivered`, when every product line's delivered + cancelled ≥ ordered;
    - **invoice status**: `none`, `partial` or `invoiced`, in the same way.
  - **An order is *done*** when it is confirmed, delivered and invoiced. *Done* is shown
    as a tag, never stored as a state (Odoo's v17 lesson, R §2.8).
  - **Confirm** (`orders:sales-confirm`) refuses:
    - `customer_disabled`: the customer is disabled or archived;
    - `order_empty`: there are no product lines;
    - `line_unpriced`;
    - `vat_code_unmapped` (O7);
    - in C, `insufficient_available` (O45).
  - **Confirm then:**
    - freezes the snapshot;
    - stores the confirmation PDF (O24);
    - publishes `orders.sales_order.confirmed.v1`.
  - **Cancel** (`orders:sales-manage`):
    - is allowed while nothing is delivered or invoiced;
    - after that it is refused (`order_has_progress`) and **Cancel remaining** is
      offered instead. That sets each line's `qty_cancelled` to
      ordered − max(delivered, invoiced), and releases any reservations in C;
    - publishes `…cancelled.v1`.
  - **Hold** and **release** (`orders:sales-manage`):
    - a held order offers nothing to invoice (B) and takes no delivery (C);
    - edits are still allowed.
  - **Lock** and **unlock** (`orders:sales-confirm`):
    - a locked order refuses every edit (`order_locked`);
    - it still invoices and delivers.
- **O19 — Prices are stored net. "Including VAT" is an entry and display mode.**
  - **Invoices prices net** (`invoices.lines.unit_price`; it has no gross mode).
  - When `prices_include_vat` is true:
    - the line editor takes a gross unit price and stores
      net = gross ÷ (1 + rate), rounded half-up to four decimals;
    - the PDF and the screen show the gross unit price
      (net × (1 + rate), rounded to øre) and gross line totals.
  - **The difference this can leave:** the shown gross may differ by at most one øre
    per line from what the customer typed. It never differs from the invoice, which
    uses the same net.
  - **When the VAT rate changes,** the net was derived at the old rate, so an agreed
    price including VAT moves with the new rate. The reference says so.
  - **Words:** "including VAT" is called *inkl. mva* everywhere. "Gross" is not used for
    it, because Invoices' `line_gross` means the amount before discount.
  - A true gross-based rounding mode, which a webshop selling to consumers may need, is
    an Invoices change listed in §13.
- **O20 — A quote's life.**
  - **The states are:**
    - `draft`;
    - `sent`;
    - from `sent`: `accepted`, `declined` (with an optional reason) or `expired`;
    - `cancelled`, from `draft` or `sent`.
  - **Sent.** *Send* (by e-mail, O25) or *Mark as sent* (for one handed over by other
    means) moves a draft to `sent`. Either way it stores the PDF of that revision,
    freezes the quote, and publishes `orders.quote.sent.v1` (O31's *Quote sent*).
  - **Revisions.**
    - *Revise* moves a sent quote back to `draft` with `revision + 1`, so the next send
      is revision 2.
    - The PDFs of earlier revisions are kept and listed.
    - The number stays the same, and the PDF prints "Tilbud 1042 – revisjon 2".
  - **Valid until.** `valid_until` is required on send. It defaults to today plus the
    setting's validity (30 days) or the template's.
    - The daily worker `orders.quote_expiry` moves a `sent` quote whose `valid_until`
      has passed to `expired`.
    - *Accept* on an expired quote is still allowed, with a confirmation step in the UI.
      It is common to honour a quote a few days late.
  - **Accept** (`orders:quotes-manage`) chooses:
    - which **optional lines** to include;
    - what to create:
      - **a draft order** (the default);
      - **a confirmed order** (*Accept and confirm*, which also needs
        `orders:sales-confirm`);
      - from S, **a subscription**.
  - **What *Accept* does.** It copies the header and the chosen lines with their frozen
    prices, sets the new document's origin to `channel = quote` and
    `source = quote id @ revision`, and publishes `orders.quote.accepted.v1`.
  - **A quote is accepted once.** *Copy* makes a new draft quote from any quote.
- **O21 — Optional lines.**
  - A quote's product line may be `optional`. It is printed in its own block, "Valgfritt
    / Optional", and **left out of the quote's totals**.
  - At accept the user ticks the optional lines the customer took. They become ordinary
    lines on the order, and the rest are dropped.
  - Orders and subscriptions have no optional lines (`optional_not_allowed`).
- **O22 — Discount visibility.**
  - With `discount_visible` true, the PDF shows the list price, the discount % and the
    net line amount.
  - With it false, the PDF shows only the net unit price, which is
    price × (1 − discount), rounded to øre, and the net line amount.
  - The screen always shows both.
- **O23 — Quote templates.**
  - **What a template holds** (`orders.templates`, `orders:settings` to edit):
    - name;
    - intro and closing text;
    - validity days;
    - `discount_visible`;
    - lines: product lines with variant, quantity, optional flag and an optional
      price override; section lines; note lines.
  - **Applying a template** to a draft quote:
    - appends its lines, or replaces the quote's lines if the user chooses so;
    - fills empty texts;
    - prices the lines as O16 does, in the quote's currency.
  - **Variants that went away** (archived or deleted) are skipped, and the answer names
    them.
- **O24 — PDFs.**
  - **Three documents, printed with maroto v2 as `invoices/pdf.go` does:**
    - the quote;
    - the order confirmation (*ordrebekreftelse*);
    - **pro forma** (*proforma*), a print variant of either one, titled
      "Proforma – ikke en faktura", that is never stored and never sent as the
      document itself.
  - **What the PDF prints:**
    - the seller block from `SalesTax.Seller`;
    - the customer snapshot;
    - the references;
    - the lines with sections, notes, the optional block and the discount mode;
    - VAT per rate;
    - the totals;
    - the payment terms;
    - for a quote, valid until;
    - the texts.
  - It is printed in the document's language.
  - **Storage.**
    - Each sent quote revision and each sent or confirmed order version is stored once,
      through the storage port, as `invoices/pdfstore.go` stores invoices.
    - A stored PDF is immutable. Retention is O54.
  - Orders embeds its own NotoSans fonts, as Inventory does.
- **O25 — Sending.**
  - **Through `contracts.OutboundMail`**, on the channel chosen in Orders' settings
    (`send_channel`, MB rule 17).
  - **Each send is written to `orders.mailings`:** document, revision or version,
    recipient, channel, status (`queued`, `sent`, `failed`), and provider message id.
    This copies `invoices.deliveries`.
  - **A send from the dialog** is synchronous, outside any transaction, and written to
    the log afterwards, as Invoices' send is.
  - **An automatic send is queued.**
    - The handler or the booking (O52) inserts a `queued` row on its own transaction,
      unique per `(document, version, trigger_event_id)`.
    - A leased worker, `orders.mail`, sends queued rows with backoff and marks them
      `sent` or `failed`.
  - **Recipient:** the contact's e-mail, else the customer's invoice e-mail. It is
    editable in the send dialog, which also takes CC and a message.
  - **The mail text** is per document kind and language, from `orders/mailtext.go`. It
    has a settings override for the subject and the body.
  - **Communications off** means no *Send*: only *Download* and *Mark as sent*.
  - **The setting `confirmation_on_confirm`** (default off) sends the order confirmation
    automatically.
    - Orders' own handler, `orders.queue_confirmation`, receives
      `orders.sales_order.confirmed.v1` and only queues the mailing. It stays inside
      the database and runs exactly once (O2).
    - The `orders.mail` worker sends it.

### Editing after confirmation

- **O26 — A confirmed order stays editable in its open quantity** (R §5.3).
  - **Allowed while it is not locked:**
    - add a line;
    - change a line's quantity, down to max(delivered, invoiced) + its cancelled
      quantity;
    - change price, discount or description on a line with nothing invoiced;
    - remove a line with no progress;
    - change the delivery date, the delivery address, the contact or
      `your_reference`.
  - **Draft allocations count as progress (B).** A quantity may not go below
    invoiced + draft-allocated, and the price may not change while any draft
    allocation exists (`line_in_draft_invoice`).
  - **Refused:**
    - `quantity_below_progress`;
    - `line_invoiced`: a price change on a line with anything invoiced;
    - `line_has_progress`: removing a line;
    - `order_locked`.
  - **Every save** of a confirmed order:
    - takes the row with the client's `version` (optimistic; `version_conflict` on a
      stale one);
    - writes one `orders.order_changes` row: version, actor, time, and a typed list of
      actions (`line_added`, `line_removed`, `quantity_changed`, `price_changed`,
      `discount_changed`, `text_changed`, `delivery_changed`, `contact_changed`,
      `reference_changed`, `cancelled_remaining`, `held`, `released`, `locked`,
      `unlocked`);
    - bumps `version`;
    - publishes `orders.sales_order.changed.v1`.
  - **The order page** shows the change log. *Send confirmation* afterwards prints
    "Revidert ordrebekreftelse" with the version.

### Origin, idempotency and the create command

- **O27 — Every quote, order and subscription carries its origin** (R §4.1):
  - `channel`, an open set of application constants: `manual`, `quote`, `copy`, and
    later `peppol`, `webshop`, `pos`, `import`;
  - `source_module`, `source_kind`, `source_id`, as Inventory's movements have them;
  - `external_id` (≤ 100) and `external_number` (≤ 50), with a unique index on
    `(channel, external_id) WHERE external_id IS NOT NULL`;
  - `client_request_id` (uuid). A create that repeats one answers the first create's
    document with 200, not 201, as Inventory's postings do;
  - `payment_reference` (≤ 100, null) and `prepaid_amount` (exact, null). These are
    reserved for a channel whose order arrives paid, such as a webshop. Invoices then
    settles the invoice drafted from it. Nothing writes them in this phase.
- **O28 — One create command inside Orders.**
  - `createOrder(ctx, CreateOrder{Origin, Customer, Header, Lines})` is the only path
    that writes a new order. The API, *Accept* and *Copy* all use it.
  - **It is not published as a contract in this phase.** A contract without a consumer
    is speculative. It becomes `contracts.SalesOrders.Create`, unchanged in shape, with
    its first channel (§14).
  - A channel may hand in priced lines with `price_source = channel`. The command keeps
    their prices and computes only the VAT and the amounts.

### The other modules' holders, and the timeline

- **O29 — The customer holders.**
  - **`CustomerReferenceHolder`** (MB rule 8): a merge re-points `customer_id` on
    quotes, orders and subscriptions. The snapshot is left as the customer was when the
    document froze.
  - **`CustomerPersonalData`** (MB rule 9):
    - **Export** lists the person's quotes, orders and subscriptions with their lines.
    - **Erase** anonymises the person's snapshot (name, addresses, contact) on every
      quote and draft order at once. It also blanks the recipient on their
      `orders.mailings` rows. On a confirmed order, a subscription or a delivery, it
      does so **once the retention of O54 has run**.
    - Until then the erase answer reports the order numbers it kept and why. This is
      Invoices' rule for issued invoices; the plan copies its wording.
  - **A user's erasure** (MB rule 19) keeps the salesperson's and the actors' id and
    display name on documents and change logs. These are records of an agreement, which
    is Inventory's default for its ledger.
- **O30 — `VariantReferenceHolder`** (MB rule 20). Deleting a variant is refused while
  any of these names it:
  - a draft or sent quote;
  - an order line not done;
  - an active subscription line.
  - **The contract grows for this.**
    - `VariantHold` (`srv/contracts/references.go:83`) gains `Kind` (`stock`,
      `open_documents`) and `Documents int`.
    - Products' refusal text names the kind: "Variant has stock" or "Variant is on N
      open quotes or orders".
    - Products also passes a `purpose` (`delete` or `type_change`) to the holder.
      Orders answers a hold for `delete` only, so an open order never blocks changing
      a product from Goods to Service.
  - History keeps the SKU snapshot.
- **O54 — Retention.**
  - **What is kept:** a confirmed order, a sent quote revision, a posted delivery, a
    subscription, their stored PDFs and their change logs. Under bokføringsloven § 13
    (R §8.1) they are kept **3 years and 6 months after the end of the financial year**
    in which:
    - the order was done or cancelled;
    - the quote was answered or expired;
    - the subscription ended.
  - **What may be deleted at once:** a draft, or a quote never sent.
  - **The sweep:** a daily worker, `orders.retention`, anonymises the snapshots of an
    erased person's documents as their retention runs out. It works from the erasure
    record O29 writes. It deletes nothing else.
- **O31 — Customers writes the timeline.**
  - A Customers handler, `customers.timeline_orders`, writes *Quote sent*, *Quote
    accepted*, *Order confirmed* and *Order cancelled* to the customer's timeline from
    the events.
  - This is the first entry of the roadmap's "other modules writing to the timeline",
    which waited for the outbox.
  - The entry links to the document by its route (`/orders/quotes/{id}`,
    `/orders/orders/{id}`).

### The customer page's Orders tab

- **O31a — An *Ordre / Orders* tab on the customer page** (owner, 2026-11-05).
  - **It joins the page the way Energy, Projects and Invoices do:**
    - a child route `/customers/$customerId/orders` in the host
      (`$customerId.orders.tsx` with `-customer-orders-tab.tsx`);
    - an entry in `customerDetailTabs`
      (`apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`), gated on
      the `orders` module and `orders:access`;
    - `activeCustomerDetailTab` and `customer.ordersTab` in the host's catalogs, in
      `en` and `nb`.
  - **The tab renders `CustomerOrdersPanel`** from `@vantigo/orders-ui`.
    - It gates on the module itself, as `-customer-invoices-tab.tsx` does, so a pasted
      link never hits an API 404.
  - **The panel shows three lists for the customer:**
    - **open quotes**: draft and sent, with valid-until and value;
    - **orders**: status, delivery and invoice status, value, the *done* tag, and a
      filter for open or all;
    - **subscriptions** (from S): status, interval, billed through, value per period.
    - Each row links to the document in the Orders app.
  - **Above the lists, a summary:**
    - the value of open quotes;
    - the value ordered and not delivered;
    - the value delivered and not invoiced (from B).
  - **The actions are *New quote* and *New order*.** They open the Orders app's editor
    with the customer filled in.
    - Each needs its permission (`orders:quotes-manage`, `orders:sales-manage`).
    - They need an active customer, neither merged away nor anonymised, the rule the
      Invoices tab already applies (`isReadOnlyCustomer`).
  - **What the server provides:**
    - the list endpoints take `customerId`;
    - one endpoint, `GET /api/v1/orders/customers/{customerId}/summary`, answers the
      summary with counts and sums per currency, under `orders:access`.
  - **The customer 360 overview panel is not in this phase** (§13).
    - The panel is one Customers endpoint composed from contracts (ROADMAP Customers
      phase 5), so an orders tile would need a new provider read.

### Manual delivery marking

- **O32 — Until C, and for services and non-stock lines after it, delivery is marked by
  hand.**
  - *Register delivered* on a confirmed order takes per-line quantities, defaulting to
    the remaining quantity.
  - It adds them to `qty_delivered`, refuses beyond ordered − cancelled
    (`over_delivery`), and logs `delivered_marked` in the change log.
  - *Undo* subtracts, never below 0. Undo is refused below `qty_invoiced` when the
    line's policy is `delivery`.
  - Both need `orders:sales-manage`.
  - **In A and B every line's policy is `order`**, so marking delivery only feeds the
    delivery status and the *done* tag.

## 4. Decisions — delivery A2, selling comfort

- **O33 — Sales warnings.**
  - **Where a warning is set:**
    - on a customer, in Orders' own table `orders.customer_warnings` keyed by
      customer id;
    - on a product, in `orders.product_warnings` keyed by product id.
    - Orders keeps them itself. A warning is a selling rule, and neither Customers nor
      Products needs it.
  - **What a warning has:**
    - a level, `info` or `block`;
    - a text of up to 500 characters.
  - **What happens:**
    - an `info` warning is shown when the customer or product is put on a quote or
      order;
    - a `block` warning also refuses send and confirm (`blocked_by_warning`, with the
      text);
    - a holder of `orders:settings` may override a block with a reason, which the
      change log keeps.
  - **Who edits them:** `orders:settings`.
  - The holders erase a customer's warning on erase and re-point it on merge.
- **O34 — Variant-grid entry.**
  - For a product with variants on two options (for example size and colour),
    *Add from grid* shows a matrix with the products' variants along the two options,
    built from `VariantsOfProduct` and `option_values`.
  - A quantity typed in a cell adds or updates that variant's line.
  - A product with more than two options shows a list instead. A grid larger than
    30 × 30 also falls back to a list.
- **O35 — The analysis views** (`orders:analysis-view`), read from Orders' own tables
  with no warehouse:
  - **Quote hit rate:** quotes sent in a period, and how many of them were accepted,
    declined, expired or are open. Counted and summed by value, per salesperson.
  - **Order intake:** confirmed orders' net value per month, by salesperson, customer,
    product and product category (the category is read from the catalog for the
    period's variants).
  - **Backlog:**
    - net value ordered and not delivered;
    - net value delivered and not invoiced, with age buckets (0–7, 8–30, over 30 days),
      as bokføringsforskriften § 5-2-2 asks an invoice within a month (R §8.1).
  - Each view is a table with a CSV export. Charts follow the house dataviz rules, one
    per view.

## 5. Decisions — delivery B, invoicing from orders

- **O36 — Allocations, Energy's settlement pattern.**
  - **Why a line cannot be the source.**
    - Invoices holds one live source per `(source_kind, source_id)`
      (`ux_line_sources_live`, `mig/00040`).
    - An order line may be invoiced in several parts.
    - So the source is an **allocation**: `orders.invoice_allocations` (line, quantity,
      amount, state).
    - This is how Energy makes a settlement per billing period (`ConsumptionProvider.Settle`).
  - **States:** `draft`, `invoiced`, `released`.
  - **To invoice** = ordered − cancelled − invoiced − draft-allocated (*on order*), or
    delivered − invoiced − draft-allocated (*on delivery*, C).
    - A quantity in a draft allocation is therefore not offered twice.
  - **Each `Allocate` writes new rows.** It never rewrites an existing draft allocation,
    so a line may sit on several drafts at once, each with its own allocation and
    Invoices' unique live source.
    - `Allocate` takes the order rows, then the lines, `FOR UPDATE` in id order, after
      `LockVariants`.
    - Two concurrent `Allocate` calls therefore serialise, and never both see the same
      open quantity.
  - **Releasing a draft's allocations.**
    - `BillableOrders.ReleaseAllocations(ids)` turns draft allocations `released`.
    - Invoices calls it **after** a draft invoice's delete, or a line's removal from a
      draft, commits. It is best effort, logged on failure, and outside the transaction,
      as every contract call is.
  - **Two backstops for a release that never arrives:**
    - **A sweep.** A draft allocation untouched for **30 days** is released by the daily
      `orders.allocation_sweep`. This is Energy's horizon
      (`srv/energy/consumption_provider.go:24-27`).
      - If its draft still exists, issuing it later gets `source_changed`, and the user
        redrafts.
    - **The order page's *Release draft allocations*** (`orders:sales-manage`). It
      releases a line's draft allocations at once, with a warning that any draft invoice
      holding them will refuse to issue.
  - **Cancel** is refused while draft allocations exist (`order_in_draft_invoice`). The
    refusal offers that action. It does not name the draft, because Orders cannot know
    the draft's id.
- **O37 — `contracts.BillableOrders`**, a new single-provider slot served by Orders.
  - **`DueOrders(ctx, req{CustomerIDs, OrderIDs, Every, Until})`** lists, per order,
    what can be invoiced now:
    - the order (id, number, customer, currency, project, references, delivery date,
      on-hold reason);
    - its lines with to-invoice quantity > 0.
    - Held and draft orders are listed with a `Blocked` reason, as Energy's
      `DueSupply.Blocked` is, so the wizard can say why.
  - **`Allocate(ctx, req{Lines []{LineID, Quantity}})`** writes or recomputes draft
    allocations and answers them. Each allocation carries:
    - id and revision;
    - order and line ids;
    - quantity;
    - the line's description, unit, unit price and discount;
    - VAT code id;
    - the exact amount;
    - the order's references.
    - A quantity above the to-invoice quantity is refused (`allocation_exceeds_open`).
  - **`Allocations(ctx, ids)`** answers allocations as they stand.
  - **`ReleaseAllocations(ctx, ids)`** (O36).
  - **Page limits** are `MaxBillableRows`, as the other billable reads use.
  - **Two write commands on a read contract.** `Allocate` and `ReleaseAllocations` are
    the second and third such commands, after Energy's `Settle`. The comment in
    `contracts/consumption.go` calls `Settle` "the one method of any contract that
    does". That comment and MB rule 5's text are updated to name the pattern: a
    provider read may carry commands that write only the provider's own schema, on
    the provider's own transaction.
  - **The amount on an allocation** is `docmath`'s line net for its quantity, unit
    price and discount. Invoices builds its line from the same three numbers, so the
    two always agree.
    - A pro-rated subscription period (O43) is priced as its quantity × a unit price at
      eight decimals, the pro-rated amount ÷ quantity.
    - `docmath` then reproduces the pro-rated amount to the øre.
    - No "amount wins" path is needed.
- **O38 — The source kind and the holder.**
  - `contracts.WorkSourceOrderAllocation = "orders.allocation"`. It is appended **last**
    to `InvoicedWorkOrder`, after `energy.settlement`.
    - An invoice may mix a project's hours and an order's lines, so the place is fixed.
  - **Orders implements `InvoicedWorkHolder`:**
    - **`MarkInvoiced`** locks the order rows, then the lines, then the allocations, in
      id order. It judges each allocation in the order the contract sets:
      - already invoiced: `source_already_invoiced`;
      - the order is cancelled or on hold, or the quantity is no longer open:
        `source_not_invoiceable`;
      - the allocation's revision or exact amount moved: `source_changed`.
      It then stamps the allocation `invoiced` with the invoice's id, number and date,
      adds its quantity to the line's `qty_invoiced`, recomputes the invoice status,
      and publishes `orders.sales_order.invoiced.v1` with `occurred_at = ref.IssuedAt`.
    - **`ReleaseInvoiced`** reverses this on a full credit. It tolerates a missing
      stamp, as the contract requires.
  - **A holder that publishes.** MB rule 10 is amended: a holder may `events.Publish`
    on the issue's transaction. That is a write to the platform's outbox, not to
    another module, and it takes the issue's clock.
  - **When a credit releases quantity.**
    - Invoices calls the holder when a credit returns the source's line **in full**,
      including on the last of several partial credits that together return it
      (`srv/invoices/credits.go:645-646`). The quantity is then released.
    - A credit that returns part of a line, or the line at a lower price, releases
      nothing. The order keeps that quantity as invoiced.
    - The reference states both cases. Returns (§13) handle the rest.
  - **Projects off.** If Invoices' issue refuses a source whose `ProjectID` is set while
    Projects is off, an allocation carries a project only when Projects is enabled. The
    plan checks `source_not_selectable` / `projects_unavailable` in Invoices' issue.
- **O39 — Invoices' *From orders* wizard** (Invoices' delivery inside B,
  `invoices:create`).
  - **Started from:**
    - Invoices' *New invoice → From orders*;
    - an order's *Invoice* button, which opens the wizard with that order chosen.
  - **The steps:**
    1. pick customers or orders;
    2. see each order's lines with to-invoice quantities, editable down for
       *delfakturering*, and a *Remaining after* column;
    3. choose **one invoice per order** or **combined per customer** (*samlefaktura*;
       the default comes from Orders' setting `combine_by_default`);
    4. create the drafts.
  - **Grouping for samlefaktura.** The orders of one customer are combined when they
    share currency, `your_reference`, project and payment terms.
    Otherwise they make separate drafts, and the wizard says why.
  - **What the draft carries:**
    - **one invoice line per allocation**, with the order line's description, quantity,
      unit, unit price, discount and VAT code;
    - **a section text per order** when several are combined ("Ordre 2210, Deres ref.
      …");
    - **the header:**
      - `order_reference` is the order number when the invoice holds one order;
      - with several, it is empty and the section texts carry the numbers. EHF's
        `cac:OrderReference` is one value (R §2.4);
      - `your_reference` from the orders. It is the same on every order, by the
        grouping;
      - the delivery date: the latest delivery date or marking date of the lines;
      - for subscription periods, the header's `delivery_from` and `delivery_to` span
        the earliest start to the latest end of the periods on the invoice. Each line's
        text carries its own period;
      - the delivery address from the order;
      - the project.
- **O40 — The billing run takes orders.** Invoices' billing run (`billing_worker.go`)
  gains an *orders* source: every customer with due order lines through a date, combined
  by the setting, as drafts or issued, as the run already offers for energy.
- **O41 — The order shows its invoices.** The order page lists the invoices issued
  from it, through Orders' own allocations (number, date, amount, state), linking to
  Invoices' routes. No contract read back is needed.

## 6. Decisions — delivery S, subscriptions

- **O42 — A subscription (*abonnement*) is its own aggregate inside Orders**
  (R §11.2 (b)). In this phase it is for **services only** (R §11.1 S3).
  - **What it has:**
    - the header shape of O13, without `delivery_date` and with no delivery address
      required;
    - a number from the `subscription` series.
  - **Its plan:**
    - `interval`: `monthly`, `quarterly`, `half_yearly` or `yearly`;
    - `billing`: `in_advance` or `in_arrears`;
    - `start_date`;
    - `end_date` (optional);
    - `notice_months` (0–12; informational, printed);
    - `invoice_day`: the period's first day, or the setting's day of month.
  - **Its lines:** product lines whose variant must be a `Service` product
    (`subscription_goods_not_allowed`), or free-text lines. Sections and notes are
    allowed.
  - **Its status:** `draft`, `active`, `paused`, `ended`, `cancelled`.
  - **`billed_through`** (date), derived as O43 says, never written by hand.
  - **How one is created:** by hand, or by accepting a quote as a subscription (O20).
    Activating needs `orders:subscriptions-manage`.
- **O43 — Periods and billing.**
  - **The periods** are computed from the start date and the interval.
    - The first period is **partial** when the start is not the first of a month. Its
      amount is pro-rated by days, as a fraction of the period, to øre.
    - That is the only pro-rating in this phase.
  - **A period is due:**
    - when its first day is on or before the run date, for `in_advance`;
    - when its last day is, for `in_arrears`.
    - Advance invoicing is allowed for subscriptions (bokføringsforskriften § 5-2-6),
      up to a year ahead. The run refuses to bill a period that starts more than
      12 months ahead.
  - **Due periods reach Invoices through the same seam as orders:**
    - `BillableOrders.DueOrders` also lists active subscriptions with due periods;
    - `Allocate` takes `{SubscriptionID, Through}` and writes one draft allocation per
      due period and line, with `kind = subscription_period` and the period's dates.
    - The source kind stays `orders.allocation`, and the holder stamps the period.
      Invoices' wizard and billing run show them as "Abonnement 3001 – januar 2027".
  - **The invoice line carries the period** as its text, and the header carries the
    invoice period (`delivery period`), as Energy's lines do.
  - **The per-period allocation is the truth.**
    - A unique index on `(subscription_line_id, period_start) WHERE state IN ('draft',
      'invoiced')` makes billing one period twice impossible.
    - A period is due only when it has no live allocation and is not skipped (O44).
    - So crediting January in full makes January due again and nothing else. February
      stays invoiced.
  - **`billed_through`** is derived: the end of the last period in the unbroken run,
    from the start, of periods that are invoiced or skipped. It is stored for lists and
    recomputed by the holder.
- **O44 — Changes take effect from the next unbilled period** (R §11.1).
  - **Editing a line** (price, quantity, add, remove) of an active subscription:
    - asks for an *effective from* date;
    - is refused for any period that has a live allocation
      (`change_in_billed_period`);
    - takes the next period start as the default.
    - Lines are dated: `valid_from` and `valid_to`, so a period is billed with the
      lines valid on its first day.
    - There is no proration of mid-period changes.
  - **Pause** stops billing from the next period. **Resume** restarts it from a chosen
    period start.
    - The periods in between are written to `orders.subscription_skips`
      (`subscription_id`, `period_start`) and never billed.
  - **End** sets `end_date`. It must be after the last period with a live allocation,
    or the user is asked to credit first.
  - **Each change** is logged in `orders.order_changes`, which also serves
    subscriptions. Events: `orders.subscription.started.v1`, `…changed.v1`, `…ended.v1`.

## 7. Decisions — delivery C, deliveries and stock

- **O45 — `contracts.Stock` and `contracts.StockLedger`**, served by Inventory and
  named in Inventory's spec §11 and §12.
  - **`Stock`** is a read: `OnHand(ctx, variantIDs, siteID?)` and
    `Available(ctx, variantIDs, siteID?)`, in total and per site.
    - Available = on hand − reserved.
    - The product page and the order line show both.
  - **`StockLedger`** writes inside the caller's transaction, with the calls:
    - `Reserve(tx, src, siteID, lines)`;
    - `Release(tx, src, lines)`;
    - `Issue(tx, src, movement)`, which consumes reservations;
    - `ReservationsFor(tx, src)`.
  - **This is MB rule 24**, the thirteenth sanctioned write.
    - The lock order is Products' `LockVariants`, then the caller's rows, then
      Inventory's balances and reservations.
    - Every call carries the source reference `{module, kind, id, lineID}` that
      Inventory's movements already hold.
    - It refuses only through a typed `StockRefusal`. Anything else is a failure. The
      refusal codes are:
      - `insufficient_available`;
      - `insufficient_stock`, Inventory's existing code, on an issue;
      - `site_not_fulfilling`;
      - `variant_not_stocked`.
  - **Composed whatever `MODULES` says.** As holders are, Inventory's `StockLedger` is
    composed even when Inventory is off. A reservation is therefore always released
    by cancel, by an edit or by manual marking.
    - Manual delivery marking (O32) of a reserved line releases the marked quantity's
      reservation.
  - **Inventory's own decreases ignore reservations.**
    - Adjustments, moves and counts are corrections of fact, so they may take on hand
      below reserved.
    - Available then shows negative, with a warning on the product page and the order
      line.
    - A delivery's issue still refuses beyond on hand.
  - **Inventory adds:**
    - `inventory.reservations` (variant, site, quantity, source), per site as its §12.6
      kept;
    - the movement kind `issue`;
    - the site flag `fulfils_orders` (default true for a warehouse, false for a store).
- **O46 — Reserving at confirm, synchronously** (R §11.1 reading 8).
  - When Inventory is on and an order has a `site_id`, confirm reserves each goods line's
    open quantity at the site.
    - A shortfall is refused with `insufficient_available` per line.
    - The user may *Confirm without reserving*, logged.
  - **Lines without a reservation:** the user can *Reserve now*.
  - **An edit** that changes an open quantity adjusts the reservation in the same
    transaction.
  - **Cancel and cancel remaining** release reservations.
  - **A delivery** issues stock against them.
  - **`site_id` defaults** to the setting's default site.
  - **An order without a site** reserves nothing, and its goods lines are delivered by
    hand (O32).
- **O47 — The delivery document (*leveranse*).**
  - **What it has:**
    - a number from the `delivery` series;
    - the order;
    - the site;
    - lines (order line, quantity, from-location chosen at picking);
    - status `draft`, then `posted`, or `cancelled`.
  - **Creating one:**
    - *Create delivery* on an order proposes every open, available goods line;
    - it may cover part of the order;
    - several drafts may exist.
  - **The printouts:**
    - **Pick list** (*plukkliste*): lines sorted by location full code, with the
      locations holding stock;
    - **Packing slip** (*pakkseddel*): lines and quantities with the customer's delivery
      address. It is stored when posted.
  - **Posting** (`orders:deliveries-manage`):
    - calls `StockLedger.Issue` with the delivery as source, one line per (variant,
      from-location);
    - adds to the order lines' `qty_delivered`;
    - publishes `orders.delivery.posted.v1`;
    - refuses stock Inventory does not have (`insufficient_stock`, from Inventory).
  - **What is not delivered** stays open on the order as a **back order** (*restordre*).
    The order list's *To deliver* filter shows it. No separate back-order document is
    made (R §5.4).
  - **A posted delivery is immutable.** A mistake is corrected by a return (§13), or by
    an Inventory adjustment and *Undo delivered* on the line.
- **O48 — The *on delivery* invoice policy.**
  - From C, a goods line on an order with a site defaults to `invoice_policy = delivery`.
    Its to-invoice quantity is delivered − invoiced (O36).
  - Services stay `order`.
  - The policy may be changed per line while nothing is invoiced.

## 7a. Decisions — delivery D, shipping (owner, 2026-11-05)

Shipping lives **inside Orders** because every shipment in this phase ships a delivery.
It becomes its own app only if shipping grows beyond orders. These would mark that
growth (§13):
- shipments that come from no order;
- pickup planning;
- checking freight invoices against booked costs.

The carrier details below rest on the research in R §2.9. Where that research
corrects a name or a limit, R wins and this section is revised.

- **O49 — The platform port `srv/shipping`, shaped like `srv/payments`.**
  - **What the package is:**
    - interfaces and values only, with no SQL;
    - it imports no module. The consumer keeps its own tables (MB rule 4);
    - an adapter lives beside it (`shipping/bring`), and its fake beside the adapter
      (`shipping/bring/bringtest`), for tests only.
  - **One interface, `Carrier`, one value per carrier account:**
    - `Rates(ctx, RateRequest) ([]Rate, error)`: per service, the price, and the
      delivery time when the carrier gives one. The price is the account's agreement
      price when the carrier returns it, else the list price, and says which.
    - `Book(ctx, BookRequest) (Booking, error)`: sender, recipient, service,
      value-added services (SMS or e-mail notice), packages, references and the
      caller's **idempotency key**. It answers the consignment number, a tracking
      number per package, the booked cost when given, and the documents available.
    - `Label(ctx, booking, format) (Document, error)`: the label, or the waybill
      (*fraktbrev*), as PDF, or ZPL where the carrier offers it.
    - `Track(ctx, trackingNumbers) ([]TrackingStatus, error)`: events folded onto
      `booked`, `in_transit`, `ready_for_pickup`, `delivered`, `returned`,
      `exception`, with the carrier's own code and text kept.
    - `Cancel(ctx, booking, idempotencyKey) error`: "already cancelled" counts as
      success. A booking past the carrier's cancel window answers `ErrTooLate`.
    - `Services(ctx)`: the services this account may book, for the settings page and
      the picker.
    - `PickupPoints(ctx, postalCode, country)`: for services delivered to a pickup
      point.
    - `Verify(ctx)`: the cheapest authenticated call, for the settings page.
  - **Money crosses in minor units.** Weights cross in grams and dimensions in
    centimetres. Adapters convert.
  - **The adapter never retries on its own and never invents an idempotency key**, the
    payments port's rule.
- **O50 — Shipments on a delivery.**
  - **What a shipment has:**
    - the delivery (O47) and the carrier account;
    - the service, and value-added services;
    - the sender (the account's address, else the site's) and the recipient (the
      delivery address snapshot, with the contact's phone and e-mail for notices);
    - packages, each with weight (g), length, width, height (cm), an optional content
      text, and its own tracking number once booked;
    - the consignment's tracking number and tracking URL;
    - **the booked cost** (exact decimal and currency) and whether it is an
      agreement price;
    - the stored label and waybill (O52);
    - a `status`.
  - **Statuses:**
    - `draft`;
    - `booked`;
    - `in_transit`;
    - `delivered`;
    - `cancelled`;
    - plus `exception`, a flag from tracking that the delivery page shows.
  - **How a shipment is made:**
    - A posted delivery may carry **one or more shipments**.
    - *Ship* on a posted delivery proposes one shipment with one package, its weight
      summed from the variants' weights (Products stores them).
    - The user adjusts the packages and the service, sees the carrier's price
      (`Rates`), and books.
  - **Booking** (`orders:deliveries-manage`):
    - calls `Book` with an idempotency key stored on the shipment before the call, so a
      retried request books once;
    - stores the answer, fetches and stores the label and the waybill, and moves the
      shipment to `booked`;
    - publishes `orders.shipment.booked.v1`;
    - a carrier refusal is shown with the carrier's message
      (`carrier_refused`, 422), and the shipment stays `draft`.
  - **Cancel** calls `Cancel`. `ErrTooLate` is refused with `shipment_past_cancel`.
  - **Tracking:**
    - a leased worker, `orders.shipment_tracking`, polls `Track` every 30 minutes
      for shipments in `booked`, `in_transit` or `exception` from the last 60 days;
    - it stores new events in `orders.shipment_events`, moves the status, and
      publishes `orders.shipment.delivered.v1` on delivery;
    - **webhooks** are used instead where the carrier offers them and R §2.9 shows
      they are reliable. Polling stays the fallback.
  - **A shipment's booked cost is recorded, not invoiced.** It is the business's cost.
    What the customer pays is the `shipping` line (O53).
- **O51 — Carrier accounts under Integrations.**
  - **Where:** a new item in Workspace admin's *Integrations* section
    (integrations design T2), *Frakt / Shipping* at `/workspace/shipping`, gated on
    the `orders` module and `orders:carriers-manage`.
  - **An account has:**
    - the carrier (`bring` in this phase);
    - a name;
    - the carrier's customer number;
    - the API credentials for Bring: the Mybring user id and API key;
    - a **test flag**, which uses the carrier's test mode;
    - an optional sender address;
    - a default service;
    - `active` or `archived`.
  - **Credentials are workspace-tier secrets** (integrations design T1). They are sealed
    with `srv/secrets`, as Invoices keeps its Vipps keys, never shown again, and
    replaced, not read.
  - **Verify** calls `Verify` and `Services`, and shows the services the account can
    book.
  - Several accounts may exist, for example one per site or one for test. Orders'
    settings name a **default account per site**.
  - **The instance tier gives one switch:** `SHIPPING_CARRIERS` lists the adapters the
    installation allows (default `bring`), so an operator can withhold one.
- **O52 — The waybill and the tracking link reach the customer.**
  - **The packing slip** (O47) prints, per shipment, the carrier, the service, the
    consignment number as text and Code 128, and the tracking URL as a QR code.
  - **The waybill** (*fraktbrev*) is the carrier's document, stored as the carrier
    issues it and printable from the delivery page.
    - For a service that needs no separate waybill, the label serves, and the page says
      so.
  - **Labels** print from the delivery page as PDF (A6 or A4 with four per page, as the
    carrier offers). ZPL printing is later, as Inventory's labels are (§13).
  - **The shipping e-mail.**
    - The setting `shipping_notice_on_book` (default on) queues a *Your order has
      shipped* e-mail through O25's mailings and worker when a shipment is booked.
    - It carries the order number, the carrier, the consignment number and the
      tracking link.
    - It is sent through the same `send_channel`.
  - **The order confirmation's and the invoice's mail are not changed.**
- **O53 — The `shipping` line kind.**
  - **What it is:** an order or quote line of `kind = shipping`, for the freight the
    customer pays. It has:
    - a description;
    - a price;
    - a VAT code: freight follows the VAT of the goods, so it defaults to the code of
      the order's standard-rated lines, and the user may change it;
    - quantity 1;
    - `invoice_policy = order`.
  - **Which variant it uses.** Orders' settings may name a **freight product** (a
    Service variant), whose price and VAT code fill the line. Without one, the line is
    free text.
  - **The estimate.**
    - *Estimate* on the line asks `Rates` with the order's delivery address, the
      estimated weight of its goods lines, and the default account and service.
    - It fills the price with the estimate, plus an optional markup percent from the
      settings, as `price_source = carrier`.
    - The user may overwrite it, and it then becomes `manual`.
  - **A shipping line follows its order.** It is invoiced like any line (B), has no
    delivered quantity of its own, and counts as delivered when the order's first
    shipment is booked.
  - `price_source` gains the value `carrier`.

## 8. Permissions

| Key | Covers | Sensitive |
| --- | --- | --- |
| `orders:access` | See the app, read quotes, orders, subscriptions, deliveries | no |
| `orders:quotes-manage` | Create, edit, send, revise, accept, decline, cancel quotes | no |
| `orders:sales-manage` | Create and edit orders, hold, cancel, mark delivered | no |
| `orders:sales-confirm` | Confirm, lock, unlock | no |
| `orders:subscriptions-manage` | Create, activate, change, pause, end subscriptions (S) | no |
| `orders:deliveries-manage` | Create, pick, post and cancel deliveries (C); create, book, cancel shipments and print labels (D) | no |
| `orders:carriers-manage` | Carrier accounts and their keys under Integrations (D) | yes |
| `orders:analysis-view` | The analysis views (A2) | no |
| `orders:settings` | Settings, series, templates, warnings, override a block | no |

Every key is Delegable except `orders:settings` and `orders:carriers-manage`. The
operator's event view needs no key (O4). Invoices'
wizard is behind `invoices:create`, and it reads Orders through the contract, never
through Orders' API.

## 9. Data model (schema `orders`)

| Table | Delivery | Holds |
| --- | --- | --- |
| `orders.settings` | A | One row: series starts, default validity, default site (C), `send_channel`, `confirmation_on_confirm`, `combine_by_default`, default payment terms, mail text overrides |
| `orders.counters` | A | `counter_name`, `next_value` |
| `orders.quotes` | A | O13 header, `revision`, `valid_until`, `status`, `accepted_at`, `accepted_into_kind/id`, decline reason, O27 origin |
| `orders.orders` | A | O13 header, `status`, `on_hold`, `hold_reason`, `locked`, `version`, `delivery_status`, `invoice_status`, `site_id` (C), O27 origin |
| `orders.subscriptions` | S | O13 header, O42 plan, `status`, `billed_through`, `version`, O27 origin |
| `orders.lines` | A | O15. `owner_kind` (`quote`, `order`, `template`, `subscription`) and `owner_id`; `valid_from` and `valid_to` for subscription lines |
| `orders.templates` | A | O23 |
| `orders.order_changes` | A | `owner_kind`, `owner_id`, `version`, `actor_*`, `at`, `actions` jsonb |
| `orders.documents` | A | Stored PDFs: owner, kind (`quote`, `confirmation`, `packing_slip`), revision or version, storage key, `created_at` |
| `orders.mailings` | A | O25 |
| `orders.customer_warnings`, `orders.product_warnings` | A2 | O33 |
| `orders.invoice_allocations` | B | O36. Owner line, quantity, unit price, discount, amount, `kind` (`order_line`, `subscription_period`), period start and end, `state`, the invoice stamp, `revision`, `created_at`. Unique live `(line_id, period_start)` for periods (O43) |
| `orders.subscription_skips` | S | O44 |
| `orders.erasures` | A | O29, O54. Customer id, requested at; worked through by `orders.retention` |
| `orders.deliveries`, `orders.delivery_lines` | C | O47 |
| `orders.carrier_accounts` | D | O51. Carrier, name, customer number, sealed credentials, test flag, status, sender address |
| `orders.shipments`, `orders.shipment_packages` | D | O50. Delivery, carrier account, service, status, tracking number and URL, booked cost, idempotency key; packages with weight, dimensions and package tracking number |
| `orders.shipment_documents` | D | Stored labels and waybills per shipment (storage keys, format) |
| `orders.shipment_events` | D | Tracking events as the carrier reported them (code, text, place, time) |

The `platform` schema gains `events` and `event_deliveries` (0). Invoices gains
`tax_category_vat_codes` (0). Inventory gains `reservations`, the `issue` kind and
`sites.fulfils_orders` (C).

**Indexes the lists need:**
- `orders` on `(status, delivery_status, invoice_status)`;
- `orders` on `customer_id`;
- `orders` on `salesperson_id`;
- `quotes` on `(status, valid_until)`;
- `lines` on `(owner_kind, owner_id, position)`;
- `lines` on `variant_id` where it is not null, for the variant holder;
- the origin's unique index.

## 10. Platform, registries and documentation

- **Registries, joined in A:**
  - `everyModule()`;
  - `APP_ROUTE_PREFIXES` and `appRoutes`, with a `promptVersion` bump;
  - every depguard set;
  - `modulesEnv`;
  - the `MODULES` dependency (O9);
  - `openapi.go` `Modules`, `schema_test.go`, `compose_test.go`, `gen-client.ts`;
  - `docs/astro.config.mjs`;
  - `deploy/compose/*`;
  - the host's `moduleKeys`, `apps.ts`, the layout route, `i18n.ts`, the admin
    permission labels, the dashboard tile ("Quotes awaiting an answer", "Orders to
    invoice"), the spotlight, and `sessionClients`;
  - README.md and CONTRIBUTING.md.
- **The Assistant** gets Orders' read operations as tools in A, through the existing
  per-module tool registration. Writes come only after the browser check.
- **Documentation**, each in the delivery that changes it, in English and bokmål where
  the section is bilingual:
  - `user/orders.md` (new): quotes, orders, sending, invoicing from orders,
    subscriptions, deliveries, analysis. A, then extended in A2, B, S and C;
  - `admin/orders.md` (new): settings, series, the send channel, templates, warnings.
    A and A2;
  - `reference/orders.md` (new, English): every rule, status code and permission
    above. A–C;
  - `reference/invoices.md`: the VAT mapping, `SalesTax`, the *From orders* wizard, the
    billing run's orders source. 0 and B;
  - `user/invoices.md` and `admin/invoices.md`: the VAT mapping (0) and the wizard (B);
  - `reference/products.md`: the contract changes (0);
  - `reference/customers.md`: the contract additions and the timeline entries from
    Orders (0, A);
  - for shipping (D):
    - `user/orders.md` gains the shipments section;
    - `admin/shipping.md` (new, `en` and `nb`): carrier accounts, test mode, the
      default account per site, the freight product, the markup;
    - `reference/orders.md` gains §shipping;
    - `contributing/` gains a page on the `srv/shipping` port and how to add a carrier;
    - `admin/configuration.md` lists `SHIPPING_CARRIERS`;
  - `reference/inventory.md`, `user/inventory.md`: reservations, available, the
    `fulfils_orders` flag, the `issue` kind (C);
  - `contributing/module-boundaries.md`: rules 23 and 24, the new slots, the outbox
    (0, C);
  - `admin/operations.md` (or the page operators use for workers): the events worker
    and the stuck-deliveries view (0);
  - every overview page that lists modules.
  - **`go generate`** in `apps/server` after every docs change.
- **ROADMAP.md:**
  - an Orders section with these phases;
  - the outbox marked built;
  - Products phase 4 and 5 re-aimed: phase 5 waits for the purchasing app;
  - Inventory's reservations and `issue` marked done in C;
  - Customers' timeline writers started.

## 11. Edge cases

- **The customer's currency changes after a draft was priced.** The next save re-prices
  catalog lines (O16). A sent or confirmed document keeps its currency.
- **A product's tax category changes.** Drafts follow it on save. Frozen documents keep
  their snapshot, and so does the invoice drafted from them, because the allocation
  carries the line's VAT code.
- **A VAT rate changes on 1 January** while an order spans it.
  - The line keeps its frozen code.
  - Invoices applies its own dated rate at issue, as it does today.
  - The order's shown VAT may then differ from the invoice's. The order page shows
    "VAT as invoiced" from the allocations.
  - The reference documents this. It is rare and lawful.
- **Two users edit one confirmed order:** the second save gets `version_conflict` (O26).
- **An order is put on hold while a draft invoice holds its allocations.** The issue is
  refused (`source_not_invoiceable`), and the user releases the hold or deletes the
  draft.
- **An order is cancelled while a draft invoice holds an allocation.** Cancel is refused
  while any `draft` allocation exists (`order_in_draft_invoice`). The refusal offers *Release draft allocations* (O36).
- **A merged customer:**
  - documents follow the merge (O29);
  - a combined draft invoice across the two customers is Invoices' own merge rule.
- **Inventory switched off after reservations exist.** The ledger is still composed
  (O45), so cancel, edits and manual marking release reservations as before. Nothing
  is stranded, and turning Inventory back on shows true figures.
- **A subscription started on 31 January, monthly:** periods end on the last day of each
  month, and the first period is pro-rated from 31 January to 31 January.
- **A quote in EUR accepted into an order:** the order keeps EUR. The invoice is in EUR,
  as Invoices supports.

## 12. Testing

- **0:**
  - the outbox: publish and roll back (no event);
  - at-least-once to a handler that fails twice;
  - aggregate order held behind a failed delivery;
  - dead after eight attempts;
  - pruning;
  - two dispatchers on one database never handling one delivery twice (a pool-of-one
    test, per the lease-worker lessons);
  - the catalog contract changes;
  - `docmath` extracted with Invoices' existing tests passing unchanged.
- **A:**
  - every state transition and refusal of O18–O26;
  - the snapshot freeze;
  - revisions and their PDFs;
  - optional lines out of totals;
  - the template apply;
  - idempotent create by request id and by `(channel, external_id)`;
  - the customer and variant holders;
  - the timeline handler through the dispatcher.
- **B:**
  - partial and combined invoicing;
  - two drafts never allocating one quantity;
  - the holder's three refusals;
  - full credit release;
  - **line amounts equal** between order and invoice on generated lines (property test
    over prices, discounts, rates and quantities), and **document totals equal** for one
    full invoice per order;
  - concurrent `Allocate` never over-allocates; a released draft frees its quantity;
    the sweep;
  - the billing run.
- **S:**
  - the period calendar (month ends, leap years, partial first period);
  - in advance and in arrears;
  - a change effective from the next period;
  - pause and resume skipping periods;
  - the 12-month limit;
  - `billed_through` forward and back.
- **C:**
  - reserve, refuse and release under concurrency (two confirms competing for the last
    unit);
  - issue consuming reservations;
  - partial deliveries and back orders;
  - the *on delivery* policy;
  - Inventory off.
- **D:**
  - the Bring adapter against `bringtest`, and once against Bring's test mode behind
    an opt-in environment variable, as `vipps_test_env_test.go` does;
  - a retried booking books once (the idempotency key);
  - the cancel window;
  - tracking status folding;
  - the poller's lease on a pool of one;
  - a sealed key never answered by the API;
  - the estimate and the markup on a shipping line;
  - the packing slip with consignment number and QR;
  - the shipping e-mail queued once per booking.
- **Integration:** `assistant_test.go`'s `everyModule()` with Orders, and a scenario
  from quote to subscription invoice.
- **Gates:** `mise run server:check`, `server:test`, `frontend:check` and `docs:check`
  pass for each delivery.

## 13. Later, and what each waits for

| Later | Waits for |
| --- | --- |
| Price lists, customer-group prices, quantity breaks | Products phase 4 |
| Online acceptance link, then a customer portal | A platform decision on external users |
| Prepayment request (*forskuddsforespørsel*) with KID, deducted on the final invoice | Phase 3, Invoices |
| Returns (RMA), partial credit releasing quantity, restock | Phase 3; eCommerce makes it required |
| Peppol Order in, Order Response out | Phase 3, the access point's ordering support (to verify) |
| Quote → project | Phase 3 |
| Goods subscriptions (an order per period) | Owner's call after S |
| Gross-based rounding for consumer channels | Invoices, with eCommerce |
| Incoterms on order and invoice | Phase 3 |
| Kits and bundles | Products Later |
| Carriers beyond Bring (PostNord, Helthjem, Porterbuddy, or nShift as an aggregator) | An adapter each on the `srv/shipping` port, in the order R §2.9 recommends |
| ZPL label printing | Demand, with Inventory's thermal labels |
| Shipping as its own app | Shipping growing beyond orders: shipments from no order, pickup planning, freight invoice checking. Until then it stays inside Orders (§7a) |
| An orders tile on the customer 360 overview panel | A provider read for Customers' overview endpoint |
| Transfers between sites | The purchasing app |
| Purchase orders, suppliers, receipts against a PO, drop shipment | The purchasing app (R §11.1) |
| `contracts.SalesOrders.Create` published | The first channel: Peppol, eCommerce or Point of sale |

## 14. What this phase keeps for the channels

1. **Origin and the unique external reference** on every document (O27). A channel adds
   a `channel` value, never a column.
2. **The create command** has the contract's shape already (O28). It accepts
   channel-priced lines.
3. **Line kinds are an open set**, and `parent_line_id` exists (O15).
4. **Prices are stored net, with a price mode on the document** (O19). Gross-based
   rounding is a known Invoices change, not an Orders reshape.
5. **Quantities, not amounts, carry progress** (O15, O36). A returned quantity joins as
   one more bucket.
6. **The site is on the order**, and stock is reserved per site (O45, O46). Point of
   sale's click-and-collect is an order at a store site.
7. **Events are versioned contracts** (O1). A channel listens without Orders knowing.
8. **Subscriptions are their own aggregate** (O42). Goods subscriptions add orders per
   period through the create command, not fields on the order.
9. **Carriers sit behind one port** (O49). A webshop's checkout asks the same `Rates`
   and `PickupPoints` that an order's shipping line asks.

## 15. Open questions

None from the owner: R §11.1 answered every reading. The plan confirms these in the
code:

- **Q2:** Where Invoices keeps the seller block, which O8's `Seller` reads.
- **Q3:** Invoices' erase wording for retained documents, which O29 copies.
- **Q4:** answered by the review: Invoices' header has `delivery_from` and
  `delivery_to` (`mig/00034`). O39 uses them.
- **Q5:** Whether Invoices' issue refuses a source with a `ProjectID` while Projects is
  off (O38).
- **Q6:** Where Invoices calls `ReleaseAllocations` after a draft's delete or a line's
  removal commits (O36). This is the drafts code in `srv/invoices/drafts.go`.

## 16. Revision 2 — the critic review and shipping, 2026-11-05

These came from an adversarial review against the code. Each was checked and fixed in
place.

- **O17. The arithmetic.**
  - Invoices' `computeLine` and `summarize` move verbatim. Lines store gross,
    allowance and net, and VAT is per document. Rounding is half away from zero.
  - Totals are equal only for one full invoice per order.
- **O15. The line.**
  - `unit_price` is `numeric(18,8)`.
  - `packaging_id` and `packaging_quantity`, and `external_line_id`, are added.
  - Every line write locks the variants first (MB rule 21).
- **O6. The catalog.**
  - Time's rate chain is the second `ListPrice` caller.
  - The float rule is scoped to new contracts.
  - The tax kind and rate are cut from `VariantEntry`.
- **O13 and O39. References.** There is one reference, `your_reference`, which is
  EHF's `BuyerReference`.
- **O30. The variant holder.** `VariantHold` gains a kind and a count, and the holder
  is told the purpose, so a type change is never blocked by orders.
- **O54. Retention** is now written, and erasure is completed: mailings, user
  snapshots and the retention sweep.
- **O36 and O37. Allocations.**
  - Each `Allocate` writes new rows under the lines' lock.
  - `ReleaseAllocations` is called by Invoices after a draft is deleted, with a 30-day
    sweep and a manual release as backstops.
  - Cancel no longer names a draft it cannot know.
- **O43 and O44. Subscriptions.**
  - Per-period allocations are the truth, behind a unique live index.
  - `billed_through` is derived, and skipped periods are recorded.
  - A full credit of one period no longer re-bills the next.
- **O25 and O2. Handlers.**
  - Handlers never leave the process. Mail is queued and sent by `orders.mail`.
- **O3. Ordering per aggregate** holds under the aggregate's row lock, and a dead
  delivery blocks until it is retried or skipped. Payloads carry no personal data.
- **O4. The operator view** moves to the management listener. The
  `platform:events-manage` key is gone.
- **O20.** `orders.quote.sent.v1` is published, for the timeline.
- **O38.**
  - Rule 10 is amended for a holder that publishes an event.
  - The partial-credit behaviour is stated exactly.
  - Projects being off is a plan check.
- **O45. The ledger** is composed whatever `MODULES` says. Manual marking releases
  reservations. Inventory's corrections may take available below zero.
- **O26.** Draft allocations count as progress when an order is edited.
- **O27.** `payment_reference` and `prepaid_amount` are reserved for prepaid
  channels.
- **O10.** Addresses carry their id.

**Shipping** (owner, 2026-11-05) is delivery D, decisions O49–O53 (§7a). The carrier
research of R §2.9 includes Porterbuddy.

