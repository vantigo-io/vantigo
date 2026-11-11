---
title: Manufacturing
description: Turning Manufacturing on — the two modules it requires, the migration and the manufacturing schema, granting it to a least-privilege role — the five permissions and what each gives, the sensitive cost permission and who should hold it, who manages manufacturing orders and who registers production, the roles to build from them; how to verify it, and what can go wrong.
sidebar:
  order: 51
sources:
  - apps/server/internal/manufacturing/module.go
  - apps/server/internal/db/migrations/00156_manufacturing_baseline.sql
  - apps/server/internal/db/migrations/00157_manufacturing_orders.sql
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
   to the schema. The default privileges under Database roles cover them; a role granted
   table by table instead needs the `GRANT` statements run again after the upgrade.

3. The **Manufacturing** tile appears for everyone with `manufacturing:access`.

The module starts no background worker and has no settings of its own.

## Give people access

Nobody but the Owner can use the app until a role grants its permissions, in **Workspace
administration** → **Roles and access** ([Roles](/en/user/workspace-administration/#roles)).
Every Manufacturing permission needs **Use Manufacturing** (`manufacturing:access`)
beside it.

| Permission | Key | Gives |
| --- | --- | --- |
| Use Manufacturing | `manufacturing:access` | The app, and reading work centers, their closures, bills of materials and their overview, and manufacturing orders with their productions and history |
| Manage bills of materials and work centers | `manufacturing:boms-manage` | Creating, editing, archiving, reactivating and deleting work centers and their closures; creating, editing, archiving and reactivating bills of materials and making one the default |
| Manage manufacturing orders | `manufacturing:orders-manage` | Creating, changing, confirming, reserving, closing and cancelling manufacturing orders; adding and changing their components; registering a production |
| Work on the shop floor | `manufacturing:shop-floor` | Registering a production on a manufacturing order; the work orders and time logs come with the shop floor in a later delivery |
| See manufacturing costs | `manufacturing:costs-view` | Every cost figure: a work center's machine and labour rates, and setting them; the standard costs, extended values, operation costs and the rolled-up cost in the overview. Sensitive, and cannot be delegated |

**See manufacturing costs** is sensitive: it shows what each product costs to make and
what an hour of each machine and of labour costs. Give it to whoever prices and
calculates — production management, finance — rather than to everyone who keeps the
bills of materials. Without it a person still edits a work center; the rates stay as they
were, and are neither shown nor sent.

The stock figures in the overview are Manufacturing's own reads of Inventory, so they need
no Inventory permission. Picking a product or a component needs no Products permission
either.

Registering a production needs either **Work on the shop floor** or **Manage
manufacturing orders** beside Use Manufacturing, so an operator who reports what was made
need not be able to create, confirm or cancel orders.

A typical set: everyone in production gets Use Manufacturing; operators add Work on the
shop floor; planners and supervisors add Manage manufacturing orders; whoever keeps the
recipes and the machines adds Manage bills of materials and work centers; production
management adds See manufacturing costs.

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
| A production is refused for stock | The site does not hold enough of a component, or the site is archived in Inventory | Receive or move the stock, reactivate the site, or correct with an Inventory adjustment |
