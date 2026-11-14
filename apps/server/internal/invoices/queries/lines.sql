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
-- the VAT snapshot is the issue's to write. A deduction line names the
-- invoice it deducts (invoices work design D7); a correction line bills an
-- energy correction settlement and may be negative (energy consumption
-- billing design D7).
INSERT INTO invoices.lines (
    invoice_id, position, description, quantity, unit, unit_price, discount_percent, vat_code_id,
    credits_line_id, line_gross, line_allowance, line_net, deducts_invoice_id, correction
) VALUES (
    @invoice_id, @position, @description, @quantity, @unit, @unit_price, @discount_percent, @vat_code_id,
    @credits_line_id, @line_gross, @line_allowance, @line_net, @deducts_invoice_id, @correction
)
RETURNING id;

-- name: VatSummaries :many
-- VatSummaries is an issued document's VAT per (category, rate), the highest
-- rate first.
SELECT * FROM invoices.vat_summaries WHERE invoice_id = @invoice_id ORDER BY rate_percent DESC, vat_category;

-- name: SnapshotLine :exec
-- SnapshotLine writes the VAT a line was issued with (D6 step 6), and what it
-- was made from (accounting design R6): its kind, the product's tax category
-- and its project. It runs while the document is still a draft; the trigger
-- refuses it afterwards (D9).
--
-- What a line was made from is read from its basis: the line itself, or on a
-- credit note the original line it credits. An original issued with the
-- snapshot gives its own; else the basis's sources are read — every source
-- whose lines include the basis's position (an energy settlement's row sits
-- on its first line and covers line_count of them, and is told by its metering
-- point, as 00078 tells it, without naming another schema). One kind of source names
-- the kind; an order allocation's is its subkind; sources of several kinds are
-- mixed; a deduction line deducts an a-konto; a line with no source is
-- manual. The project and the tax category are the sources' when they all
-- share one, else NULL.
UPDATE invoices.lines AS l SET
    vat_rate_percent = @vat_rate_percent,
    vat_category = @vat_category,
    saf_t_code = @saf_t_code,
    exemption_reason = @exemption_reason,
    line_kind = d.line_kind,
    tax_category_id = d.tax_category_id,
    project_id = d.project_id
FROM (
    SELECT
        CASE
            WHEN b.line_kind IS NOT NULL THEN b.line_kind
            WHEN b.deducts_invoice_id IS NOT NULL THEN 'deduction'
            WHEN s.kinds IS NULL THEN 'manual'
            WHEN cardinality(s.kinds) > 1 THEN 'mixed'
            WHEN s.kinds[1] = 'time.entry' THEN 'hours'
            WHEN s.kinds[1] = 'expenses.entry' THEN 'expense'
            WHEN s.kinds[1] = 'projects.milestone' THEN 'milestone'
            WHEN s.kinds[1] = 'settlement' THEN 'settlement'
            WHEN s.kinds[1] IN ('order_line', 'subscription_period') THEN s.kinds[1]
            ELSE 'mixed'
        END::text AS line_kind,
        CASE WHEN b.line_kind IS NOT NULL THEN b.tax_category_id ELSE s.tax_category_id END AS tax_category_id,
        CASE WHEN b.line_kind IS NOT NULL THEN b.project_id ELSE s.project_id END AS project_id
    FROM invoices.lines t
    JOIN invoices.lines b ON b.id = COALESCE(t.credits_line_id, t.id)
    CROSS JOIN LATERAL (
        SELECT
            array_agg(DISTINCT CASE
                WHEN ls.source_kind = 'orders.allocation' THEN COALESCE(ls.source_subkind, 'order_line')
                WHEN ls.point_gsrn IS NOT NULL THEN 'settlement'
                ELSE ls.source_kind END) AS kinds,
            CASE WHEN count(*) = count(ls.project_id) AND count(DISTINCT ls.project_id) = 1
                THEN min(ls.project_id) END AS project_id,
            CASE WHEN count(*) = count(ls.tax_category_id) AND count(DISTINCT ls.tax_category_id) = 1
                THEN min(ls.tax_category_id) END AS tax_category_id
        FROM invoices.line_sources ls
        JOIN invoices.lines f ON f.id = ls.line_id
        WHERE ls.invoice_id = b.invoice_id
          AND b.position BETWEEN f.position AND f.position + ls.line_count - 1
    ) AS s
    WHERE t.id = @id
) AS d
WHERE l.id = @id;

-- name: InsertVatSummary :exec
INSERT INTO invoices.vat_summaries (
    invoice_id, vat_category, rate_percent, saf_t_code, exemption_reason, taxable_amount, vat_amount, vat_amount_nok
) VALUES (
    @invoice_id, @vat_category, @rate_percent, @saf_t_code, @exemption_reason, @taxable_amount, @vat_amount, @vat_amount_nok
);
