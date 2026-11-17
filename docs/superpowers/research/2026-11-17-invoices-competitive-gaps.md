# Invoices — what the competitors have that we do not

The owner asked on 2026-11-17 what Invoices is missing next to its competitors, Odoo
Invoicing first. This note sets Odoo's invoicing, and where it matters Tripletex, Fiken and
PowerOffice Go, against what Invoices does after its five phases. It ranks the gaps and
proposes phase 6. The design spec (`specs/2026-11-17-invoices-phase6-design.md`) starts
from §6 and does not reopen it.

Code references are against `main` at `a9f140c82`, whose latest migration is `00192`.
`srv/` abbreviates `apps/server/internal/`, `mig/` `srv/db/migrations/`, `R/`
`docs/src/content/docs/en/reference/`.

## 1. What is on the record

- **ROADMAP.md, Invoices › Later** names, among others: recurring invoices and retainers
  (since built as Orders' subscriptions), the 2028 buyer org-number rule, eFaktura and
  AvtaleGiro for consumers, a customer portal, several legal entities per tenant, and a
  posting export. Phase 4 left out on purpose (design D20): customer credit balances,
  refunds as a flow, setting an overpayment off against the next invoice, rounding off
  small differences, cards and other payment providers.
- **Phase 2** left out (`R/invoices.md`, *What comes next*): foreign currency in the EHF,
  bulk sending, the document-level allowance, the corrected invoice (type 384), and
  eFaktura and AvtaleGiro.
- **Phase 1B** left out: a payment in another currency than the document's, one payment
  across several invoices, a payment against a credit note, HTML mail, a logo, an editable
  template, a personal message in the mail, bulk sending.
- **Phase 3, D11** (`specs/2026-10-05-invoices-work-to-invoices-design.md:621`): the module
  issues in NOK only; work in another currency is listed and not selectable.
- **Phase 5, D12**: "send all" for a billing run, and a run on a schedule.
- **Accounting's design**: inbound EHF is its phase 2 (`specs/2026-11-14-accounting-design.md`),
  confirmed by Purchasing's design (`specs/2026-11-17-purchasing-design.md`, §1 and §13).
- **2026-11-08, the owner**: *do not stray far from what our competitors do*.

## 2. What Odoo Invoicing offers

Sources: [the app page](https://www.odoo.com/app/invoicing), [the feature list](https://www.odoo.com/app/invoicing-features),
[multi-currency](https://www.odoo.com/documentation/latest/applications/finance/accounting/get_started/multi_currency.html)
and [payments](https://www.odoo.com/documentation/latest/applications/finance/accounting/payments.html)
in Odoo's documentation, read 2026-11-17.

- **Invoices:** create, print, e-mail; credit notes and refunds; recurring invoices by a
  product's invoicing frequency; configurable payment terms (instalments); automatic tax
  with configurable rounding; fiscal positions (OSS); Incoterms; customer autocompletion
  from a VAT number; sequence numbers with gap warnings.
- **E-invoicing:** Peppol and national formats in 160 countries, including EHF; *unlimited
  e-invoices sent and received* at no cost.
- **Design:** the invoice's layout, style and logo.
- **Payments:** online payment through a customer portal (Stripe, Adyen, PayPal and
  others); payment links; SEPA direct debit and card tokens for batch collection; a bank
  connection; a payment not linked to an invoice becomes a customer credit that reduces
  unpaid invoices; several payments per invoice and one batch across several.
- **Currencies:** any currency on an invoice; rates fetched daily, weekly or monthly or
  entered by hand; exchange differences posted automatically to dedicated accounts in a
  dedicated journal.
- **Follow-ups:** open items with statuses and due dates; follow-up reminders; postal
  sending of invoices and follow-ups (*snail mail*).
- **Credit:** customer credit limits with an alert when exceeded.
- **Other:** several companies, with orders and invoices mirrored between them; a mobile
  app; invoice analysis and sales dashboards; first app free with unlimited invoices.

## 3. What Invoices does today

Five phases, all merged (`ROADMAP.md`, *Invoices*; `R/invoices.md`):

- the legally strict sales document, gap-free series, VAT codes, PDF, credit notes, the
  journal, retention;
- EHF over Peppol through Storecove, the Peppol lookup, KID, and from 2027-01-01 the
  B2B duty enforced in the send dialog (`buyer_norwegian_business_required`);
- invoicing hours, expenses and milestones from Time, Expenses and Projects; consumption
  from Energy with billing runs; orders and subscriptions from Orders; a-konto and the
  final settlement; the quick invoice;
- payments by hand, OCR and camt.054 files, the bank feed (Folio), the exception queue
  with 14 reasons and manual allocation of one bank line across up to 20 invoices
  (`srv/invoices/bankqueue.go:652-845`);
- reminders and collection under inkassoloven and the forsinkelsesrenteloven, charges,
  printed letters, holds, the hand-off to collection;
- Vipps MobilePay pay links, a public pay page and payment on site;
- Accounting reads the sales journal and posts it; it ages receivables.

## 4. The gaps, ranked

Each gap names what Odoo (O) or the Norwegian field (N) has, what we have, and why it is
ranked where it is.

### 4.1 Invoicing in other currencies — high

- **O:** any currency, rates by a web service, exchange differences posted.
- **Us:** NOK only. The schema is half ready:
  - `invoices.invoices` already carries `currency`, `exchange_rate` and
    `exchange_rate_date` (`mig/00034:117-119`), and no CHECK pins NOK. Nothing ever sets
    the rate, so it stays 1 (`srv/invoices/queries/invoices.sql:140`).
  - `docmath.Summarize` already computes VAT in NOK by the rate (`srv/docmath/docmath.go:87-108`).
  - Credit notes already copy the original's currency and rate (`srv/invoices/queries/credits.sql:11,20`).
  - Orders already carries a currency on quotes, orders and subscriptions (`mig/00119:101,194`,
    `mig/00124:77`). Invoices refuses them with `currency_not_supported`
    (`srv/invoices/from_orders.go:122,434`).
  - Customers' billing profile already has a currency (`mig/00019:12`).
- **What blocks it:**
  - The settings and the draft refuse anything but NOK (`settings.go:215`, `drafts.go:248`).
  - The bank paths pin NOK:
    - `ck_bank_transactions_currency` (`mig/00041:151`);
    - `bank_feed_sync.go:461`;
    - `bankfile/camt054.go:196`;
    - `bankqueue.go:816`.
  - Vipps attempts hard-code `payments.NOK` (`attempts.go:429`).
  - The EHF has no `TaxCurrencyCode` and one `TaxTotal` (`ehf/render.go:271-280`).
  - The PDF prints neither the rate nor VAT in NOK (`pdf.go:440-484`).
  - The sales journal sends Accounting only NOK amounts and no rate
    (`srv/contracts/sales_journal.go:96-196`). Accounting posts sales in NOK only, though
    its postings and supplier payments already carry currency, rate and realised
    gain/loss on 8060/8160 (`mig/00181:244-246`, `srv/accounting/supplier_payments.go`).
  - **No exchange-rate source exists anywhere in the Go code.** Energy takes its EUR/NOK
    from the spot-price service.
- **Why high:** an exporter, a consultancy with Swedish or EU clients, or anyone quoting
  in EUR rules us out in the first demo. Orders already sells in currency and then cannot
  invoice it.

### 4.2 eFaktura and AvtaleGiro — high (N), absent in Odoo

- **N:** Tripletex charges NOK 4 per eFaktura or AvtaleGiro claim
  ([prices](https://www.tripletex.no/priser/transaksjoner/)). PowerOffice Go adds NOK 3 per
  eFaktura ([prices](https://poweroffice.no/no/priser)). Fiken sells *E-faktura via Vipps*
  at NOK 5 and lists no AvtaleGiro ([prices](https://fiken.no/priser)). All three are
  listed eFaktura systems at Mastercard Payment Services (MPS).
- **Us:** neither one; deferred in phases 2, 4 and 5.
- **How it works** (sources in §7):
  - eFaktura reaches every bank the consumer uses and Vipps. Since 2024-03-01 only the
    eFaktura 2.0 consent, *Ja takk til alle*, exists.
  - An issuer goes either through its own bank and a submitter integrated with MPS (bank
    fees around NOK 75/month plus per invoice), or through an aggregator.
    [Maventa](https://documentation.maventa.com/integration-guide/invoice-sending/consumer-invoicing/no/)
    needs neither a bank nor an MPS agreement for eFaktura, and looks consumers up by
    fødselsnummer, or by date of birth, phone and e-mail.
  - **Peppol does not carry B2C eFaktura**: Storecove, our access point, is not a route.
  - AvtaleGiro needs the creditor's own agreement with its bank and a KID. The payer's
    mandate lives in their netbank. Claims are due 4 days before the due date when the
    creditor notifies the payer itself, which an eFaktura flagged AvtaleGiro does. MPS's
    Combo API sends eFaktura, AvtaleGiro or both in one shipment.
- **Why high:** for consumer invoicing, and Energy above all, a Norwegian customer
  expects the invoice in the netbank and paid automatically. It is where we beat Odoo,
  not catch up with it. Payments come back through OCR and camt.054 with the KID, which
  the matching already handles.

### 4.3 Branding: the logo, the mail — high (cheap)

- **O:** layout, style and logo.
- **Us:**
  - The PDF has no image component. `invoices.settings` has no logo or phone column.
  - `contracts.SellerProfile.LogoStorageKey` exists and is filled with `""`
    (`srv/invoices/sales_tax.go:26-29`).
  - The mail is plain text with no personal message (`mailtext.go:11-18`), though
    `mail.Outbound` supports `HTMLBody` and inline `cid:` images (`srv/mail/outbound.go:13-28,74`)
    and Calendar already sends HTML.
  - The issued PDF is stored once and never re-rendered (`pdf.go:438-439`), so a logo
    must be snapshotted at issue like the seller record.
- **Why high:** it is the first thing a prospect sees on the first invoice in a trial,
  and it is small.

### 4.4 Freer payment matching and the customer's balance — high

- **O:** a payment not linked to an invoice becomes the customer's credit and is applied
  to open invoices; several payments per invoice; one batch across several; exchange
  differences on payment.
- **Us:**
  - Automatic matching is by KID alone (`matching.go:276-318`). A bank line above
    open + charges goes to `exceeds_open`, and an overpayment is always refused
    (`payments.go:196-202`).
  - A person can split one line across up to 20 invoices from the queue. What is left is
    `unappliedAmount`, and a dismissed surplus is "owed back", settled outside Vantigo
    (`bankqueue.go:261-306`).
  - A credit note on a paid invoice leaves `refundDue` with no flow behind it.
  - Nothing rounds off a small difference **by itself**. A person can write the rest off
    with reason `rounding`, which Accounting posts to 7790 (`mig/00182:28`,
    `srv/accounting/chart.go:231`), but a customer who pays NOK 0.50 short keeps an open
    invoice until someone does.
- **Why high:** with the bank feed live, every customer who pays two invoices in one
  transfer, or a few kroner short, lands in the queue by hand. Odoo and every Norwegian
  competitor settle these with a click or by a rule.

### 4.5 Card and other online payments — medium

- **O:** Stripe, Adyen, PayPal and others; card tokens and SEPA direct debit for batch
  collection.
- **Us:** Vipps MobilePay only, NOK only. The payments port (`srv/payments`) was built
  provider-neutral for a second adapter.
- **Why medium:** a Norwegian B2B customer pays by bank transfer and a consumer by Vipps.
  Cards matter for foreign customers, so the value rises with §4.1 and can follow it.

### 4.6 Sending in bulk, and runs on a schedule — medium (cheap)

- **O:** batch sending. **Us:** a billing run drafts and issues but does not send; no run
  is scheduled (phase 5, D12); there is no bulk send anywhere.
- **Why medium:** energy and subscription customers send hundreds of documents a month,
  one click each.

### 4.7 A customer portal — medium

- **O:** the customer sees every invoice, downloads it and pays it. **Us:** one public
  pay page per invoice (`paypage.go`), no overview.
- **Why medium:** eFaktura covers consumers better in Norway, and EHF covers businesses.
  A portal is wanted chiefly by customers with many invoices. It needs a customer
  identity, which Vantigo has nowhere yet.

### 4.8 Credit limits — medium (cheap)

- **O:** a limit per customer, an alert when exceeded. **Us:** none in Customers, Orders
  or Invoices. Orders already has sales warnings with an override, where a limit fits.

### 4.9 Lower

- **Several legal entities per tenant**, each with its own series (O mirrors between
  companies).
- **The corrected invoice** (EHF type 384), and a discount on the whole document.
- **Incoterms** (with §4.1).
- **Postal sending through a print service.** We print the letter and the business posts
  it.
- **A mobile app.** The web client is responsive.
- **Invoice analysis.** Accounting ages receivables, and Orders has sales analysis.

### 4.10 Not gaps

- **Recurring invoices:** Orders' subscriptions.
- **Autocompletion by organisation number:** Brønnøysund in Customers.
- **Gap warnings:** the journal's gap check.
- **Instalments:** a-konto and the final settlement cover the Norwegian case; payment
  terms in instalments are a reading for later.
- **AI:** the Assistant and virtual employees.
- **Inbound e-invoices:** Odoo's "received" half of its offer. It is Accounting's
  phase 2, and the receiving duty starts 2030-01-01 (transitional rule of 2026-09-25,
  [regjeringen.no](https://www.regjeringen.no/no/aktuelt/fastsetter-overgangsregel-om-plikten-til-a-motta-e-faktura-og-skattedirektoratet-delegeres-myndighet/id3173725/)).
  It is not Invoices' to build, but it is a hole in the comparison until Accounting
  phase 2 lands.

### 4.11 Where we are ahead

Odoo has no equivalent of these:

- reminders, charges and collection under Norwegian law;
- KID, with its matching;
- invoicing work from Time, Expenses and Projects;
- consumption billing for Energy;
- Vipps and payment on site;
- the B2B duty enforced at the send.

## 5. Compliance that is due regardless

- **2028-01-01, bokføringsforskriften § 5-1-2:** the buyer's organisation number on
  every invoice to a bookkeeping-obligated buyer, and the invoice always sent to the
  buyer ([Skatteetaten, regulations of 2026-09-29](https://www.skatteetaten.no/en/rettskilder/type/uttalelser/uttalelser/forskrifter-til-lovendringer-om-digital-bokforing-og-e-fakturering/)).
  It is on the roadmap's Later list: the customers directory knows no "bookkeeping-obligated"
  fact, and the issue still accepts an address instead. It is not a competitive gap but
  has a date, so phase 6 carries it.
- **2027-01-01, the B2B sending duty:** already enforced (§3).
- **B2C e-invoicing:** the Tax Directorate reports by 2026-12-15 on whether it should
  become mandatory ([Comarch summary](https://www.comarch.com/trade-and-services/data-management/legal-regulation-changes/norway-accelerates-mandatory-b2b-e-invoicing-and-digital-bookkeeping-timeline/)).
  Uncertain. If it does, §4.2 becomes compliance.

## 6. Phase 6, proposed

In order of value per effort, and so that each delivery stands alone:

| Delivery | What | Gaps |
| --- | --- | --- |
| **A** | The logo on the PDF and in the mail, the phone, an HTML mail with a personal message | 4.3 |
| **B** | The customer's balance: overpayments kept as credit, applied to invoices, refunded; one bank line matched to several invoices automatically; small differences rounded off | 4.4 |
| **C** | Foreign currency: Norges Bank rates, the document in currency with VAT in NOK, EHF, PDF, payments with the exchange difference, Orders' currency invoiced, Accounting posting in currency | 4.1 |
| **D** | eFaktura and AvtaleGiro through a consumer-delivery port, its first adapter, the mandate, the channel's fallback | 4.2 |
| **E** | Send all, and the 2028 buyer org-number rule; credit limits in Customers, warned in Orders and Invoices | 4.6, 4.8, §5 |

Later, after phase 6: cards as a second payments adapter (after C), the customer portal,
several legal entities, the corrected invoice, scheduled billing runs.

## 7. Readings, with the default taken

Each is a question the design answers with this default. §14 of the design says what an
overturn changes.

1. **The eFaktura and AvtaleGiro route.**
   - Default: build a port and pick the first adapter in the plan, not here. An
     aggregator (Maventa) needs no bank agreement for eFaktura, but Maventa is Visma's, a
     competitor's. MPS's Combo API is first-hand, but its submitter terms are not public.
   - The owner decides (design §14).
   - Sources:
     - [Maventa](https://documentation.maventa.com/integration-guide/invoice-sending/consumer-invoicing/no/)
     - [MPS Combo API](https://mastercardpaymentservices.com/norway/utvikler/combo-api)
     - [MPS mandate API](https://www.mastercardpaymentservices.com/norway/utvikler/creditor-mandate-api/)
     - [Bits, AvtaleGiro rules](https://www.bits.no/document/regler-om-avtalegiro)
2. **Fødselsnummer.** Default: not stored. The consumer is looked up by e-mail, phone and
   date of birth, which the aggregator route supports. A fødselsnummer would be a new
   class of personal data in Customers.
3. **The rate's source and day.**
   - Default: Norges Bank's daily mid rate (the open API at data.norges-bank.no). The
     rate is that of the issue date, or the latest business day before it, and a person
     may override it on the draft.
   - The legal rule for converting VAT, and its paragraph, is for the plan to confirm.
4. **Who owns the rates.** Default: Invoices, which needs them and does not require
   Accounting. They are offered as a contract slot that Accounting and Orders may read.
5. **Work in another currency.** Default: not in this phase. A foreign-currency document
   takes Orders' lines, quick and manual lines. Hours, expenses and consumption stay NOK
   documents. A consultancy billing NOK hours in EUR is the first follow-up.
6. **Reminder charges on a foreign-currency invoice.** Default: letters without charges.
   The statutory amounts are NOK and written for Norwegian debtors.
7. **Unrealised exchange differences at the year end.** Default: Accounting's, later. Only
   the realised difference on payment is posted.
8. **Customer credit.** Default: kept per customer in Invoices and posted by Accounting as
   a credit on the receivables account. A refund is registered after the money has left
   by other means: Vantigo moves no money out.
