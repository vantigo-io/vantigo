package manufacturing_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// ledgerState is the fake's Inventory, in memory: the sites' locations, what
// each holds of a variant, the reservations each document line holds, and
// the productions posted. It keeps the rules a caller relies on — a refused
// reserve writes nothing, a production consumes its line's reservation and is
// idempotent by its document — but not a transaction's rollback: a test reads
// Manufacturing's rows for that, never the fake's.
type ledgerState struct {
	locations map[int64]fakeLocation
	onHand    map[spot]*big.Rat
	primary   map[spot]bool
	reserved  map[lineKey]*big.Rat
	variantOf map[lineKey]int32
	posted    map[contracts.StockSource]postedProduction
	movements int64
	// produceHook, when set, runs inside Produce on the caller's
	// transaction before anything else and may refuse: a test's window on
	// a refusal after the engine has written.
	produceHook func(ctx context.Context, tx pgx.Tx, p contracts.StockProduction) error
	// reserveHook, when set, runs once at the start of the next
	// ReserveForProduction, on the caller's transaction: a test's window
	// inside a transaction that makes orders.
	reserveHook func()
	produced    []contracts.StockProduction
}

type fakeLocation struct {
	site   int64
	code   string
	active bool
	root   bool
}

type spot struct {
	variant  int32
	location int64
}

type lineKey struct {
	src  contracts.StockSource
	line int64
	site int64
}

type postedProduction struct {
	movement int64
	body     string
}

// The seeded locations: each site's root, two places at the main site and an
// archived one.
const (
	locMainRoot   int64 = 10
	locMainA1     int64 = 11
	locMainA2     int64 = 12
	locMainOld    int64 = 13
	locBranchRoot int64 = 20
)

func newLedgerState() ledgerState {
	return ledgerState{
		locations: map[int64]fakeLocation{
			locMainRoot:   {site: siteMain, code: "MAIN", active: true, root: true},
			locMainA1:     {site: siteMain, code: "MAIN-A-01", active: true},
			locMainA2:     {site: siteMain, code: "MAIN-A-02", active: true},
			locMainOld:    {site: siteMain, code: "MAIN-OLD", active: false},
			locBranchRoot: {site: siteBranch, code: "BRANCH", active: true, root: true},
		},
		onHand: map[spot]*big.Rat{}, primary: map[spot]bool{}, reserved: map[lineKey]*big.Rat{},
		variantOf: map[lineKey]int32{}, posted: map[contracts.StockSource]postedProduction{}, movements: 7000,
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
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func orZero(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return r
}

// put sets what a location holds of a variant.
func (s *fakeStock) put(variant int32, location int64, qty string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onHand[spot{variant, location}] = rat(qty)
}

// setPrimary makes location the variant's primary location.
func (s *fakeStock) setPrimary(variant int32, location int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.primary[spot{variant, location}] = true
}

// held is what a location holds of a variant.
func (s *fakeStock) held(variant int32, location int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ratText(orZero(s.onHand[spot{variant, location}]))
}

// reservedFor is what an order's line holds reserved, at any site.
func (s *fakeStock) reservedFor(order, line int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := new(big.Rat)
	for k, q := range s.reserved {
		if k.src.Kind == "order" && k.src.ID == order && k.line == line {
			total.Add(total, q)
		}
	}
	return ratText(total)
}

// productions is every production posted, in order.
func (s *fakeStock) productions() []contracts.StockProduction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.produced)
}

// onReserve installs the reserve hook, run once.
func (s *fakeStock) onReserve(hook func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserveHook = hook
}

// onProduce installs the produce hook.
func (s *fakeStock) onProduce(hook func(ctx context.Context, tx pgx.Tx, p contracts.StockProduction) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.produceHook = hook
}

// free is a variant's on hand at a site less what is reserved there; the
// caller holds mu.
func (s *fakeStock) free(variant int32, site int64) *big.Rat {
	out := new(big.Rat)
	for k, q := range s.onHand {
		if k.variant == variant && s.locations[k.location].site == site {
			out.Add(out, q)
		}
	}
	for k, q := range s.reserved {
		if k.site == site && s.variantOf[k] == variant {
			out.Sub(out, q)
		}
	}
	return out
}

