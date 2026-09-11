-- name: DeleteExpiredPasswordResetTokens :exec
DELETE FROM identity.password_reset_tokens
WHERE expires_at <= @now::timestamptz;

-- name: InsertPasswordResetToken :exec
INSERT INTO identity.password_reset_tokens (token_hash, user_id, expires_at)
VALUES (@token_hash, @user_id, @expires_at::timestamptz);

-- name: PasswordResetTokenValid :one
-- PasswordResetTokenValid reports whether the token is the user's and still
-- unexpired at now, without consuming it.
SELECT (EXISTS (
    SELECT 1 FROM identity.password_reset_tokens
    WHERE token_hash = @token_hash AND user_id = @user_id AND expires_at > @now::timestamptz
))::boolean AS valid;

-- name: ConsumePasswordResetToken :execrows
-- ConsumePasswordResetToken spends the token once: of two resets racing
-- with it, only one deletes the row.
DELETE FROM identity.password_reset_tokens
WHERE token_hash = @token_hash AND user_id = @user_id AND expires_at > @now::timestamptz;
