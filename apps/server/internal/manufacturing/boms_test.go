package manufacturing_test

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// lineCodes is a refusal's line codes, by index.
func lineCodes(p gen.ManufacturingProblem) map[int32][]string {
	out := map[int32][]string{}
	if p.Lines != nil {
		for _, l := range *p.Lines {
			out[l.Index] = append(out[l.Index], l.Code)
		}
	}
	return out
}

// TestBom_CreateGetAndList is M10–M12's happy path: a BoM with operations
// and lines read back, operations by sequence and lines by sort order, the
// site its operations' work centers stand at, the first BoM the default, and
// the list's filters.
func TestBom_CreateGetAndList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	weld := createWorkCenter(t, c, workCenterInput("WELD", siteMain))

	in := bomInput(varFrame, "2", line(varTube, "4.5"), line(varPaint, "0.25"))
	in.Code, in.Note = str(" Rev A "), str("Steel frame")
	in.LeadTimeDays = i32(3)
	in.Lines[0].OperationKey = str("cut")
	in.Lines[0].SortOrder = i32(2)
	in.Lines[1].SortOrder = i32(1)
	in.Lines[1].ScrapPercent = str("5")
	in.Operations = []gen.ManufacturingBomOperationInput{operation("weld", 20, weld.Id, "12.5"), operation("cut", 10, saw.Id, "3")}
	in.Operations[1].SetupMinutes = str("15")
	b := createBom(t, c, in)
	if b.Code == nil || *b.Code != "Rev A" || !b.IsDefault || b.OutputQuantity != "2" || b.LeadTimeDays != 3 || b.Note != "Steel frame" ||
		b.Variant.Sku != "FRAME-1" || b.SiteId == nil || *b.SiteId != siteMain || b.Status != gen.ManufacturingStatusActive || b.Revision != 1 {
		t.Fatalf("created = %+v", b)
	}
	if len(b.Operations) != 2 || b.Operations[0].Name != "Op cut" || *b.Operations[0].SetupMinutes != "15" ||
		b.Operations[0].Key != fmt.Sprint(b.Operations[0].Id) || b.Operations[1].SetupMinutes != nil || b.Operations[1].WorkCenterCode != "WELD" {
		t.Errorf("operations = %+v", b.Operations)
	}
	if len(b.Lines) != 2 || b.Lines[0].Variant.Sku != "PAINT-1" || b.Lines[0].ScrapPercent != "5" || b.Lines[0].OperationId != nil ||
		b.Lines[1].Quantity != "4.5" || b.Lines[1].OperationId == nil || *b.Lines[1].OperationId != b.Operations[0].Id ||
		*b.Lines[1].OperationKey != b.Operations[0].Key {
		t.Errorf("lines = %+v", b.Lines)
	}
	if b.Variant.StandardCost != nil || b.Lines[1].Variant.StandardCost != nil {
		t.Error("a manager without costs-view sees standard costs")
	}
	got := getBom(t, h.SignIn(t, costReader...), b.Id)
	if string(got.Lines[1].Variant.StandardCost) != `"20.00"` || string(got.Variant.StandardCost) != "null" {
		t.Errorf("with costs-view the standard costs are %s and %s", got.Lines[1].Variant.StandardCost, got.Variant.StandardCost)
	}
	mustStatus(t, c.Do(http.MethodGet, bomPath(999999), nil), http.StatusNotFound, "get unknown")

	second := createBom(t, c, bomInput(varFrame, "1", line(varTube, "5")))
	if second.IsDefault || second.SiteId != nil {
		t.Errorf("the second BoM = default %v site %v, want neither", second.IsDefault, second.SiteId)
	}
	bike := createBom(t, c, bomInput(varBike, "1", line(varFrame, "1"), line(varWheel, "2")))
	res := c.Do(http.MethodPost, bomPath(second.Id, "archive"), revisionInput(second.Revision))
	mustStatus(t, res, http.StatusOK, "archive the second")

	list := func(query string) gen.ManufacturingBomList {
		t.Helper()
		res := c.Do(http.MethodGet, bomsPath+query, nil)
		mustStatus(t, res, http.StatusOK, "list "+query)
		var l gen.ManufacturingBomList
		res.JSON(&l)
		return l
	}
	if l := list(""); l.Total != 2 || len(l.Items) != 2 || l.Items[0].Id != bike.Id || l.Items[1].LineCount != 2 || l.Items[1].OperationCount != 2 {
		t.Errorf("active list = %+v", l)
	}
	if l := list("?status=all&limit=1&offset=1"); l.Total != 3 || len(l.Items) != 1 {
		t.Errorf("a page of all = %+v", l)
	}
	if l := list(fmt.Sprintf("?status=archived&variantId=%d", varFrame)); l.Total != 1 || l.Items[0].Id != second.Id {
		t.Errorf("archived frames = %+v", l)
	}
	for q, want := range map[string]int64{"rev": b.Id, "steel": b.Id, "bicyc": bike.Id, "BIKE-1": bike.Id} {
		if l := list("?q=" + q); l.Total != 1 || l.Items[0].Id != want {
			t.Errorf("q=%s = %+v, want BoM %d", q, l.Items, want)
		}
	}
}

