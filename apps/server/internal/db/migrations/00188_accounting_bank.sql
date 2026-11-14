-- +goose Up
-- Accounting's bank (accounting design §7, delivery F: A26–A29, as amended by
-- R5 and R11).
--
-- bank_accounts are the company's accounts at the bank, each the statement
-- of one ledger account (an asset account, one bank account each): the
-- account number (a Norwegian BBAN, mod 11), IBAN, BIC, NOK only in this
-- phase, where its statement comes from — files uploaded, or Invoices' bank
-- feed from a start day (A29) — the CSV column mapping a file of it is read
-- with, and the feed's own state: the lease of the accounting.bankfeed
-- worker, when it last read the bank and what the bank last said.
--
-- statements are what was imported: a camt.053 statement, a CSV file, or one
-- cycle of the feed, with its period and its opening and closing balances
-- when the source gives them, the file kept in the object store (key and
-- hash), who imported it and when, and whether its balances follow on from
-- the previous statement's (a gap or an overlap is flagged, never refused).
--
-- statement_lines are the account's movements, signed (money in positive),
-- each with what the bank said of it, the bank's own id when it gave one and
-- a fingerprint of its fields. A line already imported is skipped by its
-- bank id, or without one by its fingerprint and its ordinal among the
-- file's lines of that fingerprint — so two genuine identical lines of one
-- day both survive. A line is open until a match stands on it.
--
-- statement_matches link a line to the postings it is (statement_match_
-- postings): a posting is matched to at most one line, while the match
-- stands. A match that made a voucher names it; an undone match keeps its
-- row, with who undid it and the reversal it posted.
--
-- bank_rules propose a coded voucher for a line by its text or its
-- counterparty: bank fees to an account, with a tax code.
--
-- voucher_bank_refs is, for a voucher of a payment that came from a bank
-- line in Invoices (a file's or the feed's), the receiving account, the
-- line's reference and its source, so reconciliation matches the line on
-- the statement to the payment's posting by the shared reference.
CREATE TABLE accounting.bank_accounts (
    id                  bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name                varchar(100) NOT NULL,
    account_number      varchar(11)  NOT NULL,
    iban                varchar(34),
    bic                 varchar(11),
    currency            char(3)      NOT NULL DEFAULT 'NOK',
    ledger_account_id   integer      NOT NULL REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    source              varchar(4)   NOT NULL DEFAULT 'file',
    feed_start_day      date,
    feed_status         varchar(12),
    feed_detail         varchar(500) NOT NULL DEFAULT '',
    feed_last_synced_at timestamptz,
    feed_next_sync_at   timestamptz,
    feed_lease_id       uuid,
    feed_lease_until    timestamptz,
    csv_mapping         jsonb,
    active              boolean      NOT NULL DEFAULT true,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL,
    revision            integer      NOT NULL DEFAULT 1,
    CONSTRAINT ck_bank_accounts_name CHECK (btrim(name) <> ''),
    CONSTRAINT ck_bank_accounts_number CHECK (account_number ~ '^[0-9]{11}$'),
    CONSTRAINT ck_bank_accounts_currency CHECK (currency = 'NOK'),
    CONSTRAINT ck_bank_accounts_source CHECK (source IN ('file', 'feed')),
    CONSTRAINT ck_bank_accounts_feed CHECK (source <> 'feed' OR feed_start_day IS NOT NULL),
    CONSTRAINT ck_bank_accounts_feed_status CHECK (
        feed_status IS NULL OR feed_status IN ('ok', 'unauthorized', 'rate_limited', 'failing', 'not_offered', 'off')),
    CONSTRAINT ck_bank_accounts_lease CHECK ((feed_lease_id IS NULL) = (feed_lease_until IS NULL))
);
CREATE UNIQUE INDEX ux_bank_accounts_number ON accounting.bank_accounts (account_number);
CREATE UNIQUE INDEX ux_bank_accounts_ledger ON accounting.bank_accounts (ledger_account_id);
CREATE INDEX ix_bank_accounts_feed_due ON accounting.bank_accounts (feed_next_sync_at) WHERE source = 'feed' AND active;

