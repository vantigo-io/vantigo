-- +goose Up
-- Staff's absence (staff foundation design S8–S12, §3.5, delivery B): the
-- absence types in the statutory vocabulary with their a-melding mapping, the
-- holiday schemes and the adjustments of the holiday account they drive, the
-- sick-leave cases with their follow-up milestones, and the absences
-- themselves. Delivery B also adds four columns to tables 00042 and 00043
-- created: employments.holiday_scheme_id (00042 left it to B, with the table
-- its key points at), documents.case_id (a health document filed under a
-- case, S15), settings.auto_carry_over_since — the one settings column B
-- adds after all, against 00042's "never to these two": the derived
-- carry-over must not reach back before the day it was turned on (plan
-- reading 9, pre-flight M6) — and people.self_cert_withdrawn_from, the day
-- HR withdrew the right to egenmelding, from which it is refused (Task 5
-- review L1).
--
-- The version is 00047 and 00046 is not this file's: the Invoices delivery
-- planned beside this one claims it, and whichever lands second renumbers
-- (plan "Architecture"). As 00042, there are no CHECK constraints: the
-- module's validation owns the rules and reports them per field. Every rule
-- of overlap, the case join and the account is the module's, judged under the
-- person's lock (design §4 Locking).

-- An absence type (S8, §3.5). kind is what the rules key on — holiday,
-- sick_self, sick_certified, child_sick, leave, time_off, other; unit is days,
-- or hours for a registration of part of one day; amelding_leave is the
-- a-melding's permisjonsOgPermitteringsBeskrivelse it is reported as, or null;
-- visibility hr_only shows the manager "leave" for a type that reveals
-- special-category data. A seed is never deleted: it may be hidden, renamed
-- and re-sorted, nothing else. key, kind and unit never change.
CREATE TABLE staff.absence_types (
    id             bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    key            varchar(40)  NOT NULL,
    name_en        varchar(100) NOT NULL,
    name_nb        varchar(100) NOT NULL,
    kind           varchar(20)  NOT NULL,
    unit           varchar(10)  NOT NULL,
    paid           boolean      NOT NULL,
    draws_holiday  boolean      NOT NULL DEFAULT false,
    allows_percent boolean      NOT NULL DEFAULT false,
    needs_decision boolean      NOT NULL,
    visibility     varchar(10)  NOT NULL DEFAULT 'absent',
    amelding_leave varchar(40),
    seeded         boolean      NOT NULL DEFAULT false,
    hidden         boolean      NOT NULL DEFAULT false,
    sort           integer      NOT NULL,
    revision       integer      NOT NULL DEFAULT 1,
    created_at     timestamptz  NOT NULL,
    updated_at     timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_absence_types_key ON staff.absence_types (key);

-- A holiday scheme (S9, §3.5): the entitlement and its parts in virkedager,
-- Monday to Saturday, as the Holiday Act counts them; the module converts
-- them to working days. Exactly one is the default — the scheme of an
-- employment that names none; the partial unique index holds "at most one"
-- (as ux_business_units_default, 00042) and the module "at least one": the
-- seed is it, the default only moves, and it is never archived. An archived
-- scheme stays named by the employments that name it and leaves the picker;
-- only a scheme nobody names is archived.
CREATE TABLE staff.holiday_schemes (
    id                         bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name                       varchar(100) NOT NULL,
    entitlement_virkedager     smallint     NOT NULL,
    extra_from_60_virkedager   smallint     NOT NULL,
    main_period_virkedager     smallint     NOT NULL DEFAULT 18,
    after_september_virkedager smallint     NOT NULL DEFAULT 6,
    max_carry_over_virkedager  smallint     NOT NULL DEFAULT 12,
    is_default                 boolean      NOT NULL DEFAULT false,
    seeded                     boolean      NOT NULL DEFAULT false,
    archived_at                timestamptz,
    revision                   integer      NOT NULL DEFAULT 1,
    created_at                 timestamptz  NOT NULL,
    updated_at                 timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_holiday_schemes_name_lower ON staff.holiday_schemes (lower(name));
CREATE UNIQUE INDEX ux_holiday_schemes_default ON staff.holiday_schemes (is_default) WHERE is_default;

-- The scheme an employment's holiday follows (§3.2); null is the default.
ALTER TABLE staff.employments ADD COLUMN holiday_scheme_id bigint REFERENCES staff.holiday_schemes (id);
CREATE INDEX ix_employments_holiday_scheme ON staff.employments (holiday_scheme_id);

-- A change to a person's holiday account for a year (S9, §3.5), in working
-- days, signed: a carry-over into year from from_year — agreed (capped by the
-- scheme) or forced (uncapped, with its reason) — or HR's correction. At most
-- one carry-over per person and year it comes from (plan pre-flight I8).
CREATE TABLE staff.holiday_adjustments (
    id         bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id  bigint       NOT NULL REFERENCES staff.people (id),
    year       smallint     NOT NULL,
    days       numeric(5,1) NOT NULL,
    reason     varchar(20)  NOT NULL,
    from_year  smallint,
    note       varchar(500),
    by_user_id uuid         NOT NULL,
    created_at timestamptz  NOT NULL,
    updated_at timestamptz  NOT NULL
);
CREATE INDEX ix_holiday_adjustments_person_year ON staff.holiday_adjustments (person_id, year);
CREATE UNIQUE INDEX ux_holiday_adjustments_carry_over ON staff.holiday_adjustments (person_id, from_year)
    WHERE reason IN ('agreed_carry_over', 'forced_carry_over');

-- A sick-leave case (S10, §3.5): the sickness its egenmelding and sykmelding
-- rows gather under, from its first absent day. last_day is the last day of
-- its rows, stored and recomputed under the person's lock, so "closed" and
-- the case's retention schedule are judged in SQL (plan reading 13). There is
-- no field for a diagnosis. Class E whole: after the erase the case keeps
-- only its id and its two dates.
CREATE TABLE staff.sick_cases (
    id                   bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id            bigint        NOT NULL REFERENCES staff.people (id),
    first_day            date          NOT NULL,
    last_day             date          NOT NULL,
    closed_at            timestamptz,
    accommodation_note   varchar(2000),
    max_benefit_estimate date,
    revision             integer       NOT NULL DEFAULT 1,
    created_at           timestamptz   NOT NULL,
    updated_at           timestamptz   NOT NULL
);
CREATE INDEX ix_sick_cases_person_first_day ON staff.sick_cases (person_id, first_day);

-- A case's follow-up milestones (S10, §3.5), one per kind: the end of the
-- arbeidsgiverperiode (due_date null until the sixteenth absence day is
-- reached), the follow-up plan, the two dialogue meetings and NAV's activity
-- requirement, each marked done or waived with its reason.
CREATE TABLE staff.sick_case_milestones (
    id                bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    case_id           bigint        NOT NULL REFERENCES staff.sick_cases (id),
    kind              varchar(30)   NOT NULL,
    due_date          date,
    done_at           timestamptz,
    done_by_user_id   uuid,
    waived            boolean       NOT NULL DEFAULT false,
    waived_reason     varchar(500),
    waived_by_user_id uuid,
    note              varchar(2000),
    revision          integer       NOT NULL DEFAULT 1,
    created_at        timestamptz   NOT NULL,
    updated_at        timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_sick_case_milestones_case_kind ON staff.sick_case_milestones (case_id, kind);

-- An absence (S8, §3.5): a person, a type, a first and last day — or one day
-- and its hours, for an hour type — a percentage, a status (requested,
-- approved, rejected, cancelled, registered) and the decision. A sick row
-- belongs to a case and carries its degree; it never carries a note. The
-- requested rows are the inbox's, hence the partial index.
CREATE TABLE staff.absences (
    id                   bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id            bigint       NOT NULL REFERENCES staff.people (id),
    type_id              bigint       NOT NULL REFERENCES staff.absence_types (id),
    first_day            date         NOT NULL,
    last_day             date         NOT NULL,
    hours                numeric(4,2),
    percent              numeric(5,2) NOT NULL DEFAULT 100,
    status               varchar(12)  NOT NULL,
    note                 varchar(500),
    case_id              bigint       REFERENCES staff.sick_cases (id),
    degree_percent       numeric(5,2),
    requested_by_user_id uuid         NOT NULL,
    decided_by_user_id   uuid,
    decided_at           timestamptz,
    decision_note        varchar(500),
    self_approved        boolean      NOT NULL DEFAULT false,
    revision             integer      NOT NULL DEFAULT 1,
    created_at           timestamptz  NOT NULL,
    updated_at           timestamptz  NOT NULL
);
CREATE INDEX ix_absences_person_first_day ON staff.absences (person_id, first_day);
CREATE INDEX ix_absences_type ON staff.absences (type_id);
CREATE INDEX ix_absences_case ON staff.absences (case_id);
CREATE INDEX ix_absences_requested ON staff.absences (status) WHERE status = 'requested';

-- The case a health document belongs to (S15), so the case's erase reaches it.
ALTER TABLE staff.documents ADD COLUMN case_id bigint REFERENCES staff.sick_cases (id);
CREATE INDEX ix_documents_case ON staff.documents (case_id);

-- The day auto_carry_over was last turned on (plan pre-flight M6): the
-- derived carry-in reaches no year before it.
ALTER TABLE staff.settings ADD COLUMN auto_carry_over_since date;

-- The day HR withdrew a person's right to egenmelding (ftrl. § 8-27, Task 5
-- review L1): an egenmelding is refused from it until
-- self_cert_withdrawn_until, never for days before the decision. Class E,
-- as the withdrawal is.
ALTER TABLE staff.people ADD COLUMN self_cert_withdrawn_from date;

-- The statutory vocabulary (S8, plan Task 2 and Revision 2). The a-melding
-- descriptions are the kodeverk PermisjonsOgPermitteringsBeskrivelse's six
-- leave descriptions and permittering, in NAV's spellings; the codes
-- velferdspermisjon and the unsplit utdanningspermisjon expired 31.12.2022,
-- so welfare leave — agreed, not statutory — reports
-- andreIkkeLovfestedePermisjoner. The reserved, hidden absent is what the
-- health erase writes in place of a sick or child-sick type (plan reading 12).
INSERT INTO staff.absence_types
    (key, name_en, name_nb, kind, unit, paid, draws_holiday, allows_percent, needs_decision, visibility, amelding_leave, seeded, hidden, sort, created_at, updated_at)
VALUES
    ('ferie', 'Holiday', 'Ferie', 'holiday', 'days', true, true, false, true, 'absent', NULL, true, false, 1, now(), now()),
    ('egenmelding', 'Self-certified sick leave', 'Egenmelding', 'sick_self', 'days', true, false, false, false, 'absent', NULL, true, false, 2, now(), now()),
    ('sykmelding', 'Certified sick leave', 'Sykmelding', 'sick_certified', 'days', true, false, false, false, 'absent', NULL, true, false, 3, now(), now()),
    ('svangerskapskontroll', 'Antenatal check-up', 'Svangerskapskontroll', 'other', 'hours', true, false, false, false, 'hr_only', NULL, true, false, 4, now(), now()),
    ('svangerskapspermisjon', 'Pregnancy leave', 'Svangerskapspermisjon', 'leave', 'days', false, false, false, true, 'hr_only', 'permisjonMedForeldrepenger', true, false, 5, now(), now()),
    ('omsorgspermisjon', 'Leave at the birth', 'Omsorgspermisjon', 'leave', 'days', false, false, true, true, 'absent', 'andreLovfestedePermisjoner', true, false, 6, now(), now()),
    ('foreldrepermisjon', 'Parental leave', 'Foreldrepermisjon', 'leave', 'days', false, false, true, true, 'absent', 'permisjonMedForeldrepenger', true, false, 7, now(), now()),
    ('fodselspermisjon', 'Birth leave', 'Fødselspermisjon', 'leave', 'days', false, false, false, true, 'hr_only', 'permisjonMedForeldrepenger', true, false, 8, now(), now()),
    ('ammefri', 'Nursing time', 'Ammefri', 'leave', 'hours', false, false, false, true, 'absent', 'andreLovfestedePermisjoner', true, false, 9, now(), now()),
    ('sykt_barn', 'Sick child', 'Sykt barn', 'child_sick', 'days', true, false, false, false, 'absent', NULL, true, false, 10, now(), now()),
    ('pleie_naerstaende', 'Care of a close relative', 'Pleie av nærstående', 'leave', 'days', false, false, true, true, 'absent', 'andreLovfestedePermisjoner', true, false, 11, now(), now()),
    ('utdanning_lovfestet', 'Educational leave (statutory)', 'Utdanningspermisjon (lovfestet)', 'leave', 'days', false, false, true, true, 'absent', 'utdanningspermisjonLovfestet', true, false, 12, now(), now()),
    ('utdanning_ikke_lovfestet', 'Educational leave (not statutory)', 'Utdanningspermisjon (ikke lovfestet)', 'leave', 'days', false, false, true, true, 'absent', 'utdanningspermisjonIkkeLovfestet', true, false, 13, now(), now()),
    ('militaertjeneste', 'Military service', 'Militærtjeneste', 'leave', 'days', false, false, false, true, 'absent', 'permisjonVedMilitaertjeneste', true, false, 14, now(), now()),
    ('offentlig_verv', 'Public office', 'Offentlige verv', 'leave', 'days', false, false, true, true, 'absent', 'andreLovfestedePermisjoner', true, false, 15, now(), now()),
    ('religios_hoytid', 'Religious holiday', 'Religiøs høytid', 'leave', 'days', false, false, false, true, 'hr_only', 'andreLovfestedePermisjoner', true, false, 16, now(), now()),
    ('velferdspermisjon', 'Welfare leave', 'Velferdspermisjon', 'leave', 'days', true, false, false, true, 'absent', 'andreIkkeLovfestedePermisjoner', true, false, 17, now(), now()),
    ('permittering', 'Lay-off', 'Permittering', 'leave', 'days', false, false, true, true, 'absent', 'permittering', true, false, 18, now(), now()),
    ('avspasering', 'Time off in lieu', 'Avspasering', 'time_off', 'hours', true, false, false, true, 'absent', NULL, true, false, 19, now(), now()),
    ('helse', 'Doctor or dentist', 'Lege eller tannlege', 'other', 'hours', true, false, false, false, 'absent', NULL, true, false, 20, now(), now()),
    ('annen_lovfestet', 'Other statutory leave', 'Annen lovfestet permisjon', 'leave', 'days', false, false, true, true, 'absent', 'andreLovfestedePermisjoner', true, false, 21, now(), now()),
    ('annen_ikke_lovfestet', 'Other non-statutory leave', 'Annen ikke-lovfestet permisjon', 'leave', 'days', false, false, true, true, 'absent', 'andreIkkeLovfestedePermisjoner', true, false, 22, now(), now()),
    ('absent', 'Absent', 'Fravær', 'other', 'days', false, false, false, false, 'absent', NULL, true, true, 23, now(), now());

-- The statutory scheme (ferieloven § 5, S9): 25 virkedager, 6 more from the
-- year one turns 60, 18 in the main period, 6 for a start after
-- 30 September, 12 agreed carry-over. The default.
INSERT INTO staff.holiday_schemes
    (name, entitlement_virkedager, extra_from_60_virkedager, main_period_virkedager, after_september_virkedager, max_carry_over_virkedager, is_default, seeded, created_at, updated_at)
VALUES ('Statutory (ferieloven)', 25, 6, 18, 6, 12, true, true, now(), now());

-- +goose Down
ALTER TABLE staff.people DROP COLUMN self_cert_withdrawn_from;
ALTER TABLE staff.settings DROP COLUMN auto_carry_over_since;
ALTER TABLE staff.documents DROP COLUMN case_id;
DROP TABLE staff.absences;
DROP TABLE staff.sick_case_milestones;
DROP TABLE staff.sick_cases;
DROP TABLE staff.holiday_adjustments;
ALTER TABLE staff.employments DROP COLUMN holiday_scheme_id;
DROP TABLE staff.holiday_schemes;
DROP TABLE staff.absence_types;
