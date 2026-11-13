-- Shared shop-floor stations (manufacturing phase 2 M129–M143).

-- name: InsertStation :one
INSERT INTO identity.stations (id, name, idle_minutes, require_pin_with_badge, allowed_networks,
                               pairing_hash, pairing_expires_at, created_by, created_at)
VALUES (@id, @name, @idle_minutes, @require_pin_with_badge, sqlc.narg(allowed_networks)::cidr[],
        @pairing_hash, @pairing_expires_at, @created_by, @now)
RETURNING *;

-- name: ListStations :many
SELECT * FROM identity.stations ORDER BY lower(name), id;

-- name: GetStation :one
SELECT * FROM identity.stations WHERE id = @id;

-- name: LockStation :one
SELECT * FROM identity.stations WHERE id = @id FOR UPDATE;

-- name: UpdateStation :one
UPDATE identity.stations
SET name = @name, idle_minutes = @idle_minutes, require_pin_with_badge = @require_pin_with_badge,
    allowed_networks = sqlc.narg(allowed_networks)::cidr[]
WHERE id = @id
RETURNING *;

-- name: SetStationPairing :one
-- SetStationPairing issues a new one-time code (re-pairing). The token stays
-- until the code is used: pairing replaces it.
UPDATE identity.stations
SET pairing_hash = @pairing_hash, pairing_expires_at = @pairing_expires_at
WHERE id = @id
RETURNING *;

-- name: RevokeStation :one
-- RevokeStation clears the token and any code, so the very next request with
-- the old cookie finds no station.
UPDATE identity.stations
SET token_hash = NULL, token_expires_at = NULL, pairing_hash = NULL, pairing_expires_at = NULL,
    revoked_at = @now, revoked_by = @revoked_by
WHERE id = @id
RETURNING *;

-- name: LockStationByPairingHash :one
SELECT * FROM identity.stations WHERE pairing_hash = @pairing_hash FOR UPDATE;

-- name: PairStation :one
-- PairStation spends the code and replaces the token; a revoked station paired
-- again with a fresh code is in use again.
UPDATE identity.stations
SET token_hash = @token_hash, token_expires_at = @token_expires_at, paired_at = @now,
    pairing_hash = NULL, pairing_expires_at = NULL, revoked_at = NULL, revoked_by = NULL
WHERE id = @id
RETURNING *;

-- name: GetStationByTokenHash :one
SELECT * FROM identity.stations WHERE token_hash = @token_hash;

-- name: TouchStation :exec
-- TouchStation records the station's last request at most once a minute.
UPDATE identity.stations
SET last_seen_at = sqlc.arg(now)::timestamptz, last_seen_ip = sqlc.narg(ip)
WHERE id = @id
  AND (last_seen_at IS NULL OR last_seen_at <= sqlc.arg(now)::timestamptz - interval '1 minute');

-- name: MarkStationAlerted :execrows
-- MarkStationAlerted claims the station's alert for the hour: one row when no
-- alert went out in the last hour, none otherwise.
UPDATE identity.stations
SET alerted_at = sqlc.arg(now)::timestamptz
WHERE id = @id
  AND (alerted_at IS NULL OR alerted_at <= sqlc.arg(now)::timestamptz - interval '1 hour');

-- name: GetUserPin :one
SELECT * FROM identity.user_pins WHERE user_id = @user_id;

-- name: UpsertUserPin :exec
INSERT INTO identity.user_pins (user_id, pin_hash, set_at)
VALUES (@user_id, @pin_hash, @now)
ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = EXCLUDED.set_at;

-- name: DeleteUserPin :execrows
DELETE FROM identity.user_pins WHERE user_id = @user_id;

-- name: GetUserBadge :one
SELECT * FROM identity.user_badges WHERE user_id = @user_id;

-- name: GetUserBadgeByHash :one
SELECT * FROM identity.user_badges WHERE badge_hash = @badge_hash;

-- name: UpsertUserBadge :exec
INSERT INTO identity.user_badges (user_id, badge_hash, set_at, set_by)
VALUES (@user_id, @badge_hash, @now, @set_by)
ON CONFLICT (user_id) DO UPDATE SET badge_hash = EXCLUDED.badge_hash, set_at = EXCLUDED.set_at, set_by = EXCLUDED.set_by;

