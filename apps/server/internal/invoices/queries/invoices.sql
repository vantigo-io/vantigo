-- name: GetInvoice :one
SELECT * FROM invoices.invoices WHERE id = @id;

-- name: LockInvoice :one
-- LockInvoice takes a document FOR UPDATE: a draft save, a delete and an issue
-- hold it to their commit. It is always the first lock an issue takes (D6).
SELECT * FROM invoices.invoices WHERE id = @id FOR UPDATE;

-- name: InsertInvoiceDraft :one
-- InsertInvoiceDraft writes an invoice draft (D4). It has no number, no issue
-- date and no snapshots; its totals are the ones computed with today's rates,
-- which the issue computes again for the issue date. timesheet is whether it
-- carries a timesheet (invoices work design D5). A draft in another currency
-- than NOK carries its rate, its day and its source (invoices phase 6 design
-- I19); left out, they are NOK's 1, none and nok.
INSERT INTO invoices.invoices (
    kind, customer_id, delivery_date, delivery_from, delivery_to,
    delivery_address_line1, delivery_address_line2, delivery_postal_code, delivery_city, delivery_country,
    payment_terms_days, currency, your_reference, our_reference, order_reference, note, internal_note,
    net_total, vat_total, gross_total, vat_total_nok, timesheet, created_by_user_id, created_at, updated_at,
    exchange_rate, exchange_rate_date, exchange_rate_source
) VALUES (
    'invoice', @customer_id, @delivery_date, @delivery_from, @delivery_to,
    @delivery_address_line1, @delivery_address_line2, @delivery_postal_code, @delivery_city, @delivery_country,
    @payment_terms_days, @currency, @your_reference, @our_reference, @order_reference, @note, @internal_note,
    @net_total, @vat_total, @gross_total, @vat_total_nok, @timesheet, @created_by_user_id, @now::timestamptz, @now::timestamptz,
    coalesce(sqlc.narg(exchange_rate)::numeric, 1), sqlc.narg(exchange_rate_date)::date,
    coalesce(sqlc.narg(exchange_rate_source)::text, 'nok')
)
RETURNING *;

-- name: UpdateDraft :one
-- UpdateDraft replaces a draft's own fields — its timesheet flag among them
-- (invoices work design D5) — and moves its revision on. The caller holds the
-- row (LockInvoice) and has checked it is still a draft.
UPDATE invoices.invoices SET
    customer_id = @customer_id,
    delivery_date = @delivery_date,
    delivery_from = @delivery_from,
    delivery_to = @delivery_to,
    delivery_address_line1 = @delivery_address_line1,
    delivery_address_line2 = @delivery_address_line2,
    delivery_postal_code = @delivery_postal_code,
    delivery_city = @delivery_city,
    delivery_country = @delivery_country,
    payment_terms_days = @payment_terms_days,
    your_reference = @your_reference,
    our_reference = @our_reference,
    order_reference = @order_reference,
    note = @note,
    internal_note = @internal_note,
    net_total = @net_total,
    vat_total = @vat_total,
    gross_total = @gross_total,
    vat_total_nok = @vat_total_nok,
    timesheet = @timesheet,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = @id AND status = 'draft'
RETURNING *;

-- name: SetDraftRate :one
-- SetDraftRate sets an invoice draft's currency and its rate (invoices phase
-- 6 design I19): the rate, the day it is of — none while no rate is found —
-- and its source. The caller holds the row and has checked it is a draft.
UPDATE invoices.invoices SET
    currency = @currency, exchange_rate = @exchange_rate, exchange_rate_date = sqlc.narg(exchange_rate_date)::date,
    exchange_rate_source = @exchange_rate_source
WHERE id = @id AND status = 'draft'
RETURNING *;

-- name: OpenNOK :one
-- OpenNOK is the NOK receivable still open on an invoice (invoices.open_nok,
-- plan reading 5), read under its lock.
SELECT invoices.open_nok(@id::bigint)::numeric AS open_nok;

