-- Groups (Entra design D9, D10 and D14; delivery D,
-- docs/superpowers/plans/2026-10-20-entra-groups.md): HR's groups, listed or
-- derived, their members and owners, and their sections per provider, which
-- the accounts worker syncs to the provider's directory under a lease as it
-- works the links (links.sql).

-- name: ListGroups :many
-- ListGroups is every group by name, archived ones too, with how many
-- members and owners it has.
SELECT g.id, g.key, g.name, g.description, g.kind, g.rule, g.archived_at, g.archived_by_user_id, g.revision,
    g.created_at, g.updated_at,
    (SELECT count(*) FROM staff.group_members m WHERE m.group_id = g.id)::integer AS member_count,
    (SELECT count(*) FROM staff.group_owners o WHERE o.group_id = g.id)::integer AS owner_count
FROM staff.groups g
ORDER BY g.name, g.id;

-- name: ListGroupTraitStates :many
-- ListGroupTraitStates is every group's sections, by group and provider:
-- whether HR has the provider on for it and where the sync stands — what
-- the groups' list shows per provider without reading each group's rows.
SELECT group_id, provider, enabled, state FROM staff.group_traits ORDER BY group_id, provider;

-- name: GetGroup :one
-- GetGroup is one group, with how many members and owners it has. No row is
-- no such group.
SELECT g.id, g.key, g.name, g.description, g.kind, g.rule, g.archived_at, g.archived_by_user_id, g.revision,
    g.created_at, g.updated_at,
    (SELECT count(*) FROM staff.group_members m WHERE m.group_id = g.id)::integer AS member_count,
    (SELECT count(*) FROM staff.group_owners o WHERE o.group_id = g.id)::integer AS owner_count
FROM staff.groups g
WHERE g.id = @id;

-- name: LockGroup :one
-- LockGroup holds one group for a write of its own — its PUT, its archive,
-- its delete, its members, owners or sections — so the If-Match revision
-- and the archive are judged against the row the write then changes.
SELECT * FROM staff.groups WHERE id = @id FOR UPDATE;

-- name: GetGroupByKey :one
-- GetGroupByKey is the group holding a key, which a create answers as a
-- conflict. No row is a free key.
SELECT * FROM staff.groups WHERE key = @key;

