-- The dashboard's reads (design §6's Dashboard row). Every figure is scoped in
-- SQL to what the caller may see: their own expenses (user_id), or the
-- approval queue's scope (see_all for expenses:approve, managed_project_ids
-- otherwise, and nothing dated before locked_before — NULL for no lock or for
-- expenses:manage), which is exactly CountApprovalGroups' predicate. None of
-- it runs in a transaction, and none of it needs another module.
--
-- The count is deliberately fixed: a dashboard that costs one statement per
-- expense is a dashboard nobody keeps open.

-- name: StatsMyStatusCounts :one
-- StatsMyStatusCounts is how many of the caller's own **standalone** expenses
-- stand in each status, every status present whether or not anything is in it.
--
-- A travel claim's lines are left out. Their own status column stays at its
-- default 'draft' and is never read (unitOf in authorize.go), so counting them
-- here would report five drafts for a trip its owner can do nothing with one
-- at a time — they cannot be submitted, and the one thing that *is* actionable,
-- the claim, would not be in the figure at all. The claims' own per-status
-- counts are StatsMyClaimStatusCounts below, and the handler adds the two.
SELECT
    count(*) FILTER (WHERE status = 'draft')     AS drafts,
    count(*) FILTER (WHERE status = 'submitted') AS submitted,
    count(*) FILTER (WHERE status = 'approved')  AS approved,
    count(*) FILTER (WHERE status = 'rejected')  AS rejected
FROM expenses.entries
WHERE user_id = @user_id AND claim_id IS NULL;

-- name: StatsMyClaimStatusCounts :one
-- StatsMyClaimStatusCounts is the other half of the figure above: how many of
-- the caller's own **travel claims** stand in each status. A trip counts once,
-- whatever it holds, because a trip is what its owner submits.
SELECT
    count(*) FILTER (WHERE status = 'draft')     AS drafts,
    count(*) FILTER (WHERE status = 'submitted') AS submitted,
    count(*) FILTER (WHERE status = 'approved')  AS approved,
    count(*) FILTER (WHERE status = 'rejected')  AS rejected
FROM expenses.claims
WHERE user_id = @user_id;

-- name: StatsMyUnreimbursed :many
-- StatsMyUnreimbursed is what the caller is still owed, per currency, over
-- every **unit** that owes them: their own approved standalone expenses, and
-- the lines of their own approved travel claims — both not yet paid. The
-- predicate is the reimbursement list's, restricted to one person, and a
-- claim's lines are judged by the claim exactly as the list judges them.
SELECT e.currency, SUM(e.gross_amount)::numeric(14,2) AS amount
FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE e.user_id = @user_id
  AND e.gross_amount > 0
  AND expenses.owes_employee(e.kind, e.paid_by)
  AND COALESCE(c.status, e.status) = 'approved'
  AND COALESCE(c.reimbursed_at, e.reimbursed_at) IS NULL
GROUP BY e.currency
ORDER BY e.currency;

-- name: StatsAwaitingApproval :one
-- StatsAwaitingApproval is the approval figure of both dashboard reads: the
-- submitted expenses in the approval queue's scope now, and how many of those
-- in scope were already waiting when the period began — submitted before it,
-- and either still submitted or decided since. An expense since unapproved or
-- edited back to draft has lost the stamps that say so and is not counted
-- then, exactly as time's own figure behaves.
SELECT
    count(*) FILTER (WHERE status = 'submitted') AS awaiting,
    count(*) FILTER (WHERE submitted_at < @period_from::timestamptz AND (
        status = 'submitted'
        OR (status IN ('approved', 'rejected') AND decided_at >= @period_from::timestamptz)
    )) AS awaiting_at_period_start
FROM expenses.entries
WHERE status IN ('submitted', 'approved', 'rejected')
  AND claim_id IS NULL
  AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date);

-- name: StatsAwaitingApprovalClaims :one
-- StatsAwaitingApprovalClaims is StatsAwaitingApproval over the other unit, so
-- the dashboard counts a trip once rather than once per line it holds. The two
-- figures are added in Go.
SELECT
    count(*) FILTER (WHERE status = 'submitted') AS awaiting,
    count(*) FILTER (WHERE submitted_at < @period_from::timestamptz AND (
        status = 'submitted'
        OR (status IN ('approved', 'rejected') AND decided_at >= @period_from::timestamptz)
    )) AS awaiting_at_period_start
