-- Task 12 (the retention worker, SV/CommunicationsRetentionWorker.cs:9-44 +
-- SV/RetentionCleanupService.cs:15-116; communications inventory §12.1, design
-- doc §4 and D3). Every query here belongs to retention.go.
--
-- THE ORDER OF THE TWO DELETES BELOW IS LOAD-BEARING. See
-- DeleteMessageEventsByMessageIDs.

-- name: SelectRetentionMessageIDs :many
-- Candidate set 1 (`:53-61`), the only one that survives the scope cut: .NET's
-- sets 2 and 3 select inbound_email_jobs and orphan inbound_receipts, and both
-- tables are dropped with the inbound subsystem (inventory §9, §12.1).
--
-- "Terminal" for a message means it has no outbox job at all, or every job it
-- has is completed/cancelled/failed. .NET writes that as
-- `!Any(job) || All(job => terminal)`; the NOT EXISTS below is the same
-- predicate stated once — a message is a candidate exactly when no
-- non-terminal job references it. Queued and retryable work is deliberately
-- excluded so retention cannot interrupt delivery
-- (SV/RetentionCleanupService.cs:11-14, pinned by
-- TS/Integration/RetentionCleanupTests.cs:11-17): a message with a pending,
-- retry or processing job is never deleted, however old it is.
--
-- The id tiebreaker is this port's own addition to .NET's bare
-- `OrderBy(CreatedAt)`: it cannot change which rows are eligible, only make
-- the batch boundary deterministic when several messages share a timestamp.
SELECT m.id
FROM communications.conversation_messages m
WHERE m.created_at < @cutoff
  AND NOT EXISTS (
      SELECT 1 FROM communications.outbox_jobs j
      WHERE j.message_id = m.id
        AND j.status NOT IN ('completed', 'cancelled', 'failed')
  )
ORDER BY m.created_at ASC, m.id ASC
LIMIT @batch_size;

-- name: DeleteMessageEventsByMessageIDs :exec
-- Step 1 (`:85`), and it MUST run before DeleteMessageDeliveriesByMessageIDs.
-- message_events.delivery_id -> message_deliveries.id is ON DELETE RESTRICT
-- (migration 00006, inventory §10 item 8, §19.1 item 6) — the only Restrict FK
-- between two tables retention deletes together, every other FK in the schema
-- being Cascade or SetNull. Deleting the deliveries first raises SQLSTATE
-- 23001, and since the whole batch is one transaction the batch rolls back:
-- retention would never drain and never recover on its own. Pinned by
-- TestRetention_DeletesEventsBeforeDeliveries.
DELETE FROM communications.message_events WHERE message_id = ANY(@message_ids::uuid[]);

-- name: DeleteMessageDeliveriesByMessageIDs :exec
-- Step 2 (`:86`). See the ordering note above.
DELETE FROM communications.message_deliveries WHERE message_id = ANY(@message_ids::uuid[]);

-- name: ListRetentionAttachments :many
-- Step 3's attachment read (`:87`): every attachment object in the batch,
-- with the message that owns it, so its key can be queued for deletion before
-- the rows identifying the owner are gone.
SELECT id, message_id, storage_key
FROM communications.message_attachments
WHERE message_id = ANY(@message_ids::uuid[])
ORDER BY created_at ASC, id ASC;

