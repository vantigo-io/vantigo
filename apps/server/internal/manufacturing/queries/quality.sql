-- Quality checks and deviations (manufacturing phase 2 M95–M99).

-- name: ChecksOfOperations :many
-- ChecksOfOperations is the check templates of the operations, by operation
-- and sequence.
SELECT * FROM manufacturing.bom_operation_checks
WHERE operation_id = ANY(@operation_ids::bigint[])
ORDER BY operation_id, sequence, id;

-- name: DeleteChecksOfBom :exec
-- DeleteChecksOfBom removes the check templates of every operation of the
-- BoM: an edit writes them again from the request (M95).
DELETE FROM manufacturing.bom_operation_checks
WHERE operation_id IN (SELECT id FROM manufacturing.bom_operations WHERE bom_id = @bom_id);

-- name: InsertBomOperationCheck :exec
INSERT INTO manufacturing.bom_operation_checks (operation_id, sequence, kind, title, instructions, nominal, min_value, max_value, unit,
                                                frequency, quarantine_on_fail)
VALUES (@operation_id, @sequence, @kind, @title, @instructions, sqlc.narg(nominal), sqlc.narg(min_value), sqlc.narg(max_value),
        sqlc.narg(unit), @frequency, @quarantine_on_fail);

-- name: SnapshotWorkOrderChecks :exec
-- SnapshotWorkOrderChecks copies the operation's check templates onto its
-- work order at confirm (M96).
INSERT INTO manufacturing.work_order_checks (work_order_id, template_id, sequence, kind, title, instructions, nominal, min_value, max_value,
                                             unit, frequency, quarantine_on_fail)
SELECT @work_order_id, c.id, c.sequence, c.kind, c.title, c.instructions, c.nominal, c.min_value, c.max_value, c.unit, c.frequency,
       c.quarantine_on_fail
FROM manufacturing.bom_operation_checks c
WHERE c.operation_id = @operation_id
ORDER BY c.sequence, c.id;

-- name: ChecksOfWorkOrders :many
-- ChecksOfWorkOrders is the work orders' checks, each with its result in
-- force when it has one, by work order and sequence.
SELECT sqlc.embed(c), r.id AS result_id, r.result, r.value, r.note, r.photo_id, r.user_id, r.user_display, r.recorded_at,
       d.id AS deviation_id, d.number AS deviation_number
FROM manufacturing.work_order_checks c
LEFT JOIN manufacturing.check_results r ON r.work_order_check_id = c.id AND r.superseded_by IS NULL
LEFT JOIN manufacturing.deviations d ON d.check_result_id = r.id
WHERE c.work_order_id = ANY(@work_order_ids::bigint[])
ORDER BY c.work_order_id, c.sequence, c.id;

-- name: LockWorkOrderCheck :one
-- LockWorkOrderCheck takes one check FOR UPDATE, after its work order (M97):
-- two results recorded at once for one check serialise here, and the second
-- supersedes the first.
SELECT * FROM manufacturing.work_order_checks WHERE id = @id AND work_order_id = @work_order_id FOR UPDATE;

-- name: CurrentCheckResult :one
SELECT * FROM manufacturing.check_results WHERE work_order_check_id = @work_order_check_id AND superseded_by IS NULL;

-- name: InsertCheckResult :one
INSERT INTO manufacturing.check_results (work_order_check_id, result, value, note, photo_id, user_id, user_display, recorded_at, station_id)
VALUES (@work_order_check_id, @result, sqlc.narg(value), sqlc.narg(note), sqlc.narg(photo_id), sqlc.narg(user_id), @user_display, @now, sqlc.narg(station_id))
RETURNING *;

-- name: SupersedeCheckResult :exec
UPDATE manufacturing.check_results SET superseded_by = @superseded_by WHERE id = @id;

-- name: MissingChecksOfWorkOrders :many
-- MissingChecksOfWorkOrders is the checks of the work orders with no result
-- in force — of the frequencies named — by work order sequence (M98).
SELECT c.id, c.work_order_id, c.title, c.frequency, w.sequence AS work_order_sequence, w.name AS work_order_name
FROM manufacturing.work_order_checks c
JOIN manufacturing.work_orders w ON w.id = c.work_order_id
WHERE c.work_order_id = ANY(@work_order_ids::bigint[])
  AND c.frequency = ANY(@frequencies::text[])
  AND NOT EXISTS (SELECT 1 FROM manufacturing.check_results r WHERE r.work_order_check_id = c.id AND r.superseded_by IS NULL)
ORDER BY w.sequence, w.id, c.sequence, c.id;

-- name: InsertCheckPhoto :one
INSERT INTO manufacturing.check_photos (work_order_id, object_key, content_type, size_bytes, uploaded_by_user_id, uploaded_by_display, created_at)
VALUES (@work_order_id, @object_key, @content_type, @size_bytes, sqlc.narg(uploaded_by_user_id), sqlc.narg(uploaded_by_display), @now)
RETURNING *;

