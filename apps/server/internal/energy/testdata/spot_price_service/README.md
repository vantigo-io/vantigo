# Spot-price service fixtures

Answers of the shared spot-price service (`vantigo-spotprices`) for the energy
module's client and sync tests (`spot_price_service_client_test.go`,
`spot_price_service_sync_test.go`).

**They are hand-written, not captured.** No running service was available when
they were made (2026-10-26), so they are written to the service's contract,
`openapi/spotprices.yaml` at vantigo-spotprices `9242382`: the `PriceInterval`
and `Area` schemas, figures as JSON numbers with their own digits and never
padded, times in UTC. The prices are made up. They are to be replaced by
captures from a local service (`docker compose up`, a key, a `hks` backfill of
a few days including 2025-10-26 and 2026-03-29, and the live window) when one
is at hand; the tests read only their shape.

| File | What it is |
| --- | --- |
| `areas.json` | `GET /api/v1/areas`: the twelve zones, NO1–NO5 active; NO3's coverage holds nothing yet, NO4's is null, SE1 is inactive with coverage. |
| `prices_live_2026-09-10_to_2026-09-13.json` | `GET /api/v1/prices` for NO1 over four Oslo days at a quarter-hour: 2026-09-10 final (96), 2026-09-11 absent, 2026-09-12 final (96), 2026-09-13 preliminary (96). |
| `prices_quarter_hours_spring_2026-03-29.json` | The spring change at a quarter-hour: 92 entries. |
| `prices_quarter_hours_autumn_2025-10-26.json` | The autumn change at a quarter-hour: 100 entries. |
| `prices_hks_hourly_2025-03-29_to_2025-03-31.json` | Hourly `hks` history across the spring change: 24, 23, 24 entries, NOK by `norges_bank`. |
| `prices_hks_hourly_2024-10-26_to_2024-10-28.json` | Hourly `hks` history across the autumn change: 24, 25, 24 entries. |
| `prices_entsoe_without_nok_2024-06-15.json` | An ENTSO-E day in EUR only: no `priceNokPerKwh`, `exchangeRate` or `rateSource`. |
| `prices_gap_2026-09-08.json` | A quarter-hour day with one interval (10:15) missing: 95 entries. |

The generator was a short script writing each day's entries from local
midnight to the next in Europe/Oslo; the sync tests build their own answers
from the same rule (`serviceIntervals` in `spot_price_service_sync_test.go`).
