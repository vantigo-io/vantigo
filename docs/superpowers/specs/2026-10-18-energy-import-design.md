# Energy import from a file — design

An energy supplier moving to Vantigo arrives with an export from the system it leaves:
one line per metering point, with the GSRN, the meter number, the installation address,
the price area and — as the one link to who is supplied — the customer's
**organisation number**. Today every point is typed in by hand. This delivery adds two
imports:

1. **Metering points**, a record-keeping import in the customers import wizard's shape
   ([`2026-10-13-customers-import-wizard-design.md`](2026-10-13-customers-import-wizard-design.md)):
   upload, map columns, check, import, with a background worker. A row may name a
   customer by organisation number, and may say from when the point is supplied.
2. **Consumption**, one file for one metering point, from a button on that point's
   page: small enough to check and write in a request, all or nothing.

Consumption is not part of the metering-point import. A metering-point export and a
consumption export are different files with different shapes (one line per point;
one line per interval, thousands per point), usually from different screens of the
old system, and consumption is only meaningful once the point exists.

## Decisions

### D1 — The customer link is a supply period, and the import may leave it for later

A metering point has no customer column. The only link is `energy.supply_periods`
(`customer_id`, `start`, `end`, `status`, no two non-cancelled periods on one point
overlapping). Linking a point to a customer therefore needs a **start**, and most
exports carry none. So the import separates the two things a row says:

- **The point** is always written (created or updated) by its row.
- **The customer** is resolved from the organisation number and kept on the row
  (`import_rows.customer_id`). Whether a supply period is written depends on the start:
  - **a start mapped** (a column, or one fixed date for every row): the period is
    written in the row's transaction — assigned, or switched (D4);
  - **no start**: no period is written. The point is imported without one and the row
    is **pending**: matched to its customer, not yet attached. After the import, the
    result offers to **attach the pending points** to their customers from a date the
    person picks then (D6).
- **No organisation number** (the column unmapped, or the cell blank): the point is
  imported with no customer. It is attached later, one by one with the existing
  **Assign customer**, found through the metering-point list's new **Without customer**
  filter (D9). A blank cell never ends or changes an existing supply period.

### D2 — Reading the file is shared, the import stays the module's

`customers/imports_read.go` (encoding, separator, quoting, the formula guard, the caps)
and the header normalisation of `imports_suggest.go` (`headerKey`) move to a new
platform package, **`internal/fileimport`**, which both modules import. It holds no
SQL and no tables, as `internal/payments` holds none (module boundaries rule 4): each
module keeps its own `imports` and `import_rows`, its own worker and its own
endpoints. The move is its own commit, a refactor with no behaviour change
(`Docs-Impact: none`), apart from `contributing/module-boundaries.md` naming the new
platform package in rule 1's list.

The worker loop is not shared in this delivery: its shape (lease, chunks, nudge,
retention) is copied, its row side is the module's. Two copies are the evidence a
shared runner would need; a third importer is when to extract one.

### D3 — The metering-point import: record, fields, match

Migration `00048_energy_imports.sql`: `energy.imports` as `customers.imports` (D1 of the
customers design, same columns, same states, same counts), plus `switched integer`
and `pending integer`; `energy.import_rows` as `customers.import_rows` with
`metering_point_id integer` in place of `customer_id`, plus:

```sql
    customer_id  integer,       -- the customer the row's organisation number resolved to
    link         varchar(10),   -- none | same | assigned | switched | pending | attached
    switch_from  integer,       -- the customer the point was supplied to before a switch
    CONSTRAINT ck_import_rows_link CHECK (link IS NULL OR link IN
        ('none', 'same', 'assigned', 'switched', 'pending', 'attached'))
```

`customer_id` is an opaque id (rule 4), named through `contracts.CustomerDirectory`.

**Fields**, each validated by the validator its endpoint uses:

