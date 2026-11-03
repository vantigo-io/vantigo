---
title: Inventory
description: What lies where — the Inventory app and its sidebar, finding a location or an item by typing or scanning its code, setting up a site, adding locations one at a time and in bulk, changing, moving, archiving and deleting them, printing location labels, receiving goods, adjusting and moving stock with the line editor, entering quantities in packagings, counting stock, importing opening balances, what lies on a location, an item's stock and movements, the Movements list and a movement's page, the Stock card on the product page, the dashboard card, its attention item and the quick actions, and who sees what.
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
A **count** checks what really lies on the shelves and posts the differences.
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
| **Movements** | `inventory:locations-view` and `inventory:stock-view` |
| **Receipts** — the movements list showing receipts only | `inventory:locations-view` and `inventory:stock-view` |
| **Counts** — the stock counts | `inventory:counts-manage`, with both views |
| **Import opening balances** — a site's first stock from a spreadsheet | `inventory:stock-adjust`, with both views |

Anything your permissions do not allow is not shown at all: a button you do not see is a
permission you do not have. The buttons that post — receipts, adjustments and moves —
need both views as well as **Receive goods** or **Adjust stock**; counting needs both
views and **Count stock**. Quantities need both views, because every place stock lies
is a location; **View locations** alone shows the sites and their locations without any
quantity.

## Find a location or an item

**Overview** opens with the field **Find location or item**, ready to type in. Type or
scan one of these and press Enter:

- a location's **full code**, such as `OSL-A-03-2`, in capitals or not;
- a location's **label code**, `LOC` and a number;
- an item's **SKU** or **barcode**, or the barcode of one of its packagings — a box or
  a pallet opens the item.

One match opens it straight away: a location opens its page, an item its stock page.
Several matches are listed, each marked **Location** or **Item**, for you to choose. No
match says *Nothing matches “…”.* and keeps what you typed, so you can correct it.

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
- an address if you want one: **Address**, **Address line 2**, **Postal code**,
  **City** and **Country** (two letters, such as NO).

The new site opens with its page. A site is a location too: stock can lie on the site
itself, which suits a small shop with no shelves, and moves down later when you add
some.

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
  or below it: move it or adjust it to zero first.
- **Reactivate** brings back the location alone — not what lies below it — and only
  under an active parent.
- **Delete** removes the location and everything below it for good. It is refused once
  stock has ever been posted to any of them: archive instead.

The site's own location — the top of the tree — cannot be archived or deleted on its
own; the site is archived or deleted instead. In the site's **…** menu: **Edit site**
(changing the site's code changes every full code in it — printed labels still scan),
**Archive** (refused while the site holds stock; archives every location in it),
**Reactivate** (the site and its own location; the locations below are reactivated one
by one) and **Delete** (only for a site no movement was ever posted in). Each asks you
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
**Location** is *As the receipt* unless you choose another. Press **Post receipt**.

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
item lies, and the item's movements, newest first. **Receive** and **Adjust** are
offered as your permissions allow. An item deleted from the catalog still shows its
history, marked *Deleted from the catalog*.

## Movements

**Movements** lists every adjustment, move, receipt and count, newest first. Filter by
**Site**, **Location** (which includes everything below it), **Item**, **Kind**
(**Adjustment**, **Move**, **Receipt**, **Count**) and **Posted between**, two days in your
organisation's time zone. **Load more** fetches the next page. **Receipts** in the
sidebar is the same list showing receipts only.

A movement's page shows its kind, its site, when and by whom it was posted, the reason
or the supplier and the supplier's reference, the note, the count it posted, and its
lines: **SKU**, **Product**, **From**, **To** and **Quantity**, with **Entered as** for a
line entered in packagings. A movement is never edited or deleted.

## The Stock card on the product page

When Inventory is on and you hold both **View locations** and **View stock**, a
product's page in Products has a **Stock** card: the total on hand per unit, each site
opening to the locations where the item lies — by full code and name — and, for a
product with several variants, the same for each SKU, with **Stock and movements of …**
to its stock page. With nothing in stock it says **No stock** and offers **Receive** or
**Adjust** as you may. A service has no Stock card. See
[the product page](/en/user/products/#the-product-page).

A variant that still lies in stock cannot be deleted in Products, and a product with
stock cannot be made a service: move the stock or adjust it to zero here first.

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

## Who sees what

| What | View locations | View stock | Manage locations | Adjust stock | Receive goods | Count stock |
| --- | --- | --- | --- | --- | --- | --- |
| The tile and **Overview** with the lookup — either view is enough | ✓ | ✓ | – | – | – | – |
| The sites, their locations and a location's page; locations in the lookup and **Find location or item**; **Print labels** | ✓ | – | – | – | – | – |
| Quantities: **Contents**, an item's stock page, **Movements** and **Receipts**, the product page's **Stock** card, the **On hand** column of the product list, the dashboard figures, items in the lookup | ✓ | ✓ | – | – | – | – |
| **New site**, **New location**, **Bulk add**, **Edit**, **Archive**, **Reactivate**, **Delete** | ✓ | – | ✓ | – | – | – |
| **New adjustment**, **Adjust**, **New move**, **Move from here** — on Overview, a location, an item, the movements and the product's Stock card; **Import opening balances** | ✓ | ✓ | – | ✓ | – | – |
| **New receipt**, **Receive here**, **Receive** — wherever they appear | ✓ | ✓ | – | – | ✓ | – |
| **Counts**, **Open count**, **Count**, **Start count**, counting, posting and cancelling; the dashboard's **A count has been open for a long time** | ✓ | ✓ | – | – | – | ✓ |

Every other row needs every permission it ticks: the posting and counting buttons
appear only to someone who holds both views **and** the write permission. Printing
labels needs only **View locations**: a label shows nothing a location's page does not.
None of the six permissions is sensitive. The full table is under
[Permissions](/en/reference/inventory/#permissions) in the reference.
