package manufacturing_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

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
	// The lots (manufacturing phase 2): each lot by id, what each holds at
	// each location (also counted in onHand), and every EnsureLots asked.
	lots    map[int64]*fakeLot
	lotHeld map[lotSpot]*big.Rat
	nextLot int64
	ensured [][]contracts.NewLot
	// holds is each lot's active holds, by source, with their kind
	// (manufacturing phase 2 M64): HoldLots and ReleaseLotHolds keep them,
	// and a lot's status is derived from them.
	holds map[int64]map[contracts.StockSource]string
}

// fakeLot is a lot of the fake: its variant, number, dates and status.
type fakeLot struct {
	variant    int32
	number     string
	serial     bool
	expiry     *time.Time
	bestBefore *time.Time
	production time.Time
	received   time.Time
	status     string
}

type lotSpot struct {
	lot      int64
	location int64
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
	picks    []contracts.StockPick
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
		lots: map[int64]*fakeLot{}, lotHeld: map[lotSpot]*big.Rat{}, nextLot: 5000,
		holds: map[int64]map[contracts.StockSource]string{},
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

func (s *fakeStock) Reserve(context.Context, pgx.Tx, contracts.StockSource, int64, time.Time, []contracts.StockLine) error {
	return errNotManufacturing
}

func (s *fakeStock) Issue(context.Context, pgx.Tx, contracts.StockSource, contracts.StockIssue) (contracts.StockPosted, error) {
	return contracts.StockPosted{}, errNotManufacturing
}

// receiveLot makes a lot of the variant holding qty at location, received
// at received, with an expiry when given; it answers the lot's id.
func (s *fakeStock) receiveLot(variant int32, number string, location int64, qty string, received time.Time, expiry *time.Time) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextLot++
	id := s.nextLot
	s.lots[id] = &fakeLot{variant: variant, number: number, expiry: expiry, received: received, status: contracts.LotAvailable}
	s.lotHeld[lotSpot{id, location}] = rat(qty)
	k := spot{variant, location}
	s.onHand[k] = new(big.Rat).Add(orZero(s.onHand[k]), rat(qty))
	return id
}

// setLotStatus puts a lot on hold ("quarantine", "blocked") or frees it.
func (s *fakeStock) setLotStatus(id int64, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lots[id].status = status
}

// lotAt is what a lot holds at a location.
func (s *fakeStock) lotAt(id, location int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ratText(orZero(s.lotHeld[lotSpot{id, location}]))
}

// ensureCalls is every EnsureLots asked, in order.
func (s *fakeStock) ensureCalls() [][]contracts.NewLot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ensured)
}

// EnsureLots finds each lot by its variant and number, comparing only the
// dates stated — or refusing lot_exists for one that MustCreate — or creates
// it: Inventory's rule (M57, M67), without the catalog's offsets.
func (s *fakeStock) EnsureLots(_ context.Context, tx pgx.Tx, src contracts.StockSource, today time.Time, lots []contracts.NewLot) ([]int64, error) {
	if tx == nil {
		panic("EnsureLots without a transaction")
	}
	if today.IsZero() {
		return nil, errNoToday
	}
	if err := src.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensured = append(s.ensured, slices.Clone(lots))
	ids := make([]int64, 0, len(lots))
	var refused []contracts.StockLineRefusal
	for i, n := range lots {
		if n.ProductionDate.IsZero() {
			return nil, errors.New("fake ledger: a lot without a production date")
		}
		var found int64
		for id, l := range s.lots {
			if l.variant == n.VariantID && strings.EqualFold(l.number, n.Number) {
				found = id
			}
		}
		if found != 0 && n.MustCreate {
			refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockLotExists, VariantID: n.VariantID, Index: i, LotID: found,
				Detail: "The lot exists already."})
			ids = append(ids, found)
			continue
		}
		if found != 0 {
			l := s.lots[found]
			differs := func(stated, stored *time.Time) bool {
				return stated != nil && (stored == nil || !stated.Equal(*stored))
			}
			if differs(n.Expiry, l.expiry) || differs(n.BestBefore, l.bestBefore) {
				refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockLotDatesDiffer, VariantID: n.VariantID, Index: i, LotID: found,
					Detail: "A stated date differs from the lot's."})
			}
			ids = append(ids, found)
			continue
		}
		s.nextLot++
		s.lots[s.nextLot] = &fakeLot{variant: n.VariantID, number: n.Number, expiry: n.Expiry, bestBefore: n.BestBefore,
			production: n.ProductionDate, received: today, status: contracts.LotAvailable}
		ids = append(ids, s.nextLot)
	}
	if len(refused) > 0 {
		return nil, &contracts.StockRefusal{Code: refused[0].Code, VariantID: refused[0].VariantID, Detail: refused[0].Detail, Lines: refused}
	}
	return ids, nil
}

