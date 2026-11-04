-- +goose Up
-- The google module's accounts and groups (Google Workspace design G17–G21,
-- docs/superpowers/specs/2026-11-03-google-workspace-design.md; delivery B,
-- docs/superpowers/plans/2026-11-03-google-accounts.md): the module as
-- Staff's contracts.IdentityProvisioner under the key google.
--
-- google.accounts are the users the module made or found in the organisation
-- for Staff's people, by their Directory id (user_id, Google's numeric id),
-- one per employee number. primary_email is the address the module made or
-- found, never renamed. state is what the last write left: disabled
-- (suspended), enabled or deleted, a deleted row kept for the events. The
-- password a person is handed at their activation is kept sealed under
-- google/pass until it is revealed once (pass_revealed_at) or its lifetime
-- ends (pass_expires_at). created_by_us is false for a user the module found
-- holding the employee number: adopted untrusted, nothing written to it until
-- a holder of google:manage confirms the adoption (adoption_confirmed_at,
-- adoption_confirmed_by_user_id). licences_assigned are the licences the
-- module itself assigned (productId/skuId), the only ones it removes;
-- reported the codes the last writes reported, and last_error the last
-- refusal's code.
--
-- google.groups are the groups the module made or adopted for Staff's groups,
-- by their Directory id, one per Staff group id (staff_group_id, the caller's
-- reference as text). email is the group's address, which never changes once
-- the group exists. A group the module found holding the address and did not
-- create is adopted untrusted as an account is.
--
-- google.connection gains the account rules: the address domain (one of the
-- verified domains) and pattern, the organisational unit new users are made
-- in, the password's lifetime in days and who it is handed to, the leaver's
-- steps (remove the licences, leave the groups — all, the module's own or
-- none — and sign out), and skus, the licences the organisation holds as the
-- last read found them — at most 200 of [{ "productId", "skuId", "name",
-- "assigned" }] — from which the licences trait is declared with no network.
-- No table here references another schema.
CREATE TABLE google.accounts (
    user_id                       varchar(32)  PRIMARY KEY,
    employee_number               varchar(16)  NOT NULL,
    primary_email                 varchar(254) NOT NULL,
    state                         varchar(10)  NOT NULL,
    pass_sealed                   bytea,
    pass_expires_at               timestamptz,
    pass_revealed_at              timestamptz,
    created_by_us                 boolean      NOT NULL,
    licences_assigned             text[]       NOT NULL DEFAULT '{}',
    reported                      text[]       NOT NULL DEFAULT '{}',
    last_error                    varchar(60),
    last_synced_at                timestamptz,
    adoption_confirmed_at         timestamptz,
    adoption_confirmed_by_user_id uuid,
    created_at                    timestamptz  NOT NULL,
    updated_at                    timestamptz  NOT NULL,
    CONSTRAINT accounts_state_check CHECK (state IN ('disabled', 'enabled', 'deleted')),
    CONSTRAINT accounts_pass_check CHECK (pass_sealed IS NULL OR pass_expires_at IS NOT NULL)
);
CREATE UNIQUE INDEX ux_google_accounts_number ON google.accounts (upper(employee_number));

CREATE TABLE google.groups (
    group_id                      varchar(32)  PRIMARY KEY,
    staff_group_id                varchar(64)  NOT NULL,
    email                         varchar(254) NOT NULL,
    created_by_us                 boolean      NOT NULL,
    adoption_confirmed_at         timestamptz,
    adoption_confirmed_by_user_id uuid,
    created_at                    timestamptz  NOT NULL,
    updated_at                    timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_google_groups_staff_group ON google.groups (staff_group_id);
CREATE UNIQUE INDEX ux_google_groups_email ON google.groups (lower(email));

ALTER TABLE google.connection
    ADD COLUMN address_domain         varchar(253),
    ADD COLUMN address_pattern        varchar(40)  NOT NULL DEFAULT 'given.family',
    ADD COLUMN org_unit_path          varchar(400) NOT NULL DEFAULT '/',
    ADD COLUMN pass_lifetime_days     integer      NOT NULL DEFAULT 7,
    ADD COLUMN pass_delivery          varchar(10)  NOT NULL DEFAULT 'hr',
    ADD COLUMN leaver_remove_licences boolean      NOT NULL DEFAULT true,
    ADD COLUMN leaver_remove_groups   varchar(10)  NOT NULL DEFAULT 'managed',
    ADD COLUMN leaver_sign_out        boolean      NOT NULL DEFAULT true,
    ADD COLUMN skus                   jsonb        NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT connection_address_pattern_check CHECK (address_pattern IN ('given.family', 'g.family', 'given')),
    ADD CONSTRAINT connection_org_unit_path_check CHECK (org_unit_path LIKE '/%'),
    ADD CONSTRAINT connection_pass_lifetime_days_check CHECK (pass_lifetime_days BETWEEN 1 AND 30),
    ADD CONSTRAINT connection_pass_delivery_check CHECK (pass_delivery IN ('hr', 'manager', 'both')),
    ADD CONSTRAINT connection_leaver_remove_groups_check CHECK (leaver_remove_groups IN ('all', 'managed', 'none'));

-- +goose Down
-- The rows go, and the account rules: the users and groups the module made
-- stay in the organisation for an administrator, and a later Up finds the
-- users again by employee number; any password not yet revealed is lost.
ALTER TABLE google.connection
    DROP CONSTRAINT connection_leaver_remove_groups_check,
    DROP CONSTRAINT connection_pass_delivery_check,
    DROP CONSTRAINT connection_pass_lifetime_days_check,
    DROP CONSTRAINT connection_org_unit_path_check,
    DROP CONSTRAINT connection_address_pattern_check,
    DROP COLUMN skus,
    DROP COLUMN leaver_sign_out,
    DROP COLUMN leaver_remove_groups,
    DROP COLUMN leaver_remove_licences,
    DROP COLUMN pass_delivery,
    DROP COLUMN pass_lifetime_days,
    DROP COLUMN org_unit_path,
    DROP COLUMN address_pattern,
    DROP COLUMN address_domain;
DROP TABLE google.groups;
DROP TABLE google.accounts;
