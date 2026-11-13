package orders_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// varKit is a kit sold at NOK 500: four bolts and one and a half litres of
// milk, a tracked component (manufacturing phase 2 M145).
const varKit int32 = 301

// fakeKits is Manufacturing's contracts.KitStructure: the kits it answers and
// the variants each call asked about.
type fakeKits struct {
	mu    sync.Mutex
	kits  map[int32][]contracts.KitComponent
	asked [][]int32
}

func (f *fakeKits) Kits(_ context.Context, ids []int32) (map[int32][]contracts.KitComponent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, slices.Clone(ids))
	out := map[int32][]contracts.KitComponent{}
	for _, id := range ids {
		if comps, ok := f.kits[id]; ok {
			out[id] = slices.Clone(comps)
		}
	}
	return out, nil
}

func (f *fakeKits) set(kit int32, comps ...contracts.KitComponent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kits[kit] = comps
}

// toolbox is the kit's composition.
func toolbox() *fakeKits {
	return &fakeKits{kits: map[int32][]contracts.KitComponent{
		varKit: {{VariantID: varBolt, PerKit: "4"}, {VariantID: varLot, PerKit: "1.5"}},
	}}
}

// childrenOf is the order's child lines of the kit line, in order.
func childrenOf(o gen.OrdersOrder, kit int64) []gen.OrdersLine {
	var out []gen.OrdersLine
	for _, l := range o.Lines {
		if l.ParentLineId != nil && *l.ParentLineId == kit {
			out = append(out, l)
		}
	}
	return out
}

// kitLines is o's kit lines as the editor saves them: the kit at qty, its
// children left out.
func kitLines(o gen.OrdersOrder, qty float64) []gen.OrdersLineInput {
	var out []gen.OrdersLineInput
	for _, l := range keepAt(o, varKit, qty) {
		if l.VariantId != nil && *l.VariantId == varKit {
			out = append(out, l)
		}
	}
	return out
}

// kitShape is the kit line and its children as "sku qty@perKit price" lines.
func kitShape(t *testing.T, o gen.OrdersOrder) []string {
	t.Helper()
	kit := lineOf(t, o, varKit)
	out := []string{fmt.Sprintf("%s %v %v", *kit.Sku, *kit.Quantity, *kit.UnitPrice)}
	for _, c := range childrenOf(o, kit.Id) {
		out = append(out, fmt.Sprintf("%s %v@%v %v %v", *c.Sku, *c.Quantity, *c.PerKit, *c.UnitPrice, c.LineNet))
	}
	return out
}

