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
    time_zone = @time_zone,
    updated_at = @now::timestamptz
WHERE id = 1
RETURNING *;

-- name: ResolveTimeZone :one
-- ResolveTimeZone asks Postgres whether it knows a zone name, by doing the very
-- thing the claims list's filter will do with it. A business time zone is read
-- by Go (every date derived from a claim's instants) *and* by Postgres (that
-- same derivation in SQL), and the two carry their own tzdata: a name only one
-- of them knows would be stored here and then quietly disagree with itself.
--
-- A name Postgres does not know raises 22023 rather than answering false, which
-- is exactly the signal the caller turns into the timeZone field error. The
-- view pg_timezone_names would answer more directly, but sqlc's catalog has no
-- entry for it, and a query the generator cannot type is not one this module
-- writes.
SELECT (now() AT TIME ZONE @name::text) IS NOT NULL;
