# Energy spot prices from the shared service, and price history: implementation plan

> **For agentic workers:** one implementer per task, a reviewer after each; tick the
> boxes as you go. Read the design
> (`docs/superpowers/specs/2026-10-26-energy-spot-price-service-design.md`) before task
> 1, and delivery A's plan (`docs/superpowers/plans/2026-10-24-energy-spot-prices.md`)
> for the code it extends.

**Goal:** two pull requests against `main` and one against `vantigo-infra`.

- **Delivery 1** `feat/energy-spot-price-service` (design S2–S7, S10): the service as a
  third spot-price source. Covers migration `00075_energy_spot_price_service.sql`, the
  settings `ENERGY_SPOT_PRICE_SERVICE_URL`, `ENERGY_SPOT_PRICE_SERVICE_KEY` and
  `ENERGY_SPOT_PRICE_HISTORY_FROM`, the `netguard` private-network exception, and the
  `energy-spot-prices` worker's service cycle with its live window and history walk.
  The intervals and coverage reads gain their new fields.
- **Delivery 2** `feat/energy-price-history` (S8): `getEnergySpotPricesSummary` and the
  Prices page's periods, comparison, figures and URL state.
- **infra** `vantigo-infra` `feat/spot-price-settings` (S9).

**Architecture:** everything in `internal/energy` on the `energy` schema, plus two
functions in `internal/netguard`. The service client is new and separate from the
feed's. The day judgement and the write helper are shared by both sources. No contract,
no slot, nothing of Invoices.

`srv/` is `apps/server/internal/`, `mig/` `srv/db/migrations/`, `R/`
`docs/src/content/docs/en/reference/`, `U/` `docs/src/content/docs/{en,nb}/user/`, `A/`
`docs/src/content/docs/{en,nb}/admin/`, `MB`
`docs/src/content/docs/en/contributing/module-boundaries.md`.

## Global constraints

- Each delivery is cut from `main` after the previous one lands
 ; never commit to `main`. The migration takes the next free
  number on `main` when the branch is cut. If `main` moves while the branch is open,
  renumber it in history before rebasing.
- A fresh worktree runs `bun install --frozen-lockfile` before the frontend tests or
  `docs:check`.
