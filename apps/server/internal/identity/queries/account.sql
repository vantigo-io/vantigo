-- name: HasProfileAvatar :one
SELECT (EXISTS (SELECT 1 FROM identity.profile_avatars WHERE user_id = @user_id))::boolean;

-- name: UpdateAccountProfile :one
-- UpdateAccountProfile is the self-service profile edit
-- (EA/AccountSettingsEndpoints.cs:93-131): display name and preferred
-- language only. Unlike .NET's UpdateSecurityStampAsync, it deliberately
-- does not touch version or any session (spec *Sessions*, a divergence).
UPDATE identity.users
SET display_name = @display_name, preferred_language = @preferred_language, updated_at = @now::timestamptz
WHERE id = @id
RETURNING id, display_name, email, preferred_language;

-- name: UpdateAccountPassword :execrows
-- UpdateAccountPassword stores the new hash and bumps version, the
-- concurrency-stamp analogue (EA/AccountSettingsEndpoints.cs:157). It
-- affects no row when the user has been deleted.
UPDATE identity.users
SET password_hash = @password_hash, version = @version, updated_at = @now::timestamptz
WHERE id = @id;

-- name: DeleteUserPasswordResetTokens :exec
-- DeleteUserPasswordResetTokens keeps a changed password from being
-- undone by a reset token minted before it (spec *Credentials*).
DELETE FROM identity.password_reset_tokens WHERE user_id = @user_id;

-- name: UpsertProfileAvatar :exec
-- UpsertProfileAvatar stores an upload, starting version at 1 or
-- incrementing it on a replace (EA/AccountSettingsEndpoints.cs:218-236).
INSERT INTO identity.profile_avatars (user_id, data, content_type, version, updated_at)
VALUES (@user_id, @data, @content_type, 1, @now::timestamptz)
ON CONFLICT (user_id) DO UPDATE
SET data = excluded.data,
    content_type = excluded.content_type,
    version = identity.profile_avatars.version + 1,
    updated_at = excluded.updated_at;

-- name: GetProfileAvatar :one
SELECT data, content_type FROM identity.profile_avatars WHERE user_id = @user_id;

-- name: DeleteProfileAvatar :exec
DELETE FROM identity.profile_avatars WHERE user_id = @user_id;
