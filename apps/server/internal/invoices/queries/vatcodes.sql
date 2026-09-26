-- name: VatCodesInForce :many
-- VatCodesInForce is every active VAT code with the rate of the period that
-- covers day (D3): what GET /meta offers a new line. A code with no period
-- covering day is not in force and is left out.
SELECT c.id, c.code, c.name, c.saf_t_code, c.ehf_category, c.exemption_reason, r.rate_percent
FROM invoices.vat_codes c
JOIN invoices.vat_code_rates r ON r.vat_code_id = c.id
WHERE c.active
  AND r.valid_from <= @day::date
  AND (r.valid_to IS NULL OR r.valid_to >= @day::date)
ORDER BY lower(c.code), c.id;

-- name: ListVatCodes :many
-- ListVatCodes is every code, inactive ones included, in the order GET
-- /vat-codes answers them.
SELECT * FROM invoices.vat_codes ORDER BY lower(code), id;

-- name: ListVatCodeRates :many
-- ListVatCodeRates is every period of every code, each code's the earliest
-- first.
SELECT * FROM invoices.vat_code_rates ORDER BY vat_code_id, valid_from;

-- name: VatCodeRates :many
-- VatCodeRates is one code's periods, the earliest first.
SELECT * FROM invoices.vat_code_rates WHERE vat_code_id = @vat_code_id ORDER BY valid_from;

-- name: VatCodesInUse :many
-- VatCodesInUse is every code a line carries, draft or issued (D3's "in use").
SELECT DISTINCT vat_code_id FROM invoices.lines ORDER BY vat_code_id;

-- name: VatCodeInUse :one
-- VatCodeInUse is whether any line, draft or issued, carries the code.
SELECT EXISTS (SELECT 1 FROM invoices.lines WHERE vat_code_id = @vat_code_id)::boolean AS in_use;

-- name: GetVatCode :one
SELECT * FROM invoices.vat_codes WHERE id = @id;

-- name: LockVatCode :one
-- LockVatCode takes the code FOR UPDATE, which also waits for any draft save
-- whose new line references it (the foreign key's KEY SHARE lock), so the
-- in-use check after it sees every committed line.
SELECT * FROM invoices.vat_codes WHERE id = @id FOR UPDATE;

-- name: InsertVatCode :one
INSERT INTO invoices.vat_codes (code, name, saf_t_code, ehf_category, exemption_reason, active, created_at, updated_at)
VALUES (@code, @name, @saf_t_code, @ehf_category, @exemption_reason, true, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateVatCode :one
UPDATE invoices.vat_codes SET
    code = @code,
    name = @name,
    saf_t_code = @saf_t_code,
    ehf_category = @ehf_category,
    exemption_reason = @exemption_reason,
    active = @active,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = @id
RETURNING *;

-- name: InsertVatCodeRate :one
INSERT INTO invoices.vat_code_rates (vat_code_id, rate_percent, valid_from, valid_to, created_at)
VALUES (@vat_code_id, @rate_percent, @valid_from, NULL, @now::timestamptz)
RETURNING *;

-- name: CloseOpenVatCodeRate :exec
-- CloseOpenVatCodeRate ends the code's open period on valid_to (D3's
-- rate-change rule: the day before the new period starts).
UPDATE invoices.vat_code_rates SET valid_to = @valid_to
WHERE vat_code_id = @vat_code_id AND valid_to IS NULL;

-- name: DeleteVatCodeRate :exec
DELETE FROM invoices.vat_code_rates WHERE id = @id;

-- name: ReopenVatCodeRate :exec
-- ReopenVatCodeRate makes a period open-ended again, after the one that
-- followed it was removed.
UPDATE invoices.vat_code_rates SET valid_to = NULL WHERE id = @id;
