-- +goose Up
-- Invoices, the sales document (invoices foundation design D2-D4, D9): the
-- whole phase 1A schema at once, so no later task of the delivery adds a
-- migration. customer_id is opaque — the customer lives in another schema
-- (docs/module-boundaries.md rule 4) and is read through
-- contracts.CustomerDirectory. Unlike 00012's house style this schema carries
-- CHECK constraints: an issued document is bookkeeping material, and the rules
-- that make it lawful are the database's to hold as well as the module's (D9).
-- No column is named with a word PostgreSQL reserves; a schema test pins it.
CREATE SCHEMA invoices;

-- btree_gist supplies the "=" operator class the rate periods' exclusion
-- needs (D3). 00005 already created it; repeated so this schema stands alone.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- The seller record and the series start (D2): one row, id 1, inserted below
-- with empty values. Every text column is NOT NULL with '' for "not set", so
-- "is the seller complete" is a question about empty strings, never NULLs.
CREATE TABLE invoices.settings (
    id                         smallint     PRIMARY KEY,
    legal_name                 varchar(200) NOT NULL DEFAULT '',
    organisation_number        varchar(9)   NOT NULL DEFAULT '',
    vat_registered             boolean      NOT NULL DEFAULT false,
    in_foretaksregisteret      boolean      NOT NULL DEFAULT false,
    address_line1              varchar(200) NOT NULL DEFAULT '',
    address_line2              varchar(200) NOT NULL DEFAULT '',
    postal_code                varchar(20)  NOT NULL DEFAULT '',
    city                       varchar(100) NOT NULL DEFAULT '',
    country                    char(2)      NOT NULL DEFAULT 'NO',
    bank_account               varchar(11)  NOT NULL DEFAULT '',
    iban                       varchar(34)  NOT NULL DEFAULT '',
    bic                        varchar(11)  NOT NULL DEFAULT '',
    email                      varchar(254) NOT NULL DEFAULT '',
    default_payment_terms_days integer      NOT NULL DEFAULT 14,
    default_currency           char(3)      NOT NULL DEFAULT 'NOK',
    footer_text                varchar(500) NOT NULL DEFAULT '',
    series_start               bigint       NOT NULL DEFAULT 1,
    updated_at                 timestamptz  NOT NULL,
    revision                   integer      NOT NULL DEFAULT 1,
    CONSTRAINT ck_settings_single_row CHECK (id = 1),
    CONSTRAINT ck_settings_series_start CHECK (series_start >= 1),
    CONSTRAINT ck_settings_payment_terms CHECK (default_payment_terms_days BETWEEN 0 AND 365)
);

-- The number series (D2): the counter-row primitive customers uses
-- (00003_customers_baseline.sql), not a SEQUENCE, because a sequence burns a
-- value on rollback. next_value is the next number nobody has taken, as in
-- projects. There is one row, 'documents', and it exists exactly when
-- something has been issued: "anything issued" is "the row exists", never a
-- count(*).
CREATE TABLE invoices.counters (
    counter_name text   PRIMARY KEY,
    next_value   bigint NOT NULL
);

-- The VAT codes (D3): the tenant's label, the SAF-T standard tax code and the
-- UNCL5305 category. The rate is not here: it is a dated period in
-- vat_code_rates, so a rate change is a new period on the same code. A code is
-- never deleted; active = false stops offering it for new lines.
CREATE TABLE invoices.vat_codes (
    id               integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    code             varchar(10)  NOT NULL,
    name             varchar(100) NOT NULL,
    saf_t_code       varchar(5)   NOT NULL,
    ehf_category     varchar(2)   NOT NULL,
    exemption_reason varchar(200),
    active           boolean      NOT NULL DEFAULT true,
    created_at       timestamptz  NOT NULL,
    updated_at       timestamptz  NOT NULL,
    revision         integer      NOT NULL DEFAULT 1,
    CONSTRAINT ck_vat_codes_category CHECK (ehf_category IN ('S', 'Z', 'E', 'AE', 'G', 'O', 'K')),
    CONSTRAINT ck_vat_codes_exemption_reason
        CHECK (ehf_category = 'S' OR (exemption_reason IS NOT NULL AND exemption_reason <> ''))
);
CREATE UNIQUE INDEX ux_vat_codes_code_lower ON invoices.vat_codes (lower(code));

