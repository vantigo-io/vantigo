---
title: Inventory
description: What lies where — the Inventory app and its sidebar, finding a location or an item by typing or scanning its code, setting up a site, adding locations one at a time and in bulk, changing, moving, archiving and deleting them, printing location labels, receiving goods, adjusting and moving stock with the line editor, entering quantities in packagings, counting stock, importing opening balances, transferring stock between sites — drafting, sending, receiving in parts, closing a difference and cancelling — the transfers list and a transfer's page, setting an item's primary location, reorder point and maximum at a site, the low-stock list — its suggestions rounded up to whole packagings, creating a transfer from it and exporting it — importing placements, what lies on a location, an item's stock and movements, the Movements list and a movement's page, the Stock card on the product page, what orders have reserved and what is available, which sites fulfil orders, the dashboard card, its attention items and the quick actions, and who sees what.
sidebar:
  order: 65
sources:
  - apps/inventory/frontend
  - apps/host/frontend/src/routes/inventory
---

The Inventory app keeps track of the stock of your goods: in which warehouse or store,
and on which shelf, bin or display, each item lies, and how much of it. Every place
belongs to a **site** — a warehouse or a store — and a site is divided into
**locations**, as deep as you need: a zone, an aisle, a rack, a shelf. Stock changes
only when someone posts a **movement** — a **receipt** of delivered goods, an
**adjustment** with a reason, or a **move** from one location to another — and a
movement, once posted, is never changed: a mistake is put right with a new adjustment.
A **count** checks what really lies on the shelves and posts the differences, and a
**transfer** sends stock from one site to another, where it is **in transit** until it is
received. A **placement** says, per item and site, where the item belongs — its
**primary location** — and when it runs low — its **reorder point** — and **Low stock**
lists what has.
The items come from [Products](/en/user/products/): only **goods** are kept in stock,
never services. The rules behind every screen — the codes, the refusals and the
permission each request is checked against — are in the
[Inventory module reference](/en/reference/inventory/); this page is about what you do on
each screen.

## The app and its sidebar

The **Inventory** tile, after Products in the app switcher, is shown to everyone with
**View locations** (`inventory:locations-view`) or **View stock**
(`inventory:stock-view`). The sidebar shows:

| Area in the sidebar | Who sees it |
| --- | --- |
| **Overview** — the lookup and the sites | `inventory:locations-view` or `inventory:stock-view` |
| **Transfers** — stock sent between sites | `inventory:locations-view` and `inventory:stock-view` |
| **Movements** | `inventory:locations-view` and `inventory:stock-view` |
| **Receipts** — the movements list showing receipts only | `inventory:locations-view` and `inventory:stock-view` |
| **Counts** — the stock counts | `inventory:counts-manage`, with both views |
| **Low stock** — the items at or below their reorder point | `inventory:locations-view` and `inventory:stock-view` |
| **Import opening balances** — a site's first stock, or the placements, from a spreadsheet | `inventory:stock-adjust`, with both views |

Anything your permissions do not allow is not shown at all: a button you do not see is a
permission you do not have. The buttons that post — receipts, adjustments and moves —
need both views as well as **Receive goods** or **Adjust stock**; counting needs both
views and **Count stock**; creating, sending, receiving and cancelling transfers needs both
views and **Transfer stock**. Quantities need both views, because every place stock lies
is a location; **View locations** alone shows the sites and their locations without any
quantity.

## Find a location or an item

**Overview** opens with the field **Find location or item**, ready to type in. Type or
scan one of these and press Enter:

- a location's **full code**, such as `OSL-A-03-2`, in capitals or not;
- a location's **label code**, `LOC` and a number;
- an item's **SKU** or **barcode**, or the barcode of one of its packagings — a box or
  a pallet opens the item;
