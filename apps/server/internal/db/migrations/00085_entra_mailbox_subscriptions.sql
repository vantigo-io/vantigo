-- +goose Up
-- The change notifications from Outlook (calendar Outlook sync design O11,
-- docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md;
-- delivery D, docs/superpowers/plans/2026-10-28-calendar-outlook-notifications.md).
--
-- entra.mailbox_subscriptions holds one Graph subscription per synced
-- mailbox (O11.4), by the account's object id: subscription_id is Graph's
-- (a GUID, null while the create is in flight), client_state_hash the
-- SHA-256 of the clientState sent with the create — a 256-bit random secret,
-- so a hash is enough, and the plain value is never stored — expires_at the
-- expiry Graph granted, state 'active', 'reauthorize', 'removed' or
-- 'deleting', and notified_at when a valid notification last named it: what
-- the calendar's worker asks for (Notified), through the index on it.
--
-- entra.connection gains notifications_validating_until, the end of the
-- window in which the webhook answers Graph's handshake (set before each
-- create and cleared after it, O11.5); notifications_unreachable_at, when a
-- create's handshake last failed (O11.7: no create for anyone for an hour);
-- and last_notification_at, when a notification last came, moved at most
-- once a minute.
--
-- By house style no CHECK holds a value to its set: the module's validation
-- does.
CREATE TABLE entra.mailbox_subscriptions (
    object_id         uuid         PRIMARY KEY,
    subscription_id   varchar(64)  UNIQUE,
    client_state_hash bytea        NOT NULL,
    expires_at        timestamptz,
    state             varchar(20)  NOT NULL DEFAULT 'active',  -- 'active' | 'reauthorize' | 'removed' | 'deleting'
    notified_at       timestamptz,
    created_at        timestamptz  NOT NULL,
    updated_at        timestamptz  NOT NULL
);
CREATE INDEX ix_entra_mailbox_subscriptions_notified ON entra.mailbox_subscriptions (notified_at);

ALTER TABLE entra.connection
    ADD COLUMN notifications_validating_until timestamptz,   -- the handshake's window, while a create is in flight
    ADD COLUMN notifications_unreachable_at   timestamptz,   -- when a create's handshake last failed
    ADD COLUMN last_notification_at           timestamptz;   -- when a notification last came, to within a minute

-- +goose Down
-- The rows go, and the columns: Graph's subscriptions lapse on their own
-- within seven days, and their notifications meanwhile reach no route.
ALTER TABLE entra.connection
    DROP COLUMN last_notification_at,
    DROP COLUMN notifications_unreachable_at,
    DROP COLUMN notifications_validating_until;
DROP TABLE entra.mailbox_subscriptions;
