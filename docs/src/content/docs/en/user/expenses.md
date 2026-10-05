---
title: Expenses
description: Outlays, mileage and travel claims, receipts, approval and reimbursement.
sidebar:
  order: 40
sources:
  - apps/expenses/frontend
---

The Expenses app is where you record what you paid for and the distance you drove,
gather a trip's costs into a travel claim, and send it all for approval. Once an
expense is approved it can be paid back to you through payroll, and — if it was
booked on a project — passed on to the customer. The rules behind every step are in
the [Expenses module reference](/en/reference/expenses/); this page is about what you
do on each screen.

## Who sees what

The sidebar shows the areas your permissions give you:

| Area in the sidebar | Who sees it |
| --- | --- |
| **My expenses** | everyone with `expenses:access` |
| **Expense approvals** | `expenses:approve` — a project manager approves their own project's expenses without it, reaching the same queue from the dashboard's attention list |
| **Reimbursements** | `expenses:manage` |
| **Expense settings** | `expenses:manage` |

`expenses:manage` also lets you record expenses for a colleague and work on days the
period lock has closed; `expenses:view-all` lets you see everyone's expenses. The
full table is under [Permissions](/en/reference/expenses/#permissions).

## Find your way around My expenses

**My expenses** opens with three figures: **In drafts**, **Awaiting approval** and
**Approved, not paid back** (or **Nothing owed**). Below them, the filters —
**Status**, **Kind**, **Paid back**, **From** and **To** — narrow both lists on the
page: **Travel claims** first, then **Expenses**. The two are listed separately, each
with its own pages, because a trip is one unit however many expenses it holds. While
a **Kind** filter is set, the travel claims are not shown — a trip is not of one kind.

Every expense row shows its date, description, kind, project, amount, status and
receipts, and the actions you may take on it right now: edit, send for approval and
delete. A rejected expense carries the reason in red, and a paid one says **Paid
back** with the date.

## Record an outlay

1. Press **New expense**. The **Kind** control offers **Outlay**, **Mileage** and —
   when the projects module is on — **Supplier invoice**. The kind is locked once the
   expense is saved.
2. Fill in **Date** and **Description**. Days before the period lock are greyed out
   and the form says so; see [the period lock](/en/reference/expenses/#the-period-lock).
3. Choose a **Category**, name the **Supplier** if you want to, and give the
   **Amount including VAT** in the installation's currency, which is shown under the
   field. Under **VAT**, the helper **Work the VAT out** fills the VAT at 25 %, 15 % or
   12 % of the amount; a figure you type yourself wins. **Net** is shown beside it.
4. Under **Who paid**, choose **I paid** or **The company paid**. An outlay the
   company paid owes you nothing and never reaches payroll; see
   [what "owed to the employee" is](/en/reference/expenses/#money-rules).
5. If the projects module is on, pick a **Project** and, if the project has them, a
   **Line**, then switch **Billable** on if the customer is to be charged. You can
   only book on a project you may log time on; otherwise the form says so and the
   expense is recorded without one. What the customer is billed is set from the
   project's side, not here.
6. Press **Save draft** to keep it open and add receipts, or **Save and submit** to
   send it for approval at once.

A new outlay stays open after **Save draft** so its receipts can be added.

## Add receipts

Receipts can only be attached to an expense that exists, so save it as a draft first.
Then drop the files on the receipt area, choose them with the file picker, or use
**Take a photo** on a phone. A receipt is JPEG, PNG, HEIC or PDF, at most 10 MB, and an
outlay holds at most ten; a file that does not fit is refused by name. Click an image
to view it (a PDF opens in a new tab), and remove one with the cross beside it while
the expense is still yours to edit.

If the installation's receipt rule applies to an outlay you paid for yourself, the
form says **An outlay you paid for more than … needs a receipt before it can be
submitted**, and the submit is refused until one is attached. Mileage never takes a
receipt. See [the receipt rule](/en/reference/expenses/#the-receipt-rule) and
[receipts](/en/reference/expenses/#receipts).

## Record mileage

Choose **Mileage** as the kind, then give **From** and **To** if you want to, the
**Distance in kilometres** (one decimal, at most 9999.9) and the number of
**Passengers** (0 to 8). The form shows a preview — kilometres × rate = amount — from
the rate in force on the expense's date, with the passenger supplement added per
passenger. It is only a preview: the amount is worked out again on save and frozen when
the expense is submitted. If no mileage rate applies on that date, the form says so and
the expense cannot be sent for approval until an administrator adds one. There is no
amount to type and no receipt to attach. See [the dated
rates](/en/reference/expenses/#the-rates-and-what-they-deliberately-do-not-model).

## Record a supplier invoice

A supplier's invoice for goods or work on a project is recorded as a **Supplier
invoice**: name the **Supplier** and the supplier's **Invoice number**, give the
**Invoice date** and, optionally, a **Due date** on or after it, choose a **Category**
(**Subcontractor** is preselected when it exists) and enter the amount and VAT. The
company always pays it, so there is no payer control and nobody is paid anything back
for it. The **Project** is required and **Billable** starts on; the picker lists the
projects whose money you may see, and when it is empty the form points you to the
project's own page instead.

After **Save draft** the form stays open so you can attach **The supplier's
invoice**; **Save and submit** is unavailable until it is attached. Once submitted it
follows the ordinary approval flow. See [the supplier
invoice](/en/reference/expenses/#the-supplier-invoice).

## Record a travel claim

A travel claim is a trip and everything it cost, submitted and paid back as one.

1. Press **New travel claim** on My expenses (or use the **New travel claim** action
   in the search box).
2. Write **What the trip was for** and, if you like, **Where it went**. Choose
   **Domestic** or **Abroad**; a trip abroad asks for the **Day rate abroad** and the
   **Currency abroad**, which replace the dated per diem table for that trip.
3. Give the **Day of departure**, **Time of departure**, **Day of return** and **Time
   of return**. The return must be after the departure, and the times are in the
   business time zone the form names, not your browser's.
4. Pick a **Project** if the trip is booked on one. Every expense on the trip takes
   the trip's project.
5. Press **Save**. You land on the trip's own page.

The trip's page shows its status, where it went, its dates and **The trip's totals**
per currency (and **To the customer** when something is billable). From here, **Edit
the trip** changes the header, **Delete** removes the trip with everything on it, and
**Submit claim** sends the whole trip for approval. A trip holds at most 200 expenses
and the page says when it is full.

## Add per diem days to a trip

Under **Per diem** on the trip's page, press **Suggest days**. Answer **Did you stay
overnight?** — the times alone cannot tell, and it decides whether the trip is counted
in 24-hour periods or as one day — then press **Suggest**. The days the trip's times
earn are listed, priced with the rate in force on each date; a day already on the trip
says **Already added**, and a day no rate applies to cannot be ticked. Press **Add …
days** to record the ticked ones.

Each day in the table has a **Kind of day** (**Day, 6 to 12 hours**, **Day, over 12
hours**, **Overnight, hotel** or **Overnight, other lodging**), a **Day rate**, and
**Meals covered** — tick **Breakfast**, **Lunch** or **Dinner** when somebody else paid
for it, and the percentage beside each is deducted. Every change is saved the moment
you make it and the **Amount** is the server's answer. A kind of day the rate table
does not price, or a covered meal with no deduction priced, is refused on that field
until an administrator enters the rate. See [the per diem
day](/en/reference/expenses/#the-per-diem-day).

## Add driving and outlays to a trip

**Add mileage** under **Mileage** and **Add an outlay** under **Outlays** open the
ordinary expense form with the kind fixed and the date starting on the day of
departure. The line takes the trip's project; if the trip is booked on one, the
line's own **Billable** switch is in the form. There is no **Save and submit** on a
line, because a trip is submitted whole. Lines are edited and deleted from their rows
with the pencil and the bin.

## Edit, delete and submit

While an expense is a **Draft** or has been **Rejected**, and its date is not behind
the period lock, its row offers the pencil to edit it, the bin to delete it (asked
out loud: **Delete the expense?** — it cannot be undone) and the paper plane to send
it for approval. Tick several expenses and travel claims and press **Submit … selected**
to send them in one go; the batch is all or nothing, and a refusal is written under
the row it is about.

Submitting freezes a mileage line's rate and a per diem day's rate and deductions as
they stand that day. A submit can be refused for a missing receipt, a rate the table
no longer has, a trip holding no expenses, or a date the lock has closed — the
sentence says which. Once submitted, the expense is **Submitted** and no longer yours
to change: opening it shows **This expense can no longer be changed.** See [the flow,
and what freezes on submit](/en/reference/expenses/#the-flow-and-what-freezes-on-submit).

## When an expense is sent back

A rejected expense shows **Rejected: …** with the reason, who rejected it and when, on
My expenses, on your Home dashboard and in the form. Correct it and submit it again:
saving a rejected expense makes it a draft, while a rejected trip stays **Rejected**
until you press **Submit claim** again.

## Approve or reject expenses

**Approvals** (sidebar: **Expense approvals**) lists the submitted expenses and travel
claims you may decide, one card per person, the person who has waited longest first.
Each card carries the person's totals per currency and two flags worth a look: **…
without a receipt** and **… with a replaced rate**.

- Tick what you want to decide — trips and expenses alike — and press **Approve …
  selected** or **Reject … selected**. Rejecting opens **Send the expenses back?**,
  where a **Reason** is required (at most 1000 characters); the person sees it with
  every expense you send back. The batch is all or nothing.
- Click an expense's description, or **Open** on a trip, for the details: the amounts,
  the receipts, the owner, the project and the billing block if you may see it. The
  drawer offers **Approve** and **Reject**, and on a submitted mileage line or per diem
  day **Replace the rate**, where the dialog says what the table gave and what the line
  becomes; on mileage with passengers the **Passenger supplement per kilometre** can be
  replaced too. The replacement is recorded with your name.
- The **Approved** half of the switch lists what has been approved. Tick and press
  **Take … approvals back** (or **Take the approval back** in the drawer) to return a
  unit to draft so it goes round again. It is refused once the unit has been paid back
  or one of its lines invoiced.

You may approve your own expenses. If you approve nobody's, the page says **You
approve nobody's expenses**. See [approval](/en/reference/expenses/#approval).

## Pay people back

**Reimbursements** needs `expenses:manage`; without it the page says **You cannot see
what is owed back**. **Waiting to be paid** lists, per person, the approved expenses
and travel claims that owe the person something, with **Owed back** per row and per
currency; **From** and **To** narrow the list by date. A trip is one row, for the sum
of what its lines owe.

1. Tick the rows, or **Select everything of …** on a person's card, and press **Mark …
   as paid back**.
2. In **Record a payroll run**, give the **Day the money went** (today by default,
   never in the future) and an optional **Payroll reference** (at most 100
   characters), then press **Mark as paid back**. All or nothing, and a trip is paid as
   one.

**Already paid** shows the runs recorded, with the date and reference per row; tick and
press **Undo … payments** to clear a stamp and put the units back in the waiting list.
Neither marking nor undoing is held back by the period lock. See [the two tracks after
approval](/en/reference/expenses/#the-two-tracks-after-approval).

## Export the payroll file

On either half of Reimbursements, **Export everything waiting** / **Export everything
paid** downloads what the current filters show, and **Export … selected** exactly the
ticked units. The file is semicolon-separated with a decimal comma and ISO dates, one
row per line with the trip's purpose beside each of its lines, named
`expenses-reimbursements-<date>.csv`. An export over 5 000 rows is refused with
**Too many rows to export** — narrow the dates instead. The columns are listed under
[the payroll CSV](/en/reference/expenses/#the-payroll-csv).

## Expenses on a project

The project page's **Expenses** tab shows **What the expenses come to** — one card per
currency, nothing converted — with the **Approved**, **Submitted** and **Draft**
buckets, the **Total**, **Passed on to the customer**, **Ready to invoice**,
**Invoiced** and **Of which supplier invoices**. The totals need financial rights on
the project; the list underneath, **Expenses on this project**, shows the expenses you
may open, and says so when that is fewer than the totals cover.

- **Record a cost** opens the expense form with the project stated rather than offered,
  **The company paid** to start with, and the **Line** and **Billable** controls in
  reach. **Record a supplier invoice** stands beside it for the project's financial
  side.
- The **Ready to invoice** chip narrows the list to the approved, billable, priced lines
  not yet invoiced.
- Click a description to open the line. **Price for the customer** sets **Billable**,
  the **Line**, and the **Markup** (an outlay or supplier invoice) or the **Customer
  rate per kilometre** (mileage); a field left empty keeps what the line carries. It is
  open in every status until the line is invoiced, and never on a per diem day.
- **Mark invoiced** records that the line went out on an invoice, with an optional
  **Invoice reference**. *Invoiced* is a stamp, not a status: the line stays approved,
  leaves the ready-to-invoice list, and counts under **Invoiced** on the card. **Undo
  invoicing** takes the stamp back after a confirmation.
- A line that went out on an invoice issued in the **Invoices** app is invoiced by that
  invoice. **Undo invoicing** is not offered on it: only a credit note that returns the
  whole line takes the stamp back and puts the line in the ready-to-invoice list again.
  Marking such a line invoiced, or undoing it, by hand is refused.

Pricing and invoicing belong to whoever may see the project's money, not to
`expenses:manage`, and the period lock does not reach them. See [pricing by the
project side](/en/reference/expenses/#pricing-by-the-project-side) and [on the project
page](/en/reference/expenses/#on-the-project-page).

## Change the settings

**Expense settings** needs `expenses:manage` and has three sections.

**General**: the **Default currency** every new expense is recorded in; the **Default
markup** a billable outlay is charged on above its net unless the project says
otherwise; the **Receipt rule** — **Off**, **Always** or **Over an amount** with
**Receipt needed above**; **Locked before**, the first open day of the period lock
(leave it empty for no lock; changing it asks **Change the lock?**); and the **Business
time zone**, which decides which day a trip's departure and return fall on. Changing
the zone moves the days of trips already recorded and is confirmed out loud — set it
once, when the installation is set up.

**Rates**: every rate kind is listed whether it has rows or not — **Mileage**,
**Passenger supplement**, **Customer rate per kilometre**, the four per diem kinds and
the three meal deductions. A row is in force from its **Valid from** day until the
next row takes over. **Add a rate** (or **Add a rate to …** under a kind) asks for the
**Kind**, **Valid from**, the **Value** (or **Percent**, 0 to 100, for a deduction),
the **Currency** and an optional **Source** label; a row with none is shown as **Own
rate**, a labelled one as **State rate**. Edit and delete a row with the pencil and the
bin — expenses already submitted keep the rate they were frozen with. **Restore the
state rates of …** puts the shipped rows of a kind back as they shipped and leaves your
own rows alone. Two kinds ship empty on purpose, **Customer rate per kilometre** and
**Per diem, other lodging**; see [the
rates](/en/reference/expenses/#the-rates-and-what-they-deliberately-do-not-model).

**Categories**: what an outlay is booked under. **Add category** asks for a **Name**;
the pencil renames one and switches **Can be chosen** off or on; the arrows move it up
or down the list, and **Deactivate …** / **Reactivate …** does the same as the switch. A
category is never deleted: a deactivated one stays on the expenses already booked on
it but cannot be chosen for new ones.
