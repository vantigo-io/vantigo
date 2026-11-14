-- +goose Up
-- Accounting, the company's books (accounting design §2, delivery A, as
-- amended by §13): the settings row, the voucher counters, the chart with its
-- grouping codes, the tax codes and their rates, vouchers and their postings,
-- the attachments, the party ledgers' open items, and the lock's history.
-- Nothing is seeded here: Set up the books (R12) writes the settings row and
-- seeds the chart, the tax codes and the system roles, so an upgrade that
-- switches the module on changes nothing until a person does.
--
-- Every foreign identifier is opaque (module-boundaries rule 4): a customer is
-- Customers' integer id, a project Projects' integer id, a person identity's
-- uuid. A supplier is this module's own bigint id; its table arrives with
-- delivery C, so supplier_id is a plain column until then. No key crosses a
-- schema.
--
-- The database keeps the ledger's rules itself (A4, R10, R13), so no code path
-- can write books that do not balance:
--   * a voucher's postings sum to zero, and a voucher has postings — two
--     deferred constraint triggers, checked at commit;
--   * vouchers, postings and attachments are never updated, deleted or
--     truncated, and a posting is added only to the voucher its own
--     transaction has just inserted;
--   * a voucher is dated on or after books_start and after locked_through,
--     read from settings FOR SHARE, so a lock move (which takes the row FOR
--     UPDATE) and a post cannot pass each other.
CREATE SCHEMA accounting;

