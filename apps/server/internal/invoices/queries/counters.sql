-- name: CounterNextValue :one
-- CounterNextValue reads the document counter's next_value without
-- allocating (D2). No row means nothing has ever been issued — "something is
-- issued" is "the counter row exists", never a count(*) — which the caller
-- reads from pgx.ErrNoRows.
SELECT next_value FROM invoices.counters WHERE counter_name = 'documents';
