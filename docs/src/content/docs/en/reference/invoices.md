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
accountant's CSV export and the dashboard's stats; phase 2
([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-03-invoices-ehf-peppol-kid-design.md))
added the e-invoice — an issued document sent as EHF over the Peppol network through an
access point, followed by two workers to its outcome — and the KID under the seller's
bank agreement. Vantigo stays a sub-ledger: there is
no general ledger and nothing is posted — a payment here is a registration, not a
posting, and nothing is matched to a bank file.

> **The e-invoicing duties.** Invoicing the public sector has required EHF since 2019
> (FOR-2019-04-01-444), and invoicing Norwegian businesses requires an e-invoice from
> **2027-01-01** (Lov 19. juni 2026 nr. 39). A PDF by e-mail is not an e-invoice; a
> document sent as EHF is ([Sending as EHF](#sending-as-ehf)), on an installation set up
> for it ([E-invoicing](/en/admin/e-invoicing/)). The e-mail send warns where the duty
> applies and EHF is the channel the customer expects.

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
| `invoices.transmissions` | One EHF transmission of an issued document: the provider, the idempotency key, the sender's and receiver's Peppol ids, the document type and process, the submitted UBL's object key and SHA-256 and the PDF's SHA-256, the status (`queued`, `submitted`, `delivered`, `failed`, `unconfirmed`, `cancelled`), the provider's reference, the evidence's key and SHA-256, the attempt counters and the next attempt, the crash marker `submit_attempted_at`, the worker's lease, the last error, the receiver lookup it was queued under, the timestamps of each state, and the resolution of an `unconfirmed` row — a person's, with who and a required note, or the worker's own when the provider answers at last, with a note and no user (`ck_transmissions_resolution`). Every completion of a worker's claim names the status the claim saw, so a row the events worker moved meanwhile is left alone, never refused. Never deleted; only its state columns change, a failed or cancelled row not at all, and a delivered row only its lease, cadence and — once — its evidence. A trigger refuses one under a draft (`invoices: a transmission needs an issued document`) or for an anonymised customer (`invoices: the customer is anonymised`); `ux_transmissions_active` allows one queued, submitted, delivered or unconfirmed transmission per document. |

A document's state is not a column: `invoices.document_state(...)` derives it, see
[Payments and the state of an invoice](#payments-and-the-state-of-an-invoice).

Phase 2's schema (`00036_invoices_ehf_kid.sql`, the
[design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-03-invoices-ehf-peppol-kid-design.md))
adds the Peppol id, the KID agreement, the document's KID and the two tables in one
migration; [Sending as EHF](#sending-as-ehf) queues a transmission and its
[workers](#workers) carry it to an outcome.

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
with. The same figure is on every settings answer, `GET` and `PUT` alike, as
`nextNumber`: the number the next issue takes — the counter's next value once anything
is issued, else `seriesStart`.

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

## The access point's credentials

A document travels onto the Peppol network through an access point, a provider Vantigo
hands it to; Storecove is the one provider so far. Its credentials are one row of their
own, `invoices.access_point_credentials`, off the settings row every issue reads `FOR
SHARE`: the provider, its settings that are not secret (`settings_json`, Storecove's
`legalEntityId` — the legal entity documents are sent as) and the API key, sealed by the
secrets box under the purpose `invoices/access-point-credential`. The key is never
answered, logged or sent anywhere but to the provider, as `Authorization: Bearer`.

`PUT /settings/access-point` (`invoices:manage`) takes `{provider, legalEntityId,
apiKey?}`: `provider` is `storecove`, `legalEntityId` a positive integer, and `apiKey`
the key, trimmed — an omitted or null key keeps the stored one, which the PUT opens and
seals again from the row it read `FOR UPDATE` in its own transaction, so a concurrent
PUT's new key is never written back to the old one. The first PUT must carry a key, and
a blank one is a 400. The answer is `{provider, legalEntityId, hasCredentials,
rejectedAt?}`, never the key. A PUT clears `rejectedAt`. `DELETE` removes the row (204,
also when there is none), and a switch to another provider would replace it: both are
refused with 409 `transmissions_active` while any transmission is `queued`, `submitted`
or `unconfirmed`, because the provider still holds what those need. A new key or legal
entity for the same provider is not refused — a refused key must be replaceable while
documents wait for it. `GET /settings/access-point` (`invoices:manage`) answers the same shape, never the key;
with nothing stored it is a 200 with `hasCredentials: false` and neither `provider`
nor `legalEntityId` — an empty setting, not a missing resource, so the settings page
opens an empty card. `POST /settings/access-point/verify` makes the provider's
cheapest authenticated read with the stored key (Storecove: `GET legal_entities/{id}`)
and answers `{result}`: `ok`, which clears `rejectedAt`; `unauthorized`, a 401 or 403,
which sets it; or `unreachable` for anything else — the network, a timeout, a 5xx, a
legal entity the key does not reach. Without credentials it is a 409 `ehf_unavailable`.

`rejected_at`, which meta reports as `accessPointCredentialsRejected`, is set the first
time the provider refuses the key and when the stored key cannot be opened — `APP_SECRET`
changed, or the row was altered — which is also logged at error and answered 503
`ehf_unavailable` wherever the key must be opened (the verify, a PUT that keeps the
key). It is cleared by a successful verify, by a new PUT, and by any call of the
[workers](#workers) the provider accepts — a submission, a 422, an evidence probe, a read
of the event queue. Neither a PUT nor a verify moves a transmission: a row the refused
key held back waits out its hour.

**The Storecove adapter** (`accesspoint/storecove.go`) speaks Storecove's API v2 at
`INVOICES_STORECOVE_BASE_URL`, the operator's setting — so it dials unguarded, as the
Brreg lookup does — never follows a redirect, never retries (the worker retries under
the idempotency key, one call per claim), and bounds each call, every request in it,
by 30 seconds. A submission is `POST document_submissions` with the legal entity, the
transmission's idempotency key as `idempotencyGuid`, the receiver under Storecove's own
scheme (`0192` is `NO:ORG`; any other scheme is refused before any call) and the UBL
base64-encoded for Storecove to parse; Storecove regenerates the UBL it transmits.
Status is Storecove's pull queue (`GET webhook_instances/`, one event or 204; `DELETE
webhook_instances/{guid}` acknowledges): `succeeded` is delivered — the receiving access
point's AS4 receipt, nothing stronger — `failed` and `no_action_taken` are failed, and
every other state is still submitted. The evidence (`GET
document_submissions/{guid}/evidence/sending`, 404 until it succeeded) lists the
delivered documents at expiring URLs, which are fetched at once, over https only, without
the key, at most 20 MiB each; a failed download's error never names its URL, whose query
is the signature that grants it. A 422 is Storecove's refusal of the document **or** of a
key it has already seen — the same answer — 401 and 403 are the key, a 429 carries its
`Retry-After` (seconds, or a date judged by the module's clock), and a transport failure or a 5xx leaves the outcome unknown.

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
both — and `ehf_buyer_reference_missing`: a draft headed for EHF — its customer's
billing profile prefers `ehf` or carries a Peppol id, or, on a credit-note draft, the
buyer snapshot it copied has one — with neither `yourReference` nor `orderReference`.
Peppol needs one of the two (`PEPPOL-EN16931-R003`), and neither changes after the
issue, so the send as EHF would refuse the document with `buyer_reference_missing` and
only a credit note would mend it ([Sending as EHF](#sending-as-ehf)).

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
registration or removal only its invoice, a send's delivery row only its document,
`FOR SHARE`, and the send as EHF its document `FOR UPDATE` and then the access-point
credentials row `FOR SHARE`, which `PUT` and `DELETE /settings/access-point` lock `FOR
UPDATE` alone; the EHF workers lock no document; no issue locks a code. The merge holder locks the documents it re-points
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

## The EHF document

An issued document can be rendered as **EHF Billing 3.0** — a Peppol BIS Billing 3.0
UBL 2.1 `Invoice` or `CreditNote` (EHF is Peppol BIS plus the two Norwegian checks
NO-R-001 and NO-R-002, with no customization id of its own) — by the package
[`apps/server/internal/invoices/ehf`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/invoices/ehf).
This section is the document; storing it and transmitting it over Peppol belong to the
send.

**Identifiers.** `cbc:CustomizationID`
`urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0`,
`cbc:ProfileID` `urn:fdc:peppol.eu:2017:poacc:billing:01:1.0`, no `cbc:UBLVersionID`,
`cbc:InvoiceTypeCode` **380** or `cbc:CreditNoteTypeCode` **381** — a credit note is
never a negative invoice, and its amounts are positive, as stored.

**Deterministic.** The EHF is a pure function of the document's own rows and snapshots,
the seller's Peppol id at the time of rendering (not part of the snapshot) and the
stored PDF's bytes: no clock, no randomness, and the same inputs render the same bytes.
It is written by a small hand writer over Go's `encoding/xml` encoder with literal
`cac:`/`cbc:` element names, the three namespace declarations on the root and the
elements in the UBL schema's order. Amounts have exactly two decimals, quantities their
stored three and unit prices their stored four, never in exponent form; an empty value
is left out rather than written as an empty element (PEPPOL-EN16931-R008).

**The mapping**, from the snapshot and nothing else (the directory is never read; the
buyer is the snapshot's, as the PDF prints it):

| UBL | From |
| --- | --- |
| `cbc:ID`, `cbc:IssueDate`, `cbc:DueDate` (invoice only) | `number`, `issue_date`, `due_date` |
| `cbc:DocumentCurrencyCode` | `currency` (NOK) |
| `cbc:BuyerReference` (BT-10) | `your_reference`, when set |
| `cac:OrderReference/cbc:ID` | `order_reference`, when set. Peppol needs one of the two references (PEPPOL-EN16931-R003) |
| `cac:BillingReference/cac:InvoiceDocumentReference` (credit note) | the original's `number` and `issue_date` |
| `cac:InvoicePeriod` (`cbc:StartDate`, `cbc:EndDate`) | `delivery_from`, `delivery_to` |
| `cac:Delivery/cbc:ActualDeliveryDate` | `delivery_date` |
| `cac:Delivery/cac:DeliveryLocation/cac:Address` | the place of delivery, **only when it has a country** (BR-57); one without a country is left out of the EHF, the PDF still prints it |
| `cac:AdditionalDocumentReference` | the stored PDF: `cbc:ID` the number, `cbc:DocumentDescription` "Faktura (PDF)" / "Invoice (PDF)" ("Kreditnota (PDF)" / "Credit note (PDF)" on a credit note), `cac:Attachment/cbc:EmbeddedDocumentBinaryObject` the bytes in Base64 with `mimeCode="application/pdf"` and `filename` the download's name |
| `cac:AccountingSupplierParty/cac:Party` | `cbc:EndpointID@schemeID` the seller's Peppol id split at its colon; `cac:PartyName/cbc:Name` and `cac:PostalAddress` from the seller snapshot; `cac:PartyTaxScheme` with `cbc:CompanyID` `NO<organisation number>MVA` under `VAT` **only when VAT-registered** (NO-R-001), and `Foretaksregisteret` under `TAX` **only when registered there** (NO-R-002, a warning when absent); `cac:PartyLegalEntity` the legal name and the organisation number under `schemeID="0192"`; `cac:Contact/cbc:ElectronicMail` the seller's e-mail when set |
| `cac:AccountingCustomerParty/cac:Party` | `cbc:EndpointID@schemeID` from `buyer_peppol_id` (the scheme its prefix, the value the rest); `cac:PostalAddress` from the buyer snapshot, the region as `cbc:CountrySubentity`; `cac:PartyLegalEntity/cbc:RegistrationName` **always** (BR-07), with `cbc:CompanyID@schemeID="0192"` for a Norwegian business's organisation number, `cbc:CompanyID` without a scheme for a foreign id, and none for a person |
| `cac:PaymentMeans` (invoice only) | **one**, `cbc:PaymentMeansCode` **30** (credit transfer); `cac:PayeeFinancialAccount/cbc:ID` the domestic account, or for a buyer whose country is not NO the IBAN with `cac:FinancialInstitutionBranch/cbc:ID` the BIC when the seller has an IBAN; `cbc:PaymentID` the KID, and **no `PaymentID` at all without one** — Norwegian receivers read it as a KID |
| `cac:PaymentTerms/cbc:Note` | "Forfall 15.10.2026" / "Due 2026-10-15" on an invoice; "Kreditnota – beløpet godskrives" / "Credit note – the amount is credited" on a credit note, which has no due date (BR-CO-25) |
| `cac:TaxTotal` | `cbc:TaxAmount` the VAT total; one `cac:TaxSubtotal` per VAT summary row, in the document's order: `cbc:TaxableAmount`, `cbc:TaxAmount`, and `cac:TaxCategory` by the category rules below |
| `cac:LegalMonetaryTotal` | `cbc:LineExtensionAmount` and `cbc:TaxExclusiveAmount` the net total, `cbc:TaxInclusiveAmount` and `cbc:PayableAmount` the gross total; no rounding amount |
| `cac:InvoiceLine` / `cac:CreditNoteLine` | `cbc:ID` the position; `cbc:InvoicedQuantity` / `cbc:CreditedQuantity` with `unitCode` from the unit table; `cbc:LineExtensionAmount` the line net; when the discount is not zero, `cac:AllowanceCharge` with `cbc:ChargeIndicator` false, reason code `95` and reason "Rabatt" / "Discount" (BR-42), `cbc:MultiplierFactorNumeric` the discount percent, `cbc:Amount` the line allowance and `cbc:BaseAmount` the line gross (PEPPOL-EN16931-R040–R042); `cac:Item/cbc:Name` the description; `cac:Item/cac:ClassifiedTaxCategory` the line's snapshot category, with `cbc:Percent` except for O; `cac:Price/cbc:PriceAmount` the unit price |

**The category rules.** `cbc:Percent` is written for S, Z, E, AE, G and K, **never for
O** (BR-O-05). In a VAT summary row, AE, G and O carry `cbc:TaxExemptionReasonCode`
`VATEX-EU-AE`, `VATEX-EU-G` and `VATEX-EU-O`; **E alone carries the free-text reason**
as `cbc:TaxExemptionReason`; Z carries nothing (BR-Z-10 forbids any reason on Z), and
nor does S. A final credit note's squaring row — a taxable amount of 0.00 with a small
negative VAT — is written as stored, inside BR-CO-17's and BR-S-09's one-krone
tolerance; and since BR-S-08 requires a line at a VAT row's rate to exist, **every VAT
row with no line at its category and rate gets a zero line** after the real ones: the
next position after the highest, a quantity of `0.000` `C62`, a line amount of `0.00`,
the name "Avrunding merverdiavgift 15 %" / "VAT rounding 15 %" in the document's
language, the row's category and rate, and a price of `0.0000`. It adds nothing to any
sum, and the stored document is unchanged. Category K needs the buyer's VAT identifier, which
the module does not hold, so a document with a K line is never sent as EHF (the
pre-check below); its PDF is unaffected.

### Units

A line's `unit` is free text; EHF needs a UN/ECE Recommendation 20 code on every
quantity. The module maps the common words, trimmed, case-insensitively and with
trailing punctuation stripped (`Stk.` is `stk`), by one table:

| Code | Words |
| --- | --- |
| `C62` (one) | `stk`, `pcs`, `piece` — and every unit not below, the empty one included |
| `HUR` | `time`, `timer`, `h`, `hour`, `hours` |
| `MIN` | `min` |
| `DAY` | `dag`, `day` |
| `WEE` | `uke`, `week` |
| `MON` | `mnd`, `month` |
| `ANN` | `år`, `year` |
| `KGM` | `kg` |
| `GRM` | `g` |
| `MTR` | `m` |
| `MTK` | `m2`, `m²` |
| `MTQ` | `m3`, `m³` |
| `LTR` | `l`, `liter`, `litre` |
| `KMT` | `km` |
| `XPK` | `pakke`, `pack`, `pk` |
| `SET` | `sett`, `set` |
| `KWH` | `kWh` |

`t` is deliberately not mapped — hour or tonne — and falls back to `C62`, as does any
other word; the description carries the meaning. There is no unit-code column and no
picker.

### The pre-check

Two checks run on the rendered bytes. **The pre-check** reads the XML alone and names
the rules a person can be told about: neither a buyer reference nor an order reference
(`PEPPOL-EN16931-R003`); no buyer endpoint (`PEPPOL-EN16931-R010`), or a buyer endpoint
scheme not on the Peppol EAS code list (`PEPPOL-EN16931-CL008`; the list is vendored
whole from the Peppol BIS release `v3.0.20`); a seller VAT id beginning `NO` that is not
`NO`, a valid organisation number and `MVA` (`NO-R-001`); a K category
(`vat_category_k_unsupported`). **The invariants** are what only the module could get
wrong, and a broken one is an error, never a refusal in words: the totals re-summed from
the lines and the VAT (`BR-CO-10`, `BR-CO-13`, `BR-CO-15`), and the VAT rows re-summed
against them — their VAT against the VAT total (`BR-CO-14`) and their taxable amounts
against the net (`vat_taxable_sum`); the KID re-verified against
its stored algorithm and the payment id equal to it, with no payment id without a KID
(`kid_invalid`, `payment_id_without_kid`); the stored PDF attached
(`pdf_attachment_missing`); every unit code one of the table's (`unit_code_unknown`).
Neither replaces the official XSD and Schematron artefacts, which validate the committed
golden documents (`apps/server/internal/invoices/ehf/testdata/golden`); hand-tampered
documents under `testdata/invalid`, with a manifest of the rule ids each must trip, hold
the pre-check to the same ids.

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

**The warnings** are never refusals: a send is never refused for one. They are on
`sendDefaults.warnings` and on the send's own response, and the server judges each
against today in Oslo from its own clock — never the browser, which has neither, and a
code is what a test with a fixed clock can pin.

| Warning | When |
| --- | --- |
| `delivery_preference_ehf` | the customer's current billing profile says `ehf`: the customer expects EHF, and an e-mailed PDF does not meet the e-invoicing duty — when this document cannot go as EHF |
| `ehf_preferred` | the same preference, **in place of** `delivery_preference_ehf`, when the caller can send as EHF on this installation (`capabilities.canSendEhf`) **and** the document's `ehf.blockedBy` is empty: send it as EHF instead ([Sending as EHF](#sending-as-ehf)). A document that cannot go — no Peppol id in its snapshot, no reference, already sent — keeps `delivery_preference_ehf` |
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

## Sending as EHF

`POST /invoices/{id}/send-ehf` queues an issued document's EHF — the UBL of
[The EHF document](#the-ehf-document), the stored PDF embedded — for the Peppol network.
It needs `invoices:issue` and takes no body. It does **not** call the access point: it
judges, renders, re-checks the receiver, stores the UBL and writes one row in
`invoices.transmissions` as `queued`, and the `invoices-ehf` worker submits what is
queued. In order:

1. **503 `ehf_unavailable`** when the installation cannot send as EHF —
   `INVOICES_EHF_ENABLED` off, the Peppol lookup disabled, no access-point credentials
   stored, or no seller Peppol id — judged before anything is read, as meta's
   `ehfAvailable` is.
2. 404; 409 `invoice_draft` on a draft; 409 `customer_anonymised` when this module has
   anonymised the document's customer.
3. **409 `no_peppol_id`** when the buyer snapshot has no Peppol id, or one that is not
   `<scheme>:<value>` — the snapshot is the document's, so one issued before the customer
   got a Peppol id is credited and issued again, or e-mailed.
4. **409 `buyer_reference_missing`** when neither `yourReference` nor `orderReference` is
   set (`PEPPOL-EN16931-R003`); drafts warn of it early (`ehf_buyer_reference_missing`,
   [Drafts](#drafts)).
5. **409 `ehf_already_sent`** while a transmission of the document is `queued`,
   `submitted`, `delivered` or `unconfirmed` — read without a lock for a quick answer, and
   judged again in step 9.
6. **The render.** The stored PDF, through the download's own path (503
   `storage_unavailable`; 500 for a stored object gone or altered), the UBL rendered from
   the document's rows, the seller's current Peppol id and those PDF bytes, and
   [the pre-check](#the-pre-check) on the bytes: a failed rule is **409 `ehf_invalid`**
   with `rules` — each `{id, message}`, the official rule id where there is one. A broken
   invariant is a 500 and an error log. Cheap and local, before the network.
7. **The re-check.** The receiver — the snapshot's Peppol id — is looked up on the Peppol
   network now, outside any lock, never read from the customers module's stored answer:
   not registered, or registered without this document's type (an invoice or a credit
   note), is **409 `peppol_not_receivable`** with `peppolRegistered` and
   `peppolCanReceive`; a network that cannot answer is **502 `peppol_lookup_failed`**,
   logged by its kind (`timeout`, `network`, …) and never with the identifier.
8. **The UBL stored once** by its SHA-256 under `documents/<id>/<number>-<sha256>.xml` in
   the `invoices` scope (`application/xml`), outside any lock — `Exists` before `Put`, so
   a send after a cancel that renders the same bytes stores nothing again; 503
   `storage_unavailable`. **The reuse rule:** when any of the document's transmissions is
   `failed` with `resolved_by_user_id` set — it was `unconfirmed`, and a person resolved
   it as failed — its bytes may have reached the receiver, so the new transmission
   carries the same object, hash, PDF hash and sender as the newest such one, read back
   and verified against its hash (a 500 when it is gone or altered). All of the
   document's rows are looked at, not only the latest: a reused send that is then
   cancelled leaves the next send carrying the same bytes still. Without one, after any
   other `failed`, or a `cancelled`, the send renders fresh, so a corrected seller id or
   a mapping fixed in a later release is not locked out.
9. **One transaction**: the document `FOR UPDATE` — two sends serialise on it; the
   anonymisation judged again (an erasure that committed meanwhile has, by then); the
   access-point credentials row `FOR SHARE` — a `DELETE` of them locks it `FOR UPDATE`
   and counts what is in flight, so it either waits and is refused
   `transmissions_active`, or commits first and the send finds no row (503
   `ehf_unavailable`); `ehf_already_sent` judged again; then the row: `queued`, a fresh
   idempotency key, the sender (the settings' Peppol id) and the receiver, the document
   type and process, the UBL's key and hash, the PDF's hash, the lookup it was queued
   under, queued and due now, and who sent it. **The floor** is `ux_transmissions_active`
   — one live transmission per document — whose violation is `ehf_already_sent` too; the
   insert trigger's own refusal of an anonymised customer, an erasure committing after
   the judgment, is `customer_anonymised` — never a 500. The locks are always taken in
   this order — the document, then the credentials — and the workers lock no document,
   so the send cannot deadlock against them.
10. The answer is the document with its `ehf` block, without `sendDefaults`.

It is rate limited apart from the e-mail send: 60 per client per 10 minutes under the
policy `invoices-send-ehf`, counted as `invoices-send` is.

**Cancel.** `POST /{id}/transmissions/{transmissionId}/cancel` (`invoices:issue`, no body)
cancels a transmission only while it is `queued`, **never attempted** — the worker
stamps `submit_attempted_at` immediately before it calls the provider — and not leased
by a worker at that moment; anything else is **409 `transmission_not_cancellable`**: once
the provider may hold the document, only its outcome decides. A transmission named under
another document is a 404. A cancelled transmission lets the document be sent again.

**Resolve.** `POST /{id}/transmissions/{transmissionId}/resolve` (`invoices:issue`) takes
`{outcome, note}` — `outcome` `delivered` or `failed` (anything else a 400; the query
also refuses any other outcome on its own), `note` 1 to 500 characters once trimmed —
and resolves only an `unconfirmed` transmission, recording the outcome, who and the
note; anything else is **409 `transmission_not_resolvable`**. `delivered` closes it with
its delivery time; `failed` lets the document be sent again, with the same bytes (the
reuse rule).

**The UBL.** `GET /{id}/transmissions/{transmissionId}/ubl` (`invoices:access`) answers
the stored UBL as `application/xml`, named as the PDF is, with the transmission's id
added — `faktura-1001-1001.xml`, `invoice-…`, `kreditnota-…` or `credit-note-…` by the
document's kind and the buyer's language — never cached — read whole and verified
against the transmission's `ubl_sha256`. A stored object gone or altered is a 500 and an
error log, never a render; a store that cannot be read is 503 `storage_unavailable`.

**The `ehf` block.** Every issued document answers `ehf` (a draft has none): `{status,
queuedAt?, submittedAt?, deliveredAt?, failedAt?, providerRef?, reason?, canSend,
blockedBy?, preference?, buyerPeppolId?, transmissions}`. `status` and the four
timestamps are the latest transmission's, or `not_sent`. `transmissions` is every one,
the newest first — `{id, status, provider, idempotencyKey, receiverParticipant,
ublSha256, queuedAt, submitAttemptedAt?, submittedAt?, deliveredAt?, failedAt?,
cancelledAt?, providerRef?, reason?, resolvedByUserId?, resolutionNote?, ublUrl}`,
`resolvedByUserId` absent when the machine resolved it — the provider's evidence or its
event — and `submitAttemptedAt` the crash marker — when the worker last stamped it,
right before calling the provider; absent while the transmission was never attempted,
which is when a queued one can still be cancelled. **`providerRef`, `reason` and
`submitAttemptedAt` are answered only to a caller with `invoices:issue`**; `reason` is
`last_error` with every e-mail address replaced by `<e-mail>` and every `NNNN:`
participant identifier by `<participant>`, cut to 500 characters on a character boundary
— never the provider's words to a reader. **`canSend`** is what the send would answer
without the network and without a render, and **`blockedBy`** names the first refusal:
`ehf_unavailable`, `customer_anonymised`, `no_peppol_id`, `buyer_reference_missing`,
`ehf_already_sent`, or `ehf_invalid` for a line in VAT category K — the one pre-check
rule the lines alone decide; every other `ehf_invalid` is found at the send.
**`preference`** (the billing profile's invoice delivery) and **`buyerPeppolId`** (the
customer's current Peppol id) are answered by `GET /{id}` only, to a caller with
`invoices:issue`, from the same best-effort directory read as `sendDefaults` — on an
installation that can send as EHF as well as on one that can mail, since an EHF-only
installation has no SMTP — and are absent when the directory could not be read. The list
answers `ehfStatus` on each issued document: its latest transmission's status, or
`not_sent`, the page's in one query.

**Channel precedence** is the app's: the receiver's acceptance is known only at the
send's re-check. The e-mail dialog's `ehf_preferred` (in place of
`delivery_preference_ehf` when the caller can send as EHF and nothing blocks the
document) says so; neither channel is refused for the other.

### Workers

Two background workers carry a queued transmission to its outcome
([design D9](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-03-invoices-ehf-peppol-kid-design.md)).
Both run only when `INVOICES_EHF_ENABLED` is on — off, neither is handed to the
runner — and both run with `PEPPOL_LOOKUP_ENABLED` off too: what is already submitted
still completes, the 48-hour age cap still reaches a `queued` row (it makes no call),
and short of it a `queued` row is left alone, unleased, said in one log line when the
number left changes or an hour after it last was. Each builds the access-point adapter
from the credentials row on every claim or drain. When it cannot — no credentials row,
a key that cannot be opened, settings that do not decode — it logs at error and sets
`rejected_at`, which meta reports as `accessPointCredentialsRejected`; the claimed row
waits an hour, uncounted, and the drain waits for its next cycle. Every time they judge is the module clock's; no
document is locked and no call is made inside a transaction.

**`invoices-ehf`** polls every 5 seconds. It claims one due row at a time by a
conditional `UPDATE` with a **60-second lease** (`FOR UPDATE SKIP LOCKED`, the
communications outbox's shape) and makes **exactly one provider call per claim**,
bounded to 30 seconds. Every completion names the lease and the status the claim saw;
when either changed — the events worker moved the row, or the lease ran out and another
worker took it — the completion changes nothing and is logged at debug. `last_error` is
always redacted as `reason` is. What a claim does depends on the row:

- **`queued`.** First **the age cap**: a row queued more than **48 hours** ago becomes
  `unconfirmed` when `submit_attempted_at` is set — the provider may have the document —
  and `failed` when it never was. Attempts are never capped. Then, when the lookup the
  row was queued under is more than 24 hours old, the receiver is looked up again (a
  lookup, not a provider call) and the `lookup_*` columns refreshed; a receiver no longer
  registered or no longer taking the document type ends the row `failed` with
  `receiver_not_receivable`, the marker untouched. A lookup that fails counts an attempt
  and waits on the backoff, as a store that cannot be read does. Then the adapter, the stored UBL read — bounded as a provider call is — and
  checked against `ubl_sha256` (gone or altered: an error log and an hour's wait — never
  a new render). A claim with less of its lease left than the call may take (30 seconds)
  stops there: the row is due again at once, unmarked and uncounted. Then **the crash
  marker** `submit_attempted_at`, stamped and committed immediately before `Submit`:
  - accepted → `submitted` with the provider's reference and `submitted_at`, the first
    probe five minutes out;
  - **the 422 rule** — the provider answers a validation refusal and a duplicate
    idempotency key alike: when the marker was NULL before this claim it is a
    validation refusal, `failed` with the provider's messages; when it was already set it
    may be the duplicate of a submission that went through, so the row becomes
    `submitted` **without a reference**, for the event drain to match by its key;
  - a transport failure, a timeout or a 5xx → still `queued`, `submit_attempts` + 1, and
    the next attempt after `min(3600, 2^n)` seconds; the marker stays set — the outcome
    is unknown, and the retry goes under the same idempotency key;
  - 429 → the provider's `Retry-After` (seconds or an HTTP date), a minute when it names
    none; 401 or 403 → an hour, `rejected_at` set and an error log; a receiver scheme
    the adapter cannot map → `failed`. These three prove the provider did not take the
    document: none counts an attempt, and each puts the marker back to what it was
    before the claim.
- **`submitted` with a reference** is probed with `Evidence` on a cadence by
  `poll_attempts` — 5 minutes after the submission, 15 after the first probe, then
  hourly. The evidence is the status: not yet available means not yet delivered, an
  answer means **`delivered`** (`delivered_at` the probe's time) even if the provider's
  event was lost. **Seven days after `submitted_at`** without an outcome the row becomes
  `unconfirmed`. A probe answered 429 or 401/403 is treated as the submit's is — the
  `Retry-After`, or an hour with `rejected_at` set — and counts no probe. `submitted`
  **without** a reference is never probed: it is looked at hourly and becomes
  `unconfirmed` at seven days, never probed again.
- **`delivered`** without its evidence stays claimable: the next claim fetches the
  evidence again and stores it once — `Exists` before `Put` — beside the document's PDF:
  the provider's receipt as `documents/<id>/<number>-<transmission>-receipt.json` and the
  document the provider actually transmitted (Storecove regenerates the UBL it was
  given) as `…-delivered.xml`; `evidence_object_key` is the receipt's key and
  `evidence_sha256` the hash of the receipt **as stored** — an object an earlier claim
  stored is kept and read back for it, since every fetch of the evidence differs (its
  document URLs are presigned and expire). A failed fetch or store is retried on the
  probe cadence.
- **`unconfirmed`** is a person's to resolve ([Resolve](#sending-as-ehf)). With a
  reference the worker still probes it **once a day for thirty days** — the thirty days
  after the seven — and when the evidence answers it resolves the row itself:
  `delivered`, `resolved_by_user_id` NULL and the note "Resolved by the provider's
  evidence." After the thirty days, or at once without a reference, `next_attempt_at` is
  `'infinity'` and the row waits for a person.

**`invoices-ehf-events`** polls every 30 seconds and drains the provider's event queue
(Storecove's pull queue: `GET webhook_instances/`, `DELETE webhook_instances/{guid}`)
under a PostgreSQL advisory lock (key `0x494E5645484631`, "INVEHF1"), so one replica
drains at a time. It asks the provider nothing unless a row awaits an event — one
`submitted`, `unconfirmed` and still probed, or `queued` with the marker set — and then
reads until the queue is empty, at most 500 events a cycle; the next cycle reads on. An `unconfirmed` row parked at
`'infinity'` does not count as awaiting an event: it waits for a person, and Storecove
retries its events for five days only. Each event is applied **idempotently and without a row lease**: matched
by its provider reference, or by its idempotency key when the row never learned the
reference (which it then takes), and only while the row is `queued`, `submitted` or
`unconfirmed`. `succeeded` makes it `delivered` at the worker's clock (Storecove's
events carry no time); `failed` and `no_action_taken` make it `failed` with the
provider's reason, redacted; any other state changes nothing. An `unconfirmed` row so
resolved — delivered or failed, the machine's verdict either way — carries the note
"Resolved by the provider's event." and no user; resolved as failed, it does not make
the reuse rule hold the next send to its bytes, which only a person's verdict does. An event that
matches no row in flight — a duplicate, a row the probe already delivered, a failed row,
a submission not of this installation — is logged by its guid. **Every event read is
acknowledged**, so **an installation must have its provider account to itself**: another
system on the same account would lose its events. An event the database refuses — a
value the column cannot hold (SQLSTATE class 22), a constraint (class 23) or a trigger's
refusal (`P0001`) — would be refused every cycle and, the queue being first in, first
out, hold every event behind it: it is **dead-lettered**, logged at error with its guid,
reference, state and SQLSTATE (never the provider's wording), and acknowledged. Any
other failure to apply it — a lost connection, a lock timeout — and a read or an
acknowledgement that fails end the cycle unacknowledged, and the next one reads the same
event again.

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
| `KID` | an invoice's KID, appended last in phase 2 so the earlier columns keep their places; empty without one and on a credit note. Guarded as text, though a KID never begins with a character the guard is for. A spreadsheet that reads it as a number strips its leading zeros — import the column as text |

**The byte format** is the expenses payroll file's ([the payroll CSV](/en/reference/expenses/#the-payroll-csv)),
duplicated into this module as customers duplicated it — depguard keeps modules from
sharing it: UTF-8 with a byte order mark, `;` between cells, the decimal comma and two
decimals, dates as `YYYY-MM-DD`, CRLF after every row the last included, and RFC 4180
quoting — a cell holding `;`, `"`, CR or LF is quoted and its quotes doubled. Every
amount is the stored `numeric` as exact text, never a float, and a credit note's `0,00`
stays `0,00`.

**The formula guard is on the text columns only.** `Kind`, `Delivery`, `Customer number`,
`Buyer`, `Buyer org no`, `Currency`, `SAF-T code`, `Credits number` and `KID` get an apostrophe in
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

**Payments, deliveries and transmissions are kept with the document** they hang off. A
payment registration is bookkeeping material read under the same § 13 — **unconfirmed**,
see [Payments](#payments-and-the-state-of-an-invoice) — and a delivery is the record of
when the claim was handed to the mail server, not of its receipt; none is ever deleted,
and their foreign keys refuse a document's deletion. An EHF transmission's UBL is the
sales document as it was sent, as the PDF is, and the receipt and delivered copy its
evidence stores are the record of the transmission: like the PDF, they are kept five
years after the end of the financial year, and the module never deletes an object
([Sending as EHF](#sending-as-ehf)).

The module fills both customer slots ([module boundaries](/en/contributing/module-boundaries/)):

- **Merging customers** (`contracts.CustomerReferenceHolder`) re-points every document of
  the absorbed customer, drafts and issued, reported as `invoices.invoices`. An issued
  document keeps its buyer snapshot — the id is not printed, the snapshot is — and its
  revision. Payments, deliveries and transmissions hang off the document by id and
  carry no customer id, so they follow it and are not reported.
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
  free text about them — its `deliveries`, each with its recipient, sent time and
  subject, and its `transmissions`, every EHF transmission of it, the oldest first,
  each with its id, document type, status, provider, the receiver's Peppol id, the
  idempotency key, the UBL's SHA-256, the time it was queued, submitted, delivered,
  failed or cancelled, a resolution's note, and the reason as the API answers it —
  e-mail addresses and participant ids redacted, never `last_error` as stored. No
  bytes are exported: not the UBL, not the evidence, not their object keys, not the
  provider's reference. A draft has none of the three.
- **Anonymisation**, inside the customers module's transaction, in this order: it locks
  the person's documents `FOR UPDATE`, newest first — the merge holder's statement, the
  module's lock order — so a delivery insert, whose trigger takes the document `FOR
  SHARE`, waits for it; writes the marker in `invoices.erased_customers`; blanks the
  recipient of every delivery of those documents; blanks the note of every payment of
  them, live and removed; cancels every `queued` transmission of them that was never
  attempted (`submit_attempted_at` NULL), leased or not — a worker holding one stamps
  its marker only on a row still `queued`, so it finds the row cancelled and makes no
  call; and deletes the drafts. It reports five kinds, in this order:
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
    the record of when the claim was handed to the mail server, the address gone;
  - `invoices.transmissions` — the transmissions cancelled, `cancelled_at` the
    anonymisation's time: one never attempted has sent nothing, so it is not sent after
    the person is gone. Every other row is kept untouched — a `queued` one whose crash
    marker is set (its bytes may already be with the provider, so the worker settles
    it), and every `submitted`, `delivered`, `failed`, `unconfirmed` and `cancelled`
    one: the UBL is the sales document under § 13 and carries the buyer snapshot, and
    the receiver's Peppol id is an organisation's or the snapshot's own. Its resolution
    note stays, the audit trail of a person's verdict, as a payment's removal reason
    does. The insert trigger refuses any later transmission for the customer.

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
| `invoices:access` | no | Use the app; read every invoice, credit note, PDF, payment and delivery, every document's EHF state and transmissions and download their UBL, the journal, the CSV export and the stats. |
| `invoices:create` | no | Create, edit and delete drafts; preview a draft. |
| `invoices:issue` | yes | Issue a draft; create a credit-note draft; send an issued document by e-mail, and see where each send went; send it as EHF, cancel a transmission never attempted and resolve an unconfirmed one. |
| `invoices:manage` | yes | The seller record and its Peppol id, the series start, the KID agreement, VAT codes and their rates, and the access point's credentials. |
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

**Sending as EHF is under `invoices:issue` too** ([Sending as EHF](#sending-as-ehf)),
and needs an installation that can: `GET /meta` answers `ehfAvailable` — `INVOICES_EHF_ENABLED` on, the Peppol lookup
enabled (`PEPPOL_LOOKUP_ENABLED`; a send that cannot re-check its receiver does not
send), an access-point credentials row stored and the seller's Peppol id set, all four
([configuration](/en/admin/authentication/#transport-storage-and-modules)) —
`capabilities.canSendEhf`, `invoices:issue` and `ehfAvailable`, and
`accessPointCredentialsRejected`, whether the provider refused the stored key. A refused
key is reported beside `ehfAvailable`, never folded into it. Meta asks the Peppol network
nothing; the receiver is re-checked when a document is sent.

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
| `GET /settings/access-point` | `invoices:manage` | none: 200 with `hasCredentials: false` when nothing is stored |
| `PUT /settings/access-point` | `invoices:manage` | 400 on `provider`, `legalEntityId` or `apiKey` (blank, too long, or omitted while none is stored); 409 `transmissions_active` on a provider switch; 503 `ehf_unavailable`, a kept key that cannot be opened |
| `DELETE /settings/access-point` | `invoices:manage` | 409 `transmissions_active` |
| `POST /settings/access-point/verify` | `invoices:manage` | 409 `ehf_unavailable`, no credentials; 503 `ehf_unavailable`, a stored key that cannot be opened |
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
| `POST /{id}/send-ehf` | `invoices:issue` | 429 `rate_limited`; 503 `ehf_unavailable`; 404; 409 `invoice_draft`, `customer_anonymised`, `no_peppol_id`, `buyer_reference_missing`, `ehf_already_sent`; 503 `storage_unavailable`; 500 a missing or altered stored PDF, a render that fails or breaks an invariant; 409 `ehf_invalid` (with `rules`); 502 `peppol_lookup_failed`; 409 `peppol_not_receivable` (with `peppolRegistered`, `peppolCanReceive`); 500 a missing or altered reused UBL; 503 `storage_unavailable`; then under the lock 409 `customer_anonymised`, 503 `ehf_unavailable` when the credentials vanished, 409 `ehf_already_sent` |
| `POST /{id}/transmissions/{transmissionId}/cancel` | `invoices:issue` | 404 the document, or a transmission not its own; 409 `transmission_not_cancellable` |
| `POST /{id}/transmissions/{transmissionId}/resolve` | `invoices:issue` | 400 on `outcome` (not `delivered` or `failed`) or `note` (empty, over 500); 404 the document, or a transmission not its own; 409 `transmission_not_resolvable` |
| `GET /{id}/transmissions/{transmissionId}/ubl` | | 404 the document, or a transmission not its own; 500 a missing or altered stored UBL; 503 `storage_unavailable` |
| `GET /journal` | | 400 `from` or `to` missing or not a calendar date, `from` after `to`, paging |
| `GET /export.csv` | | 400 `from` or `to` missing or not a calendar date, `from` after `to`, more than 5000 rows |
| `GET /stats/summary` | | 400 `from` after `to` |

## What comes next

- **3** (next): hours, expenses and milestones turned into lines, with a write-back
  contract.
- **4**: payment files matched on KID — every invoice issued under an agreement carries
  one — reminders and late interest, and overpayment, customer credit balances and
  refunds as a flow, which 1B refuses or only shows as a figure.
- **5**: energy consumption billing.

Open in phase 2: whether the PDF embedded in the submitted UBL survives Storecove's
regeneration is unproven until the tagged sandbox test (`go test -tags storecove
./internal/invoices/accesspoint/`) has run against Storecove with a sandbox key; and the
Peppol artefacts are pinned at `v3.0.20`, with 3.0.21 adopted — a pin bump, the goldens
re-validated — the day OpenPEPPOL tags it.

Left out of phase 2 on purpose: receiving e-invoices (2030; another module), the Peppol
Invoice Response and status beyond the four states, Storecove's push webhooks, a second
provider, running as one's own access point, eFaktura and AvtaleGiro, several KID
lengths on one agreement and a KID per customer, a unit-code column or picker, foreign
currency in the EHF, VAT category K, Schematron at runtime, resending a `delivered`
document, sending a draft, bulk sending, the document-level allowance and the corrected
invoice (type 384).

Left out of 1B on purpose: editing a payment (remove it and register it again), a
payment in another currency than the document's, a payment against a credit note, one
payment allocated across several invoices, an idempotency key, HTML mail, a logo, an
editable template or a personal message in the mail, sending through an outbox or a
worker, honouring `communications.suppressions`, bulk sending, a "paid" stamp on the PDF
(the PDF is immutable), timeseries and attention stats, per-currency stats, a public-body
fact on the directory, user display names on payments and deliveries (ids only), and a
purge of anything.
