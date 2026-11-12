-- A lot's files (manufacturing phase 2 M54) and the deletion ledger of their
-- objects: Customers' attachments and object_deletions (00044), for lots.

-- name: LotAttachments :many
-- LotAttachments is a lot's files, newest first.
SELECT * FROM inventory.lot_attachments WHERE lot_id = @lot_id ORDER BY created_at DESC, id DESC;

-- name: LotAttachment :one
-- LotAttachment is one file of a lot; another lot's is no row.
SELECT * FROM inventory.lot_attachments WHERE id = @id AND lot_id = @lot_id;

-- name: CountLotAttachments :one
-- CountLotAttachments is how many files a lot carries, read under its lock
-- for the cap.
SELECT count(*)::int FROM inventory.lot_attachments WHERE lot_id = @lot_id;

-- name: InsertLotAttachment :one
INSERT INTO inventory.lot_attachments (lot_id, object_key, file_name, content_type, size_bytes,
                                       uploaded_by_user_id, uploaded_by_display, created_at)
VALUES (@lot_id, @object_key, @file_name, @content_type, @size_bytes,
        sqlc.narg(uploaded_by_user_id), sqlc.narg(uploaded_by_display), @now::timestamptz)
RETURNING *;

-- name: DeleteLotAttachment :one
-- DeleteLotAttachment removes a file's row and answers it; its object is
-- queued on the ledger in the same transaction (InsertObjectDeletion).
DELETE FROM inventory.lot_attachments WHERE id = @id AND lot_id = @lot_id RETURNING *;

-- name: InsertObjectDeletion :exec
-- InsertObjectDeletion queues an object for removal, in the transaction that
-- removed the row pointing at it; it is due at once.
INSERT INTO inventory.object_deletions (object_key, next_attempt_at, created_at)
VALUES (@object_key, @now::timestamptz, @now::timestamptz)
ON CONFLICT (object_key) DO NOTHING;

-- name: RecordObjectDeletionFailure :exec
-- RecordObjectDeletionFailure records one failed attempt at removing an
-- object: the attempt counted, why, and when the next is due; the claim is
-- given up, so the row is the next due pass's.
UPDATE inventory.object_deletions SET
    attempts = attempts + 1,
    last_error = @last_error,
    next_attempt_at = @next_attempt_at::timestamptz,
    claimed_until = NULL
WHERE object_key = @object_key;

-- name: DeleteObjectDeletion :exec
-- DeleteObjectDeletion clears an object's ledger row once the store has
-- confirmed the object is gone.
DELETE FROM inventory.object_deletions WHERE object_key = @object_key;

-- name: DueObjectDeletions :many
-- DueObjectDeletions is the ledger rows a pass may claim: due, and not
-- claimed — or claimed by a pass whose window has run out — the longest
-- waiting first.
SELECT object_key
FROM inventory.object_deletions
WHERE next_attempt_at <= @now::timestamptz
  AND (claimed_until IS NULL OR claimed_until < @now::timestamptz)
ORDER BY next_attempt_at, id
LIMIT @row_limit;

-- name: ClaimObjectDeletion :one
-- ClaimObjectDeletion claims one ledger row by a conditional update that
-- asserts the candidate predicate again: of two passes that both selected
-- the row, one updates it and the other finds no row. It answers the
-- attempts already failed.
UPDATE inventory.object_deletions SET claimed_until = @claimed_until::timestamptz
WHERE object_key = @object_key
  AND next_attempt_at <= @now::timestamptz
  AND (claimed_until IS NULL OR claimed_until < @now::timestamptz)
RETURNING attempts;