FROM expenses.claims
WHERE status IN ('submitted', 'approved', 'rejected')
  AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(locked_before)::date IS NULL
       OR (departure_at AT TIME ZONE @time_zone::text)::date >= sqlc.narg(locked_before)::date);

-- name: StatsNetBuckets :many
-- StatsNetBuckets is the timeseries: the caller's own approved expenses per
-- entry date, as net (gross less VAT), in one currency — the installation's
-- default. Two currencies never add up (design §4), so an expense in another
-- one is left out rather than folded into a number that is in neither. A date
-- is in the period when its midnight UTC falls in [range_from, range_to).
SELECT e.entry_date AS day,
       SUM(e.gross_amount - COALESCE(e.vat_amount, 0))::numeric(14,2) AS value
FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE e.user_id = @user_id
  AND COALESCE(c.status, e.status) = 'approved'
  AND e.currency = @currency
  AND (e.entry_date::timestamp AT TIME ZONE 'UTC') >= @range_from::timestamptz
  AND (e.entry_date::timestamp AT TIME ZONE 'UTC') < @range_to::timestamptz
GROUP BY e.entry_date
ORDER BY e.entry_date;

-- name: StatsApprovalWaitingGroups :many
-- StatsApprovalWaitingGroups is one attention item per person with something
-- waiting for this caller, under the approval queue's own predicate: how many
-- of their expenses are waiting, and when the oldest of them was submitted.
WITH units AS (
    SELECT user_id, submitted_at FROM expenses.entries
    WHERE status = 'submitted'
      AND claim_id IS NULL
      AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
      AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
    UNION ALL
    SELECT user_id, submitted_at FROM expenses.claims
    WHERE status = 'submitted'
      AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
      AND (sqlc.narg(locked_before)::date IS NULL
           OR (departure_at AT TIME ZONE @time_zone::text)::date >= sqlc.narg(locked_before)::date)
)
SELECT user_id,
       count(*)::bigint AS waiting,
       MIN(submitted_at)::timestamptz AS oldest_submitted_at
FROM units
GROUP BY user_id;

-- name: StatsMyRejected :many
-- StatsMyRejected is the caller's own expenses that were sent back, newest
-- decision first. It is capped rather than paged: a dashboard shows the few
-- that matter, and somebody with a hundred rejections has a list to open
-- rather than a dashboard to read.
SELECT id, description, decided_at
FROM expenses.entries
WHERE user_id = @user_id AND claim_id IS NULL AND status = 'rejected' AND decided_at IS NOT NULL
ORDER BY decided_at DESC, id DESC
LIMIT @row_limit;

-- name: StatsMyRejectedClaims :many
-- StatsMyRejectedClaims is the same list over the other unit: the caller's own
-- travel claims that were sent back. One item per trip, titled by its purpose —
-- never one per line, which would bury the dashboard under a trip nobody can act
-- on a piece of.
SELECT id, purpose, decided_at
FROM expenses.claims
WHERE user_id = @user_id AND status = 'rejected' AND decided_at IS NOT NULL
ORDER BY decided_at DESC, id DESC
LIMIT @row_limit;

-- name: StatsReimbursementsWaiting :many
-- StatsReimbursementsWaiting is the payroll clerk's single item: how many
-- expenses are waiting to be paid anywhere in the installation, and when the
-- oldest of them was approved. Only expenses:manage asks, and expenses:manage
-- sees every expense, so there is no scope to narrow it by.
--
-- It answers one row, or none at all when nothing is waiting — which is why
-- it is :many over an aggregate with a HAVING: an empty set's MIN is NULL,
-- and "no row" says what the item means better than a row of nulls would.
-- An approved expense always carries decided_at; the fallback is there so a
-- row that somehow does not cannot turn the dashboard into a 500.
WITH units AS (
    SELECT decided_at, updated_at FROM expenses.entries
    WHERE status = 'approved'
      AND claim_id IS NULL
      AND reimbursed_at IS NULL
      AND gross_amount > 0
      AND expenses.owes_employee(kind, paid_by)
    UNION ALL
    SELECT c.decided_at, c.updated_at FROM expenses.claims c
    WHERE c.status = 'approved'
      AND c.reimbursed_at IS NULL
      AND EXISTS (
          SELECT 1 FROM expenses.entries e
          WHERE e.claim_id = c.id
            AND e.gross_amount > 0
            AND expenses.owes_employee(e.kind, e.paid_by))
)
SELECT count(*)::bigint AS waiting,
       COALESCE(MIN(decided_at), MIN(updated_at))::timestamptz AS oldest_decided_at
FROM units
HAVING count(*) > 0;
