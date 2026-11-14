---
title: Accounting
description: The Accounting app — setting up the books, the chart of accounts with its grouping codes and system accounts, the tax codes and their rates, posting a journal entry net or gross with its VAT lines, copying a voucher, attaching files, reversing a voucher, locking a period and moving the lock back, the opening balances and Invoices' open invoices, how invoices, payments, charges and write-offs post themselves, the posting rules, Needs attention and Retry, open items and registering a receipt, payment or refund, the reports and their CSV, and inviting an accountant.
sidebar:
  order: 67
sources:
  - apps/accounting/frontend
  - apps/host/frontend/src/routes/accounting
---

The Accounting app keeps the company's books as the Norwegian Bookkeeping Act
(bokføringsloven) asks for them. In Norwegian the app is **Regnskap**. Every entry in the
books is a **voucher** (bilag): a numbered record, dated, with its **postings** — the
amounts on each account, debit or credit, that together sum to zero. A voucher is never
changed or deleted once it is posted; a mistake is corrected by **reversing** it. The
**chart of accounts** lists the accounts the books post to. **Open items** are what each
customer owes, document by document. The **lock date** closes the months behind it, and
the **reports** are the specifications the law asks a company to be able to produce.

Accounting needs [Invoices](/en/user/invoices/) switched on. You post journal entries by
hand and enter the opening balances; what Invoices issues and is paid — invoices, credit
notes, payments, reminder charges and write-offs — posts itself
([How sales arrive](#how-sales-arrive-from-invoices)). Purchases, VAT returns, SAF-T and the
bank follow with the next releases. The rules behind every screen — what is refused and why, the status codes and
the permission each request is checked against — are in the
[Accounting module reference](/en/reference/accounting/).

## The sidebar

| Entry | What it is |
| --- | --- |
| **Overview** | Whether the books are set up, the vouchers of the fiscal year, the open customer items, the lock date, what needs attention and shortcuts. |
| **New journal entry** | The journal entry form. Shown to those who may post vouchers. |
| **Vouchers** | Every voucher, newest first. |
| **Needs attention** | The sales the books could not post, with why and **Retry**. |
| **Open items** | What customers owe, document by document. |
| **Reports** | The journal, an account, customers, suppliers, the trial balance, profit and loss and the balance sheet. |
| **Chart of accounts** | The accounts, their grouping codes and the system accounts. |
| **Tax codes** | The standard VAT codes, their accounts and their rates. |
| **Posting rules** | Which revenue account each sales line goes to. |
| **Settings** | The fiscal settings and the lock date. |

## Setting up the books

Until the books are set up, the app shows only the way there: **Overview** says *The books
are not set up*, and every other page says the same. A person with **Manage the books**
(`accounting:close`) clicks **Set up the books** and chooses, once:

1. **The books start on** — the first day the books cover. Nothing dated before it is
   posted. A company that has kept its books elsewhere until now chooses the first day of a
   month after its last closed period there. The earliest is 1 January 2022: the VAT rates
   the books start with are known from then.
2. **The fiscal year starts in** — January for most companies.
3. **VAT term** — *Bimonthly* for most, *Yearly* for a company the tax office allows it,
   *Not VAT-registered* for a company outside the VAT register.
4. **Names of the chart** — whether the chart of accounts and the tax codes are named in
   Norwegian or in English. They can be renamed one by one later.

**Set up the books** makes the default chart of accounts, Skatteetaten's standard tax
codes with their rates — 25, 15, 12 and 11.11 %, unchanged since 2022 — and the system
accounts, and opens **Opening balances**. The
four choices cannot be changed afterwards; **Settings** shows them.

## The chart of accounts

**Chart of accounts** lists every account by number with its kind (asset, liability,
equity, income or expense), its default tax code, its grouping code and, for the accounts
the books post to by themselves, its system role. Search by number or name. Click an
account to open it.

The default chart has about 130 accounts in the numbering Norwegian accountants know:
1500 accounts receivable, 1920 bank, 2400 accounts payable, 2700–2740 VAT, 3000 sales,
4005 cost of goods, 5000 pay, 6000–7000 other costs, 8000 finance. Their names and grouping
codes come from Skatteetaten's list for the tax return's business specification
(næringsspesifikasjonen).

- **New account** adds one: a number of 4 to 6 digits that no other account has, a name, a
  kind, an optional default tax code and a grouping code. The kind follows the number's
  class, and the form fills it in as you type: 1 asset, 20 equity, 21–29 liability, 3
  income, 4–7 expense, and 8 income or expense — choose which. A number begins with 1 to
  8; another kind than its class's is refused.
- **Grouping code** is the code SAF-T reports the account under, picked from Skatteetaten's
  list for the year. An account without one is reported as *NA*.
- An account's **name** can always change. Its **number** and **kind** are fixed once it
  has a posting, and such an account cannot be deleted — make it inactive instead. An
  inactive account takes no new postings.
- **System accounts** are the ones the books post to by themselves: accounts receivable,
  accounts payable, the bank, the VAT settlement, rounding, bad debt, what is owed to
  employees, the opening balance, the result for the year, Vipps clearing, fees and
  interest, currency gain and loss, and payment fees. **Make it a system account** on an
  account moves a role to it; the role leaves the account that had it. A role moves only
  to an account of its kind — the bank to an asset account, the VAT settlement to a
  liability, rounding to an expense — and accounts receivable and accounts payable stay on
  their account once it has postings, since what each customer and supplier owes is those
  postings. A role is never removed, so an account holding one cannot be deleted or made
  inactive until its role has moved.

Changing the chart needs **Manage the books**.

## Tax codes

**Tax codes** lists Skatteetaten's standard codes with their direction (output, input or
none), the rate valid today, the VAT account they post to and whether the VAT return
reports them — codes 0, 7, 20, 21 and 22 are not reported. Reverse-charge codes (services
bought abroad, imports) post to two accounts: the deductible VAT and the calculated VAT.

Click a code to change its name, its VAT account (and, for reverse charge, its counter
account) or whether it is offered. **Add rate** starts a new rate from a date; the current
rate ends the day before. A voucher takes the rate valid on its own date.

## Posting a journal entry

**New journal entry** is the form for anything the books do not post by themselves: pay
from a payroll system, rent, a loan, a correction.

1. Choose the **Voucher date** and write a **Description**.
2. On each line choose the **Account** and type the amount under **Debit** or **Credit**.
   The account's default tax code is filled in, and follows the account when you choose
   another — until you choose a tax code yourself, which then stays.
3. A line on accounts receivable names its **Customer**. A line with a **Project** shows in
   the project's figures, when Projects is on.
4. Choose **Net** or **Gross**. With *Net* the amount on a line is without VAT, and the VAT
   is added on its own line; with *Gross* the amount includes the VAT, and the line is split
   into the net and the VAT. A reverse-charge code (services bought abroad, imports) is
   always entered net: the invoice carries no Norwegian VAT, so *Gross* with one is
   refused.
5. **Postings** shows, while you type, exactly what will be posted: every line, the VAT
   lines the server computes, and whether the voucher balances.
6. Add the receipt or the document under **Attachments**, then **Post**. A file over 20 MB,
   or any file when the installation has no file storage, is pointed out before you post,
   and **Post** waits until it is removed.

The voucher is numbered in its fiscal year without gaps (2026-000123) and opens. There is
no draft: posted means posted. A voucher dated on or before the lock date, or before the
books start, is refused. If a file cannot be kept after the voucher is posted, the voucher
opens anyway with a notice naming the file: attach it again there with **Attach a file**.

The VAT is computed line by line and rounded to the øre, half away from zero: 1 000,00 net
at 25 % gives 250,00; 1 000,00 gross at 25 % gives 800,00 and 200,00.

**Copy from a voucher.** For the entries that come back every month, open a voucher you
posted by hand and click **Copy**. The form opens with its lines, entered net — the VAT
lines are computed again — so you change the date and the amounts and post.

Posting needs **Post vouchers** (`accounting:post`).

## Vouchers

**Vouchers** lists every voucher, newest first. Filter by period and kind, or search for a
text or a number. Click one to open it.

A voucher's page shows its dates — the **Voucher date** it is booked on and the **Document
date** of what it records — its kind, when it was posted, every posting with its account,
text, tax code, customer and project, debit and credit, and its attachments. **Attach a
file** adds one; an attachment is never removed. A voucher the system posted for something
another app recorded says so, names its source and the rule that made it with its version,
and links to the invoice in Invoices it belongs to.

## Reversing a voucher

A mistake is never edited away. On the voucher click **Reverse**, give the date and the
reason, and **Reverse**: a new voucher with every sign turned is posted, naming the
original, and both show the link — *Reverses* and *Reversed by*. A voucher is reversed
only once, on the same accounts, even one made inactive since. Then post the right one,
with **Copy** if it helps.

A voucher the system posted cannot be reversed here: its source reverses it — a credit
note in Invoices, a payment removed there. Otherwise the books and the other app would
disagree.

## Locking a period

**Settings** shows the **Lock date**: every voucher on or before it is final. To lock,
choose the month under **Lock through the end of** and click **Lock**. Lock a month once
its vouchers are in and checked — at the latest when its VAT term is reported. The lock
reaches at most the end of the current month.

A month locked too early can be opened again: choose an earlier month, give the
**Reason**, and click **Move the lock back** (or **Lift the lock**). Every move is kept in
the lock's history with who made it and why, because opening closed books is something the
law asks to be accounted for.

While a month is locked, a journal entry dated in it is refused. What the system posts by
itself for a locked day is booked on the first open day, keeping its document date.

Moving the lock needs **Manage the books**.

## Opening balances

**Opening balances**, opened after setup and from the overview, enters the balances on
the day the books start, as one voucher dated that day.

1. Type each balance: the account and the amount under **Debit** or **Credit** — the bank,
   the share capital, loans, fixed assets.
2. Enter what customers owe **one line per open document** on accounts receivable, with the
   customer, the document number, its date and due date, so each becomes an open item.
3. When the company already invoices in Vantigo, **Invoices has … open invoices at the
   books' start** offers them: **Add the open invoices** adds a line per invoice with its
   customer, number, dates and the amount still open the day before the books start.
4. What the lines do not balance goes to the opening balance account; the form shows how
   much. **Post the opening balances**.

An invoice added from Invoices keeps its customer and the amount open on that day; a line
that names it with another customer or amount is refused.

The opening balances can be reversed from their voucher and entered again until the first
month is locked; after that they are final, even if the lock is later moved back or
lifted. Entering them, and reversing them, needs **Manage the books**: someone who may only
post vouchers sees no **Reverse** on the opening voucher.

## How sales arrive from Invoices

Once the books are set up, everything Invoices issues and is paid posts itself, a voucher
for each, usually within seconds and at the latest within five minutes. Nobody types them,
and an invoice is never held back because of the books: if something cannot be posted, it
waits under [Needs attention](#needs-attention) while the rest carries on.

| In Invoices | The voucher |
| --- | --- |
| An invoice is issued | Accounts receivable debited for the total on the invoice's own open item; each line's amount credited to its revenue account ([Posting rules](#posting-rules)); each VAT rate's VAT credited to its tax code's VAT account. A line of zero is left out, and an invoice of nothing but zeros posts nothing. |
| A credit note is issued | The same with every sign turned, on the open item of the invoice it credits. |
| A payment is registered | The bank account debited — Vipps clearing for a Vipps payment — and accounts receivable credited on the invoice's item. |
| A payment is removed | The same turned back, dated the day it was removed. |
| Reminder charges are paid | The bank debited and the charge income account credited, with tax code 7 (no VAT): fees, compensation and interest are not turnover. A removal turns it back. |
| An invoice is written off | Bad debts debited and accounts receivable credited for what was open; with the VAT taken back, the VAT share goes back on each rate's VAT account, marked for the VAT return — shared over what is left of the invoice once its credit notes are taken off, so a rate credited away takes no VAT back. A rounding goes to the rounding account. Undoing it turns the voucher posted for it back, line for line, even if an account has changed since. |
| Money arrives after a write-off | The bank debited and bad debts credited — and the VAT taken back, in proportion, returned. Several part payments that together pay what was written off give back exactly what it took, to the øre; an øre of rounding falls on bad debts, never on VAT. |

How [writing off](/en/user/invoices/#writing-an-invoice-off) works in Invoices is on its own page.

The books' start and the lock decide the date:

- An invoice issued before the books start is not posted: it belongs in the opening
  balances.
- A payment of an invoice from before the books start posts on that invoice's opening
  item. One dated before the books start but registered after you entered the opening
  balances is booked on the books' start, so it is counted once; one the opening balances
  already reflect is not posted again.
- A voucher dated in a locked month is booked on the first open day, keeping its own date
  as the document date.

The overview's **Sales from Invoices** says how far the books have read and whether
anything is waiting. A long backlog — after the books were set up on a busy installation,
say — keeps reading every fifteen seconds until it is through.

## Posting rules

**Posting rules** decide which revenue account each invoice line is credited to. A rule
matches on one thing, and the first that matches wins:

1. the line's **kind** — hours, expenses, milestones, energy settlements, order lines,
   subscription periods, a-konto deductions, lines typed by hand;
2. the product's **tax category**, for a line from an order;
3. the line's **tax code**;
4. the **default** rule of the tax code, set up with the books: taxable sales to 3000
   (3010 middle rate, 3020 low rate), exempt sales to 3100, export to 3150, domestic
   reverse charge to 3160, sales outside the VAT Act to 3200.

**Add a rule** with its name, what it matches and the account — an income account: the
picker offers no other. Click a rule's name to change its name or account: either makes a
new **version** of the rule, and only vouchers posted after it follow it — what is posted
stays as it was. Each revenue line of a voucher names the rule and the version that chose
its account, under the name the rule had then. **Archive** takes a rule out of use: it no
longer matches, its name and match are free for a new rule, and vouchers it placed still
name it. A default rule is never archived; point it at another account instead.

Changing the rules needs **Manage the books**.

## Needs attention

**Needs attention** lists every sale the books could not post, with why: a tax code the
books do not have, or that is inactive; an account that is inactive, or a rule pointing at
an account that is not an income account; a kind of fact this version does not know. The
overview and the dashboard count them.

What comes after it for the same invoice waits behind it, so the books never take a
payment before the invoice it pays: such a line says it **waits for an earlier sale of the
same invoice**.

Fix the cause — activate the account, add the tax code, change the rule — and click
**Retry** on the line: it reads the fact again from Invoices and posts it, and the line
then shows its voucher. When it posts, what waited behind it posts too, in order. Retrying
a line that waits keeps it waiting: retry the one it waits for. A fact is never dismissed.
**Show those posted since** shows the ones a retry has posted.

Retrying needs **Post vouchers**.


**Open items** lists, per customer, every document not yet settled: its number, dates, the
original amount and the balance, 200 at a time with **Previous** and **Next**. **Show
settled** shows the settled ones too. A posting on
accounts receivable that names no document lands on the customer's **Unallocated** item.

**Register** on an item records money moved: a receipt from the customer, or a refund of a
credit balance. Give the date, the amount (at most the balance) and, when it is not the
bank account, the account the money moved through. The voucher is posted at once.

An item marked **In Invoices** stands for an invoice there: its payment is registered on
the invoice in Invoices, never here.

Registering needs **Post vouchers**.

## Reports

**Reports** produces the specifications for any period, each with when it was produced:

- **Journal** (bokføringsspesifikasjon) — every voucher's postings in number order.
- **Account** (kontospesifikasjon) — one account's opening balance, its postings and its
  closing balance.
- **Customers** and **Suppliers** — per party, the items with a balance or a movement in the
  period, and the balance aged by due date: not due, 1–30, 31–60, 61–90 and over 90 days.
  An item for an invoice links to it in Invoices.
- **Trial balance** (saldobalanse), **Profit and loss** and **Balance sheet**. **Compare
  with the year before** adds the same period a year earlier (29 February compares with 28
  February). The balance sheet shows the result not yet closed to equity as two lines in
  equity: *Earlier years' result, not yet closed* and *Result for the year, not yet
  closed*. Every account is on the side of its kind; one outside the usual number groups
  is under *Other assets*, *Other liabilities* or *Other income and costs*, so the
  balance sheet always balances.

The period starts on the first day of the current fiscal year. Choose the report, the
period and, for **Account**, the account; with Projects on, a **Project** limits it to that
project's postings. A journal or an account with more than 50 000 postings in the period is
not shown cut short: choose a shorter period. **Download CSV** gives the file a
spreadsheet opens directly: semicolons and the decimal comma. Downloading needs **Export
the books** (`accounting:export`), because the files carry customers' names.

## Inviting an accountant

An accountant is an ordinary user. Invite them under
[Workspace administration](/en/user/workspace-administration/) and give them a role with
the permissions they need:

| Permission | What it allows |
| --- | --- |
| **Use Accounting** (`accounting:read`) | Every screen and report. |
| **Post vouchers** (`accounting:post`) | Journal entries, reversals, attachments, settlements and **Retry** under Needs attention. |
| **Manage the books** (`accounting:close`) | Setting up the books, the chart, the tax codes, the posting rules, the opening balances and the lock. Only an Owner can give it. |
| **Export the books** (`accounting:export`) | The reports as CSV. |

When the engagement ends, remove the role or disable the user: everything they posted
stays in the books under their name.
