-- name: InsertCustomerAttachment :one
-- InsertCustomerAttachment records one attachment (customer attachments design
-- D1, D3), inside the transaction that holds the customer's lock, after the
-- object was written. timeline_entry_id is NULL for a document of the
-- relationship itself.
INSERT INTO customers.customer_attachments
    (customer_id, timeline_entry_id, object_key, file_name, content_type, size_bytes, uploaded_by_user_id, created_at)
VALUES (@customer_id, sqlc.narg('timeline_entry_id')::int, @object_key, @file_name, @content_type, @size_bytes,
        sqlc.narg('uploaded_by_user_id')::uuid, @now::timestamptz)
RETURNING *;

-- name: ListCustomerAttachments :many
-- ListCustomerAttachments is one customer's attachments, newest first, each
-- with the timeline entry it belongs to when it belongs to one (design D3):
-- the card shows the entry's type and day beside a file, and the timeline is
-- paged, so the entry may not be loaded. A deleted entry is still named — the
-- row stays, as its revisions do — and its state says so.
SELECT a.id, a.customer_id, a.timeline_entry_id, a.object_key, a.file_name, a.content_type, a.size_bytes,
       a.uploaded_by_user_id, a.created_at,
       e.event_type AS entry_event_type, e.occurred_on AS entry_occurred_on, e.summary AS entry_summary,
       e.state AS entry_state
FROM customers.customer_attachments a
LEFT JOIN customers.customers_timeline_entries e ON e.id = a.timeline_entry_id
WHERE a.customer_id = @customer_id
ORDER BY a.created_at DESC, a.id DESC;

-- name: GetCustomerAttachment :one
-- GetCustomerAttachment is one attachment of one customer, with its entry: the
-- download's and the delete's read. An id on another customer is the same 404
-- an unknown id is.
SELECT a.id, a.customer_id, a.timeline_entry_id, a.object_key, a.file_name, a.content_type, a.size_bytes,
       a.uploaded_by_user_id, a.created_at,
       e.event_type AS entry_event_type, e.occurred_on AS entry_occurred_on, e.summary AS entry_summary,
       e.state AS entry_state
FROM customers.customer_attachments a
LEFT JOIN customers.customers_timeline_entries e ON e.id = a.timeline_entry_id
WHERE a.id = @id AND a.customer_id = @customer_id;

-- name: LockCustomerAttachment :one
-- LockCustomerAttachment is the delete's read under the customer's lock: the
-- row as it is now, or pgx.ErrNoRows for one that went in the meantime.
SELECT * FROM customers.customer_attachments
WHERE id = @id AND customer_id = @customer_id
FOR UPDATE;

-- name: CountCustomerAttachments :one
-- CountCustomerAttachments is the cap's count (design D2): every attachment of
-- the customer, on the customer and on its entries alike, judged under the
-- customer's lock so two uploads racing for the last slot cannot both take it.
SELECT count(*) FROM customers.customer_attachments WHERE customer_id = @customer_id;

-- name: DeleteCustomerAttachment :exec
-- DeleteCustomerAttachment removes the row; its object is queued on the
-- ledger in the same transaction (InsertObjectDeletion).
DELETE FROM customers.customer_attachments WHERE id = @id;

-- name: AttachableTimelineEntry :one
-- AttachableTimelineEntry is the entry an upload names, as it is under the
-- customer's lock: it must be this customer's, manual and active (design D2).
-- A row that is none of those answers pgx.ErrNoRows, and the handler's field
-- error says which rule without this query telling it apart — the message is
-- the same for every way the entry is not one to attach to.
SELECT id FROM customers.customers_timeline_entries
WHERE id = @id AND customer_id = @customer_id AND provenance = 'manual' AND state = 'active';

-- name: MoveCustomerAttachments :execrows
-- MoveCustomerAttachments moves every attachment of the absorbed customer to
-- the survivor (design D5): one column, nothing in the store, since a key
-- names no customer. An attachment on an entry follows its entry, which moved
-- a statement earlier. Its row count is the merge's customers.attachments.
UPDATE customers.customer_attachments
SET customer_id = @into_customer_id::int
WHERE customer_id = @from_customer_id::int;

-- name: ListCustomerAttachmentsForExport :many
-- ListCustomerAttachmentsForExport is every attachment of one customer for
-- its personal-data export (design D6), oldest first: the metadata, never the
-- bytes — the file is JSON built in memory, and each attachment is downloaded
-- through its own endpoint for as long as the customer is not anonymised.
SELECT id, customer_id, timeline_entry_id, object_key, file_name, content_type, size_bytes, uploaded_by_user_id, created_at
FROM customers.customer_attachments
WHERE customer_id = @customer_id
ORDER BY created_at, id;

-- name: DeleteAllCustomerAttachments :many
-- DeleteAllCustomerAttachments is the anonymisation's delete (design D6):
-- every attachment of the customer goes, and the keys come back so the run
-- can queue each on the ledger in the same transaction. Its row count is
-- customer.anonymised's customers.attachments.
DELETE FROM customers.customer_attachments WHERE customer_id = @customer_id
RETURNING object_key;
