-- The groups (Google Workspace design G20): the groups the module made or
-- adopted for Staff's groups, by their Directory id, one per Staff group id.

-- name: GetGroup :one
SELECT * FROM google.groups WHERE group_id = @group_id;

-- name: GetGroupByStaffID :one
SELECT * FROM google.groups WHERE staff_group_id = @staff_group_id;

-- name: GetGroupByEmail :one
SELECT * FROM google.groups WHERE lower(email) = lower(@email::text);

-- name: InsertGroup :one
INSERT INTO google.groups (group_id, staff_group_id, email, created_by_us, created_at, updated_at)
VALUES (@group_id, @staff_group_id, @email, @created_by_us, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: TouchGroup :exec
UPDATE google.groups SET updated_at = @now::timestamptz WHERE group_id = @group_id;

-- name: DeleteGroup :exec
-- DeleteGroup forgets a group: deleted at Google, gone there, or adopted and
-- let go. The events keep its id.
DELETE FROM google.groups WHERE group_id = @group_id;

-- name: ConfirmGroupAdoption :one
-- ConfirmGroupAdoption records that a holder of google:manage confirmed an
-- adopted group. It answers the row whatever it was, so a second
-- confirmation is answered as the first.
UPDATE google.groups SET
    adoption_confirmed_at = COALESCE(adoption_confirmed_at, CASE WHEN created_by_us THEN NULL ELSE @now::timestamptz END),
    adoption_confirmed_by_user_id = COALESCE(adoption_confirmed_by_user_id, CASE WHEN created_by_us THEN NULL ELSE @confirmed_by_user_id::uuid END),
    updated_at = @now::timestamptz
WHERE group_id = @group_id
RETURNING *;

-- name: ListGroups :many
SELECT * FROM google.groups ORDER BY email, group_id;

-- name: ListGroupIDs :many
-- ListGroupIDs are the groups the module made, for a leaver's managed
-- groups: an adopted group is the organisation's.
SELECT group_id FROM google.groups WHERE created_by_us ORDER BY group_id;
