-- name: RevokeActiveInvitationsForEmail :exec
-- RevokeActiveInvitationsForEmail ends every pending invitation for one
-- normalized email (EA/AuthAccountEndpoints.cs:673-681), so at most one is
-- ever active (ux_invitations_active_email).
UPDATE identity.invitations
SET revoked_at = @now::timestamptz
WHERE normalized_email = @normalized_email AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: InsertInvitation :one
INSERT INTO identity.invitations (
    id, email, normalized_email, role, display_name, token_hash, invited_by_user_id, created_at, expires_at
) VALUES (
    @id, @email, @normalized_email, @role, @display_name, @token_hash, @invited_by_user_id,
    @created_at::timestamptz, @expires_at::timestamptz
)
RETURNING *;

-- name: ListInvitations :many
-- ListInvitations is every invitation, newest first
-- (EA/AuthAccountEndpoints.cs:1148-1174).
SELECT * FROM identity.invitations
ORDER BY created_at DESC, id;

-- name: GetInvitation :one
SELECT * FROM identity.invitations WHERE id = @id;

-- name: RevokeInvitation :one
-- RevokeInvitation revokes a pending invitation and leaves an accepted or
-- already revoked one as it is (EA/AuthAccountEndpoints.cs:1176-1201).
UPDATE identity.invitations
SET revoked_at = CASE WHEN accepted_at IS NULL AND revoked_at IS NULL THEN @now::timestamptz ELSE revoked_at END
WHERE id = @id
RETURNING *;

-- name: GetActiveInvitationByTokenHash :one
-- GetActiveInvitationByTokenHash finds the invitation a token names while
-- it can still be accepted: neither revoked nor accepted, and not expired at
-- now (EA/AuthAccountEndpoints.cs:1469-1478).
SELECT * FROM identity.invitations
WHERE token_hash = @token_hash
  AND revoked_at IS NULL
  AND accepted_at IS NULL
  AND expires_at > @now::timestamptz;

-- name: LockInvitationByTokenHash :one
-- LockInvitationByTokenHash is acceptance's SELECT … FOR UPDATE
-- (EA/AuthAccountEndpoints.cs:1299-1302): a concurrent acceptance of the
-- same token waits here, then fails to serialize.
SELECT * FROM identity.invitations WHERE token_hash = @token_hash FOR UPDATE;

-- name: MarkInvitationAccepted :exec
UPDATE identity.invitations SET accepted_at = @now::timestamptz WHERE id = @id;

-- name: CountPendingOwnerInvitations :one
-- CountPendingOwnerInvitations is how many Owner invitations can still be
-- accepted at @now: BOOTSTRAP_OWNER_EMAIL's startup step issues one only when
-- there is none, and the management status reports "invited" while there is.
SELECT count(*)::int
FROM identity.invitations
WHERE role = 'Owner' AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > @now::timestamptz;
