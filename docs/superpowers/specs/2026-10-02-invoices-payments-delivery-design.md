# Invoices — payments, delivery and the export — design (Invoices phase 1B)

The second delivery of the Invoices module, on a branch cut from `main` after phase 1A
merged (PR #128, `0e84ab03`), building only on 1A's tables. Phase 1A
(`docs/superpowers/specs/2026-09-26-invoices-foundation-design.md`, "Phase 1B (next
branch)" at its end) recorded the review's rulings for this phase; every one of them is
honoured below, and where this design reads one of them differently it says so.
Research: `docs/superpowers/research/2026-09-26-invoices-module.md` (§2.5 retention of
payment registrations, §5.2 "open amount = gross − credited − paid; paid / partly paid /
overdue is derived, not stored", §6.3 the patterns to take). Module doc: `docs/invoices.md`.

A document issued in 1A is lawful and can be handed over. What 1A cannot say is whether
it was paid, and it cannot hand it over itself. This delivery is:

- **payments**: a registration of money received against an invoice, kept five years,
  removable only by a soft removal with a reason;
- **derived states**: `open`, `partially_paid`, `overdue`, `paid`, `credited` — computed,
  never stored, from one SQL function with a Go mirror pinned to it;
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

### D2 — Payments: `invoices.payments`, a registration kept five years

Migration `00035_invoices_payments_delivery.sql` holds everything in this phase (D2, D3,
D4). No column is named with a word PostgreSQL reserves; 1A's schema test
(`TestInvoicesSchema_NamesNoColumnWithAReservedWord`) already covers every column of the
`invoices` schema, the new tables included.

**`invoices.payments`:**

| Column | Rule |
| --- | --- |
| `id` | identity, start 1001 |
| `invoice_id` | `REFERENCES invoices.invoices ON DELETE RESTRICT` |
| `paid_on date` | the day the money arrived; on or after the invoice's `issue_date`, not after today (Oslo) |
| `amount numeric(14,2)` | > 0, at most two decimals, at most 99 999 999 999.99 (the document bound) |
| `currency char(3)` | the document's — NOK, and only NOK in this phase; copied, never chosen |
| `reference varchar(100)` | the bank's or the payer's reference, `''` when none |
| `note varchar(500)` | free text, `''` when none |
| `registered_by_user_id uuid`, `registered_at timestamptz` | |
| `removed_at timestamptz`, `removed_by_user_id uuid`, `removal_reason varchar(200)` | the soft removal: all three set together or none (a CHECK), the reason non-empty |

Indexes: `ix_payments_invoice (invoice_id)` and the partial
`ix_payments_invoice_live (invoice_id) WHERE removed_at IS NULL` — the sums read the live
rows.

**A payment is a record, not a correction.** Payment registrations are bookkeeping
material kept five years (research §2.5), so a row is never deleted and never edited: a
trigger `tr_payments_immutable` (`invoices.refuse_payment_change()`, SQLSTATE `P0001`,
"invoices: a payment registration is immutable") refuses every DELETE and every UPDATE
but the one that sets `removed_at`, `removed_by_user_id` and `removal_reason` from NULL
to values, once — the 1A `refuse_issued_document_change` shape (the whole row as jsonb
less those three columns must be unchanged, and the three must have been NULL). A
mistake is removed with a reason and registered again.

**A payment belongs to an issued invoice.** A second trigger, `tr_payments_parent`
(`invoices.refuse_payment_on_unissued()`), reads the document `FOR SHARE` on INSERT —
the 1A child-row pattern — and refuses a row under a draft or a credit note ("invoices:
a payment needs an issued invoice"). The API refuses first, with its own codes; the
trigger is the floor.

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
   `CreditedGross`) and `paid` (the live payments' sum) are read under that lock, and:
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
payment is allowed** and may push the open amount below zero (D3).

**`POST /invoices/{id}/payments/{paymentId}/remove`** (`invoices:access+invoices:payments`),
body `{reason}`: 404 when the payment is not that invoice's; 400 on `reason` (empty or
over 200); 409 `payment_removed` when already removed. It locks the invoice `FOR
UPDATE` too — the state and the open amount are judged under one lock by every path —
and sets the three columns. It answers the document. A removal is never undone; the
payment is registered again.

The document response carries every registration, removed ones included, so the page
can show them struck through with the reason — the record is the point.

### D3 — Derived states, from one SQL function and its Go mirror

Nothing is stored. For a document, with `credited` the issued credit notes' gross (0 for
a credit note) and `paid` the live payments' sum, judged against `today` (Oslo), the
first match wins:

| State | When |
| --- | --- |
| `draft` | `status = 'draft'` |
| `issued` | an issued credit note |
| `credited` | an issued invoice with `credited ≥ gross_total` |
| `paid` | `gross_total − credited − paid ≤ 0` |
| `overdue` | `due_date < today` |
| `partially_paid` | `paid > 0` |
| `open` | otherwise |

The order is the ruling's: a fully credited invoice is `credited` even if it was paid
first (the money is now a refund due, below); a paid invoice is never `overdue`; a late
partial payment is `overdue`, not `partially_paid` — overdue is the fact that matters.

**One SQL function**, `invoices.document_state(kind text, status text, gross numeric,
credited numeric, paid numeric, due_date date, today date) RETURNS text`, `LANGUAGE sql
IMMUTABLE PARALLEL SAFE`, in the migration between goose `StatementBegin/End` and dropped
in `Down` — the `expenses.owes_employee` precedent (`00033`). The list, the stats and
the customer tab filter and sort with it; `today` is always a parameter from
`Deps.Clock()` in Oslo, never `CURRENT_DATE`. **A Go mirror**, `documentState(...)` in
`srv/invoices/state.go`, computes the same for a response, and
`TestDocumentState_TheGoMirrorAgreesWithTheSQLFunction` runs every combination of kind,
status, credited-vs-gross, paid-vs-open and due-vs-today through both and fails on any
difference — the owes_employee test's shape, with `modtest.One[string]`.

The state names on the wire are **snake_case** (`partially_paid`), as every other
multi-word wire value of this codebase is (`credit_note`, `relay_accepted`); the ruling
wrote `partiallyPaid` in prose, and this is read as a spelling, not a decision — on the
record for the user's verdict.

**The response.** Every document answers `state` (required). An issued invoice also
answers `paidAmount`, `openAmount` (= gross − credited − paid, which may be negative),
`refundDue` (= −open, present only when open < 0; refunds are phase 4 — this is the
figure, not a flow), `payments[]` (D2) and, with D4, `deliveries[]` and `sendDefaults`.
An issued credit note answers `deliveries[]` and `sendDefaults`. Money stays absent
when it does not apply: a draft has no `paidAmount`, a credit note none.

**The list.** `GET /invoices` gains `state` — one of `open`, `partially_paid`,
`overdue`, `paid`, `credited` — validated in Go like `status` (400 "'state' must be one
of …"), applied in SQL through the function with `@today`; it implies issued invoices,
and combines with the other filters. Each item answers `state` (required) and, on an
issued invoice, `openAmount`. The page's filter chips offer the five states beside the
existing status and kind chips.

No OpenAPI `enum` — none is used anywhere in `openapi/*.yaml`; the values are in the
description and checked in Go.

### D4 — E-mail delivery: the stored PDF, sent now, logged once

**The platform change.** `mail.Outbound` (`srv/mail/outbound.go`) gains `ReplyTo
string`; `message()` calls `msg.ReplyTo(out.ReplyTo)` when it is set; `go-mail` writes
the header. A test in `srv/mail` reads the built message back and finds the header.
Nothing else in the platform changes: `Deps.Config.Mail`, `Deps.SMTPSend` and
`modtest.WithSMTPSend` exist.

**`POST /invoices/{id}/send`** (`invoices:access+invoices:issue`), body
`{recipient?}` — an override of the address, validated as one (`net/mail.ParseAddress`,
400 on `recipient`). In order:

1. **503 `mail_unavailable`** when `Config.Mail.Driver` is not `smtp` (the `log` driver
   delivers nothing; an installation without SMTP cannot hand a document over this way).
   Judged first: nothing is read for an installation that cannot send.
2. 404; 409 `invoice_draft`.
3. **The recipient**: the override, else the current billing profile's `InvoiceEmail`
   — a directory read, outside any lock, through `contractscalls.go`; a credit note
   reads the profile too (it goes to the same buyer, today's address, not the
   snapshot's — the person's mail changes, the document does not). An anonymised or
   unknown customer has none. None is **409 `no_invoice_email`**.
4. **The PDF**: the stored object, verified against its hash, exactly as the download
   does (`pdfstore.go`); a document whose PDF was never stored is stored once first —
   the download's own path — so a send never attaches bytes the store does not hold.
   503 `storage_unavailable`, 500 on a missing or altered object or a render failure,
   as the download answers.
5. **The envelope** (`mail.Outbound`): From `Config.Mail.From` with `DisplayName` the
   document's **seller snapshot's** legal name (what the PDF says); **Reply-To the
   current settings' `email`** when set (replies should reach today's mailbox) and
   absent when the settings have none; To the recipient; `MessageID` a fresh
   `<uuid>@vantigo.invalid` (communications' domain); the PDF attached under the
   download's own localised filename (`faktura-1001.pdf`, `invoice-1001.pdf`,
   `kreditnota-1002.pdf`, `credit-note-1002.pdf`); plain text only, in the document's
   `buyer_language`, the exact texts below. No HTML, no logo, no template system — the
   wording lives in Go beside the PDF's labels (`pdf.go`'s `labels` map is the
   precedent).
