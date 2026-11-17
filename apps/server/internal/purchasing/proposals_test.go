package purchasing_test

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

const (
	proposalsPath      = "/api/v1/purchasing/proposals"
	proposalOrdersPath = "/api/v1/purchasing/proposals/orders"
)

// fakeDemand is contracts.PurchaseDemand over the needs a test sets, in place
// of Inventory's low stock and Manufacturing's buy suggestions.
type fakeDemand struct {
	mu    sync.Mutex
	needs []contracts.PurchaseNeed
}

func (f *fakeDemand) set(needs ...contracts.PurchaseNeed) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.needs = slices.Clone(needs)
}

func (f *fakeDemand) Demand(_ context.Context, siteID *int64) ([]contracts.PurchaseNeed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.PurchaseNeed
	for _, n := range f.needs {
		if siteID == nil || n.SiteID == *siteID {
			out = append(out, n)
		}
	}
	return out, nil
}

// lowStock is a low-stock need computed now.
func lowStock(h *harness, variant int32, site int64, quantity string) contracts.PurchaseNeed {
	return contracts.PurchaseNeed{VariantID: variant, SiteID: site, Quantity: quantity, Source: "low_stock", ComputedAt: h.Now()}
}

// newItem creates a supplier item and answers it.
func newItem(t *testing.T, c *modtest.Client, in gen.PurchasingSupplierItemInput) gen.PurchasingSupplierItem {
	t.Helper()
	res := c.Do(http.MethodPost, itemsPath, in)
	mustStatus(t, res, http.StatusCreated, "create a supplier item")
	return itemOf(t, res)
}

// proposals reads the proposals at query.
func proposals(t *testing.T, c *modtest.Client, query string) gen.PurchasingProposalList {
	t.Helper()
	res := c.Do(http.MethodGet, proposalsPath+query, nil)
	mustStatus(t, res, http.StatusOK, "list the proposals")
	var out gen.PurchasingProposalList
	res.JSON(&out)
	return out
}

// proposalAt is the proposal of variant at site, nil when there is none.
func proposalAt(list gen.PurchasingProposalList, variant int32, site int64) *gen.PurchasingProposal {
	for i := range list.Rows {
		if list.Rows[i].VariantId == variant && list.Rows[i].SiteId == site {
			return &list.Rows[i]
		}
	}
	return nil
}

// createFromProposals posts Create orders.
func createFromProposals(c *modtest.Client, id uuid.UUID, rows ...gen.PurchasingProposalOrdersRow) *modtest.Response {
	return c.Do(http.MethodPost, proposalOrdersPath, gen.PurchasingProposalOrdersInput{ClientRequestId: id, Rows: rows})
}

func proposalRow(variant int32, site, item int64, quantity string) gen.PurchasingProposalOrdersRow {
	return gen.PurchasingProposalOrdersRow{VariantId: variant, SiteId: site, SupplierItemId: item, OrderQuantity: quantity}
}

// proposalSetup is two suppliers' items of the bolt and the demand of P31.
type proposalSetup struct {
	h       *harness
	c       *modtest.Client
	demand  *fakeDemand
	norsk   gen.PurchasingSupplierItem // the bolt in boxes of 10 at 12.50 NOK, minimum 3, multiple 2, 7 days
	german  gen.PurchasingSupplierItem // the bolt by the piece at 1.10 EUR
	germanN gen.PurchasingSupplierItem // the nut by the piece at 0.50 EUR
}

func newProposalSetup(t *testing.T) proposalSetup {
	t.Helper()
	demand := &fakeDemand{}
	h := newHarness(t, harnessOptions{demand: demand})
	c := h.SignIn(t, buyer...)
	s := proposalSetup{h: h, c: c, demand: demand}
	s.norsk = newItem(t, c, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: varBolt, SupplierItemNumber: str("NS-88"),
		Price: str("12.5"), PackagingId: i32(packBox), LeadTimeDays: i32(7), MinimumQuantity: str("3"), Multiple: str("2")})
	s.german = newItem(t, c, gen.PurchasingSupplierItemInput{SupplierId: supGerman, VariantId: varBolt, Price: str("1.1")})
	s.germanN = newItem(t, c, gen.PurchasingSupplierItemInput{SupplierId: supGerman, VariantId: varNut, Price: str("0.5")})
	return s
}

