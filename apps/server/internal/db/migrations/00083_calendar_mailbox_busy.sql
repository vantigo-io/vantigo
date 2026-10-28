-- +goose Up
-- Outlook's busy time in Vantigo (calendar Outlook sync design O9,
-- docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md;
-- delivery B, docs/superpowers/plans/2026-10-27-calendar-outlook-busy.md).
--
-- calendar.mailbox_busy is what the sync worker read back from a person's
-- default calendar in their mailbox: one row per event that blocks their
-- time — not a copy the worker wrote, not free, not cancelled — with its
-- times and its show_as ('busy', 'tentative', 'oof' or 'workingElsewhere')
-- and nothing else. A timed event is starts_at..ends_at; an all-day one
-- start_date..end_date, end_date inclusive, its timestamps null. Never a
-- subject, a body, a location, an organizer or an attendee: the delta
-- answers them, and they are not decoded. event_id is the mailbox's id of
-- the event or the occurrence, the key the delta's changes and removals
-- name; it never leaves the server.
--
-- calendar.mailbox_sync gains the delta's state: busy_link, the provider's
-- next or delta link (opaque, valid for the person's mailbox alone),
-- busy_from and busy_to, the window the delta was started over, and
-- busy_synced_at, when a read last reached the delta link. All four are
-- cleared, and the person's busy rows deleted, when the sync is turned off,
-- cleaned up or left unavailable (design O9.9).
--
-- user_id is an opaque id into identity, as everywhere in this schema; by
-- house style no CHECK holds a value to its set: the module's validation
-- does.
CREATE TABLE calendar.mailbox_busy (
    user_id    uuid         NOT NULL,
    event_id   varchar(512) NOT NULL,  -- the event's id in the mailbox, at most the provider's bound
    all_day    boolean      NOT NULL,
    starts_at  timestamptz,            -- a timed event's start
    ends_at    timestamptz,            -- a timed event's end, exclusive
    start_date date,                   -- an all-day event's first day
    end_date   date,                   -- an all-day event's last day, inclusive
    show_as    varchar(20)  NOT NULL,  -- 'busy' | 'tentative' | 'oof' | 'workingElsewhere'
    updated_at timestamptz  NOT NULL,
    PRIMARY KEY (user_id, event_id)
);
-- The reads of a window: the timed rows by their start, the all-day by
-- their first day.
CREATE INDEX ix_mailbox_busy_starts ON calendar.mailbox_busy (user_id, starts_at);
CREATE INDEX ix_mailbox_busy_dates ON calendar.mailbox_busy (user_id, start_date);

ALTER TABLE calendar.mailbox_sync
    ADD COLUMN busy_link      text,         -- the delta's next or delta link
    ADD COLUMN busy_from      date,         -- the window the delta was started over
    ADD COLUMN busy_to        date,
    ADD COLUMN busy_synced_at timestamptz;  -- when a read last reached the delta link

-- +goose Down
-- The busy rows go with their table and the delta's state with its columns:
-- nothing of them is held anywhere else, and the mailbox keeps its events.
ALTER TABLE calendar.mailbox_sync
    DROP COLUMN busy_synced_at,
    DROP COLUMN busy_to,
    DROP COLUMN busy_from,
    DROP COLUMN busy_link;
DROP TABLE calendar.mailbox_busy;
