-- name: InsertOpenItem :one
INSERT INTO accounting.open_items (kind, customer_id, supplier_id, document_number, document_date, due_date,
    original_amount, source_document_id, created_at)
VALUES (sqlc.arg(kind), sqlc.narg(customer_id), sqlc.narg(supplier_id), sqlc.narg(document_number),
    sqlc.narg(document_date), sqlc.narg(due_date), sqlc.arg(original_amount)::text::numeric,
    sqlc.narg(source_document_id), sqlc.arg(now))
RETURNING id;

-- name: EnsureUnallocatedItem :exec
-- EnsureUnallocatedItem makes the party's unallocated item (R5) when it has
-- none; two posts at once both end up naming the one row.
INSERT INTO accounting.open_items (kind, customer_id, supplier_id, unallocated, created_at)
VALUES (sqlc.arg(kind), sqlc.narg(customer_id), sqlc.narg(supplier_id), true, sqlc.arg(now))
ON CONFLICT DO NOTHING;

-- name: UnallocatedItem :one
SELECT id FROM accounting.open_items
WHERE unallocated AND kind = sqlc.arg(kind)
  AND coalesce(customer_id::bigint, supplier_id) = sqlc.arg(party_id)::bigint;

-- name: OpenItemBySourceDocument :one
SELECT id, customer_id, supplier_id FROM accounting.open_items
WHERE kind = sqlc.arg(kind) AND source_document_id = sqlc.arg(source_document_id);

-- name: OpenItemsForPosting :many
SELECT id, kind, customer_id, supplier_id, source_document_id FROM accounting.open_items
WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: LockOpenItems :exec
-- LockOpenItems takes every item a post names FOR UPDATE, in id order, so two
-- posts naming the same items wait for each other rather than deadlock (M5).
SELECT id FROM accounting.open_items WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY id FOR UPDATE;

-- name: SetOpeningAsOfSeqs :exec
-- SetOpeningAsOfSeqs records, on each customer item the opening made for an
-- invoice in Invoices, the AsOfSeq of the OpenAt page it was read on (R14).
UPDATE accounting.open_items o SET opening_as_of_seq = s.seq
FROM (SELECT unnest(sqlc.arg(document_ids)::bigint[]) AS document_id, unnest(sqlc.arg(seqs)::bigint[]) AS seq) s
WHERE o.kind = 'customer' AND o.source_document_id = s.document_id;

-- name: GetOpenItem :one
SELECT o.id, o.kind, o.customer_id, o.supplier_id, o.unallocated, o.document_number, o.document_date, o.due_date,
    o.original_amount::text AS original_amount, o.source_document_id,
    coalesce((SELECT sum(p.amount) FROM accounting.postings p WHERE p.open_item_id = o.id), 0)::text AS balance
FROM accounting.open_items o WHERE o.id = sqlc.arg(id);

-- name: LockOpenItem :exec
SELECT 1 FROM accounting.open_items WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ListOpenItems :many
-- ListOpenItems is a page of the items matching the filters, with the count
-- of every match.
SELECT o.id, o.kind, o.customer_id, o.supplier_id, o.unallocated, o.document_number, o.document_date, o.due_date,
    o.original_amount::text AS original_amount, o.source_document_id, b.balance::text AS balance,
    count(*) OVER () AS total
FROM accounting.open_items o
CROSS JOIN LATERAL (SELECT coalesce(sum(p.amount), 0) AS balance FROM accounting.postings p
    WHERE p.open_item_id = o.id) b
WHERE (sqlc.narg(kind)::text IS NULL OR o.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(customer_id)::int IS NULL OR o.customer_id = sqlc.narg(customer_id)::int)
  AND (sqlc.narg(supplier_id)::bigint IS NULL OR o.supplier_id = sqlc.narg(supplier_id)::bigint)
  AND (NOT sqlc.arg(only_open)::boolean OR b.balance <> 0)
ORDER BY o.kind, coalesce(o.customer_id::bigint, o.supplier_id), o.due_date NULLS FIRST, o.id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountOpenCustomerItems :one
SELECT count(*)::int FROM accounting.open_items o
WHERE o.kind = 'customer'
  AND coalesce((SELECT sum(p.amount) FROM accounting.postings p WHERE p.open_item_id = o.id), 0) <> 0;