-- name: GetCheckPhoto :one
SELECT * FROM manufacturing.check_photos WHERE id = @id AND work_order_id = @work_order_id;

-- name: CheckPhotoForShare :one
-- CheckPhotoForShare takes the photo a result names FOR SHARE, so the sweep
-- of unattached photos, which deletes FOR UPDATE, cannot take it from under
-- the result.
SELECT * FROM manufacturing.check_photos WHERE id = @id FOR SHARE;

-- name: StaleCheckPhotos :many
-- StaleCheckPhotos is the photos no result names, uploaded before the cut
-- (M97: 24 hours), skipping one another transaction holds — a result being
-- recorded with it.
SELECT p.id, p.object_key FROM manufacturing.check_photos p
WHERE p.created_at < @cut
  AND NOT EXISTS (SELECT 1 FROM manufacturing.check_results r WHERE r.photo_id = p.id)
ORDER BY p.id
LIMIT @row_limit
FOR UPDATE SKIP LOCKED;

-- name: DeleteUnattachedCheckPhoto :execrows
-- DeleteUnattachedCheckPhoto deletes the photo while no result names it,
-- judged again by the delete's own snapshot: a result recorded with it after
-- the sweep read it keeps it, and 0 rows says so (M97).
DELETE FROM manufacturing.check_photos p
WHERE p.id = @id
  AND NOT EXISTS (SELECT 1 FROM manufacturing.check_results r WHERE r.photo_id = p.id);

-- name: InsertObjectDeletion :exec
-- InsertObjectDeletion queues an object whose row is gone, in the
-- transaction that removed the row.
INSERT INTO manufacturing.object_deletions (object_key, next_attempt_at, created_at)
VALUES (@object_key, @now, @now)
ON CONFLICT (object_key) DO NOTHING;

-- name: DueObjectDeletions :many
SELECT object_key FROM manufacturing.object_deletions
WHERE next_attempt_at <= @now AND (claimed_until IS NULL OR claimed_until <= @now)
ORDER BY next_attempt_at, id
LIMIT @row_limit;

-- name: ClaimObjectDeletion :one
-- ClaimObjectDeletion claims one due row for a pass, answering the attempts
-- that failed before; 0 rows is a row another pass claimed.
UPDATE manufacturing.object_deletions SET claimed_until = @claimed_until
WHERE object_key = @object_key AND next_attempt_at <= @now AND (claimed_until IS NULL OR claimed_until <= @now)
RETURNING attempts;

-- name: DeleteObjectDeletion :exec
DELETE FROM manufacturing.object_deletions WHERE object_key = @object_key;

-- name: RecordObjectDeletionFailure :exec
UPDATE manufacturing.object_deletions
SET attempts = attempts + 1, last_error = @last_error, next_attempt_at = @next_attempt_at, claimed_until = NULL
WHERE object_key = @object_key;

-- name: NextDeviationNumber :one
-- NextDeviationNumber allocates the next DEV-<n> under the counter row's
-- lock, from 1.
INSERT INTO manufacturing.counters (counter_name, next_value) VALUES ('deviation', 2)
ON CONFLICT (counter_name) DO UPDATE SET next_value = manufacturing.counters.next_value + 1
RETURNING (next_value - 1)::bigint AS number;

-- name: InsertDeviation :one
INSERT INTO manufacturing.deviations (number, title, description, order_id, work_order_id, check_result_id, lot_id, lot_number, variant_id,
                                      quarantine, opened_by_user_id, opened_by_display, opened_at, station_id)
VALUES (@number, @title, @description, sqlc.narg(order_id), sqlc.narg(work_order_id), sqlc.narg(check_result_id), sqlc.narg(lot_id),
        sqlc.narg(lot_number), sqlc.narg(variant_id), @quarantine, sqlc.narg(opened_by_user_id), @opened_by_display, @now, sqlc.narg(station_id))
RETURNING *;

-- name: OpenDeviationOfCheck :one
-- OpenDeviationOfCheck takes the open deviation a failed result of the check
-- opened, FOR UPDATE after the check: a fail recorded again while it is open
-- is added to it rather than opening another (M99).
SELECT d.* FROM manufacturing.deviations d
JOIN manufacturing.check_results r ON r.id = d.check_result_id
WHERE r.work_order_check_id = @work_order_check_id AND d.status = 'open'
ORDER BY d.id
LIMIT 1
FOR UPDATE OF d;

-- name: AddResultToDeviation :one
-- AddResultToDeviation points the deviation at the check's latest failed
-- result and adds its reading to the description, within its 5000
-- characters.
UPDATE manufacturing.deviations
SET check_result_id = @check_result_id, description = left(description || @addition::text, 5000), revision = revision + 1
WHERE id = @id
RETURNING *;

