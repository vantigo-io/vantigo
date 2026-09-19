-- name: ListRates :many
-- ListRates is every dated rate, by kind and then the latest first. The
-- client groups them per kind, so one flat list is all the settings screen
-- needs.
SELECT * FROM expenses.rates ORDER BY kind, valid_from DESC;

-- name: EffectiveRate :one
-- EffectiveRate is the rate of one kind in force on a day (design §3.4): the
-- latest row whose valid_from is on or before it. A kind with no such row
-- answers no rows, which rateFor turns into the typed "no rate" result.
SELECT * FROM expenses.rates
WHERE kind = @kind AND valid_from <= @on_date
ORDER BY valid_from DESC
LIMIT 1;

-- name: GetRate :one
SELECT * FROM expenses.rates WHERE id = @id;

-- name: InsertRate :one
-- InsertRate adds a rate row. A second row for the same kind and day raises
-- 23505 on ux_rates_kind_valid_from, which the handler turns into the
-- validFrom field error.
INSERT INTO expenses.rates (kind, valid_from, value, currency, source, created_at, updated_at)
VALUES (@kind, @valid_from, @value, @currency, @source, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: RestoreSeedRate :execrows
-- RestoreSeedRate writes one shipped row back as it shipped — the reset
-- operation's whole write. A day the product ships is the product's: removed,
-- it is created again; edited, it returns to its shipped value, currency and
-- source label, keeping the row's id so anything that quotes it still finds
-- it. Only the (kind, valid_from) pairs named by the seed table are touched,
-- so the company's own rows, on their own days, are never in the conflict
-- target and never change.
INSERT INTO expenses.rates (kind, valid_from, value, currency, source, created_at, updated_at)
VALUES (@kind, @valid_from, @value, @currency, @source, @now::timestamptz, @now::timestamptz)
ON CONFLICT (kind, valid_from) DO UPDATE SET
    value = EXCLUDED.value,
    currency = EXCLUDED.currency,
    source = EXCLUDED.source,
    updated_at = EXCLUDED.updated_at;

-- name: UpdateRate :one
-- UpdateRate replaces a row's day, value, currency and source; the kind stays
-- the row's own. No row is an unknown id.
UPDATE expenses.rates SET
    valid_from = @valid_from,
    value = @value,
    currency = @currency,
    source = @source,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteRate :execrows
-- DeleteRate removes a row. Expenses it priced keep their snapshots.
DELETE FROM expenses.rates WHERE id = @id;
