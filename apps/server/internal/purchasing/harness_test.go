package purchasing_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// recorder validates every exchange of every purchasing test against
// purchasing.yaml and records which operations answered successfully;
// TestMain turns it into the coverage gate.
var recorder = contracttest.NewForModule("purchasing")

// pdfModels is every PDF model built, for the tests that read what a PDF
// says (the bytes are embedded-subset and cannot be read back).
var pdfModels = &models{}

type models struct {
	mu  sync.Mutex
	all []purchasing.PDFModel
}

func (m *models) add(p purchasing.PDFModel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.all = append(m.all, p)
}

// find is the last model whose title and subtitle are these and which every
// want accepts: tests run in parallel, each on its own database, so two print
// "Ordre 1" alike, and a test tells its own model apart with a want.
func (m *models) find(title, subtitle string, want ...func(purchasing.PDFModel) bool) (purchasing.PDFModel, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
next:
	for i := len(m.all) - 1; i >= 0; i-- {
		if m.all[i].Title != title || m.all[i].Subtitle != subtitle {
			continue
		}
		for _, ok := range want {
			if !ok(m.all[i]) {
				continue next
			}
		}
		return m.all[i], true
	}
	return purchasing.PDFModel{}, false
}

func TestMain(m *testing.M) {
	purchasing.SetContractCallHook(lockedContractCalls.note)
	purchasing.SetPDFModelHook(pdfModels.add)
	os.Exit(contracttest.RequireCoverage(m, recorder))
}

// harness is one purchasing installation for one test: modtest's harness
// composing this module beside identity, with fakes of the supplier
// register, the catalog, the sites, the seller and the object store in place
// of Accounting, Products, Inventory and Invoices — which depguard keeps out
// of this package — and, when asked, Communications' fake channel.
type harness struct {
	*modtest.Harness
	catalog *fakeCatalog
	dir     *fakeDirectory
	stock   *fakeStock
	objects *fakeObjectStore
	mail    *modtest.FakeOutboundMail
	channel uuid.UUID
}

// The seeded data.
const (
	supNorsk    int64 = 501 // NO, NOK, active, with an e-mail
	supGerman   int64 = 502 // DE, EUR
	supInactive int64 = 503
	supPerson   int64 = 504 // a sole proprietor, no e-mail

	siteMain     int64 = 11
	siteArchived int64 = 12
	siteSecond   int64 = 13

	varBolt    int32 = 101 // goods, a box of 10
	varOld     int32 = 102 // goods, discontinued
	varService int32 = 103 // a service
	varNut     int32 = 104 // goods

	packBox int32 = 7
)

type harnessOptions struct {
	communications bool
}

