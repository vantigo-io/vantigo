-- name: GetEntries :many
-- GetEntries reads the expenses in ids without locking them: what a batch
-- reads before its transaction, to learn which projects it will need the
-- caller's role and the project's billing type for. An id with no expense is
-- simply absent from the result.
SELECT * FROM expenses.entries WHERE id = ANY(@ids::bigint[]);

-- name: LockEntries :many
-- LockEntries reads the expenses in ids and holds their rows until the
-- transaction ends, in id order, so two batches over overlapping expenses take
-- their locks in the same order and cannot deadlock. An id with no expense is
-- simply absent from the result.
SELECT * FROM expenses.entries
WHERE id = ANY(@ids::bigint[])
ORDER BY id
FOR UPDATE;

-- name: SubmitEntry :one
-- SubmitEntry moves one draft or rejected expense to submitted and freezes it
-- (decision X4): the rate, the passenger supplement, the amount and what the
-- customer is billed are written one last time, computed in Go from the tables
-- in force on the expense's own date, and nothing recomputes them afterwards.
-- It is one statement per expense rather than one per batch because each of
-- them freezes different numbers.
--
-- A rate somebody had overridden is cleared with it: the line has just been
-- priced from the table again, so a record saying otherwise would be a lie.
-- The previous decision goes too — a rejected line comes back as if it had
-- never been decided on. The row is already locked by the caller, which decided
-- it may be submitted; the status guard is the last line, not the rule.
UPDATE expenses.entries SET
    rate = @rate,
    passenger_rate = @passenger_rate,
    gross_amount = @gross_amount,
    rate_overridden_by_user_id = NULL,
    rate_table_value = NULL,
    passenger_rate_table_value = NULL,
    billable = @billable,
    markup_percent = @markup_percent,
    bill_rate_per_km = @bill_rate_per_km,
    bill_amount = @bill_amount,
    status = 'submitted',
    submitted_at = @now::timestamptz,
    decided_at = NULL,
    decided_by_user_id = NULL,
    rejection_reason = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND status IN ('draft', 'rejected')
RETURNING *;

-- name: ApproveEntries :many
-- ApproveEntries moves submitted expenses to approved, recording who decided
-- and when. The frozen figures are not touched: an approval agrees with them.
-- The rows are already locked by the caller, which decided every one of them
-- may be approved; the status guard is the last line, not the rule.
UPDATE expenses.entries SET
    status = 'approved',
    decided_at = @now::timestamptz,
    decided_by_user_id = @decided_by::uuid,
    rejection_reason = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'submitted'
RETURNING *;

-- name: RejectEntries :many
-- RejectEntries moves submitted expenses to rejected with the reason their
-- owner sees. The submission stamp stays: the expense was submitted, and the
-- next submit overwrites it.
UPDATE expenses.entries SET
    status = 'rejected',
    decided_at = @now::timestamptz,
    decided_by_user_id = @decided_by::uuid,
    rejection_reason = @reason::text,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'submitted'
RETURNING *;

-- name: UnapproveEntries :many
-- UnapproveEntries returns approved expenses to a fresh draft, clearing the
-- decision, the submission stamp and the record of any rate an approver had
-- overridden: *fresh* means the draft carries nothing of the decision that was
-- undone, and an override is part of that decision. The frozen amounts stay
-- where they are — they are what the line was approved at — until the next save
-- or submit reprices them from the table, which is also where the audit would
-- have been cleared had the line gone forward instead. Never an expense already
-- reimbursed or invoiced; the caller has refused those, and the guard here says
-- so too.
UPDATE expenses.entries SET
    status = 'draft',
    submitted_at = NULL,
    decided_at = NULL,
    decided_by_user_id = NULL,
    rejection_reason = NULL,
    rate_overridden_by_user_id = NULL,
    rate_table_value = NULL,
    passenger_rate_table_value = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'approved'
  AND reimbursed_at IS NULL AND invoiced_at IS NULL
RETURNING *;

