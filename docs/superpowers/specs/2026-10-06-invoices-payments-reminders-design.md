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

**Status:** design, revision 2, for the user's verdict on the readings. **Scope:** money in —
the bank tells Vantigo what was paid, Vantigo tells the customer what is late, and a Vipps
request lets a customer pay from a link or on the spot. **Sub-phases:** 4A the ledger and
bank imports, 4B overdue and reminders (pull request 1, receivables); 4C the payments port,
Vipps, the pay page and the quick invoice (pull request 2, planned after pull request 1
merges). Migrations `00041` (PR 1) and `00042` (PR 2).

The delivery:

- **the payments ledger** grows a source (`manual`, `ocr`, `camt054`, `vipps`), an
  optional registering user that only a Vipps capture may lack, and a link to the bank
  transaction or the payment attempt it came from;
- **bank files** — OCR giro and camt.054 (`.001.02` and `.001.08`) uploaded by a person,
  checked all-or-nothing, stored once, deduplicated per file and per transaction, a format
  per account with a cutover, and **matched on KID** into payments, with every other line
  — a possible duplicate among them — in an **exception queue** a person resolves;
- **reminders under dated rules** — the inkassosats, the late-interest rate and the § 3a
  compensation as dated rows; a regime that must be reviewed before it lapses; an overdue
  list; reminder runs previewed then made, letters delivered by a worker or printed and
  posted; fees, compensation and interest kept apart from the principal and waivable; a
  hold for a disputed invoice; a recorded delivery as the condition of every charge; the
  hand-off to a collection agency and its CSV;
- **the payments port** in a shared server package with **Vipps MobilePay ePayment** as
  its first adapter; a **pay page** on a public host serving nothing else; a poll worker
  that reserves under the invoice's lock, captures outside it and never lets go of a
  capture it cannot disprove; optional webhooks;
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
   (`inv/server.go:99-118`, MB rule 10's restatement). The worker decides the amount
   under the lock and **reserves** it (a reservation the open amount counts), commits,
   captures outside any lock, and registers the payment in a second locked transaction.
2. **Matching runs one bank transaction per database transaction** (D4), after the
   file's rows commit, not inside one long transaction: the lock order is the bank
   transaction, then its one invoice; a crash leaves rows `pending`, which
   `POST /invoices/bank-files/{id}/match` finishes. Only the queue's multi-invoice actions
   lock several invoices, in descending id.
3. **The webhook receiver lives under the module's mount**,
   `POST /api/v1/invoices/vipps/webhooks` — modules mount only at `/api/v1/<name>/`
   (`srv/module/module.go`, MB rule 5) — and needs a new router option that keeps the raw
   body (D16).
4. **`PUBLIC_BASE_URL` does not exist yet** (`srv/config/config.go:111-116` knows only
   `APP_URL` and `APP_BASE_PATH`); it is new platform configuration with its own host
   model (D14).
5. **The pay page is a public route of the host SPA** over three anonymous operations of
   the invoices contract (`x-vantigo-access: anonymous`, which the router supports,
   `srv/module/router_test.go:31`), not a server-rendered page: the server's mux has no
   module hook for pages (`srv/server/server.go:62-67`) (D15).
6. **Payments gain `payment_attempt_id`** (a unique foreign key) instead of
   `provider_payment_id`: the attempt row carries every provider id, and the unique key is
   the exactly-once guard (D2, D16).
7. **An auto-match covers principal, then charges outstanding** (D4): a customer who pays
   a reminder's total with the invoice's KID would otherwise always land in the queue as
   an overpayment. Only what exceeds both is queued, `exceeds_open`.
8. **`currency` is a file-level refusal, not a queue reason** (D3): the pre-checks are
   all-or-nothing per file. The queue gains `paid_before_issue`, `account_mismatch`,
   `negative_amount`, `possible_duplicate` and `payment_removed` (D5).
9. **The import format is per account, with a cutover** (D3): the OCR archive reference
   and camt's `AcctSvcrRef` are not known to match (R4 §7 item 29), so the same KID payment
   imported from both formats would register twice. (Revision 1 replaced the first
   draft's single installation-wide format.)
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
    waited in the queue or for the post must not lose days.
13. **Rates**: an unused future row can be deleted (a typo must be correctable), a seed
    never collides with a row the user added, and a run that needs a half-yearly rate for
    a half-year with no row is refused, `collection_rates_outdated` (D6).
14. **The § 3a compensation continues after the new inkassolov's in-force date**: it is
    forsinkelsesrenteloven, which LOV-2026-05-22-19 does not repeal for business debtors
    (R4 §2.1 item 6 amends only the consumer rule). Only fees stop (D6, D8).
15. **`INVOICES_VIPPS_ENABLED`** (default on) is added beside the base URL — phase 2's
    operator switch — and `workers` (`inv/module.go:101-106`) no longer returns nothing
    when EHF is off (D1).
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

**Revision 1** — after the critical review of `89ef73c5` (REVISE: 5 BLOCKER, 21 IMPORTANT,
18 MINOR; its "must survive" list kept whole) and the coordinator's decisions on it. Each
finding and what changed:

| Id | Finding | Change |
| --- | --- | --- |
| B1 | the six-month reset read as a sliding window | D8: the count runs back from the last fee letter and stops at a gap of six months or more; a fee is refused while it is 2; months are added by a clamped helper, `addMonthsClamped`, never `AddDate`; a test table with 31 Aug |
| B2 | interest counted twice in a letter's total | D9/D10: `charges_earlier` is earlier fees and compensation less their waivers and payments, never interest; interest is shown once, cumulative to the letter's date, with what of it was paid |
| B3 | a barring lift still re-demanded earlier fees; no waiver | D9: `invoices.charge_waivers` and `POST /invoices/{id}/charges/waive`; D11: a barring lift waives every fee and compensation claimed; waived charges leave letters, the export, auto-match |
| B4 | a format change re-registered payments | D3: a format per account with `cutover_through`; D4: lines booked on or before it, and lines matching R4's soft key against a registered line of another file, are queued `possible_duplicate` |
| B5 | an unknown capture outcome released the reservation | D16: `capture_unknown` keeps the reservation and keeps polling; `capture_failed` only on a definitive answer; an `invoices:manage` abandon with an acknowledgement; a partial capture registered as captured |
| I1 | the public host could not work and would expose everything | D14: `PUBLIC_BASE_URL` may name another host, same base path; `HostFilter` admits it; on it an exact allowlist, everything else 404; tests |
| I2 | the pay page's polling exceeded its limit | D15: a status bucket of its own; polling backs off; D23: `TRUSTED_PROXY_HOPS`/`CIDRS` in the admin page |
| I3 | runs on stale bank data; the OCR blind spot | D10: the run shows the last import's date; past `stale_import_days` a run with any charge needs `acknowledgeStaleImport` (409 `bank_import_stale`); a standing OCR warning |
| I4 | fees on undelivered invoices | D8: a charge needs a recorded delivery on or before the due date — an e-mail, a delivered EHF transmission, or a new manual delivery record; the quick invoice records "handed over" |
| I5 | paper dated before posting; zero grace; `ordered_on` unused | D10: paper letters are printed for a posting date and become `sent` only when the batch is confirmed posted on or before it; `grace_days` ≥ 1; D8: `ordered_on` decides whether a deadline was met, and a fee claimed after a met deadline is waived at match |
| I6 | compensation to a "business" without an org number | D8: business **and** an organisation number (or a foreign business id) on the snapshot; a NULL type is a person |
| I7 | interest on amounts later credited | kept by the coordinator's decision: interest on the principal net of each credit note **from its date**, the period split there too (reading 5) |
| I8 | no guard when the 2026 regime lapses unnoticed | D6: `regime_reviewed_through` (seeded 2026-12-31); after it a fee or a creditor's inkassovarsel is refused `collection_regime_unreviewed` until a manager confirms; fee-free letters continue |
| I9 | no waiver | B3's |
| I10 | letters in flight not engine input | D8: any queued, awaiting, printed or failed letter blocks, `letter_pending` |
| I11 | outdated rates at dispatch unspecified | D10: the worker reschedules an hour out, uncounted, with an attention item |
| I12 | a seed collides with a user's row | D6: seeds `ON CONFLICT DO UPDATE SET release_value` once; a warning when they differ |
| I13 | a matched line stranded after its payment's removal | D5: `reopen` from `matched` with no live payment → `exception`, `payment_removed`; matched lines with an unapplied rest listed |
| I14 | `handle-reversal` removed nothing | D5: it removes the named payments in its own transaction, or records `noPayment` with a note; else 409 `reversal_payment_required` |
| I15 | the CHECK cleared the reason `reopen` restores; events table missing | D3: the reason is kept once set; `bank_transaction_events` in the schema |
| I16 | skipped duplicates not kept | D3: kept as `duplicate` rows with `duplicate_of_id`; a partial unique index; the queue can treat one as distinct |
| I17 | a 0.00 camt amount failed the import | D3: ignored and counted, `zero_amount` |
| I18 | the first import's settings write unplaced in the lock order | D3/D18: the account rows are the import transaction's first statements, in account order |
| I19 | terminal states contradicted the remainder cancel | D16: `release_pending` on `captured`/`capture_failed` rows, indexed as due, until the cancel succeeds |
| I20 | credential changes and the switch stranded reservations | D14: a `PUT` changing the sales unit or keys refused while attempts are live; the MSN on each attempt; D16: the abandon path; reservations stay and raise attention with the switch off |
| I21 | the pay link on site bypassed the notice | D15/D17: the app shows no pay link or its QR on screen, and the PDF opens in no "show the customer" mode, unless on-site payment is enabled; turning pay links on shows the kontantsalg notice too; the PDF and the e-mail keep the link (remote payment is a credit sale); reading 20 says what remains advisory |
| M1 | `registeredByUserId` is required in the contract | D2/D21: optional, a contract change named |
| M2 | "same body as revoked" contradicted | D15: the claim dropped; a revoked link answers `payable: false`, `revoked` |
| M3 | the token in the logged path | D15: the token travels in the query string (`/pay?token=`), stored as its SHA-256, the URL sealed for re-use |
| M4 | references could collide across installations | D16: an installation prefix in every reference |
| M5 | payment settings changed shape between PRs | the format moved to the bank accounts (PR 1); `payment_settings` is PR 2 only |
| M6 | the reservation depends on 4C | D2: PR 1's `openOf` is unchanged |
| M7 | `action` filter vs SQL paging | D12: the whole overdue set is judged before paging, at most 5 000 |
| M8 | `publicPaths` call sites | moot: `/pay` is an exact path, so the set and its four call sites stay |
| M9 | camt parser hardening | D3: `Strict`, no `DOCTYPE`, a depth and element cap |
| M10 | notes left on erase | D19: resolution and event notes of lines linked to the person's payments blanked |
| M11 | who registers on `…/match` | D3: the caller |
| M12 | the combined PDF downloadable once | D10: print batches, re-downloadable |
| M13 | D18 incomplete | D18: every new write listed |
| M14 | `kid.Parse` overflow | D4: a body beyond `int64` is `kid_unknown` |
| M15 | § 5-4-5 documentation | D17: an immutable day-reconciliation record |
| M16 | a credit note after a capture | D12: attention `invoiceRefundDue` for any invoice whose open amount is below zero |
| M17 | bad-debt relief needs three reminders | D23: the user guide says the default sequence makes two |
| M18 | citations | fixed (`inv/payments.go:43`, `host/lib/public-paths.ts:1-10`, `inv/server.go:99-118`) |
| gap | a refund made in the Vipps portal | D16: captured attempts re-read daily for 30 days; a refund raises `vippsRefundSeen` |
| open | an objection after a fee; e-mail for a varsel; a partial capture | waived by default on a barring lift (B3); open question 10; registered as captured (B5) |

Nothing was rejected outright. Three findings are answered differently from the critic's
proposal, by the coordinator's decisions: I5's +2-day paper deadline became a confirmed
posting date; I5's two-business-day grace became a minimum of one day (`grace_days`
default 3); I7 keeps the credit note's own date. I19's suggested `releasing` state became a
`release_pending` flag on the two terminal states it concerns, so the outcome is not lost.

**Revision 2** — after the critic's re-check of `19d3e405` (APPROVE WITH CHANGES: 1 new
BLOCKER, 4 IMPORTANT, 12 MINOR; every original finding confirmed closed). Each finding and
what changed:

| Id | Finding | Change |
| --- | --- | --- |
| NB1 | a paper letter posted early kept a fee judged for a later day | D10: `postedOn` must equal `postOn`; earlier → 409 `reminder_posted_early`, later → `reminder_posted_late`; reprint (reading 39) |
| NI1 | interest waivers subtracted twice between the letter and D9 | D8/D9: interest always cumulative from the day after `E`; an interest waiver is an amount (claimed and unpaid); the letter gains `interest_waived`; one formula serves the letter and D9, `interest_paid` capped net of waived; a test of waiver then later letter |
| NI2 | the `duplicate` rows contradicted their schema | D3: the CHECK is an implication; `duplicate_of_id` frozen; the fingerprint index partial on `duplicate_of_id IS NULL`; D5: confirm-duplicate and treat-as-distinct set reason `possible_duplicate` |
| NI3 | an undelivered invoice could still get the inkassovarsel and the hand-off | D8: without a delivery only fee-free reminders; the notice and the hand-off `blocked`, `not_delivered` (reading 40) |
| NI4 | the posted confirmation did not re-judge | D10: it re-judges each letter at `L = postOn` under the locks D18 takes; a letter whose invoice was paid, held, handed off or anonymised, or whose outcome no longer carries its charge, is `sent` with that charge waived `claimed_in_error`; a test |
| m1 | the pay page's SPA polls the system status | D14: `GET /api/v1/identity/system/status` allowlisted; the citations fixed to `host/routes/__root.tsx:84-89, 91-96, 237` |
| m2 | the public origin needlessly trusted for CSRF | D14: dropped; same-origin POST passes; cookies host-only |
| m3 | the token reached Vipps and proxy logs | D15: the return URL carries the attempt reference and a nonce, never the token; the page keeps the token in `sessionStorage`; D23: the admin page says to strip `/pay`'s query from the proxy's access log (reading 41) |
| m4 | the camt element cap could refuse a lawful file | D3: 10 000 + 100 per transaction (510 000 at the cap) |
| m5 | the six-month anniversary day | D8: `>` — the fee is allowed from the day after the anniversary; the chain breaks only at a gap of more than six months; the table redone (reading 32) |
| m6 | deleting a user's rate left the half-year empty | D6: the delete re-inserts the release's value as a seeded row |
| m7 | a manual delivery could never be removed | D8: removal with a reason, refused `delivery_relied_on` while a sent letter's charge relies on it |
| m8 | "no import ever" | D10: stale |
| m9 | a paper batch past the review or a missing half-year | D10: those letters are left out of the batch, stay `awaiting_print`, and are reported |
| m10 | a late-resolved capture dated by the claim day | D13/D16: `CaptureTime` from the event log dates the payment (reading 42) |
| m11 | the print batch's lock mode | D10/D18: the batch row `FOR NO KEY UPDATE` |
| m12 | the soft key's same-day repeat payment | D4: a test of a genuine second payment queued, never lost; D23: the user guide explains it |

Nothing was rejected. For m3 the mitigation chosen is the nonce in the return URL, so the
token never reaches Vipps; the token's own trip through the customer's link remains, and
the admin page covers the proxy log.

## Decisions

### D1 — Scope, phasing, authority and the switches

**No new permission.** Checked against the reference's permission table
(`R/invoices.md:1985-2034`) and `inv/module.go:39-65`:

| Key | Gains in phase 4 |
| --- | --- |
| `invoices:access` | the overdue list, an invoice's reminders, hold, hand-off, deliveries and charges, the collection rates, the day's Vipps payments and reconciliations, the attention items about overdue invoices and refunds due |
| `invoices:payments` | bank imports and the bank accounts list, the exception queue and its actions, reminder runs, printing, posting, withdrawing and retrying letters, holds, the hand-off and the collection export, charge payments and waivers, the per-customer reminder policy, a day's reconciliation, the attention items about the queue, letters and captures |
| `invoices:manage` | the reminder settings and the regime review, adding or deleting a future collection rate, an account's import format, the payment settings, the Vipps credentials and webhook, abandoning a stuck payment attempt |
| `invoices:issue` | a manual delivery record, a pay link, an on-site payment request on an issued invoice |
| `invoices:create` + `invoices:issue` | the quick invoice (`permission:invoices:access+invoices:create+invoices:issue`, the three-key grammar `openapi/customers.yaml:4398` already uses) |
| anonymous | the pay page's three operations and the webhook receiver, each rate-limited |

Why reminders are not `invoices:issue`: a reminder is not a sales document and takes no
number (R4 §1.2 item 7); it is credit control — "what the company says it is owed", the
very reason `invoices:payments` is sensitive (`R/invoices.md:1997-1998`). Why the on-site
request and the manual delivery are `invoices:issue`: handing the sale over belongs to
whoever issues and sends it, and neither registers money by hand. The permission
descriptions in `inv/module.go:41-63` and the reference's table are rewritten to say all
this.

**The switches** (`srv/config/config.go`, by its conventions; the configuration reference):

- `PUBLIC_BASE_URL` — platform, unset by default (D14).
- `INVOICES_VIPPS_ENABLED` — default **on**; `0`: every provider call stops, meta's
  `payLinksAvailable` and `onSitePaymentsAvailable` are false, the payment worker does
  not start, a pay page answers `unavailable`; live reservations stay counted and appear
  as attention items until a manager abandons them (D16).
- `INVOICES_VIPPS_BASE_URL` — default `https://api.vipps.no`, validated by `httpBaseURL`
  (`config.go:516`) like the Storecove URL; the operator's, never the tenant's. The test
  environment is `https://apitest.vipps.no` (R4 §4.8).

`workers` (`inv/module.go:101-106`) becomes a list built per switch: the two EHF workers
when `InvoicesEhfEnabled`; `invoices-reminders` (D10) always; `invoices-payments` (D16)
when `InvoicesVippsEnabled`.

