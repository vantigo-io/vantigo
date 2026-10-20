-- +goose Up
-- The entra module's groups (Entra design D10 and D14,
-- docs/superpowers/specs/2026-10-19-entra-design.md; delivery D,
-- docs/superpowers/plans/2026-10-20-entra-groups.md).
--
-- entra.groups are the groups the module made or adopted in the tenant for
-- Staff's groups, by their immutable object id, one per Staff group id
-- (group_id, the caller's reference, as text). key is Staff's key; the
-- display name and the mail nickname are what the module last wrote; type is
-- the kind the group was made with, which never changes. team_created_at is
-- when the group became a team. created_by_us is false for a group the
-- module found by its nickname and adopted: untrusted, the module writes
-- nothing to it, until a holder of entra:manage confirms the adoption
-- (adoption_confirmed_at, adoption_confirmed_by_user_id), as an adopted
-- account is. A deleted group is kept, marked deleted; a security group the
-- module made is never deleted in the tenant (Graph deletes one for good),
-- but emptied of its people and kept, marked retired. last_members_at is
-- when its members were last reconciled.
--
-- entra.accounts gains licences_assigned: the SKU ids (lower-case) of the
-- direct licences the module itself assigned, the only ones an Update
-- removes (design reading 9) — a licence an administrator assigned is never
-- the module's to strip.
--
-- entra.connection gains service_principal_id, the application's service
-- principal in the tenant, read from its token's oid claim and named as an
-- owner of every group the module makes (plan reading 2); and skus, the
-- tenant's licences as the last connection test or licences read found
-- them — at most 200 of [{ "skuId", "skuPartNumber", "enabled",
-- "consumed" }] — from which the licences trait is declared with no network
-- (plan reading 1).
CREATE TABLE entra.groups (
    object_id       uuid         PRIMARY KEY,
    group_id        varchar(60)  NOT NULL,
    key             varchar(60)  NOT NULL,
    display_name    varchar(256) NOT NULL,
    mail_nickname   varchar(64)  NOT NULL,
    type            varchar(12)  NOT NULL,
    team_created_at timestamptz,
    created_by_us   boolean      NOT NULL,
    state           varchar(10)  NOT NULL,
    last_members_at timestamptz,
    created_at      timestamptz  NOT NULL,
    updated_at      timestamptz  NOT NULL,
    adoption_confirmed_at         timestamptz,
    adoption_confirmed_by_user_id uuid,
    CONSTRAINT groups_type_check CHECK (type IN ('security', 'microsoft365')),
    CONSTRAINT groups_state_check CHECK (state IN ('present', 'deleted', 'retired'))
);
CREATE UNIQUE INDEX ux_entra_groups_group_id ON entra.groups (group_id);

ALTER TABLE entra.connection
    ADD COLUMN service_principal_id uuid,
    ADD COLUMN skus jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE entra.accounts ADD COLUMN licences_assigned text[] NOT NULL DEFAULT '{}';

-- +goose Down
-- The rows go, and the two columns: the groups the module made stay in the
-- tenant for an administrator, and the next connection test reads the
-- service principal and the licences again; the licences the module
-- assigned are forgotten.
ALTER TABLE entra.accounts DROP COLUMN licences_assigned;
ALTER TABLE entra.connection
    DROP COLUMN skus,
    DROP COLUMN service_principal_id;
DROP TABLE entra.groups;
