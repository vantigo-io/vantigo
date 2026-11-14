-- +goose Up
-- Write-offs (accounting design A16 as amended by R4; plan
-- docs/superpowers/plans/2026-11-14-accounting-plan.md, delivery B): giving
-- up an invoice's open amount, undoing that, and money that arrives after it.
-- Nothing here names a ledger; contracts.SalesJournal reads the facts.

-- A write-off closes an issued invoice's whole open amount on a day, with a
-- reason: loss (a loss that is konstatert, which may carry the VAT correction
-- of mval. § 4-7), rounding (a small difference) or other. amount is the open
-- amount it closed, fixed when it was made. It is bookkeeping material, so it
-- is never deleted and never edited: it is undone — undone_at,
-- undone_by_user_id and undo_reason set together, once — and a new one may
-- then be made. At most one stands per invoice.
CREATE TABLE invoices.write_offs (
    id                    bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id            bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    written_off_on        date          NOT NULL,
    amount                numeric(14,2) NOT NULL,
    currency              char(3)       NOT NULL,
    reason                varchar(10)   NOT NULL,
    vat_correction        boolean       NOT NULL DEFAULT false,
    registered_by_user_id uuid          NOT NULL,
    registered_at         timestamptz   NOT NULL,
    undone_at             timestamptz,
    undone_by_user_id     uuid,
    undo_reason           varchar(200),
    CONSTRAINT ck_write_offs_amount CHECK (amount > 0),
    CONSTRAINT ck_write_offs_reason CHECK (reason IN ('loss', 'rounding', 'other')),
    CONSTRAINT ck_write_offs_vat_correction CHECK (NOT vat_correction OR reason = 'loss'),
    CONSTRAINT ck_write_offs_undo CHECK (
        (undone_at IS NULL AND undone_by_user_id IS NULL AND undo_reason IS NULL)
        OR (undone_at IS NOT NULL AND undone_by_user_id IS NOT NULL AND undo_reason IS NOT NULL AND undo_reason <> ''))
);
CREATE INDEX ix_write_offs_invoice ON invoices.write_offs (invoice_id);
-- One standing write-off per invoice; the sums read it through this index.
CREATE UNIQUE INDEX ux_write_offs_live ON invoices.write_offs (invoice_id) WHERE undone_at IS NULL;

-- A write-off is refused a DELETE and every UPDATE but its undoing: the three
-- undo columns going from NULL to a value, the rest of the row unchanged.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_write_off_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.undone_at IS NULL AND NEW.undone_at IS NOT NULL
       AND (to_jsonb(OLD) - 'undone_at' - 'undone_by_user_id' - 'undo_reason')
           = (to_jsonb(NEW) - 'undone_at' - 'undone_by_user_id' - 'undo_reason') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a write-off is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_write_offs_immutable
    BEFORE UPDATE OR DELETE ON invoices.write_offs
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_write_off_change();

-- A write-off belongs to an issued invoice, read FOR SHARE first: 00035's
-- refuse_payment_on_unissued for payments. The API refuses first.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_write_off_on_unissued()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    parent_kind   text;
    parent_status text;
BEGIN
    SELECT kind, status INTO parent_kind, parent_status
    FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE;
    IF parent_kind IS DISTINCT FROM 'invoice' OR parent_status IS DISTINCT FROM 'issued' THEN
        RAISE EXCEPTION 'invoices: a write-off needs an issued invoice' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_write_offs_parent
    BEFORE INSERT ON invoices.write_offs
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_write_off_on_unissued();

