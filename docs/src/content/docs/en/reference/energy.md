---
title: "Energy module"
description: "Metering points, meters, supply periods, consumption, the metering-point and consumption imports, spot prices and permissions."
sources:
  - apps/server/internal/energy
  - openapi/energy.yaml
  - apps/server/internal/db/migrations/00005_energy_baseline.sql
  - apps/server/internal/db/migrations/00049_energy_imports.sql
  - apps/server/internal/db/migrations/00065_energy_spot_prices.sql
  - apps/server/internal/db/migrations/00075_energy_spot_price_service.sql
  - apps/server/internal/energy/spot_price_service_client.go
  - apps/server/internal/energy/spot_price_service_sync.go
  - apps/server/internal/netguard
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
stored as intervals in the installation's own database, filled by the
`energy-spot-prices` worker from **one** source — the shared spot-price service or a
public feed — or imported from a file, and read by the Prices page. The design is D2 of
[`docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md),
and for the service
[`docs/superpowers/specs/2026-10-26-energy-spot-price-service-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-26-energy-spot-price-service-design.md).
Prices are market data, not a customer's: they are read without a supply-period gate
and are in no customer's export.

The service collects and the installation keeps what it bills by: with the service as
its source the worker **copies** its prices into `energy.spot_prices`, with their
provenance, and never reads through to it. Every read — the Prices page, the coverage
and the settlements to come — reads the installation's own rows, which are in its own
backups.

### The model

- **Spot price** — `energy.spot_prices` (migration `00065_energy_spot_prices.sql`,
  widened by `00075_energy_spot_price_service.sql`): `price_area` (as a metering point
  carries it, such as `NO1`), `start` and `end` (half-open `[start, end)`, `end` after
  `start`), `nok_per_kwh` (`numeric(12,6)`, the price a settlement uses), `eur_per_kwh`
  and `exchange_rate` (`numeric(12,6)`, kept for the record when the source gives
  them), `source` (`Feed` for the public feed, `Service` for the spot-price service,
  `Import` for a file; `ck_spot_prices_source`) and `received_at`. One row is one
  interval at whatever resolution the source gives — hourly from the public feed, a
  quarter of an hour from the service since the day-ahead market moved to it on
  2025-10-01 and an hour before — exactly as consumption intervals are.
- **State and provenance.** `state` is `final` or `preliminary`
  (`ck_spot_prices_state`, default `final`). A `Feed` or `Import` row is always
  `final`: the feed publishes a day once, and a file is the operator's say-so. A
  `Service` row is `preliminary` while the service says so — it has tomorrow as soon as
  the auction publishes it, and Nord Pool's NOK figure stays preliminary until the
  day's exchange rate is final. A settlement prices only from `final` intervals (billing
  design D5 as amended). `upstream` (the service's own source, such as
  `nordpool_portal`, `entsoe` or `hks`), `rate_source` (whose EUR/NOK rate gave the NOK
  figure, such as `nordpool` or `norges_bank`) and `upstream_version` (the service's
  version of the interval) are a `Service` row's provenance, null on the others.
  `upstream` is not CHECKed, since the service may add a source.
