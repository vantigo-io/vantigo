package main

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// assertDemoProposals reads the demo's purchase proposals through
// Purchasing's own operation (delivery C): Manufacturing's buy suggestions
// are their source, and the screws, the plywood and the powder paint are
// proposed, each with its supplier item to order it from.
func assertDemoProposals(t *testing.T, ctx context.Context, databaseURL string) {
	t.Helper()
	cfg, err := config.FromOS()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	deps, access, err := moduleDeps(cfg, pool, slog.Default(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := purchasing.Operations(module.WorkerDeps(deps, businessModules(access)...))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ops.ListPurchasingProposals(ctx, gen.ListPurchasingProposalsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	list, ok := res.(gen.ListPurchasingProposals200JSONResponse)
	if !ok {
		t.Fatalf("the proposals answered %T %+v", res, res)
	}
	if list.Source == nil || *list.Source != "replenishment" {
		t.Errorf("the proposals' source = %v, want Manufacturing's buy suggestions", list.Source)
	}
	var proposed []string
	for _, r := range list.Rows {
		if len(r.Options) > 0 {
			proposed = append(proposed, r.Sku)
		}
	}
	for _, sku := range []string{"VRK-SKRUE-M8", "VRK-FINER-18", "VRK-LAKK-7016"} {
		if !slices.Contains(proposed, sku) {
			t.Errorf("%s is not proposed with a supplier item; proposed: %v", sku, proposed)
		}
	}
}
