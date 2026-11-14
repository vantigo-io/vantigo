-- The bank (A26–A29, R5, R11): the bank accounts, their statements and
-- lines, the matches that reconcile a line with its postings, the bank rules,
-- the payments' bank references and the feed's lease.

-- name: ListBankAccounts :many
-- ListBankAccounts is every bank account with its ledger account, its open
-- lines and its latest statement's day.
SELECT b.*, a.number AS ledger_number, a.name AS ledger_name,
    (SELECT count(*) FROM accounting.statement_lines l WHERE l.bank_account_id = b.id AND l.state = 'open')::int AS open_lines,
    coalesce((SELECT max(s.period_to) FROM accounting.statements s WHERE s.bank_account_id = b.id)::text, '')::text AS last_statement_to
FROM accounting.bank_accounts b
JOIN accounting.accounts a ON a.id = b.ledger_account_id
ORDER BY b.active DESC, b.name, b.id;

-- name: GetBankAccount :one
SELECT b.*, a.number AS ledger_number, a.name AS ledger_name,
    (SELECT count(*) FROM accounting.statement_lines l WHERE l.bank_account_id = b.id AND l.state = 'open')::int AS open_lines,
    coalesce((SELECT max(s.period_to) FROM accounting.statements s WHERE s.bank_account_id = b.id)::text, '')::text AS last_statement_to
FROM accounting.bank_accounts b
JOIN accounting.accounts a ON a.id = b.ledger_account_id
WHERE b.id = sqlc.arg(id);

-- name: LockBankAccount :one
SELECT * FROM accounting.bank_accounts WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: BankAccountByNumber :one
SELECT * FROM accounting.bank_accounts WHERE account_number = sqlc.arg(account_number);

-- name: BankAccountByLedger :one
SELECT * FROM accounting.bank_accounts WHERE ledger_account_id = sqlc.arg(ledger_account_id);

-- name: BankLedgers :many
-- BankLedgers is each active bank account's number and ledger account: what
-- a payment from Invoices posts to by its receiving account (A26).
SELECT account_number, ledger_account_id FROM accounting.bank_accounts WHERE active;

-- name: CountBankAccounts :one
SELECT count(*)::int AS n FROM accounting.bank_accounts;

-- name: InsertBankAccount :one
INSERT INTO accounting.bank_accounts (name, account_number, iban, bic, currency, ledger_account_id, active, created_at, updated_at)
VALUES (sqlc.arg(name), sqlc.arg(account_number), sqlc.narg(iban), sqlc.narg(bic), 'NOK', sqlc.arg(ledger_account_id),
    sqlc.arg(active), sqlc.arg(now), sqlc.arg(now))
RETURNING id;

-- name: UpdateBankAccount :one
UPDATE accounting.bank_accounts
SET name = sqlc.arg(name), account_number = sqlc.arg(account_number), iban = sqlc.narg(iban), bic = sqlc.narg(bic),
    ledger_account_id = sqlc.arg(ledger_account_id), active = sqlc.arg(active), revision = revision + 1,
    updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(revision)
RETURNING id;

-- name: SetBankAccountSource :exec
-- SetBankAccountSource switches the statement's source: to the feed from a
-- start day, due now, nothing read yet; back to files, the feed's state
-- cleared.
UPDATE accounting.bank_accounts
SET source = sqlc.arg(source), feed_start_day = sqlc.narg(feed_start_day), feed_read_through = NULL,
    feed_status = sqlc.narg(feed_status), feed_detail = '', feed_next_sync_at = sqlc.narg(feed_next_sync_at), feed_lease_id = NULL, feed_lease_until = NULL,
    revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: SetBankAccountMapping :exec
UPDATE accounting.bank_accounts SET csv_mapping = sqlc.arg(csv_mapping), revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: BankAccountHasStatements :one
SELECT EXISTS (SELECT 1 FROM accounting.statements WHERE bank_account_id = sqlc.arg(id)::bigint)::boolean AS used;

-- name: DeleteBankAccount :execrows
DELETE FROM accounting.bank_accounts WHERE id = sqlc.arg(id);

