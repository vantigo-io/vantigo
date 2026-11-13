---
title: Manufacturing
description: The Manufacturing app — its sidebar and the Overview; setting up a work center — its code, site, capacity, time efficiency, setup and cleanup, cost rates, its weekly working pattern and its closures — archiving, reactivating and deleting one, and grouping interchangeable work centers; making a bill of materials for a product — its output quantity, its lines with scrap, its operations at work centers or work center groups with their instructions, what each operation depends on and its send-ahead quantity, and which operation consumes a line, and its by-products with their share of the cost — the default and the alternative bills of materials, archiving and reactivating them; reading a bill of materials' overview — the quantities with scrap, made and bought, what is available at a site, the expected minutes and, for those who see costs, the rolled-up cost; making a manufacturing order — creating, changing and confirming it, reserving its components and the short lines, adding and changing a component, registering a production with its prefill and the deviation warning, the lot it makes, serial numbers and the lots it consumes, the order's Lots tab, closing it with or without a backorder, cancelling it, splitting it, merging orders, unbuilding what a done order made and reading its history — and the overview's order figures; the shop floor — choosing your work center, its queue, starting, pausing and finishing a work order, starting one out of sequence, Group work, registering a quantity passed on, switching to another, registering production at the last operation, reporting scrap, blocking and unblocking a stopped work center, My work and the 12-hour warning; and for supervisors the order's work orders and time logs, correcting a log and adding a missing one; the production schedule — planning an order and planning all, its requested start, reading the board's load, colours and closed days, operations side by side and after a send-ahead quantity, the member of a group a work order is planned at, moving a work order by dragging it — to another member's row too — or with Move to, and removing a plan; an order's cost — the estimate, the actual, the deviation and the cost per unit, its split over the by-products, and the work orders' time deviation; the reports — production cost, work-center utilisation and time deviation, and downloading them; quality — the checks of an operation, recording them at the station, what they stop, and deviations — reporting one, putting the output in quarantine, closing it and releasing its holds; replenishment suggestions — what is projected, what is suggested to make or buy and by when, creating draft or confirmed orders from them, dismissing one, running it now, exporting the buy suggestions and automatic creation on a bill of materials; why Products refuses to delete a variant a bill of materials or an open manufacturing order names; and who sees what.
sidebar:
  order: 66
sources:
  - apps/manufacturing/frontend
  - apps/host/frontend/src/routes/manufacturing
---

The Manufacturing app describes how your products are made. In Norwegian the app is
**Produksjon**. A **work center** is a machine, a cell or a team where a step of the work
is done, with the hours it is open. A **bill of materials** (a BoM) tells how to make one
product: which components go into it, in which quantities, and the **operations** — the
steps, each at a work center — that turn them into the product. The **overview** of a BoM
explodes it into everything a given quantity needs, level by level, with what is in stock
and how long the work is expected to take. A **manufacturing order** makes a quantity of
a product by one of its bills of materials: it reserves the components, and records each
**production** — what was made and what it used. Each operation of a confirmed order is a
**work order** at its work center, and the **shop floor** is where operators start, pause
and finish them, recording the time they work. The **production schedule** places the work
orders on the work centers' calendars, and the **reports** compare what was planned with
what happened.

The products and components come from [Products](/en/user/products/), and only **goods**
can be made or used: never services. The sites and the stock come from
[Inventory](/en/user/inventory/). The rules behind every screen — the limits, the
refusals and the permission each request is checked against — are in the
[Manufacturing module reference](/en/reference/manufacturing/).

## The app and its sidebar

The **Manufacturing** tile is shown to everyone with **Use Manufacturing**
(`manufacturing:access`). The sidebar shows:

| Area in the sidebar | What it holds |
| --- | --- |
| **Overview** | **Reports** in its header; **Orders to start today**, **Orders in progress** and **Late orders**, and the orders late or due this week; **Active work centers**, **Blocked work centers**, **Active bills of materials** and **Suggestions to make**: how many there are |
| **Manufacturing orders** | Every open manufacturing order, with its number, product, quantity, site, due date and reservation status |
| **Suggestions** | What to make or buy so that stock stays above each product's reorder point, site by site, with the reason and the date it is needed by |
| **Unbuilds** | Every unbuild, newest first: what it took apart, from which order and lot, and the components it restored |
| **Shop floor** | The work centers' queues, where operators start, pause and finish work orders, and **My work** |
| **Production schedule** | The board: each work center's planned work orders, day by day, with its load, and the orders still to plan |
| **Deviations** | What went wrong: every deviation, open ones first in view, with its order, product and lots, and whether it holds them in quarantine |
| **Bills of materials** | Every bill of materials, with its product, its code, its output quantity, whether it is the default, and its number of lines and operations |
| **Work centers** | Every work center, by code, with its site |

