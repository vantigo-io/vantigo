-- +goose Up
-- A project's work types (work types design D1): a rule defined once per
-- project — "Overtid 50 %", "Helg" — that a time entry may pick, and that
-- multiplies every rate the chain resolves for it, on every billing line of
-- the project and at every step. Projects stores the two percentages and
-- nothing else; Time multiplies them and snapshots them onto the entry.
--
-- numeric(6,2): a percentage greater than zero and at most 1000, with two
-- decimals, validated in Go (validateMultiplier, work_types.go) the way every
-- other decimal here is — no CHECK, house style. 100 is "as the rate says".
-- There is no DELETE, so no ON DELETE either: a type is deactivated, because
-- entries that picked it still name it.
--
-- The name is unique per project without regard to case, on lower(name), so
-- "Overtid" and "overtid" are one type; the handler turns this index's 23505
-- into the 409, as a line's code index is turned into its field error.
CREATE TABLE projects.work_types (
    id                      integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id              integer      NOT NULL REFERENCES projects.projects (id),
    name                    varchar(100) NOT NULL,
    bill_multiplier_percent numeric(6,2) NOT NULL,
    cost_multiplier_percent numeric(6,2) NOT NULL,
    active                  boolean      NOT NULL DEFAULT true,
    created_at              timestamptz  NOT NULL,
    updated_at              timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_work_types_project_id_name ON projects.work_types (project_id, lower(name));

-- +goose Down
DROP TABLE projects.work_types;