func newHarness(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	h := &harness{catalog: newFakeCatalog(), dir: newFakeDirectory(), objects: newFakeObjectStore(),
		stock: &fakeStock{sites: []contracts.StockSite{
			{ID: siteMain, Code: "HOVED", Name: "Hovedlager", Active: true},
			{ID: siteArchived, Code: "GML", Name: "Gammelt lager", Active: false},
			{ID: siteSecond, Code: "VEST", Name: "Vestlager", Active: true},
		}}}
	tax := &fakeSalesTax{seller: contracts.SellerProfile{LegalName: "Vantigo Demo AS", OrganisationNumber: "923609016", VATRegistered: true,
		AddressLine1: "Storgata 1", PostalCode: "0155", City: "Oslo", Country: "no", Email: "post@demo.example"}}
	opts := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(purchasing.Module()),
		modtest.WithProducts(h.catalog),
		modtest.WithSupplierDirectory(h.dir),
		modtest.WithStock(h.stock, nil),
		modtest.WithSalesTax(tax),
		modtest.WithObjectStore(h.objects),
	}
	if o.communications {
		h.mail = modtest.NewFakeOutboundMail()
		h.channel = uuid.New()
		h.mail.SetChannels(contracts.OutboundChannel{ID: h.channel, Address: "innkjop@demo.example", Provider: "smtp", Available: true})
		opts = append(opts, modtest.WithOutboundMail(h.mail))
	}
	before := lockedContractCalls.count()
	h.Harness = modtest.New(t, opts...)
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its transactions:\n%s", strings.Join(calls, "\n"))
		}
	})
	h.dir.seed(
		contracts.SupplierEntry{ID: supNorsk, Number: 2001, Name: "Norsk Stål AS", OrganisationNumber: "987654321", Country: "NO",
			AddressLine: "Stålveien 1", PostalCode: "2000", City: "Lillestrøm", Email: "ordre@stal.example", Currency: "NOK", Active: true},
		contracts.SupplierEntry{ID: supGerman, Number: 2002, Name: "Schrauben GmbH", Country: "DE", AddressLine: "Am Werk 3",
			PostalCode: "10117", City: "Berlin", Email: "order@schrauben.example", Currency: "EUR", Active: true},
		contracts.SupplierEntry{ID: supInactive, Number: 2003, Name: "Borte AS", Country: "NO", Currency: "NOK", Active: false},
		contracts.SupplierEntry{ID: supPerson, Number: 2004, Name: "Ola Snekker", Country: "NO", AddressLine: "Hjemme 1",
			PostalCode: "5003", City: "Bergen", Currency: "NOK", Active: true, IsPerson: true},
	)
	boxBarcode := "7090000000077"
	boltBarcode := "7090000000011"
	h.catalog.seed(
		contracts.VariantEntry{ID: varBolt, ProductID: 1, ProductName: "Bolt M8", SKU: "BOLT-M8", Unit: "pcs", ProductType: "Goods",
			ProductStatus: "Active", Barcode: &boltBarcode, Packagings: []contracts.PackagingEntry{{ID: packBox, Name: "Eske", Kind: "case", Quantity: "10.000", Barcode: &boxBarcode}}},
		contracts.VariantEntry{ID: varOld, ProductID: 2, ProductName: "Gammel skrue", SKU: "OLD-1", Unit: "pcs", ProductType: "Goods", ProductStatus: "Discontinued"},
		contracts.VariantEntry{ID: varService, ProductID: 3, ProductName: "Montasje", SKU: "SRV-1", Unit: "h", ProductType: "Service", ProductStatus: "Active"},
		contracts.VariantEntry{ID: varNut, ProductID: 4, ProductName: "Mutter M8", SKU: "NUT-M8", Unit: "pcs", ProductType: "Goods", ProductStatus: "Active"},
	)
	return h
}

// The permission sets a test signs in with (§8).
var (
	reader  = []string{"purchasing:access"}
	manager = []string{"purchasing:access", "purchasing:orders-manage"}
	buyer   = []string{"purchasing:access", "purchasing:orders-manage", "purchasing:orders-send", "purchasing:supplier-items-manage"}
	admin   = []string{"purchasing:access", "purchasing:settings"}
)

// lockedContractCalls is every call out of the module made from inside one of
// its transactions but the two locking reads.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if !purchasing.InOpenTx(ctx) || method == purchasing.LockVariantsCall || method == purchasing.LockSuppliersCall {
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

// The paths the tests call.
const (
	ordersPath = "/api/v1/purchasing/orders"
	itemsPath  = "/api/v1/purchasing/supplier-items"
)

