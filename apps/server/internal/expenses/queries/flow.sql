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

-- name: LockClaims :many
-- LockClaims reads the travel claims in ids and holds their rows until the
-- transaction ends, **in id order** — the first half of a mixed batch's lock
-- order. Every write in this module takes a claim's row before it takes any of
-- its lines, so a batch that starts here can never be the one to close a cycle.
-- An id with no claim is simply absent from the result.
SELECT * FROM expenses.claims
WHERE id = ANY(@ids::bigint[])
ORDER BY id
FOR UPDATE;

-- name: LockBatchEntries :many
-- LockBatchEntries holds every expense row one mixed batch is about — the lines
-- of the travel claims it names and the standalone expenses it names — in **one
-- statement, in id order**.
--
-- One statement is the point. Taking each claim's lines separately and the
-- named expenses afterwards would let two batches that name each other's claims
-- cross: batch A holding claim 9's lines and waiting for a line of claim 8,
-- while batch B holds claim 8's lines and waits for a line of claim 9. Asking
-- for every row of the batch in one ascending pass makes that impossible, and it
-- is still the module's documented order — claim rows first, then lines in id
-- order — with the standalone expenses folded into the same ascending pass.
SELECT * FROM expenses.entries
WHERE claim_id = ANY(@claim_ids::bigint[]) OR id = ANY(@entry_ids::bigint[])
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
WHERE id = @id AND status IN ('draft', 'rejected') AND claim_id IS NULL
  -- claim_id IS NULL is the rule this operation is about: the unit that moves
  -- through the flow is a standalone expense or a whole travel claim, never one
  -- of a claim's lines (unitOf in authorize.go). Go refuses a line by id before
  -- this ever runs, so the guard is unreachable today — which is exactly why it
  -- is here: a regression up there should fail loudly rather than quietly move
  -- one line of somebody's trip on its own.
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
WHERE id = ANY(@ids::bigint[]) AND status = 'submitted' AND claim_id IS NULL
  -- claim_id IS NULL is the rule this operation is about: the unit that moves
  -- through the flow is a standalone expense or a whole travel claim, never one
  -- of a claim's lines (unitOf in authorize.go). Go refuses a line by id before
  -- this ever runs, so the guard is unreachable today — which is exactly why it
  -- is here: a regression up there should fail loudly rather than quietly move
  -- one line of somebody's trip on its own.
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
WHERE id = ANY(@ids::bigint[]) AND status = 'submitted' AND claim_id IS NULL
  -- claim_id IS NULL is the rule this operation is about: the unit that moves
  -- through the flow is a standalone expense or a whole travel claim, never one
  -- of a claim's lines (unitOf in authorize.go). Go refuses a line by id before
  -- this ever runs, so the guard is unreachable today — which is exactly why it
  -- is here: a regression up there should fail loudly rather than quietly move
  -- one line of somebody's trip on its own.
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
WHERE id = ANY(@ids::bigint[]) AND status = 'approved' AND claim_id IS NULL
  AND reimbursed_at IS NULL AND invoiced_at IS NULL
  -- claim_id IS NULL is the rule this operation is about: the unit that moves
  -- through the flow is a standalone expense or a whole travel claim, never one
  -- of a claim's lines (unitOf in authorize.go). Go refuses a line by id before
  -- this ever runs, so the guard is unreachable today — which is exactly why it
  -- is here: a regression up there should fail loudly rather than quietly move
  -- one line of somebody's trip on its own.
RETURNING *;

-- The same five moves over the other unit: a whole travel claim. Every stamp is
-- the claim's own — its lines carry none — and each statement repeats the guards
-- the caller already judged under the claim's row lock, so the two can never
-- disagree and a Go regression fails loudly rather than moving a trip nobody
-- decided on.

-- name: SubmitClaim :one
-- SubmitClaim moves one draft or rejected travel claim to submitted. The lines
-- were frozen by FreezeClaimLine in this same transaction, before this ran, so
-- what the trip is worth is settled the moment its status changes. The previous
-- decision goes with it: a rejected claim comes back as if it had never been
-- decided on.
UPDATE expenses.claims SET
    status = 'submitted',
    submitted_at = @now::timestamptz,
    decided_at = NULL,
    decided_by_user_id = NULL,
    rejection_reason = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND status IN ('draft', 'rejected')
