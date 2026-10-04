-- +goose Up
-- Invoices phase 2 (EHF over Peppol, and KID; design D2, D3, D7, D9): the
-- whole phase's schema at once — the seller's Peppol id and the KID agreement
-- on the settings, the KID on the document, the access point's credentials
-- and the transmissions — so no later task of the phase adds a migration.
-- Every existing table keeps what 00034 and 00035 gave it. No column is named
-- with a word PostgreSQL reserves; a schema test pins it.

-- The seller's own Peppol participant id (D2), the sender's address on the
-- network at the time of sending — never part of the seller snapshot — and
-- the KID agreement the bank made (D3): a length of 4-25 digits including
-- the check digit and the check digit's algorithm, a pair or nothing. The
-- second branch names both as NOT NULL: a half-set pair would otherwise make
-- the whole expression NULL, which a CHECK lets through.
ALTER TABLE invoices.settings
    ADD COLUMN peppol_id     varchar(60),
    ADD COLUMN kid_length    smallint,
    ADD COLUMN kid_algorithm varchar(5),
    ADD CONSTRAINT ck_settings_kid CHECK (
        (kid_length IS NULL AND kid_algorithm IS NULL)
        OR (kid_length IS NOT NULL AND kid_algorithm IS NOT NULL
            AND kid_length BETWEEN 4 AND 25 AND kid_algorithm IN ('mod10', 'mod11')));

-- An existing installation is ready without re-saving: a Norwegian seller's
-- Peppol id is 0192 and its organisation number (D2).
UPDATE invoices.settings SET peppol_id = '0192:' || organisation_number
    WHERE organisation_number ~ '^[0-9]{9}$' AND peppol_id IS NULL;

-- The document's KID and the algorithm it was computed with (D3), set in the
-- draft→issued update and from then on frozen by 00034's trigger, which
-- compares the whole row. All or none, and never on a credit note.
ALTER TABLE invoices.invoices
    ADD COLUMN kid           varchar(25),
    ADD COLUMN kid_algorithm varchar(5),
    ADD CONSTRAINT ck_invoices_kid CHECK ((kid IS NULL) = (kid_algorithm IS NULL)),
    ADD CONSTRAINT ck_invoices_kid_algorithm CHECK (kid_algorithm IS NULL OR kid_algorithm IN ('mod10', 'mod11')),
    ADD CONSTRAINT ck_invoices_kid_kind CHECK (kid IS NULL OR kind = 'invoice');

-- The access point's credentials (D7): one row, off the settings row every
-- issue reads FOR SHARE. settings_json holds what is not secret (Storecove's
-- legalEntityId); secret_ciphertext the API key sealed by the secrets box.
-- rejected_at is set when the provider refused the key and cleared by the
-- next call it accepts, or by a new PUT.
CREATE TABLE invoices.access_point_credentials (
    id                integer     PRIMARY KEY,
    provider          varchar(20) NOT NULL,
    settings_json     text        NOT NULL DEFAULT '{}',
    secret_ciphertext text        NOT NULL,
    rejected_at       timestamptz,
    updated_at        timestamptz NOT NULL,
    CONSTRAINT ck_access_point_single_row CHECK (id = 1),
    CONSTRAINT ck_access_point_provider CHECK (provider IN ('storecove'))
);

