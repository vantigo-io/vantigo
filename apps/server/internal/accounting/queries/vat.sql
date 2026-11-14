-- VAT (A21–A23, R10): the specification computed from the postings by
-- voucher date, never stored; the terms a close writes; and their history.
--
-- The VAT accounts are every account a tax code names as its VAT account or
-- its counter account. A posting's part of the specification is decided in
-- Go (vatPart in vat.go) from the groups VatPeriodGroups answers, and in SQL
-- by VatPostings' CASE for the drill-down; the two say the same.

-- name: VatPeriodGroups :many
-- VatPeriodGroups is every posting dated in the period that carries a tax
-- code or lands on a VAT account, summed per account, tax code, rate on the
-- voucher date, tax role and specification. The settlement vouchers (and
-- their reversals) are left out: they move a term's VAT to vat_settlement and
-- are no part of what the term reports.
WITH vat_accounts AS (
    SELECT vat_account_id AS id FROM accounting.tax_codes WHERE vat_account_id IS NOT NULL
    UNION
    SELECT counter_vat_account_id FROM accounting.tax_codes WHERE counter_vat_account_id IS NOT NULL
)
SELECT p.account_id, a.number AS account_number, a.name AS account_name,
    coalesce(p.tax_code_id, 0)::int AS tax_code_id, coalesce(t.code, '')::text AS tax_code,
    coalesce(t.name, '')::text AS tax_name, coalesce(t.direction, '')::text AS tax_direction,
    coalesce(t.reverse_charge, false)::boolean AS reverse_charge, coalesce(t.reported, false)::boolean AS reported,
    coalesce(r.rate_percent::text, '')::text AS rate_percent, coalesce(p.tax_role, '')::text AS tax_role,
    coalesce(p.tax_specification, '')::text AS tax_specification,
    (p.account_id IN (SELECT id FROM vat_accounts))::boolean AS on_vat_account,
    sum(p.amount)::text AS amount, count(*)::int AS postings
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
JOIN accounting.accounts a ON a.id = p.account_id
LEFT JOIN accounting.tax_codes t ON t.id = p.tax_code_id
LEFT JOIN accounting.tax_code_rates r ON r.tax_code_id = p.tax_code_id AND r.valid_from <= v.voucher_date
    AND (r.valid_to IS NULL OR r.valid_to >= v.voucher_date)
WHERE v.voucher_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
  AND v.kind <> 'vat_settlement'
  AND (p.tax_code_id IS NOT NULL OR p.account_id IN (SELECT id FROM vat_accounts))
GROUP BY p.account_id, a.number, a.name, p.tax_code_id, t.code, t.name, t.direction, t.reverse_charge, t.reported,
    r.rate_percent, p.tax_role, p.tax_specification
ORDER BY a.number, t.code, r.rate_percent, p.tax_role, p.tax_specification;

-- name: VatPostings :many
-- VatPostings is the drill-down behind a figure: the postings dated in the
-- period of one tax code (or every one), of one part, or — unreconciled —
-- those that make the VAT accounts' movement differ from the return's VAT: a
-- posting on a VAT account the return does not count, or one it counts that
-- is not on a VAT account. In voucher order, a page at a time, with the
-- total that match.
WITH vat_accounts AS (
    SELECT vat_account_id AS id FROM accounting.tax_codes WHERE vat_account_id IS NOT NULL
    UNION
    SELECT counter_vat_account_id FROM accounting.tax_codes WHERE counter_vat_account_id IS NOT NULL
), classified AS (
    SELECT p.id, p.voucher_id, p.line_no, p.account_id, p.amount, p.tax_code_id, p.tax_role, p.tax_base,
        p.tax_specification, p.description, v.fiscal_year, v.number, v.voucher_date, v.description AS voucher_description,
        a.number AS account_number, a.name AS account_name, t.code AS tax_code,
        coalesce(t.reported, false) AS reported,
        (p.account_id IN (SELECT id FROM vat_accounts)) AS on_vat_account,
        CASE
            WHEN p.tax_code_id IS NULL THEN 'untaxed'
            WHEN p.tax_specification = 'tapPåKrav' AND p.tax_role = 'base' THEN 'loss_base'
            WHEN p.tax_specification = 'tapPåKrav' THEN 'loss'
            WHEN p.tax_role = 'base' THEN 'base'
            WHEN p.tax_role = 'tax_reverse' THEN 'calculated'
            WHEN t.direction = 'output' THEN 'output'
            WHEN t.direction = 'input' AND p.account_id IN (SELECT id FROM vat_accounts) THEN 'deducted'
            WHEN t.direction = 'input' THEN 'not_deducted'
            ELSE 'other'
        END AS part
    FROM accounting.postings p
    JOIN accounting.vouchers v ON v.id = p.voucher_id
    JOIN accounting.accounts a ON a.id = p.account_id
    LEFT JOIN accounting.tax_codes t ON t.id = p.tax_code_id
    WHERE v.voucher_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
      AND v.kind <> 'vat_settlement'
      AND (p.tax_code_id IS NOT NULL OR p.account_id IN (SELECT id FROM vat_accounts))
)
SELECT c.id, c.voucher_id, c.line_no, c.account_id, c.account_number, c.account_name, c.amount::text AS amount,
    c.tax_code_id, c.tax_code, c.tax_role, coalesce(c.tax_base::text, '')::text AS tax_base, c.tax_specification, c.description,
    c.fiscal_year, c.number, c.voucher_date, c.voucher_description, c.part::text AS part,
    count(*) OVER ()::int AS total
