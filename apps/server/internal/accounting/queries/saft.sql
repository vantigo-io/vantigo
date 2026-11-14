-- SAF-T Financial (A24, A25): the exports' queue and log, the reads the
-- export's writer pages through, and the imports' previews. Every read the
-- writer makes is bounded by through_voucher_id, the highest voucher committed
-- when the run began, so the totals written first and the transactions
-- streamed after them describe the same vouchers.

-- name: InsertSaftExport :one
INSERT INTO accounting.saft_exports (fiscal_year, from_period, to_period, period_start, period_end, unmapped_accounts,
    requested_at, requested_by_user_id)
VALUES (sqlc.arg(fiscal_year), sqlc.arg(from_period), sqlc.arg(to_period), sqlc.arg(period_start), sqlc.arg(period_end),
    sqlc.arg(unmapped_accounts), sqlc.arg(now), sqlc.arg(user_id))
RETURNING *;

-- name: ListSaftExports :many
SELECT * FROM accounting.saft_exports ORDER BY id DESC LIMIT sqlc.arg(row_limit);

-- name: GetSaftExport :one
SELECT * FROM accounting.saft_exports WHERE id = sqlc.arg(id);

-- name: SaftExportFiles :many
SELECT * FROM accounting.saft_export_files WHERE export_id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY export_id, part;

-- name: GetSaftExportFile :one
SELECT * FROM accounting.saft_export_files WHERE export_id = sqlc.arg(export_id) AND part = sqlc.arg(part);

-- name: ClaimSaftExport :one
-- ClaimSaftExport takes the oldest export that waits — queued, or running
-- under a lease that has run out (a worker that crashed) — under a new
-- lease, in one short transaction; SKIP LOCKED lets two workers take two.
UPDATE accounting.saft_exports e
SET state = 'running', lease_id = sqlc.arg(lease_id), lease_until = sqlc.arg(lease_until),
    attempts = e.attempts + 1, started_at = sqlc.arg(now)
WHERE e.id = (
    SELECT x.id FROM accounting.saft_exports x
    WHERE x.state = 'queued' OR (x.state = 'running' AND x.lease_until < sqlc.arg(now))
    ORDER BY x.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED)
RETURNING *;

-- name: ExtendSaftExportLease :execrows
UPDATE accounting.saft_exports SET lease_until = sqlc.arg(lease_until)
WHERE id = sqlc.arg(id) AND lease_id = sqlc.arg(lease_id) AND state = 'running';

-- name: InsertSaftExportFile :exec
INSERT INTO accounting.saft_export_files (export_id, part, parts, file_name, object_key, size_bytes, sha256,
    from_period, to_period, period_start, period_end)
SELECT sqlc.arg(export_id), sqlc.arg(part), sqlc.arg(parts), sqlc.arg(file_name), sqlc.arg(object_key),
    sqlc.arg(size_bytes), sqlc.arg(sha256), sqlc.arg(from_period), sqlc.arg(to_period), sqlc.arg(period_start),
    sqlc.arg(period_end)
FROM accounting.saft_exports
WHERE id = sqlc.arg(export_id) AND lease_id = sqlc.arg(lease_id) AND state = 'running';

-- name: FinishSaftExport :execrows
UPDATE accounting.saft_exports
SET state = 'done', error = NULL, total_bytes = sqlc.arg(total_bytes), finished_at = sqlc.arg(now),
    lease_id = NULL, lease_until = NULL
WHERE id = sqlc.arg(id) AND lease_id = sqlc.arg(lease_id) AND state = 'running';

-- name: FailSaftExport :execrows
UPDATE accounting.saft_exports
SET state = 'failed', error = sqlc.arg(error), finished_at = sqlc.arg(now), lease_id = NULL, lease_until = NULL
WHERE id = sqlc.arg(id) AND lease_id = sqlc.arg(lease_id) AND state = 'running';

-- name: MaxVoucherID :one
SELECT coalesce(max(id), 0)::bigint AS id FROM accounting.vouchers;

-- name: CountVouchers :one
SELECT count(*)::bigint AS n FROM accounting.vouchers;

-- name: SaftAccounts :many
-- SaftAccounts is the whole chart (every account, Skatteetaten's rule for a
-- file sent to it) with the grouping in force for the year: the row with the
-- greatest from_year not after it; empty when the account has none.
SELECT a.id, a.number, a.name, a.kind, a.system_role, a.created_at,
    coalesce(g.category, '')::text AS grouping_category, coalesce(g.code, '')::text AS grouping_code
FROM accounting.accounts a
LEFT JOIN LATERAL (
    SELECT ag.category, ag.code FROM accounting.account_groupings ag
    WHERE ag.account_id = a.id AND ag.from_year <= sqlc.arg(year)::int
    ORDER BY ag.from_year DESC LIMIT 1) g ON true
ORDER BY a.number;

