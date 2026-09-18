-- name: GetSetting :one
-- GetSetting reads one installation-wide time setting by key; an unset key
-- answers no rows. 'locked_before' (D9) is the only key so far, stored as
-- YYYY-MM-DD.
SELECT value FROM time.settings WHERE key = @key;

-- name: SetSetting :exec
-- SetSetting stores one setting, replacing what the key held.
INSERT INTO time.settings (key, value) VALUES (@key, @value)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- name: DeleteSetting :exec
-- DeleteSetting removes one setting; an unset key stays unset.
DELETE FROM time.settings WHERE key = @key;
