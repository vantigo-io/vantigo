---
title: Energy
description: Metering points, meters, supply periods, consumption per customer and spot prices.
sidebar:
  order: 70
sources:
  - apps/energy/frontend
  - apps/host/frontend/src/routes/energy
---

The Energy app keeps the metering points your organisation supplies: where each one
is, which meter sits on it, which customer it is supplied to and for how long, and the
consumption read from it, and the spot prices that consumption is priced by. It has
two areas, **Metering points** and **Prices**, and it adds an **Energy** tab to every
customer in the Customers app.

The app is shown when the energy module is switched on and you hold
`energy:metering-points-view` and `energy:meters-view` for **Metering points**, or
`energy:prices-view` for **Prices**; each area is offered only to whoever holds its
permissions. Each part of a metering
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

Turn on **Without customer** to list only the metering points that have no customer
now: no active supply period that has started and not ended. A point whose only
supply period starts later is listed too. This is the list to work through after an
import that left points without a customer, see
[Import metering points](#import-metering-points).

## Create a metering point

Click **New metering point** on the list. Fill in:

- **GSRN**: exactly 18 digits. Two metering points cannot share a GSRN.
- **Meter number**: the meter installed on the point today. It is registered as the
  point's first meter, installed at the moment you save.
- **Street address**, **Postal code**, **City**, **Country code**: all required; the
  country code is two letters and defaults to `NO`.
- **Price area**: NO1 to NO5.
- **Grid area**: optional, at most 64 characters.
- **Connection status**: **New**, **Connected** or **Disconnected**; new points start
  as **New**.
- **Expected annual consumption (kWh)**, **Latitude**, **Longitude**: optional.
  Expected annual consumption must be less than 100 000 000 000 kWh; latitude must lie
  between -90 and 90, longitude between -180 and 180.

**Create metering point** saves it and confirms with *Metering point created*. The
dialog marks the fields that are missing or malformed; a GSRN that already exists is
refused with *Could not save metering point*. Creating needs
`energy:metering-points-manage` and `energy:meters-manage`.

## Import metering points

**Import** on the list brings in the metering points from your previous system's
export, one row per metering point. It is shown when you hold `energy:import` and
`energy:metering-points-manage`; what the import writes needs the same permissions as
writing it by hand (see the table at the end). Click **Import** to open **Import
metering points**.

The file needs a **GSRN** on every row; every row is matched by it. A new metering
point also needs its **Meter number**, **Street address**, **Postal code**, **City**
and **Price area**. To link a point to its customer, the file needs the customer's
**organisation number**, and optionally the date the supply starts.

1. **Upload the file.** Drop a CSV file on the page or choose one. It may be
   separated by semicolons, commas or tabs, in UTF-8 or Windows (ANSI) encoding, with
   the column names in the first row; at most 50 MB and 100 000 rows. **Recent
   imports** below lists the earlier imports with their state: open one to continue
   where it stood, or delete one that is not running. Deleting an import does not
   remove the metering points it wrote.
2. **Map columns.** Under **What to do with the rows**, choose **Create new metering
   points** (a GSRN that already exists is refused), **Update existing metering
   points** (a GSRN nobody has is refused) or **Create and update**. Then, for each
   column of the file, choose under **Imported as** the field it becomes, from the
   groups **Metering point**, **Address** and **Customer link**, or leave it as **Not
   imported**. Vantigo suggests the fields for the column names supplier exports use,
   such as *MÅLEPUNKTID*, *MÅLERNR*, *ANLEGGSADRESSE*, *PRISOMRÅDE*, *ORG. NR.* and
   *STARTDATO KUNDE*; when the file has both the customer's postal address and the
   installation's (*POSTNR* and *POSTNR ANLEGG*), the installation's is suggested.
   **Same value for every row** sets a field the file has no column for, for example
   one price area or one supply start date for every row. **Preview** shows the first
   rows as they will be read, with each problem marked on its cell. The mapping is
   saved as you change it. On an update only the mapped fields change; a blank cell
   in a mapped optional field clears it.
3. **Check file.** Every row is tried exactly as the import would, without saving
   anything. The result counts the rows that **Would be created**, **Would be
   updated**, **Would be skipped** and **Have errors**, and, when there are any,
   those that **Would switch customer** and **Would wait for a start date**. **Change
   mapping** takes you back to step 2.
4. **Import** saves the rows, after a confirmation that repeats how many metering
   points are created and updated. The import runs in the background: you can leave
   the page and come back. **Stop** stops it after the rows in progress; the rows
   already imported stay.

The modes you may not run are greyed out, and the **Customer link** group is only
offered when you hold `energy:supply-periods-manage`.

### How a row is linked to its customer

A metering point is linked to a customer by a supply period, and a supply period needs
a start date. So the import reads the customer and the date separately:

- **Customer's organisation number** names the customer. It must be a valid
  Norwegian organisation number held by exactly one customer. A number nobody holds,
  one that several customers hold, or a customer that is archived or merged into
  another, fails the row, and the metering point is not written.
- **Supply start date**, from a column or as one value for every row, is the day the
  supply period starts (at midnight UTC). Dates may be written `2026-01-01`,
  `01.01.2026` or `01/01/2026`, always with the day first. With a date, the supply
  period is written as the point is imported.
- **Without a start date**, the point is imported and matched to its customer, but
  waits to be attached: see [Attach the waiting points](#attach-the-waiting-points).
- **Without an organisation number** (the column not mapped, or the cell blank), the
  point is imported with no customer, and an existing supply period is left as it is.
  Find these points with **Without customer** on the list and give each one a
  customer with **Assign customer**.
- A point already supplied to that customer is left as it is, whatever the date says.

### When a metering point switches customer

When a point is supplied to another customer today and its row names a new customer
with a start date, importing it **switches** the customer: the current supply period
ends on the row's start date and the customer in the file takes over. The start date
must be later than the current period's start.

Since the file may be older than a switch made by hand, the check puts a red warning
at the top of the page: *12 metering points will switch customer. Each one's current
supply period ends on the row's start date and the customer in the file takes over.*,
with a table of the **GSRN**, the **Current customer**, the **New customer** and the
**Date**. When you click **Import**, the confirmation repeats the number, and its
**Import** button stays disabled until you tick *I understand that 12 metering points
will switch customer.* If the number has changed since the check, the import is
refused with the reason and the page reads the import again; check the file again
before importing.

### Attach the waiting points

When rows had a customer but no start date, the finished import shows **Attach to
customers**: how many metering points wait, and a table of them with their customer.
Choose the **Supply start date** and click **Attach**; every waiting point gets a
supply period for its customer from that date. When some of them are supplied to
another customer now, the same red warning and checkbox as above appear, and attaching
switches them on that date. Each point is attached, or keeps waiting with the reason
shown in the table, so you can attach again with another date until none are left. A
point someone attached by hand in the meantime is left as it is. Attaching needs
`energy:supply-periods-manage`.

### Failed and skipped rows

- **Failed** rows are listed by row, column and problem, for example a GSRN that is
  not 18 digits, a second row with the same GSRN, or a meter number on an update that
  differs from the current meter (use **Replace meter** for that). **Download failed
  rows** gives you those rows exactly as they were, with an error column in front;
  fix them and upload that file as a new import.
- **Skipped** rows are totals lines: a row with no GSRN and at most one other filled
  cell, such as the summed annual consumption many exports end with. They are counted
  and listed, never failed.

The finished import lists the metering points created and updated, and those that
switched customer with both customers, each as a link. Imports are removed
automatically after 30 days; the metering points and supply periods stay. The full
rules are in [Importing metering points](/en/reference/energy/#importing-metering-points).

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
  **Manual**) and **Source** (**Elhub**, **Manual** or **Import**).
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

## Import readings

To bring a metering point's history in from another system, click **Import readings**
in **Consumption**. It is shown when you hold `energy:import`,
`energy:consumption-manage` and `energy:consumption-view`. One file holds the readings
of this one metering point.

1. **Choose a CSV file** of at most 10 MB and 50 000 rows (more than five years of
   hourly values): the first row names the columns, the separator may be a semicolon,
   a comma or a tab, and the file may be UTF-8 or a Norwegian Excel's own encoding.
2. Say which column holds what. The columns are suggested from their names (*Fra*,
   *Fra og med*, *Start* or *From* for the start; *Til*, *Slutt*, *End* or *To* for the
   end; *kWh*, *Forbruk*, *Volum* or *Mengde* for the quantity; *Kvalitet* for the
   quality; *Målepunkt* or *GSRN* for the metering point), and you can change each one:
   - **Start** and **Quantity (kWh)** are needed.
   - **End**, or, when the file has one timestamp per line, a **Resolution**: **One
     hour (PT1H)** or **15 minutes (PT15M)**, and each reading ends that long after it
     starts.
   - **Quality** is optional; without it every reading is **Measured**. The file may
     say Measured, Estimated, Corrected or Manual, or *målt*, *estimert*, *stipulert*,
     *korrigert* or *manuell*, in any case; an empty cell is Measured.
   - **Metering point (GSRN)** is optional. When you map it, every row must name this
     metering point, so a file meant for another point is refused before anything is
     written.
3. Click **Check**. Nothing is saved; you see how many readings are **new**, how many
   are **replacing** one already on the metering point and how many are **unchanged**,
   the period the file covers, and, when rows have problems, the first 200 of them
   with their row (the first row under the header is row 1) and column.
4. When the check found no problem, click **Import**. It says what it will do — *Adds
   12 readings and replaces 3.* — and **Import** there writes them. The page's
   consumption is updated straight away.

How the file is read:

- **Times.** A date and time may be written as `2025-01-31 13:00` or
  `2025-01-31T13:00` (seconds optional), `31.01.2025 13:00` or `31/01/2025 13:00`.
  A time with an offset, such as `2025-01-31T13:00+01:00` or `…Z`, is that moment. A
  time without one is the metering point's local time: Norwegian time for a
  Norwegian price area.
- **Summer time.** On the spring night the clocks skip from 02:00 to 03:00, a time in
  between, such as 02:30, does not exist, and its row is refused. On the autumn night
  the clocks go back from 03:00 to 02:00, so the hour from 02:00 happens twice: the
  first row starting at a repeated time is the first (summer-time) occurrence, the
  next row starting at the same time the second (winter-time) one. Keep the rows in
  the order they happened. An end at a repeated time is the first moment of that name
  after the reading's start, so *02:00–03:00* twice is read as two consecutive hours.
- **Quantities** accept a decimal comma or point (`1,5` and `1.5`; a space as thousands
  separator is ignored), must be zero or greater, and are stored to three decimals.
- **Each row** follows the manual reading's rules: the end must be later than the
  start. Two rows of the file with the same start and end are an error on the second.

What the import writes:

- A row whose start and end equal a reading already on the metering point, with the
  same quantity and quality, is **unchanged**: nothing is written for it, so importing
  the same file twice changes nothing.
- A row with the same start and end but another quantity or quality **replaces** that
  reading, as a manual reading does; the old reading is kept as history.
- Any other row is **new**. A reading that only overlaps a row without having the same
  start and end is left as it is.
- Imported readings have the source **Import**.
- **All or nothing.** When any row has an error, nothing is imported; fix the file and
  check it again.

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

## Prices

**Prices** holds the day-ahead spot prices of each price area: the price per kWh for
every hour, or quarter of an hour, that consumption on a spot contract is priced by.
Prices come in two ways. A background job fetches each day's prices from a public price
feed, when your administrator has switched it on
([Background workers and scaling](/en/admin/installation/#background-workers-and-scaling));
and prices can be imported from a file, which is also how days older than a week, or
days the feed lacks, are filled in.

Open **Energy** → **Prices**. The page needs `energy:prices-view`. Prices are market
figures, not a customer's, so the page shows no customer data.

### Read the coverage

The first card, **Coverage**, has one row per price area: every area a metering point
with a non-cancelled supply period is in, and every area that already has prices. **No price areas
yet** means neither exists. Each row shows:

- **First** and **Last**: the start of the first price held and the end of the last;
- **Intervals**: how many prices are held;
- **Days without prices, last 30**: the days among the last 30, today included, in
  which no price starts — **Complete** when there are none;
- **Last fetch**: the day the background job asked the feed about most recently, and
  how it went. **Fetched**: the day's prices are stored. **Not yet published**: today's or
  tomorrow's prices are not out yet; the day-ahead auction publishes them around 13:00
  Norwegian time, and the job asks again every hour. **Missing**: the feed has no prices
  for that past day; the job asks again once a day, up to seven times in a row, after
  which you import the day from a file. **Failed**: the reason is shown under it, and the job tries again later,
  waiting longer each time, at most six hours. **Not fetched** means the job has never
  tried the area, as when it is switched off.

The job only fetches the last seven days and tomorrow. A day missing further back must
be imported.

### Look at an area's prices

The card **Spot prices** shows one area's prices as a stepped line in NOK/kWh, each
price drawn from its interval's start in Norwegian time and held until the next (a
day also marks each interval with a dot). It opens on today in the first area. Choose the **Price area**, then **Day** or
**Month** and the day or the month. A day has 24 hourly prices (23 or 25 on the nights
the clocks change), or 96 quarter-hours when prices come at that resolution. On the
autumn night the hour from 02:00 comes twice, and the chart labels each run with its
UTC offset, *02:00 +02* and *02:00 +01*. A period without prices says *No prices for
this period.*

### Import prices from a file

**Import prices**, at the top of the page, is shown when you also hold
`energy:prices-manage`. It opens **Import prices**:

1. **Choose a CSV file** of at most 10 MB and 50 000 rows: the first row names the
   columns, the separator may be a semicolon, a comma or a tab, and the file may be
   UTF-8 or a Norwegian Excel's own encoding. The columns are found by their names,
   whatever their case, spacing or punctuation:
   - the **price area**, needed: *Area*, *Price area*, *Prisområde*, *Elspotområde*,
     *Område* or *Sone*, holding the area, such as `NO1`;
   - the **start**, needed: *Start*, *From*, *Fra*, *Time start* or *Starttid*;
   - the **end**, optional: *End*, *To*, *Til*, *Time end* or *Sluttid*;
   - the **price**, needed: in NOK per kWh, *NOK per kWh*, *Price*, *Pris*, *kr/kWh*
     or *NOK/kWh*; or in øre per kWh, *øre/kWh* or *ore_per_kwh*, which is divided by
     100;
   - the **price in EUR** (*EUR per kWh*, *EUR/kWh*) and the **exchange rate** (*EXR*,
     *Exchange rate*, *Kurs*), optional and kept for the record.

   A file may hold several price areas. A file with two columns for the same thing —
   *Price* and *øre/kWh*, say — is refused, as is one missing a needed column.
2. The file is checked as soon as you choose it, and nothing is saved. The result,
   **Dry run** with the number of rows, shows for each area how many prices are
   **New**, how many are **Replaced** (a price already held, with other figures) and
   how many are **Unchanged**, and the period the file **Covers**; or, when rows have
   problems, the first 200 of them with their row (the first row under the header is
   row 1) and column, and nothing can be imported until the file is fixed.
3. When the check found no problem, click **Import** to save the prices. *Prices
   imported* confirms it, and the coverage and the chart are updated. When every price
   in the file is already held, the check says *Every price in the file is already
   stored.* and there is nothing to import. If the stored prices changed after the
   check so that a row now has a problem, nothing is imported: *The file has errors
   now. They are listed below.*, and the check runs again.

How the file is read:

- **Times** are written as for [Import readings](#import-readings): `2025-01-31 13:00`,
  `2025-01-31T13:00`, `31.01.2025 13:00` or `31/01/2025 13:00`. A time with an offset
  (`+01:00`, `Z`) is that moment; a time without one is Norwegian time, whatever the
  area. On the spring night a time the clocks skip is refused; on the autumn night an
  area's first row at a repeated time is the first occurrence and its next the second.
- **The area** may be written in any case: `no1` is `NO1`.
- **Prices** take a decimal comma or a decimal point (`0,8123` or `0.8123`, not both);
  a space as thousands separator is ignored, and a price may be negative. They are kept
  to six decimals. An exchange rate must be above zero; the price in EUR and the rate
  may be left blank.
- **With an end column**, each price lasts from its start to its end, at most an hour.
  The rows need not follow on from each other, so one file may fill several separate
  days; but each area's rows must be in time order and must not overlap.
- **Without an end column**, each price lasts until the next row of the same area
  starts, and an area's last row as long as the row before it (an hour when the area
  has only one row). Keep each area's rows in time order with no gaps: a row is refused
  when the next row of its area starts later than one interval after it (at most an
  hour).

What the import writes:

- A price with the same area, start and end as one already held, and the same figures,
  is **Unchanged**: importing the same file twice changes nothing.
- One with other figures **replaces** the price held; the old figure is not kept.
- A price that overlaps a held price of another length — a quarter of an hour inside a
  stored hour, say — is refused on its row: one area cannot hold prices at two
  resolutions for the same time.
- **All or nothing.** When any row has an error, nothing is imported; fix the file and
  choose it again.

The full rules are in [Spot prices](/en/reference/energy/#spot-prices).

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
| `energy:prices-view` | See **Prices**: the coverage and each area's prices |
| `energy:prices-manage` | Import prices from a file, with `energy:prices-view` |
| `energy:import` | Import metering points from a file, with `energy:metering-points-manage`; import readings, with `energy:consumption-manage` and `energy:consumption-view`; what an import writes needs the permissions above |
