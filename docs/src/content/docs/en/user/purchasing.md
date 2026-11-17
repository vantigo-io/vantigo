---
title: Purchasing
description: The Purchasing app — the purchase order list and its filters; make a purchase order for a supplier and a receiving site, with product lines filled from the supplier's items and text lines; send it by e-mail, mark it as sent or send it again; record the supplier's confirmation; change a sent order and read its history; close a line or the whole order, reopen it, cancel it or copy it; the PDF; the suppliers and what each sells — supplier items, the preferred supplier and the price-list import with its CSV columns; the settings; the purchase proposals from low stock or Manufacturing's buy suggestions, and Create orders with Order… from those screens; the dashboard card and the product page's Suppliers card; receiving the goods — the receive screen and scanning, more or less than ordered, the order's receipts, undoing a receipt and marking a text or service line delivered; the supplier invoices — the order's invoices, Register invoice, matching an invoice from Accounting's Purchase order panel, the differences, what posting, reversing and deleting the invoice do, the last price, and Received, not invoiced with its CSV; and what comes later — inbound EHF, payment files and supplier returns.
sidebar:
  order: 68
sources:
  - apps/purchasing/frontend
  - apps/host/frontend/src/routes/purchasing
  - apps/host/frontend/src/lib/purchasing-access.ts
  - apps/host/frontend/src/lib/dashboard-helpers.ts
  - apps/host/frontend/src/components/app-spotlight.tsx
  - apps/host/frontend/src/routes/products/$productId.tsx
  - apps/host/frontend/src/routes/accounting/supplier-invoices.$supplierInvoiceId.tsx
---

The Purchasing app is where you order goods and services from your suppliers. In
Norwegian the app is **Innkjøp**. A **purchase order** says what you buy from whom, at
what price and when you expect it; a **supplier item** says what a supplier sells of one
of your products, at what price, in which packaging and with which lead time.

