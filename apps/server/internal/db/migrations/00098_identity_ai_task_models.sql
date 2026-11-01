-- +goose Up
-- The Owner's choice of model per AI task (AI as a platform provider design
-- T12, docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md):
-- one row per task an Owner has chosen a model for, keyed by the task's key
-- ("communications.compose-reply"), which the declaring module owns and never
-- changes. A task with no row uses the task's own recommended model if the
-- plan allows it, else the plan's default; both are instance configuration
-- (AI_MODELS), not rows here, so a plan that changes its models changes what a
-- row's choice resolves to without touching the table.
--
-- Identity keeps it because it is the always-mounted module that already keeps
-- the workspace's own settings (system_settings, sso_settings) and the page is
-- an Owner's. There is no foreign key to a task: tasks live in module
-- declarations, not in the database, and a module that is turned off leaves
-- its rows for when it is turned on again. No version column either: a task's
-- If-Match is its row's updated_at in microseconds, and 0 for a task with no
-- row. The model is not checked against the allowed list here (the list is
-- configuration); the resolver and the endpoint do. updated_by names the Owner
-- who chose, null for the import of COMMUNICATIONS_AI_MODEL, with no foreign
-- key as identity's other audit columns have none.
CREATE TABLE identity.ai_task_models (
    task_key   text        PRIMARY KEY,
    model      text        NOT NULL,
    updated_at timestamptz NOT NULL,
    updated_by uuid
);

-- +goose Down
-- The Down drops the choices: a binary from before this migration reads
-- COMMUNICATIONS_AI_MODEL (or its default) again, so an installation rolling
-- back sets it first if it wants a model other than gpt-4o-mini.
DROP TABLE identity.ai_task_models;
