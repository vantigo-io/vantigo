-- Competence profiles (staff competence design S12–S14, §3.8, delivery C):
-- the profiles catalogue — a named list of competences, each with a minimum
-- level and whether it must be valid — a person's assigned profiles and a
-- department's default, and the reads the gap list, the competenceGap item,
-- the matrix and the interests overview are computed from. A profile's write
-- locks its row (FOR NO KEY UPDATE) and shares each competence an item names
-- (ShareCompetence); an assignment runs under its person's lock
-- (withPersonLock), then shares each profile it names
-- (ShareCompetenceProfile), as a department's default does, so an archive and
-- an assignment are judged one after the other (§4 Locking). The item rows
-- read the competence's summary columns in the same order as
-- ListPersonCompetences.

-- name: ListCompetenceProfiles :many
-- ListCompetenceProfiles is every profile, archived ones included, by key;
-- Settings restores an archived one.
SELECT * FROM staff.competence_profiles ORDER BY key, id;

-- name: GetCompetenceProfile :one
SELECT * FROM staff.competence_profiles WHERE id = @id;

-- name: LockCompetenceProfile :one
-- LockCompetenceProfile holds one profile for a change of its own, so the
-- If-Match revision is judged against the row the change then writes; an
-- assignment sharing it (ShareCompetenceProfile) waits for it.
SELECT * FROM staff.competence_profiles WHERE id = @id FOR NO KEY UPDATE;

-- name: ShareCompetenceProfile :one
-- ShareCompetenceProfile reads the profile an assignment or a department's
-- default is about to name and holds it against an archive until the
-- transaction ends, as ShareCompetence holds a competence.
SELECT * FROM staff.competence_profiles WHERE id = @id FOR SHARE;

