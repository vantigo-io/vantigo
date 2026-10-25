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
-- self-certification policy and the carry-over (delivery B) are written as
-- the caller resolved them from the row it locked. The account settings
-- (delivery C) keep their values when left out: create_before_days when
-- NULL, delete_external_after_days unless @set_delete_external_after_days —
-- its NULL is a value, "never". The two erase settings (Task 5b) keep their
-- values when NULL, and so do the default account kind and the directory
-- profile's switches (00060), which the caller merges over the row's. The
-- competence review interval (00067) keeps its value when NULL.
UPDATE staff.settings SET
    employee_number_prefix = @employee_number_prefix,
    next_employee_number = @next_employee_number,
    pension_provider = @pension_provider,
    injury_insurer = @injury_insurer,
    retention_grace_days = coalesce(sqlc.narg(retention_grace_days)::smallint, retention_grace_days),
    health_retention_days = coalesce(sqlc.narg(health_retention_days)::smallint, health_retention_days),
    self_cert_policy = @self_cert_policy,
    self_cert_days_per_occurrence = @self_cert_days_per_occurrence,
    self_cert_occurrences_per_year = @self_cert_occurrences_per_year,
    self_cert_days_per_year = @self_cert_days_per_year,
    self_cert_after_months = @self_cert_after_months,
    auto_carry_over = @auto_carry_over,
    auto_carry_over_since = @auto_carry_over_since,
    create_before_days = coalesce(sqlc.narg(create_before_days)::smallint, create_before_days),
    delete_external_after_days = CASE WHEN @set_delete_external_after_days::boolean
        THEN sqlc.narg(delete_external_after_days)::smallint ELSE delete_external_after_days END,
    erase_pregnancy_health_types = coalesce(sqlc.narg(erase_pregnancy_health_types)::boolean, erase_pregnancy_health_types),
    erase_sick_case_dates = coalesce(sqlc.narg(erase_sick_case_dates)::boolean, erase_sick_case_dates),
    default_account_kind = coalesce(sqlc.narg(default_account_kind)::varchar, default_account_kind),
    directory_fields = coalesce(sqlc.narg(directory_fields)::jsonb, directory_fields),
    competence_review_interval_months = coalesce(sqlc.narg(competence_review_interval_months)::smallint, competence_review_interval_months),
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = 1
RETURNING *;

-- name: SetThresholdsAcknowledged :exec
-- SetThresholdsAcknowledged replaces when each threshold was acknowledged
-- (design S20). The caller holds the row's lock (LockSettings) and adds one
-- key to what it read. The revision is left alone: it guards the replace of
-- the editable settings, which this is not among.
UPDATE staff.settings SET
    thresholds_acknowledged = @thresholds_acknowledged::jsonb,
    updated_at = @now::timestamptz
WHERE id = 1;

-- name: SetProviderStatus :exec
-- SetProviderStatus records what the accounts worker last saw of one
-- provider under its key — {"status", "reason", "checkedAt"} — the other
-- providers' kept. The revision and updated_at are left alone: no one's
-- settings changed.
UPDATE staff.settings SET
    provider_status = provider_status || jsonb_build_object(@provider::text, @status::jsonb)
WHERE id = 1;

-- name: SeedAccountKinds :exec
-- SeedAccountKinds gives every person the kind the recorded authority mode
-- reads as (plan reading 4: local → local, federated → external), once:
-- nothing while the settings say the kinds were seeded. The caller holds the
-- settings row's lock (LockSettings) and marks the seed done in the same
-- transaction (MarkAccountKindsSeeded).
UPDATE staff.people SET account_kind = @account_kind
WHERE NOT (SELECT s.account_kind_seeded FROM staff.settings s WHERE s.id = 1);

-- name: MarkAccountKindsSeeded :exec
-- MarkAccountKindsSeeded records that the kinds were seeded, the default
-- kind with them, and moves the revision on when the default changed, so a
-- settings form read before the seed is a conflict rather than a write of
-- the old default; a seed that leaves the default as it was (local) moves
-- nothing a form could hold. Nothing when they were seeded already.
UPDATE staff.settings SET
    default_account_kind = @default_account_kind::varchar,
    account_kind_seeded = true,
    revision = revision + CASE WHEN default_account_kind <> @default_account_kind::varchar THEN 1 ELSE 0 END,
    updated_at = CASE WHEN default_account_kind <> @default_account_kind::varchar THEN @now::timestamptz ELSE updated_at END
WHERE id = 1 AND NOT account_kind_seeded;
