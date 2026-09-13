-- Suppressions: getCommunicationsSuppressions, postCommunicationsSuppressions,
-- getCommunicationsSuppressionsById, deleteCommunicationsSuppressionsById
-- (communications inventory §1.5; EP/SuppressionEndpoints.cs).

-- name: ListSuppressions :many
-- ListSuppressions: a bare array, ordered by CreatedAt descending (inventory
-- §1.5, §19.2 item 19 -- deliberately the opposite direction from tags'
-- Name ascending).
SELECT id, normalized_email_address, reason, created_at
FROM communications.suppressions
ORDER BY created_at DESC;

-- name: GetSuppressionByNormalizedEmail :one
-- CreateSuppression's existing-row lookup (`SuppressionEndpoints.cs:24`):
-- read before insert, on the same normalised (uppercased, D7) form the
-- unique index enforces -- a suppression that already exists answers 200
-- with the pre-existing row, never 409.
SELECT id, normalized_email_address, reason, created_at
FROM communications.suppressions
WHERE normalized_email_address = @normalized_email_address;

-- name: InsertSuppression :one
-- CreateSuppression's insert for a genuinely new address -> 201.
INSERT INTO communications.suppressions (id, normalized_email_address, reason, created_at)
VALUES (@id, @normalized_email_address, @reason, @created_at)
RETURNING id, normalized_email_address, reason, created_at;

-- name: GetSuppressionByID :one
-- GetSuppression: id lookup -> 404 bare when absent.
SELECT id, normalized_email_address, reason, created_at
FROM communications.suppressions
WHERE id = @id;

-- name: DeleteSuppression :execrows
-- DeleteSuppression: the caller checks the affected-row count and answers
-- 404 bare when it is zero, else 204.
DELETE FROM communications.suppressions WHERE id = @id;