// TestProposals_ListAndRound is P31–P33 and A4 against Inventory's low
// stock: one row per variant and site with every active supplier's item, the
// preferred first, rounded once to each — whole packagings, then the minimum,
// then the multiple — with its price and the day to order by; a variant with
// no supplier item listed with no option; the site and variant filters.
func TestProposals_ListAndRound(t *testing.T) {
	t.Parallel()
	s := newProposalSetup(t)
	h, c := s.h, s.c
	s.demand.set(
		lowStock(h, varBolt, siteMain, "23"),
		lowStock(h, varBolt, siteSecond, "5"),
		lowStock(h, varNut, siteMain, "4"),
		lowStock(h, varService, siteMain, "2"),
		lowStock(h, varBolt, siteArchived, "9"), // an archived site's row is left out
	)
	h.dir.seed(contracts.SupplierEntry{ID: 600, Name: "Uten AS", Country: "NO", Currency: "NOK", Active: true})
	list := proposals(t, c, "")
	if list.Source == nil || *list.Source != "low_stock" || len(list.Rows) != 4 {
		t.Fatalf("proposals = %+v", list)
	}
	bolt := proposalAt(list, varBolt, siteMain)
	if bolt == nil || bolt.Need != "23" || bolt.Remaining != "23" || bolt.OnDrafts != "0" || bolt.SentSince != "0" ||
		bolt.SiteName != "Hovedlager" || bolt.Sku != "BOLT-M8" || len(bolt.Options) != 2 {
		t.Fatalf("the bolt at the main site = %+v", bolt)
	}
	n, g := bolt.Options[0], bolt.Options[1]
	// 23 pcs: 2.3 boxes, 3 whole, the minimum 3, the multiple 2: 4 boxes.
	if n.SupplierItemId != s.norsk.Id || !n.Preferred || n.OrderQuantity != "4" || n.Quantity != "40" || n.PackagingId == nil ||
		*n.PackagingId != packBox || n.PackagingQuantity != "10" || deref(n.Price) != "12.50" || n.Currency != "NOK" || n.CurrencyDiffers ||
		deref(n.SupplierItemNumber) != "NS-88" || n.SupplierName != "Norsk Stål AS" {
		t.Errorf("the Norwegian option = %+v", n)
	}
	if g.SupplierItemId != s.german.Id || g.Preferred || g.OrderQuantity != "23" || g.Quantity != "23" || g.PackagingId != nil ||
		deref(g.Price) != "1.10" || g.Currency != "EUR" {
		t.Errorf("the German option = %+v", g)
	}
	if n.OrderBy.Time.AddDate(0, 0, 7) != g.OrderBy.Time {
		t.Errorf("order by %s and %s: want the lead time of 7 days between them", n.OrderBy, g.OrderBy)
	}
	// 5 pcs at the second site: 1 box, the minimum 3, the multiple: 4.
	if second := proposalAt(list, varBolt, siteSecond); second == nil || second.Options[0].OrderQuantity != "4" || second.SiteName != "Vestlager" {
		t.Errorf("the bolt at the second site = %+v", second)
	}
	// A variant with no supplier item is listed, under No supplier.
	if svc := proposalAt(list, varService, siteMain); svc == nil || len(svc.Options) != 0 {
		t.Errorf("the service = %+v, want it with no option", svc)
	}
	if nut := proposalAt(list, varNut, siteMain); nut == nil || len(nut.Options) != 1 || nut.Options[0].OrderQuantity != "4" {
		t.Errorf("the nut = %+v", nut)
	}

	if one := proposals(t, c, "?variantId=104"); len(one.Rows) != 1 || one.Rows[0].VariantId != varNut {
		t.Errorf("filtered to the nut = %+v", one.Rows)
	}
	if second := proposals(t, c, "?siteId=13"); len(second.Rows) != 1 || second.Rows[0].SiteId != siteSecond {
		t.Errorf("filtered to the second site = %+v", second.Rows)
	}

	// By the unit a need is not rounded to a whole; an inactive supplier's
	// item is no option.
	s.demand.set(lowStock(h, varNut, siteMain, "2.5"), lowStock(h, varBolt, siteMain, "1"))
	if nut := proposalAt(proposals(t, c, ""), varNut, siteMain); nut == nil || nut.Options[0].OrderQuantity != "2.5" {
		t.Errorf("the nut by the piece = %+v", nut)
	}
	h.dir.update(supGerman, func(e *contracts.SupplierEntry) { e.Active = false })
	list = proposals(t, c, "")
	if b := proposalAt(list, varBolt, siteMain); b == nil || len(b.Options) != 1 || b.Options[0].SupplierItemId != s.norsk.Id {
		t.Errorf("with the German supplier inactive = %+v", b)
	}

	// A replenishment need: needed on a day, the day to order by before it.
	needed := time.Date(2030, 3, 20, 0, 0, 0, 0, time.UTC)
	s.demand.set(contracts.PurchaseNeed{VariantID: varBolt, SiteID: siteMain, Quantity: "12", NeededOn: &needed, Source: "replenishment", ComputedAt: h.Now()})
	list = proposals(t, c, "")
	if b := proposalAt(list, varBolt, siteMain); list.Source == nil || *list.Source != "replenishment" || b == nil || b.NeededOn == nil ||
		b.NeededOn.Time != needed || b.Options[0].OrderBy.Time != needed.AddDate(0, 0, -7) || b.Options[0].OrderQuantity != "4" {
		t.Errorf("the replenishment row = %+v", b)
	}
}

