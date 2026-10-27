-- Outlook's busy time (calendar Outlook sync design O9): the events of a
-- person's default calendar that the sync worker read back from their
-- mailbox (calendar.mailbox_busy), times and show_as alone, and the delta's
-- state on calendar.mailbox_sync. The worker's writes leave revision alone,
-- as its other writes do, so a read never turns a person's open form into a
-- 412. The reads join mailbox_sync and answer rows only while the person's
-- sync is on (O9.9): a row left behind by a sync turned off is never shown.

-- name: UpsertMailboxBusy :exec
-- UpsertMailboxBusy stores one event a delta answered, or rewrites it: a
-- timed event's instants, or an all-day event's dates (end inclusive), and
-- its show_as. Nothing else of the event is ever stored.
INSERT INTO calendar.mailbox_busy (user_id, event_id, all_day, starts_at, ends_at, start_date, end_date, show_as, updated_at)
VALUES (@user_id, @event_id, @all_day::boolean, sqlc.narg(starts_at)::timestamptz, sqlc.narg(ends_at)::timestamptz,
        sqlc.narg(start_date)::date, sqlc.narg(end_date)::date, @show_as, @now::timestamptz)
ON CONFLICT (user_id, event_id) DO UPDATE SET
    all_day = EXCLUDED.all_day,
    starts_at = EXCLUDED.starts_at,
    ends_at = EXCLUDED.ends_at,
    start_date = EXCLUDED.start_date,
    end_date = EXCLUDED.end_date,
    show_as = EXCLUDED.show_as,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteMailboxBusy :exec
-- DeleteMailboxBusy forgets one event: removed, cancelled, free, out of the
-- window, or found to be a copy of the calendar's own.
DELETE FROM calendar.mailbox_busy
WHERE user_id = @user_id AND event_id = @event_id;

-- name: DeleteMailboxBusyForUser :execrows
-- DeleteMailboxBusyForUser empties the person's busy rows: a delta started
-- afresh, a sync turned off, cleaned up or left unavailable (O9.1, O9.9).
DELETE FROM calendar.mailbox_busy WHERE user_id = @user_id;

-- name: CountMailboxBusy :one
-- CountMailboxBusy is how many busy rows the person holds, for the cap of
-- 5000 (O9.3).
SELECT count(*)::integer FROM calendar.mailbox_busy WHERE user_id = @user_id;

-- name: ListMailboxBusyEventIDs :many
-- ListMailboxBusyEventIDs is the event ids of the person's busy rows,
-- whatever their sync's state: what the worker's read holds against the cap
-- of 5000, telling an event it stores already from a new one (O9.3).
SELECT event_id FROM calendar.mailbox_busy WHERE user_id = @user_id;

-- name: ListMailboxBusyInWindow :many
-- ListMailboxBusyInWindow is the person's busy rows in a window, while their
-- sync is on: a timed row overlapping from_at..to_at (the end exclusive), an
-- all-day row overlapping from_date..to_date (both inclusive). Through
-- ix_mailbox_busy_starts and ix_mailbox_busy_dates.
SELECT b.*
FROM calendar.mailbox_busy b
JOIN calendar.mailbox_sync s ON s.user_id = b.user_id AND s.enabled
WHERE b.user_id = @user_id
  AND (
    (NOT b.all_day AND b.starts_at < @to_at::timestamptz AND b.ends_at > @from_at::timestamptz)
    OR (b.all_day AND b.start_date <= @to_date::date AND b.end_date >= @from_date::date)
  )
ORDER BY b.all_day, b.starts_at, b.start_date, b.event_id;

-- name: ListMailboxBusyForUsers :many
-- ListMailboxBusyForUsers is ListMailboxBusyInWindow for a batch of people
-- and a set of show_as values — a colleague's busy and oof (O9.7), the busy
-- time CalendarBusy answers (O9.8) — each row for the person whose sync is
-- on.
SELECT b.*
FROM calendar.mailbox_busy b
JOIN calendar.mailbox_sync s ON s.user_id = b.user_id AND s.enabled
WHERE b.user_id = ANY(@user_ids::uuid[])
  AND b.show_as = ANY(@show_as::text[])
  AND (
    (NOT b.all_day AND b.starts_at < @to_at::timestamptz AND b.ends_at > @from_at::timestamptz)
    OR (b.all_day AND b.start_date <= @to_date::date AND b.end_date >= @from_date::date)
  )
ORDER BY b.user_id, b.all_day, b.starts_at, b.start_date, b.event_id;

-- name: SetMailboxBusyLink :exec
-- SetMailboxBusyLink records the delta's link after a page — a next link
-- while one is read, the delta link at its end — and the window it was
-- started over, whatever revision the row holds, as SetMailboxSyncMailbox
-- does: the link answers for the mailbox's state, not for the setting.
UPDATE calendar.mailbox_sync SET
    busy_link = @busy_link::text,
    busy_from = @busy_from::date,
    busy_to = @busy_to::date,
    updated_at = @now::timestamptz
WHERE user_id = @user_id;

-- name: SetMailboxBusySynced :exec
-- SetMailboxBusySynced records that a read reached the delta link at now.
UPDATE calendar.mailbox_sync SET
    busy_synced_at = @now::timestamptz,
    updated_at = @now::timestamptz
WHERE user_id = @user_id;

-- name: ClearMailboxBusyLink :exec
-- ClearMailboxBusyLink forgets the delta's link, its window and the last
-- read, so the next read starts afresh (O9.4, O9.9).
UPDATE calendar.mailbox_sync SET
    busy_link = NULL,
    busy_from = NULL,
    busy_to = NULL,
    busy_synced_at = NULL,
    updated_at = @now::timestamptz
WHERE user_id = @user_id;