-- name: InsertStatement :one
INSERT INTO accounting.statements (bank_account_id, source, statement_ref, period_from, period_to, opening_balance,
    closing_balance, continuity, continuity_detail, file_name, object_key, file_sha256, imported_at, imported_by_user_id)
VALUES (sqlc.arg(bank_account_id), sqlc.arg(source), sqlc.arg(statement_ref), sqlc.arg(period_from), sqlc.arg(period_to),
    sqlc.narg(opening_balance)::text::numeric, sqlc.narg(closing_balance)::text::numeric, sqlc.arg(continuity),
    sqlc.arg(continuity_detail), sqlc.narg(file_name), sqlc.narg(object_key), sqlc.narg(file_sha256), sqlc.arg(now),
    sqlc.narg(user_id))
RETURNING id;

-- name: SetStatementCounts :exec
UPDATE accounting.statements SET lines_imported = sqlc.arg(lines_imported), lines_skipped = sqlc.arg(lines_skipped)
WHERE id = sqlc.arg(id);

-- name: StatementOfFile :one
-- StatementOfFile is a statement this account already has from the same file
-- and statement id: an upload of a file again.
SELECT id FROM accounting.statements
WHERE bank_account_id = sqlc.arg(bank_account_id) AND file_sha256 = sqlc.arg(file_sha256)
  AND statement_ref = sqlc.arg(statement_ref)
ORDER BY id LIMIT 1;

-- name: PreviousBalancedStatement :one
-- PreviousBalancedStatement is the account's latest statement with balances
-- that starts on or before the given day: the one a new statement starting
-- that day follows on from, or overlaps.
SELECT id, period_from, period_to, closing_balance::text AS closing_balance
FROM accounting.statements
WHERE bank_account_id = sqlc.arg(bank_account_id) AND closing_balance IS NOT NULL
  AND period_from <= sqlc.arg(before)::date
ORDER BY period_to DESC, id DESC LIMIT 1;

-- name: ListStatements :many
SELECT s.id, s.bank_account_id, s.source, s.statement_ref, s.period_from, s.period_to,
    coalesce(s.opening_balance::text, '')::text AS opening_balance, coalesce(s.closing_balance::text, '')::text AS closing_balance,
    s.continuity, s.continuity_detail, s.file_name, (s.object_key IS NOT NULL)::boolean AS has_file, s.lines_imported, s.lines_skipped,
    s.imported_at, s.imported_by_user_id
FROM accounting.statements s
WHERE (sqlc.narg(bank_account_id)::bigint IS NULL OR s.bank_account_id = sqlc.narg(bank_account_id)::bigint)
ORDER BY s.period_to DESC, s.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetStatement :one
SELECT * FROM accounting.statements WHERE id = sqlc.arg(id);

-- name: InsertStatementLine :one
-- InsertStatementLine writes a line unless the duplicate guard holds it: its
-- bank id, or its fingerprint and ordinal, already on the account. id is 0
-- when it was skipped.
WITH ins AS (
    INSERT INTO accounting.statement_lines (statement_id, bank_account_id, booked_on, value_on, amount, text, reference,
        kid, counterparty_name, counterparty_account, bank_id, fingerprint, ordinal, kind, reversal, batch, tx_reference,
        possible_duplicate_of, created_at)
    VALUES (sqlc.arg(statement_id), sqlc.arg(bank_account_id), sqlc.arg(booked_on), sqlc.narg(value_on),
        sqlc.arg(amount)::text::numeric, sqlc.arg(text), sqlc.arg(reference), sqlc.narg(kid), sqlc.arg(counterparty_name),
        sqlc.arg(counterparty_account), sqlc.narg(bank_id), sqlc.arg(fingerprint), sqlc.arg(ordinal), sqlc.arg(kind),
        sqlc.arg(reversal), sqlc.narg(batch), sqlc.arg(tx_reference), sqlc.narg(possible_duplicate_of), sqlc.arg(now))
    ON CONFLICT DO NOTHING
    RETURNING id
)
SELECT coalesce((SELECT id FROM ins), 0)::bigint AS id;

