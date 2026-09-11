-- name: InsertAuditEvent :exec
INSERT INTO identity.authorization_audit_events (
    actor_user_id, target_user_id, target_role_id, action, details,
    before_json, after_json, correlation_id, mfa_authenticated, occurred_at
) VALUES (
    @actor_user_id, @target_user_id, @target_role_id, @action, @details,
    @before_json, @after_json, @correlation_id, @mfa_authenticated, @occurred_at::timestamptz
);
