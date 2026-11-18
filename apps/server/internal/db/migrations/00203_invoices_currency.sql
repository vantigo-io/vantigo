-- +goose Up
-- Invoices in a foreign currency (invoices phase 6 design I17–I28; plan
-- docs/superpowers/plans/2026-11-18-invoices-phase6-c-plan.md, delivery C,
-- and its readings). NOK stays the functional currency: every figure the
-- ledger reads is in NOK, and a document in another currency carries the
-- rate it was issued at and its NOK snapshots.

-- Norges Bank's rates and the ones a manager entered (I18): NOK per one
-- unit, one row per currency and day, at ten decimals — VND and IDR are
-- worth a few hundredths of an øre a unit, and six would lose a tenth of a
-- per cent of them. A manual row is never replaced by a
-- fetch (reading 4): the fetch's upsert skips it, and the trigger below
-- refuses it in SQL too.
CREATE TABLE invoices.exchange_rates (
    currency           char(3)       NOT NULL,
    rate_date          date          NOT NULL,
    rate               numeric(18,10) NOT NULL,
    source             varchar(12)   NOT NULL,
    fetched_at         timestamptz   NOT NULL,
    entered_by_user_id uuid,
    PRIMARY KEY (currency, rate_date),
    CONSTRAINT ck_exchange_rates_rate CHECK (rate > 0),
    CONSTRAINT ck_exchange_rates_source CHECK (source IN ('norges_bank', 'manual')),
    CONSTRAINT ck_exchange_rates_actor CHECK ((source = 'manual') = (entered_by_user_id IS NOT NULL)),
    CONSTRAINT ck_exchange_rates_currency CHECK (currency ~ '^[A-Z]{3}$' AND currency <> 'NOK')
);

-- A fetched row may be refetched, and a manual row replaced or removed by a
-- manager; a fetch never touches a manual row, and a fetched row is never
-- deleted.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_exchange_rate_overwrite()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.source <> 'manual' THEN
            RAISE EXCEPTION 'invoices: a fetched exchange rate is never deleted' USING ERRCODE = 'P0001';
        END IF;
        RETURN OLD;
    END IF;
    IF OLD.source = 'manual' AND NEW.source <> 'manual' THEN
        RAISE EXCEPTION 'invoices: a manual exchange rate is never overwritten by a fetch' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_exchange_rates_overwrite BEFORE UPDATE OR DELETE ON invoices.exchange_rates
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_exchange_rate_overwrite();

-- invoices.exchange_rate.fetched (spec §7): a currency's rate of a day was
-- written, fetched or entered. No personal data.
-- +goose StatementBegin
CREATE FUNCTION invoices.publish_exchange_rate_fetched() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO platform.events (name, version, module, aggregate, aggregate_id, payload, occurred_at)
    VALUES ('invoices.exchange_rate.fetched', 1, 'invoices', 'invoices.exchange_rate',
            NEW.currency || ':' || to_char(NEW.rate_date, 'YYYY-MM-DD'),
            jsonb_build_object('currency', NEW.currency, 'day', to_char(NEW.rate_date, 'YYYY-MM-DD'),
                'source', NEW.source),
            NEW.fetched_at);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER tr_exchange_rates_fetched AFTER INSERT OR UPDATE ON invoices.exchange_rates
    FOR EACH ROW EXECUTE FUNCTION invoices.publish_exchange_rate_fetched();

-- One row per enabled currency: the invoices-rates worker's lease and the
-- settings page's last fetch. Both lease columns or neither, the bank feed
-- account's shape.
CREATE TABLE invoices.rate_fetches (
    currency        char(3)      PRIMARY KEY,
    lease_id        varchar(32),
    lease_until     timestamptz,
    last_attempt_at timestamptz,
    last_success_at timestamptz,
    last_error      varchar(200),
    CONSTRAINT ck_rate_fetches_lease CHECK ((lease_id IS NULL) = (lease_until IS NULL)),
    CONSTRAINT ck_rate_fetches_currency CHECK (currency ~ '^[A-Z]{3}$' AND currency <> 'NOK')
);