-- The one settings row (A2, R12). It exists exactly when the books are set up.
-- books_start is the first day the ledger covers; fiscal_year_start_month the
-- month a fiscal year begins; vat_term the VAT terms (delivery D); and
-- locked_through the last day of the last locked month (A8), NULL before the
-- first lock. chart_language is the language the default chart was seeded in.
-- opening_as_of_seq is the lowest of the Invoices journal facts the opening's
-- pages of open invoices reflect (SalesJournal.OpenAt's AsOfSeq per page),
-- NULL when the opening was not entered with Invoices present. Delivery B
-- posts a fact about an invoice with an opening item (open_items'
-- opening_as_of_seq) when it is dated on or after books_start or its seq is
-- after that item's value; about any other invoice, with this one.
CREATE TABLE accounting.settings (
    id                      smallint    PRIMARY KEY DEFAULT 1,
    books_start             date        NOT NULL,
    fiscal_year_start_month smallint    NOT NULL DEFAULT 1,
    vat_term                varchar(10) NOT NULL,
    locked_through          date,
    chart_language          varchar(2)  NOT NULL,
    opening_as_of_seq       bigint,
    set_up_at               timestamptz NOT NULL,
    set_up_by_user_id       uuid        NOT NULL,
    updated_at              timestamptz NOT NULL,
    CONSTRAINT ck_settings_single_row CHECK (id = 1),
    CONSTRAINT ck_settings_fiscal_month CHECK (fiscal_year_start_month BETWEEN 1 AND 12),
    CONSTRAINT ck_settings_vat_term CHECK (vat_term IN ('bimonthly', 'yearly', 'none')),
    CONSTRAINT ck_settings_language CHECK (chart_language IN ('en', 'nb')),
    CONSTRAINT ck_settings_lock_month_end
        CHECK (locked_through IS NULL
            OR locked_through = (date_trunc('month', locked_through) + interval '1 month - 1 day')::date),
    CONSTRAINT ck_settings_lock_after_start CHECK (locked_through IS NULL OR locked_through >= books_start)
);

-- The voucher number series (A4): Invoices' counter-row primitive, not a
-- SEQUENCE, because a sequence burns a value on rollback. One row per fiscal
-- year, named 'voucher:<year>', made by the year's first voucher; next_value
-- is the next number nobody has taken. The row is held from the allocation to
-- the commit, so a failed post gives its number back.
CREATE TABLE accounting.counters (
    counter_name varchar(30) PRIMARY KEY,
    next_value   bigint      NOT NULL
);

-- The chart (A3). number is 4 to 6 digits and never changes once the account
-- has a posting, nor does kind; name may. An account with postings is never
-- deleted (the postings' RESTRICT), only made inactive. system_role names the
-- accounts the module posts to by itself; a role moves between accounts and
-- is never removed (the module's rule).
CREATE TABLE accounting.accounts (
    id                  integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number              varchar(6)   NOT NULL,
    name                varchar(200) NOT NULL,
    kind                varchar(10)  NOT NULL,
    default_tax_code_id integer,
    active              boolean      NOT NULL DEFAULT true,
    system_role         varchar(30),
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL,
    revision            integer      NOT NULL DEFAULT 1,
    CONSTRAINT ck_accounts_number CHECK (number ~ '^[0-9]{4,6}$'),
    CONSTRAINT ck_accounts_name CHECK (btrim(name) <> ''),
    CONSTRAINT ck_accounts_kind CHECK (kind IN ('asset', 'liability', 'equity', 'income', 'expense')),
    CONSTRAINT ck_accounts_role CHECK (system_role IN (
        'receivables', 'payables', 'bank', 'vat_settlement', 'rounding', 'bad_debt', 'employee_payables',
        'opening', 'year_result', 'clearing_vipps', 'charge_income',
        'currency_gain', 'currency_loss', 'payment_fees'))
);
CREATE UNIQUE INDEX ux_accounts_number ON accounting.accounts (number);
CREATE UNIQUE INDEX ux_accounts_system_role ON accounting.accounts (system_role) WHERE system_role IS NOT NULL;

-- An account's grouping per year (A3, R13): SAF-T's category and code, kept
-- historically. The row with the greatest from_year not after a year is the
-- account's grouping that year; an account with none exports NA.
CREATE TABLE accounting.account_groupings (
    account_id integer      NOT NULL REFERENCES accounting.accounts (id) ON DELETE CASCADE,
    from_year  integer      NOT NULL,
    category   varchar(100) NOT NULL,
    code       varchar(20)  NOT NULL,
    PRIMARY KEY (account_id, from_year),
    CONSTRAINT ck_account_groupings_year CHECK (from_year BETWEEN 2000 AND 2100)
);

-- The tax codes (A5): the SAF-T standard code, its direction, whether it is
-- reverse charge, the VAT account it posts to (and, for reverse charge, the
-- counter account of the other side), and whether the VAT return reports it
-- (false for 0, 7, 20, 21 and 22). A code is never deleted.
CREATE TABLE accounting.tax_codes (
    id                     integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    code                   varchar(5)   NOT NULL,
    name                   varchar(200) NOT NULL,
    direction              varchar(6)   NOT NULL,
    reverse_charge         boolean      NOT NULL DEFAULT false,
    vat_account_id         integer      REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    counter_vat_account_id integer      REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    reported               boolean      NOT NULL DEFAULT true,
    active                 boolean      NOT NULL DEFAULT true,
    created_at             timestamptz  NOT NULL,
    updated_at             timestamptz  NOT NULL,
    revision               integer      NOT NULL DEFAULT 1,
    CONSTRAINT ck_tax_codes_direction CHECK (direction IN ('output', 'input', 'none')),
    CONSTRAINT ck_tax_codes_counter CHECK (reverse_charge OR counter_vat_account_id IS NULL)
);
CREATE UNIQUE INDEX ux_tax_codes_code ON accounting.tax_codes (code);

ALTER TABLE accounting.accounts
    ADD CONSTRAINT fk_accounts_default_tax_code
        FOREIGN KEY (default_tax_code_id) REFERENCES accounting.tax_codes (id) ON DELETE RESTRICT;

-- A code's rates as dated periods (A5), Invoices' shape (00034): valid_to NULL
-- is open-ended, and no two periods of one code overlap.
CREATE TABLE accounting.tax_code_rates (
    id           integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    tax_code_id  integer      NOT NULL REFERENCES accounting.tax_codes (id) ON DELETE RESTRICT,
    rate_percent numeric(5,2) NOT NULL,
    valid_from   date         NOT NULL,
    valid_to     date,
    created_at   timestamptz  NOT NULL,
    CONSTRAINT ck_tax_code_rates_rate CHECK (rate_percent >= 0 AND rate_percent < 100),
    CONSTRAINT ck_tax_code_rates_period CHECK (valid_to IS NULL OR valid_to >= valid_from),
    CONSTRAINT ex_tax_code_rates_no_overlap EXCLUDE USING gist (
        tax_code_id WITH =,
        daterange(valid_from, valid_to, '[]') WITH &&
    )
);

-- A voucher (A4). number is gap-free per fiscal year; voucher_date is the day
-- it is booked on and document_date the day of what it records, which differ
-- when a system voucher meets the lock (A8). source_* name the fact a system
-- voucher records: one fact, one voucher, however often it is delivered (the
-- partial unique index; source_part is '' rather than NULL, so a NULL cannot
-- let a duplicate through, R13). A manual voucher has no source.
-- reverses_voucher_id names the voucher this one reverses: at most once.
CREATE TABLE accounting.vouchers (
    id                  bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    fiscal_year         integer      NOT NULL,
    number              integer      NOT NULL,
    voucher_date        date         NOT NULL,
    document_date       date         NOT NULL,
    description         varchar(500) NOT NULL,
    kind                varchar(30)  NOT NULL,
    source_module       varchar(30),
    source_kind         varchar(40),
    source_id           bigint,
    source_part         varchar(40)  NOT NULL DEFAULT '',
    rule                varchar(60),
    rule_version        integer,
    reverses_voucher_id bigint       REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    reversal_reason     varchar(500),
    posted_at           timestamptz  NOT NULL,
    posted_by_user_id   uuid,
    CONSTRAINT ck_vouchers_number CHECK (number >= 1),
    CONSTRAINT ck_vouchers_kind CHECK (kind IN (
        'manual', 'opening', 'sales_invoice', 'sales_credit_note', 'customer_payment', 'charge', 'write_off',
        'supplier_invoice', 'supplier_credit_note', 'supplier_payment', 'expense', 'reimbursement',
        'vat_settlement', 'bank', 'import')),
    CONSTRAINT ck_vouchers_source CHECK (
        (source_module IS NULL) = (source_kind IS NULL) AND (source_module IS NULL) = (source_id IS NULL)
        AND (source_module IS NOT NULL OR source_part = '')),
    CONSTRAINT ck_vouchers_rule CHECK ((rule IS NULL) = (rule_version IS NULL)),
    CONSTRAINT ck_vouchers_reversal CHECK ((reverses_voucher_id IS NULL) = (reversal_reason IS NULL))
);
CREATE UNIQUE INDEX ux_vouchers_number ON accounting.vouchers (fiscal_year, number);
CREATE UNIQUE INDEX ux_vouchers_source ON accounting.vouchers (source_module, source_kind, source_id, source_part)
    WHERE source_module IS NOT NULL;
CREATE UNIQUE INDEX ux_vouchers_reverses ON accounting.vouchers (reverses_voucher_id)
    WHERE reverses_voucher_id IS NOT NULL;
CREATE INDEX ix_vouchers_date ON accounting.vouchers (voucher_date);

-- A party ledger's open item (A10, R5): a customer's or a supplier's document
-- with its original amount; its balance is the sum of the postings that name
-- it, and it is settled when that is zero. source_document_id is the document
-- in Invoices it stands for, so a later payment finds it. A posting on
-- receivables or payables that names no item lands on the party's unallocated
-- item: one per party and kind. opening_as_of_seq is, for an item the opening
-- balances made for an invoice in Invoices, the AsOfSeq of the OpenAt page the
-- invoice was read on: a journal fact with a seq at or below it is already in
-- the opening amount (R14). NULL for every other item.
CREATE TABLE accounting.open_items (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind               varchar(10)   NOT NULL,
    customer_id        integer,
    supplier_id        bigint,
    unallocated        boolean       NOT NULL DEFAULT false,
    document_number    varchar(40),
    document_date      date,
    due_date           date,
    original_amount    numeric(14,2) NOT NULL DEFAULT 0,
    source_document_id bigint,
    opening_as_of_seq  bigint,
    created_at         timestamptz   NOT NULL,
    CONSTRAINT ck_open_items_kind CHECK (kind IN ('customer', 'supplier')),
    CONSTRAINT ck_open_items_party CHECK (
        (kind = 'customer' AND customer_id IS NOT NULL AND supplier_id IS NULL)
        OR (kind = 'supplier' AND supplier_id IS NOT NULL AND customer_id IS NULL)),
    CONSTRAINT ck_open_items_unallocated CHECK (
        NOT unallocated OR (document_number IS NULL AND source_document_id IS NULL AND original_amount = 0)),
    CONSTRAINT ck_open_items_opening_seq CHECK (opening_as_of_seq IS NULL OR source_document_id IS NOT NULL)
);
CREATE UNIQUE INDEX ux_open_items_unallocated
    ON accounting.open_items (kind, coalesce(customer_id::bigint, supplier_id)) WHERE unallocated;
CREATE UNIQUE INDEX ux_open_items_source_document
    ON accounting.open_items (kind, source_document_id) WHERE source_document_id IS NOT NULL;
CREATE INDEX ix_open_items_customer ON accounting.open_items (customer_id) WHERE customer_id IS NOT NULL;
CREATE INDEX ix_open_items_supplier ON accounting.open_items (supplier_id) WHERE supplier_id IS NOT NULL;

-- A posting (A4): one line of a voucher. amount is signed, debit positive and
-- credit negative, and never zero (R3). The currency columns stay NULL in this
-- phase so SAF-T's shape needs no later migration. tax_role says whether the
-- line is a tax code's base or its tax; a tax line carries the base it was
-- computed from. A posting names at most one party, and an open item only
-- with its party. project_id is Projects' id, the one dimension (R13).
CREATE TABLE accounting.postings (
    id              bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    voucher_id      bigint        NOT NULL REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    line_no         integer       NOT NULL,
    account_id      integer       NOT NULL REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    amount          numeric(14,2) NOT NULL,
    currency        varchar(3),
    currency_amount numeric(14,2),
    exchange_rate   numeric(12,6),
    tax_code_id     integer       REFERENCES accounting.tax_codes (id) ON DELETE RESTRICT,
    tax_role        varchar(4),
    tax_base        numeric(14,2),
    customer_id     integer,
    supplier_id     bigint,
    open_item_id    bigint        REFERENCES accounting.open_items (id) ON DELETE RESTRICT,
    project_id      integer,
    description     varchar(500)  NOT NULL DEFAULT '',
    CONSTRAINT ck_postings_amount CHECK (amount <> 0),
    CONSTRAINT ck_postings_line CHECK (line_no >= 1),
    CONSTRAINT ck_postings_tax CHECK ((tax_code_id IS NULL) = (tax_role IS NULL)),
    CONSTRAINT ck_postings_tax_role CHECK (tax_role IN ('base', 'tax')),
    CONSTRAINT ck_postings_tax_base CHECK (tax_base IS NULL OR tax_role = 'tax'),
    CONSTRAINT ck_postings_party CHECK (customer_id IS NULL OR supplier_id IS NULL),
    CONSTRAINT ck_postings_open_item CHECK (open_item_id IS NULL OR customer_id IS NOT NULL OR supplier_id IS NOT NULL)
);
CREATE UNIQUE INDEX ux_postings_line ON accounting.postings (voucher_id, line_no);
CREATE INDEX ix_postings_account ON accounting.postings (account_id);
CREATE INDEX ix_postings_open_item ON accounting.postings (open_item_id) WHERE open_item_id IS NOT NULL;
CREATE INDEX ix_postings_customer ON accounting.postings (customer_id) WHERE customer_id IS NOT NULL;
CREATE INDEX ix_postings_project ON accounting.postings (project_id) WHERE project_id IS NOT NULL;

-- A voucher's attachments (A4): kept in the object store under object_key, by
-- name, type, size and SHA-256. Added after posting too, never removed.
CREATE TABLE accounting.attachments (
    id                  bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    voucher_id          bigint       NOT NULL REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    object_key          varchar(200) NOT NULL,
    file_name           varchar(255) NOT NULL,
    content_type        varchar(100) NOT NULL,
    size_bytes          bigint       NOT NULL,
    sha256              char(64)     NOT NULL,
    uploaded_at         timestamptz  NOT NULL,
    uploaded_by_user_id uuid         NOT NULL,
    CONSTRAINT ck_attachments_size CHECK (size_bytes > 0)
);
CREATE UNIQUE INDEX ux_attachments_object_key ON accounting.attachments (object_key);
CREATE INDEX ix_attachments_voucher ON accounting.attachments (voucher_id);

-- Every move of the lock (A8, R10): forward with accounting:close, back with a
-- reason. This is the audit log the law asks for when closed books are
-- opened again; it is append-only.
CREATE TABLE accounting.lock_events (
    id               bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    locked_from      date,
    locked_to        date,
    reason           varchar(500),
    changed_at       timestamptz  NOT NULL,
    changed_by_user_id uuid       NOT NULL
);

-- +goose StatementBegin
CREATE FUNCTION accounting.refuse_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    RAISE EXCEPTION 'accounting: a row of % is never changed or deleted', TG_TABLE_NAME
        USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_vouchers_immutable
    BEFORE UPDATE OR DELETE ON accounting.vouchers
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_postings_immutable
    BEFORE UPDATE OR DELETE ON accounting.postings
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_attachments_immutable
    BEFORE UPDATE OR DELETE ON accounting.attachments
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_lock_events_immutable
    BEFORE UPDATE OR DELETE ON accounting.lock_events
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();
-- TRUNCATE fires no row trigger, so each append-only table refuses it as a
-- statement.
CREATE TRIGGER tr_vouchers_no_truncate
    BEFORE TRUNCATE ON accounting.vouchers
    FOR EACH STATEMENT EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_postings_no_truncate
    BEFORE TRUNCATE ON accounting.postings
    FOR EACH STATEMENT EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_attachments_no_truncate
    BEFORE TRUNCATE ON accounting.attachments
    FOR EACH STATEMENT EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_lock_events_no_truncate
    BEFORE TRUNCATE ON accounting.lock_events
    FOR EACH STATEMENT EXECUTE FUNCTION accounting.refuse_change();

-- The lock and the books' start (A4, A8, R10): read FOR SHARE, so a lock move,
-- which takes the row FOR UPDATE, waits for a post in flight and a post waits
-- for a lock move, and neither passes the other.
-- +goose StatementBegin
CREATE FUNCTION accounting.check_voucher_date()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    s accounting.settings%ROWTYPE;
BEGIN
    SELECT * INTO s FROM accounting.settings WHERE id = 1 FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'accounting: the books are not set up' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.voucher_date < s.books_start THEN
        RAISE EXCEPTION 'accounting: voucher date % is before the books start on %', NEW.voucher_date, s.books_start
            USING ERRCODE = 'P0001';
    END IF;
    IF s.locked_through IS NOT NULL AND NEW.voucher_date <= s.locked_through THEN
        RAISE EXCEPTION 'accounting: voucher date % is in the period locked through %', NEW.voucher_date, s.locked_through
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_vouchers_date
    BEFORE INSERT ON accounting.vouchers
    FOR EACH ROW EXECUTE FUNCTION accounting.check_voucher_date();

-- A posting is added only to the voucher its own transaction has just
-- inserted (A4, R10): never to a committed voucher, whose date the lock was
-- checked against when it was inserted and may since have passed. The vouchers'
-- insert trigger records the new voucher's id in a transaction-local setting,
-- accounting.open_voucher, and the postings' insert trigger refuses any other
-- voucher id. A setting rather than xmin = pg_current_xact_id(): a row
-- inserted inside a savepoint carries the subtransaction's xid, not the top
-- transaction's, and the engine may run inside one. set_config(..., true) is
-- subtransaction-aware: rolled back with a savepoint, kept when it is
-- released, gone at the transaction's end, so a pooled connection carries
-- nothing to its next transaction. The setting guards code paths; a session
-- that sets it by hand could as well drop the trigger.
-- +goose StatementBegin
CREATE FUNCTION accounting.mark_open_voucher()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    PERFORM set_config('accounting.open_voucher', NEW.id::text, true);
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_vouchers_open
    AFTER INSERT ON accounting.vouchers
    FOR EACH ROW EXECUTE FUNCTION accounting.mark_open_voucher();

-- +goose StatementBegin
CREATE FUNCTION accounting.check_posting_voucher()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.voucher_id::text IS DISTINCT FROM nullif(current_setting('accounting.open_voucher', true), '') THEN
        RAISE EXCEPTION 'accounting: voucher % was not inserted by this transaction; a posted voucher takes no new postings',
            NEW.voucher_id USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_postings_open_voucher
    BEFORE INSERT ON accounting.postings
    FOR EACH ROW EXECUTE FUNCTION accounting.check_posting_voucher();

-- A voucher's postings sum to zero (A4), checked at commit once its every
-- posting is in.
-- +goose StatementBegin
CREATE FUNCTION accounting.check_voucher_balance()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    total numeric;
BEGIN
    SELECT coalesce(sum(amount), 0) INTO total FROM accounting.postings WHERE voucher_id = NEW.voucher_id;
    IF total <> 0 THEN
        RAISE EXCEPTION 'accounting: the postings of voucher % sum to %, not zero', NEW.voucher_id, total
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER tr_postings_balance
    AFTER INSERT ON accounting.postings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION accounting.check_voucher_balance();

-- A voucher has postings (R13), checked at commit.
-- +goose StatementBegin
CREATE FUNCTION accounting.check_voucher_has_postings()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM accounting.postings WHERE voucher_id = NEW.id) THEN
        RAISE EXCEPTION 'accounting: voucher % has no postings', NEW.id USING ERRCODE = 'P0001';
    END IF;
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER tr_vouchers_has_postings
    AFTER INSERT ON accounting.vouchers
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION accounting.check_voucher_has_postings();

-- An account's number and kind freeze at its first posting (A3).
-- +goose StatementBegin
CREATE FUNCTION accounting.freeze_posted_account()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF (NEW.number <> OLD.number OR NEW.kind <> OLD.kind)
        AND EXISTS (SELECT 1 FROM accounting.postings WHERE account_id = OLD.id) THEN
        RAISE EXCEPTION 'accounting: account % has postings; its number and kind are fixed', OLD.number
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_accounts_freeze
    BEFORE UPDATE ON accounting.accounts
    FOR EACH ROW EXECUTE FUNCTION accounting.freeze_posted_account();

-- +goose Down
DROP SCHEMA accounting CASCADE;
