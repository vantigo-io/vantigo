-- Purchases (A17–A20, R2, R5, R8): suppliers, supplier invoices with their
-- lines, files and payments, and the reads Expenses' facts are posted with.

-- name: InsertSupplier :one
INSERT INTO accounting.suppliers (number, name, organisation_number, country, address_line, postal_code, city, email,
    bank_account, iban, bic, payment_terms_days, default_account_id, default_tax_code_id, currency, is_person,
    created_at, updated_at)
VALUES (sqlc.arg(number), sqlc.arg(name), sqlc.narg(organisation_number), sqlc.arg(country), sqlc.narg(address_line),
    sqlc.narg(postal_code), sqlc.narg(city), sqlc.narg(email), sqlc.narg(bank_account), sqlc.narg(iban), sqlc.narg(bic),
    sqlc.arg(payment_terms_days), sqlc.narg(default_account_id), sqlc.narg(default_tax_code_id), sqlc.arg(currency),
    sqlc.arg(is_person), sqlc.arg(now), sqlc.arg(now))
RETURNING id;

-- name: UpdateSupplier :one
UPDATE accounting.suppliers
SET name = sqlc.arg(name), organisation_number = sqlc.narg(organisation_number), country = sqlc.arg(country),
    address_line = sqlc.narg(address_line), postal_code = sqlc.narg(postal_code), city = sqlc.narg(city),
    email = sqlc.narg(email), bank_account = sqlc.narg(bank_account), iban = sqlc.narg(iban), bic = sqlc.narg(bic),
    payment_terms_days = sqlc.arg(payment_terms_days), default_account_id = sqlc.narg(default_account_id),
    default_tax_code_id = sqlc.narg(default_tax_code_id), currency = sqlc.arg(currency), is_person = sqlc.arg(is_person),
    active = sqlc.arg(active), revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(revision)
RETURNING id;

-- name: GetSupplier :one
-- GetSupplier is a supplier with whether it has postings, its balance on
-- payables and the date of its last voucher.
SELECT s.*,
    EXISTS (SELECT 1 FROM accounting.postings p WHERE p.supplier_id = s.id) AS has_postings,
    coalesce((SELECT sum(p.amount) FROM accounting.postings p
              JOIN accounting.accounts a ON a.id = p.account_id AND a.system_role = 'payables'
              WHERE p.supplier_id = s.id), 0)::text AS balance,
    coalesce(lv.last_voucher_date::text, '')::text AS last_voucher_date
FROM accounting.suppliers s
LEFT JOIN LATERAL (SELECT v.voucher_date AS last_voucher_date FROM accounting.postings p
    JOIN accounting.vouchers v ON v.id = p.voucher_id WHERE p.supplier_id = s.id
    ORDER BY v.voucher_date DESC LIMIT 1) lv ON true
WHERE s.id = sqlc.arg(id);

-- name: ListSuppliers :many
SELECT s.*,
    EXISTS (SELECT 1 FROM accounting.postings p WHERE p.supplier_id = s.id) AS has_postings,
    coalesce((SELECT sum(p.amount) FROM accounting.postings p
              JOIN accounting.accounts a ON a.id = p.account_id AND a.system_role = 'payables'
              WHERE p.supplier_id = s.id), 0)::text AS balance,
    coalesce(lv.last_voucher_date::text, '')::text AS last_voucher_date,
    count(*) OVER () AS total
FROM accounting.suppliers s
LEFT JOIN LATERAL (SELECT v.voucher_date AS last_voucher_date FROM accounting.postings p
    JOIN accounting.vouchers v ON v.id = p.voucher_id WHERE p.supplier_id = s.id
    ORDER BY v.voucher_date DESC LIMIT 1) lv ON true
