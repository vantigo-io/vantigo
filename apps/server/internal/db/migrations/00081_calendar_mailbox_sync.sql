-- +goose Up
-- The calendar's Outlook sync (calendar Outlook sync design O5,
-- docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md;
-- delivery A, docs/superpowers/plans/2026-10-27-calendar-outlook-sync.md).
--
-- calendar.mailbox_sync is a person's setting and the worker's record of it,
-- one row per user who has ever turned it on. enabled is the person's
-- toggle; target is where the copies go — 'default', their main calendar,
-- which blocks the time for colleagues, or 'own', a calendar named Vantigo
-- (calendar_id, once made); detail is 'full' or 'busy' (titles become Busy);
-- kinds are the item kinds chosen, calendar.appointment and the provider
-- kinds. mailbox is the account's external id last confirmed through Staff,
-- kept for the clean-up alone: a mailbox that changed, or a person who lost
-- it, still has copies to remove there. state is 'pending', 'active',
-- 'stopping' (a clean-up owed), 'off', 'unavailable' or 'failed'; error the
-- last refusal's code. next_sync_at (null is now), last_synced_at, attempts
-- and reconcile (the next sync reads the mailbox's own list first) are the
-- worker's; revision is the If-Match of the person's PUT.
--
-- calendar.mailbox_events is the map of what the worker wrote: one row per
-- item key ('appointment:42:2026-11-02', 'staff.absence:7') with the event's
-- id in the mailbox, the hash of what was written, and starts_on, the item's
-- first day, so a mapped event that now starts before the window is
-- forgotten rather than deleted. Nothing else of the event is stored.
--
-- user_id is an opaque id into identity, as everywhere in this schema; by
-- house style no CHECK holds a value to its set: the module's validation
-- does.
CREATE TABLE calendar.mailbox_sync (
    user_id        uuid         PRIMARY KEY,
    enabled        boolean      NOT NULL DEFAULT false,
    target         varchar(10)  NOT NULL DEFAULT 'default',  -- 'default' | 'own'
    detail         varchar(10)  NOT NULL DEFAULT 'full',     -- 'full' | 'busy'
    kinds          text[]       NOT NULL DEFAULT '{}',
    mailbox        varchar(200),                             -- the account's external id last confirmed
    calendar_id    varchar(500),                             -- the Vantigo calendar's id under target 'own'
    state          varchar(12)  NOT NULL DEFAULT 'pending',
    error          varchar(40),                              -- the last refusal's code; null after a sync that worked
    next_sync_at   timestamptz,                              -- when the worker syncs next; null is now
    last_synced_at timestamptz,
    attempts       integer      NOT NULL DEFAULT 0,          -- failures in a row
    reconcile      boolean      NOT NULL DEFAULT true,       -- the next sync reads the mailbox's list first
    revision       integer      NOT NULL DEFAULT 1,
    created_at     timestamptz  NOT NULL,
    updated_at     timestamptz  NOT NULL
);
-- The worker's due list: the rows on, and those owing a clean-up.
CREATE INDEX ix_mailbox_sync_due ON calendar.mailbox_sync (next_sync_at) WHERE enabled OR state = 'stopping';

CREATE TABLE calendar.mailbox_events (
    user_id      uuid         NOT NULL,
    item_key     varchar(200) NOT NULL,
    event_id     varchar(500) NOT NULL,  -- the event's id in the mailbox
    content_hash varchar(64)  NOT NULL,  -- hex SHA-256 of what was written
    starts_on    date         NOT NULL,  -- the item's first day, for the window
    created_at   timestamptz  NOT NULL,
    updated_at   timestamptz  NOT NULL,
    PRIMARY KEY (user_id, item_key)
);

-- +goose Down
-- Both tables go with their rows: the copies already in people's mailboxes
-- stay there, unmapped — nothing of them was ever copied into another table.
DROP TABLE calendar.mailbox_events;
DROP TABLE calendar.mailbox_sync;
