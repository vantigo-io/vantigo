-- name: InsertProject :one
-- InsertProject creates a project row. created_at and updated_at are the same
-- instant on creation, supplied by the caller from Deps.Clock(); status and
-- revision take the column defaults ('planned', 1). A code already taken
-- raises 23505 on ux_projects_code, which the handler turns into the `code`
-- field error rather than a 500 — that is what makes two racing creates safe.
INSERT INTO projects.projects (
    code, name, description, customer_id, start_date, end_date, billing_type,
    currency, fixed_price_amount, budget_hours, budget_amount, default_bill_rate,
    created_by_user_id, created_at, updated_at
) VALUES (
    @code, @name, @description, @customer_id, @start_date, @end_date, @billing_type,
    @currency, @fixed_price_amount, @budget_hours, @budget_amount, @default_bill_rate,
    @created_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: GetProject :one
-- GetProject fetches one project by id. Visibility is decided in Go
-- (authorize.go), never here: an outsider's 404 has to be indistinguishable
-- from an unknown id's, so the row is loaded first and discarded after.
SELECT * FROM projects.projects WHERE id = @id;

-- name: ListProjects :many
-- ListProjects is one page of the projects a caller may see, filtered.
-- Visibility is decided here rather than in Go so that the count below — and
-- every stats query — is over what the caller can actually see (design §5).
-- projects.visible (the baseline migration) is that one predicate: see_all is
-- the caller's view-all/manage-all, and without it only projects they hold a
-- role on match. `mine` is its own EXISTS rather than a second visible() call
-- because it narrows to the caller's own projects even for a caller who has
-- see_all, which is the opposite question.
--
-- search is already ILIKE-escaped by the caller and arrives without its
-- wildcards, which are added here: a '%' or '_' somebody typed is a
-- character, not a pattern. An empty search filters nothing.
SELECT p.* FROM projects.projects p
WHERE projects.visible(p.id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean)
  AND (NOT sqlc.arg(mine)::boolean OR EXISTS (
         SELECT 1 FROM projects.project_roles r WHERE r.project_id = p.id AND r.user_id = sqlc.arg(user_id)))
  AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status))
  AND (sqlc.narg(customer_id)::integer IS NULL OR p.customer_id = sqlc.narg(customer_id))
  AND (sqlc.narg(internal)::boolean IS NULL OR (p.customer_id IS NULL) = sqlc.narg(internal))
  AND (sqlc.arg(search)::text = '' OR p.code ILIKE '%' || sqlc.arg(search) || '%' ESCAPE '\'
                                   OR p.name ILIKE '%' || sqlc.arg(search) || '%' ESCAPE '\')
ORDER BY p.code, p.id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountProjects :one
-- CountProjects is ListProjects' total, under the identical WHERE clause.
-- The two must stay the same predicate: a filter applied to one and not the
-- other gives a page whose rows and whose totalCount disagree, which is a
-- paging bug nobody notices until the last page.
SELECT count(*) FROM projects.projects p
WHERE projects.visible(p.id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean)
  AND (NOT sqlc.arg(mine)::boolean OR EXISTS (
         SELECT 1 FROM projects.project_roles r WHERE r.project_id = p.id AND r.user_id = sqlc.arg(user_id)))
  AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status))
  AND (sqlc.narg(customer_id)::integer IS NULL OR p.customer_id = sqlc.narg(customer_id))
  AND (sqlc.narg(internal)::boolean IS NULL OR (p.customer_id IS NULL) = sqlc.narg(internal))
  AND (sqlc.arg(search)::text = '' OR p.code ILIKE '%' || sqlc.arg(search) || '%' ESCAPE '\'
                                   OR p.name ILIKE '%' || sqlc.arg(search) || '%' ESCAPE '\');

-- name: LockProject :one
-- LockProject is a project row, locked for the rest of the transaction
-- (design §3.3). Every writer that decides something from the project's
-- currency, fixed price or billing type — the project's own update, a
-- billing line's create or change, and every billing milestone write, since
-- a 'fixed' amount, a budget amount and a milestone are all denominated in
-- that currency — takes this lock as its *first* statement and decides
-- against the row it reads back here, never against a row read before the
-- transaction opened.
--
-- FOR NO KEY UPDATE, not FOR UPDATE: it conflicts with itself and with the
-- project UPDATE's own row lock, so every one of those writers still
-- serialises against every other and no guarantee is weaker — but it does
-- not conflict with the FOR KEY SHARE that Postgres takes on this row for
-- every insert that references it. FOR UPDATE would have made an unrelated
-- task, role, comment, line or timeline insert on the same project wait
-- behind a guarded write. The one statement that does change a key here is
-- UpdateProject writing a new code (ux_projects_code): it runs in the
-- transaction that already holds this lock, so Postgres upgrades the lock in
-- place for that one edit — still mutually exclusive, only not any cheaper.
SELECT * FROM projects.projects WHERE id = @id FOR NO KEY UPDATE;

-- name: UpdateProject :one
-- UpdateProject applies one edit, guarded by the revision the caller read
-- (design §3). A revision that has moved on matches no row, which is the
-- handler's 409: the second writer never silently overwrites the first. The
-- code's uniqueness is left to ux_projects_code here as it is on the insert.
UPDATE projects.projects SET
    code = @code,
    name = @name,
    description = @description,
    customer_id = @customer_id,
    start_date = @start_date,
    end_date = @end_date,
    billing_type = @billing_type,
    currency = @currency,
    fixed_price_amount = @fixed_price_amount,
    budget_hours = @budget_hours,
    budget_amount = @budget_amount,
    default_bill_rate = @default_bill_rate,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND revision = @revision
RETURNING *;

-- name: UpdateProjectStatus :one
-- UpdateProjectStatus is D14's own operation, so the change gets its own
-- timeline entry and, later, its own rules. It carries no revision guard:
-- the handler has already read the row it is changing and a status is not a
-- field two people edit against each other. It still bumps the revision, so
-- an update based on a project read before the status changed is stale.
UPDATE projects.projects SET
    status = @status,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: RecentProjectCodesForCustomer :many
-- RecentProjectCodesForCustomer is a customer's 20 most recently created
-- project codes, the input to customerLetters' "does a hand-chosen prefix
-- stick" rule (design §4.2): a customer with existing projects whose codes
-- agree on a prefix other than the one derived from their name keeps that
-- prefix on the next suggestion.
SELECT code FROM projects.projects
WHERE customer_id = @customer_id
ORDER BY created_at DESC, id DESC
LIMIT 20;

-- name: ProjectCodeExists :one
-- ProjectCodeExists reports whether code is already in use by any project.
-- ux_projects_code is what actually enforces uniqueness on create; this is
-- only the code suggestion's own check, so it can skip an already-taken
-- candidate before offering it (design §4.2).
SELECT EXISTS(SELECT 1 FROM projects.projects WHERE code = @code) AS taken;

-- name: ManagersForProjects :many
-- ManagersForProjects is the managers of a whole page of projects in one
-- query, for the list's embedded `managers` — one round trip for the page
-- rather than one per row. The order is the same as ListProjectManagers':
-- oldest assignment first, so a project's creator leads.
SELECT project_id, user_id FROM projects.project_roles
WHERE project_id = ANY(@project_ids::integer[]) AND role = 'manager'
ORDER BY project_id, created_at, user_id;