// TestBom_ShapeRules is M10–M12's field and line rules, every failing line at
// once: quantity_invalid, operation_key_unknown, the scrap's range, the
// output, the operation's key and minutes.
func TestBom_ShapeRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))

	in := bomInput(varFrame, "1", line(varTube, "0"), line(varWheel, "1.2345"), line(varPaint, "1"), line(varBolt, "1"))
	in.Lines[2].OperationKey = str("nope")
	p := expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "quantity_invalid", "bad lines")
	codes := lineCodes(p)
	if !slices.Equal(codes[0], []string{"quantity_invalid"}) || !slices.Equal(codes[1], []string{"quantity_invalid"}) ||
		!slices.Equal(codes[2], []string{"operation_key_unknown"}) || len(codes[3]) != 0 {
		t.Errorf("line codes = %v", codes)
	}

	in = bomInput(varFrame, "0", line(varTube, "1"))
	in.Lines[0].ScrapPercent = str("100")
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 1, saw.Id, "0"), operation("a", 2, saw.Id, "1")}
	p = expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "quantity_invalid", "bad fields")
	var fields []string
	for f := range *p.Errors {
		fields = append(fields, f)
	}
	slices.Sort(fields)
	if want := []string{"lines[0].scrapPercent", "operations[0].minutesPerCycle", "operations[1].key", "outputQuantity"}; !slices.Equal(fields, want) {
		t.Errorf("fields = %v, want %v", fields, want)
	}

	// Two operations with one id, or one sequence, are refused on the second.
	in = bomInput(varFrame, "1", line(varTube, "1"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 1, saw.Id, "1"), operation("b", 1, saw.Id, "1")}
	in.Operations[0].Id, in.Operations[1].Id = new(int64(7)), new(int64(7))
	p = expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "", "a repeated id and sequence")
	if want := []string{"Two operations have this id"}; !slices.Equal((*p.Errors)["operations[1].id"], want) {
		t.Errorf("errors = %v, want operations[1].id %v", p.Errors, want)
	}
	if want := []string{"Two operations have this sequence"}; !slices.Equal((*p.Errors)["operations[1].sequence"], want) {
		t.Errorf("errors = %v, want operations[1].sequence %v", p.Errors, want)
	}
	if (*p.Errors)["operations[0].id"] != nil || (*p.Errors)["operations[0].sequence"] != nil {
		t.Errorf("errors = %v, want none on operations[0]", p.Errors)
	}

	// An unknown property inside a line is refused on its path.
	body := map[string]any{"variantId": varFrame, "outputQuantity": "1", "operations": []any{},
		"lines": []any{map[string]any{"variantId": varTube, "quantity": "1", "scrap": "2"}}}
	p = expectRefusal(t, c.Do(http.MethodPost, bomsPath, body, modtest.SkipContract("an unknown property on purpose")), http.StatusBadRequest, "", "unknown line property")
	if (*p.Errors)["lines[0].scrap"] == nil {
		t.Errorf("errors = %v", p.Errors)
	}
}

// TestBom_Limits is M11 and M12's limits: 200 lines, 50 operations.
func TestBom_Limits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	offContract := modtest.SkipContract("past the contract's maxItems on purpose")

	in := bomInput(varFrame, "1")
	for range 201 {
		in.Lines = append(in.Lines, line(varTube, "1"))
	}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in, offContract), http.StatusBadRequest, "too_many_lines", "201 lines")
	in.Lines = in.Lines[:200]
	b := createBom(t, c, in)
	if len(b.Lines) != 200 {
		t.Errorf("200 lines saved as %d", len(b.Lines))
	}

	in = bomInput(varBike, "1", line(varTube, "1"))
	for i := range 51 {
		in.Operations = append(in.Operations, operation(fmt.Sprint(i), int32(i), saw.Id, "1"))
	}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in, offContract), http.StatusBadRequest, "too_many_operations", "51 operations")
}

