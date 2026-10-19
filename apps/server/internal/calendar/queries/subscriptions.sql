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
WHERE id = @id
RETURNING *;

-- name: RecordSubscriptionNotModified :one
-- RecordSubscriptionNotModified records a fetch answered 304: the body and
-- the validators stand, it worked now, and the next is due at next_fetch_at.
UPDATE calendar.subscriptions SET
    fetched_at = @now::timestamptz,
    attempted_at = @now::timestamptz,
    next_fetch_at = @next_fetch_at::timestamptz,
    error = NULL
WHERE id = @id
RETURNING *;

-- name: RecordSubscriptionFailed :one
-- RecordSubscriptionFailed records a fetch that failed and why. The body, the
-- validators and fetched_at are untouched, so the calendar keeps showing the
-- last good read and the screen says when it last worked.
UPDATE calendar.subscriptions SET
    attempted_at = @now::timestamptz,
    next_fetch_at = @next_fetch_at::timestamptz,
    error = @error::varchar
WHERE id = @id
RETURNING *;

-- name: ListSubscriptionsDue :many
-- ListSubscriptionsDue is the worker's cycle: the subscriptions due at now,
-- never fetched first and then the longest due, at most row_limit, locked so
-- no other cycle fetches the same one. It carries what a fetch sends and not
-- the body, which a cycle of two hundred would otherwise read for nothing.
SELECT id, user_id, url, etag, last_modified
FROM calendar.subscriptions
WHERE next_fetch_at IS NULL OR next_fetch_at <= @now::timestamptz
ORDER BY next_fetch_at NULLS FIRST, id
LIMIT @row_limit::integer
FOR UPDATE SKIP LOCKED;

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
