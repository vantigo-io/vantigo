-- Directory queries back contracts.ProjectDirectory (directory.go), the one
-- sanctioned way another module reads projects' data. Each selects only the
-- columns the contract's DTOs carry, the same minimal-column style
-- customers' own DirectoryCustomer and DirectoryContact use, rather than
-- reusing the handlers' own full-row queries (GetProject, RoleForUser):
-- callers should never notice a wider handler query grew a column the
-- directory was never meant to expose.

-- name: DirectoryProject :one
-- DirectoryProject is contracts.ProjectDirectory.Project's row: a project of
-- any status, cancelled and completed included, since a consumer holding a
-- historical reference (a logged hour, say) must still be able to name it.
SELECT id, code, name, customer_id, status, billing_type
FROM projects.projects
WHERE id = @id;

-- name: DirectoryRoleForUser :one
-- DirectoryRoleForUser is contracts.ProjectDirectory.Role's row. No row means
-- no role, which reaches the caller as pgx.ErrNoRows, mapped to "" rather than
-- an error: "" is not itself a valid role name, so it is never confused with
-- one.
SELECT role FROM projects.project_roles
WHERE project_id = @project_id AND user_id = @user_id;

-- name: DirectoryBillingLine :one
-- DirectoryBillingLine is contracts.ProjectDirectory.BillingLine's row: one
-- line, scoped to the project it is asked about, so a line id that exists but
-- belongs to another project answers no row rather than someone else's line.
-- An inactive line still resolves, so old hours stay priced.
SELECT id, project_id, code, variant_id, pricing_mode, fixed_amount, discount_percent, active
FROM projects.billing_lines
WHERE id = @id AND project_id = @project_id;

-- name: DirectoryProjectsForUser :many
-- DirectoryProjectsForUser is contracts.ProjectDirectory.ProjectsForUser's
-- rows: every project userID holds any role on, whatever its status, ordered
-- by code. project_roles' primary key (project_id, user_id) keeps this to one
-- row per project.
SELECT p.id, p.code, p.name, p.customer_id, p.status, p.billing_type
FROM projects.projects p
JOIN projects.project_roles r ON r.project_id = p.id
WHERE r.user_id = @user_id
ORDER BY p.code, p.id;
