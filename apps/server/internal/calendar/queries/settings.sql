-- The calendar's workspace settings (integrations design T6): the one row of
-- calendar.settings, the channel invitations to people outside the company are
-- sent from. The migration writes the row, so a read always answers it.

-- name: GetSettings :one
-- GetSettings reads the installation's single settings row.
SELECT id, invitation_channel, updated_at, revision FROM calendar.settings WHERE id = 1;

-- name: LockSettings :one
-- LockSettings takes the settings row FOR UPDATE: PUT /invitation-settings
-- waits here behind another write and compares the revision it reads.
SELECT id, invitation_channel, updated_at, revision FROM calendar.settings WHERE id = 1 FOR UPDATE;

-- name: SetInvitationChannel :one
-- SetInvitationChannel replaces the channel invitations are sent from, null
-- for off, and moves the revision on. The caller holds the row (LockSettings)
-- and has checked the revision.
UPDATE calendar.settings
SET invitation_channel = sqlc.narg(invitation_channel), updated_at = @now::timestamptz, revision = revision + 1
WHERE id = 1
RETURNING id, invitation_channel, updated_at, revision;