-- name: SaftAccountBalances :many
-- SaftAccountBalances is each account's balance before from_date and at
-- to_date, as the trial balance counts them: a balance-sheet account's
-- everything before, an income or expense account's from year_start.
SELECT a.id,
    coalesce(sum(p.amount) FILTER (WHERE v.voucher_date < sqlc.arg(from_date)::date
        AND (a.kind IN ('asset', 'liability', 'equity') OR v.voucher_date >= sqlc.arg(year_start)::date)), 0)::text AS opening,
    coalesce(sum(p.amount) FILTER (WHERE a.kind IN ('asset', 'liability', 'equity')
        OR v.voucher_date >= sqlc.arg(year_start)::date), 0)::text AS closing
FROM accounting.accounts a
JOIN accounting.postings p ON p.account_id = a.id
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE v.voucher_date <= sqlc.arg(to_date)::date AND v.id <= sqlc.arg(through_voucher_id)::bigint
GROUP BY a.id;

-- name: SaftEarlierResult :one
-- SaftEarlierResult is every income and expense posting before year_start:
-- the earlier years' result the books have not closed, which the file
-- carries on the year_result account.
SELECT coalesce(sum(p.amount), 0)::text AS result
FROM accounting.postings p
JOIN accounting.accounts a ON a.id = p.account_id
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE a.kind IN ('income', 'expense') AND v.voucher_date < sqlc.arg(year_start)::date
  AND v.id <= sqlc.arg(through_voucher_id)::bigint;

-- name: SaftCustomerBalances :many
-- SaftCustomerBalances is each customer's balance on the receivables
-- account before from_date and at to_date, and its postings in the period:
-- the customers the file lists are those with a posting in the period or a
-- balance other than zero at either end.
SELECT p.customer_id::int AS customer_id, p.account_id,
    coalesce(sum(p.amount) FILTER (WHERE v.voucher_date < sqlc.arg(from_date)::date), 0)::text AS opening,
    coalesce(sum(p.amount), 0)::text AS closing,
    count(*) FILTER (WHERE v.voucher_date >= sqlc.arg(from_date)::date)::int AS postings
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
JOIN accounting.accounts a ON a.id = p.account_id AND a.system_role = 'receivables'
WHERE p.customer_id IS NOT NULL AND v.voucher_date <= sqlc.arg(to_date)::date
  AND v.id <= sqlc.arg(through_voucher_id)::bigint
GROUP BY p.customer_id, p.account_id
ORDER BY p.customer_id;

-- name: SaftSupplierBalances :many
-- SaftSupplierBalances is the same for the suppliers on payables, with the
-- supplier's master data; a supplier id from before the table (a journal's)
-- has none.
SELECT b.supplier_id::bigint AS supplier_id, b.account_id, b.opening, b.closing, b.postings,
    s.number AS supplier_number, coalesce(s.name, '')::text AS name, s.organisation_number,
    coalesce(s.country, '')::text AS country, s.address_line, s.postal_code, s.city, s.email, s.bank_account,
    s.iban, s.bic, coalesce(s.payment_terms_days, 0)::int AS payment_terms_days, coalesce(s.is_person, false)::boolean AS is_person,
    coalesce(s.active, true)::boolean AS active
FROM (
    SELECT p.supplier_id, p.account_id,
        coalesce(sum(p.amount) FILTER (WHERE v.voucher_date < sqlc.arg(from_date)::date), 0)::text AS opening,
        coalesce(sum(p.amount), 0)::text AS closing,
        count(*) FILTER (WHERE v.voucher_date >= sqlc.arg(from_date)::date)::int AS postings
    FROM accounting.postings p
    JOIN accounting.vouchers v ON v.id = p.voucher_id
    JOIN accounting.accounts a ON a.id = p.account_id AND a.system_role = 'payables'
    WHERE p.supplier_id IS NOT NULL AND v.voucher_date <= sqlc.arg(to_date)::date
      AND v.id <= sqlc.arg(through_voucher_id)::bigint
    GROUP BY p.supplier_id, p.account_id) b
LEFT JOIN accounting.suppliers s ON s.id = b.supplier_id
ORDER BY coalesce(s.number, b.supplier_id), b.supplier_id;