func (s *fakeStock) locationsHolding(ids []int32, site int64) []contracts.StockLocation {
	var out []contracts.StockLocation
	for k, q := range s.onHand {
		loc := s.locations[k.location]
		if loc.site == site && loc.active && slices.Contains(ids, k.variant) && q.Sign() > 0 {
			out = append(out, contracts.StockLocation{VariantID: k.variant, LocationID: k.location, FullCode: loc.code,
				Quantity: ratText(q), Primary: s.primary[k]})
		}
	}
	slices.SortFunc(out, func(a, b contracts.StockLocation) int {
		return cmp.Or(cmp.Compare(a.VariantID, b.VariantID), cmp.Compare(a.FullCode, b.FullCode))
	})
	return out
}

func (s *fakeStock) siteLocations(site int64) []contracts.StockSiteLocation {
	var out []contracts.StockSiteLocation
	for id, l := range s.locations {
		if l.site != site {
			continue
		}
		loc := contracts.StockSiteLocation{ID: id, Kind: "bin", FullCode: l.code, Active: l.active, Root: l.root}
		if !l.root {
			for rid, r := range s.locations {
				if r.root && r.site == site {
					parent := rid
					loc.ParentID = &parent
				}
			}
		}
		out = append(out, loc)
	}
	slices.SortFunc(out, func(a, b contracts.StockSiteLocation) int { return cmp.Compare(a.FullCode, b.FullCode) })
	return out
}

func (s *fakeStock) site(id int64) (contracts.StockSite, bool) {
	for _, site := range s.sites {
		if site.ID == id {
			return site, true
		}
	}
	return contracts.StockSite{}, false
}

var errNotManufacturing = errors.New("fake ledger: manufacturing never calls this")

func (s *fakeStock) Reserve(context.Context, pgx.Tx, contracts.StockSource, int64, []contracts.StockLine) error {
	return errNotManufacturing
}

func (s *fakeStock) Issue(context.Context, pgx.Tx, contracts.StockSource, contracts.StockIssue) (int64, error) {
	return 0, errNotManufacturing
}

// ReserveForProduction reserves all the lines or none: a variant whose lines
// together want more than the site's free is refused, naming each of them.
func (s *fakeStock) ReserveForProduction(_ context.Context, tx pgx.Tx, src contracts.StockSource, siteID int64, lines []contracts.StockLine) error {
	if tx == nil {
		panic("ReserveForProduction without a transaction")
	}
	s.mu.Lock()
	hook := s.reserveHook
	s.reserveHook = nil
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	site, ok := s.site(siteID)
	if !ok {
		return &contracts.StockRefusal{Code: contracts.StockSiteUnknown, Detail: "No such site."}
	}
	if !site.Active {
		return &contracts.StockRefusal{Code: contracts.StockSiteArchived, Detail: "The site is archived."}
	}
	want := map[int32]*big.Rat{}
	for _, l := range lines {
		want[l.VariantID] = new(big.Rat).Add(orZero(want[l.VariantID]), rat(l.Quantity))
	}
	var refused []contracts.StockLineRefusal
	for _, l := range lines {
		free := s.free(l.VariantID, siteID)
		if q, ok := s.available[siteID][l.VariantID]; ok {
			free.Add(free, rat(q))
		}
		if want[l.VariantID].Cmp(free) > 0 {
			refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockInsufficientAvailable, VariantID: l.VariantID,
				LineID: l.LineID, Detail: ratText(free) + " available at the site."})
		}
	}
	if len(refused) > 0 {
		return &contracts.StockRefusal{Code: refused[0].Code, VariantID: refused[0].VariantID, LineID: refused[0].LineID,
			Detail: refused[0].Detail, Lines: refused}
	}
	for _, l := range lines {
		k := lineKey{src, l.LineID, siteID}
		s.reserved[k] = new(big.Rat).Add(orZero(s.reserved[k]), rat(l.Quantity))
		s.variantOf[k] = l.VariantID
	}
	return nil
}

