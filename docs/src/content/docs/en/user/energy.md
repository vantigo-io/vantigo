---
title: Energy
description: Metering points, meters, supply periods, consumption per customer, spot prices, tariffs and settlements.
sidebar:
  order: 70
sources:
  - apps/energy/frontend
  - apps/host/frontend/src/routes/energy
---

The Energy app keeps the metering points your organisation supplies: where each one
is, which meter sits on it, which customer it is supplied to and for how long, and the
consumption read from it, the spot prices that consumption is priced by, the
power and grid tariffs it is billed on, and the settlements that price each billing
period. It has three areas, **Metering points**,
**Prices** and **Tariffs**, and it adds an **Energy** tab to every customer in the
Customers app.

The app is shown when the energy module is switched on and you hold
`energy:metering-points-view` and `energy:meters-view` for **Metering points**,
`energy:prices-view` for **Prices**, or `energy:tariffs-view` for **Tariffs**; each
area is offered only to whoever holds its permissions. Each part of a metering
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
   in a mapped optional field clears it. An import never changes a point's
   **Billing** facts: a new point has no grid tariff, is not marked for household
   use and has the ordinary electricity-tax class, and an updated point keeps its
   own (see [Edit a metering point](#edit-a-metering-point)).
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

A supply period the import writes, when the row is imported, switched or attached
later, has no power tariff and the cadence **Monthly**: the file carries no contract.
Give each period its tariff afterwards with **Change terms**, see
[Change a supply period's terms](#change-a-supply-periods-terms).

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

The dialog also holds the point's billing facts, which a settlement will need:

- **Grid tariff**: the grid tariff your organisation bills this point's grid services
  on, when it is the grid operator here; leave it empty when the grid company bills
  them itself, the usual case for a supplier. Only active grid tariffs are offered; a
  tariff archived since it was chosen stays on the point until you change it. The list
  is shown when you hold `energy:tariffs-view`; otherwise the tariff's name is shown
  and cannot be changed here.
- **Household use**: tick it when the point is supplied for household use. Vantigo
  cannot know this; you set it.
- **Electricity tax**: **Ordinary rate** for most points, **Reduced rate** or **Exempt**
  for a point that qualifies, which decides the electricity-tax rate the grid tariff
  applies.

The dialog keeps the three as they are when you edit only the address or another
field.

## Read a metering point's page

The page is headed by the GSRN and the address, and has six cards:

- **Metering point details**: meter number, price area, grid area, connection status
  and expected annual consumption.
- **Billing**: the **Grid tariff** (or none), **Household use**, the **Electricity
  tax** and the **VAT**. The VAT is not set by hand: it is
  *Exempt — household in Northern Norway (VAT Act § 6-6)* when the point is for household use, its
  country is `NO` and its postal code is from 8000 to 9999 — electricity for
  household use in Nordland, Troms and Finnmark is exempt from VAT — and *Standard*
  otherwise. It is shown so that a wrong postal code or a forgotten **Household use**
  is seen here, before an invoice is made.
- **Meter history**: every meter that has sat on the point, with **Installed** and
  **Removed** times. The current meter has no removed time.
- **Supply periods**: which customer the point has been supplied to, from when to
  when, on which **Power tariff** and **Billing**, with the status **Active**,
  **Ended** or **Cancelled**. An end that is not set reads **Open-ended**.
- **Settlements**: what each closed billing period comes to, see
  [below](#read-a-metering-points-settlements).
- **Consumption**: the readings for a date range, as a chart and a table.

Times on this page are shown in Norwegian market time (Europe/Oslo).

## Read a metering point's settlements

A settlement is what a customer owes for the metering point over one billing period:
the period's consumption priced by the spot prices and the tariffs, line by line. The
**Settlements** card, under **Supply periods**, lists them for the last 24 months,
newest period first. A billing period is the calendar month, quarter or year the
supply period's **Billing** says, cut to the supply period's start and end, and it is
shown once it has ended. Opening the page changes nothing: a period not yet invoiced is
calculated afresh each time, so its figures follow the readings, the prices and the
tariffs until it is invoiced.

Each row shows:

- **Period**: the first and the last day of the billing period.
- **Kind**: **Regular**, or **Correction** for the difference found after a period
  was invoiced (see below).
- **Status**: **Draft** until the period is invoiced, **Invoiced** once it is, and
  **Released** when a credit note took the whole invoice back; the period is then
  billed again as a new draft. **Estimated** beside it means some of the readings are
  estimates, which the invoice will say.
- **kWh** and **Amount (NOK)**.
- **Invoice**: the invoice's number, once invoiced; click it to open the invoice.
- **Show lines** opens the lines the amount is made of — each with its **Line** (the
  tariff component's name and its kind, such as *Spot energy*, *Monthly fee* or *Grid
  capacity*), **Quantity**, **Unit price** and **Amount (NOK)** — and **Hide lines**
  closes them.

When no billing period has ended yet the card says *No billing period has closed yet.*

### When a period cannot be priced

A period that cannot be priced is still listed, with an amount of zero and the reason
in red under its status, followed by the detail:

- **The billing period is still open**: the period has not ended yet. Wait for it to
  end.
- **No tariff applies**: the supply period has no **Power tariff**, or a tariff has no
  version for some day of the period. Give the supply period a tariff
  ([Change a supply period's terms](#change-a-supply-periods-terms)) or add the
  missing version to the tariff ([Add versions](#add-versions)).
- **No consumption in the period**: there are no readings at all for the period.
  Import the readings ([Import readings](#import-readings)) or add them by hand.
- **Readings are missing**: some of the period's readings are missing, a reading runs
  across the start or end of the period, or two readings overlap. Import the missing
  readings, or correct the ones that cross or overlap.
- **Spot prices are missing**: the price area has no spot price for part of the
  period. Import the prices ([Import prices from a file](#import-prices-from-a-file)),
  or wait for them to be fetched.
- **Spot prices are still preliminary**: a price the period needs is still
  preliminary. Wait until the prices are final
  ([Preliminary prices](#preliminary-prices)).

You do not have to do anything to the settlement itself: once the cause is fixed, the
next time the page is opened the period is priced. The last two only concern spot-price
contracts.

### Corrections

An invoiced period is never changed. When its readings, its prices or its tariffs
change after it was invoiced — up to twelve months after the period ended — the
difference is drafted as a **Correction** of that period the next time the customer's
consumption is settled for invoicing: its lines are the differences, and its amount may
be negative.

Consumption is not yet invoiced from Vantigo: invoicing it comes in a later version.
Until then the card shows what each period comes to.

The card is shown when you hold both `energy:supply-periods-view` and
`energy:consumption-view`. The rules behind the figures are in
[Settlements](/en/reference/energy/#settlements).

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
choose the **Customer** (type to search) and the **Switch date**, and the terms the
customer is supplied on:

- **Power tariff**: the customer's contract, one of the active power tariffs (see
  [Tariffs](#tariffs)). It may be left empty, but a period without one cannot be
  billed. The list is shown when you hold `energy:tariffs-view`.
- **Billing**: how often the period is billed, **Monthly** (the default),
  **Quarterly** or **Yearly**. A consumer may be billed at most quarterly; Vantigo
  accepts **Yearly** here for any customer, and the invoicing will refuse it for a
  private person.

- With no active period, a new **Active** period starts on that date.
- With an active period, that period is ended on the switch date and a new one starts
  for the new customer on the same date. The switch date must be later than the
  active period's start, and the customer must be a different one; the dialog shows
  the reason on the field. The new customer's period starts on the terms in the
  dialog; it does not take over the ended period's tariff or cadence.

The dialog refuses when the point already has a non-cancelled period covering that
date, and tells you to end the existing period first. The period starts at midnight
UTC on the chosen day. Needs `energy:supply-periods-manage`.

A supply period can also be started from the customer's side, see
[the Energy tab](#the-customer-pages-energy-tab) below.

## Change a supply period's terms

In **Supply periods**, click **Change terms** on the row. The dialog holds the
period's **Power tariff** and **Billing**, as when it was started; change them and
save. This is how a period started without a tariff, by an import or before tariffs
existed, gets one. An active or an ended period can be changed; a cancelled one
cannot. A tariff archived since the period was given it stays as it is, so you can
change the cadence alone, but another archived tariff cannot be chosen. Needs
`energy:supply-periods-manage`.

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
period for the customer on that point, with no power tariff and the cadence
**Monthly**; set them on the metering point's page with **Change terms**. It is refused with *This metering point already
has an overlapping supply period* when the point is already supplied on that date, and
with *Could not attach metering point* otherwise. The button is not offered on a
customer that has been merged into another or anonymised. When two customers are
merged, the supply periods of the absorbed customer move to the surviving one.

## Prices

**Prices** holds the day-ahead spot prices of each price area: the price per kWh for
every hour, or quarter of an hour, that consumption on a spot contract is priced by.
Prices come from one source your administrator has chosen
([Background workers and scaling](/en/admin/installation/#background-workers-and-scaling)):
the **spot-price service** our hosted environments run, which a background job copies
from, the years of history included; or a **public price feed**, which the job asks for
each day's prices; or neither. Prices can always be imported from a file as well, which
is also how days the source lacks are filled in.

Open **Energy** → **Prices**. The page needs `energy:prices-view`. Prices are market
figures, not a customer's, so the page shows no customer data.

### Read the coverage

The first card, **Coverage**, says in a badge beside its title where the prices come
from: **Spot-price service**, **Public feed**, or **Files only** when no source is set
and prices come by import alone. It has one row per price area: every area a metering
point with a non-cancelled supply period is in, every area that already has prices
and, with the spot-price service, every area the service follows — so the card fills
before the first metering point exists. **No price areas yet** means there is no such
area. Each row shows:

- **First** and **Last**: the start of the first price held and the end of the last;
- **Resolution**: the length of the latest price held, **15 min** or **1 hour**. The
  day-ahead market moved from hours to quarter-hours on 1 October 2025, so an area with
  history before then holds both;
- **Intervals**: how many prices are held;
- **Days without prices, last 30**: the days among the last 30, today included, in
  which no price starts — **Complete** when there are none;
- **History**, only with the spot-price service: how far back the job has copied the
  area's prices. **Copying… back to** *a date*: the copy is under way, going backwards
  from two weeks ago, and has reached that day; while it copies, the job works every
  minute, and five areas back to 2015 take about half an hour. **Complete from** *a
  date*: everything the service holds for the area from that day on is copied — the
  oldest day it holds, or a later day your administrator chose. **Waiting for the
  service's coverage**: the copy has not started, because the service has not yet said
  how far back it holds the area. Under the text, *N days skipped* counts days the
  service did not hold whole, which the job leaves for you to import from a file, and
  the last failure, when there is one, is shown in a sentence; the job tries again
  later on its own. **Not followed by the spot-price service** marks an area listed
  here that the service does not collect: the job does not ask for
  it, and its prices must be imported. An area the service stops following keeps the
  prices and the history already copied;
- **Last fetch**: the day the background job asked about most recently, and how it
  went. **Fetched**: the day's prices are stored. **Not yet published**: today's or
  tomorrow's prices are not out yet — the day-ahead auction publishes them around 13:00
  Norwegian time — or, from the spot-price service, the day is stored but some of its
  prices are still preliminary ([below](#preliminary-prices)); the job asks again every
  hour. **Missing**: the source has no prices for that past day; the job asks again
  once a day, up to seven times in a row, after which you import the day from a file.
  **Failed**: the reason is shown under it, and the job tries again later, waiting
  longer each time, at most six hours. **Not fetched** means the job has never tried
  the area, as when it is switched off.

From the public feed, the job only fetches the last seven days and tomorrow; a day
missing further back must be imported. From the spot-price service, it reads the last
fourteen days and tomorrow again and again, picking up the service's corrections, and
copies the history once.

### Look at the prices of a day or a week

The card **Spot prices** shows prices as a line in NOK/kWh, read in Norwegian time. It
opens on today in the first area. Choose the period in the row of buttons — **Day**,
**Week**, **Month**, **Year** or **Range** — and then the time it covers.

- **Day**, with the day in **Day**: every price of the day as a stepped line, each
  drawn from its interval's start and held until the next, with a dot on each. A day
  has 24 hourly prices (23 or 25 on the nights the clocks change), or 96 quarter-hours
  at that resolution. On the autumn night the hour from 02:00 comes twice, and the
  chart labels each run with its UTC offset, *02:00 +02* and *02:00 +01*.
- **Week**, with any day of it in **Day**: every price of the week, Monday to Sunday,
  as a stepped line without dots, labelled with the weekday and the time.

To see every price of a month, look at it a week at a time with **Week**: **Month**
shows daily averages ([below](#look-at-a-month-a-year-or-a-range)).

### Look at a month, a year or a range

A month or more is too many prices to draw one by one, so these periods show
**averages**: a line through each day's, week's or month's average price, on a shaded
band from that day's lowest to its highest price (**Lowest–highest**). Pointing at the
chart shows the average and the band's two ends.

- **Month**, chosen in **Month**: one average per day of the month. This changed:
  **Month** used to draw every price of the month, and now draws daily averages; use
  **Week** for every price.
- **Year**, chosen in **Year**: one average per day of the year.
- **Range**, from **From month** to **To month**, both included: one average per week
  for a range of up to two years — whole weeks, Monday to Sunday, so the first and the
  last week may reach into the months on either side — and one per month for a longer
  one, up to thirty years (a longer range keeps its last thirty). It opens on the last
  twelve months.

An average weighs each price by how long it held, so a day with hourly prices and a day
with quarter-hours count alike. A day, a week or a month without any price is left out
of the line rather than drawn as zero, and one with only some of its prices is averaged
over those. A period without prices says *No prices for this period.*

### Compare areas

In **Compare areas**, choose up to five price areas. Each gets its own line in its own
colour, with a legend, the first area chosen in the first colour. The
lowest–highest band is drawn only when one area is shown, since several bands would
hide each other. At least one area is always chosen.

### Read the figures

Above the chart, three figures sum up the period for the **first** area in **Compare
areas** — when you compare, each label names it, as *Average · NO1*:

- **Average**: the period's average price, each price weighed by how long it held;
- **Lowest** and **Highest**: the period's lowest and highest price, with when it held:
  the interval (and, for a week, its day) for **Day** and **Week**, or the day, the
  week or the month it fell in for the longer periods.

### Preliminary prices

From the spot-price service, tomorrow's prices are published as soon as the auction is
done, while their NOK figure is still **preliminary**: it uses a provisional exchange
rate until the day's official one is out. The job stores them as they are and replaces
them when they turn final, so a preliminary price may still change slightly. When any
price in view is preliminary, a **Preliminary** badge appears beside **Spot prices**,
and the preliminary part of each line is drawn **dashed**, with its own entry in the
legend (*NO1 (preliminary)*). For the longer periods, a day, week or month with any
preliminary price is drawn dashed. Prices from the public feed or from a file are
always final.

### Share a link to a view

The page keeps what you are looking at — the areas, the period, the day and the months
of a range — in its address, as in
`/energy/prices?areas=NO1,NO2&period=range&from=2024-01&to=2025-12`. Copy the address to
send someone the same view, bookmark it, or reload the page without losing it. A value
in the address the page does not recognise is ignored, and the page shows its default
instead; so is an area the page does not list.

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
- **Every price** lasts a quarter of an hour or an hour, starts on the quarter-hour
  (minute 00, 15, 30 or 45), and a price of an hour starts on the hour — as the
  market's do. A row that does not is refused with the reason.
- **With an end column**, each price lasts from its start to its end.
  The rows need not follow on from each other, so one file may fill several separate
  days; but each area's rows must be in time order and must not overlap.
- **Without an end column**, each price lasts until the next row of the same area
  starts, and an area's last row as long as the row before it (an hour when the area
  has only one row). Keep each area's rows in time order with no gaps: a row is refused
  when the next row of its area starts later than one interval after it.

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

## Tariffs

**Tariffs** holds the price lists consumption will be billed on, besides the spot
price. There are two kinds:

- A **power tariff** is your contract with a customer: a spot price or a fixed price,
  a markup and a monthly fee, say. It is chosen on the customer's supply period.
- A **grid tariff** is your own grid tariff, for the metering points your
  organisation is the grid operator of: the energy charge, the capacity charge, the
  fixed charge, the electricity tax and the Enova levy. It is chosen on the metering
  point. When the grid company bills grid services itself, you need none.

A tariff holds **versions** by date, and each version a list of **components**, one
line of the invoice each. When the prices change, add a version from the day they
change; the old version stays for the periods it priced.

Open **Energy** → **Tariffs**. The list shows each tariff's name, kind, status, the
version valid today and how many supply periods and metering points use it, active
tariffs first; narrow it by kind or status. The page needs `energy:tariffs-view`;
creating, editing, archiving and deleting need `energy:tariffs-manage` as well.

The tariffs price the metering points' settlements, see
[Read a metering point's settlements](#read-a-metering-points-settlements). A change to
a tariff changes the settlements not yet invoiced the next time they are shown.

### Create a tariff

Click **New tariff**. Choose the **Kind**, **Power tariff** or **Grid tariff** (it
cannot be changed afterwards), and give the tariff a **Name**, which is printed on the invoice as the
contract's name, and optionally a description. Then add at least one version with at
least one component, and save. To change a tariff later, click **Edit** on its row (it
needs `energy:tariffs-manage`) to open the tariff and edit it the same way.

The dialog marks each problem on the field it concerns, such as a missing rate or two
versions whose dates overlap.

### Add versions

Each version has **Valid from** and **Valid until (not included)**. **Valid from** is
the first day the version applies; **Valid until** is the first day it no longer applies, so a
version from 1 January valid until 1 July covers January to June. Leave **Valid
until** empty for a version that applies until further notice. The days are
Norwegian calendar days.

Two versions of one tariff cannot overlap. They may leave a gap, but a day no version
covers prices nothing. To change the prices from a day, set **Valid until** on the
current version to that day and add a new version from it.

### Add components

Add components to a version by kind; each one has a **Line text**, the text of its line on
the invoice. Move them up or down to set the order the lines are printed in. Rates are
in NOK. A power tariff may hold:

- **Spot price**: the energy at the hour's or quarter's spot price. No rate.
- **Fixed price**: the energy at a fixed price per kWh.
- **Markup**: a price per kWh on top of the energy price. A version may have more
  than one.
- **Monthly fee**: a fixed amount per month, prorated when the supply covers part of a
  month.

Each version of a power tariff has exactly one **Spot price** or **Fixed price**.

A grid tariff may hold:

- **Energy charge**: a price per kWh in the day, and optionally a night rate with the
  hours the day rate applies (from and to, whole hours) and whether the night rate
  applies all weekend. Turn on the switch **Night and weekend rate** to set it: enter
  the **Night rate (NOK/kWh)**, then when the day starts (**Day starts (hour)**) and
  ends (**Day ends (hour, not included)**), and tick **Day rate on weekdays only** if the day rate
  should not apply at weekends.
- **Capacity charge**: an amount per month by the steps below.
- **Fixed charge**: a fixed amount per month.
- **Electricity tax**: the ordinary and the reduced rate per kWh; the metering point's
  **Electricity tax** decides which applies, or none.
- **Enova levy**: the household rate per kWh, and the amount per year a point that is
  not for household use pays.
- **Monthly fee**: as for a power tariff.

A version holds at most one each of the energy charge, the capacity charge, the
electricity tax and the Enova levy.

### Set the capacity steps

The capacity charge is a table of steps: **From kW** and the price in **NOK per month**. Each
month, the point's step is found from the average of its three highest hourly
consumptions, on three different days. The first step starts at 0 kW, and each next
one must start higher; a table has at most 50 steps.

### Archive or delete a tariff

**Archive** a tariff that should no longer be chosen. An archived tariff stays on the
supply periods and metering points that already have it and still applies to them,
and you can still edit it, to correct a figure; it is just not offered for new
choices.

**Delete** removes a tariff for good, and only one nobody uses: no supply period, not
even an ended or a cancelled one, and no metering point has it. For a tariff in use the
button is disabled with the reason; archive it instead.

The full rules are in [Tariffs](/en/reference/energy/#tariffs).

## Permissions

| Permission | Lets you |
| --- | --- |
| `energy:metering-points-view` | See the list and the metering point's page, search metering points, see the Energy tab |
| `energy:metering-points-manage` | Create and edit metering points, their billing facts included |
| `energy:meters-view` | See the meter history (needed with the above to see the app at all) |
| `energy:meters-manage` | Replace a meter and register the first meter when creating |
| `energy:supply-periods-view` | See supply periods, and a customer's metering points; with `energy:consumption-view`, the **Settlements** card |
| `energy:supply-periods-manage` | Assign, switch, end and attach supply periods, and change their terms |
| `energy:consumption-view` | See consumption and the Energy tab's consumption figure; with `energy:supply-periods-view`, the **Settlements** card |
| `energy:consumption-manage` | Add manual readings |
| `energy:prices-view` | See **Prices**: the coverage and each area's prices |
| `energy:prices-manage` | Import prices from a file, with `energy:prices-view` |
| `energy:tariffs-view` | See **Tariffs**, and choose a tariff on a supply period or a metering point |
| `energy:tariffs-manage` | Create, edit, archive and delete tariffs, with `energy:tariffs-view` |
| `energy:import` | Import metering points from a file, with `energy:metering-points-manage`; import readings, with `energy:consumption-manage` and `energy:consumption-view`; what an import writes needs the permissions above |
