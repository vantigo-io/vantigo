# Invoices — payments and reminders — design (Invoices phase 4)

The fifth delivery of the Invoices module, on `feat/invoices-payments-reminders`, cut from
`main` after phase 3. Research: `docs/superpowers/research/2026-10-06-invoices-payments-reminders.md`
("R4": §1 what changes the roadmap's assumptions, §2 the law, §3 the bank files, §4 Vipps
ePayment, §5 the code's seams, §6 the twenty questions it left to this design, §7 the
UNCERTAIN items, §9 the addendum on the quick invoice and kontantsalg). Module doc:
`docs/src/content/docs/en/reference/invoices.md`. The roadmap's phase is `ROADMAP.md:851-870`;
the Point of sale section it hands the counter to is `ROADMAP.md:893-933`. Phases 1B
(payments), 2 (`2026-10-03-invoices-ehf-peppol-kid-design.md`, the access-point pattern and
the KID) and 3 (`2026-10-05-invoices-work-to-invoices-design.md`) are its baseline.

**Status:** design, for the user's verdict on the readings. **Scope:** money in — the bank
tells Vantigo what was paid, Vantigo tells the customer what is late, and a Vipps request
lets a customer pay from a link or on the spot. **Sub-phases:** 4A the ledger and bank
imports, 4B overdue and reminders (pull request 1, receivables); 4C the payments port,
Vipps, the pay page and the quick invoice (pull request 2, planned after pull request 1
merges). Migrations from `00041`.

The delivery:

- **the payments ledger** grows a source (`manual`, `ocr`, `camt054`, `vipps`), an
  optional registering user that only a Vipps capture may lack, and a link to the bank
  transaction or the payment attempt it came from;
- **bank files** — OCR giro and camt.054 (`.001.02` and `.001.08`) uploaded by a person,
  checked all-or-nothing, stored once, deduplicated per file and per transaction, and
  **matched on KID** into payments, with every other line in an **exception queue** a
  person resolves;
- **reminders under dated rules** — the inkassosats, the late-interest rate and the § 3a
  compensation as dated rows; an overdue list; reminder runs previewed then made, letters
  delivered by a worker or printed in a batch; fees, compensation and interest kept apart
  from the principal; a hold for a disputed invoice; the hand-off to a collection agency
  and its CSV;
- **the payments port** in a shared server package with **Vipps MobilePay ePayment** as
  its first adapter; a **pay page** on the installation's public URL; a poll worker that
  captures and registers; optional webhooks;
- **the quick invoice** — created and issued in one transaction — and, behind the
  kontantsalg study's hard requirements, **an on-site Vipps payment** of it;
- the OpenAPI contract, the frontend, and the documentation.

Vantigo stays a sub-ledger: a payment is a registration, a reminder fee a claim recorded
beside the invoice, and nothing is posted.

