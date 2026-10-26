-- +goose Up
-- Planning, the plan of who works on what (planning design §3, delivery A):
-- the installation's settings, the allocations — a person booked on a project
-- over a period, tentative or confirmed — and the record of every write.
-- Delivery B adds the requests and their requirements and names a request on
-- an allocation; delivery C adds the terms and the wishes. Each in its own
-- migration, none touching these three tables' columns but B's request_id.
--
-- Every foreign identifier is opaque: a person is Staff's, read through
-- contracts.Workforce; a project is Projects', read through
-- contracts.ProjectDirectory; a user is identity's, read through
-- contracts.UserDirectory (docs/src/content/docs/en/contributing/module-boundaries.md
-- rule 4), so no key crosses a schema and nothing of a person or a project is
-- copied here — no name, no code. There are no CHECK constraints anywhere in
-- this schema — the house style of 00012 and 00042 — because the module's own
-- validation owns the domain rules and reports them per field; the one
-- exception is the settings table's one-row index, which states a fact about
-- the table rather than a rule about a value.
CREATE SCHEMA planning;

-- The installation's Planning settings (S20): one row, id 1, written below
-- with the design's defaults. overbooking is warn or refuse (S7);
-- default_load_unit pre-fills the allocation form and nothing else (S2); the
-- unbooked threshold and horizon shape the unbooked list (S13); the two lead
-- times shape the attention items (S22).
CREATE TABLE planning.settings (
    id                         smallint    PRIMARY KEY,
    overbooking                varchar(10) NOT NULL DEFAULT 'warn',
    default_load_unit          varchar(16) NOT NULL DEFAULT 'percent',
    unbooked_threshold_percent smallint    NOT NULL DEFAULT 50,
    unbooked_horizon_weeks     smallint    NOT NULL DEFAULT 6,
    request_lead_days          smallint    NOT NULL DEFAULT 14,
    ending_lead_weeks          smallint    NOT NULL DEFAULT 4,
    revision                   integer     NOT NULL DEFAULT 1,
    updated_at                 timestamptz NOT NULL,
    updated_by_user_id         uuid
);
-- "One row" is the table's rule, as staff.settings states its own: a unique
-- index on a constant admits exactly one row, whoever writes it.
CREATE UNIQUE INDEX ux_settings_single_row ON planning.settings ((true));

INSERT INTO planning.settings (id, updated_at) VALUES (1, now());

-- An allocation (S1–S5): a person on a project from from_date to to_date
-- inclusive, civil dates; the load as typed — percent of the person's position
-- or hours a week, two decimals (S2); tentative or confirmed (S3); the role's
-- title, read by planning:view, and the planners' note, read by
-- planning:manage alone (S5). confirmed_at is the last confirmation, cleared
-- when the row is set back to tentative. The two indexes are the two ways a
-- window is read: one person's rows, one project's rows.
CREATE TABLE planning.allocations (
    id                 bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id          bigint       NOT NULL,
    project_id         integer      NOT NULL,
    from_date          date         NOT NULL,
    to_date            date         NOT NULL,
    load_unit          varchar(16)  NOT NULL,
    load               numeric(5,2) NOT NULL,
    status             varchar(10)  NOT NULL,
    title              varchar(100),
    note               varchar(500),
    confirmed_at       timestamptz,
    revision           integer      NOT NULL DEFAULT 1,
    created_at         timestamptz  NOT NULL,
    created_by_user_id uuid,
    updated_at         timestamptz  NOT NULL,
    updated_by_user_id uuid
);
CREATE INDEX ix_allocations_person_dates ON planning.allocations (person_id, from_date, to_date);
CREATE INDEX ix_allocations_project_dates ON planning.allocations (project_id, from_date, to_date);

-- The record (S23): append-only, one row per write, the entity it is about
-- (an allocation in delivery A; a request or a wish from B and C), the person
-- and the project it touches, the changed fields before and after, and the
-- actor snapshotted as Staff keeps it (00042's staff.events) — the person's
-- name is never written. The row of a deleted allocation stays: the record
-- outlives what it records.
CREATE TABLE planning.events (
    id            bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    entity_kind   varchar(12)  NOT NULL,
    entity_id     bigint       NOT NULL,
    person_id     bigint,
    project_id    integer,
    kind          varchar(32)  NOT NULL,
    payload       jsonb        NOT NULL DEFAULT '{}'::jsonb,
    actor_kind    varchar(20)  NOT NULL,
    actor_user_id uuid,
    actor_display varchar(200),
    occurred_at   timestamptz  NOT NULL
);
CREATE INDEX ix_events_person_occurred ON planning.events (person_id, occurred_at);
CREATE INDEX ix_events_project_occurred ON planning.events (project_id, occurred_at);
CREATE INDEX ix_events_entity ON planning.events (entity_kind, entity_id);

-- +goose Down
DROP SCHEMA planning CASCADE;
