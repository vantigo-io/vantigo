-- The reminder settings (invoices payments and reminders design D6, D7): one
-- row, id 1.

-- name: GetReminderSettings :one
SELECT * FROM invoices.reminder_settings WHERE id = 1;

-- name: UpdateReminderSettings :one
-- UpdateReminderSettings replaces every field when @revision is the row's,
-- and answers no row otherwise — a stale revision. Moving
-- regime_reviewed_through, or changing inkassolov_2026_from, records who and
-- when (D6): the right-hand sides read the row as it was.
UPDATE invoices.reminder_settings SET
    enabled = @enabled,
    first_reminder_days = @first_reminder_days,
    deadline_days = @deadline_days,
    grace_days = @grace_days,
    reminders_before_notice = @reminders_before_notice,
    collection_notice = @collection_notice,
    person_charge = @person_charge,
    business_charge = @business_charge,
    late_interest = @late_interest,
    stale_import_days = @stale_import_days,
    inkassolov_2026_from = sqlc.narg(inkassolov_2026_from)::date,
    regime_reviewed_through = sqlc.arg(regime_reviewed_through)::date,
    regime_reviewed_by_user_id = CASE
        WHEN regime_reviewed_through IS DISTINCT FROM sqlc.arg(regime_reviewed_through)::date
          OR inkassolov_2026_from IS DISTINCT FROM sqlc.narg(inkassolov_2026_from)::date
        THEN sqlc.arg(by)::uuid ELSE regime_reviewed_by_user_id END,
    regime_reviewed_at = CASE
        WHEN regime_reviewed_through IS DISTINCT FROM sqlc.arg(regime_reviewed_through)::date
          OR inkassolov_2026_from IS DISTINCT FROM sqlc.narg(inkassolov_2026_from)::date
        THEN sqlc.arg(now)::timestamptz ELSE regime_reviewed_at END,
    revision = revision + 1,
    updated_at = sqlc.arg(now)::timestamptz,
    updated_by_user_id = sqlc.arg(by)::uuid
WHERE id = 1 AND revision = @revision
RETURNING *;
