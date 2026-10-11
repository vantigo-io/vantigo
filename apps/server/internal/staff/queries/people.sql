-- The people (design S2, §3.1), their next of kin and their roles. No query
-- here reads a sealed column: a person is read as GetPerson's columns, which
-- answer whether a national identity number is on file and the bank account's
-- kind and last four digits, and nothing a reveal would.

-- name: GetPerson :one
-- GetPerson is one person as every response of theirs is built from.
SELECT
    id, employee_number, given_name, family_name, display_name, birth_date, gender,
    national_id_kind, (national_id_mac IS NOT NULL)::boolean AS national_id_on_file,
    work_email, phone_work, phone_mobile, phone_private, private_email,
    street, postal_code, city, country, work_country, preferred_language,
    bank_account_kind, bank_account_last4, notes, revision
FROM staff.people
WHERE id = @id;

-- name: PersonLinkedUser :one
-- PersonLinkedUser is the user a person's self-service link names (design
-- S24), as the link stores it: the user's id as text. No row is a person
-- linked to nobody.
SELECT external_id FROM staff.identity_links
WHERE person_id = @person_id AND provider = 'identity';

-- name: FirstFreeEmployeeNumber :one
-- FirstFreeEmployeeNumber is the first n from @next_number whose number —
-- the prefix and n, unpadded — no person has however it is cased: the unique
-- index's own question, asked in one statement so the sequence skips past
-- every number HR assigned by hand. Among next .. next + the number of
-- people at least one n is free, so the series is bounded by the table.
WITH probe AS (
    SELECT @next_number::bigint AS first, @prefix::text AS prefix, (SELECT count(*) FROM staff.people) AS people
)
SELECT candidate.n::bigint AS n
FROM probe, generate_series(probe.first, probe.first + probe.people) AS candidate(n)
WHERE NOT EXISTS (
    SELECT 1 FROM staff.people p WHERE upper(p.employee_number) = upper(probe.prefix || candidate.n::text)
)
ORDER BY candidate.n
LIMIT 1;

-- name: PersonActiveOn :one
-- PersonActiveOn is whether a person has an employment in force on @today
-- — started, and not ended before it (the end day itself is still worked).
-- It decides the colleague audience (plan reading 15): staff:view alone
-- sees only the people active today.
SELECT EXISTS (
    SELECT 1 FROM staff.employments e
    WHERE e.person_id = @person_id AND e.start_date <= @today::date
      AND (e.end_date IS NULL OR e.end_date >= @today::date)
) AS active;

-- name: BumpPersonRevision :exec
-- BumpPersonRevision moves a person's revision on for a change to a list of
-- theirs — next of kin, roles — which the person's revision guards.
UPDATE staff.people SET revision = revision + 1, updated_at = @now::timestamptz WHERE id = @id;

-- name: AdvanceEmployeeNumber :exec
-- AdvanceEmployeeNumber moves the sequence on past the number just used. It
-- runs under LockSettings. The revision is left alone: it guards HR's replace
-- of the settings, and a sequence that moved under an open settings form is
-- no conflict — a stale next number written back only makes the next
-- generation skip the numbers already taken.
UPDATE staff.settings SET next_employee_number = @next_employee_number WHERE id = 1;

-- name: InsertPerson :one
INSERT INTO staff.people (
    employee_number, given_name, family_name, display_name, birth_date, gender,
    work_email, phone_work, phone_mobile, phone_private, private_email,
    street, postal_code, city, country, work_country, preferred_language, notes,
    created_at, updated_at
) VALUES (
    @employee_number, @given_name, @family_name, @display_name, @birth_date, @gender,
    @work_email, @phone_work, @phone_mobile, @phone_private, @private_email,
    @street, @postal_code, @city, @country, @work_country, @preferred_language, @notes,
    @now::timestamptz, @now::timestamptz
)
RETURNING id;

-- name: UpdatePerson :exec
-- UpdatePerson replaces a person's editable fields and moves the revision on.
-- The employee number, the sealed fields, the flags and the link are not
-- among them.
UPDATE staff.people SET
    given_name = @given_name,
    family_name = @family_name,
    display_name = @display_name,
    birth_date = @birth_date,
    gender = @gender,
    work_email = @work_email,
    phone_work = @phone_work,
    phone_mobile = @phone_mobile,
    phone_private = @phone_private,
    private_email = @private_email,
    street = @street,
    postal_code = @postal_code,
    city = @city,
    country = @country,
    work_country = @work_country,
    preferred_language = @preferred_language,
    notes = @notes,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: CountPeople :one
