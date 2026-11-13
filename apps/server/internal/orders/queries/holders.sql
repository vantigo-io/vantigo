-- name: RepointCustomerQuotes :execrows
-- RepointCustomerQuotes moves every quote of a merged-away customer to the
-- surviving one (O29). The snapshot is left as it is: a frozen quote keeps
-- the customer as it was when it was sent. updated_at moves, so an editor
-- holding a quote from before the merge is stale and cannot write the
-- merged-away customer back.
UPDATE orders.quotes SET customer_id = sqlc.arg(into_customer_id), updated_at = now()
WHERE customer_id = sqlc.arg(from_customer_id);

-- name: RepointCustomerOrders :execrows
-- RepointCustomerOrders is the orders'. A draft's updated_at moves as a
-- quote's does; a confirmed or cancelled order's stays, since it is the
-- moment a done order's retention runs from, and its customer is frozen.
UPDATE orders.orders
SET customer_id = sqlc.arg(into_customer_id),
    updated_at = CASE WHEN status = 'draft' THEN now() ELSE updated_at END
WHERE customer_id = sqlc.arg(from_customer_id);

-- name: CustomerQuotes :many
-- CustomerQuotes is every quote of a customer, oldest first, for the export.
SELECT * FROM orders.quotes WHERE customer_id = sqlc.arg(customer_id) ORDER BY id;

-- name: CustomerOrders :many
SELECT * FROM orders.orders WHERE customer_id = sqlc.arg(customer_id) ORDER BY id;

-- name: LinesOfOwners :many
-- LinesOfOwners is the lines of several documents of one kind, by document
-- then position.
SELECT * FROM orders.lines
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = ANY(sqlc.arg(owner_ids)::bigint[])
ORDER BY owner_id, position;

-- name: MailingsOfOwners :many
SELECT * FROM orders.mailings
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = ANY(sqlc.arg(owner_ids)::bigint[])
ORDER BY owner_id, created_at, id;

-- name: LockCustomerQuotes :exec
-- LockCustomerQuotes takes a customer's quotes FOR UPDATE in id order, the
-- erase's first step: a write of one quote holds only that quote's row (after
-- its variants'), so the two never wait on each other in a circle.
SELECT id FROM orders.quotes WHERE customer_id = sqlc.arg(customer_id) ORDER BY id FOR UPDATE;

-- name: LockCustomerOrders :exec
SELECT id FROM orders.orders WHERE customer_id = sqlc.arg(customer_id) ORDER BY id FOR UPDATE;

-- name: AnonymiseCustomerQuotes :execrows
-- AnonymiseCustomerQuotes takes the person out of every quote of theirs at
-- once (O29): the name, the legal ids, the addresses, the contact, the
-- invoice e-mail, your reference and the internal note. The customer number,
-- the countries and the lines stay.
UPDATE orders.quotes SET
    customer_name = '', customer_organisation_number = NULL, customer_foreign_id = NULL,
    invoice_address_line1 = NULL, invoice_address_line2 = NULL, invoice_postal_code = NULL, invoice_city = NULL,
    delivery_address_id = NULL, delivery_name = NULL, delivery_address_line1 = NULL, delivery_address_line2 = NULL,
    delivery_postal_code = NULL, delivery_city = NULL,
    contact_id = NULL, contact_name = NULL, contact_email = NULL, contact_phone = NULL,
    customer_invoice_email = NULL, your_reference = '', internal_note = '', anonymised_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE customer_id = sqlc.arg(customer_id) AND anonymised_at IS NULL;

-- name: AnonymiseCustomerOrders :execrows
-- AnonymiseCustomerOrders takes the person out of a customer's orders that
-- are no record (O29, O54): every order never confirmed — a draft, or one
-- cancelled as a draft — when cutoff is NULL; and, for the retention sweep,
-- every confirmed order whose retention has run: cancelled, or done, before
-- cutoff. Done is ListOrders' rule: confirmed with every product line's
-- cancelled and invoiced covering what was ordered, a kit's child line
-- invoiced as its kit. The column list is the
-- quotes'; updated_at moves, so an editor holding the order before is stale.
UPDATE orders.orders o SET
    customer_name = '', customer_organisation_number = NULL, customer_foreign_id = NULL,
    invoice_address_line1 = NULL, invoice_address_line2 = NULL, invoice_postal_code = NULL, invoice_city = NULL,
    delivery_address_id = NULL, delivery_name = NULL, delivery_address_line1 = NULL, delivery_address_line2 = NULL,
    delivery_postal_code = NULL, delivery_city = NULL,
    contact_id = NULL, contact_name = NULL, contact_email = NULL, contact_phone = NULL,
    customer_invoice_email = NULL, your_reference = '', internal_note = '', anonymised_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE o.customer_id = sqlc.arg(customer_id) AND o.anonymised_at IS NULL
  AND (
      (sqlc.narg(cutoff)::timestamptz IS NULL AND o.confirmed_at IS NULL)
      OR (sqlc.narg(cutoff)::timestamptz IS NOT NULL AND (
          o.confirmed_at IS NULL
          OR (o.status = 'cancelled' AND o.cancelled_at < sqlc.narg(cutoff)::timestamptz)
          OR (o.status = 'confirmed' AND o.updated_at < sqlc.narg(cutoff)::timestamptz
              AND EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                          AND l.kind = 'product' AND l.quantity IS NOT NULL)
              AND NOT EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                          AND l.kind = 'product' AND l.quantity IS NOT NULL
                          AND ((l.parent_line_id IS NULL AND l.qty_cancelled + l.qty_invoiced < l.quantity)
                               OR l.qty_cancelled + greatest(l.qty_delivered, l.qty_invoiced) < l.quantity)))))
  );