-- A code's rates as periods (D3). valid_to NULL is open-ended; no two periods
-- of one code overlap, inclusive at both ends — the energy supply periods'
-- exclusion is the precedent (00005).
CREATE TABLE invoices.vat_code_rates (
    id           integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    vat_code_id  integer      NOT NULL REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    rate_percent numeric(5,2) NOT NULL,
    valid_from   date         NOT NULL,
    valid_to     date,
    created_at   timestamptz  NOT NULL,
    CONSTRAINT ck_vat_code_rates_period CHECK (valid_to IS NULL OR valid_to >= valid_from),
    CONSTRAINT ex_vat_code_rates_no_overlap EXCLUDE USING gist (
        vat_code_id WITH =,
        daterange(valid_from, valid_to, '[]') WITH &&
    )
);

-- The document (D4): a draft until it is issued into an immutable, numbered
-- salgsdokument. kind is 'invoice' or 'credit_note'; a draft has no number and
-- no issue date; an issued one carries both, the buyer snapshot, the seller
-- snapshot and its totals, and from then on the trigger below refuses any
-- change but the merge holder's customer_id and the PDF set once.
CREATE TABLE invoices.invoices (
    id                     bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind                   varchar(20)   NOT NULL,
    status                 varchar(20)   NOT NULL DEFAULT 'draft',
    number                 bigint,
    customer_id            integer       NOT NULL,
    credits_invoice_id     bigint        REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    issue_date             date,
    delivery_date          date,
    delivery_from          date,
    delivery_to            date,
    delivery_address_line1 varchar(200),
    delivery_address_line2 varchar(200),
    delivery_postal_code   varchar(20),
    delivery_city          varchar(100),
    delivery_country       char(2),
    payment_terms_days     integer,
    due_date               date,
    currency               char(3)       NOT NULL DEFAULT 'NOK',
    exchange_rate          numeric(14,6) NOT NULL DEFAULT 1,
    exchange_rate_date     date,
    your_reference         varchar(100)  NOT NULL DEFAULT '',
    our_reference          varchar(100)  NOT NULL DEFAULT '',
    order_reference        varchar(100)  NOT NULL DEFAULT '',
    note                   varchar(1000) NOT NULL DEFAULT '',
    internal_note          varchar(1000) NOT NULL DEFAULT '',
    -- The buyer snapshot (D4), written at issue from the billing profile — on a
    -- credit note, copied from the original when the draft is made. Each width
    -- is at least the customers module's own (a name and an address line are
    -- up to 255 there), so no valid customer can fail an issue on length.
    buyer_customer_number     bigint,
    buyer_type                varchar(20),
    buyer_name                varchar(255),
    buyer_organisation_number varchar(9),
    buyer_foreign_id          varchar(60),
    buyer_address_line1       varchar(255),
    buyer_address_line2       varchar(255),
    buyer_postal_code         varchar(20),
    buyer_city                varchar(100),
    buyer_region              varchar(100),
    buyer_country             char(2),
    buyer_peppol_id           varchar(100),
    buyer_gln                 varchar(13),
    buyer_language            varchar(2),
    -- The seller snapshot (D4), copied from invoices.settings at issue.
    seller_legal_name            varchar(200),
    seller_organisation_number   varchar(9),
    seller_vat_registered        boolean,
    seller_in_foretaksregisteret boolean,
    seller_address_line1         varchar(200),
    seller_address_line2         varchar(200),
    seller_postal_code           varchar(20),
    seller_city                  varchar(100),
    seller_country               char(2),
    seller_bank_account          varchar(11),
    seller_iban                  varchar(34),
    seller_bic                   varchar(11),
    seller_email                 varchar(254),
    seller_footer_text           varchar(500),
    net_total          numeric(14,2) NOT NULL DEFAULT 0,
    vat_total          numeric(14,2) NOT NULL DEFAULT 0,
    gross_total        numeric(14,2) NOT NULL DEFAULT 0,
    vat_total_nok      numeric(14,2) NOT NULL DEFAULT 0,
    pdf_object_key     varchar(300),
    pdf_sha256         char(64),
    issued_at          timestamptz,
    issued_by_user_id  uuid,
    created_by_user_id uuid          NOT NULL,
    created_at         timestamptz   NOT NULL,
    updated_at         timestamptz   NOT NULL,
    revision           integer       NOT NULL DEFAULT 1,
    CONSTRAINT ck_invoices_kind CHECK (kind IN ('invoice', 'credit_note')),
    CONSTRAINT ck_invoices_status CHECK (status IN ('draft', 'issued')),
    CONSTRAINT ck_invoices_number CHECK ((status = 'draft') = (number IS NULL)),
    CONSTRAINT ck_invoices_issue_date CHECK ((status = 'draft') = (issue_date IS NULL)),
    CONSTRAINT ck_invoices_credits CHECK ((kind = 'credit_note') = (credits_invoice_id IS NOT NULL)),
    CONSTRAINT ck_invoices_delivery CHECK (
        (delivery_date IS NOT NULL AND delivery_from IS NULL AND delivery_to IS NULL)
        OR (delivery_date IS NULL AND delivery_from IS NOT NULL AND delivery_to IS NOT NULL
            AND delivery_from <= delivery_to)
        OR (delivery_date IS NULL AND delivery_from IS NULL AND delivery_to IS NULL)
    ),
    CONSTRAINT ck_invoices_payment_terms CHECK (payment_terms_days IS NULL OR payment_terms_days BETWEEN 0 AND 365),
    -- A credit note has no payment terms and no due date (D4).
    CONSTRAINT ck_invoices_credit_note_terms CHECK (kind = 'invoice' OR (payment_terms_days IS NULL AND due_date IS NULL)),
    CONSTRAINT ck_invoices_exchange_rate CHECK (exchange_rate > 0),
    -- The PDF's key and hash are one fact, set together once (D7): a hash
    -- without the key it names would be a stored PDF nobody can find.
    CONSTRAINT ck_invoices_pdf CHECK ((pdf_object_key IS NULL) = (pdf_sha256 IS NULL))
);
CREATE UNIQUE INDEX ux_invoices_number ON invoices.invoices (number);
CREATE INDEX ix_invoices_customer ON invoices.invoices (customer_id);
CREATE INDEX ix_invoices_credits ON invoices.invoices (credits_invoice_id) WHERE credits_invoice_id IS NOT NULL;
CREATE INDEX ix_invoices_issue_date ON invoices.invoices (issue_date) WHERE status = 'issued';

