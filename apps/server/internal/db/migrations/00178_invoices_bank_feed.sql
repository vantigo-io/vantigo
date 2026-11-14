-- +goose Up
-- Invoices: a bank feed, Folio first (invoices bank feed design F3, F4, F10;
-- plan findings 3, 4, 9): the delivery's whole schema at once — the
-- connection and its accounts, and the third bank line format, feed, whose
-- lines no person uploaded and whose payments no person need register. Every
-- query over these tables takes the time as a parameter from Deps.Clock(),
-- never now(). No column is named with a word PostgreSQL reserves; a schema
-- test pins it. The frozen tables' triggers compare rows as jsonb, so each
-- freezes the column it gains with the rest and none changes here.

-- The bank connection (F3): one row, payment_provider_credentials' shape.
-- secret_ciphertext is the API key sealed by the secrets box under
-- invoices/bank-feed-credential; key_last4 its last four characters, the one
-- part a screen shows. status is unauthorized once the bank refused the key,
-- at rejected_at, until a PUT stores a key again. A DELETE is the API's.
CREATE TABLE invoices.bank_feed_connections (
    id                   integer     PRIMARY KEY,
    provider             varchar(20) NOT NULL,
    secret_ciphertext    text        NOT NULL,
    key_last4            varchar(4)  NOT NULL,
    status               varchar(12) NOT NULL DEFAULT 'ok',
    rejected_at          timestamptz,
    connected_by_user_id uuid        NOT NULL,
    connected_at         timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL,
    CONSTRAINT ck_bank_feed_connections_single_row CHECK (id = 1),
    CONSTRAINT ck_bank_feed_connections_provider CHECK (provider IN ('folio')),
    CONSTRAINT ck_bank_feed_connections_status CHECK (status IN ('ok', 'unauthorized'))
);

-- An account the feed reads (F4): switched on by a person from start_day, its
-- bank_import_accounts row then in format feed. status, reason, detail and
-- failures are the sync's (F7); next_sync_at and the lease its worker's
-- claim. A switch off keeps the row, enabled false.
CREATE TABLE invoices.bank_feed_accounts (
    account            varchar(11)  PRIMARY KEY,
    enabled            boolean      NOT NULL,
    start_day          date         NOT NULL,
    status             varchar(12)  NOT NULL DEFAULT 'ok',
    reason             varchar(40),
    detail             varchar(500) NOT NULL DEFAULT '',
    failures           integer      NOT NULL DEFAULT 0,
    last_synced_at     timestamptz,
    last_bank_file_id  bigint       REFERENCES invoices.bank_files (id),
    next_sync_at       timestamptz,
    lease_id           varchar(32),
    lease_until        timestamptz,
    enabled_by_user_id uuid         NOT NULL,
    updated_at         timestamptz  NOT NULL,
    CONSTRAINT ck_bank_feed_accounts_status CHECK (status IN ('ok', 'failing', 'refused', 'unauthorized')),
    CONSTRAINT ck_bank_feed_accounts_lease CHECK ((lease_id IS NULL) = (lease_until IS NULL))
);
-- The worker's claim reads the accounts due.
CREATE INDEX ix_bank_feed_accounts_due ON invoices.bank_feed_accounts (next_sync_at) WHERE enabled;

-- A feed batch is a bank file no person uploaded (F5 step 6): its format is
-- feed exactly when it has a provider and no uploader.
ALTER TABLE invoices.bank_files
    ADD COLUMN provider varchar(20),
    ALTER COLUMN uploaded_by_user_id DROP NOT NULL,
    DROP CONSTRAINT ck_bank_files_format;
ALTER TABLE invoices.bank_files
    ADD CONSTRAINT ck_bank_files_format CHECK (format IN ('ocr', 'camt054', 'feed')),
    ADD CONSTRAINT ck_bank_files_origin CHECK (
        (format = 'feed') = (uploaded_by_user_id IS NULL) AND (format = 'feed') = (provider IS NOT NULL));

ALTER TABLE invoices.bank_transactions DROP CONSTRAINT ck_bank_transactions_format;
ALTER TABLE invoices.bank_transactions
    ADD CONSTRAINT ck_bank_transactions_format CHECK (format IN ('ocr', 'camt054', 'feed'));

ALTER TABLE invoices.bank_import_accounts
    DROP CONSTRAINT ck_bank_import_accounts_format,
    DROP CONSTRAINT ck_bank_import_accounts_previous_format;
ALTER TABLE invoices.bank_import_accounts
    ADD CONSTRAINT ck_bank_import_accounts_format CHECK (format IN ('ocr', 'camt054', 'feed')),
    ADD CONSTRAINT ck_bank_import_accounts_previous_format CHECK (previous_format IN ('ocr', 'camt054', 'feed'));

