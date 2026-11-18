# Invoices phase 6 — implementation plan

This plan builds phase 6 of Invoices. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-17-invoices-phase6-design.md`, revision
  4. Its decisions are numbered I*n*.
- **The research:** `docs/superpowers/research/2026-11-17-invoices-competitive-gaps.md`.

This revision details **deliveries A and B**. C to E are sketched, and each gets its own
section before it starts.

**Abbreviations:**

- `srv/` is `apps/server/internal/`;
- `inv/` is `srv/invoices/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/invoices/frontend/src/`.

## How it lands

- **One PR per delivery, against `main`**, except B, which lands as two (B-1, then B-2
  stacked on it). A, B and D do not depend on each other. C waits for B-1's
  `amount_nok`, not for B-2.
- **Migrations:** each delivery claims the next number at the time it opens its PR, and
  rechecks it against `main` right before merging.
- **Order** (spec §14 Q8): A, B, C, E, D. D's code may start before C, against MPS's test
  environment. Its live send waits for Vantigo's submitter agreement with Mastercard
  Payment Services.

| Delivery | Branch | Migration (main at `a9f140c82`, latest `00192`) |
| --- | --- | --- |
| A | `claude/invoices-branding` | `00193_invoices_branding` |
| B-1 | `claude/invoices-credit` | `00197_invoices_credit` (00194–00196 are the api-access stack's, PR #370) |
| B-2 | `claude/invoices-split-rounding` | next free when its PR opens (`00198` if nothing lands first) |
| C | `claude/invoices-currency` | next free |
| D | `claude/invoices-efaktura` | next free |
| E | `claude/invoices-send-all` | next free |

## Delivery A — the logo, the phone, the HTML mail

### A1. Migration `00193_invoices_branding.sql`

- `invoices.settings`:
  - `logo_object_key varchar(200)`, `logo_sha256 char(64)` and
    `logo_content_type varchar(20)`, all NULL. `ck_settings_logo` requires all three or
    none, and the content type to be `image/png` or `image/jpeg`.
  - `phone varchar(30) NOT NULL DEFAULT ''`, as the other text columns are
    (`mig/00034:18-42`).
- `invoices.invoices`:
  - `seller_logo_object_key varchar(200)` and `seller_logo_sha256 char(64)`, NULL, both
    or none.
  - `seller_phone varchar(30)`, NULL.
  - No trigger change: `refuse_issued_document_change` compares whole rows
    (`mig/00034:254-281`).
- `invoices.deliveries`:
  - `message varchar(2000) NOT NULL DEFAULT ''`.
  - Replace `refuse_delivery_change` (`mig/00035:127-140`). It now allows an update
    that sets `recipient` and `message` to `''` and changes nothing else.
  - Replace `guard_delivery_insert` (`mig/00035:156-173`). For an erased customer it
    blanks `message` as well as `recipient`.
  - The Down restores both functions as they were.
- Append the file to `inv/sqlc.yaml`'s schema list. `srv/db/schema_test.go` checks the
  list.

### A2. Server: settings and the logo

- **Queries** (`inv/queries/settings.sql`):
  - `UpdateSettings` gains `phone`.
  - New `SetSettingsLogo` and `ClearSettingsLogo`, under `LockSettings`.
  - `GetSettings` returns the new columns.
- **Handlers** (`inv/settings.go`):
  - `parseSettings` validates `phone` by Customers' rule (`srv/customers/values.go:326`).
    Copy the rule rather than import it, under rule 1. Name the field `phone`.
  - `settingsResponse` answers `phone` and `logo` (`{contentType, sha256, updatedAt}` or
    null).
- **`PUT /api/v1/invoices/settings/logo`** (multipart `file`, `invoices:manage`):
  - It follows `srv/staff/person_photo.go:178-284` and its form helpers
    (`person_photo_validation.go`):
    1. Read with a limit of 512 KiB + 1.
    2. Sniff the type, PNG or JPEG.
    3. `image.DecodeConfig`: the longer side at most 2,000 px, the shorter at least
       16 px.
    4. `imagemeta.Strip`.
    5. Compute the SHA-256 and `objectPut` at `branding/logo-{sha256}.{png|jpg}`.
    6. Under `LockSettings`, set the columns.
  - It answers 200 with the settings, and refuses with:
    - 400 `logo_unsupported_type`, `logo_too_large`, `logo_too_wide`, `logo_too_small`
      or `logo_invalid`;
    - 503 `storage_unavailable`.
  - The body limit is under the router's default of 1 MiB, so no `BodyLimits` entry is
    needed. Say so in a comment.
- **`DELETE /api/v1/invoices/settings/logo`** (`invoices:manage`) clears the columns and
  never deletes the object (I1). It answers 204, or 404 when no logo is set.
- **`GET /api/v1/invoices/settings/logo`** (`invoices:access`) returns the bytes for the
  settings page, with `ETag` set to the SHA-256 and `Cache-Control: private, max-age=0,
  must-revalidate`.
- **Events:** the module's settings change event, if it has one, names `logo_set`,
  `logo_removed` and `phone`. If it has none, add nothing.

### A3. Server: the snapshot, the PDF and the letters

- **Issue** (`inv/issue.go:672-685` `sellerSnapshot`, `inv/queries/invoices.sql:155-168`)
  copies `phone` and the logo columns. Credit notes take them the same way.
- **The preview** (`inv/pdfstore.go:632`, `copyIssueParams` `:731-745`) carries the new
  fields.
- **The model:**
  - `pdfParty` (`inv/pdf.go:131-135`) gains `phone`. `sellerLines` (`:349-366`) prints
    it after the e-mail.
  - `pdfDocument` gains `logo []byte` and `logoType`. `pdfDocumentOf` (`inv/pdfstore.go:44`)
    loads the object by the snapshot's key.
  - The loaded bytes must hash to the snapshot's SHA-256, or the render fails.
- **The layout** (`inv/pdf.go:629`, the title row):
  - With a logo, the row is the image in a column fitted to 45 × 20 mm, with the
    proportions kept (`image.NewFromBytesCol`), beside the title.
  - Without one, the layout is exactly today's.
  - Do not change `pinnedRenderSHA256` (`inv/pdf_internal_test.go:234`). A document
    without a logo or phone must render byte for byte as before; that is the test that
    nothing else moved.
  - Add a second pinned hash for a fixture with a logo.
- **Reminder letters** (`inv/reminderpdf.go:51,137,352`): the same title row and seller
  lines. The reminder goldens with no logo stay as they are.
- **`contracts.SellerProfile`** (`inv/sales_tax.go:26-29,108-120`) answers `Phone` and
  `LogoStorageKey` from the settings, and the comment changes.

### A4. Server: the HTML mail and the message

- **`inv/mailtext.go`:**
  - `mailText` gains `html`. A `coverMail` HTML template (`html/template`, embedded),
    per language, renders the same text:
    - paragraphs;
    - the pay link as a link;
    - the logo as `<img src="cid:logo" alt="{seller name}" style="max-height:60px">`
      when there is one.
  - No external URL other than the pay link, no tracking, and inline styles only.
  - Update the file comment.
- **Send** (`inv/send.go:232-420`):
  - `InvoicesSendRequest` gains `message` (optional, at most 2,000 characters, trimmed).
    It is validated as 400 `message_too_long`.
  - The message goes above the standard text in both bodies, escaped in the HTML, with
    line breaks kept as `<br>`.
  - `InsertDelivery` (`inv/queries/deliveries.sql:8-18`) stores the message.
  - `envelope` sets `HTMLBody` and adds the logo as an inline attachment (`ContentID:
    "logo"`, `Inline: true`) beside the PDF.
- **Reminders** (`inv/reminder_worker.go:317-321`): the same HTML wrapper, with no
  message.
- **Erasure:** `BlankCustomerDeliveries` (`inv/queries/customers.sql:57-64`) sets
  `message = ''` as well.
- **The deliveries' response** answers `message`.
- **The Cloudflare driver** (`srv/mail/cloudflare.go:199-209`):
  - An inline attachment goes as `disposition: "inline"` with `content_id` set to its
    `ContentID`.
  - `cloudflareAttachment` gains `ContentID string \`json:"content_id,omitempty"\``.
  - Update the driver's test, replacing the "cannot carry an inline attachment" case.
