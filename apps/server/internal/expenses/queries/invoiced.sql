-- The invoices work design's two halves on this module's side (D1, D3): the
-- billable read the invoices module builds lines from, and the stamp its issue
-- writes on the lines it invoiced — and takes back when a credit note returns
-- one. The invoice's id and number are opaque here; the invoices schema is
-- another module's (rule 4).
--
-- "Billable" is expenses.ready_to_invoice (00038) — the unit approved, the
-- line billable, priced and never a per diem day — and not yet invoiced, the
-- very rule GET /entries?toInvoice=true and the project page's ready figure
-- are. The unit's status is the claim's for a claim's line (unitOf in
-- authorize.go), hence the join.
--
-- supplier_invoice_rebilled (D15) is true on a supplier invoice when another
-- supplier invoice from the same supplier (trimmed, case-folded) with the same
-- number has already been invoiced: a warning for the invoices view, never a
-- refusal — this module allows the number twice (00033).

-- name: BillableExpensesForProjects :many
-- BillableExpensesForProjects is the billable lines of the projects named,
-- dated on or before until when one is given, project by project and oldest
-- first. row_limit is one past what a page answers, so the caller can say
-- there was more.
SELECT e.id,
       e.revision,
       e.project_id::integer AS project_id,
       e.claim_id,
       e.kind,
       e.entry_date,
       e.description,
       COALESCE(e.supplier, '')::text AS supplier,
       COALESCE(e.supplier_invoice_number, '')::text AS supplier_invoice_number,
       (e.gross_amount - COALESCE(e.vat_amount, 0))::text AS net_amount,
       e.markup_percent,
       e.distance_km,
       e.bill_rate_per_km,
       e.bill_amount::text AS bill_amount,
       e.currency::text AS currency,
       (e.kind = 'supplier_invoice' AND EXISTS (
           SELECT 1 FROM expenses.entries o
           WHERE o.id <> e.id
             AND o.kind = 'supplier_invoice'
             AND o.invoiced_at IS NOT NULL
             AND lower(btrim(o.supplier)) = lower(btrim(e.supplier))
             AND o.supplier_invoice_number = e.supplier_invoice_number))::boolean AS supplier_invoice_rebilled
FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE e.project_id = ANY(@project_ids::integer[])
  AND expenses.ready_to_invoice(COALESCE(c.status, e.status), e.billable, e.kind, e.bill_amount)
  AND e.invoiced_at IS NULL
  AND (sqlc.narg(until)::date IS NULL OR e.entry_date <= sqlc.narg(until)::date)
ORDER BY e.project_id, e.entry_date, e.id
LIMIT @row_limit;

-- name: BillableExpensesByIDs :many
-- BillableExpensesByIDs is BillableExpensesForProjects over exact lines: those
-- of ids still billable, in id order. A line since invoiced, or no longer
-- ready, is absent rather than an error — the freshness of a draft's held
-- source is exactly that question.
SELECT e.id,
       e.revision,
       e.project_id::integer AS project_id,
       e.claim_id,
       e.kind,
       e.entry_date,
       e.description,
       COALESCE(e.supplier, '')::text AS supplier,
       COALESCE(e.supplier_invoice_number, '')::text AS supplier_invoice_number,
       (e.gross_amount - COALESCE(e.vat_amount, 0))::text AS net_amount,
       e.markup_percent,
       e.distance_km,
       e.bill_rate_per_km,
       e.bill_amount::text AS bill_amount,
       e.currency::text AS currency,
       (e.kind = 'supplier_invoice' AND EXISTS (
           SELECT 1 FROM expenses.entries o
           WHERE o.id <> e.id
             AND o.kind = 'supplier_invoice'
             AND o.invoiced_at IS NOT NULL
             AND lower(btrim(o.supplier)) = lower(btrim(e.supplier))
             AND o.supplier_invoice_number = e.supplier_invoice_number))::boolean AS supplier_invoice_rebilled
FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE e.id = ANY(@ids::bigint[])
  AND e.project_id IS NOT NULL
  AND expenses.ready_to_invoice(COALESCE(c.status, e.status), e.billable, e.kind, e.bill_amount)
  AND e.invoiced_at IS NULL
  AND (sqlc.narg(until)::date IS NULL OR e.entry_date <= sqlc.narg(until)::date)
ORDER BY e.id;

-- name: EntryClaims :many
-- EntryClaims is which travel claim each of ids belongs to, read without a
-- lock: the first half of the module's lock order is the claims, so the
-- holder has to know them before it takes any line. A line never moves into a
-- claim or out of one, which is what makes the unlocked read good enough; the
-- holder still checks every locked line against it.
SELECT id, claim_id FROM expenses.entries
WHERE id = ANY(@ids::bigint[])
ORDER BY id;

-- name: StampEntriesInvoicedByInvoice :many
-- StampEntriesInvoicedByInvoice is the invoices issue's stamp on the lines it
-- invoiced: when (the issue's own time), by whom, and which invoice — with no
-- hand-typed reference, because the invoice is named by its id and number.
-- The rows are already locked and judged by the caller; the guards are the
-- last line, not the rule.
UPDATE expenses.entries SET
    invoiced_at = @issued_at::timestamptz,
    invoiced_by_user_id = @issued_by::uuid,
    invoiced_invoice_id = @invoice_id::bigint,
    invoiced_number = @invoice_number::bigint,
    invoice_reference = NULL,
    revision = revision + 1,
    updated_at = @issued_at::timestamptz
WHERE expenses.entries.id = ANY(@ids::bigint[])
  AND expenses.entries.invoiced_at IS NULL
  AND expenses.ready_to_invoice(
        COALESCE(
          (SELECT c.status FROM expenses.claims c WHERE c.id = expenses.entries.claim_id),
          expenses.entries.status),
        expenses.entries.billable, expenses.entries.kind, expenses.entries.bill_amount)
RETURNING id;

-- name: ReleaseEntriesInvoicedByInvoice :many
-- ReleaseEntriesInvoicedByInvoice takes one invoice's stamp back off the
-- lines a credit note returned: everything the stamp wrote — what
-- UnmarkEntryInvoiced clears and the invoice's id and number — so the line is
-- ready to invoice again. Only a line still carrying that invoice's stamp is
-- touched.
UPDATE expenses.entries SET
    invoiced_at = NULL,
    invoiced_by_user_id = NULL,
    invoice_reference = NULL,
    invoiced_invoice_id = NULL,
    invoiced_number = NULL,
    revision = revision + 1,
    updated_at = @released_at::timestamptz
WHERE id = ANY(@ids::bigint[]) AND invoiced_invoice_id = @invoice_id::bigint
RETURNING id;
