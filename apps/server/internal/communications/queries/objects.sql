-- The object-ownership reservation protocol
-- (SV/ObjectOwnershipLifecycle.cs:14-189, communications inventory §12.3):
-- the queries objects.go needs on top of the three attachments.sql already
-- carries for staging (FindLatestCleanupRecordByStorageKey,
-- InsertCleanupRecord, ResetCleanupRecordToStaged, MarkCleanupRecordOwned).
--
-- These belong to the protocol, not to one caller: task 12's retention worker
-- queues deletions through them, and task 13's attachment-cleanup worker
-- reads the same ledger. They live in their own file for that reason.

-- name: CleanupRecordCompletedExists :one
-- The second half of QueueForDeletionAsync's no-live-record branch
-- (`:157-159`): when nothing non-completed exists for the key but a COMPLETED
-- record does, the queue call returns without writing anything, because a
-- completed reservation means the object is already gone. Without this check a
-- port would insert a fresh pending record on every retention pass over a
-- long-deleted object, and the cleanup worker — which has no terminal failure
-- state — would keep trying to delete a key that no longer exists.
SELECT EXISTS (
    SELECT 1 FROM communications.attachment_cleanup_records
    WHERE storage_key = @storage_key AND status = 'completed'
);

-- name: InsertPendingCleanupRecord :exec
-- QueueForDeletionAsync's insert (`:161-173`): a brand-new record, already
-- 'pending' (not 'staged' — nothing is being reserved here, the object is
-- being handed straight to the cleanup worker) and due immediately.
-- attempts is written explicitly because the column has no DDL default; so is
-- the id, which is a plain random uuid here exactly as .NET's Guid.NewGuid()
-- is — the deterministic id belongs to ReserveAsync, not to this path.
INSERT INTO communications.attachment_cleanup_records
    (id, message_id, storage_key, status, attempts, next_attempt_at, created_at)
VALUES (@id, @message_id, @storage_key, 'pending', 0, @next_attempt_at, @created_at);

-- name: MarkCleanupRecordPending :exec
-- QueueForDeletionAsync's update branch (`:176-182`): an existing live
-- reservation for the key — 'staged', 'pending' or, in the common case, the
-- 'owned' row the upload's promotion left — becomes 'pending' in place, with
-- the owning message recorded and the reservation window, lease and error all
-- cleared. One row per key: there is deliberately no unique index on
-- storage_key (inventory §10 item 11), so inserting instead of updating would
-- silently leave two live records for one object.
UPDATE communications.attachment_cleanup_records
SET message_id = @message_id,
    status = 'pending',
    next_attempt_at = @next_attempt_at,
    reservation_expires_at = NULL,
    last_error = NULL,
    lease_id = NULL,
    lease_until = NULL
WHERE id = @id;
