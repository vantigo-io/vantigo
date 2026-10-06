-- The collection rates (invoices payments and reminders design D6): dated
-- rows, each in force from valid_from until the next row of its kind. A row
-- is used once a letter with facts — printed or sent — is dated on or after
-- its valid_from and before the next row of its kind (plan reading 6); such a
-- row is not usable, and is never deleted.

-- name: ListCollectionRates :many
-- ListCollectionRates is every row by kind and date, each with whether it is
-- the row in force on @today and whether a letter used it.
SELECT r.*,
    (r.valid_from <= sqlc.arg(today)::date
        AND NOT EXISTS (SELECT 1 FROM invoices.collection_rates n
            WHERE n.kind = r.kind AND n.valid_from > r.valid_from AND n.valid_from <= sqlc.arg(today)::date))::boolean AS in_force,
    EXISTS (SELECT 1 FROM invoices.reminders l
        WHERE l.status IN ('printed', 'sent') AND l.sent_on >= r.valid_from
          AND NOT EXISTS (SELECT 1 FROM invoices.collection_rates n
              WHERE n.kind = r.kind AND n.valid_from > r.valid_from AND n.valid_from <= l.sent_on))::boolean AS used
FROM invoices.collection_rates r
ORDER BY r.kind, r.valid_from;

-- name: GetCollectionRate :one
-- GetCollectionRate is one row with whether a letter used it, as
-- ListCollectionRates judges it: the DELETE's reading.
SELECT r.*,
    EXISTS (SELECT 1 FROM invoices.reminders l
        WHERE l.status IN ('printed', 'sent') AND l.sent_on >= r.valid_from
          AND NOT EXISTS (SELECT 1 FROM invoices.collection_rates n
              WHERE n.kind = r.kind AND n.valid_from > r.valid_from AND n.valid_from <= l.sent_on))::boolean AS used
FROM invoices.collection_rates r
WHERE r.id = @id;

-- name: InsertCollectionRate :one
-- InsertCollectionRate adds a user's row ahead of a release; a second row of
-- the kind on the same day is uq_collection_rates_kind_valid_from.
INSERT INTO invoices.collection_rates (kind, valid_from, value, source_ref, created_by_user_id, created_at)
VALUES (@kind, @valid_from, @value, @source_ref, sqlc.arg(created_by_user_id)::uuid, @now)
RETURNING *;

-- name: DeleteCollectionRate :execrows
-- DeleteCollectionRate deletes one row; the append-only trigger refuses a
-- seeded one.
DELETE FROM invoices.collection_rates WHERE id = @id;

-- name: ReseedCollectionRate :one
-- ReseedCollectionRate puts the release's value back as a seeded row, in the
-- transaction that deleted the user's row it had been recorded over (D6,
-- m6), so the half-year never goes empty.
INSERT INTO invoices.collection_rates (kind, valid_from, value, source_ref, created_by_user_id, created_at)
VALUES (@kind, @valid_from, @value, @source_ref, NULL, @now)
RETURNING *;

-- name: LatestLetterDay :one
-- LatestLetterDay is the sent_on of the latest letter with facts — printed
-- or sent — or NULL: a new rate takes effect after it (D6, the Task 6
-- review's M2), so it never contradicts a letter already printed or posted.
SELECT max(sent_on)::date AS day FROM invoices.reminders WHERE status IN ('printed', 'sent');

-- name: RatesFor :many
-- RatesFor is every row of the three kinds, for the reminder engine
-- (reminderrules.Rate, by way of ratesOf).
SELECT * FROM invoices.collection_rates ORDER BY kind, valid_from;
