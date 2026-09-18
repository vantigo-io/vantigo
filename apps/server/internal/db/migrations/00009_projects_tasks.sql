-- +goose Up
-- Task tracking for a project: tasks nested one level deep (a parent must
-- itself be top-level, enforced in Go, not here), a checklist per task and
-- comments on it. Also gives every project a default bill rate, which Time
-- will read through the directory, never the schema. See
-- docs/superpowers/specs/2026-09-18-time-tracking-and-tasks-design.md §3.1.
CREATE TABLE projects.tasks (
    id                 integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id         integer      NOT NULL REFERENCES projects.projects (id),
    parent_task_id     integer      REFERENCES projects.tasks (id) ON DELETE CASCADE,
    title              varchar(200) NOT NULL,
    description        varchar(4000),
    status             varchar(20)  NOT NULL DEFAULT 'todo',
    assignee_user_id   uuid,
    start_date         date,
    due_date           date,
    estimate_hours     numeric(8,2),
    position           integer      NOT NULL,
    completed_at       timestamptz,
    created_by_user_id uuid         NOT NULL,
    revision           integer      NOT NULL DEFAULT 1,
    created_at         timestamptz  NOT NULL,
    updated_at         timestamptz  NOT NULL
);
CREATE INDEX ix_tasks_project_id_parent_task_id_position ON projects.tasks (project_id, parent_task_id, position);
CREATE INDEX ix_tasks_assignee_user_id_open ON projects.tasks (assignee_user_id) WHERE status <> 'done';

CREATE TABLE projects.task_checklist_items (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id    integer      NOT NULL REFERENCES projects.tasks (id) ON DELETE CASCADE,
    text       varchar(500) NOT NULL,
    done       boolean      NOT NULL DEFAULT false,
    position   integer      NOT NULL,
    created_at timestamptz  NOT NULL,
    updated_at timestamptz  NOT NULL
);
CREATE INDEX ix_task_checklist_items_task_id_position ON projects.task_checklist_items (task_id, position);

CREATE TABLE projects.task_comments (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id        integer       NOT NULL REFERENCES projects.tasks (id) ON DELETE CASCADE,
    author_user_id uuid          NOT NULL,
    body           varchar(4000) NOT NULL,
    created_at     timestamptz   NOT NULL,
    edited_at      timestamptz
);
CREATE INDEX ix_task_comments_task_id_created_at ON projects.task_comments (task_id, created_at, id);

-- The default bill rate is in the project's currency and is a financial field
-- (spec D8); Time reads it through the directory, never the schema.
ALTER TABLE projects.projects ADD COLUMN default_bill_rate numeric(12,2);

-- +goose Down
ALTER TABLE projects.projects DROP COLUMN default_bill_rate;
DROP TABLE projects.task_comments;
DROP TABLE projects.task_checklist_items;
DROP TABLE projects.tasks;
