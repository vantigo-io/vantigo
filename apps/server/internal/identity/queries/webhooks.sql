-- name: AcquireWebhookCreationLock :exec
-- AcquireWebhookCreationLock serializes the creation of endpoints for the
-- rest of the transaction, so the per-workspace limit holds under
-- concurrency (outgoing webhooks design W36).
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: IntegrationArchivedAt :one
-- IntegrationArchivedAt reads whether an integration exists, and when it
-- was archived, without locking it.
SELECT archived_at FROM identity.integrations WHERE user_id = @user_id;

-- name: CountWebhookEndpoints :one
-- CountWebhookEndpoints is the endpoints of the integration and of the whole
-- workspace, counting only integrations that are not archived (W1).
SELECT count(*) FILTER (WHERE e.integration_id = @integration_id)::int AS integration_count,
       count(*)::int AS workspace_count
FROM identity.webhook_endpoints e
JOIN identity.integrations i ON i.user_id = e.integration_id
WHERE i.archived_at IS NULL;

-- name: InsertWebhookEndpoint :one
INSERT INTO identity.webhook_endpoints (
    id, integration_id, url, description, events, secret_ciphertext, created_by, created_at, updated_at, version
) VALUES (
    sqlc.arg(id), sqlc.arg(integration_id), sqlc.arg(url), sqlc.narg(description), sqlc.arg(events)::text[],
    sqlc.arg(secret_ciphertext), sqlc.narg(created_by), sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz, sqlc.arg(version)
)
RETURNING *;

-- name: ListWebhookEndpoints :many
-- ListWebhookEndpoints is an integration's endpoints, oldest first.
SELECT * FROM identity.webhook_endpoints
WHERE integration_id = @integration_id
ORDER BY created_at, id;

-- name: GetWebhookEndpoint :one
SELECT * FROM identity.webhook_endpoints
WHERE id = @id AND integration_id = @integration_id;

-- name: LockWebhookEndpoint :one
-- LockWebhookEndpoint holds an endpoint's row for a write, after its
-- integration's (W31): the fan-out reads it FOR SHARE, so a delivery is never
-- inserted for it after its pending ones were settled.
SELECT * FROM identity.webhook_endpoints
WHERE id = @id AND integration_id = @integration_id
FOR UPDATE;

-- name: LockIntegrationWebhookEndpoints :many
-- LockIntegrationWebhookEndpoints holds every endpoint of an integration, in
-- id order, for its archive (W2, W31).
SELECT * FROM identity.webhook_endpoints
WHERE integration_id = @integration_id
ORDER BY id
FOR UPDATE;

-- name: UpdateWebhookEndpoint :one
-- UpdateWebhookEndpoint replaces the URL, description and events (W10); the
-- secret is not rolled.
UPDATE identity.webhook_endpoints
SET url = sqlc.arg(url), description = sqlc.narg(description), events = sqlc.arg(events)::text[],
    updated_at = sqlc.arg(now)::timestamptz, version = sqlc.arg(version)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DisableWebhookEndpoint :one
-- DisableWebhookEndpoint disables an enabled endpoint for reason.
UPDATE identity.webhook_endpoints
SET status = 'disabled', disabled_reason = sqlc.arg(reason), disabled_at = sqlc.arg(now)::timestamptz,
    updated_at = sqlc.arg(now)::timestamptz, version = sqlc.arg(version)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: EnableWebhookEndpoint :one
-- EnableWebhookEndpoint enables a disabled endpoint: it starts again with no
-- failure counted, and a later disabling sends its notice again (W18, W32).
UPDATE identity.webhook_endpoints
SET status = 'enabled', disabled_reason = NULL, disabled_at = NULL, failing_since = NULL,
    disabled_notice_sent_at = NULL, updated_at = sqlc.arg(now)::timestamptz, version = sqlc.arg(version)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: RollWebhookEndpointSecret :one
-- RollWebhookEndpointSecret makes secret the endpoint's and keeps the one it
-- replaces signing until previous_until (W9); a previous secret still in its
-- overlap is dropped at once.
UPDATE identity.webhook_endpoints
SET previous_secret_ciphertext = secret_ciphertext, previous_secret_expires_at = sqlc.arg(previous_until)::timestamptz,
    secret_ciphertext = sqlc.arg(secret_ciphertext), updated_at = sqlc.arg(now)::timestamptz, version = sqlc.arg(version)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteWebhookEndpoint :exec
-- DeleteWebhookEndpoint deletes an endpoint and, by the cascade, its
-- deliveries and their attempts.
DELETE FROM identity.webhook_endpoints WHERE id = @id;

-- name: DeadLetterWebhookDeliveries :execrows
-- DeadLetterWebhookDeliveries settles every pending delivery of the
-- endpoints as dead for reason, so nothing stale is sent once one is enabled
-- again (W2, W18).
UPDATE identity.webhook_deliveries
SET state = 'dead', state_reason = sqlc.arg(reason), completed_at = sqlc.arg(now)::timestamptz,
    next_attempt_at = NULL, lease_id = NULL, lease_until = NULL
WHERE endpoint_id = ANY(sqlc.arg(endpoint_ids)::uuid[]) AND state = 'pending';