-- name: DeleteUserBadge :execrows
DELETE FROM identity.user_badges WHERE user_id = @user_id;

-- name: UserExists :one
SELECT EXISTS (SELECT 1 FROM identity.users WHERE id = @id AND kind = 'person')::boolean AS present;

-- name: GetOperatorBackoff :one
SELECT * FROM identity.operator_backoff WHERE station_id = @station_id AND user_id = @user_id;

-- name: RecordOperatorFailure :one
-- RecordOperatorFailure counts one more consecutive failure of a user at a
-- station; only a success (DeleteOperatorBackoff) ends the run.
INSERT INTO identity.operator_backoff (station_id, user_id, failures, first_failure_at, last_failure_at)
VALUES (@station_id, @user_id, 1, sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz)
ON CONFLICT (station_id, user_id) DO UPDATE
SET failures = identity.operator_backoff.failures + 1,
    last_failure_at = EXCLUDED.last_failure_at
RETURNING *;

-- name: SetOperatorNextAttempt :exec
UPDATE identity.operator_backoff SET next_attempt_at = @next_attempt_at
WHERE station_id = @station_id AND user_id = @user_id;

-- name: DeleteOperatorBackoff :exec
DELETE FROM identity.operator_backoff WHERE station_id = @station_id AND user_id = @user_id;

-- name: EndStaleOperatorSessions :exec
-- EndStaleOperatorSessions ends the station's sessions idle past its timeout
-- (idle) or older than the absolute bound (expired), before a count.
UPDATE identity.station_operator_sessions
SET ended_at = sqlc.arg(now)::timestamptz, token_hash = NULL,
    end_reason = CASE WHEN started_at <= sqlc.arg(now)::timestamptz - sqlc.arg(absolute)::interval THEN 'expired' ELSE 'idle' END
WHERE station_id = @station_id AND ended_at IS NULL
  AND (last_activity_at <= sqlc.arg(now)::timestamptz - sqlc.arg(idle)::interval
       OR started_at <= sqlc.arg(now)::timestamptz - sqlc.arg(absolute)::interval);

-- name: EndUserOperatorSessionsAtStation :many
-- EndUserOperatorSessionsAtStation ends a user's active sessions at a station:
-- a new sign-in replaces them.
UPDATE identity.station_operator_sessions
SET ended_at = @now, end_reason = @end_reason, token_hash = NULL
WHERE station_id = @station_id AND user_id = @user_id AND ended_at IS NULL
RETURNING id;

-- name: CountActiveOperatorSessions :one
SELECT count(*)::integer FROM identity.station_operator_sessions WHERE station_id = @station_id AND ended_at IS NULL;

-- name: InsertOperatorSession :exec
INSERT INTO identity.station_operator_sessions (id, station_id, user_id, token_hash, started_at, last_activity_at)
VALUES (@id, @station_id, @user_id, @token_hash, @now, @now);

-- name: GetOperatorSessionByTokenHash :one
-- GetOperatorSessionByTokenHash resolves an operator token with the user's
-- state, which GetSessionByTokenHash's predicates judge in Go: an ended
-- session has no token hash, so only a live row is found.
SELECT s.id, s.station_id, s.user_id, s.started_at, s.last_activity_at,
       u.is_disabled, u.lockout_end, (u.pending_since IS NOT NULL)::boolean AS pending, u.kind,
       r.role_names, r.is_owner,
       m.upstream_active AS scim_upstream_active
FROM identity.station_operator_sessions s
JOIN identity.users u ON u.id = s.user_id
CROSS JOIN LATERAL (
    SELECT coalesce(array_agg(ro.name ORDER BY ro.name), '{}')::text[] AS role_names,
           coalesce(bool_or(ro.name = 'Owner'), false)::boolean AS is_owner
    FROM identity.user_roles ur
    JOIN identity.roles ro ON ro.id = ur.role_id
    WHERE ur.user_id = s.user_id
) r
LEFT JOIN identity.scim_user_mappings m ON m.user_id = s.user_id
WHERE s.token_hash = @token_hash AND s.ended_at IS NULL;