-- CountPeople is ListPeople's total. @patterns holds one ILIKE-escaped
-- "%word%" per word of the search; a person passes when every pattern
-- matches at least one of the searched fields, and everyone passes when there
-- is no search (unnest of an empty array yields no rows). @active_only keeps
-- the people active on @today — the colleague's directory (plan reading 15),
-- judged as PersonActiveOn judges one person. @status is staff:manage's
-- filter on where people stand on @today: 'current' — someone active or
-- starting later (an employment not ended before today) or with no
-- employment at all — 'ended' (every employment ended before today) or
-- 'all'. @unit_id, @department_id and @location_id, each when given, keep
-- the people with one employment in force on @today in that unit whose terms
-- in force that day name that department and location.
SELECT count(*)
FROM staff.people p
WHERE NOT EXISTS (
    SELECT 1 FROM unnest(@patterns::text[]) AS s(pattern)
    WHERE NOT (
        p.given_name ILIKE s.pattern OR
        p.family_name ILIKE s.pattern OR
        p.display_name ILIKE s.pattern OR
        p.employee_number ILIKE s.pattern OR
        coalesce(p.work_email, '') ILIKE s.pattern
    )
)
AND (NOT @active_only::boolean OR EXISTS (
    SELECT 1 FROM staff.employments e
    WHERE e.person_id = p.id AND e.start_date <= @today::date
      AND (e.end_date IS NULL OR e.end_date >= @today::date)
))
AND CASE @status::text
    WHEN 'all' THEN true
    WHEN 'ended' THEN
        EXISTS (SELECT 1 FROM staff.employments e WHERE e.person_id = p.id)
        AND NOT EXISTS (
            SELECT 1 FROM staff.employments e
            WHERE e.person_id = p.id AND (e.end_date IS NULL OR e.end_date >= @today::date)
        )
    ELSE
        NOT EXISTS (SELECT 1 FROM staff.employments e WHERE e.person_id = p.id)
        OR EXISTS (
            SELECT 1 FROM staff.employments e
            WHERE e.person_id = p.id AND (e.end_date IS NULL OR e.end_date >= @today::date)
        )
END
AND (
    (sqlc.narg('unit_id')::bigint IS NULL AND sqlc.narg('department_id')::bigint IS NULL AND sqlc.narg('location_id')::bigint IS NULL)
    OR EXISTS (
        SELECT 1 FROM staff.employments e
        LEFT JOIN LATERAL (
            SELECT t.department_id, t.location_id FROM staff.employment_terms t
            WHERE t.employment_id = e.id AND t.valid_from <= @today::date
            ORDER BY t.valid_from DESC
            LIMIT 1
        ) t ON true
        WHERE e.person_id = p.id AND e.start_date <= @today::date
          AND (e.end_date IS NULL OR e.end_date >= @today::date)
          AND (sqlc.narg('unit_id')::bigint IS NULL OR e.business_unit_id = sqlc.narg('unit_id')::bigint)
          AND (sqlc.narg('department_id')::bigint IS NULL OR t.department_id = sqlc.narg('department_id')::bigint)
          AND (sqlc.narg('location_id')::bigint IS NULL OR t.location_id = sqlc.narg('location_id')::bigint)
    )
);

-- name: ListPeople :many
-- ListPeople is one page of the directory, by display name, the work group's
-- columns only, filtered as CountPeople counts.
SELECT p.id, p.employee_number, p.given_name, p.family_name, p.display_name,
       p.work_email, p.phone_work, p.work_country, p.preferred_language
