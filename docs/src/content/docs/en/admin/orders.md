---
title: Orders
description: Turning Orders on — the modules it requires, mapping Products' tax categories to VAT codes, the nine permissions and the roles to build from them — and keeping its settings — the number series, quote validity, payment terms, the send channel, the confirmation on confirm the mail texts and a subscription's final period — its quote templates and its sales warnings, who sees the sales analysis, who keeps subscriptions, and who invoices orders and subscriptions; how to verify it, and what can go wrong.
sidebar:
  order: 48
sources:
  - apps/server/internal/orders/module.go
  - apps/server/internal/orders/settings.go
  - apps/server/internal/orders/settingsrow.go
  - apps/server/internal/orders/mailtext.go
  - apps/orders/frontend/src/pages/settings.tsx
  - apps/orders/frontend/src/pages/templates.tsx
  - apps/orders/frontend/src/components/warnings-card.tsx
  - apps/orders/frontend/src/pages/analysis.tsx
---

The Orders module (**Ordre** in Norwegian) is where the business makes quotes for its
customers and takes their orders. This page covers turning it on and keeping its settings;
how people use it is in [Orders](/en/user/orders/), and every rule in the
[reference](/en/reference/orders/).

## Before you start

- **Products, Customers and Invoices** must be enabled: an order's lines are the catalog's
  products, its documents are a customer's, and its VAT codes and the seller printed on
  every PDF are Invoices'.
- **Invoices' seller details** — the company's name, organisation number and address —
  are printed on every quote and order confirmation. Fill them in under Invoice settings
  first.
- **Communications** is optional. Without it, or without a channel to send from, quotes
  and confirmations are downloaded and handed over by other means.

## Enable the module

1. With `MODULES` unset every module is on, Orders included. With `MODULES` set, list
   `orders` beside `products`, `customers` and `invoices`, for example
   `MODULES=customers,products,invoices,orders` ([Configuration](/en/admin/configuration/)).
   Without one of the three the server refuses to start and names it — `orders requires
   invoices`.