-- Money that arrives after a write-off (R4) is a payment of kind recovered,
-- naming the write-off it recovers: it is not part of the open amount (the
-- write-off closed that) and is allowed up to the amount written off, which
-- the API judges under the invoice's lock. It is registered by hand.
ALTER TABLE invoices.payments
    ADD COLUMN kind         varchar(10) NOT NULL DEFAULT 'payment',
    ADD COLUMN write_off_id bigint REFERENCES invoices.write_offs (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_payments_kind CHECK (kind IN ('payment', 'recovered')),
    ADD CONSTRAINT ck_payments_recovered CHECK (
        (kind = 'recovered') = (write_off_id IS NOT NULL) AND (kind = 'payment' OR source = 'manual'));
CREATE INDEX ix_payments_write_off ON invoices.payments (write_off_id) WHERE write_off_id IS NOT NULL;

-- What settles an invoice's principal: its live payments that are not
-- recoveries, and its standing write-off. Every open amount and every state
-- this module derives reads it, so a written-off invoice is not open, not
-- overdue and not reminded.
-- +goose StatementBegin
CREATE FUNCTION invoices.settled_amount(p_invoice_id bigint)
RETURNS numeric LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT (coalesce((SELECT sum(p.amount) FROM invoices.payments p
                      WHERE p.invoice_id = p_invoice_id AND p.removed_at IS NULL AND p.kind = 'payment'), 0)
          + coalesce((SELECT w.amount FROM invoices.write_offs w
                      WHERE w.invoice_id = p_invoice_id AND w.undone_at IS NULL), 0))::numeric(14,2)
$$;
-- +goose StatementEnd

-- The standing write-off's amount, 0 when there is none.
-- +goose StatementBegin
CREATE FUNCTION invoices.written_off_amount(p_invoice_id bigint)
RETURNS numeric LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce((SELECT w.amount FROM invoices.write_offs w
                     WHERE w.invoice_id = p_invoice_id AND w.undone_at IS NULL), 0)::numeric(14,2)
$$;
-- +goose StatementEnd

-- A document's state with the write-off (D3, A16): document_state's rule,
-- with paid what settled_amount answers, and written_off when the standing
-- write-off is what leaves nothing open. The list, the stats and the customer
-- tab filter with it; Go's documentState is its mirror.
-- +goose StatementBegin
CREATE FUNCTION invoices.invoice_state(kind text, status text, gross numeric, credited numeric, paid numeric,
    written_off numeric, due_date date, today date)
RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN status = 'draft' THEN 'draft'
        WHEN kind = 'credit_note' THEN 'issued'
        WHEN credited > 0 AND credited >= gross THEN 'credited'
        WHEN gross - credited - paid <= 0 AND written_off > 0 THEN 'written_off'
        WHEN gross - credited - paid <= 0 THEN 'paid'
        WHEN due_date < today THEN 'overdue'
        WHEN paid > 0 THEN 'partially_paid'
        ELSE 'open'
    END
$$;
-- +goose StatementEnd

-- The fact log (00180) gains the write-off, its undoing, and the recovered
-- payment and its removal, so a ledger can tell a recovery from a payment.
ALTER TABLE invoices.journal_facts
    DROP CONSTRAINT ck_journal_facts_kind,
    DROP CONSTRAINT ck_journal_facts_removal,
    ADD CONSTRAINT ck_journal_facts_kind CHECK (kind IN (
        'payment', 'payment_removed', 'charge_payment', 'charge_payment_removed',
        'write_off', 'write_off_undone', 'recovered_payment', 'recovered_payment_removed')),
    ADD CONSTRAINT ck_journal_facts_removal CHECK (
        (kind IN ('payment_removed', 'charge_payment_removed', 'write_off_undone', 'recovered_payment_removed'))
        = (removed_at IS NOT NULL));

-- 00180's trigger function, now naming a recovered payment's facts
-- recovered_payment and recovered_payment_removed. The row's kind is read
-- through to_jsonb, since a charge payment has no such column.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.record_journal_fact()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    next_seq  bigint;
    fact_kind text := TG_ARGV[0];
BEGIN
    IF fact_kind = 'payment' AND to_jsonb(NEW) ->> 'kind' = 'recovered' THEN
        fact_kind := 'recovered_payment';
    END IF;
    IF TG_OP = 'INSERT' THEN
        INSERT INTO invoices.counters (counter_name, next_value)
        VALUES ('journal', 2)
        ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
        RETURNING next_value - 1 INTO next_seq;
        INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, business_date, created_at)
        VALUES (next_seq, fact_kind, NEW.id, NEW.invoice_id, NEW.paid_on, now());
        IF NEW.removed_at IS NULL THEN
            RETURN NULL;
        END IF;
    END IF;
    INSERT INTO invoices.counters (counter_name, next_value)
    VALUES ('journal', 2)
    ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
    RETURNING next_value - 1 INTO next_seq;
    INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, removed_at, created_at)
    VALUES (next_seq, fact_kind || '_removed', NEW.id, NEW.invoice_id, NEW.removed_at, now());
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

