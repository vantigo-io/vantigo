-- +goose Up
-- Groups in Staff (Entra design D9, D10 and D14,
-- docs/superpowers/specs/2026-10-19-entra-design.md; delivery D,
-- docs/superpowers/plans/2026-10-20-entra-groups.md).
--
-- staff.groups are HR's groups: listed (HR names the members) or derived
-- (rule, one predicate over the register — everyone employed, a department
-- and optionally those beneath it, a business unit, a location, an employee
-- type, a manager's direct reports — which the accounts worker evaluates on
-- the business day). key is stable and lower-case, the provider's
-- reference, immutable once made. An archived group keeps its rows; who
-- archived it is archived_by_user_id. revision is the If-Match of its PUT.
--
-- staff.group_members are a group's people: source says whether HR listed
-- the person or the rule derived them, since the day they joined. A derived
-- group's rows are the worker's materialisation, written as the difference,
-- each change an event on the person. staff.group_owners are the people who
-- own the group in a provider's directory.
--
-- staff.group_traits are a group's section per provider (D10): the trait
-- values Staff stores as the provider declared them, whether HR has the
-- provider on for the group (enabled, and when it turned it off,
-- disabled_at), and the accounts worker's sync of it (D14) — the external
-- group's id the provider answered, the state ('pending', 'synced',
-- 'failed', 'retired', 'deleted'), the hashes of the values and the members
-- last sent, the last error code and the trait it names, when it last
-- synced and when it was retired, and the claim's lease and retry, as
-- identity_links holds them.
--
-- A person's rows reference staff.people and go before the person on an
-- erase or a delete. As 00042, no CHECK holds a closed set to its values:
-- the module's validation owns the rules and reports them per field, and a
-- trait's values are the provider's to judge.
CREATE TABLE staff.groups (
    id                  bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    key                 varchar(60)  NOT NULL,
    name                varchar(120) NOT NULL,
    description         varchar(500),
    kind                varchar(10)  NOT NULL,
    rule                jsonb,
    archived_at         timestamptz,
    archived_by_user_id uuid,
    revision            integer      NOT NULL DEFAULT 1,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_groups_key ON staff.groups (key);

CREATE TABLE staff.group_members (
    group_id  bigint      NOT NULL REFERENCES staff.groups (id) ON DELETE CASCADE,
    person_id bigint      NOT NULL REFERENCES staff.people (id),
    source    varchar(10) NOT NULL,
    since     date        NOT NULL,
    PRIMARY KEY (group_id, person_id)
);
CREATE INDEX ix_group_members_person ON staff.group_members (person_id);

CREATE TABLE staff.group_owners (
    group_id  bigint NOT NULL REFERENCES staff.groups (id) ON DELETE CASCADE,
    person_id bigint NOT NULL REFERENCES staff.people (id),
    PRIMARY KEY (group_id, person_id)
);
CREATE INDEX ix_group_owners_person ON staff.group_owners (person_id);

CREATE TABLE staff.group_traits (
    group_id          bigint       NOT NULL REFERENCES staff.groups (id) ON DELETE CASCADE,
    provider          varchar(20)  NOT NULL,
    values            jsonb        NOT NULL,
    enabled           boolean      NOT NULL DEFAULT true,
    disabled_at       timestamptz,
    external_id       varchar(200),
    state             varchar(20)  NOT NULL,
    last_values_hash  bytea,
    last_members_hash bytea,
    last_error        varchar(200),
    last_error_field  varchar(40),
    synced_at         timestamptz,
    retired_at        timestamptz,
    attempts          integer      NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz,
    lease_id          uuid,
    lease_until       timestamptz,
    PRIMARY KEY (group_id, provider)
);
CREATE INDEX ix_group_traits_due ON staff.group_traits (provider, next_attempt_at);

-- +goose Down
-- An operator's last resort, never a routine rollback: the groups, their
-- members, owners and trait sections go. The groups a provider made in its
-- directory are left there for an administrator; a later Up starts with no
-- group, and a provider finds its own again by the key's nickname.
DROP TABLE staff.group_traits;
DROP TABLE staff.group_owners;
DROP TABLE staff.group_members;
DROP TABLE staff.groups;