FROM staff.people p
WHERE NOT EXISTS (
    SELECT 1 FROM unnest(@patterns::text[]) AS s(pattern)
    WHERE NOT (
        p.given_name ILIKE s.pattern OR
        p.family_name ILIKE s.pattern OR
        p.display_name ILIKE s.pattern OR
        p.employee_number ILIKE s.pattern OR
        coalesce(p.work_email, '') ILIKE s.pattern
    )
)
AND (NOT @active_only::boolean OR EXISTS (
    SELECT 1 FROM staff.employments e
    WHERE e.person_id = p.id AND e.start_date <= @today::date
      AND (e.end_date IS NULL OR e.end_date >= @today::date)
))
AND CASE @status::text
    WHEN 'all' THEN true
    WHEN 'ended' THEN
        EXISTS (SELECT 1 FROM staff.employments e WHERE e.person_id = p.id)
        AND NOT EXISTS (
            SELECT 1 FROM staff.employments e
            WHERE e.person_id = p.id AND (e.end_date IS NULL OR e.end_date >= @today::date)
        )
    ELSE
        NOT EXISTS (SELECT 1 FROM staff.employments e WHERE e.person_id = p.id)
        OR EXISTS (
            SELECT 1 FROM staff.employments e
            WHERE e.person_id = p.id AND (e.end_date IS NULL OR e.end_date >= @today::date)
        )
END
AND (
    (sqlc.narg('unit_id')::bigint IS NULL AND sqlc.narg('department_id')::bigint IS NULL AND sqlc.narg('location_id')::bigint IS NULL)
    OR EXISTS (
        SELECT 1 FROM staff.employments e
        LEFT JOIN LATERAL (
            SELECT t.department_id, t.location_id FROM staff.employment_terms t
            WHERE t.employment_id = e.id AND t.valid_from <= @today::date
            ORDER BY t.valid_from DESC
            LIMIT 1
        ) t ON true
        WHERE e.person_id = p.id AND e.start_date <= @today::date
          AND (e.end_date IS NULL OR e.end_date >= @today::date)
          AND (sqlc.narg('unit_id')::bigint IS NULL OR e.business_unit_id = sqlc.narg('unit_id')::bigint)
          AND (sqlc.narg('department_id')::bigint IS NULL OR t.department_id = sqlc.narg('department_id')::bigint)
          AND (sqlc.narg('location_id')::bigint IS NULL OR t.location_id = sqlc.narg('location_id')::bigint)
    )
)
ORDER BY p.display_name, p.id
LIMIT @page_size OFFSET @page_offset;

-- name: PersonInUse :one
-- PersonInUse is why a person may not be deleted as created in error (plan
-- reading 5): an employment, a user link, a department naming them as its
-- manager, or any terms naming them as the manager. It is read under the
-- person's lock and the manager-graph lock, which every write of those edges
-- takes, so nothing can start naming the person between this and the delete.
SELECT
    EXISTS (SELECT 1 FROM staff.employments e WHERE e.person_id = @id) AS has_employment,
    EXISTS (SELECT 1 FROM staff.identity_links l WHERE l.person_id = @id) AS has_link,
    EXISTS (SELECT 1 FROM staff.departments d WHERE d.manager_person_id = @id) AS manages_department,
    EXISTS (SELECT 1 FROM staff.employment_terms t WHERE t.manager_person_id = @id) AS named_as_manager;

-- name: DeletePersonEvents :exec
-- DeletePersonEvents is the one exception to the events table's append-only
-- rule (plan reading 5): a person created in error leaves with their history.
DELETE FROM staff.events WHERE person_id = @person_id;

-- name: DeletePerson :exec
DELETE FROM staff.people WHERE id = @id;

-- name: ListNextOfKin :many
SELECT name, relationship, phone, note FROM staff.next_of_kin
WHERE person_id = @person_id
ORDER BY position, id;

-- name: DeleteNextOfKin :exec
DELETE FROM staff.next_of_kin WHERE person_id = @person_id;

-- name: InsertNextOfKin :exec
INSERT INTO staff.next_of_kin (person_id, position, name, relationship, phone, note, created_at, updated_at)
VALUES (@person_id, @position, @name, @relationship, @phone, @note, @now::timestamptz, @now::timestamptz);

-- name: ListPersonRoles :many
-- ListPersonRoles is a person's roles in the order they were last written.
SELECT role, from_date, to_date FROM staff.person_roles
WHERE person_id = @person_id
ORDER BY id;

-- name: DeletePersonRoles :exec
DELETE FROM staff.person_roles WHERE person_id = @person_id;

-- name: InsertPersonRole :exec
INSERT INTO staff.person_roles (person_id, role, from_date, to_date, created_at, updated_at)
VALUES (@person_id, @role, @from_date, @to_date, @now::timestamptz, @now::timestamptz);
