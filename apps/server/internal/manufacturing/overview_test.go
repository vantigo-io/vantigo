package manufacturing_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
)

// overviewFixture is M15's two-level fixture: a bicycle made of a made frame
// (no standard cost of its own), bought wheels and bought paint (no standard
// cost); the frame made of tubes and bolts. Each BoM has one operation.
func overviewFixture(t *testing.T, h *harness) gen.ManufacturingBom {
	t.Helper()
	c := h.SignIn(t, everything...)
	saw := workCenterInput("SAW", siteMain)
	saw.Capacity, saw.TimeEfficiency = i32(2), i32(80)
	saw.SetupMinutes, saw.CleanupMinutes = str("10"), str("5")
	saw.MachineCostPerHour, saw.LabourCostPerHour = json.RawMessage(`"600"`), json.RawMessage(`"300"`)
	sawCenter := createWorkCenter(t, c, saw)
	painter := workCenterInput("PAINTER", siteMain)
	painter.LabourCostPerHour = json.RawMessage(`"120"`)
	painterCenter := createWorkCenter(t, c, painter)

	frame := bomInput(varFrame, "1", line(varTube, "3"), line(varBolt, "6"))
	frame.Lines[0].ScrapPercent = str("2.5")
	frame.Operations = []gen.ManufacturingBomOperationInput{operation("cut", 10, sawCenter.Id, "7.5")}
	frame.Operations[0].SetupMinutes = str("20")
	createBom(t, c, frame)

	bike := bomInput(varBike, "2", line(varFrame, "1"), line(varWheel, "4"), line(varPaint, "0.333"))
	bike.Lines[0].ScrapPercent = str("10")
	bike.Lines[2].ScrapPercent = str("5")
	bike.Operations = []gen.ManufacturingBomOperationInput{operation("assemble", 10, painterCenter.Id, "30")}
	return createBom(t, c, bike)
}

