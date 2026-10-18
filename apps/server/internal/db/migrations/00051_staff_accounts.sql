-- +goose Up
-- Staff's accounts (staff foundation design S22–S27, §3.6, delivery C). The
-- account flag, its dates and the link's worker columns are 00042's already;
-- this file changes two things of 00042's and adds two columns.
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
ALTER TABLE staff.identity_links ALTER COLUMN external_id DROP NOT NULL;
ALTER TABLE staff.identity_links ADD COLUMN synced_employee_number varchar(16);

ALTER TABLE staff.people ADD COLUMN account_revision integer NOT NULL DEFAULT 1;

DROP INDEX staff.ux_people_employee_number_upper;
CREATE UNIQUE INDEX ux_people_employee_number_upper ON staff.people (upper(employee_number COLLATE "C"));

-- +goose Down
-- An operator's last resort, never a routine rollback: a link naming no user
-- is a preparing one, which 00042 cannot hold, so it is deleted — the person
-- keeps their flag for the worker to prepare again — and the flag's own
-- revision and the synced employee number go.
ALTER TABLE staff.identity_links DROP COLUMN synced_employee_number;
ALTER TABLE staff.people DROP COLUMN account_revision;
DROP INDEX staff.ux_people_employee_number_upper;
CREATE UNIQUE INDEX ux_people_employee_number_upper ON staff.people (upper(employee_number));

DELETE FROM staff.identity_links WHERE external_id IS NULL OR state = 'preparing';
ALTER TABLE staff.identity_links ALTER COLUMN external_id SET NOT NULL;
