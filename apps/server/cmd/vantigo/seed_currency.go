package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// seedInvoicesCurrency shows an invoice in euro (invoices phase 6 design
// I17–I24, plan delivery C) as the module would have written it: EUR turned
// on, Norges Bank's rates of the last 30 business days near 11.60 and one rate
// a manager entered, one customer whose billing profile is in EUR, and an
// issued invoice of EUR 1 625.00 at 11.60 — NOK 18 850.00 — paid in two NOK
// transfers: the first brought NOK 100.00 more than it cleared (a currency
// gain), the closing one NOK 87.50 less than the rest (a loss), so nothing
// is left open in NOK.
func seedInvoicesCurrency(ctx context.Context, tx pgx.Tx, now time.Time, ownerID uuid.UUID, customerID int32, number int64,
	sellerOrg, sellerBank string,
) error {
	if _, err := tx.Exec(ctx, `UPDATE invoices.settings SET currencies = '{EUR}' WHERE id = 1`); err != nil {
		return fmt.Errorf("seed the currencies: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE customers.customers SET currency = 'EUR' WHERE id = $1`, customerID); err != nil {
		return fmt.Errorf("seed the EUR customer: %w", err)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for d := 30; d >= 1; d-- {
		day := today.AddDate(0, 0, -d)
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		rate := 11.6 + float64(d%5)*0.01
		source, by := "norges_bank", (*uuid.UUID)(nil)
		if d == 3 {
			source, by = "manual", &ownerID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoices.exchange_rates (currency, rate_date, rate, source, fetched_at, entered_by_user_id)
			VALUES ('EUR', $1::date, $2, $3, $1::date + time '17:00', $4)`, day, rate, source, by); err != nil {
			return fmt.Errorf("seed the EUR rate of %s: %w", day.Format(time.DateOnly), err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.rate_fetches (currency, last_attempt_at, last_success_at) VALUES ('EUR', $1, $1)`, now); err != nil {
		return fmt.Errorf("seed the EUR fetch: %w", err)
	}

	issueDate := today.AddDate(0, 0, -20)
	var invID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.invoices (kind, status, customer_id, currency, exchange_rate, exchange_rate_source, payment_terms_days,
		    your_reference, net_total, vat_total, gross_total, vat_total_nok, created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 'draft', $1, 'EUR', 11.6, 'norges_bank', 14, 'Purchasing', 1300, 325, 1625, 3770, $2, $3, $3)
		RETURNING id`, customerID, ownerID, issueDate).Scan(&invID); err != nil {
		return fmt.Errorf("seed the EUR invoice: %w", err)
	}
	for _, l := range []struct {
		position             int
		description          string
		quantity, price, net float64
		netNOK               float64
	}{
		{1, "Consulting hours", 10, 120, 1200, 13920},
		{2, "Travel day", 1, 100, 100, 1160},
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit, unit_price, discount_percent, vat_code_id,
			    line_gross, line_allowance, line_net, line_net_nok, vat_rate_percent, vat_category, saf_t_code)
			VALUES ($1, $2, $3, $4, 'hour', $5, 0, 1, $6, 0, $6, $7, 25.00, 'S', '3')`,
			invID, l.position, l.description, l.quantity, l.price, l.net, l.netNOK); err != nil {
			return fmt.Errorf("seed the EUR invoice's line %d: %w", l.position, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.vat_summaries (invoice_id, vat_category, rate_percent, saf_t_code, taxable_amount, vat_amount,
		    vat_amount_nok, taxable_amount_nok)
		VALUES ($1, 'S', 25.00, '3', 1300, 325, 3770, 15080)`, invID); err != nil {
		return fmt.Errorf("seed the EUR invoice's VAT: %w", err)
	}
	var buyerNumber int64
	var buyerName, buyerOrg string
	if err := tx.QueryRow(ctx, `SELECT customer_number, legal_name, legal_id FROM customers.customers WHERE id = $1`, customerID).
		Scan(&buyerNumber, &buyerName, &buyerOrg); err != nil {
		return fmt.Errorf("read the EUR customer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE invoices.invoices SET status = 'issued', number = $2, issue_date = $3::date, due_date = $3::date + 14,
		    exchange_rate_date = $3::date - 1, buyer_customer_number = $4, buyer_type = 'business', buyer_name = $5,
		    buyer_organisation_number = $6, buyer_country = 'NO', buyer_language = 'en',
		    seller_legal_name = 'Vantigo Consulting AS', seller_organisation_number = $7, seller_vat_registered = true,
		    seller_in_foretaksregisteret = true, seller_address_line1 = 'Karl Johans gate 41', seller_postal_code = '0162',
		    seller_city = 'Oslo', seller_country = 'NO', seller_bank_account = $8, seller_iban = 'NO9386011117947',
		    seller_bic = 'DNBANOKKXXX', seller_email = 'faktura@vantigo.local', issued_by_user_id = $9, issued_at = $3,
		    updated_at = $3
		WHERE id = $1`, invID, number, issueDate, buyerNumber, buyerName, buyerOrg, sellerOrg, sellerBank, ownerID); err != nil {
		return fmt.Errorf("issue the EUR invoice: %w", err)
	}
	// 800 at 11.60 clears 9 280.00 and 9 380.00 arrived (+100.00); the
	// closing 825 clears the 9 570.00 left and 9 482.50 arrived (−87.50).
	for _, p := range []struct {
		days                      int
		amount, amountNOK, differ float64
		reference                 string
	}{
		{8, 800, 9380, 100, fmt.Sprintf("EUR-%d-1", number)},
		{12, 825, 9482.50, -87.50, fmt.Sprintf("EUR-%d-2", number)},
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoices.payments (invoice_id, paid_on, amount, amount_nok, exchange_difference_nok, currency, source,
			    reference, note, registered_by_user_id, registered_at)
			VALUES ($1, $2::date, $3, $4, $5, 'EUR', 'manual', $6, 'Received in NOK', $7, $2)`,
			invID, issueDate.AddDate(0, 0, p.days), p.amount, p.amountNOK, p.differ, p.reference, ownerID); err != nil {
			return fmt.Errorf("seed the EUR invoice's payment: %w", err)
		}
	}
	return nil
}