CREATE TABLE accounting.statements (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    bank_account_id     bigint        NOT NULL REFERENCES accounting.bank_accounts (id) ON DELETE RESTRICT,
    source              varchar(7)    NOT NULL,
    statement_ref       varchar(140)  NOT NULL DEFAULT '',
    period_from         date          NOT NULL,
    period_to           date          NOT NULL,
    opening_balance     numeric(14,2),
    closing_balance     numeric(14,2),
    continuity          varchar(8)    NOT NULL DEFAULT 'unknown',
    continuity_detail   varchar(500)  NOT NULL DEFAULT '',
    file_name           varchar(255),
    object_key          varchar(255),
    file_sha256         char(64),
    lines_imported      integer       NOT NULL DEFAULT 0,
    lines_skipped       integer       NOT NULL DEFAULT 0,
    imported_at         timestamptz   NOT NULL,
    imported_by_user_id uuid,
    CONSTRAINT ck_statements_source CHECK (source IN ('camt053', 'csv', 'feed')),
    CONSTRAINT ck_statements_period CHECK (period_to >= period_from),
    CONSTRAINT ck_statements_balances CHECK ((opening_balance IS NULL) = (closing_balance IS NULL)),
    CONSTRAINT ck_statements_continuity CHECK (continuity IN ('ok', 'gap', 'overlap', 'mismatch', 'unknown')),
    CONSTRAINT ck_statements_file CHECK ((source = 'feed') = (object_key IS NULL) AND (object_key IS NULL) = (file_sha256 IS NULL)),
    CONSTRAINT ck_statements_by CHECK ((source = 'feed') = (imported_by_user_id IS NULL))
);
CREATE INDEX ix_statements_account ON accounting.statements (bank_account_id, period_to);

CREATE TABLE accounting.statement_lines (
    id                   bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    statement_id         bigint        NOT NULL REFERENCES accounting.statements (id) ON DELETE RESTRICT,
    bank_account_id      bigint        NOT NULL REFERENCES accounting.bank_accounts (id) ON DELETE RESTRICT,
    booked_on            date          NOT NULL,
    value_on             date,
    amount               numeric(14,2) NOT NULL,
    currency             char(3)       NOT NULL DEFAULT 'NOK',
    text                 varchar(1000) NOT NULL DEFAULT '',
    reference            varchar(140)  NOT NULL DEFAULT '',
    kid                  varchar(25),
    counterparty_name    varchar(140)  NOT NULL DEFAULT '',
    counterparty_account varchar(34)   NOT NULL DEFAULT '',
    bank_id              varchar(140),
    fingerprint          char(64)      NOT NULL,
    ordinal              smallint      NOT NULL DEFAULT 1,
    kind                 varchar(10)   NOT NULL DEFAULT 'transfer',
    reversal             boolean       NOT NULL DEFAULT false,
    batch                jsonb,
    state                varchar(7)    NOT NULL DEFAULT 'open',
    created_at           timestamptz   NOT NULL,
    CONSTRAINT ck_statement_lines_amount CHECK (amount <> 0),
    CONSTRAINT ck_statement_lines_currency CHECK (currency = 'NOK'),
    CONSTRAINT ck_statement_lines_ordinal CHECK (ordinal >= 1),
    CONSTRAINT ck_statement_lines_kind CHECK (kind IN ('transfer', 'card', 'internal', 'other')),
    CONSTRAINT ck_statement_lines_state CHECK (state IN ('open', 'matched'))
);
-- The duplicate guard: by the bank's id when it gave one, else by the
-- fingerprint and its ordinal.
CREATE UNIQUE INDEX ux_statement_lines_bank_id ON accounting.statement_lines (bank_account_id, bank_id)
    WHERE bank_id IS NOT NULL;
CREATE UNIQUE INDEX ux_statement_lines_fingerprint ON accounting.statement_lines (bank_account_id, fingerprint, ordinal)
    WHERE bank_id IS NULL;
CREATE INDEX ix_statement_lines_account ON accounting.statement_lines (bank_account_id, booked_on);
CREATE INDEX ix_statement_lines_statement ON accounting.statement_lines (statement_id);

