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

-- The predicate, written out once in words and then in full in every query
-- below: a **unit** — a standalone expense, or a whole travel claim — that is
-- approved, has not been paid (or has, for state=reimbursed) and owes its owner
-- something. A claim owes what its lines owe; a claim whose lines owe nothing is
-- not in the list, exactly as a company-paid outlay is not.

-- name: CountReimbursementGroups :one
-- CountReimbursementGroups is how many people the reimbursement list holds —
-- the total it pages through. Its predicate is ListReimbursementGroups'.
SELECT count(*) FROM (
    SELECT user_id FROM expenses.entries
    WHERE status = 'approved'
      AND claim_id IS NULL
      AND gross_amount > 0
      AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
      AND (@reimbursed::boolean = (reimbursed_at IS NOT NULL))
      AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
      AND (sqlc.narg(from_date)::date IS NULL OR entry_date >= sqlc.narg(from_date)::date)
      AND (sqlc.narg(to_date)::date IS NULL OR entry_date <= sqlc.narg(to_date)::date)
    UNION
    SELECT user_id FROM expenses.claims c
    WHERE c.status = 'approved'
      AND (@reimbursed::boolean = (c.reimbursed_at IS NOT NULL))
      AND EXISTS (
          SELECT 1 FROM expenses.entries e
          WHERE e.claim_id = c.id
            AND e.gross_amount > 0
            AND NOT (e.kind = 'outlay' AND (e.paid_by IS NULL OR e.paid_by <> 'employee')))
      AND (sqlc.narg(user_id)::uuid IS NULL OR c.user_id = sqlc.narg(user_id)::uuid)
      AND (sqlc.narg(from_date)::date IS NULL
           OR (c.departure_at AT TIME ZONE @time_zone::text)::date >= sqlc.narg(from_date)::date)
      AND (sqlc.narg(to_date)::date IS NULL
           OR (c.departure_at AT TIME ZONE @time_zone::text)::date <= sqlc.narg(to_date)::date)
) AS groups;

-- name: ListReimbursementGroups :many
-- ListReimbursementGroups is one page of the list's **people**, paged in SQL
-- exactly as the approval queue's are: the people are grouped and the page taken
-- first, and only then are their units read, so a page always holds whole
-- groups.
--
-- The order is the one the state asks for. What is waiting puts the person who
-- has waited longest first (the oldest day among their units); what has been
-- paid puts the latest payout first, so an undo is reachable without paging. The
-- two orders are separate CASE expressions because one is a date and the other
-- an instant.
WITH units AS (
    SELECT user_id, entry_date AS waiting_since, reimbursed_at FROM expenses.entries
    WHERE status = 'approved'
      AND claim_id IS NULL
      AND gross_amount > 0
      AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
      AND (@reimbursed::boolean = (reimbursed_at IS NOT NULL))
      AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
      AND (sqlc.narg(from_date)::date IS NULL OR entry_date >= sqlc.narg(from_date)::date)
      AND (sqlc.narg(to_date)::date IS NULL OR entry_date <= sqlc.narg(to_date)::date)
    UNION ALL
    SELECT c.user_id, (c.departure_at AT TIME ZONE @time_zone::text)::date, c.reimbursed_at
    FROM expenses.claims c
    WHERE c.status = 'approved'
      AND (@reimbursed::boolean = (c.reimbursed_at IS NOT NULL))
      AND EXISTS (
          SELECT 1 FROM expenses.entries e
          WHERE e.claim_id = c.id
            AND e.gross_amount > 0
            AND NOT (e.kind = 'outlay' AND (e.paid_by IS NULL OR e.paid_by <> 'employee')))
      AND (sqlc.narg(user_id)::uuid IS NULL OR c.user_id = sqlc.narg(user_id)::uuid)
      AND (sqlc.narg(from_date)::date IS NULL
           OR (c.departure_at AT TIME ZONE @time_zone::text)::date >= sqlc.narg(from_date)::date)
      AND (sqlc.narg(to_date)::date IS NULL
           OR (c.departure_at AT TIME ZONE @time_zone::text)::date <= sqlc.narg(to_date)::date)
)
SELECT user_id
FROM units
GROUP BY user_id
ORDER BY
    CASE WHEN @reimbursed::boolean THEN NULL ELSE min(waiting_since) END ASC,
    CASE WHEN @reimbursed::boolean THEN max(reimbursed_at) END DESC,
    user_id
