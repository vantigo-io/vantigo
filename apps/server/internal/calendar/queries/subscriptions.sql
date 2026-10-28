-- Subscriptions (calendar feed design D6, D7, D8): the calendars a person
-- subscribes to from elsewhere, at most ten each, every operation on the
-- caller's own rows — another person's is the no row an unknown id is. The
-- worker's record of the last fetch sits on the row: the validators, the body,
-- fetched_at when one last worked, attempted_at when one was last tried, the
-- error of a failed one, and next_fetch_at.

-- name: ListSubscriptions :many
-- ListSubscriptions is the person's subscriptions, by name and then by id.
SELECT * FROM calendar.subscriptions
WHERE user_id = @user_id
ORDER BY name, id;

-- name: LockSubscriptionsOfUser :exec
-- LockSubscriptionsOfUser takes the person's subscriptions lock for the rest
-- of the transaction, so a count and the insert it allows are one step: two
-- POSTs at nine cannot both see nine and both insert. The key is the
-- person's id under this table's own prefix, so it shares no key with
-- another lock keyed on a user.
SELECT pg_advisory_xact_lock(hashtextextended('calendar.subscriptions:' || sqlc.arg(user_id)::uuid::text, 0));

-- name: CountSubscriptions :one
-- CountSubscriptions is how many subscriptions the person has, for the limit
-- of ten.
SELECT count(*) FROM calendar.subscriptions
WHERE user_id = @user_id;

-- name: GetSubscription :one
-- GetSubscription is one of the person's subscriptions.
SELECT * FROM calendar.subscriptions
WHERE id = @id AND user_id = @user_id;

-- name: InsertSubscription :one
-- InsertSubscription adds a subscription with no fetch recorded: next_fetch_at
-- null, due at once.
INSERT INTO calendar.subscriptions (user_id, name, url, colour, busy_only, created_at, updated_at)
VALUES (@user_id, @name, @url, @colour, @busy_only, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateSubscription :one
-- UpdateSubscription replaces what the person wrote — last write wins, with
-- no revision — and leaves the fetch's record; a new url is the caller's to
-- clear with ResetSubscriptionFetch.
UPDATE calendar.subscriptions SET
    name = @name,
    url = @url,
    colour = @colour,
    busy_only = @busy_only,
    updated_at = @now::timestamptz
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: ResetSubscriptionFetch :one
-- ResetSubscriptionFetch forgets every fetch of the subscription, for a new
-- url: the validators and the body were the old address's, and it is due at
-- once.
UPDATE calendar.subscriptions SET
    etag = NULL,
    last_modified = NULL,
    body = NULL,
    fetched_at = NULL,
    attempted_at = NULL,
    next_fetch_at = NULL,
    error = NULL
WHERE id = @id
RETURNING *;

-- The three records of a fetch each match the url the fetch was made to as
-- well as the id: a PUT that changed the address while the fetch was out
-- has made it a fetch of an address the row no longer names, and it writes
-- nothing — no row, which the caller reads as overtaken.

-- name: RecordSubscriptionFetched :one
-- RecordSubscriptionFetched records a fetch that answered a calendar: its
-- validators (null when it sent none), its body, and when the next is due,
-- with the error of an earlier failure cleared.
UPDATE calendar.subscriptions SET
    etag = sqlc.narg(etag),
    last_modified = sqlc.narg(last_modified),
    body = @body::text,
    fetched_at = @now::timestamptz,
    attempted_at = @now::timestamptz,
    next_fetch_at = @next_fetch_at::timestamptz,
    error = NULL
WHERE id = @id AND url = @url
RETURNING *;

-- name: RecordSubscriptionNotModified :one
-- RecordSubscriptionNotModified records a fetch answered 304: the body and
-- the validators stand, it worked now, and the next is due at next_fetch_at.
UPDATE calendar.subscriptions SET
    fetched_at = @now::timestamptz,
    attempted_at = @now::timestamptz,
    next_fetch_at = @next_fetch_at::timestamptz,
    error = NULL
WHERE id = @id AND url = @url
RETURNING *;

-- name: RecordSubscriptionFailed :one
-- RecordSubscriptionFailed records a fetch that failed and why. The body, the
-- validators and fetched_at are untouched, so the calendar keeps showing the
-- last good read and the screen says when it last worked.
UPDATE calendar.subscriptions SET
    attempted_at = @now::timestamptz,
    next_fetch_at = @next_fetch_at::timestamptz,
    error = @error::varchar
WHERE id = @id AND url = @url
RETURNING *;

-- name: GetSubscriptionByID :one
-- GetSubscriptionByID is a subscription by its id alone: the row as it now
-- stands, for a fetch that was overtaken to answer, its owner already
-- checked by the caller.
SELECT * FROM calendar.subscriptions
WHERE id = @id;

-- name: ClaimDueSubscription :one
-- ClaimDueSubscription is the worker's claim: the subscription due at now —
-- never fetched first, then the longest due — put off until claimed_until,
-- so no other cycle fetches it while this one does; the fetch's record then
-- sets when it is due next, and a fetch cut short leaves it due once the
-- claim lapses. Locked SKIP LOCKED, so two cycles claiming at once take two
-- rows; no row is nothing left due. It carries what a fetch sends and not
-- the body.
UPDATE calendar.subscriptions SET next_fetch_at = @claimed_until::timestamptz
WHERE id = (
    SELECT c.id FROM calendar.subscriptions c
    WHERE c.next_fetch_at IS NULL OR c.next_fetch_at <= @now::timestamptz
    ORDER BY c.next_fetch_at NULLS FIRST, c.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, user_id, url, etag, last_modified;

-- name: DeferSubscriptionFetch :exec
-- DeferSubscriptionFetch puts a subscription off until next_fetch_at without
-- fetching it: its person may no longer use Calendar, so it is not fetched,
-- and not read again every tick either. Nothing of the last fetch's record
-- changes, since nothing was attempted.
UPDATE calendar.subscriptions SET next_fetch_at = @next_fetch_at::timestamptz
WHERE id = @id;

-- name: ListSubscriptionBodies :many
-- ListSubscriptionBodies is what GET /items parses: each of the person's
-- subscriptions with a calendar fetched, by name and then by id. One never
-- fetched, or whose new address has not answered yet, contributes nothing.
SELECT id, name, colour, busy_only, body::text AS body
FROM calendar.subscriptions
WHERE user_id = @user_id AND body IS NOT NULL
ORDER BY name, id;

-- name: DeleteSubscription :execrows
-- DeleteSubscription removes one of the person's subscriptions; its items are
-- gone from the next read. No row deleted is an id that is not theirs.
DELETE FROM calendar.subscriptions
WHERE id = @id AND user_id = @user_id;