-- name: ListStatementLines :many
-- ListStatementLines is an account's lines booked in the period, by day, with
-- the standing match of each.
SELECT l.id, l.statement_id, l.bank_account_id, l.booked_on, l.value_on, l.amount::text AS amount, l.text, l.reference,
    l.kid, l.counterparty_name, l.counterparty_account, l.bank_id, l.kind, l.reversal, l.batch, l.state,
    l.tx_reference, l.possible_duplicate_of, l.note, s.source AS statement_source,
    m.id AS match_id, m.kind AS match_kind, m.reason AS match_reason, m.voucher_id AS match_voucher_id,
    m.matched_at AS matched_at, m.matched_by_user_id AS matched_by_user_id, count(*) OVER () AS total
FROM accounting.statement_lines l
JOIN accounting.statements s ON s.id = l.statement_id
LEFT JOIN accounting.statement_matches m ON m.statement_line_id = l.id AND m.undone_at IS NULL
WHERE l.bank_account_id = sqlc.arg(bank_account_id)
  AND l.booked_on BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(to_day)::date
  AND (sqlc.narg(state)::text IS NULL OR l.state = sqlc.narg(state)::text)
ORDER BY l.booked_on, l.id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: GetStatementLine :one
SELECT l.id, l.statement_id, l.bank_account_id, l.booked_on, l.value_on, l.amount::text AS amount, l.text, l.reference,
    l.kid, l.counterparty_name, l.counterparty_account, l.bank_id, l.kind, l.reversal, l.batch, l.state,
    l.tx_reference, l.possible_duplicate_of, l.note, s.source AS statement_source
FROM accounting.statement_lines l
JOIN accounting.statements s ON s.id = l.statement_id
WHERE l.id = sqlc.arg(id);

-- name: LockStatementLine :one
SELECT l.id FROM accounting.statement_lines l WHERE l.id = sqlc.arg(id) FOR UPDATE;

-- name: SetStatementLineState :exec
UPDATE accounting.statement_lines SET state = sqlc.arg(state) WHERE id = sqlc.arg(id);

-- name: OpenLinesNear :many
-- OpenLinesNear is the open lines of the other bank accounts with the
-- opposite amount booked within the window: the other side of a transfer.
SELECT l.id, l.bank_account_id, l.booked_on, l.amount::text AS amount, b.ledger_account_id, b.name AS bank_account_name
FROM accounting.statement_lines l
JOIN accounting.bank_accounts b ON b.id = l.bank_account_id
WHERE l.bank_account_id <> sqlc.arg(bank_account_id) AND l.state = 'open' AND l.possible_duplicate_of IS NULL
  AND l.amount = sqlc.arg(amount)::text::numeric
  AND l.booked_on BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(to_day)::date
ORDER BY abs(l.booked_on - sqlc.arg(near)::date), l.id
LIMIT 5;

-- name: StandingMatchOfLine :one
SELECT * FROM accounting.statement_matches WHERE statement_line_id = sqlc.arg(statement_line_id) AND undone_at IS NULL;

-- name: GetMatch :one
SELECT * FROM accounting.statement_matches WHERE id = sqlc.arg(id);

-- name: StandingMatchesOfVoucher :many
SELECT * FROM accounting.statement_matches WHERE voucher_id = sqlc.arg(voucher_id)::bigint AND undone_at IS NULL ORDER BY id;

-- name: InsertMatch :one
INSERT INTO accounting.statement_matches (statement_line_id, kind, request_key, reason, voucher_id, supplier_payment_id,
    bank_rule_id, bank_rule_version, vat_closing_id, vipps_through, matched_at, matched_by_user_id)
VALUES (sqlc.arg(statement_line_id), sqlc.arg(kind), sqlc.arg(request_key), sqlc.arg(reason), sqlc.narg(voucher_id),
    sqlc.narg(supplier_payment_id), sqlc.narg(bank_rule_id), sqlc.narg(bank_rule_version), sqlc.narg(vat_closing_id),
    sqlc.narg(vipps_through), sqlc.arg(now), sqlc.arg(user_id))
RETURNING id;

-- name: InsertMatchPosting :exec
INSERT INTO accounting.statement_match_postings (match_id, posting_id) VALUES (sqlc.arg(match_id), sqlc.arg(posting_id));

