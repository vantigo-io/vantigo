-- name: GetInvoice :one
SELECT * FROM invoices.invoices WHERE id = @id;

-- name: LockInvoice :one
-- LockInvoice takes a document FOR UPDATE: a draft save, a delete and an issue
-- hold it to their commit. It is always the first lock an issue takes (D6).
SELECT * FROM invoices.invoices WHERE id = @id FOR UPDATE;

-- name: InsertInvoiceDraft :one
-- InsertInvoiceDraft writes an invoice draft (D4). It has no number, no issue
-- date and no snapshots; its totals are the ones computed with today's rates,
-- which the issue computes again for the issue date.
INSERT INTO invoices.invoices (
    kind, customer_id, delivery_date, delivery_from, delivery_to,
    delivery_address_line1, delivery_address_line2, delivery_postal_code, delivery_city, delivery_country,
    payment_terms_days, currency, your_reference, our_reference, order_reference, note, internal_note,
    net_total, vat_total, gross_total, vat_total_nok, created_by_user_id, created_at, updated_at
) VALUES (
    'invoice', @customer_id, @delivery_date, @delivery_from, @delivery_to,
    @delivery_address_line1, @delivery_address_line2, @delivery_postal_code, @delivery_city, @delivery_country,
    @payment_terms_days, @currency, @your_reference, @our_reference, @order_reference, @note, @internal_note,
    @net_total, @vat_total, @gross_total, @vat_total_nok, @created_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateDraft :one
-- UpdateDraft replaces a draft's own fields and moves its revision on. The
-- caller holds the row (LockInvoice) and has checked it is still a draft.
UPDATE invoices.invoices SET
    customer_id = @customer_id,
    delivery_date = @delivery_date,
    delivery_from = @delivery_from,
    delivery_to = @delivery_to,
    delivery_address_line1 = @delivery_address_line1,
    delivery_address_line2 = @delivery_address_line2,
    delivery_postal_code = @delivery_postal_code,
    delivery_city = @delivery_city,
    delivery_country = @delivery_country,
    payment_terms_days = @payment_terms_days,
    your_reference = @your_reference,
    our_reference = @our_reference,
    order_reference = @order_reference,
    note = @note,
    internal_note = @internal_note,
    net_total = @net_total,
    vat_total = @vat_total,
    gross_total = @gross_total,
    vat_total_nok = @vat_total_nok,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = @id AND status = 'draft'
RETURNING *;

-- name: DeleteDraft :execrows
-- DeleteDraft removes a draft and, by the cascade, its lines. An issued
-- document is never matched; the trigger would refuse it anyway (D9).
DELETE FROM invoices.invoices WHERE id = @id AND status = 'draft';

-- name: ListInvoices :many
-- ListInvoices is one page of GET /invoices (D4): drafts first, then by number
-- descending, the id breaking ties so a page never shifts under a reader.
-- search is a number (exact) or a buyer-name pattern; a draft has no buyer
-- snapshot and is found through customer_id instead.
SELECT * FROM invoices.invoices
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(customer_id)::int IS NULL OR customer_id = sqlc.narg(customer_id)::int)
  AND ((sqlc.narg(search_number)::bigint IS NULL AND sqlc.narg(search_pattern)::text IS NULL)
       OR number = sqlc.narg(search_number)::bigint
       OR buyer_name ILIKE sqlc.narg(search_pattern)::text)
  AND (sqlc.narg(issued_from)::date IS NULL OR issue_date >= sqlc.narg(issued_from)::date)
  AND (sqlc.narg(issued_to)::date IS NULL OR issue_date <= sqlc.narg(issued_to)::date)
ORDER BY number DESC NULLS FIRST, id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: CountInvoices :one
-- CountInvoices is ListInvoices' total, over the same filters.
SELECT count(*)::int FROM invoices.invoices
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(customer_id)::int IS NULL OR customer_id = sqlc.narg(customer_id)::int)
  AND ((sqlc.narg(search_number)::bigint IS NULL AND sqlc.narg(search_pattern)::text IS NULL)
       OR number = sqlc.narg(search_number)::bigint
       OR buyer_name ILIKE sqlc.narg(search_pattern)::text)
  AND (sqlc.narg(issued_from)::date IS NULL OR issue_date >= sqlc.narg(issued_from)::date)
  AND (sqlc.narg(issued_to)::date IS NULL OR issue_date <= sqlc.narg(issued_to)::date);