WHERE (sqlc.narg(active)::boolean IS NULL OR s.active = sqlc.narg(active)::boolean)
  AND (sqlc.narg(q)::text IS NULL OR s.name ILIKE '%' || replace(replace(replace(sqlc.narg(q)::text, '\', '\\'), '%', '\%'), '_', '\_') || '%'
       OR s.number::text = sqlc.narg(q)::text OR s.organisation_number = sqlc.narg(q)::text)
ORDER BY s.number
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: SupplierHasHistory :one
SELECT (EXISTS (SELECT 1 FROM accounting.postings p WHERE p.supplier_id = sqlc.arg(id)::bigint)
    OR EXISTS (SELECT 1 FROM accounting.open_items o WHERE o.supplier_id = sqlc.arg(id)::bigint)
    OR EXISTS (SELECT 1 FROM accounting.supplier_invoices si WHERE si.supplier_id = sqlc.arg(id)::bigint))::boolean AS used;

-- name: DeleteSupplier :execrows
DELETE FROM accounting.suppliers WHERE id = sqlc.arg(id);

-- name: SupplierByName :one
-- SupplierByName is the active supplier of that name, case aside, the lowest
-- number first: what an expense's free-text supplier is matched to.
SELECT id FROM accounting.suppliers WHERE lower(btrim(name)) = lower(btrim(sqlc.arg(name)::text)) AND active
ORDER BY number LIMIT 1;

-- name: SupplierPostings :many
-- SupplierPostings is every posting naming a supplier, for its export.
SELECT v.id AS voucher_id, v.fiscal_year, v.number, v.voucher_date, a.number AS account_number, p.amount::text AS amount,
    coalesce(nullif(p.description, ''), v.description)::text AS description
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
JOIN accounting.accounts a ON a.id = p.account_id
WHERE p.supplier_id = sqlc.arg(id)::bigint
ORDER BY v.voucher_date, v.id, p.line_no;

-- name: InsertSupplierInvoice :one
INSERT INTO accounting.supplier_invoices (supplier_id, kind, supplier_number, invoice_date, due_date, currency,
    exchange_rate, total, total_nok, kid, reference, description, credits_invoice_id, copied_from_id,
    source_expense_entry_id, created_at, created_by_user_id, updated_at)
VALUES (sqlc.arg(supplier_id), sqlc.arg(kind), sqlc.arg(supplier_number), sqlc.arg(invoice_date), sqlc.narg(due_date),
    sqlc.arg(currency), sqlc.narg(exchange_rate)::text::numeric, sqlc.arg(total)::text::numeric,
    sqlc.narg(total_nok)::text::numeric, sqlc.narg(kid), sqlc.narg(reference), sqlc.arg(description),
    sqlc.narg(credits_invoice_id), sqlc.narg(copied_from_id), sqlc.narg(source_expense_entry_id), sqlc.arg(now),
    sqlc.arg(user_id), sqlc.arg(now))
RETURNING id;

-- name: UpdateSupplierInvoice :one
UPDATE accounting.supplier_invoices
SET supplier_id = sqlc.arg(supplier_id), kind = sqlc.arg(kind), supplier_number = sqlc.arg(supplier_number),
    invoice_date = sqlc.arg(invoice_date), due_date = sqlc.narg(due_date), currency = sqlc.arg(currency),
    exchange_rate = sqlc.narg(exchange_rate)::text::numeric, total = sqlc.arg(total)::text::numeric,
    total_nok = sqlc.narg(total_nok)::text::numeric, kid = sqlc.narg(kid), reference = sqlc.narg(reference),
    description = sqlc.arg(description), credits_invoice_id = sqlc.narg(credits_invoice_id),
    revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(revision) AND state = 'draft'
RETURNING id;

-- name: DeleteSupplierInvoiceLines :exec
DELETE FROM accounting.supplier_invoice_lines WHERE supplier_invoice_id = sqlc.arg(id);

-- name: InsertSupplierInvoiceLine :exec
INSERT INTO accounting.supplier_invoice_lines (supplier_invoice_id, position, account_id, tax_code_id, amount, entry,
    project_id, text)
VALUES (sqlc.arg(supplier_invoice_id), sqlc.arg(position), sqlc.arg(account_id), sqlc.narg(tax_code_id),
    sqlc.arg(amount)::text::numeric, sqlc.arg(entry), sqlc.narg(project_id), sqlc.arg(text));

-- name: LockSupplierInvoice :one
SELECT id, supplier_id, kind, supplier_number, invoice_date, due_date, currency, coalesce(exchange_rate::text, '')::text AS exchange_rate,
    total::text AS total, coalesce(total_nok::text, '')::text AS total_nok, kid, reference, description, state, credits_invoice_id,
    voucher_id, open_item_id, reversal_voucher_id, copied_from_id, source_expense_entry_id, revision
FROM accounting.supplier_invoices WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: GetSupplierInvoices :many
-- GetSupplierInvoices is the invoices with those ids, their supplier, and
-- their open item's balance in NOK and in the invoice's currency.
SELECT si.id, si.supplier_id, s.name AS supplier_name, s.number AS supplier_seq, si.kind, si.supplier_number,
    si.invoice_date, si.due_date, si.currency, coalesce(si.exchange_rate::text, '')::text AS exchange_rate, si.total::text AS total,
    coalesce(si.total_nok::text, '')::text AS total_nok, si.kid, si.reference, si.description, si.state, si.credits_invoice_id,
    si.voucher_id, si.open_item_id, si.reversal_voucher_id, si.copied_from_id, si.source_expense_entry_id,
    si.revision, si.created_at, si.updated_at, si.posted_at,
    b.balance::text AS balance, b.balance_currency::text AS balance_currency,
    v.fiscal_year AS voucher_year, v.number AS voucher_number, rv.fiscal_year AS reversal_year, rv.number AS reversal_number
FROM accounting.supplier_invoices si
JOIN accounting.suppliers s ON s.id = si.supplier_id
LEFT JOIN accounting.vouchers v ON v.id = si.voucher_id
LEFT JOIN accounting.vouchers rv ON rv.id = si.reversal_voucher_id
CROSS JOIN LATERAL (SELECT coalesce(sum(p.amount), 0) AS balance,
        coalesce(sum(coalesce(p.currency_amount, p.amount)), 0) AS balance_currency
    FROM accounting.postings p WHERE si.open_item_id IS NOT NULL AND p.open_item_id = si.open_item_id) b
WHERE si.id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY si.invoice_date DESC, si.id DESC;

-- name: ListSupplierInvoiceIDs :many
-- ListSupplierInvoiceIDs is a page of the invoices matching the filters,
-- newest invoice date first, with the count of every match. paid is a posted
-- invoice whose open item's balance is zero; overdue a posted one not
-- settled and due before today.
SELECT si.id, count(*) OVER () AS total
FROM accounting.supplier_invoices si
CROSS JOIN LATERAL (SELECT coalesce(sum(p.amount), 0) AS balance FROM accounting.postings p
    WHERE si.open_item_id IS NOT NULL AND p.open_item_id = si.open_item_id) b
WHERE (sqlc.narg(state)::text IS NULL
       OR (sqlc.narg(state)::text = 'paid' AND si.state = 'posted' AND b.balance = 0)
       OR (sqlc.narg(state)::text = 'posted' AND si.state = 'posted' AND b.balance <> 0)
       OR (sqlc.narg(state)::text IN ('draft', 'reversed') AND si.state = sqlc.narg(state)::text))
  AND (sqlc.narg(supplier_id)::bigint IS NULL OR si.supplier_id = sqlc.narg(supplier_id)::bigint)
  AND (sqlc.narg(supplier_number)::text IS NULL
       OR lower(btrim(si.supplier_number)) = lower(btrim(sqlc.narg(supplier_number)::text)))
  AND (sqlc.narg(due_from)::date IS NULL OR si.due_date >= sqlc.narg(due_from)::date)
  AND (sqlc.narg(due_to)::date IS NULL OR si.due_date <= sqlc.narg(due_to)::date)
  AND (NOT sqlc.arg(overdue)::boolean OR (si.state = 'posted' AND b.balance <> 0 AND si.due_date < sqlc.arg(today)::date))
ORDER BY si.invoice_date DESC, si.id DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: SupplierInvoiceLines :many
SELECT l.id, l.supplier_invoice_id, l.position, l.account_id, a.number AS account_number, a.name AS account_name,
    l.tax_code_id, t.code AS tax_code, l.amount::text AS amount, l.entry, l.project_id, l.text
FROM accounting.supplier_invoice_lines l
JOIN accounting.accounts a ON a.id = l.account_id
LEFT JOIN accounting.tax_codes t ON t.id = l.tax_code_id
WHERE l.supplier_invoice_id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY l.supplier_invoice_id, l.position;

-- name: SupplierInvoiceFiles :many
SELECT * FROM accounting.supplier_invoice_files
WHERE supplier_invoice_id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY supplier_invoice_id, id;

-- name: InsertSupplierInvoiceFile :one
INSERT INTO accounting.supplier_invoice_files (supplier_invoice_id, object_key, file_name, content_type, size_bytes,
    sha256, uploaded_at, uploaded_by_user_id)
VALUES (sqlc.arg(supplier_invoice_id), sqlc.arg(object_key), sqlc.arg(file_name), sqlc.arg(content_type),
    sqlc.arg(size_bytes), sqlc.arg(sha256), sqlc.arg(now), sqlc.arg(user_id))
RETURNING *;

-- name: GetSupplierInvoiceFile :one
SELECT * FROM accounting.supplier_invoice_files WHERE id = sqlc.arg(id) AND supplier_invoice_id = sqlc.arg(supplier_invoice_id);

-- name: DeleteSupplierInvoiceFile :one
DELETE FROM accounting.supplier_invoice_files WHERE id = sqlc.arg(id) AND supplier_invoice_id = sqlc.arg(supplier_invoice_id)
RETURNING object_key;

-- name: ObjectKeyInUse :one
-- ObjectKeyInUse is whether any file or attachment still names an object:
-- one that none names is deleted from the store.
SELECT (EXISTS (SELECT 1 FROM accounting.supplier_invoice_files f WHERE f.object_key = sqlc.arg(object_key)::varchar)
    OR EXISTS (SELECT 1 FROM accounting.attachments a WHERE a.object_key = sqlc.arg(object_key)::varchar))::boolean AS used;

-- name: DeleteSupplierInvoice :many
-- DeleteSupplierInvoice deletes a draft with its lines and files, answering
-- the files' object keys.
WITH keys AS (
    SELECT f.object_key FROM accounting.supplier_invoice_files f
    JOIN accounting.supplier_invoices si ON si.id = f.supplier_invoice_id
    WHERE si.id = sqlc.arg(id)::bigint AND si.state = 'draft'
), gone AS (
    DELETE FROM accounting.supplier_invoices d WHERE d.id = sqlc.arg(id)::bigint AND d.state = 'draft' RETURNING d.id
)
SELECT k.object_key::varchar AS object_key FROM keys k WHERE EXISTS (SELECT 1 FROM gone)
UNION ALL
SELECT '' WHERE EXISTS (SELECT 1 FROM gone) AND NOT EXISTS (SELECT 1 FROM keys);

-- name: MarkSupplierInvoicePosted :exec
UPDATE accounting.supplier_invoices
SET state = 'posted', voucher_id = sqlc.arg(voucher_id), open_item_id = sqlc.arg(open_item_id),
    posted_at = sqlc.arg(now), posted_by_user_id = sqlc.arg(user_id), revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND state = 'draft';

-- name: MarkSupplierInvoiceReversed :exec
UPDATE accounting.supplier_invoices
SET state = 'reversed', reversal_voucher_id = sqlc.arg(voucher_id), revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND state = 'posted';

-- name: VoucherOpenItem :one
-- VoucherOpenItem is the open item a voucher's payables line landed on.
SELECT p.open_item_id::bigint FROM accounting.postings p
WHERE p.voucher_id = sqlc.arg(voucher_id) AND p.open_item_id IS NOT NULL ORDER BY p.line_no LIMIT 1;

-- name: InsertSupplierPayment :one
INSERT INTO accounting.supplier_payments (supplier_invoice_id, paid_on, amount_nok, amount_currency, bank_account_id,
    voucher_id, created_at, created_by_user_id)
VALUES (sqlc.arg(supplier_invoice_id), sqlc.arg(paid_on), sqlc.arg(amount_nok)::text::numeric,
    sqlc.arg(amount_currency)::text::numeric, sqlc.arg(bank_account_id), sqlc.arg(voucher_id), sqlc.arg(now),
    sqlc.arg(user_id))
RETURNING id;

-- name: SupplierPayments :many
SELECT sp.id, sp.supplier_invoice_id, sp.paid_on, sp.amount_nok::text AS amount_nok,
    sp.amount_currency::text AS amount_currency, sp.bank_account_id, a.number AS bank_account_number, sp.voucher_id,
    v.fiscal_year AS voucher_year, v.number AS voucher_number, sp.removed_at, sp.removal_voucher_id,
    rv.fiscal_year AS removal_year, rv.number AS removal_number,
    coalesce((SELECT sum(p.amount) FROM accounting.postings p
              JOIN accounting.accounts ra ON ra.id = p.account_id AND ra.system_role IN ('currency_gain', 'currency_loss')
              WHERE p.voucher_id = sp.voucher_id), 0)::text AS currency_difference
FROM accounting.supplier_payments sp
JOIN accounting.accounts a ON a.id = sp.bank_account_id
JOIN accounting.vouchers v ON v.id = sp.voucher_id
LEFT JOIN accounting.vouchers rv ON rv.id = sp.removal_voucher_id
WHERE sp.supplier_invoice_id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY sp.supplier_invoice_id, sp.paid_on, sp.id;

-- name: LockSupplierPayment :one
SELECT id, supplier_invoice_id, paid_on, voucher_id, removed_at FROM accounting.supplier_payments
WHERE id = sqlc.arg(id) AND supplier_invoice_id = sqlc.arg(supplier_invoice_id) FOR UPDATE;

-- name: MarkSupplierPaymentRemoved :exec
UPDATE accounting.supplier_payments
SET removed_at = sqlc.arg(now), removed_by_user_id = sqlc.arg(user_id), removal_voucher_id = sqlc.arg(voucher_id)
WHERE id = sqlc.arg(id) AND removed_at IS NULL;

-- name: OpenItemCurrencyBalance :one
-- OpenItemCurrencyBalance is an item's balance in NOK and in its currency
-- (the postings' currency amounts; a NOK posting counts its amount).
SELECT coalesce(sum(p.amount), 0)::text AS balance,
    coalesce(sum(coalesce(p.currency_amount, p.amount)), 0)::text AS balance_currency
FROM accounting.postings p WHERE p.open_item_id = sqlc.arg(id)::bigint;

-- name: SupplierInvoiceOfItem :one
SELECT id FROM accounting.supplier_invoices WHERE open_item_id = sqlc.arg(open_item_id)::bigint ORDER BY id LIMIT 1;

-- name: PayablesDue :one
-- PayablesDue is the posted supplier invoices not settled that are overdue at
-- today, and those due from today through soon.
SELECT count(*) FILTER (WHERE si.due_date < sqlc.arg(today)::date)::int AS overdue,
    count(*) FILTER (WHERE si.due_date >= sqlc.arg(today)::date AND si.due_date <= sqlc.arg(soon)::date)::int AS due_soon
FROM accounting.supplier_invoices si
WHERE si.state = 'posted' AND si.kind = 'invoice'
  AND coalesce((SELECT sum(p.amount) FROM accounting.postings p WHERE p.open_item_id = si.open_item_id), 0) <> 0;

-- name: SourcePostingsByKey :many
-- SourcePostingsByKey is the postings of the voucher a whole source key
-- posted as, part included, with the voucher's id: what an undo turns back.
SELECT v.id AS voucher_id, p.line_no, p.account_id, p.amount::text AS amount, p.tax_code_id, p.tax_role,
    coalesce(p.tax_base::text, '')::text AS tax_base, p.tax_specification, p.customer_id, p.supplier_id,
    p.open_item_id, p.project_id, p.description, p.posting_rule_id, p.posting_rule_version, p.currency,
    coalesce(p.currency_amount::text, '')::text AS currency_amount, coalesce(p.exchange_rate::text, '')::text AS exchange_rate
FROM accounting.vouchers v
JOIN accounting.postings p ON p.voucher_id = v.id
WHERE v.source_module = sqlc.arg(source_module) AND v.source_kind = sqlc.arg(source_kind)
  AND v.source_id = sqlc.arg(source_id) AND v.source_part = sqlc.arg(source_part)
ORDER BY p.line_no;

-- name: VoucherPostingsForTurn :many
-- VoucherPostingsForTurn is a voucher's postings as lines to turn back.
SELECT p.voucher_id, p.line_no, p.account_id, p.amount::text AS amount, p.tax_code_id, p.tax_role,
    coalesce(p.tax_base::text, '')::text AS tax_base, p.tax_specification, p.customer_id, p.supplier_id,
    p.open_item_id, p.project_id, p.description, p.posting_rule_id, p.posting_rule_version, p.currency,
    coalesce(p.currency_amount::text, '')::text AS currency_amount, coalesce(p.exchange_rate::text, '')::text AS exchange_rate
FROM accounting.postings p WHERE p.voucher_id = sqlc.arg(voucher_id) ORDER BY p.line_no;

-- name: ExpenseRules :many
-- ExpenseRules is every expense rule in force with its account.
SELECT r.id, r.name, r.expense_category_id, r.account_id, r.version, coalesce(r.vat_deductible, false)::boolean AS vat_deductible,
    a.active AS account_active, a.number AS account_number, a.kind AS account_kind
FROM accounting.posting_rules r
JOIN accounting.accounts a ON a.id = r.account_id
WHERE r.scope = 'expense' AND r.archived_at IS NULL;

-- name: InputRates :many
-- InputRates is the domestic deductible input codes (direction input, not
-- reverse charge, with a VAT account and not an import code) with their
-- rate on day: what an expense's VAT is matched against (R2).
SELECT t.id, t.code, t.vat_account_id::int AS vat_account_id, r.rate_percent::text AS rate_percent
FROM accounting.tax_codes t
JOIN accounting.tax_code_rates r ON r.tax_code_id = t.id AND r.valid_from <= sqlc.arg(day)::date
    AND (r.valid_to IS NULL OR r.valid_to >= sqlc.arg(day)::date)
WHERE t.active AND t.direction = 'input' AND NOT t.reverse_charge AND t.vat_account_id IS NOT NULL
  AND t.code IN ('1', '11', '12', '13') AND r.rate_percent > 0
ORDER BY t.code;

-- name: RecordInformational :exec
-- RecordInformational keeps a row that posted but says something a person
-- should see (vat_not_deducted, R2): it is posted at once and never blocks.
INSERT INTO accounting.unposted (stream, position, source_module, source_kind, source_id, source_part, fact_date,
    amount, reason, detail, first_seen_at, last_tried_at, posted_voucher_id, posted_at, order_key, informational,
    supplier_invoice_id)
VALUES (sqlc.arg(stream), sqlc.arg(position), sqlc.arg(source_module), sqlc.arg(source_kind), sqlc.arg(source_id),
    sqlc.arg(source_part), sqlc.narg(fact_date), sqlc.narg(amount)::text::numeric, sqlc.arg(reason), sqlc.arg(detail),
    sqlc.arg(now), sqlc.arg(now), sqlc.arg(voucher_id), sqlc.arg(now), sqlc.narg(order_key), true,
    sqlc.narg(supplier_invoice_id))
ON CONFLICT (source_module, source_kind, source_id, source_part) DO UPDATE
SET reason = EXCLUDED.reason, detail = EXCLUDED.detail, posted_voucher_id = EXCLUDED.posted_voucher_id,
    posted_at = EXCLUDED.posted_at, last_tried_at = EXCLUDED.last_tried_at, informational = true,
    waiting_on = NULL, attempts = accounting.unposted.attempts + 1,
    supplier_invoice_id = coalesce(EXCLUDED.supplier_invoice_id, accounting.unposted.supplier_invoice_id)
WHERE accounting.unposted.posted_at IS NULL;

-- name: UnpostedBlockerByKey :one
-- UnpostedBlockerByKey is the row an expense fact waits behind (R2, R15): the
-- earliest open row of the same entry or claim that is not its own and was
-- recorded before it.
SELECT u.id, u.source_kind FROM accounting.unposted u
WHERE u.order_key = sqlc.arg(order_key) AND u.posted_at IS NULL AND u.resolved_at IS NULL
  AND NOT (u.source_module = sqlc.arg(source_module) AND u.source_kind = sqlc.arg(source_kind)
           AND u.source_id = sqlc.arg(source_id) AND u.source_part = sqlc.arg(source_part))
  AND u.id < coalesce((SELECT o.id FROM accounting.unposted o
                       WHERE o.source_module = sqlc.arg(source_module) AND o.source_kind = sqlc.arg(source_kind)
                         AND o.source_id = sqlc.arg(source_id) AND o.source_part = sqlc.arg(source_part)),
                      9223372036854775807)
ORDER BY u.id
LIMIT 1;

-- name: UnpostedDependantsByKey :many
SELECT u.id, u.stream, u.position, u.source_module, u.source_kind, u.source_id, u.source_part, u.fact_date, u.document_id,
    u.document_number, u.customer_id, coalesce(u.amount, 0)::text AS amount, u.reason, u.detail, u.attempts, u.first_seen_at,
    u.last_tried_at, u.posted_voucher_id, u.posted_at, NULL::integer AS voucher_year, NULL::integer AS voucher_number,
    u.invoice_id, u.waiting_on, u.order_key, u.informational, u.resolved_at, u.supplier_invoice_id,
    coalesce(u.nok_amounts::text, '')::text AS nok_amounts
FROM accounting.unposted u
WHERE u.order_key = sqlc.arg(order_key) AND u.posted_at IS NULL AND u.resolved_at IS NULL AND u.id > sqlc.arg(after_id)
ORDER BY u.id;

-- name: OpenUnpostedBySource :one
SELECT id, reason FROM accounting.unposted
WHERE source_module = sqlc.arg(source_module) AND source_kind = sqlc.arg(source_kind)
  AND source_id = sqlc.arg(source_id) AND source_part = sqlc.arg(source_part)
  AND posted_at IS NULL AND resolved_at IS NULL;

-- name: SetUnpostedNokAmounts :exec
UPDATE accounting.unposted SET nok_amounts = sqlc.arg(nok_amounts)::text::jsonb
WHERE id = sqlc.arg(id) AND posted_at IS NULL;

-- name: ResolveUnposted :execrows
UPDATE accounting.unposted
SET resolved_at = sqlc.arg(now), supplier_invoice_id = sqlc.narg(supplier_invoice_id), last_tried_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND resolved_at IS NULL;

-- name: ReopenUnpostedOfEntry :exec
-- ReopenUnpostedOfEntry gives an expense of the supplier_invoice kind back to
-- Needs attention when the draft made from it — or the copy Reverse and copy
-- made of that — is deleted: its approval's row, matched by the entry.
UPDATE accounting.unposted SET resolved_at = NULL, supplier_invoice_id = NULL
WHERE source_module = 'expenses' AND source_kind = 'entry.approved' AND source_id = sqlc.arg(entry_id)::bigint
  AND reason = 'enter_as_supplier_invoice' AND posted_at IS NULL AND resolved_at IS NOT NULL
  AND id = (SELECT max(u.id) FROM accounting.unposted u
            WHERE u.source_module = 'expenses' AND u.source_kind = 'entry.approved'
              AND u.source_id = sqlc.arg(entry_id)::bigint AND u.reason = 'enter_as_supplier_invoice');

-- name: RepointUnpostedDraft :exec
-- RepointUnpostedDraft names the copy Reverse and copy made on the rows that
-- named the invoice it reversed.
UPDATE accounting.unposted SET supplier_invoice_id = sqlc.arg(copy_id)::bigint
WHERE supplier_invoice_id = sqlc.arg(original_id)::bigint;

-- name: ItemHasOtherVouchers :one
-- ItemHasOtherVouchers is whether an open item carries a posting of a voucher
-- other than those named that stands: not reversed, and not itself a
-- reversal — a credit note, a payment, a settlement on it.
SELECT EXISTS (
    SELECT 1 FROM accounting.postings p JOIN accounting.vouchers v ON v.id = p.voucher_id
    WHERE p.open_item_id = sqlc.arg(item_id)::bigint AND NOT (v.id = ANY(sqlc.arg(own)::bigint[]))
      AND v.reverses_voucher_id IS NULL
      AND NOT EXISTS (SELECT 1 FROM accounting.vouchers r WHERE r.reverses_voucher_id = v.id))::boolean AS used;

-- name: UnpostedOfSource :one
-- UnpostedOfSource is the row of a source key, open or not.
SELECT id, reason, posted_at, resolved_at, supplier_invoice_id, informational FROM accounting.unposted
WHERE source_module = sqlc.arg(source_module) AND source_kind = sqlc.arg(source_kind)
  AND source_id = sqlc.arg(source_id) AND source_part = sqlc.arg(source_part);

-- name: ResolveInformationalBySource :exec
-- ResolveInformationalBySource closes an informational row whose voucher an
-- undoing has turned back: there is nothing left to say about it.
UPDATE accounting.unposted SET resolved_at = sqlc.arg(now), last_tried_at = sqlc.arg(now)
WHERE source_module = sqlc.arg(source_module) AND source_kind = sqlc.arg(source_kind)
  AND source_id = sqlc.arg(source_id) AND source_part = sqlc.arg(source_part)
  AND informational AND resolved_at IS NULL;

-- name: SupplierInvoiceState :one
SELECT state, voucher_id, supplier_number FROM accounting.supplier_invoices WHERE id = sqlc.arg(id);

-- name: AnonymiseSupplier :execrows
-- AnonymiseSupplier takes a person out of a supplier's record once the
-- bookkeeping's retention has run out: the name becomes its number's, every
-- contact and bank field is cleared. Postings keep their supplier id.
UPDATE accounting.suppliers
SET name = sqlc.arg(name), organisation_number = NULL, address_line = NULL, postal_code = NULL, city = NULL,
    email = NULL, bank_account = NULL, iban = NULL, bic = NULL, active = false, anonymised_at = sqlc.arg(now),
    revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND is_person AND anonymised_at IS NULL;

-- name: InitUnpostedLines :exec
-- InitUnpostedLines keeps, on an expense fact's row, its lines in another
-- currency, for the amounts in NOK a person enters; once.
UPDATE accounting.unposted SET nok_amounts = coalesce(nok_amounts, sqlc.arg(lines)::text::jsonb)
WHERE source_module = sqlc.arg(source_module) AND source_kind = sqlc.arg(source_kind)
  AND source_id = sqlc.arg(source_id) AND source_part = sqlc.arg(source_part);

-- name: ResolveUnpostedBySource :exec
UPDATE accounting.unposted SET resolved_at = sqlc.arg(now), last_tried_at = sqlc.arg(now),
    detail = sqlc.arg(detail)
WHERE source_module = sqlc.arg(source_module) AND source_kind = sqlc.arg(source_kind)
  AND source_id = sqlc.arg(source_id) AND source_part = sqlc.arg(source_part)
  AND posted_at IS NULL AND resolved_at IS NULL;

-- name: LatestExpenseCost :one
-- LatestExpenseCost is the voucher an entry's or a claim's last approval
-- posted as and that no unapproval has turned back: what its reimbursement
-- pays out of employee_payables.
SELECT v.id FROM accounting.vouchers v
WHERE v.source_module = 'expenses' AND v.source_kind = sqlc.arg(source_kind) AND v.source_id = sqlc.arg(source_id)
  AND NOT EXISTS (SELECT 1 FROM accounting.vouchers r WHERE r.reverses_voucher_id = v.id)
ORDER BY v.id DESC LIMIT 1;

-- name: VoucherRoleTotal :one
-- VoucherRoleTotal is what a voucher posted on the account holding a role.
SELECT coalesce(sum(p.amount), 0)::text AS total FROM accounting.postings p
JOIN accounting.accounts a ON a.id = p.account_id
WHERE p.voucher_id = sqlc.arg(voucher_id) AND a.system_role = sqlc.arg(role)::varchar;

-- name: ExpenseInvoiced :one
-- ExpenseInvoiced is whether an expense has a supplier invoice made from it
-- that is not reversed.
SELECT EXISTS (SELECT 1 FROM accounting.supplier_invoices
    WHERE source_expense_entry_id = sqlc.arg(entry_id)::bigint AND state <> 'reversed')::boolean AS invoiced;

-- name: LockSupplierForUpdate :one
-- LockSupplierForUpdate takes a supplier FOR UPDATE before its delete or its
-- anonymisation asks the holders (purchasing design A8, rule 28): a writer
-- that holds it FOR SHARE (LockSuppliers) has committed, or waits.
SELECT id FROM accounting.suppliers WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: DirectorySuppliers :many
-- DirectorySuppliers is the suppliers named, for the supplier directory.
SELECT id, number, name, organisation_number, country, address_line, postal_code, city, email, currency,
    payment_terms_days, active, is_person, anonymised_at, revision
FROM accounting.suppliers WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: LockDirectorySuppliers :many
-- LockDirectorySuppliers takes FOR SHARE on the suppliers named, in id
-- order, on the caller's transaction (SupplierDirectory.LockSuppliers).
SELECT id, number, name, organisation_number, country, address_line, postal_code, city, email, currency,
    payment_terms_days, active, is_person, anonymised_at, revision
FROM accounting.suppliers WHERE id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY id FOR SHARE;

-- name: SearchDirectorySuppliers :many
-- SearchDirectorySuppliers is the suppliers whose name holds the query,
-- ignoring case, or whose number or organisation number is it, by number.
SELECT id, number, name, organisation_number, country, address_line, postal_code, city, email, currency,
    payment_terms_days, active, is_person, anonymised_at, revision
FROM accounting.suppliers s
WHERE s.name ILIKE '%' || replace(replace(replace(sqlc.arg(q)::text, '\', '\\'), '%', '\%'), '_', '\_') || '%'
   OR s.number::text = sqlc.arg(q)::text OR s.organisation_number = sqlc.arg(q)::text
ORDER BY s.number
LIMIT sqlc.arg(row_limit);

-- name: SaftFillSupplierBank :exec
-- SaftFillSupplierBank fills a register supplier's bank account, IBAN and BIC
-- from a SAF-T file where the register's is empty, never over one it has
-- (purchasing delivery 0); the revision moves only when something is filled.
UPDATE accounting.suppliers
SET bank_account = coalesce(nullif(bank_account, ''), sqlc.narg(bank_account)),
    iban = coalesce(nullif(iban, ''), sqlc.narg(iban)),
    bic = coalesce(nullif(bic, ''), sqlc.narg(bic)),
    revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND anonymised_at IS NULL
  AND ((nullif(bank_account, '') IS NULL AND sqlc.narg(bank_account)::text IS NOT NULL)
    OR (nullif(iban, '') IS NULL AND sqlc.narg(iban)::text IS NOT NULL)
    OR (nullif(bic, '') IS NULL AND sqlc.narg(bic)::text IS NOT NULL));

-- name: SaftMatchSuppliers :many
-- SaftMatchSuppliers is every supplier of the register, for a SAF-T
-- import's matching of the file's suppliers (purchasing design A2).
SELECT id, number, name, organisation_number, country
FROM accounting.suppliers
ORDER BY number;
