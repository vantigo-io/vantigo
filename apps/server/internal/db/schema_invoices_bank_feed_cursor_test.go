package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// bankFeedCursorVersion is 00179_invoices_bank_feed_cursor.sql, the bank
// feed's read cursor and the bank's wait.
const bankFeedCursorVersion = 179

// TestInvoicesBankFeedCursor_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins its two columns on
// invoices.bank_feed_accounts: read_through, a nullable date, and
// rate_limited_until, a nullable timestamptz. The down drops both.
func TestInvoicesBankFeedCursor_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, bankFeedCursorVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	columns := func() []string {
		return staffCollect(t, ctx, pool, "the cursor columns", `
			SELECT column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'invoices' AND table_name = 'bank_feed_accounts'
			  AND column_name IN ('read_through', 'rate_limited_until')`)
	}
	want := []string{"rate_limited_until:timestamp with time zone:YES:", "read_through:date:YES:"}
	if got := columns(); !equalStrings(got, want) {
		t.Errorf("the cursor columns = %v, want %v", got, want)
	}
	migrateTo(t, url, bankFeedCursorVersion-1)
	if got := columns(); len(got) != 0 {
		t.Errorf("the cursor columns after the down = %v, want none", got)
	}
	migrateTo(t, url, bankFeedCursorVersion)
	if got := columns(); !equalStrings(got, want) {
		t.Errorf("the cursor columns after up again = %v, want %v", got, want)
	}
}