-- name: UndoMatch :exec
UPDATE accounting.statement_matches
SET undone_at = sqlc.arg(now), undone_by_user_id = sqlc.arg(user_id), undo_voucher_id = sqlc.narg(undo_voucher_id)
WHERE id = sqlc.arg(id) AND undone_at IS NULL;

-- name: UnstandMatchPostings :exec
UPDATE accounting.statement_match_postings SET standing = false WHERE match_id = sqlc.arg(match_id);

-- name: MatchPostings :many
-- MatchPostings is the postings of the standing matches named, with their
-- vouchers.
SELECT mp.match_id, p.id AS posting_id, p.amount::text AS amount, v.id AS voucher_id, v.fiscal_year, v.number,
    v.voucher_date, v.description, v.kind
FROM accounting.statement_match_postings mp
JOIN accounting.postings p ON p.id = mp.posting_id
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE mp.match_id = ANY(sqlc.arg(match_ids)::bigint[])
ORDER BY mp.match_id, v.voucher_date, p.id;

-- name: UnmatchedPostings :many
-- UnmatchedPostings is the postings on a ledger account dated in the window
-- that no standing match holds, with their vouchers and the bank reference
-- of a payment from Invoices. A reversed voucher's postings and a reversal's
-- are left out: they cancel each other; so are a payment removed in Invoices
-- and its removal.
SELECT p.id, p.amount::text AS amount, p.description AS posting_text, v.id AS voucher_id, v.fiscal_year, v.number,
    v.voucher_date, v.description, v.kind, v.rule, v.source_module, v.source_kind, v.source_id,
    r.reference AS bank_reference, r.source AS bank_source, r.bank_account AS bank_account
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
LEFT JOIN accounting.voucher_bank_refs r ON r.voucher_id = v.id
WHERE p.account_id = sqlc.arg(account_id)
  AND v.voucher_date BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(to_day)::date
  AND v.reverses_voucher_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE rv.reverses_voucher_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE v.source_kind IS NOT NULL AND rv.source_module = 'invoices' AND v.source_module = 'invoices'
      AND rv.source_kind = v.source_kind || '_removed' AND rv.source_id = v.source_id)
  AND NOT (v.source_kind LIKE '%\_removed' AND EXISTS (SELECT 1 FROM accounting.vouchers ov WHERE ov.source_module = 'invoices' AND v.source_module = 'invoices'
      AND ov.source_kind = left(v.source_kind, -8) AND ov.source_id = v.source_id))
  AND NOT EXISTS (SELECT 1 FROM accounting.statement_match_postings mp WHERE mp.posting_id = p.id AND mp.standing)
ORDER BY v.voucher_date, p.id
LIMIT 2000;

-- name: CountUnmatchedPostings :one
SELECT count(*)::int AS n
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE p.account_id = sqlc.arg(account_id) AND v.voucher_date <= sqlc.arg(through)::date
  AND v.voucher_date >= sqlc.arg(since)::date
  AND v.reverses_voucher_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE rv.reverses_voucher_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE v.source_kind IS NOT NULL AND rv.source_module = 'invoices' AND v.source_module = 'invoices'
      AND rv.source_kind = v.source_kind || '_removed' AND rv.source_id = v.source_id)
  AND NOT (v.source_kind LIKE '%\_removed' AND EXISTS (SELECT 1 FROM accounting.vouchers ov WHERE ov.source_module = 'invoices' AND v.source_module = 'invoices'
      AND ov.source_kind = left(v.source_kind, -8) AND ov.source_id = v.source_id))
  AND NOT EXISTS (SELECT 1 FROM accounting.statement_match_postings mp WHERE mp.posting_id = p.id AND mp.standing);

-- name: PostingsOnAccount :many
-- PostingsOnAccount is a voucher's postings on one account.
SELECT id, amount::text AS amount FROM accounting.postings
WHERE voucher_id = sqlc.arg(voucher_id) AND account_id = sqlc.arg(account_id) ORDER BY line_no;

