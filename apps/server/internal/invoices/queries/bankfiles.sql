-- The bank import (invoices payments and reminders design D3): the accounts a
-- file may name, the file read twice for an earlier import, the file row and
-- its lines inserted once, the reads of the files and the accounts, and an
-- account's format changed with its cutover. Every time is a parameter, never
-- now().

-- name: KnownSellerAccounts :many
-- KnownSellerAccounts is every account a bank file may name (D3 step 4): the
-- seller's bank_account as the settings hold it, and every account an issued
-- document printed — an older one the seller has since changed included.
SELECT bank_account::text AS account FROM invoices.settings WHERE bank_account <> ''
UNION
SELECT DISTINCT seller_bank_account::text FROM invoices.invoices
WHERE status = 'issued' AND seller_bank_account IS NOT NULL AND seller_bank_account <> '';

-- name: BankFileBySHA :one
-- BankFileBySHA is the earlier import of the same bytes, with its uploader
-- and time (D3 step 5's quick answer, read on the pool).
SELECT id, uploaded_at, uploaded_by_user_id FROM invoices.bank_files WHERE sha256 = @sha256;

-- name: BankFileByIdentity :one
-- BankFileByIdentity is the earlier import of the same file identity in the
-- same format — OCR "sender:transmission:recipient", camt "MsgId|CreDtTm" —
-- with its uploader and time.
SELECT id, uploaded_at, uploaded_by_user_id FROM invoices.bank_files
WHERE format = @format AND file_identity = @file_identity;

-- name: InsertBankFile :one
-- InsertBankFile is the file row (D3 step 7.2), after the accounts' locks;
-- duplicates stays NULL until SetBankFileDuplicates, in the same
-- transaction (plan reading 3). uq_bank_files_sha256 and
-- uq_bank_files_identity refuse an import the pool read did not see.
INSERT INTO invoices.bank_files (
    format, sha256, file_identity, object_key, byte_size, accounts, first_booked_on, last_booked_on,
    transactions, ignored, ignored_kinds, uploaded_by_user_id, uploaded_at
) VALUES (
    @format, @sha256, @file_identity, @object_key, @byte_size, sqlc.arg(accounts)::text[], @first_booked_on, @last_booked_on,
    @transactions, @ignored, @ignored_kinds, @uploaded_by_user_id, @uploaded_at::timestamptz
)
RETURNING *;

-- name: InsertBankTransactions :many
-- InsertBankTransactions is every line of a file in one statement, ordered by
-- fingerprint, against ux_bank_transactions_fingerprint (D3 step 7.3): two
-- overlapping imports wait on the index in the same order and never
-- deadlock. A line whose account and fingerprint a live line already has is
-- skipped and not returned; InsertDuplicateTransactions keeps it. The
-- arrays are parallel, one element per line, unnested side by side; an
-- empty kid is NULL.
INSERT INTO invoices.bank_transactions (
    bank_file_id, line_ref, format, account, direction, negative, booked_on, value_on, ordered_on,
    amount, currency, kid, remittance_text, debtor_name, debtor_account, archive_ref, bank_code,
    fingerprint, ordinal
)
SELECT @bank_file_id::bigint, t.line_ref, @format::text, t.account, t.direction, t.negative, t.booked_on, t.value_on, t.ordered_on,
    t.amount, 'NOK', NULLIF(t.kid, ''), t.remittance_text, t.debtor_name, t.debtor_account, t.archive_ref, t.bank_code,
    t.fingerprint, t.ordinal
FROM (
    SELECT unnest(sqlc.arg(line_refs)::text[]) AS line_ref, unnest(sqlc.arg(accounts)::text[]) AS account,
        unnest(sqlc.arg(directions)::text[]) AS direction, unnest(sqlc.arg(negatives)::boolean[]) AS negative,
        unnest(sqlc.arg(booked_ons)::date[]) AS booked_on, unnest(sqlc.arg(value_ons)::date[]) AS value_on,
        unnest(sqlc.arg(ordered_ons)::date[]) AS ordered_on, unnest(sqlc.arg(amounts)::numeric[]) AS amount,
        unnest(sqlc.arg(kids)::text[]) AS kid, unnest(sqlc.arg(remittance_texts)::text[]) AS remittance_text,
        unnest(sqlc.arg(debtor_names)::text[]) AS debtor_name, unnest(sqlc.arg(debtor_accounts)::text[]) AS debtor_account,
        unnest(sqlc.arg(archive_refs)::text[]) AS archive_ref, unnest(sqlc.arg(bank_codes)::text[]) AS bank_code,
        unnest(sqlc.arg(fingerprints)::text[]) AS fingerprint, unnest(sqlc.arg(ordinals)::smallint[]) AS ordinal
) AS t
ORDER BY t.fingerprint
ON CONFLICT (account, fingerprint) WHERE duplicate_of_id IS NULL DO NOTHING
RETURNING id, fingerprint;

-- name: InsertDuplicateTransactions :many
-- InsertDuplicateTransactions keeps the lines InsertBankTransactions skipped
-- (D3 step 7.4): each a duplicate row linked to the live line of its account
-- and fingerprint — committed by an earlier import, or by an overlapping one
-- this import waited for — ordered by fingerprint. The fingerprint index
-- leaves them out for good (NI2). The arrays are InsertBankTransactions'.
INSERT INTO invoices.bank_transactions (
    bank_file_id, line_ref, format, account, direction, negative, booked_on, value_on, ordered_on,
    amount, currency, kid, remittance_text, debtor_name, debtor_account, archive_ref, bank_code,
    fingerprint, ordinal, duplicate_of_id, status
)
SELECT @bank_file_id::bigint, t.line_ref, @format::text, t.account, t.direction, t.negative, t.booked_on, t.value_on, t.ordered_on,
    t.amount, 'NOK', NULLIF(t.kid, ''), t.remittance_text, t.debtor_name, t.debtor_account, t.archive_ref, t.bank_code,
    t.fingerprint, t.ordinal, live.id, 'duplicate'
FROM (
    SELECT unnest(sqlc.arg(line_refs)::text[]) AS line_ref, unnest(sqlc.arg(accounts)::text[]) AS account,
        unnest(sqlc.arg(directions)::text[]) AS direction, unnest(sqlc.arg(negatives)::boolean[]) AS negative,
        unnest(sqlc.arg(booked_ons)::date[]) AS booked_on, unnest(sqlc.arg(value_ons)::date[]) AS value_on,
        unnest(sqlc.arg(ordered_ons)::date[]) AS ordered_on, unnest(sqlc.arg(amounts)::numeric[]) AS amount,
        unnest(sqlc.arg(kids)::text[]) AS kid, unnest(sqlc.arg(remittance_texts)::text[]) AS remittance_text,
        unnest(sqlc.arg(debtor_names)::text[]) AS debtor_name, unnest(sqlc.arg(debtor_accounts)::text[]) AS debtor_account,
        unnest(sqlc.arg(archive_refs)::text[]) AS archive_ref, unnest(sqlc.arg(bank_codes)::text[]) AS bank_code,
        unnest(sqlc.arg(fingerprints)::text[]) AS fingerprint, unnest(sqlc.arg(ordinals)::smallint[]) AS ordinal
) AS t
JOIN invoices.bank_transactions live
    ON live.account = t.account AND live.fingerprint = t.fingerprint AND live.duplicate_of_id IS NULL
ORDER BY t.fingerprint
RETURNING id;

-- name: SetBankFileDuplicates :execrows
-- SetBankFileDuplicates sets the file's duplicates once, from NULL, inside
-- the import's own transaction, so no reader ever sees it NULL
-- (tr_bank_files_immutable allows exactly that write).
UPDATE invoices.bank_files SET duplicates = @duplicates::integer
WHERE id = @id AND duplicates IS NULL;

-- name: CountBankFiles :one
-- CountBankFiles is how many files GET /bank-files pages over.
SELECT count(*) FROM invoices.bank_files;

-- name: ListBankFiles :many
-- ListBankFiles is one page of the imported files, newest first, the id
-- breaking ties, each with its lines counted by status.
SELECT sqlc.embed(f), c.pending, c.exceptions, c.matched
FROM invoices.bank_files f
CROSS JOIN LATERAL (
    SELECT count(*) FILTER (WHERE t.status = 'pending')   AS pending,
           count(*) FILTER (WHERE t.status = 'exception') AS exceptions,
           count(*) FILTER (WHERE t.status = 'matched')   AS matched
    FROM invoices.bank_transactions t WHERE t.bank_file_id = f.id
) c
ORDER BY f.uploaded_at DESC, f.id DESC
LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: GetBankFile :one
-- GetBankFile is one imported file with its lines counted by status.
SELECT sqlc.embed(f), c.pending, c.exceptions, c.matched
FROM invoices.bank_files f
CROSS JOIN LATERAL (
    SELECT count(*) FILTER (WHERE t.status = 'pending')   AS pending,
           count(*) FILTER (WHERE t.status = 'exception') AS exceptions,
           count(*) FILTER (WHERE t.status = 'matched')   AS matched
    FROM invoices.bank_transactions t WHERE t.bank_file_id = f.id
) c
WHERE f.id = @id;

-- name: TransactionsOfFile :many
-- TransactionsOfFile is every line a file brought, duplicates included, in
-- the order they were inserted.
SELECT * FROM invoices.bank_transactions WHERE bank_file_id = @bank_file_id ORDER BY id;

-- name: ListImportAccounts :many
-- ListImportAccounts is every account a file was imported for — or the one
-- named, when account is given — with its format and cutover, its latest
-- file and the latest booking day of its own lines (D3), in account order. Every account row is written by an import
-- beside its file, so a latest file always exists; were one ever missing,
-- last_file_id is 0 and last_uploaded_at the account's set_at, which the
-- handler reads as none.
SELECT a.*, coalesce(lf.id, 0)::bigint AS last_file_id,
    coalesce(lf.uploaded_at, a.set_at)::timestamptz AS last_uploaded_at, lb.last_booked_on
FROM invoices.bank_import_accounts a
LEFT JOIN LATERAL (
    SELECT f.id, f.uploaded_at FROM invoices.bank_files f
    WHERE a.account = ANY (f.accounts)
    ORDER BY f.uploaded_at DESC, f.id DESC
    LIMIT 1
) lf ON true
CROSS JOIN LATERAL (
    SELECT max(t.booked_on)::date AS last_booked_on FROM invoices.bank_transactions t WHERE t.account = a.account
) lb
WHERE sqlc.narg(account)::text IS NULL OR a.account = sqlc.narg(account)::text
ORDER BY a.account;

-- name: LatestBookedInFormat :one
-- LatestBookedInFormat is the latest booking day of the account's own lines
-- in format — the cutover a format change records (D3 as amended, plan
-- reading 51): a file's last_booked_on spans its other accounts too. NULL
-- when the account has no line in it.
SELECT max(booked_on)::date AS latest FROM invoices.bank_transactions
WHERE account = @account AND format = @format;

-- name: ChangeImportFormat :one
-- ChangeImportFormat is the format PUT's write, under LockImportAccount: the
-- old format kept as previous_format with its cutover, the new one set, and
-- who and when.
UPDATE invoices.bank_import_accounts
SET previous_format = format, format = @format, cutover_through = @cutover_through,
    set_by_user_id = @set_by_user_id, set_at = @set_at::timestamptz
WHERE account = @account
RETURNING *;
