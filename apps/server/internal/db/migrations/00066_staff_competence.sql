-- +goose Up
-- Staff competence (staff competence design S1–S7, §3.1–3.3, delivery A,
-- docs/superpowers/specs/2026-10-25-staff-competence-design.md; the plan,
-- docs/superpowers/plans/2026-10-25-staff-competence.md, task 2): the
-- competence catalogue — its categories and the competences themselves, the
-- Norwegian certificates seeded — and a person's competences, declared by the
-- person or added by a verifier, verified, renewed as new rows and stamped by
-- the staff-competence worker ahead of expiry. Delivery B's seen stamps and
-- delivery C's profiles are not this file's. As 00042, there are no CHECK
-- constraints: the module's validation owns the domain rules (a kind of
-- skill or certificate, a level of 0–4 exactly when the competence has one, a
-- source of self, manager or hr, a basis of evidence or observed, valid_to on
-- or after valid_from) and reports them per field.

-- A competence category (S1, §3.1): the five seeds and the company's own. A
-- seeded category's key never changes; one no longer wanted is archived, so
-- the competences filed under it keep their name. sort is the next one when
-- a category is added.
CREATE TABLE staff.competence_categories (
    id          bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    key         varchar(40)  NOT NULL,
    name_en     varchar(100) NOT NULL,
    name_nb     varchar(100) NOT NULL,
    seeded      boolean      NOT NULL DEFAULT false,
    archived_at timestamptz,
    sort        integer      NOT NULL,
    revision    integer      NOT NULL DEFAULT 1,
    created_at  timestamptz  NOT NULL,
    updated_at  timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_competence_categories_key ON staff.competence_categories (key);

INSERT INTO staff.competence_categories (key, name_en, name_nb, seeded, sort, created_at, updated_at)
VALUES
    ('fag',        'Trade',       'Fag',        true, 1, now(), now()),
    ('sertifikat', 'Certificate', 'Sertifikat', true, 2, now(), now()),
    ('sprak',      'Language',    'Språk',      true, 3, now(), now()),
    ('verktoy',    'Tool',        'Verktøy',    true, 4, now(), now()),
    ('domene',     'Domain',      'Domene',     true, 5, now(), now());

-- A competence (S1, S2, S7, §3.2): kind is skill — a proficiency, a level of
-- 0–4 when has_level, never a validity — or certificate — issued by someone,
-- valid for validity_months or without end, reminded reminder_lead_days
-- ahead. issuer_hint is the issuer the law names and basis the regulation it
-- rests on, for the seeds. A seed's key, kind, has_level and validity_months
-- never change; its names, lead, issuer hint and hidden flag do. A hidden or
-- archived competence is offered to no new row and keeps every row naming it.
CREATE TABLE staff.competences (
    id                 bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    category_id        bigint       NOT NULL REFERENCES staff.competence_categories (id),
    key                varchar(60)  NOT NULL,
    name_en            varchar(100) NOT NULL,
    name_nb            varchar(100) NOT NULL,
    kind               varchar(12)  NOT NULL,
    has_level          boolean      NOT NULL,
    validity_months    smallint,
    reminder_lead_days smallint,
    issuer_hint        varchar(200),
    basis              varchar(200),
    seeded             boolean      NOT NULL DEFAULT false,
    hidden             boolean      NOT NULL DEFAULT false,
    archived_at        timestamptz,
    sort               integer      NOT NULL,
    revision           integer      NOT NULL DEFAULT 1,
    created_at         timestamptz  NOT NULL,
    updated_at         timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_competences_key ON staff.competences (key);
CREATE INDEX ix_competences_category ON staff.competences (category_id);

-- The Norwegian certificates (§3.2), under sertifikat, without a level; the
-- lead by S7's rule — 30 days for a validity of up to a year, 60 up to two,
-- 180 beyond.
INSERT INTO staff.competences (category_id, key, name_en, name_nb, kind, has_level, validity_months, reminder_lead_days,
                               issuer_hint, basis, seeded, sort, created_at, updated_at)
VALUES
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'fagbrev', 'Trade certificate', 'Fagbrev eller svennebrev', 'certificate', false,
     NULL, NULL, 'fylkeskommunen', 'opplæringslova', true, 1, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'fse', 'FSE training with first aid', 'FSE-opplæring med førstehjelp', 'certificate', false,
     12, 30, 'arbeidsgiver eller kursleverandør', 'FSE § 7', true, 2, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'hms-kort', 'HMS card', 'HMS-kort', 'certificate', false,
     24, 60, 'Arbeidstilsynets kortutsteder', 'FOR-2007-03-30-366', true, 3, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'truck', 'Forklift, certified training', 'Truckfører, sertifisert opplæring', 'certificate', false,
     NULL, NULL, NULL, 'fua. § 10-3', true, 4, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'kran', 'Crane, certified training', 'Kranfører, sertifisert opplæring', 'certificate', false,
     NULL, NULL, NULL, 'fua. § 10-3', true, 5, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'masseforflytning', 'Earth-moving machinery, certified training', 'Masseforflytningsmaskiner, sertifisert opplæring', 'certificate', false,
     NULL, NULL, NULL, 'fua. § 10-3', true, 6, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'dokumentert-sikkerhetsopplaering', 'Documented safety training', 'Dokumentert sikkerhetsopplæring', 'certificate', false,
     NULL, NULL, NULL, 'fua. §§ 10-1, 10-2', true, 7, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'stillas-bruker', 'Scaffolding user', 'Stillasbruker', 'certificate', false,
     NULL, NULL, NULL, 'fua. § 17-5', true, 8, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'stillas-bygger', 'Scaffolding erector', 'Stillasbygger', 'certificate', false,
     NULL, NULL, NULL, 'fua. §§ 17-2–17-4', true, 9, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'forerkort-b', 'Driving licence B', 'Førerkort klasse B', 'certificate', false,
     180, 180, 'Statens vegvesen', 'førerkortforskriften § 4-1', true, 10, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'forerkort-c', 'Driving licence C', 'Førerkort klasse C', 'certificate', false,
     60, 180, 'Statens vegvesen', 'førerkortforskriften § 4-1', true, 11, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'forerkort-d', 'Driving licence D', 'Førerkort klasse D', 'certificate', false,
     60, 180, 'Statens vegvesen', 'førerkortforskriften § 4-1', true, 12, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'yrkessjaforkompetanse', 'Professional driver competence (code 95)', 'Yrkessjåførkompetanse (kode 95)', 'certificate', false,
     60, 180, 'Statens vegvesen', 'yrkessjåførforskriften', true, 13, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'varme-arbeider', 'Hot work', 'Varme arbeider', 'certificate', false,
     60, 180, 'Norsk brannvernforening', 'insurers'' sikkerhetsforskrift', true, 14, now(), now()),
    ((SELECT id FROM staff.competence_categories WHERE key = 'sertifikat'), 'arbeidsvarsling', 'Work zone signage', 'Arbeidsvarsling', 'certificate', false,
     60, 180, NULL, 'Statens vegvesen N301', true, 15, now(), now());

-- A person's competence (S3–S7, §3.3): one current row per person and
-- competence — superseded_by_id is null — declared until verified_at is set.
-- valid_from defaults to issued_on, else the day the row was entered; a
-- renewal writes a new row and points the old one at it, leaving its dates and
-- evidence as they were. The evidence is a document in the person's archive,
-- the reference cleared when HR deletes it. source says who wrote the row:
-- self, manager or hr. verified_by_user_id is identity's user, opaque here
-- (module-boundaries rule 4). The two notified stamps are the worker's, set
-- once per row before it mails (S7).
CREATE TABLE staff.person_competences (
    id                   bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id            bigint       NOT NULL REFERENCES staff.people (id),
    competence_id        bigint       NOT NULL REFERENCES staff.competences (id),
    level                smallint,
    issued_on            date,
    valid_from           date         NOT NULL,
    valid_to             date,
    issuer               varchar(200),
    reference            varchar(100),
    evidence_document_id bigint       REFERENCES staff.documents (id) ON DELETE SET NULL,
    source               varchar(10)  NOT NULL,
    verified_at          timestamptz,
    verified_by_user_id  uuid,
    verified_basis       varchar(10),
    verified_note        varchar(200),
    superseded_by_id     bigint       REFERENCES staff.person_competences (id),
    expiring_notified_at timestamptz,
    expired_notified_at  timestamptz,
    revision             integer      NOT NULL DEFAULT 1,
    created_at           timestamptz  NOT NULL,
    updated_at           timestamptz  NOT NULL
);
-- At most one current row per person and competence (S3).
CREATE UNIQUE INDEX ux_person_competences_current ON staff.person_competences (person_id, competence_id)
    WHERE superseded_by_id IS NULL;
CREATE INDEX ix_person_competences_person ON staff.person_competences (person_id);
CREATE INDEX ix_person_competences_competence ON staff.person_competences (competence_id);
-- The worker and the attention items read the current rows that expire (S7).
CREATE INDEX ix_person_competences_valid_to ON staff.person_competences (valid_to)
    WHERE valid_to IS NOT NULL AND superseded_by_id IS NULL;

-- +goose Down
DROP TABLE staff.person_competences;
DROP TABLE staff.competences;
DROP TABLE staff.competence_categories;
