package timetracking

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// priceList is a contracts.ProductCatalog answering one list price per
// currency for every variant, and recording the moment it was asked for, so
// a test can prove the rate chain prices a line at the entry's date rather
// than at the wall clock.
type priceList struct {
	mu     sync.Mutex
	prices map[string]float64
	asked  []time.Time
}

var _ contracts.ProductCatalog = (*priceList)(nil)

func (p *priceList) Variant(context.Context, int32) (*contracts.VariantEntry, error) { return nil, nil }
func (p *priceList) Variants(context.Context, []int32) ([]contracts.VariantEntry, error) {
	return nil, nil
}

func (p *priceList) ListPrice(_ context.Context, _ int32, currency string, at time.Time) (*contracts.Money, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, at)
	amount, ok := p.prices[currency]
	if !ok {
		return nil, nil
	}
	return &contracts.Money{Amount: amount, Currency: currency}, nil
}

// customerRates is a contracts.CustomerDirectory answering one billing
// profile per customer id and recording every id BillingProfile was asked
// about, so a test can prove the chain asks at most once and only when it
// reaches the customer step. fail makes every lookup an error. The other four
// methods are what time never asks.
type customerRates struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
	fail     bool
	asked    []int32
}

var _ contracts.CustomerDirectory = (*customerRates)(nil)

func (c *customerRates) Customer(context.Context, int32) (*contracts.CustomerEntry, error) {
	return nil, nil
}

func (c *customerRates) Customers(context.Context, []int32) ([]contracts.CustomerEntry, error) {
	return []contracts.CustomerEntry{}, nil
}

func (c *customerRates) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (c *customerRates) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return []contracts.ContactMatch{}, nil
}

