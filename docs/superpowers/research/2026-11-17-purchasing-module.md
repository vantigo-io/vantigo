# Purchasing module — research

The owner chose on 2026-11-17 **Purchasing as the next module**: the sell side runs from
quote to the books, and the buy side has no purchase order. This note surveys what
comparable products do, sets it against what Vantigo has, and proposes phases. The design
spec (`specs/2026-11-17-purchasing-design.md`) starts from §8 and does not reopen it.

The module is **Purchasing** (`purchasing`; Norwegian **Innkjøp**; package
`@vantigo/purchasing-ui`; permissions `purchasing:*`). Code references are against `main`
at `64829c4e2`, whose latest migration is `00188`. `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`.

## 1. What is decided, and what this note adds

### 1.1 On the record

- **Orders' design** (`specs/2026-11-05-orders-design.md:32`): *Purchasing is not in this
  module. It becomes its own app with its own research.* Purchase orders, suppliers,
  receipts against an order and drop shipment are named for it (`:1501`).
- **ROADMAP.md, Inventory › Later:** *purchase orders from the low-stock list, and
  incoming from open purchase orders, wait for Purchasing*; supplier records on receipts
  wait for Products phase 5.
- **ROADMAP.md, Manufacturing › Later:** *subcontracted operations wait for Purchasing*.
  The phase 2 design keeps **buy** suggestions read-only *because there is no Purchasing
  module* (`specs/2026-11-12-manufacturing-phase2-design.md:1052`).
- **ROADMAP.md, Products phase 5:** unit conversions and *supplier records per
  product/variant (vendor SKU, lead time, minimum order quantity)*.
- **Accounting's design** (`specs/2026-11-14-accounting-design.md:50, 310, 317, 523`):
  inbound EHF, a second person's approval and payment files are its phase 2; inbound EHF
  *lands as a draft supplier invoice*.
- **The bank feed's design** (`specs/2026-11-13-bank-feed-folio-design.md:293`):
  *supplier invoices paid with KID belong with the Purchasing module*. §9 reads this
  again.
- **2026-11-08, the owner** (Inventory phase 2): *do not stray far from what our
  competitors do*.

### 1.2 What this note settles

- what purchasing software does, and what a small Norwegian business uses of it (§2, §3);
- the domain model (§4) and its seams to Accounting, Inventory, Products, Manufacturing
  and Orders (§5);
- where the codebase stands (§6);
- the phases (§7) and the must-have list for phase 1 (§8);
- the readings, with the default taken for each (§9).

## 2. What the products do

Sources are the products' own documentation; the URL follows each claim. Tripletex's and
Visma's help sites refused a direct fetch, so their claims rest on search excerpts of the
named articles and are marked **(excerpt)**.

### 2.1 The Norwegian and Nordic SMB field