-- name: SaftTaxCodes :many
-- SaftTaxCodes is every tax code (Skatteetaten's rule: all of them), with the
-- rate period in force at at_date, else the latest before it.
SELECT t.id, t.code, t.name, t.direction, t.reverse_charge, t.vat_account_id, t.active,
    coalesce(r.rate_percent::text, '')::text AS rate_percent, r.valid_from, r.valid_to
FROM accounting.tax_codes t
LEFT JOIN LATERAL (
    SELECT tr.rate_percent, tr.valid_from, tr.valid_to FROM accounting.tax_code_rates tr
    WHERE tr.tax_code_id = t.id AND tr.valid_from <= sqlc.arg(at_date)::date
    ORDER BY tr.valid_from DESC LIMIT 1) r ON true
ORDER BY t.code;

-- name: SaftProjects :many
-- SaftProjects is every project a posting of the period names.
SELECT DISTINCT p.project_id::int AS project_id
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE p.project_id IS NOT NULL AND v.voucher_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
  AND v.id <= sqlc.arg(through_voucher_id)::bigint
ORDER BY 1;

-- name: SaftTotals :one
-- SaftTotals is GeneralLedgerEntries' header: the vouchers of the period and
-- the sums of their debits and credits.
SELECT count(DISTINCT v.id)::bigint AS vouchers,
    coalesce(sum(p.amount) FILTER (WHERE p.amount > 0), 0)::text AS debit,
    coalesce(-sum(p.amount) FILTER (WHERE p.amount < 0), 0)::text AS credit
FROM accounting.vouchers v
JOIN accounting.postings p ON p.voucher_id = v.id
WHERE v.voucher_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
  AND v.id <= sqlc.arg(through_voucher_id)::bigint;

-- name: SaftVoucherPage :many
-- SaftVoucherPage is a page of one journal's vouchers in the period, by
-- fiscal year and number after the cursor.
SELECT v.id, v.fiscal_year, v.number, v.voucher_date, v.document_date, v.description, v.kind,
    v.source_module, v.source_kind, v.source_id, v.posted_at, v.reverses_voucher_id
FROM accounting.vouchers v
WHERE v.voucher_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
  AND v.id <= sqlc.arg(through_voucher_id)::bigint
  AND v.kind = ANY(sqlc.arg(kinds)::text[])
  AND (v.fiscal_year, v.number) > (sqlc.arg(after_year)::int, sqlc.arg(after_number)::int)
ORDER BY v.fiscal_year, v.number
LIMIT sqlc.arg(row_limit);

-- name: SaftPostings :many
-- SaftPostings is the postings of a page's vouchers, with what a line
-- carries: the account's number and role, the tax code and its rate on the
-- voucher's date, the open item's document number and due date.
SELECT p.voucher_id, p.line_no, a.number AS account_number, coalesce(a.system_role, '')::text AS account_role,
    p.amount::text AS amount, coalesce(p.currency, '')::text AS currency,
    coalesce(p.currency_amount::text, '')::text AS currency_amount, coalesce(p.exchange_rate::text, '')::text AS exchange_rate,
    coalesce(t.code, '')::text AS tax_code, coalesce(p.tax_role, '')::text AS tax_role,
    coalesce(p.tax_base::text, '')::text AS tax_base, coalesce(r.rate_percent::text, '')::text AS rate_percent,
    p.customer_id, p.supplier_id, s.number AS supplier_number, p.project_id, p.description,
    coalesce(oi.document_number, '')::text AS document_number, oi.due_date
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
JOIN accounting.accounts a ON a.id = p.account_id
LEFT JOIN accounting.tax_codes t ON t.id = p.tax_code_id
LEFT JOIN accounting.tax_code_rates r ON r.tax_code_id = p.tax_code_id AND r.valid_from <= v.voucher_date
    AND (r.valid_to IS NULL OR r.valid_to >= v.voucher_date)
LEFT JOIN accounting.open_items oi ON oi.id = p.open_item_id
LEFT JOIN accounting.suppliers s ON s.id = p.supplier_id
WHERE p.voucher_id = ANY(sqlc.arg(voucher_ids)::bigint[])
ORDER BY p.voucher_id, p.line_no;

-- name: InsertSaftImport :one
INSERT INTO accounting.saft_imports (file_name, object_key, size_bytes, sha256, audit_file_version, selection_start,
    selection_end, preview, uploaded_at, uploaded_by_user_id)
VALUES (sqlc.arg(file_name), sqlc.arg(object_key), sqlc.arg(size_bytes), sqlc.arg(sha256), sqlc.arg(audit_file_version),
    sqlc.arg(selection_start), sqlc.arg(selection_end), sqlc.arg(preview), sqlc.arg(now), sqlc.arg(user_id))
RETURNING id;

-- name: GetSaftImport :one
SELECT * FROM accounting.saft_imports WHERE id = sqlc.arg(id);

-- name: LockSaftImport :one
SELECT * FROM accounting.saft_imports WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ListSaftImports :many
SELECT id, state, file_name, size_bytes, sha256, audit_file_version, selection_start, selection_end, uploaded_at,
    uploaded_by_user_id, applied_at, applied_by_user_id, opening_voucher_id
FROM accounting.saft_imports ORDER BY id DESC LIMIT 50;

-- name: MarkSaftImportApplied :exec
UPDATE accounting.saft_imports
SET state = 'applied', applied_at = sqlc.arg(now), applied_by_user_id = sqlc.arg(user_id),
    opening_voucher_id = sqlc.narg(opening_voucher_id)
WHERE id = sqlc.arg(id);
