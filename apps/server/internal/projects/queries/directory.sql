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
SELECT id, code, name, customer_id, status, billing_type, currency, default_bill_rate
FROM projects.projects
WHERE id = @id;

-- name: DirectoryProjects :many
-- DirectoryProjects is contracts.ProjectDirectory.Projects' rows: every
-- project in ids, in any status, ordered by code. An id in ids that does not
-- exist simply has no matching row, which is what makes an unknown id
-- "omitted" rather than an error.
SELECT id, code, name, customer_id, status, billing_type, currency, default_bill_rate
FROM projects.projects
WHERE id = ANY(@ids::integer[])
ORDER BY code, id;

-- name: DirectoryProjectByCode :one
-- DirectoryProjectByCode is contracts.ProjectDirectory.ProjectByCode's row.
-- The caller upper-cases code before calling, matching how codes are stored
-- (validateProjectCode); this query does not itself normalize case.
SELECT id, code, name, customer_id, status, billing_type, currency, default_bill_rate
FROM projects.projects
WHERE code = @code;

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
SELECT p.id, p.code, p.name, p.customer_id, p.status, p.billing_type, p.currency, p.default_bill_rate
FROM projects.projects p
JOIN projects.project_roles r ON r.project_id = p.id
WHERE r.user_id = @user_id
ORDER BY p.code, p.id;

-- name: DirectoryProjectsForCustomer :many
-- DirectoryProjectsForCustomer is contracts.ProjectDirectory.ProjectsForCustomer's
-- rows: every project billed to customerID, whatever its status, by id, and
-- no more than the caller's cap (contracts.MaxActualsRequests, passed in so
-- the number lives in one place). Id order, not code order like its
-- neighbours: the LIMIT has to cut somewhere stable, and the oldest projects
-- are the ones a customer page can least afford to lose. ix_projects_customer_id
-- (00008) serves the WHERE; the casts keep both parameters non-null int32.
SELECT id, code, name, customer_id, status, billing_type, currency, default_bill_rate
FROM projects.projects
WHERE customer_id = @customer_id::integer
ORDER BY id
LIMIT @max_projects::integer;

-- name: DirectoryBillingLines :many
-- DirectoryBillingLines is contracts.ProjectDirectory.BillingLines' rows:
-- every billing line on projectID, active and inactive, ordered by code. A
-- caller that wants only the active ones filters the result itself.
SELECT id, project_id, code, variant_id, pricing_mode, fixed_amount, discount_percent, active
FROM projects.billing_lines
WHERE project_id = @project_id
ORDER BY code;

-- name: DirectoryTask :one
-- DirectoryTask is contracts.ProjectDirectory.Task's row. There is no
-- soft-delete flag on projects.tasks, so a deleted task simply has no row,
-- the same as one that never existed.
SELECT id, project_id, title, status, assignee_user_id, due_date
FROM projects.tasks
WHERE id = @id;

-- name: DirectoryOpenTasksForUser :many
-- DirectoryOpenTasksForUser is contracts.ProjectDirectory.OpenTasksForUser's
-- rows: userID's tasks whose status is not 'done', across every project,
-- ordered by due date (nulls last), project ID and position — the same
-- order "my tasks" (design §4.1) wants.
SELECT id, project_id, title, status, assignee_user_id, due_date
FROM projects.tasks
WHERE assignee_user_id = @assignee_user_id AND status <> 'done'
ORDER BY due_date NULLS LAST, project_id, position;

-- name: DirectoryCanLogTime :one
-- DirectoryCanLogTime is contracts.ProjectDirectory.CanLogTime's row: true
-- when projectID is active and userID holds the member or manager role on
-- it, exactly the condition Time will gate logging on.
SELECT EXISTS (
    SELECT 1
    FROM projects.projects p
    JOIN projects.project_roles r ON r.project_id = p.id
    WHERE p.id = @project_id
      AND p.status = 'active'
      AND r.user_id = @user_id
      AND r.role IN ('member', 'manager')
);

-- name: DirectoryWorkType :one
-- DirectoryWorkType is contracts.ProjectDirectory.WorkType's row: one type by
-- id, active or not, whatever project it is on — the consumer compares the
-- project itself (work types design D2).
SELECT id, project_id, name, bill_multiplier_percent, cost_multiplier_percent, active
FROM projects.work_types
WHERE id = @id;

-- name: DirectoryWorkTypes :many
-- DirectoryWorkTypes is contracts.ProjectDirectory.WorkTypes' rows: every type
-- on projectID, in ListWorkTypes' order.
SELECT id, project_id, name, bill_multiplier_percent, cost_multiplier_percent, active
FROM projects.work_types
WHERE project_id = @project_id
ORDER BY active DESC, lower(name), id;