| Field | Notes |
| --- | --- |
| `gsrn` | Required. The match key; 18 digits, the existing check. |
| `meterNumber` | Required on create (the point's first meter). On an update a different number is the row's error: *"The meter number differs from the current meter's; use Replace meter."* A meter is not replaced by import. |
| `streetAddress`, `postalCode`, `city`, `countryCode` | `countryCode` blank or unmapped is `NO`. |
| `priceArea` | Required on create. `NO 1`, `no1` and `NO1` all read as `NO1`. |
| `gridArea`, `expectedAnnualConsumptionKwh`, `latitude`, `longitude` | Optional. A decimal comma is read as a point. |
| `connectionStatus` | `New`, `Connected`, `Disconnected`, plus `ny`, `tilkoblet`, `frakoblet`. |
| `customerLegalId` | The customer's organisation number. Whitespace dropped, country `NO`. |
| `supplyStart` | A date (`2026-01-01`, `01.01.2026`, `01/01/2026` — always day first), midnight UTC, as the Assign customer dialog. Needs `customerLegalId`. |

**Modes** are the customers import's three: `create`, `update`, `createOrUpdate`, always
matched by GSRN. A mapped field is written, an unmapped one left as it is, a blank mapped
cell clears an optional field, as in the customers design D3. Two rows with one GSRN are
refused on the second. A row whose GSRN is blank and which has at most one other
non-blank cell is a **totals line** (exports end with one: a summed annual consumption
under an otherwise empty row) and is `skipped`, counted and listed as such, never
failed. **Suggestions** use the same three tiers; the alias table adds
`målepunkt`, `målepunktid`, `meteringpointid`, `gsrn` → `gsrn`; `målernummer`,
`målernr`, `meterno` → `meterNumber`; `anleggsadresse`, `adresse` → `streetAddress`;
`postnranlegg`, `postnummeranlegg`, `postnr`, `postnummer` → `postalCode`;
`poststedanlegg`, `poststed` → `city`; `prisområde`, `elspotområde` → `priceArea`;
`netteier`, `nettområde`, `nettselskap` → `gridArea`; `årsforbruk`,
`forventetårsforbruk`, `forventetårsforbrukkwh` → `expectedAnnualConsumptionKwh`;
`orgnr`, `organisasjonsnummer`, `kundeorgnr` → `customerLegalId`; `oppstart`,
`startdato`, `startdatokunde`, `leveringsstart` → `supplyStart`. Where a field has an
installation-specific alias (`… anlegg`) and a plain one, the installation alias wins
whatever the column order: a supplier's export carries the customer's postal address
(`POSTNR`, `POSTSTED`) beside the installation's (`POSTNR ANLEGG`, `POSTSTED ANLEGG`),
and the point's address is the installation's.

The file this was checked against (a supplier's export of active business customers,
194 points, 93 customers, 19 columns) maps itself fully under these aliases except
`KOMMUNE ANLEGG` and `Norgespris fra`, which have no field and stay unmapped, and the
customer columns (`KUNDENR`, `KUNDENAVN`, `EPOST`, …), which belong to the customers
import.

### D4 — Resolving the customer, and switching one

**The contract.** `contracts.CustomerDirectory` gains

```go
// CustomersByLegalID finds the customers holding each organisation number in ids,
// in one round trip, keyed by the id as given. An id nobody holds is absent.
CustomersByLegalID(ctx context.Context, country string, ids []string) (map[string][]CustomerEntry, error)
```

backed by the customers module's existing `CustomersByLegalIdentity` query and index.
The worker resolves **every distinct organisation number of the file once, before the
first row**: nothing leaves the database while a row's transaction is open, as in the
customers import.

**A row's link**, given the customer C its number resolved to, the start S, and the
point's active supply period P:

| Situation | Outcome |
| --- | --- |
| No organisation number | `none`; P untouched |
| Number not valid, nobody holds it, or more than one customer does | the row's error on that column, naming the numbers when ambiguous; the point is not written |
| C archived or merged away | the row's error (*"merged into 10234"*, or *"archived"*) |
| P is already C's | `same`; nothing written, whatever S says |
| No P, S mapped | `assigned`: a period from S |
| No P, no S | `pending` |
| P another customer's, S mapped | **`switched`**: P ends at S and C's period starts at S, the existing switch, in the row's transaction; S must be after P's start, otherwise the row's error |
| P another customer's, no S | `pending`, with `switch_from` set: attaching it later is a switch (D6) |

A period that would overlap an ended or later one is the row's error with the switch
dialog's own message. Every write goes through the functions the supply-period
endpoints call, under `lockSupplyPeriods`, with their events, the uploader as actor.

**The switch warning.** A switch takes a point away from a customer who is supplied
today, which the person may not have meant: the export may be older than the last
switch made by hand. So:

- The check counts `switched` (and pending switches) separately, and the **Check**
  step shows, above everything else, a red alert: *"12 metering points will switch
  customer. Each one's current supply period ends on the row's start date and the
  customer in the file takes over."*, with a table of GSRN, current customer, new
  customer and date.
- **Import** then opens a confirmation that repeats the number and is only enabled
  once the person ticks *"I understand that 12 metering points will switch
  customer."*
- The server holds the same line: `POST …/run` takes `{ "confirmSwitches": 12 }`, and
  a run whose check counted switches without that exact number is a 409
  `import_switch_not_confirmed`. A script cannot switch customers without saying how
  many it expects to.
- The finished import lists the switched rows with both customers as links.

### D5 — Permission: `energy:import`

| Key | Display | Description | Sensitive |
| --- | --- | --- | --- |
| `energy:import` | Import energy data | Import metering points and consumption from a file; each thing written needs the permission it needs by hand. | yes |

The metering-point import's operations are `permission:energy:import+energy:metering-points-view`.
The content is checked when the mapping is saved, as in the customers design D4:
creating or updating needs `energy:metering-points-manage`; mapping `customerLegalId`
needs `energy:supply-periods-manage`. Attaching (D6) needs
`energy:supply-periods-manage`. The consumption import needs `energy:import` and
`energy:consumption-manage+energy:consumption-view`.

### D6 — Lifecycle, worker, and attaching the pending points

States as the customers import: `draft → checking → checked → importing → done`, a dry
run that is the real run rolled back row by row, stop, delete, a row's outcome written
inside its transaction, resume by row.

**Attaching** adds one transition: `done ──attach──▶ attaching ──(worker)──▶ done`.
`POST /energy/imports/{id}/attach` with `{ start, confirmSwitches }` attaches every
`pending` row: a period for the row's customer from `start`, or a switch from that
date when the point is now another customer's (re-read at attach time, not trusted
from the import). Pending switches need `confirmSwitches` as the run does, with the
same warning on the page. Each row becomes `attached` or keeps `pending` with its
error, so attaching can be retried, with another date, until nothing is pending.
A point attached by hand in the meantime is `same` and skipped.