LIMIT @page_size OFFSET @page_offset;

-- name: ListReimbursementGroupEntries :many
-- ListReimbursementGroupEntries is the standalone expenses of the people one
-- page holds.
SELECT * FROM expenses.entries
WHERE status = 'approved'
  AND claim_id IS NULL
  AND gross_amount > 0
  AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
  AND (@reimbursed::boolean = (reimbursed_at IS NOT NULL))
  AND user_id = ANY(@user_ids::uuid[])
  AND (sqlc.narg(from_date)::date IS NULL OR entry_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR entry_date <= sqlc.narg(to_date)::date)
ORDER BY user_id, entry_date, id;

-- name: ListReimbursementGroupClaims :many
-- ListReimbursementGroupClaims is the other half of the same page: the travel
-- claims of those people, one row each.
SELECT c.* FROM expenses.claims c
WHERE c.status = 'approved'
  AND (@reimbursed::boolean = (c.reimbursed_at IS NOT NULL))
  AND c.user_id = ANY(@user_ids::uuid[])
  AND EXISTS (
      SELECT 1 FROM expenses.entries e
      WHERE e.claim_id = c.id
        AND e.gross_amount > 0
        AND NOT (e.kind = 'outlay' AND (e.paid_by IS NULL OR e.paid_by <> 'employee')))
  AND (sqlc.narg(from_date)::date IS NULL
       OR (c.departure_at AT TIME ZONE @time_zone::text)::date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL
       OR (c.departure_at AT TIME ZONE @time_zone::text)::date <= sqlc.narg(to_date)::date)
ORDER BY c.user_id, c.departure_at, c.id;

-- name: ListReimbursementRows :many
-- ListReimbursementRows is the export's whole set in one statement, and it is a
-- set of **lines** rather than of units: a standalone expense is its own line,
-- and a travel claim contributes one row per expense it holds. The claim's
-- purpose comes with each of them, because the file says which trip a line was
-- on.
--
-- The same predicate again, narrowed either by the list's filters or by explicit
-- ids (all_ids false — a **standalone** expense named by entry_ids, or every
-- line of a claim named by claim_ids). It reads one row more than the cap so the
-- caller can tell "this is the whole file" from "there is more than a file may
-- hold" without a second count.
--
-- claim_id IS NULL on the entry_ids branch is the module's rule that a claim's
-- line is never named on its own: the unit a payroll run pays is the whole trip,
-- so a file must never hold one line of one. Go refuses such an id by id before
-- this runs (missingExportIDs), so the guard is the last line rather than the
-- rule — but here the two together are what keeps half a trip out of payroll.
--
-- The claim's departure day comes with each of its lines as well as its
-- purpose: it is the day the *unit* is read at, which is what the file groups a
-- trip's lines by, and it is a day in the installation's own zone rather than an
-- instant (reimbursementCSV).
SELECT sqlc.embed(e), c.purpose AS claim_purpose,
       (c.departure_at AT TIME ZONE @time_zone::text)::date AS claim_departure_day
FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE e.gross_amount > 0
  AND NOT (e.kind = 'outlay' AND (e.paid_by IS NULL OR e.paid_by <> 'employee'))
  AND (
      (e.claim_id IS NULL AND e.status = 'approved'
       AND (@by_ids::boolean OR @reimbursed::boolean = (e.reimbursed_at IS NOT NULL)))
      OR (c.id IS NOT NULL AND c.status = 'approved'
       AND (@by_ids::boolean OR @reimbursed::boolean = (c.reimbursed_at IS NOT NULL)))
  )
  AND (@all_ids::boolean
       OR (e.claim_id IS NULL AND e.id = ANY(@entry_ids::bigint[]))
       OR e.claim_id = ANY(@claim_ids::bigint[]))
  AND (@by_ids::boolean OR sqlc.narg(user_id)::uuid IS NULL OR e.user_id = sqlc.narg(user_id)::uuid)
  AND (@by_ids::boolean OR sqlc.narg(from_date)::date IS NULL
       OR COALESCE((c.departure_at AT TIME ZONE @time_zone::text)::date, e.entry_date) >= sqlc.narg(from_date)::date)
  AND (@by_ids::boolean OR sqlc.narg(to_date)::date IS NULL
       OR COALESCE((c.departure_at AT TIME ZONE @time_zone::text)::date, e.entry_date) <= sqlc.narg(to_date)::date)
ORDER BY e.user_id, e.entry_date, e.id
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
  AND claim_id IS NULL
  AND reimbursed_at IS NULL
  AND gross_amount > 0
  AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
  -- claim_id IS NULL is the rule this operation is about: a payroll run covers
  -- a standalone expense or a whole travel claim, never one of a claim's lines
  -- (unitOf in authorize.go). Go refuses a line by id before this ever runs, so
  -- the guard is unreachable today — which is exactly why it is here: a
  -- regression up there should fail loudly rather than quietly pay one line of
  -- somebody's trip on its own.
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
WHERE id = ANY(@ids::bigint[]) AND reimbursed_at IS NOT NULL AND claim_id IS NULL
  -- claim_id IS NULL is the rule this operation is about: a payroll run covers
  -- a standalone expense or a whole travel claim, never one of a claim's lines
  -- (unitOf in authorize.go). Go refuses a line by id before this ever runs, so
  -- the guard is unreachable today — which is exactly why it is here: a
  -- regression up there should fail loudly rather than quietly pay one line of
  -- somebody's trip on its own.
RETURNING *;

-- name: MarkClaimsReimbursed :many
-- MarkClaimsReimbursed records one payroll run on the travel claims it paid.
-- The stamp is the claim's own and its lines carry none: a trip is paid as one
-- unit, for the sum of what its lines owe its owner. The rows are already
-- locked by the caller, which decided every one of them may be paid; the guards
-- here are the last line, not the rule.
UPDATE expenses.claims SET
    reimbursed_at = @now::timestamptz,
    reimbursed_by_user_id = @reimbursed_by::uuid,
    reimbursement_date = @reimbursement_date,
    reimbursement_reference = @reference,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[])
  AND status = 'approved'
  AND reimbursed_at IS NULL
  AND EXISTS (
      SELECT 1 FROM expenses.entries e
      WHERE e.claim_id = expenses.claims.id
        AND e.gross_amount > 0
        AND NOT (e.kind = 'outlay' AND (e.paid_by IS NULL OR e.paid_by <> 'employee')))
RETURNING *;

-- name: UnmarkClaimsReimbursed :many
-- UnmarkClaimsReimbursed takes a payroll run back off the travel claims it was
-- recorded on, the whole of it, so they stand in the waiting list exactly as
-- they did before.
UPDATE expenses.claims SET
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
--
-- The status guarded is the **unit's** (unitOf in authorize.go): a line inside
-- a travel claim is approved exactly when its claim is, and its own column
-- stays at its default. The handler holds the claim's row lock before this
-- runs, so the subquery reads the very row it judged.
WHERE expenses.entries.id = @id
  AND expenses.entries.revision = @revision
  AND COALESCE(
        (SELECT c.status FROM expenses.claims c WHERE c.id = expenses.entries.claim_id),
        expenses.entries.status) = 'approved'
  -- A per diem day is never billed on to a customer and so is never invoiced.
  -- The capability and the handler both exclude it, so this is unreachable
  -- today; it is here for the reason every other SQL twin in this module is.
  AND expenses.entries.kind <> 'per_diem'
  AND expenses.entries.billable
  AND expenses.entries.bill_amount IS NOT NULL
  AND expenses.entries.invoiced_at IS NULL
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
