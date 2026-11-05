-- name: CounterNextValue :one
-- CounterNextValue reads a series' next number without allocating (O12). No
-- row means the series has never handed one out, which the caller reads from
-- pgx.ErrNoRows.
SELECT next_value FROM orders.counters WHERE counter_name = sqlc.arg(counter_name);

-- name: AllocateNumber :one
-- AllocateNumber takes the next number of a series (O12): the first
-- allocation is series_start, every later one one more — and never below a
-- start the settings raised since. The row's lock is held until the
-- creating transaction commits, so it serialises two creates, and a
-- rolled-back create rolls its number back with it.
INSERT INTO orders.counters (counter_name, next_value)
VALUES (sqlc.arg(counter_name), sqlc.arg(series_start)::bigint + 1)
ON CONFLICT (counter_name) DO UPDATE
    SET next_value = greatest(orders.counters.next_value, sqlc.arg(series_start)::bigint) + 1
RETURNING (next_value - 1)::bigint AS allocated;
