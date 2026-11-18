-- +goose Up
-- One bank line for several invoices, and small differences rounded off
-- (invoices phase 6 design I14, I15; plan
-- docs/superpowers/plans/2026-11-17-invoices-phase6-plan.md, delivery B-2,
-- and its readings 10 to 12).

-- Every structured remittance reference a camt.054 line carried
-- (RmtInf/Strd/CdtrRefInf/Ref, of any type), in the file's order, at most
-- 20. The KID column keeps the first that is one; matching reads the rest
-- for the invoices one payment names. A line from before, an OCR line and a
-- bank-feed line have none and are read by their text. The fingerprint does
-- not read it, so duplicate detection is unchanged, and
-- refuse_bank_transaction_change freezes it with the rest of the row.
ALTER TABLE invoices.bank_transactions
    ADD COLUMN remittance_refs varchar(35)[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT ck_bank_transactions_remittance_refs CHECK (cardinality(remittance_refs) <= 20);

-- A line that matching applied across several invoices of one customer by
-- the references it named (I14): a line event, since a payment has no match
-- of its own. Like a match, the bank feed's sync writes it by no one.
ALTER TABLE invoices.bank_transaction_events
    DROP CONSTRAINT ck_bank_transaction_events_event,
    DROP CONSTRAINT ck_bank_transaction_events_by,
    ADD CONSTRAINT ck_bank_transaction_events_event CHECK (event IN ('matched', 'queued', 'applied', 'dismissed',
        'reversal_handled', 'reopened', 'duplicate_confirmed', 'treated_as_distinct', 'reversed', 'kept_as_credit',
        'auto_split')),
    ADD CONSTRAINT ck_bank_transaction_events_by CHECK (by_user_id IS NOT NULL OR event IN ('matched', 'queued', 'auto_split'));

-- What a payment may leave open and still be rounded off (I15), in NOK: 0
-- to 100, and 0, the default, rounds nothing off.
ALTER TABLE invoices.settings
    ADD COLUMN tolerance numeric(5,2) NOT NULL DEFAULT 0,
    ADD CONSTRAINT ck_settings_tolerance CHECK (tolerance BETWEEN 0 AND 100);

-- A write-off is a person's (manual) or the module's (auto): the rounding
-- write-off a payment that leaves at most the tolerance open writes by
-- itself, with no one registering it. Only a rounding write-off is ever
-- automatic. Adding a column with a constant default and dropping NOT NULL
-- write no row, so refuse_write_off_change needs no allowance; its jsonb
-- comparison freezes source as it does every column.
ALTER TABLE invoices.write_offs
    ADD COLUMN source varchar(10) NOT NULL DEFAULT 'manual',
    ALTER COLUMN registered_by_user_id DROP NOT NULL,
    ADD CONSTRAINT ck_write_offs_source CHECK (source IN ('manual', 'auto')),
    ADD CONSTRAINT ck_write_offs_actor CHECK ((source = 'manual') = (registered_by_user_id IS NOT NULL)),
    ADD CONSTRAINT ck_write_offs_auto CHECK (source = 'manual' OR reason = 'rounding');

-- The standing write-off whose reason is not rounding, 0 when there is none
-- (plan reading 12): what invoice_state reads as written off. A small
-- difference is not a loss, so an invoice a rounding write-off closed reads
-- paid — every rounding write-off, the manual ones made before this too.
-- written_off_amount still answers every standing write-off, which the
-- open amount subtracts (settled_amount).
-- +goose StatementBegin
CREATE FUNCTION invoices.loss_written_off_amount(p_invoice_id bigint)
RETURNS numeric LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce((SELECT w.amount FROM invoices.write_offs w
                     WHERE w.invoice_id = p_invoice_id AND w.undone_at IS NULL AND w.reason <> 'rounding'), 0)::numeric(14,2)
$$;
-- +goose StatementEnd

-- +goose Down
-- An automatic write-off has no one who registered it, and an auto_split
-- event names an event the older schema does not know: the Down refuses
-- while either exists, as 00182's does while a write-off does.
-- +goose StatementBegin
DO $down$
BEGIN
    IF EXISTS (SELECT 1 FROM invoices.write_offs WHERE source = 'auto') THEN
        RAISE EXCEPTION 'invoices: automatic write-offs exist' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.bank_transaction_events WHERE event = 'auto_split') THEN
        RAISE EXCEPTION 'invoices: a bank line was split automatically' USING ERRCODE = 'P0001';
    END IF;
END;
$down$;
-- +goose StatementEnd

DROP FUNCTION invoices.loss_written_off_amount(bigint);

-- No row is automatic (above), so every row has its user again; dropping a
-- column writes no row, so the strict trigger stands as it is.
ALTER TABLE invoices.write_offs
    DROP CONSTRAINT ck_write_offs_auto,
    DROP CONSTRAINT ck_write_offs_actor,
    DROP CONSTRAINT ck_write_offs_source,
    DROP COLUMN source,
    ALTER COLUMN registered_by_user_id SET NOT NULL;

ALTER TABLE invoices.settings
    DROP CONSTRAINT ck_settings_tolerance,
    DROP COLUMN tolerance;

ALTER TABLE invoices.bank_transaction_events
    DROP CONSTRAINT ck_bank_transaction_events_by,
    DROP CONSTRAINT ck_bank_transaction_events_event,
    ADD CONSTRAINT ck_bank_transaction_events_event CHECK (event IN ('matched', 'queued', 'applied', 'dismissed',
        'reversal_handled', 'reopened', 'duplicate_confirmed', 'treated_as_distinct', 'reversed', 'kept_as_credit')),
    ADD CONSTRAINT ck_bank_transaction_events_by CHECK (by_user_id IS NOT NULL OR event IN ('matched', 'queued'));

ALTER TABLE invoices.bank_transactions
    DROP CONSTRAINT ck_bank_transactions_remittance_refs,
    DROP COLUMN remittance_refs;
