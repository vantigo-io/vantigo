-- The webhooks' fan-out and sender (outgoing webhooks design W14-W19,
-- W29-W33, W37, W38).

-- name: LockWebhookEndpointsForEvent :many
-- LockWebhookEndpointsForEvent holds, FOR SHARE and in id order, the enabled
-- endpoints listing the event whose integration is neither archived nor
-- disabled (W14, W31): a disable, an archive or a delete locks them FOR
-- UPDATE before settling their pending deliveries, so none is inserted after.
SELECT e.id, e.integration_id
FROM identity.webhook_endpoints e
JOIN identity.integrations i ON i.user_id = e.integration_id
JOIN identity.users u ON u.id = e.integration_id
WHERE e.status = 'enabled' AND e.events @> ARRAY[@event_name::text]
  AND i.archived_at IS NULL AND NOT u.is_disabled
ORDER BY e.id
FOR SHARE OF e;

-- name: InsertWebhookDelivery :execrows
-- InsertWebhookDelivery queues one message to one endpoint; a second
-- fan-out of the same event to it is a no-op, as the outbox is
-- at-least-once (W14). A ping has no event and never conflicts (W37).
INSERT INTO identity.webhook_deliveries (
    id, endpoint_id, event_id, event_name, kind, body, state, state_reason,
    max_run_attempts, next_attempt_at, created_at, completed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(endpoint_id), sqlc.narg(event_id), sqlc.arg(event_name), sqlc.arg(kind), sqlc.arg(body),
    sqlc.arg(state), sqlc.narg(state_reason), sqlc.arg(max_run_attempts), sqlc.narg(next_attempt_at),
    sqlc.arg(created_at)::timestamptz, sqlc.narg(completed_at)
)
ON CONFLICT (endpoint_id, event_id) DO NOTHING;

-- name: LockDueWebhookEndpoints :many
-- LockDueWebhookEndpoints is the claim's first step (W29 a): the enabled
-- endpoints of active integrations with a delivery due and not leased,
-- locked FOR UPDATE SKIP LOCKED in id order, so two senders never count the
-- same endpoint's leases at once and the cap of 4 in flight holds across
-- processes. The endpoint's URL and secrets are read here, as they are at
-- the attempt (W10).
SELECT e.id, e.integration_id, e.url, e.events, e.secret_ciphertext, e.previous_secret_ciphertext, e.previous_secret_expires_at
FROM identity.webhook_endpoints e
JOIN identity.integrations i ON i.user_id = e.integration_id
JOIN identity.users u ON u.id = e.integration_id
WHERE e.status = 'enabled' AND i.archived_at IS NULL AND NOT u.is_disabled
  AND EXISTS (
      SELECT 1 FROM identity.webhook_deliveries d
      WHERE d.endpoint_id = e.id AND d.state = 'pending' AND d.next_attempt_at <= sqlc.arg(now)::timestamptz
        AND (d.lease_until IS NULL OR d.lease_until <= sqlc.arg(now)::timestamptz))
ORDER BY e.id
FOR UPDATE OF e SKIP LOCKED;

