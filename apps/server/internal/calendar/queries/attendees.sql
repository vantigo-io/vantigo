-- Attendees (calendar design D3): the colleagues on an appointment, the owner
-- never among them. Written under the appointment's row lock.

-- name: InsertAttendees :exec
-- InsertAttendees adds each user not already on the appointment at
-- needs-action; one already there keeps their answer. With
-- DeleteAttendeesExcept it replaces the list.
INSERT INTO calendar.appointment_attendees (appointment_id, user_id, status, responded_at, created_at)
SELECT @appointment_id::bigint, u, 'needs-action', NULL, @now::timestamptz
FROM unnest(@user_ids::uuid[]) AS u
ON CONFLICT (appointment_id, user_id) DO NOTHING;

-- name: DeleteAttendeesExcept :exec
-- DeleteAttendeesExcept removes every attendee not in user_ids — all of them
-- when it is empty.
DELETE FROM calendar.appointment_attendees
WHERE appointment_id = @appointment_id AND NOT (user_id = ANY(@user_ids::uuid[]));

-- name: ListAttendees :many
SELECT * FROM calendar.appointment_attendees
WHERE appointment_id = @appointment_id
ORDER BY id;

-- name: SetAttendeeStatus :one
-- SetAttendeeStatus records one attendee's answer.
UPDATE calendar.appointment_attendees SET status = @status, responded_at = @now::timestamptz
WHERE appointment_id = @appointment_id AND user_id = @user_id
RETURNING *;

-- name: ResetAttendeeStatuses :exec
-- ResetAttendeeStatuses puts every attendee back to needs-action when the
-- appointment's time changes: what they answered is no longer what is asked.
-- The owner's own status is not stored, so none is spared.
UPDATE calendar.appointment_attendees SET status = 'needs-action', responded_at = NULL
WHERE appointment_id = @appointment_id;