-- name: LockPostings :many
-- LockPostings takes the named postings' match rows' place: the postings
-- FOR SHARE, so an accept and a voucher's reversal are ordered.
SELECT p.id, p.account_id, p.amount::text AS amount, p.voucher_id FROM accounting.postings p
WHERE p.id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY p.id FOR SHARE;

-- name: AccountBalanceThrough :one
SELECT coalesce(sum(p.amount), 0)::text AS balance
FROM accounting.postings p JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE p.account_id = sqlc.arg(account_id) AND v.voucher_date <= sqlc.arg(through)::date;

-- name: StatementBalanceAt :one
-- StatementBalanceAt is the account's balance by its statements at the end of
-- a day: the latest closing balance given on or before it, and every line
-- booked after that statement's period through the day. has_balance is false
-- when no statement gave one.
WITH base AS (
    SELECT s.period_to, s.closing_balance FROM accounting.statements s
    WHERE s.bank_account_id = sqlc.arg(bank_account_id) AND s.closing_balance IS NOT NULL
      AND s.period_to <= sqlc.arg(through)::date
    ORDER BY s.period_to DESC, s.id DESC LIMIT 1
)
SELECT EXISTS (SELECT 1 FROM base)::boolean AS has_balance,
    (coalesce((SELECT closing_balance FROM base), 0) + coalesce((SELECT sum(l.amount) FROM accounting.statement_lines l
        WHERE l.bank_account_id = sqlc.arg(bank_account_id) AND l.booked_on <= sqlc.arg(through)::date AND l.state <> 'duplicate'
          AND l.booked_on > coalesce((SELECT period_to FROM base), '-infinity'::date)), 0))::text AS balance;

-- name: OpenLinesThrough :one
SELECT count(*)::int AS n FROM accounting.statement_lines
WHERE bank_account_id = sqlc.arg(bank_account_id) AND state = 'open' AND booked_on <= sqlc.arg(through)::date;

-- name: OpenSupplierInvoices :many
-- OpenSupplierInvoices is the posted supplier invoices with money left owed,
-- with the supplier's bank account: what an outgoing line may pay.
SELECT si.id, si.supplier_id, si.supplier_number, si.kid, si.reference, si.currency, si.exchange_rate::text AS exchange_rate,
    si.due_date, si.open_item_id,
    s.name AS supplier_name, s.bank_account AS supplier_bank_account, b.balance::text AS balance
FROM accounting.supplier_invoices si
JOIN accounting.suppliers s ON s.id = si.supplier_id
CROSS JOIN LATERAL (SELECT coalesce(sum(p.amount), 0) AS balance FROM accounting.postings p
    WHERE p.open_item_id = si.open_item_id) b
WHERE si.state = 'posted' AND si.kind = 'invoice' AND si.open_item_id IS NOT NULL AND b.balance < 0
ORDER BY si.due_date NULLS LAST, si.id
LIMIT 500;

-- name: ClosedTermSettlements :many
-- ClosedTermSettlements is each standing closing's settlement voucher's
-- posting on the VAT settlement account — what a term's payment or refund
-- is — that no standing bank match has paid.
SELECT t.year, t.term, t.period_end, c.id AS closing_id, p.amount::text AS amount
FROM accounting.vat_terms t
JOIN accounting.vat_term_closings c ON c.id = t.current_closing_id
JOIN accounting.postings p ON p.voucher_id = c.settlement_voucher_id
WHERE p.account_id = sqlc.arg(account_id)
  AND NOT EXISTS (SELECT 1 FROM accounting.statement_matches m WHERE m.vat_closing_id = c.id AND m.undone_at IS NULL)
ORDER BY t.period_end DESC
LIMIT 12;

-- name: ListBankRules :many
SELECT r.*, a.number AS account_number, a.name AS account_name, tc.code AS tax_code
FROM accounting.bank_rules r
JOIN accounting.accounts a ON a.id = r.account_id
LEFT JOIN accounting.tax_codes tc ON tc.id = r.tax_code_id
ORDER BY r.active DESC, r.name, r.id;

-- name: GetBankRule :one
SELECT * FROM accounting.bank_rules WHERE id = sqlc.arg(id);

-- name: InsertBankRule :one
INSERT INTO accounting.bank_rules (name, bank_account_id, text_contains, counterparty_contains, direction, account_id,
    tax_code_id, project_id, description, active, created_at, updated_at)