-- name: LeaseWebhookDeliveries :many
-- LeaseWebhookDeliveries is the claim's second step (W29 b-d), a statement of
-- its own so its snapshot sees every lease committed before the endpoints
-- were locked: per locked endpoint it counts the unexpired leases, ranks the
-- due deliveries by creation, and leases those ranked within both the pass's
-- share (2) and what the endpoint's cap (4) leaves, at most batch_size in
-- all. A delivery's turn is its rank plus what its endpoint already has in
-- flight, so every endpoint with nothing in flight is served before any gets
-- a second send; and reserve of the batch is kept for such first sends, so
-- endpoints that hang never take every slot of the process (S1).
WITH inflight AS (
    SELECT d.endpoint_id, count(*)::int AS n
    FROM identity.webhook_deliveries d
    WHERE d.endpoint_id = ANY(sqlc.arg(endpoint_ids)::uuid[]) AND d.state = 'pending'
      AND d.lease_until > sqlc.arg(now)::timestamptz
    GROUP BY d.endpoint_id
), ranked AS (
    SELECT d.id, d.endpoint_id, d.created_at,
           row_number() OVER (PARTITION BY d.endpoint_id ORDER BY d.created_at, d.id) AS rank
    FROM identity.webhook_deliveries d
    WHERE d.endpoint_id = ANY(sqlc.arg(endpoint_ids)::uuid[]) AND d.state = 'pending'
      AND d.next_attempt_at <= sqlc.arg(now)::timestamptz
      AND (d.lease_until IS NULL OR d.lease_until <= sqlc.arg(now)::timestamptz)
), eligible AS (
    SELECT r.id, r.rank + COALESCE(f.n, 0) AS turn,
           row_number() OVER (ORDER BY r.rank + COALESCE(f.n, 0), r.created_at, r.id) AS ordinal
    FROM ranked r
    LEFT JOIN inflight f ON f.endpoint_id = r.endpoint_id
    WHERE r.rank <= LEAST(sqlc.arg(per_pass)::int, sqlc.arg(per_endpoint)::int - COALESCE(f.n, 0))
), chosen AS (
    SELECT e.id
    FROM eligible e
    WHERE e.ordinal <= sqlc.arg(batch_size)::int
      AND (e.turn = 1 OR e.ordinal <= sqlc.arg(batch_size)::int - sqlc.arg(reserve)::int)
)
UPDATE identity.webhook_deliveries d
SET lease_id = sqlc.arg(lease_id), lease_until = sqlc.arg(lease_until)::timestamptz
FROM chosen c
WHERE d.id = c.id AND d.state = 'pending'
  AND (d.lease_until IS NULL OR d.lease_until <= sqlc.arg(now)::timestamptz)
RETURNING d.id, d.endpoint_id, d.kind, d.event_name, d.body, d.run_attempts, d.max_run_attempts;

-- name: SkipWebhookDeliveries :execrows
-- SkipWebhookDeliveries settles leased deliveries whose integration no
-- longer holds their event's permission, or whose endpoint no longer lists
-- their event, as skipped instead of sending them (W33). The migration's
-- check ties skipped to permission_not_held, which covers both.
UPDATE identity.webhook_deliveries
SET state = 'skipped', state_reason = 'permission_not_held', completed_at = sqlc.arg(now)::timestamptz,
    next_attempt_at = NULL, lease_id = NULL, lease_until = NULL
WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND lease_id = sqlc.arg(lease_id) AND state = 'pending';

-- name: LockWebhookEndpointByID :one
-- LockWebhookEndpointByID holds an endpoint's row for an attempt's result,
-- before its delivery's: every writer that settles an endpoint's deliveries
-- locks the endpoint first (W31).
SELECT * FROM identity.webhook_endpoints WHERE id = @id FOR UPDATE;

-- name: RecordWebhookDeliveryResult :execrows
-- RecordWebhookDeliveryResult records an attempt's outcome on its delivery,
-- only while the claim's lease still holds it (W29, W38): a lease lost, or a
-- delivery settled or deleted meanwhile, records nothing.
UPDATE identity.webhook_deliveries
SET state = sqlc.arg(state), state_reason = sqlc.narg(state_reason),
    run_attempts = run_attempts + 1, total_attempts = total_attempts + 1,
    next_attempt_at = sqlc.narg(next_attempt_at), lease_id = NULL, lease_until = NULL,
    last_status = sqlc.narg(last_status), last_error = sqlc.narg(last_error),
    completed_at = sqlc.narg(completed_at)
WHERE id = sqlc.arg(id) AND lease_id = sqlc.arg(lease_id) AND state = 'pending';

-- name: InsertWebhookAttempt :exec
INSERT INTO identity.webhook_attempts (delivery_id, at, status_code, duration_ms, error, excerpt, secret_used)
VALUES (sqlc.arg(delivery_id), sqlc.arg(at)::timestamptz, sqlc.narg(status_code), sqlc.arg(duration_ms),
        sqlc.narg(error), sqlc.narg(excerpt), sqlc.arg(secret_used));

