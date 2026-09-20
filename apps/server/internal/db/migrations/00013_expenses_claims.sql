-- +goose Up
-- The travel claim (design §3.6, decision X4): the container a trip's expenses
-- sit in. Its lines are ordinary rows of expenses.entries carrying its id —
-- they keep their own kind, date, amounts, receipts and billing, and take
-- their owner, their project, their status, their decision and their
-- reimbursement from the claim.
--
-- House style, as in 00012: no CHECK constraints anywhere, and every foreign
-- identifier (the owner, the project) is opaque — it lives in another schema
-- and is read through contracts, never joined to.
CREATE TABLE expenses.claims (
    id                      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id                 uuid          NOT NULL,
    created_by_user_id      uuid          NOT NULL,
    purpose                 varchar(200)  NOT NULL,
    destination             varchar(200),
    -- A trip abroad is priced from the claim's own day rate rather than the
    -- dated table, in the currency the traveller was paid in; both are
    -- required together and refused together (the module's validation).
    abroad                  boolean       NOT NULL DEFAULT false,
    abroad_day_rate         numeric(10,2),
    abroad_currency         char(3),
    -- The trip itself, as wall-clock instants: the per diem day boundaries are
    -- 24-hour periods counted from the departure, not calendar midnights.
    departure_at            timestamptz   NOT NULL,
    return_at               timestamptz   NOT NULL,
    project_id              integer,
    -- The status, decision and reimbursement columns are exactly the ones a
    -- standalone entry carries (§3.1), because a claim is a unit of approval
    -- and of payroll in exactly the same way: one shape, one vocabulary, one
    -- set of queries to write against.
    status                  varchar(20)   NOT NULL DEFAULT 'draft',
    submitted_at            timestamptz,
    decided_at              timestamptz,
    decided_by_user_id      uuid,
    rejection_reason        varchar(1000),
    reimbursed_at           timestamptz,
    reimbursed_by_user_id   uuid,
    reimbursement_reference varchar(100),
    reimbursement_date      date,
    revision                integer       NOT NULL DEFAULT 1,
    created_at              timestamptz   NOT NULL,
    updated_at              timestamptz   NOT NULL
);
-- The list's own index: a person's claims, most recent trip first.
CREATE INDEX ix_claims_user_id_departure_at ON expenses.claims (user_id, departure_at);
-- The project side reads the same shape keyed on the project, as the entries
-- are keyed for the project page's costs.
CREATE INDEX ix_claims_project_id_departure_at ON expenses.claims (project_id, departure_at);
-- The two partial indexes 00012 gave the entries, for the two things a claim
-- is waiting on: an approver, and a payroll run. Each holds only the rows
-- still waiting for somebody to act and shrinks again as they are dealt with.
CREATE INDEX ix_claims_submitted ON expenses.claims (status) WHERE status = 'submitted';
CREATE INDEX ix_claims_reimbursement_waiting ON expenses.claims (user_id, departure_at)
    WHERE status = 'approved' AND reimbursed_at IS NULL;

-- A line belongs to its claim: deleting the claim takes its lines, and the
-- cascade 00012 put on attachments takes their receipts with them. 00012
-- created entries.claim_id and its index without the key, because the table it
-- points at is this migration's.
ALTER TABLE expenses.entries
    ADD CONSTRAINT fk_entries_claim_id FOREIGN KEY (claim_id)
        REFERENCES expenses.claims (id) ON DELETE CASCADE;

-- The per diem columns of design §4. per_diem_type is which kind of day the
-- line is (day_6_12, day_over_12, overnight_hotel, overnight_other); the three
-- flags are the meals somebody else covered; the three percentages are what
-- each of those meals deducted, frozen beside the day rate in `rate` so a
-- later change to the table never moves a line already recorded.
ALTER TABLE expenses.entries
    ADD COLUMN per_diem_type          varchar(30),
    ADD COLUMN breakfast_covered      boolean      NOT NULL DEFAULT false,
    ADD COLUMN lunch_covered          boolean      NOT NULL DEFAULT false,
    ADD COLUMN dinner_covered         boolean      NOT NULL DEFAULT false,
    ADD COLUMN meal_breakfast_percent numeric(5,2),
    ADD COLUMN meal_lunch_percent     numeric(5,2),
    ADD COLUMN meal_dinner_percent    numeric(5,2);

-- One per diem day per claim per date (design §4). Every door that records or
-- moves one already decides this under the claim's own row lock, so the index
-- is the backstop rather than the rule — the same way this module's other three
-- invariants are guarded in Go and again in SQL. It is partial because it is
-- only about per diem days: a trip may hold any number of outlays and mileage
-- lines on one date, and a standalone expense has no claim at all.
CREATE UNIQUE INDEX ux_entries_claim_per_diem_day ON expenses.entries (claim_id, entry_date)
    WHERE kind = 'per_diem';

-- The installation's own business time zone. A travel claim stores two
-- instants, and every date derived from them — the day the period lock judges,
-- the day GET /claims' from/to filter matches, the first and last day a per
-- diem line may fall on, and the day the suggestion proposes — is the calendar
-- day of that instant **here**, in the company's own zone. Without it the only
-- derivation available is UTC, which puts a departure at 00:30 on 1 July in
-- Oslo on 30 June: one day early for an hour or two a day, and wrong at exactly
-- the month boundaries a period lock and a payroll run are about.
--
-- An IANA name (varchar(64) holds every one of them, the longest being 32
-- characters), defaulted to Europe/Oslo because that is whose per diem
-- agreement this module implements. PUT /settings checks a new one against Go's
-- tzdata and then asks this database what it means by the same name — the UTC
-- offset at a winter instant and a summer one, compared with Go's — so a name
-- the two read differently is refused rather than stored and later disagreeing
-- with itself. Knowing the name is not enough: Postgres resolves
-- pg_timezone_abbrevs first, which makes 'CET' a fixed +01:00 to it and the
-- zone with summer time to Go.
ALTER TABLE expenses.settings
    ADD COLUMN time_zone varchar(64) NOT NULL DEFAULT 'Europe/Oslo';

-- The per diem seeds (design §3.4, delivery B), verified on 2026-09-20 against
-- the state's Særavtale om dekning av utgifter til reise og kost innenlands,
-- in force 2026-01-01 to 2027-12-31, §§ 6 and 9. They are ordinary rows an
-- administrator may change or remove; POST /rates/reset puts a removed one
-- back from seededRates in rates.go, which a test holds against these rows.
--
-- The agreement knows one overnight rate, so per_diem_overnight_other is
-- deliberately unseeded: it is for a company's own figure (a boarding house, a
-- barracks), and a day of that type cannot be priced until one exists. The
-- three meal percentages carry no currency — they are percentages of whatever
-- day rate applies.
--
-- ON CONFLICT DO NOTHING because this migration meets databases the product has
-- already been used on. Delivery A shipped POST /rates accepting every one of
-- these kinds, so an administrator preparing for travel claims may already have
-- entered one of them — on 2026-01-01, which is when the agreement took effect
-- and therefore the obvious day to enter. ux_rates_kind_valid_from admits one
-- row per kind and day, so a plain INSERT would abort the migration and leave
-- that tenant's server refusing to start, fixable only by hand in the database.
-- The company's own figure wins, which is the right way round: it was entered
-- on purpose, and POST /rates/reset is the door that puts the shipped one back
-- on request.
INSERT INTO expenses.rates (kind, valid_from, value, currency, source, created_at, updated_at) VALUES
    ('per_diem_6_12',            DATE '2026-01-01',  397.00, 'NOK', 'State rate', now(), now()),
    ('per_diem_over_12',         DATE '2026-01-01',  736.00, 'NOK', 'State rate', now(), now()),
    ('per_diem_overnight_hotel', DATE '2026-01-01', 1012.00, 'NOK', 'State rate', now(), now()),
    ('meal_breakfast_percent',   DATE '2026-01-01',   20.00, NULL,  'State rate', now(), now()),
    ('meal_lunch_percent',       DATE '2026-01-01',   30.00, NULL,  'State rate', now(), now()),
    ('meal_dinner_percent',      DATE '2026-01-01',   50.00, NULL,  'State rate', now(), now())