-- The currencies a document may be in besides NOK (I17).
ALTER TABLE invoices.settings
    ADD COLUMN currencies char(3)[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT ck_settings_currencies CHECK (NOT ('NOK' = ANY (currencies))
        AND array_to_string(currencies, ',') ~ '^([A-Z]{3}(,[A-Z]{3})*)?$');

-- A document's rate at the same ten decimals (the baseline's six, fixed at 1
-- for every NOK document). Changing the type rewrites the table without a
-- row trigger, so refuse_issued_document_change needs no allowance.
ALTER TABLE invoices.invoices ALTER COLUMN exchange_rate TYPE numeric(18,10);

-- Where a document's rate came from (I19): nok for NOK, else Norges Bank's
-- or one a person entered. A constant default writes no row, so
-- refuse_issued_document_change needs no allowance; its jsonb comparison
-- freezes the column from now on.
ALTER TABLE invoices.invoices
    ADD COLUMN exchange_rate_source varchar(12) NOT NULL DEFAULT 'nok',
    ADD CONSTRAINT ck_invoices_exchange_rate_source CHECK (
        exchange_rate_source IN ('nok', 'norges_bank', 'manual')
        AND (currency = 'NOK') = (exchange_rate_source = 'nok')
        AND (currency <> 'NOK' OR exchange_rate = 1));

-- The NOK snapshots of a document's VAT rows and lines (I21, reading 2):
-- written at issue while the document is still a draft, so
-- refuse_issued_child_change needs no change. NULL is a document issued
-- before this — every one NOK — whose reader takes the currency figure.
ALTER TABLE invoices.vat_summaries ADD COLUMN taxable_amount_nok numeric(14,2);
ALTER TABLE invoices.lines ADD COLUMN line_net_nok numeric(14,2);

-- What a payment's NOK differs from the NOK receivable it cleared (I24,
-- reading 5): positive a gain. 0 for NOK. A constant default writes no row,
-- and refuse_payment_change's jsonb comparison freezes it.
ALTER TABLE invoices.payments
    ADD COLUMN exchange_difference_nok numeric(14,2) NOT NULL DEFAULT 0,
    ADD CONSTRAINT ck_payments_exchange_difference CHECK (currency <> 'NOK' OR exchange_difference_nok = 0);

-- A refund in currency (reading 9): what the credit was carried at less the
-- NOK that left.
ALTER TABLE invoices.credit_refunds
    ADD COLUMN exchange_difference_nok numeric(14,2) NOT NULL DEFAULT 0,
    ADD CONSTRAINT ck_credit_refunds_exchange_difference CHECK (currency <> 'NOK' OR exchange_difference_nok = 0);

-- The NOK rest a credit note leaves when it closes an invoice in another
-- currency (review M1): the currency open amount reaches 0 while the NOK
-- receivable keeps an øre or two of rounding, which no payment will ever
-- clear. The issue writes the rest here — positive a receivable that never
-- comes, a currency loss; negative a gain — and Accounting posts it to
-- 8160 or 8060 from its fact. The credit note's own VAT figures are left as
-- they are. Immutable.
CREATE TABLE invoices.exchange_differences (
    id              bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id      bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    document_id     bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    difference_day  date          NOT NULL,
    amount_nok      numeric(14,2) NOT NULL,
    registered_at   timestamptz   NOT NULL,
    CONSTRAINT ck_exchange_differences_amount CHECK (amount_nok <> 0)
);
CREATE UNIQUE INDEX ux_exchange_differences_document ON invoices.exchange_differences (document_id);
CREATE INDEX ix_exchange_differences_invoice ON invoices.exchange_differences (invoice_id);

-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_exchange_difference_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    RAISE EXCEPTION 'invoices: an exchange difference is never changed' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_exchange_differences_immutable BEFORE UPDATE OR DELETE ON invoices.exchange_differences
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_exchange_difference_change();

-- Its journal fact, numbered at commit as every fact is (00180).
ALTER TABLE invoices.journal_facts
    DROP CONSTRAINT ck_journal_facts_kind,
    ADD CONSTRAINT ck_journal_facts_kind CHECK (kind IN (
        'payment', 'payment_removed', 'charge_payment', 'charge_payment_removed',
        'write_off', 'write_off_undone', 'recovered_payment', 'recovered_payment_removed',
        'credit', 'credit_merged', 'exchange_difference'));

