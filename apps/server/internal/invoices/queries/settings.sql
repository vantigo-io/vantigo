-- name: GetSettings :one
-- GetSettings reads the installation's single settings row, the seller record
-- and the series start (invoices foundation design D2). The migration writes
-- it, so this always answers a row.
SELECT * FROM invoices.settings WHERE id = 1;

-- name: LockSettings :one
-- LockSettings takes the settings row FOR UPDATE: PUT /settings and every rate
-- change wait here behind an issue in flight, which holds the row FOR SHARE
-- until it commits (D2, D3). What they read after it is final.
SELECT * FROM invoices.settings WHERE id = 1 FOR UPDATE;

-- name: UpdateSettings :one
-- UpdateSettings replaces the seller record, the series start, the seller's
-- Peppol id, the KID agreement (EHF and KID design D2, D3), the VAT code
-- each kind of work is invoiced at (invoices work design D6) and the
-- timesheet's default and person label (D5), and moves the revision on. The
-- label's CHECK refuses anything but initials, number or name. The caller holds the row (LockSettings) and has checked the
-- revision and the series lock.
UPDATE invoices.settings SET
    legal_name = @legal_name,
    organisation_number = @organisation_number,
    vat_registered = @vat_registered,
    in_foretaksregisteret = @in_foretaksregisteret,
    address_line1 = @address_line1,
    address_line2 = @address_line2,
    postal_code = @postal_code,
    city = @city,
    country = @country,
    bank_account = @bank_account,
    iban = @iban,
    bic = @bic,
    email = @email,
    default_payment_terms_days = @default_payment_terms_days,
    default_currency = @default_currency,
    footer_text = @footer_text,
    series_start = @series_start,
    peppol_id = sqlc.narg(peppol_id),
    kid_length = sqlc.narg(kid_length),
    kid_algorithm = sqlc.narg(kid_algorithm),
    work_vat_code_hours = @work_vat_code_hours,
    work_vat_code_expenses = @work_vat_code_expenses,
    work_vat_code_milestones = @work_vat_code_milestones,
    timesheet_default = @timesheet_default,
    timesheet_person_label = @timesheet_person_label,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = 1
RETURNING *;

-- name: ShareSettings :one
-- ShareSettings takes the settings row FOR SHARE: the issue's seller snapshot
-- and series start (D6 step 3). Two issues share it; PUT /settings and a rate
-- change wait for both to commit.
SELECT * FROM invoices.settings WHERE id = 1 FOR SHARE;
