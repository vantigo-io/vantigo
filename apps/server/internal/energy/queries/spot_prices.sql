-- Spot prices (energy consumption billing design D2): the intervals, the
-- feed's record and the areas the worker fetches for. The writes are plain
-- insert and update by id: the handler and the worker read the stored rows
-- of the starts they carry first and decide, so the counts they answer are
-- exact; the exclusion constraint spot_prices_no_overlap is the backstop
-- behind both.

-- name: ListSpotPricesByStarts :many
-- ListSpotPricesByStarts is the rows an area already holds at the given
-- starts, the write's first read.
SELECT id, price_area, "start", "end", nok_per_kwh, eur_per_kwh, exchange_rate, source, received_at,
       state, upstream, rate_source, upstream_version
FROM energy.spot_prices
WHERE price_area = @price_area AND "start" = ANY(@starts::timestamptz[])
ORDER BY "start";

-- name: ListSpotPricesOverlapping :many
-- ListSpotPricesOverlapping is every stored interval of an area touching
-- [from, to): what a day at another resolution would collide with. The range
-- operator lets the exclusion constraint's GiST index answer it, where
-- "start" < to alone would walk the area's whole history.
SELECT id, price_area, "start", "end", nok_per_kwh, eur_per_kwh, exchange_rate, source, received_at,
       state, upstream, rate_source, upstream_version
FROM energy.spot_prices
WHERE price_area = @price_area AND tstzrange("start", "end", '[)') && tstzrange(@from_ts::timestamptz, @to_ts::timestamptz, '[)')
ORDER BY "start";

-- name: InsertSpotPrice :exec
INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, eur_per_kwh, exchange_rate, source, received_at,
                                 state, upstream, rate_source, upstream_version)
VALUES (@price_area, @start::timestamptz, @end_at::timestamptz, @nok_per_kwh, @eur_per_kwh, @exchange_rate, @source, @received_at::timestamptz,
        @state, @upstream, @rate_source, @upstream_version);

-- name: UpdateSpotPrice :exec
-- UpdateSpotPrice replaces the figures of an interval already stored with
-- the same start and end, its state and its provenance with them.
UPDATE energy.spot_prices
SET nok_per_kwh = @nok_per_kwh, eur_per_kwh = @eur_per_kwh, exchange_rate = @exchange_rate,
    source = @source, received_at = @received_at::timestamptz,
    state = @state, upstream = @upstream, rate_source = @rate_source, upstream_version = @upstream_version
WHERE id = @id;

-- name: DeleteSpotPricesByIDs :exec
-- DeleteSpotPricesByIDs removes a fetched day's rows that a fetched day at
-- another resolution replaces (spot-price service design S6), inside the
-- write's transaction and under the area's lock.
DELETE FROM energy.spot_prices WHERE id = ANY(@ids::bigint[]);

-- name: ListSpotPrices :many
-- ListSpotPrices is the read behind GET /energy/spot-prices: the intervals
-- of an area starting inside [from, to), ascending.
SELECT id, price_area, "start", "end", nok_per_kwh, eur_per_kwh, exchange_rate, source, received_at,
       state, upstream, rate_source, upstream_version
FROM energy.spot_prices
WHERE price_area = @price_area AND "start" >= @from_ts::timestamptz AND "start" < @to_ts::timestamptz
ORDER BY "start";

-- name: ListSpotPriceAreaBounds :many
-- ListSpotPriceAreaBounds is the coverage's first and last instant per area.
SELECT price_area, min("start")::timestamptz AS first_start, max("end")::timestamptz AS last_end, count(*)::bigint AS interval_count
FROM energy.spot_prices
GROUP BY price_area
ORDER BY price_area;

-- name: ListSpotPriceDays :many
-- ListSpotPriceDays is the civil days (in the given zone) an area has at
-- least one interval starting in, from since on — the coverage's gap count
-- and the planner's "is this day stored" are both computed from it in Go.
SELECT DISTINCT (("start" AT TIME ZONE @time_zone::text)::date)::date AS day
FROM energy.spot_prices
WHERE price_area = @price_area AND "start" >= @since::timestamptz
ORDER BY day;

-- name: ListSpotPriceAreasInUse :many
-- ListSpotPriceAreasInUse is the areas the worker fetches for: every area of
-- a metering point with a supply period that is not cancelled, and every
-- area already priced, so a point supplied once keeps its prices current.
SELECT DISTINCT area::varchar(4) AS price_area FROM (
    SELECT mp.price_area AS area
    FROM energy.metering_points mp
    WHERE EXISTS (SELECT 1 FROM energy.supply_periods p WHERE p.metering_point_id = mp.id AND p.status <> 'Cancelled')
    UNION
    SELECT sp.price_area AS area FROM energy.spot_prices sp
) areas
ORDER BY price_area;

