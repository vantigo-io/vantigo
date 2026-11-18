-- +goose Up
-- Outgoing webhooks of integrations (outgoing webhooks design W1, W8, W9,
-- W23, W30, W35, W37, docs/superpowers/specs/2026-11-18-outgoing-webhooks-design.md):
-- an integration's endpoints, the deliveries fanned out to them and every
-- attempt to send one. Only identity's webhooks service and its worker write
-- them. Every table of the feature is here, so the sender and the log add
-- none.

-- An endpoint (W1): an HTTPS URL an Owner gave, the catalogue events it
-- receives and the secret its messages are signed with, sealed by
-- secrets.Box under identity/webhook-secret (W8). While a roll's overlap
-- runs, the previous secret signs too until previous_secret_expires_at (W9).
-- disabled_reason says why an endpoint is disabled and is null exactly while
-- it is enabled. failing_since, last_success_at and disabled_notice_sent_at
-- are the sender's (W18, W32). Deleting an endpoint deletes its deliveries;
-- the audit row keeps the fact (W24). An integration is archived, never
-- deleted (K8), so RESTRICT never meets a delete.
CREATE TABLE identity.webhook_endpoints (
    id                          uuid PRIMARY KEY,
    integration_id              uuid          NOT NULL REFERENCES identity.integrations (user_id) ON DELETE RESTRICT,
    url                         varchar(2048) NOT NULL,
    description                 varchar(500),
    events                      text[]        NOT NULL,
    secret_ciphertext           bytea         NOT NULL,
    previous_secret_ciphertext  bytea,
    previous_secret_expires_at  timestamptz,
    status                      varchar(10)   NOT NULL DEFAULT 'enabled',
    disabled_reason             varchar(30),
    disabled_at                 timestamptz,
    failing_since               timestamptz,
    last_success_at             timestamptz,
    disabled_notice_sent_at     timestamptz,
    created_by                  uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    created_at                  timestamptz   NOT NULL,
    updated_at                  timestamptz   NOT NULL,
    version                     uuid          NOT NULL,
    CONSTRAINT ck_webhook_endpoints_url CHECK (url LIKE 'https://%'),
    -- 1 to 50 names, none NULL or empty; Go keeps them distinct and sorted.
    CONSTRAINT ck_webhook_endpoints_events CHECK (
        cardinality(events) BETWEEN 1 AND 50 AND array_position(events, NULL) IS NULL AND array_position(events, '') IS NULL),
    CONSTRAINT ck_webhook_endpoints_status CHECK (status IN ('enabled', 'disabled')),
    CONSTRAINT ck_webhook_endpoints_reason CHECK (
        disabled_reason IS NULL OR disabled_reason IN ('manual', 'failing', 'gone', 'integration_archived')),
    CONSTRAINT ck_webhook_endpoints_disabled CHECK (
        (status = 'enabled') = (disabled_reason IS NULL) AND (status = 'enabled') = (disabled_at IS NULL)),
    CONSTRAINT ck_webhook_endpoints_previous CHECK (
        (previous_secret_ciphertext IS NULL) = (previous_secret_expires_at IS NULL))
);
CREATE INDEX ix_webhook_endpoints_integration ON identity.webhook_endpoints (integration_id, created_at);
CREATE INDEX ix_webhook_endpoints_created_by ON identity.webhook_endpoints (created_by) WHERE created_by IS NOT NULL;
-- The fan-out reads the enabled endpoints that list an event (W14).
CREATE INDEX ix_webhook_endpoints_events ON identity.webhook_endpoints USING gin (events) WHERE status = 'enabled';

