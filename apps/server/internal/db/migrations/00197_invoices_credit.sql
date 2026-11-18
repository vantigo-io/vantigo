-- +goose Up
-- The customer's credit (invoices phase 6 design I8-I13, I16, I17a; plan
-- docs/superpowers/plans/2026-11-17-invoices-phase6-plan.md, delivery B-1,
-- with its readings): a ledger of every movement of a customer's credit, the
-- refunds paid back outside Vantigo, the balances a merge moved, the NOK a
-- payment and a write-off brought, a credit note's surplus taken off its
-- invoice's open amount, and the journal facts a ledger posts the credit
-- from. Accounting's side is its own migration (00198), since a migration
-- names one module's schema.

-- A refund (I12): money paid back to the customer outside Vantigo, from the
-- account bank_account names. It is bookkeeping, so it is never deleted and
-- never edited: a mistake is removed — the triple set together, once, with a
-- reason — as a payment is (00035). A merge re-points customer_id; the
-- anonymisation blanks the note.
CREATE TABLE invoices.credit_refunds (
    id                    bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    customer_id           integer       NOT NULL,
    refunded_on           date          NOT NULL,
    amount                numeric(14,2) NOT NULL,
    currency              char(3)       NOT NULL,
    amount_nok            numeric(14,2) NOT NULL,
    bank_account          char(11)      NOT NULL,
    reference             varchar(100)  NOT NULL DEFAULT '',
    note                  varchar(500)  NOT NULL DEFAULT '',
    registered_by_user_id uuid          NOT NULL,
    registered_at         timestamptz   NOT NULL,
    removed_at            timestamptz,
    removed_by_user_id    uuid,
    removal_reason        varchar(200),
    CONSTRAINT ck_credit_refunds_amount CHECK (amount > 0),
    CONSTRAINT ck_credit_refunds_bank_account CHECK (bank_account ~ '^[0-9]{11}$'),
    CONSTRAINT ck_credit_refunds_removal CHECK (
        (removed_at IS NULL AND removed_by_user_id IS NULL AND removal_reason IS NULL)
        OR (removed_at IS NOT NULL AND removed_by_user_id IS NOT NULL AND removal_reason IS NOT NULL AND removal_reason <> ''))
);
CREATE INDEX ix_credit_refunds_customer ON invoices.credit_refunds (customer_id);

