-- name: InsertDocument :one
-- InsertDocument records a stored PDF (O24): once per owner, kind and
-- revision or version, immutable after.
INSERT INTO orders.documents (owner_kind, owner_id, kind, version, storage_key, sha256, size_bytes, created_at)
VALUES (sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(kind), sqlc.arg(version), sqlc.arg(storage_key),
        sqlc.arg(sha256), sqlc.arg(size_bytes), sqlc.arg(created_at))
RETURNING *;

-- name: DocumentsOf :many
-- DocumentsOf is a document's stored PDFs, newest first.
SELECT * FROM orders.documents
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id)
ORDER BY version DESC, id DESC;

-- name: DocumentOf :one
-- DocumentOf is the stored PDF of one revision or version. No row is
-- pgx.ErrNoRows.
SELECT * FROM orders.documents
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id)
  AND kind = sqlc.arg(kind) AND version = sqlc.arg(version);

-- name: GetDocument :one
SELECT * FROM orders.documents WHERE id = sqlc.arg(id);

-- name: InsertMailing :one
-- InsertMailing logs a send from the dialog after it was made (O25), sent or
-- failed.
INSERT INTO orders.mailings (
    owner_kind, owner_id, version, document_id, recipient, cc, subject, channel_id, status,
    provider_message_id, attempts, last_error, sent_at, sent_by_user_id, sent_by_display, created_at
) VALUES (
    sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(version), sqlc.narg(document_id), sqlc.arg(recipient),
    sqlc.arg(cc), sqlc.arg(subject), sqlc.narg(channel_id), sqlc.arg(status), sqlc.narg(provider_message_id),
    1, sqlc.narg(last_error), sqlc.narg(sent_at), sqlc.narg(sent_by_user_id), sqlc.narg(sent_by_display),
    sqlc.arg(created_at)
)
RETURNING *;

-- name: QueueMailing :execrows
-- QueueMailing queues an automatic send (O25) on the caller's own
-- transaction — an event handler's or a booking's — once per document,
-- version and triggering event: a repeat is a no-op (0 rows). The orders.mail
-- worker sends it.
INSERT INTO orders.mailings (
    owner_kind, owner_id, version, document_id, recipient, cc, subject, channel_id, status,
    trigger_event_id, attempts, next_attempt_at, created_at
) VALUES (
    sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(version), sqlc.narg(document_id), sqlc.arg(recipient),
    sqlc.arg(cc), sqlc.arg(subject), sqlc.narg(channel_id), 'queued', sqlc.arg(trigger_event_id), 0,
    sqlc.arg(created_at), sqlc.arg(created_at)
)
ON CONFLICT (owner_kind, owner_id, version, trigger_event_id) WHERE trigger_event_id IS NOT NULL DO NOTHING;

-- name: MailingsOf :many
-- MailingsOf is a document's sends, newest first.
SELECT * FROM orders.mailings
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id)
ORDER BY created_at DESC, id DESC;

-- name: ClaimMailing :one
-- ClaimMailing takes the next queued mailing due by @now for the orders.mail
-- worker: one conditional UPDATE over a SKIP LOCKED pick, so two workers
-- never take one row. The claim pushes next_attempt_at out by the lease, so a
-- worker that dies mid-send leaves the row claimable again once the lease has
-- run out, and gives the row its Message-ID once, kept by every retry. No row
-- is pgx.ErrNoRows.
UPDATE orders.mailings
SET next_attempt_at = sqlc.arg(lease_until)::timestamptz,
    provider_message_id = coalesce(provider_message_id, sqlc.arg(message_id)::text)
WHERE status = 'queued' AND id = (
    SELECT m.id FROM orders.mailings m
    WHERE m.status = 'queued' AND m.next_attempt_at <= sqlc.arg(now)::timestamptz
    ORDER BY m.next_attempt_at, m.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkMailingSent :execrows
-- MarkMailingSent records a claimed mailing as sent, only while it is still
-- queued under the claim's lease.
UPDATE orders.mailings
SET status = 'sent', sent_at = sqlc.arg(now), attempts = attempts + 1, last_error = NULL,
    next_attempt_at = NULL, document_id = coalesce(document_id, sqlc.narg(document_id)),
    channel_id = coalesce(channel_id, sqlc.narg(channel_id)), subject = sqlc.arg(subject)
WHERE id = sqlc.arg(id) AND status = 'queued' AND next_attempt_at = sqlc.arg(lease_until)::timestamptz;

-- name: RetryMailing :execrows
-- RetryMailing counts a failed attempt of a claimed mailing and puts it back
-- for @next_attempt_at, or fails it for good when @fail is set.
UPDATE orders.mailings
SET attempts = attempts + 1, last_error = sqlc.arg(last_error)::text,
    status = CASE WHEN sqlc.arg(fail)::boolean THEN 'failed' ELSE 'queued' END,
    next_attempt_at = CASE WHEN sqlc.arg(fail)::boolean THEN NULL ELSE sqlc.arg(next_attempt_at)::timestamptz END
WHERE id = sqlc.arg(id) AND status = 'queued' AND next_attempt_at = sqlc.arg(lease_until)::timestamptz;