-- name: InsertCompetenceProfile :one
-- InsertCompetenceProfile adds a profile; its items are inserted after it.
INSERT INTO staff.competence_profiles (key, name_en, name_nb, created_at, updated_at)
VALUES (@key, @name_en, @name_nb, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateCompetenceProfile :one
-- UpdateCompetenceProfile replaces a profile's names and archives or
-- restores it; an archive keeps the instant it was first archived at. Its key
-- never changes; its items are replaced beside it.
UPDATE staff.competence_profiles SET
    name_en = @name_en,
    name_nb = @name_nb,
    archived_at = CASE WHEN @archived::boolean THEN coalesce(archived_at, @now::timestamptz) END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ListCompetenceProfileItems :many
-- ListCompetenceProfileItems is the items of @profile_ids, each with its
-- competence's summary, validity and lead, by profile and then in the
-- catalogue's order.
SELECT sqlc.embed(i),
       c.key AS competence_key, c.name_en AS competence_name_en, c.name_nb AS competence_name_nb,
       c.kind AS competence_kind, c.has_level AS competence_has_level, c.category_id AS competence_category_id,
       c.archived_at AS competence_archived_at, c.hidden AS competence_hidden,
       c.validity_months AS competence_validity_months, c.reminder_lead_days AS competence_reminder_lead_days
FROM staff.competence_profile_items i
JOIN staff.competences c ON c.id = i.competence_id
JOIN staff.competence_categories k ON k.id = c.category_id
WHERE i.profile_id = ANY(@profile_ids::bigint[])
ORDER BY i.profile_id, k.sort, k.id, c.sort, c.id;

-- name: InsertCompetenceProfileItem :exec
-- InsertCompetenceProfileItem adds one item to a profile.
INSERT INTO staff.competence_profile_items (profile_id, competence_id, min_level, must_be_valid, created_at)
VALUES (@profile_id, @competence_id, sqlc.narg(min_level), @must_be_valid, @now::timestamptz);

-- name: UpdateCompetenceProfileItem :exec
-- UpdateCompetenceProfileItem replaces the minimum level and the validity
-- rule of the profile's item on a competence.
UPDATE staff.competence_profile_items SET
    min_level = sqlc.narg(min_level),
    must_be_valid = @must_be_valid
WHERE profile_id = @profile_id AND competence_id = @competence_id;

-- name: DeleteCompetenceProfileItemsNotIn :execrows
-- DeleteCompetenceProfileItemsNotIn removes the profile's items on every
-- competence but the ones kept.
DELETE FROM staff.competence_profile_items
WHERE profile_id = @profile_id AND NOT (competence_id = ANY(@keep::bigint[]));

-- name: ListPersonProfiles :many
-- ListPersonProfiles is the profiles assigned to a person, archived ones
-- included, by key.
SELECT sqlc.embed(p)
FROM staff.person_profiles pp
JOIN staff.competence_profiles p ON p.id = pp.profile_id
WHERE pp.person_id = @person_id
ORDER BY p.key, p.id;

-- name: ListAllPersonProfiles :many
-- ListAllPersonProfiles is every assignment, by person, for the competenceGap
-- item.
SELECT * FROM staff.person_profiles ORDER BY person_id, profile_id;

-- name: InsertPersonProfile :exec
-- InsertPersonProfile assigns one profile to a person.
INSERT INTO staff.person_profiles (person_id, profile_id, created_at)
VALUES (@person_id, @profile_id, @now::timestamptz);

-- name: DeletePersonProfilesNotIn :execrows
-- DeletePersonProfilesNotIn removes the person's assignments of every profile
-- but the ones kept.
DELETE FROM staff.person_profiles
WHERE person_id = @person_id AND NOT (profile_id = ANY(@keep::bigint[]));

-- name: DeletePersonProfiles :exec
-- DeletePersonProfiles removes every assignment of a person deleted as
-- created in error.
DELETE FROM staff.person_profiles WHERE person_id = @person_id;

-- name: ListCurrentCompetenceRowsOf :many
-- ListCurrentCompetenceRowsOf is the current rows of @person_ids, valid or
-- not, each with its competence's kind, level, validity, lead and archive —
-- what the gap rule judges an item on (S13).
SELECT pc.person_id, pc.competence_id, pc.level, pc.verified_at, pc.valid_from, pc.valid_to,
       c.kind AS competence_kind, c.has_level AS competence_has_level,
       c.validity_months AS competence_validity_months, c.reminder_lead_days AS competence_reminder_lead_days,
       c.archived_at AS competence_archived_at
FROM staff.person_competences pc
JOIN staff.competences c ON c.id = pc.competence_id
WHERE pc.person_id = ANY(@person_ids::bigint[]) AND pc.superseded_by_id IS NULL
ORDER BY pc.person_id, pc.competence_id;

-- name: MatrixRowsOn :many
-- MatrixRowsOn is everyone's current rows valid on the day, with the
-- competence's summary, by person and then in the catalogue's order;
-- category_id, when given, keeps one category's. The matrix keeps the people
-- active that day and not erased, and the competences and levels selected.
SELECT pc.person_id, pc.competence_id, pc.level, pc.verified_at, pc.valid_from, pc.valid_to,
       c.key AS competence_key, c.name_en AS competence_name_en, c.name_nb AS competence_name_nb,
       c.kind AS competence_kind, c.has_level AS competence_has_level, c.category_id AS competence_category_id,
       c.archived_at AS competence_archived_at, c.hidden AS competence_hidden
FROM staff.person_competences pc
JOIN staff.competences c ON c.id = pc.competence_id
JOIN staff.competence_categories k ON k.id = c.category_id
WHERE pc.superseded_by_id IS NULL
  AND pc.valid_from <= @day::date
  AND (pc.valid_to IS NULL OR pc.valid_to >= @day::date)
  AND (sqlc.narg(category_id)::bigint IS NULL OR c.category_id = sqlc.narg(category_id)::bigint)
ORDER BY pc.person_id, k.sort, k.id, c.sort, c.id;

-- name: ListAllInterests :many
-- ListAllInterests is every person's interests in the catalogue's order, then
-- by the person's name, each with its competence, whether the person holds a
-- current row of it — as ListPersonInterests judges it — and the person's
-- name, number and erasure, for the interests overview.
SELECT sqlc.embed(i),
       c.key AS competence_key, c.name_en AS competence_name_en, c.name_nb AS competence_name_nb,
       c.kind AS competence_kind, c.has_level AS competence_has_level, c.category_id AS competence_category_id,
       c.archived_at AS competence_archived_at, c.hidden AS competence_hidden,
       EXISTS (
           SELECT 1 FROM staff.person_competences pc
           WHERE pc.person_id = i.person_id AND pc.competence_id = i.competence_id AND pc.superseded_by_id IS NULL
       ) AS held,
       p.display_name AS person_display_name, p.employee_number AS person_employee_number,
       p.erased_at AS person_erased_at
FROM staff.person_interests i
JOIN staff.competences c ON c.id = i.competence_id
JOIN staff.competence_categories k ON k.id = c.category_id
JOIN staff.people p ON p.id = i.person_id
ORDER BY k.sort, k.id, c.sort, c.id, p.display_name, p.id;

-- name: DepartmentsWithDefaultProfile :many
-- DepartmentsWithDefaultProfile is every department with a default profile,
-- archived or not, for the competenceGap item.
SELECT id, competence_profile_id, name, code FROM staff.departments
WHERE competence_profile_id IS NOT NULL
ORDER BY id;