- **Tripletex, Logistikk Basis (excerpt).** Purchase order (*innkjøp*), goods receipt
  (*varemottak*), supplier invoice. An order does not change stock; a receipt does
  ([Nytt innkjøp](https://hjelp.tripletex.no/hc/no/articles/4408491926289-Nytt-innkj%C3%B8p)).
  A short delivery is a deviation on the receipt, and completing it spawns a back-order
  purchase numbered `4/1`, `4/2`
  ([Avvik i varemottak](https://hjelp.tripletex.no/hc/no/articles/4406369801617-Hvordan-registrerer-jeg-avvik-i-varemottaket)).
  Posting a detailed supplier invoice can create the receipt
  ([Automatisk varemottak](https://hjelp.tripletex.no/hc/no/articles/42861758812817-Slik-bruker-du-automatisk-varemottak-basert-p%C3%A5-ordrelinjer)).
  Stock value is purchase price × quantity, not FIFO
  ([Lagerverdi](https://hjelp.tripletex.no/hc/no/articles/20137755610257-Slik-beregner-Tripletex-lagerverdi)).
- **Tripletex, Logistikk Komplett (excerpt)** is Rackbeat: FIFO or average, cost locked
  when the supplier invoice is posted through the order, freight spread over a purchase by
  quantity or value
  ([Leverandørfaktura via innkjøpsordre](https://hjelp.tripletex.no/hc/no/articles/34648204809745-Slik-oppretter-du-en-leverand%C3%B8rfaktura-gjennom-en-innkj%C3%B8psordre-i-logistikk-komplett)).
- **PowerOffice Go.** No purchase order or goods receipt found in its help centre;
  purchasing is a partner extension
  ([CostTracker](https://www.poweroffice.no/utvidelser/costtracker)). Probable, not
  confirmed.
- **Fiken.** No purchase order found. Stock is a year-end count posted as a value, and
  Fiken states it knows no purchase price
  ([Varetelling](https://hjelp.fiken.no/varelager--varetelling)).
- **Visma eAccounting (excerpt).** Purchase orders in three tabs — drafts, ongoing,
  completed; sending sets *Ordered*; marking *Delivered* or *Partially delivered* creates
  the inbound stock movement
  ([Innkjøpsordre](https://help.visma.net/no_no/eaccounting/content/online-help/purchasing-purchase-orders.htm)).
  Stock is FIFO on purchase price plus freight.
- **Fortnox Lager.** Purchase orders in the supplier's currency, created from a customer
  order too; inbound deliveries linked to one or several orders and reconciled against the
  supplier invoice, which updates the purchase price; automatic purchase suggestions, a
  reorder point and maximum per article and location, supplier article number, price per
  supplier ([Fortnox Lager](https://www.fortnox.se/produkt/lagerprogram)).
- **e-conomic Lager.** Average cost, set by a posted supplier invoice; order → receipt →
  invoice with partial receipts and backorders
  ([Lager](https://www.e-conomic.dk/support/artikler/lager-0)).

### 2.2 Odoo, Business Central, ERPNext, Visma.net

- **Odoo Purchase.** A request for quotation becomes a purchase order on confirmation,
  which creates the receipt; expected arrival from the vendor's lead time
  ([RFQ](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/purchase/manage_deals/rfq.html)).
  Bills are controlled on ordered or received quantities, per product
  ([Vendor bills](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/purchase/manage_deals/manage.html)).
  Reordering rules of minimum, maximum and multiple use the first vendor on the product
  ([Reordering rules](https://www.odoo.com/documentation/18.0/applications/inventory_and_mrp/inventory/warehouses_storage/replenishment/reordering_rules.html)).
  Dropshipping and landed costs are separate flows.
- **Business Central.** Quote, order, blanket order, invoice, return order, credit memo;
  approval workflows on all
  ([Purchasing](https://learn.microsoft.com/en-us/dynamics365/business-central/purchasing-manage-purchasing)).
  *Qty. to Receive* and *Qty. to Invoice* per line; what is not received cannot be
  invoiced; over-receipt by a tolerance code. **An incoming e-invoice is matched to an
  order by an exact lookup of its purchase order number**, then warns on quantity over
  received, over remaining, and a price difference above one percentage
  ([Match drafts to orders](https://learn.microsoft.com/en-us/dynamics365/business-central/match-purchase-invoice-drafts-to-orders)).
- **ERPNext.** Material request, supplier quotation, purchase order, purchase receipt,
  purchase invoice. Order statuses *To Receive and Bill*, *To Receive*, *To Bill*,
  *Completed*, *On Hold*, *Closed*, *Cancelled*; received and billed percentages per order;
  a purchase unit with a conversion factor
  ([Purchase Order](https://docs.frappe.io/erpnext/user/manual/en/purchase-order)).
- **Visma.net ERP (excerpt).** Minimum and maximum receipt percentages per line with
  reject, warn or accept; a unit-cost difference between invoice and receipt posts to a
  purchase price variance account.

### 2.3 What every one of them has

For the systems that have purchasing at all:

- one purchase order per supplier, sent by mail, with a small state set: draft, ordered,
  partially received, received;
- a receipt against the order that alone moves stock, with a partial receipt and a
  remainder;
- a link from the supplier invoice to the order, which updates the purchase price;
- a price per supplier in the supplier's currency.

### 2.4 What one or two have

Requisitions, requests for quotation and supplier quotations; blanket orders; matching
policies and tolerances; approval limits; planning engines; drop shipment as a flow
(Fortnox is the SMB exception); landed-cost documents; received-not-invoiced accruals;
standard cost with variances.

### 2.5 Valuation at receipt

| System | Cost method |
| --- | --- |
| Tripletex Basis | purchase price × quantity |
| Rackbeat (Tripletex Komplett, e-conomic) | FIFO or average |
| Visma eAccounting, Fortnox | FIFO |
| e-conomic Lager | average |
| Odoo | standard, average or FIFO per category |
| Business Central | FIFO, LIFO, average, specific, standard |
| ERPNext | FIFO or moving average |

Goods received and not invoiced are accrued in the ledger by Business Central (optional),
Odoo and ERPNext. The SMB systems do not: Visma eAccounting books stock on demand,
Tripletex Basis not at all, Fiken a year-end count.

## 3. Norwegian and Peppol specifics

- **The order reference on an EHF invoice.** Peppol BIS Billing 3.0 requires the buyer
  reference or the order reference, and says the order reference should be used when there
  is one; lines may name order lines
  ([BIS Billing 3.0](https://docs.peppol.eu/poacc/billing/3.0/bis/)). In UBL it is
  `cac:OrderReference/cbc:ID`. So **our order number, printed on the order, is the key an
  incoming invoice is matched by** — the Business Central model.
- **The e-invoicing duty**
  ([Skatteetaten](https://www.skatteetaten.no/bedrift-og-organisasjon/starte-og-drive/rutiner-regnskap-og-kassasystem/pliktig-efaktura/)):
  sending between bokføringspliktige from 2027-01-01 to a buyer registered in ELMA;
  receiving, and an electronic accounting system, from 2030-01-01. A registered buyer
  gets nearly every domestic supplier invoice as EHF from 2027, so matching by order
  number pays off three years before the duty to receive.
- **EHF Ordering and Despatch Advice** exist and are maintained by DFØ
  ([the specifications](https://anskaffelser.dev/postaward/g3/spec/)), written for public
  procurement; access-point support varies. No mandate and no figures for SMB use were
  found. That it is mostly public sector and the large chains is an inference.
- **Bookkeeping.** The purchase voucher is the seller's sales document
  ([bokføringsforskriften § 5-5](https://lovdata.no/forskrift/2004-12-01-1558/§5-5)); the
  purchase order is not a voucher. Which retention class an order and an incoming packing
  slip fall in was **not verified**.
- **VAT.** A foreign supplier's goods invoice carries no Norwegian VAT; import VAT is
  self-assessed in the MVA-melding from the customs declaration's values (codes 81–85),
  and services bought abroad are reverse-charged (86, 87)
  ([Skatteetaten](https://www.skatteetaten.no/bedrift-og-organisasjon/avgifter/mva/utland/import/beregne/)).
  The basis is not on the order line, so **the tax treatment is the invoice's, never the
  order's**.
- **Payment.** pain.001 carries the KID as a structured creditor reference
  ([Nordea, Norway](https://www.nordea.com/en/doc/corporate-access-payables-appendix-norway.pdf)).
  It pays a supplier invoice, not an order.

## 4. The domain model

- **Supplier** — who is bought from. One register, Accounting's (§5.1).
- **Supplier item** — what one supplier sells of one variant: its item number there, the
  price in its currency, the purchase packaging, the lead time, the minimum quantity and
  the multiple. A variant may have several; one is preferred.
- **Purchase order** — one supplier, one receiving site, a currency, lines. A line is a
  variant (goods) or a text (a service, freight, something outside the catalog), with a
  quantity, a unit price and an expected day. States *draft*, *sent*, *partially
  received*, *received*, *closed*, *cancelled*. It moves neither stock nor the ledger.
- **Receipt** — goods arriving against an order, some or all lines, into locations. It is
  one Inventory movement of the existing kind `receipt`, whose source is the order.
- **Match** — which order lines a supplier invoice bills, by quantity and amount. Per
  line the order then knows ordered, received and invoiced.
- **Proposal** — a suggestion to buy, from the low-stock list or from Manufacturing's
  replenishment, turned into draft orders per supplier.

## 5. Seams to the modules that exist

### 5.1 Accounting

- The supplier register is `accounting.suppliers` (`mig/00185_accounting_purchases.sql:17`):
  number, name, organisation number, address, bank account, payment terms, currency,
  default account and tax code, `is_person`, `anonymised_at`. **No contract exposes it.**
- Every Accounting operation but the setup answers `409 books_not_set_up` until *Set up
  the books* is done (`srv/accounting/server.go:121`). The register is behind that gate
  today (reading 1).
- A supplier invoice's lines are account, tax code and amount (`mig/00185:143`): no
  variant, no quantity. Its `reference` (100 characters) is free text. The match
  therefore lives beside the invoice, not on it.
- Accounting publishes no domain event (`srv/contracts/` has no `events_accounting.go`).
- A payment records one already made (`srv/accounting/supplier_payments.go:103`); there
  are no payment files.

### 5.2 Inventory

- A receipt is `POST /movements/receipts` (`srv/inventory/receipts.go:28`), with free-text
  `supplierName` and `supplierReference`, lots and serials on tracked variants, and the
  `clientRequestId` idempotency.
- `inventory.movements` already has `source_module`, `source_kind`, `source_id`,
  `source_line_id` (`mig/00108_inventory_baseline.sql:68`), which a receipt does not set.
- `contracts.StockLedger` (`srv/contracts/stock.go:312`) reserves, issues and produces on
  the caller's transaction (rule 24). **It has no receive.**
- *Incoming* on the low-stock list is transfers in transit alone
  (`srv/inventory/queries/transfers.sql:204`).
- Inventory has no valuation: its roadmap leaves that to a ledger.

### 5.3 Products

- `contracts.VariantEntry` (`srv/contracts/catalog.go:15`) carries the unit, the
  packagings, the tracking mode and a `StandardCost` without a currency. There is no
  supplier data on a variant and no write contract for the cost.
- Packagings (a case of 15, a pallet of 40 cases) are the units a purchase is entered in
  today. Purchase units that are not packagings — kg bought, pieces stocked — are
  Products phase 5.

### 5.4 Manufacturing

- Replenishment (`srv/manufacturing/replenishment.go`) computes *make* and *buy*
  suggestions from projected stock and unreserved demand, and refuses to act on a *buy*
  row (`:664`).
- Subcontracting is not started.

### 5.5 Orders and the platform

- A purchase order mirrors Orders' documents: the numbering series
  (`srv/orders/settings.go:27`), the stored PDF (`pdfdocs.go`), the leased mail queue
  through `contracts.OutboundMail` (`mail_worker.go`), a delivery that posts stock
  (`deliveries.go`).
- The outbox is `events.Publish` on the publisher's transaction
  (`srv/events/events.go:80`), handlers through `Module.EventHandlers`.
- The access-point port (`srv/invoices/accesspoint/accesspoint.go:20`) only sends; a
  received document is acknowledged and dropped (`storecove.go:150`).

## 6. Where the codebase stands

Nothing of Purchasing exists: no module, no schema, no supplier contract, no receive on
the ledger. Everything it needs to read exists. Four seams must be cut first: a supplier
directory, a receive on the ledger, a slot for open purchases that Inventory and
Manufacturing read, and Accounting's events for a supplier invoice.

## 7. Phases

- **Phase 1 — order, receive, match.** The seams; supplier items and purchase orders;
  receipts against an order with *incoming*; proposals from low stock and from
  Manufacturing; the supplier invoice matched to the order.
- **Beside it, in Accounting's phase 2:** inbound EHF as a draft supplier invoice, and
  payment files. Phase 1's match is built so that a draft's order reference finds its
  order whoever made the draft.
- **Phase 2 — candidates:** supplier returns and the credit note; subcontracted
  operations with Manufacturing; drop shipment from a sales order; an approval limit;
  freight spread over a receipt; purchase units with Products phase 5.
- **Later:** requests for quotation, requisitions, blanket orders, EHF Ordering, supplier
  price lists with quantity breaks, valuation and the received-not-invoiced accrual.

## 8. The must-have list (phase 1)

1. The supplier register read through a contract, and usable for purchasing.
2. Supplier items per variant: item number, price, packaging, lead time, minimum,
   multiple, one preferred.
3. A purchase order in the supplier's currency with variant and text lines, a number
   from a series, a PDF in the supplier's language, sent by mail from a chosen channel.
4. The supplier's confirmation recorded: its reference and the confirmed day.
5. A receipt against the order, partial or whole, into locations, with lots and serials,
   posted through Inventory's ledger with the order as its source.
6. The remainder closed, and a receipt taken back.
7. *Incoming* from open purchase orders on the low-stock list, the product's stock and
   Manufacturing's replenishment.
8. Proposals: low stock and *buy* suggestions turned into draft orders per preferred
   supplier.
9. A supplier invoice matched to an order, suggested by the order number in its
   reference; ordered, received and invoiced per line; warnings on quantity and price,
   never a refusal.
10. A received-not-invoiced list.

## 9. Readings, with the defaults taken

1. **Whose is the supplier register?** *Default: Accounting's stays the one register,
   Purchasing requires Accounting, and the register is let out from behind the setup
   gate.* A second register in Purchasing would have to be merged with Accounting's on
   every invoice. Moving the register to its own module is the cleaner end and a larger
   change; the contract in between keeps that move open.
2. **Where do supplier items live?** *Default: Purchasing.* The roadmap put them in
   Products phase 5, written before a supplier register existed. They are a supplier's
   facts about a variant, and Products would have to read Accounting to hold them.
   Products phase 5 keeps the unit conversions.
3. **Who builds inbound EHF and payment files?** *Default: Accounting, as its phase 2,
   sequenced right after this phase.* The bank feed's design named Purchasing for KID
   payments; an invoice with no order is paid the same way, so it is the payables' work.
4. **Does a receipt set a cost?** *Default: no valuation.* The order line's price is kept
   on the receipt line and the invoice's on the supplier item as the last price; nothing
   is posted at receipt. Two of the four SMB systems do no more.
5. **Is the VAT on the order?** *Default: no.* An order shows amounts without VAT; the
   invoice decides the treatment (§3).
6. **Does a mismatch stop the invoice?** *Default: never.* Posting is Accounting's and is
   not held by Purchasing; a difference is a warning on the order and the invoice.
7. **An approval before sending?** *Default: two permissions and no limit.* Writing an
   order and sending it are separate keys; an amount limit is phase 2.
8. **A short delivery?** *Default: the order stays open with its remainder*, closed by a
   person, as Business Central and ERPNext do — not Tripletex's spawned back-orders.
