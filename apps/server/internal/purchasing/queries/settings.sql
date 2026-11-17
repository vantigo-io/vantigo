-- name: GetSettings :one
SELECT * FROM purchasing.settings WHERE id = 1;

-- name: LockSettings :one
-- LockSettings takes the one settings row FOR UPDATE, so a save judges the
-- series start against the counter (P20) while no other save runs.
SELECT * FROM purchasing.settings WHERE id = 1 FOR UPDATE;

-- name: UpsertSettings :one
-- UpsertSettings writes the one settings row, creating it on the first save,
-- and bumps its revision.
INSERT INTO purchasing.settings (
    id, order_series_start, default_site_id, send_channel, order_footer, delivery_addresses,
    price_tolerance_percent, revision, updated_at
) VALUES (
    1, sqlc.arg(order_series_start), sqlc.narg(default_site_id), sqlc.narg(send_channel), sqlc.arg(order_footer),
    sqlc.arg(delivery_addresses), sqlc.arg(price_tolerance_percent), 1, sqlc.arg(updated_at)
)
ON CONFLICT (id) DO UPDATE SET
    order_series_start      = EXCLUDED.order_series_start,
    default_site_id         = EXCLUDED.default_site_id,
    send_channel            = EXCLUDED.send_channel,
    order_footer            = EXCLUDED.order_footer,
    delivery_addresses      = EXCLUDED.delivery_addresses,
    price_tolerance_percent = EXCLUDED.price_tolerance_percent,
    revision                = purchasing.settings.revision + 1,
    updated_at              = EXCLUDED.updated_at
RETURNING *;

-- name: CounterNextValue :one
-- CounterNextValue reads a series' next number without allocating (P20). No
-- row means the series has never handed one out (pgx.ErrNoRows).
SELECT next_value FROM purchasing.counters WHERE counter_name = sqlc.arg(counter_name);

-- name: AllocateNumber :one
-- AllocateNumber takes the next number of a series (P20): the first is the
-- series start, every later one one more, never below a start the settings
-- raised since. The row's lock is held until the sending transaction
-- commits, so it serialises two sends, and a rolled-back send rolls its
-- number back with it: the series has no gaps.
INSERT INTO purchasing.counters (counter_name, next_value)
VALUES (sqlc.arg(counter_name), sqlc.arg(series_start)::bigint + 1)
ON CONFLICT (counter_name) DO UPDATE
    SET next_value = greatest(purchasing.counters.next_value, sqlc.arg(series_start)::bigint) + 1
RETURNING (next_value - 1)::bigint AS allocated;
