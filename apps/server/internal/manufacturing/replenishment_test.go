package manufacturing_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

const (
	suggestionsPath = "/api/v1/manufacturing/replenishment/suggestions"
	runsPath        = "/api/v1/manufacturing/replenishment/runs"
)

// runReplenishment runs the worker's cycle once, as the harness's Deps.
func runReplenishment(t *testing.T, h *harness) int {
	t.Helper()
	n, err := manufacturing.ReplenishmentCycle(context.Background(), h.Deps())
	if err != nil {
		t.Fatalf("ReplenishmentCycle: %v", err)
	}
	return n
}

// suggestions is the open suggestions at a site, by variant.
func suggestions(t *testing.T, c *modtest.Client, site int64) map[int32]gen.ManufacturingSuggestion {
	t.Helper()
	res := c.Do(http.MethodGet, suggestionsPath, nil)
	mustStatus(t, res, http.StatusOK, "list the suggestions")
	var list gen.ManufacturingSuggestionList
	res.JSON(&list)
	out := map[int32]gen.ManufacturingSuggestion{}
	for _, g := range list.Sites {
		if g.Site.Id != site {
			continue
		}
		for _, s := range g.Suggestions {
			out[s.Variant.Id] = s
		}
	}
	return out
}

// TestReplenishment_Projection is M107 through the worker: usable on hand,
// reserved and incoming from Inventory; an open order's output counted and
// its components' unreserved demand subtracted; a manual draft not counted;
// a make-to-order order's output left out and its sales line counted less
// what the order still makes — what it produced already is on hand, so it
// stays demand; unreserved sales demand subtracted; the quantity rounded to
// the multiple.
func TestReplenishment_Projection(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)

	// An open order of 3: supply 3, and its wheels (2 each, nothing
	// reserved) needed by production 6.
	confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "3")))
	// A manual draft is no supply.
	createOrder(t, c, orderInput(varBike, "100"))
	// A make-to-order order of 50 for sales line 901, 20 produced: its
	// output is no supply, and the line's 50 counts less the 30 it still
	// makes — the 20 produced are in on hand, promised to the line.
	mto := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "50"))).Order
	h.Exec(t, `UPDATE manufacturing.orders SET origin_kind = 'sales_order', origin_order_id = 90, origin_line_id = 901, produced = 20 WHERE id = $1`, mto.Id)
	lines.seedUnreserved(siteMain,
		contracts.UnreservedDemand{OrderID: 90, LineID: 901, VariantID: varBike, Quantity: "50"},
		contracts.UnreservedDemand{OrderID: 91, LineID: 902, VariantID: varBike, Quantity: "4"},
	)
	h.stock.setPlacements(siteMain,
		contracts.StockPlacement{VariantID: varBike, ReorderPoint: "20", Maximum: "40", Multiple: "5", OnHand: "30", Reserved: "25", Incoming: "2"},
		contracts.StockPlacement{VariantID: varWheel, ReorderPoint: "10", OnHand: "12", Reserved: "0", Incoming: "0"},
		contracts.StockPlacement{VariantID: varTube, ReorderPoint: "1", OnHand: "5", Reserved: "0", Incoming: "0"},
	)

	if n := runReplenishment(t, h); n < 1 {
		t.Fatalf("runs finished = %d", n)
	}
	got := suggestions(t, c, siteMain)
	bike, wheel := got[varBike], got[varWheel]
	// 30 − 25 + 2 + 3 − 0 − (20 + 4) = −14; 40 + 14 = 54, rounded up to 55.
	if bike.Kind != gen.Make || bike.Quantity != "55" || bike.Reason.Projected != "-14" || bike.Reason.InProduction != "3" ||
		bike.Reason.NeededBySales != "24" || bike.Reason.NeededByProduction != "0" || bike.BomId == nil {
		t.Errorf("bike = %+v", bike)
	}
	// The open orders' wheels — the make-to-order one's too, which still
	// consumes them — 2 × 3 + 2 × 50 = 106: 12 − 106 = −94, buy 105.
	if wheel.Kind != gen.Buy || wheel.Quantity != "105" || wheel.Reason.NeededByProduction != "106" || wheel.BomId != nil {
		t.Errorf("wheel = %+v", wheel)
	}
	if _, ok := got[varTube]; ok || len(got) != 2 {
		t.Errorf("suggestions = %+v", got)
	}

	// A run asked for now is answered for a minute instead of a second one.
	var first, second gen.ManufacturingReplenishmentRun
	res := c.Do(http.MethodPost, runsPath, gen.ManufacturingReplenishmentRunInput{})
	mustStatus(t, res, http.StatusAccepted, "run now")
	res.JSON(&first)
	c.Do(http.MethodPost, runsPath, gen.ManufacturingReplenishmentRunInput{}).JSON(&second)
	if first.Id != second.Id || first.Kind != gen.ManufacturingReplenishmentRunKindManual {
		t.Errorf("runs = %+v, %+v", first, second)
	}
	runReplenishment(t, h)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.suggestions WHERE status = 'open'`); n != 2 {
		t.Errorf("open suggestions after a second run = %d, want 2 (the first superseded)", n)
	}
}

// TestReplenishment_AutoCreateOnce is M111's automatic creation: off by
// default; on, the run makes one draft from the suggestion, which the next
// run counts as supply, and a run still short adds no second order while
// the first is open, marking the suggestion instead.
func TestReplenishment_AutoCreateOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.setPlacements(siteMain, contracts.StockPlacement{VariantID: varBike, ReorderPoint: "10", Maximum: "20", OnHand: "0", Reserved: "0", Incoming: "0"})

	runReplenishment(t, h)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.orders WHERE origin_kind = 'reorder'`); n != 0 {
		t.Fatalf("orders made with no rule = %d", n)
	}

	res := c.Do(http.MethodPut, "/api/v1/manufacturing/replenishment/rules/101/1", gen.ManufacturingReplenishmentRuleInput{AutoCreate: true})
	mustStatus(t, res, http.StatusOK, "set the rule")
	var rules gen.ManufacturingReplenishmentRuleList
	c.Do(http.MethodGet, "/api/v1/manufacturing/replenishment/rules?variantId=101", nil).JSON(&rules)
	if i := slices.IndexFunc(rules.Items, func(r gen.ManufacturingReplenishmentRule) bool { return r.SiteId == siteMain }); i < 0 ||
		!rules.Items[i].AutoCreate || rules.Items[i].AutoConfirm || len(rules.Items) != 2 {
		t.Errorf("rules = %+v", rules.Items)
	}
	h.Advance(24 * time.Hour) // the next night
	runReplenishment(t, h)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.orders WHERE origin_kind = 'reorder' AND status = 'draft' AND quantity = 20`); n != 1 {
		t.Fatalf("drafts made = %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.suggestions WHERE status = 'done' AND order_id IS NOT NULL`); n != 1 {
		t.Errorf("suggestions done = %d", n)
	}

	// The draft is supply: 0 + 20 > 10, nothing suggested.
	h.Advance(24 * time.Hour)
	runReplenishment(t, h)
	c = h.SignIn(t, planner...) // the session of two nights ago has expired
	if got := suggestions(t, c, siteMain); len(got) != 0 {
		t.Errorf("suggested again with the draft open: %+v", got)
	}

	// Still short with the draft counted: listed, marked, no second order.
	h.stock.setPlacements(siteMain, contracts.StockPlacement{VariantID: varBike, ReorderPoint: "30", Maximum: "40", OnHand: "0", Reserved: "0", Incoming: "0"})
	h.Advance(24 * time.Hour)
	runReplenishment(t, h)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.orders WHERE origin_kind = 'reorder'`); n != 1 {
		t.Errorf("reorder orders = %d, want still 1", n)
	}
	c = h.SignIn(t, planner...)
	s := suggestions(t, c, siteMain)[varBike]
	if !s.ReorderOpen || s.Quantity != "20" {
		t.Errorf("suggestion = %+v", s)
	}
	// A person's create is refused too while the draft is open.
	var result gen.ManufacturingSuggestionOrdersResult
	c.Do(http.MethodPost, suggestionsPath+"/orders", gen.ManufacturingSuggestionOrdersInput{
		Items: []gen.ManufacturingSuggestionOrderItem{{SuggestionId: s.Id}}}).JSON(&result)
	if len(result.Made) != 0 || len(result.Failed) != 1 || result.Failed[0].Code != "reorder_open" {
		t.Errorf("create with a reorder open = %+v", result)
	}
}

// setAutoCreate turns automatic creation of the bicycle at the main site on.
func setAutoCreate(t *testing.T, c *modtest.Client) {
	t.Helper()
	res := c.Do(http.MethodPut, "/api/v1/manufacturing/replenishment/rules/101/1", gen.ManufacturingReplenishmentRuleInput{AutoCreate: true})
	mustStatus(t, res, http.StatusOK, "set the rule")
}

// TestReplenishment_AutoCreateRace is M111's one order per variant against
// a race: an order from a suggestion made between the run's reads and its
// transaction is seen under LockVariants, so the run makes none and marks
// its suggestion instead; and two runs at once make one order between them.
func TestReplenishment_AutoCreateRace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	setAutoCreate(t, c)
	h.stock.setPlacements(siteMain, contracts.StockPlacement{VariantID: varBike, ReorderPoint: "10", Maximum: "20", OnHand: "0", Reserved: "0", Incoming: "0"})

	// The window: the run's create reads the site's locations, then a
	// person's reorder order lands before its transaction.
	var made int64
	h.stock.onSiteLocationsOnce(func() {
		o := createOrder(t, c, orderInput(varBike, "5"))
		h.Exec(t, `UPDATE manufacturing.orders SET origin_kind = 'reorder' WHERE id = $1`, o.Id)
		made = o.Id
	})
	runReplenishment(t, h)
	if made == 0 {
		t.Fatal("the window never opened")
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.orders WHERE origin_kind = 'reorder'`); n != 1 {
		t.Errorf("reorder orders = %d, want only the person's", n)
	}
	if s := suggestions(t, c, siteMain)[varBike]; s.Status != gen.ManufacturingSuggestionStatusOpen || !s.ReorderOpen {
		t.Errorf("suggestion = %+v, want open and marked", s)
	}

	// Two runs at once, with no order open: one order between them.
	h.Exec(t, `UPDATE manufacturing.orders SET status = 'cancelled' WHERE id = $1`, made)
	h.Exec(t, `INSERT INTO manufacturing.replenishment_runs (kind, requested_at) VALUES ('manual', $1), ('manual', $1)`, h.Now())
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { _, errs[i] = manufacturing.ReplenishmentCycle(context.Background(), h.Deps()) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("ReplenishmentCycle: %v", err)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.orders WHERE origin_kind = 'reorder' AND status = 'draft'`); n != 1 {
		t.Errorf("drafts made by two runs at once = %d, want 1", n)
	}
}

// TestReplenishment_ReorderDraftComponents is M107 over a draft from a
// suggestion: it is supply, and what it will consume — its BoM's lines
// scaled to its quantity, with the scrap — is component demand.
func TestReplenishment_ReorderDraftComponents(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	d := createOrder(t, c, orderInput(varBike, "10"))
	h.Exec(t, `UPDATE manufacturing.orders SET origin_kind = 'reorder' WHERE id = $1`, d.Id)
	h.stock.setPlacements(siteMain,
		contracts.StockPlacement{VariantID: varBike, ReorderPoint: "1", OnHand: "100", Reserved: "0", Incoming: "0"},
		contracts.StockPlacement{VariantID: varWheel, ReorderPoint: "10", OnHand: "12", Reserved: "0", Incoming: "0"},
		contracts.StockPlacement{VariantID: varBolt, ReorderPoint: "10", OnHand: "40", Reserved: "0", Incoming: "0"},
	)
	runReplenishment(t, h)
	got := suggestions(t, c, siteMain)
	// Wheels 2 × 10 = 20: 12 − 20 = −8, buy 19. Bolts 3 × 10 × 1.1 = 33:
	// 40 − 33 = 7, buy 4.
	if w := got[varWheel]; w.Quantity != "19" || w.Reason.NeededByProduction != "20" {
		t.Errorf("wheel = %+v", w)
	}
	if b := got[varBolt]; b.Quantity != "4" || b.Reason.NeededByProduction != "33" {
		t.Errorf("bolt = %+v", b)
	}
	if _, ok := got[varBike]; ok {
		t.Errorf("the bicycle, 100 + 10 on hand and in production, was suggested")
	}
}

// TestReplenishment_Sites is M109 over the sites: an archived site's open
// suggestions are superseded by the next run and no longer counted; a site
// whose read fails fails the run, naming it, while the others are judged.
func TestReplenishment_Sites(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	low := contracts.StockPlacement{VariantID: varBike, ReorderPoint: "10", OnHand: "0", Reserved: "0", Incoming: "0"}
	h.stock.setPlacements(siteBranch, low)
	h.stock.setPlacements(siteMain, low)
	runReplenishment(t, h)
	var stats gen.ManufacturingStats
	c.Do(http.MethodGet, base+"/stats", nil).JSON(&stats)
	if stats.SuggestionsToMake != 2 {
		t.Fatalf("suggestions to make = %d, want 2", stats.SuggestionsToMake)
	}

	h.stock.archiveSite(siteBranch)
	c.Do(http.MethodGet, base+"/stats", nil).JSON(&stats)
	if stats.SuggestionsToMake != 1 {
		t.Errorf("suggestions to make with the branch archived = %d, want 1", stats.SuggestionsToMake)
	}
	h.Exec(t, `INSERT INTO manufacturing.replenishment_runs (kind, requested_at) VALUES ('manual', $1)`, h.Now())
	runReplenishment(t, h)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.suggestions WHERE status = 'open' AND site_id = $1`, siteBranch); n != 0 {
		t.Errorf("open suggestions at the archived branch = %d", n)
	}

	// A failing site: the run fails naming it; the others are judged.
	h2 := newHarness(t)
	c2 := h2.SignIn(t, planner...)
	h2.stock.setPlacements(siteMain, contracts.StockPlacement{VariantID: varWheel, ReorderPoint: "10", OnHand: "0", Reserved: "0", Incoming: "0"})
	h2.stock.failPlacements(siteBranch)
	if n := runReplenishment(t, h2); n != 0 {
		t.Errorf("runs finished = %d, want the run failed", n)
	}
	if s := suggestions(t, c2, siteMain)[varWheel]; s.Quantity != "11" {
		t.Errorf("the main site's suggestion = %+v", s)
	}
	var runs gen.ManufacturingReplenishmentRunList
	c2.Do(http.MethodGet, runsPath, nil).JSON(&runs)
	if len(runs.Items) == 0 || runs.Items[0].Status != gen.ManufacturingReplenishmentRunStatusFailed || runs.Items[0].Suggestions != 1 ||
		runs.Items[0].Error == nil || !strings.Contains(*runs.Items[0].Error, "site BRANCH") || strings.Contains(*runs.Items[0].Error, "MAIN") {
		t.Errorf("runs = %+v", runs.Items)
	}
}

