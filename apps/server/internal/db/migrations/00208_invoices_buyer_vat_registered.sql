-- +goose Up
-- The buyer's VAT registration, snapshotted at the issue (invoices phase 6
-- delivery E-2, review M4): bokføringsforskriften § 5-1-2 has a buyer's
-- organisation number followed by "MVA" when the buyer is in the VAT register,
-- so the PDF prints "<org nr> MVA" and the EHF carries the buyer's
-- PartyTaxScheme. It is the customer directory's word at the issue — the
-- registry record's VAT flag — and false for every document issued before,
-- which keeps their PDF and EHF as they were. ADD COLUMN fires no row trigger,
-- so the issued rows' immutability is untouched.
ALTER TABLE invoices.invoices
    ADD COLUMN buyer_vat_registered boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE invoices.invoices
    DROP COLUMN buyer_vat_registered;