- **No two overlapping.** `(price_area, start)` is unique, and the GiST exclusion
  `spot_prices_no_overlap` on `(price_area, tstzrange(start, end, '[)'))` refuses two
  intervals of one area that overlap: a day answered in quarter-hours over a day stored
  in hours is never doubled. The service sync replaces a fetched day at another
  resolution whole ([Judging a day and writing it](#judging-a-day-and-writing-it)); the
  feed and the file import are refused.
- **Replaced, not revisioned.** An interval written again with the same area, start
  and end and other figures, another `state` or other provenance has them overwritten,
  `source` and `received_at` with them, and counts as `replaced` — a correction, or a
  preliminary price turning final; the same figures, compared at the column's six
  decimals, and the same state and provenance count as `unchanged` and write nothing.
  A file written over a `Service` interval leaves it `Import`, `final` and without
  provenance. No history row is kept: a settlement built on the old figure keeps it,
  and the next settlement carries the difference.
- **Fetch record** — `energy.spot_price_fetches`, the worker's record of one
  `(price_area, day)` of its live window, whichever source fills it, the day a civil
  day in Europe/Oslo: `status` (`ok` once the day's prices are stored — from the
  service, a whole day with every interval `final` — `pending` while today's or
  tomorrow's are not yet published or, from the service, not yet whole, and while a
  day the service wrote still has a `preliminary` interval, `missing` for a past day
  the source does not have, `failed` for anything else), `attempts` (the requests made
  since the record last changed status — a day pending for hours and then missing
  starts its missing attempts from one), `last_error` (the sentence of the failure when
  the last request failed and, from the service, the reason a `missing` day was not
  written; cleared by any other answer; never the response's body), `attempted_at`,
  `fetched_at` (from the feed, the last time the day was stored; from the service, the
  last read of the live window that recorded the day, whatever its status; kept through
  a failed request) and `next_attempt_at` (when the worker may ask again; absent once it
  never will).
- **History record** — `energy.spot_price_history` (migration
  `00075_energy_spot_price_service.sql`), one row per area the service sync follows,
  written in the first cycle the service answers the area `active`:
  `cursor_day` (the oldest civil day read so far, null before the first window),
  `floor_day` (the effective floor: the later of the Europe/Oslo day of the service's
  first interval for the area and `ENERGY_SPOT_PRICE_HISTORY_FROM`, set again each time
  the service answers the area; kept as it was while the service's coverage of the area
  is null, and null until it is first known),
  `live_through` (the last day of the most recent live window read whole for the area —
  tomorrow at that read — null before the first; [the gap](#the-gap-after-a-downtime)),
  `active` (whether the service followed the area when `/areas` last answered; a row
  whose area the service stops following is kept, inactive),
  `days_skipped` (the days left for a file, [below](#the-history-walk)), `attempts`,
  `last_error` and `next_attempt_at` (the backoff of a failed window), and
  `updated_at`. The walk is complete once `cursor_day` is at or before `floor_day`.

### The sources

The worker is **off by default**: fetching prices is an outbound call an installation
opts into. Without it, prices come by file ([below](#importing-prices)). It has one
source, chosen by the settings:

| Setting | Default | Meaning |
| --- | --- | --- |
| `ENERGY_SPOT_PRICE_SERVICE_URL` | unset | The spot-price service's base URL, `http://vantigo-spotprices:8080` in our hosted environments. Set, it hands the worker to the runner with the service as its source. An absolute `http` or `https` URL with a host; a user, a query, a fragment or a path other than `/` stops the start-up ("must be the service's base URL, with no user, path, query or fragment"). An `http` URL is dialled only to a private address ([The service client](#the-service-client-and-its-guard)) |
| `ENERGY_SPOT_PRICE_SERVICE_KEY` | unset | The installation's bearer key for the service. Required with the URL, refused without it; at most 200 bytes of printable characters with no space. Redacted wherever the configuration is printed, and never in a problem, a log line or an error sentence |
| `ENERGY_SPOT_PRICE_HISTORY_FROM` | unset | The oldest civil day the service sync copies, `YYYY-MM-DD`; anything else stops the start-up. Unset copies everything the service holds for the area |
| `ENERGY_SPOT_PRICE_FEED_ENABLED` | `0` | `1` hands the worker to the runner with the public feed as its source; `0` leaves the feed out; any other value stops the start-up |
| `ENERGY_SPOT_PRICE_FEED_URL` | `https://www.hvakosterstrommen.no/api/v1/prices/{year}/{month}-{day}_{area}.json` | The feed as a template: an absolute `https` URL carrying `{year}`, `{month}`, `{day}` and `{area}`, rendered per area and day with the month and the day zero-padded (`…/2026/03-29_NO1.json`). A template that is not `https` or lacks a placeholder stops the start-up, as an invalid setting does — even with the worker off |
| `ENERGY_SPOT_PRICE_FEED_POLL` | `15m` | How often a cycle runs, **with either source**: a Go duration of at least `1m`; a shorter one, or one that does not parse, stops the start-up — even with the worker off. While the service sync is still copying history, the next cycle comes after a minute instead ([The budget](#the-budget-and-the-cadence)) |

**One source per installation.** `ENERGY_SPOT_PRICE_SERVICE_URL` together with
`ENERGY_SPOT_PRICE_FEED_ENABLED=1` stops the start-up, on the service's URL: "set one
spot price source: the service or ENERGY_SPOT_PRICE_FEED_ENABLED, not both". Two
writers of one area would make `source` meaningless and race the exclusion constraint.
The file import is not a source in this sense: it is always there, beside either.

Both sources share the cycle's frame:

- **The lease.** The cycle runs under a PostgreSQL advisory lock taken with
  `pg_try_advisory_lock`, key `ENRGSPT1` (`0x454E524753505431`); a cycle that finds it
  held by another instance does nothing, so one replica fetches.
- **The areas in use**: the distinct `price_area` of the metering points with a supply
  period that is not `Cancelled`, and every area already in `energy.spot_prices`. With
  the feed these are the areas followed, and an installation with no such point
  fetches nothing; with the service, the areas followed are those the service answers
  `active`, and the areas in use stand in for them only when it cannot be asked
  ([The areas followed](#the-areas-followed)).
- **The write**: every source's day goes through the one write the file import uses,
  under the area's advisory lock, and is judged by the one day judgement
  ([Judging a day and writing it](#judging-a-day-and-writing-it)).

### The feed worker: `energy-spot-prices`

With `ENERGY_SPOT_PRICE_FEED_ENABLED=1` the worker reads the public feed a day at a
time. A cycle:

1. **The lease**, as above.
2. **The areas in use**, as above, and no other.
3. **The days.** For each area, the civil days in Europe/Oslo from seven days before
   today to tomorrow in which no stored interval of the area starts — a day a file
   filled is not asked for, whatever its fetch record says — and that are due: the day
   has no fetch record yet, or its `next_attempt_at` has passed. A record with no
   `next_attempt_at` (`ok`, or `missing` given up on) is never due again. The feed's
   days are Europe/Oslo days whatever the area — the Nordic day-ahead market's day is a
   CET day — and the stored intervals are instants. A cycle makes **at most twenty
   requests**, the oldest days first; the rest wait for the next cycle. The worker
   never reaches further back than seven days on its own: older days come by file.
4. **The answer**, written to the day's fetch record:

| Answer | Status | Asked again |
| --- | --- | --- |
| `200` with a day that passes the checks below | `ok`, the day's prices written | never |
| `404` for today or tomorrow | `pending` — the day-ahead auction publishes tomorrow's prices about 13:00 CET | in an hour, as long as the day stays in the window |
| `404` for a past day | `missing` | in a day, until the seventh `missing` answer in a row — `attempts` counts the requests since the record last changed status, so a day's hours of `pending` do not spend them; the seventh leaves it `missing` with no `next_attempt_at`, and the day comes by file |
| anything else — another status, a redirect, a network error or a timeout, an address the guard refuses, a body over 1 MiB or not a JSON array, a day the checks refuse | `failed`, with the reason in `last_error` | after `min(6 h, 15 min × 2^(attempts − 1))`, `attempts` counting this request and the `failed` ones before it since the status last changed |

A `200`'s entries (`time_start`, `time_end` as RFC 3339 times with an offset;
`NOK_per_kWh`, required; `EUR_per_kWh` and `EXR`, optional; a price may be negative)
are written only when they are contiguous, lie within the day — the first starting at
the day's local midnight, the last ending at the next, so a DST day has 23 or 25
hours — every figure is one `numeric(12,6)` can store, and they overlap no stored
interval of another length. Then the day's intervals (`source`
`Feed`, replaced or unchanged under the model's rule) and the fetch record `ok` are
written in one transaction; otherwise nothing of the day is written and the record is
`failed` with the reason.

**The client** is a copy of the calendar subscriptions'
([The fetch](/en/reference/calendar/#the-fetch)): its dialler is
`netguard.DialContext`, so the feed's host is resolved and every address checked
against the platform's forbidden ranges — private, loopback, link-local, carrier-grade
NAT and cloud metadata — and the connection made to the address checked; no proxy;
**no redirect followed**; **twenty seconds** for the whole fetch, within it ten to
dial, ten for the TLS handshake and fifteen for the response's headers; no kept
connection; the request carries `User-Agent: Vantigo-Energy` and
`Accept: application/json`, and the body is read to at most **1 MiB**. A feed on a
private address cannot be used; import its files instead.

**The log.** A day stored is logged at info with the area, the day and the inserted,
replaced and unchanged counts; a `404` for a past day at info, with the attempts and
whether the worker gave up; one for today or tomorrow at debug; a failure at
warning with the area, the day, the feed's host — never the address's path or query,
nor the body — the attempts and the sentence; a cycle the database ended at error.

### The service sync

With `ENERGY_SPOT_PRICE_SERVICE_URL` set the worker copies from
[vantigo-spotprices](https://github.com/vantigo-io/vantigo-spotprices), the shared
service our hosted environments run beside their tenants: it polls Nord Pool's data
portal, backfills history from ENTSO-E and the community feed on the operator's
request, and serves every interval with its state, source, version and exchange-rate
source. Every tenant sees the same rows; the key is for revocation, not for separating
data. The sync reads two of its operations, `GET /api/v1/areas` and
`GET /api/v1/prices`. A cycle, under the lease, does three things — and, after the
worker was away longer than the live window reaches back, a fourth
([the gap](#the-gap-after-a-downtime)).

#### The areas followed

1. **The areas.** `GET /api/v1/areas` once. An area is **followed** when the service
   answers it `active` — NO1–NO5 on our hosts — whether or not it is in use, so the
   Prices page fills on a fresh installation with no metering point yet. A code that is
   not one of the Nordic price areas (`^(NO[1-5]|SE[1-4]|DK[12]|FI)$`) is ignored. Each
   followed area gets its history row in that cycle; its floor is the later of the
   Europe/Oslo day of its `coverage.firstStart` and `ENERGY_SPOT_PRICE_HISTORY_FROM`,
   and while its `coverage` is null the floor it had is kept. A floor on or after the
   live window's first day leaves nothing to walk, and the walk is complete at once.
   Every answer sets each history row's `active`: true for an area the service answers
   `active`, false for any other. A row whose area the service stops following is
   kept, with what it copied, and is no longer read; answered `active` again, it is
   followed again, and [the gap](#the-gap-after-a-downtime) reads what it missed.
   An area **in use that the service does not follow** — no history row, or an inactive
   one — gets no request and no fetch record, and the coverage says so
   (`followedByService: false`, "Not followed by the spot-price service" on the Prices
   page).
   When the `/areas` request fails, nothing is known of what the service follows: the
   failure is logged, the history rows are **left as they are** — their `attempts`,
   `last_error` and `next_attempt_at` are their windows' alone, so the next answer has
   nothing of the failure to clear —, no history or gap window is read that cycle, and
   the live window runs instead for the areas in use whose code is a Nordic price area.
   A `429` on `/areas` ends the cycle's requests there
   ([The budget](#the-budget-and-the-cadence)).

#### The live window

2. **The live window**, per followed area: the civil days in Europe/Oslo from fourteen
   days before today to tomorrow — fourteen because the service re-checks Nord Pool's
   corrections that far back. A day is **due** when it has no fetch record, or its
   `next_attempt_at` has come — an `ok` record, or a `missing` one given up on, has
   none. The window is **due** when any of its days is, or when its last read — the
   newest `fetched_at` among its days — is absent or more than **24 hours** old, to pick
   up a correction's new version. A window that is not due costs no request. A due
   window is **one request**:
   `GET /api/v1/prices?area=<area>&from=<today−14, 00:00 Oslo>&to=<today+2, 00:00 Oslo>&currency=EUR`,
   both instants in UTC. `currency=EUR` is deliberate: asked for NOK, the service
   refuses the whole window when one day has no exchange rate, while the NOK figure is
   answered whenever it is known anyway. The answer is split into civil days and each
   day judged on its own ([below](#judging-a-day-and-writing-it)). A day **written** is
   recorded on every read, due or not — a correction or a day turning final can come
   early — with `fetched_at` the read's time. A day **not written** is recorded only
   when it is due, so a missing day is asked for daily however often the window is
   read; it too gets `fetched_at` the read's time. The days written are written
   together in one transaction under the area's lock, with their records, and with
   the area's history row's `live_through` set to tomorrow — unless a gap is left
   before the window, which [the gap's](#the-gap-after-a-downtime) windows read
   first. A window re-read with no day due whose request fails records nothing — no day
   was due to fail — and is asked again the next cycle, not after a backoff.

| Day | Status | Asked again |
| --- | --- | --- |
| Whole, every interval with a NOK price, every interval `final` | `ok`, the day written | never on its own; it is read again with the window |
| Whole, every interval with a NOK price, some interval `preliminary` | `pending`, the day written | in an hour |
| Today or tomorrow, not written — absent, not whole or without a NOK price (and due) | `pending`, nothing of the day written | in an hour, as the feed's `pending` |
| A past day not written — absent, not whole or without a NOK price (and due) | `missing`, nothing of the day written, the reason in `last_error`: "the spot-price service holds no prices for the day", "the spot-price service holds no NOK price for the day" or the judgement's sentence | in a day, given up after the seventh `missing` in a row, as the feed's |
| A day with an interval Vantigo cannot take — an unknown source or state, no version, a figure it cannot store (and due) | `failed`, nothing of the day written, with the interval's sentence | after `min(6 h, 15 min × 2^(attempts − 1))`, as the feed's |
| A day the write refuses — over an imported day at another resolution (and due) | `failed`, nothing of the day written, with "the day's intervals overlap stored prices of another length" | as any `failed` |
| The request failed — a refused key, rate limiting, another status, a network error or a timeout, an address the guard refuses, a body too large, one that is not a list of prices, or an interval whose time Vantigo cannot read or that does not end after it starts | `failed`, the **due** days only, with the request's sentence; the others are left as they are, and `fetched_at` is kept | after `min(6 h, 15 min × 2^(attempts − 1))`, or at a `429`'s `Retry-After` when that is later |

#### The history walk

3. **History**, per followed area in area order, with what the cycle's request budget
   leaves, and only when `/areas` answered. An area walks while its floor is known, its
   cursor (or, before the first window, the live window's first day) is after the
   floor, and no backoff is pending; with no floor known no walk starts. The walk
   starts at the day before the live window (fifteen days before today) and goes
   **backwards** in windows of **35 civil days**, one request a window, never past the
   floor (`floor_day`, [the model](#the-model)). The service refuses a window longer
   than 35 × 24 hours, so a window across the autumn clock change, 35 civil days of 841
   hours, is shortened to **34 days**. Each window's days are judged as the live
   window's are and written, with the history row's new cursor (the window's first
   day), in **one transaction** per window, so an interrupted walk resumes where it
   stopped; a window answered resets `attempts`, `last_error` and `next_attempt_at`. A
   history window records no fetch records: a day that is not whole, has no NOK price,
   has an interval Vantigo cannot take or lies over an imported day at another
   resolution adds to `days_skipped`, is not asked for again, and is left for a file.
   The walk is complete once `cursor_day` is at or before `floor_day`; when the floor
   moves earlier — the operator ran another backfill on the service — the walk resumes
   from its cursor down to the new floor. A failed request counts in the history row's
   `attempts`, records its sentence in `last_error` and sets `next_attempt_at` by the
   live window's backoff (or a `429`'s `Retry-After` when later), without moving the
   cursor. `attempts` counts failures of one kind in a row — **permanent** or
   **transient** ([the client](#the-service-client-and-its-guard)) — and starts again
   from one when the kind changes. A window that failed **permanently three times in a
   row** — the service answered `400`, or an answer Vantigo cannot read — is
   **skipped**: its days are added to `days_skipped`, the cursor moves past it,
   `attempts` and `next_attempt_at` are cleared and the walk goes on, while its sentence
   stays in `last_error` until the next window is answered. A transient failure never
   skips a window, however often it repeats. The walk's first window, when no live
   window has been read whole yet, also sets `live_through` to the day before the live
   window, so a live window that fails for days leaves a gap the next answer closes.

#### The gap after a downtime

The live window reaches back fourteen days and the walk only goes back from its cursor,
so a worker away longer than that would leave days nobody reads. Before an area's live
window, per followed area, when its `live_through` is before **fifteen days before
today** (the day before the live window), the days from the day after `live_through`
to that day are read **forward, oldest first**, in history windows: 35 civil days a
request, 34 across the autumn clock change, never past the day before the live window.
Each window's days are judged and written as the walk's are — no fetch records; a day
not whole, without a NOK price, with an interval Vantigo cannot take or over an
imported day at another resolution adds to `days_skipped` — and each window moves
`live_through` to its last day, in one transaction. The gap shares the cycle's budget
and the history row's backoff with the walk, and its windows are skipped after three
permanent failures in a row as the walk's are. The live window read after the gap
closes moves `live_through` on to tomorrow; one read while a gap is left (the gap backed
off) leaves it, so no day is lost. A row with no `live_through` (none read yet) has no
gap: its first live window sets it.

#### The budget and the cadence

A cycle makes **at most twenty requests**, the `/areas` request included, shared by
every area: `/areas` first, then per area in area order its gap's windows and its live
window when due, then history. A `429` — on any of them — ends the cycle's requests;
the failure is recorded as above and the rest waits for the next cycle. While, at the
end of a cycle, any followed area's walk or gap can go on — for the walk a floor known,
the cursor after it; for the gap `live_through` before the day before the live window;
and no backoff pending — the next cycle comes **one minute** after this one instead of after
`ENERGY_SPOT_PRICE_FEED_POLL`; once every walk is complete or backed off, or `/areas`
failed, the poll paces the worker again. That cadence is the worker's own in-memory
state, set by each cycle it runs, not a column: an instance whose cycle found the
lease held by another replica waits the poll. Five areas from 2015 are about 600 requests, so a first copy takes roughly
half an hour. The service allows 600 requests a minute per key.

#### Judging a day and writing it

Every source judges a day with one function, `judgeSpotPriceDay`: a day is
**complete** when its intervals, ascending, are contiguous, the first starting at the
day's local midnight and the last ending at the next — 24 hours, or 23 and 25 on the
DST nights, at any resolution — and otherwise fails with the feed's own sentences
(above). From the service a day is written only when it is complete, every interval
is one Vantigo can take and every interval carries a NOK price. Its figures:
`nok_per_kwh` is the service's `priceNokPerKwh`, `eur_per_kwh` its `priceEurPerMwh`
divided by 1 000, and `exchange_rate` its `exchangeRate`, each decoded from the JSON
number's own text and scaled by moving its decimal point, never through a binary
division, then rounded to six decimals as the column stores them; each must be less
than 1 000 000 in size once rounded, as the feed's are. `priceEurPerMwh` is required;
`priceNokPerKwh` and `exchangeRate` may be null — a null NOK price leaves the day
unwritten, not failed. Every interval must carry a `version`, an integer; `state`
must be `final` or `preliminary`; the service's `source` and `rateSource` must be a
lower-case letter followed by at most nineteen lower-case letters, digits and
underscores (`^[a-z][a-z0-9_]{0,19}$`), `rateSource` only when given. An interval that
breaks one of these fails its **day** with its sentence
([below](#the-service-client-and-its-guard)): in the live window the day is `failed`
and backed off, in the history walk it is skipped. An interval whose `start` or `end`
is not an RFC 3339 time, or that does not end after it starts, fails the **whole
answer** instead, as a failed request.

The write is the file import's ([the model](#the-model)), with one addition for the
service (`planSpotPriceDaysWrite`): when a day's rows conflict with stored rows of
**another resolution**, for example the service's quarter-hours over the feed's hours,
and **every** stored row touching that day is fetched (`Feed` or `Service`), the
stored rows of the day are **deleted and the day inserted**, deletes first, in one
transaction under the area's lock. A fetched source is replaced by a better fetched
source. When any stored row touching the day is an `Import`, the day stays a conflict
and nothing of it is written — the sync leaves that day out and writes the others: an
operator's file is never overwritten, and removing the file's rows is the operator's
call. Nor is an imported interval of the **same** start and end: it stands, whatever
the service says of it, and is counted `unchanged`, so a file wins over the service
at any resolution. Nothing is ever written that the exclusion constraint would refuse,
so a day is all or nothing.

### The service client and its guard

The service is reached at a private address — `http://vantigo-spotprices:8080` on an
internal Docker network on our hosts — which the platform's outbound guard refuses to
every other caller. The client is separate from the feed's, and the exception it uses
is as narrow as it can be:

- **The dialler** is `netguard.DialContextAllowingPrivate`: the host is resolved and
  every address checked before any dial, and the connection made to the address
  checked, as for every guarded client — but an address `netguard.PrivateNetwork`
  accepts is allowed. That is RFC 1918 IPv4 (`10.0.0.0/8`, `172.16.0.0/12`,
  `192.168.0.0/16`) and RFC 4193 unique-local IPv6 (`fc00::/7`), an IPv4-mapped form
  unwrapped first, and nothing else: loopback, link-local and the `169.254.169.254`
  metadata address, carrier-grade NAT and every reserved range stay refused, and a
  private address embedded in a NAT64, 6to4 or IPv4-compatible IPv6 address is not
  seen through.
- **`http` only to a private address.** When the URL is `http` every address the host
  resolves to must be private, so the bearer key is never sent in the clear to a
  public address; over `https` a public or a private address is allowed.
- **Host-only.** A round-tripper refuses any request whose scheme and host are not the
  configured ones, before anything is dialled, so the exception cannot be reached
  through a redirect or by any other caller; such a refusal is recorded as an address
  the guard refuses.
- **No proxy, no redirect followed** (a `3xx` is a failure), a body of at most
  **1 MiB** for `/areas` and **4 MiB** for `/prices` once decompressed (the transport
  asks for gzip and decodes it) — a 35-day window at quarter-hours is 3 364 intervals,
  about 0.8 MB compact and 1 MB indented with every field at its widest, so 4 MiB is
  about four times the largest answer —, the feed's
  timeouts — **twenty seconds** for the whole request, within it ten to dial, ten for
  the TLS handshake and fifteen for the response's headers — and, unlike the feed,
  **kept connections** (at most four idle, for 90 seconds): the service is asked many
  times a cycle while history is copied. The transport is built per worker, since it
  carries the URL's scheme rule.
- **The key** goes as `Authorization: Bearer <key>` on every request, with
  `User-Agent: vantigo/<version>` and `Accept: application/json`. It is never in a log
  line, a problem or an error sentence.

A failure's sentence in `last_error` never quotes the body, the key or the address's
query. A failed request's sentence goes on the due days of the live window it asked
for, or on the history row of the walk or the gap; for `/areas` it is logged only; an
interval's sentence goes on its day alone. A failure is **permanent** when asking again
cannot mend it — a `400`, a body over the cap, an answer that is not a list, an
interval whose time Vantigo cannot read or that does not end after it starts — and
**transient** otherwise: a refused key, rate limiting, any other status, a network
error, a timeout, an address the guard refuses, a redirect. Only a history or gap
window is ever skipped for a permanent failure; the live window backs off on both.

| Failure | Sentence |
| --- | --- |
| `401` | the spot-price service refused the key |
| `429` | the spot-price service is rate limiting this installation — the cycle makes no further request, and its `Retry-After`, in seconds or as an HTTP date, either at most a day ahead, is the next attempt when it is later than the backoff |
| An `http` URL whose host resolves to a public address | the spot-price service's address is public; use https |
| Any other address the guard refuses | the spot-price service's address is not one Vantigo may fetch |
| A redirect | the spot-price service redirects; set ENERGY_SPOT_PRICE_SERVICE_URL to the address it serves |
| A timeout | the spot-price service did not answer in time |
| A network error | the spot-price service could not be reached |
| A body over the cap | the spot-price service's answer is larger than 1 MiB (`/areas`) or … larger than 4 MiB (`/prices`) |
| Another status | the spot-price service answered *status* *text*, such as `503 Service Unavailable` |
| An answer that is not a list of prices, or of areas | the spot-price service did not answer a list of prices (… of areas) |
| An interval whose `start` or `end` is not an RFC 3339 time, or that does not end after it starts (the whole answer) | an interval's time is not an RFC 3339 time with an offset (… an interval does not end after it starts) |
| An interval whose `source` or `rateSource` is not such a word, whose `state` is neither, or without an integer `version` (its day) | the spot-price service answered an unknown source (… an unknown state, … an interval without a version) |
| An interval without `priceEurPerMwh`, or with a figure Vantigo cannot store (its day) | an interval's price is not a number Vantigo can store |

The service's problem `detail` on a `400` is logged at debug level only, with the
request's path and never its query, and never stored.

**The log.** A live window read is logged at info with the area, its first and last
day, the days written and the inserted, replaced, unchanged and deleted counts; a
history or gap window at info with the area, which of the two, its first and last
day, the area's `days_skipped` so far and the same counts; a window skipped after its
third permanent failure at warning ("a window the spot-price service cannot answer was
skipped") with the area, which of the two, its days and the sentence. A failed request is logged at warning
("the spot-price service could not be read") with the area — empty for `/areas` —,
the service's host and the sentence, never the address's query, the key or the body;
a day of the live window recorded `failed` at warning with the area, the day and the
sentence. A cycle the database ended is logged at error.

### Importing prices

`POST /spot-prices/import?dryRun=true|false` imports prices from a file in one
request, in the consumption import's shape: no import record, no worker. Its rule is
`permission:energy:prices-manage+energy:prices-view`; it is rate-limited at 600 calls
an hour per client address (`EnergySpotPriceImport`, a budget of its own beside the
consumption import's), its body is capped at 10 MiB + 64 KiB, and the body is
multipart with one part, `file`; any other part is skipped.

**Reading.** `internal/fileimport` with the consumption import's limits: 10 MiB,
50 000 data rows, 50 columns; UTF-8 with or without a byte order mark, else
Windows-1252; the separator detected among `;`, `,` and tab; the first non-blank row
the header. A refused file is a `400` on `file` — no part named `file` or two of them,
an empty one, one past the limits, a line the reader cannot parse — as is a header
without an area, a start or a NOK price column, or with two columns naming one field.

**The layout** is fixed, unlike the consumption import's, whose files come from many
systems: there is no mapping part. Each column is found by its header as
`fileimport.HeaderKey` reads it (lowercased, letters and digits only, so `Price area`,
`price_area` and `PRICEAREA` are one word). Each field is taken from one column: two
columns naming the same field — the NOK price and the øre price count as one — are
the file's `400`, since which one is meant cannot be told. The header words:

| Column | Header words |
| --- | --- |
| Area — required | `area`, `price_area`, `pricearea`, `prisområde`, `elspotområde`, `område`, `sone` |
| Start — required | `start`, `from`, `fra`, `time_start`, `starttid` |
| End | `end`, `to`, `til`, `time_end`, `sluttid` |
| NOK price per kWh — required | `nok_per_kwh`, `price`, `pris`, `kr/kwh`, `nok/kwh`; or in øre, divided by 100: `øre/kwh`, `ore_per_kwh` |
| EUR price per kWh | `eur_per_kwh`, `eur/kwh` |
| Exchange rate | `exr`, `exchange_rate`, `kurs` |

**A row.**

- **The area** is read in any case and must be two letters and one or two digits
  (`no1` is `NO1`).
- **Timestamps** are read as the consumption import reads them
  ([Importing consumption](#importing-consumption)): ISO 8601, `d.m.yyyy h:mm` or
  `d/m/yyyy h:mm`; with an offset or `Z` that instant, without one a wall time in
  **Europe/Oslo**, whatever the row's area — the spring gap is the row's error and a
  repeated autumn time is the earlier instant the first time the area has it and the
  later one after.
- **Figures** — the NOK price, the EUR price and the exchange rate — are read as the
  consumption import reads a quantity, but signed: a decimal comma or a decimal point
  (a cell with both is refused), spaces dropped, no exponent; rounded to six decimals
  as `numeric(12,6)` stores them and less than 1 000 000 in size. A price may be
  negative; an exchange rate must be greater than zero. The EUR price and the rate
  may be blank. A price in øre is divided by 100 and rounded again.
- **The end.** With an end column, each row's end is read as the consumption import
  reads an end, and gaps between an area's rows are allowed, so one file may fill
  several separate days; a row that starts before the area's row before it in the
  file, or inside that row's interval, is the row's error, and an end not after the
  start is the end's. Without one, an interval closes at the next row's start **of
  the same area**, in file order, and an area's last row takes the length of the row
  before it (an hour when the area has a single row). A row is then the row's error
  when it is out of order, or when the next row of its area starts more than the
  previous interval's length after it.
- **On the market's grid.** With or without an end column, every interval is a
  quarter of an hour or an hour, as the market's are, starting on the quarter-hour,
  and an hour starts on the hour — so an interval never crosses an hour, a day or a
  month, which the summary's buckets rely on, and never blocks the feed's days. A
  start off the quarter-hour (minute 00, 15, 30 or 45, no seconds) is the start's
  error, "The start must be on the quarter-hour: minute 00, 15, 30 or 45", and the
  row gets no other; an interval of another length is "The interval must be a quarter
  of an hour or an hour", and an hour off the hour "An interval of an hour must start
  on the hour" — the end's error with an end column, the row's without one (where a
  wrong step also reaches the area's last row, which takes its length).
- **Several areas** may share a file; each area's rows are judged on their own.
- **Against what is stored**: judged and written under an advisory lock per area
  (`pg_advisory_xact_lock` on a hash of the area's name, areas in sorted order), which
  the feed worker takes too, so two writers of one area never race the exclusion
  constraint and the counts answered are exact. A row equal to a stored interval (the same area, start
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

### Prices over time: the summary

Years of quarter-hours are too many to chart or to send, so the Prices page reads a
month, a year or a range from a summary instead of the intervals
(`spot_prices_summary.go`, design S8).
`GET spot-prices/summary?area&bucket&from&to` (`getEnergySpotPricesSummary`, rule
`permission:energy:prices-view`) answers one area's intervals **starting** in
`[from, to)` grouped per bucket, ascending:
`{priceArea, bucket, timeZone: "Europe/Oslo", buckets: [{start, end, avgNokPerKwh,
minNokPerKwh, maxNokPerKwh, coveredMinutes, preliminary}]}`.

- **The buckets** are civil periods in **Europe/Oslo**, whatever the area: `day` from
  one local midnight to the next, so a clock-change day is 23 or 25 hours; `week` the
  ISO week from Monday's midnight; `month` from the first's midnight. `hour` is
  truncated on the **UTC** hour instead: Oslo's offset is whole hours, so it is the
  same hour, and the autumn night's 02:00, which comes twice, stays **two** buckets
  where a truncated local time would merge them. `start` and `end` are instants, the
  bucket's `[start, end)`, every bucket's written with Oslo's offset (the repeated
  hour's two as `02:00+02:00` and `02:00+01:00`). An interval belongs to the bucket
  it starts in; since every interval lies on the market's grid (the import refuses
  any other, see [Importing prices](#importing-prices)), none crosses a bucket.
- **The average is time-weighted**: `Σ(nok_per_kwh × length) / Σ(length)` over the
  bucket's intervals, rounded to six decimals. History before 2025-10-01 is hourly and
  after it quarter-hourly, and a plain mean would weigh each newer quarter-hour as much
  as an older hour. `minNokPerKwh` and `maxNokPerKwh` are the bucket's lowest and
  highest interval price.
- **`coveredMinutes`** is the minutes the bucket's intervals cover, less than the
  bucket's length where prices are missing, so a reader can mark an incomplete bucket.
  A bucket with no interval is **absent**, not zero.
- **`preliminary`** is true when any interval in the bucket is `preliminary`
  ([the model](#the-model)).
- One SQL statement over `energy.spot_prices`, grouped by
  `date_trunc(<bucket>, "start" AT TIME ZONE 'Europe/Oslo')` (`'UTC'` for `hour`); the
  unique index on `(price_area, "start")` serves the range.

**The checks**, every failing one reported at once in a `400` validation problem,
keyed by the field:

| Field | Refused when | Sentence |
| --- | --- | --- |
| `area` | not two uppercase letters and one or two digits | "Price area must contain two uppercase letters followed by one or two digits." |
| `bucket` | none of `hour`, `day`, `week`, `month` | "The bucket must be hour, day, week or month." |
| `from` | off the bucket's boundary (checked with a valid bucket) | "The from field must be a whole hour." (`hour`), "… a midnight in Europe/Oslo." (`day`), "… a Monday's midnight in Europe/Oslo." (`week`), "… the first of a month's midnight in Europe/Oslo." (`month`) |
| `to` | off the bucket's boundary, as `from`; else not after `from`; else wider than the bucket allows | the boundary's sentence; "The to field must be later than from."; "The to field must be at most *N* days after from for the *bucket* bucket." |

The window is measured in civil days on Oslo's calendar, so one across a clock change
is not a day short or long: `hour` at most **35** days, `day` **400**, `week` **1 830**
(five years) and `month` **10 980** (thirty years).

**On the page.** **Day** and **Week** read the intervals; **Month** and **Year** read
`day` buckets; **Range** reads `week` buckets for up to 24 months, widened to whole ISO
weeks, and `month` buckets beyond, at most 360 months. One request per compared area,
up to five. The figures above the chart are the first area's: the average weighs each
bucket's average by its `coveredMinutes`, which is the period's time-weighted average,
and the lowest and highest are the lowest `minNokPerKwh` and the highest
`maxNokPerKwh` with their bucket. The view — `areas` (a comma list of up to five),
`period` (`day|week|month|year|range`), `date` (`YYYY-MM-DD`), `from` and `to`
(`YYYY-MM`) — is the host route's search parameters
([`apps/host/frontend/src/routes/energy/prices.tsx`](https://github.com/vantigo-io/vantigo/blob/main/apps/host/frontend/src/routes/energy/prices.tsx));
a value that does not parse is dropped.

### Endpoints

All under `/api/v1/energy/`.

| Operation | Rule | What it does |
| --- | --- | --- |
| `GET spot-prices?area&from&to` | `permission:energy:prices-view` | The intervals of one area with `start ≥ from` and `start < to`, ascending, each with `id`, `priceArea`, `start`, `end`, `nokPerKwh`, `eurPerKwh` and `exchangeRate` when stored, `source` (`Feed`, `Service` or `Import`), `state` (`final` or `preliminary`) and, for a `Service` interval, `upstream`, `rateSource` and `upstreamVersion`. `area` must be two uppercase letters and one or two digits, `from` before `to`, and `to − from` at most **35 days** — a month at a quarter-hour resolution is one answer — otherwise a `400` (a window too long on `to`). |
| `GET spot-prices/coverage` | `permission:energy:prices-view` | `source` — where the worker fetches from: `service` when `ENERGY_SPOT_PRICE_SERVICE_URL` is set, `feed` when `ENERGY_SPOT_PRICE_FEED_ENABLED` is on, `none` (files only) otherwise — and `areas`: per area in use or already priced, and with the service every area it follows (those with a history row), in area order: `priceArea`, `firstStart` and `lastEnd` (absent with no prices), `intervalCount` (the intervals stored, `0` with none), `daysMissingLast30` — the Europe/Oslo days among the last thirty, today included, with no interval starting in them, `30` with no prices — `lastFetch`, the fetch record of the worker's most recent attempt for the area, by `attemptedAt` (`day`, `status`, `attempts`, `attemptedAt`, `nextAttemptAt`, `error`), absent when it never tried one, and `resolution`, the length of the area's latest stored interval as an ISO 8601 duration (`PT15M`, `PT60M`), absent with no prices. With the service as the source, once the sync has written any history row: `followedByService`, whether the area has one marked `active` (false for no row, or a row the service stopped following), and for an area with a row, active or not, `history` — what was copied stays visible after the service stops following the area — with `floorDay` and `cursorDay` (absent until known), `complete` (the cursor at or before the floor; false while no floor is known, as before the service has answered a coverage for the area, which the Prices page shows as waiting for the service's coverage), `daysSkipped` and `error`, the last failed window's sentence, kept after a skipped window until the next window is answered. Before the sync's first cycle, and with another source, both are absent. |
| `GET spot-prices/summary?area&bucket&from&to` | `permission:energy:prices-view` | One area's prices per civil hour, day, ISO week or month in Europe/Oslo — each bucket's time-weighted average, lowest, highest, covered minutes and whether it holds a preliminary price — over a window on the bucket's boundaries and within its limit ([above](#prices-over-time-the-summary)); otherwise a `400` naming the field. |
| `POST spot-prices/import?dryRun` | `permission:energy:prices-manage+energy:prices-view` | Imports a file of prices ([above](#importing-prices)). |

The coverage is read from the database alone, so the API process answers it the same
in a split deployment, where the worker runs elsewhere.

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
`energy:metering-points-manage` and `energy:meters-manage` with both views. The
Prices page needs `energy:prices-view`, and its **Import prices** also
`energy:prices-manage`.

## Workers

| Worker | What it does |
| --- | --- |
| `energy-import` | Checks, runs and attaches metering-point imports; sweeps old ones ([above](#the-worker-energy-import)) |
| `energy-spot-prices` | Copies the day-ahead prices and their history from the spot-price service when `ENERGY_SPOT_PRICE_SERVICE_URL` is set ([above](#the-service-sync)), or fetches those of the areas in use from the public feed when `ENERGY_SPOT_PRICE_FEED_ENABLED` is on ([above](#the-feed-worker-energy-spot-prices)) — never both |
