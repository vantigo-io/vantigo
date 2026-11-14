-- Sales post themselves (A12–A16, R7): the catch-up's cursors, requests and
-- leases, the posting rules, the facts under Needs attention, and the reads
-- a sales voucher is built from.

-- name: RequestCatchUp :exec
-- RequestCatchUp is an event handler's one write (R7, rule 23): a wake-up for
-- the stream, kept at its first moment until a claim takes it.
INSERT INTO accounting.catchup_requests (stream, requested_at)
VALUES (sqlc.arg(stream), sqlc.arg(now))
ON CONFLICT (stream) DO NOTHING;

-- name: EnsureCursor :exec
INSERT INTO accounting.cursors (stream, updated_at) VALUES (sqlc.arg(stream), sqlc.arg(now))
ON CONFLICT (stream) DO NOTHING;

-- name: ClaimCursor :one
-- ClaimCursor takes the stream's lease when it is free or has run out, and
-- the stream is due: a wake-up waits, or the last run is older than the
-- sweep. The claim's transaction then deletes the wake-up (TakeRequest); one
-- that arrives later waits for the next claim.
UPDATE accounting.cursors c
SET lease_id = sqlc.arg(lease_id), lease_until = sqlc.arg(lease_until), updated_at = sqlc.arg(now)
WHERE c.stream = sqlc.arg(stream)
  AND (c.lease_until IS NULL OR c.lease_until <= sqlc.arg(now))
  AND (EXISTS (SELECT 1 FROM accounting.catchup_requests r WHERE r.stream = c.stream)
       OR c.last_run_at IS NULL OR c.last_run_at <= sqlc.arg(sweep_before))
RETURNING c.position;

-- name: TakeRequest :exec
DELETE FROM accounting.catchup_requests WHERE stream = sqlc.arg(stream);

-- name: AdvanceCursor :execrows
-- AdvanceCursor moves the stream's position under the lease that holds it; 0
-- rows is a lease lost.
UPDATE accounting.cursors
SET position = sqlc.arg(position), updated_at = sqlc.arg(now)
WHERE stream = sqlc.arg(stream) AND lease_id = sqlc.arg(lease_id) AND position < sqlc.arg(position);

-- name: ReleaseCursor :exec
-- ReleaseCursor ends the claim and says how the run ended.
UPDATE accounting.cursors
SET lease_id = NULL, lease_until = NULL, last_run_at = sqlc.arg(now), last_error = sqlc.narg(last_error),
    updated_at = sqlc.arg(now)
WHERE stream = sqlc.arg(stream) AND lease_id = sqlc.arg(lease_id);

-- name: ListCursors :many
SELECT c.stream, c.position, c.last_run_at, c.last_error, r.requested_at
FROM accounting.cursors c
LEFT JOIN accounting.catchup_requests r ON r.stream = c.stream
ORDER BY c.stream;

-- name: PendingRequest :one
SELECT requested_at FROM accounting.catchup_requests WHERE stream = sqlc.arg(stream);

-- name: TaxCodesByCode :many
-- TaxCodesByCode is every tax code with its VAT account, for a sales voucher.
SELECT id, code, direction, active, vat_account_id FROM accounting.tax_codes ORDER BY code;

-- name: SalesRules :many
-- SalesRules is every sales rule in force — archived ones match nothing —
-- with its account.
SELECT r.id, r.name, r.match, r.line_kind, r.tax_category_id, r.tax_code, r.account_id, r.version,
    a.active AS account_active, a.number AS account_number, a.kind AS account_kind
FROM accounting.posting_rules r
JOIN accounting.accounts a ON a.id = r.account_id
WHERE r.scope = 'sales' AND r.archived_at IS NULL;

-- name: OpenItemOpeningSeq :one
-- OpenItemOpeningSeq is the customer item standing for an Invoices document,
-- with the seq of the opening page it was read on (R14).
SELECT id, opening_as_of_seq FROM accounting.open_items
WHERE kind = 'customer' AND source_document_id = sqlc.arg(source_document_id);

-- name: AccountByNumber :one
SELECT id FROM accounting.accounts WHERE number = sqlc.arg(number);

-- name: ListPostingRules :many
-- ListPostingRules is the rules in force; an archived rule is kept for its
-- versions only.
SELECT r.*, a.number AS account_number, a.name AS account_name
FROM accounting.posting_rules r
JOIN accounting.accounts a ON a.id = r.account_id
WHERE r.archived_at IS NULL
ORDER BY CASE r.match WHEN 'line_kind' THEN 1 WHEN 'tax_category' THEN 2 WHEN 'expense_category' THEN 3
    WHEN 'tax_code' THEN 4 ELSE 5 END, r.scope, r.name;

-- name: GetPostingRule :one
SELECT r.*, a.number AS account_number, a.name AS account_name
FROM accounting.posting_rules r
JOIN accounting.accounts a ON a.id = r.account_id
WHERE r.id = sqlc.arg(id) AND r.archived_at IS NULL;

