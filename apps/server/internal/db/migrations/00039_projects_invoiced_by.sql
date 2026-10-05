-- +goose Up
-- The invoice that invoiced a billing milestone (invoices work design D1): its
-- id and number, opaque — the invoices schema is another module's (rule 4).
-- Written only by the invoices issue through contracts.InvoicedWorkHolder,
-- cleared only by the credit note that returns the milestone's line. Both or
-- neither, and only on an invoiced milestone; a milestone marked invoiced by
-- hand (design E5's manual step) carries its stamps without them, and only
-- such a milestone may be moved back to ready by hand.
ALTER TABLE projects.billing_milestones
    ADD COLUMN invoiced_invoice_id bigint,
    ADD COLUMN invoiced_number     bigint,
    ADD CONSTRAINT ck_billing_milestones_invoiced_by
        CHECK ((invoiced_invoice_id IS NULL) = (invoiced_number IS NULL)),
    ADD CONSTRAINT ck_billing_milestones_invoiced_by_status
        CHECK (invoiced_invoice_id IS NULL OR status = 'invoiced');

-- +goose Down
ALTER TABLE projects.billing_milestones
    DROP CONSTRAINT ck_billing_milestones_invoiced_by_status,
    DROP CONSTRAINT ck_billing_milestones_invoiced_by,
    DROP COLUMN invoiced_number,
    DROP COLUMN invoiced_invoice_id;
