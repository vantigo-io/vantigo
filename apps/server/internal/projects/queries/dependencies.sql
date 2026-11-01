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
-- ShiftTasksDates shifts start_date and due_date by days_delta for the given
-- task ids. A task with neither date has nothing to shift, so it is left
-- alone: its revision does not move and it is not counted as shifted.
UPDATE projects.tasks SET
    start_date = start_date + @days_delta::integer,
    due_date = due_date + @days_delta::integer,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::integer[])
  AND (start_date IS NOT NULL OR due_date IS NOT NULL)
RETURNING id;
