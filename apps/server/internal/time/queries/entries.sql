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

-- name: LockEntry :one
-- LockEntry reads one entry and holds its row until the transaction ends. An
-- update decides everything it refuses on (the status, the revision) from
-- this row, not from the one the handler read before the transaction, so a
-- submit that committed in between is seen and wins.
SELECT * FROM time.entries WHERE id = @id FOR UPDATE;

-- name: UpdateEntry :one
-- UpdateEntry replaces an entry's content with its rates resolved again (D3).
-- A save always leaves a draft: a rejected entry returns to draft with its
-- rejection reason and its submission stamp cleared (design 4.2). The revision and
-- the status are guarded again here, although the row is already locked, so
-- that no caller can ever write over a revision it did not read.
UPDATE time.entries SET
    project_id = @project_id,
    billing_line_id = @billing_line_id,
    task_id = @task_id,
    task_title = @task_title,
    entry_date = @entry_date,
    hours = @hours,
    start_time = @start_time,
    end_time = @end_time,
    note = @note,
    billable = @billable,
    bill_rate = @bill_rate,
    bill_currency = @bill_currency,
    cost_rate = @cost_rate,
    cost_currency = @cost_currency,
    rate_source = @rate_source,
    status = 'draft',
    rejection_reason = NULL,
    submitted_at = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND revision = @revision AND status IN ('draft', 'rejected')
RETURNING *;

-- name: CountEntries :one
-- CountEntries counts what ListEntries pages through, under exactly the same
-- predicate, so the total is the number of entries the caller may see and
-- the last page is never empty. Visibility is a predicate of its own (the
-- rule authorize.go's entryAccess applies to one entry): everything for
-- see_all, the caller's own, and the entries on the projects the caller
-- manages. The filters are optional — user_id NULL is everyone's, which the
-- handler only allows with a project filter; week_start and week_end are a
-- Monday and its Sunday.
SELECT count(*) FROM time.entries
WHERE (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (@see_all::boolean OR user_id = @caller_id::uuid OR project_id = ANY(@managed_project_ids::integer[]))
  AND (sqlc.narg(week_start)::date IS NULL
       OR entry_date BETWEEN sqlc.narg(week_start)::date AND sqlc.narg(week_end)::date)
  AND (sqlc.narg(project_id)::integer IS NULL OR project_id = sqlc.narg(project_id)::integer)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text);

-- name: ListEntries :many
-- ListEntries is one page of CountEntries' entries, the latest day first and,
-- within a day, the latest created first.
SELECT * FROM time.entries
WHERE (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (@see_all::boolean OR user_id = @caller_id::uuid OR project_id = ANY(@managed_project_ids::integer[]))
  AND (sqlc.narg(week_start)::date IS NULL
       OR entry_date BETWEEN sqlc.narg(week_start)::date AND sqlc.narg(week_end)::date)
  AND (sqlc.narg(project_id)::integer IS NULL OR project_id = sqlc.narg(project_id)::integer)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY entry_date DESC, id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: LockEntries :many
-- LockEntries reads the entries in ids and holds their rows until the
-- transaction ends, in id order, so two batches over overlapping entries
-- take their locks in the same order and cannot deadlock. An id with no
-- entry is simply absent from the result.
SELECT * FROM time.entries
WHERE id = ANY(@ids::bigint[])
ORDER BY id
FOR UPDATE;

-- name: SubmitEntries :many
-- SubmitEntries moves drafts to submitted (D2), freezing their rates (D3).
-- The rows are already locked by the caller, which decided every one of them
-- may be submitted; the status guard is the last line, not the rule.
UPDATE time.entries SET
    status = 'submitted',
    submitted_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'draft'
RETURNING *;

-- name: GetEntries :many
-- GetEntries reads the entries in ids without locking them: what a batch
-- reads before its transaction, to learn which projects it will need the
-- caller's role on. An id with no entry is simply absent from the result.
SELECT * FROM time.entries WHERE id = ANY(@ids::bigint[]);
