-- +goose Up
-- The agents module (virtual employees design,
-- docs/superpowers/specs/2026-11-08-virtual-employees-design.md, delivery A1):
-- the workspace's settings, the agents with their persona, the levels the Owner
-- gave each agent's permissions (V9), their roles bound to channels (V12) and
-- the record of the Owner's changes. The trigger, the runs and the review
-- queue come with A2 and A3.
--
-- An agent's user is identity's, kept as an opaque id with no foreign key
-- (module boundaries rule 4); so is a channel (communications') and a role's
-- hand-off person.
CREATE SCHEMA agents;

-- The workspace's settings: one row. company_name is what the disclosure names
-- (V11), empty until the Owner sets it; no agent is created before. retention_months
-- is how long a run is kept (V26), 1 to 60. version moves on with every change.
CREATE TABLE agents.settings (
    id                 smallint     PRIMARY KEY,
    retention_months   smallint     NOT NULL DEFAULT 6 CHECK (retention_months BETWEEN 1 AND 60),
    company_name       varchar(120) NOT NULL DEFAULT '',
    version            integer      NOT NULL DEFAULT 1,
    updated_at         timestamptz  NOT NULL,
    updated_by_user_id uuid
);
CREATE UNIQUE INDEX ux_settings_single_row ON agents.settings ((true));
INSERT INTO agents.settings (id, updated_at) VALUES (1, now());

-- An agent (V10). user_id is its identity user, of kind agent. languages says
-- which language it answers in (customer: the customer's, else bokmål),
-- disclosure_wording which of the code's wordings opens its messages (V11).
-- state is active, paused (no new runs) or deleted (its user disabled, its
-- channels free; the row stays so the audit's references stay true).
-- daily_token_budget is null for none (V24). version moves on with a change of
-- the persona, of a level or of a role, so the gate can cache by it.
CREATE TABLE agents.agents (
    id                 uuid        PRIMARY KEY,
    user_id            uuid        NOT NULL UNIQUE,
    name               varchar(60) NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    style              text        NOT NULL DEFAULT '' CHECK (char_length(style) <= 1500),
    languages          varchar(10) NOT NULL DEFAULT 'customer' CHECK (languages IN ('customer', 'nb', 'en')),
    disclosure_wording varchar(10) NOT NULL DEFAULT 'assistant' CHECK (disclosure_wording IN ('assistant', 'talking', 'compact')),
    state              varchar(10) NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'paused', 'deleted')),
    daily_token_budget integer     CHECK (daily_token_budget > 0),
    version            integer     NOT NULL DEFAULT 1,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    created_by_user_id uuid
);

-- The Owner's levels for an agent (V9). Only what was given is stored: every
-- permission of an agent is off until a row says otherwise.
CREATE TABLE agents.permission_levels (
    agent_id       uuid        NOT NULL REFERENCES agents.agents (id) ON DELETE CASCADE,
    permission_key text        NOT NULL,
    level          varchar(10) NOT NULL CHECK (level IN ('off', 'read', 'change')),
    updated_at     timestamptz NOT NULL,
    updated_by     uuid,
    PRIMARY KEY (agent_id, permission_key)
);

-- A role of an agent (V12): a template of the code (front_desk in A), the
-- instructions the Owner wrote, how its replies are decided (reply_mode, and
-- the threshold of auto_above) and the caps a run keeps to. hand_off_user_id is
-- the person a hand-off goes to, null for the Inbox, unassigned. The template's
-- allowlist is code, not a column.
CREATE TABLE agents.roles (
    id                  uuid         PRIMARY KEY,
    agent_id            uuid         NOT NULL REFERENCES agents.agents (id) ON DELETE CASCADE,
    template            varchar(40)  NOT NULL CHECK (template IN ('front_desk')),
    name                varchar(80)  NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    instructions        text         NOT NULL DEFAULT '' CHECK (char_length(instructions) <= 8000),
    reply_mode          varchar(12)  NOT NULL DEFAULT 'review' CHECK (reply_mode IN ('review', 'auto_above', 'auto')),
    threshold           smallint     NOT NULL DEFAULT 85 CHECK (threshold BETWEEN 50 AND 100),
    caps_tool_calls     smallint     NOT NULL DEFAULT 30 CHECK (caps_tool_calls BETWEEN 1 AND 60),
    caps_turns          smallint     NOT NULL DEFAULT 8 CHECK (caps_turns BETWEEN 1 AND 16),
    max_replies_per_day smallint     NOT NULL DEFAULT 5 CHECK (max_replies_per_day BETWEEN 1 AND 50),
    hand_off_user_id    uuid,
    version             integer      NOT NULL DEFAULT 1,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL
);
CREATE INDEX ix_roles_agent ON agents.roles (agent_id);

-- The channels a role answers. A channel belongs to at most one role, so two
-- agents never answer the same channel.
CREATE TABLE agents.role_channels (
    role_id    uuid NOT NULL REFERENCES agents.roles (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL UNIQUE,
    PRIMARY KEY (role_id, channel_id)
);

-- The record of the Owner's changes (V25 for the settings of this delivery):
-- who changed what, with before and after. Identity's audit is identity's
-- alone, and a module may not write another's schema.
CREATE TABLE agents.events (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at            timestamptz NOT NULL,
    actor_user_id uuid        NOT NULL,
    action        text        NOT NULL,
    target        text,
    before        jsonb       NOT NULL,
    after         jsonb       NOT NULL
);
CREATE INDEX ix_events_at ON agents.events (at);

-- +goose Down
DROP SCHEMA agents CASCADE;