-- A write-off's facts, the same way: its insert is write_off on its day, its
-- undoing write_off_undone at the moment it was undone (the reader takes the
-- business day). A deferred constraint trigger, so the seq is taken at commit.
-- +goose StatementBegin
CREATE FUNCTION invoices.record_write_off_fact()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    next_seq bigint;
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO invoices.counters (counter_name, next_value)
        VALUES ('journal', 2)
        ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
        RETURNING next_value - 1 INTO next_seq;
        INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, business_date, created_at)
        VALUES (next_seq, 'write_off', NEW.id, NEW.invoice_id, NEW.written_off_on, now());
        IF NEW.undone_at IS NULL THEN
            RETURN NULL;
        END IF;
    END IF;
    INSERT INTO invoices.counters (counter_name, next_value)
    VALUES ('journal', 2)
    ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
    RETURNING next_value - 1 INTO next_seq;
    INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, removed_at, created_at)
    VALUES (next_seq, 'write_off_undone', NEW.id, NEW.invoice_id, NEW.undone_at, now());
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER tr_write_offs_journal_made
    AFTER INSERT ON invoices.write_offs
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION invoices.record_write_off_fact();
CREATE CONSTRAINT TRIGGER tr_write_offs_journal_undone
    AFTER UPDATE ON invoices.write_offs
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (OLD.undone_at IS NULL AND NEW.undone_at IS NOT NULL)
    EXECUTE FUNCTION invoices.record_write_off_fact();

-- A bank line for a written-off invoice (R15), from a file or the feed, is
-- queued invoice_written_off and registers nothing: money after a write-off
-- is a recovery, which a person registers.
ALTER TABLE invoices.bank_transactions
    DROP CONSTRAINT ck_bank_transactions_reason,
    ADD CONSTRAINT ck_bank_transactions_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed',
        'invoice_written_off'));
ALTER TABLE invoices.bank_transaction_events
    DROP CONSTRAINT ck_bank_transaction_events_reason,
    ADD CONSTRAINT ck_bank_transaction_events_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed',
        'invoice_written_off'));

-- +goose Down
-- A write-off and a recovery are bookkeeping: the Down refuses while one
-- exists, as 00180's does while the log holds a fact.
-- +goose StatementBegin
DO $down$
BEGIN
    IF EXISTS (SELECT 1 FROM invoices.write_offs)
       OR EXISTS (SELECT 1 FROM invoices.payments WHERE kind = 'recovered') THEN
        RAISE EXCEPTION 'invoices: write-offs exist' USING ERRCODE = 'P0001';
    END IF;
END;
$down$;
-- +goose StatementEnd

ALTER TABLE invoices.bank_transaction_events
    DROP CONSTRAINT ck_bank_transaction_events_reason,
    ADD CONSTRAINT ck_bank_transaction_events_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed'));
ALTER TABLE invoices.bank_transactions
    DROP CONSTRAINT ck_bank_transactions_reason,
    ADD CONSTRAINT ck_bank_transactions_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed'));
DROP TRIGGER tr_write_offs_journal_undone ON invoices.write_offs;
DROP TRIGGER tr_write_offs_journal_made ON invoices.write_offs;
DROP FUNCTION invoices.record_write_off_fact();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.record_journal_fact()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    next_seq bigint;
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO invoices.counters (counter_name, next_value)
        VALUES ('journal', 2)
        ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
        RETURNING next_value - 1 INTO next_seq;
        INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, business_date, created_at)
        VALUES (next_seq, TG_ARGV[0], NEW.id, NEW.invoice_id, NEW.paid_on, now());
        IF NEW.removed_at IS NULL THEN
            RETURN NULL;
        END IF;
    END IF;
    INSERT INTO invoices.counters (counter_name, next_value)
    VALUES ('journal', 2)
    ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
    RETURNING next_value - 1 INTO next_seq;
    INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, removed_at, created_at)
    VALUES (next_seq, TG_ARGV[0] || '_removed', NEW.id, NEW.invoice_id, NEW.removed_at, now());
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd
ALTER TABLE invoices.journal_facts
    DROP CONSTRAINT ck_journal_facts_kind,
    DROP CONSTRAINT ck_journal_facts_removal,
    ADD CONSTRAINT ck_journal_facts_kind
        CHECK (kind IN ('payment', 'payment_removed', 'charge_payment', 'charge_payment_removed')),
    ADD CONSTRAINT ck_journal_facts_removal
        CHECK ((kind IN ('payment_removed', 'charge_payment_removed')) = (removed_at IS NOT NULL));
DROP FUNCTION invoices.invoice_state(text, text, numeric, numeric, numeric, numeric, date, date);
DROP FUNCTION invoices.written_off_amount(bigint);
DROP FUNCTION invoices.settled_amount(bigint);
ALTER TABLE invoices.payments
    DROP CONSTRAINT ck_payments_recovered,
    DROP CONSTRAINT ck_payments_kind,
    DROP COLUMN write_off_id,
    DROP COLUMN kind;
DROP TABLE invoices.write_offs;
DROP FUNCTION invoices.refuse_write_off_on_unissued();
DROP FUNCTION invoices.refuse_write_off_change();
