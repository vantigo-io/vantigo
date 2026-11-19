-- The webhooks' delivery log and resend (outgoing webhooks design W22, W23,
-- W35).

-- name: ListWebhookDeliveries :many
-- ListWebhookDeliveries is a page of the endpoint's deliveries, newest first,
-- after the cursor (created_at, id) when there is one, under the log's
-- filters. ix_webhook_deliveries_endpoint serves it. The body is read too:
-- it is thin (W12), and the log answers without it.
SELECT * FROM identity.webhook_deliveries
WHERE endpoint_id = sqlc.arg(endpoint_id)
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state)::text)
  AND (sqlc.narg(event_name)::text IS NULL OR event_name = sqlc.narg(event_name)::text)
  AND (sqlc.narg(before_at)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size)::int;

-- name: GetWebhookDelivery :one
-- GetWebhookDelivery is one delivery of the endpoint, with its body.
SELECT * FROM identity.webhook_deliveries
WHERE id = sqlc.arg(id) AND endpoint_id = sqlc.arg(endpoint_id);

-- name: ListWebhookAttempts :many
-- ListWebhookAttempts is a delivery's latest attempts, newest first.
SELECT at, status_code, duration_ms, error, excerpt, secret_used
FROM identity.webhook_attempts
WHERE delivery_id = sqlc.arg(delivery_id)
ORDER BY at DESC, id DESC
LIMIT sqlc.arg(page_size)::int;

-- name: LockWebhookDelivery :one
-- LockWebhookDelivery holds one delivery of the endpoint for a resend, after
-- the endpoint's row (W31): every writer that settles a delivery holds its
-- endpoint first.
SELECT * FROM identity.webhook_deliveries
WHERE id = sqlc.arg(id) AND endpoint_id = sqlc.arg(endpoint_id)
FOR UPDATE;

-- name: ResendWebhookDelivery :one
-- ResendWebhookDelivery puts a settled delivery back to pending for a fresh
-- run of max_run_attempts, due at once, its body and id unchanged (W22), and
-- counts the resend in its window (W35). The last attempt's status and error
-- stay until the next attempt replaces them.
UPDATE identity.webhook_deliveries
SET state = 'pending', state_reason = NULL, completed_at = NULL, run_attempts = 0,
    max_run_attempts = sqlc.arg(max_run_attempts), next_attempt_at = sqlc.arg(now)::timestamptz,
    lease_id = NULL, lease_until = NULL,
    resend_count = sqlc.arg(resend_count), resend_window_start = sqlc.arg(resend_window_start)::timestamptz
WHERE id = sqlc.arg(id) AND state IN ('succeeded', 'dead')
RETURNING *;
