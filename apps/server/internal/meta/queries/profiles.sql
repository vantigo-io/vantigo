-- name: GetProfile :one
SELECT display_name, fetched_at FROM meta.profiles WHERE page_id = @page_id AND psid = @psid;

-- name: UpsertProfile :exec
INSERT INTO meta.profiles (page_id, psid, display_name, fetched_at)
VALUES (@page_id, @psid, @display_name, @now)
ON CONFLICT (page_id, psid) DO UPDATE SET display_name = EXCLUDED.display_name, fetched_at = EXCLUDED.fetched_at;

-- name: DeleteOldProfiles :execrows
DELETE FROM meta.profiles WHERE fetched_at < @before;

-- name: DeleteProfilesForPage :exec
DELETE FROM meta.profiles WHERE page_id = @page_id;
