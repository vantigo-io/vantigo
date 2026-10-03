---
title: Energy
description: Metering points, meters, supply periods and consumption per customer.
sidebar:
  order: 70
sources:
  - apps/energy/frontend
---

The Energy app keeps the metering points your organisation supplies: where each one
is, which meter sits on it, which customer it is supplied to and for how long, and the
consumption read from it. It has one area, **Metering points**, and it adds an
**Energy** tab to every customer in the Customers app.

The app is shown when the energy module is switched on and you hold
`energy:metering-points-view` and `energy:meters-view`. Each part of a metering
point's page needs its own permission as well; a part you lack the permission for is
simply shown empty. The permissions are listed at the end of this page.

## Find a metering point

Open **Energy** → **Metering points**. The list shows every metering point with its
GSRN, meter number, address, price area and connection status (**New**, **Connected**
or **Disconnected**), 25 per page, with the total in the heading.

Type in the search box (**Search by GSRN, meter number or address…**) to narrow the
list. The search matches the GSRN, the current meter's number, the street address and
the city. The search box in the top bar finds metering points too, under the heading
**Metering points**, as long as you hold `energy:metering-points-view`.

Click a row to open the metering point. The pencil at the end of the row opens the
edit dialog directly.

## Create a metering point

Click **New metering point** on the list. Fill in:

- **GSRN**: exactly 18 digits. Two metering points cannot share a GSRN.
- **Meter number**: the meter installed on the point today. It is registered as the
  point's first meter, installed at the moment you save.
- **Street address**, **Postal code**, **City**, **Country code**: all required; the
  country code is two letters and defaults to `NO`.
- **Price area**: NO1 to NO5.
- **Grid area**: optional.
- **Connection status**: **New**, **Connected** or **Disconnected**; new points start
  as **New**.
- **Expected annual consumption (kWh)**, **Latitude**, **Longitude**: optional.
  Latitude must lie between -90 and 90, longitude between -180 and 180.

**Create metering point** saves it and confirms with *Metering point created*. The
dialog marks the fields that are missing or malformed; a GSRN that already exists is
refused with *Could not save metering point*. Creating needs
`energy:metering-points-manage` and `energy:meters-manage`.

## Edit a metering point

Open the metering point and click **Edit metering point**, or click the pencil on its
row in the list. The dialog holds the same fields as when creating, except the meter
number: a meter is changed by replacing it, below. **Save changes** confirms with
*Metering point updated*. Changing the GSRN to one another metering point already has
is refused. Editing needs `energy:metering-points-manage`.

## Read a metering point's page

The page is headed by the GSRN and the address, and has four cards:

- **Metering point details**: meter number, price area, grid area, connection status
  and expected annual consumption.
- **Meter history**: every meter that has sat on the point, with **Installed** and
  **Removed** times. The current meter has no removed time.
- **Supply periods**: which customer the point has been supplied to, from when to
  when, with the status **Active**, **Ended** or **Cancelled**. An end that is not set
  reads **Open-ended**.
- **Consumption**: the readings for a date range, as a chart and a table.

Times on this page are shown in Norwegian market time (Europe/Oslo).

## Replace a meter

In **Meter history**, click **Replace meter**. Fill in the **New meter number** and
**Installed at**, which starts at the current time. The meter in place is marked
removed at that time and the new one becomes the current meter; the meter number in
the details card follows. The installation time must be later than the current
meter's own installation time, otherwise the dialog refuses it on that field. Needs
`energy:meters-manage`.

## Start a supply period for a customer

In **Supply periods**, the button reads **Assign customer** when the point has no
active period and **Switch customer** when it has one. Both open the same dialog:
choose the **Customer** (type to search) and the **Switch date**.

- With no active period, a new **Active** period starts on that date.
- With an active period, that period is ended on the switch date and a new one starts
  for the new customer on the same date. The switch date must be later than the
  active period's start, and the customer must be a different one; the dialog shows
  the reason on the field.

The dialog refuses when the point already has a non-cancelled period covering that
date, and tells you to end the existing period first. The period starts at midnight
UTC on the chosen day. Needs `energy:supply-periods-manage`.

A supply period can also be started from the customer's side, see
[the Energy tab](#the-customer-pages-energy-tab) below.

## End a supply period

In **Supply periods**, click **End period** on the active row. The period ends at the
current time, without a further prompt, and its status becomes **Ended**. Only an
active period has the button; a cancelled one cannot be ended. If the end could not be
saved you are told with *Could not end period*. Needs `energy:supply-periods-manage`.

## Look at consumption

In **Consumption**, pick **From** and **To** (the last month is preselected) and a
resolution: **Hour**, **Day** or **Month**. The chart sums the kWh per bucket.

- **Hour** lists every reading in the range, grouped by day, with **Start**, **End**,
  **Quantity (kWh)**, **Quality** (**Measured**, **Estimated**, **Corrected** or
  **Manual**) and **Source** (**Elhub** or **Manual**).
- **Day** and **Month** list one row per bucket with the summed **Quantity (kWh)**,
  the number of **Intervals** behind it, and **Measured** or **Contains estimated**
  for its quality.

A range without readings says *No readings found for the selected date range*.
Viewing consumption needs `energy:consumption-view`.

## Add a manual reading

Click **Add manual reading** in **Consumption**. Fill in **Start**, **End** and
**Quantity (kWh)**: the end must be later than the start and the quantity zero or
greater. The reading is stored with quality **Manual** and source **Manual**. A reading
with exactly the same start and end as an existing one replaces it in the lists and
sums; the old one is kept as history. A refusal is shown as *Could not add reading*.
Needs `energy:consumption-manage`.

## The customer page's Energy tab

Open a customer under **Customers** and choose the **Energy** tab. It is shown when the
energy module is on and you hold `energy:metering-points-view`; see
[Customers](/en/user/customers/) for the customer page itself.

At the top, four figures: **Metering points** supplied to this customer, their summed
**Expected annual consumption**, **Consumption last 12 months** across them, and
**Active supply periods**. Below, a table with one row per metering point the customer
has a non-cancelled supply period on: **Metering point ID**, **Installation address**,
**Price area**, **Expected annual consumption**, **Supply periods** (the latest period
and its status; hover to see them all) and **Status**. Click a row to open the
metering point. A customer with none is told *No metering points* — *Attach a
metering point to start tracking consumption.*

**Attach metering point** opens a dialog where you search the **Metering points** by
GSRN or meter number and choose a **Start** date; this starts an **Active** supply
period for the customer on that point. It is refused with *This metering point already
has an overlapping supply period* when the point is already supplied on that date, and
with *Could not attach metering point* otherwise. The button is not offered on a
customer that has been merged into another or anonymised. When two customers are
merged, the supply periods of the absorbed customer move to the surviving one.

## Permissions

| Permission | Lets you |
| --- | --- |
| `energy:metering-points-view` | See the list and the metering point's page, search metering points, see the Energy tab |
| `energy:metering-points-manage` | Create and edit metering points |
| `energy:meters-view` | See the meter history (needed with the above to see the app at all) |
| `energy:meters-manage` | Replace a meter and register the first meter when creating |
| `energy:supply-periods-view` | See supply periods, and a customer's metering points |
| `energy:supply-periods-manage` | Assign, switch, end and attach supply periods |
| `energy:consumption-view` | See consumption and the Energy tab's consumption figure |
| `energy:consumption-manage` | Add manual readings |
