package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// seedInvoicesSplit shows the automatic split and the small difference
// (invoices phase 6 design I14, I15, plan delivery B-2), as matching and a
// payment would have written them: a rounding tolerance of 5 kroner; the
// invoice rounded, paid 2 kroner short, with the automatic rounding
// write-off of the rest; and a bank-feed line whose text names the two open
// invoices first and second, paid in one, matched across both with an
// auto_split event and by no one, as the feed registers.
func seedInvoicesSplit(ctx context.Context, tx pgx.Tx, now time.Time, rounded, first, second int64, sellerBank string) error {
	if _, err := tx.Exec(ctx, `UPDATE invoices.settings SET tolerance = 5 WHERE id = 1`); err != nil {
		return fmt.Errorf("seed the rounding tolerance: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.write_offs (invoice_id, written_off_on, amount, amount_nok, currency, reason, source, registered_at)
		SELECT p.invoice_id, p.paid_on, 2, 2, 'NOK', 'rounding', 'auto', p.registered_at
		FROM invoices.payments p WHERE p.invoice_id = $1 ORDER BY p.id LIMIT 1`, rounded); err != nil {
		return fmt.Errorf("seed the automatic rounding write-off: %w", err)
	}

	var numbers [2]int64
	var total float64
	for i, id := range []int64{first, second} {
		var gross float64
		if err := tx.QueryRow(ctx, `SELECT number, gross_total::float8 FROM invoices.invoices WHERE id = $1`, id).
			Scan(&numbers[i], &gross); err != nil {
			return fmt.Errorf("read invoice %d: %w", id, err)
		}
		total += gross
	}
	bookedOn := now.AddDate(0, 0, -1)
	sum := sha256.Sum256([]byte("demo-split-line"))
	sha := fmt.Sprintf("%x", sum)
	var fileID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, first_booked_on,
		    last_booked_on, transactions, ignored, ignored_kinds, provider, uploaded_at)
		VALUES ('feed', $1::text, 'DEMO-SPLIT-1', 'bank-files/' || $1::text || '.json', 800, ARRAY[$2::text], $3::date, $3::date,
		    1, 0, '{"debit":0,"not_booked":0,"card_information":0,"zero_amount":0}', 'folio', $3)
		RETURNING id`, sha, sellerBank, bookedOn).Scan(&fileID); err != nil {
		return fmt.Errorf("seed the bank feed batch: %w", err)
	}
	var lineID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on, amount, currency,
		    remittance_text, debtor_name, fingerprint, ordinal, status)
		VALUES ($1, 'DEMO-SPLIT-1/1', 'feed', $2, 'credit', $3::date, $4, 'NOK', $5, 'Kunde AS', $6, 1, 'matched')
		RETURNING id`, fileID, sellerBank, bookedOn, total,
		fmt.Sprintf("Faktura %d og %d", numbers[0], numbers[1]), sha).Scan(&lineID); err != nil {
		return fmt.Errorf("seed the split bank line: %w", err)
	}
	for _, id := range []int64{first, second} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoices.payments (invoice_id, paid_on, amount, amount_nok, currency, source, bank_transaction_id,
			    reference, note, registered_at)
			SELECT id, $2::date, gross_total, gross_total, 'NOK', 'bank_feed', $3, '', '', $2
			FROM invoices.invoices WHERE id = $1`, id, bookedOn, lineID); err != nil {
			return fmt.Errorf("seed the split's payment of %d: %w", id, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.bank_transaction_events (bank_transaction_id, event, at) VALUES ($1, 'auto_split', $2)`,
		lineID, bookedOn); err != nil {
		return fmt.Errorf("seed the split's event: %w", err)
	}
	return nil
}