-- name: OverrideEntryRate :one
-- OverrideEntryRate replaces a submitted mileage line's or per diem day's rate
-- and the amount it was frozen at (decision X8), recording who did it and what
-- the table had said
-- about *both* rates it can replace. Each of the two table values keeps what the
-- line was frozen at before the **first** override of that rate, so a second one
-- never loses the table's own figure; the passenger one is only recorded when
-- this request actually names a supplement, so its absence means "never
-- touched" rather than "the same as the one still on the line". The revision is
-- guarded here as well as under the lock, so no caller writes over a revision it
-- did not read.
UPDATE expenses.entries SET
    rate = @rate,
    passenger_rate = @passenger_rate,
    gross_amount = @gross_amount,
    rate_overridden_by_user_id = @overridden_by::uuid,
    rate_table_value = COALESCE(rate_table_value, rate),
    passenger_rate_table_value = CASE WHEN @passenger_overridden::boolean
        THEN COALESCE(passenger_rate_table_value, passenger_rate)
        ELSE passenger_rate_table_value END,
    revision = revision + 1,
    updated_at = @now::timestamptz
--
-- The status guarded is the **unit's** (unitOf in authorize.go): a line inside
-- a travel claim is submitted exactly when its claim is, and its own column
-- stays at its default. The handler holds the claim's row lock before this
-- runs, so the subquery reads the very row it judged.
WHERE expenses.entries.id = @id
  AND expenses.entries.revision = @revision
  AND expenses.entries.kind IN ('mileage', 'per_diem')
  AND COALESCE(
        (SELECT c.status FROM expenses.claims c WHERE c.id = expenses.entries.claim_id),
        expenses.entries.status) = 'submitted'
RETURNING *;

-- name: UpdateEntryBilling :one
-- UpdateEntryBilling is the project side's pricing: the billing columns and
-- nothing else. The expense's own amount, status and stamps are untouched, and
-- an invoiced line is refused here as well as by the caller.
UPDATE expenses.entries SET
    billing_line_id = @billing_line_id,
    billable = @billable,
    markup_percent = @markup_percent,
    bill_rate_per_km = @bill_rate_per_km,
    bill_amount = @bill_amount,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND revision = @revision AND invoiced_at IS NULL
RETURNING *;

-- name: CountApprovalGroups :one
-- CountApprovalGroups is how many people have something waiting for this
-- caller — the total the approval queue pages through. It shares its predicate
-- with ListApprovalGroupEntries, so the count and the pages can never disagree.
SELECT count(DISTINCT user_id) FROM expenses.entries
WHERE status = 'submitted'
  AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date);

-- name: ListApprovalGroupEntries :many
-- ListApprovalGroupEntries is one page of the approval queue, paged **by
-- person in SQL**: the people are grouped and the page taken first, and only
-- then are their expenses read, so a page always holds whole groups and the
-- database never hands Go more rows than the page needs.
--
-- The page is taken in the queue's own order: the person who has been waiting
-- longest first — the oldest expense date in their group — and then their user
-- id. It is deliberately not by display name: the names live in identity, and
-- ordering on them would mean reading every group into Go before paging, which
-- is what this query exists to avoid.
--
-- The rows themselves come back by person and then by day, and the caller puts
-- the groups back into the queue's order from the very figures it pages on
-- (min(entry_date), user id) — which it can, because a page holds whole groups.
WITH groups AS (
    SELECT user_id, min(entry_date) AS oldest
    FROM expenses.entries
    WHERE status = 'submitted'
      AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
      AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
    GROUP BY user_id
    ORDER BY min(entry_date), user_id
    LIMIT @page_size OFFSET @page_offset
)
SELECT e.* FROM expenses.entries e
JOIN groups ON groups.user_id = e.user_id
WHERE e.status = 'submitted'
  AND (@see_all::boolean OR (e.project_id IS NOT NULL AND e.project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(locked_before)::date IS NULL OR e.entry_date >= sqlc.narg(locked_before)::date)
ORDER BY e.user_id, e.entry_date, e.id;
