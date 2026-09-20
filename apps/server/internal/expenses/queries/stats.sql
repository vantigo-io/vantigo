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
-- counts arrive with the claim flow and are added on top of these.
SELECT
    count(*) FILTER (WHERE status = 'draft')     AS drafts,
    count(*) FILTER (WHERE status = 'submitted') AS submitted,
    count(*) FILTER (WHERE status = 'approved')  AS approved,
    count(*) FILTER (WHERE status = 'rejected')  AS rejected
FROM expenses.entries
WHERE user_id = @user_id AND claim_id IS NULL;

-- name: StatsMyUnreimbursed :many
-- StatsMyUnreimbursed is what the caller is still owed, per currency: their
-- own approved expenses that owe them something and have not been paid. The
-- predicate is the reimbursement list's, restricted to one person.
SELECT currency, SUM(gross_amount)::numeric(14,2) AS amount
FROM expenses.entries
WHERE user_id = @user_id
  AND status = 'approved'
  AND reimbursed_at IS NULL
  AND gross_amount > 0
  AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
GROUP BY currency
ORDER BY currency;

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
  AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date);

-- name: StatsNetBuckets :many
-- StatsNetBuckets is the timeseries: the caller's own approved expenses per
-- entry date, as net (gross less VAT), in one currency — the installation's
-- default. Two currencies never add up (design §4), so an expense in another
-- one is left out rather than folded into a number that is in neither. A date
-- is in the period when its midnight UTC falls in [range_from, range_to).
SELECT entry_date AS day,
       SUM(gross_amount - COALESCE(vat_amount, 0))::numeric(14,2) AS value
FROM expenses.entries
WHERE user_id = @user_id
  AND status = 'approved'
  AND currency = @currency
  AND (entry_date::timestamp AT TIME ZONE 'UTC') >= @range_from::timestamptz
  AND (entry_date::timestamp AT TIME ZONE 'UTC') < @range_to::timestamptz
GROUP BY entry_date
ORDER BY entry_date;

-- name: StatsApprovalWaitingGroups :many
-- StatsApprovalWaitingGroups is one attention item per person with something
-- waiting for this caller, under the approval queue's own predicate: how many
-- of their expenses are waiting, and when the oldest of them was submitted.
SELECT user_id,
       count(*)::bigint AS waiting,
       MIN(submitted_at)::timestamptz AS oldest_submitted_at
FROM expenses.entries
WHERE status = 'submitted'
  AND (@see_all::boolean OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
GROUP BY user_id;

-- name: StatsMyRejected :many
-- StatsMyRejected is the caller's own expenses that were sent back, newest
-- decision first. It is capped rather than paged: a dashboard shows the few
-- that matter, and somebody with a hundred rejections has a list to open
-- rather than a dashboard to read.
SELECT id, description, decided_at
FROM expenses.entries
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
SELECT count(*)::bigint AS waiting,
       COALESCE(MIN(decided_at), MIN(updated_at))::timestamptz AS oldest_decided_at
FROM expenses.entries
WHERE status = 'approved'
  AND reimbursed_at IS NULL
  AND gross_amount > 0
  AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
HAVING count(*) > 0;
