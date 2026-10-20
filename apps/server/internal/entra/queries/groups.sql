-- The groups (Entra design D14): the groups the module made or adopted in
-- the tenant for Staff's groups, by their immutable object id, one per Staff
-- group id. Written in short transactions after each Graph call, never
-- around one, as the accounts are.

-- name: GetGroupByGroupID :one
-- GetGroupByGroupID is the group made or adopted for Staff's group id. No
-- row is a group the module has never made.
SELECT * FROM entra.groups WHERE group_id = @group_id;

-- name: GetGroup :one
SELECT * FROM entra.groups WHERE object_id = @object_id;

-- name: InsertGroup :one
-- InsertGroup records the group EnsureGroup made (created_by_us) or adopted,
-- present. A row already there for the object id is brought to what the
-- call found, created_by_us and the team kept: who made the group does not
-- change. Another row for Staff's group id trips ux_entra_groups_group_id.
INSERT INTO entra.groups (
    object_id, group_id, key, display_name, mail_nickname, type, created_by_us, state, created_at, updated_at
) VALUES (
    @object_id, @group_id, @key, @display_name, @mail_nickname, @type, @created_by_us, 'present',
    @now::timestamptz, @now::timestamptz
)
ON CONFLICT (object_id) DO UPDATE SET
    group_id = EXCLUDED.group_id,
    key = EXCLUDED.key,
    display_name = EXCLUDED.display_name,
    mail_nickname = EXCLUDED.mail_nickname,
    state = 'present',
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: UpdateGroup :one
-- UpdateGroup records what a later EnsureGroup wrote: the key, the display
-- name and the mail nickname. The type never changes.
UPDATE entra.groups SET
    key = @key,
    display_name = @display_name,
    mail_nickname = @mail_nickname,
    updated_at = @now::timestamptz
WHERE object_id = @object_id
RETURNING *;

-- name: SetGroupTeam :exec
-- SetGroupTeam records when the group became a team; a team recorded keeps
-- its first stamp.
UPDATE entra.groups SET team_created_at = coalesce(team_created_at, @now::timestamptz), updated_at = @now::timestamptz
WHERE object_id = @object_id;

-- name: SetGroupMembersAt :exec
-- SetGroupMembersAt records when SetMembers last reconciled the group's
-- members.
UPDATE entra.groups SET last_members_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE object_id = @object_id;

-- name: SetGroupState :one
-- SetGroupState records a group present or deleted: a deleted one is kept,
-- marked, as an account is.
UPDATE entra.groups SET state = @state, updated_at = @now::timestamptz
WHERE object_id = @object_id
RETURNING *;

-- name: ListGroups :many
-- ListGroups is every group the module made or adopted, deleted ones too, by
-- display name: the module page's Groups table.
SELECT * FROM entra.groups ORDER BY display_name, object_id;

-- name: CountGroups :one
-- CountGroups is how many groups the module holds present in the tenant:
-- the connection's groups.count.
SELECT count(*)::integer FROM entra.groups WHERE state = 'present';

-- name: ListGroupObjectIDs :many
-- ListGroupObjectIDs is the object ids of the module's groups present in the
-- tenant: the groups the leaver's managed step removes a user from.
SELECT object_id FROM entra.groups WHERE state = 'present' ORDER BY object_id;
