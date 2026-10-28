-- The change notifications' subscriptions (calendar Outlook sync design
-- O11.4): one row per synced mailbox, by the account's object id, holding
-- Graph's subscription id, the SHA-256 of the clientState the create sent,
-- the expiry Graph granted and when a valid notification last named it.
-- Watch and Unwatch write them around their Graph calls; the webhook only
-- marks a row notified (MarkMailboxNotified); Notified reads them.

-- name: GetMailboxSubscription :one
-- GetMailboxSubscription is the mailbox's subscription; no row is a mailbox
-- not watched.
SELECT * FROM entra.mailbox_subscriptions WHERE object_id = @object_id;

-- name: GetMailboxSubscriptionByID :one
-- GetMailboxSubscriptionByID is the row of Graph's subscription id, as a
-- notification names it.
SELECT * FROM entra.mailbox_subscriptions WHERE subscription_id = @subscription_id::varchar;

-- name: UpsertMailboxSubscription :one
-- UpsertMailboxSubscription is a create's write: before the POST, the
-- clientState's hash with no id and no expiry; after Graph's 201, the id and
-- the expiry it answered, with the same hash. Either way the row is active
-- and when it was last notified is kept.
INSERT INTO entra.mailbox_subscriptions (object_id, subscription_id, client_state_hash, expires_at, state, created_at, updated_at)
VALUES (@object_id, sqlc.narg(subscription_id)::varchar, @client_state_hash, sqlc.narg(expires_at)::timestamptz, 'active', @now::timestamptz, @now::timestamptz)
ON CONFLICT (object_id) DO UPDATE SET
    subscription_id = EXCLUDED.subscription_id,
    client_state_hash = EXCLUDED.client_state_hash,
    expires_at = EXCLUDED.expires_at,
    state = 'active',
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: RenewMailboxSubscription :exec
-- RenewMailboxSubscription records a renewal's new expiry, which
-- reauthorizes the subscription too: the row is active again.
UPDATE entra.mailbox_subscriptions SET
    expires_at = @expires_at::timestamptz,
    state = 'active',
    updated_at = @now::timestamptz
WHERE object_id = @object_id;

-- name: SetMailboxSubscriptionState :exec
-- SetMailboxSubscriptionState sets the row's state: 'deleting' when Graph
-- refused the delete, so it is owed.
UPDATE entra.mailbox_subscriptions SET state = @state, updated_at = @now::timestamptz
WHERE object_id = @object_id;

-- name: DeleteMailboxSubscription :exec
-- DeleteMailboxSubscription forgets the mailbox's subscription: deleted in
-- Graph, gone there, or past its expiry.
DELETE FROM entra.mailbox_subscriptions WHERE object_id = @object_id;

-- name: MarkMailboxNotified :execrows
-- MarkMailboxNotified is a valid notification's one write (O11.5): the row
-- notified at now and, for a lifecycle event, its state ('reauthorize',
-- 'removed'). A row marked within the last five seconds is left alone unless
-- a lifecycle state is given, so a flood of notifications only makes the
-- mailbox due, one UPDATE at most per five seconds.
UPDATE entra.mailbox_subscriptions SET
    notified_at = @now::timestamptz,
    state = COALESCE(sqlc.narg(lifecycle_state)::varchar, state),
    updated_at = @now::timestamptz
WHERE subscription_id = @subscription_id::varchar
  AND (notified_at IS NULL
       OR notified_at + interval '5 seconds' < @now::timestamptz
       OR sqlc.narg(lifecycle_state)::varchar IS NOT NULL);

-- name: ListMailboxNotified :many
-- ListMailboxNotified is what Notified answers: the mailboxes notified after
-- since, oldest first, at most row_limit. Through the index on notified_at.
SELECT object_id, notified_at::timestamptz AS notified_at
FROM entra.mailbox_subscriptions
WHERE notified_at > @since::timestamptz
ORDER BY notified_at, object_id
LIMIT @row_limit::integer;

-- name: CountLiveMailboxSubscriptions :one
-- CountLiveMailboxSubscriptions is GET /connection's count (O11.7): the
-- subscriptions Graph still holds as far as the module knows: not past
-- their expiry, not removed by Graph, and not owed a delete.
SELECT count(*)::integer FROM entra.mailbox_subscriptions
WHERE expires_at > @now::timestamptz
  AND state NOT IN ('removed', 'deleting');
