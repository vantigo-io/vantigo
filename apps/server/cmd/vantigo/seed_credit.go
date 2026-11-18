package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// seedInvoicesCredit gives the first demo customer a customer's credit
// (invoices phase 6 design I8-I12, plan delivery B-1), as the screens would
// have written it: 500 of an overpayment registered by hand on its paid
// invoice, kept as credit; a later bank line of 300 for the same, settled,
// invoice, kept as credit from the exception queue; 400 of it applied to the
// customer's open invoice; and 100 refunded from the company's account —
// leaving 300 in credit. paid is the paid invoice, open the open one.
func seedInvoicesCredit(ctx context.Context, tx pgx.Tx, now time.Time, ownerID uuid.UUID, customerID int32,
	paid, open int64, sellerBank string,
) error {
	var paymentID int64
	var paidOn time.Time
	var kid *string
	if err := tx.QueryRow(ctx, `SELECT p.id, p.paid_on, i.kid FROM invoices.payments p JOIN invoices.invoices i ON i.id = p.invoice_id
		WHERE p.invoice_id = $1 ORDER BY p.id LIMIT 1`, paid).Scan(&paymentID, &paidOn, &kid); err != nil {
		return fmt.Errorf("read the paid invoice's payment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.customer_credits (customer_id, currency, amount, kind, credit_day, note, invoice_id, payment_id,
		    registered_by_user_id, registered_at)
		VALUES ($1, 'NOK', 500, 'overpayment', $2::date, 'Betalte 500 kroner for mye', $3, $4, $5, $2)`,
		customerID, paidOn, paid, paymentID, ownerID); err != nil {
		return fmt.Errorf("seed the overpayment kept by hand: %w", err)
	}

	// A camt.054 line for the settled invoice, queued invoice_settled and
	// kept as credit from the queue.
	bookedOn := paidOn.AddDate(0, 0, 3)
	sum := sha256.Sum256([]byte("demo-credit-line"))
	sha := fmt.Sprintf("%x", sum)
	var fileID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, first_booked_on,
		    last_booked_on, transactions, ignored, ignored_kinds, uploaded_by_user_id, uploaded_at)
		VALUES ('camt054', $1::text, 'DEMO-CREDIT-1', 'bank-files/' || $1::text || '.xml', 1200, ARRAY[$2::text], $3::date, $3::date,
		    1, 0, '{"debit":0,"not_booked":0,"card_information":0,"zero_amount":0}', $4, $3)
		RETURNING id`, sha, sellerBank, bookedOn, ownerID).Scan(&fileID); err != nil {
		return fmt.Errorf("seed the bank file: %w", err)
	}
	var lineID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on, amount, currency,
		    kid, remittance_text, debtor_name, fingerprint, ordinal, status, reason, suggested_invoice_id, resolution,
		    resolved_by_user_id, resolved_at, resolution_note)
		VALUES ($1, 'DEMO-CREDIT-1/1/1', 'camt054', $2, 'credit', $3::date, 300, 'NOK', $4, 'Faktura betalt to ganger', 'Kunde AS',
		    $5, 1, 'resolved', 'invoice_settled', $6, 'applied', $7, $3, 'Betalt to ganger, holdt som kreditt')
		RETURNING id`, fileID, sellerBank, bookedOn, kid, sha, paid, ownerID).Scan(&lineID); err != nil {
		return fmt.Errorf("seed the bank line: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.bank_transaction_events (bank_transaction_id, event, reason, note, by_user_id, at)
		VALUES ($1, 'queued', 'invoice_settled', '', $2, $3), ($1, 'kept_as_credit', 'invoice_settled', 'Betalt to ganger, holdt som kreditt', $2, $3)`,
		lineID, ownerID, bookedOn); err != nil {
		return fmt.Errorf("seed the bank line's events: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.customer_credits (customer_id, currency, amount, kind, credit_day, note, invoice_id, bank_transaction_id,
		    registered_by_user_id, registered_at)
		VALUES ($1, 'NOK', 300, 'overpayment', $2::date, 'Betalt to ganger, holdt som kreditt', $3, $4, $5, $2)`,
		customerID, bookedOn, paid, lineID, ownerID); err != nil {
		return fmt.Errorf("seed the overpayment kept from the queue: %w", err)
	}

	// 400 applied to the customer's open invoice: a payment of source
	// credit and its applied movement.
	appliedOn := now.AddDate(0, 0, -1)
	var creditPayment int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.payments (invoice_id, paid_on, amount, amount_nok, currency, source, reference, note, registered_by_user_id, registered_at)
		VALUES ($1, $2::date, 400, 400, 'NOK', 'credit', '', '', $3, $2) RETURNING id`, open, appliedOn, ownerID).Scan(&creditPayment); err != nil {
		return fmt.Errorf("seed the credit applied: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.customer_credits (customer_id, currency, amount, kind, credit_day, invoice_id, payment_id,
		    registered_by_user_id, registered_at)
		VALUES ($1, 'NOK', -400, 'applied', $2::date, $3, $4, $5, $2)`, customerID, appliedOn, open, creditPayment, ownerID); err != nil {
		return fmt.Errorf("seed the applied movement: %w", err)
	}

	// 100 refunded from the company's account.
	var refundID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.credit_refunds (customer_id, refunded_on, amount, currency, bank_account, reference, note,
		    registered_by_user_id, registered_at)
		VALUES ($1, $2::date, 100, 'NOK', $3, 'Tilbakebetaling', 'Kunden ba om 100 kroner tilbake', $4, $2) RETURNING id`,
		customerID, now, sellerBank, ownerID).Scan(&refundID); err != nil {
		return fmt.Errorf("seed the refund: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.customer_credits (customer_id, currency, amount, kind, credit_day, note, refund_id,
		    registered_by_user_id, registered_at)
		VALUES ($1, 'NOK', -100, 'refunded', $2::date, 'Kunden ba om 100 kroner tilbake', $3, $4, $2)`,
		customerID, now, refundID, ownerID); err != nil {
		return fmt.Errorf("seed the refunded movement: %w", err)
	}
	return nil
}