// TestReplenishment_LostClaim is M109's lease: a run claimed again while it
// runs — its lease ran out and another worker took it — stops before the
// next site and finishes nothing; the new claim is the one to finish it.
func TestReplenishment_LostClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.stock.setPlacements(siteMain, contracts.StockPlacement{VariantID: varWheel, ReorderPoint: "10", OnHand: "0", Reserved: "0", Incoming: "0"})
	// A person's run, claimed before the night's.
	h.Exec(t, `INSERT INTO manufacturing.replenishment_runs (kind, requested_at) VALUES ('manual', $1)`, h.Now().Add(-time.Hour))
	// BRANCH is judged first; meanwhile another worker claims the run.
	var once sync.Once
	h.stock.onPlacements(func(site int64) {
		if site == siteBranch {
			once.Do(func() {
				h.Exec(t, `UPDATE manufacturing.replenishment_runs SET attempts = attempts + 1 WHERE kind = 'manual'`)
			})
		}
	})
	runReplenishment(t, h)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.replenishment_runs WHERE kind = 'manual' AND status = 'running' AND finished_at IS NULL`); n != 1 {
		t.Errorf("the reclaimed run is not left running for its new claim")
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.suggestions s JOIN manufacturing.replenishment_runs r ON r.id = s.run_id
		WHERE r.kind = 'manual'`); n != 0 {
		t.Errorf("suggestions of the lost run = %d, want none: it stops before the next site", n)
	}
}

