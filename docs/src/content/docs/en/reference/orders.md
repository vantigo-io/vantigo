---
title: "Orders module"
description: "The quotes and sales orders made for customers: the module, its schema, its contract, its nine permissions and the modules it requires."
sources:
  - apps/server/internal/orders
  - apps/orders/frontend
  - openapi/orders.yaml
  - apps/server/internal/openapi/specs/orders.yaml
  - apps/server/internal/db/migrations/00119_orders_baseline.sql
  - apps/host/frontend/src/routes/orders
---
The Orders module keeps the quotes and the sales orders the business makes for its
customers. In Norwegian the app is **Ordre**. The design is
[`2026-11-05-orders-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-05-orders-design.md).

## The module

The module is `orders`, its schema `orders` (migration `00119_orders_baseline.sql`), its
API `/api/v1/orders` from `openapi/orders.yaml` and its frontend `@vantigo/orders-ui`.
It reads the customer through `contracts.CustomerDirectory`, the catalog through
`contracts.ProductCatalog` and the VAT codes and the seller through `contracts.SalesTax`,
so `MODULES` refuses `orders` without `customers`, `products` or `invoices`, and the
module refuses to mount without any of the three contracts.

## Permissions

| Key | Covers | Sensitive | Delegable |
| --- | --- | --- | --- |
| `orders:access` | See the app, read quotes, orders, subscriptions and deliveries | no | yes |
| `orders:quotes-manage` | Create, edit, send, revise, accept, decline and cancel quotes | no | yes |
| `orders:sales-manage` | Create and edit orders, hold, cancel, mark delivered | no | yes |
| `orders:sales-confirm` | Confirm, lock, unlock | no | yes |
| `orders:subscriptions-manage` | Subscriptions | no | yes |
| `orders:deliveries-manage` | Deliveries and shipments | no | yes |
| `orders:carriers-manage` | Carrier accounts and their keys | yes | no |
| `orders:analysis-view` | The analysis views | no | yes |
| `orders:settings` | Settings, series, templates and warnings | no | no |

Every operation asks for `orders:access` beside its own key. The operations are declared
in the contract and answer `501` until each is built.