CREATE TABLE accounting.bank_rules (
    id                    bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name                  varchar(100) NOT NULL,
    bank_account_id       bigint       REFERENCES accounting.bank_accounts (id) ON DELETE RESTRICT,
    text_contains         varchar(140),
    counterparty_contains varchar(140),
    direction             varchar(3)   NOT NULL DEFAULT 'any',
    account_id            integer      NOT NULL REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    tax_code_id           integer      REFERENCES accounting.tax_codes (id) ON DELETE RESTRICT,
    project_id            integer,
    description           varchar(500) NOT NULL DEFAULT '',
    active                boolean      NOT NULL DEFAULT true,
    version               integer      NOT NULL DEFAULT 1,
    created_at            timestamptz  NOT NULL,
    updated_at            timestamptz  NOT NULL,
    CONSTRAINT ck_bank_rules_name CHECK (btrim(name) <> ''),
    CONSTRAINT ck_bank_rules_match CHECK (text_contains IS NOT NULL OR counterparty_contains IS NOT NULL),
    CONSTRAINT ck_bank_rules_direction CHECK (direction IN ('in', 'out', 'any'))
);

CREATE TABLE accounting.statement_matches (
    id                 bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    statement_line_id  bigint       NOT NULL REFERENCES accounting.statement_lines (id) ON DELETE RESTRICT,
    kind               varchar(16)  NOT NULL,
    request_key        varchar(200) NOT NULL,
    reason             varchar(500) NOT NULL DEFAULT '',
    voucher_id         bigint       REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    supplier_payment_id bigint      REFERENCES accounting.supplier_payments (id) ON DELETE RESTRICT,
    bank_rule_id       bigint       REFERENCES accounting.bank_rules (id) ON DELETE RESTRICT,
    bank_rule_version  integer,
    matched_at         timestamptz  NOT NULL,
    matched_by_user_id uuid         NOT NULL,
    undone_at          timestamptz,
    undone_by_user_id  uuid,
    undo_voucher_id    bigint       REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    CONSTRAINT ck_statement_matches_kind CHECK (kind IN (
        'posting', 'feed_payment', 'batch', 'supplier_invoice', 'vipps_payout', 'vat', 'transfer', 'rule', 'coded')),
    CONSTRAINT ck_statement_matches_undone CHECK ((undone_at IS NULL) = (undone_by_user_id IS NULL)),
    CONSTRAINT ck_statement_matches_rule CHECK ((bank_rule_id IS NULL) = (bank_rule_version IS NULL))
);
-- One standing match a line.
CREATE UNIQUE INDEX ux_statement_matches_line ON accounting.statement_matches (statement_line_id) WHERE undone_at IS NULL;
CREATE INDEX ix_statement_matches_voucher ON accounting.statement_matches (voucher_id) WHERE voucher_id IS NOT NULL;

CREATE TABLE accounting.statement_match_postings (
    match_id   bigint  NOT NULL REFERENCES accounting.statement_matches (id) ON DELETE RESTRICT,
    posting_id bigint  NOT NULL REFERENCES accounting.postings (id) ON DELETE RESTRICT,
    standing   boolean NOT NULL DEFAULT true,
    PRIMARY KEY (match_id, posting_id)
);
-- A posting is matched to at most one line while the match stands.
CREATE UNIQUE INDEX ux_statement_match_postings_posting ON accounting.statement_match_postings (posting_id) WHERE standing;

CREATE TABLE accounting.voucher_bank_refs (
    voucher_id   bigint       PRIMARY KEY REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    bank_account varchar(11)  NOT NULL DEFAULT '',
    reference    varchar(140) NOT NULL DEFAULT '',
    source       varchar(10)  NOT NULL
);
CREATE INDEX ix_voucher_bank_refs_reference ON accounting.voucher_bank_refs (reference) WHERE reference <> '';

CREATE TRIGGER tr_voucher_bank_refs_immutable
    BEFORE UPDATE OR DELETE ON accounting.voucher_bank_refs
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();

-- +goose Down
DROP TABLE accounting.voucher_bank_refs;
DROP TABLE accounting.statement_match_postings;
DROP TABLE accounting.statement_matches;
DROP TABLE accounting.bank_rules;
DROP TABLE accounting.statement_lines;
DROP TABLE accounting.statements;
DROP TABLE accounting.bank_accounts;
