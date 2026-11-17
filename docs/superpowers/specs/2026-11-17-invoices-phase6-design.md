# Invoices phase 6 — branding, the customer's balance, foreign currency, eFaktura and AvtaleGiro, sending in bulk

Revision 3, 2026-11-17: the owner chose the consumer provider (§15). This spec builds on the research
`docs/superpowers/research/2026-11-17-invoices-competitive-gaps.md` ("R"). It does not
reopen R §6, and it takes the defaults R §7 took for the readings. §14 says what an
overturn of each would change. Decisions are numbered I*n*. Earlier phases' decisions are
cited by their design, for example *phase 3 D11*.

Abbreviations:

- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `R/` is `docs/src/content/docs/en/reference/`.

Code references are against `main` at `a9f140c82` (latest migration `00192`).

## 1. Purpose

After five phases Invoices is ahead of its competitors on Norwegian law and on turning
work into invoices. It is behind them on what a buyer sees first:

- the invoice carries no logo;
- it cannot be in euro;
- a customer who pays two invoices at once, or a few kroner short, needs a person;
- a consumer cannot get it in the netbank.

Phase 6 closes those gaps in five deliveries, each of which stands alone.

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **A** | The logo and the phone on the PDF and the letters; an HTML mail with the logo and a personal message | — |
| **B** | The customer's balance: an overpayment kept as credit, applied to invoices, refunded; one bank line matched to several invoices by its remittance text; small differences rounded off | — |
| **C** | Foreign currency: Norges Bank's rates, the document in currency with VAT in NOK, its EHF and PDF, payments with the exchange difference, Orders' currencies invoiced, Accounting posting in currency | B (the payment's NOK amount, I13) |
| **D** | eFaktura and AvtaleGiro through a consumer-delivery port and its first adapter | — |
| **E** | Send all; the 2028 buyer org-number rule; credit limits | D for the eFaktura channel in send all (E ships without it if D is later) |

A, B and D do not depend on each other. The plan may run them in parallel, with
migrations renumbered at merge (see the memory *migration number collisions*).

**Not in this phase** (§13 says what each waits for):

- cards and a second payments provider;
- the customer portal;
- work, expenses and consumption in another currency;
- unrealised exchange differences;
- a template editor or colours;
- several legal entities;
- the corrected invoice;
- scheduled billing runs;
- a bank account in another currency.

## 2. Decisions — delivery A, branding

- **I1 — The logo is a setting.**
  - `invoices.settings` gains `logo_object_key`, `logo_sha256` and `logo_content_type`:
    all three or none (`ck_settings_logo`).
  - `PUT /invoices/settings/logo` (multipart, `invoices:manage`) takes a PNG or a JPEG of
    at most 512 KiB and 2,000 px on its longer side. It refuses with
    `logo_unsupported_type`, `logo_too_large` or `logo_too_wide`.
  - The file is stored under the module's scope as `branding/logo-{sha256}.{ext}`, so
    the key is content-addressed. `DELETE` clears the columns and never the object.
  - An object is never deleted while a document's snapshot names it. In this phase it is
    simply never deleted: logos are small.
- **I2 — The logo is snapshotted at issue, as the seller is.**
  - `invoices.invoices` gains `seller_logo_object_key` and `seller_logo_sha256`, written
    at issue from the settings and immutable after, under the trigger that guards the
    seller snapshot.
  - The stored PDF is rendered from the snapshot alone and never again (`pdf.go:438-439`), so an
    issued document keeps the logo it was issued with. A document issued before A has
    none.
  - A credit note takes the logo the way it takes the rest of the seller snapshot.
- **I3 — Where the logo goes.**
  - Top left of the PDF and of every reminder letter, fitted inside 45 × 20 mm with its
    proportions kept. The title moves right of it, and the seller and buyer block is
    unchanged.
  - maroto's image component takes the bytes. The fixed `/ModDate` (`pdf.go:33,55`)
    keeps the output reproducible.
  - A draft's preview uses the settings' current logo.
  - The EHF carries no logo.
- **I4 — The phone.**
  - `invoices.settings.phone` (optional, E.164-normalised as Customers does) and the
    snapshot `seller_phone`, printed in the seller block after the e-mail.
  - `contracts.SellerProfile` answers `Phone` and `LogoStorageKey` from Invoices
    (`srv/invoices/sales_tax.go:26-29` stops answering `""`). Accounting reads `Phone`
    already (`srv/accounting/saft_export.go:791`). Nothing reads `LogoStorageKey` yet;
    it is filled for Accounting's own documents later.
- **I5 — The mail is HTML too.**
  - Every document and reminder mail sets `HTMLBody` beside `TextBody`
    (`srv/mail/outbound.go:74`), rendered from one small template per mail kind with
    `html/template`.
  - The logo goes in as an inline attachment (`cid:logo`); the PDF stays attached. The
    text body is what it is today.
  - No external image, no tracking pixel, no link but the pay link.
- **I6 — A personal message.**
  - **Send by e-mail** takes an optional `message` (at most 2,000 characters, plain
    text). It is placed above the standard text in both bodies, escaped in the HTML.
  - It is stored on the delivery row (`invoices.deliveries.message`) and blanked when the
    customer is anonymised, as a payment's note is.
  - A's migration widens two triggers from `mig/00035`. `refuse_delivery_change` lets
    `message` change only to `''`, as it lets `recipient`. `guard_delivery_insert`
    blanks the message for an erased customer, as it blanks the recipient.
  - Reminder mails take no message.
- **I7 — No template editor.** The layout is fixed: what changes per tenant is the
  logo, the footer text and the phone. A colour and an editable template are later (§13).

## 3. Decisions — delivery B, the customer's balance

- **I8 — The credit ledger.**
  - `invoices.customer_credits` holds one row per movement of a customer's credit:
    - `customer_id`, `currency`, a signed `amount` (never 0) and its `amount_nok`;
    - `kind`;
    - the source, by kind (`ck_customer_credits_source`): `payment_id`,
      `bank_transaction_id`, `document_id` or `refund_id`;
    - the day, the registering user (NULL only for `credit_note`, which the issue
      writes), when, and a note.
  - The kinds:
    - `overpayment` (+): the part of a received amount above what was open;
    - `credit_note` (+): what a credit note credited beyond the open amount of an
      invoice (I10);
    - `applied` (−): used against an invoice (I11);
    - `refunded` (−): paid back outside Vantigo (I12);
    - `reversed` (±): the counter-row of a removal.
  - Rows are never changed or deleted. A removal writes `reversed`.
  - The balance per customer and currency is the sum, and never below zero
    (`credit_exceeds_balance`, checked under the customer's lock, I17a).
  - The customer slots take the table:
    - a merge re-points the rows and the balances add;
    - anonymisation blanks the notes and keeps the amounts;
    - `mergeKindNouns` gains `customer credits`.
- **I9 — An overpayment kept as credit.**
  - Registering a payment by hand may exceed what is open when the request says
    `keepRestAsCredit: true`. The payment row takes what was open, and an `overpayment`
    row takes the rest, both under the same reference. Without the flag,
    `payment_exceeds_open` stands (`payments.go:196-202`).
  - In the exception queue, a line queued `exceeds_open` or `invoice_settled` gains the
    action **Keep the rest as credit**, beside apply and dismiss. It applies what is open
    and books the surplus as `overpayment` on the invoice's customer, naming the line.
  - A dismissed surplus is "owed back", as today (`bankqueue.go:261-270`).
  - Automatic matching does not keep credit by itself: a surplus still queues
    (§14 Q4).
  - **The bank line's arithmetic counts credit.** `unappliedOf` (`bankqueue.go:290-302`)
    and the `allocation_exceeds_transaction` check (`:805-813`) subtract the line's
    payments' `amount_nok` (I13) **and** the `overpayment` rows that name the line. They
    stop subtracting `payments.amount`.
- **I10 — A credit note's surplus moves to the customer.**
  - When a credit note's issue leaves its invoice's open amount below zero, the surplus
    leaves the invoice: the invoice's open amount reads 0, and a `credit_note` row of the
    difference is written for the customer.
  - It is written in a **follow-up transaction** right after the issue commits, under
    I17a's order. The issue's own lock order (`issue.go:32-35`) is untouched.
  - The follow-up is idempotent per credit note, and the issue path retries it as
    `storeAfterIssue` retries the PDF (`pdfstore.go:406-424`).
  - `open` becomes `gross − credited − paid + moved`, where `moved` is the sum of the
    invoice's `credit_note` rows. That applies in `invoices.settled_amount()`,
    `credits.go:35-56` and `payments.go:91-99`. `refundDue` on the invoice disappears;
    the customer's balance replaces it.
  - The migration writes the row, unattributed, for every invoice whose open amount is
    negative today.
- **I11 — Credit applied.**
  - `POST /invoices/{id}/apply-credit` (`invoices:payments`) takes an amount of at most
    the lesser of the balance in the invoice's currency and what is open. In one
    transaction it writes two rows:
    - a payment of the new `source` `credit`, with no bank line;
    - an `applied` row naming it.
  - Removing that payment writes the `reversed` row.
  - The invoice page shows *The customer has NOK x in credit* with the action, when both
    the balance and what is open are above zero.
  - **Issue** offers *Use the customer's credit* (off by default). It runs as I10's
    follow-up transaction does, never inside the issue.
- **I12 — Refunds.**
  - `POST /api/v1/invoices/customers/{customerId}/credit/refunds` (`invoices:payments`;
    the path follows `reminder-policy`'s, `openapi/invoices.yaml:6798`) records money
    paid back: day, amount, currency, the bank account it left, reference, note. It
    writes a `refunded` row.
  - Vantigo moves no money out. A Vipps refund through the payments port's `Refund`
    (`srv/payments/payments.go:39`) is later (§13).
- **I13 — A payment knows the NOK it brought.**
  - `invoices.payments` gains `amount_nok`, NOT NULL, the NOK that arrived. C's foreign
    payments need it (I24); B builds it so that every credit row has a NOK value too.
  - The migration adds the column nullable. It replaces `refuse_payment_change`
    (`mig/00035:67-80`) with a version that allows only NULL → `amount`, backfills, sets
    NOT NULL, and restores the strict trigger, which now names the column.
  - A `BEFORE INSERT` default sets `amount_nok = amount` for a NOK payment.
  - `write_offs` gains `amount_nok` the same way.
- **I14 — One bank line, several invoices, matched automatically.**
  - Before a line is queued `exceeds_open`, `kid_unknown` or `no_kid`, the matcher reads
    its remittance for references:
    - camt.054's structured `RmtInf/Strd/CdtrRefInf/Ref`, every occurrence;
    - its unstructured `Ustrd`;
    - the bank feed's text.
  - It takes every valid KID it finds. It also takes every invoice number of this
    installation's series that stands as a whole token: bounded by start, end,
    whitespace or `,;/:#`, not inside a longer digit run, and not right after a currency
    code or before a decimal separator.
  - If those references name issued, open, NOK invoices of **one** customer whose open
    principal sums **exactly** to the line's amount, the line is applied across them,
    oldest due first, as the system (I15's actor), with match `auto_split`.
  - Any doubt queues as today, with the invoices found pre-filled as the suggestion:
    two customers, a sum that differs, a written-off or credited invoice.
  - Charges are never taken by an automatic split.
- **I15 — Small differences, by the existing rounding write-off.**
  - The module already has the write-off this needs: reason `rounding`, *a small
    difference* (`mig/00182:8-9,28`). Accounting posts it to the `rounding` role, 7790
    *Øreavrunding og små differanser* (`srv/accounting/chart.go:231`, rule
    `sales.write_off_rounding`, `srv/accounting/sales.go:45`). B adds no reason, kind or
    account; it adds a rule that writes one by itself.
  - `invoices.settings.tolerance` (numeric, NOK, 0 to 100, default 0, which is today's
    behaviour).
  - When a payment, by hand or from a bank file or the feed, leaves an invoice's
    principal open by more than 0 and at most the tolerance, the same transaction writes
    a `rounding` write-off of what is left. A Vipps capture never does, because it pays
    the reserved amount exactly (`payments.go:187-190`).
  - **The actor.** `write_offs.registered_by_user_id` becomes nullable with a new
    `source` (`manual` or `auto`), and `ck_write_offs_actor` requires a user exactly when
    the source is `manual`. A bank-feed payment already registers without a user, the
    same way.
  - **The state.** A standing `rounding` write-off reads `paid`, not `written_off`, in
    `invoices.invoice_state()` (`mig/00182:128`) and `state.go:55`. This is for every
    rounding write-off, the existing manual ones included: a small difference is not a
    loss. The reminder and the attention rules follow the state.
  - **A late residual.** If the customer later pays what was written off, the line
    queues `invoice_settled`, and a person keeps it as credit (I9). Recovered payments
    (`ck_payments_recovered`) stay for `loss` write-offs.
  - A surplus at or below the tolerance is not income. When a person applies the line,
    it is kept as credit (I9).
  - A foreign-currency invoice compares its residual in NOK at the document's rate.
- **I16 — Posting the balance, as journal facts.**
  - Accounting reads Invoices through `contracts.SalesJournal`, a seq-ordered fact log
    with `AsOfSeq` snapshots (`srv/contracts/sales_journal.go:211-249`). Credit joins
    that log; it is not a side list.
  - **The new fact kind is `FactCredit`.** It is one per `customer_credits` row: kind,
    customer, currency, amount, `amount_nok`, and the payment, line or document it came
    from. The `journal_facts` kind CHECK widens to take it.
  - **Accounting keeps one customer-credit open item per customer**: a new open-item
    kind `customer_credit`, on 1500, with no document.
    - Like a document's item, it is refused manual settlement
      (`settlement_source_document`, `R/accounting.md:285-289`).
    - The opening reads it from `OpenAt`, which gains the customer's credit balance
      beside its invoices' open amounts (`srv/accounting/opening.go:46`).
  - The postings, by kind:
    - `overpayment`: debit bank, credit the customer's credit item.
    - `credit_note`: a move from the invoice's item, whose negative open amount it
      clears (I10), to the credit item. Its total is zero on 1500.
    - `applied`: the payment of source `credit` posts **no bank line**. Accounting skips
      `bankLine` for it (`srv/accounting/sales.go:491-504`). The `FactCredit` posts the
      move from the credit item to the invoice's item. The money moves once.
    - `refunded`: debit the credit item, credit the bank account the refund names.
  - The aged receivables show the credit item as a negative amount, not due. Since I10
    zeroes the invoice's negative open amount, nothing counts twice.
- **I17a — The lock order.** Every path that writes credit takes its locks in one
  order, which extends phase 4 D18's (`locks.go:14-20`):
  1. the bank line, if any;
  2. the customer's advisory lock, or the two customers' locks in ascending id for a
     merge, taken after the merge's `LockCustomerDocuments` (`reminder_policy.go:93-95`);
  3. invoices, in descending id;
  4. settings and the counter.

  Issue never writes credit (I10, I11), so its order (`issue.go:32-35`) does not
  change. A races test covers each pair.

## 4. Decisions — delivery C, foreign currency

- **I17 — NOK stays the functional currency.**
  - `default_currency` remains NOK, and its refusal stands (`settings.go:215`).
  - `invoices.settings.currencies` (`char(3)[]`, default empty) lists the currencies a
    document may be in, drawn from those Norges Bank publishes. Turning one on needs a
    rate (I18) and the seller's IBAN and BIC (I22).
- **I18 — Rates, from Norges Bank.**
  - `invoices.exchange_rates(currency, rate_date, rate numeric(14,6), source, fetched_at)`,
    primary key (currency, rate_date):
    - `rate` is NOK per **one** unit. Norges Bank quotes some currencies per 100; the
      fetcher divides by its `UNIT_MULT`.
    - `source` is `norges_bank` or `manual`. A manual row is entered under
      `invoices:manage` and never overwritten by a fetch.
  - The worker `invoices-rates` runs on business days after 16:30 Oslo time. It fetches
    `https://data.norges-bank.no/api/data/EXR/B.{cur}.NOK.SP?format=sdmx-json&startPeriod=…`
    for the enabled currencies, through the guarded fetch client, at a host the config
    names (`INVOICES_RATES_URL`, that address by default). It is a lease worker with no
    hold-and-wait (see the memory *lease worker pool fix*).
  - Turning a currency on backfills 30 days. With the fetch off or failing, a manual
    rate is the only way to issue, and the settings page names the last fetch.
  - `contracts.ExchangeRates` (single provider, Invoices) answers `Rate(ctx, currency,
    day)` for Accounting and Orders, both optional readers.
- **I19 — The document's rate.**
  - A draft takes its currency from the request, else the customer's billing profile if
    that currency is enabled, else NOK.
  - The rate is the one of the issue date, or the latest business day before it with a
    rate, shown on the draft with its date and source. `invoices.invoices` gains
    `exchange_rate_source` (`norges_bank`, `manual` or `nok`).
  - A person may override the rate on the draft (`manual`). An automatic rate is resolved
    again at issue for the actual issue date.
  - The issue refuses `exchange_rate_missing` when no rate is found within 7 days before
    the issue date.
  - At issue `exchange_rate` and `exchange_rate_date` are fixed, and `queries/invoices.sql:140`
    stops defaulting the date alone.
  - A credit note keeps its original's currency and rate, as it does
    (`queries/credits.sql:11,20`).
- **I20 — What a foreign document may bill.**
  - Manual lines, Orders' order lines and subscription periods in the order's currency
    (`from_orders.go:122,434`: `currency_not_supported` and `currency_not_nok` give way
    to `currency_not_enabled`), and a-konto and the final settlement.
  - The billing worker stops skipping foreign orders (`billing_worker.go:416-447`).
  - Hours, expenses, milestones and consumption stay NOK-only: phase 3 D11 and
    `consumption.go:373` stand, and a NOK source on a foreign draft keeps today's
    refusal, `currency_not_nok`.
  - The quick invoice and payment on site stay NOK (Vipps).
- **I21 — Arithmetic.**
  - Lines, VAT and totals are computed in the document's currency, as today.
  - Per VAT row:
    - `BaseNOK` is `round(base × rate, 2)`.
    - `VATNOK` is `round(VAT × rate, 2)`, already in `docmath.Summarize`
      (`docmath.go:87-108`).
  - Line nets in NOK are `round(net × rate, 2)`. The remainder against their row's
    `BaseNOK` goes on the row's largest line, **within the VAT row**, as
    `srv/accounting/purchases.go:145` does. So lines sum to each row's `BaseNOK`, and
    SAF-T's per-rate sums hold.
  - **The receivable in NOK is defined** as the sum of `BaseNOK` plus the sum of
    `VATNOK`. It is never `round(gross × rate, 2)`.
- **I22 — Paying a foreign invoice.**
  - The PDF's payment block leads with the IBAN and the BIC and keeps the account
    number. Issue refuses `iban_required` without them.
  - A KID is still printed, since a Norwegian payer may use it.
  - No pay link is made, since Vipps is NOK: the action is hidden and `paylinks.go`
    refuses `currency_not_nok`.
- **I23 — The EHF in currency.**
  - `DocumentCurrencyCode` is the document's.
  - **Only when that is not NOK**, `TaxCurrencyCode` is `NOK`, since
    PEPPOL-EN16931-R005 forbids it to equal the document's currency. A second
    `cac:TaxTotal`, written **after** the one with subtotals, then carries only
    `cbc:TaxAmount currencyID="NOK"` with the VAT in NOK.
  - The precheck reads `First("cac:TaxTotal")` (`ehf/precheck.go:175-196`), so the order
    matters. It is widened to check the second total too.
  - `ehf.Doc` gains the NOK VAT. A NOK document's EHF is unchanged byte for byte.
  - PaymentMeans stays 30, with the IBAN as `PayeeFinancialAccount`.
- **I24 — Payments in currency.**
  - A payment's `amount` is in the document's currency (as today), and `amount_nok`
    (I13) is what arrived.
  - Registering by hand asks for both. `amount_nok` is proposed at the day's rate.
  - A NOK bank line (camt.054, the bank feed) can be applied from the queue to a foreign
    invoice. The person enters the currency amount it settles, and `amount_nok` is the
    line's share. `bankqueue.go:816-820` stops answering an internal error.
  - Automatic matching never pays a foreign invoice. A line whose KID names one queues
    with the new reason `currency_differs`.
  - Bank transactions stay NOK (`ck_bank_transactions_currency`). A currency account is
    later. A line's unapplied rest is in NOK (I9's arithmetic), whatever the currency of
    the invoices it paid.
  - **The exchange difference** is what is needed to keep the NOK receivable (I21)
    matching the currency open amount, as supplier payments treat paid as a balance of
    zero (`srv/accounting/supplier_payments.go:4-10`):
    - A payment that leaves the invoice partly open carries `amount_nok` minus
      `round(amount × document rate, 2)`.
    - The payment that brings the currency open amount to zero carries `amount_nok`
      minus the NOK receivable still open. That clears the NOK open item exactly, so no
      phantom 0.01 is left.
    - Write-offs and credit rows in currency take their NOK at the document's rate, and
      on the closing one take the NOK rest, by the same rule.
  - The difference is carried on the journal's payment fact.
- **I25 — Reminders on a foreign invoice.**
  - Letters go as for any invoice, without reminder fees or the B2B compensation, both
    NOK amounts written for Norwegian debtors (`reminderrules/fee.go`,
    `reminderrules/rates.go:13`).
  - Late-payment interest is still computed, in the invoice's currency at the Norwegian
    rate, and printed as such.
  - The hand-off to collection is allowed.
- **I26 — The PDF.**
  - Every amount carries the currency code.
  - Below the VAT rows the PDF prints the rate (*Kurs EUR/NOK 11,7234, Norges Bank
    16.11.2026*, or *avtalt kurs* when manual) and the VAT in NOK per rate and in total.
  - The plan cites the paragraph that requires VAT in NOK (§14 Q6).
- **I27 — The journal, Accounting and the export.**
  - `contracts.JournalDocument` gains `ExchangeRate` and, per line, `Net` in the
    document's currency beside `NetNOK`.
  - Accounting posts the receivable line with `currency`, `currency_amount` and
    `exchange_rate` (the columns exist, `mig/00181:244-246`). Revenue and VAT post in
    NOK.
  - A payment's exchange difference posts to `currency_gain` or `currency_loss`
    (8060/8160, as `srv/accounting/supplier_payments.go` does).
  - The CSV export gains currency, amount in currency and rate.
  - Stats sum in NOK at each document's rate.
- **I28 — Customers and Orders.**
  - A billing profile whose currency is not enabled draws a warning on the draft
    (`customer_currency_differs` stands).
  - Orders keeps its own prices per currency and may read `contracts.ExchangeRates` for
    a NOK equivalent in its analysis (optional, the plan decides).

## 5. Decisions — delivery D, eFaktura and AvtaleGiro

- **I29 — The port.**
  - `srv/consumerinvoice`, a platform package with no SQL and no module import, like
    `srv/payments`:
    - `Lookup(ctx, Consumer) (Address, Status)`;
    - `Send(ctx, Shipment) (Ref, error)`, for eFaktura, AvtaleGiro or both;
    - `Status(ctx, Ref)`;
    - `Cancel(ctx, Ref)`;
    - `Mandates(ctx, since)`.
  - **The first adapter is Mastercard Payment Services, direct** (the owner, 2026-11-17,
    §14 Q1). It uses MPS's Combo API (`addShipment`, eFaktura, AvtaleGiro or both, at most
    1,000 transactions a shipment), its Mandate API, and the eFaktura recipient lookup.
    No aggregator is in the flow.
  - **What that asks of whom:**
    - Vantigo becomes an MPS **submitter** (*innsender*). That is a commercial step with
      MPS before D's first live send, and is not code. The plan builds against MPS's
      test environment meanwhile.
    - Each tenant, as the issuer, has its own **eFaktura agreement** and, for
      AvtaleGiro, its own **AvtaleGiro agreement** with its bank, registered at MPS with
      Vantigo as submitter. The admin page (`admin/e-invoicing.md`) says how, and
      settings record the tenant's issuer id and agreement state.
    - The bank's fees are the tenant's.
  - The port stays provider-neutral, so an aggregator can be a second adapter later.
  - Its credentials are sealed as the access point's are, under `invoices:manage`, with
    a test connection.
- **I30 — Finding the consumer.**
  - A customer that is a person is looked up by e-mail, phone and, when given, date of
    birth. No fødselsnummer is stored (R §7.2).
  - The lookup's result is Invoices', since Invoices asks the provider: it is kept in
    `invoices.consumer_lookups` (customer, status `found` or `not_found`, the address,
    `checked_at`). Invoices does not write Customers' schema (module boundaries, rule 4).
  - A worker refreshes the lookup weekly, and again before a send when it is older than
    7 days. The customer slots merge it and erase it.
  - The date of birth is Customers' fact: a new optional field on a person, read through
    the billing profile and blanked by anonymisation.
  - Delivery preference gains `efaktura`.
- **I31 — eFaktura as a channel.**
  - **Send as eFaktura** sits beside e-mail and EHF, for NOK documents with a KID and a
    due date 3 days to a year ahead. It refuses `efaktura_not_found`,
    `efaktura_due_too_soon`, `efaktura_due_too_far` or `currency_not_nok`.
  - The transmission is kept and followed as an EHF's is (`transmissions.go`, its states
    and its worker), with `channel = 'efaktura'`.
  - Credit notes are not sent as eFaktura, since the network carries none: they go by
    e-mail or post, and the dialog says so.
- **I32 — The mandate KID, in a space of its own.**
  - AvtaleGiro identifies the payer by a KID fixed in their mandate. Invoices issues a
    KID per invoice: the invoice number padded to the agreed length less one, then the
    check digit (`srv/invoices/kid/kid.go:1-5`).
  - A customer KID built the same way from the customer number would collide: customer
    1234 and invoice 1234 would share a KID, and `invoiceOfKid` (`matching.go:332-353`)
    would put an AvtaleGiro payment on the invoice.
  - **The customer KID is therefore a separate space:**
    - the leading digit `9`, then the customer number padded to the agreed length less
      two, then the check digit by the agreement's algorithm;
    - invoice numbers are capped at the agreed length less two digits, so no invoice
      KID begins with `9`.
    - The plan checks every installation's series against the cap. Where one could
      reach it, §14 Q7's second agreement is the way out.
  - The mandates are stored as `invoices.avtalegiro_mandates(customer_id, kid, limit,
    status, …)`, synced daily from `Mandates`. A mandate whose KID names no customer is
    listed for a person to resolve.
  - **Matching runs the mandate rule first.** A KID with the leading `9` that is a
    mandate's resolves to that customer's claim due on the booking day with that amount,
    and from the claim to its invoice. It never reaches `invoiceOfKid`. Any other KID
    takes today's path. A `9` KID with no claim that fits queues as `kid_unknown`.
- **I33 — The claim.**
  - When an invoice to a customer with an active mandate is sent as eFaktura, the
    shipment is both, if:
    - the due date is at least 4 calendar days ahead;
    - the amount is within the mandate's limit.
    Otherwise it is eFaktura alone, and the send says why.
  - `invoices.avtalegiro_claims` records invoice, mandate, amount, due date and state
    (`submitted`, `accepted`, `rejected`, `cancelled`, `paid`).
  - A reminder does not run for an accepted claim until a business day after the due
    date passes unpaid.
- **I34 — Credit and the claim.**
  - A credit note on an invoice with a live claim cancels the claim through `Cancel`,
    when that is still possible (until the banking day before the due date).
  - After that the payment comes, and the surplus is credit (I10).
- **I35 — Fallback.**
  - Send all (I36) chooses, for a person, eFaktura if found, else e-mail, else the print
    batch. For a business it chooses EHF if the buyer can receive it, else e-mail. In both
    cases the customer's preference wins.
  - The per-invoice fee is the provider's. The settings page shows the count per month,
    and billing tenants for it is not this module's.

## 6. Decisions — delivery E, sending in bulk, the 2028 rule, credit limits

- **I36 — Send all.**
  - Selected documents on the invoices list, and every issued document of a billing run
    (phase 5 D12), can be sent in one action (`invoices:issue`).
  - The action enqueues one row per document in `invoices.send_jobs` (document, channel,
    state, attempts, error). The `invoices-send` lease worker sends them through the
    existing paths: e-mail, EHF, eFaktura when D is in, and the print batch.
  - The channel is chosen per document as in I35.
  - Idempotent per document and channel: a document already delivered on that channel
    is skipped and reported.
  - Progress shows through live updates, and the result lists each document's outcome.
- **I37 — The buyer's organisation number, from 2028-01-01.**
  - Customers derives `bookkeeping_obligated` from Brønnøysund (registered in
    Foretaksregisteret, or in the VAT register), with a manual override.
  - From 2028-01-01 the issue refuses `buyer_org_number_required` when the buyer is
    obligated and the snapshot has no organisation number. Before that day it warns.
  - The address alternative stays for buyers that are not obligated.
- **I38 — Credit limits.**
  - Customers' billing profile gains `credit_limit` (NOK, optional).
  - The exposure is open receivables (NOK, Invoices' `contracts` slot) plus confirmed,
    uninvoiced orders (Orders).
  - Orders' sales warnings gain `credit_limit_exceeded` at confirm, overridable as its
    warnings are. The draft and the issue in Invoices warn and never refuse.
  - The customer page shows the limit and the exposure.

## 7. Events

New events, in the module's event table and schema:

- `invoices.customer_credit.changed` (customer, currency, balance);
- `invoices.exchange_rate.fetched` (currency, day);
- `invoices.efaktura.transmission_changed`;
- `invoices.avtalegiro.claim_changed`;
- `invoices.send_job.finished`.

`invoices.payment.registered` gains `amountNok` and `source = credit`. Customers emits
`customers.credit_limit.changed`.

## 8. Permissions

No new key. Each action goes to the key whose kind of action it is:

- the logo, the phone, the tolerance, currencies and manual rates, and the consumer
  provider's credentials are `invoices:manage`;
- credit, refunds and payments in currency are `invoices:payments`;
- sending as eFaktura and send all are `invoices:issue`;
- reading rates, balances and claims is `invoices:access`.

The credit limit is Customers' billing permission. The catalog's descriptions say each
of these.

## 9. Data model

The migration numbers are claimed at merge.

| Delivery | Tables and columns |
| --- | --- |
| A | `settings.logo_object_key`, `logo_sha256`, `logo_content_type`, `phone`; `invoices.seller_logo_object_key`, `seller_logo_sha256`, `seller_phone`; `deliveries.message`, and its two triggers (I6) |
| B | `customer_credits` (with `amount_nok`); `payments.amount_nok` and `write_offs.amount_nok`, under the replaced triggers (I13); `payments.source` gains `credit`; `write_offs.source`, and `registered_by_user_id` nullable (I15); `invoice_state()` reads a rounding write-off as `paid`; `settings.tolerance`; the `journal_facts` kind `FactCredit`; Accounting's `customer_credit` open item; the backfill of I10 |
| C | `exchange_rates`; `settings.currencies`; `invoices.exchange_rate_source`; the queue reason `currency_differs` |
| D | `consumer_lookups`, `avtalegiro_mandates`, `avtalegiro_claims`; transmissions' `channel` gains `efaktura`; the consumer provider's sealed credentials; in Customers a person's date of birth |
| E | `send_jobs`; in Customers `bookkeeping_obligated`, its override and `credit_limit` |

Every new table follows the module's rules: an immutable history where money moves,
triggers that refuse the rest, and the customer slots' merge and anonymisation, whose
`mergeKindNouns` gain the new kinds.

## 10. Platform, registries and documentation

- **Workers:** `invoices-rates`, `invoices-send`, `invoices-efaktura` (the transmissions
  and the mandates). Each is registered with the host and gets a pool-of-one test.
- **Config:** `INVOICES_RATES_URL`. The consumer provider's base URL goes in its
  settings, as the access point's does.
- **Assistant:** new tour targets (logo, credit, rate, eFaktura) and `promptVersion`
  bumped.
- **Documentation** (AGENTS.md), in `en/` and `nb/` where bilingual:
  - `user/invoices.md`: branding, the customer's credit, foreign currency, eFaktura and
    AvtaleGiro, send all;
  - `reference/invoices.md`: every rule above;
  - `reference/accounting.md` and `user/accounting.md`: posting in currency, credit,
    small differences;
  - `user/customers.md` and `reference/customers.md`: eFaktura status, credit limit, the
    2028 fact;
  - `reference/orders.md` and `user/orders.md`: the credit-limit warning, foreign
    orders invoiced;
  - `admin/e-invoicing.md`: the consumer provider;
  - `admin/configuration.md`: `INVOICES_RATES_URL`.
- **The public site:** the Invoices page on vantigo.io gains currencies, eFaktura and
  AvtaleGiro, and branding, each when its delivery lands. These are things a customer
  can be sold on.

## 11. Edge cases

- A draft in EUR with no rate on a Monday after a Norwegian holiday: the latest business
  day with a rate, within 7 days (I19).
- A rate overridden by hand and the issue a day later: the manual rate stands. Only
  automatic rates are re-resolved.
- An invoice in EUR paid in two NOK transfers at different rates: each payment carries
  its own `amount_nok` and its own difference.
- A customer merged while holding credit: the credit rows re-point with the merge, and
  the balances add.
- Anonymisation: credit rows keep amounts and lose notes. Messages on deliveries are
  blanked.
- A credit note for a paid invoice whose customer is now disabled: the credit is still
  written (I10), and refund stays possible.
- An automatic split where one named invoice was paid an hour earlier: the sum no longer
  matches, so the line queues.
- AvtaleGiro claim accepted and the customer stops the payment: the claim becomes
  `rejected` on the status poll, and the invoice follows the ordinary reminder flow from
  its due date.
- A tolerance changed after a payment: it applies to payments registered after the
  change, never retroactively.
- Turning a currency off with open documents in it: allowed. Open documents keep their
  currency, and new drafts cannot take it.

## 12. Testing

Each delivery brings:

- unit tests of the arithmetic:
  - I21's remainder within each VAT row;
  - I24's difference, including the closing payment that clears the NOK rest;
  - I15's tolerance in currency;
  - I14's token rule against amounts, dates and account numbers in `Ustrd`;
- integration tests against Postgres for every new refusal code and every trigger;
- races tests for the credit lock order (customer before invoice) and the send jobs'
  lease;
- golden PDFs: a logo, a EUR invoice with VAT in NOK;
- EHF validation of a EUR invoice against the Peppol BIS 3 Schematron in CI, as the
  existing EHF fixtures are;
- a fake Norges Bank feed and a fake consumer provider, each with `FailOn` rather than
  `FailNext` (see the memory *Google fake FailNext vs token cache*);
- seed data: a logo, a EUR customer with an order and an invoice, a customer with
  credit, a person with eFaktura found and a mandate.

## 13. Later, and what each waits for

| Item | Waits for |
| --- | --- |
| Hours and expenses invoiced in another currency | C, then a rule for converting NOK work at the document's rate |
| Cards and a second payments provider | C, so foreign customers have something to pay |
| A bank account in another currency | demand, and Folio's or camt.054's currency accounts |
| Unrealised exchange differences at year end | Accounting's year-end |
| The customer portal | a customer identity in the platform |
| A template editor, colours | demand after A |
| Scheduled billing runs | E's send jobs |
| Refunds through Vipps | B; the payments port's `Refund` exists (`srv/payments/payments.go:39`) but has no Invoices flow |
| Several legal entities, the corrected invoice, Incoterms | demand |

## 14. Open questions

Each has the default this spec takes, and what an overturn changes.

1. **The consumer provider — decided 2026-11-17: Mastercard Payment Services, direct**
   (I29).
   - What the choice costs: Vantigo's submitter agreement with MPS, which is open
     before D goes live, and each tenant's own agreements with its bank.
   - What it avoids: a competitor (Visma's Maventa) in the flow, and an aggregator's
     margin.
   - **Still open for the plan:** which identifiers MPS's recipient lookup accepts. If
     it needs a fødselsnummer to find most consumers, Q2 comes back to the owner with
     the hit rate measured in MPS's test environment.
2. **Fødselsnummer.**
   - Default: not stored.
   - Overturned, the lookup hits more reliably, but Customers holds a national identity
     number. That needs its own access rule, its own encryption at rest and a DPIA note.
3. **Rate rule.**
   - Default: Norges Bank's mid rate of the issue date (I19).
   - Overturned to the delivery date or a customer-agreed rate, the draft gains a date
     field. The arithmetic does not change.
4. **Automatic credit.**
   - Default: a surplus on automatic matching queues (I9).
   - Overturned, a setting books the surplus as credit with no person, and the queue
     loses the case.
5. **A rounding write-off reads `paid`.**
   - Default: every standing `rounding` write-off, the existing ones included (I15).
   - Overturned, only automatic ones read `paid`, and the state needs the source as well
     as the reason.
6. **VAT in NOK.**
   - Default: printed (I26).
   - The plan confirms the legal source, bokføringsforskriften § 5-1-1 or
     merverdiavgiftsforskriften, and its wording.
7. **Customer KID.**
   - Default: a leading `9` under the same agreement, with invoice numbers capped below
     it (I32).
   - Overturned to a second KID agreement, the settings gain a second agreement, and
     phase 2's "several KID lengths on one agreement" is no longer needed.
8. **Delivery order.**
   - Default: A, B, C, E, then D. D's code can start now, against MPS's test
     environment. Its live send waits for the submitter agreement (I29).
   - The owner may want D first for Energy's consumers, as R §4.2 argues. D does not
     depend on B or C.

## 15. Revision log

- **1, 2026-11-17:** first revision, from R.
- **2, 2026-11-17:** after an adversarial review against the code:
  - B reuses the existing `rounding` write-off and its 7790 posting, and names its actor
    and its state (I15).
  - Credit becomes journal facts with an open item of its own in Accounting, and an
    applied credit posts no bank line (I16).
  - The lock order is written out, and credit is written outside the issue (I10, I11,
    I17a).
  - A bank line's rest counts in NOK and counts credit (I9).
  - The NOK receivable and the closing payment's difference are defined (I21, I24).
  - The customer KID moves to a space of its own, and the mandate rule runs first (I32).
  - The eFaktura lookup is kept in Invoices (I30).
  - The triggers that A and B must widen are named (I6, I13).
  - `TaxCurrencyCode` is written only off NOK (I23).
- **3, 2026-11-17:** the owner chose Mastercard Payment Services, direct, as the
  consumer provider (§14 Q1). I29 now names the APIs, Vantigo's submitter agreement and
  the tenant's own agreements. Q2 may come back with the lookup's hit rate.
