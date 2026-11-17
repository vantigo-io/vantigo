-- name: Stats :one
-- Stats is the dashboard card's figures: active sites, active locations
-- below the roots, the stock lines (a variant on a location, its lots counted
-- once), the receipts since the week began — not one taken back since, whose
-- reversal names the same source — and the counts still counting.
SELECT (SELECT count(*) FROM inventory.sites WHERE status = 'active')::int AS sites,
       (SELECT count(*) FROM inventory.locations WHERE status = 'active' AND parent_id IS NOT NULL)::int AS locations,
       (SELECT count(DISTINCT (variant_id, location_id)) FROM inventory.balances)::int AS stock_lines,
       (SELECT count(*) FROM inventory.movements m WHERE m.kind = 'receipt' AND m.posted_at >= @week_start::timestamptz
            AND NOT EXISTS (SELECT 1 FROM inventory.movements x WHERE x.kind = 'receipt_reversal'
                AND x.source_module = m.source_module AND x.source_kind = m.source_kind AND x.source_id = m.source_id))::int AS receipts_this_week,
       (SELECT count(*) FROM inventory.counts WHERE status = 'counting')::int AS open_counts;
