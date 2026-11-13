package manufacturing_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
)

// kitInput is a kit BoM of variant making output with lines.
func kitInput(variant int32, output string, lines ...gen.ManufacturingBomLineInput) gen.ManufacturingBomInput {
	in := bomInput(variant, output, lines...)
	kind := gen.ManufacturingBomKindKit
	in.Kind = &kind
	return in
}

// A kit BoM has lines only: operations, by-products and make-to-order are
// each refused kit_not_manufactured; it is read back as a kit, its kind kept
// by an update; and no manufacturing order is made of it (M144).
func TestKits_KitBomRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))

	in := kitInput(varBike, "1", line(varFrame, "1"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("cut", 10, saw.Id, "3")}
	p := expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "kit_not_manufactured", "a kit with an operation")
	if p.Errors == nil || (*p.Errors)["operations"] == nil {
		t.Errorf("errors %v, want operations", p.Errors)
	}
	in = kitInput(varBike, "1", line(varFrame, "1"))
	in.Outputs = &[]gen.ManufacturingBomOutputInput{{VariantId: varBolt, Quantity: "1"}}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "kit_not_manufactured", "a kit with a by-product")
	in = kitInput(varBike, "1", line(varFrame, "1"))
	in.MakeToOrder = new(bool)
	*in.MakeToOrder = true
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "kit_not_manufactured", "a kit made to order")

	kit := createBom(t, c, kitInput(varBike, "1", line(varFrame, "1"), line(varWheel, "2")))
	if kit.Kind != gen.ManufacturingBomKindKit || !kit.IsDefault || len(kit.Lines) != 2 {
		t.Fatalf("kit %+v", kit)
	}
	normal := createBom(t, c, bomInput(varFrame, "1", line(varTube, "3")))
	if normal.Kind != gen.ManufacturingBomKindNormal {
		t.Errorf("a BoM sent without a kind is %q, want normal", normal.Kind)
	}
	u := updateOf(kit)
	u.Lines = append(u.Lines, line(varBolt, "4"))
	res := c.Do(http.MethodPut, bomPath(kit.Id), u)
	mustStatus(t, res, http.StatusOK, "update the kit")
	if got := bomOf(t, res); got.Kind != gen.ManufacturingBomKindKit || len(got.Lines) != 3 {
		t.Errorf("updated kit %+v", got)
	}
	u = updateOf(bomOf(t, res))
	u.Operations = []gen.ManufacturingBomOperationInput{operation("cut", 10, saw.Id, "3")}
	expectRefusal(t, c.Do(http.MethodPut, bomPath(kit.Id), u), http.StatusBadRequest, "kit_not_manufactured", "an operation added to a kit")

	site := siteMain
	o := orderInput(varBike, "1")
	o.SiteId = &site
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, o), http.StatusBadRequest, "kit_not_manufactured", "an order of a kit")
	o.BomId = &kit.Id
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, o), http.StatusBadRequest, "kit_not_manufactured", "an order naming the kit")

	if l := c.Do(http.MethodGet, bomsPath, nil); l.Status == http.StatusOK {
		var list gen.ManufacturingBomList
		l.JSON(&list)
		for _, item := range list.Items {
			if item.Id == kit.Id && item.Kind != gen.ManufacturingBomKindKit {
				t.Errorf("listed kit %+v", item)
			}
		}
	}
}

// Kits nest at most three levels, counted down through the components'
// default kits and up through the kits that name the variant (M144).
func TestKits_NestAtMostThreeLevels(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)

	createBom(t, c, kitInput(varWheel, "1", line(varTube, "2")))
	createBom(t, c, kitInput(varFrame, "1", line(varWheel, "1")))
	createBom(t, c, kitInput(varBike, "1", line(varFrame, "1")))
	// A fourth kit above the three.
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, kitInput(varPaint, "1", line(varBike, "1"))), http.StatusBadRequest,
		"bom_too_deep", "a kit above three")
	// A fourth below them: the tube's kit would be under the wheel's.
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, kitInput(varTube, "1", line(varBolt, "1"))), http.StatusBadRequest,
		"bom_too_deep", "a kit below three")
	// Not its variant's default, a kit counts only what is below it.
	createBom(t, c, bomInput(varTube, "1", line(varBolt, "1")))
	if b := createBom(t, c, kitInput(varTube, "1", line(varBolt, "1"))); b.IsDefault {
		t.Errorf("a second BoM of the tube became its default")
	}
	// A normal BoM above the kits nests none.
	createBom(t, c, bomInput(varPaint, "1", line(varBike, "1")))
}

