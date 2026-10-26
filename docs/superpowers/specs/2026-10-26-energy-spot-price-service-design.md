# Energy spot prices from the shared spot-price service, and price history — design

Energy phase 4 delivery A (`feat/energy-spot-prices`, merged) gave Vantigo spot prices:
`energy.spot_prices` per area, the `energy-spot-prices` worker reading the public feed
hvakosterstrommen.no a day at a time from seven days back to tomorrow, the file import,
and a **Prices** page charting one area for a day or a month. Since then two private
repositories have shipped the other half:

- **[vantigo-spotprices](https://github.com/vantigo-io/vantigo-spotprices)** (read at
  `9242382`): a Go service that polls Nord Pool's data portal every ten minutes, backfills
  history on request from ENTSO-E (EUR, from 2015) and the community feed (NOK, hourly,
  from 2021-12-01), keeps every interval as `numeric` with its `state` (`preliminary` |
  `final`), `source`, `version`, `rateSource`, the replaced figures and the raw payloads,
  and serves them behind a bearer key (`openapi/spotprices.yaml`).
- **[vantigo-infra](https://github.com/vantigo-io/vantigo-infra)** (read at `801b54a`):
  one `vantigo-spotprices` container per host on an internal Docker network `spotprices`
  every tenant joins; no tunnel rule, no host port. `vantigo add` writes
  `SPOTPRICES_URL` (`http://vantigo-spotprices:8080`) and a per-tenant
  `SPOTPRICES_API_KEY` into the tenant's `.env`.

Nothing in Vantigo reads the service yet. This design makes it Vantigo's spot-price
source in our hosted environments, and uses the history it holds to let a person look at
prices over time, not only a day or a month.

The split in one sentence: **the service collects, Vantigo keeps what it bills by.**
Every tenant still stores the prices it uses in `energy.spot_prices` — the book a
settlement (billing design D5) is priced from, in the tenant's own database and its own
backups — but it copies them from the service instead of from the public feed, with their
provenance, and copies the history the service holds rather than seven days.

`srv/` is `apps/server/internal/`, `mig/` is `srv/db/migrations/`, `R/` is
`docs/src/content/docs/en/reference/`, `U/` is `docs/src/content/docs/{en,nb}/user/`,
`A/` is `docs/src/content/docs/{en,nb}/admin/`, `BD` is
`docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md`. Line numbers
are against `f37fdf87`.

## Decisions

### S1 — Copy, never read through

The tenant keeps `energy.spot_prices` as the store every read and every later
settlement uses; the service is a **source**, like the feed and the file. Reading
through to the service for the chart and copying only for billing was weighed and
refused:

- a settlement must be reproducible from the tenant's own database and backups (BD D5:
  a settlement freezes the figures it priced with); a price that lives only on a shared
  container on one host is not that;
- the service is ours, private, and runs only where we host. Vantigo is also installed by
  others, for whom the public feed and the file import stay the sources (S3);
- one place to read keeps the Prices page, the coverage and the future settlement on the
  same figures. Two would let the chart show a price the invoice did not use.

Size is not an argument against copying: five areas from 2015 at an hour, and at a
quarter-hour since 2025-10-01, are well under a million rows, and an installation
follows the areas it needs (S4).

### S2 — What a row records: state and provenance

Migration `mig/00075_energy_spot_price_service.sql` (the next free number on `main`
today; the plan fixes it when the branch is cut):

- `energy.spot_prices.source` gains `Service` (`ck_spot_prices_source`: `Feed`, `Import`,
  `Service`);
- `state varchar(12) NOT NULL DEFAULT 'final'`, CHECK `final` | `preliminary`. Existing
  rows are `final`: the public feed publishes each day once, and a file is the operator's
  say-so;
- `upstream varchar(20) NULL`: the service's own `source` (`nordpool_portal`, `entsoe`,
  `hks`) for a `Service` row, null otherwise. It is not CHECKed: the service may add
  sources, and the contract's enum is read as text. The client only accepts
  `^[a-z][a-z0-9_]{0,19}$`;
- `rate_source varchar(20) NULL`: whose EUR/NOK rate gave the NOK figure (`nordpool`,
  `norges_bank`), as the service says;
- `upstream_version integer NULL`: the service's `version`.

A new table records history progress per area (S5):

