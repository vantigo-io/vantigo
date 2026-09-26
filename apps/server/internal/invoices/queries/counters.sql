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
