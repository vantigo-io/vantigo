-- name: WeekHoursOf :many
-- WeekHoursOf is, for each of the given users with anything in the seven days
-- from week_start, the hours logged (every entry not rejected) and the hours
-- approved (invoiced included): contracts.Timesheets (timesheets.go). A user
-- with no entry in the window has no row.
SELECT user_id,
       COALESCE(SUM(hours) FILTER (WHERE status <> 'rejected'), 0)::numeric(9,2) AS logged,
       COALESCE(SUM(hours) FILTER (WHERE status IN ('approved', 'invoiced')), 0)::numeric(9,2) AS approved
FROM time.entries
WHERE user_id = ANY(@user_ids::uuid[])
  AND entry_date >= sqlc.arg(week_start)::date
  AND entry_date < sqlc.arg(week_start)::date + 7
GROUP BY user_id;
