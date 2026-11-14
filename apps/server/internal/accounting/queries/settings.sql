-- name: GetSettings :one
-- GetSettings is the settings row; no row means the books are not set up.
SELECT * FROM accounting.settings WHERE id = 1;

-- name: GetSettingsForShare :one
-- GetSettingsForShare reads the settings under a share lock (R10): a post
-- holds it to commit, so the lock cannot move under it.
SELECT * FROM accounting.settings WHERE id = 1 FOR SHARE;

-- name: GetSettingsForUpdate :one
-- GetSettingsForUpdate is how the lock moves (R10): every post in flight is
-- waited for.
SELECT * FROM accounting.settings WHERE id = 1 FOR UPDATE;

-- name: InsertSettings :one
INSERT INTO accounting.settings (id, books_start, fiscal_year_start_month, vat_term, chart_language,
    set_up_at, set_up_by_user_id, updated_at)
VALUES (1, sqlc.arg(books_start), sqlc.arg(fiscal_year_start_month), sqlc.arg(vat_term), sqlc.arg(chart_language),
    sqlc.arg(now), sqlc.arg(user_id), sqlc.arg(now))
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: SetLockedThrough :exec
UPDATE accounting.settings SET locked_through = sqlc.narg(locked_through), updated_at = sqlc.arg(now) WHERE id = 1;

-- name: SetOpeningAsOfSeq :exec
-- SetOpeningAsOfSeq records the journal fact the opening's open invoices
-- reflect (delivery B's rule for facts at or below it).
UPDATE accounting.settings SET opening_as_of_seq = sqlc.narg(seq), updated_at = sqlc.arg(now) WHERE id = 1;

-- name: InsertLockEvent :exec
INSERT INTO accounting.lock_events (locked_from, locked_to, reason, changed_at, changed_by_user_id)
VALUES (sqlc.narg(locked_from), sqlc.narg(locked_to), sqlc.narg(reason), sqlc.arg(now), sqlc.arg(user_id));

-- name: ListLockEvents :many
SELECT * FROM accounting.lock_events ORDER BY changed_at DESC, id DESC LIMIT 200;
