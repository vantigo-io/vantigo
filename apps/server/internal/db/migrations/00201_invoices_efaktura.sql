-- +goose Up
-- Invoices' eFaktura and AvtaleGiro through Mastercard Payment Services
-- (invoices phase 6 design I29-I35, delivery D): the consumer provider's
-- credentials, the consumer lookups, the AvtaleGiro mandates and claims, the
-- transmission's channel, and the settings' AvtaleGiro switch.

-- The consumer provider's credentials (I29, readings 6 and 7): one row, as
-- 00036's access_point_credentials. settings_json holds what is the tenant's
-- and not secret — the agreements' states, the AvtaleGiro creditor account,
-- the brand and abbreviated names. secret_ciphertext is the optional
-- per-creditor Mandate API certificate and key, sealed, '' when none: the
-- submitter's own certificate and user are the operator's files.
CREATE TABLE invoices.consumer_provider_credentials (
    id                integer     PRIMARY KEY,
    provider          varchar(20) NOT NULL,
    settings_json     text        NOT NULL DEFAULT '{}',
    secret_ciphertext text        NOT NULL DEFAULT '',
    rejected_at       timestamptz,
    updated_at        timestamptz NOT NULL,
    -- The worker's daily housekeeping (the mandate queue and the weekly
    -- lookups), claimed as a lease on this row so no worker holds a
    -- connection while it calls the provider.
    housekeeping_at          timestamptz,
    housekeeping_lease_until timestamptz,
    -- The X-Request-ID of the mandate page asked for and not yet stored: a
    -- lost answer or a failed store asks again under it, which replays the
    -- same page, and only a stored page clears it.
    mandate_request_id       uuid,
    CONSTRAINT ck_consumer_provider_single_row CHECK (id = 1),
    CONSTRAINT ck_consumer_provider_provider CHECK (provider IN ('mps'))
);

-- What the provider answered about a person (I30): the lookup is Invoices'
-- own, keyed by the customer. No name, phone or e-mail is copied; the
-- lookup's input is read from the billing profile each time. Sendable is
-- found AND (auto_accept OR has_agreement). The customer slots merge and
-- erase it.
CREATE TABLE invoices.consumer_lookups (
    customer_id         integer     PRIMARY KEY,
    status              varchar(12) NOT NULL,
    efaktura_identifier char(9),
    auto_accept         boolean     NOT NULL DEFAULT false,
    has_agreement       boolean     NOT NULL DEFAULT false,
    checked_at          timestamptz NOT NULL,
    next_check_at       timestamptz NOT NULL,
    CONSTRAINT ck_consumer_lookups_status CHECK (status IN ('found', 'not_found', 'blocked', 'ambiguous')),
    CONSTRAINT ck_consumer_lookups_identifier CHECK ((status = 'found') = (efaktura_identifier IS NOT NULL))
);
CREATE INDEX ix_consumer_lookups_due ON invoices.consumer_lookups (next_check_at);

-- The AvtaleGiro mandates (I32, readings 8 and 10), synced daily from the
-- provider's delta queue. The KID is the customer KID, '9' first; the
-- customer is NULL while the KID names no customer this module knows, and a
-- person assigns it. No limit is stored: the Mandate API answers none.
CREATE TABLE invoices.avtalegiro_mandates (
    id                  bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kid                 varchar(25) NOT NULL UNIQUE,
    customer_id         integer,
    status              varchar(10) NOT NULL,
    notify_by_mail      boolean     NOT NULL DEFAULT false,
    provider_seen_at    timestamptz NOT NULL,
    resolved_by_user_id uuid,
    -- A person's reason for assigning a mandate whose KID names another
    -- customer number.
    resolution_note     varchar(500),
    -- Where in the delta queue the last delivery stood: a later delivery
    -- has a higher position, so an older one never overwrites it.
    queue_position      bigint      NOT NULL DEFAULT 0,
    updated_at          timestamptz NOT NULL,
    CONSTRAINT ck_avtalegiro_mandates_status CHECK (status IN ('active', 'inactive', 'pending'))
);
CREATE INDEX ix_avtalegiro_mandates_customer ON invoices.avtalegiro_mandates (customer_id);

