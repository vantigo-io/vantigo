-- The events (Google Workspace design G16): the module's own append-only
-- trail. A user or a system acts, exactly one; subject_id is a Google id,
-- never an address, and the codes are codes — never a key, a secret or a
-- token.

-- name: InsertEvent :exec
INSERT INTO google.events (at, actor_user_id, actor_system, action, subject_id, codes)
VALUES (@at, @actor_user_id, @actor_system, @action, @subject_id, @codes);

-- name: ListEvents :many
SELECT * FROM google.events ORDER BY at DESC, id DESC LIMIT @row_limit;
