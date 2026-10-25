-- Competence (staff competence design S1–S7, §3.1–3.3, delivery A): the
-- catalogue — its categories and the competences — and a person's
-- competences, declared, verified, renewed as new rows and stamped by the
-- staff-competence worker ahead of expiry. A catalogue write locks its row
-- (FOR NO KEY UPDATE); a person's write runs under its person's lock
-- (withPersonLock), then shares the competence it names (ShareCompetence), then
-- locks its own row: person → competence → person competence (§4 Locking).
-- The joined person rows read the same columns in the same order in
-- ListPersonCompetences, ListPersonCompetenceHistory and GetPersonCompetence,
-- so their generated rows convert one to another.

-- name: ListCompetenceCategories :many
-- ListCompetenceCategories is every category in its order, archived ones
-- included; the caller leaves out what its audience does not read.
SELECT * FROM staff.competence_categories ORDER BY sort, id;

-- name: LockCompetenceCategory :one
-- LockCompetenceCategory holds one category for a change of its own, so the
-- If-Match revision is judged against the row the change then writes.
SELECT * FROM staff.competence_categories WHERE id = @id FOR NO KEY UPDATE;

-- name: InsertCompetenceCategory :one
-- InsertCompetenceCategory adds a company category after every other one.
INSERT INTO staff.competence_categories (key, name_en, name_nb, sort, created_at, updated_at)
VALUES (
    @key, @name_en, @name_nb,
    (SELECT coalesce(max(sort), 0) + 1 FROM staff.competence_categories), @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateCompetenceCategory :one
-- UpdateCompetenceCategory replaces a category's names and archives or
-- restores it; an archive keeps the instant it was first archived at.
UPDATE staff.competence_categories SET
    name_en = @name_en,
    name_nb = @name_nb,
    archived_at = CASE WHEN @archived::boolean THEN coalesce(archived_at, @now::timestamptz) END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: CompetenceCategoryInUse :one
-- CompetenceCategoryInUse is whether a competence not archived is filed under
-- the category, which an archive of the category refuses (category_in_use).
SELECT EXISTS (
    SELECT 1 FROM staff.competences WHERE category_id = @category_id AND archived_at IS NULL
) AS in_use;

-- name: ListCompetences :many
-- ListCompetences is the catalogue in its categories' order, then its own;
-- include_hidden and include_archived add the rows only staff:manage lists,
-- and category_id, when given, keeps one category's.
SELECT c.*
FROM staff.competences c
JOIN staff.competence_categories k ON k.id = c.category_id
WHERE (@include_hidden::boolean OR NOT c.hidden)
  AND (@include_archived::boolean OR c.archived_at IS NULL)
  AND (sqlc.narg(category_id)::bigint IS NULL OR c.category_id = sqlc.narg(category_id)::bigint)
ORDER BY k.sort, k.id, c.sort, c.id;

-- name: GetCompetence :one
SELECT * FROM staff.competences WHERE id = @id;

-- name: LockCompetence :one
-- LockCompetence holds one competence for a change of its own, so the
-- If-Match revision and CompetenceInUse are judged against the row the change
-- then writes; a person's write sharing it (ShareCompetence) waits for it.
SELECT * FROM staff.competences WHERE id = @id FOR NO KEY UPDATE;

-- name: ShareCompetence :one
-- ShareCompetence reads the competence a person's row is about to name and
-- holds it against a change — hidden, archived, its kind or level — until the
-- transaction ends, as ShareDocumentCategory holds a category.
SELECT * FROM staff.competences WHERE id = @id FOR SHARE;

-- name: NextCompetenceSort :one
-- NextCompetenceSort is the place after every competence there is.
SELECT (coalesce(max(sort), 0) + 1)::integer AS next FROM staff.competences;

-- name: InsertCompetence :one
-- InsertCompetence adds a company competence; never a seed.
INSERT INTO staff.competences (
    category_id, key, name_en, name_nb, kind, has_level, validity_months, reminder_lead_days, issuer_hint,
    sort, created_at, updated_at
) VALUES (
    @category_id, @key, @name_en, @name_nb, @kind, @has_level, sqlc.narg(validity_months), sqlc.narg(reminder_lead_days),
    sqlc.narg(issuer_hint), @sort, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateCompetence :one
-- UpdateCompetence replaces a competence's category, names, level, validity,
-- lead, issuer hint and hidden flag, and archives or restores it; an archive
-- keeps the instant it was first archived at. Its key, kind and basis never
-- change here; a seed's fixed fields are the caller's to keep as they are.
UPDATE staff.competences SET
    category_id = @category_id,
    name_en = @name_en,
    name_nb = @name_nb,
    has_level = @has_level,
    validity_months = sqlc.narg(validity_months),
    reminder_lead_days = sqlc.narg(reminder_lead_days),
    issuer_hint = sqlc.narg(issuer_hint),
    hidden = @hidden,
    archived_at = CASE WHEN @archived::boolean THEN coalesce(archived_at, @now::timestamptz) END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: CompetenceInUse :one
-- CompetenceInUse is whether any person's row, current or superseded, or any
-- profile item with a minimum level names the competence, which fixes a
-- company competence's key and kind (competence_in_use) — an item's level is
-- never orphaned (delivery C, reading 28). Asked under LockCompetence, which
-- a person's write sharing the competence waits for.
SELECT (
    EXISTS (SELECT 1 FROM staff.person_competences pc WHERE pc.competence_id = @competence_id::bigint)
    OR EXISTS (
        SELECT 1 FROM staff.competence_profile_items i
        WHERE i.competence_id = @competence_id::bigint AND i.min_level IS NOT NULL
    )
)::boolean AS in_use;

-- name: ListPersonCompetences :many
-- ListPersonCompetences is a person's current rows in the catalogue's order,
-- each with its competence and its evidence document, when it has one.
SELECT sqlc.embed(pc),
       c.key AS competence_key, c.name_en AS competence_name_en, c.name_nb AS competence_name_nb,
       c.kind AS competence_kind, c.has_level AS competence_has_level, c.category_id AS competence_category_id,
       c.archived_at AS competence_archived_at, c.reminder_lead_days AS competence_reminder_lead_days,
       d.file_name AS evidence_file_name, d.content_type AS evidence_content_type, d.size_bytes AS evidence_size_bytes
FROM staff.person_competences pc
JOIN staff.competences c ON c.id = pc.competence_id
JOIN staff.competence_categories k ON k.id = c.category_id
LEFT JOIN staff.documents d ON d.id = pc.evidence_document_id
WHERE pc.person_id = @person_id AND pc.superseded_by_id IS NULL
ORDER BY k.sort, k.id, c.sort, c.id;

-- name: ListPersonCompetenceHistory :many
-- ListPersonCompetenceHistory is every row of a person's, superseded ones
-- included, newest first, joined as ListPersonCompetences.
SELECT sqlc.embed(pc),
       c.key AS competence_key, c.name_en AS competence_name_en, c.name_nb AS competence_name_nb,
       c.kind AS competence_kind, c.has_level AS competence_has_level, c.category_id AS competence_category_id,
       c.archived_at AS competence_archived_at, c.reminder_lead_days AS competence_reminder_lead_days,
       d.file_name AS evidence_file_name, d.content_type AS evidence_content_type, d.size_bytes AS evidence_size_bytes
FROM staff.person_competences pc
JOIN staff.competences c ON c.id = pc.competence_id
LEFT JOIN staff.documents d ON d.id = pc.evidence_document_id
WHERE pc.person_id = @person_id
ORDER BY pc.created_at DESC, pc.id DESC;

-- name: GetPersonCompetence :one
-- GetPersonCompetence is one row, joined as ListPersonCompetences.
SELECT sqlc.embed(pc),
       c.key AS competence_key, c.name_en AS competence_name_en, c.name_nb AS competence_name_nb,
       c.kind AS competence_kind, c.has_level AS competence_has_level, c.category_id AS competence_category_id,
       c.archived_at AS competence_archived_at, c.reminder_lead_days AS competence_reminder_lead_days,
       d.file_name AS evidence_file_name, d.content_type AS evidence_content_type, d.size_bytes AS evidence_size_bytes
FROM staff.person_competences pc
JOIN staff.competences c ON c.id = pc.competence_id
LEFT JOIN staff.documents d ON d.id = pc.evidence_document_id
WHERE pc.id = @id;

-- name: LockPersonCompetence :one
-- LockPersonCompetence takes a person's row after its person's lock, for the
-- rest of the transaction.
SELECT * FROM staff.person_competences WHERE id = @id FOR NO KEY UPDATE;

-- name: CurrentPersonCompetenceOf :one
-- CurrentPersonCompetenceOf is the person's current row for the competence,
-- if they hold one (competence_held).
SELECT * FROM staff.person_competences
WHERE person_id = @person_id AND competence_id = @competence_id AND superseded_by_id IS NULL;

-- name: InsertPersonCompetence :one
-- InsertPersonCompetence writes a declared row, or a verified one when the
-- verifier or HR adds or renews it.
INSERT INTO staff.person_competences (
    person_id, competence_id, level, issued_on, valid_from, valid_to, issuer, reference, evidence_document_id,
    source, verified_at, verified_by_user_id, verified_basis, verified_note, created_at, updated_at
) VALUES (
    @person_id, @competence_id, sqlc.narg(level), sqlc.narg(issued_on), @valid_from, sqlc.narg(valid_to),
    sqlc.narg(issuer), sqlc.narg(reference), sqlc.narg(evidence_document_id), @source,
    sqlc.narg(verified_at), sqlc.narg(verified_by_user_id), sqlc.narg(verified_basis), sqlc.narg(verified_note),
    @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdatePersonCompetence :one
-- UpdatePersonCompetence replaces a row's level, dates, issuer and reference.
-- A new end clears the worker's two stamps, so the row is mailed again ahead
-- of it; restamp moves the verification's instant and verifier to the
-- verifier changing a verified row's level, its basis and note kept;
-- clear_seen drops the one-to-one's Seen stamp, for the person's own change
-- of their declared row (staff competence design S8, amendment 17).
UPDATE staff.person_competences SET
    level = sqlc.narg(level),
    issued_on = sqlc.narg(issued_on),
    valid_from = @valid_from,
    valid_to = sqlc.narg(valid_to)::date,
    issuer = sqlc.narg(issuer),
    reference = sqlc.narg(reference),
    expiring_notified_at = CASE WHEN valid_to IS DISTINCT FROM sqlc.narg(valid_to)::date THEN NULL ELSE expiring_notified_at END,
    expired_notified_at = CASE WHEN valid_to IS DISTINCT FROM sqlc.narg(valid_to)::date THEN NULL ELSE expired_notified_at END,
    verified_at = CASE WHEN @restamp::boolean THEN @now::timestamptz ELSE verified_at END,
    verified_by_user_id = CASE WHEN @restamp::boolean THEN @verifier::uuid ELSE verified_by_user_id END,
    seen_at = CASE WHEN @clear_seen::boolean THEN NULL ELSE seen_at END,
    seen_by_user_id = CASE WHEN @clear_seen::boolean THEN NULL ELSE seen_by_user_id END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: VerifyPersonCompetence :one
-- VerifyPersonCompetence stamps a row verified, with the level the verifier
-- agrees to; a verified row verified again moves the stamp.
UPDATE staff.person_competences SET
    level = sqlc.narg(level),
    verified_at = @verified_at::timestamptz,
    verified_by_user_id = @verified_by_user_id::uuid,
    verified_basis = @verified_basis::text,
    verified_note = sqlc.narg(verified_note),
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SupersedePersonCompetence :exec
-- SupersedePersonCompetence points a renewed row at the row that renews it,
-- leaving its dates, evidence and stamps as they were.
UPDATE staff.person_competences SET
    superseded_by_id = @superseded_by_id::bigint,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: SetPersonCompetenceEvidence :one
-- SetPersonCompetenceEvidence names the row's evidence document; an earlier
-- one stays in the archive. clear_seen drops the one-to-one's Seen stamp, for
-- the person's own upload (staff competence design S8, amendment 17).
UPDATE staff.person_competences SET
    evidence_document_id = @evidence_document_id::bigint,
    seen_at = CASE WHEN @clear_seen::boolean THEN NULL ELSE seen_at END,
    seen_by_user_id = CASE WHEN @clear_seen::boolean THEN NULL ELSE seen_by_user_id END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: RestorePredecessorOf :many
-- RestorePredecessorOf makes the row a removed renewal superseded current
-- again, answering its id. The removed row points at itself first, so the
-- person holds one current row of the competence at every statement.
UPDATE staff.person_competences SET
    superseded_by_id = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE superseded_by_id = @id::bigint AND id <> @id::bigint
RETURNING id;

-- name: DeletePersonCompetence :execrows
-- DeletePersonCompetence removes one current row.
DELETE FROM staff.person_competences WHERE id = @id;

-- name: DeletePersonCompetenceChain :execrows
-- DeletePersonCompetenceChain removes every row the person has had for the
-- competence, in one statement, so the superseded rows go with the row they
-- point at.
DELETE FROM staff.person_competences WHERE person_id = @person_id AND competence_id = @competence_id;

-- name: ListDeclaredCompetences :many
-- ListDeclaredCompetences is every current row not verified, with its person
-- and its competence, for the attention items; the caller keeps the active
-- people's.
SELECT sqlc.embed(pc),
       p.display_name AS person_display_name, p.erased_at AS person_erased_at,
       c.key AS competence_key, c.name_en AS competence_name_en, c.name_nb AS competence_name_nb,
       c.kind AS competence_kind
FROM staff.person_competences pc
JOIN staff.people p ON p.id = pc.person_id
JOIN staff.competences c ON c.id = pc.competence_id
WHERE pc.superseded_by_id IS NULL AND pc.verified_at IS NULL
ORDER BY pc.created_at, pc.id;

-- name: ListExpiringCompetences :many
-- ListExpiringCompetences is every current row with an end, with its person
-- and its competence's lead, for the worker and the attention items; the
-- caller judges the lead and keeps the active people's.
SELECT sqlc.embed(pc),
       p.display_name AS person_display_name, p.erased_at AS person_erased_at,
       c.key AS competence_key, c.name_en AS competence_name_en, c.name_nb AS competence_name_nb,
       c.kind AS competence_kind, c.reminder_lead_days AS competence_reminder_lead_days
FROM staff.person_competences pc
JOIN staff.people p ON p.id = pc.person_id
JOIN staff.competences c ON c.id = pc.competence_id
WHERE pc.superseded_by_id IS NULL AND pc.valid_to IS NOT NULL
ORDER BY pc.valid_to, pc.id;

-- name: MarkCompetenceExpiringNotified :exec
-- MarkCompetenceExpiringNotified stamps a row's expiring mail as the worker's
-- run, before it mails (plan reading 4).
UPDATE staff.person_competences SET expiring_notified_at = @now::timestamptz WHERE id = @id;

-- name: MarkCompetenceExpiredNotified :exec
-- MarkCompetenceExpiredNotified stamps a row's expired mail, as
-- MarkCompetenceExpiringNotified.
UPDATE staff.person_competences SET expired_notified_at = @now::timestamptz WHERE id = @id;

-- name: HeldCompetencesOn :many
-- HeldCompetencesOn is the people's current rows valid on the day, with the
-- competence's key and kind, for contracts.Workforce; the caller drops a
-- declared certificate and the people it may not see.
SELECT pc.person_id, pc.competence_id, c.key AS competence_key, c.kind AS competence_kind, pc.level,
       pc.verified_at, pc.valid_from, pc.valid_to
FROM staff.person_competences pc
JOIN staff.competences c ON c.id = pc.competence_id
WHERE pc.person_id = ANY(@person_ids::bigint[])
  AND pc.superseded_by_id IS NULL
  AND pc.valid_from <= @day::date
  AND (pc.valid_to IS NULL OR pc.valid_to >= @day::date)
ORDER BY pc.person_id, c.sort, c.id;
