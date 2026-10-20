-- The events (Entra design D13): the module's own append-only trail of its
-- writes, read on the status card. A user or a system acts, exactly one; the
-- UPN is the one the module made or found, and the codes are codes — never
-- an address beyond the UPN, a secret or a pass.

-- name: InsertEvent :exec
INSERT INTO entra.events (at, actor_user_id, actor_system, action, object_id, upn, codes)
VALUES (@at, @actor_user_id, @actor_system, @action, @object_id, @upn, @codes);

-- name: ListEvents :many
SELECT * FROM entra.events ORDER BY at DESC, id DESC LIMIT @row_limit;