-- A payment from a feed line (F6, finding 3): its bank line, no attempt, and a
-- user either way — none when the feed matched it, the person's when a person
-- applied it from the queue or ran the match. A file's payment still needs
-- its user.
ALTER TABLE invoices.payments
    DROP CONSTRAINT ck_payments_source,
    DROP CONSTRAINT ck_payments_origin;
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

-- A charge payment likewise (finding 4): a bank line unless manual, and a
-- user unless from the feed.
ALTER TABLE invoices.charge_payments
    ALTER COLUMN registered_by_user_id DROP NOT NULL,
    DROP CONSTRAINT ck_charge_payments_source,
    DROP CONSTRAINT ck_charge_payments_origin;
ALTER TABLE invoices.charge_payments
    ADD CONSTRAINT ck_charge_payments_source CHECK (source IN ('manual', 'ocr', 'camt054', 'bank_feed')),
    ADD CONSTRAINT ck_charge_payments_origin CHECK (
        (source = 'manual') = (bank_transaction_id IS NULL) AND (registered_by_user_id IS NOT NULL OR source = 'bank_feed'));

-- A waiver is a person's, but for the one matching writes when a payment met
-- a letter's deadline (finding 4) — by no user when the feed matched it.
ALTER TABLE invoices.charge_waivers
    ALTER COLUMN waived_by_user_id DROP NOT NULL,
    ADD CONSTRAINT ck_charge_waivers_by CHECK (waived_by_user_id IS NOT NULL OR reason = 'deadline_met');

-- A line's matched and queued events are by no user when the feed matched it.
ALTER TABLE invoices.bank_transaction_events
    ALTER COLUMN by_user_id DROP NOT NULL;

-- +goose Down
-- Everything 00178 added, in reverse: every user required again and every
-- CHECK restored as 00041 and 00046 left it — which fails while a feed batch,
-- a feed line, a bank_feed payment or anything registered by no user exists,
-- the honest answer: a Down never deletes bookkeeping — then the provider
-- column and the two tables.
ALTER TABLE invoices.bank_transaction_events
    ALTER COLUMN by_user_id SET NOT NULL;

ALTER TABLE invoices.charge_waivers
    DROP CONSTRAINT ck_charge_waivers_by,
    ALTER COLUMN waived_by_user_id SET NOT NULL;

ALTER TABLE invoices.charge_payments
    DROP CONSTRAINT ck_charge_payments_origin,
    DROP CONSTRAINT ck_charge_payments_source;
ALTER TABLE invoices.charge_payments
    ADD CONSTRAINT ck_charge_payments_source CHECK (source IN ('manual', 'ocr', 'camt054')),
    ADD CONSTRAINT ck_charge_payments_origin CHECK ((source = 'manual') = (bank_transaction_id IS NULL)),
    ALTER COLUMN registered_by_user_id SET NOT NULL;

ALTER TABLE invoices.payments
    DROP CONSTRAINT ck_payments_origin,
    DROP CONSTRAINT ck_payments_source;
ALTER TABLE invoices.payments
    ADD CONSTRAINT ck_payments_source CHECK (source IN ('manual', 'ocr', 'camt054', 'vipps')),
    ADD CONSTRAINT ck_payments_origin CHECK (
        (source = 'manual' AND bank_transaction_id IS NULL AND registered_by_user_id IS NOT NULL
            AND payment_attempt_id IS NULL)
        OR (source IN ('ocr', 'camt054') AND bank_transaction_id IS NOT NULL AND registered_by_user_id IS NOT NULL
            AND payment_attempt_id IS NULL)
        OR (source = 'vipps' AND payment_attempt_id IS NOT NULL AND bank_transaction_id IS NULL
            AND registered_by_user_id IS NULL));

ALTER TABLE invoices.bank_import_accounts
    DROP CONSTRAINT ck_bank_import_accounts_previous_format,
    DROP CONSTRAINT ck_bank_import_accounts_format;
ALTER TABLE invoices.bank_import_accounts
    ADD CONSTRAINT ck_bank_import_accounts_format CHECK (format IN ('ocr', 'camt054')),
    ADD CONSTRAINT ck_bank_import_accounts_previous_format CHECK (previous_format IN ('ocr', 'camt054'));

ALTER TABLE invoices.bank_transactions DROP CONSTRAINT ck_bank_transactions_format;
ALTER TABLE invoices.bank_transactions
    ADD CONSTRAINT ck_bank_transactions_format CHECK (format IN ('ocr', 'camt054'));

DROP TABLE invoices.bank_feed_accounts;
DROP TABLE invoices.bank_feed_connections;

ALTER TABLE invoices.bank_files
    DROP CONSTRAINT ck_bank_files_origin,
    DROP CONSTRAINT ck_bank_files_format;
ALTER TABLE invoices.bank_files
    ADD CONSTRAINT ck_bank_files_format CHECK (format IN ('ocr', 'camt054')),
    ALTER COLUMN uploaded_by_user_id SET NOT NULL,
    DROP COLUMN provider;