-- name: DeleteDraft :execrows
-- DeleteDraft removes a draft and, by the cascade, its lines. An issued
-- document is never matched; the trigger would refuse it anyway (D9).
DELETE FROM invoices.invoices WHERE id = @id AND status = 'draft';

-- name: ListInvoices :many
-- ListInvoices is one page of GET /invoices (D4): drafts first, then by number
-- descending, the id breaking ties so a page never shifts under a reader.
-- search is a number (exact) or a buyer-name pattern; a draft has no buyer
-- snapshot and is found through customer_id instead. project_id is the
-- document's own derived project (invoices work design D9).
--
-- Each row carries its derived state (D3): credited (the issued credit notes'
-- gross) and paid (the live payments' sum) from one lateral join each, read
-- once per row and handed to invoices.document_state with today, the
-- business day the caller passes — never CURRENT_DATE. open_amount is gross
-- less both; the two themselves are not selected, since the list answers
-- neither. The state filter runs the same call; CountInvoices repeats the
-- joins and the predicate word for word, so the total counts what the page
-- shows.
SELECT sqlc.embed(i),
       (i.gross_total - coalesce(cr.credited, 0) - coalesce(pd.paid, 0))::numeric(14,2) AS open_amount,
       invoices.invoice_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), coalesce(pd.written_off, 0), i.due_date, @today::date)::text AS state
FROM invoices.invoices i
LEFT JOIN LATERAL (
    SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited
    FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
) cr ON true
LEFT JOIN LATERAL (
    SELECT invoices.settled_amount(i.id)::numeric(14,2) AS paid,
        invoices.loss_written_off_amount(i.id)::numeric(14,2) AS written_off
) pd ON true
WHERE (sqlc.narg(status)::text IS NULL OR i.status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR i.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(customer_id)::int IS NULL OR i.customer_id = sqlc.narg(customer_id)::int)
  AND (sqlc.narg(project_id)::int IS NULL OR i.project_id = sqlc.narg(project_id)::int)
  AND ((sqlc.narg(search_number)::bigint IS NULL AND sqlc.narg(search_pattern)::text IS NULL)
       OR i.number = sqlc.narg(search_number)::bigint
       OR i.buyer_name ILIKE sqlc.narg(search_pattern)::text)
  AND (sqlc.narg(issued_from)::date IS NULL OR i.issue_date >= sqlc.narg(issued_from)::date)
  AND (sqlc.narg(issued_to)::date IS NULL OR i.issue_date <= sqlc.narg(issued_to)::date)
  AND (sqlc.narg(state)::text IS NULL
       OR invoices.invoice_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), coalesce(pd.written_off, 0), i.due_date, @today::date) = sqlc.narg(state)::text)
ORDER BY i.number DESC NULLS FIRST, i.id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: CountInvoices :one
-- CountInvoices is ListInvoices' total, over the same filters — the state
-- filter's lateral joins included.
SELECT count(*)::int
FROM invoices.invoices i
LEFT JOIN LATERAL (
    SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited
    FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
) cr ON true
LEFT JOIN LATERAL (
    SELECT invoices.settled_amount(i.id)::numeric(14,2) AS paid,
        invoices.loss_written_off_amount(i.id)::numeric(14,2) AS written_off
) pd ON true
WHERE (sqlc.narg(status)::text IS NULL OR i.status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR i.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(customer_id)::int IS NULL OR i.customer_id = sqlc.narg(customer_id)::int)
  AND (sqlc.narg(project_id)::int IS NULL OR i.project_id = sqlc.narg(project_id)::int)
  AND ((sqlc.narg(search_number)::bigint IS NULL AND sqlc.narg(search_pattern)::text IS NULL)
       OR i.number = sqlc.narg(search_number)::bigint
       OR i.buyer_name ILIKE sqlc.narg(search_pattern)::text)
  AND (sqlc.narg(issued_from)::date IS NULL OR i.issue_date >= sqlc.narg(issued_from)::date)
  AND (sqlc.narg(issued_to)::date IS NULL OR i.issue_date <= sqlc.narg(issued_to)::date)
  AND (sqlc.narg(state)::text IS NULL
       OR invoices.invoice_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), coalesce(pd.written_off, 0), i.due_date, @today::date) = sqlc.narg(state)::text);

