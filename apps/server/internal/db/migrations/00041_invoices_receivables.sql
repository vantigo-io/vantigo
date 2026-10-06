-- +goose Up
-- Invoices phase 4, pull request 1 (invoices payments and reminders design
-- D2, D3, D5-D11, D18): the whole pull request's schema at once — the bank
-- import accounts, files, lines and their events, the payments' source and
-- bank line, the collection rates with their seeds, the reminder settings and
-- the per-customer policy, manual deliveries, charge payments, reminder runs,
-- print batches and letters, holds, hand-offs and charge waivers — so no
-- later task of the pull request adds a migration (it amends this one in
-- place while it is unreleased). Every query over these tables takes the time
-- as a parameter from Deps.Clock(), never now(); the one now() here is the
-- migration's own stamp on the rows it seeds. customer_id is opaque, as
-- everywhere in this schema. No column is named with a word PostgreSQL
-- reserves; a schema test pins it.

-- A row that is never deleted: attached BEFORE DELETE, it refuses every
-- delete — the reminder settings row's.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_row_delete()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    RAISE EXCEPTION 'invoices: a row of % is never deleted', TG_TABLE_NAME USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

-- The format each receiving account's bank files come in (D3): set by the
-- account's first import, changed only by the format PUT, which keeps the
-- previous format and the last booking day its lines covered (the cutover;
-- D4 holds back what the old format may already have registered).
CREATE TABLE invoices.bank_import_accounts (
    account         varchar(11) PRIMARY KEY,
    format          varchar(10) NOT NULL,
    previous_format varchar(10),
    cutover_through date,
    set_by_user_id  uuid        NOT NULL,
    set_at          timestamptz NOT NULL,
    CONSTRAINT ck_bank_import_accounts_format CHECK (format IN ('ocr', 'camt054')),
    CONSTRAINT ck_bank_import_accounts_previous_format CHECK (previous_format IN ('ocr', 'camt054')),
    -- A cutover day belongs to a previous format.
    CONSTRAINT ck_bank_import_accounts_cutover CHECK ((previous_format IS NULL) <= (cutover_through IS NULL))
);

-- An account is never deleted, and an UPDATE changes only what the format PUT
-- writes; the account itself never changes.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_bank_import_account_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    format_columns text[] := ARRAY['format', 'previous_format', 'cutover_through', 'set_by_user_id', 'set_at'];
BEGIN
    IF TG_OP = 'UPDATE' AND (to_jsonb(OLD) - format_columns) = (to_jsonb(NEW) - format_columns) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a bank import account changes only its format' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_bank_import_accounts_immutable
    BEFORE UPDATE OR DELETE ON invoices.bank_import_accounts
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_bank_import_account_change();

-- One imported bank file (D3), stored once under its hash and imported once:
-- its bytes and its own identity (OCR "sender:transmission:recipient", camt
-- "MsgId|CreDtTm") are each unique. ignored_kinds is the by-kind breakdown of
-- ignored, {"debit":n,"not_booked":n,"card_information":n,"zero_amount":n}.
-- duplicates is known only after the lines' insert, so it is NULL until the
-- import's own transaction sets it, once (plan reading 3).
CREATE TABLE invoices.bank_files (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    format              varchar(10)   NOT NULL,
    sha256              char(64)      NOT NULL,
    file_identity       varchar(200)  NOT NULL,
    object_key          varchar(300)  NOT NULL,
    byte_size           integer       NOT NULL,
    accounts            varchar(11)[] NOT NULL,
    first_booked_on     date,
    last_booked_on      date,
    transactions        integer       NOT NULL,
    duplicates          integer,
    ignored             integer       NOT NULL,
    ignored_kinds       jsonb         NOT NULL,
    uploaded_by_user_id uuid          NOT NULL,
    uploaded_at         timestamptz   NOT NULL,
    CONSTRAINT ck_bank_files_format CHECK (format IN ('ocr', 'camt054')),
    CONSTRAINT ck_bank_files_ignored_kinds CHECK (jsonb_typeof(ignored_kinds) = 'object'),
    CONSTRAINT ck_bank_files_counts CHECK (transactions >= 0 AND ignored >= 0 AND duplicates >= 0),
    CONSTRAINT uq_bank_files_sha256 UNIQUE (sha256),
    CONSTRAINT uq_bank_files_identity UNIQUE (format, file_identity)
);

-- A file is never deleted and takes one write: duplicates from NULL to a
-- value, nothing else changed.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_bank_file_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.duplicates IS NULL AND NEW.duplicates IS NOT NULL
       AND (to_jsonb(OLD) - 'duplicates') = (to_jsonb(NEW) - 'duplicates') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a bank file is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_bank_files_immutable
    BEFORE UPDATE OR DELETE ON invoices.bank_files
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_bank_file_change();

