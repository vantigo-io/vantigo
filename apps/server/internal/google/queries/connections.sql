-- The person's own Google connection (Google Workspace design G25–G28):
-- google.connections, one row per Google account by its Directory id, one live
-- row per person. Every write that moves a row bumps revision; the ones a
-- caller decided on an earlier read are guarded by the revision it read.

-- name: GetPersonal :one
-- GetPersonal is the account's connection, in any state.
SELECT * FROM google.connections WHERE google_user_id = @google_user_id;

-- name: GetLivePersonalByUser :one
-- GetLivePersonalByUser is the person's live connection: active or in
-- reconnect, never closing.
SELECT * FROM google.connections WHERE user_id = @user_id AND state <> 'closing';

-- name: LockPersonal :one
-- LockPersonal is GetPersonal under the row's lock, for complete.
SELECT * FROM google.connections WHERE google_user_id = @google_user_id FOR UPDATE;

-- name: LockLivePersonalByUser :one
-- LockLivePersonalByUser is GetLivePersonalByUser under the row's lock.
SELECT * FROM google.connections WHERE user_id = @user_id AND state <> 'closing' FOR UPDATE;

-- name: InsertPersonal :one
-- InsertPersonal is a first connect of the account: active, at revision 1,
-- connected and refreshed now.
INSERT INTO google.connections
    (google_user_id, user_id, scopes, refresh_sealed, state, connected_at, refreshed_at, changed_at, revision, created_at, updated_at)
