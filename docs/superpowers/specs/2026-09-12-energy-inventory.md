# Energy module — behavioural inventory for the Go port

Input to the Go port of `apps/energy/backend/Energy.Module/` (~3.8k lines). Describes what the .NET module *does*
beyond `apps/server/internal/openapi/specs/energy.yaml` (20 operations), so the port reproduces it faithfully.
The port is single-tenant: every `tenant_id` column, the `ITenantOwned`/RLS machinery, and `MapTenantGroup` are
**dropped** — noted where they appear, not described in depth.

Path abbreviations: `EP/` = `apps/energy/backend/Energy.Module/Endpoints/`, `DM/` = `…/Energy.Module/Domain/`,
`DB/` = `…/Energy.Module/Database/Energy/`, `AZ/` = `…/Energy.Module/Authorization/`,
`TS/` = `apps/energy/backend/Energy.Module.Tests/`, `CT/` = `packages/contracts/Vantigo.Contracts/`,
`HOST/` = `apps/host/backend/Vantigo.Host/`. "unverified" = not provable from repo source (framework internals).

Contract access mix (20 ops, all `permission:` — no `policy:` or `anonymous` in this module): every op requires
one or more `energy:*` permission keys ANDed together (`+`-joined in the contract). Route base:
`/api/v{version:apiVersion}/energy` via `NewVersionedApi().MapTenantGroup(...)`, `ApiVersion(1)`
(`EP/VersionedBusinessEndpointExtensions.cs:9-18`; `MapTenantGroup` is the tenancy wrapper, dropped).

**Route/contract parity: exact.** 20 `.Map*` calls in the three endpoint groups (`EP/MeteringPointsEndpoints.cs`:14,
`EP/CustomerEnergyEndpoints.cs`:3, `EP/EnergyStatsEndpoints.cs`:3) = 20 contract `operationId`s. No orphan either
direction. Every `.RequirePermission(...)` set on a route matches its `x-vantigo-access` clause exactly (verified
permission-by-permission below); this module has no contract/permission drift.

## 1. Endpoints

### 1.1 Metering points (`EP/MeteringPointsEndpoints.cs`, group `/metering-points`)

| Method/path | operationId | Permissions (AND) | Handler | Success | Errors (order checked) |
|---|---|---|---|---|---|
| GET `` | getEnergyMeteringPoints | `metering-points-view`, `meters-view` | `GetMeteringPointsEndpoint.Handler` (`EP/MeteringPoints/GetMeteringPointsEndpoint.cs:12`) | 200 `PaginatedResponse<MeteringPointResponse>` | 400 plain `ProblemDetails` if `page<1` or `pageSize` not in 1..100 (`:15-16`). **Contract also declares 404 (`energy.yaml:685-686`) but the handler's `Results<Ok,ProblemHttpResult>` can never produce one** — contract/code mismatch. |
| POST `` | postEnergyMeteringPoints | `metering-points-manage`, `metering-points-view`, `meters-manage`, `meters-view` | `CreateMeteringPointEndpoint.Handler` (`.../CreateMeteringPointEndpoint.cs:13`) | 201 Created, `Location` via route name `GetEnergyMeteringPoint` | 1) field validation → 400 `ValidationProblem` (`:16-17`); 2) duplicate GSRN → 409 plain `Problem` (`:18-19`, title "Duplicate GSRN") |
| GET `/{id}` | getEnergyMeteringPoint | `metering-points-view`, `meters-view` | `GetMeteringPointEndpoint.Handler` | 200 or 404 `NotFound` | single lookup, no validation |
| PUT `/{id}` | putEnergyMeteringPointsById | `metering-points-manage`, `metering-points-view`, `meters-view` (**not** `meters-manage` — PUT never touches meters) | `UpdateMeteringPointEndpoint.Handler` | 200 | 1) field validation → 400 (`:14-15`); 2) lookup → 404 (`:16-17`); 3) duplicate GSRN (excluding self) → 409 (`:18-19`). Order: **validate before existence check.** |
| GET `/{id}/meters` | getEnergyMeteringPointsByIdMeters | `meters-view` | `GetMetersEndpoint.Handler` | 200 list ordered by `InstalledAt` asc | 404 if metering point missing |
| POST `/{id}/meters` | postEnergyMeteringPointsByIdMeters | `meters-manage`, `meters-view` | `ReplaceMeterEndpoint.Handler` | 201 | 1) existence → 404 (`:15`); 2) field validation → 400 (`:16-17`); 3) inside a transaction, `installedAt <= active.InstalledAt` → 400 `ValidationProblem` field `installedAt` (`:22-27`), **not** a 409 even though it is a business conflict. Order: **existence before validation** (opposite of PUT above). No contract 409 for this op — matches. |
| GET `/{id}/consumption` | getEnergyMeteringPointsByIdConsumption | `consumption-view` | `GetConsumptionEndpoint.Handler` | 200 list, current revision only, ordered by `Start` | 1) `to<=from` (both given) → 400 plain `Problem` (`:14-15`); 2) existence → 404 (`:16`). Order: **interval sanity before existence.** |
| POST `/{id}/consumption` | postEnergyMeteringPointsByIdConsumption | `consumption-manage`, `consumption-view` | `AddManualConsumptionEndpoint.Handler` | 200 (not 201 — contract agrees, this "add" is really an upsert) | 1) `ConsumptionInterval.Validate` → 400 `ValidationProblem` field `interval` (`:15-16`); 2) existence → 404 (`:17`). Order: **validate before existence**, same pattern as consumption GET below. |
| GET `/{id}/consumption/aggregate` | getEnergyMeteringPointsByIdConsumptionAggregate | `consumption-view` | `GetConsumptionAggregateEndpoint.Handler` | 200 list of buckets | 1) `ConsumptionAggregateValidation.TryValidate` → 400 `ValidationProblem`, **can carry multiple field errors at once** (`from`, `to`, `resolution` are independent `if`s, `EP/Consumption/ConsumptionAggregateValidation.cs:15-20`); 2) existence → 404 (`GetConsumptionAggregateEndpoint.cs:23-27`). |
| GET `/{id}/supply-periods` | getEnergyMeteringPointsByIdSupplyPeriods | `supply-periods-view` | `GetSupplyPeriodsEndpoint.Handler` | 200 ordered by `Start` | 404 if metering point missing |
| POST `/{id}/supply-periods` | postEnergyMeteringPointsByIdSupplyPeriods | `supply-periods-manage`, `supply-periods-view` | `CreateSupplyPeriodEndpoint.Handler` | 201 | 1) `customerId<=0` → 400 (`:16`); 2) `SupplyPeriod.Validate(start,null)` (UTC-offset check only, since end is null) → 400 (`:17-18`); 3) metering point existence → 404 (`:19`); 4) `ICustomerDirectory.FindCustomerAsync` miss → **400** `ValidationProblem` (not 404) field `customerId` (`:20-21`); 5) overlap pre-check → 409 plain `Problem` (`:22-24`, title "Overlapping supply period"). |
| POST `/{id}/supply-periods/switch` | postEnergyMeteringPointsByIdSupplyPeriodsSwitch | `supply-periods-manage`, `supply-periods-view` | `SwitchSupplyPeriodEndpoint.Handler` | 201, body `{endedPeriod, newPeriod}` | 1) metering point existence → 404 (`:17`) **first**, unlike Create above; 2) `customerId<=0` → 400 (`:18-21`); 3) `SwitchAt` UTC check → 400 (`:22-24`); 4) customer lookup miss → 400 (`:25-28`); then inside a transaction: if an open active period exists — same customer → 400 (`:36-39`); `switchAt <= active.Start` → 400 (`:40-43`); else (no open period) overlap pre-check → 409 (`:50-56`). |
| POST `/{id}/supply-periods/{periodId}/end` | postEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd | `supply-periods-manage`, `supply-periods-view` | `EndSupplyPeriodEndpoint.Handler` | 200 | 1) lookup by `(periodId, meteringPointId)` → 404 (`:15-16`); 2) `Status==Cancelled` → **400 plain `Problem`** (`:17`, title "Invalid supply period") — **contract says this op's 400 is `HttpValidationProblemDetails`, but this branch returns the plain `ProblemDetails` shape, not a `ValidationProblem`; the same endpoint's other 400 (bad end date, `:18-19`) does return `ValidationProblem`. Two different 400 bodies from one operation.** |
| DELETE `/{id}/supply-periods/{periodId}` | deleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId | `supply-periods-manage` | `CancelSupplyPeriodEndpoint.Handler` | 204 | 404 if not found. **No status guard**: an already-`Ended` (or already-`Cancelled`) period can be cancelled again unconditionally (`CancelSupplyPeriodEndpoint.cs:15`) — the state machine has no "cannot cancel a closed period" rule. |