-- name: KeptOrderNumbers :many
-- KeptOrderNumbers is the numbers of a customer's orders still holding the
-- person: the confirmed ones whose retention has not run (O54).
SELECT number FROM orders.orders
WHERE customer_id = sqlc.arg(customer_id) AND anonymised_at IS NULL
ORDER BY number;

-- name: BlankCustomerMailings :execrows
-- BlankCustomerMailings blanks the recipient and the copies of every send of
-- a customer's quotes and orders (O29), and fails a queued one, which would
-- otherwise go to an address that is gone.
UPDATE orders.mailings SET
    recipient = '', cc = '',
    status = CASE WHEN status = 'queued' THEN 'failed' ELSE status END,
    last_error = CASE WHEN status = 'queued' THEN 'The customer was anonymised; nothing was sent.' ELSE last_error END,
    next_attempt_at = CASE WHEN status = 'queued' THEN NULL ELSE next_attempt_at END
WHERE (recipient <> '' OR cc <> '' OR status = 'queued')
  AND ((owner_kind = 'quote' AND owner_id IN (SELECT q.id FROM orders.quotes q WHERE q.customer_id = sqlc.arg(customer_id)))
    OR (owner_kind = 'order' AND owner_id IN (SELECT o.id FROM orders.orders o WHERE o.customer_id = sqlc.arg(customer_id)))
    OR (owner_kind = 'shipment' AND owner_id IN (SELECT sh.id FROM orders.shipments sh
                                                 JOIN orders.deliveries dv ON dv.id = sh.delivery_id
                                                 JOIN orders.orders o ON o.id = dv.order_id
                                                 WHERE o.customer_id = sqlc.arg(customer_id))));

-- name: RecordErasure :exec
-- RecordErasure records a customer whose kept orders the retention sweep
-- anonymises later (O29, O54); a repeat keeps the first request.
INSERT INTO orders.erasures (customer_id, requested_at) VALUES (sqlc.arg(customer_id), sqlc.arg(requested_at))
ON CONFLICT (customer_id) DO NOTHING;

-- name: OpenErasures :many
-- OpenErasures is the erasures the retention sweep still works on, oldest
-- first.
SELECT customer_id FROM orders.erasures WHERE completed_at IS NULL
ORDER BY requested_at, customer_id
LIMIT sqlc.arg(batch_size)::integer;

