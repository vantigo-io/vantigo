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
-- timesheet's default and person label (D5), the two VAT codes an energy
-- line takes by its hint (energy consumption billing design D8), the channel
-- documents and reminders are sent from (integrations design T6; null is
-- Disabled, and the caller decides whether a request that leaves it out keeps
-- the stored one), and moves
-- the revision on. The phone is the seller's (invoices phase 6 design I4);
-- the tolerance what a payment may leave open and be rounded off (I15);
-- the logo is SetSettingsLogo's and ClearSettingsLogo's, never this one's. The
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
    phone = @phone,
    series_start = @series_start,
    peppol_id = sqlc.narg(peppol_id),
    kid_length = sqlc.narg(kid_length),
    kid_algorithm = sqlc.narg(kid_algorithm),
    work_vat_code_hours = @work_vat_code_hours,
    work_vat_code_expenses = @work_vat_code_expenses,
    work_vat_code_milestones = @work_vat_code_milestones,
    timesheet_default = @timesheet_default,
    timesheet_person_label = @timesheet_person_label,
    energy_vat_code = @energy_vat_code,
    energy_vat_code_exempt = @energy_vat_code_exempt,
    send_channel = sqlc.narg(send_channel),
    tolerance = @tolerance,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = 1
RETURNING *;

-- name: SetSettingsLogo :one
-- SetSettingsLogo names the seller's logo (invoices phase 6 design I1): the
-- object's key, the hash of its bytes and its type, set together. The
-- revision does not move: the logo is no field of the seller form, whose
-- unsaved edits it must not make stale. The caller holds the row
-- (LockSettings) and has stored the object.
UPDATE invoices.settings SET
    logo_object_key = @logo_object_key,
    logo_sha256 = @logo_sha256,
    logo_content_type = @logo_content_type,
    updated_at = @now::timestamptz
WHERE id = 1
RETURNING *;

-- name: ClearSettingsLogo :one
-- ClearSettingsLogo removes the seller's logo (I1): the three columns
-- cleared, the object never deleted, since an issued document's snapshot may
-- name it. The revision does not move, as SetSettingsLogo's. The caller
-- holds the row (LockSettings) and has seen a logo set.
UPDATE invoices.settings SET
    logo_object_key = NULL,
    logo_sha256 = NULL,
    logo_content_type = NULL,
    updated_at = @now::timestamptz
WHERE id = 1
RETURNING *;

-- name: ShareSettings :one
-- ShareSettings takes the settings row FOR SHARE: the issue's seller snapshot
-- and series start (D6 step 3). Two issues share it; PUT /settings and a rate
-- change wait for both to commit.
SELECT * FROM invoices.settings WHERE id = 1 FOR SHARE;

-- name: GetSendChannel :one
-- GetSendChannel reads the id of the channel documents and reminders are sent
-- from (integrations design T6), null for Disabled. The id names a
-- Communications channel; whether it can send now is asked of
-- contracts.OutboundMail, never read from that schema.
SELECT send_channel FROM invoices.settings WHERE id = 1;