// TestReplenishment_RunNowPerSite is M109's one a minute, per site: a run
// of one site is not answered with one of every site, or of another.
func TestReplenishment_RunNowPerSite(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	start := func(site *int64) gen.ManufacturingReplenishmentRun {
		var run gen.ManufacturingReplenishmentRun
		res := c.Do(http.MethodPost, runsPath, gen.ManufacturingReplenishmentRunInput{SiteId: site})
		mustStatus(t, res, http.StatusAccepted, "run now")
		res.JSON(&run)
		return run
	}
	main, branch := siteMain, siteBranch
	first := start(&main)
	every := start(nil)
	again := start(&main)
	other := start(&branch)
	if first.Id != again.Id || every.Id == first.Id || other.Id == first.Id || other.Id == every.Id ||
		every.SiteId != nil || other.SiteId == nil || *other.SiteId != siteBranch {
		t.Errorf("runs = %+v, %+v, %+v, %+v", first, every, again, other)
	}
}

// TestReplenishment_CreateAndDismiss is the screen's actions (M111): an
// order made and confirmed from a make suggestion with an edited quantity,
// due on its need-by date, origin reorder; the suggestion is then done and a
// second create fails suggestion_not_open; a buy suggestion is never made;
// a dismissed one cannot be dismissed again.
func TestReplenishment_CreateAndDismiss(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.setPlacements(siteMain,
		contracts.StockPlacement{VariantID: varBike, ReorderPoint: "10", OnHand: "0", Reserved: "0", Incoming: "0"},
		contracts.StockPlacement{VariantID: varWheel, ReorderPoint: "10", OnHand: "0", Reserved: "0", Incoming: "0"},
	)
	runReplenishment(t, h)
	got := suggestions(t, c, siteMain)
	bike, wheel := got[varBike], got[varWheel]

	seven := "7"
	in := gen.ManufacturingSuggestionOrdersInput{Confirm: true, Items: []gen.ManufacturingSuggestionOrderItem{
		{SuggestionId: bike.Id, Quantity: &seven}, {SuggestionId: wheel.Id},
	}}
	var result gen.ManufacturingSuggestionOrdersResult
	res := c.Do(http.MethodPost, suggestionsPath+"/orders", in)
	mustStatus(t, res, http.StatusOK, "create from suggestions")
	res.JSON(&result)
	if len(result.Made) != 1 || result.Made[0].SuggestionId != bike.Id || len(result.Failed) != 1 ||
		result.Failed[0].SuggestionId != wheel.Id || result.Failed[0].Code != "bom_required" {
		t.Fatalf("result = %+v", result)
	}
	o := getOrder(t, c, result.Made[0].OrderId)
	if o.Origin.Kind != gen.ManufacturingOriginReorder || o.Status != gen.ManufacturingOrderStatusConfirmed || o.Quantity != "7" ||
		o.DueDate == nil || !o.DueDate.Equal(bike.NeedBy.Time) || o.SiteId != siteMain {
		t.Errorf("order = %+v", o)
	}
	c.Do(http.MethodPost, suggestionsPath+"/orders", gen.ManufacturingSuggestionOrdersInput{
		Items: []gen.ManufacturingSuggestionOrderItem{{SuggestionId: bike.Id}}}).JSON(&result)
	if len(result.Made) != 0 || len(result.Failed) != 1 || result.Failed[0].Code != "suggestion_not_open" {
		t.Errorf("second create = %+v", result)
	}

	zero := "0"
	res = c.Do(http.MethodPost, suggestionsPath+"/orders", gen.ManufacturingSuggestionOrdersInput{
		Items: []gen.ManufacturingSuggestionOrderItem{{SuggestionId: wheel.Id}, {SuggestionId: bike.Id, Quantity: &zero}}})
	if p := expectRefusal(t, res, http.StatusBadRequest, "quantity_invalid", "a quantity of 0"); p.Errors == nil ||
		(*p.Errors)["items[1].quantity"] == nil {
		t.Errorf("errors = %+v, want items[1].quantity", p.Errors)
	}

	dismiss := pathOf(suggestionsPath, wheel.Id, "dismiss")
	mustStatus(t, c.Do(http.MethodPost, dismiss, nil), http.StatusOK, "dismiss")
	mustStatus(t, c.Do(http.MethodPost, dismiss, nil), http.StatusConflict, "dismiss again")

	// The actions need orders-manage.
	reader := h.SignIn(t, "manufacturing:access")
	mustStatus(t, reader.Do(http.MethodPost, runsPath, gen.ManufacturingReplenishmentRunInput{}), http.StatusForbidden, "run as a reader")
}

