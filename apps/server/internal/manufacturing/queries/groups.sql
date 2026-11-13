-- Work center groups (manufacturing phase 2 M117): interchangeable active
-- centers of one site an operation may target instead of one center. Every
-- write of a group locks its row first (GetWorkCenterGroupForUpdate), then
-- its members' centers FOR SHARE in id order.

-- name: ListWorkCenterGroups :many
-- ListWorkCenterGroups is the groups in one status (every status when status
-- is null), at one site when site_id is set, by code.
SELECT * FROM manufacturing.work_center_groups
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(site_id)::bigint IS NULL OR site_id = sqlc.narg(site_id)::bigint)
ORDER BY code;

-- name: GetWorkCenterGroup :one
SELECT * FROM manufacturing.work_center_groups WHERE id = @id;

-- name: GetWorkCenterGroupForUpdate :one
SELECT * FROM manufacturing.work_center_groups WHERE id = @id FOR UPDATE;

-- name: WorkCenterGroupsByIDs :many
SELECT * FROM manufacturing.work_center_groups WHERE id = ANY(@ids::bigint[]) ORDER BY id;

-- name: WorkCenterGroupCodeTaken :one
-- WorkCenterGroupCodeTaken reports whether another group has the code; id 0
-- asks about a new one.
SELECT EXISTS (SELECT 1 FROM manufacturing.work_center_groups WHERE code = @code AND id <> @id);

-- name: InsertWorkCenterGroup :one
INSERT INTO manufacturing.work_center_groups (code, name, site_id, created_at, updated_at)
VALUES (@code, @name, @site_id, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateWorkCenterGroup :one
UPDATE manufacturing.work_center_groups
SET code = @code, name = @name, site_id = @site_id, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SetWorkCenterGroupStatus :one
UPDATE manufacturing.work_center_groups
SET status = @status, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteWorkCenterGroupMembers :exec
DELETE FROM manufacturing.work_center_group_members WHERE group_id = @group_id;

-- name: InsertWorkCenterGroupMember :exec
INSERT INTO manufacturing.work_center_group_members (group_id, work_center_id) VALUES (@group_id, @work_center_id);

-- name: MemberIDsOfGroups :many
-- MemberIDsOfGroups is the groups' members, by group and the member's code.
SELECT m.group_id, m.work_center_id FROM manufacturing.work_center_group_members m
JOIN manufacturing.work_centers c ON c.id = m.work_center_id
WHERE m.group_id = ANY(@group_ids::bigint[])
ORDER BY m.group_id, c.code;

-- name: LockWorkCentersForShareByIDs :many
-- LockWorkCentersForShareByIDs is a group write's members FOR SHARE in id
-- order, after the group's row: an archive of one, which takes it FOR
-- UPDATE, waits for the write.
SELECT * FROM manufacturing.work_centers WHERE id = ANY(@ids::bigint[]) ORDER BY id FOR SHARE;

-- name: CountActiveBomsAtGroup :one
-- CountActiveBomsAtGroup is how many active BoMs have an operation that
-- targets the group: while any does, its site does not change and it is not
-- archived (M117, M12a).
SELECT count(DISTINCT b.id)::int FROM manufacturing.boms b
JOIN manufacturing.bom_operations o ON o.bom_id = b.id
WHERE o.work_center_group_id = @group_id AND b.status = 'active';
