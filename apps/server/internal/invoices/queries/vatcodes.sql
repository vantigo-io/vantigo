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