**Meta** (`GET /invoices/meta`) grows `remindersEnabled`, `payLinksAvailable`,
`onSitePaymentsAvailable`, `vippsCredentialsRejected`, and capabilities
`canImportBankFiles`, `canRunReminders` (both `invoices:payments`),
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
NULL` — **only a Vipps capture lacks a user**. A bank import is uploaded by a person and its
auto-match runs in that request, so the uploader is the registering user (the caller of
`…/match` when that finishes the job); the queue's actions are their caller's. The
nullable-user precedent is `transmissions.resolved_by_user_id`
(`mig/00036_invoices_ehf_kid.sql:92,104`).

`tr_payments_immutable` (`mig/00035…:67-89`) needs no change: it compares `to_jsonb(OLD)`
less the removal columns and the note, so every new column is frozen with the rest, and
`ALTER … DROP NOT NULL` is DDL, which it does not see (R4 §5.1). `bank_transaction_id` is
**not unique**: one bank line may be applied to several invoices (D5); `payment_attempt_id`
is, one capture being one payment.

**The open amount** stays principal-only. **In PR 1 `openOf` (`inv/payments.go:93-108`)
is unchanged.** In 4C it becomes gross − credited − live payments − **live reservations**
(D16: the Vipps captures decided and not yet registered or disproved). A manual
registration over a reservation is refused `payment_exceeds_open`, its detail saying a
Vipps payment of that amount is being captured. `document_state` (`mig/00035…:190-200`)
is unchanged: it sees the paid sum, and a reservation is not a payment.

**The registration and removal endpoints keep their rules** (`R/invoices.md:1054-1098`):
a manual payment is `source = manual`. A removal of an imported or Vipps payment is
allowed and is how a refund made outside Vantigo is recorded; it never touches the bank
transaction or the attempt — the line's applied amount is derived from its live payments,
so the lock order of D18 holds, and D5's `reopen` brings the line back to the queue.

**The response and the contract**: each payment answers `source`, and
`bankTransactionId` or `paymentAttemptId`. `registeredByUserId` leaves the schema's
`required` list (`openapi/invoices.yaml:1433-1441`) and is absent on a Vipps payment — a
contract change: the generated client types make it optional, and the frontend's payments
card shows "Vipps" in its place. The personal-data export carries no registering user
today and is unaffected.

**Tests**: `TestPayments_SourceCheck` (each invalid origin refused by the CHECK, a manual
row without a user refused, a Vipps row with a user refused — after `00042`);
`TestPayments_ExistingRowsAreManual` (a migration test over a 1B fixture);
`TestPayments_NewColumnsFrozen`; `TestPayments_ReservationCountsInOpen` (4C).

### D3 — Bank files: two formats, the account's format, stored once, imported once

**`POST /invoices/bank-files`** (`invoices:access+invoices:payments`): multipart, one part
named `file`, at most **10 MiB** (`BodyLimits`, the customers import's pattern,
`srv/customers/import.go:92-150`). In order:

1. 400 on `file` — a missing, second or empty part, or one past the limit.
2. **Detect the format**: the first non-blank line, CR/LF stripped, is 80 characters and
   begins `NY000010` → **OCR giro**; the document's root is `Document` in the namespace
   `urn:iso:std:iso:20022:tech:xsd:camt.054.001.02` or `…001.08` → **camt.054**; anything
   else → 400 on `file`, "Not an OCR giro or camt.054 file".
3. **Parse and pre-check, all or nothing** (R4 §3.1, §3.2, §3.6); any failure is a 400 on
   `file` naming the record or element and nothing is stored:
   - **OCR**: every record exactly 80 characters, starting `NY`; the grammar `10 (20 (30 31
     [32])+ 88)+ 89`; items 2 and 3 share item 1's transaction number and type, item 3
     iff type 20 or 21; per assignment and per transmission the transaction count, the
     record count (start and end records included), the **signed** amount sum (a credit
     note's `-` subtracts; a type 18/20 reversal adds, as the specification says) and the
     first and last settlement date equal the `88` and `89` fields; service code 09 only;
     DDMMYY with the century window 2000-2099; KID characters digits, a trailing `-`
     allowed (MOD11). Item-3 text is decoded as ISO-8859-1 (R4 §7 item 20), leniently.
   - **camt.054**: `encoding/xml` with `Decoder.Strict = true`, any `DOCTYPE` refused, at
     most 64 levels of nesting and 10 000 + 100 per transaction elements — 510 000 for
     a file at the 5 000-transaction cap, room for a `.001.02` `TxDtls` of 30–40 elements
     (a hostile file is refused, not parsed); both namespaces' paths (R4 §3.2's table); per entry the `TxDtls` amounts sum
     to `Ntry/Amt` and their count equals `Btch/NbOfTxs` when present, and `TxsSummry`
     agrees when present; every `Ccy` is `NOK` (else 400 — **currency is a file-level
     refusal**, research case m); amounts at most two decimals. No runtime XSD validation
     (R4 §3.7): the vendored XSDs are a test oracle (`mise run bankfiles:validate`).
   - **Both**: every booking date on or before today (Oslo, the request's one clock read)
     and not before 2000-01-01; at most 5 000 transactions in a file, since each is matched
     in a transaction of its own.
4. **The account** (case m): every OCR `Oppdragskonto`, every camt `Ntfctn/Acct` (a NO IBAN
   normalised to its 11-digit BBAN) must be the seller's `bank_account`
   (`mig/00034…:30`) **or** a `seller_bank_account` some issued invoice snapshotted
   (`:153`). Otherwise 409 `bank_account_unknown` naming the account's last four digits.
5. **The file, twice** (R4 §3.6): its SHA-256 and its own identity — OCR `(Dataavsender,
   Forsendelsesnummer, Datamottaker)` of `NY000010`, camt `(MsgId, CreDtTm)` — are each
   unique; either already imported → 409 **`bank_file_duplicate`** with `bankFileId`,
   `uploadedAt` and `uploadedBy` of the earlier one. Read first on the pool for the quick
   answer, enforced by the unique indexes in step 7.
6. **Stored once**: the bytes under `bank-files/<sha256>.<ocr|xml>` (`Exists` before
   `Put`, the PDF's `storeOnce` shape, `inv/pdfstore.go:268-306`), outside any
   transaction; 503 `storage_unavailable` without an object store. The file is the
   documentation of the payments booked from it (bokføringsloven § 10), kept like the
   PDFs. An object stored by a request that then fails is harmless.
7. **One transaction**, in this order:
   1. **the file's accounts, in account order**: `INSERT INTO invoices.bank_import_accounts
      (account, format, …) VALUES … ON CONFLICT (account) DO NOTHING` — the first import of
      an account sets its format — then `SELECT … FOR SHARE` of those rows; an account
      whose format differs from the file's → 409 **`bank_import_format_mismatch`** naming
      the account and its format. These are the transaction's first statements, so two
      first imports of overlapping files queue on the same rows in the same order (I18);
   2. the file row (a unique violation on the hash or identity maps to
      `bank_file_duplicate`);
   3. every transaction row **in one `INSERT … ON CONFLICT DO NOTHING RETURNING` ordered
      by fingerprint** against `ux_bank_transactions_fingerprint`, so two overlapping
      imports wait on the index in the same order and never deadlock (phase 3's
      `line_sources` rule, reading 33);
   4. the rows the conflict skipped, inserted again as **`duplicate`** rows with
      `duplicate_of_id` the live row they collided with (the partial index, `WHERE
      duplicate_of_id IS NULL`, excludes them for good), so a skipped line is kept and
      visible, not only counted (I16).
8. **Matching** (D4), one transaction per bank transaction, after the commit.
9. **201** with the import's result: the file row, `transactions`, `matched` and
   `matchedAmount`, `exceptions` and `exceptionsAmount`, `duplicates`, `ignored` (by
   kind: `debit`, `not_booked`, `card_information`, `zero_amount`), and `pending`
   (non-zero only when matching stopped early).

**What becomes a transaction.** OCR: every amount item of types 10–17 (the giro kinds);
types 18–21 (card information) are **ignored and counted** — Vantigo has no terminal
agreement (R4 §3.1); a negative (`-`) line is a transaction with `negative` set, queued
`negative_amount`. camt: every `TxDtls` of a `BOOK`ed `CRDT` entry (an entry without
`TxDtls` is one transaction of its own amount); a `DBIT` entry is stored only when it is a
reversal — `RvslInd = true` or the bank code `PMNT/ICDT-RCDT/RRTN` (R4 §3.2) — as
`direction = debit`, queued `reversal`; any other `DBIT`, any entry not `BOOK`ed and **any
transaction of 0.00** (R4 §3.2: "may be 0.00") is ignored and counted.

**The fingerprint** (R4 §3.6's proposal): SHA-256 over the receiving account, the booking
(OCR: settlement) date, the amount in øre with its sign, the KID or the normalised text
(trimmed, case-folded, whitespace collapsed), the debtor account if present, the archive
reference if present (OCR item 2's `Arkivreferanse`; camt `TxDtls/Refs/AcctSvcrRef`), and
an **ordinal** — the n-th line with all the rest identical within the same file. The same
file, or a file overlapping it, reproduces the ordinals and its lines become `duplicate`
rows; two identical payments in one file stay two. Two genuine identical payments of one
day split across two files without a reference collide — R4's residual risk — and the
second is the `duplicate` row the person can treat as distinct (D5).

**The account's format and its cutover** (B4). `invoices.bank_import_accounts`:
`account varchar(11) PRIMARY KEY`, `format varchar(10) CHECK (format IN
('ocr','camt054'))`, `previous_format varchar(10)`, `cutover_through date` — the last
booking date the previous format's files covered for this account — `set_by_user_id uuid
NOT NULL`, `set_at`. `GET /invoices/bank-accounts` (`invoices:payments`) lists each with
its last file and last booked date. `PUT /invoices/bank-accounts/{account}/format
{format}` (`invoices:manage`): the row `FOR UPDATE`; the same format → 200, nothing
changed; another → `previous_format` the old one, `cutover_through` the latest
`last_booked_on` of the account's files in the old format (NULL when none), `format` the
new one; 404 for an account never imported. Matching then holds back what the old format
may already have registered (D4). A change of bank is a new account and gets its own row.

**Schema** (`00041`):

```text
invoices.bank_import_accounts — above
invoices.bank_files
  id bigint identity (START WITH 1001) PK, format varchar(10) CHECK (format IN ('ocr','camt054')),
  sha256 char(64) NOT NULL UNIQUE, file_identity varchar(200) NOT NULL,   -- OCR "sender:transmission:recipient", camt "MsgId|CreDtTm"
  object_key varchar(300) NOT NULL, byte_size integer NOT NULL,
  accounts varchar(11)[] NOT NULL, first_booked_on date, last_booked_on date,
  transactions integer NOT NULL, duplicates integer NOT NULL, ignored integer NOT NULL,
  uploaded_by_user_id uuid NOT NULL, uploaded_at timestamptz NOT NULL,
  UNIQUE (format, file_identity)
invoices.bank_transactions
  id bigint identity PK, bank_file_id bigint NOT NULL REFERENCES invoices.bank_files ON DELETE RESTRICT,
  line_ref varchar(60) NOT NULL,                 -- OCR "assignment/transaction", camt "notification/entry/tx"
  format varchar(10) NOT NULL,                   -- the file's, denormalised for the soft key
  account varchar(11) NOT NULL, direction varchar(6) CHECK (direction IN ('credit','debit')),
  negative boolean NOT NULL DEFAULT false,
  booked_on date NOT NULL, value_on date, ordered_on date,   -- ordered_on: OCR Oppdragsdato (D8)
  amount numeric(14,2) NOT NULL CHECK (amount > 0), currency char(3) NOT NULL CHECK (currency = 'NOK'),
  kid varchar(25), remittance_text varchar(1000) NOT NULL DEFAULT '',
  debtor_name varchar(140) NOT NULL DEFAULT '', debtor_account varchar(34) NOT NULL DEFAULT '',
  archive_ref varchar(35) NOT NULL DEFAULT '', bank_code varchar(35) NOT NULL DEFAULT '',
  fingerprint char(64) NOT NULL, ordinal smallint NOT NULL,
  duplicate_of_id bigint REFERENCES invoices.bank_transactions,
  status varchar(10) NOT NULL DEFAULT 'pending'
      CHECK (status IN ('pending','matched','exception','resolved','duplicate')),
  reason varchar(30),                              -- D5's codes: kept once set
  suggested_invoice_id bigint,                     -- the unambiguous suggestion of D5, no FK (a hint)
  resolution varchar(25) CHECK (resolution IN ('applied','not_customer_payment','reversal_handled','duplicate_confirmed')),
  resolved_by_user_id uuid, resolved_at timestamptz, resolution_note varchar(500) NOT NULL DEFAULT '',
  CONSTRAINT ck_bank_transactions_state CHECK (
      (status = 'exception') <= (reason IS NOT NULL)                       -- an exception has a reason; a resolved line keeps it
      AND (status = 'resolved') = (resolution IS NOT NULL AND resolved_at IS NOT NULL)
      AND (status = 'duplicate') <= (duplicate_of_id IS NOT NULL))     -- an implication: the link outlives the status
  ux_bank_transactions_fingerprint UNIQUE (account, fingerprint) WHERE duplicate_of_id IS NULL
  ix_bank_transactions_open (status) WHERE status IN ('pending','exception','duplicate')
  ix_bank_transactions_file (bank_file_id)
  ix_bank_transactions_soft (account, booked_on, amount, kid) WHERE kid IS NOT NULL
invoices.bank_transaction_events
  id bigint identity PK, bank_transaction_id bigint NOT NULL REFERENCES invoices.bank_transactions,
  event varchar(20) CHECK (event IN ('matched','queued','applied','dismissed','reversal_handled',
        'reopened','duplicate_confirmed','treated_as_distinct')),
  reason varchar(30), note varchar(500) NOT NULL DEFAULT '', by_user_id uuid NOT NULL, at timestamptz NOT NULL
```

`bank_import_accounts` changes only through the format `PUT`; `bank_files` is never updated
or deleted; `bank_transaction_events` is insert-only (triggers, the payments' shape).
`bank_transactions` refuses DELETE and every UPDATE but its state columns — `status`,
`reason`, `suggested_invoice_id`, `resolution`, `resolved_by_user_id`, `resolved_at`,
`resolution_note` — comparing `to_jsonb` less those, as `refuse_payment_change` does; the
note may be blanked by an erase (D19). **`duplicate_of_id` is frozen** with the rest, so a
duplicate that is confirmed or treated as distinct keeps its link and stays out of the
fingerprint index (NI2). A line never goes back to `pending`.

**`POST /invoices/bank-files/{id}/match`** (`invoices:payments`): runs D4 over the file's
`pending` rows, the caller registering — 200 with the same counts; 404. `GET
/invoices/bank-files` (paged, newest first) and `GET /invoices/bank-files/{id}` (with its
transactions) show `pending > 0`, and the screen offers "Match the rest".

**Tests**: `TestBankFileOCR_Parse` over R4 §3.1's constructed example and a fixture for
every rule; `TestBankFileCamt_Parse` over both namespaces, R4 §3.2's example, our own
Danske-shaped fixture (the same entry `AcctSvcrRef` twice, no transaction reference,
`NTAV`, the KID keyed on `SCOR`), a `DBIT` reversal, a non-`BOOK` entry, an entry without
`TxDtls`, a 0.00 transaction, a sum mismatch, a USD amount, a `DOCTYPE`, a file past the
depth cap; `TestBankFile_XSDOracle` (tagged `bankfiles`, the vendored XSDs with their
NOTICE line, R4 §3.7); `TestBankFile_Detect`; `TestBankFile_AccountUnknown`;
`TestBankFile_Duplicate`; `TestBankFile_FirstImportSetsAccountFormat`,
`…_FormatMismatch`, `…_FormatChangeSetsCutover`; `TestBankFile_Fingerprint` (two identical
lines in one file both kept; the same file again → `duplicate` rows with their originals;
an overlap); `TestBankFile_StoredOnce`, `…_StorageUnavailable`; `TestBankFile_Immutable`.
**Race**: `TestBankImport_TwoOverlappingImports` (a pool of `MaxConns = 2`, the first held
after its inserts on a seam `bankImportAfterInsert`), with a **first-import variant** in
which neither account row exists yet: both finish, every shared line registered once and
the other copy `duplicate`, no `40P01`, `pg_stat_database.deadlocks` still 0.

### D4 — Matching on KID, and what a match registers

**`kid.Parse`** (new, `inv/kid/kid.go`, a leaf still): `Parse(s string) (body string,
number int64, fits bool, ok bool)` — 2 to 25 characters, every one but the last a digit,
the last a digit or `-`; `body` the digits without the check character; `number` its
value with leading zeros dropped, and `fits` false when it exceeds `int64` (a 25-digit KID
of another agreement). It does not judge the check digit; `Verify` (`kid.go:95-113`) does.

**Classifying a credit line**, in this order, each read on the pool (R4 §3.5):

1. `direction = debit` → `reversal`; `negative` → `negative_amount`.
2. **A possible duplicate** (B4): the account's `previous_format` is set, this line's
   format is the account's current one and it is booked on or before `cutover_through`;
   or **R4's soft key** — the same account, booking date, amount and KID — matches a line
   of **another file** that has a live payment or charge payment → `possible_duplicate`
   (judged again under the lock, step 8).
3. The text (`remittance_text` or camt `AddtlNtryInf`) matches `Vippsnr \d+` and the line
   has no KID → `vipps_payout` (R4 §4.9; never invoice-matched — the invoices were settled
   at capture).
4. No KID → `no_kid`.
5. `kid.Parse` fails, or its check character verifies under neither MOD10 nor MOD11 →
   `kid_invalid`.
6. The body does not fit `int64`, or no issued document has that `number`
   (`ux_invoices_number`, `mig/00034…:195`) with its stored `kid` **exactly** the line's
   and `kid.Verify(kid, kid_algorithm, number)` (R4 §5.2: no new index; a credit note's KID
   is NULL) → `kid_unknown`.
7. The invoice's `seller_bank_account` differs from the line's account →
   `account_mismatch`.
8. Otherwise the line is a candidate for that invoice, matched under its lock.

**Under the lock**, one READ COMMITTED transaction per bank transaction: **the bank
transaction `FOR UPDATE`** (still `pending`, else nothing to do), then **the invoice `FOR
UPDATE`** (`LockInvoice`), then every figure after it (the payments' rule,
`inv/payments.go:16-24`) — the soft key re-read first, so a concurrent import of the other
format that registered the same payment meanwhile is seen (→ `possible_duplicate`). With
`open` = gross − credited − paid (− reserved, 4C) and `charges` = the charges outstanding
(D9):

| Case (R4 §3.5) | Judged | Result |
| --- | --- | --- |
| c | `credited > 0` and `credited ≥ gross` | queued `invoice_credited` |
| — | `booked_on` before the issue date | queued `paid_before_issue` |
| d, e | `0 < amount ≤ open` | a payment of `amount` |
| — | `open > 0`, `open < amount ≤ open + charges` | a payment of `open` and a charge payment of the rest (D9) |
| — | `open ≤ 0`, `0 < amount ≤ charges` | a charge payment of `amount` |
| g, h | `open ≤ 0` and `amount > charges` | queued `invoice_settled` |
| f | `amount > open + charges` | queued `exceeds_open` |

A payment and a charge payment registered here are `source` the file's format,
`bank_transaction_id` the line, `paid_on` **the booking date** (OCR settlement date, camt
`BookgDt`), `reference` the KID, `registered_by_user_id` the uploader (or `…/match`'s
caller), `registered_at` the request's one clock read; then the line becomes `matched`
with a `matched` event. **The allocation is principal first**, then charges (reading 6),
recorded as two rows so it can be explained (new inkassolov § 16, R4 §2.7). Then, in the
same transaction, **the deadline-met waiver** (D8, I5): when the line carries `ordered_on`
and a sent letter's fee or compensation was claimed after an earlier letter's deadline that
the payments ordered on or before it — this one included — turn out to have met, that fee
is waived, `deadline_met` (D9). A handed-off or held invoice is matched like any other:
payments are always registered (D11).

**Tests**: `TestKidParse` (MOD10 and MOD11 bodies, the `-`, leading zeros, letters, too
short, too long, past `int64`); `TestMatch_Classify` (each step by removing its guard, in
order); `TestMatch_Cases` (each row of the table at its boundaries);
`TestMatch_PossibleDuplicate` (the cutover; the soft key across formats; a second genuine
payment of one file not flagged; **a genuine same-day, same-amount, same-KID second payment
in another file, with a different reference, queued `possible_duplicate` — never lost — and
applied from the queue**, the case the user guide explains, m12); `TestMatch_PaidOnIsBookingDate`;
`TestMatch_PrincipalThenCharges`; `TestMatch_DeadlineMetWaiver`;
`TestMatch_HeldAndHandedOffStillMatch`; `TestMatch_PendingFinishedByMatchEndpoint`.
**Races**: `TestBankImport_RacesManualPayment` — an import's match and a manual
registration of the whole open amount, the manual one held after its lock
(`paymentAfterLock`, `inv/payments.go:43`): the import waits, then queues
`invoice_settled`; reversed, the manual one is refused `invoice_settled`; never two
payments. `TestBankImport_OtherFormatRace` — an OCR and a camt import of the same payment,
each held after its line lock: one payment, the other line `possible_duplicate`.

### D5 — The exception queue

Every line not matched is `exception` with one **reason**, or `duplicate`:

| Reason | Case | What the person usually does |
| --- | --- | --- |
| `kid_invalid` | a | apply by hand, or dismiss |
| `kid_unknown` | b | apply, or dismiss (another system's KID) |
| `invoice_credited` | c | dismiss with a note — a refund is owed and made outside Vantigo |
| `invoice_settled` | g, h | the same |
| `exceeds_open` | f | apply part to the invoice (and its charges), the rest stays unapplied |
| `no_kid` | i, j | apply to one or several invoices from the suggestions |
| `negative_amount` | k | dismiss with a note |
| `reversal` | l | handle it: remove the payment it reverses (below) |
| `vipps_payout` | n | dismiss — "not a customer payment"; the Vipps payments were registered at capture |
| `paid_before_issue` | — | apply after checking, or dismiss |
| `account_mismatch` | — | apply after checking, or dismiss |
| `possible_duplicate` | — | confirm it a duplicate, or apply it as a distinct payment |
| `payment_removed` | — | a matched line whose payment was removed: apply again, or dismiss |
| (status `duplicate`) | — | the fingerprint's twin: confirm, or treat as distinct (→ `possible_duplicate`) |

**Suggestions** for `no_kid`, `kid_invalid`, `kid_unknown`, `payment_removed` (read on
`GET`, never auto-posted — R4 §3.5 j): an issued invoice number found as a whole word in
the text; an invoice whose open amount equals the line's amount; the invoices of the
customer whose earlier matched payments came from the same debtor account. Each names why;
an unambiguous one is kept in `suggested_invoice_id`. For `possible_duplicate` and
`duplicate`: the line it may repeat, with that line's payments.

**Endpoints** (`invoices:access+invoices:payments`):

- `GET /invoices/bank-transactions?status=&reason=&bankFileId=&unapplied=&from=&to=` —
  paged, oldest open first; each line with its file, the KID or text, the debtor, the
  amounts applied (live payments and charge payments referring to it), `unappliedAmount`,
  its suggestions when open, and its events. `unapplied=true` lists every `matched` or
  `resolved` line with an unapplied rest.
- `POST /invoices/bank-transactions/{id}/apply` `{allocations: [{invoiceId, amount,
  chargesAmount?}], note?}` — 1 to 20 allocations, each invoice once, amounts above 0
  with two decimals. Order: 400 on the fields; 404; 409 `bank_transaction_not_open` (not
  `exception`); `reversal` and `negative_amount` lines → 409
  `bank_transaction_not_applicable`. Then **one transaction: the line `FOR UPDATE`, then
  the invoices `FOR UPDATE` in descending id** (the module's invariant,
  `R/invoices.md:950-957`), and per invoice, after its lock: kind invoice and issued, else
  409 `allocation_not_an_invoice`; `amount ≤ open` else 409 `payment_exceeds_open` (with
  `invoiceId`, `openAmount`); `chargesAmount ≤ charges` else 409
  `charge_payment_exceeds_outstanding`; `booked_on` before its issue date → 409
  `paid_before_issue`; and Σ (amount + chargesAmount) ≤ the line's amount less what is
  already applied, else 409 `allocation_exceeds_transaction`. Each allocation writes a
  payment (and a charge payment) as D4 does, registered by the caller; the line becomes
  `resolved`, `applied`. **What is not applied stays visible** as `unappliedAmount` — no
  customer credit balance and no refund in phase 4 (reading 4).
- `POST /invoices/bank-transactions/{id}/dismiss` `{note}` (1–500 characters) — `resolved`,
  `not_customer_payment`; any reason but `reversal`. 409 `bank_transaction_not_open`.
- `POST /invoices/bank-transactions/{id}/handle-reversal` `{note, removePayments:
  [{invoiceId, paymentId}], noPayment?}` — only a `reversal` line (409
  `bank_transaction_not_applicable` otherwise). **One transaction: the line `FOR UPDATE`,
  then the named payments' invoices `FOR UPDATE` in descending id**, each payment removed
  through the removal's own rules (`R/invoices.md:1088-1098`; 404 a payment not its
  invoice's; 409 `payment_removed`) with the reason "Reversed by the bank: line {ref}"; the
  line `resolved`, `reversal_handled`. With no payment named, `noPayment: true` and a note
  saying why are required, else 409 **`reversal_payment_required`** (I14). The screen
  offers the live payments of the same amount and account as candidates; nothing links
  them automatically (R4 §7 item 22).
- `POST /invoices/bank-transactions/{id}/confirm-duplicate` `{note?}` — a
  `possible_duplicate` or `duplicate` line: `resolved`, `duplicate_confirmed`, its reason
  set to `possible_duplicate` (so a later reopen lands on a reason; NI2).
- `POST /invoices/bank-transactions/{id}/treat-as-distinct` — a `duplicate` line becomes
  `exception` with reason `possible_duplicate`, so it can be applied (I16); its
  `duplicate_of_id` stays, and it stays out of the fingerprint index.
- `POST /invoices/bank-transactions/{id}/reopen` — a `resolved` line back to `exception`
  with its reason, or a **`matched` line whose payments were all removed** back to
  `exception`, `payment_removed` (I13); refused 409 `bank_transaction_applied` while any
  live payment or charge payment refers to it. The line alone is locked. Resolutions are
  cleared on the line and kept in its events.

Every action writes its event (who, when, the note).

**Tests**: each endpoint's refusals in order, each by removing its guard; apply across
three invoices taking locks in descending id (the lock seam records the order);
`unappliedAmount` and the `unapplied` filter; reopen from `resolved` and from `matched`;
handle-reversal removing two payments, with `noPayment`, and refused bare; confirm and
treat-as-distinct; the events. **Races**: `TestBankQueue_ApplyRacesManualPayment` — an
apply over two invoices and a manual registration on the lower id: the apply refused for
that allocation and rolled back whole; `TestBankQueue_ReversalRacesApply` — a
handle-reversal and an apply naming the same invoice, both finishing in the descending
order, no `40P01`.

### D6 — Collection rates as dated data, the two regimes, and the review

`invoices.collection_rates` (`00041`):

```text
id bigint identity PK,
kind varchar(30) CHECK (kind IN ('late_interest_percent','inkassosats','b2b_compensation_nok')),
valid_from date NOT NULL, value numeric(10,2) NOT NULL CHECK (value > 0),
release_value numeric(10,2),                 -- what a release seeded for this (kind, valid_from), when it differs from a user's row
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
(2019-01-01, FOR-2018-12-20-2050) and 750 (2026-01-01, FOR-2025-12-19-2709).