RETURNING *;

-- name: FreezeClaimLine :exec
-- FreezeClaimLine writes one line of a travel claim its final figures, in the
-- transaction that submits the claim (decision X4). It is SubmitEntry's body
-- without the status: a line has no status of its own, because the unit that
-- moves is the claim.
--
-- The currency and the three meal percentages are here as well as the amount,
-- because a per diem day is priced from all five and the freeze has just read
-- them again. A rate an approver had overridden is cleared with them: the line
-- has just been priced from the table, so a record saying otherwise would be a
-- lie.
--
-- The claim's own status is guarded rather than the line's. A line's column
-- stays at its default 'draft' and says nothing, and the claim's row is held by
-- the caller, so this reads the very row the freeze was judged against.
UPDATE expenses.entries SET
    currency = @currency,
    rate = @rate,
    passenger_rate = @passenger_rate,
    gross_amount = @gross_amount,
    meal_breakfast_percent = @meal_breakfast_percent,
    meal_lunch_percent = @meal_lunch_percent,
    meal_dinner_percent = @meal_dinner_percent,
    rate_overridden_by_user_id = NULL,
    rate_table_value = NULL,
    passenger_rate_table_value = NULL,
    billable = @billable,
    markup_percent = @markup_percent,
    bill_rate_per_km = @bill_rate_per_km,
    bill_amount = @bill_amount,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE expenses.entries.id = @id
  AND expenses.entries.claim_id = @claim_id
  AND (SELECT c.status FROM expenses.claims c WHERE c.id = expenses.entries.claim_id)
      IN ('draft', 'rejected');

-- name: ApproveClaims :many
-- ApproveClaims moves submitted travel claims to approved, recording who
-- decided and when. The frozen figures are not touched: an approval agrees with
-- them.
UPDATE expenses.claims SET
    status = 'approved',
    decided_at = @now::timestamptz,
    decided_by_user_id = @decided_by::uuid,
    rejection_reason = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'submitted'
RETURNING *;

-- name: RejectClaims :many
-- RejectClaims sends submitted travel claims back with the reason their owner
-- sees. The submission stamp stays: the trip was submitted, and the next submit
-- overwrites it.
UPDATE expenses.claims SET
    status = 'rejected',
    decided_at = @now::timestamptz,
    decided_by_user_id = @decided_by::uuid,
    rejection_reason = @reason::text,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'submitted'
RETURNING *;

-- name: UnapproveClaims :many
-- UnapproveClaims returns approved travel claims to a fresh draft, clearing the
-- decision and the submission stamp. Never one already reimbursed, and never one
-- holding a line that has been invoiced: undoing either of those has its own
-- door on its own track, and the caller has refused them before this runs.
UPDATE expenses.claims SET
    status = 'draft',
    submitted_at = NULL,
    decided_at = NULL,
    decided_by_user_id = NULL,
    rejection_reason = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[])
  AND status = 'approved'
  AND reimbursed_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM expenses.entries e
      WHERE e.claim_id = expenses.claims.id AND e.invoiced_at IS NOT NULL)
RETURNING *;

-- name: ClearClaimLineOverrides :exec
-- ClearClaimLineOverrides drops the rate-override audit from every line of the
-- claims being unapproved — what UnapproveEntries does for a standalone expense,
-- one level up. *Fresh* means the draft carries nothing of the decision that was
-- undone, and an approver's replaced rate is part of that decision. The frozen
-- amounts stay where they are until the next save or submit reprices them.
UPDATE expenses.entries SET
    rate_overridden_by_user_id = NULL,
    rate_table_value = NULL,
    passenger_rate_table_value = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE claim_id = ANY(@claim_ids::bigint[])
  AND rate_overridden_by_user_id IS NOT NULL;

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
--
-- kind <> 'per_diem' is the other half of that: a per diem day is never billed
-- on to a customer, and the capability and the handler both exclude it, so this
-- guard is unreachable today — which is exactly why it is here, beside the
-- module's other SQL twins. A Go regression should write nothing rather than
-- quietly make a day of somebody's subsistence billable.
UPDATE expenses.entries SET
    billing_line_id = @billing_line_id,
    billable = @billable,
    markup_percent = @markup_percent,
    bill_rate_per_km = @bill_rate_per_km,
    bill_amount = @bill_amount,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND revision = @revision AND invoiced_at IS NULL AND kind <> 'per_diem'
