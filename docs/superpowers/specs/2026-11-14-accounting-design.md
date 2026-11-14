# Accounting — the ledger, sales and purchases that post themselves, VAT, SAF-T, bank (phase 1)

Revision 2, 2026-11-14, after a critic review. **§13 amends earlier decisions and wins
where they differ**; it also lists what changed. This spec builds on the research `docs/superpowers/research/2026-11-14-accounting-module.md`
("R"). It does not reopen the owner's answers in R §10, and it takes the defaults of R §9
except where §1 says otherwise. Decisions are numbered A*n*. The implementation plan is
`docs/superpowers/plans/2026-11-14-accounting-plan.md`. Abbreviations:

- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/accounting/frontend/src/`.

## 1. Purpose

Accounting is a new module: **Accounting** (`accounting`; Norwegian **Regnskap**; package
`@vantigo/accounting-ui`). It keeps a company's books as bokføringsloven requires:

- **The ledger:** accounts, vouchers that are never edited, periods that lock.
- **Posting without typing:** what Invoices and Expenses already record becomes vouchers.
- **Purchases:** suppliers, supplier invoices, what is owed.
- **VAT:** every tax code, the term's return as figures and as XML.
- **SAF-T:** the file an audit asks for, and the way in from another system.
- **Bank:** statements imported and every movement reconciled.

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **0** | The seams: `invoices.document.issued`, `invoices.payment.removed`, the `SalesJournal` contract, the Brønnøysund client as a platform package | — |
| **A** | The module, settings, the chart with grouping codes, tax codes, vouchers and postings, manual journals, reversal, periods, opening balances, the specifications | — |
| **B** | Sales post themselves: documents, payments, charges; posting rules; the customer ledger; the catch-up worker; write-offs | 0, A |
| **C** | Purchases: suppliers, supplier invoices, the supplier ledger, payments; expenses post | A |
| **D** | VAT: the specification, terms, the return as figures and XML, the settlement voucher | B, C |
| **E** | SAF-T Financial 1.40 export, and import as opening balances | B, C |
| **F** | Bank: accounts, camt.053 and CSV import, reconciliation; the bank feed as a third source | B, C; the feed part waits for the Folio bank feed |

**The owner's answers (R §10, 2026-11-14).** We register as a system vendor soon, and that
blocks phase 2 only. A self-hoster brings their own Maskinporten client, and the
administration guide says so. Bank is files in this phase, and the Folio
bank feed when it is built (2026-11-14: *we will also have the bank integration with Folio
soon*). The skattemelding is not built.
Nothing is a paid add-on. The chart and its names are ours.

**What changed from R §9.**

- R §9.4 moved the seller's record to a shared place. It **stays in Invoices**, read
  through `contracts.SalesTax.Seller()`; Accounting adds only what a ledger needs (A2).
- R §9.3 made events the source with a contract to catch up. The **contract is the
  source**; the event only makes it prompt (A12).

**Not in this phase.** §12 says what each waits for: filing with Skatteetaten (MVA-melding
submission, Frister, Krav og betalinger), inbound EHF, a bank feed and outgoing payments,
year-end, several legal entities, foreign-currency ledgers, stock valuation, fixed assets,
payroll.

## 2. Delivery A — the ledger

### A1 — The module

- Package `srv/accounting`, schema `accounting`, contract `openapi/accounting.yaml`.
- `MODULES`: **accounting requires invoices**, which requires customers. It works with
  every other business module off.
- Permissions:
  - `accounting:read` — every screen and report;
  - `accounting:post` — manual journals, supplier invoices, payments, bank reconciliation;
  - `accounting:close` — settings, the chart, posting rules, period locks, VAT terms;
  - `accounting:export` — SAF-T and the specification exports, which carry names.
- An **accountant** is an ordinary user with these permissions: invited, named, revocable.
  Identity's roles already do it; the user guide has the procedure.

### A2 — Settings

One row, `accounting.settings`:

- `books_start` (date): the first day the ledger covers. Nothing dated before it is
  posted (A13). Set once, at activation, and locked by the first voucher.
- `fiscal_year_start_month` (1–12, default 1).
- `vat_term`: `bimonthly`, `yearly` or `none`. `none` when the seller is not registered.
- `locked_through` (date, nullable): A8.

The company's name, organisation number and VAT registration come from
`SalesTax.Seller()`.

### A3 — Accounts

- `accounting.accounts`: `number` (4 to 6 digits, unique), `name`, `kind` (`asset`,
  `liability`, `equity`, `income`, `expense`), `default_tax_code` (nullable), `active`,
  `system_role` (nullable, unique).
- A number is never reused and an account with postings is never deleted, only made
  inactive. Its number and kind freeze at the first posting; its name may change.
- **System roles** name the accounts the module posts to by itself:
  `receivables`, `payables`, `bank` (the default one), `vat_settlement`, `rounding`,
  `bad_debt`, `employee_payables`, `opening`, `year_result`, `clearing_vipps`,
  `charge_income`, `interest_income`. Output and input VAT accounts are on the tax codes
  (A5). A role can be moved to another account with `accounting:close`; never removed.
- **Grouping** (`accounting.account_groupings`): `account_id`, `from_year`, `category`,
  `code`. SAF-T needs it per account, kept historically. Skatteetaten's yearly list is
  embedded as data (`srv/accounting/grouping/<year>.csv`) and offered in a picker; an
  account without one exports `NA` and shows as an attention item.
- **The default chart** is seeded at activation, not in the migration: about 130
  accounts in the four-digit space Norwegian accountants know (1500 receivables, 1920
  bank, 2400 payables, 2700–2740 VAT, 3000 sales, 4000 goods, 5000 pay, 6000–7000 other
  cost, 8000 finance), **with our own names in the workspace's language** and a grouping
  code each. A customer may edit all of it, or arrive with their own chart (E3).

### A4 — Vouchers and postings

- `accounting.vouchers`:
  - `fiscal_year`, `number` (gap-free per fiscal year, from a counter row as Invoices does
    it, so a failed post gives the number back), shown as `2027-000123`;
  - `voucher_date` (the date it is booked on), `document_date`, `description`;
  - `kind`: `manual`, `opening`, `sales_invoice`, `sales_credit_note`, `customer_payment`,
    `charge`, `write_off`, `supplier_invoice`, `supplier_credit_note`, `supplier_payment`,
    `expense`, `reimbursement`, `vat_settlement`, `bank`, `import`;
  - `source_module`, `source_kind`, `source_id`, `source_part` (nullable together), with a
    **unique index** over the four: one source fact, one voucher, however often it is
    delivered;
  - `rule` and `rule_version` for a voucher the system made (A14);
  - `reverses_voucher_id` (nullable, unique): a voucher is reversed at most once;
  - `posted_at`, `posted_by_user_id` (null for the system).
- `accounting.postings`:
  - `voucher_id`, `line_no`, `account_id`;
  - `amount numeric(14,2)`, **signed**: debit positive, credit negative, never zero;
  - `currency`, `currency_amount`, `exchange_rate` (all null in this phase; A4 keeps the
    columns so SAF-T's shape is not a later migration);
  - `tax_code_id`, `tax_role` (`base` or `tax`), `tax_base` (on a tax line, the base it
    was computed from);
  - `customer_id`, `supplier_id` (at most one), `open_item_id` (A10);
  - `project_id`, `department_id`: plain ids, no foreign keys across schemas;
  - `description`.
- **Rules the database keeps**, by trigger, as Invoices keeps its own:
  - a voucher's postings sum to zero (deferred constraint trigger, checked at commit);
  - vouchers and postings are **never updated or deleted**;
  - `voucher_date` is after `locked_through` and on or after `books_start`.
- **Posted means posted** (R §9.10). There is no draft voucher. The manual journal form
  holds its lines in the browser until *Post*.
- **Attachments**: `accounting.attachments` (`voucher_id`, object key, name, type, size,
  SHA-256), through the storage package. They can be added after posting, never removed.

### A5 — Tax codes

- `accounting.tax_codes`: `code` (the SAF-T standard code, unique), `name`, `direction`
  (`output`, `input`, `none`), `reverse_charge` (boolean), `vat_account_id`,
  `counter_vat_account_id` (reverse charge only), `reported` (false for 0, 7, 20, 21, 22),
  `active`.
- `accounting.tax_code_rates`: dated periods, no overlap, as Invoices has them.
- The whole standard list is seeded at activation with the 2026 rates.
- **Invoices keeps its own codes** (R §9.7). The two meet on the standard code already
  snapshotted on every issued line and VAT summary. A sales document whose standard code
  is unknown here does not post; it waits as an attention item (A15).

### A6 — Manual journals

- `POST /accounting/vouchers`: date, description, lines (account, debit or credit, tax
  code, party, project, department, text), attachments.
- A line with a tax code is entered **gross or net** (a switch on the form); the server
  computes the tax line with docmath's rounding and shows it before posting.
- A manual line on the receivables or payables account must name a party.
- Needs `accounting:post`.

### A7 — Reversal

- `POST /accounting/vouchers/{id}/reverse` with a date and a reason: a new voucher with
  every sign turned, naming the original. Both show the link.
- A system voucher is reversed **only by its source** (a credit note, a removed payment);
  the endpoint refuses it with 409 `voucher_owned_by_source`. Otherwise the books and the
  sub-ledger would disagree.

### A8 — Periods

- Periods are calendar months; fiscal years follow `fiscal_year_start_month`.
- **One lock date**, `locked_through`. `PUT /accounting/lock` moves it to a month end.
  Moving it forward needs `accounting:close`. Moving it **back** is allowed only to
  after the last closed VAT term (D3), needs a reason, and is written to the audit log:
  the law forbids changing closed books, and an accountant who locked a month too early
  still has to be able to fix that.
- **A system voucher whose date falls on or before the lock is booked on the first open
  day**, keeping its `document_date` (R §9.5). A manual voucher is refused instead (409
  `period_locked`).

### A9 — Opening balances

- One voucher of kind `opening`, dated `books_start`, against the `opening` account.
- Receivables and payables lines are **per open document** (party, document number, date,
  due date, amount), so the party ledgers start with real open items.
- For a company already invoicing in Vantigo, the form offers the open invoices at
  `books_start`, read from `SalesJournal` (A12).
- It can be reversed and entered again until the first lock.

### A10 — Party ledgers

- `accounting.open_items`: `kind` (`customer` or `supplier`), the party id, document
  number, dates, the original amount, and the postings that touch it.
- An item's balance is the sum of its postings; it is settled when that is zero.
- Sales items follow Invoices: a payment there names its invoice, so matching is never a
  guess. Supplier items are matched when a payment is registered (C4).

### A11 — The specifications

Every report takes a period, shows when it was produced, and exports CSV:

- **Journal** (*bokføringsspesifikasjon*): every voucher in number order.
- **Account** (*kontospesifikasjon*): opening balance, postings, closing balance.
- **Customers** and **Suppliers**: per party, with open items and an aged balance.
- **VAT** (*merverdiavgiftsspesifikasjon*): D1.
- **Trial balance**, **Profit and loss**, **Balance sheet**, each with a comparison
  period; the last two group by account class, and filter on project and department.

Withdrawals by owners, sales to owners and to senior employees are Account reports on the
accounts the chart seeds for them. Payroll-reportable benefits wait for payroll.

## 3. Delivery 0 and B — sales post themselves

### A12 — The source is a contract; the event makes it prompt

- New contract `contracts.SalesJournal`, provided by Invoices:
  - `Documents(ctx, after Cursor, limit)`: issued invoices and credit notes in issue
    order, each with number, kind, dates, customer, the credited document, **lines**
    (net, line kind, product tax category, project) and **VAT summaries** (standard code,
    base and VAT in NOK);
  - `Payments(ctx, after Cursor, limit)`: registrations and removals in the order they
    happened, each with invoice, date, amount, source and the receiving account;
  - `Charges(ctx, after Cursor, limit)`: charge payments and waivers;
  - `OpenAt(ctx, date)`: open invoices at a date, for A9.
  Cursors are opaque and monotonic. Invoices adds a sequence column where a table has no
  total order today.
- New events, published in the transaction that makes the fact:
  `invoices.document.issued` and `invoices.payment.removed`. Payloads carry ids, numbers,
  amounts and dates, as today's events do.
- Accounting's event handlers do one thing: **wake the catch-up** for that stream.
- Worker `accounting.catchup` (every minute, and on wake): for each stream, read after
  the stored cursor, post each fact in its own transaction, advance the cursor. The unique
  source index (A4) makes a repeat harmless.
- So a handler added late, a pruned event and a dead delivery all end the same way:
  the next sweep posts what is missing. The books may lag by seconds and **never refuse
  an invoice** (R §9.3).

### A13 — What is posted

- Facts dated before `books_start` are skipped, and the cursor moves past them.
- **Invoice:** debit `receivables` (customer, open item) for the total; credit a revenue
  account per line net (A14); credit the tax code's VAT account per VAT summary. Any
  rounding difference between line nets and the summaries' bases goes on the largest
  revenue line, so the voucher follows the document's own totals.
- **Credit note:** the same with the signs turned, on the open item of the invoice it
  credits.
- **Payment:** debit the bank account's ledger account (F1; `bank` before F), credit
  `receivables` on the invoice's item. A Vipps payment debits `clearing_vipps`.
  **Removal:** the reversal, dated the removal.
- **Charges:** a paid reminder fee credits `charge_income`, paid interest
  `interest_income`; both outside VAT (code 6).

### A14 — Posting rules

- `accounting.posting_rules`: `scope` (`sales` or `expense`), a match on **one** of line
  kind, product tax category, expense category or standard tax code, and the account.
- Sales lines resolve in that order of precedence, then fall back to the seeded defaults
  by tax code: taxable sales, exempt sales, sales outside the law, export.
- Each system voucher stores the rule name and version that made it. The reference page
  documents every rule, which is the description the law's § 6 asks for.
- Changing a rule changes future vouchers only.

### A15 — Attention

A source fact that cannot be posted (an unknown tax code, an inactive account) is stored
in `accounting.unposted` with its reason, and **the cursor still advances**: one bad
document must not stop the books. The screen *Needs attention* lists them with *Retry*;
the dashboard card counts them. Fixing the cause and retrying posts it.

### A16 — Write-offs

Invoices has no way to give up a receivable (R §7.1), and a ledger cannot go without.

- New in Invoices: `POST /invoices/{id}/write-off` with a date, a reason and
  `vat_correction` (boolean, for a loss that is *konstatert*, mval. § 4-7). It closes the
  open amount; the invoice shows *Written off*. Needs `invoices:write`. It can be undone
  while the period is open.
- It appears in `SalesJournal.Payments` as its own kind. Accounting posts: debit
  `bad_debt` for the net share (and debit output VAT for the VAT share when corrected),
  credit `receivables`.
- Small differences use the same door with the reason *rounding*; Accounting posts those
  to `rounding`.

## 4. Delivery C — purchases

### A17 — Suppliers

- `accounting.suppliers`: number, name, organisation number, country, address, e-mail,
  bank account, IBAN and BIC, payment terms, default account, default tax code, active.
- Lookup by organisation number in Brønnøysund. The client lives in Customers today, and
  a module may not import another: delivery 0 lifts it to the platform package
  `srv/brreg`, and Customers uses it from there, unchanged in behaviour.
- A supplier who is a person (a sole proprietor) is personal data: Accounting fills the
  personal-data slot the way Customers does, with the retention the law sets (five years
  after the last voucher).

### A18 — Supplier invoices

- `accounting.supplier_invoices`: supplier, kind (`invoice` or `credit_note`), the
  supplier's number, invoice date, due date, total, KID or reference, state.
- `accounting.supplier_invoice_lines`: account, tax code, amount (net or gross as
  entered), project, department, text.
- **States:** `draft` → `posted` → `paid` (derived from the open item). A draft is not a
  voucher and can be edited or deleted. *Post* needs `accounting:post`, an attachment and
  lines that sum to the total.
- **The same supplier and number twice is refused** (409 `duplicate_supplier_invoice`).
- **Posting:** debit each line's account for its net; debit the tax code's VAT account;
  credit `payables` (supplier, open item). A **reverse-charge** code (services from
  abroad, import) posts both sides of the VAT and leaves the payable at the net.
- A mistake after posting is corrected by *Reverse and copy*: the reversal, and a new
  draft with the same lines.
- A separate approval step (someone other than the one who enters) waits for inbound EHF,
  where it earns its place.

### A19 — Paying suppliers

`POST /accounting/supplier-invoices/{id}/payments`: date, amount, bank account. Debit
`payables` on the item, credit the bank. Partial payments are allowed; a payment can be
removed while its period is open, which posts the reversal. Payment files are phase 2.

### A20 — Expenses post

- Delivery C adds to Expenses what B adds to Invoices: a contract
  `contracts.ExpenseJournal` (`Entries`, `Reimbursements`, cursors) and the events
  `expenses.entry.approved` and `expenses.entry.reimbursed` to wake the catch-up.
- An approved entry **paid by the employee**: debit the category's account (a posting
  rule on expense category; unmapped categories fall back to *Other cost*), credit
  `employee_payables`. Reimbursed: debit `employee_payables`, credit the bank.
- An approved entry **paid by the company**: debit the category's account, credit the
  bank.
- **VAT.** An entry has one VAT amount and no code. The rate is inferred from VAT over
  net when it matches a current rate within one øre per hundred kroner; then the input
  code for that rate is used. Otherwise the entry posts gross with no deduction and is
  listed under *Needs attention* as *VAT not deducted*, where an accountant can reverse
  and re-enter it. Mileage and per diem carry no VAT.
- Expenses' own `supplier_invoice` kind keeps working. With Accounting on, the screen
  points to Accounting's supplier invoices for new ones, and existing approved entries
  post like any company-paid entry.
- The project on an entry becomes the posting's project.

## 5. Delivery D — VAT

### A21 — The specification

Per period and tax code: the base and the VAT, split into output, input, reverse charge
and import, with every posting behind each figure one click away. It is computed from
postings by `voucher_date`, never stored.

### A22 — Terms

- `accounting.vat_terms`: year, term number, the period, state (`open`, `closed`), and at
  close: the figures per code (jsonb), the XML, the settlement voucher, who and when; and
  `filed_at`, `filed_reference`, which a person fills in after filing by hand in this
  phase.
- **Close** (`accounting:close`):
  1. refuses while *Needs attention* holds anything dated in the term;
  2. snapshots the figures and builds the return;
  3. posts the settlement voucher on the term's last day: each VAT account's balance for
     the term to `vat_settlement`;
  4. moves `locked_through` to the term's end.
- **Reopen** is allowed until `filed_at` is set: it reverses the settlement voucher and
  moves the lock back. After filing, a correction is posted in the open term, which is
  how a small error is corrected in practice; replacing a filed return is phase 2.
- The payment to Skatteetaten is a bank movement reconciled against `vat_settlement` (F).

### A23 — The return

- Figures laid out as Skatteetaten's return is, per code and specification line.
- The **XML** in the published `mvamelding` v1.0 schema, validated against the XSD in the
  test suite with the published examples as fixtures. In this phase it is a download, and
  the screen says plainly that filing is done at Skatteetaten: the figures are there to
  be typed in or compared.
- `mvaKode` is the tax code's own code; codes with `reported = false` are left out.

## 6. Delivery E — SAF-T

### A24 — Export

- `POST /accounting/saft-exports` for one fiscal year or a period within it; a worker
  writes the file to object storage as a stream and the screen offers the download.
  Needs `accounting:export`; every export is logged with who and what.
- Version **1.40 only** (R §9.12).
- Contents: Header from the seller; accounts with grouping category and code and opening
  and closing balances; customers and suppliers that have postings, with their balance
  account structure; the tax table with standard codes; analysis types for project and
  department; every voucher as a transaction with debit and credit lines and tax
  information.
- The file is named as Skatteetaten recommends and validated against the XSD in the test
  suite. A file over 2 GB is split by period, each part valid alone.
- An account without a grouping exports `NA`, and the export screen warns first.

### A25 — Import

The way in from Fiken, Tripletex or any other system: `POST /accounting/saft-imports`
with a 1.30 or 1.40 file, **only while the ledger has no vouchers**.

- **The chart** is taken from the file, replacing the default: numbers, names, grouping.
  Kinds follow the number's class and are shown for confirmation. System roles are
  proposed by number and confirmed.
- **Suppliers** are created. **Customers** are matched to Customers by organisation
  number, and the unmatched are listed for the user to create or link; Accounting does
  not write another module's records.
- **The opening voucher** is built from the file's closing balances. Open items per
  party come from the file's customer and supplier balances as one item each, since
  SAF-T does not say which documents are open; the user may split them.
- History is **not** imported as vouchers: the old system's file is the record of those
  years, and the law asks the customer to keep it. The file is stored with the import.
- Shown as a preview before anything is written.

## 7. Delivery F — bank

### A26 — Bank accounts

`accounting.bank_accounts`: name, account number, IBAN, the ledger account (one each),
active. The seller's account in Invoices is offered as the first. Payments from Invoices
post to the ledger account whose number matches the receiving account, else to `bank`.

### A27 — Statements

- Import **camt.053** and **CSV** (a column mapping saved per bank account) into
  `accounting.statement_lines`: date, amount, text, reference, KID, counterparty, the
  bank's own id. A line already imported is skipped, by bank id or by a hash of its
  fields.
- Opening and closing balances from camt.053 are kept per statement.

### A28 — Reconciliation

Each line ends **matched** to postings on its ledger account, or stays open.

- **Suggested** matches, most certain first: an existing posting with the same amount and
  date window; a batch of Invoices payments whose sum is the line (how KID payments
  arrive on a statement); a supplier open item with that amount or KID; a rule the user
  saved on text (bank fees to an account).
- Accepting a suggestion that names an open item **registers the payment** (A19).
  *Code it* posts a bank voucher with the accounts the user gives. A customer payment
  not yet in Invoices is registered there, through the link, never here.
- A match can be undone while the period is open.
- The screen's foot shows the statement balance, the ledger balance and the difference;
  a month can be locked only knowingly when they differ.

### A29 — The bank feed

The bank feed designed for Invoices (`specs/2026-11-13-bank-feed-folio-design.md`, Folio
the first bank) reads incoming payments through the port `internal/bankfeed` and stores
nothing else: debits, card purchases and internal transfers leave only a count (its F5).
A ledger needs exactly those, so the statement gets a third source beside the two files.

- **One connection, owned by Invoices** (its F2, F3). Accounting does not hold a second
  key. Invoices provides `contracts.BankFeed`: `Accounts` and `Transactions(account, from,
  to)`, answering what the port answers, every direction and kind. This settles, for
  Accounting, the shared-credential question that design left for later.
- A bank account (A26) whose number is one of the feed's accounts can be switched to
  **Feed**, with a start day. Worker `accounting.bankfeed` then reads it on the feed's
  own interval, with the same ten-day overlap, into `statement_lines`, the bank's
  transaction id as the line's id. Card and tax accounts are offered too: here they are
  ledger accounts like any other.
- Reconciliation (A28) is unchanged. A credit the feed already registered as a customer
  payment in Invoices arrives here as a statement line whose first suggestion is that
  payment's posting, by the shared transaction id, so it is matched without a guess.
- An internal transfer between two of the company's accounts is suggested as one voucher
  between their ledger accounts, matched on both statements.
- This part of F **waits for the feed's own delivery A**; files work without it.

## 8. Data model summary

Schema `accounting`: `settings`, `counters`, `accounts`, `account_groupings`,
`tax_codes`, `tax_code_rates`, `vouchers`, `postings`, `attachments`, `open_items`,
`posting_rules`, `cursors`, `unposted`, `suppliers`, `supplier_invoices`,
`supplier_invoice_lines`, `vat_terms`, `saft_exports`, `saft_imports`, `bank_accounts`,
`statements`, `statement_lines`, `statement_matches`, `bank_rules`.

Other modules: Invoices gains the write-off, two events, a sequence for its journal
streams and the `SalesJournal` provider; Expenses gains two events and the
`ExpenseJournal` provider; Customers uses `srv/brreg`.

## 9. Edge cases

- **Accounting switched on years after Invoices.** `books_start` decides; older documents
  are skipped; open ones enter through the opening voucher.
- **Accounting switched off and on.** Cursors stay; the sweep posts what was missed.
- **A credit note for an invoice before `books_start`.** It posts against the opening
  item for that invoice.
- **A payment removed after its period locked.** The reversal is booked on the first open
  day.
- **A VAT rate change.** New rate periods on the tax codes; documents carry their own
  rate, so nothing is recomputed.
- **Two sweeps at once.** The cursor row is locked for the sweep; the unique source index
  is the second guard.
- **The seller is not VAT-registered.** `vat_term = none`; sales arrive with code 7;
  input VAT is not deducted; D is hidden.
- **A customer merge in Customers.** Postings keep the old id as history; the party
  reports follow the merge through Customers' reference contract.
- **Deleting a person's data.** Vouchers are kept for five years by law; the erasure
  ledger records the hold, as Invoices does for issued documents.

## 10. Testing

- Database rules (balance, immutability, lock) by tests that try to break them.
- Each posting kind as a table test: the source fact in, the voucher out, to the øre.
- Catch-up: repeated delivery, a late handler, a failing fact that does not block.
- The integration test joins `everyModule()` and runs Invoices → voucher → VAT term →
  SAF-T on one database.
- SAF-T and the VAT return validated against the published XSDs; a golden file each.
- camt.053 fixtures from the published examples of two banks.

## 11. Documentation and the public site

- **User guide**, English and Norwegian: `user/accounting.md` — setting up the books, the
  chart, journals, supplier invoices, the VAT term, reports, SAF-T, bank, inviting an
  accountant, coming from another system. `user/invoices.md` gains the write-off;
  `user/expenses.md` what posting changes.
- **Administration**, both languages: where books may be stored and the yearly backup
  test (bokføringsforskriften §§ 7-2, 7-5), in `admin/installation.md`; and, with phase 2,
  a page on a self-hoster's own Maskinporten client.
- **Reference**: `reference/accounting.md` with the model, every rule and its version,
  permissions, workers and endpoints; `reference/invoices.md`, `reference/expenses.md`
  and `contributing/platform-packages.md` (the new contracts, `srv/brreg`).
- **The public site**: `vantigo.io/apps/accounting`, from the user guide, in the landing
  repository, with the last delivery.

## 12. Later, and what each waits for

- **MVA-melding submission, Frister, Krav og betalinger**: our registration as a system
  vendor and an ID-porten integration (R §3.4). The return's XML is already built.
- **Inbound EHF**: the access point port gains receiving; it lands as a draft supplier
  invoice, and approval by a second person arrives with it. Required by 2030.
- **More banks and payment files**: Folio comes with the bank feed (A29); for the other
  banks a quote from Aritma. Paying suppliers: pain.001 from open supplier items, and for
  Folio a draft payment the person signs with BankID, with the voucher's attachment sent
  along (that design's R7).
- **Year-end**: closing entries, aksjonærregisteroppgaven, årsregnskap. No skattemelding.
- **Several legal entities**: the roadmap item; every table here would gain an entity.
- **Foreign currency**: Invoices issues NOK only today; the posting columns are ready.
- **Stock valuation**: Inventory carries no cost; until then stock is a period-end
  voucher (R §9.6).
- **Fixed assets and the VAT adjustment rules; payroll.**

## 13. Revision 2 — the critic review

A review against the code returned *revise*: the spec had assumed the source tables are
append-only, and they are not. Each amendment names the decision it changes.

### R1 — A fact log orders what is not a document (A12)

- **Documents** are read in `number` order. The number comes from a counter row held to
  commit, so numbers become visible in order and a cursor on them never skips. Delivery 0
  confirms that every issue path takes that counter.
- **Payments, charge payments, removals and write-offs** have no such order: ids are
  handed out before commit, and a removal is an update. Invoices gains
  `invoices.journal_facts`: `seq`, `kind` (`payment`, `payment_removed`, `charge_payment`,
  `charge_payment_removed`, `write_off`, `write_off_undone`), the row it is about, the
  business date. It is append-only. Rows are written by **deferred constraint triggers**
  on those tables, and `seq` is taken from `invoices.counters('journal')` **at commit**, so
  the counter is held for an instant and a bank line paying several invoices cannot
  deadlock on it.
- `SalesJournal.Payments` and `Charges` become one `Facts(after seq, limit)`.
- Waivers are not facts: a waiver posts nothing.

### R2 — Expenses needs the same, with occurrences (A20)

- Expenses can unapprove an entry and undo a reimbursement, and trips are approved per
  claim. `expenses.journal_facts` records `approved`, `unapproved`, `reimbursed`,
  `reimbursement_undone`, for an entry or a claim, sequenced the same way. The events are
  `expenses.entry.*` and `expenses.claim.*`.
- A voucher's `source_part` is the fact's `seq`, so approve, unapprove, approve gives three
  vouchers: the cost, its reversal, the cost again.
- **An entry not in NOK** is not posted by itself, since no rate is stored. It waits under
  *Needs attention*, where the amount in NOK is entered, normally from the bank statement.
- The voucher is dated the entry's date; a reimbursement its own date.
- **A category rule carries `vat_deductible`.** VAT is inferred and deducted only for a
  category that allows it (mval. § 8-3 forbids it for entertainment and welfare).
- **Expenses' own `supplier_invoice` kind is not posted by itself.** It has a free-text
  supplier and no payment. An approved one waits under *Needs attention* with *Make a
  supplier invoice*, which opens a draft (A18) with its figures and attachment.

### R3 — Zero is not a posting (A4, A13)

Exempt summaries have a VAT of zero, and a line may be free. Zero lines are left out; the
tax code rides on the base posting. A document whose every line is zero posts no voucher,
and the cursor moves on.

### R4 — Write-offs that balance (A16)

- **Without the VAT correction:** debit `bad_debt` for the whole open amount.
- **With it:** the open amount is split over the invoice's VAT summaries in proportion;
  each share's VAT is debited to that code's VAT account, the rest to `bad_debt`. The tax
  postings carry the original code and the specification `tapPåKrav`, which the return
  has a line for (A23).
- **The undo is always allowed.** Invoices cannot see the ledger's lock and does not need
  to: the reversing voucher follows A8.
- **Money that arrives after a write-off** is registered in Invoices as a payment of kind
  *recovered*, allowed up to the amount written off. It credits `bad_debt`, and turns the
  VAT correction back in the same proportion.

### R5 — Open items that can always be settled (A9, A10, A19, A25, A28)

- An open item carries `source_document_id` when it stands for a document in Invoices, so
  a later payment finds it. The opening form fills it for invoices read from `OpenAt`.
- A posting on receivables or payables that names no item lands on **the party's
  unallocated item**, one per party and kind.
- `POST /accounting/open-items/{id}/settlements`: a receipt, a payment or a refund against
  any item, with a date and a bank account. It is how an imported balance is paid, how a
  credit balance is refunded, and what *Code it* uses when the line belongs to a party.
  For an item with a `source_document_id` it is refused: that payment belongs in
  Invoices.
- A payment registered after activation for an invoice in the books is always posted; a
  date before `books_start` is booked on `books_start`.

### R6 — Lines remember what they were (A12, A14)

Invoice lines carry no line kind, tax category or project today. Delivery 0 snapshots
`line_kind`, `tax_category_id` and `project_id` on each line at issue. Lines issued
before that fall back to the default for their tax code.

### R7 — A request, a lease, no transaction held across a read (A12)

- A handler cannot wake a worker, and may not read a contract on its delivery
  transaction. It upserts `accounting.catchup_requests(stream)`.
- Worker `accounting.catchup` runs every 15 seconds: it claims a stream's lease in one
  short transaction; reads the contract with **no transaction open**; posts each fact in
  its own transaction, which also advances the cursor; releases the lease. With no
  request it still sweeps every five minutes.
- Nothing runs until Accounting is set up (R12).

### R8 — Purchases in another currency (A18)

- A supplier invoice has `currency`, `exchange_rate` and the total in NOK; the form
  proposes nothing and the person enters the rate or the NOK total. Postings carry the
  currency amount beside the NOK amount.
- A payment is entered in NOK as the bank took it. The difference to the item goes to the
  new roles `currency_gain` or `currency_loss`.
- **Import of goods:** the VAT base is the customs declaration's, not the invoice's. It
  is a manual journal with the import codes, and the user guide shows the entry. The
  supplier invoice itself is coded without VAT treatment.

### R9 — Charges (A13)

Fees, compensation and interest are not turnover: they post with code **7**, which the
return leaves out, not code 6. Compensation credits `charge_income` as the reminder fee
does. A removed charge payment posts the reversal.

### R14 — After the review of delivery 0 (A3, A12, A13, R5, R9)

- **Charges post as cash received, on one account.** A charge payment is not split into
  fee, compensation and interest: what a payment paid depends on letters and waivers that
  can still change after the ledger has read it. Every charge payment credits
  `charge_income`, code 7, and its removal reverses it. `interest_income` is not a role.
- **`OpenAt` answers with `AsOfSeq`**, the highest fact its snapshot reflects, and is
  paged by number. Set-up stores it. A fact at or before it is posted only when its date
  is on or after `books_start`; a fact after it is always posted, a date before
  `books_start` booked on `books_start`. That is what keeps a late or backdated payment
  from being counted both in the opening balance and as a voucher.
- **The fact log's backfill is in event order**: registrations and removals interleaved
  by when they happened.

### R10 — The lock is read under a lock (A8, A22)

- The posting trigger reads `settings` `FOR SHARE`; moving the lock, closing and
  reopening a term take it `FOR UPDATE`.
- **Close** needs the lock to be before the term's end, and says so otherwise; the
  settlement voucher is posted first, then the lock moves.
- **Reopen** moves the lock back to the day before the term's start of settlement, then
  posts the reversal on the term's last day. A8's limit reads: not behind the last term
  that is *filed*.
- Close also refuses when the VAT accounts' movement in the term differs from the
  return's VAT, which a manual posting straight onto a VAT account causes; the screen
  lists the postings.
- `vat_term` in settings decides whether there are terms. When it disagrees with the
  seller's VAT registration in Invoices, the Overview says so.

### R11 — Vipps, and what Invoices already imports (A13, A27, A28)

- **Vipps** pays out net of its fees. Reconciliation gains the suggestion *Vipps payout*:
  the line's amount from `clearing_vipps` to the bank, the remainder of the clearing
  balance up to that day to the new role `payment_fees`, shown before it is posted.
- **Invoices keeps its own imports** (OCR giro, camt.054, and the feed): they register
  customer payments. Accounting's statement is the account's whole movement. A statement
  line with a KID, or one that sums a batch, is never coded here as income: its
  suggestions are the payments' postings, and when there are none, a link to Invoices'
  exception queue.
- A manual or Vipps payment names no receiving account: it posts to `bank` or
  `clearing_vipps`. A removal is dated the business day of its timestamp, in the
  workspace's business time zone.

### R12 — Setting up (A2, A3)

Every known module is on by default, so an upgrade switches Accounting on. It does
nothing until a person with `accounting:close` completes **Set up the books**:
`books_start`, the fiscal year, the VAT term, and the chart (the default, or a SAF-T
file). That writes the settings row and seeds accounts, tax codes and roles. Before it,
the handlers and workers return at once and the app shows only the setup.

### R13 — Smaller amendments

- **The source index** (A4) is partial, `WHERE source_module IS NOT NULL`, and
  `source_part` is `NOT NULL DEFAULT ''`: nulls would let a duplicate through.
- **Deferred constraint triggers** are new to this repository. The balance check is one;
  a second refuses a voucher with no postings.
- **The department dimension is dropped from this phase** (A4, A11, A24). Departments are
  Staff's, with no contract to read them. Project is the one dimension, through the
  Projects contract; `department_id` is not created.
- **Names in the default chart** (A3) follow Skatteetaten's published næringsspesifikasjon
  list, which carries a Norwegian and an English name per four-digit code. The grouping
  is then the account's own number by default.
- **The balance sheet** shows *Result for the year, not yet closed* as a computed line in
  equity; `year_result` is used by the year-end closing in phase 3.
- **Copy a voucher** opens the journal form with a posted manual voucher's lines, for the
  monthly entries (pay from a payroll system, rent) a ledger without payroll needs.
- **Brønnøysund** (A17): the client and the entity decoding move to `srv/brreg` without
  Customers' generated types; the feed worker and the registry records stay.

### What the review suggested and this revision keeps anyway

The VAT return's XML, the SAF-T import and the CSV statement were proposed as cuts. They
stay: the XML is what phase 2 submits, the import is how a company arrives from Fiken or
Tripletex, and the smaller banks export only CSV.