FROM classified c
WHERE (sqlc.narg(tax_code_id)::int IS NULL OR c.tax_code_id = sqlc.narg(tax_code_id)::int)
  AND (sqlc.narg(part)::text IS NULL OR c.part = sqlc.narg(part)::text)
  AND (NOT sqlc.arg(unreconciled)::boolean OR (
        (c.reported AND c.part IN ('output', 'calculated', 'deducted', 'loss')) <> c.on_vat_account))
ORDER BY c.voucher_date, c.fiscal_year, c.number, c.line_no
OFFSET sqlc.arg(row_offset) LIMIT sqlc.arg(row_limit);

-- name: ListVatTerms :many
SELECT * FROM accounting.vat_terms ORDER BY year, term;

-- name: GetVatTerm :one
SELECT * FROM accounting.vat_terms WHERE year = sqlc.arg(year) AND term = sqlc.arg(term);

-- name: GetVatTermForUpdate :one
-- GetVatTermForUpdate is taken after the settings row (FOR UPDATE), the order
-- every write to a term takes them in.
SELECT * FROM accounting.vat_terms WHERE year = sqlc.arg(year) AND term = sqlc.arg(term) FOR UPDATE;

-- name: CloseVatTerm :one
-- CloseVatTerm writes a term's close: made on its first close, its
-- state, figures, XML and settlement voucher replaced on a later one.
INSERT INTO accounting.vat_terms (year, term, term_kind, period_start, period_end, state, closings, figures,
    return_xml, settlement_voucher_id, closed_at, closed_by_user_id, updated_at)
VALUES (sqlc.arg(year), sqlc.arg(term), sqlc.arg(term_kind), sqlc.arg(period_start), sqlc.arg(period_end), 'closed',
    sqlc.arg(closings), sqlc.arg(figures), sqlc.arg(return_xml), sqlc.narg(settlement_voucher_id), sqlc.arg(now),
    sqlc.arg(user_id), sqlc.arg(now))
ON CONFLICT (year, term) DO UPDATE
SET state = 'closed', closings = EXCLUDED.closings, figures = EXCLUDED.figures, return_xml = EXCLUDED.return_xml,
    settlement_voucher_id = EXCLUDED.settlement_voucher_id, closed_at = EXCLUDED.closed_at,
    closed_by_user_id = EXCLUDED.closed_by_user_id, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: ReopenVatTerm :exec
-- ReopenVatTerm opens a closed term again: what the close made is history
-- now, in vat_term_events and the reversed settlement voucher.
UPDATE accounting.vat_terms
SET state = 'open', figures = NULL, return_xml = NULL, settlement_voucher_id = NULL, closed_at = NULL,
    closed_by_user_id = NULL, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: SetVatTermFiled :exec
UPDATE accounting.vat_terms
SET filed_at = sqlc.narg(filed_at), filed_reference = sqlc.narg(filed_reference),
    filed_by_user_id = sqlc.narg(filed_by_user_id), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: InsertVatTermEvent :exec
INSERT INTO accounting.vat_term_events (vat_term_id, kind, voucher_id, reason, filed_at, filed_reference, at, by_user_id)
VALUES (sqlc.arg(vat_term_id), sqlc.arg(kind), sqlc.narg(voucher_id), sqlc.narg(reason), sqlc.narg(filed_at),
    sqlc.narg(filed_reference), sqlc.arg(now), sqlc.arg(user_id));

-- name: ListVatTermEvents :many
SELECT e.*, v.fiscal_year AS voucher_fiscal_year, v.number AS voucher_number
FROM accounting.vat_term_events e
LEFT JOIN accounting.vouchers v ON v.id = e.voucher_id
WHERE e.vat_term_id = sqlc.arg(vat_term_id)
ORDER BY e.id DESC;

-- name: LastFiledTermEnd :one
-- LastFiledTermEnd is the end of the latest term filed at Skatteetaten: the
-- lock never moves back behind it (A8 as R10 amends it). No row when none is.
SELECT period_end FROM accounting.vat_terms WHERE filed_at IS NOT NULL ORDER BY period_end DESC LIMIT 1;

-- name: CountWaitingIn :one
-- CountWaitingIn is the facts under Needs attention that wait — not posted,
-- not resolved, not informational — dated in the period: what a term's
-- close waits for (A22).
SELECT count(*)::int FROM accounting.unposted
WHERE posted_at IS NULL AND resolved_at IS NULL
  AND fact_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date;
