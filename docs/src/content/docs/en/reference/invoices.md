---
title: "Invoices module"
description: "The sales document: the seller record, gap-free numbering, VAT, issue and immutability, credit notes, the PDF, payments, sending, the journal, the export and retention."
sources:
  - apps/server/internal/invoices
  - apps/invoices/frontend
  - openapi/invoices.yaml
---
The Invoices module issues the sales document of Norwegian bookkeeping: a draft
becomes a numbered, immutable invoice or credit note, rendered to a PDF that is stored
once and downloaded as stored, and listed in a journal that proves the number series has
no gaps. Phase 1A built that
([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-09-26-invoices-foundation-design.md),
[research](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/research/2026-09-26-invoices-module.md)); phase 1B
([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-02-invoices-payments-delivery-design.md)) added
payments and the derived state of an invoice, sending a document by e-mail, the
accountant's CSV export and the dashboard's stats. Vantigo stays a sub-ledger: there is
no general ledger and nothing is posted — a payment here is a registration, not a
posting, and nothing is matched to a bank file.

> **Phases 1A and 1B do not meet the e-invoicing duties.** Invoicing the public sector
> has required EHF since 2019 (FOR-2019-04-01-444), and invoicing Norwegian businesses
> requires an e-invoice from **2027-01-01** (Lov 19. juni 2026 nr. 39). These phases
> issue PDFs, which a person hands over or the module e-mails — a PDF by e-mail is not
> an e-invoice; EHF over Peppol is phase 2. See [What comes next](#what-comes-next).

## The law in one page

Sources: bokføringsforskriften kap. 5
(<https://lovdata.no/dokument/SF/forskrift/2004-12-01-1558/KAPITTEL_5-1>) and EHF
Billing 3.0 Norway (<https://anskaffelser.dev/postaward/g3/spec/current/billing-3.0/norway/>).

- **Numbering (§ 5-1-3).** One machine-assigned series for invoices and credit notes,
  continuous across years, starting at the settings' `seriesStart`. A draft has no
  number; the number is allocated inside the issue transaction from a counter row, not
  a PostgreSQL `SEQUENCE`, so a refused or failed issue gives its number back. The
  start locks at the first issue (409 `series_locked`).
- **Immutability (§ 5-2-9).** An issued document is never changed or deleted — by the
  API (409 `invoice_issued`) and by the database: triggers refuse any change to an
  issued row but the merge's `customer_id` and the PDF columns set once, and any write
  to its lines or VAT summaries (SQLSTATE `P0001`, "invoices: issued document is
  immutable"). The child-row trigger reads the document `FOR SHARE` before it judges
  it, so a line written beside an issue that has not committed yet waits for it and is
  then refused. CHECKs hold the rest of the shape: an issued row carries `issued_at`,
  both parties' names and its rate date, an invoice has a due date exactly when issued,
  a credit note has none and no terms, and a credit note credits an original line at
  most once (`ux_lines_credit_once`).
- **Correction (§ 5-2-7).** A credit note in the same series reverses the original,
  in full or in part.
- **VAT per rate (§ 5-1-5, Peppol BR-CO-17).** VAT is computed per (category, rate) on
  the sum of the lines' nets, never per line, and every 0 % category gets its own row.
- **The issue date (§ 5-1-3 third paragraph).** Today, or the last day of the previous
  month while today's calendar day is 15 or less and the delivery ended on or before
  it. The regulation says "de femten første virkedager"; fifteen working days always
  reach at least the 17th, so **"calendar day ≤ 15" is stricter than the law** and needs
  no holiday calendar. On top of that no document may be dated before the latest issued
  one, so numbers and dates are both monotone — a guard the law does not ask for.
- **Late issue (§ 5-2-2).** More than a month after delivery the issue still succeeds
  and warns `issued_late`: refusing would leave the sale undocumented. It is judged on
  the day the document was actually issued (`issued_at`), not the date it carries: one
  issued on the 14th and dated the last of the previous month is judged on the 14th.
- **Delivery (§ 5-1-1 nr. 4).** A day or a period is required to issue; a place of
  delivery is optional and printed only when it is not the buyer's address. Whether the
  buyer address is enough as the place of delivery for services is **unconfirmed** — no
  Skatteetaten statement was read on it.
- **The parties (§ 5-1-2).** The seller's name and organisation number, followed by
  "MVA" when VAT-registered and "Foretaksregisteret" when registered there. The buyer's
  name and either a complete address or an organisation number (409 `buyer_incomplete`).
- **NOK only (§ 5-1-1 nr. 6).** VAT must be stated in NOK at the invoice date's rate;
  this phase issues in NOK and nothing else ("Only NOK in this phase"). The exchange
  rate columns exist, fixed at 1, dated at issue.

## The model

| Table | What it holds |
| --- | --- |
| `invoices.settings` | One row: the seller record (legal name, organisation number, VAT registration, Foretaksregisteret, address, bank account, IBAN/BIC, e-mail, footer), the default terms and currency, `series_start`, the seller's Peppol id `peppol_id`, and the KID agreement `kid_length` and `kid_algorithm` (a pair or both NULL, `ck_settings_kid`). |
| `invoices.counters` | The one counter row, `documents`; it exists exactly when something has been issued. |
| `invoices.vat_codes` | The tenant's codes: label, name, SAF-T code, UNCL5305 category, exemption reason, active. |
| `invoices.vat_code_rates` | Each code's rates as dated periods that never overlap (an exclusion constraint). A rate change is a new period, not a new code. |
| `invoices.invoices` | Drafts and issued documents: kind, status, number, customer, delivery, references, notes, the buyer snapshot and the seller snapshot (written at issue), the totals, the stored PDF's key and SHA-256, and an invoice's `kid` with the `kid_algorithm` it was computed with (set at issue, both or neither, never on a credit note). |
| `invoices.lines` | Description, quantity (3 decimals), unit, unit price (4), discount (2), VAT code, the computed gross, allowance and net, the credited line on a credit note, and the VAT snapshot written at issue. |
| `invoices.vat_summaries` | An issued document's VAT per (category, rate) with its SAF-T code and reason. |
| `invoices.payments` | Money received against an issued invoice: the day it arrived, the amount and the currency (the invoice's, copied), the bank's or the payer's reference, a note (`''` once the customer is anonymised, and on every registration made after), who registered it and when, and — once removed — when, by whom and why. Never deleted; never changed but by the removal, once, and that blanking. |
| `invoices.deliveries` | One row per e-mail that handed an issued document over: the recipient (`''` once the customer is anonymised), the subject, the Message-ID, the SHA-256 of the PDF attached, when and by whom. Never deleted; never changed but by that blanking. |
| `invoices.erased_customers` | The customers this module has anonymised, by id, with when: the marker a send and the delivery, payment and transmission triggers read. Never removed. |
| `invoices.access_point_credentials` | One row (`id = 1`): the access point provider (`storecove`), its settings that are not secret (`settings_json`), the API key sealed by the secrets box, `rejected_at` once the provider refused the key, and `updated_at`. Kept off the settings row every issue reads `FOR SHARE`. |
| `invoices.transmissions` | One EHF transmission of an issued document: the provider, the idempotency key, the sender's and receiver's Peppol ids, the document type and process, the submitted UBL's object key and SHA-256 and the PDF's SHA-256, the status (`queued`, `submitted`, `delivered`, `failed`, `unconfirmed`, `cancelled`), the provider's reference, the evidence's key and SHA-256, the attempt counters and the next attempt, the crash marker `submit_attempted_at`, the worker's lease, the last error, the receiver lookup it was queued under, the timestamps of each state, and a person's resolution. Never deleted; only its state columns change, a failed or cancelled row not at all, and a delivered row only its lease, cadence and — once — its evidence. A trigger refuses one under a draft (`invoices: a transmission needs an issued document`) or for an anonymised customer (`invoices: the customer is anonymised`); `ux_transmissions_active` allows one queued, submitted, delivered or unconfirmed transmission per document. |

A document's state is not a column: `invoices.document_state(...)` derives it, see
[Payments and the state of an invoice](#payments-and-the-state-of-an-invoice).

Phase 2's schema (`00036_invoices_ehf_kid.sql`, the
[design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-03-invoices-ehf-peppol-kid-design.md))
adds the Peppol id, the KID agreement, the document's KID and the two tables in one
migration; sending EHF itself is not yet wired to any endpoint.

The seeded codes, each from 2026-01-01: `3` 25 %, `31` 15 %, `32` 11.11 %, `33` 12 %
(all S), `5` Z, `51` AE, `52` G, `6` **E** (unntatt, mval. kap. 3) and `7` **O** (a seller
outside the VAT register) at 0 %.

**Money.** Amounts arrive as JSON numbers and are read as the decimal text they were
written as, never as a binary float. A line's gross is quantity × unit price rounded to
øre, its allowance the discount of that rounded gross, rounded, and its net the
difference — gross less allowance, as EHF expresses a discount. Every rounding is two
decimals, the half away from zero; there is no øre rounding of the total. A line is at
most 999 999 999.99, a document 99 999 999 999.99 gross, and at most 500 lines.

## The Peppol id and the KID agreement

Both live on `PUT /settings` (`invoices:manage`), beside the seller record, and are
**required and nullable** on it: a body without `peppolId`, `kidLength` or
`kidAlgorithm` is a 400 on that field, so a client that predates them cannot clear them
by leaving them out; null is a value.

**The seller's Peppol id** is the sender's address on the Peppol network, read when a
document is sent and never part of the seller snapshot. It is a four-digit scheme, a
colon and 1-50 letters, digits or hyphens (`0192:974760673`), as the customers module
validates a buyer's; a `0192` id must be the seller's own organisation number. Null or
empty defaults it to `0192:` and the organisation number when that is set, and to null
without one. The migration backfilled it the same way from a valid organisation number,
so an existing installation is ready without re-saving.

**The KID agreement** is the pair the bank agreed: `kidLength`, 4-25 digits including
the check digit (the OCR giro's rule), and `kidAlgorithm`, `mod10` or `mod11`; both or
neither (400 on the missing one). Saving is refused with a 400 on `kidLength` when the
next number to be issued does not fit in `kidLength − 1` digits — the counter's next
value, or the request's own `seriesStart` before the first issue — judged under the
settings row's lock. The response warns `kid_headroom_low` (on `GET /settings` too) when
fewer than two digits remain to spare, a hundredfold growth. Clearing or changing the
pair is allowed: every issued invoice keeps the KID and the algorithm it was issued
with.

**The KID** of an invoice issued under the agreement is its number zero-padded to
`kidLength − 1` digits, then the check digit: MOD10 is Luhn (weights 2 and 1 from the
right, each product's digits summed, `(10 − sum mod 10) mod 10`), MOD11 weights 2-7
repeating from the right, `11 − (sum mod 11)`, 0 for a remainder of 0 and **`-` for a
remainder of 1**, as the specification prescribes (`12345678` is `123456782` under MOD10
and `123456785` under MOD11). It is computed after the number is allocated and stored
with its algorithm in the draft→issued update, frozen with the rest of the document. A
credit note never gets one, nor an invoice issued before the agreement. Every render
re-verifies the stored KID against the **stored** algorithm and the number, never the
agreement in force; one that does not verify is a 500 logged at error.

## Drafts

A draft is created for a customer id; its buyer is read through
`contracts.CustomerDirectory.BillingProfile` before anything is written. An omitted
`yourReference` is the profile's buyer reference and omitted terms are the profile's,
else the settings' default. The customer gates, in order: merged away (409
`customer_merged` with `mergedInto`), archived — which includes an anonymised person —
(`customer_archived`), disabled — "blocked for invoicing" — (`customer_blocked`), and no
customer at all (`customer_missing`). They run on create, on every save and again at
issue; never on a credit note, and never on a read. An invoice draft's totals are
computed with the rates in force today — a line whose code has no rate period covering
today counts at 0 % and the draft warns `vat_code_not_valid`, which the issue would
refuse — and the issue computes them again for the issue date. A credit-note draft is
totalled at its original lines' rates, in its response and its preview alike. Warnings
never refuse: `customer_currency_differs`, `issued_late` (never on a credit note, which
keeps its original's delivery and is late by nature), `vat_code_not_valid`,
`credit_exceeds_invoice`, `credit_exceeds_line` — a credit draft over both caps carries
both.

In the app's editor, the totals shown while a draft is being worked on are computed in
the browser by this same rule and are an estimate; the figures the server actually
saves — and, for the last credit note against an invoice, the exact reconciliation
under [Credit notes](#credit-notes) — are always the server's own computation, run
again server-side on save.

## Issuing

`POST /invoices/{id}/issue` runs one READ COMMITTED transaction in a fixed order:
lock the document, share the settings row, allocate the number, and only then check
every rule — the counter row is what serialises two issues, so every check that
depends on other documents runs after it. The directory is read before the transaction
and the object store is used after it; neither is ever called under a lock. The lock
order is always document → settings → counter → original, and nothing takes them in
another order: `PUT /settings` takes only the settings row, the rate operations the
settings row and then the VAT code, `PUT /vat-codes/{id}` only the code, a payment's
registration or removal only its invoice, and a send's delivery row only its document,
`FOR SHARE`; no issue locks a code. The merge holder locks the documents it re-points
**newest first** before it writes them: a credit note's issue holds the credit note and
then locks its older original, and an UPDATE alone could lock the original first, a
deadlock. This is the module's one lock invariant, and every multi-row lock inside it
keeps to it: **take locks in descending id**, which is the same rule as "a credit note is
always newer — holds a higher id — than the original it credits", stated twice; any
future path that locks more than one row of `invoices.invoices` at once must keep both
true.

The checks, each a 409 that rolls the number back: `seller_incomplete`, `no_lines`,
`delivery_date_missing`, `issue_date_not_allowed` (with `allowedIssueDates`); for an
invoice the customer gates, `buyer_incomplete`, `vat_code_inactive` and
`vat_code_not_valid` (with `linePosition`), `vat_not_registered` (a seller outside the
register issues only O lines), `category_o_not_allowed` (a registered seller issues no O
line), `reverse_charge_needs_org_number`, `vat_codes_ambiguous` and, under a KID agreement,
`kid_length_exceeded` (the allocated number no longer fits a shortened agreement); for a credit note
`credit_exceeds_line` (with `linePosition`) and `credit_exceeds_invoice`. Before the
transaction: `invoice_issued`, and 503 `storage_unavailable` when no object store is
configured — an issued number whose PDF could never be stored is not allowed to exist.
A Norwegian business's organisation number reaches the buyer snapshot only from this
release on: before it, the snapshot compared the directory's lowercase country
case-sensitively, so a document issued to one then carries `buyer_foreign_id` `no…`
instead — snapshots are immutable, so credit and re-issue one where it matters
([upgrading](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/README.md#upgrading)).
A merge that re-points the draft between the directory read and the lock is
`invoice_changed`.

## Credit notes

`POST /invoices/{id}/credit` makes a credit-note draft of an issued invoice: the
customer, currency, rate and the date it was taken on, delivery, references and the
**buyer snapshot** are copied — no directory is read, so an anonymised customer's
correction names the person the original named — with every line, its VAT code and
the line it credits. A credit draft
may remove lines, lower a quantity or a unit price, and edit a description and the
notes; anything else is a 400 on the field. The caps are decided at issue under the
original's lock: per original line, the quantity and net credited by the issued credit
notes and this one; and this one's gross against what the invoice has left. A credit
note skips every customer gate, the VAT active and validity checks and the registration
rules — it reverses the original's treatment at the original's rates — and keeps the
issue-date rule, `seller_incomplete`, `no_lines`, `delivery_date_missing` and both
caps. It has no due date and no payment block. Refusals: `invoice_draft`,
`credit_note_not_creditable`, `invoice_fully_credited`. The cap is common practice, not
law.

**Squaring the øre, per line.** A credit note rounds as any document does: each line on
its own quantity and price, the VAT per (category, rate) row on the sum of its lines'
nets, half away from zero. So partial notes' roundings do not in general sum back to
what was charged — three returns of one unit of a 3 × 31.66 line are not 94.99 until
something squares them. Squaring is **per line**: the credit that returns a line's last
unit — with the issued credit notes before it, the line's whole quantity credited, and
every credit of that line, theirs and this one, a return at the line's own unit price
and discount — takes that line's *remaining* gross, allowance and net (the original's
less what the issued notes credited on it), whatever the note's other lines do. The
**final** credit note — every line of the invoice returned in full that way, each by an
earlier note's last return or by one of this note's — also takes each VAT row's VAT and
NOK VAT as charged less what the issued notes reversed on that row; when an earlier
partial note over-reversed a rate — its own rounding took more than that row's fair
share — the final note carries a row at that rate with a taxable amount of 0.00 and the
small negative VAT remainder. It prints on the PDF as it is, and the EHF phase (2) must
be able to carry a negative VAT row too. A price reduction ("prisavslag") or a higher
discount is never squared: its credit was a choice, not a rounding, and squaring after
it would credit the reduction again. Whatever is squared, every note's line nets sum to
its net total (EN 16931 BR-CO-10), so no credit note is one an EHF could not carry.

**A free line needs no return.** An original line with no money in it — "Frakt 0,-" —
does not have to be credited for the last note to be the final one: leaving it out of
the last note (the natural edit) still squares the VAT rows, where it would otherwise
leave an øre that the headline cap then refuses.

**A price reduction uses up the line's quantity.** The per-line cap counts quantity as
well as net, so a prisavslag credit of a line's full quantity at a lower price uses the
line's whole quantity cap: a later return of goods on that line is refused
`credit_exceeds_line`. The workaround is to return the goods first and then credit the
reduction only on the units that are kept, so the cap still has room for both. The cap is
kept as it is because it is what stops one line at 25 % being reversed twice while a 0 %
line is never credited, which would misstate the VAT return per SAF-T code.

**A draft's stored totals can lag.** A credit-note draft's totals as stored — what the
list and an invoice's `creditNotes` show — can lag an øre behind once a sibling credit
note against the same invoice issues and changes what a line has left; the draft's own
page, its preview and its issue always total it afresh.

## Payments and the state of an invoice

**A payment is a registration.** `POST /invoices/{id}/payments` records money received
against an issued invoice: `paidOn`, the day it arrived — on or after the invoice's issue
date and not after today (Oslo, from the server's clock) — `amount`, above 0 with at most
two decimals and at most 99 999 999 999.99, and optionally `reference` (the bank's or
the payer's, at most 100 characters) and `note` (at most 500), both trimmed. The
currency is the invoice's, copied, never chosen. Every field that fails is named in one
400. A credit note takes no payment, draft or issued (409 `credit_note_no_payments`,
judged before the status), and an invoice draft none either (409 `invoice_draft`). No
directory is read and no customer gate runs: the customer may be disabled, archived,
merged or anonymised since, and the money arrived regardless.

**Never more than is open.** The registration locks the invoice `FOR UPDATE` — the only
row it locks — and only then reads what its issued credit notes credit and what its live
payments paid. **The open amount** is gross − credited − paid. Nothing open (open ≤ 0) is
409 `invoice_settled`; an amount above the open amount is 409 `payment_exceeds_open`,
whose problem carries `openAmount`. An overpayment would be a customer credit balance,
which is phase 4; refusing it keeps payments alone from taking the open amount below
zero. A payment of exactly the open amount is accepted, and the invoice is then `paid`.
The response is the document, with its new state, open amount and payments.

**The lock a registration and a credit issue share.** A credit note's issue locks its
original last, after the counter ([Issuing](#issuing)); a registration locks that same
row. The two serialise on it, and each reads its figures after the lock — under READ
COMMITTED every statement after the lock sees every commit before it — so the credit's
caps and the payment's are each judged on figures the other cannot change underneath
it. **A credit note after a payment is allowed** and may take the open amount below
zero: the invoice then answers **`refundDue`**, −open, what is owed back. Refunds are not
a flow in this phase (phase 4); this is the figure, not a payout. Two registrations of
the whole open amount racing each other give one 200 and one `invoice_settled`. A
retried registration of a partial amount registers twice — two receipts are what the
body says — and no idempotency key is taken in this phase.

**Removal, never deletion.** `POST /invoices/{id}/payments/{paymentId}/remove` takes a
`reason` — 1 to 200 characters once trimmed, judged before anything is read — locks the
invoice first and reads the payment after the lock, so two removals of one payment give
one 200 and one 409 `payment_removed`, and sets `removed_at`, `removed_by_user_id` and
`removal_reason` together. A payment that is not that invoice's is a 404. A removed
registration counts for nothing but keeps its row: the document answers every payment,
removed ones included with their removal, in the order the money arrived. A removal is
never undone, and a payment is never edited: a mistake is removed with a reason and the
payment registered again.

**Why a row never leaves.** A registration is kept as long as the document it is
registered against — bokføringsloven § 13, five years after the end of the financial
year, as this module reads it for the document. That reading is **unconfirmed**, as the
document's own is ([Retention and personal data](#retention-and-personal-data)): the
2027 wording of § 13 is unread, and the research (§ 2.5) speaks of the sales
documentation, not of payment registrations by name. So nothing deletes one. Two
triggers hold it in the database: `tr_payments_immutable` refuses every DELETE and every
UPDATE but two writes, which one statement may make together — the removal, which sets
the three removal columns from NULL, once, and an anonymisation's blanking of the note
to `''` — with the rest of the row unchanged ("invoices: a payment registration is
immutable", SQLSTATE `P0001`); `tr_payments_parent` reads the document and its current
customer `FOR SHARE` on INSERT, refuses a row under anything but an issued invoice
("invoices: a payment needs an issued invoice"), and then writes the note as `''` when
the customer has been anonymised — so a registration that raced the erase, or came
after it, never keeps a staff note about the person. A CHECK holds the three removal columns together and the
reason non-empty, another the amount above 0, and the foreign key is `ON DELETE
RESTRICT`. The API refuses first, with its own codes; the triggers are the floor.

**The state is derived, never stored.** Judged against today in Oslo, with `credited`
the issued credit notes' gross (0 for a credit note) and `paid` the live payments' sum,
the first match wins:

| State | When |
| --- | --- |
| `draft` | a draft, invoice or credit note |
| `issued` | an issued credit note |
| `credited` | an issued invoice with `credited > 0` and `credited ≥ gross` |
| `paid` | gross − credited − paid ≤ 0 |
| `overdue` | the due date is before today |
| `partially_paid` | something is paid |
| `open` | otherwise |

The order is the rule: a fully credited invoice is `credited` even when it was paid
first (the money is then a refund due); a paid invoice is never `overdue`; a late partial
payment is `overdue`, not `partially_paid` — overdue is the fact that matters. An invoice
is not overdue on its due date, and is from the day after. `credited > 0` keeps an
invoice of free lines only (gross 0, which can never be credited) out of `credited`: it is
`paid`, nothing being open. The rule lives once in SQL, `invoices.document_state(kind,
status, gross, credited, paid, due_date, today)`, `IMMUTABLE`, with `today` always a
parameter from the server's clock and never `CURRENT_DATE`; the list and the stats filter
with it. `documentState` in `state.go` is its Go mirror, for one response at a time, and
a test runs every combination through both.

Every document answers `state`. An issued invoice also answers `paidAmount`,
`openAmount` (which may be negative), `refundDue` only while the open amount is below
zero, and `payments`; a draft and a credit note answer none of them — money is absent,
not null, where it does not apply. `GET /invoices?state=` takes one of `open`,
`partially_paid`, `overdue`, `paid` or `credited` — anything else is a 400 — combines with
the other filters, and only an issued invoice can match it. Each list item answers
`state` and, on an issued invoice, `openAmount`. The list's order is unchanged: there is
no sort by state.

## The PDF

**The currency is on the page** (§ 5-1-1 nr. 6): the line amounts' header reads
"Beløp (NOK)" / "Amount (NOK)", the VAT column "MVA (NOK)" / "VAT (NOK)", and the
amount to pay "NOK 15 045,00" — the document's own currency, which is NOK only in this
phase.

Rendered with maroto v2 (pure Go; the runtime image has no fonts) in **Noto Sans**,
embedded, under the SIL Open Font License 1.1 — its text is
`apps/server/internal/invoices/fonts/LICENSE`. A document prints in its buyer's language
(nb, or en), except the reverse-charge text "Omvendt avgiftsplikt – Merverdiavgift ikke
beregnet", which is the regulation's own wording and printed in Norwegian always.

**Store-once is what makes it lawful, not determinism.** Right after an issue commits,
the PDF is rendered from the document's own rows and snapshots only — never the
settings, the directory or the VAT tables — hashed, and put under
`documents/<id>/<number>-<sha256>.pdf` in the `invoices` scope (physically
`invoices/documents/…`), and the row records the key and hash once. A store failure there
never fails the issue: the response says `pdfStored: false` and the first download — or
the first send — stores it. Every download streams the stored object, verified against
its hash; a document whose hash is set is never rendered again, and the module never
deletes an object. A stored object that is gone or no longer matches its hash is a 500
logged at error — an operator problem, never papered over. A first download that cannot
reach the store is a 503 to retry; one that cannot render the document is a 500, since
retrying does not mend it. Reproducible bytes are a nice-to-have: catalog sorting and a
fixed modification date are set process-wide and the creation date is the issue instant,
but the bytes may change with a maroto, gofpdf or font upgrade; stored PDFs never do.

**The payment block** of an invoice lists the account, its KID when it has one (the
"KID" line, verified as above), the IBAN and BIC when set, and the due date; the note
under it asks for the KID ("Vennligst bruk KID ved betaling" / "Please use the KID with
your payment") when there is one, and for the invoice number otherwise.

`GET /invoices/{id}/preview.pdf` renders a draft on demand with the watermark
"UTKAST — ikke et salgsdokument", no number, today's date, the current settings and,
for an invoice draft, the customer's current profile at today's rates; a credit-note
draft keeps its copied buyer and its original lines' rates. It is never stored. The app
opens it in a new browser tab — opened with the click, before the PDF is fetched, so
the browser's pop-up blocker never sees a `window.open` outside a click — and falls
back to a plain download when the tab could not be opened at all.

## Sending a document

`POST /invoices/{id}/send` e-mails an issued document's **stored PDF** — an invoice's or
a credit note's — to the customer, now, and logs it. It is synchronous: no outbox and no
worker, and a failure is the caller's to see. It needs `invoices:issue`
([Permissions](#permissions)); the body is `{recipient?}`. In order:

1. **503 `mail_unavailable`** when the installation's mail driver is not `smtp`, judged
   before anything is read. The `log` driver delivers nothing and is allowed only in
   development ([email delivery](/en/admin/authentication/#email-delivery)), so this
   is a development installation's answer.
2. 404; 409 `invoice_draft` on a draft.
3. **409 `customer_anonymised`** when this module has anonymised the document's customer
   (the marker, below): a person who has been anonymised is not written to again, at an
   override's address or any other.
4. **The recipient**: `recipient` when given — a bare address that parses to itself, at
   most 254 characters once trimmed, so `"Name <a@b>"` is a 400 on `recipient` — else
   the invoice e-mail of the customer's **current** billing profile, read from the
   directory outside any lock. A credit note reads the profile too: it goes to the same
   buyer at today's address, not the snapshot's, because a person's mailbox changes and
   the document does not. No address is 409 `no_invoice_email`; a directory that fails
   here is a 500.
5. **The PDF**, through the download's own path: stored first when it never was, and
   otherwise read and verified against its hash, so a send never attaches bytes the store
   does not hold — 503 `storage_unavailable`, and a 500 for a stored object gone or
   altered or a document that cannot be rendered, as `GET /{id}/pdf` answers.
6. **The envelope.** From is the installation's `SMTP_FROM` under the display name of
   the document's **seller snapshot** — the legal name the PDF prints. **Reply-To is the
   current settings' e-mail** — replies should reach today's mailbox, not the one the
   document was issued under — and there is none when the settings have none (the
   platform's `mail.Outbound.ReplyTo`). To is the recipient, alone. The Message-ID is a
   fresh `{uuid}@vantigo.invalid`, bare. The PDF is attached as `application/pdf` under
   the download's own name — `faktura-1001.pdf`, `invoice-1001.pdf`,
   `kreditnota-1002.pdf`, `credit-note-1002.pdf`. The body is plain text in the
   document's language, below: no HTML, no logo, no template, no personal message.
7. **The send**, through the platform's guarded SMTP client with the `SMTP_*`
   configuration. **A failure is 502 `mail_failed` and records nothing**; its detail says
   the mail may still have been delivered if the server timed out, so check before
   sending again. The error is logged at warn with the document id and never put on the
   wire.
8. **The log**: one row in `invoices.deliveries`. The answer is the document, its
   `deliveries` holding the row and `sendDefaults.warnings` the send's warnings.

**Three contexts.** The reads before the send run on the request's context and stop with
it. The send runs on a context the request's cancellation does not reach, bounded at
**30 seconds**: a browser that goes away must not abort a transfer the mail server may
already have accepted. The delivery row is written on an uncancellable context of its
own, bounded at **5 seconds** — never what is left of the send's 30, so a send accepted at
the 29th second is still logged — and the response is rendered uncancellable too,
bounded at 5 seconds of its own, so a caller that went away gets no error-level log for
a send that succeeded.

**The timeout caveat.** A send that times out after the mail server has taken the data
answers 502 `mail_failed` and logs nothing, though the mail may have gone. And the row's
five seconds include the delivery trigger's lock wait: the insert waits on the document
behind an anonymisation that holds the customer's documents
([Retention and personal data](#retention-and-personal-data)), so an erase that holds
them longer than five seconds leaves a sent mail with no row. A row that cannot be
written after a successful send — for that reason or any other — is a 500 and an
error-level log line naming the document id only, never the recipient, whose address may
be a person's being anonymised at that moment: the mail went, and the operator is told.

**Re-sending.** Sending twice is allowed and logged twice: a re-send is a legitimate act.
No suppression list is read — `communications.suppressions` is another module's table
([module boundaries](/en/contributing/module-boundaries/), rule 4). **A bounce goes to the envelope
sender**, the installation's `SMTP_FROM`, not to the seller's Reply-To, and Vantigo
records none: a delivery row means the mail server accepted the mail, not that it
arrived. Point `SMTP_FROM` at a mailbox someone reads if bounces matter
([deploy/compose/README.md](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/README.md)). There is no bulk send.

**The rate limit.** 60 sends per client per 10 minutes (the policy `invoices-send`):
with an arbitrary override the endpoint is an authenticated relay through the
installation's SMTP server, and a limit is cheap. It is counted per client IP address —
an office behind one NAT shares one budget — before the access check, so every request
to the endpoint counts, a refused one too. Over it is a 429 in the limiter's shape
everywhere in Vantigo — `application/json` `{"error": {"code": "rate_limited",
"message": …}}` with `Retry-After` — never a problem document.

**The texts.** A document whose buyer language is `en` is written in English, every other
in Norwegian (nb). `{number}` is the document's number, `{seller}` the seller snapshot's
legal name, `{amount}` the gross and `{open}` the open amount at the send, both as the PDF
prints money (`NOK 15 045,00`, `NOK 15,045.00`), `{due}` the due date as the PDF prints a
date (`31.10.2026`, `2026-10-31`), `{account}` the snapshot's bank account, `{iban}` and
`{bic}` its IBAN and BIC, and `{original}` the number of the invoice a credit note
credits.

Invoice, nb — subject `Faktura {number} fra {seller}`:

```text
Hei,

Vedlagt følger faktura {number} fra {seller} på {amount}, med forfall {due}.
{the payment paragraph}

Med vennlig hilsen
{seller}
```

Invoice, en — subject `Invoice {number} from {seller}`:

```text
Hello,

Please find attached invoice {number} from {seller} for {amount}, due {due}.
{the payment paragraph}

Kind regards
{seller}
```

Credit note, nb — subject `Kreditnota {number} fra {seller}`:

```text
Hei,

Vedlagt følger kreditnota {number} fra {seller} på {amount}, som krediterer faktura {original}.

Med vennlig hilsen
{seller}
```

Credit note, en — subject `Credit note {number} from {seller}`:

```text
Hello,

Please find attached credit note {number} from {seller} for {amount}, crediting invoice {original}.

Kind regards
{seller}
```

**The payment paragraph** of an invoice follows the open amount at the send, so a
re-send never asks for money that is not owed; a credit note has none:

| Open at the send | nb | en |
| --- | --- | --- |
| the whole gross | `Beløpet betales til kontonummer {account}. Merk betalingen med fakturanummer {number}.` | `Please pay {to}, quoting invoice number {number}.` |
| above 0, below the gross | `Utestående beløp er {open}, som betales til kontonummer {account}. Merk betalingen med fakturanummer {number}.` | `The outstanding amount is {open}; please pay it {to}, quoting invoice number {number}.` |
| 0 or below (paid or credited) | `Fakturaen er gjort opp. Det er ingenting å betale.` | `The invoice has been settled. Nothing is due.` |

An invoice with a KID is marked with it instead of its number: `Merk betalingen med KID
{kid}.` / `quoting KID {kid}.` in the same sentences.

**The IBAN form.** In English `{to}` is `to IBAN {iban} (BIC {bic})` when the snapshot has
an IBAN — the BIC in brackets only when it has one too — and `to account {account}`
otherwise: an English-language buyer is usually abroad and cannot pay a domestic
account. The Norwegian text always names the account. A seller without a bank account
cannot have issued (`seller_incomplete`), so the account is always there.

**The four warnings** are never refusals: a send is never refused for one. They are on
`sendDefaults.warnings` and on the send's own response, and the server judges each
against today in Oslo from its own clock — never the browser, which has neither, and a
code is what a test with a fixed clock can pin.

| Warning | When |
| --- | --- |
| `delivery_preference_ehf` | the customer's current billing profile says `ehf`: the customer expects EHF, and an e-mailed PDF does not meet the e-invoicing duty |
| `delivery_preference_other` | the profile says `efaktura` or `paper` |
| `buyer_norwegian_business` | the buyer snapshot has an organisation number and today is before 2027-01-01: from that day a Norwegian business must receive an e-invoice, and this is a PDF |
| `buyer_norwegian_business_required` | the same buyer from **2027-01-01**, when an e-mailed PDF no longer meets the B2B duty |

**The public-body half, dropped, and what replaces it.** The 1A review asked for a
warning when the buyer is a public body. The directory carries no such fact — "Public
sector" in [Customers](/en/reference/customers/#groups) is a group an installation may name, a word
of its own vocabulary, not a fact a module can read — so that half is not built, and no
fact is added to the contract for it. What the snapshot does carry is the buyer's
organisation number, which every public body and every Norwegian business has; the two
`buyer_norwegian_business` warnings key on it, and from 2027 the B2B duty makes that the
warning that matters.

**`sendDefaults`** — `{recipient?, preference?, warnings}` — is what the Send dialog opens
with: the customer's current invoice e-mail, the profile's invoice delivery preference,
and the warnings. It is on an issued document's `GET /{id}` and on the send's own
response only, and only for a caller who may send — `invoices:issue` on an installation
that can send: the customer's invoice e-mail sits behind `customers:view` in its own
module, and `invoices:access` alone must not widen that. A customer this module has
anonymised gets none, and the directory is not asked: a send to one is refused. A
payment's, a removal's, an issue's and a credit's responses never carry it, so no write
adds a directory call to its answer. It is the one directory read on an issued document,
and it is best effort: a directory that fails leaves `sendDefaults` out and logs at
warn, never a 500 on a read of bookkeeping material. The document's own `warnings`
(`issued_late`, …) are another list and never mixed with the send's. Where
`sendDefaults` would be, an anonymised customer's document answers `customerAnonymised:
true` instead, so the app offers no send; it is absent otherwise.

**The delivery log.** `invoices.deliveries` holds one row per mail the server took: the
recipient, the subject as sent, the bare Message-ID, the SHA-256 of the PDF attached, when,
and the sender's user id. Every issued document answers `deliveries`, the first first.
**The recipient is on the wire only for a caller with `invoices:issue`**; a reader sees
when each send happened, by whom and its subject, not the address. Two triggers hold the
log: `tr_deliveries_immutable` refuses a DELETE and every UPDATE but the recipient to
`''` — the one write an anonymisation makes ("invoices: a delivery is immutable") — and
`tr_deliveries_parent` reads the document's status and current customer `FOR SHARE` on
INSERT, refuses a draft's ("invoices: a delivery needs an issued document"), and then
blanks the recipient when the customer has been anonymised.

**The anonymised customer.** A send reads its recipient before it sends and writes its
row after; an anonymisation committing in between would otherwise blank the rows that
existed and leave the new one holding the person's address, for good.
`invoices.erased_customers`, written by the erase under a lock on the customer's
documents, closes that: a send after the erase is refused `customer_anonymised`; a row
written while the erase holds the documents waits on the trigger's `FOR SHARE` and, once
the erase commits, sees the marker and is written with `''`; a row written after the
erase is blanked the same way; a row written before it is blanked by the erase. The check
is the trigger's because only a statement run after the lock wait sees an erase that
committed during it — the inserting statement's own snapshot was taken before. The
trigger reads the document's current customer, so a merge in between is covered too. A
delivery whose recipient is `''` is one whose customer was anonymised.

## The journal

`GET /invoices/journal?from&to` lists the issued documents with an issue date in the
range in number order, credit notes **signed negative** in every amount (they are stored
positive), totals per SAF-T code, category and rate over the whole range, and the gap
check: every number from one past the issued document before the range's first (the
series start when there is none) to the range's last that no issued document holds — at
most 1000 listed. A gap can thus lie before the range's own first document — between
the previous period's last document and this one's first — and a hole straddling two
ranges is listed in full by the later one, not only the number just before its first.
An empty range, one no issued document falls inside, carries no `checkedFrom` or
`checkedTo`; otherwise they are the numbers the check actually covered, not the
requested range. `counterLast` is the counter's last allocated number and
`highestIssued` the highest number an issued document holds, whatever its date;
`counterLast` is never lower than `highestIssued` — every issued document took its
number from the counter — so **`counterLast` greater than `highestIssued`** is exactly
what it means for a number to have been taken without a document ever being issued for
it. Neither a non-empty `gaps` nor a `counterLast` above `highestIssued` is ever normal:
a failed issue rolls its number back inside the same transaction and an issued row
never changes afterwards, so either can only mean the series was broken from outside
the application — a manual SQL statement, a partial restore. A closed month's journal
can still change after the month ends, while today's calendar day is 15 or less: a
document may still be dated that month's last day under the backdating rule above, and
only while no later-dated document has yet been issued. This is what shows "det ikke er
brudd i nummerserien".

## The CSV export

`GET /invoices/export.csv?from&to` is the accountant's file of a period: the journal's
selection — the issued documents with an issue date from `from` to `to`, both required
calendar dates, `from` on or before `to` — in number order, **one row per document and
VAT summary row**, and within a document by category then rate. A credit note is signed
negative in every amount column, as the journal signs it (it is stored positive).

The columns, fixed and English, in this order:

```text
Number;Kind;Issue date;Delivery;Due;Customer number;Buyer;Buyer org no;Currency;SAF-T code;Rate;Base;VAT;Base NOK;VAT NOK;Credits number;KID
```

| Column | Rule |
| --- | --- |
| `Number` | the document's number |
| `Kind` | `invoice` or `credit_note` |
| `Issue date` | the issue date |
| `Delivery` | the delivery day, or the period as `YYYY-MM-DD/YYYY-MM-DD` (ISO 8601's interval notation); empty when there is none |
| `Due` | the invoice's due date; empty on a credit note |
| `Customer number` | the buyer snapshot's |
| `Buyer` | the buyer snapshot's name |
| `Buyer org no` | the buyer snapshot's organisation number; empty for a person or a foreign buyer |
| `Currency` | the document's — NOK in this phase |
| `SAF-T code` | the VAT row's |
| `Rate` | the VAT row's rate in percent (`25,00`) |
| `Base` | the VAT row's taxable amount, in the document's currency |
| `VAT` | the VAT row's VAT, in the document's currency |
| `Base NOK` | `Base` × the document's exchange rate (1 in this phase), rounded to øre — the one computed column |
| `VAT NOK` | the VAT row's stored NOK VAT |
| `Credits number` | on a credit note, the number of the invoice it credits; empty on an invoice |
| `KID` | an invoice's KID, appended last in phase 2 so the earlier columns keep their places; empty without one and on a credit note. A spreadsheet that reads it as a number strips its leading zeros — import the column as text |

**The byte format** is the expenses payroll file's ([the payroll CSV](/en/reference/expenses/#the-payroll-csv)),
duplicated into this module as customers duplicated it — depguard keeps modules from
sharing it: UTF-8 with a byte order mark, `;` between cells, the decimal comma and two
decimals, dates as `YYYY-MM-DD`, CRLF after every row the last included, and RFC 4180
quoting — a cell holding `;`, `"`, CR or LF is quoted and its quotes doubled. Every
amount is the stored `numeric` as exact text, never a float, and a credit note's `0,00`
stays `0,00`.

**The formula guard is on the text columns only.** `Kind`, `Delivery`, `Customer number`,
`Buyer`, `Buyer org no`, `Currency`, `SAF-T code` and `Credits number` get an apostrophe in
front when they begin with `=`, `+`, `-`, `@`, a tab or a CR, so a buyer named `=cmd`
opens as text. `Number`, `Issue date`, `Due`, `Rate` and the four amounts are never
guarded: the payroll file guards every cell because none of its amounts is ever
negative, but a credit note's are, and a guarded `-1234,50` is text in a spreadsheet, not
a number.

**The cap and the headers.** A period of more than **5000 rows** is a 400 asking for a
narrower period — known before a byte is written, never a file cut short; the cap is the
customers export's. The answer is `text/csv; charset=utf-8`, with
`Content-Disposition: attachment; filename="invoices-<from>-<to>.csv"` and
`Cache-Control: private, no-store`: a period's invoices name who the business sold to and
for how much, and belong in nobody's cache.

## Stats

`GET /invoices/stats/summary?from&to` is the dashboard's Invoices card, in the envelope
every module's summary shares: `from` and `to` are instants, `to` defaulting to now and
`from` to 30 days before it, and `from` after `to` is a 400. The answer echoes the
period as `from` and `to`, and:

| Field | What it counts |
| --- | --- |
| `outstandingAmount`, `outstandingCount` | **now**: the issued invoices that are `open`, `partially_paid` or `overdue`, at their open amounts — credit notes and paid or credited invoices are never outstanding |
| `overdueAmount`, `overdueCount` | **now**: of those, the `overdue` ones |
| `issuedCount`, `issuedGrossTotal` | the invoices issued in the period |
| `issuedGrossTotalDelta` | `issuedGrossTotal` less the previous period's — an amount, not a percentage |
| `creditedCount`, `creditedGrossTotal` | the credit notes issued in the period |
| `paidAmount`, `paidCount` | the live payments whose `paidOn` is in the period |

"Now" is today in Oslo from the server's clock, judged through the same state function
the list filters with. Every figure is NOK, the only currency in this phase.

**From instants to Oslo days.** The period arrives as half-open instants `[from, to)`;
issue dates and payment dates are Oslo calendar days. `fromDay` is the Oslo day `from`
falls on; `toDayExclusive` is the Oslo day of the last instant inside the period (`to`
less a nanosecond) **plus one day** — so a period ending now, or at the end of today,
includes today, which a bare `< toDay` would drop from every dashboard preset. A document
is in the period when `fromDay ≤ issue date < toDayExclusive`, a payment when its
`paidOn` is. **The previous period is counted in days, not in duration**: the same number
of Oslo days, `toDayExclusive − fromDay`, ending at `fromDay`. The platform's
`previousFrom` is an absolute duration, a day off the current period's length across a
daylight-saving change or for a default period that starts mid-day, and "the period of
the same length just before" is what the delta compares against. There is no timeseries
and no attention list in this phase.

## Retention and personal data

Sales documentation is kept **five years after the end of the financial year**
(bokføringsloven § 13, <https://lovdata.no/lov/2004-11-19-73/§13>). Nothing is purged in
this phase; a purge is later work. The 2027 wording of § 13 (Lov 2026 nr. 39) was not
read — **unconfirmed**. **The operator's backup of the object store is part of that
retention**: the only storage driver is `fs`, with no WORM, so the PDFs are only as safe
as the volume and its backups ([storage](/en/admin/object-storage/)).

**Payments and deliveries are kept with the document** they hang off. A payment
registration is bookkeeping material read under the same § 13 — **unconfirmed**, see
[Payments](#payments-and-the-state-of-an-invoice) — and a delivery is the record of when
the claim was handed to the mail server, not of its receipt; neither is ever deleted,
and their foreign keys refuse a document's deletion.

The module fills both customer slots ([module boundaries](/en/contributing/module-boundaries/)):

- **Merging customers** (`contracts.CustomerReferenceHolder`) re-points every document of
  the absorbed customer, drafts and issued, reported as `invoices.invoices`. An issued
  document keeps its buyer snapshot — the id is not printed, the snapshot is — and its
  revision. Payments and deliveries hang off the document by id and carry no customer
  id, so they follow it and are not reported.
- **A person's export** (`contracts.CustomerPersonalData`) hands over every issued
  document and every draft, each with its lines, a structured `buyer` — the full
  snapshot: name, type, organisation number, foreign id, GLN, Peppol id, language and
  the address with its region — the `deliveryAddress` when one is set, the references
  and both notes, and, on a credit note, `credits{number, issueDate}` naming what it
  credits. An issued document's internal note is exported too: it is immutable once
  issued, the same as every other column, and export carves out no exception for it.
  An issued document also carries its `payments` — every registration, with its paid
  date, amount, currency, reference, note and registration time, and a removed one's
  removal time and reason: a bank reference often names the payer, and a note is staff
  free text about them — and its `deliveries`, each with its recipient, sent time and
  subject. A draft has neither.
- **Anonymisation**, inside the customers module's transaction, in this order: it locks
  the person's documents `FOR UPDATE`, newest first — the merge holder's statement, the
  module's lock order — so a delivery insert, whose trigger takes the document `FOR
  SHARE`, waits for it; writes the marker in `invoices.erased_customers`; blanks the
  recipient of every delivery of those documents; blanks the note of every payment of
  them, live and removed; and deletes the drafts. It reports four kinds, in this order:
  - `invoices.drafts` — the drafts deleted, invoice and credit-note drafts alike: a
    draft is not a sales document and has no retention basis, so GDPR art. 17 applies;
  - `invoices.documents`, at 0 — every issued document, its buyer snapshot and its
    internal note are kept under § 13; the note is immutable once issued, so
    anonymisation has no more standing to touch it than any other write does;
  - `invoices.payments` — the payment notes blanked (0 when none had one). A
    registration is kept, because it is bookkeeping material kept with the document,
    not because it holds nothing personal: its date, amount and the bank's reference —
    which often names the payer — stay, while the note, staff free text about the
    person that no retention rule needs, goes. A removed registration's
    `removal_reason` stays too: it is the audit trail kept with the registration — it
    says why a registration was withdrawn, not who the person is;
  - `invoices.deliveries` — the deliveries whose recipient was blanked: the rows stay as
    the record of when the claim was handed to the mail server, the address gone.

  Run twice, it finds nothing and reports zeros, and the marker keeps its first time.
  The marker refuses every later send (`customer_anonymised`), blanks any delivery
  row a send racing the erase writes ([Sending a document](#sending-a-document)), and
  blanks the note of any payment registered after or racing the erase; it is
  read by this module only and never removed — anonymisation is never undone.
  `contracts.ErasedData` carries no reason field; this paragraph is where the reasons
  are written.

## Permissions

No built-in role holds any of these; Owner has the wildcard.

| Key | Sensitive | What it allows |
| --- | --- | --- |
| `invoices:access` | no | Use the app; read every invoice, credit note, PDF, payment and delivery, the journal, the CSV export and the stats. |
| `invoices:create` | no | Create, edit and delete drafts; preview a draft. |
| `invoices:issue` | yes | Issue a draft; create a credit-note draft; send an issued document by e-mail, and see where each send went. |
| `invoices:manage` | yes | The seller record, the series start, VAT codes and their rates. |
| `invoices:payments` | yes | Register a payment against an issued invoice, and remove a registration with a reason. |

`invoices:payments` is sensitive because a registration changes what the company says it
is owed, and a wrong one is corrected only by a removal that stays on record.

**Sending is under `invoices:issue`**: whoever may create bookkeeping material may hand
it over, and a reader with `invoices:access` alone may download it, as before, and sees
each send's time and subject but not its address. Sending also needs an installation
that can send — `MAIL_DRIVER=smtp` and the `SMTP_*` configuration
([email delivery](/en/admin/authentication/#email-delivery)). `GET /meta` answers
`mailAvailable`, `capabilities.canSend` — `invoices:issue` and `mailAvailable` — and
`capabilities.canRegisterPayments`, so no client re-derives either rule.

**Creating a draft in the app also needs `customers:view`**: the directory has no
search, so the buyer picker reads the customers module's own list. The API takes a
customer id and checks nothing more; the app hides "New invoice" without it.

## Endpoints

All under `/api/v1/invoices`, every one behind `invoices:access`. The access rules are
`permission:invoices:access` and, with the key under "Also needs",
`permission:invoices:access+invoices:create`, `permission:invoices:access+invoices:issue`,
`permission:invoices:access+invoices:manage` and
`permission:invoices:access+invoices:payments`.

| Operation | Also needs | Refusals |
| --- | --- | --- |
| `GET /meta` | | |
| `GET /settings` | | |
| `PUT /settings` | `invoices:manage` | 400 on the field (both mod-11 checks, IBAN mod-97, BIC, "Only NOK in this phase", the Peppol id, the KID pair, a next number the KID length does not fit, any of the three required-nullable fields absent); 409 `series_locked`, or a stale revision (no code) |
| `GET /vat-codes` | | |
| `POST /vat-codes` | `invoices:manage` | 400 on the field, a duplicate code on `code` |
| `PUT /vat-codes/{id}` | `invoices:manage` | 404; 400; 409 `vat_code_in_use`, a stale revision |
| `POST /vat-codes/{id}/rates` | `invoices:manage` | 404; 400 on `ratePercent` or `validFrom`; 409 `rate_change_in_past` |
| `DELETE /vat-codes/{id}/rates/{rateId}` | `invoices:manage` | 404; 409 `rate_period_not_latest`, `rate_period_last`, `rate_period_in_use` |
| `GET /` | | 400 paging, status, kind, state, `from` after `to` |
| `POST /` | `invoices:create` | 400 on the field; 409 the customer gates |
| `GET /{id}` | | 404 |
| `PUT /{id}` | `invoices:create` | 404; 400; 409 `invoice_issued`, the customer gates, a stale revision |
| `DELETE /{id}` | `invoices:create` | 404; 409 `invoice_issued` |
| `POST /{id}/issue` | `invoices:issue` | 400 a body that does not decode (none, or an `issueDate` that is no calendar day); 404; 409 every code under [Issuing](#issuing); 503 `storage_unavailable` |
| `POST /{id}/credit` | `invoices:issue` | 404; 409 `invoice_draft`, `credit_note_not_creditable`, `invoice_fully_credited` |
| `GET /{id}/pdf` | | 404; 409 `invoice_draft`; 500 a missing or altered stored object, or a render that fails; 503 `storage_unavailable` |
| `GET /{id}/preview.pdf` | `invoices:create` | 404; 409 `invoice_issued` |
| `POST /{id}/payments` | `invoices:payments` | 400 a body that does not decode; 404; 409 `credit_note_no_payments`, `invoice_draft`; 400 on the field; 409 `invoice_settled`, `payment_exceeds_open` (with `openAmount`) |
| `POST /{id}/payments/{paymentId}/remove` | `invoices:payments` | 400 on `reason`; 404 the document, or a payment not its own; 409 `payment_removed` |
| `POST /{id}/send` | `invoices:issue` | 429 `rate_limited`; 503 `mail_unavailable`; 404; 409 `invoice_draft`, `customer_anonymised`; 400 on `recipient`; 409 `no_invoice_email`; 503 `storage_unavailable`; 500 a directory that fails, a missing or altered stored object, a render that fails, or a sent mail whose row could not be written; 502 `mail_failed` |
| `GET /journal` | | 400 `from` or `to` missing or not a calendar date, `from` after `to`, paging |
| `GET /export.csv` | | 400 `from` or `to` missing or not a calendar date, `from` after `to`, more than 5000 rows |
| `GET /stats/summary` | | 400 `from` after `to` |

## What comes next

- **2** (next): EHF over Peppol and KID — what makes B2G and, from 2027, B2B invoicing
  lawful, and what the send's warnings point at.
- **3**: hours, expenses and milestones turned into lines, with a write-back contract.
- **4**: payment files matched on KID, reminders and late interest — and overpayment,
  customer credit balances and refunds as a flow, which 1B refuses or only shows as a
  figure.
- **5**: energy consumption billing.

Left out of 1B on purpose: editing a payment (remove it and register it again), a
payment in another currency than the document's, a payment against a credit note, one
payment allocated across several invoices, an idempotency key, HTML mail, a logo, an
editable template or a personal message in the mail, sending through an outbox or a
worker, honouring `communications.suppressions`, bulk sending, a "paid" stamp on the PDF
(the PDF is immutable), timeseries and attention stats, per-currency stats, a public-body
fact on the directory, user display names on payments and deliveries (ids only), and a
purge of anything.