-- name: ListRetentionRawPayloadKeys :many
-- Step 3's raw-payload read (`:88-91`): the non-null
-- conversation_messages.raw_payload_storage_key values in the batch. Nothing
-- in this port writes that column — its only .NET writer was the inbound
-- processor (migration 00006's comment) — so this returns nothing in
-- production today and is kept because the column is, for the day an inbound
-- provider lands.
SELECT id, raw_payload_storage_key
FROM communications.conversation_messages
WHERE id = ANY(@message_ids::uuid[]) AND raw_payload_storage_key IS NOT NULL
ORDER BY created_at ASC, id ASC;

-- name: DeleteMessageAttachmentsByMessageIDs :exec
-- Step 6 (`:103`), after every reservation for those attachments is written.
DELETE FROM communications.message_attachments WHERE message_id = ANY(@message_ids::uuid[]);

-- name: DeleteIdempotencyRecordsByMessageIDs :exec
-- Step 6 (`:104`), and mandatory rather than incidental:
-- idempotency_records.message_id has NO foreign key at all, only an index
-- (migration 00006, inventory §12.1), so nothing cascades these rows. Drop
-- this statement and every deleted message leaks one idempotency record
-- forever.
DELETE FROM communications.idempotency_records WHERE message_id = ANY(@message_ids::uuid[]);

-- name: DeleteOutboxJobsByMessageIDs :exec
-- Step 6 (`:105`). outbox_jobs.message_id IS a Cascade FK, so this is
-- redundant against the message delete that follows — .NET issues it anyway
-- and so do we, because the batch's own ordering should not depend on a
-- cascade to be correct.
DELETE FROM communications.outbox_jobs WHERE message_id = ANY(@message_ids::uuid[]);

-- name: DeleteConversationMessagesByIDs :execrows
-- Step 6's last delete (`:106`) and the batch's return value: post-port the
-- deleted-message count IS the whole return value, since the two inbound
-- tables that also contributed to it are gone. The drain loop keys off it.
DELETE FROM communications.conversation_messages WHERE id = ANY(@ids::uuid[]);

-- name: DeleteOrphanConversationParticipants :exec
-- Step 7 (`:111`): a conversation left with no messages loses its participant
-- links. Deliberately unconditional — .NET runs it on every batch, not only
-- when the batch deleted something.
DELETE FROM communications.conversation_participants cp
WHERE NOT EXISTS (
    SELECT 1 FROM communications.conversation_messages m WHERE m.conversation_id = cp.conversation_id
);

-- name: DeleteOrphanParticipants :exec
-- Step 7 (`:112`): a participant left with no remaining link goes too.
DELETE FROM communications.participants p
WHERE NOT EXISTS (
    SELECT 1 FROM communications.conversation_participants cp WHERE cp.participant_id = p.id
);

-- name: SelectExpiredAttachmentUploads :many
-- The staged-upload expiry sweep re-homed from the deleted scanner
-- (ExpireUploadsAsync, SV/AttachmentScanning.cs:251-280; design doc D3,
-- inventory §5.8 and §19.1 item 2). Without it nothing expires a staged upload
-- or queues its object, and staged objects leak despite expires_at being set.
--
-- .NET's predicate also carried `(scan_status != 'scanning' OR
-- scan_lease_until <= now)`; that disjunct dies with the five scan columns the
-- port drops (migration 00006) — no row can be 'scanning' when nothing writes
-- it. 'quarantined', 'expired' and 'claimed' are excluded exactly as .NET
-- excludes them: 'claimed' in particular is a reply holding the upload inside
-- its own transaction, and expiring it would queue a live message's object for
-- deletion.
SELECT id, storage_key
FROM communications.attachment_uploads
WHERE expires_at < @now AND scan_status NOT IN ('quarantined', 'expired', 'claimed')
ORDER BY expires_at ASC, id ASC
LIMIT @batch_size;

-- name: ExpireAttachmentUpload :execrows
-- The sweep's conditional claim (`:268-274`): the same predicate as the select
-- above, re-asserted as an UPDATE. .NET's comment says why it must be
-- conditional — "A concurrent reply can win with the clean -> claimed
-- transition, in which case this worker must not enqueue that object's
-- deletion" — so the caller queues the object only when this reports one row.
-- scan_lease_id / scan_lease_until are not cleared here because the port drops
-- both columns.
UPDATE communications.attachment_uploads
SET scan_status = 'expired'
WHERE id = @id AND expires_at < @now AND scan_status NOT IN ('quarantined', 'expired', 'claimed');
