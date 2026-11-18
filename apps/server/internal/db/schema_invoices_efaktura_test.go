package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// invoicesEfakturaVersion is 00201_invoices_efaktura.sql and
// customersDateOfBirthVersion 00202_customers_date_of_birth.sql (invoices
// phase 6 delivery D).
const (
	invoicesEfakturaVersion     = 201
	customersDateOfBirthVersion = 202
)

// TestInvoicesEfaktura_AppliesAndIsIdempotent proves 00201 applies, rolls
// back and re-applies cleanly, and pins what delivery D rests on: an EHF row
// keeps its nine Peppol columns and an eFaktura row has none of them and its
// own four instead (reading 2); one live transmission per document and
// channel; provider_status a state column of the replaced trigger and the
// eFaktura identity frozen; and a claim that only changes its state, is never
// deleted, and is one live per invoice.
func TestInvoicesEfaktura_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, invoicesEfakturaVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var invoice int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, due_date, exchange_rate_date,
		    seller_legal_name, buyer_name, gross_total, kid, kid_algorithm, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 'issued', 1, 7, DATE '2026-09-12', DATE '2026-09-26', DATE '2026-09-12',
		    'Selger AS', 'Kari Nordmann', 1000, '00000000018', 'mod10', now(), gen_random_uuid(), now(), now()) RETURNING id`).Scan(&invoice); err != nil {
		t.Fatalf("seed an issued invoice: %v", err)
	}

	const insertEhf = `
		INSERT INTO invoices.transmissions (invoice_id, provider, idempotency_key, sender_participant, receiver_participant,
		    document_type, process_id, ubl_object_key, ubl_sha256, pdf_sha256, next_attempt_at,
		    lookup_registered, lookup_can_receive, lookup_at, queued_at, created_by_user_id)
		VALUES ($1, 'storecove', gen_random_uuid(), '0192:974760673', '0192:923609016',
		    'busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Invoice-2', 'urn:fdc:peppol.eu:2017:poacc:billing:01:1.0',
		    'documents/1/1-ubl.xml', repeat('a', 64), repeat('b', 64), now(),
		    true, true, now(), now(), gen_random_uuid()) RETURNING id`
	const insertEfaktura = `
		INSERT INTO invoices.transmissions (invoice_id, channel, provider, idempotency_key, pdf_sha256, next_attempt_at, queued_at,
		    created_by_user_id, efaktura_identifier, shipment_id, payment_type, pdf_object_key)
		VALUES ($1, 'efaktura', 'mps', gen_random_uuid(), repeat('b', 64), now(), now(),
		    gen_random_uuid(), '123456789', gen_random_uuid(), $2, 'documents/1/1.pdf') RETURNING id`

	var ehfRow, efRow int64
	if err := pool.QueryRow(ctx, insertEhf, invoice).Scan(&ehfRow); err != nil {
		t.Fatalf("an EHF transmission: %v", err)
	}
	if err := pool.QueryRow(ctx, insertEfaktura, invoice, "both").Scan(&efRow); err != nil {
		t.Fatalf("an eFaktura beside the EHF: %v, want it allowed (one live per channel)", err)
	}
	if _, err := pool.Exec(ctx, insertEfaktura, invoice, "efaktura"); !isUniqueViolation(err) {
		t.Errorf("a second live eFaktura: %v, want ux_transmissions_active's refusal", err)
	}
	if _, err := pool.Exec(ctx, insertEfaktura, invoice, "avtalegiro"); !checkViolationOf(err, "ck_transmissions_channel") {
		t.Errorf("an unknown payment type: %v, want ck_transmissions_channel", err)
	}
	// An eFaktura row carries none of the Peppol columns, an EHF row all.
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoices.transmissions (invoice_id, channel, provider, idempotency_key, pdf_sha256, next_attempt_at, queued_at,
		    created_by_user_id, efaktura_identifier, shipment_id, payment_type, pdf_object_key, receiver_participant)
		VALUES ($1, 'efaktura', 'mps', gen_random_uuid(), repeat('b', 64), now(), now(),
		    gen_random_uuid(), '123456789', gen_random_uuid(), 'efaktura', 'k', '0192:1')`, invoice); !checkViolationOf(err, "ck_transmissions_channel") {
		t.Errorf("an eFaktura with a receiver participant: %v, want ck_transmissions_channel", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoices.transmissions (invoice_id, provider, idempotency_key, pdf_sha256, next_attempt_at, queued_at, created_by_user_id)
		VALUES ($1, 'storecove', gen_random_uuid(), repeat('b', 64), now(), now(), gen_random_uuid())`, invoice); !checkViolationOf(err, "ck_transmissions_channel") {
		t.Errorf("an EHF without its Peppol columns: %v, want ck_transmissions_channel", err)
	}

	// provider_status is a state column; the eFaktura identity is frozen.
	if _, err := pool.Exec(ctx, `UPDATE invoices.transmissions SET provider_status = 'IN_PROGRESS' WHERE id = $1`, efRow); err != nil {
		t.Errorf("a provider status: %v, want it allowed", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices.transmissions SET efaktura_identifier = '987654321' WHERE id = $1`, efRow); !refusedWith(err, "invoices: a transmission is immutable") {
		t.Errorf("a new identifier: %v, want the trigger's refusal", err)
	}

	// The claim.
	var mandate int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices.avtalegiro_mandates (kid, customer_id, status, provider_seen_at, updated_at)
		VALUES ('900000000074', 7, 'active', now(), now()) RETURNING id`).Scan(&mandate); err != nil {
		t.Fatalf("a mandate: %v", err)
	}
	const insertClaim = `
		INSERT INTO invoices.avtalegiro_claims (invoice_id, transmission_id, mandate_id, amount, due_date, kid, submitted_at)
		VALUES ($1, $2, $3, 1000, DATE '2026-09-26', '900000000074', now()) RETURNING id`
	var claim int64
	if err := pool.QueryRow(ctx, insertClaim, invoice, efRow, mandate).Scan(&claim); err != nil {
		t.Fatalf("a claim: %v", err)
	}
	if _, err := pool.Exec(ctx, insertClaim, invoice, efRow, mandate); !isUniqueViolation(err) {
		t.Errorf("a second live claim: %v, want ux_avtalegiro_claims_live's refusal", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices.avtalegiro_claims SET status = 'accepted' WHERE id = $1`, claim); !checkViolationOf(err, "ck_avtalegiro_claims_stamps") {
		t.Errorf("accepted without its stamp: %v, want ck_avtalegiro_claims_stamps", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices.avtalegiro_claims SET status = 'accepted', accepted_at = now() WHERE id = $1`, claim); err != nil {
		t.Errorf("accepting the claim: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices.avtalegiro_claims SET amount = 999 WHERE id = $1`, claim); !refusedWith(err, "invoices: an AvtaleGiro claim is immutable") {
		t.Errorf("a new amount: %v, want the trigger's refusal", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices.avtalegiro_claims SET status = 'paid', paid_at = now() WHERE id = $1`, claim); err != nil {
		t.Errorf("paying the claim: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices.avtalegiro_claims SET attention = true WHERE id = $1`, claim); !refusedWith(err, "invoices: an AvtaleGiro claim is immutable") {
		t.Errorf("changing a paid claim: %v, want the trigger's refusal", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM invoices.avtalegiro_claims WHERE id = $1`, claim); !refusedWith(err, "invoices: an AvtaleGiro claim is never deleted") {
		t.Errorf("deleting a claim: %v, want the trigger's refusal", err)
	}
	// A paid claim is not live: the invoice may carry another.
	if _, err := pool.Exec(ctx, insertClaim, invoice, efRow, mandate); err != nil {
		t.Errorf("a claim after a paid one: %v, want it allowed", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO invoices.consumer_lookups (customer_id, status, checked_at, next_check_at) VALUES (7, 'found', now(), now())`); !checkViolationOf(err, "ck_consumer_lookups_identifier") {
		t.Errorf("found without an identifier: %v, want ck_consumer_lookups_identifier", err)
	}
}

// TestCustomersDateOfBirth_AppliesAndIsIdempotent proves 00202 applies, rolls
// back and re-applies, and that only a person carries a date of birth.
func TestCustomersDateOfBirth_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, customersDateOfBirthVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var person, business int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers.customers (customer_number, name, type, date_of_birth, created_at, updated_at) VALUES (90001, 'Kari Nordmann', 'person', DATE '1980-05-17', now(), now()) RETURNING id`).Scan(&person); err != nil {
		t.Fatalf("a person with a date of birth: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO customers.customers (customer_number, name, type, created_at, updated_at) VALUES (90002, 'Firma AS', 'business', now(), now()) RETURNING id`).Scan(&business); err != nil {
		t.Fatalf("a business: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE customers.customers SET date_of_birth = DATE '1980-05-17' WHERE id = $1`, business); !checkViolationOf(err, "ck_customers_date_of_birth") {
		t.Errorf("a business's date of birth: %v, want ck_customers_date_of_birth", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE customers.customers SET type = 'business' WHERE id = $1`, person); !checkViolationOf(err, "ck_customers_date_of_birth") {
		t.Errorf("a person turned business keeping the date: %v, want ck_customers_date_of_birth", err)
	}
}