// TestReplenishment_WorkerOnAPoolOfOne is the worker as a deployment builds
// it — through module.Workers, the stock and the sales order lines from the
// enabled modules providing them — on a pool of one: it reads every
// contract holding no connection, so the run finishes.
func TestReplenishment_WorkerOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.setPlacements(siteMain, contracts.StockPlacement{VariantID: varBike, ReorderPoint: "5", OnHand: "0", Reserved: "0", Incoming: "0"})
	lines.seedUnreserved(siteMain, contracts.UnreservedDemand{OrderID: 1, LineID: 2, VariantID: varBike, Quantity: "3"})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	base := h.DepsOn(one)
	base.Products, base.Stock, base.StockLedger, base.SalesOrderLines, base.Production = nil, nil, nil, nil, nil
	cfg := *base.Config
	cfg.Modules = slices.Clone(cfg.Modules)
	for _, name := range []string{"products", "inventory", "orders"} {
		if !slices.Contains(cfg.Modules, name) {
			cfg.Modules = append(cfg.Modules, name)
		}
	}
	base.Config = &cfg
	workers := module.Workers(base,
		module.Module{Name: "products", Products: func(module.Deps) contracts.ProductCatalog { return h.catalog }},
		module.Module{Name: "inventory",
			Stock:       func(module.Deps) contracts.Stock { return h.stock },
			StockLedger: func(module.Deps) contracts.StockLedger { return h.stock }},
		module.Module{Name: "orders", SalesOrderLines: func(module.Deps) contracts.SalesOrderLines { return lines }},
		manufacturing.Module())
	var cycle interface {
		RunCycle(context.Context) (int, error)
	}
	for _, w := range workers {
		if w.Name() == "manufacturing.replenishment" {
			cycle, _ = w.(interface {
				RunCycle(context.Context) (int, error)
			})
		}
	}
	if cycle == nil {
		t.Fatalf("module.Workers built no replenishment worker with a cycle: %d workers", len(workers))
	}
	if n, err := cycle.RunCycle(ctx); err != nil || n < 1 {
		t.Fatalf("RunCycle on a pool of one = %d, %v", n, err)
	}
	// 0 − 3 (sales) = −3 ≤ 5: target 6, make 9.
	if s := suggestions(t, c, siteMain)[varBike]; s.Quantity != "9" || s.Reason.NeededBySales != "3" {
		t.Errorf("suggestion = %+v", s)
	}
	var run gen.ManufacturingReplenishmentRunList
	c.Do(http.MethodGet, runsPath, nil).JSON(&run)
	if len(run.Items) == 0 || run.Items[0].Status != gen.ManufacturingReplenishmentRunStatusDone || !run.Items[0].SalesCounted {
		t.Errorf("runs = %+v", run.Items)
	}
}
