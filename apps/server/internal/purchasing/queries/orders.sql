-- name: GetOrder :one
SELECT * FROM purchasing.orders WHERE id = sqlc.arg(id);

-- name: LockOrder :one
SELECT * FROM purchasing.orders WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: OrderBySendRequest :one
-- OrderBySendRequest is the order a Send or a Mark as sent of this
-- clientRequestId sent (A11).
SELECT * FROM purchasing.orders WHERE send_client_request_id = sqlc.arg(client_request_id);

-- name: InsertOrder :one
INSERT INTO purchasing.orders (
    supplier_id, supplier_number, supplier_name, supplier_organisation_number, supplier_address_line,
    supplier_postal_code, supplier_city, supplier_country, supplier_email, supplier_contact,
    site_id, currency, language, order_date, expected_on, buyer_id, buyer_display, note, internal_note, net_total,
    copied_from_id, created_by_user_id, created_by_display, created_at, updated_at
) VALUES (
    sqlc.arg(supplier_id), sqlc.narg(supplier_number), sqlc.arg(supplier_name), sqlc.narg(supplier_organisation_number),
    sqlc.narg(supplier_address_line), sqlc.narg(supplier_postal_code), sqlc.narg(supplier_city), sqlc.narg(supplier_country),
    sqlc.narg(supplier_email), sqlc.arg(supplier_contact), sqlc.arg(site_id), sqlc.arg(currency), sqlc.arg(language),
    sqlc.arg(order_date), sqlc.narg(expected_on), sqlc.narg(buyer_id), sqlc.narg(buyer_display), sqlc.arg(note),
    sqlc.arg(internal_note), sqlc.arg(net_total), sqlc.narg(copied_from_id), sqlc.narg(created_by_user_id),
    sqlc.narg(created_by_display), sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: UpdateOrderHeader :one
-- UpdateOrderHeader writes an order's header as saved (P15, P18). The
-- supplier snapshot is a draft's, following the register until it is sent.
UPDATE purchasing.orders SET
    supplier_id                  = sqlc.arg(supplier_id),
    supplier_number              = sqlc.narg(supplier_number),
    supplier_name                = sqlc.arg(supplier_name),
    supplier_organisation_number = sqlc.narg(supplier_organisation_number),
    supplier_address_line        = sqlc.narg(supplier_address_line),
    supplier_postal_code         = sqlc.narg(supplier_postal_code),
    supplier_city                = sqlc.narg(supplier_city),
    supplier_country             = sqlc.narg(supplier_country),
    supplier_email               = sqlc.narg(supplier_email),
    supplier_contact             = sqlc.arg(supplier_contact),
    site_id                      = sqlc.arg(site_id),
    currency                     = sqlc.arg(currency),
    language                     = sqlc.arg(language),
    order_date                   = sqlc.arg(order_date),
    expected_on                  = sqlc.narg(expected_on),
    buyer_id                     = sqlc.narg(buyer_id),
    buyer_display                = sqlc.narg(buyer_display),
    note                         = sqlc.arg(note),
    internal_note                = sqlc.arg(internal_note),
    net_total                    = sqlc.arg(net_total),
    updated_at                   = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SendOrder :one
-- SendOrder freezes a draft as sent (P18, P20): its number, its first
-- version, the supplier's and the delivery address's snapshots, how it was
-- sent and by whom, and the clientRequestId and hash a replay is judged by.
UPDATE purchasing.orders SET
    number                       = sqlc.arg(number),
    status                       = 'sent',
    version                      = 1,
    supplier_number              = sqlc.narg(supplier_number),
    supplier_name                = sqlc.arg(supplier_name),
    supplier_organisation_number = sqlc.narg(supplier_organisation_number),
    supplier_address_line        = sqlc.narg(supplier_address_line),
    supplier_postal_code         = sqlc.narg(supplier_postal_code),
    supplier_city                = sqlc.narg(supplier_city),
    supplier_country             = sqlc.narg(supplier_country),
    supplier_email               = sqlc.narg(supplier_email),
    delivery_name                = sqlc.narg(delivery_name),
    delivery_address_line1       = sqlc.narg(delivery_address_line1),
    delivery_address_line2       = sqlc.narg(delivery_address_line2),
    delivery_postal_code         = sqlc.narg(delivery_postal_code),
    delivery_city                = sqlc.narg(delivery_city),
    delivery_country             = sqlc.narg(delivery_country),
    sent_at                      = sqlc.arg(now),
    sent_by_user_id              = sqlc.narg(sent_by_user_id),
    sent_by_display              = sqlc.narg(sent_by_display),
    send_method                  = sqlc.arg(send_method),
    send_client_request_id       = sqlc.arg(client_request_id),
    send_request_hash            = sqlc.arg(request_hash),
    updated_at                   = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetOrderVersion :one
-- SetOrderVersion counts one more PDF sent (Send again, P18).
UPDATE purchasing.orders SET version = sqlc.arg(version), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetOrderState :one
-- SetOrderState moves an order to a state, with the days it was closed and
-- cancelled as they now stand.
UPDATE purchasing.orders SET
    status       = sqlc.arg(status),
    closed_at    = sqlc.narg(closed_at),
    cancelled_at = sqlc.narg(cancelled_at),
    updated_at   = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetOrderConfirmation :one
-- SetOrderConfirmation records the supplier's confirmation (P19): a fact on
-- the order, no state.
UPDATE purchasing.orders SET
    supplier_reference   = sqlc.narg(supplier_reference),
    confirmed_on         = sqlc.narg(confirmed_on),
    confirmed_at         = sqlc.narg(confirmed_at),
    confirmed_by_user_id = sqlc.narg(confirmed_by_user_id),
    confirmed_by_display = sqlc.narg(confirmed_by_display),
    updated_at           = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: TouchOrder :exec
UPDATE purchasing.orders SET updated_at = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- name: DeleteOrder :exec
DELETE FROM purchasing.orders WHERE id = sqlc.arg(id) AND status = 'draft';

-- name: ListOrders :many
-- ListOrders is a page of orders, newest first (P23), with the total the
-- filters match. Late and unconfirmed are judged at the caller's today and
-- the instant three business days back (P19).
SELECT o.*, count(*) OVER ()::integer AS total,
    (SELECT count(*) FROM purchasing.order_lines l WHERE l.order_id = o.id)::integer AS line_count,
    (SELECT count(*) FROM purchasing.order_lines l WHERE l.order_id = o.id AND l.kind = 'variant' AND l.product_type = 'Goods')::integer AS goods_lines,
    (SELECT count(*) FROM purchasing.order_lines l WHERE l.order_id = o.id AND l.kind = 'variant' AND l.product_type = 'Goods'
        AND (l.received >= l.quantity OR l.closed_at IS NOT NULL))::integer AS received_lines,
    (SELECT count(*) FROM purchasing.order_lines l WHERE l.order_id = o.id AND l.invoiced >= l.quantity)::integer AS invoiced_lines,
    coalesce((SELECT m.status FROM purchasing.mailings m WHERE m.order_id = o.id ORDER BY m.id DESC LIMIT 1), '')::text AS last_mailing_status
FROM purchasing.orders o
WHERE (cardinality(sqlc.arg(statuses)::text[]) = 0 OR o.status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.narg(supplier_id)::bigint IS NULL OR o.supplier_id = sqlc.narg(supplier_id))
  AND (sqlc.narg(site_id)::bigint IS NULL OR o.site_id = sqlc.narg(site_id))
  AND (sqlc.narg(buyer_id)::uuid IS NULL OR o.buyer_id = sqlc.narg(buyer_id))
  AND (NOT sqlc.arg(open_only)::boolean OR o.status IN ('draft', 'sent', 'partially_received'))
  AND (NOT sqlc.arg(late_only)::boolean OR (o.status IN ('sent', 'partially_received') AND EXISTS (
        SELECT 1 FROM purchasing.order_lines l WHERE l.order_id = o.id AND l.closed_at IS NULL
          AND l.delivered_at IS NULL AND l.received < l.quantity
          AND coalesce(l.expected_on, o.expected_on) < sqlc.arg(today)::date)))
  AND (NOT sqlc.arg(unconfirmed_only)::boolean OR (o.status IN ('sent', 'partially_received')
        AND o.supplier_reference IS NULL AND o.confirmed_on IS NULL AND o.sent_at < sqlc.arg(unconfirmed_before)::timestamptz))
  AND (sqlc.arg(q)::text = '' OR o.number::text = sqlc.arg(q)::text
        OR o.supplier_name ILIKE '%' || sqlc.arg(q)::text || '%' OR o.supplier_reference ILIKE '%' || sqlc.arg(q)::text || '%')
ORDER BY o.created_at DESC, o.id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: LateOrderIDs :many
-- LateOrderIDs is which of the orders given are late at today (P23).
SELECT DISTINCT o.id FROM purchasing.orders o JOIN purchasing.order_lines l ON l.order_id = o.id
WHERE o.id = ANY(sqlc.arg(ids)::bigint[]) AND o.status IN ('sent', 'partially_received')
  AND l.closed_at IS NULL AND l.delivered_at IS NULL AND l.received < l.quantity
  AND coalesce(l.expected_on, o.expected_on) < sqlc.arg(today)::date;

-- name: Summary :one
-- Summary is the dashboard's figures (P23, §11).
SELECT
    (SELECT count(*) FROM purchasing.orders WHERE status = 'draft')::integer AS drafts,
    (SELECT count(*) FROM purchasing.orders WHERE status IN ('sent', 'partially_received'))::integer AS open,
    (SELECT count(DISTINCT o.id) FROM purchasing.orders o JOIN purchasing.order_lines l ON l.order_id = o.id
        WHERE o.status IN ('sent', 'partially_received') AND l.closed_at IS NULL AND l.delivered_at IS NULL
          AND l.received < l.quantity AND coalesce(l.expected_on, o.expected_on) < sqlc.arg(today)::date)::integer AS late,
    (SELECT count(*) FROM purchasing.orders WHERE status IN ('sent', 'partially_received')
        AND supplier_reference IS NULL AND confirmed_on IS NULL AND sent_at < sqlc.arg(unconfirmed_before)::timestamptz)::integer AS unconfirmed,
    (SELECT count(*) FROM purchasing.orders WHERE status IN ('sent', 'partially_received')
        AND supplier_reference IS NULL AND confirmed_on IS NULL AND sent_at < sqlc.arg(unconfirmed_before)::timestamptz
        AND buyer_id = sqlc.narg(buyer_id)::uuid)::integer AS mine_unconfirmed,
    (SELECT count(*) FROM purchasing.orders o WHERE o.status IN ('draft', 'sent', 'partially_received')
        AND (SELECT m.status FROM purchasing.mailings m WHERE m.order_id = o.id ORDER BY m.id DESC LIMIT 1) = 'failed')::integer AS failed_mailings;
