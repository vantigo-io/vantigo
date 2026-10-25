-- The one-to-one (staff competence design S8–S11, §3.3–3.7, delivery B):
-- what the person wants — their interests, their preferences and their
-- availability declaration, each replaced by the person alone as a whole list
-- and kept row by row where it did not change — the Seen stamp the manager or
-- HR sets on every row without one and on the person's current competence
-- rows, and the competence reviews. Every write runs under its person's lock
-- (withPersonLock); an interest's write then shares the competence it names
-- (ShareCompetence), as a person's competence row does (§4 Locking).

-- name: ListPersonInterests :many
-- ListPersonInterests is a person's interests in the catalogue's order, each
-- with its competence and whether the person holds a current row of it — the
-- interest then reads "wants to use more", else "wants to learn" (S8).
SELECT sqlc.embed(i),
       c.key AS competence_key, c.name_en AS competence_name_en, c.name_nb AS competence_name_nb,
       c.kind AS competence_kind, c.has_level AS competence_has_level, c.category_id AS competence_category_id,
       c.archived_at AS competence_archived_at, c.hidden AS competence_hidden,
       EXISTS (
           SELECT 1 FROM staff.person_competences pc
           WHERE pc.person_id = i.person_id AND pc.competence_id = i.competence_id AND pc.superseded_by_id IS NULL
       ) AS held
FROM staff.person_interests i
JOIN staff.competences c ON c.id = i.competence_id
JOIN staff.competence_categories k ON k.id = c.category_id
WHERE i.person_id = @person_id
ORDER BY k.sort, k.id, c.sort, c.id;

-- name: InsertPersonInterest :exec
-- InsertPersonInterest adds one interest, unstamped.
INSERT INTO staff.person_interests (person_id, competence_id, created_at)
VALUES (@person_id, @competence_id, @now::timestamptz);

-- name: DeletePersonInterestsNotIn :execrows
-- DeletePersonInterestsNotIn removes the person's interests in every
-- competence but the ones kept, which keep their rows and their stamps.
DELETE FROM staff.person_interests
WHERE person_id = @person_id AND NOT (competence_id = ANY(@keep::bigint[]));

-- name: ListPersonPreferences :many
-- ListPersonPreferences is a person's preferences, by kind.
SELECT * FROM staff.person_preferences WHERE person_id = @person_id ORDER BY kind;

-- name: InsertPersonPreference :exec
-- InsertPersonPreference adds one preference, unstamped; value is the kind's
-- canonical object.
INSERT INTO staff.person_preferences (person_id, kind, value, weight, created_at, updated_at)
VALUES (@person_id, @kind, @value::jsonb, @weight, @now::timestamptz, @now::timestamptz);

-- name: UpdatePersonPreference :exec
-- UpdatePersonPreference replaces the value and weight of the person's
-- preference of a kind and clears its stamp, so the change reads as new.
UPDATE staff.person_preferences SET
    value = @value::jsonb,
    weight = @weight,
    seen_at = NULL,
    seen_by_user_id = NULL,
    updated_at = @now::timestamptz
WHERE person_id = @person_id AND kind = @kind;

-- name: DeletePersonPreferencesNotIn :execrows
-- DeletePersonPreferencesNotIn removes the person's preferences of every kind
-- but the ones kept.
DELETE FROM staff.person_preferences
WHERE person_id = @person_id AND NOT (kind = ANY(@keep::text[]));

-- name: ListAvailabilityDeclarations :many
-- ListAvailabilityDeclarations is a person's availability declaration, the
-- recurring rows by weekday first, then the dated ones by day, each by its
-- start.
SELECT * FROM staff.availability_declarations
WHERE person_id = @person_id
ORDER BY weekday NULLS LAST, from_date, start_minute, id;

-- name: InsertAvailabilityDeclaration :exec
-- InsertAvailabilityDeclaration adds one row, unstamped: recurring when
-- weekday is given, dated otherwise; the whole day without the minutes.
INSERT INTO staff.availability_declarations (
    person_id, kind, weekday, from_date, to_date, start_minute, end_minute, created_at
) VALUES (
    @person_id, @kind, sqlc.narg(weekday), sqlc.narg(from_date), sqlc.narg(to_date), sqlc.narg(start_minute),
    sqlc.narg(end_minute), @now::timestamptz
);

