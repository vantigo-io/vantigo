-- name: RepointCustomerQuotes :execrows
-- RepointCustomerQuotes moves every quote of a merged-away customer to the
-- surviving one (O29). The snapshot is left as it is: a frozen quote keeps
-- the customer as it was when it was sent.
UPDATE orders.quotes SET customer_id = sqlc.arg(into_customer_id) WHERE customer_id = sqlc.arg(from_customer_id);

-- name: RepointCustomerOrders :execrows
UPDATE orders.orders SET customer_id = sqlc.arg(into_customer_id) WHERE customer_id = sqlc.arg(from_customer_id);

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
    customer_invoice_email = NULL, your_reference = '', internal_note = '', anonymised_at = sqlc.arg(now)
WHERE customer_id = sqlc.arg(customer_id) AND anonymised_at IS NULL;

-- name: AnonymiseCustomerOrders :execrows
-- AnonymiseCustomerOrders takes the person out of a customer's orders that
-- are no record (O29, O54): every order never confirmed — a draft, or one
-- cancelled as a draft — when cutoff is NULL; and, for the retention sweep,
-- every confirmed order whose retention has run: cancelled, or done
-- (confirmed, delivered and invoiced), before cutoff. The column list is the
-- quotes'.
UPDATE orders.orders SET
    customer_name = '', customer_organisation_number = NULL, customer_foreign_id = NULL,
    invoice_address_line1 = NULL, invoice_address_line2 = NULL, invoice_postal_code = NULL, invoice_city = NULL,
    delivery_address_id = NULL, delivery_name = NULL, delivery_address_line1 = NULL, delivery_address_line2 = NULL,
    delivery_postal_code = NULL, delivery_city = NULL,
    contact_id = NULL, contact_name = NULL, contact_email = NULL, contact_phone = NULL,
    customer_invoice_email = NULL, your_reference = '', internal_note = '', anonymised_at = sqlc.arg(now)
WHERE customer_id = sqlc.arg(customer_id) AND anonymised_at IS NULL
  AND (
      (sqlc.narg(cutoff)::timestamptz IS NULL AND confirmed_at IS NULL)
      OR (sqlc.narg(cutoff)::timestamptz IS NOT NULL AND (
          confirmed_at IS NULL
          OR (status = 'cancelled' AND cancelled_at < sqlc.narg(cutoff)::timestamptz)
          OR (status = 'confirmed' AND delivery_status = 'delivered' AND invoice_status = 'invoiced'
              AND updated_at < sqlc.narg(cutoff)::timestamptz)))
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
    OR (owner_kind = 'order' AND owner_id IN (SELECT o.id FROM orders.orders o WHERE o.customer_id = sqlc.arg(customer_id))));

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
-- holds the person any more.
UPDATE orders.erasures SET completed_at = sqlc.arg(now)
WHERE orders.erasures.customer_id = sqlc.arg(customer_id) AND completed_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM orders.orders o WHERE o.customer_id = orders.erasures.customer_id AND o.anonymised_at IS NULL);

-- name: OpenDocumentsOfVariant :one
-- OpenDocumentsOfVariant is how many open documents name a variant (O30): a
-- draft or sent quote, and an order not cancelled with a line of it that is
-- not done — delivered or invoiced short of what is ordered less cancelled.
-- A template is no document: a variant gone from it is skipped when it is
-- applied (O23).
SELECT (
    (SELECT count(DISTINCT q.id) FROM orders.quotes q
     JOIN orders.lines l ON l.owner_kind = 'quote' AND l.owner_id = q.id
     WHERE l.variant_id = sqlc.arg(variant_id) AND q.status IN ('draft', 'sent'))
  + (SELECT count(DISTINCT o.id) FROM orders.orders o
     JOIN orders.lines l ON l.owner_kind = 'order' AND l.owner_id = o.id
     WHERE l.variant_id = sqlc.arg(variant_id) AND o.status <> 'cancelled'
       AND (l.qty_delivered + l.qty_cancelled < l.quantity OR l.qty_invoiced + l.qty_cancelled < l.quantity))
)::integer AS documents;

-- name: ExpireQuotes :execrows
-- ExpireQuotes moves every sent quote whose valid-until has passed to
-- expired (O20): one statement, so two replicas running it at once do the
-- same thing once.
UPDATE orders.quotes SET status = 'expired', updated_at = sqlc.arg(now)
WHERE status = 'sent' AND valid_until < sqlc.arg(today)::date;
