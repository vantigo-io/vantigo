-- name: InsertAPIRequests :copyfrom
-- InsertAPIRequests is the request log's buffered writer's one write (API
-- access design K23): a batch of rows, copied in.
INSERT INTO identity.api_requests (
    at, key_id, integration_id, method, operation_id, status, reason, duration_ms, client_ip, trace_id
) VALUES (
    @at, @key_id, @integration_id, @method, @operation_id, @status, @reason, @duration_ms, @client_ip, @trace_id
);

-- name: EnsureAPIRequestsPartition :one
-- EnsureAPIRequestsPartition makes day's partition if it is missing,
-- draining DEFAULT of that day's rows first (K39), and answers whether it did.
SELECT identity.ensure_api_requests_partition(@day::date)::boolean AS created;

-- name: DropAPIRequestsBefore :one
-- DropAPIRequestsBefore drops the days before day and answers how many.
SELECT identity.drop_api_requests_before(@day::date)::int AS dropped;

-- name: ListDefaultAPIRequestDays :many
-- ListDefaultAPIRequestDays is the UTC days DEFAULT holds rows of, from since
-- on: the days the worker moves into partitions of their own.
SELECT DISTINCT (at AT TIME ZONE 'UTC')::date AS day
FROM identity.api_requests_default
WHERE at >= @since::timestamptz
ORDER BY day;

-- name: APIRequestDailyCounts :many
-- APIRequestDailyCounts is the integration's requests per UTC day and status
-- class (2 for 2xx, …) in [from, to), under the screen's filters.
SELECT (r.at AT TIME ZONE 'UTC')::date AS day, (r.status / 100)::int AS status_class, count(*)::int AS requests
FROM identity.api_requests r
WHERE r.integration_id = @integration_id
  AND r.at >= @from_at::timestamptz AND r.at < @to_at::timestamptz
  AND (sqlc.narg(key_id)::uuid IS NULL OR r.key_id = sqlc.narg(key_id)::uuid)
  AND (sqlc.narg(status_class)::int IS NULL OR r.status / 100 = sqlc.narg(status_class)::int)
  AND (sqlc.narg(reason)::text IS NULL OR r.reason = sqlc.narg(reason)::text)
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: APIRequestTopOperations :many
-- APIRequestTopOperations is the operations the integration called most in
-- [from, to), under the screen's filters.
SELECT r.operation_id, count(*)::int AS requests
FROM identity.api_requests r
WHERE r.integration_id = @integration_id
  AND r.at >= @from_at::timestamptz AND r.at < @to_at::timestamptz
  AND (sqlc.narg(key_id)::uuid IS NULL OR r.key_id = sqlc.narg(key_id)::uuid)
  AND (sqlc.narg(status_class)::int IS NULL OR r.status / 100 = sqlc.narg(status_class)::int)
  AND (sqlc.narg(reason)::text IS NULL OR r.reason = sqlc.narg(reason)::text)
GROUP BY r.operation_id
ORDER BY requests DESC, r.operation_id
LIMIT @top::int;

-- name: APIRequestsLatest :many
-- APIRequestsLatest is the integration's latest requests in [from, to),
-- under the screen's filters, each with its key's name while the key's row
-- is kept.
SELECT r.at, r.key_id, k.name AS key_name, r.method, r.operation_id, r.status, r.reason,
       r.duration_ms, r.client_ip, r.trace_id
FROM identity.api_requests r
LEFT JOIN identity.api_keys k ON k.id = r.key_id
WHERE r.integration_id = @integration_id
  AND r.at >= @from_at::timestamptz AND r.at < @to_at::timestamptz
  AND (sqlc.narg(key_id)::uuid IS NULL OR r.key_id = sqlc.narg(key_id)::uuid)
  AND (sqlc.narg(status_class)::int IS NULL OR r.status / 100 = sqlc.narg(status_class)::int)
  AND (sqlc.narg(reason)::text IS NULL OR r.reason = sqlc.narg(reason)::text)
ORDER BY r.at DESC
LIMIT @latest::int;

-- name: IntegrationExists :one
SELECT EXISTS (SELECT 1 FROM identity.integrations WHERE user_id = @user_id)::boolean;

-- name: ListAPIKeyNoticesDue :many
-- ListAPIKeyNoticesDue is every key owed an expiry notice at now (K25), with
-- the one kind it is owed: today on the UTC day it expires, before it has, 3_days
-- when it
-- expires 1 to 3 days ahead, 14_days 4 to 14 days ahead, each when not sent
-- yet. A key revoked, or rolled (another key's rolled_from), is owed none,
-- nor is one of an archived integration.
SELECT ak.id, ak.name, ak.display, ak.expires_at, i.user_id AS integration_id, i.name AS integration_name,
       i.contact_user_id, d.kind
FROM identity.api_keys ak
JOIN identity.integrations i ON i.user_id = ak.integration_id
CROSS JOIN LATERAL (
    SELECT (ak.expires_at AT TIME ZONE 'UTC')::date - (sqlc.arg(now)::timestamptz AT TIME ZONE 'UTC')::date AS days
) left_days
CROSS JOIN LATERAL (
    SELECT (CASE WHEN left_days.days = 0 THEN 'today'
                 WHEN left_days.days BETWEEN 1 AND 3 THEN '3_days'
                 ELSE '14_days' END)::text AS kind
) d
WHERE ak.revoked_at IS NULL
  AND ak.expires_at > sqlc.arg(now)::timestamptz
  AND i.archived_at IS NULL
  AND left_days.days BETWEEN 0 AND 14
  AND NOT EXISTS (SELECT 1 FROM identity.api_keys n WHERE n.rolled_from = ak.id)
  AND NOT EXISTS (SELECT 1 FROM identity.api_key_notices s WHERE s.key_id = ak.id AND s.kind = d.kind)
ORDER BY ak.expires_at, ak.id
LIMIT @batch_size;

-- name: InsertAPIKeyNotice :execrows
-- InsertAPIKeyNotice records a notice as sent; no row when it already was.
INSERT INTO identity.api_key_notices (key_id, kind, sent_at)
VALUES (@key_id, @kind, @now::timestamptz)
ON CONFLICT DO NOTHING;

-- name: ListOwnerIDs :many
-- ListOwnerIDs is every person holding Owner who is not disabled; the notice
-- itself skips whoever is not active (DirectoryUserForNotice).
SELECT u.id
FROM identity.users u
WHERE u.kind = 'person' AND NOT u.is_disabled
  AND EXISTS (
      SELECT 1
      FROM identity.user_roles ur
      JOIN identity.roles r ON r.id = ur.role_id
      WHERE ur.user_id = u.id AND r.name = 'Owner'
  )
ORDER BY u.id;

-- name: DeleteAPIKeyNotice :exec
-- DeleteAPIKeyNotice takes back a notice's record when it reached no one, so
-- the next cycle tries it again.
DELETE FROM identity.api_key_notices WHERE key_id = @key_id AND kind = @kind;
