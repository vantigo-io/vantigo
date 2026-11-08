-- name: RoleOfChannel :one
-- The role that answers a channel, with whether its agent is active and its user.
SELECT r.id AS role_id, r.agent_id, a.user_id AS agent_user_id, a.state AS agent_state
FROM agents.role_channels rc
JOIN agents.roles r ON r.id = rc.role_id
JOIN agents.agents a ON a.id = r.agent_id
WHERE rc.channel_id = @channel_id;

-- name: GetAgentByUser :one
SELECT * FROM agents.agents WHERE user_id = @user_id;

-- name: GetAgentByID :one
SELECT * FROM agents.agents WHERE id = @id;

-- name: GetRoleByID :one
SELECT * FROM agents.roles WHERE id = @id;

-- name: GetHandedOff :one
SELECT * FROM agents.handed_off WHERE conversation_id = @conversation_id;

-- name: UpsertHandedOff :exec
INSERT INTO agents.handed_off (conversation_id, role_id, at, reason)
VALUES (@conversation_id, @role_id, @at::timestamptz, @reason)
ON CONFLICT (conversation_id) DO UPDATE SET role_id = EXCLUDED.role_id, at = EXCLUDED.at, reason = EXCLUDED.reason;

-- name: DeleteHandedOff :execrows
DELETE FROM agents.handed_off WHERE conversation_id = @conversation_id;

-- name: DeleteUntakenRequestsOfConversation :exec
DELETE FROM agents.run_queue WHERE conversation_id = @conversation_id AND taken_at IS NULL;

-- name: UpsertRunRequest :exec
-- One untaken request per (role, conversation): a second message moves
-- not_before to now + the wait, never past due_by (first + the ceiling).
INSERT INTO agents.run_queue (role_id, conversation_id, reason, not_before, due_by, created_at)
VALUES (@role_id, @conversation_id, @reason, @not_before::timestamptz, @due_by::timestamptz, @now::timestamptz)
ON CONFLICT (role_id, conversation_id) WHERE taken_at IS NULL
DO UPDATE SET not_before = LEAST(EXCLUDED.not_before, agents.run_queue.due_by);

-- name: InsertRetryRequest :exec
INSERT INTO agents.run_queue (role_id, conversation_id, reason, attempt, not_before, due_by, created_at)
VALUES (@role_id, @conversation_id, 'retry', @attempt, @now::timestamptz, @now::timestamptz, @now::timestamptz)
ON CONFLICT (role_id, conversation_id) WHERE taken_at IS NULL DO NOTHING;

-- name: ClaimDueRequest :one
-- The oldest due request of an active agent whose conversation has no run in
-- progress, locked so another replica skips it. The caller commits before the
-- run starts.
SELECT q.id, q.role_id, q.conversation_id, q.reason, q.attempt, r.agent_id
FROM agents.run_queue q
JOIN agents.roles r ON r.id = q.role_id
JOIN agents.agents a ON a.id = r.agent_id
WHERE q.taken_at IS NULL AND q.not_before <= @now::timestamptz AND a.state = 'active'
  AND NOT EXISTS (
      SELECT 1 FROM agents.runs x
      WHERE x.role_id = q.role_id AND x.conversation_id = q.conversation_id AND x.state IN ('queued', 'running'))
ORDER BY q.not_before, q.id
LIMIT 1
FOR UPDATE OF q SKIP LOCKED;

-- name: MarkRequestTaken :exec
UPDATE agents.run_queue SET taken_at = @now::timestamptz, run_id = @run_id WHERE id = @id;

-- name: InsertRun :exec
INSERT INTO agents.runs (id, agent_id, role_id, conversation_id, reason, attempt, state, claimed_by, claimed_until, started_at)
VALUES (@id, @agent_id, @role_id, @conversation_id, @reason, @attempt, 'running', @claimed_by, @claimed_until::timestamptz, @now::timestamptz);

