-- name: BootstrapConsumed :one
-- BootstrapConsumed reports whether the one-time Owner bootstrap is used
-- up: its marker row exists, or any user already holds Owner
-- (EA/AuthEndpoints.cs:491-504).
SELECT (EXISTS (SELECT 1 FROM identity.bootstrap_state WHERE id = 1)
     OR EXISTS (
         SELECT 1
         FROM identity.user_roles ur
         JOIN identity.roles r ON r.id = ur.role_id
         WHERE r.name = 'Owner'
     ))::boolean AS consumed;

-- name: InstallationInUse :one
-- InstallationInUse reports whether the installation is past its first run:
-- any user exists, or the bootstrap marker does
-- (SV/SystemAdminBootstrapper.cs:57-58).
SELECT (EXISTS (SELECT 1 FROM identity.users)
     OR EXISTS (SELECT 1 FROM identity.bootstrap_state WHERE id = 1))::boolean AS in_use;

-- name: InsertBootstrapState :exec
INSERT INTO identity.bootstrap_state (id, completed_at)
VALUES (1, @completed_at::timestamptz);

-- name: RolesByNormalizedName :many
SELECT id, name, normalized_name, is_system, is_built_in
FROM identity.roles
WHERE normalized_name = ANY (@normalized_names::text[]);
