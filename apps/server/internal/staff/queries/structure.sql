-- The company's structure (design S6, §3.4): legal entities, business units,
-- departments and locations. Every list answers archived rows too, so a stored
-- reference can still be named; the pickers leave them out.

-- name: ListLegalEntities :many
SELECT * FROM staff.legal_entities ORDER BY name, id;

-- name: GetLegalEntity :one
SELECT * FROM staff.legal_entities WHERE id = @id;

-- name: LockLegalEntity :one
-- LockLegalEntity holds one legal entity for a write of its own, so the
-- If-Match revision is judged against the row the write then changes.
SELECT * FROM staff.legal_entities WHERE id = @id FOR UPDATE;

-- name: ShareLegalEntity :one
-- ShareLegalEntity reads a legal entity a business unit is about to name and
-- holds it against an archive until the transaction ends, so a unit is never
-- put under a legal entity archived between the check and the write.
SELECT * FROM staff.legal_entities WHERE id = @id FOR SHARE;

-- name: HasDefaultLegalEntity :one
SELECT EXISTS (SELECT 1 FROM staff.legal_entities WHERE is_default) AS has_default;

-- name: ClearLegalEntityDefault :exec
-- ClearLegalEntityDefault takes the default away from every legal entity but
-- keep, before keep is made it — the partial unique index admits one default,
-- and is judged per statement.
UPDATE staff.legal_entities
SET is_default = false, revision = revision + 1, updated_at = @now::timestamptz
WHERE is_default AND id <> @keep;

-- name: InsertLegalEntity :one
INSERT INTO staff.legal_entities (name, org_number, is_default, created_at, updated_at)
VALUES (@name, @org_number, @is_default, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateLegalEntity :one
-- UpdateLegalEntity replaces a legal entity's fields; make_default true makes
-- it the default, false leaves the flag as it is.
UPDATE staff.legal_entities SET
    name = @name,
    org_number = @org_number,
    is_default = is_default OR @make_default::boolean,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ArchiveLegalEntity :one
-- ArchiveLegalEntity stamps a legal entity archived. One already archived
-- keeps its stamp and its revision: archiving it again changes nothing.
UPDATE staff.legal_entities SET
    archived_at = coalesce(archived_at, @now::timestamptz),
    revision = revision + CASE WHEN archived_at IS NULL THEN 1 ELSE 0 END,
    updated_at = CASE WHEN archived_at IS NULL THEN @now::timestamptz ELSE updated_at END
WHERE id = @id
RETURNING *;

-- name: ListBusinessUnits :many
SELECT * FROM staff.business_units ORDER BY name, id;

-- name: LockBusinessUnit :one
SELECT * FROM staff.business_units WHERE id = @id FOR UPDATE;

-- name: HasDefaultBusinessUnit :one
SELECT EXISTS (SELECT 1 FROM staff.business_units WHERE is_default) AS has_default;

-- name: ClearBusinessUnitDefault :exec
UPDATE staff.business_units
SET is_default = false, revision = revision + 1, updated_at = @now::timestamptz
WHERE is_default AND id <> @keep;

-- name: InsertBusinessUnit :one
INSERT INTO staff.business_units (legal_entity_id, name, org_number, is_default, created_at, updated_at)
VALUES (@legal_entity_id, @name, @org_number, @is_default, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateBusinessUnit :one
UPDATE staff.business_units SET
    legal_entity_id = @legal_entity_id,
    name = @name,
    org_number = @org_number,
    is_default = is_default OR @make_default::boolean,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ArchiveBusinessUnit :one
UPDATE staff.business_units SET
    archived_at = coalesce(archived_at, @now::timestamptz),
    revision = revision + CASE WHEN archived_at IS NULL THEN 1 ELSE 0 END,
    updated_at = CASE WHEN archived_at IS NULL THEN @now::timestamptz ELSE updated_at END
WHERE id = @id
RETURNING *;

-- name: ListDepartments :many
SELECT * FROM staff.departments ORDER BY code, id;

-- name: LockDepartment :one
SELECT * FROM staff.departments WHERE id = @id FOR UPDATE;

-- name: ShareDepartment :one
-- ShareDepartment reads the department a write is about to name as a parent
-- and holds it against an archive until the transaction ends.
SELECT * FROM staff.departments WHERE id = @id FOR SHARE;

-- name: DepartmentParents :many
-- DepartmentParents is every department's parent, the whole graph the cycle
-- check walks. It is read under the manager-graph lock, which every write of
-- a parent takes, so no other write can change the graph while it is judged.
SELECT id, parent_id FROM staff.departments;

-- name: PersonExists :one
SELECT EXISTS (SELECT 1 FROM staff.people WHERE id = @id) AS person_exists;

-- name: InsertDepartment :one
INSERT INTO staff.departments (code, name, manager_person_id, cost_centre, division, parent_id, created_at, updated_at)
VALUES (@code, @name, @manager_person_id, @cost_centre, @division, @parent_id, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateDepartment :one
UPDATE staff.departments SET
    code = @code,
    name = @name,
    manager_person_id = @manager_person_id,
    cost_centre = @cost_centre,
    division = @division,
    parent_id = @parent_id,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ArchiveDepartment :one
UPDATE staff.departments SET
    archived_at = coalesce(archived_at, @now::timestamptz),
    revision = revision + CASE WHEN archived_at IS NULL THEN 1 ELSE 0 END,
    updated_at = CASE WHEN archived_at IS NULL THEN @now::timestamptz ELSE updated_at END
WHERE id = @id
RETURNING *;

-- name: ListLocations :many
SELECT * FROM staff.locations ORDER BY name, id;

-- name: LockLocation :one
SELECT * FROM staff.locations WHERE id = @id FOR UPDATE;

-- name: InsertLocation :one
INSERT INTO staff.locations (name, street, postal_code, city, country, created_at, updated_at)
VALUES (@name, @street, @postal_code, @city, @country, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateLocation :one
UPDATE staff.locations SET
    name = @name,
    street = @street,
    postal_code = @postal_code,
    city = @city,
    country = @country,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ArchiveLocation :one
UPDATE staff.locations SET
    archived_at = coalesce(archived_at, @now::timestamptz),
    revision = revision + CASE WHEN archived_at IS NULL THEN 1 ELSE 0 END,
    updated_at = CASE WHEN archived_at IS NULL THEN @now::timestamptz ELSE updated_at END
WHERE id = @id
RETURNING *;
