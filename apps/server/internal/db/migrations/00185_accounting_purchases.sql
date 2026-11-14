-- +goose Up
-- Accounting, purchases (accounting design A17–A20 as amended by R2, R5, R8;
-- delivery C): suppliers, supplier invoices with their lines and files, the
-- payments made on them, the expense rules' deductibility, and what Needs
-- attention learns for Expenses' facts. Nothing is seeded here.

-- A supplier (A17): this module's own party, numbered gap-free from the
-- counter row 'supplier' (accounting.counters, as vouchers are). A supplier
-- who is a person (a sole proprietor) is personal data; is_person says so,
-- and the bookkeeping keeps them for five years after their last voucher.
-- bank_account is a Norwegian account number of eleven digits (mod 11, as
-- Invoices checks the seller's); iban and bic are a foreign one's. A supplier
-- with postings is never deleted (the postings' and open items' keys below):
-- it is made inactive.
CREATE TABLE accounting.suppliers (
    id                  bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number              integer      NOT NULL,
    name                varchar(200) NOT NULL,
    organisation_number varchar(20),
    country             char(2)      NOT NULL DEFAULT 'NO',
    address_line        varchar(300),
    postal_code         varchar(20),
    city                varchar(100),
    email               varchar(254),
    bank_account        varchar(11),
    iban                varchar(34),
    bic                 varchar(11),
    payment_terms_days  smallint     NOT NULL DEFAULT 14,
    default_account_id  integer      REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    default_tax_code_id integer      REFERENCES accounting.tax_codes (id) ON DELETE RESTRICT,
    currency            char(3)      NOT NULL DEFAULT 'NOK',
    is_person           boolean      NOT NULL DEFAULT false,
    active              boolean      NOT NULL DEFAULT true,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL,
    revision            integer      NOT NULL DEFAULT 1,
    CONSTRAINT ck_suppliers_number CHECK (number >= 1),
    CONSTRAINT ck_suppliers_name CHECK (btrim(name) <> ''),
    CONSTRAINT ck_suppliers_country CHECK (country ~ '^[A-Z]{2}$'),
    CONSTRAINT ck_suppliers_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_suppliers_bank_account CHECK (bank_account IS NULL OR bank_account ~ '^[0-9]{11}$'),
    CONSTRAINT ck_suppliers_terms CHECK (payment_terms_days BETWEEN 0 AND 365)
);
CREATE UNIQUE INDEX ux_suppliers_number ON accounting.suppliers (number);
CREATE UNIQUE INDEX ux_suppliers_organisation_number ON accounting.suppliers (organisation_number)
    WHERE organisation_number IS NOT NULL;
CREATE INDEX ix_suppliers_name ON accounting.suppliers (lower(name));

-- A posting's and an open item's supplier is a supplier of this table from
-- now on. NOT VALID: a manual journal of delivery A may have named a supplier
-- id before the table existed; every new row is held to it, and a supplier
-- with postings or items cannot be deleted.
ALTER TABLE accounting.postings
    ADD CONSTRAINT fk_postings_supplier FOREIGN KEY (supplier_id)
        REFERENCES accounting.suppliers (id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE accounting.open_items
    ADD CONSTRAINT fk_open_items_supplier FOREIGN KEY (supplier_id)
        REFERENCES accounting.suppliers (id) ON DELETE RESTRICT NOT VALID;

-- The currency a posting is in beside its NOK amount (R8): set together, on
-- a foreign-currency purchase's postings, and NULL on every NOK one.
ALTER TABLE accounting.postings
    ADD CONSTRAINT ck_postings_currency CHECK (
        (currency IS NULL) = (currency_amount IS NULL) AND (currency IS NULL) = (exchange_rate IS NULL));

-- A supplier invoice or credit note (A18, R8). A draft is no voucher: it is
-- edited and deleted freely. Post makes its voucher (voucher_id) and its open
-- item on payables (open_item_id); paid is derived from that item's balance,
-- never stored. Reverse and copy reverses the voucher (reversal_voucher_id),
-- marks the invoice reversed and makes a new draft with the same lines and
-- files (copied_from_id on the copy). The same supplier's number of the same
-- kind is refused twice among the invoices that are not reversed
-- (ux_supplier_invoices_number, 409 duplicate_supplier_invoice).
-- exchange_rate is NOK per unit of currency (1 for NOK); total is the
-- invoice's total in its currency, total_nok in NOK. A credit note may name
-- the invoice it credits, whose open item it lands on.
-- source_expense_entry_id is the expense of Expenses' supplier_invoice kind
-- the draft was made from (R2), an opaque id.
CREATE TABLE accounting.supplier_invoices (
    id                      bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    supplier_id             bigint        NOT NULL REFERENCES accounting.suppliers (id) ON DELETE RESTRICT,
    kind                    varchar(12)   NOT NULL DEFAULT 'invoice',
    supplier_number         varchar(60)   NOT NULL,
    invoice_date            date          NOT NULL,
    due_date                date,
    currency                char(3)       NOT NULL DEFAULT 'NOK',
    exchange_rate           numeric(12,6) NOT NULL DEFAULT 1,
    total                   numeric(14,2) NOT NULL,
    total_nok               numeric(14,2) NOT NULL,
    kid                     varchar(25),
    reference               varchar(100),
    description             varchar(500)  NOT NULL DEFAULT '',
    state                   varchar(10)   NOT NULL DEFAULT 'draft',
    credits_invoice_id      bigint        REFERENCES accounting.supplier_invoices (id) ON DELETE RESTRICT,
    voucher_id              bigint        REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    open_item_id            bigint        REFERENCES accounting.open_items (id) ON DELETE RESTRICT,
    reversal_voucher_id     bigint        REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    copied_from_id          bigint        REFERENCES accounting.supplier_invoices (id) ON DELETE SET NULL,
    source_expense_entry_id bigint,
    created_at              timestamptz   NOT NULL,
    created_by_user_id      uuid          NOT NULL,
    updated_at              timestamptz   NOT NULL,
    posted_at               timestamptz,
    posted_by_user_id       uuid,
    revision                integer       NOT NULL DEFAULT 1,
    CONSTRAINT ck_supplier_invoices_kind CHECK (kind IN ('invoice', 'credit_note')),
    CONSTRAINT ck_supplier_invoices_state CHECK (state IN ('draft', 'posted', 'reversed')),
    CONSTRAINT ck_supplier_invoices_number CHECK (btrim(supplier_number) <> ''),
    CONSTRAINT ck_supplier_invoices_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_supplier_invoices_rate CHECK (exchange_rate > 0 AND (currency <> 'NOK' OR exchange_rate = 1)),
    CONSTRAINT ck_supplier_invoices_totals CHECK (total >= 0 AND total_nok >= 0),
    CONSTRAINT ck_supplier_invoices_credits CHECK (credits_invoice_id IS NULL OR kind = 'credit_note'),
    CONSTRAINT ck_supplier_invoices_posted CHECK (
        (state = 'draft') = (voucher_id IS NULL) AND (state = 'draft') = (posted_at IS NULL)
        AND (state = 'reversed') = (reversal_voucher_id IS NOT NULL))
);
CREATE UNIQUE INDEX ux_supplier_invoices_number
    ON accounting.supplier_invoices (supplier_id, kind, lower(btrim(supplier_number))) WHERE state <> 'reversed';
CREATE INDEX ix_supplier_invoices_state ON accounting.supplier_invoices (state, due_date);
CREATE INDEX ix_supplier_invoices_supplier ON accounting.supplier_invoices (supplier_id);
CREATE UNIQUE INDEX ux_supplier_invoices_expense ON accounting.supplier_invoices (source_expense_entry_id)
    WHERE source_expense_entry_id IS NOT NULL AND state <> 'reversed';

-- A line of a supplier invoice (A18): the account, the tax code, the amount
-- as entered in the invoice's currency — net or gross (entry) — the project
-- (Projects' id) and a text. Edited with its draft only.
CREATE TABLE accounting.supplier_invoice_lines (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    supplier_invoice_id bigint        NOT NULL REFERENCES accounting.supplier_invoices (id) ON DELETE CASCADE,
    position            integer       NOT NULL,
    account_id          integer       NOT NULL REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    tax_code_id         integer       REFERENCES accounting.tax_codes (id) ON DELETE RESTRICT,
    amount              numeric(14,2) NOT NULL,
    entry               varchar(5)    NOT NULL DEFAULT 'gross',
    project_id          integer,
    text                varchar(500)  NOT NULL DEFAULT '',
    CONSTRAINT ck_supplier_invoice_lines_entry CHECK (entry IN ('net', 'gross')),
    CONSTRAINT ck_supplier_invoice_lines_position CHECK (position >= 1)
);
CREATE UNIQUE INDEX ux_supplier_invoice_lines_position ON accounting.supplier_invoice_lines (supplier_invoice_id, position);

-- A supplier invoice's files: the invoice itself as received. A draft's file
-- may be removed; at posting each becomes the voucher's attachment
-- (accounting.attachments, the same object), and Reverse and copy gives the
-- new draft the same objects. An object is deleted from the store only when
-- no file and no attachment names it any more.
CREATE TABLE accounting.supplier_invoice_files (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    supplier_invoice_id bigint        NOT NULL REFERENCES accounting.supplier_invoices (id) ON DELETE CASCADE,
    object_key          varchar(200)  NOT NULL,
    file_name           varchar(255)  NOT NULL,
    content_type        varchar(100)  NOT NULL,
    size_bytes          bigint        NOT NULL,
    sha256              char(64)      NOT NULL,
    uploaded_at         timestamptz   NOT NULL,
    uploaded_by_user_id uuid          NOT NULL,
    CONSTRAINT ck_supplier_invoice_files_size CHECK (size_bytes > 0)
);
CREATE INDEX ix_supplier_invoice_files_invoice ON accounting.supplier_invoice_files (supplier_invoice_id);
CREATE INDEX ix_supplier_invoice_files_object ON accounting.supplier_invoice_files (object_key);

-- A payment of a supplier invoice (A19, R5, R8): the day, what the bank took
-- in NOK, the share of the invoice it settles in the invoice's currency, the
-- bank's ledger account, and its voucher. Removing it, while its period is
-- open, posts the reversal (removal_voucher_id) and keeps the row.
CREATE TABLE accounting.supplier_payments (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    supplier_invoice_id bigint        NOT NULL REFERENCES accounting.supplier_invoices (id) ON DELETE RESTRICT,
    paid_on             date          NOT NULL,
    amount_nok          numeric(14,2) NOT NULL,
    amount_currency     numeric(14,2) NOT NULL,
    bank_account_id     integer       NOT NULL REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    voucher_id          bigint        NOT NULL REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    removed_at          timestamptz,
    removed_by_user_id  uuid,
    removal_voucher_id  bigint        REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    created_at          timestamptz   NOT NULL,
    created_by_user_id  uuid          NOT NULL,
    CONSTRAINT ck_supplier_payments_amounts CHECK (amount_nok > 0 AND amount_currency > 0),
    CONSTRAINT ck_supplier_payments_removed CHECK ((removed_at IS NULL) = (removal_voucher_id IS NULL))
);
CREATE INDEX ix_supplier_payments_invoice ON accounting.supplier_payments (supplier_invoice_id);

-- An expense rule (A14, R2) says whether the VAT of its category may be
-- deducted (mval. § 8-3 forbids it for entertainment and welfare); NULL on a
-- sales rule. A change of it is a new version, as a change of name or
-- account is, and each version keeps it.
ALTER TABLE accounting.posting_rules
    ADD COLUMN vat_deductible boolean,
    ADD CONSTRAINT ck_posting_rules_vat_deductible CHECK ((scope = 'expense') = (vat_deductible IS NOT NULL));
ALTER TABLE accounting.posting_rule_versions ADD COLUMN vat_deductible boolean;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION accounting.record_posting_rule_version()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.version <> OLD.version THEN
        INSERT INTO accounting.posting_rule_versions (rule_id, version, name, account_id, valid_from, vat_deductible)
        VALUES (NEW.id, NEW.version, NEW.name, NEW.account_id, NEW.updated_at, NEW.vat_deductible);
    END IF;
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

-- Needs attention for Expenses' facts (R2). order_key is what an expense
-- fact waits behind — 'entry:<id>' or 'claim:<id>' — as invoice_id is for a
-- sales fact (R15). informational marks a row that did post but says
-- something a person should see (vat_not_deducted): it never blocks and is
-- not counted with the rows that wait. resolved_at closes a row that will
-- never post — informational and acknowledged, or an expense of the
-- supplier_invoice kind made into a supplier invoice (supplier_invoice_id) —
-- and nok_amounts holds the amounts in NOK a person entered for an expense
-- in another currency, per entry ({"<entry id>": "123.45"}).
ALTER TABLE accounting.unposted
    ADD COLUMN order_key           varchar(60),
    ADD COLUMN informational       boolean      NOT NULL DEFAULT false,
    ADD COLUMN resolved_at         timestamptz,
    ADD COLUMN supplier_invoice_id bigint       REFERENCES accounting.supplier_invoices (id) ON DELETE SET NULL,
    ADD COLUMN nok_amounts         jsonb,
    ADD CONSTRAINT ck_unposted_informational CHECK (NOT informational OR posted_voucher_id IS NOT NULL);
CREATE INDEX ix_unposted_open_order_key ON accounting.unposted (order_key, id)
    WHERE posted_at IS NULL AND resolved_at IS NULL;

-- +goose Down
-- Suppliers and their invoices are bookkeeping: the Down refuses while a
-- supplier exists, as 00180's does while the sales journal holds a fact.
-- +goose StatementBegin
DO $down$
BEGIN
    IF EXISTS (SELECT 1 FROM accounting.suppliers) THEN
        RAISE EXCEPTION 'accounting: the books hold suppliers' USING ERRCODE = 'P0001';
    END IF;
END;
$down$;
-- +goose StatementEnd

DROP INDEX accounting.ix_unposted_open_order_key;
ALTER TABLE accounting.unposted
    DROP CONSTRAINT ck_unposted_informational,
    DROP COLUMN nok_amounts,
    DROP COLUMN supplier_invoice_id,
    DROP COLUMN resolved_at,
    DROP COLUMN informational,
    DROP COLUMN order_key;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION accounting.record_posting_rule_version()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.version <> OLD.version THEN
        INSERT INTO accounting.posting_rule_versions (rule_id, version, name, account_id, valid_from)
        VALUES (NEW.id, NEW.version, NEW.name, NEW.account_id, NEW.updated_at);
    END IF;
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd
ALTER TABLE accounting.posting_rule_versions DROP COLUMN vat_deductible;
ALTER TABLE accounting.posting_rules
    DROP CONSTRAINT ck_posting_rules_vat_deductible,
    DROP COLUMN vat_deductible;
DROP TABLE accounting.supplier_payments;
DROP TABLE accounting.supplier_invoice_files;
DROP TABLE accounting.supplier_invoice_lines;
DROP TABLE accounting.supplier_invoices;
ALTER TABLE accounting.postings DROP CONSTRAINT ck_postings_currency;
ALTER TABLE accounting.open_items DROP CONSTRAINT fk_open_items_supplier;
ALTER TABLE accounting.postings DROP CONSTRAINT fk_postings_supplier;
DROP TABLE accounting.suppliers;