func orderPath(id int64, rest ...string) string {
	p := fmt.Sprintf("%s/%d", ordersPath, id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

func mustStatus(t *testing.T, res *modtest.Response, want int, what string) {
	t.Helper()
	if res.Status != want {
		t.Fatalf("%s = %d %s, want %d", what, res.Status, res.Body, want)
	}
}

// expectRefusal fails the test unless res is a problem with status and code.
func expectRefusal(t *testing.T, res *modtest.Response, status int, code, what string) gen.PurchasingProblem {
	t.Helper()
	mustStatus(t, res, status, what)
	var p gen.PurchasingProblem
	res.JSON(&p)
	if code != "" && (p.Code == nil || *p.Code != code) {
		t.Fatalf("%s: code %v, want %s (%s)", what, p.Code, code, res.Body)
	}
	return p
}

func orderOf(t *testing.T, res *modtest.Response) gen.PurchasingOrder {
	t.Helper()
	var o gen.PurchasingOrder
	res.JSON(&o)
	return o
}

func getOrder(t *testing.T, c *modtest.Client, id int64) gen.PurchasingOrder {
	t.Helper()
	res := c.Do(http.MethodGet, orderPath(id), nil)
	mustStatus(t, res, http.StatusOK, "get order")
	return orderOf(t, res)
}

func str(v string) *string { return &v }
func i32(v int32) *int32   { return &v }
func i64(v int64) *int64   { return &v }

// variantLine and textLine are line inputs.
func variantLine(v int32, qty string) gen.PurchasingLineInput {
	return gen.PurchasingLineInput{Kind: gen.PurchasingLineInputKindVariant, VariantId: &v, OrderQuantity: qty}
}

func textLine(desc, qty, price string) gen.PurchasingLineInput {
	return gen.PurchasingLineInput{Kind: gen.PurchasingLineInputKindText, Description: &desc, OrderQuantity: qty, UnitPrice: &price, Unit: str("stk")}
}

// createOrder creates a draft for supplier at the main site with lines.
func createOrder(t *testing.T, c *modtest.Client, supplier int64, lines ...gen.PurchasingLineInput) gen.PurchasingOrder {
	t.Helper()
	if lines == nil {
		lines = []gen.PurchasingLineInput{}
	}
	res := c.Do(http.MethodPost, ordersPath, gen.PurchasingOrderInput{SupplierId: supplier, SiteId: i64(siteMain), Lines: &lines})
	mustStatus(t, res, http.StatusCreated, "create order")
	return orderOf(t, res)
}

// markSent marks a draft as sent and answers it.
func markSent(t *testing.T, c *modtest.Client, o gen.PurchasingOrder) gen.PurchasingOrder {
	t.Helper()
	res := c.Do(http.MethodPost, orderPath(o.Id, "mark-sent"), gen.PurchasingMarkSentInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "mark sent")
	var r gen.PurchasingSendResult
	res.JSON(&r)
	return r.Order
}

// version is a write that carries only the version read.
func version(o gen.PurchasingOrder) gen.PurchasingVersionInput {
	return gen.PurchasingVersionInput{ExpectedUpdatedAt: o.UpdatedAt}
}

// updateOf is an order's update input as it stands.
func updateOf(o gen.PurchasingOrder) gen.PurchasingOrderUpdate {
	u := gen.PurchasingOrderUpdate{
		ExpectedUpdatedAt: o.UpdatedAt, SupplierId: o.SupplierId, SiteId: o.SiteId, Currency: o.Currency,
		Language: gen.PurchasingOrderUpdateLanguage(o.Language), OrderDate: o.OrderDate, ExpectedOn: o.ExpectedOn,
		SupplierContact: o.SupplierContact, Note: o.Note, InternalNote: o.InternalNote, Lines: []gen.PurchasingLineInput{},
	}
	if o.Buyer != nil {
		u.BuyerId = o.Buyer.Id
	}
	for _, l := range o.Lines {
		id := l.Id
		in := gen.PurchasingLineInput{Id: &id, Kind: gen.PurchasingLineInputKind(l.Kind), VariantId: l.VariantId, OrderQuantity: l.OrderQuantity,
			UnitPrice: str(l.UnitPrice), DiscountPercent: str(l.DiscountPercent), ExpectedOn: l.ExpectedOn, Description: str(l.Description)}
		u.Lines = append(u.Lines, in)
	}
	return u
}

// withUpdatedAt is a delete's path with its expectedUpdatedAt.
func withUpdatedAt(path string, at time.Time) string {
	return path + "?expectedUpdatedAt=" + url.QueryEscape(at.Format(time.RFC3339Nano))
}

// eventsNamed counts the outbox's events of a name about an order.
func eventsNamed(t *testing.T, h *harness, name string, id int64) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM platform.events WHERE name = $1 AND aggregate_id = $2`, name, fmt.Sprint(id))
}

// worker is the module's worker of name, built as worker mode builds it.
func workerNamed(t *testing.T, h *harness, name string) func() int {
	t.Helper()
	for _, w := range h.Workers(h.Deps()) {
		if w.Name() == name {
			return func() int {
				n, err := purchasing.RunCycle(context.Background(), w)
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
		}
	}
	t.Fatalf("no worker %s", name)
	return nil
}

// responseT is a harness response.
type responseT = modtest.Response

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func itoa(n int64) string { return fmt.Sprint(n) }
