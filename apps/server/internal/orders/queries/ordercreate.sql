-- name: InsertOrder :one
-- InsertOrder writes a new sales order (O13, O27, O28): the one insert of
-- orders.orders, made by createOrder (ordercreate.go) and nothing else. The
-- order starts as a draft at version 1; its lines are written after it.
INSERT INTO orders.orders (
    number, status,
    customer_id, customer_number, customer_name, customer_type, customer_organisation_number,
    customer_foreign_id, customer_country, invoice_address_line1, invoice_address_line2,
    invoice_postal_code, invoice_city, invoice_country, delivery_address_id, delivery_name,
    delivery_address_line1, delivery_address_line2, delivery_postal_code, delivery_city,
    delivery_country, contact_id, contact_name, contact_email, contact_phone, your_reference,
    salesperson_id, salesperson_display, project_id, currency, prices_include_vat,
    payment_terms_days, discount_visible, language, intro_text, closing_text, internal_note,
    delivery_date, net_total, vat_total, gross_total,
    channel, source_module, source_kind, source_id, external_id, external_number, client_request_id,
    payment_reference, prepaid_amount, created_by_user_id, created_by_display, created_at, updated_at
) VALUES (
    sqlc.arg(number), 'draft',
    sqlc.arg(customer_id), sqlc.narg(customer_number), sqlc.arg(customer_name), sqlc.narg(customer_type),
    sqlc.narg(customer_organisation_number), sqlc.narg(customer_foreign_id), sqlc.narg(customer_country),
    sqlc.narg(invoice_address_line1), sqlc.narg(invoice_address_line2), sqlc.narg(invoice_postal_code),
    sqlc.narg(invoice_city), sqlc.narg(invoice_country), sqlc.narg(delivery_address_id), sqlc.narg(delivery_name),
    sqlc.narg(delivery_address_line1), sqlc.narg(delivery_address_line2), sqlc.narg(delivery_postal_code),
    sqlc.narg(delivery_city), sqlc.narg(delivery_country), sqlc.narg(contact_id), sqlc.narg(contact_name),
    sqlc.narg(contact_email), sqlc.narg(contact_phone), sqlc.arg(your_reference),
    sqlc.narg(salesperson_id), sqlc.narg(salesperson_display), sqlc.narg(project_id), sqlc.arg(currency),
    sqlc.arg(prices_include_vat), sqlc.arg(payment_terms_days), sqlc.arg(discount_visible), sqlc.arg(language),
    sqlc.arg(intro_text), sqlc.arg(closing_text), sqlc.arg(internal_note), sqlc.narg(delivery_date),
    sqlc.arg(net_total), sqlc.arg(vat_total), sqlc.arg(gross_total),
    sqlc.arg(channel), sqlc.narg(source_module), sqlc.narg(source_kind), sqlc.narg(source_id),
    sqlc.narg(external_id), sqlc.narg(external_number), sqlc.narg(client_request_id),
    sqlc.narg(payment_reference), sqlc.narg(prepaid_amount),
    sqlc.narg(created_by_user_id), sqlc.narg(created_by_display), sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: OrderByID :one
-- OrderByID reads one order, for its PDF and its mail (pdfdocs.go,
-- mail_worker.go). No row is pgx.ErrNoRows.
SELECT * FROM orders.orders WHERE id = sqlc.arg(id);
