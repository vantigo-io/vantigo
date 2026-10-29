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
-- when a clean-up is owed), due at once, and whether the next sync reads the
-- mailbox's own list first (set when it is turned on or a clean-up is owed).
-- The first write makes the row at revision 1; every later one moves
-- revision on. The worker's record — the mailbox, the calendar, the error,
-- the last sync — is left as it was.
INSERT INTO calendar.mailbox_sync (user_id, enabled, target, detail, kinds, state, next_sync_at, reconcile, created_at, updated_at)
VALUES (@user_id, @enabled, @target, @detail, @kinds::text[], @state, @now::timestamptz, @reconcile::boolean, @now::timestamptz, @now::timestamptz)
ON CONFLICT (user_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    target = EXCLUDED.target,
    detail = EXCLUDED.detail,
    kinds = EXCLUDED.kinds,
    state = EXCLUDED.state,
    next_sync_at = EXCLUDED.next_sync_at,
    reconcile = EXCLUDED.reconcile,
    revision = calendar.mailbox_sync.revision + 1,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: ClaimDueMailboxSync :one
-- ClaimDueMailboxSync is the worker's claim (O7): the row on, or owing a
-- clean-up, that is due at now and not claimed — never synced first, then
-- the longest due — and not one of done, the people the cycle has synced
-- already, claimed until claimed_until, so no other cycle takes it while it
-- syncs. Locked SKIP LOCKED, so two cycles claiming at once take two rows.
-- No row is nothing left due. Through ix_mailbox_sync_due.
UPDATE calendar.mailbox_sync SET claimed_until = @claimed_until::timestamptz
WHERE user_id = (
    SELECT c.user_id FROM calendar.mailbox_sync c
    WHERE (c.enabled OR c.state = 'stopping')
      AND (c.next_sync_at IS NULL OR c.next_sync_at <= @now::timestamptz)
      AND (c.claimed_until IS NULL OR c.claimed_until <= @now::timestamptz)
      AND NOT (c.user_id = ANY(COALESCE(@done::uuid[], '{}')))
    ORDER BY c.next_sync_at NULLS FIRST, c.user_id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: ReleaseMailboxSyncClaim :exec
-- ReleaseMailboxSyncClaim gives back the claim a sync took, once it ended,
-- whatever it wrote: only that claim — one that lapsed and was taken again
-- is the other cycle's.
UPDATE calendar.mailbox_sync SET claimed_until = NULL
WHERE user_id = @user_id AND claimed_until = @claimed_until::timestamptz;

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
-- that the event was rewritten (a new id after a re-create, a new hash), with
-- the event's version the write answered (O10.3): null keeps the one stored,
-- for a provider that answers none.
INSERT INTO calendar.mailbox_events (user_id, item_key, event_id, content_hash, starts_on, change_key, created_at, updated_at)
VALUES (@user_id, @item_key, @event_id, @content_hash, @starts_on, sqlc.narg(change_key)::varchar, @now::timestamptz, @now::timestamptz)
ON CONFLICT (user_id, item_key) DO UPDATE SET
    event_id = EXCLUDED.event_id,
    content_hash = EXCLUDED.content_hash,
    starts_on = EXCLUDED.starts_on,
    change_key = COALESCE(EXCLUDED.change_key, calendar.mailbox_events.change_key),
    updated_at = EXCLUDED.updated_at;

-- name: SetMailboxEventChangeKey :exec
-- SetMailboxEventChangeKey keeps the version a look at the event answered
-- (O10.4): the baseline of a row that had none, or Exchange's own touch,
-- which changed nothing the copy says. The hash is left as it is.
UPDATE calendar.mailbox_events SET
    change_key = @change_key::varchar,
    updated_at = @now::timestamptz
WHERE user_id = @user_id AND item_key = @item_key;

-- name: StaleMailboxEvent :exec
-- StaleMailboxEvent clears the row's hash, so the push rewrites the copy
-- whole (O10.6): a change in Outlook put back, or an appointment written
-- from it. The version read is kept when given (null keeps the stored one),
-- so the copy is not read again before the push.
UPDATE calendar.mailbox_events SET
    content_hash = '',
    change_key = COALESCE(sqlc.narg(change_key)::varchar, change_key),
    updated_at = @now::timestamptz
WHERE user_id = @user_id AND item_key = @item_key;

-- name: ReleaseMailboxEvent :exec
-- ReleaseMailboxEvent releases the copy the person turned into a meeting in
-- Outlook (O10.8): never written, deleted or taken back again, and forgotten
-- without a delete by the push and the clean-up.
UPDATE calendar.mailbox_events SET
    released_at = @now::timestamptz,
    updated_at = @now::timestamptz
WHERE user_id = @user_id AND item_key = @item_key;

-- name: DeleteMailboxEvent :exec
-- DeleteMailboxEvent forgets one item: its event was deleted, was gone, or
-- now starts before the window and stays in the mailbox unmapped.
DELETE FROM calendar.mailbox_events
WHERE user_id = @user_id AND item_key = @item_key;

-- name: DeleteMailboxEvents :execrows
-- DeleteMailboxEvents empties the person's map, after a clean-up or a
-- calendar that changed (O8).
DELETE FROM calendar.mailbox_events WHERE user_id = @user_id;

-- name: SetMailboxPutBack :exec
-- SetMailboxPutBack records the last change made in Outlook that the
-- write-back did not take (O10.3): the reason's code, the copy's item key
-- and when, whatever revision the row holds — nothing of what the change
-- said.
UPDATE calendar.mailbox_sync SET
    put_back_reason = @reason::varchar,
    put_back_key = @key::varchar,
    put_back_at = @at::timestamptz,
    updated_at = @at::timestamptz
WHERE user_id = @user_id;

-- name: ClearMailboxPutBack :exec
-- ClearMailboxPutBack forgets the put-back, after a clean-up or the sync
-- turned off.
UPDATE calendar.mailbox_sync SET
    put_back_reason = NULL,
    put_back_key = NULL,
    put_back_at = NULL
WHERE user_id = @user_id;

-- name: MarkMailboxSyncNotified :execrows
-- MarkMailboxSyncNotified makes due at now the syncs of the mailboxes a
-- cycle's change notices named, each at its at (calendar Outlook sync
-- design O11.6), in one statement for the whole cycle — the two arrays,
-- of equal length, unnested side by side and, per mailbox compared case-insensitively, the
-- latest at kept: the rows of that mailbox that are on and active or
-- pending, not backing off a failure (attempts 0: a notice never overrides
-- the backoff), whose last sync started before at, and which are not due
-- already. A notice asked for twice marks nothing the second time once the
-- row has synced since. Through the index on lower(mailbox) of the rows on.
UPDATE calendar.mailbox_sync s SET next_sync_at = @now::timestamptz
FROM (
    SELECT lower(n.mailbox) AS mailbox, max(n.at) AS at
    FROM (SELECT unnest(@mailboxes::text[]) AS mailbox, unnest(@ats::timestamptz[]) AS at) AS n
    GROUP BY lower(n.mailbox)
) AS notice
WHERE lower(s.mailbox) = notice.mailbox
  AND s.enabled
  AND s.state IN ('active', 'pending')
  AND s.attempts = 0
  AND (s.last_synced_at IS NULL OR s.last_synced_at < notice.at)
  AND (s.next_sync_at IS NULL OR s.next_sync_at > @now::timestamptz);

-- name: MarkMailboxSyncConnected :execrows
-- MarkMailboxSyncConnected makes due at now the syncs of the mailboxes
-- whose connection to the provider moved (Outlook connection design M9),
-- each at its at, in one statement for the cycle's changes — the two arrays
-- unnested side by side and, per mailbox compared case-insensitively, the
-- latest at kept: the rows of that mailbox that are on or owe a clean-up,
-- last written before at, and not due already. Unlike a notice, a change
-- overrides the backoff and the parking: it changes what the next sync can
-- do. A change taken twice marks nothing once the row was written since.
UPDATE calendar.mailbox_sync s SET next_sync_at = @now::timestamptz
FROM (
    SELECT lower(c.mailbox) AS mailbox, max(c.at) AS at
    FROM (SELECT unnest(@mailboxes::text[]) AS mailbox, unnest(@ats::timestamptz[]) AS at) AS c
    GROUP BY lower(c.mailbox)
) AS change
WHERE lower(s.mailbox) = change.mailbox
  AND (s.enabled OR s.state = 'stopping')
  AND s.updated_at < change.at
  AND (s.next_sync_at IS NULL OR s.next_sync_at > @now::timestamptz);