`srv/` is `apps/server/internal/`, `inv/` is `srv/invoices/`, `mig/` is
`srv/db/migrations/`, `MB` is `docs/src/content/docs/en/contributing/module-boundaries.md`,
`R/` is `docs/src/content/docs/en/reference/`, `fe/` is `apps/invoices/frontend/src/`,
`host/` is `apps/host/frontend/src/`. Line numbers are against `b2a8da28` (the code is
`0ba2840e`'s, which R4 §5 cites). Money stays as 1A has it: exact decimal inside, JSON
numbers on the wire; øre (`int64` minor units) only across the payments port, whose
providers speak them.

**Amendments to the brief.** Where the coordinator's direction proved wrong or incomplete
against R4 and the code, the design says so in place; in short:

1. **A capture is never made under the invoice's lock** (D16). The brief's "under the
   invoice's lock, capture min(authorized, open now)" would call the provider while a
   transaction holds a row lock — the one thing `withLockedTx` forbids
   (`inv/server.go:100-109`, MB rule 10's restatement). The worker decides the amount
   under the lock and **reserves** it (a reservation the open amount counts), commits,
   captures outside any lock, and registers the payment in a second locked transaction.
2. **Matching runs one bank transaction per database transaction** (D4), after the
   file's rows commit, not inside one long transaction: the lock order is the bank
   transaction, then its one invoice; a crash leaves rows `pending`, which
   `POST /invoices/bank-files/{id}/match` finishes. Only the queue's multi-invoice apply
   locks several invoices, in descending id.
3. **The webhook receiver lives under the module's mount**,
   `POST /api/v1/invoices/vipps/webhooks` — modules mount only at `/api/v1/<name>/`
   (`srv/module/module.go`, MB rule 5) — and needs a new router option that keeps the raw
   body (D16).
4. **`PUBLIC_BASE_URL` does not exist yet** (`srv/config/config.go` knows only `APP_URL`
   and `APP_BASE_PATH`); it is new platform configuration, https only, unset by default,
   and a trusted CSRF origin beside `APP_URL` (D14).
5. **The pay page is a public route of the host SPA** over three anonymous operations of
   the invoices contract (`x-vantigo-access: anonymous`, which the router supports,
   `srv/module/router_test.go:31`), not a server-rendered page: the server's mux has no
   module hook for pages (`srv/server/server.go:62-67`). `publicPaths`
   (`host/lib/public-paths.ts:1-9`) is a set of exact paths and gains a prefix rule (D15).
6. **Payments gain `payment_attempt_id`** (a unique foreign key) instead of
   `provider_payment_id`: the attempt row carries every provider id, and the unique key is
   the exactly-once guard (D2, D16).
7. **An auto-match covers principal, then charges outstanding** (D4): a customer who pays
   a reminder's total with the invoice's KID would otherwise always land in the queue as
   an overpayment. Only what exceeds both is queued, `exceeds_open`.
8. **`currency` is a file-level refusal, not a queue reason** (D3): the pre-checks are
   all-or-nothing per file. The queue gains `paid_before_issue`, `account_mismatch` and
   `negative_amount` (D5).
9. **One import format per installation** (`bankImportFormat`, D3): the OCR archive
   reference and camt's `AcctSvcrRef` are not known to match (R4 §7 item 29), so the same
   KID payment imported from both formats would register twice.
10. **The customer policy's modes are `normal`, `no_charges`, `none`** (D7): "no fee"
    must also stop the compensation and interest, so `no_fee` was misnamed. A merge keeps
    **the stricter** mode, not the survivor's.
11. **There is no "invoice's own address" to fall back to**: the buyer snapshot holds no
    e-mail (`mig/00034_invoices_baseline.sql:129-142`), and the contract's
    `ReminderEmail` already falls back to the invoice e-mail and the contact e-mail
    (`srv/contracts/directory.go:103-105`). With no address, a letter goes to paper, with a
    warning (D10).
12. **A letter's facts are fixed when it is sent, not when the run creates it** (D10):
    the inkassovarsel's deadline counts from sending (INKL § 9, R4 §2.6), so a letter that
    waited in the queue or for the printer must not lose days.
13. **Rates**: an unused future row can be deleted (a typo must be correctable), and a run
    that needs a half-yearly rate for a half-year with no row is refused,
    `collection_rates_outdated` (D6).
14. **The § 3a compensation continues after the new inkassolov's in-force date**: it is
    forsinkelsesrenteloven, which LOV-2026-05-22-19 does not repeal for business debtors
    (R4 §2.1 item 6 amends only the consumer rule). Only fees stop (D6, D8).
15. **`INVOICES_VIPPS_ENABLED`** (default on) is added beside the base URL — phase 2's
    operator switch, so an operator can stop every provider call — and `workers`
    (`inv/module.go:101-106`) no longer returns nothing when EHF is off (D1).
16. **The quick invoice's on-site payment is decided, not a placeholder**: the
    kontantsalg study (R4 §9) answers it — on-site payment is kontantsalg, lawful without
    a kassasystem only as a kontantfaktura, so the design builds the study's requirements
    in as hard rules (D17), and the roadmap's "this is a credit sale" is corrected (D23).
17. **The overdue list is readable under `invoices:access`** (D12) — it shows nothing a
    reader of the invoices and their payments cannot already see — while every action on
    it is `invoices:payments`. **An on-site payment request is `invoices:issue`**, as the
    quick invoice is (D1).
18. **Reminder and payment settings live in their own one-row tables and endpoints**,
    not on `invoices.settings`, which every issue reads `FOR SHARE` and whose `PUT`
    requires every field (D7, D14) — phase 2's credentials precedent.

## Decisions

### D1 — Scope, phasing, authority and the switches

**No new permission.** Checked against the reference's permission table
(`R/invoices.md:1985-2034`) and `inv/module.go:39-65`:

| Key | Gains in phase 4 |
| --- | --- |
| `invoices:access` | the overdue list, an invoice's reminders, hold, hand-off and charges, the collection rates, the day's Vipps payments, the attention items about overdue invoices |
| `invoices:payments` | bank imports, the exception queue and its actions, reminder runs, printing, withdrawing and retrying letters, holds, the hand-off and the collection export, charge payments, the per-customer reminder policy, the attention items about the queue and failed letters and captures |
| `invoices:manage` | the reminder settings, adding or deleting a future collection rate, the payment settings (bank-file format, pay links, on-site payment and its acknowledgement), the Vipps credentials and webhook |
| `invoices:issue` | an on-site payment request on an issued invoice |
| `invoices:create` + `invoices:issue` | the quick invoice (`permission:invoices:access+invoices:create+invoices:issue`, the three-key grammar `openapi/customers.yaml:4398` already uses) |
| anonymous | the pay page's three operations and the webhook receiver, each rate-limited |

Why reminders are not `invoices:issue`: a reminder is not a sales document and takes no
number (R4 §1.2 item 7); it is credit control — "what the company says it is owed", the
very reason `invoices:payments` is sensitive (`R/invoices.md:1997-1998`). Why the on-site
request is `invoices:issue`: it is part of handing the sale over by whoever issued it, and
it registers nothing by hand — the capture does. The permission descriptions in
`inv/module.go:41-63` and the reference's table are rewritten to say all this.

**The switches** (`srv/config/config.go`, by its conventions; the configuration reference):

- `PUBLIC_BASE_URL` — platform, unset by default (D14).
- `INVOICES_VIPPS_ENABLED` — default **on**; `0`: every provider call stops, meta's
  `payLinksAvailable` and `onSitePaymentsAvailable` are false, the payment workers do not
  start, a pay page answers `payments_unavailable`.
- `INVOICES_VIPPS_BASE_URL` — default `https://api.vipps.no`, validated by `httpBaseURL`
  (`config.go:516`) like the Storecove URL; the operator's, never the tenant's, so an
  `invoices:manage` holder cannot aim the client elsewhere. The test environment is
  `https://apitest.vipps.no` (R4 §4.8).

`workers` (`inv/module.go:101-106`) becomes a list built per switch: the two EHF workers
when `InvoicesEhfEnabled`; `invoices-reminders` (D10) always; `invoices-payments` (D16)
when `InvoicesVippsEnabled`.

**Meta** (`GET /invoices/meta`) grows `bankImportFormat`, `remindersEnabled`,
`payLinksAvailable`, `onSitePaymentsAvailable`, `vippsCredentialsRejected`, and
capabilities `canImportBankFiles`, `canRunReminders` (both `invoices:payments`),
`canTakeOnSitePayment` (`invoices:issue` and `onSitePaymentsAvailable`),
`canQuickInvoice` (`invoices:create` and `invoices:issue`).

### D2 — The payments ledger: a source, an optional user, the origin

`00041` alters `invoices.payments` (`mig/00035_invoices_payments_delivery.sql:13-33`):

```sql
ALTER TABLE invoices.payments
    ADD COLUMN source varchar(10) NOT NULL DEFAULT 'manual',
    ADD COLUMN bank_transaction_id bigint REFERENCES invoices.bank_transactions (id) ON DELETE RESTRICT,
    ALTER COLUMN registered_by_user_id DROP NOT NULL,
    ADD CONSTRAINT ck_payments_source CHECK (source IN ('manual', 'ocr', 'camt054')),
    ADD CONSTRAINT ck_payments_origin CHECK (
        (source = 'manual' AND bank_transaction_id IS NULL AND registered_by_user_id IS NOT NULL)
        OR (source IN ('ocr', 'camt054') AND bank_transaction_id IS NOT NULL AND registered_by_user_id IS NOT NULL));
CREATE INDEX ix_payments_bank_transaction ON invoices.payments (bank_transaction_id)
    WHERE bank_transaction_id IS NOT NULL;
```

Existing rows take `manual`. `00042` (4C) adds `payment_attempt_id bigint UNIQUE
REFERENCES invoices.payment_attempts (id) ON DELETE RESTRICT` and replaces both CHECKs:
`source IN ('manual','ocr','camt054','vipps')`, and the third branch `source = 'vipps' AND
payment_attempt_id IS NOT NULL AND bank_transaction_id IS NULL AND registered_by_user_id IS
NULL` — **only a Vipps capture lacks a user**. A bank import is uploaded by a person and
its auto-match runs in that request, so the uploader is the registering user; the queue's
apply is its caller's. The nullable-user precedent is `transmissions.resolved_by_user_id`
(`mig/00036_invoices_ehf_kid.sql:92,104`).

`tr_payments_immutable` (`mig/00035…:67-89`) needs no change: it compares `to_jsonb(OLD)`
less the removal columns and the note, so every new column is frozen with the rest, and
`ALTER … DROP NOT NULL` is DDL, which it does not see (R4 §5.1). `bank_transaction_id` is
**not unique**: one bank line may be applied to several invoices (D5); `payment_attempt_id`
is, one capture being one payment.

**The open amount** stays principal-only: `openOf` (`inv/payments.go:93-108`) is gross −
credited − live payments − **live reservations** (D16: the Vipps captures decided and not
yet registered). A manual registration over a reservation is refused
`payment_exceeds_open`, its detail saying a Vipps payment of that amount is being
captured. `document_state` (`mig/00035…:190-200`) is unchanged: it sees the paid sum, and
a reservation is not a payment.

**The registration and removal endpoints keep their rules** (`R/invoices.md:1054-1094`):
a manual payment is `source = manual`. A removal of an imported or Vipps payment is
allowed and is how a reversal or a refund made outside Vantigo is recorded (D5); it never
touches the bank transaction or the attempt — the transaction's applied amount is derived
from its live payments, so the lock order of D18 holds.

**The response**: each payment answers `source`, and `bankTransactionId` or
`paymentAttemptId`; `registeredBy` is absent on a Vipps payment.

**Tests**: `TestPayments_SourceCheck` (each invalid origin refused by the CHECK, a manual
row without a user refused, a Vipps row with a user refused — after `00042`);
`TestPayments_ExistingRowsAreManual` (a migration test over a 1B fixture);
`TestPayments_NewColumnsFrozen` (the trigger refuses an UPDATE of each new column);
`TestPayments_ReservationCountsInOpen` (D16's reservation lowers `openAmount` and refuses a
manual payment of the whole open amount).

### D3 — Bank files: two formats, the pre-checks, stored once, imported once

**`POST /invoices/bank-files`** (`invoices:access+invoices:payments`): multipart, one part
named `file`, at most **10 MiB** (`BodyLimits`, the customers import's pattern,
`srv/customers/import.go:92-150`). In order:

1. 400 on `file` — a missing, second or empty part, or one past the limit.
2. **Detect the format**: the first non-blank line, CR/LF stripped, is 80 characters and
   begins `NY000010` → **OCR giro**; the document's root is `Document` in the namespace
   `urn:iso:std:iso:20022:tech:xsd:camt.054.001.02` or `…001.08` → **camt.054**; anything
   else → 400 on `file`, "Not an OCR giro or camt.054 file" (other camt versions named).
3. 409 `bank_import_format_mismatch` when the settings' `bankImportFormat` (D14's payment
   settings) is set and differs. **Unset, the first successful import sets it** — recorded
   in the same transaction as the file — so the common case needs no settings visit;
   changing it is `invoices:manage`'s, on the settings card, with the warning that the
   same payment in both formats would register twice.
4. **Parse and pre-check, all or nothing** (R4 §3.1, §3.2, §3.6); any failure is a 400 on
   `file` naming the record or element and nothing is stored:
   - **OCR**: every record exactly 80 characters, starting `NY`; the grammar `10 (20 (30 31
     [32])+ 88)+ 89`; items 2 and 3 share item 1's transaction number and type, item 3
     iff type 20 or 21; per assignment and per transmission the transaction count, the
     record count (start and end records included), the **signed** amount sum (a credit
     note's `-` subtracts; a type 18/20 reversal adds, as the specification says) and
     the first and last settlement date equal the `88` and `89` fields; service code 09
     only (another service is a 400 — an open choice, taken strictly); DDMMYY with the
     century window 2000-2099; KID characters digits, a trailing `-` allowed (MOD11).
     Item-3 text is decoded as ISO-8859-1 (R4 §7 item 20), leniently.
   - **camt.054**: well-formed XML through `encoding/xml` with both namespaces' paths
     (R4 §3.2's table: `Ntry/Sts` vs `Ntry/Sts/Cd`, `Dbtr/Nm` vs `Dbtr/Pty/Nm`); per entry
     the `TxDtls` amounts sum to `Ntry/Amt` and their count equals `Btch/NbOfTxs` when
     present, and `TxsSummry` agrees when present; every `Ccy` is `NOK` (else 400 —
     **currency is a file-level refusal**, research case m); amounts at most two
     decimals. No runtime XSD validation (R4 §3.7): the vendored XSDs are a test oracle
     (`xmllint --schema` over every fixture, `mise run bankfiles:validate`).
   - **Both**: every booking date on or before today (Oslo, the request's one clock read)
     and not before 2000-01-01; at most 5 000 transactions in a file (the module's export
     cap), since each is matched in a transaction of its own.
5. **The account** (case m): every OCR `Oppdragskonto`, every camt `Ntfctn/Acct` (a NO IBAN
   normalised to its 11-digit BBAN) must be the seller's `bank_account`
   (`mig/00034…:30`) **or** a `seller_bank_account` some issued invoice snapshotted
   (`:153`) — an account the seller used to invoice to. Otherwise 409
   `bank_account_unknown` naming the account's last four digits.
6. **The file, twice** (R4 §3.6): its SHA-256 and its own identity — OCR `(Dataavsender,
   Forsendelsesnummer, Datamottaker)` of `NY000010`, camt `(MsgId, CreDtTm)` — are each
   unique; either already imported → 409 **`bank_file_duplicate`** with `bankFileId`,
   `uploadedAt` and `uploadedBy` of the earlier one. Read first on the pool for the quick
   answer, enforced by the unique indexes in step 8.
7. **Stored once**: the bytes under `bank-files/<sha256>.<ocr|xml>` (`Exists` before
   `Put`, the PDF's `storeOnce` shape, `inv/pdfstore.go:268-306`), outside any
   transaction; 503 `storage_unavailable` without an object store. The file is the
   documentation of the payments booked from it (bokføringsloven § 10), kept like the
   PDFs. An object stored by a request that then fails is harmless: the next upload of
   the same bytes finds it.
8. **One transaction** (no row lock taken but the inserts'): the file row, then every
   transaction row **in one `INSERT … ON CONFLICT (account, fingerprint) DO NOTHING`
   ordered by fingerprint**, so two overlapping imports wait on the index in the same
   order and never deadlock (phase 3's `line_sources` rule, reading 33); the rows the
   conflict skipped are counted as `skippedKnown`. A unique violation on the file's hash
   or identity maps to `bank_file_duplicate`.
9. **Matching** (D4), one transaction per bank transaction, after the commit.
10. **201** with the import's result: the file row, `transactions`, `matched` and
    `matchedAmount`, `exceptions` and `exceptionsAmount`, `skippedKnown`, `ignored` (by
    kind: `debit`, `not_booked`, `card_information`), and `pending` (non-zero only when
    matching stopped early).

**What becomes a transaction.** OCR: every amount item of types 10–17 (the giro kinds);
types 18–21 (card information) are **ignored and counted** — Vantigo has no terminal
agreement (R4 §3.1); a negative (`-`) line is a transaction with `direction = credit` and
a negative sign recorded, queued `negative_amount`. camt: every `TxDtls` of a `BOOK`ed
`CRDT` entry (an entry without `TxDtls` is one transaction of its own amount); a `DBIT`
entry is stored only when it is a reversal — `RvslInd = true` or the bank code
`PMNT/ICDT-RCDT/RRTN` (R4 §3.2) — as `direction = debit`, queued `reversal`; any other
`DBIT` and any entry not `BOOK`ed is ignored and counted.

**The fingerprint** (R4 §3.6's proposal): SHA-256 over the receiving account, the booking
(OCR: settlement) date, the amount in øre with its sign, the KID or the normalised text
(trimmed, case-folded, whitespace collapsed), the debtor account if present, the archive
reference if present (OCR item 2's `Arkivreferanse`; camt `TxDtls/Refs/AcctSvcrRef`), and
an **ordinal** — the n-th line with all the rest identical within the same file. The same
file, or a file overlapping it, reproduces the ordinals and is skipped; two identical
payments in one file stay two. Two identical payments of one day split across two files
without a reference would collapse into one — R4's residual risk, which the import result
shows (`skippedKnown`, listed by line) for a person to see; the alternative, no ordinal and
no dedupe, registers every overlapping file twice.

**Schema** (`00041`):

```text
invoices.bank_files
  id bigint identity (START WITH 1001) PK, format varchar(10) CHECK (format IN ('ocr','camt054')),
  sha256 char(64) NOT NULL UNIQUE, file_identity varchar(200) NOT NULL,   -- OCR "sender:transmission:recipient", camt "MsgId|CreDtTm"
  object_key varchar(300) NOT NULL, byte_size integer NOT NULL,
  accounts varchar(11)[] NOT NULL, first_booked_on date, last_booked_on date,
  transactions integer, skipped_known integer, ignored integer,
  uploaded_by_user_id uuid NOT NULL, uploaded_at timestamptz NOT NULL,
  UNIQUE (format, file_identity)
invoices.bank_transactions
  id bigint identity PK, bank_file_id bigint NOT NULL REFERENCES invoices.bank_files ON DELETE RESTRICT,
  line_ref varchar(60) NOT NULL,                 -- OCR "assignment/transaction", camt "notification/entry/tx"
  account varchar(11) NOT NULL, direction varchar(6) CHECK (direction IN ('credit','debit')),
  negative boolean NOT NULL DEFAULT false,       -- an OCR "-" line
  booked_on date NOT NULL, value_on date, ordered_on date,   -- ordered_on: OCR Oppdragsdato (R4 §2.7)
  amount numeric(14,2) NOT NULL CHECK (amount > 0), currency char(3) NOT NULL CHECK (currency = 'NOK'),
  kid varchar(25), remittance_text varchar(1000) NOT NULL DEFAULT '',
  debtor_name varchar(140) NOT NULL DEFAULT '', debtor_account varchar(34) NOT NULL DEFAULT '',
  archive_ref varchar(35) NOT NULL DEFAULT '', bank_code varchar(35) NOT NULL DEFAULT '',
  fingerprint char(64) NOT NULL, ordinal smallint NOT NULL,
  status varchar(10) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','matched','exception','resolved')),
  reason varchar(30),                              -- D5's codes; set exactly when status = 'exception'
  suggested_invoice_id bigint,                     -- the unambiguous suggestion of D5, no FK (a hint)
  resolution varchar(25) CHECK (resolution IN ('applied','not_customer_payment','reversal_handled')),
  resolved_by_user_id uuid, resolved_at timestamptz, resolution_note varchar(500) NOT NULL DEFAULT '',
  CONSTRAINT ck_bank_transactions_state CHECK (
      (status = 'exception') = (reason IS NOT NULL)
      AND (status = 'resolved') = (resolution IS NOT NULL AND resolved_at IS NOT NULL)),
  UNIQUE (account, fingerprint)
  INDEX ix_bank_transactions_open (status) WHERE status IN ('pending','exception')
  INDEX ix_bank_transactions_file (bank_file_id)
```

`bank_files` is never updated or deleted (a trigger, the payments' shape).
`bank_transactions` refuses DELETE and every UPDATE but its state columns — `status`,
`reason`, `suggested_invoice_id`, `resolution`, `resolved_by_user_id`, `resolved_at`,
`resolution_note` — comparing `to_jsonb` less those, as `refuse_payment_change` does, and
blanks nothing on anonymisation (D19). `matched` and `resolved` never go back to `pending`.

**`POST /invoices/bank-files/{id}/match`** (`invoices:payments`): runs D4 over the file's
`pending` rows — 200 with the same counts; 404. A file whose request died mid-match shows
`pending > 0` in `GET /invoices/bank-files` and `GET /invoices/bank-files/{id}`
(`invoices:payments`; the list paged newest first, the one with its transactions), and
the screen offers "Match the rest".

**Tests**: `TestBankFileOCR_Parse` over R4 §3.1's constructed example and fixtures for
every rule (a 79-character record, a wrong grammar, a wrong count, a wrong sum, a negative
line netted, a reversal type added, service 21, a bad date, a `-` KID); `TestBankFileCamt_Parse`
over both namespaces, R4 §3.2's example, our own Danske-shaped fixture (the same entry
`AcctSvcrRef` twice, no transaction reference, `NTAV`, the KID keyed on `SCOR` not on the
bank code), a `DBIT` reversal, a non-`BOOK` entry, an entry without `TxDtls`, a sum
mismatch, a USD amount; `TestBankFile_XSDOracle` (tagged `bankfiles`, the vendored XSDs
with their NOTICE line, R4 §3.7); `TestBankFile_Detect`; `TestBankFile_AccountUnknown`
(the settings' account, an old snapshotted account, a stranger, an IBAN normalised);
`TestBankFile_Duplicate` (same bytes; same identity, new bytes; the 409's fields);
`TestBankFile_FormatMismatch` and `…_FirstImportSetsFormat`; `TestBankFile_Fingerprint`
(two identical lines in one file both kept, the same file again all skipped, an overlap
counted); `TestBankFile_StoredOnce` and `…_StorageUnavailable`; `TestBankFile_Immutable`.
**Race**: `TestBankImport_TwoOverlappingImports` (`srv/integration`-style, a pool of
`MaxConns = 2` for the two racing imports, the first held after its inserts on a seam
`bankImportAfterInsert`): both finish, every shared line registered once, no `40P01`,
`pg_stat_database.deadlocks` still 0.

### D4 — Matching on KID, and what a match registers

**`kid.Parse`** (new, `inv/kid/kid.go`, a leaf still): `Parse(s string) (number int64,
ok bool)` — 2 to 25 characters, every one but the last a digit, the last a digit or `-`;
`number` is the body without its check character, leading zeros dropped. It does not
judge the check digit; `Verify` (`kid.go:96-113`) does, against an algorithm.

**Classifying a credit line**, in this order, each read on the pool (R4 §3.5):

1. `direction = debit` → `reversal`; `negative` → `negative_amount`.
2. The text (`remittance_text` or camt `AddtlNtryInf`) matches `Vippsnr \d+` and the line
   has no KID → `vipps_payout` (R4 §4.9; never invoice-matched — the invoices were settled
   at capture).
3. No KID → `no_kid`.
4. `kid.Parse` fails, or its check character verifies under neither MOD10 nor MOD11 →
   `kid_invalid`.
5. Look up the issued document by `number` (`ux_invoices_number`, `mig/00034…:195`) and
   require its stored `kid` to equal the line's KID **exactly** and `kid.Verify(kid,
   kid_algorithm, number)` (R4 §5.2: no new index; a credit note's KID is NULL) — none →
   `kid_unknown` (a KID from another agreement or an older system).
6. The invoice's `seller_bank_account` differs from the line's account →
   `account_mismatch` (the payment reached an account this invoice did not name).
7. Otherwise the line is a candidate for that invoice: matched under its lock (below).

**Under the lock**, one READ COMMITTED transaction per bank transaction: **the bank
transaction `FOR UPDATE`** (still `pending`, else nothing to do — another match took it),
then **the invoice `FOR UPDATE`** (`LockInvoice`), then every figure after it (the
payments' rule, `inv/payments.go:16-24`). With `open` = gross − credited − paid −
reserved and `charges` = the charges outstanding (D9):

| Case (R4 §3.5) | Judged | Result |
| --- | --- | --- |
| c | `credited > 0` and `credited ≥ gross` | queued `invoice_credited` |
| — | `booked_on` before the issue date | queued `paid_before_issue` (only a backdated issue could allow it; never auto-posted — R4 §2.7's "sane dates") |
| d, e | `0 < amount ≤ open` | a payment of `amount` |
| — | `open > 0`, `open < amount ≤ open + charges` | a payment of `open` and a charge payment of the rest (D9) |
| — | `open ≤ 0`, `0 < amount ≤ charges` | a charge payment of `amount` |
| g, h | `open ≤ 0` and `amount > charges` | queued `invoice_settled` |
| f | `amount > open + charges` | queued `exceeds_open`, with `openAmount` and `chargesOutstanding` on the row's read |

A payment and a charge payment registered here are `source` the file's format,
`bank_transaction_id` the line, `paid_on` **the booking date** (OCR settlement date, camt
`BookgDt`), `reference` the KID, `registered_by_user_id` the uploader,
`registered_at` the request's one clock read; then the line becomes `matched`. **The
allocation is principal first**, then charges (reading 6): the order is recorded — a
payment row and a charge payment row — so it can be explained (new inkassolov § 16, R4
§2.7). A handed-off or held invoice is matched like any other: payments are always
registered (D11).

A match is never refused by `tr_payments_parent` (the invoice is issued) and blanks the
note of an anonymised customer's payment as today; the KID is a reference, not a note.

**Tests**: `TestKidParse` (MOD10 and MOD11 bodies, the `-`, leading zeros, letters, too
short, too long); `TestMatch_Classify` (each step by removing its guard, in order);
`TestMatch_Cases` (each row of the table, amounts at the boundaries — exactly open,
exactly open + charges, one øre over); `TestMatch_PaidOnIsBookingDate`;
`TestMatch_PrincipalThenCharges`; `TestMatch_HeldAndHandedOffStillMatch`;
`TestMatch_PendingFinishedByMatchEndpoint` (a seam stops after the first line).
**Race**: `TestBankImport_RacesManualPayment` — an import's match and a manual
registration of the whole open amount on the same invoice, the manual one held after its
lock (`paymentAfterLock`, `inv/payments.go:40`): the import waits, then queues
`invoice_settled`; reversed, the manual one is refused `invoice_settled`; never two
payments; no `40P01`.

### D5 — The exception queue

Every line that is not matched is `exception` with one **reason**:

| Reason | Case | What the person usually does |
| --- | --- | --- |
| `kid_invalid` | a | apply by hand, or dismiss |
| `kid_unknown` | b | apply, or dismiss (another system's KID) |
| `invoice_credited` | c | dismiss with a note — a refund is owed and made outside Vantigo |
| `invoice_settled` | g, h | the same |
| `exceeds_open` | f | apply part to the invoice (and its charges), the rest stays unapplied |
| `no_kid` | i, j | apply to one or several invoices from the suggestions |
| `negative_amount` | k | dismiss with a note |
| `reversal` | l | remove the payment it reverses, then mark it handled |
| `vipps_payout` | n | dismiss — "not a customer payment"; the Vipps payments were registered at capture |
| `paid_before_issue` | — | apply after checking, or dismiss |
| `account_mismatch` | — | apply after checking, or dismiss |

**Suggestions** for `no_kid`, `kid_invalid` and `kid_unknown` (read on `GET`, never
auto-posted — R4 §3.5 j): an issued invoice number found as a whole word in the text; an
invoice whose open amount equals the line's amount; the invoices of the customer whose
earlier matched payments came from the same debtor account. Each suggestion names why.
When exactly one invoice is suggested by the first or the third rule,
`suggested_invoice_id` holds it for the list's column.

**Endpoints** (`invoices:access+invoices:payments`):

- `GET /invoices/bank-transactions?status=&reason=&bankFileId=&from=&to=` — paged, oldest
  open first; each line with its file, the KID or text, the debtor, the amounts applied
  (live payments and charge payments referring to it) and `unappliedAmount`, its
  suggestions when open.
- `POST /invoices/bank-transactions/{id}/apply` `{allocations: [{invoiceId, amount,
  chargesAmount?}], note?}` — 1 to 20 allocations, each invoice once, amounts above 0
  with two decimals. Order: 400 on the fields; 404; 409 `bank_transaction_not_open` (not
  `exception`); `reversal` and `negative_amount` lines refuse apply, 409
  `bank_transaction_not_applicable`. Then **one transaction: the line `FOR UPDATE`, then
  the invoices `FOR UPDATE` in descending id** (the module's invariant, `R/invoices.md:950-957`),
  and per invoice, after its lock: kind invoice and issued, else 409
  `allocation_not_an_invoice`; `amount ≤ open` else 409 `payment_exceeds_open` (with
  `invoiceId`, `openAmount`); `chargesAmount ≤ charges` else 409
  `charge_payment_exceeds_outstanding`; `booked_on` before its issue date, 409
  `paid_before_issue`; and Σ (amount + chargesAmount) over the allocations ≤ the line's
  amount less what is already applied, else 409 `allocation_exceeds_transaction`. Each
  allocation writes a payment (and a charge payment), as D4 does, registered by the
  caller; the line becomes `resolved`, `applied`, with the note. **What is not applied
  stays visible** as `unappliedAmount` — no customer credit balance and no refund in
  phase 4 (reading 4).
- `POST /invoices/bank-transactions/{id}/dismiss` `{note}` (1–500 characters) — `resolved`,
  `not_customer_payment`; any reason but `reversal`. 409 `bank_transaction_not_open`.
- `POST /invoices/bank-transactions/{id}/handle-reversal` `{note}` — only a `reversal`
  line: `resolved`, `reversal_handled`. The payment it reverses is removed first through
  the existing removal (`POST /invoices/{id}/payments/{paymentId}/remove`, a reason
  naming the reversal); the screen offers the matching live payments of the same amount
  and account. Nothing links the two automatically: R4 §7 item 22.
- `POST /invoices/bank-transactions/{id}/reopen` — a `resolved` line back to `exception`
  with its original reason: refused 409 `bank_transaction_applied` while any live
  payment or charge payment refers to it (remove those first). The resolution columns are
  cleared; the history is the payments' removals and the line's `resolved_at` audit in
  the response's `events` (each resolve and reopen, who and when — a small
  `invoices.bank_transaction_events` table, insert-only, so the reopen does not erase who
  dismissed it).

**Tests**: each endpoint's refusals in order, each by removing its guard; apply across
three invoices taking locks in descending id (the lock seam records the order);
`unappliedAmount`; reopen refused with a live payment and allowed after its removal; the
events. **Race**: `TestBankQueue_ApplyRacesManualPayment` — an apply over two invoices and
a manual registration on the lower id, both finishing, the apply refused
`payment_exceeds_open` for that allocation and rolled back whole, never a partial apply.

### D6 — Collection rates as dated data, and the two regimes

`invoices.collection_rates` (`00041`):

```text
id bigint identity PK,
kind varchar(30) CHECK (kind IN ('late_interest_percent','inkassosats','b2b_compensation_nok')),
valid_from date NOT NULL, value numeric(10,2) NOT NULL CHECK (value > 0),
source_ref varchar(100) NOT NULL,            -- the regulation, e.g. "FOR-2026-06-25-1372"
created_by_user_id uuid, created_at timestamptz NOT NULL,   -- NULL for the seeded rows
UNIQUE (kind, valid_from)
```

A row is **in force from `valid_from` until the next row of its kind**. Seeded from R4
§2.11, every value read on Lovdata: `late_interest_percent` 12.50 (2024-01-01,
FOR-2023-12-14-2043), 12.50 (2024-07-01, FOR-2024-06-26-1320), 12.50 (2025-01-01,
FOR-2024-12-19-3279), 12.25 (2025-07-01, FOR-2025-06-23-1321), 12.00 (2026-01-01,
FOR-2025-12-18-2658), 12.25 (2026-07-01, FOR-2026-06-25-1372); `b2b_compensation_nok` 470,
460, 470, 460, 460, 430 on the same dates and regulations; `inkassosats` 700
(2019-01-01, FOR-2018-12-20-2050) and 750 (2026-01-01, FOR-2025-12-19-2709). No invoice
of this module predates 2026, so 2024 H1 is ample.

**Append-only**: a trigger refuses every UPDATE, and a DELETE of a seeded row
(`created_by_user_id IS NULL`). The API adds the rest:

- `GET /invoices/collection-rates` (`invoices:access`) — every row by kind and date, each
  with `inForce` today and `usable` (no letter has used it).
- `POST /invoices/collection-rates` (`invoices:access+invoices:manage`) `{kind, validFrom,
  value, sourceRef}` — `validFrom` after today (Oslo), else 400 ("a rate in force or past
  is never changed; it is how earlier letters were computed"); the half-yearly kinds on
  1 January or 1 July, else 400; bounds — interest 0.01–30, compensation 100–2 000,
  inkassosats 100–5 000 (400); a duplicate `(kind, validFrom)` → 409
  `collection_rate_exists`. 201.
- `DELETE /invoices/collection-rates/{id}` (`invoices:manage`) — only while `validFrom` is
  after today: 409 `collection_rate_in_force`; a seeded row 409 too. 204; 404.

A release also ships each half-year's values as a seed migration while Vantigo is
maintained (reading 2); the screen is for an installation that is not upgraded in time.

**Reading the rates.** The engine (D8) reads the row in force **on the letter's date**
for the inkassosats and the compensation, and **on each day** of an interest period for
the rate, splitting at every change (FRL § 3, R2). **Outdated rates refuse**: when a
letter would need a half-yearly kind and the latest row of that kind starts before the
current half-year (a 2027-01-01 with no 2027 H1 row), the run and the dispatch answer
409 **`collection_rates_outdated`** naming the kind and half-year — the late interest and
compensation change every half-year, and claiming last half-year's figure is claiming the
wrong amount (reading 3). The overdue list warns the same.

**The two regimes.** `invoices.reminder_settings.inkassolov_2026_from date NULL` — the day
LOV-2026-05-22-19 enters into force, which Kongen has not yet set (R4 §2.1; signalled
2027-01-01). NULL or after the letter's date: **the 1988 regime** (INKL, INKF) — every rule
of D8. Set and reached: **the 2026 regime**: no fee on any letter (the creditor's own
fee-bearing kravbrev wait for the § 19 forskrift, which a later phase encodes); **no
creditor's inkassovarsel** (new § 20 is the inkassoforetak's); the last letter before the
hand-off says the claim will be sent to an inkassoforetak if unpaid by its deadline
(Prop. 3 L 12.5.5); late interest and the B2B § 3a compensation continue — they are
forsinkelsesrenteloven (amendment 14). The regime is judged **per letter, on its date**,
and recorded on the letter (`regime`), so a run straddling the day is right letter by
letter (reading 1). Only `invoices:manage` sets the date, in the reminder settings, and a
release may seed it once the date is announced.

**Tests**: `TestCollectionRates_Seeds` (every seeded row against R4 §2.11's tables);
`TestCollectionRates_AppendOnly` (UPDATE refused; a seeded DELETE refused); each API
refusal by its guard; `TestCollectionRates_InForceOn` (boundaries on 1 January and 1 July);
`TestCollectionRates_Outdated` (the clock moved to 2027-01-02 → 409 on run and dispatch,
the warning on the list); `TestRegime_PerLetterDate`.

### D7 — The reminder settings and the per-customer policy

**`invoices.reminder_settings`** (`00041`), one row (`id = 1`), off the settings row every
issue shares:

| Column | Rule | Default |
| --- | --- | --- |
| `enabled` | reminders offered at all | false |
| `first_reminder_days` | the first letter's earliest day after the effective due date (D8), 1–60 | 14 |
| `deadline_days` | every letter's deadline after its sending, 14–60 (≥ 14: INKL § 9, INKF § 1-3) | 14 |
| `grace_days` | days after a deadline before the next letter, 0–10 (the bank's booking lag, R4 §2.7 R15) | 3 |
| `reminders_before_notice` | reminders before the inkassovarsel, 0–2 (a purring is not required before a varsel, FinKN 2023-845) | 1 |
| `collection_notice` | the creditor's inkassovarsel offered (1988 regime only) | true |
| `person_charge` | `fee` \| `none` — a consumer is never charged the compensation (FRL § 4 d) | `fee` |
| `business_charge` | `fee` \| `compensation` \| `none` — never both: they offset (INKF §§ 1-5, 2-6) | `fee` |
| `late_interest` | late interest claimed on letters | false |
| `inkassolov_2026_from` | D6 | NULL |
| `revision`, `updated_at`, `updated_by_user_id` | the settings' optimistic revision | |

`GET /invoices/settings/reminders` (`invoices:access`); `PUT` (`invoices:manage`), every
field required (null is a value only for `inkassolov_2026_from`), 400 on the field,
409 on a stale revision. Changing anything changes only letters created afterwards.

**The per-customer policy** — `invoices.customer_reminder_policies` (`00041`):
`customer_id integer PRIMARY KEY` (opaque), `mode varchar(12) CHECK (mode IN
('normal','no_charges','none'))`, `note varchar(500) NOT NULL DEFAULT ''`,
`updated_by_user_id uuid NOT NULL`, `updated_at`. **No row is `normal`.** `no_charges`:
letters without fee, compensation or interest; `none`: no letter at all (the invoice is
still listed overdue, its next action `blocked`, `policy_none`).

`GET /invoices/customers/{customerId}/reminder-policy` (`invoices:access`; 200 with
`mode: normal` and no note when there is no row); `PUT` (`invoices:payments`) `{mode,
note}` — 400 on the fields; the customer is not checked against the directory (an opaque
id, as everywhere in this schema) but must have an issued invoice or a draft here, else
404. A `PUT` of `normal` with an empty note deletes the row.

**Why an invoices table, not the customers billing profile** (R4 §6 item 1). The profile
already carries *where* to send (`reminder_email`, `reminder_delivery`,
`mig/00019_customers_billing_profile.sql:10,15`) — contact data the customers module owns
and edits under its own permissions. *Whether* to remind and charge a customer is a
credit-control decision, the sensitive `invoices:payments` (D1), and no customers
permission should exempt a debtor from reminders. Read in the module's own schema, the
policy is judged in the run's own transaction without another directory field; the cost
is the slots below. Group defaults are out of scope (D20).

**The slots** (`inv/customer_slots.go`): **merge** (`RepointCustomer`, after
`LockCustomerDocuments`): `from`'s row moves to `into` when `into` has none; when both
have one, **the stricter mode wins** (`none` > `no_charges` > `normal`; reading 7 — a
letter sent wrongly is worse than one not sent) and the notes are joined `into` first,
` / `, cut to 500; `from`'s row is deleted; reported as
`invoices.customer_reminder_policies` with the count. Lock order: the documents first (as
today), then the policy rows by customer id ascending (two rows, one merge) — no other
path locks a policy row with a document. **Export**: the person's section gains
`reminderPolicy {mode, note, updatedAt}`. **Erase**: the row is deleted (a staff note
about the person, no retention basis), reported `invoices.customer_reminder_policies`.

**Tests**: settings defaults, bounds, revision and permission; policy CRUD, the 404, the
delete on `normal`; `TestPolicy_MergeStricterWins` (each pair of modes, the notes, the
count); export and erase; **race** `TestPolicy_MergeRacesPolicyPut` (both finish, the
stricter mode stands or the `PUT` lands on the survivor, no `40P01`).

### D8 — The rules engine

One pure function, `inv/reminderrules.go` — `nextAction(in ruleInput) ruleOutcome` — fed
everything under the invoice's lock (D10) and tested exhaustively; the overdue list, the
preview, the run and the dispatch all call it, so the four never disagree. Its inputs: the
invoice (issue date, due date, `buyer_type` from the snapshot — R4 §5.5: the snapshot, not
a fresh directory read, chooses B2B or B2C), the principal's history (gross, each credit
note's issue date and gross, each live payment's `paid_on` and amount, each live
reservation), the charges (D9), the letters sent (level, `sent_on`, `deadline`, fee,
compensation), the settings, the customer's mode, the live hold and hand-off, the
collection rates, and the day `L` it is asked about.

**The effective due date** `E` is the due date, moved to the following Monday when it is a
Saturday or a Sunday (R4 §2.7, the lenient reading; holidays are not moved — there is no
holiday calendar, reading 8).

**The next action**, the first that applies:

1. principal open ≤ 0 → `none` (charges may still be outstanding; D9);
2. a live hand-off → `none`, `handed_off`;
3. a live hold → `blocked`, `on_hold`;
4. the customer's mode `none` → `blocked`, `policy_none`; reminders not `enabled` →
   `blocked`, `reminders_disabled`;
5. no letter sent → `reminder` (or `collection_notice` when `reminders_before_notice = 0`
   under the 1988 regime), earliest `E + first_reminder_days`;
6. the last letter's deadline + `grace_days` not yet passed → `waiting`, earliest the day
   after it;
7. letters sent < `reminders_before_notice` → `reminder`;
8. under the 1988 regime, `collection_notice` on and none sent → `collection_notice`;
   under the 2026 regime one more `reminder` that **announces the hand-off**
   (`announces_collection`) when none did;
9. otherwise → `hand_off` (suggested; never automatic).

An earliest date in the future is `waiting` with that date. A letter whose day `L` is in
the 2026 regime and whose level the engine computed as `collection_notice` is a
`reminder` with `announces_collection` (D6).

**The fee** on a letter (`fee_kind = reminder_fee`), all of R4 §2.11's rules:

- the 1988 regime on `L` (R20);
- `L ≥ E + 14` (R7 — with `E`, which is never before the due date, so stricter);
- the charge setting for the buyer type is `fee` and the mode is `normal`, and no lifted
  hold barred charges (D11);
- fewer than **two** fee-bearing letters with `sent_on` within the six months before `L`
  (R9, R11 — the six-month reset counted from the last fee letter, so a letter more than
  six months after it counts afresh);
- a second fee only when the previous fee letter's deadline was at least 14 days after
  its `sent_on` and has passed by `L` (R10 — true by construction of `deadline_days ≥ 14`
  and step 6, and still checked);
- the amount: the inkassosats in force on `L`, ÷ 20, **rounded to the nearest krone, .50
  up** (R8; 750 → 38).

A purring sent before `E + 14` carries no fee, not a refusal: `first_reminder_days` may be
7 (R4 §2.5). The creditor's betalingsoppfordring (3/20) is not offered (D20).

**The compensation** (`fee_kind = compensation`): business buyer, `business_charge =
compensation`, mode `normal`, no barring hold, and **no earlier letter of the invoice
claimed it** — once per invoice (reading 9; R4 §7 item 6), the NOK figure in force on `L`
(reading 10; item 7), claimable from the due date without a reminder (R5) but claimed on
the first letter only. Under `compensation` no reminder fee is ever claimed on that
invoice: they offset, and the compensation is larger than two fees (R6). Never on a
person (FRL § 4 d), in either regime.

**Late interest** (`late_interest` on, mode `normal`): **simple** interest on the
principal (R3), from **the day after `E`** (reading 5; the text says "fra forfallsdag",
R4 §7 item 2), to `L` inclusive: each day `d` bears `open(d − 1) × rate(d) / 100 / 365`,
where `open(d − 1)` is gross less the credit notes issued and the live payments paid on
or before `d − 1` (so interest runs to and including a payment's own `paid_on`, the common
practice), and `rate(d)` the row
in force on `d`; actual/365 (reading 5; item 3); summed exactly and **rounded to øre once**,
half away from zero. Interest is never computed on fees or the compensation (item 4). The
letter shows the rate(s) and the from-date (INKL § 10 d's content, which a purring may
carry voluntarily) and the amount.

**The outcome** carries the action, its earliest date, the blocking reasons
(`on_hold`, `handed_off`, `policy_none`, `reminders_disabled`, `waiting`,
`collection_rates_outdated`), and for a letter its `level`, `announces_collection`,
`regime`, `fee_kind`, fee, compensation, interest with its segments `[{from, to, rate,
base}]`, and the inkassosats and compensation rows used.

**Tests** (`TestReminderRules_*`, table-driven, every rule by its boundary, both
regimes): R7 at 13 and 14 days, with a Saturday due date; R8's rounding at 700 and 750 and
a hypothetical 725 (36.25 → 36) and 770 (38.50 → 39); R9 (a third letter without fee);
R10; R11 at six months less a day and six months; R5/R6 (compensation once, never with a
fee, never for a person); `no_charges`; R16 (a hold blocks; a lift without charges bars
fees and compensation, keeps interest); interest across 1 January and 1 July, across a
partial payment and a credit note, on the day of payment, over a weekend due date; the
outdated-rate refusal; R20 (no fee, no notice, the announcing reminder, compensation and
interest kept); `reminders_before_notice = 0`.

### D9 — Charges are not principal

The invoice's open amount, its state and the payments ledger stay **principal-only**
(Finanstilsynet's 2020 letter: fees and interest are never folded into the principal,
R4 §2.2, §2.10). What a letter claims lives on the letter (D10): its `fee`,
`compensation` and `interest`. **The charges outstanding** of an invoice are

```text
Σ fee + Σ compensation over its sent letters
+ the interest of its latest sent letter that claimed interest   (interest is cumulative to that letter's date)
− Σ its live charge payments
```

never below zero. A payment of charges is its own record:

```text
invoices.charge_payments
  id bigint identity PK, invoice_id bigint NOT NULL REFERENCES invoices.invoices ON DELETE RESTRICT,
  paid_on date NOT NULL, amount numeric(14,2) NOT NULL CHECK (amount > 0), currency char(3) NOT NULL,
  source varchar(10) NOT NULL CHECK (source IN ('manual','ocr','camt054')),
  bank_transaction_id bigint REFERENCES invoices.bank_transactions ON DELETE RESTRICT,
  reference varchar(100) NOT NULL DEFAULT '', note varchar(500) NOT NULL DEFAULT '',
  registered_by_user_id uuid NOT NULL, registered_at timestamptz NOT NULL,
  removed_at, removed_by_user_id, removal_reason   -- all or none, as payments
  CHECK ((source = 'manual') = (bank_transaction_id IS NULL))
```

with the payments' two triggers copied (immutable but the removal and the note's
blanking; an issued invoice parent read `FOR SHARE`, the note blanked for an erased
customer). `POST /invoices/{id}/charge-payments` (`invoices:payments`): the payment's
fields and rules (`parsePayment`), then under the invoice's lock 409
`no_charges_outstanding` or `charge_payment_exceeds_outstanding` (with
`chargesOutstanding`); `POST /invoices/{id}/charge-payments/{chargePaymentId}/remove`
`{reason}` as the payment's removal. The queue's apply and the auto-match write them too
(D4, D5).

The issued invoice answers `charges {claimed, paid, outstanding, interestToday?}` — 
`interestToday` the engine's interest to today when interest is on (a figure, not a
claim). The stats summary is unchanged (principal); the overdue list shows both.

**VAT and bookkeeping** (R4 §2.9): a statutory fee and late interest are outside the VAT
base (mval. § 4-1 (2) b, c); the § 3a compensation very likely too (**UNCERTAIN**, R4 §7
item 14). A reminder is not given a number from the salgsdokument series and is not
treated as a salgsdokument (**UNCERTAIN** as a statement, an inference from bokføringsforskriften
§ 5-1-1; R4 §7 item 15); the sent letter and its row are the documentation of the claim
(bokføringsloven § 10). Income recognition is the accountant's; no export of charges in
phase 4 (D20).

**Tests**: the formula (fees, the compensation once, the latest interest, payments,
removed ones ignored, never negative); the endpoint's refusals; the triggers; the
response block.

### D10 — Reminder runs and the letters

**`GET /invoices/overdue`** is D12's list. **`POST /invoices/reminder-runs`**
(`invoices:access+invoices:payments`):

- `{dryRun: true}` — **the preview**: every issued invoice whose next action (D8) is
  `reminder` or `collection_notice` with its earliest date on or before today, each with
  the letter as it would be sent today (level, fee, compensation, interest, the total,
  the deadline, the channel and recipient, warnings), and the invoices blocked or
  waiting with their reasons. 200. No row written, no lock taken.
- `{dryRun: false, items: [{invoiceId, action}]}` — **the run**, 1–500 items, each an
  invoice the caller saw in a preview with the action it showed. 400 on the fields; 409
  `reminders_disabled`; 409 `collection_rates_outdated`. Before any lock, the billing
  profiles of the items' customers are read through the directory, one call per distinct
  customer (the recipient: `ReminderEmail`, already falling back to the invoice e-mail
  and the contact e-mail — amendment 11; the channel: `ReminderDelivery`, `paper` when
  it says so, `email` otherwise; `email` with no address resolved → `paper` with the
  warning `reminder_email_missing`; mail not available on the installation → `paper` with
  `mail_unavailable`). Then the `invoices.reminder_runs` row, and **per item one
  transaction**: the invoice `FOR UPDATE`, then every figure after it; the customer's
  anonymisation marker (`invoices.erased_customers`) → skipped `customer_anonymised`; the
  engine; an action different from the item's → skipped `action_changed` (a payment, a
  hold, another run came between). Otherwise a letter row is inserted, `queued` for
  e-mail or `awaiting_print` for paper. 201 `{run, created: [reminder], skipped:
  [{invoiceId, reason}]}`. Two runs over one invoice serialise on its lock; the second
  finds the first's letter and is `waiting` → `action_changed`.

**`invoices.reminder_runs`**: `id`, `run_on date`, `created_at`, `created_by_user_id`,
`letters integer`, `skipped integer`. Immutable.

**`invoices.reminders`** (`00041`):

```text
id bigint identity PK, invoice_id bigint NOT NULL REFERENCES invoices.invoices ON DELETE RESTRICT,
run_id bigint NOT NULL REFERENCES invoices.reminder_runs,
sequence smallint NOT NULL,                     -- the invoice's n-th letter
level varchar(20) CHECK (level IN ('reminder','collection_notice')),
announces_collection boolean NOT NULL DEFAULT false,
channel varchar(5) CHECK (channel IN ('email','paper')),
recipient varchar(254) NOT NULL DEFAULT '',     -- '' for paper, and once the customer is anonymised
language char(2) NOT NULL,                      -- the buyer snapshot's
created_at timestamptz NOT NULL, created_by_user_id uuid NOT NULL,
-- the letter's facts, written by each dispatch attempt and frozen once it is sent or printed:
sent_on date, deadline date, regime varchar(15) CHECK (regime IN ('inkassolov_1988','inkassolov_2026')),
principal_open numeric(14,2), fee_kind varchar(15) CHECK (fee_kind IN ('none','reminder_fee','compensation')),
fee numeric(14,2), compensation numeric(14,2), interest numeric(14,2), interest_from date,
interest_segments jsonb, inkassosats numeric(10,2), charges_earlier numeric(14,2), total numeric(14,2),
pdf_object_key varchar(300), pdf_sha256 char(64), message_id varchar(200), sent_at timestamptz,
status varchar(15) NOT NULL CHECK (status IN ('queued','awaiting_print','sent','printed','withdrawn','failed')),
attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz, lease_id varchar(100), lease_until timestamptz,
last_error varchar(500), failed_at timestamptz,
withdrawn_at timestamptz, withdrawn_by_user_id uuid, withdrawal_reason varchar(200),
UNIQUE (invoice_id, sequence)
INDEX ix_reminders_due (next_attempt_at) WHERE status = 'queued'
CHECK: sent/printed ⇒ every fact and sent_at set; withdrawn ⇒ withdrawn_at and a reason; failed ⇒ failed_at
```

`UNIQUE (invoice_id, sequence)` is the floor under two runs. A parent trigger on INSERT
reads the invoice `FOR SHARE` (an issued invoice only; the anonymisation marker re-read
after the wait — the delivery insert's shape, `mig/00035…:156-177`) and blanks the
recipient for a marked customer. An immutability trigger: no DELETE; identity columns
(`invoice_id`, `run_id`, `sequence`, `level`, `channel`, `language`, `created_*`) never
change; the facts change only while the status is `queued`, `awaiting_print` or
`failed`; once `sent` or `printed`, nothing changes but `pdf_object_key`/`pdf_sha256` set
once and the recipient blanked; a `withdrawn` row changes nothing. A withdrawn letter
keeps its sequence — the next letter takes the next.

**Why the facts are written at sending** (amendment 12): the deadline is "at least 14
days from sending" (INKL § 9) and a fee is judged on its letter's date (INKF § 1-2), so a
letter queued on Monday and sent on Wednesday must say Wednesday.

**The worker `invoices-reminders`** (`inv/reminder_worker.go`), the row-lease shape of
`EhfWorker` (`inv/ehf_worker.go:149`, 60-second lease, poll every 5 s): claim one `queued`
row whose `next_attempt_at ≤ @now` by conditional `UPDATE` over a `FOR UPDATE SKIP
LOCKED` pick (`inv/queries/transmissions.sql:54-73`'s shape), then:

1. **One transaction**: the invoice `FOR UPDATE`, then the letter `FOR UPDATE`, then the
   engine at `L = today` (the claim's one clock read). Still the same action and level?
   — open > 0, no hold, no hand-off, the mode not `none`, not anonymised, the rates not
   outdated — else the letter becomes `withdrawn` with the reason
   (`settled`, `on_hold`, `handed_off`, `policy_none`, `customer_anonymised`,
   `action_changed`), `withdrawn_by_user_id` NULL. Otherwise the facts are written —
   `sent_on = L`, `deadline = L + deadline_days`, the amounts. Commit.
2. Render the letter's PDF from the row (`inv/reminderpdf.go`, the invoice PDF's maroto
   layout and fonts, `inv/pdf.go`) and store it once at
   `reminders/<invoiceId>/<reminderId>-<sentOn>.pdf` — outside any lock; 503 from the
   store reschedules.
3. Send through the installation's SMTP seam (`s.smtpSend`, `inv/send.go:303` — not the
   rate-limited endpoint, R4 §5.3) with the seller as Reply-To, the subject
   "Purring: faktura {n}" / "Inkassovarsel: faktura {n}" (en: "Reminder: invoice {n}" /
   "Debt collection notice: invoice {n}"), a short cover text and the PDF; **a stable
   Message-ID per letter**, `<reminder-{id}@…>`, so a retry after a crash is the same
   message to a mail client (at least once, reading 13).
4. `sent`, `sent_at`, `message_id` — a lease-checked `UPDATE`, a no-op when the lease
   changed. A failure: `attempts + 1`, backoff `min(3600, 2^n)` s; still `queued` 48
   hours after its first attempt → `failed` with the reason (an attention item, D12).
   `POST /invoices/reminders/{reminderId}/retry` (`invoices:payments`) puts a `failed`
   letter back to `queued` (409 `reminder_not_failed`); its facts are written afresh by
   the next attempt.

At most one letter per second, serially: SMTP's own pace, not a burst.

**Paper.** `POST /invoices/reminders/print` (`invoices:payments`) `{reminderIds}` (1–200,
each `awaiting_print`, else 409 `reminder_not_awaiting_print` naming it): per letter the
transaction of step 1 with `L` = today (a withdrawn one is reported and left out), then
each PDF rendered and stored as in step 2, and one **combined PDF** of the printed
letters, one per page group, answered `application/pdf` (`Content-Disposition:
attachment; filename="reminders-<date>.pdf"`, `Cache-Control: private, no-store`); the
letters are `printed`, `sent_on` today — "print and post today" is the screen's words
(reading 14). `GET /invoices/reminders/{reminderId}/pdf` (`invoices:access`) answers a
sent or printed letter's stored PDF (409 `reminder_not_sent` otherwise; 500 on a
missing or altered object; the invoice PDF's checks, `inv/pdfstore.go:390-440`).
`POST /invoices/reminders/{reminderId}/withdraw` `{reason}` (`invoices:payments`) — a
`queued`, `awaiting_print` or `failed` letter; 409 `reminder_not_withdrawable`.

**The letter's content** (R4 §2.6's table), in the buyer's language (nb, en), fixed text
pinned by golden PDFs and text extraction:

- the seller block as the invoice's (the seller snapshot), the buyer block, the date
  `sent_on`, the heading **"Purring"** / **"Inkassovarsel"** (nb; en "Payment reminder" /
  "Debt collection notice");
- what the claim concerns: invoice number, issue date, due date, and the amounts
  **separately**: the invoice's total, credited, paid, **the principal open**; this
  letter's fee or compensation; earlier charges outstanding; the interest to `sent_on`
  with its rate(s) and from-date; **the total to pay** (R13; INKL § 10 c, d by choice);
- the deadline, `deadline`, and payment information: the account, the **invoice's KID**
  when it has one, else "merk betalingen med fakturanummer {n}"; the pay link (D15) when
  the invoice has a live one;
- "Har du betalt i mellomtiden, kan du se bort fra dette brevet" (en equivalent);
- **the objection sentence** on every letter: "Har du innsigelser mot kravet, gi oss beskjed
  før fristen" — the right to avoid costs by objecting in time must never be obscured
  (FinKN 2025-240, R4 §2.6);
- **an inkassovarsel** additionally says, clearly and unambiguously, that "kravet vil bli
  sendt til inkasso dersom det ikke er betalt innen {deadline}" (INKL § 9, SOM) and that
  this **may** add costs — never that it necessarily will (FinKN 2025-240); its deadline
  is ≥ 14 days from sending by `deadline_days`;
- **a reminder announcing the hand-off** (the 2026 regime) says the claim "vil bli
  oversendt til et inkassoforetak" if unpaid by the deadline (Prop. 3 L 12.5.5).

**Tests**: preview and run (each skip reason by its guard; the recipient and channel
rules; 500 items; the run row); the triggers (each forbidden change); the worker (claim
and lease — two workers, one send; the re-judge withdrawing for each reason; facts
written at sending, a send two days late carrying its own date and deadline; the PDF
stored once; the stable Message-ID; backoff; 48 hours → `failed`; retry; the lease-changed
no-op); print (the combined PDF's page count, the facts, a withdrawn letter left out);
withdraw; golden PDFs of each level in both languages (`pdfModelBuilt`'s seam) and their
text. **Races**: `TestReminderRun_RacesPayment` — a run's item held after its invoice lock
and a manual payment of the whole open amount: run first → a letter, then withdrawn at
dispatch `settled`; payment first → skipped `action_changed`; `TestReminderRun_TwoRuns`
— one letter; `TestReminderDispatch_RacesHold` — a hold placed while the letter is queued
→ withdrawn `on_hold`; `TestReminderDispatch_RacesImport` — an import's match settling the
invoice while the worker holds the claim; all without `40P01`.

### D11 — Holds, the hand-off to collection, and its export

**A hold** marks an invoice disputed: `invoices.invoice_holds` (`id`, `invoice_id`,
`kind varchar(10) CHECK (kind = 'disputed')`, `note varchar(500) NOT NULL`, `placed_at`,
`placed_by_user_id`, `lifted_at`, `lifted_by_user_id`, `lift_note varchar(500)`,
`charges_allowed boolean` — set at the lift), one live per invoice (`UNIQUE (invoice_id)
WHERE lifted_at IS NULL`). `POST /invoices/{id}/hold` `{note}` (`invoices:payments`):
under the invoice's lock; 404; 409 `invoice_draft`, `credit_note_no_reminders`,
`invoice_on_hold`. `POST /invoices/{id}/hold/lift` `{note, chargesAllowed}`: 409
`invoice_not_on_hold`. While held: no letter is created or sent (D8 step 3, D10's
re-judge), no fee or compensation. **On the lift** the person answers whether charges may
still be claimed: `false` (the default in the form) bars fees and the compensation on
that invoice for good — the objection had reasonable grounds (INKL § 17 second paragraph;
new § 18, R16); `true` — it was groundless. Late interest is not a cost and keeps running.
A held invoice still takes payments and imports.

**The hand-off**: `invoices.collection_handoffs` (`id`, `invoice_id`, `handed_on date`,
`agency varchar(200) NOT NULL`, `agency_reference varchar(100) NOT NULL DEFAULT ''`,
`note varchar(500) NOT NULL DEFAULT ''`, `created_at`, `created_by_user_id`,
`withdrawn_on date`, `withdrawn_by_user_id`, `withdrawal_reason varchar(200)`), one live
per invoice. `POST /invoices/{id}/collection` `{handedOn, agency, agencyReference?,
note?}` (`invoices:payments`): `handedOn` not after today and not before the issue date
(400); under the invoice's lock; 409 `invoice_draft`, `credit_note_no_reminders`,
`invoice_settled` (nothing open), `invoice_handed_off`. Every queued or awaiting letter
of the invoice is withdrawn in the same transaction (`handed_off`).
`POST /invoices/{id}/collection/withdraw` `{withdrawnOn, reason}`: 409
`invoice_not_handed_off`. While handed off: no letters; **payments are still registered**
(by hand and by import) — the creditor still owns the claim (INKL § 2, R4 §2.10), and a
direct payment must be reported to the agency, which the invoice view says beside the
payment (R4 §7 item 16); the pay page is closed (D15).

**`GET /invoices/collection-export.csv`** (`invoices:access+invoices:payments`): the
live hand-offs `?handedFrom&handedTo` (both dates, required together) or the invoices
`?invoiceId=` (repeatable, 1–500, each issued); 400 otherwise or past 500 rows. One row
per invoice, in the module's CSV format (`inv/csvfile.go`: UTF-8 with BOM, `;`, the
decimal comma, CRLF, RFC 4180 quoting, the formula guard on text columns; `Cache-Control:
private, no-store`; `invoices-collection-<date>.csv`). The columns, fixed, English, in
this order — R4 §2.10's implied minimum, **principal apart from charges**:

```text
Invoice number;Issue date;Due date;Delivery;KID;Customer number;Debtor;Debtor type;Org no;Foreign id;Address line 1;Address line 2;Postal code;City;Country;E-mail;Gross;Credited;Paid;Principal open;Payments;Fees claimed;Compensation claimed;Interest rate;Interest from;Interest to;Interest claimed;Charges paid;Letters;Notice sent;Notice deadline;Disputed;Handed on;Agency;Agency reference
```

`Debtor` and the address are the buyer snapshot's; `E-mail` the profile's `ReminderEmail`,
read through the directory before the export (empty when it fails — logged at warn);
`Payments` and `Letters` compact lists (`2026-10-01 500,00 ocr | …`, `2026-10-20
reminder 2026-11-03 38,00 | …`); `Interest to` today, the interest the engine computes
to today. No national identity number (none is held).

**Tests**: hold and lift (each refusal; the barring lift; interest unaffected); the
hand-off (refusals; letters withdrawn in its transaction; imports still matched; the pay
page closed); the export (both selections, each column, the guard, the cap, the order,
the directory failure); **race** `TestHandoff_RacesReminderDispatch` — the hand-off and
the worker's re-judge on one invoice: the letter is sent before the hand-off or withdrawn
by it, never sent after.

### D12 — The overdue list and attention

**`GET /invoices/overdue`** (`invoices:access`): the issued invoices whose state is
`overdue` (`invoices.document_state`, unchanged), and with `?charges=outstanding` also the
paid ones with charges outstanding; filters `customerId`, `action` (`reminder`,
`collection_notice`, `hand_off`, `blocked`, `waiting`), `dueBefore`; paged, most overdue
first. Each item: the invoice (id, number, customer, buyer name, `buyerType`, issue and
due date), `daysOverdue` (from `E`), `principalOpen`, `charges` (D9), `interestToday` when
interest is on, `lastLetter {level, status, sentOn, deadline}`, `nextAction {action,
earliestOn, reasons[]}` (D8), `hold`, `handoff`, `policyMode`, `remindable`. One clock read;
the engine runs per item on figures read in one statement per page (no lock — a read),
so a page of 100 is one query plus the engine. The warning `collection_rates_outdated`
when it applies.

**`GET /invoices/stats/attention`** (`invoices:access`), the dashboard's shared item shape
(`openapi/expenses.yaml:3637-3664`; the host translates the sentence from `type`):

- `invoiceOverdue` — the 20 most overdue invoices; `id` `invoiceOverdue/<invoiceId>`,
  `entityId` the invoice, `title` the buyer's name, `occurredAt` the day after `E`.
- for `invoices:payments` only: `bankTransactionsOpen` — one per bank file with lines
  `pending` or `exception` (`entityId` the file, `occurredAt` its upload);
  `reminderFailed` — one per `failed` letter (`entityId` the invoice); and from 4C
  `paymentCaptureFailed` — one per attempt `capture_failed` (`entityId` the invoice).
  A caller without it is answered the first kind alone, never a 403 (customers'
  precedent).

`GET /invoices/stats/summary` is unchanged (R/invoices.md:1849-1879).

**Tests**: the list's filters, order, paging, each field and the engine's agreement with
the preview; attention per permission, the 20 cap, each type's clearing.

### D13 — The payments port and the Vipps adapter

**A shared platform package `srv/payments`** (R4 §5.9; the roadmap's "not inside
Invoices, so a later Point of sale module consumes the same adapters", `ROADMAP.md:859-861`):
interfaces and DTOs only, **no SQL** (MB rule 4 — the tables are each consumer's), importing
no module (MB rule 2). It joins depguard's `platform` list (`apps/server/.golangci.yml:361-400`,
beside `internal/peppol` and `internal/secrets`) with `internal/payments/vipps` and
`internal/payments/vipps/vippstest`, and MB rule 1's list.

```go
package payments

type Provider interface {
    // Create asks for a payment of Amount under Reference. One reference is one
    // payment request; the same IdempotencyKey twice is one request.
    Create(ctx context.Context, r CreateRequest) (Created, error)
    // Get is the payment as the provider holds it — its state and its four amounts.
    Get(ctx context.Context, reference string) (Payment, error)
    Capture(ctx context.Context, reference string, amount Money, idempotencyKey string) (Payment, error)
    // Cancel releases what is authorized and not captured, or ends a request not yet
    // authorized; "already cancelled" is success.
    Cancel(ctx context.Context, reference string, idempotencyKey string) (Payment, error)
    Refund(ctx context.Context, reference string, amount Money, idempotencyKey string) (Payment, error)
    // VerifyWebhook authenticates a callback against the registration's secret and the
    // URL it was registered at, and answers the reference it concerns.
    VerifyWebhook(r WebhookRequest, secret string, registeredURL *url.URL) (WebhookEvent, error)
    // Verify makes the cheapest authenticated call, for the settings page.
    Verify(ctx context.Context) error
}
type Money struct{ Currency string; Minor int64 }        // øre for NOK
type Flow string                                        // FlowRedirect | FlowQR
type CreateRequest struct {
    Reference, Description, IdempotencyKey string
    Amount                                 Money
    Flow                                   Flow
    ReturnURL                              string   // FlowRedirect
    CustomerPresent                        bool
    TermsURL, PrivacyURL                   string
}
type Created struct{ Reference, RedirectURL, QRSVG string } // RedirectURL for FlowRedirect; QRSVG for FlowQR
type State string // StateCreated | StateAuthorized | StateAborted | StateExpired | StateTerminated
type Payment struct {
    Reference, PSPReference string // the CREATED event's
    State                   State
    Authorized, Captured, Cancelled, Refunded Money
    CaptureGuaranteedUntil  *time.Time
}
type WebhookRequest struct{ Method, PathAndQuery, Host string; Header http.Header; Body []byte }
type WebhookEvent struct{ Reference, Name, PSPReference string; Success bool }
```

Typed errors: `ErrUnauthorized` (401/403, the keys), `ErrThrottled{RetryAfter}` (429, 423),
`ErrNotFound`, `ErrConflict{Code}` (409, the provider's code), `ErrInsufficientFunds`
(6260), `ErrCaptureFailed` (6280), `ErrInvalidSignature`; anything else — a transport
failure, a 5xx — is "outcome unknown". **What differs between providers** stays inside the
adapter (R4 §4.10): reserve-capture, the request's lifetime, who renders the QR, the
webhook signature.

**The Vipps ePayment adapter** `srv/payments/vipps` (R4 §4.4–§4.7):
`vipps.New(Options{BaseURL, ClientID, ClientSecret, SubscriptionKey, MSN, SystemVersion,
Transport, Now})`. The access token from `POST /accesstoken/get`, cached until a minute
before its expiry (1 h test, 24 h production), one fetch at a time; every call sends
`Authorization: Bearer`, `Ocp-Apim-Subscription-Key`, `Merchant-Serial-Number` and the
system headers `Vipps-System-Name: vantigo`, `Vipps-System-Version: <release>`,
`Vipps-System-Plugin-Name: vantigo-invoices`, `Vipps-System-Plugin-Version: <release>`;
`Idempotency-Key` on create, capture, cancel and refund — the key the caller persisted, so a
retry reuses it (R4 §4.5). Create: `paymentMethod.type = WALLET`; `WEB_REDIRECT` with
`returnUrl`, or `QR` with `qrFormat {format: IMAGE/SVG+XML}` and `customerInteraction:
CUSTOMER_PRESENT`; amounts 100–65 000 000 øre in NOK; `reference` `^[a-zA-Z0-9-]{8,64}$`;
`paymentDescription` 3–100 characters; `merchantLegalLinks`. Problems are RFC 7807 with
`extraDetails` codes, mapped to the typed errors; "already cancelled" (6050, 6190) is
success. A 30-second bound per request, no redirects, **no retry inside the adapter**
(the worker retries under the key), `Deps.HTTPTransport` as the seam, unguarded like
Brreg and Storecove (the URL is the operator's). Webhook verification: R4 §4.7's exact
algorithm — the content hash over the raw body, the string-to-sign `POST\n<pathAndQuery>\n<x-ms-date>;<host>;<x-ms-content-sha256>`
with `\n`, HMAC-SHA256 under the secret, constant-time comparison; `pathAndQuery` and
`host` from the **registered URL** (built from `PUBLIC_BASE_URL`), never the request line
(a proxy may rewrite them). The published vector's signature does not reproduce (R4
§7 item 37): the content-hash step is pinned to it, the signature step to a fixture the
tagged test captures from a real test-environment webhook (D16).

**`vippstest`** — an ordinary package serving an `httptest` TLS server speaking the
ePayment, token and webhooks APIs as their OpenAPI documents describe them (R4 §4.4),
`storecovetest`'s shape (`inv/accesspoint/storecovetest`): scripted states, the special
amounts (151 insufficient funds), 409/423/429/5xx, idempotency by key, a signed webhook
POST to a URL the test gives. Every suite runs the real adapter over it through
`Deps.HTTPTransport`.

**The tagged test** `srv/payments/vipps/vipps_test_env_test.go` (`//go:build vipps`),
skipped unless `VIPPS_TEST_CLIENT_ID`, `VIPPS_TEST_CLIENT_SECRET`,
`VIPPS_TEST_SUBSCRIPTION_KEY`, `VIPPS_TEST_MSN` and `VIPPS_TEST_PHONE` are set (R4 §4.8):
token; create; force approve (`/epayment/v1/test/payments/{reference}/approve`); poll to
`AUTHORIZED`; capture with a key, `capturedAmount` asserted; the capture repeated with the
same key; a partial refund; the event log; a second create cancelled to `TERMINATED`;
amount 151; a QR create; and, with `VIPPS_TEST_WEBHOOK_URL` (a reachable URL the tester
controls), a registration and one captured event written to `testdata/` as the
signature fixture. One reference per scenario; the token refreshed past an hour.

**Tests** (untagged): the adapter over `vippstest` — each operation's request (headers,
key, body), each error mapping, the token cache and its refresh, a reused key, the
QR's SVG, the webhook verification against the published content hash and a
self-generated signature (flagged as such) and the captured fixture once it exists.

### D14 — Vipps credentials, payment settings and `PUBLIC_BASE_URL`

**`PUBLIC_BASE_URL`** (platform, `srv/config`): the https URL, base path included, at which
the installation is reachable **from the internet** — unset by default; an http or
malformed value refuses to start (the config's problems list). `APP_URL` may be an
intranet name; publishing a pay page is the operator's decision, so it is its own setting.
When set, its origin is added to the CSRF protection's trusted origins
(`srv/server/server.go:41-53`). Pay links and webhooks need it; on-site QR payments do not
(the tablet is signed in, D17).

**Credentials** — `invoices.payment_provider_credentials` (`00042`), phase 2's shape
(`mig/00036…:44-55`): one row (`id = 1`), `provider varchar(20) CHECK (provider IN
('vipps'))`, `settings_json text` (non-secret: `msn`), `secret_ciphertext text` (sealed
JSON `{clientId, clientSecret, subscriptionKey}` — `client_id` sealed too, R4 §7 item 36),
`webhook_id varchar(100)`, `webhook_secret_ciphertext text`, `rejected_at`, `updated_at`.
Purposes `invoices/payment-provider-credential` and `invoices/payment-webhook-secret`
(`srv/secrets/secrets.go:99-203`). One sales unit per installation (R4 §7 item 32 —
whether on-site and remote need two is an open question). Rule 4: Point of sale gets its
own table later.

- `GET /invoices/settings/vipps` (`invoices:manage`): `{msn?, hasCredentials, rejectedAt?,
  webhook {registered, url?}}`, 200 with `hasCredentials: false` when none.
- `PUT` `{msn, clientId?, clientSecret?, subscriptionKey?}`: the first PUT needs all three
  secrets; an omitted one keeps the stored one (opened and re-sealed from the row read
  `FOR UPDATE`); 400 on the fields (`msn` digits, 4–10); 503 `payments_unavailable` when a
  kept secret cannot be opened. Clears `rejected_at`. Never answers a secret.
- `DELETE`: 409 `payment_attempts_active` while any attempt is live (D16); 204.
- `POST …/verify`: a token fetch → `{result: ok | unauthorized | unreachable}`;
  `unauthorized` sets `rejected_at`, `ok` clears it; 409 `payments_unavailable` without
  credentials.
- `POST …/webhook` registers `PUBLIC_BASE_URL + /api/v1/invoices/vipps/webhooks` for the
  ePayment events (R4 §4.7) and stores the id and the sealed secret, replacing (and then
  deleting at Vipps) an earlier registration; 409 `public_url_missing`; 502
  `provider_failed`. `DELETE …/webhook` unregisters.

**Payment settings** — `invoices.payment_settings` (`00041` with `bank_import_format`;
`00042` adds the rest), one row:

| Column | Rule |
| --- | --- |
| `bank_import_format` | `ocr` \| `camt054` \| NULL (D3) |
| `pay_links_enabled` | pay links on issued invoices (D15); requires `PUBLIC_BASE_URL`, credentials, both URLs below |
| `terms_url`, `privacy_url` | https URLs of the seller's sales terms and privacy notice — Vipps' `merchantLegalLinks`, shown on the pay page (R4 §4.2) |
| `on_site_payments_enabled` | the on-site QR (D17) |
| `on_site_acknowledged_by_user_id`, `on_site_acknowledged_at` | who acknowledged the kontantsalg notice, when; both set whenever the flag is |

`GET /invoices/settings/payments` (`invoices:access`); `PUT` (`invoices:manage`) — every
field required; turning `onSitePaymentsEnabled` on requires `acknowledgeKontantsalg:
true` in the same body (400 otherwise), which records the caller and the time; turning it
off clears both. 409 `public_url_missing` when pay links are turned on without
`PUBLIC_BASE_URL`; `payments_unavailable` without credentials.

**Tests**: config (unset, http refused, a path kept, the trusted origin); credentials
(never answered, kept when omitted, re-sealed, rejected-at, delete refused while live,
verify's three answers, the webhook registration through `vippstest`); payment settings
(each rule; the acknowledgement recorded and cleared).

### D15 — The pay page and pay links

**Why a page** (R4 §1.2 item 3, §4.2): a Vipps payment request lives ten minutes and a
long-living one is closed to an invoice's 14-day term in Norway, so the link on an invoice
is a **stable URL of the installation**, `PUBLIC_BASE_URL/pay/<token>`, which creates the
ePayment only when the customer presses "Betal med Vipps".

**`invoices.pay_links`** (`00042`): `id`, `invoice_id` (`UNIQUE` among live links),
`token varchar(43) NOT NULL UNIQUE` (32 random bytes, base64url — 256 bits; stored plain:
it grants paying an invoice and seeing its number and amount, nothing a database reader
cannot already see), `url varchar(500) NOT NULL` (the full URL as printed, a snapshot),
`created_at`, `created_by_user_id` (NULL when the issue made it), `revoked_at`,
`revoked_by_user_id`. A trigger: no DELETE; only the revocation, once.

- **At issue**, when pay links are available (meta's rule, judged before the transaction
  with the profile — no lock), the issue inserts the link in its own transaction (a child insert under the document it already holds) and the PDF —
  stored once after the commit — prints "Betal med Vipps" with the URL and a QR of it
  beside the payment block. A PDF is never re-rendered, so an invoice issued before pay
  links has none in its PDF.
- `POST /invoices/{id}/pay-link` (`invoices:issue`) makes one for an issued invoice that
  has none (201; 409 `pay_links_unavailable`, `invoice_draft`,
  `credit_note_no_payments`, `pay_link_exists`); `POST /invoices/{id}/pay-link/revoke`
  (`invoices:issue`). The e-mail cover text (`inv/mailtext.go`) and the reminder letter
  (D10) carry the live link.

**The anonymous operations** (`x-vantigo-access: anonymous`; each rate-limited per client
address, `invoices-pay` 30 per 10 minutes for the reads and 10 per 10 minutes for the
create — the router's `Limits`, `inv/module.go:74-77`):

- `GET /invoices/pay/{token}` → 200 `{seller {name, organisationNumber}, invoiceNumber,
  issueDate, dueDate, openAmount, currency, kid?, termsUrl, privacyUrl, language, payable,
  reason?}` — **never the buyer's name or address** (anyone holding the link sees the
  page). `payable` false with `reason`: `settled` (open ≤ 0), `credited`, `revoked`,
  `handed_off` ("contact the agency"), `amount_out_of_range` (open above 650 000 or below
  1.00), `unavailable` (the switch, the credentials or `PUBLIC_BASE_URL` gone). An
  unknown token is 404 with the same body and timing as a revoked one's lookup (no
  enumeration oracle beyond the token's own entropy).
- `POST /invoices/pay/{token}/attempts` `{acceptTerms: true}` → 400 without it (Vipps
  requires the customer's active acceptance before a payment starts, R4 §4.2); 409 the
  `reason`s above; 409 `too_many_attempts` (five live attempts on the invoice); 503
  `payments_unavailable`; 502 `provider_failed`; else **201 `{reference, redirectUrl}`**
  — D16's attempt with `flow = pay_link`, `WEB_REDIRECT`, `returnUrl` =
  `PUBLIC_BASE_URL/pay/<token>?attempt=<reference>`. The page opens `redirectUrl` at once,
  unchanged (R4 §4.2).
- `GET /invoices/pay/{token}/attempts/{reference}` → `{state: pending | paid | failed |
  cancelled}` — the return page polls it every two seconds while `pending`.

**The host** adds the public route `/pay/$token` (`host/routes/pay.$token.tsx`), rendering
`PayPage` from `@vantigo/invoices-ui`, which calls only the three operations and works
without a session; `publicPaths` gains a prefix rule for `/pay/` (a test pins that
`/payments` is not public). The page states the seller, the invoice number, the amount
open, the due date and the KID (to pay by bank instead), links the terms and privacy
notice with the acceptance checkbox, and shows the state on return. nb by default, en by
the snapshot's language or the reader's choice.

**Tests**: the link at issue (in the issue's transaction; none without availability; the
PDF's line and QR in a golden), on demand, revoked; the e-mail and letter lines; each
operation's answers and refusals; no buyer data in any answer (a test greps the JSON for
the snapshot's name and address); the rate limits; CSRF — a same-origin POST passes, a
cross-site one is refused; the host route public and `/payments` not.

### D16 — Payment attempts, the poll worker, capture and webhooks

**`invoices.payment_attempts`** (`00042`):

```text
id bigint identity PK, invoice_id bigint NOT NULL REFERENCES invoices.invoices ON DELETE RESTRICT,
provider varchar(20) CHECK (provider = 'vipps'), flow varchar(10) CHECK (flow IN ('pay_link','on_site')),
pay_link_id bigint REFERENCES invoices.pay_links,   -- pay_link flow only
reference varchar(64) NOT NULL UNIQUE,               -- "inv<number>-<attempt id>"
amount numeric(14,2) NOT NULL CHECK (amount >= 1),   -- the open amount at creation
create_key uuid, capture_key uuid, cancel_key uuid NOT NULL,   -- Idempotency-Keys, persisted before each call
state varchar(16) NOT NULL CHECK (state IN ('creating','created','authorized','capturing','captured',
      'cancelling','cancelled','aborted','expired','terminated','capture_failed','failed')),
psp_reference varchar(100), redirect_url varchar(2500),       -- redirect_url: sensitive, never logged, answered only to the attempt's creator
authorized_amount, captured_amount, cancelled_amount numeric(14,2),
reserved_amount numeric(14,2),                        -- the capture decided under the invoice's lock; live while 'capturing'
capture_psp_reference varchar(100), capture_guaranteed_until timestamptz,
created_at timestamptz NOT NULL, created_by_user_id uuid,   -- on_site: the tablet's user; pay_link: NULL
expires_at timestamptz NOT NULL, authorized_at, captured_at, finished_at timestamptz,
next_poll_at timestamptz, polls integer NOT NULL DEFAULT 0, capture_attempts integer NOT NULL DEFAULT 0,
lease_id varchar(100), lease_until timestamptz, last_error varchar(500)
UNIQUE (invoice_id) WHERE flow = 'on_site' AND state IN ('creating','created','authorized','capturing')
INDEX ix_payment_attempts_due (next_poll_at) WHERE state IN ('creating','created','authorized','capturing','cancelling')
```

No DELETE; identity columns frozen; terminal states (`captured`, `cancelled`, `aborted`,
`expired`, `terminated`, `capture_failed`, `failed`) change nothing but the lease. Every
query takes `@now` from `Deps.Clock()`.

**Create** (the pay page's POST, or D17's on-site request): read on the pool; one
transaction — the invoice `FOR UPDATE`, open computed, the five-live cap, the attempt
inserted `creating` with `amount` = open and `create_key`; commit; then `Provider.Create`
**outside any lock**; then `created` with `redirect_url` (or the QR) and `next_poll_at =
created_at + 5 s` (R4 §4.5). A failure → `failed` and 502 `provider_failed`; an unknown
outcome leaves `creating` for the worker, which polls `Get` (a 404 after the request's
ten minutes → `failed`).

**The worker `invoices-payments`** (`inv/payment_worker.go`, the row-lease shape, a
one-second tick, claiming due rows by `next_poll_at`), one claim per attempt:

- `creating`/`created`: `Get`. `CREATED` → next poll in 2 s while before `expires_at`
  (ten minutes), then one last read; `ABORTED`, `EXPIRED`, `TERMINATED` → that state,
  `finished_at`; `AUTHORIZED` → `authorized`, `authorized_amount`, and on at once to the
  capture decision. At most 30 reads a minute per reference — under Vipps' 120 (R4 §4.5).
- **The capture decision** — one transaction: the attempt `FOR UPDATE`, then **the invoice
  `FOR UPDATE`**, then `open` (gross − credited − paid − every **other** live
  reservation). `capture = min(authorized, open)`. Above 0 → `capturing`,
  `reserved_amount = capture`, `capture_key` set. Zero (paid by a manual payment, an
  import or another attempt meanwhile) → `cancelling`. Commit. **No provider call is
  made inside it** (amendment 1).
- `capturing`: first `Get` — `captured ≥ reserved` already (a capture whose answer was
  lost) → register. Else `Capture(reference, reserved, capture_key)`; success with
  `capturedAmount ≥ reserved` (the status code alone is not enough, R4 §4.4) →
  **register**: one transaction — the attempt `FOR UPDATE`, the invoice `FOR UPDATE`, a
  payment `source = vipps`, `payment_attempt_id` (unique: the exactly-once guard),
  `amount = reserved`, `paid_on` the Oslo day of the claim's clock read, `reference` the
  attempt's reference, no user; the attempt `captured`, `captured_amount`,
  `capture_psp_reference`, `captured_at`, `reserved_amount` NULL; commit. Then, when
  `authorized > captured`, `Cancel` the remainder (`cancel_key`), its outcome recorded,
  retried on the cadence until it succeeds. `ErrInsufficientFunds`/`ErrCaptureFailed` →
  `capture_failed`, the reservation released, `Cancel` (an attention item, D12). Unknown
  outcome → retry with the same key, backoff `min(600, 2^n)` s; past
  `capture_guaranteed_until` or seven days → `capture_failed` (R4 §4.4: BankAxept's
  reservation lapses in seven).
- `cancelling` → `Cancel(cancel_key)`; done → `cancelled`.

**Capture at once** on `AUTHORIZED` (reading 15): an issued invoice describes a delivered
service, which is what Vipps' "capture when ready to deliver" asks (R4 §4.4).

**The reservation in `openOf`** (D2) is what makes a capture and a manual payment safe
together: whichever locks the invoice first decides, and the other sees its figure.

**Webhooks** (optional, reading 16): `POST /invoices/vipps/webhooks` (`x-vantigo-access:
anonymous`, rate-limited `invoices-vipps-webhook` 600 per minute per client). The router
gains `RouterOptions.RawBodies map[string]int64` (operationId → cap; here 64 KiB): the
raw bytes are read under the cap and kept in the request's context before the generated
wrapper decodes, a platform change in `srv/module/router.go` beside `BodyLimits`. The
handler verifies with `VerifyWebhook` against the stored secret and the registered URL:
invalid → 401 (logged at warn, no detail); valid → the attempt with that reference gets
`next_poll_at = now` (a nudge — **the poll stays the only path that changes state**,
R4 §4.7's "never rely on webhooks alone"), and 200 at once, also for an unknown
reference (Vipps would otherwise retry for seven days). Whether to ship the receiver
depends on the tagged test pinning the signature (open question 3); poll-only is complete
without it.

**Tests**: create (the cap, `creating` on an unknown outcome, the keys persisted before
each call); the worker through every state against `vippstest` (each `Get` answer, the
cadence and the ten-minute bound, capture success, a lost capture answer registered once,
a partial capture and the remainder cancelled, 6260 → `capture_failed` and cancelled, a
retry with the same key, the seven-day bound, `cancelling`); exactly once (the capture's
answer, the poll and a nudge all reporting one capture → one payment; the unique key
refusing a second); the webhook (a valid signature nudges, an invalid one is 401, an
unknown reference 200, the raw body reaching the handler, the cap). **Races**:
`TestVippsCapture_RacesManualPayment` — the capture decision and a manual registration of
the whole open amount: decision first → the manual one refused `payment_exceeds_open`
(reserved), the capture registered; manual first → the decision cancels; never paid
twice; `TestVippsCapture_TwoAttemptsBothAuthorized` — the open amount captured once, the
other cancelled; `TestVippsCapture_RacesImport` — a bank import of the same invoice's KID
payment during `capturing`: the import sees the reservation and queues
`invoice_settled`/`exceeds_open` rather than over-registering.

### D17 — The quick invoice, and on-site payment as kontantsalg

**The quick invoice** — `POST /invoices/quick`
(`permission:invoices:access+invoices:create+invoices:issue`): `{customerId, lines (1–10):
[{description, quantity, unit?, unitPrice, discountPercent?, vatCodeId}], deliveryDate?
(default today), paymentTermsDays?, yourReference?, note?, payOnSite?}` — the draft's
rules (`parseDraft`, `inv/drafts.go:171`) for every field. **One transaction** (R4 §5.8):
the issue's body is refactored out of `PostInvoicesByIdIssue` (`inv/issue.go:254-555`)
into `issueLocked(ctx, tx, txq, locked, draft, profile, work, issueDateReq)`, which runs
steps 3–6 on a document already locked; `POST /{id}/issue` calls it after its `LockInvoice`,
the quick invoice after **inserting** the draft and its lines on the same transaction (the
new row is the one it holds, so the order stays document → settings → counter). Before
the transaction, as the issue does: the billing profile (the customer gates of
`customerGate`, `inv/drafts.go:63`, and the buyer snapshot), and 503
`storage_unavailable` without a store. Any refusal — the draft's 400s, the customer gates,
every issue check (`seller_incomplete`, **`buyer_incomplete`**, `issue_date_not_allowed`,
the VAT checks, `kid_length_exceeded`) — rolls back everything: no draft is left behind
and no number is taken. 201 with the issued document; the PDF stored after the commit
(`storeAfterIssue`). An ordinary invoice in the ordinary series, terms and due date as any
other (the profile's, else the settings'), work never (no sources).

**Paid later, it is a credit sale** (R4 §9.1 a): by KID through the bank, or through the
pay link after the tradesperson has left. No kassasystem; nothing more to do.

**Paid on site, it is kontantsalg** (R4 §9.1 b, §9.2). Vipps at delivery is "kontanter"
(bokføringsforskriften § 5-3-1 c; Skattedirektoratet 29.06.2017), and the invoice does not
change that — **the roadmap's "this is a credit sale" is wrong** (`ROADMAP.md:866-868`;
corrected in D23). It is lawful without a kassasystem only as a **kontantfaktura**
(§ 5-4-1 tredje ledd), read narrowly by Skattedirektoratet (07.01.2019): for a business
that ordinarily sells on credit to identified customers, never for retail or counter
sales, and every such sale needs a full delkapittel 5-1 invoice naming the buyer with an
address or an organisation number, whatever the amount (R4 §9.3). Skatteetaten's own Q&A
makes the same condition **the supplier's**: a system on which a kontantsalg can be
registered without the buyer's name and address is a kassasystem under kassasystemlova,
and an undeclared one (R4 §9.4). So the following are **hard product rules**, protecting
Vantigo as much as its users, and no setting relaxes them:

1. **The buyer is identified before any payment can start.** An on-site request is
   accepted only for an **issued invoice** (`kind = invoice`), and the issue already
   refuses `buyer_incomplete` unless the snapshot has a name and either a Norwegian
   organisation number or a complete address — line 1, city and country, and the postal
   code for NO (`buyerComplete`, `inv/issue.go:154-166`): § 5-1-2's "navn og adresse
   eller organisasjonsnummer", which is exactly the kontantfaktura's content. **Where it
   falls short**: it checks that the fields are present, not that they are true — a
   customer registered as "Kontantkunde" with an invented address passes; Vantigo cannot
   tell, and the acknowledgement below puts that on the user. A foreign business with a
   foreign id and no address is refused (not complete), which is right. The request
   re-judges `buyerComplete` on the stored snapshot as a floor (409
   `buyer_identity_missing`), and the quick invoice requires a `customerId` — **there is
   no anonymous, "kontantkunde" or receipt-only path anywhere in the product** until a
   Point of sale module ships with a declared kassasystem (R4 §9.5 requirement 3).
2. **One payment, one invoice.** An on-site attempt covers the invoice's whole open amount
   (for a fresh quick invoice, its gross) and nothing else; one live on-site attempt per
   invoice (the partial unique index of D16).
3. **The notice.** On-site payment is off until an `invoices:manage` holder turns it on
   **with the acknowledgement** (D14), whose text the settings card shows in full: on-site
   payment is kontantsalg; it is allowed without a kassasystem only under
   bokføringsforskriften § 5-4-1 tredje ledd, for a business that mainly sells on credit
   to identified customers; a shop or counter sale needs a produkterklært kassasystem;
   the buyer must be named truthfully. The quick invoice screen repeats a one-line
   reminder beside "Betal med Vipps nå".
4. **The time of payment and a daily list.** Every attempt records `authorized_at` and
   `captured_at`; `GET /invoices/vipps-payments?date=` (`invoices:access`) lists one Oslo
   day's Vipps payments — time, invoice, buyer, amount, flow (`on_site` or `pay_link`) —
   with the day's totals per flow, as JSON and as CSV (`.csv`, the module's format), for
   reconciliation against the Vipps settlement (§ 5-4-5's daily count, which arguably
   reaches kontantfaktura sales — **UNCERTAIN**, R4 §9.6 item 4; built in).
5. **Not limited to business buyers** — the exemption covers identified private buyers,
   and the restriction would add nothing (R4 §9.5).

**The pay link on site is the same kontantsalg.** A customer who opens the pay link while
the tradesperson waits pays at handover — kontantsalg in substance (R4 §9.5, §9.6 item 2;
the internet-sale carve-out is **not** relied on, item 3). The pay page serves only issued
invoices, so rule 1 holds for it too; and **the quick invoice screen shows no pay-link QR
to scan on the spot unless on-site payment is enabled** — without it, the invoice is
handed over by e-mail or EHF and paid later. The daily list includes pay-link payments,
since Vantigo cannot know where the customer stood.

**The on-site request.** `payOnSite: true` on the quick invoice, or
`POST /invoices/{id}/payment-requests` (`invoices:issue`) `{flow: "on_site"}` on an
issued invoice: 409 `on_site_payments_unavailable` (the setting, the switch, the
credentials), `invoice_draft`, `credit_note_no_payments`, `invoice_settled`,
`invoice_handed_off`, `buyer_identity_missing`, `payment_request_active` (a live on-site
attempt), `amount_out_of_range`; 502 `provider_failed`. D16's create with `flow =
on_site`, `QR`, `customerInteraction: CUSTOMER_PRESENT`, `created_by_user_id` the caller,
no `returnUrl`. 201 `{attemptId, reference, qrSvg, expiresAt}` (on the quick invoice:
the document with `paymentRequest` beside it — when the invoice issued but the request
failed, 201 with the document and `paymentRequestError`, since the invoice stands).
`GET /invoices/{id}/payment-requests/{attemptId}` (`invoices:issue`) →
`{state, capturedAt?}`, polled by the tablet every two seconds; `POST …/cancel`
(`invoices:issue`) → `cancelling`. The QR is Vipps' one-time QR, valid ten minutes, never
printed (R4 §4.1).

**Tests**: the quick invoice in one transaction (the number and the document, no draft
left by any refusal — each by its guard, `buyer_incomplete` among them; the PDF after;
the refactored issue's own suite unchanged); the on-site rules (refused without the
setting and the acknowledgement; refused on a draft, a credit note, an incomplete
snapshot written by a test past the issue; one live attempt; the QR flow and
`CUSTOMER_PRESENT` sent; the daily list's day boundaries in Oslo, totals and CSV); the
screen offers no pay-link QR with on-site off. **Race**: `TestQuickInvoice_RacesIssue` —
a quick invoice and an ordinary issue on a pool of two: both numbered, no gap, no
`40P01`.

### D18 — The lock order, restated

The module's invariant (`R/invoices.md:938-957`) — **locks in descending id among
invoices**, and document → settings → counter → original → the source modules' rows —
gains the new rows. Each path, in its order:

| Path | Locks |
| --- | --- |
| a bank match (D4) | the bank transaction, then its invoice |
| the queue's apply (D5) | the bank transaction, then its invoices in descending id |
| dismiss, reversal handled, reopen | the bank transaction alone |
| a charge payment, a hold, a lift, a hand-off, its withdrawal | the invoice alone (a hand-off then its letters, D11) |
| a reminder run's item (D10) | the invoice (the letter is inserted) |
| the reminder worker's dispatch, the print | the invoice, then its letter |
| a policy `PUT` | the policy row; the merge: the documents, then the policy rows by customer id |
| an attempt's create | the invoice (the attempt is inserted) |
| the capture decision, the registration | the attempt, then its invoice |
| the issue and the quick invoice | the document (locked or inserted), settings, counter, as today; a pay link inserted |

**No path locks an invoice and then a bank transaction or an attempt**, so the two
"module row first" orders cannot cycle with each other or with the payments' and credit
notes' invoice-only and descending locks; a removal of an imported or Vipps payment
locks only the invoice and never writes the transaction or the attempt (D2). Every
provider call, object-store call, directory read and SMTP send is made **outside** any
transaction that holds a lock — MB rule 10's restatement, kept by the harness's
contract-call hook, which gains `payments.<op>` and `smtp.reminder` through
`contractscalls.go` so a test fails on one made under a lock.

**Clock reads.** Every request and every worker claim reads `Deps.Clock()` **once** and
derives its Oslo day with `businessDay` (`inv/values.go:32`): the import (`uploaded_at`,
`registered_at`, the booking-date sanity check), the queue's actions, a run (its
`run_on` and each item's engine day — one read for the whole run), each dispatch claim
(`sent_on`, the lease), the print, the overdue list, the attention items, a pay-page
request, an attempt's create and each poll claim (`expires_at`, `next_poll_at`, `paid_on`
of a capture). Every query over the new tables takes `@now` or `@today` as a parameter,
never `now()` or `CURRENT_DATE` (R4 §5.4; phase 2 reading 17).

**Tests**: the lock-order seam records each path's order in its own test; every race
named in D3–D17 runs on a pool of `MaxConns = 2` with each raw lock-holding transaction on
its own `pgx.Connect`, every probe `NOWAIT` from its own connection, and
`pg_stat_database.deadlocks` 0 after each (MB's race conventions).

### D19 — The slots, retention and privacy

- **Merge**: payments, charge payments, letters, holds, hand-offs, pay links and attempts
  hang off the document; bank transactions off the file; only the reminder policy is
  keyed by customer and is re-pointed (D7).
- **A person's export**: each issued document gains `chargePayments`, `reminders` (level,
  status, dates, amounts, channel, recipient, regime), `holds` (with notes),
  `collectionHandoffs`, `payAttempts` (state, amount, times — never `redirect_url`), and
  each payment its `source` and, for an imported one, the bank line's date, debtor name,
  debtor account and text (a bank reference often names the payer); the section gains
  `reminderPolicy`.
- **Anonymisation**, inside the customers module's transaction, after today's steps and
  in this order: every `queued`, `awaiting_print` or `failed` letter of the person's
  documents **withdrawn** (`customer_anonymised`); every letter's recipient blanked; the
  notes of charge payments, holds (and lift notes) and hand-offs blanked; every live pay
  link revoked; the policy row deleted. Reported, after today's five kinds:
  `invoices.reminders` (withdrawn and blanked), `invoices.charge_payments` (notes),
  `invoices.invoice_holds`, `invoices.collection_handoffs`, `invoices.pay_links`
  (revoked), `invoices.customer_reminder_policies` (deleted). **Kept**: the sent letters
  (the documentation of the claim, and the bad-debt VAT relief's evidence — "minst tre
  purringskrav", FMVA § 4-7-1, R4 §2.9), charge payments, hand-offs, attempts, and the
  bank files and transactions — the bank's record of money received, bookkeeping material
  under § 13 like the payments (reading 11). The letters' and the PDFs' buyer data is the
  snapshot's, kept as the invoice's is. The marker refuses any later letter
  (`customer_anonymised`) and blanks a letter row inserted after it.
- **Retention**: bank files, letters and their PDFs are kept five years after the end of
  the financial year, as the invoice PDFs; the module deletes no object.

**Tests**: export carries each; erase does each and reports each; run twice reports zeros;
a letter racing the erase inserted with its recipient blanked.

### D20 — Out of scope, named

Customer credit balances, refunds as a flow (and the port's `Refund` used by Invoices),
setting an overpayment off against the next invoice, rounding off small differences
(R4 §3.5 f); an export of payments or charges for the accountant; bank APIs and direct
file delivery (DNB Connect, an ERP kundeenhet-ID); camt.053 and bank reconciliation of
non-customer movements; the Vipps Report API and settlement reconciliation (a payout is
dismissed as `vipps_payout`); MobilePay markets, other providers, cards, `PUSH_MESSAGE`,
long-living payments; the creditor's own betalingsoppfordring (INKL § 10, 3/20) — its fee
ends with the new law; the § 19 forskrift's egeninkasso fees; an agreed B2B interest rate
(R4); interest on fees; the chapter 2 cost caps; letters as EHF or eFaktura (the
customers module's own rule, `srv/customers/billing_values.go:215-216`), SMS; a letter
for charges alone; an agency API and an automatic hand-off; a group-level reminder
policy; the B2B ≤ 60-day term check (R19); several KID lengths on one agreement; several
seller accounts with different formats; automatic webhook re-registration; a "paid" stamp
or receipt on the PDF; anything of a kassasystem — counter sales, receipts, X/Z reports
(Point of sale).

### D21 — The OpenAPI contract

`openapi/invoices.yaml` (the API reference regenerates from it):

- **4A**: `postInvoicesBankFiles` (multipart), `getInvoicesBankFiles`,
  `getInvoicesBankFilesById`, `postInvoicesBankFilesByIdMatch`,
  `getInvoicesBankTransactions`, `postInvoicesBankTransactionsByIdApply`, `…Dismiss`,
  `…HandleReversal`, `…Reopen`. Schemas `InvoicesBankFile`, `InvoicesBankImportResult`,
  `InvoicesBankTransaction`, `InvoicesBankTransactionSuggestion`, `InvoicesAllocation`;
  the payment gains `source`, `bankTransactionId`.
- **4B**: `getInvoicesOverdue`, `postInvoicesReminderRuns`, `getInvoicesReminderRuns`,
  `getInvoicesReminderRunsById`, `getInvoicesRemindersByIdPdf`, `postInvoicesRemindersPrint`
  (`application/pdf`), `postInvoicesRemindersByIdWithdraw`, `…Retry`,
  `postInvoicesByIdHold`, `postInvoicesByIdHoldLift`, `postInvoicesByIdCollection`,
  `postInvoicesByIdCollectionWithdraw`, `getInvoicesCollectionExportCsv`,
  `postInvoicesByIdChargePayments`, `postInvoicesByIdChargePaymentsByChargePaymentIdRemove`,
  `getInvoicesCustomersByCustomerIdReminderPolicy`, `put…`, `getInvoicesCollectionRates`,
  `postInvoicesCollectionRates`, `deleteInvoicesCollectionRatesById`,
  `getInvoicesSettingsReminders`, `putInvoicesSettingsReminders`,
  `getInvoicesSettingsPayments`, `putInvoicesSettingsPayments`, `getInvoicesStatsAttention`.
  The document gains `charges`, `reminders`, `hold`, `handoff`, `nextAction`.
- **4C**: `postInvoicesQuick`, `postInvoicesByIdPaymentRequests`,
  `getInvoicesByIdPaymentRequestsByAttemptId`, `…Cancel`, `postInvoicesByIdPayLink`,
  `…Revoke`, `getInvoicesPayByToken`, `postInvoicesPayByTokenAttempts`,
  `getInvoicesPayByTokenAttemptsByReference` (the three anonymous),
  `postInvoicesVippsWebhooks` (anonymous), `getInvoicesVippsPayments` (+ `.csv`),
  `get/put/deleteInvoicesSettingsVipps`, `postInvoicesSettingsVippsVerify`,
  `post/deleteInvoicesSettingsVippsWebhook`; the document gains `payLink` and
  `paymentAttempts`, the payment `paymentAttemptId`; meta D1's fields.
- **Codes** on `InvoicesConflictProblem`: `bank_import_format_mismatch`,
  `bank_account_unknown`, `bank_file_duplicate` (+ `bankFileId`, `uploadedAt`,
  `uploadedBy`), `bank_transaction_not_open`, `bank_transaction_not_applicable`,
  `bank_transaction_applied`, `allocation_not_an_invoice`,
  `allocation_exceeds_transaction`, `paid_before_issue`,
  `charge_payment_exceeds_outstanding` (+ `chargesOutstanding`), `no_charges_outstanding`,
  `collection_rate_exists`, `collection_rate_in_force`, `collection_rates_outdated` (+
  `kind`, `halfYear`), `reminders_disabled`, `reminder_not_failed`,
  `reminder_not_awaiting_print`, `reminder_not_sent`, `reminder_not_withdrawable`,
  `credit_note_no_reminders`, `invoice_on_hold`, `invoice_not_on_hold`,
  `invoice_handed_off`, `invoice_not_handed_off`, `payments_unavailable`,
  `payment_attempts_active`, `public_url_missing`, `provider_failed` (502),
  `pay_links_unavailable`, `pay_link_exists`, `too_many_attempts`,
  `on_site_payments_unavailable`, `buyer_identity_missing`, `payment_request_active`,
  `amount_out_of_range`. Reasons and skip reasons as enums. Warnings:
  `reminder_email_missing`, `mail_unavailable`, `collection_rates_outdated`.

### D22 — Frontend

All in `@vantigo/invoices-ui` (`fe/`), mounted by host routes; en + nb throughout in
`invoicesCatalog` (`fe/i18n.ts`), the host's nav and attention sentences in the host
catalog.

- **Payments — "Innbetalinger" / "Payments"** (`host/routes/invoices/payments.tsx`,
  `invoices:payments`): upload a bank file (format chosen on first upload, the
  duplicate and refusal messages in words), the import's result
  (`/invoices/payments/files/$bankFileId`: matched, exceptions, skipped, ignored, "Match
  the rest"), the **exception queue** (filters by reason and file; each line's KID or
  text, debtor and amounts; the suggestions; "Apply" — a dialog splitting the amount
  across invoices and their charges with the remaining shown; "Not a customer payment";
  "Reversal handled" with the matching payments to remove; "Reopen"), and from 4C a
  **Vipps** tab with the day's list and its CSV.
- **Overdue — "Forfalt" / "Overdue"** (`host/routes/invoices/overdue.tsx`,
  `invoices:access`; actions `invoices:payments`): the list with its filters and each
  invoice's next action and reasons; "Send reminders" opens the **run preview** (the
  letters as they would go today, deselectable, the warnings) and makes the run; the
  run's result page `/invoices/reminder-runs/$runId` with "Print the paper letters"
  (the combined PDF; "print and post today").
- **The invoice page** (`fe/pages/invoice.tsx`): a **Reminders card** (each letter with
  its level, status, dates, amounts and PDF; withdraw and retry), the **charges** and
  "Register a charge payment", **hold** and **lift** (with the charges question), **hand
  off to collection** and its withdrawal and the export of this invoice, the payments'
  source and bank line, and from 4C the pay link (copy, revoke, make one) and "Take a
  Vipps payment now" (the QR, polled, with the kontantsalg line) when allowed.
- **Settings** (`fe/pages/settings.tsx`): a **Reminders** card (D7's fields, the regime
  date with its explanation) and a **Collection rates** card (the rows, in force
  highlighted, add a future row, delete an unused one); a **Bank files** card (the
  format); from 4C a **Vipps** card (MSN, keys write-only, verify, the webhook) and a
  **Pay links and on-site payment** card (the switches, the URLs, the acknowledgement's
  full text and checkbox, `PUBLIC_BASE_URL` missing explained).
- **The customer**: a **Reminder policy** card in `CustomerInvoicesPanel` on the customer's
  Invoices tab (`host/routes/customers/-customer-invoices-tab.tsx`), `invoices:access` to
  read, `invoices:payments` to change.
- **The quick invoice** (4C, `host/routes/invoices/quick.tsx`, `canQuickInvoice`): one
  screen for a tablet — the customer (search, or "New customer" through the customers
  API when the caller holds `customers:create`; the buyer's address shown and required
  for on-site payment), one to ten lines, "Issue" and, when on-site payment is
  available, "Issue and take payment with Vipps" (the QR full screen, the state polled,
  cancel); every refusal in words.
- **The pay page** (4C, `host/routes/pay.$token.tsx`, public): D15.
- The dashboard's attention sentences for the four types; the nav's two new entries
  (Payments, Overdue) behind their permissions.

### D23 — Documentation, as its own deliverable

Per `AGENTS.md`'s page map, in each pull request for what it ships:

- **`R/invoices.md`**: new sections "Bank files and the exception queue" (formats,
  pre-checks, dedupe, matching and its table, the reasons and actions, the allocation
  order), "Charges", "Reminders" (the rates and the two regimes, the settings and the
  customer policy, the engine's rules with their sources, runs and letters, the worker,
  paper, the letter's content), "Holds and the hand-off to collection" (and the CSV's
  columns), "Vipps payments" (the port, credentials, the pay page and pay links,
  attempts and the worker, capture and reservation, webhooks), "The quick invoice" (one
  transaction; credit sale vs kontantsalg; the hard rules; the daily list); the model
  table's new tables and columns; "Payments and the state of an invoice" (source,
  reservation, removal of imported payments); the lock order paragraph (D18); permissions
  (D1's table, rewritten descriptions); endpoints; retention and personal data (D19);
  stats (attention); and **"What comes next" corrected** — overpayment, customer credit
  balances and refunds as a flow are **not** phase 4 (R4 §6 item 2): an overpayment's
  rest stays visible on its bank line and a refund is made outside Vantigo; they move to
  the backlog with D20's items; the opening paragraph's "nothing is matched to a bank
  file" goes.
- **`MB`**: rule 1's platform list names `internal/payments` (the port, shared so Point of
  sale can use it; no SQL, rule 4) and its Vipps adapter; "Invoices requires customers"
  gains the directory read of the run and the export; the router's raw-body option.
- **`R/customers.md`**: the anonymisation table's six new kinds; the merge's reminder
  policy rule.
- **User guide** `en|nb/user/invoices.md`: "Importing payments from the bank", "The
  exception queue", "Overdue invoices and reminders", "Printing paper letters", "A
  disputed invoice", "Handing an invoice to collection", "Charges", "Reminder settings
  and rates", "A customer's reminder policy", and from 4C "Vipps and pay links", "The
  quick invoice", "Taking a payment on site" (what kontantsalg means for the user, in
  plain words) and "The day's Vipps payments"; the permissions section; `en|nb/user/customers.md`
  the policy card if the customer page is described there.
- **A new administration page** `en|nb/admin/payments.md` (listed in the admin overview;
  `sources` `apps/server/internal/payments` and the invoices files of the imports and the
  Vipps setup): **the bank agreement** — what to ask the bank for (an OCR/KID agreement,
  "Fakturere med KID"; for every incoming payment an eGiro / camt.054 "Innbetaling Total"
  agreement; tvungen KID), where to download the files in DNB's, Nordea's and SpareBank
  1's online banks (as R4 §3.4 found them), choosing one format, and what a duplicate or
  an unknown account means; **Vipps** — the merchant agreement ("Integrert betaling"),
  the sales unit's keys in the portal, test vs production and `INVOICES_VIPPS_BASE_URL`,
  `INVOICES_VIPPS_ENABLED`, `PUBLIC_BASE_URL` and the reverse proxy (https, the base path,
  the `Host` it forwards — the webhook is verified against the configured URL), the
  webhook, the `invoices-payments` worker; **the reminder worker** and mail; the
  kontantsalg notice for an operator enabling on-site payment.
- `admin/authentication.md`'s configuration reference: `PUBLIC_BASE_URL`,
  `INVOICES_VIPPS_ENABLED`, `INVOICES_VIPPS_BASE_URL`; `admin/installation.md` "Background
  workers and scaling": `invoices-reminders`, `invoices-payments`; `admin/object-storage.md`
  (en + nb): the `bank-files/` and `reminders/` keys; `deploy/compose` `vantigo.env.example`.
- **`ROADMAP.md`**: phase 4 as delivered with its design link; **the quick-invoice
  paragraph corrected** — "a credit sale when paid later; paid on site by Vipps it is
  kontantsalg, allowed without a kassasystem only as a kontantfaktura, which the quick
  invoice enforces (the buyer named with an address or an organisation number, one
  payment per invoice in the ordinary series, a notice, the day's list)"; **the Point of
  sale section corrected** — its line between "an invoice paid at once by Vipps" and a
  cash sale is drawn (R4 §9): an invoice paid at delivery is still kontantsalg; a
  business that sells over a counter needs a kassasystem whatever document it issues;
  Invoices' on-site payment covers only the kontantfaktura case, and research item 1 is
  narrowed to the register itself; the backlog gains D20's items.

The docs task checks every page against the code, as earlier phases did, and `mise run
docs:check` passes.

## Readings on the record

Each an interpretation the user may overturn.

1. **The 2026 regime by a dated setting.** `inkassolov_2026_from` (NULL until Kongen sets
   the date) switches, per letter on its date: no fee, no creditor's inkassovarsel, a last
   reminder announcing the inkassoforetak; interest and the B2B compensation continue.
   Encoding the § 19 forskrift is a later phase.
2. **Rates as seeded rows plus a screen.** A release seeds each half-year's values while
   maintained; `invoices:manage` adds a future row when an installation is not upgraded in
   time; rows in force are never changed.
3. **An outdated half-yearly rate refuses** the run and the dispatch rather than claiming
   last half-year's figure.
4. **Overpayment is left unapplied** on its bank line, visible; **no customer credit
   balance and no refunds** in phase 4 — a refund is made outside Vantigo and the line
   dismissed with a note. The reference page's "What comes next" is corrected.
5. **Interest from the day after the effective due date**, simple, actual/365, each day
   on the principal open at the end of the day before, split at every rate change and
   payment, rounded to øre once (R4 §7 items 2, 3).
6. **Principal first.** A payment pays the principal before any charge; a payment of
   charges is its own record; the order is recorded and explainable.
7. **The policy's home is Invoices** (`invoices.customer_reminder_policies`, under
   `invoices:payments`), not the customers billing profile; a merge keeps the stricter
   mode; anonymisation deletes the row.
8. **Weekend due dates move to Monday** for the engine (fees, interest, the first
   letter); holidays do not; the state `overdue` is unchanged.
9. **Fee or compensation is the installation's choice for business buyers**
   (`business_charge`), never both — they offset; a person is charged the fee or nothing.
   **The § 3a compensation is claimed once per invoice**, on its first letter, never with
   a reminder fee on that invoice, never for a person.
10. **The compensation's NOK figure is the one in force on the letter's date** (R4 §7 item
    7; the due date's is the alternative).
11. **Bank files and transactions are kept through anonymisation** as bookkeeping
    material; the person's export carries the matched lines' payer data.
12. **`paidOn` is the booking date** (OCR settlement date, camt `BookgDt`); a booking
    before the issue date is queued, never posted.
13. **Reminder e-mail is at least once**, with a stable Message-ID per letter; a letter's
    facts are written at sending and frozen once sent.
14. **Paper letters are dated the day they are printed**, and the screen says to post
    them that day.
15. **Capture at once** on `AUTHORIZED`, of min(authorized, open), with the reservation
    taken under the invoice's lock and the provider called outside it; the rest is
    cancelled.
16. **Poll first, webhooks optional**: the poll is the only path that changes an attempt;
    a verified webhook only nudges it. Webhooks ship only if the tagged test pins the
    signature.
17. **The pay page exists**, on `PUBLIC_BASE_URL` (https, unset by default), as a public
    route of the host SPA over anonymous operations; it shows the seller, the number, the
    amount open, the due date, the KID and the terms, never the buyer; it closes for a
    settled, credited, revoked or handed-off invoice.
18. **A pay link is made at issue** when available and printed with a QR in the PDF; an
    earlier invoice gets one on demand, in its e-mail and letters but never its PDF.
19. **The quick invoice paid on site is kontantsalg**, offered only as a kontantfaktura
    under D17's hard rules (R4 §9) — the buyer identified by the issue's own
    `buyerComplete`, one payment per invoice, the acknowledgement, the daily list; paid
    later it is a credit sale. The roadmap is corrected.
20. **The pay link opened on site is treated as kontantsalg too**: the quick invoice
    screen shows no pay-link QR unless on-site payment is enabled.
21. **One bank-import format per installation**, set by the first import.
22. **The fingerprint's ordinal** keeps identical lines of one file apart and dedupes
    overlapping files, at the residual risk R4 §3.6 names.
23. **Card information (OCR 18–21) and non-reversal debits are ignored**, counted;
    reversals are queued for a person.
24. **A hold's lift decides charges**: barred by default (a reasonable objection),
    allowed when the person says the objection was groundless; interest keeps running.
25. **Payments continue after a hand-off**, and the invoice view reminds the user to tell
    the agency; the pay page closes.
26. **No new permission**: reminders, imports and the hand-off are `invoices:payments`;
    the settings and rates `invoices:manage`; the on-site request and the pay link
    `invoices:issue`; the quick invoice `invoices:create` + `invoices:issue`; the overdue
    list `invoices:access`.
27. **`INVOICES_VIPPS_ENABLED`** defaults on, as `INVOICES_EHF_ENABLED` does.
28. **The betalingsoppfordring is not offered**, and a purring is not required before an
    inkassovarsel (`reminders_before_notice` may be 0).
29. **A reminder is not a salgsdokument** and takes no number (an inference, R4 §7 item 15).

## Testing

Through the invoices harness, plus a fake object store, the fixed clock (moved across Oslo
midnight, 1 January and 1 July, and to 2027 for the outdated-rate case), `Deps.HTTPTransport`
for `vippstest`, the SMTP seam recording messages, and committed fixtures — constructed OCR
files (R4 §3.1's example and one per rule), camt.054 files in both namespaces in our own
words (no third-party file in the repository, R4 §3.2), and the vendored ISO 20022 XSDs
with their NOTICE (R4 §3.7). Each decision's named tests are listed in it; in summary:

- **4A**: parsing and pre-checks per format; detection; the account, duplicate, format and
  fingerprint rules; stored once; the classification order and the match table; the queue's
  endpoints and suggestions; the payments' source CHECK and frozen columns; the races
  `TestBankImport_TwoOverlappingImports`, `TestBankImport_RacesManualPayment`,
  `TestBankQueue_ApplyRacesManualPayment`.
- **4B**: the seeds and rates API; the settings and the policy (merge, export, erase,
  `TestPolicy_MergeRacesPolicyPut`); the engine's table (`TestReminderRules_*`, every R-rule
  at its boundary, both regimes); charges and charge payments; preview, run, worker, print,
  withdraw, retry; letter goldens in both languages; holds and hand-offs; the export; the
  overdue list and attention; the races `TestReminderRun_RacesPayment`,
  `TestReminderRun_TwoRuns`, `TestReminderDispatch_RacesHold`,
  `TestReminderDispatch_RacesImport`, `TestHandoff_RacesReminderDispatch`.
- **4C**: the adapter over `vippstest`; the tagged `//go:build vipps` test; credentials,
  settings and `PUBLIC_BASE_URL`; pay links and the anonymous operations (no buyer data,
  rate limits, CSRF); attempts and the worker through every state; exactly-once; the
  webhook; the quick invoice's one transaction; the on-site rules; the daily list; the
  races `TestVippsCapture_RacesManualPayment`, `TestVippsCapture_TwoAttemptsBothAuthorized`,
  `TestVippsCapture_RacesImport`, `TestQuickInvoice_RacesIssue`.
- **The integration test** (`srv/integration`, real customers and invoices): issue two
  invoices with KIDs → import an OCR file paying one in full and one in part, plus an
  unknown KID → two payments and one exception → a reminder run on the partly paid one →
  the letter sent through the SMTP seam with its fee → a camt.054 paying principal and fee
  with the KID → a payment and a charge payment, the invoice paid, charges zero; then (4C)
  a quick invoice paid on site through `vippstest` → captured, registered once, on the
  day's list.
- **Frontend**: each screen and dialog with its refusals in words, the public pay page
  without a session, the attention sentences, both catalogs.
- **Docs**: D23's pages against the code; `mise run docs:check`.

## Phasing

Two pull requests, three sub-phases, each a working state with its docs:

- **4A — the ledger and bank imports** (PR 1): `00041`'s payments columns, bank files and
  transactions, payment settings' format; `kid.Parse`; the two parsers; the import, the
  match and the queue; the payments' `source` on the wire; the Payments screen; the
  reference, user and admin (bank agreement) sections.
- **4B — overdue and reminders** (PR 1): `00041`'s rates (seeded), reminder settings,
  customer policies, runs, letters, holds, hand-offs, charge payments; the engine; the
  worker and the letter PDF; the overdue list, attention, the export; the slots; the
  Overdue screen, the invoice cards, the settings cards, the customer card; the docs'
  remaining sections, the roadmap's correction of "What comes next".
- **4C — the payments port, Vipps, the pay page and the quick invoice** (PR 2, planned
  after PR 1 merges): `00042`; `srv/payments`, the Vipps adapter, `vippstest`, the tagged
  test; `PUBLIC_BASE_URL`, the switches; credentials and payment settings; pay links, the
  anonymous operations, the router's raw-body option and the webhook; attempts and the
  worker; the issue refactor and the quick invoice; on-site payment and the daily list;
  the pay page and the quick invoice screen; MB rule 1; the admin page's Vipps half; the
  roadmap's quick-invoice and Point of sale corrections.

## Open questions

What only the user can answer; the design's interim answer in brackets.

1. **A Vipps merchant agreement and a test sales unit** for Vantigo's own tagged test
   (R4 §4.8: no anonymous sandbox; an organisation number, a bank account, eID). [4C's
   adapter is built against `vippstest`; the tagged test waits for the keys.]
2. **Which bank and which files** for the user's own account: does it deliver camt.054
   (DNB "Total payment"/eGiro, MPS "Innbetaling Total") or only OCR, and can a real file
   (anonymised) be used as a private fixture? [Both formats are built; the user picks
   one.]
3. **Webhooks without a pinned signature**: if no real test-environment webhook can be
   captured before 4C, ship poll-only? [Yes — the receiver is left out, reading 16.]
4. **Defaults**: late interest on or off (off), B2B fee or compensation (fee), reminders
   before the inkassovarsel (1), the first reminder at 14 days.
5. **Vipps' view** on paying an issued invoice through standard "Integrert betaling", the
   terms-acceptance rule for it, and whether on-site and remote payments need separate
   sales units (R4 §7 items 31, 32). [One sales unit; the pay page asks for acceptance.]
6. **Exposing the installation**: will the user's own deployment get a public https URL
   for the pay page (`PUBLIC_BASE_URL`, e.g. a tunnel), or run QR-only? [Both work.]
7. **The kontantsalg acknowledgement's wording**: should the user's accountant or
   revisor review it before on-site payment is turned on anywhere? [The text follows
   R4 §9 verbatim in substance.]
8. **The collection agency**: which inkassobyrå, and will it take the CSV as is? [The
   implied minimum set of R4 §2.10; no standard exists.]
9. **The new inkassolov's in-force date** when announced: set by a release's migration,
   by the user in settings, or both? [Both are possible; the setting is NULL until then.]