- **The SMTP renderer** already embeds inline parts (`srv/mail/outbound.go:268-285`).
  Add a test that a document mail carries `multipart/related` with the logo.

### A5. OpenAPI and generation

- `openapi/invoices.yaml`:
  - settings request and response: `phone`, `logo`;
  - the three logo operations, with multipart following `openapi/staff.yaml:11981`;
  - `InvoicesSendRequest.message`;
  - the delivery's `message`.
- Run `go generate ./...` in `apps/server` (sqlc, oapi-codegen, the embedded docs) and
  `bun run gen:client`. Commit `fe/api-schema.d.ts`. CI checks it.

### A6. Frontend

- **`fe/pages/settings.tsx`** (`SellerForm` `:229`):
  - a **Phone** input next to the e-mail (`:395`);
  - a **Logo** block above the footer:
    - the current logo as an image, from the GET, with the SHA-256 as the cache key;
    - **Upload logo** (PNG or JPEG, up to 512 KB) and **Remove logo**;
    - the refusals, shown by their codes through `lib/errors.ts`;
    - help text: *Shown at the top of the invoice, credit notes and reminder letters
      issued from now on. Documents already issued keep the logo they were issued
      with.*
- **`fe/api/settings.ts`:** `uploadInvoiceLogo(file)` and `removeInvoiceLogo()`, which
  invalidate the settings query.
- **`fe/pages/-send-dialog.tsx`** (`:165-175`):
  - a **Message** `Textarea` (optional, 2,000 characters with a counter) below the
    recipient;
  - its help text: *Shown above the standard text in the e-mail.*
- **`fe/components/deliveries-card.tsx`** shows a delivery's message, when it has one,
  collapsed.
- **i18n:** every new string in `en` and `nb` (`fe/i18n.ts`). `bun run translations:check`
  passes.
- **Tests:**
  - the logo block: upload, a refusal, remove;
  - the send dialog sends `message`.

### A7. Seed data

`apps/server/cmd/vantigo/seed.go`, `seedInvoicesReceivables` (`:1525`):

- set a phone in the settings;
- store a small generated PNG logo through the object store, and set the logo columns;
- give the issued seed invoices' snapshots (`:1660-1688`) the phone and the logo.

### A8. Documentation (spec §10)

- **`docs/src/content/docs/en/user/invoices.md` and `nb/user/invoices.md`:**
  - *The seller record and the number series*: the phone and the logo, and what is
    issued with which logo;
  - *Sending a document by e-mail* › *Sending*: the message, and that the mail shows the
    logo.
- **`en/reference/invoices.md`:**
  - the settings, invoices and deliveries rows of the model table;
  - *The PDF*: the logo's place and size, and the snapshot;
  - *Sending a document*: HTML and text, the inline logo, the message, its limit and its
    erasure;
  - the endpoints and their codes;
  - `Seller(ctx)` (`:5740-5743`): Phone and LogoStorageKey are now filled;
  - *What comes next*: phase 6 A done.
  - `sources` gains the migration.
- **`admin/` page that documents the mail drivers:** the Cloudflare driver now carries
  inline images. Find the page by `bun run tools/docs/check-coverage.ts --list` for
  `srv/mail/`.
- **`ROADMAP.md`, Invoices:** a *Phase 6* heading with A done, and B to E listed.
- Run `go generate ./...` again after the docs, since the assistant embeds them.
- **The public site** (`../vantigo-landing`, `web/src/apps/invoices.ts`): one line under
  what the app does, on the logo on invoices and in mail. Make it a PR there, linked
  from this one. If the repository is not checked out, say so in the PR.

### A9. Assistant

- `fe/assist-targets.json`: `invoices.settings.logo` and `invoices.send.message`, with
  `en` and `nb` labels.
- Bump `promptVersion` in `srv/assistant/prompt.go:15`.

### A10. The gates

- `mise run server:check`
- `mise run server:test`, or the invoices, mail and accounting packages with `-p 1` if
  the shared database runs out of clients
- `mise run frontend:check`: read its log, since its wrapper exits 0 anyway
- `mise run docs:check`
- Commits follow Conventional Commits, `feat(invoices): …`, and are SSH-signed.

### A11. Review

An independent review of the diff before the PR leaves draft. It checks, in particular:

- the two trigger replacements;
- the SHA check on the loaded logo;
- HTML escaping of the message;
- the byte-identical PDF without a logo.

## Delivery B — the customer's balance

B is two PRs. Together they are too large to review well, and their halves meet in one
place only:

