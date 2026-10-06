-- Reminder runs and their letters (invoices payments and reminders design
-- D10). A run's row precedes its letters, which reference it; its counts are
-- known only at its end and set once (plan reading 3). A letter is created
-- without its facts — they are written when it is sent (the worker) or
-- printed (a print batch), amendment 12.

-- name: InsertReminderRun :one
INSERT INTO invoices.reminder_runs (run_on, created_at, created_by_user_id, last_booked_on, stale_import_acknowledged)
VALUES (@run_on, @created_at, @created_by_user_id, sqlc.narg(last_booked_on), @stale_import_acknowledged)
RETURNING *;

-- name: SetRunCounts :one
-- SetRunCounts sets a run's letters and skipped once, from NULL — the only
-- change its immutability trigger lets through.
UPDATE invoices.reminder_runs SET letters = @letters, skipped = @skipped
WHERE id = @id AND letters IS NULL AND skipped IS NULL
RETURNING *;

-- name: NextSequence :one
-- NextSequence is the invoice's next letter number; the caller holds the
-- invoice FOR UPDATE, so no other letter of it is being inserted, and
-- uq_reminders_invoice_sequence is the floor beneath.
SELECT (coalesce(max(sequence), 0) + 1)::smallint AS sequence FROM invoices.reminders WHERE invoice_id = @invoice_id;

-- name: InsertReminder :one
-- InsertReminder creates a letter of a run, queued for e-mail (due for the
-- worker at next_attempt_at) or awaiting print for paper, without facts.
INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, announces_collection, channel, recipient, language,
    created_at, created_by_user_id, status, next_attempt_at)
VALUES (@invoice_id, @run_id, @sequence, @level, @announces_collection, @channel, @recipient, @language,
    @created_at, @created_by_user_id, @status, sqlc.narg(next_attempt_at))
RETURNING *;

-- name: CountReminderRuns :one
SELECT count(*)::int FROM invoices.reminder_runs;

-- name: ListReminderRuns :many
-- ListReminderRuns is one page of the runs, the newest first.
SELECT * FROM invoices.reminder_runs
ORDER BY id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: GetReminderRun :one
SELECT * FROM invoices.reminder_runs WHERE id = @id;

-- name: LettersOfRun :many
-- LettersOfRun is a run's letters with their current status (plan reading
-- 26), in the order the run made them.
SELECT * FROM invoices.reminders WHERE run_id = @run_id ORDER BY id;

-- name: LettersOfInvoice :many
-- LettersOfInvoice is every letter of an invoice, any status, by sequence.
SELECT * FROM invoices.reminders WHERE invoice_id = @invoice_id ORDER BY sequence, id;
