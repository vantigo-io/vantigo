-- name: LockCustomerDocuments :exec
-- LockCustomerDocuments locks every document of the two customers a merge
-- touches, newest first, before RepointCustomer writes them (D10). A
-- credit note's issue locks the credit note, then its older original; an
-- UPDATE locks in whatever order it scans, which can be the original first —
-- and a merge holding the original while waiting on the credit note, beside
-- an issue holding the credit note while waiting on the original, is a
-- deadlock. Locking id-descending takes them in the issue's order. The erase
-- (D6) reuses it with the one customer's id as both, to hold the person's
-- documents against a send's delivery insert.
SELECT id FROM invoices.invoices
WHERE customer_id IN (sqlc.arg(from_customer_id)::integer, sqlc.arg(into_customer_id)::integer)
ORDER BY id DESC
FOR UPDATE;

-- name: RepointCustomer :execrows
-- RepointCustomer moves every document of one customer to another (D10), the
-- merge holder's one write, inside the merge's transaction. A draft's revision
-- and updated_at move as any change to it does; an issued document's do not —
-- the trigger allows exactly the customer_id to change on it (D9), and its
-- buyer snapshot, which is what it printed, is untouched.
UPDATE invoices.invoices SET
    customer_id = @into_customer_id,
    revision = CASE WHEN status = 'draft' THEN revision + 1 ELSE revision END,
    updated_at = CASE WHEN status = 'draft' THEN @now::timestamptz ELSE updated_at END
WHERE customer_id = @from_customer_id;

-- name: CustomerDocuments :many
-- CustomerDocuments is every document of one customer, issued ones by number
-- first and then the drafts: a private person's export (D10).
SELECT * FROM invoices.invoices
WHERE customer_id = @customer_id
ORDER BY status DESC, number, id;

-- name: LinesOf :many
-- LinesOf is the lines of several documents at once, in their documents'
-- order.
SELECT * FROM invoices.lines WHERE invoice_id = ANY(@invoice_ids::bigint[]) ORDER BY invoice_id, position;

-- name: DeleteCustomerDrafts :execrows
-- DeleteCustomerDrafts erases a person's drafts on anonymisation (D10): a
-- draft is not a salgsdokument, so nothing keeps it. Issued documents stay —
-- bokføringsloven § 13 keeps them five years after the financial year.
DELETE FROM invoices.invoices WHERE customer_id = @customer_id AND status = 'draft';

-- name: MarkCustomerErased :exec
-- MarkCustomerErased records that this module has anonymised a customer
-- (payments and delivery design D6), under the erase's lock on the
-- customer's documents. A send to a marked customer is refused, and a
-- delivery row written after the mark is blanked by tr_deliveries_parent.
-- Never removed: anonymisation is never undone, and a second erase keeps the
-- first time.
INSERT INTO invoices.erased_customers (customer_id, erased_at)
VALUES (@customer_id, @erased_at::timestamptz)
ON CONFLICT (customer_id) DO NOTHING;

-- name: BlankCustomerDeliveries :execrows
-- BlankCustomerDeliveries removes the address from every delivery of a
-- customer's documents on anonymisation (D6) — the one write
-- tr_deliveries_immutable allows. The rows stay: they are the evidence of
-- when the claim was sent. A row blanked already is not counted again.
UPDATE invoices.deliveries d SET recipient = ''
FROM invoices.invoices i
WHERE d.invoice_id = i.id AND i.customer_id = @customer_id AND d.recipient <> '';

-- name: BlankCustomerPaymentNotes :execrows
-- BlankCustomerPaymentNotes removes the staff-written note from every payment
-- of a customer's documents, live and removed, on anonymisation (D6) — the
-- write tr_payments_immutable allows besides the removal. The registrations
-- stay, the bank's reference with them: they are bookkeeping material kept
-- with the document. A note blanked already, or never written, is not
-- counted.
UPDATE invoices.payments p SET note = ''
FROM invoices.invoices i
WHERE p.invoice_id = i.id AND i.customer_id = @customer_id AND p.note <> '';