-- name: RecordWebhookEndpointSuccess :exec
-- RecordWebhookEndpointSuccess notes a success at at (W18): the latest
-- success is kept, and failing_since is cleared unless a failure later than
-- this success set it.
UPDATE identity.webhook_endpoints
SET last_success_at = GREATEST(COALESCE(last_success_at, sqlc.arg(at)::timestamptz), sqlc.arg(at)::timestamptz),
    failing_since = CASE WHEN failing_since > sqlc.arg(at)::timestamptz THEN failing_since END
WHERE id = sqlc.arg(id);

-- name: RecordWebhookEndpointFailure :exec
-- RecordWebhookEndpointFailure notes a failure at at (W18, W32): failing_since
-- keeps the first failure since the last success, and a failure older than
-- the last success, a result that raced it, sets nothing.
UPDATE identity.webhook_endpoints
SET failing_since = COALESCE(failing_since, sqlc.arg(at)::timestamptz)
WHERE id = sqlc.arg(id) AND (last_success_at IS NULL OR sqlc.arg(at)::timestamptz > last_success_at);

-- name: ListFailingWebhookEndpoints :many
-- ListFailingWebhookEndpoints is every enabled endpoint failing since before
-- cutoff with at least min_dead deliveries that died by running out of
-- attempts since then (W18, W32), in id order. Pings never count.
SELECT e.id
FROM identity.webhook_endpoints e
WHERE e.status = 'enabled' AND e.failing_since <= sqlc.arg(cutoff)::timestamptz
  AND (SELECT count(*) FROM identity.webhook_deliveries d
       WHERE d.endpoint_id = e.id AND d.kind = 'event' AND d.state = 'dead'
         AND d.state_reason = 'attempts_exhausted' AND d.completed_at >= e.failing_since) >= sqlc.arg(min_dead)::int
ORDER BY e.id;

-- name: LockFailingWebhookEndpoint :one
-- LockFailingWebhookEndpoint holds an endpoint ListFailingWebhookEndpoints
-- named while it still is one.
SELECT e.*
FROM identity.webhook_endpoints e
WHERE e.id = sqlc.arg(id) AND e.status = 'enabled' AND e.failing_since <= sqlc.arg(cutoff)::timestamptz
  AND (SELECT count(*) FROM identity.webhook_deliveries d
       WHERE d.endpoint_id = e.id AND d.kind = 'event' AND d.state = 'dead'
         AND d.state_reason = 'attempts_exhausted' AND d.completed_at >= e.failing_since) >= sqlc.arg(min_dead)::int
FOR UPDATE OF e;

-- name: ListWebhookNoticesDue :many
-- ListWebhookNoticesDue is the endpoints the sender disabled, as failing or
-- gone, since after since, whose notice no one has been mailed yet (W32): a
-- notice nobody could get for a week is given up.
SELECT id FROM identity.webhook_endpoints
WHERE status = 'disabled' AND disabled_reason IN ('failing', 'gone') AND disabled_notice_sent_at IS NULL
  AND disabled_at > sqlc.arg(since)::timestamptz
ORDER BY id
LIMIT sqlc.arg(batch_size)::int;

-- name: ClaimWebhookNotice :one
-- ClaimWebhookNotice claims an endpoint's disabled notice, as
-- api_key_notices' row claims a key's (W32), so another sender never mails
-- it too, and reads what the notice says.
UPDATE identity.webhook_endpoints e
SET disabled_notice_sent_at = sqlc.arg(now)::timestamptz
FROM identity.integrations i
WHERE e.id = sqlc.arg(id) AND i.user_id = e.integration_id AND e.status = 'disabled'
  AND e.disabled_reason IN ('failing', 'gone') AND e.disabled_notice_sent_at IS NULL
RETURNING e.id, e.url, e.disabled_reason, i.name AS integration_name, i.contact_user_id;

-- name: ReleaseWebhookNotice :exec
-- ReleaseWebhookNotice takes back a claim no one was mailed under, so the
-- notice is tried again.
UPDATE identity.webhook_endpoints
SET disabled_notice_sent_at = NULL
WHERE id = sqlc.arg(id) AND disabled_notice_sent_at = sqlc.arg(claimed_at)::timestamptz;

