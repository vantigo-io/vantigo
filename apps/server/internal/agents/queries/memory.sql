-- Memory and feedback (virtual employees design V27, V28, and V21's Auto
-- replies answered over).

-- name: ListMemory :many
-- What a run reads when it starts (V27): the agent's own notes about the run's
-- customer, when it has one, and about its conversation, the newest first. A
-- note about another customer is never selected.
SELECT * FROM agents.memory
WHERE agent_id = @agent_id
  AND ((subject_type = 'customer' AND subject_id = sqlc.narg(customer_id)::text)
    OR (subject_type = 'conversation' AND subject_id = @conversation_id::text))
ORDER BY created_at DESC, id
LIMIT @n;

-- name: InsertMemory :execrows
-- A note the agent already holds for the subject is not written twice.
INSERT INTO agents.memory (id, agent_id, subject_type, subject_id, text, created_at, run_id)
SELECT @id::uuid, @agent_id::uuid, @subject_type::varchar, @subject_id::text, @text::varchar, @now::timestamptz, sqlc.narg(run_id)::uuid
WHERE NOT EXISTS (
    SELECT 1 FROM agents.memory x
    WHERE x.agent_id = @agent_id::uuid AND x.subject_type = @subject_type::varchar AND x.subject_id = @subject_id::text AND x.text = @text::varchar
);

-- name: CountMemoryOfRun :one
SELECT count(*) FROM agents.memory WHERE run_id = @run_id;

-- name: TrimMemory :exec
-- Keeps the newest notes of a subject and drops the older ones (V27).
DELETE FROM agents.memory WHERE id IN (
    SELECT x.id FROM agents.memory x
    WHERE x.agent_id = @agent_id AND x.subject_type = @subject_type AND x.subject_id = @subject_id
    ORDER BY x.created_at DESC, x.id
    OFFSET @keep::integer
);

-- name: GetMemory :one
SELECT * FROM agents.memory WHERE id = @id;

-- name: DeleteMemory :execrows
DELETE FROM agents.memory WHERE id = @id;

-- name: ListMemoryOfCustomer :many
-- The notes the agents hold about one customer, for the customer page.
SELECT m.id, m.agent_id, a.name AS agent_name, m.text, m.created_at
FROM agents.memory m
JOIN agents.agents a ON a.id = m.agent_id
WHERE m.subject_type = 'customer' AND m.subject_id = @customer_id::text AND a.state <> 'deleted'
ORDER BY a.name, m.agent_id, m.created_at DESC, m.id;

-- name: CountMemoryOfAgent :one
SELECT
    (SELECT count(*) FROM agents.memory x WHERE x.agent_id = @agent_id AND x.subject_type = 'customer')::bigint AS customer_notes,
    (SELECT count(*) FROM agents.memory x WHERE x.agent_id = @agent_id AND x.subject_type = 'conversation')::bigint AS conversation_notes,
    (SELECT count(DISTINCT x.subject_id) FROM agents.memory x WHERE x.agent_id = @agent_id AND x.subject_type = 'customer')::bigint AS customers;

-- name: DeleteMemoryBefore :execrows
-- Retention (V26): a note expires with the runs.
DELETE FROM agents.memory WHERE created_at < @cutoff::timestamptz;

-- name: DeleteMemoryOfAgent :execrows
DELETE FROM agents.memory WHERE agent_id = @agent_id;

-- name: CountMemoryToErase :one
SELECT count(*) FROM agents.memory
WHERE (subject_type = 'customer' AND subject_id = (sqlc.narg(customer_id)::integer)::text)
   OR (subject_type = 'conversation' AND subject_id = ANY(@conversation_texts::text[]))
   OR run_id IN (SELECT id FROM agents.runs WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer);

-- name: DeleteMemoryToErase :execrows
-- Erasure (V26): the customer's notes, the notes about the conversations erased, and
-- what a run of those wrote, whichever subject it named.
DELETE FROM agents.memory
WHERE (subject_type = 'customer' AND subject_id = (sqlc.narg(customer_id)::integer)::text)
   OR (subject_type = 'conversation' AND subject_id = ANY(@conversation_texts::text[]))
   OR run_id IN (SELECT id FROM agents.runs WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer);

