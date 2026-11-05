---
title: Shipping
description: Setting up the carrier accounts Orders books shipments with — Bring's Mybring user and API key, the financial role prices need, the customer numbers, the test mode and its test customer numbers, the sender, the default service and the default account per site — Verify, the freight product and the markup, the SHIPPING_CARRIERS switch and the tracking worker; how to verify it, and what can go wrong.
sidebar:
  order: 49
sources:
  - apps/server/internal/orders/carriers.go
  - apps/orders/frontend/src/pages/shipping-settings.tsx
  - apps/host/frontend/src/routes/workspace/shipping.tsx
  - apps/server/internal/shipping/bring/products.json
---

Orders ships a posted delivery with a carrier: it asks the carrier for prices, books the
shipment, prints the carrier's label and waybill and follows the tracking. This phase has
one carrier, **Bring**, booked directly through Bring's own API with your company's Mybring
user. This page is the administrator's side — the carrier accounts, the test mode and the
defaults; how people ship is in [Orders](/en/user/orders/#ship-a-delivery), and every rule
in [the reference](/en/reference/orders/#shipping).

## Before you start

- The **Orders** module is on, with **Inventory** keeping stock: a shipment ships a posted
  delivery ([Orders](/en/admin/orders/)).
- `SHIPPING_CARRIERS` allows Bring. It does unless an operator changed it
  ([Configuration](/en/admin/configuration/)).
- A **Mybring** account for the company, with a **customer number** for parcels and, if you
  send pallets or groupage, one for cargo. Bring gives the customer numbers with the
  agreement.
- A Mybring **user** for Vantigo — a person's login, or one made for the integration — with
  access to those customer numbers, and its **API key**: in Mybring, *Settings* → *API*.
  The key belongs to the user, not the company.
- For prices, that user needs the **financial role** on the customer numbers. Without it
  Bring answers list prices only, and Verify says so.
- The server reaches `api.bring.com` over https. Bring uses Let's Encrypt certificates;
  nothing is pinned.

## Add a carrier account

The page is Workspace admin › **Integrations** › **Shipping** (*Frakt*), for a holder of
**Manage carriers** (`orders:carriers-manage`), a sensitive permission that cannot be
delegated. Give it to whoever keeps the company's carrier agreements.

1. Choose **Add carrier account**.
2. **Name** it — *Bring*, or *Bring test* for a test account.
3. **Customer numbers**: the parcels customer number first, then, when you have one, the
   cargo customer number. Bring's pallets, groupage and part loads (5100, 5300, 5400) are
   booked on the second.
4. **Mybring user id** (the e-mail you sign in with) and **API key**. They are sealed with
   the installation's `APP_SECRET` when you save and are never shown again; to change them,
   type new ones.
5. **Test mode**: on for a test account ([Test mode](#test-mode)).
6. **Sender**: the name, address, phone and e-mail Bring prints as the sender. An empty
   field takes the seller's from Invoices' settings.
7. **Save**. Open the account again to choose its **Default service** — the product a new
   shipment and an estimate use, such as *5800 Parcel to pickup point* or *5000 Business
   parcel*.

Choose **Verify** on the account. Vantigo asks Bring for a price with the account's keys and
first customer number:

- *The carrier accepted the keys* — and the services the account can book. When it also
  says *Prices are list prices*, the user lacks the financial role: give it in Mybring and
  verify again.
- *The carrier refused the keys* — the user id or the key is wrong, or the key was renewed.
- *The carrier refused the request* — with Bring's words, typically a customer number the
  user has no access to.
- *The carrier did not answer* — try again; check the outbound connection to
  `api.bring.com`.

Several accounts may exist — one per site, or one for test. **Archive** one you no longer
use: its shipments keep naming it.

## Test mode

A test account books in Bring's test mode: every call carries Bring's test header, nothing
is collected and nothing is billed. Bring gives test customer numbers on request — **5**
(parcels in Norway), **6** (the Nordics) and **7** (cargo) — and from 12 October 2026 the
header is the only way to test on a real customer number.

**Bring still sends its notices to real phone numbers and e-mail addresses in test mode.**
Test with made-up contacts — the shipment's recipient phone and e-mail can be changed before
booking — and never with a real customer's.

## Choose the default account per site

Under **Default account per site**, choose an account for each site that fulfils orders and
**Save**; clear a site's choice to take its default away. A save changes only the sites on
the page, so two administrators saving at once do not undo each other. A site's new shipments and the shipping estimates of its orders use it; a quote's
estimate uses the default site's. A site without one uses the only active account when
there is exactly one; otherwise a shipment names its account.

## Set the freight product and the markup

In **Orders settings** › **Shipping**:

- **Freight product** — a Service product, for example *Frakt*, whose name and price a
  shipping line takes. Its VAT code follows the goods on the quote or order: theirs when they
  share one, else the one most of the goods' amount carries; the freight product's own VAT
  code serves only a document without goods. Without a freight product a shipping line is
  free text.
- **Markup on the carrier's estimate** — the percent **Estimate** adds to Bring's price.
- **E-mail the customer when a shipment is booked** — on by default: the shipping notice,
  with the order number, Bring, the consignment number and the tracking link, sent from the
  Orders send channel. Its text can be changed under **Mail texts** › **Shipping notice**.

## The tracking worker

`orders.shipment_tracking` asks Bring every 30 minutes for every shipment booked or in
transit in the last 60 days, records the events and moves the shipment — in transit,
delivered, an exception. A cancel Bring reports is shown as an exception, not acted on: a
person cancels the shipment in Vantigo. Each shipment is asked about within 10 seconds, and
one that hangs does not hold up the others; a cycle stops asking after 10 minutes, and the
shipments it did not reach come first in the next. It runs in the worker process with the
others and needs nothing of its own; any number of instances share it. Bring's push
notifications (Event Cast) are not used.

## Verify

1. Under Integrations › Shipping, **Verify** says the keys are accepted and prices
   available.
2. Make an order of a product with a weight, confirm it, post a delivery, choose **Ship** on
   the delivery's Shipments card, **Show prices**, then **Book shipment** with a test
   account and made-up contacts.
3. The shipment shows a consignment number, a **Track** link and a **Label** to print.
4. Within half an hour the shipment shows Bring's first tracking event.

## What can go wrong

| Symptom | Cause | Fix |
| --- | --- | --- |
| **Shipping** is missing under Integrations | Orders is off, or the person lacks Manage carriers | Turn Orders on, or grant `orders:carriers-manage` |
| "This carrier is not allowed on this installation" | `SHIPPING_CARRIERS` withholds `bring` | Ask the operator to allow it |
| Verify: *The carrier refused the keys* | The Mybring user id or API key is wrong, or the key was renewed | Type the user id and a new key, save, and verify again |
| Verify: *Prices are list prices* | The Mybring user lacks the financial role on the customer number | Give the user the role in Mybring |
| "The carrier account's keys cannot be read on this server" | `APP_SECRET` changed since the keys were saved | Type the keys again |
| "No carrier account to use" when shipping | No account is the site's default, and there is not exactly one active account | Choose the site's default account |
| The carrier refuses a booking, naming a postal code or an address | The order's delivery address is incomplete | Correct the delivery address on the order, make a new shipment, and book |
| A booking ends with "The carrier did not answer" | Bring did not answer in time; it may have booked | Wait two minutes and book again: Vantigo looks the shipment up at Bring first and books once. Until then the shipment cannot be changed |
| "The shipment is being booked right now" (`booking_in_progress`) | Someone booked it under two minutes ago, and Bring may still be booking | Wait a couple of minutes and look again; it books once |
| "The carrier does not show the earlier booking yet" (`booking_outcome_unknown`) | A booking got no answer under fifteen minutes ago, and Bring does not show it by reference yet | Look the shipment up in Mybring under the order's consignments; or wait fifteen minutes and book again — then Vantigo books anew if Bring still shows none |
| The shipment says "print the label in Mybring" | The booking was found again after a lost answer, and Bring gives no label link for it | Print the label from the consignment in Mybring |
| A test booking sent an SMS to a customer | Bring notifies real contacts in test mode | Use made-up contacts on test shipments |
| Shipments stay *Booked* | The tracking worker does not run, or the server cannot reach Bring | Check that the worker process runs and that `api.bring.com` is reachable |
| A cancelled booked shipment still shows its label | Bring has no cancel: the shipment is voided in Vantigo | Do not use the label; Bring bills only parcels handed in |