-- name: ExpireWebhookDeliveries :execrows
-- ExpireWebhookDeliveries settles as dead (expired) a batch of deliveries
-- still pending since before cutoff and not in flight (W30). The rows are
-- taken SKIP LOCKED, so a result being written for one is left to it, and
-- the outer WHERE checks again what the subquery chose, so a row that was
-- leased or settled meanwhile is never expired. A resent delivery's 3 days
-- run from its resend: resend_window_start is within a minute of the last
-- one (W35, an approximation: the window opens at the first resend of a
-- minute, so a later one in it measures from up to a minute earlier), so
-- its run is not expired the moment it starts.
UPDATE identity.webhook_deliveries
SET state = 'dead', state_reason = 'expired', completed_at = sqlc.arg(now)::timestamptz,
    next_attempt_at = NULL, lease_id = NULL, lease_until = NULL
WHERE id IN (
    SELECT d.id FROM identity.webhook_deliveries d
    WHERE d.state = 'pending' AND d.created_at < sqlc.arg(cutoff)::timestamptz
      AND (d.resend_window_start IS NULL OR d.resend_window_start < sqlc.arg(cutoff)::timestamptz)
      AND (d.lease_until IS NULL OR d.lease_until <= sqlc.arg(now)::timestamptz)
    ORDER BY d.created_at
    LIMIT sqlc.arg(batch_size)::int
    FOR UPDATE SKIP LOCKED)
  AND state = 'pending'
  AND (lease_until IS NULL OR lease_until <= sqlc.arg(now)::timestamptz);

-- name: PruneWebhookDeliveries :execrows
-- PruneWebhookDeliveries deletes a batch of deliveries created before
-- cutoff, whatever their state, with their attempts (W23, W30), skipping a
-- row another transaction holds, which the next sweep takes. A pending
-- delivery resent since the cutoff is left to finish its run, which the
-- 3-day expiry bounds.
DELETE FROM identity.webhook_deliveries
WHERE id IN (
    SELECT d.id FROM identity.webhook_deliveries d
    WHERE d.created_at < sqlc.arg(cutoff)::timestamptz
      AND NOT (d.state = 'pending' AND d.resend_window_start >= sqlc.arg(cutoff)::timestamptz)
    ORDER BY d.created_at
    LIMIT sqlc.arg(batch_size)::int
    FOR UPDATE SKIP LOCKED)
  AND created_at < sqlc.arg(cutoff)::timestamptz
  AND NOT (state = 'pending' AND resend_window_start >= sqlc.arg(cutoff)::timestamptz);

-- name: DeadLetterStaleWebhookDeliveries :execrows
-- DeadLetterStaleWebhookDeliveries settles as dead (expired) the pending
-- deliveries of the endpoints that have been due since before cutoff: what
-- waited, overdue, while their integration was disabled, too old to send
-- once it is enabled again (W30). A delivery queued long ago whose retry is
-- not yet that overdue keeps its run, as does one resent within that time.
UPDATE identity.webhook_deliveries
SET state = 'dead', state_reason = 'expired', completed_at = sqlc.arg(now)::timestamptz,
    next_attempt_at = NULL, lease_id = NULL, lease_until = NULL
WHERE endpoint_id = ANY(sqlc.arg(endpoint_ids)::uuid[]) AND state = 'pending'
  AND next_attempt_at < sqlc.arg(cutoff)::timestamptz
  AND (resend_window_start IS NULL OR resend_window_start < sqlc.arg(cutoff)::timestamptz);

-- name: RecentSkippedWebhookDelivery :one
-- RecentSkippedWebhookDelivery reports whether the endpoint already has a
-- skipped delivery of the event since since: the fan-out records at most one
-- a day per endpoint and event (S5), so an integration that lost a role does
-- not grow the log by a row per event.
SELECT EXISTS (
    SELECT 1 FROM identity.webhook_deliveries
    WHERE endpoint_id = sqlc.arg(endpoint_id) AND event_name = sqlc.arg(event_name)
      AND state = 'skipped' AND created_at >= sqlc.arg(since)::timestamptz
) AS found;
