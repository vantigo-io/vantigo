-- name: ListAttachmentsForEntries :many
-- ListAttachmentsForEntries is the receipts each of the given expenses
-- carries, oldest first, for the attachments (and the attachmentCount, which
-- is their number) every entry is rendered with — one query for a whole page
-- rather than one per row. An id with no receipts is simply absent from the
-- result, which the caller reads as an empty list.
SELECT * FROM expenses.attachments
WHERE entry_id = ANY(@entry_ids::bigint[])
ORDER BY entry_id, id;

-- name: GetAttachment :one
-- GetAttachment fetches one receipt by id. Who may read it is decided in Go
-- from the expense it is on (authorize.go), never here: an outsider's 404 has
-- to be indistinguishable from an unknown id's, so the row is loaded first and
-- discarded after.
SELECT * FROM expenses.attachments WHERE id = @id;

-- name: CountAttachmentsForEntry :one
-- CountAttachmentsForEntry is how many receipts one expense carries. It is
-- read under the expense's own row lock, so two uploads that both see nine
-- cannot both become the tenth.
SELECT count(*)::bigint FROM expenses.attachments WHERE entry_id = @entry_id;

-- name: ListAttachmentKeysForEntry :many
-- ListAttachmentKeysForEntry is the object keys of one expense's receipts,
-- read before the expense is deleted: the rows go with it (the foreign key
-- cascades), and the objects they name are removed once that delete has
-- committed.
SELECT object_key FROM expenses.attachments WHERE entry_id = @entry_id ORDER BY id;

-- name: InsertAttachment :one
-- InsertAttachment records one receipt. The object is already written under
-- object_key: the row is what makes it a receipt, so it is inserted last, in
-- the transaction that holds the expense's row lock.
INSERT INTO expenses.attachments (
    entry_id, object_key, file_name, content_type, size_bytes, uploaded_by_user_id, created_at
) VALUES (
    @entry_id, @object_key, @file_name, @content_type, @size_bytes, @uploaded_by_user_id, @now::timestamptz
)
RETURNING *;

-- name: DeleteAttachment :execrows
-- DeleteAttachment removes one receipt's row. The object it named is removed
-- after the transaction commits; a failure there is logged and never fails the
-- request, because the row — not the object — is what the expense carries.
DELETE FROM expenses.attachments WHERE id = @id;