// TestProposals_Netting is A4: a proposal is the need less what
// Purchasing's drafts hold for the variant and the site, and less what was
// sent to the site after the source computed it — an order sent before is
// already in the source's figure, and a cancelled one holds nothing — and a
// need covered so is no row.
func TestProposals_Netting(t *testing.T) {
	t.Parallel()
	s := newProposalSetup(t)
	h, c := s.h, s.c
	markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "1"))) // 10, before the run
	h.Advance(time.Minute)
	computed := h.Now()
	h.Advance(time.Minute)
	markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "2"))) // 20, after it
	cancelled := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "3")))
	mustStatus(t, c.Do(http.MethodPost, orderPath(cancelled.Id, "cancel"), gen.PurchasingCancelInput{ExpectedUpdatedAt: cancelled.UpdatedAt}),
		http.StatusOK, "cancel")
	createOrder(t, c, supGerman, variantLine(varBolt, "5")) // a draft: 5 pcs
	createOrder(t, c, supNorsk, variantLine(varNut, "1"))   // another variant's draft
	other := createOrder(t, c, supNorsk)                    // another site's draft counts nothing here
	other.SiteId = siteSecond
	u := updateOf(other)
	u.Lines = []gen.PurchasingLineInput{variantLine(varBolt, "9")}
	mustStatus(t, c.Do(http.MethodPut, orderPath(other.Id), u), http.StatusOK, "move a draft to the second site")

	s.demand.set(contracts.PurchaseNeed{VariantID: varBolt, SiteID: siteMain, Quantity: "50", Source: "replenishment", ComputedAt: computed})
	bolt := proposalAt(proposals(t, c, ""), varBolt, siteMain)
	if bolt == nil || bolt.Need != "50" || bolt.OnDrafts != "5" || bolt.SentSince != "20" || bolt.Remaining != "25" {
		t.Fatalf("the bolt = %+v, want 50 less 5 on a draft and 20 sent since", bolt)
	}
	// 25 pcs: 3 boxes, the multiple: 4.
	if bolt.Options[0].OrderQuantity != "4" {
		t.Errorf("the Norwegian option = %+v", bolt.Options[0])
	}
	s.demand.set(contracts.PurchaseNeed{VariantID: varBolt, SiteID: siteMain, Quantity: "25", Source: "replenishment", ComputedAt: computed})
	if b := proposalAt(proposals(t, c, ""), varBolt, siteMain); b != nil {
		t.Errorf("a need covered by the draft and the sent order is still proposed: %+v", b)
	}
}