-- name: CompleteErasure :execrows
-- CompleteErasure marks an erasure done once none of the customer's orders
-- and subscriptions holds the person any more and no stored PDF of theirs —
-- a quote's, an order's or a delivery's packing slip — is left.
UPDATE orders.erasures SET completed_at = sqlc.arg(now)
WHERE orders.erasures.customer_id = sqlc.arg(customer_id) AND completed_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM orders.orders o WHERE o.customer_id = orders.erasures.customer_id AND o.anonymised_at IS NULL)
  AND NOT EXISTS (SELECT 1 FROM orders.subscriptions s WHERE s.customer_id = orders.erasures.customer_id AND s.anonymised_at IS NULL)
  AND NOT EXISTS (SELECT 1 FROM orders.documents d
                  WHERE (d.owner_kind = 'quote' AND d.owner_id IN (SELECT q.id FROM orders.quotes q WHERE q.customer_id = orders.erasures.customer_id))
                     OR (d.owner_kind = 'order' AND d.owner_id IN (SELECT o.id FROM orders.orders o WHERE o.customer_id = orders.erasures.customer_id))
                     OR (d.owner_kind = 'delivery' AND d.owner_id IN (SELECT dv.id FROM orders.deliveries dv
                                                                      JOIN orders.orders o ON o.id = dv.order_id
                                                                      WHERE o.customer_id = orders.erasures.customer_id)))
  AND NOT EXISTS (SELECT 1 FROM orders.shipment_documents sd
                  JOIN orders.shipments sh ON sh.id = sd.shipment_id
                  JOIN orders.deliveries dv ON dv.id = sh.delivery_id
                  JOIN orders.orders o ON o.id = dv.order_id
                  WHERE o.customer_id = orders.erasures.customer_id);

-- name: CustomerHasDocuments :one
-- CustomerHasDocuments reports whether any quote or order of a customer has a
-- stored PDF, a packing slip, a label or a waybill: the erase records the
-- customer then, so the retention sweep deletes the PDFs of what was
-- anonymised.
SELECT (EXISTS (SELECT 1 FROM orders.documents d
               WHERE (d.owner_kind = 'quote' AND d.owner_id IN (SELECT q.id FROM orders.quotes q WHERE q.customer_id = sqlc.arg(customer_id)))
                  OR (d.owner_kind = 'order' AND d.owner_id IN (SELECT o.id FROM orders.orders o WHERE o.customer_id = sqlc.arg(customer_id)))
                  OR (d.owner_kind = 'delivery' AND d.owner_id IN (SELECT dv.id FROM orders.deliveries dv
                                                                   JOIN orders.orders o ON o.id = dv.order_id
                                                                   WHERE o.customer_id = sqlc.arg(customer_id))))
    OR EXISTS (SELECT 1 FROM orders.shipment_documents sd
               JOIN orders.shipments sh ON sh.id = sd.shipment_id
               JOIN orders.deliveries dv ON dv.id = sh.delivery_id
               JOIN orders.orders o ON o.id = dv.order_id
               WHERE o.customer_id = sqlc.arg(customer_id)))::boolean;

-- name: ErasedDocuments :many
-- ErasedDocuments is the stored PDFs of a customer's anonymised quotes and
-- orders (O29, O54): a quote's at once after the erase, a confirmed order's
-- once its retention has run and it was anonymised, and so the packing slips
-- of its deliveries (O47). The PDF prints the person, so it goes with them.
SELECT d.* FROM orders.documents d
WHERE (d.owner_kind = 'quote' AND d.owner_id IN (SELECT q.id FROM orders.quotes q
                                                 WHERE q.customer_id = sqlc.arg(customer_id) AND q.anonymised_at IS NOT NULL))
   OR (d.owner_kind = 'order' AND d.owner_id IN (SELECT o.id FROM orders.orders o
                                                 WHERE o.customer_id = sqlc.arg(customer_id) AND o.anonymised_at IS NOT NULL))
   OR (d.owner_kind = 'delivery' AND d.owner_id IN (SELECT dv.id FROM orders.deliveries dv
                                                    JOIN orders.orders o ON o.id = dv.order_id
                                                    WHERE o.customer_id = sqlc.arg(customer_id) AND o.anonymised_at IS NOT NULL))
ORDER BY d.id;

-- name: DetachDocumentMailings :exec
-- DetachDocumentMailings unlinks the sends of a stored PDF about to be
-- deleted; the send rows stay, blanked, as the record that a send was made.
UPDATE orders.mailings SET document_id = NULL WHERE document_id = sqlc.arg(document_id);

-- name: DeleteDocumentRow :execrows
DELETE FROM orders.documents WHERE id = sqlc.arg(id);