VALUES (sqlc.arg(name), sqlc.narg(bank_account_id), sqlc.narg(text_contains), sqlc.narg(counterparty_contains),
    sqlc.arg(direction), sqlc.arg(account_id), sqlc.narg(tax_code_id), sqlc.narg(project_id), sqlc.arg(description),
    sqlc.arg(active), sqlc.arg(now), sqlc.arg(now))
RETURNING id;

-- name: UpdateBankRule :execrows
UPDATE accounting.bank_rules
SET name = sqlc.arg(name), bank_account_id = sqlc.narg(bank_account_id), text_contains = sqlc.narg(text_contains),
    counterparty_contains = sqlc.narg(counterparty_contains), direction = sqlc.arg(direction),
    account_id = sqlc.arg(account_id), tax_code_id = sqlc.narg(tax_code_id), project_id = sqlc.narg(project_id),
    description = sqlc.arg(description), active = sqlc.arg(active), version = version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: BankRuleUsed :one
SELECT EXISTS (SELECT 1 FROM accounting.statement_matches WHERE bank_rule_id = sqlc.arg(id)::bigint)::boolean AS used;

-- name: DeleteBankRule :execrows
DELETE FROM accounting.bank_rules WHERE id = sqlc.arg(id);

-- name: InsertVoucherBankRef :exec
INSERT INTO accounting.voucher_bank_refs (voucher_id, bank_account, reference, source)
VALUES (sqlc.arg(voucher_id), sqlc.arg(bank_account), sqlc.arg(reference), sqlc.arg(source));

-- name: ClaimFeedAccount :one
-- ClaimFeedAccount takes the lease of one bank account the feed reads that is
-- due and not held: the oldest due first.
UPDATE accounting.bank_accounts b
SET feed_lease_id = sqlc.arg(lease_id), feed_lease_until = sqlc.arg(lease_until)
WHERE b.id = (
    SELECT c.id FROM accounting.bank_accounts c
    WHERE c.source = 'feed' AND c.active
      AND (c.feed_next_sync_at IS NULL OR c.feed_next_sync_at <= sqlc.arg(now)::timestamptz)
      AND (c.feed_lease_until IS NULL OR c.feed_lease_until < sqlc.arg(now)::timestamptz)
    ORDER BY c.feed_next_sync_at NULLS FIRST, c.id
    LIMIT 1 FOR UPDATE SKIP LOCKED)
RETURNING b.id, b.account_number, b.feed_start_day, b.feed_read_through, b.feed_status, b.feed_detail;

-- name: AdvanceFeedRead :execrows
-- AdvanceFeedRead moves the read cursor to @read_through once a window is
-- read to its end and written — never back — only while the lease is the
-- claim's; no row is a lease lost.
UPDATE accounting.bank_accounts
SET feed_read_through = greatest(coalesce(feed_read_through, sqlc.arg(read_through)::date), sqlc.arg(read_through)::date)
WHERE id = sqlc.arg(id) AND feed_lease_id = sqlc.arg(lease_id) AND source = 'feed';

-- name: HoldsFeedLease :one
-- HoldsFeedLease locks the account while its lease is still this claim's.
SELECT id FROM accounting.bank_accounts
WHERE id = sqlc.arg(id) AND feed_lease_id = sqlc.arg(lease_id) AND source = 'feed'
FOR UPDATE;

-- name: ReleaseFeedAccount :exec
-- ReleaseFeedAccount lets a claim go, saying how the read ended and when the
-- account is due again; last_synced_at moves only on a read that succeeded.
UPDATE accounting.bank_accounts
SET feed_lease_id = NULL, feed_lease_until = NULL, feed_status = sqlc.arg(feed_status), feed_detail = sqlc.arg(feed_detail),
    feed_next_sync_at = sqlc.arg(next_sync_at),
    feed_last_synced_at = CASE WHEN sqlc.arg(synced)::boolean THEN sqlc.arg(now)::timestamptz ELSE feed_last_synced_at END
WHERE id = sqlc.arg(id) AND feed_lease_id = sqlc.arg(lease_id);