-- name: PaymentsOfDocuments :many
-- PaymentsOfDocuments is every registration of several documents at once,
-- removed ones included, for a private person's export (D6).
SELECT * FROM invoices.payments
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, paid_on, id;

-- name: DeliveriesOfDocuments :many
-- DeliveriesOfDocuments is every send of several documents at once, for a
-- private person's export (D6).
SELECT * FROM invoices.deliveries
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, sent_at, id;

-- The receivables of a person's export and erase (invoices payments and
-- reminders design D19): what each issued document carries beyond its
-- payments, deliveries and transmissions, and what the erase blanks of it.

-- name: ChargePaymentsOfDocuments :many
-- ChargePaymentsOfDocuments is every charge payment of several documents at
-- once, removed ones included, for a person's export (D19).
SELECT * FROM invoices.charge_payments
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, paid_on, id;

-- name: BankLinesOfDocuments :many
-- BankLinesOfDocuments is every bank line a payment or a charge payment of
-- several documents came from, for a person's export (D19): the booking day
-- and what the bank said of the payer.
SELECT t.id, t.booked_on, t.debtor_name, t.debtor_account, t.remittance_text
FROM invoices.bank_transactions t
WHERE t.id IN (
    SELECT p.bank_transaction_id FROM invoices.payments p
    WHERE p.invoice_id = ANY(@invoice_ids::bigint[]) AND p.bank_transaction_id IS NOT NULL
    UNION
    SELECT c.bank_transaction_id FROM invoices.charge_payments c
    WHERE c.invoice_id = ANY(@invoice_ids::bigint[]) AND c.bank_transaction_id IS NOT NULL)
ORDER BY t.id;

-- name: WaiversOfDocuments :many
-- WaiversOfDocuments is every charge waiver of several documents at once,
-- for a person's export (D19).
SELECT * FROM invoices.charge_waivers
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, waived_at, id;

-- name: ManualDeliveriesOfDocuments :many
-- ManualDeliveriesOfDocuments is every manual delivery of several documents
-- at once, removed ones included, for a person's export (D19).
SELECT * FROM invoices.manual_deliveries
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, delivered_on, id;

-- name: RemindersOfDocuments :many
-- RemindersOfDocuments is every letter of several documents at once, in
-- every status, for a person's export (D19).
SELECT * FROM invoices.reminders
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, sequence, id;

-- name: HoldsOfDocuments :many
-- HoldsOfDocuments is every hold of several documents at once, lifted ones
-- included, for a person's export (D19).
SELECT * FROM invoices.invoice_holds
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, placed_at, id;

-- name: HandoffsOfDocuments :many
-- HandoffsOfDocuments is every hand-off of several documents at once,
-- withdrawn ones included, for a person's export (D19).
SELECT * FROM invoices.collection_handoffs
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, created_at, id;

-- name: CustomerLetterInvoices :many
-- CustomerLetterInvoices is the person's invoices with a letter in flight or
-- printed, newest first, the erase's documents being locked already: each is
-- handed to withdrawInFlight (D19).
SELECT DISTINCT r.invoice_id
FROM invoices.reminders r
JOIN invoices.invoices i ON i.id = r.invoice_id
WHERE i.customer_id = @customer_id AND r.status IN ('queued', 'awaiting_print', 'failed', 'printed')
ORDER BY r.invoice_id DESC;

-- name: BlankCustomerLetterRecipients :many
-- BlankCustomerLetterRecipients blanks the recipient of every letter of a
-- customer's documents, in every status (D19; tr_reminders_immutable allows
-- exactly that, B1), and answers the letters it blanked. Only a letter with
-- a recipient is touched: a paper letter's is '' already, so a printed
-- letter a reprint holds without its invoice is never waited for.
UPDATE invoices.reminders r SET recipient = ''
FROM invoices.invoices i
WHERE r.invoice_id = i.id AND i.customer_id = @customer_id AND r.recipient <> ''
RETURNING r.id;

-- name: BlankCustomerChargePaymentNotes :execrows
-- BlankCustomerChargePaymentNotes removes the staff-written note from every
-- charge payment of a customer's documents, live and removed (D19).
UPDATE invoices.charge_payments c SET note = ''
FROM invoices.invoices i
WHERE c.invoice_id = i.id AND i.customer_id = @customer_id AND c.note <> '';

