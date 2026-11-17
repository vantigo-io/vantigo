---
title: Purchasing
description: The Purchasing app — the purchase order list and its filters; make a purchase order for a supplier and a receiving site, with product lines filled from the supplier's items and text lines; send it by e-mail, mark it as sent or send it again; record the supplier's confirmation; change a sent order and read its history; close a line or the whole order, reopen it, cancel it or copy it; the PDF; the suppliers and what each sells — supplier items, the preferred supplier and the price-list import with its CSV columns; the settings; the dashboard card and the product page's Suppliers card; and what comes later — receiving, proposals and the invoice match.
sidebar:
  order: 68
sources:
  - apps/purchasing/frontend
  - apps/host/frontend/src/routes/purchasing
  - apps/host/frontend/src/lib/purchasing-access.ts
  - apps/host/frontend/src/lib/dashboard-helpers.ts
  - apps/host/frontend/src/components/app-spotlight.tsx
  - apps/host/frontend/src/routes/products/$productId.tsx
---

The Purchasing app is where you order goods and services from your suppliers. In
Norwegian the app is **Innkjøp**. A **purchase order** says what you buy from whom, at
what price and when you expect it; a **supplier item** says what a supplier sells of one
of your products, at what price, in which packaging and with which lead time.

A purchase order moves neither stock nor the books. In this version you order and follow
up; receiving the goods into stock, proposals from low stock and matching the supplier's
invoice to the order come later ([What comes later](#what-comes-later)). Purchasing buys
from the suppliers in [Accounting](/en/user/accounting/#suppliers)'s register and orders
the products of [Products](/en/user/products/) for a site of
[Inventory](/en/user/inventory/), so an installation has all three when it has
Purchasing.

## The app and its sidebar

The sidebar has **Purchase orders** and **Suppliers** for everyone with **Use Purchasing**,
and **Purchasing settings** for those who manage the Purchasing settings. What you can do depends on
your permissions: writing orders, sending them and keeping the supplier items are
separate permissions, so a colleague may see buttons you do not
([Permissions](#permissions)). Everyone with **Use Purchasing** sees the prices.

## The order list

**Purchase orders** lists the orders newest first, with their **Number**, **Supplier** —
the supplier's reference under the name once it is recorded —, **Status**, **Order date**,
**Expected day**, **Received**, **Invoiced** and the **Net total**, excluding VAT. A draft has no
number yet. **Received** counts the order's goods lines received in full or closed, out of
all its goods lines, and **Invoiced** its lines invoiced in full, out of all its lines;
receiving and the invoice match come with later versions of Purchasing, so until then
**Received** counts only closed lines and **Invoiced** reads 0 of the lines. An order
can be:

- **Draft** — being written; it has no number and can be deleted.
- **Sent** — sent to the supplier, by e-mail or marked as sent; it has its number.
- **Partially received** and **Received** — reached once goods are received against the
  order, which comes with receiving.
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

## Close a line or an order

- **Close line** ends one line: what is left on it will not come. When no line is left
  open, the order is closed — or **Received**, when everything on it was received.
- **Close** ends a sent order with what is left on it: every open line is closed and the
  order is **Closed**. What was left stops counting as *on order* in Inventory.
- **Reopen** opens a closed order again, with the lines its closing ended; a line you
  closed by itself before stays closed.

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
- **Price tolerance (%)** — a percentage, 2 % unless changed, for the invoice match that
  comes later; it changes nothing yet.

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
contact and notes from the orders' history — which keeps that they changed —, and the
orders' stored PDFs are deleted.

## Permissions

| Permission | Lets you |
| --- | --- |
| **Use Purchasing** (`purchasing:access`) | Open the app and read every order, supplier and supplier item, prices included, and download the PDFs. |
| **Manage purchase orders** (`purchasing:orders-manage`) | Write and delete drafts, change sent orders, record the confirmation, close a line or an order, reopen and copy. |
| **Send purchase orders** (`purchasing:orders-send`) | Send, mark as sent, send again and cancel. |
| **Manage supplier items** (`purchasing:supplier-items-manage`) | Add, change, delete and import supplier items. |
| **Manage Purchasing settings** (`purchasing:settings`) | The settings. |

**Receive goods (coming)** (`purchasing:receive`) and **Match supplier invoices (coming)**
(`purchasing:invoices-match`) can already be given in a role; they do nothing yet, and are
used once receiving and the invoice match arrive.

## What comes later

- **Receiving** — taking the goods of a sent order into stock, in part or in full, with
  their lots and serial numbers.
- **Proposals** — draft orders made from low stock and from Manufacturing's buy
  suggestions, per preferred supplier.
- **The invoice match** — the supplier's invoice in Accounting matched to its order, and
  what differs.

The rules behind every screen are in the [Purchasing reference](/en/reference/purchasing/).
