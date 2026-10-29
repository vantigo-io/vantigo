-- The people's Outlook connections (Outlook connection design M3): one row
-- per Entra account by its object id, at most one live (not closing) row per
-- person. Every write that moves the state or the token moves revision on,
-- and the token's writes are guarded by the revision their caller read, so
-- a refresh never overwrites a newer connect and a refusal of an old token
-- never marks a newer one. None of them touches the connection's version.

-- name: GetOutlookConnection :one
-- GetOutlookConnection is the account's connection, in any state.
SELECT * FROM entra.outlook_connections WHERE object_id = @object_id;

-- name: GetLiveOutlookConnectionByUser :one
-- GetLiveOutlookConnectionByUser is the person's live connection: active or
-- in reconnect, never closing.
SELECT * FROM entra.outlook_connections WHERE user_id = @user_id AND state <> 'closing';

-- name: LockOutlookConnection :one
-- LockOutlookConnection is GetOutlookConnection under the row's lock, for
-- complete.
SELECT * FROM entra.outlook_connections WHERE object_id = @object_id FOR UPDATE;

-- name: LockLiveOutlookConnectionByUser :one
-- LockLiveOutlookConnectionByUser is GetLiveOutlookConnectionByUser under
-- the row's lock, for complete.
SELECT * FROM entra.outlook_connections WHERE user_id = @user_id AND state <> 'closing' FOR UPDATE;

-- name: InsertOutlookConnection :one
-- InsertOutlookConnection is a first connect of the account: active, at
-- revision 1, connected and refreshed now.
INSERT INTO entra.outlook_connections
    (object_id, user_id, tenant_id, scopes, refresh_sealed, state, connected_at, refreshed_at, changed_at, revision, created_at, updated_at)
