---
title: "Accounting module"
description: "The company's books: sales that post themselves — the catch-up worker, its event handlers, streams, cursors and leases, what each document and fact posts as with every system rule and its version, the books' start and the opening's seqs, the posting rules, Needs attention and retry; the module and the module it requires; the four permissions; Set up the books and the gate before it; the settings; the default chart with Skatteetaten's grouping codes, the system roles and the standard tax codes with their rates from 2022; accounts and their rules; groupings per year; tax codes and rate periods; vouchers and postings and the rules the database keeps — balance, a voucher has postings, immutability, postings only on the voucher just inserted, the books' start and the lock read under a share lock; the posting engine — numbering, dating, the lock rule, zero lines, open items and idempotence on the source key; manual journals and their VAT lines; reversal; attachments; the lock and its history; the opening balances and Invoices' open invoices; open items and settlements; the reports and their CSV; the refusals and the endpoints."
sources:
  - apps/server/internal/accounting
  - openapi/accounting.yaml
  - apps/server/internal/openapi/specs/accounting.yaml
  - apps/server/internal/db/migrations/00181_accounting_baseline.sql
  - apps/server/internal/db/migrations/00183_accounting_sales.sql
---

Accounting keeps the company's books as bokføringsloven asks for them. Its package is
[`apps/server/internal/accounting`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/accounting),
its schema `accounting` (migration `00181_accounting_baseline.sql`), its contract
[`openapi/accounting.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/accounting.yaml),
mounted at `/api/v1/accounting`. The design is
`docs/superpowers/specs/2026-11-14-accounting-design.md`; this page is what deliveries A
(the ledger) and B (sales post themselves, migration `00183_accounting_sales.sql`) built. The user guide is [Accounting](/en/user/accounting/).

## The module

- `MODULES`: **`accounting` requires `invoices`** (which requires `customers`); without it
  the server refuses to start, naming it. It reads `contracts.SalesJournal` (Invoices'
  open invoices for the opening balances), `contracts.CustomerDirectory` (customers'
  names) and, when Projects is on, `contracts.ProjectDirectory` (projects' names). Every
  call out of the module is made with no transaction of its own open.
- It publishes no event. It starts one worker, `accounting.catchup`, and declares three
  event handlers ([Sales post themselves](#sales-post-themselves)); before the books are
  set up both return at once, and the handlers do nothing with the module off in
  `MODULES`.
- Attachments live in the object store under the scope `accounting`.

## Permissions

| Key | Display | Delegable | Sensitive | Asked by |
| --- | --- | --- | --- | --- |
| `accounting:read` | Use Accounting | yes | no | every operation |
| `accounting:post` | Post vouchers | yes | no | manual journals, their preview, reversal, attachments, settlements, a retry under Needs attention |
| `accounting:close` | Manage the books | **no** | no | setup, the chart, the roles, the tax codes, the posting rules, the opening balances, the lock |
| `accounting:export` | Export the books | yes | **yes** | the reports' CSV |

A write asks for `accounting:read` and its own key (`permission:accounting:post+accounting:read`
and so on). `accounting:close` is in the agents' never list: no virtual employee may hold
it. The Assistant never uploads or downloads an attachment or a CSV
(`x-vantigo-assistant: never`); the preview is callable without asking (`auto`). Every
other write — a manual journal, a reversal, a settlement, the opening balances and the
lock — is called only after the person confirmed it (`confirm`, S4's default for a write;
`internal/integration`'s contract test pins those five), and the opening and the lock
need the person's own `accounting:close`.

## Set up the books

Every known module is on by default, so an upgrade switches Accounting on. **Before the
settings row exists, every operation but `GET /meta` and `POST /setup` answers 409
`books_not_set_up`**, from a strict middleware that runs before any handler.

`POST /setup` (`accounting:close`) takes `booksStart` (2022-01-01 or later, else 400
`books_start_too_early`: the seeded rates are known to hold from then), `fiscalYearStartMonth` (1–12,
default 1), `vatTerm` (`bimonthly`, `yearly`, `none`) and `language` (`en` or `nb`). In one
transaction it writes the settings row — a second setup answers 409
`books_already_set_up` — and seeds:

- **The default chart**, 129 accounts named from Skatteetaten's næringsspesifikasjon list
  (`internal/accounting/grouping/2026.csv`, the 2025–2026 list, embedded whole for the
  grouping picker) in the chosen language. Each account's grouping is its own number and
  that code's category, from `booksStart`'s year. The accounts the list has no code for
  carry names of the module's own and the nearest code: the separate VAT accounts
  2700–2715 (grouped 2740), Vipps clearing 1580 (1570), the opening balance contra account
  2060 (2050), bank and payment fees 7770 and rounding 7790 (7700). An account's kind
  follows its number's class: 1 asset; 20 equity, 21–29 liability; 3 and 80 income; 4–7
  and 81–89 expense. An account created or changed later is held to the same classes
  (see [Accounts and tax codes](#accounts-and-tax-codes)).
- **The system roles**, each on one account:

  | Role | Account | | Role | Account |
  | --- | --- | --- | --- | --- |
  | `receivables` | 1500 | | `opening` | 2060 |
  | `payables` | 2400 | | `year_result` | 2050 |
  | `bank` | 1920 | | `clearing_vipps` | 1580 |
  | `vat_settlement` | 2740 | | `charge_income` | 3900 |
  | `rounding` | 7790 | | `currency_gain` | 8060 |
  | `bad_debt` | 7830 | | `currency_loss` | 8160 |
  | `employee_payables` | 2910 | | `payment_fees` | 7770 |

  Every charge payment posts to `charge_income`; interest income is the ordinary account
  8050, not a role (design R14). A role sits on an account of its kind: `bank`,
  `receivables` and `clearing_vipps` on an asset; `payables`, `vat_settlement` and
  `employee_payables` on a liability; `opening` and `year_result` on equity;
  `charge_income` and `currency_gain` on income; `rounding`, `bad_debt`, `currency_loss`
  and `payment_fees` on an expense account.
- **The tax codes**: Skatteetaten's whole standard list (30 codes), named from
  `grouping/standard-tax-codes.csv` with the rate after the name, each with its direction,
  reverse-charge flag, VAT account and, for reverse charge, counter account, and
  `reported = false` for 0, 7, 20, 21 and 22. The rates are Invoices' own (migration
  00034): 25, 15, 12 and 11.11 % for raw fish, in one period from 2022-01-01, the first
  day all four have stood unchanged since (the low rate's temporary 6 % ended on
  2021-09-30). That is why the books cannot start earlier.

  | Codes | Direction | VAT account | Counter |
  | --- | --- | --- | --- |
  | 1, 11, 12, 13 | input, 25/15/11.11/12 % | 2710, 2711, 2713, 2712 | — |
  | 14, 15 | input (import VAT paid) | 2714 | — |
  | 3, 31, 32, 33 | output, 25/15/11.11/12 % | 2700, 2701, 2703, 2702 | — |
  | 5, 51, 52, 6 | output, 0 % | — | — |
  | 0, 7, 20, 21, 22 | none, not reported | — | — |
  | 81, 83 | reverse charge, import with deduction | 2714 | 2705 |
  | 86, 88, 91 | reverse charge, services and emissions/gold with deduction | 2715 | 2705 |
  | 82, 84, 85, 87, 89, 92 | reverse charge without deduction | the line's own account | 2705 |

- **The default posting rules**, one per sales tax code ([Posting rules](#posting-rules)).

The setup's choices never change afterwards; nothing changes `books_start`.

## The data model

| Table | What it holds |
| --- | --- |
| `settings` | One row: `books_start`, `fiscal_year_start_month`, `vat_term`, `locked_through` (a month end, null before the first lock), `chart_language`, `opening_as_of_seq` (the lowest `AsOfSeq` of the `OpenAt` pages the opening was entered from; see [Opening balances](#opening-balances)). |
| `counters` | The voucher number series, one row `voucher:<fiscal year>` per year. |
| `accounts` | `number` (4–6 digits, unique), `name`, `kind`, `default_tax_code_id`, `active`, `system_role` (unique), `revision`. |
| `account_groupings` | `account_id`, `from_year`, `category`, `code`: the grouping in force a year is the row with the greatest `from_year` not after it. |
| `tax_codes`, `tax_code_rates` | The codes and their rate periods; no two periods of a code overlap (an exclusion constraint). |
| `vouchers` | `fiscal_year`, `number`, `voucher_date`, `document_date`, `description`, `kind`, `source_module`/`source_kind`/`source_id`/`source_part`, `rule`/`rule_version`, `reverses_voucher_id` with `reversal_reason`, `posted_at`, `posted_by_user_id`. |
| `postings` | `voucher_id`, `line_no`, `account_id`, `amount` (signed numeric(14,2): debit positive, never zero), the currency columns (null in this phase), `tax_code_id`/`tax_role`/`tax_base`, `customer_id` (Customers' integer id) or `supplier_id` (this module's bigint, delivery C), `open_item_id`, `project_id` (Projects' integer id), `description`. |
| `attachments` | `voucher_id`, `object_key`, `file_name`, `content_type`, `size_bytes`, `sha256`, `uploaded_at`, `uploaded_by_user_id`. |
| `open_items` | `kind` (`customer`, `supplier`), the party, `unallocated`, `document_number`, `document_date`, `due_date`, `original_amount`, `source_document_id`, `opening_as_of_seq` (for an item the opening made for an invoice in Invoices, the `AsOfSeq` of the page it was read on; null otherwise). One unallocated item per party and kind; one item per source document and kind. |
| `lock_events` | Every move of the lock: from, to, reason, when, who. |
| `posting_rules` | `name` (unique among the rules in force), `scope` (`sales`; `expense` arrives with delivery C), `match` (`line_kind`, `tax_category`, `expense_category`, `tax_code`, `default`) with exactly the one value its match names, `account_id`, `version`, `revision`, `archived_at` (set when it is archived; a delete is refused). One rule in force per scope and match value. |
| `posting_rule_versions` | Every version of a rule: `rule_id`, `version`, `name`, `account_id`, `valid_from`, written by a trigger when a rule is made and each time its version moves. |
| `cursors` | One row per stream (`invoices.documents`, `invoices.facts`): `position` (the last document number or fact seq read), `lease_id`/`lease_until`, `last_run_at`, `last_error`. |
| `catchup_requests` | A wake-up per stream, `requested_at`: what a handler writes and a claim deletes. |
| `unposted` | A source fact that could not be posted: its stream and position, its source key (unique), date, the Invoices document, `invoice_id` (the invoice it is about — a credit note's, the one it credits), customer and amount it is about, `reason`, `detail`, `attempts`, `waiting_on` (with reason `waiting_on`, the row it waits behind), and once a retry posted it `posted_voucher_id` and `posted_at`. |

Migration 00183 also adds `vouchers.source_document_id` (the Invoices document a system
voucher belongs to — an invoice for its own voucher and for its payments', credit notes'
and write-offs'), `postings.tax_specification` (the VAT return's specification line of
a tax posting: `tapPåKrav` for a loss's VAT taken back; it needs a tax code) and
`postings.posting_rule_id`/`posting_rule_version` (the rule version that placed a revenue
line, a key into `posting_rule_versions`; null on every other posting).

No key crosses a schema: a customer, a project and a person are opaque ids. There is no
department dimension in this phase; the project is the one dimension.

## The rules the database keeps

| Rule | How |
| --- | --- |
| A voucher's postings sum to zero | deferred constraint trigger `tr_postings_balance`, at commit |
| A voucher has postings | deferred constraint trigger `tr_vouchers_has_postings`, at commit |
| Vouchers, postings, attachments and lock events are never updated, deleted or truncated | `refuse_change()` before update or delete, and before `TRUNCATE` as a statement trigger |
| A posting is added only to the voucher its own transaction has just inserted — never to a committed one, whose date the lock no longer covers | the vouchers' `mark_open_voucher()` after insert records the new id in the transaction-local setting `accounting.open_voucher` (`set_config(…, true)`); the postings' `check_posting_voucher()` before insert refuses any other voucher id |
| A posting is never zero | `CHECK (amount <> 0)` |
| A voucher is dated on or after `books_start` and after `locked_through` | `check_voucher_date()` before insert, reading `settings` **FOR SHARE** |
| One source fact, one voucher | unique index on `(source_module, source_kind, source_id, source_part)` where `source_module` is set; `source_part` is `''`, never null |
| A voucher is reversed at most once | unique index on `reverses_voucher_id` |
| An account's number and kind freeze at its first posting | `freeze_posted_account()` before update |
| An account with postings is never deleted | the postings' `ON DELETE RESTRICT` |

The lock and a post cannot pass each other: a post holds the settings row FOR SHARE to its
commit, and moving the lock takes it FOR UPDATE, so each waits for the other and a post
that waited reads the new lock.

The open voucher is a setting, not a comparison of the row's `xmin` with
`pg_current_xact_id()`: a row inserted inside a savepoint carries the subtransaction's
id, and the engine runs inside one. The setting follows savepoints — rolled back with
one, kept when it is released — and ends with the transaction, so a pooled connection
carries nothing on. It guards code paths; a session that sets it by hand could as well
drop the trigger.

## The posting engine

Every voucher — a person's journal, the opening balances, a reversal, a settlement and,
from delivery B, every source fact — is posted by one function on the caller's
transaction:

```go
func Post(ctx context.Context, tx pgx.Tx, v Voucher) (Posted, error)
```

A `Voucher` has a kind, the date asked for, the document date (default the date), a
description, an optional `Source{Module, Kind, ID, Part}` and the Invoices document it
belongs to (`SourceDocumentID`), the rule and its version, the
voucher it reverses with the reason, who posts it, and its `Line`s: an account id or a
system role, a signed exact amount, an optional tax code with its role (`base` or `tax`),
base and VAT-return specification (`TaxSpecification`), a customer or a supplier, the open item (by id, by the Invoices document it
stands for, a new item with its document number and dates, or else the party's
unallocated item), a project and a text. In order, `Post`:

1. With a source already posted, answers that voucher with `Repeat` and writes nothing.
   Everything after this step runs inside a savepoint of the caller's transaction: a
   refusal undoes what the post wrote and leaves the transaction usable, and a source
   another transaction posted at the same moment — the source index's violation — is read
   back and answered as `Repeat`, its number given back.
2. Reads the settings FOR SHARE.
3. Dates the voucher. **A person's voucher** (no source) dated before `books_start` is
   refused 400 `date_before_books_start`, and one on or before the lock 409
   `period_locked`. **A system voucher** is booked on `books_start`, or on the first day
   after the lock, keeping its document date (`Moved`).
4. Resolves the roles to accounts, **leaves out zero lines** (the tax code rides on the
   base posting) and refuses 400 `voucher_empty` when nothing is left, `amount_invalid`
   for more than two decimals, `account_unknown` or `account_inactive`, `party_required`
   for a receivables line without a customer or a payables line without a supplier,
   `party_not_allowed` for both or the wrong kind, and `open_item_unknown` for an item on
   another account or another party's item. **A reversal** may post on an inactive
   account: it turns back what was posted there. A refusal names the line by its place
   in the voucher as given, zero lines counted.
5. Refuses 400 `voucher_unbalanced` when the lines do not sum to zero.
6. Attaches each receivables and payables line to its open item, making it when needed:
   an item named by id, or found by its Invoices document, must be the line's party's
   (400 `open_item_unknown`); **a person's voucher** — no source, not a reversal — may not
   name by id an item that stands for an Invoices document (400
   `open_item_source_document`: it moves with Invoices' own facts). Two transactions
   opening the item for one document at once both end on the one item. Then it takes
   every item the voucher names **FOR UPDATE, in id order**, so two posts on one item —
   a settlement and a journal — wait for each other rather than deadlock.
7. Takes the next number of the voucher's fiscal year from its counter row — held to the
   commit, so a failed post gives the number back — and inserts the voucher and its
   postings.

A voucher's fiscal year is the calendar year in which the fiscal year containing its date
begins; its number shows as `2026-000123`.

## Sales post themselves

Invoices' [`contracts.SalesJournal`](/en/reference/invoices/#what-invoices-reports-to-other-modules)
is the source (design A12, R1, R7); Invoices' events only make the books prompt. Nothing
of it refuses an invoice: what cannot be posted waits under
[Needs attention](#needs-attention) and the cursor moves on (A15).

### The handlers and the worker

| Handler | Event | Writes |
| --- | --- | --- |
| `accounting.catchup.document-issued` | `invoices.document.issued` v1 | a wake-up for `invoices.documents` |
| `accounting.catchup.payment-received` | `invoices.payment.received` v1 | a wake-up for `invoices.facts` |
| `accounting.catchup.payment-removed` | `invoices.payment.removed` v1 | a wake-up for `invoices.facts` |

Each upserts its stream's `catchup_requests` row on the delivery's transaction and does
nothing else (module-boundaries rule 23): it reads no contract and locks nothing but that
row. Charge payments and write-offs publish no event; the sweep finds them.

Worker **`accounting.catchup`** runs every 15 seconds. For each stream, documents first so
an invoice's item exists before its payment posts:

1. **Claim**, in one short transaction: make the cursor row if missing, then take its lease
   (`lease_until` two minutes on) when no live lease holds it **and** a wake-up waits or the
   last run is five minutes old; delete the wake-up. A stream not due, or held, is skipped.
2. **Read** the journal after `position`, 200 at a time, **with no transaction open** —
   `Documents` by number, `Facts` by seq, and for a write-off or a recovery whose VAT is
   corrected the invoice through `Document` and its credit notes through `CreditNotesOf`.
3. **Post** each item in **its own transaction**, which also moves `position` under the
   lease (`AdvanceCursor` names `lease_id`; no row is a lost lease, which rolls the item
   back and stops the run). A run stops 30 seconds before its lease ends. A transaction
   that meets a deadlock (SQLSTATE `40P01` — a person settling the same open item at that
   moment) is tried once more; any other error stops the run, recorded as `last_error`,
   and the next run takes the item up again.
4. **Release** the lease, on a context the shutdown does not reach, with `last_run_at` and
   the error that stopped the run, if one did. **A run that stopped at its lease's margin
   with items left first upserts its stream's wake-up** (R15), so the next poll, fifteen
   seconds on, takes the backlog up rather than the five-minute sweep.

A crashed claim holds its stream until its lease runs out. A fact delivered twice, a
cursor put back, and two workers at once all end on one voucher per source
(`ux_vouchers_source`, and the engine's `Repeat`). A handler added late, a pruned event
and a dead delivery end the same way: the next sweep posts what is missing. Nothing runs
before the books are set up, nor without Invoices' journal. The worker reads the journal
from `module.Workers`' Deps, which resolves `SalesJournal` as Compose does.

`GET /catch-up` (`accounting:read`) answers each stream's `position`, `lastRunAt`,
`requestedAt`, `lastError` and `behind` — the items after the cursor, read from the
journal with no transaction open and counted up to 500 (`behindMore` past it; null when
the journal cannot be read) — and the count under Needs attention.

### What each fact posts as

Every system voucher is posted through `Post` with the source key `(invoices, kind, id,
'')` — a document's kind and id, or a fact's kind and the row it is about — its
`source_document_id`, and the system rule and version below, which the voucher stores.
This table is the description of system-generated entries bokføringsloven § 6 asks for;
a change to a rule is a new version.

| Rule (version) | Fact | Voucher kind | Debit | Credit |
| --- | --- | --- | --- | --- |
| `sales.invoice` (1) | an issued invoice | `sales_invoice` | `receivables` — the summaries' bases plus VAT, on the invoice's item | each line's net on its [posting rule](#posting-rules)'s account, with the line's tax code as `base`; each VAT summary's VAT on its tax code's VAT account, as `tax` with the summary's base |
| `sales.credit_note` (1) | an issued credit note | `sales_credit_note` | the invoice's lines with every sign turned | `receivables` on **the credited invoice's item** — its opening item when the invoice predates the books |
| `sales.payment` (1) | `payment` | `customer_payment` | `bank`, or `clearing_vipps` for a Vipps payment | `receivables` on the invoice's item |
| `sales.payment_removed` (1) | `payment_removed` | `customer_payment` | `receivables` | `bank` / `clearing_vipps` — dated the removal's business day |
| `sales.charge_payment` (1) | `charge_payment` | `charge` | `bank` | `charge_income`, tax code 7 (`base`), one account, never split (R14) |
| `sales.charge_payment_removed` (1) | `charge_payment_removed` | `charge` | `charge_income`, code 7 | `bank` |
| `sales.write_off` (1) | `write_off`, reason `loss` or `other`, no VAT correction | `write_off` | `bad_debt` — the whole amount | `receivables` |
| `sales.write_off_vat` (1) | `write_off` with the VAT correction | `write_off` | per VAT summary of what is left of the invoice — its summaries less its issued credit notes', per code and rate (R15) — the amount's share by gross: its VAT on the code's VAT account (`tax`) and the rest on `bad_debt` (`base`), both with the code and `tax_specification = tapPåKrav`; a zero-VAT summary's share on `bad_debt` alone | `receivables` |
| `sales.write_off_rounding` (1) | `write_off`, reason `rounding` | `write_off` | `rounding` | `receivables` |
| `sales.write_off_undone` (1) | `write_off_undone` | `write_off` | the write-off's **stored** voucher, read back, line for line with every sign turned, on the accounts, tax codes and items it was posted to — an account inactive since included; the voucher names it as the one it `reverses` | |
| `sales.recovered` (1) | `recovered_payment` | `customer_payment` | `bank` | the write-off's stored debit lines scaled **cumulatively** (below), signs turned: `bad_debt`, and with the correction the VAT given back in proportion, `tapPåKrav` |
| `sales.recovered_removed` (1) | `recovered_payment_removed` | `customer_payment` | the recovery's stored voucher, line for line with every sign turned, naming it as the one it `reverses` | |

A bank payment — a bank file's (`ocr`, `camt054`) or the bank feed's (`bank_feed`) — posts
to `bank` in this delivery; delivery F maps the receiving
account to its ledger account. Amounts are exact, in NOK, rounded to øre half away from
zero. **A difference between the lines' nets and the summaries' bases** goes on the
largest revenue line, so the voucher follows the document's own totals. **A share of a
write-off** is the amount × the remaining summary's gross ÷ the remaining gross, rounded,
the last summary taking what rounding left; its VAT is the share × the summary's VAT ÷ its
gross, rounded. Only the credit notes are taken off before the split: the invoice's
payments are in proportion, so what was paid is taken to have paid each rate alike. A
credit note cannot be issued while a write-off stands (Invoices refuses it,
`invoice_written_off`), so the credit notes the split reads are those before it.
**A recovery's lines** are scaled cumulatively (R15): with *before* what the write-off had
recovered before it (the journal fact's `RecoveredBeforeNOK`) and *W* the amount written
off, each line is round(line × (before + recovered) ÷ W) − round(line × before ÷ W); what
rounding leaves of the recovery goes on a `bad_debt` line with no tax code — the
write-off's own when it has one, else a line of its own — never on a VAT line. Recoveries
that add up to the amount written off therefore give back exactly its lines. The lines
scaled are those of the write-off's stored voucher; a write-off the books never posted —
made before they started — is rebuilt from the journal, and its undoing and a recovery's
removal then post as the rebuilt voucher turned. **Zero is not a posting** (R3): zero lines
are left out and a document of zeros posts no voucher; the cursor moves past it. Each
revenue posting stores the posting rule and the version that placed it
(`posting_rule_id`, `posting_rule_version`, answered with the rule's name in that version
as `postingRuleId`, `postingRuleVersion` and `postingRuleName`), and its text names them
too (`Line 2 · rule Hours v1`).

### The books' start and the opening

- A document **issued before `books_start`** is not posted, and the cursor moves past it:
  it belongs in the opening balances.
- A fact **dated on or after `books_start`** is posted. One dated before it is posted when
  its seq is above the opening's snapshot — the invoice's opening item's
  `opening_as_of_seq` when it has one, else `settings.opening_as_of_seq` — and is then
  booked on `books_start` with its own date as the document date (R5, R14); at or below it,
  or with no opening seq, it is already in the opening amount, or in the old books, and is
  not posted.
- A voucher dated on or before the lock is booked on the first day after it (A8).
- A payment of an invoice with no item yet — one from before the books that the opening
  left out — opens the item for that invoice.

`settings.opening_as_of_seq` — the lowest `AsOfSeq` of the opening's pages — is the
threshold for an invoice that has no opening item, and that is sound: an invoice with no
opening item was not open at the day before `books_start` (or was left out of the
opening). A fact about it dated before `books_start` is then either a payment, which
Invoices refuses on an invoice with nothing open, or a removal, which is dated its own
business day — the day it was made, never before the books when made after the snapshot.
So a backdated fact with a seq above the lowest page's snapshot is one the opening cannot
have counted, and it is posted; one at or below it was in the old books.

### Posting rules

A sales line's revenue account is the first rule in force that matches, in this order
(A14): `line_kind` (the line's snapshot: `hours`, `expense`, `milestone`, `settlement`,
`order_line`, `subscription_period`, `deduction`, `manual`, `mixed`), `tax_category`
(Products' category of the line's product), `tax_code` (the line's standard code), then
`default` for the line's code. No match waits under Needs attention as
`posting_rule_missing`; a match whose account is not an income account — one changed to
another kind since — as `account_kind_mismatch` (R15), checked when the line is posted.

The defaults are seeded with the books, and for books set up before 00183 by the catch-up
worker's first run (idempotently, when no default rule exists) — never by a read — each on
the default chart's account when the chart has it:

| Code | Account | | Code | Account |
| --- | --- | --- | --- | --- |
| 3 | 3000 | | 5 | 3100 |
| 31 | 3010 | | 51 | 3160 |
| 32 | 3000 | | 52 | 3150 |
| 33 | 3020 | | 6 | 3200 |
| 0 | 3100 | | 7 | 3100 |

`GET /posting-rules` (`accounting:read`) lists the rules in force in precedence order and
writes nothing. `POST /posting-rules` (`accounting:close`) takes `name` (1–100), `scope`
(`sales`; `expense` is 400 until delivery C), `match` (`line_kind`, `tax_category` or
`tax_code` — a `default` is never created) with its value (`lineKind` one of the kinds,
`taxCategoryId` above 0, `taxCode` an active code of the books, else 400
`tax_code_unknown`) and an active **income** `accountId` (400 `account_unknown`,
`account_inactive`, `account_kind_mismatch`); 409 `posting_rule_name_taken` and
`posting_rule_match_taken` among the rules in force. `PUT /posting-rules/{id}` changes the
name and the account under its `revision` (409 `revision_conflict`; the account held to the
same rules); **a new name or a new account adds one to `version`**, and every version — its
name, account and the moment it took effect — is kept in `posting_rule_versions`, written
by a trigger when a rule is made and each time its version moves. A posting placed before
keeps naming the version that placed it. `DELETE /posting-rules/{id}` **archives** a rule
(R15) — `archived_at` set; the table refuses a delete — never a default one (409
`posting_rule_default`): it matches nothing from then on, is no longer listed, frees its
name and match for a new rule, and a second archive is a 404. What it placed still names
it.

### Needs attention

What cannot be posted is written to `unposted` in the item's own transaction, and the
cursor still moves (A15). Its `reason` is `tax_code_unknown` (a line's or summary's
standard code not active in the books, or a code with VAT and no VAT account),
`posting_rule_missing`, `account_kind_mismatch` (a matching rule's account is not an
income account), `unknown_kind` (a fact kind this version does not post — never dropped),
`document_unknown` (the written-off invoice not in the journal), `waiting_on` (below), or an
engine refusal's code (`account_inactive`, `account_unknown`, …) with its detail. Each row
carries `invoiceId`, the invoice it is about — a credit note's, the invoice it credits.
The meta's `unposted` and the host's dashboard count those not posted.

**What waits, waits in order** (R15). A document or fact of an invoice that has an open
row under Needs attention, met before it, is not posted ahead of it: it is stored with
reason `waiting_on` and `waitingOn` naming that row (the earliest open one of the
invoice), and the cursor moves on. An item already in the books is never held. So a
payment never posts before the invoice it pays, nor an undo before its write-off.

`GET /unposted` (`accounting:read`, `open` default true, `offset`, `limit` default 100 at
most 500) lists them oldest first. `POST /unposted/{id}/retry` (`accounting:post`) reads
the fact again through the journal with no transaction open — the document by `Document`,
the fact by `Facts` after its seq less one — and posts it as the worker does, in one
transaction: posted, the row records its voucher; refused again, its new reason and one
more attempt; an earlier open row of the same invoice, `waiting_on` again. **A retry that
posts then retries the rows of its invoice recorded after it**, one transaction each, in
the order they were met, until one does not post. A fact the journal no longer answers is
`document_unknown`. 409 `unposted_already_posted` for a row that has posted. There is no
dismissal.

`GET /vouchers?sourceDocumentId=` lists the system vouchers of one Invoices document —
the invoice's own, its credit notes', payments' and write-offs', and the voucher a
document posted as itself, so a credit note's page finds its own voucher, which belongs to
the invoice it credits — which an invoice's page links to, the first 20 with a button for
the rest. The `customers` report's item rows carry the Invoices `documentId` they stand
for.

## Manual journals

`POST /vouchers` (`accounting:post`) takes `voucherDate`, `description` (at most 500),
`entry` (`net`, default, or `gross`) and up to 200 `lines`: `accountId`, a signed
`amount` (debit positive), and optionally `taxCodeId`, `customerId`, `supplierId`,
`openItemId`, `projectId` and `text`.

Each line with a tax code whose direction is not `none` and whose rate on the voucher date
is above zero gets its tax line, on the code's VAT account with the base's sign. The
amounts are exact decimals rounded with docmath's rule, half away from zero:

- **net**: tax = round(amount × rate / 100); the base is the amount;
- **gross**: tax = round(amount × rate / (100 + rate)); the base is the amount less the tax;
- **reverse charge** is entered net, and posts the tax twice: on the deductible account
  (the line's own account when the code has none) and, negated, on the counter account. A
  `gross` journal with a reverse-charge code is refused 400 `reverse_charge_gross`.

The tax is computed and rounded **per line**, never over the voucher's sum: two lines of
0.02 at 25 % make two tax lines of 0.01.

A code without a rate on the date is refused 400 `tax_code_no_rate`; an unknown or
inactive code 400 `tax_code_unknown`. `POST /vouchers/preview` answers the same expansion
— every posting with the line it came from, the debit, the credit, the difference and
whether it balances — and writes nothing; an unbalanced journal is reported there, not
refused.

## Reversal

`POST /vouchers/{id}/reverse` (`accounting:post`) with `date` and `reason` posts a voucher
of the same kind with every posting's sign turned — the same accounts, parties, open items,
tax codes and projects — naming the original (`reverses`, and the original's
`reversedBy`). It refuses 409 `voucher_owned_by_source` for a voucher with a source,
`voucher_already_reversed` for one reversed or itself a reversal, and the opening's after
the first lock with `opening_locked`. **An opening voucher's reversal needs
`accounting:close` too** (403 without it), as entering one does, and takes the settings
row FOR UPDATE as entering does; it is always dated `books_start`. The reversal follows
the lock rule of a person's voucher, and posts on accounts made inactive since.

## Attachments

`POST /vouchers/{id}/attachments` (`accounting:post`, multipart field `file`, at most 20
MB, else 400 `attachment_too_large`) writes the object first, under
`vouchers/<id>/<uuid>`, then its row with the name, the type (the part's, or sniffed),
the size and the SHA-256; the object is removed again if the row is refused. With no
object store it answers 503 `storage_not_configured`. `GET
/vouchers/{id}/attachments/{attachmentId}` streams it as a download, never cached. An
attachment is never removed.

## The lock

`GET /lock` answers `lockedThrough`, `booksStart` and the history, newest first. `PUT
/lock` (`accounting:close`) takes `lockedThrough` (a month's last day, else 400
`lock_not_month_end`; not before `books_start`, else 400 `lock_before_books_start`; not
after the end of the business calendar's current month, else 400 `lock_in_future`; null
lifts it) and `reason`, required when the lock moves back or is lifted (400
`lock_reason_required`). Each change writes a `lock_events` row. Delivery D will hold a
move back to after the last filed VAT term.

## Opening balances

`GET /opening` answers the standing opening voucher (one of kind `opening` that is not
reversed), whether one may be entered (none standing, and the books never locked —
lifting the lock does not open the opening again: `lock_events` keeps every lock), and,
when Invoices' journal is composed, its open invoices at the day before `books_start`
with the customers' names.

The open invoices are read from `SalesJournal.OpenAt(ctx, day, afterNumber, limit)`, page
by page in number order until a page comes back short. **Each page is its own snapshot**,
and its `AsOfSeq` — the highest journal fact it saw — applies to the invoices on that page,
so every invoice keeps its own page's.

`POST /opening` (`accounting:close`) takes up to 2000 `lines`: `accountId`, a signed
`amount`, and for a receivables line the `customerId`, `documentNumber`, `documentDate`,
`dueDate` and `sourceDocumentId` (the invoice in Invoices). It reads the open invoices
again, outside its transaction, and refuses 400 `open_item_unknown` for a
`sourceDocumentId` that is not one of them, is another customer's, has another amount
than its open amount, or is on two lines. It then takes the settings row **FOR UPDATE**,
so two openings entered at once wait for each other and the second sees the first, and
posts one voucher of kind `opening` dated `books_start`, each line with a document
opening its own item, and the difference on the `opening` account. It refuses 409
`opening_exists` while one stands and `opening_locked` once the books have been locked.

What it stores for delivery B (design R14), with Invoices composed:

- on each item made for an invoice, `open_items.opening_as_of_seq`: the `AsOfSeq` of the
  page that invoice was read on;
- in `settings.opening_as_of_seq`: the **lowest** `AsOfSeq` of every page read (the one
  page's when there is one).

Without Invoices both stay null. The rule delivery B posts by: a journal fact about an
invoice that has an opening item is posted when its date is on or after `books_start`
**or** its seq is after that item's `opening_as_of_seq`; a fact about any other invoice is
held to `settings.opening_as_of_seq` the same way. A fact at or below the seq, dated
before `books_start`, is already in the opening amount and is not posted again.

## Open items and settlements

`GET /open-items` lists a page of items with their balance (the sum of their postings),
by party and due date, filtered by `kind`, `customerId`, `supplierId` and `open` (default
true: a balance other than zero), with `offset` and `limit` (default 200, at most 1000)
and the `total` that match. An item is settled when its balance is zero.

`POST /open-items/{id}/settlements` (`accounting:post`) takes `date`, a positive `amount`,
an optional `bankAccountId` (an active asset account, else 400 `bank_account_invalid`;
default the `bank` role's) and `text`. It posts, for a customer item, a
`customer_payment` voucher that moves the item toward zero against the bank — a receipt for
a balance owed, a refund for a credit — and for a supplier item a `supplier_payment`. It
refuses 409 `settlement_source_document` for an item that stands for an Invoices document
(its payment belongs there), `settlement_settled` for one with nothing left and
`settlement_exceeds_balance` for more than its balance.

## The reports

`GET /reports/{report}` (`accounting:read`) and `GET /reports/{report}/csv`
(`accounting:export`) take `from` and `to` (required; `from` after `to` is 400
`period_invalid`), `compareFrom` and `compareTo` (both or neither), `accountId`,
`customerId`, `supplierId` and `projectId`. Every report is computed from the postings by
voucher date in one read-only snapshot and answered as one table — columns with a key, a
label in the books' language and a type, rows with a style (`line`, `heading`, `subtotal`,
`total`), a depth, cells by key and the voucher or account a row links to — with
`producedAt`.

| Report | Rows |
| --- | --- |
| `journal` | Every posting of the vouchers in the period, in number order, and the total; at most 366 days and 50 000 postings. |
| `account` | The account's opening balance, its postings with the running balance, the closing balance; `accountId` is required (400 `report_account_required`); at most 50 000 postings. |
| `customers`, `suppliers` | Per party a heading, its items with a balance at `to` or a movement in the period, the balance aged by due date at `to` (not due, 1–30, 31–60, 61–90, over 90 days), the party's subtotal and the total. |
| `trial_balance` | Per account the opening balance, the debits, the credits and the closing balance, and with a comparison the closing balance at `compareTo`. |
| `profit_and_loss` | The period's movement on income and expense accounts, income positive and costs negative, grouped by class with the operating result, the result before tax and the result; an income or expense account in no class group is under **Other income and costs**, before the result. |
| `balance_sheet` | The balances at `to`, each account on the side of its kind: the asset accounts, then equity with two computed lines — **Earlier years' result, not yet closed** (every income and expense posting up to the day before the fiscal year of `to` begins) and **Result for the year, not yet closed** (the rest up to `to`) — and the liability accounts, shown positive. An asset or a liability account in no class group is under **Other assets** or **Other liabilities**, so the sheet always balances. |

An income or expense account's balances count from the start of the fiscal year of `from`
— the year is not closed in this phase — and the trial balance shows the earlier years'
unclosed result as its own line, so it still sums to zero. The `year_result` role is the
year-end closing's, in phase 3.

The journal and an account's specification are never cut short: past 50 000 postings
they are refused 400 `report_too_large`, and a shorter period answers.

The CSV is the report with its title, period and production time, then the header and the
rows: a byte order mark, semicolons, CRLF, the decimal comma, RFC 4180 quoting and the
formula guard on text, served as `<report>-<from>-<to>.csv`, never cached.

## Accounts and tax codes

- `POST /accounts` (`accounting:close`) takes `number`, `name`, `kind`, an optional
  `defaultTaxCodeId` and `grouping`; 409 `account_number_taken`. The kind is the
  number's class's — 1 asset, 20 equity, 21–29 liability, 3 income, 4–7 expense, 8
  income or expense — and a number begins with 1 to 8; else 400
  `account_kind_mismatch`. `PUT` holds a changed number or kind to the same rule.
- `PUT /accounts/{id}` changes all five fields under its `revision` (409
  `revision_conflict`); a changed number or kind of an account with postings is 409
  `account_has_postings`, and making an account holding a role inactive 409
  `account_has_role`.
- `DELETE /accounts/{id}` deletes an account with no postings and no role (409
  `account_has_postings`, `account_has_role`).
- `PUT /accounts/{id}/groupings` replaces its groupings: one per year, each a category and
  code of that year's list. `GET /grouping-codes?year=&q=` is the list in force for the
  year.
- `PUT /roles/{role}` moves a role to an active account holding no other role, of the
  role's kind (400 `account_kind_mismatch`). `receivables` and `payables` never leave an
  account with postings (409 `account_has_postings`): the party ledgers are the postings
  on it.
- `PUT /tax-codes/{id}` changes a code's name, VAT account, counter account (reverse
  charge only) and activity under its revision. `POST /tax-codes/{id}/rates` starts a rate
  period (0–99.99 %) after the open one's start (400 `rate_period_invalid`), which then ends
  the day before.

## Status codes

Every refusal is an RFC 9457 problem (`AccountingProblem`) with its `code`: 400 for the
request's fields (`errors` keyed by the camelCase path, such as `lines[2].accountId`), 404
bare for an unknown id, 409 for the state of the books, and 503 for a missing object
store. The contract's `AccountingProblem` description names every code; the module's test
holds it to the list in `errors.go`.

## Endpoints

Every operation, with its request, its responses and the permission it is checked against, is in the generated API reference (`/en/reference/api/accounting/`), built from the contract.