-- name: IssueDocument :one
-- IssueDocument turns a draft into an issued document (D6 step 6), last of the
-- issue's writes: the number, the dates, both snapshots and the totals. From
-- this row's commit on the trigger refuses every change but the merge
-- holder's customer_id and the PDF set once (D9).
UPDATE invoices.invoices SET
    status = 'issued',
    number = @number,
    issue_date = @issue_date,
    due_date = @due_date,
    -- The rate the issue resolved (invoices phase 6 design I19): an
    -- invoice's automatic rate of the issue date, its agreed one, or NOK's
    -- 1; a credit note its original's, which InsertCreditDraft copied.
    exchange_rate = @exchange_rate,
    exchange_rate_date = @exchange_rate_date,
    exchange_rate_source = @exchange_rate_source,
    buyer_customer_number = @buyer_customer_number,
    buyer_type = @buyer_type,
    buyer_name = @buyer_name,
    buyer_organisation_number = @buyer_organisation_number,
    buyer_foreign_id = @buyer_foreign_id,
    buyer_address_line1 = @buyer_address_line1,
    buyer_address_line2 = @buyer_address_line2,
    buyer_postal_code = @buyer_postal_code,
    buyer_city = @buyer_city,
    buyer_region = @buyer_region,
    buyer_country = @buyer_country,
    buyer_peppol_id = @buyer_peppol_id,
    buyer_gln = @buyer_gln,
    buyer_language = @buyer_language,
    buyer_avtalegiro_kid = sqlc.narg(buyer_avtalegiro_kid),
    seller_legal_name = @seller_legal_name,
    seller_organisation_number = @seller_organisation_number,
    seller_vat_registered = @seller_vat_registered,
    seller_in_foretaksregisteret = @seller_in_foretaksregisteret,
    seller_address_line1 = @seller_address_line1,
    seller_address_line2 = @seller_address_line2,
    seller_postal_code = @seller_postal_code,
    seller_city = @seller_city,
    seller_country = @seller_country,
    seller_bank_account = @seller_bank_account,
    seller_iban = @seller_iban,
    seller_bic = @seller_bic,
    seller_email = @seller_email,
    seller_footer_text = @seller_footer_text,
    -- The phone and the logo (invoices phase 6 design I2, I4), NULL for none.
    seller_phone = @seller_phone,
    seller_logo_object_key = @seller_logo_object_key,
    seller_logo_sha256 = @seller_logo_sha256,
    net_total = @net_total,
    vat_total = @vat_total,
    gross_total = @gross_total,
    vat_total_nok = @vat_total_nok,
    -- An invoice's KID under the agreement in force (EHF and KID design D3),
    -- NULL without one and on a credit note.
    kid = sqlc.narg(kid),
    kid_algorithm = sqlc.narg(kid_algorithm),
    issued_at = @now::timestamptz,
    issued_by_user_id = @issued_by_user_id,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = @id AND status = 'draft'
RETURNING *;

-- name: SetDocumentPDF :execrows
-- SetDocumentPDF records where an issued document's PDF is stored and the hash
-- of its bytes, once (D7): the first writer wins, and a loser of a race sees
-- no row and streams the winner's object instead. The trigger allows exactly
-- this change, from NULL, and never another (D9).
UPDATE invoices.invoices SET pdf_object_key = @pdf_object_key, pdf_sha256 = @pdf_sha256
WHERE id = @id AND status = 'issued' AND pdf_sha256 IS NULL;
