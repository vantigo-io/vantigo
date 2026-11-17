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
-- computed at since does not count yet (A4): a goods line's, as OpenPurchases
-- counts them — a closed line what it received, an open one what it orders
-- or more when more was received. A cancelled order holds nothing. The caller
-- passes since less a safety margin (sentSinceMargin).
WITH d AS (
    SELECT unnest(sqlc.arg(variant_ids)::int[]) AS variant_id, unnest(sqlc.arg(site_ids)::bigint[]) AS site_id,
           unnest(sqlc.arg(since)::timestamptz[]) AS since
)
SELECT d.variant_id::int AS variant_id, d.site_id::bigint AS site_id,
       sum(CASE WHEN l.closed_at IS NOT NULL THEN l.received ELSE greatest(l.quantity, l.received) END)::text AS quantity
FROM d
JOIN purchasing.orders o ON o.site_id = d.site_id AND o.sent_at > d.since AND o.status NOT IN ('draft', 'cancelled')
JOIN purchasing.order_lines l ON l.order_id = o.id AND l.kind = 'variant' AND l.product_type = 'Goods' AND l.variant_id = d.variant_id
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

-- name: ProposalRequest :one
-- ProposalRequest is a Create orders request by its clientRequestId: its
-- body's hash and the result it answered (A58).
SELECT request_hash, orders FROM purchasing.proposal_requests WHERE client_request_id = sqlc.arg(client_request_id);

-- name: InsertProposalRequest :exec
-- InsertProposalRequest keeps a Create orders request's result, in the
-- transaction that wrote its drafts.
INSERT INTO purchasing.proposal_requests (client_request_id, request_hash, orders, actor_user_id, created_at)
VALUES (sqlc.arg(client_request_id), sqlc.arg(request_hash), sqlc.arg(orders), sqlc.narg(actor_user_id), sqlc.arg(created_at));

-- name: RefreshOrderTotal :exec
-- RefreshOrderTotal sums an order's lines into its total and moves its
-- updated_at.
UPDATE purchasing.orders
SET net_total = (SELECT coalesce(sum(l.line_net), 0) FROM purchasing.order_lines l WHERE l.order_id = sqlc.arg(id)),
    updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);