```sql
CREATE TABLE energy.spot_price_history (
    price_area      varchar(4)  PRIMARY KEY,
    cursor_day      date,          -- the oldest civil day read so far; null before the first window
    floor_day       date,          -- the oldest day the service holds for the area, as last read
    days_skipped    integer NOT NULL DEFAULT 0,  -- days the service held incompletely or without NOK
    attempts        integer NOT NULL DEFAULT 0,
    last_error      varchar(500),
    next_attempt_at timestamptz,
    updated_at      timestamptz NOT NULL
);
```

`spot_price_fetches` (delivery A) keeps recording the live window per area and civil
day, whichever source fills it.

**Why state matters:** the service writes tomorrow as soon as the auction publishes it,
and Nord Pool marks the NOK figure `Preliminary` until the exchange rate is final. BD D5
gains a rule (S10): a settlement prices only from `final` intervals, and a period with a
preliminary one is blocked with that reason.

### S3 — One source per installation, chosen by settings

| Setting | Default | Meaning |
| --- | --- | --- |
| `ENERGY_SPOT_PRICE_SERVICE_URL` | unset | The service's base URL, `http://vantigo-spotprices:8080` in our hosted environments. Absolute `http` or `https`, no user info, no query, path empty or `/`. Set, it turns the worker on with the service as its source. |
| `ENERGY_SPOT_PRICE_SERVICE_KEY` | unset | The tenant's bearer key. Required when the URL is set; never logged, never in an error sentence, never in any answer. |
| `ENERGY_SPOT_PRICE_HISTORY_FROM` | unset | Optional oldest civil day to copy (`YYYY-MM-DD`). Unset copies everything the service holds for the area. |
| `ENERGY_SPOT_PRICE_FEED_ENABLED`, `…_FEED_URL`, `…_FEED_POLL` | as today | The public feed (delivery A), unchanged. |

Setting both `ENERGY_SPOT_PRICE_SERVICE_URL` and `ENERGY_SPOT_PRICE_FEED_ENABLED=1` is
a start-up problem ("set one spot-price source"). Two writers to one area would make
`source` meaningless and the exclusion constraint a race. `ENERGY_SPOT_PRICE_FEED_POLL`
is the poll for either source; it is renamed in neither code nor documentation, but the
admin page says it applies to both.

**The names.** Vantigo's settings are module-prefixed and documented in one table
(`A/authentication.md`). infra's `SPOTPRICES_URL` and `SPOTPRICES_API_KEY` are not, and
its `.env.example` already calls them inert until the application's source ships. So
infra adopts Vantigo's two names (S9) rather than Vantigo reading infra's.

### S4 — Which areas are followed

With the service as the source, an area is followed when it is **active in the
service** (`GET /api/v1/areas`, `active: true`; NO1–NO5 on our hosts), whether or not it
is in use. The Prices page is useful on a fresh tenant with no metering points yet,
which is the point of the page for someone looking at prices over time. An area in use
that the service does not follow is shown in the coverage table as "Not followed by the
spot-price service" and gets no request and no fetch rows. Only when `/api/v1/areas`
cannot be asked does the live window run for the areas in use (delivery A's rule: a
metering point with a non-cancelled supply period, or prices already stored).
*(Amended while building delivery 1: the first text said "in use or active", which
contradicted its own last sentence.)*

With the feed, delivery A's rule is unchanged: in use only, because the feed is a third
party's courtesy.

### S5 — The sync: a live window and a history walk

One worker, `energy-spot-prices`, as today. Its cycle runs under delivery A's lease
`ENRGSPT1` and picks the source from the settings. With the service, a cycle does the
following:

1. **Areas.** `GET /api/v1/areas` once, which gives the followed set (S4) and each area's
   `coverage.firstStart`, the floor for history. A failure here records `failed` on
   every followed area's history row with backoff and ends the cycle; the live window
   needs no area list it does not already have, so it still runs for the areas in use.
