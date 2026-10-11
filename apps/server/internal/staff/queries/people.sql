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

-- name: EmployeeNumberTaken :one
-- EmployeeNumberTaken is whether a person has the number however it is
-- cased — the unique index's own question, asked before a generated number
-- is used so the sequence skips past a number HR assigned by hand.
SELECT EXISTS (
    SELECT 1 FROM staff.people WHERE upper(employee_number) = upper(@employee_number::text)
) AS taken;

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
-- is no search (unnest of an empty array yields no rows).
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
);

-- name: ListPeople :many
-- ListPeople is one page of the directory, by display name, the work group's
-- columns only.
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