-- name: InsertPostingRule :one
-- InsertPostingRule writes a rule; a default rule seeded again is left as it
-- is (the seed is idempotent).
INSERT INTO accounting.posting_rules (name, scope, match, line_kind, tax_category_id, expense_category_id, tax_code,
    account_id, created_at, updated_at, vat_deductible)
VALUES (sqlc.arg(name), sqlc.arg(scope), sqlc.arg(match), sqlc.narg(line_kind), sqlc.narg(tax_category_id),
    sqlc.narg(expense_category_id), sqlc.narg(tax_code), sqlc.arg(account_id), sqlc.arg(now), sqlc.arg(now),
    sqlc.narg(vat_deductible))
ON CONFLICT DO NOTHING
RETURNING id;

-- name: CountDefaultRules :one
SELECT count(*)::int FROM accounting.posting_rules WHERE match = 'default';

-- name: UpdatePostingRule :one
-- UpdatePostingRule changes a rule's name and account under its revision; a
-- changed name or account is a new version, which tr_posting_rules_version
-- keeps (R15).
UPDATE accounting.posting_rules
SET name = sqlc.arg(name)::varchar, account_id = sqlc.arg(account_id),
    vat_deductible = CASE WHEN scope = 'expense' THEN coalesce(sqlc.narg(vat_deductible)::boolean, vat_deductible) END,
    version = version + CASE WHEN account_id <> sqlc.arg(account_id) OR name <> sqlc.arg(name)::varchar
        OR (scope = 'expense' AND vat_deductible IS DISTINCT FROM coalesce(sqlc.narg(vat_deductible)::boolean, vat_deductible))
        THEN 1 ELSE 0 END,
    revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(revision) AND archived_at IS NULL
RETURNING id;

-- name: ArchivePostingRule :execrows
-- ArchivePostingRule takes a rule that is not a default one out of force
-- (R15); its versions stay, and so does what it placed.
UPDATE accounting.posting_rules SET archived_at = sqlc.arg(now), revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND match <> 'default' AND archived_at IS NULL;

-- name: RecordUnposted :exec
-- RecordUnposted keeps a fact that could not be posted, or its new reason
-- when it failed again.
INSERT INTO accounting.unposted (stream, position, source_module, source_kind, source_id, source_part, fact_date,
    document_id, document_number, customer_id, amount, reason, detail, first_seen_at, last_tried_at, invoice_id,
    waiting_on, order_key, supplier_invoice_id)
VALUES (sqlc.arg(stream), sqlc.arg(position), sqlc.arg(source_module), sqlc.arg(source_kind), sqlc.arg(source_id),
    sqlc.arg(source_part), sqlc.narg(fact_date), sqlc.narg(document_id), sqlc.narg(document_number),
    sqlc.narg(customer_id), sqlc.narg(amount)::text::numeric, sqlc.arg(reason), sqlc.arg(detail), sqlc.arg(now),
    sqlc.arg(now), sqlc.narg(invoice_id), sqlc.narg(waiting_on), sqlc.narg(order_key), sqlc.narg(supplier_invoice_id))
ON CONFLICT (source_module, source_kind, source_id, source_part) DO UPDATE
SET reason = EXCLUDED.reason, detail = EXCLUDED.detail, attempts = accounting.unposted.attempts + 1,
    last_tried_at = EXCLUDED.last_tried_at, invoice_id = coalesce(EXCLUDED.invoice_id, accounting.unposted.invoice_id),
    waiting_on = EXCLUDED.waiting_on, order_key = coalesce(EXCLUDED.order_key, accounting.unposted.order_key),
    supplier_invoice_id = coalesce(EXCLUDED.supplier_invoice_id, accounting.unposted.supplier_invoice_id)
WHERE accounting.unposted.posted_at IS NULL AND accounting.unposted.resolved_at IS NULL;

-- name: UnpostedBlocker :one
-- UnpostedBlocker is the row under Needs attention a source of the invoice
-- waits behind (R15): the earliest open row of the same invoice that is not
-- the source's own and was recorded before it — any, for a source with no
-- row yet.
SELECT u.id, u.document_number, u.source_kind FROM accounting.unposted u
WHERE u.invoice_id = sqlc.arg(invoice_id) AND u.posted_at IS NULL AND u.resolved_at IS NULL
  AND NOT (u.source_module = sqlc.arg(source_module) AND u.source_kind = sqlc.arg(source_kind)
           AND u.source_id = sqlc.arg(source_id) AND u.source_part = sqlc.arg(source_part))
  AND u.id < coalesce((SELECT o.id FROM accounting.unposted o
                       WHERE o.source_module = sqlc.arg(source_module) AND o.source_kind = sqlc.arg(source_kind)
                         AND o.source_id = sqlc.arg(source_id) AND o.source_part = sqlc.arg(source_part)),
                      9223372036854775807)
ORDER BY u.id
LIMIT 1;

