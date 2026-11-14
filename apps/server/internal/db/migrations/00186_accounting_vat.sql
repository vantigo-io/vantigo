-- +goose Up
-- Accounting's VAT terms (accounting design §5, delivery D: A21–A23, as
-- amended by R10). The terms themselves are derived from the settings'
-- vat_term — six two-month terms a calendar year, or one — starting with the
-- term that holds books_start; a row is written here when a term is first
-- closed, and keeps what the close made: the figures per code (the return as
-- a snapshot), the XML in Skatteetaten's skattemeldingformerverdiavgift v1.0
-- schema, the settlement voucher, who closed it and when. A person fills in
-- filed_at, filed_reference and filed_by_user_id after filing by hand at
-- Skatteetaten; a filed term is not reopened, and the lock never moves back
-- behind it. The VAT specification (A21) is computed from the postings and
-- never stored.
--
-- Every close, reopen, filing and unfiling is written to vat_term_events,
-- append-only like the lock's history, with the voucher it posted and the
-- reason a reopening or an unfiling gives.
CREATE TABLE accounting.vat_terms (
    id                    bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    year                  integer      NOT NULL,
    term                  integer      NOT NULL,
    term_kind             varchar(10)  NOT NULL,
    period_start          date         NOT NULL,
    period_end            date         NOT NULL,
    state                 varchar(6)   NOT NULL,
    -- closings counts the closes, so each close's settlement voucher and its
    -- reversal have a source of their own (A4's source index).
    closings              integer      NOT NULL DEFAULT 0,
    figures               jsonb,
    return_xml            text,
    -- NULL on a closed term whose VAT accounts did not move: nothing to settle.
    settlement_voucher_id bigint       REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    closed_at             timestamptz,
    closed_by_user_id     uuid,
    filed_at              date,
    filed_reference       varchar(100),
    filed_by_user_id      uuid,
    updated_at            timestamptz  NOT NULL,
    CONSTRAINT ck_vat_terms_kind CHECK (term_kind IN ('bimonthly', 'yearly')),
    CONSTRAINT ck_vat_terms_term CHECK (
        (term_kind = 'bimonthly' AND term BETWEEN 1 AND 6) OR (term_kind = 'yearly' AND term = 1)),
    CONSTRAINT ck_vat_terms_year CHECK (year BETWEEN 2000 AND 2100),
    CONSTRAINT ck_vat_terms_period CHECK (period_end > period_start),
    CONSTRAINT ck_vat_terms_state CHECK (state IN ('open', 'closed')),
    CONSTRAINT ck_vat_terms_closed CHECK (
        (state = 'closed') = (figures IS NOT NULL)
        AND (state = 'closed') = (return_xml IS NOT NULL)
        AND (state = 'closed') = (closed_at IS NOT NULL)
        AND (state = 'closed') = (closed_by_user_id IS NOT NULL)
        AND (state = 'closed' OR settlement_voucher_id IS NULL)),
    CONSTRAINT ck_vat_terms_filed CHECK (
        (filed_at IS NULL) = (filed_reference IS NULL)
        AND (filed_at IS NULL) = (filed_by_user_id IS NULL)
        AND (filed_at IS NULL OR state = 'closed'))
);
CREATE UNIQUE INDEX ux_vat_terms_term ON accounting.vat_terms (year, term);

-- What happened to a term, in order: closed (with its settlement voucher),
-- reopened (with the reversal and the reason), filed (the day and the
-- reference) and unfiled (the reason).
CREATE TABLE accounting.vat_term_events (
    id              bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vat_term_id     bigint       NOT NULL REFERENCES accounting.vat_terms (id) ON DELETE RESTRICT,
    kind            varchar(10)  NOT NULL,
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

CREATE TRIGGER tr_vat_term_events_immutable
    BEFORE UPDATE OR DELETE ON accounting.vat_term_events
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_vat_term_events_no_truncate
    BEFORE TRUNCATE ON accounting.vat_term_events
    FOR EACH STATEMENT EXECUTE FUNCTION accounting.refuse_change();

-- +goose Down
DROP TABLE accounting.vat_term_events;
DROP TABLE accounting.vat_terms;
