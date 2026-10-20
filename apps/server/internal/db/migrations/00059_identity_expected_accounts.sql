-- +goose Up
-- The announced account (Entra design D4,
-- docs/superpowers/specs/2026-10-19-entra-design.md; delivery B,
-- docs/superpowers/plans/2026-10-20-staff-provisioner-slot.md): a Vantigo
-- user Staff knows will arrive from the identity provider for a person it
-- registers. Staff has had the provider create the external account and
-- announces it (contracts.AccountProvisioning.Expect), so that the user the
-- provider's SCIM push or the person's first sign-in creates is adopted as
-- Staff-managed, numbered and not pending, with no Owner in between.
--
-- An arrival is matched on three keys, in order: the enterprise extension's
-- employeeNumber, by employee_number_key — identity's one Go normaliser's
-- key (contracts.NormalizeEmployeeNumber), the table's primary key, so a
-- number has at most one announcement and a new one replaces it; SCIM's
-- userName against user_principal_name, stored normalised as emails are; and
-- OIDC's oid (or SCIM's externalId, when the tenant maps it so) against
-- object_id, under the issuer the user will sign in with. Neither of the
-- last two is unique (plan reading 5): two announcements naming one would be
-- Staff's bug, and the match takes the newest, so their indexes are plain.
-- replace_user_id names a pending, credential-less managed user of the same
-- number — the person's kind changed from local — whom the arrival completes
-- instead of creating another; the announcement goes with that user. The
-- actor is a user or a system, exactly one, as the audit names it.
CREATE TABLE identity.expected_accounts (
    employee_number_key text         PRIMARY KEY,
    employee_number     varchar(16)  NOT NULL,
    display_name        varchar(200) NOT NULL,
    preferred_language  varchar(2),
    issuer              varchar(200),
    object_id           varchar(200),
    user_principal_name varchar(254),
    replace_user_id     uuid         REFERENCES identity.users (id) ON DELETE CASCADE,
    created_at          timestamptz  NOT NULL,
    created_by_user_id  uuid,
    created_by_system   varchar(40),
    CONSTRAINT expected_accounts_created_by_check CHECK ((created_by_user_id IS NULL) <> (created_by_system IS NULL))
);
CREATE INDEX ix_expected_accounts_object_id ON identity.expected_accounts (object_id) WHERE object_id IS NOT NULL;
CREATE INDEX ix_expected_accounts_upn ON identity.expected_accounts (user_principal_name) WHERE user_principal_name IS NOT NULL;

-- provisioning_roles_at is when Staff's Activate last gave the user the
-- provisioning roles, null until it has (plan reading 3). An adopted user
-- arrives enabled — SCIM's active, or the sign-in that made them — so the
-- Activate that enables them never comes; for the entra kind the roles are
-- given once, on the first Activate that finds this null, and an Owner's
-- later removal of one is not undone. The local path sets it too.
ALTER TABLE identity.users ADD COLUMN provisioning_roles_at timestamptz;

-- A user Staff manages who is not pending had their roles given already:
-- by the previous release's Activate (the local kind, which gave them on
-- the enable), or by an Owner when they adopted the user (an external
-- kind). The once-only grant is spent for them, so an entra Activate after
-- this upgrade does not give the roles again over what an Owner has since
-- removed. A pending user has had none and keeps null.
UPDATE identity.users SET provisioning_roles_at = now()
WHERE managed_by IS NOT NULL AND pending_since IS NULL;

-- +goose Down
-- The announcements not yet fulfilled go with the table: Staff announces a
-- person's account again after its provider's next Prepare, and until then
-- an arrival is a plain user an Owner adopts, as before this version. The
-- record of the roles given goes too, so a binary from before it gives them
-- as it did — on the Activate that enables a user Staff disabled.
ALTER TABLE identity.users DROP COLUMN provisioning_roles_at;
DROP TABLE identity.expected_accounts;
