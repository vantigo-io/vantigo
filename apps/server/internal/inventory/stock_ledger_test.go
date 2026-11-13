package inventory_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The stock contracts (orders design O45): the ledger's reserve, release and
// issue on the caller's transaction, and the read of on hand and available.

// order is the document most of these tests reserve for.
var order = contracts.StockSource{Module: "orders", Kind: "order", ID: 7}

// ledgerOf is the module's StockLedger as Compose builds it.
func ledgerOf(h *harness) contracts.StockLedger { return inventory.Module().StockLedger(h.Deps()) }

// stockOf is the module's Stock as Compose builds it.
func stockOf(h *harness) contracts.Stock { return inventory.Module().Stock(h.Deps()) }

// inTx runs fn in a transaction on the harness's pool, committing when fn
// answers nil and rolling back otherwise; it answers fn's error.
func inTx(t *testing.T, h *harness, fn func(tx pgx.Tx) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return nil
}

// refusalCode is err's StockRefusal code, "" when it is none.
func refusalCode(err error) string {
	var r *contracts.StockRefusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

// reserved is what the document holds, as line:site:quantity.
func reserved(t *testing.T, h *harness, src contracts.StockSource) []string {
	t.Helper()
	var out []string
	err := inTx(t, h, func(tx pgx.Tx) error {
		rows, err := ledgerOf(h).ReservationsFor(t.Context(), tx, src)
		for _, r := range rows {
			out = append(out, strconv.FormatInt(r.LineID, 10)+":"+strconv.FormatInt(r.SiteID, 10)+":"+r.Quantity)
		}
		return err
	})
	if err != nil {
		t.Fatalf("ReservationsFor: %v", err)
	}
	return out
}

// availableAt is the variant's available at site through contracts.Stock.
func availableAt(t *testing.T, h *harness, variant int32, site int64) string {
	t.Helper()
	q, err := stockOf(h).Available(t.Context(), []int32{variant}, &site)
	if err != nil || len(q) != 1 {
		t.Fatalf("Available = %v, %v", q, err)
	}
	return q[0].Quantity
}

func TestStockLedger_ReserveRefuseRelease(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10))
	receive(t, c, s.site.Id, s.shelf2.Id, receiptLine(varBolt, 5), receiptLine(varNut, 2.5))
	l := ledgerOf(h)
	site := s.site.Id

	// Two lines; the second asked again adds to its reservation.
	err := inTx(t, h, func(tx pgx.Tx) error {
		return l.Reserve(t.Context(), tx, order, site, testToday, []contracts.StockLine{
			{VariantID: varBolt, LineID: 11, Quantity: "12"}, {VariantID: varNut, LineID: 12, Quantity: "1.5"},
		})
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.Reserve(t.Context(), tx, order, site, testToday, []contracts.StockLine{{VariantID: varNut, LineID: 12, Quantity: "1"}})
	}); err != nil {
		t.Fatalf("Reserve again: %v", err)
	}
	want := []string{"11:" + strconv.FormatInt(site, 10) + ":12", "12:" + strconv.FormatInt(site, 10) + ":2.5"}
	if got := reserved(t, h, order); !slices.Equal(got, want) {
		t.Errorf("reservations = %v, want %v", got, want)
	}

	// Beyond available: 15 on hand, 12 reserved, 3 left. Every failing line is
	// named, and nothing is written.
	other := contracts.StockSource{Module: "orders", Kind: "order", ID: 8}
	err = inTx(t, h, func(tx pgx.Tx) error {
		return l.Reserve(t.Context(), tx, other, site, testToday, []contracts.StockLine{
			{VariantID: varBolt, LineID: 21, Quantity: "2"}, {VariantID: varBolt, LineID: 22, Quantity: "2"},
			{VariantID: varNut, LineID: 23, Quantity: "0.001"},
		})
	})
	var r *contracts.StockRefusal
	if !errors.As(err, &r) || r.Code != contracts.StockInsufficientAvailable || len(r.Lines) != 2 || r.Lines[0].LineID != 22 || r.Lines[1].LineID != 23 {
		t.Fatalf("Reserve beyond available = %+v, want lines 22 and 23 insufficient_available", err)
	}
	if got := reserved(t, h, other); got != nil {
		t.Errorf("a refused reserve wrote %v", got)
	}
	if got := availableAt(t, h, varBolt, site); got != "3" {
		t.Errorf("available bolts = %s, want 3", got)
	}

	// The batched read, in a read-only transaction: each document's
	// reservations once, a document named twice or holding none answered
	// with nothing more.
	ro, err := h.Pool().BeginTx(t.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("begin read-only: %v", err)
	}
	batch, err := l.ReservationsForSources(t.Context(), ro, []contracts.StockSource{other, order, order})
	_ = ro.Rollback(t.Context())
	if err != nil || len(batch) != 2 || batch[0].Source != order || batch[0].LineID != 11 || batch[1].LineID != 12 ||
		batch[1].Quantity != "2.5" {
		t.Errorf("ReservationsForSources = %+v, %v", batch, err)
	}

	// The read: on hand and available, in total and per site.
	onHand, err := stockOf(h).OnHand(t.Context(), []int32{varBolt, varWasher}, nil)
	if err != nil || len(onHand) != 2 || onHand[0].Quantity != "15" || len(onHand[0].Sites) != 1 || onHand[1].Quantity != "0" || len(onHand[1].Sites) != 0 {
		t.Errorf("OnHand = %+v, %v", onHand, err)
	}

	// The API shows reserved and available beside on hand.
	res := c.Do(http.MethodGet, "/api/v1/inventory/variants/"+strconv.Itoa(int(varBolt))+"/stock", nil)
	mustStatus(t, res, http.StatusOK, "variant stock")
	var vs gen.InventoryVariantStock
	res.JSON(&vs)
	if vs.TotalOnHand != 15 || vs.TotalReserved != 12 || vs.TotalAvailable != 3 || len(vs.Sites) != 1 || vs.Sites[0].Reserved != 12 || vs.Sites[0].Available != 3 {
		t.Errorf("variant stock = %+v", vs)
	}
	res = c.Do(http.MethodGet, "/api/v1/inventory/stock?variantIds="+strconv.Itoa(int(varNut)), nil)
	mustStatus(t, res, http.StatusOK, "totals")
	var totals gen.InventoryStockTotals
	res.JSON(&totals)
	if len(totals.Totals) != 1 || totals.Totals[0].OnHand != 2.5 || totals.Totals[0].Reserved != 2.5 || totals.Totals[0].Available != 0 {
		t.Errorf("totals = %+v", totals)
	}

	// Release a quantity of one line, more than a line holds (floored), then
	// everything.
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.Release(t.Context(), tx, order, []contracts.StockRelease{{LineID: 11, Quantity: "2"}, {LineID: 12, Quantity: "9"}, {LineID: 99}})
	}); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := reserved(t, h, order); !slices.Equal(got, []string{"11:" + strconv.FormatInt(site, 10) + ":10"}) {
		t.Errorf("after release = %v, want line 11 at 10", got)
	}
	if err := inTx(t, h, func(tx pgx.Tx) error { return l.Release(t.Context(), tx, order, nil) }); err != nil {
		t.Fatalf("Release all: %v", err)
	}
	if got := reserved(t, h, order); got != nil {
		t.Errorf("after release all = %v, want none", got)
	}
	if got := availableAt(t, h, varBolt, site); got != "15" {
		t.Errorf("available bolts = %s, want 15", got)
	}
}

