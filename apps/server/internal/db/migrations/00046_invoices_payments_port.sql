-- +goose Up
-- Invoices phase 4, pull request 2 (invoices payments and reminders design
-- D2's third branch, D14-D17, sub-phase 4C): the whole pull request's schema
-- at once — the payment provider's credentials, the payment settings, pay
-- links, payment attempts, a Vipps payment's origin on the payments and the
-- day's reconciliations — so no later task of the pull request adds a
-- migration (it amends this one in place while it is unreleased). Every
-- query over these tables takes the time as a parameter from Deps.Clock(),
-- never now(); the one now() here is the migration's own stamp on the row it
-- seeds. customer_id is opaque, as everywhere in this schema. No column is
-- named with a word PostgreSQL reserves; a schema test pins it.

-- A row that is never changed: attached BEFORE UPDATE OR DELETE, it refuses
-- both — a day's reconciliation's.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_row_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    RAISE EXCEPTION 'invoices: a row of % is never changed', TG_TABLE_NAME USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

-- The payment provider's credentials (D14): one row, phase 2's
-- access_point_credentials shape. settings_json holds what is not secret
-- (the sales unit, {"msn": "…"}); secret_ciphertext the sealed JSON
-- {clientId, clientSecret, subscriptionKey}; the webhook's id and its sealed
-- secret come together. rejected_at is set when Vipps refused the keys and
-- cleared by the next call it accepts, or by a new PUT. A DELETE is the
-- API's, refused while an attempt is live.
CREATE TABLE invoices.payment_provider_credentials (
    id                        integer      PRIMARY KEY,
    provider                  varchar(20)  NOT NULL,
    settings_json             text         NOT NULL DEFAULT '{}',
    secret_ciphertext         text         NOT NULL,
    webhook_id                varchar(100),
    webhook_secret_ciphertext text,
    rejected_at               timestamptz,
    updated_at                timestamptz  NOT NULL,
    CONSTRAINT ck_payment_provider_credentials_single_row CHECK (id = 1),
    CONSTRAINT ck_payment_provider_credentials_provider CHECK (provider IN ('vipps')),
    CONSTRAINT ck_payment_provider_credentials_webhook CHECK ((webhook_id IS NULL) = (webhook_secret_ciphertext IS NULL))
);

-- The payment settings (D14): one row. reference_prefix is six random base36
-- characters made here, once, and every attempt's reference starts with it
-- (M4), so two installations never share a reference at Vipps; it never
-- changes. Pay links need both URLs (Vipps' merchantLegalLinks); on-site
-- payment needs the kontantsalg acknowledgement, who and when, together.
CREATE TABLE invoices.payment_settings (
    id                                  smallint     PRIMARY KEY,
    reference_prefix                    char(6)      NOT NULL,
    pay_links_enabled                   boolean      NOT NULL DEFAULT false,
    terms_url                           varchar(500),
    privacy_url                         varchar(500),
    on_site_payments_enabled            boolean      NOT NULL DEFAULT false,
    kontantsalg_acknowledged_by_user_id uuid,
    kontantsalg_acknowledged_at         timestamptz,
    revision                            integer      NOT NULL DEFAULT 1,
    updated_at                          timestamptz  NOT NULL,
    updated_by_user_id                  uuid,
    CONSTRAINT ck_payment_settings_single_row CHECK (id = 1),
    CONSTRAINT ck_payment_settings_reference_prefix CHECK (reference_prefix ~ '^[0-9a-z]{6}$'),
    CONSTRAINT ck_payment_settings_acknowledgement CHECK (
        (kontantsalg_acknowledged_at IS NULL) = (kontantsalg_acknowledged_by_user_id IS NULL)),
    CONSTRAINT ck_payment_settings_on_site CHECK (NOT on_site_payments_enabled OR kontantsalg_acknowledged_at IS NOT NULL),
    CONSTRAINT ck_payment_settings_pay_links CHECK (
        NOT pay_links_enabled OR (terms_url IS NOT NULL AND privacy_url IS NOT NULL))
);

-- The row is never deleted and its reference prefix never changes.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_payment_settings_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.reference_prefix = OLD.reference_prefix THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: the payment settings row is never deleted, and its reference prefix never changes'
        USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_payment_settings_kept
    BEFORE UPDATE OR DELETE ON invoices.payment_settings
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_payment_settings_change();

-- The prefix from random(), not gen_random_bytes(): no pgcrypto (m13).
INSERT INTO invoices.payment_settings (id, reference_prefix, updated_at)
SELECT 1, string_agg(substr('0123456789abcdefghijklmnopqrstuvwxyz', 1 + floor(random() * 36)::integer, 1), ''), now()
FROM generate_series(1, 6);

-- An invoice's pay link (D15): the token's SHA-256 only, never the token; the
-- URL sealed, so it can be printed again. One live link per invoice; revoked
-- once — by a user, or by an erase, which has none.
CREATE TABLE invoices.pay_links (
    id                 bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id         bigint      NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    token_sha256       char(64)    NOT NULL,
    url_ciphertext     text        NOT NULL,
    created_at         timestamptz NOT NULL,
    created_by_user_id uuid,
    revoked_at         timestamptz,
    revoked_by_user_id uuid,
    CONSTRAINT uq_pay_links_token UNIQUE (token_sha256),
    CONSTRAINT ck_pay_links_revocation CHECK (revoked_by_user_id IS NULL OR revoked_at IS NOT NULL)
);
CREATE UNIQUE INDEX ux_pay_links_live ON invoices.pay_links (invoice_id) WHERE revoked_at IS NULL;

-- A pay link or a payment attempt belongs to an issued invoice — never a
-- draft, never a credit note — read FOR SHARE and then judged
-- (guard_child_of_issued_insert's shape); the marker is then read in a
-- statement of its own, which sees an erase that committed while this insert
-- waited. Where that guard blanks a note, this one refuses: a link to, or a
-- payment by, an anonymised buyer must never exist (plan reading 19). That
-- refusal's message is a fixed key, the transmissions' own, so the issue can
-- tell it from any other and skip the link.
-- +goose StatementBegin
CREATE FUNCTION invoices.guard_payment_child_insert()
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
        RAISE EXCEPTION 'invoices: the customer is anonymised' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- A pay link is never deleted; an UPDATE is the revocation, once, and
-- nothing else — never a no-op.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_pay_link_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (to_jsonb(OLD) - 'revoked_at' - 'revoked_by_user_id') = (to_jsonb(NEW) - 'revoked_at' - 'revoked_by_user_id')
       AND OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a pay link is immutable but for its revocation, once' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_pay_links_immutable
    BEFORE UPDATE OR DELETE ON invoices.pay_links
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_pay_link_change();
CREATE TRIGGER tr_pay_links_parent
    BEFORE INSERT ON invoices.pay_links
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_payment_child_insert();

-- One Vipps ePayment of an invoice (D16): made by the pay page (pay_link) or
-- on site (on_site), polled by the invoices-payments worker under a lease.
-- msn is the sales unit it was made under (I20); the Idempotency-Keys are
-- persisted before each call; reserved_amount is the capture decided and not
-- yet registered or disproved, live while capturing or capture_unknown and
-- counted in the invoice's open amount; release_pending marks a captured or
-- failed capture whose uncaptured rest is not yet released (I19).
-- redirect_url is sensitive and never logged.
CREATE TABLE invoices.payment_attempts (
    id                       bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id               bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    provider                 varchar(20)   NOT NULL,
    msn                      varchar(10)   NOT NULL,
    return_nonce_sha256      char(64),
    flow                     varchar(10)   NOT NULL,
    pay_link_id              bigint        REFERENCES invoices.pay_links (id) ON DELETE RESTRICT,
    reference                varchar(64)   NOT NULL,
    amount                   numeric(14,2) NOT NULL,
    create_key               uuid,
    capture_key              uuid,
    cancel_key               uuid          NOT NULL,
    state                    varchar(16)   NOT NULL,
    release_pending          boolean       NOT NULL DEFAULT false,
    psp_reference            varchar(100),
    redirect_url             varchar(2500),
    authorized_amount        numeric(14,2),
    captured_amount          numeric(14,2),
    cancelled_amount         numeric(14,2),
    refunded_amount          numeric(14,2),
    reserved_amount          numeric(14,2),
    capture_psp_reference    varchar(100),
    capture_guaranteed_until timestamptz,
    created_at               timestamptz   NOT NULL,
    created_by_user_id       uuid,
    expires_at               timestamptz   NOT NULL,
    authorized_at            timestamptz,
    captured_at              timestamptz,
    finished_at              timestamptz,
    refund_watch_until       timestamptz,
    next_poll_at             timestamptz,
    polls                    integer       NOT NULL DEFAULT 0,
    capture_attempts         integer       NOT NULL DEFAULT 0,
    lease_id                 varchar(100),
    lease_until              timestamptz,
    last_error               varchar(500),
    abandoned_by_user_id     uuid,
    abandoned_at             timestamptz,
    abandon_reason           varchar(500),
    CONSTRAINT uq_payment_attempts_reference UNIQUE (reference),
    CONSTRAINT ck_payment_attempts_provider CHECK (provider = 'vipps'),
    CONSTRAINT ck_payment_attempts_amount CHECK (amount >= 1),
    CONSTRAINT ck_payment_attempts_state CHECK (state IN ('creating', 'created', 'authorized', 'capturing',
        'capture_unknown', 'captured', 'cancelling', 'cancelled', 'aborted', 'expired', 'terminated',
        'capture_failed', 'failed', 'abandoned')),
    -- A pay-link attempt names its link and its return nonce; an on-site one
    -- neither.
    CONSTRAINT ck_payment_attempts_flow CHECK (flow IN ('pay_link', 'on_site')
        AND (flow = 'pay_link') = (pay_link_id IS NOT NULL)
        AND (flow = 'pay_link') = (return_nonce_sha256 IS NOT NULL)),
    -- The reservation is live exactly while capturing or capture_unknown, and
    -- is then above zero and no more than was authorized.
    CONSTRAINT ck_payment_attempts_reserved CHECK (
        (reserved_amount IS NOT NULL) = (state IN ('capturing', 'capture_unknown'))
        AND (reserved_amount IS NULL
             OR (reserved_amount > 0 AND authorized_amount IS NOT NULL AND reserved_amount <= authorized_amount))),
    CONSTRAINT ck_payment_attempts_release CHECK (NOT release_pending OR state IN ('captured', 'capture_failed')),
    CONSTRAINT ck_payment_attempts_abandon CHECK (
        (state = 'abandoned' AND abandoned_by_user_id IS NOT NULL AND abandoned_at IS NOT NULL
            AND abandon_reason IS NOT NULL AND abandon_reason <> '')
        OR (state <> 'abandoned' AND abandoned_by_user_id IS NULL AND abandoned_at IS NULL AND abandon_reason IS NULL)),
    CONSTRAINT ck_payment_attempts_reference CHECK (reference ~ '^[a-zA-Z0-9-]{8,64}$')
);
-- One live on-site attempt per invoice (D17's "one payment, one invoice").
CREATE UNIQUE INDEX ux_payment_attempts_on_site_live ON invoices.payment_attempts (invoice_id)
    WHERE flow = 'on_site' AND state IN ('creating', 'created', 'authorized', 'capturing', 'capture_unknown');
-- What the worker claims: the live states, a release pending, a refund watch.
CREATE INDEX ix_payment_attempts_due ON invoices.payment_attempts (next_poll_at)
    WHERE state IN ('creating', 'created', 'authorized', 'capturing', 'capture_unknown', 'cancelling')
       OR release_pending OR (state = 'captured' AND refund_watch_until IS NOT NULL);
CREATE INDEX ix_payment_attempts_invoice ON invoices.payment_attempts (invoice_id);
-- The day's list (D17).
CREATE INDEX ix_payment_attempts_captured_on ON invoices.payment_attempts ((captured_at));

-- An attempt's reference is <prefix>-<invoice number>-<its own id> (M4, I1):
-- set before the insert, from the identity's value, which PostgreSQL applies
-- before BEFORE row triggers fire — so id stays GENERATED ALWAYS and no
-- caller makes it; a supplied reference is overwritten. Fires after
-- tr_payment_attempts_parent (triggers fire in name order), which has judged
-- the invoice issued, so its number is there.
-- +goose StatementBegin
CREATE FUNCTION invoices.set_payment_attempt_reference()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    prefix         text;
    invoice_number bigint;
BEGIN
    SELECT reference_prefix INTO prefix FROM invoices.payment_settings WHERE id = 1;
    SELECT number INTO invoice_number FROM invoices.invoices WHERE id = NEW.invoice_id;
    NEW.reference := prefix || '-' || invoice_number || '-' || NEW.id;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- An attempt is never deleted. What makes it the attempt it is never
-- changes. Its create key is set once, from NULL; its capture key is set
-- from NULL and replaced only before its first capture was attempted (the
-- re-decision, m3). A live attempt's state columns change as the worker
-- learns; a terminal one (captured, cancelled, aborted, expired, terminated,
-- capture_failed, failed, abandoned) changes only the worker's bookkeeping —
-- its lease, next poll, polls and last error, and its finish, set once from
-- NULL — its release — pending true to false, with the cancelled amount —
-- and, captured, its refunded amount and refund watch: the release of the
-- uncaptured rest and the refund watch keep polling a finished attempt.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_payment_attempt_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    terminal_free text[] := ARRAY['lease_id', 'lease_until', 'next_poll_at', 'polls', 'last_error', 'finished_at',
        'release_pending', 'cancelled_amount', 'refunded_amount', 'refund_watch_until'];
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a payment attempt is never deleted' USING ERRCODE = 'P0001';
    END IF;
    IF (OLD.id, OLD.invoice_id, OLD.provider, OLD.msn, OLD.flow, OLD.pay_link_id, OLD.return_nonce_sha256,
        OLD.reference, OLD.amount, OLD.cancel_key, OLD.created_at, OLD.created_by_user_id)
       IS DISTINCT FROM
       (NEW.id, NEW.invoice_id, NEW.provider, NEW.msn, NEW.flow, NEW.pay_link_id, NEW.return_nonce_sha256,
        NEW.reference, NEW.amount, NEW.cancel_key, NEW.created_at, NEW.created_by_user_id) THEN
        RAISE EXCEPTION 'invoices: a payment attempt keeps its identity' USING ERRCODE = 'P0001';
    END IF;
    IF OLD.state IN ('captured', 'cancelled', 'aborted', 'expired', 'terminated', 'capture_failed', 'failed', 'abandoned') THEN
        IF (to_jsonb(OLD) - terminal_free) <> (to_jsonb(NEW) - terminal_free)
           OR (OLD.finished_at IS NOT NULL AND NEW.finished_at IS DISTINCT FROM OLD.finished_at)
           OR (NEW.release_pending AND NOT OLD.release_pending)
           OR (OLD.release_pending AND NOT NEW.release_pending AND NEW.cancelled_amount IS NULL)
           OR (NEW.cancelled_amount IS DISTINCT FROM OLD.cancelled_amount
               AND NOT (OLD.release_pending AND NOT NEW.release_pending))
           OR ((NEW.refunded_amount, NEW.refund_watch_until) IS DISTINCT FROM (OLD.refunded_amount, OLD.refund_watch_until)
               AND OLD.state <> 'captured') THEN
            RAISE EXCEPTION 'invoices: a payment attempt is final once %', OLD.state USING ERRCODE = 'P0001';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.create_key IS DISTINCT FROM OLD.create_key AND OLD.create_key IS NOT NULL THEN
        RAISE EXCEPTION 'invoices: a payment attempt''s create key is set once' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.capture_key IS DISTINCT FROM OLD.capture_key AND OLD.capture_key IS NOT NULL AND OLD.capture_attempts > 0 THEN
        RAISE EXCEPTION 'invoices: a payment attempt''s capture key is replaced only before its first capture'
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_payment_attempts_immutable
    BEFORE UPDATE OR DELETE ON invoices.payment_attempts
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_payment_attempt_change();
CREATE TRIGGER tr_payment_attempts_parent
    BEFORE INSERT ON invoices.payment_attempts
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_payment_child_insert();
CREATE TRIGGER tr_payment_attempts_reference
    BEFORE INSERT ON invoices.payment_attempts
    FOR EACH ROW EXECUTE FUNCTION invoices.set_payment_attempt_reference();

-- A Vipps payment's origin (D2's third branch): a capture registered by the
-- worker names its attempt — one capture, one payment — and has neither a
-- user nor a bank line; no other source names an attempt.
-- tr_payments_immutable's jsonb comparison freezes the new column with the
-- rest. charge_payments is untouched: a capture pays principal only.
ALTER TABLE invoices.payments
    ADD COLUMN payment_attempt_id bigint UNIQUE REFERENCES invoices.payment_attempts (id) ON DELETE RESTRICT,
    DROP CONSTRAINT ck_payments_source,
    DROP CONSTRAINT ck_payments_origin;
ALTER TABLE invoices.payments
    ADD CONSTRAINT ck_payments_source CHECK (source IN ('manual', 'ocr', 'camt054', 'vipps')),
    ADD CONSTRAINT ck_payments_origin CHECK (
        (source = 'manual' AND bank_transaction_id IS NULL AND registered_by_user_id IS NOT NULL
            AND payment_attempt_id IS NULL)
        OR (source IN ('ocr', 'camt054') AND bank_transaction_id IS NOT NULL AND registered_by_user_id IS NOT NULL
            AND payment_attempt_id IS NULL)
        OR (source = 'vipps' AND payment_attempt_id IS NOT NULL AND bank_transaction_id IS NULL
            AND registered_by_user_id IS NULL));

-- A day of Vipps payments reconciled (D17, M15): recorded once, immutably.
CREATE TABLE invoices.vipps_day_reconciliations (
    day                   date          PRIMARY KEY,
    payments              integer       NOT NULL,
    total                 numeric(14,2) NOT NULL,
    on_site_total         numeric(14,2) NOT NULL,
    pay_link_total        numeric(14,2) NOT NULL,
    reconciled_by_user_id uuid          NOT NULL,
    reconciled_at         timestamptz   NOT NULL,
    note                  varchar(500)  NOT NULL DEFAULT ''
);

CREATE TRIGGER tr_vipps_day_reconciliations_immutable
    BEFORE UPDATE OR DELETE ON invoices.vipps_day_reconciliations
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_row_change();

-- A quick invoice's request id (D17; after review): the client's own key for
-- one quick invoice, so a retry after a lost answer finds the invoice the
-- first request issued instead of issuing a second. A table of its own, not
-- a column on invoices.invoices: the key is a fact about the request, not
-- the sales document, so the document's row, its immutability trigger and
-- every read and export of it stay as they are. body_sha256 is the request
-- as first sent; the same id with another body is refused. Inserted in the
-- quick invoice's transaction right after its draft, so a second request
-- with the id waits on the key and finds the first's; never changed.
CREATE TABLE invoices.quick_requests (
    request_id  uuid        PRIMARY KEY,
    invoice_id  bigint      NOT NULL UNIQUE REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    body_sha256 char(64)    NOT NULL,
    created_at  timestamptz NOT NULL,
    CONSTRAINT ck_quick_requests_body CHECK (body_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE TRIGGER tr_quick_requests_immutable
    BEFORE UPDATE OR DELETE ON invoices.quick_requests
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_row_change();


-- A reminder letter's pay link (D10, D15; after review): one of its facts,
-- the invoice's live link judged under the invoice's lock when the facts are
-- written, cleared with them, and — as every fact — frozen once the letter is
-- sent. The letter prints that link's URL whether or not it was revoked
-- later, so a letter renders the same bytes every time and a re-download of
-- a posted batch is what was posted; a link revoked before the facts are
-- written is never a letter's. refuse_reminder_change (00041's) is replaced
-- with the column among the facts and the link required to be the letter's
-- own invoice's; the Down restores 00041's.
ALTER TABLE invoices.reminders
    ADD COLUMN pay_link_id bigint REFERENCES invoices.pay_links (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_reminders_pay_link CHECK (pay_link_id IS NULL OR sent_on IS NOT NULL);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_reminder_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    changing_columns text[] := ARRAY['recipient', 'print_batch_id', 'announces_collection', 'sent_on',
        'deadline', 'regime', 'principal_open', 'credited', 'fee_kind', 'fee', 'compensation', 'charges_earlier',
        'interest', 'interest_waived', 'interest_paid', 'interest_from', 'interest_segments', 'inkassosats', 'total',
        'charge_notes', 'pdf_object_key', 'pdf_sha256', 'message_id', 'sent_at', 'status', 'held_reason',
        'attempts', 'next_attempt_at', 'first_attempt_at', 'lease_id', 'lease_until', 'last_error', 'failed_at',
        'withdrawn_at', 'withdrawn_by_user_id', 'withdrawal_reason', 'pay_link_id'];
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
    IF NEW.pay_link_id IS NOT NULL AND NEW.pay_link_id IS DISTINCT FROM OLD.pay_link_id
       AND NOT EXISTS (SELECT 1 FROM invoices.pay_links WHERE id = NEW.pay_link_id AND invoice_id = NEW.invoice_id) THEN
        RAISE EXCEPTION 'invoices: a reminder''s pay link is its own invoice''s' USING ERRCODE = 'P0001';
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

-- +goose Down
-- Everything 00046 added, in reverse: the quick requests, the letters' pay
-- link with 00041's refuse_reminder_change restored, the reconciliations,
-- the payments' attempt with 00041's two CHECKs restored — which fails while a Vipps
-- payment exists, the honest answer: a Down never deletes bookkeeping —
-- then the attempts, the links, the settings, the credentials (their
-- triggers and indexes go with them) and the functions.
DROP TABLE invoices.quick_requests;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_reminder_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    changing_columns text[] := ARRAY['recipient', 'print_batch_id', 'announces_collection', 'sent_on',
        'deadline', 'regime', 'principal_open', 'credited', 'fee_kind', 'fee', 'compensation', 'charges_earlier',
        'interest', 'interest_waived', 'interest_paid', 'interest_from', 'interest_segments', 'inkassosats', 'total',
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
ALTER TABLE invoices.reminders DROP COLUMN pay_link_id;
DROP TABLE invoices.vipps_day_reconciliations;
ALTER TABLE invoices.payments
    DROP CONSTRAINT ck_payments_origin,
    DROP CONSTRAINT ck_payments_source;
ALTER TABLE invoices.payments
    ADD CONSTRAINT ck_payments_source CHECK (source IN ('manual', 'ocr', 'camt054')),
    ADD CONSTRAINT ck_payments_origin CHECK (
        (source = 'manual' AND bank_transaction_id IS NULL AND registered_by_user_id IS NOT NULL)
        OR (source IN ('ocr', 'camt054') AND bank_transaction_id IS NOT NULL AND registered_by_user_id IS NOT NULL)),
    DROP COLUMN payment_attempt_id;
DROP TABLE invoices.payment_attempts;
DROP TABLE invoices.pay_links;
DROP TABLE invoices.payment_settings;
DROP TABLE invoices.payment_provider_credentials;
DROP FUNCTION invoices.refuse_payment_attempt_change();
DROP FUNCTION invoices.set_payment_attempt_reference();
DROP FUNCTION invoices.refuse_pay_link_change();
DROP FUNCTION invoices.guard_payment_child_insert();
DROP FUNCTION invoices.refuse_payment_settings_change();
DROP FUNCTION invoices.refuse_row_change();