-- The ledger (I8, plan reading 2): one row per movement, signed, never 0.
-- overpayment (+) is the part of money received above what was open, by
-- hand (payment_id, the payment that took what was open) or from the queue
-- (bank_transaction_id); credit_note (+) what a credit note credited beyond
-- its invoice's open amount (document_id the credit note, invoice_id its
-- original; I10); applied (−) credit used against an invoice through a
-- payment of source credit (I11); refunded (−) a refund (I12); reversed (±)
-- the counter-row of a removal, naming the row it reverses, with that row's
-- invoice. registered_by_user_id is NULL only on a credit_note row, which the
-- issue's follow-up writes. amount_nok is the NOK the movement is worth
-- (I13): the amount itself for NOK.
CREATE TABLE invoices.customer_credits (
    id                    bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    customer_id           integer       NOT NULL,
    currency              char(3)       NOT NULL,
    amount                numeric(14,2) NOT NULL,
    amount_nok            numeric(14,2) NOT NULL,
    kind                  varchar(15)   NOT NULL,
    credit_day            date          NOT NULL,
    note                  varchar(500)  NOT NULL DEFAULT '',
    invoice_id            bigint        REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    payment_id            bigint        REFERENCES invoices.payments (id) ON DELETE RESTRICT,
    bank_transaction_id   bigint        REFERENCES invoices.bank_transactions (id) ON DELETE RESTRICT,
    document_id           bigint        REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    refund_id             bigint        REFERENCES invoices.credit_refunds (id) ON DELETE RESTRICT,
    reverses_id           bigint        REFERENCES invoices.customer_credits (id) ON DELETE RESTRICT,
    registered_by_user_id uuid,
    registered_at         timestamptz   NOT NULL,
    CONSTRAINT ck_customer_credits_amount CHECK (amount <> 0),
    CONSTRAINT ck_customer_credits_kind CHECK (kind IN ('overpayment', 'credit_note', 'applied', 'refunded', 'reversed')),
    CONSTRAINT ck_customer_credits_actor CHECK ((registered_by_user_id IS NULL) = (kind = 'credit_note')),
    CONSTRAINT ck_customer_credits_source CHECK (
        (kind = 'overpayment' AND amount > 0 AND invoice_id IS NOT NULL
            AND (payment_id IS NULL) <> (bank_transaction_id IS NULL)
            AND document_id IS NULL AND refund_id IS NULL AND reverses_id IS NULL)
        OR (kind = 'credit_note' AND amount > 0 AND invoice_id IS NOT NULL AND document_id IS NOT NULL
            AND payment_id IS NULL AND bank_transaction_id IS NULL AND refund_id IS NULL AND reverses_id IS NULL)
        OR (kind = 'applied' AND amount < 0 AND invoice_id IS NOT NULL AND payment_id IS NOT NULL
            AND bank_transaction_id IS NULL AND document_id IS NULL AND refund_id IS NULL AND reverses_id IS NULL)
        OR (kind = 'refunded' AND amount < 0 AND refund_id IS NOT NULL AND invoice_id IS NULL
            AND payment_id IS NULL AND bank_transaction_id IS NULL AND document_id IS NULL AND reverses_id IS NULL)
        OR (kind = 'reversed' AND reverses_id IS NOT NULL
            AND payment_id IS NULL AND bank_transaction_id IS NULL AND document_id IS NULL AND refund_id IS NULL))
);
-- A credit note moves its surplus once (I10): the follow-up's idempotency.
CREATE UNIQUE INDEX ux_customer_credits_credit_note ON invoices.customer_credits (document_id) WHERE kind = 'credit_note';
-- A payment of source credit has one applied row.
CREATE UNIQUE INDEX ux_customer_credits_applied ON invoices.customer_credits (payment_id) WHERE kind = 'applied';
CREATE INDEX ix_customer_credits_balance ON invoices.customer_credits (customer_id, currency);
CREATE INDEX ix_customer_credits_invoice ON invoices.customer_credits (invoice_id) WHERE invoice_id IS NOT NULL;
CREATE INDEX ix_customer_credits_line ON invoices.customer_credits (bank_transaction_id) WHERE bank_transaction_id IS NOT NULL;
CREATE INDEX ix_customer_credits_payment ON invoices.customer_credits (payment_id) WHERE payment_id IS NOT NULL;
CREATE INDEX ix_customer_credits_reverses ON invoices.customer_credits (reverses_id) WHERE reverses_id IS NOT NULL;
CREATE INDEX ix_customer_credits_refund ON invoices.customer_credits (refund_id) WHERE refund_id IS NOT NULL;

-- A balance moved by a merge (plan reading 4): per currency in which the
-- absorbed customer held credit, written by the merge after it re-points the
-- rows, and never changed. The ledger hears from it that a balance moved
-- customer.
CREATE TABLE invoices.credit_merges (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    from_customer_id integer       NOT NULL,
    into_customer_id integer       NOT NULL,
    currency         char(3)       NOT NULL,
    amount           numeric(14,2) NOT NULL,
    amount_nok       numeric(14,2) NOT NULL,
    merged_at        timestamptz   NOT NULL,
    CONSTRAINT ck_credit_merges_amount CHECK (amount > 0),
    CONSTRAINT ck_credit_merges_customers CHECK (from_customer_id <> into_customer_id)
);

-- A movement is refused a DELETE, and every UPDATE but two, which one
-- statement may make together: the merge's customer_id and the
-- anonymisation's note to ''. The rest of the row, as jsonb, never changes —
-- refuse_payment_change's shape (00035), so a column added later is covered.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_customer_credit_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (to_jsonb(OLD) - 'customer_id' - 'note') = (to_jsonb(NEW) - 'customer_id' - 'note')
       AND (NEW.note = OLD.note OR NEW.note = '') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a customer credit movement is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_customer_credits_immutable
    BEFORE UPDATE OR DELETE ON invoices.customer_credits
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_customer_credit_change();

