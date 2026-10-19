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

-- name: TouchAppointment :one
-- TouchAppointment moves the revision on after a write to the appointment's
-- attendees or exceptions alone, so an If-Match read before it is stale.
UPDATE calendar.appointments SET revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteAppointment :execrows
-- DeleteAppointment removes the series; its attendees and exceptions go with
-- it (ON DELETE CASCADE).
DELETE FROM calendar.appointments WHERE id = @id;

-- name: ListAppointmentsInWindow :many
-- ListAppointmentsInWindow is the user's appointments — owned, or attended with
-- any status — that may have an occurrence in the window, each with the user's
-- own status (null when they own it). A single one is in when its instants
-- overlap [from_at, to_at) or its dates overlap [from_date, to_date]; a series
-- when it starts before the window ends and its last day, when it has one, is
-- no earlier than from_date. The caller widens from_date and to_date enough to
-- cover the zones the series expand in and trims what the expansion yields.
SELECT sqlc.embed(a), att.status AS my_status
FROM calendar.appointments a
LEFT JOIN calendar.appointment_attendees att ON att.appointment_id = a.id AND att.user_id = @user_id
WHERE (a.owner_user_id = @user_id OR att.user_id IS NOT NULL)
  AND (
    (a.rrule IS NULL AND (
        (NOT a.all_day AND a.starts_at < @to_at::timestamptz AND a.ends_at > @from_at::timestamptz)
        OR (a.all_day AND a.start_date <= @to_date::date AND a.end_date >= @from_date::date)
    ))
    OR (a.rrule IS NOT NULL
        AND (a.starts_at < @to_at::timestamptz OR a.start_date <= @to_date::date)
        AND (a.series_last_date IS NULL OR a.series_last_date >= @from_date::date))
  )
ORDER BY a.id;

-- name: ListColleagueBusyAppointmentsInWindow :many
-- ListColleagueBusyAppointmentsInWindow is a colleague's appointments that
-- block their time (calendar design D6): owned, or attended and accepted, with
-- transparency busy, in the window as ListAppointmentsInWindow judges it.
SELECT a.*
FROM calendar.appointments a
WHERE a.transparency = 'busy'
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
  )
ORDER BY a.id;
