package orders_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/shipping/bring/bringtest"
)

// harness is one orders installation for one test: internal/modtest's shared
// harness composing this module beside identity, every exchange validated
// against orders.yaml through the package recorder, and fakes of the catalog,
// the sales tax, the customer directory and the object store in place of
// Products, Invoices and Customers — which depguard keeps out of this
// package. modtest lists products, customers and invoices in MODULES whenever
// orders is composed, so config's rule holds.
type harness struct {
	*modtest.Harness
	catalog *fakeCatalog
	tax     *fakeSalesTax
	dir     *fakeDirectory
	objects *fakeObjectStore
	mail    *modtest.FakeOutboundMail
	channel uuid.UUID
	// stock is Inventory's fake stock read and ledger, when composed.
	stock *fakeStock
	// bring is the httptest Bring the carrier adapter calls, when composed.
	bring *bringtest.Server
	// onSend, when set, runs as each mail is handed to the provider, before
	// it is taken.
	onSend func()
}

// hookedMail is the fake provider with the harness's onSend before each send.
type hookedMail struct {
	*modtest.FakeOutboundMail
	h *harness
}

func (m hookedMail) Send(ctx context.Context, id uuid.UUID, out mail.Outbound) error {
	if m.h.onSend != nil {
		m.h.onSend()
	}
	return m.FakeOutboundMail.Send(ctx, id, out)
}

// harnessOptions say what a harness composes beyond the defaults.
type harnessOptions struct {
	// communications composes a fake contracts.OutboundMail with one
	// available channel.
	communications bool
	// stock composes a fake contracts.Stock and contracts.StockLedger:
	// Inventory on. stockOff composes the ledger alone: Inventory switched
	// off after reservations were made (O45).
	stock, stockOff bool
	// bring composes an httptest Bring (bringtest) as the carrier the
	// shipping adapter calls (SHIPPING_BRING_BASE_URL, Deps.HTTPTransport).
	bring bool
	// production composes Manufacturing's production read (manufacturing
	// design M46).
	production contracts.Production
	// kits composes Manufacturing's kit structure (manufacturing phase 2
	// M145); nil leaves the slot empty, Manufacturing off.
	kits contracts.KitStructure
}

// newHarness is an orders installation with the seeded catalog, VAT codes
// and customers. Every harness carries the module's lock rule out of its
// test: no call out of the module's own database is made from inside one of
// its transactions, but LockVariants.
func newHarness(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	h := &harness{catalog: newFakeCatalog(), tax: newFakeSalesTax(), dir: newFakeDirectory(), objects: newFakeObjectStore()}
	opts := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(orders.Module()),
		modtest.WithProducts(h.catalog),
		modtest.WithSalesTax(h.tax),
		modtest.WithDirectory(h.dir),
		modtest.WithObjectStore(h.objects),
	}
	if o.communications {
		h.mail = modtest.NewFakeOutboundMail()
		h.channel = uuid.New()
		h.mail.SetChannels(contracts.OutboundChannel{ID: h.channel, Address: "salg@demo.example", Provider: "smtp", Available: true})
		opts = append(opts, modtest.WithOutboundMail(hookedMail{FakeOutboundMail: h.mail, h: h}))
	}
	switch {
	case o.stock:
		h.stock = newFakeStock()
		opts = append(opts, modtest.WithStock(h.stock, h.stock))
	case o.stockOff:
		h.stock = newFakeStock()
		opts = append(opts, modtest.WithStock(nil, h.stock))
	}
	if o.production != nil {
		opts = append(opts, modtest.WithProduction(o.production))
	}
	if o.kits != nil {
		opts = append(opts, modtest.WithKitStructure(o.kits))
	}
	if o.bring {
		h.bring = bringtest.New(t)
		opts = append(opts, modtest.WithEnv("SHIPPING_BRING_BASE_URL", h.bring.URL()), modtest.WithTransport(h.bring.Transport()))
	}
	before := lockedContractCalls.count()
	h.Harness = modtest.New(t, opts...)
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	seed(h)
	return h
}

// The seeded catalog: tax categories, VAT codes and variants.
const (
	catStandard int32 = 10 // → code 3, S 25 %
	catFood     int32 = 20 // → code 31, S 15 %
	catUnmapped int32 = 30 // mapped to no code

	catHardware int32 = 5 // the bolt's product category

	codeStandard int32 = 3
	codeFood     int32 = 31
	codeZero     int32 = 6

	varBolt     int32 = 101 // NOK 100, standard VAT, a case of 15
	varFood     int32 = 102 // NOK 33.335, food VAT
	varUnmapped int32 = 103 // NOK 10, its category mapped to no code
	varOld      int32 = 104 // discontinued
	varNoPrice  int32 = 105 // no list price at all

	packCase int32 = 7

	custBusiness int32 = 501
	custPerson   int32 = 502
	custDisabled int32 = 503

	addrWarehouse int32 = 11
	addrShop      int32 = 12
	addrVisiting  int32 = 13
	contactKari   int32 = 21
)

