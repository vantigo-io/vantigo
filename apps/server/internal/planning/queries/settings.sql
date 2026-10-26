-- The installation's settings (design S20, §3.1): one row, id 1.

-- name: GetSettings :one
-- GetSettings reads the installation's single settings row. The migration
-- writes it, so this always answers a row.
SELECT * FROM planning.settings WHERE id = 1;

-- name: LockSettings :one
-- LockSettings reads the settings row and holds it for the rest of the
-- transaction, so a change judges its If-Match revision against the row it
-- then writes, and two changes at once queue rather than both passing the
-- check.
SELECT * FROM planning.settings WHERE id = 1 FOR UPDATE;

-- name: UpdateSettings :one
-- UpdateSettings writes the six fields as the caller merged them over the row
-- it locked, and moves the revision on.
UPDATE planning.settings SET
    overbooking = @overbooking,
    default_load_unit = @default_load_unit,
    unbooked_threshold_percent = @unbooked_threshold_percent,
    unbooked_horizon_weeks = @unbooked_horizon_weeks,
    request_lead_days = @request_lead_days,
    ending_lead_weeks = @ending_lead_weeks,
    revision = revision + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = sqlc.narg(updated_by_user_id)::uuid
WHERE id = 1
RETURNING *;
