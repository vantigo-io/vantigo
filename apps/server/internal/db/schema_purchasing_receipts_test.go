package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// purchasingReceiptsVersion is 00190_purchasing_receipts.sql, Purchasing's
// receipts (docs/superpowers/specs/2026-11-17-purchasing-design.md, §4).
const purchasingReceiptsVersion = 190

// TestPurchasingReceipts_DownDeletesTheReceiptCounter proves the receipts'
// down path takes the receipt series' counter row with the tables, so an Up
// after it numbers receipts from 1 again, and leaves the order series' row.
func TestPurchasingReceipts_DownDeletesTheReceiptCounter(t *testing.T) {
	t.Parallel()
	url := testdb.URL(t)
	migrateTo(t, url, purchasingReceiptsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO purchasing.counters (counter_name, next_value) VALUES ('receipt', 7), ('order', 3001)
		ON CONFLICT (counter_name) DO UPDATE SET next_value = excluded.next_value`); err != nil {
		t.Fatalf("insert the counters: %v", err)
	}
	pool.Close()

	migrateTo(t, url, purchasingReceiptsVersion-1)

	pool, err = db.Open(ctx, url)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer pool.Close()
	var receipt, order int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE counter_name = 'receipt'), count(*) FILTER (WHERE counter_name = 'order')
		FROM purchasing.counters`).Scan(&receipt, &order); err != nil {
		t.Fatalf("read the counters: %v", err)
	}
	if receipt != 0 || order != 1 {
		t.Errorf("after the Down: receipt counter rows %d, order %d; want 0 and 1", receipt, order)
	}
	migrateTo(t, url, purchasingReceiptsVersion)
}