-- name: ExportMemoryOfCustomer :many
SELECT m.id, a.name AS agent_name, m.text, m.created_at
FROM agents.memory m JOIN agents.agents a ON a.id = m.agent_id
WHERE m.subject_type = 'customer' AND m.subject_id = @customer_id::text
ORDER BY m.created_at, m.id;

-- name: CountSuggestionsToErase :one
SELECT count(*) FROM agents.instruction_suggestions
WHERE item_ids && (SELECT coalesce(array_agg(id), '{}'::uuid[]) FROM agents.review_items
                   WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer);

-- name: DeleteSuggestionsToErase :execrows
-- A suggestion rests on review items and may quote them: it goes with them.
DELETE FROM agents.instruction_suggestions
WHERE item_ids && (SELECT coalesce(array_agg(id), '{}'::uuid[]) FROM agents.review_items
                   WHERE conversation_id = ANY(@conversation_ids::uuid[]) OR customer_id = sqlc.narg(customer_id)::integer);

-- name: DeleteSuggestionsBefore :execrows
DELETE FROM agents.instruction_suggestions WHERE created_at < @cutoff::timestamptz;

-- name: MarkAnsweredOver :execrows
-- A person answered in the conversation (V21): an Auto reply the agent sent in
-- the 24 hours before is answered over.
UPDATE agents.runs SET answered_over_at = @now::timestamptz
WHERE conversation_id = @conversation_id AND outcome = 'sent' AND answered_over_at IS NULL
  AND sent_at IS NOT NULL AND sent_at <= @now::timestamptz AND sent_at > @since::timestamptz;

-- name: ListRolesDueFeedback :many
-- The roles of active agents the weekly check has not looked at since the week began.
SELECT r.* FROM agents.roles r JOIN agents.agents a ON a.id = r.agent_id
WHERE a.state = 'active' AND (r.feedback_checked_at IS NULL OR r.feedback_checked_at < @week_start::timestamptz)
ORDER BY r.created_at, r.id;

-- name: ClaimRoleFeedback :execrows
-- One replica claims a role's check for the week; the others find it claimed.
UPDATE agents.roles SET feedback_checked_at = @now::timestamptz
WHERE id = @id AND (feedback_checked_at IS NULL OR feedback_checked_at < @week_start::timestamptz);

-- name: SetRoleFeedbackChecked :exec
-- Gives a claim back (to the time before it), when the check could not be made.
UPDATE agents.roles SET feedback_checked_at = sqlc.narg(at)::timestamptz WHERE id = @id;

-- name: CountFeedbackItems :one
SELECT count(*) FROM agents.review_items
WHERE role_id = @role_id AND kind = 'reply' AND decided_at > @since::timestamptz
  AND (state = 'discarded' OR (state = 'edited_sent' AND edit_ratio > @ratio::real));

-- name: ListFeedbackItems :many
-- What a person corrected since the check last looked: discarded replies, and
-- replies edited beyond the ratio (V28).
SELECT id, state, discard_reason, discard_text, edit_ratio, item
FROM agents.review_items
WHERE role_id = @role_id AND kind = 'reply' AND decided_at > @since::timestamptz
  AND (state = 'discarded' OR (state = 'edited_sent' AND edit_ratio > @ratio::real))
ORDER BY decided_at DESC, id
LIMIT @n;

-- name: CountProposedSuggestions :one
SELECT count(*) FROM agents.instruction_suggestions WHERE role_id = @role_id AND state = 'proposed';

-- name: InsertSuggestion :exec
INSERT INTO agents.instruction_suggestions (id, role_id, old_text, new_text, rationale, item_ids, created_at)
VALUES (@id, @role_id, sqlc.narg(old_text)::text, @new_text, @rationale, @item_ids::uuid[], @now::timestamptz);

-- name: ListSuggestionsOfRole :many
SELECT * FROM agents.instruction_suggestions
WHERE role_id = @role_id AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state)::text)
ORDER BY created_at DESC, id
LIMIT @n;

-- name: GetSuggestion :one
SELECT * FROM agents.instruction_suggestions WHERE id = @id;

-- name: DecideSuggestion :one
UPDATE agents.instruction_suggestions SET state = @state, decided_by = @decided_by, decided_at = @now::timestamptz
WHERE id = @id AND state = 'proposed'
RETURNING *;
