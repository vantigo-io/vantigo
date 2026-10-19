-- Attendees (calendar design D3, calendar invitations design D1): the people
-- on an appointment, the owner never among them, each of one kind — a user, a
-- customer's contact (an opaque id, named through the directory) or an address
-- typed in. The two outside kinds carry the RSVP token's hash, the sealed link
-- and the record of their last invitation. Written under the appointment's row
-- lock.

-- name: InsertUserAttendee :one
-- InsertUserAttendee adds a colleague at needs-action. One already on the
-- appointment keeps their row and their answer: nothing is inserted and no
-- row is returned.
INSERT INTO calendar.appointment_attendees (appointment_id, kind, user_id, status, created_at)
VALUES (@appointment_id, 'user', @user_id::uuid, 'needs-action', @now::timestamptz)
ON CONFLICT (appointment_id, user_id) WHERE user_id IS NOT NULL DO NOTHING
RETURNING *;

-- name: InsertContactAttendee :one
-- InsertContactAttendee adds a customer's contact at needs-action with its
-- RSVP token's hash and sealed link. One already on the appointment keeps
-- their row, their answer and their token: nothing is inserted and no row is
-- returned.
INSERT INTO calendar.appointment_attendees (
    appointment_id, kind, contact_id, status, rsvp_token_sha256, rsvp_link_ciphertext, created_at
) VALUES (
    @appointment_id, 'contact', @contact_id::integer, 'needs-action', @rsvp_token_sha256::char(64), @rsvp_link_ciphertext::text, @now::timestamptz
)
ON CONFLICT (appointment_id, contact_id) WHERE contact_id IS NOT NULL DO NOTHING
RETURNING *;

-- name: InsertEmailAttendee :one
-- InsertEmailAttendee adds an address typed in, lower-cased by the caller,
-- with the name as typed, at needs-action with its RSVP token's hash and
-- sealed link. One already on the appointment keeps their row, their answer
-- and their token, and takes the name the write gives them now — so a row is
-- returned either way, and the caller tells a kept one from a new one by the
-- row ids it kept (insertAttendees).
INSERT INTO calendar.appointment_attendees (
    appointment_id, kind, email, name, status, rsvp_token_sha256, rsvp_link_ciphertext, created_at
) VALUES (
    @appointment_id, 'email', @email::varchar, @name, 'needs-action', @rsvp_token_sha256::char(64), @rsvp_link_ciphertext::text, @now::timestamptz
)
ON CONFLICT (appointment_id, email) WHERE kind = 'email' DO UPDATE SET name = EXCLUDED.name
RETURNING *;

-- name: DeleteAttendeesNotIn :many
-- DeleteAttendeesNotIn removes every attendee of the appointment whose row id
-- is not in ids — all of them when it is empty — and answers the rows it
-- removed, so the dropped outside attendees can be told.
DELETE FROM calendar.appointment_attendees
WHERE appointment_id = @appointment_id AND NOT (id = ANY(@ids::bigint[]))
RETURNING *;

-- name: ListAttendees :many
SELECT * FROM calendar.appointment_attendees
WHERE appointment_id = @appointment_id
ORDER BY id;

-- name: GetAttendee :one
-- GetAttendee is one attendee of one appointment, by its row id.
SELECT * FROM calendar.appointment_attendees
WHERE id = @id AND appointment_id = @appointment_id;

-- name: GetAttendeeByRsvpToken :one
-- GetAttendeeByRsvpToken is the attendee whose RSVP token hashes to
-- token_sha256 (hex SHA-256), with its appointment — the RSVP page's lookup.
-- The token itself is never stored.
SELECT sqlc.embed(att), sqlc.embed(a)
FROM calendar.appointment_attendees att
JOIN calendar.appointments a ON a.id = att.appointment_id
WHERE att.rsvp_token_sha256 = @token_sha256::char(64);

-- name: SetAttendeeStatus :one
-- SetAttendeeStatus records a colleague's answer, by their user id.
UPDATE calendar.appointment_attendees SET status = @status, responded_at = @now::timestamptz
WHERE appointment_id = @appointment_id AND user_id = @user_id::uuid
RETURNING *;

-- name: SetAttendeeStatusByID :one
-- SetAttendeeStatusByID records an attendee's answer by their row id — an
-- outside attendee's, through the RSVP link.
UPDATE calendar.appointment_attendees SET status = @status, responded_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ResetAttendeeStatuses :exec
-- ResetAttendeeStatuses puts every attendee back to needs-action when the
-- appointment's time changes: what they answered is no longer what is asked.
-- The owner's own status is not stored, so none is spared.
UPDATE calendar.appointment_attendees SET status = 'needs-action', responded_at = NULL
WHERE appointment_id = @appointment_id;

-- name: MarkInvited :exec
-- MarkInvited records a send that succeeded — when, and the SEQUENCE it
-- carried — and clears a failure recorded before it.
UPDATE calendar.appointment_attendees SET
    invited_at = @invited_at::timestamptz,
    invited_sequence = @invited_sequence::integer,
    send_failed_at = NULL
WHERE id = @id;

-- name: MarkSendFailed :exec
-- MarkSendFailed records a send that failed, for the owner's Resend. The last
-- successful send stays recorded.
UPDATE calendar.appointment_attendees SET send_failed_at = @at::timestamptz
WHERE id = @id;
