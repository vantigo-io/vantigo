-- name: InsertQuote :one
-- InsertQuote writes a new draft quote (O13, O20, O27) with the number the
-- quote series handed out; its lines are written after it.
INSERT INTO orders.quotes (
    number, status, revision, valid_until, template_id,
    customer_id, customer_number, customer_name, customer_type, customer_organisation_number,
    customer_foreign_id, customer_country, invoice_address_line1, invoice_address_line2,
    invoice_postal_code, invoice_city, invoice_country, delivery_address_id, delivery_name,
    delivery_address_line1, delivery_address_line2, delivery_postal_code, delivery_city,
    delivery_country, contact_id, contact_name, contact_email, contact_phone, customer_invoice_email, your_reference,
    salesperson_id, salesperson_display, project_id, currency, prices_include_vat,
    payment_terms_days, discount_visible, language, intro_text, closing_text, internal_note,
    delivery_date, net_total, vat_total, gross_total,
    channel, source_module, source_kind, source_id, client_request_id,
    created_by_user_id, created_by_display, created_at, updated_at
) VALUES (
    sqlc.arg(number), 'draft', 1, sqlc.narg(valid_until), sqlc.narg(template_id),
    sqlc.arg(customer_id), sqlc.narg(customer_number), sqlc.arg(customer_name), sqlc.narg(customer_type),
    sqlc.narg(customer_organisation_number), sqlc.narg(customer_foreign_id), sqlc.narg(customer_country),
    sqlc.narg(invoice_address_line1), sqlc.narg(invoice_address_line2), sqlc.narg(invoice_postal_code),
    sqlc.narg(invoice_city), sqlc.narg(invoice_country), sqlc.narg(delivery_address_id), sqlc.narg(delivery_name),
    sqlc.narg(delivery_address_line1), sqlc.narg(delivery_address_line2), sqlc.narg(delivery_postal_code),
    sqlc.narg(delivery_city), sqlc.narg(delivery_country), sqlc.narg(contact_id), sqlc.narg(contact_name),
    sqlc.narg(contact_email), sqlc.narg(contact_phone), sqlc.narg(customer_invoice_email), sqlc.arg(your_reference),
    sqlc.narg(salesperson_id), sqlc.narg(salesperson_display), sqlc.narg(project_id), sqlc.arg(currency),
    sqlc.arg(prices_include_vat), sqlc.arg(payment_terms_days), sqlc.arg(discount_visible), sqlc.arg(language),
    sqlc.arg(intro_text), sqlc.arg(closing_text), sqlc.arg(internal_note), sqlc.narg(delivery_date),
    sqlc.arg(net_total), sqlc.arg(vat_total), sqlc.arg(gross_total),
    sqlc.arg(channel), sqlc.narg(source_module), sqlc.narg(source_kind), sqlc.narg(source_id),
    sqlc.narg(client_request_id), sqlc.narg(created_by_user_id), sqlc.narg(created_by_display),
    sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: GetQuote :one
SELECT * FROM orders.quotes WHERE id = sqlc.arg(id);

-- name: LockQuote :one
-- LockQuote reads a quote under its row lock: every write of a quote takes
-- it, after the variants' locks (MB rule 21), and an event about the quote is
-- published only while it is held (O3).
SELECT * FROM orders.quotes WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: QuoteByClientRequest :one
-- QuoteByClientRequest is the quote an earlier create with this
-- clientRequestId made (O27). No row is pgx.ErrNoRows.
SELECT * FROM orders.quotes WHERE client_request_id = sqlc.arg(client_request_id);

-- name: UpdateQuoteDraft :one
-- UpdateQuoteDraft replaces a draft quote's header, its refreshed snapshot
-- (O14) and its totals; the caller holds the row and has judged it a draft.
UPDATE orders.quotes SET
    valid_until = sqlc.narg(valid_until), template_id = sqlc.narg(template_id),
    customer_id = sqlc.arg(customer_id), customer_number = sqlc.narg(customer_number),
    customer_name = sqlc.arg(customer_name), customer_type = sqlc.narg(customer_type),
    customer_organisation_number = sqlc.narg(customer_organisation_number),
    customer_foreign_id = sqlc.narg(customer_foreign_id), customer_country = sqlc.narg(customer_country),
    invoice_address_line1 = sqlc.narg(invoice_address_line1), invoice_address_line2 = sqlc.narg(invoice_address_line2),
    invoice_postal_code = sqlc.narg(invoice_postal_code), invoice_city = sqlc.narg(invoice_city),
    invoice_country = sqlc.narg(invoice_country), delivery_address_id = sqlc.narg(delivery_address_id),
    delivery_name = sqlc.narg(delivery_name), delivery_address_line1 = sqlc.narg(delivery_address_line1),
    delivery_address_line2 = sqlc.narg(delivery_address_line2), delivery_postal_code = sqlc.narg(delivery_postal_code),
    delivery_city = sqlc.narg(delivery_city), delivery_country = sqlc.narg(delivery_country),
    contact_id = sqlc.narg(contact_id), contact_name = sqlc.narg(contact_name),
    contact_email = sqlc.narg(contact_email), contact_phone = sqlc.narg(contact_phone),
    customer_invoice_email = sqlc.narg(customer_invoice_email),
    your_reference = sqlc.arg(your_reference), salesperson_id = sqlc.narg(salesperson_id),
    salesperson_display = sqlc.narg(salesperson_display), project_id = sqlc.narg(project_id),
    currency = sqlc.arg(currency), prices_include_vat = sqlc.arg(prices_include_vat),
    payment_terms_days = sqlc.arg(payment_terms_days), discount_visible = sqlc.arg(discount_visible),
    language = sqlc.arg(language), intro_text = sqlc.arg(intro_text), closing_text = sqlc.arg(closing_text),
    internal_note = sqlc.arg(internal_note), delivery_date = sqlc.narg(delivery_date),
    net_total = sqlc.arg(net_total), vat_total = sqlc.arg(vat_total), gross_total = sqlc.arg(gross_total),
    updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetQuoteSent :one
-- SetQuoteSent freezes a draft as sent (O20): its valid-until and the moment.
UPDATE orders.quotes
SET status = 'sent', sent_at = sqlc.arg(now), valid_until = sqlc.arg(valid_until), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND status = 'draft'
RETURNING *;

-- name: ReviseQuoteRow :one
-- ReviseQuoteRow moves a sent quote back to draft at the next revision (O20).
UPDATE orders.quotes
SET status = 'draft', revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: AcceptQuoteRow :one
-- AcceptQuoteRow records the accept and what it made (O20).
UPDATE orders.quotes
SET status = 'accepted', accepted_at = sqlc.arg(now), accepted_into_kind = sqlc.arg(into_kind)::text,
    accepted_into_id = sqlc.arg(into_id)::bigint, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeclineQuoteRow :one
UPDATE orders.quotes
SET status = 'declined', declined_at = sqlc.arg(now), decline_reason = sqlc.narg(reason), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CancelQuoteRow :one
UPDATE orders.quotes
SET status = 'cancelled', cancelled_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteQuoteRow :execrows
DELETE FROM orders.quotes WHERE id = sqlc.arg(id);

-- name: ListQuotes :many
-- ListQuotes is one page of quotes, newest first, filtered (O31a): q matches
-- the number's digits, the customer's name and your reference.
SELECT * FROM orders.quotes
WHERE (sqlc.narg(customer_id)::integer IS NULL OR customer_id = sqlc.narg(customer_id))
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.narg(salesperson_id)::uuid IS NULL OR salesperson_id = sqlc.narg(salesperson_id))
  AND (sqlc.arg(q)::text = '' OR number::text = sqlc.arg(q)::text
       OR customer_name ILIKE '%' || sqlc.arg(q)::text || '%'
       OR your_reference ILIKE '%' || sqlc.arg(q)::text || '%')
ORDER BY id DESC
LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: CountQuotes :one
SELECT count(*)::integer FROM orders.quotes
WHERE (sqlc.narg(customer_id)::integer IS NULL OR customer_id = sqlc.narg(customer_id))
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.narg(salesperson_id)::uuid IS NULL OR salesperson_id = sqlc.narg(salesperson_id))
  AND (sqlc.arg(q)::text = '' OR number::text = sqlc.arg(q)::text
       OR customer_name ILIKE '%' || sqlc.arg(q)::text || '%'
       OR your_reference ILIKE '%' || sqlc.arg(q)::text || '%');
