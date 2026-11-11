package manufacturing

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestVariantHolder_CountsOpenOrders is M16 with delivery B: an order that
// is not done or cancelled holds its product and its component lines'
// variants, even when its BoM is archived; a done or cancelled one holds
// nothing.
func TestVariantHolder_CountsOpenOrders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, _ := testdb.Migrated(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO manufacturing.boms (id, variant_id, output_quantity, status, created_at, updated_at)
	      OVERRIDING SYSTEM VALUE VALUES (1, 50, 1, 'archived', now(), now())`)
	order := func(id, number int64, variant int32, status string) {
		exec(`INSERT INTO manufacturing.orders (id, number, variant_id, bom_id, quantity, site_id, output_location_id, status, created_at, updated_at)
		      OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, 1, 1, 1, 10, $4, now(), now())`, id, number, variant, status)
	}
	order(1, 1, 50, "in_progress")
	order(2, 2, 60, "done")
	order(3, 3, 61, "cancelled")
	exec(`INSERT INTO manufacturing.order_components (order_id, variant_id, sku, product_name, unit, required) VALUES (1, 51, 'C', 'C', 'pcs', 1), (2, 62, 'D', 'D', 'pcs', 1)`)

	holder := newVariantHolder(module.Deps{})
	for _, tc := range []struct {
		variant int32
		held    bool
	}{{50, true}, {51, true}, {60, false}, {61, false}, {62, false}} {
		var hold *contracts.VariantHold
		err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			hold, err = holder.VariantHeld(ctx, tx, tc.variant, contracts.VariantHoldDelete)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if (hold != nil) != tc.held || (hold != nil && hold.Documents != 1) {
			t.Errorf("VariantHeld(%d) = %+v, want held %v", tc.variant, hold, tc.held)
		}
	}
}
