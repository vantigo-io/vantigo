package manufacturing_test

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// harness is one manufacturing installation for one test: internal/modtest's
// shared harness composing this module beside identity, every exchange
// validated against manufacturing.yaml through the package recorder, and
// fakes of the catalog and the stock in place of Products and Inventory,
// which depguard keeps out of this package. modtest lists products and
// inventory in MODULES whenever manufacturing is composed, so config's rule
// holds.
type harness struct {
	*modtest.Harness
	catalog *fakeCatalog
	stock   *fakeStock
}

// newHarness is a manufacturing installation with the seeded catalog and
// sites. Every harness carries the module's lock rule out of its test: no
// call out of the module's own database is made from inside one of its
// transactions, but LockVariants.
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	h := &harness{catalog: newFakeCatalog(), stock: newFakeStock()}
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(manufacturing.Module()),
		modtest.WithProducts(h.catalog),
		modtest.WithStock(h.stock, h.stock),
	}
	before := lockedContractCalls.count()
	h.Harness = modtest.New(t, append(base, opts...)...)
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	seed(h)
	return h
}

// The seeded catalog and sites.
const (
	varBike    int32 = 101 // a made bicycle, standard cost 2000
	varFrame   int32 = 102 // a made frame, no standard cost of its own
	varWheel   int32 = 103 // a bought wheel, standard cost 150.50
	varTube    int32 = 104 // a bought tube, standard cost 20
	varPaint   int32 = 105 // a bought paint, no standard cost
	varService int32 = 106 // a service
	varBolt    int32 = 107 // a bought bolt, standard cost 0.25
	varOld     int32 = 108 // goods of a discontinued product

	siteMain   int64 = 1
	siteBranch int64 = 2
	siteGone   int64 = 3 // archived
)

func goods(id int32, sku, name, cost string) contracts.VariantEntry {
	return contracts.VariantEntry{ID: id, ProductID: id, SKU: sku, ProductName: name, Unit: "pcs", ProductType: "Goods",
		ProductStatus: "Active", StandardCost: cost}
}

func seed(h *harness) {
	service := goods(varService, "SVC-1", "Assembly service", "")
	service.ProductType = "Service"
	old := goods(varOld, "OLD-1", "Old part", "")
	old.ProductStatus = "Discontinued"
	h.catalog.seed(goods(varBike, "BIKE-1", "Bicycle", "2000.00"), goods(varFrame, "FRAME-1", "Frame", ""),
		goods(varWheel, "WHEEL-1", "Wheel", "150.50"), goods(varTube, "TUBE-1", "Tube", "20.00"),
		goods(varPaint, "PAINT-1", "Paint", ""), service, goods(varBolt, "BOLT-1", "Bolt", "0.25"), old)
	h.stock.sites = []contracts.StockSite{
		{ID: siteBranch, Code: "BRANCH", Name: "Branch", Active: true, RootLocationID: 20},
		{ID: siteMain, Code: "MAIN", Name: "Main", Active: true, RootLocationID: 10},
		{ID: siteGone, Code: "OLD", Name: "Old site", Active: false, RootLocationID: 30},
	}
}

// The permission sets a test signs in with (§8).
var (
	reader     = []string{"manufacturing:access"}
	manager    = []string{"manufacturing:access", "manufacturing:boms-manage"}
	costReader = []string{"manufacturing:access", "manufacturing:costs-view"}
	everything = []string{"manufacturing:access", "manufacturing:boms-manage", "manufacturing:costs-view",
		"manufacturing:orders-manage", "manufacturing:shop-floor"}
)

// lockedContractCalls is every call out of the module made from inside one of
// its transactions, but LockVariants, reported by contractscalls.go's hook
// (TestMain) and by the fakes themselves.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if !manufacturing.InOpenTx(ctx) || method == manufacturing.LockVariantsCall {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, method+"\n"+string(debug.Stack()))
}

func (l *lockedCalls) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func (l *lockedCalls) since(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n >= len(l.calls) {
		return nil
	}
	return slices.Clone(l.calls[n:])
}

// fakeCatalog is contracts.ProductCatalog over the variants a test seeds. Its
// LockVariants answers lockedAs in place of a seeded entry when one is set:
// what the catalog says once the variant is locked, which may differ from
// what an earlier read said — the module must judge from the locked answer.
type fakeCatalog struct {
	mu       sync.Mutex
	variants map[int32]contracts.VariantEntry
	lockedAs map[int32]*contracts.VariantEntry
	locks    [][]int32
}

var _ contracts.ProductCatalog = (*fakeCatalog)(nil)

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{variants: map[int32]contracts.VariantEntry{}, lockedAs: map[int32]*contracts.VariantEntry{}}
}