RETURNING *;

-- name: CountApprovalGroups :one
-- CountApprovalGroups is how many people have something waiting for this
-- caller — the total the approval queue pages through. It shares its predicate
-- with ListApprovalGroups, so the count and the pages can never disagree.
--
-- The queue is a queue of **units**: a standalone expense, or a whole travel
-- claim. A claim's lines keep their own status column at its default and are
-- never listed loose (unitOf in authorize.go), which is what claim_id IS NULL
-- says here; the claims themselves are the second half of the union. A person
-- with one submitted trip and no loose expenses is one group, counted once.
SELECT count(*) FROM (
    SELECT user_id FROM expenses.entries
    WHERE status = 'submitted'
      AND claim_id IS NULL
      AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
      AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
    UNION
    SELECT user_id FROM expenses.claims
    WHERE status = 'submitted'
      AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
      AND (sqlc.narg(locked_before)::date IS NULL
           OR (departure_at AT TIME ZONE @time_zone::text)::date >= sqlc.narg(locked_before)::date)
) AS groups;

-- name: ListApprovalGroups :many
-- ListApprovalGroups is one page of the approval queue's **people**, taken in
-- the queue's own order: whoever has been waiting longest first — the oldest
-- day among their waiting units — and then their user id. It is deliberately
-- not by display name: the names live in identity, and ordering on them would
-- mean reading every group into Go before paging, which is what this query
-- exists to avoid.
--
-- A unit's day is the expense's own entry date, or the day the trip departed in
-- the installation's business time zone — the same derivation businessDay makes
-- in Go, from the same stored name, so a queue and a period lock can never
-- disagree about which day a trip departed on.
WITH units AS (
    SELECT user_id, entry_date AS waiting_since FROM expenses.entries
    WHERE status = 'submitted'
      AND claim_id IS NULL
      AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
      AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
    UNION ALL
    SELECT user_id, (departure_at AT TIME ZONE @time_zone::text)::date FROM expenses.claims
    WHERE status = 'submitted'
      AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
      AND (sqlc.narg(locked_before)::date IS NULL
           OR (departure_at AT TIME ZONE @time_zone::text)::date >= sqlc.narg(locked_before)::date)
)
SELECT user_id
FROM units
GROUP BY user_id
ORDER BY min(waiting_since), user_id
LIMIT @page_size OFFSET @page_offset;

-- name: ListApprovalGroupEntries :many
-- ListApprovalGroupEntries is the standalone expenses of the people one page of
-- the queue holds. The page of people is taken first (ListApprovalGroups) and
-- only then are their units read, so the database never hands Go more rows than
-- the page needs and a page always holds whole groups.
SELECT * FROM expenses.entries
WHERE status = 'submitted'
  AND claim_id IS NULL
  AND user_id = ANY(@user_ids::uuid[])
  AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
ORDER BY user_id, entry_date, id;

-- name: ListApprovalGroupClaims :many
-- ListApprovalGroupClaims is the other half of the same page: the travel claims
-- of those people, one row each. Its predicate is the union's second branch,
-- written out again so the page and the count cannot drift.
SELECT * FROM expenses.claims
WHERE status = 'submitted'
  AND user_id = ANY(@user_ids::uuid[])
  AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(locked_before)::date IS NULL
       OR (departure_at AT TIME ZONE @time_zone::text)::date >= sqlc.narg(locked_before)::date)
ORDER BY user_id, departure_at, id;
