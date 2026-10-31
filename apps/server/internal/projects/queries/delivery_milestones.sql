-- The delivery milestone queries (phase 4 delivery A, design M1).

-- name: GetDeliveryMilestone :one
-- GetDeliveryMilestone fetches one delivery milestone by id.
SELECT * FROM projects.delivery_milestones WHERE id = @id;

-- name: LockDeliveryMilestone :one
-- LockDeliveryMilestone fetches one delivery milestone held for update.
SELECT * FROM projects.delivery_milestones WHERE id = @id FOR UPDATE;

-- name: GetDeliveryMilestoneWithProgress :one
-- GetDeliveryMilestoneWithProgress fetches one delivery milestone with derived task counts and estimates.
SELECT
    m.*,
    coalesce(count(t.id), 0)::integer AS total_tasks,
    coalesce(count(t.id) FILTER (WHERE t.status = 'done'), 0)::integer AS completed_tasks,
    coalesce(sum(t.estimate_hours), 0)::numeric(10,2) AS estimated_hours_total,
    coalesce(sum(t.estimate_hours) FILTER (WHERE t.status = 'done'), 0)::numeric(10,2) AS estimated_hours_completed,
    count(t.id) FILTER (WHERE t.estimate_hours IS NOT NULL)::integer AS tasks_with_estimates_count
FROM projects.delivery_milestones m
LEFT JOIN projects.tasks t ON t.delivery_milestone_id = m.id
WHERE m.id = @id
GROUP BY m.id;

-- name: ListProjectDeliveryMilestones :many
-- ListProjectDeliveryMilestones lists all delivery milestones of a project with derived task progress.
SELECT
    m.*,
    coalesce(count(t.id), 0)::integer AS total_tasks,
    coalesce(count(t.id) FILTER (WHERE t.status = 'done'), 0)::integer AS completed_tasks,
    coalesce(sum(t.estimate_hours), 0)::numeric(10,2) AS estimated_hours_total,
    coalesce(sum(t.estimate_hours) FILTER (WHERE t.status = 'done'), 0)::numeric(10,2) AS estimated_hours_completed,
    count(t.id) FILTER (WHERE t.estimate_hours IS NOT NULL)::integer AS tasks_with_estimates_count
FROM projects.delivery_milestones m
LEFT JOIN projects.tasks t ON t.delivery_milestone_id = m.id
WHERE m.project_id = @project_id
GROUP BY m.id
ORDER BY (m.status = 'completed'), m.position, m.id;

-- name: MaxDeliveryMilestonePosition :one
-- MaxDeliveryMilestonePosition is the highest position number currently used on the project.
SELECT coalesce(max(position), 0)::integer FROM projects.delivery_milestones
WHERE project_id = @project_id;

-- name: DeliveryMilestoneIDs :many
-- DeliveryMilestoneIDs returns all delivery milestone IDs on a project in position order, locked.
SELECT id FROM projects.delivery_milestones
WHERE project_id = @project_id
ORDER BY position, id
FOR UPDATE;

-- name: RenumberDeliveryMilestones :exec
-- RenumberDeliveryMilestones updates position numbers for delivery milestones in one statement.
UPDATE projects.delivery_milestones m
SET position = v.ord::integer, updated_at = @now::timestamptz
FROM unnest(@ids::integer[]) WITH ORDINALITY AS v(id, ord)
WHERE m.id = v.id AND m.position <> v.ord::integer;

-- name: InsertDeliveryMilestone :one
-- InsertDeliveryMilestone creates one delivery milestone.
INSERT INTO projects.delivery_milestones (
    project_id, name, description, target_date, position,
    created_by_user_id, created_at, updated_at
) VALUES (
    @project_id, @name, @description, @target_date, @position,
    @created_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateDeliveryMilestone :one
-- UpdateDeliveryMilestone updates name, description, and target_date.
UPDATE projects.delivery_milestones SET
    name = @name,
    description = @description,
    target_date = @target_date,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: UpdateDeliveryMilestoneStatus :one
-- UpdateDeliveryMilestoneStatus updates status and completed_at/by stamps.
UPDATE projects.delivery_milestones SET
    status = @status,
    completed_at = @completed_at,
    completed_by_user_id = @completed_by_user_id,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteDeliveryMilestone :execrows
-- DeleteDeliveryMilestone removes one delivery milestone by id.
DELETE FROM projects.delivery_milestones WHERE id = @id;