2. **The live window**, per followed area: the civil days in Europe/Oslo from
   `today − 14` to `today + 1`. Fourteen days matches the service's own re-check of
   Nord Pool's corrections. A window is **due** when any of its days has no `ok` fetch
   row whose `next_attempt_at` has passed, or when its last full read is older than
   24 hours (to pick up a correction's version bump). Then **one request**:
   `GET /api/v1/prices?area=&from=<today−14 00:00 Oslo>&to=<today+2 00:00 Oslo>&currency=EUR`.
   `currency=EUR` is deliberate: with `NOK` a single day without a rate fails the whole
   window with a 400, and NOK is read whenever present anyway. The answer is split into
   civil days and each day is judged on its own (S6). A day's fetch row becomes:
   - `ok`: complete and every interval `final`;
   - `pending`: complete but preliminary, or absent for today or tomorrow. Retried hourly,
     as delivery A's `pending`;
   - `missing`: a past day absent or incomplete, or lacking NOK. Retried daily, given up
     after seven, as today;
   - `failed`: every day of the window when the request failed. Backoff
     `min(6h, 15min × 2^(attempts−1))`, as today.
3. **History**, per followed area, with what the cycle's request budget leaves: the walk
   goes backwards from the day before the live window to
   `max(floor from step 1, ENERGY_SPOT_PRICE_HISTORY_FROM)` in **35-day windows**, the
   service's limit and so one request per window, each written in its own transaction
   with the cursor. A window's days are judged as the live window's are, but they record
   no fetch rows: a day that is incomplete or has no NOK adds to `days_skipped` and is
   left for the file import. When the floor moves earlier (the operator ran another
   backfill on the service), the walk resumes from its cursor to the new floor. A
   failed request backs the area off as the live window does, without losing the
   cursor.

**Budget.** At most twenty requests a cycle, as today; live windows first, history
after. While any area's history is incomplete, the next cycle comes after a minute,
not after the poll. Five areas from 2015 are about 600 requests, roughly half an hour.
The service allows 600 requests a minute per key, and the worker makes at most twenty.

**Conditional requests.** The worker sends `Accept-Encoding: gzip` (the service
compresses). It does not keep ETags: a live window changes daily, and a history window
is read once.

### S6 — Judging a day and writing it

A day of an answer is **complete** when its intervals tile the civil day from local
midnight to the next local midnight with no gap, which delivery A's
`parseSpotFeedDay` already checks for the feed. Its checks move into one function
used by both sources. It is **written** only when complete and every interval carries
`priceNokPerKwh`. Figures: `nok_per_kwh = priceNokPerKwh`,
`eur_per_kwh = priceEurPerMwh / 1000`, `exchange_rate = exchangeRate`. They are decoded
as `json.Number` and scaled by moving the decimal point, never through a float division,
then rounded to `numeric(12,6)` as `roundPrice` does. The bound is delivery A's
`maxSpotFeedPrice`.

The write extends delivery A's plan/apply helper (`srv/energy/spot_prices_write.go`):

- the same interval with different figures, `state`, `upstream`, `rate_source` or
  `upstream_version` is **replaced** (a correction, or preliminary becoming final);
- the same interval with equal figures and state is **unchanged**;
- **another resolution over a `Feed` or `Service` day**, for example the service's
  quarter-hours over the feed's hours for the same day: the stored rows of that area and
  day are **deleted and the day inserted**, in one transaction under the area lock. A
  fetched source is replaced by a better fetched source;
- **another resolution over an `Import` day**: refused, as today. The day records
  `failed` with delivery A's sentence "the day's intervals overlap stored prices of
  another length", or adds to `days_skipped` in history. An operator's file is not
  overwritten silently; deleting the file's rows is the operator's call (open question
  Q2);
- **the same interval over an `Import` row**: left as it is and counted unchanged, so a
  file wins over the service at its own resolution too. *(Amended while building
  delivery 1: the first text guarded only another resolution, and the general rule
  above would have replaced an imported figure.)*

Nothing is ever written that the exclusion constraint would refuse, so a day is all or
nothing.

### S7 — The client and the outbound guard

The service is reached at a private address, and `netguard` refuses every private
range. The exception is as narrow as it can be:

- **`netguard.PrivateNetwork(addr) bool`** (new, in the one table): RFC 1918 IPv4 and
  RFC 4193 unique-local IPv6, nothing else. Loopback, link-local (the
  `169.254.169.254` metadata address), carrier-grade NAT, unspecified, multicast and
  reserved ranges stay refused for every caller.
- **`netguard.DialContextAllowingPrivate(resolver, dial, requirePrivate bool)`**: the
  existing dialler's resolve, check every address, dial the checked address, except that
  an address passing `PrivateNetwork` is allowed. With `requirePrivate` every address
  must be private.
- The energy module builds **one client for the service**, separate from the feed's:
  - its transport uses the private-allowing dialler, with `requirePrivate` set **when the
    URL is `http`**, so the bearer key is never sent in the clear to a public address;
    over `https` a public or private address is allowed;
  - a round-tripper refuses any request whose host is not the configured host, so the
    exception cannot be reached through a redirect, a template or a future caller;
  - no proxy, no redirect followed, 1 MiB body cap after gzip, 20 s budget, keep-alives
    **on**: unlike the feed, the service is asked several times a cycle while history
    runs;
  - `Authorization: Bearer <key>` on every request; `User-Agent: vantigo/<version>`.
- Failure sentences in `last_error` never quote the body or the key: "the spot-price
  service refused the key" (401), "the spot-price service is rate limiting this
  installation" (429, honouring `Retry-After` as the next attempt), "the spot-price
  service's address is public; use https" (`requirePrivate` refused it), and delivery A's
  sentences for timeouts, unreachable hosts, oversized bodies and malformed answers.
  The service's problem `detail` on a 400 is read for the log at debug level only.

`A/transport-security.md` gains the exception: which setting opens it, which ranges, and
why `http` is accepted only to a private address.

### S8 — Prices over time: the reads and the page

**A summary read.** Price history means months and years at quarter-hour resolution, too
many points to chart and too many to send. A new operation:

`GET /api/v1/energy/spot-prices/summary?area=NO1&from&to&bucket=hour|day|week|month`
(`getEnergySpotPricesSummary`, `energy:prices-view`) answers
`{area, bucket, timeZone: "Europe/Oslo", buckets: [{start, end, avgNokPerKwh,
minNokPerKwh, maxNokPerKwh, coveredMinutes, preliminary}]}`:

- buckets are civil hours, days, ISO weeks or months in Europe/Oslo, so a DST day is 23
  or 25 hours and a month starts at local midnight;
- the average is **time-weighted**, `Σ(price × length) / Σ(length)`, because history
  before 2025-10-01 is hourly and after it quarter-hourly; a plain mean would weigh the
  newer quarter-hours four times;
- `coveredMinutes` lets the page mark an incomplete bucket; a bucket with no interval is
  absent, not zero; `preliminary` is true when any interval in it is preliminary;
- the window per bucket is bounded: `hour` ≤ 35 days, `day` ≤ 400 days, `week` ≤ 5
  years, `month` ≤ 30 years. `from`/`to` must fall on the bucket's boundaries in
  Europe/Oslo (400 otherwise, naming the field);
- one SQL statement over `energy.spot_prices` with
  `date_trunc(<bucket>, "start" AT TIME ZONE 'Europe/Oslo')`. The unique index on
  `(price_area, "start")` serves the range.

**The intervals read** (`getEnergySpotPrices`) gains `state`, `upstream`, `rateSource`,
and `source` gains `Service`. **The coverage read** gains
`source: "service" | "feed" | "none"` at the top, and per area
`followedByService?: boolean`, `resolution?: "PT15M" | "PT60M"` (the latest day's) and
`history?: {floorDay, cursorDay, complete, daysSkipped, error?}`.

**The page** (`/energy/prices`, Energy's frontend):

- **Coverage** card as today, plus a source badge in the title ("Spot-price service",
  "Public feed", "Files only"), a *Resolution* column and a *History* column: "Complete
  from 1 Jan 2015", or "Copying… back to 14 Mar 2019" with the skipped days, or the
  error. An area the service does not follow says so (S4).
- **Spot prices** card, the chart, with the period control **Day · Week · Month · Year ·
  Range**:

  | Period | Read | Chart |
  | --- | --- | --- |
  | Day | intervals | step line, every interval (today's behaviour) |
  | Week | intervals | step line, no dots |
  | Month | summary `day` | daily average line on a min–max band |
  | Year | summary `day` | daily average line on a min–max band |
  | Range (two month pickers) | summary `week` up to 2 years, `month` beyond | average line on a min–max band |

  The *Month* period today charts every interval; it becomes daily averages with the
  band, and a person who wants every interval of a month uses *Week*. This is a change a
  user will see, and the user guide says so.
- **Areas to compare**: the area `Select` becomes a `MultiSelect` of up to five areas,
  one line each in the chart's categorical colours. The min–max band is drawn only for a
  single area.
- **Figures above the chart** for the first area and the period: the average, the
  lowest and the highest, the last two with their interval or day. A *Preliminary*
  badge appears when any figure in view is preliminary, and preliminary points are drawn
  dashed.
- **The state in the URL**: `area`, `period`, `date` and `from`/`to` are the route's
  search parameters (`apps/host/frontend/src/routes/energy/prices.tsx`), so a view can
  be linked and survives a reload.
- The unit stays NOK/kWh. EUR is out of scope (BD D12).

### S9 — The companion change in vantigo-infra

One pull request in `vantigo-infra`, landing before or with S1's release:

- `vantigo add` and `vantigo spotprices key` write `ENERGY_SPOT_PRICE_SERVICE_URL`
  (`$SPOTPRICES_URL`) and `ENERGY_SPOT_PRICE_SERVICE_KEY` into the tenant's `.env`,
  instead of `SPOTPRICES_URL` and `SPOTPRICES_API_KEY`. `vantigo spotprices init`, rerun on a host, moves an
  existing `SPOTPRICES_API_KEY` to the new name. The rotation stays gap-free: write,
  restart, revoke;
- `vantigo show` and the template's comment name the new settings;
- `test/smoke.sh` with `SMOKE_SPOTPRICES_IMAGE` asserts that the tenant's
  `GET /api/v1/energy/spot-prices/coverage` answers `source: "service"`, then that an
  area has prices after a cycle;
- README and CHANGELOG ("what existing hosts must do": rerun `vantigo spotprices init`,
  restart tenants).

`vantigo-spotprices` needs no change. Its README's last paragraph ("The consumer in
Vantigo … will add a `nordpool` source kind reading `/api/v1/prices/day`") is updated
to say that Vantigo reads `/api/v1/prices` and `/api/v1/areas`.
`/prices/day` stays for Nord Pool-shaped consumers.

### S10 — Amendments to the billing design

- **BD D2** gains the third source, the row's `state` and provenance, and the
  replacement of a fetched day at another resolution (S2, S6).
- **BD D5**: a settlement prices only from `final` intervals. A period touching a
  `preliminary` interval is blocked with "prices for <day> are preliminary". Delivery C's
  plan carries this.
- **BD D12**'s "the 15-minute feed (stored when it comes)" is done: the service serves
  quarter-hours since 2025-10-01.

### S11 — Out of scope, named

Reading through to the service (S1) · EUR on the page · intraday and regulating prices ·
a Nord Pool `DayAheadPrices` parser (`/prices/day`) · exporting from Vantigo (the
service's `export.csv` exists) · alerts on price thresholds · consumption-weighted prices
per customer, which belong with settlements (delivery C) · whether serving Nord Pool's
figures to hosted tenants is redistribution under Nord Pool's terms, which is the
operator's decision and is recorded in Q1, not decided in code.

### S12 — Documentation, as its own deliverable in each delivery

- **Delivery 1:** `R/energy.md`, "Spot prices": the sources, the model's new columns, the
  service sync with its live window, history and day judgement, the settings, the client
  and its guard, `sources` gaining the migration and the new files.
  `A/authentication.md` (en, nb) gets the three settings and the one-source rule.
  `A/installation.md` (en, nb) gets the worker paragraph with either source, the
  history walk and the one-minute cadence while copying. `A/transport-security.md`
  (en, nb) gets the private-address exception. `en/contributing/module-boundaries.md`
  only if its netguard paragraph names the callers. `ROADMAP.md` gets Energy phase 4's
  new delivery.
- **Delivery 2:** `U/energy.md` (en, nb), "Prices": the periods, comparing areas, the
  figures, preliminary prices, the changed *Month*, the History column. `R/energy.md`
  gets the summary endpoint and the coverage's new fields. `ROADMAP.md`.
- `Docs-Impact: none` only on plan and test-only commits.

## Deliveries

| | Branch | Migration | Holds |
| --- | --- | --- | --- |
| **1** Energy 4A2: the spot-price service | `feat/energy-spot-price-service` | `00075_energy_spot_price_service.sql` | S2–S7, S10's BD note, the coverage's new fields, the intervals read's new fields, the docs of S12 (1) |
| **2** Energy 4A3: price history | `feat/energy-price-history` | — | S8's summary read and the page, S12 (2) |
| **infra** | `vantigo-infra`: `feat/spot-price-settings` | — | S9 |

Each is a pull request against `main` after the previous one lands. Delivery 1 is
useful without delivery 2: the existing page fills from the service. Both come before
BD's delivery B (tariffs), which does not depend on them, so that C's settlements are
built on `state` from the start.

## Testing

- **Config:** both sources refused together; the service URL's shape (http/https, no user
  info, no query, path); the key required with the URL; `HISTORY_FROM` as a date.
- **netguard:** `PrivateNetwork` against the ranges and their edges (`172.15.255.255`
  no, `172.16.0.0` yes, `fc00::/7` yes, `169.254.169.254` no, `100.64.0.1` no,
  `::ffff:10.0.0.1` yes, `64:ff9b::a00:1` no, because an embedded private address through
  NAT64 is not a private network); `DialContextAllowingPrivate` with and without
  `requirePrivate`, through a canned resolver.
- **The client:** a host other than the configured host refused before dialling; no
  redirect followed; the key never appears in a recorded error or a log line (asserted
  with a captured `slog` handler); 401, 429 with `Retry-After`, 5xx, gzip, an oversized
  body.
- **Judging days:** fixtures captured from the service at quarter-hour (96, 92 and 100
  entries on a plain day, the spring change and the autumn change), hourly `hks` history
  (24/23/25), an ENTSO-E day without NOK, a preliminary tomorrow, and a day with a gap.
- **The write:** preliminary to final replaced; a version bump replaced; quarter-hours
  over a `Feed` hourly day replace the day; over an `Import` day refused with the
  sentence; nothing partial written when one day of a window conflicts.
- **The cycle** against an `httptest.Server` playing the service: areas, the live
  window's single request, the statuses per day, the history walk's cursor across
  cycles and its resumption when the floor moves, the twenty-request budget shared live
  first, the one-minute cadence while copying, the lease held elsewhere.
- **The summary:** time-weighted averages across the 2025-10-01 resolution change; a DST
  day's 23 and 25 hours; a month's boundaries in Oslo; missing buckets absent; the
  window limits per bucket; under `TZ=America/New_York`, as CI runs it.
- **The page:** each period's query and chart; compare areas; the band only for one
  area; the preliminary badge; the search parameters round-trip; the coverage's source
  badge and History column.
- **infra smoke:** S9.

## Readings on the record

- The service's `/api/v1/prices` answers at most 35 days, ascending, with
  `priceEurPerMwh` always and `priceNokPerKwh`/`exchangeRate`/`rateSource` when known;
  `currency=NOK` fails the window on the first day without a rate
  (`openapi/spotprices.yaml:40-122`).
- `/api/v1/areas` answers all twelve zones with `active` and `coverage` (null until
  housekeeping ran) and `lastLiveFetch` (`openapi/spotprices.yaml:255-301`, `551-617`).
- Keys are rate-limited to 600 requests a minute and are for revocation, not data
  separation: every tenant sees the same rows (spotprices README, "The API").
- infra creates the `spotprices` network `--internal`, joins every tenant to it
  (`template/compose.yaml:88-94`), and writes `SPOTPRICES_URL` and `SPOTPRICES_API_KEY`
  into `.env` (`bin/vantigo:636-637`, the URL defaulting to `bin/vantigo:64`).
- Delivery A's feed transport refuses every private address through
  `netguard.DialContext` (`srv/energy/spot_prices_feed.go:112-140`,
  `srv/netguard/netguard.go:198-241`); `netguard.Disallowed` has no allowlist by design
  (`netguard.go:61-65`), so the exception is a new function beside it, not a flag on it.
- `energy.spot_prices` refuses overlapping intervals per area with a GiST exclusion
  (`mig/00065_energy_spot_prices.sql:28-31`), so a resolution change must delete before
  it inserts.

## Open questions

- **Q1** Nord Pool's terms: the service's README says serving the portal's figures to
  third parties may be a redistribution that Nord Pool charges for. Our hosted tenants
  are customers, so whether this applies is the operator's decision before delivery 1 is
  released to tenants. If the answer is no, the service can serve ENTSO-E and `hks`
  figures only, and nothing in this design changes; `upstream` on each row lets Vantigo
  tell them apart later.
- **Q2** An `Import` day under a service day at another resolution: refused (S6). The
  alternative, letting the service replace imported days because the service carries
  provenance, is one line in the write if the operator prefers it.
- **Q3** `ENERGY_SPOT_PRICE_HISTORY_FROM` unset copies everything the service holds,
  from 2015 if ENTSO-E was backfilled. A default of 2021-12-01 would keep the copy to NOK
  history that was never EUR-only. Unset is proposed, since days without NOK are skipped
  anyway.
