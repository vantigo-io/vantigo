-- The dashboard's reads (design S20): what GET /stats and GET
-- /stats/attention judge, each read inside one snapshot with the employments,
-- the terms and the manager graph, and judged in Go on the caller's today.

-- name: StatsPeople :many
-- StatsPeople is everyone, with what the figures and the reassessment read:
-- the name (class C, which survives the erase), and the erase and its
-- reassessment.
SELECT id, display_name, erased_at, reassessed_at FROM staff.people ORDER BY id;

-- name: SignedContractEmployments :many
-- SignedContractEmployments is the employments a signed document of the
-- contract category is filed for — a document with no employment attributes
-- its signature to none.
SELECT DISTINCT d.employment_id::bigint AS employment_id
FROM staff.documents d
JOIN staff.document_categories c ON c.id = d.category_id
WHERE c.key = 'contract' AND d.signed_at IS NOT NULL AND d.employment_id IS NOT NULL;

-- name: FailingDeletions :one
-- FailingDeletions is the ledger rows the store has refused at least once
-- that were queued before @before — the attention item's rows — and when the
-- oldest of them was queued (@before when there is none).
SELECT
    count(*)::bigint AS failing,
    coalesce(min(created_at), @before::timestamptz)::timestamptz AS oldest
FROM staff.object_deletions
WHERE attempts >= 1 AND created_at < @before::timestamptz;