2. Restart the server. The `orders` schema is migrated whatever `MODULES` says, so
   enabling it later needs no migration; a database role needs the schema granted
   ([Database roles](/en/admin/installation/#database-roles)).
3. The **Orders** tile appears for everyone with `orders:access`, and the customer page
   gets an **Orders** tab.

## Map Products' tax categories to VAT codes

Every product line takes the VAT code its product's tax category maps to in Invoices. A
quote with a line whose category maps to nothing cannot be sent, and an order with one
cannot be confirmed ("A product's tax category has no VAT code"). Map every tax category
you sell in under Invoice settings → **VAT codes** → **Products' tax categories**
([Products' tax categories](/en/user/invoices/#products-tax-categories)) before people
start quoting. A free-text line takes the standard VAT code with the highest rate unless
the person picks another.

## Give people access

Nobody but the Owner can use the app until a role grants its permissions, in **Workspace
administration** → **Roles and access** ([Roles](/en/user/workspace-administration/#roles)).
Every Orders permission needs **Use Orders** (`orders:access`) beside it.

| Permission | Key | Gives |
| --- | --- | --- |
| Use Orders | `orders:access` | The app, and reading quotes, orders, subscriptions and templates |
| Manage quotes | `orders:quotes-manage` | Making, sending, answering and copying quotes |
| Manage orders | `orders:sales-manage` | Making and changing orders, hold, cancel, register delivered, send the confirmation |
| Confirm orders | `orders:sales-confirm` | Confirm, lock and unlock; accept a quote straight into a confirmed order |
| Manage subscriptions | `orders:subscriptions-manage` | Making, activating, changing, pausing, resuming, ending and cancelling subscriptions; accepting a quote as a subscription |
| View sales analysis | `orders:analysis-view` | **Analysis**: the quote hit rate, the order intake and the backlog, with their CSV files |
| Manage Orders settings | `orders:settings` | The settings, the quote templates and the sales warnings, and overriding a block warning; cannot be delegated |

**Manage deliveries** and **Manage carriers** (sensitive, not delegable) are listed
already and do nothing yet: they belong to the deliveries that bring deliveries and
shipping.

Whoever keeps the customers' subscriptions — support agreements, hosting, licences — gets
**Manage subscriptions**; the **Subscriptions** list and each subscription's page are
open to everyone with **Use Orders**. A subscription's periods are invoiced as orders are,
by Invoices' **From orders** wizard and its billing runs of orders, with Invoices'
permissions.

**View sales analysis** shows every salesperson's figures and every customer's intake, so
give it to sales management rather than to every salesperson.

A typical set: salespeople get Use Orders, Manage quotes and Manage orders; whoever
approves orders adds Confirm orders; the person who runs sales administration adds Manage
Orders settings. Salespeople also need **View customers** in Customers to pick a customer,
**View contacts** and **View customer associations** to pick a contact, and **Use Invoices** to pick a VAT code for a
free-text line; picking a product needs no Products permission.

**Invoicing an order** is Invoices' work, not a permission of Orders: the order's
**Invoice** button and the **From orders** wizard need **Use Invoices** and **Create
invoices**, and issuing the draft **Issue invoices** — a subscription's periods the same.
Whoever invoices needs no Orders permission to do it; the order's **Invoices** card links to the invoices for those with
**Use Invoices**. **Release draft allocations** on an order needs **Manage orders**.

## Keep the settings

**Orders settings** (`orders:settings`) holds:

- **Number series** — **Quotes start at** and **Orders start at**, each showing the next
  number. A number is taken when a document is created, so a deleted draft leaves a gap,
  which is allowed: neither quotes nor orders are sales documents. A series can only
  start above the highest number already used; set the start before the first document if
  you continue a series from an earlier system.
- **Defaults** — **Quotes are valid for** (days, 30 unless changed; a template may say
  otherwise), **Payment terms** (days, 14 unless changed, used when the customer has none
  of its own) and **Combine orders on one invoice by default**, which has no effect yet:
  Invoices' **From orders** wizard starts at one invoice per order, and a billing run of
  orders combines as the run is told. **Final period when a subscription ends** is what a
  new subscription takes for the period its end falls in, when the end is not that
  period's last day: **Full period** (unless changed) bills the whole period, **Pro-rated
  to the end date** bills it by its days to the end. Each subscription keeps its own,
  which can be changed on it and when it is ended; changing the default changes no
  existing subscription.
- **Sending** — the **Send channel**: the Communications mailbox quotes and order
  confirmations are sent from, or **None — nothing is sent**. **Send the order
  confirmation when an order is confirmed** e-mails it by itself to the order's contact
  person, else the customer's invoice e-mail; an order with neither is not sent. The
  mail carries the stored confirmation of the version the order was confirmed at; if the
  order is changed again before the mail goes, the current version is sent instead, and
  the order's **Documents and mailings** shows which. An order cancelled before its
  confirmation goes gets none.
- **Mail texts** — a subject and a text per kind (**Quote**, **Order confirmation**) and
  language (Norwegian, English). Leave one empty to use the built-in text. A text may
  name `{number}`, `{seller}`, `{total}` and `{validUntil}`, which are filled in; the
  person's own message from the send dialog is added before the closing.

Choose **Save**. When someone else saved the settings since you opened them, you are asked
to reload.

## Keep the quote templates

**Quote templates** (`orders:settings`) holds ready-made quotes to start from: **New
template**, a **Name** (unique), **Valid for** in days (empty takes the setting), the intro
and closing texts, whether the discount is shown, and lines — products with a quantity, an
optional price (excluding VAT) and the optional flag, sections and notes. A template can be
archived (**Show archived** lists them) or deleted; quotes already made from it keep their
lines. A template line whose product is later discontinued or deleted is left out when the
template is used, and the person is told which.

## Keep the sales warnings

**Orders settings** → **Sales warnings** (`orders:settings`) lists every warning on a
customer or a product, with its level, text and who changed it last.

1. Choose **Add customer warning** and pick the customer, or **Add product warning** and
   search for the product by one of its variants. Picking a customer needs **View
   customers**.
2. Choose the level: **Information** is only shown on the customer's or product's quotes
   and orders; **Block** also stops sending, marking as sent, confirming and accepting a
   quote into a confirmed order.
3. Write the **Text** (at most 500 characters) and choose **Save the warning**.

**Edit** changes a warning or, with **Remove the warning**, takes it away. A warning can
also be set from a quote or an order (**Add customer warning**).

Only a holder of **Manage Orders settings** can override a block, and must give a reason,
which is kept on the order's change log or at the top of the quote's internal note. A
warning stays with the customer when customers are merged — the surviving customer's own
warning wins if both have one, except that a block on the merged-away customer turns the
survivor's information warning into a block with both texts — and is deleted when the
customer is erased. A moved warning is listed by the customer's number until it is next
saved.

A product warning outlives a deleted product: the list marks it **Product deleted**, and
**Remove** is all it offers.

## Verify

1. As a salesperson, open **Orders** → **Quotes** → **New quote**, pick a customer, add a
   catalog product and **Save**: the line has a price and the totals are shown.
2. **PDF** → **Preview the PDF** shows your company's details and the line.
3. With a send channel chosen, **Send** the quote to yourself; it arrives and the quote is
   **Sent**. Without one, **Mark as sent** works instead.
4. **Accept** it into an order and **Confirm** the order: it is **Confirmed**, and the
   customer's **Timeline** shows the quote sent, accepted and the order confirmed.
5. Set a **Block** warning on the customer and open a new draft order for it: the red
   banner shows, and **Confirm** is refused with "A warning blocks this". As a settings
   holder, **Override the warning** with a reason and confirm; the **Change log** shows
   **Warning overridden**.
6. With **View sales analysis**, **Analysis** shows the quote under **Quote hit rate** and
   the order under **Order intake**.
7. With **Create invoices** and **Issue invoices**, choose **Invoice** on the confirmed
   order, **Create the drafts** and issue the draft in Invoices: the order's **Invoices**
   card lists the invoice, and its invoicing status is **Invoiced**.

## What can go wrong

| Symptom | Cause | Fix |
| --- | --- | --- |
| The server refuses to start: `orders requires …` | `MODULES` lists `orders` without products, customers or invoices | Add the module named |
| "A product's tax category has no VAT code" on send or confirm | A tax category is not mapped in Invoices | Map it under **Products' tax categories** |
| "A line has no price in this currency" | The product has no list price in the document's currency | Give the product a price in Products, or type one on the line |
| **Send** is disabled: "Sending is off" | Communications is off, or no send channel is chosen or able to send | Turn Communications on and choose a channel; until then use **Mark as sent** |
| A send listed as **Failed** | The mail provider did not take the e-mail, or a queued confirmation failed eight times | Check the channel in Communications; send again from the order |
| A queued confirmation **Failed** at once | The send had no recipient, or the order was cancelled before it went | Nothing to fix for a cancelled order; otherwise fill in an e-mail and send the confirmation from the order |
| An automatic confirmation never sent | The order has no contact e-mail and the customer no invoice e-mail | Fill one in and send the confirmation from the order |
| "A series can only start above the highest number already used" | The new start is below the next number | Choose a start at or above the next number shown |
| The Templates and Settings entries are missing | The person lacks Manage Orders settings | Grant it |
| The Analysis entry is missing | The person lacks View sales analysis | Grant it beside Use Orders |
| **Invoice** is missing on a confirmed order | The person lacks Use Invoices or Create invoices, or Invoices is off | Grant both in Invoices |
| "Part of the order is on a draft invoice" when cancelling | A draft invoice holds part of the order | Issue or delete the draft in Invoices, or choose **Release draft allocations** on the order |
| An invoice's issue is refused: "Work on line … can no longer be invoiced" | The order's draft allocations were released on the order, or the order was put on hold | Delete the draft and invoice the order again |
| An order is greyed out in **Invoice from orders**: "in another currency than invoices are made in" | The order's currency is not the invoice settings' default currency (NOK) | Invoice it outside Vantigo, or change the order's currency while it is still a draft |
| "A warning blocks this", and no **Override the warning** | A block warning applies, and the person lacks Manage Orders settings | Ask a settings holder to override it, or change the warning |