-- One transaction of a bank file (D3): what the bank said, frozen, and its
-- state in matching (D4) and the exception queue (D5). format is the file's,
-- denormalised for the soft key; ordered_on is OCR's Oppdragsdato (D8). A
-- line whose fingerprint an earlier live line of its account already has is
-- kept as a duplicate row linked to it; the fingerprint index leaves such
-- rows out for good (NI2). suggested_invoice_id is a hint, without a foreign
-- key.
CREATE TABLE invoices.bank_transactions (
    id                   bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    bank_file_id         bigint        NOT NULL REFERENCES invoices.bank_files (id) ON DELETE RESTRICT,
    line_ref             varchar(60)   NOT NULL,
    format               varchar(10)   NOT NULL,
    account              varchar(11)   NOT NULL,
    direction            varchar(6)    NOT NULL,
    negative             boolean       NOT NULL DEFAULT false,
    booked_on            date          NOT NULL,
    value_on             date,
    ordered_on           date,
    amount               numeric(14,2) NOT NULL,
    currency             char(3)       NOT NULL,
    kid                  varchar(25),
    remittance_text      varchar(1000) NOT NULL DEFAULT '',
    debtor_name          varchar(140)  NOT NULL DEFAULT '',
    debtor_account       varchar(34)   NOT NULL DEFAULT '',
    archive_ref          varchar(35)   NOT NULL DEFAULT '',
    bank_code            varchar(35)   NOT NULL DEFAULT '',
    fingerprint          char(64)      NOT NULL,
    ordinal              smallint      NOT NULL,
    duplicate_of_id      bigint        REFERENCES invoices.bank_transactions (id),
    status               varchar(10)   NOT NULL DEFAULT 'pending',
    reason               varchar(30),
    suggested_invoice_id bigint,
    resolution           varchar(25),
    resolved_by_user_id  uuid,
    resolved_at          timestamptz,
    resolution_note      varchar(500)  NOT NULL DEFAULT '',
    CONSTRAINT ck_bank_transactions_format CHECK (format IN ('ocr', 'camt054')),
    CONSTRAINT ck_bank_transactions_direction CHECK (direction IN ('credit', 'debit')),
    CONSTRAINT ck_bank_transactions_ordinal CHECK (ordinal >= 1),
    CONSTRAINT ck_bank_transactions_amount CHECK (amount > 0),
    CONSTRAINT ck_bank_transactions_currency CHECK (currency = 'NOK'),
    CONSTRAINT ck_bank_transactions_status CHECK (status IN ('pending', 'matched', 'exception', 'resolved', 'duplicate')),
    CONSTRAINT ck_bank_transactions_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed')),
    CONSTRAINT ck_bank_transactions_resolution CHECK (resolution IN ('applied', 'not_customer_payment',
        'reversal_handled', 'duplicate_confirmed')),
    -- An exception has a reason (a resolved line keeps it); resolved is
    -- exactly a resolution with its time; a duplicate has its link, and the
    -- link outlives the status — but a linked line is never pending or
    -- matched: it reaches a payment only through the queue (D5).
    CONSTRAINT ck_bank_transactions_state CHECK (
        (status = 'exception') <= (reason IS NOT NULL)
        AND (status = 'resolved') = (resolution IS NOT NULL AND resolved_at IS NOT NULL)
        AND (status = 'duplicate') <= (duplicate_of_id IS NOT NULL)
        AND (duplicate_of_id IS NULL OR status IN ('duplicate', 'exception', 'resolved')))
);
-- One live line per account and fingerprint: the import's single INSERT …
-- ON CONFLICT DO NOTHING, ordered by fingerprint, waits on it (D3 step 7).
CREATE UNIQUE INDEX ux_bank_transactions_fingerprint ON invoices.bank_transactions (account, fingerprint)
    WHERE duplicate_of_id IS NULL;
CREATE INDEX ix_bank_transactions_open ON invoices.bank_transactions (status)
    WHERE status IN ('pending', 'exception', 'duplicate');
CREATE INDEX ix_bank_transactions_file ON invoices.bank_transactions (bank_file_id);
-- The soft key matching reads for a payment the other format may already
-- have registered (D4).
CREATE INDEX ix_bank_transactions_soft ON invoices.bank_transactions (account, booked_on, amount, kid)
    WHERE kid IS NOT NULL;

-- A line is never deleted. An UPDATE changes only its state columns — the
-- row as jsonb less them never changes, so duplicate_of_id and any column
-- added later are frozen (NI2) — never returns it to pending, makes it
-- matched only from pending (the match, D4; the queue resolves, D5), and
-- never clears a reason once set (a resolved line keeps it; I15). The erase
-- blanks resolution_note, a state column.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_bank_transaction_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    state_columns text[] := ARRAY['status', 'reason', 'suggested_invoice_id', 'resolution',
        'resolved_by_user_id', 'resolved_at', 'resolution_note'];
BEGIN
    IF TG_OP = 'DELETE' OR (to_jsonb(OLD) - state_columns) <> (to_jsonb(NEW) - state_columns) THEN
        RAISE EXCEPTION 'invoices: a bank transaction is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.status = 'pending' AND OLD.status <> 'pending' THEN
        RAISE EXCEPTION 'invoices: a bank transaction never returns to pending' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.status = 'matched' AND OLD.status NOT IN ('pending', 'matched') THEN
        RAISE EXCEPTION 'invoices: a bank transaction is matched only from pending' USING ERRCODE = 'P0001';
    END IF;
    IF OLD.reason IS NOT NULL AND NEW.reason IS NULL THEN
        RAISE EXCEPTION 'invoices: a bank transaction keeps its reason' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_bank_transactions_immutable
    BEFORE UPDATE OR DELETE ON invoices.bank_transactions
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_bank_transaction_change();

-- What happened to a line, by whom and when (D5): written by every match,
-- queueing and queue action — and reversed on a line whose payment a
-- reversal took back, which then never applies its money again.
CREATE TABLE invoices.bank_transaction_events (
    id                  bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    bank_transaction_id bigint       NOT NULL REFERENCES invoices.bank_transactions (id) ON DELETE RESTRICT,
    event               varchar(20)  NOT NULL,
    reason              varchar(30),
    note                varchar(500) NOT NULL DEFAULT '',
    by_user_id          uuid         NOT NULL,
    at                  timestamptz  NOT NULL,
    CONSTRAINT ck_bank_transaction_events_event CHECK (event IN ('matched', 'queued', 'applied', 'dismissed',
        'reversal_handled', 'reopened', 'duplicate_confirmed', 'treated_as_distinct', 'reversed')),
    -- The lines' reasons (ck_bank_transactions_reason).
    CONSTRAINT ck_bank_transaction_events_reason CHECK (reason IS NULL OR reason IN ('kid_invalid', 'kid_unknown',
        'invoice_credited', 'invoice_settled', 'exceeds_open', 'no_kid', 'negative_amount', 'reversal',
        'vipps_payout', 'paid_before_issue', 'account_mismatch', 'possible_duplicate', 'payment_removed'))
);
-- A line's events, read with the line (D5).
CREATE INDEX ix_bank_transaction_events_line ON invoices.bank_transaction_events (bank_transaction_id);

-- An event is never deleted and takes one write, the erase's: its note blanked
-- to '', the rest of the row unchanged (D19, B1).
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_bank_transaction_event_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.note = '' AND (to_jsonb(OLD) - 'note') = (to_jsonb(NEW) - 'note') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a bank transaction event is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_bank_transaction_events_immutable
    BEFORE UPDATE OR DELETE ON invoices.bank_transaction_events
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_bank_transaction_event_change();

