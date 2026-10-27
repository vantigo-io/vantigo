-- +goose Up
-- Invoices phase 5, delivery D (energy consumption billing design D7-D9;
-- plan docs/superpowers/plans/2026-10-27-invoices-energy.md, reading 1): a
-- settlement of the energy module as a line's source, the lines that bill
-- it, the two VAT codes its lines take and the code for the north's
-- household exemption. This migration names no other schema: a source's
-- kind is opaque here, held to a shape, and the three work kinds are named
-- only as the kinds that carry a project.

-- The line sources (D7). A settlement carries no project (readings on the
-- record 8), a subkind (regular or correction) and the snapshot its
-- document prints from — the metering point, its address, its price area,
-- the contract's name, the period's first day (source_date is its last) and
-- whether any consumption was estimated — so the PDF and the EHF read the
-- document's own rows, never the energy module. Its quantity is its kWh,
-- which on a correction is a difference: zero or negative. line_count is how
-- many lines in a row, from the one line_id names, bill the source: a
-- settlement makes a line per priced element and is held once
-- (ux_line_sources_live), so its one row sits on its first line (reading 2).
ALTER TABLE invoices.line_sources
    ALTER COLUMN project_id DROP NOT NULL,
    ADD COLUMN line_count    integer      NOT NULL DEFAULT 1,
    ADD COLUMN point_gsrn    varchar(18),
    ADD COLUMN point_address varchar(320),
    ADD COLUMN price_area    varchar(4),
    ADD COLUMN contract_name varchar(120),
    ADD COLUMN period_from   date,
    ADD COLUMN estimated     boolean,
    DROP CONSTRAINT ck_line_sources_kind,
    ADD CONSTRAINT ck_line_sources_kind CHECK (source_kind ~ '^[a-z]+\.[a-z_]+$'),
    ADD CONSTRAINT ck_line_sources_project
        CHECK ((project_id IS NOT NULL) = (source_kind IN ('time.entry', 'expenses.entry', 'projects.milestone'))),
    DROP CONSTRAINT ck_line_sources_subkind,
    ADD CONSTRAINT ck_line_sources_subkind
        CHECK ((source_kind = 'expenses.entry' OR project_id IS NULL) = (source_subkind IS NOT NULL)),
    DROP CONSTRAINT ck_line_sources_quantity,
    ADD CONSTRAINT ck_line_sources_quantity CHECK (quantity > 0 OR project_id IS NULL),
    ADD CONSTRAINT ck_line_sources_line_count CHECK (line_count >= 1 AND (line_count = 1 OR project_id IS NULL)),
    ADD CONSTRAINT ck_line_sources_point CHECK (
        (project_id IS NULL AND point_gsrn IS NOT NULL AND point_address IS NOT NULL AND price_area IS NOT NULL
            AND contract_name IS NOT NULL AND period_from IS NOT NULL AND estimated IS NOT NULL)
        OR (project_id IS NOT NULL AND point_gsrn IS NULL AND point_address IS NULL AND price_area IS NULL
            AND contract_name IS NULL AND period_from IS NULL AND estimated IS NULL));

-- The lines (D7, readings on the record 3 and 9). A unit price takes eight
-- decimals, so an energy line's net can be the settlement line's amount
-- while quantity × price stays its net (PEPPOL-EN16931-R120); the module
-- still accepts four on every other line. A correction line — one billing a
-- correction settlement, set by the module on every write, never by a
-- request — may be negative, as a deduction line may: the CHECK cannot see
-- line_sources, so the flag is the line's own.
ALTER TABLE invoices.lines
    ALTER COLUMN unit_price TYPE numeric(18,8),
    ADD COLUMN correction boolean NOT NULL DEFAULT false,
    DROP CONSTRAINT ck_lines_quantity,
    ADD CONSTRAINT ck_lines_quantity
        CHECK (quantity > 0 OR (quantity < 0 AND (deducts_invoice_id IS NOT NULL OR correction))),
    ADD CONSTRAINT ck_lines_correction
        CHECK (NOT correction OR (deducts_invoice_id IS NULL AND discount_percent = 0));

