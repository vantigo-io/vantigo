-- name: CalendarEntries :many
-- CalendarEntries is this module's calendar source (calendar design D1): one
-- person's own entries dated in the window, whatever their status, in the
-- timesheet's order — ListWeekEntries' predicate and order over any range,
-- with only the columns a calendar item is drawn from.
SELECT id, project_id, entry_date, start_time, end_time, hours, task_title, note, status
FROM time.entries
WHERE user_id = @user_id
  AND entry_date BETWEEN @from_day::date AND @to_day::date
ORDER BY entry_date, start_time NULLS LAST, id;