// lotsAt is the variant's lots holding stock at the location, FEFO; the
// caller holds mu.
func (s *fakeStock) lotsAt(variant int32, location int64) []contracts.StockLot {
	var out []contracts.StockLot
	for k, q := range s.lotHeld {
		l := s.lots[k.lot]
		if l.variant != variant || k.location != location || q.Sign() <= 0 {
			continue
		}
		out = append(out, contracts.StockLot{VariantID: variant, LotID: k.lot, Number: l.number, Serial: l.serial, Expiry: l.expiry,
			BestBefore: l.bestBefore, ReceivedAt: l.received, LocationID: k.location, FullCode: s.locations[k.location].code,
			Quantity: ratText(q), Status: l.status})
	}
	sortFEFO(out)
	return out
}

func sortFEFO(rows []contracts.StockLot) {
	slices.SortFunc(rows, func(a, b contracts.StockLot) int {
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
		return cmp.Or(a.ReceivedAt.Compare(b.ReceivedAt), cmp.Compare(a.LotID, b.LotID), cmp.Compare(a.LocationID, b.LocationID))
	})
}

// siteLots is Stock.Lots over the fake; the caller holds mu.
func (s *fakeStock) siteLots(ids []int32, site int64) []contracts.StockLot {
	var out []contracts.StockLot
	for _, v := range ids {
		var rows []contracts.StockLot
		for id, loc := range s.locations {
			if loc.site == site && loc.active {
				rows = append(rows, s.lotsAt(v, id)...)
			}
		}
		sortFEFO(rows)
		out = append(out, rows...)
	}
	return out
}

// HoldLots puts src's holds on the lots, a lot it holds already left as it
// is; an unknown lot refuses them all.
func (s *fakeStock) HoldLots(ctx context.Context, tx pgx.Tx, src contracts.StockSource, holds []contracts.LotHold) error {
	if tx == nil {
		panic("HoldLots without a transaction")
	}
	if err := src.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, h := range holds {
		if _, ok := s.lots[h.LotID]; !ok {
			return &contracts.StockRefusal{Code: contracts.StockLotUnknown, Detail: "No such lot.",
				Lines: []contracts.StockLineRefusal{{Index: i, Code: contracts.StockLotUnknown, LotID: h.LotID}}}
		}
	}
	for _, h := range holds {
		if s.holds[h.LotID] == nil {
			s.holds[h.LotID] = map[contracts.StockSource]string{}
		}
		if _, ok := s.holds[h.LotID][src]; !ok {
			s.holds[h.LotID][src] = h.Kind
		}
		s.deriveStatus(h.LotID)
	}
	return nil
}

// ReleaseLotHolds releases src's holds on the lots named, every lot when nil.
func (s *fakeStock) ReleaseLotHolds(_ context.Context, tx pgx.Tx, src contracts.StockSource, lotIDs []int64, _ contracts.LotActor) error {
	if tx == nil {
		panic("ReleaseLotHolds without a transaction")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, by := range s.holds {
		if lotIDs != nil && !slices.Contains(lotIDs, id) {
			continue
		}
		if _, ok := by[src]; ok {
			delete(by, src)
			s.deriveStatus(id)
		}
	}
	return nil
}

// deriveStatus is a lot's status from its holds: blocked while any is a
// block, quarantine while any is a quarantine.
func (s *fakeStock) deriveStatus(id int64) {
	status := contracts.LotAvailable
	for _, kind := range s.holds[id] {
		switch {
		case kind == "block":
			status = contracts.LotBlocked
		case status == contracts.LotAvailable:
			status = contracts.LotQuarantine
		}
	}
	s.lots[id].status = status
}

// hold puts a hold of another source on the lot, as a recall or the lot
// page would.
func (s *fakeStock) hold(id int64, src contracts.StockSource, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.holds[id] == nil {
		s.holds[id] = map[contracts.StockSource]string{}
	}
	s.holds[id][src] = kind
	s.deriveStatus(id)
}

// heldBy is the sources holding the lot, by kind.
func (s *fakeStock) heldBy(id int64) map[contracts.StockSource]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.holds[id])
}

