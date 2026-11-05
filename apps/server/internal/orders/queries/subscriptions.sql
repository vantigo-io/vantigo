-- name: InsertSubscription :one
-- InsertSubscription writes a new draft subscription (O42, O27): by hand, or
-- by accepting a quote as one (O20). Its lines are written after it.
INSERT INTO orders.subscriptions (
    number, status, billing_interval, billing, start_date, end_date, notice_months, invoice_day, final_period,
    customer_id, customer_number, customer_name, customer_type, customer_organisation_number,
    customer_foreign_id, customer_country, invoice_address_line1, invoice_address_line2,
    invoice_postal_code, invoice_city, invoice_country, delivery_address_id, delivery_name,
    delivery_address_line1, delivery_address_line2, delivery_postal_code, delivery_city,
    delivery_country, contact_id, contact_name, contact_email, contact_phone, customer_invoice_email, your_reference,
    salesperson_id, salesperson_display, project_id, currency, prices_include_vat,
    payment_terms_days, discount_visible, language, intro_text, closing_text, internal_note,
    net_total, vat_total, gross_total,
    channel, source_module, source_kind, source_id, external_id, external_number, client_request_id,
    created_by_user_id, created_by_display, created_at, updated_at
) VALUES (
    sqlc.arg(number), 'draft', sqlc.arg(billing_interval), sqlc.arg(billing), sqlc.arg(start_date),
    sqlc.narg(end_date), sqlc.arg(notice_months), sqlc.narg(invoice_day), sqlc.arg(final_period),
    sqlc.arg(customer_id), sqlc.narg(customer_number), sqlc.arg(customer_name), sqlc.narg(customer_type),
    sqlc.narg(customer_organisation_number), sqlc.narg(customer_foreign_id), sqlc.narg(customer_country),
    sqlc.narg(invoice_address_line1), sqlc.narg(invoice_address_line2), sqlc.narg(invoice_postal_code),
    sqlc.narg(invoice_city), sqlc.narg(invoice_country), sqlc.narg(delivery_address_id), sqlc.narg(delivery_name),
    sqlc.narg(delivery_address_line1), sqlc.narg(delivery_address_line2), sqlc.narg(delivery_postal_code),
    sqlc.narg(delivery_city), sqlc.narg(delivery_country), sqlc.narg(contact_id), sqlc.narg(contact_name),
    sqlc.narg(contact_email), sqlc.narg(contact_phone), sqlc.narg(customer_invoice_email), sqlc.arg(your_reference),
    sqlc.narg(salesperson_id), sqlc.narg(salesperson_display), sqlc.narg(project_id), sqlc.arg(currency),
    sqlc.arg(prices_include_vat), sqlc.arg(payment_terms_days), sqlc.arg(discount_visible), sqlc.arg(language),
    sqlc.arg(intro_text), sqlc.arg(closing_text), sqlc.arg(internal_note),
    sqlc.arg(net_total), sqlc.arg(vat_total), sqlc.arg(gross_total),
    sqlc.arg(channel), sqlc.narg(source_module), sqlc.narg(source_kind), sqlc.narg(source_id),
    sqlc.narg(external_id), sqlc.narg(external_number), sqlc.narg(client_request_id),
    sqlc.narg(created_by_user_id), sqlc.narg(created_by_display), sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: SubscriptionByID :one
-- SubscriptionByID reads one subscription. No row is pgx.ErrNoRows.
SELECT * FROM orders.subscriptions WHERE id = sqlc.arg(id);

-- name: LockSubscription :one
-- LockSubscription reads a subscription under its row lock: every write of a
-- subscription takes it, after the variants' locks (MB rule 21), and an
-- event about it is published only while it is held (O3).
SELECT * FROM orders.subscriptions WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: SubscriptionByClientRequest :one
-- SubscriptionByClientRequest is the subscription an earlier create with
-- this clientRequestId made (O27). No row is pgx.ErrNoRows.
SELECT * FROM orders.subscriptions WHERE client_request_id = sqlc.arg(client_request_id);

-- name: UpdateSubscriptionHeader :one
-- UpdateSubscriptionHeader replaces a subscription's header, plan and totals
-- at version: a draft's save (the snapshot refreshed, O14) and an active
-- subscription's change (the plan's fixed parts left as they are by the
-- caller, the version bumped). The caller holds the row.
UPDATE orders.subscriptions SET
    billing_interval = sqlc.arg(billing_interval), billing = sqlc.arg(billing), start_date = sqlc.arg(start_date),
    end_date = sqlc.narg(end_date), notice_months = sqlc.arg(notice_months), invoice_day = sqlc.narg(invoice_day),
    final_period = sqlc.arg(final_period), customer_id = sqlc.arg(customer_id), customer_number = sqlc.narg(customer_number),
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
    internal_note = sqlc.arg(internal_note),
    net_total = sqlc.arg(net_total), vat_total = sqlc.arg(vat_total), gross_total = sqlc.arg(gross_total),
    version = sqlc.arg(version), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetSubscriptionState :one