-- The floor of every movement written (I8, I13, I17a). The customer's credit
-- lock — the advisory lock every path takes first through lockCustomerCredit,
-- so this takes it again in the same transaction and never waits — then:
-- amount_nok is the amount for NOK when the writer left it out; a marked
-- customer's note is blanked (guard_delivery_insert's shape, 00035); a
-- reversal is of the opposite sign to the row it reverses, of at most what is
-- left of it — all of it, once, unless it returns a credit note's surplus,
-- which goes back in parts — and carries that row's invoice; and the balance
-- of the customer in the currency never goes below zero. The API refuses
-- first, with credit_exceeds_balance; this is the floor.
-- +goose StatementBegin
CREATE FUNCTION invoices.guard_customer_credit_insert()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    original invoices.customer_credits%ROWTYPE;
    reversed numeric;
    balance  numeric;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('invoices.customer_credit:' || NEW.customer_id, 0));
    IF NEW.amount_nok IS NULL AND NEW.currency = 'NOK' THEN
        NEW.amount_nok := NEW.amount;
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = NEW.customer_id) THEN
        NEW.note := '';
    END IF;
    IF NEW.kind = 'reversed' THEN
        SELECT * INTO original FROM invoices.customer_credits WHERE id = NEW.reverses_id;
        IF original.kind = 'reversed' THEN
            RAISE EXCEPTION 'invoices: a reversal is never reversed' USING ERRCODE = 'P0001';
        END IF;
        IF original.customer_id <> NEW.customer_id OR original.currency <> NEW.currency
           OR sign(original.amount) = sign(NEW.amount)
           OR original.invoice_id IS DISTINCT FROM NEW.invoice_id THEN
            RAISE EXCEPTION 'invoices: a reversal mirrors the movement it reverses' USING ERRCODE = 'P0001';
        END IF;
        SELECT coalesce(sum(amount), 0) INTO reversed FROM invoices.customer_credits WHERE reverses_id = NEW.reverses_id;
        IF abs(reversed + NEW.amount) > abs(original.amount)
           OR (original.kind <> 'credit_note' AND (reversed <> 0 OR NEW.amount <> -original.amount)) THEN
            RAISE EXCEPTION 'invoices: a movement is reversed once, at most what it was' USING ERRCODE = 'P0001';
        END IF;
    END IF;
    SELECT coalesce(sum(amount), 0) INTO balance FROM invoices.customer_credits
    WHERE customer_id = NEW.customer_id AND currency = NEW.currency;
    IF balance + NEW.amount < 0 THEN
        RAISE EXCEPTION 'invoices: a customer''s credit never goes below zero' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_customer_credits_guard
    BEFORE INSERT ON invoices.customer_credits
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_customer_credit_insert();

-- A refund is refused a DELETE, and every UPDATE but three: its removal (the
-- triple from NULL to a value), the merge's customer_id and the note to ''.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_credit_refund_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (to_jsonb(OLD) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note' - 'customer_id')
           = (to_jsonb(NEW) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note' - 'customer_id')
       AND (NEW.note = OLD.note OR NEW.note = '')
       AND ((OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL)
            OR (OLD.removed_at, OLD.removed_by_user_id, OLD.removal_reason)
               IS NOT DISTINCT FROM (NEW.removed_at, NEW.removed_by_user_id, NEW.removal_reason)) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a credit refund is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_credit_refunds_immutable
    BEFORE UPDATE OR DELETE ON invoices.credit_refunds
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_credit_refund_change();

-- A refund's insert: amount_nok and the marked customer's note, as a
-- movement's.
-- +goose StatementBegin
CREATE FUNCTION invoices.guard_credit_refund_insert()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.amount_nok IS NULL AND NEW.currency = 'NOK' THEN
        NEW.amount_nok := NEW.amount;
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = NEW.customer_id) THEN
        NEW.note := '';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_credit_refunds_guard
    BEFORE INSERT ON invoices.credit_refunds
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_credit_refund_insert();

