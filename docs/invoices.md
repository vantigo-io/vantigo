# Invoices

The Invoices module issues the sales document of Norwegian bookkeeping: a draft
becomes a numbered, immutable invoice or credit note, rendered to a PDF that is stored
once and downloaded as stored, and listed in a journal that proves the number series has
no gaps. This is phase 1A of the module
([design](superpowers/specs/2026-09-26-invoices-foundation-design.md),
[research](superpowers/research/2026-09-26-invoices-module.md)). Vantigo stays a
sub-ledger: there is no general ledger and nothing is posted.

> **Phase 1A alone does not meet the e-invoicing duties.** Invoicing the public sector
> has required EHF since 2019 (FOR-2019-04-01-444), and invoicing Norwegian businesses
> requires an e-invoice from **2027-01-01** (Lov 19. juni 2026 nr. 39). This phase
> issues PDFs a person hands over; EHF over Peppol is phase 2. See
> [What comes next](#what-comes-next).

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
| `invoices.settings` | One row: the seller record (legal name, organisation number, VAT registration, Foretaksregisteret, address, bank account, IBAN/BIC, e-mail, footer), the default terms and currency, and `series_start`. |
| `invoices.counters` | The one counter row, `documents`; it exists exactly when something has been issued. |
| `invoices.vat_codes` | The tenant's codes: label, name, SAF-T code, UNCL5305 category, exemption reason, active. |
| `invoices.vat_code_rates` | Each code's rates as dated periods that never overlap (an exclusion constraint). A rate change is a new period, not a new code. |
| `invoices.invoices` | Drafts and issued documents: kind, status, number, customer, delivery, references, notes, the buyer snapshot and the seller snapshot (written at issue), the totals, and the stored PDF's key and SHA-256. |
| `invoices.lines` | Description, quantity (3 decimals), unit, unit price (4), discount (2), VAT code, the computed gross, allowance and net, the credited line on a credit note, and the VAT snapshot written at issue. |
| `invoices.vat_summaries` | An issued document's VAT per (category, rate) with its SAF-T code and reason. |

The seeded codes, each from 2026-01-01: `3` 25 %, `31` 15 %, `32` 11.11 %, `33` 12 %
(all S), `5` Z, `51` AE, `52` G, `6` **E** (unntatt, mval. kap. 3) and `7` **O** (a seller
outside the VAT register) at 0 %.

**Money.** Amounts arrive as JSON numbers and are read as the decimal text they were
written as, never as a binary float. A line's gross is quantity × unit price rounded to
øre, its allowance the discount of that rounded gross, rounded, and its net the
difference — gross less allowance, as EHF expresses a discount. Every rounding is two
decimals, the half away from zero; there is no øre rounding of the total. A line is at
most 999 999 999.99, a document 99 999 999 999.99 gross, and at most 500 lines.

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
settings row and then the VAT code, and `PUT /vat-codes/{id}` only the code — no issue
locks a code — and the merge holder locks the documents it re-points **newest first** before it writes
them — a credit note's issue holds the credit note and then locks its older original,
and an UPDATE alone could lock the original first, a deadlock. This is the module's one
lock invariant, and every multi-row lock inside it keeps to it: **take locks in
descending id**, which is the same rule as "a credit note is always newer — holds a
higher id — than the original it credits", stated twice; any future path that locks
more than one row of `invoices.invoices` at once must keep both true.

The checks, each a 409 that rolls the number back: `seller_incomplete`, `no_lines`,
`delivery_date_missing`, `issue_date_not_allowed` (with `allowedIssueDates`); for an
invoice the customer gates, `buyer_incomplete`, `vat_code_inactive` and
`vat_code_not_valid` (with `linePosition`), `vat_not_registered` (a seller outside the
register issues only O lines), `category_o_not_allowed` (a registered seller issues no O
line), `reverse_charge_needs_org_number` and `vat_codes_ambiguous`; for a credit note
`credit_exceeds_line` (with `linePosition`) and `credit_exceeds_invoice`. Before the
transaction: `invoice_issued`, and 503 `storage_unavailable` when no object store is
configured — an issued number whose PDF could never be stored is not allowed to exist.
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
never fails the issue: the response says `pdfStored: false` and the first download stores
it. Every download streams the stored object, verified against its hash; a document whose
hash is set is never rendered again, and the module never deletes an object. A stored
object that is gone or no longer matches its hash is a 500 logged at error — an operator
problem, never papered over. A first download that cannot reach the store is a 503 to
retry; one that cannot render the document is a 500, since retrying does not mend it. Reproducible bytes are a nice-to-have: catalog sorting and a
fixed modification date are set process-wide and the creation date is the issue instant,
but the bytes may change with a maroto, gofpdf or font upgrade; stored PDFs never do.

`GET /invoices/{id}/preview.pdf` renders a draft on demand with the watermark
"UTKAST — ikke et salgsdokument", no number, today's date, the current settings and,
for an invoice draft, the customer's current profile at today's rates; a credit-note
draft keeps its copied buyer and its original lines' rates. It is never stored. The app
opens it in a new browser tab — opened with the click, before the PDF is fetched, so
the browser's pop-up blocker never sees a `window.open` outside a click — and falls
back to a plain download when the tab could not be opened at all.

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

## Retention and personal data

Sales documentation is kept **five years after the end of the financial year**
(bokføringsloven § 13, <https://lovdata.no/lov/2004-11-19-73/§13>). Nothing is purged in
this phase; a purge is later work. The 2027 wording of § 13 (Lov 2026 nr. 39) was not
read — **unconfirmed**. **The operator's backup of the object store is part of that
retention**: the only storage driver is `fs`, with no WORM, so the PDFs are only as safe
as the volume and its backups ([storage](storage.md)).

The module fills both customer slots ([module boundaries](module-boundaries.md)):

- **Merging customers** (`contracts.CustomerReferenceHolder`) re-points every document of
  the absorbed customer, drafts and issued, reported as `invoices.invoices`. An issued
  document keeps its buyer snapshot — the id is not printed, the snapshot is — and its
  revision.
- **A person's export** (`contracts.CustomerPersonalData`) hands over every issued
  document and every draft, each with its lines, a structured `buyer` — the full
  snapshot: name, type, organisation number, foreign id, GLN, Peppol id, language and
  the address with its region — the `deliveryAddress` when one is set, the references
  and both notes, and, on a credit note, `credits{number, issueDate}` naming what it
  credits. An issued document's internal note is exported too: it is immutable once
  issued, the same as every other column, and export carves out no exception for it.
- **Anonymisation** deletes the person's drafts, invoice and credit-note drafts alike,
  reported as `invoices.drafts` — a draft is not a sales document and has no retention
  basis, so GDPR art. 17 applies — and keeps every issued document, its buyer snapshot
  and its internal note under § 13, reported as `invoices.documents` at 0 — the note is
  immutable once issued, so anonymisation has no more standing to touch it than any
  other write does. `contracts.ErasedData` carries no reason field; this paragraph is
  where the reason is written.

## Permissions

No built-in role holds any of these; Owner has the wildcard.

| Key | Sensitive | What it allows |
| --- | --- | --- |
| `invoices:access` | no | Use the app; read every invoice, credit note, PDF and the journal. |
| `invoices:create` | no | Create, edit and delete drafts; preview a draft. |
| `invoices:issue` | yes | Issue a draft; create a credit-note draft. |
| `invoices:manage` | yes | The seller record, the series start, VAT codes and their rates. |

**Creating a draft in the app also needs `customers:view`**: the directory has no
search, so the buyer picker reads the customers module's own list. The API takes a
customer id and checks nothing more; the app hides "New invoice" without it.

## Endpoints

All under `/api/v1/invoices`, every one behind `invoices:access`.

| Operation | Also needs | Refusals |
| --- | --- | --- |
| `GET /meta` | | |
| `GET /settings` | | |
| `PUT /settings` | `invoices:manage` | 400 on the field (both mod-11 checks, IBAN mod-97, BIC, "Only NOK in this phase"); 409 `series_locked`, or a stale revision (no code) |
| `GET /vat-codes` | | |
| `POST /vat-codes` | `invoices:manage` | 400 on the field, a duplicate code on `code` |
| `PUT /vat-codes/{id}` | `invoices:manage` | 404; 400; 409 `vat_code_in_use`, a stale revision |
| `POST /vat-codes/{id}/rates` | `invoices:manage` | 404; 400 on `ratePercent` or `validFrom`; 409 `rate_change_in_past` |
| `DELETE /vat-codes/{id}/rates/{rateId}` | `invoices:manage` | 404; 409 `rate_period_not_latest`, `rate_period_last`, `rate_period_in_use` |
| `GET /` | | 400 paging, status, kind, `from` after `to` |
| `POST /` | `invoices:create` | 400 on the field; 409 the customer gates |
| `GET /{id}` | | 404 |
| `PUT /{id}` | `invoices:create` | 404; 400; 409 `invoice_issued`, the customer gates, a stale revision |
| `DELETE /{id}` | `invoices:create` | 404; 409 `invoice_issued` |
| `POST /{id}/issue` | `invoices:issue` | 400 a body that does not decode (none, or an `issueDate` that is no calendar day); 404; 409 every code under [Issuing](#issuing); 503 `storage_unavailable` |
| `POST /{id}/credit` | `invoices:issue` | 404; 409 `invoice_draft`, `credit_note_not_creditable`, `invoice_fully_credited` |
| `GET /{id}/pdf` | | 404; 409 `invoice_draft`; 500 a missing or altered stored object, or a render that fails; 503 `storage_unavailable` |
| `GET /{id}/preview.pdf` | `invoices:create` | 404; 409 `invoice_issued` |
| `GET /journal` | | 400 `from` or `to` missing or not a calendar date, `from` after `to`, paging |

## What comes next

- **1B** (next): payments with soft removal and derived states (open, overdue, paid,
  credited), e-mail delivery with the PDF, the accountant's CSV export, the dashboard
  card and stats, the customer page's Invoices tab.
- **2**: EHF over Peppol and KID — what makes B2G and, from 2027, B2B invoicing lawful.
- **3**: hours, expenses and milestones turned into lines, with a write-back contract.
- **4**: payment files and reminders.
- **5**: energy consumption billing.
