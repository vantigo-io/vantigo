-- +goose Up
-- The account kind, per person, and the provider link (Entra design D5, D7
-- and D8, docs/superpowers/specs/2026-10-19-entra-design.md; delivery B,
-- docs/superpowers/plans/2026-10-20-staff-provisioner-slot.md).
--
-- people.account_kind is who holds a person's account authority: local
-- (identity holds the credentials, Staff creates and invites), external (an
-- identity provider owns the account and an Owner adopts the user it
-- creates) or entra (a provider module in the IdentityProvisioner slot holds
-- it, which Staff drives and whose account it announces to identity, so no
-- Owner adopts it). It replaces the installation-wide authority mode in
-- Staff's decisions, and travels on every call to identity.
-- settings.default_account_kind is the kind a new person takes. Both are
-- 'local' here: a migration may not read identity's schema
-- (internal/db/schema_test.go), so the seed from the recorded mode (local →
-- local, federated → external) runs in Staff, once, under the settings
-- lock, and account_kind_seeded records that it has (plan reading 4). As
-- 00042, no CHECK holds the kinds to their three values: the module's
-- validation owns the rule (contracts.ValidAccountKind) and reports it per
-- field.
--
-- settings.directory_fields is which of the directory profile's switchable
-- fields a provider writes (D8), each true or false, seeded with the
-- defaults; a key absent reads as off. settings.provider_status is what the
-- accounts worker last saw of each provider — { "<key>": { "status",
-- "reason", "checkedAt" } } — from which the installation's attention items
-- are judged without asking the provider at read time.
--
-- A provider link is a second row of staff.identity_links per person, its
-- provider the provider's key ('entra') beside 'identity' — the
-- (person_id, provider) and (provider, external_id) indexes already admit it
-- (S reading 12) — and reuses the row's state, external_id, blocked_reason,
-- last_spec_hash and the worker's columns. The identity link stays the
-- claim. It gains the UPN and the address the provider answered
-- (external_upn, external_email), when Staff announced the account
-- (announced_at), when the provider issued a pass not yet revealed
-- (pass_issued_at — never the pass itself), and the facts the provider
-- reported (reported_codes, codes only, never an address).
ALTER TABLE staff.people ADD COLUMN account_kind varchar(10) NOT NULL DEFAULT 'local';

ALTER TABLE staff.settings
    ADD COLUMN default_account_kind varchar(10) NOT NULL DEFAULT 'local',
    ADD COLUMN account_kind_seeded boolean NOT NULL DEFAULT false,
    ADD COLUMN directory_fields jsonb NOT NULL DEFAULT '{"employeeHireDate":true,"employeeLeaveDateTime":true,"employeeType":true,"jobTitle":true,"department":true,"companyName":true,"employeeOrgData":true,"officeLocation":true,"workAddress":false,"phones":false,"preferredLanguage":true,"manager":true}'::jsonb,
    ADD COLUMN provider_status jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE staff.identity_links
    ADD COLUMN external_upn varchar(254),
    ADD COLUMN external_email varchar(254),
    ADD COLUMN announced_at timestamptz,
    ADD COLUMN pass_issued_at timestamptz,
    ADD COLUMN reported_codes text[] NOT NULL DEFAULT '{}';

-- +goose Down
-- An operator's last resort, never a routine rollback: a binary from before
-- this version knows no provider link and reads only the identity link, so
-- the provider links are deleted — the external accounts they name are left
-- in the provider's directory for an administrator — and the kinds, the
-- directory profile's switches and the providers' last status go with their
-- columns. The installation's mode decides again, as it did.
DELETE FROM staff.identity_links WHERE provider <> 'identity';
ALTER TABLE staff.identity_links
    DROP COLUMN reported_codes,
    DROP COLUMN pass_issued_at,
    DROP COLUMN announced_at,
    DROP COLUMN external_email,
    DROP COLUMN external_upn;
ALTER TABLE staff.settings
    DROP COLUMN provider_status,
    DROP COLUMN directory_fields,
    DROP COLUMN account_kind_seeded,
    DROP COLUMN default_account_kind;
ALTER TABLE staff.people DROP COLUMN account_kind;