-- A merge's record is never changed.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_credit_merge_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    RAISE EXCEPTION 'invoices: a credit merge is never changed' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_credit_merges_immutable
    BEFORE UPDATE OR DELETE ON invoices.credit_merges
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_credit_merge_change();

-- A payment knows the NOK it brought (I13). The column is added nullable,
-- the table held against writes to the commit, as 00180 holds it for its
-- backfill, so no payment commits between the backfill and NOT NULL. While
-- the column fills, refuse_payment_change allows one more write: amount_nok
-- from NULL to the amount, the rest of the row unchanged; then 00035's body
-- is restored word for word. Its jsonb comparison covers amount_nok as it
-- covers every column, so the strict trigger freezes it with no new clause.
ALTER TABLE invoices.payments ADD COLUMN amount_nok numeric(14,2);
LOCK TABLE invoices.payments, invoices.write_offs IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_payment_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.amount_nok IS NULL AND NEW.amount_nok = NEW.amount
       AND (to_jsonb(OLD) - 'amount_nok') = (to_jsonb(NEW) - 'amount_nok') THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a payment registration is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(OLD) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
           = (to_jsonb(NEW) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
       AND (NEW.note = OLD.note OR NEW.note = '')
       AND ((OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL)
            OR (NEW.note = ''
                AND (OLD.removed_at, OLD.removed_by_user_id, OLD.removal_reason)
                    IS NOT DISTINCT FROM (NEW.removed_at, NEW.removed_by_user_id, NEW.removal_reason))) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a payment registration is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

UPDATE invoices.payments SET amount_nok = amount;
ALTER TABLE invoices.payments ALTER COLUMN amount_nok SET NOT NULL;

-- 00035's body, restored. amount_nok is one of the columns its jsonb
-- comparison freezes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_payment_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a payment registration is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(OLD) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
           = (to_jsonb(NEW) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
       AND (NEW.note = OLD.note OR NEW.note = '')
       AND ((OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL)
            OR (NEW.note = ''
                AND (OLD.removed_at, OLD.removed_by_user_id, OLD.removal_reason)
                    IS NOT DISTINCT FROM (NEW.removed_at, NEW.removed_by_user_id, NEW.removal_reason))) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a payment registration is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

-- A NOK payment written without amount_nok takes its amount (I13): every
-- insert this module made before B-1 keeps working.
-- +goose StatementBegin
CREATE FUNCTION invoices.guard_payment_amount_nok()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.amount_nok IS NULL AND NEW.currency = 'NOK' THEN
        NEW.amount_nok := NEW.amount;
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_payments_amount_nok
    BEFORE INSERT ON invoices.payments
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_payment_amount_nok();

-- A write-off's amount_nok, the same way, through 00182's trigger: its one
-- allowed write is the undoing, and while the column fills amount_nok from
-- NULL to the amount besides.
ALTER TABLE invoices.write_offs ADD COLUMN amount_nok numeric(14,2);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_write_off_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.amount_nok IS NULL AND NEW.amount_nok = NEW.amount
       AND (to_jsonb(OLD) - 'amount_nok') = (to_jsonb(NEW) - 'amount_nok') THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.undone_at IS NULL AND NEW.undone_at IS NOT NULL
       AND (to_jsonb(OLD) - 'undone_at' - 'undone_by_user_id' - 'undo_reason')
           = (to_jsonb(NEW) - 'undone_at' - 'undone_by_user_id' - 'undo_reason') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a write-off is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

UPDATE invoices.write_offs SET amount_nok = amount;
ALTER TABLE invoices.write_offs ALTER COLUMN amount_nok SET NOT NULL;

-- 00182's body, restored.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_write_off_change()
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

CREATE TRIGGER tr_write_offs_amount_nok
    BEFORE INSERT ON invoices.write_offs
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_payment_amount_nok();

-- Credit applied (I11) is a payment of source credit: no bank line, no
-- attempt, and the person who applied it.
ALTER TABLE invoices.payments
    DROP CONSTRAINT ck_payments_source,
    DROP CONSTRAINT ck_payments_origin;
ALTER TABLE invoices.payments
    ADD CONSTRAINT ck_payments_source CHECK (source IN ('manual', 'ocr', 'camt054', 'vipps', 'bank_feed', 'credit')),
    ADD CONSTRAINT ck_payments_origin CHECK (
        (source = 'manual' AND bank_transaction_id IS NULL AND registered_by_user_id IS NOT NULL
            AND payment_attempt_id IS NULL)
        OR (source IN ('ocr', 'camt054') AND bank_transaction_id IS NOT NULL AND registered_by_user_id IS NOT NULL
            AND payment_attempt_id IS NULL)
        OR (source = 'vipps' AND payment_attempt_id IS NOT NULL AND bank_transaction_id IS NULL
            AND registered_by_user_id IS NULL)
        OR (source = 'bank_feed' AND bank_transaction_id IS NOT NULL AND payment_attempt_id IS NULL)
        OR (source = 'credit' AND bank_transaction_id IS NULL AND payment_attempt_id IS NULL
            AND registered_by_user_id IS NOT NULL));

-- What a credit note's surplus moved off an invoice (I10): its credit_note
-- rows, less what of them a reversal returned. 0 when nothing moved.
-- +goose StatementBegin
CREATE FUNCTION invoices.moved_amount(p_invoice_id bigint)
RETURNS numeric LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce((SELECT sum(c.amount) FROM invoices.customer_credits c
                     WHERE c.invoice_id = p_invoice_id
                       AND (c.kind = 'credit_note'
                            OR (c.kind = 'reversed' AND EXISTS (
                                SELECT 1 FROM invoices.customer_credits o
                                WHERE o.id = c.reverses_id AND o.kind = 'credit_note')))), 0)::numeric(14,2)
$$;
-- +goose StatementEnd

-- 00182's settled_amount, less what moved (I10): open = gross − credited −
-- paid − written off + moved, so an invoice whose credit note's surplus went
-- to the customer reads 0 open, not below. Every open amount and state the
-- queries derive reads it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.settled_amount(p_invoice_id bigint)
RETURNS numeric LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT (coalesce((SELECT sum(p.amount) FROM invoices.payments p
                      WHERE p.invoice_id = p_invoice_id AND p.removed_at IS NULL AND p.kind = 'payment'), 0)
          + coalesce((SELECT w.amount FROM invoices.write_offs w
                      WHERE w.invoice_id = p_invoice_id AND w.undone_at IS NULL), 0)
          - invoices.moved_amount(p_invoice_id))::numeric(14,2)
$$;
-- +goose StatementEnd

-- The fact log (I16, plan readings 3 and 4): a credit fact per movement and
-- a credit_merged fact per merge record. A refund's movement and a merge name
-- no invoice, so invoice_id is nullable for those two kinds only.
-- customer_id is the customer the movement belonged to when it was written —
-- a merge later re-points the row, and moves the balance by a fact of its
-- own — and is set on the two credit kinds only.
ALTER TABLE invoices.journal_facts
    ALTER COLUMN invoice_id DROP NOT NULL,
    ADD COLUMN customer_id integer,
    DROP CONSTRAINT ck_journal_facts_kind,
    ADD CONSTRAINT ck_journal_facts_kind CHECK (kind IN (
        'payment', 'payment_removed', 'charge_payment', 'charge_payment_removed',
        'write_off', 'write_off_undone', 'recovered_payment', 'recovered_payment_removed',
        'credit', 'credit_merged')),
    ADD CONSTRAINT ck_journal_facts_invoice CHECK (invoice_id IS NOT NULL OR kind IN ('credit', 'credit_merged')),
    ADD CONSTRAINT ck_journal_facts_customer CHECK ((customer_id IS NOT NULL) = (kind IN ('credit', 'credit_merged')));

-- A movement's fact on its credit_day; a merge's on the UTC day of
-- merged_at, which the reader replaces with the business day merged_at falls
-- on, as it does for a removal. A deferred constraint trigger, so the seq is
-- taken at commit (00180).
-- +goose StatementBegin
CREATE FUNCTION invoices.record_credit_fact()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    next_seq bigint;
BEGIN
    INSERT INTO invoices.counters (counter_name, next_value)
    VALUES ('journal', 2)
    ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
    RETURNING next_value - 1 INTO next_seq;
    IF TG_TABLE_NAME = 'credit_merges' THEN
        INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, customer_id, business_date, created_at)
        VALUES (next_seq, 'credit_merged', NEW.id, NULL, NEW.into_customer_id,
                (NEW.merged_at AT TIME ZONE 'UTC')::date, now());
    ELSE
        INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, customer_id, business_date, created_at)
        VALUES (next_seq, 'credit', NEW.id, NEW.invoice_id, NEW.customer_id, NEW.credit_day, now());
    END IF;
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER tr_customer_credits_journal
    AFTER INSERT ON invoices.customer_credits
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION invoices.record_credit_fact();
CREATE CONSTRAINT TRIGGER tr_credit_merges_journal
    AFTER INSERT ON invoices.credit_merges
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION invoices.record_credit_fact();

