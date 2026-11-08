-- The review queue, calibration, activity, usage, retention and erasure
-- (virtual employees design V20, V21, V24 to V26).

-- name: GetReviewItem :one
SELECT i.*, a.name AS agent_name, r.name AS role_name, COALESCE(r.template, 'front_desk')::text AS role_template
FROM agents.review_items i
JOIN agents.agents a ON a.id = i.agent_id
LEFT JOIN agents.roles r ON r.id = i.role_id
WHERE i.id = @id;

-- name: ListReviewItems :many
-- The waiting items come first, the ones that lapse soonest first; then the
-- decided ones, newest first.
SELECT i.*, a.name AS agent_name, r.name AS role_name, COALESCE(r.template, 'front_desk')::text AS role_template
FROM agents.review_items i
JOIN agents.agents a ON a.id = i.agent_id
LEFT JOIN agents.roles r ON r.id = i.role_id
WHERE (sqlc.narg(agent_id)::uuid IS NULL OR i.agent_id = sqlc.narg(agent_id)::uuid)
  AND COALESCE(r.template, 'front_desk') = ANY(@templates::text[])
  AND (sqlc.narg(state)::text IS NULL OR i.state = sqlc.narg(state)::text
       OR (sqlc.narg(state)::text = 'decided' AND i.state NOT IN ('waiting', 'deciding')))
ORDER BY (i.state IN ('waiting', 'deciding')) DESC,
         CASE WHEN i.state = 'waiting' THEN i.expires_at END ASC,
         i.created_at DESC
LIMIT @n OFFSET @off;

-- name: CountReviewItems :one
SELECT count(*) FROM agents.review_items i
LEFT JOIN agents.roles r ON r.id = i.role_id
WHERE (sqlc.narg(agent_id)::uuid IS NULL OR i.agent_id = sqlc.narg(agent_id)::uuid)
  AND COALESCE(r.template, 'front_desk') = ANY(@templates::text[])
  AND (sqlc.narg(state)::text IS NULL OR i.state = sqlc.narg(state)::text
       OR (sqlc.narg(state)::text = 'decided' AND i.state NOT IN ('waiting', 'deciding')));

-- name: CountWaitingItems :one
SELECT count(*) FROM agents.review_items WHERE state = 'waiting';

-- name: ClaimReviewItem :execrows
-- A reviewer claims a waiting item at the version they read, before acting on it. Only
-- one claim wins; the version does not move.
UPDATE agents.review_items SET state = 'deciding', claimed_at = @now::timestamptz
WHERE id = @id AND version = @version AND state = 'waiting';

-- name: ReleaseReviewItem :exec
-- Gives a claim back when nothing was done (a refusal, a hold).
UPDATE agents.review_items SET state = 'waiting', claimed_at = NULL
WHERE id = @id AND state = 'deciding';

-- name: ResetStaleClaims :execrows
-- A claim never finished (the process stopped) goes back to waiting.
UPDATE agents.review_items SET state = 'waiting', claimed_at = NULL, version = version + 1
WHERE state = 'deciding' AND claimed_at < @before::timestamptz;

-- name: DecideReviewItem :one
-- Decides an item: a discard of a waiting one at the version the reviewer read, or the
-- end of a claim (from_state deciding, the version no longer matters). item replaces the
-- stored item when given (an edited reply keeps the disclosure and the rest).
UPDATE agents.review_items SET
    state = @state,
    decided_by_user_id = sqlc.narg(decided_by)::uuid,
    decided_at = @now::timestamptz,
    discard_reason = sqlc.narg(discard_reason)::text,
    discard_text = sqlc.narg(discard_text)::text,
    edit_ratio = sqlc.narg(edit_ratio)::real,
    outcome = sqlc.narg(outcome)::text,
    item = COALESCE(sqlc.narg(item)::jsonb, item),
    version = version + 1
WHERE id = @id AND state = @from_state AND (NOT @check_version::boolean OR version = @version)
RETURNING *;

-- name: HoldReviewItem :exec
-- A rule that failed again at approval is added to what holds the item.
UPDATE agents.review_items SET held_by = @held_by::text[], version = version + 1
WHERE id = @id AND state IN ('waiting', 'deciding');

