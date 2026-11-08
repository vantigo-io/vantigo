-- +goose Up
-- The agent at work (virtual employees design, delivery A2: V13, V14, V17,
-- V20, V24, V25): the run requests the events write, the hand-off markers, the
-- runs with their transcripts and action logs, the review items a run leaves
-- for a person (decided by A3) and the day's usage. Every conversation, channel,
-- customer and user here is another module's, kept as an opaque id (module
-- boundaries rule 4).

-- The Communications tags that mark a complaint (S5), per role.
ALTER TABLE agents.roles ADD COLUMN complaint_tag_ids uuid[] NOT NULL DEFAULT '{}';

-- A request for a run (V13). Coalesced per (role, conversation) while not yet
-- taken: the partial unique index holds one, and a second message updates its
-- not_before. due_by is first-message + 2 minutes, which not_before never
-- passes. A request that is taken stays (taken_at, run_id) until retention.
-- attempt is 2 for a run re-queued after a lapsed claim.
CREATE TABLE agents.run_queue (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    role_id         uuid        NOT NULL REFERENCES agents.roles (id) ON DELETE CASCADE,
    conversation_id uuid        NOT NULL,
    reason          varchar(12) NOT NULL CHECK (reason IN ('message', 'assigned', 'retry')),
    attempt         smallint    NOT NULL DEFAULT 1,
    not_before      timestamptz NOT NULL,
    due_by          timestamptz NOT NULL,
    created_at      timestamptz NOT NULL,
    taken_at        timestamptz,
    run_id          uuid
);
CREATE UNIQUE INDEX ux_run_queue_untaken ON agents.run_queue (role_id, conversation_id) WHERE taken_at IS NULL;
CREATE INDEX ix_run_queue_due ON agents.run_queue (not_before) WHERE taken_at IS NULL;

-- A conversation the agent handed to the people (V13): the trigger ignores it
-- until a person replies in it or assigns it, or it is given back.
CREATE TABLE agents.handed_off (
    conversation_id uuid        PRIMARY KEY,
    role_id         uuid        NOT NULL,
    at              timestamptz NOT NULL,
    reason          text        NOT NULL
);

-- A run (V14). One run per role and conversation at a time (the partial unique
-- index). customer_id is the run's customer scope (V9b), kept for erasure.
-- claimed_by is the replica, claimed_until renewed every 30 seconds. outcome says
-- what the run ended in: sent, review, handed_off or no_reply; sent_at stamps a
-- sent reply for the per-day count (S7). A run in state handed_off whose outcome
-- is still null has its hand-off to do (the note, the assignee): a worker cannot
-- reach Communications, so the API process completes it.
CREATE TABLE agents.runs (
    id              uuid         PRIMARY KEY,
    agent_id        uuid         NOT NULL REFERENCES agents.agents (id),
    role_id         uuid         NOT NULL,
    conversation_id uuid         NOT NULL,
    customer_id     integer,
    reason          varchar(12)  NOT NULL,
    attempt         smallint     NOT NULL DEFAULT 1,
    state           varchar(14)  NOT NULL CHECK (state IN ('queued', 'running', 'done', 'waiting_review', 'handed_off', 'failed', 'stopped')),
    outcome         varchar(12),
    error_code      varchar(40),
    summary         text,
    claimed_by      uuid,
    claimed_until   timestamptz,
    tool_calls      integer      NOT NULL DEFAULT 0,
    model_turns     integer      NOT NULL DEFAULT 0,
    input_tokens    bigint       NOT NULL DEFAULT 0,
    output_tokens   bigint       NOT NULL DEFAULT 0,
    started_at      timestamptz  NOT NULL,
    ended_at        timestamptz,
    sent_at         timestamptz
);
CREATE UNIQUE INDEX ux_runs_active ON agents.runs (role_id, conversation_id) WHERE state IN ('queued', 'running');
CREATE INDEX ix_runs_conversation ON agents.runs (conversation_id, started_at);
CREATE INDEX ix_runs_lapsed ON agents.runs (claimed_until) WHERE state = 'running';

-- A run's transcript (V14): what the model was told and answered, and what its
-- tools answered. Bodies of messages are copied here, so retention (V26) deletes
-- them with the run.
CREATE TABLE agents.run_messages (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id       uuid        NOT NULL REFERENCES agents.runs (id) ON DELETE CASCADE,
    role         varchar(12) NOT NULL,
    text         text        NOT NULL DEFAULT '',
    tool_calls   jsonb,
    tool_call_id text,
    created_at   timestamptz NOT NULL
);
CREATE INDEX ix_run_messages_run ON agents.run_messages (run_id, id);

