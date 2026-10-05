-- The billing milestone queries of invoiced work (invoices work design D1, D3):
-- the billable milestones read Invoices builds a draft from, and the stamp and
-- the release its issue makes through contracts.InvoicedWorkHolder on its own
-- transaction (invoiced_work.go, billable.go). The holder locks with
-- LockProject (queries/projects.sql) and LockMilestonesByIDs
-- (queries/milestones.sql) before any of these writes.

-- name: BillableMilestonesForProjects :many
-- BillableMilestonesForProjects is every milestone ready to invoice on the
-- given projects: status 'ready', which an invoiced, planned or cancelled
-- milestone is not. The project's currency and fixed price ride along, read
-- from this module's own projects table, because a percent milestone's amount
-- is a share of that price and its currency is the project's. until bounds the
-- milestones by the Oslo business day they became ready (the day Invoices
-- dates a milestone by); NULL is no bound. The plan's own order within each
-- project, and one row past the limit the caller asks for, so it can say there
-- was more.
SELECT m.id, m.revision, m.project_id, m.name, m.description, m.planned_date,
       COALESCE(m.ready_at, m.updated_at)::timestamptz AS ready_at,
       m.amount, m.amount_currency, m.percent, m.invoiced_amount,
       p.currency AS project_currency, p.fixed_price_amount
FROM projects.billing_milestones m
JOIN projects.projects p ON p.id = m.project_id
WHERE m.project_id = ANY(@project_ids::integer[])
  AND m.status = 'ready'
  AND (sqlc.narg(until)::date IS NULL
       OR (COALESCE(m.ready_at, m.updated_at) AT TIME ZONE 'Europe/Oslo')::date <= sqlc.narg(until)::date)
ORDER BY m.project_id, m.position, m.id
LIMIT @row_limit;

-- name: BillableMilestonesByIDs :many
-- BillableMilestonesByIDs is BillableMilestonesForProjects for exactly the
-- given milestones — a held source's freshness: one since invoiced, or no
-- longer ready, is simply absent.
SELECT m.id, m.revision, m.project_id, m.name, m.description, m.planned_date,
       COALESCE(m.ready_at, m.updated_at)::timestamptz AS ready_at,
       m.amount, m.amount_currency, m.percent, m.invoiced_amount,
       p.currency AS project_currency, p.fixed_price_amount
FROM projects.billing_milestones m
JOIN projects.projects p ON p.id = m.project_id
WHERE m.id = ANY(@ids::integer[])
  AND m.status = 'ready'
  AND (sqlc.narg(until)::date IS NULL
       OR (COALESCE(m.ready_at, m.updated_at) AT TIME ZONE 'Europe/Oslo')::date <= sqlc.narg(until)::date)
ORDER BY m.project_id, m.position, m.id
LIMIT @row_limit;

-- name: StampMilestoneInvoicedByInvoice :one
-- StampMilestoneInvoicedByInvoice is the invoices issue's stamp on one locked,
-- judged milestone: invoiced, by the issuer, at the issue's own instant, with
-- the invoice's date, id and number, and the effective amount frozen as the
-- holder computed it. There is no free-text reference: the invoice is named
-- by its id and number. Every timestamp is the issue's, never now(). The
-- status guard is the statement's last line of defence, not the rule — the
-- holder has judged the row under its lock already.
UPDATE projects.billing_milestones SET
    status = 'invoiced',
    invoiced_at = @issued_at::timestamptz,
    invoiced_by_user_id = @issued_by,
    invoice_date = @issue_date,
    invoiced_amount = @invoiced_amount,
    invoice_reference = NULL,
    invoiced_invoice_id = @invoice_id,
    invoiced_number = @invoice_number,
    ever_moved = true,
    revision = revision + 1,
    updated_at = @issued_at::timestamptz
WHERE id = @id AND status = 'ready'
RETURNING *;

-- name: ReleaseMilestoneInvoicedByInvoice :one
-- ReleaseMilestoneInvoicedByInvoice takes an invoice's stamp back off one
-- locked milestone, in the issue of the credit note that returns its line:
-- invoiced → ready, the five invoice columns and the invoice's id and number
-- cleared, the ready stamps kept. amount, amount_currency and percent are the
-- holder's to decide — carried over, or converted to the frozen amount when
-- the fixed price the percent was a share of is gone (milestoneMoveRefusal).
-- Only a milestone still carrying this invoice's stamp matches.
UPDATE projects.billing_milestones SET
    status = 'ready',
    amount = @amount,
    amount_currency = @amount_currency,
    percent = @percent,
    invoiced_at = NULL,
    invoiced_by_user_id = NULL,
    invoice_reference = NULL,
    invoice_date = NULL,
    invoiced_amount = NULL,
    invoiced_invoice_id = NULL,
    invoiced_number = NULL,
    ever_moved = true,
    revision = revision + 1,
    updated_at = @released_at::timestamptz
WHERE id = @id AND invoiced_invoice_id = @invoice_id
RETURNING *;
