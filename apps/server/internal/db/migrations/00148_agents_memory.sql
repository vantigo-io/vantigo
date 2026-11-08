-- +goose Up
-- Memory and feedback of the agents module (virtual employees design, delivery
-- B: V27, V28, and V21's note on Auto replies a person answered over).

-- What an agent remembers (V27): a short note about a customer or, when the run
-- had no customer, about a conversation. subject_id is the customer's number or
-- the conversation's id, both another module's, kept as opaque text. run_id is
-- the run that wrote it, with no foreign key: a run is deleted by retention and
-- the note lives by its own age.
CREATE TABLE agents.memory (
    id           uuid         PRIMARY KEY,
    agent_id     uuid         NOT NULL REFERENCES agents.agents (id) ON DELETE CASCADE,
    subject_type varchar(12)  NOT NULL CHECK (subject_type IN ('customer', 'conversation')),
    subject_id   text         NOT NULL,
    text         varchar(500) NOT NULL CHECK (char_length(btrim(text)) > 0),
    created_at   timestamptz  NOT NULL,
    run_id       uuid
);
CREATE INDEX ix_memory_subject ON agents.memory (agent_id, subject_type, subject_id, created_at DESC);
CREATE INDEX ix_memory_run ON agents.memory (run_id);
CREATE INDEX ix_memory_created ON agents.memory (created_at);

-- What the weekly check proposes for a role's instructions (V28). old_text is
-- the passage to replace, null to append new_text. item_ids are the review items
-- it rests on, ids only. Nothing here changes the instructions: the Owner
-- applies or dismisses.
CREATE TABLE agents.instruction_suggestions (
    id            uuid        PRIMARY KEY,
    role_id       uuid        NOT NULL REFERENCES agents.roles (id) ON DELETE CASCADE,
    old_text      text        CHECK (char_length(old_text) <= 1000),
    new_text      text        NOT NULL CHECK (char_length(new_text) <= 1000),
    rationale     text        NOT NULL CHECK (char_length(rationale) <= 500),
    item_ids      uuid[]      NOT NULL DEFAULT '{}',
    state         varchar(10) NOT NULL DEFAULT 'proposed' CHECK (state IN ('proposed', 'applied', 'dismissed')),
    created_at    timestamptz NOT NULL,
    decided_by    uuid,
    decided_at    timestamptz
);
CREATE INDEX ix_suggestions_role ON agents.instruction_suggestions (role_id, created_at DESC);
CREATE INDEX ix_suggestions_items ON agents.instruction_suggestions USING gin (item_ids);

-- When the weekly check last looked at a role, so that it looks once a week
-- whichever replica runs the worker.
-- feedback_since is the end of the corrections the last real check read.
ALTER TABLE agents.roles ADD COLUMN feedback_checked_at timestamptz;
ALTER TABLE agents.roles ADD COLUMN feedback_since timestamptz;

-- A person answered over an Auto-sent reply within 24 hours (V21): the run's
-- reply is then a bad label for its score.
ALTER TABLE agents.runs ADD COLUMN answered_over_at timestamptz;
CREATE INDEX ix_runs_sent ON agents.runs (role_id, ended_at) WHERE outcome = 'sent';

-- +goose Down
DROP INDEX agents.ix_runs_sent;
ALTER TABLE agents.runs DROP COLUMN answered_over_at;
ALTER TABLE agents.roles DROP COLUMN feedback_since;
ALTER TABLE agents.roles DROP COLUMN feedback_checked_at;
DROP TABLE agents.instruction_suggestions;
DROP TABLE agents.memory;
