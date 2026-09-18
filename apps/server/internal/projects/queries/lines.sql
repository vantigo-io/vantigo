-- name: ListBillingLines :many
-- ListBillingLines is every billing line of one project, deactivated ones
-- included: a line is deactivated rather than deleted (there is no DELETE),
-- and the rule behind an hour logged last month is still the answer to what
-- that hour cost. The order is the code's, which is what the trackable code
-- is built from and how the frontend lists them.
SELECT * FROM projects.billing_lines
WHERE project_id = @project_id
ORDER BY code, id;

-- name: InsertBillingLine :one
-- InsertBillingLine adds one line to a project. created_at and updated_at are
-- the same instant on creation, supplied by the caller from Deps.Clock();
-- active takes the column default (true). A code already used inside the
-- project raises 23505 on ux_billing_lines_project_id_code, which the handler
-- turns into the `code` field error rather than a 500 — the same way a
-- project's own code is kept unique.
INSERT INTO projects.billing_lines (
    project_id, code, variant_id, pricing_mode, fixed_amount, discount_percent,
    created_at, updated_at
) VALUES (
    @project_id, @code, @variant_id, @pricing_mode, @fixed_amount, @discount_percent,
    @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: GetBillingLine :one
-- GetBillingLine is one line of one project, read before the change's own
-- transaction because validation has to know what the line already says: a
-- body that keeps the variant the line is pinned to is not asking for that
-- variant to still exist in the catalog. It informs the rules only — what
-- the timeline is decided from is the locked read below.
SELECT * FROM projects.billing_lines
WHERE id = @id AND project_id = @project_id;

-- name: LockBillingLine :one
-- LockBillingLine is one line of one project, locked for the rest of the
-- transaction. The change reads it this way because the row as it stood is
-- what decides the timeline: which fields moved, and whether the line was
-- deactivated or reactivated. Scoping by project_id is also the 404 for a
-- line id that exists but belongs to somebody else's project.
SELECT * FROM projects.billing_lines
WHERE id = @id AND project_id = @project_id
FOR UPDATE;

-- name: UpdateBillingLine :one
-- UpdateBillingLine applies one change to a line under that lock. It carries
-- no revision guard: a line is four small fields, and the lock already
-- serialises two managers editing the same one. `active` is nullable here
-- because the request may leave it out, which leaves the line as it stands —
-- coalesce, not a second statement, so one write covers both.
UPDATE projects.billing_lines SET
    code = @code,
    variant_id = @variant_id,
    pricing_mode = @pricing_mode,
    fixed_amount = @fixed_amount,
    discount_percent = @discount_percent,
    active = coalesce(sqlc.narg(active)::boolean, active),
    updated_at = @now::timestamptz
WHERE id = @id AND project_id = @project_id
RETURNING *;

-- name: CountFixedBillingLines :one
-- CountFixedBillingLines is D13's guard on the project update: a 'fixed' line
-- is an amount denominated in the project's currency, so the currency cannot
-- be cleared while one exists. Deactivated lines count too — their amount is
-- still what an hour already logged against them was worth.
--
-- 'fixed' here is the pricingFixed constant (lines_validation.go), the same
-- string the contract's pricingMode enumeration writes.
SELECT count(*) FROM projects.billing_lines
WHERE project_id = @project_id AND pricing_mode = 'fixed';