-- name: LotOfOrder :one
-- LotOfOrder reports whether the order's productions made the lot or
-- consumed it.
SELECT (EXISTS (SELECT 1 FROM manufacturing.production_outputs po JOIN manufacturing.productions p ON p.id = po.production_id
                WHERE p.order_id = @order_id AND po.lot_id = @lot_id)
     OR EXISTS (SELECT 1 FROM manufacturing.production_line_lots l JOIN manufacturing.productions p ON p.id = l.production_id
                WHERE p.order_id = @order_id AND l.lot_id = @lot_id))::boolean AS of_order;

-- name: GetDeviation :one
SELECT * FROM manufacturing.deviations WHERE id = @id;

-- name: GetDeviationForUpdate :one
-- GetDeviationForUpdate takes the deviation after its order (M99).
SELECT * FROM manufacturing.deviations WHERE id = @id FOR UPDATE;

-- name: CloseDeviation :one
UPDATE manufacturing.deviations
SET status = 'closed', disposition = @disposition, closing_note = @closing_note, closed_by_user_id = sqlc.narg(closed_by_user_id),
    closed_by_display = @closed_by_display, closed_at = @now, holds_released = @holds_released, revision = revision + 1
WHERE id = @id
RETURNING *;

-- name: MarkDeviationReleased :one
UPDATE manufacturing.deviations SET holds_released = true, revision = revision + 1 WHERE id = @id RETURNING *;

-- name: OpenQuarantineDeviationsOfOrder :many
-- OpenQuarantineDeviationsOfOrder is the order's open deviations that hold
-- its output (M99), by id: a production holds each lot it makes from each.
SELECT * FROM manufacturing.deviations
WHERE order_id = @order_id AND status = 'open' AND quarantine
ORDER BY id;

-- name: OutputLotsOfOrder :many
-- OutputLotsOfOrder is every lot the order's productions made so far, once,
-- with its number, by lot.
SELECT DISTINCT ON (po.lot_id) po.lot_id, po.lot_number
FROM manufacturing.production_outputs po
JOIN manufacturing.productions p ON p.id = po.production_id
WHERE p.order_id = @order_id
ORDER BY po.lot_id;

-- name: InsertDeviationLot :exec
INSERT INTO manufacturing.deviation_lots (deviation_id, lot_id, lot_number, held_at)
VALUES (@deviation_id, @lot_id, @lot_number, @now)
ON CONFLICT (deviation_id, lot_id) DO NOTHING;

-- name: LotsOfDeviations :many
SELECT * FROM manufacturing.deviation_lots WHERE deviation_id = ANY(@deviation_ids::bigint[]) ORDER BY deviation_id, lot_number, lot_id;

-- name: ListDeviations :many
-- ListDeviations is a page of deviations by the filters, newest first.
SELECT sqlc.embed(d)
FROM manufacturing.deviations d
LEFT JOIN manufacturing.orders o ON o.id = d.order_id
WHERE (sqlc.narg(status)::text IS NULL OR d.status = sqlc.narg(status))
  AND (sqlc.narg(order_id)::bigint IS NULL OR d.order_id = sqlc.narg(order_id))
  AND (sqlc.narg(variant_id)::int IS NULL OR COALESCE(d.variant_id, o.variant_id) = sqlc.narg(variant_id))
  AND (sqlc.narg(lot_id)::bigint IS NULL OR d.lot_id = sqlc.narg(lot_id)
       OR EXISTS (SELECT 1 FROM manufacturing.deviation_lots l WHERE l.deviation_id = d.id AND l.lot_id = sqlc.narg(lot_id)))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR d.opened_at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR d.opened_at < sqlc.narg(to_at))
ORDER BY d.opened_at DESC, d.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountDeviations :one
SELECT count(*)::int
FROM manufacturing.deviations d
LEFT JOIN manufacturing.orders o ON o.id = d.order_id
WHERE (sqlc.narg(status)::text IS NULL OR d.status = sqlc.narg(status))
  AND (sqlc.narg(order_id)::bigint IS NULL OR d.order_id = sqlc.narg(order_id))
  AND (sqlc.narg(variant_id)::int IS NULL OR COALESCE(d.variant_id, o.variant_id) = sqlc.narg(variant_id))
  AND (sqlc.narg(lot_id)::bigint IS NULL OR d.lot_id = sqlc.narg(lot_id)
       OR EXISTS (SELECT 1 FROM manufacturing.deviation_lots l WHERE l.deviation_id = d.id AND l.lot_id = sqlc.narg(lot_id)))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR d.opened_at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR d.opened_at < sqlc.narg(to_at));

-- name: WorkOrderNames :many
SELECT id, name, sequence FROM manufacturing.work_orders WHERE id = ANY(@ids::bigint[]) ORDER BY id;