// TestBom_VariantsJudgedFromTheLock is M10a: whether the product and each
// component exist and are goods is judged from what LockVariants answers,
// not from any earlier read — the fake's locked answer differs from its
// plain read to prove it.
func TestBom_VariantsJudgedFromTheLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)

	// The plain reads still say goods; the lock says service.
	tube := goods(varTube, "TUBE-1", "Tube", "20.00")
	tube.ProductType = "Service"
	h.catalog.lockAnswer(varTube, &tube)
	p := expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(varFrame, "1", line(varWheel, "1"), line(varTube, "1"), line(varService, "1"))),
		http.StatusBadRequest, "variant_not_goods", "components that are services")
	if codes := lineCodes(p); len(codes[0]) != 0 || !slices.Equal(codes[1], []string{"variant_not_goods"}) || !slices.Equal(codes[2], []string{"variant_not_goods"}) {
		t.Errorf("line codes = %v", codes)
	}
	frame := goods(varFrame, "FRAME-1", "Frame", "")
	frame.ProductType = "Service"
	h.catalog.lockAnswer(varFrame, &frame)
	p = expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(varFrame, "1", line(varWheel, "1"))), http.StatusBadRequest, "variant_not_goods", "a service product")
	if (*p.Errors)["variantId"] == nil {
		t.Errorf("the product's refusal names %v, want variantId", p.Errors)
	}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(varService, "1", line(varWheel, "1"))), http.StatusBadRequest, "variant_not_goods", "the seeded service")

	// Gone once locked: variant_not_found, for the product and on a line.
	h.catalog.lockAnswer(varWheel, nil)
	p = expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(varBike, "1", line(varBolt, "1"), line(varWheel, "1"))), http.StatusBadRequest, "variant_not_found", "a component gone")
	if codes := lineCodes(p); !slices.Equal(codes[1], []string{"variant_not_found"}) {
		t.Errorf("line codes = %v", codes)
	}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(4242, "1", line(varBolt, "1"))), http.StatusBadRequest, "variant_not_found", "an unknown product")

	// Each write locked the product and every component, in id order.
	locks := h.catalog.lockCalls()
	if len(locks) == 0 || !slices.Equal(locks[len(locks)-1], []int32{varBolt, 4242}) {
		t.Errorf("the last lock = %v, want the product and its component in id order", locks)
	}
}

// TestBom_OperationRules is M12 and M12a: an operation's work center must be
// active (work_center_unknown) and all of them at one site
// (work_center_other_site).
func TestBom_OperationRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	branch := createWorkCenter(t, c, workCenterInput("BR", siteBranch))
	gone := createWorkCenter(t, c, workCenterInput("GONE", siteMain))
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(gone.Id, "archive"), revisionInput(gone.Revision)), http.StatusOK, "archive")

	in := bomInput(varFrame, "1", line(varTube, "1"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 1, saw.Id, "1"), operation("b", 2, gone.Id, "1"), operation("c", 3, 999999, "1")}
	p := expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "work_center_unknown", "archived and unknown centers")
	if (*p.Errors)["operations[1].workCenterId"] == nil || (*p.Errors)["operations[2].workCenterId"] == nil || (*p.Errors)["operations[0].workCenterId"] != nil {
		t.Errorf("errors = %v", p.Errors)
	}
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 1, saw.Id, "1"), operation("b", 2, branch.Id, "1")}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "work_center_other_site", "two sites")
}

// TestBom_CodeTaken is M10's unique code.
func TestBom_CodeTaken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	in := bomInput(varFrame, "1", line(varTube, "1"))
	in.Code = str("REV-A")
	createBom(t, c, in)
	in.VariantId = varBike
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusConflict, "bom_code_taken", "create")
	other := createBom(t, c, bomInput(varBike, "1", line(varWheel, "1")))
	up := updateOf(other)
	up.Code = str("REV-A")
	expectRefusal(t, c.Do(http.MethodPut, bomPath(other.Id), up), http.StatusConflict, "bom_code_taken", "update")
}