-- invoices.customer_credit.changed (spec §7): the customer, the currency and
-- the balance after the movement, written by the movement itself, under the
-- customer's credit lock. A merge's record publishes one for each of its two
-- customers; the merge writes it after re-pointing the rows, so the balances
-- are the merged ones. No personal data.
-- +goose StatementBegin
CREATE FUNCTION invoices.publish_customer_credit_changed() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    customers integer[];
    customer  integer;
BEGIN
    -- Each branch names only its own table's columns: a record field is
    -- resolved when its statement runs.
    IF TG_TABLE_NAME = 'credit_merges' THEN
        customers := ARRAY[NEW.from_customer_id, NEW.into_customer_id];
    ELSE
        customers := ARRAY[NEW.customer_id];
    END IF;
    FOREACH customer IN ARRAY customers LOOP
        INSERT INTO platform.events (name, version, module, aggregate, aggregate_id, payload, occurred_at)
        VALUES ('invoices.customer_credit.changed', 1, 'invoices', 'invoices.customer_credit', customer::text,
                jsonb_build_object('customerId', customer, 'currency', NEW.currency,
                    'balance', to_char((SELECT coalesce(sum(c.amount), 0) FROM invoices.customer_credits c
                                        WHERE c.customer_id = customer AND c.currency = NEW.currency),
                                       'FM999999999990.00')),
                now());
    END LOOP;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER tr_customer_credits_changed AFTER INSERT ON invoices.customer_credits
    FOR EACH ROW EXECUTE FUNCTION invoices.publish_customer_credit_changed();
