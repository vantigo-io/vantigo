-- name: GetSettings :one
-- GetSettings reads the installation's single settings row (design §3.5). The
-- migration writes it, so this always answers a row.
SELECT * FROM expenses.settings WHERE id = 1;

-- name: UpdateSettings :one
-- UpdateSettings replaces every setting at once; there is only ever the one
-- row, written by the migration, so this never inserts.
UPDATE expenses.settings SET
    locked_before = @locked_before,
    default_currency = @default_currency,
    default_markup_percent = @default_markup_percent,
    receipt_required_over = @receipt_required_over,
    updated_at = @now::timestamptz
WHERE id = 1
RETURNING *;