-- name: ListExpiredItems :many
SELECT * FROM agents.review_items
WHERE state = 'waiting' AND expires_at <= @now::timestamptz
ORDER BY expires_at
LIMIT @n
FOR UPDATE SKIP LOCKED;

-- name: SetItemState :exec
UPDATE agents.review_items SET state = @state, decided_at = @now::timestamptz, version = version + 1
WHERE id = @id AND state = 'waiting';

-- name: WithdrawWaitingRepliesOfConversation :execrows
-- A newer reply supersedes the replies that still wait on the conversation.
UPDATE agents.review_items SET state = 'withdrawn', decided_at = @now::timestamptz, version = version + 1
WHERE conversation_id = @conversation_id AND kind = 'reply' AND state = 'waiting';

-- name: WithdrawWaitingItemsOfAgent :many
UPDATE agents.review_items SET state = 'withdrawn', decided_at = @now::timestamptz, version = version + 1
WHERE agent_id = @agent_id AND state = 'waiting'
RETURNING id, agent_id, role_id, conversation_id;

-- name: WithdrawWaitingItemsOfChannels :many
UPDATE agents.review_items SET state = 'withdrawn', decided_at = @now::timestamptz, version = version + 1
WHERE role_id = @role_id AND channel_id = ANY(@channel_ids::uuid[]) AND state = 'waiting'
RETURNING id, agent_id, role_id, conversation_id;

-- name: ListLabelledItems :many
-- The role's last labelled replies (V21): the ones a person decided, and the
-- Auto replies, which a person answered over within 24 hours (bad) or that were
-- left alone for 24 hours (good).
SELECT score, held_by, state, edit_ratio FROM (
    SELECT i.score, i.held_by, i.state::text AS state, i.edit_ratio, i.decided_at AS at, i.id::text AS key
    FROM agents.review_items i
    WHERE i.role_id = @role_id AND i.kind = 'reply' AND i.state IN ('sent', 'edited_sent', 'discarded')
    UNION ALL
    SELECT (r.verdict->>'score')::smallint, '{}'::text[],
           CASE WHEN r.answered_over_at IS NOT NULL THEN 'answered_over' ELSE 'sent_auto' END,
           NULL::real, coalesce(r.answered_over_at, r.sent_at), r.id::text
    FROM agents.runs r
    WHERE r.role_id = @role_id AND r.outcome = 'sent' AND r.verdict->>'score' IS NOT NULL
      AND (r.answered_over_at IS NOT NULL OR r.sent_at < @matured::timestamptz)
) labelled
ORDER BY at DESC, key
LIMIT @n;

-- name: SetRunVerdict :exec
UPDATE agents.runs SET verdict = @verdict::jsonb WHERE id = @id;

-- name: UpsertConversationLink :exec
INSERT INTO agents.conversation_links (conversation_id, customer_id, contact_ids, seen_at)
VALUES (@conversation_id, sqlc.narg(customer_id)::integer, @contact_ids::integer[], @now::timestamptz)
ON CONFLICT (conversation_id) DO UPDATE SET
    customer_id = EXCLUDED.customer_id,
    contact_ids = EXCLUDED.contact_ids,
    seen_at = EXCLUDED.seen_at;

-- name: ListRunsOfAgent :many
SELECT x.* FROM agents.runs x
LEFT JOIN agents.roles r ON r.id = x.role_id
WHERE x.agent_id = @agent_id AND (sqlc.narg(state)::text IS NULL OR x.state = sqlc.narg(state)::text)
  AND COALESCE(r.template, 'front_desk') = ANY(@templates::text[])
ORDER BY x.started_at DESC, x.id
LIMIT @n OFFSET @off;

-- name: CountRunsOfAgent :one
SELECT count(*) FROM agents.runs x
LEFT JOIN agents.roles r ON r.id = x.role_id
WHERE x.agent_id = @agent_id AND (sqlc.narg(state)::text IS NULL OR x.state = sqlc.narg(state)::text)
  AND COALESCE(r.template, 'front_desk') = ANY(@templates::text[]);

-- name: ListRunMessages :many
SELECT * FROM agents.run_messages WHERE run_id = @run_id ORDER BY id;

-- name: ListUsageDays :many
SELECT * FROM agents.usage_days
WHERE agent_id = @agent_id AND day >= @from_day::date AND day <= @to_day::date
ORDER BY day;