-- name: InsertHandedOffRun :exec
-- A run that never started: a request handed off before it could (the budget,
-- a wait of more than a day). Its hand-off effects are the API process's.
INSERT INTO agents.runs (id, agent_id, role_id, conversation_id, reason, attempt, state, error_code, summary, started_at, ended_at)
VALUES (@id, @agent_id, @role_id, @conversation_id, @reason, 1, 'handed_off', @error_code, @summary, @now::timestamptz, @now::timestamptz);

-- name: GetRun :one
SELECT * FROM agents.runs WHERE id = @id;

-- name: RenewClaim :execrows
UPDATE agents.runs SET claimed_until = @claimed_until::timestamptz
WHERE id = @id AND claimed_by = @claimed_by AND state = 'running';

-- name: SetRunCustomer :exec
UPDATE agents.runs SET customer_id = sqlc.narg(customer_id)::integer WHERE id = @id;

-- name: AddRunProgress :exec
UPDATE agents.runs SET
    tool_calls = tool_calls + @tool_calls,
    model_turns = model_turns + @model_turns,
    input_tokens = input_tokens + @input_tokens,
    output_tokens = output_tokens + @output_tokens
WHERE id = @id;

-- name: EndRun :exec
UPDATE agents.runs SET
    state = @state,
    outcome = sqlc.narg(outcome)::text,
    error_code = sqlc.narg(error_code)::text,
    summary = sqlc.narg(summary)::text,
    sent_at = COALESCE(sqlc.narg(sent_at)::timestamptz, sent_at),
    claimed_until = NULL,
    ended_at = @now::timestamptz
WHERE id = @id AND state = 'running';

-- name: SetRunOutcome :exec
UPDATE agents.runs SET outcome = @outcome WHERE id = @id;

-- name: ListRunsToHandOff :many
-- Runs handed off whose note and assignee are still to do.
SELECT * FROM agents.runs WHERE state = 'handed_off' AND outcome IS NULL ORDER BY ended_at LIMIT @n;

-- name: LockLapsedRuns :many
SELECT * FROM agents.runs
WHERE state = 'running' AND claimed_until < @now::timestamptz
ORDER BY claimed_until
LIMIT @n
FOR UPDATE SKIP LOCKED;

-- name: StopRun :exec
UPDATE agents.runs SET state = @state, error_code = @error_code, summary = sqlc.narg(summary)::text,
    claimed_until = NULL, ended_at = @now::timestamptz
WHERE id = @id;

-- name: ListStaleRequests :many
SELECT q.id, q.role_id, q.conversation_id, q.reason, r.agent_id
FROM agents.run_queue q
JOIN agents.roles r ON r.id = q.role_id
WHERE q.taken_at IS NULL AND q.created_at < @before::timestamptz
ORDER BY q.id
LIMIT @n
FOR UPDATE OF q SKIP LOCKED;

-- name: ListUntakenRequestsOfAgent :many
SELECT q.id, q.role_id, q.conversation_id, q.reason
FROM agents.run_queue q
JOIN agents.roles r ON r.id = q.role_id
WHERE q.taken_at IS NULL AND r.agent_id = @agent_id
ORDER BY q.id
FOR UPDATE OF q SKIP LOCKED;

-- name: DeleteRequest :exec
DELETE FROM agents.run_queue WHERE id = @id;

-- name: ListAgentsWithBudget :many
SELECT id, daily_token_budget FROM agents.agents WHERE state = 'active' AND daily_token_budget IS NOT NULL;

-- name: InsertRunMessage :exec
INSERT INTO agents.run_messages (run_id, role, text, tool_calls, tool_call_id, created_at)
VALUES (@run_id, @role, @text, sqlc.narg(tool_calls)::jsonb, sqlc.narg(tool_call_id)::text, @now::timestamptz);

-- name: InsertAction :exec
INSERT INTO agents.actions (run_id, agent_id, customer_id, operation_id, ref, scoped, mode, status, http_status, duration_ms, approved_by, created_at)
VALUES (@run_id, @agent_id, sqlc.narg(customer_id)::integer, @operation_id, @ref, @scoped, @mode, @status,
        sqlc.narg(http_status)::integer, @duration_ms, sqlc.narg(approved_by)::uuid, @now::timestamptz);

