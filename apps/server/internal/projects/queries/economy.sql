-- The economy read's own queries (design §5, delivery B). What a project
-- budgeted and what it billed already have queries of their own — GetProject
-- carries the project's budget, ListBillingLines its lines' budgets and
-- ListProjectMilestones its invoice plan — so this file holds only the figure
-- nothing else asks for. What was actually *logged* is never read here: it
-- lives in another module's schema and reaches Projects through
-- contracts.ProjectActuals alone.

-- name: ProjectTaskEstimateHours :one
-- ProjectTaskEstimateHours is the project's task estimates added up, a
-- secondary planning figure beside the budget (design §2 E3). Every task
-- counts: subtasks as well as top-level ones, and finished ones as well as
-- open ones, because the estimate is what the work was thought to take, not
-- what is left of it. There is no soft delete in this schema — a deleted task
-- is gone, its subtasks with it — so there is nothing to filter out.
--
-- A project none of whose tasks carries an estimate sums to SQL NULL rather
-- than to zero, which is the difference between "nobody estimated anything"
-- and "somebody estimated nothing"; the handler leaves the field out for the
-- first.
SELECT sum(estimate_hours)::numeric AS estimate_hours
FROM projects.tasks
WHERE project_id = @project_id;