**The worker** `energy-import`: the customers import worker's shape, lease key
`ENRGIMP1`, chunks of 500, the in-process nudge, settings `ENERGY_IMPORT_ENABLED`
(default on), `ENERGY_IMPORT_POLL` (5 s) and `ENERGY_IMPORT_RETENTION` (30 days).
Caps as the customers import: 50 MiB, 100 000 rows, 200 columns.

### D7 — Metering-point import endpoints

All `permission:energy:import+energy:metering-points-view`, under `/api/v1/energy/`:

| Operation | What it does |
| --- | --- |
| `POST imports` (multipart `file`) | Reads, stores, answers 201 `EnergyImport` with `suggested` |
| `GET imports` | Newest first, paged |
| `GET imports/{id}` | State, file facts, header, sample with errors, mapping, counts (`created`, `updated`, `failed`, `switched`, `pending`), progress, times |
| `PUT imports/{id}/mapping` | D3/D5; `draft`/`checked` only |
| `POST imports/{id}/check` | `draft` → `checking` |
| `POST imports/{id}/run` | `checked` → `importing`; `confirmSwitches` (D4) |
| `POST imports/{id}/attach` | `done` with pending rows → `attaching` (D6) |
| `POST imports/{id}/stop` | as the customers import |
| `DELETE imports/{id}` | not running |
| `GET imports/{id}/rows?outcome=&link=` | rows with cells, outcome, link, point, customer and previous customer |
| `GET imports/{id}/failed-rows` | `text/csv`, the file's own header with an `error` column first |

Conflict codes: `import_running`, `import_not_checked`, `import_nothing_to_import`,
`import_finished`, `import_switch_not_confirmed`, `import_nothing_pending`.

### D8 — The consumption import, one metering point at a time

A button **Import readings** in the metering point's **Consumption** card opens a
modal. One endpoint, called twice:

`POST /api/v1/energy/metering-points/{id}/consumption/import` — multipart `file` and a
`mapping` part, `?dryRun=true|false`.

- **Mapping**: `start` (required), `end` **or** `resolution` (`PT1H`, `PT15M`: end is
  start plus the resolution, since many exports give one timestamp per line),
  `quantityKwh` (required), `quality` (optional, `Measured` when unmapped), and an
  optional `gsrn` column. When `gsrn` is mapped every row must name **this** point: a
  file for another point is refused row by row, which catches the wrong file before
  anything is written.