-- The agent's action log (V25): one row per tool call, never a body. ref is the
-- operation and the ids it named (getInvoicesById 2026-0931) or an article id:
-- what V17's sources are checked against. scoped says the operation returns a
-- customer's data (S1).
CREATE TABLE agents.actions (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id        uuid        NOT NULL REFERENCES agents.runs (id) ON DELETE CASCADE,
    agent_id      uuid        NOT NULL,
    customer_id   integer,
    operation_id  text        NOT NULL,
    ref           text        NOT NULL DEFAULT '',
    scoped        boolean     NOT NULL DEFAULT false,
    mode          varchar(12) NOT NULL DEFAULT '',
    status        varchar(12) NOT NULL DEFAULT '',
    http_status   integer,
    duration_ms   integer     NOT NULL DEFAULT 0,
    approved_by   uuid,
    created_at    timestamptz NOT NULL
);
CREATE INDEX ix_actions_run ON agents.actions (run_id, id);

-- What a run leaves for a person (V20), decided by A3: kind reply (item holds
-- text, components and attachment references) or change (the operation, its
-- arguments and the preview). score, sources and model_score are V17's; held_by
-- the rule codes that held it. expires_at is 24 hours, or a Messenger reply's
-- window less 15 minutes.
CREATE TABLE agents.review_items (
    id                 uuid        PRIMARY KEY,
    run_id             uuid        NOT NULL REFERENCES agents.runs (id) ON DELETE CASCADE,
    agent_id           uuid        NOT NULL,
    role_id            uuid        NOT NULL,
    conversation_id    uuid        NOT NULL,
    customer_id        integer,
    kind               varchar(8)  NOT NULL CHECK (kind IN ('reply', 'change')),
    item               jsonb       NOT NULL,
    score              smallint    NOT NULL CHECK (score BETWEEN 0 AND 100),
    sources            jsonb       NOT NULL DEFAULT '[]',
    model_score        smallint    NOT NULL CHECK (model_score BETWEEN 0 AND 100),
    held_by            text[]      NOT NULL DEFAULT '{}',
    state              varchar(12) NOT NULL DEFAULT 'waiting' CHECK (state IN ('waiting', 'sent', 'edited_sent', 'discarded', 'withdrawn', 'expired')),
    version            integer     NOT NULL DEFAULT 1,
    decided_by_user_id uuid,
    decided_at         timestamptz,
    discard_reason     varchar(20),
    discard_text       text,
    edit_ratio         real,
    outcome            text,
    expires_at         timestamptz NOT NULL,
    created_at         timestamptz NOT NULL
);
CREATE INDEX ix_review_items_waiting ON agents.review_items (expires_at) WHERE state = 'waiting';
CREATE INDEX ix_review_items_conversation ON agents.review_items (conversation_id, created_at);
CREATE INDEX ix_review_items_role ON agents.review_items (role_id, created_at);

-- The day's usage per agent (V24), on the business day; counts only, kept 24 months.
CREATE TABLE agents.usage_days (
    agent_id      uuid    NOT NULL REFERENCES agents.agents (id),
    day           date    NOT NULL,
    runs          integer NOT NULL DEFAULT 0,
    input_tokens  bigint  NOT NULL DEFAULT 0,
    output_tokens bigint  NOT NULL DEFAULT 0,
    sent          integer NOT NULL DEFAULT 0,
    held          integer NOT NULL DEFAULT 0,
    discarded     integer NOT NULL DEFAULT 0,
    PRIMARY KEY (agent_id, day)
);

-- The addresses of the channels the module has read (V23: an agent never
-- answers another channel of this installation) and the senders it has met (S4:
-- a first contact).
CREATE TABLE agents.channel_addresses (
    channel_id uuid PRIMARY KEY,
    address    text NOT NULL
);
CREATE TABLE agents.seen_senders (
    channel_id      uuid NOT NULL,
    address         text NOT NULL,
    conversation_id uuid NOT NULL,
    first_seen_at   timestamptz NOT NULL,
    PRIMARY KEY (channel_id, address)
);

-- +goose Down
DROP TABLE agents.seen_senders;
DROP TABLE agents.channel_addresses;
DROP TABLE agents.usage_days;
DROP TABLE agents.review_items;
DROP TABLE agents.actions;
DROP TABLE agents.run_messages;
DROP TABLE agents.runs;
DROP TABLE agents.handed_off;
DROP TABLE agents.run_queue;
ALTER TABLE agents.roles DROP COLUMN complaint_tag_ids;
