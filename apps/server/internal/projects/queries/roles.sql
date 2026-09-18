-- name: InsertProjectRole :exec
-- InsertProjectRole assigns one user one role on one project. The primary key
-- (project_id, user_id) is what keeps a user to a single role per project.
INSERT INTO projects.project_roles (project_id, user_id, role, created_at)
VALUES (@project_id, @user_id, @role, @now::timestamptz);

-- name: UpsertProjectRole :one
-- UpsertProjectRole adds one user's role on one project, or changes the role
-- they already hold. created_at is left alone on a change: an assignment that
-- moved from member to viewer is the same assignment, not a new one. Whether
-- this was an add or a change is decided by the caller from the role it read
-- first, so two managers assigning the same user at once both write a row
-- rather than one of them failing the primary key.
INSERT INTO projects.project_roles (project_id, user_id, role, created_at)
VALUES (@project_id, @user_id, @role, @now::timestamptz)
ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role
RETURNING user_id, role, created_at;

-- name: DeleteProjectRole :exec
-- DeleteProjectRole takes one user off one project. A role is removed, never
-- deactivated: unlike a project or a billing line, nothing else in the
-- product holds a reference to it — the timeline keeps the record that it
-- existed.
DELETE FROM projects.project_roles
WHERE project_id = @project_id AND user_id = @user_id;

-- name: GetProjectRole :one
-- GetProjectRole is one user's whole assignment on one project, for the
-- operations whose subject is the assignment itself. authorize() uses
-- RoleForUser instead: it needs the role and nothing else, on every request.
SELECT user_id, role, created_at FROM projects.project_roles
WHERE project_id = @project_id AND user_id = @user_id;

-- name: ListProjectRoles :many
-- ListProjectRoles is everyone holding a role on one project. The order is
-- only a stable one: the contract's order is managers first and then by
-- display name, and display names live in identity's schema, so the sort
-- happens in Go after the directory has named them.
SELECT user_id, role, created_at FROM projects.project_roles
WHERE project_id = @project_id
ORDER BY created_at, user_id;

-- name: RoleForUser :one
-- RoleForUser is the caller's role on one project, the per-project half of
-- authorize(). No row means no role, which reaches the caller as
-- pgx.ErrNoRows rather than an empty string, so "no role" is never confused
-- with a role that failed to load.
SELECT role FROM projects.project_roles
WHERE project_id = @project_id AND user_id = @user_id;

-- name: ListProjectManagers :many
-- ListProjectManagers is every user holding the manager role on one project,
-- for the response's `managers`. Display names are resolved outside SQL,
-- through contracts.UserDirectory: identity's users live in another schema.
SELECT user_id FROM projects.project_roles
WHERE project_id = @project_id AND role = 'manager'
ORDER BY created_at, user_id;