// KitStructure answers each kit variant's components exploded through nested
// kits, quantities multiplied through and a variant reached twice summed in
// the order first reached; a variant with no kit, or a normal default BoM, is
// absent. It is ungated: a context with no principal reads it (M145).
func TestKits_KitStructureExplodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)

	createBom(t, c, kitInput(varWheel, "1", line(varTube, "2"), line(varBolt, "4")))
	createBom(t, c, kitInput(varFrame, "2", line(varWheel, "1"), line(varPaint, "1")))
	createBom(t, c, kitInput(varBike, "1", line(varFrame, "2"), line(varBolt, "1")))
	createBom(t, c, bomInput(varTube, "1", line(varPaint, "0.1")))

	kits := manufacturing.Module().KitStructure(h.Deps())
	got, err := kits.Kits(context.Background(), []int32{varBike, varFrame, varTube, varService})
	if err != nil {
		t.Fatalf("Kits: %v", err)
	}
	want := map[int32][]contracts.KitComponent{
		varBike:  {{VariantID: varTube, PerKit: "2"}, {VariantID: varBolt, PerKit: "5"}, {VariantID: varPaint, PerKit: "1"}},
		varFrame: {{VariantID: varTube, PerKit: "1"}, {VariantID: varBolt, PerKit: "2"}, {VariantID: varPaint, PerKit: "0.5"}},
	}
	if len(got) != len(want) {
		t.Fatalf("kits %v, want %v", got, want)
	}
	for v, comps := range want {
		if !slices.Equal(got[v], comps) {
			t.Errorf("kit %d = %v, want %v", v, got[v], comps)
		}
	}
}

// A kit's line whose quantity per kit — its quantity over the output
// quantity — is 0 at three decimals is refused quantity_invalid on the line,
// at a create and at an update; 0.0005 rounds half-up to 0.001 and is kept
// (M144, M145).
func TestKits_LineThatRoundsToNothingRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)

	p := expectRefusal(t, c.Do(http.MethodPost, bomsPath, kitInput(varBike, "1000", line(varFrame, "0.4"), line(varWheel, "2000"))),
		http.StatusBadRequest, "quantity_invalid", "a line of nothing per kit")
	if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].Index != 0 {
		t.Errorf("lines %+v, want the first line alone", p.Lines)
	}
	kit := createBom(t, c, kitInput(varBike, "1000", line(varFrame, "0.5"), line(varWheel, "2000")))
	u := updateOf(kit)
	u.Lines = append(u.Lines, line(varBolt, "0.1"))
	p = expectRefusal(t, c.Do(http.MethodPut, bomPath(kit.Id), u), http.StatusBadRequest, "quantity_invalid", "a line of nothing added")
	if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].Index != 2 {
		t.Errorf("lines %+v, want the third line alone", p.Lines)
	}
	// A normal BoM is not a kit: the same line is kept.
	createBom(t, c, bomInput(varFrame, "1000", line(varTube, "0.4")))
}

// Replenishment suggests nothing for a kit, neither to make nor to buy: it
// is sold as its components, each suggested for itself (M144).
func TestKits_ReplenishmentSkipsKits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	createBom(t, c, kitInput(varBike, "1", line(varFrame, "1"), line(varWheel, "2")))
	h.stock.setPlacements(siteMain,
		contracts.StockPlacement{VariantID: varBike, ReorderPoint: "10", OnHand: "0", Reserved: "0", Incoming: "0"},
		contracts.StockPlacement{VariantID: varWheel, ReorderPoint: "10", OnHand: "0", Reserved: "0", Incoming: "0"},
	)
	runReplenishment(t, h)
	got := suggestions(t, c, siteMain)
	if _, ok := got[varBike]; ok {
		t.Errorf("the kit was suggested: %+v", got[varBike])
	}
	if w, ok := got[varWheel]; !ok || w.Kind != gen.Buy {
		t.Errorf("suggestions %+v, want the wheel bought", got)
	}
}
