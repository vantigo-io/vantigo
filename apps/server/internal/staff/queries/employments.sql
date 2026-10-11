-- The employments (design S2, §3.2) and their dated terms (S5, §3.3). Every
-- write here runs under the person's lock (withPersonLock), the employment's
-- row after it; a write that changes an edge of the manager graph takes the
-- graph's advisory lock after both.

-- name: GetEmployment :one
SELECT * FROM staff.employments WHERE id = @id;

-- name: LockEmployment :one
-- LockEmployment takes an employment's row after its person's, for the rest
-- of the transaction. FOR NO KEY UPDATE, like the person's: a terms row's
-- foreign key takes KEY SHARE on it, which this does not block.
SELECT * FROM staff.employments WHERE id = @id FOR NO KEY UPDATE;

-- name: ListPersonEmployments :many
SELECT * FROM staff.employments WHERE person_id = @person_id ORDER BY start_date, id;

-- name: EmploymentNumberTaken :one
-- EmploymentNumberTaken is the unique index's own question, asked before the
-- insert so a typed number another employment holds is refused by name.
SELECT EXISTS (
    SELECT 1 FROM staff.employments WHERE upper(employment_number) = upper(@employment_number)
) AS taken;

-- name: FirstFreeEmploymentNumber :one
-- FirstFreeEmploymentNumber is the first n from 1 whose number — the
-- employee number, a hyphen and n — no employment has however it is cased,
-- so a generated number skips past one typed by hand. Among 1 .. the number
-- of employments + 1 at least one n is free.
WITH probe AS (
    SELECT @employee_number::text AS prefix, (SELECT count(*) FROM staff.employments) AS employments
)
SELECT candidate.n::bigint AS n
FROM probe, generate_series(1, probe.employments + 1) AS candidate(n)
WHERE NOT EXISTS (
    SELECT 1 FROM staff.employments e WHERE upper(e.employment_number) = upper(probe.prefix || '-' || candidate.n::text)
)
ORDER BY candidate.n
LIMIT 1;

