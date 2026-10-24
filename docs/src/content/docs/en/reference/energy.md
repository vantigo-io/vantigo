---
title: "Energy module"
description: "Metering points, meters, supply periods, consumption, the metering-point and consumption imports, spot prices and permissions."
sources:
  - apps/server/internal/energy
  - openapi/energy.yaml
  - apps/server/internal/db/migrations/00005_energy_baseline.sql
  - apps/server/internal/db/migrations/00049_energy_imports.sql
  - apps/server/internal/db/migrations/00065_energy_spot_prices.sql
---
The Energy module keeps the metering points an energy supplier supplies: where each
one is, which meter sits on it, which customer it is supplied to and for how long, and
the consumption read from it. It also holds the day-ahead spot prices of each price
area, the price dimension consumption billing is built on
([Spot prices](#spot-prices)). It is served under `/api/v1/energy/*`
(`apps/server/internal/energy`, schema `energy`) and described by
[`openapi/energy.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/energy.yaml).

Energy reads customers only through `contracts.CustomerDirectory`, never the
`customers` schema: a supply period's `customer_id` is an opaque id, not a foreign key.
It provides no directory of its own. It is one of the customers module's
`CustomerReferenceHolder`s (a customer merge re-points its supply periods) and
`CustomerPersonalData` holders (a private person's export carries their supply
periods, with the point's address and the monthly consumption inside each).

## Domain model

- **Metering point** — `gsrn` (exactly 18 ASCII digits, unique; no GS1 check digit is
  verified), an owned address (`streetAddress` ≤ 200, `postalCode` ≤ 16, `city` ≤ 100,
  all required; `countryCode` two letters, `NO` when absent), `priceArea` (two
  upper-case letters and one or two digits, such as `NO1`), an optional `gridArea`
  (trimmed, ≤ 64, blank stored as none), an optional `expectedAnnualConsumptionKwh`
  (`numeric(14,3)`, not negative and, rounded to three decimals as the column stores
  it, less than 100 000 000 000), optional `latitude` (−90 to 90) and `longitude`
  (−180 to 180), and `connectionStatus` — `New`, `Connected` or `Disconnected`, matched
  without regard to case, `New` when absent.
- **Meter** — `meterNumber` (required, ≤ 64 after trimming), `installedAt`,
  `removedAt`. A point has at most one current meter (`removedAt` empty), enforced by
  a partial unique index. Creating a point creates its first meter at the same
  instant. **Replace meter** (`POST …/metering-points/{id}/meters`) closes the current
  one at the new one's `installedAt`, which must be after the current one's; the two
  are adjacent, never overlapping.
- **Supply period** — the link between a point and a customer: `customerId`, `start`,
  `end` (open-ended when absent) and `status` — `Active`, `Ended` or `Cancelled`.
  Every timestamp a request gives must be UTC.
- **Consumption interval** — `start`, `end` (after `start`), `quantityKwh` (≥ 0),
  `quality` (`Measured`, `Estimated`, `Corrected` or `Manual`) and `source` (`Manual`
  for a manual reading, `Import` for a consumption import, `Elhub`), in a table
  partitioned by month.

## Supply periods and the no-overlap rule

No two supply periods of one metering point that are not `Cancelled` may overlap,
the ranges read as `[start, end)` with no end as for ever. A GiST exclusion constraint
(`supply_periods_no_overlap`) decides it; the endpoints run a friendly pre-check in
front of it and answer `409` "Overlapping supply period" when it fails. A write that
races past the pre-check meets the constraint and is a generic `409`. Every write to
`supply_periods` takes `lockSupplyPeriods`, a transaction-scoped advisory lock keyed by
the metering point, so concurrent writers on one point go through the constraint one
at a time.

- **Create** (`POST …/supply-periods`) — `customerId` > 0, a UTC `start` (a body
  without one is a `400` on `start`, *"Start is required."* — the router does not
  enforce the contract's required fields), the point
  (404), the customer through the directory (an unknown one is a `400` on
  `customerId`), the overlap pre-check (`409`), then an open-ended `Active` period.
- **Switch** (`POST …/supply-periods/switch`) — with an open `Active` period: the same
  customer is a `400` on `customerId`, a `switchAt` missing (*"Switch at is
  required."*) or at or before its start a `400` on `switchAt`, and otherwise it ends at `switchAt` and the new customer's period starts
  there. With none, it is a move-in and runs the create's overlap pre-check.
- **End** (`POST …/supply-periods/{periodId}/end`) — `end` after the period's start; a
  cancelled period cannot be ended. **Cancel** (`DELETE …`) sets `Cancelled`, from any
  status.

The create's write (`insertSupplyPeriod`) and the switch's transaction body
(`switchSupplyPeriod`) are functions of their own, which the import calls too.

## Consumption

- `POST …/metering-points/{id}/consumption` adds a manual interval (`source` and
  `quality` `Manual`). An interval with exactly the same start and end as a current
  one supersedes it: the old row stays as history (`is_current` false) and the new one
  points at it. Nothing else is checked for overlap. A file of readings is imported
  under the same rules ([below](#importing-consumption)).
- `GET …/consumption?from&to` lists the current intervals, `start ≥ from` and
  `end ≤ to`.
- `GET …/consumption/aggregate?from&to&resolution=hour|day|month` sums the current
  intervals in buckets of the point's **market time zone** — `Europe/Stockholm` for a
  price area starting `SE`, `Europe/Copenhagen` for `DK`, `Europe/Helsinki` for `FI`,
  `Europe/Oslo` otherwise — so a day is a local day, 23 or 25 hours across a DST
  change. `hasEstimated` is true when a bucket holds an `Estimated` or `Corrected`
  interval.
- The customer endpoints (`/customers/{customerId}/metering-points`, `/consumption`,
  `/consumption/aggregate`) read a customer's points and the intervals covered by one
  of the customer's non-cancelled supply periods. An unknown customer answers an empty
  list.
- `/stats/summary`, `/stats/timeseries` and `/stats/attention` feed the dashboard:
  counts against the start of the period, daily sums by UTC date, and the active
  periods ending within thirty days.

## The metering-point list

`GET /metering-points?search&withoutCustomer&page&pageSize` lists points by id, a page
of 1–100 (default 25). `search` matches the GSRN, the city, the street address or the
current meter's number. **`withoutCustomer=true`** keeps only the points without a
customer now: those with no `Active` supply period whose `start` is at or before the
moment of the request and whose `end` is absent or after it. A point whose only
`Active` period starts later is listed, as is a point whose periods have all ended or
been cancelled. `false`, or absent, is every point. It is the list to attach the points
an import left without a customer.

## Importing metering points

A supplier moving to Vantigo brings an export with one line per metering point. The
import reads it once, maps its columns to metering-point fields, checks it with a dry
run, and imports it in the background, in the customers import's shape
([Export, and import from a file](/en/reference/customers/#export-and-import-from-a-file)).
The design is
[`docs/superpowers/specs/2026-10-18-energy-import-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-18-energy-import-design.md).

### The record

`energy.imports` (migration `00049_energy_imports.sql`) is the customers import's
record: `state`, the file's facts, its `header`, the saved `mapping`, the counts
`processed`, `created`, `updated`, `failed`, `skipped`, and beside them `switched`,
`pending` and `attached`; `confirmed_switches` and `attach_start` for the run and the
attach. `energy.import_rows` holds each row's `cells` and, once run, its `outcome`
(`created`, `updated`, `failed`, `skipped`), `metering_point_id`, `gsrn`,
`customer_id`, `link` (below), `switch_from`, `supply_start` and `errors`. The bytes of
the file are not kept.

States: `draft → checking → checked → importing → done`, and one more transition:
`done → attaching → done`.

### Reading the file

`POST /energy/imports` takes one multipart part named `file`, read by
`internal/fileimport` exactly as the customers import reads its file: at most 50 MiB,
100 000 data rows and 200 columns; UTF-8 with or without a byte order mark, else
Windows-1252; the separator detected among `;`, `,` and tab; RFC 4180 quoting; the
first non-blank row the header; blank rows skipped. Rate-limited at 600 uploads an hour
per client address.

### Fields

A mapping is `{ mode, columns: [{ field, column | value }] }`: each field read from a
column of the file (0-based) or given one value for every row; a field at most once, a
column at most once.

| Field | Rule |
| --- | --- |
| `gsrn` | Required, from a column (never a fixed value): every row is matched by it. 18 digits. |
| `meterNumber` | Required on a create, the point's first meter. On an update a number other than the current meter's is the row's error, *"The meter number differs from the current meter's; use Replace meter."*; the same number or a blank cell leaves the meter alone. A meter is never replaced by an import. |
| `streetAddress`, `postalCode`, `city` | Required on a create; the endpoint's lengths. |
| `countryCode` | Two letters; blank or unmapped is `NO` on a create; unmapped keeps the point's on an update. |
| `priceArea` | Required on a create. Whitespace dropped and upper-cased: `NO 1`, `no1` and `NO1` are all `NO1`. |
| `gridArea` | Optional; a blank cell clears it. At most 64 characters once trimmed. |
| `expectedAnnualConsumptionKwh`, `latitude`, `longitude` | Optional; a blank cell clears it. Spaces dropped (thousands), a decimal comma read as a point; the endpoint's ranges. The consumption is rounded to three decimals, as the column stores it, before it is judged and compared: `1000,0004` against `1000` on file changes nothing. |
| `connectionStatus` | `New`, `Connected`, `Disconnected` in any case, and `ny`, `tilkoblet`, `frakoblet`. A blank cell is `New` on a create and keeps the point's on an update. |
| `customerLegalId` | The customer's organisation number: whitespace dropped, nine digits with a valid mod-11 check digit, looked up in country `NO`. |
| `supplyStart` | A date — `2026-01-01`, `01.01.2026` or `01/01/2026`, always day first — read as midnight UTC. Needs `customerLegalId` mapped. Ignored on a row with no organisation number. |

A mapped field is written and an unmapped one left as it is. Each row's point is
judged whole by the endpoints' own validator (`validateMeteringPointFields`,
`validateMeterNumber`), the point's own values laid under the row's on an update, and
an update that changes nothing writes nothing. A field a create needs and the mapping
lacks is said in those words (*"A new metering point needs a price area, and no
priceArea column is mapped"*). A value the database still refuses as too long or out
of range (SQLSTATE class `22`) fails its row with *"A value is out of range or too
long for its field."* on no column, in the check and the run alike — never the whole
run.

**Modes**: `create` refuses a GSRN on file (*"A metering point with that GSRN already
exists."*); `update` refuses a GSRN nobody has; `createOrUpdate` does both.

**Totals line**: a row whose GSRN is blank and which has at most one other non-blank
cell is `skipped`, counted and listed with its reason, never failed — exports end
with a summed annual consumption under an otherwise empty row.

**In-file duplicate**: two rows with one GSRN — the cells compared once trimmed — are
refused on the second (*"Row N of this file already has this GSRN"*), in the check
and in the run alike.

### Suggested mapping

Every read of an import carries `suggested`: the mapping last saved on an import with
the very same header; otherwise each field from the column whose header (lowercased,
letters and digits only — `ORG. NR.` is `orgnr`) is the field's own JSON name or one of
its aliases; the mode `createOrUpdate`.

| Field | Aliases |
| --- | --- |
| `gsrn` | `målepunkt`, `målepunktid`, `meteringpointid`, `gsrn` |
| `meterNumber` | `målernummer`, `målernr`, `meterno` |
| `streetAddress` | **`anleggsadresse`**, `adresse` |
| `postalCode` | **`postnranlegg`**, **`postnummeranlegg`**, `postnr`, `postnummer` |
| `city` | **`poststedanlegg`**, `poststed` |
| `priceArea` | `prisområde`, `elspotområde` |
| `gridArea` | `netteier`, `nettområde`, `nettselskap` |
| `expectedAnnualConsumptionKwh` | `årsforbruk`, `forventetårsforbruk`, `forventetårsforbrukkwh` |
| `customerLegalId` | `orgnr`, `organisasjonsnummer`, `kundeorgnr` |
| `supplyStart` | `oppstart`, `startdato`, `startdatokunde`, `leveringsstart` |

The installation's aliases (bold) win over the plain ones whatever the column order: a
supplier's export carries the customer's postal address (`POSTNR`, `POSTSTED`) beside
the installation's (`POSTNR ANLEGG`, `POSTSTED ANLEGG`), and the point's address is the
installation's. A field's JSON name wins over both. A supplier's export of the shape
`KUNDENR;KUNDENAVN;ORG. NR.;EPOST;MOBILNR;POSTADRESSE;POSTNR;POSTSTED;MÅLEPUNKTID;MÅLERNR;PRISOMRÅDE;NETTEIER;ANLEGGSADRESSE;POSTNR ANLEGG;POSTSTED ANLEGG;KOMMUNE ANLEGG;STARTDATO KUNDE;FORVENTET ÅRSFORBRUK kWh;Norgespris fra`
maps itself fully except `KOMMUNE ANLEGG` and `Norgespris fra`, which have no field,
and the customer columns, which belong to the customers import.

### The customer link

A metering point has no customer column; the link is a supply period, and a period
needs a start, which most exports lack. So a row's point is always written, and its
customer is resolved and kept on the row (`customer_id`). Every distinct organisation
number of the file is resolved **once per run, before the first row**, through
`contracts.CustomerDirectory.CustomersByLegalID` — outside every row's transaction.
Given the customer C the number names, the start S and the point's open `Active`
period P, the row's `link` is:

| Situation | Outcome |
| --- | --- |
| No organisation number (unmapped or blank) | `none`; P untouched — a blank cell never ends or changes a period |
| Not a valid number, nobody holds it, or several customers do | the row's error on that column, naming the holders when several; the point is not written |
| C merged away or archived | the row's error (*"Customer 1005 … was merged into customer 1001 and takes no more supply periods"*, *"… is archived …"*); the point is not written |
| P is already C's | `same`; nothing written, whatever S says |
| No P, S given | `assigned`: a period from S (`insertSupplyPeriod`); one that would overlap an ended or later period is the row's error with the endpoint's own message |
| No P, no S | `pending` |
| P another customer's, S given | `switched`: P ends at S and C's period starts there (`switchSupplyPeriod`); S must be after P's start, otherwise the switch's own error |
| P another customer's, no S | `pending`, with `switch_from` set to P's customer |

Every link write runs under the point's `lockSupplyPeriods` in the row's transaction.
Energy writes no events, so a row leaves no other trace than the points and periods
themselves and its own result. The customers module off (no directory composed), a
mapping with `customerLegalId` is a `400`; a row naming a number in an import saved
before is the row's error.

### Check, run and the switch confirmation

`POST …/check` queues the dry run — the state moves to `checking` first and the
previous results are cleared only when it did, so a check asked for as the worker takes
a run up is `409 import_running` and the run's outcomes stay: every row runs exactly as the real run would, each
in its own transaction, rolled back at its end — no point, meter or period is kept, and
a point the check would create has no id on its row. `POST …/run` queues the real run
of a checked import whose check found something to create or update; each row's
outcome is written in the row's own transaction beside its point and period, so a run
interrupted resumes at the first row without one.

A switch takes a point away from a customer supplied today, so the run states how many
it expects: `POST …/run` takes `{ "confirmSwitches": n }`, and when `n` is not the
check's `switched` (absent is 0) the answer is `409 import_switch_not_confirmed` with
`switches` the number. The run then switches no more than `n` points — each switch
spends one of them in its own transaction — so a row that the check assigned but that
would now switch (someone attached the point by hand meanwhile) is refused, *"…which
was not among the switches confirmed"*.

### Attaching the pending points

`POST …/attach` with `{ start, confirmSwitches }` attaches every `pending` row of a
`done` import, from `start` (a UTC timestamp, required — a body without it is a `400`
on `start`): the import goes to `attaching` and the
worker reads each pending point's `Active` period **again, now** — none, and C's
period starts at `start`; C's already (attached by hand meanwhile), and the row becomes
`same` with nothing written; another customer's, and it is switched at `start`.
`confirmSwitches` must equal the number of pending points whose active period is
another customer's at the moment of the request (`409 import_switch_not_confirmed`
otherwise), and the attach switches no more than that. The customers the rows name are
read once through the directory before the first row; one archived or merged away
since is refused. Each row becomes `attached` (with `switch_from` when it switched) or
stays `pending` with its error and its `switch_from` as the attach read it, so the
attach can be asked for again, with another date, until nothing is pending.
`pendingSwitches` on the import counts the pending rows whose `switch_from` is set: as
the run wrote them, and as each attach re-read them.

### Endpoints

All under `/api/v1/energy/`, every one admitted on `energy:import` and
`energy:metering-points-view`, the attach on `energy:supply-periods-manage` too.

| Operation | What it does |
| --- | --- |
| `POST imports` | Reads and stores the file; `201` with the import, its first twenty rows and `suggested` |
| `GET imports` | The imports, newest change first, a page of 1–100 |
| `GET imports/{id}` | One import: state, file facts, header, sample with each row's errors under the mapping, mapping, suggestion, counts, times |
| `PUT imports/{id}/mapping` | Saves the mapping on a `draft` or `checked` import (a checked one goes back to `draft`, its results cleared) |
| `POST imports/{id}/check` | `draft` → `checking` |
| `POST imports/{id}/run` | `checked` → `importing`, with `confirmSwitches` |
| `POST imports/{id}/attach` | `done` with pending rows → `attaching`, with `start` and `confirmSwitches` |
| `POST imports/{id}/stop` | An unstarted check, run or attach goes back to `draft`, `checked` or `done`; one under way stops between chunks — a check back to `draft` with its results cleared, a run as `done` with what it committed, an attach as `done` with the rest still pending |
| `DELETE imports/{id}` | Removes an import that is not running; the points and periods it wrote stay |
| `GET imports/{id}/rows?outcome=&link=` | The rows in file order, a page of 1–500, each with its cells, outcome, link, point, GSRN, customer and previous customer (named through the directory), start and errors |
| `GET imports/{id}/failed-rows` | The failed rows as `energy-import-<id>-failed-rows.csv`: the file's own header behind an `error` column, semicolons, UTF-8 with a byte order mark, guarded against formulas |

The import's `409`s carry a `code` (`EnergyImportConflictProblem`) — the only place
this module answers one, since the import pages are one flow across the customers and
energy imports: `import_running` (checking, importing or attaching),
`import_not_checked` (a run on a draft), `import_nothing_to_import` (a run on a check
that found nothing to create or update), `import_finished` (a mapping, check or run on
a done import, or a stop with nothing running), `import_switch_not_confirmed` and
`import_nothing_pending` (an attach on an import that is not done or has no pending
row).

### The permission rule

`energy:import` is the door; the mapping is checked when it is saved against what its
sender could do by hand, and refused as a `400` naming the key: every mode needs
`energy:metering-points-manage`; `create` and `createOrUpdate` also need
`energy:meters-manage`, because a point is created with its first meter and the create
endpoint itself demands it; mapping `customerLegalId` needs
`energy:supply-periods-manage`. The attach needs `energy:supply-periods-manage` at the
door.

### The worker: `energy-import`

The customers import worker's shape: an advisory lease (key `ENRGIMP1`, one cycle at a
time per installation), every statement on the lease's connection but the directory
read. A cycle takes the oldest queued import; for a check or a run it reads every row's
GSRN and organisation number once (the in-file duplicates and the numbers to resolve),
resolves the numbers through the directory, then walks the rows with no outcome in
**chunks of 500**, writing the counts and reading the stop flag after each; for an
attach it walks the untried pending rows the same way. Then the next queued import,
then the sweep. The worker takes an import up only while it is still in the state it
was read in (`StartImport`), so one stopped or deleted in between is passed over. A
whole-run failure is written to `error` on the import and retried next cycle; a row's
own value never is one (above).

A row locks its point with `FOR NO KEY UPDATE` and then takes the point's
`lockSupplyPeriods`; an endpoint takes `lockSupplyPeriods` and then inserts a period,
whose foreign key takes `FOR KEY SHARE` on the point. The two locks on the point do not
conflict, so the two orders cannot deadlock. `module.Workers` hands the worker the customers module's directory as
`Compose` hands it to the handlers, so worker mode resolves customers too.

| Setting | Default | Meaning |
| --- | --- | --- |
| `ENERGY_IMPORT_ENABLED` | `1` | `0` → the worker is never handed to the runner; an import queues and waits |
| `ENERGY_IMPORT_POLL` | `5s` | how often a cycle looks for a queued import |
| `ENERGY_IMPORT_RETENTION` | `720h` | how long a finished or abandoned import and its rows are kept before the sweep removes them |

In the default deployment the check, run and attach handlers also nudge the worker
in-process; in a split deployment the poll is the bound.

## Importing consumption

`POST /metering-points/{id}/consumption/import?dryRun=true|false` imports one point's
readings from a file in one request (design D8): no import record, no worker. Its rule
is `permission:energy:consumption-manage+energy:consumption-view+energy:import` — the
manual reading's keys behind the import's door. It is rate-limited at 600 calls an
hour per client address (`EnergyConsumptionImport`) and its body is capped at
10 MiB + 64 KiB.

The body is multipart: a part `file` and, optionally, a part `mapping` holding a
`ConsumptionImportMapping` as JSON. The answer is a `ConsumptionImportResult` on every
call: the file's `encoding`, `separator`, `header`, the first ten rows as `sample`,
`rows` and the `suggested` mapping.

| Call | What happens |
| --- | --- |
| No `mapping` part, `dryRun` true or absent | The file is only read: `evaluated` false, every count zero. This is the modal's first call. |
| No `mapping` part, `dryRun=false` | `400` on `mapping`. |
| `mapping`, `dryRun` true or absent | Every row checked and compared; nothing written. `200` with the counts, `errorCount`, the first 200 `errors` and `rangeStart`/`rangeEnd`. |
| `mapping`, `dryRun=false` | In one transaction: the point locked (`FOR NO KEY UPDATE`), every row checked and compared again, and — when no row has an error — the partitions of every month a row starts in created, the replaced intervals set not current, every row copied in; `200` with `imported` true. With any row error, `422` with the dry run's body and nothing written. |

**Reading.** `internal/fileimport` with this import's limits: 10 MiB, **50 000** data
rows, 50 columns; a refused file is a `400` on `file`, an unknown point a `404`.

**The mapping.** Each entry is a 0-based column: `start` and `quantityKwh` required,
exactly one of `end` and `resolution` (`PT1H` or `PT15M`, the end then being the start
plus that), `quality` and `gsrn` optional; a column at most once, every column within
the header. An unknown key or a body that is not one JSON object is a `400` on
`mapping`; the rest are `400`s on the field. The suggestion takes each field from the
first column whose `fileimport.HeaderKey` is one of these, and `resolution` `PT1H` when
no column names an end:

| Field | Header words |
| --- | --- |
| `start` | `fra`, `fra og med`, `start`, `from`, `starttid` |
| `end` | `til`, `til og med`, `end`, `to`, `slutt` |
| `quantityKwh` | `kwh`, `forbruk`, `volum`, `quantity`, `mengde`, `forbruk kwh`, `quantityKwh` |
| `quality` | `kvalitet`, `quality` |
| `gsrn` | `målepunkt`, `målepunktid`, `gsrn`, `meteringpointid` |

**A row.** Every problem is a `ConsumptionImportError`: the row as the import counts
it (the first under the header is 1, blank rows unnumbered), the column, the message.

- **Timestamps** are read as ISO 8601 (`2006-01-02T15:04[:05]`, `T` or a space, a
  fraction of a second allowed), `d.m.yyyy h:mm[:ss]` or `d/m/yyyy h:mm[:ss]`. With an
  offset (`Z`, `+01:00`, `+0100`) a timestamp is that instant. Without one it is a wall
  time in the point's market time zone (the aggregation's, above), stored in UTC:
  - a start the clocks skip (spring) is the row's error;
  - a start the clocks repeat (autumn) is the **earlier** instant the first time the
    file has that wall time and the **later** one every time after, in file order;
  - an end is read as the moment its interval closes: a wall time names an instant
    when the clock read it at that instant or just before it, so 03:00 on the autumn
    day names both 01:00Z and 02:00Z (CEST and CET), and 03:00 on the spring day
    names 01:00Z. Of the instants an end names, the earliest after the row's start is
    taken; an end that names none is the row's error.