func TestStockLedger_RefusesSitesAndVariants(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10))
	res := c.Do(http.MethodPost, sitesPath, gen.InventorySiteInput{Code: "SHOP", Name: "Shop", Kind: gen.Store})
	mustStatus(t, res, http.StatusCreated, "create a store")
	var store gen.InventorySite
	res.JSON(&store)
	if store.FulfilsOrders || !s.site.FulfilsOrders {
		t.Fatalf("fulfilsOrders: store %v, warehouse %v; want false and true", store.FulfilsOrders, s.site.FulfilsOrders)
	}
	l := ledgerOf(h)
	line := []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "1"}}
	for _, tc := range []struct {
		name string
		site int64
		line []contracts.StockLine
		want string
	}{
		{"an unknown site", 999999, line, contracts.StockSiteUnknown},
		{"a store", store.Id, line, contracts.StockSiteNotFulfilling},
		{"a service", s.site.Id, []contracts.StockLine{{VariantID: varService, LineID: 1, Quantity: "1"}}, contracts.StockVariantNotStocked},
		{"an unknown variant", s.site.Id, []contracts.StockLine{{VariantID: 999, LineID: 1, Quantity: "1"}}, contracts.StockVariantNotStocked},
	} {
		err := inTx(t, h, func(tx pgx.Tx) error { return l.Reserve(t.Context(), tx, order, tc.site, testToday, tc.line) })
		if refusalCode(err) != tc.want {
			t.Errorf("Reserve at %s = %v, want %s", tc.name, err, tc.want)
		}
	}
	// A bad quantity is the caller's bug: a failure, not a refusal.
	err := inTx(t, h, func(tx pgx.Tx) error {
		return l.Reserve(t.Context(), tx, order, s.site.Id, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "0.0001"}})
	})
	if err == nil || refusalCode(err) != "" {
		t.Errorf("Reserve 0.0001 = %v, want a failure", err)
	}

	// The flag is the site's own: turned on, the store fulfils orders.
	update := gen.InventorySiteUpdate{Code: store.Code, Name: store.Name, Kind: store.Kind, Status: gen.Active, FulfilsOrders: ptr(true)}
	res = c.Do(http.MethodPut, sitesPath+"/"+strconv.FormatInt(store.Id, 10), update, ifMatch(store.Revision))
	mustStatus(t, res, http.StatusOK, "turn fulfilsOrders on")
	res.JSON(&store)
	if !store.FulfilsOrders {
		t.Errorf("store = %+v, want it fulfilling orders", store)
	}
	// Absent, the flag is left as it is.
	update.FulfilsOrders = nil
	res = c.Do(http.MethodPut, sitesPath+"/"+strconv.FormatInt(store.Id, 10), update, ifMatch(store.Revision))
	mustStatus(t, res, http.StatusOK, "replace without the flag")
	res.JSON(&store)
	if !store.FulfilsOrders {
		t.Errorf("store = %+v, want the flag kept", store)
	}
	// A locations viewer cannot change sites at all; the flag needs
	// inventory:locations-manage like every other field.
	viewer, _ := signIn(t, h, locationsViewer...)
	update.FulfilsOrders = ptr(false)
	res = viewer.Do(http.MethodPut, sitesPath+"/"+strconv.FormatInt(store.Id, 10), update, ifMatch(store.Revision))
	mustStatus(t, res, http.StatusForbidden, "a viewer turns the flag off")
}

