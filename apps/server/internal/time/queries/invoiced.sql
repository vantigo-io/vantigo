-- name: BillableHoursForProjects :many
-- BillableHoursForProjects is contracts.BillableHours read by project
-- (invoices work design D3): the approved, billable, priced entries not yet
-- invoiced, row by row, dated on or before until when one is given. The
-- caller asks for one row past contracts.MaxBillableRows, so the page knows
-- whether there was more. amount is hours × bill rate × the multiplier (100 %
-- for ordinary hours), exact — numeric multiplies without rounding, so it
-- carries up to eight decimals — the figure the holder judges a draft's
-- amount against. The multiplier is its decimal text, '' for ordinary hours.
-- The note is never read: it is the person's own text.
SELECT id,
       revision,
       project_id,
       billing_line_id,
       user_id,
       entry_date,
       (hours * 100)::bigint AS hours_hundredths,
       bill_rate::text AS bill_rate,
       COALESCE(bill_currency, '')::text AS currency,
       COALESCE(bill_multiplier_percent::text, '')::text AS bill_multiplier_percent,
       work_type_id,
       work_type_name,
       task_title,
       (hours * bill_rate * (COALESCE(bill_multiplier_percent, 100) * 0.01))::text AS amount
FROM time.entries
WHERE project_id = ANY(@project_ids::integer[])
  AND status = 'approved' AND billable AND bill_rate IS NOT NULL
  AND (sqlc.narg(until)::date IS NULL OR entry_date <= sqlc.narg(until)::date)
ORDER BY project_id, entry_date, id
LIMIT @row_limit;

-- name: BillableHoursByIDs :many
-- BillableHoursByIDs is BillableHoursForProjects over exactly the entries in
-- ids — a held source's freshness: an entry since invoiced, or no longer
-- approved, billable and priced, is simply absent.
SELECT id,
       revision,
       project_id,
       billing_line_id,
       user_id,
       entry_date,
       (hours * 100)::bigint AS hours_hundredths,
       bill_rate::text AS bill_rate,
       COALESCE(bill_currency, '')::text AS currency,
       COALESCE(bill_multiplier_percent::text, '')::text AS bill_multiplier_percent,
       work_type_id,
       work_type_name,
       task_title,
       (hours * bill_rate * (COALESCE(bill_multiplier_percent, 100) * 0.01))::text AS amount
FROM time.entries
WHERE id = ANY(@ids::bigint[])
  AND status = 'approved' AND billable AND bill_rate IS NOT NULL
  AND (sqlc.narg(until)::date IS NULL OR entry_date <= sqlc.narg(until)::date)
ORDER BY project_id, entry_date, id;

-- name: StampEntriesInvoiced :many
-- StampEntriesInvoiced is the invoices issue's stamp (invoices work design
-- D1): the entries become invoiced by the invoice, at the issue's own time.
-- The rows are already locked by the holder, which judged every one of them
-- approved and unchanged; the status guard is the last line, not the rule.
UPDATE time.entries SET
    status = 'invoiced',
    invoiced_at = @issued_at::timestamptz,
    invoiced_invoice_id = @invoice_id::bigint,
    invoiced_number = @invoice_number::bigint,
    revision = revision + 1,
    updated_at = @issued_at::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'approved'
RETURNING id;

-- name: ReleaseEntriesInvoiced :many
-- ReleaseEntriesInvoiced takes the invoice's stamp back in the issue of the
-- credit note that returns the entries' line (D1, D8): invoiced → approved,
-- the three invoiced columns cleared, the approval stamps kept. Only an entry
-- carrying this invoice's stamp moves.
UPDATE time.entries SET
    status = 'approved',
    invoiced_at = NULL,
    invoiced_invoice_id = NULL,
    invoiced_number = NULL,
    revision = revision + 1,
    updated_at = @released_at::timestamptz
WHERE id = ANY(@ids::bigint[]) AND invoiced_invoice_id = @invoice_id::bigint
RETURNING id;
