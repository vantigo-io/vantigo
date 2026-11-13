---
title: Manufacturing
description: Turning Manufacturing on — the two modules it requires, the migration and the manufacturing schema, granting it to a least-privilege role — the six permissions and what each gives, the quality permission that closes deviations, the sensitive cost permission and who should hold it, who manages manufacturing orders and who registers production, who works on the shop floor and who corrects time logs, who plans the schedule, the roles to build from them; the business time zone the work centers' calendars run in; setting up tablets on the floor — an account per operator, a current browser, keeping the screen awake, the work center each tablet remembers, the camera and the network; the storage the checks' photos take; the nightly replenishment job and automatic creation, off by default; how to verify it, and what can go wrong.
sidebar:
  order: 51
sources:
  - apps/server/internal/manufacturing/module.go
  - apps/server/internal/db/migrations/00156_manufacturing_baseline.sql
  - apps/server/internal/db/migrations/00157_manufacturing_orders.sql
  - apps/server/internal/db/migrations/00158_manufacturing_work_orders.sql
  - apps/server/internal/db/migrations/00159_manufacturing_schedule.sql
  - apps/server/internal/db/migrations/00160_manufacturing_mto.sql
  - apps/server/internal/db/migrations/00168_manufacturing_quality.sql
  - apps/server/internal/db/migrations/00171_manufacturing_replenishment.sql
  - apps/server/internal/manufacturing/replenishment.go
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
  if those who see costs should get a complete rolled-up cost, and a complete estimate
  and actual on each manufacturing order.