-- SetSubscriptionState writes a subscription's lifecycle (O42, O44): its
-- status, the period a pause stops billing from, its end and how its final
-- period is billed, who activated it and when, when it ended or was
-- cancelled, and its version. The caller holds the row and passes every
-- column as it is to stay.
UPDATE orders.subscriptions SET
    status = sqlc.arg(status), paused_from = sqlc.narg(paused_from), end_date = sqlc.narg(end_date),
    final_period = sqlc.arg(final_period),
    activated_at = sqlc.narg(activated_at), activated_by_user_id = sqlc.narg(activated_by_user_id),
    activated_by_display = sqlc.narg(activated_by_display), ended_at = sqlc.narg(ended_at),
    cancelled_at = sqlc.narg(cancelled_at), version = sqlc.arg(version), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetSubscriptionBilledThrough :one
-- SetSubscriptionBilledThrough stores a subscription's derived billed
-- through (O43) at the moment of the write that moved it — the invoices
-- holder's issue or credit — its version left.
UPDATE orders.subscriptions SET billed_through = sqlc.narg(billed_through), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListSubscriptions :many
-- ListSubscriptions is one page of subscriptions, newest first, filtered: q
-- matches the number's digits, the customer's name and your reference.
SELECT * FROM orders.subscriptions s
WHERE (sqlc.narg(customer_id)::integer IS NULL OR s.customer_id = sqlc.narg(customer_id))
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR s.status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.arg(q)::text = '' OR s.number::text = sqlc.arg(q)::text
       OR s.customer_name ILIKE '%' || sqlc.arg(q)::text || '%'
       OR s.your_reference ILIKE '%' || sqlc.arg(q)::text || '%')
ORDER BY s.id DESC
LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: CountSubscriptions :one
SELECT count(*)::integer FROM orders.subscriptions s
WHERE (sqlc.narg(customer_id)::integer IS NULL OR s.customer_id = sqlc.narg(customer_id))
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR s.status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.arg(q)::text = '' OR s.number::text = sqlc.arg(q)::text
       OR s.customer_name ILIKE '%' || sqlc.arg(q)::text || '%'
       OR s.your_reference ILIKE '%' || sqlc.arg(q)::text || '%');

-- name: InsertSubscriptionChange :one
-- InsertSubscriptionChange writes one entry of a subscription's change log
-- (O44), in the orders' change log table.
INSERT INTO orders.order_changes (owner_kind, owner_id, version, actor_kind, actor_user_id, actor_display, at, actions)
VALUES ('subscription', sqlc.arg(owner_id), sqlc.arg(version), sqlc.arg(actor_kind), sqlc.narg(actor_user_id),
        sqlc.narg(actor_display), sqlc.arg(at), sqlc.arg(actions))
RETURNING *;

-- name: SubscriptionChangesOf :many
-- SubscriptionChangesOf is a subscription's change log, oldest first.
SELECT * FROM orders.order_changes
WHERE owner_kind = 'subscription' AND owner_id = sqlc.arg(owner_id)
ORDER BY at, id;

-- name: SubscriptionSkips :many
-- SubscriptionSkips is the period starts a pause skipped (O44), in order.
SELECT period_start FROM orders.subscription_skips
WHERE subscription_id = sqlc.arg(subscription_id)
ORDER BY period_start;

-- name: SkipsOfSubscriptions :many
-- SkipsOfSubscriptions is the skipped periods of several subscriptions.
SELECT subscription_id, period_start FROM orders.subscription_skips
WHERE subscription_id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY subscription_id, period_start;

