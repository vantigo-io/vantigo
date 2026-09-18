-- name: InsertEntry :one
-- InsertEntry creates a draft entry with its rates already resolved and
-- snapshotted (D3). created_at and updated_at are the same instant, supplied
-- by the caller from Deps.Clock(); status and revision take the column
-- defaults ('draft', 1).
INSERT INTO time.entries (
    user_id, project_id, billing_line_id, task_id, task_title, entry_date,
    hours, start_time, end_time, note, billable,
    bill_rate, bill_currency, cost_rate, cost_currency, rate_source,
    created_at, updated_at
) VALUES (
    @user_id, @project_id, @billing_line_id, @task_id, @task_title, @entry_date,
    @hours, @start_time, @end_time, @note, @billable,
    @bill_rate, @bill_currency, @cost_rate, @cost_currency, @rate_source,
    @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: GetEntry :one
-- GetEntry fetches one entry by id. Who may see it is decided in Go
-- (authorize.go), never here: a stranger's 404 has to be indistinguishable
-- from an unknown id's, so the row is loaded first and discarded after.
SELECT * FROM time.entries WHERE id = @id;

-- name: DeleteEntry :execrows
-- DeleteEntry removes an entry only while it is still the owner's to change
-- (D10). The status is re-checked here rather than trusted from the row the
-- handler read, so a submit that commits in between wins and the delete
-- removes nothing.
DELETE FROM time.entries WHERE id = @id AND status IN ('draft', 'rejected');

-- name: AcquireDayLock :exec
-- AcquireDayLock is the serialisation point of every write that decides a
-- person's day total (the 24-hour cap): a row lock cannot cover a create,
-- because what two racing creates contend for is a row that does not exist
-- yet. The lock is keyed on the person and the day, hashed into the second
-- argument, and held for the rest of the transaction. A hash collision only
-- serialises two unrelated days; it never lets two writes to one day pass.
-- It is a two-argument advisory lock, a lock space of its own that can never
-- collide with the single-argument ones.
SELECT pg_advisory_xact_lock(@lock_class::integer, hashtext(@day_key::text));

-- name: SumDayHours :one
-- SumDayHours is the hours a person has logged on one day, leaving out the
-- entry being saved (exclude_id, NULL on a create), so the cap is checked
-- against what the day will hold after the save.
SELECT COALESCE(SUM(hours), 0)::numeric(7,2) AS total
FROM time.entries
WHERE user_id = @user_id
  AND entry_date = @entry_date
  AND (sqlc.narg(exclude_id)::bigint IS NULL OR id <> sqlc.narg(exclude_id)::bigint);
