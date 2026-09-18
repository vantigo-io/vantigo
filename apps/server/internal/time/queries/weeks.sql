-- name: ListWeekEntries :many
-- ListWeekEntries is one person's entries in the week from the Monday
-- week_start to the Sunday week_end, whatever their status, in the order the timesheet shows them:
-- by day, then by start time (entries without one last), then as created.
SELECT * FROM time.entries
WHERE user_id = @user_id
  AND entry_date BETWEEN @week_start::date AND @week_end::date
ORDER BY entry_date, start_time NULLS LAST, id;

-- name: LockWeekDrafts :many
-- LockWeekDrafts is one person's draft entries in the week from the Monday
-- week_start to the Sunday week_end, their rows held until the transaction ends, in id order.
-- A row an edit or a single submit is holding is waited for and then read
-- again, and one that is no longer a draft, or no longer in the week, drops out.
SELECT * FROM time.entries
WHERE user_id = @user_id
  AND entry_date BETWEEN @week_start::date AND @week_end::date
  AND status = 'draft'
ORDER BY id
FOR UPDATE;

-- name: GetWeekSubmission :one
-- GetWeekSubmission is when a person last submitted a week as a whole; a week
-- never submitted answers no rows.
SELECT submitted_at FROM time.week_submissions
WHERE user_id = @user_id AND week_start = @week_start;

-- name: UpsertWeekSubmission :exec
-- UpsertWeekSubmission records a week as submitted now, moving the stamp of a
-- week submitted before.
INSERT INTO time.week_submissions (user_id, week_start, submitted_at)
VALUES (@user_id, @week_start, @now::timestamptz)
ON CONFLICT (user_id, week_start) DO UPDATE SET submitted_at = EXCLUDED.submitted_at;
