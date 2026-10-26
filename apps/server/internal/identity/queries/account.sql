-- name: HasProfileAvatar :one
SELECT (EXISTS (SELECT 1 FROM identity.profile_avatars WHERE user_id = @user_id))::boolean;

-- name: UpdateAccountProfile :one
-- UpdateAccountProfile is the self-service profile edit
-- (EA/AccountSettingsEndpoints.cs:93-131): display name, preferred
-- language and, when set_time_zone says the request named it, the user's
-- own time zone (time zones design D5; NULL follows the workspace's). Unlike
-- .NET's UpdateSecurityStampAsync, it deliberately does not touch version or
-- any session (spec *Sessions*, a divergence).
UPDATE identity.users
SET display_name = @display_name, preferred_language = @preferred_language,
    time_zone = CASE WHEN @set_time_zone::boolean THEN sqlc.narg(time_zone)::text ELSE time_zone END,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING id, display_name, email, preferred_language, time_zone;

-- name: UpdateAccountPassword :execrows
-- UpdateAccountPassword stores the new hash and bumps version, the
-- concurrency-stamp analogue (EA/AccountSettingsEndpoints.cs:157). It
-- affects no row when the user has been deleted, or is pending: a pending
-- user's only way to a password is accepting their invitation, and the
-- guard is on the row written, so a pending mark committed while this waits
-- on the row is seen.
UPDATE identity.users
SET password_hash = @password_hash, version = @version, updated_at = @now::timestamptz
WHERE id = @id AND pending_since IS NULL;

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

-- name: GetManagedAvatar :one
-- GetManagedAvatar is the user's managed avatar (staff photo sync design S1):
-- Staff's photo as their picture, standing in front of their own.
SELECT data, content_type, source, source_stamp, version, updated_at
FROM identity.managed_avatars WHERE user_id = @user_id;

-- name: UpsertManagedAvatar :exec
-- UpsertManagedAvatar stores the managed avatar, starting version at 1 or
-- incrementing it on a replace, as UpsertProfileAvatar does; the user's own
-- avatar is left as it is.
INSERT INTO identity.managed_avatars (user_id, data, content_type, source, source_stamp, version, updated_at)
VALUES (@user_id, @data, @content_type, @source, @source_stamp, 1, @now::timestamptz)
ON CONFLICT (user_id) DO UPDATE
SET data = excluded.data,
    content_type = excluded.content_type,
    source = excluded.source,
    source_stamp = excluded.source_stamp,
    version = identity.managed_avatars.version + 1,
    updated_at = excluded.updated_at;

-- name: DeleteManagedAvatar :exec
-- DeleteManagedAvatar clears the managed avatar: the user's own shows again.
DELETE FROM identity.managed_avatars WHERE user_id = @user_id;

-- name: GetEffectiveAvatar :one
-- GetEffectiveAvatar is the user's effective avatar (staff photo sync design
-- §3.1): the managed row when one exists, else the user's own. stamp names
-- the picture standing — "managed:<source_stamp>" or
-- "own:<version>@<unix seconds of updated_at>", so a delete then an upload
-- never repeats a stamp — and managed_by is the managed row's source
-- ("staff"), "" for the user's own.
SELECT e.data, e.content_type, e.stamp, e.managed_by, e.updated_at
FROM (
    SELECT 0 AS priority, m.data, m.content_type, ('managed:' || m.source_stamp)::text AS stamp,
           m.source AS managed_by, m.updated_at
    FROM identity.managed_avatars m WHERE m.user_id = @user_id::uuid
    UNION ALL
    SELECT 1 AS priority, a.data, a.content_type,
           ('own:' || a.version::text || '@' || extract(epoch FROM a.updated_at)::bigint::text)::text AS stamp,
           ''::text AS managed_by, a.updated_at
    FROM identity.profile_avatars a WHERE a.user_id = @user_id::uuid
) e
ORDER BY e.priority
LIMIT 1;

-- name: GetEffectiveAvatarInfo :one
-- GetEffectiveAvatarInfo is GetEffectiveAvatar without the bytes: the
-- effective avatar's type, stamp, source and time.
SELECT e.content_type, e.stamp, e.managed_by, e.updated_at
FROM (
    SELECT 0 AS priority, m.content_type, ('managed:' || m.source_stamp)::text AS stamp,
           m.source AS managed_by, m.updated_at
    FROM identity.managed_avatars m WHERE m.user_id = @user_id::uuid
    UNION ALL
    SELECT 1 AS priority, a.content_type,
           ('own:' || a.version::text || '@' || extract(epoch FROM a.updated_at)::bigint::text)::text AS stamp,
           ''::text AS managed_by, a.updated_at
    FROM identity.profile_avatars a WHERE a.user_id = @user_id::uuid
) e
ORDER BY e.priority
LIMIT 1;

-- name: HasEffectiveAvatar :one
-- HasEffectiveAvatar is whether the user has an effective avatar: a managed
-- one or their own.
SELECT (EXISTS (SELECT 1 FROM identity.managed_avatars m WHERE m.user_id = @user_id::uuid)
        OR EXISTS (SELECT 1 FROM identity.profile_avatars a WHERE a.user_id = @user_id::uuid))::boolean;

-- name: HasManagedAvatar :one
-- HasManagedAvatar is whether a managed avatar stands for the user: while one
-- does, the user's own avatar cannot be changed (staff photo sync design §4).
SELECT (EXISTS (SELECT 1 FROM identity.managed_avatars m WHERE m.user_id = @user_id::uuid))::boolean;
