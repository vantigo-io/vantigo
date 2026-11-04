-- +goose Up
-- Calendar sync on the person's own Google connection (Google Workspace design
-- G30–G32, docs/superpowers/specs/2026-11-03-google-workspace-design.md;
-- delivery E, docs/superpowers/plans/2026-11-03-google-calendar.md): what
-- Entra's mailbox subscriptions (00085, 00093) are for Outlook.
--
-- google.calendars are the secondary calendars the module made in a person's
-- Google Calendar, by the account's Directory id: Google admits two calendars
-- of one name and carries no property on a calendar, so this row is what
-- makes a calendar the module's — never adopted, never deleted without it.
--
-- google.calendar_channels is the one push channel (events.watch) a person's
-- primary calendar is watched by: Google's channel id and resource id, the
-- SHA-256 of the channel token the notifications carry (the token itself is
-- never stored), when Google said the channel expires and the life it
-- granted (renewed at min(3 days, half of it), U5), and when a notification
-- last named it. A row keeps google-sweep from deleting a closing connection
-- before its channel is stopped (G28).
--
-- google.connection.marker is 32 hex digits drawn once: the value of the
-- vantigo private extended property on every event the installation writes,
-- so two installations writing one calendar never take each other's copies.
CREATE TABLE google.calendars (
    google_user_id varchar(32)  NOT NULL,          -- the account's Directory id
    calendar_id    varchar(255) NOT NULL,          -- Google's calendar id
    created_at     timestamptz  NOT NULL,
    PRIMARY KEY (google_user_id, calendar_id)
);

CREATE TABLE google.calendar_channels (
    google_user_id  varchar(32)  PRIMARY KEY,      -- the account's Directory id
    channel_id      uuid         NOT NULL,         -- the channel's id, ours
    resource_id     varchar(255) NOT NULL,         -- Google's id of the watched resource
    token_hash      bytea        NOT NULL,         -- SHA-256 of the channel token
    address         varchar(2048) NOT NULL,        -- where Google posts, as asked
    expires_at      timestamptz  NOT NULL,
    granted_seconds integer      NOT NULL,
    notified_at     timestamptz,
    created_at      timestamptz  NOT NULL,
    CONSTRAINT calendar_channels_token_hash_check CHECK (octet_length(token_hash) = 32)
);
CREATE UNIQUE INDEX ux_google_calendar_channels_channel ON google.calendar_channels (channel_id);
CREATE INDEX ix_google_calendar_channels_notified ON google.calendar_channels (notified_at) WHERE notified_at IS NOT NULL;

ALTER TABLE google.connection
    ADD COLUMN marker varchar(32) NOT NULL DEFAULT replace(gen_random_uuid()::text, '-', '');
ALTER TABLE google.connection
    ADD CONSTRAINT connection_marker_check CHECK (marker ~ '^[0-9a-f]{32}$');

-- +goose Down
-- The calendars and the copies stay in the people's Google Calendars; the
-- channels lapse at their expiry.
ALTER TABLE google.connection
    DROP CONSTRAINT connection_marker_check,
    DROP COLUMN marker;
DROP TABLE google.calendar_channels;
DROP TABLE google.calendars;
