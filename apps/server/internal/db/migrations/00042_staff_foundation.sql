-- +goose Up
-- Staff, the workforce register (staff foundation design §3, delivery A1): the
-- people, their employments and dated terms, the company's structure, the
-- self-service link to a user, the history and the settings. staff.people and
-- staff.settings carry every column the phase will ever need — the sealed
-- identifiers, the care-day, account, legal-hold and erase columns included —
-- so the later deliveries (A2, B, C) add tables and columns to other tables,
-- never to these two. employments.holiday_scheme_id is delivery B's, with the
-- table its key points at.
--
-- Every foreign identifier is opaque: a user lives in identity's schema
-- (docs/src/content/docs/en/contributing/module-boundaries.md rule 4) and is
-- read through contracts.UserDirectory, so no key crosses a schema. There are
-- no CHECK constraints anywhere in this schema — the house style of 00012 —
-- because the module's own validation owns the domain rules and reports them
-- per field; the one exception is the settings table's one-row index, which
-- states a fact about the table rather than a rule about a value.
CREATE SCHEMA staff;

-- The juridisk enhet (design S6, §3.4): the legal entity a business unit
-- belongs to, with its organisation number (nine digits, MOD11 — checked by the
-- module). Exactly one row is the default, which a new business unit is put
-- under when the client names none; the partial unique index holds "at most
-- one" and the module holds "at least one" (the first row becomes it, and the
-- default is never archived). Archived rows stay referenced and leave the
-- pickers.
CREATE TABLE staff.legal_entities (
    id          bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name        varchar(200) NOT NULL,
    org_number  char(9),
    is_default  boolean      NOT NULL DEFAULT false,
    archived_at timestamptz,
    revision    integer      NOT NULL DEFAULT 1,
    created_at  timestamptz  NOT NULL,
    updated_at  timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_legal_entities_org_number ON staff.legal_entities (org_number);
CREATE UNIQUE INDEX ux_legal_entities_default ON staff.legal_entities (is_default) WHERE is_default;

-- The virksomhet or underenhet (S6): what the a-melding reports per, with an
-- organisation number of its own, under a legal entity. An employment names
-- one. The default is the one an employment is put in when the request names
-- none (delivery A1's Task 4).
CREATE TABLE staff.business_units (
    id              bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    legal_entity_id bigint       NOT NULL REFERENCES staff.legal_entities (id),
    name            varchar(200) NOT NULL,
    org_number      char(9),
    is_default      boolean      NOT NULL DEFAULT false,
    archived_at     timestamptz,
    revision        integer      NOT NULL DEFAULT 1,
    created_at      timestamptz  NOT NULL,
    updated_at      timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_business_units_org_number ON staff.business_units (org_number);
CREATE UNIQUE INDEX ux_business_units_default ON staff.business_units (is_default) WHERE is_default;
CREATE INDEX ix_business_units_legal_entity ON staff.business_units (legal_entity_id);

-- The people (design S2, §3.1): one row per human the company has a
-- relationship with, stable across hires and accounts. Each column's
-- retention class (S15) is in the design's table; the comments here say only
-- what the table itself cannot.
CREATE TABLE staff.people (
    id                         bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    -- The permanent, short number every export and provider sees (S3). Unique
    -- however it is cased, by the index below, so "emp-7" and "EMP-7" are one.
    employee_number            varchar(16)   NOT NULL,
    given_name                 varchar(100)  NOT NULL,
    family_name                varchar(100)  NOT NULL,
    display_name               varchar(200)  NOT NULL,
    birth_date                 date,
    gender                     varchar(12),
    -- The national identity number, sealed under staff/national-id and found by
    -- its MAC under staff/national-id-index (S4); a duplicate is the unique
    -- constraint's to refuse, without opening anything.
    national_id_kind           varchar(20),
    national_id_sealed         bytea,
    national_id_mac            bytea         UNIQUE,
    national_id_country        char(2),
    -- Stored lower-cased by the module, and unique where set (the index below).
    work_email                 varchar(254),
    phone_work                 varchar(32),
    phone_mobile               varchar(32),
    phone_private              varchar(32),
    private_email              varchar(254),
    street                     varchar(200),
    postal_code                varchar(16),
    city                       varchar(100),
    country                    char(2),
    work_country               char(2)       NOT NULL DEFAULT 'NO',
    preferred_language         varchar(2)    NOT NULL DEFAULT 'nb',
    bank_account_sealed        bytea,
    bank_account_last4         char(4),
    bank_account_kind          varchar(10),
    -- The care-day inputs (S11), delivery B's to expose.
    children_under_12          smallint      NOT NULL DEFAULT 0,
    sole_carer                 boolean       NOT NULL DEFAULT false,
    chronically_ill_children   smallint      NOT NULL DEFAULT 0,
    -- The self-certification withdrawal (S10), delivery B's.
    self_cert_withdrawn_until  date,
    self_cert_withdrawn_reason varchar(500),
    -- The account flag (S22), delivery C's.
    provision_account          boolean       NOT NULL DEFAULT false,
    provision_from             date,
    account_email_kind         varchar(10),
    -- The legal hold and the erase (S15), delivery A2's.
    legal_hold                 boolean       NOT NULL DEFAULT false,
    legal_hold_reason          varchar(500),
    legal_hold_by_user_id      uuid,
    legal_hold_at              timestamptz,
    erased_at                  timestamptz,
    reassessed_at              timestamptz,
    reassessed_by_user_id      uuid,
    notes                      varchar(2000),
    revision                   integer       NOT NULL DEFAULT 1,
    created_at                 timestamptz   NOT NULL,
    updated_at                 timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_people_employee_number_upper ON staff.people (upper(employee_number));
CREATE UNIQUE INDEX ux_people_work_email_lower ON staff.people (lower(work_email)) WHERE work_email IS NOT NULL;

-- A department (S6): a code, a name, a manager who is the default manager of
-- the people whose terms name the department and no manager of their own, an
-- optional parent — cycles refused by the module — a cost centre and a
-- division.
CREATE TABLE staff.departments (
    id                bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    code              varchar(20)  NOT NULL,
    name              varchar(200) NOT NULL,
    manager_person_id bigint       REFERENCES staff.people (id),
    cost_centre       varchar(40),
    division          varchar(100),
    parent_id         bigint       REFERENCES staff.departments (id),
    archived_at       timestamptz,
    revision          integer      NOT NULL DEFAULT 1,
    created_at        timestamptz  NOT NULL,
    updated_at        timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_departments_code_upper ON staff.departments (upper(code));
CREATE INDEX ix_departments_manager ON staff.departments (manager_person_id);
CREATE INDEX ix_departments_parent ON staff.departments (parent_id);

-- The arbeidssted (S6): a named workplace with its address and country.
CREATE TABLE staff.locations (
    id          bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name        varchar(200) NOT NULL,
    street      varchar(200),
    postal_code varchar(16),
    city        varchar(100),
    country     char(2)      NOT NULL DEFAULT 'NO',
    archived_at timestamptz,
    revision    integer      NOT NULL DEFAULT 1,
    created_at  timestamptz  NOT NULL,
    updated_at  timestamptz  NOT NULL
);

-- A person's next of kin (§3.1): at most three, a rule the module holds.
CREATE TABLE staff.next_of_kin (
    id           bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id    bigint       NOT NULL REFERENCES staff.people (id),
    position     smallint     NOT NULL,
    name         varchar(200) NOT NULL,
    relationship varchar(20)  NOT NULL,
    phone        varchar(32),
    note         varchar(500),
    created_at   timestamptz  NOT NULL,
    updated_at   timestamptz  NOT NULL
);
CREATE INDEX ix_next_of_kin_person ON staff.next_of_kin (person_id);

-- The roles a person holds for the company (S6): verneombud, AMU member, HMS
-- responsible, with the dates they were elected for.
CREATE TABLE staff.person_roles (
    id         bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id  bigint      NOT NULL REFERENCES staff.people (id),
    role       varchar(30) NOT NULL,
    from_date  date,
    to_date    date,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX ix_person_roles_person ON staff.person_roles (person_id);

-- One arbeidsforhold (S2, §3.2), the a-melding's unit. employment_number is its
-- arbeidsforholdID, unique however it is cased. holiday_scheme_id is delivery
-- B's.
CREATE TABLE staff.employments (
    id                 bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id          bigint      NOT NULL REFERENCES staff.people (id),
    employment_number  varchar(40) NOT NULL,
    relationship_type  varchar(20) NOT NULL,
    business_unit_id   bigint      NOT NULL REFERENCES staff.business_units (id),
    start_date         date        NOT NULL,
    end_date           date,
    end_reason         varchar(40),
    contract_signed_at date,
    revision           integer     NOT NULL DEFAULT 1,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_employments_number_upper ON staff.employments (upper(employment_number));
CREATE INDEX ix_employments_person_start ON staff.employments (person_id, start_date);
CREATE INDEX ix_employments_end_date ON staff.employments (end_date) WHERE end_date IS NOT NULL;

-- An employment's terms (S5, §3.3): one row per change, in force from
-- valid_from until the next row's, never overwritten. The first row is dated
-- the employment's start; the module holds that and every other rule of §4.
CREATE TABLE staff.employment_terms (
    id                          bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    employment_id               bigint        NOT NULL REFERENCES staff.employments (id),
    valid_from                  date          NOT NULL,
    employment_type             varchar(12)   NOT NULL,
    temporary_ground            varchar(30),
    expected_end_date           date,
    appointment_form            varchar(40)   NOT NULL,
    hired_out_to                varchar(200),
    fte_percent                 numeric(6,2)  NOT NULL,
    working_hours_scheme        varchar(40)   NOT NULL DEFAULT 'not_shift',
    full_time_hours_per_week    numeric(5,2)  NOT NULL,
    occupation_code             char(7),
    title                       varchar(120),
    employee_type               varchar(20),
    remuneration_type           varchar(20),
    pay_components              jsonb         NOT NULL DEFAULT '[]'::jsonb,
    pay_method                  varchar(40),
    payday_text                 varchar(100),
    department_id               bigint        REFERENCES staff.departments (id),
    location_id                 bigint        REFERENCES staff.locations (id),
    manager_person_id           bigint        REFERENCES staff.people (id),
    cost_centre                 varchar(40),
    division                    varchar(100),
    working_time_text           varchar(1000),
    break_minutes               smallint,
    special_hours_text          varchar(1000),
    overtime_rule_text          varchar(1000),
    probation_end_date          date,
    probation_extension_agreed  boolean       NOT NULL DEFAULT false,
    probation_extended_to       date,
    probation_extension_reason  varchar(200),
    notice_employee             smallint,
    notice_employer             smallint,
    notice_unit                 varchar(6),
    tariff_reference            varchar(200),
    holiday_terms_text          varchar(1000),
    competence_development_text varchar(1000),
    social_security_text        varchar(1000),
    seniority_date              date,
    pay_grade                   varchar(20),
    note                        varchar(1000),
    revision                    integer       NOT NULL DEFAULT 1,
    created_at                  timestamptz   NOT NULL,
    updated_at                  timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_employment_terms_valid_from ON staff.employment_terms (employment_id, valid_from);
CREATE INDEX ix_employment_terms_manager ON staff.employment_terms (manager_person_id);
CREATE INDEX ix_employment_terms_department ON staff.employment_terms (department_id);

-- The link between a person and an account (S24, §3.6). In delivery A1 it is
-- the self-service link HR sets (provider 'identity', the user's id as text,
-- state 'linked', managed false), which establishes the Self and Manager
-- audiences and nothing else; delivery C's accounts worker adds the other
-- states and the lease columns' use, and acts only on a link whose user
-- identity reports managed.
CREATE TABLE staff.identity_links (
    id              bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id       bigint       NOT NULL REFERENCES staff.people (id),
    provider        varchar(20)  NOT NULL,
    external_id     varchar(200) NOT NULL,
    state           varchar(20)  NOT NULL,
    managed         boolean      NOT NULL DEFAULT false,
    blocked_reason  varchar(200),
    last_error      varchar(1000),
    attempts        integer      NOT NULL DEFAULT 0,
    next_attempt_at timestamptz,
    lease_id        uuid,
    lease_until     timestamptz,
    last_synced_at  timestamptz,
    last_spec_hash  bytea,
    created_at      timestamptz  NOT NULL,
    updated_at      timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_identity_links_person_provider ON staff.identity_links (person_id, provider);
CREATE UNIQUE INDEX ux_identity_links_provider_external ON staff.identity_links (provider, external_id);
CREATE INDEX ix_identity_links_next_attempt ON staff.identity_links (next_attempt_at);

-- The history (§3.6): append-only, one row per change, the actor snapshotted.
-- person_id is null for an installation-level event (a person deleted as
-- created in error). payload_classes is the retention classes the payload
-- touches, for the erase alone; who may read which field is the module's
-- field-group table, never this column.
CREATE TABLE staff.events (
    id              bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id       bigint       REFERENCES staff.people (id),
    kind            varchar(40)  NOT NULL,
    actor_kind      varchar(20)  NOT NULL,
    actor_user_id   uuid,
    actor_display   varchar(200),
    at              timestamptz  NOT NULL,
    payload         jsonb        NOT NULL DEFAULT '{}'::jsonb,
    payload_classes text[]       NOT NULL DEFAULT '{}'
);
CREATE INDEX ix_events_person_at ON staff.events (person_id, at);

-- The installation's Staff settings (§3.6): one row, id 1, written below. The
-- business time zone is the one "today" and every civil date of this module is
-- taken in; it is validated as Expenses validates its own (00013), by Go and by
-- this database, which must mean the same thing by the name. Delivery A1's
-- contract exposes the zone, the employee number sequence, the pension
-- provider and the insurer; the self-certification, account, retention and
-- threshold columns reach the contract with their deliveries.
CREATE TABLE staff.settings (
    id                             smallint     PRIMARY KEY,
    time_zone                      varchar(64)  NOT NULL DEFAULT 'Europe/Oslo',
    employee_number_prefix         varchar(8)   NOT NULL DEFAULT '',
    next_employee_number           bigint       NOT NULL DEFAULT 1,
    self_cert_policy               varchar(10)  NOT NULL DEFAULT 'minimum',
    self_cert_days_per_occurrence  smallint     NOT NULL DEFAULT 3,
    self_cert_occurrences_per_year smallint     NOT NULL DEFAULT 4,
    self_cert_days_per_year        smallint     NOT NULL DEFAULT 24,
    self_cert_after_months         smallint     NOT NULL DEFAULT 2,
    auto_carry_over                boolean      NOT NULL DEFAULT false,
    create_before_days             smallint     NOT NULL DEFAULT 14,
    delete_external_after_days     smallint,
    retention_grace_days           smallint     NOT NULL DEFAULT 90,
    health_retention_days          smallint     NOT NULL DEFAULT 365,
    pension_provider               varchar(200),
    injury_insurer                 varchar(200),
    thresholds_acknowledged        jsonb        NOT NULL DEFAULT '{}'::jsonb,
    revision                       integer      NOT NULL DEFAULT 1,
    updated_at                     timestamptz  NOT NULL
);
-- "One row" is the table's rule, as expenses.settings states its own: a unique
-- index on a constant admits exactly one row, whoever writes it.
CREATE UNIQUE INDEX ux_settings_single_row ON staff.settings ((true));

INSERT INTO staff.settings (id, updated_at) VALUES (1, now());

-- +goose Down
DROP SCHEMA staff CASCADE;