// TestOverview_TwoLevelNumbers is M15, M18's ceiling and M28a on the
// fixture at quantity 3, with the availability at a site.
func TestOverview_TwoLevelNumbers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	bike := overviewFixture(t, h)
	h.stock.setAvailable(siteMain, varWheel, "10")
	h.stock.setAvailable(siteMain, varTube, "2.5")
	c := h.SignIn(t, costReader...)

	res := c.Do(http.MethodGet, fmt.Sprintf("%s?quantity=3&siteId=%d", bomPath(bike.Id, "overview"), siteMain), nil)
	mustStatus(t, res, http.StatusOK, "overview")
	var o gen.ManufacturingBomOverview
	res.JSON(&o)
	if o.Quantity != "3" || o.SiteId == nil || *o.SiteId != siteMain || o.NodeCount != 6 || o.BomId != bike.Id {
		t.Fatalf("overview = %+v", o)
	}
	root := o.Root
	if !root.Made || root.Depth != 0 || root.QuantityRequired != "3" || *root.BomId != bike.Id ||
		string(root.StandardCost) != `"2000.00"` || string(root.ExtendedValue) != `"6000.00"` || *root.Available != "0" {
		t.Errorf("root = %+v", root)
	}
	// assemble: ⌈(3/2)/1⌉ = 2 cycles × 30 = 60 minutes; 1 h × 120 = 120.
	if len(root.Operations) != 1 || root.Operations[0].ExpectedMinutes != 60 || string(root.Operations[0].Cost) != `"120.00"` {
		t.Errorf("root operations = %+v", root.Operations)
	}
	if len(root.Children) != 3 {
		t.Fatalf("root children = %d", len(root.Children))
	}
	frame, wheel, paint := root.Children[0], root.Children[1], root.Children[2]
	// 1 × 3 / 2 × 1.10 = 1.65; 4 × 1.5 = 6; 0.333 × 1.5 × 1.05 = 0.524475 ↑ 0.525.
	if !frame.Made || frame.QuantityRequired != "1.65" || frame.ScrapPercent != "10" || frame.Depth != 1 ||
		string(frame.StandardCost) != "null" || string(frame.ExtendedValue) != "null" {
		t.Errorf("frame = %+v", frame)
	}
	if wheel.Made || wheel.BomId != nil || wheel.QuantityRequired != "6" || string(wheel.ExtendedValue) != `"903.00"` || *wheel.Available != "10" {
		t.Errorf("wheel = %+v", wheel)
	}
	if paint.QuantityRequired != "0.525" || string(paint.StandardCost) != "null" {
		t.Errorf("paint = %+v", paint)
	}
	// cut: ⌈1.65/2⌉ = 1 cycle; 20 (the operation's setup) + 5 + 7.5 × 100/80
	// = 34.375 → 34 minutes; 34/60 h × 900 = 510.
	if len(frame.Operations) != 1 || frame.Operations[0].ExpectedMinutes != 34 || string(frame.Operations[0].Cost) != `"510.00"` ||
		frame.Operations[0].WorkCenterCode != "SAW" {
		t.Errorf("frame operations = %+v", frame.Operations)
	}
	tube, bolt := frame.Children[0], frame.Children[1]
	// 3 × 1.65 × 1.025 = 5.07375 ↑ 5.074; 6 × 1.65 = 9.9.
	if tube.QuantityRequired != "5.074" || tube.Depth != 2 || string(tube.ExtendedValue) != `"101.48"` || *tube.Available != "2.5" {
		t.Errorf("tube = %+v", tube)
	}
	if bolt.QuantityRequired != "9.9" || string(bolt.ExtendedValue) != `"2.48"` {
		t.Errorf("bolt = %+v", bolt)
	}
	// Components: the frame has no standard cost, so its own components count
	// (101.48 + 2.475) and its operation; the wheels 903; the paint none
	// (incomplete). 1006.955 → 1006.96; operations 120 + 510; total 1636.955.
	if o.RolledUpCost == nil || o.RolledUpCost.Components != "1006.96" || o.RolledUpCost.Operations != "630.00" ||
		o.RolledUpCost.Total != "1636.96" || o.CostIncomplete == nil || !*o.CostIncomplete {
		t.Errorf("rolled up = %+v, incomplete %v", o.RolledUpCost, o.CostIncomplete)
	}

	// A made component with a standard cost of its own stands for its
	// subtree: its operations are not counted.
	h.catalog.seed(goods(varFrame, "FRAME-1", "Frame", "400.00"))
	h.catalog.seed(goods(varPaint, "PAINT-1", "Paint", "10"))
	c.Do(http.MethodGet, bomPath(bike.Id, "overview")+"?quantity=3", nil).JSON(&o)
	// 1.65 × 400 = 660; 903; 0.525 × 10 = 5.25 → 1568.25; operations 120.
	if o.RolledUpCost.Components != "1568.25" || o.RolledUpCost.Operations != "120.00" || *o.CostIncomplete {
		t.Errorf("with the frame's own cost = %+v, incomplete %v", o.RolledUpCost, *o.CostIncomplete)
	}
	if o.SiteId != nil || o.Root.Available != nil {
		t.Error("without a site the availability is not null")
	}

	// The default quantity is the BoM's output quantity.
	c.Do(http.MethodGet, bomPath(bike.Id, "overview"), nil).JSON(&o)
	if o.Quantity != "2" || o.Root.Children[1].QuantityRequired != "4" {
		t.Errorf("default quantity = %s, wheels %s", o.Quantity, o.Root.Children[1].QuantityRequired)
	}
}

