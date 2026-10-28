-- +goose Up
-- The write-back from Outlook (calendar Outlook sync design O10,
-- docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md;
-- delivery C, docs/superpowers/plans/2026-10-28-calendar-outlook-writeback.md).
--
-- calendar.mailbox_events gains change_key, the event's version as the
-- worker's last write, or its last look, left it: a copy whose version is
-- still that one is as Vantigo wrote it, and one whose version moved is read
-- whole and compared (design O10.4). Null for a row written before C or
-- adopted by a reconcile, which takes the version next answered as its
-- baseline. released_at is set when the person turned the copy into a
-- meeting in Outlook (they added attendees): the row is never written,
-- deleted or taken back again, and is forgotten without a delete (O10.8).
--
-- calendar.mailbox_sync gains the last change made in Outlook that the
-- write-back did not take (O10.3): put_back_reason ('conflict', 'refused',
-- 'not_owner', 'not_editable' or 'many_deleted'), put_back_key, the item key
-- of the copy it was made to ('' for many_deleted, which names none), and
-- put_back_at, when it was put back. The setting's GET
-- answers them; a clean-up, and turning the sync off, clear them. Nothing of
-- what the change said is stored.
--
-- By house style no CHECK holds a value to its set: the module's validation
-- does.
ALTER TABLE calendar.mailbox_events
    ADD COLUMN change_key  varchar(512),   -- the event's version the last write or look left
    ADD COLUMN released_at timestamptz;    -- when the copy became a meeting, never written again

ALTER TABLE calendar.mailbox_sync
    ADD COLUMN put_back_reason varchar(20),   -- 'conflict' | 'refused' | 'not_owner' | 'not_editable' | 'many_deleted'
    ADD COLUMN put_back_key    varchar(200),  -- the item key of the copy the change was made to
    ADD COLUMN put_back_at     timestamptz;   -- when the change was put back

-- +goose Down
-- The versions, the releases and the put-back go with their columns: nothing
-- of them is held anywhere else, and the mailbox keeps its events. A row
-- released then is a copy again, the next push rewriting it whole.
ALTER TABLE calendar.mailbox_sync
    DROP COLUMN put_back_at,
    DROP COLUMN put_back_key,
    DROP COLUMN put_back_reason;
ALTER TABLE calendar.mailbox_events
    DROP COLUMN released_at,
    DROP COLUMN change_key;
