-- name: RateOn :one
-- RateOn is the rate of currency that holds on the day (I19): the latest row on or
-- before it and no more than seven days before. The primary key keeps one
-- row a day, and a manual entry replaces the fetched row of that day (reading 4),
-- so the row read is the manual one whenever one was entered. No row is
-- pgx.ErrNoRows.
SELECT * FROM invoices.exchange_rates
WHERE currency = @currency AND rate_date <= @day::date AND rate_date + 7 >= @day::date
ORDER BY rate_date DESC
LIMIT 1;

-- name: RatesBetween :many
-- RatesBetween is a page of the rates of currency from from_day to to_day, the
-- newest first, at most max_rows.
SELECT * FROM invoices.exchange_rates
WHERE currency = @currency AND rate_date >= sqlc.arg(first_day)::date AND rate_date <= sqlc.arg(last_day)::date
ORDER BY rate_date DESC
LIMIT sqlc.arg(max_rows)::int;

-- name: LatestRateDate :one
-- LatestRateDate is the newest day a fetched rate of currency is stored for,
-- NULL when none is.
SELECT max(rate_date)::date AS latest FROM invoices.exchange_rates
WHERE currency = @currency AND source = 'norges_bank';

-- name: UpsertFetchedRate :execrows
-- UpsertFetchedRate writes a rate Norges Bank published. A manual row of the
-- day is left as it is (I18, reading 4): the conflict clause skips it, and
-- refuse_exchange_rate_overwrite refuses it in SQL too. A fetched row whose
-- rate is unchanged is not rewritten, so no event is published for it.
INSERT INTO invoices.exchange_rates (currency, rate_date, rate, source, fetched_at)
VALUES (@currency, @rate_date, @rate, 'norges_bank', @fetched_at)
ON CONFLICT (currency, rate_date) DO UPDATE SET rate = EXCLUDED.rate, fetched_at = EXCLUDED.fetched_at
WHERE invoices.exchange_rates.source = 'norges_bank' AND invoices.exchange_rates.rate <> EXCLUDED.rate;

-- name: PutManualRate :one
-- PutManualRate writes or replaces a manual rate of the day, which may
-- replace a fetched one (reading 4).
INSERT INTO invoices.exchange_rates (currency, rate_date, rate, source, fetched_at, entered_by_user_id)
VALUES (@currency, @rate_date, @rate, 'manual', @entered_at, @entered_by_user_id)
ON CONFLICT (currency, rate_date) DO UPDATE SET rate = EXCLUDED.rate, source = 'manual',
    fetched_at = EXCLUDED.fetched_at, entered_by_user_id = EXCLUDED.entered_by_user_id
RETURNING *;

-- name: GetRate :one
SELECT * FROM invoices.exchange_rates WHERE currency = @currency AND rate_date = @rate_date;

-- name: DeleteManualRate :execrows
-- DeleteManualRate removes a manual rate; the next fetch fills the day.
DELETE FROM invoices.exchange_rates WHERE currency = @currency AND rate_date = @rate_date AND source = 'manual';

-- name: EnsureRateFetch :exec
-- EnsureRateFetch gives an enabled currency its fetch row.
INSERT INTO invoices.rate_fetches (currency) VALUES (@currency) ON CONFLICT (currency) DO NOTHING;

-- name: ClaimRateFetch :one
-- ClaimRateFetch leases currency's fetch row for one cycle, unless another
-- worker's lease still runs. FOR NO KEY UPDATE SKIP LOCKED, in one
-- statement: two workers never take one currency, and the claim holds no
-- connection while it fetches. No row is pgx.ErrNoRows.
UPDATE invoices.rate_fetches
SET lease_id = @lease_id::text, lease_until = @lease_until::timestamptz, last_attempt_at = @now::timestamptz
WHERE currency = (
    SELECT f.currency FROM invoices.rate_fetches f
    WHERE f.currency = @currency AND (f.lease_until IS NULL OR f.lease_until <= @now::timestamptz)
    FOR NO KEY UPDATE SKIP LOCKED
)
RETURNING *;

-- name: ReleaseRateFetch :execrows
-- ReleaseRateFetch ends a claim with its outcome, under its lease: a success
-- clears the error, a failure keeps the last success. No row is a claim
-- lost.
UPDATE invoices.rate_fetches SET
    lease_id = NULL, lease_until = NULL,
    last_success_at = CASE WHEN sqlc.narg(last_error)::text IS NULL THEN @now::timestamptz ELSE last_success_at END,
    last_error = sqlc.narg(last_error)::text
WHERE currency = @currency AND lease_id = @lease_id::text;

-- name: RateFetchStatus :many
SELECT * FROM invoices.rate_fetches ORDER BY currency;

-- name: EnabledCurrencies :one
SELECT currencies::text[] AS currencies FROM invoices.settings WHERE id = 1;

-- name: InsertExchangeDifference :exec
-- InsertExchangeDifference records the NOK rest a closing credit note left
-- on an invoice in another currency (review M1), under the issue's lock.
INSERT INTO invoices.exchange_differences (invoice_id, document_id, difference_day, amount_nok, registered_at)
VALUES (@invoice_id, @document_id, @difference_day, @amount_nok, @registered_at::timestamptz);

-- name: LiveRecoveriesNOK :one
-- LiveRecoveriesNOK is what the live recovered payments of a write-off took
-- in its currency and cleared of its NOK (review M2).
SELECT coalesce(sum(amount), 0)::numeric(14,2) AS amount,
       coalesce(sum(amount_nok - exchange_difference_nok), 0)::numeric(14,2) AS cleared_nok
FROM invoices.payments
WHERE write_off_id = @write_off_id AND kind = 'recovered' AND removed_at IS NULL;
