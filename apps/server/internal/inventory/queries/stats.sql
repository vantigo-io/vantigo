-- name: Stats :one
-- Stats is the dashboard card's figures: active sites, active locations
-- below the roots, the stock lines (a variant on a location, its lots counted
-- once), the receipts since the week began, and the counts still counting.
SELECT (SELECT count(*) FROM inventory.sites WHERE status = 'active')::int AS sites,
       (SELECT count(*) FROM inventory.locations WHERE status = 'active' AND parent_id IS NOT NULL)::int AS locations,
       (SELECT count(DISTINCT (variant_id, location_id)) FROM inventory.balances)::int AS stock_lines,
       (SELECT count(*) FROM inventory.movements WHERE kind = 'receipt' AND posted_at >= @week_start::timestamptz)::int AS receipts_this_week,
       (SELECT count(*) FROM inventory.counts WHERE status = 'counting')::int AS open_counts;