-- A payment's origin (D2): registered by hand, or from a line of an OCR or a
-- camt.054 file, always by a user — the uploader, the caller of the match or
-- the queue's. Existing rows are manual. The user goes nullable now so
-- 00042 can add the one origin without one (a Vipps capture) by replacing the
-- two CHECKs. tr_payments_immutable's jsonb comparison freezes the new
-- columns with the rest (R4 §5.1). A line may pay several invoices, so
-- bank_transaction_id is not unique.
ALTER TABLE invoices.payments
    ADD COLUMN source varchar(10) NOT NULL DEFAULT 'manual',
    ADD COLUMN bank_transaction_id bigint REFERENCES invoices.bank_transactions (id) ON DELETE RESTRICT,
    ALTER COLUMN registered_by_user_id DROP NOT NULL,
    ADD CONSTRAINT ck_payments_source CHECK (source IN ('manual', 'ocr', 'camt054')),
    ADD CONSTRAINT ck_payments_origin CHECK (
        (source = 'manual' AND bank_transaction_id IS NULL AND registered_by_user_id IS NOT NULL)
        OR (source IN ('ocr', 'camt054') AND bank_transaction_id IS NOT NULL AND registered_by_user_id IS NOT NULL));
CREATE INDEX ix_payments_bank_transaction ON invoices.payments (bank_transaction_id)
    WHERE bank_transaction_id IS NOT NULL;

-- The statutory rates as dated rows (D6): a row is in force from valid_from
-- until the next row of its kind. The seeded rows have no user; a user adds
-- later ones ahead of a release. release_value and release_source_ref are
-- what a later release seeded for a (kind, valid_from) a user had already
-- added — recorded whether or not they differ; the warning compares them.
CREATE TABLE invoices.collection_rates (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind               varchar(30)   NOT NULL,
    valid_from         date          NOT NULL,
    value              numeric(10,2) NOT NULL,
    release_value      numeric(10,2),
    release_source_ref varchar(100),
    source_ref         varchar(100)  NOT NULL,
    created_by_user_id uuid,
    created_at         timestamptz   NOT NULL,
    CONSTRAINT ck_collection_rates_kind CHECK (kind IN ('late_interest_percent', 'inkassosats', 'b2b_compensation_nok')),
    CONSTRAINT ck_collection_rates_value CHECK (value > 0),
    CONSTRAINT ck_collection_rates_release CHECK (release_value > 0
        AND (release_value IS NULL) = (release_source_ref IS NULL)),
    -- The late interest rate and the compensation are set per half-year.
    CONSTRAINT ck_collection_rates_half_year CHECK (kind = 'inkassosats'
        OR (extract(day FROM valid_from) = 1 AND extract(month FROM valid_from) IN (1, 7))),
    CONSTRAINT uq_collection_rates_kind_valid_from UNIQUE (kind, valid_from)
);

-- Append-only (D6): an UPDATE only of the release's value and regulation,
-- set once from NULL, nothing else changed; a DELETE only of a user's row
-- (the API deletes only one not yet in force or used).
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_collection_rate_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.created_by_user_id IS NOT NULL THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'invoices: a seeded collection rate is never deleted' USING ERRCODE = 'P0001';
    END IF;
    IF OLD.release_value IS NULL AND NEW.release_value IS NOT NULL
       AND (to_jsonb(OLD) - 'release_value' - 'release_source_ref')
           = (to_jsonb(NEW) - 'release_value' - 'release_source_ref') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a collection rate is append-only' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_collection_rates_append_only
    BEFORE UPDATE OR DELETE ON invoices.collection_rates
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_collection_rate_change();

-- The release's seed of one rate (D6, plan reading 4): this migration calls
-- it for every seed, and every later release's migration calls it for its
-- own, so it never fails on a row a user added first — the user's value is
-- kept, and the release's value and regulation are recorded beside it, once,
-- equal or not. A row already seeded is left as it is. Its created_at is the
-- calling migration's now().
-- +goose StatementBegin
CREATE FUNCTION invoices.seed_collection_rate(p_kind text, p_valid_from date, p_value numeric, p_source_ref text)
RETURNS void LANGUAGE sql AS $$
    INSERT INTO invoices.collection_rates (kind, valid_from, value, source_ref, created_by_user_id, created_at)
    VALUES (p_kind, p_valid_from, p_value, p_source_ref, NULL, now())
    ON CONFLICT (kind, valid_from) DO UPDATE
        SET release_value = EXCLUDED.value, release_source_ref = EXCLUDED.source_ref
    WHERE collection_rates.release_value IS NULL AND collection_rates.created_by_user_id IS NOT NULL
$$;
-- +goose StatementEnd

-- R4 §2.11, every value read on Lovdata.
SELECT invoices.seed_collection_rate('late_interest_percent', DATE '2024-01-01', 12.50, 'FOR-2023-12-14-2043');
SELECT invoices.seed_collection_rate('late_interest_percent', DATE '2024-07-01', 12.50, 'FOR-2024-06-26-1320');
SELECT invoices.seed_collection_rate('late_interest_percent', DATE '2025-01-01', 12.50, 'FOR-2024-12-19-3279');
SELECT invoices.seed_collection_rate('late_interest_percent', DATE '2025-07-01', 12.25, 'FOR-2025-06-23-1321');
SELECT invoices.seed_collection_rate('late_interest_percent', DATE '2026-01-01', 12.00, 'FOR-2025-12-18-2658');
SELECT invoices.seed_collection_rate('late_interest_percent', DATE '2026-07-01', 12.25, 'FOR-2026-06-25-1372');
SELECT invoices.seed_collection_rate('b2b_compensation_nok', DATE '2024-01-01', 470, 'FOR-2023-12-14-2043');
SELECT invoices.seed_collection_rate('b2b_compensation_nok', DATE '2024-07-01', 460, 'FOR-2024-06-26-1320');
SELECT invoices.seed_collection_rate('b2b_compensation_nok', DATE '2025-01-01', 470, 'FOR-2024-12-19-3279');
SELECT invoices.seed_collection_rate('b2b_compensation_nok', DATE '2025-07-01', 460, 'FOR-2025-06-23-1321');
SELECT invoices.seed_collection_rate('b2b_compensation_nok', DATE '2026-01-01', 460, 'FOR-2025-12-18-2658');
SELECT invoices.seed_collection_rate('b2b_compensation_nok', DATE '2026-07-01', 430, 'FOR-2026-06-25-1372');
SELECT invoices.seed_collection_rate('inkassosats', DATE '2019-01-01', 700, 'FOR-2018-12-20-2050');
SELECT invoices.seed_collection_rate('inkassosats', DATE '2026-01-01', 750, 'FOR-2025-12-19-2709');