func seed(h *harness) {
	h.tax.codes = []contracts.VATCodeEntry{
		{ID: codeStandard, Code: "3", Name: "Utgående mva, høy sats", RatePercent: "25.00", Category: "S", SAFT: "3"},
		{ID: codeFood, Code: "31", Name: "Utgående mva, middels sats", RatePercent: "15.00", Category: "S", SAFT: "31"},
		{ID: codeZero, Code: "6", Name: "Fritatt", RatePercent: "0.00", Category: "Z", SAFT: "6"},
	}
	h.tax.mapCategory(catStandard, codeStandard)
	h.tax.mapCategory(catFood, codeFood)
	variant := func(id int32, sku, name string, category int32) contracts.VariantEntry {
		return contracts.VariantEntry{ID: id, ProductID: id, ProductName: name, SKU: sku, Unit: "pcs", ProductType: "Goods",
			ProductStatus: "Active", TaxCategoryID: category}
	}
	bolt := variant(varBolt, "BOLT-M8", "Bolt M8", catStandard)
	bolt.Description = "Galvanised"
	bolt.CategoryID, bolt.CategoryName = i32(catHardware), "Hardware"
	bolt.Packagings = []contracts.PackagingEntry{{ID: packCase, Name: "Case", Kind: "case", Quantity: "15.000"}}
	old := variant(varOld, "OLD-1", "Old thing", catStandard)
	old.ProductStatus = "Discontinued"
	h.catalog.seed(bolt, variant(varFood, "FOOD-1", "Coffee", catFood), variant(varUnmapped, "ODD-1", "Odd thing", catUnmapped),
		old, variant(varNoPrice, "NEW-1", "New thing", catStandard))
	h.catalog.setPrice(varBolt, "NOK", "100.00")
	h.catalog.setPrice(varFood, "NOK", "33.335")
	h.catalog.setPrice(varUnmapped, "NOK", "10")
	h.catalog.setPrice(varOld, "NOK", "5")
	h.catalog.setPrice(varBolt, "EUR", "9.50")

	terms := int32(30)
	h.dir.seed(contracts.CustomerBillingProfile{
		ID: custBusiness, CustomerNumber: 10001, Name: "Acme AS", Type: "business", Status: "active", LegalCountry: "no",
		LegalID: "987654321", InvoiceAddress: &contracts.CustomerAddressEntry{Line1: "Kundeveien 2", PostalCode: "0150", City: "Oslo", Country: "no"},
		InvoiceEmail: "faktura@acme.example", PaymentTermsDays: &terms, Currency: "NOK", Language: "nb", BuyerReference: "PO-77",
		DeliveryAddresses: []contracts.CustomerAddressEntry{
			{ID: addrWarehouse, Type: "delivery", Label: "Lager", Line1: "Lagerveien 5", PostalCode: "2000", City: "Lillestrøm", Country: "no"},
			{ID: addrShop, Type: "delivery", Label: "Butikk", Line1: "Butikkgata 1", PostalCode: "0151", City: "Oslo", Country: "no"},
			{ID: addrVisiting, Type: "visiting", Label: "Besøk", Line1: "Besøksveien 3", PostalCode: "0152", City: "Oslo", Country: "no"},
		},
		Contacts: []contracts.CustomerContactEntry{{ID: contactKari, Name: "Kari Nordmann", Email: "kari@acme.example", Phone: "+4712345678"}},
	})
	h.dir.seed(contracts.CustomerBillingProfile{
		ID: custPerson, CustomerNumber: 10002, Name: "Ola Nordmann", Type: "person", Status: "active",
		InvoiceAddress: &contracts.CustomerAddressEntry{Line1: "Hjemme 1", PostalCode: "5003", City: "Bergen", Country: "no"},
		InvoiceEmail:   "ola@example.com", Language: "en",
	})
	h.dir.seed(contracts.CustomerBillingProfile{ID: custDisabled, CustomerNumber: 10003, Name: "Gone AS", Type: "business", Status: "disabled"})
}