// TestStockLedger_ConcurrentReservesForTheLastUnit is two confirms competing
// for the last unit: each reserves it in its own transaction at once; one
// succeeds and the other is refused insufficient_available.
func TestStockLedger_ConcurrentReservesForTheLastUnit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 1))
	l := ledgerOf(h)
	for round := range 5 {
		errs := make([]error, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range errs {
			wg.Go(func() {
				src := contracts.StockSource{Module: "orders", Kind: "order", ID: int64(100*round + i + 1)}
				<-start
				errs[i] = inTx(t, h, func(tx pgx.Tx) error {
					return l.Reserve(t.Context(), tx, src, s.site.Id, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "1"}})
				})
			})
		}
		close(start)
		wg.Wait()
		codes := []string{refusalCode(errs[0]), refusalCode(errs[1])}
		slices.Sort(codes)
		if !slices.Equal(codes, []string{"", contracts.StockInsufficientAvailable}) || (errs[0] != nil) == (errs[1] != nil) {
			t.Fatalf("round %d: %v; want one reserve and one insufficient_available", round, errs)
		}
		if got := availableAt(t, h, varBolt, s.site.Id); got != "0" {
			t.Fatalf("round %d: available = %s, want 0", round, got)
		}
		h.Exec(t, `DELETE FROM inventory.reservations`)
	}
}