CREATE TRIGGER tr_credit_merges_changed AFTER INSERT ON invoices.credit_merges
    FOR EACH ROW EXECUTE FUNCTION invoices.publish_customer_credit_changed();

-- invoices.payment.received gains amountNok (I13), additive in version 1.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.publish_payment_received() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    inv invoices.invoices%ROWTYPE;
BEGIN
    SELECT * INTO inv FROM invoices.invoices WHERE id = NEW.invoice_id;
    INSERT INTO platform.events (name, version, module, aggregate, aggregate_id, payload, occurred_at)
    VALUES ('invoices.payment.received', 1, 'invoices', 'invoices.payment', NEW.id::text,
            jsonb_build_object(
                'paymentId', NEW.id, 'invoiceId', NEW.invoice_id, 'number', coalesce(inv.number, 0),
                'customerId', inv.customer_id, 'amount', to_char(NEW.amount, 'FM999999999990.00'), 'currency', NEW.currency,
                'amountNok', to_char(NEW.amount_nok, 'FM999999999990.00'),
                'paidOn', to_char(NEW.paid_on, 'YYYY-MM-DD'), 'source', NEW.source, 'registeredAt', NEW.registered_at),
            NEW.registered_at);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- A queue line whose surplus a person kept as credit (I9).
ALTER TABLE invoices.bank_transaction_events
    DROP CONSTRAINT ck_bank_transaction_events_event,
    ADD CONSTRAINT ck_bank_transaction_events_event CHECK (event IN ('matched', 'queued', 'applied', 'dismissed',
        'reversal_handled', 'reopened', 'duplicate_confirmed', 'treated_as_distinct', 'reversed', 'kept_as_credit'));

