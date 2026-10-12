-- The deletion ledger (customer attachments design D6): Staff's five
-- statements (internal/staff/queries/documents.sql, retention.sql) on this
-- module's own table. An attachment's row and its object cannot go in one
-- transaction, so the row's transaction queues the key here and the ledger
-- finishes the object afterwards — one attempt right after the commit, and
-- the anonymisation worker's passes until the store confirms.

-- name: InsertObjectDeletion :exec
-- InsertObjectDeletion queues an object for removal, in the transaction that
-- removed the row pointing at it; it is due at once.
INSERT INTO customers.object_deletions (object_key, next_attempt_at, created_at)
VALUES (@object_key, @now::timestamptz, @now::timestamptz)
ON CONFLICT (object_key) DO NOTHING;

-- name: RecordObjectDeletionFailure :exec
-- RecordObjectDeletionFailure records one failed attempt at removing an
-- object on its ledger row: the attempt counted, why, and when the next is
-- due; the claim is given up, so the row is the next due pass's.
UPDATE customers.object_deletions SET
    attempts = attempts + 1,
    last_error = @last_error,
    next_attempt_at = @next_attempt_at::timestamptz,
    claimed_until = NULL
WHERE object_key = @object_key;

-- name: DeleteObjectDeletion :exec
-- DeleteObjectDeletion clears an object's ledger row once the store has
-- confirmed the object is gone.
DELETE FROM customers.object_deletions WHERE object_key = @object_key;

-- name: DueObjectDeletions :many
-- DueObjectDeletions is the ledger rows a pass may claim: due, and not
-- claimed — or claimed by a pass whose window has run out. A hundred at a
-- time, the longest waiting first.
SELECT object_key
FROM customers.object_deletions
WHERE next_attempt_at <= @now::timestamptz
  AND (claimed_until IS NULL OR claimed_until < @now::timestamptz)
ORDER BY next_attempt_at, id
LIMIT @row_limit;

-- name: ClaimObjectDeletion :one
-- ClaimObjectDeletion claims one ledger row for @claimed_until by a
-- conditional update that asserts the candidate predicate again: of two
-- passes, on two replicas, that both selected the row, one updates it and the
-- other finds no row. It answers the attempts already failed.
UPDATE customers.object_deletions SET claimed_until = @claimed_until::timestamptz
WHERE object_key = @object_key
  AND next_attempt_at <= @now::timestamptz
  AND (claimed_until IS NULL OR claimed_until < @now::timestamptz)
RETURNING attempts;