-- name: UnpostedDependants :many
-- UnpostedDependants is the open rows of an invoice recorded after the row
-- after_id, in the order they were met: what waits behind it (R15).
SELECT u.id, u.stream, u.position, u.source_module, u.source_kind, u.source_id, u.source_part, u.fact_date, u.document_id,
    u.document_number, u.customer_id, coalesce(u.amount, 0)::text AS amount, u.reason, u.detail, u.attempts, u.first_seen_at,
    u.last_tried_at, u.posted_voucher_id, u.posted_at, NULL::integer AS voucher_year, NULL::integer AS voucher_number,
    u.invoice_id, u.waiting_on, u.order_key, u.informational, u.resolved_at, u.supplier_invoice_id,
    coalesce(u.nok_amounts::text, '')::text AS nok_amounts
FROM accounting.unposted u
WHERE u.invoice_id = sqlc.arg(invoice_id) AND u.posted_at IS NULL AND u.resolved_at IS NULL AND u.id > sqlc.arg(after_id)
ORDER BY u.id;

-- name: MarkUnpostedPosted :exec
-- MarkUnpostedPosted records the voucher a fact under Needs attention posted
-- as, once.
UPDATE accounting.unposted
SET posted_voucher_id = sqlc.arg(voucher_id), posted_at = sqlc.arg(now), last_tried_at = sqlc.arg(now),
    attempts = attempts + 1
WHERE source_module = sqlc.arg(source_module) AND source_kind = sqlc.arg(source_kind)
  AND source_id = sqlc.arg(source_id) AND source_part = sqlc.arg(source_part) AND posted_at IS NULL;

-- name: ListUnposted :many
SELECT u.id, u.stream, u.position, u.source_module, u.source_kind, u.source_id, u.source_part, u.fact_date, u.document_id,
    u.document_number, u.customer_id, coalesce(u.amount, 0)::text AS amount, u.reason, u.detail, u.attempts, u.first_seen_at,
    u.last_tried_at, u.posted_voucher_id, u.posted_at, v.fiscal_year AS voucher_year, v.number AS voucher_number,
    u.invoice_id, u.waiting_on, u.order_key, u.informational, u.resolved_at, u.supplier_invoice_id,
    coalesce(u.nok_amounts::text, '')::text AS nok_amounts, count(*) OVER () AS total
FROM accounting.unposted u
LEFT JOIN accounting.vouchers v ON v.id = u.posted_voucher_id
WHERE (NOT sqlc.arg(open_only)::boolean OR (u.resolved_at IS NULL AND (u.posted_at IS NULL OR u.informational)))
ORDER BY u.first_seen_at, u.id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: GetUnposted :one
SELECT u.id, u.stream, u.position, u.source_module, u.source_kind, u.source_id, u.source_part, u.fact_date, u.document_id,
    u.document_number, u.customer_id, coalesce(u.amount, 0)::text AS amount, u.reason, u.detail, u.attempts, u.first_seen_at,
    u.last_tried_at, u.posted_voucher_id, u.posted_at, v.fiscal_year AS voucher_year, v.number AS voucher_number,
    u.invoice_id, u.waiting_on, u.order_key, u.informational, u.resolved_at, u.supplier_invoice_id,
    coalesce(u.nok_amounts::text, '')::text AS nok_amounts
FROM accounting.unposted u
LEFT JOIN accounting.vouchers v ON v.id = u.posted_voucher_id
WHERE u.id = sqlc.arg(id);

-- name: CountUnposted :one
-- CountUnposted is the rows that wait: not posted and not resolved. An
-- informational row posted and is counted apart (CountInformational).
SELECT count(*)::int FROM accounting.unposted WHERE posted_at IS NULL AND resolved_at IS NULL;

-- name: CountInformational :one
SELECT count(*)::int FROM accounting.unposted WHERE informational AND resolved_at IS NULL;

-- name: InsertPostingRuleStrict :one
-- InsertPostingRuleStrict writes a person's rule; a name or match taken is
-- the unique index's violation.
INSERT INTO accounting.posting_rules (name, scope, match, line_kind, tax_category_id, expense_category_id, tax_code,
    account_id, created_at, updated_at, vat_deductible)
VALUES (sqlc.arg(name), sqlc.arg(scope), sqlc.arg(match), sqlc.narg(line_kind), sqlc.narg(tax_category_id),
    sqlc.narg(expense_category_id), sqlc.narg(tax_code), sqlc.arg(account_id), sqlc.arg(now), sqlc.arg(now),
    sqlc.narg(vat_deductible))
RETURNING id;

-- name: SourcePostings :many
-- SourcePostings is the postings of the voucher a source posted as, with
-- the voucher's id: what an undo or a removal turns back line for line
-- (R15). No rows when the source has no voucher.
SELECT v.id AS voucher_id, p.line_no, p.account_id, p.amount::text AS amount, p.tax_code_id, p.tax_role,
    coalesce(p.tax_base::text, '')::text AS tax_base, p.tax_specification, p.customer_id, p.supplier_id,
    p.open_item_id, p.project_id, p.description, p.posting_rule_id, p.posting_rule_version
FROM accounting.vouchers v
JOIN accounting.postings p ON p.voucher_id = v.id
WHERE v.source_module = sqlc.arg(source_module) AND v.source_kind = sqlc.arg(source_kind)
  AND v.source_id = sqlc.arg(source_id) AND v.source_part = ''
ORDER BY p.line_no;
