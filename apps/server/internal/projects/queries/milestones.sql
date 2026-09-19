-- This file starts as the two reads the project's own guards need (design
-- §3.3), decided against milestone rows before there is any milestone
-- operation to create them through — Task 2 adds milestones' own CRUD here.

-- name: CountNonCancelledMilestones :one
-- CountNonCancelledMilestones is D13's currency guard, extended to
-- milestones: any milestone that is not cancelled carries an amount in the
-- project's currency (a flat one, or a percent of the fixed price, itself in
-- that currency), so the currency cannot be cleared or changed while one
-- exists. A cancelled milestone does not count — it bills nothing.
SELECT count(*) FROM projects.billing_milestones
WHERE project_id = @project_id AND status <> 'cancelled';

-- name: ListOpenPercentMilestoneNames :many
-- ListOpenPercentMilestoneNames is the fixed-price guard's own read: a
-- percent milestone that is still 'planned' or 'ready' depends on the
-- project's fixed price to resolve its amount, so removing that price (or
-- moving the project off fixed-price billing) is refused while any exist.
-- Invoiced and cancelled ones are excluded — an invoiced milestone's amount
-- is already frozen, and a cancelled one bills nothing. The order is
-- position, the project's own manual order, so the names a refusal lists
-- read the way the invoice plan does.
SELECT name FROM projects.billing_milestones
WHERE project_id = @project_id AND percent IS NOT NULL AND status IN ('planned', 'ready')
ORDER BY position, id;
