# Invoices — payments, delivery and the export — design (Invoices phase 1B)

The second delivery of the Invoices module, on a branch cut from `main` after phase 1A
merged (PR #128, `0e84ab03`), building only on 1A's tables. Phase 1A
(`docs/superpowers/specs/2026-09-26-invoices-foundation-design.md`, "Phase 1B (next
branch)" at its end) recorded the review's rulings for this phase; every one of them is
honoured below, and where this design reads one of them differently it says so.
Research: `docs/superpowers/research/2026-09-26-invoices-module.md` (§2.5 retention,
§5.2 "open amount = gross − credited − paid; paid / partly paid / overdue is derived, not
stored", §6.3 the patterns to take). Module doc: `docs/invoices.md`. This revision
follows the design's critical review (one blocker — a send racing an anonymisation —
and twenty-four findings, each taken or answered in its place).

A document issued in 1A is lawful and can be handed over. What 1A cannot say is whether
it was paid, and it cannot hand it over itself. This delivery is:

- **payments**: a registration of money received against an invoice, bookkeeping
  material kept like the document, removable only by a soft removal with a reason;
- **derived states**: `open`, `partially_paid`, `overdue`, `paid`, `credited` for an
  issued invoice (`draft` and `issued` for the rest) — computed, never stored, from one
  SQL function with a Go mirror pinned to it;
- **e-mail delivery**: the stored PDF sent to the customer's invoice e-mail with the
  seller as Reply-To, logged, synchronous, never queued;
- **the accountant's CSV export** of a period, one row per document and VAT row;
- **stats and the dashboard card**: what is outstanding and overdue now, and what the
  period issued;
- **the customer page's Invoices tab**;
- **the customers + invoices integration test**, both real modules composed;
- one platform change: `mail.Outbound.ReplyTo`;
- the frontend for all of it, and the docs.

Vantigo stays a sub-ledger: a payment here is a registration, not a posting; nothing is
matched to a bank file (phase 4), nothing is sent as EHF (phase 2).

`srv/` below means `apps/server/internal/`. Money on the wire stays as 1A has it: JSON
numbers read as decimal text, two decimals, half away from zero, `math/big.Rat` and SQL
`numeric` inside, and **absent, not null** when a figure does not apply.

## Decisions

### D1 — One new permission, `invoices:payments`; sending is under `invoices:issue`

| Key | Sensitive | What it allows |
| --- | --- | --- |
| `invoices:payments` | yes | Register a payment against an issued invoice, and remove a registration with a reason. |

Delegable, no built-in role (Owner has the wildcard), the grammar
`permission:invoices:access+invoices:payments` as 1A's three. It is sensitive because a
registration changes what the company says it is owed, and a wrong one is corrected only
by a removal that stays on record. Admin catalog entries en + nb in
`apps/host/frontend/src/catalogs/admin.ts`.

**Sending is under `invoices:issue`** (1A's ruling: "puts sending under
`invoices:issue`"). Whoever may create bookkeeping material may hand it over; a reader
with `invoices:access` alone may download it, as before.

`GET /invoices/meta` grows: `capabilities.canRegisterPayments` (`invoices:payments`),
`capabilities.canSend` (`invoices:issue` **and** `mailAvailable`), and the top-level
`mailAvailable` — whether this installation's mail driver is `smtp` (D4). The pages hide
"Send" without it and say why in the settings page's checklist.

### D2 — Payments: `invoices.payments`, a registration kept with the document

Migration `00035_invoices_payments_delivery.sql` holds everything in this phase (D2, D3,
D4, D6). No column is named with a word PostgreSQL reserves. The schema tests pin it:
`TestInvoicesPaymentsDelivery_AppliesAndIsIdempotent` (`applyUpDownUp(t, url, 35)`, the
`00033` precedent) pins the three tables, every trigger and its message, the CHECKs and
that `Down` drops `document_state`; and 1A's reserved-word test
(`TestInvoicesSchema_NamesNoColumnWithAReservedWord`) is moved from `migrateTo(…, 34)`
to 35, so it reads the new tables too.

**`invoices.payments`:**

| Column | Rule |
| --- | --- |
| `id bigint` | identity, start 1001 |
| `invoice_id bigint NOT NULL` | `REFERENCES invoices.invoices ON DELETE RESTRICT` |
| `paid_on date NOT NULL` | the day the money arrived; on or after the invoice's `issue_date`, not after today (Oslo) |
| `amount numeric(14,2) NOT NULL` | > 0 (a CHECK), at most two decimals, at most 99 999 999 999.99 (the document bound) |
| `currency char(3) NOT NULL` | the document's — NOK, and only NOK in this phase; copied, never chosen |
| `reference varchar(100) NOT NULL DEFAULT ''` | the bank's or the payer's reference |
| `note varchar(500) NOT NULL DEFAULT ''` | free text |
| `registered_by_user_id uuid NOT NULL`, `registered_at timestamptz NOT NULL` | |
| `removed_at timestamptz`, `removed_by_user_id uuid`, `removal_reason varchar(200)` | the soft removal: all three set together or none (a CHECK), the reason non-empty (a CHECK) |

Indexes: `ix_payments_invoice (invoice_id)` and the partial
`ix_payments_invoice_live (invoice_id) WHERE removed_at IS NULL` — the sums read the live
rows.

**A payment is a record, not a correction.** A registration is kept as long as the
document it is registered against — bokføringsloven § 13, five years after the end of
the financial year, as 1A reads it for the document; **UNCERTAIN** as 1A marks it: the
2027 wording of § 13 is unread, and research §2.5 speaks of the sales documentation,
not of payment registrations by name — so a row is never deleted and never edited: a
trigger `tr_payments_immutable` (`invoices.refuse_payment_change()`, SQLSTATE `P0001`,
"invoices: a payment registration is immutable") refuses every DELETE and every UPDATE
but two writes, which one statement may make together: the removal, which sets
`removed_at`, `removed_by_user_id` and `removal_reason` from NULL to values, once; and
the anonymisation's blanking of `note` to `''` (D6; reading 10) — the 1A
`refuse_issued_document_change` shape (the whole row as jsonb less those three columns
and `note` must be unchanged; the three change only in the removal, from NULL; `note`
changes only to `''`). A mistake is removed with a reason and registered again.

**A payment belongs to an issued invoice.** A second trigger, `tr_payments_parent`
(`invoices.refuse_payment_on_unissued()`), reads the document `FOR SHARE` on INSERT —
the 1A child-row pattern, **not** 1A's `refuse_issued_child_change`, which refuses
everything under an issued document — and refuses a row under a draft or a credit note
("invoices: a payment needs an issued invoice"). The API refuses first, with its own
codes; the trigger is the floor.

**`POST /invoices/{id}/payments`** (`invoices:access+invoices:payments`), body
`{paidOn, amount, reference?, note?}`:

1. 404 when there is no document; 409 `invoice_draft` on a draft; 409
   `credit_note_no_payments` on a credit note. No customer gate: the customer may be
   disabled, archived, merged or anonymised — the money arrived regardless.
2. 400 on the field: `paidOn` before the issue date or after today (Oslo, from
   `Deps.Clock()` through 1A's `businessDay`), `amount` ≤ 0, more than two decimals or
   over the bound, `reference` over 100, `note` over 500.
3. One `withLockedTx`: the invoice `FOR UPDATE` — the only row this operation locks, so
   1A's lock invariant (locks in descending id; document → settings → counter →
   original) holds trivially — then `credited` (the issued credit notes' gross, 1A's
   `CreditedGross`) and `paid` (the live payments' sum, `LivePaymentsSum`) are read
   under that lock, and:
   - open = gross − credited − paid; **open ≤ 0 is 409 `invoice_settled`** (the ruling:
     "payments are refused while the open amount is ≤ 0");
   - **`amount` > open is 409 `payment_exceeds_open`** carrying `openAmount`. An
     overpayment is a customer credit balance, which is phase 4 (1A "Out of scope":
     overpayment, refunds and credit balances); refusing it keeps the open amount
     non-negative by payments alone, so a negative open amount can only come from a
     credit note after payment (D3).
4. INSERT, commit, and answer the document (D3's response) — the caller sees the new
   state and the open amount without a second read.

A credit note's issue (1A) locks the original after the counter; a registration locks
the same row. The two therefore serialise on it, and both caps — the credit's and the
payment's — are evaluated against figures read under the same lock. **A credit after a
payment is allowed** and may push the open amount below zero (D3). Under READ COMMITTED
each statement after the lock sees every commit before it, so the figures are never
stale.

**`POST /invoices/{id}/payments/{paymentId}/remove`** (`invoices:access+invoices:payments`),
body `{reason}`: 400 on `reason` (empty or over 200) before anything is read; then one
`withLockedTx`: the invoice `FOR UPDATE` **first**, the payment read **after** the
lock — so two racing removals of one payment give exactly one 200 and one 409
`payment_removed` — 404 when the payment is not that invoice's, 409 `payment_removed`
when already removed, then the three columns set. It answers the document. A removal
is never undone; the payment is registered again.

The document response carries every registration, removed ones included, so the page
can show them struck through with the reason — the record is the point.

**Double submissions.** A retried `POST` of a partial amount registers twice, and both
pass the open-amount check: that is two registrations of two receipts, which is what
the body says. The dialogs (D10) disable their button while a request is pending; no
idempotency key is added in this phase.

### D3 — Derived states, from one SQL function and its Go mirror

Nothing is stored. For a document, with `credited` the issued credit notes' gross (0 for
a credit note) and `paid` the live payments' sum, judged against `today` (Oslo), the
first match wins:

| State | When |
| --- | --- |
| `draft` | `status = 'draft'` |
| `issued` | an issued credit note |
| `credited` | an issued invoice with `credited > 0 AND credited ≥ gross_total` |
| `paid` | `gross_total − credited − paid ≤ 0` |
| `overdue` | `due_date < today` |
| `partially_paid` | `paid > 0` |
| `open` | otherwise |

The order is the ruling's: a fully credited invoice is `credited` even if it was paid
first (the money is now a refund due, below); a paid invoice is never `overdue`; a late
partial payment is `overdue`, not `partially_paid` — overdue is the fact that matters.
`credited > 0` keeps an invoice of free lines only (gross 0, which 1A allows and can
never credit) out of `credited`: it falls to `paid`, open being 0.

**One SQL function**, `invoices.document_state(kind text, status text, gross numeric,
credited numeric, paid numeric, due_date date, today date) RETURNS text`, `LANGUAGE sql
IMMUTABLE PARALLEL SAFE`, in the migration between goose `StatementBegin/End` and dropped
in `Down` — the `expenses.owes_employee` precedent (`00033`). The list, the stats and
the customer tab filter with it; `today` is always a parameter from `Deps.Clock()` in
Oslo, never `CURRENT_DATE`. **A Go mirror**, `documentState(...)` in
`srv/invoices/state.go`, computes the same for a response — one row at a time, never
over a list, which the SQL answers — and
`TestDocumentState_TheGoMirrorAgreesWithTheSQLFunction` runs every combination of kind,
status, gross 0 or not, credited below / equal / above gross, paid 0 / below / equal /
above open and due before / on / after today through both and fails on any difference
— the owes_employee test's shape, with `modtest.One[string]`.

**The list query.** `ListInvoices` and `CountInvoices` stop being `SELECT *`: the
select list is `sqlc.embed(invoices)` plus two correlated aggregates — `credited`
(`coalesce((SELECT sum(gross_total) FROM invoices.invoices c WHERE c.credits_invoice_id =
i.id AND c.status = 'issued'), 0)`) and `paid` (the live payments' sum) — and
`invoices.document_state(i.kind, i.status, i.gross_total, credited, paid, i.due_date,
@today::date)::text AS state` (the cast pins sqlc's type; the aggregates are repeated
inside the call or taken from a `LEFT JOIN LATERAL`, the implementer's choice, with the
same text in both queries). The `state` filter is `sqlc.narg(state)::text IS NULL OR
state = …` over that expression; `Count` repeats the predicate with `@today`. The row
type changes from `store.InvoicesInvoice` to the generated row; `list.go` adapts.

The state names on the wire are **snake_case** (`partially_paid`), as every other
multi-word wire value of this codebase is (`credit_note`, `relay_accepted`); the ruling
wrote `partiallyPaid` in prose, and this is read as a spelling, not a decision — on the
record for the user's verdict.

**The response.** Every document answers `state` (required; the description names all
seven values). An issued invoice also answers `paidAmount`, `openAmount` (= gross −
credited − paid, which may be negative), `refundDue` (= −open, present only when open <
0; refunds are phase 4 — this is the figure, not a flow), `payments[]` (D2) and, with D4,
`deliveries[]` and `sendDefaults`. An issued credit note answers `deliveries[]` and
`sendDefaults`. Money stays absent when it does not apply: a draft has no `paidAmount`, a
credit note none.

**The list.** `GET /invoices` gains `state` — one of `open`, `partially_paid`,
`overdue`, `paid`, `credited` — validated in Go like `status` (400 "'state' must be one
of …"), applied in SQL through the function with `@today`; it implies issued invoices
(a draft and a credit note never match it), and combines with the other filters. The
order stays 1A's (drafts first, then number descending): no sort by state. Each item
answers `state` (required) and, on an issued invoice, `openAmount`. The page's filter
chips offer the five states beside the existing status and kind chips.

No OpenAPI `enum` — none is used anywhere in `openapi/*.yaml`; the values are in the
description and checked in Go.

### D4 — E-mail delivery: the stored PDF, sent now, logged once

**The platform change.** `mail.Outbound` (`srv/mail/outbound.go`) gains `ReplyTo
string`; `message()` calls `msg.ReplyTo(out.ReplyTo)` when it is set; `go-mail` writes
the header. `TestSendOutbound_RendersTheFullEnvelope`'s shape proves it: the built
message carries `Reply-To`, and none when unset. Nothing else in the platform changes:
`Deps.Config.Mail`, `Deps.SMTPSend` and `modtest.WithSMTPSend` exist.

**`POST /invoices/{id}/send`** (`invoices:access+invoices:issue`), body
`{recipient?}` — an override of the address, validated as the settings validate the
seller's e-mail (`settings.go`: `net/mail.ParseAddress`, the parsed address equal to the
input, at most 254 characters — so `"Name <a@b>"` is refused), 400 on `recipient`. The
operation carries a **rate limit** in `RouterOptions.Limits` (the identity and communications modules' `limits` maps, keyed by operation id): 60 sends per client per 10 minutes — with an arbitrary override
the endpoint is an authenticated relay through the installation's SMTP, and a limit is
cheap. In order:

1. **503 `mail_unavailable`** when `Config.Mail.Driver` is not `smtp` (the `log` driver
   delivers nothing; an installation without SMTP cannot hand a document over this way).
   Judged first: nothing is read for an installation that cannot send.
2. 404; 409 `invoice_draft`.
3. **409 `customer_anonymised`** when the document's customer is in
   `invoices.erased_customers` (D6): a person who has been anonymised is not written to
   again, override or not.
4. **The recipient**: the override, else the current billing profile's `InvoiceEmail`
   — a directory read, outside any lock, through `contractscalls.go`; a credit note
   reads the profile too (it goes to the same buyer, today's address, not the
   snapshot's — the person's mail changes, the document does not). None is **409
   `no_invoice_email`**.
5. **The PDF**: `loadStoredPDF(ctx, q, inv) ([]byte, problem)` — extracted from
   `GetInvoicesByIdPdf`, which keeps calling it — stores once when the row has no hash
   and otherwise reads the object and verifies it against the hash, so a send never
   attaches bytes the store does not hold: 503 `storage_unavailable`, 500 on a missing
   or altered object or a render failure, as the download answers.
6. **The envelope** (`mail.Outbound`): From `Config.Mail.From` with `DisplayName` the
   document's **seller snapshot's** legal name (what the PDF says); **Reply-To the
   current settings' `email`** when set (replies should reach today's mailbox) and
   absent when the settings have none; To the recipient; `MessageID` a fresh
   `<uuid>@vantigo.invalid` (communications' domain); the PDF attached under the
   download's own localised filename (`faktura-1001.pdf`, `invoice-1001.pdf`,
   `kreditnota-1002.pdf`, `credit-note-1002.pdf`); plain text only, in the document's
   `buyer_language`, the exact texts below. No HTML, no logo, no template system — the
   wording lives in Go beside the PDF's labels (`pdf.go`'s `labels` map is the
   precedent).
7. **The send**, through a `contractscalls.go` accessor over `Deps.SMTPSend` (nil →
   `mail.SendOutbound`; `noteContractCall("SMTPSend")`, so the harness's hook proves no
   send ever happens inside `withLockedTx`) with `Config.Mail`, under
   **`context.WithoutCancel(ctx)` and a 30-second timeout** (communications' send
   timeout): a browser that disconnects mid-send must not abort a transfer the server
   may already have accepted, nor the row that is its evidence. **A failure is 502
   `mail_failed` and records nothing** — the ruling. The error is logged at warn with
   the document id, never put on the wire. A timeout that strikes after the server has
   accepted the data can mean the mail went with no row; `docs/invoices.md` says so.
8. **The log**: one row in `invoices.deliveries`, written on an uncancellable context
   of its own with its **own short timeout** (five seconds), never the send's remaining
   budget — a send the server accepted at the 29th second must still be logged; the five
   seconds include the insert trigger's `FOR SHARE` wait behind an erase. After it, the
   response is rendered on an uncancellable context too, bounded by five seconds of its
   own, so a caller that disconnected gets no error-level log for a send that succeeded. **The blanking of a recipient whose customer was erased meanwhile is the
   insert trigger's job, not the statement's** (`tr_deliveries_parent`, below): an
   anonymisation running between step 4 and here — committed, or still holding the
   customer's documents — must not leave the person's address in a row the erase has
   already run past, and only a check made *after* the trigger's lock wait sees the
   erase's marker (an `INSERT … SELECT CASE WHEN EXISTS (marker)` would read a
   snapshot taken before the wait and leak). A row that fails to write after a
   successful send is logged at error with the document id only — never the
   recipient, which may be a person's whom an erase is anonymising at that moment —
   the mail went; the operator is told.
9. The document is answered, its `deliveries[]` now holding the row.

Sending twice is allowed and logged twice: a re-send is a legitimate act. No
suppression list is read — `communications.suppressions` is another module's table
(`docs/module-boundaries.md` rule 4), and a customer whose invoice address bounces is a
problem the person sending will hear about.

**`invoices.deliveries`:**

| Column | Rule |
| --- | --- |
| `id bigint` | identity, start 1001 |
| `invoice_id bigint NOT NULL` | `REFERENCES invoices.invoices ON DELETE RESTRICT` |
| `recipient varchar(254) NOT NULL` | the address it went to; **`''` once anonymised** (D6). Named `recipient`, never `to` (reserved) |
| `subject varchar(300) NOT NULL` | as sent (a 200-character seller name plus the prefix fits) |
| `message_id varchar(200) NOT NULL` | the Message-ID, bare |
| `pdf_sha256 char(64) NOT NULL` | which bytes were attached |
| `sent_at timestamptz NOT NULL`, `sent_by_user_id uuid NOT NULL` | |

Two triggers: `tr_deliveries_immutable` refuses DELETE and any UPDATE but `recipient`
to `''` — the one write anonymisation makes; `tr_deliveries_parent`
(`invoices.guard_delivery_insert()`) reads the document's `status` and `customer_id`
`FOR SHARE` on INSERT, refuses a draft's ("invoices: a delivery needs an issued
document"), and **then** — in a statement after the lock wait, which under READ
COMMITTED sees whatever committed while it waited — sets `NEW.recipient := ''` when
`invoices.erased_customers` holds that `customer_id`. The payments' parent trigger's
shape plus the one check that closes the race (D6): it reads the document's *current*
customer, so a merge in between is covered too.

**The 429.** The limiter answers as it does everywhere (`ratelimit.Reject`):
`application/json` `{"error": {"code": "rate_limited", "message": …}}` with
`Retry-After`, declared as identity declares it (`common.yaml`'s `AuthErrorResponse`),
never as ProblemDetails.

**The texts.** `{number}`, `{seller}`, `{amount}` (the PDF's own money format, `NOK
15 045,00` / `NOK 15,045.00`), `{due}` (the PDF's date format), `{account}` (the
snapshot's bank account as the PDF prints it), `{iban}`, `{bic}`, `{original}` (a
credit note's original number), `{open}` (the open amount at the send, D3).

The **payment paragraph** of an invoice depends on the open amount at the send, so a
re-send never asks for money that is not owed: *in full* when open = gross; *partly*
when 0 < open < gross; *nothing* when open ≤ 0 (paid or credited). The English text
names the IBAN and BIC when the snapshot has them — an `en` buyer is usually abroad
and cannot pay a domestic account — and the domestic account otherwise.

Invoice, nb — subject `Faktura {number} fra {seller}`:

```
Hei,

Vedlagt følger faktura {number} fra {seller} på {amount}, med forfall {due}.
<in full>  Beløpet betales til kontonummer {account}. Merk betalingen med fakturanummer {number}.
<partly>   Utestående beløp er {open}, som betales til kontonummer {account}. Merk betalingen med fakturanummer {number}.
<nothing>  Fakturaen er gjort opp. Det er ingenting å betale.

Med vennlig hilsen
{seller}
```

Invoice, en — subject `Invoice {number} from {seller}`:

```
Hello,

Please find attached invoice {number} from {seller} for {amount}, due {due}.
<in full>  Please pay to IBAN {iban} (BIC {bic}) | to account {account}, quoting invoice number {number}.
<partly>   The outstanding amount is {open}; please pay it to IBAN {iban} (BIC {bic}) | to account {account}, quoting invoice number {number}.
<nothing>  The invoice has been settled. Nothing is due.

Kind regards
{seller}
```

Credit note, nb — subject `Kreditnota {number} fra {seller}`:

```
Hei,

Vedlagt følger kreditnota {number} fra {seller} på {amount}, som krediterer faktura {original}.

Med vennlig hilsen
{seller}
```

Credit note, en — subject `Credit note {number} from {seller}`:

```
Hello,

Please find attached credit note {number} from {seller} for {amount}, crediting invoice {original}.

Kind regards
{seller}
```

A seller without a bank account cannot have issued (1A's `seller_incomplete`), so the
account is always there; "(BIC {bic})" is printed only with a BIC.

**Warning loudly.** The ruling: "warn loudly when the profile says `ehf` or the buyer is
a public body". The directory carries `InvoiceDelivery`; it carries **no public-body
fact** — "Public sector" in `docs/customers.md` is a group an installation may name, a
vocabulary word, not a fact a module can read. What the snapshot does carry is the
buyer's **organisation number**, which every public body and every Norwegian business
has, and the B2B duty makes that the warning that matters: from **2027-01-01** (Lov 19.
juni 2026 nr. 39) a PDF by e-mail to a Norwegian business is not a lawful e-invoice.
So the warnings, on the document's `sendDefaults.warnings`, on the send's response
`warnings`, and in the Send dialog:

- `delivery_preference_ehf` — the current profile's `InvoiceDelivery` is `ehf`: a red
  alert that cannot be missed ("This customer expects EHF. An e-mailed PDF does not
  meet the e-invoicing duty; phase 2 adds EHF."); `efaktura` and `paper` give a plain
  note (`delivery_preference_other`).
- `buyer_norwegian_business` — the snapshot has `buyer_organisation_number` and today
  (Oslo, the server's clock) is before 2027-01-01: a plain note ("From 1 January 2027
  Norwegian businesses must receive an e-invoice; this is a PDF.").
- `buyer_norwegian_business_required` — the same buyer from **2027-01-01**: a red alert
  that cannot be missed. The server judges the date, never the browser: the app has
  neither the server's clock nor Oslo's day, and a code is what a fixed-clock test can
  pin. This recovers the public-body half of the ruling without a contract fact and
  says the one thing the person sending needs to know. The send is never refused for
  any warning.

`sendDefaults{recipient?, preference?, warnings[]}` is on an issued document's response
**only for a caller with `canSend`** (`invoices:issue` and mail available): the
customer's invoice e-mail sits behind `customers:view` in its own module, and
`invoices:access` alone should not widen that. **For the same reason a delivery's
`recipient` is on `deliveries[]` only for a caller with `invoices:issue`**; a reader
sees when each send happened and its subject, not the address. It is computed by **`GET /invoices/{id}`
and the send's own response only** — not by a payment's, a removal's, an issue's or a
credit's — so no write adds a directory call to its answer; the app invalidates the
document after those writes rather than setting their responses into the cache. It is
the one new directory read on an issued document (1A read the profile for drafts
only); it is **best effort** — a directory error leaves `sendDefaults` absent and logs
at warn, never a 500 on a read of bookkeeping material. The send's warnings live in
`sendDefaults.warnings`; the document's own `warnings` (`issued_late`, …) keep their
meaning and are never mixed with them.

### D5 — The accountant's CSV export

**`GET /invoices/export.csv?from&to`** (`invoices:access`): `from` and `to` required
calendar dates, `from ≤ to` (400 on either), the issued documents whose issue date is in
the range — the journal's own selection — in number order, **one row per (document ×
VAT summary row)**, in each document the rows by category then rate. A credit note's
amounts are **negative** in every amount column (stored positive, signed on output, the
journal's rule).

The byte format is the expenses payroll file's (`docs/expenses.md`, "The payroll CSV"),
duplicated into this module as customers did (depguard forbids sharing the file): UTF-8
with a BOM, `;`, decimal comma, `YYYY-MM-DD`, CRLF after every row the last included,
RFC 4180 quoting with `;` as the separator, and the formula-injection guard — **on the
text columns only** (`Kind`, `Delivery`, `Customer number`, `Buyer`, `Buyer org no`,
`Currency`, `SAF-T code`, `Credits number`), never on an amount or a rate: the
expenses writer guards every cell because none of its amounts is negative, and a
guarded `-1234,50` is text in a spreadsheet, not a number. Amounts are the stored
`numeric` values as text, never a float.

Columns, fixed and English (the ruling's), in this order:

```
Number;Kind;Issue date;Delivery;Due;Customer number;Buyer;Buyer org no;Currency;SAF-T code;Rate;Base;VAT;Base NOK;VAT NOK;Credits number
```

- `Kind`: `invoice` or `credit_note`. `Delivery`: the date, or the period as
  `YYYY-MM-DD/YYYY-MM-DD` (ISO 8601's interval notation), empty when none. `Due`: the
  invoice's due date, empty on a credit note.
- `Customer number`: the snapshot's; `Buyer`: the snapshot's name; `Buyer org no`: the
  snapshot's organisation number, empty for a person or a foreign buyer.
- `SAF-T code`, `Rate`: the VAT row's; `Base`, `VAT`: the row's taxable amount and VAT
  in the document's currency; `Base NOK`: base × the document's exchange rate (1 in this
  phase), rounded to øre; `VAT NOK`: the stored `vat_amount_nok`.
- `Credits number`: a credit note's original's number, empty on an invoice.

Over **5000 rows a 400** "Too many rows to export — narrow the period." — counted before
anything is written, never truncated; the cap is the customers export's. The response is
`text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="invoices-<from>-<to>.csv"`,
`Cache-Control: private, no-store`, set by the customers `csvDownload` shape (the
generated response hard-codes a bare `text/csv`). `contracttest` records and validates
`text/*` bodies, so the export is covered like any operation. The byte-exact test's
file holds an invoice with two VAT rows, a credit note and a buyer named `=cmd`.

A button "Export CSV" on the **journal page**, which already holds the period, fetching
through the customers `downloadFile` pattern so a 400 is a notification in the reader's
language, not a problem document opened in the browser.

### D6 — The two customer slots, extended, and the erased-customer marker

- **Merge** (`CustomerReferenceHolder`): unchanged — payments and deliveries hang off
  the document by id and never carry a customer id. Reported as before.
- **Export** (`ExportCustomerData`): each document's section gains `payments` (every
  registration: `paidOn`, `amount`, `reference`, `note`, `registeredAt`, and the removal
  when there is one) and `deliveries` (`recipient`, `sentAt`, `subject`). A draft has
  neither.
- **Erase** (`EraseCustomerData`), inside the customers transaction, in this order:
  1. **lock the customer's documents `FOR UPDATE`, newest first** (`ORDER BY id DESC`,
     1A's lock invariant; the merge holder's statement), so a delivery insert — whose
     parent trigger takes the document `FOR SHARE` — waits for the erase;
  2. write the marker `invoices.erased_customers (customer_id integer PRIMARY KEY,
     erased_at timestamptz NOT NULL)`, `ON CONFLICT DO NOTHING`;
  3. blank every delivery's `recipient` (reported as `invoices.deliveries`, the count
     blanked), then blank the `note` of every payment of the person's documents, live
     and removed (reported as `invoices.payments`, the count of notes blanked — 0 when
     none had one), delete the drafts (`invoices.drafts`) and keep the documents
     (`invoices.documents`, 0).

  Payments are kept because they are bookkeeping material kept with the document, not
  because they hold nothing personal — their date, amount and the bank's `reference`
  (which often names the payer) stay. Their `note` is staff free text about the person
  that no retention rule needs, so it is blanked, as a delivery's address is (reading
  10); the export is unchanged and carries the note while there is one. Deliveries are
  kept with the address gone: they are the record of when the claim was handed to the
  mail server. The reporting order is `invoices.drafts`,
  `invoices.documents`, `invoices.payments`, `invoices.deliveries`; the anonymisation
  table in `docs/customers.md` gains the two rows. Run twice, it finds nothing and
  reports zeros (the marker is already there).

**The marker closes the race** the review found. A send reads the recipient before it
sends and writes its row after; without a marker an erase committing in between would
blank the rows that existed and the send would then add one holding the person's
address, for good (the delivery trigger allows no second blanking by anyone but the
worker, which never returns). With it: a send after the erase is refused
`customer_anonymised` (D4 step 3); a send whose row is written while the erase holds
the documents waits on the trigger's `FOR SHARE` and, once the erase commits, the
trigger's check sees the marker and writes `''` (D4); a send whose row is written
after the erase commits is blanked by the same check; a send whose row is written
before is blanked by the erase. The check is in the trigger because only a statement
run after the lock wait sees the marker — the inserting statement's own snapshot was
taken before it. The marker is module-private, read by this module only, and never
removed — anonymisation is never undone.

### D7 — Stats, and the dashboard card

**`GET /invoices/stats/summary?from&to`** (`invoices:access`), the host dashboard's own
shape (`buildStatsUrl(module, "summary", range)`, date-time `from`/`to`,
`apicommon.NormalizePeriod`, default 30 days, 400 `application/problem+json` on an
invalid period), in the envelope every module's summary uses:

```
{
  from, to,                                 // the normalised period, as every summary echoes it
  outstandingAmount, outstandingCount,      // now: issued invoices with open > 0; credit notes excluded
  overdueAmount, overdueCount,              // now: those with due_date < today
  issuedCount, issuedGrossTotal,            // invoices issued in the period
  issuedGrossTotalDelta,                    // against the previous period of the same length
  creditedCount, creditedGrossTotal,        // credit notes issued in the period
  paidAmount, paidCount                     // live payments with paid_on in the period
}
```

**The period's dates.** `NormalizePeriod` answers half-open instants
`[periodFrom, periodTo)`; this module's facts are calendar dates in Oslo (`issue_date`,
`paid_on`). The rule, computed in Go through `businessDay`: `fromDay` is the Oslo day
`periodFrom` falls in; `toDayExclusive` is the Oslo day of the last instant *inside*
the period (`periodTo − 1ns`) **plus one day** — so a period ending "now" or at the end
of today includes today, which a bare `< toDay` would drop from every dashboard
preset. **The previous period is counted in days, not in duration:** it is the same
number of Oslo days ending at `fromDay` — `[fromDay − n days, fromDay)` with `n =
toDayExclusive − fromDay` — never `businessDay(previousFrom)`: `NormalizePeriod`'s
`previousFrom` is an absolute duration, which across a daylight-saving change or for a
default period that starts mid-day is a day off the current period's own length, and
"the period of the same length just before" is what the delta compares against. A
document is in the period when `issue_date >= fromDay AND issue_date < toDayExclusive`,
a payment when `paid_on` is. The test plants a document on the first day, on the last
day, on the day before the first, on the previous period's first day, and runs a
period that contains the October change (2026-10-25) to prove both periods hold the
same number of days. All NOK (only NOK in this phase). "Now" is `today` in Oslo from
`Deps.Clock()`.

No timeseries and no attention list in this phase: the dashboard queries them per
module only where a module is listed, and Invoices adds no `metrics` entry — on the
record, Out of scope.

**The dashboard card** (`apps/host/frontend/src/routes/dashboard.tsx`): an `Invoices`
entry in `moduleCards` (`module: "invoices"`, `requiredPermissions: ["invoices:access"]`,
path `/invoices`, the `IconFileInvoice` icon), the summary query beside the others, the
hand-written `InvoicesSummary` wire type, and **one `KpiCard`** — the registry draws
one per module: the value is the outstanding amount, the hint "N overdue (amount)" only
when non-zero (the `awaitingApprovalHint` pattern), the delta `issuedGrossTotalDelta`
against the previous period's issued gross. No colour: `KpiCard` has no tone prop and
this phase does not add one. No KPI strip on the Invoices list page — the review called
it scope beyond the roadmap, and it is dropped.

### D8 — The customer page's Invoices tab

Host-owned composition, the projects tab's shape, in
`apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`:

- `CustomerDetailView` gains `"invoices"`, the `labelKey` union
  `"customer.invoicesTab"`, the `to` union `"/customers/$customerId/invoices"`;
- `customerDetailTabs` gains `{ value: "invoices", labelKey: "customer.invoicesTab",
  icon: IconFileInvoice, to: "/customers/$customerId/invoices", module: "invoices",
  requiredPermissions: ["invoices:access"] }`;
- the `activeTab` ternary gains the `/customers/$customerId/invoices` route id — without
  it the tab would never highlight and the row would silently say "Overview";
- the route file `$customerId.invoices.tsx` and `-customer-invoices-tab.tsx`, guarding
  on the enabled module (`ModuleNotEnabledPage`) and rendering `CustomerInvoicesPanel`
  from `@vantigo/invoices-ui` with `canCreate` = `invoices:create && customers:view &&
  !isReadOnlyCustomer(customer) && customer.status === "active"` — a merged-away,
  anonymised, archived or disabled customer gets no "New invoice", since the server
  would refuse every one of them (1A's gates);
- `customer.invoicesTab` en + nb in `catalogs/customer.ts`; `customer-detail-tabs.test.ts`
  gains the gating cases.

The panel is the list filtered by `customerId` (the existing query) with the state badge,
the open amount and the same paging, and "New invoice" creating the draft for that
customer and navigating to it.

### D9 — The customers + invoices integration test

`srv/integration/invoices_test.go`, both modules real: `modInvoices` is added to the
harness's names and to the merged recorder (`invoices.yaml` merged in), and a
`newInstallationWith(t, opts, names...)` variant takes the options this file needs — an
object store (`storage.NewFS` over `t.TempDir()`), `modtest.WithSMTPSend` recording the
envelope, and `WithEnv` for `MAIL_DRIVER=smtp`, `SMTP_HOST=smtp.example.invalid` and
`SMTP_FROM=faktura@example.invalid` (`config.Load` refuses the `smtp` driver without a
host and a from). The scenarios:

1. a customer created through the customers API with a billing profile (invoice
   e-mail, 30 days, a buyer reference) → a draft takes the profile's terms and reference
   → issued → the buyer snapshot is the customer's own data;
2. a disabled customer is refused a new draft (`customer_blocked`) and a credit note of
   its issued invoice is still created and issued;
3. a merge through the customers API re-points the documents, and `customer.merged`'s
   `moved` names `invoices.invoices`;
4. a send reaches the profile's invoice e-mail with Reply-To the seller's; the delivery
   is on the document;
5. a person's export through the customers API has the invoices section with the
   document, its payment (with a note) and its delivery; the anonymisation worker run
   once deletes the drafts, keeps the document, blanks the delivery's recipient and the
   payment's note (`invoices.payments` 1), and
   `customer.anonymised` lists `invoices.drafts`, `invoices.documents`,
   `invoices.payments` and `invoices.deliveries`; a send afterwards is
   `customer_anonymised`.

The invoices package's own tests keep their fake directory; this file is where the
two modules' understanding of the contract is proven to be one.

### D10 — The frontend

- **List**: the state chips (D3); a state badge per row — grey draft, blue open, yellow
  partially paid, red overdue, green paid, grey credited — and the open amount column
  on issued invoices.
- **Invoice page, issued invoice**: the state badge in the header beside the number;
  the totals card adds "Paid", "Open" and, when due, "Refund due"; a **Payments card**
  — date, amount, reference, note, registered when; removed rows struck through with
  their reason; "Register payment" (`canRegisterPayments`, hidden when the state is
  `paid` or `credited`) opening a modal with today prefilled, the amount prefilled with
  the open amount, reference and note; "Remove" per live row opening a reason modal;
  every refusal in the reader's language (`invoice_settled`, `payment_exceeds_open`
  with the open amount, `payment_removed`). Every modal's button is disabled while its
  request is pending.
- **Invoice page, issued document**: "Send" (`canSend`) opening the Send dialog —
  recipient prefilled from `sendDefaults`, editable; the alerts of D4 (EHF red; the
  Norwegian-business note, red from 2027; the other-preference note); for a settled or
  partly paid invoice the dialog says what the mail will say about payment; "Send" →
  the notification "Sent to <recipient>"; a **Deliveries card** listing each send
  (when, to whom — "(anonymised)" for a blank recipient, and no address column at all for a reader without `invoices:issue`); `no_invoice_email`,
  `customer_anonymised`, `mail_unavailable`, `mail_failed` and the 429 in the reader's
  language.
- **Journal**: "Export CSV" (D5).
- **Settings**: the checklist gains "Mail is configured (SMTP)" from `meta.mailAvailable`
  — informative, not a gate on issuing.
- **Host**: the dashboard card (D7), the customer tab (D8), the admin catalog entry (D1).
- en + nb throughout; `translations:check` passes.

### D11 — Docs

- `docs/invoices.md`: a "Payments and the state of an invoice" section (the table of
  states and the order, open amount, refund due, soft removal and why a row never
  leaves, the lock the registration and the credit issue share, the retention marked
  UNCERTAIN as 1A marks it); "Sending a document" (the recipient rule, Reply-To, the
  texts and the payment paragraph's three forms, the codes, the rate limit, the log, the
  timeout caveat, re-sending, the four warnings, the dropped public-body half and what
  replaces it, the anonymised refusal); "The CSV export" (the columns, the byte format,
  which columns are guarded); "Stats" (the period-to-days rule); the endpoints table
  and the permissions table extended; the anonymisation paragraph gaining payments and
  deliveries and the marker; "What comes next" moved to phase 2.
- `docs/module-boundaries.md`: the platform `mail.Outbound.ReplyTo` change; the
  customer tab as host-owned composition.
- `docs/customers.md`: the anonymisation table's invoices row gains payments (kept,
  their notes blanked) and deliveries (recipient blanked).
- `docs/customers-authentication.md` (the SMTP section): invoices now sends through
  the same `SMTP_*` configuration.
- `ROADMAP.md`: 1B done, phase 2 next. `deploy/compose/README.md` "Upgrading": the new
  permission, and that sending needs `SMTP_*`.

## Readings on the record

For the user's verdict, as 1A's plan listed its readings:

1. `partially_paid`, not `partiallyPaid` (D3).
2. An overpayment is refused (`payment_exceeds_open`), not held as a credit balance (D2).
3. `paid_on ≥ issue_date` is the ruling's; a receipt before the issue (cash on delivery,
   a Vipps payment) cannot be registered until the invoice exists, and then with the
   issue date at the earliest — a-konto and prepayment are phase 4's.
4. The public-body warning is replaced by the organisation-number warning, red from
   2027-01-01 (D4).
5. `sendDefaults` only for a caller who may send (D4).
6. The cover mail's payment paragraph follows the open amount (D4).
7. A send to an anonymised customer is refused outright (D4, D6).
8. One dashboard KPI, no list-page strip, no timeseries (D7).
9. The erase keeps the payments (D6) — their date, amount and the bank's reference;
   `invoices.payments` reports the notes reading 10 blanks, not a zero.
10. A person's payment `note` is blanked on erase, like a delivery's recipient (D2, D6):
    the trigger allows `note` to `''` besides the removal, and `invoices.payments`
    reports the count blanked.

## Out of scope

EHF/Peppol and KID (phase 2); hours, expenses and milestones as lines (phase 3);
overpayment, customer credit balances, refunds as a flow, payment files, reminders and
late interest (phase 4); energy billing (phase 5). Also: editing a payment (remove and
register again); a payment in another currency than the document's; a payment against a
credit note; allocating one payment across several invoices; an idempotency key; HTML
mail, a logo, an editable template or a personal message in the mail; sending through
an outbox or a worker (a send is synchronous and its failure is the caller's to see);
honouring `communications.suppressions`; bulk send; a "paid" stamp on the PDF (the PDF
is immutable); attention and timeseries stats; a tone prop on `KpiCard`; per-currency
stats; a public-body fact on the directory; user display names on payments and
deliveries (ids only); a purge of anything.

## Testing

Through 1A's harness (`modtest`, the fake directory, the fake object store, the fixed
Oslo clock), plus `modtest.WithSMTPSend` recording envelopes and `WithEnv` for
`MAIL_DRIVER=smtp`, `SMTP_HOST` and `SMTP_FROM` in the send tests.

- **The schema.** `TestInvoicesPaymentsDelivery_AppliesAndIsIdempotent` (up, down, up;
  the three tables, four triggers and their messages, the CHECKs, `document_state`
  present after up and gone after down); the reserved-word test at migration 35.
- **Payments.** Register against an issued invoice; 404; `invoice_draft`;
  `credit_note_no_payments`; every 400 (`paidOn` before issue, after today, `amount`
  0, negative, three decimals, over the bound; `reference`, `note` lengths);
  `invoice_settled` once paid; `payment_exceeds_open` with `openAmount`; a payment
  equal to the open amount is accepted and the state is `paid`; removal with a reason
  reopens it; `payment_removed`; 400 on an empty reason; direct SQL cannot update or
  delete a payment, can set the removal once, cannot insert one under a draft or a
  credit note; **the locks**: a registration of 400 racing a credit-note issue of 300
  against a 1000 invoice — both commit and open is exactly 300; two racing
  registrations of the whole open amount — exactly one succeeds; two racing removals of
  one payment — one 200, one `payment_removed`; a registration for an anonymised
  customer's invoice succeeds; `invoices:payments` on both writes.
- **States.** The mirror test over every combination, the gross-0 invoice included; a
  credited invoice that was paid is `credited` with `refundDue`; a late partial payment
  is `overdue`; the day it falls due it is not overdue, the day after it is (the fixed
  clock); the list `state` filter for each value, combined with `kind` and
  `customerId`; an unknown state is a 400; a draft and a credit note never match a state
  filter; the list's `openAmount`.
- **Sending.** `mail_unavailable` first on the `log` driver; `invoice_draft`;
  `customer_anonymised`; `no_invoice_email` for a profile without one; the override
  wins and is validated (`"Name <a@b>"` refused); the envelope (From, DisplayName the
  snapshot's seller, Reply-To the current settings' email and absent without one, To,
  the subject and body in nb and en, each of the three payment paragraphs, the IBAN
  form, the attachment's name, type and bytes equal to the stored PDF, the Message-ID);
  a never-stored PDF is stored first and the row's hash is the attached bytes';
  `storage_unavailable`; a send failure is 502 and no delivery row exists; a cancelled
  request still sends and still writes the row; a credit note is sent with its text;
  `deliveries[]` on the document; `sendDefaults` for a sender, absent for a reader and
  on a draft, absent with a warn log when the directory fails; the four warnings, the 2027
  switch under the fixed clock; `invoices:issue` required; the rate limit's 429;
  `mail.Outbound.ReplyTo` reaches the message (`srv/mail`); the harness's locked-call
  hook sees `SMTPSend` and never inside a lock.
- **The export.** Byte-exact against a hand-written file (the BOM, the separator, the
  decimal comma, CRLF, the quoting, the formula guard on a buyer named `=cmd`, a
  negative amount unguarded), one row per VAT row, credit notes negative, the period's
  selection, the file name and headers, 5000 + 1 rows a 400, `from > to` and a missing
  date a 400.
- **Stats.** The figures over a planted set (open, partially paid, overdue, paid,
  credited, a credit note excluded from outstanding, a removed payment not counted, the
  period and the previous period, the boundary day in and the day before out); the
  default period; an invalid period; the envelope's `from`/`to`.
- **The slots.** Export carries payments and deliveries; erase locks the documents,
  writes the marker, blanks every delivery's recipient and every payment's note (live
  and removed) and reports the four kinds, keeps the payments, and run twice reports
  zeros; **the race**: a send whose directory read
  happened before the erase and whose row is written after it leaves `recipient = ''`
  (the send is held between the two with the test seam).
- **The integration test** (D9).
- **Frontend.** The payments card and both modals with their refusals and their
  pending state; the state badge and chips; the send dialog with both alerts and the
  prefilled recipient; the deliveries card; the CSV button's error path; the dashboard
  card's gating on module and permission and its hint; the customer tab's gating, its
  active state and its "New invoice" on an active customer only; both catalogs.
- **Docs** checked against the code as 1A's Task 11 did: every endpoint, code and
  permission in `docs/invoices.md` exists, and nothing the code has is missing.
