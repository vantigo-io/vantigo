---
title: Orders
description: Turning Orders on — the modules it requires and its nine permissions — and, as they arrive, its settings, number series, send channel and quote templates.
sidebar:
  order: 48
sources:
  - apps/server/internal/orders/module.go
  - apps/server/internal/orders/settings.go
---

Orders requires the Products, Customers and Invoices modules: with `MODULES` set, list
`orders` beside `products`, `customers` and `invoices`, or the server refuses to start
and names the one missing. Nobody but the Owner can use the app until a role grants its
permissions; `orders:access` opens it.

The settings — the number series, the default validity of a quote, the payment terms, the
send channel and the mail texts — are being built, and this page describes them as they
arrive.
