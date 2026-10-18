-- +goose Up
-- Staff's accounts (staff foundation design S22–S27, §3.6, delivery C). The
-- account flag, its dates and the link's worker columns are 00042's already;
-- this file changes two things of 00042's and adds three columns — and, for
-- the absence settings the owner asked for (Task 5b, below), one of 00047's
-- seeds, one reserved seed and two settings columns.
--
-- identity_links.external_id becomes nullable (plan reading 7): the accounts
-- worker writes a preparing link — under the person's lock, before it calls
-- identity — that names no user until identity has created or found one, so
-- the link needs no placeholder. Two links naming no user do not collide on
-- ux_identity_links_provider_external: a unique index admits any number of
-- nulls.
--
-- ux_people_employee_number_upper is rebuilt on upper() under the "C"
-- collation (Task 2a review). upper() under the database's own collation is
-- that collation's — under a Turkish one upper('i') is 'İ', so Staff would
-- admit i-1 beside I-1 while identity, whose key is Go's case mapping
-- (contracts.NormalizeEmployeeNumber), sees one number. Under "C" upper()
-- maps a–z to A–Z and nothing else, which on Staff's alphabet (letters a–z,
-- digits and hyphens, people_validation.go) is Go's mapping exactly. The
-- queries that compare employee numbers use the same expression
-- (queries/people.sql), so they ask the index's own question.
--
-- people.account_revision is the account flag's own revision (review M1): a
-- change of the flag moves it and never people.revision, which a manager, an
-- Approver and a colleague read — so a flag turned on or off is no signal to
-- anyone outside the links group, as a sealed field's write is none.
-- identity_links.synced_employee_number is the employee number the accounts
-- worker last wrote to identity for the link's user (Task 4b): written only
-- after a Prepare or an Update identity accepted, reset with the worker's
-- other columns whenever the link names another user (SetPersonLink), so a
-- person HR renumbered is still known by the number identity holds — and a
-- user who holds an old number of someone else's is not.
-- identity_links.disable_requested_at is when HR's Disable now asked for the
-- link's user to be disabled, until identity has confirmed it (Task 4c review
-- M1): Disable now turns the flag off and commits before it calls identity,
-- so a call that never finished — the client gone, identity down, the link
-- claimed meanwhile — leaves the marker, which the worker finishes (a link
-- whose flag is off is claimed only while it is set) and from which the
-- attention item is raised.
ALTER TABLE staff.identity_links ALTER COLUMN external_id DROP NOT NULL;
ALTER TABLE staff.identity_links ADD COLUMN synced_employee_number varchar(16);
ALTER TABLE staff.identity_links ADD COLUMN disable_requested_at timestamptz;

ALTER TABLE staff.people ADD COLUMN account_revision integer NOT NULL DEFAULT 1;

DROP INDEX staff.ux_people_employee_number_upper;
CREATE UNIQUE INDEX ux_people_employee_number_upper ON staff.people (upper(employee_number COLLATE "C"));

-- B's open questions 11–13, answered by the owner (plan "The owner's answers"
-- 7, Task 5b). The seed ammefri is paid: aml. § 12-8 (2) gives pay for up to
-- one hour a day in the child's first year on a working day of seven hours or
-- more — the law's floor, which the module never lets HR go below
-- (absence_types.go). Two settings columns, against 00042's "never to these
-- two", as 00047 added auto_carry_over_since (plan reading 23):
-- erase_pregnancy_health_types puts the pregnancy and health-appointment
-- rows on the health schedule, erase_sick_case_dates makes a sick-leave
-- case's erase remove its dates and milestones too; both off by default, as
-- built. The reserved absent_hours (Task 5b, beside 00047's absent) is what
-- the pregnancy and health erase writes in place of an hour type —
-- svangerskapskontroll, helse — so a two-hour visit stays two hours and
-- never becomes a day of absence: absent counts in days.
UPDATE staff.absence_types SET paid = true, revision = revision + 1, updated_at = now() WHERE key = 'ammefri' AND seeded;
INSERT INTO staff.absence_types
    (key, name_en, name_nb, kind, unit, paid, draws_holiday, allows_percent, needs_decision, visibility, amelding_leave, seeded, hidden, sort, created_at, updated_at)
VALUES
    ('absent_hours', 'Absent (hours)', 'Fravær (timer)', 'other', 'hours', false, false, false, false, 'absent', NULL, true, true, 24, now(), now());
ALTER TABLE staff.settings ADD COLUMN erase_pregnancy_health_types boolean NOT NULL DEFAULT false;
ALTER TABLE staff.settings ADD COLUMN erase_sick_case_dates boolean NOT NULL DEFAULT false;

-- +goose Down
-- An operator's last resort, never a routine rollback: a link naming no user
-- is a preparing one, which 00042 cannot hold, so it is deleted — the person
-- keeps their flag for the worker to prepare again — and the flag's own
-- revision, the synced employee number and the disable marker go. The two
-- erase settings go, ammefri is unpaid again, and the rows the erase gave
-- absent_hours take absent, 00047's one reserved type, before it is deleted.
ALTER TABLE staff.settings DROP COLUMN erase_sick_case_dates;
ALTER TABLE staff.settings DROP COLUMN erase_pregnancy_health_types;
UPDATE staff.absences SET type_id = (SELECT id FROM staff.absence_types WHERE key = 'absent')
WHERE type_id = (SELECT id FROM staff.absence_types WHERE key = 'absent_hours');
DELETE FROM staff.absence_types WHERE key = 'absent_hours' AND seeded;
UPDATE staff.absence_types SET paid = false, revision = revision + 1, updated_at = now() WHERE key = 'ammefri' AND seeded;
ALTER TABLE staff.identity_links DROP COLUMN disable_requested_at;
ALTER TABLE staff.identity_links DROP COLUMN synced_employee_number;
ALTER TABLE staff.people DROP COLUMN account_revision;
DROP INDEX staff.ux_people_employee_number_upper;
CREATE UNIQUE INDEX ux_people_employee_number_upper ON staff.people (upper(employee_number));

DELETE FROM staff.identity_links WHERE external_id IS NULL OR state = 'preparing';
ALTER TABLE staff.identity_links ALTER COLUMN external_id SET NOT NULL;
