-- name: EnsureConsumptionPartition :exec
-- EnsureConsumptionPartition guarantees the target month's partition exists
-- before a manual-consumption insert lands (energy inventory §3.3,
-- AddManualConsumptionEndpoint.cs:35-36). partition_month is cast through
-- timestamptz first so sqlc maps the Go parameter to time.Time, this
-- module's usual timestamp type, rather than pgtype.Date, then narrowed to
-- date for the function's own signature.
SELECT energy.ensure_consumption_partition((@partition_month::timestamptz)::date);

-- name: FindCurrentConsumptionInterval :one
-- FindCurrentConsumptionInterval is AddManualConsumptionEndpoint's exact-
-- tuple lookup (:18-19) — the only overlap-adjacent check this table gets is
-- an exact (metering_point_id, start, end) match among current rows, not a
-- range overlap (energy inventory §2.4). Deliberately no overlap check is
-- added here (this task's dispatch correction 2, energy inventory §8 oddity
-- 6): two different, non-matching intervals may freely cover overlapping
-- time.
SELECT id, metering_point_id, start, "end", quantity_kwh, quality, source, received_at
FROM energy.consumption_intervals
WHERE metering_point_id = @metering_point_id AND start = @start::timestamptz AND "end" = @end_at::timestamptz AND is_current
LIMIT 1;

-- name: SetConsumptionIntervalNotCurrent :exec
-- SetConsumptionIntervalNotCurrent flips the superseded row's is_current
-- (AddManualConsumptionEndpoint.cs:20): the supersede chain's other half.
UPDATE energy.consumption_intervals SET is_current = false WHERE id = @id AND start = @start::timestamptz;

-- name: InsertConsumptionInterval :one
-- InsertConsumptionInterval is AddManualConsumptionEndpoint's insert
-- (:21-34): quality and source are always the Manual enum members on this
-- write path — the module's only Elhub/Estimated rows come from a bypass
-- test fixture, not any production endpoint (energy inventory §8 oddity 6).
INSERT INTO energy.consumption_intervals (
    metering_point_id, start, "end", quantity_kwh, quality, source, received_at, is_current, supersedes_id, supersedes_start
) VALUES (
    @metering_point_id, @start::timestamptz, @end_at::timestamptz, @quantity_kwh, 'Manual', 'Manual',
    @received_at::timestamptz, true, @supersedes_id, @supersedes_start
)
RETURNING id, metering_point_id, start, "end", quantity_kwh, quality, source, received_at;

-- name: ListConsumptionByMeteringPoint :many
-- ListConsumptionByMeteringPoint is GetConsumptionEndpoint's success path
-- (:17-22): current rows only, ordered by Start, with the deliberately
-- asymmetric range filter (energy inventory §4 line 341: Start >= from but
-- End <= to) applied only when the caller supplied that bound.
SELECT id, metering_point_id, start, "end", quantity_kwh, quality, source, received_at
FROM energy.consumption_intervals
WHERE metering_point_id = @metering_point_id
  AND is_current
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR start >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR "end" <= sqlc.narg(to_ts)::timestamptz)
ORDER BY start;

-- name: ListConsumptionByCustomer :many
-- ListConsumptionByCustomer is CustomerConsumptionFilter.CurrentIntervals
-- plus GetCustomerConsumptionEndpoint's own from/to filter
-- (CustomerConsumptionFilter.cs:17-24, GetCustomerConsumptionEndpoint.cs:18-20):
-- a current interval counts for the customer when some non-cancelled supply
-- period of theirs on the same metering point covers it
-- (interval.Start >= period.Start && (period.End IS NULL || interval.End <= period.End)),
-- optionally narrowed to one metering point.
SELECT c.id, c.metering_point_id, c.start, c."end", c.quantity_kwh, c.quality, c.source, c.received_at
FROM energy.consumption_intervals c
WHERE c.is_current
  AND (sqlc.narg(metering_point_id)::int IS NULL OR c.metering_point_id = sqlc.narg(metering_point_id)::int)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR c.start >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR c."end" <= sqlc.narg(to_ts)::timestamptz)
  AND EXISTS (
      SELECT 1 FROM energy.supply_periods p
      WHERE p.customer_id = @customer_id
        AND p.metering_point_id = c.metering_point_id
        AND p.status <> 'Cancelled'
        AND c.start >= p.start
        AND (p."end" IS NULL OR c."end" <= p."end")
  )
ORDER BY c.start;