A purchase order moves neither stock nor the books. You order and follow up, make draft
orders from [purchase proposals](#purchase-proposals), and when the goods arrive you
[receive them](#receive-the-goods), which puts them in stock in Inventory; when the
supplier's invoice comes, you [match it to the order](#supplier-invoices). Purchasing buys
from the suppliers in [Accounting](/en/user/accounting/#suppliers)'s register and orders
the products of [Products](/en/user/products/) for a site of
[Inventory](/en/user/inventory/), so an installation has all three when it has
Purchasing.

## The app and its sidebar

The sidebar has **Purchase orders**, **Proposals** and **Suppliers** for everyone with **Use Purchasing**,
**Receive** for everyone with **Receive goods**, **Received, not invoiced** for those who
match supplier invoices, and **Purchasing settings** for those who manage the Purchasing
settings. What you can do depends on
your permissions: writing orders, sending them and keeping the supplier items are
separate permissions, so a colleague may see buttons you do not
([Permissions](#permissions)). Everyone with **Use Purchasing** sees the prices; someone
with **Receive goods** alone sees **Receive** and no prices.

## The order list

**Purchase orders** lists the orders newest first, with their **Number**, **Supplier** —
the supplier's reference under the name once it is recorded —, **Status**, **Order date**,
**Expected day**, **Received**, **Invoiced** and the **Net total**, excluding VAT. A draft has no
number yet. **Received** counts the order's goods lines received in full or closed, out of
all its goods lines, and **Invoiced** its lines invoiced in full, out of all its lines, by
the [supplier invoices matched to it](#supplier-invoices). An order can be:

- **Draft** — being written; it has no number and can be deleted.
- **Sent** — sent to the supplier, by e-mail or marked as sent; it has its number.
- **Partially received** — some of the goods are [received](#receive-the-goods), and the
  rest is still to come.
- **Received** — every goods line is received in full or closed, and every text and
  service line is marked delivered.
- **Closed** — ended with something left that will not come.
- **Cancelled** — called off before anything was received.

Two marks show beside the status:

- **Late** — the order is sent and an open line's expected day (the line's own, else the
  order's) is before today.
- **Unconfirmed** — the order was sent more than three business days ago (Monday to
  Friday, public holidays left out) and no confirmation from the supplier is recorded: no
  supplier's reference and no confirmed day.

Narrow the list with the search (the order number, the supplier's name or the supplier's
reference), the status chips — **Draft**, **Sent**, **Partially received**, **Received**,
**Closed**, **Cancelled**, any of them together — and the switches **Late**,
**Unconfirmed** and **Mine** (the orders where you are **Our reference**).

## Make a purchase order

1. In **Purchase orders**, choose **New purchase order**.
2. Pick the **Supplier**. Only an active supplier can get a new order.
3. Check the **Receiving site** — the site the goods are delivered to. It is the settings'
   default site, else the only active site; with several and no default you choose one.
4. Choose **Create the draft**. The order opens as a draft.

From the supplier, the order takes its **Currency** and the **Language of the order** — Norwegian when the supplier's country is Norway or not
given, English otherwise. You are **Our reference** (the buyer) unless you choose a
colleague who may manage purchase orders.

### The header

On a draft, choose **Edit** to change the supplier, the receiving site, the currency —
until a line has a price —, the language, the **Order date**, the **Expected day**, **Our reference**,
**The supplier's contact**, a **Note on the order**, printed on the PDF, and an **Internal
note**, never printed.

The **Delivery address** is the address the settings give the site, or, when they give
none, the company's address with the site's name. A draft shows it as it is now; sending
the order fixes it on the order, with the supplier's name and address.

### Lines

An order has up to 500 lines, of two kinds:

- **Product line** (**Add product**) — search the catalog by SKU or name, or only what the supplier has
  supplier items for. The line takes from the supplier's item what you leave empty: **the supplier's item
  number**, the price — when the item's price is in the order's currency —, the
  **Purchase packaging** while the product still has it, and the **Expected day**, the
  order date plus the item's lead time. The item's price is per its own purchase
  packaging: a line in another packaging takes it converted — the price of a box of 10
  is ten times the unit's —, and stays at 0 when the packaging's size is not known. You
  enter the quantity in the purchase packaging; the packaging's choice names how many of
  the product's unit it holds — **Eske (15 pcs)** —, so 3 of them are 45 pieces on order.
  **Discount %** is a percentage off the price. The line's amount is quantity × price, less
  the discount, rounded to two decimals.
- **Text line** (**Add text line**) — something outside the catalog, such as freight or a service: a
  description, a quantity, a unit and a price.

A quantity under the supplier item's **Minimum quantity**, or not a whole number of its
**Multiple**, is shown as a warning; the order can still be saved and sent. A product
that is **Discontinued** cannot be put on a new line; a service product can be ordered.

All amounts exclude VAT, in the order's currency, and the PDF says so. A line's amount and
the order's total are at most 999 999 999 999.99; a larger one is refused.

Writing orders needs **Manage purchase orders**: without it, **New purchase order** and
**Edit** say so instead of offering a form. If somebody changes the order while you edit
it, **Save** says so and offers **Load again**, which opens the order as it now is.

## Send the order

**Send** needs **Send purchase orders**, at least one line, and e-mail set up: Purchasing
on a mail channel in its [settings](#settings) and Communications in use.

1. On the draft, choose **Send**.
2. Check the recipient — the supplier's e-mail in the register, or another address for
   this order — and, if you like, add copies, a subject and a message to the supplier.
3. Send. The order gets the next number in the series, its PDF is stored, and the PDF is
   mailed to the supplier with a short cover mail in the order's language asking them to
   quote the order number on their invoice.

The e-mail is part of the send: it is queued together with the order, so a sent order
always has its mail. If it does not go out at once — the mail provider does not take it,
or the PDF could not be stored —, the order is still sent and has its number: the dialog
closes and the app warns that the order is sent and its e-mail queued, and the mail is
sent by itself as soon as it can, tried again for a while. The order page says the last
mail failed and is retried; **Send again** sends a new copy.

**Mark as sent** does the same without a mail — for an order placed by phone or in the
supplier's web shop: the order gets its number and its PDF is stored. When e-mail is not
set up, **Mark as sent** is what the order page offers.

Choosing **Send** twice, or a lost connection followed by a retry, does not send the
order twice: the dialog sends the same request again, and Purchasing answers the first.

## After sending

A sent order can still change, and every change is written to its **History** with who
made it and when:

- new lines;
- a line's quantity, price, discount and expected day — but not its product, kind or
  packaging;
- the order date, the expected day, our reference, the supplier's contact, the notes and
  the language;
- the receiving site, while nothing is received.

The supplier and the currency stay as sent. A closed line does not change, and a closed,
received or cancelled order is not edited.

On a sent order, **Edit** opens the order for these changes; **Save** writes them.

**Send again** stores a new PDF of the order as it is now, marked **Revised**, with the
next version number, and mails it to the supplier; untick the mail to store it only. The
order's **History** has three parts: **Changes**, **PDFs** — every stored PDF with its
version — and **Mailings**, each with its recipient and whether it was **Sent**,
**Queued** to be tried again or **Failed**. A mailing that did not go out says why in your
words, with the mail provider's own message beside it. A queued mail of an earlier version
is not sent once the order is sent again: it is **Failed**, as a newer version was sent
first.

## Record the supplier's confirmation

When the supplier confirms the order, choose **Record confirmation** on a sent order and
enter **the supplier's reference** (their order number) and the **Confirmed day** — for
the whole order and, if the supplier confirms lines differently, per line. The
confirmation is a note on the order; it does not change its status. It ends the order's
**Unconfirmed** mark. Empty the fields to remove it.

## Receive the goods

When the goods arrive, choose **Receive** on a sent or partially received order — or, at
the goods reception, **Receive** in the sidebar: it lists every sent order with goods
still to come, the earliest expected first, with the supplier, the receiving site, the
expected day, **Late** when it is overdue, and how many of its goods lines are left —
*2 of 3* —; choose the order's number or **Receive** on its row. Receiving needs
**Receive goods** alone, so someone at the goods reception can receive without managing
orders or seeing what they cost. The screen, **Receive order 4501**, shows no prices. It has a row for each open
goods line — *Open 60 of 180 m, 120 received* — with:

- the **Quantity**, filled in with what is still open on the line, in the product's unit,
  with a decimal point or comma as you type it; **Receive … in** shows it in the line's
  purchase packaging when the line has one — what you typed stays as you typed it, so
  switching back loses nothing, and a quantity typed in the packaging is received as that
  many packagings;
- the **Location** on the receiving site the goods are put on: the item's
  [primary location](/en/user/inventory/#set-an-items-primary-location-and-reorder-point)
  at the site when the item lies in stock there, else the location where most of it lies,
  else the site itself;
- for an item kept in lots, the **Lot number** — left empty with a supplier's lot or a
  date given, Inventory numbers the lot; with nothing given the receipt asks for the lot —,
  the **Supplier's lot**, the **Production date**, **Best before** and **Expiry**; a date
  left empty follows from the product's days, as on
  [Inventory's receipt](/en/user/inventory/#lots-on-a-receipt-a-move-and-an-adjustment);
- for an item kept in serial numbers, the **Serial numbers**, one per line, one for each
  unit.

**Split** adds a row for the same line, to put part of it on another location or in
another lot; **Remove the row** takes one away. A row at 0 is left out, so a line that has
not come is simply left at 0. The **Delivery note** — the number on the supplier's
delivery note — and a note belong to the whole receipt.

Choose **Receive**. The receipt gets the next number in a series of its own — *Receipt 12
is posted.* — and the goods are put on their locations in one stock movement of kind
**Receipt** in Inventory, which links back to the order. Receiving posts nothing to the
books. When you hold **Receive goods** without **Use Purchasing**, you are taken back to
**Receive** after the receipt; otherwise to the order. An item [inspected on receipt](/en/user/inventory/#inspect-on-receipt) has its lots
put in quarantine, as on Inventory's own receipt, and a product discontinued since it was
ordered is still received. What is received stops counting as *on order* in Inventory at
once.

Choosing **Receive** twice, or trying again after a lost connection, does not receive the
goods twice: the screen sends the same receipt again, and Purchasing answers with the
first — even when that first receipt has since finished the order or been undone. When
two people receive the same order at the same moment, one waits for the other, and the
second counts what the first received.

When the supplier is a person, the stock movement names neither them nor their delivery
note: Inventory keeps its movements when a supplier's personal data is erased.

### Scan the goods

**Scan an item** takes what a barcode scanner types and the Enter it sends: the item's
barcode, the barcode of one of its packagings, or the SKU. The first scan switches the
screen to counting: every filled-in quantity is cleared, and each scan then adds to its
line — one packaging for a packaging's barcode, one of the unit for the item's own. A
GS1-128 code, such as a pallet label's, also fills in the lot number and the production,
best-before and expiry dates; a second lot of the same line opens a split row of its own,
and a serial number is added to the row's serial numbers. Fields of the label the screen
does not use — a net weight, a pallet's SSCC, a count — are skipped. When the lot cannot be
read for sure — the scanner left out the separator after it, so what follows might be part
of it — the item is counted on a row without a lot and the screen asks you to type the
lot. A code without a serial number on an item kept in serial numbers says **Scan the
serial number** and counts nothing. A code that names no open line of the order says
**Not on this order**.

### More or less than ordered

More than is open on a line is asked about first: **More than ordered — receive anyway?**
names the lines, and **Receive anyway** receives it, so the line has more received than
ordered.

Less is fine. The order is **Partially received**, the rest stays open on the lines for a
later receipt, and no back order is made; when the rest will not come, **Close line** or
**Close** ends it. When nothing is left open the order ends, whichever step finished it: it
is **Received** when every goods line came in full and every text and service line is
marked delivered, and **Closed** when a line was closed with something still missing —
also when the last receipt or delivered mark comes after that line was closed. A closed
order is not received: **Reopen** it first.

### The order's receipts

The order page's **Receipts** lists the order's receipts, newest first: **Receipt 12**,
the day, who received it, the delivery note, **Posted** or **Undone** — *Undone … by …*
—, and each line with its quantity, its location, and its lot or serial numbers with
their dates. **Stock movement** and **Undo movement** open the movements in Inventory for
those who may see them (**View stock**). A receipt shows no prices. With none it says
*Nothing is received on this order yet.*

### Undo a receipt

**Undo** on a posted receipt takes it back — a wrong count, or goods that failed their
inspection. Add a note if you like. The goods are taken off the locations and lots they
were put on, whatever holds the lots have, the quarantine the receipt put on them is
released, and the lines count them as not received again. Inventory shows it as a
movement of kind **Receipt reversal**; the receipt stays on the order, marked **Undone**
with who and when.

An undo is refused when the goods no longer lie where they were received — moved or
delivered since —, when a location they were put on has been archived, and when the
receipt is undone already. A received order becomes partially received again, or sent
when nothing else on it is received or delivered; a closed order stays closed.

### Mark a text or service line delivered

A text line or a service is never received into stock. When it is delivered — the freight
carried, the service done — choose **Mark delivered** on the line of a sent or partially
received order; the line then says *Delivered …*. **Not delivered** takes the mark off
again, on a received order too, which is then partially received — or sent, when nothing
else on it has come. A goods line is received instead, and a closed line is not marked.
**Not delivered** is refused while a supplier invoice bills the line: unmatch the invoice
first.

Receiving, undoing and marking a line delivered need **Receive goods**.

## Supplier invoices

When the supplier's invoice comes, it is a supplier invoice in
[Accounting](/en/user/accounting/#supplier-invoices), and Purchasing **matches** it to the
order: which order lines it bills, and how much of each. The match is what **Invoiced**
counts, and it shows where the invoice and the order differ. An invoice can bill several
orders of its supplier and an order several invoices, but each line of an invoice bills one
order line. Everything here needs **Match supplier invoices**; reading the matches needs
only **Use Purchasing**.

### The order's supplier invoices

The order page's **Supplier invoices** lists the invoices matched to the order — **Invoice
55120** or **Credit note …**, its date, **Provisional** while the invoice is a draft in
Accounting and **Posted** once it is posted — with, per invoice line, the order line, the
quantity **Matched**, the **Order price per unit** and the differences. Those who may use
Accounting (**Use Accounting**) also see the **Invoice line** and the **Invoice price per
unit**; others see the quantities alone. With none it says *No supplier invoice is matched
to this order.* Each order line shows what is ordered, received and invoiced.

### Register invoice

**Register invoice** on a sent, received or closed order makes the supplier invoice in
Accounting for you, matched to the order already:

1. Type the **Supplier's invoice number** and the **Invoice date**, and if you like the
   **Due date** (left empty, the supplier's payment terms after the invoice date) and the
   **Invoice total** as printed, VAT included.
2. For an order in another currency than NOK, give the **Exchange rate** or the **Total in
   NOK**.
3. **Register invoice**. Accounting gets a draft with one line per order line that is
   received — a text or service line: marked delivered — and not invoiced yet, at the
   order's prices, entered net on the supplier's default account and tax code, and the
   order number as its reference.

Then open the draft in Accounting, check it against the invoice, attach the invoice's file
and post it. Left without a total, the draft's total is the lines' net: correct it to the
invoice's before posting, as Accounting posts an invoice only when its lines make its total.
A draft holds 200 lines; an order with more is invoiced in parts — the dialog says how many
lines are left, and the next **Register invoice** takes them. What one draft took counts as
invoiced, so a second registration takes only the rest, and an order with nothing left to
invoice says *Nothing on the order is received and not invoiced.*

**Register invoice** needs **Post vouchers** in Accounting as well as **Match supplier
invoices**. It is refused while the books are not set up, when the supplier has no default
account — *Open the supplier in Accounting* takes you there to set it —, and when the
supplier's invoice number is entered already.

### Match an invoice from Accounting

An invoice a bookkeeper typed in Accounting is matched from its page there: the **Purchase
order** panel shows the orders of the invoice's supplier whose number its reference names
(**Orders the reference names**, each with how many lines are left to invoice), and the
invoice's matches with their differences.

- **Match all received** pairs the invoice's unmatched lines, in order, with the order's
  lines that have something received and not invoiced, in order, each for what is left.
- **Allocate…** matches line by line: choose the order — one the reference names, or
  **Another order of the supplier** by its number —, then for each invoice line the
  **Order line** and the quantity, and **Match**. An invoice line matched to the same order
  line again takes the new quantity.
- **Unmatch** takes one line's match back, or a whole order's.

Quantities and amounts take a decimal comma or a point. More than is received and not invoiced on a line asks first — *Match anyway* matches it,
and the line then shows *Invoiced more than received*. A credit note lowers what is invoiced
and is never matched for more than is invoiced on a line. An invoice of another supplier
than the order's, in another currency than the order's, or reversed, is not matched; an
invoice line matched to another order line is unmatched first. A reversed invoice is matched
to nothing, but a match a credit note held on it stays, and **Unmatch** takes it back. Those
without **Use Accounting** see the panel only for an invoice matched to an order, with the
matched quantities alone — no invoice lines, amounts or suggested orders.

### The differences

A match shows its differences as warnings; they never stop a posting:

- **Invoiced more than received** — the order line is invoiced beyond what is received.
- **Invoiced more than ordered** — beyond what is ordered.
- **Price differs** — the invoice line's net differs from what the order makes of the same
  quantity by more than the **Price tolerance** in the [settings](#settings).
- **Currency differs** — the invoice is in another currency than the order.

The panel and Accounting's badge count them, a line's own — invoiced more than received or
ordered — once however many invoice lines bill it.

### When the invoice is posted or reversed

A draft's match is **Provisional**; it is **Posted** when the invoice is posted in
Accounting, and the prices are judged again from the posted lines. Each matched product
line's price per unit on the posted invoice then becomes the supplier item's price, marked
as set by the invoice on its date — unless the invoice is in another currency than the
supplier item, or the price was set after the invoice's date, or set by a person or an
import on that date. A posted invoice whose supplier was changed on the draft no longer
bills the order, and its match is removed. An invoice posted before it was matched is
matched as posted.

**Reverse and copy** in Accounting takes the match off the reversed invoice and gives it to
the copy, provisional again, so what is invoiced stays as it was. An undo of a receipt is
refused while what it brought in is invoiced: unmatch the invoice first.

### When the draft is deleted

Deleting a draft supplier invoice in Accounting — one you typed, one **Register invoice**
made, or the copy **Reverse and copy** made — takes its matches off the order, so what they
counted is to invoice again and **Register invoice** takes it once more. A match a credit
note holds stays until the credit note is unmatched; then **Unmatch** on the order's
invoice takes it back, though Accounting no longer has the invoice.

### Received, not invoiced

**Received, not invoiced** in the sidebar, for those who match supplier invoices, lists per
supplier and order line what was received **As of** a day — and not taken back by then —
less what posted invoices dated by that day bill, at the order's net price per unit, with a
total per currency. A text or service line counts once marked delivered by then. It is the
basis for an accrual at a period's end; nothing is posted from it. **Download CSV** gives
the same list as a file with semicolons and the decimal comma.

## Close a line or an order

- **Close line** ends one line: what is left on it will not come. When no line is left
  open, the order is closed — or **Received**, when everything on it was received or
  delivered.
- **Close** ends a sent order with what is left on it: every open line is closed and the
  order is **Closed**. What was left stops counting as *on order* in Inventory.
- **Reopen** opens a closed order again, with the lines its closing ended; a line you
  closed by itself before stays closed. The order is then partially received when
  anything on it is received or marked delivered, else sent.

Closing needs **Manage purchase orders**.

## Cancel an order

**Cancel order** calls off a sent order before anything is received; a draft is deleted
instead. When the order was mailed, the supplier is sent its cancellation — a PDF marked
**Cancelled** — unless you untick **Send the supplier the cancellation**. An order marked as sent is
cancelled without a mail. Cancelling needs **Send purchase orders**.

## Delete a draft

**Delete draft** removes a draft; it has no number, so nothing is left of it. A sent order
is cancelled or closed instead. Deleting needs **Manage purchase orders**.

## Copy an order

**Copy** makes a new draft from any order, for the same supplier and site, in the
supplier's currency now and with today's prices from the supplier items. A product that
is now **Discontinued** is left out; text lines are copied as they are. A purchase you
make again and again is a copy of the last one.

## The PDF

The PDF has your company's name and address, the supplier's, the delivery address, the
order number and date, our reference, the lines with their quantities, prices and
amounts, the total, *Amounts exclude VAT*, a line asking the supplier to quote the order
number on the invoice, the note on the order and the settings' text under the order. It
is in Norwegian or English by the order's language.

On a draft, **Preview PDF** prints the PDF as it is now, marked **Draft**, and stores
nothing. On a sent order, **Download PDF** gives the PDF stored when it was sent, or last
sent again; a revised one is marked **Revised**, a
cancellation **Cancelled**.

## Purchase proposals

**Proposals** in the sidebar lists what should be bought now, one row per product and
site. Where [Manufacturing](/en/user/manufacturing/#replenishment-suggestions) is on, the
rows are its buy suggestions from the last replenishment run, which count what is being
made and what sales orders need; where it is not, they are Inventory's
[low-stock list](/en/user/inventory/#the-low-stock-list). A product Manufacturing makes is
never proposed for purchase. The line under the list says which of the two it is:
*From Manufacturing's buy suggestions.* or *From Inventory's low stock.*

Each row shows:

- **Product** and **Site**.
- **Needed** — what is still to buy, in the product's unit: the need less what is already
  on draft purchase orders for the product and the site, and less what was ordered for the
  site since the suggestions were computed. When something was taken off, the row says so:
  *33 needed, 10 on drafts, 0 ordered since*. A product covered so is no row.
- **Supplier** — every active supplier with a supplier item of the product, the preferred
  first, with the supplier's item number. Choosing another changes the quantity and the
  price.
- **Quantity** — in the supplier's purchase packaging, rounded up once: to whole
  packagings, then to the supplier item's minimum, then to its multiple. Under it, the same
  in the product's unit. You may type another quantity, more than 0 with at most three
  decimals, with a point or a comma (*2,5*).
- **Price** — the supplier item's price per purchase packaging, or *No price*. A line
  takes the price only when it lands on a draft in the price's currency. A price that is
  not in the supplier's currency is shown in orange: a new draft is in the supplier's
  currency, so there the line gets no price.
- **Order by** — the day it is needed, or today, less the supplier's lead time, marked
  **Late** when it has passed.

A product with no supplier item is listed apart under **No supplier**: it cannot be
ordered here. Add a supplier item on its supplier's page ([Supplier
items](#supplier-items)) and it gets a supplier on the list. A discontinued product is
listed there too, marked **Discontinued**, whatever supplier items it has: it is not
ordered any more.

### Create orders from proposals

1. Tick the rows to order, and change the supplier or the quantity where you need.
2. Click **Create orders**. It says how many rows are ticked: *Create orders (3 rows)*.

One draft purchase order is made per supplier and site — or, when you already have a draft
for that supplier and site, the lines are added to the oldest one, where a line of the
same product in the same packaging gets the quantity added. Each line takes the supplier's
item number, packaging, price and expected day, as a new line in the editor does
([Lines](#lines)). A line added to a draft you already had takes its price only when that
draft is in the price's currency; when it is not, the page says beside the draft how many
lines got no price, so you can set it there. The page then links each draft, *Draft for
Norsk Stål AS*: open it, check it and send it ([Send the order](#send-the-order)).
Nothing is sent from the proposals, and nothing is ordered by itself. The draft's history
shows **Lines from proposals**.

What is on a draft is taken off the proposals at once. Each ticked row is sent with the
need the page showed, and a row is ordered only when exactly that need still remains when
the orders are made. When it does not — the row went on a draft, was ordered, or its need
changed, since the list was loaded, whether by your own earlier click or by a colleague's
— nothing at all is made, the page says so on the row and above the list, and loads the
list again; check the rows and click again. A row whose supplier is no longer offered
after the list is loaded again is unticked, and its quantity goes back to the proposal.

Clicking **Create orders** again with the same rows after an answer that never came does
not order twice: the second click gets the first answer, even if one of its drafts was
deleted since.

**Order…** on a row of Inventory's low-stock list and on a buy suggestion in Manufacturing
opens the proposals for that product and site; **Show all** goes back to every row. It is
there when Purchasing is on and you may manage purchase orders.

Seeing the proposals needs **Use Purchasing**; creating orders from them needs **Manage
purchase orders** as well.

## Suppliers and their items

**Suppliers** lists the suppliers in Accounting's register, with how many supplier items
and open orders each has. A supplier's page shows its entry — number, name, organisation
number, address, e-mail, currency and payment terms — read-only, and its supplier items.
**Open in Accounting** leads to Accounting's page for the supplier, for those with **Use
Accounting**. The list shows the first 50 suppliers that match; search to find the
others.

If you may keep the suppliers — **Keep the suppliers**, or **Post vouchers** with **Use
Accounting** — **New supplier** on the list adds a supplier to the register from
Purchasing, and **Edit supplier** on a supplier's page changes it. **Keep the suppliers**
alone is enough for both: the form opens on the register's entry as Purchasing reads it.
Without **Post vouchers** and **Use Accounting** the form has no bank account, IBAN, BIC,
default account or tax code; see [Suppliers](/en/user/accounting/#suppliers) for what
each permission lets you change. If the supplier cannot be read, **Edit supplier** says so.

### Supplier items

A supplier item is what one supplier sells of one product variant; a supplier has one
item per variant. It holds:

- **the supplier's item number** and its name for the item, both optional;
- the **Price** per purchase packaging, in the supplier's currency, up to four decimals,
  with the day it was set and whether a person or the import set it;
- the **Purchase packaging** — the product's unit or one of its packagings;
- the **Lead time** in days, the **Minimum quantity** and the **Multiple**, in purchase
  packagings, each optional.

The first supplier item of a variant is its **Preferred** supplier. Marking another one
preferred moves the mark there; a variant always keeps one preferred item while it has
any, and when the preferred one is deleted, the oldest of the others takes over.

When the supplier's currency in the register changes, the item keeps its price and says
the currency differs until someone sets the price again. When the packaging an item names
is deleted from the product, the item falls back to the unit and says so.

**Add supplier item** on a supplier's page adds one; each item can be edited and deleted.
The page shows up to 200 items and says how many there are when the supplier has more;
search to find the others.
Adding, changing and deleting supplier items needs **Manage supplier items**. Deleting
one does not change the orders that took from it.

### Import a price list

**Import price list** on a supplier's page reads the supplier's prices from a CSV file —
UTF-8 or Windows-1252, separated by semicolons, commas or tabs — of at most 5000 rows. The
header names the columns, in any order and case:

| Column | What it holds |
| --- | --- |
| `sku` or `barcode` | Required, one of them: the product variant's SKU, or the barcode of a variant or of one of its packagings — a packaging's barcode also sets the purchase packaging |
| `supplier_item_number` | The supplier's item number |
| `supplier_item_name` | The supplier's name for the item |
| `price` | The price per purchase packaging, in the supplier's currency |
| `packaging` | The name of one of the product's packagings |
| `lead_time_days` | The lead time in days |
| `minimum_quantity` | The minimum quantity, in purchase packagings |
| `multiple` | The multiple, in purchase packagings |

1. Choose **The file**, then **Check the file**. Nothing is written yet: each row says whether it would
   create a supplier item or update one, or why it fails — an unknown or ambiguous SKU or
   barcode, an unknown packaging, a price or quantity that is not a number, a row that
   repeats another.
2. When every row passes, choose **Import**. Every row is written, or — if any fails — none.

An empty cell keeps what the supplier item has. A price may be written as `1234.50`, or the
Norwegian way, `1234,50`, `1 234,50` or `1.234,50`. Prices from the import are marked as
set by the import.

## Settings

**Purchasing settings** needs **Manage Purchasing settings**:

- **First order number** — where the order series starts. Once numbers are used it can
  only be raised above the last one.
- **Default receiving site** — the site a new order gets.
- **Mail channel** — the Communications channel orders are sent from. Without one,
  orders are marked as sent instead.
- **Text under the order** — printed at the bottom of every purchase order, up to 4000
  characters.
- **Delivery addresses** — per site, tick **Own delivery address** to give the address the
  supplier delivers to; without one, the company's address with the site's name is used.
  The country is a two-letter code, such as `NO`.
- **Price tolerance (%)** — how far, in percent, an invoice's price may differ from the
  order's before the match shows *Price differs* ([The differences](#the-differences)); 2 %
  unless changed.

If somebody saved the settings while you changed them, **Save** says so and offers **Load
again**.

## The dashboard and the product page

Purchasing's card on the dashboard, for those with **Use Purchasing**, counts the orders
that are late and those unconfirmed. When orders where you are **Our reference** are
unconfirmed, the dashboard's attention list says so — *2 of your purchase orders are not
confirmed by the supplier* — and opens the order list. Purchasing's pages can also be
found from the search in the top bar.

On a product's page in Products, the **Suppliers** card lists the supplier items of the
product's variants — which supplier sells it, at what price and which is preferred — when Purchasing is in use and you have **Use
Purchasing**. It shows up to 200 and says how many there are when there are more.

When a supplier item or a purchase order line names a product variant, Products does not
delete it, and a product with an open goods line on a draft or sent order does not become
a service.

## When a supplier is deleted or anonymised

A supplier that any purchase order or supplier item names cannot be deleted in
Accounting; it can be made inactive, and its open orders stay. A supplier who is a person
is not anonymised while a draft, sent or partially received order names them. When a
person supplier is anonymised, their name, address, e-mail and contact are removed from
the orders, the addresses, messages and refusals from the orders' mailings, and the
contact and notes from the orders' history — which keeps that they changed —, the
delivery notes and notes from the orders' receipts, and the orders' stored PDFs are
deleted.

## Permissions

| Permission | Lets you |
| --- | --- |
| **Use Purchasing** (`purchasing:access`) | Open the app and read every order, supplier and supplier item, prices included, and the proposals, and download the PDFs. |
| **Manage purchase orders** (`purchasing:orders-manage`) | Write and delete drafts, create orders from proposals, change sent orders, record the confirmation, close a line or an order, reopen and copy. |
| **Send purchase orders** (`purchasing:orders-send`) | Send, mark as sent, send again and cancel. |
| **Receive goods** (`purchasing:receive`) | See **Receive** — the orders to receive —, receive goods against purchase orders, undo a receipt, and mark a text or service line delivered. Alone, without **Use Purchasing**, it shows no prices. |
| **Manage supplier items** (`purchasing:supplier-items-manage`) | Add, change, delete and import supplier items. |
| **Match supplier invoices** (`purchasing:invoices-match`) | Match and unmatch supplier invoices to orders, register an invoice from an order — with **Post vouchers** in Accounting too —, and read **Received, not invoiced**. |
| **Manage Purchasing settings** (`purchasing:settings`) | The settings. |

## What comes later

- **Inbound EHF and payment files** — a supplier's invoice arriving as EHF with the order
  reference, and paying it from a payment file; both wait for Accounting's next phase.
- **Supplier returns** — goods sent back to the supplier.

The rules behind every screen are in the [Purchasing reference](/en/reference/purchasing/).