-- A delivery (W23): one message to one endpoint, its body fixed at fan-out
-- so a resend sends the same bytes (W12). event_id is the outbox event's id
-- and has no FK, since platform.events prunes on its own schedule; a ping
-- has none (W37), which the unique index tolerates. A run is max_run_attempts
-- attempts, 8 for an event and 1 for a ping (W17, W19); state_reason says
-- why a delivery was skipped or is dead. lease_id and lease_until are the
-- sender's claim (W29). resend_count and resend_window_start are the resend
-- limit (W35). completed_at is set when a delivery leaves pending, on insert
-- for a skipped one (W30).
CREATE TABLE identity.webhook_deliveries (
    id                  uuid PRIMARY KEY,
    endpoint_id         uuid         NOT NULL REFERENCES identity.webhook_endpoints (id) ON DELETE CASCADE,
    event_id            bigint,
    event_name          varchar(100) NOT NULL,
    kind                varchar(10)  NOT NULL,
    body                text         NOT NULL,
    state               varchar(10)  NOT NULL DEFAULT 'pending',
    state_reason        varchar(30),
    run_attempts        integer      NOT NULL DEFAULT 0,
    max_run_attempts    integer      NOT NULL,
    total_attempts      integer      NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz,
    lease_id            uuid,
    lease_until         timestamptz,
    last_status         smallint,
    last_error          varchar(30),
    resend_count        integer      NOT NULL DEFAULT 0,
    resend_window_start timestamptz,
    created_at          timestamptz  NOT NULL,
    completed_at        timestamptz,
    CONSTRAINT ck_webhook_deliveries_kind CHECK (kind IN ('event', 'ping') AND (kind = 'ping') = (event_id IS NULL)),
    CONSTRAINT ck_webhook_deliveries_state CHECK (state IN ('pending', 'succeeded', 'dead', 'skipped')),
    -- A reason belongs to its state: none while pending or succeeded,
    -- permission_not_held for a skipped delivery, why for a dead one.
    CONSTRAINT ck_webhook_deliveries_reason CHECK (
        (state IN ('pending', 'succeeded') AND state_reason IS NULL)
        OR (state = 'skipped' AND state_reason = 'permission_not_held')
        OR (state = 'dead' AND state_reason IN ('attempts_exhausted', 'gone', 'endpoint_disabled', 'integration_archived', 'expired'))),
    -- A pending delivery is always due at some time.
    CONSTRAINT ck_webhook_deliveries_due CHECK (state <> 'pending' OR next_attempt_at IS NOT NULL),
    CONSTRAINT ck_webhook_deliveries_completed CHECK ((state = 'pending') = (completed_at IS NULL)),
    CONSTRAINT ck_webhook_deliveries_attempts CHECK (
        max_run_attempts BETWEEN 1 AND 8 AND run_attempts BETWEEN 0 AND max_run_attempts AND total_attempts >= run_attempts),
    CONSTRAINT ck_webhook_deliveries_error CHECK (last_error IS NULL OR last_error IN (
        'timeout', 'connection_refused', 'dns', 'tls', 'blocked_address', 'redirect', 'http_status', 'too_large', 'other')),
    CONSTRAINT ck_webhook_deliveries_resends CHECK (resend_count >= 0)
);
-- The outbox is at-least-once: a second fan-out of one event to one
-- endpoint is a no-op (W14).
CREATE UNIQUE INDEX ux_webhook_deliveries_event ON identity.webhook_deliveries (endpoint_id, event_id);
-- The sender's claim: the due deliveries of an endpoint, ranked by creation
-- (W29).
CREATE INDEX ix_webhook_deliveries_due ON identity.webhook_deliveries (state, next_attempt_at) WHERE state = 'pending';
-- The claim ranks an endpoint's pending deliveries by creation (W29).
CREATE INDEX ix_webhook_deliveries_pending_rank ON identity.webhook_deliveries (endpoint_id, created_at, id) WHERE state = 'pending';
CREATE INDEX ix_webhook_deliveries_endpoint ON identity.webhook_deliveries (endpoint_id, created_at DESC, id);
-- Pruning reads by age, whatever the state (W30).
CREATE INDEX ix_webhook_deliveries_created ON identity.webhook_deliveries (created_at);

-- An attempt (W16, W23): when, the status the receiver answered, how long
-- it took, an error from a closed set (never the network stack's words) and
-- at most 512 bytes of a non-2xx answer. secret_used is whether the current
-- secret signed alone or with the previous one, during a roll's overlap.
CREATE TABLE identity.webhook_attempts (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    delivery_id uuid        NOT NULL REFERENCES identity.webhook_deliveries (id) ON DELETE CASCADE,
    at          timestamptz NOT NULL,
    status_code smallint,
    duration_ms integer     NOT NULL,
    error       varchar(30),
    excerpt     text,
    secret_used varchar(10) NOT NULL,
    CONSTRAINT ck_webhook_attempts_error CHECK (error IS NULL OR error IN (
        'timeout', 'connection_refused', 'dns', 'tls', 'blocked_address', 'redirect', 'http_status', 'too_large', 'other')),
    CONSTRAINT ck_webhook_attempts_excerpt CHECK (excerpt IS NULL OR octet_length(excerpt) <= 512),
    CONSTRAINT ck_webhook_attempts_secret CHECK (secret_used IN ('current', 'both')),
    CONSTRAINT ck_webhook_attempts_duration CHECK (duration_ms >= 0)
);
CREATE INDEX ix_webhook_attempts_delivery ON identity.webhook_attempts (delivery_id, at);

-- +goose Down
DROP TABLE identity.webhook_attempts;
DROP TABLE identity.webhook_deliveries;
DROP TABLE identity.webhook_endpoints;
