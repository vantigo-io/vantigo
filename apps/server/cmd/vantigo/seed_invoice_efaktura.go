package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
)

// The eFaktura and AvtaleGiro demo (invoices phase 6 plan D10), all of it
// written as rows — no provider is called (spec §12): the consumer
// provider's settings with both agreements active and AvtaleGiro on under a
// KID agreement of 10 digits, MOD10; a person with a phone and a date of
// birth whose lookup is found and auto-accepted; an active mandate on their
// customer KID; one invoice sent as both, its eFaktura delivered with its
// evidence and its claim accepted; and one mandate whose KID names nobody.

const (
	demoEfakturaKidLength = 10
	demoEfakturaPerson    = "Kari Nordmann"
)

func seedInvoicesEfaktura(ctx context.Context, tx pgx.Tx, now time.Time, ownerID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.consumer_provider_credentials (id, provider, settings_json, secret_ciphertext, updated_at)
		VALUES (1, 'mps', $1, '', $2)
		ON CONFLICT (id) DO NOTHING
	`, `{"efakturaAgreement":"active","avtalegiroAgreement":"active","creditorAccount":"15061000017","brandName":"Vantigo Consulting AS","abbreviatedName":"VANTIGO"}`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE invoices.settings SET kid_length = $1, kid_algorithm = 'mod10', avtalegiro_enabled = true WHERE id = 1
	`, demoEfakturaKidLength); err != nil {
		return err
	}

	// The person, numbered past every seeded company.
	var customerID int32
	var customerNumber int64
	if err := tx.QueryRow(ctx, `
		WITH next AS (
			UPDATE customers.counters SET next_value = next_value + 1 WHERE counter_name = 'customer-number'
			RETURNING next_value - 1 AS number
		)
		INSERT INTO customers.customers (customer_number, name, type, status, email, phone, date_of_birth,
		    invoice_email, language, invoice_delivery, revision, created_at, updated_at)
		SELECT number, $1, 'person', 'active', 'kari.nordmann@example.no', '+47 912 34 567', DATE '1980-05-17',
		    'kari.nordmann@example.no', 'nb', 'efaktura', 1, $2, $2 FROM next
		RETURNING id, customer_number
	`, demoEfakturaPerson, now).Scan(&customerID, &customerNumber); err != nil {
		return fmt.Errorf("the eFaktura person: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO customers.customer_addresses (customer_id, type, line1, postal_code, city, country, is_primary, created_at, updated_at)
		VALUES ($1, 'invoice', 'Storgata 12', '0155', 'Oslo', 'no', true, $2, $2)
	`, customerID, now); err != nil {
		return fmt.Errorf("the eFaktura person's address: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.consumer_lookups (customer_id, status, efaktura_identifier, auto_accept, has_agreement, checked_at, next_check_at)
		VALUES ($1, 'found', '123456789', true, false, $2, $3)
	`, customerID, now, now.AddDate(0, 0, 7)); err != nil {
		return err
	}
	customerKID, err := kid.CustomerKID(customerNumber, demoEfakturaKidLength, kid.Mod10)
	if err != nil {
		return err
	}
	strangerKID, err := kid.CustomerKID(99999, demoEfakturaKidLength, kid.Mod10)
	if err != nil {
		return err
	}
	var mandateID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.avtalegiro_mandates (kid, customer_id, status, notify_by_mail, provider_seen_at, updated_at)
		VALUES ($1, $2, 'active', false, $3, $3) RETURNING id
	`, customerKID, customerID, now).Scan(&mandateID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.avtalegiro_mandates (kid, status, notify_by_mail, provider_seen_at, updated_at)
		VALUES ($1, 'active', true, $2, $2)
	`, strangerKID, now); err != nil {
		return err
	}

	// The invoice, issued under the reserved KID and sent as both.
	var number int64
	if err := tx.QueryRow(ctx, `
		UPDATE invoices.counters SET next_value = next_value + 1 WHERE counter_name = 'documents' RETURNING next_value - 1
	`).Scan(&number); err != nil {
		return err
	}
	invoiceKID, err := kid.ComputeReserved(number, demoEfakturaKidLength, kid.Mod10)
	if err != nil {
		return err
	}
	const net, vat, gross = 1000.0, 250.0, 1250.0
	issueDate := now.AddDate(0, 0, -3)
	dueDate := issueDate.AddDate(0, 0, 14)
	var invID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.invoices (kind, status, customer_id, currency, payment_terms_days, our_reference,
		    net_total, vat_total, gross_total, vat_total_nok, revision, created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 'draft', $1, 'NOK', 14, 'Astrid Lindqvist', $2, $3, $4, $3, 1, $5, $6, $6)
		RETURNING id
	`, customerID, net, vat, gross, ownerID, now).Scan(&invID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit, unit_price, discount_percent, vat_code_id,
		    line_gross, line_allowance, line_net, vat_rate_percent, vat_category, saf_t_code)
		VALUES ($1, 1, 'Rådgivning', 1, 'hour', $2, 0, 1, $2, 0, $2, 25.00, 'S', '3')
	`, invID, net); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.vat_summaries (invoice_id, vat_category, rate_percent, saf_t_code, taxable_amount, vat_amount, vat_amount_nok)
		VALUES ($1, 'S', 25.00, '3', $2, $3, $3)
	`, invID, net, vat); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE invoices.invoices
		SET status = 'issued', number = $2, issue_date = $3::date, due_date = $4::date, exchange_rate_date = $3::date,
		    buyer_customer_number = $5, buyer_type = 'person', buyer_name = $6,
		    buyer_address_line1 = 'Storgata 12', buyer_postal_code = '0155', buyer_city = 'Oslo', buyer_country = 'NO',
		    buyer_language = 'nb', buyer_avtalegiro_kid = $7,
		    seller_legal_name = 'Vantigo Consulting AS', seller_organisation_number = '987654325',
		    seller_vat_registered = true, seller_in_foretaksregisteret = true,
		    seller_address_line1 = 'Karl Johans gate 41', seller_postal_code = '0162', seller_city = 'Oslo', seller_country = 'NO',
		    seller_bank_account = $8, seller_email = 'faktura@vantigo.local',
		    kid = $9, kid_algorithm = 'mod10',
		    issued_by_user_id = $10, issued_at = $3, updated_at = $3
		WHERE id = $1
	`, invID, number, issueDate, dueDate, customerNumber, demoEfakturaPerson, customerKID,
		norwegianBankAccount("1506100001"), invoiceKID, ownerID); err != nil {
		return fmt.Errorf("issue the eFaktura invoice: %w", err)
	}
	submitted := issueDate.Add(10 * time.Minute)
	var transmissionID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.transmissions (invoice_id, channel, provider, idempotency_key, pdf_sha256, pdf_object_key,
		    efaktura_identifier, shipment_id, payment_type, status, provider_ref, provider_status,
		    next_attempt_at, queued_at, submitted_at, delivered_at, evidence_object_key, evidence_sha256, created_by_user_id)
		VALUES ($1, 'efaktura', 'mps', $2, repeat('0', 64), $3, '123456789', $4, 'both', 'delivered', $5, 'ACCEPTED',
		    $6, $7, $7, $6, $8, repeat('0', 64), $9)
		RETURNING id
	`, invID, uuid.New(), fmt.Sprintf("documents/%d/%d.pdf", invID, number), uuid.New(), uuid.NewString(),
		submitted.Add(time.Hour), issueDate, fmt.Sprintf("documents/%d/efaktura-status.xml", invID), ownerID).Scan(&transmissionID); err != nil {
		return fmt.Errorf("the eFaktura transmission: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.avtalegiro_claims (invoice_id, transmission_id, mandate_id, amount, due_date, kid, status, submitted_at, accepted_at)
		VALUES ($1, $2, $3, $4, $5::date, $6, 'accepted', $7, $8)
	`, invID, transmissionID, mandateID, gross, dueDate, customerKID, submitted, submitted.Add(time.Hour)); err != nil {
		return fmt.Errorf("the AvtaleGiro claim: %w", err)
	}
	return nil
}