-- name: ListSpotPriceFetches :many
-- ListSpotPriceFetches is the feed's record of an area over a window of
-- days, what the planner decides from.
SELECT price_area, day, status, attempts, last_error, attempted_at, fetched_at, next_attempt_at
FROM energy.spot_price_fetches
WHERE price_area = @price_area AND day >= @from_day::date AND day <= @to_day::date
ORDER BY day;

-- name: ListLatestSpotPriceFetches :many
-- ListLatestSpotPriceFetches is the most recent attempt per area, for the
-- coverage read.
SELECT DISTINCT ON (price_area) price_area, day, status, attempts, last_error, attempted_at, fetched_at, next_attempt_at
FROM energy.spot_price_fetches
WHERE attempted_at IS NOT NULL
ORDER BY price_area, attempted_at DESC, day DESC;

-- name: RecordSpotPriceFetch :exec
-- RecordSpotPriceFetch writes one attempt's outcome: the row created on the
-- first attempt and updated after, attempts counted up while the status
-- stays and starting again from one when it changes — a day pending for
-- hours before it is published does not spend a later status's budget.
INSERT INTO energy.spot_price_fetches (price_area, day, status, attempts, last_error, attempted_at, fetched_at, next_attempt_at)
VALUES (@price_area, @day::date, @status, 1, @last_error, @attempted_at::timestamptz, @fetched_at, @next_attempt_at)
ON CONFLICT (price_area, day) DO UPDATE SET
    status = EXCLUDED.status,
    attempts = CASE WHEN energy.spot_price_fetches.status = EXCLUDED.status THEN energy.spot_price_fetches.attempts + 1 ELSE 1 END,
    last_error = EXCLUDED.last_error,
    attempted_at = EXCLUDED.attempted_at,
    fetched_at = COALESCE(EXCLUDED.fetched_at, energy.spot_price_fetches.fetched_at),
    next_attempt_at = EXCLUDED.next_attempt_at;

-- name: GetSpotPriceHistory :one
-- GetSpotPriceHistory is one area's walk back through the service's
-- history (spot-price service design S5).
SELECT price_area, cursor_day, floor_day, days_skipped, attempts, last_error, next_attempt_at, updated_at
FROM energy.spot_price_history
WHERE price_area = @price_area;

-- name: ListSpotPriceHistory :many
-- ListSpotPriceHistory is every area's walk, for the coverage read.
SELECT price_area, cursor_day, floor_day, days_skipped, attempts, last_error, next_attempt_at, updated_at
FROM energy.spot_price_history
ORDER BY price_area;

-- name: UpsertSpotPriceHistory :exec
-- UpsertSpotPriceHistory writes an area's walk as the sync decided it:
-- the cursor, the floor, the skipped days and the backoff, whole.
INSERT INTO energy.spot_price_history (price_area, cursor_day, floor_day, days_skipped, attempts, last_error, next_attempt_at, updated_at)
VALUES (@price_area, @cursor_day, @floor_day, @days_skipped, @attempts, @last_error, @next_attempt_at, @updated_at::timestamptz)
ON CONFLICT (price_area) DO UPDATE SET
    cursor_day = EXCLUDED.cursor_day,
    floor_day = EXCLUDED.floor_day,
    days_skipped = EXCLUDED.days_skipped,
    attempts = EXCLUDED.attempts,
    last_error = EXCLUDED.last_error,
    next_attempt_at = EXCLUDED.next_attempt_at,
    updated_at = EXCLUDED.updated_at;

-- name: ListLatestSpotPriceResolutions :many
-- ListLatestSpotPriceResolutions is, per area, the length of the latest
-- stored interval in seconds: the coverage's resolution.
SELECT DISTINCT ON (price_area) price_area, extract(epoch FROM "end" - "start")::bigint AS seconds
FROM energy.spot_prices
ORDER BY price_area, "start" DESC;

-- name: LatestSpotPriceWindowRead :many
-- LatestSpotPriceWindowRead is when an area's live window was last read:
-- the newest fetched_at of its days from from_day on, none when it never
-- was. Every read of the window stamps every day it judged.
SELECT fetched_at::timestamptz AS fetched_at
FROM energy.spot_price_fetches
WHERE price_area = @price_area AND day >= @from_day::date AND fetched_at IS NOT NULL
ORDER BY fetched_at DESC
LIMIT 1;
