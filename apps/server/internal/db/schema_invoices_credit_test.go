package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// 00197_invoices_credit.sql (invoices phase 6 design I8-I16, plan delivery
// B-1).
const invoicesCreditVersion = 197

// The backfill (I10): an issued invoice a credit note took below zero open
// before the migration moves its surplus to its customer, unattributed,
// naming the latest credit note and dated its issue; every payment takes
// its amount as amount_nok; settled_amount subtracts what moved, so the
// invoice reads 0 open; the movement writes its fact at the migration's
// commit. The Down refuses while a movement exists.
func TestInvoicesCredit_TheBackfill(t *testing.T) {
	t.Parallel()
	url := testdb.URL(t)
	migrateTo(t, url, invoicesCreditVersion-1)
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	f := &receivablesFixture{t: t, ctx: ctx, pool: pool, next: 1}
	invoice := f.issued(7)
	if _, err := journalPayment(ctx, pool, invoice, time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	credit := f.issue(f.draft(7, invoice))
	untouched := f.issued(8)

	migrateTo(t, url, invoicesCreditVersion)
	if got := f.text(`SELECT string_agg(customer_id || ' ' || kind || ' ' || amount || ' ' || amount_nok || ' ' || document_id || ' ' ||
		credit_day || ' ' || (registered_by_user_id IS NULL), ',') FROM invoices.customer_credits WHERE invoice_id = $1`, invoice); got != fmt.Sprintf("7 credit_note 1.00 1.00 %d 2026-10-05 true", credit) {
		t.Errorf("the backfilled movement = %q", got)
	}
	if n := f.text(`SELECT count(*)::text FROM invoices.customer_credits WHERE invoice_id = $1`, untouched); n != "0" {
		t.Errorf("an invoice not below zero moved %s", n)
	}
	if got := f.text(`SELECT invoices.settled_amount($1)::text`, invoice); got != "0.00" {
		t.Errorf("settled_amount = %s, want 1 paid less 1 moved", got)
	}
	if got := f.text(`SELECT string_agg(amount_nok::text, ',') FROM invoices.payments`); got != "1.00" {
		t.Errorf("amount_nok = %s, want the payment's amount", got)
	}
	if got := f.text(`SELECT count(*)::text FROM invoices.journal_facts WHERE kind = 'credit' AND customer_id = 7 AND invoice_id = $1`, invoice); got != "1" {
		t.Errorf("credit facts = %s, want the backfilled one", got)
	}
	if err := migrateDown(t, url, invoicesCreditVersion-1); !refusedWith(err, "invoices: customer credit exists") {
		t.Errorf("the Down with credit: %v, want it refused", err)
	}
}

// The migration applies on an empty database, rolls back and applies again.
func TestInvoicesCredit_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, invoicesCreditVersion)
}