-- A document's lines (D4, D5). line_gross, line_allowance and line_net are
-- computed on every save; the VAT columns are the issue snapshot and NULL on a
-- draft. credits_line_id is, on a credit note's line, the original line it
-- credits.
CREATE TABLE invoices.lines (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id       bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE CASCADE,
    position         integer       NOT NULL,
    description      varchar(500)  NOT NULL,
    quantity         numeric(12,3) NOT NULL,
    unit             varchar(20)   NOT NULL DEFAULT '',
    unit_price       numeric(14,4) NOT NULL,
    discount_percent numeric(5,2)  NOT NULL DEFAULT 0,
    vat_code_id      integer       NOT NULL REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    credits_line_id  bigint        REFERENCES invoices.lines (id) ON DELETE RESTRICT,
    line_gross       numeric(14,2) NOT NULL,
    line_allowance   numeric(14,2) NOT NULL,
    line_net         numeric(14,2) NOT NULL,
    vat_rate_percent numeric(5,2),
    vat_category     varchar(2),
    saf_t_code       varchar(5),
    exemption_reason varchar(200),
    CONSTRAINT ck_lines_position CHECK (position >= 1),
    CONSTRAINT ck_lines_quantity CHECK (quantity > 0),
    CONSTRAINT ck_lines_unit_price CHECK (unit_price >= 0),
    CONSTRAINT ck_lines_discount CHECK (discount_percent BETWEEN 0 AND 100)
);
CREATE UNIQUE INDEX ux_lines_invoice_position ON invoices.lines (invoice_id, position);
CREATE INDEX ix_lines_vat_code ON invoices.lines (vat_code_id);
CREATE INDEX ix_lines_credits_line ON invoices.lines (credits_line_id) WHERE credits_line_id IS NOT NULL;

-- VAT per (category, rate) of an issued document (D4, D5), a row for each
-- 0 % category too (§ 5-1-5).
CREATE TABLE invoices.vat_summaries (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id       bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE CASCADE,
    vat_category     varchar(2)    NOT NULL,
    rate_percent     numeric(5,2)  NOT NULL,
    saf_t_code       varchar(5)    NOT NULL,
    exemption_reason varchar(200),
    taxable_amount   numeric(14,2) NOT NULL,
    vat_amount       numeric(14,2) NOT NULL,
    vat_amount_nok   numeric(14,2) NOT NULL
);
CREATE UNIQUE INDEX ux_vat_summaries_invoice_rate ON invoices.vat_summaries (invoice_id, vat_category, rate_percent);