-- name: InsertGroup :one
-- InsertGroup writes a new group at revision 1. A key another group holds
-- trips ux_groups_key, which the caller answers as a conflict.
INSERT INTO staff.groups (key, name, description, kind, rule, created_at, updated_at)
VALUES (@key, @name, sqlc.narg(description)::varchar, @kind, sqlc.narg(rule)::jsonb, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateGroup :one
-- UpdateGroup replaces a group's fields but its key, which never changes,
-- and moves its revision.
UPDATE staff.groups SET
    name = @name,
    description = sqlc.narg(description)::varchar,
    kind = @kind,
    rule = sqlc.narg(rule)::jsonb,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ArchiveGroup :one
-- ArchiveGroup stamps a group archived, by whom. One already archived keeps
-- its stamp, its archiver and its revision: archiving it again changes
-- nothing.
UPDATE staff.groups SET
    archived_at = coalesce(archived_at, @now::timestamptz),
    archived_by_user_id = CASE WHEN archived_at IS NULL THEN sqlc.narg(archived_by_user_id)::uuid ELSE archived_by_user_id END,
    revision = revision + CASE WHEN archived_at IS NULL THEN 1 ELSE 0 END,
    updated_at = CASE WHEN archived_at IS NULL THEN @now::timestamptz ELSE updated_at END
WHERE id = @id
RETURNING *;

-- name: DeleteGroup :execrows
-- DeleteGroup removes a group, its members, owners and sections with it
-- (ON DELETE CASCADE). The caller refuses one ever synced
-- (CountGroupTraitsSynced) first. No row is no such group.
DELETE FROM staff.groups WHERE id = @id;

-- name: ListGroupMembers :many
-- ListGroupMembers is a group's members by name, with their employee
-- numbers, how each came in and since when.
SELECT m.person_id, p.display_name, p.employee_number, m.source, m.since
FROM staff.group_members m JOIN staff.people p ON p.id = m.person_id
WHERE m.group_id = @group_id
ORDER BY p.display_name, p.id;

-- name: GroupMemberIDs :many
-- GroupMemberIDs is a group's members' person ids, ascending: the set a
-- materialisation or a listed group's PUT writes the difference against.
SELECT person_id FROM staff.group_members WHERE group_id = @group_id ORDER BY person_id;

-- name: InsertGroupMember :exec
-- InsertGroupMember adds a person to a group from @since, listed or
-- derived. A person already in it is left as they were.
INSERT INTO staff.group_members (group_id, person_id, source, since)
VALUES (@group_id, @person_id, @source, @since::date)
ON CONFLICT (group_id, person_id) DO NOTHING;

-- name: DeleteGroupMember :execrows
-- DeleteGroupMember takes a person out of a group. No row is a person not
-- in it.
DELETE FROM staff.group_members WHERE group_id = @group_id AND person_id = @person_id;

-- name: DeleteGroupMembersNotIn :many
-- DeleteGroupMembersNotIn takes out of a group everyone not among
-- @person_ids and answers who left, for their events.
DELETE FROM staff.group_members
WHERE group_id = @group_id AND NOT (person_id = ANY(@person_ids::bigint[]))
RETURNING person_id;

-- name: ListGroupOwners :many
-- ListGroupOwners is a group's owners by name, with their employee numbers.
SELECT o.person_id, p.display_name, p.employee_number
FROM staff.group_owners o JOIN staff.people p ON p.id = o.person_id
WHERE o.group_id = @group_id
ORDER BY p.display_name, p.id;

-- name: ReplaceGroupOwners :exec
-- ReplaceGroupOwners makes a group's owners exactly @person_ids: the others
-- deleted, the missing added, in one statement.
WITH gone AS (
    DELETE FROM staff.group_owners
    WHERE group_id = @group_id AND NOT (person_id = ANY(@person_ids::bigint[]))
)
INSERT INTO staff.group_owners (group_id, person_id)
SELECT @group_id::bigint, unnest(@person_ids::bigint[])
ON CONFLICT (group_id, person_id) DO NOTHING;

-- name: PeopleNotErased :many
-- PeopleNotErased is which of @person_ids name a person who exists and is
-- not erased: the people a group's members or owners may be.
SELECT id FROM staff.people WHERE id = ANY(@person_ids::bigint[]) AND erased_at IS NULL ORDER BY id;

-- name: GroupsOfPerson :many
-- GroupsOfPerson is the groups a person is in, archived ones aside, by
-- name: how they came in and since when, for the Account tab.
SELECT g.id, g.name, g.key, m.source, m.since
FROM staff.group_members m JOIN staff.groups g ON g.id = m.group_id
WHERE m.person_id = @person_id AND g.archived_at IS NULL
ORDER BY g.name, g.id;

-- name: GroupNamesOfPerson :many
-- GroupNamesOfPerson is the groups a person is in, archived ones aside, by
-- name only: what My page shows the person.
SELECT g.id, g.name
FROM staff.group_members m JOIN staff.groups g ON g.id = m.group_id
WHERE m.person_id = @person_id AND g.archived_at IS NULL
ORDER BY g.name, g.id;

-- The person's group rows go before an erase or a delete of the person, in
-- its transaction, both of these: no event, the person is going, and the
-- next sync removes them in the provider's directory.

-- name: DeletePersonGroupMembers :exec
-- DeletePersonGroupMembers takes a person out of every group.
DELETE FROM staff.group_members WHERE person_id = @person_id;

-- name: DeletePersonGroupOwners :exec
-- DeletePersonGroupOwners takes a person out of every group's owners.
DELETE FROM staff.group_owners WHERE person_id = @person_id;

-- name: ListDerivedGroups :many
-- ListDerivedGroups is every derived group not archived, the ones a
-- materialisation evaluates.
SELECT * FROM staff.groups WHERE kind = 'derived' AND archived_at IS NULL ORDER BY id;

-- name: ListGroupTraits :many
-- ListGroupTraits is a group's sections, by provider.
SELECT * FROM staff.group_traits WHERE group_id = @group_id ORDER BY provider;

-- name: GetGroupTrait :one
-- GetGroupTrait is a group's section under @provider. No row is a provider
-- HR never turned on for the group.
SELECT * FROM staff.group_traits WHERE group_id = @group_id AND provider = @provider;

-- name: UpsertGroupTrait :one
-- UpsertGroupTrait turns @provider on for a group with @values: the section
-- pending, no attempt counted, due now, the values' hash forgotten — so the
-- worker's next cycle sends them — and no longer disabled, retired or
-- failing. The external group's id and the members' hash stay: the provider
-- finds its group again by the caller's id. A claim the worker holds on the
-- row is ended, so its result, judged on the values it read, is never
-- written over these (WriteGroupTraitResult).
INSERT INTO staff.group_traits (group_id, provider, values, enabled, state, attempts, next_attempt_at)
VALUES (@group_id, @provider, @values::jsonb, true, 'pending', 0, @now::timestamptz)
ON CONFLICT (group_id, provider) DO UPDATE SET
    values = EXCLUDED.values,
    enabled = true,
    disabled_at = NULL,
    state = 'pending',
    last_values_hash = NULL,
    last_error = NULL,
    last_error_field = NULL,
    retired_at = NULL,
    attempts = 0,
    next_attempt_at = EXCLUDED.next_attempt_at,
    lease_id = NULL,
    lease_until = NULL
RETURNING *;

-- name: DisableGroupTrait :one
-- DisableGroupTrait turns @provider off for a group: disabled since the
-- first time it was, its state as it stands, and due now unless already
-- retired or deleted — the worker retires the external group per Staff's
-- deleteExternalAfterDays, judged from disabled_at. A claim the worker
-- holds on the row is ended, as UpsertGroupTrait's is. No row is a provider
-- never turned on, which the caller answers as done.
UPDATE staff.group_traits SET
    enabled = false,
    disabled_at = coalesce(disabled_at, @now::timestamptz),
    next_attempt_at = CASE WHEN state IN ('retired', 'deleted') THEN next_attempt_at ELSE @now::timestamptz END,
    lease_id = NULL,
    lease_until = NULL
WHERE group_id = @group_id AND provider = @provider
RETURNING *;

-- name: MakeGroupTraitsDue :exec
-- MakeGroupTraitsDue makes every section of a group not retired or deleted
-- due now, any claim on it ended: the group was archived, renamed or
-- redescribed, which the worker carries to each provider — a failing one
-- included, since what HR changed may be what it failed on.
UPDATE staff.group_traits SET next_attempt_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
WHERE group_id = @group_id AND state NOT IN ('retired', 'deleted');

-- name: ClaimGroupTrait :one
-- ClaimGroupTrait leases the next due section — its next attempt reached,
-- no lease or a lapsed one, not retired or deleted — enabled or not, since
-- a disabled one or an archived group's may need retiring. A section with
-- no next attempt is not due: one failing on a value the provider refused,
-- or on a group it may not manage, waits for HR to change the values
-- (UpsertGroupTrait makes it due). The row alone is locked and one another
-- cycle is claiming is skipped; LIMIT 1 with SKIP LOCKED may find none
-- while due rows remain, which the next poll claims, never lost — as
-- ClaimAccountLink (accounts.sql).
UPDATE staff.group_traits SET lease_id = @lease_id::uuid, lease_until = @lease_until::timestamptz
WHERE (group_id, provider) = (
    SELECT t.group_id, t.provider FROM staff.group_traits t
    WHERE t.state NOT IN ('retired', 'deleted')
      AND t.next_attempt_at <= @now::timestamptz
      AND (t.lease_until IS NULL OR t.lease_until <= @now::timestamptz)
    ORDER BY t.next_attempt_at, t.group_id, t.provider
    LIMIT 1
    FOR UPDATE OF t SKIP LOCKED
)
RETURNING *;

-- name: WriteGroupTraitResult :execrows
-- WriteGroupTraitResult records what a claim did on a section, every sync
-- column replaced, and clears the lease — on the claim's lease alone: no
-- row is a section gone (the group deleted) or a claim lost, its lease
-- lapsed and taken by another cycle or ended by HR's change of the section
-- (UpsertGroupTrait, DisableGroupTrait). The values and the switch are HR's
-- and never written here.
UPDATE staff.group_traits SET
    external_id = sqlc.narg(external_id)::varchar,
    state = @state,
    last_values_hash = sqlc.narg(last_values_hash)::bytea,
    last_members_hash = sqlc.narg(last_members_hash)::bytea,
    last_error = sqlc.narg(last_error)::varchar,
    last_error_field = sqlc.narg(last_error_field)::varchar,
    synced_at = sqlc.narg(synced_at)::timestamptz,
    retired_at = sqlc.narg(retired_at)::timestamptz,
    attempts = @attempts,
    next_attempt_at = sqlc.narg(next_attempt_at)::timestamptz,
    lease_id = NULL,
    lease_until = NULL
WHERE group_id = @group_id AND provider = @provider AND lease_id = @lease_id::uuid;

-- name: CountGroupTraitsSynced :one
-- CountGroupTraitsSynced is how many of a group's sections ever reached the
-- provider — synced once, or holding the external group's id — which makes
-- the group one to archive, never delete.
SELECT count(*)::integer FROM staff.group_traits
WHERE group_id = @group_id AND (synced_at IS NOT NULL OR external_id IS NOT NULL);

-- name: ListFailedGroupTraits :many
-- ListFailedGroupTraits is every failed section, by group name: the
-- groupSyncFailed attention items, one per row, naming the group, the
-- provider and the code — never the directory's words.
SELECT g.id AS group_id, g.name, g.key, t.provider, t.last_error, t.last_error_field
FROM staff.group_traits t JOIN staff.groups g ON g.id = t.group_id
WHERE t.state = 'failed'
ORDER BY g.name, g.id, t.provider;

-- name: LicencesOfPerson :many
-- LicencesOfPerson is the licences the groups a person is in carry under
-- @provider — each value of the licences trait of their sections turned on,
-- not retired or deleted, of groups not archived — distinct and sorted; the
-- caller makes the spec's union of them (Entra design D10).
SELECT DISTINCT l.licence::text AS licence
FROM staff.group_members m
JOIN staff.groups g ON g.id = m.group_id
JOIN staff.group_traits t ON t.group_id = m.group_id
CROSS JOIN LATERAL jsonb_array_elements_text(
    CASE WHEN jsonb_typeof(t.values -> 'licences') = 'array' THEN t.values -> 'licences' ELSE '[]'::jsonb END
) AS l(licence)
WHERE m.person_id = @person_id AND t.provider = @provider AND t.enabled
  AND t.state NOT IN ('retired', 'deleted') AND g.archived_at IS NULL
ORDER BY 1;

-- name: EnabledProviderLinksOfPeople :many
-- EnabledProviderLinksOfPeople is the external account of each of
-- @person_ids' people under @provider that is enabled, by external id: a
-- group's members' and owners' ids as the provider's directory knows them.
-- A disabled or deleted account is no member.
SELECT person_id, external_id::text AS external_id FROM staff.identity_links
WHERE person_id = ANY(@person_ids::bigint[]) AND provider = @provider
  AND state = 'enabled' AND external_id IS NOT NULL
ORDER BY external_id;
