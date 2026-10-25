-- +goose Up
-- Staff's one-to-one (staff competence design S8–S11, §3.3–3.7 and §3.9,
-- delivery B, docs/superpowers/specs/2026-10-25-staff-competence-design.md;
-- the plan, docs/superpowers/plans/2026-10-25-staff-one-to-one.md, task 2):
-- what the person wants — their interests in competences, their weighted
-- preferences and their availability declaration, each written by the person
-- alone as a whole list — the manager's Seen stamp on every such row and on a
-- person's current competence rows, the review stamp that records the two went
-- through the Competence tab together, and the interval after which a review
-- is overdue. Delivery C's profiles are not this file's. As 00042 and 00066,
-- there are no CHECK constraints: the module's validation owns the domain
-- rules (a preference's kind and its value, a weight of 1–3, a weekday of
-- 1–7, a dated row's from_date, to_date on or after from_date, both minutes
-- or neither and the end after the start, an interval of 0–60 months) and
-- reports them per field.

-- The Seen stamp on a person's competence row (S8, §3.3): set by the manager
-- or HR on every current row that has none, so a declaration read but not yet
-- verified is acknowledged. seen_by_user_id is identity's user, opaque here
-- (module-boundaries rule 4).
ALTER TABLE staff.person_competences
    ADD COLUMN seen_at timestamptz,
    ADD COLUMN seen_by_user_id uuid;

-- The review interval (S11, §3.9): a person in employment today whose latest
-- review — or the start of their current unbroken employment run, when later —
-- is more than this many months ago is review-overdue; 0 turns the item off.
ALTER TABLE staff.settings ADD COLUMN competence_review_interval_months smallint NOT NULL DEFAULT 12;

-- A person's interest in a competence (S8, §3.4): one row per person and
-- competence. Its meaning is derived — a competence the person holds is one
-- they want to use more, one they do not one they want to learn. A row the
-- person sends again unchanged keeps its row and its stamp.
CREATE TABLE staff.person_interests (
    id              bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id       bigint      NOT NULL REFERENCES staff.people (id),
    competence_id   bigint      NOT NULL REFERENCES staff.competences (id),
    seen_at         timestamptz,
    seen_by_user_id uuid,
    created_at      timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_person_interests_person_competence ON staff.person_interests (person_id, competence_id);
CREATE INDEX ix_person_interests_competence ON staff.person_interests (competence_id);

-- A person's preference (S9, §3.5): one row per person and kind, its value
-- the kind's canonical object, validated in Go, and its weight 1–3. A changed
-- value or weight clears the stamp.
CREATE TABLE staff.person_preferences (
    id              bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id       bigint      NOT NULL REFERENCES staff.people (id),
    kind            varchar(30) NOT NULL,
    value           jsonb       NOT NULL,
    weight          smallint    NOT NULL,
    seen_at         timestamptz,
    seen_by_user_id uuid,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_person_preferences_person_kind ON staff.person_preferences (person_id, kind);

-- A person's availability declaration (S10, §3.6): available, unavailable or
-- preferred — recurring on a weekday, within optional bounds, or dated from a
-- day (to a day, or that day alone) — the whole day, or from start_minute to
-- end_minute. Data only: nothing in Staff enforces or sums it.
CREATE TABLE staff.availability_declarations (
    id              bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id       bigint      NOT NULL REFERENCES staff.people (id),
    kind            varchar(12) NOT NULL,
    weekday         smallint,
    from_date       date,
    to_date         date,
    start_minute    smallint,
    end_minute      smallint,
    seen_at         timestamptz,
    seen_by_user_id uuid,
    created_at      timestamptz NOT NULL
);
CREATE INDEX ix_availability_declarations_person ON staff.availability_declarations (person_id);

-- A competence review (S11, §3.7): the day the person and whoever may verify
-- their competences went through the tab together. reviewer_user_id is
-- identity's user, opaque here; reviewer_person_id the reviewer's linked
-- person, when they have one, cleared if that person is deleted as created in
-- error — the review itself is the reviewed person's row and stays. No notes
-- and no goals.
CREATE TABLE staff.competence_reviews (
    id                 bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id          bigint      NOT NULL REFERENCES staff.people (id),
    reviewed_on        date        NOT NULL,
    reviewer_user_id   uuid        NOT NULL,
    reviewer_person_id bigint      REFERENCES staff.people (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL
);
-- The latest review per person, for the person's response and the overdue
-- item.
CREATE INDEX ix_competence_reviews_person_reviewed ON staff.competence_reviews (person_id, reviewed_on DESC);

-- +goose Down
DROP TABLE staff.competence_reviews;
DROP TABLE staff.availability_declarations;
DROP TABLE staff.person_preferences;
DROP TABLE staff.person_interests;
ALTER TABLE staff.settings DROP COLUMN competence_review_interval_months;
ALTER TABLE staff.person_competences
    DROP COLUMN seen_by_user_id,
    DROP COLUMN seen_at;
