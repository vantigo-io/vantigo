-- +goose Up
-- A model per subject (virtual employees design V7,
-- docs/superpowers/specs/2026-11-08-virtual-employees-design.md): a task a
-- module declares PerSubject, such as agents.run, can have a model chosen for
-- one subject (one agent) besides the Owner's choice for the task itself. The
-- task's own choice keeps the empty subject, so every existing row is it and
-- the AI models page is unchanged. A subject has no foreign key, as a task has
-- none: it is the declaring module's id.
ALTER TABLE identity.ai_task_models ADD COLUMN subject text NOT NULL DEFAULT '';
ALTER TABLE identity.ai_task_models DROP CONSTRAINT ai_task_models_pkey;
ALTER TABLE identity.ai_task_models ADD PRIMARY KEY (task_key, subject);

-- +goose Down
-- The Down drops the per-subject choices: without them every subject follows
-- its task again.
DELETE FROM identity.ai_task_models WHERE subject <> '';
ALTER TABLE identity.ai_task_models DROP CONSTRAINT ai_task_models_pkey;
ALTER TABLE identity.ai_task_models DROP COLUMN subject;
ALTER TABLE identity.ai_task_models ADD PRIMARY KEY (task_key);