-- One EHF transmission of an issued document (D9): an outbox row the
-- invoices-ehf worker claims under a lease. Identity, the parties, the
-- document type and the UBL's key and hashes are what was sent and never
-- change; the rest is its state. submit_attempted_at is the crash marker,
-- stamped immediately before the provider is called. Every query over this
-- table takes the time as a parameter from Deps.Clock(), never now().
CREATE TABLE invoices.transmissions (
    id                   bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id           bigint       NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    provider             varchar(20)  NOT NULL,
    idempotency_key      uuid         NOT NULL UNIQUE,
    sender_participant   varchar(60)  NOT NULL,
    receiver_participant varchar(100) NOT NULL,
    document_type        varchar(300) NOT NULL,
    process_id           varchar(100) NOT NULL,
    ubl_object_key       varchar(300) NOT NULL,
    ubl_sha256           char(64)     NOT NULL,
    pdf_sha256           char(64)     NOT NULL,
    status               varchar(20)  NOT NULL DEFAULT 'queued',
    provider_ref         varchar(200),
    evidence_object_key  varchar(300),
    evidence_sha256      char(64),
    submit_attempts      integer      NOT NULL DEFAULT 0,
    poll_attempts        integer      NOT NULL DEFAULT 0,
    next_attempt_at      timestamptz  NOT NULL,
    submit_attempted_at  timestamptz,
    lease_id             varchar(100),
    lease_until          timestamptz,
    last_error           varchar(500),
    lookup_registered    boolean      NOT NULL,
    lookup_can_receive   boolean      NOT NULL,
    lookup_at            timestamptz  NOT NULL,
    queued_at            timestamptz  NOT NULL,
    submitted_at         timestamptz,
    delivered_at         timestamptz,
    failed_at            timestamptz,
    cancelled_at         timestamptz,
    resolved_by_user_id  uuid,
    resolution_note      varchar(500),
    created_by_user_id   uuid         NOT NULL,
    CONSTRAINT ck_transmissions_provider CHECK (provider IN ('storecove')),
    CONSTRAINT ck_transmissions_status CHECK (status IN ('queued', 'submitted', 'delivered', 'failed', 'unconfirmed', 'cancelled')),
    CONSTRAINT ck_transmissions_stamps CHECK (
        (status <> 'submitted' OR submitted_at IS NOT NULL) AND
        (status <> 'delivered' OR delivered_at IS NOT NULL) AND
        (status <> 'failed' OR failed_at IS NOT NULL) AND
        (status <> 'cancelled' OR cancelled_at IS NOT NULL)),
    -- A person's resolution of an unconfirmed row says who and why; the
    -- worker's own (the provider answered at last) has a note and no user.
    CONSTRAINT ck_transmissions_resolution CHECK (resolved_by_user_id IS NULL OR resolution_note IS NOT NULL),
    CONSTRAINT ck_transmissions_evidence CHECK ((evidence_object_key IS NULL) = (evidence_sha256 IS NULL))
);
-- One live transmission per document (D8): queued, submitted, delivered or
-- unconfirmed blocks a new send; failed and cancelled do not.
CREATE UNIQUE INDEX ux_transmissions_active ON invoices.transmissions (invoice_id)
    WHERE status IN ('queued', 'submitted', 'delivered', 'unconfirmed');
-- What the worker claims (D9): every row still in flight, and a delivered
-- row with a reference until its evidence is stored.
CREATE INDEX ix_transmissions_due ON invoices.transmissions (status, next_attempt_at)
    WHERE status IN ('queued', 'submitted', 'unconfirmed')
       OR (status = 'delivered' AND evidence_object_key IS NULL AND provider_ref IS NOT NULL);

-- A transmission belongs to an issued document, an invoice or a credit note,
-- and never to a customer this module has anonymised. The document is read
-- FOR SHARE, so an insert waits for an erase holding the customer's
-- documents FOR UPDATE, and only then is the marker read, in a statement of
-- its own that sees an erase committed during the wait — 00035's
-- guard_delivery_insert shape. On INSERT only: the worker's claim takes no
-- document lock.
-- +goose StatementBegin
CREATE FUNCTION invoices.guard_transmission_insert()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    parent_status   text;
    parent_customer integer;
BEGIN
    SELECT status, customer_id INTO parent_status, parent_customer
    FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE;
    IF parent_status IS DISTINCT FROM 'issued' THEN
        RAISE EXCEPTION 'invoices: a transmission needs an issued document' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = parent_customer) THEN
        RAISE EXCEPTION 'invoices: the customer is anonymised' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_transmissions_parent
    BEFORE INSERT ON invoices.transmissions
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_transmission_insert();

-- A transmission is never deleted. An UPDATE may change only the state
-- columns — the row as jsonb less them never changes, so a column added
-- later is frozen without touching this function. A failed or cancelled row
-- changes nothing. A delivered row keeps its status and delivered_at, and
-- while its evidence is not stored takes only the lease, the cadence
-- (next_attempt_at, poll_attempts, last_error) and the evidence's key and
-- hash once; with its evidence stored it is final.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_transmission_change()
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

CREATE TRIGGER tr_transmissions_immutable
    BEFORE UPDATE OR DELETE ON invoices.transmissions
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_transmission_change();

-- +goose Down
-- Everything 00036 added: the tables (their triggers and indexes go with
-- them), the trigger functions, then the document's and the settings'
-- columns with their CHECKs.
DROP TABLE invoices.transmissions;
DROP TABLE invoices.access_point_credentials;
DROP FUNCTION invoices.refuse_transmission_change();
DROP FUNCTION invoices.guard_transmission_insert();
ALTER TABLE invoices.invoices
    DROP CONSTRAINT ck_invoices_kid_kind,
    DROP CONSTRAINT ck_invoices_kid_algorithm,
    DROP CONSTRAINT ck_invoices_kid,
    DROP COLUMN kid_algorithm,
    DROP COLUMN kid;
ALTER TABLE invoices.settings
    DROP CONSTRAINT ck_settings_kid,
    DROP COLUMN kid_algorithm,
    DROP COLUMN kid_length,
    DROP COLUMN peppol_id;
