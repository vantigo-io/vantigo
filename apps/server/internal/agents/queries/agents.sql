-- name: InsertAgent :one
INSERT INTO agents.agents (id, user_id, name, style, languages, disclosure_wording, created_at, updated_at, created_by_user_id)
VALUES (@id, @user_id, @name, @style, @languages, @disclosure_wording, @now::timestamptz, @now::timestamptz, @created_by::uuid)
RETURNING *;

-- name: GetAgent :one
SELECT * FROM agents.agents WHERE id = @id AND state <> 'deleted';

-- name: LockAgent :one
-- LockAgent holds the agent for the transaction, so a change judges its If-Match
-- version against the row it then writes.
SELECT * FROM agents.agents WHERE id = @id AND state <> 'deleted' FOR UPDATE;

-- name: ListAgents :many
SELECT * FROM agents.agents WHERE state <> 'deleted' ORDER BY lower(name), id;

-- name: UpdateAgent :one
UPDATE agents.agents SET
    name = @name,
    style = @style,
    languages = @languages,
    disclosure_wording = @disclosure_wording,
    version = version + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SetAgentState :one
UPDATE agents.agents SET
    state = @state,
    version = version + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: BumpAgentVersion :exec
-- BumpAgentVersion moves the version on after a level or a role changed, which
-- is what makes the gate's cache notice.
UPDATE agents.agents SET version = version + 1, updated_at = @now::timestamptz WHERE id = @id;

-- name: GetAgentGate :one
-- GetAgentGate is what the gate reads on every call: the version to cache by and
-- the state.
SELECT version, state FROM agents.agents WHERE id = @id;

-- name: ListAgentLevels :many
SELECT * FROM agents.permission_levels WHERE agent_id = @agent_id ORDER BY permission_key;

-- name: GetAgentLevel :one
SELECT * FROM agents.permission_levels WHERE agent_id = @agent_id AND permission_key = @permission_key;

-- name: UpsertAgentLevel :exec
INSERT INTO agents.permission_levels (agent_id, permission_key, level, updated_at, updated_by)
VALUES (@agent_id, @permission_key, @level, @now::timestamptz, @updated_by::uuid)
ON CONFLICT (agent_id, permission_key) DO UPDATE SET
    level = EXCLUDED.level, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by;

-- name: DeleteAgentLevel :exec
DELETE FROM agents.permission_levels WHERE agent_id = @agent_id AND permission_key = @permission_key;