- **Reading**: `internal/fileimport`, at most **50 000 rows** (more than five years of
  hourly values, a year of quarter-hours) and 10 MiB. Timestamps with an offset are
  read as such; without one they are **local time in the point's market time zone**
  (the zone the aggregation already derives from the price area), with the DST rules
  stated: a repeated hour in October is read as its first occurrence then its
  second, in file order; the skipped hour in March is the row's error. `dd.mm.yyyy
  hh:mm` and ISO 8601 are read; a decimal comma is a point.
- **Writing**: the manual reading's own rules, source **`Import`** (a new value of the
  `source` column, beside `Elhub` and `Manual`). A row with the same start and end as a
  current interval and the same quantity and quality is **unchanged** and writes
  nothing; a different one supersedes it, the old one kept as history. Two rows of the
  file with one interval are the second's error.
- **All or nothing.** A dry run answers the counts (new, replacing, unchanged), the
  first 200 errors, and the range the file covers. The real run writes every row in
  one transaction, or nothing when any row has an error. No record, no worker:
  50 000 rows is seconds.

The modal: choose the file → columns suggested (`fra`, `fra og med`, `start`,
`from` → `start`; `til`, `end`, `to` → `end`; `kwh`, `forbruk`, `volum`, `quantity`
→ `quantityKwh`; `kvalitet` → `quality`; `målepunkt`, `gsrn` → `gsrn`) and editable,
with the resolution choice when no end column is mapped → **Check** (the dry run,
showing the counts, the range and the errors) → **Import**, which says how many
readings it will add and replace.

### D9 — Frontend

- **`/energy/metering-points/import`** and **`/energy/metering-points/import/$importId`**
  in `@vantigo/energy-ui`, host routes as thin wrappers; the customers import's page
  structure (dropzone and recent imports; stepper **Map columns · Check · Import**
  derived from the state, polling while running). Where the components are generic
  (the mapping table, the progress, the errors table) they move to a shared UI package
  rather than being copied; where they are customer-specific they stay.
- **Check** adds the switch alert (D4). **Import**'s confirmation has the switch
  checkbox. **Done** adds an **Attach to customers** panel when rows are pending: the
  count, a date picker, the switch warning for the pending switches, and **Attach**.
- The metering-point list: **Import** in the page header (behind `energy:import` and
  `energy:metering-points-manage`), and a **Without customer** filter (no active supply
  period), served by a new `withoutCustomer=true` parameter on
  `GET /energy/metering-points`.
- The metering point page: **Import readings** in the Consumption card (D8).
- Strings in en and nb.

### D10 — Docs and roadmap

- `docs/src/content/docs/{en,nb}/user/energy.md`: **Import metering points** (the three
  steps, the customer link, the switch warning, attaching pending points afterwards,
  the Without customer filter) and **Import readings**; the permission table gains
  `energy:import`.
- `docs/src/content/docs/en/reference/energy.md`: there is no energy reference page
  today. A new one, listed in the reference overview, covering at least the two
  imports' rules (D1, D3, D4, D6, D8), the permission and the worker; the rest of the
  module described from the code as far as this delivery reaches.
- `docs/src/content/docs/{en,nb}/admin/installation.md`: the worker and its three
  settings.
- `docs/src/content/docs/en/contributing/module-boundaries.md`: `internal/fileimport`.
- `ROADMAP.md`: Energy gains the import as a delivery.

## Delivery order

1. `internal/fileimport`, customers moved onto it (refactor).
2. `CustomersByLegalID` on the directory.
3. The metering-point import: migration, endpoints, worker, attach, the list filter.
4. The frontend for it.
5. The consumption import, server and modal.
6. Documentation pages, en and nb, with each step's own commit where the step changes
   what a user sees.

## Out of scope

Creating customers from the metering-point file (import customers first); matching a
customer by customer number or birth number; replacing a meter by import; ending a
supply period by import; the point's municipality and its Norgespris enrolment date
(real columns in suppliers' exports; Norgespris matters to billing and belongs with
the invoicing groundwork, Roadmap phase 4); importing consumption for many points from one file (the
`gsrn` column is the seam for it later); Excel files; Elhub (Roadmap phase 5).

## Testing

**Backend.** `fileimport`: the customers reading tests moved, unchanged. Metering-point
import: each field's validation and default; create, update and createOrUpdate by
GSRN; a different meter number refused; the in-file duplicate; every row of D4's link
table, through the directory fake and through the real customers module in the
integration test; customers resolved once per run; a switch refused before P's start
and on overlap; the dry run keeping no point, period or event; `confirmSwitches`
missing, wrong and right; attach (assign, switch re-read at attach time, a point
attached by hand meanwhile, a retry after an error); the lifecycle and worker tests
of the customers import on the new path; permissions per mapped field; the
`withoutCustomer` filter. Consumption import: offset and local timestamps, both DST
days, the resolution instead of an end, decimal comma, the wrong point's GSRN,
unchanged / replacing / new, a file duplicate, all or nothing, the caps.

**Frontend.** The two import pages as the customers import's tests; the switch alert
and the checkbox gating Import; the attach panel; the filter; the readings modal's
steps.

**Gates**: `mise run server:check`, `server:test`, `frontend:check`, `docs:check`.