**Append-only**: a trigger refuses every UPDATE but `release_value` set once from NULL,
and a DELETE of a seeded row (`created_by_user_id IS NULL`). **A later release's seed**
is `INSERT … ON CONFLICT (kind, valid_from) DO UPDATE SET release_value = EXCLUDED.value
WHERE collection_rates.release_value IS NULL AND collection_rates.value <> EXCLUDED.value`
— it never fails on a row a user added first (I12); the overdue list and the rates card
warn `collection_rate_differs_from_release` while a user's value differs from the
release's. The API adds the rest:

- `GET /invoices/collection-rates` (`invoices:access`) — every row by kind and date, each
  with `inForce` today, `usable` (no letter has used it) and `releaseValue`.
- `POST /invoices/collection-rates` (`invoices:access+invoices:manage`) `{kind, validFrom,
  value, sourceRef}` — `validFrom` after today (Oslo), else 400; the half-yearly kinds on
  1 January or 1 July, else 400; bounds — interest 0.01–30, compensation 100–2 000,
  inkassosats 100–5 000 (400); a duplicate → 409 `collection_rate_exists`. 201.
- `DELETE /invoices/collection-rates/{id}` (`invoices:manage`) — only while `validFrom` is
  after today and the row is not seeded: 409 `collection_rate_in_force`. 204; 404. **A
  deleted user row whose `release_value` is set is replaced in the same transaction by a
  seeded row of the release's value** (`created_by_user_id` NULL), so the half-year never
  goes empty and refuses every interest letter (m6).

**Reading the rates.** The engine (D8) reads the row in force **on the letter's date** for
the inkassosats and the compensation, and **on each day** of an interest period for the
rate, splitting at every change (FRL § 3, R2). **Outdated rates refuse**: when a letter
would need a half-yearly kind and the latest row of that kind starts before the current
half-year, the run answers 409 **`collection_rates_outdated`** naming the kind and
half-year, and the dispatch reschedules (D10); the overdue list warns the same.

**The two regimes.** `invoices.reminder_settings.inkassolov_2026_from date NULL` — the day
LOV-2026-05-22-19 enters into force, which Kongen has not yet set (R4 §2.1; signalled
2027-01-01). NULL or after the letter's date: **the 1988 regime** (INKL, INKF) — every rule
of D8. Set and reached: **the 2026 regime**: no fee on any letter (the creditor's own
fee-bearing kravbrev wait for the § 19 forskrift, which a later phase encodes); **no
creditor's inkassovarsel** (new § 20 is the inkassoforetak's); the last letter before the
hand-off announces the inkassoforetak (Prop. 3 L 12.5.5); late interest and the B2B § 3a
compensation continue (amendment 14). The regime is judged **per letter, on its date**,
and recorded on the letter.

**The review** (I8). `invoices.reminder_settings.regime_reviewed_through date NOT NULL`,
seeded **2026-12-31** — the last day before the signalled date. A letter dated after it,
under the 1988 regime (`inkassolov_2026_from` NULL or later than the letter), that would
carry **a fee or be a creditor's inkassovarsel** is refused: the run answers 409
**`collection_regime_unreviewed`**, the dispatch reschedules (D10), the overdue list and
the preview say so. Fee-free reminders continue. A manager clears it by setting
`inkassolov_2026_from` or by moving `regime_reviewed_through` forward (at most a year
beyond today) in the reminder settings, which records who and when; a release may do
either once the date is announced. Under a law whose in-force day is unknown, sending the
creditor's own fee-bearing varsel after the signalled day without anyone having looked is
the one failure this cannot allow.

**Tests**: `TestCollectionRates_Seeds` (every seeded row against R4 §2.11's tables);
`TestCollectionRates_AppendOnly`; `TestCollectionRates_ReleaseSeedOverUserRow` (no
failure, `release_value` set, the warning); each API refusal by its guard;
`TestCollectionRates_InForceOn` (1 January and 1 July); `TestCollectionRates_Outdated`;
`TestRegime_PerLetterDate`; `TestRegime_ReviewLapses` (2027-01-01 with neither setting
moved: a fee letter and a notice refused, a fee-free reminder made; moving the review
clears it).

### D7 — The reminder settings and the per-customer policy

**`invoices.reminder_settings`** (`00041`), one row (`id = 1`), off the settings row every
issue shares:

| Column | Rule | Default |
| --- | --- | --- |
| `enabled` | reminders offered at all | false |
| `first_reminder_days` | the first letter's earliest day after the effective due date (D8), 1–60 | 14 |
| `deadline_days` | every letter's deadline after its sending, 14–60 (≥ 14: INKL § 9, INKF § 1-3) | 14 |
| `grace_days` | days after a deadline before the next letter, **1**–10 (a payment ordered on the deadline day is on time and is booked later, INKF § 1-2 third paragraph, R4 §2.7) | 3 |
| `reminders_before_notice` | reminders before the inkassovarsel, 0–2 (a purring is not required before a varsel, FinKN 2023-845) | 1 |
| `collection_notice` | the creditor's inkassovarsel offered (1988 regime only) | true |
| `person_charge` | `fee` \| `none` — a consumer is never charged the compensation (FRL § 4 d) | `fee` |
| `business_charge` | `fee` \| `compensation` \| `none` — never both: they offset (INKF §§ 1-5, 2-6) | `fee` |
| `late_interest` | late interest claimed on letters | false |
| `stale_import_days` | how old the last imported booking may be before a charging run needs confirmation (D10), 1–30 | 3 |
| `inkassolov_2026_from` | D6 | NULL |
| `regime_reviewed_through`, `regime_reviewed_by_user_id`, `regime_reviewed_at` | D6 | 2026-12-31, NULL, the migration's time |
| `revision`, `updated_at`, `updated_by_user_id` | the settings' optimistic revision | |

`GET /invoices/settings/reminders` (`invoices:access`); `PUT` (`invoices:manage`), every
field required (null is a value only for `inkassolov_2026_from`), 400 on the field,
409 on a stale revision. Changing anything changes only letters created afterwards.

**The per-customer policy** — `invoices.customer_reminder_policies` (`00041`):
`customer_id integer PRIMARY KEY` (opaque), `mode varchar(12) CHECK (mode IN
('normal','no_charges','none'))`, `note varchar(500) NOT NULL DEFAULT ''`,
`updated_by_user_id uuid NOT NULL`, `updated_at`. **No row is `normal`.** `no_charges`:
letters without fee, compensation or interest; `none`: no letter at all (the invoice is
still listed overdue, `blocked`, `policy_none`).

`GET /invoices/customers/{customerId}/reminder-policy` (`invoices:access`; 200 with
`mode: normal` when there is no row); `PUT` (`invoices:payments`) `{mode, note}` — 400 on
the fields; the customer must have an issued invoice or a draft here, else 404. A `PUT`
of `normal` with an empty note deletes the row.

**Why an invoices table, not the customers billing profile** (R4 §6 item 1). The profile
carries *where* to send (`reminder_email`, `reminder_delivery`,
`mig/00019_customers_billing_profile.sql:10,15`) — contact data the customers module owns
and edits under its own permissions. *Whether* to remind and charge a customer is a
credit-control decision, the sensitive `invoices:payments` (D1), and no customers
permission should exempt a debtor from reminders. Group defaults are out of scope (D20).

**The slots** (`inv/customer_slots.go`): **merge** (`RepointCustomer`, after
`LockCustomerDocuments`, `:54-68`): `from`'s row moves to `into` when `into` has none; when
both have one, **the stricter mode wins** (`none` > `no_charges` > `normal`; reading 7)
and the notes are joined `into` first, ` / `, cut to 500; `from`'s row is deleted;
reported as `invoices.customer_reminder_policies`. Lock order: the documents first (as
today), then the policy rows by customer id ascending. **Export**: `reminderPolicy {mode,
note, updatedAt}`. **Erase**: the row is deleted.

**Tests**: settings defaults, bounds (`grace_days` 0 refused), revision and permission;
policy CRUD, the 404, the delete on `normal`; `TestPolicy_MergeStricterWins`; export and
erase; **race** `TestPolicy_MergeRacesPolicyPut`.

### D8 — The rules engine

One pure function, `inv/reminderrules.go` — `nextAction(in ruleInput) ruleOutcome` — fed
everything under the invoice's lock (D10) and tested exhaustively; the overdue list, the
preview, the run and the dispatch all call it, so the four never disagree. Its inputs:
the invoice (issue date, due date, `buyer_type`, `buyer_organisation_number` and
`buyer_foreign_id` from the snapshot — R4 §5.5: the snapshot, not a fresh directory read),
the principal's history (gross, each credit note's issue date and gross, each live
payment's `paid_on`, `ordered_on` (from its bank line, when any) and amount, each live
reservation), **the deliveries** (below), the charges (D9: letters, waivers, charge
payments), **every letter** — sent ones with their facts, and those **in flight**
(`queued`, `awaiting_print`, `printed`, `failed`) — the settings, the customer's mode, the
live hold and hand-off, the collection rates, and the day `L` it is asked about.

**Months** are added by one helper, `addMonthsClamped(d, n)`: the same day `n` months on,
clamped to that month's last day (31 August + 6 → 28 or 29 February; Go's `AddDate` would
give 3 March). Every "six months" below uses it.

**The effective due date** `E` is the due date, moved to the following Monday when it is a
Saturday or a Sunday (R4 §2.7, the lenient reading; holidays are not moved — reading 8).

**The delivery fact** (I4). An invoice not validly delivered does not fall due, and a
varsel or fee on it is invalid (FinKN 2017-492, R4 §2.6). **A charge — a fee, the
compensation or interest — needs a recorded delivery on or before the due date**: an
e-mail in `invoices.deliveries` (`mig/00035…:38-48`), an EHF transmission `delivered`, or
a **manual delivery** (`invoices.manual_deliveries`: `id`, `invoice_id`, `kind` `handed_over`
| `posted`, `delivered_on date`, `note`, `recorded_by_user_id`, `recorded_at`, and
`removed_at`, `removed_by_user_id`, `removal_reason` all or none — the payments' shape;
never deleted, changed only by the removal, once, and its note blanked on erase; inserted
under an issued invoice's `FOR SHARE`, the payments' parent trigger shape) — `POST
/invoices/{id}/manual-deliveries {kind, deliveredOn, note?}` (`invoices:issue`;
`deliveredOn` from the issue date to today, 400 otherwise; 409 `invoice_draft`,
`credit_note_no_reminders`), and `POST /invoices/{id}/manual-deliveries/{deliveryId}/remove
{reason}` (`invoices:issue`; the invoice locked; 404; 409 `delivery_removed`; **409
`delivery_relied_on` while a sent letter of the invoice carries a charge not waived** — a
mistaken record is then corrected by waiving those charges, `claimed_in_error`, and removing
it after; m7). The quick invoice records `handed_over` on its issue date in its own
transaction (D17). **Without a live one, the invoice is not due** (FinKN 2017-492): the
engine offers only **fee-free `reminder` letters** — no fee, compensation or interest, at
most `max(reminders_before_notice, 1)` of them — and the `collection_notice` and the
`hand_off` are `blocked`, `not_delivered`, until a delivery is recorded (NI3, reading 30).

**The next action**, the first that applies:

1. principal open ≤ 0 → `none` (charges may still be outstanding; D9);
2. a live hand-off → `none`, `handed_off`;
3. a live hold → `blocked`, `on_hold`;
4. the customer's mode `none` → `blocked`, `policy_none`; reminders not `enabled` →
   `blocked`, `reminders_disabled`;
5. **a letter in flight** → `blocked`, `letter_pending` (the worker, judging the letter it
   dispatches, leaves that one out) (I10);
6. no letter sent → `reminder` (or `collection_notice` when `reminders_before_notice = 0`
   under the 1988 regime), earliest `E + first_reminder_days`;
7. the last letter's deadline + `grace_days` not yet passed → `waiting`, earliest the day
   after it;
8. letters sent < `reminders_before_notice` → `reminder`;
9. under the 1988 regime, `collection_notice` on and none sent → `collection_notice`;
   under the 2026 regime one more `reminder` that **announces the hand-off**
   (`announces_collection`) when none did;
10. otherwise → `hand_off` (suggested; never automatic).

A letter whose day `L` is in the 2026 regime and whose level would be `collection_notice`
is a `reminder` with `announces_collection` (D6).

