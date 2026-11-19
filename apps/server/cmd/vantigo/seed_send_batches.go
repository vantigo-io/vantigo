package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The send all demo (invoices phase 6 plan E8), written as rows — no worker
// runs: one finished batch over three of the seeded invoices that were never
// delivered, one sent by e-mail (its delivery logged), one awaiting print and
// one failed no_invoice_email, so the batch page and the attention item have
// something to show.
func seedInvoicesSendBatch(ctx context.Context, tx pgx.Tx, now time.Time, ownerID uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT i.id FROM invoices.invoices i
		WHERE i.status = 'issued' AND i.kind = 'invoice'
		  AND NOT EXISTS (SELECT 1 FROM invoices.deliveries d WHERE d.invoice_id = i.id)
		  AND NOT EXISTS (SELECT 1 FROM invoices.transmissions t WHERE t.invoice_id = i.id)
		  AND NOT EXISTS (SELECT 1 FROM invoices.manual_deliveries m WHERE m.invoice_id = i.id)
		ORDER BY i.id DESC
		LIMIT 3`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	if len(ids) < 3 {
		return fmt.Errorf("the send all demo needs three undelivered invoices, found %d", len(ids))
	}
	at := now.Add(-2 * time.Hour)
	var batchID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.send_batches (source, requested_by, created_at, finished_at)
		VALUES ('selection', $1, $2, $3) RETURNING id
	`, ownerID, at, at.Add(time.Minute)).Scan(&batchID); err != nil {
		return fmt.Errorf("the send batch: %w", err)
	}
	var deliveryID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.deliveries (invoice_id, recipient, subject, message_id, pdf_sha256, sent_at, sent_by_user_id)
		SELECT $1, 'faktura@example.no', 'Faktura ' || number, gen_random_uuid()::text || '@vantigo.invalid', repeat('0', 64), $2, $3
		FROM invoices.invoices WHERE id = $1
		RETURNING id
	`, ids[0], at.Add(30*time.Second), ownerID).Scan(&deliveryID); err != nil {
		return fmt.Errorf("the send batch's e-mail: %w", err)
	}
	done := at.Add(time.Minute)
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.send_jobs (batch_id, invoice_id, channel, state, code, detail, attempts, started_at, delivery_id, created_at, finished_at)
		VALUES
		    ($1, $2, 'email', 'sent', NULL, '', 1, $5, $6, $5, $7),
		    ($1, $3, 'print', 'awaiting_print', NULL, 'Download the document for printing, post it, and mark it posted.', 1, $5, NULL, $5, $7),
		    ($1, $4, 'email', 'failed', 'no_invoice_email', 'The customer has no invoice e-mail. Add one to the customer, or send it from the document.', 1, $5, NULL, $5, $7)
	`, batchID, ids[0], ids[1], ids[2], at, deliveryID, done); err != nil {
		return fmt.Errorf("the send batch's jobs: %w", err)
	}
	return nil
}
