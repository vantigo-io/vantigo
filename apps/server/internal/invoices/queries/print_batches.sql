-- Paper letters (invoices payments and reminders design D10, reading 39): a
-- batch printed for one posting day, its letters' facts written as the
-- engine judges them on that day, and the letters sent only when a person
-- confirms the batch was posted on it — or reprinted. A batch is posted once
-- or reprinted once, never both (its immutability trigger, plan reading 40).

-- name: PrintCandidates :many
-- PrintCandidates is the letters a batch is asked to print, as the pool reads
-- them before anything is written: each must be awaiting print.
SELECT * FROM invoices.reminders WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY id;

-- name: InsertPrintBatch :one
INSERT INTO invoices.reminder_print_batches (post_on, created_at, created_by_user_id)
VALUES (@post_on, @created_at, @created_by_user_id)
RETURNING *;

-- name: MarkPrinted :one
-- MarkPrinted puts a letter whose facts judgeAndWriteFacts has just written
-- at L = post_on into its batch, printed. The caller holds the invoice, then
-- the letter (D18).
UPDATE invoices.reminders SET status = 'printed', print_batch_id = @print_batch_id::bigint
WHERE id = @id AND status = 'awaiting_print' AND sent_on IS NOT NULL
RETURNING *;

-- name: WithdrawUnprinted :one
-- WithdrawUnprinted is a batch's re-judge withdrawing a letter awaiting print
-- that no longer goes on the posting day (D10 step 1's withdrawals): the
-- module's reason code and no user (plan reading 44).
UPDATE invoices.reminders
SET status = 'withdrawn', withdrawn_at = @withdrawn_at::timestamptz, withdrawal_reason = @withdrawal_reason::text,
    held_reason = NULL
WHERE id = @id AND status = 'awaiting_print'
RETURNING *;

-- name: SetPrintedPDF :execrows
-- SetPrintedPDF records the PDF a batch rendered and stored for one of its
-- letters, outside any lock: only while the letter is still printed in that
-- batch and has no PDF yet, so a reprint in between records nothing.
UPDATE invoices.reminders SET pdf_object_key = @pdf_object_key::text, pdf_sha256 = @pdf_sha256::text
WHERE id = @id AND print_batch_id = @print_batch_id::bigint AND status = 'printed' AND pdf_object_key IS NULL;

-- name: GetPrintBatch :one
SELECT * FROM invoices.reminder_print_batches WHERE id = @id;

-- name: LettersOfBatch :many
-- LettersOfBatch is every letter naming a batch, any status, by id: printed
-- ones until the posting or a reprint, sent ones after the posting, and the
-- ones withdrawn by hand since printing.
SELECT * FROM invoices.reminders WHERE print_batch_id = @print_batch_id::bigint ORDER BY id;

-- name: LettersOfBatches :many
-- LettersOfBatches is LettersOfBatch for a page of batches at once.
SELECT * FROM invoices.reminders WHERE print_batch_id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY print_batch_id, id;

-- name: MarkPosted :one
-- MarkPosted is a printed letter posted (D10): sent at the confirmation's
-- time. The caller holds the batch, the letter's invoice, then the letter.
UPDATE invoices.reminders SET status = 'sent', sent_at = @sent_at::timestamptz
WHERE id = @id AND status = 'printed' AND print_batch_id = @print_batch_id::bigint
RETURNING *;

-- name: SetBatchPosted :one
-- SetBatchPosted records the posting once: the day, who and when together.
UPDATE invoices.reminder_print_batches
SET posted_on = @posted_on::date, posted_at = @posted_at::timestamptz, posted_by_user_id = @posted_by_user_id::uuid
WHERE id = @id AND posted_on IS NULL AND reprinted_at IS NULL
RETURNING *;

-- name: ReprintBatch :many
-- ReprintBatch returns a batch's printed letters to awaiting print, with
-- every fact, the batch and the PDF cleared — a later batch writes them again
-- for its own day and stores a new PDF under a new key (plan reading 35).
-- Letters withdrawn since printing keep their batch: a withdrawn letter
-- never changes.
UPDATE invoices.reminders
SET status = 'awaiting_print', print_batch_id = NULL,
    sent_on = NULL, deadline = NULL, regime = NULL, principal_open = NULL, fee_kind = NULL, fee = NULL,
    compensation = NULL, charges_earlier = NULL, interest = NULL, interest_waived = NULL, interest_paid = NULL,
    interest_from = NULL, interest_segments = NULL, inkassosats = NULL, total = NULL, charge_notes = NULL,
    pdf_object_key = NULL, pdf_sha256 = NULL
WHERE print_batch_id = @print_batch_id::bigint AND status = 'printed'
RETURNING id;

-- name: SetBatchReprinted :one
-- SetBatchReprinted records the reprint once (plan reading 40).
UPDATE invoices.reminder_print_batches SET reprinted_at = @reprinted_at::timestamptz
WHERE id = @id AND posted_on IS NULL AND reprinted_at IS NULL
RETURNING *;

-- name: CountPrintBatches :one
-- CountPrintBatches is how many batches ListPrintBatches' filter matches:
-- posted true, the posted ones; false, the open ones — neither posted nor
-- reprinted; NULL, all.
SELECT count(*)::int FROM invoices.reminder_print_batches
WHERE sqlc.narg(posted)::boolean IS NULL
   OR (sqlc.narg(posted)::boolean AND posted_on IS NOT NULL)
   OR (NOT sqlc.narg(posted)::boolean AND posted_on IS NULL AND reprinted_at IS NULL);

-- name: ListPrintBatches :many
-- ListPrintBatches is one page of the batches (plan reading 12), the newest
-- first, filtered as CountPrintBatches.
SELECT * FROM invoices.reminder_print_batches
WHERE sqlc.narg(posted)::boolean IS NULL
   OR (sqlc.narg(posted)::boolean AND posted_on IS NOT NULL)
   OR (NOT sqlc.narg(posted)::boolean AND posted_on IS NULL AND reprinted_at IS NULL)
ORDER BY id DESC
LIMIT @page_size OFFSET @page_offset;