-- name: BlankCustomerWaiverNotes :execrows
-- BlankCustomerWaiverNotes removes the note from every charge waiver of a
-- customer's documents (D19).
UPDATE invoices.charge_waivers w SET note = ''
FROM invoices.invoices i
WHERE w.invoice_id = i.id AND i.customer_id = @customer_id AND w.note <> '';

-- name: BlankCustomerManualDeliveryNotes :execrows
-- BlankCustomerManualDeliveryNotes removes the note from every manual
-- delivery of a customer's documents, live and removed (D19).
UPDATE invoices.manual_deliveries d SET note = ''
FROM invoices.invoices i
WHERE d.invoice_id = i.id AND i.customer_id = @customer_id AND d.note <> '';

-- name: BlankCustomerHoldNotes :execrows
-- BlankCustomerHoldNotes removes the note and the lift's note from every
-- hold of a customer's documents (D19); a hold never lifted keeps no lift
-- note.
UPDATE invoices.invoice_holds h SET note = '', lift_note = CASE WHEN h.lift_note IS NULL THEN NULL ELSE '' END
FROM invoices.invoices i
WHERE h.invoice_id = i.id AND i.customer_id = @customer_id AND (h.note <> '' OR coalesce(h.lift_note, '') <> '');

-- name: BlankCustomerHandoffNotes :execrows
-- BlankCustomerHandoffNotes removes the note from every hand-off of a
-- customer's documents (D19); the agency and its reference stay.
UPDATE invoices.collection_handoffs c SET note = ''
FROM invoices.invoices i
WHERE c.invoice_id = i.id AND i.customer_id = @customer_id AND c.note <> '';

-- name: BlankCustomerBankLineNotes :many
-- BlankCustomerBankLineNotes blanks the resolution note of every bank line a
-- customer's payments or charge payments, live or removed, came from (D19,
-- M10) — only a resolved line whose note is not empty: the lock order's one
-- named exception (D18, plan reading 54). The erase holds the person's
-- documents; no path locks a resolved line before an invoice (apply and
-- handle-reversal lock exception lines, reopen locks the line alone), and a
-- line that is not resolved is skipped without being waited for, so no cycle
-- forms. It answers the lines it blanked.
UPDATE invoices.bank_transactions t SET resolution_note = ''
WHERE t.status = 'resolved' AND t.resolution_note <> ''
  AND t.id IN (
    SELECT p.bank_transaction_id FROM invoices.payments p JOIN invoices.invoices i ON i.id = p.invoice_id
    WHERE i.customer_id = @customer_id AND p.bank_transaction_id IS NOT NULL
    UNION
    SELECT c.bank_transaction_id FROM invoices.charge_payments c JOIN invoices.invoices i ON i.id = c.invoice_id
    WHERE i.customer_id = @customer_id AND c.bank_transaction_id IS NOT NULL)
RETURNING t.id;

-- name: BlankCustomerBankEventNotes :many
-- BlankCustomerBankEventNotes blanks the note of every event of the resolved
-- bank lines BlankCustomerBankLineNotes reads (D19; the one write
-- tr_bank_transaction_events_immutable allows, B1), and answers each
-- event's line. The lines are read, never locked, and an event row is
-- locked by nothing else: the queue only ever inserts them.
UPDATE invoices.bank_transaction_events e SET note = ''
WHERE e.note <> ''
  AND e.bank_transaction_id IN (
    SELECT t.id FROM invoices.bank_transactions t
    WHERE t.status = 'resolved' AND t.id IN (
        SELECT p.bank_transaction_id FROM invoices.payments p JOIN invoices.invoices i ON i.id = p.invoice_id
        WHERE i.customer_id = @customer_id AND p.bank_transaction_id IS NOT NULL
        UNION
        SELECT c.bank_transaction_id FROM invoices.charge_payments c JOIN invoices.invoices i ON i.id = c.invoice_id
        WHERE i.customer_id = @customer_id AND c.bank_transaction_id IS NOT NULL))
RETURNING e.bank_transaction_id;
