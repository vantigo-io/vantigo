-- name: ApproveEntries :many
-- ApproveEntries moves submitted entries to approved (D2), recording who
-- approved them and when. The rows are already locked by the caller, which
-- decided every one of them may be approved; the status guard is the last
-- line, not the rule.
UPDATE time.entries SET
    status = 'approved',
    approved_by_user_id = @approver_id::uuid,
    approved_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'submitted'
RETURNING *;

-- name: RejectEntries :many
-- RejectEntries moves submitted entries to rejected with the reason their
-- owner sees; an edit then returns them to draft (UpdateEntry). The
-- submission stamp stays: the entry was submitted.
UPDATE time.entries SET
    status = 'rejected',
    rejection_reason = @reason::text,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'submitted'
RETURNING *;

-- name: UnapproveEntries :many
-- UnapproveEntries returns approved entries to draft, clearing the approval
-- and the submission stamp alike, so the owner's week shows each as a fresh
-- draft that has to be submitted again. Never an invoiced entry.
UPDATE time.entries SET
    status = 'draft',
    approved_by_user_id = NULL,
    approved_at = NULL,
    submitted_at = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status = 'approved'
RETURNING *;

-- name: ListApprovalGroups :many
-- ListApprovalGroups is the approval queue's groups: the submitted entries
-- the caller may approve — on every project for see_all (time:approve), on
-- the projects they manage otherwise, and not dated before locked_before
-- (NULL for no lock, or for time:manage) — summed per person and week (the
-- Monday, ISO day of week 1). The groups are ordered by the caller, who
-- names the people.
SELECT user_id,
       (entry_date - (EXTRACT(ISODOW FROM entry_date)::integer - 1))::date AS week_start,
       SUM(hours)::numeric(9,2) AS hours
FROM time.entries
WHERE status = 'submitted'
  AND (@see_all::boolean OR project_id = ANY(@managed_project_ids::integer[]))
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
GROUP BY user_id, week_start;

-- name: ListApprovalEntries :many
-- ListApprovalEntries is the entries of the groups on one page of the queue,
-- each named by its key '<user id>/<week start YYYY-MM-DD>', under exactly
-- ListApprovalGroups' predicate, in the order a group lists them: by day,
-- then by start time (entries without one last), then as created.
SELECT * FROM time.entries
WHERE status = 'submitted'
  AND (@see_all::boolean OR project_id = ANY(@managed_project_ids::integer[]))
  AND (sqlc.narg(locked_before)::date IS NULL OR entry_date >= sqlc.narg(locked_before)::date)
  AND user_id::text || '/' || to_char(entry_date - (EXTRACT(ISODOW FROM entry_date)::integer - 1), 'YYYY-MM-DD')
      = ANY(@group_keys::text[])
ORDER BY entry_date, start_time NULLS LAST, id;
