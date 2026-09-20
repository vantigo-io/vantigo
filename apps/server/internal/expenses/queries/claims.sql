-- The travel claim (design §3.6): the container a trip's expenses sit in.
--
-- Two rules run through this file. **Visibility** is the entries' own rule
-- applied to the claim — the caller's own, everything on a project they manage,
-- and everything for see_all — and because a line's owner and project are
-- always its claim's, a line is visible exactly when its claim is.
-- **The lock order** is the claim's row first and then its lines in id order;
-- every query here that takes a lock is written to be called in that order (see
-- the comment on lockOrder in claims.go).

-- name: InsertClaim :one
-- InsertClaim records one travel claim as a draft with no lines. status and
-- revision take the column defaults ('draft', 1); created_by_user_id is
-- whoever made the request, which is not always user_id, the person the trip
-- concerns.
INSERT INTO expenses.claims (
    user_id, created_by_user_id, purpose, destination,
    abroad, abroad_day_rate, abroad_currency, departure_at, return_at, project_id,
    created_at, updated_at
) VALUES (
    @user_id, @created_by_user_id, @purpose, @destination,
    @abroad, @abroad_day_rate, @abroad_currency, @departure_at, @return_at, @project_id,
    @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: GetClaim :one
-- GetClaim fetches one claim by id. Who may see it is decided in Go
-- (authorize.go), never here: an outsider's 404 has to be indistinguishable
-- from an unknown id's, so the row is loaded first and discarded after.
SELECT * FROM expenses.claims WHERE id = @id;

-- name: GetClaims :many
-- GetClaims is one read for the claims a set of entries belong to, so a page of
-- lines resolves its units without a query per line.
SELECT * FROM expenses.claims WHERE id = ANY(@ids::bigint[]);

-- name: LockClaim :one
-- LockClaim reads one claim and holds its row until the transaction ends. It
-- is the **first** lock every write in this module takes: a write on the claim
-- itself, and a write on one of its lines, both start here, so two callers
-- working on the same trip queue rather than deadlock.
SELECT * FROM expenses.claims WHERE id = @id FOR UPDATE;

-- name: UpdateClaim :one
-- UpdateClaim replaces a claim's header. A save always leaves a draft: a
-- rejected claim returns to draft with its rejection and its decision cleared,
-- exactly as a rejected entry does. The revision, the status and who may write
-- are guarded again here, although the row is already locked, so that no
-- caller can ever write over a revision it did not read.
UPDATE expenses.claims SET
    purpose = @purpose,
    destination = @destination,
    abroad = @abroad,
    abroad_day_rate = @abroad_day_rate,
    abroad_currency = @abroad_currency,
    departure_at = @departure_at,
    return_at = @return_at,
    project_id = @project_id,
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

-- name: DeleteClaim :execrows
-- DeleteClaim removes a claim only while it is still its owner's to change.
-- Its lines go with it through fk_entries_claim_id, and their receipt rows
-- through the cascade attachments already carry; the objects those rows name
-- are removed after this has committed. The status and who may write are
-- re-checked here rather than trusted from the row the handler read.
DELETE FROM expenses.claims
WHERE id = @id
  AND status IN ('draft', 'rejected')
  AND (@any_owner::boolean OR user_id = @user_id::uuid);

-- name: CountClaims :one
-- CountClaims counts what ListClaims pages through, under exactly the same
-- predicate, so the total is the number of claims the caller may see and the
-- last page is never empty. from and to are judged on the departure day in
-- UTC, the same day the period lock is judged on.
SELECT count(*) FROM expenses.claims
WHERE (@see_all::boolean
       OR user_id = @caller_id::uuid
       OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR (departure_at AT TIME ZONE 'UTC')::date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR (departure_at AT TIME ZONE 'UTC')::date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(reimbursed)::boolean IS NULL OR (reimbursed_at IS NOT NULL) = sqlc.narg(reimbursed)::boolean);

-- name: ListClaims :many
-- ListClaims is one page of CountClaims' claims, the most recent trip first.
SELECT * FROM expenses.claims
WHERE (@see_all::boolean
       OR user_id = @caller_id::uuid
       OR (project_id IS NOT NULL AND project_id = ANY(@managed_project_ids::integer[])))
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR (departure_at AT TIME ZONE 'UTC')::date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR (departure_at AT TIME ZONE 'UTC')::date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(reimbursed)::boolean IS NULL OR (reimbursed_at IS NOT NULL) = sqlc.narg(reimbursed)::boolean)
ORDER BY departure_at DESC, id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: ClaimTotals :many
-- ClaimTotals is each claim's figures per currency, for a whole page of them
-- at once: what its lines come to, and what of that its owner is owed back.
-- The "owes the employee" predicate is owedToEmployee (responses.go) in SQL,
-- the same one the reimbursement track filters on.
SELECT
    claim_id,
    currency,
    count(*)::bigint AS line_count,
    SUM(gross_amount)::numeric(14,2) AS gross,
    SUM(CASE
        WHEN gross_amount > 0 AND NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))
        THEN gross_amount ELSE 0
    END)::numeric(14,2) AS owed_to_employee,
    SUM(CASE WHEN billable THEN COALESCE(bill_amount, 0) ELSE 0 END)::numeric(14,2) AS bill_amount
