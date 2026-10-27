-- Settlements (energy consumption billing design D5, energy settlements plan
-- readings 1–13): the subjects a settlement is computed for, the intervals and
-- prices it is computed from, the rows it is written to, and the invoiced-work
-- holder's locks and stamps. A tariff's versions and components are read with
-- ListTariffVersions and ListTariffComponents (tariffs.sql).

-- name: ListSettlementSubjectsByIDs :many
-- ListSettlementSubjectsByIDs is what a settlement of each supply period is
-- computed from, beside the intervals, the prices and the tariffs: the supply
-- period, its metering point's billing facts and the power tariff's name.
-- Cancelled periods are answered too; the caller decides what they settle.
SELECT sp.id AS supply_period_id, sp.metering_point_id, sp.customer_id, sp.start AS supply_start,
       sp."end" AS supply_end, sp.status, sp.tariff_id, sp.cadence,
       mp.gsrn, mp.street_address, mp.postal_code, mp.city, mp.country_code, mp.price_area,
       mp.household, mp.electricity_tax_class, mp.grid_tariff_id,
       COALESCE(t.name, '')::text AS contract_name
FROM energy.supply_periods sp
JOIN energy.metering_points mp ON mp.id = sp.metering_point_id
LEFT JOIN energy.tariffs t ON t.id = sp.tariff_id
WHERE sp.id = ANY(@ids::int[])
ORDER BY sp.id;

-- name: ListSettlementSupplyPeriodIDsByCustomers :many
-- ListSettlementSupplyPeriodIDsByCustomers is Settle's and DueSupplies' "every
-- supply period of these customers": the non-cancelled ones that started
-- before @through, in id order.
SELECT id
FROM energy.supply_periods
WHERE customer_id = ANY(@customer_ids::int[])
  AND status <> 'Cancelled'
  AND start < @through::timestamptz
ORDER BY id;

-- name: ListSettlementSupplyPeriodIDsByMeteringPoint :many
-- ListSettlementSupplyPeriodIDsByMeteringPoint is the endpoint's supply
-- periods: every non-cancelled one of the point, in id order.
SELECT id
FROM energy.supply_periods
WHERE metering_point_id = @metering_point_id
  AND status <> 'Cancelled'
ORDER BY id;

-- name: ListSettlementIntervals :many
-- ListSettlementIntervals is every current interval of the point that
-- overlaps [@from, @to): those inside the period and those crossing one of
-- its bounds, which make it incomplete (plan reading 3). The lower bound on
-- start reaches a day back so a long interval crossing @from is seen and the
-- partitions can still be pruned.
SELECT "start", "end", quantity_kwh, quality, received_at
FROM energy.consumption_intervals
WHERE metering_point_id = @metering_point_id
  AND is_current
  AND "start" < @to_ts::timestamptz
  AND "start" >= @from_ts::timestamptz - interval '1 day'
  AND "end" > @from_ts::timestamptz
ORDER BY "start", "end";

-- name: ListSettlementSpotPrices :many
-- ListSettlementSpotPrices is every spot price of the area overlapping
-- [@from, @to), final or preliminary: the computation tells them apart
-- (spot-price service design S10).
SELECT "start", "end", nok_per_kwh, state, received_at
FROM energy.spot_prices
WHERE price_area = @price_area
  AND "start" < @to_ts::timestamptz
  AND "end" > @from_ts::timestamptz
ORDER BY "start";

-- name: SettlementInputsChangedSince :one
-- SettlementInputsChangedSince is a correction's trigger (plan reading 8):
-- whether an interval of the point in [@from, @to), a price of the area in it,
-- or one of the two tariffs was written after @since.
SELECT (
    EXISTS (
        SELECT 1 FROM energy.consumption_intervals c
        WHERE c.metering_point_id = @metering_point_id
          AND c."start" < @to_ts::timestamptz
          AND c."start" >= @from_ts::timestamptz - interval '1 day'
          AND c."end" > @from_ts::timestamptz
          AND c.received_at > @since::timestamptz
    )
    OR EXISTS (
        SELECT 1 FROM energy.spot_prices p
        WHERE p.price_area = @price_area
          AND p."start" < @to_ts::timestamptz
          AND p."end" > @from_ts::timestamptz
          AND p.received_at > @since::timestamptz
    )
    OR EXISTS (
        SELECT 1 FROM energy.tariffs t
        WHERE t.id = ANY(@tariff_ids::int[])
          AND t.updated_at > @since::timestamptz
    )
)::bool AS changed;

-- name: ListSettlementsBySupplyPeriods :many
-- ListSettlementsBySupplyPeriods is every settlement of the supply periods,
-- of every state, oldest period first.
SELECT *
FROM energy.settlements
WHERE supply_period_id = ANY(@supply_period_ids::int[])
ORDER BY supply_period_id, period_start, kind, id;

-- name: ListSettlementsByIDs :many
SELECT *
FROM energy.settlements
WHERE id = ANY(@ids::bigint[])
ORDER BY id;

-- name: ListSettlementsByMeteringPoint :many
-- ListSettlementsByMeteringPoint is the endpoint's stored settlements: every
-- one of the point whose period ends after @since.
SELECT *
FROM energy.settlements
WHERE metering_point_id = @metering_point_id
  AND period_end > @since::timestamptz
ORDER BY period_start DESC, kind DESC, id;

-- name: ListSettlementLines :many
-- ListSettlementLines is the lines of the settlements, each settlement's in
-- position order.
SELECT *
FROM energy.settlement_lines
WHERE settlement_id = ANY(@settlement_ids::bigint[])
ORDER BY settlement_id, position;

