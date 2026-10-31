-- +goose Up
-- Projects Phase 4 Delivery C: Project and task templates (kickoff blueprints with relative offsets).

CREATE TABLE projects.templates (
    id                  integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name                varchar(200) NOT NULL,
    description         varchar(2000),
    billing_type        varchar(30),
    milestones          jsonb NOT NULL DEFAULT '[]'::jsonb,
    tasks               jsonb NOT NULL DEFAULT '[]'::jsonb,
    dependencies        jsonb NOT NULL DEFAULT '[]'::jsonb,
    revision            integer NOT NULL DEFAULT 1,
    created_by_user_id  uuid NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ix_templates_name ON projects.templates (name);

-- +goose Down
DROP TABLE IF EXISTS projects.templates;
