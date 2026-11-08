-- name: InsertAgentUser :exec
-- InsertAgentUser makes a user of kind agent (virtual employees design V5): a
-- confirmed address that never routes, no credential, and pending_since left
-- null: a user with pending_since set may not act, and an agent must.
INSERT INTO identity.users (
    id, email, normalized_email, email_confirmed, display_name, kind, version, created_at, updated_at
) VALUES (
    @id, @email, @normalized_email, true, @display_name, 'agent', @version, @now::timestamptz, @now::timestamptz
);

-- name: RenameAgentUser :execrows
UPDATE identity.users
SET display_name = @display_name, version = @version, updated_at = @now::timestamptz
WHERE id = @id AND kind = 'agent';

-- name: SetAgentUserDisabled :execrows
-- SetAgentUserDisabled disables or enables an agent's user: disabled_by is
-- 'owner' for a disable, as an Owner's is, and null for an enable.
UPDATE identity.users
SET is_disabled = @is_disabled, disabled_by = sqlc.narg(disabled_by), version = @version, updated_at = @now::timestamptz
WHERE id = @id AND kind = 'agent';

-- name: IsAgentUser :one
SELECT EXISTS (SELECT 1 FROM identity.users WHERE id = @id AND kind = 'agent')::boolean AS is_agent;

-- name: GetAgentPrincipal :one
-- GetAgentPrincipal is what Access.Check needs of an agent's user: whether it
-- is of kind agent, whether it is disabled, and its direct roles. A user that
-- does not exist has no row.
SELECT u.kind, u.is_disabled, u.lockout_end, r.role_names
FROM identity.users u
CROSS JOIN LATERAL (
    SELECT coalesce(array_agg(ro.name ORDER BY ro.name), '{}')::text[] AS role_names
    FROM identity.user_roles ur
    JOIN identity.roles ro ON ro.id = ur.role_id
    WHERE ur.user_id = u.id
) r
WHERE u.id = @id;
