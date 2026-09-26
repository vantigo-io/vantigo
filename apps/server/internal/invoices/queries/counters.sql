-- name: CounterNextValue :one
-- CounterNextValue reads the document counter's next_value without
-- allocating (D2). No row means nothing has ever been issued — "something is
-- issued" is "the counter row exists", never a count(*) — which the caller
-- reads from pgx.ErrNoRows.
SELECT next_value FROM invoices.counters WHERE counter_name = 'documents';

-- name: LatestIssueDate :one
-- LatestIssueDate is the latest issue date of any issued document, NULL when
-- nothing is issued. A rate change must start after it (D3), and an issue may
-- not be dated before it (D6); both read it after the lock that makes it
-- final.
SELECT max(issue_date)::date AS latest FROM invoices.invoices WHERE status = 'issued';

-- name: AllocateNumber :one
-- AllocateNumber takes the next number of the one series (D2): the first
-- allocation is series_start, every later one one more. The lock on the counter row
-- is held until the issue commits, so it is the one thing that serialises
-- two issues, and a rolled-back issue rolls its number back with it.
INSERT INTO invoices.counters (counter_name, next_value)
VALUES ('documents', sqlc.arg(series_start)::bigint + 1)
ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
RETURNING (next_value - 1)::bigint AS allocated;
