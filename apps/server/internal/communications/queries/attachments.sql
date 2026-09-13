-- Attachment staging and download: postCommunicationsConversationsByIdAttachments,
-- getCommunicationsConversationsByConversationIdAttachmentsByAttachmentId,
-- getCommunicationsAttachmentsByIdDownload (communications inventory §5,
-- §12.3; EP/ConversationEndpoints.cs:103-236). Task 6's brief and dispatch
-- are the authority for the behaviour these back.

-- name: FindAttachmentUploadByUploaderAndKey :one
-- FindAttachmentUploadByUploaderAndKey is StageAttachment's replay lookup
-- (`:112-113`), keyed by (UploadedByUserId, IdempotencyKey) — per-user,
-- deliberately asymmetric with idempotency_records' global key (inventory
-- §5.1, §8's closing note). Checked before the conversation is even known to
-- exist: a replayed key returns the prior upload for a since-deleted
-- conversation too.
SELECT id, conversation_id, uploaded_by_user_id, file_name, content_type, size_bytes,
       content_hash, content_id, storage_key, scan_status, is_inline, idempotency_key,
       expires_at, created_at
FROM communications.attachment_uploads
WHERE uploaded_by_user_id = @uploaded_by_user_id AND idempotency_key = @idempotency_key;

-- name: CountLiveAttachmentUploads :one
-- CountLiveAttachmentUploads is the per-(conversation, user) cap check
-- (`:128`): every upload except one already `expired` or `quarantined`
-- counts against the limit of 20. Post-port every staged upload is born
-- `clean` (design doc D2), so in practice this simply counts every upload
-- for the pair — the exclusion stays because a hand-inserted fixture or a
-- future scanner can still produce those two statuses.
SELECT count(*)
FROM communications.attachment_uploads
WHERE conversation_id = @conversation_id AND uploaded_by_user_id = @uploaded_by_user_id
  AND scan_status NOT IN ('expired', 'quarantined');

-- name: InsertAttachmentUpload :one
-- InsertAttachmentUpload is StageAttachment's own row insert (`:158-175`).
-- scan_status is passed explicitly as 'clean' (design doc D2) rather than
-- relying on the column default, so the value is visible at the call site
-- that is the actual reason for it, not implicit in the schema.
INSERT INTO communications.attachment_uploads
    (id, conversation_id, uploaded_by_user_id, file_name, content_type, size_bytes,
     content_hash, content_id, storage_key, scan_status, is_inline, idempotency_key,
     expires_at, created_at)
VALUES (@id, @conversation_id, @uploaded_by_user_id, @file_name, @content_type, @size_bytes,
        @content_hash, @content_id, @storage_key, 'clean', @is_inline, @idempotency_key,
        @expires_at, @created_at)
RETURNING id, conversation_id, uploaded_by_user_id, file_name, content_type, size_bytes,
          content_hash, content_id, storage_key, scan_status, is_inline, idempotency_key,
          expires_at, created_at;

-- name: GetAttachmentUploadForCaller :one
-- GetAttachmentUploadForCaller is GetAttachmentUploadStatus's scoped lookup
-- (`:205-207`): (attachmentId, conversationId, uploaderUserId, scanStatus !=
-- 'expired') together, so a valid upload id cannot be used to probe another
-- conversation or another uploader's row (inventory §5.2).
SELECT id, conversation_id, uploaded_by_user_id, file_name, content_type, size_bytes,
       content_hash, content_id, storage_key, scan_status, is_inline, idempotency_key,
       expires_at, created_at
FROM communications.attachment_uploads
WHERE id = @id AND conversation_id = @conversation_id AND uploaded_by_user_id = @uploaded_by_user_id
  AND scan_status != 'expired';

-- name: GetCleanMessageAttachmentByID :one
-- GetCleanMessageAttachmentByID is DownloadAttachment's lookup (`:215-216`):
-- scan_status = 'clean' only, with no per-conversation scoping at all — any
-- conversations-view holder may fetch any clean attachment by id (inventory
-- §19.2 item 22, deliberately not "fixed"). message_attachments.message_id
-- is NOT NULL and conversation_messages.conversation_id is NOT NULL with an
-- ON DELETE CASCADE from conversations, so a row here already implies a live
-- conversation — .NET's `Message.Conversation != null` guard has no
-- reachable false case to reproduce.
SELECT id, message_id, file_name, content_type, size_bytes, storage_key
FROM communications.message_attachments
WHERE id = @id AND scan_status = 'clean';

-- name: FindLatestCleanupRecordByStorageKey :one
-- FindLatestCleanupRecordByStorageKey is ObjectOwnershipLifecycle.ReserveAsync's
-- own lookup (`:38-83`, inventory §12.3): the newest non-completed record for
-- storage_key. No unique index on storage_key backs this — a completed
-- historical row may legitimately coexist with a live reservation for a
-- reused key (migration 00006's comment on attachment_cleanup_records).
SELECT id, message_id, storage_key, status, attempts, next_attempt_at, lease_id,
       lease_until, reservation_expires_at, last_error, created_at
FROM communications.attachment_cleanup_records
WHERE storage_key = @storage_key AND status != 'completed'
ORDER BY created_at DESC
LIMIT 1;

-- name: InsertCleanupRecord :exec
-- InsertCleanupRecord is ReserveAsync's insert path (no existing record):
-- status 'staged', a fresh 10-minute reservation_expires_at (inventory
-- §12.3's ReservationLifetime). Written and committed *before* the
-- object-store write it guards (inventory §5.4 step 8) — the caller runs
-- this outside any transaction shared with the later attachment_uploads
-- insert, so it is durable on its own.
INSERT INTO communications.attachment_cleanup_records
    (id, message_id, storage_key, status, attempts, next_attempt_at, reservation_expires_at, created_at)
VALUES (@id, NULL, @storage_key, 'staged', 0, @next_attempt_at, @reservation_expires_at, @created_at);

-- name: ResetCleanupRecordToStaged :exec
-- ResetCleanupRecordToStaged is ReserveAsync's reuse path: an existing
-- non-completed, non-owned, non-deleting record for the same key (a
-- crash-retry that reserved and committed but never reached PutAsync) is
-- reset to 'staged' with a fresh reservation window, the same row rather
-- than a second one.
UPDATE communications.attachment_cleanup_records
SET status = 'staged', next_attempt_at = @next_attempt_at, reservation_expires_at = @reservation_expires_at,
    last_error = NULL
WHERE id = @id;

-- name: MarkCleanupRecordOwned :exec
-- MarkCleanupRecordOwned is ObjectOwnershipLifecycle.MarkOwnedAsync
-- (`:86-123`), called inside the same transaction and commit as the
-- attachment_uploads insert (inventory §5.4 step 11): the reservation
-- transitions to 'owned', its expiry clears, and any lease/error is
-- discarded.
UPDATE communications.attachment_cleanup_records
SET status = 'owned', reservation_expires_at = NULL, lease_id = NULL, lease_until = NULL, last_error = NULL
WHERE storage_key = @storage_key;
