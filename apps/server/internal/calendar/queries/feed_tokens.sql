-- Feed addresses (calendar feed design D1, D2): at most one per person per
-- variant, full or busy. The token is found by its hex SHA-256 and never
-- stored; the whole address is kept sealed under calendar/feed-address so the
-- owner's screen can show it again. Every write but the GET's touch is the
-- owner's own, on their own rows.

-- name: GetFeedToken :one
-- GetFeedToken is the person's address of one variant.
SELECT * FROM calendar.feed_tokens
WHERE user_id = @user_id AND variant = @variant;

-- name: GetFeedTokenByHash :one
-- GetFeedTokenByHash is the address whose token hashes to token_sha256 (hex
-- SHA-256) — the feed GET's lookup, through ux_feed_tokens_hash. A token
-- nobody holds, a reset one among them, finds no row.
SELECT * FROM calendar.feed_tokens
WHERE token_sha256 = @token_sha256::char(64);

-- name: UpsertFeedToken :one
-- UpsertFeedToken creates the person's address of a variant, or resets the
-- one they have: a new hash and sealed address over the old, so the old
-- address is dead from this moment, created_at the reset's time and
-- last_used_at cleared, since nothing has read the new one yet.
INSERT INTO calendar.feed_tokens (user_id, variant, token_sha256, address_ciphertext, created_at)
VALUES (@user_id, @variant, @token_sha256::char(64), @address_ciphertext::text, @now::timestamptz)
ON CONFLICT (user_id, variant) DO UPDATE SET
    token_sha256 = EXCLUDED.token_sha256,
    address_ciphertext = EXCLUDED.address_ciphertext,
    created_at = EXCLUDED.created_at,
    last_used_at = NULL
RETURNING *;

-- name: TouchFeedToken :exec
-- TouchFeedToken records a feed request that answered 200 or 304, by the
-- token's hash, so the screen can say when the address was last read.
UPDATE calendar.feed_tokens SET last_used_at = @now::timestamptz
WHERE token_sha256 = @token_sha256::char(64);

-- name: ListFeedTokens :many
-- ListFeedTokens is the person's addresses, full before busy, as the screen
-- shows them.
SELECT * FROM calendar.feed_tokens
WHERE user_id = @user_id
ORDER BY variant DESC;

-- name: DeleteFeedToken :execrows
-- DeleteFeedToken turns the person's address of a variant off; the address is
-- dead from this moment. No row deleted is an address that did not exist.
DELETE FROM calendar.feed_tokens
WHERE user_id = @user_id AND variant = @variant;
