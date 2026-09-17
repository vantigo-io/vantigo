-- name: InsertProjectRole :exec
-- InsertProjectRole assigns one user one role on one project. The primary key
-- (project_id, user_id) is what keeps a user to a single role per project.
INSERT INTO projects.project_roles (project_id, user_id, role, created_at)
VALUES (@project_id, @user_id, @role, @now::timestamptz);

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
