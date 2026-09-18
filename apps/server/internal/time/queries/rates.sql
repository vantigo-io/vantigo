-- name: EffectivePersonRate :one
-- EffectivePersonRate is the person rate card row in effect on a day: the
-- latest one whose valid_from is on or before it (design §4.3). A person with
-- no such row answers no rows, which the rate chain reads as "no person
-- rate".
SELECT * FROM time.person_rates
WHERE user_id = @user_id AND valid_from <= @on_date
ORDER BY valid_from DESC
LIMIT 1;

-- name: ListPersonRates :many
-- ListPersonRates is the rate card rows, one person's (user_id) or everyone's
-- (NULL), each person's latest first. The handler orders people by name,
-- which lives in identity.
SELECT * FROM time.person_rates
WHERE sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid
ORDER BY user_id, valid_from DESC;

-- name: GetPersonRate :one
SELECT * FROM time.person_rates WHERE id = @id;

-- name: InsertPersonRate :one
-- InsertPersonRate adds a rate card row. A second row for the same person and
-- day raises 23505 on ux_person_rates_user_id_valid_from, which the handler
-- turns into the validFrom field error.
INSERT INTO time.person_rates (user_id, valid_from, bill_rate, cost_rate, currency, created_at, updated_at)
VALUES (@user_id, @valid_from, @bill_rate, @cost_rate, @currency, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdatePersonRate :one
-- UpdatePersonRate replaces a row's day, rates and currency; the person stays
-- the row's own. No row is an unknown id.
UPDATE time.person_rates SET
    valid_from = @valid_from,
    bill_rate = @bill_rate,
    cost_rate = @cost_rate,
    currency = @currency,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeletePersonRate :execrows
-- DeletePersonRate removes a row. Entries it priced keep their snapshots.
DELETE FROM time.person_rates WHERE id = @id;
