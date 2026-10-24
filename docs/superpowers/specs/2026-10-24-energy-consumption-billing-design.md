# Energy consumption billing — design (Energy phase 4 and Invoices phase 5)

Two roadmap phases, one design: Energy phase 4, "Invoicing groundwork (align with
Invoices)" (`ROADMAP.md:492-500`), and Invoices phase 5, "Energy consumption billing"
(`ROADMAP.md:1039-1045`). Research: `docs/superpowers/research/2026-10-24-energy-consumption-billing.md`
("R5": §2 the bookkeeping rules, §3 the market regulation, §4 taxes and VAT, §5 prices,
§7 the codebase, §9 the decisions it asked for, §10 its UNCERTAIN items). Baselines: the
work-to-invoices design (`docs/superpowers/specs/2026-10-05-invoices-work-to-invoices-design.md`,
"R3 design": D1 the write-back, D2 `line_sources`, D3 the view and the wizard, D6 the VAT
codes) and the energy import design (`docs/superpowers/specs/2026-10-18-energy-import-design.md`).
Module docs: `R/energy.md`, `R/invoices.md`.

The split in one sentence: **Energy settles, Invoices documents.** Energy owns the price
dimension (spot prices per price area, the supplier's tariffs, the installation's own grid
tariff) and computes, per supply period and billing period, a **settlement** — the priced
lines a customer owes for a metering point — and hands it across
`contracts.ConsumptionProvider` as the sixteenth contract. Invoices builds a document from
settlements exactly as it builds one from hours (R3): held through `line_sources`, judged
and stamped at issue through Energy's `InvoicedWorkHolder`, released by a credit note.
Batch issuing is a billing run: a worker that settles, drafts and issues one document per
customer and records what it skipped.

`srv/` is `apps/server/internal/`, `mig/` is `srv/db/migrations/`, `MB` is
`docs/src/content/docs/en/contributing/module-boundaries.md`, `R/` is
`docs/src/content/docs/en/reference/`, `U/` is `docs/src/content/docs/{en,nb}/user/`,
`A/` is `docs/src/content/docs/{en,nb}/admin/`. Line numbers are against `df9bf81d`.
Money: exact decimal inside, JSON numbers on the wire, decimal text across a contract
(`srv/contracts/actuals.go:65-68`).

## Decisions

### D1 — The contract: `contracts.ConsumptionProvider`, a settlement per supply period and period

**The choice.** Energy prices. The roadmap places the price dimension in Energy; pricing
is Σ over intervals of kWh × the interval's price, which needs the hourly series and the
tariff — both Energy's — and must never cross a module boundary row by row (a month of a
single point is 744 intervals, 2 976 at PT15M). What crosses is the **settlement**: a row
Energy owns, with an id and a revision, its figures frozen, that an invoice line is built
from — the same shape as a billable hour (`srv/contracts/billable.go`), so R3's machinery
applies unchanged. R5 §9.1.

**The slot.** `Consumption` (`contracts.ConsumptionProvider`), the thirteenth
single-provider slot, declared by Energy (`Module.Consumption func(Deps)
contracts.ConsumptionProvider`), resolved by `soleProvider` **after `Directory`** and in
the `Billable*` group (`srv/module/compose.go:275-299`), optional for its consumer (nil
when Energy is off), and resolved by `module.Workers` too (D10). MB rule 5's list gains it.

**The contract** (`srv/contracts/consumption.go`, new):