-- +goose StatementBegin
CREATE FUNCTION invoices.record_exchange_difference_fact()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    next_seq bigint;
BEGIN
    INSERT INTO invoices.counters (counter_name, next_value)
    VALUES ('journal', 2)
    ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
    RETURNING next_value - 1 INTO next_seq;
    INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, business_date, created_at)
    VALUES (next_seq, 'exchange_difference', NEW.id, NEW.invoice_id, NEW.difference_day, now());
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER tr_exchange_differences_journal
    AFTER INSERT ON invoices.exchange_differences
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION invoices.record_exchange_difference_fact();

-- A document's NOK (I21): Σ BaseNOK, Σ VATNOK and their sum, the
-- receivable in NOK — never the gross times the rate. 0 for a draft, which
-- has no VAT rows.
-- +goose StatementBegin
CREATE FUNCTION invoices.document_nok(p_invoice_id bigint, OUT base_nok numeric, OUT vat_nok numeric, OUT gross_nok numeric)
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(sum(coalesce(v.taxable_amount_nok, v.taxable_amount)), 0)::numeric(14,2),
           coalesce(sum(v.vat_amount_nok), 0)::numeric(14,2),
           (coalesce(sum(coalesce(v.taxable_amount_nok, v.taxable_amount)), 0)
            + coalesce(sum(v.vat_amount_nok), 0))::numeric(14,2)
    FROM invoices.vat_summaries v
    WHERE v.invoice_id = p_invoice_id
$$;
-- +goose StatementEnd

-- The NOK receivable still open on an invoice (I24, reading 5): its NOK
-- less its issued credit notes', less what the live payments cleared
-- (amount_nok less the exchange difference), less the standing write-off's
-- NOK, less the NOK rest a closing credit note left (exchange_differences),
-- plus the NOK of what moved to the customer's credit. For NOK it is
-- settled_amount()'s open amount.
-- +goose StatementBegin
CREATE FUNCTION invoices.open_nok(p_invoice_id bigint)
RETURNS numeric LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT ((invoices.document_nok(p_invoice_id)).gross_nok
          - coalesce((SELECT sum((invoices.document_nok(c.id)).gross_nok) FROM invoices.invoices c
                      WHERE c.credits_invoice_id = p_invoice_id AND c.kind = 'credit_note' AND c.status = 'issued'), 0)
          - coalesce((SELECT sum(p.amount_nok - p.exchange_difference_nok) FROM invoices.payments p
                      WHERE p.invoice_id = p_invoice_id AND p.removed_at IS NULL AND p.kind = 'payment'), 0)
          - coalesce((SELECT w.amount_nok FROM invoices.write_offs w
                      WHERE w.invoice_id = p_invoice_id AND w.undone_at IS NULL), 0)
          - coalesce((SELECT sum(x.amount_nok) FROM invoices.exchange_differences x
                      WHERE x.invoice_id = p_invoice_id), 0)
          + coalesce((SELECT sum(c.amount_nok) FROM invoices.customer_credits c
                      WHERE c.invoice_id = p_invoice_id
                        AND (c.kind = 'credit_note'
                             OR (c.kind = 'reversed' AND EXISTS (
                                 SELECT 1 FROM invoices.customer_credits o
                                 WHERE o.id = c.reverses_id AND o.kind = 'credit_note')))), 0))::numeric(14,2)
$$;
-- +goose StatementEnd

-- A line whose KID names an invoice in another currency (I24): matching
-- never pays it, and a person applies it with the NOK share.
ALTER TABLE invoices.bank_transactions
    DROP CONSTRAINT ck_bank_transactions_reason,
    ADD CONSTRAINT ck_bank_transactions_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed',
        'invoice_written_off', 'currency_differs'));
ALTER TABLE invoices.bank_transaction_events
    DROP CONSTRAINT ck_bank_transaction_events_reason,
    ADD CONSTRAINT ck_bank_transaction_events_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed',
        'invoice_written_off', 'currency_differs'));

