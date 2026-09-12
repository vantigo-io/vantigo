-- name: InsertMeter :one
-- InsertMeter is CreateMeteringPointEndpoint's/ReplaceMeterEndpoint's meter
-- insert. installed_at is caller-supplied (request time on create, the
-- validated request body on replace), never a database default.
INSERT INTO energy.meters (metering_point_id, meter_number, installed_at)
VALUES (@metering_point_id, @meter_number, @installed_at::timestamptz)
RETURNING id, metering_point_id, meter_number, installed_at, removed_at;

-- name: GetActiveMeter :one
-- GetActiveMeter is the "current" meter for a metering point: the one row
-- with removed_at IS NULL (energy inventory §2.2), enforced by a partial
-- unique index, not an in-app invariant. pgx.ErrNoRows means no active
-- meter exists (only reachable off the public endpoints, which always
-- leave exactly one after create/replace).
SELECT id, metering_point_id, meter_number, installed_at, removed_at
FROM energy.meters
WHERE metering_point_id = @metering_point_id AND removed_at IS NULL;

-- name: SetMeterRemovedAt :exec
-- SetMeterRemovedAt is ReplaceMeterEndpoint's close-the-old-meter step
-- (:31), always the new meter's installed_at — adjacent, no gap, no
-- overlap by construction.
UPDATE energy.meters SET removed_at = @removed_at::timestamptz WHERE id = @id;

-- name: ListMetersByMeteringPoint :many
-- ListMetersByMeteringPoint is GetMetersEndpoint's history, ordered by
-- InstalledAt ascending (:16).
SELECT id, metering_point_id, meter_number, installed_at, removed_at
FROM energy.meters
WHERE metering_point_id = @metering_point_id
ORDER BY installed_at;