```go
// ConsumptionProvider is the energy module's read for invoicing: what a
// customer owes for its metering points, settled per supply period and
// billing period, priced by the module that owns the prices.
type ConsumptionProvider interface {
    // DueSupplies lists, per customer, the supply periods with at least one
    // billing period closed on or before through and not yet invoiced — the
    // uninvoiced view's figures, without settling anything.
    DueSupplies(ctx context.Context, req DueSuppliesRequest) (DueSuppliesPage, error)
    // Settle computes (or recomputes, while still draft) the settlements of
    // the customers' or supply periods' billing periods closed on or before
    // through, writes them as draft rows and answers them — regular ones for
    // periods never invoiced, corrections for invoiced periods whose figures
    // changed since (D5). It reads and writes on the pool, outside any lock
    // of the caller's, and never calls a directory.
    Settle(ctx context.Context, req SettleRequest) (SettlementsPage, error)
    // Settlements answers settlements by id, as they stand now: a draft one
    // recomputed, an invoiced or released one as frozen. A missing id is
    // absent, never an error.
    Settlements(ctx context.Context, ids []int64) (SettlementsPage, error)
}

type DueSuppliesRequest struct { CustomerIDs []int32; Through time.Time }   // ≤ MaxActualsRequests customers
type SettleRequest struct {
    CustomerIDs     []int32  // exactly one of the two, ≤ MaxActualsRequests customers
    SupplyPeriodIDs []int32  // or ≤ MaxBillableRows periods
    Through         time.Time
}
type DueSupply struct {
    CustomerID, SupplyPeriodID, MeteringPointID int32
    GSRN, Address, PriceArea, ContractName      string
    Cadence                                     string     // monthly | quarterly | yearly
    BilledThrough                               *time.Time // the last invoiced period's end, nil when none
    DuePeriods                                  int        // closed, uninvoiced periods through Through
    Blocked                                     string     // "" or the first period's blocker (Settlement.Blocked)
}
type Settlement struct {
    ID                                          int64
    Revision                                    int32
    Kind                                        string // regular | correction
    CorrectsID                                  *int64 // a correction's invoiced original
    SupplyPeriodID, MeteringPointID, CustomerID int32
    GSRN, Address, PriceArea                    string
    Household                                   bool
    ContractName                                string // the power tariff's name (§ 7-2 d)
    PeriodStart, PeriodEnd                      time.Time // half-open, market-local civil bounds
    KWh                                         string // decimal text, three decimals
    Estimated                                   bool   // any interval Estimated
    MissingIntervals                            int    // expected − present; 0 is complete
    Currency                                    string // "NOK"
    Lines                                       []SettlementLine
    Amount                                      string // Σ lines, two decimals
    Blocked                                     string // "" or: no_tariff | price_gap | incomplete | no_consumption | period_open
    State                                       string // draft | invoiced | released
    InvoicedBy                                  *InvoiceStampRef // nil while draft
}
type SettlementLine struct {
    Kind      string // spot_energy | fixed_energy | markup | monthly_fee | grid_energy | grid_capacity | grid_fixed | electricity_tax | enova_levy | correction
    Text      string // the component's own name, language-neutral ("Spot NO1", "Påslag", "Nettleie energiledd dag")
    Quantity  string // kWh, months (prorated, three decimals) or 1
    Unit      string // kWh | month | pcs
    UnitPrice string // six decimals, NOK
    Amount    string // two decimals; negative on a correction
    VATHint   string // standard | exempt_household_north (D8)
}
```

**The rules a provider keeps** (the `Billable*` list, `srv/contracts/billable.go:135-150`,
plus two): it reads and writes on the pool, never under a lock of the caller's; it never
calls a directory while serving (the customer's facts are the caller's to read); it
refuses a request `Validate` refuses; it answers at most `MaxBillableRows` settlements and
sets `More`; by ids it answers only settlements that exist; it fails rather than
under-reports. The two new: **`Settle` writes** — draft rows, idempotently, in short
autocommit transactions of its own — which is why it is named a command and documented as
one; and a **blocked settlement is still answered**, with `Blocked` set and no lines, so
the caller can show why a point is not billed instead of silently skipping it.

### D2 — Spot prices: intervals per price area, fetched or imported

**The model.** `energy.spot_prices` (`mig/00065_energy_spot_prices.sql`): `id bigint`,
`price_area varchar(4)`, `start`/`end timestamptz` (half-open, `end > start`),
`nok_per_kwh numeric(12,6)`, `eur_per_kwh numeric(12,6) NULL`, `exchange_rate
numeric(12,6) NULL`, `source varchar(20)` (`Feed` | `Import`), `received_at`. Unique
`(price_area, start)`; a GiST exclusion on `(price_area, tstzrange(start, end, '[)'))`
refuses overlaps, so a feed that one day answers 96 quarter-hours for a day that already
holds 24 hours is refused for that day rather than doubled (R5 §5.1, U2). A re-import of a
price for the same `(area, start, end)` replaces the figure; the settlement built on the
old one is frozen and the next `Settle` produces the correction (D5). No month partitioning:
five areas at PT15M are 175 000 rows a year.