- **`quantityKwh`**: spaces dropped, a decimal comma read as a point (a cell with both
  is refused), digits only — no exponent; rounded to three decimals as
  `numeric(14,3)` stores it, then `≥ 0` and `< 100 000 000 000`.
- **`quality`**: `Measured`, `Estimated`, `Corrected`, `Manual` or `målt`, `estimert`,
  `stipulert`, `korrigert`, `manuell`, any case, stored canonical; blank or unmapped is
  `Measured`.
- **`gsrn`**: when mapped, the cell must be the point's own GSRN.
- The interval follows `validateConsumptionInterval` (the manual reading's): the end
  after the start. A row with the same UTC start and end as an earlier row is an error
  on the later one ("The same interval as row N").

**Comparing.** The rows without an error are compared with the point's current
intervals by exact `(start, end)`: the same `quantity_kwh` and `quality` is
**unchanged** (nothing written), another is **replacing** (the old row `is_current`
false, the new one's `supersedes_id`/`supersedes_start` pointing at it, as the manual
add does), none is **new**. As with the manual add, intervals that overlap without
being equal are not compared, and the source is ignored: a manual reading repeated by
an import is unchanged. Written rows have `source` **`Import`** and `received_at` the
request's time. The current-row unique index backs a race with a manual add: the
loser is a `409`.

## Spot prices

The price dimension consumption is billed by: the day-ahead price of each price area,
stored as intervals, fetched every day by a worker from a configured feed or imported
from a file, and read by the Prices page. The design is D2 of
[`docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md).
Prices are market data, not a customer's: they are read without a supply-period gate
and are in no customer's export.

### The model

- **Spot price** — `energy.spot_prices` (migration `00065_energy_spot_prices.sql`):
  `price_area` (as a metering point carries it, such as `NO1`), `start` and `end`
  (half-open `[start, end)`, `end` after `start`), `nok_per_kwh` (`numeric(12,6)`, the
  price a settlement uses), `eur_per_kwh` and `exchange_rate` (`numeric(12,6)`, kept for
  the record when the source gives them), `source` (`Feed` for the worker, `Import` for
  a file) and `received_at`. One row is one interval at whatever resolution the source
  gives — hourly from the public feed today, a quarter of an hour when the day-ahead
  market's 15-minute unit reaches it — exactly as consumption intervals are.
- **No two overlapping.** `(price_area, start)` is unique, and the GiST exclusion
  `spot_prices_no_overlap` on `(price_area, tstzrange(start, end, '[)'))` refuses two
  intervals of one area that overlap: a day answered in quarter-hours over a day stored
  in hours is refused, not doubled.
- **Replaced, not revisioned.** An interval written again with the same area, start
  and end and other figures has its figures overwritten, `source` and `received_at`
  with them, and counts as `replaced`; the same figures, compared at the column's six
  decimals, count as `unchanged` and write nothing. No history row is kept: a
  settlement built on the old figure keeps it, and the next settlement carries the
  difference.
- **Fetch record** — `energy.spot_price_fetches`, the feed worker's record of one
  `(price_area, day)`, the day a civil day in Europe/Oslo: `status` (`ok` once the
  day's prices are stored, `pending` while tomorrow's are not yet published, `missing`
  for a past day the feed has no file for, `failed` for anything else), `attempts`
  (every request counted), `last_error` (the sentence of the last failure, never the
  response's body), `attempted_at`, `fetched_at` and `next_attempt_at` (when the worker
  may ask again).

### The feed worker: `energy-spot-prices`

The worker is **off by default**: fetching prices is an outbound call an installation
opts into. Without it, prices come by file ([below](#importing-prices)).

| Setting | Default | Meaning |
| --- | --- | --- |
| `ENERGY_SPOT_PRICE_FEED_ENABLED` | `0` | `1` hands the worker to the runner; `0` leaves it out |
| `ENERGY_SPOT_PRICE_FEED_URL` | `https://www.hvakosterstrommen.no/api/v1/prices/{year}/{month}-{day}_{area}.json` | The feed as a template: an absolute `https` URL carrying `{year}`, `{month}`, `{day}` and `{area}`, rendered per area and day with the month and the day zero-padded (`…/2026/03-29_NO1.json`). A template that is not `https` or lacks a placeholder stops the start-up, as an invalid setting does |
| `ENERGY_SPOT_PRICE_FEED_POLL` | `15m` | How often a cycle runs; at least `1m`, a shorter one stopping the start-up |

A cycle:

1. **The lease.** The cycle runs under a PostgreSQL advisory lock taken with
   `pg_try_advisory_lock`, key `ENRGSPT1` (`0x454E524753505431`); a cycle that finds it
   held by another instance does nothing, so one replica fetches.
2. **The areas in use**: the distinct `price_area` of the metering points with a supply
   period that is not `Cancelled`, and every area already in `energy.spot_prices`. An
   installation with no such point fetches nothing.
3. **The days.** For each area, the civil days in Europe/Oslo from seven days before
   today to tomorrow whose fetch record is not `ok` and is due (`next_attempt_at` absent
   or passed). The feed's days are Europe/Oslo days whatever the area — the Nordic
   day-ahead market's day is a CET day — and the stored intervals are instants. A cycle
   makes **at most twenty requests**; the rest wait for the next cycle. The worker never
   reaches further back than seven days on its own: older days come by file.
4. **The answer**, written to the day's fetch record:

| Answer | Status | Asked again |
| --- | --- | --- |
| `200` with a day that passes the checks below | `ok`, the day's prices written | never |
| `404` for tomorrow | `pending` — the day-ahead auction publishes about 13:00 CET | in an hour |
| `404` for a past day | `missing` | in a day, up to seven attempts; then left `missing`, and the day comes by file |
| anything else — another status, a network error, a body that is not a JSON array, a day the checks refuse | `failed`, with the reason in `last_error` | after `min(6 h, 15 min × 2^(attempts − 1))` |

A `200`'s entries (`time_start`, `time_end`, `NOK_per_kWh`, `EUR_per_kWh`, `EXR`) are
written only when they are contiguous, lie within the day — the first starting at the
day's local midnight, the last ending at the next, so a DST day has 23 or 25 hours —
and overlap no stored interval of another length. Then the day's intervals (`source`
`Feed`, replaced or unchanged under the model's rule) and the fetch record `ok` are
written in one transaction; otherwise nothing of the day is written and the record is
`failed` with the reason.

**The client** is a copy of the calendar subscriptions'
([The fetch](/en/reference/calendar/#the-fetch)): its dialler is
`netguard.DialContext`, so the feed's host is resolved and every address checked
against the platform's forbidden ranges — private, loopback, link-local, carrier-grade
NAT and cloud metadata — and the connection made to the address checked; no proxy;
**no redirect followed**; ten seconds to dial and fifteen for the response's headers;
the request carries `Accept: application/json` and the body is read to at most
**1 MiB**. A feed on a private address cannot be used; import its files instead.

### Importing prices

`POST /spot-prices/import?dryRun=true|false` imports prices from a file in one
request, in the consumption import's shape: no import record, no worker. Its rule is
`permission:energy:prices-manage+energy:prices-view`; it is rate-limited at 600 calls
an hour per client address, and the body is multipart with one part, `file`.

**Reading.** `internal/fileimport` with the consumption import's limits: 10 MiB,
50 000 data rows, 50 columns; UTF-8 with or without a byte order mark, else
Windows-1252; the separator detected among `;`, `,` and tab; the first non-blank row
the header. A refused file is a `400` on `file`, as is a header without an area, a
start or a NOK price column.

**The layout** is fixed, unlike the consumption import's, whose files come from many
systems: there is no mapping part. Each column is found by its header as
`fileimport.HeaderKey` reads it (lowercased, letters and digits only, so `Price area`,
`price_area` and `PRICEAREA` are one word), each header matched at most once:

| Column | Header words |
| --- | --- |
| Area — required | `area`, `price_area`, `pricearea`, `prisområde`, `elspotområde`, `område`, `sone` |
| Start — required | `start`, `from`, `fra`, `time_start`, `starttid` |
| End | `end`, `to`, `til`, `time_end`, `sluttid` |
| NOK price per kWh — required | `nok_per_kwh`, `price`, `pris`, `kr/kwh`, `nok/kwh`; or in øre, divided by 100: `øre/kwh`, `ore_per_kwh` |
| EUR price per kWh | `eur_per_kwh`, `eur/kwh` |
| Exchange rate | `exr`, `exchange_rate`, `kurs` |

**A row.**

- **Timestamps** are read as the consumption import reads them
  ([Importing consumption](#importing-consumption)): ISO 8601, `d.m.yyyy h:mm` or
  `d/m/yyyy h:mm`; with an offset or `Z` that instant, without one a wall time in
  **Europe/Oslo**, whatever the row's area — the spring gap is the row's error and a
  repeated autumn time is the earlier instant the first time and the later one after.
- **The end.** With an end column, each row's end is read as the consumption import
  reads an end, and gaps between an area's rows are allowed, so one file may fill
  several separate days; a row out of order or overlapping another of its area is the
  row's error. Without one, an interval closes at the next row's start **of the same
  area**, in file order, and an area's last row takes the length of the row before it
  (an hour when the area has a single row). A row is then the row's error when it is
  out of order, or when the next row of its area starts more than the previous
  interval's length — at most an hour — after it.
- **Several areas** may share a file; each area's rows are judged on their own.
- **Against what is stored**: a row equal to a stored interval (the same area, start
  and end) replaces its figures when they differ and is unchanged when they do not; a
  row whose interval overlaps a stored interval of **another length** is the row's
  error, since prices of one area never overlap.

**The calls.**

| Call | What happens |
| --- | --- |
| `dryRun` true or absent | Every row checked and compared with what is stored; nothing written. `200`. |
| `dryRun=false` | In one transaction, every row checked and compared again and — when no row has an error — every inserted and replaced interval written with `source` `Import` and `received_at` the request's time; `200` with `imported` true. **All or nothing**: with any row error, `422` with the same body and nothing written. |

The answer is a `SpotPriceImportResult`: `dryRun`; `imported`, true only when prices
were written; `rows`, the data rows read; `errorCount`, every problem found, and
`errors`, the first 200 in file order, each `{row, column, message}` — the row as the
import counts it (the first under the header is 1, blank rows unnumbered), the 0-based
column, absent for a problem with the row as a whole; and `areas`, per area of the
file over the rows without an error, `{priceArea, inserted, replaced, unchanged,
rangeStart, rangeEnd}`.

### Endpoints

All under `/api/v1/energy/`.

| Operation | Rule | What it does |
| --- | --- | --- |
| `GET spot-prices?area&from&to` | `permission:energy:prices-view` | The intervals of one area with `start ≥ from` and `start < to`, ascending. `area` must be two letters and one or two digits, `from` before `to`, and `to − from` at most **35 days** — a month at a quarter-hour resolution is one answer — otherwise a `400` (a window too long on `to`). |
| `GET spot-prices/coverage` | `permission:energy:prices-view` | Per area in use or already priced: `priceArea`, `firstStart` and `lastEnd` (absent with no prices), `intervalCount`, `daysMissingLast30` — the Europe/Oslo days among the last thirty, today included, with no interval starting in them — and `lastFetch`, the worker's most recent fetch record for the area (`day`, `status`, `attempts`, `attemptedAt`, `nextAttemptAt`, `error`), absent when it never tried one. |
| `POST spot-prices/import?dryRun` | `permission:energy:prices-manage+energy:prices-view` | Imports a file of prices ([above](#importing-prices)). |

## Permissions

| Key | Display | Sensitive |
| --- | --- | --- |
| `energy:consumption-manage` | Manage energy consumption | no |
| `energy:consumption-view` | View energy consumption | no |
| `energy:import` | Import energy data — import metering points and consumption from a file; each thing written needs the permission it needs by hand | yes |
| `energy:metering-points-manage` | Manage energy metering points | no |
| `energy:metering-points-view` | View energy metering points | no |
| `energy:meters-manage` | Manage energy meters | no |
| `energy:meters-view` | View energy meters | no |
| `energy:prices-manage` | Manage energy prices — import spot prices from a file | no |
| `energy:prices-view` | View energy prices — spot prices per price area and what is held of them | no |
| `energy:supply-periods-manage` | Manage energy supply periods | no |
| `energy:supply-periods-view` | View energy supply periods | no |

Every key is delegable and in the category Energy. Each operation's rule is its
`x-vantigo-access` in the contract; the metering-point list and page need
`energy:metering-points-view` and `energy:meters-view`, and creating a point
`energy:metering-points-manage` and `energy:meters-manage` with both views.

## Workers

| Worker | What it does |
| --- | --- |
| `energy-import` | Checks, runs and attaches metering-point imports; sweeps old ones ([above](#the-worker-energy-import)) |
| `energy-spot-prices` | Fetches the day-ahead prices of the areas in use from the configured feed, when `ENERGY_SPOT_PRICE_FEED_ENABLED` is on ([above](#the-feed-worker-energy-spot-prices)) |
