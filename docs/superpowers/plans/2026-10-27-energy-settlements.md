# Energy settlements and the consumption contract: implementation plan

> **For agentic workers:** one implementer per task, on disjoint files, none of them
> committing; the coordinator commits. Read the design
> (`docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md`, D1, D4, D5,
> D6, D8, D11, D13, "Deliveries", "Testing", "Readings on the record", the open
> questions) and its amendment (`docs/superpowers/specs/2026-10-26-energy-spot-price-service-design.md`,
> S10) before task 2, and delivery B's plan
> (`docs/superpowers/plans/2026-10-27-energy-tariffs.md`) for the shape it was run in.

**Goal:** one pull request against `main`, **delivery C** `feat/energy-settlements`
(Energy 4C): a supply period's billing periods priced into **settlements** — the priced
lines a customer owes for a metering point — stored as rows Energy owns, handed across
`contracts.ConsumptionProvider` (the sixteenth contract), stamped and released by
Energy's `InvoicedWorkHolder`, and shown on the metering point's page in a
**Settlements** card. Migration `00077_energy_settlements.sql` (the next free on `main`
at `323c0ac5`). Energy phase 4 is done with it.

**Architecture:** everything priced in `internal/energy` on the `energy` schema; the
contract in `internal/contracts/consumption.go`; the slot in `internal/module`. Nothing
of Invoices: the wizard, `line_sources`, the VAT codes and the document are delivery D,
billing runs and `module.Workers` learning the slot delivery E.

`srv/` is `apps/server/internal/`, `mig/` `srv/db/migrations/`, `R/`
`docs/src/content/docs/en/reference/`, `U/` `docs/src/content/docs/{en,nb}/user/`, `MB`
`docs/src/content/docs/en/contributing/module-boundaries.md`.

## Global constraints

- The branch is cut from `main`; never commit to `main`. If `main` takes 00077 while
  the branch is open, renumber in history before rebasing (`prs-land-by-rebase`).
- A fresh worktree runs `bun install --frozen-lockfile` before the frontend tests or
  `docs:check`.
