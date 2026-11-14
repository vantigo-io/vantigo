-- name: JournalFacts :many
-- JournalFacts is the expense journal's facts after a seq, in seq order
-- (contracts.ExpenseJournal, accounting design A20, R2; migration 00184).
-- lines is the jsonb the fact's trigger snapshotted, read as text and
-- decoded in Go.
SELECT seq, occurrence, subject, subject_id, user_id, business_date,
    coalesce(undoes_seq, 0)::bigint AS undoes_seq,
    coalesce(reimbursement_reference, '')::text AS reimbursement_reference,
    lines::text AS lines
FROM expenses.journal_facts
WHERE seq > @after_seq::bigint
ORDER BY seq
LIMIT @row_limit::int;

-- name: JournalCategoryNames :many
-- JournalCategoryNames is the current name of each category a page of facts
-- names.
SELECT id, name FROM expenses.categories WHERE id = ANY(@ids::int[]);