VALUES
    (@object_id, @user_id, @tenant_id, @scopes::text[], @refresh_sealed, 'active', @now::timestamptz, @now::timestamptz, @now::timestamptz, 1, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: TakeOutlookConnection :one
-- TakeOutlookConnection is a connect over an existing row of the account —
-- the caller's own in any state, or anyone's closing one — at the revision
-- complete locked: the person's, active again, with the new token and
-- scopes.
UPDATE entra.outlook_connections SET
    user_id = @user_id,
    tenant_id = @tenant_id,
    scopes = @scopes::text[],
    refresh_sealed = @refresh_sealed,
    state = 'active',
    error_code = NULL,
    connected_at = @now::timestamptz,
    refreshed_at = @now::timestamptz,
    changed_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE object_id = @object_id AND revision = @revision
RETURNING *;

-- name: CloseOutlookConnection :one
-- CloseOutlookConnection closes a live connection: a disconnect keeps the
-- token for the removals that may still need it (M7); a leaver and a
-- deleted account forget it. With a revision, only the row still at the
-- revision its caller read closes, so a close decided on an old read never
-- closes a newer connect or forgets its token. No row is a connection
-- already closing, or moved on since, whose token ForgetOutlookRefresh
-- forgets when it must.
UPDATE entra.outlook_connections SET
    state = 'closing',
    refresh_sealed = CASE WHEN @forget::boolean THEN NULL ELSE refresh_sealed END,
    changed_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE object_id = @object_id AND state <> 'closing'
  AND (sqlc.narg(revision)::bigint IS NULL OR revision = sqlc.narg(revision))
RETURNING user_id, revision;

-- name: ForgetOutlookRefresh :exec
-- ForgetOutlookRefresh forgets the account's token, whatever the state, so
-- a leaver's close of a row already closing still drops it.
UPDATE entra.outlook_connections SET
    refresh_sealed = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE object_id = @object_id AND refresh_sealed IS NOT NULL;

-- name: ForgetClosingOutlookRefresh :execrows
-- ForgetClosingOutlookRefresh forgets a closing connection's token Microsoft
-- refused, at the revision the refresh read: no removal can use it again.
-- No row means a newer connect or a forget won.
UPDATE entra.outlook_connections SET
    refresh_sealed = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE object_id = @object_id AND revision = @revision AND state = 'closing' AND refresh_sealed IS NOT NULL;

-- name: CloseAllOutlookConnections :many
-- CloseAllOutlookConnections closes every live connection, keeping the
-- tokens: the switch turned off.
UPDATE entra.outlook_connections SET
    state = 'closing',
    changed_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE state <> 'closing'
RETURNING object_id, user_id;

-- name: StoreOutlookRefresh :execrows
-- StoreOutlookRefresh records a refresh's new token and scopes at the
-- revision the refresh read, while the token is still held. No row is the
-- lost race (another refresh, a connect, a close since), never an error.
UPDATE entra.outlook_connections SET
    refresh_sealed = @refresh_sealed,
    scopes = @scopes::text[],
    refreshed_at = @now::timestamptz,
    used_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE object_id = @object_id AND revision = @revision AND refresh_sealed IS NOT NULL;

-- name: MarkOutlookReconnect :execrows
-- MarkOutlookReconnect records Microsoft's refusal of the token the caller
-- read, with its code, while the row is still active at that revision. No
-- row means a newer connect or another failure won.
UPDATE entra.outlook_connections SET
    state = 'reconnect',
    error_code = @code,
    changed_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE object_id = @object_id AND revision = @revision AND state = 'active';

-- name: TouchOutlookUsed :exec
-- TouchOutlookUsed records that the token was used, at most once an hour.
-- It leaves revision alone.
UPDATE entra.outlook_connections SET used_at = @now::timestamptz
WHERE object_id = @object_id
  AND (used_at IS NULL OR used_at + interval '1 hour' < @now::timestamptz);

-- name: CountOutlookConnections :one
-- CountOutlookConnections counts the connections working and those waiting
-- for their person to reconnect, for outlookConnect's counts.
SELECT
    count(*) FILTER (WHERE state = 'active') AS connected,
    count(*) FILTER (WHERE state = 'reconnect') AS reconnect
FROM entra.outlook_connections;

-- name: OutlookConnectedSince :one
-- OutlookConnectedSince answers whether a live connection was made at or
-- after since — the switch's last turning on — which makes the status ready.
SELECT EXISTS (
    SELECT 1 FROM entra.outlook_connections
    WHERE state <> 'closing' AND connected_at >= @since::timestamptz
);

-- name: DeleteSpentOutlookConnections :many
-- DeleteSpentOutlookConnections is entra-outlook-sweep's one statement (M7):
-- a closing connection goes seven days after it closed, or once a day has
-- passed since it was last used and since it closed, and no subscription
-- of the mailbox is left for its removals to delete.
DELETE FROM entra.outlook_connections c
WHERE c.state = 'closing'
  AND (c.changed_at + interval '7 days' < @now::timestamptz
       OR (GREATEST(COALESCE(c.used_at, c.changed_at), c.changed_at) + interval '24 hours' < @now::timestamptz
           AND NOT EXISTS (SELECT 1 FROM entra.mailbox_subscriptions s
                           WHERE s.object_id = c.object_id AND s.resource = 'events')))
RETURNING object_id, user_id;

-- name: ListOutlookAccountChecks :many
-- ListOutlookAccountChecks is the connections waiting in reconnect whose
-- account entra-outlook-sweep has not read in the tenant for a day (design
-- M6): a person who never reconnects is still found once their account is
-- disabled or deleted. Oldest check first, at most row_limit, each with the
-- revision a close it decides is guarded by.
SELECT object_id, revision FROM entra.outlook_connections
WHERE state = 'reconnect'
  AND (account_checked_at IS NULL OR account_checked_at + interval '1 day' < @now::timestamptz)
ORDER BY account_checked_at NULLS FIRST, object_id
LIMIT @row_limit;

-- name: MarkOutlookAccountChecked :exec
-- MarkOutlookAccountChecked records that the account was read in the tenant
-- and found enabled. Bookkeeping like used_at: it leaves revision alone.
UPDATE entra.outlook_connections SET account_checked_at = @now::timestamptz
WHERE object_id = @object_id;

-- name: ListOutlookConnectionChanges :many
-- ListOutlookConnectionChanges is what the mailbox calendar's Connections
-- answers (design M9): the connections whose state moved after since,
-- oldest first, at most row_limit. Through ix_entra_outlook_connections_changed.
-- A refresh, a forgotten token and the bookkeeping move no changed_at.
SELECT object_id, state, changed_at FROM entra.outlook_connections
WHERE changed_at > @since::timestamptz
ORDER BY changed_at, object_id
LIMIT @row_limit::integer;