### 1.2 Customer-scoped energy (`EP/CustomerEnergyEndpoints.cs`, group `/customers`)

These decouple from the Customers module entirely through `CT/ICustomerDirectory.cs:3-25` (`FindCustomerAsync`,
`FindContactAsync`, optional `FindByEmailAsync`) — Energy never references Customers types directly; the host wires
the real `CustomerDirectory` (`apps/customers/backend/Customers.Module/Services/ApplicationServiceCollectionExtensions.cs:16-20`)
and `ModuleCompositionValidator.cs:22` refuses to activate Energy without Customers. Only `CreateSupplyPeriodEndpoint`
and `SwitchSupplyPeriodEndpoint` actually call the directory (to validate `customerId`); the three endpoints below
**never** call it — they trust `customerId` as an opaque foreign key and return empty results for an unknown one.

| Method/path | operationId | Permissions | Handler | Notes |
|---|---|---|---|---|
| GET `/{customerId}/metering-points` | getEnergyCustomersByCustomerIdMeteringPoints | `metering-points-view`, `meters-view`, `supply-periods-view` | `GetCustomerMeteringPointsEndpoint.Handler` | Joins metering points to non-cancelled supply periods for that customer (`:17-30`). No existence check on `customerId`; unknown id → `[]`, always 200. No 400/404 in contract either — matches. |
| GET `/{customerId}/consumption` | getEnergyCustomersByCustomerIdConsumption | `consumption-view` | `GetCustomerConsumptionEndpoint.Handler` | Filters via `CustomerConsumptionFilter.CurrentIntervals` (§2.4). Optional `meteringPointId` query narrows to one point. `to<=from` → 400 plain `Problem` (`:15-16`). |
| GET `/{customerId}/consumption/aggregate` | getEnergyCustomersByCustomerIdConsumptionAggregate | `consumption-view` | `GetCustomerConsumptionAggregateEndpoint.Handler` | Same `ConsumptionAggregateValidation` as the metering-point version; **loops per matching metering point issuing one raw-SQL query each** (`:32-38`, N+1 by design), then merges and sorts by `(meteringPointId, bucketStart)` in C# (`:40-43`). |

### 1.3 Dashboard stats (`EP/EnergyStatsEndpoints.cs`, mounted directly, no sub-group)

| Method/path | operationId | Permissions | Notes |
|---|---|---|---|
| GET `/stats/summary` | getEnergyStatsSummary | `metering-points-view`+`meters-view`+`supply-periods-view`+`consumption-view` | Period defaults: `to = now`, `from = to - 30d` if omitted (`DefaultPeriodDays=30`, `:12,134-135`); 400 plain `Problem` if `from > to` (`:136-147`, title "Invalid period"). Returns current counts vs. counts "as of period start", plus consumption sum for the period vs. the immediately-preceding period of equal length (`Period.Previous`, `:153`, i.e. `[from-(to-from), from)`). Consumption sum filters `IsCurrent` intervals with `Start>=period.From && End<=period.To` (`:56-57`) — same half-open-ish inclusive/inclusive bounds as the aggregate SQL. |
| GET `/stats/timeseries` | getEnergyStatsTimeseries | `consumption-view` | Only accepted `metric` value is `consumptionKwh` (case-insensitive) else 400 "Invalid metric" (`:87-93`). Buckets by **calendar date of `Start` in UTC** (`item.Start.Date`, `:97`) — **not** timezone-aware per metering point's price area, unlike the two aggregate endpoints. |
| GET `/stats/attention` | getEnergyStatsAttention | `supply-periods-view` | Hard-coded rule: active supply periods whose `End` falls within `[now, now+30d]` (`:110-114`), type `"supplyPeriodExpiring"`. Single attention-item type; no pagination; no error branch at all. |

## 2. Domain rules

### 2.1 MeteringPoint (`DM/MeteringPoints/MeteringPoint.cs`)

Fields: `Gsrn` (value type, §2.1a), `Address` (owned value object, §2.1b), `PriceArea` (string), `GridArea`
(nullable string, trimmed-or-null on write), `ExpectedAnnualConsumptionKwh` (nullable decimal), `Latitude`/`Longitude`
(nullable double), `ConnectionStatus` (enum `New|Connected|Disconnected`, default `New`), `CreatedAt`/`UpdatedAt`
(stamped by `EnergyDbContext.StampTimestamps`, `DB/EnergyDbContext.cs:47-62` — **application-level timestamping, not
a DB default/trigger**; only fires through `SaveChanges`/`SaveChangesAsync` overrides, so anything bypassing EF
skips it).

