-- Exceptions (calendar design D4): one occurrence of a series cancelled or
-- moved, keyed by the day the series put it on. Written under the
-- appointment's row lock.

-- name: UpsertException :one
INSERT INTO calendar.appointment_exceptions (
    appointment_id, occurrence_date, cancelled, start_date, end_date, starts_at, ends_at, created_at, updated_at
) VALUES (
    @appointment_id, @occurrence_date, @cancelled, @start_date, @end_date, @starts_at, @ends_at,
    @now::timestamptz, @now::timestamptz
)
ON CONFLICT (appointment_id, occurrence_date) DO UPDATE SET
    cancelled = EXCLUDED.cancelled,
    start_date = EXCLUDED.start_date,
    end_date = EXCLUDED.end_date,
    starts_at = EXCLUDED.starts_at,
    ends_at = EXCLUDED.ends_at,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteException :execrows
DELETE FROM calendar.appointment_exceptions
WHERE appointment_id = @appointment_id AND occurrence_date = @occurrence_date;

-- name: ListExceptions :many
-- ListExceptions is the exceptions of every appointment named, for the
-- window's expansion.
SELECT * FROM calendar.appointment_exceptions
WHERE appointment_id = ANY(@appointment_ids::bigint[])
ORDER BY appointment_id, occurrence_date;

-- name: DeleteExceptions :exec
-- DeleteExceptions drops every exception of an appointment, when a PUT
-- changes its rule or its start and the days they named may no longer exist.
DELETE FROM calendar.appointment_exceptions WHERE appointment_id = @appointment_id;
