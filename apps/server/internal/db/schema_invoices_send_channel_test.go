package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// invoicesSendChannelVersion is 00096_invoices_send_channel.sql, the channel
// Invoices sends documents and reminder letters from (integrations design T6).
const invoicesSendChannelVersion = 96

// TestInvoicesSendChannel_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly: invoices.settings gains a nullable uuid
// send_channel, null on the existing row (Disabled), holding any id (no foreign
// key across schemas) and clearing back to null.
func TestInvoicesSendChannel_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, invoicesSendChannelVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := staffCollect(t, ctx, pool, "invoices.settings.send_channel", `
		SELECT data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'invoices' AND table_name = 'settings' AND column_name = 'send_channel'`)
	if !equalStrings(columns, []string{"uuid:YES:"}) {
		t.Errorf("invoices.settings.send_channel = %v, want a nullable uuid with no default", columns)
	}
	var channel *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT send_channel FROM invoices.settings WHERE id = 1`).Scan(&channel); err != nil {
		t.Fatalf("read the settings row: %v", err)
	}
	if channel != nil {
		t.Errorf("send_channel = %v on the existing row, want null (Disabled)", channel)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices.settings SET send_channel = $1 WHERE id = 1`, uuid.New()); err != nil {
		t.Errorf("a channel id of another schema: %v, want it held with no foreign key", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices.settings SET send_channel = NULL WHERE id = 1`); err != nil {
		t.Errorf("clearing the channel: %v", err)
	}
	var fks int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.conrelid = 'invoices.settings'::regclass AND c.contype = 'f' AND a.attname = 'send_channel'`).Scan(&fks); err != nil {
		t.Fatalf("count foreign keys: %v", err)
	}
	if fks != 0 {
		t.Errorf("send_channel carries %d foreign keys, want none (rule 4)", fks)
	}
}
