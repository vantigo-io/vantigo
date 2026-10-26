-- The wishes (design S21, §3.5): a person's, off a named project or for
-- something new, from a date, without a word of free text. The table holds
-- open wishes only: a resolved or withdrawn wish is deleted, its event
-- keeping the kind, the project id and the date. Every write runs under the
-- person's lock of locks.go (S23), after the wish it changes was read FOR
-- UPDATE.

-- name: InsertWish :one
-- InsertWish writes a new open wish, not yet seen.
INSERT INTO planning.wishes (person_id, kind, project_id, from_date, created_at)
VALUES (@person_id, @kind, sqlc.narg(project_id), @from_date, @now::timestamptz)
RETURNING *;

-- name: WishByID :one
-- WishByID reads one wish. No row is an unknown id — or a wish already
-- resolved or withdrawn, which is the same thing.
SELECT * FROM planning.wishes WHERE id = @id;

-- name: LockWish :one
-- LockWish reads one wish and holds it for the rest of the transaction,
-- after the person's lock, so a write judges the row it then writes.
SELECT * FROM planning.wishes WHERE id = @id FOR UPDATE;

-- name: DeleteWish :one
-- DeleteWish deletes one wish — resolved, withdrawn, or its person erased
-- (S26) — answering the row as it stood, which its event describes.
DELETE FROM planning.wishes WHERE id = @id RETURNING *;

-- name: MarkWishSeen :one
-- MarkWishSeen stamps one wish seen by the caller (S21).
UPDATE planning.wishes SET seen_at = @now::timestamptz, seen_by_user_id = sqlc.narg(user_id)::uuid
WHERE id = @id
RETURNING *;

-- name: OpenWishesOfPerson :many
-- OpenWishesOfPerson is one person's open wishes, oldest first: My planning
-- and the person's timeline.
SELECT * FROM planning.wishes
WHERE person_id = @person_id
ORDER BY created_at, id;

-- name: ListOpenWishes :many
-- ListOpenWishes is GET /wishes: every open wish, or one person's when
-- @person_id is given, oldest first.
SELECT * FROM planning.wishes
WHERE (sqlc.narg(person_id)::bigint IS NULL OR person_id = sqlc.narg(person_id)::bigint)
ORDER BY created_at, id;

-- name: CountOpenWishesOfPerson :one
-- CountOpenWishesOfPerson is how many open wishes one person has, read under
-- their lock: at most five (wish_limit).
SELECT count(*)::integer FROM planning.wishes WHERE person_id = @person_id;

-- name: OffWishExists :one
-- OffWishExists reports whether the person already wishes off the project,
-- read under their lock (wish_exists; ux_wishes_person_project_off is the
-- backstop).
SELECT EXISTS (
    SELECT 1 FROM planning.wishes
    WHERE person_id = @person_id AND project_id = @project_id AND kind = 'off'
);

-- name: UnseenWishes :many
-- UnseenWishes is the open wishes no planner has stamped seen, oldest
-- first: the wishesUnseen item (S22).
SELECT * FROM planning.wishes
WHERE seen_at IS NULL
ORDER BY created_at, id;

-- name: PersonHasConfirmedRowOnProjectFrom :one
-- PersonHasConfirmedRowOnProjectFrom reports whether the person has a
-- confirmed row on the project ending on @today or later, read under their
-- lock: what an off wish needs (wish_no_booking).
SELECT EXISTS (
    SELECT 1 FROM planning.allocations
    WHERE person_id = @person_id AND project_id = @project_id
      AND status = 'confirmed' AND to_date >= @today::date
);
