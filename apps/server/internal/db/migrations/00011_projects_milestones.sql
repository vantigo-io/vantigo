-- +goose Up
-- Billing milestones (delivery A of the project economy, design §3.2): the
-- invoice plan a fixed-price or milestone-billed project is read off, each
-- one either a flat amount or a percentage of the project's fixed price. The
-- project FK has no ON DELETE clause, the same as tasks' own (00009): a
-- project is never actually deleted, so nothing here has to decide what
-- happens when one is.
--
-- No CHECK constraints (house style, see 00009): "exactly one of amount or
-- percent", "percent only while the project has a fixed price", the status
-- moves and everything else in design §3.2 are Go's rules, not the schema's.
CREATE TABLE projects.billing_milestones (
    id                  integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id          integer      NOT NULL REFERENCES projects.projects (id),
    name                varchar(200) NOT NULL,
    description         varchar(2000),
    planned_date        date,
    amount              numeric(12,2),
    percent             numeric(5,2),
    status              varchar(20)  NOT NULL DEFAULT 'planned',
    position            integer      NOT NULL,
    ready_at            timestamptz,
    ready_by_user_id    uuid,
    invoiced_at         timestamptz,
    invoiced_by_user_id uuid,
    invoice_reference   varchar(100),
    invoice_date        date,
    invoiced_amount     numeric(12,2),
    ever_moved          boolean      NOT NULL DEFAULT false,
    revision            integer      NOT NULL DEFAULT 1,
    created_by_user_id  uuid         NOT NULL,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL
);
CREATE INDEX ix_billing_milestones_project_id_position ON projects.billing_milestones (project_id, position);

-- The alert and portfolio reads (a later task's) both ask "which open
-- milestones have a planned date": a partial index over the two open
-- statuses is what they run against, and it carries no rows for an invoiced
-- or cancelled milestone, which neither read has any use for.
CREATE INDEX ix_billing_milestones_project_id_planned_date_open
    ON projects.billing_milestones (project_id, planned_date)
    WHERE status IN ('planned', 'ready');

-- A line's own optional budget (design §3.1): planning hours and a planning
-- amount in the project's currency, both validated in Go like the project's
-- own budget is — greater than zero when set, and the amount requires a
-- currency.
ALTER TABLE projects.billing_lines ADD COLUMN budget_hours numeric(10,2), ADD COLUMN budget_amount numeric(12,2);

-- +goose Down
ALTER TABLE projects.billing_lines DROP COLUMN budget_hours, DROP COLUMN budget_amount;
DROP TABLE projects.billing_milestones;