// TestProposals_CreateOrders is P33, P34 and A11: one draft per supplier
// and site, each line with the supplier item's number, packaging, price in
// its own currency and expected day; lines added to an existing draft of
// the supplier and site, a line of the variant in the item's packaging taking
// the quantity; a second click refused as no longer proposed; a replay
// answering the first result and another body under the same id refused.
func TestProposals_CreateOrders(t *testing.T) {
	t.Parallel()
	s := newProposalSetup(t)
	h, c := s.h, s.c
	// An existing Norwegian draft at the main site with a box of bolts.
	existing := createOrder(t, c, supNorsk, variantLine(varBolt, "1"))
	s.demand.set(
		lowStock(h, varBolt, siteMain, "33"),  // less the draft's 10: 23, 4 boxes
		lowStock(h, varBolt, siteSecond, "5"), // 4 boxes
		lowStock(h, varNut, siteMain, "7"),
	)
	// The register's German supplier now trades in NOK: its item's EUR price
	// is not taken (A32).
	h.dir.update(supGerman, func(e *contracts.SupplierEntry) { e.Currency = "NOK" })
	list := proposals(t, c, "")
	if nut := proposalAt(list, varNut, siteMain); nut == nil || !nut.Options[0].CurrencyDiffers {
		t.Fatalf("the nut = %+v, want its EUR price flagged", nut)
	}
	id := uuid.New()
	rows := []gen.PurchasingProposalOrdersRow{
		proposalRow(varBolt, siteMain, s.norsk.Id, "4"),
		proposalRow(varBolt, siteSecond, s.norsk.Id, "4"),
		proposalRow(varNut, siteMain, s.germanN.Id, "7"),
	}
	res := createFromProposals(c, id, rows...)
	mustStatus(t, res, http.StatusOK, "create orders")
	var out gen.PurchasingProposalOrdersResult
	res.JSON(&out)
	if len(out.Orders) != 3 {
		t.Fatalf("orders = %+v, want one per supplier and site", out.Orders)
	}
	byKey := map[[2]int64]gen.PurchasingProposalOrder{}
	for _, o := range out.Orders {
		byKey[[2]int64{o.SupplierId, o.SiteId}] = o
	}
	mainDraft, second, german := byKey[[2]int64{supNorsk, siteMain}], byKey[[2]int64{supNorsk, siteSecond}], byKey[[2]int64{supGerman, siteMain}]
	if mainDraft.OrderId != existing.Id || mainDraft.Created || mainDraft.LinesAdded != 1 || !second.Created || !german.Created {
		t.Errorf("orders = %+v", out.Orders)
	}
	o := getOrder(t, c, existing.Id)
	if len(o.Lines) != 1 || o.Lines[0].OrderQuantity != "5" || o.Lines[0].Quantity != "50" || o.NetTotal != "62.50" {
		t.Errorf("the existing draft = %+v, want its box line at 5 boxes", o.Lines)
	}
	o = getOrder(t, c, second.OrderId)
	l := o.Lines[0]
	if o.Status != gen.PurchasingOrderStatusDraft || o.SiteId != siteSecond || o.Currency != "NOK" || len(o.Lines) != 1 ||
		l.OrderQuantity != "4" || l.Quantity != "40" || l.UnitPrice != "12.50" || deref(l.SupplierItemNumber) != "NS-88" ||
		l.PackagingId == nil || *l.PackagingId != packBox || l.ExpectedOn == nil || l.ExpectedOn.Time != o.OrderDate.Time.AddDate(0, 0, 7) {
		t.Errorf("the second site's draft = %+v %+v", o, l)
	}
	if o = getOrder(t, c, german.OrderId); o.Currency != "NOK" || o.Lines[0].UnitPrice != "0.0000" && o.Lines[0].UnitPrice != "0" && o.Lines[0].UnitPrice != "0.00" {
		t.Errorf("the German draft = %s %+v, want NOK with no price from the EUR item", o.Currency, o.Lines)
	}
	if hist := historyOf(t, c, second.OrderId); len(hist.Changes) != 1 || len(hist.Changes[0].Actions) != 2 ||
		hist.Changes[0].Actions[1].Action != "from_proposals" || deref(hist.Changes[0].Actions[1].To) != "1" {
		t.Errorf("history = %+v", hist.Changes)
	}
	if list := proposals(t, c, ""); len(list.Rows) != 0 {
		t.Errorf("proposals after Create orders = %+v, want none", list.Rows)
	}

	lines := h.Count(t, `SELECT count(*) FROM purchasing.order_lines`)
	// A replay answers the first result and orders nothing more.
	res = createFromProposals(c, id, rows...)
	mustStatus(t, res, http.StatusOK, "replay")
	var replay gen.PurchasingProposalOrdersResult
	res.JSON(&replay)
	if len(replay.Orders) != 3 || h.Count(t, `SELECT count(*) FROM purchasing.order_lines`) != lines ||
		getOrder(t, c, existing.Id).Lines[0].OrderQuantity != "5" {
		t.Errorf("replay = %+v", replay.Orders)
	}
	// Another body under the same id.
	expectRefusal(t, createFromProposals(c, id, rows[0]), http.StatusConflict, "client_request_reused", "reused id")
	// A second click, a new id: every row is on a draft now.
	p := expectRefusal(t, createFromProposals(c, uuid.New(), rows...), http.StatusConflict, "proposal_gone", "second click")
	if p.Lines == nil || len(*p.Lines) != 3 {
		t.Errorf("proposal_gone lines = %+v", p.Lines)
	}
	if h.Count(t, `SELECT count(*) FROM purchasing.order_lines`) != lines {
		t.Error("the second click wrote lines")
	}
}

