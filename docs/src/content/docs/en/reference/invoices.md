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
bank agreement; phase 3
([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-05-invoices-work-to-invoices-design.md))
turned the work other modules record — hours, expenses, billing milestones — into
lines, marked invoiced in their own modules by the issue and released by the credit
note that returns them ([Invoicing work](#invoicing-work)). Vantigo stays a sub-ledger: there is
no general ledger and nothing is posted — a payment here is a registration, not a
posting, made by hand or from a bank file's line matched on its KID ([Matching](#matching)).

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
| `invoices.settings` | One row: the seller record (legal name, organisation number, VAT registration, Foretaksregisteret, address, bank account, IBAN/BIC, e-mail, footer), the default terms and currency, `series_start`, the seller's Peppol id `peppol_id`, the KID agreement `kid_length` and `kid_algorithm` (a pair or both NULL, `ck_settings_kid`), the VAT code each kind of work is invoiced at — `work_vat_code_hours`, `work_vat_code_expenses`, `work_vat_code_milestones`, each a code that exists, all 1 (`3`, 25 %) by default — and the timesheet's default `timesheet_default` (off) and person label `timesheet_person_label` (`initials`, the default, `number` or `name`). |
| `invoices.counters` | The one counter row, `documents`; it exists exactly when something has been issued. |
| `invoices.vat_codes` | The tenant's codes: label, name, SAF-T code, UNCL5305 category, exemption reason, active. |
| `invoices.vat_code_rates` | Each code's rates as dated periods that never overlap (an exclusion constraint). A rate change is a new period, not a new code. |
| `invoices.invoices` | Drafts and issued documents: kind, status, number, customer, delivery, references, notes, the buyer snapshot and the seller snapshot (written at issue), the totals, the stored PDF's key and SHA-256, an invoice's `kid` with the `kid_algorithm` it was computed with (set at issue, both or neither, never on a credit note), the project its work belongs to — `project_id` and its code `project_reference`, both or neither (`ck_invoices_project`), derived by every save of an invoice draft and copied by a credit note ([The project](#the-project)) — and the `timesheet` flag (off by default). Every one of them is frozen at issue with the rest of the row. |
| `invoices.lines` | Description, quantity (3 decimals), unit, unit price (4), discount (2), VAT code, the computed gross, allowance and net, the credited line on a credit note, the invoice a deduction line deducts (`deducts_invoice_id` — an unconstrained reference, deliberately without a foreign key, whose `FOR KEY SHARE` on the deducted invoice would cycle with the newest-first lock of a customer's documents; the save and the issue accept only an issued invoice of the same customer, and an issued document is never deleted), and the VAT snapshot written at issue. The quantity is above 0, or below 0 on a deduction line only (`ck_lines_quantity`), and a deduction line carries no discount (`ck_lines_deduction_no_discount`). `(id, invoice_id)` is unique, so a child row names its line and its document together and the two never disagree. |
| `invoices.line_sources` | The work a line bills: the source's kind (`time.entry`, `expenses.entry`, `projects.milestone`) and id — opaque, the rows are other modules' — the revision it was taken at, an expense's kind (`source_subkind`, only on an expense), the project, the quantity, the source's exact amount (`numeric(22,8)`: an hour's amount carries up to eight decimals), the currency, the work's date and the state: `held` from the draft, `invoiced` by the issue, `released` by the credit note that returns its line. Its line and document are one composite key, and the rows go with their line. A source is live — `held` or `invoiced` — on one row at most (`ux_line_sources_live`). A row is written `held`; under a draft its one change is to `invoiced`, under an issued document its one change is from `invoiced` to `released`, once, nothing else changed. A row is deleted only under a draft and only while `held`, so dropping a hold never frees an invoiced source. A trigger refuses the rest (`invoices: a line source is written held`, `invoices: a line source changes only its state`, `invoices: a line source is deleted only while held`, `invoices: issued document is immutable`). Every save of a draft deletes its lines and so its held rows, and inserts the rows it carries anew under the new lines, in one statement ordered by kind and id ([Invoicing work](#invoicing-work)). |
| `invoices.line_releases` | A credit note's release of an original line's source: the credit note, its line and the source, a source released once. Frozen with the credit note at its issue. |
| `invoices.timesheet_rows` | The timesheet as printed, a snapshot: the position, the time entry, the person's label, the date, the hours, the work type and the description — never the entry's note. Written whole while the document's `timesheet` flag is on, pruned by every save to the hours the draft still holds ([The timesheet](#the-timesheet)); frozen with its document at issue, deleted with a deleted draft. |
| `invoices.vat_summaries` | An issued document's VAT per (category, rate) with its SAF-T code and reason. |
| `invoices.payments` | Money received against an issued invoice: the day it arrived, the amount and the currency (the invoice's, copied), `source` (`manual`, `ocr` or `camt054`) and the bank line of one a match took from a bank file (`ck_payments_origin`: a line exactly when the source is not `manual`, and a registering user either way), the bank's or the payer's reference, a note (`''` once the customer is anonymised, and on every registration made after), who registered it and when, and — once removed — when, by whom and why. Never deleted; never changed but by the removal, once, and that blanking. |
| `invoices.bank_import_accounts` | One row per receiving account a bank file was imported for: the `format` its files come in (`ocr` or `camt054`), set by its first import, and — once a manager changed it — the `previous_format` with `cutover_through`, the latest booking day of the account's own lines in it; who set it and when. Never deleted; an update changes only those columns ([Bank files](#bank-files-and-the-exception-queue)). |
| `invoices.bank_files` | One imported bank file: its format, the SHA-256 of its bytes and its own identity (each unique), the object key it is stored under, its size, the accounts it names, the first and last booking day of its lines, how many transactions it brought, how many of them were `duplicates` (set once by the import, from `NULL`), how many lines were `ignored` and `ignored_kinds` by kind, who uploaded it and when. Never deleted or changed but by that one write. |
| `invoices.bank_transactions` | One line of a bank file as the bank wrote it — the line reference, the format, the receiving account, the direction, `negative`, the booking, value and ordering days, the amount (above 0, NOK), the KID, the remittance text, the debtor's name and account, the archive reference, the bank's code — its `fingerprint` and `ordinal`, `duplicate_of_id` when it repeats a live line, and its state: `status` (`pending`, `matched`, `exception`, `resolved` or `duplicate`), the `reason` it was queued for, the suggested invoice and the resolution. One live line per account and fingerprint (`ux_bank_transactions_fingerprint`). Never deleted; only the state columns change, never back to `pending`, and a reason once set stays. |
| `invoices.bank_transaction_events` | What happened to a line, by whom and when — matched, queued, the queue's actions, and `reversed` on a line whose payment a reversal took back — with a reason and a note. Insert-only, but for its note blanked by an erase. |
| `invoices.deliveries` | One row per e-mail that handed an issued document over: the recipient (`''` once the customer is anonymised), the subject, the Message-ID, the SHA-256 of the PDF attached, when and by whom. Never deleted; never changed but by that blanking. |
| `invoices.manual_deliveries` | A delivery recorded by hand ([The delivery fact](#the-delivery-fact)): the invoice, `kind` (`handed_over` or `posted`), `delivered_on`, a note (`''` once the customer is anonymised), who recorded it and when, and — once removed — when, by whom and why. Never deleted; never changed but by the removal, once, and that blanking. |
| `invoices.charge_payments` | Money received against an invoice's charges, never its principal ([Charges](#charges)): the day, the amount and the currency (the invoice's), `source` (`manual`, `ocr` or `camt054`) and the bank line of one taken from a bank file (`ck_charge_payments_origin`: a line exactly when the source is not `manual`), the reference, a note (blanked as the payments' is), who registered it and when, and the removal. Never deleted; never changed but by the removal, once, and that blanking. |
| `invoices.charge_waivers` | A charge a sent letter claimed, released ([Charges](#charges)): the invoice, the letter (`reminder_id`, the same invoice's by a composite foreign key), `kind` (`fee`, `compensation` or `interest`), the amount, `interest_through` (interest only: the letter's sent day), the reason (`objection_upheld`, `claimed_in_error`, `goodwill` or `deadline_met`), a note, who and when. One fee or compensation waiver per letter (`ux_charge_waivers_letter_kind`); interest waivers may follow one another. Insert-only; the erase blanks the note. |
| `invoices.erased_customers` | The customers this module has anonymised, by id, with when: the marker a send and the delivery, payment and transmission triggers read. Never removed. |
| `invoices.access_point_credentials` | One row (`id = 1`): the access point provider (`storecove`), its settings that are not secret (`settings_json`), the API key sealed by the secrets box, `rejected_at` once the provider refused the key, and `updated_at`. Kept off the settings row every issue reads `FOR SHARE`. |
| `invoices.transmissions` | One EHF transmission of an issued document: the provider, the idempotency key, the sender's and receiver's Peppol ids, the document type and process, the submitted UBL's object key and SHA-256 and the PDF's SHA-256, the status (`queued`, `submitted`, `delivered`, `failed`, `unconfirmed`, `cancelled`), the provider's reference, the evidence's key and SHA-256, the attempt counters and the next attempt, the crash marker `submit_attempted_at`, the worker's lease, the last error, the receiver lookup it was queued under, the timestamps of each state, and the resolution of an `unconfirmed` row — a person's, with who and a required note, or the worker's own when the provider answers at last, with a note and no user (`ck_transmissions_resolution`). Every completion of a worker's claim names the status the claim saw, so a row the events worker moved meanwhile is left alone, never refused. Never deleted; only its state columns change, a failed or cancelled row not at all, and a delivered row only its lease, cadence and — once — its evidence. A trigger refuses one under a draft (`invoices: a transmission needs an issued document`) or for an anonymised customer (`invoices: the customer is anonymised`); `ux_transmissions_active` allows one queued, submitted, delivered or unconfirmed transmission per document. |
| `invoices.collection_rates` | The statutory rates as dated rows ([Collection rates](#collection-rates)): the kind (`late_interest_percent`, `b2b_compensation_nok`, `inkassosats`), `valid_from` (1 January or 1 July for the two half-yearly kinds), the value, the regulation `source_ref`, who added it (NULL for a release's seed) and when, and `release_value` with `release_source_ref` — what a later release seeded over a user's row, both or neither. One row per kind and day. Append-only: a trigger refuses every change but the release's value set once, and the deletion of a seeded row. |
| `invoices.reminder_settings` | One row (`id = 1`): [the reminder settings](#the-reminder-settings), the 2026 regime's day `inkassolov_2026_from`, the review `regime_reviewed_through` with who moved it and when, and the row's revision, who changed it and when. Never deleted. |
| `invoices.customer_reminder_policies` | One row per customer with [a reminder policy](#a-customers-reminder-policy) other than the default — keyed by the opaque `customer_id`, the mode (`normal`, `no_charges`, `none`), the note and who set it when. No row is `normal`. |
| `invoices.reminder_runs` | One reminder run ([Runs](#runs)): the day, who made it and when, the bank data's `last_booked_on` and `stale_import_acknowledged`, and `letters` and `skipped`, set together once at its end from NULL. Never deleted or changed but by that one write. |
| `invoices.reminders` | One letter of a run ([Letters](#letters)): the invoice, the run, its `sequence` on the invoice (unique per invoice), the level (`reminder` or `collection_notice`), `announces_collection`, the channel (`email` or `paper`), the recipient (`''` for paper and once the customer is anonymised), the language, who made it and when, the status (`queued`, `awaiting_print`, `printed`, `sent`, `withdrawn`, `failed`), and the facts written when it is sent or printed — `sent_on`, the deadline, the regime, the amounts, `credited` (what the invoice's issued credit notes had taken off it then, so the letter renders again as it went whatever is credited later), the interest segments and the charge notes — with its PDF's key and hash; the worker's columns — `message_id` (set once, while queued), `attempts`, `next_attempt_at`, `first_attempt_at`, the lease (`lease_id`, `lease_until`), `last_error`, `held_reason` (`collection_rates_outdated` or `collection_regime_unreviewed`), `failed_at` — and the withdrawal's. Created without facts; never deleted; its facts change only while it is not sent or withdrawn. |
| `invoices.reminder_print_batches` | One print batch of paper letters ([Paper and posting](#paper-and-posting)): `post_on`, the day its letters' facts were judged for and the only day it may be confirmed posted, who made it and when, and either the posting — `posted_on`, `posted_by_user_id`, `posted_at`, set together once — or `reprinted_at`, set once; never both. Never deleted or changed but by those writes. |
| `invoices.invoice_holds` | An invoice marked disputed ([Holds and the hand-off to collection](#holds-and-the-hand-off-to-collection)): the invoice, `kind` (`disputed`), the note, who placed it and when, and — once lifted — when, by whom, the lift's note and `charges_allowed`, the answer to whether the objection was groundless; the lift's three facts together (`ck_invoice_holds_lift`). One live hold per invoice (`ux_invoice_holds_live`). Never deleted; an update is the lift, once, or the erase's blanking of the notes — and the lift of an anonymised customer's hold keeps no note. |
| `invoices.collection_handoffs` | An invoice's hand-off to a collection agency ([Holds and the hand-off to collection](#holds-and-the-hand-off-to-collection)): the invoice, `handed_on`, the agency, its reference, a note, who recorded it and when, and — once withdrawn — `withdrawn_on`, by whom and why, together (`ck_collection_handoffs_withdrawal`). One live hand-off per invoice (`ux_collection_handoffs_live`). Never deleted; an update is the withdrawal, once, or the erase's blanking of the note. |

A document's state is not a column: `invoices.document_state(...)` derives it, see
[Payments and the state of an invoice](#payments-and-the-state-of-an-invoice).

Phase 2's schema (`00036_invoices_ehf_kid.sql`, the
[design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-03-invoices-ehf-peppol-kid-design.md))
adds the Peppol id, the KID agreement, the document's KID and the two tables in one
migration; [Sending as EHF](#sending-as-ehf) queues a transmission and its
[workers](#workers) carry it to an outcome.

Phase 3's schema (`00040_invoices_work.sql`, the
[design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-05-invoices-work-to-invoices-design.md))
adds the line sources, the releases, the timesheet rows, the deduction line, the
document's project and timesheet flag and the work settings in one migration. The
wizard adds line sources and every save of a draft carries them and derives its
project from them, the issue invoices them ([Invoicing work](#invoicing-work)), the
work VAT codes and the timesheet's default and person label are on the settings, a
final settlement deducts its a-konto invoices with deduction lines
([A-konto and the final settlement](#a-konto-and-the-final-settlement)), and an invoice
whose flag is on carries its timesheet rows into its PDF
([The timesheet](#the-timesheet)).

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
by leaving them out; null is a value. The same body carries `workVatCodes`, required
too: the VAT code each kind of work is invoiced at ([VAT codes for
work](#vat-codes-for-work)); and `timesheetDefault` and `timesheetPersonLabel`, required
and never null: whether a new invoice draft carries a timesheet, and how it names each
person ([The timesheet](#the-timesheet)). The app's settings page edits all three on its
card "Work to invoice".

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

A draft that bills work carries its sources on its lines, and every save carries them
by rule; its warnings `line_differs_from_sources`, `sources_released`, `source_changed`
and `source_not_invoiceable` are under [Invoicing work](#invoicing-work). A final
settlement's draft warns `deduction_exceeds_invoice` and `invoice_total_not_positive`
([A-konto and the final settlement](#a-konto-and-the-final-settlement)).

In the app's editor, the totals shown while a draft is being worked on are computed in
the browser by this same rule and are an estimate; the figures the server actually
saves — and, for the last credit note against an invoice, the exact reconciliation
under [Credit notes](#credit-notes) — are always the server's own computation, run
again server-side on save.

## Invoicing work

Phase 3 ([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-05-invoices-work-to-invoices-design.md))
invoices the work other modules record: Time's hour entries (`time.entry`), Expenses'
lines (`expenses.entry`) and Projects' billing milestones (`projects.milestone`). The
source rows stay the other modules'; this module keeps, per line, which of them the
line bills, and reads them only through the three billable read contracts
(`contracts.BillableHours`, `BillableExpenses`, `BillableMilestones`), each optional — a
module switched off has none.

In the app the view is the card "Uninvoiced work" above a customer's documents on the
customer page's Invoices tab, and the project page's Invoicing tab for one project; the
wizard opens from either, and a draft's editor carries its work, its timesheet and the
"Deduct earlier invoices" step ([the user guide](/en/user/invoices/#invoicing-work)).
All of it asks for `invoices:access` and `invoices:create` and nothing more
([Permissions](#permissions)).

### The uninvoiced view

`GET /work?customerId=` or `GET /work?projectId=` (`invoices:create`), exactly one of the
two (else a 400 on `customerId`), with an optional `until` date: the work not yet
invoiced, per project and per kind, as the billable reads answer it.

- **Before anything is read**, 409 `work_unavailable` when none of the three billable
  reads is composed (time, expenses and projects all switched off), and 409
  `projects_unavailable` with the projects module off — work is invoiced per project.
- **The projects**: for a customer, every project it is billed for
  (`ProjectDirectory.ProjectsForCustomer`, at most 2 000, in any status — exactly
  2 000 warns `work_truncated`, since there may be more); for a project, that one (404
  for an unknown id) and its customer. Ordered by code.
- **The work**: each composed billable read over those projects, dated on or before
  `until` when given, on the pool and never under a lock, one after the other. Time's
  hours are approved, billable and priced; Expenses' lines are ready to invoice
  (`expenses.ready_to_invoice`); Projects' milestones are ready. None is already
  invoiced. A read with more than 5 000 rows answers the first 5 000 and the view
  warns `work_truncated`.
- **What live documents hold**, from this module's own rows: work a draft holds or an
  issued invoice has invoiced (`line_sources` `held` or `invoiced`) is **listed, not
  selectable**, with `heldBy: {invoiceId, number?, status}`, and each project answers
  `heldOnDrafts: [{invoiceId, kind, count}]`.
- **The people** the hours name, through `UserDirectory.Users`, as `users: [{id,
  displayName}]`.

Each row carries `selectable` and, when it is not, `reason`:

| Reason | When |
| --- | --- |
| `held` | A live document holds it (`heldBy` names it). |
| `no_customer` | Its project bills no customer (a project's view only). |
| `non_billable` | Its project is `non-billable` — possible when a project changed model after the work was approved. Any kind. |
| `fixed_price` | An hour of a `fixed-price` project: shown as information, the hours against the plan; the project's ready milestones are what it invoices (design D14). |
| `currency` | Not in NOK, the one currency this module invoices in (D11). |

A row that is not selectable counts in no total; `totals` is the selectable work's
amount per currency. An hour answers its hours, its bill rate, its multiplier and its
effective `rate` — the bill rate times the multiplier, rounded half away from zero to
four decimals, the unit price a line bills it at — and Time's exact `amount`; an
expense its kind, description, supplier and supplier invoice number, net amount,
markup, distance and rate per km, and `billAmount`; a milestone its name, planned date,
`readyAt` and `date` (that instant's Oslo business day, the work's date) and its
effective `amount`.

| Warning | Where | When |
| --- | --- | --- |
| `work_overdue_to_invoice` | the project | Its oldest selectable work is dated before the same day a calendar month back from today in Oslo — the month's last day when it is shorter, so 31 March looks back to 28 February — an hour's or an expense's date, a milestone's `readyAt` day: merverdiavgiftsforskriften § 5-2-2's "senest en måned etter levering", the discrete rule. The continuous-service rule (§ 5-2-4) is not applied: nothing tells Vantigo which projects are continuous. |
| `currency_not_nok` | the row | It is in another currency than NOK. |
| `supplier_invoice_rebilled` | the row | A supplier invoice whose supplier (trimmed, case-folded) and number Expenses says another entry has already invoiced, or that another row of this answer repeats — both rows of the pair. Expenses allows the number twice. |
| `work_truncated` | the answer | A billable read had more than 5 000 rows, or the customer has 2 000 projects, as many as the directory answers. |

None refuses anything. The view needs `invoices:create`: whoever builds the invoice sees
the hours, the people and the rates it will state, as any issued PDF shows them to
`invoices:access` (D10). `GET /meta` answers `workAvailable` — any billable read
composed — and `work: {hours, expenses, milestones}`, which kinds.

### From work to a draft

`POST /from-work` (`invoices:create`) makes an invoice draft of the chosen work — 201 —
or, with `invoiceId` and the `revision` it was read at, adds it to an existing invoice
draft of the same customer — 200. The body: `customerId`, `sources: [{kind, id,
revision}]`, `grouping` (default `project`), `vatCodes: {hours?, expenses?,
milestones?}`, an optional `deliveryFrom`/`deliveryTo`, an optional `note` and an
optional `timesheet` — absent, the settings' `timesheetDefault` for a new draft and the
target's own flag for an append ([The timesheet](#the-timesheet)). **In order**, each before
any transaction:

1. **The body**: a customer, at least one source, each of a known kind and named once
   (400 on `sources[i]`), a known grouping, `invoiceId` and `revision` together, a
   delivery period of both days in order. An append's target: 404 unknown, 409
   `invoice_issued` once issued, 400 on `invoiceId` for a credit note ("a credit note
   adds no work") or another customer's draft. Then 409 `too_many_sources` past 5 000
   sources — the target's held work counted with the new — before anything another
   module answers is read.
2. **The settings, the billing profile and the customer gates**
   ([Drafts](#drafts)).
3. **The sources by id**, through each composed billable read, on the pool.
4. **Their projects** (`ProjectDirectory.Projects`; 409 `projects_unavailable` with the
   module off).
5. **The judgments**, each over every source before the next, the first source refused
   named in `sourceKind` and `sourceId`: a project billing another customer, or gone —
   409 `source_not_for_customer`; an hour of a `fixed-price` or `non-billable` project,
   or any work of a `non-billable` one — `source_not_selectable`; a source no longer
   answered (unapproved, not ready, invoiced, deleted) or of a kind whose module is off
   — `source_not_invoiceable`; another revision than the body's, for an hour or a
   milestone — `source_changed` (an expense's revision is display-only: a reimbursement
   moves it without changing what is billed, and its billing facts are taken as read
   now); then the currencies: more than one — `mixed_currency`, judged first — and one
   that is not NOK — `currency_not_nok`.
6. **The lines**: each kind's VAT code ([VAT codes for work](#vat-codes-for-work)), the
   people the lines name (`UserDirectory.Users`, for the `person` and `itemised`
   groupings), the grouping below. With the timesheet on, the hours an append's target
   already holds are read again by id (`BillableHours`) and every hour's person is named
   through the same one `UserDirectory.Users` read. More than 500 lines with the target's
   own — every one, a settlement's deduction lines included — is 409
   `too_many_lines` with `suggestedGrouping`, the next coarser grouping whose lines fit
   (absent when none does); a line too large for its columns is a 400 on `sources`.

Then **one transaction**: the new draft is inserted — or the target is locked `FOR
UPDATE`, still a draft and at the body's revision (else 409 `invoice_issued` or the
revision 409) — and its lines written: an append keeps the target's own lines first,
their work carried — a settlement's deduction lines with the invoice they deduct, their
-1 and their a-kontos' snapshots ([A-konto and the final
settlement](#a-konto-and-the-final-settlement)) — and adds the new ones after, the totals
computed again. Under that
lock the wizard reads whether another live document holds or has invoiced any of the
new work and refuses with 409 `source_held_elsewhere` — `heldBy` and the source name the
first such document — and only then holds the work, in **one statement ordered by kind
and id**. Two wizards racing for the same work end in one hold: the second either finds
the first's hold under its lock, or fails on `ux_line_sources_live`, answered with the
same 409 — never a 500, never a deadlock — and its draft is rolled back with it. With the
timesheet on, its rows are then written for every hour the draft holds, the target's and
the new alike; an append turning a target's timesheet off deletes them.

**Prefills.** A new draft's delivery period is the request's, else the work's first
and last day (an hour's or an expense's date, a milestone's ready day), so every line's
period sits inside the header's; its `yourReference` and terms are the billing
profile's, as `POST /` takes them. An append keeps the rest of the target's header, and
an append widens the target's delivery period to cover the added work, unless the
request gives one: the target's period — or its delivery day as both ends — stretched
to the added work's first and last day, written as a period; a target delivered on one
day that the added work does not leave keeps that delivery date, the period unset; a
target without a delivery takes the first and last day of all the work it then holds,
its own and the added.

**The note.** The request may give the draft's `note` (at most 1 000 characters). Without
one, when any of the chosen work was released by a credit note before, the wizard
suggests the note naming the invoice it replaces ([Release on
credit](#release-on-credit)), in the buyer's language, read on the pool before the
transaction, and cut to fit the note's 1 000 characters: whole sentences only, ending in
"…" when any was left out. An append writes a note only on a target whose note is empty;
on a target that has one, a note in the request is dropped and the target's kept.

**The project.** The draft's project is derived like any other draft's
([The project](#the-project)): after the work is held, from every line source the draft
then holds — on an append the target's own and the added — set when they share one
project and unset when they span two or more or there are none; the code comes from the
project directory read at step 4, which on an append also covers the projects of the
target's held work.

**What each held row takes** (`line_sources`): an hour its hours and Time's exact amount
(up to eight decimals); a mileage line its kilometres and bill amount; an outlay, a
supplier invoice and a milestone 1 and their amount; an expense its kind; every row the
revision, the project, the currency and the date.

**Grouping and the line text** (D4). The key is always the project and the kind of work,
then the grouping's term:

| `grouping` | Hours | Expenses | Milestones |
| --- | --- | --- | --- |
| `project` (default) | one line per project | one line per expense kind | one line each |
| `work_type` | per work type | per expense kind | one line each |
| `person` | per person | per expense kind | one line each |
| `date` | per day | per expense kind | one line each |
| `itemised` | one line per entry | one line per expense | one line each |

**A line has one unit price**, so within a key hours split further by their effective
rate: two people at two rates on one project are two lines under `project`, and an hour
of a work type with a multiplier is billed at its own price. The rate is rounded to the
four decimals `unit_price` holds; where that reaches the øre, the line's net differs from
Time's exact amount and the draft warns `line_differs_from_sources`
([The link and its states](#the-link-and-its-states)). Lines are ordered by the
project's code, then hours, expenses and milestones, then the key.

The text is in the buyer's language — English for a billing profile in English,
Norwegian otherwise, as the buyer snapshot decides:

| Kind | Norwegian | English | Quantity, unit, price |
| --- | --- | --- | --- |
| hours | "Konsulenttimer, <project>, <period>", and " – <work type>" or " – <person>" by grouping | "Consulting hours, <project>, <period>" | the hours, `timer` / `hours` (both `HUR`), the effective rate |
| hours, itemised | "Konsulenttimer, <project>, <day> – <person>[ – <work type>]" | "Consulting hours, <project>, <day> – <person>[ – <work type>]" | as above |
| outlay | "Viderefakturerte kostnader, <project>, <period>" (itemised: the expense's description) | "Re-billed costs, <project>, <period>" | 1, no unit, the bill amounts summed |
| mileage | "Kjøregodtgjørelse, <project>, <period>" (itemised: and " – <description>") | "Mileage, <project>, <period>" | grouped: 1, no unit, the sum; itemised: the kilometres, `km` (`KMT`), the rate per km |
| supplier invoice | "Viderefakturert leverandørfaktura <supplier> <number>"; two or more on one line "Viderefakturerte leverandørfakturaer, <project>, <period>" | "Re-billed supplier invoice <supplier> <number>"; "Re-billed supplier invoices, <project>, <period>" | 1, no unit, the bill amount |
| milestone | its name | its name | 1, no unit, its effective amount |

`<project>` is the project's name. `<period>` runs from the line's first to its last
work date: a whole calendar month by name ("september 2026", "September 2026"),
otherwise its days — "3. sep. 2026" / "3 Sep 2026", "1.–15. sep. 2026" / "1–15 Sep 2026",
"28. aug.–3. sep. 2026" / "28 Aug – 3 Sep 2026". The markup is Expenses' own, already
in the bill amount; Invoices adds none. No line says "utlegg"
([VAT codes for work](#vat-codes-for-work)).

### VAT codes for work

`PUT /settings` (`invoices:manage`, with its revision) carries `workVatCodes: {hours,
expenses, milestones}`, **required** — a body without it is a 400 on `workVatCodes`, so a
client that predates it cannot reset the codes by leaving it out: the code each kind of
work's lines take, each a code that exists (else a 400 on `workVatCodes.hours`,
`workVatCodes.expenses` or `workVatCodes.milestones`) and, when it differs from the
stored one, is active — a code kept as stored passes though it has since been
deactivated, so the seller record can still be saved, and the wizard refuses that
default when it would use it. All three are 1 — `3`, 25 % — until changed; `GET
/settings` answers them.

The wizard takes, per kind of work in the selection, the request's `vatCodes.<kind>`,
else the settings' — or, while the seller is not VAT-registered, id 9 (`7`, category O)
for every kind, since the issue refuses any other category then. A code given must
exist and be active (400 on `vatCodes.<kind>`); a default that has since become
inactive, with no code given, is a 400 on `vatCodes.<kind>` naming it — choose another,
or change the default in the settings. A kind the selection does not have is not
judged. A line's code is then editable like any.

The app's card "Work to invoice" on the settings page offers the active codes and the
one stored, marked "(no longer offered)" when it has since been deactivated. The
wizard shows, for each kind it is given, the code the server would take — the
settings', or id 9 (code `7`) while the seller is not VAT-registered — and always
sends the code shown for every kind chosen, so a deactivated default is the 400 above,
said under the field.

**Every re-billed expense takes the chosen code** — the main supply's rate, never the
receipt's (merverdiavgiftsloven § 4-2 (1)); the VAT Expenses records on a receipt never
reaches the line. **Utlegg** — a cost paid on the customer's behalf and passed on
outside the VAT base (§ 4-1 (2) a) — is **not supported**: no line text says "utlegg",
and a re-billed cost is a sale like any other.

### The link and its states

A line's work is its rows in `invoices.line_sources`, one per source: the kind and id,
and the snapshot the draft took of it — its revision, its project, the quantity (hours,
kilometres or 1), its exact amount, its currency, its date and, for an expense, its kind.

| State | Meaning |
| --- | --- |
| `held` | On a draft. The work is reserved for this draft: no other document can take it. |
| `invoiced` | On an issued invoice. |
| `released` | The credit note that returned its line in full gave the work back; it is uninvoiced again. |

**The floor.** A source is `held` or `invoiced` on one row at most, whatever the
interleaving (`ux_line_sources_live`); a hold an insert would duplicate is 409
`source_held_elsewhere`, naming the document that has it.

**What a save carries.** A save replaces a draft's lines, so the rows go with the old
lines; the save reads them under the document's lock first and inserts again, under
the new lines, each one a line still names — with the snapshot it had, never a figure
from the request — in **one statement ordered by kind and id**, so two transactions
holding overlapping work wait on the index in one order and one fails rather than both
deadlocking. The request names a line's work by identity only, in `lines[i].sources`
(`[{kind, id}]`):

- On a draft that holds work, **every line names its sources** — `[]` for none. A line
  that leaves the field out is a 400 on `lines[i].sources`: a client that does not know
  the field cannot drop work by omission.
- A save **never adds work**: a source the draft does not hold is a 400 on
  `lines[i].sources` ("work is added through the uninvoiced view"), and so is one named
  twice, or a kind this module does not know. A draft that holds no work takes no field
  at all.
- At most 5 000 sources on one document — one billable read's page; past it, a 400 on
  `lines` before any read.
- `POST /invoices` holds no work: a line naming sources is a 400, and so is
  `refreshSources`. A credit-note draft adds none either: sources or `refreshSources` on
  one is a 400.

**What a save drops.** Work no line names any more — left out, or on a line removed —
and, when the save changes the draft's customer, every hold the draft had (the lines'
sources are then not held: a client changing the customer sends `[]`). Deleting the draft
drops its holds with its lines. A save that drops work names it in `releasedSources`
(`[{kind, id}]`) with the warning `sources_released`; the work is uninvoiced again. A
merge that re-points the draft to the surviving customer drops nothing.

**The sources on a document.** Every document that bills work answers, from its own
rows and never a live read, `sources: {count, held, invoiced, released}` and on each line
`sources[]` — `{kind, id, projectId, date, quantity, amount, state}` — and `warnings`, the
line's own codes; a document that bills no work carries neither. A credit-note draft of an
invoice that bills invoiced work answers the block too, its counts zero, with
`wouldRelease` ([Release on credit](#release-on-credit)).

**The warnings.** None refuses a save.

| Warning | When |
| --- | --- |
| `line_differs_from_sources` | On a draft, a line's net is not its sources' amounts summed and rounded to øre — a write-down, a rounding. On the line and once on the document. |
| `sources_released` | On a save's answer, the save dropped work; `releasedSources` names it. |
| `source_changed` | On `GET` of an invoice draft, a source's billing facts changed since the draft took them: for an hour entry or a milestone its revision, project, currency or amount; for an expense its bill amount, project, currency or kind — never its revision, which a reimbursement moves without changing what is billed. On the line and once on the document. |
| `source_not_invoiceable` | On `GET` of an invoice draft, a source its module no longer answers as billable — unapproved, invoiced elsewhere, deleted. On the line and once on the document. |

**Freshness.** `GET /{id}` of an invoice draft that holds work, for a caller holding
`invoices:create`, reads its sources by id through the billable reads, on the pool and
never under a lock, and judges each one by the facts above, amounts by value. A kind
whose module is switched off is not judged; a read that fails is logged and the
warnings are left out. A reader without `invoices:create`, the list, an issued document
and a credit-note draft read nothing.

**Refresh.** `PUT /{id}` with `refreshSources: true` reads the draft's held work and the
billable reads' answer for it before the save's transaction, and the save takes, for
each source its module still answers, its current revision, project, quantity, amount,
date and kind; a source no longer answered is dropped and named in `releasedSources`,
and so is one its module now answers in another currency than the draft took it in — a
refresh never takes work into a document in a currency it was not taken in. A kind whose module is switched off is carried as it stood. Under the
lock the save requires the held work it read — the same sources at the same revisions
and amounts — or answers 409 `invoice_changed`: a save slipped in between. A draft whose
timesheet is on has it written again from the refreshed hours, every row's label with it
([The timesheet](#the-timesheet)).

### The project

A document names **the project its work belongs to** (`project_id`) with that project's
code as a snapshot (`project_reference`, at most 30 characters) — the reference its PDF
and its EHF print from the document's own row, never the directory. Both are
**derived, never written by a request** (a `projectId` or `projectReference` in a body
is ignored):

- **Every save of an invoice draft** sets them to the one project all the line sources
  the draft holds after the save share, and to NULL when they span two projects or
  there are none. Work from another project arriving on a draft that had one clears it
  at the next save; a save that drops the second project's work sets the first.
- **The code** is taken when the document's derived project last became this one, and
  is the stored `project_reference` while the project is unchanged — a later rename in
  Projects does not move it; a refresh whose work its module now answers under another
  project moves the document to that one, with its code. Otherwise it is read through
  `ProjectDirectory.Projects` **before the save's transaction**, a contract call never
  made under the lock, and only when the work the request keeps — the held work its
  lines name, at a refresh's current project — belongs to one project the draft does not
  already name. With Projects switched off, or the project gone from the directory,
  there is no code and both columns stay NULL (`ck_invoices_project`).
- **The issue freezes them** with the rest of the row; the trigger refuses a change.
- **A credit note copies its original's** at `POST /{id}/credit` and keeps them through
  its saves and its issue: it holds no work to derive one from.

`GET /invoices?projectId=` lists the documents that name a project — the derived one, so
a document whose work spans two projects matches neither. Every document and list item
carries `projectId` and `projectReference` when set. The PDF prints "Prosjekt" /
"Project" and the reference after the references ([The PDF](#the-pdf)), the EHF carries
it as BT-11 or, on a credit note, a reference of type 50
([The EHF document](#the-ehf-document)), the CSV export's last column is `Project`
([The CSV export](#the-csv-export)), and a person's export carries it on each document
as `projectReference` ([Retention and personal data](#retention-and-personal-data)).

### The write-back

The issue of an invoice that bills work marks that work invoiced in the modules that own
it, **inside the issue's own transaction**: each module's holder
(`contracts.InvoicedWorkHolder`, [rule 10](/en/contributing/module-boundaries/)) runs on
the issue's `pgx.Tx`, so an issued invoice and its stamps commit together or not at all.
In order:

1. **Before the transaction**, beside the billing profile: the draft's `line_sources`,
   on the pool. When it holds any, every kind must be claimed by a composed holder — a
   kind none claims is a composition bug, logged at error and answered 500, never a
   skipped stamp; with Projects switched off the issue fails closed, 409
   `projects_unavailable`; the projects of the held work are read
   (`ProjectDirectory.Projects`), and one gone or no longer billing the draft's customer
   is 409 `source_customer_changed`; and the issuer's name is read
   (`UserDirectory.User`, `""` for a user the directory does not know) for the holders'
   timeline events. All of it before a number exists. A draft holding no work reads
   nothing more than it did.
2. **Under the lock**, after the number and every check of [Issuing](#issuing), the KID
   included: the draft's `line_sources` again — any difference from what was read before
   the transaction (a source, its revision, its amount or its line) is a save slipped in
   between, 409 `invoice_changed`; then the projects' billing types as read: hours of a
   project now `fixed-price` or `non-billable`, and any work of a project now
   `non-billable`, are 409 `source_not_selectable`. The billing types are the ones read
   before the transaction, and the projects are not locked for them: a project turned
   fixed-price or non-billable while an issue is in flight does not stop that issue.
   The outcome is what it would have been had the change come just after the issue —
   a project's change of billing type never refuses invoiced work — and a credit note
   that returns the work releases it.
3. **The holders**, each kind's once, in the cross-module lock order
   (`contracts.InvoicedWorkOrder`): Projects' milestones, then Expenses' lines, then
   Time's entries — after this module's own locks (the document, the settings row, the
   counter), so no transaction waits on another in the opposite order. Each is handed
   its sources by id with the snapshot the draft took — revision, project, currency,
   the exact amount and an expense's kind — and the ref `{ID, Number, IssueDate,
   IssuedAt, IssuedBy, IssuedByDisplay}`: the invoice's id, number and issue date, and
   `IssuedAt`, the issue's own clock read once, which is also the document's
   `issued_at`. Each holder locks its rows in its own order and judges them as they
   stand: already invoiced is `source_already_invoiced`; no longer approved, ready or
   billable is `source_not_invoiceable`; changed since the draft took it is
   `source_changed` — for an hour entry its revision, project, currency or amount; for a
   milestone its revision, currency or amount; for an expense its billing facts and never
   its revision.
4. **Then** the document's `line_sources` move from `held` to `invoiced`, and only then
   the lines' VAT snapshots, the VAT summary and the document itself are written.

**The refusals.** Every refusal about a source is a 409 naming it — `linePosition` (the
first line holding it), `sourceKind` and `sourceId` — and rolls the whole issue back: the
number, and everything any holder wrote. A holder that fails in any other way is a 500,
rolled back the same way. A source refused is still held by the draft: refresh the work
([Refresh](#the-link-and-its-states)) or edit the line, and issue again. The contract's
`InvoicesConflictProblem` schema has one `description` for every 409 `code` across the
module; it lists only the codes the server answers today.

### Release on credit

A credit note gives work back — `invoiced` to `released`, uninvoiced again — only for an
original line it **returns in full**: with the credit notes issued before it, the line's
whole quantity credited, and every credit of the line, theirs and this one, a return at
the line's own unit price and discount — the same last return that squares the line's
øre ([Credit notes](#credit-notes)). A price reduction or a higher discount is never a
return, so a line credited that way never releases; a line credited in part releases
nothing until the rest of it is returned, and then all of its work at once, a grouped
line's every source; a milestone is released whole.

1. **Before the transaction**, the original's `line_sources` are read on the pool; when
   any is `invoiced`, the composed holders are taken and the issuer's name is read
   (`UserDirectory.User`, `""` for a user the directory does not know). An original that
   bills no invoiced work reads nothing more.
2. **Under the lock**, after the original's (the last of this module's own locks), its
   credit book and both caps: the releases are decided — the `invoiced` rows of every
   original line this credit note returns in full.
3. **At the same place as the write-back**, after every check and the number and before
   the credit note is written: `invoices.line_releases` records each release on the
   credit side — the credit note's id, its line returning the source's line, the source's
   row — so the child trigger freezes it with the credit note; the original's rows move
   from `invoiced` to `released`, the one change an issued document's row allows; and each
   kind's holder's `ReleaseInvoiced` runs once, in the lock order, on the issue's
   `pgx.Tx`, with the ref `{ID, Number, IssueDate}` of the **original** — the stamp being
   taken back — and `IssuedAt`, `IssuedBy`, `IssuedByDisplay` of the credit note's issue:
   who took it back, and when.

A credit note is never blocked by its work: a holder tolerates a source that no longer
carries the stamp, and a released source whose kind no composed holder claims is logged at
error and skipped — its stamp stays in its module — while the release is still recorded
here. A holder that fails in any other way is a 500 and rolls the credit note back, its
number with it.

**What a draft would release.** A credit-note draft of an invoice that bills invoiced
work answers `sources: {count: 0, held: 0, invoiced: 0, released: 0, wouldRelease}` — a
credit note holds no work of its own — where `wouldRelease` (`[{kind, id}]`) is what its
issue would release as the draft stands, `[]` for nothing; the issue decides it again
under the original's lock. The original answers its released rows with the state
`released`.

**Pulling released work again.** Released work is uninvoiced: no live row holds it, so a
new draft may hold it and a new invoice bill it, with **no mandatory reference** to the
credit note. The uninvoiced view lists it selectable again, and when the wizard takes it
without a note of the request's, it suggests one naming, for each piece of work, the
invoice its latest release replaced and the credit note that released it — "Erstatter
faktura <n>, kreditert med kreditnota <c>" in Norwegian, "Replaces invoice <n>, credited
by credit note <c>" in English, the buyer's language deciding — each pair once, the
newest first, joined by ". " and cut to fit the note's 1 000 characters, ending in "…"
when a pair was left out; work never released adds nothing. It is a suggestion: the
note can be edited like any.

### The races

The issue races every other writer of the rows it stamps. Each race below is proved
against the real modules by the integration package
([`work_races_test.go`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/integration/work_races_test.go)),
on a database pool of **two connections** serving only the two racing writers — so a
call that took a second pool connection while its transaction held locks would starve
and fail the test rather than pass on a larger pool. A raw transaction on a connection
of its own holds the contested row; the first writer is started and seen waiting on it,
then the second, seen waiting behind the first; the raw transaction commits. Both must
answer within their deadline, with no deadlock — none detected by Postgres either, so
not even one a writer retried away:

| The issue against | Who waits on what | When the other writer is first | When the issue is first |
| --- | --- | --- | --- |
| An expense's manual mark (Expenses' `POST /entries/{id}/invoiced`) | the expense line | the issue finds the line marked by hand under its holder's lock: 409 `source_already_invoiced`, the number rolled back | the mark finds the line stamped under its lock: 409 `invoiced_by_invoices` naming the invoice |
| A batch reimbursement of the held expense (Expenses' `POST /reimbursed`) | the expense line | both commit: a reimbursement moves the line's revision and nothing the bill reads, and the holder judges an expense by its billing facts, never its revision | both commit; the line ends reimbursed and stamped by the invoice |
| A milestone's manual move from ready to invoiced | the project row (`LockProject`) | the issue finds the milestone invoiced by hand: 409 `source_already_invoiced` | the move finds the milestone stamped, at a revision the stamp moved on: the stale-revision 409; a move at the current revision is the 400 on `status` — invoiced cannot move to invoiced |
| A fixed-price project's price edit (`PUT /projects/{id}`) against the issue of a percent milestone | the project row | the milestone's effective amount moved with the price: 409 `source_changed` | the edit commits after the issue; the invoiced milestone keeps the amount it was invoiced at |
| A time unapprove of the held entry | the entry, by id | the issue finds the entry a draft again: 409 `source_not_invoiceable` | the unapprove finds the entry invoiced and refuses it: a 400 on `ids`, "Entry n is invoiced" |
| A customers merge of the draft's customer, the draft holding a milestone of the absorbed customer's project | the number counter, which the issue waits on holding its document | — | the issue holds its document and the merge, which calls the invoices holder before projects' ([module boundaries rule 5](/en/contributing/module-boundaries/#the-rules)), waits on it while holding no project row; the issue's holder takes the project and both commit, the invoice and the project naming the survivor |

**The window the issue accepts.** The projects' billing types are read before the
transaction ([The write-back](#the-write-back)) and the issue locks no project for hours:
hours of a project turned fixed-price after that read — while the issue waits at the
entry's row — are stamped and issued, and nothing under the lock sees the change. The
outcome is the one the change would have had a moment after the issue, which never
refuses invoiced work; a credit note that returns the hours releases them. The
integration package pins this window as it stands.

### A-konto and the final settlement

An **a-konto invoice** is an ordinary invoice — any lines, typically a ready milestone —
in the same series (§ 5-1-3), its VAT due in its term (mval. § 15-9 (1)); nothing marks
it as one. A **final settlement** is an invoice that also carries **deduction lines**,
each deducting one earlier invoice of the same customer at one of its VAT codes:

- `deductsInvoiceId` on the line request names the invoice deducted; it is frozen on the
  line (`invoices.lines.deducts_invoice_id`, with no foreign key — [The model](#the-model))
  and answered on the line as `deductsInvoiceId`.
- The quantity is exactly **-1**, the unit price the amount deducted, **above 0** — so
  EN 16931's BR-27 (no negative price) holds — and the discount 0; the line's gross and
  net are then negative. The quantity may be negative only on such a line: anywhere else
  it is a 400 on `lines[i].quantity`, as is a deduction of any other quantity; a price
  of 0 is a 400 on `unitPrice`, a discount a 400 on `discountPercent`. The schema
  agrees: `ck_lines_quantity` allows `quantity < 0` only with `deducts_invoice_id`, and
  `ck_lines_deduction_no_discount` keeps its discount at 0.
- The VAT code is one the deducted invoice has a line at (a 400 on
  `lines[i].vatCodeId` otherwise), and the line is **taxed at that invoice's line's
  snapshot** — its category and rate — on the draft, in its preview and at the issue,
  never at today's rate of the code. It is therefore exempt from the checks that judge a
  code as it stands today: a code no longer offered for new lines (`vat_code_inactive`)
  or with no rate on the issue date (`vat_code_not_valid`) refuses neither its save nor
  its issue.
- The deducted document is an **issued invoice of the same customer, in the
  settlement's currency** — never a draft (the settlement itself included), a credit
  note, another customer's invoice or one in another currency: a 400 on
  `lines[i].deductsInvoiceId`.
- **A document is not deductible at a code where it carries deduction lines of its
  own** — a settlement's lines there mix today's rate with its a-kontos' older snapshots:
  a 400 on `lines[i].vatCodeId`, and `GET …/deductible` leaves that code out. A deduction line bills no work: naming `sources` on one is
  a 400 on `lines[i].sources`.
- **One deduction line per (deducted invoice, VAT code)** per draft: a second is 409
  `deduction_duplicated`, with the second line's `linePosition`, on a create or a save.
  An a-konto with lines at two codes is deducted by two lines.

The text the editor proposes is "Tidligere fakturert a konto, faktura <n>" /
"Previously invoiced on account, invoice <n>"; the server keeps whatever description
the line is given.

**The cap.** Per (a-konto, VAT code), a deduction may take at most what the a-konto has
left there: its lines' net at the code, less what its issued credit notes credited on
those lines, less what issued settlements' deduction lines took and their issued credit
notes did not give back. Drafts count for nothing. A draft past the cap warns
`deduction_exceeds_invoice`; its issue is refused with the same code and the line's
`linePosition`. The issue reads the cap **after the number is allocated, and never
row-locks the invoices it deducts**: every write that changes what an a-konto has left
is itself an issue — a credit note of it, another settlement, a credit note of a
settlement — and the counter serialises every issue, so the cap read under it is exact;
a row lock would cycle with the customers merge, which locks a customer's documents
newest first, whenever an a-konto is newer than the settlement's draft. **A save locks
the draft only, and a deduction line takes no lock on the deducted invoice** — nor does
the creation of a settlement's credit note, which locks its original: the column has no
foreign key. The issue re-checks that each deducted document is still an issued invoice
of this customer in its currency with a line at the line's code, and that it does not
deduct earlier invoices at that code itself; one that is not, or does, has nothing left
to deduct for it, and is refused `deduction_exceeds_invoice` with the line too — the
detail says which.

**A settlement's gross must be positive.** A draft whose deductions take as much as it
bills, or more, warns `invoice_total_not_positive`, and its issue is refused with it.
A zero settlement could never be corrected — a credit note is refused once nothing is
left to credit (`invoice_fully_credited`) — so neither it nor the a-konto it deducted
could ever be credited; a negative one is a credit in substance. A fixed price billed in
full on account ends with its last a-konto, not a zero settlement.

**What is left to deduct.** `GET /invoices/{id}/deductible` (`invoices:create`) answers,
for an invoice draft, every issued invoice of its customer with something left to deduct,
per VAT code: `invoiceId`, `number`, `issueDate`, `vatCodeId`, the snapshot's `category`
and `ratePercent`, and `left`, by number and then code. The draft itself is never one,
nor an invoice in another currency than the draft's; a settlement is listed like any
invoice, but never at a code where it deducts itself. An
unknown id is a 404, an issued document 409 `invoice_issued`, and a credit-note draft
409 `credit_note_deducts_nothing`: a credit note deducts nothing. It is the editor's
"Deduct earlier invoices" step, which proposes one line per chosen row — the text below,
in the buyer's language from the billing profile, else the reader's,
-1 at the amount chosen (above 0, at most `left`), the row's VAT code — and shows a pair
the draft already deducts without offering it again. On a deduction line the editor
does not let the quantity, the discount or the VAT code be edited
([the user guide](/en/user/invoices/#final-settlement)).

**Credit notes and deductions.** A credit note of a settlement copies its deduction lines
as they are — the negative quantity, the price, the deducted invoice — so its issue
gives the deduction back and the a-konto's cap grows again ([Credit notes](#credit-notes)).
Crediting an a-konto a settlement deducted is judged per VAT code: at a code an issued
settlement deducted, a credit note may take no more than the a-konto has left there;
past it the issue is refused `invoice_deducted`, its detail naming the settlements —
credit the settlement first. At a code no settlement deducted, the line caps alone
decide.

**On the documents.** The PDF prints a deduction as it prints any negative — a leading
minus on the quantity and the line amount, "-1" and "-125 000,00" ("-125,000.00" in
English), the unit price positive — and lists the invoices deducted under the
references, "Fratrukket a konto: Faktura 985 av 01.08.2026" / "Deducted on account:
Invoice 985 of 2026-08-01" ([The PDF](#the-pdf)). The EHF carries one
`cac:BillingReference` per invoice deducted (BG-3) and the deductions as negative lines;
it writes no `PrepaidAmount` ([The EHF document](#the-ehf-document)).

### The timesheet

An invoice may carry a **timesheet** inside its PDF (the design's D5): one row per hour
entry the invoice holds — the date, the person, the work type, the description and the
hours — so the customer sees what the hours lines bill. It is optional per invoice: the
document's `timesheet` flag, off unless asked for, which the settings'
`timesheetDefault` turns on for every new invoice draft whose request leaves it out — the
wizard's, and one created by hand with `POST /`. A credit note carries none (`timesheet:
true` on a credit-note draft is a 400 on `timesheet`). `PUT /settings` (`invoices:manage`,
with its revision) carries `timesheetDefault` and `timesheetPersonLabel`, both
**required** — a body without either, or with null, is a 400 on it, so a client that
predates them cannot reset them by leaving them out — and a label other than `initials`,
`number` or `name` is a 400 on `timesheetPersonLabel`, whether the parse or the column's
CHECK (`ck_settings_timesheet_person_label`) refuses it. `GET /settings` answers them,
`false` and `initials` until changed.

**What a row holds.** The rows are `invoices.timesheet_rows`, a snapshot taken when they
are written: the hours as Time's billable read answers them (`contracts.BillableHour`),
the work type's name, and as the **description the task title, else the project's
name** — the hour's project, else the one its held row took — else the work type, and
only then nothing — **never the time entry's note**. The note is the person's own text and may hold
health data ("legetime", a sick child), and the billable read does not carry it, so it
never reaches this module. The rows are ordered by date, then by the entry; a total per
person and the whole close the block.

**The person** is labelled by the settings' `timesheetPersonLabel`, applied when the rows
are written:

| Label | Prints |
| --- | --- |
| `initials` (the default) | the display name's initials — Kari Nordmann is "KN"; a second person whose initials are "KN" is "KN2", a third "KN3", in order of first appearance on the timesheet |
| `number` | "Person 1", "Person 2", … in order of first appearance on the timesheet — Vantigo stores no employee number |
| `name` | the display name; a second person of the same name gets " 2" |

No two people on one timesheet ever share a label — the totals are per label — so a
suffixed label that is already someone's own takes the next free number ("K N 2" is
"KN2" too; after Kari Nordmann and Knut Nilsen it is "KN22"). A person the user
directory no longer knows is "?". The names are read through
`UserDirectory.Users`, on the pool, before the writer's transaction — never under its
lock.

**When the rows are written.** Each write is whole — every row deleted, then one per
hour the draft holds that Time's billable read answers, every person labelled afresh — so
a timesheet never carries two numbering schemes:

- **The wizard** (`POST /from-work`) with the timesheet on writes them for every hour
  the draft holds once the work is added — on an append the target's own hours, read
  again by id, among them.
- **A save turning the flag on** (`PUT /{id}` with `timesheet: true` on a draft whose
  flag is off) reads the held hours through `BillableHours` by id and their people,
  before its transaction, and writes them.
- **A refresh** (`refreshSources: true`) writes them again from the refreshed hours, every
  label with them.

Otherwise **every save prunes** them to the hours the draft still holds — an hour dropped
from its line loses its row and the rest are written again densely, positions from 1, in
the same transaction; on a timesheet labelled by `number` the "Person n" labels are
renumbered by first appearance among the rows left, with no directory read, while an
`initials` or `name` label stays as written (a snapshot), so a "KN2" may remain without
a "KN". A customer change, which drops every hold, empties the timesheet; **a save turning the flag off** deletes
them; one leaving `timesheet` out keeps the flag. An hour the billable read no longer
answers gets no row — the issue refuses it anyway (`source_not_invoiceable`). With Time
switched off there is nothing to write rows from: a save prunes them and turning the flag
on writes none.

**Frozen at issue.** The flag is frozen with the document and the rows by the child
trigger (`refuse_issued_child_change`), so an issued invoice's timesheet is exactly what
its stored PDF printed. Nothing identity later does to a user — renaming, disabling or
deleting them — touches a snapshot, and neither does a change of the label.

**The employees' data.** A timesheet discloses employees' work to a customer. The basis
for that is the employer's — the installation's — under GDPR art. 6(1)(f) or (b), and the
notice art. 13 requires is the employer's to give its employees; the user guide says so.
The minimised label, `initials`, is the default (art. 25(2)); `name` is an opt-in on the
settings. The rows are kept with the document — **five years after the end of the
financial year** (bokføringsloven § 13, the conservative reading) — and an issued
document's rows are part of the sales document, which art. 17(3)(b) exempts from
erasure ([Retention and personal data](#retention-and-personal-data)).

## Issuing

`POST /invoices/{id}/issue` runs one READ COMMITTED transaction in a fixed order:
lock the document, share the settings row, allocate the number, and only then check
every rule — the counter row is what serialises two issues, so every check that
depends on other documents runs after it; then, for an invoice that bills work, the
holders mark it invoiced ([The write-back](#the-write-back)), and for a credit note that
returns a line of work in full they take its stamps back ([Release on
credit](#release-on-credit)); then the lines' snapshots,
the VAT rows and the document are written. The directories — the customer's billing
profile, and for work the projects and the issuer's name — are read before the
transaction and the object store is used after it; none is ever called under a lock. The
lock order is always document → settings → counter → original → the source modules'
rows (Projects, Expenses, Time), and nothing takes them in another order — a
settlement's deducted invoices are read after the counter and never locked, and its save
and its credit note's creation lock only the draft and the original ([A-konto and the
final settlement](#a-konto-and-the-final-settlement)): `PUT /settings` takes only the settings row, the rate operations the
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

**The receivables' lock order** (phase 4, design D18) keeps that invariant and adds one
rule: **own row first, then invoices in descending id, never the reverse.** Every new
path takes its row locks through one helper each, which reports them to a lock-order
seam its tests pin.

| Path | Locks, in order |
| --- | --- |
| a bank import | the file's account rows, in account order (`FOR SHARE`), then the inserts |
| an account's format change | the account row alone |
| a bank match | the line `FOR NO KEY UPDATE`, then its invoice (a deadline-met waiver inserted) |
| the queue's apply and handle-reversal | the line, then its invoices — those it names and those its payments were registered against — in descending id |
| dismiss, confirm-duplicate | the line, then — when its payments or charge payments were registered against any — those invoices in descending id, to judge its note after an erase |
| treat-as-distinct, reopen | the line alone |
| a charge payment, a waiver, a manual delivery and its removal | the invoice alone (the children's insert triggers share it) |
| a hold, a lift, a hand-off and its withdrawal | the invoice, then its letters (withdrawn), and on a barring lift the waivers inserted |
| a reminder run's item | the invoice, then the letter inserted |
| the reminder worker's dispatch | the invoice, then its letter |
| a print batch's letter | its own batch `FOR SHARE`, then the invoice, then the letter, then the collection rates of its day `FOR SHARE` |
| a batch posted | the batch `FOR NO KEY UPDATE`, then its letters' invoices in descending id, then the letters |
| a batch reprinted | the batch `FOR NO KEY UPDATE`, then its printed letters — no invoice ([Paper and posting](#paper-and-posting)) |
| a collection rate's delete | the rate row alone |
| a letter's withdraw or retry | the letter alone |
| a policy `PUT` | the customer's documents `FOR SHARE`, newest first, then the policy row; the merge: the documents, then the policy rows by customer id |
| the erase | the person's documents `FOR UPDATE`, newest first, then their letters, then their children's notes, then the resolved bank lines (below) |

**No path locks an invoice and then a bank line, an account row or a print batch**, so
the own-row-first orders cannot cycle with each other or with the invoice-only and
descending paths of payments and credit notes. **The one named exception is the
erase** ([Retention and personal data](#retention-and-personal-data)): holding the
person's documents, it blanks the `resolution_note` of the bank lines linked to their
payments — but only **`resolved`** lines with a **non-empty** note. No path locks a
resolved line before an invoice — `apply` and `handle-reversal` lock `exception` lines,
`dismiss` and `confirm-duplicate` lock an `exception` or `duplicate` line and then the
invoices its payments were registered against, `reopen` locks its line alone; the line
any of them holds is never `resolved` while it takes an invoice lock, since it resolves
the line only after — and the erase's statement skips, without waiting, a line that is
not resolved, so the documents-then-line order can never meet a line-then-invoice order
on the same line. Its blanking of the lines' event notes locks only event rows, which
nothing else locks — the queue only inserts them — and no line row. Likewise its recipient blanking touches only a letter whose
recipient is not blank already, so it never waits on a printed paper letter a reprint
holds without its invoice. A removal of an imported payment locks only the invoice and
never writes its line. Every directory read, object-store call and SMTP send is made
outside any transaction, read-only snapshots included.

The checks, each a 409 that rolls the number back: `seller_incomplete`, `no_lines`,
`delivery_date_missing`, `issue_date_not_allowed` (with `allowedIssueDates`); for an
invoice the customer gates, `buyer_incomplete`, `vat_code_inactive` and
`vat_code_not_valid` (with `linePosition`), `vat_not_registered` (a seller outside the
register issues only O lines), `category_o_not_allowed` (a registered seller issues no O
line), `reverse_charge_needs_org_number`, `vat_codes_ambiguous` and, under a KID agreement,
`kid_length_exceeded` (the allocated number no longer fits a shortened agreement), and for
a final settlement `deduction_exceeds_invoice` (with `linePosition`) and
`invoice_total_not_positive` — a deduction line is exempt from the two VAT code checks,
taxed at its a-konto's snapshot; for a credit note
`credit_exceeds_line` (with `linePosition`), `credit_exceeds_invoice`,
`credit_total_negative` (its gross below zero) and `invoice_deducted` (an a-konto credited
past what a settlement left of it); last, for an
invoice that bills work, `invoice_changed` (its work changed since the reads before the
transaction), `source_not_selectable` and a holder's `source_already_invoiced`,
`source_not_invoiceable` or `source_changed` (each with `linePosition`, `sourceKind` and
`sourceId`). Before the transaction: `invoice_issued`, for work `projects_unavailable` and
`source_customer_changed` (with the source), and 503 `storage_unavailable` when no object
store is configured — an issued number whose PDF could never be stored is not allowed to exist.
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
law. A credit note adds no work, and one that returns a line of work in full releases
that line's work in its issue ([Release on credit](#release-on-credit)).

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

**A settlement's credit note** copies its deduction lines with their negative quantity and
the invoice they deduct ([A-konto and the final
settlement](#a-konto-and-the-final-settlement)). A credit line is negative **exactly when
the line it credits is a deduction line** — decided once the original is read, a 400 on
`lines[i].quantity` otherwise — and `deductsInvoiceId` on a credit note's own request is a
400: its lines deduct what the lines they credit deducted. Every comparison of a credit
line with its line is **by magnitude and of the same sign**: lowering a quantity (a
deduction's -1 to -0.5, never to -1.5 or +1), the per-line cap across the issued credit
notes, and the last return that squares the line. A credit note whose gross is **below
zero** — a settlement's deduction credited without its work — is refused at the issue,
`credit_total_negative`; a zero one stays allowed.

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

**Where a payment came from.** Every payment answers `source` — `manual` for one
registered here, `ocr` or `camt054` for one a bank line was matched or applied to
([Matching](#matching)), with `bankTransactionId` naming the line — and
`registeredByUserId`, the person who registered it: the caller here, the uploader or the
caller of `…/match` for a match. The wire makes `registeredByUserId` optional, for a
later source with no person; every payment of this release has one. An imported payment
is removed like any other, with a reason — how a refund made outside Vantigo is
recorded — and the removal locks only the invoice and never touches the line: what a
line has applied is derived from its live payments, so a line whose payments are all
removed goes back to a person through [the exception queue](#the-exception-queue)'s
reopen — unless a reversal removed one, which marks the line so its money is never
applied again.

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

**What comes next.** An issued invoice also answers `nextAction`, what the reminder engine
says to do with it today — a reminder or the collection notice with the letter as it
would go, a suggested hand-off, or why it waits or is blocked ([The rules](#the-rules)) —
and `reminders`, its letters ([Runs](#runs)). A paid invoice answers `none`.

## Charges

Phase 4 ([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-06-invoices-payments-reminders-design.md),
D9) adds what a reminder claims beside the invoice — a reminder fee or the compensation,
and late interest — and keeps it apart from the invoice's own amount.

**Not principal.** Fees and interest are never folded into the principal
(Finanstilsynet's letter of 2020): the open amount, the state and the payments
([Payments and the state of an invoice](#payments-and-the-state-of-an-invoice)) stay
principal-only, and nothing here changes them. What a letter claims lives on the letter,
written when it is sent: its `fee` (the reminder fee, `fee_kind = reminder_fee`) or its
`compensation` (the § 3a compensation, `fee_kind = compensation`), never both, and its
`interest`, the cumulative late interest from the day after the due date to the letter's
date. **Only a sent letter claims**: one still queued, awaiting print, printed or failed
claims nothing yet, a withdrawn one never.

**What is outstanding.** An invoice's charges outstanding are

```text
  Σ fee + Σ compensation over its sent letters − their waivers
+ the latest sent letter's cumulative interest − Σ interest waivers
− Σ its live charge payments
```

— the same terms a letter's own total states, so the letter and the invoice never
disagree. The formula is `reminderrules.Charges`, one pure function, which the
document and every charges write call. Below zero — a charge paid, then waived — the
invoice answers **`refundDue`**, what is owed back, and nothing is outstanding; the
refund is made outside Vantigo.

**Allocation within charges.** A charge payment pays the fees and the compensation
first, the oldest letter first, each net of its waivers, then interest — at most the
latest sent letter's interest less the interest waivers, so no payment is ever counted
as interest twice — and what is left is `refundDue`. The allocation is **recomputed over
every live charge payment** whenever it is read, and **each letter freezes the figures it
printed** (its `charges_earlier`, `interest_paid` and total, as they stood when it was
sent). So a surplus that was `refundDue` against the first letter is absorbed by the
interest a later letter claims on the same invoice. That is the same invoice's charges
meeting each other, not a set-off against another invoice, which phase 4 does not do.

**The block.** An issued invoice answers `charges` — `claimed` (the sent letters' fees
and compensation and the latest one's cumulative interest, before waivers), `waived` (every waiver's amount), `paid` (the live charge payments),
`outstanding` (never below zero), `refundDue` only above zero and `interestToday` — the
late interest accrued to today as a letter sent today would claim it, cumulative and before
waivers and what is paid of it, present only when interest applies and the rates cover the
period ([The rules](#the-rules)) — with `chargePayments`
(every one, removed ones included with their removal, in the order the money arrived)
and `waivers` (the first first). A draft and a credit note answer none of them: a credit
note is never reminded of.

**Charge payments.** `POST /invoices/{id}/charge-payments` (`invoices:payments`)
registers money received against the charges: a payment's fields and rules
([Payments](#payments-and-the-state-of-an-invoice)) — 404; 409 `credit_note_no_payments`
for a credit note, draft or issued, and `invoice_draft` for an invoice draft, judged
before the body; 400 on `paidOn` (from the issue date to today, Oslo), `amount` (above 0,
two decimals, the document bound), `reference` or `note`. Then one transaction locks the
invoice `FOR UPDATE` — the only row it locks — reads the sent letters, the waivers and
the live charge payments after the lock and refuses 409 **`no_charges_outstanding`** when
nothing is outstanding (no letter sent, everything waived or paid, or a refund due) and
409 **`charge_payment_exceeds_outstanding`** above what is, its problem carrying
**`chargesOutstanding`**. The currency is the invoice's. A charge payment registered by
hand is `source = manual`; `ocr` and `camt054` name the bank line it was taken from
(`bank_transaction_id`, `ck_charge_payments_origin`). `POST
/invoices/{id}/charge-payments/{chargePaymentId}/remove` takes a `reason` and removes one
as a payment is removed: the reason judged first (400), the invoice locked, then the
charge payment read — another document's is a 404, one removed already 409
`payment_removed`. A removal of a charge payment taken from a bank line writes nothing to
the line. `tr_charge_payments_immutable` refuses a DELETE and every UPDATE but the removal,
once, and the erase's blanking of the note; `tr_charge_payments_parent` refuses a row
under anything but an issued invoice and blanks the note of an anonymised customer's.

**Waivers and their reasons.** `POST /invoices/{id}/charges/waive`
(`invoices:payments`) takes `{waivers: [{reminderId, kind}], reason, note}` — one to 50
waivers, `kind` `fee`, `compensation` or `interest`, `reason` **`objection_upheld`** (the
debtor's objection was right), **`claimed_in_error`** or **`goodwill`**, and a note of at
most 500 characters; anything else is a 400. The fourth reason, **`deadline_met`**, is reserved
for the bank match (a fee claimed after a deadline the payments in fact met) and is never
taken from a person. 404; 409 `credit_note_no_reminders`, `invoice_draft`. Then, under the
invoice's lock, each waiver is judged in order against what the earlier ones left, and
one refusal writes none of them:

- a letter that is not the invoice's is a 404;
- **a fee or the compensation is waived whole** — whatever was paid of it, which then
  becomes a refund due — once per letter (`ux_charge_waivers_letter_kind`); it is 409
  **`charge_not_claimed`** when the letter was not sent, claimed no such charge, or it is
  waived already;
- **interest is waived as an amount**: the interest the latest sent letter claimed less
  every earlier interest waiver and the charge payments allocated to interest — what is
  claimed and unpaid, nothing accrued since. It names the latest sent letter, and the
  waiver records that letter's sent day as `interest_through`. An earlier letter, or no
  interest left unpaid, is `charge_not_claimed`. Interest waivers may follow one another:
  20 claimed and 12 paid waives 8; a later letter claiming 33 waives 33 − 8 − 12 = 13.

A waived charge leaves the charges outstanding and every later letter's
`charges_earlier`; the letters themselves are history and keep what they said. A waiver is never removed or changed
(`tr_charge_waivers_immutable`; the erase blanks its note), its letter is the same
invoice's (the composite foreign key onto `(id, invoice_id)` of the reminders), and
`tr_charge_waivers_parent` refuses one under anything but an issued invoice.

**VAT and bookkeeping.** A statutory fee and late interest are outside the VAT base
(merverdiavgiftsloven § 4-1 (2) b and c); the § 3a compensation very likely too — that
reading is **unconfirmed**. A reminder takes no number from the invoice series and is not
treated as a salgsdokument — also **unconfirmed** as a statement, an inference from
bokføringsforskriften § 5-1-1. The sent letter and its row are the documentation of the
claim, and a waiver the documentation of its release (bokføringsloven § 10). Charges are
not exported in phase 4.

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

**The project** a document's work belongs to prints in the meta block after the
references, "Prosjekt" / "Project" and its reference, when the document names one
([The project](#the-project)).

**A final settlement** lists each invoice it deducts after them, "Fratrukket a konto" /
"Deducted on account" with "Faktura 985 av 01.08.2026" / "Invoice 985 of 2026-08-01",
and prints a deduction line with a leading minus on its quantity and its amount ("-1",
"-125 000,00"), the unit price positive ([A-konto and the final
settlement](#a-konto-and-the-final-settlement)). A credit note of a settlement lists
none: its one preceding invoice is the settlement.

**The timesheet** of an invoice whose flag is on prints after everything else, on pages
of its own: "Timeliste" / "Timesheet", the columns "Dato", "Person", "Arbeidstype",
"Beskrivelse", "Timer" / "Date", "Person", "Work type", "Description", "Hours", one line
per stored row with the hours to two decimals, then "Sum <person>" / "Total <person>" for
each person in order of first appearance and "Sum timer" / "Total hours" for the whole.
It is laid out with maroto's `AddRows`, which breaks to a new page wherever one ends, so a
timesheet of any length paginates, with the column header repeated at the top of every
one of its pages; the document's own pages are the same with the timesheet or without. It is part of the one PDF: the store-once key and the
stored hash cover it like every other word, the EHF attaches it with the rest, and the
draft's preview renders it as it stands ([The timesheet](#the-timesheet)).

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
| `cac:BillingReference/cac:InvoiceDocumentReference` (BG-3) | on a credit note, the original's `number` and `issue_date`; on a final settlement, one per invoice its deduction lines deduct, by number, each its `number` and `issue_date` ([A-konto and the final settlement](#a-konto-and-the-final-settlement)); none on any other invoice. A settlement's deductions are negative lines, never a `PrepaidAmount`, which lowers the amount due but not the VAT base — the a-kontos were VAT invoices |
| `cac:InvoicePeriod` (`cbc:StartDate`, `cbc:EndDate`) | `delivery_from`, `delivery_to` |
| `cac:Delivery/cbc:ActualDeliveryDate` | `delivery_date` |
| `cac:Delivery/cac:DeliveryLocation/cac:Address` | the place of delivery, **only when it has a country** (BR-57); one without a country is left out of the EHF, the PDF still prints it |
| `cac:AdditionalDocumentReference` | the stored PDF: `cbc:ID` the number, `cbc:DocumentDescription` "Faktura (PDF)" / "Invoice (PDF)" ("Kreditnota (PDF)" / "Credit note (PDF)" on a credit note), `cac:Attachment/cbc:EmbeddedDocumentBinaryObject` the bytes in Base64 with `mimeCode="application/pdf"` and `filename` the download's name |
| `cac:ProjectReference/cbc:ID` (BT-11, invoice only) | `project_reference`, when set ([The project](#the-project)); one per invoice (PEPPOL-EN16931-R080), after the `AdditionalDocumentReference` as the schema orders it |
| a second `cac:AdditionalDocumentReference` (credit note only) | `project_reference`, when set: a credit note's syntax has no `ProjectReference`, so `cbc:ID` the reference and `cbc:DocumentTypeCode` **50**, no description and no attachment (BIS §11.3.7), after the PDF's |
| `cac:AccountingSupplierParty/cac:Party` | `cbc:EndpointID@schemeID` the seller's Peppol id split at its colon; `cac:PartyName/cbc:Name` and `cac:PostalAddress` from the seller snapshot; `cac:PartyTaxScheme` with `cbc:CompanyID` `NO<organisation number>MVA` under `VAT` **only when VAT-registered** (NO-R-001), and `Foretaksregisteret` under `TAX` **only when registered there** (NO-R-002, a warning when absent); `cac:PartyLegalEntity` the legal name and the organisation number under `schemeID="0192"`; `cac:Contact/cbc:ElectronicMail` the seller's e-mail when set |
| `cac:AccountingCustomerParty/cac:Party` | `cbc:EndpointID@schemeID` from `buyer_peppol_id` (the scheme its prefix, the value the rest); `cac:PostalAddress` from the buyer snapshot, the region as `cbc:CountrySubentity`; `cac:PartyLegalEntity/cbc:RegistrationName` **always** (BR-07), with `cbc:CompanyID@schemeID="0192"` for a Norwegian business's organisation number, `cbc:CompanyID` without a scheme for a foreign id, and none for a person |
| `cac:PaymentMeans` (invoice only) | **one**, `cbc:PaymentMeansCode` **30** (credit transfer); `cac:PayeeFinancialAccount/cbc:ID` the domestic account, or for a buyer whose country is not NO the IBAN with `cac:FinancialInstitutionBranch/cbc:ID` the BIC when the seller has an IBAN; `cbc:PaymentID` the KID, and **no `PaymentID` at all without one** — Norwegian receivers read it as a KID |
| `cac:PaymentTerms/cbc:Note` | "Forfall 15.10.2026" / "Due 2026-10-15" on an invoice; "Kreditnota – beløpet godskrives" / "Credit note – the amount is credited" on a credit note, which has no due date (BR-CO-25) |
| `cac:TaxTotal` | `cbc:TaxAmount` the VAT total; one `cac:TaxSubtotal` per VAT summary row, in the document's order: `cbc:TaxableAmount`, `cbc:TaxAmount`, and `cac:TaxCategory` by the category rules below |
| `cac:LegalMonetaryTotal` | `cbc:LineExtensionAmount` and `cbc:TaxExclusiveAmount` the net total, `cbc:TaxInclusiveAmount` and `cbc:PayableAmount` the gross total; no rounding amount |
| `cac:InvoiceLine` / `cac:CreditNoteLine` | `cbc:ID` the position; `cbc:InvoicedQuantity` / `cbc:CreditedQuantity` with `unitCode` from the unit table — `-1.000` on a deduction line and on a credit note's copy of one; `cbc:LineExtensionAmount` the line net, negative on such a line, so the totals and the VAT rows sum it as they are (BR-CO-10, BR-CO-13); when the discount is not zero, `cac:AllowanceCharge` with `cbc:ChargeIndicator` false, reason code `95` and reason "Rabatt" / "Discount" (BR-42), `cbc:MultiplierFactorNumeric` the discount percent, `cbc:Amount` the line allowance and `cbc:BaseAmount` the line gross (PEPPOL-EN16931-R040–R042); `cac:Item/cbc:Name` the description; `cac:Item/cac:ClassifiedTaxCategory` the line's snapshot category, with `cbc:Percent` except for O; `cac:Price/cbc:PriceAmount` the unit price |

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
A third, `invoices-reminders`, sends the reminder letters and runs on every
installation ([The worker](#the-worker)).
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
`submitted`; `unconfirmed` and still probed, or without a provider reference and queued
less than fourteen days ago; or `queued` with the marker set — and then reads until the
queue is empty, at most 500 events a cycle; the next cycle reads on. An `unconfirmed` row
parked at `'infinity'` with a reference waits for a person and is not listened for; one
parked without a reference is, for fourteen days from its queueing, since an event
matched by its idempotency key is the machine's only way to resolve it. An event for any
row still in the queue is applied whenever a drain runs. Each event is applied **idempotently and without a row lease**: matched
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

## Bank files and the exception queue

Phase 4 reads the bank's own record of the money that arrived
([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-06-invoices-payments-reminders-design.md),
D3). A person with `invoices:payments` uploads a file of incoming payments; the import
checks it all or nothing, keeps the file, and stores each payment in it as a bank line.
Once it has committed, every line it stored that is not a duplicate is **matched on its
KID** ([Matching](#matching)): it becomes a payment against an invoice, or a case for a
person in [the exception queue](#the-exception-queue). A line is `pending` only until
matching reaches it.

**Two formats.** `POST /invoices/bank-files` takes one multipart part named `file`, at
most 10 MiB (the operation's own body limit, 10 MiB and 64 KiB for the framing). The
format is detected from the bytes, a UTF-8 byte-order mark dropped first: an **OCR
giro** file (Mastercard Payment Services' format, the OCR/KID agreement's) when the first
non-blank line, CR/LF stripped, is 80 characters beginning `NY000010`; a **camt.054**
notification when the document's root is `Document` in the namespace
`urn:iso:std:iso:20022:tech:xsd:camt.054.001.02` or `…001.08`. Anything else is 400 on
`file`, "Not an OCR giro or camt.054 file". A part missing, sent twice, empty or past the
limit, and a body past the operation's limit, are the same 400 on `file`.

**Checked all or nothing.** The parser (package
[`bankfile`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/invoices/bankfile),
a leaf that reads no database) refuses the whole file at the first rule it breaks, and
the 400 on `file` names where — `record 7: …` for an OCR record, the element's path such
as `Ntry[2]/NtryDtls/TxDtls[1]/Amt: …` for camt.054. Nothing is stored or written.

- **OCR giro**: every record exactly 80 characters, starting `NY`; the records in the
  grammar `10 (20 (30 31 [32])+ 88)+ 89`; an amount item's second and third records share
  its first's transaction number and type, the third present exactly for types 20 and 21;
  per assignment and per transmission, the transaction count, the record count (start and
  end records included), the **signed** sum (a line with the sign `-` subtracts; a type 18
  or 20 reversal adds, as the specification says) and the first and last settlement date
  equal the end records'; service code 09 only; dates DDMMYY in 2000–2099; a KID of digits
  with an optional trailing `-` (MOD11); amounts at most `numeric(14,2)`. The text of a
  type 20 or 21 is read as ISO-8859-1.
- **camt.054**: read with a strict XML decoder; a `DOCTYPE` or any other declaration
  refused, at most 64 levels of nesting and 10 000 elements plus 100 per transaction, the
  encodings UTF-8, ISO-8859-1 and US-ASCII only — a hostile file is refused, not parsed;
  both versions' paths; per entry, the transactions' amounts sum to the entry's and their
  count equals `Btch/NbOfTxs` when present, and `TxsSummry` agrees when present; **every
  booked amount is in NOK** — the entry's, and `.08`'s transaction amount and
  `AmtDtls/TxAmt` — a missing currency counting as not NOK, so a file in another currency
  is refused whole (research case m); an instructed, counter-value or remitted amount
  and a charge are not judged; an amount has at most two decimals. The ISO 20022 schemas
  are not validated at run time.
- **Both**: every booking day on or before today (Oslo, the request's one clock read)
  and not before 2000-01-01; at most 5 000 transactions, since each is later matched in a
  transaction of its own.

**What becomes a line.** OCR: every amount item of types 10–17 (the giro kinds), its
amount in øre; a line with the sign `-` is stored with `negative` set. Types 18–21 (card
information — Vantigo has no terminal agreement) are checked, summed and **ignored**,
counted `card_information`. camt.054: every transaction (`TxDtls`) of a booked (`BOOK`)
credit entry — an entry without transactions is one line of its own amount; a debit
entry only when it is a reversal (`RvslInd` true, or the bank code `PMNT/…/RRTN`), stored
with `direction` `debit`; any other debit entry is ignored `debit`, an entry not booked
`not_booked`. A transaction of 0.00 in either format is ignored `zero_amount`. Each line
keeps what the bank wrote: `line_ref` (OCR `<assignment>/<transaction>`, camt
`<notification>/<entry>/<transaction>`, 1-based), the receiving account, the booking day
(OCR's settlement date, camt's `BookgDt`), the value day, OCR's ordering day
(`ordered_on`, `Oppdragsdato`; camt has none), the amount, the KID as written (the first
`SCOR` reference in camt that fits a KID's 25 characters — the first too-long reference
before it does not hide it but leads the remittance text as `SCOR <ref>`; none is `NULL`), the remittance text (camt's `Ustrd` lines
joined by a space, else the entry's `AddtlNtryInf`, else OCR's text; at most 1 000
characters), the debtor's name and account, the archive reference (OCR's
`Arkivreferanse`, camt's `TxDtls/Refs/AcctSvcrRef`) and the bank's transaction code.

**The account** (research case m). Every account a file names — OCR's assignment
account, camt's `Ntfctn/Acct`, a Norwegian IBAN read as its 11-digit BBAN — must be the
seller's **Bank account** or one an issued invoice printed (its `seller_bank_account`
snapshot), so a payment to an account the seller had before is still read. Otherwise the
file is 409 **`bank_account_unknown`**, its detail naming the account's last four digits.

**The file twice.** A file's SHA-256 and its own identity — OCR's
`sender:transmission:recipient` from the start record (`Dataavsender`,
`Forsendelsesnummer`, `Datamottaker`), camt's `MsgId|CreDtTm` — are each unique
(`uq_bank_files_sha256`, `uq_bank_files_identity`, the latter per format). Either seen
before is 409 **`bank_file_duplicate`** with `bankFileId`, `uploadedAt` and
`uploadedBy` of the earlier import: read on the pool first, the hash before the identity,
before anything is stored; an import that commits the same file meanwhile is caught by
the unique index inside the transaction and answered the same way.

**Stored once.** Before the transaction, the bytes are stored under the module's scope
as `bank-files/<sha256>.ocr` or `bank-files/<sha256>.xml` — `Exists` first, `Put` only when
absent, the PDFs' shape — because the file is the documentation of the payments booked
from it (bokføringsloven § 10) and is kept like them: never deleted or overwritten
([object storage](/en/admin/object-storage/)). Without an object store, or when the store
fails, the import is 503 `storage_unavailable` and nothing is written. An object stored
by a request that then fails is harmless: the same bytes land on the same key, and the
next import of them finds it there.

**One transaction**, READ COMMITTED, in this order:

1. **The file's accounts, in account order** (`lockImportAccounts`): the accounts not
   seen before inserted with the file's format — an account's first import sets its
   format — then every one read `FOR SHARE`. An account whose format is not the file's is
   409 **`bank_import_format_mismatch`**, the detail naming the account and its format,
   and the whole import is rolled back (an account the refused file named for the first
   time gets no row). These are the transaction's first statements, so two first imports
   of overlapping files queue on the same rows in the same order.
2. **The file row**, `duplicates` still `NULL`.
3. **Every line in one `INSERT … ON CONFLICT DO NOTHING`, ordered by fingerprint**,
   against `ux_bank_transactions_fingerprint` — one live line per account and
   fingerprint. Two overlapping imports wait on that index in the same order, so they
   never deadlock: the second waits for the first, then skips what the first committed.
4. **The lines the conflict skipped**, inserted again as `duplicate` rows whose
   `duplicate_of_id` names the live line of the same account and fingerprint; the
   partial index leaves them out for good, so a skipped line is kept and visible, not
   only counted.
5. The file's `duplicates` set, once, from `NULL` (the only write `bank_files` takes).

No line event is written by the import: a line's first event is its matching.

**The fingerprint** is the hex SHA-256 over the receiving account, the booking day, the
amount in øre with its sign, the KID — or, without one, the remittance text trimmed,
case-folded and its whitespace collapsed — the debtor's account and the archive
reference when present, and an **ordinal**: the n-th line with all the rest identical in
the same file. The same file again under a new identity, or a file overlapping an earlier
one, reproduces the ordinals, so its lines become `duplicate` rows; two identical
payments in one file stay two. Two genuine identical payments of one day split across
two files without a reference collide, and the second is a `duplicate` row a person can
treat as distinct.

**The account's format and its cutover.** `GET /invoices/bank-accounts`
(`invoices:payments`) lists every account a file was imported for, in account order:
its format, the previous format and cutover a change kept, who set it and when, its
latest file and the latest booking day of its lines. `PUT
/invoices/bank-accounts/{account}/format` (`invoices:manage`) takes `{format}`, `ocr` or
`camt054` (else 400 on `format`), and locks the account's row `FOR UPDATE` — its only
lock. The same format answers the account unchanged; another keeps the old one as
`previous_format` with **`cutover_through` the latest booking day of the account's own
lines in the old format** — never a file's `last_booked_on`, which a file naming two
accounts can push past this one's — or `NULL` when it has none, and sets the new one. An
account never imported is 404. Matching then holds back a line of the new format booked
on or before the cutover as `possible_duplicate`, since the old format may already have
registered it. A change of bank
is a new account and gets its own row.

**The reads.** `GET /invoices/bank-files` (`invoices:payments`) pages the files newest
first, each with its lines counted by status — `pending`, `exceptions`, `matched` — and
`duplicates`, `ignored` and `ignoredKinds` (`debit`, `notBooked`, `cardInformation`,
`zeroAmount`). `GET /invoices/bank-files/{id}` answers `{file, transactions}`, every line
the file brought, duplicates included, in the order they were stored: its live lines
first, then its duplicates, each in fingerprint order.
The import's 201 answers the file, `transactions`, `matched` and `matchedAmount`,
`exceptions` and `exceptionsAmount`, `duplicates`, `ignored` by kind and `pending` — 0
unless matching stopped early. `GET /meta` answers `capabilities.canImportBankFiles`,
`invoices:payments`.

### Matching

Once the import's transaction has committed, every line it stored that is not a
duplicate is matched, one at a time in the order stored, the **uploader registering**
([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-06-invoices-payments-reminders-design.md),
D4). `POST /invoices/bank-files/{id}/match` (`invoices:payments`) does the same over a
file's `pending` lines, **its caller registering** — what finishes a file whose matching
stopped early. It is 404 for no such file, and otherwise 200 with the file as it now
stands, `matched` and `matchedAmount`, `exceptions` and `exceptionsAmount` of what that
request did, and the `pending` left; run again, it finds nothing to do.

**Classification**, read on the pool, in order; the first step that applies queues the
line — status `exception`, its reason, and a `queued` event by the registering user:

1. A reversal (`direction` `debit`) → `reversal`; a negative line → `negative_amount`.
2. **A possible duplicate**: the account has a previous format, the line is in its
   current one and is booked on or before `cutover_through`; or the **soft key** — a line
   of **another file** on the same account, booking day, amount and KID has a live
   payment or charge payment, or had one a reversal took back
   (`ix_bank_transactions_soft`) → `possible_duplicate`: reversed money is never
   registered again by itself.
3. No KID, and the text reads `Vippsnr` and a number → `vipps_payout`: a Vipps payout,
   never a customer's payment.
4. No KID → `no_kid`.
5. The KID is not 2–25 characters, digits but for a last digit or MOD11's `-`
   (`kid.Parse`), or its check character verifies under neither MOD10 nor MOD11 →
   `kid_invalid`.
6. Its digits exceed `int64` (another agreement's 25-digit KID), or no issued document
   has that number with **exactly** this KID, verified under the algorithm it was issued
   with → `kid_unknown`. The KID is found by its number (`ux_invoices_number`); there is no
   index on the KID, and a credit note has none.
7. The invoice printed another account (`seller_bank_account`) than the line's →
   `account_mismatch`.

Otherwise the line is a candidate for that invoice. A line queued after its KID named an
invoice keeps that invoice as its `suggested_invoice_id`. Queueing is only from
`pending`: a line another run of matching took meanwhile is left as it is.

**Under the lock.** A candidate is matched in **one READ COMMITTED transaction of its
own**: the line `FOR NO KEY UPDATE` — still `pending`, else there is nothing to do — then
the invoice `FOR UPDATE`, and nothing else (the line, then its invoice; never an invoice
and then a line). Every figure is read after both: **the soft key again first**, so the
other notification of the same payment, matched while this one waited, is seen and the
line queued `possible_duplicate`; then the open amount (gross − credited − live
payments) and the charges outstanding ([Charges](#charges)). The first row that holds
decides:

| Judged | Result |
| --- | --- |
| credited > 0 and credited ≥ gross | queued `invoice_credited` |
| booked before the invoice's issue day | queued `paid_before_issue` |
| open > 0 and amount ≤ open | a payment of the amount |
| open > 0 and amount ≤ open + charges | a payment of the open amount and a charge payment of the rest |
| open ≤ 0 and amount ≤ charges | a charge payment of the amount |
| open ≤ 0 and amount > charges | queued `invoice_settled` |
| amount > open + charges | queued `exceeds_open` |

**What a match registers.** The payment and the charge payment carry `source` the file's
format (`ocr`, `camt054`), `bank_transaction_id` the line, `paid_on` the **booking day**
— OCR's settlement date, camt.054's `BookgDt`, never the ordering day or the clock —
`reference` the KID, and the registering user at the request's one clock read. **The
principal is paid first, then the charges**, as two rows, so the allocation can be
explained (inkassoloven § 16). The line becomes `matched`, with a `matched` event. An
invoice on hold or handed off to collection is matched like any other: a payment is
always registered.

**The deadline-met waiver.** In the same transaction, after every match, the invoice's
facts are read again under its lock — the new payment among them — and every sent letter
whose **reminder fee** was claimed after an earlier letter's deadline that the payments
in fact met has that fee waived **`deadline_met`** by the registering user
(`ReliedOnMetDeadline`, [Waivers](#charges)). A payment meets a deadline by its ordering
day where the bank gives one (`ordered_on`, OCR's alone), else by its booking day — so a
camt.054 payment booked within an earlier letter's deadline, but imported after the next
fee letter went out, has that fee waived too. A payment ordered or booked after the
deadline waives nothing. The compensation is never waived so: it is due from the due
date, on no deadline.

**Possible duplicates.** Two genuine payments of one day, amount and KID in one file are
both registered — the soft key looks only at other files, and the fingerprint's ordinal
keeps them two lines. The same payment in another file — the intraday and the end-of-day
notification of it, or a genuine second payment with another reference — is held back as
`possible_duplicate`: kept, not registered, for a person to confirm or apply. An OCR file
and a camt.054 file of the same payment never both register it: the cutover holds back
the second on the pool, and two files of one format are caught by the soft key under the
lock.

**Stopping early.** Matching stops at the first error — the database's, or a line whose
KID names an invoice in another currency than NOK, which a NOK line cannot pay and no
reason of the queue names yet, so the match fails closed — or when the request ends: the line in hand rolls back and stays `pending` with the rest, the stop is
logged at warn with the file and the line, and the import's 201 (or `…/match`'s 200)
reports what is left in `pending`. The file's rows, committed before matching began,
stand. Matching reads no directory and calls nothing out of the module, so no call is
made under its locks.

### The exception queue

A line matching did not register is a case for a person
([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-06-invoices-payments-reminders-design.md),
D5): an `exception` with its reason, or a `duplicate` row. Every queue operation needs
`invoices:payments`, reads no directory and calls nothing out of the module.

| Reason | What it is | What a person usually does |
| --- | --- | --- |
| `kid_invalid` | the KID fails `kid.Parse` or both check digits | apply by hand, or dismiss |
| `kid_unknown` | no issued invoice carries exactly that KID — another system's, another agreement's | apply, or dismiss |
| `invoice_credited` | the KID's invoice is credited in full | dismiss with a note: a refund is owed and made outside Vantigo |
| `invoice_settled` | nothing is open on the KID's invoice, and the line is more than its charges | the same |
| `exceeds_open` | more than the open amount and the charges outstanding | apply part to the invoice and its charges; the rest stays unapplied |
| `no_kid` | no KID | apply to one or several invoices from the suggestions |
| `negative_amount` | an OCR line with a minus sign | dismiss with a note |
| `reversal` | a camt.054 reversal (`direction` `debit`) | handle it: remove the payment it reverses, or say why none |
| `vipps_payout` | a Vipps payout | dismiss — not a customer payment |
| `paid_before_issue` | booked before the KID's invoice was issued | apply after checking, or dismiss |
| `account_mismatch` | paid into another account than the invoice printed | apply after checking, or dismiss |
| `possible_duplicate` | the account's cutover, or the soft key — the same payment from another file | confirm it a duplicate, or apply it as a distinct payment |
| `payment_removed` | a matched line whose payments were all removed, reopened | apply again, or dismiss |
| (status `duplicate`) | the fingerprint's twin, kept as a row | confirm it, or treat it as distinct |

**The list.** `GET /invoices/bank-transactions` pages every line — `status`, `reason`,
`bankFileId`, the receiving `account` (its 11 digits), the `amount` (compared by value,
which is how the screen finds the payments a reversal may take back), the booking days
`from` and `to` (inclusive) and `unapplied` filter it; an unknown status or reason, `from`
after `to`, an account that is not 11 digits, an amount that is not above 0 with at most
two decimals, an `order` other than `queue` or `newest`, or paging out of range is 400.
By default (`order=queue`) the open lines — `pending`, `exception`, `duplicate` — come
first, each group oldest booking day first, then by id; `order=newest` lists every line
newest booking day first, then the highest id first — the screen reads a reversal's
candidates so, the latest 500 before it; the total counts the filtered lines. Each line answers what the bank wrote,
its file (`bankFile`: id, format, upload time), **`applied`** — every payment and charge
payment that refers to it, removed ones included, with the invoice and its number — and
**`unappliedAmount`**: its amount less its live payments and charge payments, and 0 for a
reversal, a negative line, a `duplicate` row, a line a reversal took a payment back from
(below), and a line resolved otherwise than `applied` — none of which is money waiting to
be applied — except a line **dismissed after it was queued `invoice_credited`,
`invoice_settled` or `exceeds_open`**: that money is owed back to the payer, refunded
outside Vantigo, so its whole amount stays unapplied. `unapplied=true` keeps the
`matched` and `resolved` lines with such a rest: a line applied in part, a line matched
and its payment since removed, and a line dismissed as money owed back. **What is not applied stays visible** there — phase 4 keeps no
customer credit balance and makes no refund. Each line also answers its `resolution`,
`resolvedBy`, `resolvedAt` and `resolutionNote`, its `suggestedInvoiceId` and its
**events**, the first first. `GET /invoices/bank-files/{id}` answers its lines with the
same fields, but no suggestions.

**Suggestions.** An exception queued `no_kid`, `kid_invalid`, `kid_unknown` or
`payment_removed` — or `possible_duplicate` without a suggested invoice — carries the
issued invoices it may pay, read when it is read and never registered by themselves (R4
§3.5 j): an invoice whose **number** is a whole word of the line's text
(`number_in_text`); one whose **open amount** equals the line's amount
(`amount_equals_open`); the open invoices of the customers whose earlier payments from a
bank line — one booked on or before this one, never the line itself — came from the
line's **debtor account** (`debtor_account`) — at most ten of
each, each invoice once under the first reason in that order, with its number, customer,
buyer and open amount. When a line is queued — by matching, by treat-as-distinct or by a
reopen — and has no invoice of its own, the one suggestion, if there is exactly one, is
kept as its `suggestedInvoiceId`; a line whose KID named an invoice keeps that one. A
`possible_duplicate` or `duplicate` line answers **`possibleDuplicateOf`**: the line it
was kept as a duplicate of, or else the earliest `matched` or `resolved` line of another
file with the same account, booking day, amount and KID, one with a live payment first —
with that line's payments and `reversed`, whether a reversal took a payment back from it,
which is why its payment is gone. The suggestions are computed per line as the page is read — a
few statements a line, on the queue's list and each action's answer only.

**The actions.** Each judges its body, then the line on the pool, then runs **one READ
COMMITTED transaction whose first lock is the line**, `FOR NO KEY UPDATE`, under which the
line is judged again — a line another person dealt with meanwhile is refused as the pool
would have refused it. The apply and the reversal then lock their invoices **`FOR
UPDATE` in descending id** — the module's invariant — together with every invoice the
line's payments and charge payments were ever registered against; a dismissal and a
duplicate's confirmation lock those too, when there are any; the treat-as-distinct and
the reopen lock the line alone. Never an invoice and then a line. A refusal rolls the
whole action back. Every action writes the line's event — what, the reason, the note, by
the caller at the request's one clock read — and answers the line as it now stands. **An
action that writes a note writes none when any of those invoices belongs to an
anonymised customer** ([Retention and personal data](#retention-and-personal-data)): the
note on the line and on its event is `""`, read after the invoices' locks.

- **Apply** — `POST /invoices/bank-transactions/{id}/apply` `{allocations: [{invoiceId,
  amount, chargesAmount?}], note?}`. In order: 400 on the fields — 1 to 20 allocations
  (`allocations`), each invoice once (`allocations[n].invoiceId`), `amount` and
  `chargesAmount` 0 or more with at most two decimals and **their sum above 0**, so a
  line can pay the charges alone of a settled invoice (`allocations[n].amount`,
  `…chargesAmount`), the note at most 500 characters; 404; 409
  **`bank_transaction_not_open`** unless the line is an `exception`; 409
  **`bank_transaction_not_applicable`** for a reversal or a negative line. Under the
  line's lock, 409 **`bank_transaction_reversed`** when the line was kept as a duplicate
  of a line a reversal took a payment back from — the same transaction. Under the
  invoices' locks, per invoice in descending id: 409 **`allocation_not_an_invoice`** unless it is
  an issued invoice; 409 `payment_exceeds_open` when `amount` is more than its open
  amount, with `invoiceId` and `openAmount`; 409 `charge_payment_exceeds_outstanding`
  when `chargesAmount` is more than its charges outstanding, with `chargesOutstanding`;
  409 **`paid_before_issue`** when the line was booked before the invoice's issue day;
  then 409 **`allocation_exceeds_transaction`** when the allocations add up to more than
  the line's amount less what its live payments and charge payments already apply. Each
  allocation registers what a match does ([Matching](#matching)): a payment of `amount`
  and a charge payment of `chargesAmount`, each when above 0, `source` the file's
  format, the line, paid on its booking day, the KID as reference, by the caller — and
  the deadline-met waiver of that invoice. The line becomes `resolved`, `applied`, its
  reason kept, the note on it, with an `applied` event.
- **Dismiss** — `POST …/{id}/dismiss` `{note}`, 1 to 500 characters (400 on `note`);
  404; 409 `bank_transaction_not_open` unless the line is an `exception`; 409
  `bank_transaction_not_applicable` for a reversal, which is handled instead. The line
  becomes `resolved`, `not_customer_payment`, with a `dismissed` event.
- **Handle a reversal** — `POST …/{id}/handle-reversal` `{note?, removePayments:
  [{invoiceId, paymentId}], noPayment?}`. 400 on the fields — at most 20 payments, each
  once, not both payments and `noPayment`, the note at most 500 characters; 404; 409
  `bank_transaction_not_open` unless an `exception`; 409 `bank_transaction_not_applicable`
  unless it is queued `reversal`; 409 **`reversal_payment_required`** when no payment is
  named and `noPayment` with a note is not given. Under the line's lock the named
  payments' invoices are locked in descending id, and each payment is removed by the
  removal's own rules — 404 a payment that is not its invoice's, 409 `payment_removed`
  one already removed — with the reason **"Reversed by the bank: line {lineRef}"**, by
  the caller, and **the line each removed payment came from gets a `reversed` event**
  with that reason as its note — its money went back, so it is **never applied again**:
  no unapplied rest, out of `unapplied=true`, and never reopened. (The event's foreign key
  takes `FOR KEY SHARE` on that line, which its `FOR NO KEY UPDATE` lets through: no wait
  and no new lock order.) The reversal line becomes `resolved`, `reversal_handled`, with a
  `reversal_handled` event. Nothing links a reversal to a payment by itself: the person
  names it. A reversal that named the wrong payment cannot be undone — a removal is never
  undone, and the reversal line is resolved — so register that payment again by hand.
  The reversed money stays out of reach elsewhere too: another notification of the same
  payment is held back by the soft key, and a `duplicate` row of the reversed line is
  refused treat-as-distinct and apply (below).
- **Confirm a duplicate** — `POST …/{id}/confirm-duplicate` `{note?}` (400 past 500
  characters): a `duplicate` row, or an `exception` queued `possible_duplicate`, becomes
  `resolved`, `duplicate_confirmed`, **its reason set to `possible_duplicate`** — so a
  reopen lands on a reason — with a `duplicate_confirmed` event. 404; 409
  `bank_transaction_not_open` for any other status; 409 `bank_transaction_not_applicable`
  for an exception of another reason.
- **Treat as distinct** — `POST …/{id}/treat-as-distinct`: a `duplicate` row becomes an
  `exception` queued `possible_duplicate`, so it can be applied, with a
  `treated_as_distinct` event. Its `duplicate_of_id` stays, and it stays out of
  `ux_bank_transactions_fingerprint`: the same payment imported again is a duplicate of
  the original live line. 404; 409 `bank_transaction_not_applicable` for an exception;
  409 `bank_transaction_not_open` for any other status; under the lock 409
  **`bank_transaction_reversed`** when the line it duplicates had a payment taken back by
  a reversal — an identical fingerprint is the same transaction, whose money went back;
  confirm it a duplicate instead. A soft-key `possible_duplicate` is not refused: a
  genuine second payment is possible, and its twin shows `reversed`.
- **Reopen** — `POST …/{id}/reopen`: a `resolved` line back to an `exception` with its
  reason; a `matched` line whose payments and charge payments were all removed back to an
  `exception` queued **`payment_removed`**. Its resolution, who, when and the note are
  cleared on the line and kept in its events, with a `reopened` event. 404; 409
  `bank_transaction_not_applicable` for a line that is `pending`, an `exception` or a
  `duplicate`; 409 **`bank_transaction_applied`** while any live payment or charge
  payment refers to it — remove them first; 409 **`bank_transaction_reversed`** when a
  reversal took back one of its payments. A payment removed by its own removal — a refund
  recorded by hand — does not block the reopen; only a reversal does. A reversal reopened finds its removed
  payments still removed: a removal is never undone.

A payment's removal never writes its line ([Payments](#payments-and-the-state-of-an-invoice)):
a line matched or applied stays as it is until a person reopens it.

## Reminders

Phase 4 ([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-06-invoices-payments-reminders-design.md))
chases what is owed: reminders (purringer) and the creditor's collection notice
(inkassovarsel), with the reminder fee, the business compensation and late interest
they may claim. This section grows with the phase. It begins with what every letter is
judged against: the statutory rates as dated rows, the two collection-law regimes and
their review, the reminder settings, and a customer's reminder policy; then the engine
that decides the next letter ([The rules](#the-rules)), the overdue list it judges
([The overdue list](#the-overdue-list)) and the runs that make the letters
([Runs](#runs)). Sending and printing the letters come later in the phase.

### Collection rates

`invoices.collection_rates` holds the three statutory figures a letter may need, each as
dated rows: `late_interest_percent` — the forsinkelsesrente, percent a year, set per
half-year (forsinkelsesrenteloven § 3); `b2b_compensation_nok` — the compensation a
business debtor owes for the creditor's costs (§ 3a), set per half-year; and
`inkassosats` — the collection rate the reminder fee is a twentieth of. **A row is in force from its `valid_from` until the next row of its kind.** The
two half-yearly kinds start on 1 January or 1 July only (`ck_collection_rates_half_year`).

The release seeds them, every value read on Lovdata:

| `valid_from` | Late interest | Compensation | Regulation |
| --- | --- | --- | --- |
| 2024-01-01 | 12.50 | 470 | FOR-2023-12-14-2043 |
| 2024-07-01 | 12.50 | 460 | FOR-2024-06-26-1320 |
| 2025-01-01 | 12.50 | 470 | FOR-2024-12-19-3279 |
| 2025-07-01 | 12.25 | 460 | FOR-2025-06-23-1321 |
| 2026-01-01 | 12.00 | 460 | FOR-2025-12-18-2658 |
| 2026-07-01 | 12.25 | 430 | FOR-2026-06-25-1372 |

and the inkassosats 700 from 2019-01-01 (FOR-2018-12-20-2050) and 750 from 2026-01-01
(FOR-2025-12-19-2709). A seeded row has no `created_by_user_id`.

**Append-only.** A trigger refuses every UPDATE but one — `release_value` and
`release_source_ref` set once from NULL, nothing else changed — and the DELETE of a
seeded row. **A later release seeds through `invoices.seed_collection_rate(kind,
valid_from, value, source_ref)`**, the function `00041` itself calls: a new day is
inserted as a seeded row; a day a user already added keeps the user's value and gains
the release's beside it, in `release_value` and `release_source_ref`, once, whether or
not the two are equal; a seeded row is left as it is. So a release never fails on a row
a user added first. While a row's `release_value` differs from its `value` (compared as
numbers) the list warns **`collection_rate_differs_from_release`**: somebody typed a
rate the regulation did not set, and should check it.

**Reading the rates.** `GET /invoices/collection-rates` (`invoices:access`) answers every
row by kind and date, with `seeded`, `createdBy`, `releaseValue` and `releaseSourceRef`,
`inForce` — the row in force today, Oslo's day from the server's clock, which moves at
Oslo midnight — and `usable`, false once a printed or sent letter dated on or after the
row's `valid_from` and before the next row of its kind has relied on it, and the list's
`warnings`. A paper letter is printed for a posting date up to seven days ahead, so a
row not yet in force can already be used.

**Adding a rate.** `POST /invoices/collection-rates` (`invoices:manage`) takes `kind`,
`validFrom`, `value` and `sourceRef`, the regulation. A rate is added ahead of a release,
so `validFrom` is after today (Oslo), and after the date of the latest printed or sent
letter — a paper letter is printed for a posting date ahead of today, and a new row must
never contradict a letter already printed or posted; a half-yearly kind starts on 1 January or 1 July;
`value` has at most two decimals and is within its kind's bounds — late interest
0.01–30, compensation 100–2 000, the inkassosats 100–5 000; `sourceRef` is 1–100
characters. Each is a 400 on its field, all of them together, before the one 409:
**`collection_rate_exists`**, a row of the kind on that day already (the unique key, so
two adds racing each other cannot both land). 201 with the row.

**Deleting a rate.** `DELETE /invoices/collection-rates/{id}` (`invoices:manage`)
deletes a mistaken row only while nothing can have relied on it. It locks the row `FOR
UPDATE` first and judges after the lock, so a print batch that holds the rates its
letters rely on until it commits is waited for, and its letters seen. A seeded row, a row in
force or past (`valid_from` on or before today), and a row a printed or sent letter used
are each **409 `collection_rate_in_force`**, the detail saying which. An unknown id is a
404; a deletion a 204. **A deleted row a release had seeded over is replaced in the same
transaction by a seeded row of the release's value and regulation**, so the half-year
never goes empty and refuses every letter that needs it.

**Outdated rates.** A letter needs the rate of every half-year it charges interest
across, and the compensation of its own half-year — a row **starting on that
half-year's first day**. A half-year with none — after the last row, between two rows,
or before the first — is outdated: an older rate is never carried across it, and the
letters that need it wait until a manager adds the row or a release seeds it
(`collection_rates_outdated`, naming the kind and the half-year; the reminder run, the
dispatch and the overdue list come later in the phase). The inkassosats is not
half-yearly and needs only a row in force on the letter's date. A half-year before
2024-H1 can be filled only by a release's seed.

### The two regimes and the review

The inkasso law of 2026 (LOV-2026-05-22-19) replaces the one of 1988 on a day the King
has not yet set; it is signalled for 2027-01-01. `inkassolov_2026_from`, in the reminder
settings, is that day once known. **A letter is judged under the regime of its own
date**, and records it: before `inkassolov_2026_from`, or while it is NULL, the **1988
regime** (inkassoloven 1988 and inkassoforskriften) — every rule of reminders, fees and
the creditor's collection notice; on or after it, the **2026 regime** — no reminder fee on
any letter (the creditor's own fee-bearing letters wait for the regulation under the new
§ 19), no creditor's collection notice (under the new § 20 it is the collection agency's),
and the last letter before a hand-off announces the agency; late interest and the
compensation continue.

**The review.** `regime_reviewed_through`, seeded 2026-12-31 — the last day before the
signalled date — is the last day a letter carrying a fee, or a creditor's collection
notice, may be made under the 1988 regime without anybody having looked again. A letter
dated after it that would carry either waits (`collection_regime_unreviewed`); fee-free
reminders continue. A manager clears it in the reminder settings by setting
`inkassolov2026From` or by moving `regimeReviewedThrough` forward — at most a year after
today — and the settings record who did it and when (`regimeReviewedBy`,
`regimeReviewedAt`); a release may do either once the day is announced. Under a law whose
day is unknown, the one failure this cannot allow is the creditor's own fee-bearing
notice sent after the signalled day without anyone having looked.

### The reminder settings

`invoices.reminder_settings` is one row (`id = 1`), off the settings row every issue
shares. `GET /invoices/settings/reminders` (`invoices:access`) reads it;
`PUT /invoices/settings/reminders` (`invoices:manage`) replaces it:

| Field | Rule | Default |
| --- | --- | --- |
| `enabled` | reminders offered at all | false |
| `firstReminderDays` | the first letter's earliest day after the effective due date, 1–60 | 14 |
| `deadlineDays` | every letter's deadline after it is sent, 14–60 (at least 14: inkassoloven § 9, inkassoforskriften § 1-3), moved to the next business day | 14 |
| `graceDays` | days after a deadline before the next letter, 1–10 — a payment ordered on the deadline is on time and booked later (inkassoforskriften § 1-2) | 3 |
| `remindersBeforeNotice` | reminders before the collection notice, 0–2 (a reminder is not required before a notice, FinKN 2023-845) | 1 |
| `collectionNotice` | the creditor's collection notice offered (1988 regime only) | true |
| `personCharge` | `fee` or `none` — a consumer never owes the compensation (forsinkelsesrenteloven § 4 d) | `fee` |
| `businessCharge` | `fee`, `compensation` or `none` — never both: they offset each other (inkassoforskriften §§ 1-5, 2-6) | `fee` |
| `lateInterest` | late interest claimed on letters | false |
| `staleImportDays` | how old the last imported booking may be before a charging run needs confirmation, 1–30 | 3 |
| `inkassolov2026From` | the 2026 regime's day, or null while unknown | null |
| `regimeReviewedThrough` | the review, at most a year after today | 2026-12-31 |

**Every field is required**, and null is a value only for `inkassolov2026From`: a body
without a field, or with null for any other, is a 400 on it, so a client that predates a
field cannot reset it by leaving it out. Each field out of its bounds is a 400 on it too,
all of them in one answer — `revision` included: a body without it is a 400 on
`revision`. The answer also carries `regimeReviewedBy` and
`regimeReviewedAt` — who last moved the review or set the 2026 regime's day, and when;
absent and the migration's time until somebody does — and `revision`, `updatedAt` and
`updatedBy`. One statement replaces the row when the body's `revision` is the stored one;
a stale revision is a 409 without a code, naming both. **Changing the settings changes
only letters made afterwards**: a letter records its facts when it is sent.

### A customer's reminder policy

`invoices.customer_reminder_policies` holds, per customer, whether they are reminded and
charged: `normal` — letters as the settings make them; `no_charges` — letters without fee,
compensation or interest; `none` — no letter at all (the invoice is still overdue, and
says why). **No row is `normal`.** A row has its `note` (at most 500 characters) and who
set it when.

`GET /invoices/customers/{customerId}/reminder-policy` (`invoices:access`) answers the
policy, `normal` with an empty note and neither `updatedAt` nor `updatedBy` when there is
no row. `PUT` (`invoices:payments`) takes `mode` and `note` (trimmed): an unknown mode or
a note over 500 characters is a 400 on its field. Then, in one transaction, the
customer's documents are read **`FOR SHARE`, newest first** — the order of the merge's
own lock and of every path that locks invoices alone — and under that lock the customer
must have a document here, an issued one or a draft, and **must not be anonymised**;
otherwise 404, so an anonymised person gets no fresh note. Only then is the policy row
locked and written; **`normal` with an empty note deletes the row**. A PUT racing a merge
of the customer either lands first, and the merge — waiting on the documents — moves its
row, or waits on the merge's documents and, once the merge has committed, finds the
customer has none left: 404. Without the share lock, a PUT that saw the documents before
the merge committed would insert a row for the absorbed customer after the merge had
looked: an orphan nobody could reach.

**Why an invoices table**, not the customers module's billing profile: the profile
carries where to send a reminder (`reminderEmail`, `reminderDelivery`) — contact data the
customers module owns under its own permissions. Whether to remind and charge a debtor
is a credit-control decision, under the sensitive `invoices:payments`, and no customers
permission should exempt anyone from reminders. A policy for a customer group is not
offered.

**Merging and erasing.** A merge re-points the absorbed customer's row to the survivor;
when both have one, **the stricter mode wins** — `none` over `no_charges` over `normal`
— the notes are joined, the survivor's first, ` / ` between them and an empty one left
out, cut to 500 characters, and the absorbed row is deleted; the merged row is recorded
as the stricter row's author's, at the merge's time. A person's export carries the
policy, and their anonymisation deletes it ([Retention and personal
data](#retention-and-personal-data)).

### The delivery fact

An invoice that was not validly delivered does not fall due, and a reminder fee on it is
invalid (Finansklagenemnda, FinKN 2017-492). **A charge — a fee, the compensation or
interest — needs a recorded delivery on or before the due date**, and a delivery after
the due date counts as none. Three kinds count, each by its day in Oslo:

- an **e-mail** that handed the invoice over (`invoices.deliveries`), by the Oslo day of
  its `sent_at` — 23:30 in Oslo on the due date counts, 00:30 the day after does not;
- an **EHF transmission** `delivered`, by the Oslo day of its `delivered_at`; a queued,
  failed, unconfirmed or cancelled one is not a delivery;
- a **manual delivery** not removed, by its `delivered_on`.

Without one, the invoice is not due: the reminder engine offers only fee-free reminders,
at most `max(reminders_before_notice, 1)` of them, and blocks the collection notice and
the hand-off `not_delivered` until a delivery is recorded.

**A manual delivery** records an invoice handed over or posted, for an invoice that went
on paper or by hand. `POST /invoices/{id}/manual-deliveries` (`invoices:issue`) takes
`{kind, deliveredOn, note?}`: 404; 409 `credit_note_no_reminders` for a credit note,
draft or issued, and `invoice_draft` for an invoice draft; 400 on `kind` (`handed_over`
or `posted`), `deliveredOn` (from the issue date to today, Oslo) and `note` (at most 500
characters, trimmed). One transaction locks the invoice and inserts the record with who
and when. An issued invoice answers `manualDeliveries`, every one, removed ones included
with their removal, the earliest first.

**Its removal.** `POST /invoices/{id}/manual-deliveries/{deliveryId}/remove`
(`invoices:issue`) takes a `reason` (1 to 200 characters, judged first), locks the
invoice and reads the record after the lock: another document's is a 404, one removed
already 409 **`delivery_removed`**. While a letter of the invoice that carries its facts
and is not withdrawn — sent, printed for the post, or being sent by e-mail — carries a
fee or the compensation not waived, or interest beyond what was waived, and no other
delivery on or before the due date would remain, the record is relied on: 409
**`delivery_relied_on`**. A printed or in-flight letter counts as a sent one does, since
its charges are on paper or on their way. A mistaken record is corrected by waiving those
charges `claimed_in_error` once the letter is sent, and removing it after; an e-mail or
another record on or before the due date lets it go at once, and a record dated after
the due date is never relied on.
The row is kept and counts for nothing. `tr_manual_deliveries_immutable` refuses a DELETE
and every UPDATE but the removal, once, and the erase's blanking of the note;
`tr_manual_deliveries_parent` reads the invoice `FOR SHARE`, refuses a row under anything
but an issued invoice, and blanks the note of an anonymised customer's.

### The rules

**One engine.** What to do next with an issued invoice is one pure function,
`reminderrules.Next` (the package `internal/invoices/reminderrules`, which reads no store
and no clock — a test holds it to that). The overdue list, the run's preview, each item of
a run and, later in the phase, the letter's dispatch and the posting of paper all call it,
so they never disagree. Its input is read by the rule-input loader alone — on the pool in
a handful of statements for a whole list, whatever its length, or after the invoice's
lock for one — and is:

- the invoice's own snapshot: the buyer's type, organisation number and foreign id, the
  issue date, the due date and the gross — never a fresh read of the customer;
- the principal's history: each issued credit note's day and gross, each live payment's
  `paid_on`, amount and — when an OCR giro line brought it — the day the payer ordered it
  (`ordered_on`; a camt.054 line carries none), and the live reservations (none until
  the payments port);
- the deliveries ([The delivery fact](#the-delivery-fact));
- every letter: the sent ones with the facts written at their sending, and those in
  flight — `queued`, `awaiting_print`, `printed` or `failed` — without; the waivers and the
  live charge payments ([Charges](#charges));
- the reminder settings, the customer's policy, a live hold, whether a lifted hold barred
  charges, a live hand-off, every collection rate;
- the day `L` it is asked about: today in Oslo, one clock read per request.

**Days.** Months are added clamped to the month's end — 31 August plus six months is
28 February, or the 29th in a leap year, where a naive addition would give 3 March.
**`E`, the effective due date,** is the due date moved to the next business day when it
falls on a Saturday, a Sunday or a Norwegian public holiday (New Year's Day, Maundy
Thursday, Good Friday, Easter Sunday and Monday, 1 May, 17 May, Ascension Day, Whit Sunday
and Monday, Christmas Day, Boxing Day). **A letter's deadline** is
`max(deadline_days, 14)` days after it is sent, moved off such a day the same way. Both
only ever delay a charge: a payment order reaches no bank on a holiday.

**The next action**, the first that applies:

1. nothing of the principal is open → `none` (charges may still be outstanding);
2. a live hand-off → `none`, `handed_off`;
3. a live hold → `blocked`, `on_hold`;
4. the customer's policy `none` → `blocked`, `policy_none`; reminders off → `blocked`,
   `reminders_disabled`;
5. a letter in flight → `blocked`, `letter_pending` (the dispatch, judging the letter it
   sends, leaves that one out);
6. no letter sent → a `reminder`, from `E + first_reminder_days` — or, when
   `reminders_before_notice` is 0, the `collection_notice` (under the 1988 regime, when it
   is offered) or a reminder announcing the hand-off (under the 2026 regime);
7. the last letter's deadline plus `grace_days` not yet passed → `waiting`, until the day
   after;
8. fewer letters sent than `reminders_before_notice` → a `reminder`;
9. under the 1988 regime, the `collection_notice` when it is offered and none was sent;
   under the 2026 regime, one more reminder that announces the hand-off
   (`announcesCollection`) when none did;
10. otherwise → `hand_off`: suggested, never automatic.

**Without a delivery** on or before the due date only fee-free reminders go, at most
`max(reminders_before_notice, 1)` of them, and whatever would follow is `blocked`,
`not_delivered`, from the day it would have come. Under the 2026 regime a letter that
would be the collection notice is a reminder announcing the hand-off.

**A deadline met.** A letter's deadline counts as met when the live payments ordered on or
before it — `ordered_on` where the bank line has one, else `paid_on`, which is never
earlier — cover what was open of the principal when it was sent; a credit note issued by
the deadline lowers what had to be paid. A reminder fee claimed on a letter sent after an
earlier letter's deadline that the payments in fact met — a second fee, or a first one
after a fee-free letter — is waived `deadline_met` when the proof arrives (the bank match).
A camt.054 line carries no order day, so a payment it brings is judged by its booking
day: the default `grace_days` of 3 covers a payment ordered on the deadline and booked
after an ordinary long weekend; Easter can take longer, and the stale-import confirmation
of a run ([Runs](#runs)) is the guard then.

**The reminder fee** (`fee_kind = reminder_fee`) is claimed only when every one of these
holds:

- the 1988 regime on `L`, and the review not lapsed ([The two regimes and the
  review](#the-two-regimes-and-the-review)) — the 2026 regime takes no fee (R20);
- a delivery on or before the due date;
- `L` at least `E + 14` days (R7, inkassoforskriften § 1-2) — a letter before that goes
  without a fee, `fee_before_14_days`;
- the charge setting for the buyer — `personCharge` or `businessCharge` — is `fee`, the
  policy is `normal`, and no lifted hold barred charges (R16), else `charges_barred`;
- **the two-fee cap with its six-month reset** (R9, R11, inkassoforskriften § 1-3): from the
  latest sent fee letter — a waived fee still counts, it was claimed — count back through
  the earlier fee letters, stopping at the first gap of more than six months between two of
  them; when `L` is more than six months after the latest the count is 0. The six months end
  on the anniversary, so the fee is allowed from the day after it. A fee is allowed while the
  count is below 2, else `fee_cap_reached`;
- a second fee only when the previous fee letter's deadline was at least 14 days after its
  sending, has passed by `L` and was not met (R10), else `fee_deadline_not_missed`;
- under `compensation` for a business, never (R6).

The amount is the inkassosats in force on `L`, ÷ 20, rounded to the krone with .50 up
(R8): 750 → 38, 725 → 36, 770 → 39. The reset, pinned:

| Fee letters sent | `L` | Count | Fee |
| --- | --- | --- | --- |
| 1 Jan, 1 Feb | 2 Jul | 2 (a one-month gap) | refused |
| 1 Jan, 1 Feb | 1 Aug | 2 (the anniversary is still inside) | refused |
| 1 Jan, 1 Feb | 2 Aug | 0 (`L` is past 1 Feb + 6 months) | allowed |
| 31 Aug | 28 Feb (not a leap year) | 1 | allowed |
| 15 Aug, 31 Aug | 1 Mar (not a leap year) | 0 (31 Aug + 6 months is 28 Feb) | allowed |
| 31 Aug, 15 Sep | 15 Mar | 2 | refused |
| 31 Aug, 15 Sep | 16 Mar | 0 | allowed |
| 1 Jan, 2 Jul, 15 Jul | 1 Aug | 2 (2 Jul to 1 Jan is more than six months: the chain stops) | refused |
| 1 Jan, 2 Jul | 3 Jul | 1 (the chain stops at the gap) | allowed |
| 1 Jan, 1 Jul | 2 Jul | 2 (exactly six months does not break the chain) | refused |

**The compensation** (`fee_kind = compensation`; forsinkelsesrenteloven § 3a, R5) is
claimed for a business with an organisation number — `buyerType` `business` and an
organisation number or foreign id on the snapshot; any other buyer is treated as a person
and never owes it (§ 4 d, R17) — when `businessCharge` is `compensation`, the policy is
`normal`, the invoice was delivered on or before its due date and no lifted hold barred
charges: once per invoice, on its first letter, the NOK figure in force on `L`. Under
`compensation` no reminder fee is ever claimed on that invoice (R6). It is claimed under
either regime.

**Late interest** (forsinkelsesrenteloven §§ 2–3, R1–R3), when `lateInterest` is on, the
policy is `normal` and the invoice was delivered on or before its due date: **simple**
interest on the principal, always cumulative from the day after `E` to `L` — a waiver of
interest is an amount beside it, never a new start. Each day `d` bears
`open(d) × rate(d) / 100 / 365`, where `open(d)` is the gross less the credit notes issued
on or before `d` and the live payments paid before `d` — a credit note lowers the
principal from its own day, a payment from the day after — and `rate(d)` is the row in
force on `d`. The segments split at every rate change, credit note and payment; the sum
is rounded to øre once, the half away from zero. Interest is never charged on fees or the
compensation. Pinned: 10 000 due Monday 15 June 2026, asked on 15 July, with 4 000
credited on 5 July → 84.89; with 4 000 paid on 5 July instead → 86.23.

**A missing rate refuses.** A letter needing a half-yearly rate — the late interest for
each half-year of its interest period, the compensation for its own — for a half-year
without a row starting on its first day (a gap, or after the last row, or before the
first) is `blocked`, `collection_rates_outdated`, naming the kind and the half-year; the
inkassosats only needs a row in force on `L`. An old rate is never carried across a
missing half-year. A letter that would carry a fee or be the collection notice past the
review under the 1988 regime is `blocked`, `collection_regime_unreviewed`.

**The outcome** is the action, its earliest day, the reasons it is blocked or waits
(`on_hold`, `handed_off`, `policy_none`, `reminders_disabled`, `letter_pending`, `waiting`,
`not_delivered`, `collection_rates_outdated` with the missing rate, and
`collection_regime_unreviewed`), and for a letter due on `L` its facts: the level,
whether it announces the hand-off, the regime, the charge it claims, the amounts apart —
the principal open, `chargesEarlier` (the earlier letters' fees and compensation not
waived or paid; below zero when charge payments beyond them pay this letter's own
charge), this letter's fee or compensation, the cumulative interest with its from-day and
segments, what of it is waived and paid — the total, and the deadline; and its **charge
notes**, why it claims less than it might:

| Note | Why |
| --- | --- |
| `not_delivered` | no delivery on or before the due date: nothing is due yet |
| `charges_barred` | a lifted hold barred fees and the compensation for good |
| `fee_cap_reached` | two fees within six months already |
| `fee_before_14_days` | the letter comes before `E + 14` |
| `fee_deadline_not_missed` | the previous fee letter's deadline was met, too short, or not yet passed |

The rules and their sources (the research, §2.11): R1–R3 forsinkelsesrenteloven §§ 2–3
(interest from the due date, the half-yearly rate on each day, simple, apart from the
principal); R5–R6 § 3a and inkassoforskriften §§ 1-5, 2-6 (the compensation, never beside
a fee); R7–R11 inkassoforskriften §§ 1-2, 1-3 (14 days, 1/20 of the inkassosats, two fees,
a missed deadline, the six-month reset); R12 inkassoloven § 9 (the notice's deadline of at
least 14 days); R16 inkassoloven § 17 (no costs on a claim with a reasonable objection);
R17 § 4 d (a consumer never owes the compensation); R20 the 2026 inkassoloven §§ 2, 19, 20.
The creditor's own betalingsoppfordring (3/20) is not offered.

### The overdue list

`GET /invoices/overdue` (`invoices:access`) lists the issued invoices whose state is
`overdue` today — and, with `charges=outstanding`, also the `paid` ones whose sent
letters claimed charges still outstanding — the oldest due date first. `customerId` and
`dueBefore` (a due date before that day) narrow the set; past **5 000** invoices it is
409 **`too_many_overdue`**, asking for one of them. **The whole set is judged before it is
paged** (M7): read on the pool through the rule-input loader in a handful of statements,
no lock, the engine run on each invoice, then the `action` filter (`reminder`,
`collection_notice`, `hand_off`, `blocked`, `waiting`) and then the page (`page`,
`pageSize`, 25 by default and at most 100) — so a page of blocked invoices is the blocked
ones', and `total` counts the set after the filter.

Each item: the invoice (`invoiceId`, `number`, `customerId`, `buyerName`, `buyerType`,
`issueDate`, `dueDate`); `daysOverdue`, counted from `E` and never below zero;
`principalOpen`; `charges`, the block of [Charges](#charges) with `interestToday`, and
`interestToday` beside it; `delivered` — on or before the due date; `lastLetter`, the
latest letter not withdrawn, with its status and, once printed or sent, its day and
deadline; `nextAction` — the action, `earliestOn`, `reasons`, `chargeNotes`, `outdated`
naming a missing rate, and `letter`, the letter as it would go today, when it is due; a
live `hold` and `handoff`; and `policyMode`, the customer's policy.

The answer carries the bank data's **`freshness`** — `lastBookedOn`, the latest booking day
of any imported file, absent when none was ever imported; `stale` when that is more than
`staleImportDays` before today, and always without a file; `ocrAccounts`, the accounts
imported as OCR giro — and the **warnings** that apply: `collection_rates_outdated`
(reminders on and a rate the settings use — the late interest, the compensation, the
inkassosats — has no row for today's half-year, or a listed letter is blocked by a missing
one), `collection_regime_unreviewed` (reminders on, today under the 1988 regime past the
review with a fee or the notice in use, or a listed letter blocked by it),
`collection_rate_differs_from_release` (a release seeded another value over a rate a
manager added), `bank_data_stale` and `ocr_without_kid_payments` — a payment without a KID
never reaches an OCR giro file, so it must be registered by hand before a run. One clock
read per request.

### Runs

`POST /invoices/reminder-runs` (`invoices:access` and `invoices:payments`) previews a run
or makes one.

**The preview** (`dryRun: true`) judges the overdue set as the list does — the same
function on the same figures, so the two agree — and answers every invoice whose next
action is a `reminder` or a `collection_notice` due today or earlier, each with its letter
as it would go today, its **channel and recipient**, their warnings and its charge notes;
the invoices `blocked` or `waiting`, with their next action and reasons; the bank data's
freshness and the list's warnings. It takes the list's `customerId` and `dueBefore`
(read by the preview only), so more than 5 000 overdue invoices — past which it is 409
`too_many_overdue` — can be previewed in parts. It writes nothing and takes no lock. **The channel** comes from the
customer's billing profile, read through the directory once per customer: `paper` when
`reminderDelivery` says paper; otherwise e-mail to `reminderEmail` — paper with
**`reminder_email_missing`** when there is no address, paper with **`mail_unavailable`**
when this installation cannot send e-mail (`MAIL_DRIVER` is not `smtp`).

**The run** (`dryRun: false`) takes `items` — 1 to 500, each an invoice once with the
`action` its preview showed — and `acknowledgeStaleImport`. Refused in order:

1. 400 on `items`: none, more than 500, an invoice twice, an action other than `reminder`
   or `collection_notice`, an id that is not an issued invoice;
2. 409 **`reminders_disabled`** — reminders are off in the settings;
3. judged on the pool before anything is written, every item with the engine:
   409 **`collection_rates_outdated`** when an item's letter needs a rate a half-year
   lacks, the problem naming the `kind` and the `halfYear`; then 409
   **`collection_regime_unreviewed`** when an item would carry a fee or be the collection
   notice past the review under the 1988 regime; then 409 **`bank_import_stale`** when the
   bank data is stale, an item would claim a fee, the compensation or interest, and
   `acknowledgeStaleImport` is not `true` — with `lastBookedOn` once a file was ever
   imported. Letters without charges are never held back by stale bank data: what the bank
   has not yet told Vantigo can make a fee wrong, not a reminder.

A refusal writes nothing. Then the items' billing profiles are read — once per customer,
before any lock — and the `invoices.reminder_runs` row is written: the day, the caller,
the bank data's `last_booked_on` and `stale_import_acknowledged`. **Each item is one
transaction**: the invoice `FOR UPDATE` and every figure read after it, so a payment, a
hold or another run that held the invoice first has committed by then; an anonymised
customer is skipped **`customer_anonymised`**; the engine judges the invoice again on
today, and an action other than the item's — paid meanwhile, another letter now in
flight, held, newly outdated or unreviewed — is skipped **`action_changed`**. So is an
invoice whose customer is no longer the one its recipient was read for (merged since),
and — while the bank data is stale and the run unconfirmed — a letter that now claims a
fee, the compensation or interest though the pre-pass judged it free of charges (a
delivery recorded meanwhile, say): the stale guard holds under the lock too. Otherwise the
letter is inserted with the next sequence of the invoice: its level and whether it
announces the hand-off, the channel and recipient, the language (the buyer snapshot's when
it is English, Norwegian otherwise), the run and the caller — `queued` for e-mail, due for
the worker at once, or `awaiting_print` for paper. **A letter is created without its
facts**: its day, deadline, regime and amounts are written when it is sent, by e-mail, or
printed for a posting day, because its deadline runs from its sending (inkassoloven § 9)
and its fee is judged on its own date (inkassoforskriften § 1-2). At the end the run's
`letters` and `skipped` are set, once; a run that stopped half-way keeps neither, and its
letters say what it made. 201 with the run, the letters made and the items skipped with
their reasons.

**Two runs over one invoice** serialise on its lock: the second sees the first's letter in
flight and skips it `action_changed`, and `UNIQUE (invoice_id, sequence)` is the floor
beneath. **The lock order** of an item is the invoice alone, then the letter inserted; the
directory is never read under it.

`GET /invoices/reminder-runs` (`invoices:access` and `invoices:payments`) lists the runs,
newest first, paged; `GET /invoices/reminder-runs/{id}` answers one run and its letters
with their current status, in the order it made them (404 for none). The items a run
skipped are answered by the run itself only; its row keeps their count.

**An invoice's letters.** An issued invoice answers `reminders` — every letter, any
status, by sequence, its facts once it has them — and `nextAction`; a letter's
`recipient` is answered only to a caller holding `invoices:payments`.

### Letters

A letter is one row of `invoices.reminders`, made by a run without its facts and moved
by the worker, a print batch or a person through its statuses:

| Status | What it means | What moves it on |
| --- | --- | --- |
| `queued` | An e-mail letter waiting for the worker, due at `next_attempt_at` | the worker sends it, withdraws it at sending, or fails it; a person withdraws it |
| `awaiting_print` | A paper letter waiting for a print batch | a print batch; a person withdraws it |
| `printed` | Printed for a posting day, its facts written | the posting confirmed sends it; a person withdraws it |
| `sent` | Mailed, or posted — final | nothing: its PDF's key and hash are set once, and the erase blanks its recipient |
| `withdrawn` | Will never go — final | nothing but the erase's blanking |
| `failed` | Unsent 48 hours after its first attempt | a person's retry puts it back in the queue |

**Why the facts are written at sending.** A letter's deadline is at least 14 days from
the day it is sent (inkassoloven § 9) and its fee is judged on its own date
(inkassoforskriften § 1-2), so a run that makes a letter on Monday that goes on Thursday
must not fix Monday's figures. The facts — `sent_on`, the deadline, the regime, the
principal open, the fee or the compensation, the earlier charges, the interest with its
segments and from-date, what of it is waived and paid, the inkassosats, the total and
the charge notes — are written by each dispatch attempt for e-mail (on the day it is
mailed) and by the print for paper (for the posting day), and frozen once the letter is
sent. A failed attempt clears them again with its lease, so a letter carrying facts is
always one a live claim holds — or a printed one.

**Withdrawn by whom.** `withdrawal_reason` is a code when the module withdrew the letter
and `withdrawn_by_user_id` is NULL — `settled`, `on_hold`, `handed_off`, `policy_none`,
`customer_anonymised` or `action_changed` — and the person's own words when a person did.

**What makes and moves them**: a run makes them ([Runs](#runs)); the worker sends the
e-mail ones ([The worker](#the-worker)); a hold, a hand-off and the erase withdraw the
ones in flight ([Holds and the hand-off to collection](#holds-and-the-hand-off-to-collection));
a person withdraws one or retries a failed one:

- `GET /invoices/reminders` (`invoices:access`) lists the letters, newest first, filtered
  by `status`, `channel`, `invoiceId` and `runId`, paged (25 by default, at most 100); a
  status or a channel that is none of the letters' is a 400. A letter's `recipient` is
  answered only to a caller holding `invoices:payments`; its `heldReason` says why a
  queued letter waits.
- `GET /invoices/reminders/{id}/pdf` (`invoices:access`) answers a printed or sent
  letter's PDF — the stored object, read whole and verified against `pdf_sha256`, never
  rendered again — as `purring-<number>-<sequence>.pdf` or
  `inkassovarsel-<number>-<sequence>.pdf` (`reminder-…` and `collection-notice-…` in
  English), `Cache-Control: private, no-store`. 404 for no letter; 409
  **`reminder_not_sent`** for one neither printed nor sent; 500 when the object is gone,
  altered or never recorded; 503 `storage_unavailable` when the store is not configured
  or cannot be read.
- `POST /invoices/reminders/{id}/withdraw` `{reason}` (`invoices:access` and
  `invoices:payments`) withdraws a `queued`, `awaiting_print`, `printed` or `failed`
  letter with the person's reason (1 to 200 characters) and id. **The letter alone is
  locked**; the worker re-reads its status after its own locks, so a letter withdrawn
  before its dispatch takes it is never sent. 400 on `reason`; 404; 409
  **`reminder_not_withdrawable`** for a sent or withdrawn letter, and for one **being
  sent** — `queued`, its facts written, under a lease still live (`lease_until` after the
  request's clock reading): it is mailed outside any lock, and a withdrawal then would
  leave a mailed letter recorded as withdrawn. A minute later it is sent, or its attempt
  failed and it may be withdrawn again.
- `POST /invoices/reminders/{id}/retry` (`invoices:access` and `invoices:payments`)
  puts a `failed` letter back to `queued`, due at once, its attempts, its 48 hours and its
  backoff begun again; the letter alone is locked. The next claim judges it again and
  withdraws it if the invoice was paid, held or handed off meanwhile. 404; 409
  **`reminder_not_failed`**.

### The worker

**`invoices-reminders`** sends the letters queued for e-mail. It runs on every
installation, whatever `INVOICES_EHF_ENABLED` says — a letter is queued only while
reminders are on and mail is available, and each claim judges its letter again — and
polls every 5 seconds; a cycle handles at most **five letters, one at a time, at most one
a second**. A claim takes one `queued` letter whose `next_attempt_at` has come by a
conditional `UPDATE` over a `FOR UPDATE SKIP LOCKED` pick, with a **60-second lease**, so
two workers — two replicas — never take one letter; a lease is free again at exactly its
end (`lease_until <= now`), the complement of "being sent", so no instant has a letter
both claimable and unwithdrawable, or neither. The claim's one clock read is its time,
and its Oslo day its `today`. Then four steps:

1. **One transaction, the invoice `FOR UPDATE`, then the letter `FOR NO KEY UPDATE`** —
   the order of every invoice-then-letter path (a run, a hold, a hand-off, the erase).
   The letter is re-read: no longer `queued` under this claim's lease — withdrawn
   meanwhile, or claimed again after this lease ran out — and the claim ends. Otherwise
   the engine judges the invoice on `today` with this letter left out of the letters in
   flight ([The rules](#the-rules)):
   - **a rate missing or the regime unreviewed** — a half-year the letter needs has no
     rate row, or it would carry a fee or be a collection notice past the review — and
     the letter waits an hour, `held_reason` `collection_rates_outdated` or
     `collection_regime_unreviewed`, **the try not counted**: neither `attempts` nor
     `first_attempt_at` moves, so waiting never makes it `failed`. Attention names the
     waiting letters by cause. Adding the rate or reviewing the regime lets the next
     claim send it, and clears `held_reason`;
   - **the letter no longer goes** — the customer anonymised (`customer_anonymised`), the
     principal settled (`settled`), the invoice handed off (`handed_off`) or on hold
     (`on_hold`), the customer's policy `none` (`policy_none`), or the engine giving
     another action or level — reminders switched off, another letter on its way, a
     notice where a reminder was made (`action_changed`) — and it is **withdrawn** with
     that reason and no user;
   - otherwise its **facts are written**, `sent_on` today and the deadline
     `max(deadline_days, 14)` days on, moved to the next business day off a weekend or a
     public holiday.
2. **The PDF** is rendered from the row ([The letter's content](#the-letters-content)) and
   stored once — `Exists` before `Put` — at
   `reminders/<invoiceId>/<reminderId>-<sentOn>-<sha256>.pdf`, outside any lock, the store's
   two calls under a 15-second timeout. The key carries the hash of the bytes, so a
   different render — a same-day retry after a payment moved the principal — lands on a
   key of its own and never records a new hash over an old object; earlier objects stay
   (the module deletes none). The row is told the key and the hash.
3. **The mail**, through the installation's SMTP settings, only **while the lease still
   covers it**: a claim with less than 30 seconds of its lease left — the send's
   20-second timeout and a 10-second margin for marking it sent — does not send, and the
   attempt fails. Nor is it sent **past Oslo midnight**: when the clock read just before
   the send is on a later Oslo day than the claim's `today`, the letter is put back, due
   at once, its lease and facts cleared, `held_reason` empty and **the try not counted**,
   so the next claim judges it on the new day — a letter never leaves with a `sent_on`
   and a deadline counted from the day before it was mailed (inkassoloven § 9). The mail goes to the letter's recipient, from the seller's legal name,
   **Reply-To the seller's e-mail** in the settings, the subject "Purring: faktura
   {n}" or "Inkassovarsel: faktura {n}" ("Reminder: invoice {n}", "Debt collection notice:
   invoice {n}"), a short cover in the letter's language and the PDF attached. Its
   **Message-ID** is `reminder-<uuid>@vantigo.invalid`, made at the letter's first claim,
   stored on the row while it is queued and kept on every retry: a mail sent twice — the
   server took it but the mark never came — is recognisably one, and never another
   installation's.
4. **One transaction, the invoice, then the letter** — the letter, still this claim's,
   **`sent`** at the claim's time. When a hold was placed, or a lift barred the charges,
   while the letter was being sent (steps 2 and 3 run under no lock, and a hold leaves a
   letter being sent alone), its fee and compensation are **waived `claimed_in_error`**
   in this transaction, after the status change, in the name of the run's author. A
   letter the mail server took but the claim no longer holds at its mark — the process
   stalled past the lease and another actor moved it — is left as that actor left it,
   and the worker logs it at warn.

**A failed attempt** — the store (or no store configured), the settings unreadable, the
send, a lease run short, a render — counts the attempt, records in `last_error` a fixed
sentence naming which step failed (never the mail server's or the store's own words,
which may quote the address), and **clears the lease and the facts**, so the
letter is not "being sent" and may be withdrawn again, and is due again after
`min(3600, 2^n)` seconds for its n-th attempt. A letter still unsent **48 hours after its
first attempt** is **`failed`** (`failed_at`), an attention item until a person retries
or withdraws it. Every write of a claim names its lease and the status it saw, and
changes nothing when another actor moved the letter. No call leaves the module inside a
transaction.

### The letter's content

A letter is one A4 page in the invoice PDF's layout and font, in the letter's language —
English when the buyer snapshot's is, Norwegian otherwise — rendered from its row and its
invoice's snapshots, so a row renders the same bytes every time (its creation date is
`sent_on`). It holds, in order:

- the seller block (the seller snapshot: name, address, organisation number with MVA,
  Foretaksregisteret, e-mail) and the buyer block, the date `sent_on`, the heading
  **"Purring"** or **"Inkassovarsel"** ("Payment reminder", "Debt collection notice");
- the invoice it concerns — number, issue date, due date — and the payment deadline, with
  an opening that says payment was not registered, or, when part of the invoice is paid,
  that the full amount was not received;
- **the claim with every amount apart** (inkassoloven § 10 c and d by choice): the
  invoice's total, what its issued credit notes had taken off it when the facts were
  written (`credited`), what was paid, the
  **principal open**; the **earlier fees and compensation still outstanding**
  (`charges_earlier`) — worded **as a credit from earlier payments of charges** when it is
  negative, the charges paid beyond the earlier ones paying this letter's own; this
  letter's **reminder fee** — on an inkassovarsel "Gebyr for inkassovarsel" ("Collection
  notice fee") — or **compensation for recovery costs**; the **late payment
  interest** accrued to `sent_on` from its from-date, each segment with its rate, its days
  and its base; what of the interest is waived and what is already paid; and **the amount
  to pay**;
- "Betal {total} innen {deadline}", the account number, the invoice's **KID** when it has
  one with "Merk betalingen med KID {kid}", else "Merk betalingen med fakturanummer {n}",
  and IBAN and BIC when the seller has them;
- **an inkassovarsel** says, clearly, "Kravet vil bli sendt til inkasso dersom det ikke
  er betalt innen {deadline}" (inkassoloven § 9) and that collection **may** add costs —
  never that it will;
- **a reminder announcing the hand-off** under the 2026 act says the claim "vil bli
  oversendt til et inkassoforetak" if unpaid by the deadline (Prop. 3 L (2025–2026)
  12.5.5);
- every letter: "Har du betalt i mellomtiden, kan du se bort fra dette brevet", and the
  objection sentence "Har du innsigelser mot kravet, gi oss beskjed før fristen" (FinKN
  2025-240).

The English letter says the same in English. The words are pinned by text goldens of
each level in both languages ([`testdata/reminders`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/invoices/testdata/reminders)).

### Paper and posting

**A paper letter is sent when it is posted, and only on the day it bears.** A run makes a
paper letter `awaiting_print`, without facts. A person prints letters for the day they
will go in the post — a **print batch** — and each letter is judged again for that day
and given that day's facts; when the person confirms the batch was posted that day, its
letters are `sent`. Posted on any other day, the batch is reprinted and its letters
printed again for the right one. Every fact on a letter — R7's 14 days, R10's passed
deadline, the six-month reset, the inkassosats, the regime and its review, the deadline
itself — is judged for its posting day: posted earlier, a letter would carry a fee judged
for a later day; posted later, it would give the debtor less time than it says
(inkassoloven § 9, inkassoforskriften § 1-2). A printed letter is in flight
([The rules](#the-rules)): no run writes to its invoice, and it counts for no fee, until
it is posted or reprinted.

**`invoices.reminder_print_batches`** is one batch: `post_on`, who made it and when, and
either the posting — `posted_on`, `posted_by_user_id` and `posted_at`, set together once
— or the reprint, `reprinted_at`, set once; never both, and never anything else.

**Printing.** `POST /invoices/reminder-print-batches` `{reminderIds, postOn}`
(`invoices:access` and `invoices:payments`):

- `reminderIds` names 1 to 200 letters, each once and each a letter; `postOn` is today or
  one of the next 7 days (Oslo) — each a 400 on its field. Without an object store, 503
  `storage_unavailable`. Then every letter is read on the pool: one that is not
  `awaiting_print` is 409 **`reminder_not_awaiting_print`**, naming it. Nothing is
  written before these.
- The batch row is inserted, and then **each letter in its own transaction: the batch
  `FOR SHARE`, its invoice `FOR UPDATE`, then the letter `FOR NO KEY UPDATE`, then the
  collection rates in force on `postOn` `FOR KEY SHARE`** — and only then are the
  engine's rates read. The batch's share waits on a posting or a reprint of the batch
  (`FOR NO KEY UPDATE`), and they on it, so a batch is never posted or reprinted while a
  letter is being printed into it: a letter printed first is seen by the posting or the
  reprint, and one whose turn comes after finds the batch closed. The rates are, of each
  kind, the row a letter dated `postOn` relies on — the rows [deleting a
  rate](#collection-rates) judges "used". The rate's `DELETE` takes its row `FOR UPDATE`
  first, so it waits for the letter's transaction and then sees the letter printed, and
  is refused; or it deleted the row before, and the letter is judged without it. The
  share decides "in force" on its own snapshot, so once the letter is judged the rows in
  force are read again, and a row that came into force on `postOn` meanwhile — added, or
  left in force by a deletion the share waited on — rolls the letter's transaction back
  and tries it again (three times at most). The letter is judged on `L = postOn`, with
  itself left out of the letters in flight, as the worker's first step judges an e-mail
  letter ([The worker](#the-worker)):
  - a letter of a batch posted or reprinted while this request was still printing it is
    **left out**, `print_batch_closed`, and stays `awaiting_print` for another batch;
  - a letter no longer `awaiting_print` under its lock — withdrawn meanwhile, or printed
    by another batch naming it — is **left out** as it is, `not_awaiting_print`;
  - one that on `postOn` needs a rate with no row for a half-year, or would carry a fee or
    be a collection notice past the regime review, is **left out and stays
    `awaiting_print`**, with `collection_rates_outdated` (naming the kind and the
    half-year) or `collection_regime_unreviewed` — as a run refuses and the worker waits;
  - one the engine no longer gives on `postOn` — the principal settled, the invoice held
    or handed off, the customer's policy `none` or the customer anonymised, another action
    or level — is **withdrawn** with that reason and no user, and left out;
  - every other gets its facts — `sent_on` is `postOn`, the deadline runs from it, the
    fee or compensation, the interest to that day — and is **`printed`** in the batch.
- After the last letter, outside any transaction, each printed letter's PDF is rendered
  from its row and stored once under its hash's key, `reminders/<invoiceId>/<reminderId>-<postOn>-<sha256>.pdf`,
  and recorded on the letter while it is still in the batch, printed — or sent, when the
  batch was posted in between — and has none. A store that fails is logged; the letter
  stays printed, its own download is a 500 for now, and the batch's PDF is rendered from
  the rows anyway. **The next posting of the batch, and the next download of its PDF**,
  store every printed or sent letter of it that has no PDF yet, outside any transaction
  — the stored PDF is what the letter's own download answers, never a render. Each of
  the three stores the letters one at a time and **stops at the first the store fails**
  (each call may wait 15 seconds), logging it once and answering as it otherwise would,
  so a store that is down costs a request one timeout, not one per letter.
- 201 with the batch and its letters, `pdfUrl` — its combined PDF — and `leftOut`, each
  letter left out with its invoice and reason.

**The combined PDF.** `GET /invoices/reminder-print-batches/{id}/pdf` (`invoices:access`
and `invoices:payments`) renders the batch's **`printed` and `sent`** letters — never one
withdrawn since printing — from their rows, in id order, each starting on a page of its
own, read in one snapshot and rendered after it: the same pages as often as it is asked
for — a letter renders from its facts alone, and what it prints as credited is
`credited`, fixed with them, so a credit note issued after printing changes nothing on
paper already printed — `paper-letters-<id>-<postOn>.pdf`, `Cache-Control: private, no-store`. 404 for no
batch, or one with no printed or sent letter (reprinted, or every letter withdrawn or
left out).

**Confirming the posting.** `POST /invoices/reminder-print-batches/{id}/posted`
`{postedOn}` (`invoices:access` and `invoices:payments`): a `postedOn` after today is a
400 — no day to come is posted. Then **the batch `FOR NO KEY UPDATE`** (the key-share
locks its letters' foreign keys take on it never conflict with that; a letter still being
printed into it holds it `FOR SHARE`, and the posting waits for that letter), **its letters'
invoices `FOR UPDATE` in descending id, then each letter**: a batch posted or reprinted
already is 409 **`print_batch_closed`**; `postedOn` before `postOn` 409
**`reminder_posted_early`**, after it 409 **`reminder_posted_late`** — reprint the batch.
On `postOn` itself, **the re-judge**: each `printed` letter is judged on `postOn` again,
left out of the letters in flight, becomes **`sent`** with `sent_at` the confirmation's
time, and keeps the facts and the PDF it was printed with — it is in the post already.
A letter withdrawn by hand since printing is **skipped**, never sent, and listed. A letter
whose invoice was **settled, put on hold, handed off or its customer anonymised** since
printing (holds, hand-offs and the erase leave printed letters to the posting), whose
customer's policy became `none`, whose charges **a lift barred**, or whose re-judged
outcome **no longer carries the fee or compensation it printed**, has that fee and
compensation **waived `claimed_in_error`** ([Charges](#charges)) in the same
transaction, after it is marked sent — a waiver names a sent letter — in the name of
the person confirming. 200 with the batch, `skipped` and `waived` — each waiver's letter,
invoice, kinds and reason (`settled`, `on_hold`, `handed_off`, `policy_none`,
`customer_anonymised`, `charges_barred` or `action_changed`). 404 for no batch.

**Reprinting.** `POST /invoices/reminder-print-batches/{id}/reprint` (`invoices:access`
and `invoices:payments`): **the batch `FOR NO KEY UPDATE`, then every printed letter of
it**, which goes back to `awaiting_print` with every fact, its batch and its PDF key
cleared — a later batch prints it again for its own day, under a new key (the old object
stays). Letters withdrawn since printing keep naming the batch. A batch posted or
reprinted already is 409 **`print_batch_closed`**: a batch is reprinted at most once, and
a posted one never. 200 with the batch; 404 for no batch. **The reprint locks no
invoice**, the one path that writes a letter without its invoice's lock, and it cannot
cycle: the reprint never waits on an invoice, so a cycle would need a path holding a
printed letter of the batch while it waits on something the reprint holds — and none
does. The worker claims only queued letters; a hold, a hand-off and the erase hold the
invoice and write only the letters in flight (`queued`, `awaiting_print`, `failed`),
whose `UPDATE` passes a printed row without waiting on it — the erase among them, which
locks no printed letter because a paper letter's recipient is `''` already, and must keep
to that; a print batch's letter holds its own batch `FOR SHARE` and its invoice, then waits
on the letter and on the rates, never on a batch it does not hold — and a letter of the
reprinted batch itself takes the batch first, so it and the reprint queue on the batch
row; a person's withdrawal holds the letter alone; and the posting takes the batch first,
so it and a reprint queue on the batch row.

**The list.** `GET /invoices/reminder-print-batches` (`invoices:access` and
`invoices:payments`) answers the batches newest first, each with its letters, paged (25
by default, at most 100): `posted=true` the posted ones, `posted=false` the open ones —
neither posted nor reprinted, which the paper page lists to post — absent, all. 400 on
paging.

## Holds and the hand-off to collection

An invoice the customer disputes is put **on hold**, and one a collection agency has
taken over is **handed off**. Both stop the letters; neither stops the money. Each is
recorded under the invoice's lock and read by the reminder engine
([The rules](#the-rules)): a live hold makes the next action `blocked` with `on_hold`; a
live hand-off makes it `none` with `handed_off`.

**The hold.** `POST /invoices/{id}/hold` `{note}` (`invoices:access` and
`invoices:payments`) marks an issued invoice disputed — `kind` `disputed`, the note (1 to
500 characters) saying what the customer disputes, who placed it and when. Refused in
order: 400 on `note`; 404; 409 **`credit_note_no_reminders`** for a credit note, a draft
one included, and **`invoice_draft`** for a draft; then one transaction locks the invoice
and, after that lock, refuses **`invoice_on_hold`** while a hold is live (one live hold per
invoice, `ux_invoice_holds_live` beneath). In the same transaction every letter of the
invoice still in flight — `queued`, `awaiting_print` or `failed` — is withdrawn with the
reason `on_hold` and no user. Two letters are left alone and named in the answer's
`lettersLeft` (`reminderId`, `status`, `printBatchId`), by sequence:

- a **printed** letter, which may be in the post already: it is left for the posting's
  re-judge, which sends it with its charges waived `claimed_in_error` when the invoice is
  held by then, and a person can pull it from the batch and withdraw it by hand;
- a letter **being sent** — `queued`, its facts written, under a lease still live
  (`lease_until` after the request's clock reading): it is mailed outside any lock and
  becomes `sent`. A letter being sent when charges are barred is marked sent with its fee
  and compensation waived `claimed_in_error`; one already sent when a barring lift comes
  is waived by the lift.

While the hold is live no run makes a letter for the invoice, its late interest keeps
running, and it still takes payments, manual or from a bank file
([Matching](#matching)).

**The lift.** `POST /invoices/{id}/hold/lift` `{chargesAllowed, note}` lifts the live hold,
once, and records the answer to the question the law asks: was the objection **obviously
groundless**? `chargesAllowed` is required — a body without it is a 400 on it, never read
as `false` — and the note holds at most 500 characters. One transaction locks the invoice
(404 for none) and refuses **`invoice_not_on_hold`** when no hold is live.

- `chargesAllowed: false` — the form's default — means the objection had reasonable
  grounds. A creditor may then claim no costs for the period of the dispute (inkassoloven
  § 17 second paragraph; the 2026 act's § 18), so **every fee and compensation the
  invoice's sent letters claimed, and no waiver released yet, is waived** in the same
  transaction — reason `objection_upheld`, the lift's note as the waivers' note, one
  waiver per letter and kind, a fee waived before (goodwill, say) skipped — and fees and
  the compensation are **barred on the invoice for good**: the engine reads a lifted hold
  with `charges_allowed = false` and gives every later letter no fee and no compensation,
  with the charge note `charges_barred`. A printed letter is not sent, so it claimed
  nothing yet; the posting judges it.
- `chargesAllowed: true` — the objection was groundless — waives nothing.

**Late interest is not a cost**: the lift never waives it, and it keeps running on the
invoice whatever the answer.

**The hand-off.** `POST /invoices/{id}/collection` `{handedOn, agency, agencyReference?,
note?, acknowledgeNotDelivered?}` (`invoices:access` and `invoices:payments`) records that
the claim was handed to a collection agency — done outside Vantigo, recorded here.
Refused in order:

1. 400 on the fields: `handedOn` missing or after today (Oslo); `agency` blank or over
   200 characters; `agencyReference` over 100; `note` over 500;
2. 404; 409 `credit_note_no_reminders`, `invoice_draft`;
3. 400 on `handedOn` before the invoice's issue date;
4. under the invoice's lock: 409 **`invoice_settled`** when nothing of the principal is
   open; 409 **`invoice_handed_off`** while a hand-off is live (one per invoice,
   `ux_collection_handoffs_live`); 409 **`invoice_not_delivered`** when no delivery — an
   e-mail, a delivered EHF transmission or a manual delivery
   ([The delivery fact](#the-delivery-fact)) — is on or before the due date, unless
   `acknowledgeNotDelivered` is `true`. An invoice not validly delivered may not have
   fallen due (FinKN 2017-492), so the refusal's detail asks for a manual delivery first
   when the invoice was in fact delivered; when it was not, the acknowledgement records
   what was done regardless — refusing the record would only keep Vantigo writing to a
   debtor whose claim sits with an agency.

The hand-off is then inserted and the letters in flight are withdrawn `handed_off`, as a
hold withdraws them, the printed letters and any being sent named in `lettersLeft`. While
the hand-off is live no letter is made, and **payments are still registered** — the
creditor still owns the claim (inkassoloven § 2) — and a payment received directly must be
reported to the agency. `POST /invoices/{id}/collection/withdraw` `{withdrawnOn, reason}`
ends it: 400 on `withdrawnOn` (missing or after today) or `reason` (1 to 200 characters);
then under the invoice's lock 404, 409 **`invoice_not_handed_off`** when none is live, and
400 on a `withdrawnOn` before the hand-off's `handedOn`. The row keeps who withdrew it, the
day and why, and the engine judges the invoice again.

Each of the four answers `{invoice, lettersLeft}`: the document — which carries `hold` and
`handoff`, its latest of each, live or ended, absent when there was none — and the letters
no withdrawal reached. A lift and a withdrawal withdraw nothing; their `lettersLeft` names
the printed letters and any being sent all the same. **The lock order** (D18): the
invoice, then the letters withdrawn — reported to the lock-order seam by id — and, on a
barring lift, the waivers inserted; a lift and a withdrawal lock the invoice alone. A
withdrawal by the module records no user and writes the reason as a code (`on_hold`,
`handed_off`; the erase writes `customer_anonymised` the same way), where a person's
withdrawal writes their own words.

**The collection file.** `GET /invoices/collection-export.csv` (`invoices:access` and
`invoices:payments`) is what an agency is sent: one row per invoice, either the invoices
with a live hand-off whose `handedOn` is from `handedFrom` to `handedTo` (both, together),
or the issued invoices named by `invoiceId`, repeated, 1 to 500 — exactly one of the two.
400 for neither or both, one date alone, `handedFrom` after `handedTo`, more than 500 ids
or one that is not an issued invoice, and past 500 rows — never a file cut short. Rows in
invoice number order. The file is the module's CSV form ([The CSV export](#the-csv-export):
UTF-8 with a byte order mark, semicolons, the decimal comma, YYYY-MM-DD, CRLF, RFC 4180
quoting, the formula guard on the text columns only), `text/csv`, served as
`invoices-collection-<today>.csv` with `Cache-Control: private, no-store`. Its columns,
fixed and English, in this order — **the principal apart from the charges, and what was
waived out of what is claimed**:

| Column | Holds |
| --- | --- |
| `Invoice number`, `Issue date`, `Due date` | The invoice's. |
| `Delivery` | The delivery of what was sold: its day, or the period as `from/to`. |
| `Delivered` | The first recorded delivery of the invoice, its kind and day: `handed_over`, `posted`, `email` or `ehf`, then the date. |
| `KID` | The invoice's KID, or empty. |
| `Customer number`, `Debtor`, `Debtor type`, `Org no`, `Foreign id`, `Address line 1`, `Address line 2`, `Postal code`, `City`, `Country` | The buyer snapshot written at issue. No national identity number is held, so none is exported. |
| `E-mail` | The customer's reminder address, read from the customer directory with no transaction open, before the snapshot below; empty when the read fails, which is logged at warn, and for a customer only the snapshot's re-read sees. |
| `Gross`, `Credited`, `Paid`, `Principal open` | The principal: the gross, what the issued credit notes credited, the live payments, and what is open of it. |
| `Payments` | Each live payment as `day amount`, joined by ` \| `. |
| `Fees claimed`, `Compensation claimed` | What the sent letters claimed, net of waivers. |
| `Charges waived` | Every waiver's total — fees, compensation and interest. |
| `Interest rate`, `Interest from`, `Interest to`, `Interest accrued` | The late interest as a letter today would claim it: the rate of the last interest segment (the one in force today), the first day it accrued, today, and the amount less what was waived of it. Empty but for `Interest to` and `0,00` when no interest applies yet; the rate and the amount empty when a rate the period needs is missing. |
| `Charges paid` | The live charge payments. |
| `Letters` | Each sent letter as its day, level, deadline and the fee or compensation it claimed — `2026-07-30 reminder deadline 2026-08-13 fee 35,00 (waived)`, `(waived)` when a waiver released it — joined by ` \| `. |
| `Notice sent`, `Notice deadline` | The latest sent collection notice's day and deadline. |
| `Disputed` | `yes` while a hold is live, else `no`. |
| `Handed on`, `Agency`, `Agency reference` | The live hand-off's, empty without one. |

The reads, in order, with one clock reading and no lock: the selection's rows on the pool,
with no transaction open, judged against the cap (the ids each taken once before the 500
cap counts them); the reminder addresses from the customer directory, still with no
transaction open — the directory's own reads take a pool connection, and one asked for
while the export held another could starve a small pool; then one read-only
`REPEATABLE READ` transaction that reads the rows again, judges them again, and reads the
engine's inputs (the rule-input loader's read) and the deliveries — one snapshot for every
figure in the file. A customer the snapshot's rows name and the first read did not (a
hand-off recorded in between) gets an empty `E-mail`, logged at warn.

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
Number;Kind;Issue date;Delivery;Due;Customer number;Buyer;Buyer org no;Currency;SAF-T code;Rate;Base;VAT;Base NOK;VAT NOK;Credits number;KID;Project
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
| `KID` | an invoice's KID, appended in phase 2 so the earlier columns keep their places; empty without one and on a credit note. Guarded as text, though a KID never begins with a character the guard is for. A spreadsheet that reads it as a number strips its leading zeros — import the column as text |
| `Project` | the document's project reference as frozen at issue — a credit note's its original's ([The project](#the-project)) — appended last in phase 3; empty on a document that names no project |

**The byte format** is the expenses payroll file's ([the payroll CSV](/en/reference/expenses/#the-payroll-csv)),
duplicated into this module as customers duplicated it — depguard keeps modules from
sharing it: UTF-8 with a byte order mark, `;` between cells, the decimal comma and two
decimals, dates as `YYYY-MM-DD`, CRLF after every row the last included, and RFC 4180
quoting — a cell holding `;`, `"`, CR or LF is quoted and its quotes doubled. Every
amount is the stored `numeric` as exact text, never a float, and a credit note's `0,00`
stays `0,00`.

**The formula guard is on the text columns only.** `Kind`, `Delivery`, `Customer number`,
`Buyer`, `Buyer org no`, `Currency`, `SAF-T code`, `Credits number`, `KID` and `Project` get an apostrophe in
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
the same length just before" is what the delta compares against. There is no timeseries.

### Attention

`GET /invoices/stats/attention` is the dashboard's attention list for Invoices, in the
item shape every module's `/stats/attention` shares — `id`, `type`, `title`,
`occurredAt`, `entityId` and, where an item stands for several things, `count` — the
host translating the sentence from `type` (design D12, plan reading 29). It needs
`invoices:access`. The first two types are answered to every caller; the other four
only to a caller who also holds `invoices:payments`, and a caller without it is answered
the first two — never a 403. Every item is derived from the rows as they are, so nothing
is dismissed: each clears when what it is about is done. One clock read: "today" is the
request's Oslo day.

| `type` | One item per | `entityId`, `title`, `occurredAt`, `count` | Clears when |
| --- | --- | --- | --- |
| `invoiceOverdue` | one of the **20 most overdue** issued invoices — overdue today by the state the list derives, oldest due date first — once the day after its effective due date E has come (the due date moved off a weekend or a public holiday; an invoice due on a Saturday is an item from the Tuesday) | the invoice; the buyer's name; the day after E at UTC midnight; — | it is paid or credited |
| `invoiceRefundDue` | every issued invoice whose open amount is **below zero** — a credit note issued after a payment (M16) — or whose charge payments exceed every charge less its waivers (the [charges](#charges)' `refundDue`); uncapped | the invoice; the buyer's name; when its money last moved (the latest credit note issued, payment or charge payment registered, or waiver); — | the money paid back outside Vantigo is recorded by removing the payment or the charge payment with a reason — refunds are not a flow in this phase |
| `bankTransactionsOpen` | bank file with lines `pending` (matching stopped early), `exception` or `duplicate` | the file; its booking days (`2026-10-01`, or `2026-10-01 – 2026-10-07`); its upload; the open lines, as the queue counts them | every line is matched or resolved ([The exception queue](#the-exception-queue)) |
| `reminderFailed` | letter whose e-mail failed for good | the letter's invoice (the item's `id` names the letter, `reminderFailed/<letterId>`); the buyer's name; its failure; — | it is retried or withdrawn |
| `remindersHeld` | cause queued letters wait on (`held_reason`, plan reading 46): `collectionRatesOutdated` — a collection rate missing for a half-year a letter needs — or `collectionRegimeUnreviewed` — the 1988 regime past its review | the cause, as `entityId` and `title`; the oldest waiting letter's creation; the letters waiting | the rate is added or the review made, and the worker sends them |
| `reminderBatchUnposted` | print batch neither confirmed posted nor reprinted from **two days** after its posting day | the batch; its posting day; that day plus two at UTC midnight; its printed letters | it is confirmed posted or reprinted ([Paper and posting](#paper-and-posting)) |

`id` is `<type>/<entityId>` but for `reminderFailed`. A credited invoice whose letters'
charges are still outstanding is in none of them: it owes nothing back, and the overdue
list's `charges=outstanding` covers only paid invoices — a follow-up.

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

**The receivables are kept with the document too** (design D19): its letters — a sent
letter is the documentation of the claim, and the evidence the bad-debt VAT relief needs
(merverdiavgiftsloven § 4-7-1) — its charge payments, waivers, manual deliveries, holds
and hand-offs; and the bank files and their lines with the payer data the bank wrote,
which are the bank's record of money received, bookkeeping material under § 13 like the
payments. Bank files, letters and their PDFs are kept five years after the end of the
financial year, as the invoice PDFs; the module deletes no object, and an anonymisation
blanks addresses and notes in them, never a row.

**A timesheet is kept with its document** for the same five years: an issued
invoice's rows are part of the sales document its PDF printed, which art. 17(3)(b)
exempts from erasure, and nothing identity does to a user — renaming, disabling or
deleting them — touches a row ([The timesheet](#the-timesheet)). A draft's rows go with
the draft.

**The work a document billed is kept with it** too: an issued document's
`line_sources` and a credit note's `line_releases` are frozen with it and never deleted,
the record of which hours, expenses and milestones the invoice stamped and which its
credit notes gave back; a draft's held rows go with the draft, and so with an
anonymisation's deletion of the drafts. They name other modules' rows by id and carry
no name; a person's export does not include them.

The module fills both customer slots ([module boundaries](/en/contributing/module-boundaries/)):

- **Merging customers** (`contracts.CustomerReferenceHolder`) re-points every document of
  the absorbed customer, drafts and issued, reported as `invoices.invoices`. An issued
  document keeps its buyer snapshot — the id is not printed, the snapshot is — and its
  revision. Payments, deliveries and transmissions — and the receivables: charge
  payments, waivers, manual deliveries, letters, holds and hand-offs — hang off the
  document by id and carry no customer id, so they follow it and are not reported; bank
  lines hang off their file and name no customer at all. The customer's
  reminder policy is keyed by customer and is re-pointed after the documents, both
  rows locked by customer id ascending: moved when the survivor has none, merged into
  the survivor's at the stricter mode with the notes joined when both have one
  ([A customer's reminder policy](#a-customers-reminder-policy)), reported as
  `invoices.customerReminderPolicies` — 1 when the absorbed customer had one, else 0.
- **A person's export** (`contracts.CustomerPersonalData`) hands over every issued
  document and every draft, each with its lines, a structured `buyer` — the full
  snapshot: name, type, organisation number, foreign id, GLN, Peppol id, language and
  the address with its region — the `deliveryAddress` when one is set, the references
  — among them `projectReference`, the project's code as the document snapshotted and
  printed it, when its work belongs to one project ([The project](#the-project)) — and
  both notes, and, on a credit note, `credits{number, issueDate}` naming what it
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
  provider's reference. A draft has none of the three. Every document that carries a
  timesheet, issued or draft, carries `timesheet` — its rows as printed, each with its
  position, person label, date, hours, work type and description — since the customer
  received it, or would; never a time entry's note. Each payment carries its `source` —
  `manual`, `ocr` or `camt054` — and an imported one its `bankLine`: the booking day,
  the debtor's name and account and the remittance text, as the bank wrote them. An
  issued document carries its receivables (design D19): `chargePayments` (each with
  its paid date, amount, source, reference, note, registration and removal, and an
  imported one's `bankLine`), `chargeWaivers` (the letter's sequence, the kind, the
  amount, through when for interest, the reason, the note and when), `manualDeliveries`
  (the kind, the day, the note, when it was recorded and a removal), `reminders` — every
  letter in every status, with its sequence, level, whether it announces collection,
  channel, recipient (`""` for paper and once anonymised), language, status, creation,
  `sentOn`, `deadline`, `regime`, the amounts it states, `sentAt`, a failure's time and a
  withdrawal's time and reason; never its PDF's key, its Message-ID or an SMTP error,
  which may quote the address — `holds` (the kind, the note, when placed, a lift's time,
  note and whether charges stay allowed) and `collectionHandoffs` (the day, the agency
  and its reference, the note, when recorded, a withdrawal's day and reason). Each is
  `[]` on an issued document with none; a draft has none of them. The section carries the customer's
  `reminderPolicy` — its mode, note and when it was set — when there is one; a customer
  with only a policy here has a section with no documents.
- **Anonymisation**, inside the customers module's transaction, with one clock read, in
  this order: it locks the person's documents `FOR UPDATE`, newest first — the merge
  holder's statement, the module's lock order — so a delivery insert, whose trigger
  takes the document `FOR SHARE`, waits for it; writes the marker in
  `invoices.erased_customers`; blanks the recipient of every delivery of those
  documents; blanks the note of every payment of them, live and removed; cancels every
  `queued` transmission of them that was never attempted (`submit_attempted_at` NULL),
  leased or not — a worker holding one stamps its marker only on a row still `queued`,
  so it finds the row cancelled and makes no call; deletes the drafts. Then the
  receivables, in design D19's order: every letter **in flight** — `queued`,
  `awaiting_print` or `failed` — is withdrawn `customer_anonymised`, each document's
  under the lock already held, with the hold's own write (plan reading 44); a
  **`printed`** letter is left to the posting, which re-judges it — the paper may be in
  the post already — and a letter **being sent** (queued, its facts written, under a live
  lease) is left to become sent (plan readings 9, 45); both are named at warn in the
  log with their ids, `invoices: the anonymisation left letters printed for the posting
  or being sent`, so a person can pull a printed one from the post and withdraw it by
  hand. Every letter's recipient is then blanked **in every status** — a sent one and
  one a hold withdrew earlier among them, the one write the letters' trigger allows on a
  final row (B1) — touching only a letter whose recipient is not blank already, so a
  printed paper letter, whose recipient is always blank, is never waited for. The notes
  of the charge payments, the waivers, the manual deliveries, the holds and their lifts,
  and the hand-offs are blanked. The `resolution_note` of every **resolved** bank line
  the person's payments or charge payments (live or removed) came from, when it is not
  empty, is blanked — the [lock order](#issuing)'s one named exception — and so is the
  note of every event of every line their payments came from, resolved or not (a
  reopened line's earlier `applied` event among them), but a `reversed` event's: an event
  row is locked by nothing, so the line's status does not matter. Last the customer's reminder policy is
  deleted. It reports thirteen kinds, in this order:
  - `invoices.drafts` — the drafts deleted, invoice and credit-note drafts alike, their
    line sources and timesheet rows with them by the cascade: a draft is not a sales
    document and has no retention basis, so GDPR art. 17 applies;
  - `invoices.documents`, at 0 — every issued document, its buyer snapshot, its
    timesheet and its internal note are kept under § 13; the note is immutable once issued, so
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
    does. The insert trigger refuses any later transmission for the customer;
  - `invoices.reminders` — the letters changed: withdrawn, or their recipient blanked,
    each counted once. A sent letter is kept, its facts and its PDF with it — the claim's
    documentation and the bad-debt relief's evidence (FMVA § 4-7-1) — and its address
    goes; the PDF in the store, rendered when it was sent, keeps the address it was
    sent to, as the invoice PDFs keep the buyer;
  - `invoices.chargePayments`, `invoices.chargeWaivers`, `invoices.manualDeliveries`,
    `invoices.invoiceHolds`, `invoices.collectionHandoffs` — the rows whose note (and a
    hold's lift note) was blanked. The rows are kept with the document under § 13: a
    charge payment's date, amount, source and reference, a waiver's amount and reason, a
    delivery's day, a hold's times and verdict, a hand-off's agency and its reference
    and a removal's or a withdrawal's reason, the audit trail as for payments;
  - `invoices.bankTransactions` — the bank lines whose note, or an event's note, was
    blanked. The lines and their events are kept with the payer data the bank wrote —
    debtor name, account and text — the bank's record of money received. A line not
    resolved keeps its `resolution_note` — an open line has none (a reopen clears it) —
    and every line keeps its `reversed` events' notes: the system's own words naming
    the reversal that took its payment back, the audit trail a payment's removal reason
    is;
  - `invoices.customerReminderPolicies` — the reminder policy deleted (0 or 1): staff's
    decision and note about the person, which no retention rule keeps. A later PUT for
    the anonymised customer is a 404.

  Run twice, it finds nothing and reports zeros, and the marker keeps its first time.
  The marker refuses every later send (`customer_anonymised`), blanks any delivery
  row a send racing the erase writes ([Sending a document](#sending-a-document)), and
  blanks the note of any payment registered after or racing the erase; it refuses a
  later run's letter (`customer_anonymised`), and a letter row inserted after it — a
  run item that held the invoice while the erase waited — is written with no
  recipient. A child row — a charge payment, a waiver, a manual delivery, a hold, a
  hand-off — inserted after it keeps no note, and a lift keeps no lift note. A queue
  action that writes a note on a line — apply, dismiss, handle-reversal,
  confirm-duplicate — locks, after the line, the invoices it applies to and those the
  line's payments and charge payments were ever registered against, in descending id,
  and when any is an anonymised customer's it writes no note, on the line or on its
  event; an erase that comes after it finds the line resolved and blanks it. The
  marker is read by this module only and never removed — anonymisation is never undone.
  `contracts.ErasedData` carries no reason field; this paragraph is where the reasons
  are written.

## Permissions

No built-in role holds any of these; Owner has the wildcard. Phase 4 adds no key (design
D1): it gives each of the five what fits it, and the catalog's descriptions, which an
administrator reads when building a role, say so.

| Key | Sensitive | What it allows |
| --- | --- | --- |
| `invoices:access` | no | Use the app; read every invoice, credit note, PDF, payment and delivery, an invoice's charges, charge payments, waivers and manual deliveries, every document's EHF state and transmissions and download their UBL, the journal, the CSV export and the stats; read the collection rates, the reminder settings and a customer's reminder policy; the overdue list, and an issued invoice's letters, next action, hold and hand-off, and a sent letter's PDF; the attention items about overdue invoices and refunds due ([Attention](#attention)). |
| `invoices:create` | no | Create, edit and delete drafts; preview a draft; list the uninvoiced work, with its people and rates, and make a draft of it, or add it to one; refresh a draft's work and see whether it is still fresh; turn a draft's timesheet on or off; list what earlier invoices have left to deduct ([Invoicing work](#invoicing-work)). |
| `invoices:issue` | yes | Issue a draft — and so mark the work it bills invoiced in its modules — and create a credit-note draft, whose issue releases the work it returns; send an issued document by e-mail, and see where each send went; send it as EHF, cancel a transmission never attempted and resolve an unconfirmed one; record that an invoice was handed over or posted, and remove such a record with a reason ([The delivery fact](#the-delivery-fact)). |
| `invoices:manage` | yes | The seller record and its Peppol id, the series start, the KID agreement, the VAT code each kind of work is invoiced at, the timesheet's default and person label, VAT codes and their rates, the access point's credentials, the format a bank account's files are imported in, the collection rates (add one ahead of a release, delete one nothing has relied on) and the reminder settings, the regime's review among them. |
| `invoices:payments` | yes | Register a payment against an issued invoice, and remove a registration with a reason; import bank files and read the imported files and their accounts; work the exception queue — apply, dismiss, handle a reversal, confirm a duplicate, treat one as distinct, reopen ([The exception queue](#the-exception-queue)); set a customer's reminder policy — whether, and with what charges, they are reminded; register a payment of an invoice's reminder charges and remove one, and waive charges ([Charges](#charges)); preview and make reminder runs and read them ([Runs](#runs)), see the address each letter goes to, print paper letters, confirm them posted or reprint them, and withdraw or retry a letter ([Letters](#letters)); hold a disputed invoice and lift the hold, record a hand-off to a collection agency and withdraw it, and export the collection file ([Holds and the hand-off to collection](#holds-and-the-hand-off-to-collection)); the attention items about the bank lines, the letters and the print batches ([Attention](#attention)). |

**Why reminders are `invoices:payments` and not `invoices:issue`.** A reminder is not a
sales document and takes no number; it is credit control — what the company says it is
owed, the very reason `invoices:payments` is sensitive. A manual delivery is
`invoices:issue`: handing the sale over belongs to whoever issues and sends it, and it
registers no money. The reminder settings, the rates and an account's import format
change what every letter and every import does, so they are `invoices:manage`.

`invoices:payments` is sensitive because a registration changes what the company says it
is owed, and a wrong one is corrected only by a removal that stays on record.

**Sending is under `invoices:issue`**: whoever may create bookkeeping material may hand
it over, and a reader with `invoices:access` alone may download it, as before, and sees
each send's time and subject but not its address. Sending also needs an installation
that can send — `MAIL_DRIVER=smtp` and the `SMTP_*` configuration
([email delivery](/en/admin/authentication/#email-delivery)). `GET /meta` answers
`mailAvailable`, `capabilities.canSend` — `invoices:issue` and `mailAvailable` — and
`capabilities.canRegisterPayments`, so no client re-derives either rule. It answers
`remindersEnabled`, the reminder settings' switch, and `capabilities.canRunReminders`,
`invoices:payments`.

**Sending as EHF is under `invoices:issue` too** ([Sending as EHF](#sending-as-ehf)),
and needs an installation that can: `GET /meta` answers `ehfAvailable` — `INVOICES_EHF_ENABLED` on, the Peppol lookup
enabled (`PEPPOL_LOOKUP_ENABLED`; a send that cannot re-check its receiver does not
send), an access-point credentials row stored and the seller's Peppol id set, all four
([configuration](/en/admin/authentication/#transport-storage-and-modules)) —
`capabilities.canSendEhf`, `invoices:issue` and `ehfAvailable`, and
`accessPointCredentialsRejected`, whether the provider refused the stored key. A refused
key is reported beside `ehfAvailable`, never folded into it. Meta asks the Peppol network
nothing; the receiver is re-checked when a document is sent.

**Invoicing work adds no permission** (design D10). The view and the wizard are
`invoices:access` and `invoices:create`: whoever builds the invoice sees the hours, the
people and the rates it will state, as every issued PDF shows them to `invoices:access`.
The stamp and the release run under `invoices:issue`, through the invoiced-work
holders, and **not** under the source modules' own rights — neither Projects' financial
rights, nor Time's approver, nor Expenses' project rights is asked: an issue marks
invoiced whatever its draft holds, and a credit note releases whatever it returns. The
app shows the card "Uninvoiced work" on a customer's Invoices tab and the project's
Invoicing tab only to a caller holding both `invoices:access` and `invoices:create`,
with the invoices module mounted, and offers "Invoice the chosen work" on a customer's
card only for an active customer, as it offers "New invoice". The source modules' badge
"Invoiced by invoice n" is a link to the invoice only for a caller who may open it —
the invoices module mounted and `invoices:access` — and plain words otherwise.

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
| `PUT /settings` | `invoices:manage` | 400 on the field (both mod-11 checks, IBAN mod-97, BIC, "Only NOK in this phase", the Peppol id, the KID pair, a next number the KID length does not fit, any of the three required-nullable fields absent, `workVatCodes` absent, a work VAT code unknown, or changed to an inactive one, `timesheetDefault` or `timesheetPersonLabel` absent or null, a label other than `initials`, `number` or `name`); 409 `series_locked`, or a stale revision (no code) |
| `GET /settings/access-point` | `invoices:manage` | none: 200 with `hasCredentials: false` when nothing is stored |
| `PUT /settings/access-point` | `invoices:manage` | 400 on `provider`, `legalEntityId` or `apiKey` (blank, too long, or omitted while none is stored); 409 `transmissions_active` on a provider switch; 503 `ehf_unavailable`, a kept key that cannot be opened |
| `DELETE /settings/access-point` | `invoices:manage` | 409 `transmissions_active` |
| `POST /settings/access-point/verify` | `invoices:manage` | 409 `ehf_unavailable`, no credentials; 503 `ehf_unavailable`, a stored key that cannot be opened |
| `GET /settings/reminders` | | |
| `PUT /settings/reminders` | `invoices:manage` | 400 on the field (`revision` too; absent, null but for `inkassolov2026From`, the wrong type, out of its bounds, `regimeReviewedThrough` more than a year ahead); a stale revision (no code) |
| `GET /collection-rates` | | |
| `POST /collection-rates` | `invoices:manage` | 400 on the field (`kind`, `validFrom` not after today, not after the latest printed or sent letter, or off 1 January and 1 July for a half-yearly kind, `value` out of its bounds or past two decimals, `sourceRef`); 409 `collection_rate_exists` |
| `DELETE /collection-rates/{id}` | `invoices:manage` | 404; 409 `collection_rate_in_force` (seeded, in force or past, or used by a printed or sent letter) |
| `GET /customers/{customerId}/reminder-policy` | | |
| `GET /overdue` | | 400 paging; 409 `too_many_overdue` |
| `POST /reminder-runs` | `invoices:payments` | the preview (narrowed by `customerId`, `dueBefore`): 409 `too_many_overdue`; the run: 400 on `items`; 409 `reminders_disabled`, `collection_rates_outdated` (with `kind`, `halfYear`), `collection_regime_unreviewed`, `bank_import_stale` (with `lastBookedOn`) |
| `GET /reminder-runs` | `invoices:payments` | 400 paging |
| `GET /reminder-runs/{id}` | `invoices:payments` | 404 |
| `GET /reminders` | | 400 paging, an unknown `status` or `channel` |
| `GET /reminders/{id}/pdf` | | 404; 409 `reminder_not_sent`; 500 a missing or altered stored object; 503 `storage_unavailable` |
| `POST /reminders/{id}/withdraw` | `invoices:payments` | 400 on `reason`; 404; then under the letter's lock 409 `reminder_not_withdrawable` (sent, withdrawn, or being sent) |
| `POST /reminders/{id}/retry` | `invoices:payments` | 404; then under the letter's lock 409 `reminder_not_failed` |
| `GET /reminder-print-batches` | `invoices:payments` | 400 paging |
| `POST /reminder-print-batches` | `invoices:payments` | 400 on `reminderIds` (none, over 200, one twice, an unknown id) or `postOn` (before today, more than 7 days on); 503 `storage_unavailable`; 409 `reminder_not_awaiting_print` (naming the letter) — all before anything is written |
| `GET /reminder-print-batches/{id}/pdf` | `invoices:payments` | 404 no batch, or none of its letters printed or sent |
| `POST /reminder-print-batches/{id}/posted` | `invoices:payments` | 400 `postedOn` after today; 404; then under the batch's lock 409 `print_batch_closed`, `reminder_posted_early`, `reminder_posted_late` |
| `POST /reminder-print-batches/{id}/reprint` | `invoices:payments` | 404; then under the batch's lock 409 `print_batch_closed` |
| `PUT /customers/{customerId}/reminder-policy` | `invoices:payments` | 400 on `mode` or `note`; 404 the customer has no document here, or is anonymised |
| `GET /vat-codes` | | |
| `POST /vat-codes` | `invoices:manage` | 400 on the field, a duplicate code on `code` |
| `PUT /vat-codes/{id}` | `invoices:manage` | 404; 400; 409 `vat_code_in_use`, a stale revision |
| `POST /vat-codes/{id}/rates` | `invoices:manage` | 404; 400 on `ratePercent` or `validFrom`; 409 `rate_change_in_past` |
| `DELETE /vat-codes/{id}/rates/{rateId}` | `invoices:manage` | 404; 409 `rate_period_not_latest`, `rate_period_last`, `rate_period_in_use` |
| `GET /work` | `invoices:create` | 400 neither or both of `customerId` and `projectId`; 409 `work_unavailable`, `projects_unavailable`; 404 an unknown project |
| `POST /from-work` | `invoices:create` | 400 on the field (`customerId`, `sources`, `sources[i]`, `grouping`, `revision`, `deliveryTo`, `invoiceId`, `vatCodes.<kind>`, `note`, `lines` — the document total too large); 404 the target; 409 `invoice_issued`, `too_many_sources`, the customer gates, `projects_unavailable`, `source_not_for_customer`, `source_not_selectable`, `source_not_invoiceable`, `source_changed`, `mixed_currency`, `currency_not_nok`, `too_many_lines` (with `suggestedGrouping`), a stale revision, `source_held_elsewhere` (with `heldBy`) |
| `GET /` | | 400 paging, status, kind, state, `from` after `to` (`projectId` filters on the document's project) |
| `POST /` | `invoices:create` | 400 on the field (`sources` and `refreshSources` included; a deduction line's `quantity`, `unitPrice`, `discountPercent`, `deductsInvoiceId`, `vatCodeId`, `sources`); 409 the customer gates, `deduction_duplicated` (with `linePosition`) |
| `GET /{id}` | | 404 (an invoice draft's work is judged fresh — `source_changed`, `source_not_invoiceable` — only for a caller holding `invoices:create`) |
| `PUT /{id}` | `invoices:create` | The body's phase 3 fields: each line's `sources` (`[{kind, id}]`, required on a draft that holds work) and `deductsInvoiceId`, `refreshSources` and `timesheet`. 404; 400 (a line's `sources` against the work the draft holds, at most 5 000; a deduction line's fields as on `POST /`; on a credit note a line's `deductsInvoiceId`, or a quantity of the other sign than the line it credits; `timesheet: true` on a credit-note draft); 409 `invoice_issued`, the customer gates, a stale revision, `invoice_changed` (`refreshSources`), `deduction_duplicated` (with `linePosition`) |
| `DELETE /{id}` | `invoices:create` | 404; 409 `invoice_issued` |
| `POST /{id}/issue` | `invoices:issue` | 400 a body that does not decode (none, or an `issueDate` that is no calendar day); 404; 409 every code under [Issuing](#issuing); 503 `storage_unavailable` |
| `POST /{id}/credit` | `invoices:issue` | 404; 409 `invoice_draft`, `credit_note_not_creditable`, `invoice_fully_credited` |
| `GET /{id}/pdf` | | 404; 409 `invoice_draft`; 500 a missing or altered stored object, or a render that fails; 503 `storage_unavailable` |
| `GET /{id}/preview.pdf` | `invoices:create` | 404; 409 `invoice_issued` |
| `GET /{id}/deductible` | `invoices:create` | 404; 409 `invoice_issued`, `credit_note_deducts_nothing` |
| `POST /{id}/payments` | `invoices:payments` | 400 a body that does not decode; 404; 409 `credit_note_no_payments`, `invoice_draft`; 400 on the field; 409 `invoice_settled`, `payment_exceeds_open` (with `openAmount`) |
| `POST /{id}/payments/{paymentId}/remove` | `invoices:payments` | 400 on `reason`; 404 the document, or a payment not its own; 409 `payment_removed` |
| `POST /{id}/charge-payments` | `invoices:payments` | 400 a body that does not decode; 404; 409 `credit_note_no_payments`, `invoice_draft`; 400 on the field; 409 `no_charges_outstanding`, `charge_payment_exceeds_outstanding` (with `chargesOutstanding`) |
| `POST /{id}/charge-payments/{chargePaymentId}/remove` | `invoices:payments` | 400 on `reason`; 404 the document, or a charge payment not its own; 409 `payment_removed` |
| `POST /{id}/charges/waive` | `invoices:payments` | 400 on `waivers`, `reason` or `note`; 404; 409 `credit_note_no_reminders`, `invoice_draft`; 404 a letter not the document's; 409 `charge_not_claimed` |
| `POST /{id}/manual-deliveries` | `invoices:issue` | 404; 409 `credit_note_no_reminders`, `invoice_draft`; 400 on `kind`, `deliveredOn` or `note` |
| `POST /{id}/manual-deliveries/{deliveryId}/remove` | `invoices:issue` | 400 on `reason`; 404 the document, or a delivery not its own; 409 `delivery_removed`, `delivery_relied_on` |
| `POST /{id}/hold` | `invoices:payments` | 400 on `note`; 404; 409 `credit_note_no_reminders`, `invoice_draft`; then under the lock 409 `invoice_on_hold` |
| `POST /{id}/hold/lift` | `invoices:payments` | 400 on `chargesAllowed` (absent or not a boolean) or `note`; 404; 409 `invoice_not_on_hold` |
| `POST /{id}/collection` | `invoices:payments` | 400 on `handedOn` (missing or after today), `agency`, `agencyReference` or `note`; 404; 409 `credit_note_no_reminders`, `invoice_draft`; 400 on `handedOn` before the issue date; then under the lock 409 `invoice_settled`, `invoice_handed_off`, `invoice_not_delivered` (unless `acknowledgeNotDelivered`) |
| `POST /{id}/collection/withdraw` | `invoices:payments` | 400 on `withdrawnOn` (missing or after today) or `reason`; 404; 409 `invoice_not_handed_off`; 400 on `withdrawnOn` before the hand-off's `handedOn` |
| `POST /{id}/send` | `invoices:issue` | 429 `rate_limited`; 503 `mail_unavailable`; 404; 409 `invoice_draft`, `customer_anonymised`; 400 on `recipient`; 409 `no_invoice_email`; 503 `storage_unavailable`; 500 a directory that fails, a missing or altered stored object, a render that fails, or a sent mail whose row could not be written; 502 `mail_failed` |
| `POST /{id}/send-ehf` | `invoices:issue` | 429 `rate_limited`; 503 `ehf_unavailable`; 404; 409 `invoice_draft`, `customer_anonymised`, `no_peppol_id`, `buyer_reference_missing`, `ehf_already_sent`; 503 `storage_unavailable`; 500 a missing or altered stored PDF, a render that fails or breaks an invariant; 409 `ehf_invalid` (with `rules`); 502 `peppol_lookup_failed`; 409 `peppol_not_receivable` (with `peppolRegistered`, `peppolCanReceive`); 500 a missing or altered reused UBL; 503 `storage_unavailable`; then under the lock 409 `customer_anonymised`, 503 `ehf_unavailable` when the credentials vanished, 409 `ehf_already_sent` |
| `POST /{id}/transmissions/{transmissionId}/cancel` | `invoices:issue` | 404 the document, or a transmission not its own; 409 `transmission_not_cancellable` |
| `POST /{id}/transmissions/{transmissionId}/resolve` | `invoices:issue` | 400 on `outcome` (not `delivered` or `failed`) or `note` (empty, over 500); 404 the document, or a transmission not its own; 409 `transmission_not_resolvable` |
| `GET /{id}/transmissions/{transmissionId}/ubl` | | 404 the document, or a transmission not its own; 500 a missing or altered stored UBL; 503 `storage_unavailable` |
| `POST /bank-files` | `invoices:payments` | 400 on `file` (the part, the format, the file's own rules); 409 `bank_account_unknown`, `bank_file_duplicate` (with `bankFileId`, `uploadedAt`, `uploadedBy`); 503 `storage_unavailable`; then under the accounts' lock 409 `bank_import_format_mismatch`, `bank_file_duplicate` |
| `GET /bank-files` | `invoices:payments` | 400 paging |
| `GET /bank-files/{id}` | `invoices:payments` | 404 |
| `POST /bank-files/{id}/match` | `invoices:payments` | 404 |
| `GET /bank-transactions` | `invoices:payments` | 400 paging, an unknown `status` or `reason`, `from` after `to` |
| `POST /bank-transactions/{id}/apply` | `invoices:payments` | 400 on `allocations`, `allocations[n].invoiceId`, `allocations[n].amount`, `allocations[n].chargesAmount` or `note`; 404; 409 `bank_transaction_not_open`, `bank_transaction_not_applicable`; then under the locks `bank_transaction_reversed`, `allocation_not_an_invoice`, `payment_exceeds_open` (with `invoiceId`, `openAmount`), `charge_payment_exceeds_outstanding` (with `chargesOutstanding`), `paid_before_issue`, `allocation_exceeds_transaction` |
| `POST /bank-transactions/{id}/dismiss` | `invoices:payments` | 400 on `note`; 404; 409 `bank_transaction_not_open`, `bank_transaction_not_applicable` |
| `POST /bank-transactions/{id}/handle-reversal` | `invoices:payments` | 400 on `removePayments`, `removePayments[n].paymentId`, `noPayment` or `note`; 404; 409 `bank_transaction_not_open`, `bank_transaction_not_applicable`, `reversal_payment_required`; then under the locks 404 a payment not its invoice's, 409 `payment_removed` |
| `POST /bank-transactions/{id}/confirm-duplicate` | `invoices:payments` | 400 on `note`; 404; 409 `bank_transaction_not_open`, `bank_transaction_not_applicable` |
| `POST /bank-transactions/{id}/treat-as-distinct` | `invoices:payments` | 404; 409 `bank_transaction_not_applicable`, `bank_transaction_not_open`; then under the lock `bank_transaction_reversed` |
| `POST /bank-transactions/{id}/reopen` | `invoices:payments` | 404; 409 `bank_transaction_not_applicable`, `bank_transaction_applied`, `bank_transaction_reversed` |
| `GET /bank-accounts` | `invoices:payments` | |
| `PUT /bank-accounts/{account}/format` | `invoices:manage` | 400 on `format`; 404 an account never imported |
| `GET /journal` | | 400 `from` or `to` missing or not a calendar date, `from` after `to`, paging |
| `GET /export.csv` | | 400 `from` or `to` missing or not a calendar date, `from` after `to`, more than 5000 rows |
| `GET /collection-export.csv` | `invoices:payments` | 400 neither or both of `handedFrom`/`handedTo` and `invoiceId`, one date alone, `handedFrom` after `handedTo`, more than 500 `invoiceId`s or one that is not an issued invoice, more than 500 rows |
| `GET /stats/attention` | | none: the items for `invoices:payments` are left out for a caller without it |
| `GET /stats/summary` | | 400 `from` after `to` |

## What comes next

- **4C** (next, the phase's second pull request): the payments port and its Vipps
  adapter, pay links and the public pay page, and the quick invoice — paid on site, a
  kontantfaktura ([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-06-invoices-payments-reminders-design.md),
  D13–D17). Attention's item shape takes its four types by adding enum values.
- **5**: energy consumption billing.

Phase 4's first pull request built receiving payments from the bank — OCR giro and
camt.054 files matched on KID, the exception queue — and chasing them: collection
rates, the reminder settings and a customer's policy, the rules engine, charges kept
apart from the principal, reminder runs, the letters by e-mail and on paper, holds and
the hand-off to collection, the overdue list and attention. **Not phase 4** (design
D20), though the earlier roadmap named some of them: overpayment set off against the
next invoice, customer credit balances and refunds as a flow — an overpayment is
refused or queued, a refund due is only shown as a figure and an attention item, and
a refund made outside Vantigo is recorded by removing the registration with a reason —
and rounding off small differences.

Left out of phase 4 on purpose (design D20): an export of payments or charges for the
accountant; bank APIs and direct file delivery; camt.053 and the reconciliation of
movements that are not customers' payments; the Vipps Report API and settlement
reconciliation (a payout is dismissed as `vipps_payout`); MobilePay markets, other
providers, cards, push messages and long-living payments; the creditor's own
betalingsoppfordring (inkassoloven § 10); the § 19 regulation's egeninkasso fees; an
agreed B2B interest rate; interest on fees; the chapter 2 cost caps; letters as EHF or
eFaktura, and SMS; a letter for charges alone; an agency API and an automatic hand-off;
a group-level reminder policy; the B2B 60-day term check; several KID lengths on one
agreement; a holiday calendar of the installation's own; automatic webhook
re-registration; a "paid" stamp or receipt on the PDF; and anything of a kassasystem —
counter sales, receipts, X and Z reports.

Left out of phase 3 on purpose (design D13, D16):

- **The 2028 buyer org-number rule.** From 2028-01-01 bokføringsforskriften § 5-1-2,
  as amended by FOR-2026-09-29-1933, reads "Ved salg til bokføringspliktig kjøper skal kjøpers
  organisasjonsnummer alltid angis", where the issue's `buyer_incomplete` still accepts
  a complete address or an organisation number. The customers directory knows no
  "bokføringspliktig" fact to tell such a buyer apart, so the rule is in the invoices
  backlog.
- **Utlegg.** A cost paid on the customer's behalf and passed on outside the VAT base
  (merverdiavgiftsloven § 4-1 (2) a) is not supported: every re-billed cost is a sale at
  the chosen code, and no line text says "utlegg" ([VAT codes for
  work](#vat-codes-for-work)).
- **Several attachments.** The timesheet is part of the one PDF; a separate timesheet
  file or forwarded receipts would be further EHF attachments, waiting on whether
  Storecove's regeneration keeps even the one (below).
- **Construction's § 8-1-2a** progress rules, ten-year timelists and retention money.
- Also: `PrepaidAmount` and the VAT-free payment request; a line's own invoice period
  and accounting cost, BT-12, BT-18 and BT-128; mixed-currency invoices; a product's or
  billing line's own VAT default; adding work to a credit note; and a permission of its
  own for invoicing work.

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
(the PDF is immutable), timeseries and attention stats (attention came in phase 4), per-currency stats, a public-body
fact on the directory, user display names on payments and deliveries (ids only), and a
purge of anything.
