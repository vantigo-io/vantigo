-- +goose Up
-- Mail change notifications share entra.mailbox_subscriptions with calendar
-- events (Outlook connection & Microsoft 365 mail design M11, M17,
-- docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md;
-- delivery E, docs/superpowers/plans/2026-10-29-communications-inbound-notifications.md).
--
-- resource names the watched Graph collection on the mailbox: 'events' for
-- a person's calendar (created with the person's delegated token) or
-- 'messages' for a channel mailbox's Inbox (created with the application
-- token). The primary key becomes (object_id, resource) so a mailbox can be
-- watched for both without colliding; subscription_id remains UNIQUE so the
-- single webhook route (/api/v1/entra/mailbox-notifications) resolves a
-- notification by Graph's subscription id alone.
--
-- By house style in entra, no CHECK holds resource to its set: the module's
-- validation does.
ALTER TABLE entra.mailbox_subscriptions
    ADD COLUMN resource varchar(40) NOT NULL DEFAULT 'events',
    DROP CONSTRAINT mailbox_subscriptions_pkey,
    ADD CONSTRAINT mailbox_subscriptions_pkey PRIMARY KEY (object_id, resource);

-- +goose Down
DELETE FROM entra.mailbox_subscriptions WHERE resource <> 'events';
ALTER TABLE entra.mailbox_subscriptions
    DROP CONSTRAINT mailbox_subscriptions_pkey,
    ADD CONSTRAINT mailbox_subscriptions_pkey PRIMARY KEY (object_id),
    DROP COLUMN resource;