**The feed.** A worker, `energy-spot-prices`, on when `ENERGY_SPOT_PRICE_FEED_ENABLED`
(default **false**: an installation opts in to an outbound call), fetching
`ENERGY_SPOT_PRICE_FEED_URL` — a template with `{year}`, `{month}`, `{day}`, `{area}`,
default `https://www.hvakosterstrommen.no/api/v1/prices/{year}/{month}-{day}_{area}.json`
— through a guarded client copied from the calendar subscriptions' (netguard dialler, no
proxy, no redirect, a 1 MiB cap, 10 s dial, 15 s header). Every cycle (`ENERGY_SPOT_PRICE_FEED_POLL`,
default 15 minutes) under a `pg_try_advisory_lock` lease (`srv/invoices/ehf_events_worker.go:216-240`'s
shape, so one replica fetches), it takes the **areas in use** — the distinct `price_area`
of metering points with a non-cancelled supply period, plus every area already priced —
and for each the civil days (Europe/Oslo) from seven days back to **tomorrow** that
`energy.spot_price_fetches (price_area, day, status, attempts, last_error, next_attempt_at,
fetched_at)` does not record as `ok`, at most twenty requests a cycle. A 200 is parsed
(`time_start`, `time_end`, `NOK_per_kWh`, `EUR_per_kWh`, `EXR`), checked to lie within
the day and to be contiguous, and upserted in one transaction with the fetch row `ok`; a
404 for tomorrow is `pending` and retried hourly (the day-ahead auction publishes about
13:00 CET), a 404 for a past day `missing` and retried daily up to seven times; any other
failure `failed` with exponential backoff capped at six hours. Older days come by file.
The worker never backfills past seven days on its own, and refuses a body that is not
JSON, not an array or whose intervals overlap what is stored (the exclusion), logging the
day and leaving the fetch row `failed`.

