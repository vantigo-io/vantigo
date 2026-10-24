# Energy phase 4, delivery A — spot prices: implementation plan

> **For agentic workers:** one implementer per task, a reviewer after each; tick the
> boxes as you go. Read the spec's D2 and D11–D13 before task 1, and R5 §5 for the feed.

**Goal:** `feat/energy-spot-prices`, the first of the five deliveries of
`docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md` (delivery A,
D2): day-ahead prices per price area stored as intervals, fetched daily by a worker from
a configured feed or imported from a file, read by a Prices page; migration
`00065_energy_spot_prices.sql`; operationIds `getEnergySpotPrices`,
`getEnergySpotPricesCoverage`, `postEnergySpotPricesImport`; permissions
`energy:prices-view`, `energy:prices-manage`; worker `energy-spot-prices`; settings
`ENERGY_SPOT_PRICE_FEED_ENABLED`, `ENERGY_SPOT_PRICE_FEED_URL`, `ENERGY_SPOT_PRICE_FEED_POLL`.

**Architecture:** everything in `internal/energy` on the `energy` schema; the fetch
through a guarded transport copied from the calendar subscriptions'
(`srv/calendar/subscriptions_fetch.go`); the worker under an advisory lease
(`srv/invoices/ehf_events_worker.go:216-240`'s `underLease`); the import through
`internal/fileimport` as the consumption import (`srv/energy/consumption_import.go`).
No contract, no slot, nothing of Invoices: those are deliveries C and D.

**Spec:** the design above is binding; where this plan reads it, the readings are
listed below. `srv/` is `apps/server/internal/`, `mig/` `srv/db/migrations/`, `R/`
`docs/src/content/docs/en/reference/`, `U/` `docs/src/content/docs/{en,nb}/user/`, `A/`
`docs/src/content/docs/{en,nb}/admin/`.

## Global constraints

- Branch `feat/energy-spot-prices` (pushed from this worktree's branch); never commit to
  `main`; no `--no-verify` unless the coordinator commits an agent's finished group.
- `TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable`.
- After an OpenAPI, queries, sqlc or migration change: `cd apps/server && mise exec -- go
  generate ./...` (twice, never package-scoped), then `mise exec -- bun run gen:client`
  from the root. Generated files are committed.
- Frontend fixtures are patched in the same commit as a wire change; docs change with the
  code, per task.
- Commit by pathspec; no `add -A`, `add .`, `stash`, `reset --hard`, `--amend`.
- Trailers: `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; `Docs-Impact:
  none — <reason>` above it on test-only or plan commits. Subjects
  `feat(energy): … (Energy 4A, task N)`, `feat(energy-ui): …`, `docs(energy): …`.
- Every new test is shown able to fail (run it against the code before the change, or
  break the expectation once).
- **Finishing gates:** `mise run server:check`, `mise run server:test`, `mise run
  frontend:check`, `mise run docs:check`; the energy tests also under
  `TZ=America/New_York` as CI runs them.

## How this plan reads the spec where it leaves a choice open

1. The migration is `mig/00065_energy_spot_prices.sql`; it is added to
   `srv/energy/sqlc.yaml`, `R/energy.md`'s `sources`, and gets
   `srv/db/schema_energy_spot_prices_test.go`.
2. `spot_price_fetches.status` is one of `ok`, `pending` (today or tomorrow not yet
   published), `missing` (a past day the feed has no file for), `failed`; `attempts`
   counts the requests since the record last changed status (the review's finding: a
   day's hours of `pending` must not spend a later status's budget); `next_attempt_at`
   is when the planner may ask again.
3. The planner's window is the civil days in Europe/Oslo from `today − 7` to `today + 1`;
   the feed's own days are Europe/Oslo days whatever the area, and the stored intervals
   are instants.
4. Retry: `pending` → in an hour; `missing` → in a day, up to seven `missing` answers
   in a row, then left `missing` and asked again only by a file; `failed` →
   `min(6h, 15min × 2^(attempts−1))`, `attempts` the `failed` ones in a row.
5. A fetched day is written only when its intervals are contiguous, lie within the day
   (the first `time_start` at the day's local midnight, the last `time_end` at the next),
   and do not overlap a stored interval of another length; otherwise the fetch row is
   `failed` with the reason and nothing of the day is written.
6. A re-fetch or a re-import of an interval already stored with the same
   `(area, start, end)` replaces its figures when they differ (`source` and `received_at`
   with them) and counts as `replaced`; equal figures count as `unchanged`.
7. The areas in use are `SELECT DISTINCT price_area FROM energy.metering_points WHERE
   EXISTS (a non-cancelled supply period)` ∪ `SELECT DISTINCT price_area FROM
   energy.spot_prices`; an installation with no points fetches nothing.
8. `GET /spot-prices` refuses `to − from > 35 days` (400 on `to`) and `from >= to`; it
   answers the intervals whose `start >= from AND start < to`, ascending.
9. The import's header words (after `fileimport.HeaderKey`): area — `area`, `price_area`,
   `pricearea`, `prisområde`, `elspotområde`, `område`, `sone`; start — `start`, `from`,
   `fra`, `time_start`, `starttid`; end — `end`, `to`, `til`, `time_end`, `sluttid`; NOK
   price — `nok_per_kwh`, `price`, `pris`, `kr/kwh`, `nok/kwh`; `øre/kwh`, `ore_per_kwh`
   read as øre (divided by 100); EUR — `eur_per_kwh`, `eur/kwh`; rate — `exr`,
   `exchange_rate`, `kurs`. Each header matched at most once; area, start and the NOK
   price are required. No mapping part: the import is a fixed layout, unlike the
   consumption import whose files come from many systems.
10. With an `end` column, rows of one area may leave gaps between them (one file fills
    several separate days); an unordered or overlapping row is the row's error. Without
    one, the interval closes at the next row's start of the same area in file order, and
    the area's last row takes the length of the row before it — an hour only when the
    area has a single row (a quarter-hour file's last row must not run into the next
    day); a row whose successor starts later than that length after it, or earlier, is
    the row's error. With an `end` column an interval is at most an hour long (the
    review's finding: a year-long row would block every feed day of that year).
11. The import answers `SpotPriceImportResult{dryRun, imported, rows, errorCount,
    errors[{row, column, message}] (first 200), areas[{area, inserted, replaced,
    unchanged, rangeStart, rangeEnd}]}`; 422 with the same body when any row has an error
    on a run; 200 on a dry run.
12. The worker runs under lease key `ENRGSPT1` (`0x454E52475350 5431` as one int64:
    `0x454E524753505431`), on when `ENERGY_SPOT_PRICE_FEED_ENABLED`, polling
    `ENERGY_SPOT_PRICE_FEED_POLL` (default 15 m, minimum 1 m); a `Config` without the
    switch leaves the worker out, as the import worker is.
13. The feed URL template must be `https`, must contain `{year}`, `{month}`, `{day}` and
    `{area}`, and is rendered with zero-padded month and day; a template failing this is
    a configuration problem at start-up.
14. The frontend's Prices page lives at `/energy/prices` behind `energy:prices-view`;
    **Import prices** is shown when the host says the caller holds
    `energy:prices-manage`. The chart is the consumption chart's library in a line
    series of NOK/kWh per interval start, one area, a day or a month; the coverage table
    is the page's first card.
15. The admin settings table is `A/authentication.md`'s environment table (where
    `ENERGY_IMPORT_*` are), and the worker paragraph `A/installation.md`'s, both
    languages.

## File structure

| File | Responsibility | Task |
| --- | --- | --- |
| `mig/00065_energy_spot_prices.sql` | the two tables, the indexes, the exclusion | 2 |
| `srv/db/schema_energy_spot_prices_test.go` | the migration's up/down/up and shape | 2 |
| `srv/energy/queries/spot_prices.sql` | upsert, read, coverage, fetch rows, areas in use | 2 |
| `srv/energy/sqlc.yaml` | the new schema file | 2 |
| `openapi/energy.yaml` | the three operations and their schemas | 2 |
| `srv/energy/module.go` | the two permissions, the worker, the body limit | 2, 4 |
| `srv/config/config.go` | the three settings | 2 |
| `srv/energy/spot_prices.go` | the model helpers, the reads, the write (plan and apply) | 3 |
| `srv/energy/spot_prices_import.go` | the file import | 3 |
| `srv/energy/spot_prices_feed.go` | the template, the guarded client, the parser | 4 |
| `srv/energy/spot_prices_worker.go` | the planner, the lease, the cycle | 4 |
| `apps/energy/frontend/src/api/spot-prices.ts` | the client | 5 |
| `apps/energy/frontend/src/pages/prices.tsx`, `-import-prices-modal.tsx`, `-price-chart.tsx` | the page | 5 |
| `apps/host/frontend/src/routes/energy/prices.tsx`, `apps.ts`, host i18n | the route and the navigation | 5 |
| `R/energy.md`, `U/energy.md` ×2, `A/installation.md` ×2, `A/authentication.md` ×2, `en/reference/index.md`, `ROADMAP.md` | the pages | 6 |

## Tasks

- [ ] **1. This plan**, committed: `docs(energy): the plan for Energy phase 4 delivery A —
  spot prices`, with the research and the design.

- [ ] **2. The schema, the queries, the contract and the settings** (`mig/00065`,
  `queries/spot_prices.sql`, `sqlc.yaml`, `openapi/energy.yaml`, `config.go`,
  `module.go`'s permissions).
  - The migration as D2 with readings 1–2; `applyUpDownUp(t, url, 64)` in the schema
    test, asserting the unique index, the exclusion constraint and the CHECKs.
  - Queries: `UpsertSpotPrice` (`ON CONFLICT (price_area, "start") DO UPDATE … WHERE
    the figures differ`, returning whether a row was inserted/replaced — done as two
    statements: `GetSpotPricesByStarts` then insert/update, inside one transaction, so
    the counts are exact), `ListSpotPrices(area, from, to)`, `SpotPriceCoverage()` (per
    area: min start, max end, the days of the last 30 with no interval starting in them —
    computed in Go from `ListSpotPriceDays(area, since)`), `ListAreasInUse`,
    `GetSpotPriceFetch`, `ListSpotPriceFetches(area, from, to)`, `UpsertSpotPriceFetch`,
    `LatestSpotPriceFetch(area)`.
  - OpenAPI: `GET /api/v1/energy/spot-prices` (`SpotPriceInterval{priceArea, start, end,
    nokPerKwh, eurPerKwh?, exchangeRate?, source}`), `GET /api/v1/energy/spot-prices/coverage`
    (`SpotPriceCoverage{areas[{priceArea, firstStart?, lastEnd?, daysMissingLast30,
    lastFetch?{day, status, at, error?}}]}`), `POST /api/v1/energy/spot-prices/import`
    (reading 11) — each with `x-vantigo-access`, the first two `permission:energy:prices-view`,
    the import `permission:energy:prices-manage+energy:prices-view`.
  - Config: `EnergySpotPriceFeedEnabled` (default false), `EnergySpotPriceFeedURL`
    (reading 13), `EnergySpotPriceFeedPoll` (reading 12), in a `energySpotPrices` loader
    beside `energyImport`, with its tests.
  - Permissions: `energy:prices-manage` and `energy:prices-view` in alphabetical place
    (`TestModule_DeclaresItsPermissionCatalog`), `R/energy.md`'s table in the same commit.
  - Regenerate; `go build ./...` passes with the handlers stubbed (`501` is not an
    option with the strict server: add the three methods to `server` returning a bare
    error until task 3).
  - **commit** `feat(energy): spot prices — the schema, the queries, the contract and the
    settings (Energy 4A, task 2)`.

- [ ] **3. The reads, the write and the import** (`spot_prices.go`,
  `spot_prices_import.go`).
  - Tests first: the write's plan against stored rows (inserted / replaced / unchanged,
    the exclusion violation surfaced as the row's error), the read's bounds (reading 8),
    the coverage's missing-day count across a DST day, the import against three files in
    `testdata/spot_prices/` — hourly NO1 with an `end` column, quarter-hourly NO2 without
    one, a mixed file whose second area overlaps stored hourly rows (refused, row named)
    — the øre header, the dry run writing nothing, the 422 body.
  - The handlers in the consumption import's shape (`consumption_import.go:645-760`):
    the parts, `fileimport.Read` with its limits, the layout from the header (reading
    9), the rows evaluated in Europe/Oslo with `localInstants`' DST handling, the plan,
    one transaction.
  - `module.go`: `limits` and `bodyLimits` for `postEnergySpotPricesImport` (the
    consumption import's figures).
  - **commit** `feat(energy): spot prices — the reads, the write and the file import
    (Energy 4A, task 3)`.

- [ ] **4. The feed and the worker** (`spot_prices_feed.go`, `spot_prices_worker.go`,
  `module.go`'s `workers`).
  - Tests first: the template's validation and rendering; the parser against
    `testdata/spot_prices/feed_2025-10-26_NO1.json` (25 entries), `…2026-03-29…` (23) and
    a plain day (24), and a malformed body; the planner (reading 3–4) as a pure function
    of `now`, the fetch rows and the areas; the cycle against an `httptest.Server` through
    `modtest.WithTransport` — a 200 written, a 404 for tomorrow `pending`, a 404 for a
    past day `missing`, a 500 `failed` with backoff, the twenty-request cap, the lease
    held by another connection skipping the cycle; netguard refusing a private address
    when the transport is the guarded one.
  - The guarded transport and client copied from the calendar's (`guardedTransport`,
    `subscriptionClient`), with `Accept: application/json` and a 1 MiB cap.
  - `workers(d)`: the import worker as today, plus `NewSpotPriceWorker(d)` when the feed
    switch is on; `TestModule_ContributesItsWorkers` extended.
  - **commit** `feat(energy): spot prices — the feed worker, energy-spot-prices (Energy
    4A, task 4)`.

- [ ] **5. The Prices page** (`apps/energy/frontend`, `apps/host/frontend`).
  - `api/spot-prices.ts` (query options for the intervals and the coverage, the import
    call), `pages/prices.tsx` (the coverage card, the chart card with area and day/month
    pickers, **Import prices**), `-import-prices-modal.tsx` (file, dry run, the result,
    import), `-price-chart.tsx`; tests for the page and the modal with `stubFetch`.
  - Host: `routes/energy/prices.tsx` (`createFileRoute("/energy/prices")`), the
    navigation entry under Energy (`apps.ts`, `navigation.prices` in the host i18n, both
    languages), `canImportPrices` from `useEnergyPermissions`.
  - i18n en and nb in `apps/energy/frontend/src/i18n.ts`; `bun run translations:check`.
  - **commit** `feat(energy-ui): the Prices page — coverage, the chart and Import prices
    (Energy 4A, task 5)` with `Docs-Impact: none — the user guide for the page lands in
    task 6`.

- [ ] **6. Documentation** (`R/energy.md`: a "Spot prices" section — the model, the feed
  and its worker, the import's layout and answers, the endpoints, the permissions,
  "Workers" and "Permissions" tables, the intro's scope; `U/energy.md` en then nb: a
  "Prices" section — where, the coverage, the chart, importing a file; `A/installation.md`
  en then nb: the worker in the list and a paragraph; `A/authentication.md` en then nb:
  the three settings; `en/reference/index.md`'s Energy row; `ROADMAP.md`'s Energy phase
  4 "Delivery A (done)" paragraph and the Invoices phase 5 pointer).
  - `mise run docs:check`.
  - **commit** `docs(energy): spot prices — the reference, the user guide and the
    operator pages in both languages (Energy 4A, task 6)`.

- [ ] **7. Review, gates and the pull request.** A code review pass (correctness, the
  guard, the lease) and a docs pass against the code, their findings in one `fix(energy):
  … (Energy 4A)` commit and a `docs(energy): the pages follow the review's fixes (Energy
  4A)` when needed; the four gates; `bun run tools/docs/check-coverage.ts --base
  origin/main`; push as `feat/energy-spot-prices`; `gh pr create --base main`; do not
  merge.

## Self-review

- D2 → tasks 2–4; D11's Prices page → task 5; D13's pages for A → task 6; the
  permissions → task 2 and 6; the settings → tasks 2 and 6.
- Names: `energy-spot-prices` (worker), `energy:prices-view` / `energy:prices-manage`,
  `getEnergySpotPrices`, `getEnergySpotPricesCoverage`, `postEnergySpotPricesImport`,
  `ENERGY_SPOT_PRICE_FEED_ENABLED|URL|POLL`, used the same everywhere.