-- name: LockLiveSettlement :one
-- LockLiveSettlement is a draft write's lock (plan reading 7): the live
-- settlement of (supply period, period, kind), FOR NO KEY UPDATE, so a
-- racing Settle or the holder's stamp waits.
SELECT *
FROM energy.settlements
WHERE supply_period_id = @supply_period_id
  AND period_start = @period_start::timestamptz
  AND kind = @kind
  AND state IN ('draft', 'invoiced')
FOR NO KEY UPDATE;

-- name: LockSettlementsByIDs :many
-- LockSettlementsByIDs is the invoiced-work holder's lock (design D6): the
-- settlements by id ascending, FOR NO KEY UPDATE, the order every batch of
-- this module keeps.
SELECT *
FROM energy.settlements
WHERE id = ANY(@ids::bigint[])
ORDER BY id
FOR NO KEY UPDATE;

-- name: InsertSettlement :one
-- InsertSettlement writes a new draft at revision 1. A racing insert of the
-- same live key fails on ux_settlements_live (23505); the caller retries by
-- reading the row the other wrote.
INSERT INTO energy.settlements (
    supply_period_id, metering_point_id, customer_id, kind, corrects_id, period_start, period_end,
    state, revision, kwh, estimated, interval_count, expected_interval_count, blocked, blocked_detail,
    amount, currency, contract_name, computed_at, touched_at, created_at, updated_at
) VALUES (
    @supply_period_id, @metering_point_id, @customer_id, @kind, sqlc.narg(corrects_id), @period_start::timestamptz,
    @period_end::timestamptz, 'draft', 1, @kwh, @estimated, @interval_count, @expected_interval_count,
    sqlc.narg(blocked), sqlc.narg(blocked_detail), @amount, @currency, @contract_name,
    @now::timestamptz, @now::timestamptz, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateSettlementDraft :one
-- UpdateSettlementDraft rewrites a draft's figures and bumps its revision;
-- the caller has locked it and found a figure changed. A row no longer draft
-- is not touched (no row answered).
UPDATE energy.settlements
SET period_end = @period_end::timestamptz,
    customer_id = @customer_id,
    corrects_id = sqlc.narg(corrects_id),
    kwh = @kwh,
    estimated = @estimated,
    interval_count = @interval_count,
    expected_interval_count = @expected_interval_count,
    blocked = sqlc.narg(blocked),
    blocked_detail = sqlc.narg(blocked_detail),
    amount = @amount,
    currency = @currency,
    contract_name = @contract_name,
    revision = revision + 1,
    computed_at = @now::timestamptz,
    touched_at = @now::timestamptz,
    updated_at = @now::timestamptz
WHERE id = @id AND state = 'draft'
RETURNING *;

-- name: TouchSettlements :exec
-- TouchSettlements records that a read answered the drafts: housekeeping
-- deletes only drafts nobody has touched for thirty days (plan reading 7).
UPDATE energy.settlements
SET touched_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND state = 'draft' AND touched_at < @now::timestamptz;

-- name: DeleteSettlementLines :exec
DELETE FROM energy.settlement_lines WHERE settlement_id = @settlement_id;

-- name: InsertSettlementLine :exec
INSERT INTO energy.settlement_lines (
    settlement_id, position, kind, text, band, month, quantity, unit, unit_price, amount, vat_hint
) VALUES (
    @settlement_id, @position, @kind, @text, sqlc.narg(band), sqlc.narg(month)::date, @quantity, @unit,
    @unit_price, @amount, @vat_hint
);

-- name: DeleteSettlementDraft :execrows
-- DeleteSettlementDraft removes one draft and its lines; a row no longer
-- draft is kept.
DELETE FROM energy.settlements WHERE id = @id AND state = 'draft';

-- name: DeleteUntouchedSettlementDrafts :execrows
-- DeleteUntouchedSettlementDrafts is the housekeeping (plan reading 7): drafts
-- nobody has touched since @before, with their lines. A draft a correction
-- follows is never one (only an invoiced settlement is corrected).
DELETE FROM energy.settlements
WHERE state = 'draft' AND touched_at < @before::timestamptz;

-- name: StampSettlementsInvoiced :many
-- StampSettlementsInvoiced is MarkInvoiced's write (design D6), on rows the
-- holder has locked and judged: invoiced, the stamp, the revision bumped.
UPDATE energy.settlements
SET state = 'invoiced',
    invoiced_invoice_id = @invoice_id,
    invoiced_number = @invoice_number,
    invoiced_at = @issued_at::timestamptz,
    invoiced_by_user_id = sqlc.narg(invoiced_by_user_id),
    invoice_date = @invoice_date::date,
    revision = revision + 1,
    updated_at = @issued_at::timestamptz
WHERE id = ANY(@ids::bigint[]) AND state = 'draft'
RETURNING id;

-- name: ReleaseSettlementsInvoiced :many
-- ReleaseSettlementsInvoiced is ReleaseInvoiced's write: the rows carrying the
-- invoice's stamp move to released, the stamp kept as history.
UPDATE energy.settlements
SET state = 'released',
    released_at = @released_at::timestamptz,
    updated_at = @released_at::timestamptz
WHERE id = ANY(@ids::bigint[]) AND state = 'invoiced' AND invoiced_invoice_id = @invoice_id
RETURNING id;

-- name: RepointSettlementsCustomer :execrows
-- RepointSettlementsCustomer is the settlements' half of energy's part of a
-- customer merge (plan reading 13): the copied customer id follows the
-- supply periods it was copied from.
UPDATE energy.settlements
SET customer_id = @into_customer_id::integer
WHERE customer_id = @from_customer_id::integer;
