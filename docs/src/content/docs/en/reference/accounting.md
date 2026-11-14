---
title: "Accounting module"
description: "The company's books: SAF-T Financial — the export by fiscal year and periods, its worker, lease and streaming, the split by period, the element mapping, the journals, the TaxInformation mapping, the file name and the checks; the import as the other way to set up the books — the stream reader and what it refuses, the preview, the chart and its fixes, the roles, the tax codes, the suppliers, the customers matched in Customers, the opening voucher with the VAT owed on the settlement account, and the refusals; VAT — the specification by tax code with its parts, drill-down and reconciliation against the VAT accounts, the terms derived from the settings with their due dates, close with its refusals, every closing kept, the settlement voucher and the lock, reopen, filing and unfiling, the lock held behind no closed term, the VAT accounts kept out of the opening and fixed on a used tax code, the return laid out as Skatteetaten's with each code's lines, the øre cut, kreditnota and tapPåKrav, and the skattemeldingformerverdiavgift XML; purchases — suppliers with the Brønnøysund lookup and a person's retention, supplier invoices from draft to voucher with every tax code's direction, foreign currency, credit notes, Reverse and copy, payments with the currency difference — and expenses that post themselves from Expenses' journal with the VAT inference, the amount in NOK and the supplier_invoice kind; sales that post themselves — the catch-up worker, its event handlers, streams, cursors and leases, what each document and fact posts as with every system rule and its version, the books' start and the opening's seqs, the posting rules, Needs attention and retry; the module and the module it requires; the four permissions; Set up the books and the gate before it; the settings; the default chart with Skatteetaten's grouping codes, the system roles and the standard tax codes with their rates from 2022; accounts and their rules; groupings per year; tax codes and rate periods; vouchers and postings and the rules the database keeps — balance, a voucher has postings, immutability, postings only on the voucher just inserted, the books' start and the lock read under a share lock; the posting engine — numbering, dating, the lock rule, zero lines, open items and idempotence on the source key; manual journals and their VAT lines; reversal; attachments; the lock and its history; the opening balances and Invoices' open invoices; open items and settlements; the reports and their CSV; the refusals and the endpoints."
sources:
  - apps/server/internal/accounting
  - openapi/accounting.yaml
  - apps/server/internal/openapi/specs/accounting.yaml
  - apps/server/internal/db/migrations/00181_accounting_baseline.sql
  - apps/server/internal/db/migrations/00183_accounting_sales.sql
  - apps/server/internal/db/migrations/00185_accounting_purchases.sql
  - apps/server/internal/db/migrations/00186_accounting_vat.sql
  - apps/server/internal/db/migrations/00187_accounting_saft.sql
---

