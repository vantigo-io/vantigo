-- name: InsertSupplyPeriod :one
-- InsertSupplyPeriod creates a new, always open-ended (end NULL) supply
-- period (CreateSupplyPeriodEndpoint.cs:25-26, SwitchSupplyPeriodEndpoint.cs:59-65).
INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, status)
VALUES (@metering_point_id, @customer_id, @start::timestamptz, @status)
RETURNING id, metering_point_id, customer_id, start, "end", status;

-- name: ListSupplyPeriodsByMeteringPoint :many
-- ListSupplyPeriodsByMeteringPoint is GetSupplyPeriodsEndpoint's history,
-- ordered by Start (:15).
SELECT id, metering_point_id, customer_id, start, "end", status
FROM energy.supply_periods
WHERE metering_point_id = @metering_point_id
ORDER BY start;

-- name: GetSupplyPeriod :one
-- GetSupplyPeriod is the (periodId, meteringPointId) lookup both
-- EndSupplyPeriodEndpoint and CancelSupplyPeriodEndpoint run (:15).
SELECT id, metering_point_id, customer_id, start, "end", status
FROM energy.supply_periods
WHERE id = @id AND metering_point_id = @metering_point_id;

-- name: GetActiveOpenSupplyPeriod :one
-- GetActiveOpenSupplyPeriod finds the one open (end IS NULL), Active period
-- for a metering point, if any (SwitchSupplyPeriodEndpoint.cs:31-32; energy
-- inventory §2.3 — at most one can ever exist per metering point, the
-- exclusion constraint's own invariant, so LIMIT 1 never discards a second
-- candidate).
SELECT id, metering_point_id, customer_id, start, "end", status
FROM energy.supply_periods
WHERE metering_point_id = @metering_point_id AND status = 'Active' AND "end" IS NULL
LIMIT 1;

-- name: SupplyPeriodOverlapExists :one
-- SupplyPeriodOverlapExists is the "friendly" advisory overlap pre-check
-- Create and Switch's move-in branch both run
-- (CreateSupplyPeriodEndpoint.cs:22-23, SwitchSupplyPeriodEndpoint.cs:50-51):
-- true when a non-Cancelled period on this metering point overlaps
-- [start, +infinity) — the new period is always open-ended at creation
-- time, so this is SupplyPeriod.Overlaps(existing, new) simplified because
-- new.End is always null (energy inventory §2.3). This includes a
-- historical (Ended) period whose own end is still in the future relative
-- to start: COALESCE("end", 'infinity') treats a null end as +infinity but
-- does not otherwise care about status beyond "not Cancelled". The GiST
-- exclusion constraint (§3.2) is what actually decides a race (§5); this
-- query reduces to the exact predicate it encodes and is only ever the
-- advisory layer in front of it.
SELECT EXISTS (
    SELECT 1 FROM energy.supply_periods
    WHERE metering_point_id = @metering_point_id
      AND status <> 'Cancelled'
      AND @start::timestamptz < COALESCE("end", 'infinity'::timestamptz)
);

-- name: EndSupplyPeriod :one
-- EndSupplyPeriod sets end and status together: EndSupplyPeriodEndpoint's
-- own transition (status always 'Ended') and SwitchSupplyPeriodEndpoint's
-- close-the-active-period step (also always 'Ended') both use it.
UPDATE energy.supply_periods SET "end" = @end_at::timestamptz, status = @status WHERE id = @id
RETURNING id, metering_point_id, customer_id, start, "end", status;

-- name: SetSupplyPeriodStatus :one
-- SetSupplyPeriodStatus is CancelSupplyPeriodEndpoint's unconditional
-- transition (:15) — no status guard, an already-Ended or already-Cancelled
-- period can be cancelled again (energy inventory §1.1/§8 oddity 5).
UPDATE energy.supply_periods SET status = @status WHERE id = @id
RETURNING id, metering_point_id, customer_id, start, "end", status;
