package manufacturing

import (
	"errors"
	"math/big"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// TestSplitCost_PartsSumToTheTotal is M122's rounding: each by-product's part
// is rounded once and the main product takes the rounded total less them, so
// the parts as shown sum to the total as shown.
func TestSplitCost_PartsSumToTheTotal(t *testing.T) {
	total := mustRat("10.01")
	outputs := []store.ManufacturingOrderOutput{
		{ID: 1, CostShare: numeric(mustRat("33.33"), percentPlaces), Produced: numeric(big.NewRat(1, 1), quantityPlaces)},
		{ID: 2, CostShare: numeric(mustRat("33.33"), percentPlaces), Produced: numeric(big.NewRat(1, 1), quantityPlaces)},
		{ID: 3, CostShare: numeric(mustRat("10"), percentPlaces)},
	}
	main, parts := splitCost(total, outputs)
	if amountText(main) != "3.33" || parts[0].Amount != "3.34" || parts[1].Amount != "3.34" || parts[2].Amount != "1.00" || !parts[2].NotProduced {
		t.Fatalf("main %s, parts %+v", amountText(main), parts)
	}
	sum := new(big.Rat).Set(main)
	for _, p := range parts[:2] {
		sum.Add(sum, mustRat(p.Amount))
	}
	if amountText(sum) != "10.01" {
		t.Errorf("the parts sum to %s, want 10.01", amountText(sum))
	}
}

// TestProductionRefusal_NamesTheByproduct is M121's refusal mapping: a
// produce line past the consume lines and the main product's lines names the
// by-product at its index in the request's outputs, never a component line
// whose id it shares.
func TestProductionRefusal_NamesTheByproduct(t *testing.T) {
	components := []store.ManufacturingOrderComponent{{ID: 1001, VariantID: 7}}
	refs := []consumeRef{{componentID: 1001, row: -1}}
	made := []byproductMade{{index: 2, output: store.ManufacturingOrderOutput{ID: 1001, VariantID: 7}}}
	r := &contracts.StockRefusal{Code: contracts.StockLotUnknown, Detail: "No such lot.", Lines: []contracts.StockLineRefusal{
		{Code: contracts.StockInsufficientStock, VariantID: 7, LineID: 1001, Index: 0},
		{Code: contracts.StockLotUnknown, VariantID: 7, LineID: 1001, Index: 2, LotID: 9},
	}}
	err := productionRefusal(r, components, refs, made, 1)
	var ref *refusal
	if !errors.As(err, &ref) || ref.problem.Lines == nil || len(*ref.problem.Lines) != 2 {
		t.Fatalf("refusal = %v", err)
	}
	lines := *ref.problem.Lines
	if lines[0].ComponentId == nil || *lines[0].ComponentId != 1001 || lines[0].OutputId != nil {
		t.Errorf("the consume line = %+v", lines[0])
	}
	by := lines[1]
	if by.ComponentId != nil || by.OutputId == nil || *by.OutputId != 1001 || by.Index != 2 || by.LotId == nil || *by.LotId != 9 {
		t.Errorf("the by-product's line = %+v", by)
	}
}