- `TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable`.
- After an OpenAPI, queries, sqlc or migration change: `cd apps/server && mise exec -- go
  generate ./...` (twice, never package-scoped), then `mise exec -- bun run gen:client`
  from the root. Generated files are committed. New status, state and source fields
  stay plain strings in OpenAPI, not enums: oapi-codegen renames existing constants
  when a new enum reuses a value (delivery A's gotcha).
- Frontend fixtures are patched in the same commit as a wire change; docs change with the
  code, per task.
- Commit by pathspec; no `add -A`, `add .`, `stash`, `reset --hard`, `--amend`.
  Subjects: `feat(energy): … (Energy 4A2, task N)`, `feat(netguard): …`,
  `feat(energy-ui): … (Energy 4A3, task N)`, `docs(energy): …`. Put
  `Docs-Impact: none — <reason>` on plan and test-only commits.
- Every new test is shown able to fail: run it against the code before the change, or
  break the expectation once.
- **Finishing gates** per delivery: `mise run server:check`, `mise run server:test`,
  `mise run frontend:check`, `mise run docs:check`; the energy tests also under
  `TZ=America/New_York`.

## How this plan reads the design where it leaves a choice open

1. The migration adds the columns `state`, `upstream`, `rate_source` and
   `upstream_version`, widens `ck_spot_prices_source` with `Service`, adds
   `ck_spot_prices_state`, and creates `energy.spot_price_history` (S2).
   `srv/db/schema_energy_spot_price_service_test.go` runs up/down/up and asserts the
   CHECKs. `schema_test.go`'s energy `wantTables` gains the new table.
2. Config: a `energySpotPriceService` loader beside `energySpotPrices`
   (`srv/config/config.go:883`). The URL is parsed with `url.Parse`: scheme `http` or
   `https`, a host, no `User`, no `RawQuery`/`Fragment`, `Path` in `""`/`"/"`. The key is
   non-empty and at most 200 bytes. `HISTORY_FROM` is a `YYYY-MM-DD` date. The one-source
   rule is a problem on `ENERGY_SPOT_PRICE_SERVICE_URL`. The key's field is added to the
   config's redaction list if one exists, and the problem's text never echoes the value.
3. netguard: `PrivateNetwork(addr)` is true for `10/8`, `172.16/12`, `192.168/16` and
   `fc00::/7` after `Unmap()`, and nothing else. NAT64, 6to4 and IPv4-compatible forms
   are **not** seen through: an embedded private address reached through a translator
   is not our network. `DialContextAllowingPrivate(resolver, dial, requirePrivate)`
   shares `resolve` and the dial loop with `DialContext`; the shared part is factored
   out, and `DialContext`'s behaviour and tests stay unchanged.
4. The service client (`srv/energy/spot_price_service_client.go`):
   `spotServiceClient{base *url.URL, key string, http *http.Client}`. The transport is
   built per worker, not `sync.OnceValue`, because it carries the configured host.
   `requirePrivate = base.Scheme == "http"`. A `hostOnly` round-tripper refuses another
   host with `netguard.ErrDisallowed`. Gzip is left to the transport
   (`DisableCompression: false`), and the 1 MiB cap is applied to the decoded body.
   `modtest.WithTransport` replaces the dialler in tests as it does for the feed.
5. Decoding: `json.Decoder.UseNumber()`. `priceEurPerMwh` → `eur_per_kwh` by
   `decimalShift(n, -3)` on the number's text, then `strconv.ParseFloat` and
   `roundPrice`. `priceNokPerKwh` and `exchangeRate` are parsed and rounded.
   `upstream` and `rateSource` must match `^[a-z][a-z0-9_]{0,19}$`, else the day fails
   with "the spot-price service answered an unknown source". `state` must be
   `final` or `preliminary`.
6. The day judgement moves out of `parseSpotFeedDay` into
   `judgeSpotPriceDay(rows, area, day, zone) (complete bool, reason string)` in
   `spot_prices_write.go`, used by the feed parser and the service. The feed's
   sentences and their tests are unchanged.
7. The write helper's `spotPriceRow` gains `State`, `Upstream`, `RateSource`,
   `UpstreamVersion`. `sameSpotPriceFigures` compares them too. Feed and import rows
   carry `State: "final"` and nil provenance. `planSpotPriceWrite` gains a `replaceDay`
   option for the service: when a conflict's stored rows of that civil day are all
   `Feed` or `Service`, the plan lists them in `deletes` and the day's rows as inserts.
   A conflict with any `Import` row stays a conflict (S6, Q2). `applySpotPriceWrite`
   deletes before it inserts, inside the area lock already taken.
8. Followed areas with the service: the `active` codes from `/api/v1/areas`; only when
   that request fails, `ListSpotPriceAreasInUse` instead (design S4 as amended). Codes outside `^(NO[1-5]|SE[1-4]|DK[12]|FI)$` are ignored, and the
   column is `varchar(4)`.
9. Live window: days `today−14 … today+1` in Europe/Oslo. Due when any day lacks an
   `ok` row whose time has come (delivery A's planner on the wider window), or when
   `max(fetched_at)` of the window's `ok` rows is older than 24 h. One request per due
   area, `currency=EUR`. Statuses and retries are delivery A's (its plan readings 2 and
   4), with `ok` meaning complete and final. A complete preliminary day is written and
   `pending` (retry 1 h).
10. History: per area, `cursor_day` starts at `today−15` and walks back in 35-day
    windows to `floor = max(coverage.firstStart's Oslo day, HISTORY_FROM)`. The walk is
    complete when `cursor_day <= floor`. A moved floor resumes it. A history window's
    days that are incomplete, without NOK or in conflict with an import add to
    `days_skipped` and are not retried. A failed request sets the backoff on the history
    row, not on fetch rows. `spot_price_history` is upserted in the window's
    transaction.
11. Budget: `maxSpotPriceFetchesPerCycle` (20) shared, live windows first. `Run`'s next
    tick is one minute while any followed area's history is incomplete and has no
    backoff pending, else the poll. `Interval()` still answers the poll for the runner's
    logs.
12. Failure sentences are listed as constants beside delivery A's (S7). A 429's
    `Retry-After` (seconds or HTTP date) sets `next_attempt_at` when it is later than the
    backoff. The service's problem `detail` is logged at debug level, never stored.
13. Coverage read: `source` is `service` | `feed` | `none` from the config. Per area,
    `followedByService` is whether the sync keeps a `spot_price_history` row for the
    area — it writes one for every area answered `active` —, absent before the sync has
    run once; read from the database, it holds in a split deployment too (as built: the
    first reading kept it in the worker's memory, which the API process does not have).
    `resolution` comes from the latest stored day's first interval, and `history` from
    `spot_price_history`.
14. Summary read (delivery 2): one query
    `SummariseSpotPrices(area, from, to, bucket)` with `date_trunc(@bucket, "start" AT
    TIME ZONE 'Europe/Oslo')`. The average is
    `sum(nok_per_kwh * extract(epoch from "end" - "start")) / sum(extract(epoch from "end" - "start"))`,
    rounded to six decimals. `preliminary` is `bool_or(state = 'preliminary')`. The
    bucket's `end` is `start + 1 <bucket>` computed in Oslo and converted back. Bounds as
    S8; `from`/`to` off a boundary answer 400 on the field.
15. The page (delivery 2): the period is a search parameter
    (`validateSearch` in `apps/host/frontend/src/routes/energy/prices.tsx`, the pattern of
    `routes/dashboard.tsx:995`): `areas` (comma list, ≤ 5), `period`
    (`day|week|month|year|range`), `date` (`YYYY-MM-DD`), `from`/`to` (`YYYY-MM`). The
    host passes them and a `navigate` callback to `PricesPage`. Charts stay on
    `@mantine/charts`: `LineChart` with `curveType="stepAfter"` for intervals, and
    `CompositeChart` (an `Area` for the band as `[min, max]`, a `Line` for the average)
    for summaries. Preliminary points use `strokeDasharray` on a second series that holds
    only the preliminary points. Colours come from the theme's categorical order.

## File structure

| File | Responsibility | Task |
| --- | --- | --- |
| `mig/00075_energy_spot_price_service.sql`, `srv/db/schema_energy_spot_price_service_test.go`, `srv/db/schema_test.go` | the columns, the history table | 2 |
| `srv/energy/queries/spot_prices.sql`, `srv/energy/sqlc.yaml` | provenance in the upsert, the day delete, history rows, summary (2) | 2, 8 |
| `openapi/energy.yaml` | the new fields; the summary (2) | 2, 8 |
| `srv/config/config.go` (+ test) | the three settings, the one-source rule | 2 |
| `srv/netguard/netguard.go` (+ test) | `PrivateNetwork`, `DialContextAllowingPrivate` | 3 |
| `srv/energy/spot_prices_write.go`, `spot_prices_feed.go` | the shared judgement, provenance, replace-day | 4 |
| `srv/energy/spot_price_service_client.go` (+ test) | the client, the decode | 5 |
| `srv/energy/spot_prices_worker.go`, `spot_price_service_sync.go` (+ tests) | the source switch, the live window, history | 5 |
| `srv/energy/spot_prices.go`, `module.go` | the reads' new fields, the worker built for either source | 2, 5 |
| `apps/energy/frontend/src/api/spot-prices.ts`, `pages/prices.tsx` | the coverage's source and History column (1); the periods, comparison, figures (2) | 6, 9 |
| `apps/host/frontend/src/routes/energy/prices.tsx`, `-prices-page.tsx` | the search parameters (2) | 9 |
| `R/energy.md`, `A/authentication.md` ×2, `A/installation.md` ×2, `A/transport-security.md` ×2, `MB`, `ROADMAP.md`, BD's amendment note | delivery 1's pages | 7 |
| `U/energy.md` ×2, `R/energy.md`, `ROADMAP.md` | delivery 2's pages | 10 |

## Tasks: delivery 1, `feat/energy-spot-price-service`

- [ ] **1. The design and this plan**, committed:
  `docs(energy): the design and the plan for spot prices from the shared service and price history`,
  with `Docs-Impact: none — design and plan only`.

- [ ] **2. The schema, the queries, the contract and the settings** (readings 1, 2, 13).
  - The migration and its schema test. Queries: `UpsertSpotPrice` and
    `UpdateSpotPrice` with the provenance columns, `DeleteSpotPricesByIDs`,
    `GetSpotPriceHistory`, `UpsertSpotPriceHistory`, `ListSpotPriceHistory`,
    `LatestSpotPriceResolution(area)`.
  - OpenAPI: `SpotPriceInterval` gains `state`, `upstream?`, `rateSource?`, and
    `source`'s description gains `Service`. `SpotPriceCoverage` gains `source` and per
    area `followedByService?`, `resolution?`, `history?{floorDay?, cursorDay?, complete,
    daysSkipped, error?}`. Plain strings throughout.
  - Config loader and tests; `R/energy.md`'s settings mention lands in task 7.
  - Feed and import paths write `state='final'`; their tests pass unchanged.
  - **commit** `feat(energy): the spot-price service — the schema, the contract and the settings (Energy 4A2, task 2)`.

- [ ] **3. The private-network exception in netguard** (reading 3).
  - Tests first: the range table of the design's Testing section, and
    `DialContextAllowingPrivate` with a canned resolver (private only and
    `requirePrivate` passes; public with `requirePrivate` refused; loopback and
    `169.254.169.254` refused either way; a mixed answer with one loopback address
    refused).
  - **commit** `feat(netguard): a private-network exception a caller asks for by name`
    (`Docs-Impact:` none here. `A/transport-security.md` changes in task 7 with its
    caller, and the commit says so).

- [ ] **4. The shared day judgement and the write's provenance and replace-day** (readings 6, 7).
  - Tests first: delivery A's feed tests pass untouched. New tests cover a preliminary
    day turning final (replaced), a version bump (replaced), quarter-hours over a `Feed`
    hourly day (deleted and inserted, counts), and quarter-hours over an `Import` day
    (conflict, nothing written).
  - **commit** `feat(energy): one day judgement for every source, provenance on a price and a fetched day replaced at another resolution (Energy 4A2, task 4)`.

- [ ] **5. The service client and the sync** (readings 4, 5, 8–12).
  - Fixtures under `srv/energy/testdata/spot_price_service/`, captured from a local
    `vantigo-spotprices` (`docker compose up`, a key, a `hks` backfill of a few days
    including 2025-10-26 and 2026-03-29, and the live window). A `README.md` says how
    they were captured and when. One hand-written ENTSO-E day without NOK, marked as
    hand-written.
  - Client tests (the host guard, no redirect, gzip, the cap, 401, 429 with
    `Retry-After`, 5xx, and the key absent from errors and logs via a captured `slog`
    handler).
  - Sync tests against an `httptest.Server` playing the service (reading 9's statuses,
    reading 10's walk across cycles and its resumption, reading 11's budget and cadence,
    the lease).
  - `module.go`'s `workers(d)` builds the worker when either source is configured.
    `NewSpotPriceWorker` chooses the source, and `TestModule_ContributesItsWorkers` is
    extended.
  - **commit** `feat(energy): the spot-price service as a source — the live window and the history walk (Energy 4A2, task 5)`.

- [ ] **6. The coverage on the Prices page** (reading 13): the source badge in the
  Coverage card's title, the *Resolution* and *History* columns, "Not followed by the
  spot-price service". i18n en and nb; `bun run translations:check`; page tests with
  `stubFetch` for each source.
  - **commit** `feat(energy-ui): the Prices page shows the source, the resolution and the history copied (Energy 4A2, task 6)`
    with `Docs-Impact: none — the user guide lands in delivery 2 with the page's other changes; the reference in task 7`.

- [ ] **7. Documentation** (S12, delivery 1). `R/energy.md` "Spot prices": the three
  sources, the new columns, "The service sync" (live window, history, judging a day,
  replacing a fetched day), the settings, the client and its guard, the coverage's new
  fields, and `sources`. `A/authentication.md` en then nb: the three settings and the
  one-source rule. `A/installation.md` en then nb: the worker with either source, the
  one-minute cadence while copying, and how to verify (the coverage's `source`, the
  History column). `A/transport-security.md` en then nb: the exception. `MB`: the
  netguard paragraph names the energy service client as the one caller of the
  exception. `ROADMAP.md`: Energy phase 4 "Delivery A2 (done)". BD: a one-line
  "Amended by" pointer under D2, D5 and D12 to this design's S10.
  - `mise run docs:check`.
  - **commit** `docs(energy): spot prices from the shared service — the reference, the operator pages and the transport-security exception in both languages (Energy 4A2, task 7)`.

- [ ] **8. Review, gates and the pull request.** An opus code review (read-only, probe
  tests through `go test -overlay`) focused on the guard, the key, the replace-day and
  the lease, and a docs pass against the code, both in parallel. The findings go in
  `fix(energy): … (Energy 4A2)`. Then the four gates and
  `bun run tools/docs/check-coverage.ts --base origin/main`. Push, then
  `gh pr create --base main`; do not merge.

## Tasks: delivery 2, `feat/energy-price-history`

- [ ] **8b. The summary read** (reading 14): query, OpenAPI
  `getEnergySpotPricesSummary` (`energy:prices-view`), the handler, tests (the design's
  summary tests, including `TZ=America/New_York`).
  - **commit** `feat(energy): the spot-price summary — hourly, daily, weekly and monthly time-weighted averages with their range (Energy 4A3, task 1)`.

- [ ] **9. The Prices page over time** (reading 15).
  - `api/spot-prices.ts`: `spotPriceSummaryQueryOptions`.
  - `pages/prices.tsx`: the period control (Day · Week · Month · Year · Range), the
    areas `MultiSelect` (≤ 5), the figures strip, the preliminary badge.
  - `-price-chart.tsx`: the interval chart for several areas; new
    `-price-summary-chart.tsx` with the band.
  - Host route search parameters.
  - Tests: each period's request and chart, comparison, the band for one area only,
    preliminary, and the search-parameter round trip.
  - i18n en and nb.
  - **commit** `feat(energy-ui): prices over time — week, month, year and range, comparing areas, the period's figures and the view in the URL (Energy 4A3, task 2)`
    with `Docs-Impact: none — the user guide lands in task 10`.

- [ ] **10. Documentation** (S12, delivery 2). `U/energy.md` en then nb, "Prices": the
  periods, comparing areas, the figures, preliminary prices, *Month* now showing daily
  averages, and the History column. `R/energy.md`: the summary endpoint. `ROADMAP.md`.
  - **commit** `docs(energy): prices over time — the user guide in both languages and the summary in the reference (Energy 4A3, task 3)`.

- [ ] **11. Review, gates and the pull request**, as task 8.

## Tasks: `vantigo-infra`, `feat/spot-price-settings`

- [ ] **12.** S9: `bin/vantigo` writes and rotates `ENERGY_SPOT_PRICE_SERVICE_URL` and
  `ENERGY_SPOT_PRICE_SERVICE_KEY`, and `spotprices init` migrates `SPOTPRICES_API_KEY`.
  Update `show`, the template's comment, `test/smoke.sh`'s assertion, and the README and
  CHANGELOG. Opened when delivery 1's PR is open, so the smoke test can run against its
  image (`SMOKE_IMAGE=vantigo:dev SMOKE_SPOTPRICES_IMAGE=vantigo-spotprices:local test/smoke.sh`).
  Merged after delivery 1. `vantigo-spotprices`' README paragraph on the consumer is
  updated in the same breath (S9).

## Self-review

- S2 → task 2; S3 → task 2; S4 → tasks 5, 6; S5 → task 5; S6 → task 4; S7 → tasks 3, 5;
  S8 → tasks 8b, 9; S9 → task 12; S10 → task 7; S12 → tasks 7, 10.
- Names used the same everywhere: `ENERGY_SPOT_PRICE_SERVICE_URL|KEY`,
  `ENERGY_SPOT_PRICE_HISTORY_FROM`, `energy.spot_price_history`, `Service`,
  `getEnergySpotPricesSummary`, `PrivateNetwork`, `DialContextAllowingPrivate`,
  `energy-spot-prices`.
- Open questions Q1–Q3 do not block delivery 1's code; Q1 blocks its **release to
  tenants**, not its merge.