- `TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable`.
- After an OpenAPI, queries or migration change: `cd apps/server && mise exec -- go
  generate ./...` (twice, never package-scoped), then `mise exec -- bun run gen:client`
  from the root. Generated files are committed. Kinds, states, units and blockers are
  **plain strings** in OpenAPI, never enums (delivery A's gotcha). `openapi/energy.yaml`
  is hand-sorted; only what is touched is sorted.
- New response fields on operations with recorded .NET exchanges are optional (the
  frozen corpus `openapi/testdata/exchanges/energy.jsonl` must still validate). The new
  operation has no recorded exchange; its fields are required where always sent.
- Every new operation is exercised by a test (`contracttest.RequireCoverage`).
- Money is exact decimal inside (`math/big.Rat`), JSON numbers on the wire, decimal
  text across the contract (`srv/contracts/actuals.go:65-68`).
- Commit by pathspec; no `add -A`, `add .`, `stash`, `reset --hard`, `--amend`.
  Subjects: `feat(energy): … (Energy 4C, task N)`, `feat(energy-ui): … (Energy 4C, task
  N)`, `docs(energy): … (Energy 4C, task N)`. `Docs-Impact: none — <reason>` only on
  plan and test-only commits.
- Every new test is shown able to fail: run it against the code before the change, or
  break the expectation once.
- **Finishing gates:** `mise run server:check`, `mise run server:test`,
  `mise run frontend:check`, `mise run docs:check`; the energy tests also under
  `TZ=America/New_York`.
- The pull request is pushed and opened as soon as the work is committed, before the
  local gates, so CI starts early; the gates then run locally and their findings are
  reported.

## How this plan reads the design where it leaves a choice open

1. **The schema** (`mig/00077_energy_settlements.sql`). `energy.settlements (id bigint
   identity, supply_period_id → supply_periods ON DELETE RESTRICT, metering_point_id →
   metering_points ON DELETE RESTRICT, customer_id integer (opaque), kind varchar(12)
   CHECK regular|correction, corrects_id bigint NULL → settlements (RESTRICT), period_start,
   period_end timestamptz CHECK end > start, state varchar(10) CHECK
   draft|invoiced|released, revision integer ≥ 1, kwh numeric(14,3), estimated bool,
   interval_count, expected_interval_count integer, blocked varchar(20) NULL CHECK the
   six blockers, blocked_detail varchar(200) NULL, amount numeric(14,2), currency char(3)
   DEFAULT 'NOK', contract_name varchar(120), computed_at, touched_at, created_at,
   updated_at timestamptz` and the stamp (`invoiced_invoice_id bigint`, `invoiced_number
   bigint`, `invoiced_at timestamptz`, `invoiced_by_user_id uuid`, `invoice_date date`,
   `released_at timestamptz`, a CHECK that the first five are all set exactly when
   `state = 'invoiced'`, and that a correction names `corrects_id` and a regular does
   not). The floor: `ux_settlements_live UNIQUE (supply_period_id, period_start, kind)
   WHERE state IN ('draft','invoiced')`. Indexes on `metering_point_id`, `customer_id`
   (the merge and `Settle` by customer) and `corrects_id`. `energy.settlement_lines
   (settlement_id → settlements ON DELETE CASCADE, position smallint, kind varchar(20),
   text varchar(120), band varchar(10) NULL, month date NULL, quantity numeric(14,3),
   unit varchar(10) CHECK kWh|month|year|pcs, unit_price numeric(14,6), amount
   numeric(14,2), vat_hint varchar(30) CHECK standard|exempt_household_north, PRIMARY KEY
   (settlement_id, position))`. RESTRICT on the supply period and the point: a priced
   period is a record, and a supply period or point with settlements is not deleted
   from under them (neither is deleted by any endpoint today; a cascade would hide one
   that was). `schema_energy_settlements_test.go` runs up/down/up and asserts the
   CHECKs and the live index; `schema_test.go`'s energy `wantTables` gains both.
2. **Billing periods** (D4, `settlement_periods.go`, pure). The civil month, quarter
   (Jan/Apr/Jul/Oct) or year in the point's market time zone (`marketTimeZone`),
   clipped to the supply period's `[start, end)`, as instants. A period is **due**
   when its end is on or before both `Through` and now — the design says "before now"
   for an open supply period; a period of an ended supply period ending in the future
   is not settled either, since its consumption cannot be there yet. `Through` crosses
   the contract as an instant: Invoices passes the start of the day after its "through"
   date (delivery D). A billing period never starts before the supply period nor
   before **2000-01-01**, a floor that keeps a malformed start from enumerating
   centuries.
3. **The intervals** (D5). A period's current intervals are those with `start ≥
   period_start AND end ≤ period_end`; any current interval overlapping a bound without
   lying inside makes the period `incomplete` ("an interval crosses <instant>"), and so
   do two current intervals that overlap each other (mixed resolutions written over one
   another): the module never prices one kWh twice. The finest length present is the
   expected unit; `MissingIntervals = ceil((period length − Σ lengths) / finest)`,
   which counts a mixed PT1H/PT15M period correctly where a plain count would not.
   `Estimated` is any interval of quality `Estimated` (D5's word; `Corrected` is a
   measurement, not an estimate).
4. **The prices** (D5 + S10). A consumption interval is priced by the spot-price
   intervals of the point's area that cover it, **time-weighted**, its kWh spread
   evenly over its length: when one price interval contains it — the design's rule,
   and every case where prices are as coarse as consumption — that is exactly the
   price containing its start; when prices are finer (quarter-hourly prices since
   2025-10-01 against hourly consumption), the hour takes the mean of its quarters
   rather than its first quarter's price. A part of an interval no price covers is
   `price_gap` ("no price for <area> at <instant>"). **S10:** only `state = 'final'`
   prices price; a period whose prices include a `preliminary` one is blocked
   **`price_preliminary`** with the detail "prices for <day> are preliminary" (the
   civil day, market zone) — a sixth blocker after `price_gap`, rather than folding it
   into the gap, so the card and Invoices can say why. Prices are read only when a
   power version of the period holds `spot_energy`.
5. **The tariffs.** The power tariff is the supply period's `tariff_id`; the grid tariff
   the point's `grid_tariff_id` (optional: none means no grid lines). Each civil day of
   the period (the day of an interval's start, market zone) takes the version whose
   `[valid_from, valid_to)` contains it, read as of the computation (`archived` tariffs
   still price). A period whose power tariff is unset, or one of whose days no power
   version covers, is `no_tariff` (detail "no power tariff" / "<tariff name> has no
   version on <day>"); a grid tariff with an uncovered day likewise. Days are counted
   for proration by their midnight: a civil day belongs to the period that contains its
   start, so two supply periods meeting at 14:00 never both count that day.
6. **The lines** (D3's table), one per (version, component) — a version change
   mid-period gives a component two lines, each with its own days' kWh and rate — and
   two for a `grid_energy` component with a night rate (`band` `day` and `night`, the
   text the component's name for both: the words are Invoices' to add per language),
   one per (calendar month, version) for `grid_capacity` (`month` the month's first
   day). Order: the kinds-table order, power before grid within `monthly_fee`, then the
   version's `valid_from`, the component's position, `day` before `night`, the month.
   - `spot_energy`: quantity the kWh, amount `round2(Σ kWh × price)` exactly, unit price
     the amount before rounding ÷ kWh to six decimals (0 when no kWh). Readings on the
     record 3: the amount wins.
   - `fixed_energy`, `markup`, `electricity_tax` (`rate` ordinary, `rate_reduced`
     reduced, no line when exempt), `enova_levy` for a household (`rate`): kWh × rate.
   - `monthly_fee`, `grid_fixed`: quantity the months covered, Σ per calendar month of
     (counted days ÷ the month's days), rounded to three decimals **first**, amount
     `round2(quantity × rate)`, so quantity × unit price is the amount on the document.
   - `grid_energy`: day when the interval's local start hour is in `[day_from, day_to)`
     on any day, except on Saturday and Sunday when `weekdays_only`, which are night
     throughout; night otherwise. Without `rate_night` one line at `rate`.
   - `grid_capacity`: PT15M (and any sub-hour) kWh summed to the clock hour; per civil
     day the highest hour; the three highest days averaged (fewer when the segment has
     fewer days); the tier whose `fromKw` is the highest not above that average (kWh in
     an hour is kW); quantity the month fraction as `monthly_fee` (a partial month is
     prorated, as the design prorates every monthly figure), unit `month`, unit price
     the tier's `perMonth`.
   - `enova_levy` for a business: `rate_business` per year, prorated by counted days ÷
     the year's days, quantity three decimals first, unit **`year`** (the design's
     units have no year; Invoices maps it in D).
   - Every amount two decimals half away from zero, every quantity three, unit prices
     six; `Amount` is Σ of the rounded line amounts. Every line carries the point's
     `vatTreatment(household, country, postal)` (D8). Currency `NOK`.
   A **blocked** settlement carries no lines and amount `0.00`, its kWh and
   `MissingIntervals` as found. Blockers in D5's order: `period_open`, `no_tariff`,
   `no_consumption`, `incomplete`, `price_gap`, `price_preliminary`; the first wins.
7. **Draft rows** (D5). `Settle` and `Settlements` both recompute every draft they
   answer and write it when any figure changed (kWh, amount, lines, blocker and its
   detail, estimated, the counts, the period's end, the contract name), bumping
   `revision`; `touched_at` is set on every answer. `Settlements` writes too because a
   revision is how Invoices judges freshness (R3 D2): answering new figures under the
   stored revision would make the holder refuse what Invoices just refreshed. Each
   settlement is written in its own short transaction: the live row locked `FOR NO KEY
   UPDATE`, compared, written; an `invoiced` live row is never touched by `Settle`
   (that is a correction's business). Energy cannot see `line_sources` (MB rule 1), so
   "drafts nobody holds" reads as **drafts nobody has touched for thirty days**: a held
   draft is refreshed by Invoices' freshness reads; one deleted from under a draft
   invoice is answered absent, which Invoices already reads as `source_not_invoiceable`.
   Housekeeping runs in `Settle` for its supply periods (also deleting drafts of
   periods no longer due, e.g. after the supply period was cancelled or its end moved)
   and in the spot-price worker's cycle for all, when that worker runs.
8. **Corrections** (D5). For each supply period `Settle` touches, every period with an
   invoiced regular settlement whose `period_end` lies within the twelve months before
   now is recomputed when an interval of the point in the period, a price of the area
   in the period, or the tariffs that priced it changed after the latest invoiced
   baseline's `computed_at` (`received_at`, `received_at`, `updated_at`; or when a
   correction draft is already live). The baseline is the invoiced regular plus its
   invoiced corrections, summed per line key `(kind, text, band, month, unit)`. The
   correction's lines are the differences per key in the recomputed order (keys only
   in the baseline after), quantity and amount the differences, unit price the
   recomputed line's (the baseline's for a vanished key); its kWh the kWh delta; lines
   whose quantity and amount are both zero are left out; a correction with none is not
   written and an existing correction draft is deleted. A blocked recomputation writes
   no correction. Line kinds stay the components' kinds; `correction` is the
   settlement's kind (the design lists it among line kinds too; nothing needs both).
9. **The contract** (`srv/contracts/consumption.go`, D1's text). Beyond D1's types:
   `Settlement.BlockedDetail string` (the sentence of reading 4 and 5);
   `SettlementLine.Band` (`day` | `night` | "") and `Month *time.Time`; `InvoiceStampRef
   {InvoiceID, Number int64; IssueDate, InvoicedAt time.Time; InvoicedBy uuid.UUID;
   ReleasedAt *time.Time}`; `DueSuppliesPage{Supplies []DueSupply; More bool}`;
   `SettlementsPage{Settlements []Settlement; More bool}`. Validation: a due-supplies
   request names 1…`MaxActualsRequests` customers, no duplicates, `Through` set; a settle
   request exactly one of customers (≤ `MaxActualsRequests`) and supply periods (≤
   `MaxBillableRows`), no duplicates, `Through` set; `ValidateSettlementIDs` 1…
   `MaxBillableRows`, no duplicates. The constants for kinds, states, blockers, units,
   bands and VAT hints are exported. `DueSupply.Address` is "street, postal city".
   `DueSupplies` judges `Blocked` by computing the first due period without writing.
   The provider is built from `Deps.Pool` and `Deps.Clock`; it never reads the
   directory.
10. **The slot** (D1). `Module.Consumption func(Deps) contracts.ConsumptionProvider`,
    `Deps.Consumption`, resolved by `soleProvider` after the three `Billable*` (the
    design's "after `Directory` and in the `Billable*` group"); nil when Energy is off.
    `compose_test.go`: injected, duplicate refused, disabled nil, a preset surviving.
    `module.Workers` is not taught the slot: delivery E.
11. **The holder** (D6). `contracts.WorkSourceSettlement = "energy.settlement"`, last in
    `InvoicedWorkOrder`. Energy's `InvoicedWork`, from `Deps.Logger` alone.
    `MarkInvoiced` locks the settlements by id ascending `FOR NO KEY UPDATE` and judges
    in R3's order: missing → `source_not_invoiceable`; `invoiced` →
    `source_already_invoiced`; `released`, or blocked as stored → `source_not_invoiceable`;
    then the settlement recomputed inside `tx` with `ref.IssuedAt` as now (the holder's
    only clock) — a blocker → `source_not_invoiceable`, any figure different from the
    stored row → `source_changed`; then the source's revision, currency and amount (by
    value) against the row → `source_changed`. It stamps `state = invoiced`, the five
    stamp columns from `InvoiceRef` (`invoiced_at = ref.IssuedAt`), `revision + 1`.
    The recomputation's own reads (intervals, prices, tariffs) take no locks: an
    interval written after the stamp is a correction's business. `ReleaseInvoiced` takes
    the same locks, moves `invoiced → released` for the rows carrying `ref.ID`, sets
    `released_at = ref.IssuedAt`, keeps the stamp columns as history (the CHECK allows
    them on a released row) and warns and skips a row not carrying the stamp. Tests on
    real transactions rolled back, as Time's and Projects' are.
12. **The endpoint.** `GET /api/v1/energy/metering-points/{id}/settlements`,
    `x-vantigo-access: permission:energy:supply-periods-view+energy:consumption-view` —
    the design names none: a settlement is a supply period's consumption, priced, so
    it needs exactly what reading both of those needs, and no new key. It **writes
    nothing**: for every non-cancelled supply period of the point, the due periods
    (through now) whose end lies within the last 24 months, each answered as the stored
    live settlement (an invoiced one as frozen, a draft recomputed in memory) or, when
    none is stored, computed in memory with no `id` (state `draft`); plus every stored
    released settlement and correction in the window. Newest period first, then regular
    before correction, then id. 404 for an unknown point. `SettlementResponse` mirrors
    `contracts.Settlement` with numbers as JSON numbers and `invoice` `{id, number,
    issueDate, invoicedAt}` when stamped; kinds, states, blockers, units plain strings.
13. **The customer merge** re-points `energy.settlements.customer_id` with the supply
    periods (`RepointSettlementsCustomer`, kind `energy.settlements` in the holder's
    answer). Settlements hold no personal data beyond the opaque customer id: the GDPR
    export is unchanged.
14. **The frontend.** A **Settlements** card on the metering point's page, shown when
    the caller holds both view permissions: a table (period, supply period's customer
    is not shown — the supply-period table above has it —, state, kWh, amount, a
    blocked reason in words with its detail, "Estimated" when so, the invoice number
    as a link to `/invoices/{id}` when stamped, a correction marked as such); an empty
    state ("No billing period has closed yet."). i18n en and nb.

## File structure

| File | Responsibility | Task |
| --- | --- | --- |
| `mig/00077_energy_settlements.sql`, `srv/db/schema_energy_settlements_test.go`, `srv/db/schema_test.go`, `srv/energy/sqlc.yaml` | the tables | 2 |
| `srv/energy/queries/settlements.sql`, `customer_references.sql` | the reads and writes | 2 |
| `openapi/energy.yaml` | the GET and `SettlementResponse` | 2 |
| `srv/contracts/consumption.go` (+ test), `invoiced_work.go` | the contract, the kind | 2 |
| `srv/module/module.go`, `compose.go` (+ test) | the slot | 2 |
| `srv/energy/settlement_periods.go` (+ test), `settlement_model.go`, `settlement_store.go` | the period calculator, the in-memory settlement, the row writer | 2 |
| `srv/energy/settlement_compute.go` (+ test) | readings 3–6, 8: the computation and the correction's differences | 3 |
| `srv/energy/consumption_provider.go`, `invoiced_work.go`, `settlements.go`, `module.go`, `customer_references.go`, `spot_prices_worker.go` (+ tests) | readings 7, 9, 11–13 | 4 |
| `apps/energy/frontend/src/api/settlements.ts`, `pages/-settlements-card.tsx`, `pages/metering-points.$meteringPointId.tsx`, `i18n.ts` (+ tests) | reading 14 | 5 |
| `R/energy.md`, `R/index.md`, `MB`, `U/energy.md` ×2, `ROADMAP.md` | D13 | 6 |

## Tasks

- [ ] **1. This plan**, committed:
  `docs(energy): the plan for settlements and the consumption contract (Energy 4C)`,
  with `Docs-Impact: none — plan only`.

- [ ] **2. The schema, the queries, the contract, the slot and the shared helpers**
  (readings 1, 2, 9, 10, 12's schema), by the coordinator.
  - The migration and its schema test; the queries; `go generate` twice.
  - OpenAPI: the GET and its schemas; the handler stubbed (501) until task 4.
  - `contracts/consumption.go` with `Validate` tests; `WorkSourceSettlement` last in
    `InvoicedWorkOrder`; `Module.Consumption`, `Deps.Consumption` and the compose
    resolution with its tests.
  - `settlement_periods.go` (the billing periods, due, the day counting) with its table
    test; `settlement_model.go` (the in-memory settlement and line, rounding helpers,
    the comparison of two computations); `settlement_store.go` (load a settlement with
    its lines, write a draft — insert or update with the revision bump — and replace
    its lines).
  - **commit** `feat(energy): settlements — the schema, the queries, the contract and the slot (Energy 4C, task 2)`.

- [ ] **3. The computation** (readings 3–6, 8), one implementer.
  - `computeSettlement(ctx, q, input, now) (settlement, error)`, deterministic for a
    database state, over a `store.DBTX` so the holder can run it inside `tx`; and
    `correctionOf(recomputed, baseline)`.
  - Tests on the test database, table-driven on a point with PT1H and PT15M intervals:
    a spot month (hourly prices, and quarter-hourly prices against hourly consumption),
    a fixed month, a version change mid-month, a partial first month, a quarter, grid
    day/night with `weekdays_only`, the capacity tier over three days with PT15M
    summed to the hour, the three tax classes, the Enova levy for a household and a
    business, the exemption hint, each blocker (including `price_preliminary` and an
    interval crossing the period's end), a correction's differences.

- [ ] **4. The provider, the holder, the endpoint and the merge** (readings 7, 9,
  11–13), one implementer.
  - `consumption_provider.go`: `DueSupplies`, `Settle`, `Settlements`, the
    housekeeping; `invoiced_work.go`: the holder; `settlements.go`: the GET; `module.go`
    declares `Consumption` and `InvoicedWork`; the merge; the worker's housekeeping
    call.
  - Tests: the contract's rules (validation refused, idempotent `Settle`, a revision
    bump on a changed interval, no bump when nothing changed, a blocked settlement
    answered, `More`, missing ids absent, a correction after an invoiced period's
    interval changed, housekeeping); the holder (`MarkInvoiced`'s three refusals in
    order, the stamp, `source_changed` on a recomputation differing, the release and its
    tolerance, the lock order by id); the GET (computed, stored, invoiced with its
    invoice, 404, the authorization table's rows); the merge.

- [ ] **5. The Settlements card** (reading 14), one implementer.
  - The API client, the card, its place on the page, i18n en and nb; tests for the
    card's rows, blockers, the invoice link, the empty state, the permission gate;
    `bun run translations:check`.

- [ ] **6. Documentation** (D13 for C), one implementer. `R/energy.md`: a
  **Settlements** section (billing periods, the computation with each kind's rule, the
  blockers, drafts, corrections, the endpoint and its permission), **The consumption
  contract** (`ConsumptionProvider`, the slot, the provider's rules), **The invoiced-work
  holder** (the judging order, the stamp, the release); the migration in `sources`; the
  merge's new kind. `MB` rule 5's slot list and rule 10's holder list and lock order.
  `R/index.md`'s Energy row. `U/energy.md` en then nb: "Read a metering point's
  settlements". `ROADMAP.md`: Energy phase 4 delivery C done, the phase done.
  - Tasks 3–6 run in parallel; the coordinator commits them as
    `feat(energy): settlements — the computation, the contract's provider, the invoiced-work holder and the endpoint (Energy 4C, task 3–4)`,
    `feat(energy-ui): the metering point's Settlements card (Energy 4C, task 5)`,
    `docs(energy): settlements and the consumption contract — the reference, the boundaries and the user guide in both languages (Energy 4C, task 6)`.

- [ ] **7. Review, the pull request and the gates.** An opus code review (read-only,
  probe tests through `go test -overlay`) and a docs pass against the code (labels from
  `i18n.ts`, en and nb), in parallel. The findings go in one
  `fix(energy): the review's findings — … (Energy 4C)`. Push and `gh pr create --base
  main` at once; then the four gates and the energy tests under
  `TZ=America/New_York`, and their findings reported. Do not merge.

## Self-review

- D1 → readings 9, 10, tasks 2 and 4; D4's billing period → reading 2; D5 → readings 1,
  3–8, tasks 2–4; S10 → reading 4's `price_preliminary`; D6 → reading 11, task 4; D8's
  hint → reading 6; D11's Settlements card → reading 14, task 5; D13 → task 6; the
  design's Testing bullets for C (the settlement computation table, the holder, the
  contract's validation, `compose_test.go` for the slot, the migration's
  `applyUpDownUp`) → tasks 2–4.
- Beyond the design's words, named: `price_preliminary` and `BlockedDetail` (reading
  4), the time-weighted price for finer prices (reading 4), `Band`/`Month` and the
  `year` unit (readings 6, 9), the capacity proration (reading 6), `Settlements`
  writing (reading 7), "untouched for thirty days" for "nobody holds" (reading 7), the
  tariffs' `updated_at` among a correction's triggers (reading 8), the endpoint's
  permission and its window (reading 12), the merge re-pointing settlements (reading
  13). None adds a contract method or touches Invoices.
- Names used the same everywhere: `energy.settlements`, `energy.settlement_lines`,
  `ConsumptionProvider`, `DueSupplies`, `Settle`, `Settlements`,
  `WorkSourceSettlement`, `energy.settlement`, `regular`, `correction`, `draft`,
  `invoiced`, `released`, `period_open`, `no_tariff`, `no_consumption`, `incomplete`,
  `price_gap`, `price_preliminary`.
- Not here: anything in Invoices (D), billing runs and `module.Workers` (E).
