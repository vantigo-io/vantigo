-- name: InsertTaxCode :one
INSERT INTO accounting.tax_codes (code, name, direction, reverse_charge, vat_account_id, counter_vat_account_id,
    reported, created_at, updated_at)
VALUES (sqlc.arg(code), sqlc.arg(name), sqlc.arg(direction), sqlc.arg(reverse_charge), sqlc.narg(vat_account_id),
    sqlc.narg(counter_vat_account_id), sqlc.arg(reported), sqlc.arg(now), sqlc.arg(now))
RETURNING id;

-- name: InsertTaxRate :exec
INSERT INTO accounting.tax_code_rates (tax_code_id, rate_percent, valid_from, valid_to, created_at)
VALUES (sqlc.arg(tax_code_id), sqlc.arg(rate_percent)::text::numeric, sqlc.arg(valid_from), sqlc.narg(valid_to),
    sqlc.arg(now));

-- name: ListTaxCodes :many
SELECT * FROM accounting.tax_codes ORDER BY length(code), code;

-- name: GetTaxCode :one
SELECT * FROM accounting.tax_codes WHERE id = sqlc.arg(id);

-- name: LockTaxCode :one
SELECT id, revision FROM accounting.tax_codes WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateTaxCode :exec
UPDATE accounting.tax_codes
SET name = sqlc.arg(name), vat_account_id = sqlc.narg(vat_account_id),
    counter_vat_account_id = sqlc.narg(counter_vat_account_id), active = sqlc.arg(active),
    updated_at = sqlc.arg(now), revision = revision + 1
WHERE id = sqlc.arg(id);

-- name: ListTaxRates :many
SELECT tax_code_id, rate_percent::text AS rate_percent, valid_from, valid_to
FROM accounting.tax_code_rates
WHERE (sqlc.narg(tax_code_id)::int IS NULL OR tax_code_id = sqlc.narg(tax_code_id)::int)
ORDER BY tax_code_id, valid_from;

-- name: OpenTaxRate :one
SELECT id, valid_from FROM accounting.tax_code_rates
WHERE tax_code_id = sqlc.arg(tax_code_id) AND valid_to IS NULL;

-- name: CloseTaxRate :exec
UPDATE accounting.tax_code_rates SET valid_to = sqlc.arg(valid_to) WHERE id = sqlc.arg(id);

-- name: TaxCodesForPosting :many
-- TaxCodesForPosting is each tax code a post names, with its rate on day
-- (NULL when no period covers it).
SELECT t.id, t.code, t.direction, t.reverse_charge, t.vat_account_id, t.counter_vat_account_id, t.active,
    r.rate_percent
FROM accounting.tax_codes t
LEFT JOIN accounting.tax_code_rates r ON r.tax_code_id = t.id AND r.valid_from <= sqlc.arg(day)::date
    AND (r.valid_to IS NULL OR r.valid_to >= sqlc.arg(day)::date)
WHERE t.id = ANY(sqlc.arg(ids)::int[]);
