---
title: "Energy module"
description: "Metering points, meters, supply periods, consumption, the metering-point import and permissions."
sources:
  - apps/server/internal/energy
  - openapi/energy.yaml
  - apps/server/internal/db/migrations/00005_energy_baseline.sql
  - apps/server/internal/db/migrations/00049_energy_imports.sql
---
The Energy module keeps the metering points an energy supplier supplies: where each
one is, which meter sits on it, which customer it is supplied to and for how long, and
the consumption read from it. It is served under `/api/v1/energy/*`
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
  (trimmed, blank stored as none), an optional `expectedAnnualConsumptionKwh`
  (`numeric(14,3)`, not negative), optional `latitude` (−90 to 90) and `longitude`
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
  `quality` and `source`, in a table partitioned by month.

## Supply periods and the no-overlap rule

No two supply periods of one metering point that are not `Cancelled` may overlap,
the ranges read as `[start, end)` with no end as for ever. A GiST exclusion constraint
(`supply_periods_no_overlap`) decides it; the endpoints run a friendly pre-check in
front of it and answer `409` "Overlapping supply period" when it fails. A write that
races past the pre-check meets the constraint and is a generic `409`. Every write to
`supply_periods` takes `lockSupplyPeriods`, a transaction-scoped advisory lock keyed by
the metering point, so concurrent writers on one point go through the constraint one
at a time.

- **Create** (`POST …/supply-periods`) — `customerId` > 0, a UTC `start`, the point
  (404), the customer through the directory (an unknown one is a `400` on
  `customerId`), the overlap pre-check (`409`), then an open-ended `Active` period.
- **Switch** (`POST …/supply-periods/switch`) — with an open `Active` period: the same
  customer is a `400` on `customerId`, a `switchAt` at or before its start a `400` on
  `switchAt`, and otherwise it ends at `switchAt` and the new customer's period starts
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
  points at it. Nothing else is checked for overlap.
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
| `gridArea` | Optional; a blank cell clears it. |
| `expectedAnnualConsumptionKwh`, `latitude`, `longitude` | Optional; a blank cell clears it. Spaces dropped (thousands), a decimal comma read as a point; the endpoint's ranges. |
| `connectionStatus` | `New`, `Connected`, `Disconnected` in any case, and `ny`, `tilkoblet`, `frakoblet`. A blank cell is `New` on a create and keeps the point's on an update. |
| `customerLegalId` | The customer's organisation number: whitespace dropped, nine digits with a valid mod-11 check digit, looked up in country `NO`. |
| `supplyStart` | A date — `2026-01-01`, `01.01.2026` or `01/01/2026`, always day first — read as midnight UTC. Needs `customerLegalId` mapped. Ignored on a row with no organisation number. |

A mapped field is written and an unmapped one left as it is. Each row's point is
judged whole by the endpoints' own validator (`validateMeteringPointFields`,
`validateMeterNumber`), the point's own values laid under the row's on an update, and
an update that changes nothing writes nothing. A field a create needs and the mapping
lacks is said in those words (*"A new metering point needs a price area, and no
priceArea column is mapped"*).

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

`POST …/check` queues the dry run: every row runs exactly as the real run would, each
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
`done` import, from `start` (a UTC timestamp): the import goes to `attaching` and the
worker reads each pending point's `Active` period **again, now** — none, and C's
period starts at `start`; C's already (attached by hand meanwhile), and the row becomes
`same` with nothing written; another customer's, and it is switched at `start`.
`confirmSwitches` must equal the number of pending points whose active period is
another customer's at the moment of the request (`409 import_switch_not_confirmed`
otherwise), and the attach switches no more than that. The customers the rows name are
read once through the directory before the first row; one archived or merged away
since is refused. Each row becomes `attached` (with `switch_from` when it switched) or
stays `pending` with its error, so the attach can be asked for again, with another
date, until nothing is pending. `pendingSwitches` on the import counts the pending rows
the run wrote as pending switches.

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
then the sweep. A whole-run failure is written to `error` on the import and retried
next cycle. `module.Workers` hands the worker the customers module's directory as
`Compose` hands it to the handlers, so worker mode resolves customers too.

| Setting | Default | Meaning |
| --- | --- | --- |
| `ENERGY_IMPORT_ENABLED` | `1` | `0` → the worker is never handed to the runner; an import queues and waits |
| `ENERGY_IMPORT_POLL` | `5s` | how often a cycle looks for a queued import |
| `ENERGY_IMPORT_RETENTION` | `720h` | how long a finished or abandoned import and its rows are kept before the sweep removes them |

In the default deployment the check, run and attach handlers also nudge the worker
in-process; in a split deployment the poll is the bound.

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
