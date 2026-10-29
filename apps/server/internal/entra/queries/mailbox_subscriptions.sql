-- The change notifications' subscriptions (calendar Outlook sync design
-- O11.4; Outlook connection & Microsoft 365 mail design M11, M17): one row per
-- watched (object_id, resource) pair — 'events' for a person's calendar,
-- 'messages' for a channel mailbox's Inbox — holding Graph's subscription id,
-- the SHA-256 of the clientState the create sent, the expiry Graph granted and
-- when a valid notification last named it. Watch and Unwatch write them around
-- their Graph calls; the webhook only marks a row notified
-- (MarkMailboxesNotified); Notified reads them by resource.

-- name: GetMailboxSubscription :one
-- GetMailboxSubscription is the mailbox's subscription for resource; no row is
-- a mailbox not watched for that resource.
SELECT * FROM entra.mailbox_subscriptions WHERE object_id = @object_id AND resource = @resource::varchar;

-- name: GetMailboxSubscriptionByID :one
-- GetMailboxSubscriptionByID is the row of Graph's subscription id, as a
-- notification names it.
SELECT * FROM entra.mailbox_subscriptions WHERE subscription_id = @subscription_id::varchar;

-- name: ListMailboxSubscriptionsByID :many
-- ListMailboxSubscriptionsByID is the rows of Graph's subscription ids, as
-- one request of notifications names them: one read for the whole request,
-- however many items it holds (O11.5).
SELECT * FROM entra.mailbox_subscriptions WHERE subscription_id = ANY(@subscription_ids::varchar[]);

-- name: UpsertMailboxSubscription :one
-- UpsertMailboxSubscription is a create's write: before the POST, the
-- clientState's hash and the webhook's address with no id and no expiry;
-- after Graph's 201, the id and the expiry it answered, with the same hash
-- and address. Either way the row is active and when it was last notified
-- is kept.
INSERT INTO entra.mailbox_subscriptions (object_id, resource, subscription_id, client_state_hash, notification_url, expires_at, state, created_at, updated_at)
VALUES (@object_id, @resource::varchar, sqlc.narg(subscription_id)::varchar, @client_state_hash, @notification_url::varchar, sqlc.narg(expires_at)::timestamptz, 'active', @now::timestamptz, @now::timestamptz)
ON CONFLICT (object_id, resource) DO UPDATE SET
    subscription_id = EXCLUDED.subscription_id,
    client_state_hash = EXCLUDED.client_state_hash,
    notification_url = EXCLUDED.notification_url,
    expires_at = EXCLUDED.expires_at,
    state = 'active',
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: RenewMailboxSubscription :exec
-- RenewMailboxSubscription records a renewal's new expiry, which
-- reauthorizes the subscription too: the row is active again — only while
-- it still holds the subscription renewed and Graph has not removed it
-- since the Watch read it (a lifecycle notification racing the PATCH), so a
-- removed row is never made active again.
UPDATE entra.mailbox_subscriptions SET
    expires_at = @expires_at::timestamptz,
    state = 'active',
    updated_at = @now::timestamptz
WHERE object_id = @object_id
  AND subscription_id = @subscription_id::varchar
  AND state <> 'removed';

-- name: SetMailboxSubscriptionState :exec
-- SetMailboxSubscriptionState sets the row's state: 'deleting' when Graph
-- refused the delete, so it is owed; 'conflict' when another installation's
-- subscription holds the mailbox, so no create is tried for a day.
UPDATE entra.mailbox_subscriptions SET state = @state, updated_at = @now::timestamptz
WHERE object_id = @object_id AND resource = @resource::varchar;

-- name: DeleteMailboxSubscription :exec
-- DeleteMailboxSubscription forgets the mailbox's subscription for resource:
-- deleted in Graph, gone there, or past its expiry.
DELETE FROM entra.mailbox_subscriptions WHERE object_id = @object_id AND resource = @resource::varchar;

-- name: MarkMailboxesNotified :execrows
-- MarkMailboxesNotified is the plain notifications' one write for a whole
-- request (O11.5): the rows of the subscription ids marked notified at now,
-- in one UPDATE however many items named them. A row marked within the last
-- five seconds is left alone, so a flood of notifications only makes the
-- mailbox due, one write at most per five seconds.
UPDATE entra.mailbox_subscriptions SET
    notified_at = @now::timestamptz,
    updated_at = @now::timestamptz
WHERE subscription_id = ANY(@subscription_ids::varchar[])
  AND (notified_at IS NULL OR notified_at + interval '5 seconds' < @now::timestamptz);

-- name: MarkMailboxLifecycle :execrows
-- MarkMailboxLifecycle is a lifecycle notification's write, one row at a
-- time (they are rare): the row notified at now, whatever it was marked
-- within the last five seconds, and its state set ('reauthorize',
-- 'removed') — unless it is 'deleting', a delete owed that no lifecycle
-- event may forget.
UPDATE entra.mailbox_subscriptions SET
    notified_at = @now::timestamptz,
    state = CASE WHEN state = 'deleting' THEN state ELSE @lifecycle_state::varchar END,
    updated_at = @now::timestamptz
WHERE subscription_id = @subscription_id::varchar;

-- name: ListMailboxNotified :many
-- ListMailboxNotified is what Notified answers for resource ('events' or
-- 'messages'): the mailboxes notified after since, oldest first, at most
-- row_limit. Through the index on notified_at.
SELECT object_id, notified_at::timestamptz AS notified_at
FROM entra.mailbox_subscriptions
WHERE resource = @resource::varchar
  AND notified_at > @since::timestamptz
ORDER BY notified_at, object_id
LIMIT @row_limit::integer;

-- name: CountLiveMailboxSubscriptions :one
-- CountLiveMailboxSubscriptions is GET /connection's count (O11.7): the
-- subscriptions Graph still holds as far as the module knows: not past
-- their expiry, not removed by Graph, and not owed a delete.
SELECT count(*)::integer FROM entra.mailbox_subscriptions
WHERE expires_at > @now::timestamptz
  AND state NOT IN ('removed', 'deleting', 'conflict');
