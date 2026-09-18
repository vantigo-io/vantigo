-- name: PeopleWeekTotals :many
-- PeopleWeekTotals is every person's hours per week (the Monday) between the
-- Mondays window_start and the Sunday window_end: all of them whatever the
-- status, the approved ones (invoiced included — they were approved before
-- they were invoiced), and how many entries stand rejected.
SELECT user_id,
       (entry_date - (EXTRACT(ISODOW FROM entry_date)::integer - 1))::date AS week_start,
       SUM(hours)::numeric(9,2) AS hours,
       COALESCE(SUM(hours) FILTER (WHERE status IN ('approved', 'invoiced')), 0)::numeric(9,2) AS approved_hours,
       COUNT(*) FILTER (WHERE status = 'rejected')::integer AS rejected_count
FROM time.entries
WHERE entry_date BETWEEN @window_start::date AND @window_end::date
GROUP BY user_id, week_start;

-- name: PeopleWeekSubmissions :many
-- PeopleWeekSubmissions is every week submission whose Monday falls in the
-- window.
SELECT user_id, week_start, submitted_at
FROM time.week_submissions
WHERE week_start BETWEEN @window_start::date AND @window_end::date;