-- name: EndOperatorSession :execrows
UPDATE identity.station_operator_sessions
SET ended_at = @now, end_reason = @end_reason, token_hash = NULL
WHERE id = @id AND ended_at IS NULL;

-- name: EndActiveOperatorSessionAtStation :one
-- EndActiveOperatorSessionAtStation is a switch out: only an active session of
-- this station ends.
UPDATE identity.station_operator_sessions
SET ended_at = @now, end_reason = @end_reason, token_hash = NULL
WHERE id = @id AND station_id = @station_id AND ended_at IS NULL
RETURNING user_id;

-- name: EndStationOperatorSessions :exec
UPDATE identity.station_operator_sessions
SET ended_at = @now, end_reason = @end_reason, token_hash = NULL
WHERE station_id = @station_id AND ended_at IS NULL;

-- name: TouchOperatorSession :exec
-- TouchOperatorSession slides the idle window at most once a minute.
UPDATE identity.station_operator_sessions
SET last_activity_at = sqlc.arg(now)::timestamptz
WHERE id = @id AND ended_at IS NULL
  AND last_activity_at <= sqlc.arg(now)::timestamptz - interval '1 minute';

-- name: ListActiveOperatorSessions :many
-- ListActiveOperatorSessions is every live operator session, of one station
-- or (station_id null) of all, with the operator's name.
SELECT s.id, s.station_id, s.user_id, s.started_at, s.last_activity_at, u.display_name
FROM identity.station_operator_sessions s
JOIN identity.users u ON u.id = s.user_id
WHERE s.ended_at IS NULL AND (sqlc.narg(station_id)::uuid IS NULL OR s.station_id = sqlc.narg(station_id)::uuid)
ORDER BY s.started_at, s.id;

-- name: StationOperatorCandidates :many
-- StationOperatorCandidates is who may switch in at a station: the active
-- people holding permission_key (DirectoryPermissionHolders' rule) who have a
-- PIN or a badge.
SELECT u.id, u.display_name,
       (p.user_id IS NOT NULL)::boolean AS has_pin,
       (b.user_id IS NOT NULL)::boolean AS has_badge
FROM identity.users u
CROSS JOIN LATERAL (
    SELECT EXISTS (
        SELECT 1
        FROM identity.user_roles ur
        JOIN identity.roles r ON r.id = ur.role_id
        WHERE ur.user_id = u.id AND r.name = 'Owner'
    ) AS is_owner
) o
LEFT JOIN identity.scim_user_mappings m ON m.user_id = u.id
LEFT JOIN identity.user_pins p ON p.user_id = u.id
LEFT JOIN identity.user_badges b ON b.user_id = u.id
WHERE NOT u.is_disabled
  AND u.kind = 'person'
  AND u.pending_since IS NULL
  AND (p.user_id IS NOT NULL OR b.user_id IS NOT NULL)
  AND (NOT @scim_enabled::boolean OR m.upstream_active IS NOT FALSE OR o.is_owner)
  AND (o.is_owner OR EXISTS (
      SELECT 1
      FROM identity.role_permissions rp
      WHERE rp.permission_key = @permission_key
        AND rp.role_id IN (
            SELECT ur.role_id
            FROM identity.user_roles ur
            WHERE ur.user_id = u.id
            UNION
            SELECT gm_map.role_id
            FROM identity.access_group_memberships gm
            JOIN identity.access_groups g ON g.id = gm.group_id AND g.is_active
            JOIN identity.access_group_role_mappings gm_map ON gm_map.group_id = g.id AND gm_map.source = g.source
            WHERE gm.user_id = u.id
              AND (gm.membership_override = 'force_member'
                   OR (gm.membership_override IS NULL AND gm.is_upstream_present))
        )
  ))
ORDER BY u.display_name, u.id;

-- name: GetSessionCreatedAt :one
SELECT created_at FROM identity.sessions WHERE id = @id;

-- name: GetUserDisplayName :one
SELECT display_name FROM identity.users WHERE id = @id;
