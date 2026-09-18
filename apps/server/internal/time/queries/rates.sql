-- name: EffectivePersonRate :one
-- EffectivePersonRate is the person rate card row in effect on a day: the
-- latest one whose valid_from is on or before it (design §4.3). A person with
-- no such row answers no rows, which the rate chain reads as "no person
-- rate".
SELECT * FROM time.person_rates
WHERE user_id = @user_id AND valid_from <= @on_date
ORDER BY valid_from DESC
LIMIT 1;
