---
title: Orders
description: The Orders app — make a quote, from the app, a customer's page or a template; add product, free-text, section and note lines, optional lines and discounts; send it or mark it as sent, revise, accept, decline, cancel and copy it; make an order, confirm it, change it after confirming, put it on hold, lock it, cancel it or its rest, register what is delivered, send the order confirmation and print a pro forma; invoice it, see its invoices and release what draft invoices hold; make a subscription, activate, change, pause, resume, end and cancel it, accept a quote as one and see its periods; the change log; lines in packagings and from the variant grid; customer and product warnings and overriding a block; the sales analysis; the customer's Orders tab; and what a refusal means.
sidebar:
  order: 55
sources:
  - apps/orders/frontend
  - apps/host/frontend/src/routes/orders
  - apps/host/frontend/src/routes/customers/-customer-orders-tab.tsx
---

The Orders app is where you make quotes for your customers and take their orders. In
Norwegian the app is **Ordre**. A **quote** is an offer the customer can accept; an
**order** is what the customer has bought, which you confirm, deliver and invoice.

## The app and its sidebar

The sidebar has **Quotes**, **Sales orders** and **Subscriptions** for everyone with access to the app,
**Analysis** for those who may view the sales analysis, and **Quote templates** and
**Orders settings** for those who manage the Orders settings.
What you can do in each depends on your permissions: making and changing quotes, making
and changing orders, and confirming orders are three separate permissions, so a
colleague may see buttons you do not.

**Quotes** lists the quotes newest first, with their number, customer, status, salesperson
and total. Narrow it with the search (number, customer or reference), **Status**,
**Customer**, **Salesperson** and **Only mine**. **Sales orders** lists the orders the same
way, with **Open**, **All** or **Done**, and the **Delivery** and **Invoicing** filters;
an order is done when it is confirmed and every product line is invoiced or cancelled in
full. An order whose remaining quantities were all cancelled is done; one delivered but not
yet invoiced is still open.

## Make a quote

1. In **Quotes**, choose **New quote**. You can also start from **New quote** in the search
   (spotlight) or on a customer's **Orders** tab, which fills in the customer.
2. Pick the **Customer**. The currency, whether prices are shown including VAT, the
   payment terms and **Your reference** are filled in from the customer.
3. To start from ready-made texts and lines, pick one under **Start from a template**.
4. Choose **Create the quote**. The quote gets the next number in the quote series and
   opens as a draft.

Picking a customer needs access to the customers; without it, start from the customer's
page instead. A disabled, archived or merged customer cannot get a new quote.

### The header

While the quote is a draft you can change **Delivery address** (by default the customer's
address), **Contact person**, **Your reference**, **Salesperson** (**Make me the
salesperson** sets you), **Project**, **Currency**, **Prices** (**excl. VAT** or **incl.
VAT**), **Payment terms**, **Document language**, **Valid until**, **Delivery date** and
**Show the discount on the PDF**, and under **Texts** the **Intro text**, the **Closing
text** and an **Internal note**, which is never printed. A draft follows the customer: if
the customer's address or name changes in Customers, the next save takes it in.

### Add lines

Under **Lines**, add:

- **Product** — search the catalog by SKU or name. The line takes the product's name,
  unit, catalog price in the quote's currency and VAT code. If the product has no price in
  this currency the line says **No price in this currency**; type one in.
- **Free-text line** — something not in the catalog: give it a name (**What is sold**), a
  quantity, a price and, if it is not the standard rate, a VAT code.
- **Section** — a heading that groups the lines below it.
- **Note** — a text printed between the lines.

Give each product line a **Quantity**, and a **Discount** in percent if any. A price you
type yourself is shown as **Manual price** and stays as you typed it; a catalog price is
updated if you change the currency or save on a later day. Tick **Optional** on a line
the customer may choose to take: it is printed, marked as optional, and left out of the
totals. Move lines with the arrows and remove them with the remove button.

With **Prices** set to **incl. VAT** you type and see prices including VAT; they are
stored without it. Choose **Save**: the server prices the lines and computes the totals —
**Net**, VAT per rate and **Total**.

A saved line keeps its kind: to turn a note into a product line, say, remove it and add a
new line.

