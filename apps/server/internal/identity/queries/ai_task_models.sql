-- name: ListAiTaskModels :many
-- ListAiTaskModels is every Owner's choice of model per AI task (AI as a
-- platform provider design T12): the resolver's 20-second cache reads it whole,
-- and so does the settings endpoint, which shows each task its row.
SELECT task_key, model, updated_at, updated_by
FROM identity.ai_task_models
ORDER BY task_key;

-- name: GetAiTaskModel :one
-- GetAiTaskModel is one task's choice, or no row. A save reads it after
-- LockAiTaskModel, so its If-Match is compared with the row it replaces.
SELECT task_key, model, updated_at, updated_by
FROM identity.ai_task_models
WHERE task_key = @task_key;

-- name: LockAiTaskModel :exec
-- LockAiTaskModel serialises two saves of one task for the rest of the
-- transaction, also while the task has no row to lock: two Owners choosing for
-- a task that has no choice yet queue, and the second compares its If-Match
-- with the row the first wrote.
SELECT pg_advisory_xact_lock(hashtextextended('identity.ai_task_models/' || @task_key::text, 0));

-- name: SaveAiTaskModel :exec
-- SaveAiTaskModel writes the Owner's choice for a task, replacing a previous
-- one; updated_at moves on, which is the task's new version.
INSERT INTO identity.ai_task_models (task_key, model, updated_at, updated_by)
VALUES (@task_key, @model, @now::timestamptz, @updated_by)
ON CONFLICT (task_key) DO UPDATE
SET model = EXCLUDED.model,
    updated_at = EXCLUDED.updated_at,
    updated_by = EXCLUDED.updated_by;

-- name: CountAiTaskModels :one
-- CountAiTaskModels is how many choices have been written: the import of
-- COMMUNICATIONS_AI_MODEL happens only while it is none, once.
SELECT count(*) FROM identity.ai_task_models;

-- name: ImportAiTaskModel :exec
-- ImportAiTaskModel writes the model the environment named for a task, with no
-- Owner behind it. A task already written is left as it is.
INSERT INTO identity.ai_task_models (task_key, model, updated_at, updated_by)
VALUES (@task_key, @model, @now::timestamptz, NULL)
ON CONFLICT (task_key) DO NOTHING;
