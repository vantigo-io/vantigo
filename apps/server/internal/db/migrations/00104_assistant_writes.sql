-- +goose Up
-- The Assistant makes changes, each confirmed by the person (the Assistant
-- design S12, S14, S9; delivery B): the pending actions a confirmation card
-- stands for, the run state the card pauses in, the daily token budget and the
-- per-person per-day counts the Owner's usage view reads.

-- A run that waits for the person is awaiting_confirmation (21 characters), and
-- one whose card was not decided in time is expired. The one-active-run index
-- now covers both states of a run that has not ended; the stale-run index
-- stays on running alone, because only a running run has a goroutine to lose.
ALTER TABLE assistant.runs ALTER COLUMN state TYPE varchar(24);
DROP INDEX assistant.ux_runs_one_running;
CREATE UNIQUE INDEX ux_runs_one_active ON assistant.runs (conversation_id)
    WHERE state IN ('running', 'awaiting_confirmation');

-- The Owner's daily token budget per person (S14): NULL is no limit. Counted
-- over the business time zone's day.
ALTER TABLE assistant.settings ADD COLUMN daily_token_budget integer
    CHECK (daily_token_budget IS NULL OR daily_token_budget BETWEEN 1000 AND 1000000000);

-- Who confirmed a change the assistant made (S12): the person, always, kept
-- apart from the operation's own audit so the action log says it too.
ALTER TABLE assistant.actions ADD COLUMN confirmed_by uuid;

-- What a decided pending action keeps of the person's data: the operation, the
-- path parameters (ids, so the card can still link its target) and the names of
-- the fields the change set, never their values.
-- +goose StatementBegin
CREATE FUNCTION assistant.redact_request(request jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_build_object(
        'operationId', request -> 'operationId',
        'pathParams', COALESCE(request -> 'pathParams', '{}'::jsonb),
        'fields', COALESCE(request -> 'fields', '[]'::jsonb))
$$;
-- +goose StatementEnd

-- The card's preview, decided: the summary and the target stay (the link a card
-- still shows), the assistant's own sentence goes (it names the new values), and
-- each field keeps its name and loses its current and new values.
-- +goose StatementBegin
CREATE FUNCTION assistant.redact_preview(preview jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_set(
        preview - 'explain',
        '{fields}',
        COALESCE((SELECT jsonb_agg(jsonb_build_object('name', f -> 'name'))
                  FROM jsonb_array_elements(COALESCE(preview -> 'fields', '[]'::jsonb)) AS f), '[]'::jsonb))
$$;
-- +goose StatementEnd

-- A pending action: one change the model proposed, waiting for the person
-- (S12). Private like everything else here: every query filters by user_id,
-- and it goes with its conversation. request is what the model asked for (the
-- path parameters, the query, the headers, the body) and is kept only until the
-- action is decided; preview is the card. status is pending, confirmed,
-- cancelled, expired or failed; outcome is set once the action is decided
-- (the HTTP status and a compacted result), and is the mark that the request
-- has been redacted. call_id and tool_message_id are the model's tool call and
-- the stored tool result the decision replaces.
CREATE TABLE assistant.pending_actions (
    id              uuid        PRIMARY KEY,
    run_id          uuid        NOT NULL REFERENCES assistant.runs (id) ON DELETE CASCADE,
    conversation_id uuid        NOT NULL REFERENCES assistant.conversations (id) ON DELETE CASCADE,
    user_id         uuid        NOT NULL,
    operation_id    text        NOT NULL,
    call_id         text        NOT NULL,
    tool_message_id uuid,
    request         jsonb       NOT NULL,
    preview         jsonb       NOT NULL,
    status          varchar(10) NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'confirmed', 'cancelled', 'expired', 'failed')),
    created_at      timestamptz NOT NULL,
    decided_at      timestamptz,
    expires_at      timestamptz NOT NULL,
    outcome         jsonb
);
CREATE INDEX ix_pending_actions_conversation ON assistant.pending_actions (conversation_id, created_at);
CREATE INDEX ix_pending_actions_expiry ON assistant.pending_actions (expires_at) WHERE status = 'pending';
CREATE INDEX ix_pending_actions_claimed ON assistant.pending_actions (decided_at) WHERE status = 'confirmed' AND outcome IS NULL;
-- One undecided change per run (S12).
CREATE UNIQUE INDEX ux_pending_actions_one_pending ON assistant.pending_actions (run_id) WHERE status = 'pending';

-- What a person used on a business day (S14, S9): counts and tokens, never a
-- word of content. It is counted as it happens, so deleting a conversation does
-- not give the day's budget back, and it is what the Owner's usage view reads.
CREATE TABLE assistant.usage_days (
    user_id       uuid   NOT NULL,
    day           date   NOT NULL,
    conversations integer NOT NULL DEFAULT 0,
    runs          integer NOT NULL DEFAULT 0,
    input_tokens  bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    confirmed     integer NOT NULL DEFAULT 0,
    failed        integer NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);
CREATE INDEX ix_usage_days_day ON assistant.usage_days (day);

-- +goose Down
DROP TABLE assistant.usage_days;
DROP TABLE assistant.pending_actions;
DROP FUNCTION assistant.redact_preview(jsonb);
DROP FUNCTION assistant.redact_request(jsonb);
ALTER TABLE assistant.actions DROP COLUMN confirmed_by;
ALTER TABLE assistant.settings DROP COLUMN daily_token_budget;
DROP INDEX assistant.ux_runs_one_active;
UPDATE assistant.runs SET state = 'failed' WHERE state NOT IN ('running', 'done', 'failed', 'stopped');
CREATE UNIQUE INDEX ux_runs_one_running ON assistant.runs (conversation_id) WHERE state = 'running';
ALTER TABLE assistant.runs ALTER COLUMN state TYPE varchar(10);