func TestStockLedger_IssueConsumesReservations(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10))
	receive(t, c, s.site.Id, s.shelf2.Id, receiptLine(varBolt, 2))
	l := ledgerOf(h)
	site := s.site.Id
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.Reserve(t.Context(), tx, order, site, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 11, Quantity: "5"}, {VariantID: varBolt, LineID: 12, Quantity: "1"}})
	}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	delivery := contracts.StockSource{Module: "orders", Kind: "delivery", ID: 3}
	issue := contracts.StockIssue{Today: testToday, Document: delivery, SiteID: site, PostedByUserID: &user, PostedByDisplay: "Picker", Lines: []contracts.IssueLine{
		{VariantID: varBolt, LineID: 11, FromLocationID: s.shelf1.Id, Quantity: "2"},
		{VariantID: varBolt, LineID: 11, FromLocationID: s.shelf2.Id, Quantity: "1"},
		{VariantID: varBolt, LineID: 12, FromLocationID: s.shelf1.Id, Quantity: "3"},
	}}
	var movementID int64
	if err := inTx(t, h, func(tx pgx.Tx) error {
		posted, err := l.Issue(t.Context(), tx, order, issue)
		movementID = posted.MovementID
		return err
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Line 11 had 5 and issued 3; line 12 had 1 and issued 3 (floored at 0).
	if got := reserved(t, h, order); !slices.Equal(got, []string{"11:" + strconv.FormatInt(site, 10) + ":2"}) {
		t.Errorf("reservations after the issue = %v, want line 11 at 2", got)
	}
	if onHand(t, h, varBolt, s.shelf1.Id) != "5.000" || onHand(t, h, varBolt, s.shelf2.Id) != "1.000" {
		t.Errorf("balances = %q, %q", onHand(t, h, varBolt, s.shelf1.Id), onHand(t, h, varBolt, s.shelf2.Id))
	}
	res := c.Do(http.MethodGet, movementPath(movementID), nil)
	mustStatus(t, res, http.StatusOK, "the issue")
	var m gen.InventoryMovement
	res.JSON(&m)
	if m.Kind != gen.Issue || len(m.Lines) != 3 || m.Source == nil || m.Source.Module != "orders" || m.Source.Kind != "delivery" || m.Source.Id != "3" ||
		m.PostedBy.UserId == nil || *m.PostedBy.UserId != user {
		t.Errorf("movement = %+v", m)
	}
	res = c.Do(http.MethodGet, movementsPath+"?kind=issue", nil)
	mustStatus(t, res, http.StatusOK, "the issues")

	// The same document again: the first movement, nothing consumed again.
	if err := inTx(t, h, func(tx pgx.Tx) error {
		again, err := l.Issue(t.Context(), tx, order, issue)
		if err == nil && again.MovementID != movementID {
			t.Errorf("a second issue of the delivery = movement %d, want %d", again.MovementID, movementID)
		}
		return err
	}); err != nil {
		t.Fatalf("Issue again: %v", err)
	}
	if got := reserved(t, h, order); len(got) != 1 {
		t.Errorf("reservations after a replay = %v", got)
	}

	// Beyond on hand, from another site's location, and of a service.
	for i, tc := range []struct {
		name string
		line contracts.IssueLine
		want string
	}{
		{"beyond on hand", contracts.IssueLine{VariantID: varBolt, LineID: 11, FromLocationID: s.shelf2.Id, Quantity: "2"}, contracts.StockInsufficientStock},
		{"elsewhere", contracts.IssueLine{VariantID: varBolt, LineID: 11, FromLocationID: s.elsewhre.RootLocationId, Quantity: "1"}, contracts.StockLocationUnknown},
		{"a service", contracts.IssueLine{VariantID: varService, LineID: 11, FromLocationID: s.shelf1.Id, Quantity: "1"}, contracts.StockVariantNotStocked},
	} {
		doc := contracts.StockSource{Module: "orders", Kind: "delivery", ID: int64(10 + i)}
		err := inTx(t, h, func(tx pgx.Tx) error {
			_, err := l.Issue(t.Context(), tx, order, contracts.StockIssue{Today: testToday, Document: doc, SiteID: site, Lines: []contracts.IssueLine{tc.line}})
			return err
		})
		var r *contracts.StockRefusal
		if !errors.As(err, &r) || r.Code != tc.want || r.LineID != 11 || len(r.Lines) != 1 {
			t.Errorf("Issue %s = %v, want %s on line 11", tc.name, err, tc.want)
		}
	}
	assertLedgerConsistent(t, h)
}

// TestStockLedger_CorrectionsIgnoreReservations: an adjustment may take on
// hand below what is reserved, and available shows negative; an issue still
// refuses beyond on hand.
func TestStockLedger_CorrectionsIgnoreReservations(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10))
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return ledgerOf(h).Reserve(t.Context(), tx, order, s.site.Id, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "8"}})
	}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	mustStatus(t, adjust(c, s.site.Id, change(varBolt, s.shelf1.Id, -5)), http.StatusCreated, "adjust below reserved")
	if got := availableAt(t, h, varBolt, s.site.Id); got != "-3" {
		t.Errorf("available = %s, want -3", got)
	}
	res := c.Do(http.MethodGet, "/api/v1/inventory/variants/"+strconv.Itoa(int(varBolt))+"/stock", nil)
	var vs gen.InventoryVariantStock
	res.JSON(&vs)
	if vs.TotalAvailable != -3 || vs.Sites[0].Available != -3 {
		t.Errorf("variant stock = %+v, want available -3", vs)
	}
	// Adjusted to zero, the site still shows its reservation.
	mustStatus(t, adjust(c, s.site.Id, change(varBolt, s.shelf1.Id, -5)), http.StatusCreated, "adjust to zero")
	res = c.Do(http.MethodGet, "/api/v1/inventory/variants/"+strconv.Itoa(int(varBolt))+"/stock", nil)
	res.JSON(&vs)
	if vs.TotalOnHand != 0 || vs.TotalAvailable != -8 || len(vs.Sites) != 1 || len(vs.Sites[0].Rows) != 0 || vs.Sites[0].Reserved != 8 {
		t.Errorf("variant stock = %+v, want the site's reservation alone", vs)
	}

	// Reserved with nothing on hand still holds the variant from a delete.
	holder := inventory.Module().VariantReferences(h.Deps())
	_ = inTx(t, h, func(tx pgx.Tx) error {
		held, err := holder.VariantHeld(t.Context(), tx, varBolt, contracts.VariantHoldDelete)
		if err != nil || held == nil || held.Kind != contracts.VariantHoldKindReserved || held.Quantity != "8" || held.Locations != 1 {
			t.Errorf("VariantHeld = %+v, %v; want 8 reserved at 1 site", held, err)
		}
		return nil
	})
}