-- name: InsertSubscriptionSkips :exec
-- InsertSubscriptionSkips records period starts a resume skips (O44); one
-- recorded already is left.
INSERT INTO orders.subscription_skips (subscription_id, period_start)
SELECT sqlc.arg(subscription_id), unnest(sqlc.arg(period_starts)::date[])
ON CONFLICT DO NOTHING;

-- name: SubscriptionAllocations :many
-- SubscriptionAllocations is every allocation of a subscription, by period
-- then id: its periods' states (O43), the change rules (O44) and billed
-- through.
SELECT * FROM orders.invoice_allocations
WHERE subscription_id = sqlc.arg(subscription_id)
ORDER BY period_start, id;

-- name: LiveAllocationsOfSubscriptions :many
-- LiveAllocationsOfSubscriptions is the draft and invoiced allocations of
-- several subscriptions: what DueOrders and Allocate judge a period by.
SELECT * FROM orders.invoice_allocations
WHERE subscription_id = ANY(sqlc.arg(ids)::bigint[]) AND state IN ('draft', 'invoiced')
ORDER BY subscription_id, period_start, id;

-- name: BillableSubscriptions :many
-- BillableSubscriptions is, for DueOrders (O43), the subscriptions that may
-- have a period to bill — active, paused (its periods before the pause) or
-- ended (its periods up to the end) — of the customers named, the ids named,
-- or every one, in id order.
SELECT * FROM orders.subscriptions s
WHERE s.status IN ('active', 'paused', 'ended')
  AND (sqlc.arg(every)::boolean
       OR s.customer_id = ANY(sqlc.arg(customer_ids)::integer[])
       OR s.id = ANY(sqlc.arg(subscription_ids)::bigint[]))
ORDER BY s.id;