-- name: AggregateConsumptionByMeteringPoint :many
-- AggregateConsumptionByMeteringPoint is ConsumptionAggregateQuery.ForMeteringPointAsync
-- (ConsumptionAggregateQuery.cs:28-52), quoted verbatim from energy inventory
-- §4 lines 288-303 (tenant_id dropped, per Global Constraints): the double
-- AT TIME ZONE idiom reinterprets each row's start as naive local time in
-- the metering point's own market zone, date_trunc truncates in that local
-- wall-clock calendar (so a "day" bucket is a local day, not a UTC day, and
-- can be 23 or 25 hours of UTC across a DST transition), then the result
-- converts back to a timestamptz. has_estimated is true when any bucketed
-- row is Estimated or Corrected — not Manual, not Measured. A bucket with no
-- qualifying row simply does not appear: no zero-fill.
WITH bucketed AS (
    SELECT date_trunc(@resolution::text, c."start" AT TIME ZONE @time_zone::text) AS bucket_local,
           c.quantity_kwh,
           c.quality
    FROM energy.consumption_intervals AS c
    WHERE c.metering_point_id = @metering_point_id
      AND c.is_current
      AND c."start" >= @from_ts::timestamptz
      AND c."end" <= @to_ts::timestamptz
)
SELECT (bucket_local AT TIME ZONE @time_zone::text)::timestamptz AS bucket_start,
       ((bucket_local + CASE @resolution::text
           WHEN 'hour' THEN interval '1 hour'
           WHEN 'day' THEN interval '1 day'
           WHEN 'month' THEN interval '1 month'
       END) AT TIME ZONE @time_zone::text)::timestamptz AS bucket_end,
       SUM(quantity_kwh)::numeric AS quantity_kwh,
       COUNT(*) AS interval_count,
       BOOL_OR(quality IN ('Estimated', 'Corrected')) AS has_estimated
FROM bucketed
GROUP BY bucket_local
ORDER BY bucket_local;

-- name: AggregateConsumptionByCustomerMeteringPoint :many
-- AggregateConsumptionByCustomerMeteringPoint is ConsumptionAggregateQuery.ForCustomerMeteringPointAsync
-- (:66-113), quoted verbatim from energy inventory §4 (tenant_id dropped):
-- the same bucketing shape as AggregateConsumptionByMeteringPoint, gated by
-- an EXISTS against the customer's non-cancelled supply periods covering
-- the interval. GetCustomerConsumptionAggregateEndpoint calls this once per
-- matching metering point (N+1 by design, energy inventory §8 oddity 10),
-- so metering_point_id is a single value here, not a set.
WITH bucketed AS (
    SELECT date_trunc(@resolution::text, c."start" AT TIME ZONE @time_zone::text) AS bucket_local,
           c.metering_point_id,
           c.quantity_kwh,
           c.quality
    FROM energy.consumption_intervals AS c
    WHERE c.metering_point_id = @metering_point_id
      AND c.is_current
      AND c."start" >= @from_ts::timestamptz
      AND c."end" <= @to_ts::timestamptz
      AND EXISTS (
          SELECT 1
          FROM energy.supply_periods AS p
          WHERE p.customer_id = @customer_id
            AND p.metering_point_id = c.metering_point_id
            AND p.status <> 'Cancelled'
            AND c."start" >= p."start"
            AND (p."end" IS NULL OR c."end" <= p."end")
      )
)
SELECT metering_point_id,
       (bucket_local AT TIME ZONE @time_zone::text)::timestamptz AS bucket_start,
       ((bucket_local + CASE @resolution::text
           WHEN 'hour' THEN interval '1 hour'
           WHEN 'day' THEN interval '1 day'
           WHEN 'month' THEN interval '1 month'
       END) AT TIME ZONE @time_zone::text)::timestamptz AS bucket_end,
       SUM(quantity_kwh)::numeric AS quantity_kwh,
       COUNT(*) AS interval_count,
       BOOL_OR(quality IN ('Estimated', 'Corrected')) AS has_estimated
FROM bucketed
GROUP BY metering_point_id, bucket_local
ORDER BY bucket_local;

-- name: ListCustomerMeteringPointPriceAreas :many
-- ListCustomerMeteringPointPriceAreas is GetCustomerConsumptionAggregateEndpoint's
-- own point/price-area lookup (:25-29): every metering point with at least
-- one of the customer's non-cancelled supply periods, optionally narrowed to
-- one metering point — id and price_area are all the caller needs to run
-- AggregateConsumptionByCustomerMeteringPoint once per point.
SELECT DISTINCT mp.id, mp.price_area
FROM energy.metering_points mp
WHERE EXISTS (
    SELECT 1 FROM energy.supply_periods p
    WHERE p.metering_point_id = mp.id
      AND p.customer_id = @customer_id
      AND p.status <> 'Cancelled'
      AND (sqlc.narg(metering_point_id)::int IS NULL OR p.metering_point_id = sqlc.narg(metering_point_id)::int)
)
ORDER BY mp.id;