- a transfer's token, `TRF` and its number, as printed under the barcode of its
  [transfer note](#print-the-transfer-note) — it opens the transfer.

One match opens it straight away: a location opens its page, an item its stock page.
Several matches are listed, each marked **Location** or **Item**, for you to choose. No
match says *Nothing matches “…”.* and keeps what you typed, so you can correct it.
Transfers, like items, show only to those who hold both views.

A **handheld scanner** works in this field as it is: a scanner types the code it reads
and presses Enter, which is just what the lookup waits for. Items show only to those
who also hold **View stock**.

The same lookup is the quick action **Find location or item** in the search in the top
bar, for everyone with **View locations**.

## Set up a site

On **Overview**, press **New site** (it needs **Manage locations**,
`inventory:locations-manage`). Give it:

- a **Code** — 1–16 letters A–Z and digits, such as `OSL`. It starts the full code of
  every location in the site, and no other site may have it;
- a **Name**;
- a **Kind**: **Warehouse** or **Store**. It changes the words on screen, not the rules;
- **Fulfils orders** — whether orders reserve and deliver stock from the site. It is on
  for a warehouse and off for a store until you change it;
- an address if you want one: **Address**, **Address line 2**, **Postal code**,
  **City** and **Country** (two letters, such as NO).

The new site opens with its page. A site is a location too: stock can lie on the site
itself, which suits a small shop with no shelves, and moves down later when you add
some.

To change whether an existing site fulfils orders, open the site, press the site's menu
(**…**) and **Edit site**, and switch **Fulfils orders**. A site that does shows
*Fulfils orders* beside its kind. Turning it off stops new reservations and deliveries
there; what orders have already reserved stays reserved.

On **Overview** each site shows as a card with its kind, its city and its number of
locations; with **View stock** also its stock lines and *Last receipt …*. The cards
load with the list of sites, so even many sites show at once.

## Add locations

On a site's page, **New location** adds one directly under the site; on a location —
in the row's menu in the **Locations** card, or in the **…** menu of the location's
page — **Add location below** adds one under it. Choose the **Parent**, the **Kind**
(**Zone**, **Area**, **Sales floor**, **Back room**, **Aisle**, **Rack**, **Shelf**,
**Bin**, **Display**, **Dock** or **Other**), the **Code**, and optionally a **Name**
and a **Sort order**. The code must be unique under the same parent, and the location's
**full code** is the codes from the site down joined with dashes: shelf `2` on rack
`03` in aisle `A` of `OSL` is `OSL-A-03-2`. The tree goes at most eight levels below the
site.

### Many at once

**Bulk add**, on the site's page or in a location's menu, creates a whole grid in one
go. Add up to three levels; for each, pick its **kind**, an optional **prefix**, and a
range **from** – **to**:

- a range of digits is padded to the width of its start: `01` to `12` gives
  01, 02 … 12;
- a range of letters runs from one to the other: `A` to `D`;
- leave **to** empty for one code.

Every code of a level is created under every location of the level before. For
example, under the site **OSL**: level 1 an **Aisle** `A` with **to** empty, level 2
**Rack** `01` to `12`, level 3 **Shelf** `1` to `5`. The dialog says *73 locations will
be created* — the aisle, its 12 racks and **60 shelves**, `OSL-A-01-1` to `OSL-A-12-5`.
At most 1000 at once, and all or nothing: if any one of the codes is already taken,
nothing is created and you are told which full codes collide.

## Change, move, archive or delete a location

Open the location's menu — the row's menu in the **Locations** card, or **…** on its
page — with **Manage locations**:

- **Edit** changes the kind, the code, the name and the sort order, and **moves** the
  location: choose another **Parent** in the same site. Everything below it, and the
  stock lying there, moves with it; the full codes below change to match. A new parent
  must be active, and cannot be the location itself or anything below it. While a
  count is open over the location, over anything below it, or over where it would go,
  it cannot be moved — a count of the whole site blocks every move in the site; post or
  cancel the count first. Changing only the code or the name is always allowed.
- **Archive** archives the location and everything below it. Archived locations take
  no stock and drop out of the pickers. It is refused while stock lies on the location
  or below it: move it or adjust it to zero first. It is refused too while a
  [transfer](#transfer-stock-between-sites) still in transit was sent from the location
  or from below it — *A line still in transit was sent from this location; a cancel
  would return it here. Receive or cancel it first.*
- **Reactivate** brings back the location alone — not what lies below it — and only
  under an active parent.
- **Delete** removes the location and everything below it for good. It is refused once
  stock has ever been posted to any of them: archive instead.

The site's own location — the top of the tree — cannot be archived or deleted on its
own; the site is archived or deleted instead. In the site's **…** menu: **Edit site**
(changing the site's code changes every full code in it — printed labels still scan),
**Archive** (refused while the site holds stock, and while a transfer to or from it is
still in transit — *A transfer to or from the site is still in transit. Receive or cancel
it first.*; archives every location in it), **Reactivate** (the site and its own
location; the locations below are reactivated one by one) and **Delete** (only for a
site no movement was ever posted in and no transfer names — *A transfer names the site,
so it can only be archived.*). A draft does not stop the archive; it can no longer be
sent. Each asks you
to confirm first.

In the **Locations** card the tree opens a level at a time. **Show archived** includes
archived locations, and **Search by code or name** finds one — as you type in a site
of up to 2000 locations; in a bigger one, type and press Enter, and the search runs
over the whole site. The tree shows at most 10,000 locations; a site with more says
*This site has more than 10,000 locations; the tree shows the first 10,000 by full
code. Search to find the others.*

## Print location labels

A label lets a handheld scanner find a location: it carries a barcode, the location's
**full code** in large letters and its **name**. Press **Print labels** on a site's
page or a location's page (it needs only **View locations**):

- on a site: **Labels for** **Every location in the site**, or **Chosen locations** —
  pick them in **Locations**, searching by code or name;
- on a location: that location, and **Include every location below this one** if you
  want its whole branch.

Choose the **Label sheet** — **Avery L7160 — 21 labels per A4** or **Avery L7163 — 14
labels per A4** — and press **Download PDF**. Print the PDF at 100 % on the sheets. The
labels come in full-code order; at most 1000 at once (*At most 1000 labels can be
printed at once.*).

The barcode holds the location's label code — `LOC` and a number — not its full code,
so a label keeps scanning after the location is renamed, recoded or moved, and on any
keyboard layout. Reprint it only when you want the printed text to change.

## Receive goods

Press **New receipt** on **Overview**, or **Receive here** on a location, or
**Receive** on an item (it needs **Receive goods**, `inventory:receipts-manage`). Choose
the **Site** and **Receive to**, the location the goods go to; optionally the
**Supplier**, the **Supplier's reference** — the delivery note's number, say — and a
**Note**. Then add the lines with the [line editor](#the-line-editor): each line's
**Location** is *As the receipt* unless you choose another — or, for an item with a
[primary location](#set-an-items-primary-location-and-reorder-point) at the site, that
location: a scanned or chosen item's line goes there until you choose another. Press
**Post receipt**.

A service cannot be received, and neither can an item whose product is discontinued.

## Adjust stock

Press **New adjustment** on **Overview**, **Adjust** on a location, or **Adjust** on an
item (it needs **Adjust stock**, `inventory:stock-adjust`). Choose the **Site** and a
**Reason** — **Correction**, **Damaged**, **Lost**, **Found**, **Opening balance** or
**Other** — and a **Note** if you want. For each line choose the item, the
**Location** and the mode:

- **Change by** adds or takes away: `5` puts five more there, `-2` takes two away;
- **Set to** says what lies there now: `12` makes it twelve, whatever it was.

A **Set to** line that already matches is left out; if every line matches, nothing is
posted. Press **Post adjustment**.

An adjustment is how you put right what was counted wrong, write off what broke, and
place your opening stock (**Opening balance**).

## Move stock

Press **New move** on **Overview**, or **Move from here** on a location (it needs
**Adjust stock**). Choose the **Site**; for each line the item, **From**, **To** and
the quantity. Both locations must be in the same site, and not the same one. Press
**Post move**.

## The line editor

The three forms share one way to enter lines:

- **Scan an item**: scan or type a SKU or barcode and press Enter. A new item adds a
  line; an item already listed counts one more on its line. **Add line** adds an empty
  line, where you search for the item by SKU, name or barcode.
- **Quantities** are in the item's own unit, with up to three decimals — 2.5 metres,
  0.125 kg.
- When **Site** changes, the locations already chosen are cleared: a movement stays in
  one site.
- **In packagings**: see [Enter quantities in packagings](#enter-quantities-in-packagings).
- When the server refuses, **nothing is posted**: every refused line is marked in red
  with the reason — *Not enough stock on the location.*, *The location is archived.* and
  the like — and the form says how many lines were refused. Correct them and post
  again: after a refusal, the next try is a new posting.
- **Retrying never posts twice.** If the connection fails — *Could not reach the
  server. Check your connection and try again.* — or the server answers with an error,
  the form keeps the same posting, so posting again cannot post it twice: Inventory
  recognises it and the goods are received once.
- **Already posted.** If an earlier version of the posting did go through but its
  answer was lost, and you changed the form before trying again, the form says
  **Already posted** — *An earlier version of this posting has already been posted; the
  answer to it may have been lost on the way. Check the movements to see what was
  posted.* Follow **Check the site's movements** to see it. *If it is not there, or this
  is meant as a second posting, post it as a new one*: **Post as a new posting** posts it
  at once as a separate movement.

After posting, the form empties for the next one and links **Open movement …** to what
you posted.

## Enter quantities in packagings

When an item has **packagings** in Products — a box of 15, a Euro pallet of 40 boxes
(see [Packagings](/en/user/products/#packagings)) — each of its lines in the line editor
shows a counter per packaging and a **loose** field in the item's own unit, with the
total beneath: 2 Euro pallets, 3 boxes and 4 loose of an item in pcs reads
*= 1,249 pcs*. Type or step the number of each packaging, and put what is left over in
the loose field.

- **Scanning a packaging's barcode** adds one of that packaging to the item's line —
  scan a pallet three times for three pallets.
- In an **adjustment** with **Change by**, choose **Add** or **Remove**: what you
  entered is added to or taken away from what lies there. With **Set to**, it is what
  lies there now.
- The quantity is worked out when the movement is posted, from the packagings as they
  are then, and the movement keeps what you entered: its page shows it under **Entered
  as** — *2 Euro pallet + 2 Box* — even if the packagings change later.
- If a packaging was removed in Products while you were typing, the line is refused:
  *The item has no such packaging any more.*

**Reading quantities in packagings.** Wherever stock is listed — a location's
**Contents**, an item's stock page, the product page's **Stock** card and the count
screen — a quantity of an item with packagings is also shown broken down, the largest
packaging first: 1,830 pcs with a pallet of 600 and a box of 15 reads *3 Euro pallet,
2 Box*, under the quantity itself.

## Transfer stock between sites

A **transfer** sends stock from one site — the **From site** — to another — the **To
site**: from the central warehouse to a store, say. It goes through these statuses:

- **Draft** — being prepared. A draft holds nothing: no stock is set aside and nothing
  moves, so it can be changed and deleted freely.
- **Sent** — the stock has left its locations at the from site and is **in transit**: it
  lies on no shelf, at neither site, until it is received.
- **Partly received** — some of it has arrived and been put away; the rest is still in
  transit.
- **Received** — everything has arrived, or what did not arrive has been
  [closed as a difference](#close-a-difference).
- **Cancelled** — the transfer was called back and everything went back where it came
  from.

Transfers need **Transfer stock** (`inventory:transfers-manage`) as well as both views.
Without it you can still open **Transfers**, a transfer's page and its transfer note, but
not create, send, receive or cancel one.

### Create a draft

In **Transfers**, press **New transfer** — *Send stock from one site to another. A draft
holds nothing until it is sent.* Choose the **From site** and the **To site**, which must
be different (*The from site and the to site must be different.*), and, if you want, the
**Expected arrival**, a **Reference** and a **Note**. Then add the lines with the
[line editor](#the-line-editor): scan or search for each item and enter the quantity, in
its unit or [in packagings](#enter-quantities-in-packagings). An item may be on several
lines — that is how stock from two locations travels in one transfer.

Beside each line, two columns show the stock at both sites as you type:

- **At OSL: available** — what is available of the item at the from site. A line asking
  for more is marked **Short**: *The transfer asks for more than is available at the
  from site. Sending more than a location holds is refused.* The draft can still be
  saved.
- **At BGN: available · incoming** — what is available at the to site and, after
  *+… incoming*, what is already on its way there in other transfers; then, when the
  item has a [reorder point](#set-an-items-primary-location-and-reorder-point) there,
  *reorder ≤ …*, marked **Low** while it is low.

A line [drafted from the low-stock list](#create-a-transfer-from-the-list) also says how
its quantity was found — *21 needed, rounded up to 2 cases of 15 = 30* — until you
change the line.

If the stock cannot be read, the form says so and the draft can still be saved.

Press **Save draft**: *Draft saved*, and the transfer's page opens with its number —
*Transfer 1042*. On the page of a draft, **Edit** changes it, and **Delete draft** asks
*Delete the draft of transfer 1042?* — *The draft and its lines are deleted. Nothing was
sent, so no stock changes.*

### Send a transfer

On the transfer's page, or straight from the form, press **Send…** (the form saves what
you changed first). The dialog **Send transfer 1042 to BGN** asks you to *pick each line
from a location at* the from site: in the column **From a location at OSL**, each line
offers the locations at the from site where the item lies, with what lies there, and
starts on the item's primary location there, marked *(primary)*, when that holds some
of it — else on the one holding most. An item that lies nowhere at the site says *Nothing of it
lies at the site*.

The dialog warns you before you send:

- *The location holds only …; the send will be refused.* — choose another location, or
  split the line on the draft;
- *Only … of … lie at OSL; the send will be refused.* — the site holds less than the
  transfer takes;
- *… of … are reserved for orders at OSL; sending leaves them short.* — the send takes
  stock that [orders have reserved](#reserved-and-available). This is a warning only: the
  send goes through, and available at the from site turns negative.

**Print the transfer note** is ticked. Press **Send 3 lines**: *Transfer sent*. The stock
leaves the chosen locations at once, posted as one **Transfer out** movement at the from
site, the status becomes **Sent**, and the transfer note downloads. A send that is
refused sends nothing; the refused lines are marked with the reason. Pressing again
after a lost connection never sends twice.

An item that has been deleted from the catalog, or whose product has become a service,
since the draft was written is refused at send.

### Print the transfer note

Once a transfer is sent, **Print transfer note** on its page downloads its transfer note
as a PDF, `folgeseddel-TRF-1042.pdf`, to travel with the goods: the number, the two sites with their
addresses, when it was sent and by whom, the expected arrival, the reference, each line
with its SKU, name, quantity (and the packagings it was entered in) and the location it
was picked from, a line for the receiver's signature, and a barcode of the transfer's
token, `TRF-1042`. Scanning that barcode — in **Find location or item** or on the receive
page — opens the transfer.

### Send and receive at once

When the two sites are next door — a store and its back room in another building, say —
and the goods arrive as you send them, press **Send and receive…** instead. The dialog
**Send and receive transfer 1042 at BGN** has, besides the source of each line, a column
**To a location at BGN** where each line is put away, starting on the item's primary
location there when it has one; **Receive every line at a location
of BGN** sets the same location on every line. Every line needs one (*Choose where every
line is received.*). Press **Send and receive 3 lines**: *Transfer sent and received*. The
stock leaves the from site and arrives at the to site in one step — a **Transfer out**
and a **Transfer in** movement — and the transfer is **Received** at once. **Print the
transfer note** is not ticked here; tick it if you want the note.

## Receive a transfer

When the goods arrive, open the transfer — from **Transfers**, from the to site's
**Incoming**, or by scanning the transfer note's barcode in **Find location or item** —
and press **Receive…**. It is offered while the transfer is **Sent** or **Partly
received**, to those who hold **Transfer stock**.

The page **Receive transfer 1042 from OSL** says when the transfer was sent and by whom,
and lists each line still in transit: **Item**, **Sent**, **Received before**, **Now** and
**To a location at BGN**. Count what has arrived:

- **Scan** in the field **Scan an item or the transfer note**: each scan of a SKU or
  barcode counts one more on its line, and a box's or pallet's barcode one box or pallet.
  Scanning the barcode of another transfer's note opens that transfer's receive page. An
  item not on the transfer says *“…” is not on this transfer, or nothing of it is still
  in transit.*
- **Type** the quantity in **Now**, in the item's unit or in its packagings.
- **Accept all** fills every line with everything still in transit — for a delivery you
  have checked as complete.

Each line is put away at the item's primary location at the to site — or, without one,
at the to site's own location — unless you choose another in **To a location at BGN**. A line cannot take more than is still in transit on it (*More than is
still in transit on the line.*). Under the lines, *2 lines will still be in transit.* tells
you what you have not counted yet.

Press **Receive**. The goods are posted as one **Transfer in** movement at the to site,
onto the locations you chose, and the transfer's page opens: *Transfer received* when
nothing is left in transit, *Transfer partly received* when something is.

### Receive in parts

A transfer can be received in as many parts as the goods arrive in: receive what came
today, and press **Receive…** again for the next part. Each part counts only against
what is still in transit.

Two people can receive the same transfer at once — one scanning the pallets, another
the loose boxes. Both receipts go through, each judged against what the other has
already received. If the other one got there first and your counts now come to more
than is still in transit, the lines that went over are marked, nothing of yours is
posted, and the page shows what is still in transit with your counts kept: correct them
and press **Receive** again. If the connection broke after a receipt was posted, the
page says *An earlier receive from this page was already posted; its answer may have
been lost on the way.* and shows the lines as they are now — check them before
receiving again. Pressing **Receive** again after a lost connection never receives
twice.

## Close a difference

When the delivery is short and the rest will not come — lost on the way, broken, never
packed — receive what arrived and close the rest as a difference. On the receive page,
press **Receive and close the difference…**; it is offered while something would still
be in transit after what you have counted.

The dialog **Close the difference** lists what will not be received, line by line —
*3 not received* — and asks **What happened?**: **Lost in transit**, **Damaged**,
**Never packed** or **Other**, with a **Note (optional)**. *What is left is recorded as
not received on transfer 1042 and added to no stock. The transfer is then received.*
Press **Close transfer 1042**: what you counted is received, the rest is recorded as
**Not received**, and the transfer is **Received**.

The missing stock already left the from site when the transfer was sent, so closing the
difference changes no stock anywhere — there is nothing to adjust at either site. It is
kept on the transfer, under **Not received** and **Difference closed** with the reason
and the note, and on the item's stock page under
[Transfer differences](#see-an-items-stock-and-movements). If the goods turn up later
after all, post them with an [adjustment](#adjust-stock) with the reason **Found**.

## Cancel a transfer

A transfer that was sent by mistake can be called back while **nothing of it has been
received or closed as not received**: press **Cancel transfer** on its page. *Cancel
transfer 1042? Everything sent goes back to the locations it left at OSL. This cannot be
undone.* Press **Cancel transfer** to confirm, or **Keep the transfer**. Every line goes
back to the location it was sent from, posted as a **Transfer in** movement at the from
site, and the transfer is **Cancelled**: *Transfer cancelled*.

Once any part has been received, the transfer can no longer be cancelled (*Only a sent
transfer with nothing received or missing can be cancelled; delete a draft instead.*):
receive the rest, or close it as a difference, and send back what should not have gone
with a new transfer. A draft is not cancelled but deleted.

## A transfer's page

A transfer's page shows its number with its status and, while it is on its way, *In
transit …*; the from site and the to site, each linking to its site; the **Reference**,
the **Expected arrival**, **Created by** and **Sent by** with when, and, for a transfer
closed with a difference, **Difference closed** with the reason and the note; and the
note.

The lines show **SKU**, **Product**, **From** — the location each line was picked from
—, **Sent** (**Quantity** on a draft), **Received**, **Not received** and **In transit**,
with **Entered as** for a line entered in packagings. A SKU opens the item's stock page.

**History** lists, oldest first, every send, receipt, closed difference and cancel: when,
by whom and at which site — *Sent by Kari Nordmann from OSL*, *Received by Ola Hansen at
BGN*, *Difference closed by Ola Hansen*, *Cancelled by Kari Nordmann, returned to OSL* —,
each line it touched with the location it went to or came from, and a link to the
movement it posted. The movement's page links back to the transfer.

The buttons follow the status: **Edit**, **Send…**, **Send and receive…** and **Delete
draft** on a draft; **Receive…** while it is sent or partly received; **Cancel transfer**
while nothing of it is received; **Print transfer note** once it is sent.

## The transfers list

**Transfers** in the sidebar — *Stock sent from one site to another, in transit until it
is received.* — lists the transfers, newest first: **Transfer** (with its reference),
**From site**, **To site**, **Status**, **Lines** and **Expected arrival**. A transfer
past its expected arrival, or sent more than 7 days ago without one, and not yet
received, is marked **Overdue**.

Filter by **Status** — **Open**, the default, is drafts, sent and partly received
transfers together; or one status; or **All statuses** —, by **From site** and **To
site**, and with **Search**, a transfer number or a SKU on one of its lines (press
Enter). **Load more** fetches the next page. **New transfer** opens the form with the
two sites you filtered by already chosen.

A site's page shows, with **View stock**, **Incoming** and **Outgoing**: the number of
transfers on their way to and from the site — *2 open transfers* — each opening the list
filtered to the site.

## Set an item's primary location and reorder point

A **placement** is three settings of one item at one site, each optional:

- **Primary location** — *Where it is received and picked by default. Stock may still
  lie anywhere.* It is a suggestion, never a rule: a receipt line, a transfer's send and
  its receipt, and an order's delivery start on it, and you may always choose another.
- **Reorder point** — *Low when available + incoming is at or below it.* Zero or more,
  in the item's unit.
- **Maximum** — *The suggestion fills up to it.* More than 0, and never below the
  reorder point.

Open the item's stock page (from the lookup or the product page's **Stock** card). Under
each site it shows the site's **Reorder point** and **Maximum** when they are set, and
**Low** while the item is low there. Press **Edit placement** under the site (it needs
**Adjust stock** and both views); a site that holds none of the item is listed too, so
you can set its placement before the first receipt. The dialog **Placement at Central
warehouse** has the three fields; press **Save** — *Placement saved*. **Remove** deletes
the placement — *Placement removed* — and the item has no primary location, reorder
point or maximum at the site any more.

The dialog refuses what it cannot keep:

- a maximum of 0 — *The maximum must be more than 0.*;
- a maximum below the reorder point — *The maximum cannot be below the reorder point.*;
- nothing set — *Set a primary location, a reorder point or a maximum — or remove the
  placement.*;
- a primary location archived in the meantime — *The primary location must be an active
  location of this site.*

If someone else changed the placement since you opened it, the page reloads and says
*Changed by someone else*: make your change again.

Archiving or deleting a location takes it away as anyone's primary location; a placement
that had nothing else set is removed. Archiving a site does the same for all its
locations and keeps the reorder points and maximums, but an archived site is never on the
low-stock list.

## The low-stock list

**Low stock** in the sidebar — *Items at or below their reorder point at each site, and
what would fill them up.* — lists every item that has run low at an active site. An item
is **low** at a site when what is **available** there — on hand over all the site's
locations, less what orders have [reserved](#reserved-and-available) — plus what is
**incoming** on transfers to the site is at or below its reorder point. Stock already on
its way counts: once a transfer bringing enough is sent, the item leaves the list.

Each row shows the **Item**, the **Site** (when the list is not filtered to one),
**Available** (red when negative), **Incoming**, the **Reorder point**, the **Maximum**,
the **Suggested** quantity and **Elsewhere** — what each other active site where the item
lies or is reserved has available, *Bergen store 12 · Trondheim store 0*. A draft
transfer to the site that already carries the item is shown under it as *Draft 1043
open*. Filter by **Site** (or **All sites**) and search for a SKU or name (press Enter);
the rows are by site code, then SKU, and **Load more** fetches the next page. With
nothing low it says *Nothing is at or below its reorder point.*

**The suggestion.** The list suggests what fills the item up: to its **maximum**, or,
without one, to **one above the reorder point**, so that receiving the suggestion takes
the row off the list — filling it only *to* the reorder point would leave it low. It is
always at least 1. When the item has [packagings](/en/user/products/#packagings), the
suggestion is rounded **up** to a whole number of its **smallest** packaging, and the row
says so under the quantity:

- *21 needed* — nothing to round;
- *21 needed, rounded up to 2 cases of 15 = 30* — the packaging named by its kind (inner
  pack, case, pallet);
- *25 needed, rounded up to 2 cases of 15 = 30, 5 above the maximum of 40* — when the
  rounding passes the maximum, by how much.

### Create a transfer from the list

With **Transfer stock**, each row has a checkbox. Tick the rows of **one** site — the
rows of other sites cannot be ticked once one is — and the bar beneath says *3 selected*.
In **Create transfer to Bergen store from**, choose the site to send from; it starts on
the site with most available of the chosen items. Press **Create transfer**: a draft is
made with each item's suggested quantity — *Draft saved* — and opens, each line saying
how its quantity was found until you change it. Nothing is capped: the draft shows the
from site's stock beside each line, and the [send](#send-a-transfer) refuses what a
location does not hold.

If a draft to the site is already open for one of the chosen items, **Create transfer**
asks first — *A draft is already open*: *A draft transfer to Bergen store is already open
for some of these items. Create another?* — and **Create another draft** makes a second
one.

### Export the list

**Export CSV** downloads the list as filtered — the site and the search — every row,
as `low-stock-2026-11-09.csv`, with the columns `site` (its code), `sku`, `name`,
`barcode`, `available`, `incoming`, `reorder_point`, `maximum`, `needed`, `suggested`,
`packaging` (the packaging's name) and `packaging_count`. The columns are separated by
semicolons and the quantities have a decimal comma, so Excel opens it as it is. There is
no supplier column: a supplier is a free text on a receipt.

## Count stock

A **count** compares what lies on the shelves with what Inventory says, and posts the
differences as one movement. It needs **Count stock** (`inventory:counts-manage`) and
both views. Movements can still be posted while a count is open: the count notices.

### Open a count

Open **Counts** in the sidebar and press **Open count**, or press **Count** on a site's
or a location's page, or choose **Start count** in the search in the top bar. Choose the
**Site** and the **Scope** — **Whole site**, or a location, which counts it and
everything below it — and a **Note** if you like. Press **Open count**.

What lies in the scope at that moment is frozen as **Expected**. Two counts cannot
cover the same place at once: *Another count still counting covers part of this
scope.* A count of an empty site or location is fine — it is how stock is first put on
the shelves: everything you count is added.

**Counts** lists the counts still **Counting** — choose **All statuses** or another
status, or a site, to see others — newest first, with the scope, who opened it and when, how many lines are counted (*12 of 40 lines counted*) and how many have
**Moved**.

### Count

The count's page, **Count …**, opens on the **Count** tab:

1. **Scan a location** — its label, or type its full code — in **Scan a location or an
   item**. **Counting at** shows where you are, and **This location only** shows only
   its lines. A location outside the count is refused: *… is not in this count.*
2. **Scan each item** on it. A scan finds the item's line and puts the cursor in its
   **Counted** field: type what you count, in packagings when the item has them. A
   box's or a pallet's barcode counts one box or pallet on the line, so scanning every
   box counts them. An item nobody expected there gets a new line marked **Added**.
   Scanning an item before any location says *Scan a location first, then the items on
   it.*
3. Without a scanner, type into **Counted** on each line.

A count of a whole site may hold thousands of lines. The page draws only the lines near
what you see and draws the rest as you scroll, so it stays quick; a scanned item's line
is scrolled into view wherever it lies, and **Tab** moves on to the next line as before.

Each line shows **Expected**, **Now** — what lies there now —, **Counted** and the
**Difference**. A line whose stock changed since the count opened is marked **Moved**.
What you type is **saved by itself** a moment after you stop: the page says **Saving…**,
then **All changes saved**; **Unsaved changes** and **Not saved** come with **Save
now**. Leaving the screen saves what you counted first; if that save fails, you are asked
whether to **Leave without saving** or **Keep counting**, and closing the tab with counts not
yet saved makes the browser ask.
Several people can count the same count at once, on different shelves.

### Review and post

The **Review** tab shows the **Differences against the stock now** — counted against
what lies there at this moment — or *Nothing differs: posting changes no stock.*

- **Moved lines**: stock was received, moved or adjusted on them while you counted.
  Posting counts them against the stock now, not against what was expected; tick
  **Post the moved lines against the stock now** to accept that, or recount them first.
  **Post count** waits until you do.
- **Lines not counted**: choose **Not counted: keep the stock** to leave them as they
  are, or **Not counted: set to zero** when not finding something means it is gone.

Press **Post count** and confirm. Anything still unsaved is saved first. The
differences are posted as one movement of the kind **Count**, linked from the count, and
the count is closed — **Posted**. A count where everything matched is posted too,
without a movement: it records that the stock was right. A posted count cannot be
undone; a mistake is put right with an adjustment.

**Cancel count** closes it without posting anything — **Keep counting** goes back. What
was counted stays as a record, and the scope can be counted again. A posted or cancelled
count is shown read-only.

While a count is open, its locations, and what lies above and below them, cannot be
archived: *A count is still counting this location or something below it. Post or
cancel it first.* A count left open more than a week shows on the dashboard.

## Import opening balances

To place a site's first stock from a spreadsheet, open **Import opening balances** in
the sidebar (it needs **Adjust stock** and both views).

1. Press **Download template** for a CSV file with the header `location,sku,quantity`,
   and fill in one row per item and location: the location's **full code**
   (`OSL-A-03-2`), the item's **SKU** and the **quantity** in the item's unit — up to
   three decimals, with a point or a comma, and no thousands separator. The columns may
   be in any order, separated by commas, semicolons or tabs; at most 5000 rows. If your
   quantities have a **decimal comma**, separate the columns with semicolons — as
   Excel does when it saves CSV in Norwegian — or put the quantity in quotes (`"2,5"`):
   in a comma-separated file an unquoted `2,5` becomes two cells, and the row is
   refused as **Not a valid quantity**. A file saved from Excel reads as it is.
2. Choose the **Site** and the **CSV file**, and press **Dry run**. Nothing is posted:
   each row shows its **Outcome** — **OK**, **No such location in the site**, **No such
   SKU**, **A service, not kept in stock** or **Not a valid quantity**.
3. When every row is OK (*All 120 rows are ready to post.*), press **Commit**. The rows
   are posted as **one adjustment** with the reason **Opening balance**, and the page
   says **Opening balances posted** with a link to it. If any row fails, nothing is
   posted: fix the file and try again.

Uploading **the same file again to the same site** posts nothing new — it answers the
movement already posted — so a commit cannot be doubled by accident. A changed file is a
new posting.

## Import placements

To set many [placements](#set-an-items-primary-location-and-reorder-point) at once,
across every site, open **Import opening balances** in the sidebar (it needs **Adjust
stock** and both views) and choose **Placements** under **What to import**.

1. Press **Download template** for `placements.csv`, with the header
   `sku,site,primary_location,reorder_point,maximum` — exactly those five columns, in any
   order. Fill in one row per item and site: the item's **SKU**, the site's **code**
   (`OSL`), the primary location's **full code** (`OSL-A-03-2`), the **reorder point**
   and the **maximum**, in the item's unit. Separators, decimal commas and the 5000-row
   limit are as for [opening balances](#import-opening-balances).
2. **Each row sets all three fields.** An empty cell clears its field, and a row with all
   three empty **removes** the placement. When a file names the same item and site twice,
   the last row wins.
3. Choose the **CSV file** and press **Dry run**. Nothing is written: each row shows its
   **SKU**, **Site**, **Primary location**, **Reorder point**, **Maximum** and
   **Outcome** — **OK**, **No such site**, **The site is archived**, **Not an active
   location of the site**, **No such SKU**, **A service, not kept in stock**, **Not a
   valid quantity** or **The maximum is below the reorder point**.
4. When every row is OK (*All 120 rows are ready to import.*), press **Commit**:
   **Placements imported** — *118 set, 2 removed.* If any row fails (*3 rows cannot be
   imported. Fix the file and try again.*), nothing is written.

Uploading **the same file again** writes nothing new, so a commit cannot be doubled by
accident. If a site or a location in the file is archived between the dry run and the
commit, the commit is refused and writes nothing: run the dry run again.

## See what lies on a location

A location's page shows its path at the top, its full code with its kind, and the
facts: site, code, full code and status. **Locations below** lists its children.
**Contents** (with **View stock**) lists every item lying on the location **and
everything below it** — SKU, product, location and on hand, broken down in packagings
where the item has them — and **This location only** restricts it to the location
itself. **Movements** shows the latest twenty that touched
the location or anything below it, and **All movements** opens the full list filtered to
it.

## See an item's stock and movements

An item's stock page — from the lookup, the product page's Stock card or a movement —
shows the SKU and the product, the total on hand, each site with the locations where the
item lies, what orders have [reserved](#reserved-and-available) there and what is
available, and the item's movements, newest first. Under a site that a
[transfer](#transfer-stock-between-sites) is bringing more of, *+… incoming* says how
much is on its way there; a site with stock on its way and nothing on hand yet has a row
too. The item's [primary location](#set-an-items-primary-location-and-reorder-point) at
a site comes first among its locations, marked *primary*, and each site shows its
**Reorder point** and **Maximum**, **Low** while it is low there and, with **Adjust
stock**, **Edit placement**. **Receive** and
**Adjust** are offered as your permissions allow. An item deleted
from the catalog still shows its history, marked *Deleted from the catalog*.

**Transfer differences** lists the transfers of the item that were
[closed with something not received](#close-a-difference) — *Transfers of this item whose
difference was closed with something not received.* — with how much of the item was not
received on each; with none it says *No transfer of this item has a difference.*

## Movements

**Movements** lists every adjustment, move, receipt, count, issue, transfer and
production movement, newest first. An **Issue** is stock taken out for an order's
delivery; it shows the order's document as its source and is posted from Orders, never
here. A **Production** is what a manufacturing order consumed — components taken off
their locations — and produced — the item put on its location — in one movement at one
site; it is posted from Manufacturing, never here. A
**Transfer out** is a transfer's send, taken off the from site's locations; a **Transfer
in** is a transfer's receipt at the to site, or its cancel putting the stock back at the
from site. Both link to their transfer and are posted from the transfer, never here.
Filter by **Site**, **Location** (which includes everything below it), **Item**, **Kind**
(**Adjustment**, **Move**, **Receipt**, **Count**, **Issue**, **Transfer out**, **Transfer
in**, **Production**) and **Posted between**, two days in your
organisation's time zone. **Load more** fetches the next page. **Receipts** in the
sidebar is the same list showing receipts only.

A movement's page shows its kind, its site, when and by whom it was posted, the reason
or the supplier and the supplier's reference, the note, the count it posted or the
**Transfer** it belongs to, the **Source** another module posted it for — a
production's links to its manufacturing order when Manufacturing is on — and its
lines: **SKU**, **Product**, **From**, **To** and **Quantity**, with **Entered as** for a
line entered in packagings. A movement is never edited or deleted.

## The Stock card on the product page

When Inventory is on and you hold both **View locations** and **View stock**, a
product's page in Products has a **Stock** card: the total on hand per unit, each site
opening to the locations where the item lies — by full code and name — and, for a
product with several variants, the same for each SKU, with **Stock and movements of …**
to its stock page. Stock on its way between sites in a transfer shows as *In transit …*
beside the total, and *+… incoming* under the site it is going to; it is not part of the
total on hand, which is what lies on shelves. Under a site where the item has a
[reorder point](#set-an-items-primary-location-and-reorder-point), *reorder ≤ 10 pcs*
says what it is, in red while the item is low there. With nothing in stock and nothing on its
way it says **No stock** and offers **Receive** or **Adjust** as you may. A service has no Stock card. See
[the product page](/en/user/products/#the-product-page).

A variant that still lies in stock cannot be deleted in Products, and a product with
stock cannot be made a service: move the stock or adjust it to zero here first. The same
goes for a variant orders have reserved: deliver or cancel those orders first — and for
a variant on its way between sites in a transfer: receive or cancel the transfer first.

## Reserved and available

When an order is confirmed in Orders, the goods on it are **reserved** at the order's
site, so two orders cannot promise the same last item. Under each site's quantity on the
Stock card and on an item's stock page you then see *Reserved … · available …*:

- **Reserved** is what open orders hold at the site;
- **Available** is the quantity on hand at the site, summed over all its locations,
  less what is reserved. It is what a new order can still reserve.

An order's delivery takes the stock off the shelves it is picked from and uses up the
reservation. Cancelling the order, or reducing a line, gives the reservation back.

Your own adjustments, moves and counts always post, even when they take on hand below
what is reserved: they correct what is really there. Available then turns negative and
shows in red, with the warning *More is reserved than lies here*. Receive more, or ask
whoever handles the orders to reduce or cancel them.

Only sites that **fulfil orders** take reservations ([set up a site](#set-up-a-site)).

## The dashboard

**Home** has an **Inventory** card — *Keep track of what lies where* — for everyone with
either view; press it to open Inventory. With both views the row of figures has an
Inventory card: **Stock lines**, the number of item-and-location pairs holding stock,
with this week's receipts beside it (*3 receipts this week*), the week starting on
Monday.

With **Count stock** and both views, **Needs attention** shows **A count has been open
for a long time** for each count still counting a week after it was opened; press it to
open the count, and post or cancel it. The search in the top bar offers **Start count**
to the same people.

With **Transfer stock** and both views, **Needs attention** shows **A transfer is past
its expected arrival** for each transfer not yet received whose expected arrival has
passed, or that was sent more than 7 days ago without one; press it to open the
transfer, and [receive it](#receive-a-transfer), [close the
difference](#close-a-difference) or [cancel it](#cancel-a-transfer).

With both views, **Needs attention** shows, for each active site with anything low,
*4 items at or below their reorder point at a site*; press it to open
[the low-stock list](#the-low-stock-list) filtered to that site.

## Who sees what

| What | View locations | View stock | Manage locations | Adjust stock | Receive goods | Count stock | Transfer stock |
| --- | --- | --- | --- | --- | --- | --- | --- |
| The tile and **Overview** with the lookup — either view is enough | ✓ | ✓ | – | – | – | – | – |
| The sites, their locations and a location's page; locations in the lookup and **Find location or item**; **Print labels** | ✓ | – | – | – | – | – | – |
| Quantities: **Contents**, an item's stock page, **Movements** and **Receipts**, the product page's **Stock** card, the **On hand** column of the product list, the dashboard figures, items in the lookup | ✓ | ✓ | – | – | – | – | – |
| **New site**, **New location**, **Bulk add**, **Edit**, **Archive**, **Reactivate**, **Delete** | ✓ | – | ✓ | – | – | – | – |
| **Low stock**, **Export CSV**; the dashboard's low-stock item | ✓ | ✓ | – | – | – | – | – |
| **New adjustment**, **Adjust**, **New move**, **Move from here** — on Overview, a location, an item, the movements and the product's Stock card; **Edit placement**; **Import opening balances**, both kinds | ✓ | ✓ | – | ✓ | – | – | – |
| **New receipt**, **Receive here**, **Receive** — wherever they appear | ✓ | ✓ | – | – | ✓ | – | – |
| **Counts**, **Open count**, **Count**, **Start count**, counting, posting and cancelling; the dashboard's **A count has been open for a long time** | ✓ | ✓ | – | – | – | ✓ | – |
| **Transfers**, a transfer's page and its history, **Print transfer note**, transfers in the lookup, **Incoming** and **Outgoing** on a site, **Transfer differences** on an item | ✓ | ✓ | – | – | – | – | – |
| **New transfer**, **Edit**, **Save draft**, **Send…**, **Send and receive…**, **Delete draft**, **Receive…**, closing a difference, **Cancel transfer**; the dashboard's **A transfer is past its expected arrival**; **Create transfer** on the low-stock list | ✓ | ✓ | – | – | – | – | ✓ |

Every other row needs every permission it ticks: the posting and counting buttons
appear only to someone who holds both views **and** the write permission. Printing
labels needs only **View locations**: a label shows nothing a location's page does not.
Receiving a transfer needs **Transfer stock**, not **Receive goods**: it moves your own
stock between your sites, and is not a receipt from a supplier. Setting a placement needs
**Adjust stock**: it is a setting of the stock, never stock itself.
None of the seven permissions is sensitive. The full table is under
[Permissions](/en/reference/inventory/#permissions) in the reference.
