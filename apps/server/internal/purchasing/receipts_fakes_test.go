package purchasing_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The site's locations the fake stock answers: the main site's own node,
// two shelves and an archived one.
const (
	locRoot     int64 = 1100
	locShelfA   int64 = 1101
	locShelfB   int64 = 1102
	locArchived int64 = 1103
)

func (f *fakeStock) SiteLocations(ctx context.Context, siteID int64) ([]contracts.StockSiteLocation, error) {
	lockedContractCalls.note(ctx, "fake Stock.SiteLocations")
	if siteID != siteMain {
		return nil, nil
	}
	root := locRoot
	return []contracts.StockSiteLocation{
		{ID: locRoot, Kind: "site", FullCode: "HOVED", Active: true, Root: true},
		{ID: locShelfA, ParentID: &root, Kind: "shelf", FullCode: "HOVED-A1", Active: true},
		{ID: locShelfB, ParentID: &root, Kind: "shelf", FullCode: "HOVED-B1", Active: true},
		{ID: locArchived, ParentID: &root, Kind: "shelf", FullCode: "HOVED-X9", Active: false},
	}, nil
}

func (f *fakeStock) Locations(ctx context.Context, variantIDs []int32, siteID int64) ([]contracts.StockLocation, error) {
	lockedContractCalls.note(ctx, "fake Stock.Locations")
	var out []contracts.StockLocation
	for _, h := range f.holding {
		if slices.Contains(variantIDs, h.VariantID) {
			out = append(out, h)
		}
	}
	return out, nil
}

// fakeLedger is contracts.StockLedger's Receive and ReverseReceipt, in place
// of Inventory, which depguard keeps out of this package (the real ledger is
// exercised by internal/integration's purchasing tests). It records every
// call, answers a receipt's replay as Inventory does, and can be told to
// fail: deadlocks the next n calls, or refuse through a function.
type fakeLedger struct {
	contracts.StockLedger
	mu        sync.Mutex
	next      int64
	receipts  []contracts.StockReceipt
	sources   []contracts.StockSource
	reversals []contracts.StockSource
	posted    map[contracts.StockSource]int64
	reversed  map[contracts.StockSource]int64
	deadlocks int
	refuse    func(r contracts.StockReceipt) error
	refuseRev func(doc contracts.StockSource) error
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{next: 7000, posted: map[contracts.StockSource]int64{}, reversed: map[contracts.StockSource]int64{}}
}

var errFakeDeadlock = &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}

func (l *fakeLedger) Receive(_ context.Context, _ pgx.Tx, src contracts.StockSource, r contracts.StockReceipt) (contracts.StockPosted, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.Today.IsZero() {
		return contracts.StockPosted{}, errors.New("fake ledger: a receipt without today")
	}
	if err := r.Document.Validate(); err != nil {
		return contracts.StockPosted{}, err
	}
	if l.deadlocks > 0 {
		l.deadlocks--
		return contracts.StockPosted{}, errFakeDeadlock
	}
	if l.refuse != nil {
		if err := l.refuse(r); err != nil {
			return contracts.StockPosted{}, err
		}
	}
	l.receipts = append(l.receipts, r)
	l.sources = append(l.sources, src)
	if id, ok := l.posted[r.Document]; ok {
		return contracts.StockPosted{MovementID: id}, nil
	}
	l.next++
	l.posted[r.Document] = l.next
	return contracts.StockPosted{MovementID: l.next}, nil
}

func (l *fakeLedger) ReverseReceipt(_ context.Context, _ pgx.Tx, doc contracts.StockSource, _ contracts.StockReversal) (contracts.StockPosted, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.posted[doc]; !ok {
		return contracts.StockPosted{}, &contracts.StockRefusal{Code: contracts.StockReceiptUnknown, Detail: "no receipt"}
	}
	if l.refuseRev != nil {
		if err := l.refuseRev(doc); err != nil {
			return contracts.StockPosted{}, err
		}
	}
	l.reversals = append(l.reversals, doc)
	if id, ok := l.reversed[doc]; ok {
		return contracts.StockPosted{MovementID: id}, nil
	}
	l.next++
	l.reversed[doc] = l.next
	return contracts.StockPosted{MovementID: l.next}, nil
}

// calls is how many receipts the ledger took, and the last one.
func (l *fakeLedger) last() (int, contracts.StockReceipt, contracts.StockSource) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.receipts) == 0 {
		return 0, contracts.StockReceipt{}, contracts.StockSource{}
	}
	return len(l.receipts), l.receipts[len(l.receipts)-1], l.sources[len(l.sources)-1]
}

func (l *fakeLedger) set(fn func(l *fakeLedger)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fn(l)
}

func (l *fakeLedger) reversalCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.reversals)
}

// day is a calendar date at midnight UTC.
func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
