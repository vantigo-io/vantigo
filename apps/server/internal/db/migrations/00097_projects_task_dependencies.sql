-- +goose Up
-- projects.task_dependencies stores finish-to-start (FS) dependencies between tasks.
CREATE TABLE projects.task_dependencies (
    id                  integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id          integer NOT NULL REFERENCES projects.projects (id) ON DELETE CASCADE,
    predecessor_task_id integer NOT NULL REFERENCES projects.tasks (id) ON DELETE CASCADE,
    successor_task_id   integer NOT NULL REFERENCES projects.tasks (id) ON DELETE CASCADE,
    created_by_user_id  uuid    NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_task_dependencies_pair UNIQUE (predecessor_task_id, successor_task_id),
    CONSTRAINT chk_task_dependencies_distinct CHECK (predecessor_task_id <> successor_task_id)
);

CREATE INDEX ix_task_dependencies_project_id ON projects.task_dependencies (project_id);
CREATE INDEX ix_task_dependencies_predecessor ON projects.task_dependencies (predecessor_task_id);
CREATE INDEX ix_task_dependencies_successor ON projects.task_dependencies (successor_task_id);

-- +goose Down
DROP TABLE IF EXISTS projects.task_dependencies;