// TestOverview_ShapedWithoutCostsView is M38 on the overview: every cost
// field is absent, not null.
func TestOverview_ShapedWithoutCostsView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	bike := overviewFixture(t, h)
	res := h.SignIn(t, reader...).Do(http.MethodGet, bomPath(bike.Id, "overview"), nil)
	mustStatus(t, res, http.StatusOK, "overview")
	var raw map[string]any
	res.JSON(&raw)
	for _, key := range []string{"rolledUpCost", "costIncomplete"} {
		if _, ok := raw[key]; ok {
			t.Errorf("%s is present without costs-view", key)
		}
	}
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		for _, key := range []string{"standardCost", "extendedValue"} {
			if _, ok := node[key]; ok {
				t.Errorf("node %v has %s", node["variant"], key)
			}
		}
		if _, ok := node["variant"].(map[string]any)["standardCost"]; ok {
			t.Error("a variant ref has its standard cost")
		}
		for _, op := range node["operations"].([]any) {
			if _, ok := op.(map[string]any)["cost"]; ok {
				t.Error("an operation has its cost")
			}
		}
		for _, child := range node["children"].([]any) {
			walk(child.(map[string]any))
		}
	}
	walk(raw["root"].(map[string]any))
}

// TestOverview_Refusals is the overview's own codes: quantity_invalid,
// site_unknown, bom_too_large past 2000 nodes (M15a) and bom_too_deep past
// ten levels (M14), and the 404.
func TestOverview_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	frame := bomInput(varFrame, "1")
	for range 10 {
		frame.Lines = append(frame.Lines, line(varTube, "1"))
	}
	createBom(t, c, frame)
	bike := bomInput(varBike, "1")
	for range 200 {
		bike.Lines = append(bike.Lines, line(varFrame, "1"))
	}
	b := createBom(t, c, bike)
	expectRefusal(t, c.Do(http.MethodGet, bomPath(b.Id, "overview"), nil), http.StatusBadRequest, "bom_too_large", "2201 nodes")
	for _, q := range []string{"0", "1.2345", "abc"} {
		expectRefusal(t, c.Do(http.MethodGet, bomPath(b.Id, "overview")+"?quantity="+q, nil), http.StatusBadRequest, "quantity_invalid", "quantity "+q)
	}
	expectRefusal(t, c.Do(http.MethodGet, fmt.Sprintf("%s?siteId=%d", bomPath(b.Id, "overview"), siteGone), nil), http.StatusBadRequest, "site_unknown", "an archived site")
	mustStatus(t, c.Do(http.MethodGet, bomPath(999999, "overview"), nil), http.StatusNotFound, "unknown BoM")

	// A chain ten deep is walked; a BoM added at its bottom later walks only
	// downward, so the top's overview finds eleven levels: bom_too_deep.
	for i := range int32(12) {
		h.catalog.seed(goods(301+i, fmt.Sprintf("LINK-%d", i), "Link", ""))
	}
	var top gen.ManufacturingBom
	for i := int32(9); i >= 0; i-- {
		top = createBom(t, c, bomInput(301+i, "1", line(302+i, "1")))
	}
	mustStatus(t, c.Do(http.MethodGet, bomPath(top.Id, "overview"), nil), http.StatusOK, "ten deep")
	createBom(t, c, bomInput(311, "1", line(312, "1")))
	expectRefusal(t, c.Do(http.MethodGet, bomPath(top.Id, "overview"), nil), http.StatusBadRequest, "bom_too_deep", "eleven deep")

	// The stock is read in batches, each variant once.
	var o gen.ManufacturingBomOverview
	frameOnly := createBom(t, c, bomInput(varWheel, "1", line(varTube, "1"), line(varTube, "2"), line(varBolt, "1")))
	before := h.stock.batches
	res := c.Do(http.MethodGet, fmt.Sprintf("%s?siteId=%d", bomPath(frameOnly.Id, "overview"), siteMain), nil)
	mustStatus(t, res, http.StatusOK, "overview at a site")
	res.JSON(&o)
	if h.stock.batches-before != 1 || o.NodeCount != 4 {
		t.Errorf("%d stock reads for %d nodes, want one", h.stock.batches-before, o.NodeCount)
	}
	var skus []string
	for _, n := range o.Root.Children {
		skus = append(skus, n.Variant.Sku)
	}
	if !slices.Equal(skus, []string{"TUBE-1", "TUBE-1", "BOLT-1"}) {
		t.Errorf("children = %v", skus)
	}
}