-- The transmission's channel (I31, reading 2). The nine Peppol columns lose
-- NOT NULL under a per-channel CHECK: an ehf row has all nine, an efaktura
-- row none. The eFaktura identity columns are frozen by
-- refuse_transmission_change's jsonb comparison; provider_status is a state
-- column, added to its list below.
ALTER TABLE invoices.transmissions
    ADD COLUMN channel             varchar(10) NOT NULL DEFAULT 'ehf',
    ADD COLUMN efaktura_identifier char(9),
    ADD COLUMN shipment_id         uuid,
    ADD COLUMN payment_type        varchar(30),
    ADD COLUMN pdf_object_key      varchar(300),
    ADD COLUMN provider_status     varchar(40),
    ALTER COLUMN sender_participant DROP NOT NULL,
    ALTER COLUMN receiver_participant DROP NOT NULL,
    ALTER COLUMN document_type DROP NOT NULL,
    ALTER COLUMN process_id DROP NOT NULL,
    ALTER COLUMN ubl_object_key DROP NOT NULL,
    ALTER COLUMN ubl_sha256 DROP NOT NULL,
    ALTER COLUMN lookup_registered DROP NOT NULL,
    ALTER COLUMN lookup_can_receive DROP NOT NULL,
    ALTER COLUMN lookup_at DROP NOT NULL,
    DROP CONSTRAINT ck_transmissions_provider,
    ADD CONSTRAINT ck_transmissions_provider CHECK (provider IN ('storecove', 'mps')),
    ADD CONSTRAINT ck_transmissions_channel_value CHECK (channel IN ('ehf', 'efaktura')),
    ADD CONSTRAINT ck_transmissions_channel CHECK (
        (channel = 'ehf'
         AND sender_participant IS NOT NULL AND receiver_participant IS NOT NULL
         AND document_type IS NOT NULL AND process_id IS NOT NULL
         AND ubl_object_key IS NOT NULL AND ubl_sha256 IS NOT NULL
         AND lookup_registered IS NOT NULL AND lookup_can_receive IS NOT NULL AND lookup_at IS NOT NULL
         AND efaktura_identifier IS NULL AND shipment_id IS NULL AND payment_type IS NULL
         AND pdf_object_key IS NULL)
        OR (channel = 'efaktura'
         AND sender_participant IS NULL AND receiver_participant IS NULL
         AND document_type IS NULL AND process_id IS NULL
         AND ubl_object_key IS NULL AND ubl_sha256 IS NULL
         AND lookup_registered IS NULL AND lookup_can_receive IS NULL AND lookup_at IS NULL
         AND efaktura_identifier IS NOT NULL AND shipment_id IS NOT NULL
         AND payment_type IN ('efaktura', 'both') AND pdf_object_key IS NOT NULL));

-- One live transmission per document and channel: an EHF and an eFaktura are
-- different sends.
DROP INDEX invoices.ux_transmissions_active;
CREATE UNIQUE INDEX ux_transmissions_active ON invoices.transmissions (invoice_id, channel)
    WHERE status IN ('queued', 'submitted', 'delivered', 'unconfirmed');

-- 00036's function with provider_status among the state columns; nothing
-- else changes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_transmission_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    state_columns text[] := ARRAY['status', 'provider_ref', 'evidence_object_key', 'evidence_sha256',
        'submit_attempts', 'poll_attempts', 'next_attempt_at', 'submit_attempted_at', 'lease_id',
        'lease_until', 'last_error', 'lookup_registered', 'lookup_can_receive', 'lookup_at',
        'submitted_at', 'delivered_at', 'failed_at', 'cancelled_at', 'resolved_by_user_id',
        'resolution_note', 'provider_status'];
    evidence_columns text[] := ARRAY['evidence_object_key', 'evidence_sha256', 'lease_id', 'lease_until',
        'next_attempt_at', 'poll_attempts', 'last_error'];
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a transmission is never deleted' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(OLD) - state_columns) = (to_jsonb(NEW) - state_columns)
       AND OLD.status NOT IN ('failed', 'cancelled')
       AND (OLD.status <> 'delivered'
            OR (OLD.evidence_object_key IS NULL
                AND (to_jsonb(OLD) - evidence_columns) = (to_jsonb(NEW) - evidence_columns))) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a transmission is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

