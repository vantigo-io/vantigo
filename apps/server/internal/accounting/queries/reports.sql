-- The reports (A11), computed from the postings by voucher date whenever they
-- are asked for; nothing here is stored.

-- name: AccountTotals :many
-- AccountTotals is every account with a posting up to to_date: its opening
-- balance at from_date, its movement in the period with its debits and
-- credits, and its closing balance at to_date. A balance-sheet account's
-- balances count everything before; an income or expense account's count from
-- year_start, the first day of the fiscal year, since the year is not closed
-- in this phase.
SELECT a.id, a.number, a.name, a.kind,
    coalesce(sum(p.amount) FILTER (WHERE v.voucher_date < sqlc.arg(from_date)::date
        AND (a.kind IN ('asset', 'liability', 'equity') OR v.voucher_date >= sqlc.arg(year_start)::date)), 0)::text AS opening,
    coalesce(sum(p.amount) FILTER (WHERE v.voucher_date >= sqlc.arg(from_date)::date), 0)::text AS movement,
    coalesce(sum(p.amount) FILTER (WHERE v.voucher_date >= sqlc.arg(from_date)::date AND p.amount > 0), 0)::text AS debit,
    coalesce(-sum(p.amount) FILTER (WHERE v.voucher_date >= sqlc.arg(from_date)::date AND p.amount < 0), 0)::text AS credit,
    coalesce(sum(p.amount) FILTER (WHERE a.kind IN ('asset', 'liability', 'equity')
        OR v.voucher_date >= sqlc.arg(year_start)::date), 0)::text AS closing
FROM accounting.accounts a
JOIN accounting.postings p ON p.account_id = a.id
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE v.voucher_date <= sqlc.arg(to_date)::date
  AND (sqlc.narg(project_id)::int IS NULL OR p.project_id = sqlc.narg(project_id)::int)
GROUP BY a.id, a.number, a.name, a.kind
ORDER BY a.number;

-- name: UnclosedResult :one
-- UnclosedResult is the sum of every income and expense posting up to
-- to_date: the result not yet closed to equity (R13), negative for a profit.
SELECT coalesce(sum(p.amount), 0)::text AS result
FROM accounting.postings p
JOIN accounting.accounts a ON a.id = p.account_id
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE a.kind IN ('income', 'expense') AND v.voucher_date <= sqlc.arg(to_date)::date
  AND (sqlc.narg(project_id)::int IS NULL OR p.project_id = sqlc.narg(project_id)::int);

-- name: JournalPostings :many
-- JournalPostings is every posting of the vouchers dated in the period, in
-- voucher number order (the bokføringsspesifikasjon).
SELECT v.id AS voucher_id, v.fiscal_year, v.number, v.voucher_date, v.document_date, v.description AS voucher_description,
    v.kind, p.line_no, a.number AS account_number, a.name AS account_name, p.amount::text AS amount,
    t.code AS tax_code, p.customer_id, p.supplier_id, p.project_id, p.description
FROM accounting.vouchers v
JOIN accounting.postings p ON p.voucher_id = v.id
JOIN accounting.accounts a ON a.id = p.account_id
LEFT JOIN accounting.tax_codes t ON t.id = p.tax_code_id
WHERE v.voucher_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
  AND (sqlc.narg(project_id)::int IS NULL OR p.project_id = sqlc.narg(project_id)::int)
ORDER BY v.fiscal_year, v.number, p.line_no
LIMIT 50001;

-- name: AccountOpening :one
SELECT coalesce(sum(p.amount), 0)::text AS opening
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE p.account_id = sqlc.arg(account_id) AND v.voucher_date < sqlc.arg(from_date)::date
  AND (NOT sqlc.arg(from_year_start)::boolean OR v.voucher_date >= sqlc.arg(year_start)::date)
  AND (sqlc.narg(project_id)::int IS NULL OR p.project_id = sqlc.narg(project_id)::int);

-- name: AccountPostings :many
SELECT v.id AS voucher_id, v.fiscal_year, v.number, v.voucher_date, v.description AS voucher_description,
    p.amount::text AS amount, t.code AS tax_code, p.customer_id, p.supplier_id, p.project_id, p.description
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
LEFT JOIN accounting.tax_codes t ON t.id = p.tax_code_id
WHERE p.account_id = sqlc.arg(account_id)
  AND v.voucher_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
  AND (sqlc.narg(project_id)::int IS NULL OR p.project_id = sqlc.narg(project_id)::int)
ORDER BY v.voucher_date, v.fiscal_year, v.number, p.line_no
LIMIT 50001;

-- name: PartyItemsAt :many
-- PartyItemsAt is every open item of a kind with its balance at to_date and
-- its movement in the period, the items with a balance or a movement only.
SELECT o.id, o.customer_id, o.supplier_id, o.unallocated, o.document_number, o.document_date, o.due_date,
    coalesce(sum(p.amount), 0)::text AS balance,
    coalesce(sum(p.amount) FILTER (WHERE v.voucher_date >= sqlc.arg(from_date)::date), 0)::text AS movement
FROM accounting.open_items o
JOIN accounting.postings p ON p.open_item_id = o.id
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE o.kind = sqlc.arg(kind) AND v.voucher_date <= sqlc.arg(to_date)::date
  AND (sqlc.narg(customer_id)::int IS NULL OR o.customer_id = sqlc.narg(customer_id)::int)
  AND (sqlc.narg(supplier_id)::bigint IS NULL OR o.supplier_id = sqlc.narg(supplier_id)::bigint)
  AND (sqlc.narg(project_id)::int IS NULL OR p.project_id = sqlc.narg(project_id)::int)
GROUP BY o.id
HAVING coalesce(sum(p.amount), 0) <> 0
    OR coalesce(sum(p.amount) FILTER (WHERE v.voucher_date >= sqlc.arg(from_date)::date), 0) <> 0
ORDER BY coalesce(o.customer_id::bigint, o.supplier_id), o.due_date NULLS FIRST, o.id;
