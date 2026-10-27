-- The Outlook sync (calendar Outlook sync design O5, O7, O8): a person's
-- setting and the worker's record of it (calendar.mailbox_sync), and the map
-- of what the worker wrote to their mailbox (calendar.mailbox_events). The
-- person's PUT is the one write under If-Match: it runs under the row's lock
-- (LockMailboxSync) and moves revision on. The worker's writes and the
-- appointment writes' nudge leave revision alone, so a background write never
-- turns a person's open form into a 412.

-- name: GetMailboxSync :one
-- GetMailboxSync is the person's setting; no row is a person who never
-- turned it on.
SELECT * FROM calendar.mailbox_sync WHERE user_id = @user_id;

-- name: LockMailboxSync :one
-- LockMailboxSync takes the person's row's lock for the PUT, so the If-Match
-- revision is judged against the row the write then changes.
SELECT * FROM calendar.mailbox_sync WHERE user_id = @user_id FOR UPDATE;

-- name: UpsertMailboxSync :one
-- UpsertMailboxSync is the PUT's write: the person's toggle, target, detail
-- and kinds, the state the handler decided (pending when turned on, stopping
-- when a clean-up is owed), due at once. The first write makes the row at
-- revision 1; every later one moves revision on. The worker's record — the
-- mailbox, the calendar, the error, the last sync — is left as it was.
INSERT INTO calendar.mailbox_sync (user_id, enabled, target, detail, kinds, state, next_sync_at, created_at, updated_at)
VALUES (@user_id, @enabled, @target, @detail, @kinds::text[], @state, @now::timestamptz, @now::timestamptz, @now::timestamptz)
ON CONFLICT (user_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    target = EXCLUDED.target,
    detail = EXCLUDED.detail,
    kinds = EXCLUDED.kinds,
    state = EXCLUDED.state,
    next_sync_at = EXCLUDED.next_sync_at,
    revision = calendar.mailbox_sync.revision + 1,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: ClaimDueMailboxSyncs :many
-- ClaimDueMailboxSyncs is the worker's cycle (O7): the rows on, or owing a
-- clean-up, that are due at now — never synced first, then the longest due —
-- at most row_limit, locked so no other cycle takes the same one. Through
-- ix_mailbox_sync_due.
SELECT * FROM calendar.mailbox_sync
WHERE (enabled OR state = 'stopping')
  AND (next_sync_at IS NULL OR next_sync_at <= @now::timestamptz)
ORDER BY next_sync_at NULLS FIRST, user_id
LIMIT @row_limit::integer
FOR UPDATE SKIP LOCKED;

-- name: WriteMailboxSyncResult :one
-- WriteMailboxSyncResult records what a sync did: the state and the error
-- code (null after one that worked), when the next is due, the last sync
-- that worked (null keeps it), the failures in a row and whether the next
-- reads the mailbox's list first. It matches the revision the sync read: a
-- PUT that changed the setting while the sync was out has made it a sync of
-- a setting the row no longer holds, and it writes nothing — no row, which
-- the caller reads as overtaken.
UPDATE calendar.mailbox_sync SET
    state = @state,
    error = sqlc.narg(error)::varchar,
    next_sync_at = sqlc.narg(next_sync_at)::timestamptz,
    last_synced_at = COALESCE(sqlc.narg(last_synced_at)::timestamptz, last_synced_at),
    attempts = @attempts::integer,
    reconcile = @reconcile::boolean,
    updated_at = @now::timestamptz
WHERE user_id = @user_id AND revision = @revision::integer
RETURNING *;

-- name: SetMailboxSyncMailbox :exec
-- SetMailboxSyncMailbox records the mailbox the worker last confirmed and
-- the Vantigo calendar it made there (null for none) as soon as it learns
-- them, whatever revision the row holds: the clean-up must find what was
-- written even after a PUT overtook the sync that wrote it.
UPDATE calendar.mailbox_sync SET
    mailbox = sqlc.narg(mailbox)::varchar,
    calendar_id = sqlc.narg(calendar_id)::varchar,
    updated_at = @now::timestamptz
WHERE user_id = @user_id;

-- name: NudgeMailboxSync :exec
-- NudgeMailboxSync makes the people's syncs due at now, from inside an
-- appointment write's transaction (plan reading 9), so the change reaches
-- the mailbox within a minute. Only rows that are on, and not due already,
-- are touched.
UPDATE calendar.mailbox_sync SET next_sync_at = @now::timestamptz
WHERE user_id = ANY(@user_ids::uuid[])
  AND enabled
  AND (next_sync_at IS NULL OR next_sync_at > @now::timestamptz);

-- name: ListMailboxEvents :many
-- ListMailboxEvents is the person's map: every item the worker wrote to the
-- mailbox, by key.
SELECT * FROM calendar.mailbox_events
WHERE user_id = @user_id
ORDER BY item_key;

-- name: UpsertMailboxEvent :exec
-- UpsertMailboxEvent maps an item to the event written for it, or records
-- that the event was rewritten (a new id after a re-create, a new hash).
INSERT INTO calendar.mailbox_events (user_id, item_key, event_id, content_hash, starts_on, created_at, updated_at)
VALUES (@user_id, @item_key, @event_id, @content_hash, @starts_on, @now::timestamptz, @now::timestamptz)
ON CONFLICT (user_id, item_key) DO UPDATE SET
    event_id = EXCLUDED.event_id,
    content_hash = EXCLUDED.content_hash,
    starts_on = EXCLUDED.starts_on,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteMailboxEvent :exec
-- DeleteMailboxEvent forgets one item: its event was deleted, was gone, or
-- now starts before the window and stays in the mailbox unmapped.
DELETE FROM calendar.mailbox_events
WHERE user_id = @user_id AND item_key = @item_key;

-- name: DeleteMailboxEvents :execrows
-- DeleteMailboxEvents empties the person's map, after a clean-up or a
-- calendar that changed (O8).
DELETE FROM calendar.mailbox_events WHERE user_id = @user_id;
