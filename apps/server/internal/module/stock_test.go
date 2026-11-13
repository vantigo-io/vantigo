package module

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

type fakeStock struct{}

func (fakeStock) OnHand(context.Context, []int32, *int64) ([]contracts.StockQuantity, error) {
	return nil, nil
}

func (fakeStock) Available(context.Context, []int32, *int64) ([]contracts.StockQuantity, error) {
	return nil, nil
}

func (fakeStock) Sites(context.Context) ([]contracts.StockSite, error) { return nil, nil }

func (fakeStock) Locations(context.Context, []int32, int64) ([]contracts.StockLocation, error) {
	return nil, nil
}

func (fakeStock) SiteLocations(context.Context, int64) ([]contracts.StockSiteLocation, error) {
	return nil, nil
}

func (fakeStock) Lots(context.Context, []int32, int64) ([]contracts.StockLot, error) { return nil, nil }

func (fakeStock) LotRefs(context.Context, []int64) ([]contracts.LotRef, error) { return nil, nil }

func (fakeStock) Placements(context.Context, int64) ([]contracts.StockPlacement, error) {
	return nil, nil
}

type fakeStockLedger struct{}

func (fakeStockLedger) Reserve(context.Context, pgx.Tx, contracts.StockSource, int64, time.Time, []contracts.StockLine) error {
	return nil
}

func (fakeStockLedger) Release(context.Context, pgx.Tx, contracts.StockSource, []contracts.StockRelease) error {
	return nil
}

func (fakeStockLedger) Issue(context.Context, pgx.Tx, contracts.StockSource, contracts.StockIssue) (contracts.StockPosted, error) {
	return contracts.StockPosted{}, nil
}

func (fakeStockLedger) ReservationsFor(context.Context, pgx.Tx, contracts.StockSource) ([]contracts.Reservation, error) {
	return nil, nil
}

func (fakeStockLedger) ReserveForProduction(context.Context, pgx.Tx, contracts.StockSource, int64, time.Time, []contracts.StockLine) error {
	return nil
}

func (fakeStockLedger) Produce(context.Context, pgx.Tx, contracts.StockSource, contracts.StockProduction) (contracts.StockPosted, error) {
	return contracts.StockPosted{}, nil
}

func (fakeStockLedger) EnsureLots(context.Context, pgx.Tx, contracts.StockSource, time.Time, []contracts.NewLot) ([]int64, error) {
	return nil, nil
}

func (fakeStockLedger) HoldLots(context.Context, pgx.Tx, contracts.StockSource, []contracts.LotHold) error {
	return nil
}

func (fakeStockLedger) ReleaseLotHolds(context.Context, pgx.Tx, contracts.StockSource, []int64, contracts.LotActor) error {
	return nil
}

// stockModule is a module providing both stock slots, as Inventory does.
func stockModule(name string) Module {
	return Module{
		Name:        name,
		Stock:       func(Deps) contracts.Stock { return fakeStock{} },
		StockLedger: func(Deps) contracts.StockLedger { return fakeStockLedger{} },
		Mount:       staticHandler(name),
	}
}

// With the stock provider disabled, Deps.Stock is nil — "stock is not kept"
// — while Deps.StockLedger is still composed, so reservations made while it
// was on are released (orders design O45, rule 24).
func TestCompose_StockLedgerComposedWhateverModulesSays(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		modules := []string{"alpha"}
		if enabled {
			modules = append(modules, "beta")
		}
		var got Deps
		_, err := compose(
			Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: modules}},
			fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
			Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
				got = d
				return staticHandler("alpha")(d)
			}},
			stockModule("beta"),
		)
		if err != nil {
			t.Fatalf("compose (beta enabled %v): %v", enabled, err)
		}
		if got.StockLedger == nil {
			t.Errorf("beta enabled %v: Deps.StockLedger is nil, want the ledger whatever MODULES says", enabled)
		}
		if (got.Stock != nil) != enabled {
			t.Errorf("beta enabled %v: Deps.Stock = %v", enabled, got.Stock)
		}
	}
}

// Two modules given that both provide a stock ledger are refused, the
// disabled one counted too.
func TestCompose_TwoStockLedgersRefused(t *testing.T) {
	_, err := compose(
		Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}},
		fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		stockModule("alpha"),
		stockModule("beta"),
	)
	if err == nil || !strings.Contains(err.Error(), "stock ledger") {
		t.Fatalf("compose = %v, want the two stock ledgers refused", err)
	}
}

// Workers resolves both slots as Compose does: the ledger from every module
// given, the read from the enabled ones.
func TestWorkers_ResolvesStock(t *testing.T) {
	var got Deps
	Workers(Deps{Config: &config.Config{Modules: []string{"alpha"}}},
		Module{Name: "alpha", Workers: func(d Deps) []worker.Worker { got = d; return nil }},
		stockModule("beta"),
	)
	if got.StockLedger == nil || got.Stock != nil {
		t.Errorf("Workers' Deps: StockLedger %v, Stock %v; want the ledger and no read", got.StockLedger, got.Stock)
	}
}

type fakeSalesOrderLines struct{}

func (fakeSalesOrderLines) Lines(context.Context, []int64) ([]contracts.SalesOrderLine, error) {
	return nil, nil
}

func (fakeSalesOrderLines) OrderLines(context.Context, int64) ([]contracts.SalesOrderLine, error) {
	return nil, nil
}

func (fakeSalesOrderLines) Unreserved(context.Context, []int32, int64) ([]contracts.UnreservedDemand, error) {
	return nil, nil
}

type fakeProduction struct{}

func (fakeProduction) ForOrderLines(context.Context, []int64) ([]contracts.LineProduction, error) {
	return nil, nil
}

func (fakeProduction) HasBOM(context.Context, []int32) (map[int32]bool, error) { return nil, nil }
func (fakeProduction) NotMade(context.Context, int64) ([]contracts.LineNotMade, error) {
	return nil, nil
}

type fakeDeliveryTrace struct{}

func (fakeDeliveryTrace) Deliveries(context.Context, []int64) ([]contracts.TracedDelivery, error) {
	return nil, nil
}

// The production reads and the delivery trace resolve from the enabled modules only: with the
// provider of each disabled, its slot is nil (manufacturing design M43, M46).
func TestWorkers_ResolvesProductionReadsFromEnabledModules(t *testing.T) {
	t.Parallel()
	var seen Deps
	consumer := Module{Name: "consumer", Workers: func(d Deps) []worker.Worker { seen = d; return nil }}
	lines := Module{Name: "lines", SalesOrderLines: func(Deps) contracts.SalesOrderLines { return fakeSalesOrderLines{} }}
	production := Module{Name: "production", Production: func(Deps) contracts.Production { return fakeProduction{} }}
	trace := Module{Name: "trace", DeliveryTrace: func(Deps) contracts.DeliveryTrace { return fakeDeliveryTrace{} }}

	Workers(Deps{Config: &config.Config{Modules: []string{"consumer", "lines", "production", "trace"}}}, consumer, lines, production, trace)
	if seen.SalesOrderLines == nil || seen.Production == nil || seen.DeliveryTrace == nil {
		t.Fatalf("Workers left a production read unset: lines %v, production %v, trace %v", seen.SalesOrderLines, seen.Production, seen.DeliveryTrace)
	}

	seen = Deps{}
	Workers(Deps{Config: &config.Config{Modules: []string{"consumer"}}}, consumer, lines, production, trace)
	if seen.SalesOrderLines != nil || seen.Production != nil || seen.DeliveryTrace != nil {
		t.Fatal("Workers resolved a production read from a disabled module")
	}
}