-- name: FeedAttention :one
-- FeedAttention counts the bank accounts whose feed is in trouble.
SELECT count(*)::int AS n FROM accounting.bank_accounts
WHERE source = 'feed' AND active AND feed_status IN ('unauthorized', 'failing', 'not_offered', 'off', 'too_large');

-- name: OpenLinesCount :one
SELECT count(*)::int AS n FROM accounting.statement_lines l
JOIN accounting.bank_accounts b ON b.id = l.bank_account_id
WHERE l.state = 'open' AND b.active;

-- name: BankRulesOfAccount :one
SELECT EXISTS (SELECT 1 FROM accounting.bank_rules WHERE bank_account_id = sqlc.narg(id)::bigint)::boolean AS used;

-- name: SupplierPaymentInvoice :one
SELECT supplier_invoice_id FROM accounting.supplier_payments WHERE id = sqlc.arg(id);

-- name: ExistingBankIDs :many
-- ExistingBankIDs is which of the bank's ids the account has a line of.
SELECT bank_id::text AS bank_id FROM accounting.statement_lines
WHERE bank_account_id = sqlc.arg(bank_account_id) AND bank_id = ANY(sqlc.arg(ids)::text[]);

-- name: StandingMatchesOnVoucher :many
-- StandingMatchesOnVoucher is every standing match that holds a posting of
-- the voucher, or that posted it.
SELECT DISTINCT m.* FROM accounting.statement_matches m
WHERE m.undone_at IS NULL AND (m.voucher_id = sqlc.arg(voucher_id)::bigint OR EXISTS (
    SELECT 1 FROM accounting.statement_match_postings mp JOIN accounting.postings p ON p.id = mp.posting_id
    WHERE mp.match_id = m.id AND mp.standing AND p.voucher_id = sqlc.arg(voucher_id)::bigint))
ORDER BY m.statement_line_id;

-- name: LockVouchers :many
-- LockVouchers takes the vouchers FOR UPDATE, in id order: what an accept of
-- their postings and a reversal of one of them both take, so the two are
-- ordered.
SELECT id FROM accounting.vouchers WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY id FOR UPDATE;

-- name: VouchersTurnedBack :one
-- VouchersTurnedBack counts the vouchers named that a voucher reverses, or
-- that a removal in Invoices turns back.
SELECT count(*)::int AS n FROM accounting.vouchers v
WHERE v.id = ANY(sqlc.arg(ids)::bigint[]) AND (
    EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE rv.reverses_voucher_id = v.id)
    OR EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE v.source_kind IS NOT NULL AND rv.source_module = 'invoices' AND v.source_module = 'invoices'
        AND rv.source_kind = v.source_kind || '_removed' AND rv.source_id = v.source_id));

-- name: SetMatchUndoVoucher :exec
UPDATE accounting.statement_matches SET undo_voucher_id = sqlc.arg(undo_voucher_id)::bigint WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: SetStatementLineNote :exec
UPDATE accounting.statement_lines SET note = sqlc.arg(note) WHERE id = sqlc.arg(id);

-- name: BankAccountInUse :one
-- BankAccountInUse is whether the account has statement lines, or a payment
-- was posted through its number.
SELECT (EXISTS (SELECT 1 FROM accounting.statement_lines WHERE bank_account_id = sqlc.arg(id)::bigint)
    OR EXISTS (SELECT 1 FROM accounting.voucher_bank_refs WHERE bank_account = sqlc.arg(account_number)::text))::boolean AS used;

-- name: LatestCovered :one
-- LatestCovered is the last day the account's statements cover: its latest
-- line's day or statement's end, '' with none.
SELECT coalesce(greatest(
    (SELECT max(booked_on) FROM accounting.statement_lines WHERE bank_account_id = sqlc.arg(id)::bigint),
    (SELECT max(period_to) FROM accounting.statements WHERE bank_account_id = sqlc.arg(id)::bigint))::text, '')::text AS day;