**The import.** `POST /api/v1/energy/spot-prices/import` (multipart `file`, through
`internal/fileimport` with the consumption import's limits) — columns found by header
words as the consumption import finds its own (`srv/energy/consumption_import.go:205-230`):
the area (`area`, `price_area`, `prisområde`, `elspotområde`), the start (`start`, `from`,
`fra`, `time_start`), the end (`end`, `to`, `til`, `time_end`; optional, else the next
row's start or one hour), the NOK price (`nok_per_kwh`, `price`, `pris`, `øre/kwh`
divided by 100 when the header says øre), optional `eur_per_kwh` and `exr`. Local times are
read in Europe/Oslo with the DST nights spelled out as the consumption import does; a file
may hold several areas. **All or nothing**: every row is checked, then written in one
transaction (`{inserted, replaced, unchanged}` per area answered; `dryRun=true` checks
only). A row overlapping a stored interval of another length is a 400 naming the row. The
permission is **`energy:prices-manage`** (new, not sensitive), with `energy:prices-view`.

**The reads.** `GET /api/v1/energy/spot-prices?area&from&to` (`energy:prices-view`; `to −
from` ≤ 35 days) answers the intervals; `GET /api/v1/energy/spot-prices/coverage` answers
per area `{area, firstStart, lastEnd, daysMissingLast30, lastFetch: {day, status, at,
error}}`, read by the Prices page (D11). Prices are not personal data and need no
customer gate.

### D3 — Tariffs: the supplier's contract, and the installation's own grid tariff

**The model** (`mig/00065_energy_tariffs.sql`). `energy.tariffs`: `id`, `kind`
(`power` | `grid`), `name varchar(120)` (what § 7-2 d prints), `description`, `status`
(`active` | `archived`), timestamps. `energy.tariff_versions`: `id`, `tariff_id`,
`valid_from date`, `valid_to date NULL`, an exclusion against overlap per tariff, so a
price change is a new version from a date and the old one stays for the periods it
priced. `energy.tariff_components`: `id`, `version_id`, `position`, `kind`, `name
varchar(80)` (the line's text), `rate numeric(12,6)` (NOK per unit), `rate_night
numeric(12,6) NULL`, `day_from`/`day_to smallint NULL` (whole hours, 06–22), `weekdays_only
bool`, `tiers jsonb NULL` (`[{"fromKw": 0, "perMonth": 135}, …]`, ascending, a CHECK on the
shape by a validation in Go before the write), `rate_reduced numeric(12,6) NULL`,
`rate_business numeric(12,6) NULL`. The kinds, and what each prices:

| Kind | Tariff | Prices |
| --- | --- | --- |
| `spot_energy` | power | Σ kWh × the interval's NOK price; the line's unit price is the volume-weighted average, six decimals, and `quantity` the kWh |
| `fixed_energy` | power | kWh × `rate` |
| `markup` | power | kWh × `rate` (øre/kWh given as NOK) |
| `monthly_fee` | power or grid | `rate` × the months covered, prorated by civil days when the supply period or the tariff version covers part of the billing period (quantity in months, three decimals) |
| `grid_energy` | grid | kWh × `rate`, or × `rate_night` for intervals outside `day_from–day_to` on weekdays (and all weekend intervals when `weekdays_only`) — two lines when both rates apply |
| `grid_capacity` | grid | the tier whose `fromKw` is the highest not above the average of the three highest hourly kWh on three different civil days of the period (R5 §5.4); PT15M intervals are summed to the hour first; one line per calendar month in the period |
| `grid_fixed` | grid | as `monthly_fee` |
| `electricity_tax` | grid | kWh × `rate` (ordinary), `rate_reduced` (reduced) or nothing (exempt), by the point's class (D4) |
| `enova_levy` | grid | household: kWh × `rate`; business: `rate_business` per year, prorated by days |

A power tariff has exactly one of `spot_energy` and `fixed_energy`; a grid tariff none of
the power kinds; the module validates and answers a 400 with the component's position.

**Assignment.** `supply_periods.tariff_id` (the power tariff, nullable: a supply without
one is `no_tariff`), `metering_points.grid_tariff_id` (nullable: empty means the grid
company bills grid services itself, the supplier's usual case — R5 §3.3). Both are plain
ids within the energy schema with a foreign key. A tariff in use cannot be deleted; it is
archived, and an archived tariff still prices the periods it covers.

**Endpoints.** `GET/POST /api/v1/energy/tariffs`, `GET/PUT /api/v1/energy/tariffs/{id}`
(the header, the versions with their components in one body, as the mapping is one body
in the import), `POST …/archive`; `energy:tariffs-view` and `energy:tariffs-manage`
(new). The supply-period create and switch bodies gain `tariffId` and `cadence`; `PUT
/metering-points/{id}` gains `gridTariffId`, `household` and `electricityTaxClass`.

### D4 — The metering point's billing facts, and the cadence

`metering_points` gains `household boolean NOT NULL DEFAULT false` (husholdningsbruk,
mvaf § 6-6-1 — set by hand, because the module cannot know it) and `electricity_tax_class
varchar(10) NOT NULL DEFAULT 'ordinary'` (`ordinary` | `reduced` | `exempt`, R5 §4.1).
The VAT exemption of R5 §4.3 is **derived**: a point is `exempt_household_north` when
`household` and its country is `NO` and its postal code is `8000`–`9999`; the derivation
is one function with a test and is shown on the point's page, so a wrong postal code is
seen. `supply_periods` gains `cadence varchar(10) NOT NULL DEFAULT 'monthly'` (`monthly` |
`quarterly` | `yearly`); the market regulation allows quarterly at most for a consumer, and
the module refuses `yearly` on a supply whose customer is a person **when it knows** —
it does not: the directory is not read while settling, so the refusal is the wizard's
(D7, `cadence_not_allowed`, judged from the billing profile's `Type`), and Energy's PUT
accepts it. A billing period is the civil calendar unit in the point's market time zone
(`marketTimeZone`, `srv/energy/consumption.go:102-117`): the month, the quarter or the
year, clipped to the supply period's own `[start, end)`. A period is **closed** when its
end is on or before `Through` and, for an open supply period, before now.

### D5 — Settlements: the row, the computation, the correction

**The model** (`mig/00066_energy_settlements.sql`). `energy.settlements`: `id bigint`,
`supply_period_id`, `metering_point_id`, `customer_id` (opaque, copied from the supply
period for the live index), `kind` (`regular` | `correction`), `corrects_id` (the invoiced
original a correction follows), `period_start`/`period_end timestamptz`, `state` (`draft`
| `invoiced` | `released`), `revision int`, `kwh numeric(14,3)`, `estimated bool`,
`interval_count`, `expected_interval_count int`, `blocked varchar(20) NULL`, `amount
numeric(14,2)`, `currency char(3)`, `contract_name varchar(120)`, `computed_at`, the
stamp (`invoiced_invoice_id`, `invoiced_number`, `invoiced_at`, `invoiced_by_user_id`,
`invoice_date`, `released_at`) and `energy.settlement_lines` (`settlement_id`, `position`,
`kind`, `text`, `quantity numeric(14,3)`, `unit`, `unit_price numeric(14,6)`, `amount
numeric(14,2)`, `vat_hint`). The floor: a partial unique index on `(supply_period_id,
period_start, kind) WHERE state IN ('draft','invoiced')` — one live regular settlement per
period, one live correction per period at a time (a second correction waits until the
first is invoiced or released; R5 §3.4's "next period" is literal).

**The computation**, one function with the queries it needs, deterministic for a given
database state: the period's current intervals inside the supply period (the aggregate's
privacy predicate, `srv/energy/queries/consumption.sql:120-152`, with the half-open bounds
corrected: an interval must lie inside the period; one straddling a boundary makes the
period `incomplete` — a PT15M interval never straddles an hour, and a supply period that
starts mid-hour is clipped at the hour by the write that created it, which Energy already
rounds); the expected count from the period's length and the point's finest interval
length present; the prices of the area over the period, each interval priced by the price
interval containing its start (a price gap is `price_gap`); the tariff versions valid on
the period's days (a version change mid-period splits the kWh by day); then the lines in
the order of the kinds table, the amounts rounded to two decimals per line, the kWh three
decimals. Blockers in order: `period_open`, `no_tariff`, `no_consumption` (no interval at
all), `incomplete` (a missing interval: the supplier bills measured consumption, § 7-1a (3)
allows an estimate only when reading is unreasonable — an estimate is entered as an
`Estimated` interval and the invoice then says so), `price_gap` (spot tariffs only).

**Draft rows.** `Settle` upserts the draft settlement of every due period of the supply
periods in the request (keyed by the live index), bumping `revision` when any figure
changed, and answers them; a draft is never "stale" by itself — every read recomputes it.
A draft that a held `line_sources` row points at is still recomputed (Invoices judges
freshness by revision and amount, R3 D2, and refreshes with `refreshSources`). Draft rows
nobody holds are deleted after thirty days by the settlements' own housekeeping in the
feed worker's cycle, or on the next `Settle` for that supply.

**Corrections.** For each invoiced settlement of a supply period whose period lies within
the last twelve months and whose point received an interval, or whose area a price, after
the settlement's `computed_at`, `Settle` recomputes the period; when any line's amount or
the kWh differs, it upserts a `correction` draft whose lines are the **differences** per
kind (negative allowed, `Amount` negative), `CorrectsID` the original, and `KWh` the delta.
A correction whose every difference is zero is not written. An invoiced correction is
itself a baseline: the next comparison is against the sum of the invoiced regular and its
invoiced corrections.

### D6 — The write-back: Energy as an `InvoicedWorkHolder`

`contracts.WorkSourceSettlement = "energy.settlement"`, appended **last** to
`InvoicedWorkOrder` (after Time: an energy row never shares a transaction with a project's
or a time entry's, so the place only has to be fixed). Energy declares `Module.InvoicedWork`,
constructed from `Deps.Logger` alone, and runs whether or not the module is enabled (MB
rule 10). `MarkInvoiced` locks the settlements by id ascending (`FOR NO KEY UPDATE`), judges
each in R3's order — already `invoiced` → `source_already_invoiced`; `released` or
`blocked` → `source_not_invoiceable`; recomputed under the lock, the revision, currency
or amount differs → `source_changed` — and stamps `state = invoiced`, the five stamp
columns from `InvoiceRef`, `revision + 1`. `ReleaseInvoiced` takes the same locks, moves
`invoiced → released`, clears the stamp, logs and tolerates a settlement not carrying the
stamp. A released regular settlement is forgotten by the live index, so the next `Settle`
writes a fresh draft for the period; a released correction likewise. `WorkSource.ProjectID`
is 0 for the kind, which D7 makes nullable on the Invoices side.

### D7 — Invoices: from consumption to a draft

**The link.** `mig/00067_invoices_energy.sql`: `line_sources.project_id` becomes nullable
with a CHECK that it is set for the three work kinds and null for `energy.settlement`;
`ck_line_sources_kind` is replaced by a shape check `source_kind ~ '^[a-z]+\.[a-z_]+$'` so
the invoices migration names no other schema (the schema test's regex, R5 §7.2);
`source_subkind` carries `regular` | `correction` for the kind; the settings gain
`energy_vat_code` (default id 1, S 25 %) and `energy_vat_code_exempt` (default id 5, Z
0 %). `quantity > 0` stays: a correction line with a negative amount is written with the
absolute kWh as quantity and a negative unit price — no: `unit_price ≥ 0` is a CHECK too.
A negative correction is therefore a line with `quantity` the kWh and `unit_price` 0 and
an `allowance`? Neither fits; the honest shape is **a credit-side line**, and the module
already has one: the a-konto deduction line (R3 D7) with `quantity < 0`. The CHECK
`ck_lines_quantity` is widened: `quantity < 0` is allowed when the line's source is a
correction as well as when it deducts — enforced by the module before the write (the
trigger cannot see `line_sources` at insert time), and the line's `line_net` is negative.
`invoice_total_not_positive` keeps the document an invoice.

**The wizard.** `POST /api/v1/invoices/from-consumption` (`invoices:access+invoices:create`):
`{customerId, through (date, default today), settlementIds?, invoiceId?+revision?}`. Before
the transaction, in the wizard's order (`srv/invoices/work.go:864-874`): parse; the append
target; the settings and the billing profile with `customerGate`; **`Settle`** for the
customer through `through` (or `Settlements` by the ids given, each checked to belong to
the customer: `source_not_for_customer`); judge — `cadence_not_allowed` (a yearly supply of
a person), `settlement_blocked` (named per settlement, the first blocker as detail), a
settlement already held elsewhere → `source_held_elsewhere` at the insert as today; the
VAT codes: the settings' two, overridable in the request as `vatCodes.energy` and
`vatCodes.energyExempt`; then the lines — **one line per settlement line**, in settlement
order, text in the buyer's language from a word table as `grouping.go:71` has it:
`"<component name>, <GSRN> <street address>, <period>"` with `periodText`, a correction's
`"Korreksjon <component>, <GSRN>, <period>, tidligere fakturert <number>"`; quantity,
unit (`kWh` → EHF `KWH`, `month` → `MON`, `pcs` → `C62`), unit price; the VAT code by
`VATHint`; the document's delivery period the span of the settlements (`widenDelivery`).
`too_many_lines` past 500 (≈ 60 points) is answered with `suggestedSplit: n` customers'
worth — the run (D10) splits a customer's settlements across documents by metering point
when a single document cannot hold them. No grouping options: § 7-2 (2) b wants every
price element on its own line. Then one `withLockedTx` as from-work: the draft,
`writeLines`, `insertSources` (kind, id, revision, subkind, project null, quantity the
kWh or months, amount, currency, `source_date` the period's end minus a day).

**Freshness, the issue, the credit note.** The GET's `freshness` reads the kind through
`Settlements` and warns `source_changed` / `source_not_invoiceable` as for work;
`refreshSources: true` re-reads and rebuilds the kind's lines from the settlement's new
lines (they may have changed in number). `readIssueWork` requires Projects only for
sources that carry a project (today's `projects_unavailable` for a draft with energy
sources alone would be wrong); `markIssueWork` calls Energy's holder in its place. The
credit note releases settlements whose lines are returned in full (R3 D8), and `Settle`
then re-drafts them.

### D8 — VAT on energy lines

Every settlement line carries a hint, not a code: `standard`, or `exempt_household_north`
when D4's derivation holds. Invoices maps the hint to the settings' code (`energy_vat_code`,
`energy_vat_code_exempt`), code 9 "O" when the seller is not VAT registered as the work
codes do (`srv/invoices/work.go:67`). The exempt default is code 5 (Z, 0 %), the EHF
exemption reason "Elektrisk kraft til husholdningsbruk i Nord-Norge, mval. § 6-6" written
on the line's snapshot (R5 U4). Grid lines, taxes and the levy follow the same hint: the
exemption is the supply's, not the component's (M-6-6: "etter samme tariff og på samme
måler").

### D9 — The document: what § 7-2 needs on the PDF and in EHF

For a document holding energy sources: the line text already names the metering point and
the period; the PDF gains a block per metering point above its lines — GSRN, address,
price area, the contract's name, the period, the kWh, "Forbruket er delvis stipulert"
when any settlement is `Estimated` — and, when the buyer is a person, a footer: "Prisene
på strøm kan sammenlignes på strømpris.no. Klager kan rettes til Elklagenemnda,
elklagenemnda.no" (bilingual by the buyer's language). EHF carries the metering point as
each line's `cac:Item/cac:AdditionalItemProperty` (`Name` "Målepunkt-ID", `Value` the
GSRN) and the period as the document's `cac:InvoicePeriod`, as work does; the per-line
period (BT-134/135) stays out (R3 D16). Nothing of a grid company's logo or original
invoice: gjennomfakturering is out (D12).

### D10 — Batch issuing: billing runs

`mig/00068_invoices_billing_runs.sql`: `invoices.billing_runs` (`id`, `through date`,
`issue bool`, `status` `queued|running|done|cancelled`, `created_by_user_id`,
`created_at`, `started_at`, `finished_at`, counts) and `invoices.billing_run_items`
(`run_id`, `customer_id`, `status` `queued|drafted|issued|skipped|failed`, `invoice_id`,
`code`, `detail`, `attempts`, `lease_id`, `lease_until`). `POST /api/v1/invoices/billing-runs`
(`invoices:access+invoices:create`, and `invoices:issue` when `issue`): `{through,
customerIds? (else every customer DueSupplies names, ≤ 5 000), issue}` — the directory
and `DueSupplies` are read before the run row is written; one item per customer; 409
`billing_run_active` while another run is not done. The worker `invoices-billing` (always
on, the reminder worker's lease shape, 5 items a cycle, three attempts with backoff)
takes an item, and in the API's own code path — `fromConsumption` then, when `issue`,
`issueLocked` and `storeAfterIssue` — drafts and issues under the run's creator as the
actor (`IssuedBy`), splitting a customer across documents by metering point when
`too_many_lines`; a wizard refusal is a `skipped` item with its code (`settlement_blocked`
names the point), an issue refusal `failed` with its code, and nothing is retried that
was refused. `GET /billing-runs`, `GET /billing-runs/{id}` (items, paged, filter by
status), `POST /billing-runs/{id}/cancel` (queued items only). Sending is not part of the
run: the documents are sent by the existing send paths or a later "send all issued" that
phase 5 names as a follow-up. **`module.Workers`** learns the slots the run needs:
`Consumption`, `Projects` and the three `Billable*`, resolved as Compose resolves them
(the worker-mode `Deps` then carries them; MB rule 5's `Workers` paragraph says so).

### D11 — Frontend

- **Energy**: a **Prices** page (`/energy/prices`): the coverage table per area, a chart
  of one area for a day or a month, **Import prices**; a **Tariffs** page (`/energy/tariffs`)
  with the list, a tariff editor (header, versions, components with the kind's fields),
  archive; the metering point's page shows and edits the grid tariff, household and the
  tax class, and shows the derived VAT treatment; the supply-period modals take the power
  tariff and the cadence; a **Settlements** card on the point's page (the periods, state,
  kWh, amount, a blocked reason, the invoice number as a link) read from
  `GET /api/v1/energy/metering-points/{id}/settlements`.
- **Invoices**: the uninvoiced panel gains a **Consumption** section (due supplies per
  customer); a **From consumption** wizard (through date, the settlements with their
  blockers, the VAT codes); the draft editor shows the kind's sources with the point;
  **Billing runs** (`/invoices/billing-runs`): new run, the list, a run's items with
  their outcomes.
- i18n en and nb in each package's catalog; the host's navigation gains the pages under
  their modules' permissions.

### D12 — Out of scope, named

Advance invoicing and à-konto for energy (§ 7-1b (3)–(6)) · gjennomfakturering's exchange
(Ediel/NBS, the guarantee, forwarding payments, the grid company's original invoice on the
document) · computing strømstøtte or Norgespris (the grid company's) · leveringsplikt ·
the year-on-year consumption graph · a line's own period in EHF · per-component VAT · a
"send all" after a run · eFaktura and AvtaleGiro · the 15-minute feed (stored when it
comes) · pricing in EUR · Elhub (Energy phase 5).

### D13 — Documentation, as its own deliverable in each delivery

`R/energy.md` (new sections: Spot prices, Tariffs, Settlements, the contract, the holder,
the worker; the two new migrations in `sources`), `R/invoices.md` (Invoicing consumption,
the VAT codes, the document, Billing runs, the worker; "What comes next" updated),
`U/energy.md` and `U/invoices.md` in both languages, `A/installation.md` in both languages
(the two workers, the feed's three settings), `MB` (rule 5's slot list and `Workers`, rule
10's holder list and lock order), `en/reference/index.md` (the Energy row), `ROADMAP.md`
(Energy phase 4 and Invoices phase 5, delivery by delivery). `Docs-Impact: none` only on
test-only and plan commits.

## Deliveries

Each is one pull request against `main` after the previous landed, with its own plan under
`docs/superpowers/plans/`, one migration per schema, its docs inside.

| | Branch | Migrations | Holds |
| --- | --- | --- | --- |
| **A** Energy 4A — spot prices | `feat/energy-spot-prices` | `00065_energy_spot_prices.sql` | D2, the two permissions, the feed worker and its settings, the import, the Prices page; D13's pages for it |
| **B** Energy 4B — tariffs and the billing facts | `feat/energy-tariffs` | `00065_energy_tariffs.sql` | D3, D4, the two permissions, the Tariffs page, the point's and the supply period's new fields |
| **C** Energy 4C — settlements and the contract | `feat/energy-settlements` | `00066_energy_settlements.sql` | D1, D5, D6, the slot in `module`, MB, the Settlements card; Energy phase 4 done |
| **D** Invoices 5A — invoices from consumption | `feat/invoices-energy` | `00067_invoices_energy.sql` | D7, D8, D9, the panel section and the wizard |
| **E** Invoices 5B — billing runs | `feat/invoices-billing-runs` | `00068_invoices_billing_runs.sql` | D10, `Workers`' slots, the Billing runs pages; Invoices phase 5 done |

Migration numbers are the next free at the time of writing; each plan fixes its own
against `main` when it is cut.

## Testing

- **Energy**: the migrations' `applyUpDownUp` schema tests; the feed parser against a
  stored sample (24, 25 and 23 entries, the DST days), the fetch planner (which days, the
  cap, the backoff), the worker against an `httptest` feed with netguard refusing a
  private address; the import against files in both resolutions and a mixed one (refused);
  the tariff validation; the settlement computation table-driven on a point with
  PT1H and PT15M intervals — a spot month, a fixed month, a version change mid-month, a
  partial first month, a grid tariff with day/night and the capacity tier, the tax
  classes, the exemption derivation, each blocker, a correction's differences; the holder
  (`MarkInvoiced`'s three refusals, the release's tolerance) with the `pgx.Tx` fakes the
  other holders use; the contract's validation.
- **Invoices**: the wizard (lines, text in both languages, VAT codes, the cadence refusal,
  `too_many_lines`), the issue with the new holder in `InvoicedWorkOrder`, a correction
  line's negative net, the PDF block and footer (golden), EHF (golden, `mise run
  ehf:validate`), the run worker end to end on the test database (drafted, issued, skipped
  with the point named, failed, split).
- **Platform**: `compose_test.go` for the slot and `Workers`' new resolutions.
- **Frontend**: each new page and modal with fixtures patched for the wire changes.
- **Gates**: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`; the energy tests under a non-UTC zone as CI runs them.

## Readings on the record

1. "Grid tariffs" in Energy phase 4 means the installation's own tariff for points it
   is the grid operator of, not a replica of a grid company's sheet (R5 §3.3).
2. A settlement's period is the civil month, quarter or year in the point's market time
   zone, clipped to the supply period; never a calendar unit in UTC.
3. The spot line's unit price is a volume-weighted average printed for the reader; the
   amount is the exact Σ kWh × price, rounded once. The two agree to the øre by
   construction only when the average is printed with enough decimals; the amount wins.
4. A price gap blocks a spot settlement; a fixed-price settlement needs no prices.
5. An estimate is never computed by the module; an estimated figure is an `Estimated`
   interval someone entered, and the document says the consumption is estimated.
6. A correction is the difference, never a re-issue; the original invoice stays as it is.
7. `Settle` is a command on a read path: it writes drafts the caller does not own, on
   the pool, idempotently; it is documented as the one contract method that writes.
8. `project_id` null on `line_sources` for the kind; the three work kinds keep it NOT NULL
   by CHECK.
9. A negative correction is a line with a negative quantity, the deduction line's shape,
   and the document must still total positive.
10. The run acts as its creator: the stamps, the issue's `issued_by`, the timeline say
    who started the run.

## Open questions

- **U1** (R5) the Enova business amount: a tariff component's figure, the operator's to
  set; no default is shipped.
- **U2** the feed's resolution: stored as given; a day refused when it conflicts.
- **U3** a short first period: billed as is, the next on the cadence.
- **U4** the exempt EHF category: Z with the reason; the settings may point the exempt
  code at an E code instead.
- **U5** the markup as a separate line: yes.
- **U6** whether a consumer's quarterly cadence should be refused for `yearly` on the
  Energy side too: no — Energy does not read the directory; the wizard refuses.