// TestStockLedger_PoolOfOne: the ledger runs on the caller's transaction and
// takes no connection of its own, so it completes on a pool of one whose only
// connection the caller's transaction holds.
func TestStockLedger_PoolOfOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 4))
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	l := inventory.Module().StockLedger(h.DepsOn(one))
	err = db.WithTx(ctx, one, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := l.Reserve(ctx, tx, order, s.site.Id, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "3"}}); err != nil {
			return err
		}
		if _, err := l.Issue(ctx, tx, order, contracts.StockIssue{Today: testToday, SiteID: s.site.Id, Lines: []contracts.IssueLine{
			{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "1"},
		}}); err != nil {
			return err
		}
		if _, err := l.ReservationsFor(ctx, tx, order); err != nil {
			return err
		}
		return l.Release(ctx, tx, order, []contracts.StockRelease{{LineID: 1, Quantity: "1"}})
	})
	if err != nil {
		t.Fatalf("the ledger on a pool of one: %v", err)
	}
	if got := availableAt(t, h, varBolt, s.site.Id); got != "2" {
		t.Errorf("available = %s, want 2", got)
	}
}

// Sites answers every site not archived, and Locations the site's locations
// holding a variant, by full code (orders design O46, O47).
func TestStock_SitesAndLocations(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf2.Id, receiptLine(varBolt, 5))
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 2))
	st := stockOf(h)

	sites, err := st.Sites(t.Context())
	if err != nil {
		t.Fatalf("Sites: %v", err)
	}
	var codes []string
	for _, site := range sites {
		codes = append(codes, site.Code)
		if !site.Active {
			t.Errorf("site %s answered inactive", site.Code)
		}
	}
	if !slices.Equal(codes, []string{"BGO", "OSL"}) {
		t.Errorf("Sites = %v, want [BGO OSL]", codes)
	}
	if len(sites) == 2 && (sites[0].RootLocationID != s.elsewhre.RootLocationId || sites[1].RootLocationID != s.site.RootLocationId) {
		t.Errorf("Sites' roots = %+v, want each site's root location", sites)
	}

	// SiteLocations: every location of the site, archived ones too, by full
	// code, the root marked.
	archived := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindShelf, "9")
	h.Exec(t, `UPDATE inventory.locations SET status = 'archived' WHERE id = $1`, archived.Id)
	all, err := st.SiteLocations(t.Context(), s.site.Id)
	if err != nil {
		t.Fatalf("SiteLocations: %v", err)
	}
	var listed []string
	for _, l := range all {
		listed = append(listed, l.FullCode+":"+strconv.FormatBool(l.Active)+":"+strconv.FormatBool(l.Root))
	}
	wantListed := []string{"OSL:true:true", s.shelf1.FullCode + ":true:false", s.shelf2.FullCode + ":true:false", archived.FullCode + ":false:false"}
	if !slices.Equal(listed, wantListed) || all[0].ID != s.site.RootLocationId || all[0].ParentID != nil || all[1].ParentID == nil || *all[1].ParentID != s.site.RootLocationId {
		t.Errorf("SiteLocations = %v, want %v", listed, wantListed)
	}
	if none, err := st.SiteLocations(t.Context(), 999999); err != nil || len(none) != 0 {
		t.Errorf("SiteLocations of an unknown site = %v, %v; want none", none, err)
	}

	locs, err := st.Locations(t.Context(), []int32{varBolt, varNut}, s.site.Id)
	if err != nil {
		t.Fatalf("Locations: %v", err)
	}
	var got []string
	for _, l := range locs {
		got = append(got, strconv.Itoa(int(l.VariantID))+"@"+strconv.FormatInt(l.LocationID, 10)+":"+l.Quantity)
	}
	want := []string{
		strconv.Itoa(int(varBolt)) + "@" + strconv.FormatInt(s.shelf1.Id, 10) + ":2",
		strconv.Itoa(int(varBolt)) + "@" + strconv.FormatInt(s.shelf2.Id, 10) + ":5",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Locations = %v, want %v", got, want)
	}
	if locs, err := st.Locations(t.Context(), []int32{varBolt}, s.elsewhre.Id); err != nil || len(locs) != 0 {
		t.Errorf("Locations elsewhere = %v, %v; want none", locs, err)
	}
}

