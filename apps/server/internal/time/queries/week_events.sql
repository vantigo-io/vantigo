-- name: UnsubmittedWeeks :many
-- UnsubmittedWeeks is the people whose week starting @monday has ended without
-- a submission and that were not announced for it: a person with draft entries in
-- the week, or one who submitted any of the eight weeks before it. hours is what
-- they registered in the week, of whatever status. Oldest first.
SELECT u.user_id,
       COALESCE((SELECT SUM(e.hours) FROM time.entries e
                 WHERE e.user_id = u.user_id AND e.entry_date BETWEEN sqlc.arg(monday)::date AND (sqlc.arg(monday)::date + 6)), 0)::numeric(9,2) AS hours
FROM (
    SELECT en.user_id FROM time.entries en
    WHERE en.entry_date BETWEEN sqlc.arg(monday)::date AND (sqlc.arg(monday)::date + 6) AND en.status = 'draft'
    UNION
    SELECT ws.user_id FROM time.week_submissions ws
    WHERE ws.week_start BETWEEN (sqlc.arg(monday)::date - 56) AND (sqlc.arg(monday)::date - 7)
) u
WHERE NOT EXISTS (SELECT 1 FROM time.week_submissions s WHERE s.user_id = u.user_id AND s.week_start = sqlc.arg(monday)::date)
  AND NOT EXISTS (SELECT 1 FROM time.week_announced a WHERE a.user_id = u.user_id AND a.week_start = sqlc.arg(monday)::date)
ORDER BY u.user_id
LIMIT sqlc.arg(n)::int;

-- name: MarkWeekAnnounced :execrows
INSERT INTO time.week_announced (user_id, week_start, announced_at)
VALUES (sqlc.arg(user_id), sqlc.arg(monday)::date, sqlc.arg(now)::timestamptz)
ON CONFLICT (user_id, week_start) DO NOTHING;
