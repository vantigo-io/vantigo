-- The four stats queries (design §4.3). Every one of them starts from
-- projects.visible(p.id, user_id, see_all) — the same predicate ListProjects
-- and CountProjects filter on, defined once in the baseline migration — so a
-- caller's KPI row, dashboard card, sparkline and attention list are all over
-- exactly the projects their list would show them. A number computed over the
-- whole table would tell a member how many projects they may not open.

-- name: ProjectStatusCounts :one
-- ProjectStatusCounts is the list page's key-figure row: how many of the
-- projects the caller may see stand in each of the five statuses. FILTER
-- rather than GROUP BY, so a status nobody is in still answers 0 instead of
-- being missing from the result.
SELECT
    count(*) FILTER (WHERE p.status = 'planned') AS planned,
    count(*) FILTER (WHERE p.status = 'active') AS active,
    count(*) FILTER (WHERE p.status = 'on-hold') AS on_hold,
    count(*) FILTER (WHERE p.status = 'completed') AS completed,
    count(*) FILTER (WHERE p.status = 'cancelled') AS cancelled
FROM projects.projects p
WHERE projects.visible(p.id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean);

-- name: ProjectStatsSummaryCounts :one
-- ProjectStatsSummaryCounts is the dashboard card's four figures, in the
-- shape customers' own summary counts them: a state now (active), the same
-- state at the period's start so the delta is a change rather than a total,
-- what was created inside [period_from, period_to), and what was created in
-- the window of the same length immediately before it.
SELECT
    count(*) FILTER (WHERE p.status = 'active') AS active,
    count(*) FILTER (WHERE p.status = 'active' AND p.created_at < @period_from::timestamptz) AS active_at_period_start,
    count(*) FILTER (WHERE p.created_at >= @period_from::timestamptz AND p.created_at < @period_to::timestamptz) AS new_projects,
    count(*) FILTER (WHERE p.created_at >= @previous_from::timestamptz AND p.created_at < @period_from::timestamptz) AS previous_new_projects
FROM projects.projects p
WHERE projects.visible(p.id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean);

-- name: ProjectCreationBuckets :many
-- ProjectCreationBuckets is the timeseries's newProjects metric: one row per
-- UTC calendar day with at least one project created in [range_from,
-- range_to). Days with nothing in them are absent rather than zero, which is
-- what every other module's buckets do and what the dashboard's sparkline
-- expects.
SELECT (p.created_at AT TIME ZONE 'UTC')::date AS day, count(*) AS value
FROM projects.projects p
WHERE projects.visible(p.id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean)
  AND p.created_at >= @range_from::timestamptz AND p.created_at < @range_to::timestamptz
GROUP BY day
ORDER BY day;

-- name: OverdueActiveProjects :many
-- OverdueActiveProjects is the dashboard's attention list: projects still
-- being worked on whose end date has passed. A completed project past the
-- same date finished late, which is history rather than something to act on,
-- and a project with no end date can never be overdue. Oldest first, so the
-- one that has been overdue longest is the one that surfaces when the
-- dashboard takes the top of the list.
SELECT p.id, p.name, p.end_date FROM projects.projects p
WHERE projects.visible(p.id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean)
  AND p.status = 'active'
  AND p.end_date IS NOT NULL
  AND p.end_date < @today::date
ORDER BY p.end_date, p.id;
