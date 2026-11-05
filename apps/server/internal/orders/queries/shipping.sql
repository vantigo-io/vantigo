-- Shipping (orders design O49–O53, delivery D): carrier accounts, the default
-- account per site, shipments of posted deliveries with their packages,
-- stored documents and tracking events, and a shipping line's delivery.

-- name: ListCarrierAccounts :many
-- ListCarrierAccounts is every carrier account, the active ones first, by
-- name.
SELECT * FROM orders.carrier_accounts ORDER BY status, lower(name), id;

-- name: GetCarrierAccount :one
SELECT * FROM orders.carrier_accounts WHERE id = sqlc.arg(id);

-- name: LockCarrierAccount :one
SELECT * FROM orders.carrier_accounts WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: InsertCarrierAccount :one
INSERT INTO orders.carrier_accounts (
    carrier, name, customer_numbers, credentials, test, sender_name, sender_address_line1, sender_address_line2,
    sender_postal_code, sender_city, sender_country, sender_phone, sender_email, default_service, created_at, updated_at
) VALUES (
    sqlc.arg(carrier), sqlc.arg(name), sqlc.arg(customer_numbers)::text[], sqlc.arg(credentials), sqlc.arg(test),
    sqlc.narg(sender_name), sqlc.narg(sender_address_line1), sqlc.narg(sender_address_line2), sqlc.narg(sender_postal_code),
    sqlc.narg(sender_city), sqlc.narg(sender_country), sqlc.narg(sender_phone), sqlc.narg(sender_email),
    sqlc.narg(default_service), sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: UpdateCarrierAccount :one
-- UpdateCarrierAccount writes an account under its lock and bumps its
-- version; the credentials are the sealed ones the caller kept or replaced.
UPDATE orders.carrier_accounts SET
    name = sqlc.arg(name), customer_numbers = sqlc.arg(customer_numbers)::text[], credentials = sqlc.arg(credentials),
    test = sqlc.arg(test), sender_name = sqlc.narg(sender_name), sender_address_line1 = sqlc.narg(sender_address_line1),
    sender_address_line2 = sqlc.narg(sender_address_line2), sender_postal_code = sqlc.narg(sender_postal_code),
    sender_city = sqlc.narg(sender_city), sender_country = sqlc.narg(sender_country), sender_phone = sqlc.narg(sender_phone),
    sender_email = sqlc.narg(sender_email), default_service = sqlc.narg(default_service), status = sqlc.arg(status),
    verified_at = CASE WHEN sqlc.arg(keys_changed)::boolean THEN NULL ELSE verified_at END,
    version = version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetCarrierAccountVerified :exec
-- SetCarrierAccountVerified records a Verify that passed, against the
-- account's version the keys were read at.
UPDATE orders.carrier_accounts SET verified_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version);

-- name: ListSiteCarrierDefaults :many
SELECT * FROM orders.site_carrier_defaults ORDER BY site_id;

-- name: SiteCarrierDefault :one
SELECT account_id FROM orders.site_carrier_defaults WHERE site_id = sqlc.arg(site_id);

-- name: DeleteSiteCarrierDefaults :exec
DELETE FROM orders.site_carrier_defaults;

-- name: InsertSiteCarrierDefault :exec
INSERT INTO orders.site_carrier_defaults (site_id, account_id, updated_at)
VALUES (sqlc.arg(site_id), sqlc.arg(account_id), sqlc.arg(now));

