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
-- ResolveTimeZone asks Postgres what a zone name *means*, by doing the very
-- thing the claims list's filter will do with it. A business time zone is read
-- by Go (every date derived from a claim's instants) *and* by Postgres (that
-- same derivation in SQL), and the two carry their own tzdata: a name they read
-- differently would be stored here and then quietly disagree with itself.
--
-- Knowing the name is not enough, which is what this query used to ask. 'CET'
-- is an IANA zone with summer time to Go and a fixed +01:00 **abbreviation** to
-- Postgres, which resolves pg_timezone_abbrevs before pg_timezone_names — so
-- both halves "know" it and they mean two different things for half the year.
-- The answer here is therefore the offset Postgres puts the name at, at a
-- winter instant and a summer one, which the caller compares with Go's. Two
-- instants are enough because the disagreements are about summer time, and a
-- zone that agrees in both January and July agrees about which side of local
-- midnight an instant falls on all year.
--
-- Both sides of each subtraction are timestamps without zone, so the interval
-- is the offset itself; EXTRACT(EPOCH …) makes it the seconds Go's Zone()
-- answers in. A name Postgres does not know raises 22023 rather than answering
-- a row, which is exactly the signal the caller turns into the timeZone field
-- error. The view pg_timezone_names would list the names more directly, but
-- sqlc's catalog has no entry for it, and a query the generator cannot type is
-- not one this module writes — and it would not answer this question anyway,
-- since it lists 'CET' too.
WITH probe AS (
    SELECT @name::text AS zone, @winter::timestamptz AS winter, @summer::timestamptz AS summer
)
SELECT
    EXTRACT(EPOCH FROM ((winter AT TIME ZONE zone) - (winter AT TIME ZONE 'UTC')))::bigint AS winter_offset,
    EXTRACT(EPOCH FROM ((summer AT TIME ZONE zone) - (summer AT TIME ZONE 'UTC')))::bigint AS summer_offset
FROM probe;
