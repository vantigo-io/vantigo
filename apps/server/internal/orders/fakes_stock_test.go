package orders_test

import (
	"cmp"
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// fakeStock is Inventory's contracts.Stock and contracts.StockLedger for this
// package, in memory: sites, what each location holds of a variant, and the
// reservations each document line holds. It keeps the contract's rules a
// caller relies on — a reserve refused beyond what is available at the site
// writes nothing, an issue consumes the line's reservation and is idempotent
// by its document — but not a transaction's rollback: a test reads what a
// refused write left, never what a rolled-back one did.
type fakeStock struct {
	mu        sync.Mutex
	sites     []contracts.StockSite
	onHand    map[stockSpot]*big.Rat // per (variant, location)
	locations map[int64]stockLocation
	reserved  map[stockLineKey]*big.Rat
	variants  map[stockLineKey]int32
	issued    map[contracts.StockSource]int64
	issues    []contracts.StockIssue
	next      int64
}

type stockSpot struct {
	variant  int32
	location int64
}

type stockLocation struct {
	site int64
	code string
}

type stockLineKey struct {
	src  contracts.StockSource
	line int64
	site int64
}

// The fake's sites and locations.
const (
	siteOslo   int64 = 1
	siteBergen int64 = 2
	siteStore  int64 = 3 // a store: does not fulfil orders
	locA1      int64 = 11
	locA2      int64 = 12
	locB1      int64 = 21
)

func newFakeStock() *fakeStock {
	return &fakeStock{
		sites: []contracts.StockSite{
			{ID: siteBergen, Code: "BGO", Name: "Bergen", FulfilsOrders: true, Active: true},
			{ID: siteOslo, Code: "OSL", Name: "Oslo", FulfilsOrders: true, Active: true},
			{ID: siteStore, Code: "SHOP", Name: "Shop", FulfilsOrders: false, Active: true},
		},
		onHand:    map[stockSpot]*big.Rat{},
		locations: map[int64]stockLocation{locA1: {siteOslo, "OSL-A-01"}, locA2: {siteOslo, "OSL-A-02"}, locB1: {siteBergen, "BGO-B-01"}},
		reserved:  map[stockLineKey]*big.Rat{},
		variants:  map[stockLineKey]int32{},
		issued:    map[contracts.StockSource]int64{},
		next:      9001,
	}
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("not a number: " + s)
	}
	return r
}

func ratText(r *big.Rat) string {
	s := r.FloatString(3)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// put sets what a location holds of a variant.
func (f *fakeStock) put(variant int32, location int64, qty string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onHand[stockSpot{variant, location}] = rat(qty)
}

// held is what a location holds of a variant.
func (f *fakeStock) held(variant int32, location int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ratText(orZeroRat(f.onHand[stockSpot{variant, location}]))
}

// reservedOf is what the document's line holds reserved, at any site.
func (f *fakeStock) reservedOf(src contracts.StockSource, line int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := new(big.Rat)
	for k, q := range f.reserved {
		if k.src == src && k.line == line {
			total.Add(total, q)
		}
	}
	return ratText(total)
}

// reservedAt is what the document's line holds at a site.
func (f *fakeStock) reservedAt(src contracts.StockSource, line, site int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ratText(orZeroRat(f.reserved[stockLineKey{src, line, site}]))
}

func orZeroRat(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return r
}

// siteOnHand and siteReserved are a variant's at a site; the caller holds mu.
func (f *fakeStock) siteOnHand(variant int32, site int64) *big.Rat {
	out := new(big.Rat)
	for k, q := range f.onHand {
		if k.variant == variant && f.locations[k.location].site == site {
			out.Add(out, q)
		}
	}
	return out
}

func (f *fakeStock) siteReserved(variant int32, site int64, lines map[stockLineKey]int32) *big.Rat {
	out := new(big.Rat)
	for k, q := range f.reserved {
		if k.site == site && lines[k] == variant {
			out.Add(out, q)
		}
	}
	return out
}

func (f *fakeStock) OnHand(ctx context.Context, ids []int32, site *int64) ([]contracts.StockQuantity, error) {
	return f.read(ids, site, false), nil
}

func (f *fakeStock) Available(ctx context.Context, ids []int32, site *int64) ([]contracts.StockQuantity, error) {
	return f.read(ids, site, true), nil
}

func (f *fakeStock) read(ids []int32, site *int64, available bool) []contracts.StockQuantity {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contracts.StockQuantity, 0, len(ids))
	for _, id := range ids {
		q := contracts.StockQuantity{VariantID: id}
		total := new(big.Rat)
		for _, s := range f.sites {
			if site != nil && s.ID != *site {
				continue
			}
			v := f.siteOnHand(id, s.ID)
			if available {
				v.Sub(v, f.siteReserved(id, s.ID, f.variants))
			}
			if v.Sign() != 0 {
				q.Sites = append(q.Sites, contracts.SiteQuantity{SiteID: s.ID, Quantity: ratText(v)})
			}
			total.Add(total, v)
		}
		q.Quantity = ratText(total)
		out = append(out, q)
	}
	return out
}

func (f *fakeStock) Sites(context.Context) ([]contracts.StockSite, error) {
	return slices.Clone(f.sites), nil
}

