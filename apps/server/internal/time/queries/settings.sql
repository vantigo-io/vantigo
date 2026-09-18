-- name: GetSetting :one
-- GetSetting reads one installation-wide time setting by key; an unset key
-- answers no rows. 'locked_before' (D9) is the only key so far, stored as
-- YYYY-MM-DD.
SELECT value FROM time.settings WHERE key = @key;