-- name: ListEvents :many
SELECT * FROM agents.events ORDER BY id DESC LIMIT @n OFFSET @off;

-- name: CountEvents :one
SELECT count(*) FROM agents.events;

-- name: LowerRetention :one
-- Lowers the retention to what Communications' own allows (V26), when it is
-- longer.
UPDATE agents.settings SET retention_months = @months, version = version + 1, updated_at = @now::timestamptz
WHERE id = 1 AND retention_months > @months
RETURNING *;

-- name: DeleteRunsBefore :execrows
-- Retention (V26): runs, and with them their transcripts, actions and review
-- items (ON DELETE CASCADE), that ended before the cutoff.
DELETE FROM agents.runs WHERE id IN (
    SELECT r.id FROM agents.runs r WHERE r.started_at < @cutoff::timestamptz AND r.state NOT IN ('queued', 'running')
      AND NOT EXISTS (SELECT 1 FROM agents.review_items i WHERE i.run_id = r.id AND i.state IN ('waiting', 'deciding'))
    ORDER BY r.started_at LIMIT @n
);

-- name: DeleteHandedOffBefore :execrows
DELETE FROM agents.handed_off WHERE at < @cutoff::timestamptz;

-- name: DeleteTakenRequestsBefore :execrows
DELETE FROM agents.run_queue WHERE taken_at IS NOT NULL AND taken_at < @cutoff::timestamptz;

-- name: DeleteSeenSendersBefore :execrows
DELETE FROM agents.seen_senders WHERE first_seen_at < @cutoff::timestamptz;

-- name: DeleteConversationLinksBefore :execrows
DELETE FROM agents.conversation_links WHERE seen_at < @cutoff::timestamptz;

-- name: DeleteUsageBefore :execrows
DELETE FROM agents.usage_days WHERE day < @cutoff::date;

-- name: ConversationsOfCustomer :many
-- Every conversation the module knows to be the customer's: seen linked to
-- them, or run or held for them (V26).
SELECT l.conversation_id FROM agents.conversation_links l WHERE l.customer_id = @customer_id::integer
UNION
SELECT r.conversation_id FROM agents.runs r WHERE r.customer_id = @customer_id::integer
UNION
SELECT i.conversation_id FROM agents.review_items i WHERE i.customer_id = @customer_id::integer;

-- name: ConversationsOfContacts :many
SELECT conversation_id FROM agents.conversation_links WHERE contact_ids && @contact_ids::integer[];

-- name: CountPersonalRows :one
-- What an erasure of these conversations will remove, by kind.
SELECT
    (SELECT count(*) FROM agents.runs WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer)::bigint AS runs,
    (SELECT count(*) FROM agents.run_messages m JOIN agents.runs r ON r.id = m.run_id
        WHERE r.conversation_id = ANY(@conversation_ids::uuid[]) OR r.customer_id = sqlc.narg(customer_id)::integer)::bigint AS messages,
    (SELECT count(*) FROM agents.review_items WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer)::bigint AS items;

-- name: DeleteRunsOfConversations :execrows
DELETE FROM agents.runs WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer;

-- name: DeleteItemsOfConversations :execrows
DELETE FROM agents.review_items WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer;

-- name: DeleteHandedOffOfConversations :execrows
DELETE FROM agents.handed_off WHERE conversation_id = ANY(@conversation_ids::uuid[]);

-- name: DeleteRequestsOfConversations :execrows
DELETE FROM agents.run_queue WHERE conversation_id = ANY(@conversation_ids::uuid[]);

-- name: DeleteSeenSendersOfConversations :execrows
DELETE FROM agents.seen_senders WHERE conversation_id = ANY(@conversation_ids::uuid[]);

-- name: DeleteConversationLinks :execrows
DELETE FROM agents.conversation_links WHERE conversation_id = ANY(@conversation_ids::uuid[]);

-- name: BlankActionsOfCustomer :execrows
UPDATE agents.actions SET customer_id = NULL WHERE customer_id = @customer_id;

-- name: ExportRunsOfConversations :many
SELECT id, conversation_id, state, outcome, summary, started_at, ended_at FROM agents.runs
WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer
ORDER BY started_at;

-- name: ExportItemsOfConversations :many
SELECT id, run_id, conversation_id, kind, item, state, decided_at, discard_reason, created_at FROM agents.review_items
WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer
ORDER BY created_at;