- Check the **business time zone** under **Workspace settings**
  ([guide](/en/user/workspace-administration/#workspace-settings)). A work center's
  weekly pattern is read as local times in that zone, so the production schedule plans
  in it, the days around a change to or from daylight saving time included.

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
   to the schema, `00158_manufacturing_work_orders.sql` the work orders and time logs
   of the shop floor, and `00159_manufacturing_schedule.sql` the production schedule's
   `work_order_slots` table and the orders' requested start, estimate and actual, and
   `00160_manufacturing_mto.sql` the make-to-order requests (`mto_requests`) and the
   orders' origin-cancelled flag. The default privileges under Database roles cover them; a role
   granted table by table instead needs the `GRANT` statements run again after each
   upgrade.

3. The **Manufacturing** tile appears for everyone with `manufacturing:access`.

The module has no settings of its own. It starts its background workers, and none
needs anything configured:

- `manufacturing-erasure`, which once a day blanks the free-text notes on time logs
  written by people identity has erased — a supervisor's correction or addition, or a
  block's note ([personal data](/en/reference/manufacturing/#personal-data));
- `manufacturing.mto-worker`, which every 15 seconds makes the manufacturing orders for
  the sales orders confirmed in Orders: one confirmed order per goods line whose product's
  default bill of materials is **Make to order**
  ([Make-to-order](/en/reference/manufacturing/#make-to-order-and-sub-assemblies)).
  It needs **Orders** enabled, and the outbox's dispatcher running in a worker process to
  hand it the confirmed orders; with Orders off it makes none, and everything else in
  Manufacturing works as before. Its requests that keep failing are left `failed` in
  `manufacturing.mto_requests` with the last error, after eight attempts.
- `manufacturing.replenishment`, the replenishment job: every night at **02:00 in the
  business time zone** (**Workspace settings**), and whenever someone presses **Run now**
  on **Suggestions**, it replaces each active site's suggestions of what to make or buy
  ([Replenishment](/en/reference/manufacturing/#replenishment)). It reads the reorder
  points, maximums and multiples set on placements in Inventory, and, with Orders
  enabled, the sales orders' unreserved demand — without Orders the page says *Sales
  orders not counted*. A run is leased for ten minutes and the lease is renewed before
  each site, so no other worker process takes it up while its worker is alive. A run
  whose worker stopped is taken up again once its lease has run out; the worker that
  lost it stops before its next site, and only the one holding the run records its end.
  A site that fails does not stop the others: the run is then `failed`, its `error`
  naming each failed site, and the other sites' suggestions are made. A site archived
  in Inventory has its open suggestions replaced by nothing on the next run. Runs are
  kept 90 days in `manufacturing.replenishment_runs`.

The migration `00171_manufacturing_replenishment.sql` adds the runs, the suggestions and
the rules of automatic creation to the schema.

## Give people access

Nobody but the Owner can use the app until a role grants its permissions, in **Workspace
administration** → **Roles and access** ([Roles](/en/user/workspace-administration/#roles)).
Every Manufacturing permission needs **Use Manufacturing** (`manufacturing:access`)
beside it.

| Permission | Key | Gives |
| --- | --- | --- |
| Use Manufacturing | `manufacturing:access` | The app, and reading work centers, their closures, bills of materials and their overview, manufacturing orders with their productions and history, the production schedule, and the utilisation and time-deviation reports |
| Manage bills of materials and work centers | `manufacturing:boms-manage` | Creating, editing, archiving, reactivating and deleting work centers and their closures; creating, editing, archiving and reactivating bills of materials and making one the default |
| Manage manufacturing orders | `manufacturing:orders-manage` | Creating, changing, confirming, reserving, closing and cancelling manufacturing orders; adding and changing their components; registering a production; everything on the shop floor; correcting a time log and adding a missing one; planning orders on the production schedule and moving their work orders |
| Work on the shop floor | `manufacturing:shop-floor` | On the shop floor: starting, pausing and finishing work orders, registering a production, reporting scrap, and blocking and unblocking a work center; registering a production on a manufacturing order |
| See manufacturing costs | `manufacturing:costs-view` | Every cost figure: a work center's machine and labour rates, and setting them; the standard costs, extended values, operation costs and the rolled-up cost in the overview; a manufacturing order's **Cost** panel; the **Production cost** report. Sensitive, and cannot be delegated |
| Manage quality | `manufacturing:quality-manage` | Closing a deviation with its disposition — which releases the quarantine holds it put on lots, unless it is closed as scrap or return — and releasing the holds of one closed so |

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

Recording a quality check and reporting a deviation are the shop floor's: Work on the shop
floor or Manage manufacturing orders. Editing the checks of an operation is editing the
bill of materials. **Manage quality** decides what happens to what a deviation holds:
give it to the quality manager or the production manager, since closing a deviation as
*use as is* lets the held lots be delivered again.

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
7. **Allow the camera.** A photo check opens the tablet's camera through the browser.
   The first time, the browser asks for leave to use it: allow it for Vantigo's address.
   Without a camera the operator can pick a picture saved on the tablet instead.

The timer counts on the tablet from when the work started, so a tablet that reconnects
shows the right time without the server sending it.

## Photos of the checks

A photo check keeps its picture in the installation's [object
storage](/en/admin/object-storage/) under the scope `manufacturing-quality`. Without
object storage configured, taking a photo is refused (*storage_not_configured*) and a
photo check cannot be recorded; every other check works.

Each picture is a JPEG or PNG of at most 10 MB, less what it says about where and when
it was taken. The shop floor makes a large picture smaller before it sends it — at most
2560 pixels on its longest side, as a JPEG — so most photos are well under that. The
upload is the one Manufacturing request allowed past the platform's 1 MB body cap: a
reverse proxy in front of Vantigo must let a request of a little over 10 MB through to
`/api/v1/manufacturing/work-orders/*/photos`, or a large photo is refused there. A photo
is a quality record: it is kept as long as the work order, even when its result is
recorded again. Count on a megabyte or so per photo check per work order. A picture uploaded but never recorded on a
check — the operator took it and left — is removed after a day by the worker
`manufacturing.object-deletions`, which runs in the worker process with the others and
retries until the store confirms.

## Automatic replenishment

Automatic creation is **off** everywhere until someone turns it on, per product and site,
under **Automatic replenishment** on the product's default bill of materials (it needs
Manage manufacturing orders). When on, the nightly run creates the manufacturing order a
suggestion asks for itself — as a draft, or confirmed too with **Confirm too** — with
nobody looking at it first. Turn it on only for products whose demand is steady, and
give Manage manufacturing orders to those who should decide it.

The run never creates a second order for a product while one from an earlier suggestion
is still a draft or open at the site — not even when two runs, or a run and a person,
act at once: the check is made with the product locked. To see what it did, read the newest runs as the
database owner:

```sql
SELECT kind, site_id, status, requested_at, finished_at, suggestions, orders_created, sales_counted, error
FROM manufacturing.replenishment_runs
ORDER BY requested_at DESC
LIMIT 10;
```

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
10. Confirm a third order and choose **Plan** on it. Open **Production schedule** and
    choose the site: the order's work orders are bars on the work center's row. With See
    manufacturing costs, the order shows its **Cost** panel, and **Overview** →
    **Reports** offers **Production cost**.
11. With Orders enabled, mark the product's default bill of materials **Make to order**,
    then confirm a sales order with it in Orders. Within a minute **Manufacturing orders**
    lists a confirmed order with origin **Sales order** and the sales order's number, and
    the sales order's line shows it.
12. In Inventory, give the product a reorder point above what is on hand at the site.
    Open **Manufacturing** → **Suggestions** and choose **Run now**: within a minute the
    product is listed as **Make** with its reason. Tick it and choose **Create draft MOs**:
    a draft with origin **Suggestion** is listed under **Manufacturing orders**.

### Look into make-to-order

Each confirmed sales order leaves one row in `manufacturing.mto_requests`. When a sales
order's line says **No manufacturing order made**, or shows nothing at all, read its row
as the database owner:

```sql
SELECT status, reasons, attempts, last_error, next_attempt_at, done_at
FROM manufacturing.mto_requests
WHERE sales_order_id = <the sales order's id>;
```

- `pending` — not handled yet; `attempts` and `last_error` say whether it has failed and
  is waiting for `next_attempt_at`. A row that stays pending means no worker process runs.
- `done` or `skipped` — handled; `reasons` lists each line left out with its code and why.
- `failed` — eight attempts failed; `last_error` says why. Fix the cause and create the
  manufacturing orders by hand from the sales order's lines.
- `cancelled` — the sales order was cancelled before its orders were made.
- No row — the confirm was not handled: Manufacturing was off when the sales order was
  confirmed, or the outbox's dispatcher is not running.

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
| `permission denied for table work_order_slots` in the log after the upgrade | The role was granted table by table, before `00159` | Run the `GRANT` statements again ([Database roles](/en/admin/installation/#database-roles)) |
| No **Plan**, **Unplan** or **Plan all**, and bars cannot be moved | The person lacks Manage manufacturing orders | Grant it to planners |
| Planning is refused: a work order does not fit within 365 days | The work center is closed, or fully booked, for the next year: no pattern, a long closure | Correct the weekly pattern or the closures, or unplan other orders |
| The schedule plans at the wrong hours | The business time zone is not the factory's | Set it under **Workspace settings** |
| **Suggestions** is empty | No placement in Inventory has a reorder point, nothing is low, or no run has been made | Set reorder points; choose **Run now** |
| **Suggestions** says *No run yet* after a night | No worker process runs | Start the worker process ([Installation](/en/admin/installation/)) |
| **Suggestions** says *Sales orders not counted* | Orders is not enabled | Enable Orders, or accept that sales demand is left out |
| A run in `replenishment_runs` is `failed` | Its `error` names each site that failed and why — Inventory not answering, or a site with more than 5000 placements with a reorder point; the other sites' suggestions were made | Fix the cause; the next run makes the suggestions again |
| A run in `replenishment_runs` stays `running` | Its worker stopped, or lost the run to another worker after its lease ran out | Nothing: it is taken up again once its lease has run out (ten minutes) |
| Automatic creation made no order | An order from an earlier suggestion of the product is still a draft or open, or the order was refused (logged by `manufacturing.replenishment`) | Finish or cancel the earlier order; read the worker's log |
| `permission denied for table suggestions` in the log after the upgrade | The role was granted table by table, before `00171` | Run the `GRANT` statements again ([Database roles](/en/admin/installation/#database-roles)) |
| No **Cost** panel on an order, no **Production cost** report | The person lacks See manufacturing costs | Grant it to those who should see costs |
| A production is refused for stock | The site does not hold enough of a component, or the site is archived in Inventory | Receive or move the stock, reactivate the site, or correct with an Inventory adjustment |
| A confirmed sales order gets no manufacturing order | The product's default bill of materials is not **Make to order**, Orders or the worker process is not running, the line already has an order, or the bill of materials has no operations and the sales order names no site | Mark the bill of materials, start the workers, give the sales order a site; the reason of each line left out is in `manufacturing.mto_requests.reasons` |
| `permission denied for table mto_requests` in the log after the upgrade | The role was granted table by table, before `00160` | Run the `GRANT` statements again ([Database roles](/en/admin/installation/#database-roles)) |