- **B-1, credit** (I8–I13, I16, I17a): the ledger, `amount_nok`, an overpayment kept as
  credit, a credit note's surplus, credit applied and refunded, the lock order, and the
  journal facts with Accounting's customer-credit item. Migration `00197_invoices_credit`.
- **B-2, the automatic split and small differences** (I14, I15): every remittance
  reference kept, one line matched to several invoices, the tolerance and its rounding
  write-off. Migration: the next free number when its PR opens.

B-2 depends on B-1 for two things: the line arithmetic in NOK that counts credit (B1.3),
and the queue action **Keep the rest as credit**, where a surplus within the tolerance
goes (I15). B-2 branches from B-1's tip and is retargeted to `main` when B-1 lands.
C needs only B-1.

Code references are against `main` at `3b09cceb7` (A merged, latest migration `00193`).
The spec's code map was written at `a9f140c82`; where a pointer has moved, the step gives
today's, and *B: readings against the code* says why.

### B1. B-1 migration `00197_invoices_credit.sql`

- **`invoices.customer_credits`**, one row per movement (I8):
  - `id bigint GENERATED ALWAYS AS IDENTITY (START WITH 1001)`, `customer_id integer NOT
    NULL`, `currency char(3) NOT NULL`, `amount numeric(14,2) NOT NULL` (signed,
    `ck_customer_credits_amount CHECK (amount <> 0)`), `amount_nok numeric(14,2) NOT
    NULL`, `kind varchar(15)` (`overpayment`, `credit_note`, `applied`, `refunded`,
    `reversed`), `credit_day date NOT NULL`, `note varchar(500) NOT NULL DEFAULT ''`,
    `registered_by_user_id uuid` (NULL only for `credit_note`, `ck_customer_credits_actor`),
    `registered_at timestamptz NOT NULL`.
  - The sources: `invoice_id`, `payment_id`, `bank_transaction_id`, `document_id`,
    `refund_id`, `reverses_id` (self-reference), each `REFERENCES … ON DELETE RESTRICT`.
    `ck_customer_credits_source` by kind:
    - `overpayment`: `invoice_id` and exactly one of `payment_id` (by hand) or
      `bank_transaction_id` (the queue); positive;
    - `credit_note`: `invoice_id` (the original) and `document_id` (the credit note);
      positive;
    - `applied`: `invoice_id` and `payment_id` (the payment of source `credit`); negative;
    - `refunded`: `refund_id`; negative;
    - `reversed`: `reverses_id`, with the reversed row's `invoice_id` copied; the sign
      opposite to it.
  - Unique: `ux_customer_credits_credit_note (document_id) WHERE kind = 'credit_note'`
    (the follow-up's idempotency, I10), `ux_customer_credits_applied (payment_id) WHERE
    kind = 'applied'`, `ux_customer_credits_reversed (reverses_id)`. Indexes on
    `(customer_id, currency)`, `invoice_id`, `bank_transaction_id`.
  - `refuse_customer_credit_change`: no DELETE; an UPDATE only of `customer_id` (the
    merge) or of `note` to `''` (anonymisation), the rest compared as jsonb, the
    `refuse_payment_change` shape (`mig/00035:67-80`).
  - `guard_customer_credit_insert`, BEFORE INSERT: `amount_nok := amount` when the
    currency is NOK and it is NULL (I13); `note := ''` when `invoices.erased_customers`
    holds the customer (`guard_delivery_insert`'s shape, `mig/00035:156-173`).
- **`invoices.credit_refunds`** (I12): `id`, `customer_id`, `refunded_on date`,
  `amount numeric(14,2) > 0`, `currency`, `amount_nok`, `bank_account char(11)` (the
  account it left), `reference varchar(100)`, `note varchar(500)`, who and when, and the
  removal triple of `payments` (`ck_credit_refunds_removal`). Its trigger allows the
  removal, the merge's `customer_id` and the note to `''`.
- **`invoices.credit_merges`**: `id`, `from_customer_id`, `into_customer_id`,
  `currency`, `amount`, `amount_nok`, `merged_at`. Written by the merge, never changed;
  it exists so the ledger hears that a balance moved customer (B3.7).
- **`invoices.payments`** (I13):
  - `ADD COLUMN amount_nok numeric(14,2)`, nullable.
  - `LOCK TABLE invoices.payments IN SHARE ROW EXCLUSIVE MODE`, as `mig/00180` does.
  - `CREATE OR REPLACE FUNCTION invoices.refuse_payment_change()` with one more allowed
    write: `amount_nok` from NULL to `amount`, the rest of the row unchanged.
  - `UPDATE invoices.payments SET amount_nok = amount`; `SET NOT NULL`; then replace the
    function again with `mig/00035`'s body. Its jsonb comparison covers the new column,
    so the strict trigger needs no new clause; only its comment names it.
  - `guard_payment_amount_nok`, BEFORE INSERT: `amount_nok := amount` for NOK when NULL.
  - `ck_payments_source` gains `credit`. `ck_payments_origin` (`mig/00178:86-95`) gains
    `(source = 'credit' AND bank_transaction_id IS NULL AND payment_attempt_id IS NULL
    AND registered_by_user_id IS NOT NULL)`.
- **`invoices.write_offs`**: `amount_nok` the same way, through `refuse_write_off_change`
  (`mig/00182:41-51`).
- **`invoices.settled_amount()`** (`mig/00182:93-104`) subtracts `moved`: the
  `credit_note` rows of the invoice plus the `reversed` rows that reverse them. Every
  open amount the queries derive (`invoices.sql:79`, `receivables.sql:21,53`,
  `stats.sql:11`, `overdue*.sql`, `attention.sql`, `bankqueue.sql:161-202`) follows
  without an edit.
- **The journal** (I16):
  - `journal_facts.invoice_id` drops NOT NULL; `ck_journal_facts_invoice CHECK
    (invoice_id IS NOT NULL OR kind IN ('credit', 'credit_merged'))`.
  - `ck_journal_facts_kind` (`mig/00182:142-144`) gains `credit` and `credit_merged`;
    `ck_journal_facts_removal` is unchanged (neither is a removal: a reversal is a row of
    its own, with its own day).
  - `record_credit_fact()`, a deferred constraint trigger AFTER INSERT on
    `customer_credits` and on `credit_merges`: `record_write_off_fact`'s shape
    (`mig/00182:176-205`), business day `credit_day` (a merge's: `merged_at`'s date in
    UTC; the reader takes the business day, as for a removal).
- **Events:** `publish_customer_credit_changed()`, AFTER INSERT on `customer_credits` and
  `credit_merges`: `invoices.customer_credit.changed` with `customerId`, `currency`,
  `balance` (one per customer a merge touches). `publish_payment_received`
  (`mig/00150:32-36`) gains `'amountNok'`.
- **`invoices.bank_transaction_events`**: `ck_bank_transaction_events_event`
  (`mig/00041:225-226`) gains `kept_as_credit`.
- **The backfill** (I10), after the triggers: one `credit_note` row, unattributed, for
  every issued invoice whose open amount is below zero — the amount its negation, the
  credit note the latest issued one crediting the invoice, `credit_day` its issue date.
  Its facts are numbered at the migration's commit.
- **The Down** refuses while `customer_credits`, `credit_refunds` or `credit_merges` hold
  a row, or a payment of source `credit` exists (`mig/00182`'s Down shape), then undoes
  the rest.
- Append the file to `inv/sqlc.yaml`.

### B2. B-1 server: the ledger, the lock and the arithmetic

- **New `inv/credit.go`** with the ledger's helpers:
  - `lockCustomerCredit(ctx, txq, customerID)` in `inv/locks.go`: `SELECT
    pg_advisory_xact_lock(hashtextextended('invoices.customer_credit:' || @customer_id,
    0))`, reported `"customer"` to `noteLock` (`locks.go:14-31`). The locks file comment
    gains I17a's order: line, customer, invoices descending, settings and counter.
  - `creditBalance(ctx, txq, customerID, currency)` (query `CreditBalance`) read after
    that lock; `insertCredit` refuses a negative row that would take the balance below
    zero with 409 `credit_exceeds_balance` and `balance` in the problem.
  - `customerOfInvoice`: a credit path reads the invoice's customer on the pool, takes
    the customer's lock, locks the invoice, and compares. A merge that committed between
    the two moved it: the transaction rolls back and runs once more with the new
    customer; a second mismatch is an error.
- **Queries** (`inv/queries/credit.sql`): `InsertCustomerCredit`, `CreditBalance`,
  `CreditBalances` (per customer and currency), `CustomerCredits` (a page of movements),
  `InsertCreditRefund`, `RemoveCreditRefund`, `CreditOfPayment`, `CreditsOfLine`,
  `MovedOf(invoice)`, `PendingCreditNoteMoves` (credit notes whose original reads
  negative and has no `credit_note` row).
- **The open amount** (I10):
  - `LivePaymentsSum` (`inv/queries/payments.sql:1-10`) answers `moved`.
  - `openAndReserved` (`inv/attempts.go:116-144`) and the invoice response
    (`inv/responses.go:214-224`) compute `gross − credited − paid − writtenOff + moved`.
  - `refundDue` leaves the response (`responses.go:221-223`). `InvoicesInvoice.refundDue`
    leaves `openapi/invoices.yaml:3427`, and `openAmount`'s description (`:3389`) loses
    "it may be negative".
  - `invoiceRefundDue` (`inv/attention.go:150-185`, query `AttentionRefundDue`) keeps
    only the charges' case: an invoice's principal no longer goes below zero.
- **The line arithmetic** (I9, I13):
  - `AppliedToTransactions` (`inv/queries/bankqueue.sql:79-94`) answers `amount_nok` for
    payments and adds the `overpayment` rows naming the line, kind `credit`, with
    `removed` true when reversed.
  - `LiveAppliedTo` (`:96-109`) sums payments' `amount_nok`, charge payments' `amount`
    and the line's live `overpayment` rows. Charge payments stay at `amount`: they are
    NOK in B and C, and have no `amount_nok`.
  - `unappliedOf` (`inv/bankqueue.go:278-306`), the `allocation_exceeds_transaction`
    check (`:793-809`), the reopen's `bank_transaction_applied` (`:1237`), and
    `ListBankTransactions`' unapplied filter (`queries/bankqueue.sql:24-60`), which
    `unappliedOf` mirrors word for word, all read those.
- **The sales journal** (`inv/sales_journal.go`, `queries/sales_journal.sql:35-71`):
  - `JournalFacts` joins the invoice with LEFT JOIN, the credit row, the refund, the
    merge, and reads `AmountNOK` from `amount_nok` where the row has one, instead of
    `amount × exchange_rate` (`nok`, `sales_journal.go:74`).
  - The customer of a `credit` fact is the credit row's, not the invoice's.
  - `JournalOpenAt` (`:73`) subtracts `moved` as of the day.
  - New `JournalCreditsAt`: per customer and currency, the balance at the end of a day,
    in one snapshot with `JournalLastSeq`.

### B3. B-1 server: the paths that write credit

Each takes I17a's locks through the helpers, and each has a lock-order test through
`lockTaken` and a races test (B6).

1. **By hand** (I9, `inv/payments.go:109-237`):
   - `InvoicesPaymentRequest.keepRestAsCredit` (default false). With it, `amt > open`
     (`:196-203`) registers `open` as the payment and the rest as an `overpayment` row
     naming the payment, its reference and note. Without it, `payment_exceeds_open`
     stands. With nothing open, `invoice_settled` stands either way: the queue is where
     money for a settled invoice is kept.
   - Taken with the flag, the path is customer, then invoice. Without it, the invoice
     only, as today.
   - A recovery (`kind: recovered`) refuses the flag: 400 on `keepRestAsCredit`.
2. **Removing a payment** (`payments.go:241-305`): the payment is read on the pool
   first. One that has credit rows — `source = 'credit'` (its `applied` row), or one an
   `overpayment` row names — takes the customer's lock before the invoice. The removal
   writes a `reversed` row of each, and refuses 409 `credit_exceeds_balance` when
   reversing an overpayment would take the balance below zero (the credit was used).
   The same path returns moved credit: when the removal raises the open amount of an
   invoice with `moved > 0`, it reverses up to `min(moved, balance)` of its `credit_note`
   rows, so the invoice never duns what the customer holds as credit. The write-off undo
   (`writeoffs.go:157`) does the same.
3. **The queue: Keep the rest as credit** (I9): `POST
   /api/v1/invoices/bank-transactions/{id}/keep-as-credit` (`invoices:payments`), body
   `{invoiceId, note?}`, the invoice defaulting to the line's suggestion.
   - Through `queueAction` (`bankqueue.go:533-572`). Judged: an exception queued
     `exceeds_open` or `invoice_settled`, else 409 `bank_transaction_not_applicable`.
   - Line, customer, `lockForNote`'s invoices descending (`:860-892`). Then
     `applyLocked`'s per-invoice checks (`:733-790`) for the invoice, the payment of
     what is open, if anything is, and an `overpayment` row of the rest naming the line.
     The line resolves `applied`, event `kept_as_credit`.
   - A dismissed surplus stays "owed back" (`owedBack`, `:261-270`).
4. **The reversal** (`bankqueue.go:1009`): `handle-reversal` gains `reverseCredits:
   [{creditId}]`, the overpayment rows of the reversed line it takes back. Line,
   customer, invoices. `credit_exceeds_balance` as in 2.
5. **A credit note's surplus** (I10):
   - `moveCreditNoteSurplus(ctx, creditNoteID)`: its own transaction, customer, then the
     original; it reads the original's open amount and writes the `credit_note` row of
     its negation, or nothing. The unique index makes a second run a no-op.
   - `issueDraft` (`inv/issue.go:296-386`) calls it after the commit, for a credit
     note, beside `storeAfterIssue` (`:386`). That covers the endpoint and the billing
     worker (`billing_worker.go:550`). A failure is logged at warn and never fails the
     issue.
   - **`invoices.credit_moves` worker** (new, `inv/credit_moves_worker.go`): every five
     minutes it runs `moveCreditNoteSurplus` for each `PendingCreditNoteMoves`. It holds
     no connection across moves, registers with the host, and has a pool-of-one test.
6. **Credit applied** (I11):
   - `POST /api/v1/invoices/{id}/apply-credit` (`invoices:payments`), body `{amount,
     paidOn?}`, `paidOn` defaulting to today.
   - It refuses 409 with `credit_note_no_payments`, `invoice_draft`, `invoice_settled`
     and `credit_exceeds_balance`, and with `credit_exceeds_open` when the amount is
     above what is open.
   - Customer, invoice. It writes a payment of `source = 'credit'`, `kind = 'payment'`,
     with the caller as its user, and the `applied` row naming it.
   - **On issue:** `InvoicesIssueRequest.useCredit` (default false). After the commit,
     `issue.go:260` runs apply-credit's transaction for the lesser of the balance and
     the gross, as the issuer. A failure is a warning on the response,
     `credit_not_applied`, never a refusal. It is not offered on the quick invoice or a
     billing run.
7. **Refunds** (I12):
   - `POST /api/v1/invoices/customers/{customerId}/credit/refunds`
     (`invoices:payments`; the path sits beside `reminder-policy`,
     `openapi/invoices.yaml:6951`).
   - The body: `refundedOn`, `amount`, `currency` (NOK only in B: 400
     `currency_unsupported`), `bankAccount` (11 digits, MOD11, else 400), `reference`,
     `note`.
   - Customer only. The refund row and its `refunded` row. 409 `credit_exceeds_balance`.
   - `POST …/credit/refunds/{refundId}/remove` with a reason: the removal triple and a
     `reversed` row.
8. **Reading:** `GET /api/v1/invoices/customers/{customerId}/credit` (`invoices:access`)
   answers the balance per currency and a page of movements, each with its source's
   number (invoice, credit note, bank line, refund).
9. **The merge and the erase** (`inv/customer_slots.go`):
   - `RepointCustomer` (`:78-110`) takes the two customers' credit locks in ascending id
     **before** `LockCustomerDocuments` (`:85`). It then re-points `customer_credits`
     and `credit_refunds`, and writes one `credit_merges` row per currency in which
     `from` held a balance. It reports `invoices.customerCredits` and
     `invoices.creditRefunds`.
   - `EraseCustomerData` (`:956-1048`) takes the customer's credit lock before
     `LockCustomerDocuments` (`:959`). It blanks the notes of both tables and reports
     the two kinds.
   - `exportCustomerData` (`:575`) adds the movements and the refunds.
   - `srv/customers/timeline_events.go:658` `mergeKindNouns` gains `customer credit` and
     `credit refund`.
10. **The events:** `contracts.CustomerCreditChanged` in
    `srv/contracts/events_invoices.go`. `PaymentReceived` (`:36-46`) gains `AmountNOK
    string \`json:"amountNok"\``; the version stays 1, since the field is additive.

### B4. B-1 Accounting: the credit fact and the customer-credit item

- **`srv/contracts/sales_journal.go`:**
  - `FactCredit = "credit"` and `FactCreditMerged = "credit_merged"`, added at the end
    (`:151-169`).
  - `JournalFact.Credit *JournalCredit`: kind, currency, amount, `AmountNOK`, the ids of
    its payment, line, document, refund and the row it reverses, and the reversed row's
    kind. A merge carries `FromCustomerID`, with `CustomerID` the survivor.
    `InvoiceID` is 0 on a refund and a merge.
  - `CreditsAt(ctx, day, afterCustomerID int32, limit) (CreditPage, error)`, beside
    `OpenAt` (`:211-249`, `:262-270`). It answers the customers whose balance at the end
    of the day is not zero, with `AsOfSeq` by `OpenAt`'s rule. The `OpenAt` comment loses
    "below zero is credit".
- **Migration** (the same `00197` file, `accounting` schema):
  - `accounting.open_items` gains `customer_credit boolean NOT NULL DEFAULT false`,
    with `ux_open_items_customer_credit (customer_id) WHERE customer_credit`.
  - `ck_open_items_unallocated` excludes it.
  - `ck_open_items_opening_seq` (`mig/00181:223`) allows `opening_as_of_seq` on a
    credit item.
- **Posting** (`srv/accounting/posting.go:139`): `OpenItemSpec.CustomerCredit`, found or
  made as the unallocated item is (`:575-579`, `EnsureCustomerCreditItem`).
- **The rules** (`srv/accounting/sales.go:36-56`, `factVoucher` `:357-490`):
  - `sales.credit_overpayment`: debit `bankLine` (the line's account, else the bank
    role), credit the credit item.
  - `sales.credit_note_moved`: debit the invoice's item, credit the credit item. It is
    zero on 1500.
  - `sales.credit_applied`: debit the credit item, credit the invoice's item.
  - `sales.credit_refunded`: debit the credit item, credit the ledger account of the
    refund's `bank_account` (`bankLedgers`, `:103-106`), else the bank role.
  - `sales.credit_reversed`: `turnBack` (`:527`) of the reversed row's stored voucher.
  - `sales.credit_merged`: debit the absorbed customer's credit item, credit the
    survivor's.
  - A `FactPayment` or `FactPaymentRemoved` of `Source == "credit"` returns a nil
    voucher. `postSource` skips it (`srv/accounting/catchup.go:455-459`), so the money
    moves once, by the `applied` row.
  - `contracts.PaymentSourceCredit` is added (`sales_journal.go:203-209`).
  - Version 1, except the two with a bank side, which take `salesRulePaymentVersion`.
- **`factInBooks`** (`sales.go:316-335`): for a credit fact without an invoice, it reads
  the credit item's `opening_as_of_seq`. The blocker in `postSource` (`catchup.go:470`)
  keys a refund's and a merge's facts on the customer.
- **The opening** (`srv/accounting/opening.go:38-64`): `creditsAtSnapshot` beside
  `openAtSnapshot`. The opening offers each customer's credit as a credit line on its
  credit item with the page's `AsOfSeq`, and `AccountingOpening.openCredits` carries it.
- **Settlements:** a credit item is refused `settlement_source_document`, as a
  document's is (`srv/accounting/bank_match.go:930`, and the open-items settlement).
- **Catch-up:** a handler on `invoices.customer_credit.changed` prompts it, beside the
  three on `invoices.document.issued`, `invoices.payment.received` and
  `invoices.payment.removed`.
- **Aged receivables** (`srv/accounting/reports.go:317-420`): the credit item has no due
  date, so it ages as not due and shows negative. It is labelled *Customer credit*.
- `openapi/accounting.yaml`: `AccountingOpenItem.customerCredit`,
  `AccountingOpening.openCredits`, and the report rows' label.

### B5. B-1 OpenAPI, generation and frontend

- **`openapi/invoices.yaml`:**
  - `keepRestAsCredit`, `useCredit`, the four credit operations and `keep-as-credit`,
    each with `x-vantigo-access` by §8;
  - `reverseCredits`;
  - `InvoicesBankTransactionApplied.kind` gains `credit`;
  - `refundDue` removed;
  - the new codes;
  - the permission catalog's `invoices:payments` description (`:22`) names credit and
    refunds.
- **Generation:** run `go generate ./...` in `apps/server` and `bun run gen:client` in
  both apps. Commit `fe/api-schema.d.ts` and Accounting's.
- **`fe/`:**
  - `pages/invoice.tsx`: a banner, *The customer has NOK x in credit*, with **Use
    credit**, when the balance and the open amount are both above zero. Its dialog is
    `-apply-credit-modal.tsx`.
  - `-register-payment-modal.tsx`: when the amount exceeds what is open, a checkbox,
    **Keep the rest as credit**.
  - `-issue-modal.tsx`: **Use the customer's credit**, off, shown when the balance is
    above zero.
  - `-remove-payment-modal.tsx`: says that the credit goes back.
  - `components/bank-transaction-table.tsx` `actionsOf` (`:48`): `keep` for
    `exceeds_open` and `invoice_settled`, with `-keep-as-credit-dialog.tsx`. Its
    applied list shows `credit` rows.
  - `-reversal-dialog.tsx` names the line's credit rows.
  - A new `components/customer-credit-card.tsx` in `customer-invoices-panel.tsx`
    (`:99`): the balance, the movements and **Record refund** with its removal.
  - `api/credit.ts`.
  - i18n `en` and `nb`. `bun run translations:check` passes.
  - Tests: the banner, the keep-rest flow, the queue action, the refund and its
    refusal.
- **`apps/accounting/frontend`:** `pages/open-items.tsx` and `pages/reports.tsx` label
  the credit item; `pages/opening.tsx` lists the open credits.

### B6. B-1 tests

- **Integration** (`inv/*_test.go`, against Postgres):
  - every new code;
  - each trigger: the immutability of credits and refunds, the merge's and the erase's
    allowed writes, the `amount_nok` guards;
  - the `ck_payments_origin` branch;
  - the migration's backfill on a fixture with a negative invoice;
  - `settled_amount` with `moved`.
- **Arithmetic:** a credit note after payment, then the payment removed (the moved
  credit returns); a line with a payment and an overpayment, `unappliedOf` 0.
- **The lock order:** one lock-order test per path in B3 through `lockTaken`. One races
  test per pair (`credit_races_test.go`, the `payment_races_test.go` helpers):
  - apply-credit against a refund;
  - keep-as-credit against a merge;
  - a merge against apply-credit, which is the deadlock B: readings 1 removes;
  - the credit-note move against a payment removal;
  - the erase against a refund's note.
- **Accounting:**
  - each rule's voucher;
  - a source-`credit` payment posts nothing;
  - the opening with credits;
  - `OpenAt` plus `CreditsAt` equal to 1500's balance;
  - a merge's move.
- **Sales journal:** `sales_journal_test.go` gains the credit facts, `AmountNOK` from
  the column, and a refund's fact without an invoice.

### B7. B-1 seed, documentation, assistant, site, gates

- **Seed** (`apps/server/cmd/vantigo/seed.go`, `seedInvoicesReceivables` `:1536`): one
  customer with an overpayment by hand, a queue line kept as credit, credit applied to
  a later invoice, and a refund.
- **Docs** (AGENTS.md; `nb` translated page for page):
  - `{en,nb}/user/invoices.md`:
    - a new *The customer's credit* after *Registering payments* (`:1095`);
    - *Registering payments*: keep the rest;
    - *Crediting an invoice* (`:1009`): the surplus moves to the customer;
    - *The exception queue* (`:1336`): **Keep the rest as credit**;
    - *Issuing* (`:888`): **Use the customer's credit**.
  - `{en,nb}/user/customers.md` (`:103`, the Invoices tab): the credit card.
  - `en/reference/invoices.md`:
    - the model table: `customer_credits`, `credit_refunds`, `credit_merges`,
      `payments.amount_nok` and `source`, `write_offs.amount_nok`;
    - *Payments and the state of an invoice* (`:2254`): the open formula with `moved`,
      `refundDue` gone;
    - a new *Customer credit* section: the rules, the codes, the lock order and the
      worker;
    - *Credit notes* (`:2180`);
    - *Bank files and the exception queue* (`:3257`): the arithmetic in NOK;
    - *What Invoices reports to other modules* (`:5805`): `FactCredit` and `CreditsAt`;
    - *Retention and personal data* (`:5585`), *Permissions*, *Endpoints*, *Events*,
      *Stats*, and *What comes next*;
    - `sources` gains the migration.
  - `en/reference/accounting.md`:
    - the `open_items` row (`:189`);
    - *Open items and settlements* (`:926-940`);
    - the opening rule (`:920-925`);
    - the sales rules;
    - the customers report (`:1793`).
  - `{en,nb}/user/accounting.md`: *How sales arrive from Invoices* (`:312`), *Open
    items* (`:689`), *Opening balances* (`:283`).
  - `en/contributing/module-boundaries.md`: the merge and erase kinds (`:2063-2072`),
    and Accounting's catch-up handlers (`:1363`).
  - `ROADMAP.md` (`:1780`, *Delivery B*): B-1 done.
  - Run `go generate ./...` after the docs.
- **Assistant:** `fe/assist-targets.json` gains `invoices.invoice.credit`,
  `invoices.queue.keepAsCredit` and `invoices.customer.credit`, in `en` and `nb`. Bump
  `promptVersion` (`srv/assistant/prompt.go:15`). There are no file endpoints, so
  `assistant_test.go`'s `filesOrRedirects` and `everyModule()` are untouched.
- **The public site:** customer credit is something a customer is sold on (B: readings
  9). In `vantigo-landing`, `web/src/apps/invoices.ts`, en and nb, add one line: an
  overpayment is kept as the customer's credit, used on the next invoice or refunded.
  Make it a PR there, linked from this one.
- **Gates:** A10's. The commits are `feat(invoices): …`, `feat(accounting): …` and
  `feat(invoices-ui): …`.
- **Review:** an independent review before the PR leaves draft. It checks, in
  particular:
  - the `amount_nok` trigger dance;
  - every path's lock order against I17a and B: readings 1;
  - `credit_exceeds_balance` under the lock;
  - the money moving once for applied credit;
  - the merge's ledger move;
  - the follow-up's idempotency.

### B8. B-2 migration (next free, `00198_invoices_split_rounding` if nothing lands first)

- **`invoices.bank_transactions.remittance_refs varchar(35)[] NOT NULL DEFAULT '{}'`.**
  These are every `RmtInf/Strd/CdtrRefInf/Ref`, of any type, in order, at most 20.
  Existing lines keep `'{}'` and fall back to their text. The column is not part of the
  fingerprint (`inv/bankfile/fingerprint.go:37`), so duplicate detection is unchanged.
- **`ck_bank_transaction_events_event`** gains `auto_split`.
- **`invoices.settings.tolerance numeric(5,2) NOT NULL DEFAULT 0`**, with
  `ck_settings_tolerance CHECK (tolerance BETWEEN 0 AND 100)`.
- **`invoices.write_offs`:**
  - `ADD COLUMN source varchar(10) NOT NULL DEFAULT 'manual'`, `ck_write_offs_source IN
    ('manual', 'auto')`;
  - `registered_by_user_id DROP NOT NULL` (`mig/00182:22`);
  - `ck_write_offs_actor CHECK ((source = 'manual') = (registered_by_user_id IS NOT
    NULL))`, and `ck_write_offs_auto CHECK (source = 'manual' OR reason = 'rounding')`.
- **`invoices.loss_written_off_amount(bigint)`**: the standing write-off whose reason is
  not `rounding`. Every `invoice_state(...)` call (11, in `invoices.sql`,
  `receivables.sql`, `stats.sql`, `overdue.sql`, `overdue_events.sql`) passes it in
  place of `pd.written_off`; the lateral joins of `attention.sql` and `bankqueue.sql`
  likewise. `invoice_state()` itself (`mig/00182:118-130`) is unchanged.

### B9. B-2 server

- **Every reference kept** (`inv/bankfile/camt054.go:775-803`): `details` keeps its
  KID rule — the first SCOR reference that fits, the first too-long one leading the
  text — and also collects every `Strd` reference into `Transaction.References`
  (`bankfile.go:78`). The import's insert writes `remittance_refs`. The bank feed has
  text only (`bank_feed_sync.go:480`).
- **`inv/remittance.go`**, `referencesOf(line, series)`, pure:
  - every token of the refs, the text and the KID that is a valid KID under MOD10 or
    MOD11;
  - every invoice number of the series (the settings' start to the documents counter)
    standing as a whole token — bounded by start, end, whitespace or `,;/:#`, not inside
    a longer digit run, not after a currency code (`NOK`, `kr`, `EUR`…), not before a
    decimal separator.
- **The split** (I14), in `matchLines` (`inv/matching.go:186-210`):
  - It runs before a line is queued `no_kid` or `kid_unknown` (from `classify`,
    `:276-318`), or `exceeds_open` (from `judgeMatch`, `:471-522`, in `matchLine`).
  - `trySplit(ctx, line, refs)` reads the candidates on the pool: issued NOK invoices,
    not written off, not fully credited, with something open.
  - With one customer and the open principals summing exactly to the line, it opens a
    transaction: line, invoices descending. Under the locks it judges again (the soft
    key, `paid_before_issue`, the sum), then registers one payment per invoice, oldest
    due first, with `registerMatch`'s shape (`:542-592`). The line is `matched`, event
    `auto_split`, and the deadline waiver runs for each invoice.
  - Charges are never taken.
  - The actor is the file's caller for a file and none for the feed: `ck_payments_origin`
    requires a user on `ocr` and `camt054` (`mig/00178:89-90`).
  - Any doubt queues with the original reason, the invoices found kept as the line's
    suggestions (`suggestionsFor`, `bankqueue.go:146`). That covers two customers, a sum
    that differs, and a candidate written off or credited.
- **The tolerance** (I15):
  - `settings.go` parses `tolerance` (0 to 100, two decimals, NOK), and `GetSettings`
    and `UpdateSettings` carry it, optional on PUT.
  - `roundOffResidual(ctx, txq, inv, now)` runs after every payment registration under
    the invoice's lock:
    - by hand (`payments.go:207`);
    - `registerMatch`;
    - `applyLocked` (`bankqueue.go:823`);
    - the split;
    - keep-as-credit.
  - It never runs after a Vipps capture (`InsertVippsPayment`), a recovery, or a payment
    of source `credit`.
  - When the principal left is above 0 and at most the tolerance, it writes a `rounding`
    write-off of it, `source = 'auto'`, no user, `written_off_on` the payment's day.
    The tolerance is read in the same transaction, so a change applies from then on.
  - `InsertWriteOff` takes `source`; the manual endpoint (`writeoffs.go:131`) passes
    `manual`.
- **The state** (`inv/state.go:44-67`): `documentState` takes the non-rounding amount,
  and its caller (`responses.go:217`) passes it.
  - `writtenOffOf` (`writeoffs.go:230`), which `judgeMatch` reads (`matching.go:500`),
    answers only a standing write-off that is not `rounding`. A late residual on a
    rounded-off invoice then falls through to `invoice_settled`, as I15 says, and a
    person keeps it as credit (B: readings 11).
  - The reminder rules and the attention read the state.

### B10. B-2 OpenAPI, frontend, tests, docs, gates

- **OpenAPI:** settings `tolerance`; `InvoicesWriteOff.source`, with
  `registeredByUserId` nullable; the line event `auto_split`; the line's `references`.
- **`fe/`:**
  - `pages/settings.tsx`: a **Rounding tolerance** field (NOK), with the help text
    *Amounts left this small after a payment are rounded off as a small difference.
    0 turns it off.*;
  - the write-offs card names an automatic one *Rounded off automatically*;
  - the bank file page shows `auto_split` lines with their invoices;
  - i18n;
  - tests.
- **Tests:**
  - `remittance_test.go`: the token rule against amounts (`1 250,00`), dates, account
    numbers, an invoice number inside a KID, `Fakt 1001/1002`, and `NOK 1001`;
  - the camt fixture with three `Strd` references;
  - the split's queueing cases, and a races test of a split against a payment by hand
    on one of its invoices;
  - the tolerance at 0, at the edge and above it, and a manual rounding write-off that
    reads `paid`;
  - `ck_write_offs_actor`.
- **Seed:** a tolerance of 5 NOK, a feed line naming two seed invoices in its text, and
  an invoice paid 2 kroner short.
- **Docs:**
  - `{en,nb}/user/invoices.md`: *Importing payments from the bank* (`:1184`), on one
    payment for several invoices; *Writing an invoice off* (`:1148`), on small
    differences; the settings;
  - `en/reference/invoices.md`: the split's rule and token rule, the tolerance, the
    write-off's `source` and actor, the state;
  - `en/reference/accounting.md` and `{en,nb}/user/accounting.md`: an automatic
    rounding write-off posts by `sales.write_off_rounding` to 7790 (`chart.go:231`), as
    a manual one does;
  - `ROADMAP.md`: B done.
- **Assistant:** the `invoices.settings.tolerance` target; bump `promptVersion`.
- **The public site:** a second line: one payment for several invoices is matched by its
  message, and small differences are rounded off.
- **Gates and review:** A10's. The review checks the token rule's false positives, the
  split's re-judgement under the locks, and the state change for existing rounding
  write-offs.

### B: readings against the code

These become spec amendments (revision 5).

1. **The merge's lock order would deadlock.** I17a has the merge take the customers'
   locks *after* `LockCustomerDocuments`, which locks invoices. Every credit path takes
   the customer before the invoice, so a merge holding an invoice and waiting on a
   customer, beside apply-credit holding that customer and waiting on the invoice, is a
   cycle. **Resolution:** the merge and the erase take the customer locks first, then
   `LockCustomerDocuments`. Every path is then customer before invoice. Issue is
   unaffected.
2. **A credit row needs more sources than I8 names.**
   - `reversed` needs `reverses_id`.
   - The journal fact and the invoice page need `invoice_id`.
   - A refund's day, account and reference need a table, `credit_refunds`, which §9
     omits.
3. **A refund's fact has no invoice.** `journal_facts.invoice_id` is NOT NULL
   (`mig/00180:61`), and `JournalFacts` joins the invoice. **Resolution:** nullable for
   the credit kinds only, with a LEFT JOIN.
4. **A merge breaks Accounting's per-customer item.** Accounting has no merge holder.
   Credit rows re-pointed by a merge would leave the absorbed customer's credit item
   standing. **Resolution:** `credit_merges` and the fact `FactCreditMerged`, posted as
   a move between the two items.
5. **Accounting's open-item `kind` is the party** (`customer`, `supplier`,
   `mig/00181:217-220`), so a `customer_credit` kind would break `ck_open_items_party`.
   **Resolution:** a `customer_credit` flag on a customer item, unique per customer.
6. **`OpenAt` pages by invoice number**, which a balance has none of. **Resolution:** a
   sibling `CreditsAt` with the same snapshot rule.
7. **Applied credit's two facts.** The payment of source `credit` and the `applied` row
   each emit a fact. **Resolution:** the payment's fact posts no voucher at all, not
   only no bank line.
8. **I10's retry has nothing to ride on.** `storeAfterIssue` does not retry: the next
   download stores the PDF. A credit move has no download. **Resolution:** an
   `invoices.credit_moves` worker, which §10 does not list. A payment removal, or a
   write-off undo, that reopens an invoice with moved credit reverses the move up to the
   balance. Otherwise the invoice would dun what the customer holds as credit.
9. **The public site.** §10 names no B line, but AGENTS.md's rule applies: a customer
   could be sold on customer credit and on automatic matching. Each PR adds one line.
10. **camt.054 keeps only the first KID** (`camt054.go:775-790`, `break`). "Every
    occurrence" needs `remittance_refs` and a parser change. The split's actor on a
    file is the uploader, because `ck_payments_origin` requires one; "as the system"
    holds for the feed only. "Match `auto_split`" is a line event, since a payment has
    no match column.
11. **A late residual after a rounding write-off** would queue `invoice_written_off`
    today, not `invoice_settled` as I15 says: `judgeMatch` refuses any standing
    write-off first (`matching.go:500`, `:506`). **Resolution:** `writtenOffOf` ignores
    a `rounding` write-off. `ck_payments_recovered` and the recovery stay for `loss` and
    `other`.
12. **Pointers that moved:**
    - The open arithmetic is `attempts.go:116-144` and `responses.go:214-224`, not
      `payments.go:91-99`, which only wraps it.
    - `refundDue` also feeds `invoiceRefundDue` (`attention.go:150`).
    - `reminder-policy` is at `openapi/invoices.yaml:6951`.
    - The payment event is `invoices.payment.received` (`mig/00150`), not
      `.registered`.
    - Rounding reading `paid` needs a second write-off figure
      (`loss_written_off_amount`), because `written_off_amount()` also settles the open
      amount.
13. **A manual registration with nothing open** keeps `invoice_settled`, even with
    `keepRestAsCredit`. Money for a settled invoice goes through the queue.

## Deliveries C to E

Each gets its own section here before it starts, written from the spec's decisions:

- **C:** I17–I28;
- **D:** I29–I35;
- **E:** I36–I38.
