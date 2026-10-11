-- The document archive (design S14, §3.6): the categories, the documents and
-- the ledger of objects still to be removed from the store. Every write of a
-- document runs under its person's lock (withPersonLock), then — for a
-- contract document that sets an employment's signing date — the employment's
-- row, then the document's: person → employment → document (§4 Locking).

-- name: ListDocumentCategories :many
SELECT * FROM staff.document_categories ORDER BY sort, id;

-- name: GetDocumentCategory :one
SELECT * FROM staff.document_categories WHERE id = @id;

-- name: ShareDocumentCategory :one
-- ShareDocumentCategory reads the category a document is about to be filed
-- under and holds it against an archive until the transaction ends.
SELECT * FROM staff.document_categories WHERE id = @id FOR SHARE;

-- name: LockDocumentCategory :one
SELECT * FROM staff.document_categories WHERE id = @id FOR NO KEY UPDATE;

-- name: InsertDocumentCategory :one
-- InsertDocumentCategory adds a company category after every other one.
INSERT INTO staff.document_categories (key, name_en, name_nb, retention_class, restricted, sort, created_at, updated_at)
VALUES (
    @key, @name_en, @name_nb, @retention_class, @restricted,
    (SELECT coalesce(max(sort), 0) + 1 FROM staff.document_categories), @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateDocumentCategory :one
-- UpdateDocumentCategory replaces a category's names and archives or
-- restores it; an archive keeps the instant it was first archived at.
UPDATE staff.document_categories SET
    name_en = @name_en,
    name_nb = @name_nb,
    archived_at = CASE WHEN @archived::boolean THEN coalesce(archived_at, @now::timestamptz) END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DocumentCategoryInUse :one
SELECT EXISTS (SELECT 1 FROM staff.documents WHERE category_id = @category_id) AS in_use;

-- name: ListPersonDocuments :many
-- ListPersonDocuments is a person's archive, newest first, each document with
-- its category; the restricted categories' documents only when asked for.
SELECT sqlc.embed(d), sqlc.embed(c)
FROM staff.documents d
JOIN staff.document_categories c ON c.id = d.category_id
WHERE d.person_id = @person_id AND (@with_restricted::boolean OR NOT c.restricted)
ORDER BY d.created_at DESC, d.id DESC;

-- name: GetDocument :one
SELECT sqlc.embed(d), sqlc.embed(c)
FROM staff.documents d
JOIN staff.document_categories c ON c.id = d.category_id
WHERE d.id = @id;

-- name: LockDocument :one
-- LockDocument takes a document's row after its person's (and, for a
-- contract document, its employment's), for the rest of the transaction.
SELECT * FROM staff.documents WHERE id = @id FOR NO KEY UPDATE;

-- name: CountPersonDocuments :one
SELECT count(*) FROM staff.documents WHERE person_id = @person_id;

-- name: CoveringEmployments :many
-- CoveringEmployments is the person's employments in force on day: started on
-- or before it, not ended before it.
SELECT * FROM staff.employments
WHERE person_id = @person_id AND start_date <= @day::date AND (end_date IS NULL OR end_date >= @day::date)
ORDER BY id;

-- name: PersonHasEmployment :one
SELECT EXISTS (SELECT 1 FROM staff.employments WHERE id = @id AND person_id = @person_id) AS has;

-- name: SetEmploymentContractSigned :one
-- SetEmploymentContractSigned records the day an employment's contract was
-- signed, from a signed contract document filed for it.
UPDATE staff.employments SET
    contract_signed_at = @contract_signed_at,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: InsertDocument :one
INSERT INTO staff.documents (
    person_id, category_id, employment_id, object_key, file_name, content_type, size_bytes, valid_to, signed_at,
    uploaded_by_user_id, created_at, updated_at
) VALUES (
    @person_id, @category_id, @employment_id, @object_key, @file_name, @content_type, @size_bytes, @valid_to, @signed_at,
    @uploaded_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateDocument :one
-- UpdateDocument replaces a document's category, dates and employment; the
-- file never changes.
UPDATE staff.documents SET
    category_id = @category_id,
    employment_id = @employment_id,
    valid_to = @valid_to,
    signed_at = @signed_at,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteDocument :exec
DELETE FROM staff.documents WHERE id = @id;

-- name: InsertObjectDeletion :exec
-- InsertObjectDeletion queues an object for removal, in the transaction that
-- removed the row pointing at it; it is due at once.
INSERT INTO staff.object_deletions (object_key, next_attempt_at, created_at)
VALUES (@object_key, @now::timestamptz, @now::timestamptz)
ON CONFLICT (object_key) DO NOTHING;

-- name: RecordObjectDeletionFailure :exec
-- RecordObjectDeletionFailure records one failed attempt at removing an
-- object on its ledger row: the attempt counted, why, and when the next is due.
UPDATE staff.object_deletions SET
    attempts = attempts + 1,
    last_error = @last_error,
    next_attempt_at = @next_attempt_at::timestamptz
WHERE object_key = @object_key;

-- name: DeleteObjectDeletion :exec
-- DeleteObjectDeletion clears an object's ledger row once the store has
-- confirmed the object is gone.
DELETE FROM staff.object_deletions WHERE object_key = @object_key;