6. **The send**, through `Deps.SMTPSend` (nil → `mail.SendOutbound`) with
   `Config.Mail`, under a 30-second timeout (communications' send timeout). **A failure
   is 502 `mail_failed` and records nothing** — the ruling. The error is logged at
   warn with the document id, never put on the wire.
7. **The log**: one row in `invoices.deliveries`, auto-committed after the send.
   A row that fails to write after a successful send is logged at error with the
   document id and the recipient — the mail went; the operator is told.
8. The document is answered, its `deliveries[]` now holding the row.

Sending twice is allowed and logged twice: a re-send is a legitimate act. No
suppression list is read — `communications.suppressions` is another module's table
(`docs/module-boundaries.md` rule 4), and a customer whose invoice address bounces is a
problem the person sending will hear about.

**`invoices.deliveries`:**

| Column | Rule |
| --- | --- |
| `id` | identity, start 1001 |
| `invoice_id` | `REFERENCES invoices.invoices ON DELETE RESTRICT` |
| `recipient varchar(254)` | the address it went to; **`''` once anonymised** (D6). Named `recipient`, never `to` (reserved) |
| `subject varchar(200)` | as sent |
| `message_id varchar(200)` | the Message-ID, bare |
| `pdf_sha256 char(64)` | which bytes were attached |
| `sent_at timestamptz`, `sent_by_user_id uuid` | |

Immutable like the rest: `tr_deliveries_immutable` refuses DELETE and any UPDATE but
`recipient` to `''` — the one write anonymisation makes.

**The texts.** `{number}`, `{seller}`, `{amount}` (the PDF's own money format, `NOK
15 045,00` / `NOK 15,045.00`), `{due}` (the PDF's date format), `{account}` (the
snapshot's bank account as the PDF prints it), `{original}` (a credit note's original
number).

Invoice, nb — subject `Faktura {number} fra {seller}`:

```
Hei,

Vedlagt er faktura {number} fra {seller} på {amount}, med forfall {due}.
Beløpet betales til kontonummer {account}. Merk betalingen med fakturanummer {number}.

Med vennlig hilsen
{seller}
```

Invoice, en — subject `Invoice {number} from {seller}`:

```
Hello,

Please find attached invoice {number} from {seller} for {amount}, due {due}.
Please pay to account {account}, quoting invoice number {number}.

Kind regards
{seller}
```

Credit note, nb — subject `Kreditnota {number} fra {seller}`:

```
Hei,

Vedlagt er kreditnota {number} fra {seller} på {amount}, som krediterer faktura {original}.

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
account line always has one. The IBAN and BIC are on the PDF; the mail names the
domestic account only.

**Warning loudly.** The ruling: "warn loudly when the profile says `ehf` or the buyer is
a public body". The directory carries `InvoiceDelivery`; it carries **no public-body
fact** — "Public sector" in `docs/customers.md` is a group an installation may name, a
vocabulary word, not a fact a module can read. So the warning is on the delivery
preference alone, and the public-body half is **dropped and on the record**: the B2G
duty is phase 2's to enforce, with the Peppol capability it needs.

To warn before the send, not after, an issued document's response (`GET
/invoices/{id}`, and every operation that answers the document) carries
`sendDefaults{recipient?, preference?}`: the current profile's `InvoiceEmail` and
`InvoiceDelivery`, read through the directory outside any lock, both absent when the
directory no longer knows the customer. This is the one new directory read on an issued
document; 1A read the profile for drafts only. The Send dialog prefills `recipient`,
shows a **red alert that cannot be missed** when `preference` is `ehf` ("This customer
expects EHF. An e-mailed PDF does not meet the e-invoicing duty; phase 2 adds EHF.") and
a plain note when it is `efaktura` or `paper`; the send itself is never refused for it.
The send's response adds `warnings: ["delivery_preference_ehf"]` likewise, so a client
that skipped the dialog still learns.

### D5 — The accountant's CSV export

**`GET /invoices/export.csv?from&to`** (`invoices:access`): `from` and `to` required
calendar dates, `from ≤ to` (400 on either), the issued documents whose issue date is in
the range — the journal's own selection — in number order, **one row per (document ×
VAT summary row)**, in each document the rows by category then rate. A credit note's
amounts are **negative** in every amount column (stored positive, signed on output, the
journal's rule).

The byte format is the expenses payroll file's, byte for byte (`docs/expenses.md`, "The
payroll CSV"), duplicated into this module as customers did (depguard forbids sharing
the file): UTF-8 with a BOM, `;`, decimal comma, `YYYY-MM-DD`, CRLF after every row the
last included, RFC 4180 quoting with `;` as the separator, the formula-injection guard on
every text cell. Amounts are the stored `numeric` values as text, never a float.

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
`text/*` bodies, so the export is covered like any operation.

A button "Export CSV" on the **journal page**, which already holds the period, fetching
through the customers `downloadFile` pattern so a 400 is a notification in the reader's
language, not a problem document opened in the browser.

### D6 — The two customer slots, extended

- **Merge** (`CustomerReferenceHolder`): unchanged — payments and deliveries hang off
  the document by id and never carry a customer id. Reported as before.
- **Export** (`ExportCustomerData`): each document's section gains `payments` (every
  registration: `paidOn`, `amount`, `reference`, `note`, `registeredAt`, and the removal
  when there is one) and `deliveries` (`recipient`, `sentAt`, `subject`). A draft has
  neither.
- **Erase** (`EraseCustomerData`): deliveries are **kept** — they are the evidence of
  when the claim was sent — with `recipient` set to `''`, reported as
  `invoices.deliveries` with the count blanked; payments are kept untouched (they carry
  no personal data; the reference is the bank's). The reporting order stays:
  `invoices.drafts`, `invoices.documents` (0), then `invoices.deliveries`. The
  anonymisation table in `docs/customers.md` gains the row.

### D7 — Stats, and the dashboard card

**`GET /invoices/stats/summary?from&to`** (`invoices:access`), the host dashboard's own
shape (`buildStatsUrl(module, "summary", range)`, date-time `from`/`to`,
`apicommon.NormalizePeriod`, default 30 days, 400 `application/problem+json` on an
invalid period):

```
{
  outstanding: {amount, count},        // now: issued invoices with open > 0, credit notes excluded
  overdue:     {amount, count},        // now: those with due_date < today
  issued:      {count, grossTotal},    // invoices issued in the period
  credited:    {count, grossTotal},    // credit notes issued in the period
  paid:        {amount, count},        // live payments with paid_on in the period
  previousIssuedGrossTotal             // the previous period's issued gross, for the delta
}
```

All NOK (only NOK in this phase; the field is not per currency). `today` is Oslo from
`Deps.Clock()`; the period is UTC day buckets as every module's stats are. No
timeseries and no attention list in this phase: the dashboard queries them per module
only when the module declares them, and Invoices declares the summary alone — on the
record, Out of scope.

**The dashboard card** (`apps/host/frontend/src/routes/dashboard.tsx`): an `Invoices`
entry in `moduleCards` (`module: "invoices"`, `requiredPermissions: ["invoices:access"]`,
path `/invoices`), the summary query, and three KPIs — outstanding, overdue (red when
non-zero), issued in the period with the delta against the previous period — through
the shared `KpiCard`. The hand-written `InvoicesSummary` wire type beside the others.
The same three KPIs sit at the top of the Invoices list page (expenses' status strip is
the precedent), from the same query.

### D8 — The customer page's Invoices tab

Host-owned composition, the projects tab's shape: `customerDetailTabs` gains
`{ key: "invoices", module: "invoices", requiredPermissions: ["invoices:access"], to:
"/customers/$customerId/invoices" }`; the route file; `-customer-invoices-tab.tsx`
guarding on the enabled module and rendering `CustomerInvoicesPanel` from
`@vantigo/invoices-ui` with `canCreate` = `invoices:create && customers:view &&
!isReadOnlyCustomer(customer)` — a merged-away or anonymised customer gets no "New
invoice", and the server would refuse it anyway (1A's gates). The panel is the list
filtered by `customerId` (the existing query) with the state badge, open amount and the
same paging, and "New invoice" creating the draft for that customer and navigating to it.
Catalog keys en + nb in `catalogs/customer.ts`.

### D9 — The customers + invoices integration test

`srv/integration/invoices_test.go`, both modules real (`modInvoices` added to the
harness's names and the merged recorder; `invoices.yaml` merged in), with an object
store (`storage.NewFS` over `t.TempDir()`) and `modtest.WithSMTPSend` recording the
envelope:

1. a customer created through the customers API with a billing profile (invoice
   e-mail, 30 days, a buyer reference) → a draft takes the profile's terms and reference
   → issued → the buyer snapshot is the customer's own data;
2. a disabled customer is refused a new draft (`customer_blocked`) and a credit note of
   its issued invoice is still created and issued;
3. a merge through the customers API re-points the documents, and `customer.merged`'s
   `repointed` names `invoices.invoices`;
4. a send reaches the profile's invoice e-mail with Reply-To the seller's; the delivery
   is on the document;
5. a person's export through the customers API has the invoices section with the
   document, its payment and its delivery; the anonymisation worker run once deletes
   the drafts, keeps the document, blanks the delivery's recipient, and
   `customer.anonymised` lists `invoices.drafts`, `invoices.documents` and
   `invoices.deliveries`.

The invoices package's own tests keep their fake directory; this file is where the
two modules' understanding of the contract is proven to be one.

### D10 — The frontend

- **List**: the KPI strip (D7); the state chips (D3); a state badge per row — grey
  draft, blue open, yellow partially paid, red overdue, green paid, grey credited —
  and the open amount column on issued invoices.
- **Invoice page, issued invoice**: the state badge in the header beside the number;
  the totals card adds "Paid", "Open" and, when due, "Refund due"; a **Payments card**
  — date, amount, reference, note, registered when; removed rows struck through with
  their reason; "Register payment" (`canRegisterPayments`, hidden when the state is
  `paid` or `credited`) opening a modal with today prefilled, the amount prefilled with
  the open amount, reference and note; "Remove" per live row opening a reason modal;
  every refusal in the reader's language (`invoice_settled`, `payment_exceeds_open`
  with the open amount, `payment_removed`).
- **Invoice page, issued document**: "Send" (`canSend`) opening the Send dialog —
  recipient prefilled from `sendDefaults`, editable; the red EHF alert and the paper /
  eFaktura note (D4); "Send" → the notification "Sent to <recipient>"; a **Deliveries
  card** listing each send (when, to whom); `no_invoice_email`, `mail_unavailable`,
  `mail_failed` in the reader's language. The page explains a blank recipient on an
  anonymised customer's delivery as "(anonymised)".
- **Journal**: "Export CSV" (D5).
- **Settings**: the checklist gains "Mail is configured (SMTP)" from `meta.mailAvailable`
  — informative, not a gate on issuing.
- **Host**: the dashboard card (D7), the customer tab (D8), the admin catalog entry (D1).
- en + nb throughout; `translations:check` passes.

### D11 — Docs

- `docs/invoices.md`: a "Payments and the state of an invoice" section (the table of
  states and the order, open amount, refund due, soft removal and why a row never
  leaves, the lock the registration and the credit issue share); "Sending a document"
  (the recipient rule, Reply-To, the texts, 502/503 codes, the log, re-sending, the EHF
  warning and the dropped public-body half); "The CSV export" (the columns and the
  byte format); "Stats"; the endpoints table and the permissions table extended; the
  retention section naming payments and deliveries; the anonymisation paragraph
  gaining deliveries; "What comes next" moved to phase 2.
- `docs/module-boundaries.md`: the platform `mail.Outbound.ReplyTo` change; the
  customer tab as host-owned composition.
- `docs/customers.md`: the anonymisation table's invoices row gains deliveries.
- `docs/customers-authentication.md` (the SMTP section): invoices now sends through
  the same `SMTP_*` configuration.
- `ROADMAP.md`: 1B done, phase 2 next. `deploy/compose/README.md` "Upgrading": the new
  permission, and that sending needs `SMTP_*`.

## Out of scope

EHF/Peppol and KID (phase 2); hours, expenses and milestones as lines (phase 3);
overpayment, customer credit balances, refunds as a flow, payment files, reminders and
late interest (phase 4); energy billing (phase 5). Also: editing a payment (remove and
register again); a payment in another currency than the document's; a payment against a
credit note; allocating one payment across several invoices; HTML mail, a logo, an
editable template or a personal message in the mail; sending through an outbox or a
worker (a send is synchronous and its failure is the caller's to see); honouring
`communications.suppressions`; bulk send; a "paid" stamp on the PDF (the PDF is
immutable); attention and timeseries stats; per-currency stats; the public-body warning
(no fact on the contract); user display names on payments and deliveries (ids only);
a purge of anything.

## Testing

Through 1A's harness (`modtest`, the fake directory, the fake object store, the fixed
Oslo clock), plus `modtest.WithSMTPSend` recording envelopes and an env with
`MAIL_DRIVER=smtp` for the send tests.

- **Payments.** Register against an issued invoice; 404; `invoice_draft`;
  `credit_note_no_payments`; every 400 (`paidOn` before issue, after today, `amount`
  0, negative, three decimals, over the bound; `reference`, `note` lengths);
  `invoice_settled` once paid; `payment_exceeds_open` with `openAmount`; a payment
  equal to the open amount is accepted and the state is `paid`; removal with a reason
  reopens it; `payment_removed`; 400 on an empty reason; direct SQL cannot update or
  delete a payment, can set the removal once, cannot insert one under a draft or a
  credit note; **the lock**: a registration racing a credit-note issue of the same
  invoice — both commit, and the final figures are consistent (open = gross − credited
  − paid, possibly negative, never a lost update); two racing registrations of the
  whole open amount — exactly one succeeds; a registration for an anonymised customer's
  invoice succeeds; `invoices:payments` on both writes.
- **States.** The mirror test over every combination; a credited invoice that was paid
  is `credited` with `refundDue`; a late partial payment is `overdue`; the day it falls
  due it is not overdue, the day after it is (the fixed clock); the list `state` filter
  for each value, combined with `kind` and `customerId`; an unknown state is a 400; a
  draft and a credit note never match a state filter.
- **Sending.** `mail_unavailable` first on the `log` driver; `invoice_draft`;
  `no_invoice_email` for a profile without one and for an anonymised customer; the
  override wins and is validated; the envelope (From, DisplayName the snapshot's seller,
  Reply-To the current settings' email and absent without one, To, the subject and body
  in nb and en, the attachment's name, type and bytes equal to the stored PDF, the
  Message-ID); a never-stored PDF is stored first and the row's hash is the attached
  bytes'; `storage_unavailable`; a send failure is 502 and no delivery row exists; a
  credit note is sent with its text; `deliveries[]` on the document; `sendDefaults` on
  an issued document and absent on a draft; `delivery_preference_ehf` on the response;
  `invoices:issue` required; `mail.Outbound.ReplyTo` reaches the message (`srv/mail`).
- **The export.** Byte-exact against a hand-written file (the BOM, the separator, the
  decimal comma, CRLF, the quoting, the formula guard on a buyer named `=cmd`), one row
  per VAT row, credit notes negative, the period's selection, the file name and headers,
  5000 + 1 rows a 400, `from > to` and a missing date a 400.
- **Stats.** The figures over a planted set (open, partially paid, overdue, paid,
  credited, a credit note excluded from outstanding, a removed payment not counted, the
  period and the previous period); the default period; an invalid period.
- **The slots.** Export carries payments and deliveries; erase blanks every delivery's
  recipient and reports `invoices.deliveries` with the count, keeps the payments, and
  run twice reports zero.
- **The integration test** (D9).
- **Frontend.** The payments card and both modals with their refusals; the state badge
  and chips; the send dialog with the EHF alert and the prefilled recipient; the
  deliveries card; the CSV button's error path; the dashboard card's gating on module
  and permission; the customer tab's gating and its "New invoice"; both catalogs.
- **Docs** checked against the code as 1A's Task 11 did: every endpoint, code and
  permission in `docs/invoices.md` exists, and nothing the code has is missing.
