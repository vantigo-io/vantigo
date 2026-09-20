-- name: InsertEntry :one
-- InsertEntry records one expense as a draft, with every amount already
-- computed in Go from exact decimals (money.go) — the columns' own scale
-- never rounds anything a second time. created_at and updated_at are the same
-- instant, supplied by the caller from Deps.Clock(); status and revision take
-- the column defaults ('draft', 1). created_by_user_id is whoever made the
-- request, which is not always user_id, the person the expense concerns.
INSERT INTO expenses.entries (
    user_id, created_by_user_id, kind, entry_date, description,
    category_id, supplier, paid_by, currency, gross_amount, vat_amount,
    distance_km, from_place, to_place, passengers, rate, passenger_rate,
    project_id, billing_line_id, billable, markup_percent, bill_rate_per_km, bill_amount,
    created_at, updated_at
) VALUES (
    @user_id, @created_by_user_id, @kind, @entry_date, @description,
    @category_id, @supplier, @paid_by, @currency, @gross_amount, @vat_amount,
    @distance_km, @from_place, @to_place, @passengers, @rate, @passenger_rate,
    @project_id, @billing_line_id, @billable, @markup_percent, @bill_rate_per_km, @bill_amount,
    @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: GetEntry :one
-- GetEntry fetches one expense by id. Who may see it is decided in Go
-- (authorize.go), never here: an outsider's 404 has to be indistinguishable
-- from an unknown id's, so the row is loaded first and discarded after.
SELECT * FROM expenses.entries WHERE id = @id;

-- name: LockEntry :one
-- LockEntry reads one expense and holds its row until the transaction ends.
-- An update decides everything it refuses on — the status, the revision —
-- from this row, not from the one the handler read before the transaction, so
-- a save that committed in between is seen and wins.
SELECT * FROM expenses.entries WHERE id = @id FOR UPDATE;

-- name: UpdateEntry :one
-- UpdateEntry replaces an expense's content, its amounts computed again (a
-- draft follows the rate table until it is submitted). A save always leaves a
-- draft: a rejected line returns to draft with its rejection and its decision
-- cleared. The revision, the status and who may write are guarded again here,
-- although the row is already locked, so that no caller can ever write over a
-- revision it did not read.
UPDATE expenses.entries SET
    kind = @kind,
    entry_date = @entry_date,
    description = @description,
    category_id = @category_id,
    supplier = @supplier,
    paid_by = @paid_by,
    currency = @currency,
    gross_amount = @gross_amount,
    vat_amount = @vat_amount,
    distance_km = @distance_km,
    from_place = @from_place,
    to_place = @to_place,
    passengers = @passengers,
    rate = @rate,
    passenger_rate = @passenger_rate,
    rate_overridden_by_user_id = NULL,
    rate_table_value = NULL,
    project_id = @project_id,
    billing_line_id = @billing_line_id,
    billable = @billable,
    markup_percent = @markup_percent,
    bill_rate_per_km = @bill_rate_per_km,
    bill_amount = @bill_amount,
    status = 'draft',
    rejection_reason = NULL,
    submitted_at = NULL,
    decided_at = NULL,
    decided_by_user_id = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
  AND revision = @revision
  AND status IN ('draft', 'rejected')
  AND (@any_owner::boolean OR user_id = @user_id::uuid)
RETURNING *;

-- name: DeleteEntry :execrows
-- DeleteEntry removes an expense only while it is still its owner's to change.
-- The status and who may write are re-checked here rather than trusted from
-- the row the handler read, so a submit that commits in between wins and the
-- delete removes nothing. any_owner is expenses:manage, which deletes anyone's
-- draft.
DELETE FROM expenses.entries
WHERE id = @id
  AND status IN ('draft', 'rejected')
  AND (@any_owner::boolean OR user_id = @user_id::uuid);

-- name: CountEntries :one
-- CountEntries counts what ListEntries pages through, under exactly the same
-- predicate, so the total is the number of expenses the caller may see and the
-- last page is never empty. Visibility is a predicate of its own — the rule
-- authorize.go applies to one entry: everything for see_all (expenses:view-all,
-- expenses:approve, expenses:manage), the caller's own, and everything on the
-- projects the caller manages, whose ids are resolved through the project
-- directory before the query rather than filtered after it.
SELECT count(*) FROM expenses.entries
WHERE (@see_all::boolean
       OR user_id = @caller_id::uuid
       OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(project_id)::integer IS NULL OR project_id = sqlc.narg(project_id)::integer)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR entry_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR entry_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(reimbursed)::boolean IS NULL OR (reimbursed_at IS NOT NULL) = sqlc.narg(reimbursed)::boolean);

-- name: ListEntries :many
-- ListEntries is one page of CountEntries' expenses, the latest day first and,
-- within a day, the latest recorded first.
SELECT * FROM expenses.entries
WHERE (@see_all::boolean
       OR user_id = @caller_id::uuid
       OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(project_id)::integer IS NULL OR project_id = sqlc.narg(project_id)::integer)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR entry_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR entry_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(reimbursed)::boolean IS NULL OR (reimbursed_at IS NOT NULL) = sqlc.narg(reimbursed)::boolean)
ORDER BY entry_date DESC, id DESC
LIMIT @page_size OFFSET @page_offset;