func (f *fakeStock) Locations(ctx context.Context, ids []int32, site int64) ([]contracts.StockLocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.StockLocation
	for k, q := range f.onHand {
		loc := f.locations[k.location]
		if loc.site == site && slices.Contains(ids, k.variant) && q.Sign() > 0 {
			out = append(out, contracts.StockLocation{VariantID: k.variant, LocationID: k.location, FullCode: loc.code, Quantity: ratText(q)})
		}
	}
	slices.SortFunc(out, func(a, b contracts.StockLocation) int {
		return cmp.Or(cmp.Compare(a.VariantID, b.VariantID), cmp.Compare(a.FullCode, b.FullCode))
	})
	return out, nil
}

func (f *fakeStock) Reserve(ctx context.Context, _ pgx.Tx, src contracts.StockSource, site int64, lines []contracts.StockLine) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var s *contracts.StockSite
	for i := range f.sites {
		if f.sites[i].ID == site {
			s = &f.sites[i]
		}
	}
	if s == nil || !s.FulfilsOrders {
		return &contracts.StockRefusal{Code: contracts.StockSiteNotFulfilling, Detail: "The site does not fulfil orders."}
	}
	want := map[int32]*big.Rat{}
	for _, l := range lines {
		want[l.VariantID] = new(big.Rat).Add(orZeroRat(want[l.VariantID]), rat(l.Quantity))
	}
	var refused []contracts.StockLineRefusal
	for _, l := range lines {
		free := f.siteOnHand(l.VariantID, site)
		free.Sub(free, f.siteReserved(l.VariantID, site, f.variants))
		if want[l.VariantID].Cmp(free) > 0 {
			refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockInsufficientAvailable, VariantID: l.VariantID,
				LineID: l.LineID, Detail: fmt.Sprintf("%s available at the site.", ratText(free))})
		}
	}
	if len(refused) > 0 {
		r := refused[0]
		return &contracts.StockRefusal{Code: r.Code, VariantID: r.VariantID, LineID: r.LineID, Detail: r.Detail, Lines: refused}
	}
	for _, l := range lines {
		k := stockLineKey{src, l.LineID, site}
		f.reserved[k] = new(big.Rat).Add(orZeroRat(f.reserved[k]), rat(l.Quantity))
		f.variants[k] = l.VariantID
	}
	return nil
}

func (f *fakeStock) Release(ctx context.Context, _ pgx.Tx, src contracts.StockSource, releases []contracts.StockRelease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, q := range f.reserved {
		if k.src != src {
			continue
		}
		if releases == nil {
			delete(f.reserved, k)
			continue
		}
		for _, r := range releases {
			if r.LineID != k.line {
				continue
			}
			if r.Quantity == "" {
				delete(f.reserved, k)
				continue
			}
			left := new(big.Rat).Sub(q, rat(r.Quantity))
			if left.Sign() <= 0 {
				delete(f.reserved, k)
			} else {
				f.reserved[k] = left
			}
		}
	}
	return nil
}

func (f *fakeStock) Issue(ctx context.Context, _ pgx.Tx, src contracts.StockSource, issue contracts.StockIssue) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc := issue.Document
	if doc.IsZero() {
		doc = src
	}
	if id, ok := f.issued[doc]; ok {
		return id, nil
	}
	var refused []contracts.StockLineRefusal
	for _, l := range issue.Lines {
		loc, ok := f.locations[l.FromLocationID]
		if !ok || loc.site != issue.SiteID {
			refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockLocationUnknown, VariantID: l.VariantID, LineID: l.LineID,
				Detail: "No such location at the site."})
			continue
		}
		if rat(l.Quantity).Cmp(orZeroRat(f.onHand[stockSpot{l.VariantID, l.FromLocationID}])) > 0 {
			refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockInsufficientStock, VariantID: l.VariantID, LineID: l.LineID,
				Detail: "The location does not hold that much."})
		}
	}
	if len(refused) > 0 {
		r := refused[0]
		return 0, &contracts.StockRefusal{Code: r.Code, VariantID: r.VariantID, LineID: r.LineID, Detail: r.Detail, Lines: refused}
	}
	for _, l := range issue.Lines {
		spot := stockSpot{l.VariantID, l.FromLocationID}
		f.onHand[spot] = new(big.Rat).Sub(f.onHand[spot], rat(l.Quantity))
		k := stockLineKey{src, l.LineID, issue.SiteID}
		if q := f.reserved[k]; q != nil {
			left := new(big.Rat).Sub(q, rat(l.Quantity))
			if left.Sign() <= 0 {
				delete(f.reserved, k)
			} else {
				f.reserved[k] = left
			}
		}
	}
	f.next++
	f.issued[doc] = f.next
	f.issues = append(f.issues, issue)
	return f.next, nil
}

func (f *fakeStock) ReservationsFor(ctx context.Context, _ pgx.Tx, src contracts.StockSource) ([]contracts.Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.Reservation
	for k, q := range f.reserved {
		if k.src == src {
			out = append(out, contracts.Reservation{VariantID: f.variants[k], LineID: k.line, SiteID: k.site, Quantity: ratText(q)})
		}
	}
	slices.SortFunc(out, func(a, b contracts.Reservation) int {
		return cmp.Or(cmp.Compare(a.LineID, b.LineID), cmp.Compare(a.SiteID, b.SiteID))
	})
	return out, nil
}
