-- +goose Up
-- The domain-events outbox (orders design O1–O3,
-- docs/superpowers/specs/2026-11-05-orders-design.md): process-wide
-- infrastructure owned by no module, beside platform.rate_limit.
--
-- platform.events is what a module published, inserted by events.Publish on
-- the publisher's own transaction, so an event commits with the state change
-- that caused it or not at all. name is "<module>.<aggregate>.<what>", version
-- the payload's wire version (a breaking change is a new version), module the
-- publisher, aggregate_id the aggregate's id as text, for ordering and lookup.
-- The payload carries ids, numbers, amounts and states, never personal data
-- (O3), so a person's erasure needs no sweep of this table.
CREATE TABLE platform.events (
    id           bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name         varchar(100) NOT NULL,
    version      smallint     NOT NULL CHECK (version > 0),
    module       varchar(40)  NOT NULL,
    aggregate_id text         NOT NULL,
    payload      jsonb        NOT NULL,
    occurred_at  timestamptz  NOT NULL,
    created_at   timestamptz  NOT NULL DEFAULT now()
);
-- Ordering per aggregate: deliveries to one handler are tried in event id
-- order per aggregate_id (O3).
CREATE INDEX ix_events_aggregate_id ON platform.events (aggregate_id, id);
-- The retention prune reads by age (O3, 30 days).
CREATE INDEX ix_events_occurred_at ON platform.events (occurred_at);

-- One row per (event, handler), written by the dispatcher when it first
-- claims the event, so the handler list is fixed then (O3). state is pending,
-- done, failed (retrying) or dead (after 8 attempts, until the operator's
-- retry or skip, O4). lease_id and lease_until are the claim of
-- communications.outbox_jobs (00006). aggregate_id is the event's, copied so
-- the claim orders and blocks per (handler, aggregate) without a join.
CREATE TABLE platform.event_deliveries (
    id              bigint        GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id        bigint        NOT NULL REFERENCES platform.events (id) ON DELETE CASCADE,
    handler         varchar(100)  NOT NULL,
    aggregate_id    text          NOT NULL,
    state           varchar(10)   NOT NULL DEFAULT 'pending',
    attempts        integer       NOT NULL DEFAULT 0,
    next_attempt_at timestamptz   NOT NULL,
    lease_id        varchar(100),
    lease_until     timestamptz,
    last_error      varchar(2000),
    note            varchar(500),
    completed_at    timestamptz,
    created_at      timestamptz   NOT NULL,
    updated_at      timestamptz   NOT NULL,
    CONSTRAINT ck_event_deliveries_state CHECK (state IN ('pending', 'done', 'failed', 'dead')),
    CONSTRAINT ux_event_deliveries_event_handler UNIQUE (event_id, handler)
);
-- The claim: due deliveries by state and next attempt.
CREATE INDEX ix_event_deliveries_state_next_attempt_at ON platform.event_deliveries (state, next_attempt_at);
-- Ordering and blocking per handler and aggregate: an earlier delivery not
-- done holds a later one back.
CREATE INDEX ix_event_deliveries_handler_aggregate ON platform.event_deliveries (handler, aggregate_id, event_id)
    WHERE state <> 'done';

-- +goose Down
DROP TABLE platform.event_deliveries;
DROP TABLE platform.events;
