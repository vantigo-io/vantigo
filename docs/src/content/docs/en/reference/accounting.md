---
title: "Accounting module"
description: "The company's books: the module and the module it requires; the four permissions; Set up the books and the gate before it; the settings; the default chart with Skatteetaten's grouping codes, the system roles and the standard tax codes with their rates from 2022; accounts and their rules; groupings per year; tax codes and rate periods; vouchers and postings and the rules the database keeps — balance, a voucher has postings, immutability, postings only on the voucher just inserted, the books' start and the lock read under a share lock; the posting engine — numbering, dating, the lock rule, zero lines, open items and idempotence on the source key; manual journals and their VAT lines; reversal; attachments; the lock and its history; the opening balances and Invoices' open invoices; open items and settlements; the reports and their CSV; the refusals and the endpoints."
sources:
  - apps/server/internal/accounting
  - openapi/accounting.yaml
  - apps/server/internal/openapi/specs/accounting.yaml
  - apps/server/internal/db/migrations/00179_accounting_baseline.sql
---

Accounting keeps the company's books as bokføringsloven asks for them. Its package is
[`apps/server/internal/accounting`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/accounting),
its schema `accounting` (migration `00179_accounting_baseline.sql`), its contract
[`openapi/accounting.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/accounting.yaml),
mounted at `/api/v1/accounting`. The design is
`docs/superpowers/specs/2026-11-14-accounting-design.md`; this page is what delivery A
built. The user guide is [Accounting](/en/user/accounting/).

## The module

- `MODULES`: **`accounting` requires `invoices`** (which requires `customers`); without it
  the server refuses to start, naming it. It reads `contracts.SalesJournal` (Invoices'
  open invoices for the opening balances), `contracts.CustomerDirectory` (customers'
  names) and, when Projects is on, `contracts.ProjectDirectory` (projects' names). Every
  call out of the module is made with no transaction of its own open.
- It starts no worker and publishes no event in this delivery.
- Attachments live in the object store under the scope `accounting`.

## Permissions

| Key | Display | Delegable | Sensitive | Asked by |
| --- | --- | --- | --- | --- |
| `accounting:read` | Use Accounting | yes | no | every operation |
| `accounting:post` | Post vouchers | yes | no | manual journals, their preview, reversal, attachments, settlements |
| `accounting:close` | Manage the books | **no** | no | setup, the chart, the roles, the tax codes, the opening balances, the lock |
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
description, an optional `Source{Module, Kind, ID, Part}`, the rule and its version, the
voucher it reverses with the reason, who posts it, and its `Line`s: an account id or a
system role, a signed exact amount, an optional tax code with its role (`base` or `tax`)
and base, a customer or a supplier, the open item (by id, by the Invoices document it
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
