-- +goose Up
-- Delivery milestones (phase 4 delivery A, design M1): execution markers
-- with derived task progress, distinct from commercial billing milestones.
-- Tasks reference a delivery milestone via delivery_milestone_id (ON DELETE SET NULL).
CREATE TABLE projects.delivery_milestones (
    id                   integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id           integer      NOT NULL REFERENCES projects.projects (id),
    name                 varchar(200) NOT NULL,
    description          varchar(2000),
    target_date          date,
    status               varchar(20)  NOT NULL DEFAULT 'open',
    position             integer      NOT NULL,
    completed_at         timestamptz,
    completed_by_user_id uuid,
    revision             integer      NOT NULL DEFAULT 1,
    created_by_user_id   uuid         NOT NULL,
    created_at           timestamptz  NOT NULL,
    updated_at           timestamptz  NOT NULL
);
CREATE INDEX ix_delivery_milestones_project_id_position ON projects.delivery_milestones (project_id, position);

ALTER TABLE projects.tasks
    ADD COLUMN delivery_milestone_id integer REFERENCES projects.delivery_milestones (id) ON DELETE SET NULL;
CREATE INDEX ix_tasks_delivery_milestone_id ON projects.tasks (delivery_milestone_id);

-- +goose Down
DROP INDEX IF EXISTS projects.ix_tasks_delivery_milestone_id;
ALTER TABLE projects.tasks DROP COLUMN IF EXISTS delivery_milestone_id;
DROP TABLE IF EXISTS projects.delivery_milestones;