VALUES
    (@google_user_id, @user_id, @scopes::text[], @refresh_sealed, 'active', @now::timestamptz, @now::timestamptz, @now::timestamptz, 1, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: TakePersonal :one
-- TakePersonal is a connect over an existing row of the account — the
-- caller's own in any state, or anyone's closing one — at the revision
-- complete locked: the person's, active again, with the new token and scopes.
UPDATE google.connections SET
    user_id = @user_id,
    scopes = @scopes::text[],
    refresh_sealed = @refresh_sealed,
    state = 'active',
    error_code = NULL,
    connected_at = @now::timestamptz,
    refreshed_at = @now::timestamptz,
    account_checked_at = NULL,
    changed_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE google_user_id = @google_user_id AND revision = @revision
RETURNING *;

-- name: ClosePersonal :one
-- ClosePersonal closes a live connection: a disconnect and the switch keep
-- the token for the removals that may still need it; a leaver, a deleted or
-- suspended account forget it. With a revision, only the row still at the
-- revision its caller read closes. No row is a connection already closing,
-- or moved on since.
UPDATE google.connections SET
    state = 'closing',
    refresh_sealed = CASE WHEN @forget::boolean THEN NULL ELSE refresh_sealed END,
    changed_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE google_user_id = @google_user_id AND state <> 'closing'
  AND (sqlc.narg('revision')::bigint IS NULL OR revision = sqlc.narg('revision')::bigint)
RETURNING *;

-- name: ForgetPersonalRefresh :exec
-- ForgetPersonalRefresh forgets the account's token, whatever the state, so
-- a leaver's close of a row already closing still drops it.
UPDATE google.connections SET
    refresh_sealed = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE google_user_id = @google_user_id AND refresh_sealed IS NOT NULL;

-- name: ForgetClosingPersonalRefresh :execrows
-- ForgetClosingPersonalRefresh forgets a closing connection's token Google
-- refused, at the revision the refresh read. No row means a newer connect or
-- a forget won.
UPDATE google.connections SET
    refresh_sealed = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE google_user_id = @google_user_id AND revision = @revision AND state = 'closing' AND refresh_sealed IS NOT NULL;

-- name: CloseAllPersonal :many
-- CloseAllPersonal closes every live connection, keeping the tokens: the
-- switch turned off.
UPDATE google.connections SET
    state = 'closing',
    changed_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE state <> 'closing'
RETURNING google_user_id, user_id;

-- name: StorePersonalRefresh :execrows
-- StorePersonalRefresh records a refresh's answer at the revision the
-- refresh read, while the token is still held: the new token when Google
-- answered one (else the stored one stays) and the answered scopes. No row
-- is the lost race, never an error.
UPDATE google.connections SET
    refresh_sealed = COALESCE(sqlc.narg('refresh_sealed')::bytea, refresh_sealed),
    scopes = @scopes::text[],
    refreshed_at = @now::timestamptz,
    used_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE google_user_id = @google_user_id AND revision = @revision AND refresh_sealed IS NOT NULL;

-- name: MarkPersonalReconnect :execrows
-- MarkPersonalReconnect records Google's refusal of the token the caller
-- read, with its code, while the row is still active at that revision. No
-- row means a newer connect or another failure won.
UPDATE google.connections SET
    state = 'reconnect',
    error_code = @code,
    changed_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE google_user_id = @google_user_id AND revision = @revision AND state = 'active';

-- name: TouchPersonalUsed :exec
-- TouchPersonalUsed records that the token was used, at most once an hour.
-- It leaves revision alone.
UPDATE google.connections SET used_at = @now::timestamptz
WHERE google_user_id = @google_user_id
  AND (used_at IS NULL OR used_at + interval '1 hour' < @now::timestamptz);

-- name: CountPersonal :one
-- CountPersonal counts the connections working and those waiting for their
-- person to reconnect, for the switch's counts.
SELECT
    count(*) FILTER (WHERE state = 'active') AS connected,
    count(*) FILTER (WHERE state = 'reconnect') AS reconnect
FROM google.connections;

-- name: DeleteSpentPersonal :many
-- DeleteSpentPersonal is google-sweep's first step (G28): a closing
-- connection goes seven days after it closed, or once a day has passed since
-- it was last used and since it closed. The statement is the claim: of two
-- replicas, one deletes each row and revokes its token.
DELETE FROM google.connections c
WHERE c.state = 'closing'
  AND (c.changed_at + interval '7 days' < @now::timestamptz
       OR GREATEST(COALESCE(c.used_at, c.changed_at), c.changed_at) + interval '24 hours' < @now::timestamptz)
RETURNING google_user_id, user_id, refresh_sealed;

-- name: ClaimPersonalAccountChecks :many
-- ClaimPersonalAccountChecks is google-sweep's second step (G28): the
-- connections waiting in reconnect whose account it has not read for a day,
-- oldest check first, at most row_limit, claimed by stamping
-- account_checked_at in the same statement — another replica skips them —
-- each with the revision a close it decides is guarded by. Bookkeeping like
-- used_at: it leaves revision alone.
UPDATE google.connections c SET account_checked_at = @now::timestamptz
FROM (
    SELECT google_user_id FROM google.connections
    WHERE state = 'reconnect'
      AND (account_checked_at IS NULL OR account_checked_at + interval '1 day' < @now::timestamptz)
    ORDER BY account_checked_at NULLS FIRST, google_user_id
    LIMIT @row_limit
    FOR UPDATE SKIP LOCKED
) due
WHERE c.google_user_id = due.google_user_id
RETURNING c.google_user_id, c.revision;

-- name: ListPersonalChanges :many
-- ListPersonalChanges is what the mailbox calendar's Connections will answer
-- (delivery E): the connections whose state moved after since, oldest first,
-- at most row_limit, through ix_google_connections_changed. A refresh, a
-- forgotten token and the bookkeeping move no changed_at.
SELECT google_user_id, state, changed_at FROM google.connections
WHERE changed_at > @since::timestamptz
ORDER BY changed_at, google_user_id
LIMIT @row_limit::integer;

-- name: SetPersonalSwitch :one
-- SetPersonalSwitch sets Connection for people; turning it on stamps
-- personal_connection_at, the instant the status's ready is judged from. It
-- leaves version alone, as the If-Match'd settings are not written.
UPDATE google.connection SET
    personal_connection = @enabled,
    personal_connection_at = CASE WHEN @enabled::boolean AND NOT personal_connection THEN @now::timestamptz ELSE personal_connection_at END
WHERE id = 1
RETURNING *;

-- name: SetPersonalLastError :exec
-- SetPersonalLastError records a failure of the web client itself an Owner
-- must act on (consent_missing, invalid_client, unauthorized_client), which
-- the switch's status shows until a connect works.
UPDATE google.connection SET
    personal_last_error = @code,
    personal_last_error_at = @now::timestamptz
WHERE id = 1;

-- name: ClearPersonalLastError :exec
-- ClearPersonalLastError forgets it once a connect works, or the web client
-- changes.
UPDATE google.connection SET personal_last_error = NULL, personal_last_error_at = NULL
WHERE id = 1 AND personal_last_error IS NOT NULL;

-- name: LockAccountShare :one
-- LockAccountShare is the account under a share lock, for complete: a
-- leaver's Deactivate, which writes the account before it closes the
-- connection, runs wholly before or after the connection is stored.
SELECT * FROM google.accounts WHERE user_id = @user_id FOR SHARE;