### Sell in packagings

A product sold in packagings — a box of 15, a pallet of 40 boxes — shows a packaging
selector under the line's quantity once you have picked it. Choose the packaging and type
how many: **3** of **Box of 15** is stored as 45 of the product's unit, and the line
shows "= 45 pcs" as you type. The PDF prints it as "3 × Box of 15 (45 pcs)" — in Norwegian "3 × Eske à 15 (45 stk)". Choose the
unit again to enter the quantity in the product's own unit. A count that would make the
quantity need more than three decimals is refused; enter the quantity in the unit instead.
A saved line keeps the packaging it was entered in even after the product no longer has
it, and offers the product's current packagings beside it.

### Add from grid

For a product with variants — sizes and colours, say — **Add from grid** saves typing a
line per variant:

1. Choose **Add from grid** under the lines, and search for the product by one of its
   variants under **Product (search a variant)**.
2. The variants are laid out as a grid, one option down and the other across. Type a
   quantity in each cell you want; a cell with a dash is a combination the product does
   not have.
3. Choose **Add the lines**. Each quantity adds that variant's line, or changes the
   quantity of the line it already has.

A product with more than two options, an option with more than 30 values, or a variant
without a value on one of them is shown as a list instead, with a quantity per variant.

### When someone else changed it

Every change you make — saving, sending, accepting, confirming, holding, locking,
registering a delivery, deleting — is checked against the quote or order as you opened it.
If someone else changed it in the meantime, even only by putting it on hold or locking it,
or the customer was merged or erased, your change is refused and the editor shows
**Changed by someone else**: "This was changed since you opened it. Reload to see the
latest version; your unsaved edits will be dropped." Choose **Reload** and make your change
again.

### Apply a template to a quote

On a draft, **More actions** → **Apply a template** adds a template's lines after the
quote's own, or with **Replace the quote's lines** instead of them, and fills in empty
texts. A template line whose product is discontinued or deleted is left out, and the app
names it.

## Warnings on a customer or a product

A customer or a product may carry a **warning**, which every quote and order with it
shows above the header:

- an **Information** warning, in yellow — "Call before delivery", say — is only shown;
- a **Block**, in red — "Does not pay", "Recalled" — also stops **Send**, **Mark as
  sent**, **Confirm** and accepting a quote into a confirmed order: the app says "A warning
  blocks this".

Someone who manages the Orders settings sees **Override the warning** under a block. Give
a **Reason** and choose **Override the warning**: the banner shows **Warning overridden**
with your reason, and your next send, mark as sent, confirm or accept goes through. The
reason is kept — in the order's **Change log** as **Warning overridden**, or at the top of
the quote's **Internal note**. **Undo** drops the override before you use it; once used, it
is gone, and the next send or confirm needs a new one. A quote whose e-mail has gone out
stays sent even if a block was set while it was being sent.