// The permission sets a test signs in with (§8).
var (
	reader      = []string{"orders:access"}
	quoter      = []string{"orders:access", "orders:quotes-manage"}
	confirmer   = []string{"orders:access", "orders:quotes-manage", "orders:sales-confirm"}
	settingsKey = []string{"orders:access", "orders:settings"}
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
	if !orders.InOpenTx(ctx) || method == orders.LockVariantsCall {
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
	quotesPath    = "/api/v1/orders/quotes"
	templatesPath = "/api/v1/orders/templates"
	settingsPath  = "/api/v1/orders/settings"
)

func quotePath(id int64, rest ...string) string {
	p := fmt.Sprintf("%s/%d", quotesPath, id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

func templatePath(id int64) string { return fmt.Sprintf("%s/%d", templatesPath, id) }

// quoteAt and orderAt are a quote's and an order's updatedAt as c reads it
// now: the concurrency token a write sends back (expectedUpdatedAt). A
// document c cannot read answers the zero time.
func quoteAt(t *testing.T, c *modtest.Client, id int64) time.Time {
	t.Helper()
	res := c.Do(http.MethodGet, quotePath(id), nil)
	if res.Status != http.StatusOK {
		return time.Time{}
	}
	return quoteOf(t, res).UpdatedAt
}

func orderAt(t *testing.T, c *modtest.Client, id int64) time.Time {
	t.Helper()
	res := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/orders/orders/%d", id), nil)
	if res.Status != http.StatusOK {
		return time.Time{}
	}
	var o gen.OrdersOrder
	res.JSON(&o)
	return o.UpdatedAt
}

// withUpdatedAt is a delete's path with its expectedUpdatedAt.
func withUpdatedAt(path string, at time.Time) string {
	return path + "?expectedUpdatedAt=" + url.QueryEscape(at.Format(time.RFC3339Nano))
}

// mustStatus fails the test unless res answered want.
func mustStatus(t *testing.T, res *modtest.Response, want int, what string) {
	t.Helper()
	if res.Status != want {
		t.Fatalf("%s = %d %s, want %d", what, res.Status, res.Body, want)
	}
}

// expectRefusal fails the test unless res is a problem with status and code.
func expectRefusal(t *testing.T, res *modtest.Response, status int, code, what string) gen.OrdersProblem {
	t.Helper()
	mustStatus(t, res, status, what)
	var p gen.OrdersProblem
	res.JSON(&p)
	if code != "" && (p.Code == nil || *p.Code != code) {
		t.Fatalf("%s: code %v, want %s (%s)", what, p.Code, code, res.Body)
	}
	return p
}

// quoteOf decodes a quote.
func quoteOf(t *testing.T, res *modtest.Response) gen.OrdersQuote {
	t.Helper()
	var q gen.OrdersQuote
	res.JSON(&q)
	return q
}

// line is a line input.
func line(kind gen.OrdersLineInputKind) gen.OrdersLineInput { return gen.OrdersLineInput{Kind: kind} }

// productLine is a catalog line of qty.
func productLine(variant int32, qty float64) gen.OrdersLineInput {
	l := line(gen.OrdersLineInputKindProduct)
	l.VariantId, l.Quantity = &variant, &qty
	return l
}

func f64(v float64) *float64 { return &v }
func str(v string) *string   { return &v }
func i32(v int32) *int32     { return &v }

// createQuote creates a quote for customer with lines and answers it.
func createQuote(t *testing.T, c *modtest.Client, customer int32, lines ...gen.OrdersLineInput) gen.OrdersQuote {
	t.Helper()
	if lines == nil {
		lines = []gen.OrdersLineInput{}
	}
	res := c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: customer, Lines: &lines})
	mustStatus(t, res, http.StatusCreated, "create quote")
	return quoteOf(t, res)
}

// markSent marks a draft quote as sent and answers it.
func markSent(t *testing.T, c *modtest.Client, q gen.OrdersQuote) gen.OrdersQuote {
	t.Helper()
	res := c.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: q.Revision})
	mustStatus(t, res, http.StatusOK, "mark sent")
	return quoteOf(t, res)
}

// lineByVariant is the quote's line of a variant.
func lineByVariant(t *testing.T, q gen.OrdersQuote, variant int32) gen.OrdersLine {
	t.Helper()
	for _, l := range q.Lines {
		if l.VariantId != nil && *l.VariantId == variant {
			return l
		}
	}
	t.Fatalf("quote %d has no line of variant %d", q.Id, variant)
	return gen.OrdersLine{}
}

// eventsNamed counts the outbox's events of a name about an aggregate id.
func eventsNamed(t *testing.T, h *harness, name string, id int64) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM platform.events WHERE name = $1 AND aggregate_id = $2`, name, fmt.Sprint(id))
}