-- The reminder settings (D7, D6's regimes and review): one row, id 1,
-- inserted below with its defaults and the review through 2026-12-31, the
-- last day before LOV-2026-05-22-19's signalled date.
CREATE TABLE invoices.reminder_settings (
    id                         smallint    PRIMARY KEY,
    enabled                    boolean     NOT NULL DEFAULT false,
    first_reminder_days        integer     NOT NULL DEFAULT 14,
    deadline_days              integer     NOT NULL DEFAULT 14,
    grace_days                 integer     NOT NULL DEFAULT 3,
    reminders_before_notice    integer     NOT NULL DEFAULT 1,
    collection_notice          boolean     NOT NULL DEFAULT true,
    person_charge              varchar(12) NOT NULL DEFAULT 'fee',
    business_charge            varchar(12) NOT NULL DEFAULT 'fee',
    late_interest              boolean     NOT NULL DEFAULT false,
    stale_import_days          integer     NOT NULL DEFAULT 3,
    inkassolov_2026_from       date,
    regime_reviewed_through    date        NOT NULL,
    regime_reviewed_by_user_id uuid,
    regime_reviewed_at         timestamptz NOT NULL,
    revision                   integer     NOT NULL DEFAULT 1,
    updated_at                 timestamptz NOT NULL,
    updated_by_user_id         uuid,
    CONSTRAINT ck_reminder_settings_single_row CHECK (id = 1),
    CONSTRAINT ck_reminder_settings_first_reminder_days CHECK (first_reminder_days BETWEEN 1 AND 60),
    -- At least 14 days: INKL § 9, INKF § 1-3.
    CONSTRAINT ck_reminder_settings_deadline_days CHECK (deadline_days BETWEEN 14 AND 60),
    -- At least a day: a payment ordered on the deadline day is booked later
    -- (INKF § 1-2 third paragraph).
    CONSTRAINT ck_reminder_settings_grace_days CHECK (grace_days BETWEEN 1 AND 10),
    CONSTRAINT ck_reminder_settings_reminders_before_notice CHECK (reminders_before_notice BETWEEN 0 AND 2),
    -- A consumer is never charged the compensation (FRL § 4 d).
    CONSTRAINT ck_reminder_settings_person_charge CHECK (person_charge IN ('fee', 'none')),
    CONSTRAINT ck_reminder_settings_business_charge CHECK (business_charge IN ('fee', 'compensation', 'none')),
    CONSTRAINT ck_reminder_settings_stale_import_days CHECK (stale_import_days BETWEEN 1 AND 30)
);

CREATE TRIGGER tr_reminder_settings_kept
    BEFORE DELETE ON invoices.reminder_settings
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_row_delete();

INSERT INTO invoices.reminder_settings (id, regime_reviewed_through, regime_reviewed_at, updated_at)
VALUES (1, DATE '2026-12-31', now(), now());

-- Whether a customer is reminded and charged (D7): no row is normal. A
-- credit-control decision, so an invoices table keyed by the opaque
-- customer id, not the billing profile.
CREATE TABLE invoices.customer_reminder_policies (
    customer_id        integer      PRIMARY KEY,
    mode               varchar(12)  NOT NULL,
    note               varchar(500) NOT NULL DEFAULT '',
    updated_by_user_id uuid         NOT NULL,
    updated_at         timestamptz  NOT NULL,
    CONSTRAINT ck_customer_reminder_policies_mode CHECK (mode IN ('normal', 'no_charges', 'none'))
);

-- A child row of an issued invoice — never a draft, never a credit note: a
-- manual delivery, a charge payment, a hold, a hand-off or a charge waiver.
-- The document is read FOR SHARE, whatever it is, and only then judged; the
-- marker is then read in a statement of its own, which sees an erase that
-- committed while this insert waited (00035's guard_delivery_insert shape),
-- and the row keeps no staff note for an anonymised customer.
-- +goose StatementBegin
CREATE FUNCTION invoices.guard_child_of_issued_insert()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    parent_kind     text;
    parent_status   text;
    parent_customer integer;
BEGIN
    SELECT kind, status, customer_id INTO parent_kind, parent_status, parent_customer
    FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE;
    IF parent_kind IS DISTINCT FROM 'invoice' OR parent_status IS DISTINCT FROM 'issued' THEN
        RAISE EXCEPTION 'invoices: % needs an issued invoice', TG_TABLE_NAME USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = parent_customer) THEN
        NEW.note := '';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- A delivery recorded by hand (D8): the invoice handed over or posted. A
-- charge needs a live delivery on or before the due date. Never deleted;
-- removed with a reason, once — the payments' shape.
CREATE TABLE invoices.manual_deliveries (
    id                  bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id          bigint       NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    kind                varchar(12)  NOT NULL,
    delivered_on        date         NOT NULL,
    note                varchar(500) NOT NULL DEFAULT '',
    recorded_by_user_id uuid         NOT NULL,
    recorded_at         timestamptz  NOT NULL,
    removed_at          timestamptz,
    removed_by_user_id  uuid,
    removal_reason      varchar(200),
    CONSTRAINT ck_manual_deliveries_kind CHECK (kind IN ('handed_over', 'posted')),
    CONSTRAINT ck_manual_deliveries_removal CHECK (
        (removed_at IS NULL AND removed_by_user_id IS NULL AND removal_reason IS NULL)
        OR (removed_at IS NOT NULL AND removed_by_user_id IS NOT NULL AND removal_reason IS NOT NULL AND removal_reason <> ''))
);
CREATE INDEX ix_manual_deliveries_invoice_live ON invoices.manual_deliveries (invoice_id) WHERE removed_at IS NULL;

-- A manual delivery is refused a DELETE and every UPDATE but the removal,
-- once, and the erase's blanking of the note — refuse_payment_change's rule,
-- which refuses a no-op too.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_manual_delivery_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (to_jsonb(OLD) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
           = (to_jsonb(NEW) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
       AND (NEW.note = OLD.note OR NEW.note = '')
       AND ((OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL)
            OR (NEW.note = ''
                AND (OLD.removed_at, OLD.removed_by_user_id, OLD.removal_reason)
                    IS NOT DISTINCT FROM (NEW.removed_at, NEW.removed_by_user_id, NEW.removal_reason))) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a manual delivery is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_manual_deliveries_immutable
    BEFORE UPDATE OR DELETE ON invoices.manual_deliveries
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_manual_delivery_change();
CREATE TRIGGER tr_manual_deliveries_parent
    BEFORE INSERT ON invoices.manual_deliveries
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_child_of_issued_insert();

-- Money received against an invoice's charges, never its principal (D9): by
-- hand, or from a bank line by the match or the queue's apply.
CREATE TABLE invoices.charge_payments (
    id                    bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id            bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    paid_on               date          NOT NULL,
    amount                numeric(14,2) NOT NULL,
    currency              char(3)       NOT NULL,
    source                varchar(10)   NOT NULL,
    bank_transaction_id   bigint        REFERENCES invoices.bank_transactions (id) ON DELETE RESTRICT,
    reference             varchar(100)  NOT NULL DEFAULT '',
    note                  varchar(500)  NOT NULL DEFAULT '',
    registered_by_user_id uuid          NOT NULL,
    registered_at         timestamptz   NOT NULL,
    removed_at            timestamptz,
    removed_by_user_id    uuid,
    removal_reason        varchar(200),
    CONSTRAINT ck_charge_payments_amount CHECK (amount > 0),
    CONSTRAINT ck_charge_payments_source CHECK (source IN ('manual', 'ocr', 'camt054')),
    CONSTRAINT ck_charge_payments_origin CHECK ((source = 'manual') = (bank_transaction_id IS NULL)),
    CONSTRAINT ck_charge_payments_removal CHECK (
        (removed_at IS NULL AND removed_by_user_id IS NULL AND removal_reason IS NULL)
        OR (removed_at IS NOT NULL AND removed_by_user_id IS NOT NULL AND removal_reason IS NOT NULL AND removal_reason <> ''))
);
CREATE INDEX ix_charge_payments_invoice_live ON invoices.charge_payments (invoice_id) WHERE removed_at IS NULL;
CREATE INDEX ix_charge_payments_bank_transaction ON invoices.charge_payments (bank_transaction_id)
    WHERE bank_transaction_id IS NOT NULL;

-- A charge payment is refused a DELETE and every UPDATE but the removal,
-- once, and the note blanked — the payments' rule.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_charge_payment_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (to_jsonb(OLD) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
           = (to_jsonb(NEW) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
       AND (NEW.note = OLD.note OR NEW.note = '')
       AND ((OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL)
            OR (NEW.note = ''
                AND (OLD.removed_at, OLD.removed_by_user_id, OLD.removal_reason)
                    IS NOT DISTINCT FROM (NEW.removed_at, NEW.removed_by_user_id, NEW.removal_reason))) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a charge payment is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_charge_payments_immutable
    BEFORE UPDATE OR DELETE ON invoices.charge_payments
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_charge_payment_change();
CREATE TRIGGER tr_charge_payments_parent
    BEFORE INSERT ON invoices.charge_payments
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_child_of_issued_insert();

-- One reminder run (D10). letters and skipped are known only at the run's
-- end, after its letters reference it, so they are NULL until the run sets
-- them, together, once (plan reading 3); a run that crashed keeps them NULL.
CREATE TABLE invoices.reminder_runs (
    id                        bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    run_on                    date        NOT NULL,
    created_at                timestamptz NOT NULL,
    created_by_user_id        uuid        NOT NULL,
    letters                   integer,
    skipped                   integer,
    last_booked_on            date,
    stale_import_acknowledged boolean     NOT NULL,
    CONSTRAINT ck_reminder_runs_counts CHECK (letters >= 0 AND skipped >= 0)
);

-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_reminder_run_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.letters IS NULL AND OLD.skipped IS NULL
       AND NEW.letters IS NOT NULL AND NEW.skipped IS NOT NULL
       AND (to_jsonb(OLD) - 'letters' - 'skipped') = (to_jsonb(NEW) - 'letters' - 'skipped') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a reminder run is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_reminder_runs_immutable
    BEFORE UPDATE OR DELETE ON invoices.reminder_runs
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_reminder_run_change();

-- Paper letters printed together for one posting day (D10): posted once —
-- who, when and the day together — or reprinted once, never both.
CREATE TABLE invoices.reminder_print_batches (
    id                 bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    post_on            date        NOT NULL,
    posted_on          date,
    created_at         timestamptz NOT NULL,
    created_by_user_id uuid        NOT NULL,
    posted_by_user_id  uuid,
    posted_at          timestamptz,
    reprinted_at       timestamptz,
    CONSTRAINT ck_reminder_print_batches_posted CHECK ((posted_on IS NULL) = (posted_at IS NULL)
        AND (posted_at IS NULL) = (posted_by_user_id IS NULL)),
    CONSTRAINT ck_reminder_print_batches_reprinted CHECK (posted_on IS NULL OR reprinted_at IS NULL)
);

-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_print_batch_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    write_columns text[] := ARRAY['posted_on', 'posted_at', 'posted_by_user_id', 'reprinted_at'];
BEGIN
    IF TG_OP = 'UPDATE' AND (to_jsonb(OLD) - write_columns) = (to_jsonb(NEW) - write_columns)
       AND ((OLD.posted_at IS NULL AND NEW.posted_at IS NOT NULL
             AND OLD.reprinted_at IS NOT DISTINCT FROM NEW.reprinted_at)
            OR (OLD.reprinted_at IS NULL AND NEW.reprinted_at IS NOT NULL
                AND (OLD.posted_on, OLD.posted_at, OLD.posted_by_user_id)
                    IS NOT DISTINCT FROM (NEW.posted_on, NEW.posted_at, NEW.posted_by_user_id))) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a print batch is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_reminder_print_batches_immutable
    BEFORE UPDATE OR DELETE ON invoices.reminder_print_batches
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_print_batch_change();

-- One letter of a run (D10): a reminder or the creditor's inkassovarsel, by
-- e-mail or on paper. Its facts are written at sending — each dispatch
-- attempt for e-mail, the print for paper — and frozen once sent, because the
-- deadline runs from the sending and a fee is judged on its letter's date.
-- held_reason is why a queued letter waits on its rates or the review (plan
-- reading 46). recipient is '' for paper and once the customer is
-- anonymised. message_id is made at the first claim and reused on every
-- retry (plan reading 52).
CREATE TABLE invoices.reminders (
    id                   bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id           bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    run_id               bigint        NOT NULL REFERENCES invoices.reminder_runs (id),
    print_batch_id       bigint        REFERENCES invoices.reminder_print_batches (id),
    sequence             smallint      NOT NULL,
    level                varchar(20)   NOT NULL,
    announces_collection boolean       NOT NULL DEFAULT false,
    channel              varchar(5)    NOT NULL,
    recipient            varchar(254)  NOT NULL DEFAULT '',
    language             char(2)       NOT NULL,
    created_at           timestamptz   NOT NULL,
    created_by_user_id   uuid          NOT NULL,
    sent_on              date,
    deadline             date,
    regime               varchar(15),
    principal_open       numeric(14,2),
    fee_kind             varchar(15),
    fee                  numeric(14,2),
    compensation         numeric(14,2),
    charges_earlier      numeric(14,2),
    interest             numeric(14,2),
    interest_waived      numeric(14,2),
    interest_paid        numeric(14,2),
    interest_from        date,
    interest_segments    jsonb,
    inkassosats          numeric(10,2),
    total                numeric(14,2),
    charge_notes         varchar(30)[],
    pdf_object_key       varchar(300),
    pdf_sha256           char(64),
    message_id           varchar(200),
    sent_at              timestamptz,
    status               varchar(15)   NOT NULL,
    held_reason          varchar(30),
    attempts             integer       NOT NULL DEFAULT 0,
    next_attempt_at      timestamptz,
    first_attempt_at     timestamptz,
    lease_id             varchar(100),
    lease_until          timestamptz,
    last_error           varchar(500),
    failed_at            timestamptz,
    withdrawn_at         timestamptz,
    withdrawn_by_user_id uuid,
    withdrawal_reason    varchar(200),
    CONSTRAINT ck_reminders_level CHECK (level IN ('reminder', 'collection_notice')),
    CONSTRAINT ck_reminders_channel CHECK (channel IN ('email', 'paper')),
    CONSTRAINT ck_reminders_regime CHECK (regime IN ('inkassolov_1988', 'inkassolov_2026')),
    CONSTRAINT ck_reminders_fee_kind CHECK (fee_kind IN ('none', 'reminder_fee', 'compensation')),
    CONSTRAINT ck_reminders_status CHECK (status IN ('queued', 'awaiting_print', 'printed', 'sent', 'withdrawn', 'failed')),
    CONSTRAINT ck_reminders_held_reason CHECK (held_reason IN ('collection_rates_outdated', 'collection_regime_unreviewed')),
    -- sent: every fact and its time (a fee-free letter has no fee,
    -- compensation, interest_from, segments or inkassosats); printed: the
    -- same facts and its batch; withdrawn: when and why; failed: when.
    CONSTRAINT ck_reminders_state CHECK (
        (status <> 'sent' OR (sent_on IS NOT NULL AND deadline IS NOT NULL AND regime IS NOT NULL
            AND principal_open IS NOT NULL AND fee_kind IS NOT NULL AND charges_earlier IS NOT NULL
            AND interest IS NOT NULL AND interest_waived IS NOT NULL AND interest_paid IS NOT NULL
            AND total IS NOT NULL AND sent_at IS NOT NULL))
        AND (status <> 'printed' OR (sent_on IS NOT NULL AND deadline IS NOT NULL AND regime IS NOT NULL
            AND principal_open IS NOT NULL AND fee_kind IS NOT NULL AND charges_earlier IS NOT NULL
            AND interest IS NOT NULL AND interest_waived IS NOT NULL AND interest_paid IS NOT NULL
            AND total IS NOT NULL AND print_batch_id IS NOT NULL))
        AND (status <> 'withdrawn' OR (withdrawn_at IS NOT NULL AND withdrawal_reason IS NOT NULL AND withdrawal_reason <> ''))
        AND (status <> 'failed' OR failed_at IS NOT NULL)),
    -- The charge the facts name, and only it: a reminder fee in fee, the
    -- compensation in compensation, neither on a fee-free letter.
    CONSTRAINT ck_reminders_charges CHECK (
        (fee_kind IS NULL OR ((fee_kind = 'reminder_fee') = (fee IS NOT NULL)
            AND (fee_kind = 'compensation') = (compensation IS NOT NULL)))
        AND fee > 0 AND compensation > 0),
    -- Paper is printed, e-mail is dispatched: only a paper letter awaits
    -- print, is printed or names a batch; only an e-mail is queued or failed.
    CONSTRAINT ck_reminders_channel_status CHECK (
        ((status IN ('awaiting_print', 'printed') OR print_batch_id IS NOT NULL) <= (channel = 'paper'))
        AND ((status IN ('queued', 'failed')) <= (channel = 'email'))),
    -- The floor under two runs over one invoice.
    CONSTRAINT uq_reminders_invoice_sequence UNIQUE (invoice_id, sequence),
    -- A waiver names its letter with its invoice (charge_waivers).
    CONSTRAINT uq_reminders_id_invoice UNIQUE (id, invoice_id)
);
-- What the worker claims.
CREATE INDEX ix_reminders_due ON invoices.reminders (next_attempt_at) WHERE status = 'queued';
CREATE INDEX ix_reminders_invoice ON invoices.reminders (invoice_id);
-- A run's letters (its page; GET /invoices/reminders?runId=).
CREATE INDEX ix_reminders_run ON invoices.reminders (run_id);
CREATE INDEX ix_reminders_batch ON invoices.reminders (print_batch_id) WHERE print_batch_id IS NOT NULL;
-- The paper page's and attention's letters.
CREATE INDEX ix_reminders_waiting ON invoices.reminders (status) WHERE status IN ('awaiting_print', 'printed', 'failed');

-- A letter belongs to an issued invoice — never a draft, never a credit
-- note — read FOR SHARE and then judged; the marker is read after the wait,
-- and a letter of an anonymised customer keeps no address (00035's
-- guard_delivery_insert shape).
-- +goose StatementBegin
CREATE FUNCTION invoices.guard_reminder_insert()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    parent_kind     text;
    parent_status   text;
    parent_customer integer;
BEGIN
    SELECT kind, status, customer_id INTO parent_kind, parent_status, parent_customer
    FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE;
    IF parent_kind IS DISTINCT FROM 'invoice' OR parent_status IS DISTINCT FROM 'issued' THEN
        RAISE EXCEPTION 'invoices: a reminder needs an issued invoice' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = parent_customer) THEN
        NEW.recipient := '';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_reminders_parent
    BEFORE INSERT ON invoices.reminders
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_reminder_insert();

-- A letter is never deleted. What makes it the letter it is — its invoice,
-- run, sequence, level, channel, language, creation and author, and any
-- column added later — never changes. Its recipient is only ever blanked,
-- and that alone passes in every status (the erase; D19, B1). Its message id
-- is set once, from NULL, while queued, and never changes after. Its facts
-- (which a failed attempt may clear again), held_reason, the attempt and
-- withdrawal columns and its status change only while it is queued, awaiting
-- print, printed or failed. Once sent, only its PDF's key and hash are set,
-- once; once withdrawn, nothing.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_reminder_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    changing_columns text[] := ARRAY['recipient', 'print_batch_id', 'announces_collection', 'sent_on',
        'deadline', 'regime', 'principal_open', 'fee_kind', 'fee', 'compensation', 'charges_earlier', 'interest',
        'interest_waived', 'interest_paid', 'interest_from', 'interest_segments', 'inkassosats', 'total',
        'charge_notes', 'pdf_object_key', 'pdf_sha256', 'message_id', 'sent_at', 'status', 'held_reason',
        'attempts', 'next_attempt_at', 'first_attempt_at', 'lease_id', 'lease_until', 'last_error', 'failed_at',
        'withdrawn_at', 'withdrawn_by_user_id', 'withdrawal_reason'];
    pdf_columns text[] := ARRAY['recipient', 'pdf_object_key', 'pdf_sha256'];
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a reminder is never deleted' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(OLD) - changing_columns) <> (to_jsonb(NEW) - changing_columns) THEN
        RAISE EXCEPTION 'invoices: a reminder keeps its identity' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.recipient <> OLD.recipient AND NEW.recipient <> '' THEN
        RAISE EXCEPTION 'invoices: a reminder''s recipient is only ever blanked' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.message_id IS DISTINCT FROM OLD.message_id
       AND (OLD.message_id IS NOT NULL OR OLD.status <> 'queued') THEN
        RAISE EXCEPTION 'invoices: a reminder''s message id is set once, while queued' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(OLD) - 'recipient') = (to_jsonb(NEW) - 'recipient') THEN
        RETURN NEW;
    END IF;
    IF OLD.status IN ('queued', 'awaiting_print', 'printed', 'failed') THEN
        RETURN NEW;
    END IF;
    IF OLD.status = 'sent' AND (to_jsonb(OLD) - pdf_columns) = (to_jsonb(NEW) - pdf_columns)
       AND (OLD.pdf_object_key IS NULL OR NEW.pdf_object_key IS NOT DISTINCT FROM OLD.pdf_object_key)
       AND (OLD.pdf_sha256 IS NULL OR NEW.pdf_sha256 IS NOT DISTINCT FROM OLD.pdf_sha256) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a reminder is final once sent or withdrawn' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_reminders_immutable
    BEFORE UPDATE OR DELETE ON invoices.reminders
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_reminder_change();

-- An invoice marked disputed (D11), one live per invoice. The lift answers
-- whether the objection was groundless (charges_allowed), with who and when,
-- together.
CREATE TABLE invoices.invoice_holds (
    id                bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id        bigint       NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    kind              varchar(10)  NOT NULL,
    note              varchar(500) NOT NULL,
    placed_at         timestamptz  NOT NULL,
    placed_by_user_id uuid         NOT NULL,
    lifted_at         timestamptz,
    lifted_by_user_id uuid,
    lift_note         varchar(500),
    charges_allowed   boolean,
    CONSTRAINT ck_invoice_holds_kind CHECK (kind = 'disputed'),
    CONSTRAINT ck_invoice_holds_lift CHECK ((lifted_at IS NULL) = (lifted_by_user_id IS NULL)
        AND (lifted_at IS NULL) = (charges_allowed IS NULL))
);
CREATE UNIQUE INDEX ux_invoice_holds_live ON invoices.invoice_holds (invoice_id) WHERE lifted_at IS NULL;

-- A hold is never deleted; an UPDATE is the lift, once, or the erase's
-- blanking of its notes. A lift of an anonymised customer's hold keeps no
-- lift note: the invoice is held FOR UPDATE by the lift, so the marker read
-- here sees an erase that committed before it (guard_child_of_issued_insert's
-- rule for the insert's note).
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_hold_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    changing_columns text[] := ARRAY['lifted_at', 'lifted_by_user_id', 'lift_note', 'charges_allowed', 'note'];
BEGIN
    IF TG_OP = 'UPDATE' AND (to_jsonb(OLD) - changing_columns) = (to_jsonb(NEW) - changing_columns)
       AND (NEW.note = OLD.note OR NEW.note = '')
       AND ((OLD.lifted_at IS NULL AND NEW.lifted_at IS NOT NULL)
            OR ((OLD.lifted_at, OLD.lifted_by_user_id, OLD.charges_allowed)
                    IS NOT DISTINCT FROM (NEW.lifted_at, NEW.lifted_by_user_id, NEW.charges_allowed)
                AND (NEW.lift_note IS NOT DISTINCT FROM OLD.lift_note OR NEW.lift_note = ''))) THEN
        IF OLD.lifted_at IS NULL AND NEW.lifted_at IS NOT NULL AND EXISTS (
            SELECT 1 FROM invoices.invoices i JOIN invoices.erased_customers e ON e.customer_id = i.customer_id
            WHERE i.id = NEW.invoice_id) THEN
            NEW.lift_note := '';
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a hold is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_invoice_holds_immutable
    BEFORE UPDATE OR DELETE ON invoices.invoice_holds
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_hold_change();
CREATE TRIGGER tr_invoice_holds_parent
    BEFORE INSERT ON invoices.invoice_holds
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_child_of_issued_insert();

-- The hand-off to a collection agency, recorded (D11), one live per invoice;
-- withdrawn with who, when and why together.
CREATE TABLE invoices.collection_handoffs (
    id                   bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id           bigint       NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    handed_on            date         NOT NULL,
    agency               varchar(200) NOT NULL,
    agency_reference     varchar(100) NOT NULL DEFAULT '',
    note                 varchar(500) NOT NULL DEFAULT '',
    created_at           timestamptz  NOT NULL,
    created_by_user_id   uuid         NOT NULL,
    withdrawn_on         date,
    withdrawn_by_user_id uuid,
    withdrawal_reason    varchar(200),
    CONSTRAINT ck_collection_handoffs_withdrawal CHECK (
        (withdrawn_on IS NULL AND withdrawn_by_user_id IS NULL AND withdrawal_reason IS NULL)
        OR (withdrawn_on IS NOT NULL AND withdrawn_by_user_id IS NOT NULL AND withdrawal_reason IS NOT NULL
            AND withdrawal_reason <> ''))
);
CREATE UNIQUE INDEX ux_collection_handoffs_live ON invoices.collection_handoffs (invoice_id) WHERE withdrawn_on IS NULL;

-- A hand-off is never deleted; an UPDATE is the withdrawal, once, or the
-- erase's blanking of its note — never a no-op (refuse_payment_change's rule).
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_handoff_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (to_jsonb(OLD) - 'withdrawn_on' - 'withdrawn_by_user_id' - 'withdrawal_reason' - 'note')
           = (to_jsonb(NEW) - 'withdrawn_on' - 'withdrawn_by_user_id' - 'withdrawal_reason' - 'note')
       AND (NEW.note = OLD.note OR NEW.note = '')
       AND ((OLD.withdrawn_on IS NULL AND NEW.withdrawn_on IS NOT NULL)
            OR (NEW.note = ''
                AND (OLD.withdrawn_on, OLD.withdrawn_by_user_id, OLD.withdrawal_reason)
                    IS NOT DISTINCT FROM (NEW.withdrawn_on, NEW.withdrawn_by_user_id, NEW.withdrawal_reason))) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a hand-off is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_collection_handoffs_immutable
    BEFORE UPDATE OR DELETE ON invoices.collection_handoffs
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_handoff_change();
CREATE TRIGGER tr_collection_handoffs_parent
    BEFORE INSERT ON invoices.collection_handoffs
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_child_of_issued_insert();

-- A charge a letter claimed, released (D9): a fee or the compensation whole,
-- once per letter; interest as an amount, as often as it is claimed again —
-- for interest, reminder_id is the latest sent letter that claimed it and
-- interest_through that letter's sent_on.
CREATE TABLE invoices.charge_waivers (
    id                bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id        bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    reminder_id       bigint        NOT NULL,
    kind              varchar(12)   NOT NULL,
    amount            numeric(14,2) NOT NULL,
    interest_through  date,
    reason            varchar(20)   NOT NULL,
    note              varchar(500)  NOT NULL DEFAULT '',
    waived_by_user_id uuid          NOT NULL,
    waived_at         timestamptz   NOT NULL,
    CONSTRAINT ck_charge_waivers_kind CHECK (kind IN ('fee', 'compensation', 'interest')),
    CONSTRAINT ck_charge_waivers_amount CHECK (amount > 0),
    CONSTRAINT ck_charge_waivers_reason CHECK (reason IN ('objection_upheld', 'claimed_in_error', 'goodwill', 'deadline_met')),
    CONSTRAINT ck_charge_waivers_interest_through CHECK ((kind = 'interest') = (interest_through IS NOT NULL)),
    -- The letter is the same invoice's.
    CONSTRAINT fk_charge_waivers_reminder FOREIGN KEY (reminder_id, invoice_id)
        REFERENCES invoices.reminders (id, invoice_id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX ux_charge_waivers_letter_kind ON invoices.charge_waivers (reminder_id, kind)
    WHERE kind IN ('fee', 'compensation');
CREATE INDEX ix_charge_waivers_invoice ON invoices.charge_waivers (invoice_id);

-- A waiver is insert-only but for the erase's blanking of its note.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_waiver_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.note = '' AND (to_jsonb(OLD) - 'note') = (to_jsonb(NEW) - 'note') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a charge waiver is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_charge_waivers_immutable
    BEFORE UPDATE OR DELETE ON invoices.charge_waivers
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_waiver_change();
CREATE TRIGGER tr_charge_waivers_parent
    BEFORE INSERT ON invoices.charge_waivers
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_child_of_issued_insert();

-- +goose Down
-- Everything 00041 added, in reverse: the tables (their triggers and indexes
-- go with them), the payments' columns, CHECKs and index — the user required
-- again, which fails while an imported payment without one exists, the
-- honest answer: a Down never deletes bookkeeping — then the bank tables and
-- the functions.
DROP TABLE invoices.charge_waivers;
DROP TABLE invoices.collection_handoffs;
DROP TABLE invoices.invoice_holds;
DROP TABLE invoices.reminders;
DROP TABLE invoices.reminder_print_batches;
DROP TABLE invoices.reminder_runs;
DROP TABLE invoices.charge_payments;
DROP TABLE invoices.manual_deliveries;
DROP TABLE invoices.customer_reminder_policies;
DROP TABLE invoices.reminder_settings;
DROP TABLE invoices.collection_rates;
DROP INDEX invoices.ix_payments_bank_transaction;
ALTER TABLE invoices.payments
    DROP CONSTRAINT ck_payments_origin,
    DROP CONSTRAINT ck_payments_source,
    ALTER COLUMN registered_by_user_id SET NOT NULL,
    DROP COLUMN bank_transaction_id,
    DROP COLUMN source;
DROP TABLE invoices.bank_transaction_events;
DROP TABLE invoices.bank_transactions;
DROP TABLE invoices.bank_files;
DROP TABLE invoices.bank_import_accounts;
DROP FUNCTION invoices.refuse_waiver_change();
DROP FUNCTION invoices.refuse_handoff_change();
DROP FUNCTION invoices.refuse_hold_change();
DROP FUNCTION invoices.refuse_reminder_change();
DROP FUNCTION invoices.guard_reminder_insert();
DROP FUNCTION invoices.refuse_print_batch_change();
DROP FUNCTION invoices.refuse_reminder_run_change();
DROP FUNCTION invoices.refuse_charge_payment_change();
DROP FUNCTION invoices.refuse_manual_delivery_change();
DROP FUNCTION invoices.guard_child_of_issued_insert();
DROP FUNCTION invoices.seed_collection_rate(text, date, numeric, text);
DROP FUNCTION invoices.refuse_collection_rate_change();
DROP FUNCTION invoices.refuse_bank_transaction_event_change();
DROP FUNCTION invoices.refuse_bank_transaction_change();
DROP FUNCTION invoices.refuse_bank_file_change();
DROP FUNCTION invoices.refuse_bank_import_account_change();
DROP FUNCTION invoices.refuse_row_delete();
