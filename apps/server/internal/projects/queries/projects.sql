-- name: InsertProject :one
-- InsertProject creates a project row. created_at and updated_at are the same
-- instant on creation, supplied by the caller from Deps.Clock(); status and
-- revision take the column defaults ('planned', 1). A code already taken
-- raises 23505 on ux_projects_code, which the handler turns into the `code`
-- field error rather than a 500 — that is what makes two racing creates safe.
INSERT INTO projects.projects (
    code, name, description, customer_id, start_date, end_date, billing_type,
    currency, fixed_price_amount, budget_hours, budget_amount,
    created_by_user_id, created_at, updated_at
) VALUES (
    @code, @name, @description, @customer_id, @start_date, @end_date, @billing_type,
    @currency, @fixed_price_amount, @budget_hours, @budget_amount,
    @created_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: GetProject :one
-- GetProject fetches one project by id. Visibility is decided in Go
-- (authorize.go), never here: an outsider's 404 has to be indistinguishable
-- from an unknown id's, so the row is loaded first and discarded after.
SELECT * FROM projects.projects WHERE id = @id;
