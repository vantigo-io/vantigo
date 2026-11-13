package orders_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// fakeStock is Inventory's contracts.Stock and contracts.StockLedger for this
// package, in memory: sites, what each location holds of a variant, and the
// reservations each document line holds. It keeps the contract's rules a
// caller relies on — a reserve refused beyond what is available at the site
// writes nothing, a line holds one variant, an issue consumes the line's
// reservation of the variant issued and is idempotent by its document — but not a transaction's rollback: a test reads what a
// refused write left, never what a rolled-back one did.
type fakeStock struct {
	mu        sync.Mutex
	sites     []contracts.StockSite
	onHand    map[stockSpot]*big.Rat // per (variant, location)
	locations map[int64]stockLocation
	primary   map[stockSpot]bool // the variants' primary locations (inventory phase 2 I55)
	reserved  map[stockLineKey]*big.Rat
	variants  map[stockLineKey]int32
	issued    map[contracts.StockSource]int64
	issues    []contracts.StockIssue
	next      int64
	// lots and lotHeld are the tracked variants' stock (manufacturing phase
	// 2 M67): each lot, and what each location holds of it; tracked marks
	// the variants whose issue needs a lot.
	lots    map[int64]fakeLot
	lotHeld map[lotSpotKey]*big.Rat
	tracked map[int32]bool
	// lotReads counts Stock.Lots calls.
	lotReads int
	// afterLots, when set, runs once after the next Stock.Lots read.
	afterLots func()
}

// fakeLot is a lot of a tracked variant.
type fakeLot struct {
	id       int64
	variant  int32
	number   string
	serial   bool
	expiry   *time.Time
	received time.Time
	status   string
}

type lotSpotKey struct {
	lot      int64
	location int64
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
		primary:   map[stockSpot]bool{},
		locations: map[int64]stockLocation{locA1: {siteOslo, "OSL-A-01"}, locA2: {siteOslo, "OSL-A-02"}, locB1: {siteBergen, "BGO-B-01"}},
		reserved:  map[stockLineKey]*big.Rat{},
		variants:  map[stockLineKey]int32{},
		issued:    map[contracts.StockSource]int64{},
		next:      9001,
		lots:      map[int64]fakeLot{},
		lotHeld:   map[lotSpotKey]*big.Rat{},
		tracked:   map[int32]bool{},
	}
}

// putLot sets what a location holds of a lot, making the lot (and marking
// its variant tracked) the first time: expiry nil for none, received the
// lot's FIFO date.
func (f *fakeStock) putLot(variant int32, lot int64, number string, location int64, qty string, expiry *time.Time, received time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.lots[lot]; !ok {
		f.lots[lot] = fakeLot{id: lot, variant: variant, number: number, expiry: expiry, received: received, status: contracts.LotAvailable}
	}
	f.tracked[variant] = true
	f.lotHeld[lotSpotKey{lot, location}] = rat(qty)
}

// setLotSerial marks a lot as a serial number's.
func (f *fakeStock) setLotSerial(lot int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.lots[lot]
	l.serial = true
	f.lots[lot] = l
}

// setLotStatus puts a lot on hold ("quarantine", "blocked") or frees it.
func (f *fakeStock) setLotStatus(lot int64, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.lots[lot]
	l.status = status
	f.lots[lot] = l
}

