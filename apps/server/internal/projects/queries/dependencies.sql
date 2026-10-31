-- The task dependency queries (phase 4 delivery B, design M6–M8).

-- name: GetTaskDependency :one
-- GetTaskDependency fetches one dependency link by id.
SELECT * FROM projects.task_dependencies WHERE id = @id;

-- name: ListTaskDependenciesByProject :many
-- ListTaskDependenciesByProject lists all task dependency links of a project.
SELECT * FROM projects.task_dependencies
WHERE project_id = @project_id
ORDER BY created_at, id;

-- name: FindTaskDependencyByPair :one
-- FindTaskDependencyByPair checks if a link between predecessor and successor already exists.
SELECT * FROM projects.task_dependencies
WHERE predecessor_task_id = @predecessor_task_id AND successor_task_id = @successor_task_id;

-- name: InsertTaskDependency :one
-- InsertTaskDependency creates a new task dependency link.
INSERT INTO projects.task_dependencies (
    project_id, predecessor_task_id, successor_task_id, created_by_user_id, created_at
) VALUES (
    @project_id, @predecessor_task_id, @successor_task_id, @created_by_user_id, @now::timestamptz
)
RETURNING *;

-- name: DeleteTaskDependency :exec
-- DeleteTaskDependency removes one dependency link by id.
DELETE FROM projects.task_dependencies WHERE id = @id;

-- name: ShiftTasksDates :many
-- ShiftTasksDates shifts start_date and due_date by days_delta for the given task ids.
UPDATE projects.tasks SET
    start_date = CASE WHEN start_date IS NOT NULL THEN start_date + @days_delta::integer ELSE NULL END,
    due_date = CASE WHEN due_date IS NOT NULL THEN due_date + @days_delta::integer ELSE NULL END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::integer[])
RETURNING id;
