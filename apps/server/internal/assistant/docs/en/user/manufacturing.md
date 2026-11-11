---
title: Manufacturing
description: The Manufacturing app — its sidebar and the Overview; setting up a work center — its code, site, capacity, time efficiency, setup and cleanup, cost rates, its weekly working pattern and its closures — and archiving, reactivating and deleting one; making a bill of materials for a product — its output quantity, its lines with scrap, its operations at work centers with their instructions, and which operation consumes a line — the default and the alternative bills of materials, archiving and reactivating them; reading a bill of materials' overview — the quantities with scrap, made and bought, what is available at a site, the expected minutes and, for those who see costs, the rolled-up cost; why Products refuses to delete a variant a bill of materials names; and who sees what.
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
and how long the work is expected to take.

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
| **Overview** | **Active work centers**, **Blocked work centers** and **Active bills of materials**: how many there are |
| **Bills of materials** | Every bill of materials, with its product, its code, its output quantity, whether it is the default, and its number of lines and operations |
| **Work centers** | Every work center, by code, with its site |

Everyone with Use Manufacturing reads all three. Creating and changing work centers and
bills of materials needs **Manage bills of materials and work centers**
(`manufacturing:boms-manage`) as well; without it the buttons that change something are
not shown. Every cost figure needs **See manufacturing costs** (`manufacturing:costs-view`)
— see [Who sees what](#who-sees-what).

When there is no bill of materials yet, **Overview** says so and offers **Go to bills of
materials**. Set up your work centers first: an operation of a BoM names one.

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
- **Delete** removes it for good, and is allowed only while no operation of any bill of
  materials names it. Otherwise you are told it is in use; archive it instead.

When someone else saved the work center after you opened it, your save is refused and you
are asked to reload, so that nobody overwrites another's change unseen.

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
   - **Make to order** — the product is made for each sales order rather than to stock.
   - **Note** — up to 2,000 characters.
4. Under **Operations**, choose **Add operation** for each step of the work, in the order
   they are done. Each has:
   - **Seq.**, the order it runs in;
   - **Operation**, its name, such as *Cut*, *Weld* or *Paint*;
   - **Work center**, among the active ones. All the operations of one BoM must be at
     work centers on the **same site**;
   - **Setup minutes**, optional: leave it empty to use the work center's;
   - **Minutes per cycle**, more than 0: the time one cycle of the work center takes;
   - **Instructions**, in Markdown, up to 20,000 characters: what the operator needs to
     know at the station.
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
6. Choose **Create bill of materials**. On a BoM that exists, the button is **Save**.

A BoM has at most 200 lines and 50 operations. A line can name an operation you added in
the same edit, before either is saved.

**Editing** a BoM saves its lines and operations as you see them, in one go. When you
remove an operation a line named, the line simply no longer names one. The edit is
refused when someone else saved the BoM after you opened it; reload and make your change
again.

### What a bill of materials may not do

- **Contain itself.** A component may not be the BoM's own product, nor be made — through
  its own default BoM, at any depth — from something that is. The refusal names the path,
  such as *Bicycle → Frame → Bicycle*, so you can see which BoM to change.
- **Be more than 10 levels deep**, counting each component made from a BoM of its own.
- **Use a service.** The product and every component must be goods. A product turned into
  a service, or deleted, since cannot be saved on a BoM; the refusal names the line.
- **Spread its operations over sites.** Every operation's work center must stand at the
  same site.

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
  would now contain itself.

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

## Deleting a product a bill of materials uses

Products refuses to delete a variant, or to turn its product into a service, while an
**active** bill of materials names it as its product or as a component. The refusal says
how many bills of materials name it. Archive those BoMs, or change their lines, first. An
archived BoM holds nothing back.

## Who sees what

| What | Use Manufacturing | Manage bills of materials and work centers | See manufacturing costs |
| --- | --- | --- | --- |
| The tile, **Overview**, **Bills of materials**, **Work centers**, a BoM, a work center and its closures, a BoM's **Overview** without costs | ✓ | – | – |
| **New work center**, **Save**, **Archive**, **Reactivate**, **Delete** on a work center; adding and deleting closures | ✓ | ✓ | – |
| **New bill of materials**, **Save**, **Archive**, **Reactivate**, **Make default** on a BoM | ✓ | ✓ | – |
| The machine and labour rates on a work center, and setting them | ✓ | ✓ to set them | ✓ |
| The standard costs, extended values, operation costs and the rolled-up cost in the overview | ✓ | – | ✓ |

Every row needs every permission it ticks. **See manufacturing costs** is sensitive: an
administrator gives it only to those who should see what things cost to make, and it
cannot be delegated. The stock figures in the overview are Manufacturing's own and need
no Inventory permission. The full table is under
[Permissions](/en/reference/manufacturing/#permissions) in the reference.
