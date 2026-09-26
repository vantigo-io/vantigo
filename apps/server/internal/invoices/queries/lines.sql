-- name: Lines :many
-- Lines is one document's lines, in their order.
SELECT * FROM invoices.lines WHERE invoice_id = @invoice_id ORDER BY position;

-- name: DeleteLines :exec
-- DeleteLines clears a draft's lines before a replace writes them again. The
-- trigger refuses it under an issued document (D9).
DELETE FROM invoices.lines WHERE invoice_id = @invoice_id;

-- name: InsertLine :exec
-- InsertLine writes one line of a draft with its computed amounts (D5); the
-- VAT snapshot is the issue's to write.
INSERT INTO invoices.lines (
    invoice_id, position, description, quantity, unit, unit_price, discount_percent, vat_code_id,
    credits_line_id, line_gross, line_allowance, line_net
) VALUES (
    @invoice_id, @position, @description, @quantity, @unit, @unit_price, @discount_percent, @vat_code_id,
    @credits_line_id, @line_gross, @line_allowance, @line_net
);

-- name: VatSummaries :many
-- VatSummaries is an issued document's VAT per (category, rate), the highest
-- rate first.
SELECT * FROM invoices.vat_summaries WHERE invoice_id = @invoice_id ORDER BY rate_percent DESC, vat_category;
