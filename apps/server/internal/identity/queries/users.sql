-- name: GetUserByNormalizedEmail :one
SELECT id, email, normalized_email, email_confirmed, display_name, preferred_language,
       password_hash, is_disabled, lockout_end, failed_login_count, totp_secret,
       totp_enabled, totp_last_step, version, created_at, updated_at
FROM identity.users
WHERE normalized_email = $1;

-- name: InsertUser :exec
INSERT INTO identity.users (
    id, email, normalized_email, email_confirmed, display_name, preferred_language,
    password_hash, is_disabled, version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
);