-- The backfill (I10): every issued invoice whose open amount is below zero
-- today — a credit note credited more than was left after its payments —
-- moves its surplus to its customer, unattributed, as the follow-up would
-- have: the amount the negation of the open amount, the credit note the
-- latest issued one crediting the invoice, the day its issue date. The facts
-- are numbered at this migration's commit.
INSERT INTO invoices.customer_credits (customer_id, currency, amount, amount_nok, kind, credit_day, invoice_id,
    document_id, registered_at)
SELECT i.customer_id, i.currency, -o.open_amount, round(-o.open_amount * i.exchange_rate, 2), 'credit_note',
    cn.issue_date, i.id, cn.id, now()
FROM invoices.invoices i
CROSS JOIN LATERAL (
    SELECT (i.gross_total
            - coalesce((SELECT sum(c.gross_total) FROM invoices.invoices c
                        WHERE c.credits_invoice_id = i.id AND c.kind = 'credit_note' AND c.status = 'issued'), 0)
            - invoices.settled_amount(i.id))::numeric(14,2) AS open_amount
) o
CROSS JOIN LATERAL (
    SELECT c.id, c.issue_date FROM invoices.invoices c
    WHERE c.credits_invoice_id = i.id AND c.kind = 'credit_note' AND c.status = 'issued'
    ORDER BY c.issue_date DESC, c.number DESC
    LIMIT 1
) cn
WHERE i.kind = 'invoice' AND i.status = 'issued' AND o.open_amount < 0
ORDER BY i.id;

-- +goose Down
-- Credit is bookkeeping: the Down refuses while a movement, a refund, a
-- merge or a payment of source credit exists (00182's shape).
-- +goose StatementBegin
DO $down$
BEGIN
    IF EXISTS (SELECT 1 FROM invoices.customer_credits)
       OR EXISTS (SELECT 1 FROM invoices.credit_refunds)
       OR EXISTS (SELECT 1 FROM invoices.credit_merges)
       OR EXISTS (SELECT 1 FROM invoices.payments WHERE source = 'credit') THEN
        RAISE EXCEPTION 'invoices: customer credit exists' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.bank_transaction_events WHERE event = 'kept_as_credit') THEN
        RAISE EXCEPTION 'invoices: a bank line was kept as credit' USING ERRCODE = 'P0001';
    END IF;
END;
$down$;
-- +goose StatementEnd

ALTER TABLE invoices.bank_transaction_events
    DROP CONSTRAINT ck_bank_transaction_events_event,
    ADD CONSTRAINT ck_bank_transaction_events_event CHECK (event IN ('matched', 'queued', 'applied', 'dismissed',
        'reversal_handled', 'reopened', 'duplicate_confirmed', 'treated_as_distinct', 'reversed'));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.publish_payment_received() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    inv invoices.invoices%ROWTYPE;
BEGIN
    SELECT * INTO inv FROM invoices.invoices WHERE id = NEW.invoice_id;
    INSERT INTO platform.events (name, version, module, aggregate, aggregate_id, payload, occurred_at)
    VALUES ('invoices.payment.received', 1, 'invoices', 'invoices.payment', NEW.id::text,
            jsonb_build_object(
                'paymentId', NEW.id, 'invoiceId', NEW.invoice_id, 'number', coalesce(inv.number, 0),
                'customerId', inv.customer_id, 'amount', to_char(NEW.amount, 'FM999999999990.00'), 'currency', NEW.currency,
                'paidOn', to_char(NEW.paid_on, 'YYYY-MM-DD'), 'source', NEW.source, 'registeredAt', NEW.registered_at),
            NEW.registered_at);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER tr_credit_merges_changed ON invoices.credit_merges;
