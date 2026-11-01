-- +goose Up
-- The Assistant app (the Assistant design,
-- docs/superpowers/specs/2026-11-01-assistant-design.md, delivery A): the
-- workspace's settings, each person's own switch, the conversations a person
-- has had with the assistant anywhere in Vantigo, their messages, the runs
-- that answered them, the action log, the Owner's permission levels (S16) and
-- the record of the Owner's changes.
--
-- Every conversation, message, run and action belongs to one person: every
-- query of this module filters by user_id (S9), and the Owner has no way to
-- read them. A user is identity's, kept as an opaque id with no foreign key
-- (module boundaries rule 4); when identity deletes the user, the module's
-- erasure sweep deletes what is left (S9, assistant/erasure.go).
CREATE SCHEMA assistant;

-- The workspace's settings: one row. mode is who has the assistant (S8):
-- all, per_person or disabled. retention_months is how long a conversation
-- is kept after its last activity (S9), 1 to 60. sensitive_default is the
-- default level of every permission the catalog marks Sensitive (S16): off or
-- read. version moves on with every change of the settings or of a permission
-- level, so the dispatcher's gate can cache by it and an Owner's If-Match
-- guards both.
CREATE TABLE assistant.settings (
    id                 smallint    PRIMARY KEY,
    mode               varchar(10) NOT NULL DEFAULT 'all' CHECK (mode IN ('all', 'per_person', 'disabled')),
    retention_months   smallint    NOT NULL DEFAULT 12 CHECK (retention_months BETWEEN 1 AND 60),
    sensitive_default  varchar(10) NOT NULL DEFAULT 'off' CHECK (sensitive_default IN ('off', 'read')),
    version            integer     NOT NULL DEFAULT 1,
    updated_at         timestamptz NOT NULL,
    updated_by_user_id uuid
);
CREATE UNIQUE INDEX ux_settings_single_row ON assistant.settings ((true));
INSERT INTO assistant.settings (id, updated_at) VALUES (1, now());

-- A person's own switch (S8), which matters in per-person mode, and the
-- conversation they had open last, which the panel reopens on any device.
CREATE TABLE assistant.person_settings (
    user_id              uuid        PRIMARY KEY,
    enabled              boolean     NOT NULL DEFAULT false,
    last_conversation_id uuid,
    version              integer     NOT NULL DEFAULT 1,
    updated_at           timestamptz NOT NULL
);

-- The Owner's overrides of the default level of a permission (S16). Only
-- overrides are stored, so a permission a later release adds takes its
-- default. level is off, read or change.
CREATE TABLE assistant.permission_levels (
    permission_key text        PRIMARY KEY,
    level          varchar(10) NOT NULL CHECK (level IN ('off', 'read', 'change')),
    updated_at     timestamptz NOT NULL,
    updated_by     uuid
);

-- A conversation (S10): where it began (app and route), whether it is pinned
-- or archived, and when anything last happened in it, which retention reads.
CREATE TABLE assistant.conversations (
    id               uuid        PRIMARY KEY,
    user_id          uuid        NOT NULL,
    title            text        NOT NULL DEFAULT '',
    origin_app       varchar(40) NOT NULL,
    origin_route     varchar(300) NOT NULL,
    pinned           boolean     NOT NULL DEFAULT false,
    archived_at      timestamptz,
    created_at       timestamptz NOT NULL,
    last_activity_at timestamptz NOT NULL
);
CREATE INDEX ix_conversations_user ON assistant.conversations (user_id, pinned DESC, last_activity_at DESC);
CREATE INDEX ix_conversations_activity ON assistant.conversations (last_activity_at);

-- A run: one answer to one user message (S5, S14). state is running, done,
-- failed or stopped; error_code says why it failed. tool_calls and model_turns
-- are counted against the per-run caps.
CREATE TABLE assistant.runs (
    id              uuid        PRIMARY KEY,
    conversation_id uuid        NOT NULL REFERENCES assistant.conversations (id) ON DELETE CASCADE,
    user_id         uuid        NOT NULL,
    state           varchar(10) NOT NULL,
    error_code      varchar(60),
    tool_calls      integer     NOT NULL DEFAULT 0,
    model_turns     integer     NOT NULL DEFAULT 0,
    started_at      timestamptz NOT NULL,
    ended_at        timestamptz,
    stop_requested  boolean     NOT NULL DEFAULT false
);
CREATE INDEX ix_runs_conversation ON assistant.runs (conversation_id, started_at);
-- One run per conversation at a time (409 run_in_progress): the index states
-- it, so two messages at once cannot both start one.
CREATE UNIQUE INDEX ux_runs_one_running ON assistant.runs (conversation_id) WHERE state = 'running';
CREATE INDEX ix_runs_running ON assistant.runs (started_at) WHERE state = 'running';

-- A message: what the person wrote, what the model said, or what a tool
-- answered the model. content is the typed parts as JSON; context is the chip
-- the person sent it with; search_text is the words, for the list's search.
-- seq orders a conversation.
CREATE TABLE assistant.messages (
    id              uuid        PRIMARY KEY,
    seq             bigint      GENERATED ALWAYS AS IDENTITY,
    conversation_id uuid        NOT NULL REFERENCES assistant.conversations (id) ON DELETE CASCADE,
    run_id          uuid        REFERENCES assistant.runs (id) ON DELETE CASCADE,
    role            varchar(10) NOT NULL,
    content         jsonb       NOT NULL,
    context         jsonb,
    search_text     text        NOT NULL DEFAULT '',
    model           varchar(100),
    input_tokens    integer,
    output_tokens   integer,
    created_at      timestamptz NOT NULL
);
CREATE INDEX ix_messages_conversation ON assistant.messages (conversation_id, seq);

-- The action log (S9): every tool call, and which operation it was, in what
-- mode, what HTTP status the router answered and how long it took. Never a
-- request or a response body: the tool result the model saw is the compacted
-- one in messages.
CREATE TABLE assistant.actions (
    id           uuid        PRIMARY KEY,
    run_id       uuid        NOT NULL REFERENCES assistant.runs (id) ON DELETE CASCADE,
    operation_id text        NOT NULL,
    mode         varchar(10) NOT NULL,
    http_status  integer,
    duration_ms  integer     NOT NULL,
    created_at   timestamptz NOT NULL
);
CREATE INDEX ix_actions_run ON assistant.actions (run_id, created_at);

-- The Owner's changes to the settings, the people's switches and the
-- permission levels, with before and after as identity's audit records its
-- own (identity's table is identity's alone, so this module keeps its own).
CREATE TABLE assistant.events (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at            timestamptz NOT NULL,
    actor_user_id uuid        NOT NULL,
    action        text        NOT NULL,
    target        text,
    before        jsonb,
    after         jsonb
);

-- +goose Down
DROP SCHEMA assistant CASCADE;