**A deadline met** (I5). A letter's deadline counts as met when the live payments
**ordered** on or before it — `ordered_on` where the bank line has one, else `paid_on` —
cover the principal open on the letter's `sent_on`. A met deadline allows no further
letter on that sequence step: the next letter is judged as if the met letter's step had
not been missed; and a fee claimed afterwards in reliance on the missed deadline is waived
when the proof arrives (D4's `deadline_met`).

**The fee** on a letter (`fee_kind = reminder_fee`), all of R4 §2.11's rules:

- the 1988 regime on `L` (R20), and the review not lapsed (D6);
- a delivery recorded on or before the due date;
- `L ≥ E + 14` (R7 — with `E`, never before the due date, so stricter);
- the charge setting for the buyer is `fee` and the mode is `normal`, and no lifted hold
  barred charges (D11);
- **the two-fee cap with the six-month reset** (R9, R11, B1): let `last` be the latest
  sent fee-bearing letter (a waived fee still counts — it was claimed). When
  `L > addMonthsClamped(last.sent_on, 6)` the count is **0** — the six months end on the
  anniversary, so the fee is allowed from **the day after** it (domstolloven § 148's month
  rule, the conservative reading; m5, reading 32). Otherwise count back from `last` through
  the earlier fee letters, **stopping at the first gap of more than six months** between two
  consecutive fee letters (`next.sent_on > addMonthsClamped(prev.sent_on, 6)`); a fee is
  allowed only while that count is **below 2**;
- a second fee only when the previous fee letter's deadline was at least 14 days after its
  `sent_on`, has passed by `L` and was **not met** (R10 — and above);
- the amount: the inkassosats in force on `L`, ÷ 20, **rounded to the nearest krone, .50
  up** (R8; 750 → 38).

The reset's dates, pinned as a table (fee letters → `L` → allowed?):

| Fee letters sent | `L` | Count | Fee |
| --- | --- | --- | --- |
| 1 Jan, 1 Feb | 2 Jul | 2 (Feb → Jan, a one-month gap) | refused |
| 1 Jan, 1 Feb | 1 Aug | 2 (the anniversary itself is still inside) | refused |
| 1 Jan, 1 Feb | 2 Aug | 0 (`L` > 1 Feb + 6 months) | allowed |
| 31 Aug | 28 Feb (non-leap) | 1 | allowed (below the cap) |
| 15 Aug, 31 Aug | 1 Mar (non-leap) | 0 (31 Aug + 6 clamps to 28 Feb; `AddDate` would give 3 Mar and a count of 2) | allowed |
| 31 Aug, 15 Sep | 15 Mar | 2 | refused |
| 31 Aug, 15 Sep | 16 Mar | 0 | allowed |
| 1 Jan, 2 Jul, 15 Jul | 1 Aug | 2 (2 Jul → 1 Jan is more than six months, the chain stops) | refused |
| 1 Jan, 2 Jul | 3 Jul | 1 (the chain stops at the gap) | allowed |
| 1 Jan, 1 Jul | 2 Jul | 2 (exactly six months does not break the chain) | refused |

A purring sent before `E + 14` carries no fee, not a refusal (R4 §2.5). The creditor's
betalingsoppfordring (3/20) is not offered (D20).

**The compensation** (`fee_kind = compensation`): **a business with an organisation
number** — `buyer_type = 'business'` and the snapshot's `buyer_organisation_number` or a
foreign business's `buyer_foreign_id`; a `business` without either, or a NULL
`buyer_type`, is treated as a person (`buyerSnapshot`, `inv/issue.go:114-151`, sets the
type from the profile even without a legal id) (I6, reading 9); `business_charge =
compensation`; mode `normal`; a delivery on or before the due date; no barring hold; **no
earlier letter of the invoice claimed it** — once per invoice, the NOK figure in force on
`L` (readings 9, 10), claimed on the first letter only. Under `compensation` no reminder
fee is ever claimed on that invoice (R6). Never on a person (FRL § 4 d), in either regime.

**Late interest** (`late_interest` on, mode `normal`, a delivery on or before the due
date): **simple** interest on the principal (R3), **always cumulative from the day after
`E`** to `L` inclusive — an interest waiver (D9) is an amount subtracted beside it, never a
new starting day (NI1): each day `d` bears `open(d − 1) × rate(d) / 100 / 365`, where `open(d − 1)` is
gross less the credit notes issued and the live payments paid on or before `d − 1` — so a
credit note reduces the principal **from its own date** and a payment from the day after
its `paid_on` (the interest runs to and including a payment's day; I7, reading 5) — and
`rate(d)` the row in force on `d`; actual/365; summed exactly and **rounded to øre once**,
half away from zero; the segments split at every rate change, payment and credit note.
Interest is never computed on fees or the compensation. The letter shows the rate(s), the
from-date and the cumulative amount (D10).

**The outcome** carries the action, its earliest date, the blocking reasons (`on_hold`,
`handed_off`, `policy_none`, `reminders_disabled`, `letter_pending`, `waiting`,
`not_delivered`, `collection_rates_outdated`, `collection_regime_unreviewed`), the charge
notes (`not_delivered`, `charges_barred`, `fee_cap_reached`, `fee_before_14_days`), and for a
letter its `level`, `announces_collection`, `regime`, `fee_kind`, fee, compensation,
interest with its segments `[{from, to, rate, base}]`, and the rate rows used.

**Tests** (`TestReminderRules_*`, table-driven, both regimes): `addMonthsClamped` over
month ends and leap years; the reset table above, every row; R7 at 13 and 14 days, with a
Saturday due date; R8's rounding at 700, 750, 725 (36.25 → 36) and 770 (38.50 → 39); R10
with a met deadline by `ordered_on`; R5/R6 (compensation once, never with a fee, never for
a person, never for a `business` without an organisation number, never for a NULL type);
`no_charges`; the delivery fact (none; after the due date; manual; EHF delivered); R16 (a
hold blocks; a barring lift bars fees and compensation, keeps interest); interest across
1 January and 1 July, across a partial payment and a credit note (split at its date), on
the day of payment, over a weekend due date, after an interest waiver; `letter_pending`
for each in-flight status and not for the dispatched one; the outdated-rate and
unreviewed-regime outcomes; R20; `reminders_before_notice = 0`.

### D9 — Charges are not principal

The invoice's open amount, its state and the payments ledger stay **principal-only**
(Finanstilsynet's 2020 letter: fees and interest are never folded into the principal,
R4 §2.2, §2.10). What a letter claims lives on the letter (D10): its `fee`,
`compensation` and `interest`.

**Charge waivers** (B3, I9) — `invoices.charge_waivers` (`00041`): `id`, `invoice_id`,
`reminder_id` (the letter whose charge it waives; for interest, the latest sent letter that
claimed it), `kind` `fee` | `compensation` | `interest`, `amount numeric(14,2) > 0`,
`interest_through date` (interest only: that letter's `sent_on`, for display),
`reason varchar(20) CHECK (reason IN ('objection_upheld','claimed_in_error','goodwill',
'deadline_met'))`, `note varchar(500)`, `waived_by_user_id uuid NOT NULL`, `waived_at`.
Insert-only (a trigger), inserted under an issued invoice's `FOR SHARE`. One waiver per
(letter, kind) for fees and the compensation (a unique index); interest waivers may follow
one another. `POST /invoices/{id}/charges/waive` (`invoices:access+invoices:payments`)
`{waivers: [{reminderId, kind}], reason, note}` — fee and compensation waive the letter's
whole charge; `{kind: interest}` waives **an amount**: the interest claimed by the latest
sent letter less every earlier interest waiver and the charge payments allocated to
interest — what is claimed and unpaid, nothing accrued since (NI1). Under the invoice's
lock; 404; 409 `charge_not_claimed` (the letter claimed no such charge, it is waived
already, or no interest is left unpaid). A waived charge leaves every later letter's `charges_earlier`, the charges
outstanding, the collection export's claimed figures and auto-match's `charges`; the
letters themselves are history and keep what they said.

**The charges outstanding** of an invoice are

```text
  Σ fee + Σ compensation over its sent letters − their waivers
+ the latest sent letter's cumulative interest − Σ interest waivers
− Σ its live charge payments
```

— the same terms as the letter's own total below, so the two can never disagree (NI1).

When negative (a charge paid, then waived) the invoice answers `chargesRefundDue`, a
figure; the refund is made outside Vantigo.

**Allocation within charges** (reading 31): a charge payment pays the fees and
compensation first, oldest letter first, then interest. So each letter can state, without
counting anything twice (B2):

- `charges_earlier` — **the earlier letters' fees and compensation, less their waivers and
  the charge payments allocated to them; never interest**;
- `interest` — **the cumulative interest to this letter's date, from the day after `E`**,
  the one interest figure the letter shows;
- `interest_waived` — Σ interest waivers so far (amounts);
- `interest_paid` — the charge payments allocated to interest so far, which the allocation
  caps at `interest − interest_waived` of the time (a payment beyond it is a charge refund
  due, never interest paid twice);
- the total: principal open + `charges_earlier` + this letter's fee or compensation +
  `interest` − `interest_waived` − `interest_paid`.

**A charge payment** — `invoices.charge_payments`:

```text
id bigint identity PK, invoice_id bigint NOT NULL REFERENCES invoices.invoices ON DELETE RESTRICT,
paid_on date NOT NULL, amount numeric(14,2) NOT NULL CHECK (amount > 0), currency char(3) NOT NULL,
source varchar(10) NOT NULL CHECK (source IN ('manual','ocr','camt054')),
bank_transaction_id bigint REFERENCES invoices.bank_transactions ON DELETE RESTRICT,
reference varchar(100) NOT NULL DEFAULT '', note varchar(500) NOT NULL DEFAULT '',
registered_by_user_id uuid NOT NULL, registered_at timestamptz NOT NULL,
removed_at, removed_by_user_id, removal_reason   -- all or none, as payments
CHECK ((source = 'manual') = (bank_transaction_id IS NULL))
```

with the payments' two triggers copied. `POST /invoices/{id}/charge-payments`
(`invoices:payments`): the payment's fields and rules (`parsePayment`), then under the
invoice's lock 409 `no_charges_outstanding` or `charge_payment_exceeds_outstanding` (with
`chargesOutstanding`); `POST /invoices/{id}/charge-payments/{chargePaymentId}/remove`
`{reason}` as the payment's removal. The queue's apply and the auto-match write them too.

The issued invoice answers `charges {claimed, waived, paid, outstanding, refundDue?,
interestToday?}` — `interestToday` the engine's cumulative interest to today when interest
is on (a figure, not a claim) — and `waivers`.

**VAT and bookkeeping** (R4 §2.9): a statutory fee and late interest are outside the VAT
base (mval. § 4-1 (2) b, c); the § 3a compensation very likely too (**UNCERTAIN**, R4 §7
item 14). A reminder takes no number from the salgsdokument series and is not treated as a
salgsdokument (**UNCERTAIN** as a statement, an inference from bokføringsforskriften
§ 5-1-1; R4 §7 item 15); the sent letter and its row are the documentation of the claim,
and a waiver of its release (bokføringsloven § 10). No export of charges in phase 4 (D20).

**Tests**: the formula (fees, the compensation once, the latest interest, waivers of each
kind, payments, removed ones ignored, `refundDue`); the allocation order;
**`TestCharges_InterestWaiverThenLaterLetter`** — interest 20, 12 of it paid, an interest
waiver (of 8), then a later letter with cumulative interest 33: the letter states 33 − 8 −
12 = 13, D9's outstanding interest is 13, and a payment of the letter's total auto-matches
without `exceeds_open` (NI1);
**`TestReminderLetter_TwoLettersWithInterestAndAChargePayment`** — a golden pair: letter 1
with a fee and interest, a charge payment between, letter 2 whose `charges_earlier`,
`interest`, `interest_paid` and total are each pinned and whose total equals principal +
outstanding charges; the waive endpoint's refusals; the triggers; the response block.

### D10 — Reminder runs and the letters

**`POST /invoices/reminder-runs`** (`invoices:access+invoices:payments`):

- `{dryRun: true}` — **the preview**: every issued invoice whose next action (D8) is
  `reminder` or `collection_notice` with its earliest date on or before today, each with
  the letter as it would be sent today (level, fee, compensation, interest, the total,
  the deadline, the channel and recipient, warnings and charge notes), the invoices
  blocked or waiting with their reasons, and **the bank data's freshness** (I3):
  `lastBookedOn` (the latest `last_booked_on` of any imported file, or none), `stale`
  when it is more than `stale_import_days` before today — **and always when no file was
  ever imported** (m8) — and, for every account whose
  format is `ocr`, the standing note that payments without a KID never reach an OCR file
  and must be registered by hand before a run. 200; nothing written, no lock taken.
- `{dryRun: false, items: [{invoiceId, action}], acknowledgeStaleImport?}` — **the run**,
  1–500 items, each an invoice the caller saw in a preview with the action it showed. 400
  on the fields; 409 `reminders_disabled`; 409 `collection_rates_outdated`; 409
  `collection_regime_unreviewed` when any item would carry a fee or be an inkassovarsel
  past the review (D6); **409 `bank_import_stale`** (with `lastBookedOn`) when the bank data
  is stale, any item would carry a fee, compensation or interest, and
  `acknowledgeStaleImport` is not `true` — fee-free letters are never held back by it.
  Before any lock, the billing profiles of the items' customers are read through the
  directory, one call per distinct customer (the recipient: `ReminderEmail`; the channel:
  `ReminderDelivery`, `paper` when it says so, `email` otherwise; `email` with no address
  → `paper` with the warning `reminder_email_missing`; mail not available → `paper` with
  `mail_unavailable`). Then the `invoices.reminder_runs` row (with `stale_import_acknowledged`
  and `last_booked_on`), and **per item one transaction**: the invoice `FOR UPDATE`, then
  every figure after it; the anonymisation marker → skipped `customer_anonymised`; the
  engine; an action different from the item's → skipped `action_changed`. Otherwise a
  letter row is inserted, `queued` for e-mail or `awaiting_print` for paper. 201 `{run,
  created: [reminder], skipped: [{invoiceId, reason}]}`. Two runs over one invoice
  serialise on its lock; the second sees the first's letter in flight (`letter_pending`)
  → `action_changed`.

**`invoices.reminder_runs`**: `id`, `run_on date`, `created_at`, `created_by_user_id`,
`letters`, `skipped`, `last_booked_on date`, `stale_import_acknowledged boolean`.
Immutable.

**`invoices.reminders`** (`00041`):

```text
id bigint identity PK, invoice_id bigint NOT NULL REFERENCES invoices.invoices ON DELETE RESTRICT,
run_id bigint NOT NULL REFERENCES invoices.reminder_runs,
print_batch_id bigint REFERENCES invoices.reminder_print_batches,
sequence smallint NOT NULL,                     -- the invoice's n-th letter
level varchar(20) CHECK (level IN ('reminder','collection_notice')),
announces_collection boolean NOT NULL DEFAULT false,
channel varchar(5) CHECK (channel IN ('email','paper')),
recipient varchar(254) NOT NULL DEFAULT '',     -- '' for paper, and once the customer is anonymised
language char(2) NOT NULL,                      -- the buyer snapshot's
created_at timestamptz NOT NULL, created_by_user_id uuid NOT NULL,
-- the letter's facts, written by each dispatch attempt (e-mail) or the print (paper), frozen once sent:
sent_on date, deadline date, regime varchar(15) CHECK (regime IN ('inkassolov_1988','inkassolov_2026')),
principal_open numeric(14,2), fee_kind varchar(15) CHECK (fee_kind IN ('none','reminder_fee','compensation')),
fee numeric(14,2), compensation numeric(14,2), charges_earlier numeric(14,2),
interest numeric(14,2), interest_waived numeric(14,2), interest_paid numeric(14,2), interest_from date, interest_segments jsonb,
inkassosats numeric(10,2), total numeric(14,2), charge_notes varchar(30)[],
pdf_object_key varchar(300), pdf_sha256 char(64), message_id varchar(200), sent_at timestamptz,
status varchar(15) NOT NULL CHECK (status IN ('queued','awaiting_print','printed','sent','withdrawn','failed')),
attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz, first_attempt_at timestamptz,
lease_id varchar(100), lease_until timestamptz, last_error varchar(500), failed_at timestamptz,
withdrawn_at timestamptz, withdrawn_by_user_id uuid, withdrawal_reason varchar(200),
UNIQUE (invoice_id, sequence)
INDEX ix_reminders_due (next_attempt_at) WHERE status = 'queued'
CHECK: sent ⇒ every fact and sent_at set; printed ⇒ every fact and print_batch_id; withdrawn ⇒ withdrawn_at and a reason; failed ⇒ failed_at
```

`UNIQUE (invoice_id, sequence)` is the floor under two runs. A parent trigger on INSERT
reads the invoice `FOR SHARE` (an issued invoice only; the anonymisation marker re-read
after the wait — the delivery insert's shape, `mig/00035…:156-177`) and blanks the
recipient for a marked customer. An immutability trigger: no DELETE; identity columns
never change; the facts change only while the status is `queued`, `awaiting_print`,
`printed` or `failed`; once `sent`, nothing changes but the PDF key and hash set once and
the recipient blanked; a `withdrawn` row changes nothing. A withdrawn letter keeps its
sequence.

**Why the facts are written at sending** (amendment 12): the deadline is "at least 14
days from sending" (INKL § 9) and a fee is judged on its letter's date (INKF § 1-2).

**The worker `invoices-reminders`** (`inv/reminder_worker.go`), the row-lease shape of
`EhfWorker` (`inv/ehf_worker.go:149`, 60-second lease, poll every 5 s): claim one `queued`
row whose `next_attempt_at ≤ @now` by conditional `UPDATE` over a `FOR UPDATE SKIP
LOCKED` pick (`inv/queries/transmissions.sql:56-73`'s shape), then:

1. **One transaction**: the invoice `FOR UPDATE`, then the letter `FOR UPDATE` — re-read:
   still `queued` (a withdrawal meanwhile ends the claim) — then the engine at `L = today`
   (the claim's one clock read), leaving this letter out of the in-flight set. **Outdated
   rates or an unreviewed regime** (I11): the letter stays `queued`, `next_attempt_at` an
   hour out, the try **not** counted towards the 48 hours, an attention item
   (`collectionRatesOutdated` / `collectionRegimeUnreviewed`); commit, end. A different
   action or level, or the invoice settled, held, handed off, its customer `none` or
   anonymised → `withdrawn` with the reason (`settled`, `on_hold`, `handed_off`,
   `policy_none`, `customer_anonymised`, `action_changed`). Otherwise the facts are written —
   `sent_on = L`, `deadline = L + deadline_days`, the amounts. Commit.
2. Render the letter's PDF from the row (`inv/reminderpdf.go`, the invoice PDF's maroto
   layout and fonts) and store it once at `reminders/<invoiceId>/<reminderId>-<sentOn>.pdf`
   — outside any lock; a store failure reschedules.
3. Send through the installation's SMTP seam (`s.smtpSend`, `inv/send.go:303` — not the
   rate-limited endpoint) with the seller as Reply-To, the subject "Purring: faktura {n}"
   / "Inkassovarsel: faktura {n}" (en: "Reminder: invoice {n}" / "Debt collection notice:
   invoice {n}"), a short cover text and the PDF; **a stable Message-ID per letter**,
   `<reminder-{id}@…>` (at least once, reading 13).
4. `sent`, `sent_at`, `message_id` — a lease-checked `UPDATE`. A failure: `attempts + 1`,
   backoff `min(3600, 2^n)` s; still `queued` 48 hours after `first_attempt_at` → `failed`
   (an attention item). `POST /invoices/reminders/{reminderId}/retry` (`invoices:payments`)
   puts a `failed` letter back to `queued` (409 `reminder_not_failed`).

At most one letter per second, serially.

**Paper** (I5, M12). A paper letter is not sent until it is posted:

- `POST /invoices/reminder-print-batches` (`invoices:payments`) `{reminderIds, postOn}` —
  1–200 letters, each `awaiting_print` (409 `reminder_not_awaiting_print` naming it);
  `postOn` today or a later day, at most 7 days on (400). The batch row, then per letter
  the transaction of step 1 with `L = postOn` (a withdrawn one is reported and left out):
  the facts written with **`sent_on = postOn`** and the deadline from it; the letter
  `printed` with the batch's id. **A letter whose `L = postOn` falls past
  `regime_reviewed_through` with a fee or as a notice, or into a half-year with no rate row
  it needs, is left out of the batch, stays `awaiting_print`, and is reported** with
  `collection_regime_unreviewed` or `collection_rates_outdated` — as the run refuses and the
  dispatch reschedules (m9). Then each PDF rendered and stored, and 201 with the batch, its
  combined PDF's URL and the letters left out.
- `GET /invoices/reminder-print-batches/{id}/pdf` — the combined PDF of the batch's
  letters, rendered from their rows, as often as needed (`application/pdf`,
  `Cache-Control: private, no-store`).
- `POST /invoices/reminder-print-batches/{id}/posted` `{postedOn}` — the person confirms
  the post. **`postedOn` must equal the batch's `postOn`** (NB1): every fact on the letters
  — R7's 14 days, R10's passed deadline, the six-month reset, the inkassosats, the regime
  and its review, the deadline itself — was judged at `L = postOn`, so a letter posted
  earlier would carry a fee judged for a later day, and one posted later would shorten its
  deadline. Earlier → 409 **`reminder_posted_early`**, later → 409
  **`reminder_posted_late`**: the letters must be reprinted. Equal → **the re-judge**
  (NI4): the batch row `FOR NO KEY UPDATE`, then its letters' invoices in descending id,
  then each letter, and the engine at `L = postOn` per letter; every `printed` letter
  becomes **`sent`** (it was posted), `sent_at` the request's time; **a letter whose invoice
  was settled, held, handed off or anonymised since printing, or whose re-judged outcome no
  longer carries the fee or compensation it printed, has that fee and compensation waived
  in the same transaction, `claimed_in_error`**, and the answer lists those letters.
- `POST /invoices/reminder-print-batches/{id}/reprint` — the batch row `FOR NO KEY UPDATE`,
  then every `printed` letter of the batch back to `awaiting_print`, its facts and PDF key
  cleared (a reprint gets a new key).
- `invoices.reminder_print_batches`: `id`, `post_on date`, `posted_on date`, `created_at`,
  `created_by_user_id`, `posted_by_user_id`, `posted_at`, `reprinted_at`.

A `printed` letter is in flight (D8) and counts for no fee until posted.

**Download and withdraw.** `GET /invoices/reminders/{reminderId}/pdf` (`invoices:access`)
answers a printed or sent letter's stored PDF (409 `reminder_not_sent` otherwise; 500 on a
missing or altered object, `inv/pdfstore.go:390-440`'s checks).
`POST /invoices/reminders/{reminderId}/withdraw` `{reason}` (`invoices:payments`) — a
`queued`, `awaiting_print`, `printed` or `failed` letter, the letter alone locked (the
worker re-reads the status after its own locks); 409 `reminder_not_withdrawable`.

**The letter's content** (R4 §2.6's table), in the buyer's language (nb, en), fixed text
pinned by golden PDFs and text extraction:

- the seller block (the seller snapshot), the buyer block, the date `sent_on`, the heading
  **"Purring"** / **"Inkassovarsel"** (en "Payment reminder" / "Debt collection notice");
- what the claim concerns: invoice number, issue date, due date, and the amounts
  **separately** (R13; INKL § 10 c, d by choice): the invoice's total, credited, paid,
  **the principal open**; **earlier fees and compensation outstanding** (`charges_earlier`);
  this letter's fee or compensation; **the interest accrued to `sent_on`** with its rate(s)
  and from-date, what of it is waived and what is already paid; **the total to pay**;
- the deadline and payment information: the account, the **invoice's KID** when it has
  one, else "merk betalingen med fakturanummer {n}"; the pay link (D15) when the invoice
  has a live one;
- "Har du betalt i mellomtiden, kan du se bort fra dette brevet";
- **the objection sentence** on every letter: "Har du innsigelser mot kravet, gi oss
  beskjed før fristen" (FinKN 2025-240);
- **an inkassovarsel** additionally says, clearly and unambiguously, that "kravet vil bli
  sendt til inkasso dersom det ikke er betalt innen {deadline}" (INKL § 9, SOM) and that
  this **may** add costs — never that it necessarily will;
- **a reminder announcing the hand-off** (the 2026 regime) says the claim "vil bli
  oversendt til et inkassoforetak" if unpaid by the deadline (Prop. 3 L 12.5.5).

**Tests**: preview (freshness, the OCR note) and run (each refusal and skip reason by its
guard — `bank_import_stale` with and without the flag and with only fee-free letters; the
recipient and channel rules; 500 items); the triggers; the worker (claim and lease; the
re-judge withdrawing for each reason; the outdated-rate and unreviewed reschedule, not
counted; facts written at sending; the PDF stored once; the stable Message-ID; backoff;
48 hours → `failed`; retry; a withdrawal during a claim); paper (a batch for a later
`postOn`, its facts and PDFs; letters past the review or a missing rate half-year left out
and reported; re-download; posted on `postOn` → `sent`; posted early or late → 409 and
reprint; **`TestPrintBatch_PostedRejudges`** — an invoice paid, one held and one handed off
between printing and posting: all three `sent`, their fees waived `claimed_in_error` and
listed; a printed letter blocking a run); goldens of each level in both languages and
the B2 pair (D9). **Races**: `TestReminderRun_RacesPayment`, `TestReminderRun_TwoRuns`,
`TestReminderDispatch_RacesHold`, `TestReminderDispatch_RacesImport`,
`TestReminderDispatch_RacesWithdraw`, all without `40P01`.

### D11 — Holds, the hand-off to collection, and its export

**A hold** marks an invoice disputed: `invoices.invoice_holds` (`id`, `invoice_id`,
`kind varchar(10) CHECK (kind = 'disputed')`, `note varchar(500) NOT NULL`, `placed_at`,
`placed_by_user_id`, `lifted_at`, `lifted_by_user_id`, `lift_note varchar(500)`,
`charges_allowed boolean`), one live per invoice. `POST /invoices/{id}/hold` `{note}`
(`invoices:payments`): under the invoice's lock; 404; 409 `invoice_draft`,
`credit_note_no_reminders`, `invoice_on_hold`. Every queued, awaiting or printed letter of
the invoice is withdrawn in the same transaction (`on_hold`). `POST
/invoices/{id}/hold/lift` `{note, chargesAllowed}`: 409 `invoice_not_on_hold`. **On the
lift** the person answers whether the objection was obviously groundless:
`chargesAllowed: false` (the form's default) means it had reasonable grounds (INKL § 17
second paragraph; new § 18, R16) — **every fee and compensation claimed on the invoice is
waived** in the same transaction (`objection_upheld`, B3), and fees and the compensation
stay barred on that invoice for good; `true` — groundless, nothing waived. Late interest
is not a cost and keeps running. A held invoice still takes payments and imports (reading
24; the critic's open question — an objection raised after a fee — is answered by the
default: waived).

**The hand-off**: `invoices.collection_handoffs` (`id`, `invoice_id`, `handed_on date`,
`agency varchar(200) NOT NULL`, `agency_reference varchar(100) NOT NULL DEFAULT ''`,
`note varchar(500) NOT NULL DEFAULT ''`, `created_at`, `created_by_user_id`,
`withdrawn_on date`, `withdrawn_by_user_id`, `withdrawal_reason varchar(200)`), one live per
invoice. `POST /invoices/{id}/collection` `{handedOn, agency, agencyReference?, note?}`
(`invoices:payments`): `handedOn` not after today and not before the issue date (400);
under the invoice's lock; 409 `invoice_draft`, `credit_note_no_reminders`,
`invoice_settled`, `invoice_handed_off`. Every letter in flight is withdrawn in the same
transaction (`handed_off`). `POST /invoices/{id}/collection/withdraw` `{withdrawnOn,
reason}`: 409 `invoice_not_handed_off`. While handed off: no letters; **payments are still
registered** — the creditor still owns the claim (INKL § 2, R4 §2.10), and a direct payment
must be reported to the agency, which the invoice view says beside it (R4 §7 item 16); the
pay page is closed (D15).

**`GET /invoices/collection-export.csv`** (`invoices:access+invoices:payments`): the live
hand-offs `?handedFrom&handedTo` or the invoices `?invoiceId=` (repeatable, 1–500); 400
otherwise or past 500 rows. One row per invoice, in the module's CSV format
(`inv/csvfile.go`; `Cache-Control: private, no-store`; `invoices-collection-<date>.csv`).
The columns, fixed, English, in this order — R4 §2.10's implied minimum, **principal apart
from charges, waived charges out**:

```text
Invoice number;Issue date;Due date;Delivery;Delivered;KID;Customer number;Debtor;Debtor type;Org no;Foreign id;Address line 1;Address line 2;Postal code;City;Country;E-mail;Gross;Credited;Paid;Principal open;Payments;Fees claimed;Compensation claimed;Charges waived;Interest rate;Interest from;Interest to;Interest accrued;Charges paid;Letters;Notice sent;Notice deadline;Disputed;Handed on;Agency;Agency reference
```

`Delivered` the first recorded delivery (its kind and date); `Fees claimed` and
`Compensation claimed` net of waivers, `Charges waived` the waived total; `Debtor` and
the address the buyer snapshot's; `E-mail` the profile's `ReminderEmail`, read through the
directory before the export (empty when it fails — logged at warn); `Payments` and
`Letters` compact lists; `Interest to` today. No national identity number (none is held).

**Tests**: hold and lift (each refusal; letters withdrawn; the barring lift writing a
waiver per fee and the compensation, and nothing on `true`; interest unaffected); the
hand-off; the export (both selections, each column, waivers excluded, the guard, the cap,
the directory failure); **race** `TestHandoff_RacesReminderDispatch`.

### D12 — The overdue list and attention

**`GET /invoices/overdue`** (`invoices:access`): the issued invoices whose state is
`overdue` (`invoices.document_state`, unchanged), and with `?charges=outstanding` also the
paid ones with charges outstanding; filters `customerId`, `action` (`reminder`,
`collection_notice`, `hand_off`, `blocked`, `waiting`), `dueBefore`. Each item: the
invoice (id, number, customer, buyer name, `buyerType`, issue and due date), `daysOverdue`
(from `E`), `principalOpen`, `charges` (D9), `interestToday`, `delivered` (or not),
`lastLetter`, `nextAction {action, earliestOn, reasons[], chargeNotes[]}` (D8), `hold`,
`handoff`, `policyMode`. **The whole overdue set is judged before paging** (M7): the
figures are read in a handful of statements on the pool (no lock), the engine runs per
invoice, then the `action` filter and the page apply; at most **5 000** overdue invoices —
past it, 409 `too_many_overdue` asks for `customerId` or `dueBefore`. One clock read. The
answer carries the bank data's freshness (D10) and the warnings
`collection_rates_outdated`, `collection_regime_unreviewed`,
`collection_rate_differs_from_release` when they apply.

**`GET /invoices/stats/attention`** (`invoices:access`), the dashboard's shared item shape
(`openapi/expenses.yaml:3637-3664`; the host translates the sentence from `type`):

- `invoiceOverdue` — the 20 most overdue invoices; `entityId` the invoice, `title` the
  buyer's name, `occurredAt` the day after `E`.
- `invoiceRefundDue` — every issued invoice whose open amount is below zero (a credit note
  after a payment or a capture, M16), or whose charges are owed back.
- for `invoices:payments` only: `bankTransactionsOpen` (one per bank file with lines
  `pending`, `exception` or `duplicate`); `reminderFailed` (one per `failed` letter);
  `remindersHeld` (`collectionRatesOutdated`, `collectionRegimeUnreviewed` — one item each
  while letters wait on them); `reminderBatchUnposted` (a print batch printed and not
  confirmed posted for two days); and from 4C `paymentCaptureFailed`,
  `paymentCaptureUnknown`, `paymentReservationStranded` (a reservation held with the
  switch off), `vippsRefundSeen` (D16). A caller without it is answered the rest, never a
  403.

`GET /invoices/stats/summary` is unchanged (`R/invoices.md:1849-1879`).

**Tests**: the list's filters, order, paging after the filter, the cap, each field and the
engine's agreement with the preview; attention per permission, the 20 cap, each type and
its clearing.

### D13 — The payments port and the Vipps adapter

**A shared platform package `srv/payments`** (R4 §5.9; the roadmap's "not inside
Invoices, so a later Point of sale module consumes the same adapters", `ROADMAP.md:859-861`):
interfaces and DTOs only, **no SQL** (MB rule 4 — the tables are each consumer's),
importing no module (MB rule 2). It joins depguard's `platform` list
(`apps/server/.golangci.yml:361-400`, beside `internal/peppol` and `internal/secrets`)
with `internal/payments/vipps` and `internal/payments/vipps/vippstest`, and MB rule 1's
list.

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
    // CaptureTime is when the provider recorded the payment's (first) capture, from its
    // event log; ok is false when it holds none.
    CaptureTime(ctx context.Context, reference string) (t time.Time, ok bool, err error)
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
adapter (R4 §4.10).

**The Vipps ePayment adapter** `srv/payments/vipps` (R4 §4.4–§4.7):
`vipps.New(Options{BaseURL, ClientID, ClientSecret, SubscriptionKey, MSN, SystemVersion,
Transport, Now})`. The access token from `POST /accesstoken/get`, cached until a minute
before its expiry (1 h test, 24 h production), one fetch at a time; every call sends
`Authorization: Bearer`, `Ocp-Apim-Subscription-Key`, `Merchant-Serial-Number` and the
system headers `Vipps-System-Name: vantigo`, `Vipps-System-Version: <release>`,
`Vipps-System-Plugin-Name: vantigo-invoices`, `Vipps-System-Plugin-Version: <release>`;
`Idempotency-Key` on create, capture, cancel and refund — the key the caller persisted
(R4 §4.5). Create: `paymentMethod.type = WALLET`; `WEB_REDIRECT` with `returnUrl`, or `QR`
with `qrFormat {format: IMAGE/SVG+XML}` and `customerInteraction: CUSTOMER_PRESENT`;
amounts 100–65 000 000 øre in NOK; `reference` `^[a-zA-Z0-9-]{8,64}$`;
`paymentDescription` 3–100 characters; `merchantLegalLinks`. Problems are RFC 7807 with
`extraDetails` codes, mapped to the typed errors; "already cancelled" (6050, 6190) is
success. A 30-second bound per request, no redirects, **no retry inside the adapter**,
`Deps.HTTPTransport` as the seam, unguarded like Brreg and Storecove. Webhook
verification: R4 §4.7's exact algorithm — the content hash over the raw body, the
string-to-sign `POST\n<pathAndQuery>\n<x-ms-date>;<host>;<x-ms-content-sha256>` with `\n`,
HMAC-SHA256 under the secret, constant-time comparison; `pathAndQuery` and `host` from
the **registered URL**, never the request line. The published vector's signature does not
reproduce (R4 §7 item 37): the content-hash step is pinned to it, the signature step to a
fixture the tagged test captures (D16).

**`vippstest`** — an ordinary package serving an `httptest` TLS server speaking the
ePayment, token and webhooks APIs as their OpenAPI documents describe them,
`storecovetest`'s shape: scripted states, the special amounts (151 insufficient funds),
409/423/429/5xx, idempotency by key, a partial capture, a refund made "in the portal", a
signed webhook POST to a URL the test gives.

**The tagged test** `srv/payments/vipps/vipps_test_env_test.go` (`//go:build vipps`),
skipped unless `VIPPS_TEST_CLIENT_ID`, `VIPPS_TEST_CLIENT_SECRET`,
`VIPPS_TEST_SUBSCRIPTION_KEY`, `VIPPS_TEST_MSN` and `VIPPS_TEST_PHONE` are set (R4 §4.8):
token; create; force approve; poll to `AUTHORIZED`; capture with a key, `capturedAmount`
asserted; the capture repeated with the same key; a partial refund; the event log; a second
create cancelled to `TERMINATED`; amount 151; a QR create; and, with
`VIPPS_TEST_WEBHOOK_URL`, a registration and one captured event written to `testdata/` as
the signature fixture.

**Tests** (untagged): the adapter over `vippstest` — each operation's request (headers,
key, body), each error mapping, the token cache and its refresh, a reused key, the QR's
SVG, the webhook verification against the published content hash, a self-generated
signature (flagged as such) and the captured fixture once it exists.

### D14 — `PUBLIC_BASE_URL`, the public host, credentials and payment settings

**`PUBLIC_BASE_URL`** (platform, `srv/config`; I1): the https URL at which the pay page is
reachable **from the internet**, unset by default. Rules, each a start-up refusal in the
config's problems list: https only; its path must equal `APP_BASE_PATH` (the SPA's index
is rendered once for that base path, `srv/web/index.go:33-51`, and `httpx.StripBasePath`
strips only it); its host may equal `APP_URL`'s or be another. When it is another:

- **`HostFilter` admits both hosts** — `security.AllowedHosts` (`srv/security/hostfilter.go:18-26`)
  takes `APP_URL`'s host, `PUBLIC_BASE_URL`'s and loopback (`srv/server/server.go:102`).
  The public origin is **not** added to the CSRF protection's trusted origins
  (`:41-53`; m2): the proxy preserves `Host`, as the platform requires
  (`srv/httpx/forwarded.go`: `X-Forwarded-Host` is never honoured), so the pay page's POST
  is same-origin and passes, and trusting the origin would only widen what a page there may
  send to `APP_URL`'s host. Session cookies are host-only (`srv/identity/cookies.go:99-101`,
  no `Domain`), so the public host never receives one.
- **On the public host the server serves only an allowlist** — a new middleware,
  `security.PublicHostGate(publicHost, allowlist)`, after `StripBasePath` so paths are
  base-relative: a request whose `Host` is the public host (port ignored) and whose method
  and path are not on the list answers **404** (a problem body under `/api/`, an empty one
  elsewhere), before any routing. The list, exact:

  | Method | Path |
  | --- | --- |
  | `GET`, `HEAD` | `/pay` (the SPA's index; any query) |
  | `GET`, `HEAD` | `/assets/*` (Vite's content-hashed bundle, `srv/web/handler.go:27-34`), `/favicon.png` |
  | `GET` | `/api/v1/invoices/pay` |
  | `POST` | `/api/v1/invoices/pay/attempts` |
  | `GET` | `/api/v1/invoices/pay/attempts/{reference}` (one segment) |
  | `POST` | `/api/v1/invoices/vipps/webhooks` |
  | `GET` | `/api/v1/identity/system/status` (the root layout's maintenance poll, below) |
  | `GET`, `HEAD` | `/health/*` |

  Sign-in, setup, every other module's API, the docs and every other SPA path answer 404
  on the public host; they stay on `APP_URL`'s. The SPA served at `/pay` must make no
  request outside the list: the root layout already skips the session query and the
  route guard on a public path (`host/routes/__root.tsx:84-89`, `:237`), while its
  maintenance poll, `GET /api/v1/identity/system/status` every 30 s (`:91-96`), runs on
  every path — so it is on the list (m1). A host test loads `/pay?token=…` with every
  request recorded and fails on any other path.
- When the two hosts are the same, there is no gate — the operator has published `APP_URL`
  itself; the admin page recommends a separate host and says what the same host exposes.

Pay links and webhooks need `PUBLIC_BASE_URL`; on-site QR payments do not (the tablet is
signed in on `APP_URL`, D17).

**Credentials** — `invoices.payment_provider_credentials` (`00042`), phase 2's shape
(`mig/00036…:44-55`): one row (`id = 1`), `provider varchar(20) CHECK (provider IN
('vipps'))`, `settings_json text` (non-secret: `msn`), `secret_ciphertext text` (sealed
JSON `{clientId, clientSecret, subscriptionKey}`), `webhook_id varchar(100)`,
`webhook_secret_ciphertext text`, `rejected_at`, `updated_at`. Purposes
`invoices/payment-provider-credential` and `invoices/payment-webhook-secret`
(`srv/secrets/secrets.go:99-203`). One sales unit per installation (open question 5).
Rule 4: Point of sale gets its own table later.

- `GET /invoices/settings/vipps` (`invoices:manage`): `{msn?, hasCredentials, rejectedAt?,
  webhook {registered, url?}}`.
- `PUT` `{msn, clientId?, clientSecret?, subscriptionKey?}`: the first PUT needs all three
  secrets; an omitted one keeps the stored one (opened and re-sealed from the row read
  `FOR UPDATE`); 400 on the fields; 503 `payments_unavailable` when a kept secret cannot be
  opened. **A change of `msn`, `clientId` or `subscriptionKey` is refused 409
  `payment_attempts_active` while any attempt is live** (I20) — `creating`, `created`,
  `authorized`, `capturing`, `capture_unknown`, `cancelling` or `release_pending`; a new
  `clientSecret` alone (a rotation within the same sales unit) is allowed. Clears
  `rejected_at`. Never answers a secret.
- `DELETE`: 409 `payment_attempts_active` while any attempt is live; 204.
- `POST …/verify`: a token fetch → `{result: ok | unauthorized | unreachable}`.
- `POST …/webhook` registers `PUBLIC_BASE_URL + /api/v1/invoices/vipps/webhooks` for the
  ePayment events and stores the id and the sealed secret, replacing an earlier
  registration; 409 `public_url_missing`; 502 `provider_failed`. `DELETE …/webhook`
  unregisters.

**Payment settings** — `invoices.payment_settings` (`00042`, PR 2 only — the bank format is
the bank accounts' in PR 1, so no payment-settings shape changes between the PRs), one row:

| Column | Rule |
| --- | --- |
| `reference_prefix` | six random base36 characters made by the migration; every attempt reference starts with it (M4) |
| `pay_links_enabled` | pay links on issued invoices (D15); requires `PUBLIC_BASE_URL`, credentials, both URLs below |
| `terms_url`, `privacy_url` | https URLs of the seller's sales terms and privacy notice — Vipps' `merchantLegalLinks`, shown on the pay page (R4 §4.2) |
| `on_site_payments_enabled` | the on-site QR and the pay link shown on screen (D17, I21) |
| `kontantsalg_acknowledged_by_user_id`, `kontantsalg_acknowledged_at` | who acknowledged the kontantsalg notice, when |

`GET /invoices/settings/payments` (`invoices:access`); `PUT` (`invoices:manage`) — every
field required; **turning `payLinksEnabled` or `onSitePaymentsEnabled` on shows the
kontantsalg notice, and turning `onSitePaymentsEnabled` on requires
`acknowledgeKontantsalg: true`** in the same body (400 otherwise), which records the caller
and the time; turning it off clears both. 409 `public_url_missing` when pay links are turned
on without `PUBLIC_BASE_URL`; 409 `payments_unavailable` without credentials.

**Tests**: config (unset; http refused; a different path refused; the same host; another
host); **`TestPublicHostGate`** — for every allowlisted method and path a request on the
public host reaches its handler, and for a sample of every other kind (sign-in, `/setup`,
`/api/v1/identity/me`, `/api/v1/customers`, `/api/v1/invoices` and
`/api/v1/invoices/meta`, `/docs/`, `/pay/x`, `PUT /api/v1/invoices/pay`) it answers 404,
while the same requests on `APP_URL`'s host behave as before; `HostFilter` admitting the
public host and still refusing a third; credentials (never answered, kept when omitted,
the sales-unit change refused while live and the secret rotation allowed, delete refused,
verify's three answers, the webhook registration); payment settings (each rule; the
acknowledgement recorded and cleared).

### D15 — The pay page and pay links

**Why a page** (R4 §1.2 item 3, §4.2): a Vipps payment request lives ten minutes and a
long-living one is closed to an invoice's 14-day term in Norway, so the link on an invoice
is a **stable URL of the installation** which creates the ePayment only when the customer
presses "Betal med Vipps".

**The URL** is `PUBLIC_BASE_URL/pay?token=<token>` — the token in the **query string**
(M3): the request log writes the path, never the query (`srv/httpx/log.go:15-32`), as the
invitation links already rely on; `Referrer-Policy: no-referrer` is the platform's
(`srv/security/headers.go:15`). `/pay` is an exact path, so it joins `publicPaths`
(`host/lib/public-paths.ts:1-10`) and its four call sites stay as they are (M8 moot).

**`invoices.pay_links`** (`00042`): `id`, `invoice_id` (one live link per invoice, a
partial unique index), `token_sha256 char(64) NOT NULL UNIQUE` (the token is 32 random
bytes, base64url; only its hash is stored for the lookup), `url_ciphertext text NOT NULL`
(the full URL sealed under `invoices/pay-link`, so an e-mail or a letter can carry it
later), `created_at`, `created_by_user_id` (NULL when the issue made it), `revoked_at`,
`revoked_by_user_id`. A trigger: no DELETE; only the revocation, once.

- **At issue**, when pay links are available (meta's rule, judged before the transaction
  with the profile — no lock), the issue inserts the link in its own transaction and the
  PDF — stored once after the commit — prints "Betal med Vipps" with the URL and a QR of
  it beside the payment block. A PDF is never re-rendered, so an invoice issued before pay
  links has none in its PDF.
- `POST /invoices/{id}/pay-link` (`invoices:issue`) makes one for an issued invoice that has
  none (201; 409 `pay_links_unavailable`, `invoice_draft`, `credit_note_no_payments`,
  `pay_link_exists`); `POST /invoices/{id}/pay-link/revoke` (`invoices:issue`; the invoice,
  then the link). The e-mail cover text (`inv/mailtext.go`) and the reminder letter (D10)
  carry the live link — they reach the customer remotely, a credit sale (D17).
- **On screen** (I21): the app's invoice page, the quick invoice screen and the PDF
  viewer show **no pay link and no QR of it, and offer no "copy link"**, unless on-site
  payment is enabled; the response's `payLink.url` is answered only to `invoices:issue`
  holders, and the frontend renders it only then. Reading 20 says what this leaves open.

**The anonymous operations** (`x-vantigo-access: anonymous`), each with its own rate-limit
policy per client address (I2; the router's `Limits`, `inv/module.go:74-77`):

| Operation | Policy | Limit |
| --- | --- | --- |
| `GET /invoices/pay?token=` | `invoices-pay-read` | 60 per 10 minutes |
| `POST /invoices/pay/attempts` | `invoices-pay-attempt` | 10 per 10 minutes |
| `GET /invoices/pay/attempts/{reference}?nonce=` | `invoices-pay-status` | 400 per 10 minutes |

The return page polls the status every **2 s for the first minute, then every 5 s** while
`pending`, at most ten minutes — about 150 reads, within its bucket. Behind a reverse proxy
the client address is the proxy's unless `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS`
are set (`srv/httpx/forwarded.go:15-30`, `config.go:382, 1240-1250`), and every payer
would share one bucket; the admin page says so (D23).

- `GET /invoices/pay?token=` → 200 `{seller {name, organisationNumber}, invoiceNumber,
  issueDate, dueDate, openAmount, currency, kid?, termsUrl, privacyUrl, language, payable,
  reason?}` — **never the buyer's name or address**. `payable` false with `reason`:
  `settled`, `credited`, `revoked`, `handed_off` ("contact the agency"),
  `amount_out_of_range` (open above 650 000 or below 1.00), `unavailable` (the switch, the
  credentials or `PUBLIC_BASE_URL` gone). A token that matches no link → 404 (M2: no claim
  of an identical body; the token's 256 bits are the protection).
- `POST /invoices/pay/attempts` `{token, acceptTerms: true}` → 400 without the acceptance
  (Vipps requires the customer's active acceptance, R4 §4.2); 404 an unknown token; 409
  the `reason`s above; 409 `too_many_attempts` (five live attempts on the invoice); 503
  `payments_unavailable`; 502 `provider_failed`; else **201 `{reference, redirectUrl}`** —
  D16's attempt with `flow = pay_link`, `WEB_REDIRECT`, and **`returnUrl` =
  `PUBLIC_BASE_URL/pay?attempt=<reference>&nonce=<nonce>` — never the token** (m3): `nonce`
  is 16 random bytes, base64url, stored hashed on the attempt (`return_nonce_sha256`), and
  grants only reading that attempt's state. Before redirecting, the page keeps the token in
  `sessionStorage`, so on return it shows the invoice again when the same browser comes
  back, and only the attempt's state otherwise (the Vipps app may return to another
  browser). The token thus never reaches Vipps; the nonce does, and Vipps' and the proxies'
  logs can learn no more from it than one attempt's outcome. The page opens `redirectUrl` at
  once, unchanged (R4 §4.2).
- `GET /invoices/pay/attempts/{reference}?nonce=` → `{state: pending | paid | failed |
  cancelled}` (404 unless the nonce is that attempt's). The token itself still travels in
  the query of the link the customer opens: the request log never writes it, but a reverse
  proxy's access log does by default, which the admin page says, with how to strip the query
  of `/pay` from it (D23).

**The host** adds the public route `/pay` (`host/routes/pay.tsx`), rendering `PayPage` from
`@vantigo/invoices-ui`, which calls only the three operations and works without a session.
It states the seller, the invoice number, the amount open, the due date and the KID (to
pay by bank instead), links the terms and privacy notice with the acceptance checkbox, and
shows the state on return. nb by default, en by the snapshot's language or the reader's
choice.

**Tests**: the link at issue (in the issue's transaction; none without availability; the
PDF's line and QR in a golden), on demand, revoked; the token stored only hashed; the
e-mail and letter lines; the on-screen gating; each operation's answers and refusals; no
buyer data in any answer (a test greps the JSON for the snapshot's name and address); each
bucket and the page's backoff (a fake clock); CSRF; the request log never holding a token;
`/pay` public and `/payments` not.

### D16 — Payment attempts, the poll worker, capture and webhooks

**`invoices.payment_attempts`** (`00042`):

```text
id bigint identity PK, invoice_id bigint NOT NULL REFERENCES invoices.invoices ON DELETE RESTRICT,
provider varchar(20) CHECK (provider = 'vipps'), msn varchar(10) NOT NULL,   -- the sales unit it was made under (I20)
return_nonce_sha256 char(64),                        -- pay_link flow: the return URL's nonce, hashed (D15)
flow varchar(10) CHECK (flow IN ('pay_link','on_site')),
pay_link_id bigint REFERENCES invoices.pay_links,   -- pay_link flow only
reference varchar(64) NOT NULL UNIQUE,               -- "<reference_prefix>-<invoice number>-<attempt id>" (M4)
amount numeric(14,2) NOT NULL CHECK (amount >= 1),   -- the open amount at creation
create_key uuid, capture_key uuid, cancel_key uuid NOT NULL,   -- Idempotency-Keys, persisted before each call
state varchar(16) NOT NULL CHECK (state IN ('creating','created','authorized','capturing','capture_unknown',
      'captured','cancelling','cancelled','aborted','expired','terminated','capture_failed','failed','abandoned')),
release_pending boolean NOT NULL DEFAULT false,      -- captured or capture_failed, the uncaptured rest not yet released (I19)
psp_reference varchar(100), redirect_url varchar(2500),       -- redirect_url: sensitive, never logged
authorized_amount, captured_amount, cancelled_amount, refunded_amount numeric(14,2),
reserved_amount numeric(14,2),                        -- live while 'capturing' or 'capture_unknown'
capture_psp_reference varchar(100), capture_guaranteed_until timestamptz,
created_at timestamptz NOT NULL, created_by_user_id uuid,   -- on_site: the tablet's user; pay_link: NULL
expires_at timestamptz NOT NULL, authorized_at, captured_at, finished_at timestamptz,
refund_watch_until timestamptz,                       -- captured_at + 30 days
next_poll_at timestamptz, polls integer NOT NULL DEFAULT 0, capture_attempts integer NOT NULL DEFAULT 0,
lease_id varchar(100), lease_until timestamptz, last_error varchar(500),
abandoned_by_user_id uuid, abandoned_at timestamptz, abandon_reason varchar(500)
UNIQUE (invoice_id) WHERE flow = 'on_site' AND state IN ('creating','created','authorized','capturing','capture_unknown')
INDEX ix_payment_attempts_due (next_poll_at)
    WHERE state IN ('creating','created','authorized','capturing','capture_unknown','cancelling')
       OR release_pending OR (state = 'captured' AND refund_watch_until IS NOT NULL)
```

No DELETE; identity columns frozen. A terminal row (`captured`, `cancelled`, `aborted`,
`expired`, `terminated`, `capture_failed`, `failed`, `abandoned`) changes only its lease,
`next_poll_at`, `release_pending` (true → false) with `cancelled_amount`, and on a captured
row `refunded_amount` and `refund_watch_until`. Every query takes `@now` from `Deps.Clock()`.

**Create** (the pay page's POST, or D17's on-site request): one transaction — the invoice
`FOR UPDATE`, open computed, the five-live cap, the attempt inserted `creating` with
`amount` = open, `msn` the current one and `create_key`; commit; then `Provider.Create`
**outside any lock**; then `created` with `redirect_url` (or the QR) and `next_poll_at =
created_at + 5 s` (R4 §4.5). A definitive refusal → `failed` and 502 `provider_failed`; an
unknown outcome leaves `creating` for the worker.

**The worker `invoices-payments`** (`inv/payment_worker.go`, the row-lease shape, a
one-second tick, claiming due rows by `next_poll_at`), one claim per attempt; every
provider call uses the attempt's own `msn` — a credential row for another sales unit
leaves the attempt alone and raises `paymentReservationStranded`:

- `creating`/`created`: `Get`. `CREATED` → next poll in 2 s while before `expires_at`, then
  one last read; `ABORTED`, `EXPIRED`, `TERMINATED` → that state; `AUTHORIZED` →
  `authorized`, and on at once to the decision. A 404 for a `creating` row after its ten
  minutes → `failed`.
- **The capture decision** — one transaction: the attempt `FOR UPDATE`, then **the invoice
  `FOR UPDATE`**, then `open` (gross − credited − paid − every **other** live reservation).
  `capture = min(authorized, open)`. Above 0 → `capturing`, `reserved_amount = capture`,
  `capture_key` set. Zero → `cancelling`. Commit. **No provider call is made inside it**
  (amendment 1).
- `capturing`: first `Get` — `captured ≥ reserved` already (an answer lost) → register.
  Else `Capture(reference, reserved, capture_key)`. **Register** on `capturedAmount ≥
  reserved` (the status code alone is not enough, R4 §4.4), and on a definitive partial
  capture `0 < capturedAmount < reserved` register what was captured: one transaction — the
  attempt `FOR UPDATE`, the invoice `FOR UPDATE`, a payment `source = vipps`,
  `payment_attempt_id` (the exactly-once guard), `amount` = captured, `paid_on` **the Oslo
  day of the capture event's time** from the provider's event log (`CaptureTime`, D13) — so
  a `capture_unknown` resolved weeks later does not overstate interest on a partly open
  invoice (m10) — falling back to the claim's clock read when the log cannot be read,
  `reference` the attempt's reference, no user; the attempt
  `captured`, `reserved_amount` NULL, `release_pending` true when `authorized > captured`,
  `refund_watch_until` 30 days on. **Definitive failure only** (B5): 6260
  (`ErrInsufficientFunds`) or 6280 (`ErrCaptureFailed`), or a successful `Get` showing
  `capturedAmount = 0` with the payment no longer capturable (cancelled, or past
  `capture_guaranteed_until`) → `capture_failed`, the reservation released,
  `release_pending` true. **Anything else** — a transport failure, a 5xx, a 401 after keys
  changed in the portal — is retried with the same key, backoff `min(600, 2^n)` s; after
  seven days of unknown outcomes the attempt becomes **`capture_unknown`**: it **keeps its
  reservation** (so no manual payment of that amount and no fee-bearing reminder while the
  money may have been taken), raises `paymentCaptureUnknown`, and is polled with `Get`
  hourly up to 180 days after `authorized_at` (Vipps' capture limit, R4 §4.4), then daily;
  the first definitive answer resolves it as above.
- `release_pending`: `Cancel(cancel_key)` on the cadence until it succeeds (an already
  cancelled answer is success), then `cancelled_amount` and `release_pending` false.
- `cancelling` → `Cancel(cancel_key)`; done → `cancelled`.
- **The refund watch** (the critic's gap): a `captured` attempt is re-read with `Get` once a
  day until `refund_watch_until`; a `refundedAmount` above the recorded one is stored and
  raises `vippsRefundSeen` — a refund made in the Vipps portal; the person removes the
  payment (or removes it and registers the part kept). Nothing is removed automatically.

**Abandon** (B5, I20) — `POST /invoices/payment-attempts/{id}/abandon` (`invoices:manage`)
`{reason, confirmNotCaptured: true}` — for an attempt `capture_unknown`, or `capturing` while
`INVOICES_VIPPS_ENABLED` is off or its `msn` is no longer the stored one: the person has
checked in the Vipps portal that nothing was captured. 400 without the confirmation; 409
`attempt_not_abandonable`. The attempt alone is locked: `abandoned`, the reservation
released, who, when and why recorded. A later capture discovered anyway is registered by
hand.

**Capture at once** on `AUTHORIZED` (reading 15). **The reservation in `openOf`** (D2) is
what makes a capture and a manual payment, an import or another attempt safe together.

**Webhooks** (optional, reading 16): `POST /invoices/vipps/webhooks` (`x-vantigo-access:
anonymous`, its own policy `invoices-vipps-webhook`, 600 per minute per client). The router
gains `RouterOptions.RawBodies map[string]int64` (operationId → cap; here 64 KiB): the raw
bytes are read under the cap and kept in the request's context before the generated wrapper
decodes, a platform change in `srv/module/router.go` beside `BodyLimits` (`:23-37`). The
handler verifies with `VerifyWebhook` against the stored secret and the registered URL:
invalid → 401 (logged at warn); valid → a conditional `UPDATE` of the attempt with that
reference, `next_poll_at = now` (the attempt alone; a nudge — **the poll is the only path
that changes state**), and 200 at once, also for an unknown reference. Whether to ship the
receiver depends on the tagged test pinning the signature (open question 3).

**Tests**: create (the cap, `creating` on an unknown outcome, the keys persisted before each
call, the MSN and prefix on the reference); the worker through every state against
`vippstest` (the cadence, capture success, a lost answer registered once, a partial capture
registered as captured with the rest released, 6260 and 6280 → `capture_failed`, a 5xx for
seven days → `capture_unknown` with the reservation kept and a manual payment still refused,
then a `Get` resolving it each way, `release_pending` retried until done, `cancelling`, the
refund watch raising its item); abandon (each refusal; the reservation released);
exactly once; the webhook. **Races**: `TestVippsCapture_RacesManualPayment`,
`TestVippsCapture_TwoAttemptsBothAuthorized`, `TestVippsCapture_RacesImport` (the import
sees the reservation and queues rather than over-registers),
`TestVippsCapture_RacesCreditNote` (the decision and a credit note's issue serialise on the
invoice; a credit after the capture leaves `refundDue` and its attention item).

### D17 — The quick invoice, and on-site payment as kontantsalg

**The quick invoice** — `POST /invoices/quick`
(`permission:invoices:access+invoices:create+invoices:issue`): `{customerId, lines (1–10):
[{description, quantity, unit?, unitPrice, discountPercent?, vatCodeId}], deliveryDate?
(default today), paymentTermsDays?, yourReference?, note?, payOnSite?}` — the draft's rules
(`parseDraft`, `inv/drafts.go:171`) for every field. **One transaction** (R4 §5.8): the
issue's body is refactored out of `PostInvoicesByIdIssue` (`inv/issue.go:254-555`) into
`issueLocked(ctx, tx, txq, locked, draft, profile, work, issueDateReq)`, which runs steps
3–6 on a document already locked; `POST /{id}/issue` calls it after its `LockInvoice`, the
quick invoice after **inserting** the draft and its lines on the same transaction (the new
row is the one it holds, so the order stays document → settings → counter). In the same
transaction it records a **manual delivery `handed_over`** on the issue date (D8), since
the invoice is handed over on site. Before the transaction, as the issue does: the billing
profile (the customer gates, `customerGate`, `inv/drafts.go:63`, and the buyer snapshot),
and 503 `storage_unavailable` without a store. Any refusal — the draft's 400s, the customer
gates, every issue check (`seller_incomplete`, **`buyer_incomplete`**,
`issue_date_not_allowed`, the VAT checks, `kid_length_exceeded`) — rolls back everything:
no draft is left behind and no number is taken. 201 with the issued document; the PDF
stored after the commit. An ordinary invoice in the ordinary series, terms and due date as
any other.

**Paid later, it is a credit sale** (R4 §9.1 a): by KID through the bank, or through the
pay link after the tradesperson has left. No kassasystem.

**Paid on site, it is kontantsalg** (R4 §9.1 b, §9.2). Vipps at delivery is "kontanter"
(bokføringsforskriften § 5-3-1 c; Skattedirektoratet 29.06.2017), and the invoice does not
change that — **the roadmap's "this is a credit sale" is wrong** (`ROADMAP.md:866-868`;
corrected in D23). It is lawful without a kassasystem only as a **kontantfaktura**
(§ 5-4-1 tredje ledd), read narrowly by Skattedirektoratet (07.01.2019): for a business that
ordinarily sells on credit to identified customers, never for retail or counter sales, and
every such sale needs a full delkapittel 5-1 invoice naming the buyer with an address or an
organisation number, whatever the amount (R4 §9.3). Skatteetaten's Q&A makes the same
condition **the supplier's**: a system on which a kontantsalg can be registered without the
buyer's name and address is a kassasystem under kassasystemlova, and an undeclared one
(R4 §9.4). So the following are **hard product rules**, protecting Vantigo as much as its
users, and no setting relaxes them:

1. **The buyer is identified before any payment can start.** An on-site request is
   accepted only for an **issued invoice** (`kind = invoice`), and the issue already
   refuses `buyer_incomplete` unless the snapshot has a name and either a Norwegian
   organisation number or a complete address — line 1, city and country, and the postal
   code for NO (`buyerComplete`, `inv/issue.go:154-166`): § 5-1-2's "navn og adresse eller
   organisasjonsnummer", the kontantfaktura's content. **Where it falls short**: it checks
   that the fields are present, not that they are true — a customer registered as
   "Kontantkunde" with an invented address passes; the acknowledgement puts that on the
   user. The request re-judges `buyerComplete` on the stored snapshot (409
   `buyer_identity_missing`), and the quick invoice requires a `customerId` — **there is no
   anonymous, "kontantkunde" or receipt-only path anywhere in the product** until a Point of
   sale module ships with a declared kassasystem (R4 §9.5 requirement 3).
2. **One payment, one invoice.** An on-site attempt covers the invoice's whole open amount
   and nothing else; one live on-site attempt per invoice (D16's partial unique index).
3. **The notice.** On-site payment is off until an `invoices:manage` holder turns it on
   **with the acknowledgement** (D14), whose text the settings card shows in full: on-site
   payment is kontantsalg; it is allowed without a kassasystem only under
   bokføringsforskriften § 5-4-1 tredje ledd, for a business that mainly sells on credit to
   identified customers; a shop or counter sale needs a produkterklært kassasystem; the
   buyer must be named truthfully; and **showing the pay link or the invoice's QR to the
   customer on site is the same kontantsalg**. The quick invoice screen repeats a one-line
   reminder beside "Betal med Vipps nå".
4. **The time of payment, a daily list, and the day's reconciliation** (M15). Every attempt
   records `authorized_at` and `captured_at`; `GET /invoices/vipps-payments?date=`
   (`invoices:access`) lists one Oslo day's Vipps payments — time, invoice, buyer, amount,
   flow — with the day's totals per flow, as JSON and CSV. `POST
   /invoices/vipps-payments/reconciliations` (`invoices:payments`) `{day, note?}` records,
   immutably, that the day was reconciled — `invoices.vipps_day_reconciliations` (`day date
   PRIMARY KEY`, `payments integer`, `total`, `on_site_total`, `pay_link_total`,
   `reconciled_by_user_id`, `reconciled_at`, `note`), the totals as they stood; 409
   `day_already_reconciled`, `day_not_over` (today or later). § 5-4-5's daily count arguably
   reaches kontantfaktura sales — **UNCERTAIN**, R4 §9.6 item 4; built in.
5. **Not limited to business buyers** (R4 §9.5).

**The pay link on site is the same kontantsalg** (I21). A customer who pays the pay link
while the tradesperson waits pays at handover (R4 §9.5, §9.6 item 2; the internet-sale
carve-out is **not** relied on). The pay page serves only issued invoices, so rule 1 holds
for it; and **the app shows no pay link or its QR on screen unless on-site payment is
enabled** (D15) — without it, the invoice is handed over by e-mail or EHF and paid later.
The daily list includes pay-link payments, since Vantigo cannot know where the customer
stood.

**The on-site request.** `payOnSite: true` on the quick invoice, or
`POST /invoices/{id}/payment-requests` (`invoices:issue`) `{flow: "on_site"}`: 409
`on_site_payments_unavailable`, `invoice_draft`, `credit_note_no_payments`,
`invoice_settled`, `invoice_handed_off`, `buyer_identity_missing`,
`payment_request_active`, `amount_out_of_range`; 502 `provider_failed`. D16's create with
`flow = on_site`, `QR`, `customerInteraction: CUSTOMER_PRESENT`, `created_by_user_id` the
caller. 201 `{attemptId, reference, qrSvg, expiresAt}` (on the quick invoice: the document
with `paymentRequest` beside it, or `paymentRequestError` when the invoice issued but the
request failed). `GET /invoices/{id}/payment-requests/{attemptId}` (`invoices:issue`) →
`{state, capturedAt?}`, polled by the tablet every two seconds; `POST …/cancel`
(`invoices:issue`; the attempt alone, a conditional update to `cancelling`). The QR is
Vipps' one-time QR, valid ten minutes, never printed (R4 §4.1).

**Tests**: the quick invoice in one transaction (the number, the document and the
`handed_over` delivery; no draft left by any refusal; the PDF after; the refactored issue's
own suite unchanged); the on-site rules (refused without the setting and the
acknowledgement; on a draft, a credit note, an incomplete snapshot; one live attempt; the QR
flow and `CUSTOMER_PRESENT` sent); the daily list and the reconciliation record; the
on-screen gating. **Race**: `TestQuickInvoice_RacesIssue`.

### D18 — The lock order, restated

The module's invariant (`R/invoices.md:938-957`) — **locks in descending id among
invoices**, and document → settings → counter → original → the source modules' rows —
gains the new rows. Each path, in its order:

| Path | Locks |
| --- | --- |
| a bank import (D3) | the file's account rows (account order), then the inserts |
| an account's format change | the account row alone |
| a bank match (D4) | the bank transaction, then its invoice (a deadline-met waiver inserted) |
| the queue's apply, handle-reversal (D5) | the bank transaction, then its invoices in descending id |
| dismiss, confirm-duplicate, treat-as-distinct, reopen | the bank transaction alone |
| a charge payment, a waiver, a manual delivery and its removal | the invoice alone (a manual delivery's trigger shares it) |
| a hold, a lift, a hand-off, its withdrawal | the invoice, then its letters (withdrawn) and, on a barring lift, the waivers inserted |
| a reminder run's item (D10) | the invoice (the letter is inserted) |
| the reminder worker's dispatch, a print batch's letter | the invoice, then its letter |
| a batch posted or reprinted | the batch row `FOR NO KEY UPDATE` (so the letters' key-share locks on it never conflict, m11), then its letters' invoices in descending id, then the letters |
| a letter's withdraw or retry | the letter alone (the worker re-reads its status after its own locks) |
| a policy `PUT` | the policy row; the merge: the documents, then the policy rows by customer id |
| an attempt's create | the invoice (the attempt is inserted) |
| the capture decision, the registration | the attempt, then its invoice |
| a cancel, a release, an abandon, a webhook nudge | the attempt alone |
| a pay link's revoke | the invoice, then the link |
| the issue and the quick invoice | the document (locked or inserted), settings, counter, as today; a pay link and a manual delivery inserted |
| a day's reconciliation | none (an insert) |

**No path locks an invoice and then a bank transaction, an account row, a print batch or an
attempt**, so the "own row first" orders cannot cycle with each other or with the payments'
and credit notes' invoice-only and descending locks; a removal of an imported or Vipps
payment locks only the invoice and never writes the line or the attempt (D2). Every provider
call, object-store call, directory read and SMTP send is made **outside** any transaction
that holds a lock — MB rule 10's restatement, kept by the harness's contract-call hook,
which gains `payments.<op>` and `smtp.reminder` through `contractscalls.go`.

**Clock reads.** Every request and every worker claim reads `Deps.Clock()` **once** and
derives its Oslo day with `businessDay` (`inv/values.go:32`): the import, the queue's
actions, a run (one read for the whole run), each dispatch claim, a print batch, the posted
confirmation, the overdue list, the attention items, a pay-page request, an attempt's create
and each poll claim. Every query over the new tables takes `@now` or `@today` as a
parameter, never `now()` or `CURRENT_DATE` (R4 §5.4).

**Tests**: the lock-order seam records each path's order in its own test; every race named
in D3–D17 runs on a pool of `MaxConns = 2` with each raw lock-holding transaction on its own
`pgx.Connect`, every probe `NOWAIT` from its own connection, and
`pg_stat_database.deadlocks` 0 after each (MB's race conventions).

### D19 — The slots, retention and privacy

- **Merge**: payments, charge payments, waivers, deliveries, letters, holds, hand-offs, pay
  links and attempts hang off the document; bank transactions off the file; only the
  reminder policy is keyed by customer and is re-pointed (D7).
- **A person's export**: each issued document gains `chargePayments`, `chargeWaivers`,
  `manualDeliveries`, `reminders` (level, status, dates, amounts, channel, recipient,
  regime), `holds`, `collectionHandoffs`, `payAttempts` (state, amounts, times — never
  `redirect_url`), and each payment its `source` and, for an imported one, the bank line's
  date, debtor name, debtor account and text; the section gains `reminderPolicy`.
- **Anonymisation**, inside the customers module's transaction, after today's steps and in
  this order: every letter in flight of the person's documents **withdrawn**
  (`customer_anonymised`); every letter's recipient blanked; the notes of charge payments,
  waivers, manual deliveries, holds (and lift notes) and hand-offs blanked; the
  `resolution_note` of every bank line, and the notes of its events, **linked to the
  person's payments or charge payments** blanked (M10); every live pay link revoked; the
  policy row deleted. Reported, after today's five kinds: `invoices.reminders`,
  `invoices.charge_payments`, `invoices.charge_waivers`, `invoices.manual_deliveries`,
  `invoices.invoice_holds`, `invoices.collection_handoffs`, `invoices.bank_transactions`
  (notes), `invoices.pay_links` (revoked), `invoices.customer_reminder_policies` (deleted).
  **Kept**: the sent letters (the documentation of the claim, and the bad-debt VAT relief's
  evidence — FMVA § 4-7-1, R4 §2.9), charge payments, waivers, deliveries, hand-offs,
  attempts, and the bank files and transactions with their payer data — the bank's record of
  money received, bookkeeping material under § 13 like the payments (reading 11). The marker
  refuses any later letter and blanks a letter row inserted after it.
- **Retention**: bank files, letters and their PDFs are kept five years after the end of the
  financial year, as the invoice PDFs; the module deletes no object.

**Tests**: export carries each; erase does each and reports each; run twice reports zeros;
a letter racing the erase inserted with its recipient blanked.

### D20 — Out of scope, named

Customer credit balances, refunds as a flow (and the port's `Refund` used by Invoices),
setting an overpayment off against the next invoice, rounding off small differences (R4
§3.5 f); an export of payments or charges for the accountant; bank APIs and direct file
delivery; camt.053 and bank reconciliation of non-customer movements; the Vipps Report API
and settlement reconciliation (a payout is dismissed as `vipps_payout`); MobilePay markets,
other providers, cards, `PUSH_MESSAGE`, long-living payments; the creditor's own
betalingsoppfordring (INKL § 10, 3/20); the § 19 forskrift's egeninkasso fees; an agreed B2B
interest rate (R4); interest on fees; the chapter 2 cost caps; letters as EHF or eFaktura
(`srv/customers/billing_values.go:215-216`), SMS; a letter for charges alone; an agency API
and an automatic hand-off; a group-level reminder policy; the B2B ≤ 60-day term check
(R19); several KID lengths on one agreement; a holiday calendar; automatic webhook
re-registration; a "paid" stamp or receipt on the PDF; anything of a kassasystem — counter
sales, receipts, X/Z reports (Point of sale).

### D21 — The OpenAPI contract

`openapi/invoices.yaml` (the API reference regenerates from it):

- **4A**: `postInvoicesBankFiles` (multipart), `getInvoicesBankFiles`,
  `getInvoicesBankFilesById`, `postInvoicesBankFilesByIdMatch`, `getInvoicesBankAccounts`,
  `putInvoicesBankAccountsByAccountFormat`, `getInvoicesBankTransactions`,
  `postInvoicesBankTransactionsByIdApply`, `…Dismiss`, `…HandleReversal`,
  `…ConfirmDuplicate`, `…TreatAsDistinct`, `…Reopen`. Schemas `InvoicesBankFile`,
  `InvoicesBankImportResult`, `InvoicesBankAccount`, `InvoicesBankTransaction`,
  `InvoicesBankTransactionEvent`, `InvoicesBankTransactionSuggestion`, `InvoicesAllocation`;
  the payment gains `source`, `bankTransactionId`, and `registeredByUserId` leaves its
  `required` list (M1).
- **4B**: `getInvoicesOverdue`, `postInvoicesReminderRuns`, `getInvoicesReminderRuns`,
  `getInvoicesReminderRunsById`, `postInvoicesReminderPrintBatches`,
  `getInvoicesReminderPrintBatchesByIdPdf`, `postInvoicesReminderPrintBatchesByIdPosted`,
  `…Reprint`, `getInvoicesRemindersByIdPdf`, `postInvoicesRemindersByIdWithdraw`, `…Retry`,
  `postInvoicesByIdManualDeliveries`, `postInvoicesByIdManualDeliveriesByDeliveryIdRemove`,
  `postInvoicesByIdHold`, `postInvoicesByIdHoldLift`,
  `postInvoicesByIdCollection`, `postInvoicesByIdCollectionWithdraw`,
  `getInvoicesCollectionExportCsv`, `postInvoicesByIdChargePayments`,
  `postInvoicesByIdChargePaymentsByChargePaymentIdRemove`, `postInvoicesByIdChargesWaive`,
  `getInvoicesCustomersByCustomerIdReminderPolicy`, `put…`, `getInvoicesCollectionRates`,
  `postInvoicesCollectionRates`, `deleteInvoicesCollectionRatesById`,
  `getInvoicesSettingsReminders`, `putInvoicesSettingsReminders`, `getInvoicesStatsAttention`.
  The document gains `charges`, `waivers`, `manualDeliveries`, `reminders`, `hold`,
  `handoff`, `nextAction`.
- **4C**: `postInvoicesQuick`, `postInvoicesByIdPaymentRequests`,
  `getInvoicesByIdPaymentRequestsByAttemptId`, `…Cancel`, `postInvoicesByIdPayLink`,
  `…Revoke`, `getInvoicesPay`, `postInvoicesPayAttempts`,
  `getInvoicesPayAttemptsByReference` (the three anonymous), `postInvoicesVippsWebhooks`
  (anonymous), `getInvoicesVippsPayments` (+ `.csv`), `postInvoicesVippsPaymentsReconciliations`,
  `postInvoicesPaymentAttemptsByIdAbandon`, `get/put/deleteInvoicesSettingsVipps`,
  `postInvoicesSettingsVippsVerify`, `post/deleteInvoicesSettingsVippsWebhook`,
  `get/putInvoicesSettingsPayments`; the document gains `payLink` and `paymentAttempts`,
  the payment `paymentAttemptId`; meta D1's fields.
- **Codes** on `InvoicesConflictProblem`: `bank_import_format_mismatch`,
  `bank_account_unknown`, `bank_file_duplicate` (+ `bankFileId`, `uploadedAt`,
  `uploadedBy`), `bank_transaction_not_open`, `bank_transaction_not_applicable`,
  `bank_transaction_applied`, `reversal_payment_required`, `allocation_not_an_invoice`,
  `allocation_exceeds_transaction`, `paid_before_issue`,
  `charge_payment_exceeds_outstanding` (+ `chargesOutstanding`), `no_charges_outstanding`,
  `charge_not_claimed`, `collection_rate_exists`, `collection_rate_in_force`,
  `collection_rates_outdated` (+ `kind`, `halfYear`), `collection_regime_unreviewed`,
  `bank_import_stale` (+ `lastBookedOn`), `reminders_disabled`, `reminder_not_failed`,
  `reminder_not_awaiting_print`, `reminder_not_sent`, `reminder_not_withdrawable`,
  `reminder_posted_late`, `reminder_posted_early`, `delivery_removed`, `delivery_relied_on`,
  `too_many_overdue`, `credit_note_no_reminders`, `invoice_on_hold`,
  `invoice_not_on_hold`, `invoice_handed_off`, `invoice_not_handed_off`,
  `payments_unavailable`, `payment_attempts_active`, `attempt_not_abandonable`,
  `public_url_missing`, `provider_failed` (502), `pay_links_unavailable`, `pay_link_exists`,
  `too_many_attempts`, `on_site_payments_unavailable`, `buyer_identity_missing`,
  `payment_request_active`, `amount_out_of_range`, `day_already_reconciled`,
  `day_not_over`. Reasons, skip reasons, charge notes and attention types as enums.
  Warnings: `reminder_email_missing`, `mail_unavailable`, `collection_rates_outdated`,
  `collection_regime_unreviewed`, `collection_rate_differs_from_release`,
  `bank_data_stale`, `ocr_without_kid_payments`.

### D22 — Frontend

All in `@vantigo/invoices-ui` (`fe/`), mounted by host routes; en + nb throughout in
`invoicesCatalog` (`fe/i18n.ts`), the host's nav and attention sentences in the host
catalog.

- **Payments — "Innbetalinger" / "Payments"** (`host/routes/invoices/payments.tsx`,
  `invoices:payments`): upload a bank file (the duplicate and refusal messages in words),
  the bank accounts with their formats (the change for `invoices:manage`, with the cutover
  explained), the import's result (`/invoices/payments/files/$bankFileId`), the **exception
  queue** (filters by reason, file and unapplied rest; each line's KID or text, debtor,
  amounts and events; the suggestions; "Apply" — a dialog splitting the amount across
  invoices and their charges; "Not a customer payment"; "Handle reversal" with the payments
  to remove; "Confirm duplicate" and "Treat as distinct"; "Reopen"), and from 4C a **Vipps**
  tab with the day's list, its CSV and "Mark the day reconciled".
- **Overdue — "Forfalt" / "Overdue"** (`host/routes/invoices/overdue.tsx`,
  `invoices:access`; actions `invoices:payments`): the list, the bank data's freshness
  banner and the OCR note; "Send reminders" opens the **run preview** (the letters as they
  would go today, deselectable, the warnings, the stale-import confirmation checkbox) and
  makes the run; the run's page `/invoices/reminder-runs/$runId`; **paper letters**
  (`/invoices/reminders/print`): choose the letters and the posting date, download the
  batch, "Confirm posted" (or "Reprint").
- **The invoice page** (`fe/pages/invoice.tsx`): a **Reminders card** (each letter, its PDF,
  withdraw, retry), the **charges** (claimed, waived, paid, outstanding) with "Register a
  charge payment" and "Waive", **Deliveries** with "Record a delivery" (handed over or
  posted), **hold** and **lift** (the groundless question, defaulting to no), **hand off to
  collection** and its export, the payments' source and bank line, and from 4C the pay link
  and "Take a Vipps payment now" only when on-site payment is enabled.
- **Settings** (`fe/pages/settings.tsx`): **Reminders** (D7's fields, the regime date and
  the review with their explanation), **Collection rates** (the rows, in force highlighted,
  the release-differs warning, add a future row, delete an unused one); from 4C **Vipps**
  (MSN, keys write-only, verify, the webhook) and **Pay links and on-site payment** (the
  switches, the URLs, the kontantsalg notice and acknowledgement in full, `PUBLIC_BASE_URL`
  missing explained).
- **The customer**: a **Reminder policy** card in `CustomerInvoicesPanel` on the customer's
  Invoices tab (`host/routes/customers/-customer-invoices-tab.tsx`).
- **The quick invoice** (4C, `host/routes/invoices/quick.tsx`, `canQuickInvoice`): one screen
  for a tablet — the customer (search, or "New customer" through the customers API when the
  caller holds `customers:create`; the address shown and required for on-site payment), one
  to ten lines, "Issue" and, when on-site payment is enabled, "Issue and take payment with
  Vipps" (the QR full screen, polled, cancel); every refusal in words.
- **The pay page** (4C, `host/routes/pay.tsx`, public): D15.
- The dashboard's attention sentences for every type; the nav's two new entries (Payments,
  Overdue) behind their permissions.

### D23 — Documentation, as its own deliverable

Per `AGENTS.md`'s page map, in each pull request for what it ships:

- **`R/invoices.md`**: new sections "Bank files and the exception queue" (formats,
  pre-checks, the account's format and cutover, dedupe and duplicates, matching and its
  table, possible duplicates, the reasons and actions, the allocation order), "Charges"
  (waivers, the allocation within charges), "Reminders" (the rates, the two regimes and the
  review, the settings and the customer policy, the delivery fact, the engine's rules with
  their sources — the reset table among them — runs, the stale-import confirmation, letters,
  the worker, paper and posting, the letter's content), "Holds and the hand-off to
  collection" (and the CSV's columns), "Vipps payments" (the port, credentials, the public
  host and its allowlist, the pay page and pay links, attempts, capture, reservation,
  `capture_unknown` and abandon, the refund watch, webhooks), "The quick invoice" (one
  transaction; credit sale vs kontantsalg; the hard rules; the daily list and
  reconciliation); the model table's new tables and columns; "Payments and the state of an
  invoice"; the lock order paragraph (D18); permissions (D1); endpoints; retention and
  personal data (D19); stats (attention); **"What comes next" corrected** — overpayment,
  customer credit balances and refunds as a flow are **not** phase 4 (R4 §6 item 2,
  `R/invoices.md:2083-2085`); the opening paragraph's "nothing is matched to a bank file"
  goes.
- **`MB`**: rule 1's platform list names `internal/payments` and its Vipps adapter; "Invoices
  requires customers" gains the directory reads of the run and the export; the router's
  raw-body option; the public host gate in the platform.
- **`R/customers.md`**: the anonymisation table's new kinds; the merge's reminder policy rule.
- **User guide** `en|nb/user/invoices.md`: "Importing payments from the bank" (the format per
  account and changing it; **why a genuine second payment of the same amount with the same
  KID on the same day in another file is queued as a possible duplicate, and how to apply
  it**, m12), "The exception queue", "Overdue invoices and reminders" (the
  stale-import confirmation; with OCR, registering payments without a KID first), "Printing
  and posting paper letters", "Recording a delivery", "A disputed invoice", "Waiving a
  charge", "Handing an invoice to collection", "Charges", "Reminder settings, the regime and
  rates", "A customer's reminder policy", and **that bad-debt VAT relief needs at least three
  reminders** (FMVA § 4-7-1 b) while the default sequence sends two (M17); from 4C "Vipps and
  pay links", "The quick invoice", "Taking a payment on site" (what kontantsalg means, in
  plain words) and "The day's Vipps payments and reconciliation"; the permissions section.
- **A new administration page** `en|nb/admin/payments.md` (listed in the admin overview;
  `sources` `apps/server/internal/payments`, the invoices files of the imports and the Vipps
  setup, and the public host gate): **the bank agreement** — an OCR/KID agreement; an eGiro /
  camt.054 "Innbetaling Total" agreement; tvungen KID; where to download the files (R4 §3.4);
  one format per account and what changing it does; **Vipps** — the merchant agreement, the
  sales unit's keys, test vs production and `INVOICES_VIPPS_BASE_URL`,
  `INVOICES_VIPPS_ENABLED`; **the public host** — `PUBLIC_BASE_URL`, a separate host with the
  same base path, the reverse proxy preserving `Host`, the allowlist and how to verify it
  (`curl` of `/sign-in` on the public host answering 404), **`TRUSTED_PROXY_HOPS` and
  `TRUSTED_PROXY_CIDRS` for the pay page's rate limits**, **and keeping `/pay`'s query
  string out of the proxy's access log** (it carries the pay token; nginx and Caddy
  examples); the webhook; the
  `invoices-payments` worker, `capture_unknown` and abandon; **the reminder worker** and
  mail; the kontantsalg notice for an operator enabling on-site payment.
- `admin/authentication.md`'s configuration reference: `PUBLIC_BASE_URL`,
  `INVOICES_VIPPS_ENABLED`, `INVOICES_VIPPS_BASE_URL`; `admin/installation.md` "Background
  workers and scaling": `invoices-reminders`, `invoices-payments`; `admin/object-storage.md`
  (en + nb): the `bank-files/` and `reminders/` keys; `deploy/compose` `vantigo.env.example`.
- **`ROADMAP.md`**: phase 4 as delivered with its design link; **the quick-invoice paragraph
  corrected** — "a credit sale when paid later; paid on site by Vipps it is kontantsalg,
  allowed without a kassasystem only as a kontantfaktura, which the quick invoice enforces
  (the buyer named with an address or an organisation number, one payment per invoice in the
  ordinary series, a notice, the day's list and reconciliation)"; **the Point of sale section
  corrected** — an invoice paid at delivery is still kontantsalg; a business that sells over
  a counter needs a kassasystem whatever document it issues; Invoices' on-site payment covers
  only the kontantfaktura case, and research item 1 is narrowed to the register itself; the
  backlog gains D20's items.

The docs task checks every page against the code, and `mise run docs:check` passes.

## Readings on the record

Each an interpretation the user may overturn.

1. **The 2026 regime by a dated setting.** `inkassolov_2026_from` (NULL until Kongen sets
   the date) switches, per letter on its date: no fee, no creditor's inkassovarsel, a last
   reminder announcing the inkassoforetak; interest and the B2B compensation continue.
2. **Rates as seeded rows plus a screen**; a release's seed never fails on a user's row and
   records its own value beside it.
3. **An outdated half-yearly rate refuses** the run and holds the dispatch.
4. **Overpayment is left unapplied** on its bank line; **no customer credit balance and no
   refunds** in phase 4.
5. **Interest always cumulative from the day after the effective due date**, an interest
   waiver an amount beside it (never a new start), simple, actual/365, each day on the principal open at the end of the day before
   — a credit note counting from its own date, a payment from the day after its own — split
   at every rate change, payment and credit note, rounded to øre once.
6. **Principal first.** A payment pays the principal before any charge; a payment of charges
   is its own record.
7. **The policy's home is Invoices**, under `invoices:payments`; a merge keeps the stricter
   mode; anonymisation deletes the row.
8. **Weekend due dates move to Monday** for the engine; holidays do not; the state `overdue`
   is unchanged.
9. **Fee or compensation is the installation's choice for business buyers**, never both;
   the compensation needs a business **with an organisation number** (or a foreign business
   id) on the snapshot — any other buyer is treated as a person — and is claimed once per
   invoice, on its first letter.
10. **The compensation's NOK figure is the one in force on the letter's date.**
11. **Bank files and transactions are kept through anonymisation** as bookkeeping material;
    their notes linked to the person are blanked.
12. **`paidOn` is the booking date**; a booking before the issue date is queued.
13. **Reminder e-mail is at least once**, with a stable Message-ID; a letter's facts are
    written at sending and frozen once sent.
14. **A paper letter is sent when its batch is confirmed posted on the date it bears**;
    posted on any other day, it is reprinted (reading 39).
15. **Capture at once** on `AUTHORIZED`, of min(authorized, open), reserved under the
    invoice's lock, the provider called outside it.
16. **Poll first, webhooks optional**; webhooks ship only if the tagged test pins the
    signature.
17. **The pay page exists** on `PUBLIC_BASE_URL`, which may be its own host with the same base
    path; on that host the server answers only an allowlist; the page never shows the buyer.
18. **A pay link is made at issue** when available and printed with a QR in the PDF; the
    token is in the query string and stored hashed.
19. **The quick invoice paid on site is kontantsalg**, offered only as a kontantfaktura under
    D17's hard rules; paid later it is a credit sale. The roadmap is corrected.
20. **The pay link shown on site is treated as kontantsalg too**: the app shows no pay link
    or QR on screen unless on-site payment (and so the acknowledgement) is enabled, and
    turning pay links on shows the notice. What remains advisory: a user can still open the
    PDF — which carries the QR for the remote customer — and hold it up; Vantigo cannot
    prevent that, and the notice says it is the same kontantsalg.
21. **One import format per account**, set by its first import; a change records a cutover,
    and the old format's period is never auto-matched again.
22. **The fingerprint's ordinal** keeps identical lines of one file apart; a line colliding
    with a known one is kept as a `duplicate` row a person can treat as distinct.
23. **Card information (OCR 18–21), non-reversal debits and 0.00 transactions are ignored**,
    counted; reversals are queued and handled by removing the reversed payments.
24. **A hold's lift decides charges**: by default the objection is upheld and every fee and
    compensation claimed is waived and barred; interest keeps running.
25. **Payments continue after a hand-off**; the pay page closes.
26. **No new permission.**
27. **`INVOICES_VIPPS_ENABLED`** defaults on.
28. **The betalingsoppfordring is not offered**, and a purring is not required before an
    inkassovarsel.
29. **A reminder is not a salgsdokument** and takes no number (an inference).
30. **A charge needs a recorded delivery on or before the due date** — an e-mail, a delivered
    EHF transmission or a manual record; without one, letters are fee-free and the
    inkassovarsel and the hand-off are blocked (reading 40). A delivery after the due date is
    treated as none (the due date the customer had was not a fair one).
31. **Within charges, a payment pays fees and compensation first, oldest letter first, then
    interest**; a letter shows earlier fees and compensation, then the cumulative interest
    once, less what of it is paid.
32. **The six-month reset is a chain** counted back from the last fee letter, stopping at a
    gap of more than six months, with months added clamped to the month's end; the
    anniversary day itself is still inside the six months, so the fee is allowed from the day
    after (domstolloven § 148's month rule, the conservative reading).
33. **A deadline is met when the payments ordered by it cover the principal**; a fee claimed
    after a met deadline is waived when the proof arrives.
34. **A run with any charge on stale bank data needs an explicit confirmation**
    (`stale_import_days`, default 3); fee-free letters never do.
35. **The regime must be reviewed**: after `regime_reviewed_through` (seeded 2026-12-31) no
    fee and no creditor's inkassovarsel until a manager confirms; fee-free letters continue.
36. **An unknown capture keeps its reservation** (`capture_unknown`) until a definitive answer
    or a manager's abandon after checking the Vipps portal; a partial capture registers what
    was captured.
37. **Refunds made in the Vipps portal are watched for 30 days** and reported, never applied
    automatically.
38. **The day's Vipps payments can be recorded as reconciled**, immutably.
39. **A paper batch is sent on the day it bears and no other**: `postedOn` must equal
    `postOn`; the confirmation re-judges each letter and waives a charge the day no longer
    supports (`claimed_in_error`).
40. **An undelivered invoice is not due**: without a recorded delivery only fee-free
    reminders go; the inkassovarsel and the hand-off wait for one. A manual delivery can be
    removed with a reason until a sent letter's charge relies on it.
41. **The pay page's return URL carries an attempt nonce, never the token**; a reverse
    proxy must keep `/pay`'s query out of its access log.
42. **A late-resolved capture is dated by the provider's capture event**, not the day it was
    learned.

## Testing

Through the invoices harness, plus a fake object store, the fixed clock (moved across Oslo
midnight, month ends, 1 January and 1 July, and to 2027 for the outdated-rate and
unreviewed-regime cases), `Deps.HTTPTransport` for `vippstest`, the SMTP seam recording
messages, and committed fixtures — constructed OCR files (R4 §3.1's example and one per
rule), camt.054 files in both namespaces in our own words (no third-party file in the
repository, R4 §3.2), and the vendored ISO 20022 XSDs with their NOTICE (R4 §3.7). Each
decision's named tests are listed in it; in summary:

- **4A**: parsing and pre-checks per format (the camt hardening); detection; the account,
  duplicate, per-account format, cutover and fingerprint rules; `duplicate` rows; stored
  once; the classification order with `possible_duplicate`; the match table; the queue's
  endpoints, reopen from `matched`, handle-reversal; the payments' source CHECK; the races
  `TestBankImport_TwoOverlappingImports` (with its first-import variant),
  `TestBankImport_RacesManualPayment`, `TestBankImport_OtherFormatRace`,
  `TestBankQueue_ApplyRacesManualPayment`, `TestBankQueue_ReversalRacesApply`.
- **4B**: the seeds, the release-over-user seed and the rates API; the settings, the review
  and the policy (`TestPolicy_MergeRacesPolicyPut`); the engine (`TestReminderRules_*` —
  `addMonthsClamped`, the reset table, the delivery fact, the met deadline, compensation's
  organisation number, interest with credits and waivers, `letter_pending`); charges,
  waivers, charge payments and the B2 golden pair; preview, run, stale import, worker,
  paper batches and posting, withdraw, retry; letter goldens in both languages; manual
  deliveries; holds and the barring lift's waivers; hand-offs; the export; the overdue list
  and attention; the races `TestReminderRun_RacesPayment`, `TestReminderRun_TwoRuns`,
  `TestReminderDispatch_RacesHold`, `TestReminderDispatch_RacesImport`,
  `TestReminderDispatch_RacesWithdraw`, `TestHandoff_RacesReminderDispatch`.
- **4C**: the adapter over `vippstest`; the tagged `//go:build vipps` test; `PUBLIC_BASE_URL`
  and `TestPublicHostGate`; credentials and payment settings; pay links, the hashed token,
  the anonymous operations and their buckets; attempts and the worker through every state,
  `capture_unknown`, abandon, `release_pending`, the refund watch; exactly-once; the
  webhook; the quick invoice's one transaction and its delivery record; the on-site rules
  and the on-screen gating; the daily list and reconciliation; the races
  `TestVippsCapture_RacesManualPayment`, `TestVippsCapture_TwoAttemptsBothAuthorized`,
  `TestVippsCapture_RacesImport`, `TestVippsCapture_RacesCreditNote`,
  `TestQuickInvoice_RacesIssue`.
- **The integration test** (`srv/integration`, real customers and invoices): issue two
  invoices with KIDs and e-mail them → import an OCR file paying one in full and one in
  part, plus an unknown KID → two payments and one exception → a reminder run on the partly
  paid one (fresh bank data) → the letter sent with its fee → a camt.054 for the account
  after a format change, repeating the OCR period's payment (→ `possible_duplicate`) and
  paying principal and fee with the KID (→ a payment and a charge payment, the invoice
  paid, charges zero); then (4C) a quick invoice paid on site through `vippstest` →
  captured, registered once, on the day's list, the day reconciled.
- **Frontend**: each screen and dialog with its refusals in words, the public pay page
  without a session and making no request outside the allowlist, the on-screen pay-link
  gating, the attention sentences, both catalogs.
- **Docs**: D23's pages against the code; `mise run docs:check`.

## Phasing

Two pull requests, three sub-phases, each a working state with its docs:

- **4A — the ledger and bank imports** (PR 1): `00041`'s payments columns, bank import
  accounts, files, transactions and events; `kid.Parse`; the two parsers; the import, the
  match and the queue; the payments' `source` on the wire; the Payments screen; the
  reference, user and admin (bank agreement) sections.
- **4B — overdue and reminders** (PR 1): `00041`'s rates (seeded), reminder settings (with
  the review), customer policies, manual deliveries, runs, letters, print batches, holds,
  hand-offs, charge payments and waivers; the engine; the worker and the letter PDF; the
  overdue list, attention, the export; the slots; the Overdue screen, the invoice cards, the
  settings cards, the customer card; the docs' remaining sections, the correction of "What
  comes next".
- **4C — the payments port, Vipps, the pay page and the quick invoice** (PR 2, planned after
  PR 1 merges): `00042`; `srv/payments`, the Vipps adapter, `vippstest`, the tagged test;
  `PUBLIC_BASE_URL`, `HostFilter`'s second host and the public host gate; the switches;
  credentials and payment settings; pay links, the anonymous operations, the router's
  raw-body option and the webhook; attempts and the worker; the reservation in `openOf`; the
  issue refactor and the quick invoice; on-site payment, the daily list and reconciliation;
  the pay page and the quick invoice screen; MB rule 1; the admin page's Vipps and public
  host half; the roadmap's quick-invoice and Point of sale corrections.

## Open questions

What only the user can answer; the design's interim answer in brackets.

1. **A Vipps merchant agreement and a test sales unit** for Vantigo's own tagged test (R4
   §4.8). [4C's adapter is built against `vippstest`; the tagged test waits for the keys.]
2. **Which bank and which files** for the user's own account: camt.054 or only OCR, and can
   a real file (anonymised) be used as a private fixture? [Both formats are built; each
   account picks one.]
3. **Webhooks without a pinned signature**: ship poll-only if no real test-environment
   webhook can be captured before 4C? [Yes, reading 16.]
4. **Defaults**: late interest (off), B2B fee or compensation (fee), reminders before the
   inkassovarsel (1 — and three reminders are needed for bad-debt VAT relief), the first
   reminder at 14 days, `stale_import_days` (3).
5. **Vipps' view** on paying an issued invoice through standard "Integrert betaling", the
   terms-acceptance rule for it, and whether on-site and remote payments need separate sales
   units (R4 §7 items 31, 32). [One sales unit; the pay page asks for acceptance.]
6. **Exposing the installation**: a separate public host for the pay page, or QR-only?
   [Both work; a separate host is recommended.]
7. **The kontantsalg acknowledgement's wording**: should the user's accountant or revisor
   review it before on-site payment is turned on anywhere? [The text follows R4 §9 in
   substance.]
8. **The collection agency**: which inkassobyrå, and will it take the CSV as is?
9. **The new inkassolov's in-force date** when announced: set by a release, by the user in
   settings, or both — and who moves the regime review meanwhile? [Both are possible; the
   review lapses on 2026-12-31 by default.]
10. **Plain e-mail for an inkassovarsel** (R4 §7 item 9: whether e-mail is "betryggende"):
    should the varsel go by paper by default, or as the profile says? [As the profile says.]
