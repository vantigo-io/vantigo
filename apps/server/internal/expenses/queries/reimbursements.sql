-- Decision X5's first track: what the employee is owed back. Every query here
-- shares one predicate — an approved expense that owes its owner something —
-- written out in full each time rather than hidden in a view, so the count,
-- the page, the export and the update can never disagree about which expenses
-- a payroll run is about.
--
-- "Owes its owner something" is owedToEmployee (responses.go) in SQL: the
-- gross of an outlay the employee paid, the whole of a mileage line, and
-- nothing at all for an outlay the company paid. A gross that rounds to zero
-- owes nothing either, which is why gross_amount > 0 is part of it.

-- name: CountReimbursementGroups :one
-- CountReimbursementGroups is how many people the reimbursement list holds —
-- the total it pages through. Its predicate is ListReimbursementGroupEntries'.
SELECT count(DISTINCT user_id) FROM expenses.entries
WHERE status = 'approved'
  AND gross_amount > 0
  AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
  AND (@reimbursed::boolean = (reimbursed_at IS NOT NULL))
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR entry_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR entry_date <= sqlc.narg(to_date)::date);

-- name: ListReimbursementGroupEntries :many
-- ListReimbursementGroupEntries is one page of the reimbursement list, paged
-- **by person in SQL** exactly as the approval queue is: the people are
-- grouped and the page taken first, and only then are their expenses read, so
-- a page always holds whole groups.
--
-- The page is taken in the order the state asks for. What is waiting puts the
-- person who has waited longest first (the oldest expense date in their
-- group); what has been paid puts the latest payout first, so an undo is
-- reachable without paging. The two orders are separate CASE expressions
-- because one is a date and the other an instant; the caller re-derives the
-- same order in Go from the figures it pages on, which it can, because a page
-- holds whole groups.
WITH groups AS (
    SELECT user_id, min(entry_date) AS oldest, max(reimbursed_at) AS paid
    FROM expenses.entries
    WHERE status = 'approved'
      AND gross_amount > 0
      AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
      AND (@reimbursed::boolean = (reimbursed_at IS NOT NULL))
      AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
      AND (sqlc.narg(from_date)::date IS NULL OR entry_date >= sqlc.narg(from_date)::date)
      AND (sqlc.narg(to_date)::date IS NULL OR entry_date <= sqlc.narg(to_date)::date)
    GROUP BY user_id
    ORDER BY
        CASE WHEN @reimbursed::boolean THEN NULL ELSE min(entry_date) END ASC,
        CASE WHEN @reimbursed::boolean THEN max(reimbursed_at) END DESC,
        user_id
    LIMIT @page_size OFFSET @page_offset
)
SELECT e.* FROM expenses.entries e
JOIN groups ON groups.user_id = e.user_id
WHERE e.status = 'approved'
  AND e.gross_amount > 0
  AND NOT (e.kind = 'outlay' AND (e.paid_by IS NULL OR e.paid_by <> 'employee'))
  AND (@reimbursed::boolean = (e.reimbursed_at IS NOT NULL))
  AND (sqlc.narg(user_id)::uuid IS NULL OR e.user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR e.entry_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR e.entry_date <= sqlc.narg(to_date)::date)
ORDER BY e.user_id, e.entry_date, e.id;

-- name: ListReimbursementRows :many
-- ListReimbursementRows is the export's whole set in one statement: the same
-- predicate again, narrowed either by the list's filters or by explicit ids
-- (all_ids false). It reads one row more than the cap so the caller can tell
-- "this is the whole file" from "there is more than a file may hold" without
-- a second count.
SELECT * FROM expenses.entries
WHERE status = 'approved'
  AND gross_amount > 0
  AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
  AND (@by_ids::boolean OR @reimbursed::boolean = (reimbursed_at IS NOT NULL))
  AND (@all_ids::boolean OR id = ANY(@ids::bigint[]))
  AND (@by_ids::boolean OR sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (@by_ids::boolean OR sqlc.narg(from_date)::date IS NULL OR entry_date >= sqlc.narg(from_date)::date)
  AND (@by_ids::boolean OR sqlc.narg(to_date)::date IS NULL OR entry_date <= sqlc.narg(to_date)::date)
ORDER BY user_id, entry_date, id
LIMIT @row_limit;

-- name: MarkEntriesReimbursed :many
-- MarkEntriesReimbursed records one payroll run on the expenses it paid. The
-- rows are already locked by the caller, which decided every one of them may
-- be paid; the guards here are the last line, not the rule. The expense stays
-- approved — being paid is not a status of its own (design §3.1) — and the
-- frozen figures are not touched.
UPDATE expenses.entries SET
    reimbursed_at = @now::timestamptz,
    reimbursed_by_user_id = @reimbursed_by::uuid,
    reimbursement_date = @reimbursement_date,
    reimbursement_reference = @reference,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[])
  AND status = 'approved'
  AND reimbursed_at IS NULL
  AND gross_amount > 0
  AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
RETURNING *;

-- name: UnmarkEntriesReimbursed :many
-- UnmarkEntriesReimbursed takes a payroll run back off the expenses it was
-- recorded on, the whole of it, so they stand exactly as they did before.
UPDATE expenses.entries SET
    reimbursed_at = NULL,
    reimbursed_by_user_id = NULL,
    reimbursement_date = NULL,
    reimbursement_reference = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND reimbursed_at IS NOT NULL
RETURNING *;

-- name: MarkEntryInvoiced :one
-- MarkEntryInvoiced records that one billable line has been billed on to the
-- customer. The revision is guarded here as well as under the lock, so no
-- caller writes over a revision it did not read, and the conditions the
-- handler judged are repeated so the two can never disagree.
UPDATE expenses.entries SET
    invoiced_at = @now::timestamptz,
    invoiced_by_user_id = @invoiced_by::uuid,
    invoice_reference = @reference,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
  AND revision = @revision
  AND status = 'approved'
  AND billable
  AND bill_amount IS NOT NULL
  AND invoiced_at IS NULL
RETURNING *;

-- name: UnmarkEntryInvoiced :one
-- UnmarkEntryInvoiced takes the invoicing back off one line, under the same
-- revision guard.
UPDATE expenses.entries SET
    invoiced_at = NULL,
    invoiced_by_user_id = NULL,
    invoice_reference = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND revision = @revision AND invoiced_at IS NOT NULL
RETURNING *;
