package module

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

type fakePurchaseSupply struct{}

func (fakePurchaseSupply) OpenByVariants(context.Context, *int64, []int32) ([]contracts.OpenPurchase, error) {
	return nil, nil
}
func (fakePurchaseSupply) OpenAll(context.Context) ([]contracts.OpenPurchase, error) { return nil, nil }

type fakeSupplierHolder struct{}

func (fakeSupplierHolder) SupplierHeld(context.Context, pgx.Tx, int64, contracts.SupplierHoldPurpose) (*contracts.SupplierHold, error) {
	return nil, nil
}

type fakeSupplierData struct{}

func (fakeSupplierData) ExportSupplierData(context.Context, int64) (any, error) { return nil, nil }
func (fakeSupplierData) EraseSupplierData(context.Context, pgx.Tx, int64) ([]contracts.ErasedData, error) {
	return nil, nil
}

// The supplier slots (purchasing design P4, P7, P10): the open purchases
// from the enabled modules only — with Purchasing off, Inventory and
// Manufacturing read nil as "nothing is on order" — and the holders and the
// personal data from every module given, under its name, as a worker sees
// them too.
func TestWorkers_ResolvesSupplierSlots(t *testing.T) {
	t.Parallel()
	purchasing := Module{Name: "purchasing",
		PurchaseSupply:       func(Deps) contracts.PurchaseSupply { return fakePurchaseSupply{} },
		SupplierReferences:   func(Deps) contracts.SupplierReferenceHolder { return fakeSupplierHolder{} },
		SupplierPersonalData: func(Deps) contracts.SupplierPersonalData { return fakeSupplierData{} },
	}
	for _, enabled := range []bool{true, false} {
		var seen Deps
		consumer := Module{Name: "consumer", Workers: func(d Deps) []worker.Worker { seen = d; return nil }}
		modules := []string{"consumer"}
		if enabled {
			modules = append(modules, "purchasing")
		}
		Workers(Deps{Config: &config.Config{Modules: modules}}, consumer, purchasing)
		if (seen.PurchaseSupply != nil) != enabled {
			t.Errorf("purchasing enabled %v: PurchaseSupply = %v", enabled, seen.PurchaseSupply)
		}
		if len(seen.SupplierReferenceHolders) != 1 || len(seen.SupplierPersonalData) != 1 || seen.SupplierPersonalData[0].Module != "purchasing" {
			t.Errorf("purchasing enabled %v: holders %v, personal data %v; want each once whatever MODULES says",
				enabled, seen.SupplierReferenceHolders, seen.SupplierPersonalData)
		}
	}
}

// Two enabled modules that both provide open purchases are a panic in
// Workers, as for every single-provider slot.
func TestWorkers_TwoPurchaseSuppliesPanic(t *testing.T) {
	t.Parallel()
	supply := func(name string) Module {
		return Module{Name: name, PurchaseSupply: func(Deps) contracts.PurchaseSupply { return fakePurchaseSupply{} }}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Workers did not panic on two purchase supplies")
		}
	}()
	Workers(Deps{Config: &config.Config{Modules: []string{"a", "b"}}}, supply("a"), supply("b"))
}

type fakePurchaseDemand struct{ source string }

func (fakePurchaseDemand) Demand(context.Context, *int64) ([]contracts.PurchaseNeed, error) {
	return nil, nil
}

// The purchase demand (purchasing design A4) as Compose builds it: one
// source, Manufacturing's buy suggestions where Manufacturing is enabled and
// Inventory's low stock where it is not, and none with neither — seen by the
// module that reads it, Purchasing, on its own Deps.
func TestCompose_PurchaseDemandByPrecedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		enabled []string
		want    string
	}{
		{[]string{"inventory", "manufacturing", "purchasing"}, "manufacturing"},
		{[]string{"manufacturing", "purchasing"}, "manufacturing"},
		{[]string{"inventory", "purchasing"}, "inventory"},
		{[]string{"purchasing"}, ""},
	} {
		var seen Deps
		mods := []Module{
			{Name: "identity", Mount: func(Deps) (http.Handler, error) { return http.NotFoundHandler(), nil }},
			{Name: "inventory", Mount: func(Deps) (http.Handler, error) { return http.NotFoundHandler(), nil },
				PurchaseDemand: func(Deps) contracts.PurchaseDemand { return fakePurchaseDemand{"inventory"} }},
			{Name: "manufacturing", Mount: func(Deps) (http.Handler, error) { return http.NotFoundHandler(), nil },
				PurchaseDemand:           func(Deps) contracts.PurchaseDemand { return fakePurchaseDemand{"manufacturing"} },
				PurchaseDemandSupersedes: []string{"inventory"}},
			{Name: "purchasing", Mount: func(d Deps) (http.Handler, error) { seen = d; return http.NotFoundHandler(), nil }},
		}
		if _, err := Compose(Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: tc.enabled}}, mods...); err != nil {
			t.Fatalf("%v: Compose: %v", tc.enabled, err)
		}
		got := ""
		if f, ok := seen.PurchaseDemand.(fakePurchaseDemand); ok {
			got = f.source
		}
		if got != tc.want {
			t.Errorf("%v enabled: the demand is %q's, want %q's", tc.enabled, got, tc.want)
		}
	}
}

// Two enabled providers of a purchase demand that neither supersedes are a
// failure, as for every single-provider slot.
func TestCompose_TwoPurchaseDemandsFail(t *testing.T) {
	t.Parallel()
	demand := func(name string) Module {
		return Module{Name: name, PurchaseDemand: func(Deps) contracts.PurchaseDemand { return fakePurchaseDemand{name} }}
	}
	if _, err := demandProvider([]Module{demand("a"), demand("b")}); err == nil {
		t.Fatal("two purchase demands resolved")
	}
}
