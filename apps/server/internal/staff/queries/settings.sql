-- name: GetSettings :one
-- GetSettings reads the installation's single settings row (design §3.6). The
-- migration writes it, so this always answers a row.
SELECT * FROM staff.settings WHERE id = 1;

-- name: LockSettings :one
-- LockSettings reads the settings row and holds it for the rest of the
-- transaction, so a replace judges its If-Match revision against the row it
-- then writes, and two replaces at once queue rather than both passing the
-- check.
SELECT * FROM staff.settings WHERE id = 1 FOR UPDATE;

-- name: UpdateSettings :one
-- UpdateSettings replaces the settings the contract exposes and moves the
-- revision on; a retention period left out (NULL) keeps its value. The
-- columns later deliveries expose are left as they are.
UPDATE staff.settings SET
    time_zone = @time_zone,
    employee_number_prefix = @employee_number_prefix,
    next_employee_number = @next_employee_number,
    pension_provider = @pension_provider,
    injury_insurer = @injury_insurer,
    retention_grace_days = coalesce(sqlc.narg(retention_grace_days)::smallint, retention_grace_days),
    health_retention_days = coalesce(sqlc.narg(health_retention_days)::smallint, health_retention_days),
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = 1
RETURNING *;

-- name: ResolveTimeZone :one
-- ResolveTimeZone asks Postgres what a zone name means, at a winter instant
-- and a summer one, for the caller to compare with Go's reading of the same
-- name: the two carry their own tzdata, and Postgres resolves
-- pg_timezone_abbrevs before pg_timezone_names, so 'CET' is a fixed +01:00 to
-- it and a zone with summer time to Go. It is Expenses' own question
-- (internal/expenses/queries/settings.sql), asked again here because no module
-- reads another's. A name Postgres does not know raises 22023.
WITH probe AS (
    SELECT @name::text AS zone, @winter::timestamptz AS winter, @summer::timestamptz AS summer
)
SELECT
    EXTRACT(EPOCH FROM ((winter AT TIME ZONE zone) - (winter AT TIME ZONE 'UTC')))::bigint AS winter_offset,
    EXTRACT(EPOCH FROM ((summer AT TIME ZONE zone) - (summer AT TIME ZONE 'UTC')))::bigint AS summer_offset
FROM probe;

-- name: SetThresholdsAcknowledged :exec
-- SetThresholdsAcknowledged replaces when each threshold was acknowledged
-- (design S20). The caller holds the row's lock (LockSettings) and adds one
-- key to what it read. The revision is left alone: it guards the replace of
-- the editable settings, which this is not among.
UPDATE staff.settings SET
    thresholds_acknowledged = @thresholds_acknowledged::jsonb,
    updated_at = @now::timestamptz
WHERE id = 1;