-- The AvtaleGiro claim (I33, I34): one per eFaktura sent as both, at most one
-- live per invoice. The amount, due date and KID are what was sent and never
-- change; the rest is its state. cancel_requested is the credit note's ask
-- (reading 15), which the invoices-efaktura worker carries to the provider.
CREATE TABLE invoices.avtalegiro_claims (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id          bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    transmission_id     bigint        NOT NULL REFERENCES invoices.transmissions (id) ON DELETE RESTRICT,
    mandate_id          bigint        NOT NULL REFERENCES invoices.avtalegiro_mandates (id) ON DELETE RESTRICT,
    amount              numeric(14,2) NOT NULL,
    due_date            date          NOT NULL,
    kid                 varchar(25)   NOT NULL,
    status              varchar(20)   NOT NULL DEFAULT 'submitted',
    attention           boolean       NOT NULL DEFAULT false,
    rejection_code      varchar(40),
    rejection_text      varchar(500),
    next_attempt_at     timestamptz,
    lease_id            varchar(100),
    lease_until         timestamptz,
    submitted_at        timestamptz   NOT NULL,
    accepted_at         timestamptz,
    rejected_at         timestamptz,
    cancel_requested_at timestamptz,
    cancel_refused_at   timestamptz,
    cancelled_at        timestamptz,
    paid_at             timestamptz,
    unpaid_at           timestamptz,
    -- unpaid: accepted, and no debit matched within five business days of
    -- the due date; the ordinary reminders take over. A late debit still
    -- pays it.
    CONSTRAINT ck_avtalegiro_claims_status CHECK (status IN ('submitted', 'accepted', 'rejected', 'cancel_requested', 'cancelled', 'paid', 'unpaid')),
    CONSTRAINT ck_avtalegiro_claims_amount CHECK (amount > 0),
    CONSTRAINT ck_avtalegiro_claims_stamps CHECK (
        (status <> 'accepted' OR accepted_at IS NOT NULL) AND
        (status <> 'rejected' OR rejected_at IS NOT NULL) AND
        (status <> 'cancel_requested' OR cancel_requested_at IS NOT NULL) AND
        (status <> 'cancelled' OR cancelled_at IS NOT NULL) AND
        (status <> 'paid' OR paid_at IS NOT NULL) AND
        (status <> 'unpaid' OR unpaid_at IS NOT NULL))
);
CREATE UNIQUE INDEX ux_avtalegiro_claims_live ON invoices.avtalegiro_claims (invoice_id)
    WHERE status IN ('submitted', 'accepted', 'cancel_requested');
CREATE INDEX ix_avtalegiro_claims_mandate ON invoices.avtalegiro_claims (mandate_id, status, due_date);
CREATE INDEX ix_avtalegiro_claims_cancel ON invoices.avtalegiro_claims (next_attempt_at)
    WHERE status = 'cancel_requested';

-- A claim is never deleted, and an UPDATE may change only its state columns:
-- the transmissions' shape. A cancelled, rejected or paid claim is final.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_avtalegiro_claim_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    state_columns text[] := ARRAY['status', 'attention', 'rejection_code', 'rejection_text',
        'next_attempt_at', 'lease_id', 'lease_until', 'accepted_at', 'rejected_at',
        'cancel_requested_at', 'cancel_refused_at', 'cancelled_at', 'paid_at', 'unpaid_at'];
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: an AvtaleGiro claim is never deleted' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(OLD) - state_columns) = (to_jsonb(NEW) - state_columns)
       AND OLD.status NOT IN ('rejected', 'cancelled', 'paid') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: an AvtaleGiro claim is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_avtalegiro_claims_immutable
    BEFORE UPDATE OR DELETE ON invoices.avtalegiro_claims
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_avtalegiro_claim_change();