They also see **Add customer warning** (or **Edit customer warning**) there, to set the
customer's warning without leaving the document. All warnings are kept under
**Orders settings** → **Sales warnings** ([administration](/en/admin/orders/#keep-the-sales-warnings)).

## Send a quote

**Send** opens **Send the quote**: the **Recipient** (the contact person's e-mail, else the
customer's invoice e-mail), up to ten **CC** addresses, **Valid until** and a **Message**
added to the mail text. **Send** e-mails the PDF, stores it and moves the quote to
**Sent**. A sent quote can no longer be edited.

Sending needs Communications and a send channel chosen in the Orders settings. When it is
off the button says so: download the PDF instead (**PDF** → **Download the PDF**), hand it
over, and choose **Mark as sent** — the PDF is stored, the quote can no longer be edited,
and it is valid until the day shown. **Documents and mailings** on the quote lists every
sent revision's PDF and every e-mail with its status: **Queued**, **Sent** or **Failed**.

**PDF** also has **Preview the PDF** (a draft's is marked as a draft) and **Proforma**, a
print titled as not an invoice that is never stored or sent.

## Answer a quote

On a sent or expired quote:

- **Accept** opens **Accept the quote**. Tick the **Optional lines the customer took**;
  the ones not ticked are left out. Under **Make**, choose **A draft order**, to check
  before it is confirmed, or **A confirmed order**, confirmed at once with the quote's
  prices — which needs the permission to confirm orders — or **A subscription**, a draft
  subscription on the plan you give, which needs the permission to manage subscriptions
  and a quote of services only ([Subscriptions](#subscriptions)). The order or the
  subscription opens; the quote shows **Accepted into** with a link to it. A quote is
  accepted once.
- **Decline** records that the customer said no, with a **Reason** if you like.
- **Revise** makes the quote a draft again, as a new revision, to change and send again.
  The PDFs of earlier revisions are kept.

A sent quote whose **Valid until** day has passed becomes **Expired** the next day. It
can still be accepted — the dialog asks you to tick that you accept it anyway — declined
or revised.

**More actions** also has **Copy**, which makes a new draft quote of any quote with the
customer's details and catalog prices taken afresh, **Cancel the quote** for a draft or a
sent quote, and **Delete the quote** for a draft that was never sent. A deleted quote's
number is not used again.

## Make an order

Accepting a quote makes an order. To make one directly, choose **New order** in **Sales
orders** or on the customer's **Orders** tab, pick the **Customer** and choose **Create the
order**. An order has the same header and lines as a quote, except that it has no
optional lines and no valid-until day.

## Confirm an order

**Confirm** on a draft order freezes its customer details and prices, stores the order
confirmation and makes it **Confirmed**. It needs the permission to confirm orders. It is
refused when the customer is disabled, the order has no product lines, a line has no
price in the order's currency, or a product's tax category has no VAT code — map it in
Invoices first ([Products' tax categories](/en/user/invoices/#products-tax-categories)).
A block warning on the customer or a product stops it too, until someone who manages the
Orders settings overrides it ([Warnings](#warnings-on-a-customer-or-a-product)).

When the Orders settings say so, the order confirmation is e-mailed by itself to the
contact person, else the customer's invoice e-mail. To send it yourself, choose **Send the
confirmation**. **Order confirmation** downloads it, and **Proforma** prints a pro forma.

A draft order can be deleted with **More actions** → **Delete the order**; its number is not
used again. If the draft was made by accepting a quote, the quote goes back to **Sent**
and can be accepted again; if its **Valid until** day has passed, it becomes **Expired** as
usual.

When the confirmation is e-mailed by itself and the order is changed again before the mail
goes, the confirmation of the order as it is then is sent, and **Documents and mailings**
shows that version. An order cancelled before its confirmation went out gets none.

## Change a confirmed order

A confirmed order takes changes to what is not yet delivered or invoiced: add lines;
lower a line's quantity, but not below what is delivered, invoiced or on a draft invoice;
change a line's price, discount or text while nothing of it is invoiced or on a draft
invoice; remove a line with no deliveries, invoices or draft invoices; and change the delivery date, the delivery address, the contact person, your
reference and the internal note. The customer, currency, VAT mode, payment terms,
language, texts, project and salesperson are fixed once it is confirmed.

A line already on the order keeps the product, name, unit and VAT code it was confirmed
with, even if the product is changed or deleted in the catalog later. Only its quantity,
price, discount and text can change; for another unit or VAT code, remove the line (if
nothing of it is delivered or invoiced) and add a new one.

Each saved change raises the order's **version**, and the confirmation you send next is
titled **Revised order confirmation**. **Change log** lists every change since the order
was confirmed: when, by whom and what — **Line added**, **Quantity changed**, **Put on
hold**, **Registered delivered** and so on.

## Hold, lock and cancel

- **Put on hold**, with a **Reason**: an order on hold is not invoiced and takes no
  delivery — neither registering nor undoing one — but can still be edited. **Release**
  takes it off hold.
- **More actions** → **Lock** (permission to confirm): a locked order takes no edits, but
  is still delivered and invoiced. **Unlock** lets it be edited again.
- **More actions** → **Cancel the order**, while nothing is delivered or invoiced. A
  locked order must be unlocked first. This is not undone.
- **More actions** → **Cancel the rest**, once something is: every line's quantity not yet
  delivered or invoiced is cancelled. This is not undone.

Neither cancel is taken while a draft invoice holds part of the order: issue or delete the
draft first, or [release what it holds](#release-what-draft-invoices-hold).

Holding, locking and registering deliveries do not change the version.

## Register what is delivered

Until deliveries from stock arrive, register deliveries by hand on a confirmed order that
is not on hold.
**Register delivered** asks how much of each line is delivered now (**Deliver now**); a
line left at 0 is not changed, and you cannot deliver more than is left. **More actions** →
**Undo delivered** takes quantities back (**Take back**). Each line shows its progress —
"3 of 5 delivered" — and the order its delivery status: **Not delivered**, **Partly
delivered** or **Delivered**.

## Invoice an order

An order is invoiced in Invoices, from its lines: you choose how much of each line to
invoice, and the invoice's issue marks it invoiced on the order. Lines are invoiced on
order — what is ordered can be invoiced as soon as the order is confirmed, delivered or
not.

1. On a confirmed order, choose **Invoice**. It needs **Use Invoices** and **Create
   invoices** in Invoices, and is not offered without them. It is disabled, saying why,
   while the order is on hold ("An order on hold is not invoiced. Release it first."),
   when nothing is left to invoice ("Nothing is left to invoice.") and while you have
   unsaved changes ("Save your changes first").
2. Invoices' **Invoice from orders** opens with the order chosen. Pick how much of each
   line to invoice — all that is left, or less to invoice part of a line — and choose
   **Create the drafts**
   ([Invoice from orders](/en/user/invoices/#invoicing-orders)).
3. Check the draft in Invoices and issue it. Until then the draft **holds** the quantities
   it bills: each line's progress shows "2 on draft invoices", and they are not offered
   to invoice again.

When the invoice is issued, the line's invoiced quantity grows by what it billed and the
order's invoicing status becomes **Partly invoiced** or **Invoiced**. An order is done
once it is confirmed and every product line is invoiced or cancelled in full — register
the deliveries too, for the **Delivery** status and the backlog.

Deleting the draft in Invoices gives the quantities back at once: they are offered to
invoice again. So does a credit note that returns an invoice line in full — the order's
invoiced quantity goes back down, and the line can be invoiced again.

### While a draft invoice holds part of the order

A draft invoice is a promise of what will be invoiced, so the order keeps to it:

- a line's quantity cannot go below what is delivered, invoiced or on a draft invoice;
- a line's price and discount cannot change while part of it is on a draft invoice;
- a line on a draft invoice cannot be removed;
- the order cannot be cancelled, nor its rest.

Issue or delete the draft in Invoices, or release what it holds.

### Release what draft invoices hold

When a draft invoice should not be issued after all — someone made it and forgot it, say
— choose **More actions** → **Release draft allocations** on the order. Every quantity
draft invoices hold of the order is offered to invoice again at once. A draft invoice that
still holds part of it is then refused when someone issues it; delete it and invoice the
order again. The **Change log** records it as **Released from draft invoices**.

A draft invoice holds its part of the order for as long as the draft exists, however
long that is: nothing gives it back by itself. What a deleted draft held is given back at
once — or, should Orders not have been told, by Invoices within the next hour or so.

### The Invoices card

A confirmed order's page has an **Invoices** card listing every invoice issued from it:
**Invoice no.**, **Invoice date**, **Net amount** — what the invoice still bills of this
order, with what credit notes gave back of it beneath, *NOK 250.00 credited* — and
**Status**, **Invoiced** or, once credit notes returned all of it, **Credited**. With
**Use Invoices** the number links to the invoice. Until something is invoiced the card says
"Nothing is invoiced yet."

## Subscriptions

A **subscription** (*abonnement*) bills services per period — support, hosting, a
licence — month after month until it ends. Subscriptions sell services only: a product
line must be a service in the catalog, and free-text lines, sections and notes are
allowed.

**Subscriptions** in the sidebar lists them newest first, with their state, plan, start
and end, **Billed through** and what a full period is worth net of VAT. Narrow it by
state, customer and the search (number, customer or reference). Everyone with access to
the app sees them; making and changing them needs the permission to manage
subscriptions.

### Make a subscription

1. Choose **New subscription**.
2. Pick the **Customer**, and the plan: the **Interval** (**Monthly**, **Quarterly**,
   **Half-yearly** or **Yearly**), **Billing** — **In advance**, a period billed from its
   first day, or **In arrears**, from its last — the **Start**, an **End** if it has one,
   the **Notice (months)**, the **Invoice day** and the **Final period when a subscription
   ends** ([End](#pause-resume-end-and-cancel)); left empty, it is the one the Orders
   settings give. The notice and the invoice day are printed for the customer; they do not
   change what is billed when.
3. Choose **Create subscription**. The draft opens; add its lines as on a quote, and
   **Save**.
4. Choose **Activate**. The subscription is checked as an order is at confirm — every
   line priced and with a VAT code, the customer active — and from now on its periods are
   billed.

A quote can become a subscription too: in **Accept the quote**, choose **A subscription**
under **Make** and give the plan ([Answer a quote](#answer-a-quote)).

The periods run from the first of the start's month: a monthly subscription's periods are
the calendar months, a quarterly one's three months at a time from the start's month.
When the start is not the first of a month, the first period runs from the start to the
period's end and is **pro-rated** by its days — a monthly subscription started on 31
January bills 1/31 of January's price for 31 January, then whole months from February.

### Its periods

The subscription's page lists its **Periods**, each with its amount and its state:

- **Invoiced** — on an issued invoice, whose number is shown;
- **On a draft invoice** — taken by a draft in Invoices, not yet issued;
- **Due** — billable today, not yet invoiced;
- **Upcoming** — not due yet;
- **Paused** and **Skipped** — see below.

**Billed through** is the last day of the periods invoiced without a gap from the start.
Periods are invoiced in Invoices, by **Invoice from orders** or a billing run of orders
([Invoicing orders](/en/user/invoices/#invoicing-orders)), and never more than twelve
months ahead. When a credit note gives an invoiced period back in full, that period is due
again — and only that one.

### Change an active subscription

Change the lines — the quantity, the price, a line added or removed — and choose
**Save**. A change never touches a period already on an invoice or a draft invoice: it
takes effect from **Changes take effect from**, the first period not yet billed, or a
later period start you choose. A changed or removed line ends the day before; its new
version starts that day. The lines a change ended are listed under **Earlier lines**. A
change is never pro-rated, and the **Change log** records it with the period it takes
effect from.

If the change is from an earlier period than a change you saved before, the new one
replaces the old from its own date: a line is never billed twice in a period.

The customer, the currency and the plan's interval, billing and start are fixed once the
subscription is active; the **Final period when a subscription ends** can still be
changed.

### Pause, resume, end and cancel

- **Pause** stops billing from a period start — the next one not billed, unless you pick
  a later one. The periods before it are still billed.
- **Resume** restarts billing from a period start you choose. The periods between the
  pause and it are **Skipped**, and never billed.
- **End** sets the end date: the period it falls in is the last one billed. When the end
  is not that period's last day, choose how it is billed under **Final period when a
  subscription ends**, pre-selected from the subscription's own setting:
  - **Full period** bills the whole period;
  - **Pro-rated to the end date** bills the period from its first day to the end date,
    inclusive, as a share of the full period's days — a monthly subscription of 310.00
    ended on 15 August bills 15/31 of August, 150.00, for 1–15 August.

  The end cannot be before a period already on an invoice or a draft invoice — credit
  that first. A last period already on an invoice or a draft invoice can only be billed
  in full: to pro-rate it, credit it first. The choice is kept on the subscription and
  shown in the **Change log**.
- **Cancel subscription** cancels a draft, or an active subscription nothing of which is
  billed yet. One with invoiced periods is ended instead.

## Sales analysis

**Analysis** (permission to view the sales analysis) has three views, each with a chart,
a table and **Download CSV**, which saves the table as a file a spreadsheet opens. Amounts
are net of VAT and never added across currencies: each chart shows one currency, and with
more than one you pick it above the chart.

- **Quote hit rate** — the quotes sent between **From** and **To**, and how many of them
  are **Accepted**, **Declined**, **Expired**, **Cancelled** or still **Open**, counted and
  summed per salesperson; the **Hit rate** is accepted of sent. Narrow it to one
  **Salesperson**. A quote sent again after a revision counts once, on the day it was last
  sent.
- **Order intake** — the orders confirmed in the period and not cancelled, grouped by
  **Month**, **Salesperson**, **Customer**, **Product** or **Category** under **Group
  by**. Free-text lines are one group, and so are products with no category.
- **Backlog** — today: what confirmed orders have **Ordered, not delivered**, and what
  they have **Delivered, not invoiced**, aged 0–7 days, 8–30 days and over 30 days since
  the delivery was registered. An invoice is due within a month of delivery, so anything
  over 30 days is shown in red.

## The customer's Orders tab

On a customer's page, the **Orders** tab shows four figures per currency, net of VAT —
**Open quotes**, **Ordered, not delivered**, **Delivered, not invoiced** and
**Subscriptions, per year**, the active and paused subscriptions' value over a year — and
below them the customer's open quotes, their orders, **Open** or **All**, and their
subscriptions with each one's value per period. **New quote** and
**New order** start one for this customer. The tab needs access to the Orders app; the
buttons need the permission to make quotes or orders, and are not shown on a merged or
anonymised customer.

The customer's **Timeline** shows when a quote was sent and accepted and an order
confirmed or cancelled ([the timeline](/en/user/customers/#the-timeline)).

## When something is refused

| The app says | What to do |
| --- | --- |
| Changed by someone else / It was changed by someone else. Reload and try again. | Someone changed it since you opened it — saved it, put it on hold, locked it, registered a delivery — or the customer was merged or erased. **Reload**; your unsaved edits are dropped. |
| A line keeps its kind. Remove it and add a new line instead. | Remove the line and add a new one of the kind you want. |
| A confirmed order's line keeps its unit (or VAT code) | Remove the line and add a new one. |
| A line has no price in this currency | Type a price on the line. |
| A product's tax category has no VAT code | Ask whoever manages invoicing to map it under Invoices' VAT codes. |
| The order has no product lines | Add at least one product line before confirming. |
| The customer is disabled or archived | Enable the customer in Customers, or use another. |
| The project belongs to another customer | Pick one of this customer's projects, or none. |
| A quantity cannot go below what is delivered, invoiced or on a draft invoice. | Cancel the rest instead, undo the delivery first, or issue or delete the draft invoice. |
| The price of an invoiced line cannot change | The line is invoiced: leave it as it is. |
| A line with deliveries, invoices or a draft invoice cannot be removed. | Lower its quantity, or cancel the rest. |
| Part of the line is on a draft invoice: its price and discount cannot change until the draft is issued or deleted. | Issue or delete the draft invoice in Invoices, or **Release draft allocations**, then change the price. |
| Part of the order is on a draft invoice. Issue or delete the draft, or choose Release draft allocations in the order's menu. | Do as it says: an order is not cancelled while a draft invoice holds part of it. |
| That is more than is left to invoice. | Someone invoiced the line meanwhile. Reload and invoice what is left. |
| The line cannot be invoiced now: its order is a draft, on hold or cancelled. | Confirm the order, or release it from hold, and invoice again. |
| Something is delivered or invoiced: cancel the rest instead | Use **Cancel the rest**. |
| The order is locked. | Someone with the permission to confirm must **Unlock** it — also before it can be cancelled. |
| The order is on hold. Release it before registering delivery. | Choose **Release**, then register or undo the delivery. |
| Sending is off | Download the PDF and **Mark as sent**, or ask an administrator to choose a send channel. |
| The e-mail could not be sent. Nothing was changed; try again, or download the PDF and mark it as sent. | The mail provider did not take the e-mail: **Documents and mailings** lists the send as **Failed**. Try again, or download the PDF and mark as sent. |
| A document takes at most 500 lines | Split it in two. |
| A warning blocks this | A customer or product on the document has a block warning. Someone who manages the Orders settings can **Override the warning** with a reason, or change the warning. |
| The packaging is not one of the product's | Pick the product again and choose one of its packagings, or enter the quantity in the unit. |
| The final period is already on an invoice or a draft invoice and cannot be pro-rated. | Choose **Full period**, or credit the period — or delete the draft invoice — first. |