// TestBom_CycleAcrossThreeLevels is M14: a BoM whose component, through each
// component's default BoM, three levels down, is the BoM's own variant is
// bom_cycle, naming the path — on create, on update, on make-default and on
// reactivate.
func TestBom_CycleAcrossThreeLevels(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	// BIKE ← FRAME ← TUBE: the bike needs a frame, the frame needs a tube.
	createBom(t, c, bomInput(varBike, "1", line(varFrame, "1"), line(varBolt, "4")))
	frame := createBom(t, c, bomInput(varFrame, "1", line(varTube, "2")))

	// A tube made from a bike closes the cycle TUBE → BIKE → FRAME → TUBE.
	p := expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(varTube, "1", line(varBolt, "1"), line(varBike, "1"))), http.StatusBadRequest, "bom_cycle", "the cycle")
	var path []string
	for _, step := range *p.Path {
		path = append(path, step.Sku)
	}
	if want := []string{"TUBE-1", "BIKE-1", "FRAME-1", "TUBE-1"}; !slices.Equal(path, want) {
		t.Errorf("path = %v, want %v", path, want)
	}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(varTube, "1", line(varTube, "1"))), http.StatusBadRequest, "bom_cycle", "itself")

	// On update: the frame's tube replaced by a bike.
	up := updateOf(frame)
	up.Lines = []gen.ManufacturingBomLineInput{line(varBike, "1")}
	expectRefusal(t, c.Do(http.MethodPut, bomPath(frame.Id), up), http.StatusBadRequest, "bom_cycle", "update into a cycle")

	expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(varFrame, "1", line(varBike, "1"))), http.StatusBadRequest, "bom_cycle", "an alternative into a cycle")

	// Make-default and reactivate walk again: two alternatives of the tube
	// needing a wheel are fine while a wheel is bought; once a wheel is made
	// from a bike, making either the tube's default would close
	// TUBE → WHEEL → BIKE → FRAME → TUBE.
	createBom(t, c, bomInput(varTube, "1", line(varBolt, "1")))
	alt := createBom(t, c, bomInput(varTube, "1", line(varWheel, "1")))
	parked := createBom(t, c, bomInput(varTube, "1", line(varWheel, "2")))
	res := c.Do(http.MethodPost, bomPath(parked.Id, "archive"), revisionInput(parked.Revision))
	mustStatus(t, res, http.StatusOK, "archive the parked alternative")
	parked = bomOf(t, res)
	createBom(t, c, bomInput(varWheel, "1", line(varBike, "1")))
	p = expectRefusal(t, c.Do(http.MethodPost, bomPath(alt.Id, "make-default"), revisionInput(alt.Revision)), http.StatusBadRequest, "bom_cycle", "make-default into a cycle")
	path = path[:0]
	for _, step := range *p.Path {
		path = append(path, step.Sku)
	}
	if want := []string{"TUBE-1", "WHEEL-1", "BIKE-1", "FRAME-1", "TUBE-1"}; !slices.Equal(path, want) {
		t.Errorf("make-default path = %v, want %v", path, want)
	}
	expectRefusal(t, c.Do(http.MethodPost, bomPath(parked.Id, "reactivate"), revisionInput(parked.Revision)), http.StatusBadRequest, "bom_cycle", "reactivate into a cycle")
}

// TestBom_TooDeep is M14's limit: ten BoMs deep is allowed, eleven is
// bom_too_deep.
func TestBom_TooDeep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	// A chain of goods 201 ← 202 ← … ← 211 (211 bought).
	for i := range int32(12) {
		h.catalog.seed(goods(201+i, fmt.Sprintf("CHAIN-%d", i), "Chain", ""))
	}
	for i := int32(9); i >= 1; i-- {
		createBom(t, c, bomInput(201+i, "1", line(202+i, "1")))
	}
	// 201's BoM is level 1, 210's level 10: allowed.
	createBom(t, c, bomInput(201, "1", line(202, "1")))
	// 200's would be eleven deep.
	h.catalog.seed(goods(200, "CHAIN-TOP", "Chain top", ""))
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(200, "1", line(201, "1"))), http.StatusBadRequest, "bom_too_deep", "eleven deep")
}