-- name: OpenDocumentsOfVariant :one
-- OpenDocumentsOfVariant is how many open documents name a variant (O30): a
-- draft or sent quote, an order not cancelled with a line of it that is
-- not done — delivered or invoiced short of what is ordered less cancelled,
-- a kit's child line invoiced as its kit (manufacturing phase 2 M145) —
-- and a subscription still billing on today — draft, active or paused, or
-- ended on or after today — with a line of it in force on or after today
-- (O42), and a draft delivery naming it (O47).
-- A template is no document: a variant gone from it is skipped when it is
-- applied (O23).
SELECT (
    (SELECT count(DISTINCT q.id) FROM orders.quotes q
     JOIN orders.lines l ON l.owner_kind = 'quote' AND l.owner_id = q.id
     WHERE l.variant_id = sqlc.arg(variant_id) AND q.status IN ('draft', 'sent'))
  + (SELECT count(DISTINCT o.id) FROM orders.orders o
     JOIN orders.lines l ON l.owner_kind = 'order' AND l.owner_id = o.id
     WHERE l.variant_id = sqlc.arg(variant_id) AND o.status <> 'cancelled'
       AND (l.qty_delivered + l.qty_cancelled < l.quantity
            OR (l.parent_line_id IS NULL AND l.qty_invoiced + l.qty_cancelled < l.quantity)))
  + (SELECT count(DISTINCT s.id) FROM orders.subscriptions s
     JOIN orders.lines l ON l.owner_kind = 'subscription' AND l.owner_id = s.id
     WHERE l.variant_id = sqlc.arg(variant_id)
       AND (s.status IN ('draft', 'active', 'paused') OR (s.status = 'ended' AND s.end_date >= sqlc.arg(today)::date))
       AND (l.valid_to IS NULL OR l.valid_to >= sqlc.arg(today)::date))
  + (SELECT count(DISTINCT d.id) FROM orders.deliveries d
     JOIN orders.delivery_lines dl ON dl.delivery_id = d.id
     WHERE dl.variant_id = sqlc.arg(variant_id) AND d.status = 'draft')
)::integer AS documents;

-- name: ExpireQuotes :execrows
-- ExpireQuotes moves every sent quote whose valid-until has passed to
-- expired (O20): one statement, so two replicas running it at once do the
-- same thing once.
UPDATE orders.quotes SET status = 'expired', updated_at = sqlc.arg(now)
WHERE status = 'sent' AND valid_until < sqlc.arg(today)::date;

-- name: AnonymiseCustomerShipments :execrows
-- AnonymiseCustomerShipments takes the person out of the shipments of a
-- customer's anonymised orders (O29, O50): the recipient as it was booked,
-- and the links its label, its waybill and its tracking page are fetched
-- from — they print or show the recipient. The consignment number, the
-- packages and the tracking events stay.
UPDATE orders.shipments sh SET
    recipient_name = '', recipient_line1 = '', recipient_line2 = '', recipient_postal_code = '', recipient_city = '',
    recipient_contact = '', recipient_phone = '', recipient_email = '', pickup_point_id = NULL,
    document_refs = '[]'::jsonb, tracking_url = NULL
FROM orders.deliveries dv, orders.orders o
WHERE dv.id = sh.delivery_id AND o.id = dv.order_id AND o.customer_id = sqlc.arg(customer_id) AND o.anonymised_at IS NOT NULL
  AND (sh.recipient_name <> '' OR sh.recipient_line1 <> '' OR sh.recipient_phone <> '' OR sh.recipient_email <> ''
       OR sh.recipient_contact <> '' OR sh.document_refs <> '[]'::jsonb OR sh.tracking_url IS NOT NULL);

-- name: ErasedShipmentDocuments :many
-- ErasedShipmentDocuments is the stored labels and waybills of the shipments
-- of a customer's anonymised orders: they print the recipient, so they go
-- with them.
SELECT sd.* FROM orders.shipment_documents sd
JOIN orders.shipments sh ON sh.id = sd.shipment_id
JOIN orders.deliveries dv ON dv.id = sh.delivery_id
JOIN orders.orders o ON o.id = dv.order_id
WHERE o.customer_id = sqlc.arg(customer_id) AND o.anonymised_at IS NOT NULL
ORDER BY sd.id;

-- name: DeleteShipmentDocumentRow :execrows
DELETE FROM orders.shipment_documents WHERE id = sqlc.arg(id);
