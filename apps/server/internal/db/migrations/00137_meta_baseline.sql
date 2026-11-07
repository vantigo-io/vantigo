-- +goose Up
-- The meta module (Messenger design N9–N12a,
-- docs/superpowers/specs/2026-11-05-communications-messenger-design.md; delivery
-- B, docs/superpowers/plans/2026-11-05-communications-messenger-plan.md): the
-- connection to Meta that fills contracts.ChannelMessenger under the key meta,
-- Facebook Messenger through a Facebook Page the business owns.
--
-- meta.settings is the installation's one row, seeded here: the Meta app's id,
-- its secret sealed under meta/app-secret, the verify token Meta's handshake
-- presents (generated; the Owner can regenerate it), the Human Agent switch
-- and the status line — when the last post with a valid signature arrived,
-- when the last one without arrived, and the last refusal of the Graph API.
--
-- meta.pages are the Pages connected, each with its access token sealed under
-- meta/page-token and a state: active, or token_rejected once Meta refused the
-- token (code 190).
--
-- meta.webhook_events is the raw queue: the body of each post whose signature
-- held, stored before Meta is answered. The splitting worker claims a row for
-- a lease (next_attempt_at), splits it into meta.inbound, and marks it split.
-- Rows older than seven days are deleted.
--
-- meta.inbound is the provider's inbox that Communications reads from a cursor
-- (id, a bigserial: never a timestamp, because webhooks arrive out of order)
-- and acknowledges: one row per message, postback, delivery or read event,
-- keyed for dedup by (page_id, dedup_key). storage_keys are the attachments
-- fetched from Meta's CDN at once, because those URLs expire, under the storage
-- scope meta-inbound. Acknowledged rows go after seven days with their files;
-- a row never acknowledged goes after thirty, which is what bounds the module's
-- personal data (N12a).
--
-- meta.profiles caches a participant's name, by Page and PSID, for at most
-- thirty days.
CREATE SCHEMA meta;

CREATE TABLE meta.settings (
    id                     smallint     PRIMARY KEY CHECK (id = 1),
    app_id                 varchar(40),
    app_secret_sealed      bytea,
    verify_token           varchar(64)  NOT NULL,
    human_agent_granted    boolean      NOT NULL DEFAULT false,
    last_webhook_at        timestamptz,
    last_rejected_at       timestamptz,
    last_graph_error_code  varchar(40),
    last_graph_error       varchar(200),
    last_graph_error_at    timestamptz,
    updated_at             timestamptz  NOT NULL,
    updated_by_user_id     uuid
);

CREATE TABLE meta.pages (
    page_id             varchar(30)  PRIMARY KEY CHECK (page_id ~ '^[0-9]{1,30}$'),
    name                varchar(200) NOT NULL,
    token_sealed        bytea        NOT NULL,
    state               varchar(20)  NOT NULL DEFAULT 'active',
    last_error          varchar(60),
    added_at            timestamptz  NOT NULL,
    added_by_user_id    uuid,
    checked_at          timestamptz,
    CONSTRAINT pages_state_check CHECK (state IN ('active', 'token_rejected'))
);

CREATE TABLE meta.webhook_events (
    id               bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    received_at      timestamptz  NOT NULL,
    payload          jsonb        NOT NULL,
    state            varchar(10)  NOT NULL DEFAULT 'pending',
    attempts         integer      NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz  NOT NULL,
    split_at         timestamptz,
    last_error       varchar(200),
    CONSTRAINT webhook_events_state_check CHECK (state IN ('pending', 'split', 'failed'))
);

CREATE INDEX ix_meta_webhook_events_pending ON meta.webhook_events (next_attempt_at) WHERE state = 'pending';
CREATE INDEX ix_meta_webhook_events_received ON meta.webhook_events (received_at);

CREATE TABLE meta.inbound (
    id            bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    page_id       varchar(30)  NOT NULL,
    kind          varchar(10)  NOT NULL,
    dedup_key     varchar(200) NOT NULL,
    participant   varchar(40)  NOT NULL,
    occurred_at   timestamptz  NOT NULL,
    payload       jsonb        NOT NULL,
    storage_keys  text[]       NOT NULL DEFAULT '{}',
    created_at    timestamptz  NOT NULL,
    acked_at      timestamptz,
    CONSTRAINT inbound_kind_check CHECK (kind IN ('message', 'delivered', 'read')),
    CONSTRAINT ux_inbound_page_dedup UNIQUE (page_id, dedup_key)
);

CREATE INDEX ix_meta_inbound_unacked ON meta.inbound (page_id, id) WHERE acked_at IS NULL;
CREATE INDEX ix_meta_inbound_created ON meta.inbound (created_at);

CREATE TABLE meta.profiles (
    page_id       varchar(30)  NOT NULL,
    psid          varchar(40)  NOT NULL,
    display_name  varchar(200) NOT NULL,
    fetched_at    timestamptz  NOT NULL,
    PRIMARY KEY (page_id, psid)
);

CREATE INDEX ix_meta_profiles_fetched ON meta.profiles (fetched_at);

INSERT INTO meta.settings (id, verify_token, updated_at)
VALUES (1, replace(gen_random_uuid()::text || gen_random_uuid()::text, '-', ''), now());

-- Two repairs to the meta module's queues (code review of the Messenger delivery).
--
-- A webhook body whose signature held and that is valid JSON can still be one
-- Postgres refuses as jsonb (an escaped NUL in a string is "untranslatable").
-- Answering 503 would make Meta retry it for ever, so the bytes are kept in
-- raw instead, in a row that is born failed ('unstorable_json') and is never
-- claimed; retention deletes it with the other raw events. payload is
-- therefore nullable, and one of the two is always set.
--
-- meta.pending_files stages the key of an attachment before it is written to
-- the object store, and is cleared in the transaction that inserts the
-- meta.inbound row that names it. A key that stays staged for a day with no
-- inbound row naming it is a file whose transaction never happened (a crash, a
-- rollback): retention deletes the object. The GIN index is for that "does a
-- row name this key" question.
ALTER TABLE meta.webhook_events
    ALTER COLUMN payload DROP NOT NULL,
    ADD COLUMN raw bytea,
    ADD CONSTRAINT webhook_events_body_check CHECK (payload IS NOT NULL OR raw IS NOT NULL);

CREATE TABLE meta.pending_files (
    storage_key  text         PRIMARY KEY,
    created_at   timestamptz  NOT NULL
);

CREATE INDEX ix_meta_pending_files_created ON meta.pending_files (created_at);
CREATE INDEX ix_meta_inbound_storage_keys ON meta.inbound USING gin (storage_keys);

-- +goose Down
-- The schema goes whole: the app's settings, the sealed secret and tokens, the
-- queued events and the cached names. Nothing at Meta is touched; the app and
-- its webhook subscription stay until the Owner removes them there.
DROP SCHEMA meta CASCADE;