Everyone with Use Manufacturing reads them all. Creating and changing work centers and
bills of materials needs **Manage bills of materials and work centers**
(`manufacturing:boms-manage`) as well; without it the buttons that change something are
not shown. Creating and working with manufacturing orders needs **Manage manufacturing
orders** (`manufacturing:orders-manage`). Every cost figure needs **See manufacturing costs** (`manufacturing:costs-view`)
— see [Who sees what](#who-sees-what).

When there is no bill of materials yet, **Overview** says so and offers **Go to bills of
materials**. Set up your work centers first: an operation of a BoM names one.

The order figures on **Overview** are: **Orders to start today**, confirmed orders not yet
started whose planned start is today or earlier; **Orders in progress**; and **Late orders**, open
orders due before today. Below them, the orders late or due by the end of this week —
Sunday, in the business's own time zone — are listed with their due date and status;
open one to see whether its components are ready. Whether an order's components are
available is shown on the order page only, not on **Overview** or in the list. The
figures and the list follow the orders as they change.

## Set up a work center

1. Open **Work centers** and choose **New work center**.
2. Fill in the work center:
   - **Code** — 1 to 16 letters A–Z and digits, such as `CNC1` or `ASM`. It is stored in
     capitals, and two work centers cannot share one.
   - **Name** — what people call it, such as *CNC mill 1* or *Assembly line*.
   - **Site** — the Inventory site it stands at. Only active sites are offered. While an
     active bill of materials has an operation at the work center, its site cannot be
     changed: archive those bills of materials, or move their operations, first.
   - **Capacity** — how many units it processes at the same time in one cycle; 1 unless
     changed. An oven that bakes 12 loaves at once has a capacity of 12.
   - **Efficiency** — a percentage from 1 to 500; 100 unless changed. At 50 the work
     takes twice as long as the operation's time says; at 200, half as long.
   - **Setup minutes** and **Cleanup minutes** — the time to get it ready before a job and
     to clear it after, counted once per job. An operation can state its own setup.
   - **Machine cost per hour** and **Labour cost per hour** — in NOK, both optional. They
     are shown only to those with **See manufacturing costs**, and only they can set or
     clear them; when someone without the permission saves the work center, the rates
     stored stay as they were.
3. Set the **Weekly pattern** — the hours it is open, day by day (see below).
4. Choose **Create work center**. On a work center that exists, the button is **Save**.

### The weekly pattern

The **Weekly pattern** is the work center's calendar: up to **two intervals** a day, added
with **Add interval**, such as
07:00–11:00 and 11:30–15:00 around a lunch break. A new work center is open Monday to
Friday, 07:00–15:00, and closed at the weekend.

- Times are on a **15-minute grid**: 07:00, 07:15, 07:30 and so on.
- Each interval starts before it ends, and a day's two intervals may not overlap.
- An interval may end at **24:00**, for a shift that runs to midnight.
- A day with no interval is **Closed**.

The times are local times in the workspace's time zone. Norwegian public holidays are
closed days whatever the pattern says.

### Closures

A **closure** closes the work center for a stretch of days — an industrial holiday, a
breakdown, a planned overhaul. On the work center, under **Closures**, give the **From**
date, the **To** date (the same day or later) and a **Note**, and choose **Add closure**. A work center keeps
at most 100 closures; delete the old ones you no longer need. Deleting a closure opens the
days again.

## Archive, reactivate or delete a work center

- **Archive** takes a work center out of use: it is no longer offered for a new
  operation, and **Work centers** shows it only when you set **Status** to **Archived** or
  **All**.
  The bills of materials whose operations name it keep it.
- **Reactivate** brings it back. That is refused when its site has been archived in
  Inventory since: reactivate the site first, or make a new work center at another site.
  A work center at such a site can still be edited and keeps its site; moving it is to an
  active site only.
  Archiving is refused while a work order that is neither done nor cancelled is at the
  work center, or while it is blocked: finish the work, or close or cancel its orders, and
  unblock it, first.
- **Delete** removes it for good, and is allowed only while no operation of any bill of
  materials, no work order and no time log names it. Otherwise you are told it is in use;
  archive it instead.

When someone else saved the work center after you opened it, your save is refused and you
are asked to reload, so that nobody overwrites another's change unseen.

## Group interchangeable work centers

When two or more work centers can do the same work — two saws, three welding cells — put
them in a **work center group**. An operation can then target the group instead of one
work center, and each order's work order goes to whichever member finishes it first.

1. Open **Work centers**. Under **Work center groups**, choose **New group**.
2. Give the group a **Code** (1 to 16 letters A–Z and digits, unique among the groups) and
   a **Name**, choose its **Site**, and pick its **Members**: active work centers of that
   site.
3. Choose **Save**.

**Edit** changes the code, the name and the members. A group's site cannot change while an
active bill of materials has an operation that targets it, and a member's own site cannot
change while it is in a group. **Archive** is refused while an active bill of materials
targets the group; **Reactivate** brings an archived group back. An archived member stays
in the group, but work is no longer planned at it.

## Make a bill of materials

1. Open **Bills of materials** and choose **New bill of materials**.
2. Pick the **Product variant** — the variant to make — by typing its SKU or name. Only goods of
   active products are offered, and those that already have a BoM are marked. The product
   cannot be changed once the BoM is saved; make a new one instead.
3. Fill in the header:
   - **Output quantity** — the quantity the BoM makes, more than 0 with at most three
     decimals. Every line's quantity and every operation's time is stated per this
     quantity: a BoM for 1 bicycle, or a BoM for a batch of 50 litres.
   - **BoM code** — optional, up to 32 characters, unique among the bills of materials,
     such as *Rev B* or *Summer recipe*. It tells the alternatives of one product apart.
     Left empty, the BoM has no code.
   - **Lead time (days)** — 0 or more, 0 unless changed.
   - **Make default** — on a new BoM only: make it the product's default at once (see
     [The default and the alternative bills of materials](#the-default-and-the-alternative-bills-of-materials)).
   - **Make to order** — the product is made for each sales order rather than to stock
     ([Make to order](#make-to-order)).
   - **Lot number pattern** — for a product tracked in lots: how the lot its
     productions make is numbered, `{MO}` unless changed. See
     [Lots and serial numbers](#lots-and-serial-numbers).
   - **Note** — up to 2,000 characters.
4. Under **Operations**, choose **Add operation** for each step of the work, in the order
   they are done. Each has:
   - **Seq.**, the order it runs in;
   - **Operation**, its name, such as *Cut*, *Weld* or *Paint*;
   - **Work center**, among the active ones — or a **Group** of work centers, which lets
     planning and the shop floor choose the member (see
     [Group interchangeable work centers](#group-interchangeable-work-centers)). All the
     operations of one BoM must be at work centers on the **same site**;
   - **Depends on**, the operations it runs after — only operations before it in
     **Seq.** are offered. A new operation depends on the one added before it; clear
     the field for one that can start at once, or pick two or more for one that waits for
     several. Operations that depend on the same one run side by side;
   - **Send-ahead quantity**, optional: how many good units this operation passes on before
     the operations after it may start, so they can overlap with it. Empty means they wait
     until it is finished;
   - **Setup minutes**, optional: leave it empty to use the work center's;
   - **Minutes per cycle**, more than 0: the time one cycle of the work center takes;
   - **Instructions**, in Markdown, up to 20,000 characters: what the operator needs to
     know at the station;
   - **Quality checks**, optional: what the operator records at the station — see
     [Quality checks on an operation](#quality-checks-on-an-operation).
5. Under **Components**, choose **Add component** for each line. Each has:
   - **Component**, a goods variant picked as the product was;
   - **Quantity** per output quantity, more than 0 with at most three decimals, in the
     component's own unit;
   - **Scrap %**, from 0 to 99.99, 0 unless changed: the share you expect to lose, which
     is added to what the BoM needs;
   - **Consumed by**, the operation that consumes it, optional: pick one of the operations
     above, or **No operation**;
   - **Order**, where the line stands in the list;
   - **Note**.

   The same component may appear on two lines.
6. Under **By-products**, choose **Add by-product** for anything else the operations make —
   sawdust from cutting, whey from cheese. Each has:
   - **By-product**, a goods variant picked as the product was;
   - **Quantity** made per output quantity, more than 0 with at most three decimals;
   - **Cost share**, the percent of the order's cost it carries, 0 to 100 with at most
     two decimals, none unless changed.

   The shares together may be at most 100 %; the product carries the rest, which the
   section shows. A BoM has at most 20 by-products.
7. Choose **Create bill of materials**. On a BoM that exists, the button is **Save**.

A BoM has at most 200 lines and 50 operations. A line can name an operation you added in
the same edit, before either is saved.

Below the operations, **Order of the operations** shows them in steps: step 1 is what can
start at once, each next step what runs after the steps before it, and the operations of
one step run side by side. Every branch must lead to the last operation, the one that makes
the product: an operation that no later operation depends on is refused — *No operation
runs after this one* — unless it is the last. An operation that depends on one that is not
before it in sequence is refused too. The editor marks both on **Depends on** before it
sends. Removing an operation others depended on makes them depend on what it depended on,
so the order stays.

**Editing** a BoM saves its lines and operations as you see them, in one go. When you
remove an operation a line named, the line simply no longer names one. The edit is
refused when someone else saved the BoM after you opened it; reload and make your change
again.

### Quality checks on an operation

Under each operation, **Add check** adds a quality check, up to 20 an operation. Each has:

- a **Check**: its title, such as *Fill weight* or *Seal tight*;
- a **Kind**:
  - **Instruction** — read and acknowledged with **Done**;
  - **Pass or fail** — the operator answers **Pass** or **Fail**;
  - **Measure** — the operator types the measured value, and Vantigo judges it: within
    the limits is a pass, outside them a fail. Give a **Min**, a **Max** or both, and
    optionally the **Nominal** — between them — and the **Unit**. Up to four decimals;
    a measure may be negative;
  - **Photo** — the operator takes a picture with the tablet's camera;
- **When**:
  - **Each work order** — recorded once before the work order is finished;
  - **First piece** — recorded before anything made at the work order is passed on, and
    shown first when the operator starts;
- **Instructions for the operator**, optional;
- **Quarantine on fail**: when the check fails, the deviation it opens puts what the
  order makes on hold — see [Deviations](#deviations).

The checks are saved with the bill of materials, as the editor shows them: removing a
check, or every check of an operation, and saving deletes it from the bill of materials.
A manufacturing order gets its operations' checks when it is confirmed: changing the bill
of materials afterwards does not change the checks of an order already confirmed.

### What a bill of materials may not do

- **Contain itself.** A component may not be the BoM's own product, nor be made — through
  its own default BoM, at any depth — from something that is. The refusal names the path,
  such as *Bicycle → Frame → Bicycle*, so you can see which BoM to change.
- **Be more than 10 levels deep**, counting each component made from a BoM of its own.
  A BoM that is, or becomes, its product's default also counts the levels above it: the
  default BoMs that use its product as a component, and those that use theirs. So a
  product at the bottom of a chain ten levels deep cannot be given a default BoM.
- **Use a service.** The product and every component must be goods. A product turned into
  a service, or deleted, since cannot be saved on a BoM; the refusal names the line.
- **Spread its operations over sites.** Every operation's work center must stand at the
  same site.
- **Make its own product or a component as a by-product.** A by-product may be neither
  the BoM's product nor one of its components — nor, through the components' own BoMs, be
  used to make the product: that is a circle, refused as one.

## The default and the alternative bills of materials

A product may have several active bills of materials — alternative recipes, a new revision
beside the old one. Exactly one of them is the **default**: the one the overview follows
when the product is a component of something else.

- The first active BoM of a product becomes its default by itself, whether or not **Make
  default** was ticked.
- **Make default**, on another of the product's BoMs, makes that one the default and
  unmarks the old one at the same time.
- **Archive** takes a BoM out of use; it is kept, and **Bills of materials** shows it when
  you set **Status** to **Archived** or **All**. An archived BoM cannot be edited or made
  the default. When you archive the default, the product's oldest other active BoM becomes
  the default, or the product has none. When that oldest one would now contain itself or
  go more than 10 levels deep, it is not made the default either: the product is left
  without one until you choose **Make default** on a BoM that can be.
- **Reactivate** brings an archived BoM back. It becomes the default when the product has
  no active default. It is checked again as if it were saved, so it is refused if it
  would now contain itself or go too deep, if a component is no longer goods, or if an
  operation's work center has been archived, or moved so the operations stand at more than
  one site, since.

## Read a bill of materials' overview

On a BoM, choose **Overview**. It shows the BoM as a tree: the product at the top, its
components below it, and — for a component that has a default BoM of its own — that BoM's
components below that, as deep as it goes.

- **Quantity to make** — how much of the product to look at; the BoM's output quantity
  when it is empty.
- **Site** — choose a site to see what is available there.

Each row shows:

- **Required**: the line's quantity scaled to the quantity above it, with the
  scrap added, **rounded up** to three decimals so that you never plan too little;
- **Made** or **Bought**: made when the component has a default active BoM of its own,
  which the tree then follows; bought when it has none;
- **Available**, at the chosen site: what is in stock there and not reserved;
- the **operations** of a made row, each with its **expected minutes**: the setup, the
  cleanup, and the cycles the quantity needs on the work center at its capacity and its
  time efficiency, rounded to the minute.

For example, an operation of 6 minutes per cycle on a work center with capacity 2, setup
15 and cleanup 5, for 10 of a product whose BoM makes 1, needs 5 cycles: 15 + 5 + 5 × 6 =
50 minutes.

With **See manufacturing costs** the overview also shows:

- each row's **Standard cost**, from the product in Products, or **No standard cost**, and
  its **Value** — the quantity required at that cost;
- each operation's **cost**: its expected hours at the work center's machine rate plus
  its labour rate;
- the rolled-up cost: **Components at standard cost**, **Operations** and, their sum,
  **Rolled-up cost**. A made
  component with a standard cost of its own counts at that cost, which stands for
  everything below it; one without is counted from its own components and operations.

When a bought component has no standard cost, it counts as 0 and the overview says **The cost
is incomplete**. Set the standard cost in Products to complete it.

A BoM whose tree would have more than 2,000 rows is too large to show; the overview says
so.

## Manufacturing orders

A **manufacturing order** says: make this quantity of this product, by this bill of
materials, at this site, by this date. It is numbered **MO-1**, **MO-2** and so on as it
is created. **Manufacturing orders** lists the open ones — drafts, confirmed and in
progress — with their product, quantity, site, due date, priority and, for a confirmed or
started order, whether its components are **Reserved**, **Partly reserved** or **Not
reserved**.

Working with orders needs **Manage manufacturing orders** (`manufacturing:orders-manage`)
beside Use Manufacturing; registering a production also allows **Work on the shop floor**
(`manufacturing:shop-floor`) instead. Without them you read the orders, but the buttons
that change something are not shown.

An order goes through these states:

| State | What it means |
| --- | --- |
| **Draft** | Being prepared; everything can change, and it follows its bill of materials as that changes |
| **Confirmed** | The components are copied from the bill of materials and reserved |
| **In progress** | At least one production is registered, or a work order has been started |
| **Done** | Closed |
| **Cancelled** | Cancelled before anything was produced |

### Find an order

On **Manufacturing orders**, filter by **Status**, **Site**, **Work center** (the orders
with a work order there; for a draft, those whose bill of materials has an operation
there), product, a due-date range, **Late** (due
before today and not done or cancelled), origin, **Responsible**, or the reservation
status, and search on the number, such as `MO-12`, or the product's SKU or name. Sort by
due date, number or priority. The list updates by itself when someone changes an order.

### Create an order

1. Open **Manufacturing orders** and choose **New manufacturing order**.
2. Pick the **Product variant**. Only products with an active bill of materials can be
   made; for one without, make a bill of materials first.
3. Fill in the order:
   - **Bill of materials** — the product's default, unless you choose one of its
     alternatives;
   - **Quantity** — more than 0, with at most three decimals;
   - **Site** — where it is made: the site of the bill of materials' work centers, else
     the first active site. An order cannot be made at another site than the work centers
     its operations name;
   - **Output location** — where the finished goods are put; the site's top location
     unless you choose one;
   - **Component location** — optional: where the components are taken from. Leave it
     empty to take each component from its primary location, then from the other
     locations that hold it;
   - **Due date** — the day the goods must be ready. The planned dates are not set here:
     only [planning](#plan-an-order) sets them, and when you change an order the form
     shows them as **Planned**, read-only;
   - **Priority** — **Normal** or **Urgent**;
   - **Responsible** — the person who follows the order up. A disabled user cannot be
     chosen;
   - **Note** — up to 2,000 characters.
4. Choose **Create manufacturing order**. The order is a **Draft**.

The locations offered are the site's active ones.

### Change an order

On a draft you can change everything. On a confirmed or in-progress order you can change
only the dates, the priority, the responsible person, the note and the locations; to make
another product or quantity, cancel it and create a new one. The locations must be active
locations of the order's own site; the order keeps its site and bill of materials even
when the bill of materials' work centers have moved to another site, or the site has been
archived, since it was confirmed. A responsible person who has been disabled since stays
on the order, but you cannot choose a disabled one. A done or cancelled order
cannot be changed. When someone else saved the order after you opened it, your save is
refused and you are asked to reload.

The form also has **Requested start**: the day [planning](#plan-an-order) may start the
order from. Leave it empty to plan from the default; on an order that exists the field
names that default.

### Confirm an order

On a draft, choose **Confirm**. Confirming:

- copies the bill of materials' lines onto the order as its **components**, so later
  changes to the bill of materials no longer reach it. Each component's **Required**
  quantity is the line's quantity scaled to the order's quantity, with the scrap added,
  rounded **up** to three decimals;
- **reserves** the components at the site, so that nobody else takes them;
- creates a **work order** for each operation of the bill of materials, at its work
  center, with its instructions and its expected minutes. The first operation is ready to
  start on the [shop floor](#the-shop-floor); the others wait their turn. An order whose
  bill of materials has no operations has no work orders. An operation that targets a
  work center group is given the group's first active member; when none of the group's
  work centers is active, the order is not confirmed — reactivate one, or add one to the
  group, first.

When the site does not have enough of a component, the order is confirmed all the same:
the lines that could not be reserved are left unreserved, and the page names each one with
the quantity missing. When the product or one of its components is **discontinued** in
Products, the order is created and confirmed all the same, and the order page warns
**A discontinued product**: it uses up what is left. A line is reserved in full or not at all. A draft whose bill of
materials has been archived since cannot be confirmed; create the order again from an
active one.

### Reserve the short lines

When stock has come in, choose **Reserve now** on a confirmed or in-progress order. It
tries again to reserve every line that is not yet covered, in the same way, and names the
lines still short.

The order page shows for each component its **Required**, **Reserved**, **Available** at
the site, **Consumed**, **Scrapped** and **Missing** quantities, and the order's component
status:

- **Ready** — every line's remaining need is reserved, or available at the site;
- **Partly available** — some lines are covered, some are not;
- **Waiting** — no line is covered.

What is available at the site is shared between the lines of one component, in the order
the lines stand. The page computes this as you look at it; the list shows only what is
reserved.

### Add or change a component

On a confirmed or in-progress order:

- **Add component** adds a line the bill of materials did not have — goods only — with its
  **Required** quantity. It is reserved at once if the site has it.
- Changing a line's **Required** reserves the difference when you raise it, and gives the
  excess back when you lower it. It cannot go below what the line has already consumed and
  scrapped.

The bill of materials itself is not changed.

### Register a production

When goods are made, choose **Register production** on a confirmed or in-progress order.

1. Fill in **Quantity** — the good units made — and, if any, **Output scrap** — finished
   units made but thrown away. Output scrap never goes into stock and does not count as
   produced.
2. Each component line is **prefilled** with what that output should use: its required
   quantity × (quantity + output scrap) / the order's quantity, rounded to three decimals.
   Change **Consumed** where more or less was used, and add **Scrap** for components lost.
   You can also choose the location a line is taken **From**, and where the output is put
   (**To**, the order's output location unless you choose another). A line you remove
   consumes nothing.
3. Choose **Register**.

If a consumed quantity differs from its prefilled figure by more than 10 %, you are warned
and shown each such line with what was expected; choose to register it anyway, or correct
it. A quantity of 0 is allowed only when something is consumed or scrapped.

Registering takes the components out of stock — from the line's location, else the
order's component location, else the component's primary location first and then the
other locations that hold it — and puts the good units on the output location, in one
**Production** movement in Inventory, which links back to the order. It is refused when
the site does not hold enough of a component, when the site has been archived, or when
Inventory refuses for another reason; nothing is then registered. The order moves to **In
progress**. Registering more than the order's quantity is allowed: the page shows the
overrun. Sending the same registration twice — a double click, a lost connection — posts it
only once.

**By-products.** When the order's bill of materials has by-products, the dialog lists
them under **By-products**, each prefilled with what the good quantity should give: its
expected quantity × the quantity / the order's quantity. Change the quantity made of
each, 0 for none. A by-product tracked in lots goes into the lot its number names — the
order's lot number pattern rendered for it unless you type another. By-products are put
on the order's output location, in the same Production movement. A by-product tracked by
serial number is refused: track it in lots.

A registered production cannot be undone in Manufacturing. Correct a mistake with an
adjustment in [Inventory](/en/user/inventory/), or take finished goods apart with an
[unbuild](#unbuild-what-an-order-made).

### Lots and serial numbers

When the product or a component is tracked in **lots** or **serial numbers** (its
variant's **Tracking** in [Products](/en/user/products/#track-lots-and-serial-numbers)),
**Register production** asks for them too.

**The lot the production makes.** For a product tracked in lots the form shows its **Lot
number**, made from the BoM's **Lot number pattern** as the order kept it when it was
confirmed:

- `{MO}` — the order's number, the default: every production of the order adds to one lot,
  *MO-12*, whatever the day.
- `{YYMMDD}`, or `{YYYY}`, `{YY}`, `{MM}`, `{DD}` — the production date: `L{YYMMDD}-{N}`
  makes a new lot each production, *L261112-1*, *L261112-2*.
- `{SKU}` — the product's SKU; `{N}` — a running number, counted for each prefix. A number
  it counts always makes a new lot: when someone has typed that number already, the next
  is taken.
- Anything else is written as it is. A lot number has 1–20 letters, digits and `. _ / -`;
  the BoM editor shows an example and refuses a pattern that does not give one, judged on
  a long order number and running number so a pattern that only fits today's short ones
  is caught too (**Can render … on a later order**).

Keep the number or type another; a number the product already has adds to that lot.
**Production date** is today unless you change it. **Expiry** and **Best before** are
left empty to take the product's own number of days from the production date; a date you
type on a lot that already exists must be the lot's own (**The lot exists with other
dates**).

For a product tracked in **serial numbers**, the good quantity must be whole, and each
unit gets its own number: **First serial number** is counted up, one per unit — by
default the order's number and four digits, *MO-12-0001*, continuing after the highest such
number the order made before. Every unit gets a new serial number: one that is used
already, even by a unit delivered long ago, is refused (**That serial or lot number is
taken already**). Output scrap is never numbered.

**The lots it consumes.** Under each tracked component the form shows the lots it would
take, the one that expires first first — *Taken first-expiry-first when you post: B-1 (1
Dec 2026) 30 · B-2 10* — or that none is in stock, or how much the lots in stock are short.
Left as it is, Inventory takes the lots when you post, first-expiry-first, skipping lots
on hold and expired lots. To take others, choose **Change lot**: each row is a lot at a
location, chosen among the site's lots with their expiry and what lies there (lots on hold
and expired lots are marked), and the quantity taken from it. **Add lot** adds a row, the
cross removes one, and **Take first-expiry-first** goes back to the proposal. The rows must
add up to what the line consumes and scraps; the form says when they do not, and marks a
row of 0 (**More than 0**) and a lot chosen in two rows (**Chosen in another row**). When
you choose where a component is taken from (**Take … from**), the lots shown are proposed
again from that location, as Inventory will take them there. A lot that is
expired can be used only by someone allowed to use expired lots, who ticks **expired** on
its row; a lot on hold or blocked is refused (**A lot is on hold or blocked and cannot be
used**).

When lots you did not change were taken by someone else in the meantime, the production
proposes them again once from what is in stock and posts. When what is left no longer
covers the line, nothing is posted and the form says why.

**The order's lots.** The order page's **Lots** tab lists the **Lots made**, with what the
productions put in each and how many productions, and the **Lots consumed** per component
with their expiry, consumed and scrap. Each lot links to its page in
[Inventory](/en/user/inventory/), and **Trace** beside it opens its
[trace](/en/user/inventory/#trace-a-lot) there: the lots made from it and who received
them. The links are there when you may see Inventory's locations and stock; otherwise
the lot numbers are shown without them. A work order's page on the shop floor shows the
same under **Lots**.

### Close an order

Choose **Close** on a confirmed or in-progress order when the work is finished. Whatever is
still reserved for it is released, and the order is **Done**. Any time still running on
its work orders is ended, and the work orders not done are cancelled.

Once something good has been produced, the order closes only when every
[quality check](#record-the-checks) of its work orders has a result: *Checks are
missing* names the ones without, so they can be recorded first. A failed check is a
result.

When less than the quantity was produced, you are asked whether to **create a backorder**:

- **Yes** creates a new order for the rest — the quantity less what was produced — with
  the same bill of materials, site, locations, due date, priority and responsible person.
  It is numbered after the original: the first backorder of MO-12 is **MO-12-2**, the
  next **MO-12-3**, even when it is a backorder of MO-12-2. It is confirmed at once and
  reserves its components; the lines it could not reserve are named. This holds even when
  the bill of materials has been archived since: the backorder keeps it, while a new order
  or a draft cannot use it.
- **No** closes the order short.

### Cancel an order

Choose **Cancel order** on a draft, a confirmed or an in-progress order while nothing has
been produced, consumed or scrapped on it. Its reservations are released and it keeps its
number. Any time still running on its work orders is ended, and its work orders are
cancelled. Once something has been registered it cannot be cancelled; close it instead. An
order split into parts is not cancelled while one of its parts is still open: cancel or
finish the parts first. A done or cancelled order is final.

### Split an order

Choose **Split** on a draft or confirmed order before anything has been registered on it
and before any time or quantity has been logged on its work orders.

1. Type the quantity of each part. The order keeps the first; **Add part** adds another,
   up to 20. The parts must add up to the order's quantity: the dialog shows the sum.
2. Choose **Split**.

Each part beyond the first becomes a new order numbered as a backorder of the original —
**MO-12-2**, **MO-12-3** — with **Split from** pointing back, and the same bill of
materials, origin, due date, locations, priority and responsible person. A planned order
is unplanned, and every part starts unplanned: plan them again on the board. A confirmed
order releases its reservations; its component lines, by-products and work orders are
taken for each part's quantity, the work orders' expected minutes worked out again, and
every part reserves again. The lines left short are named. A part of a make-to-order order
is made for the same sales order line.

### Merge orders

In **Manufacturing orders**, tick two or more orders and choose **Merge**. They must make
the same product by the same bill of materials at the same site, all be drafts or all be
confirmed, have nothing registered and no time or quantity logged, and come from the same place — all
made by hand, all from suggestions, or all for the same sales order line or parent order.
Orders of different origins are not merged, so neither promise is lost. The orders are
merged as the list showed them: if one was changed since, the merge is refused and the
list asks you to try again. Ticks are cleared when you change the filter or the page.

The order with the lowest number survives, with the quantities added up and the earliest
due date; the others are cancelled with **Merged into** pointing to it. Every order is
unplanned. Confirmed ones release their reservations; the survivor takes the others'
component lines and by-products, its work orders' expected minutes are worked out again,
and it reserves again.

### Unbuild what an order made

Choose **Unbuild** on a done order to take finished goods apart — a faulty unit, a
recalled lot.

1. For a product tracked in lots or serial numbers, choose the **Lot** among those the
   order's productions made; each says how much of it is left to take apart.
2. Type the **Quantity**, at most what is left, and a **Note** if you like.
3. **Take from** is the order's output location and **Put the components at** its
   component location; choose others if the goods or the parts are elsewhere.
4. Choose **Unbuild**.

The components go back each into the lot it was taken from — or, with no component
location, to the site's main location. Only what the productions that made the chosen lot
consumed comes back, in proportion — what they consumed made the good units and the
scrapped ones, so a unit taken apart gives back what one unit used: an unbuild never puts
components into lots another lot was made from. A part tracked by serial number comes
back whole: taking apart fewer units than would give back whole serials is refused —
unbuild whole units. An order whose productions made by-products is not unbuilt from the
order, since what it used did not make the product alone; take such goods apart by the
bill of materials instead. The order shows how much was **Unbuilt**; its cost is not
worked out again. Inventory records an **Unbuild** movement, which a lot's
[trace](/en/user/inventory/) shows as an end and never follows, linking to the unbuild.

A lot on hold can be taken apart — that is how a recalled item is dealt with — and then
every lot it restores to is put in **quarantine** by the unbuild, so held material does
not come back usable. The quarantine is on the whole of each lot restored to, what it
held before included. Once the lots are inspected, someone with **Manage quality** opens
the unbuild and chooses **Release the quarantine**; a hold put by anything else stays.

**Unbuilds** in the sidebar lists every unbuild with what it restored, and marks one whose
quarantine is not yet released. Choose an unbuild's number to open its page: what it took
apart, from where and to where, and what it restored into which lots.

An unbuild by a bill of materials, rather than from an order, can take apart any lot of
the product — one an order made included. It is not held to what that order has left to
take apart, and the order does not count it as unbuilt.

### Read an order's history

The order page shows its **History**: every event — created, changed, confirmed,
reserved, a production registered with its quantities, a component added or changed,
closed, cancelled, a backorder created, a time log corrected or added, split, split from
another order, orders merged into it, merged into another, unbuilt — with who did it and
when. Nothing in it can be
changed or removed.

### Work orders and time logs on the order page

The order page's **Work orders** tab lists the order's work orders by sequence — one for
each operation of its bill of materials — with the work center, the state, **Logged /
expected** minutes, the **Actual time** — the time the work center was worked, counted
once however many operators were on it — and the **Deviation** from the expected, in
minutes and per cent, and who is **Running now**. A draft has none yet: they are made when it
is confirmed.

The **expected minutes** are worked out once, at confirm, from the operation and the work
center as they were then: the setup, the cleanup, and the cycles the order's quantity
needs at the work center's capacity and time efficiency, rounded to the minute — the same
figure as the [overview](#read-a-bill-of-materials-overview) gives. Later changes to the
bill of materials or the work center do not change them — with one exception: a work order
of a group moved to another member before any time is logged on it takes that member's
setup, cleanup, capacity, efficiency and rates, and its expected minutes are worked out
again with them. An operation's own setup stays its own.

**Time logs** lists every log on the order's work orders: the **Person**, the **Work
order**, the **Kind** — **Productive**, or **Downtime** with its reason — **Started**,
**Ended** (or **Running**), **Duration**, **Quantity made** and the **Note**. A log is
marked **Out of order** when its work order was started with Start anyway, **Corrected**
with who corrected it and when, and **Over 12 h** when it has run that long.

### Correct a time log

When an operator forgot to pause, or started on the wrong work order, a supervisor puts
it right. It needs **Manage manufacturing orders**; without it the tab is read-only.

1. On the order's **Work orders** tab, under **Time logs**, choose **Correct** on the log.
2. In **Correct time log**, change **Started**, **Ended** or **Quantity made**, and say
   **Why** — it is required, and kept on the log as your words; the order's history
   records the correction, who made it and when.
3. Choose **Correct**. A log still running is ended at the end you give.

To record time nobody logged, choose **Add missing log**: pick the **Work order** (when the
order has more than one), the **Person**, the kind — **Productive** or **Downtime** with
its **Reason** — **Started** and **Ended**, optionally **Quantity made**, and **Why**, then
choose **Add**. An added log changes no work order's state: only Start, Pause and Finish
on the shop floor do.

A correction or an addition is refused when:

- the log, or the new start, is more than **60 days** old;
- the order is done or cancelled;
- the time overlaps another log of the same person;
- the end is not after the start, or lies in the future.

Every correction and addition is kept in the order's **History**, with who made it and
when.

## The shop floor

The **Shop floor** (`/manufacturing/shop-floor`) is where operators work. It is laid out
for a tablet at the work center: large buttons, no hovering, readable at arm's length.
You work as the person signed in, so each operator signs in with their own account;
switching operator means switching account.

Working on the shop floor needs **Work on the shop floor** (`manufacturing:shop-floor`),
or **Manage manufacturing orders**, beside Use Manufacturing. With Use Manufacturing alone
you can look at the queues and the work orders, and a work order says *You can look, but
not start or finish work*: there is no Start, Pause, Finish, Register production, Report
scrap, Block or Unblock.

A work order goes through these states:

| State | What it means |
| --- | --- |
| **Pending** | An operation it depends on is not finished yet, or has not passed on its send-ahead quantity |
| **Ready** | It can be started |
| **In progress** | Someone is working on it |
| **Paused** | It was started, and nobody is working on it now |
| **Done** | Finished |
| **Cancelled** | Its order was closed or cancelled before it was done |

### Choose your work center

1. Open **Shop floor**.
2. Under **Choose a work center**, pick the one you work at. A blocked work center is
   marked **Blocked**.

The tablet remembers the work center it was last used at, and next time offers it first,
as **Continue at** with its code and name, above the others. The choice is kept in the
browser only: on another device, or after the browser's data is cleared, you simply pick
again.

The work center page shows its **Queue**: the work orders that are ready, in progress or
paused, of orders still open — first by planned start, then the urgent orders, then by
due date. Each shows the order's number, the product, the quantity still to make, the due
date, whether its components are reserved, and who is running it. The queue updates by
itself as others start and finish work. **My work** is at the top of every shop-floor
screen.

### Start, pause and finish a work order

1. Choose the work order in the **Queue**. The work order shows the order, the quantity
   still to make, the due date, **Logged / expected** minutes, the operation's
   **Instructions** and the **Components at this operation**. Under **People**,
   **Running now** names who is working on it, and **Others here** who else has it open
   right now.
2. Choose **Start**. **Your time** runs from that moment. The first start on an order moves
   it to **In progress**.
3. Choose **Pause** for a break, or when you leave the job for now. Your time stops; when
   nobody else is working on it, the work order is **Paused**. Choose **Start** to go on.
4. Choose **Finish** when the operation is done for the whole order, and confirm
   **Finish this work order?**. Finishing ends the time of everyone still working on it,
   marks it **Done**, and makes **Ready** every operation that depended on it once all it
   depends on are done — two operations that depended on it both become ready together, and
   one that waits for two becomes ready only when both are done. Finish is not offered on a
   pending work order. Finishing one that someone else has just finished
   is not an error.

Several operators can work on one work order at once, each with their own time.

### Start a work order out of sequence

Operations run in the order their bill of materials gives: a work order is **Pending**
until every operation it depends on is done. Only the operations it depends on directly
count: when you start one out of sequence and finish it, the operation after it becomes
**Ready** even if an operation before the one you started is still open.
When you choose **Start** on it, you are told *An earlier operation is not finished*. When
the work overlaps in practice, choose **Start anyway**: the work starts as usual, and the
time log is marked **Out of order**, so the supervisor can see it. **Cancel** leaves it
pending.

### Group work

When the work center is an active member of a work center group, its page also shows
**Group work**: the group's work orders that are ready and that no member has taken yet — none is
planned and no time is logged on them. The same work orders show on every member's page.
Choose **Start here** to take one: it moves to this work center, with this work center's
setup, efficiency and rates, and its time starts. From then on it is shown only in this
work center's **Queue**. When another member took it a moment before you, you are told the
work order was taken at another work center, and the list is read again. When the group is
archived, its work orders stay at the member they have and show in that work center's
**Queue**.

### Pass a quantity on

When an operation has a **send-ahead quantity**, the operations after it may start before
it is finished, once it has passed that many good units on. On a work order that is not the
last, choose **Register quantity**, enter the good units you are passing on, and choose
**Register quantity**. Nothing is posted to stock — only the last operation's production
does that. The work order shows **Passed on**, such as *4 pcs of 10 pcs passed on before
the next may start*, and the next operations become **Ready** as soon as the quantity is
reached. The work order's first-piece checks must be recorded first. All the quantities
registered on a work order together cannot exceed the order's quantity. Sending the same
registration twice registers it once.

### Switch to another work order

You run one work order at a time, across every work center. When you open another work
order while your time is running, it warns *You are running another work order*, with
**Open the other work order**. Choosing **Start** anyway ends your time on the other one —
you need not pause it first — and you are told *Your other log was stopped*. The work
order you left keeps the others who work on it; when you were the last, it is **Paused**.

### Register production at the last operation

On the order's last operation, **Register production** stands beside **Finish**. It opens
the form of the order page — the **Quantity** made, any **Output scrap**, and each
component's prefilled **Consumed** and **Scrap** (see
[Register a production](#register-a-production)), with the same warning when a figure is
more than 10 % off, and the lot, the serial numbers and the components' lots of
[Lots and serial numbers](#lots-and-serial-numbers). Choosing **Post** registers the
production and leaves the work order running, so you can register more as you go.

When this is the last of it, also tick **Also finish this operation**: the production is
registered **and** the work order finished, together — which stops the time of everyone
working on it. When the production is refused, for instance because the site does not
hold enough of a component, nothing is registered, the work order is not finished, and the
time keeps running. Sending it twice posts it only once.

Closing the order is still done on the order page, by someone with Manage manufacturing
orders.

### Record the checks

A work order with quality checks shows a **Checks** panel: the first-piece checks not yet
recorded at the top, then the rest in order. Each shows its kind, when it is due, its
instructions and, for a measure, its limits. Record each one with its large buttons:

- an instruction: **Done**;
- pass or fail: **Pass** or **Fail**;
- a measure: type the value in **Measured value** — the tablet offers its number pad,
  and the **±** button beside the field turns the value negative, as the pad has no
  minus — and choose **Record**. The result, **Pass** or **Fail**, is Vantigo's, judged
  against the limits;
- a photo: **Take photo** opens the camera; the picture is kept with the result. A large
  picture is made smaller on the tablet before it is sent — at most 2560 pixels on its
  longest side, as a JPEG. A JPEG or PNG of at most 10 MB; what the camera writes into a
  photo about where and when it was taken is removed before it is kept.

A **Note** is optional on every check. Recording a check again replaces its result; the
earlier one is kept, with who recorded it and when. Nothing can be recorded on a work
order that is done or cancelled.

What the checks stop:

- **Finish** is refused while a check of the work order, of either kind, has no result:
  *Checks are missing*, with the checks named.
- **Register production** — on the order page or at the last operation — is refused
  while any check of an earlier operation has no result, or a **first-piece** check of
  the last operation has none. The refusal names the checks. Reporting scrap only is
  not stopped.
- **Close** on the order page is refused, once something good has been produced, while
  any check of the order's work orders has no result.
- Starting is never stopped.

A **Fail** does not stop anything: it opens a deviation at once, named on the check —
**DEV-12**, say — for a supervisor to decide on. When the check says **Quarantine on
fail**, the deviation puts the order's output in quarantine. If the same check fails
again while that deviation is open, no second deviation is opened: the new reading is
added to the open one, and the check names it.

The order page's **Work orders** tab lists every check under **Check results**, with its
result, who recorded it and when, and the deviation a failure opened.

### Report a deviation

Choose **Report a deviation** on a work order, on the order page, on the **Deviations**
list or on a lot's page in Inventory. Give its **Title**, a **Description** if you like,
and tick **Put the output in quarantine** to hold what the order made — see
[Deviations](#deviations). Reporting needs the same permission as the shop floor; on a
lot, also Inventory's permissions to see locations and stock. A deviation of an order
that names a lot must name one the order made or used.

### Report scrap

Choose **Report scrap** on the work order when components are lost or finished units are
thrown away, at any operation. Give the scrap of each component and the **Output scrap**.
It is registered as a production with no good units: the components are taken out of
stock in Inventory and the order's figures count them — a component tracked in lots from
the lots that expire first. Sending it twice posts it only once.

### Block a stopped work center

When the machine breaks down, or work stops for another reason:

1. On the work center page, choose **Block**.
2. Choose the **Reason** — **Breakdown**, **Material missing**, **Setup**, **Waiting for
   operator**, **Quality** or **Other** — and, if you like, write a note. The note is
   yours: it is kept with your name, and goes when your user is erased.
3. Choose **Block**.

From that moment the time of everyone working at the work center is recorded as
downtime with that reason, the work center's own stopped time is counted, and nobody can
start a work order there: the work center and its work orders show **Blocked** with the
reason, since when and who blocked it, and Start is not offered. **Overview** counts it under **Blocked
work centers**. Blocking a work center that is already blocked changes nothing.

While it is blocked, **Pause** or **Finish** ends your downtime. When it runs again,
choose **Unblock**: the downtime ends, and the work orders stay **Paused** — choose
**Start** on yours to go on.

### My work

**My work** (`/manufacturing/shop-floor/mine`) shows under **Running now** the work order
you are running, with your timer and **Open the work order**, and under **Today** the work
orders you have logged time on today.

### Time left running

Time is not ended by itself at the end of the day. When your time has run for more than
**12 hours**, your timer shows *Running for more than 12 hours*: pause it if you have
stopped working, or ask a supervisor to [correct the time](#correct-a-time-log). A work
order also names the others whose time has run that long, and the order's time logs mark
such a log **Over 12 h**.

## The production schedule

The **Production schedule** (`/manufacturing/schedule`) shows when each work order is
planned to run, on its work center's calendar. Everyone with Use Manufacturing reads it;
planning, moving and removing a plan need **Manage manufacturing orders**.

### Plan an order

On a confirmed or in-progress order, choose **Plan**. Each work order that is not done is
placed on its work center's calendar after the work orders it depends on, in the first
free time long enough to hold it: the work center's open hours, less the time other work
orders already hold there. Operations that depend on the same one are planned side by side
on their own work centers, and one that waits for several starts after the last of them
finishes. After an operation with a **send-ahead quantity**, the next starts once the time
for that quantity has been worked — the setup and its cycles — and is held so that it does
not finish before its predecessor plus the time of its own last batch.

A work order of a **work center group** is tried at every active member that is not
blocked, and planned at the one where it finishes first; when two finish at the same
moment, the one with the higher efficiency, then the lower code, is chosen. Its figures and
expected minutes are then that member's. Once time is logged on it, it stays at its member. A work order that does not fit in one stretch is cut into
pieces around breaks, nights, closed days and other bookings. The order's **Planned**
dates then run from the first piece's start to the last piece's finish.

- Planning starts from the **Requested start** day, or from now if that has passed. When
  the order has no requested start, the default is worked out from the due date: the due
  date less the working days the expected time needs, at 7.5 hours a day, less one more
  day — and never before today. Without a due date it is today.
- A work order already in progress, or paused, is planned for the time it still needs —
  its expected minutes less the time already logged on it, counted once when several
  operators worked on it at the same time — from now. A done work order is never moved.
- Planning an order that has a plan already (**Plan again**) replaces its old plan.
- An order without work orders is given the dates from its due date less its bill of
  materials' lead time to the due date.

When the planned finish falls after the due date, the order page shows **Late by n days**,
and its bars on the board are red. When a work order finds no room on its work center's
calendar within 365 days, nothing is planned and you are told to free up capacity or
change the calendar.

**Unplan** removes the order's plan: its planned dates and those of its unfinished work
orders are cleared, and their time on the calendars is free again.

### Read the board

1. Open **Production schedule** and choose the **Site**. The board remembers the site in
   this browser.
2. Choose **Week** or **Two weeks** under **Days shown**, and move with **Earlier**,
   **Today** and **Later**.

The board has a row for each active work center at the site and a column for each day.
Each planned piece of a work order is a bar at its time, labelled with the order's number
and the product; an urgent order is marked with a flame. A bar is red when its order will
be finished after its due date. A day the work center is closed — a public holiday
(**Holiday**) or one of its closures (**Closed**) — is greyed, and so is the row of a
**Blocked** work center.

A work order that was finished before its planned time keeps its bars only up to the
moment it was finished: the time after it is free again, and no longer counts as planned.

The board's days are the business calendar's days. Under each day the **load** is the time
planned on the work center that day as a share of the hours it is open, such as
**Load 75 %**. Above 100 %, it is marked **Overloaded**. The board updates by itself when
someone plans or moves work at the site, blocks or unblocks a work center, adds or removes
a closure, or changes a work center's working pattern.

### Plan the unscheduled orders

**Unscheduled**, below the board, lists the site's confirmed and in-progress orders that
have no plan — none of their unfinished work orders holds time on the board — with their
due date, **Requested start** and **Expected time**. Choose **Plan**
on one, or **Plan all** to plan every one of them: the urgent ones first, then by due
date — those without one last. An order that finds no capacity within 365 days is left
unplanned and named, and keeps what it had; the others are planned around it.

### Move a work order

Drag a bar along its row to another day or time and let go. A work order cut into pieces
moves as a whole: dragging any of its bars moves the work order's start by as far as you
dragged it. Or press the bar, and in **Move … to…** choose the **Day** and **Time** and
**Move**; the dialog starts at the work order's planned start, the day and time are in
your own time zone, and it also offers to open the order.

The work order takes the first free time from the moment you chose — from now, if that
has passed. The work orders that depend on it, directly or further on, follow it only when
they would otherwise start too early — before it is finished, or before its send-ahead
quantity is through. One that moves goes around those that stay, so two never share a
work center at once; work orders of other orders never move.

A work order of a work center group whose member is not fixed yet can also be dragged up or
down onto another member's row, or moved there with **Work center** in **Move … to…**: it
then runs at that member, with its figures. Any other row is refused, and so is a member
that is **blocked** — its row is not offered. Once time is logged on the work order, or
when its group is archived, it stays at its member, and a move to another row is refused.

## The cost of an order

Only those with **See manufacturing costs** see what an order costs. For them the order
page shows a **Cost** panel with, for each part — **Components**, **Machine**, **Labour**
and **Total** — the **Estimate**, the **Actual**, the **Deviation** and the
**Deviation %**.

- The **estimate** is made when the order is confirmed: the components' required
  quantities at their standard cost, and the work orders' expected hours at the work
  center's machine rate and at its labour rate, for one operator. A draft has none yet.
  For an operation that targets a group, the estimate uses the member the work order was
  first given, the group's first active one by code.
- The **actual** of a group's work order uses the rates of the member it was done at.
- The **actual** runs while the order is open (**Running**): what was consumed and
  scrapped at the same standard cost; the machine for the time each work order was
  worked, counted once however many operators were on it; and labour for every
  operator's productive time. It is fixed when the order is closed (**Fixed at close**),
  together with the **Cost per unit**: the actual total divided by the good units made.
- **Downtime by reason** lists the time the work centers stood still. It is never
  charged.

**By-products.** When the order makes by-products, **Split over the by-products** shows
each one's **Cost share** of the estimate and of the actual, and its cost per unit once
closed; **The product** carries the rest, and the order's **Cost per unit** is that rest
divided by the good units made. A by-product that was not produced has no cost per unit
— it says **Not produced**, with the part it would have had — and its share stays with
the product. Each by-product's part is rounded to øre once and the product carries the
rest, so the parts add up to the total. The **Production cost** report lists the
by-products of the orders closed in the period; its CSV has them in a section of their
own below the orders.

When a component had no standard cost, or a work center no rate, it is counted as 0 and
the panel says the estimate or the actual is incomplete. Amounts are rounded to two
decimals, so the parts may differ from the total by 0.01.

## Reports

On **Overview**, choose **Reports**. Choose the period with **From** and **To** — the last
30 days unless you change them, at most 366 days — and, if you like, a **Site**. Each
report has **Download CSV**, a file that opens in a spreadsheet with Norwegian number
formatting.

- **Production cost** — the orders closed in the period, by product: each order's
  estimate, actual, deviation, deviation % and cost per unit, with each product's
  totals. A product's produced total and cost per unit count only the orders that have an
  actual cost. It is shown only to those with **See manufacturing costs**.
- **Work-center utilisation** — per work center and week: its **Capacity**, the time
  **Planned**, the time **Productive**, the **Downtime** by reason, and the
  **Utilisation**, productive time as a share of capacity. An archived work center is
  listed only for the weeks something was planned or logged at it.
- **Time deviation** — per operation of a bill of materials: the **Average expected** and
  the **Average actual** minutes over the last 10 work orders finished in the period, and
  the deviation in per cent. Use it to see which standard times need adjusting.

## Make to order

A product made to order is made for each sales order rather than to stock. Selling and
making then follow one another: when a sales order in [Orders](/en/user/orders/) is
confirmed, Manufacturing makes the manufacturing order for it.

### Make a product to order

Open the product's **default** bill of materials, turn on **Make to order** and choose
**Save**. Only the default bill of materials counts: an alternative one marked Make to
order is not used for sales orders.

### Orders made for a sales order

When a sales order is confirmed, within a minute Manufacturing makes one **confirmed**
manufacturing order for each of its goods lines whose product is made to order:

- for the line's open quantity, due on the sales order's delivery date;
- at the site of the bill of materials' work centers. A bill of materials with no
  operations is made at the sales order's site; when the sales order names none either,
  no order is made for that line;
- with origin **Sales order** and the sales order's number, such as *SO-1042*.

Its components are reserved as when you confirm an order yourself; a short component
never stops it, and the order page names what is missing. A line that already has a
manufacturing order gets no second one. A quantity raised on the sales order later is not
followed: the sales order's line shows what is made for it, and you make the rest by hand.

### Make an order for a sales order line yourself

On the sales order's page in Orders, a goods line whose product has a bill of materials
offers **Create manufacturing order**. It opens **New manufacturing order** here, filled
in from the line — the product, the open quantity and the delivery date as the due date —
and says *Made for SO-1042*. Change what you need and choose **Create manufacturing
order**; then confirm it as any other. A line has at most one manufacturing order at a
time, its backorders aside: once one is made, the line shows it instead. Cancelling the
order frees the line.

### When the sales order is cancelled

A cancelled sales order does **not** cancel the manufacturing orders made for it: the
goods may already be half made. Each is marked **Sales order cancelled**, on the list and
on its page, and its history says so. Decide whether to finish it, close it or cancel it.
A backorder of such an order is marked too; its sub-assembly orders are not — look at the
parent. A sales order cancelled before its manufacturing orders were made gets none.

### When the order is done

When you close an order made for a sales order line, the sales order's line says the goods
are produced and ready to reserve, and its **Reserve now** reserves them — when they were
made at the sales order's site. Made at another site, the goods must be moved there first;
the line says so.

### Create sub-assembly orders

A component that is itself made — it has a default bill of materials — can be made for an
order. On a confirmed or in-progress order whose made components miss something, choose
**Create sub-assembly orders**. The sub-assembly orders already open for the order are
listed under **Already being made**, with what each still has to make. Every component
missing more than they make is listed with the rest it misses, each checked; uncheck those
you will not make now and choose **Create sub-assembly orders**. Each becomes a confirmed
order:

- for what the component misses beyond the open sub-assembly orders, by its default bill
  of materials;
- with origin **Parent order**, linked to this order;
- due when this order is planned to start, or on its requested start when it is not
  planned.

The order page lists its **Sub-assembly orders**, and each of them links to its **Parent
order**. Nothing is made automatically. What a sub-assembly order makes is not reserved for
the parent by itself: choose **Reserve now** on the parent once it is in stock.

## Deviations

A **deviation** (**avvik**) records something that went wrong: a failed check, a fault
found at the station, a lot that is not what it should be. Each has a number, **DEV-1**
upwards, its title and description, and what it was reported from: an order, one of its
work orders, a check's result, or a lot.

### Quarantine

A deviation that **puts the output in quarantine** — reported with the box ticked, or
opened by a failed check marked **Quarantine on fail** — puts a quarantine hold on:

- every lot its order has made so far;
- every lot a production of the order makes, or adds to, while the deviation is open — so
  a first piece that fails before anything is made still holds the lot the order makes
  next;
- the lot it was reported from, for one reported on a lot's page.

A lot in quarantine is not taken by deliveries or productions until its holds are
released (see [Lots](/en/user/inventory/) in Inventory). The hold is the deviation's: the
lot page shows it with the deviation's number and a link, and cannot release it there.
Only products tracked in lots or serial numbers have lots to hold.

### Find a deviation

**Deviations** lists them newest first, open ones unless you change **Status**; filter by
the period they were opened in. A link from an order or a lot shows that order's or lot's
deviations. **Download CSV** gives every deviation the filters match as a file. A
deviation's page shows where it came from, its description, and the **Lots in
quarantine** — each linked to its page in Inventory, for those who may see stock.

### Close a deviation

Closing needs **Manage quality** (`manufacturing:quality-manage`).

1. Open the deviation.
2. Under **Close the deviation**, choose the **Disposition**:
   - **Use as is** or **Rework** — its holds are released when it closes;
   - **Scrap** or **Return** — its holds stay: what is left of the lot is not to be used.
3. Write the **Closing note**, and choose **Close the deviation**.

Closing releases only the deviation's own holds. A lot another deviation holds, or a
recall has blocked, stays held until that is released too.

After **Scrap** or **Return**, write off what is left with an
[adjustment](/en/user/inventory/) in Inventory, or return it, then choose **Release its
holds** on the deviation.

## Replenishment suggestions

**Suggestions** (under **Manufacturing orders** in the sidebar) tells you what to make, or
buy, before a product runs short. It works from the **reorder point**, the **maximum** and
the **multiple** set on the product's placement at a site in Inventory — see
[Set an item's primary location and reorder point](/en/user/inventory/#set-an-items-primary-location-and-reorder-point) — and only products with a reorder point there
are judged.

For each such product and site, Manufacturing works out the **projected** stock: what is
usable on hand (held and expired lots left out), less what is reserved, plus what is on
its way there by transfer, plus what open manufacturing orders — and the drafts made from
earlier suggestions — will still make of it, less what open orders and those drafts
still need of it as a component and have not reserved, less what open sales orders still
need of it and have not reserved. An order made to order for a sales order is left out,
and the sales order line it is made for counts only for what that order does not still
make: what the order has already produced is on hand, promised to the line.

When the projected stock is at or below the reorder point, the product is suggested:

- **Make** when it has a default active bill of materials, **Buy** when it has none.
- The **quantity** fills up to the maximum — or to one above the reorder point when there
  is no maximum — rounded up to a whole number of the multiple when one is set. It is
  always at least 1, as on Inventory's low-stock list, even for a product counted in
  decimals.
- **Need by** is today when what is on hand, less what is reserved, is already at or below
  the reorder point; otherwise today plus the bill of materials' lead time, in business
  days. A buy suggestion is needed today.
- The **reason** says it in figures: *Projected 6 is at or below the reorder point 20:
  usable on hand 30, reserved 25, incoming 2, in production 3, needed by production 0,
  needed by sales orders 4.* What sales orders need is only ever a sum: no customer or
  order is named.

The suggestions are made every night at 02:00, in the business's own time zone, and each
night's replace the last. **Run now** makes them at once (at most once a minute for
each site); the page shows when they were last made, reads them again until the run has
finished, and whenever a site's suggestions change. When Orders is not in use, **Sales orders not
counted** says that sales demand is left out.

### Make orders from suggestions

1. Open **Suggestions**. Each site has its own table, the make rows first.
2. Tick the rows to make. Change a **quantity** if you want another — more than 0, with
   your decimal separator.
3. Choose **Create draft MOs** to get drafts to look over, or **Create and confirm** to
   confirm them at once, reserving their components as **Confirm** does — a shortage
   does not stop it.

Each row becomes one manufacturing order, of origin **Suggestion**, at the suggestion's
site, due on its need-by date, from the product's default bill of materials. A row whose
order could not be made says why — among them a product that already has an order from an
earlier suggestion still a draft or open at the site, which is then listed with *An MO
from an earlier suggestion is open*. Creating orders needs **Manage manufacturing orders**.

**Dismiss** hides a suggestion until the next run, which suggests it again if it is still
needed. Buy rows cannot be made into orders — there is no purchasing in Vantigo yet — but
**Export buy suggestions** downloads them as a CSV file to order from.

### Automatic creation

On a product's default bill of materials, **Automatic replenishment** lists every active
site with two switches, both off by default:

- **Create automatically** — the nightly run creates the suggested manufacturing order
  itself, as a draft.
- **Confirm too** — and confirms it.

The nightly run never creates a second order while one from an earlier suggestion of the
product is still a draft or open at the site; the suggestion is then listed with *An MO
from an earlier suggestion is open*. Orders made this way are created by nobody in
particular, so the page warns before you turn it on. Setting it needs **Manage
manufacturing orders**.

## Deleting a product a bill of materials uses

Products refuses to delete a variant, or to turn its product into a service, while an
**active** bill of materials names it as its product or as a component. The refusal says
how many bills of materials name it. Archive those BoMs, or change their lines, first. An
archived BoM holds nothing back.

The same goes for a **manufacturing order** that is not done or cancelled and names the
variant as its product or as a component: the refusal counts it with the bills of
materials. Close or cancel the order first.

## Who sees what

| What | Use Manufacturing | Manage bills of materials and work centers | See manufacturing costs |
| --- | --- | --- | --- |
| The tile, **Overview**, **Bills of materials**, **Work centers**, a BoM, a work center and its closures, a BoM's **Overview** without costs | ✓ | – | – |
| **New work center**, **Save**, **Archive**, **Reactivate**, **Delete** on a work center; adding and deleting closures | ✓ | ✓ | – |
| **New bill of materials**, **Save**, **Archive**, **Reactivate**, **Make default** on a BoM | ✓ | ✓ | – |
| The machine and labour rates on a work center, and setting them | ✓ | ✓ to set them | ✓ |
| The standard costs, extended values, operation costs and the rolled-up cost in the overview | ✓ | – | ✓ |
| An order's **Cost** panel, and the **Production cost** report | ✓ | – | ✓ |

Every row needs every permission it ticks. **See manufacturing costs** is sensitive: an
administrator gives it only to those who should see what things cost to make, and it
cannot be delegated.

| What | Use Manufacturing | Manage manufacturing orders | Work on the shop floor |
| --- | --- | --- | --- |
| **Manufacturing orders**, an order, its availability, its productions and its **History** | ✓ | – | – |
| **New manufacturing order**, **Save**, **Confirm**, **Reserve now**, **Add component**, changing a component, **Close**, **Cancel order** | ✓ | ✓ | – |
| **Split**, **Merge**, **Unbuild** | ✓ | ✓ | – |
| **Unbuilds**, an unbuild | ✓ | – | – |
| **Register production** | ✓ | either | either |
| **Shop floor**, a work center's **Queue**, a work order, **My work**, an order's **Work orders** and **Time logs** | ✓ | – | – |
| **Start**, **Start anyway**, **Pause**, **Finish**, **Report scrap**, **Block**, **Unblock** | ✓ | either | either |
| **Correct**, **Add missing log** | ✓ | ✓ | – |
| **Production schedule**, **Reports** (without production cost) | ✓ | – | – |
| **Plan**, **Plan again**, **Unplan**, **Plan all**, moving a work order, **Requested start** | ✓ | ✓ | – |
| **Create sub-assembly orders**, and **Create manufacturing order** on a sales order's line | ✓ | ✓ | – |
| **Deviations**, a deviation, an order's **Check results** | ✓ | – | – |
| Recording a check, **Take photo**, **Report a deviation** | ✓ | either | either |
| **Suggestions**, a BoM's **Automatic replenishment** | ✓ | – | – |
| **Create draft MOs**, **Create and confirm**, **Dismiss**, **Run now**, the automatic replenishment switches | ✓ | ✓ | – |

Registering a production, and every action on the shop floor, needs Use Manufacturing and
either of the other two. Closing a deviation, **Release its holds** and an unbuild's
**Release the quarantine** need **Manage quality** (`manufacturing:quality-manage`)
beside Use Manufacturing; editing the checks
of an operation is part of editing the bill of materials. The Assistant does not take shop-floor actions: an operator
clocks in themselves. The stock
figures in the overview and on an order are Manufacturing's own and need
no Inventory permission. An order made for a sales order names the sales order's
customer only to those who also hold Use Orders. The full table is under
[Permissions](/en/reference/manufacturing/#permissions) in the reference.