-- name: InsertShipment :one
INSERT INTO orders.shipments (
    delivery_id, carrier_account_id, service, vas, idempotency_key, recipient_name, recipient_line1, recipient_line2,
    recipient_postal_code, recipient_city, recipient_country, recipient_contact, recipient_phone, recipient_email,
    created_at, updated_at
) VALUES (
    sqlc.arg(delivery_id), sqlc.arg(carrier_account_id), sqlc.arg(service), sqlc.arg(vas)::text[], sqlc.arg(idempotency_key),
    sqlc.arg(recipient_name), sqlc.arg(recipient_line1), sqlc.arg(recipient_line2), sqlc.arg(recipient_postal_code),
    sqlc.arg(recipient_city), sqlc.arg(recipient_country), sqlc.arg(recipient_contact), sqlc.arg(recipient_phone),
    sqlc.arg(recipient_email), sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: GetShipment :one
SELECT * FROM orders.shipments WHERE id = sqlc.arg(id);

-- name: LockShipment :one
SELECT * FROM orders.shipments WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ShipmentsOfDelivery :many
SELECT * FROM orders.shipments WHERE delivery_id = sqlc.arg(delivery_id) ORDER BY id;

-- name: ShipmentsOfOrder :many
-- ShipmentsOfOrder is every shipment of an order's deliveries, oldest first,
-- with its delivery's number.
SELECT sqlc.embed(sh), dv.number AS delivery_number
FROM orders.shipments sh
JOIN orders.deliveries dv ON dv.id = sh.delivery_id
WHERE dv.order_id = sqlc.arg(order_id)
ORDER BY sh.id;

-- name: UpdateShipmentDraft :one
-- UpdateShipmentDraft saves a draft's account, service, value-added
-- services, pickup point and recipient contact, under its lock.
UPDATE orders.shipments SET
    carrier_account_id = sqlc.arg(carrier_account_id), service = sqlc.arg(service), vas = sqlc.arg(vas)::text[],
    pickup_point_id = sqlc.narg(pickup_point_id), recipient_contact = sqlc.arg(recipient_contact),
    recipient_phone = sqlc.arg(recipient_phone), recipient_email = sqlc.arg(recipient_email),
    version = version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND status = 'draft'
RETURNING *;

-- name: MarkShipmentBookAttempt :one
-- MarkShipmentBookAttempt records, before the carrier is called, that a
-- booking under the shipment's key is being made: a later attempt knows the
-- outcome of this one may be unknown and asks the carrier first.
UPDATE orders.shipments SET book_attempted_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND status = 'draft'
RETURNING *;

-- name: BookShipmentRow :one
-- BookShipmentRow records the carrier's booking of a draft.
UPDATE orders.shipments SET
    status = 'booked', consignment_number = sqlc.arg(consignment_number), tracking_url = sqlc.narg(tracking_url),
    document_refs = sqlc.arg(document_refs),
    booked_service = sqlc.arg(booked_service), booked_cost = sqlc.narg(booked_cost),
    booked_cost_currency = sqlc.narg(booked_cost_currency), booked_cost_estimate = sqlc.arg(booked_cost_estimate),
    planned_window_start = sqlc.narg(planned_window_start), planned_window_end = sqlc.narg(planned_window_end),
    booked_at = sqlc.arg(now), booked_by_user_id = sqlc.narg(by_user_id), booked_by_display = sqlc.narg(by_display),
    last_tracked_at = sqlc.arg(now), version = version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND status = 'draft'
RETURNING *;

-- name: CancelShipmentRow :one
-- CancelShipmentRow cancels a shipment: a draft outright, a booked one the
-- carrier cancelled, or one voided here when the carrier has no cancel.
UPDATE orders.shipments SET
    status = 'cancelled', cancelled_at = sqlc.arg(now), voided = sqlc.arg(voided), exception = false,
    version = version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND status IN ('draft', 'booked')
RETURNING *;

-- name: SetPackageNumber :exec
UPDATE orders.shipment_packages SET package_number = sqlc.arg(package_number)
WHERE shipment_id = sqlc.arg(shipment_id) AND position = sqlc.arg(position);

-- name: PackagesOf :many
SELECT * FROM orders.shipment_packages WHERE shipment_id = ANY(sqlc.arg(shipment_ids)::bigint[]) ORDER BY shipment_id, position;

-- name: DeleteShipmentPackages :exec
DELETE FROM orders.shipment_packages WHERE shipment_id = sqlc.arg(shipment_id);

-- name: InsertShipmentPackage :one
INSERT INTO orders.shipment_packages (shipment_id, position, weight_g, length_mm, width_mm, height_mm, content)
VALUES (sqlc.arg(shipment_id), sqlc.arg(position), sqlc.arg(weight_g), sqlc.arg(length_mm), sqlc.arg(width_mm),
        sqlc.arg(height_mm), sqlc.arg(content))
RETURNING *;

-- name: InsertShipmentDocument :execrows
-- InsertShipmentDocument records a stored label or waybill once; a second
-- store of the same kind is a no-op.
INSERT INTO orders.shipment_documents (shipment_id, kind, storage_key, format, created_at)
VALUES (sqlc.arg(shipment_id), sqlc.arg(kind), sqlc.arg(storage_key), sqlc.arg(format), sqlc.arg(now))
ON CONFLICT (shipment_id, kind) DO NOTHING;

-- name: DocumentsOfShipments :many
SELECT * FROM orders.shipment_documents WHERE shipment_id = ANY(sqlc.arg(shipment_ids)::bigint[]) ORDER BY shipment_id, kind;

-- name: InsertShipmentEvent :execrows
-- InsertShipmentEvent records a tracking event once, however often it is
-- polled.
INSERT INTO orders.shipment_events (shipment_id, code, text, place, occurred_at, folded_state)
VALUES (sqlc.arg(shipment_id), sqlc.arg(code), sqlc.arg(text), sqlc.arg(place), sqlc.arg(occurred_at), sqlc.arg(folded_state))
ON CONFLICT (shipment_id, occurred_at, code, md5(text)) DO NOTHING;

-- name: EventsOfShipments :many
SELECT * FROM orders.shipment_events WHERE shipment_id = ANY(sqlc.arg(shipment_ids)::bigint[])
ORDER BY shipment_id, occurred_at DESC, id DESC;

-- name: ClaimTrackableShipments :many
-- ClaimTrackableShipments takes up to @batch_size booked or in-transit
-- shipments booked since @since and not tracked since @due, for the
-- orders.shipment_tracking worker: one conditional UPDATE over a SKIP LOCKED
-- pick, which moves last_tracked_at to @now — the lease — so two workers never
-- poll one shipment, and a worker that dies leaves it due again at the next
-- interval. By account, so a cycle asks one carrier account per batch.
UPDATE orders.shipments SET last_tracked_at = sqlc.arg(now)
WHERE id IN (
    SELECT s.id FROM orders.shipments s
    WHERE s.status IN ('booked', 'in_transit') AND s.booked_at >= sqlc.arg(since)
      AND (s.last_tracked_at IS NULL OR s.last_tracked_at <= sqlc.arg(due))
    ORDER BY s.carrier_account_id, s.id
    LIMIT sqlc.arg(batch_size)::integer
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: SetShipmentTracked :one
-- SetShipmentTracked moves a booked or in-transit shipment to the state
-- tracking folded, and sets or clears its exception flag.
UPDATE orders.shipments SET status = sqlc.arg(status)::varchar, exception = sqlc.arg(exception)::boolean, updated_at = sqlc.arg(now),
    version = CASE WHEN status <> sqlc.arg(status)::varchar OR exception <> sqlc.arg(exception)::boolean THEN version + 1 ELSE version END
WHERE id = sqlc.arg(id) AND status IN ('booked', 'in_transit')
RETURNING *;

-- name: BookedShipmentsOfOrder :one
-- BookedShipmentsOfOrder counts an order's shipments booked or past.
SELECT count(*)::integer FROM orders.shipments sh
JOIN orders.deliveries dv ON dv.id = sh.delivery_id
WHERE dv.order_id = sqlc.arg(order_id) AND sh.status IN ('booked', 'in_transit', 'delivered');

-- name: QueueShippingNotice :execrows
-- QueueShippingNotice queues a booked shipment's shipping notice (O52) on the
-- booking's transaction, once per shipment: its trigger is the shipment's
-- idempotency key, so a repeat is a no-op. The orders.mail worker sends it.
INSERT INTO orders.mailings (
    kind, owner_kind, owner_id, version, recipient, cc, subject, status, trigger_event_id, attempts, next_attempt_at, created_at
) VALUES (
    'shipping_notice', 'shipment', sqlc.arg(shipment_id), 1, sqlc.arg(recipient), '', '', 'queued', sqlc.arg(trigger_event_id), 0,
    sqlc.arg(created_at), sqlc.arg(created_at)
)
ON CONFLICT (owner_kind, owner_id, version, trigger_event_id) WHERE trigger_event_id IS NOT NULL DO NOTHING;