Accounting keeps the company's books as bokføringsloven asks for them. Its package is
[`apps/server/internal/accounting`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/accounting),
its schema `accounting` (migration `00181_accounting_baseline.sql`), its contract
[`openapi/accounting.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/accounting.yaml),
mounted at `/api/v1/accounting`. The design is
`docs/superpowers/specs/2026-11-14-accounting-design.md`; this page is what deliveries A
(the ledger), B (sales post themselves, migration `00183_accounting_sales.sql`), C
(purchases and expenses, migration `00185_accounting_purchases.sql`), D (VAT, migration
`00186_accounting_vat.sql`) and E (SAF-T, migration `00187_accounting_saft.sql`) built. The user guide is [Accounting](/en/user/accounting/).

## The module

- `MODULES`: **`accounting` requires `invoices`** (which requires `customers`); without it
  the server refuses to start, naming it. It reads `contracts.SalesJournal` (Invoices'
  open invoices for the opening balances), `contracts.CustomerDirectory` (customers'
  names), when Expenses is on `contracts.ExpenseJournal` ([Expenses post
  themselves](#expenses-post-themselves)), and when Projects is on
  `contracts.ProjectDirectory` (projects' names). Every call out of the module is made
  with no transaction of its own open. It works with Expenses off: the expenses stream is
  then not read, and the meta's `expensesEnabled` is false.
- It calls Brønnøysundregisteret through the platform package `internal/brreg` for the
  supplier lookup (`BRREG_BASE_URL`, `BRREG_TIMEOUT`, as Customers does), read-only and
  rate-limited.
- It reads the seller's organisation number and VAT registration through
  `contracts.SalesTax.Seller()` for the VAT return and the meta, nil-safe: without Invoices'
  seller the return goes without the number.
- For SAF-T it reads Customers' `BillingProfile` (a customer's number, name, organisation
  number and invoice address) and `CustomersByLegalID` (the import's matching), the
  projects' names, and the names of who asked for an export
  ([SAF-T Financial](#saft-financial)).
- It publishes no event. It starts two workers, `accounting.catchup` and `accounting.saft`, and declares four
  event handlers ([Sales post themselves](#sales-post-themselves)); before the books are
  set up both return at once, and the handlers do nothing with the module off in
  `MODULES`.
- Attachments, SAF-T exports (`saft-exports/<id>/<uuid>.xml`) and uploaded SAF-T files
  (`saft-imports/<uuid>.xml`) live in the object store under the scope `accounting`.

## Permissions

| Key | Display | Delegable | Sensitive | Asked by |
| --- | --- | --- | --- | --- |
| `accounting:read` | Use Accounting | yes | no | every operation |
| `accounting:post` | Post vouchers | yes | no | manual journals, their preview, reversal, attachments, settlements, suppliers and the Brønnøysund lookup, supplier invoices, their files, Post, Reverse and copy and payments, a retry and the actions under Needs attention, marking a VAT term as filed |
| `accounting:close` | Manage the books | **no** | no | setup, a SAF-T import (upload, preview, list, apply), the chart, the roles, the tax codes, the posting rules, the opening balances, the lock, closing and reopening a VAT term and removing its filing mark |
| `accounting:export` | Export the books | yes | **yes** | the reports' CSV, a supplier's export, the SAF-T exports (check, ask for, list, read, download) and an imported SAF-T file's download |

A write asks for `accounting:read` and its own key (`permission:accounting:post+accounting:read`
and so on). `accounting:close` is in the agents' never list: no virtual employee may hold
it. The Assistant never uploads or downloads an attachment or a CSV
(`x-vantigo-assistant: never`); the preview is callable without asking (`auto`). Every
other write — a manual journal, a reversal, a settlement, the opening balances and the
lock — is called only after the person confirmed it (`confirm`, S4's default for a write;
`internal/integration`'s contract test pins those five), and the opening and the lock
need the person's own `accounting:close`. The books set up from a SAF-T file are confirmed
the same way. A SAF-T export asked for is a confirmed write by the contract, and since it
names the sensitive `accounting:export` the default gate closes it, as it closes every
sensitive operation; the SAF-T files — an export's, an upload, an imported file's download
— are never the Assistant's.

## Set up the books

Every known module is on by default, so an upgrade switches Accounting on. **Before the
settings row exists, every operation but `GET /meta`, `POST /setup` and the SAF-T import's
(`GET`/`POST /saft-imports`, `GET /saft-imports/{id}`, `POST /saft-imports/{id}/apply`,
`GET /saft-imports/{id}/file`) answers 409 `books_not_set_up`**, from a strict middleware that runs before any handler.

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

  A reverse-charge code posts three lines beside its counter-party: the base (`tax_role`
  `base`), the deducted side on the VAT account or the line's own account (`tax`), and the
  calculated side on the counter account 2705 (`tax_reverse`), the last two with the same
  `tax_base`. A VAT return counts the calculated VAT from `tax_reverse` and the deduction
  from `tax`, so the base is never counted twice. Non-deductible VAT is a `tax` line on a
  cost account (the line's own), not a VAT account.

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
| `postings` | `voucher_id`, `line_no`, `account_id`, `amount` (signed numeric(14,2): debit positive, never zero), `currency`/`currency_amount`/`exchange_rate` (a purchase in another currency's, all three or none — `ck_postings_currency`), `tax_code_id`/`tax_role`/`tax_base`, `customer_id` (Customers' integer id) or `supplier_id` (a `suppliers` row: `fk_postings_supplier`, `NOT VALID` so a journal's supplier id from before the table stays), `open_item_id`, `project_id` (Projects' integer id), `description`. |
| `attachments` | `voucher_id`, `object_key`, `file_name`, `content_type`, `size_bytes`, `sha256`, `uploaded_at`, `uploaded_by_user_id`. |
| `open_items` | `kind` (`customer`, `supplier`), the party, `unallocated`, `document_number`, `document_date`, `due_date`, `original_amount`, `source_document_id`, `opening_as_of_seq` (for an item the opening made for an invoice in Invoices, the `AsOfSeq` of the page it was read on; null otherwise). One unallocated item per party and kind; one item per source document and kind. |
| `lock_events` | Every move of the lock: from, to, reason, when, who. |
| `posting_rules` | `name` (unique among the rules in force), `scope` (`sales`, `expense`), `match` (`line_kind`, `tax_category`, `expense_category`, `tax_code`, `default`) with exactly the one value its match names, `account_id`, `vat_deductible` (an expense rule's, null on a sales rule), `version`, `revision`, `archived_at` (set when it is archived; a delete is refused). One rule in force per scope and match value. |
| `posting_rule_versions` | Every version of a rule: `rule_id`, `version`, `name`, `account_id`, `vat_deductible`, `valid_from`, written by a trigger when a rule is made and each time its version moves. |
| `cursors` | One row per stream (`invoices.documents`, `invoices.facts`, `expenses.facts`): `position` (the last document number or fact seq read), `lease_id`/`lease_until`, `last_run_at`, `last_error`. |
| `catchup_requests` | A wake-up per stream, `requested_at`: what a handler writes and a claim deletes. |
| `unposted` | A source fact that could not be posted: its stream and position, its source key (unique), date, the Invoices document, `invoice_id` (the invoice it is about — a credit note's, the one it credits), customer and amount it is about, `reason`, `detail`, `attempts`, `waiting_on` (with reason `waiting_on`, the row it waits behind), and once a retry posted it `posted_voucher_id` and `posted_at`; for an expense fact `order_key` (`entry:<id>` or `claim:<id>`, what it waits behind) and `nok_amounts` (its lines in another currency, with the NOK a person entered, `enteredBy` and `enteredAt`); `informational` (a row that posted and says something, `vat_not_deducted` or `expense_invoice_unapproved`); `resolved_at` (acknowledged, made into a supplier invoice — `supplier_invoice_id`, repointed to the copy by Reverse and copy — undone before it posted, or its voucher turned back). |
| `suppliers` | `number` (gap-free, from the counter row `supplier`), `name`, `organisation_number` (unique per `country` when set, `ux_suppliers_organisation_number`), `country`, `address_line`, `postal_code`, `city`, `email`, `bank_account` (eleven digits), `iban`, `bic`, `payment_terms_days` (0–365, default 14), `default_account_id`, `default_tax_code_id`, `currency`, `is_person`, `active`, `anonymised_at`, `revision`. Never deleted once a posting, an item or an invoice names it. |
| `supplier_invoices` | `supplier_id`, `kind` (`invoice`, `credit_note`), `supplier_number` (the supplier's own), `invoice_date`, `due_date`, `currency`, `exchange_rate` (NOK per unit, 1 for NOK; null on a draft in another currency whose rate is not set, never on a posted one), `total` (in the currency), `total_nok` (null exactly when the rate is), `kid`, `reference`, `description`, `state` (`draft`, `posted`, `reversed`; paid is derived), `credits_invoice_id`, `voucher_id`, `open_item_id`, `reversal_voucher_id`, `copied_from_id`, `source_expense_entry_id`, `revision`, who and when. Unique: the supplier, kind and number (case and spaces aside) among those not reversed (`ux_supplier_invoices_number`); one not reversed per expense (`ux_supplier_invoices_expense`). |
| `supplier_invoice_lines` | `supplier_invoice_id`, `position`, `account_id`, `tax_code_id`, `amount` (as entered, in the currency), `entry` (`net`, `gross`), `project_id`, `text`. |
| `supplier_invoice_files` | A draft's files: the object key, name, type, size and SHA-256. At posting each becomes an `attachments` row of the voucher, the same object. |
| `saft_exports` | Every SAF-T export asked for: `fiscal_year`, `from_period`/`to_period` with `period_start`/`period_end`, `state` (`queued`, `running`, `done`, `failed` with `error`), `attempts`, `lease_id`/`lease_until`, `unmapped_accounts` (accounts with no grouping code when asked for), `total_bytes`, `requested_at`, `requested_by_user_id`, `started_at`, `finished_at`. Never deleted (`refuse_change()` before delete): the record of what was handed over. |
| `saft_export_files` | One row per file an export wrote: `part`/`parts`, `file_name`, `object_key`, `size_bytes`, `sha256`, its periods and days. Never changed. |
| `saft_imports` | A SAF-T file uploaded to set up the books: `file_name`, `object_key`, `size_bytes`, `sha256`, `audit_file_version`, `selection_start`/`selection_end`, `preview` (the master data read from it, jsonb), who uploaded it and when, and once applied `applied_at`, `applied_by_user_id`, `opening_voucher_id`. At most one is applied (`ux_saft_imports_applied`). |
| `supplier_payments` | `supplier_invoice_id`, `paid_on`, `amount_nok`, `amount_currency`, `bank_account_id`, `voucher_id`, and once removed `removed_at`, `removed_by_user_id`, `removal_voucher_id`. |

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
| A posting's currency, currency amount and rate are set together or not at all | `ck_postings_currency` |
| A supplier with postings or open items is never deleted | `fk_postings_supplier`, `fk_open_items_supplier` (`ON DELETE RESTRICT`, `NOT VALID`) |
| A supplier's number of a kind is entered once among the invoices not reversed | `ux_supplier_invoices_number` |

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
system role, a signed exact amount, an optional tax code with its role (`base`, `tax`, or
`tax_reverse` for a reverse charge's calculated side — the CHECK `ck_postings_tax_role`;
`tax_base` is carried by `tax` and `tax_reverse` lines),
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
| `accounting.catchup.expenses` | `expenses.entry.*` and `expenses.claim.*` v1 (approved, unapproved, reimbursed, reimbursement_undone) | a wake-up for `expenses.facts` |

Each upserts its stream's `catchup_requests` row on the delivery's transaction and does
nothing else (module-boundaries rule 23): it reads no contract and locks nothing but that
row. Charge payments and write-offs publish no event; the sweep finds them.

Worker **`accounting.catchup`** runs every 15 seconds. For each stream whose journal is
composed — Invoices' two, then Expenses' — documents first so an invoice's item exists
before its payment posts:

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
before the books are set up; a stream whose journal is not composed is not read. The worker
reads the journals from `module.Workers`' Deps, which resolves `SalesJournal` and
`ExpenseJournal` as Compose does.

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
(`sales` or `expense`), `match` — for sales `line_kind`, `tax_category` or `tax_code` (a
`default` is never created), for expense `expense_category` — with its value (`lineKind`
one of the kinds, `taxCategoryId` above 0, `taxCode` an active code of the books, else 400
`tax_code_unknown`, `expenseCategoryId` above 0: Expenses' category, an opaque id), for an
expense rule `vatDeductible` (default true), and an active `accountId` of the scope's kind
— **income** for sales, **expense** for expense (400 `account_unknown`,
`account_inactive`, `account_kind_mismatch`); 409 `posting_rule_name_taken` and
`posting_rule_match_taken` among the rules in force. `PUT /posting-rules/{id}` changes the
name and the account — and an expense rule's `vatDeductible` — under its `revision` (409
`revision_conflict`; the account held to the same rules); **a new name, account or
deductibility adds one to `version`**, and every version — its
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
engine refusal's code (`account_inactive`, `account_unknown`, …) with its detail; for an
expense fact also `amount_in_nok_needed`, `enter_as_supplier_invoice`,
`expense_invoice_unapproved` and `vat_not_deducted` ([Expenses post themselves](#expenses-post-themselves)). Each sales row
carries `invoiceId`, the invoice it is about — a credit note's, the invoice it credits; an
expense row `orderKey`. The meta's `unposted` and the host's dashboard count the rows that
wait — not posted and not resolved; an **informational** row (`informational`, posted, its
voucher named) is counted apart in the meta's `informational` and never blocks.

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
`document_unknown`. 409 `unposted_already_posted` for a row that has posted (an
informational one included), `unposted_wrong_reason` for one resolved. A row that waits is
never dismissed; an informational one is acknowledged (below). An expense row's retry reads
the fact through `ExpenseJournal.Facts` and posts its dependants by `orderKey`.

`GET /unposted` with `open` lists the rows that wait and the informational ones not yet
acknowledged. Three actions answer expense rows (`accounting:post`):

- `POST /unposted/{id}/nok-amounts` takes `amounts` — per `entryId` of the row's
  `expenseLines` (its lines in another currency) an `amountNok` above zero — keeps them on
  the row with `enteredBy` (the caller) and `enteredAt`, and retries it as above. The
  voucher posted with them names the person who entered the latest as its poster, and each
  such line's text names the figure and the original: `NOK 921.50 entered for EUR 80.00`. 400 for an entry not on the row or an amount that is
  not one (`amount_invalid`); 409 `unposted_wrong_reason` for another reason.
- `POST /unposted/{id}/supplier-invoice` (body optional: `supplierIsPerson`) reads the
  fact again, takes its line of the `supplier_invoice` kind and, in one transaction, makes
  a draft supplier invoice — the active supplier of the line's supplier name, case aside
  (the lowest number), or a new one, `is_person` as `supplierIsPerson` says (default
  false); kind invoice; the expense's number or `EXP-<entry id>`; its date; its due date
  or 14 days on; its currency (another currency is drafted with no rate, which Post
  refuses with `exchange_rate_required` until one is given); one gross line of its amount on the account its category's rule
  names (7700 without one), with the input code its VAT matches when the rule allows
  deduction, and its project — and resolves the row naming the draft. Then, with no
  transaction open, it copies the expense's receipts through `ExpenseJournal.Attachments`
  and `OpenAttachment` into the draft's files; one that cannot be copied is logged and left
  for the person to attach. 201 with the draft; 409 `expense_already_invoiced` when a
  supplier invoice not reversed exists for the expense, or the row was resolved by another
  request first (the resolve must touch exactly the row); `duplicate_supplier_invoice` for
  the number; `unposted_wrong_reason`. Reverse and copy repoints the row to the copy;
  deleting a draft that carries `source_expense_entry_id` — the draft or its copy —
  reopens the entry's approval row, matched by the entry.
- `POST /unposted/{id}/acknowledge` resolves an informational row; 409
  `unposted_wrong_reason` for any other.

`GET /vouchers?sourceDocumentId=` lists the system vouchers of one Invoices document —
the invoice's own, its credit notes', payments' and write-offs', and the voucher a
document posted as itself, so a credit note's page finds its own voucher, which belongs to
the invoice it credits — which an invoice's page links to, the first 20 with a button for
the rest. The `customers` report's item rows carry the Invoices `documentId` they stand
for.

## Purchases

Delivery C (design A17–A19, R5, R8). Every write here needs `accounting:post`.

### Suppliers

`GET /suppliers` (`accounting:read`; `q` — a name's part, the number or the organisation
number —, `active`, `offset`, `limit` default 100 at most 500) lists them by number, each
with `hasPostings`, its `balance` on `payables`, `lastVoucherDate` and, for a person,
`retainedUntil`: the last day of the fiscal year of its last voucher, five years on
(bokføringsloven § 13). `POST /suppliers` takes the next number from the counter row
`supplier` — held to the commit, so a refused one gives its number back — and `name` (1–200),
`organisationNumber` (for a Norwegian supplier nine digits passing mod 11; spaces and dots
are taken out), `country` (two letters, default `NO`), the address, `email`,
`bankAccount` (eleven digits passing mod 11, else 400 `supplier_bank_account_invalid`),
`iban`, `bic`, `paymentTermsDays` (0–365, default 14), `currency` (default NOK),
`defaultAccountId` (active, else `account_unknown`/`account_inactive`),
`defaultTaxCodeId` (active, else `tax_code_unknown`; not an output code, else
`tax_code_not_purchase`) and `isPerson`; 409 `supplier_organisation_number_taken` for the
same organisation number in the same country. `GET /suppliers?q=` matches `%` and `_` as
themselves. `PUT /suppliers/{id}` replaces them, with `active`, under `revision` (400
without it, 409 `revision_conflict`; `supplier_anonymised` for an anonymised one). `DELETE /suppliers/{id}` deletes one no posting, open item or invoice
names; any other is 409 `supplier_has_postings` — the postings' and items' foreign keys
refuse it too.

`GET /suppliers/lookup/brreg` (`accounting:post`, rate-limited to 600 an hour per client
address) takes `organisationNumber` (nine digits: the entity itself, with its business
address — its postal one without — and its VAT registration; a struck-off one is answered
`deleted`, an unknown number nothing) or `q` (two characters at least: a name search of at
most ten, names and numbers only). It writes nothing. An unreachable or unreadable registry
is 502 `brreg_unavailable`.

**A supplier who is a person.** Customers' personal-data slot
(`contracts.CustomerPersonalData`) is keyed by a customer of Customers and cannot name this
module's supplier, so Accounting answers a person's access request itself: `GET
/suppliers/{id}/export` (`accounting:export`, never called by the Assistant) answers the
supplier, its invoices with their lines, files and payments, and every posting naming it,
with `producedAt`, and logs that it was asked. The record is bookkeeping material, kept
until `retainedUntil`. After that day `POST /suppliers/{id}/anonymise` (`accounting:post`,
confirm for the Assistant) sets the name to `Anonymised supplier <number>`, clears
`organisation_number`, the address, `email`, `bank_account`, `iban` and `bic`, makes it
inactive and sets `anonymisedAt`; postings, items and vouchers keep its id. 409
`supplier_retained` on or before `retainedUntil`, `supplier_anonymised` a second time; 400
for a supplier that is not a person. A person with no voucher has no retention date.

### Supplier invoices

`GET /supplier-invoices` (`accounting:read`) filters on `state` (`draft`, `posted` — not
settled —, `paid` — posted, its item at zero —, `reversed`), `supplierId`,
`supplierNumber` (with the supplier: the duplicate warning's read), `dueFrom`, `dueTo` and
`overdue` (posted, not settled, due before today), newest invoice date first, `offset` and
`limit` (default 100, at most 500). Each invoice is answered with its lines computed at its
invoice date — `net`, `vat` (the invoice's VAT in its currency; 0.00 on a reverse-charge
code, whose VAT the buyer calculates) and `gross` —, `linesTotal`, `vat`, its files,
payments, voucher and reversal, and once posted `balance` and `balanceCurrency`, its item's
in NOK and in its currency (negative while owed), and `overdue`.

**A draft** — `POST /supplier-invoices`, `PUT /supplier-invoices/{id}` under `revision` —
takes `supplierId` (an active supplier for a new one, else 400 `supplier_inactive`),
`kind`, `invoiceNumber` (1–60), `invoiceDate`, `dueDate` (on or after it; default the
supplier's terms after the invoice date), `currency` (default the supplier's), `total`
(zero or more), `kid`, `reference`, `description`, for a credit note `creditsInvoiceId` (a
posted invoice of the same supplier; held again at Post, below), and up to 200 `lines`: `accountId` (active, not the
receivables or payables account), `taxCodeId`, `amount` (two decimals, negative for a
discount), `entry` (`gross` default, or `net`), `projectId` and `text`. In another currency
it takes `exchangeRate` (NOK per unit, kept to six decimals; the NOK total is the total at
it, rounded) or `totalNok` (the rate is then the NOK total over the total), else 400
`exchange_rate_required`; given both, |round(total × rate) − totalNok| must be at most 0.01
× max(1, lines), else 400 `exchange_rate_mismatch`. Each line's code is held to the purchase rules below at the
invoice date. The lines need not make the total yet. 409 `duplicate_supplier_invoice` for
the same supplier, kind and number on another invoice not reversed; a change of a posted
or reversed one is 409 `supplier_invoice_not_draft`. `DELETE /supplier-invoices/{id}`
deletes a draft with its lines and files; a file's object is deleted from the store once
no file and no attachment names it.

**Files.** `POST /supplier-invoices/{id}/files` (multipart `file`, at most 20 MB, 400
`attachment_too_large`; rate-limited to 600 an hour per client address) keeps a file on a
draft — the object first, then the row under the draft's lock; refused 409
`supplier_invoice_not_draft` otherwise, the object removed again. `GET
.../files/{fileId}` streams it as a download; `DELETE .../files/{fileId}` removes it from a
draft. Without an object store, 503 `storage_not_configured`.

**Post** — `POST /supplier-invoices/{id}/post` — takes the draft FOR UPDATE and refuses it
without a file (409 `supplier_invoice_file_required`), with no rate set
(`exchange_rate_required` — a rate of exactly 1 is a rate), without lines (`voucher_empty`) or when the lines' gross does
not make the total (400 `supplier_invoice_total_mismatch`). It posts through the engine
with the source key `(accounting, supplier_invoice, id, '')` and the person as poster; a
date before `books_start` (400 `date_before_books_start`) or on or before the lock (409
`period_locked`) is refused rather than moved, as for any person's voucher. A credit note
naming an invoice takes that invoice FOR UPDATE, then its open item — the order a payment
takes them, so a credit note and a payment on the same item wait for each other — and
refuses it unless the invoice is still `posted` (409 `credit_note_invoice_not_posted`), in
the credit note's currency (`credit_note_currency_mismatch`), and has at least the credit
note's NOK total left on its item, and in another currency its currency total
(`credit_note_exceeds_balance`). The draft's
files become the voucher's attachments, the same objects; the invoice records its voucher
and the open item its payables line opened, and is `posted`. The voucher is a system
voucher: the generic reversal refuses it (`voucher_owned_by_source`).

| Rule (version) | Voucher kind | Lines |
| --- | --- | --- |
| `purchase.supplier_invoice` (1) | `supplier_invoice` | `payables` credited with the NOK total, on a new item (the supplier's number, the invoice and due dates; the supplier); per line its net debited on its account with its code as `base` and its project, and its VAT as the code says (below); in another currency each posting is its currency amount × the rate, rounded half away from zero, with `currency`, `currency_amount` and `exchange_rate` kept, and what rounding leaves against the NOK total goes on the largest base line |
| `purchase.supplier_credit_note` (1) | `supplier_credit_note` | the same with every sign turned; `payables` on the credited invoice's item when it names one, else on its own |
| `purchase.supplier_invoice_reversed` (1) | as the original | the original's stored voucher, line for line with every sign turned (source part `reversal`) |

A line's VAT, computed per line with docmath's rounding — net: round(amount × rate ÷ 100);
gross: round(amount × rate ÷ (100 + rate)) out of it — follows the code's direction and
flags as seeded:

| Codes | Direction, flags | Postings beside the base |
| --- | --- | --- |
| 1, 11, 12, 13 | input, a VAT account | the VAT debited on the code's VAT account (`tax`, with the base); in currency it carries its currency amount |
| an input code without a VAT account | input, not deductible | the VAT debited on the line's own account (`tax`) |
| 86, 88, 91 | input, reverse charge, a VAT account | entered net (gross is 400 `reverse_charge_gross`); VAT = round(NOK base × rate ÷ 100), debited on the VAT account (`tax`) and credited on the counter account 2705 (`tax_reverse`), both with the base; no currency amount; the payable is the net |
| 87, 89, 92 | input, reverse charge, no VAT account | the same, the `tax` debit on the line's own account: the VAT is a cost |
| 0 | none | none: the base alone, with its code |
| 14, 15, 20, 21, 22, 81–85 | import | refused, 400 `tax_code_not_for_supplier_invoice` |
| 3, 31, 32, 33, 5, 51, 52, 6, 7 | output | refused, 400 `tax_code_not_purchase` |

The NOK amounts are computed before the lines are built: each base and each VAT in NOK,
the rounding difference against the NOK total put on the largest base, and only then the
VAT lines, so a VAT line's `tax_base` is the base as posted.

Import of goods is not a supplier invoice's VAT (R8): the supplier's invoice is coded 0 or
carries no code, and the import VAT — the customs' or a forwarder's included — is a manual
journal from the customs declaration with 81–84 on a base line and its counter line, or 14
and 15 (the user guide shows the entry).

**Reverse and copy** — `POST /supplier-invoices/{id}/reverse-and-copy` with `date` and
`reason` (1–500) — takes a posted invoice FOR UPDATE (409 `supplier_invoice_not_posted`
otherwise; `supplier_invoice_has_payments` while a payment stands), then its open item, and
refuses it with `supplier_invoice_item_in_use` while a voucher other than its own (for a
credit note, other than its own and its invoice's) that is neither reversed nor a reversal
posts on that item — a credit note, a payment made through a credit note, a settlement.
It posts the reversal
above dated `date` (held to the books' start and the lock as Post is), marks it
`reversed`, and makes a draft with its supplier, kind, number, dates, currency, rate,
totals, lines and files (the same objects), `copiedFromId` naming it, which it answers
(201). The duplicate guard lets the copy through: the original is reversed. A row under
Needs attention that named the original names the copy.

### Payments

`POST /supplier-invoices/{id}/payments` takes `date`, `amountNok` (above zero, as the bank
took it), `bankAccountId` (an active asset account, else 400 `bank_account_invalid`;
default the `bank` role's) and, for another currency, `amountCurrency` (the share settled,
default all that is left). It takes the invoice and its item FOR UPDATE; 409
`supplier_invoice_not_posted`, `supplier_invoice_settled` (nothing left), and
`payment_exceeds_balance` for more than is left — in NOK for a NOK invoice, in the currency
otherwise. The share settled in NOK is the share × the invoice's rate, rounded; the last
share settles exactly what is left in NOK, so the item ends at zero in both.

| Rule (version) | Voucher kind | Lines |
| --- | --- | --- |
| `purchase.payment` (1) | `supplier_payment` | owed: `payables` debited with the share in NOK on the item (with its currency amount and the invoice's rate), the bank credited with what it took; a credit (the item above zero): the other way; the difference on `currency_loss` (debit: more NOK went) or `currency_gain` (credit) |
| `purchase.payment_removed` (1) | `supplier_payment` | the payment's stored voucher turned back, line for line, dated the payment's day |

The source key is `(accounting, supplier_payment, invoice id, <uuid>)`, and a removal's
`(accounting, supplier_payment_removed, payment id, '')`. Both are a person's: a date before
the books' start or in a locked period is refused. `DELETE
/supplier-invoices/{id}/payments/{paymentId}` posts the removal and keeps the row with
`removedAt` and its voucher; a second removal is 409 `payment_removed`. Each payment
answers its `currencyDifference` (positive a loss). A supplier invoice's item is refused by
the open-item settlement (`settlement_supplier_invoice`): its payments are recorded here.

The meta's `payablesOverdue` and `payablesDueSoon` count the posted supplier invoices not
settled that are due before today, and due today or in the next seven days; the host's
dashboard shows them when nothing waits under Needs attention.

## Expenses post themselves

Expenses' [`contracts.ExpenseJournal`](/en/reference/expenses/#what-expenses-reports-to-other-modules)
is the catch-up's third stream, `expenses.facts`, read by seq (design A20, R2). Each fact
posts in its own transaction with the source key `(expenses, <subject>.<occurrence>,
<entry or claim id>, <seq>)`: approve, unapprove, approve is three vouchers. A fact dated
before `books_start` is skipped and the cursor moves past it. A fact of an entry or claim
with an earlier open row under Needs attention waits behind it (`waiting_on`, by
`order_key`). An undoing whose undone fact is itself still waiting resolves that row
instead — nothing was posted to turn back.

| Rule (version) | Fact | Voucher kind | Lines |
| --- | --- | --- | --- |
| `expense.approved` (1) | `approved` | `expense` | per line: its gross — or its net with the inferred code (`base`) and its VAT on the code's VAT account (`tax`) — debited on the account of its category's expense rule (naming the rule and version), else on 7700, with the line's project; credited on `employee_payables` for a line the employee is owed (an outlay `paid_by` employee, mileage, a per diem), on `bank` for one the company paid. Dated the fact's day: the entry's date, a claim's latest line date. |
| `expense.reimbursed` (1) | `reimbursed` | `reimbursement` | `employee_payables` debited and `bank` credited with what the standing approval's voucher credited `employee_payables` — or, for an approval before the books, the lines the employee is owed. Dated the reimbursement's day, the reference in its text. |
| `expense.unapproved` (1) | `unapproved` | `expense` | the voucher of the approval it undoes (`undoesSeq`), line for line, every sign turned, naming it |
| `expense.reimbursement_undone` (1) | `reimbursement_undone` | `reimbursement` | the same for the reimbursement |

**The account.** The expense rule in force for the line's category, which must name an
expense account (else the fact waits, `account_kind_mismatch`); no rule — mileage and per
diem have no category — is the default chart's 7700, and without 7700 the fact waits
(`posting_rule_missing`).

**VAT** (R2). A line's VAT is deducted when its category's rule says `vat_deductible`, the
books' `vat_term` is not `none`, the line is an outlay in NOK with VAT above zero, and VAT
÷ (gross − VAT) matches an active input code of 1, 11, 12 or 13 at its rate on the fact's
day: |VAT − round((gross − VAT) × rate ÷ 100)| at most one øre per hundred kroner of the
net, and at least one øre — the nearest rate wins. Otherwise the line posts gross. When the
rule allowed deduction and the VAT matched no rate, an informational row
`vat_not_deducted` names the voucher; the undoing that turns the voucher back resolves it.
A foreign outlay's VAT is never deducted. Mileage and per diem carry none.

**Another currency.** A line not in NOK has no rate in Expenses: the fact waits as
`amount_in_nok_needed`, the row's `expenseLines` naming those lines, until
`/unposted/{id}/nok-amounts` gives each its NOK; the line then posts that amount, with no
VAT deducted.

**The `supplier_invoice` kind** is never posted from Expenses: its approval waits as
`enter_as_supplier_invoice` for `/unposted/{id}/supplier-invoice`. An unapproval of an
approval that was made into a supplier invoice turns nothing back: it is recorded as
`expense_invoice_unapproved`, its `supplierInvoiceId` naming the invoice — a row that waits
while the invoice is a draft (delete or post it, then retry), and an informational row
naming the invoice's voucher once it is posted.

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
`lock_reason_required`). Each change writes a `lock_events` row. A move back behind the end
of the last VAT term filed at Skatteetaten — or lifting the lock while one is filed — is 409
`lock_behind_filed_term` (A8 as R10 amends it), and behind the end of any closed VAT term
409 `lock_behind_closed_term`: a closed term is reopened, never unlocked under it. A VAT
term's close and reopening move the lock themselves ([VAT](#vat)).

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
A line on a VAT account — an account a tax code names as its VAT or counter account — is
refused 400 `opening_vat_account`: VAT owed or due from before the books start is a balance
on the VAT settlement account (`vat_settlement`, 2740), which the first term's payment then
settles. The check (`checkOpeningLines`) runs on the opening's transaction for whatever
builds the voucher, not only the endpoint.

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
(its payment belongs there), `settlement_supplier_invoice` for a supplier invoice's item
(paid on the invoice, [Payments](#payments)), `settlement_settled` for one with nothing left
and `settlement_exceeds_balance` for more than its balance.

## VAT

Delivery D (design A21–A23 as R4 and R10 amend them), migration `00186_accounting_vat.sql`.
The return's layout and XML are the subpackage
[`internal/accounting/mva`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/accounting/mva),
which knows nothing of the ledger; Skatteetaten's material it follows — the
`skattemeldingformerverdiavgift` v1.0 XSD, the code lists and example returns — is copied
under its `testdata` with the commit it was taken from.

### The specification

`GET /vat/specification?from&to` (`accounting:read`; `from` after `to` is 400
`period_invalid`) is the merverdiavgiftsspesifikasjon, computed from the postings by
`voucher_date` and never stored. The vouchers of kind `vat_settlement` — a settlement and
its reversal — are left out: they move a term's VAT and report nothing. Every posting that
carries a tax code, or lands on a **VAT account** (an account a tax code names as its
`vat_account_id` or `counter_vat_account_id`), falls in one **part**:

| Part | The posting |
| --- | --- |
| `base` | `tax_role` `base` |
| `output` | `tax` of an output code |
| `calculated` | `tax_reverse`: a reverse charge's or an import's calculated VAT |
| `deducted` | `tax` of an input code, on a VAT account |
| `not_deducted` | `tax` of an input code on another account: a cost (82, 84, 87, 89, 92, or an input code without a VAT account) |
| `loss`, `loss_base` | `tax_specification` `tapPåKrav`: a write-off's VAT taken back, and its share on `bad_debt` |
| `untaxed` | no tax code, on a VAT account |
| `other` | a `tax` line of a code of direction `none`, or a `tax_reverse` line of a code that is not reverse charge |

Vouchers of kind `opening` are left out as well: a balance at the books' start is no term's
VAT, and the opening refuses lines on a VAT account anyway ([Opening
balances](#opening-balances)).

The figures are summed per tax code, per rate in force on each posting's voucher date, and
per specification (a loss apart), in the return's sign: a base positive for sales and for
purchases (a sale's credited base turned), VAT owed positive, VAT deducted and a loss's VAT
negative; VAT not deducted is shown as the cost it is. `codes` are the reported codes in the
return's order; `notReported` the codes with `reported = false` (0, 7, 20, 21, 22). Each
figure carries its `taxCodeId` and its count of postings.

`GET /vat/postings?from&to` with `taxCodeId`, `part` and `unreconciled` (`offset`, `limit`
default 100, at most 500, with the `total` that match and their `sum`, the whole selection's)
answers the postings behind a figure, in voucher order, with their part.

**The reconciliation** (R10) sets the VAT accounts' movement in the period beside what the
return **counts** — the `output`, `calculated`, `deducted` and `loss` postings of reported
codes — account by account. It reconciles when each VAT account's movement is what is
counted on it and nothing counted lies on another account; otherwise it lists the first 200
postings that make the difference (`unreconciled`): a posting on a VAT account the return
does not count — a manual posting straight onto 2700, a base line or a not-reported code's
tax on a VAT account — or a counted one off the VAT accounts.

`GET /reports/vat` is the same specification as a report, its rows carrying `taxCodeId`,
ending with the VAT counted to the øre and, labelled apart, the return's total in whole
kroner; `/reports/vat/csv` is its file.

### Terms

Terms are derived from the settings, never stored until closed: with `vat_term`
`bimonthly` six two-month terms each **calendar** year (1 January–February … 6
November–December), whatever `fiscal_year_start_month` is; with `yearly` one per calendar
year; with `none` none. They start with the term that holds `books_start` — a start in the
middle of a term starts with that term — and `GET /vat/terms` lists them oldest first
through the term that holds today, each with its `dueDate`: a two-month term on the 10th of
the second month after it, the third on 31 August, the sixth on 10 February; a yearly term
on 10 March of the next year (research §2.3). `needsClosing` is a term ended and not
closed; `needsFiling` one past its due date and not filed; `lockCovers` whether the lock
reaches its last day; `partial` and `coversFrom` a term that starts before the books, whose
return covers `books_start` to its end only; `closing` and `filedClosing` the standing and the
filed closing's numbers; `total` the return's total, as closed or as the books stand (null in
the meta for an open term: the meta computes no figures). With
`vat_term` `none` the list is empty, and every term operation answers 409
`vat_not_registered`. A term out of range, before the books or after today's term is 404.

`GET /vat/terms/{year}/{number}` answers the term, its `return` (the stored one when closed,
the books' now when open), its specification with the reconciliation, `checks` — what a close
would refuse now: `ended`, `earlierOpen`, `partial`, `attention`, `lockAfterEnd`, `staleTerm`,
`rateMissing`, `reconciled` — its `closings` and its history, newest first.
`GET …/closings/{closing}` answers one closing's return as it was kept, and
`GET …/closings/{closing}/xml` its XML (never called by the Assistant), whatever happened to
the term since.

| Table | What it holds |
| --- | --- |
| `vat_terms` | A term once first closed: `year`, `term`, `term_kind`, `period_start`, `period_end`, `state` (`open`, `closed`), `closings` (the closes so far), `current_closing_id` (the closing that stands while closed, null while open — `ck_vat_terms_closed`), `filed_at` (a date), `filed_reference`, `filed_by_user_id` and `filed_closing_id`, set together and only on a closed term, naming its standing closing (`ck_vat_terms_filed`). Unique per `(year, term, term_kind)`: a yearly term 1 is not a two-month term 1. |
| `vat_term_closings` | Every close: the term, its `closing` number, `figures` (the return as answered, jsonb), `return_xml`, `settlement_voucher_id` (null when nothing moved), `closed_at`, `closed_by_user_id`. Never changed or deleted (`refuse_change()`): a reopening leaves it, so a filed return is always there to show. |
| `vat_term_events` | Every `closed` (its closing and settlement voucher), `reopened` (the closing, the reversal and the reason), `filed` (the closing, the day and the reference) and `unfiled` (the reason), who and when. Append-only, like `lock_events`. |

The books' `vat_term` is fixed once a term row exists (`tr_settings_freeze_vat_term`): the
terms and their returns are derived from it.

### Close

`POST /vat/terms/{year}/{number}/close` (`accounting:close`) runs in one transaction that
first takes `settings` **FOR UPDATE**, so every post in flight is waited for and none starts
until it commits, then the term's row FOR UPDATE. It refuses, in this order:

| Status, code | When |
| --- | --- |
| 409 `vat_term_closed` | the term is closed (a second close at the same moment waits for the first and gets this) |
| 409 `vat_term_not_ended` | the term's last day has not passed |
| 409 `vat_term_out_of_order` | an earlier term is not closed; the detail names it |
| 409 `vat_term_partial` | the term starts before `books_start` and the body does not say `confirmPartial: true`: its return covers `books_start` to its end only |
| 409 `vat_term_attention` | facts under Needs attention that wait (not posted, not resolved; informational rows never) with a `fact_date` in the term; the detail counts them |
| 409 `vat_term_lock_after_end` | `locked_through` is on or after the term's last day: the settlement cannot be posted on it; move the lock back |
| 409 `vat_term_stale` | a closed term's figures, computed again, are not the return it was closed with (exact total and every line's code, side, specification, base and VAT); the detail names it. The lock keeps a closed term's postings as they were, so this is a defence in depth |
| 409 `vat_term_rate_missing` | a reported code with an output side has amounts but no rate on a posting's date |
| 409 `vat_term_unreconciled` | the VAT accounts do not reconcile; the detail names the vouchers |
| 409 `vat_term_inconsistent` | the VAT counted is not the return's exact total turned, or the rounding difference is a krone a line or more (lines left out as zero counted); logged as an error — a fault of the module, never of the books |

It then builds the return and its XML, posts the settlement voucher, writes the closing
(`closings` + 1, the figures, the XML, the voucher, who and when) and sets the term `closed`
on it, and only then (R10) moves
`locked_through` to the term's last day with a `lock_events` row whose reason names the
term, and writes the `closed` event.

| Rule (version) | Voucher kind | Source | Lines |
| --- | --- | --- | --- |
| `vat.settlement` (1) | `vat_settlement`, dated the term's last day | `(accounting, vat_settlement, term id, <closings>)` | each VAT account's movement in the term turned back; `vat_settlement` credited with the return's `total` in whole kroner (debited for a total to receive); the rounding difference on `rounding` |
| `vat.settlement_reversed` (1) | `vat_settlement`, dated the term's last day | `(accounting, vat_settlement_reversed, term id, <closings>)` | the settlement's stored voucher, line for line with every sign turned, naming it as the one it `reverses` |

Both are system vouchers: the generic reversal refuses them (`voucher_owned_by_source`). A
term whose VAT accounts did not move posts none. After the close a system voucher dated in
the term is booked on the first day after it (A8) and counts in the next term; a person's is
refused (`period_locked`).

### Reopen, filing and the lock

- `POST …/reopen` (`accounting:close`, `reason` 1–500, else 400 `lock_reason_required`)
  opens the **latest** closed term (409 `vat_term_not_latest` while a later one is closed)
  that is **not filed** (409 `vat_term_filed`; 409 `vat_term_not_closed` for an open one).
  Under the settings row FOR UPDATE it first moves the lock back to the day before the
  term's first day — lifts it for a term that starts on or before `books_start`; never
  forward — with a lock event carrying the reason, then posts `vat.settlement_reversed` on
  the term's last day (R10), and sets the row `open`. The closing stays in
  `vat_term_closings` with its return, its XML and its settlement voucher; a later close is
  the next closing.
- `POST …/filing` (`accounting:post`) takes `filedAt` — after the term's last day and not
  after today (400) — and `reference` (1–100), and names the standing closing as the one
  filed: 409 `vat_term_not_closed`, `vat_term_filed`.
- `POST …/unfile` (`accounting:close`, `reason`) takes the mark off, writes the event and
  logs a warning: 409 `vat_term_not_filed`.
- **The lock** (A8 as R10 amends it, and the review's H1): `PUT /lock` refuses to move back
  behind the end of the latest filed term — to an earlier day, or lifted — with 409
  `lock_behind_filed_term`, and behind the end of the latest closed term with 409
  `lock_behind_closed_term`. A closed term is reopened instead, so its stored return always
  covers what is posted in it; `lockCovers` can only be false for an open term.
- `GET …/xml` (`accounting:read`, never called by the Assistant) streams a closed term's
  stored XML as `mva-melding-<year>-<number>.xml`, never cached; 409 `vat_term_not_closed`.

Close, reopen, filing and unfiling are called by the Assistant only after the person
confirmed them.

### The return

The return is laid out as Skatteetaten's: the groups and their order are the `mvaKodeSAFT`
code list's `temaRekkefoelge` and `kodeRekkefoelgeITema` (the package's test holds its table
to the list), and each code becomes its lines:

| Codes | Group | Lines (`mvaSpesifikasjonslinje`) |
| --- | --- | --- |
| 3, 31, 33 | Sales in Norway | one: `grunnlag` the base, `sats` the rate, `merverdiavgift` the output VAT |
| 5, 6 | Sales in Norway | one: the base, `sats` 0, VAT 0 |
| 52 | Sales abroad | one: the base, 0, 0 |
| 1, 11, 13 | Purchases in Norway | one deduction: `merverdiavgift` the VAT deducted, negative; no base, no rate (R065) |
| 81, 83 | Goods from abroad | two: the base, the rate and the calculated VAT; then the deduction alone, negative (R081, the example `mvakode81.xml`) |
| 14, 15 | Goods from abroad | one deduction |
| 82, 84 | Goods from abroad | one: base, rate, calculated VAT — no deduction |
| 85 | Goods from abroad | one: base, 0, 0 |
| 86, 88 | Services from abroad | two, as 81 |
| 87, 89 | Services from abroad | one, as 82 |
| 32 | Fish | one, as 3 |
| 12 | Fish | one deduction |
| 51 | Emission allowances and gold | one: base, 0, 0 |
| 91 | Emission allowances and gold | two, as 81 |
| 92 | Emission allowances and gold | one, as 82 |
| a loss posted with 3, 31, 32, 33 | on 1, 11, 12, 13 | one: `spesifikasjon` `tapPåKrav`, the VAT taken back, negative, no base, named as the input code is (Skatteetaten's rule R038 allows `tapPåKrav` on 1, 11, 12 and 13 only, so the loss's output code becomes the input code of its rate) |
| 0, 7, 20, 21, 22 | — | none: `reported = false`, listed apart in the specification |

**One line per combination.** Skatteetaten's model has one `mvaSpesifikasjonslinje` per
combination of code, specification and `mvaKodeRegnskapsystem`. Figures that would make
the same line — one input code at two rates within a term, say — are merged exactly and cut
once; a reverse charge's deduction line carries the code's name with *(deduction)*
(*(fradrag)* in Norwegian books), so its two lines are two combinations, as the published
examples write them.

**Rounding.** The return takes no decimals (R082). Each line's base and VAT are the ledger's
exact sums with the øre **cut toward zero** — Skatteetaten's FAQ speaks of cutting the
decimals — and `fastsattMerverdiavgift`, the `total`, is the sum of the lines' VAT (R018).
R019 checks each line's VAT against its base × its rate rounded down; its severity is
*avvikende* (a deviation Skatteetaten may ask about, not an invalid return), and the FAQ
gives it a tolerance of one krone a line, ten kroner at least for the return. A code's VAT
is the sum of each document's VAT rounded to the øre, so a term of very many documents can
drift past one krone on one line: the return's `warnings` then name the code. Before a close
posts, the VAT counted must be the return's exact total turned and the rounding difference
less than a krone a line, else 409 `vat_term_inconsistent`. `exactTotal` is the ledger's and `roundingDifference` = `exactTotal` −
`total`, which the settlement voucher posts on `rounding`. A line whose base and VAT are
both zero once cut is left out; its øre stay in `exactTotal`.

**Signs.** VAT owed is positive and VAT deducted negative. A line whose sign is opposite to
its code's — an output line with a negative base, a deduction with a positive VAT, as when a
term holds more credit notes than sales — carries `merknad`/`utvalgtMerknad` `kreditnota`
(R020, R021; the `merknadTilsvarendeMvaKode` list allows it for either sign on every code),
and the return's `warnings` say so. A `tapPåKrav` line needs none, of either sign: R021
excepts code 1, 11, 12 or 13 with `tapPåKrav` from the remark a positive deduction needs,
so a term where recoveries outweigh new losses reports a positive `tapPåKrav` line bare.

**The XML** (`mvaMeldingDto`, namespace
`no:skatteetaten:fastsetting:avgift:mva:skattemeldingformerverdiavgift:v1.0`):

| Element | Value |
| --- | --- |
| `innsending/regnskapssystemsreferanse` | `vantigo-<year>-<number>-<closings>`, one per close |
| `innsending/regnskapssystem` | `systemnavn` Vantigo, `systemversjon` the build's version |
| `skattegrunnlagOgBeregnetSkatt/skattleggingsperiode` | `periode/skattleggingsperiodeToMaaneder` `januar-februar` … `november-desember`, or `skattleggingsperiodeAar` `aarlig`; `aar` the year |
| `fastsattMerverdiavgift` | the total, whole kroner |
| `mvaSpesifikasjonslinje` | per line above: `mvaKode` the tax code's own code, `spesifikasjon`, `mvaKodeRegnskapsystem` the code's name in the books (a deduction's with *(deduction)*, a loss's the input code's), `grunnlag` and `sats` (`25`, `15`, `12`, `11,11`, `0`) on an output side, `merverdiavgift`, `merknad` |
| `betalingsinformasjon` | empty: a KID for a refund is Skatteetaten's to ask for |
| `skattepliktig/organisasjonsnummer` | the seller's, from Invoices through `SalesTax.Seller()` (spaces, dots, `NO` and `MVA` taken out); left out — and a warning given — when Invoices is not composed or has no nine-digit number |
| `meldingskategori` | `alminnelig` |

The test suite validates the goldens (a term of mixed rates; one with reverse charge, VAT
not deductible, a loss and a reversal; an empty term) and Skatteetaten's examples against
the XSD with `xmllint` when it is on the PATH, and CI runs the
[VAT return oracle](/en/contributing/vat-return-validation/) over the same files.

### The meta and the Overview

The meta answers `vatNextTerm` — the earliest term ended and not filed, else the one that
holds today; null without VAT terms; states and dates only, its `total` null while it is
open — `vatTermsToClose`, `vatTermsToFile`, and
`vatRegistrationMismatch`: whether `vat_term` (`none` or not) disagrees with the seller's
`VATRegistered` in Invoices (R10). The host's dashboard names, after the facts under Needs
attention, the terms to file and then the terms to close.

## SAF-T Financial

Delivery E (design A24, A25), migration `00187_accounting_saft.sql`. The file's writing and
reading are the subpackage
[`internal/accounting/saft`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/accounting/saft),
which knows nothing of the ledger. Skatteetaten's material it follows — the SAF-T Financial
1.30 and 1.40 XSDs and an example file of each — is copied under its `testdata` with the
commit it was taken from. The rules the XSD does not express are from Skatteetaten's
*Norwegian SAF-T Financial data — Technical description* v1.7 (31 August 2026): amounts
carry no sign, debit and credit being separate elements; a mandatory string with no value is
`NA`; a file holds one fiscal year at most; every account and every tax code is in
MasterFiles, the customers and suppliers may be limited to those with transactions or a
balance; a deviating fiscal year's period and year are its own.

### The export

`GET /saft-exports/check?fiscalYear&fromPeriod&toPeriod` (`accounting:export`) answers what
an export would meet and writes nothing: the selection's days, the accounts with no grouping
code for the year (`unmappedAccounts` — they export `NA`), `sellerMissing` and the vouchers
dated in it. `POST /saft-exports` takes `fiscalYear` (from the one `books_start` falls in to
today's, else 400 `period_invalid`), `fromPeriod` and `toPeriod` (1–12, in order, default 1
and 12; period 1 is the fiscal year's first month), refuses 409 `saft_seller_missing` when
Invoices has no seller with a name and a nine-digit organisation number (`SalesTax.Seller()`,
nil-safe), 503 `storage_not_configured` without an object store, writes the `saft_exports`
row queued with the count of unmapped accounts, logs who asked and for what, and answers 202
with the export and the unmapped accounts. `GET /saft-exports` lists the latest 100 newest
first, each with its files and the name of who asked (`Users`); `GET /saft-exports/{id}` one;
`GET /saft-exports/{id}/files/{part}` streams a file under its name, never cached, and logs
the download with the file's SHA-256.

**The worker** `accounting.saft` polls every 10 seconds, as `module.Workers` builds it:

1. **Claim**, in one short transaction: the oldest export `queued`, or `running` whose lease
   has run out — a worker that stopped — under a new lease of ten minutes (`FOR UPDATE SKIP
   LOCKED`; `attempts` + 1). A live lease is never taken.
2. **Write**, with no transaction open and no connection held: the seller, the customers'
   billing profiles and the projects' names are read through their contracts; every read of
   the books is one query on the pool, the vouchers a page of 500 at a time, each bounded by
   the highest voucher id committed when the run began. The document is streamed through a
   pipe into the object store's `Put`, counted and hashed as it passes, so memory stays
   constant whatever the size. The lease is renewed at every page; a lost lease stops the
   run and leaves the export to the one that holds it.
3. **Check** what was streamed against the totals written first: a voucher committed while
   the run was writing, with a lower id than the bound, makes them differ, and the export
   fails with *the books changed while the file was written; export again*.
4. **Split**: a file that would pass `ACCOUNTING_SAFT_SPLIT_BYTES` (2 000 000 000 by
   default) is thrown away and its periods written again as two halves, and so on; each part
   is a whole file — header, master files with the part's own opening and closing balances,
   the part's vouchers — valid alone. One period alone over the threshold fails the export.
5. **Record**, in one transaction under the lease: a `saft_export_files` row per part with
   its name, key, size and SHA-256, and the export `done` with its total size; or `failed`
   with the error, the objects written removed. Both are logged with who asked.

The file is named as Skatteetaten recommends: `SAF-T Financial_<organisation number>_<yyyymmddhhmmss>_<part>_<parts>.xml`,
the time the run recorded it in Norwegian time.

### What the file holds

| Element | From |
| --- | --- |
| `Header/AuditFileVersion`, `AuditFileCountry` | `1.40`, `NO` |
| `AuditFileDateCreated` | the day of the run |
| `SoftwareCompanyName`, `SoftwareID`, `SoftwareVersion` | `Vantigo`, `Vantigo`, the build's version |
| `Company` | the seller: `RegistrationNumber` the organisation number, `Name` the legal name, `Address` the address (`StreetAddress`), `Contact` with `ContactPerson` `NA` `NA` (no contact person is kept: the technical description's empty value) and the seller's phone and e-mail, `TaxRegistration` `<number>MVA` with `TaxAuthority` Skatteetaten when VAT-registered, `BankAccount` the seller's account |
| `DefaultCurrencyCode` | `NOK` |
| `SelectionCriteria` | `PeriodStart`/`PeriodStartYear`/`PeriodEnd`/`PeriodEndYear`: the periods of the fiscal year, the year it begins in |
| `HeaderComment` | the selection's days |
| `TaxAccountingBasis` | `A`, the only value |
| `GeneralLedgerAccounts/Account` | **every account**: `AccountID` the number, `AccountDescription` the name, `GroupingCategory`/`GroupingCode` the grouping in force for the fiscal year (`account_groupings`, the greatest `from_year` not after it) or `NA`/`NA`, `AccountType` `GL`, `AccountCreationDate`, the opening balance before the selection's first day and the closing balance at its last as the trial balance counts them — a balance-sheet account everything before, an income or expense account from the fiscal year's start — `Opening`/`ClosingDebitBalance` for zero or a debit, `…CreditBalance` for a credit. The earlier years' result the books have not closed (the trial balance's own line) is added to the `year_result` account's opening and closing, so the file's balances sum to zero as a closed year's would. |
| `Customers/Customer` | every customer with a posting on `receivables` in the selection or a balance there at either end: `CustomerID` its customer number in Customers (its id when Customers does not answer it), `Name` the legal name, `RegistrationNumber` the organisation number of a business (none for a person), `Address` the invoice address, `Contact` the invoice e-mail, one `BalanceAccount` per control account with its opening and closing balances, `PartyInfo` with the payment terms, `Type` `Company` or `Private` and `Status` |
| `Suppliers/Supplier` | the same from `suppliers` on `payables`: `SupplierID` the supplier's number, the organisation number, the address, the bank account (`BankAccountNumber`, else `IBANNumber`) and BIC; a supplier id from before the table is named by its id |
| `TaxTable/TaxTableEntry` | `TaxType` `MVA`, `Description` `Merverdiavgift`, and **every tax code**: `TaxCode` and `StandardTaxCode` the code itself (the books keep Skatteetaten's standard list), `EffectiveDate` the rate period's start, `Description` the name, `TaxPercentage` the rate in force at the selection's end (none for a code without a rate), `Country` `NO`, `BaseRate` 100 — or 0 for an input code whose VAT is a cost (82, 84, 85, 87, 89, 92) |
| `AnalysisTypeTable` | the projects a posting of the selection names: `AnalysisType` `P`, `AnalysisID` the project's id, `AnalysisIDDescription` its code and name from Projects |
| `GeneralLedgerEntries` | `NumberOfEntries`, `TotalDebit`, `TotalCredit` of the vouchers dated in the selection, then the journals below |
| `Transaction` | a voucher: `TransactionID` its number as shown (`2026-000123`), `Period`/`PeriodYear` its period of the fiscal year, `TransactionDate` the voucher date, `VoucherType` the voucher kind and `VoucherDescription` its words, `Description`, `SystemEntryDate` and `GLPostingDate` the day it was posted (Norwegian time), `SystemID` the voucher's id |
| `Line` | a posting: `RecordID` its line number, `AccountID`, `Analysis` `P` with the project and the amount, `SourceDocumentID` the open item's document number or the source (`invoices:document:123`), `CustomerID` or `SupplierID` on a line on `receivables` or `payables` only, `Description` (the posting's text, else the voucher's), `DebitAmount` or `CreditAmount` with `Amount` and, for a purchase in another currency, `CurrencyCode`, `CurrencyAmount` and `ExchangeRate`, `TaxInformation` (below), `ReferenceNumber` and `DueDate` the open item's |

**The journals**, a fixed mapping by voucher kind, in this order; an empty journal is left out:

| `JournalID` | `Type` | Voucher kinds |
| --- | --- | --- |
| `SALES` | `AR` | `sales_invoice`, `sales_credit_note`, `charge`, `write_off` |
| `PURCHASES` | `AP` | `supplier_invoice`, `supplier_credit_note`, `expense` |
| `BANK` | `GL` | `customer_payment`, `supplier_payment`, `reimbursement`, `bank` |
| `GENERAL` | `GL` | `manual`, `opening`, `vat_settlement`, `import` |

**TaxInformation** follows each posting as stored. The technical description defines it as
*tax information for the accounting line*, and says that when the books keep no tax amount on
a line it is filled with 0.00 on that line with its tax code — and that nothing is to be
computed solely for the export. The books keep a base posting and its VAT postings apart (a
sales invoice's VAT is per VAT summary, not per line), so the base's share of a VAT posting
is not in them; Skatteetaten's example file, which puts the VAT on the base line, comes from
a system that keeps it there.

| Posting | `TaxInformation` |
| --- | --- |
| `tax_role` `base` | `TaxType` `MVA`, `TaxCode`, `TaxPercentage` the rate on the voucher date, `Country` `NO`, `TaxBase` the posting's amount, and `DebitTaxAmount` or `CreditTaxAmount` 0.00 on the posting's own side |
| `tax` (output VAT, deductible input VAT, a reverse charge's deducted side, VAT not deductible on a cost account) | the code and rate, `TaxBase` the posting's `tax_base`, and the posting's amount as `DebitTaxAmount` (a debit) or `CreditTaxAmount` (a credit), with its currency amount in another currency |
| `tax_reverse` (a reverse charge's or an import's calculated VAT, on 2705) | the same: its base and its amount, a credit |
| `tax_specification` `tapPåKrav` | as its role says: the VAT taken back is a `tax` posting debited on the output VAT account with the output code; SAF-T has no element for the specification, which the VAT return carries |
| no tax code | none |

So a reverse charge with 86 shows three lines with code 86: the cost with its base and 0.00,
the deductible VAT debited on 2715 and the calculated VAT credited on 2705, each with the
base. Summing `TaxBase` over the base lines gives the base per code; summing the tax amounts
gives the VAT, each side apart.

**The tests hold a file to the books beyond the XSD**: the totals to the sums of the lines,
debit to credit; every account's closing balance to the trial balance (the earlier years'
result on `year_result`); each customer's balance to the customers report and the suppliers'
to the suppliers report; every `AccountID`, `CustomerID`, `SupplierID`, `TaxCode` and
analysis a line names to the master files. The golden of a fixed ledger with every voucher
kind is validated against the 1.40 XSD with `xmllint` when it is on the PATH, and by the
[SAF-T oracle](/en/contributing/saft-validation/) in CI.

### The import

The way in from another system (A25), and the other way to Set up the books: its operations
answer before the books are set up, and need `accounting:close`.

`POST /saft-imports` takes the multipart field `file`, at most `ACCOUNTING_SAFT_IMPORT_MAX_BYTES`
(512 MiB by default; 400 `saft_file_too_large`, the router's body limit set to match;
rate-limited with the other uploads), only while the ledger has no voucher (409
`saft_import_ledger_has_vouchers`) and the books are not set up (409 `books_already_set_up`).
The file is spooled to a temporary file and hashed, then **read as a stream** by
`saft.Read`: strict XML, UTF-8 only, the root `AuditFile` in the SAF-T namespace, **any
DOCTYPE refused** — and with it every entity declaration and external reference, so nothing
is ever expanded or fetched (XXE, the billion laughs) — and an entity beyond XML's five
refused (400 `saft_file_invalid`). The header's `AuditFileVersion` must be 1.30 or 1.40 (400
`saft_version_unsupported` for 1.20 or older, or one it cannot name: export again). The
master files are decoded one account, customer and supplier at a time; the transactions are
counted and skipped. The selection is the header's dates, or its periods read as calendar
months. The file is then kept in the object store with its SHA-256 and the master data as
`preview`, and the preview is answered (201). `GET /saft-imports/{id}` answers it again, the
customers matched again; `GET /saft-imports` lists the imports; `GET /saft-imports/{id}/file`
(`accounting:export`) streams the file as uploaded.

**The preview** — what Apply would write, with what stands in its way (`blockers`, and
`canApply`):

- **The books start** the day after the selection ends (`booksStart`; not before 2022-01-01,
  else `books_start_too_early`); the fiscal year's first month is proposed from the
  selection's.
- **The chart** replaces the default: each account's number, name (200 characters) and kind
  from its number's class (layer A's rule). A number that does not fit — not 4 to 6 digits
  beginning with 1 to 8 — is taken with its non-digits removed when that fits, left out when
  it has no balance, and otherwise *needs a number* (a blocker); a number already taken
  needs one too. An income or expense account may be left out with a balance — it is part of
  the result — a balance-sheet account may not. The apply's `accounts` (file id → number,
  `""` to leave out) overrides. A grouping (1.30/1.40 `GroupingCategory` and `GroupingCode`)
  is kept from the books' first year when it is on that year's list, and dropped with a
  problem otherwise; `NA` is none; an account with only the old `StandardAccountID` gets no
  grouping, and the preview says how many.
- **The balances** of the file's accounts must sum to zero.
- **The control accounts** are the ones the customers' and the suppliers' `BalanceAccount`s
  name; the parties' balances on more than one account each are a blocker (these books keep
  one control account per side).
- **The roles** are proposed: `receivables` and `payables` on the parties' control
  accounts, every other role on the default chart's number; a role whose account the file
  has not adds the default chart's account (`addedAccounts`, with the reason), as do the
  standard tax codes' VAT and counter accounts the file lacks. The apply's `roles` (role →
  number) overrides; every role must end on an account the chart will have (400
  `account_unknown`), of the role's kind (400 `account_kind_mismatch`), one role an account.
- **The tax codes**: the file's tax table mapped to the standard codes by `StandardTaxCode`;
  the books seed Skatteetaten's whole standard list as Set up does, and the unmapped are
  counted in the notes.
- **The suppliers** are made, numbered from the counter: the name, a Norwegian organisation
  number that passes mod 11 (a second supplier with the same number keeps none), the
  country, the address, an e-mail with an @, a bank account of eleven digits passing mod 11,
  the IBAN and BIC; what is dropped is said per supplier.
- **The customers** are matched to Customers by organisation number through
  `CustomersByLegalID` (a merged-away holder answers its survivor): one holder is
  `matched`, several `ambiguous`, none `unmatched`; a customer with no balance is
  `not_needed`. The apply's `customerLinks` (file customer id → a customer of Customers that
  `Customers` answers) makes it `linked`. **Accounting writes no customer**: a customer with
  a balance that is not matched or linked is a blocker (409 `saft_import_unmatched_customers`),
  rather than a balance on a holding account or party — which would be books that say a
  balance is no customer's.
- **The opening voucher**, dated `booksStart`: every balance-sheet account's closing
  balance but the control accounts' and the VAT accounts'; per customer and per supplier
  with a balance one line on the control account opening **one item** (document number
  *Opening balance* with the file's id, dated the selection's end); the **VAT accounts'**
  closing balances — the standard tax codes' VAT and counter accounts — summed onto the
  `vat_settlement` account (`opening.vatMoved`, a note *VAT owed at the start*), since the
  opening voucher posts nothing on a VAT account; and the result — what the lines leave —
  on the `opening` account in equity. The customers' and the suppliers' balances must equal
  their control account's closing balance (`receivablesDifference`, `payablesDifference`;
  409 `saft_import_parties_mismatch` with the difference).

`POST /saft-imports/{id}/apply` takes `vatTerm`, `language`, `fiscalYearStartMonth`, an
optional `booksStart` (400 `saft_import_books_start_mismatch` unless the day after the
selection), `accounts`, `roles` and `customerLinks`. It builds the plan again, matching the
customers with no transaction open, refuses while a blocker stands — 409
`saft_import_unresolved`, `saft_import_unmatched_customers` or `saft_import_parties_mismatch`
with every blocker in the detail — and then, **in one transaction**, takes the import FOR
UPDATE (409 `saft_import_applied` a second time), checks again that the ledger has no
voucher, writes the settings (409 `books_already_set_up` if a setup came first: the one
settings row makes the two ways mutually exclusive), the chart through the same `seedChart`
and `seedTaxCodes` Set up the books uses, the default posting rules, the suppliers, the
opening voucher through `Post` as the person's, and marks the import applied with its
voucher. History is not imported as vouchers: the uploaded file is the record of those years.

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
| `customers`, `suppliers` | Per party a heading, its items with a balance at `to` or a movement in the period, the balance aged by due date at `to` (not due, 1–30, 31–60, 61–90, over 90 days), the party's subtotal and the total. A supplier's heading is its name and carries `supplierId`; an item row carries the `supplierInvoiceId` it stands for. The suppliers' total is the balance of `payables`. |
| `trial_balance` | Per account the opening balance, the debits, the credits and the closing balance, and with a comparison the closing balance at `compareTo`. |
| `profit_and_loss` | The period's movement on income and expense accounts, income positive and costs negative, grouped by class with the operating result, the result before tax and the result; an income or expense account in no class group is under **Other income and costs**, before the result. |
| `vat` | The [VAT specification](#the-specification): per tax code, rate and specification the base, output VAT, calculated VAT, deductions, loss and VAT not deducted, the reported codes in the return's order and then those left out, each row with its `taxCodeId`; the total the return counts and the VAT accounts' movement. |
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
  charge only) and activity under its revision. The VAT and counter accounts are fixed once a
  posting carries the code (409 `tax_code_in_use`): the VAT specification reads what is
  deducted from where the code's tax lies. `POST /tax-codes/{id}/rates` starts a rate
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
