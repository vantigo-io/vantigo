-- +goose Up
-- Projects' schema: projects, the roles users hold on them, the billing
-- lines invoicing will later price, the project timeline and the counter
-- the code suggestion counts from. See
-- docs/superpowers/specs/2026-09-17-projects-design.md §3.
CREATE SCHEMA projects;

-- customer_id, created_by_user_id and every other foreign identifier here is
-- opaque: customers, identity and products live in other schemas, so there is
-- no foreign key to them (docs/module-boundaries.md rule 4).
CREATE TABLE projects.projects (
    id                 integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    code               varchar(20)   NOT NULL,
    name               varchar(200)  NOT NULL,
    description        varchar(4000),
    customer_id        integer,
    status             varchar(20)   NOT NULL DEFAULT 'planned',
    start_date         date,
    end_date           date,
    billing_type       varchar(20)   NOT NULL,
    currency           char(3),
    fixed_price_amount numeric(12,2),
    budget_hours       numeric(10,2),
    budget_amount      numeric(12,2),
    revision           integer       NOT NULL DEFAULT 1,
    created_by_user_id uuid          NOT NULL,
    created_at         timestamptz   NOT NULL,
    updated_at         timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_projects_code ON projects.projects (code);
CREATE INDEX ix_projects_customer_id ON projects.projects (customer_id);
CREATE INDEX ix_projects_status ON projects.projects (status);

CREATE TABLE projects.project_roles (
    project_id integer     NOT NULL REFERENCES projects.projects (id),
    user_id    uuid        NOT NULL,
    role       varchar(20) NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX ix_project_roles_user_id ON projects.project_roles (user_id);

CREATE TABLE projects.billing_lines (
    id               integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id       integer       NOT NULL REFERENCES projects.projects (id),
    code             varchar(10)   NOT NULL,
    variant_id       integer       NOT NULL,
    pricing_mode     varchar(20)   NOT NULL,
    fixed_amount     numeric(12,2),
    discount_percent numeric(5,2),
    active           boolean       NOT NULL DEFAULT true,
    created_at       timestamptz   NOT NULL,
    updated_at       timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_billing_lines_project_id_code ON projects.billing_lines (project_id, code);

CREATE TABLE projects.timeline_entries (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id    integer     NOT NULL REFERENCES projects.projects (id),
    event_type    varchar(50) NOT NULL,
    payload       jsonb       NOT NULL,
    actor_user_id uuid,
    actor_display text        NOT NULL,
    occurred_at   timestamptz NOT NULL
);
CREATE INDEX ix_timeline_entries_project_id_occurred_at ON projects.timeline_entries (project_id, occurred_at DESC, id DESC);

-- One row per counter, e.g. "project_code". Allocation is an upsert
-- (NextCounterValue), not a Postgres SEQUENCE, the same gapless-under-rollback
-- shape the customers module's own counters table has.
CREATE TABLE projects.counters (
    counter_name text   PRIMARY KEY,
    next_value   bigint NOT NULL
);

-- +goose Down
DROP SCHEMA projects CASCADE;