-- name: ListActionsOfRun :many
SELECT * FROM agents.actions WHERE run_id = @run_id ORDER BY id;

-- name: InsertReviewItem :exec
-- state is waiting for an item a person decides, withdrawn for one the pipeline
-- stopped (S6, S7, a conversation someone took).
INSERT INTO agents.review_items (id, run_id, agent_id, role_id, conversation_id, channel_id, customer_id, kind, item, score, sources, model_score, held_by, state, decided_at, expires_at, created_at)
VALUES (@id, @run_id, @agent_id, @role_id, @conversation_id, sqlc.narg(channel_id)::uuid, sqlc.narg(customer_id)::integer, @kind, @item, @score, @sources, @model_score,
        @held_by::text[], @state, sqlc.narg(decided_at)::timestamptz, @expires_at::timestamptz, @now::timestamptz);

-- name: ListReviewItemsOfRun :many
SELECT * FROM agents.review_items WHERE run_id = @run_id ORDER BY created_at;

-- name: AddUsage :exec
INSERT INTO agents.usage_days (agent_id, day, runs, input_tokens, output_tokens, sent, held, discarded)
VALUES (@agent_id, @day::date, @runs, @input_tokens, @output_tokens, @sent, @held, @discarded)
ON CONFLICT (agent_id, day) DO UPDATE SET
    runs = agents.usage_days.runs + EXCLUDED.runs,
    input_tokens = agents.usage_days.input_tokens + EXCLUDED.input_tokens,
    output_tokens = agents.usage_days.output_tokens + EXCLUDED.output_tokens,
    sent = agents.usage_days.sent + EXCLUDED.sent,
    held = agents.usage_days.held + EXCLUDED.held,
    discarded = agents.usage_days.discarded + EXCLUDED.discarded;

-- name: GetUsageDay :one
SELECT * FROM agents.usage_days WHERE agent_id = @agent_id AND day = @day::date;

-- name: CountRunsSince :one
-- Runs of a conversation started since a moment (S7: at most 6 an hour).
SELECT count(*) FROM agents.runs WHERE conversation_id = @conversation_id AND started_at >= @since::timestamptz AND id <> @except_run;

-- name: CountRepliesSince :one
-- Agent messages sent in a conversation since a moment (S7): a run's own send and
-- a reviewer's approval.
SELECT (
    (SELECT count(*) FROM agents.runs r WHERE r.conversation_id = @conversation_id AND r.sent_at >= @since::timestamptz) +
    (SELECT count(*) FROM agents.review_items i WHERE i.conversation_id = @conversation_id AND i.state IN ('sent', 'edited_sent') AND i.decided_at >= @since::timestamptz)
)::bigint;

-- name: UpsertChannelAddress :exec
INSERT INTO agents.channel_addresses (channel_id, address) VALUES (@channel_id, @address)
ON CONFLICT (channel_id) DO UPDATE SET address = EXCLUDED.address;

-- name: IsChannelAddress :one
SELECT EXISTS (SELECT 1 FROM agents.channel_addresses WHERE address = @address);

-- name: InsertSeenSender :exec
INSERT INTO agents.seen_senders (channel_id, address, conversation_id, first_seen_at)
VALUES (@channel_id, @address, @conversation_id, @now::timestamptz)
ON CONFLICT (channel_id, address) DO NOTHING;

-- name: FirstConversationOfSender :one
SELECT conversation_id FROM agents.seen_senders WHERE channel_id = @channel_id AND address = @address;

-- name: WithdrawWaitingItemsOfConversation :execrows
UPDATE agents.review_items SET state = 'withdrawn', decided_at = @now::timestamptz, version = version + 1
WHERE conversation_id = @conversation_id AND state = 'waiting';

-- name: WithdrawWaitingItemsOfRole :many
UPDATE agents.review_items SET state = 'withdrawn', decided_at = @now::timestamptz, version = version + 1
WHERE role_id = @role_id AND state = 'waiting'
RETURNING id, agent_id, role_id, conversation_id;
