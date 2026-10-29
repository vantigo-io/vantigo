-- The communications-mailbox-sync worker's channel_sync (Outlook connection
-- design M14; delivery D, readings 13 and 22): the claim, the lease and every
-- write of a cycle, and the row the channel operations write and read. Every
-- write of a cycle is guarded by the lease (channel_id AND lease_id), so a
-- worker that lost its lease writes nothing: 0 rows is the signal.

-- name: SelectDueChannelSync :one
-- The candidate: a row of an active Microsoft 365 channel, not stopped, due,
-- and not leased (or its lease expired). A channel E's notice marked comes
-- first, then the longest due.
SELECT s.channel_id
FROM communications.channel_sync s
JOIN communications.channels c ON c.id = s.channel_id
WHERE c.provider = 'entra' AND c.is_active
  AND s.state <> 'stopped'
  AND s.next_sync_at <= @now
  AND (s.lease_until IS NULL OR s.lease_until < @now)
ORDER BY s.notified DESC, s.next_sync_at ASC, s.channel_id ASC
LIMIT 1;

-- name: ClaimChannelSync :execrows
-- The claim: the candidate's predicate asserted again by the update itself,
-- so a row another worker took in between reports 0 rows — the conditional
-- update is the lock, as the outbox's (ClaimOutboxJob).
UPDATE communications.channel_sync s
SET lease_id = @lease_id, lease_until = @lease_until, updated_at = @now
FROM communications.channels c
WHERE s.channel_id = @channel_id
  AND c.id = s.channel_id AND c.provider = 'entra' AND c.is_active
  AND s.state <> 'stopped'
  AND s.next_sync_at <= @now
  AND (s.lease_until IS NULL OR s.lease_until < @now);

-- name: GetChannelSyncForCycle :one
-- The claimed row with what the cycle reads of its channel: the address (the
-- mailbox's own, left out of the participants) and the settings naming the
-- mailbox.
SELECT s.channel_id, s.folder, s.delta_link, s.import_from, s.state, s.attempts, s.lease_id,
       c.address AS channel_address, c.settings AS channel_settings
FROM communications.channel_sync s
JOIN communications.channels c ON c.id = s.channel_id
WHERE s.channel_id = @channel_id;

-- name: RenewChannelSyncLease :execrows
-- Before each message: the lease another two minutes from now.
UPDATE communications.channel_sync
SET lease_until = @lease_until, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: StoreChannelSyncPage :execrows
-- After a page whose every message has a receipt: its link kept and the lease
-- given back. With done, the delta link — the channel is active, its failure
-- forgotten, synced now and due again at next_sync_at; without, the next
-- page's link and next_sync_at now, so the drain claims it again at once.
UPDATE communications.channel_sync
SET delta_link = @delta_link,
    state = CASE WHEN sqlc.arg(done)::boolean THEN 'active' ELSE state END,
    error = CASE WHEN sqlc.arg(done)::boolean THEN NULL ELSE error END,
    attempts = CASE WHEN sqlc.arg(done)::boolean THEN 0 ELSE attempts END,
    last_synced_at = CASE WHEN sqlc.arg(done)::boolean THEN sqlc.arg(now)::timestamptz ELSE last_synced_at END,
    next_sync_at = @next_sync_at,
    lease_id = NULL, lease_until = NULL,
    updated_at = sqlc.arg(now)::timestamptz
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: HoldChannelSync :execrows
-- The provider unavailable (C's outbox hold): tried again at next_sync_at,
-- no attempt spent, the state kept.
UPDATE communications.channel_sync
SET error = 'provider_unavailable', next_sync_at = @next_sync_at,
    lease_id = NULL, lease_until = NULL, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: FailChannelSync :execrows
-- A failed cycle: an attempt spent, the code kept, tried again at
-- next_sync_at; state is 'failed' for a refusal no retry mends soon, else
-- the row's own.
UPDATE communications.channel_sync
SET state = COALESCE(sqlc.narg(state), state),
    error = @error,
    attempts = attempts + 1,
    next_sync_at = @next_sync_at,
    lease_id = NULL, lease_until = NULL, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: ResetChannelSyncDelta :execrows
-- The provider no longer takes the stored link: forgotten, so a cycle that
-- ends before its fresh read stores one starts afresh too.
UPDATE communications.channel_sync
SET delta_link = NULL, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: InsertChannelSync :one
-- A Microsoft 365 channel's row, written in the transaction that creates or
-- switches it: it receives from now, never its past. A row left from an
-- earlier switch is started afresh.
INSERT INTO communications.channel_sync (channel_id, folder, import_from, state, next_sync_at, updated_at)
VALUES (@channel_id, 'inbox', @now, 'pending', @now, @now)
ON CONFLICT (channel_id) DO UPDATE
SET folder = 'inbox', delta_link = NULL, import_from = excluded.import_from, state = 'pending',
    error = NULL, next_sync_at = excluded.next_sync_at, last_synced_at = NULL, attempts = 0,
    lease_id = NULL, lease_until = NULL, notified = false, updated_at = excluded.updated_at
RETURNING channel_id, folder, delta_link, import_from, state, error, next_sync_at, last_synced_at,
          attempts, lease_id, lease_until, notified, updated_at;

-- name: DeleteChannelSync :exec
-- The way back to SMTP: the channel no longer receives, and a later switch
-- starts from its own day.
DELETE FROM communications.channel_sync WHERE channel_id = @channel_id;

-- name: GetChannelSyncByChannelIDs :many
-- ChannelResponse.sync for each channel that has a row.
SELECT channel_id, state, error, last_synced_at
FROM communications.channel_sync
WHERE channel_id = ANY(@channel_ids::uuid[]);
