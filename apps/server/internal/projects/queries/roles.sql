-- name: InsertProjectRole :exec
-- InsertProjectRole assigns one user one role on one project. The primary key
-- (project_id, user_id) is what keeps a user to a single role per project.
INSERT INTO projects.project_roles (project_id, user_id, role, created_at)
VALUES (@project_id, @user_id, @role, @now::timestamptz);

-- name: UpsertProjectRole :one
-- UpsertProjectRole adds one user's role on one project, or changes the role
-- they already hold. created_at is left alone on a change: an assignment that
-- moved from member to viewer is the same assignment, not a new one.
--
-- `inserted` is what the write itself did, read off the row's xmax: zero on a
-- row this statement created, non-zero on one it updated. The timeline's
-- role-added / role-changed decision is made from it rather than from a read
-- taken beforehand, because between such a read and this write another
-- request can insert or delete the very row in question — and then the entry
-- would describe something that did not happen.
INSERT INTO projects.project_roles (project_id, user_id, role, created_at)
VALUES (@project_id, @user_id, @role, @now::timestamptz)
ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role
RETURNING user_id, role, created_at, (xmax = 0) AS inserted;

-- name: DeleteProjectRole :execrows
-- DeleteProjectRole takes one user off one project, answering how many rows
-- it removed: one means this transaction is the one that removed it, zero
-- that there was nothing left to remove. A role is removed, never
-- deactivated: unlike a project or a billing line, nothing else in the
-- product holds a reference to it — the timeline keeps the record that it
-- existed.
DELETE FROM projects.project_roles
WHERE project_id = @project_id AND user_id = @user_id;

-- name: LockProjectRole :one
-- LockProjectRole is one user's whole assignment on one project, locked for
-- the rest of the transaction. Every operation whose subject is the
-- assignment itself reads it this way: the role it finds decides what the
-- timeline is told, so nothing may change that role between the read and the
-- write. authorize() uses RoleForUser instead — it needs the role and nothing
-- else, on every request, and takes no lock.
SELECT user_id, role, created_at FROM projects.project_roles
WHERE project_id = @project_id AND user_id = @user_id
FOR UPDATE;

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
