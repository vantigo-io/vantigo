-- name: AllocateVoucherNumber :one
-- AllocateVoucherNumber takes the next number of a fiscal year's series (A4):
-- the row is held to commit, so a rolled-back post gives its number back.
INSERT INTO accounting.counters (counter_name, next_value)
VALUES (sqlc.arg(counter_name), 2)
ON CONFLICT (counter_name) DO UPDATE SET next_value = accounting.counters.next_value + 1
RETURNING (next_value - 1)::int AS allocated;

-- name: InsertVoucher :one
INSERT INTO accounting.vouchers (fiscal_year, number, voucher_date, document_date, description, kind,
    source_module, source_kind, source_id, source_part, rule, rule_version, reverses_voucher_id, reversal_reason,
    posted_at, posted_by_user_id)
VALUES (sqlc.arg(fiscal_year), sqlc.arg(number), sqlc.arg(voucher_date), sqlc.arg(document_date),
    sqlc.arg(description), sqlc.arg(kind), sqlc.narg(source_module), sqlc.narg(source_kind), sqlc.narg(source_id),
    sqlc.arg(source_part), sqlc.narg(rule), sqlc.narg(rule_version), sqlc.narg(reverses_voucher_id),
    sqlc.narg(reversal_reason), sqlc.arg(posted_at), sqlc.narg(posted_by_user_id))
RETURNING id;

-- name: InsertPosting :exec
INSERT INTO accounting.postings (voucher_id, line_no, account_id, amount, tax_code_id, tax_role, tax_base,
    customer_id, supplier_id, open_item_id, project_id, description)
VALUES (sqlc.arg(voucher_id), sqlc.arg(line_no), sqlc.arg(account_id), sqlc.arg(amount)::text::numeric,
    sqlc.narg(tax_code_id), sqlc.narg(tax_role), sqlc.narg(tax_base)::text::numeric, sqlc.narg(customer_id),
    sqlc.narg(supplier_id), sqlc.narg(open_item_id), sqlc.narg(project_id), sqlc.arg(description));

-- name: VoucherBySource :one
SELECT id, fiscal_year, number, voucher_date FROM accounting.vouchers
WHERE source_module = sqlc.arg(source_module) AND source_kind = sqlc.arg(source_kind)
  AND source_id = sqlc.arg(source_id) AND source_part = sqlc.arg(source_part);

-- name: GetVoucher :one
SELECT v.*, r.id AS reversed_by_id,
    coalesce((SELECT sum(p.amount) FILTER (WHERE p.amount > 0) FROM accounting.postings p WHERE p.voucher_id = v.id), 0)::text AS debit
FROM accounting.vouchers v
LEFT JOIN accounting.vouchers r ON r.reverses_voucher_id = v.id
WHERE v.id = sqlc.arg(id);

-- name: VoucherNumbers :many
SELECT id, fiscal_year, number FROM accounting.vouchers WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: ListPostings :many
SELECT p.voucher_id, p.line_no, p.account_id, a.number AS account_number, a.name AS account_name,
    p.amount::text AS amount, p.tax_code_id, t.code AS tax_code, p.tax_role, p.tax_base,
    p.customer_id, p.supplier_id, p.open_item_id, p.project_id, p.description
FROM accounting.postings p
JOIN accounting.accounts a ON a.id = p.account_id
LEFT JOIN accounting.tax_codes t ON t.id = p.tax_code_id
WHERE p.voucher_id = ANY(sqlc.arg(voucher_ids)::bigint[])
ORDER BY p.voucher_id, p.line_no;

-- name: ListVouchers :many
-- ListVouchers is the vouchers matching the filters, newest first, with the
-- count of every match.
SELECT v.id, v.fiscal_year, v.number, v.voucher_date, v.document_date, v.description, v.kind, v.source_module,
    v.source_kind, v.source_id, v.source_part, v.reverses_voucher_id, v.posted_at, r.id AS reversed_by_id,
    coalesce((SELECT sum(p.amount) FILTER (WHERE p.amount > 0) FROM accounting.postings p WHERE p.voucher_id = v.id), 0)::text AS debit,
    count(*) OVER () AS total
FROM accounting.vouchers v
LEFT JOIN accounting.vouchers r ON r.reverses_voucher_id = v.id
WHERE (sqlc.narg(from_date)::date IS NULL OR v.voucher_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR v.voucher_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(kind)::text IS NULL OR v.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(account_id)::int IS NULL OR EXISTS (SELECT 1 FROM accounting.postings p
       WHERE p.voucher_id = v.id AND p.account_id = sqlc.narg(account_id)::int))
  AND (sqlc.narg(customer_id)::int IS NULL OR EXISTS (SELECT 1 FROM accounting.postings p
       WHERE p.voucher_id = v.id AND p.customer_id = sqlc.narg(customer_id)::int))
  AND (sqlc.narg(supplier_id)::bigint IS NULL OR EXISTS (SELECT 1 FROM accounting.postings p
       WHERE p.voucher_id = v.id AND p.supplier_id = sqlc.narg(supplier_id)::bigint))
  AND (sqlc.narg(q)::text IS NULL OR v.description ILIKE '%' || sqlc.narg(q)::text || '%'
       OR (v.fiscal_year::text || '-' || lpad(v.number::text, 6, '0')) LIKE sqlc.narg(q)::text || '%'
       OR EXISTS (SELECT 1 FROM accounting.postings p
           WHERE p.voucher_id = v.id AND p.description ILIKE '%' || sqlc.narg(q)::text || '%'))
ORDER BY v.voucher_date DESC, v.fiscal_year DESC, v.number DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountVouchersInYear :one
SELECT count(*)::int FROM accounting.vouchers WHERE fiscal_year = sqlc.arg(fiscal_year);

-- name: AnyVoucher :one
SELECT EXISTS (SELECT 1 FROM accounting.vouchers)::boolean;

-- name: StandingOpening :one
-- StandingOpening is the opening voucher that is not reversed and is not
-- itself a reversal, if any.
SELECT v.id FROM accounting.vouchers v
WHERE v.kind = 'opening' AND v.reverses_voucher_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM accounting.vouchers r WHERE r.reverses_voucher_id = v.id)
ORDER BY v.id DESC LIMIT 1;

-- name: InsertAttachment :one
INSERT INTO accounting.attachments (voucher_id, object_key, file_name, content_type, size_bytes, sha256,
    uploaded_at, uploaded_by_user_id)
VALUES (sqlc.arg(voucher_id), sqlc.arg(object_key), sqlc.arg(file_name), sqlc.arg(content_type),
    sqlc.arg(size_bytes), sqlc.arg(sha256), sqlc.arg(now), sqlc.arg(user_id))
RETURNING *;

-- name: ListAttachments :many
SELECT * FROM accounting.attachments WHERE voucher_id = sqlc.arg(voucher_id) ORDER BY id;

-- name: GetAttachment :one
SELECT * FROM accounting.attachments WHERE id = sqlc.arg(id) AND voucher_id = sqlc.arg(voucher_id);

-- name: VoucherExists :one
SELECT EXISTS (SELECT 1 FROM accounting.vouchers WHERE id = sqlc.arg(id))::boolean;