-- The north's household exemption (D8, U4): mval. § 6-6, zero-rated, with
-- the reason the PDF's VAT summary prints. Seeded only where neither its id
-- nor its code is taken, as 00034 seeds the others.
INSERT INTO invoices.vat_codes (id, code, name, saf_t_code, ehf_category, exemption_reason, active, created_at, updated_at)
OVERRIDING SYSTEM VALUE
SELECT 10, '5N', 'Fritatt – kraft til husholdninger i Nord-Norge 0 %', '5', 'Z',
    'Elektrisk kraft til husholdningsbruk i Nord-Norge, mval. § 6-6', true, now(), now()
WHERE NOT EXISTS (SELECT 1 FROM invoices.vat_codes WHERE id = 10 OR lower(code) = '5n');
INSERT INTO invoices.vat_code_rates (vat_code_id, rate_percent, valid_from, valid_to, created_at)
SELECT 10, 0, DATE '2026-01-01', NULL, now()
WHERE EXISTS (SELECT 1 FROM invoices.vat_codes WHERE id = 10 AND code = '5N')
  AND NOT EXISTS (SELECT 1 FROM invoices.vat_code_rates WHERE vat_code_id = 10);

-- The VAT codes an energy line takes by its hint (D8): standard, and the
-- north's household exemption — code 10 where it was seeded, else 5.
ALTER TABLE invoices.settings
    ADD COLUMN energy_vat_code        integer NOT NULL DEFAULT 1 REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    ADD COLUMN energy_vat_code_exempt integer NOT NULL DEFAULT 5 REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT;
UPDATE invoices.settings SET energy_vat_code_exempt = 10
WHERE EXISTS (SELECT 1 FROM invoices.vat_codes WHERE id = 10 AND code = '5N');

-- +goose Down
-- Everything 00078 added, in reverse. Restoring the line sources' NOT NULL
-- and CHECKs fails while a settlement is a source, the old quantity CHECK
-- while a correction line exists, and the code's delete while anything uses
-- it: a Down never deletes bookkeeping. Narrowing the unit price would round
-- one silently, so it is refused first.
ALTER TABLE invoices.settings
    DROP COLUMN energy_vat_code_exempt,
    DROP COLUMN energy_vat_code;
DELETE FROM invoices.vat_code_rates
WHERE vat_code_id = 10 AND EXISTS (SELECT 1 FROM invoices.vat_codes WHERE id = 10 AND code = '5N');
DELETE FROM invoices.vat_codes WHERE id = 10 AND code = '5N';

-- +goose StatementBegin
DO $down$
BEGIN
    IF EXISTS (SELECT 1 FROM invoices.lines WHERE unit_price <> round(unit_price, 4)) THEN
        RAISE EXCEPTION 'invoices: a line''s unit price has more than four decimals' USING ERRCODE = 'P0001';
    END IF;
END;
$down$;
-- +goose StatementEnd

ALTER TABLE invoices.lines
    DROP CONSTRAINT ck_lines_correction,
    DROP CONSTRAINT ck_lines_quantity,
    ADD CONSTRAINT ck_lines_quantity
        CHECK (quantity > 0 OR (quantity < 0 AND deducts_invoice_id IS NOT NULL)),
    DROP COLUMN correction,
    ALTER COLUMN unit_price TYPE numeric(14,4);

ALTER TABLE invoices.line_sources
    DROP CONSTRAINT ck_line_sources_point,
    DROP CONSTRAINT ck_line_sources_line_count,
    DROP CONSTRAINT ck_line_sources_quantity,
    ADD CONSTRAINT ck_line_sources_quantity CHECK (quantity > 0),
    DROP CONSTRAINT ck_line_sources_subkind,
    ADD CONSTRAINT ck_line_sources_subkind
        CHECK ((source_kind = 'expenses.entry') = (source_subkind IS NOT NULL)),
    DROP CONSTRAINT ck_line_sources_project,
    DROP CONSTRAINT ck_line_sources_kind,
    ADD CONSTRAINT ck_line_sources_kind
        CHECK (source_kind IN ('time.entry', 'expenses.entry', 'projects.milestone')),
    DROP COLUMN estimated,
    DROP COLUMN period_from,
    DROP COLUMN contract_name,
    DROP COLUMN price_area,
    DROP COLUMN point_address,
    DROP COLUMN point_gsrn,
    DROP COLUMN line_count,
    ALTER COLUMN project_id SET NOT NULL;
