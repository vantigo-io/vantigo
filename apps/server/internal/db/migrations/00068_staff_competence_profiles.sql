-- +goose Up
-- Staff's competence profiles (staff competence design S12–S14 and §3.8,
-- delivery C, docs/superpowers/specs/2026-10-25-staff-competence-design.md;
-- the plan, docs/superpowers/plans/2026-10-25-staff-competence-profiles.md,
-- task 2): what a position needs — a named list of competences, each with a
-- minimum level and whether it must be valid — assigned to a person by HR or
-- made a department's default, from which the gap list, the competenceGap item
-- and the matrix are computed on read. Nothing about a gap is stored. As 00042,
-- 00066 and 00067, there are no CHECK constraints (amendment 19): the module's
-- validation owns the domain rules (a key of [a-z0-9-], at most 50 items, one
-- item per competence, a minimum level of 0–4 only on a competence with a
-- level, an unarchived profile on a new assignment or a department default)
-- and reports them per field.

-- A competence profile (S12, §3.8): a catalogue row about no person. Its key
-- never changes; one no longer wanted is archived, which keeps every
-- assignment and default naming it while it reaches nobody.
CREATE TABLE staff.competence_profiles (
    id          bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    key         varchar(40)  NOT NULL,
    name_en     varchar(100) NOT NULL,
    name_nb     varchar(100) NOT NULL,
    archived_at timestamptz,
    revision    integer      NOT NULL DEFAULT 1,
    created_at  timestamptz  NOT NULL,
    updated_at  timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_competence_profiles_key ON staff.competence_profiles (key);

-- A profile's item (S12, §3.8): one competence the profile asks for, the
-- level it must reach at least when it has one, and whether the person's row
-- must be valid; replaced whole with the profile under its revision.
CREATE TABLE staff.competence_profile_items (
    id            bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    profile_id    bigint      NOT NULL REFERENCES staff.competence_profiles (id),
    competence_id bigint      NOT NULL REFERENCES staff.competences (id),
    min_level     smallint,
    must_be_valid boolean     NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_competence_profile_items_profile_competence ON staff.competence_profile_items (profile_id, competence_id);
-- The items naming a competence, for competence_in_use.
CREATE INDEX ix_competence_profile_items_competence ON staff.competence_profile_items (competence_id);

-- A profile assigned to a person (S12, §3.8): one row per person and profile,
-- written by HR as the whole set. No cascade: a person created in error is
-- deleted with their rows, and the erase deletes them.
CREATE TABLE staff.person_profiles (
    id         bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id  bigint      NOT NULL REFERENCES staff.people (id),
    profile_id bigint      NOT NULL REFERENCES staff.competence_profiles (id),
    created_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_person_profiles_person_profile ON staff.person_profiles (person_id, profile_id);
CREATE INDEX ix_person_profiles_profile ON staff.person_profiles (profile_id);

-- A department's default profile (S12, §3.8): every person whose terms in
-- force today name the department has it, unless it is archived.
ALTER TABLE staff.departments ADD COLUMN competence_profile_id bigint REFERENCES staff.competence_profiles (id);
CREATE INDEX ix_departments_competence_profile ON staff.departments (competence_profile_id);

-- +goose Down
DROP INDEX staff.ix_departments_competence_profile;
ALTER TABLE staff.departments DROP COLUMN competence_profile_id;
DROP TABLE staff.person_profiles;
DROP TABLE staff.competence_profile_items;
DROP TABLE staff.competence_profiles;
