---
title: Manufacturing
description: Turning Manufacturing on — the two modules it requires, the migration and the manufacturing schema, granting it to a least-privilege role — the five permissions and what each gives, the sensitive cost permission and who should hold it, who manages manufacturing orders and who registers production, who works on the shop floor and who corrects time logs, the roles to build from them; setting up tablets on the floor — an account per operator, a current browser, keeping the screen awake, the work center each tablet remembers and the network; how to verify it, and what can go wrong.
sidebar:
  order: 51
sources:
  - apps/server/internal/manufacturing/module.go
  - apps/server/internal/db/migrations/00156_manufacturing_baseline.sql
  - apps/server/internal/db/migrations/00157_manufacturing_orders.sql
  - apps/server/internal/db/migrations/00158_manufacturing_work_orders.sql
---

The Manufacturing module (**Produksjon** in Norwegian) describes how the business makes
its products: the work centers where the work is done and the bills of materials that say
what goes into each product, and the manufacturing orders that make them. This page covers turning it on and giving people access; how
people use it is in [Manufacturing](/en/user/manufacturing/), and every rule in the
[reference](/en/reference/manufacturing/).

## Before you start

- **Products** must be enabled: a bill of materials makes one of the catalog's goods from
  others, and the standard costs the overview rolls up are Products'.