func (c *fakeCatalog) seed(entries ...contracts.VariantEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		c.variants[e.ID] = e
	}
}

// lockAnswer makes LockVariants answer e for its id (nil: absent) while the
// other reads still answer the seeded entry.
func (c *fakeCatalog) lockAnswer(id int32, e *contracts.VariantEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lockedAs[id] = e
}

func (c *fakeCatalog) lockCalls() [][]int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.locks)
}

func (c *fakeCatalog) sorted() []contracts.VariantEntry {
	out := make([]contracts.VariantEntry, 0, len(c.variants))
	for _, v := range c.variants {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b contracts.VariantEntry) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (c *fakeCatalog) Variant(ctx context.Context, id int32) (*contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.Variant")
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.variants[id]; ok {
		return &v, nil
	}
	return nil, nil
}

func (c *fakeCatalog) Variants(ctx context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.Variants")
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []contracts.VariantEntry
	for _, id := range ids {
		if v, ok := c.variants[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (c *fakeCatalog) ListPrice(context.Context, int32, string, time.Time) (*contracts.Money, error) {
	return nil, nil
}

func (c *fakeCatalog) VariantsOfProduct(ctx context.Context, _ int32) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.VariantsOfProduct")
	return nil, nil
}

func (c *fakeCatalog) LookupCode(ctx context.Context, _ string) ([]contracts.CodeMatch, error) {
	lockedContractCalls.note(ctx, "fake Products.LookupCode")
	return nil, nil
}

func (c *fakeCatalog) LookupSKUs(ctx context.Context, _ []string) (map[string][]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.LookupSKUs")
	return map[string][]contracts.VariantEntry{}, nil
}

func (c *fakeCatalog) SearchVariants(ctx context.Context, q string, s contracts.VariantSearch) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.SearchVariants")
	c.mu.Lock()
	defer c.mu.Unlock()
	q = strings.ToLower(q)
	var out []contracts.VariantEntry
	for _, v := range c.sorted() {
		if len(s.Types) > 0 && !slices.Contains(s.Types, v.ProductType) {
			continue
		}
		if !s.IncludeInactive && v.ProductStatus != "Active" {
			continue
		}
		if strings.Contains(strings.ToLower(v.SKU), q) || strings.Contains(strings.ToLower(v.ProductName), q) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b contracts.VariantEntry) int { return cmp.Compare(a.SKU, b.SKU) })
	if len(out) > s.Limit {
		out = out[:s.Limit]
	}
	return out, nil
}

func (c *fakeCatalog) LockVariants(ctx context.Context, tx pgx.Tx, ids []int32) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, manufacturing.LockVariantsCall)
	if tx == nil {
		panic("LockVariants without a transaction")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.locks = append(c.locks, slices.Clone(ids))
	var out []contracts.VariantEntry
	for _, id := range ids {
		if e, ok := c.lockedAs[id]; ok {
			if e != nil {
				out = append(out, *e)
			}
			continue
		}
		if v, ok := c.variants[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (c *fakeCatalog) ProductImages(context.Context, int32, string) ([]contracts.ProductImage, error) {
	return nil, nil
}

func (c *fakeCatalog) OpenProductImage(context.Context, int32) (io.ReadCloser, contracts.ProductImage, error) {
	return nil, contracts.ProductImage{}, nil
}

// fakeStock is contracts.Stock over the sites a test seeds and the available
// quantities it sets per site, and — fakes_ledger_test.go — the
// contracts.StockLedger over the locations' on hand and the documents'
// reservations.
type fakeStock struct {
	mu        sync.Mutex
	sites     []contracts.StockSite
	available map[int64]map[int32]string
	batches   int
	// locationsHook, when set, runs after each Locations read: a test's
	// window between the read a post splits on and its transaction.
	locationsHook func()
	// placements are what Placements answers, per site (M106);
	// placementsFail, the sites whose read fails; placementsHook, when set,
	// runs after each Placements read, with the site.
	placements     map[int64][]contracts.StockPlacement
	placementsFail map[int64]bool
	placementsHook func(site int64)
	// siteLocationsHook, when set, runs once after the next SiteLocations
	// read: a test's window between a create's reads and its transaction.
	siteLocationsHook func()
	ledgerState
}

var _ contracts.Stock = (*fakeStock)(nil)

func newFakeStock() *fakeStock {
	return &fakeStock{available: map[int64]map[int32]string{}, ledgerState: newLedgerState()}
}

func (s *fakeStock) setAvailable(site int64, variant int32, q string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.available[site] == nil {
		s.available[site] = map[int32]string{}
	}
	s.available[site][variant] = q
}

// setPlacements is what Placements answers for the site.
func (s *fakeStock) setPlacements(site int64, ps ...contracts.StockPlacement) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.placements == nil {
		s.placements = map[int64][]contracts.StockPlacement{}
	}
	s.placements[site] = ps
}

func (s *fakeStock) Placements(ctx context.Context, site int64) ([]contracts.StockPlacement, error) {
	lockedContractCalls.note(ctx, "fake Stock.Placements")
	s.mu.Lock()
	out, fail, hook := slices.Clone(s.placements[site]), s.placementsFail[site], s.placementsHook
	s.mu.Unlock()
	if hook != nil {
		hook(site)
	}
	if fail {
		return nil, fmt.Errorf("fake stock: the placements at site %d are unreadable", site)
	}
	return out, nil
}

// failPlacements makes the site's Placements read fail.
func (s *fakeStock) failPlacements(site int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.placementsFail == nil {
		s.placementsFail = map[int64]bool{}
	}
	s.placementsFail[site] = true
}

// onPlacements installs the Placements hook.
func (s *fakeStock) onPlacements(hook func(site int64)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.placementsHook = hook
}

// onSiteLocationsOnce installs a hook the next SiteLocations read runs.
func (s *fakeStock) onSiteLocationsOnce(hook func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.siteLocationsHook = hook
}

func (s *fakeStock) archiveSite(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.sites {
		if s.sites[i].ID == id {
			s.sites[i].Active = false
		}
	}
}

func (s *fakeStock) OnHand(ctx context.Context, _ []int32, _ *int64) ([]contracts.StockQuantity, error) {
	lockedContractCalls.note(ctx, "fake Stock.OnHand")
	return nil, nil
}

func (s *fakeStock) Available(ctx context.Context, ids []int32, site *int64) ([]contracts.StockQuantity, error) {
	lockedContractCalls.note(ctx, "fake Stock.Available")
	if err := contracts.ValidateStockVariants(ids); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches++
	var out []contracts.StockQuantity
	for _, id := range ids {
		if site == nil {
			continue
		}
		q, set := s.available[*site][id]
		free := s.free(id, *site)
		if set {
			free.Add(free, rat(q))
		}
		if set || free.Sign() != 0 {
			out = append(out, contracts.StockQuantity{VariantID: id, Quantity: ratText(free)})
		}
	}
	return out, nil
}

// Sites answers the sites that are not archived, by code, as Stock does; a
// seeded inactive one is left out.
func (s *fakeStock) Sites(ctx context.Context) ([]contracts.StockSite, error) {
	lockedContractCalls.note(ctx, "fake Stock.Sites")
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contracts.StockSite
	for _, site := range s.sites {
		if site.Active {
			out = append(out, site)
		}
	}
	slices.SortFunc(out, func(a, b contracts.StockSite) int { return cmp.Compare(a.Code, b.Code) })
	return out, nil
}

func (s *fakeStock) Locations(ctx context.Context, ids []int32, site int64) ([]contracts.StockLocation, error) {
	lockedContractCalls.note(ctx, "fake Stock.Locations")
	s.mu.Lock()
	out, hook := s.locationsHolding(ids, site), s.locationsHook
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return out, nil
}

// onLocations installs the Locations hook.
func (s *fakeStock) onLocations(hook func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locationsHook = hook
}

// Lots is layer M's read; no tracked variant exists in this layer.
func (s *fakeStock) Lots(ctx context.Context, ids []int32, site int64) ([]contracts.StockLot, error) {
	lockedContractCalls.note(ctx, "fake Stock.Lots")
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.siteLots(ids, site), nil
}

func (s *fakeStock) SiteLocations(ctx context.Context, site int64) ([]contracts.StockSiteLocation, error) {
	lockedContractCalls.note(ctx, "fake Stock.SiteLocations")
	s.mu.Lock()
	out, hook := s.siteLocations(site), s.siteLocationsHook
	s.siteLocationsHook = nil
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return out, nil
}

// The paths the tests call.
const (
	base            = "/api/v1/manufacturing"
	workCentersPath = base + "/work-centers"
	bomsPath        = base + "/boms"
)

func workCenterPath(id int64, rest ...string) string {
	return pathOf(workCentersPath, id, rest...)
}

func bomPath(id int64, rest ...string) string { return pathOf(bomsPath, id, rest...) }

func pathOf(root string, id int64, rest ...string) string {
	p := fmt.Sprintf("%s/%d", root, id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

// mustStatus fails the test unless res answered want.
func mustStatus(t *testing.T, res *modtest.Response, want int, what string) {
	t.Helper()
	if res.Status != want {
		t.Fatalf("%s = %d %s, want %d", what, res.Status, res.Body, want)
	}
}

// expectRefusal fails the test unless res is a problem with status and code.
func expectRefusal(t *testing.T, res *modtest.Response, status int, code, what string) gen.ManufacturingProblem {
	t.Helper()
	mustStatus(t, res, status, what)
	var p gen.ManufacturingProblem
	res.JSON(&p)
	if code != "" && (p.Code == nil || *p.Code != code) {
		t.Fatalf("%s: code %v, want %s (%s)", what, p.Code, code, res.Body)
	}
	return p
}

func str(v string) *string { return &v }
func i32(v int32) *int32   { return &v }

// workCenterInput is a work center at site with code.
func workCenterInput(code string, site int64) gen.ManufacturingWorkCenterInput {
	return gen.ManufacturingWorkCenterInput{Code: code, Name: "Center " + code, SiteId: site}
}

// createWorkCenter creates a work center and answers it.
func createWorkCenter(t *testing.T, c *modtest.Client, in gen.ManufacturingWorkCenterInput) gen.ManufacturingWorkCenter {
	t.Helper()
	res := c.Do(http.MethodPost, workCentersPath, in)
	mustStatus(t, res, http.StatusCreated, "create work center "+in.Code)
	var w gen.ManufacturingWorkCenter
	res.JSON(&w)
	return w
}

// line is a BoM line of qty of variant.
func line(variant int32, qty string) gen.ManufacturingBomLineInput {
	return gen.ManufacturingBomLineInput{VariantId: variant, Quantity: qty}
}

// operation is a BoM operation at a work center.
func operation(key string, sequence int32, center int64, perCycle string) gen.ManufacturingBomOperationInput {
	return gen.ManufacturingBomOperationInput{Key: key, Sequence: sequence, Name: "Op " + key, WorkCenterId: &center, MinutesPerCycle: perCycle}
}

// bomInput is a BoM of variant making output with lines and no operations.
func bomInput(variant int32, output string, lines ...gen.ManufacturingBomLineInput) gen.ManufacturingBomInput {
	if lines == nil {
		lines = []gen.ManufacturingBomLineInput{}
	}
	return gen.ManufacturingBomInput{VariantId: variant, OutputQuantity: output, Lines: lines, Operations: []gen.ManufacturingBomOperationInput{}}
}

// createBom creates a BoM and answers it.
func createBom(t *testing.T, c *modtest.Client, in gen.ManufacturingBomInput) gen.ManufacturingBom {
	t.Helper()
	res := c.Do(http.MethodPost, bomsPath, in)
	mustStatus(t, res, http.StatusCreated, "create BoM")
	return bomOf(t, res)
}

func bomOf(t *testing.T, res *modtest.Response) gen.ManufacturingBom {
	t.Helper()
	var b gen.ManufacturingBom
	res.JSON(&b)
	return b
}

// getBom reads a BoM.
func getBom(t *testing.T, c *modtest.Client, id int64) gen.ManufacturingBom {
	t.Helper()
	res := c.Do(http.MethodGet, bomPath(id), nil)
	mustStatus(t, res, http.StatusOK, "get BoM")
	return bomOf(t, res)
}

// revisionInput is the revision a lifecycle write sends.
func revisionInput(r int32) gen.ManufacturingRevisionInput {
	return gen.ManufacturingRevisionInput{Revision: r}
}

// updateOf is the update that sends b back as it is.
func updateOf(b gen.ManufacturingBom) gen.ManufacturingBomUpdate {
	u := gen.ManufacturingBomUpdate{Code: b.Code, OutputQuantity: b.OutputQuantity, Revision: b.Revision,
		Operations: []gen.ManufacturingBomOperationInput{}, Lines: []gen.ManufacturingBomLineInput{}}
	for _, o := range b.Operations {
		id := o.Id
		depends := slices.Clone(o.DependsOn)
		u.Operations = append(u.Operations, gen.ManufacturingBomOperationInput{Id: &id, Key: o.Key, Sequence: o.Sequence,
			Name: o.Name, WorkCenterId: o.WorkCenterId, WorkCenterGroupId: o.WorkCenterGroupId, MinutesPerCycle: o.MinutesPerCycle,
			SetupMinutes: o.SetupMinutes, SendAheadQuantity: o.SendAheadQuantity, DependsOn: &depends})
	}
	for _, l := range b.Lines {
		scrap := l.ScrapPercent
		u.Lines = append(u.Lines, gen.ManufacturingBomLineInput{VariantId: l.Variant.Id, Quantity: l.Quantity,
			ScrapPercent: &scrap, OperationKey: l.OperationKey})
	}
	return u
}
