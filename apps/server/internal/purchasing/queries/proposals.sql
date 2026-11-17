-- name: DraftQuantities :many
-- DraftQuantities is what Purchasing's draft orders hold of the variants
-- given, per variant and site, in the variant's unit (A4): a proposal is the
-- need less it.
SELECT l.variant_id::int AS variant_id, o.site_id, sum(l.quantity)::text AS quantity
FROM purchasing.order_lines l
JOIN purchasing.orders o ON o.id = l.order_id
WHERE o.status = 'draft' AND l.kind = 'variant' AND l.variant_id = ANY(sqlc.arg(variant_ids)::int[])
GROUP BY l.variant_id, o.site_id;

-- name: SentSince :many
-- SentSince is, per (variant, site, since) given side by side, what orders
-- sent to the site after since hold of the variant — what a stored demand
-- computed at since does not count yet (A4): a closed line what it received,
-- an open one what it orders or more when more was received. A cancelled
-- order holds nothing.
WITH d AS (
    SELECT unnest(sqlc.arg(variant_ids)::int[]) AS variant_id, unnest(sqlc.arg(site_ids)::bigint[]) AS site_id,
           unnest(sqlc.arg(since)::timestamptz[]) AS since
)
SELECT d.variant_id::int AS variant_id, d.site_id::bigint AS site_id,
       sum(CASE WHEN l.closed_at IS NOT NULL THEN l.received ELSE greatest(l.quantity, l.received) END)::text AS quantity
FROM d
JOIN purchasing.orders o ON o.site_id = d.site_id AND o.sent_at > d.since AND o.status NOT IN ('draft', 'cancelled')
JOIN purchasing.order_lines l ON l.order_id = o.id AND l.kind = 'variant' AND l.variant_id = d.variant_id
GROUP BY d.variant_id, d.site_id;

-- name: SupplierItemsOfVariants :many
-- SupplierItemsOfVariants is every supplier's items of the variants given,
-- the preferred first, then by id.
SELECT * FROM purchasing.supplier_items
WHERE variant_id = ANY(sqlc.arg(variant_ids)::int[])
ORDER BY variant_id, preferred DESC, id;

-- name: SupplierItemsByIDs :many
SELECT * FROM purchasing.supplier_items WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: LockOldestDraft :one
-- LockOldestDraft is the oldest draft of the supplier for the site, locked:
-- the draft Create orders adds its lines to (P33).
SELECT * FROM purchasing.orders
WHERE status = 'draft' AND supplier_id = sqlc.arg(supplier_id) AND site_id = sqlc.arg(site_id)
ORDER BY id
LIMIT 1
FOR UPDATE;

-- name: LockProposalOrders :exec
-- LockProposalOrders serialises Create orders on the transaction's advisory
-- lock, so a second request sees the first's drafts before it judges its
-- rows.
SELECT pg_advisory_xact_lock(sqlc.arg(key)::bigint);

-- name: ProposalRequestChanges :many
-- ProposalRequestChanges is the change rows a Create orders request wrote,
-- by its clientRequestId, with their orders as they stand.
SELECT c.order_id, c.actions, o.supplier_id, o.supplier_name, o.site_id
FROM purchasing.order_changes c
JOIN purchasing.orders o ON o.id = c.order_id
WHERE c.actions @> jsonb_build_array(jsonb_build_object('requestId', sqlc.arg(request_id)::text))
ORDER BY c.order_id;

-- name: RefreshOrderTotal :exec
-- RefreshOrderTotal sums an order's lines into its total and moves its
-- updated_at.
UPDATE purchasing.orders
SET net_total = (SELECT coalesce(sum(l.line_net), 0) FROM purchasing.order_lines l WHERE l.order_id = sqlc.arg(id)),
    updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);