// TestStockLedger_ALineHoldsOneVariant: a reserve naming a line that already
// holds another variant at the site is refused and writes nothing, and an
// issue consumes a line's reservation only when it issues the variant held.
func TestStockLedger_ALineHoldsOneVariant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10), receiptLine(varNut, 10))
	l := ledgerOf(h)
	site := s.site.Id
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.Reserve(t.Context(), tx, order, site, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 31, Quantity: "4"}})
	}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	want := []string{"31:" + strconv.FormatInt(site, 10) + ":4"}

	err := inTx(t, h, func(tx pgx.Tx) error {
		return l.Reserve(t.Context(), tx, order, site, testToday, []contracts.StockLine{{VariantID: varNut, LineID: 31, Quantity: "2"}})
	})
	if err == nil || refusalCode(err) != "" {
		t.Errorf("Reserve of another variant on the line = %v, want a failure", err)
	}
	if got := reserved(t, h, order); !slices.Equal(got, want) {
		t.Errorf("reservations = %v, want %v", got, want)
	}
	if got := availableAt(t, h, varNut, site); got != "10.000" && got != "10" {
		t.Errorf("nut available = %s, want 10", got)
	}

	// An issue of the nut on line 31 leaves the bolt's reservation as it is.
	delivery := contracts.StockSource{Module: "orders", Kind: "delivery", ID: 31}
	if err := inTx(t, h, func(tx pgx.Tx) error {
		_, err := l.Issue(t.Context(), tx, order, contracts.StockIssue{Today: testToday, Document: delivery, SiteID: site, Lines: []contracts.IssueLine{
			{VariantID: varNut, LineID: 31, FromLocationID: s.shelf1.Id, Quantity: "3"},
		}})
		return err
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := reserved(t, h, order); !slices.Equal(got, want) {
		t.Errorf("reservations after issuing another variant = %v, want %v", got, want)
	}
	assertLedgerConsistent(t, h)
}