FROM expenses.entries
WHERE claim_id = ANY(@claim_ids::bigint[])
GROUP BY claim_id, currency
ORDER BY claim_id, currency;

-- name: ListClaimLines :many
-- ListClaimLines is one claim's expenses in the order the claim renders them:
-- the day they happened, and then as they were recorded.
SELECT * FROM expenses.entries WHERE claim_id = @claim_id ORDER BY entry_date, id;

-- name: LockClaimLines :many
-- LockClaimLines holds every one of a claim's lines, in id order — the second
-- half of the module's lock order, taken only after LockClaim. It is what a
-- project re-point writes through, and what a line count decided under the
-- claim's lock reads.
SELECT * FROM expenses.entries WHERE claim_id = @claim_id ORDER BY id FOR UPDATE;

-- name: CountClaimLines :one
-- CountClaimLines is how many expenses a claim already holds, read under the
-- claim's own lock so two lines added at once cannot both slip past the cap.
SELECT count(*) FROM expenses.entries WHERE claim_id = @claim_id;

-- name: CountClaimPerDiemOnDate :one
-- CountClaimPerDiemOnDate is how many per diem days a claim already holds for
-- one date, read under the claim's own row lock so two days of the same date
-- added at once cannot both slip through. exclude_id is the line being
-- replaced, 0 on a create — a save that keeps a per diem day where it is must
-- not find itself.
SELECT count(*) FROM expenses.entries
WHERE claim_id = @claim_id
  AND kind = 'per_diem'
  AND entry_date = @entry_date
  AND id <> @exclude_id::bigint;

-- name: SetClaimLineProject :exec
-- SetClaimLineProject re-points one of a claim's lines onto the claim's
-- project, in the transaction that changed it. The billing columns come with
-- it because they belong to the project the line is on: a billing line that is
-- not one of the new project's is cleared, and a claim with no project at all
-- bills nothing, so every figure goes with the flag.
--
-- The revision moves, so a client holding the line at its old revision is told
-- to read it again rather than writing over a project it never saw.
UPDATE expenses.entries SET
    project_id = @project_id,
    billing_line_id = @billing_line_id,
    billable = @billable,
    markup_percent = @markup_percent,
    bill_rate_per_km = @bill_rate_per_km,
    bill_amount = @bill_amount,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: ListAttachmentKeysForClaim :many
-- ListAttachmentKeysForClaim is the object keys of every receipt on every one
-- of a claim's lines, read under the claim's lock before it is deleted: the
-- rows go with it through the two cascades, and the objects they name are
-- removed once that delete has committed.
SELECT a.object_key
FROM expenses.attachments a
JOIN expenses.entries e ON e.id = a.entry_id
WHERE e.claim_id = @claim_id
ORDER BY a.id;