-- name: ProbableDuplicate :one
-- ProbableDuplicate is an existing line of the account from another source
-- on the same day of the same amount with the same KID, counterparty account
-- or text: the same movement read twice, perhaps.
SELECT l.id FROM accounting.statement_lines l
JOIN accounting.statements s ON s.id = l.statement_id
WHERE l.bank_account_id = sqlc.arg(bank_account_id) AND l.booked_on = sqlc.arg(booked_on)
  AND l.amount = sqlc.arg(amount)::text::numeric AND s.source <> sqlc.arg(source)::text AND l.state <> 'duplicate'
  AND ((sqlc.arg(kid)::text <> '' AND l.kid = sqlc.arg(kid)::text)
    OR (sqlc.arg(counterparty_account)::text <> '' AND l.counterparty_account = sqlc.arg(counterparty_account)::text)
    OR (sqlc.arg(text_key)::text <> '' AND lower(regexp_replace(l.text, '[^[:alnum:]]+', '', 'g')) = sqlc.arg(text_key)::text))
ORDER BY l.id LIMIT 1;

-- name: ResolveDuplicate :exec
-- ResolveDuplicate settles a possible duplicate: the same movement (state
-- duplicate) or another (the flag cleared).
UPDATE accounting.statement_lines
SET state = CASE WHEN sqlc.arg(duplicate)::boolean THEN 'duplicate' ELSE state END,
    possible_duplicate_of = CASE WHEN sqlc.arg(duplicate)::boolean THEN possible_duplicate_of ELSE NULL END
WHERE id = sqlc.arg(id);

-- name: VippsSettledThrough :one
-- VippsSettledThrough is the last settlement day a standing Vipps payout was
-- read through, '' with none.
SELECT coalesce(max(vipps_through)::text, '')::text AS day FROM accounting.statement_matches
WHERE kind = 'vipps_payout' AND undone_at IS NULL;

-- name: ClearingDays :many
-- ClearingDays is the money each day brought onto the clearing account — its
-- debits, reversed vouchers and reversals left out — after one day and before
-- another.
SELECT v.voucher_date AS day, sum(p.amount)::text AS amount
FROM accounting.postings p JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE p.account_id = sqlc.arg(account_id) AND p.amount > 0
  AND v.voucher_date > sqlc.arg(after)::date AND v.voucher_date < sqlc.arg(before)::date
  AND v.reverses_voucher_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE rv.reverses_voucher_id = v.id)
GROUP BY v.voucher_date ORDER BY v.voucher_date;

-- name: CustomerItemOfAmount :one
-- CustomerItemOfAmount is whether a customer's open item is owed exactly the
-- amount.
SELECT EXISTS (SELECT 1 FROM accounting.open_items o
    WHERE o.kind = 'customer' AND (SELECT coalesce(sum(p.amount), 0) FROM accounting.postings p WHERE p.open_item_id = o.id)
        = sqlc.arg(amount)::text::numeric)::boolean AS found;

-- name: UnmatchedPostingsPage :many
-- UnmatchedPostingsPage is the first of the ledger account's postings dated
-- in the period that no standing match holds.
SELECT p.id, p.amount::text AS amount, p.description AS posting_text, v.id AS voucher_id, v.fiscal_year, v.number,
    v.voucher_date, v.description, v.kind
FROM accounting.postings p
JOIN accounting.vouchers v ON v.id = p.voucher_id
WHERE p.account_id = sqlc.arg(account_id)
  AND v.voucher_date BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(to_day)::date
  AND v.reverses_voucher_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE rv.reverses_voucher_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE v.source_kind IS NOT NULL AND rv.source_module = 'invoices' AND v.source_module = 'invoices'
      AND rv.source_kind = v.source_kind || '_removed' AND rv.source_id = v.source_id)
  AND NOT (v.source_kind LIKE '%\_removed' AND EXISTS (SELECT 1 FROM accounting.vouchers ov WHERE ov.source_module = 'invoices' AND v.source_module = 'invoices'
      AND ov.source_kind = left(v.source_kind, -8) AND ov.source_id = v.source_id))
  AND NOT EXISTS (SELECT 1 FROM accounting.statement_match_postings mp WHERE mp.posting_id = p.id AND mp.standing)
ORDER BY v.voucher_date, p.id
LIMIT sqlc.arg(row_limit);