-- +goose Down
-- A document, a payment, a write-off or a credit row in another currency,
-- or a rate, is bookkeeping the older schema cannot hold: the Down refuses
-- while any exists (00197's shape).
-- +goose StatementBegin
DO $down$
BEGIN
    IF EXISTS (SELECT 1 FROM invoices.invoices WHERE currency <> 'NOK')
       OR EXISTS (SELECT 1 FROM invoices.payments WHERE currency <> 'NOK')
       OR EXISTS (SELECT 1 FROM invoices.write_offs WHERE currency <> 'NOK')
       OR EXISTS (SELECT 1 FROM invoices.customer_credits WHERE currency <> 'NOK')
       OR EXISTS (SELECT 1 FROM invoices.credit_refunds WHERE currency <> 'NOK') THEN
        RAISE EXCEPTION 'invoices: documents or money in another currency than NOK exist' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.exchange_differences) THEN
        RAISE EXCEPTION 'invoices: exchange differences exist' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.exchange_rates) THEN
        RAISE EXCEPTION 'invoices: exchange rates exist' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.bank_transactions WHERE reason = 'currency_differs')
       OR EXISTS (SELECT 1 FROM invoices.bank_transaction_events WHERE reason = 'currency_differs') THEN
        RAISE EXCEPTION 'invoices: a bank line was queued for another currency' USING ERRCODE = 'P0001';
    END IF;
END;
$down$;
-- +goose StatementEnd

ALTER TABLE invoices.bank_transaction_events
    DROP CONSTRAINT ck_bank_transaction_events_reason,
    ADD CONSTRAINT ck_bank_transaction_events_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed',
        'invoice_written_off'));
ALTER TABLE invoices.bank_transactions
    DROP CONSTRAINT ck_bank_transactions_reason,
    ADD CONSTRAINT ck_bank_transactions_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed',
        'invoice_written_off'));

DROP FUNCTION invoices.open_nok(bigint);
DROP FUNCTION invoices.document_nok(bigint);

DROP TRIGGER tr_exchange_differences_journal ON invoices.exchange_differences;
DROP FUNCTION invoices.record_exchange_difference_fact();
ALTER TABLE invoices.journal_facts
    DROP CONSTRAINT ck_journal_facts_kind,
    ADD CONSTRAINT ck_journal_facts_kind CHECK (kind IN (
        'payment', 'payment_removed', 'charge_payment', 'charge_payment_removed',
        'write_off', 'write_off_undone', 'recovered_payment', 'recovered_payment_removed',
        'credit', 'credit_merged'));
DROP TABLE invoices.exchange_differences;
DROP FUNCTION invoices.refuse_exchange_difference_change();

-- Dropping a column writes no row, so the strict triggers stand as they are.
ALTER TABLE invoices.credit_refunds
    DROP CONSTRAINT ck_credit_refunds_exchange_difference,
    DROP COLUMN exchange_difference_nok;
ALTER TABLE invoices.payments
    DROP CONSTRAINT ck_payments_exchange_difference,
    DROP COLUMN exchange_difference_nok;
ALTER TABLE invoices.lines DROP COLUMN line_net_nok;
ALTER TABLE invoices.vat_summaries DROP COLUMN taxable_amount_nok;
ALTER TABLE invoices.invoices
    DROP CONSTRAINT ck_invoices_exchange_rate_source,
    DROP COLUMN exchange_rate_source;
-- Every document is NOK by the refusal above, its rate 1.
ALTER TABLE invoices.invoices ALTER COLUMN exchange_rate TYPE numeric(14,6);
ALTER TABLE invoices.settings
    DROP CONSTRAINT ck_settings_currencies,
    DROP COLUMN currencies;

DROP TABLE invoices.rate_fetches;
DROP TRIGGER tr_exchange_rates_fetched ON invoices.exchange_rates;
DROP FUNCTION invoices.publish_exchange_rate_fetched();
DROP TABLE invoices.exchange_rates;
DROP FUNCTION invoices.refuse_exchange_rate_overwrite();