-- name: ListCustomerMeteringPoints :many
-- ListCustomerMeteringPoints is GetCustomerMeteringPointsEndpoint's point
-- half (:17-19): every metering point with at least one of the customer's
-- non-cancelled supply periods, ordered by id, each carrying its current
-- meter's number the same way ListMeteringPoints does.
SELECT DISTINCT mp.id, mp.gsrn, mp.street_address, mp.postal_code, mp.city, mp.country_code, mp.price_area, mp.grid_area,
       mp.expected_annual_consumption_kwh, mp.latitude, mp.longitude, mp.connection_status, mp.created_at, mp.updated_at,
       active.meter_number AS active_meter_number
FROM energy.metering_points mp
LEFT JOIN energy.meters active ON active.metering_point_id = mp.id AND active.removed_at IS NULL
WHERE EXISTS (
    SELECT 1 FROM energy.supply_periods p
    WHERE p.metering_point_id = mp.id AND p.customer_id = @customer_id AND p.status <> 'Cancelled'
)
ORDER BY mp.id;

-- name: ListCustomerSupplyPeriods :many
-- ListCustomerSupplyPeriods is GetCustomerMeteringPointsEndpoint's period
-- half (:25-29): every non-cancelled period of the customer's, across every
-- metering point, ordered so each point's own periods group together and
-- sort by Start within the group — the handler slices this one query's rows
-- per point rather than issuing one query per point.
SELECT id, metering_point_id, customer_id, start, "end", status
FROM energy.supply_periods
WHERE customer_id = @customer_id AND status <> 'Cancelled'
ORDER BY metering_point_id, start;

-- name: EnergyStatsSummaryCounts :one
-- EnergyStatsSummaryCounts is EnergyStatsEndpoints.Summary's six counts/sums
-- (EnergyStatsEndpoints.cs:39-64) in one round trip: current vs.
-- as-of-period-start counts for metering points and active supply periods,
-- plus the consumption sum for the period and for the immediately preceding
-- period of equal length (period_from - (period_to - period_from), i.e.
-- [previous_from, period_from) — Period.Previous, :153). The consumption
-- sums share the aggregate queries' half-open-ish Start>=from/End<=to bounds
-- (energy inventory §1.1 line 63).
SELECT
    (SELECT count(*) FROM energy.metering_points) AS metering_point_count,
    (SELECT count(*) FROM energy.metering_points WHERE created_at < @period_from::timestamptz) AS metering_point_count_at_period_start,
    (SELECT count(*) FROM energy.supply_periods WHERE status = 'Active') AS active_supply_periods,
    (SELECT count(*) FROM energy.supply_periods
        WHERE status = 'Active' AND start < @period_from::timestamptz AND ("end" IS NULL OR "end" >= @period_from::timestamptz)
    ) AS active_supply_periods_at_period_start,
    (SELECT COALESCE(SUM(quantity_kwh), 0)::numeric FROM energy.consumption_intervals
        WHERE is_current AND start >= @period_from::timestamptz AND "end" <= @period_to::timestamptz
    ) AS consumption_kwh,
    (SELECT COALESCE(SUM(quantity_kwh), 0)::numeric FROM energy.consumption_intervals
        WHERE is_current AND start >= @previous_from::timestamptz AND "end" <= @period_from::timestamptz
    ) AS previous_consumption_kwh;

-- name: EnergyConsumptionDailyBuckets :many
-- EnergyConsumptionDailyBuckets is EnergyStatsEndpoints.Timeseries's grouping
-- (:87-100): item.Start.Date buckets by the *UTC* calendar date of Start —
-- unlike the two aggregate endpoints, this is not timezone-aware per
-- metering point (energy inventory §1.1 line 64). AT TIME ZONE 'UTC' makes
-- that explicit rather than relying on the session's own timezone setting
-- for date_trunc/::date to happen to agree.
SELECT (c."start" AT TIME ZONE 'UTC')::date AS bucket_date, COALESCE(SUM(c.quantity_kwh), 0)::numeric AS quantity_kwh
FROM energy.consumption_intervals c
WHERE c.is_current AND c."start" >= @period_from::timestamptz AND c."end" <= @period_to::timestamptz
GROUP BY bucket_date
ORDER BY bucket_date;

-- name: ListExpiringSupplyPeriods :many
-- ListExpiringSupplyPeriods is EnergyStatsEndpoints.Attention's hard-coded
-- rule (:110-114): active periods whose End falls within [now, now+30d],
-- ordered by End.
SELECT id, metering_point_id, "end"
FROM energy.supply_periods
WHERE status = 'Active' AND "end" IS NOT NULL AND "end" >= @now::timestamptz AND "end" <= @expires_by::timestamptz
ORDER BY "end";
