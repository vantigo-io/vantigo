-- name: Lines :many
-- Lines is one document's lines, in their order.
SELECT * FROM invoices.lines WHERE invoice_id = @invoice_id ORDER BY position;

-- name: DeleteLines :exec
-- DeleteLines clears a draft's lines before a replace writes them again. The
-- trigger refuses it under an issued document (D9).
DELETE FROM invoices.lines WHERE invoice_id = @invoice_id;

-- name: InsertLine :one
-- InsertLine writes one line of a draft with its computed amounts (D5) and
-- answers its id, which the line's sources name (invoices work design D2);
-- the VAT snapshot is the issue's to write.
INSERT INTO invoices.lines (
    invoice_id, position, description, quantity, unit, unit_price, discount_percent, vat_code_id,
    credits_line_id, line_gross, line_allowance, line_net
) VALUES (
    @invoice_id, @position, @description, @quantity, @unit, @unit_price, @discount_percent, @vat_code_id,
    @credits_line_id, @line_gross, @line_allowance, @line_net
)
RETURNING id;

-- name: VatSummaries :many
-- VatSummaries is an issued document's VAT per (category, rate), the highest
-- rate first.
SELECT * FROM invoices.vat_summaries WHERE invoice_id = @invoice_id ORDER BY rate_percent DESC, vat_category;

-- name: SnapshotLine :exec
-- SnapshotLine writes the VAT a line was issued with (D6 step 6). It runs while
-- the document is still a draft; the trigger refuses it afterwards (D9).
UPDATE invoices.lines SET
    vat_rate_percent = @vat_rate_percent,
    vat_category = @vat_category,
    saf_t_code = @saf_t_code,
    exemption_reason = @exemption_reason
WHERE id = @id;

-- name: InsertVatSummary :exec
INSERT INTO invoices.vat_summaries (
    invoice_id, vat_category, rate_percent, saf_t_code, exemption_reason, taxable_amount, vat_amount, vat_amount_nok
) VALUES (
    @invoice_id, @vat_category, @rate_percent, @saf_t_code, @exemption_reason, @taxable_amount, @vat_amount, @vat_amount_nok
);