DROP TRIGGER tr_customer_credits_changed ON invoices.customer_credits;
DROP FUNCTION invoices.publish_customer_credit_changed();
DROP TRIGGER tr_credit_merges_journal ON invoices.credit_merges;
DROP TRIGGER tr_customer_credits_journal ON invoices.customer_credits;
DROP FUNCTION invoices.record_credit_fact();

-- No credit fact exists (the movements' and merges' absence above), so the
-- log's rows all name an invoice again.
ALTER TABLE invoices.journal_facts
    DROP CONSTRAINT ck_journal_facts_customer,
    DROP CONSTRAINT ck_journal_facts_invoice,
    DROP CONSTRAINT ck_journal_facts_kind,
    ADD CONSTRAINT ck_journal_facts_kind CHECK (kind IN (
        'payment', 'payment_removed', 'charge_payment', 'charge_payment_removed',
        'write_off', 'write_off_undone', 'recovered_payment', 'recovered_payment_removed')),
    DROP COLUMN customer_id,
    ALTER COLUMN invoice_id SET NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.settled_amount(p_invoice_id bigint)
RETURNS numeric LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT (coalesce((SELECT sum(p.amount) FROM invoices.payments p
                      WHERE p.invoice_id = p_invoice_id AND p.removed_at IS NULL AND p.kind = 'payment'), 0)
          + coalesce((SELECT w.amount FROM invoices.write_offs w
                      WHERE w.invoice_id = p_invoice_id AND w.undone_at IS NULL), 0))::numeric(14,2)
$$;
-- +goose StatementEnd
DROP FUNCTION invoices.moved_amount(bigint);

ALTER TABLE invoices.payments
    DROP CONSTRAINT ck_payments_origin,
    DROP CONSTRAINT ck_payments_source;
ALTER TABLE invoices.payments
    ADD CONSTRAINT ck_payments_source CHECK (source IN ('manual', 'ocr', 'camt054', 'vipps', 'bank_feed')),
    ADD CONSTRAINT ck_payments_origin CHECK (
        (source = 'manual' AND bank_transaction_id IS NULL AND registered_by_user_id IS NOT NULL
            AND payment_attempt_id IS NULL)
        OR (source IN ('ocr', 'camt054') AND bank_transaction_id IS NOT NULL AND registered_by_user_id IS NOT NULL
            AND payment_attempt_id IS NULL)
        OR (source = 'vipps' AND payment_attempt_id IS NOT NULL AND bank_transaction_id IS NULL
            AND registered_by_user_id IS NULL)
        OR (source = 'bank_feed' AND bank_transaction_id IS NOT NULL AND payment_attempt_id IS NULL));

-- The two amount_nok columns go. Dropping a column needs no write to the
-- rows, so the strict triggers stand as they are: their bodies are 00035's
-- and 00182's already.
DROP TRIGGER tr_write_offs_amount_nok ON invoices.write_offs;
DROP TRIGGER tr_payments_amount_nok ON invoices.payments;
DROP FUNCTION invoices.guard_payment_amount_nok();
ALTER TABLE invoices.write_offs DROP COLUMN amount_nok;
ALTER TABLE invoices.payments DROP COLUMN amount_nok;

DROP TABLE invoices.credit_merges;
DROP FUNCTION invoices.refuse_credit_merge_change();
DROP TABLE invoices.customer_credits;
DROP FUNCTION invoices.guard_customer_credit_insert();
DROP FUNCTION invoices.refuse_customer_credit_change();
DROP TABLE invoices.credit_refunds;
DROP FUNCTION invoices.guard_credit_refund_insert();
DROP FUNCTION invoices.refuse_credit_refund_change();
