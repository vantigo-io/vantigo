-- The dashboard's stats and the project time summary (design §4.2, §7). Every
-- dashboard query is scoped in SQL to what the caller may see: their own
-- entries (user_id), or the approval queue's scope (see_all for
-- time:approve, managed_project_ids otherwise, and nothing dated before
-- locked_before — NULL for no lock or for time:manage), exactly
-- ListApprovalGroups' predicate. The project summary is over one project the
-- handler has already decided the caller sees. Week start in SQL is the
-- Monday, ISO day of week 1.

-- name: StatsWeekHours :one
-- StatsWeekHours is the summary's hours: the caller's own hours dated in the
-- current week and in the week before it, rejected entries left out (they
-- were sent back, not accepted), whatever the others' status. week_end is
-- the current week's Sunday.
SELECT
    COALESCE(SUM(hours) FILTER (WHERE entry_date >= @week_start::date), 0)::numeric(9,2) AS this_week,
    COALESCE(SUM(hours) FILTER (WHERE entry_date < @week_start::date), 0)::numeric(9,2) AS previous_week
FROM time.entries
WHERE user_id = @user_id
  AND status <> 'rejected'
  AND entry_date BETWEEN @previous_start::date AND @week_end::date;

-- name: StatsAwaitingApproval :one
-- StatsAwaitingApproval is the summary's approval figure: the submitted
-- entries in the approval queue's scope now, and how many of those in scope
-- were waiting when the period began — submitted before it, and either still
-- submitted or decided since (approved or, for a rejection, last changed at or
-- after period_from). An entry since unapproved or edited back to draft has
-- lost the stamps that say so, and is not counted then.
SELECT
    count(*) FILTER (WHERE status = 'submitted') AS awaiting,
    count(*) FILTER (WHERE submitted_at < @period_from::timestamptz AND (
        status = 'submitted'
        OR (status IN ('approved', 'invoiced') AND approved_at >= @period_from::timestamptz)
        OR (status = 'rejected' AND updated_at >= @period_from::timestamptz)
    )) AS awaiting_at_period_start
FROM time.entries
WHERE status IN ('submitted', 'approved', 'invoiced', 'rejected')
  AND (@see_all::boolean OR project_id = ANY(@managed_project_ids::integer[]))
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date);

-- name: StatsHourBuckets :many
-- StatsHourBuckets is the timeseries: the caller's own hours per entry date,
-- rejected ones left out and, for billable_only, non-billable ones too. A
-- date is in the period when its midnight UTC falls in [range_from,
-- range_to). Days with nothing are absent rather than zero, as every other
-- module's buckets are.
SELECT entry_date AS day, SUM(hours)::numeric(9,2) AS value
FROM time.entries
WHERE user_id = @user_id
  AND status <> 'rejected'
  AND (NOT @billable_only::boolean OR billable)
  AND (entry_date::timestamp AT TIME ZONE 'UTC') >= @range_from::timestamptz
  AND (entry_date::timestamp AT TIME ZONE 'UTC') < @range_to::timestamptz
GROUP BY entry_date
ORDER BY entry_date;

-- name: StatsUnsubmittedWeeks :many
-- StatsUnsubmittedWeeks is the caller's past weeks — before current_week —
-- with draft entries in them, and the drafts' hours, oldest first. Drafts
-- dated before locked_before (NULL for no lock, or for time:manage) are left
-- out: their owner can no longer submit them.
SELECT (entry_date - (EXTRACT(ISODOW FROM entry_date)::integer - 1))::date AS week_start,
       SUM(hours)::numeric(9,2) AS hours
FROM time.entries
WHERE user_id = @user_id
  AND status = 'draft'
  AND entry_date < @current_week::date
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
GROUP BY week_start
ORDER BY week_start;

-- name: StatsWaitingApprovals :many
-- StatsWaitingApprovals is the approval queue's groups — per person and week,
-- under ListApprovalGroups' predicate — restricted to the entries submitted
-- before submitted_before, with the oldest submission in each.
SELECT user_id,
       (entry_date - (EXTRACT(ISODOW FROM entry_date)::integer - 1))::date AS week_start,
       SUM(hours)::numeric(9,2) AS hours,
       MIN(submitted_at)::timestamptz AS oldest_submitted_at
FROM time.entries
WHERE status = 'submitted'
  AND submitted_at < @submitted_before::timestamptz
  AND (@see_all::boolean OR project_id = ANY(@managed_project_ids::integer[]))
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
GROUP BY user_id, week_start;

-- name: ProjectHourGroups :many
-- ProjectHourGroups is one project's hours per status, billing line and
-- person, every status included; the project summary folds the groups into
-- its per-status, per-line and per-person figures.
SELECT status, billing_line_id, user_id, SUM(hours)::numeric(9,2) AS hours
FROM time.entries
WHERE project_id = @project_id
GROUP BY status, billing_line_id, user_id;

-- name: ProjectBillingTotals :many
-- ProjectBillingTotals is what one project's billable time is worth, per
-- bill currency: the submitted, approved and invoiced billable hours at the
-- bill rate each entry snapshotted, summed exactly and rounded to cents, and
-- the hours of those entries that have no rate (bill_currency NULL, then).
SELECT bill_currency,
       COALESCE(ROUND(SUM(hours * bill_rate) FILTER (WHERE bill_rate IS NOT NULL), 2), 0)::numeric(14,2) AS amount,
       COALESCE(SUM(hours) FILTER (WHERE bill_rate IS NOT NULL), 0)::numeric(9,2) AS priced_hours,
       COALESCE(SUM(hours) FILTER (WHERE bill_rate IS NULL), 0)::numeric(9,2) AS unpriced_hours
FROM time.entries
WHERE project_id = @project_id
  AND billable
  AND status IN ('submitted', 'approved', 'invoiced')
GROUP BY bill_currency;
