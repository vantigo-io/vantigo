-- name: InsertDocument :one
-- InsertDocument records a stored PDF (P21), after it was put in the store:
-- once per order, kind and version.
INSERT INTO purchasing.documents (order_id, kind, version, storage_key, sha256, size_bytes, created_at)
VALUES (sqlc.arg(order_id), sqlc.arg(kind), sqlc.arg(version), sqlc.arg(storage_key), sqlc.arg(sha256),
    sqlc.arg(size_bytes), sqlc.arg(created_at))
RETURNING *;

-- name: DocumentOf :one
SELECT * FROM purchasing.documents
WHERE order_id = sqlc.arg(order_id) AND kind = sqlc.arg(kind) AND version = sqlc.arg(version);

-- name: GetDocument :one
SELECT * FROM purchasing.documents WHERE id = sqlc.arg(id) AND order_id = sqlc.arg(order_id);

-- name: DocumentsOf :many
SELECT * FROM purchasing.documents WHERE order_id = sqlc.arg(order_id) ORDER BY id DESC;

-- name: InsertMailing :one
-- InsertMailing logs a mailing (P21, A19): a person's send after it was
-- made, sent; or one the provider refused, queued for the purchasing.mail
-- worker's retry at next_attempt_at.
INSERT INTO purchasing.mailings (
    order_id, kind, version, document_id, recipient, cc, subject, message, channel_id, status, provider_message_id,
    attempts, next_attempt_at, last_error, sent_at, sent_by_user_id, sent_by_display, created_at
) VALUES (
    sqlc.arg(order_id), sqlc.arg(kind), sqlc.arg(version), sqlc.narg(document_id), sqlc.arg(recipient), sqlc.arg(cc),
    sqlc.arg(subject), sqlc.arg(message), sqlc.narg(channel_id), sqlc.arg(status), sqlc.narg(provider_message_id),
    sqlc.arg(attempts), sqlc.narg(next_attempt_at), sqlc.narg(last_error), sqlc.narg(sent_at), sqlc.narg(sent_by_user_id),
    sqlc.narg(sent_by_display), sqlc.arg(created_at)
)
RETURNING *;

-- name: MailingsOf :many
SELECT * FROM purchasing.mailings WHERE order_id = sqlc.arg(order_id) ORDER BY id DESC;

-- name: LatestMailing :one
SELECT * FROM purchasing.mailings WHERE order_id = sqlc.arg(order_id) AND kind = sqlc.arg(kind) ORDER BY id DESC LIMIT 1;

-- name: ClaimMailing :one
-- ClaimMailing takes the next queued mailing due by @now for the
-- purchasing.mail worker: one conditional UPDATE over a SKIP LOCKED pick, so
-- two workers never take one row. The claim pushes next_attempt_at out by
-- the lease, so a worker that dies mid-send leaves the row claimable again
-- once the lease has run out. No row is pgx.ErrNoRows.
UPDATE purchasing.mailings
SET next_attempt_at = sqlc.arg(lease_until)::timestamptz,
    provider_message_id = coalesce(provider_message_id, sqlc.arg(message_id)::text)
WHERE status = 'queued' AND id = (
    SELECT m.id FROM purchasing.mailings m
    WHERE m.status = 'queued' AND m.next_attempt_at <= sqlc.arg(now)::timestamptz
    ORDER BY m.next_attempt_at, m.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkMailingSent :execrows
-- MarkMailingSent records a claimed mailing as sent, only while it is still
-- queued under the claim's lease.
UPDATE purchasing.mailings
SET status = 'sent', sent_at = sqlc.arg(now), attempts = attempts + 1, last_error = NULL, next_attempt_at = NULL,
    channel_id = coalesce(channel_id, sqlc.narg(channel_id))
WHERE id = sqlc.arg(id) AND status = 'queued' AND next_attempt_at = sqlc.arg(lease_until)::timestamptz;

-- name: RetryMailing :execrows
-- RetryMailing counts a failed attempt of a claimed mailing and puts it back
-- for @next_attempt_at, or fails it for good when @fail is set.
UPDATE purchasing.mailings
SET attempts = attempts + 1, last_error = sqlc.arg(last_error)::text,
    status = CASE WHEN sqlc.arg(fail)::boolean THEN 'failed' ELSE 'queued' END,
    next_attempt_at = CASE WHEN sqlc.arg(fail)::boolean THEN NULL ELSE sqlc.arg(next_attempt_at)::timestamptz END
WHERE id = sqlc.arg(id) AND status = 'queued' AND next_attempt_at = sqlc.arg(lease_until)::timestamptz;

-- name: NextDeletion :one
-- NextDeletion is the next stored object the supplier's erase wrote to the
-- ledger (A8) that is not deleted yet and has attempts left, after @after_id.
SELECT * FROM purchasing.deletions
WHERE completed_at IS NULL AND attempts < sqlc.arg(max_attempts) AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT 1;

-- name: CompleteDeletion :exec
UPDATE purchasing.deletions SET completed_at = sqlc.arg(now), attempts = attempts + 1, last_error = NULL
WHERE id = sqlc.arg(id) AND completed_at IS NULL;

-- name: FailDeletion :exec
UPDATE purchasing.deletions SET attempts = attempts + 1, last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id) AND completed_at IS NULL;