ON CONFLICT (kind, valid_from) DO NOTHING;

-- +goose Down
-- A claim's lines are the claim's, so they go where the foreign key would have
-- sent them. Their receipts cascade from the entries; the objects those rows
-- name are the operator's to sweep, as they are for any migration that removes
-- rows the API would have removed through its own door.
DELETE FROM expenses.entries WHERE claim_id IS NOT NULL;

-- The one-per-day index goes by hand: every column it is on (claim_id,
-- entry_date, kind) belongs to 00012, so dropping this migration's own columns
-- would leave it behind.
DROP INDEX expenses.ux_entries_claim_per_diem_day;

ALTER TABLE expenses.entries
    DROP COLUMN meal_dinner_percent,
    DROP COLUMN meal_lunch_percent,
    DROP COLUMN meal_breakfast_percent,
    DROP COLUMN dinner_covered,
    DROP COLUMN lunch_covered,
    DROP COLUMN breakfast_covered,
    DROP COLUMN per_diem_type;

ALTER TABLE expenses.settings DROP COLUMN time_zone;

ALTER TABLE expenses.entries DROP CONSTRAINT fk_entries_claim_id;

DROP TABLE expenses.claims;

-- Only the rows this migration seeded, named by kind, day and label, so a rate
-- an administrator entered on another day — or one they relabelled as their
-- own — is left exactly where it is.
--
-- The identity is deliberately (kind, valid_from, source) and not the value:
-- ux_rates_kind_valid_from admits only one row per kind and day, so a company
-- that wants its own figure for 2026-01-01 has to overwrite the shipped row,
-- and a row they edited **but left labelled "State rate"** goes with this
-- delete. Relabelling it as their own is what spares it, which is the same
-- signal POST /rates/reset reads when it decides what to put back.
DELETE FROM expenses.rates
WHERE source = 'State rate'
  AND valid_from = DATE '2026-01-01'
  AND kind IN ('per_diem_6_12', 'per_diem_over_12', 'per_diem_overnight_hotel',
               'meal_breakfast_percent', 'meal_lunch_percent', 'meal_dinner_percent');