// A kit line added to an order brings a child line per component from
// KitStructure, after it, priced 0; the kit carries the price. A save sends
// the kit alone — or with its children, which are left out — and rescales
// them under their ids; a removed kit takes them with it (M145).
func TestKits_ChildLinesCopiedAndRescaled(t *testing.T) {
	t.Parallel()
	kits := toolbox()
	h := newHarness(t, harnessOptions{stock: true, kits: kits})
	seedTracked(h)
	h.catalog.seed(contracts.VariantEntry{ID: varKit, ProductID: varKit, ProductName: "Toolbox kit", SKU: "KIT-1", Unit: "pcs",
		ProductType: "Goods", ProductStatus: "Active", TaxCategoryID: catStandard})
	h.catalog.setPrice(varKit, "NOK", "500")
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)

	o := createOrder(t, c, custBusiness, productLine(varKit, 2), productLine(varFood, 1))
	if got, want := kitShape(t, o), []string{"KIT-1 2 500", "BOLT-M8 8@4 0 0", "MILK-1 3@1.5 0 0"}; !slices.Equal(got, want) {
		t.Fatalf("kit lines %v, want %v", got, want)
	}
	if len(o.Lines) != 4 || *o.Lines[3].VariantId != varFood || o.Lines[1].Position != 2 {
		t.Fatalf("the children follow their kit: %+v", o.Lines)
	}
	if o.Totals.Net != 1033.34 {
		t.Errorf("net %v, want the kit's 1000 and the coffee's 33.34", o.Totals.Net)
	}
	kit := lineOf(t, o, varKit)
	children := childrenOf(o, kit.Id)
	for _, ch := range children {
		if ch.DeliveryMethod != gen.OrdersLineDeliveryMethodDelivery || ch.VatCodeId == nil || *ch.VatCodeId != codeStandard {
			t.Errorf("child %+v, want delivered by delivery at the kit's VAT code", ch)
		}
	}
	if len(kits.asked) != 1 || !slices.Equal(kits.asked[0], []int32{varFood, varKit}) {
		t.Errorf("KitStructure asked %v, want the added lines' variants once", kits.asked)
	}

	// The kit alone, at 3: the children are rescaled under their ids, and
	// the kept kit line is not asked about again.
	res := save(c, o, kitLines(o, 3), nil)
	mustStatus(t, res, http.StatusOK, "rescale")
	o = orderOf(t, res)
	if got, want := kitShape(t, o), []string{"KIT-1 3 500", "BOLT-M8 12@4 0 0", "MILK-1 4.5@1.5 0 0"}; !slices.Equal(got, want) {
		t.Fatalf("rescaled %v, want %v", got, want)
	}
	if again := childrenOf(o, kit.Id); again[0].Id != children[0].Id || again[1].Id != children[1].Id {
		t.Errorf("the children's ids moved: %d %d, want %d %d", again[0].Id, again[1].Id, children[0].Id, children[1].Id)
	}
	if len(kits.asked) != 1 {
		t.Errorf("a kept kit line read KitStructure again: %v", kits.asked)
	}

	// A later change of the kit's composition does not reach the order; the
	// children sent back as read are left out, not doubled.
	kits.set(varKit, contracts.KitComponent{VariantID: varFood, PerKit: "1"})
	res = save(c, o, keep(o), nil)
	mustStatus(t, res, http.StatusOK, "save as read")
	o = orderOf(t, res)
	if got := kitShape(t, o); len(o.Lines) != 3 || got[1] != "BOLT-M8 12@4 0 0" {
		t.Fatalf("saved as read %v (%d lines)", got, len(o.Lines))
	}

	// A new kit line takes the composition as it is now.
	res = save(c, o, append(kitLines(o, 3), productLine(varKit, 1)), nil)
	mustStatus(t, res, http.StatusOK, "a second kit")
	o = orderOf(t, res)
	if len(o.Lines) != 5 || o.Lines[3].VariantId == nil || *o.Lines[3].VariantId != varKit || *o.Lines[4].VariantId != varFood ||
		*o.Lines[4].ParentLineId != o.Lines[3].Id {
		t.Fatalf("second kit %+v", o.Lines)
	}

	// The kits removed take their children with them.
	res = save(c, o, []gen.OrdersLineInput{productLine(varBolt, 1)}, nil)
	mustStatus(t, res, http.StatusOK, "remove the kits")
	if o = orderOf(t, res); len(o.Lines) != 1 {
		t.Fatalf("lines after removing the kits: %+v", o.Lines)
	}
}

// Without Manufacturing (a nil KitStructure) a kit variant is an ordinary
// goods line: it has no children and reserves itself (M145).
func TestKits_WithoutManufacturingAnOrdinaryLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	h.catalog.seed(contracts.VariantEntry{ID: varKit, ProductID: varKit, ProductName: "Toolbox kit", SKU: "KIT-1", Unit: "pcs",
		ProductType: "Goods", ProductStatus: "Active", TaxCategoryID: catStandard})
	h.catalog.setPrice(varKit, "NOK", "500")
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varKit, locA1, "5")

	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varKit, 2)))
	if len(o.Lines) != 1 || o.Lines[0].ParentLineId != nil {
		t.Fatalf("lines %+v, want the kit alone", o.Lines)
	}
	if h.stock.reservedOf(orderSource(o.Id), o.Lines[0].Id) != "2" {
		t.Errorf("the kit variant reserved %s, want 2", h.stock.reservedOf(orderSource(o.Id), o.Lines[0].Id))
	}
}