// lotStatus is the lot's status.
func (s *fakeStock) lotStatus(id int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lots[id].status
}

// LotRefs answers the lots named, by id.
func (s *fakeStock) LotRefs(ctx context.Context, ids []int64) ([]contracts.LotRef, error) {
	lockedContractCalls.note(ctx, "fake Stock.LotRefs")
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contracts.LotRef
	for _, id := range ids {
		if l, ok := s.lots[id]; ok {
			out = append(out, contracts.LotRef{ID: id, VariantID: l.variant, Number: l.number, Status: l.status})
		}
	}
	return out, nil
}

// errNoToday is a ledger call without today: the caller must read the
// business day on the pool before its transaction (manufacturing phase 2
// M62, rule 24), so every reserve and production a test makes proves it did.
var errNoToday = errors.New("fake ledger: a stock call without today")

// ReserveForProduction reserves all the lines or none: a variant whose lines
// together want more than the site's free is refused, naming each of them.
func (s *fakeStock) ReserveForProduction(_ context.Context, tx pgx.Tx, src contracts.StockSource, siteID int64, today time.Time, lines []contracts.StockLine) error {
	if tx == nil {
		panic("ReserveForProduction without a transaction")
	}
	if today.IsZero() {
		return errNoToday
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

// ReservationsForSources is ReservationsFor of each source.
func (s *fakeStock) ReservationsForSources(ctx context.Context, tx pgx.Tx, srcs []contracts.StockSource) ([]contracts.SourceReservation, error) {
	var out []contracts.SourceReservation
	for _, src := range slices.Compact(slices.Clone(srcs)) {
		rs, err := s.ReservationsFor(ctx, tx, src)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			out = append(out, contracts.SourceReservation{Source: src, Reservation: r})
		}
	}
	return out, nil
}

// Produce posts a production: each consume line off its location (refused
// when the location does not hold it, naming every failing line), consuming
// the line's reservation, each produce line onto its location; idempotent by
// the document.
func (s *fakeStock) Produce(ctx context.Context, tx pgx.Tx, src contracts.StockSource, p contracts.StockProduction) (contracts.StockPosted, error) {
	movement, picks, err := s.produce(ctx, tx, src, p)
	return contracts.StockPosted{MovementID: movement, Picks: picks}, err
}

func (s *fakeStock) produce(ctx context.Context, tx pgx.Tx, src contracts.StockSource, p contracts.StockProduction) (int64, []contracts.StockPick, error) {
	if tx == nil {
		panic("Produce without a transaction")
	}
	if p.Today.IsZero() {
		return 0, nil, errNoToday
	}
	s.mu.Lock()
	hook := s.produceHook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, tx, p); err != nil {
			return 0, nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	body := fmt.Sprint(p.Consume, p.Produce)
	if prior, ok := s.posted[p.Document]; ok {
		if prior.body != body {
			return 0, nil, &contracts.StockRefusal{Code: contracts.StockDocumentReused, Detail: "Posted before with another body."}
		}
		return prior.movement, prior.picks, nil
	}
	if len(p.Consume) == 0 && len(p.Produce) == 0 {
		return 0, nil, &contracts.StockRefusal{Code: contracts.StockNothingToPost, Detail: "No line."}
	}
	take := map[spot]*big.Rat{}
	lotTake := map[lotSpot]*big.Rat{}
	for _, l := range p.Consume {
		k := spot{l.VariantID, l.FromLocationID}
		take[k] = new(big.Rat).Add(orZero(take[k]), rat(l.Quantity))
		if l.LotID != 0 {
			lk := lotSpot{l.LotID, l.FromLocationID}
			lotTake[lk] = new(big.Rat).Add(orZero(lotTake[lk]), rat(l.Quantity))
		}
	}
	var refused []contracts.StockLineRefusal
	var picks []contracts.StockPick
	for i, l := range p.Consume {
		k := spot{l.VariantID, l.FromLocationID}
		if loc := s.locations[l.FromLocationID]; loc.site != p.SiteID || !loc.active {
			refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockLocationUnknown, VariantID: l.VariantID, LineID: l.LineID, Index: i, Detail: "No such location."})
			continue
		}
		if take[k].Cmp(orZero(s.onHand[k])) > 0 {
			refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockInsufficientStock, VariantID: l.VariantID, LineID: l.LineID, Index: i,
				Detail: ratText(orZero(s.onHand[k])) + " at the location."})
			continue
		}
		if l.LotID != 0 {
			lot := s.lots[l.LotID]
			lk := lotSpot{l.LotID, l.FromLocationID}
			switch {
			case lot == nil || lot.variant != l.VariantID:
				refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockLotUnknown, VariantID: l.VariantID, LineID: l.LineID, Index: i, LotID: l.LotID, Detail: "No such lot."})
			case lot.status != contracts.LotAvailable:
				refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockLotBlocked, VariantID: l.VariantID, LineID: l.LineID, Index: i, LotID: l.LotID,
					LocationID: l.FromLocationID, Detail: "The lot is on hold."})
			case contracts.LotExpired(lot.expiry, p.Today) && (!l.AllowExpired || !p.ExpiredOverride):
				refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockLotExpired, VariantID: l.VariantID, LineID: l.LineID, Index: i, LotID: l.LotID,
					LocationID: l.FromLocationID, Detail: "The lot is expired."})
			case lotTake[lk].Cmp(orZero(s.lotHeld[lk])) > 0:
				refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockInsufficientStock, VariantID: l.VariantID, LineID: l.LineID, Index: i, LotID: l.LotID,
					LocationID: l.FromLocationID, Detail: ratText(orZero(s.lotHeld[lk])) + " of the lot at the location."})
			}
			continue
		}
		if l.AutoLot {
			left := rat(l.Quantity)
			for _, r := range s.lotsAt(l.VariantID, l.FromLocationID) {
				if left.Sign() == 0 {
					break
				}
				if r.Status != contracts.LotAvailable || contracts.LotExpired(r.Expiry, p.Today) {
					continue
				}
				q := minRatFake(rat(r.Quantity), left)
				left.Sub(left, q)
				picks = append(picks, contracts.StockPick{Index: i, LotID: r.LotID, LocationID: r.LocationID, Quantity: ratText(q)})
			}
			if left.Sign() > 0 {
				refused = append(refused, contracts.StockLineRefusal{Code: contracts.StockInsufficientStock, VariantID: l.VariantID, LineID: l.LineID, Index: i,
					LocationID: l.FromLocationID, Detail: "The usable lots at the location do not cover it."})
			}
		}
	}
	if len(refused) > 0 {
		return 0, nil, &contracts.StockRefusal{Code: refused[0].Code, VariantID: refused[0].VariantID, LineID: refused[0].LineID,
			Detail: refused[0].Detail, Lines: refused}
	}
	for _, l := range p.Consume {
		if l.LotID != 0 {
			lk := lotSpot{l.LotID, l.FromLocationID}
			s.lotHeld[lk] = new(big.Rat).Sub(orZero(s.lotHeld[lk]), rat(l.Quantity))
		}
	}
	for _, pk := range picks {
		lk := lotSpot{pk.LotID, pk.LocationID}
		s.lotHeld[lk] = new(big.Rat).Sub(orZero(s.lotHeld[lk]), rat(pk.Quantity))
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
		if l.LotID != 0 {
			lk := lotSpot{l.LotID, l.ToLocationID}
			s.lotHeld[lk] = new(big.Rat).Add(orZero(s.lotHeld[lk]), rat(l.Quantity))
		}
	}
	s.movements++
	s.posted[p.Document] = postedProduction{movement: s.movements, body: body, picks: picks}
	s.produced = append(s.produced, p)
	return s.movements, picks, nil
}

func minRatFake(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) < 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}