// TestProposals_CreateRefusals is every refusal of Create orders' rows,
// each named by its index, and the 503 with no demand provider.
func TestProposals_CreateRefusals(t *testing.T) {
	t.Parallel()
	s := newProposalSetup(t)
	h, c := s.h, s.c
	s.demand.set(lowStock(h, varBolt, siteMain, "10"), lowStock(h, varNut, siteMain, "3"))
	cases := []struct {
		row  gen.PurchasingProposalOrdersRow
		code string
	}{
		{proposalRow(varNut, siteMain, s.norsk.Id, "1"), "supplier_item_not_found"},
		{proposalRow(varBolt, siteMain, 99999, "1"), "supplier_item_not_found"},
		{proposalRow(varBolt, siteArchived, s.norsk.Id, "1"), "site_archived"},
		{proposalRow(varBolt, 77, s.norsk.Id, "1"), "site_not_found"},
		{proposalRow(varBolt, siteMain, s.norsk.Id, "0"), "quantity_invalid"},
		{proposalRow(varBolt, siteMain, s.norsk.Id, "1.2345"), "quantity_invalid"},
	}
	for _, tc := range cases {
		p := expectRefusal(t, createFromProposals(c, uuid.New(), tc.row), http.StatusBadRequest, tc.code, tc.code)
		if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].Index != 0 {
			t.Errorf("%s: lines = %+v", tc.code, p.Lines)
		}
	}
	p := expectRefusal(t, createFromProposals(c, uuid.New(), proposalRow(varBolt, siteMain, s.norsk.Id, "1"),
		proposalRow(varBolt, siteMain, s.german.Id, "1")), http.StatusBadRequest, "line_invalid", "the variant twice at a site")
	if (*p.Lines)[0].Index != 1 {
		t.Errorf("twice: lines = %+v", p.Lines)
	}
	// A row the demand does not propose.
	expectRefusal(t, createFromProposals(c, uuid.New(), proposalRow(varBolt, siteSecond, s.norsk.Id, "1")), http.StatusConflict, "proposal_gone", "not proposed")
	// An inactive supplier.
	h.dir.update(supGerman, func(e *contracts.SupplierEntry) { e.Active = false })
	expectRefusal(t, createFromProposals(c, uuid.New(), proposalRow(varNut, siteMain, s.germanN.Id, "3")), http.StatusConflict, "supplier_inactive", "inactive")
	if n := h.Count(t, `SELECT count(*) FROM purchasing.orders`); n != 0 {
		t.Errorf("orders after refusals = %d", n)
	}

	// With no demand provider: no rows, and Create orders is unavailable.
	bare := newHarness(t, harnessOptions{})
	bc := bare.SignIn(t, buyer...)
	if list := proposals(t, bc, ""); list.Source != nil || len(list.Rows) != 0 {
		t.Errorf("proposals with no demand = %+v", list)
	}
	item := newItem(t, bc, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: varBolt})
	expectRefusal(t, createFromProposals(bc, uuid.New(), proposalRow(varBolt, siteMain, item.Id, "1")), http.StatusServiceUnavailable,
		"dependency_unavailable", "no demand")
}