-- name: LockSubscriptionsByIDs :many
-- LockSubscriptionsByIDs takes subscriptions FOR UPDATE in id order: after
-- the orders and before the lines and the allocations (O36's one lock order).
SELECT * FROM orders.subscriptions WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY id FOR UPDATE;

-- name: LockLinesByIDs :many
-- LockLinesByIDs takes lines of any owner FOR UPDATE in id order, after
-- their orders and subscriptions: the holder's lock of the lines its
-- allocations bill.
SELECT * FROM orders.lines WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY id FOR UPDATE;

-- name: SubscriptionLinesOf :many
-- SubscriptionLinesOf is several subscriptions' lines, every dated version,
-- by subscription, position and validity.
SELECT * FROM orders.lines
WHERE owner_kind = 'subscription' AND owner_id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY owner_id, position, valid_from NULLS FIRST, id;

-- name: InsertPeriodAllocation :one
-- InsertPeriodAllocation writes one new draft allocation of a subscription
-- line's period at revision 1 (O43). The unique live period index refuses a
-- second live one for the same line and period.
INSERT INTO orders.invoice_allocations (
    line_id, subscription_id, kind, period_start, period_end, quantity, unit_price, discount_percent, amount,
    vat_code_id, state, revision, created_at, updated_at
) VALUES (
    sqlc.arg(line_id), sqlc.arg(subscription_id), 'subscription_period', sqlc.arg(period_start), sqlc.arg(period_end),
    sqlc.arg(quantity), sqlc.arg(unit_price), sqlc.arg(discount_percent), sqlc.arg(amount), sqlc.arg(vat_code_id),
    'draft', 1, sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: CloseSubscriptionLine :exec
-- CloseSubscriptionLine ends a dated subscription line on valid_to, the day
-- before a change takes effect (O44).
UPDATE orders.lines SET valid_to = sqlc.arg(valid_to)
WHERE id = sqlc.arg(id) AND owner_kind = 'subscription';

-- name: SetSubscriptionLinePosition :exec
-- SetSubscriptionLinePosition moves a kept subscription line to its place in
-- the lines a change hands in.
UPDATE orders.lines SET position = sqlc.arg(position)
WHERE id = sqlc.arg(id) AND owner_kind = 'subscription';

-- name: DeleteLineByID :execrows
-- DeleteLineByID removes one line — a dated subscription line a change
-- replaced before it ever took effect.
DELETE FROM orders.lines WHERE id = sqlc.arg(id);

-- name: LineHasAllocations :one
-- LineHasAllocations reports whether any allocation names a line.
SELECT EXISTS (SELECT 1 FROM orders.invoice_allocations WHERE line_id = sqlc.arg(line_id))::boolean;

-- name: ActiveSubscriptionSums :many
-- ActiveSubscriptionSums is a customer's active and paused subscriptions,
-- counted, and their value per year — a full period's net at the lines in
-- force from the latest change on, times the periods in a year — per
-- currency (the customer summary).
SELECT currency::text AS currency, count(*)::integer AS documents,
       coalesce(sum(net_total * CASE billing_interval WHEN 'monthly' THEN 12 WHEN 'quarterly' THEN 4
                                                      WHEN 'half_yearly' THEN 2 ELSE 1 END), 0)::numeric(14,2) AS amount
FROM orders.subscriptions
WHERE customer_id = sqlc.arg(customer_id) AND status IN ('active', 'paused')
GROUP BY currency
ORDER BY currency;

-- name: AcceptQuoteIntoSubscription :one
-- AcceptQuoteIntoSubscription records a sent or expired quote accepted into
-- a subscription (O20).
UPDATE orders.quotes
SET status = 'accepted', accepted_at = sqlc.arg(now), accepted_into_kind = 'subscription',
    accepted_into_id = sqlc.arg(into_id), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: RepointCustomerSubscriptions :execrows
-- RepointCustomerSubscriptions moves every subscription of a merged-away
-- customer to the surviving one (O29); a draft's updated_at moves.
UPDATE orders.subscriptions
SET customer_id = sqlc.arg(into_customer_id),
    updated_at = CASE WHEN status = 'draft' THEN now() ELSE updated_at END
WHERE customer_id = sqlc.arg(from_customer_id);

-- name: CustomerSubscriptions :many
-- CustomerSubscriptions is every subscription of a customer, for the export.
SELECT * FROM orders.subscriptions WHERE customer_id = sqlc.arg(customer_id) ORDER BY id;

-- name: LockCustomerSubscriptions :exec
-- LockCustomerSubscriptions takes a customer's subscriptions FOR UPDATE in id
-- order, after their quotes and orders.
SELECT id FROM orders.subscriptions WHERE customer_id = sqlc.arg(customer_id) ORDER BY id FOR UPDATE;

-- name: AnonymiseCustomerSubscriptions :execrows
-- AnonymiseCustomerSubscriptions takes the person out of a customer's
-- subscriptions that are no record (O29, O54): every one never activated
-- when cutoff is NULL; and, for the retention sweep, also every activated
-- one that ended or was cancelled before cutoff — kept until 3 years and 6
-- months after the end of the financial year it ended in. The column list
-- is the orders'.
UPDATE orders.subscriptions s SET
    customer_name = '', customer_organisation_number = NULL, customer_foreign_id = NULL,
    invoice_address_line1 = NULL, invoice_address_line2 = NULL, invoice_postal_code = NULL, invoice_city = NULL,
    delivery_address_id = NULL, delivery_name = NULL, delivery_address_line1 = NULL, delivery_address_line2 = NULL,
    delivery_postal_code = NULL, delivery_city = NULL,
    contact_id = NULL, contact_name = NULL, contact_email = NULL, contact_phone = NULL,
    customer_invoice_email = NULL, your_reference = '', internal_note = '', anonymised_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE s.customer_id = sqlc.arg(customer_id) AND s.anonymised_at IS NULL
  AND (
      s.activated_at IS NULL
      OR (sqlc.narg(cutoff)::timestamptz IS NOT NULL AND (
          (s.status = 'cancelled' AND s.cancelled_at < sqlc.narg(cutoff)::timestamptz)
          OR (s.status = 'ended' AND s.end_date < sqlc.narg(cutoff)::timestamptz::date)))
  );

-- name: KeptSubscriptionNumbers :many
-- KeptSubscriptionNumbers is the numbers of a customer's subscriptions still
-- holding the person: the activated ones whose retention has not run (O54).
SELECT number FROM orders.subscriptions
WHERE customer_id = sqlc.arg(customer_id) AND anonymised_at IS NULL
ORDER BY number;