// lotAt is what a location holds of a lot.
func (f *fakeStock) lotAt(lot, location int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ratText(orZeroRat(f.lotHeld[lotSpotKey{lot, location}]))
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

// setPrimary makes location the variant's primary location at its site.
func (f *fakeStock) setPrimary(variant int32, location int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.primary[stockSpot{variant, location}] = true
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
	for k, q := range f.lotHeld {
		if f.lots[k.lot].variant == variant && f.locations[k.location].site == site {
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
			out = append(out, contracts.StockLocation{VariantID: k.variant, LocationID: k.location, FullCode: loc.code, Quantity: ratText(q),
				Primary: f.primary[k]})
		}
	}
	lotSums := map[stockSpot]*big.Rat{}
	for k, q := range f.lotHeld {
		v := f.lots[k.lot].variant
		if f.locations[k.location].site == site && slices.Contains(ids, v) && q.Sign() > 0 {
			spot := stockSpot{v, k.location}
			lotSums[spot] = new(big.Rat).Add(orZeroRat(lotSums[spot]), q)
		}
	}
	for spot, q := range lotSums {
		out = append(out, contracts.StockLocation{VariantID: spot.variant, LocationID: spot.location, FullCode: f.locations[spot.location].code,
			Quantity: ratText(q), Primary: f.primary[spot]})
	}
	slices.SortFunc(out, func(a, b contracts.StockLocation) int {
		return cmp.Or(cmp.Compare(a.VariantID, b.VariantID), cmp.Compare(a.FullCode, b.FullCode))
	})
	return out, nil
}

// SiteLocations is Manufacturing's; Orders never calls it.
func (f *fakeStock) SiteLocations(context.Context, int64) ([]contracts.StockSiteLocation, error) {
	return nil, errors.New("fake stock: orders never lists a site's locations")
}

// Placements is Manufacturing's; Orders never calls it.
func (f *fakeStock) Placements(context.Context, int64) ([]contracts.StockPlacement, error) {
	return nil, errors.New("fake stock: orders never reads placements")
}

// LotRefs is Manufacturing's; Orders never calls it.
func (f *fakeStock) LotRefs(context.Context, []int64) ([]contracts.LotRef, error) {
	return nil, errors.New("fake stock: orders never reads lots by id")
}

// Lots is every lot of the variants holding more than 0 at a location of
// the site, in FEFO order (manufacturing phase 2 M65).
func (f *fakeStock) Lots(_ context.Context, ids []int32, site int64) ([]contracts.StockLot, error) {
	f.mu.Lock()
	defer func() {
		after := f.afterLots
		f.afterLots = nil
		f.mu.Unlock()
		if after != nil {
			after()
		}
	}()
	f.lotReads++
	var out []contracts.StockLot
	for k, q := range f.lotHeld {
		l := f.lots[k.lot]
		loc := f.locations[k.location]
		if loc.site != site || !slices.Contains(ids, l.variant) || q.Sign() <= 0 {
			continue
		}
		out = append(out, contracts.StockLot{VariantID: l.variant, LotID: l.id, Number: l.number, Serial: l.serial, Expiry: l.expiry,
			ReceivedAt: l.received, LocationID: k.location, FullCode: loc.code, Quantity: ratText(q), Status: l.status})
	}
	slices.SortFunc(out, func(a, b contracts.StockLot) int {
		if c := cmp.Compare(a.VariantID, b.VariantID); c != 0 {
			return c
		}
		switch {
		case a.Expiry == nil && b.Expiry != nil:
			return 1
		case a.Expiry != nil && b.Expiry == nil:
			return -1
		case a.Expiry != nil && b.Expiry != nil:
			if c := a.Expiry.Compare(*b.Expiry); c != 0 {
				return c
			}
		}
		return cmp.Or(a.ReceivedAt.Compare(b.ReceivedAt), cmp.Compare(a.LotID, b.LotID), cmp.Compare(a.FullCode, b.FullCode))
	})
	return out, nil
}

// errNoToday is a ledger call without today: Orders reads the business day
// on the pool before its transaction (manufacturing phase 2 M62, rule 24),
// so every reserve and issue a test makes proves it did.
var errNoToday = errors.New("fake stock: a stock call without today")

func (f *fakeStock) Reserve(ctx context.Context, _ pgx.Tx, src contracts.StockSource, site int64, today time.Time, lines []contracts.StockLine) error {
	if today.IsZero() {
		return errNoToday
	}
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
	// A line holds one variant at a site, as Inventory's AddReservations.
	for _, l := range lines {
		k := stockLineKey{src, l.LineID, site}
		if v, ok := f.variants[k]; ok && f.reserved[k] != nil && v != l.VariantID {
			return fmt.Errorf("fake stock: line %d already holds variant %d", l.LineID, v)
		}
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

func (f *fakeStock) Issue(ctx context.Context, tx pgx.Tx, src contracts.StockSource, issue contracts.StockIssue) (contracts.StockPosted, error) {
	movement, err := f.issue(ctx, tx, src, issue)
	return contracts.StockPosted{MovementID: movement}, err
}

func (f *fakeStock) issue(ctx context.Context, _ pgx.Tx, src contracts.StockSource, issue contracts.StockIssue) (int64, error) {
	if issue.Today.IsZero() {
		return 0, errNoToday
	}
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
	for i, l := range issue.Lines {
		refuse := func(code, detail string) {
			refused = append(refused, contracts.StockLineRefusal{Code: code, VariantID: l.VariantID, LineID: l.LineID, Detail: detail,
				Index: i, LotID: l.LotID, LocationID: l.FromLocationID})
		}
		loc, ok := f.locations[l.FromLocationID]
		if !ok || loc.site != issue.SiteID {
			refuse(contracts.StockLocationUnknown, "No such location at the site.")
			continue
		}
		if l.LotID == 0 {
			switch {
			case f.tracked[l.VariantID]:
				refuse(contracts.StockLotRequired, "The item is tracked by lot.")
			case rat(l.Quantity).Cmp(orZeroRat(f.onHand[stockSpot{l.VariantID, l.FromLocationID}])) > 0:
				refuse(contracts.StockInsufficientStock, "The location does not hold that much.")
			}
			continue
		}
		lot, ok := f.lots[l.LotID]
		switch {
		case !ok || lot.variant != l.VariantID:
			refuse(contracts.StockLotUnknown, "No such lot of the item.")
		case lot.status != contracts.LotAvailable:
			refuse(contracts.StockLotBlocked, "The lot is on hold.")
		case contracts.LotExpired(lot.expiry, issue.Today) && (!l.AllowExpired || !issue.ExpiredOverride):
			refuse(contracts.StockLotExpired, "The lot is expired.")
		case rat(l.Quantity).Cmp(orZeroRat(f.lotHeld[lotSpotKey{l.LotID, l.FromLocationID}])) > 0:
			refuse(contracts.StockInsufficientStock, "The location does not hold that much of the lot.")
		}
	}
	if len(refused) > 0 {
		r := refused[0]
		return 0, &contracts.StockRefusal{Code: r.Code, VariantID: r.VariantID, LineID: r.LineID, Detail: r.Detail, Lines: refused}
	}
	for _, l := range issue.Lines {
		if l.LotID != 0 {
			key := lotSpotKey{l.LotID, l.FromLocationID}
			f.lotHeld[key] = new(big.Rat).Sub(f.lotHeld[key], rat(l.Quantity))
		} else {
			spot := stockSpot{l.VariantID, l.FromLocationID}
			f.onHand[spot] = new(big.Rat).Sub(f.onHand[spot], rat(l.Quantity))
		}
		k := stockLineKey{src, l.LineID, issue.SiteID}
		if q := f.reserved[k]; q != nil && f.variants[k] == l.VariantID {
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

// ReserveForProduction and Produce are Manufacturing's; Orders never calls
// them, so the fake refuses to be called.
func (f *fakeStock) ReserveForProduction(context.Context, pgx.Tx, contracts.StockSource, int64, time.Time, []contracts.StockLine) error {
	return errors.New("fake stock: orders never reserves for production")
}

func (f *fakeStock) Produce(context.Context, pgx.Tx, contracts.StockSource, contracts.StockProduction) (contracts.StockPosted, error) {
	return contracts.StockPosted{}, errors.New("fake stock: orders never produces")
}

func (f *fakeStock) EnsureLots(context.Context, pgx.Tx, contracts.StockSource, time.Time, []contracts.NewLot) ([]int64, error) {
	return nil, errors.New("fake stock: orders makes no lots")
}

func (f *fakeStock) HoldLots(context.Context, pgx.Tx, contracts.StockSource, []contracts.LotHold) error {
	return errors.New("fake stock: orders holds no lots")
}

func (f *fakeStock) ReleaseLotHolds(context.Context, pgx.Tx, contracts.StockSource, []int64, contracts.LotActor) error {
	return errors.New("fake stock: orders holds no lots")
}

// variantHeld is the variant the document's line holds reserved, 0 when it
// holds none.
func (f *fakeStock) variantHeld(src contracts.StockSource, line int64) int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, q := range f.reserved {
		if k.src == src && k.line == line && q.Sign() > 0 {
			return f.variants[k]
		}
	}
	return 0
}

// testToday is the business day a test hands the fake ledger directly.
var testToday = time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)

// lastIssue is the last issue posted, with the today and override its
// caller read before its transaction.
func (f *fakeStock) lastIssue() (contracts.StockIssue, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.issues) == 0 {
		return contracts.StockIssue{}, false
	}
	return f.issues[len(f.issues)-1], true
}

// onNextLots runs fn once after the next Stock.Lots read.
func (f *fakeStock) onNextLots(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.afterLots = fn
}

// lotReadCount is how many times Stock.Lots was read.
func (f *fakeStock) lotReadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lotReads
}