-- The AvtaleGiro switch (I32, reading 12), and the customer KIDs' reserved
-- space: set the first time AvtaleGiro is turned on and never cleared, since
-- customer KIDs have been handed out. While it is set the issue caps invoice
-- numbers one digit below the KID agreement's, so no invoice KID begins with
-- 9 — AvtaleGiro on or off.
ALTER TABLE invoices.settings
    ADD COLUMN avtalegiro_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN kid_space_reserved boolean NOT NULL DEFAULT false;

-- The customer KID the PDF prints for a person while AvtaleGiro is on
-- (reading 11), frozen on issue by 00034's whole-row trigger.
ALTER TABLE invoices.invoices
    ADD COLUMN buyer_avtalegiro_kid varchar(25);

-- +goose Down
ALTER TABLE invoices.invoices DROP COLUMN buyer_avtalegiro_kid;
ALTER TABLE invoices.settings DROP COLUMN kid_space_reserved, DROP COLUMN avtalegiro_enabled;
DROP TABLE invoices.avtalegiro_claims;
DROP FUNCTION invoices.refuse_avtalegiro_claim_change();

-- 00036's function as it was.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_transmission_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    state_columns text[] := ARRAY['status', 'provider_ref', 'evidence_object_key', 'evidence_sha256',
        'submit_attempts', 'poll_attempts', 'next_attempt_at', 'submit_attempted_at', 'lease_id',
        'lease_until', 'last_error', 'lookup_registered', 'lookup_can_receive', 'lookup_at',
        'submitted_at', 'delivered_at', 'failed_at', 'cancelled_at', 'resolved_by_user_id',
        'resolution_note'];
    evidence_columns text[] := ARRAY['evidence_object_key', 'evidence_sha256', 'lease_id', 'lease_until',
        'next_attempt_at', 'poll_attempts', 'last_error'];
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a transmission is never deleted' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(OLD) - state_columns) = (to_jsonb(NEW) - state_columns)
       AND OLD.status NOT IN ('failed', 'cancelled')
       AND (OLD.status <> 'delivered'
            OR (OLD.evidence_object_key IS NULL
                AND (to_jsonb(OLD) - evidence_columns) = (to_jsonb(NEW) - evidence_columns))) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a transmission is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

-- The eFaktura rows go first: the restored NOT NULLs and CHECK hold for EHF
-- rows only. The trigger refuses their DELETE, so it is set aside for it.
ALTER TABLE invoices.transmissions DISABLE TRIGGER tr_transmissions_immutable;
DELETE FROM invoices.transmissions WHERE channel = 'efaktura';
ALTER TABLE invoices.transmissions ENABLE TRIGGER tr_transmissions_immutable;
DROP INDEX invoices.ux_transmissions_active;
CREATE UNIQUE INDEX ux_transmissions_active ON invoices.transmissions (invoice_id)
    WHERE status IN ('queued', 'submitted', 'delivered', 'unconfirmed');
ALTER TABLE invoices.transmissions
    DROP CONSTRAINT ck_transmissions_channel,
    DROP CONSTRAINT ck_transmissions_channel_value,
    DROP CONSTRAINT ck_transmissions_provider,
    ADD CONSTRAINT ck_transmissions_provider CHECK (provider IN ('storecove')),
    ALTER COLUMN sender_participant SET NOT NULL,
    ALTER COLUMN receiver_participant SET NOT NULL,
    ALTER COLUMN document_type SET NOT NULL,
    ALTER COLUMN process_id SET NOT NULL,
    ALTER COLUMN ubl_object_key SET NOT NULL,
    ALTER COLUMN ubl_sha256 SET NOT NULL,
    ALTER COLUMN lookup_registered SET NOT NULL,
    ALTER COLUMN lookup_can_receive SET NOT NULL,
    ALTER COLUMN lookup_at SET NOT NULL,
    DROP COLUMN provider_status,
    DROP COLUMN pdf_object_key,
    DROP COLUMN payment_type,
    DROP COLUMN shipment_id,
    DROP COLUMN efaktura_identifier,
    DROP COLUMN channel;
DROP TABLE invoices.avtalegiro_mandates;
DROP TABLE invoices.consumer_lookups;
DROP TABLE invoices.consumer_provider_credentials;