// TestBom_TooDeepAbove is M14's limit counted from above: a BoM that becomes
// its variant's default sits under every default BoM that consumes the
// variant, so under a chain of ten a BoM for the bought bottom variant is
// eleven deep — refused, though it is one level on its own.
func TestBom_TooDeepAbove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	// A chain of goods 301 ← 302 ← … ← 311 (311 bought), built top down.
	for i := range int32(11) {
		h.catalog.seed(goods(301+i, fmt.Sprintf("ABOVE-%d", i), "Chain", ""))
	}
	for i := range int32(10) {
		createBom(t, c, bomInput(301+i, "1", line(302+i, "1")))
	}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, bomInput(311, "1", line(varBolt, "1"))), http.StatusBadRequest, "bom_too_deep", "a BoM under ten levels")
}

// TestBom_DefaultSwap is M10 and M14a's default rules: the first active BoM
// is the default; isDefault on create unmarks the old one; make-default
// unmarks the old one; archiving the default promotes the oldest other active
// BoM, or leaves none; reactivating becomes the default when there is none.
func TestBom_DefaultSwap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	defaults := func() []int64 {
		t.Helper()
		var ids []int64
		res := c.Do(http.MethodGet, fmt.Sprintf("%s?variantId=%d&status=all", bomsPath, varFrame), nil)
		var l gen.ManufacturingBomList
		res.JSON(&l)
		for _, b := range l.Items {
			if b.IsDefault {
				ids = append(ids, b.Id)
			}
		}
		return ids
	}
	first := createBom(t, c, bomInput(varFrame, "1", line(varTube, "1")))
	second := createBom(t, c, bomInput(varFrame, "1", line(varTube, "2")))
	if !first.IsDefault || second.IsDefault {
		t.Fatalf("first default %v, second %v", first.IsDefault, second.IsDefault)
	}
	in := bomInput(varFrame, "1", line(varTube, "3"))
	in.IsDefault = new(bool)
	*in.IsDefault = true
	third := createBom(t, c, in)
	if got := defaults(); !slices.Equal(got, []int64{third.Id}) {
		t.Fatalf("after create with isDefault the defaults are %v, want %d", got, third.Id)
	}

	second = getBom(t, c, second.Id)
	res := c.Do(http.MethodPost, bomPath(second.Id, "make-default"), revisionInput(second.Revision))
	mustStatus(t, res, http.StatusOK, "make-default")
	if b := bomOf(t, res); !b.IsDefault || b.Revision != second.Revision+1 {
		t.Errorf("made default = %+v", b)
	}
	if got := defaults(); !slices.Equal(got, []int64{second.Id}) {
		t.Fatalf("after make-default the defaults are %v", got)
	}
	expectRefusal(t, c.Do(http.MethodPost, bomPath(second.Id, "make-default"), revisionInput(second.Revision)), http.StatusConflict, "revision_conflict", "stale make-default")

	// Archiving the default promotes the oldest other active one: the first.
	second = getBom(t, c, second.Id)
	res = c.Do(http.MethodPost, bomPath(second.Id, "archive"), revisionInput(second.Revision))
	mustStatus(t, res, http.StatusOK, "archive the default")
	if b := bomOf(t, res); b.IsDefault || b.Status != gen.ManufacturingStatusArchived {
		t.Errorf("archived = %+v", b)
	}
	if got := defaults(); !slices.Equal(got, []int64{first.Id}) {
		t.Fatalf("after archiving the default the defaults are %v, want the oldest %d", got, first.Id)
	}

	// An archived BoM is neither edited nor made the default.
	archived := getBom(t, c, second.Id)
	expectRefusal(t, c.Do(http.MethodPost, bomPath(second.Id, "make-default"), revisionInput(archived.Revision)), http.StatusConflict, "bom_not_active", "make-default of an archived BoM")
	expectRefusal(t, c.Do(http.MethodPut, bomPath(second.Id), updateOf(archived)), http.StatusConflict, "bom_not_active", "edit of an archived BoM")

	// Archive the other two: none is left the default.
	for _, id := range []int64{first.Id, third.Id} {
		b := getBom(t, c, id)
		mustStatus(t, c.Do(http.MethodPost, bomPath(id, "archive"), revisionInput(b.Revision)), http.StatusOK, "archive")
	}
	if got := defaults(); len(got) != 0 {
		t.Fatalf("with every BoM archived the defaults are %v", got)
	}

	// Reactivating with no default becomes the default; the next does not.
	res = c.Do(http.MethodPost, bomPath(second.Id, "reactivate"), revisionInput(archived.Revision))
	mustStatus(t, res, http.StatusOK, "reactivate")
	if b := bomOf(t, res); !b.IsDefault || b.Status != gen.ManufacturingStatusActive {
		t.Errorf("reactivated = %+v, want the default", b)
	}
	b := getBom(t, c, third.Id)
	res = c.Do(http.MethodPost, bomPath(third.Id, "reactivate"), revisionInput(b.Revision))
	mustStatus(t, res, http.StatusOK, "reactivate another")
	if b := bomOf(t, res); b.IsDefault {
		t.Error("a second reactivated BoM became the default")
	}
	mustStatus(t, c.Do(http.MethodPost, bomPath(999999, "archive"), revisionInput(1)), http.StatusNotFound, "archive unknown")
	mustStatus(t, c.Do(http.MethodPost, bomPath(999999, "reactivate"), revisionInput(1)), http.StatusNotFound, "reactivate unknown")
	mustStatus(t, c.Do(http.MethodPost, bomPath(999999, "make-default"), revisionInput(1)), http.StatusNotFound, "make-default unknown")
}

