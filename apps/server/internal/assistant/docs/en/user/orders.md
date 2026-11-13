---
title: Orders
description: The Orders app — make a quote, from the app, a customer's page or a template; add product, free-text, section and note lines, optional lines and discounts; send it or mark it as sent, revise, accept, decline, cancel and copy it; make an order, confirm it, change it after confirming, put it on hold, lock it, cancel it or its rest, register what is delivered, send the order confirmation and print a pro forma; reserve stock at a site, make, pick and post deliveries with their pick list and packing slip, pick goods kept in lots or serial numbers and change a lot, and follow the back orders; ship a delivery with Bring — packages, prices, booking, labels, the waybill, tracking and cancelling — and add a shipping line with the carrier's estimate; invoice it, see its invoices and release what draft invoices hold; make a subscription, activate, change, pause, resume, end and cancel it, accept a quote as one and see its periods; the change log; lines in packagings and from the variant grid; customer and product warnings and overriding a block; the sales analysis; the customer's Orders tab; and what a refusal means.
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

The sidebar has **Quotes**, **Sales orders**, **Deliveries** and **Subscriptions** for everyone with access to the app,
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
primary delivery address; any of the customer's addresses may be chosen, and one that is not
a delivery address on the customer — a visiting or postal address, say — is accepted with
the yellow note *This is not a delivery address on the customer.*), **Contact person**, **Your reference**, **Salesperson** (**Make me the
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
- **Shipping** (*Add shipping*, on a quote or an order) — the freight the customer pays:
  quantity 1, a price and a VAT code, the standard rate unless you choose another.
  **Estimate** asks the carrier for a price for the saved document's delivery address and
  the weight of its goods, adds the markup from the Orders settings and fills in the price,
  marked **Carrier estimate**; when the settings name a freight product, the line takes its
  name. Its VAT code follows the goods: theirs when they share one, else the one most of
  the goods' amount carries. Type over the price to make it your own. Estimate needs the
  document saved, a carrier account (see [Shipping](/en/admin/shipping/)) and the
  permission to manage the quote or the order. The line is counted as
  delivered when the order's first shipment is booked, and is invoiced like any line.

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
optional lines and no valid-until day. When you reach the page from **Create order** on a
customer's *Wants to buy* answer in a Messenger conversation, it opens with the customer and
the product the customer asked for: the product's only variant becomes the order's first
line (quantity 1), and for a product with several variants you pick one — or none — before
you choose **Create the order**.

When Inventory keeps stock, a new order also gets a **Site** — the warehouse its goods are
reserved at and delivered from — from the Orders settings' **Default site**. Change it in
the order's header while the order is a draft; a confirmed order's site can be changed to
another site until it has a delivery, and its reservations move with it.

## Sell a kit

When [Manufacturing](/en/user/manufacturing/#make-a-kit) is on, a product whose default
bill of materials is a **kit** is sold as one line and delivered as its components.

1. Add the kit's product to the order as any product line, at its quantity and price.
2. Save. Under the kit's line the order shows a line per component — its quantity, the
   kit's times how many one kit holds, and how many one kit holds. The components are
   copied onto the order when the kit's line is added: a later change of the kit in
   Manufacturing does not reach it.
3. Change the kit's quantity and save: its components follow. Remove the kit's line and
   they go with it. The components are not edited themselves.

The kit's line carries the price; its components are priced 0, are not invoiced on their
own and are not printed with prices on the order confirmation. Each component is
reserved, picked — with its lots, when it is kept in lots — and delivered like any goods
line; the kit itself never is. On a confirmed order at a site the kit's **Available** is
how many kits its components make of what is available: the least, over its components,
of what is available of each divided by how many one kit holds. The kit counts as
delivered the whole kits its components' deliveries make, and is offered to invoice
alone; its invoice covers its components, and the order counts as invoiced and done by the
kit's line. The pick list prints the components only; the packing slip prints the kit with
its components under it.

**Register delivered** treats a kit by how the order is delivered. On an order delivered by
delivery documents the kit's line is not offered — its components are, and the kit counts
the whole kits they make. Taking a component back is refused when it would leave fewer
whole kits delivered than are invoiced or in a draft invoice. On an order marked delivered by hand (no site, or Inventory not
keeping stock) the kit's line is offered and its components are not: registering the kit
registers each component with it, the kit's quantity times how many one kit holds, and
taking it back takes them back.

When the components were delivered unevenly — more of one than of another — and you
**Cancel the rest**, the kit keeps the whole kits delivered and the rest of it is
cancelled. A component delivered beyond those whole kits stays delivered and is never
invoiced, since only whole kits carry a price: the change log says **Delivered beyond whole
kits, not invoiced** on that component, with how much. The order is done when the kept kits
are invoiced.

When something is refused, the line named is the kit's line, also when the reason is one of
its components — for example a component short at the site on **Confirm**.

Without Manufacturing, a kit's product is sold as an ordinary goods line, reserved and
delivered itself.

## Confirm an order

**Confirm** on a draft order freezes its customer details and prices, stores the order
confirmation and makes it **Confirmed**. It needs the permission to confirm orders. It is
refused when the customer is disabled, the order has no product lines, a line has no
price in the order's currency, or a product's tax category has no VAT code — map it in
Invoices first ([Products' tax categories](/en/user/invoices/#products-tax-categories)).
A block warning on the customer or a product stops it too, until someone who manages the
Orders settings overrides it ([Warnings](#warnings-on-a-customer-or-a-product)).

On an order with a site, **Confirm** also reserves each goods line's quantity at the site,
so no other order can take it. When the site does not have enough available, the confirm
is refused and **Not enough available** lists the short lines with what is available of
each. Choose **Confirm without reserving** to confirm the order all the same — the change
log records it — and reserve later with **Reserve now**, once the goods are in.

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

On an order holding reservations, a change moves them along: a larger quantity reserves the
difference — refused when the site does not have it available — a smaller one gives the
rest back, a new goods line reserves its quantity, and a removed line gives its reservation
back. A line whose product changes gives back what it held of the old product and reserves its
quantity of the new one — refused when the site does not have it available. While Inventory
keeps no stock, a change reserves nothing more but keeps what the order holds, giving back only
what a smaller quantity no longer needs.

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

**Cancel the order** gives back everything the order holds reserved and cancels its draft
deliveries. **Cancel the rest** gives back what is no longer left to deliver, and takes the
lines with nothing left to deliver out of the draft deliveries — a draft left empty is
cancelled. A line invoiced beyond what is delivered still has that much to deliver: it keeps
its reservation for it, and stays on its drafts.

Neither cancel is taken while a draft invoice holds part of the order: issue or delete the
draft first, or [release what it holds](#release-what-draft-invoices-hold).

Holding, locking and registering deliveries do not change the version.

## Register what is delivered

When the order has no site, or Inventory keeps no stock, and for services and lines without
a product, register deliveries by hand on a confirmed order that is not on hold. Goods on an
order with a site are delivered with a [delivery](#deliveries-and-stock); registering them
by hand still works, for a correction, and gives back as much of the line's reservation as
you register.
**Register delivered** asks how much of each line is delivered now (**Deliver now**); a
line left at 0 is not changed, and you cannot deliver more than is left. **More actions** →
**Undo delivered** takes quantities back (**Take back**); on a line invoiced on delivery, not
below what is invoiced or on a draft invoice. Each line shows its progress —
"3 of 5 delivered" — and the order its delivery status: **Not delivered**, **Partly
delivered** or **Delivered**.

## Deliveries and stock

When Inventory keeps stock, an order with a site is delivered from that site with a
**delivery** (*leveranse*). Making, picking, posting and cancelling deliveries needs the
permission to manage deliveries.

### What each line holds

On a confirmed order at a site, each goods line shows **Reserved** — what is reserved for it
at the site — and **Available** — what of the product is at the site and not reserved for
any order. Available can be negative after a stock correction in Inventory. When a line
holds less than is left to deliver, **Reserve now** reserves the rest; it is refused when the
site does not have it available.

### Goods made to order

When [Manufacturing](/en/user/manufacturing/#make-to-order) is on and you hold Use
Manufacturing, a goods line whose product has a bill of materials shows what is being
made for it: each **manufacturing order** — its number, which opens it, its status, what is
produced of its quantity, and when it is planned to finish. A product made to order gets
its manufacturing order by itself when you confirm the order; for any other product made
by a bill of materials, **Create manufacturing order** on the line opens Manufacturing's
new order filled in from the line. It needs the permission to manage manufacturing
orders, and is offered while the line has none. When a product made to order got no
manufacturing order, the line says **No manufacturing order made:** and why — the bill of
materials has no operations and the order names no site, say, or making it failed and the
manufacturing order must be created by hand.

When a manufacturing order made for the line is closed, the line says how much was
**produced, ready to reserve**: choose **Reserve now** to reserve it. When the goods were
made at another site than the order's, the line says so: move them to the order's site in
Inventory first. Cancelling the order does not cancel its manufacturing orders: they are
marked in Manufacturing for someone to decide.

### Make a delivery

1. On the confirmed order, the **Deliveries** card lists its deliveries. Choose **Create
   delivery**. It is disabled while the order is on hold, when nothing is left to deliver,
   and while you have unsaved changes.
2. The new draft delivery opens with every line that is left to deliver, at what can go
   from the site: what the line holds reserved, and what is available beside it. Each line
   is picked from the first location that holds all of it — the item's
   [primary location](/en/user/inventory/#set-an-items-primary-location-and-reorder-point)
   at the site first, when it has one.
3. Change a line's **Quantity**, or set it to 0 to leave it out, and choose its **From
   location** among the site's locations holding the product — its primary location
   first — then **Save**.
4. **Pick list** prints the lines sorted by location, with every location holding each
   product, its primary location first, for whoever picks the goods.
5. **Post delivery** takes the goods off the chosen locations in Inventory, adds the
   quantities to the order's delivered quantities and stores the **Packing slip** —
   the lines, what was ordered, what this delivery takes, what is left as back order and
   the delivery address. It needs every line picked, and is refused when a location no
   longer holds what it should take.

A posted delivery does not change. A mistake is corrected with a stock correction in
Inventory and **Undo delivered** on the order line. A draft delivery can be cancelled; its
quantities are open on the order again.

### Deliver goods kept in lots

A product kept in [lots or serial numbers](/en/user/inventory/#lots-and-serial-numbers) is
picked by lot instead of by location: its line says **Picked by lot**, and the lots it is
taken from are listed under it with each lot's **Expiry**, **Location** and **Quantity**.

1. When the delivery is made, the lots are chosen for you: the one that expires first,
   then the oldest, skipping a lot on hold and an expired one; a serial number is taken
   whole. A line may take several lots, at different locations.
2. When the lots do not hold enough, the line keeps its quantity and says **Short by** what
   is missing. **Post delivery** waits until the lots add up to the line's quantity: lower
   the quantity, or choose more.
3. To take another lot, choose it in **Change lot**: the site's lots of the product, the
   one that expires first at the top, each with its expiry, location and how much lies
   there. On a line that is short, the lot covers what is missing; otherwise it replaces
   the lots. Change a lot's quantity in its row, or remove it with the bin. A lot in
   quarantine or blocked cannot be chosen.
4. An expired lot can only be delivered by someone with the permission **Use expired
   lots** (`inventory:expired-override`): tick **Use expired lot** under the line, then
   choose it in **Change lot**; it is marked **Expired**. Without the permission it is
   shown but cannot be chosen, and a delivery that names one is refused. Someone without
   the permission can still save the delivery with that lot as it was chosen, but not
   change its quantity.
5. A serial number is delivered one at a time: its quantity is 1.
6. **Save**. Change only the line's quantity, and lots you chose stay: the line says
   **Short by** or that the lots take more than its quantity until you make them add up.
   Lots chosen for you are chosen again for the new quantity.
7. **Propose again** under a line chooses its lots for you again — the one that expires
   first, from what is there now — and saves the delivery.

The **Pick list** has a row per lot, under its location, with the lot number and expiry in
the **Lot** column. The **Packing slip** lists the lots — and serial numbers — under each
line, with their expiry, each lot once with how much of it the line takes: it is the
customer's record of which lots they received. The order's **Deliveries** card lists the
lots of each posted delivery and of each draft as chosen so far; a cancelled delivery
lists none.

If you may see stock in Inventory — both **View locations** and **View stock** — each lot
on the delivery links to its page there, and **Trace** beside it opens its
[trace](/en/user/inventory/#trace-a-lot): where the lot came from and who else received
it. Without them the lots are named without links.

When you post, someone else may have taken a lot meanwhile, or put it on hold. If you did
not change the lots yourself, they are chosen again from what is there now and the
delivery is posted with them; the delivery shows the lots it took. If you chose the lots,
or nothing is left to choose from, the post is refused and the lot is named on its row.

### Back orders

What is not delivered stays on the order as its back order (*restordre*); there is no
separate document. **Deliveries** → **To deliver** lists the confirmed orders with something
left to deliver; make the next delivery from the order when the goods are in. **Deliveries**
→ **Deliveries** lists the delivery documents, drafts first.

### Ship a delivery

A posted delivery is sent with a carrier from its **Shipments** card. This needs the
permission to manage deliveries, and a carrier account set up under Workspace admin ›
Integrations › Shipping ([Shipping](/en/admin/shipping/)).

1. On the posted delivery, choose **Ship**. A draft shipment is made with one package
   weighing the delivered goods (a kilogram when the products have no weight), to the
   order's delivery address, on the site's default carrier account and its default service.
2. Change the **Service** — for example *Parcel to pickup point* or *Business parcel* —
   the recipient's phone and e-mail, which the carrier sends its notices to, and the
   **Packages**: each one's weight in grams and its length, width and height in
   millimetres. Add a package for each parcel, then **Save shipment**.
3. **Show prices** lists what the carrier charges for each of its services, cheapest first.
   Bring's prices are estimates: the carrier's invoice is the truth.
4. **Book shipment** books it. The shipment shows its consignment number (*sendingsnummer*),
   a **Track** link and a **Label** to print; a cargo service also has a **Waybill**
   (*fraktbrev*) — for the others the label serves. When the Orders settings say so, the
   customer gets an e-mail with the tracking link. The order's shipping lines count as
   delivered. Printing the label and the waybill needs the permission to manage deliveries.

A test account books in Bring's test mode, marked **Test**. Bring still sends its notices
to real phone numbers and e-mail addresses in test mode, so use made-up contacts there.

When booking fails because the carrier did not answer, wait a couple of minutes and choose
**Book shipment** again: Vantigo asks the carrier first and books once. Until then the
shipment cannot be changed or cancelled, and while a booking is being made another
**Book shipment** says so. The carrier may take up to a quarter of an hour to show a
booking it made: until then Vantigo books nothing more and says so — check Mybring, or wait
and book again. A booking found again this way has no label in Vantigo: the shipment says
**print the label in Mybring**, and you print it there. When the carrier refuses, its own
words say why — a wrong postal code, say — and the shipment stays a draft you can change.

### Follow and cancel a shipment

The shipment's state moves by itself as the carrier reports it — **Booked**, **In transit**,
**Delivered** — every half hour, with each **tracking event** listed under it. A deviation
or a return marks it with **Exception**, and so does a cancel Bring reports: Vantigo does not
cancel the shipment on its own — look at it, and cancel it here if it is so. The order's **Shipments** card lists every booked
shipment with its tracking link.

**Cancel shipment** cancels a draft. A booked Bring shipment cannot be cancelled with Bring:
it is **voided** in Vantigo and its label must not be used — Bring charges only for parcels
handed in. A parcel the carrier already has cannot be cancelled.

## Invoice an order

An order is invoiced in Invoices, from its lines: you choose how much of each line to
invoice, and the invoice's issue marks it invoiced on the order. A line is invoiced
**on order** — what is ordered can be invoiced as soon as the order is confirmed, delivered or
not — or **on delivery**: only what is delivered can be invoiced. Goods on an order with a
site are invoiced on delivery unless you choose otherwise; everything else on order. Change
a line's policy under its name in the lines while nothing of it is invoiced or on a draft
invoice.

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
| There is not enough available at the site. | Confirm without reserving and reserve later, lower the quantity, or deliver from another site. |
| The location does not hold that much. | Someone took stock meanwhile: choose another location, lower the quantity, or correct the stock in Inventory. |
| Choose the location every line is taken from. | Pick a **From location** for every line, then post. |
| The lots do not add up to the line's quantity. | Choose more lots in **Change lot**, change a lot's quantity, or lower the line's quantity. |
| Choose the lots the line is taken from. | Choose at least one lot in **Change lot**, then save and post. |
| The lot is expired. | Choose another lot, or ask someone with **Use expired lots** to post the delivery with **Use expired lot**. |
| The lot is on hold. | The lot is in quarantine or blocked in Inventory: choose another lot. |
| That lot does not hold the item at that location. | Someone took the lot meanwhile: reload and choose another. |
| Only someone allowed to use expired lots can choose an expired lot. | Choose another lot, or ask someone with **Use expired lots** to choose it. |
| Nothing of the order is open and available at its site. | Wait for the goods, or reserve them, or finish the draft delivery that already holds them. |
| The order has deliveries; its site no longer changes. | Leave the site as it is. |
| A posted delivery no longer changes. | Correct the stock in Inventory and use **Undo delivered** on the order. |
| A warning blocks this | A customer or product on the document has a block warning. Someone who manages the Orders settings can **Override the warning** with a reason, or change the warning. |
| The packaging is not one of the product's | Pick the product again and choose one of its packagings, or enter the quantity in the unit. |
| The final period is already on an invoice or a draft invoice and cannot be pro-rated. | Choose **Full period**, or credit the period — or delete the draft invoice — first. |
| No carrier account to use | Ask an administrator to add one, or to choose the site's default account, under Integrations › Shipping. |
| The carrier refused the request. … | The carrier's own words follow: correct what it names — the address, the weight — and book again. |
| The carrier did not answer. | Try again in a couple of minutes. A booking whose answer was lost is found again: **Book shipment** books once. |
| The shipment is being booked right now. | Wait a couple of minutes and look again. |
| The carrier does not show the earlier booking yet. | Check the booking in Mybring, or wait a quarter of an hour and book again. |
| A booking's outcome is unknown: book again to resolve it before changing the shipment. | Choose **Book shipment**; change the shipment once it is booked or refused. |
| The carrier has the parcel; it can no longer be cancelled. | Arrange a return with the carrier instead. |
| The document has no address to ship to. | Choose a delivery address on the document, save it, and estimate again. |