- **Inventory** must be enabled: every work center stands at an Inventory site, and the
  overview shows what is available there. Set up at least one site in Inventory first
  ([Set up a site](/en/user/inventory/#set-up-a-site)).
- Give the products you make and the components you use a **standard cost** in Products
  if those who see costs should get a complete rolled-up cost.

## Enable the module

1. With `MODULES` unset every module is on, Manufacturing included. With `MODULES` set,
   list `manufacturing` beside `products` and `inventory`, for example
   `MODULES=products,inventory,manufacturing` ([Configuration](/en/admin/configuration/)).
   Without one of the two the server refuses to start and names it —
   `manufacturing requires inventory`.
2. Restart the server. The migration `00156_manufacturing_baseline.sql` creates the
   `manufacturing` schema, empty, whatever `MODULES` says, so enabling the module later
   needs no migration. A least-privilege database role needs the schema granted — add
   `manufacturing` to the three `GRANT` statements under
   [Database roles](/en/admin/installation/#database-roles):

   ```sql
   GRANT USAGE ON SCHEMA manufacturing TO "vantigo_app";
   GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA manufacturing TO "vantigo_app";
   GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA manufacturing TO "vantigo_app";
   ```

   The migration `00157_manufacturing_orders.sql` adds the manufacturing orders' tables
   to the schema, and `00158_manufacturing_work_orders.sql` the work orders and time logs
   of the shop floor. The default privileges under Database roles cover them; a role
   granted table by table instead needs the `GRANT` statements run again after each
   upgrade.

3. The **Manufacturing** tile appears for everyone with `manufacturing:access`.

The module has no settings of its own. It starts one background worker,
`manufacturing-erasure`, which once a day blanks the free-text note of the time logs of
people identity has erased ([personal data](/en/reference/manufacturing/#personal-data));
it needs nothing configured.

## Give people access

Nobody but the Owner can use the app until a role grants its permissions, in **Workspace
administration** → **Roles and access** ([Roles](/en/user/workspace-administration/#roles)).
Every Manufacturing permission needs **Use Manufacturing** (`manufacturing:access`)
beside it.

| Permission | Key | Gives |
| --- | --- | --- |
| Use Manufacturing | `manufacturing:access` | The app, and reading work centers, their closures, bills of materials and their overview, and manufacturing orders with their productions and history |
| Manage bills of materials and work centers | `manufacturing:boms-manage` | Creating, editing, archiving, reactivating and deleting work centers and their closures; creating, editing, archiving and reactivating bills of materials and making one the default |
| Manage manufacturing orders | `manufacturing:orders-manage` | Creating, changing, confirming, reserving, closing and cancelling manufacturing orders; adding and changing their components; registering a production; everything on the shop floor; correcting a time log and adding a missing one |
| Work on the shop floor | `manufacturing:shop-floor` | On the shop floor: starting, pausing and finishing work orders, registering a production, reporting scrap, and blocking and unblocking a work center; registering a production on a manufacturing order |
| See manufacturing costs | `manufacturing:costs-view` | Every cost figure: a work center's machine and labour rates, and setting them; the standard costs, extended values, operation costs and the rolled-up cost in the overview. Sensitive, and cannot be delegated |

**See manufacturing costs** is sensitive: it shows what each product costs to make and
what an hour of each machine and of labour costs. Give it to whoever prices and
calculates — production management, finance — rather than to everyone who keeps the
bills of materials. Without it a person still edits a work center; the rates stay as they
were, and are neither shown nor sent.

The stock figures in the overview are Manufacturing's own reads of Inventory, so they need
no Inventory permission. Picking a product or a component needs no Products permission
either.

Registering a production, and every action on the shop floor, needs either **Work on the
shop floor** or **Manage manufacturing orders** beside Use Manufacturing, so an operator
who reports what was made need not be able to create, confirm or cancel orders.
Correcting a time log, or adding a missing one, needs Manage manufacturing orders: an
operator cannot change their own recorded time. The Assistant takes no action on the shop
floor, whatever the person's role: an operator clocks in themselves.

A typical set: everyone in production gets Use Manufacturing; operators add Work on the
shop floor; planners and supervisors add Manage manufacturing orders; whoever keeps the
recipes and the machines adds Manage bills of materials and work centers; production
management adds See manufacturing costs.

## Tablets on the floor

The shop floor (**Manufacturing** → **Shop floor**) is built for a tablet at each work
center. Every action is recorded as the person signed in, so set the tablets up for
that.

1. **Give every operator their own account.** The operator is the signed-in user, and
   switching operator means switching account: signing out and in again. There is no
   shared tablet with a PIN per operator in this version. Do not let a shift share one
   account — the time logs, the productions and the order's history would all name that
   account.
2. **Grant the roles.** Give each operator Use Manufacturing (`manufacturing:access`) and
   Work on the shop floor (`manufacturing:shop-floor`), and nothing more unless they also
   plan. Supervisors who correct time need Manage manufacturing orders.
3. **Use a current browser** — an up-to-date Safari, Chrome, Edge or Firefox. The shop
   floor is a page of Vantigo and needs no app or plug-in.
4. **Keep the screen awake.** A tablet that locks itself stops showing the running timer
   and the queue. Set the screen timeout in the device's settings to the length of a
   shift, or run the browser in the device's kiosk mode (Guided Access on an iPad, screen
   pinning or a kiosk app on Android) so it stays on the shop floor.
5. **Choose the work center on each tablet.** Open **Shop floor** and pick the work
   center under **Choose a work center**. The tablet remembers it in the browser and
   offers it first next time, as **Continue at** with its code and name; clearing the
   browser's data, or a private window, forgets it, and the operator picks again.
6. **Check the network.** The queue and the work order view update live over the
   tablet's connection to the server, and a start, a pause or a finish is a request to it:
   on a tablet that is offline it is not recorded. Cover the floor with Wi-Fi where the
   work centers stand.

The timer counts on the tablet from when the work started, so a tablet that reconnects
shows the right time without the server sending it.

## Verify

1. As someone with Manage bills of materials and work centers, open **Manufacturing** →
   **Work centers** → **New work center**, give it a code, a name and an Inventory site,
   and choose **Create work center**. It is listed, open Monday to Friday 07:00–15:00.
2. Open **Bills of materials** → **New bill of materials**, pick a goods product, add one
   operation at the new work center and one component, and choose **Create bill of
   materials**. The BoM is listed as the product's **Default**.
3. Open its **Overview** and choose the site: the component is listed with what is
   available there, and the operation with its expected minutes.
4. With See manufacturing costs, the work center shows its rates and the overview its
   costs. Without it, neither shows.
5. As someone with Manage manufacturing orders, open **Manufacturing orders** → **New
   manufacturing order**, pick the product, a quantity and the site, and choose **Create
   manufacturing order**, then **Confirm**. The order is **Confirmed**, and either its
   component is reserved or the page names it as short.
6. Choose **Register production** and **Register**. A **Production** movement is listed
   under **Movements** in Inventory, linked to the order, and the order is **In
   progress**.
7. In Products, try to delete the component: it is refused, naming a bill of materials and
   the open order.
8. Confirm another order for the product. Sign in on a tablet as an operator with Use
   Manufacturing and Work on the shop floor, open **Shop floor** and choose the work
   center: the order's first work order is in the **Queue**. Open it and choose
   **Start**: the timer runs. Choose **Finish**: the work order leaves the queue.
9. As a supervisor, open the order's **Work orders** tab: the operator's time is under
   **Time logs**, and **Correct** is offered.

## What can go wrong

| Symptom | Cause | Fix |
| --- | --- | --- |
| The server refuses to start: `manufacturing requires …` | `MODULES` lists `manufacturing` without `products` or `inventory` | Add the module named |
| `permission denied for schema manufacturing` in the log | The database role was not granted the new schema | Add `manufacturing` to the `GRANT` statements ([Database roles](/en/admin/installation/#database-roles)) |
| The tile is missing | The person lacks Use Manufacturing | Grant it |
| No **New work center** or **New bill of materials** | The person lacks Manage bills of materials and work centers | Grant it beside Use Manufacturing |
| No **Site** to choose on a work center | Inventory has no active site | Set up a site in Inventory |
| No rates on a work center, no costs in the overview | The person lacks See manufacturing costs | Grant it to those who should see costs |
| Saving a rate is refused | The person lacks See manufacturing costs | Grant it, or have someone who holds it set the rates |
| The overview says **The cost is incomplete** | A bought component has no standard cost in Products | Give it one in Products |
| **Reactivate** on a work center is refused | Its site has been archived in Inventory | Reactivate the site, or use a work center at another site |
| Products refuses to delete a variant or make its product a service | An active bill of materials or an open manufacturing order names it | Archive the bill of materials, or change its lines; close or cancel the order |
| No **New manufacturing order**, **Confirm** or **Close** | The person lacks Manage manufacturing orders | Grant it beside Use Manufacturing |
| No **Register production** | The person has neither Work on the shop floor nor Manage manufacturing orders | Grant one of them |
| `permission denied for table orders` in the log after the upgrade | The role was granted table by table, before `00157` | Run the `GRANT` statements again ([Database roles](/en/admin/installation/#database-roles)) |
| No **Start**, **Pause** or **Finish** on the shop floor | The operator has neither Work on the shop floor nor Manage manufacturing orders | Grant Work on the shop floor |
| No **Start**, and the work order says the work center is **Blocked** | Someone blocked it | **Unblock** it when it runs again |
| No **Correct** on a time log | The person lacks Manage manufacturing orders | Grant it to supervisors |
| Correcting a time log is refused | The log is more than 60 days old, the order is done or cancelled, or the times overlap another of the person's logs | Correct within 60 days and before the order is closed; fix the other log first |
| The tablet never offers **Continue at** | The browser forgets its data: a private window, or the data is cleared | Use a normal browser window and keep its site data |
| The timer stops or the screen goes dark | The tablet locks itself | Lengthen the screen timeout, or use kiosk mode |
| `permission denied for table work_orders` in the log after the upgrade | The role was granted table by table, before `00158` | Run the `GRANT` statements again ([Database roles](/en/admin/installation/#database-roles)) |
| A production is refused for stock | The site does not hold enough of a component, or the site is archived in Inventory | Receive or move the stock, reactivate the site, or correct with an Inventory adjustment |
