-- name: Stats :one
-- Stats is the dashboard card's figures: active sites, active locations
-- below the roots, the balances, and the receipts since the week began.
SELECT (SELECT count(*) FROM inventory.sites WHERE status = 'active')::int AS sites,
       (SELECT count(*) FROM inventory.locations WHERE status = 'active' AND parent_id IS NOT NULL)::int AS locations,
       (SELECT count(*) FROM inventory.balances)::int AS stock_lines,
       (SELECT count(*) FROM inventory.movements WHERE kind = 'receipt' AND posted_at >= @week_start::timestamptz)::int AS receipts_this_week;