func (c *customerRates) BillingProfile(_ context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, id)
	if c.fail {
		return nil, errors.New("customer directory unavailable")
	}
	p, ok := c.profiles[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func float(v float64) *float64 { return &v }
func text(v string) *string    { return &v }

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestResolveRates is D3's chain as a table: every step, every way a step
// can fail to answer and fall through, and the cost rate beside it.
func TestResolveRates(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	q := store.New(pool)

	withCard := uuid.New()    // bill 1100 / cost 650 NOK from January, 1200 / 700 from 15 September
	costOnly := uuid.New()    // a card with a cost rate and no bill rate
	withoutCard := uuid.New() // no rate card at all
	euroCard := uuid.New()    // bill 1000 / cost 500 EUR
	for _, row := range []struct {
		user       uuid.UUID
		from       string
		bill, cost any
		currency   string
	}{
		{withCard, "2026-01-01", 1100.0, 650.0, "NOK"},
		{withCard, "2026-09-15", 1200.0, 700.0, "NOK"},
		{costOnly, "2026-01-01", nil, 500.0, "SEK"},
		{euroCard, "2026-01-01", 1000.0, 500.0, "EUR"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO time.person_rates (user_id, valid_from, bill_rate, cost_rate, currency, created_at, updated_at)
		                             VALUES ($1, $2::date, $3::numeric, $4::numeric, $5, now(), now())`,
			row.user, row.from, row.bill, row.cost, row.currency); err != nil {
			t.Fatal(err)
		}
	}

	nok := contracts.ProjectEntry{ID: 1, Code: "P1", Currency: text("NOK"), DefaultBillRate: float(900), BillingType: "time-and-materials"}
	noDefault := contracts.ProjectEntry{ID: 2, Code: "P2", Currency: text("NOK"), BillingType: "time-and-materials"}
	noCurrency := contracts.ProjectEntry{ID: 3, Code: "P3", DefaultBillRate: float(900), BillingType: "time-and-materials"}
	euro := contracts.ProjectEntry{ID: 4, Code: "P4", Currency: text("EUR"), BillingType: "time-and-materials"}
	fixed := &contracts.BillingLineEntry{ID: 10, PricingMode: "fixed", FixedAmount: float(1500), VariantID: 7, Active: true}
	list := &contracts.BillingLineEntry{ID: 11, PricingMode: "list", VariantID: 7, Active: true}
	discount := &contracts.BillingLineEntry{ID: 12, PricingMode: "discount", DiscountPercent: float(12.5), VariantID: 7, Active: true}

	catalog := func() *priceList { return &priceList{prices: map[string]float64{"NOK": 1333.33}} }

	for name, tc := range map[string]struct {
		catalog     *priceList // nil: products disabled
		user        uuid.UUID
		billable    bool
		project     contracts.ProjectEntry
		line        *contracts.BillingLineEntry
		on          string
		wantSource  string
		wantBill    *float64
		wantBillCur *string
		wantCost    *float64
		wantCostCur *string
	}{
		"fixed line":                      {catalog(), withoutCard, true, nok, fixed, "2026-09-14", "line", float(1500), text("NOK"), nil, nil},
		"list line":                       {catalog(), withoutCard, true, nok, list, "2026-09-14", "line", float(1333.33), text("NOK"), nil, nil},
		"discount line, rounded to cents": {catalog(), withoutCard, true, nok, discount, "2026-09-14", "line", float(1166.66), text("NOK"), nil, nil},
		"list line, products disabled, falls through to the project": {nil, withoutCard, true, nok, list, "2026-09-14", "project", float(900), text("NOK"), nil, nil},
		"list line, no price in the currency, falls through":         {&priceList{prices: map[string]float64{"EUR": 1}}, withoutCard, true, nok, list, "2026-09-14", "project", float(900), text("NOK"), nil, nil},
		"fixed line, project without currency, falls through":        {catalog(), withoutCard, true, noCurrency, fixed, "2026-09-14", "none", nil, nil, nil, nil},
		"no line, project default":                                   {catalog(), withCard, true, nok, nil, "2026-09-14", "project", float(900), text("NOK"), float(650), text("NOK")},
		"default without currency is no default":                     {catalog(), withCard, true, noCurrency, nil, "2026-09-14", "person", float(1100), text("NOK"), float(650), text("NOK")},
		"person rate before the change":                              {catalog(), withCard, true, noDefault, nil, "2026-09-14", "person", float(1100), text("NOK"), float(650), text("NOK")},
		"person rate from the change":                                {catalog(), withCard, true, noDefault, nil, "2026-09-15", "person", float(1200), text("NOK"), float(700), text("NOK")},
		"person card without a bill rate":                            {catalog(), costOnly, true, noDefault, nil, "2026-09-14", "none", nil, nil, float(500), text("SEK")},
		"no card before the first one":                               {catalog(), withCard, true, noDefault, nil, "2025-12-31", "none", nil, nil, nil, nil},
		"no rate anywhere":                                           {catalog(), withoutCard, true, noDefault, nil, "2026-09-14", "none", nil, nil, nil, nil},
		"NOK card on a EUR project is no rate":                       {catalog(), withCard, true, euro, nil, "2026-09-14", "none", nil, nil, float(650), text("NOK")},
		"EUR card on a EUR project":                                  {catalog(), euroCard, true, euro, nil, "2026-09-14", "person", float(1000), text("EUR"), float(500), text("EUR")},
		"card on a currency-less project takes the card's currency":  {catalog(), euroCard, true, noCurrency, nil, "2026-09-14", "person", float(1000), text("EUR"), float(500), text("EUR")},
		"not billable, cost still resolved":                          {catalog(), withCard, false, nok, fixed, "2026-09-14", "none", nil, nil, float(650), text("NOK")},
	} {
		deps := module.Deps{}
		if tc.catalog != nil {
			deps.Products = tc.catalog
		}
		s := newServer(deps)
		got, err := s.resolveRates(ctx, q, rateRequest{
			UserID: tc.user, Billable: tc.billable, Project: tc.project, Line: tc.line, Date: date(t, tc.on),
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Source != tc.wantSource {
			t.Errorf("%s: source = %q, want %q", name, got.Source, tc.wantSource)
		}
		if !equalPtr(got.BillRate, tc.wantBill) || !equalPtr(got.BillCurrency, tc.wantBillCur) {
			t.Errorf("%s: bill = %v %v, want %v %v", name, show(got.BillRate), show(got.BillCurrency), show(tc.wantBill), show(tc.wantBillCur))
		}
		if !equalPtr(got.CostRate, tc.wantCost) || !equalPtr(got.CostCurrency, tc.wantCostCur) {
			t.Errorf("%s: cost = %v %v, want %v %v", name, show(got.CostRate), show(got.CostCurrency), show(tc.wantCost), show(tc.wantCostCur))
		}
	}
}

// TestResolveRates_PricesAListLineAtNoonOnTheEntryDate proves the list price
// is the one in force on the day worked, not on the day the entry was saved:
// the catalog is asked at noon UTC on the entry date, clear of either
// midnight a price change could sit on.
func TestResolveRates_PricesAListLineAtNoonOnTheEntryDate(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	catalog := &priceList{prices: map[string]float64{"NOK": 1600}}
	s := newServer(module.Deps{Products: catalog})

	_, err := s.resolveRates(context.Background(), store.New(pool), rateRequest{
		UserID: uuid.New(), Billable: true,
		Project: contracts.ProjectEntry{Currency: text("NOK")},
		Line:    &contracts.BillingLineEntry{PricingMode: "list", VariantID: 7, Active: true},
		Date:    date(t, "2026-03-02"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.March, 2, 12, 0, 0, 0, time.UTC)
	if len(catalog.asked) != 1 || !catalog.asked[0].Equal(want) {
		t.Errorf("catalog asked at %v, want exactly once at %v", catalog.asked, want)
	}
}

// TestResolveRates_CustomerStep is the chain's third step as a table (customers
// bill-rate design D3): the customer's default bill rate prices what neither
// the line nor the project did, under the person card's currency rule, and
// falls through to the person whenever it cannot — customers off, no customer,
// a customer the directory does not know, one without a rate, one in another
// currency. An archived customer's rate still applies: the directory resolves
// archived customers on purpose, and a project of theirs may still be worked
// on. The directory is asked at most once, and never by an entry the chain
// priced before the step (or that is not billable at all).
func TestResolveRates_CustomerStep(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	q := store.New(pool)

	nokCard := uuid.New()  // bill 1100 / cost 650 NOK
	euroCard := uuid.New() // bill 1000 / cost 500 EUR
	for _, row := range []struct {
		user       uuid.UUID
		bill, cost float64
		currency   string
	}{
		{nokCard, 1100, 650, "NOK"},
		{euroCard, 1000, 500, "EUR"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO time.person_rates (user_id, valid_from, bill_rate, cost_rate, currency, created_at, updated_at)
		                             VALUES ($1, '2026-01-01', $2::numeric, $3::numeric, $4, now(), now())`,
			row.user, row.bill, row.cost, row.currency); err != nil {
			t.Fatal(err)
		}
	}

	const (
		nokCustomer      int32 = 7  // 1250 NOK
		sekCustomer      int32 = 8  // 1400 SEK
		noRateCustomer   int32 = 9  // NOK, no rate
		archivedCustomer int32 = 10 // 1250 NOK, archived
		unknownCustomer  int32 = 99 // the directory answers (nil, nil)
	)
	directory := func() *customerRates {
		return &customerRates{profiles: map[int32]contracts.CustomerBillingProfile{
			nokCustomer:    {ID: nokCustomer, Currency: "NOK", DefaultBillRate: float(1250)},
			sekCustomer:    {ID: sekCustomer, Currency: "SEK", DefaultBillRate: float(1400)},
			noRateCustomer: {ID: noRateCustomer, Currency: "NOK"},
			// Archived customers still resolve (contracts.CustomerDirectory's own
			// rule), and a project of theirs may still be worked on: its rate applies.
			archivedCustomer: {ID: archivedCustomer, Archived: true, Currency: "NOK", DefaultBillRate: float(1250)},
		}}
	}
	project := func(currency *string, defaultRate *float64, customer *int32) contracts.ProjectEntry {
		return contracts.ProjectEntry{ID: 1, Code: "P1", BillingType: "time-and-materials", Currency: currency, DefaultBillRate: defaultRate, CustomerID: customer}
	}
	id := func(v int32) *int32 { return &v }
	fixed := &contracts.BillingLineEntry{ID: 10, PricingMode: "fixed", FixedAmount: float(1500), VariantID: 7, Active: true}

	for name, tc := range map[string]struct {
		directory   *customerRates // nil: the customers module is off
		user        uuid.UUID
		billable    bool
		project     contracts.ProjectEntry
		line        *contracts.BillingLineEntry
		wantSource  string
		wantBill    *float64
		wantBillCur *string
		wantAsked   int
	}{
		"customer default when the project has none": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(nokCustomer)),
			wantSource: "customer", wantBill: float(1250), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"a project default wins over the customer's": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), float(900), id(nokCustomer)),
			wantSource: "project", wantBill: float(900), wantBillCur: text("NOK"), wantAsked: 0,
		},
		"a line wins over the customer's": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(nokCustomer)), line: fixed,
			wantSource: "line", wantBill: float(1500), wantBillCur: text("NOK"), wantAsked: 0,
		},
		"a project default without a currency is no default, and the customer prices it": {
			directory: directory(), user: euroCard, billable: true, project: project(nil, float(900), id(nokCustomer)),
			wantSource: "customer", wantBill: float(1250), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"a currency-less project takes the customer's currency": {
			directory: directory(), user: euroCard, billable: true, project: project(nil, nil, id(sekCustomer)),
			wantSource: "customer", wantBill: float(1400), wantBillCur: text("SEK"), wantAsked: 1,
		},
		"a customer in another currency falls through to the person": {
			directory: directory(), user: euroCard, billable: true, project: project(text("EUR"), nil, id(nokCustomer)),
			wantSource: "person", wantBill: float(1000), wantBillCur: text("EUR"), wantAsked: 1,
		},
		"a customer in another currency and a card in another is no rate": {
			directory: directory(), user: nokCard, billable: true, project: project(text("EUR"), nil, id(nokCustomer)),
			wantSource: "none", wantAsked: 1,
		},
		"customers module off falls through to the person": {
			user: nokCard, billable: true, project: project(text("NOK"), nil, id(nokCustomer)),
			wantSource: "person", wantBill: float(1100), wantBillCur: text("NOK"),
		},
		"a project without a customer falls through to the person": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, nil),
			wantSource: "person", wantBill: float(1100), wantBillCur: text("NOK"), wantAsked: 0,
		},
		"a customer the directory does not know falls through to the person": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(unknownCustomer)),
			wantSource: "person", wantBill: float(1100), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"an archived customer's rate still applies": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(archivedCustomer)),
			wantSource: "customer", wantBill: float(1250), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"a customer without a rate falls through to the person": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(noRateCustomer)),
			wantSource: "person", wantBill: float(1100), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"not billable never asks": {
			directory: directory(), user: nokCard, billable: false, project: project(text("NOK"), nil, id(nokCustomer)),
			wantSource: "none", wantAsked: 0,
		},
	} {
		deps := module.Deps{}
		if tc.directory != nil {
			// Only a real directory: a nil *customerRates in the interface would
			// be a non-nil Directory, which is not what "customers off" is.
			deps.Directory = tc.directory
		}
		got, err := newServer(deps).resolveRates(ctx, q, rateRequest{
			UserID: tc.user, Billable: tc.billable, Project: tc.project, Line: tc.line, Date: date(t, "2026-09-14"),
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Source != tc.wantSource {
			t.Errorf("%s: source = %q, want %q", name, got.Source, tc.wantSource)
		}
		if !equalPtr(got.BillRate, tc.wantBill) || !equalPtr(got.BillCurrency, tc.wantBillCur) {
			t.Errorf("%s: bill = %v %v, want %v %v", name, show(got.BillRate), show(got.BillCurrency), show(tc.wantBill), show(tc.wantBillCur))
		}
		if tc.directory != nil && len(tc.directory.asked) != tc.wantAsked {
			t.Errorf("%s: directory asked %v, want %d call(s)", name, tc.directory.asked, tc.wantAsked)
		}
	}
}

// TestResolveRates_CustomerDirectoryErrorIsAnError proves a failing directory
// fails the resolve, the way a failing list price does (customers bill-rate
// design D3): a lookup silently skipped would store the person's rate on hours
// the customer's should have priced.
func TestResolveRates_CustomerDirectoryErrorIsAnError(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	customer := int32(7)
	s := newServer(module.Deps{Directory: &customerRates{fail: true}})

	_, err := s.resolveRates(context.Background(), store.New(pool), rateRequest{
		UserID: uuid.New(), Billable: true,
		Project: contracts.ProjectEntry{Currency: text("NOK"), CustomerID: &customer},
		Date:    date(t, "2026-09-14"),
	})
	if err == nil {
		t.Fatal("resolveRates with a failing directory = nil error, want the directory's error")
	}
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func show[T any](p *T) any {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// TestDiscounted is the discount arithmetic done in exact decimal and
// rounded half up to cents: in float64 the half-cent results below land a
// hair under the half and round down (101.10 at 15 % came out 85.93).
func TestDiscounted(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		list, percent, want float64
	}{
		{101.10, 15, 85.94},      // 85.935
		{100.50, 33, 67.34},      // 67.335
		{101.30, 15, 86.11},      // 86.105
		{1333.33, 12.5, 1166.66}, // 1166.66375
		{1600, 10, 1440},
		{1600, 100, 0},
		{0.01, 50, 0.01}, // 0.005
	} {
		if got := discounted(tc.list, tc.percent); got != tc.want {
			t.Errorf("discounted(%v, %v) = %v, want %v", tc.list, tc.percent, got, tc.want)
		}
	}
}

// TestResolveRates_DiscountLine_HalfCentRoundsUp is TestDiscounted through
// the chain, so the line step cannot quietly go back to float arithmetic.
func TestResolveRates_DiscountLine_HalfCentRoundsUp(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	s := newServer(module.Deps{Products: &priceList{prices: map[string]float64{"NOK": 101.10}}})

	got, err := s.resolveRates(context.Background(), store.New(pool), rateRequest{
		UserID: uuid.New(), Billable: true,
		Project: contracts.ProjectEntry{Currency: text("NOK")},
		Line:    &contracts.BillingLineEntry{PricingMode: "discount", DiscountPercent: float(15), VariantID: 7, Active: true},
		Date:    date(t, "2026-09-14"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "line" || !equalPtr(got.BillRate, float(85.94)) {
		t.Errorf("rate = %s %v, want line 85.94", got.Source, show(got.BillRate))
	}
}
