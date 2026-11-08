-- name: InsertRole :one
INSERT INTO agents.roles (id, agent_id, template, name, instructions, created_at, updated_at)
VALUES (@id, @agent_id, @template, @name, @instructions, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: GetRole :one
SELECT * FROM agents.roles WHERE id = @id AND agent_id = @agent_id;

-- name: LockRole :one
SELECT * FROM agents.roles WHERE id = @id AND agent_id = @agent_id FOR UPDATE;

-- name: ListRolesOfAgent :many
SELECT * FROM agents.roles WHERE agent_id = @agent_id ORDER BY created_at, id;

-- name: ListRolesOfAgents :many
SELECT * FROM agents.roles WHERE agent_id = ANY(@agent_ids::uuid[]) ORDER BY created_at, id;

-- name: UpdateRole :one
UPDATE agents.roles SET
    name = @name,
    instructions = @instructions,
    reply_mode = @reply_mode,
    threshold = @threshold,
    caps_tool_calls = @caps_tool_calls,
    caps_turns = @caps_turns,
    max_replies_per_day = @max_replies_per_day,
    hand_off_user_id = sqlc.narg(hand_off_user_id)::uuid,
    complaint_tag_ids = @complaint_tag_ids::uuid[],
    start_mode = @start_mode,
    change_mode = @change_mode,
    wait_days = @wait_days,
    recipient_user_id = sqlc.narg(recipient_user_id)::uuid,
    schedule_kind = sqlc.narg(schedule_kind)::text,
    schedule_day = sqlc.narg(schedule_day)::smallint,
    schedule_time = sqlc.narg(schedule_time)::text,
    schedule_fired_at = sqlc.narg(schedule_fired_at)::timestamptz,
    version = version + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteRole :exec
DELETE FROM agents.roles WHERE id = @id;

-- name: ListChannelsOfRoles :many
SELECT role_id, channel_id FROM agents.role_channels WHERE role_id = ANY(@role_ids::uuid[]) ORDER BY channel_id;

-- name: DeleteRoleChannels :exec
DELETE FROM agents.role_channels WHERE role_id = @role_id;

-- name: InsertRoleChannel :exec
INSERT INTO agents.role_channels (role_id, channel_id) VALUES (@role_id, @channel_id);

-- name: DeleteChannelsOfAgent :exec
-- DeleteChannelsOfAgent frees every channel the agent's roles answered, as
-- deleting the agent does.
DELETE FROM agents.role_channels WHERE role_id IN (SELECT id FROM agents.roles WHERE agent_id = @agent_id);

-- name: ListScheduledRoles :many
-- The roles of active agents that have a schedule (V29).
SELECT r.* FROM agents.roles r JOIN agents.agents a ON a.id = r.agent_id
WHERE r.schedule_kind IS NOT NULL AND a.state = 'active'
ORDER BY r.id;

-- name: MarkScheduleFired :exec
UPDATE agents.roles SET schedule_fired_at = @fired_at::timestamptz WHERE id = @id;

-- name: ListRolesOfTemplate :many
-- The roles of agents that are not deleted that use a template: a paused agent's
-- roles are woken too, and its wakeups wait until it is active again.
SELECT r.* FROM agents.roles r JOIN agents.agents a ON a.id = r.agent_id
WHERE r.template = @template AND a.state <> 'deleted'
ORDER BY r.id;
