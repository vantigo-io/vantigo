---
title: Accounting
description: The Accounting app — setting up the books, the chart of accounts with its grouping codes and system accounts, the tax codes and their rates, posting a journal entry net or gross with its VAT lines, copying a voucher, attaching files, reversing a voucher, locking a period and moving the lock back, the opening balances and Invoices' open invoices, how invoices, payments, charges and write-offs post themselves, suppliers and the Brønnøysund lookup, entering, posting and paying a supplier invoice, another currency, reverse charge and import of goods, credit notes, Reverse and copy, how expenses arrive from Expenses, the posting rules for sales and expense categories, Needs attention with Retry, the amount in NOK and Make a supplier invoice, open items and registering a receipt, payment or refund, the reports and their CSV, and inviting an accountant.
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
([How sales arrive](#how-sales-arrive-from-invoices)), and so does what
[Expenses](/en/user/expenses/) approves and reimburses
([How expenses arrive](#how-expenses-arrive-from-expenses)). You enter what suppliers bill
as [supplier invoices](#supplier-invoices) and register their payments. VAT returns, SAF-T
and the bank follow with the next releases. The rules behind every screen — what is refused and why, the status codes and
the permission each request is checked against — are in the
[Accounting module reference](/en/reference/accounting/).

## The sidebar

| Entry | What it is |
| --- | --- |
| **Overview** | Whether the books are set up, the vouchers of the fiscal year, the open customer items, the lock date, what needs attention and shortcuts. |
| **New journal entry** | The journal entry form. Shown to those who may post vouchers. |
| **Vouchers** | Every voucher, newest first. |
| **Needs attention** | What the books could not post, with why and what to do: **Retry**, the amount in NOK, **Make a supplier invoice**; and what posted but you should know. |
| **Supplier invoices** | Invoices and credit notes from suppliers: drafts, posted, paid, reversed. |
| **Suppliers** | The companies and people you buy from. |
| **Open items** | What customers owe and what is owed to suppliers, document by document. |
| **Reports** | The journal, an account, customers, suppliers, the trial balance, profit and loss and the balance sheet. |
| **Chart of accounts** | The accounts, their grouping codes and the system accounts. |
| **Tax codes** | The standard VAT codes, their accounts and their rates. |
| **Posting rules** | Which revenue account each sales line goes to, and which cost account each expense category goes to. |
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

## Suppliers

**Suppliers** lists the companies and people you buy from, by number: each new supplier
takes the next number. **New supplier** opens the form. Type an organisation number (nine
digits) or a name in **Look up in Brønnøysundregisteret** and pick the entity: its name,
organisation number and address fill in. The rest is yours to give: the e-mail, the bank
account (eleven digits — checked, so a mistyped one is refused), or an IBAN and BIC for a
foreign one, the payment terms in days, the currency its invoices come in, and the account
and tax code a new invoice line proposes.

An organisation number belongs to one supplier per country: the same number twice in
Norway is refused, while a foreign entity may share it.

A supplier the books name — with a voucher or an invoice — is never deleted: clear
**Active** instead, and it is no longer offered for new invoices. One that is not yet in the
books has **Delete**.

**The supplier is a person** marks a sole proprietor: their record is personal data. The
bookkeeping keeps it — like every voucher — for five years after the end of the fiscal year
of their last voucher, and the supplier's page says until when. **Export the supplier's
data** downloads everything the books hold about them — the record, their invoices with
lines and payments, and every posting naming them — to answer a person's request for their
data. It needs **Export the books**.

Once that date has passed, the person's page offers **Anonymise**: the name becomes
*Anonymised supplier* and its number, the organisation number, address, e-mail and bank
details are cleared, and the supplier is made inactive. The vouchers stay as they are and
still name the supplier by its number. It cannot be undone, and an anonymised supplier is
not changed again. Before the date it is refused: the bookkeeping still needs the record.
A company is not anonymised — its record is not personal data.

Keeping the suppliers needs **Post vouchers**.

## Supplier invoices

**Supplier invoices** lists what suppliers have billed, newest first, filtered by state,
supplier and due date, or **Overdue only**. An invoice is a **Draft** until it is posted,
then **Posted** while something is owed, **Paid** when nothing is left, and **Reversed**
once corrected by Reverse and copy.

### Entering and posting a supplier invoice

1. Click **New supplier invoice**. Choose the **Supplier** (the **+** beside it adds a new
   one), **Invoice** or **Credit note**, and type the **Supplier's number** — the number on
   the invoice. The same supplier's number twice is refused: the form warns as you type,
   with a link to the one already entered.
2. Give the **Invoice date**, the **Due date** (empty is the supplier's payment terms after
   the invoice date), the **KID** if the invoice has one, and the **Total** as the invoice
   states it.
3. Add a line per account: the **Account**, the **Tax code**, the amount as **Gross** (with
   the VAT in it, as most invoices show it) or **Net**, with Projects on the **Project**,
   and a text. Each line shows its net and its VAT as the books will post them, and under
   the lines the form adds up the lines and their VAT. While the lines do not make the
   total, it says how much is left.
4. **Save draft**. A draft can be changed and deleted freely; nothing is in the books yet.
5. Under **Files**, attach the invoice as you received it — a PDF or a scan. A draft needs
   one to be posted.
6. **Post**. The voucher debits each line's account for its net with its tax code, debits
   the VAT to the code's VAT account, and credits accounts payable for the total on an open
   item for the invoice. The files become the voucher's attachments.

The invoice is posted on its invoice date: a date before the books start, or in a locked
month, is refused — date it in an open month, or move the lock back.

A line's tax code decides its VAT:

| Code | On the invoice | The voucher |
| --- | --- | --- |
| 1, 11, 12, 13 | Norwegian VAT at 25, 15, 11.11 or 12 % | the VAT on the code's input VAT account (2710–2713) |
| 0 | no VAT | the net alone, with the code |
| 86–89, 91, 92 | no VAT: reverse charge | the VAT calculated by you, both sides (below) |

An output code (3, 31, …) is a sale's and is refused on a purchase. The import codes — 14,
15, 20, 21, 22 and 81–85 — are refused on a supplier invoice too: an import's VAT is
reckoned from the customs declaration, not from an invoice, and is entered as a journal
entry ([Import of goods](#import-of-goods)).

### Reverse charge — services bought from abroad

A supplier abroad bills without Norwegian VAT, and you calculate it yourself: the books post
it twice, as VAT you owe and — when it is deductible — as VAT you deduct. The payable stays
at what the supplier bills. Enter the line **Net**: the invoice carries no VAT to take out.

*Example.* A consultancy in Sweden bills NOK 1 000 for an audit of your systems; you enter
one line of 1 000.00 on 6700 with code **86** (services from abroad, deductible). The
voucher:

| Account | Debit | Credit |
| --- | --- | --- |
| 6700 Accountancy and consultancy services (code 86, base) | 1 000.00 | |
| 2715 Input VAT, reverse charge (code 86) | 250.00 | |
| 2705 Calculated VAT, reverse charge and import (code 86) | | 250.00 |
| 2400 Debt to suppliers | | 1 000.00 |

With code **87** (services from abroad, *not* deductible — for a cost whose VAT you may not
deduct) the 250.00 is debited to 6700 itself instead of 2715: the VAT becomes part of the
cost. Codes 88 and 89 are the same at the low rate, 91 and 92 emissions allowances and gold.

On the voucher the calculated side — the 2705 line — is marked **VAT owed (reverse
charge)**, apart from the VAT you deduct, so the VAT return counts the base once.

### Import of goods

When you import goods, the VAT base is the customs declaration's (tolldeklarasjonen), not
the supplier's invoice: the customs value, with freight and duty. So the two are entered
apart:

1. The supplier's invoice, as a supplier invoice, with its goods line on the goods account
   and no tax code (or code **0**): the cost of imported goods, with no VAT of its own.
2. The import VAT, as a [journal entry](#posting-a-journal-entry) from the customs
   declaration: a line on the goods account with code **81** (83 at the middle rate; 82 and
   84 when the VAT is not deductible) and the declaration's base, entered **Net**, and the
   same amount credited back to the same account with no code. The VAT lines are added for
   you.

*Example.* The declaration values goods at NOK 10 000 at 25 %:

| Account | Debit | Credit |
| --- | --- | --- |
| 4005 Cost of goods (code 81, base) | 10 000.00 | |
| 4005 Cost of goods | | 10 000.00 |
| 2714 Input VAT, import (code 81) | 2 500.00 | |
| 2705 Calculated VAT, reverse charge and import (code 81) | | 2 500.00 |

The two base lines cancel each other: the goods are already on the supplier's invoice, and
the entry adds only the VAT, owed and deducted. Import VAT a forwarder or the customs bills
you for (codes 14 and 15) is entered the same way, as a journal entry from the declaration.

### Another currency

Choose the invoice's **Currency** (the supplier's proposes it). The books are kept in NOK,
so give either the rate — **By rate**, NOK per unit — or **By NOK total**, the total in NOK;
the form shows the other beside it, and nothing is proposed: take it from the bank or the
customs. Through the API both may be given, and they must agree: the total at the rate,
rounded to øre, within an øre per line of the NOK total — otherwise the invoice is refused
as mistyped. A draft made from an expense in another currency has no rate yet: **Post**
asks for it. Each posting is the line's amount at the rate, rounded to øre, with the amount in
the currency and the rate kept beside it; an øre the rounding leaves goes on the largest
line, so the voucher keeps the invoice's own total. A reverse-charge VAT is calculated on
the NOK amount.

### Paying a supplier

On a posted invoice, **Register a payment** asks for the date, the **Amount in NOK** as the
bank took it and the **Bank account** (empty is the bank account; another asset account,
such as a currency account, can be chosen). The voucher debits accounts payable on the
invoice's item and credits the bank. A part payment leaves the rest owed; the invoice is
**Paid** when nothing is left.

For an invoice in another currency, also give the **Share settled** in its currency — empty
settles all that is left. The share is worth its amount at the invoice's rate; if the bank
took more NOK than that, the difference is a currency loss (8160), if less a currency gain
(8060), posted on the same voucher.

**Remove** on a payment posts it turned back, dated the day it was paid — refused once that
month is locked. The invoice is owed again.

A supplier invoice's item is paid here, never with **Register** under Open items.

### Credit notes

A credit note is entered as an invoice is, with **Credit note** chosen and, under **Credits
invoice**, the posted invoice it credits. It posts with every sign turned — the cost and the
VAT credited, accounts payable debited — on the open item of the invoice it credits, which
is then owed the rest. A credit note for no particular invoice stands on its own item; a
refund the supplier pays back is registered on it as a payment.

**Post** holds a credit note to its invoice as it stands then: the invoice must still be
posted, the credit note must be in the invoice's currency, and it may not credit more than
is left on the invoice — in NOK, and in the currency for an invoice in another one. A
credit note and a payment posted at the same moment on the same invoice wait for each
other, so the second sees what the first left.

### Correcting a posted invoice: Reverse and copy

A posted voucher is never changed. **Reverse and copy** asks for a date and a reason, posts
the invoice's voucher reversed, line for line, marks the invoice **Reversed**, and opens a
new draft with the same supplier, number, lines and files for you to correct and post. An
invoice with a payment standing is not reversed: remove the payment first. Nor is one with a
credit note, or a payment made through a credit note, standing on it: reverse the credit note
first (Reverse and copy on the credit note), then the invoice.

Entering, posting and paying supplier invoices needs **Post vouchers**.

## How expenses arrive from Expenses

With [Expenses](/en/user/expenses/) on, what it approves and reimburses posts itself, as
sales do — a voucher each, within seconds or minutes:

| In Expenses | The voucher |
| --- | --- |
| An expense the employee paid (an outlay, mileage, a per diem) is approved | The expense's category's account debited ([Posting rules](#posting-rules)); 2910 *Debt to employees* credited. |
| An expense the company paid is approved | The category's account debited; the bank credited. |
| A travel claim is approved | Each of its lines as above, on one voucher. |
| An expense or a claim is reimbursed | 2910 debited and the bank credited, by what the approval owed, dated the reimbursement's day. |
| An approval or a reimbursement is undone | Its voucher turned back, line for line. |

So approving, unapproving and approving again gives three vouchers: the cost, its reversal
and the cost again. The project on an expense is the posting's project. An expense dated
before the books start is not posted.

**VAT.** An expense has a VAT amount but no tax code. The books deduct it when the
category's rule says the VAT is deductible, the expense is an outlay in NOK, and the VAT
matches one of the input rates — 25, 15, 12 or 11.11 % of the rest, give or take an øre per
hundred kroner: the cost is then posted net with that rate's code, and the VAT on its input
VAT account. Otherwise the expense posts gross, nothing deducted; when its category allows
deduction but the VAT matched no rate, Needs attention says *VAT not deducted* — for your
information: it is posted. Mileage and per diem carry no VAT.

**In another currency.** Expenses keeps no exchange rate, so an expense in another currency
waits under Needs attention for its amount in NOK. The voucher posted with the amount you
enter is yours, as a journal entry would be, and its line names what was entered: *NOK
921.50 entered for EUR 80.00*.

**Expenses' supplier invoices.** An approved expense of the supplier invoice kind is not
posted by itself — it has no supplier of the books and no payment. It waits under Needs
attention for **Make a supplier invoice**.

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

**Expense rules.** **Add a rule** with **Expenses** chosen names an expense category from
Expenses, an expense account, and whether the category's VAT is **deductible** — leave it
off for entertainment and welfare, whose VAT the VAT Act never lets you deduct. An expense
of a category no rule names is posted to 7700 Other costs, with no VAT deducted. Changing
the account or the deductibility is a new version, as for a sales rule.

Changing the rules needs **Manage the books**.

## Needs attention

**Needs attention** lists every fact the books could not post, with why: a tax code the
books do not have, or that is inactive; an account that is inactive, or a rule pointing at
an account of the wrong kind; a kind of fact this version does not know; an expense in
another currency; an expense of the supplier invoice kind. The overview and the dashboard
count them.

What comes after it for the same invoice — or the same expense or travel claim — waits
behind it, so the books never take a payment before the invoice it pays, or a
reimbursement before its expense: such a line says it **waits** for the earlier one.

Fix the cause — activate the account, add the tax code, change the rule — and click
**Retry** on the line: it reads the fact again and posts it, and the line then shows its
voucher. When it posts, what waited behind it posts too, in order. Retrying a line that
waits keeps it waiting: retry the one it waits for. A fact is never dismissed. **Show those
posted since** shows the ones a retry has posted.

Two reasons have their own action:

- **In another currency: the amount in NOK is needed.** Click **Enter the amount in NOK**,
  give what each expense cost in NOK — normally from the bank or card statement — and
  **Enter and post**. What waited behind it, its reimbursement say, posts after it.
- **A supplier invoice from Expenses.** Click **Make a supplier invoice**. The dialog asks
  whether a new supplier is a person (a sole proprietor) — tick it if so: a person's record
  is kept and later anonymised as [Suppliers](#suppliers) says. A draft then opens with the
  expense's amount, VAT code, date, number, due date, project and receipts, and its
  supplier — the supplier of that name, or a new one. Check it, and post it as any
  supplier invoice. Deleting the draft — or the copy a Reverse and copy of it made — puts
  the expense back on the list. If the expense is unapproved in Expenses before then, its
  line goes away by itself. An expense is made into one supplier invoice: a second is
  refused.

If an expense is unapproved in Expenses *after* it was made into a supplier invoice, the
books turn nothing back — the invoice is yours — and Needs attention says so on a line
naming the invoice: *The expense behind a supplier invoice was unapproved*. While the
invoice is a draft the line waits: delete the draft, or post it if the invoice is owed all
the same, and click **Retry**. Once the invoice is posted the line is for your information;
reverse the invoice if it is not owed.

A line marked **For your information** — *VAT not deducted* — has posted; it is not counted
with what waits. **Acknowledge** takes it off the list, and it goes by itself when the
expense is unapproved in Expenses and its voucher turned back. If the VAT should have been
deducted, reverse the expense's voucher with a journal entry and enter it again with the
right code.

Retrying and the actions need **Post vouchers**.

## Open items

**Open items** lists, per customer and per supplier, every document not yet settled: its number, dates, the
original amount and the balance, 200 at a time with **Previous** and **Next**. **Show
settled** shows the settled ones too. A posting on
accounts receivable that names no document lands on the customer's **Unallocated** item.

**Register** on an item records money moved: a receipt from the customer, or a refund of a
credit balance. Give the date, the amount (at most the balance) and, when it is not the
bank account, the account the money moved through. The voucher is posted at once.

An item marked **In Invoices** stands for an invoice there: its payment is registered on
the invoice in Invoices, never here. A supplier invoice's item is paid on the supplier
invoice.

Registering needs **Post vouchers**.

## Reports

**Reports** produces the specifications for any period, each with when it was produced:

- **Journal** (bokføringsspesifikasjon) — every voucher's postings in number order.
- **Account** (kontospesifikasjon) — one account's opening balance, its postings and its
  closing balance.
- **Customers** and **Suppliers** — per party, the items with a balance or a movement in the
  period, and the balance aged by due date: not due, 1–30, 31–60, 61–90 and over 90 days.
  An item for an invoice links to it in Invoices; a supplier links to its page and an item
  to its supplier invoice. The suppliers' total is the balance of accounts payable.
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
| **Post vouchers** (`accounting:post`) | Journal entries, reversals, attachments, settlements, suppliers, supplier invoices and their payments, and the actions under Needs attention. |
| **Manage the books** (`accounting:close`) | Setting up the books, the chart, the tax codes, the posting rules, the opening balances and the lock. Only an Owner can give it. |
| **Export the books** (`accounting:export`) | The reports as CSV, and a supplier's data. |

When the engagement ends, remove the role or disable the user: everything they posted
stays in the books under their name.