Validations (`MeteringPoint.cs:24-43`, mirrored in `MeteringPointRequest.Validate()`/`MeteringPointUpdateRequest.Validate()`):
- `Gsrn` — see §2.1a. Message: "A GSRN must contain exactly 18 digits." (domain) / "GSRN must contain exactly 18
  digits." (DTO, note the missing "A" — **wording differs between the two validators**).
- `PriceArea` — `PriceArea.IsValid`: 2 uppercase ASCII letters + 1-2 ASCII digits (`DM/MeteringPoints/PriceArea.cs:5-9`).
  Message: "Price area must contain two uppercase letters followed by one or two digits."
- Location: `latitude` in [-90,90], `longitude` in [-180,180], checked independently; whichever fails produces ONE
  combined error under the single field key `"location"` (DTO) — i.e. a bad latitude and a bad longitude together
  still yield one message (whichever check runs first: latitude, `MeteringPoint.cs:26-27`).
- `ExpectedAnnualConsumptionKwh` — only rejects `< 0`; no upper bound, no decimal-scale check (DB column is
  `numeric(14,3)`; a value with more than 3 decimal places is **silently rounded by Postgres on write**, not rejected).
- Address sub-validation (DTO only, `MeteringPointRequest.cs:24-30`, `MeteringPointUpdateRequest.cs:75-81`): each of
  `streetAddress`(≤200)/`postalCode`(≤16)/`city`(≤100) required + max-length, `countryCode` exactly 2 letters (case
  not enforced at DTO level — accepts lower-case letters, e.g. "no"; the domain `Address.ValidateCountry` *would*
  reject non-letters but the DTO's own regex-like check `CountryCode.Length!=2 || !All(IsLetter)` allows lowercase).
  `ToDomain()`/`Update()` then upper-cases it via `Address.ValidateCountry` (`DM/MeteringPoints/Address.cs:41-47`).
- `ConnectionStatus` — case-insensitive `Enum.TryParse`; unset ⇒ `New`.
- **`MeteringPointRequestExtensions.IsNullOrValidGsrn` is misleadingly named**: `value is not null && Gsrn.IsValid(value)`
  (`MeteringPointRequest.cs:104`) — despite the name, a `null` Gsrn does **not** pass; it behaves as "is a valid Gsrn",
  correctly enforcing the contract's `required: gsrn`. A porter reading only the name would get this backwards.

**2.1a Gsrn** (`DM/MeteringPoints/Gsrn.cs`): readonly struct wrapping a string; valid iff exactly 18 characters, all
ASCII digits (`Length==18 && All(IsDigit)`, `:20-21`). No GS1 check-digit validation despite the doc-comment calling
it a "GS1 Global Service Relation Number" — any 18-digit string passes.

**2.1b Address** (`DM/MeteringPoints/Address.cs`): owned entity, not a separate table (EF `OwnsOne`, §3.1).
Constructor/`Update` trim each field and enforce max length (200/16/100) and non-empty, else throw `DomainException`
(only reachable if a caller bypasses the DTO validation — the HTTP path always validates first). Country defaults to
`"NO"` when omitted; `ValidateCountry` upper-cases and requires exactly 2 A–Z letters after trimming.

### 2.2 Meter (`DM/Meters/Meter.cs`)

Fields: `MeteringPointId`, `MeterNumber` (≤64 chars, trimmed, required — `ValidateMeterNumber`, `:16-18`),
`InstalledAt`, nullable `RemovedAt`. "Current"/active meter = the one row per metering point with `RemovedAt==null`;
enforced at the DB layer only by a **partial unique index** on `(tenant_id, metering_point_id)` filtered
`removed_at IS NULL` (`DB/Configurations/MeterEntityTypeConfiguration.cs:24-25`) — there is no in-app invariant
object preventing two simultaneously-active meters outside that index (a race is caught only by the DB constraint,
see §5).

Replace flow (`ReplaceMeterEndpoint.cs`): the new meter's `installedAt` must be **strictly after** the current
active meter's `InstalledAt` (`installedAt <= active.InstalledAt` rejected, `:22-27`); the old meter's `RemovedAt` is
set to the new meter's `installedAt` (i.e. adjacent, no gap, no overlap by construction) before the new row is
inserted, all inside one transaction. A metering point can also have **zero** active meters transiently only if a
caller manually created one with `RemovedAt` set (not reachable via the public endpoints, which always leave exactly
one active meter after create/replace).

### 2.3 SupplyPeriod (`DM/SupplyPeriods/SupplyPeriod.cs`) — the state machine

States: `Active → Ended` (via End, sets `End`), `Active → Cancelled` (via Cancel; **also reachable from `Ended`**,
§1.1 DELETE row — no illegal-transition guard at all besides the End endpoint's single check). No `Cancelled → *`
or `Ended → Active` transition exists in the endpoints.

Fields: `MeteringPointId`, `CustomerId` (`> 0` required, `SupplyPeriod.cs:32`), `Start` (UTC `DateTimeOffset`),
`End` (nullable — **null means open-ended, i.e. "currently active with no scheduled end"**), `Status`.

**`Validate(start, end)`** (`:23-27`, shared by create/end/switch call sites):
1. `start.Offset != Zero || (end has value && end.Offset != Zero)` → "Start and end must be UTC timestamps."
   (**note**: only checked for request-supplied timestamps; query-string `from`/`to` filters elsewhere are never
   offset-checked — see §8.)
2. `end <= start` (when end supplied) → "End must be later than start." (end == start is invalid; must be strictly after)

**Overlap** (`Overlaps`, `:17-21`): two periods `(aStart,aEnd)`,`(bStart,bEnd)` overlap iff
`aStart < (bEnd ?? +∞) && bStart < (aEnd ?? +∞)` — i.e. half-open `[start, end)` interval semantics, `end=null`
treated as `+∞`. This is **exactly** the predicate the DB GiST exclusion constraint encodes (§3.2) and the one the
application's overlap pre-checks reduce to algebraically:
- Create (`CreateSupplyPeriodEndpoint.cs:22-23`) and the "no active period" branch of Switch (`:50-51`) check, for a
  new period that is itself open-ended (`newEnd=null`): `existing.End == null || newStart < existing.End` — this is
  literally `Overlaps(existing, new)` simplified because `new.End` is always null at creation time (the
  `bStart < aEnd??∞` half is always true, leaving only `aStart<bEnd??∞` inverted to the query's own variable naming).
  Only non-`Cancelled` periods are checked; an `Ended` period with a still-future `End` **can** still conflict.
- Boundary: a period ending exactly at `T` does **not** overlap one starting at `T` (half-open, `<` not `<=`) — this
  is directly asserted by `TS/Domain/SupplyPeriodTests.cs:8-12` (`Adjacent_periods_do_not_overlap`) and
  `:15-19` (`Open_ended_period_overlaps_later_period`: an open period always overlaps anything starting after it,
  since its own "end" is `+∞`).
- Two null-ended (open) periods on the same metering point always overlap (both "ends" are `+∞`), so at most one
  open period can exist per metering point at a time — this is the invariant the Switch endpoint depends on (find
  "the" active-and-open period, `SwitchSupplyPeriodEndpoint.cs:31-32`).

**Switch** (`SwitchSupplyPeriodEndpoint.cs`) is "end current + start new" as one transaction:
- If an open active period exists: reject same customer (400) or `switchAt <= active.Start` (400); else end it at
  `switchAt` (`Status=Ended`) and create the new period starting at `switchAt`. No overlap pre-check is needed here
  because ending the old period first removes the conflict (both periods share the exact boundary, which the
  half-open rule allows).
- If no open active period exists (e.g., after a manual End earlier): behaves as a plain "move-in" — runs the same
  overlap pre-check as Create, and can 409 against a **historical** (`Ended`) period whose `End` is still in the
  future relative to `switchAt` (asserted by `TS/Integration/EnergyEndpointsTests.cs:176-192`,
  `Supply_period_switch_rejects_overlap_with_historical_period`).

### 2.4 ConsumptionInterval (`DM/Consumption/ConsumptionInterval.cs`)

Fields: `MeteringPointId`, `Start`/`End` (UTC required), `QuantityKwh` (decimal, `numeric(14,3)`), `Quality`
(`Measured|Estimated|Corrected|Manual`), `Source` (`Elhub|Manual`), `ReceivedAt`, `IsCurrent` (bool, default true),
`SupersedesId`/`SupersedesStart` (nullable, self-FK to the previous revision this row replaces).

**`Validate(start,end,qty)`** (`:23-28`): both timestamps must be UTC (`Offset==Zero`); `end<=start` rejected
("End must be later than start."); `qty<0` rejected ("Quantity must be zero or greater." — zero is allowed).

**No overlap protection.** Unlike SupplyPeriod, there is no exclusion constraint and no application check
preventing two *different* `(start,end)` intervals from covering overlapping time ranges — the only DB constraint is
a **unique index on the exact tuple** `(tenant_id, metering_point_id, start, end)` filtered `is_current=TRUE`
(`DB/Configurations/ConsumptionIntervalEntityTypeConfiguration.cs:34-36`). "Supersede" only fires when a new manual
entry has the **identical** `start`/`end` as an existing current row (`AddManualConsumptionEndpoint.cs:18-19`): the
old row's `IsCurrent` flips to `false` and the new row records `SupersedesId`/`SupersedesStart` pointing at it
(`:20,31-32`). This is a real revision *chain*, not a full history table swap: `Supersedes` is a self-referencing FK
with `DeleteBehavior.Restrict` (`ConsumptionIntervalEntityTypeConfiguration.cs:30-33`). All read endpoints filter
`IsCurrent` — superseded rows are invisible everywhere except by following the chain manually (no endpoint does).

## 3. Persistence

Schema `energy` (`DB/EnergyDbContext.cs:27`). All four tables carry `tenant_id uuid NOT NULL` as part of their
primary key and have Postgres RLS enabled (`EnableTenantRls`, `Migrations/20260816005144_Initial.cs:178-181`,
null-safety hardened in `.../20260825105939_TenantRlsPolicyNullSafe.cs`) — **`tenant_id` and RLS are dropped
entirely in the single-tenant port**; every PK/FK/index below loses its `tenant_id` component.

| Table | PK | Notable columns | Indexes/constraints |
|---|---|---|---|
| `metering_points` | `(tenant_id, id)`, `id` identity starting at 1001 | `gsrn varchar(18)`, address columns (`street_address` 200, `postal_code` 16, `city` 100, `country_code` 2 — owned, same table, no separate `addresses` table), `price_area varchar(4)`, `grid_area varchar(64)` null, `expected_annual_consumption_kwh numeric(14,3)` null, `latitude`/`longitude float8` (declared `precision:9,scale:6` but Postgres `double precision` has no such enforcement — **precision/scale on a float column is a no-op annotation**, unverified whether EF even emits it as a CHECK; migration SQL shows a plain `double precision` column, `Migrations/Initial.cs:41-42`), `connection_status varchar(20)`, `created_at`/`updated_at timestamptz` | unique `(tenant_id, gsrn)` (`MeteringPointEntityTypeConfiguration.cs:33`) |
| `meters` | `(tenant_id, id)`, identity from 1001 | `metering_point_id`, `meter_number varchar(64)`, `installed_at`, `removed_at` null | FK → metering_points (cascade delete); unique `(tenant_id, metering_point_id)` **filtered `removed_at IS NULL`** (`MeterEntityTypeConfiguration.cs:24-25`) — enforces "at most one active meter" |
| `supply_periods` | `(tenant_id, id)`, identity from 1001 | `metering_point_id`, `customer_id` (opaque FK to the Customers module — no DB FK, resolved only via `ICustomerDirectory`), `start`, `end` null, `status varchar(20)` | FK → metering_points (cascade); plain index `(tenant_id, metering_point_id)`; **GiST exclusion constraint**, §3.2 |
| `consumption_intervals` | `(tenant_id, id, start)` — **`start` is part of the PK** because the table is range-partitioned on `start` (Postgres requires the partition key in every unique/PK index) | `id bigint GENERATED BY DEFAULT AS IDENTITY`, `metering_point_id`, `end`, `quantity_kwh numeric(14,3)`, `quality varchar(20)`, `source varchar(20)`, `received_at`, `is_current bool`, `supersedes_id bigint` null, `supersedes_start timestamptz` null | FK → metering_points (cascade); self-FK `(tenant_id, supersedes_id, supersedes_start)` → `(tenant_id, id, start)` (**restrict** delete); plain index `(tenant_id, supersedes_id, supersedes_start)`; **unique** `(tenant_id, metering_point_id, start, end)` filtered `is_current = TRUE` |

All four EF configs live in `DB/Configurations/*EntityTypeConfiguration.cs`; the raw-SQL table/partition/constraint
DDL that EF's fluent API cannot express lives entirely in `Migrations/20260816005144_Initial.cs`.

### 3.1 EF conventions worth flagging for a framework-less port

- `HasIdentityOptions(1001, 1)` on every integer PK (`Meter/MeteringPoint/SupplyPeriod` configs) — ids start at 1001,
  not 1; the Go port must reproduce this if any test or fixture hard-codes low ids, and must decide its own sequence
  start (this is an EF/Npgsql-generated `IDENTITY (START WITH 1001 INCREMENT BY 1)`, not a domain requirement).
- `HasConversion<string>()` on every enum column (`ConnectionStatus`, `SupplyPeriodStatus`, `ConsumptionQuality`,
  `ConsumptionSource`) — enums are stored as their **C# member name** (`"Active"`, `"Estimated"`, …), not an integer
  and not a Postgres native enum type. The raw aggregation SQL relies on this directly: `quality IN ('Estimated',
  'Corrected')` (§4) and `p.status <> 'Cancelled'` are string literals matching the enum's `ToString()`.
- `Address` is an EF **owned entity** (`OwnsOne`), i.e. columns inlined into `metering_points`, not a child table —
  a naive port might reach for a join table by habit.
- Decimal columns (`numeric(14,3)`) are exposed in the OpenAPI contract as `format: double` (`quantityKwh`,
  `expectedAnnualConsumptionKwh` in `energy.yaml`) — **this is the OpenAPI generator's default mapping for C#
  `decimal`, not a deliberate choice**; the Go port has no such generator default and should use a fixed-point or
  decimal-safe type to avoid the float64 precision loss the wire format nominally allows.

### 3.2 The GiST exclusion constraint (supply-period overlap)

`Migrations/20260816005144_Initial.cs:176`, raw SQL (quoted verbatim):

```sql
ALTER TABLE energy.supply_periods ADD CONSTRAINT supply_periods_no_overlap
    EXCLUDE USING gist (
        tenant_id WITH =,
        metering_point_id WITH =,
        tstzrange(start, COALESCE("end", 'infinity'::timestamptz), '[)') WITH &&
    ) WHERE (status <> 'Cancelled');
```

Requires `CREATE EXTENSION IF NOT EXISTS btree_gist;` (`:22`, needed because the constraint mixes `=` on
scalar/uuid/int columns with `&&` range overlap in one GiST index — `btree_gist` supplies the `=` operator classes
for GiST). Semantics: for a fixed `(tenant_id, metering_point_id)`, no two **non-cancelled** rows may have
overlapping `[start, end)` ranges, with `end IS NULL` treated as `infinity`. This is the DB-level mirror of
`SupplyPeriod.Overlaps` (§2.3) and is what actually decides concurrent-create races (§5) — the C# pre-checks are a
"friendly" advisory layer only. In the single-tenant port, drop `tenant_id WITH =` from the `EXCLUDE USING gist(...)`
list but keep everything else, including the partial `WHERE (status <> 'Cancelled')`.

### 3.3 Consumption partitioning and the aggregation-adjacent function

`consumption_intervals` is `PARTITION BY RANGE (start)` (`Migrations/Initial.cs:74`). Partitions are monthly and
created on demand by a Postgres function (quoted verbatim, `Migrations/Initial.cs:83-94`):

```sql
CREATE OR REPLACE FUNCTION energy.ensure_consumption_partition(partition_month date)
RETURNS void LANGUAGE plpgsql AS $function$
DECLARE
    partition_start date := date_trunc('month', partition_month)::date;
    partition_end date := (partition_start + interval '1 month')::date;
    partition_name text := format('consumption_intervals_%s', to_char(partition_start, 'YYYY_MM'));
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS energy.%I PARTITION OF energy.consumption_intervals FOR VALUES FROM (%L) TO (%L)',
        partition_name, partition_start, partition_end);
END;
$function$;
```

The initial migration pre-creates partitions for `[-12, +12]` months around "now" (`:97-100`). A later migration
(`Migrations/20260825105939_TenantRlsPolicyNullSafe.cs:30-33`) makes it `SECURITY DEFINER SET search_path =
pg_catalog`, because DDL (creating partitions) requires privileges the least-privilege runtime role does not have;
running as the function owner with a pinned `search_path` is what makes that safe. **Every manual-consumption write
calls this function first**, synchronously, outside any explicit transaction, before `SaveChangesAsync`
(`AddManualConsumptionEndpoint.cs:35-36`: `SELECT energy.ensure_consumption_partition({start.Date}::date)`) — so the
target month's partition is guaranteed to exist by the time the INSERT lands. **A Go port without native monthly
partitioning must decide how to replace this** (single unbounded table, application-level sharding, or a scheduled
job) — nothing else in the module depends on partitioning being visible at the SQL level.

### 3.4 Tenancy columns dropped

Every table's `tenant_id uuid` column, its place in every PK/FK/index, `current_setting('app.tenant_id', true)::uuid`
predicates baked into the raw aggregation SQL (§4), the `ITenantOwned` interface on all four domain entities
(`MeteringPoint.cs:6`, `Meter.cs:6`, `SupplyPeriod.cs:6`, `ConsumptionInterval.cs:6`), `EnergyDbContext : ITenantDbContext`
(`DB/EnergyDbContext.cs:15,18`), `modelBuilder.ApplyTenantOwnership(this)` (`:32`), RLS policies, and the
`EnergyTenancyIntegrationTests` test class (§7) are all dropped in the single-tenant port.

## 4. The aggregation (`EP/Consumption/ConsumptionAggregateQuery.cs`)

Both endpoints share one raw-SQL executor (`ExecuteAsync<T>`, `:142-174`) that opens the connection and — if no
ambient EF transaction exists — starts one just to run the `SELECT` (`:151-154`), always closing/disposing what it
opened (`:169-173`). Both queries take `@resolution` (`hour|day|month`, validated to exactly these three strings by
`ConsumptionAggregateValidation.cs:19-20`), `@time_zone` (an IANA zone id derived from the metering point's
`PriceArea` via `MarketTimeZone.GetId`, §4.1), `@from`/`@to` (converted `.ToUniversalTime()` before binding, `:138-139`).

**Per-metering-point** (`ForMeteringPointAsync`, quoted verbatim, `:28-52`):

```sql
WITH bucketed AS (
    SELECT date_trunc(@resolution, c."start" AT TIME ZONE @time_zone) AS bucket_local,
           c.quantity_kwh,
           c.quality
    FROM energy.consumption_intervals AS c
    WHERE c.tenant_id = current_setting('app.tenant_id', true)::uuid
       AND c.metering_point_id = @metering_point_id
      AND c.is_current
      AND c."start" >= @from
      AND c."end" <= @to
)
SELECT bucket_local AT TIME ZONE @time_zone AS bucket_start,
       (bucket_local + CASE @resolution
           WHEN 'hour' THEN interval '1 hour'
           WHEN 'day' THEN interval '1 day'
           WHEN 'month' THEN interval '1 month'
       END) AT TIME ZONE @time_zone AS bucket_end,
       SUM(quantity_kwh) AS quantity_kwh,
       COUNT(*) AS interval_count,
       BOOL_OR(quality IN ('Estimated', 'Corrected')) AS has_estimated
FROM bucketed
GROUP BY bucket_local
ORDER BY bucket_local
```

**Per-customer** (`ForCustomerMeteringPointAsync`, quoted verbatim, `:76-113`) is the same shape plus an `EXISTS`
against `energy.supply_periods` gating each interval to the requesting customer's non-cancelled period(s) covering
it (`c."start" >= p."start" AND (p."end" IS NULL OR c."end" <= p."end")`) and groups by `(metering_point_id,
bucket_local)` since it can be called once per point but is written to also work if extended to many.

**Mechanics**:
- **Time-zone bucketing**: `c."start" AT TIME ZONE @time_zone` reinterprets the `timestamptz` as a naive local
  timestamp in the metering point's market zone (Oslo/Stockholm/Copenhagen/Helsinki, §4.1), `date_trunc` truncates
  in **local wall-clock calendar** (so "day" buckets are local days, not UTC days), then `AT TIME ZONE @time_zone`
  again converts the truncated naive timestamp back to a `timestamptz` — the standard "double `AT TIME ZONE`"
  Postgres idiom for zone-aware bucketing. `bucket_end` is computed the same way after adding one unit — **the
  interval literal is chosen from a hard-coded `CASE @resolution` list; passing anything outside `hour/day/month`
  yields `NULL` bucket_end** (unreachable in practice because `ConsumptionAggregateValidation` restricts the input,
  but there is no DB-level fallback/error if that ever changes).
- **DST**: because the interval arithmetic happens in local time before the final `AT TIME ZONE` conversion, a
  "day" bucket that spans a DST transition (e.g. Oslo's spring-forward) is **23 or 25 hours of UTC**, not 24 —
  asserted by `TS/Integration/EnergyEndpointsTests.cs:267-282` (`Consumption_aggregate_marks_estimated_and_handles_oslo_dst_day`:
  23 one-hour intervals from `2026-03-28T23:00Z` fold into one `day` bucket `[2026-03-28T23:00Z, 2026-03-29T22:00Z)`,
  a 23-hour span, `hasEstimated=true` since all rows are `Quality=Estimated`).
- **Units/rounding**: `quantity_kwh` stays `numeric(14,3)` through `SUM` — Postgres numeric aggregation does not
  lose precision or introduce float rounding; no explicit `ROUND()` anywhere. `interval_count` is a plain `COUNT(*)`
  (`bigint`/`long`). `has_estimated` is true if **any** interval in the bucket has `Quality` `Estimated` **or**
  `Corrected` (not `Manual`, not `Measured`) — i.e. it flags "not a raw meter measurement", including corrections,
  but manual entries are *not* flagged as estimated even though they are also not measured.
- **Gaps**: a bucket with zero qualifying intervals simply **does not appear** in the result — there is no
  zero-fill / calendar-complete series. Callers must not assume contiguous buckets.
- **Partial buckets at the query boundary**: `WHERE c."start" >= @from AND c."end" <= @to` filters on the raw
  instants before bucketing, so a bucket at the very start/end of the requested window can be partial (missing the
  portion of local-time bucket outside `[from,to]`) with **no flag distinguishing it** from a complete bucket — same
  `hasEstimated`/`intervalCount` shape either way. Note the asymmetry: `Start >= from` but `End <= to` — an interval
  that straddles `to` (starts before, ends after) is excluded entirely, not partially counted.
- **Boundary-condition test coverage**: `TS/Integration/EnergyEndpointsTests.cs:246-265`
  (`Consumption_aggregate_uses_oslo_day_and_month_boundaries`) exercises an interval crossing local midnight
  (`23:30Z→00:30Z` UTC = `00:30→01:30` Oslo local, lands entirely in the earlier local day because `AT TIME ZONE`
  bucketing uses `start`, not `end`) and a `month` resolution collapsing two January entries into one bucket while a
  February entry becomes a second.

### 4.1 `MarketTimeZone.GetId` (`DM/MeteringPoints/MarketTimeZone.cs:10-20`)

Maps the metering point's `PriceArea` prefix (first two characters) to an IANA zone: `SE→Europe/Stockholm`,
`DK→Europe/Copenhagen`, `FI→Europe/Helsinki`, **anything else (including `NO`, empty, or `null`) → `Europe/Oslo`**
— Oslo is the fallback default, not a Norway-specific match (`TS/Domain/MarketTimeZoneTests.cs:12-15`: `"XX1"` and
`null` both map to Oslo).

## 5. Concurrency

- **No EF/DB row-version concurrency tokens anywhere** in this module (no `[Timestamp]`/`xmin` mapping on any
  entity) — conflicting concurrent writes to the *same row* (e.g. two PUTs on one metering point) simply last-write-wins
  under Postgres's default `READ COMMITTED` isolation (never overridden — no `IsolationLevel` appears anywhere in
  the module, `grep` confirms zero hits — this is an **unstated Npgsql/Postgres default**, not a deliberate choice).
- **Explicit transactions** exist only in `ReplaceMeterEndpoint.cs:19` (read-active-meter → close it → insert new
  meter) and `SwitchSupplyPeriodEndpoint.cs:30` (read-active-period → maybe close it → insert new period); `Create`
  SupplyPeriod and `AddManualConsumption` run their pre-check and insert as separate, non-transactional statements —
  the DB constraints (§3.2, unique index on consumption) are the real backstop, not the C# code's own atomicity.
  `ConsumptionAggregateQuery.ExecuteAsync` also opens a throwaway transaction purely to satisfy Npgsql's raw-command
  API when no ambient transaction exists (`:151-154`) — this is plumbing, not a correctness requirement.
- **What answers a conflict**: `HOST/Diagnostics/VantigoExceptionHandler.cs` is a **host-wide** (not energy-specific)
  `IExceptionHandler` that maps any `PostgresException` (bare or wrapped in `DbUpdateException`) whose `SqlState` is
  `unique_violation` (`23505`) or `exclusion_violation` (`23P01`) to **409** with a **generic** sanitized detail,
  `"The request conflicts with data that already exists. Verify the values and try again."` (`:90-93,22-23`) — this
  is a *different* message from the endpoints' own friendly 409s ("Overlapping supply period", "Duplicate GSRN").
  So the same logical conflict can surface with two different bodies depending on whether the friendly pre-check or
  the raw DB constraint caught it — both are 409, but the porter must implement both paths (a pre-check for the
  common case, and a catch-all constraint-violation→409 mapping for the race). Confirmed by
  `TS/Integration/EnergyEndpointsTests.cs:88-105` (`Concurrent_overlapping_supply_period_creates_yield_one_success_and_conflicts`:
  4 simultaneous POSTs to the same metering point/time, exactly one 201, the rest 409 — the comment at `:94-96`
  states outright that "the database exclusion constraint decides the winner"). The same handler also turns any
  `BadHttpRequestException` (ASP.NET's own malformed-request/model-binding failure) into 400 with detail "The
  request could not be read." (`:85`) — relevant to §8's unparseable-query-string case.
- Meter replace race: two concurrent "replace meter" calls can both read the same active meter as non-null (no
  conflict yet), then both attempt to insert a new active-meter row; the partial unique index (§3, `meters`) throws
  a unique violation on the loser, caught by the same host handler → generic 409. No energy-specific code prevents
  or specially messages this case.

## 6. Cross-cutting

- **Rate limits**: none found in the module (no `[EnableRateLimiting]`/`RequireRateLimiting` anywhere in
  `apps/energy/backend/Energy.Module`). Any rate limiting is host-global and out of this module's scope — unverified
  whether the host applies one; not found by search within the module itself.
- **Audit/events**: the module publishes nothing — no domain events, no outbox writes, no notifications. It
  *consumes* `ICustomerDirectory` (§1.2) but has no equivalent contract of its own that other modules consume; Energy
  is a leaf module for cross-module purposes.
- **Permission catalog** (`AZ/EnergyPermissionCatalogContributor.cs:9-65`), all `Module="energy"`, `Category="Energy"`,
  `Sensitive=false` (default, none set), `Delegable=true` (explicit on every entry):

  | Key | Display name | Description |
  |---|---|---|
  | `energy:metering-points-view` | View energy metering points | View energy metering point details and listings. |
  | `energy:metering-points-manage` | Manage energy metering points | Create and update energy metering points. |
  | `energy:meters-view` | View energy meters | View energy meter history for metering points. |
  | `energy:meters-manage` | Manage energy meters | Replace meters installed at energy metering points. |
  | `energy:consumption-view` | View energy consumption | View energy consumption intervals and aggregates. |
  | `energy:consumption-manage` | Manage energy consumption | Add and replace manual energy consumption intervals. |
  | `energy:supply-periods-view` | View energy supply periods | View energy supply periods for metering points. |
  | `energy:supply-periods-manage` | Manage energy supply periods | Create, switch, end, and cancel energy supply periods. |

  Stable ordering asserted alphabetically by key in `TS/Authorization/EnergyPermissionCatalogTests.cs:14-23`. No
  permission in this module is `Sensitive`.
- Development-only seed data (`DB/DevelopmentSeed/DevelopmentDataSeeder.cs`) and design-time DB tooling
  (`DB/DesignTimeNpgsqlDataSource.cs`, `DB/Energy/EnergyDbContextFactory.cs`) exist but are dev/tooling concerns, not
  behavior to port.

## 7. Tests

| Class | File | Count | Description | Port? |
|---|---|---|---|---|
| `ConsumptionIntervalTests` | `TS/Domain/ConsumptionIntervalTests.cs` | 2 | Rejects end-before-start; rejects negative quantity | Port |
| `GsrnTests` | `TS/Domain/GsrnTests.cs` | 2 (2 `[Theory]`; 5 `InlineData` cases) | Accepts 18-digit strings; rejects short/empty/non-digit | Port |
| `MarketTimeZoneTests` | `TS/Domain/MarketTimeZoneTests.cs` | 1 (1 `[Theory]`; 6 cases) | Price-area prefix → IANA zone, incl. unknown/null → Oslo fallback | Port |
| `MeterTests` | `TS/Domain/MeterTests.cs` | 3 (2 `[Fact]` + 1 `[Theory]`; 5 cases) | Rejects missing/blank meter number; rejects >64 chars; trimming allowed | Port |
| `PriceAreaTests` | `TS/Domain/PriceAreaTests.cs` | 2 (2 `[Theory]`; 6 cases) | Accepts NN#/NN## format; rejects short/lowercase/too-long | Port |
| `SupplyPeriodTests` | `TS/Domain/SupplyPeriodTests.cs` | 2 | Adjacent periods don't overlap; open-ended overlaps later period | Port |
| `EnergyPermissionCatalogTests` | `TS/Authorization/EnergyPermissionCatalogTests.cs` | 1 | Catalog keys, ordering, module/category/non-blank metadata | Port (adapt to Go catalog shape) |
| `EnergyAuthorizationIntegrationTests` | `TS/Integration/EnergyAuthorizationIntegrationTests.cs` | 3 | (1) every endpoint 403s without its permission(s) and 401/403s a disabled user; (2) customer metering-points needs *all three* composite permissions; (3) every composite (multi-permission) endpoint 403s if any one required permission is missing | Port (re-target to Go's auth mechanism; behavior, not .NET Identity plumbing, is the contract) |
| `EnergyEndpointsTests` | `TS/Integration/EnergyEndpointsTests.cs` | 15 | Metering-point CRUD round trip; meter swap history; manual-consumption supersede; supply-period overlap/409 + end-then-recreate; concurrent overlapping creates (1 win, rest 409); switch ends+creates contiguous; switch as move-in (no active period); switch rejects before-start/same-customer; switch rejects overlap vs. historical period; customer consumption partitioned by switch; customer consumption respects period boundaries; aggregate Oslo day/month boundaries; aggregate DST 23-hour day + `hasEstimated`; customer aggregate excludes out-of-period intervals; aggregate rejects invalid resolution | Port — this is the core functional/behavioral suite |
| `EnergyTenancyIntegrationTests` | `TS/Integration/EnergyTenancyIntegrationTests.cs` | 1 | Tenant isolation of metering points/consumption + per-tenant GSRN uniqueness | **Drop (tenancy-only)** |

**The metric is .NET test methods: a `[Theory]` counts as one method regardless of its `InlineData` count.**
Every `Count` above is now a method count, with the case count noted in parentheses where the two differ.

**Total: 32 methods. Portable: 31** (all except `EnergyTenancyIntegrationTests`' single tenancy-only test).

Corrected 2026-09-12 in Task 16, from "44 total / 43 portable". Two errors: this table counted theory *cases*
rather than methods for the five theory-bearing domain classes (21 cases across 8 theory methods), and it
undercounted `EnergyEndpointsTests` as 13 where the file carries 15 `[Fact]`s. Counting methods:
2 + 2 + 1 + 3 + 2 + 2 + 1 + 3 + 15 = 31 portable, plus the 1 dropped = 32.

`EnergyApiFactory.cs` is test infrastructure (WebApplicationFactory + a `FakeCustomerDirectory` stubbing
customers `1001`/`1002`), not a test class itself.

## 8. Oddities (porter hazards)

1. **`getEnergyMeteringPoints` contract declares a 404 the code cannot produce** (`energy.yaml:685-686` vs.
   `GetMeteringPointsEndpoint.cs:12` handler type `Results<Ok,ProblemHttpResult>` — no `NotFound` arm). Decide
   whether the Go port keeps the unreachable 404 in its OpenAPI or removes it; either is defensible, but silently
   copying the .NET behavior means the 404 response schema is dead documentation.
2. **`EndSupplyPeriodEndpoint` returns two different 400 shapes from one operation**: the "cancelled period" branch
   returns a plain `ProblemDetails` (`EndSupplyPeriodEndpoint.cs:17`) while the "bad end date" branch returns
   `HttpValidationProblemDetails`/`ValidationProblem` (`:18-19`) — the contract only documents the latter shape for
   this op's 400 (`energy.yaml` around `postEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd`). A client cannot
   assume a consistent 400 body for this one endpoint.
3. **Validation order is inconsistent across endpoints** and matters for status codes: `UpdateMeteringPoint` and
   `CreateSupplyPeriod` validate the body *before* checking the referenced entity exists (so a malformed body on a
   nonexistent id returns 400, not 404), while `ReplaceMeter` and `SwitchSupplyPeriod` check existence *first* (404
   wins over a malformed body). There is no single rule to fall back on; each handler's literal `if` order is the
   spec (see the per-endpoint "Errors (order checked)" column in §1.1).
4. **An unknown `customerId` on Create/Switch supply-period is a 400 `ValidationProblem`, not a 404**, even though
   it is "the referenced resource doesn't exist" in spirit (`CreateSupplyPeriodEndpoint.cs:20-21`,
   `SwitchSupplyPeriodEndpoint.cs:25-28`) — while an unknown `meteringPointId` on the very same endpoints *is* a 404.
   Two different "doesn't exist" flavors get two different status codes depending on which foreign id is missing.
5. **Cancel has no state guard**: `CancelSupplyPeriodEndpoint.cs:15` sets `Status=Cancelled` unconditionally,
   including on an already-`Ended` or already-`Cancelled` period — there is no "you can't cancel a closed period"
   rule anywhere, unlike `End`, which does refuse a `Cancelled` period (`EndSupplyPeriodEndpoint.cs:17`). If the Go
   port is expected to be stricter here, that is a deliberate behavior change, not a faithful port.
6. **Consumption intervals have zero overlap protection** beyond exact-tuple dedup (§2.4) — this is easy to
   "improve" by adding an overlap check while porting, which would silently diverge from .NET behavior (e.g. the
   `EnergyEndpointsTests` DST test intentionally inserts 23 back-to-back, non-overlapping, but not deduplicated
   against any prior data, one-hour intervals with `Quality=Estimated` directly via `EnergyDbContext`, bypassing the
   manual-consumption endpoint's supersede logic entirely — that path exists only for the test seam, not production
   traffic, since production consumption normally arrives with `Source=Elhub` through an unmodeled ingestion path
   not present in this module at all — unverified whether that ingestion exists elsewhere in the codebase; nothing
   under `apps/energy` writes `Source=Elhub` outside this test).
7. **`decimal` fields serialize as `format: double`** in the OpenAPI contract (`quantityKwh`, `expectedAnnualConsumptionKwh`,
   stats `consumptionKwh`/`*Delta`, `EnergyStatsDailyBucket.value`) — a byproduct of the .NET OpenAPI generator's
   default `decimal→double` mapping, not a deliberate numeric-type decision. The Go port has no such generator and
   should pick a precision-safe representation deliberately rather than copying `float64` from the wire format.
8. **Query-string date-time parsing has no explicit UTC-offset validation**, unlike request *bodies* (`from`/`to` on
   `GetConsumption*`, `GetConsumptionAggregate*`, `stats/summary`, `stats/timeseries` are plain `DateTimeOffset?`
   minimal-API parameters bound via .NET's `IParsable<DateTimeOffset>`, which — for an offset-less input string —
   falls back to interpreting it in the **server process's local time zone** (a framework/BCL default, unverified
   exact behavior version-to-version, but is standard `DateTimeOffset.Parse` semantics with no `AssumeUniversal`
   style set). Request *bodies* (`ManualConsumptionRequest`, `CreateSupplyPeriodRequest`, etc.) are explicitly
   UTC-checked by `ConsumptionInterval.Validate`/`SupplyPeriod.Validate` (`Offset != TimeSpan.Zero` rejected) — query
   parameters get no equivalent check anywhere. A Go port (no such implicit fallback) must decide and document its
   own offset-less-string policy; simply "parsing whatever the client sends" will not reproduce this ambiguity, nor
   should it try to.
9. **Unparseable query/route values 400 via a framework default, not module code**: e.g. `?page=abc` or a
   non-integer `{id}` never reaches any Energy handler — ASP.NET's minimal-API model binding throws
   `BadHttpRequestException` first, mapped by the **host-wide** exception handler to 400 "The request could not be
   read." (`HOST/Diagnostics/VantigoExceptionHandler.cs:85`). None of this is visible in the Energy module's source;
   a porter who only reads `Energy.Module` will miss this behavior entirely.
10. **N+1 by design** in `GetCustomerConsumptionAggregateEndpoint` (`:32-38`): one raw-SQL round trip per matching
    metering point, sequentially, inside a loop — acceptable at expected scale (few metering points per customer)
    but a naive Go rewrite "batching all points into one query" would change the SQL shape (and, subtly, the
    per-point transaction-opening behavior of `ConsumptionAggregateQuery.ExecuteAsync`, §5) even though the
    end-to-end response shape stays identical.
