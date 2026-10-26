-- +goose Up
-- Planning delivery C (planning design §3.3, §3.5, S14, S21): the terms an
-- allocation that is also a contract with the customer carries, and the
-- wishes a person files without a word of free text. No CHECK constraints,
-- as 00072 explains: the module's validation owns the rules (the notice's
-- count and unit both or neither, the handover's 0 to 90 days, the
-- non-compete's until date required when any customer id is given, the
-- wish's project required for off and absent for new) and reports them per
-- field. Every foreign id is opaque: a customer is Customers', read through
-- contracts.CustomerDirectory; a person is Staff's; a project is Projects'.

-- The terms (S14): one row to an allocation, the planners' to read and
-- write, gone with the row. The two dates the module derives from them — the
-- earliest release and the handover start (S15) — are computed on read and
-- never stored. Terms may be set on a tentative row and bind once the row is
-- confirmed (reading 8).
CREATE TABLE planning.allocation_terms (
    allocation_id              bigint       PRIMARY KEY REFERENCES planning.allocations (id) ON DELETE CASCADE,
    notice_count               smallint,
    notice_unit                varchar(6),
    notice_given_on            date,
    minimum_until              date,
    replacement_needs_approval boolean      NOT NULL DEFAULT false,
    handover_days              smallint,
    non_compete_until          date,
    non_compete_customer_ids   integer[]    NOT NULL DEFAULT '{}',
    reference                  varchar(200),
    revision                   integer      NOT NULL DEFAULT 1,
    created_at                 timestamptz  NOT NULL,
    created_by_user_id         uuid,
    updated_at                 timestamptz  NOT NULL,
    updated_by_user_id         uuid
);

-- A wish (S21): the person's voice, a kind — off a named project, or
-- something new — and a date, nothing more. Resolved and withdrawn wishes
-- are deleted, so the table holds open wishes only; the record keeps the
-- kind, the project id and the date. One off wish a person on one project.
CREATE TABLE planning.wishes (
    id              bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id       bigint      NOT NULL,
    kind            varchar(10) NOT NULL,
    project_id      integer,
    from_date       date        NOT NULL,
    created_at      timestamptz NOT NULL,
    seen_at         timestamptz,
    seen_by_user_id uuid
);
CREATE INDEX ix_wishes_person ON planning.wishes (person_id, from_date);
CREATE UNIQUE INDEX ux_wishes_person_project_off
    ON planning.wishes (person_id, project_id) WHERE kind = 'off';

-- +goose Down
DROP TABLE planning.wishes;
DROP TABLE planning.allocation_terms;