-- name: InsertEmployment :one
INSERT INTO staff.employments (
    person_id, employment_number, relationship_type, business_unit_id, start_date, contract_signed_at, created_at, updated_at
) VALUES (
    @person_id, @employment_number, @relationship_type, @business_unit_id, @start_date, @contract_signed_at, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateEmployment :one
-- UpdateEmployment replaces an employment's own fields. The number, the end
-- and its reason are not among them.
UPDATE staff.employments SET
    relationship_type = @relationship_type,
    business_unit_id = @business_unit_id,
    start_date = @start_date,
    contract_signed_at = @contract_signed_at,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SetEmploymentEnd :one
-- SetEmploymentEnd ends an employment, or — with both NULL — reopens it.
UPDATE staff.employments SET
    end_date = @end_date,
    end_reason = @end_reason,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DefaultBusinessUnit :one
-- DefaultBusinessUnit is the unit an employment that names none is put in,
-- held against an archive until the transaction ends. No row is an
-- installation with no business unit yet.
SELECT * FROM staff.business_units WHERE is_default FOR SHARE;

-- name: ShareBusinessUnit :one
-- ShareBusinessUnit reads the unit an employment is about to name and holds
-- it against an archive until the transaction ends.
SELECT * FROM staff.business_units WHERE id = @id FOR SHARE;

-- name: ShareLocation :one
-- ShareLocation reads the location a terms row is about to name and holds it
-- against an archive until the transaction ends.
SELECT * FROM staff.locations WHERE id = @id FOR SHARE;

-- name: ListTerms :many
-- ListTerms is an employment's terms chain, oldest first.
SELECT * FROM staff.employment_terms WHERE employment_id = @employment_id ORDER BY valid_from, id;

-- name: ListTermsOfEmployments :many
-- ListTermsOfEmployments is the chains of several employments, each oldest
-- first.
SELECT * FROM staff.employment_terms WHERE employment_id = ANY(@employment_ids::bigint[]) ORDER BY employment_id, valid_from, id;

-- name: GetTerms :one
SELECT * FROM staff.employment_terms WHERE id = @id;

-- name: InsertTerms :one
INSERT INTO staff.employment_terms (
    employment_id, valid_from, employment_type, temporary_ground, expected_end_date, appointment_form, hired_out_to,
    fte_percent, working_hours_scheme, full_time_hours_per_week, occupation_code, title, employee_type,
    remuneration_type, pay_components, pay_method, payday_text, department_id, location_id, manager_person_id,
    cost_centre, division, working_time_text, break_minutes, special_hours_text, overtime_rule_text,
    probation_end_date, probation_extension_agreed, probation_extended_to, probation_extension_reason,
    notice_employee, notice_employer, notice_unit, tariff_reference, holiday_terms_text,
    competence_development_text, social_security_text, seniority_date, pay_grade, note, created_at, updated_at
) VALUES (
    @employment_id, @valid_from, @employment_type, @temporary_ground, @expected_end_date, @appointment_form, @hired_out_to,
    @fte_percent, @working_hours_scheme, @full_time_hours_per_week, @occupation_code, @title, @employee_type,
    @remuneration_type, @pay_components, @pay_method, @payday_text, @department_id, @location_id, @manager_person_id,
    @cost_centre, @division, @working_time_text, @break_minutes, @special_hours_text, @overtime_rule_text,
    @probation_end_date, @probation_extension_agreed, @probation_extended_to, @probation_extension_reason,
    @notice_employee, @notice_employer, @notice_unit, @tariff_reference, @holiday_terms_text,
    @competence_development_text, @social_security_text, @seniority_date, @pay_grade, @note, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateTerms :one
UPDATE staff.employment_terms SET
    valid_from = @valid_from,
    employment_type = @employment_type,
    temporary_ground = @temporary_ground,
    expected_end_date = @expected_end_date,
    appointment_form = @appointment_form,
    hired_out_to = @hired_out_to,
    fte_percent = @fte_percent,
    working_hours_scheme = @working_hours_scheme,
    full_time_hours_per_week = @full_time_hours_per_week,
    occupation_code = @occupation_code,
    title = @title,
    employee_type = @employee_type,
    remuneration_type = @remuneration_type,
    pay_components = @pay_components,
    pay_method = @pay_method,
    payday_text = @payday_text,
    department_id = @department_id,
    location_id = @location_id,
    manager_person_id = @manager_person_id,
    cost_centre = @cost_centre,
    division = @division,
    working_time_text = @working_time_text,
    break_minutes = @break_minutes,
    special_hours_text = @special_hours_text,
    overtime_rule_text = @overtime_rule_text,
    probation_end_date = @probation_end_date,
    probation_extension_agreed = @probation_extension_agreed,
    probation_extended_to = @probation_extended_to,
    probation_extension_reason = @probation_extension_reason,
    notice_employee = @notice_employee,
    notice_employer = @notice_employer,
    notice_unit = @notice_unit,
    tariff_reference = @tariff_reference,
    holiday_terms_text = @holiday_terms_text,
    competence_development_text = @competence_development_text,
    social_security_text = @social_security_text,
    seniority_date = @seniority_date,
    pay_grade = @pay_grade,
    note = @note,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: MoveTermsValidFrom :exec
-- MoveTermsValidFrom moves the first terms row with its employment's start.
UPDATE staff.employment_terms SET valid_from = @valid_from, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id;

-- name: DeleteTerms :exec
DELETE FROM staff.employment_terms WHERE id = @id;

-- name: GraphEmployments :many
-- GraphEmployments is the spans of the employments of @person_ids — of
-- everyone when it is NULL — the manager graph's nodes over time, with the
-- business unit a person's place today names. The cycle check reads
-- everyone's under the manager-graph lock; the org view, the placement and
-- the Manager audience read outside it.
SELECT id, person_id, business_unit_id, start_date, end_date FROM staff.employments
WHERE sqlc.narg('person_ids')::bigint[] IS NULL OR person_id = ANY(sqlc.narg('person_ids')::bigint[]);

-- name: GraphTerms :many
-- GraphTerms is the edges of the terms rows of @person_ids' employments —
-- of everyone's when it is NULL: the manager each names, and the department
-- whose manager is the fallback — with the title and the location a
-- person's place today names.
SELECT t.employment_id, t.valid_from, t.department_id, t.manager_person_id, t.title, t.location_id
FROM staff.employment_terms t JOIN staff.employments e ON e.id = t.employment_id
WHERE sqlc.narg('person_ids')::bigint[] IS NULL OR e.person_id = ANY(sqlc.narg('person_ids')::bigint[]);

-- name: PeopleSummaries :many
-- PeopleSummaries names people as the work group does.
SELECT id, display_name, employee_number FROM staff.people WHERE id = ANY(@ids::bigint[]);

-- name: DepartmentsByID :many
SELECT * FROM staff.departments WHERE id = ANY(@ids::bigint[]);

-- name: ListAllEmployments :many
-- ListAllEmployments is every employment, by person and start: the
-- register's export reads them whole, with ListTermsOfEmployments' chains,
-- to add up what is in force today.
SELECT * FROM staff.employments ORDER BY person_id, start_date, id;
