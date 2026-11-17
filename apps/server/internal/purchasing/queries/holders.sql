-- name: VariantDocuments :one
-- VariantDocuments is what names a variant (P10, A17): its supplier items and
-- the orders with a line of it, any state — what stops its delete.
SELECT ((SELECT count(*) FROM purchasing.supplier_items si WHERE si.variant_id = sqlc.arg(variant)::integer)
      + (SELECT count(DISTINCT ol.order_id) FROM purchasing.order_lines ol WHERE ol.variant_id = sqlc.arg(variant)::integer))::integer;

-- name: VariantOpenGoodsOrders :one
-- VariantOpenGoodsOrders is the drafts and the sent or partially received
-- orders with an open goods line of the variant (A17): what stops its
-- product's change to a service.
SELECT count(DISTINCT o.id)::integer FROM purchasing.orders o
JOIN purchasing.order_lines l ON l.order_id = o.id
WHERE l.variant_id = sqlc.arg(variant_id)::integer AND l.product_type = 'Goods' AND l.closed_at IS NULL AND l.received < l.quantity
  AND o.status IN ('draft', 'sent', 'partially_received');

-- name: SupplierDocuments :one
-- SupplierDocuments is every order, drafts included, and every supplier item
-- naming the supplier (P10, A23): what stops its delete.
SELECT ((SELECT count(*) FROM purchasing.orders po WHERE po.supplier_id = sqlc.arg(supplier)::bigint)
      + (SELECT count(*) FROM purchasing.supplier_items si WHERE si.supplier_id = sqlc.arg(supplier)::bigint))::integer;

-- name: SupplierOpenOrders :one
-- SupplierOpenOrders is the drafts and the sent or partially received orders
-- naming the supplier (A23): what stops its anonymisation.
SELECT count(*)::integer FROM purchasing.orders
WHERE supplier_id = sqlc.arg(supplier_id) AND status IN ('draft', 'sent', 'partially_received');

-- name: SupplierOrders :many
-- SupplierOrders is every order naming the supplier, for its export (P7).
SELECT * FROM purchasing.orders WHERE supplier_id = sqlc.arg(supplier_id) ORDER BY id;

-- name: SupplierMailings :many
SELECT m.* FROM purchasing.mailings m JOIN purchasing.orders o ON o.id = m.order_id
WHERE o.supplier_id = sqlc.arg(supplier_id) ORDER BY m.id;

-- name: EraseSupplierOrders :execrows
-- EraseSupplierOrders blanks the supplier's name, address, e-mail and
-- contact on its orders (P7, A8), once.
UPDATE purchasing.orders SET
    supplier_name = '', supplier_organisation_number = NULL, supplier_address_line = NULL, supplier_postal_code = NULL,
    supplier_city = NULL, supplier_email = NULL, supplier_contact = '', anonymised_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE supplier_id = sqlc.arg(supplier_id) AND anonymised_at IS NULL;

-- name: EraseSupplierMailings :execrows
-- EraseSupplierMailings blanks the addresses the supplier's orders were
-- mailed to, the message and the provider's last refusal, which may quote
-- them.
UPDATE purchasing.mailings m SET recipient = '', cc = '', message = '', last_error = NULL
FROM purchasing.orders o
WHERE o.id = m.order_id AND o.supplier_id = sqlc.arg(supplier_id)
  AND (m.recipient <> '' OR m.cc <> '' OR m.message <> '' OR m.last_error IS NOT NULL);

-- name: SupplierChanges :many
-- SupplierChanges is the change log of every order naming the supplier, for
-- its export (P7).
SELECT c.* FROM purchasing.order_changes c JOIN purchasing.orders o ON o.id = c.order_id
WHERE o.supplier_id = sqlc.arg(supplier_id) ORDER BY c.order_id, c.id;

-- name: EraseSupplierChanges :execrows
-- EraseSupplierChanges strips the from and to values of the supplier's
-- orders' changes to the fields that may name a person at the supplier —
-- the contact and the two notes — keeping that the field changed.
UPDATE purchasing.order_changes c SET actions = (
    SELECT coalesce(jsonb_agg(CASE WHEN e.a->>'field' IN ('supplierContact', 'note', 'internalNote') THEN e.a - 'from' - 'to' ELSE e.a END
        ORDER BY e.n), '[]'::jsonb)
    FROM jsonb_array_elements(c.actions) WITH ORDINALITY AS e(a, n))
FROM purchasing.orders o
WHERE o.id = c.order_id AND o.supplier_id = sqlc.arg(supplier_id)
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(c.actions) x
      WHERE x->>'field' IN ('supplierContact', 'note', 'internalNote') AND (x ? 'from' OR x ? 'to'));

-- name: EraseSupplierDocuments :many
-- EraseSupplierDocuments marks the stored PDFs of the supplier's orders
-- erased and answers their keys, for the deletion ledger.
UPDATE purchasing.documents d SET erased_at = sqlc.arg(now)
FROM purchasing.orders o
WHERE o.id = d.order_id AND o.supplier_id = sqlc.arg(supplier_id) AND d.erased_at IS NULL
RETURNING d.storage_key;

-- name: InsertDeletion :exec
INSERT INTO purchasing.deletions (storage_key, requested_at) VALUES (sqlc.arg(storage_key), sqlc.arg(requested_at))
ON CONFLICT (storage_key) DO NOTHING;

-- name: OpenPurchases :many
-- OpenPurchases is what is ordered and not received of each (variant, site)
-- (P4, A3): goods variant lines of sent and partially received orders,
-- not closed, with more ordered than received, and their earliest expected
-- day — the line's, else the order's.
SELECT l.variant_id::integer AS variant_id, o.site_id, sum(l.quantity - l.received)::numeric AS quantity,
    min(coalesce(l.expected_on, o.expected_on))::date AS expected_on
FROM purchasing.order_lines l JOIN purchasing.orders o ON o.id = l.order_id
WHERE o.status IN ('sent', 'partially_received') AND l.kind = 'variant' AND l.product_type = 'Goods'
  AND l.closed_at IS NULL AND l.quantity > l.received
  AND (cardinality(sqlc.arg(variant_ids)::integer[]) = 0 OR l.variant_id = ANY(sqlc.arg(variant_ids)::integer[]))
  AND (sqlc.narg(site_id)::bigint IS NULL OR o.site_id = sqlc.narg(site_id))
GROUP BY l.variant_id, o.site_id
ORDER BY l.variant_id, o.site_id;
