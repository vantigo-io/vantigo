-- Appointments (calendar design D2, D3, D5): one row per appointment, single
-- or a series. Every write runs in one transaction under the row's lock
-- (GetAppointmentForUpdate), and the directory is read before it opens.

-- name: InsertAppointment :one
INSERT INTO calendar.appointments (
    owner_user_id, title, description, location, all_day,
    start_date, end_date, starts_at, ends_at, time_zone,
    visibility, transparency, rrule, series_last_date,
    customer_id, project_id, ical_uid, created_at, updated_at
) VALUES (
    @owner_user_id, @title, @description, @location, @all_day,
    @start_date, @end_date, @starts_at, @ends_at, @time_zone,
    @visibility, @transparency, @rrule, @series_last_date,
    @customer_id, @project_id, @ical_uid, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: GetAppointment :one
SELECT * FROM calendar.appointments WHERE id = @id;

-- name: GetAppointmentForUpdate :one
-- GetAppointmentForUpdate takes the appointment's row lock for a write of its
-- own — the appointment itself, its attendees, its exceptions or an answer —
-- so the If-Match revision is judged against the row the write then changes.
SELECT * FROM calendar.appointments WHERE id = @id FOR UPDATE;

-- name: UpdateAppointment :one
-- UpdateAppointment replaces everything a PUT carries. bump_sequence is set
-- when what changed is when, where or the rule (iCalendar's SEQUENCE, for the
-- invitations and the feed); the revision moves on every write.
UPDATE calendar.appointments SET
    title = @title,
    description = @description,
    location = @location,
    all_day = @all_day,
    start_date = @start_date,
    end_date = @end_date,
    starts_at = @starts_at,
    ends_at = @ends_at,
    time_zone = @time_zone,
    visibility = @visibility,
    transparency = @transparency,
    rrule = @rrule,
    series_last_date = @series_last_date,
    customer_id = @customer_id,
    project_id = @project_id,
    sequence = sequence + CASE WHEN @bump_sequence::boolean THEN 1 ELSE 0 END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: TouchAppointmentWhen :one
-- TouchAppointmentWhen moves the revision and the sequence on after a write
-- to the appointment's exceptions alone: a moved, cancelled or restored
-- occurrence is a change of when (calendar design D2), and an If-Match read
-- before it is stale. An attendee's answer changes nothing of the
-- appointment and touches neither.
UPDATE calendar.appointments SET
    revision = revision + 1,
    sequence = sequence + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: CancelAppointment :one
-- CancelAppointment marks the appointment cancelled (calendar invitations
-- design D6) instead of deleting it, once someone outside has been sent an
-- invitation: the sequence moves on for the CANCEL that tells them, and the
-- revision, since every write to it from then on is refused.
UPDATE calendar.appointments SET
    cancelled_at = @cancelled_at::timestamptz,
    sequence = sequence + 1,
    revision = revision + 1,
    updated_at = @cancelled_at::timestamptz
WHERE id = @id
RETURNING *;

-- name: ListCancelledDue :many
-- ListCancelledDue is the cancelled appointments whose last day has passed,
-- for the calendar-cancellations worker, oldest first and at most row_limit:
-- a single one when its end has (a timed one's instant, an all-day one's last
-- day before today in its own zone), a bounded series when both its last
-- occurrence's day and the end of every occurrence moved — perhaps past that
-- day — have, and an unbounded series when it was cancelled before
-- unbounded_cancelled_before (ninety days ago, the caller's constant).
SELECT a.id
FROM calendar.appointments a
WHERE a.cancelled_at IS NOT NULL
  AND (
    (a.rrule IS NULL AND NOT a.all_day AND a.ends_at <= @now::timestamptz)
    OR (a.rrule IS NULL AND a.all_day AND a.end_date < timezone(a.time_zone, @now::timestamptz)::date)
    OR (a.rrule IS NOT NULL AND a.series_last_date IS NOT NULL
        AND a.series_last_date < timezone(a.time_zone, @now::timestamptz)::date
        AND NOT EXISTS (
            SELECT 1 FROM calendar.appointment_exceptions e
            WHERE e.appointment_id = a.id AND NOT e.cancelled
              AND (e.ends_at > @now::timestamptz
                OR e.end_date >= timezone(a.time_zone, @now::timestamptz)::date)
        ))
    OR (a.rrule IS NOT NULL AND a.series_last_date IS NULL
        AND a.cancelled_at < @unbounded_cancelled_before::timestamptz)
  )
ORDER BY a.cancelled_at, a.id
LIMIT @row_limit::integer;

-- name: DeleteCancelled :execrows
-- DeleteCancelled erases the cancelled appointments in ids, their attendees
-- and exceptions with them (ON DELETE CASCADE). One that is not cancelled is
-- never erased here.
DELETE FROM calendar.appointments WHERE id = ANY(@ids::bigint[]) AND cancelled_at IS NOT NULL;

-- name: DeleteAppointment :execrows
-- DeleteAppointment removes the series; its attendees and exceptions go with
-- it (ON DELETE CASCADE).
DELETE FROM calendar.appointments WHERE id = @id;

-- name: ListAppointmentsInWindow :many
-- ListAppointmentsInWindow is the user's appointments — owned, or attended with
-- any status, and not cancelled — that may have an occurrence in the window, each with the user's
-- own status (null when they own it). A single one is in when its instants
-- overlap [from_at, to_at) or its dates overlap [from_date, to_date]; a series
-- when it starts before the window ends and its last day, when it has one, is
-- no earlier than from_date; and any series with an occurrence moved into
-- the window. The caller widens from_date and to_date enough to
-- cover the zones the series expand in and trims what the expansion yields.
SELECT sqlc.embed(a), att.status AS my_status
FROM calendar.appointments a
LEFT JOIN calendar.appointment_attendees att ON att.appointment_id = a.id AND att.user_id = @user_id::uuid
WHERE (a.owner_user_id = @user_id OR att.user_id IS NOT NULL)
  AND a.cancelled_at IS NULL
  AND (
    (a.rrule IS NULL AND (
        (NOT a.all_day AND a.starts_at < @to_at::timestamptz AND a.ends_at > @from_at::timestamptz)
        OR (a.all_day AND a.start_date <= @to_date::date AND a.end_date >= @from_date::date)
    ))
    OR (a.rrule IS NOT NULL
        AND (a.starts_at < @to_at::timestamptz OR a.start_date <= @to_date::date)
        AND (a.series_last_date IS NULL OR a.series_last_date >= @from_date::date))
    -- An occurrence moved into the window from anywhere — before the series
    -- starts, or past its last day.
    OR EXISTS (
        SELECT 1 FROM calendar.appointment_exceptions e
        WHERE e.appointment_id = a.id AND NOT e.cancelled
          AND ((e.starts_at < @to_at::timestamptz AND e.ends_at > @from_at::timestamptz)
            OR (e.start_date <= @to_date::date AND e.end_date >= @from_date::date))
    )
  )
ORDER BY a.id;

-- name: ListColleagueBusyAppointmentsInWindow :many
-- ListColleagueBusyAppointmentsInWindow is a colleague's appointments that
-- block their time (calendar design D6): owned, or attended and accepted, with
-- transparency busy and not cancelled, in the window as ListAppointmentsInWindow judges it.
SELECT a.*
FROM calendar.appointments a
WHERE a.transparency = 'busy'
  AND a.cancelled_at IS NULL
  AND (
    a.owner_user_id = @user_id
    OR EXISTS (
        SELECT 1 FROM calendar.appointment_attendees att
        WHERE att.appointment_id = a.id AND att.user_id = @user_id AND att.status = 'accepted'
    )
  )
  AND (
    (a.rrule IS NULL AND (
        (NOT a.all_day AND a.starts_at < @to_at::timestamptz AND a.ends_at > @from_at::timestamptz)
        OR (a.all_day AND a.start_date <= @to_date::date AND a.end_date >= @from_date::date)
    ))
    OR (a.rrule IS NOT NULL
        AND (a.starts_at < @to_at::timestamptz OR a.start_date <= @to_date::date)
        AND (a.series_last_date IS NULL OR a.series_last_date >= @from_date::date))
    -- An occurrence moved into the window from anywhere — before the series
    -- starts, or past its last day.
    OR EXISTS (
        SELECT 1 FROM calendar.appointment_exceptions e
        WHERE e.appointment_id = a.id AND NOT e.cancelled
          AND ((e.starts_at < @to_at::timestamptz AND e.ends_at > @from_at::timestamptz)
            OR (e.start_date <= @to_date::date AND e.end_date >= @from_date::date))
    )
  )
ORDER BY a.id;
