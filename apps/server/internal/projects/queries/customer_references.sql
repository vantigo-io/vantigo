-- name: RepointProjectsCustomer :execrows
-- RepointProjectsCustomer is this module's half of a customer merge (customers
-- merge design D1, contracts.CustomerReferenceHolder): every project billed to
-- the absorbed customer bills to the survivor, inside the customers module's
-- own transaction. There is no per-customer uniqueness to collide with —
-- ux_projects_code is global — and ix_projects_customer_id finds the rows. The
-- revision advances as it does for any change to the row, so an edit form
-- opened before the merge answers the stale-revision 409 rather than writing
-- the absorbed customer back.
UPDATE projects.projects
SET customer_id = @into_customer_id::integer,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE customer_id = @from_customer_id::integer;