// TestBom_ReactivateJudgesGoodsAgain is M10a on reactivate: a component that
// became a service while the BoM was archived refuses it.
func TestBom_ReactivateJudgesGoodsAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	b := createBom(t, c, bomInput(varFrame, "1", line(varTube, "1")))
	res := c.Do(http.MethodPost, bomPath(b.Id, "archive"), revisionInput(b.Revision))
	mustStatus(t, res, http.StatusOK, "archive")
	archived := bomOf(t, res)
	tube := goods(varTube, "TUBE-1", "Tube", "")
	tube.ProductType = "Service"
	h.catalog.lockAnswer(varTube, &tube)
	expectRefusal(t, c.Do(http.MethodPost, bomPath(b.Id, "reactivate"), revisionInput(archived.Revision)), http.StatusBadRequest, "variant_not_goods", "reactivate with a service")
}

// TestBom_ReactivateJudgesWorkCentersAgain is M12 and M12a on reactivate: a
// work center moved to another site while the BoM was archived is
// work_center_other_site, one archived meanwhile work_center_unknown.
func TestBom_ReactivateJudgesWorkCentersAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	drill := createWorkCenter(t, c, workCenterInput("DRILL", siteMain))
	in := bomInput(varFrame, "1", line(varTube, "1"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("cut", 10, saw.Id, "5"), operation("drill", 20, drill.Id, "2")}
	b := createBom(t, c, in)
	res := c.Do(http.MethodPost, bomPath(b.Id, "archive"), revisionInput(b.Revision))
	mustStatus(t, res, http.StatusOK, "archive the BoM")
	archived := bomOf(t, res)

	up := gen.ManufacturingWorkCenterUpdate{Code: "DRILL", Name: "Drill", SiteId: siteBranch, Revision: drill.Revision}
	var moved gen.ManufacturingWorkCenter
	res = c.Do(http.MethodPut, workCenterPath(drill.Id), up)
	mustStatus(t, res, http.StatusOK, "move the drill to the branch")
	res.JSON(&moved)
	expectRefusal(t, c.Do(http.MethodPost, bomPath(b.Id, "reactivate"), revisionInput(archived.Revision)), http.StatusBadRequest, "work_center_other_site", "reactivate across two sites")

	up.SiteId, up.Revision = siteMain, moved.Revision
	res = c.Do(http.MethodPut, workCenterPath(drill.Id), up)
	mustStatus(t, res, http.StatusOK, "move the drill back")
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(saw.Id, "archive"), revisionInput(saw.Revision)), http.StatusOK, "archive the saw")
	p := expectRefusal(t, c.Do(http.MethodPost, bomPath(b.Id, "reactivate"), revisionInput(archived.Revision)), http.StatusBadRequest, "work_center_unknown", "reactivate at an archived center")
	if p.Errors == nil || (*p.Errors)["operations[0].workCenterId"] == nil {
		t.Errorf("errors = %v, want one on operations[0].workCenterId", p.Errors)
	}
}