-- Immutability, in SQL too (D9). An issued document is refused a DELETE, and
-- an UPDATE unless the only columns that differ are customer_id (the merge
-- holder, D10) and pdf_object_key / pdf_sha256 going from NULL to a value
-- once. The comparison is of the whole row as jsonb less those three, so a
-- column added later is covered without touching this function.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_issued_document_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF OLD.status <> 'issued' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(NEW) - 'customer_id' - 'pdf_object_key' - 'pdf_sha256')
           IS DISTINCT FROM (to_jsonb(OLD) - 'customer_id' - 'pdf_object_key' - 'pdf_sha256')
       OR (OLD.pdf_object_key IS NOT NULL AND NEW.pdf_object_key IS DISTINCT FROM OLD.pdf_object_key)
       OR (OLD.pdf_sha256 IS NOT NULL AND NEW.pdf_sha256 IS DISTINCT FROM OLD.pdf_sha256) THEN
        RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_invoices_immutable
    BEFORE UPDATE OR DELETE ON invoices.invoices
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_document_change();

-- A line or a VAT summary under an issued document is refused any write. A
-- parent that is not there is a cascade from deleting a draft (the parent's
-- own trigger has already refused deleting an issued one), and is allowed.
-- An UPDATE is judged against both the old and the new parent, so a row
-- cannot be moved out from under an issued document either.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_issued_child_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') AND EXISTS (
        SELECT 1 FROM invoices.invoices WHERE id = OLD.invoice_id AND status = 'issued'
    ) THEN
        RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') AND EXISTS (
        SELECT 1 FROM invoices.invoices WHERE id = NEW.invoice_id AND status = 'issued'
    ) THEN
        RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_lines_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.lines
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_child_change();

CREATE TRIGGER tr_vat_summaries_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.vat_summaries
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_child_change();

-- The single settings row, with empty values: the seller is incomplete until
-- somebody with invoices:manage fills it in.
INSERT INTO invoices.settings (id, updated_at) VALUES (1, now());

-- The seeded VAT codes (D3), each with one open period from 2026-01-01. The
-- ids are fixed, 1-9, below the identity's 1001, so a later code never
-- collides with them. 6 is E (unntatt, mval. kap. 3); 7 is O, for a seller
-- outside the VAT register.
INSERT INTO invoices.vat_codes (id, code, name, saf_t_code, ehf_category, exemption_reason, active, created_at, updated_at)
OVERRIDING SYSTEM VALUE VALUES
    (1, '3',  'Utgående mva 25 %',        '3',  'S',  NULL, true, now(), now()),
    (2, '31', 'Utgående mva 15 %',        '31', 'S',  NULL, true, now(), now()),
    (3, '32', 'Utgående mva 11,11 %',     '32', 'S',  NULL, true, now(), now()),
    (4, '33', 'Utgående mva 12 %',        '33', 'S',  NULL, true, now(), now()),
    (5, '5',  'Fritatt innenlands 0 %',   '5',  'Z',  'Fritatt for merverdiavgift', true, now(), now()),
    (6, '51', 'Omvendt avgiftsplikt 0 %', '51', 'AE', 'Omvendt avgiftsplikt – Merverdiavgift ikke beregnet', true, now(), now()),
    (7, '52', 'Utførsel 0 %',             '52', 'G',  'Utførsel av varer og tjenester', true, now(), now()),
    (8, '6',  'Utenfor mva-loven 0 %',    '6',  'E',  'Unntatt fra merverdiavgift (mval. kap. 3)', true, now(), now()),
    (9, '7',  'Ingen mva-behandling',     '7',  'O',  'Selger er ikke registrert i Merverdiavgiftsregisteret', true, now(), now())
ON CONFLICT DO NOTHING;

INSERT INTO invoices.vat_code_rates (vat_code_id, rate_percent, valid_from, valid_to, created_at) VALUES
    (1, 25.00, DATE '2026-01-01', NULL, now()),
    (2, 15.00, DATE '2026-01-01', NULL, now()),
    (3, 11.11, DATE '2026-01-01', NULL, now()),
    (4, 12.00, DATE '2026-01-01', NULL, now()),
    (5, 0,     DATE '2026-01-01', NULL, now()),
    (6, 0,     DATE '2026-01-01', NULL, now()),
    (7, 0,     DATE '2026-01-01', NULL, now()),
    (8, 0,     DATE '2026-01-01', NULL, now()),
    (9, 0,     DATE '2026-01-01', NULL, now())
ON CONFLICT DO NOTHING;

-- +goose Down
-- The schema only: btree_gist stays, energy's exclusion needs it.
DROP SCHEMA invoices CASCADE;