// TestProposals_Permissions is §8: the proposals are read with
// purchasing:access, and Create orders needs purchasing:orders-manage — each
// key alone.
func TestProposals_Permissions(t *testing.T) {
	t.Parallel()
	s := newProposalSetup(t)
	h := s.h
	s.demand.set(lowStock(h, varBolt, siteMain, "10"))
	for _, key := range []string{"purchasing:access", "purchasing:orders-manage", "purchasing:orders-send", "purchasing:receive",
		"purchasing:supplier-items-manage", "purchasing:invoices-match", "purchasing:settings"} {
		c := h.SignIn(t, key)
		res := c.Do(http.MethodGet, proposalsPath, nil)
		if (res.Status == http.StatusOK) != (key == "purchasing:access") || (res.Status != http.StatusOK && res.Status != http.StatusForbidden) {
			t.Errorf("GET proposals with %s alone = %d", key, res.Status)
		}
		res = createFromProposals(c, uuid.New(), proposalRow(varNut, siteMain, s.norsk.Id, "1"))
		if (res.Status == http.StatusForbidden) == (key == "purchasing:orders-manage") {
			t.Errorf("Create orders with %s alone = %d", key, res.Status)
		}
	}
	m := h.SignIn(t, "purchasing:orders-manage")
	mustStatus(t, createFromProposals(m, uuid.New(), proposalRow(varBolt, siteMain, s.norsk.Id, "4")), http.StatusOK, "orders-manage alone creates")
}

// TestProposals_LockOrder is rules 21 and 28 for Create orders: every
// contract is read before the transaction, and inside it the variants are
// locked before the suppliers. Not parallel: it reads the module's
// contract-call hook, which every test shares.
//
//nolint:paralleltest // swaps the package-wide contract-call hook
func TestProposals_LockOrder(t *testing.T) {
	s := newProposalSetup(t)
	h, c := s.h, s.c
	s.demand.set(lowStock(h, varBolt, siteMain, "10"))
	var mu sync.Mutex
	var calls []string
	purchasing.SetContractCallHook(func(ctx context.Context, method string) {
		mu.Lock()
		calls = append(calls, method)
		mu.Unlock()
		lockedContractCalls.note(ctx, method)
	})
	t.Cleanup(func() { purchasing.SetContractCallHook(lockedContractCalls.note) })
	mustStatus(t, createFromProposals(c, uuid.New(), proposalRow(varBolt, siteMain, s.norsk.Id, "2")), http.StatusOK, "create orders")
	purchasing.SetContractCallHook(lockedContractCalls.note)
	variants := slices.Index(calls, purchasing.LockVariantsCall)
	suppliers := slices.Index(calls, purchasing.LockSuppliersCall)
	demand := slices.Index(calls, "PurchaseDemand.Demand")
	if variants < 0 || suppliers < variants || demand < 0 || demand > variants || slices.Index(calls[variants:], "Stock.Sites") >= 0 {
		t.Errorf("calls = %v: want the demand and the sites read before LockVariants, then LockSuppliers", calls)
	}
}
