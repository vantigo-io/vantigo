-- +goose Up
-- Accounting's VAT terms (accounting design §5, delivery D: A21–A23, as
-- amended by R10). The terms themselves are derived from the settings'
-- vat_term — six two-month terms a calendar year, or one — starting with the
-- term that holds books_start; a row is written here when a term is first
-- closed. Each close is kept in vat_term_closings, never changed: the figures
-- per code (the return as a snapshot), the XML in Skatteetaten's
-- skattemeldingformerverdiavgift v1.0 schema, the settlement voucher, who
-- closed it and when. A person fills in filed_at, filed_reference and
-- filed_by_user_id after filing by hand at Skatteetaten, naming the closing
-- filed; a filed term is not reopened, and the lock never moves back behind
-- a closed term. The VAT specification (A21) is computed from the postings and
-- never stored.
--
-- Every close, reopen, filing and unfiling is written to vat_term_events,
-- append-only like the lock's history, with the voucher it posted and the
-- reason a reopening or an unfiling gives.
CREATE TABLE accounting.vat_terms (
    id                 bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    year               integer      NOT NULL,
    term               integer      NOT NULL,
    term_kind          varchar(10)  NOT NULL,
    period_start       date         NOT NULL,
    period_end         date         NOT NULL,
    state              varchar(6)   NOT NULL,
    -- closings counts the closes, so each close's settlement voucher and its
    -- reversal have a source of their own (A4's source index).
    closings           integer      NOT NULL DEFAULT 0,
    -- current_closing_id is the closing that stands while the term is closed;
    -- NULL while it is open. Every closing is kept in vat_term_closings.
    current_closing_id bigint,
    filed_at           date,
    filed_reference    varchar(100),
    filed_by_user_id   uuid,
    -- filed_closing_id is the closing a person filed at Skatteetaten.
    filed_closing_id   bigint,
    updated_at         timestamptz  NOT NULL,
    CONSTRAINT ck_vat_terms_kind CHECK (term_kind IN ('bimonthly', 'yearly')),
    CONSTRAINT ck_vat_terms_term CHECK (
        (term_kind = 'bimonthly' AND term BETWEEN 1 AND 6) OR (term_kind = 'yearly' AND term = 1)),
    CONSTRAINT ck_vat_terms_year CHECK (year BETWEEN 2000 AND 2100),
    CONSTRAINT ck_vat_terms_period CHECK (period_end > period_start),
    CONSTRAINT ck_vat_terms_state CHECK (state IN ('open', 'closed')),
    CONSTRAINT ck_vat_terms_closed CHECK ((state = 'closed') = (current_closing_id IS NOT NULL)),
    CONSTRAINT ck_vat_terms_filed CHECK (
        (filed_at IS NULL) = (filed_reference IS NULL)
        AND (filed_at IS NULL) = (filed_by_user_id IS NULL)
        AND (filed_at IS NULL) = (filed_closing_id IS NULL)
        AND (filed_at IS NULL OR (state = 'closed' AND filed_closing_id = current_closing_id)))
);
-- A term is its kind's: a yearly term 1 and a two-month term 1 of one year
-- are different terms (and the books' vat_term never changes once a term is
-- closed, below).
CREATE UNIQUE INDEX ux_vat_terms_term ON accounting.vat_terms (year, term, term_kind);

-- Every close of a term (A22, A23): the return as it was closed, its XML and
-- its settlement voucher (NULL when nothing moved), who and when. Never
-- changed or deleted: a reopening leaves it, and a filed return stays where
-- it was filed.
CREATE TABLE accounting.vat_term_closings (
    id                    bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    vat_term_id           bigint       NOT NULL REFERENCES accounting.vat_terms (id) ON DELETE RESTRICT,
    closing               integer      NOT NULL,
    figures               jsonb        NOT NULL,
    return_xml            text         NOT NULL,
    settlement_voucher_id bigint       REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    closed_at             timestamptz  NOT NULL,
    closed_by_user_id     uuid         NOT NULL,
    CONSTRAINT ck_vat_term_closings_closing CHECK (closing >= 1)
);
CREATE UNIQUE INDEX ux_vat_term_closings_closing ON accounting.vat_term_closings (vat_term_id, closing);

ALTER TABLE accounting.vat_terms
    ADD CONSTRAINT fk_vat_terms_current_closing
        FOREIGN KEY (current_closing_id) REFERENCES accounting.vat_term_closings (id) ON DELETE RESTRICT,
    ADD CONSTRAINT fk_vat_terms_filed_closing
        FOREIGN KEY (filed_closing_id) REFERENCES accounting.vat_term_closings (id) ON DELETE RESTRICT;

-- What happened to a term, in order: closed (with its closing and settlement
-- voucher), reopened (with the reversal and the reason), filed (the closing,
-- the day and the reference) and unfiled (the reason).
CREATE TABLE accounting.vat_term_events (
    id              bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vat_term_id     bigint       NOT NULL REFERENCES accounting.vat_terms (id) ON DELETE RESTRICT,
    kind            varchar(10)  NOT NULL,
    closing_id      bigint       REFERENCES accounting.vat_term_closings (id) ON DELETE RESTRICT,
    voucher_id      bigint       REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    reason          varchar(500),
    filed_at        date,
    filed_reference varchar(100),
    at              timestamptz  NOT NULL,
    by_user_id      uuid         NOT NULL,
    CONSTRAINT ck_vat_term_events_kind CHECK (kind IN ('closed', 'reopened', 'filed', 'unfiled')),
    CONSTRAINT ck_vat_term_events_reason CHECK (kind NOT IN ('reopened', 'unfiled') OR reason IS NOT NULL)
);
CREATE INDEX ix_vat_term_events_term ON accounting.vat_term_events (vat_term_id, id);

CREATE TRIGGER tr_vat_term_closings_immutable
    BEFORE UPDATE OR DELETE ON accounting.vat_term_closings
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_vat_term_closings_no_truncate
    BEFORE TRUNCATE ON accounting.vat_term_closings
    FOR EACH STATEMENT EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_vat_term_events_immutable
    BEFORE UPDATE OR DELETE ON accounting.vat_term_events
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_vat_term_events_no_truncate
    BEFORE TRUNCATE ON accounting.vat_term_events
    FOR EACH STATEMENT EXECUTE FUNCTION accounting.refuse_change();

-- The books' VAT term never changes once a term has been closed: the terms
-- and their returns are derived from it.
-- +goose StatementBegin
CREATE FUNCTION accounting.freeze_vat_term()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.vat_term IS DISTINCT FROM OLD.vat_term AND EXISTS (SELECT 1 FROM accounting.vat_terms) THEN
        RAISE EXCEPTION 'accounting: a VAT term has been closed; the books'' VAT term is fixed' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_settings_freeze_vat_term
    BEFORE UPDATE ON accounting.settings
    FOR EACH ROW EXECUTE FUNCTION accounting.freeze_vat_term();

-- +goose Down
DROP TRIGGER tr_settings_freeze_vat_term ON accounting.settings;
DROP FUNCTION accounting.freeze_vat_term();
DROP TABLE accounting.vat_term_events;
ALTER TABLE accounting.vat_terms DROP CONSTRAINT fk_vat_terms_current_closing, DROP CONSTRAINT fk_vat_terms_filed_closing;
DROP TABLE accounting.vat_term_closings;
DROP TABLE accounting.vat_terms;
