-- name: UpsertWakeup :exec
-- One pending wakeup per role and subject: a second one moves the first (a
-- follow-up asked for again, a quote revised and sent again).
INSERT INTO agents.wakeups (id, role_id, subject_type, subject_id, customer_id, at, reason, created_at)
VALUES (@id, @role_id, @subject_type, @subject_id, sqlc.narg(customer_id)::integer, @at::timestamptz, @reason, @now::timestamptz)
ON CONFLICT (role_id, subject_type, subject_id) WHERE taken_at IS NULL
DO UPDATE SET at = EXCLUDED.at, reason = EXCLUDED.reason, customer_id = EXCLUDED.customer_id;

-- name: InsertScheduleWakeup :execrows
-- The wakeup of one occurrence of a role's schedule; the same occurrence is
-- never woken twice, even after it was taken.
INSERT INTO agents.wakeups (id, role_id, subject_type, subject_id, at, reason, created_at)
SELECT @id, @role_id, 'schedule', @subject_id, @at::timestamptz, @reason, @now::timestamptz
WHERE NOT EXISTS (SELECT 1 FROM agents.wakeups WHERE role_id = @role_id AND subject_type = 'schedule' AND subject_id = @subject_id);

-- name: LockDueWakeups :many
-- The wakeups that came due, of active agents' roles. A paused agent's stay
-- pending: they fire when it is active again.
SELECT w.*, r.agent_id FROM agents.wakeups w
JOIN agents.roles r ON r.id = w.role_id
JOIN agents.agents a ON a.id = r.agent_id
WHERE w.taken_at IS NULL AND w.at <= @now::timestamptz AND a.state = 'active'
ORDER BY w.at, w.id
LIMIT @n
FOR UPDATE OF w SKIP LOCKED;

-- name: TakeWakeup :exec
UPDATE agents.wakeups SET taken_at = @now::timestamptz WHERE id = @id;

-- name: DeleteWakeupsOfSubject :exec
-- A quote accepted, a conversation handed off: what waited to look at it is moot.
DELETE FROM agents.wakeups WHERE subject_type = @subject_type AND subject_id = @subject_id AND taken_at IS NULL;

-- name: CountPendingWakeupsOfRun :one
-- How many pending wakeups the follow_up tool has made for a role and subject.
SELECT count(*) FROM agents.wakeups WHERE role_id = @role_id AND subject_type = @subject_type AND subject_id = @subject_id AND taken_at IS NULL;

-- name: DeleteTakenWakeupsBefore :execrows
DELETE FROM agents.wakeups WHERE taken_at IS NOT NULL AND taken_at < @before::timestamptz;

-- name: DeleteWakeupsOfCustomer :execrows
-- A customer's erasure takes what was waiting to look at their quotes and conversations.
DELETE FROM agents.wakeups WHERE customer_id = sqlc.narg(customer_id)::integer;

-- name: DeleteRequestsOfCustomer :execrows
DELETE FROM agents.run_queue WHERE customer_id = sqlc.narg(customer_id)::integer;

-- name: DeleteStartItemsOfContacts :many
-- The conversations an agent would start to an erased contact (V26): the contact's id is kept on the item.
DELETE FROM agents.review_items WHERE kind = 'start' AND (item->>'contactId')::integer = ANY(@contact_ids::integer[])
RETURNING run_id;

-- name: DeleteRunsByIds :execrows
DELETE FROM agents.runs WHERE id = ANY(@ids::uuid[]);
