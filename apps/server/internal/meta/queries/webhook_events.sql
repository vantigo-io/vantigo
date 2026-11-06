-- name: InsertWebhookEvent :one
INSERT INTO meta.webhook_events (received_at, payload, next_attempt_at)
VALUES (@now, @payload::jsonb, @now)
RETURNING id;

-- name: ClaimWebhookEvents :many
-- Takes up to @batch pending rows that are due and moves their next attempt to
-- the lease's end, so a replica that dies lets go of them by itself; two
-- replicas never take the same row.
UPDATE meta.webhook_events e SET next_attempt_at = @lease_until, attempts = e.attempts + 1
WHERE e.id IN (
    SELECT w.id FROM meta.webhook_events w
    WHERE w.state = 'pending' AND w.next_attempt_at <= @now
    ORDER BY w.id
    LIMIT @batch
    FOR UPDATE SKIP LOCKED
)
RETURNING e.id, e.received_at, e.payload, e.attempts;

-- name: MarkWebhookEventSplit :exec
UPDATE meta.webhook_events SET state = 'split', split_at = @now, last_error = NULL WHERE id = @id;

-- name: DeferWebhookEvent :exec
UPDATE meta.webhook_events SET next_attempt_at = @next_attempt_at, last_error = @last_error WHERE id = @id;

-- name: FailWebhookEvent :exec
UPDATE meta.webhook_events SET state = 'failed', last_error = @last_error WHERE id = @id;

-- name: DeleteOldWebhookEvents :execrows
DELETE FROM meta.webhook_events WHERE received_at < @before;