-- name: DeleteAvailabilityDeclarations :execrows
-- DeleteAvailabilityDeclarations removes the person's rows of those ids — the
-- ones the replace no longer sends.
DELETE FROM staff.availability_declarations WHERE person_id = @person_id AND id = ANY(@ids::bigint[]);

-- name: StampInterestsSeen :execrows
-- StampInterestsSeen stamps every interest of the person that has no stamp
-- (S8), answering how many.
UPDATE staff.person_interests SET seen_at = @now::timestamptz, seen_by_user_id = @user_id::uuid
WHERE person_id = @person_id AND seen_at IS NULL;

-- name: StampPreferencesSeen :execrows
-- StampPreferencesSeen stamps every preference of the person that has no
-- stamp, as StampInterestsSeen.
UPDATE staff.person_preferences SET seen_at = @now::timestamptz, seen_by_user_id = @user_id::uuid
WHERE person_id = @person_id AND seen_at IS NULL;

-- name: StampDeclarationsSeen :execrows
-- StampDeclarationsSeen stamps every availability row of the person that has
-- no stamp, as StampInterestsSeen.
UPDATE staff.availability_declarations SET seen_at = @now::timestamptz, seen_by_user_id = @user_id::uuid
WHERE person_id = @person_id AND seen_at IS NULL;

-- name: StampCompetencesSeen :execrows
-- StampCompetencesSeen stamps every current competence row of the person that
-- has no stamp (plan reading 17): a declaration read and not yet verified is
-- thereby acknowledged. The row's revision is left alone — the stamp is no
-- change of the row's.
UPDATE staff.person_competences SET seen_at = @now::timestamptz, seen_by_user_id = @user_id::uuid
WHERE person_id = @person_id AND seen_at IS NULL AND superseded_by_id IS NULL;

-- name: ListCompetenceReviews :many
-- ListCompetenceReviews is a person's reviews, newest first.
SELECT * FROM staff.competence_reviews WHERE person_id = @person_id ORDER BY reviewed_on DESC, id DESC;

-- name: InsertCompetenceReview :one
-- InsertCompetenceReview records a review (S11); reviewer_person_id is the
-- reviewer's linked person, NULL when they have none.
INSERT INTO staff.competence_reviews (person_id, reviewed_on, reviewer_user_id, reviewer_person_id, created_at)
VALUES (@person_id, @reviewed_on, @reviewer_user_id, sqlc.narg(reviewer_person_id), @now::timestamptz)
RETURNING *;

-- name: LatestCompetenceReview :one
-- LatestCompetenceReview is a person's latest review, for the person's
-- lastCompetenceReview; no row when there is none.
SELECT * FROM staff.competence_reviews
WHERE person_id = @person_id
ORDER BY reviewed_on DESC, id DESC
LIMIT 1;

-- name: LatestCompetenceReviews :many
-- LatestCompetenceReviews is every person's latest review, for the overdue
-- attention item; a person never reviewed has no row.
SELECT DISTINCT ON (person_id) person_id, reviewed_on, reviewer_user_id
FROM staff.competence_reviews
ORDER BY person_id, reviewed_on DESC, id DESC;

-- name: DeletePersonInterests :exec
-- DeletePersonInterests removes every interest of the person, for the delete
-- of a person created in error (people_delete.go): the rows' key to the
-- person has no cascade.
DELETE FROM staff.person_interests WHERE person_id = @person_id;

-- name: DeletePersonPreferences :exec
-- DeletePersonPreferences removes every preference of the person, as
-- DeletePersonInterests.
DELETE FROM staff.person_preferences WHERE person_id = @person_id;

-- name: DeletePersonAvailability :exec
-- DeletePersonAvailability removes every row of the person's availability
-- declaration, as DeletePersonInterests.
DELETE FROM staff.availability_declarations WHERE person_id = @person_id;

-- name: DeletePersonCompetenceReviews :exec
-- DeletePersonCompetenceReviews removes every review of the person, as
-- DeletePersonInterests.
DELETE FROM staff.competence_reviews WHERE person_id = @person_id;
