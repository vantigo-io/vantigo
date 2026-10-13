-- Absence (design S8, §3.5, delivery B): the absence types. The absences,
-- the cases and the account arrive with their tasks.

-- name: ListAbsenceTypes :many
-- ListAbsenceTypes is the types in their order; with_hidden false leaves
-- out the hidden ones, the reserved absent among them.
SELECT * FROM staff.absence_types
WHERE @with_hidden::boolean OR NOT hidden
ORDER BY sort, id;

-- name: LockAbsenceType :one
-- LockAbsenceType holds one type for a change of its own, so the If-Match
-- revision is judged against the row the change then writes.
SELECT * FROM staff.absence_types WHERE id = @id FOR UPDATE;

-- name: NextAbsenceTypeSort :one
-- NextAbsenceTypeSort is the place after every type there is.
SELECT (coalesce(max(sort), 0) + 1)::integer AS next FROM staff.absence_types;

-- name: InsertAbsenceType :one
INSERT INTO staff.absence_types (
    key, name_en, name_nb, kind, unit, paid, draws_holiday, allows_percent, needs_decision,
    visibility, amelding_leave, hidden, sort, created_at, updated_at
) VALUES (
    @key, @name_en, @name_nb, @kind, @unit, @paid, @draws_holiday, @allows_percent, @needs_decision,
    @visibility, @amelding_leave, @hidden, @sort, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateAbsenceType :one
-- UpdateAbsenceType replaces a type's names, flags, visibility, mapping,
-- hidden and sort. Its key, kind and unit never change; a seed's flags are
-- the caller's to keep as they are.
UPDATE staff.absence_types SET
    name_en = @name_en,
    name_nb = @name_nb,
    paid = @paid,
    draws_holiday = @draws_holiday,
    allows_percent = @allows_percent,
    needs_decision = @needs_decision,
    visibility = @visibility,
    amelding_leave = @amelding_leave,
    hidden = @hidden,
    sort = @sort,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: AbsenceTypeInUse :one
-- AbsenceTypeInUse is whether any absence names the type. Asked under the
-- type's FOR UPDATE, which an absence's foreign key on it (KEY SHARE) waits
-- for, so no absence names it between the answer and the change.
SELECT EXISTS (SELECT 1 FROM staff.absences WHERE type_id = @id::bigint) AS in_use;
