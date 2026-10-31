-- The project template queries (phase 4 delivery C, design M9).

-- name: GetTemplate :one
-- GetTemplate fetches one project template by id.
SELECT * FROM projects.templates WHERE id = @id;

-- name: ListTemplates :many
-- ListTemplates lists all project templates in alphabetical order.
SELECT * FROM projects.templates
ORDER BY name, id;

-- name: InsertTemplate :one
-- InsertTemplate creates a new project template.
INSERT INTO projects.templates (
    name, description, billing_type, milestones, tasks, dependencies,
    created_by_user_id, created_at, updated_at
) VALUES (
    @name, @description, @billing_type, @milestones, @tasks, @dependencies,
    @created_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateTemplate :one
-- UpdateTemplate updates a template's definition.
UPDATE projects.templates SET
    name = @name,
    description = @description,
    billing_type = @billing_type,
    milestones = @milestones,
    tasks = @tasks,
    dependencies = @dependencies,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND revision = @revision
RETURNING *;

-- name: DeleteTemplate :exec
-- DeleteTemplate removes a project template by id.
DELETE FROM projects.templates WHERE id = @id;