// A confirmed kit order: each child reserves, the kit does not; the kit's
// available is the kits its children make; a quantity change moves the
// children's reservations; a delivery takes the children only, with lots,
// and posting it counts the kit delivered; the packing slip prints the kit
// with its children under it, the pick list the children only (M145, M146).
func TestKits_ReserveDeliverAndPrint(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true, kits: toolbox()})
	seedTracked(h)
	h.catalog.seed(contracts.VariantEntry{ID: varKit, ProductID: varKit, ProductName: "Toolbox kit", SKU: "KIT-1", Unit: "pcs",
		ProductType: "Goods", ProductStatus: "Active", TaxCategoryID: catStandard})
	h.catalog.setPrice(varKit, "NOK", "500")
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "20")
	h.stock.putLot(varLot, 1, "A1", locA1, "10", day(h, 40), h.Now().AddDate(0, -1, 0))

	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varKit, 3)))
	src := orderSource(o.Id)
	kit := lineOf(t, o, varKit)
	bolt, milk := childrenOf(o, kit.Id)[0], childrenOf(o, kit.Id)[1]
	if h.stock.reservedOf(src, kit.Id) != "0" || h.stock.reservedOf(src, bolt.Id) != "12" || h.stock.reservedOf(src, milk.Id) != "4.5" {
		t.Fatalf("reserved kit %s bolt %s milk %s, want 0, 12 and 4.5",
			h.stock.reservedOf(src, kit.Id), h.stock.reservedOf(src, bolt.Id), h.stock.reservedOf(src, milk.Id))
	}
	// Bolts: 20 − 12 = 8, two kits; milk: 10 − 4.5 = 5.5, 3.666 kits.
	o = getOrder(t, c, o.Id)
	if k := lineOf(t, o, varKit); k.Available == nil || *k.Available != 2 {
		t.Errorf("kit available %v, want 2", k.Available)
	}

	res := save(c, o, kitLines(o, 2), nil)
	mustStatus(t, res, http.StatusOK, "kit down to 2")
	o = orderOf(t, res)
	if h.stock.reservedOf(src, bolt.Id) != "8" || h.stock.reservedOf(src, milk.Id) != "3" {
		t.Errorf("after the edit bolt %s milk %s, want 8 and 3", h.stock.reservedOf(src, bolt.Id), h.stock.reservedOf(src, milk.Id))
	}

	res = c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "create delivery")
	d := deliveryOf(t, res)
	if len(d.Lines) != 2 || d.Lines[0].OrderLineId != bolt.Id || d.Lines[1].OrderLineId != milk.Id || !d.Lines[1].Tracked ||
		!slices.Equal(lotsOf(d.Lines[1]), []string{fmt.Sprintf("1@%d:3", locA1)}) {
		t.Fatalf("delivery lines %+v, want the children only, the milk picking its lot", d.Lines)
	}

	mustStatus(t, c.Do(http.MethodGet, deliveryPath(d.Id, "pick-list"), nil), http.StatusOK, "pick list")
	subtitle := fmt.Sprintf("Leveranse %d – Ordre %d", d.Number, o.Number)
	pick, ok := pdfModels.find("Plukkliste", subtitle)
	if !ok {
		t.Fatal("no pick list printed")
	}
	for _, r := range pick.Rows {
		if strings.Contains(r[1], "KIT-1") {
			t.Errorf("the pick list prints the kit: %v", pick.Rows)
		}
	}
	if len(pick.Rows) != 2 {
		t.Errorf("pick list rows %v, want the two children", pick.Rows)
	}

	mustStatus(t, postDelivery(c, d), http.StatusOK, "post")
	o = getOrder(t, c, o.Id)
	if k := lineOf(t, o, varKit); k.QtyDelivered != 2 || o.DeliveryStatus != gen.OrdersOrderDeliveryStatusDelivered {
		t.Errorf("kit delivered %v, order %s; want 2 and delivered", k.QtyDelivered, o.DeliveryStatus)
	}
	mustStatus(t, c.Do(http.MethodGet, deliveryPath(d.Id, "packing-slip"), nil), http.StatusOK, "packing slip")
	slip, ok := pdfModels.find("Pakkseddel", subtitle)
	if !ok {
		t.Fatal("no packing slip printed")
	}
	if len(slip.Rows) < 3 || !strings.HasPrefix(slip.Rows[0][0], "KIT-1") || slip.Rows[0][1] != "2" ||
		!strings.HasPrefix(slip.Rows[1][0], "    – BOLT-M8") || !strings.HasPrefix(slip.Rows[2][0], "    – MILK-1") {
		t.Errorf("packing slip rows %v, want the kit and its children under it", slip.Rows)
	}
}
