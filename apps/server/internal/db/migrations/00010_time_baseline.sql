-- +goose Up
CREATE SCHEMA time;

-- Every foreign identifier here is opaque: users, projects, billing lines and
-- tasks live in other schemas (docs/module-boundaries.md rule 4).
CREATE TABLE time.entries (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id             uuid          NOT NULL,
    project_id          integer       NOT NULL,
    billing_line_id     integer,
    task_id             integer,
    task_title          varchar(200),
    entry_date          date          NOT NULL,
    hours               numeric(5,2)  NOT NULL,
    start_time          time,
    end_time            time,
    note                varchar(2000),
    billable            boolean       NOT NULL,
    bill_rate           numeric(12,2),
    bill_currency       char(3),
    cost_rate           numeric(12,2),
    cost_currency       char(3),
    rate_source         varchar(20)   NOT NULL,
    status              varchar(20)   NOT NULL DEFAULT 'draft',
    rejection_reason    varchar(1000),
    submitted_at        timestamptz,
    approved_by_user_id uuid,
    approved_at         timestamptz,
    invoiced_at         timestamptz,
    revision            integer       NOT NULL DEFAULT 1,
    created_at          timestamptz   NOT NULL,
    updated_at          timestamptz   NOT NULL
);
CREATE INDEX ix_entries_user_id_entry_date ON time.entries (user_id, entry_date);
CREATE INDEX ix_entries_project_id_entry_date ON time.entries (project_id, entry_date);
CREATE INDEX ix_entries_status_entry_date ON time.entries (status, entry_date);

CREATE TABLE time.person_rates (
    id         integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    user_id    uuid          NOT NULL,
    valid_from date          NOT NULL,
    bill_rate  numeric(12,2),
    cost_rate  numeric(12,2),
    currency   char(3)       NOT NULL,
    created_at timestamptz   NOT NULL,
    updated_at timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_person_rates_user_id_valid_from ON time.person_rates (user_id, valid_from);

CREATE TABLE time.week_submissions (
    user_id      uuid        NOT NULL,
    week_start   date        NOT NULL,
    submitted_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, week_start)
);

CREATE TABLE time.settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);

-- +goose Down
DROP SCHEMA time CASCADE;