func (s *fakeStock) Release(_ context.Context, _ pgx.Tx, src contracts.StockSource, releases []contracts.StockRelease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, q := range s.reserved {
		if k.src != src {
			continue
		}
		if len(releases) == 0 {
			delete(s.reserved, k)
			continue
		}
		for _, r := range releases {
			if r.LineID != k.line {
				continue
			}
			if r.Quantity == "" {
				delete(s.reserved, k)
				continue
			}
			left := new(big.Rat).Sub(q, rat(r.Quantity))
			if left.Sign() <= 0 {
				delete(s.reserved, k)
			} else {
				s.reserved[k] = left
			}
		}
	}
	return nil
}

func (s *fakeStock) ReservationsFor(_ context.Context, _ pgx.Tx, src contracts.StockSource) ([]contracts.Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contracts.Reservation
	for k, q := range s.reserved {
		if k.src == src && q.Sign() > 0 {
			out = append(out, contracts.Reservation{VariantID: s.variantOf[k], LineID: k.line, SiteID: k.site, Quantity: ratText(q)})
		}
	}
	slices.SortFunc(out, func(a, b contracts.Reservation) int {
		return cmp.Or(cmp.Compare(a.LineID, b.LineID), cmp.Compare(a.SiteID, b.SiteID))
	})
	return out, nil
}

// Produce posts a production: each consume line off its location (refused
// when the location does not hold it, naming every failing line), consuming
// the line's reservation, each produce line onto its location; idempotent by
// the document.
func (s *fakeStock) Produce(ctx context.Context, tx pgx.Tx, src contracts.StockSource, p contracts.StockProduction) (int64, error) {
	if tx == nil {
		panic("Produce without a transaction")
	}
	s.mu.Lock()
	hook := s.produceHook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, tx, p); err != nil {
			return 0, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	body := fmt.Sprint(p.Consume, p.Produce)
	if prior, ok := s.posted[p.Document]; ok {
		if prior.body != body {
			return 0, &contracts.StockRefusal{Code: contracts.StockDocumentReused, Detail: "Posted before with another body."}
		}
		return prior.movement, nil
	}
	if len(p.Consume) == 0 && len(p.Produce) == 0 {
		return 0, &contracts.StockRefusal{Code: contracts.StockNothingToPost, Detail: "No line."}
	}
	take := map[spot]*big.Rat{}
	for _, l := range p.Consume {
		k := spot{l.VariantID, l.FromLocationID}
		take[k] = new(big.Rat).Add(orZero(take[k]), rat(l.Quantity))
	}
	var refused []contracts.StockLineRefusal
	for _, l := range p.Consume {
		k := spot{l.VariantID, l.FromLocationID}
		if loc := s.locations[l.FromLocationID]; loc.site != p.SiteID || !loc.active {
			refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockLocationUnknown, VariantID: l.VariantID, LineID: l.LineID, Detail: "No such location."})
			continue
		}
		if take[k].Cmp(orZero(s.onHand[k])) > 0 {
			refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockInsufficientStock, VariantID: l.VariantID, LineID: l.LineID,
				Detail: ratText(orZero(s.onHand[k])) + " at the location."})
		}
	}
	if len(refused) > 0 {
		return 0, &contracts.StockRefusal{Code: refused[0].Code, VariantID: refused[0].VariantID, LineID: refused[0].LineID,
			Detail: refused[0].Detail, Lines: refused}
	}
	for _, l := range p.Consume {
		k := spot{l.VariantID, l.FromLocationID}
		s.onHand[k] = new(big.Rat).Sub(orZero(s.onHand[k]), rat(l.Quantity))
		r := lineKey{src, l.LineID, p.SiteID}
		if held := s.reserved[r]; held != nil {
			left := new(big.Rat).Sub(held, rat(l.Quantity))
			if left.Sign() <= 0 {
				delete(s.reserved, r)
			} else {
				s.reserved[r] = left
			}
		}
	}
	for _, l := range p.Produce {
		k := spot{l.VariantID, l.ToLocationID}
		s.onHand[k] = new(big.Rat).Add(orZero(s.onHand[k]), rat(l.Quantity))
	}
	s.movements++
	s.posted[p.Document] = postedProduction{movement: s.movements, body: body}
	s.produced = append(s.produced, p)
	return s.movements, nil
}