// TestBom_UpdateReplacesKeepingOperationIDs is M13: an update replaces the
// lines and operations; an operation sent with its id keeps it, one left out
// is deleted and a line that named it is cleared; the revision moves on.
func TestBom_UpdateReplacesKeepingOperationIDs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varFrame, "1", line(varTube, "1"), line(varPaint, "1"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("cut", 1, saw.Id, "2"), operation("paint", 2, saw.Id, "4")}
	in.Lines[0].OperationKey, in.Lines[1].OperationKey = str("cut"), str("paint")
	b := createBom(t, c, in)
	cut, paint := b.Operations[0], b.Operations[1]

	// Keep cut by id (renamed), drop paint, add polish; the paint line now
	// names no operation, a new line names the new one by its key.
	up := gen.ManufacturingBomUpdate{OutputQuantity: "2", Revision: b.Revision, MakeToOrder: new(bool),
		Operations: []gen.ManufacturingBomOperationInput{operation("polish", 3, saw.Id, "1"), operation(cut.Key, 1, saw.Id, "2.5")},
		Lines:      []gen.ManufacturingBomLineInput{line(varTube, "2"), line(varPaint, "1"), line(varBolt, "8")}}
	up.Operations[1].Id = &cut.Id
	up.Lines[0].OperationKey = str(cut.Key)
	up.Lines[2].OperationKey = str("polish")
	res := c.Do(http.MethodPut, bomPath(b.Id), up)
	mustStatus(t, res, http.StatusOK, "update")
	got := bomOf(t, res)
	if got.Revision != 2 || got.OutputQuantity != "2" || len(got.Operations) != 2 || len(got.Lines) != 3 {
		t.Fatalf("updated = %+v", got)
	}
	if got.Operations[0].Id != cut.Id || got.Operations[0].MinutesPerCycle != "2.5" || got.Operations[1].Id == paint.Id {
		t.Errorf("operations = %+v, want cut kept by id and paint gone", got.Operations)
	}
	if *got.Lines[0].OperationId != cut.Id || got.Lines[1].OperationId != nil || *got.Lines[2].OperationId != got.Operations[1].Id {
		t.Errorf("lines = %+v", got.Lines)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.bom_operations WHERE id = $1`, paint.Id); n != 0 {
		t.Errorf("the dropped operation is still there")
	}

	expectRefusal(t, c.Do(http.MethodPut, bomPath(b.Id), up), http.StatusConflict, "revision_conflict", "stale update")
	up.Revision = got.Revision
	stranger := int64(999999)
	up.Operations[1].Id = &stranger
	p := expectRefusal(t, c.Do(http.MethodPut, bomPath(b.Id), up), http.StatusBadRequest, "", "an operation id of another BoM")
	if (*p.Errors)["operations[1].id"] == nil {
		t.Errorf("errors = %v", p.Errors)
	}
	mustStatus(t, c.Do(http.MethodPut, bomPath(999999), up), http.StatusNotFound, "update unknown")

	// Removing an operation by SQL's own key clears the line: the FK's
	// ON DELETE SET NULL, which DeleteBomOperationsExcept relies on.
	h.Exec(t, `DELETE FROM manufacturing.bom_operations WHERE id = $1`, cut.Id)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.bom_lines WHERE bom_id = $1 AND operation_id IS NULL`, b.Id); n != 2 {
		t.Errorf("%d lines without an operation, want 2", n)
	}
}

// TestBom_ConcurrentDefaults is M14a under a race on a pool of four: two
// concurrent default writes for one variant — a create with isDefault and a
// make-default — never leave two defaults.
func TestBom_ConcurrentDefaults(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c := h.SignIn(t, manager...)
	createBom(t, c, bomInput(varFrame, "1", line(varTube, "1")))
	for round := range 4 {
		alt := createBom(t, c, bomInput(varFrame, "1", line(varTube, fmt.Sprint(round+2))))
		in := bomInput(varFrame, "1", line(varPaint, "1"))
		in.IsDefault = new(bool)
		*in.IsDefault = true
		var made, created *modtest.Response
		var wg sync.WaitGroup
		wg.Go(func() { made = c.Do(http.MethodPost, bomPath(alt.Id, "make-default"), revisionInput(alt.Revision)) })
		wg.Go(func() { created = c.Do(http.MethodPost, bomsPath, in) })
		wg.Wait()
		mustStatus(t, made, http.StatusOK, "make-default")
		mustStatus(t, created, http.StatusCreated, "create as the default")
		if n := h.Count(t, `SELECT count(*) FROM manufacturing.boms WHERE variant_id = $1 AND is_default AND status = 'active'`, varFrame); n != 1 {
			t.Fatalf("round %d: %d defaults", round, n)
		}
	}
}
