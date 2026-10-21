-- name: InsertAuditEvent :exec
INSERT INTO identity.authorization_audit_events (
    actor_user_id, target_user_id, target_role_id, action, details,
    before_json, after_json, correlation_id, mfa_authenticated, occurred_at
) VALUES (
    @actor_user_id, @target_user_id, @target_role_id, @action, @details,
    @before_json, @after_json, @correlation_id, @mfa_authenticated, @occurred_at::timestamptz
);

-- name: ListUserAuditEventsForErase :many
-- ListUserAuditEventsForErase lists the audit rows naming the erased user
-- (and any targetless announcement audit rows when the user held an employee
-- number) so their before_json and after_json snapshots can be pseudonymised
-- in place (Entra design D16).
SELECT id, target_user_id, action, before_json, after_json
FROM identity.authorization_audit_events
WHERE target_user_id = @user_id
   OR (sqlc.narg(employee_number_key)::text IS NOT NULL
       AND target_user_id IS NULL
       AND action IN ('account.expected', 'account.unexpected'))
ORDER BY id
FOR UPDATE;

-- name: UpdateAuditEventSnapshots :exec
UPDATE identity.authorization_audit_events
SET before_json = @before_json, after_json = @after_json
WHERE id = @id;

