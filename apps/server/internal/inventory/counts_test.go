package inventory_test

import (
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const countsPath = "/api/v1/inventory/counts"

func countPath(id int64) string { return countsPath + "/" + strconv.FormatInt(id, 10) }

// counter holds what a count needs (§5).
var counter = []string{"inventory:locations-view", "inventory:stock-view", "inventory:counts-manage"}

// openCount opens a count of site, of location's subtree when it is not 0.
func openCount(c *modtest.Client, site, location int64) *modtest.Response {
	in := gen.InventoryCountInput{SiteId: site}
	if location != 0 {
		in.LocationId = &location
	}
	return c.Do(http.MethodPost, countsPath, in)
}

// mustOpenCount opens a count and answers it.
func mustOpenCount(t *testing.T, c *modtest.Client, site, location int64) gen.InventoryCount {
	t.Helper()
	res := openCount(c, site, location)
	mustStatus(t, res, http.StatusCreated, "POST /counts")
	var out gen.InventoryCount
	res.JSON(&out)
	return out
}

// counted is a counted line.
func counted(variant int32, location int64, quantity float64) gen.InventoryCountLineInput {
	return gen.InventoryCountLineInput{VariantId: variant, LocationId: location, Counted: qty(quantity)}
}

// putCounted records counted lines.
func putCounted(c *modtest.Client, id int64, lines ...gen.InventoryCountLineInput) *modtest.Response {
	return c.Do(http.MethodPut, countPath(id)+"/lines", gen.InventoryCountLinesInput{Lines: lines})
}

// postCount posts a count.
func postCount(c *modtest.Client, id int64, uncounted gen.InventoryUncounted, acceptMoved bool) *modtest.Response {
	in := gen.InventoryCountPostInput{Uncounted: uncounted}
	if acceptMoved {
		in.AcceptMoved = ptr(true)
	}
	return c.Do(http.MethodPost, countPath(id)+"/post", in)
}

// countAnswer decodes a count.
func countAnswer(t *testing.T, res *modtest.Response, want int, what string) gen.InventoryCount {
	t.Helper()
	mustStatus(t, res, want, what)
	var out gen.InventoryCount
	res.JSON(&out)
	return out
}

// lineAt is the count's line of variant at location.
func lineAt(t *testing.T, c gen.InventoryCount, variant int32, location int64) gen.InventoryCountLine {
	t.Helper()
	for _, l := range *c.Lines {
		if l.Variant.Id == variant && l.Location.Id == location {
			return l
		}
	}
	t.Fatalf("count %d has no line of %d at %d: %+v", c.Id, variant, location, *c.Lines)
	return gen.InventoryCountLine{}
}

// TestCount_FirstCount is I20: a count of a site with no stock opens with no
// lines; every line counted is added, one entered in packagings; posting
// writes one count movement that places the stock, and the count keeps it.
func TestCount_FirstCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, everything...)
	c, _ := signIn(t, h, counter...)
	s := newLedgerSite(t, h, admin)
	h.catalog.setPackagings(varBolt, boxAndPallet()...)

	count := mustOpenCount(t, c, s.site.Id, 0)
	if count.Status != gen.InventoryCountStatusCounting || count.LineCount != 0 || count.Lines == nil || len(*count.Lines) != 0 ||
		count.Scope.Id != s.site.RootLocationId || count.OpenedBy.DisplayName == "" {
		t.Fatalf("count = %+v, want an open count of the whole site with no lines", count)
	}
	count = countAnswer(t, putCounted(c, count.Id, counted(varNut, s.shelf2.Id, 7),
		gen.InventoryCountLineInput{VariantId: varBolt, LocationId: s.shelf1.Id, Entered: entered(qty(3), pkgBox, 2)}), http.StatusOK, "count")
	if count.LineCount != 2 || count.CountedLines != 2 || count.Revision != 2 {
		t.Errorf("count = %+v, want two counted lines", count)
	}
	bolt := lineAt(t, count, varBolt, s.shelf1.Id)
	if !bolt.Added || bolt.Expected != 0 || bolt.Counted == nil || *bolt.Counted != 33 || bolt.Entered == nil ||
		bolt.Entered.Packagings[0].Name != "Box" || bolt.CountedBy == nil || bolt.CountedAt == nil || bolt.Variant.Packagings == nil {
		t.Errorf("the bolts' line = %+v, want 2 boxes and 3 added", bolt)
	}
	if (*count.Lines)[0].Location.FullCode != "OSL-1" {
		t.Errorf("lines = %+v, want them by full code", *count.Lines)
	}

	count = countAnswer(t, postCount(c, count.Id, gen.Keep, false), http.StatusOK, "post")
	if count.Status != gen.InventoryCountStatusPosted || count.MovementId == nil || count.PostedBy == nil || count.Uncounted == nil || *count.Uncounted != gen.Keep {
		t.Fatalf("count = %+v, want it posted with its movement", count)
	}
	res := admin.Do(http.MethodGet, movementPath(*count.MovementId), nil)
	mustStatus(t, res, http.StatusOK, "the count's movement")
	var m gen.InventoryMovement
	res.JSON(&m)
	if m.Kind != gen.Count || m.CountId == nil || *m.CountId != count.Id || len(m.Lines) != 2 {
		t.Errorf("movement = %+v, want one count movement of two lines", m)
	}
	if onHand(t, h, varBolt, s.shelf1.Id) != "33.000" || onHand(t, h, varNut, s.shelf2.Id) != "7.000" {
		t.Errorf("balances = %q, %q", onHand(t, h, varBolt, s.shelf1.Id), onHand(t, h, varNut, s.shelf2.Id))
	}
	res = admin.Do(http.MethodGet, movementsPath+"?kind=count", nil)
	mustStatus(t, res, http.StatusOK, "the count movements")
	var page gen.InventoryMovementPage
	res.JSON(&page)
	if len(page.Movements) != 1 || page.Movements[0].CountId == nil {
		t.Errorf("movements = %+v", page.Movements)
	}
	if got := eventKinds(t, h); !slices.Contains(got, "count_opened") || !slices.Contains(got, "count_posted") {
		t.Errorf("events = %v", got)
	}
	assertLedgerConsistent(t, h)
}

// TestCount_KeepAndZero is I19's differences and the uncounted lines: keep
// leaves an uncounted line's balance, zero empties it; a count whose lines
// change nothing is posted with no movement.
func TestCount_KeepAndZero(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10), receiptLine(varNut, 5))
	receive(t, c, s.site.Id, s.shelf2.Id, receiptLine(varWasher, 3))

	count := mustOpenCount(t, c, s.site.Id, 0)
	if count.LineCount != 3 || lineAt(t, count, varBolt, s.shelf1.Id).Expected != 10 {
		t.Fatalf("count = %+v, want three frozen lines", count)
	}
	countAnswer(t, putCounted(c, count.Id, counted(varBolt, s.shelf1.Id, 9), counted(varBolt, s.shelf1.Id, 8)), http.StatusOK, "count the bolts")
	count = countAnswer(t, postCount(c, count.Id, gen.Keep, false), http.StatusOK, "post, keeping the rest")
	if onHand(t, h, varBolt, s.shelf1.Id) != "8.000" || onHand(t, h, varNut, s.shelf1.Id) != "5.000" || onHand(t, h, varWasher, s.shelf2.Id) != "3.000" {
		t.Errorf("after keep: bolts %q, nuts %q, washers %q", onHand(t, h, varBolt, s.shelf1.Id), onHand(t, h, varNut, s.shelf1.Id), onHand(t, h, varWasher, s.shelf2.Id))
	}
	res := c.Do(http.MethodGet, movementPath(*count.MovementId), nil)
	var m gen.InventoryMovement
	res.JSON(&m)
	if len(m.Lines) != 1 || m.Lines[0].From == nil || m.Lines[0].Quantity != 2 {
		t.Errorf("movement lines = %+v, want 2 bolts out", m.Lines)
	}

	count = mustOpenCount(t, c, s.site.Id, 0)
	countAnswer(t, putCounted(c, count.Id, counted(varNut, s.shelf1.Id, 5)), http.StatusOK, "count the nuts")
	count = countAnswer(t, postCount(c, count.Id, gen.Zero, false), http.StatusOK, "post, zeroing the rest")
	if onHand(t, h, varBolt, s.shelf1.Id) != "" || onHand(t, h, varNut, s.shelf1.Id) != "5.000" || onHand(t, h, varWasher, s.shelf2.Id) != "" {
		t.Errorf("after zero: bolts %q, nuts %q, washers %q", onHand(t, h, varBolt, s.shelf1.Id), onHand(t, h, varNut, s.shelf1.Id), onHand(t, h, varWasher, s.shelf2.Id))
	}

	// A count that finds the stock as it is: posted, no movement.
	count = mustOpenCount(t, c, s.site.Id, s.shelf1.Id)
	countAnswer(t, putCounted(c, count.Id, counted(varNut, s.shelf1.Id, 5)), http.StatusOK, "count the nuts again")
	count = countAnswer(t, postCount(c, count.Id, gen.Keep, false), http.StatusOK, "post a count that changes nothing")
	if count.Status != gen.InventoryCountStatusPosted || count.MovementId != nil {
		t.Errorf("count = %+v, want it posted without a movement", count)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.movements WHERE kind = 'count'`); n != 2 {
		t.Errorf("%d count movements, want 2", n)
	}
	assertLedgerConsistent(t, h)
}

// TestCount_MovedLines is I19's flag: a receipt while counting moves its
// line, the post is refused naming it, and acceptMoved posts the difference
// against the balance at posting.
func TestCount_MovedLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10), receiptLine(varNut, 5))
	count := mustOpenCount(t, c, s.site.Id, s.shelf1.Id)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 4))

	res := c.Do(http.MethodGet, countPath(count.Id), nil)
	count = countAnswer(t, res, http.StatusOK, "GET the count")
	bolt := lineAt(t, count, varBolt, s.shelf1.Id)
	if !bolt.Moved || bolt.Expected != 10 || bolt.Current != 14 || count.MovedLines != 1 || lineAt(t, count, varNut, s.shelf1.Id).Moved {
		t.Fatalf("count = %+v, want the bolts moved", count)
	}
	countAnswer(t, putCounted(c, count.Id, counted(varBolt, s.shelf1.Id, 13), counted(varNut, s.shelf1.Id, 5)), http.StatusOK, "count")
	p := expectRefusal(t, postCount(c, count.Id, gen.Keep, false), http.StatusConflict, "count_lines_moved", "post with a moved line")
	if got := lineCodes(p); !slices.Equal(got, []string{"0:count_lines_moved"}) {
		t.Errorf("lines = %v, want the bolts' line, the first", got)
	}
	if onHand(t, h, varBolt, s.shelf1.Id) != "14.000" {
		t.Errorf("a refused post changed the stock")
	}
	count = countAnswer(t, postCount(c, count.Id, gen.Keep, true), http.StatusOK, "post accepting it")
	if onHand(t, h, varBolt, s.shelf1.Id) != "13.000" || count.MovedLines != 0 {
		t.Errorf("bolts = %q, want 13: the difference from the balance at posting", onHand(t, h, varBolt, s.shelf1.Id))
	}
	assertLedgerConsistent(t, h)
}

// TestCount_Refusals is every refusal of a count's writes: a line outside the
// scope (location_not_found), an unknown variant, a service added, a count
// below zero, both quantity and entered; a written count no longer counting
// (count_not_open); an unknown count (404); an unknown site or location.
func TestCount_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	h.catalog.setPackagings(varBolt, boxAndPallet()...)
	count := mustOpenCount(t, c, s.site.Id, s.shelf1.Id)

	res := putCounted(c, count.Id, counted(varBolt, s.shelf2.Id, 1), counted(999, s.shelf1.Id, 1), counted(varService, s.shelf1.Id, 1),
		counted(varBolt, s.shelf1.Id, -1),
		gen.InventoryCountLineInput{VariantId: varBolt, LocationId: s.shelf1.Id, Counted: qty(1), Entered: entered(nil, pkgBox, 1)},
		gen.InventoryCountLineInput{VariantId: varBolt, LocationId: s.shelf1.Id, Entered: entered(nil, 99, 1)})
	p := expectRefusal(t, res, http.StatusBadRequest, "location_not_found", "bad lines")
	want := []string{"0:location_not_found", "1:variant_not_found", "2:variant_not_stockable", "3:quantity_invalid", "4:entered_invalid", "5:packaging_not_found"}
	if got := lineCodes(p); !slices.Equal(got, want) {
		t.Errorf("lines = %v, want %v", got, want)
	}
	expectRefusal(t, openCount(c, s.site.Id, s.elsewhre.RootLocationId), http.StatusBadRequest, "location_not_found", "a scope in another site")
	mustStatus(t, openCount(c, 999999, 0), http.StatusBadRequest, "an unknown site")

	for _, path := range []string{countPath(999999), countPath(999999) + "/cancel"} {
		method := http.MethodGet
		if path != countPath(999999) {
			method = http.MethodPost
		}
		mustStatus(t, c.Do(method, path, nil), http.StatusNotFound, method+" "+path)
	}
	mustStatus(t, putCounted(c, 999999, counted(varBolt, s.shelf1.Id, 1)), http.StatusNotFound, "PUT lines of no count")
	mustStatus(t, postCount(c, 999999, gen.Keep, false), http.StatusNotFound, "post no count")

	count = countAnswer(t, c.Do(http.MethodPost, countPath(count.Id)+"/cancel", nil), http.StatusOK, "cancel")
	if count.Status != gen.InventoryCountStatusCancelled || count.CancelledBy == nil || count.CancelledAt == nil {
		t.Errorf("count = %+v, want cancelled", count)
	}
	expectRefusal(t, putCounted(c, count.Id, counted(varBolt, s.shelf1.Id, 1)), http.StatusConflict, "count_not_open", "count a cancelled count")
	expectRefusal(t, postCount(c, count.Id, gen.Keep, false), http.StatusConflict, "count_not_open", "post a cancelled count")
	expectRefusal(t, c.Do(http.MethodPost, countPath(count.Id)+"/cancel", nil), http.StatusConflict, "count_not_open", "cancel twice")

	// The list, newest first, by status and site.
	open := mustOpenCount(t, c, s.site.Id, 0)
	res = c.Do(http.MethodGet, countsPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /counts")
	var list gen.InventoryCountList
	res.JSON(&list)
	if len(list.Counts) != 2 || list.Counts[0].Id != open.Id || list.Counts[0].Lines != nil {
		t.Errorf("counts = %+v, want the two, newest first, without lines", list.Counts)
	}
	res = c.Do(http.MethodGet, countsPath+"?status=cancelled&siteId="+strconv.FormatInt(s.site.Id, 10), nil)
	list = gen.InventoryCountList{}
	res.JSON(&list)
	if len(list.Counts) != 1 || list.Counts[0].Id != count.Id {
		t.Errorf("cancelled counts = %+v", list.Counts)
	}
	if got := eventKinds(t, h); !slices.Contains(got, "count_cancelled") {
		t.Errorf("events = %v", got)
	}
	assertLedgerConsistent(t, h)
}

// TestCount_Overlap is I17's refusal: a count still counting refuses another
// of the same location, one below it and one above it, but not a sibling's
// nor another site's; once it is cancelled, the scope is free.
func TestCount_Overlap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	aisle := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	shelf := createLocation(t, c, aisle.Id, gen.InventoryLocationKindShelf, "1")

	first := mustOpenCount(t, c, s.site.Id, aisle.Id)
	expectRefusal(t, openCount(c, s.site.Id, aisle.Id), http.StatusConflict, "count_scope_overlaps", "the same scope")
	expectRefusal(t, openCount(c, s.site.Id, shelf.Id), http.StatusConflict, "count_scope_overlaps", "a scope below")
	expectRefusal(t, openCount(c, s.site.Id, 0), http.StatusConflict, "count_scope_overlaps", "the whole site, above")
	mustOpenCount(t, c, s.site.Id, s.shelf1.Id)
	mustOpenCount(t, c, s.elsewhre.Id, 0)
	mustStatus(t, c.Do(http.MethodPost, countPath(first.Id)+"/cancel", nil), http.StatusOK, "cancel")
	mustOpenCount(t, c, s.site.Id, shelf.Id)
}

// TestCount_ConcurrentOpens is §4's open lock: two overlapping opens at once
// — the site row serialises them, so exactly one is opened.
func TestCount_ConcurrentOpens(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10))
	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Go(func() {
			location := int64(0)
			if i == 1 {
				location = s.shelf1.Id
			}
			statuses[i] = openCount(c, s.site.Id, location).Status
		})
	}
	wg.Wait()
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusCreated, http.StatusConflict}) {
		t.Errorf("statuses = %v, want one 201 and one 409", statuses)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.counts WHERE status = 'counting'`); n != 1 {
		t.Errorf("%d open counts, want 1", n)
	}
}

// TestCount_ConcurrentPostAndReceipt is the count post's lock order racing
// postings to its lines: every receipt lands, the count posts (accepting
// what moved), and the ledger adds up.
func TestCount_ConcurrentPostAndReceipt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(12))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10), receiptLine(varNut, 10))
	count := mustOpenCount(t, c, s.site.Id, 0)
	countAnswer(t, putCounted(c, count.Id, counted(varBolt, s.shelf1.Id, 12), counted(varNut, s.shelf1.Id, 9)), http.StatusOK, "count")
	statuses := make([]int, 6)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Go(func() {
			if i == 0 {
				statuses[i] = postCount(c, count.Id, gen.Keep, true).Status
				return
			}
			statuses[i] = postReceipt(c, s.site.Id, s.shelf1.Id, receiptLine(varNut, 1), receiptLine(varBolt, 1)).Status
		})
	}
	wg.Wait()
	if statuses[0] != http.StatusOK {
		t.Errorf("post = %d, want 200", statuses[0])
	}
	for i, st := range statuses[1:] {
		if st != http.StatusCreated {
			t.Errorf("receipt %d = %d, want 201", i, st)
		}
	}
	assertLedgerConsistent(t, h)
}

// TestCount_LinesRaceDelete is counting racing a location's delete: the
// lines' locations are shared, so either the lines are recorded and the
// delete is refused (location_has_history), or the location is gone and the
// line is location_not_found — never a 500.
func TestCount_LinesRaceDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	count := mustOpenCount(t, c, s.site.Id, 0)
	for i := range 8 {
		bin := createLocation(t, c, s.shelf1.Id, gen.InventoryLocationKindBin, strconv.Itoa(i))
		var put, del *modtest.Response
		var wg sync.WaitGroup
		wg.Go(func() { put = putCounted(c, count.Id, counted(varBolt, bin.Id, 1)) })
		wg.Go(func() { del = c.Do(http.MethodDelete, locationPath(bin.Id), nil) })
		wg.Wait()
		switch {
		case put.Status == http.StatusOK && del.Status == http.StatusConflict:
		case put.Status == http.StatusBadRequest && del.Status == http.StatusNoContent:
			if p := problemOf(t, put); p.Code == nil || *p.Code != "location_not_found" {
				t.Errorf("round %d: lines = %s, want location_not_found", i, put.Body)
			}
		default:
			t.Errorf("round %d: lines = %d %s, delete = %d %s", i, put.Status, put.Body, del.Status, del.Body)
		}
	}
}

// TestCount_CancelWithoutCatalog is the cancel's answer when the catalog is
// unavailable after it commits: the answer names the lines' variants, so it
// is the 503 the other count writes answer, never a 500 — and the count is
// cancelled.
func TestCount_CancelWithoutCatalog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	count := mustOpenCount(t, c, s.site.Id, 0)
	mustStatus(t, putCounted(c, count.Id, counted(varBolt, s.shelf1.Id, 2)), http.StatusOK, "count a line")
	h.catalog.mu.Lock()
	h.catalog.err = inventory.ErrDependencyUnavailable
	h.catalog.mu.Unlock()
	expectRefusal(t, c.Do(http.MethodPost, countPath(count.Id)+"/cancel", nil), http.StatusServiceUnavailable, "dependency_unavailable", "cancel without the catalog")
	if n := h.Count(t, `SELECT count(*) FROM inventory.counts WHERE id = $1 AND status = 'cancelled'`, count.Id); n != 1 {
		t.Errorf("the count is not cancelled")
	}
}

// TestCount_LocationHasOpenCount is §9's archive under an open count: the
// scope, a location below it, one above it and the site are refused
// (location_has_open_count) until the count is cancelled. A location a count
// named is never deleted.
func TestCount_LocationHasOpenCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	aisle := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	shelf := createLocation(t, c, aisle.Id, gen.InventoryLocationKindShelf, "1")
	bin := createLocation(t, c, shelf.Id, gen.InventoryLocationKindBin, "1")
	count := mustOpenCount(t, c, s.site.Id, shelf.Id)

	archive := func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }
	expectRefusal(t, putLocation(c, getLocation(t, c, shelf.Id).Location, archive), http.StatusConflict, "location_has_open_count", "archive the scope")
	expectRefusal(t, putLocation(c, getLocation(t, c, bin.Id).Location, archive), http.StatusConflict, "location_has_open_count", "archive below it")
	expectRefusal(t, putLocation(c, getLocation(t, c, aisle.Id).Location, archive), http.StatusConflict, "location_has_open_count", "archive above it")
	site := s.site
	res := c.Do(http.MethodPut, sitePath(site.Id), gen.InventorySiteUpdate{Code: site.Code, Name: site.Name, Kind: site.Kind, Status: gen.Archived}, ifMatch(site.Revision))
	expectRefusal(t, res, http.StatusConflict, "location_has_open_count", "archive the site")
	mustStatus(t, putLocation(c, getLocation(t, c, s.shelf2.Id).Location, archive), http.StatusOK, "archive a shelf the count does not cover")

	mustStatus(t, c.Do(http.MethodPost, countPath(count.Id)+"/cancel", nil), http.StatusOK, "cancel")
	mustStatus(t, putLocation(c, getLocation(t, c, bin.Id).Location, archive), http.StatusOK, "archive the bin")
	expectRefusal(t, c.Do(http.MethodDelete, locationPath(shelf.Id), nil), http.StatusConflict, "location_has_history", "delete a counted scope")
	expectRefusal(t, c.Do(http.MethodDelete, sitePath(site.Id), nil), http.StatusConflict, "location_has_history", "delete a counted site")
}

// TestCount_ReparentUnderOpenCount is the move under an open count: a
// re-parent is refused (location_has_open_count) while a count still counting
// has its scope in the moved subtree, above where it was, or at or above
// where it would go — so two counts never cover the same balances and a
// count's lines never leave its scope.
func TestCount_ReparentUnderOpenCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	aisleA := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	shelfA := createLocation(t, c, aisleA.Id, gen.InventoryLocationKindShelf, "1")
	binA := createLocation(t, c, shelfA.Id, gen.InventoryLocationKindBin, "1")
	aisleB := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindAisle, "B")
	aisleC := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindAisle, "C")
	under := func(parent int64) func(*gen.InventoryLocationUpdate) {
		return func(u *gen.InventoryLocationUpdate) { u.ParentId = &parent }
	}

	// The scope of one count in the moved subtree, into another's scope.
	inBin := mustOpenCount(t, c, s.site.Id, binA.Id)
	inB := mustOpenCount(t, c, s.site.Id, aisleB.Id)
	expectRefusal(t, putLocation(c, getLocation(t, c, shelfA.Id).Location, under(aisleB.Id)), http.StatusConflict, "location_has_open_count", "move a count's scope into another's")
	expectRefusal(t, putLocation(c, getLocation(t, c, shelfA.Id).Location, under(aisleC.Id)), http.StatusConflict, "location_has_open_count", "move a count's scope anywhere")
	expectRefusal(t, putLocation(c, getLocation(t, c, s.shelf1.Id).Location, under(aisleB.Id)), http.StatusConflict, "location_has_open_count", "move into a count's scope")
	mustStatus(t, c.Do(http.MethodPost, countPath(inBin.Id)+"/cancel", nil), http.StatusOK, "cancel the bin's count")
	mustStatus(t, c.Do(http.MethodPost, countPath(inB.Id)+"/cancel", nil), http.StatusOK, "cancel aisle B's count")

	// A location with lines in a count, out of the count's scope.
	inA := mustOpenCount(t, c, s.site.Id, aisleA.Id)
	mustStatus(t, putCounted(c, inA.Id, counted(varBolt, binA.Id, 3)), http.StatusOK, "count the bin")
	expectRefusal(t, putLocation(c, getLocation(t, c, binA.Id).Location, under(aisleC.Id)), http.StatusConflict, "location_has_open_count", "move a counted bin out of its count")
	mustStatus(t, putLocation(c, getLocation(t, c, s.shelf2.Id).Location, under(aisleC.Id)), http.StatusOK, "move a shelf no count covers")
	if got := getLocation(t, c, binA.Id).Location; *got.ParentId != shelfA.Id {
		t.Errorf("the bin moved to %d under an open count", *got.ParentId)
	}
	mustStatus(t, c.Do(http.MethodPost, countPath(inA.Id)+"/cancel", nil), http.StatusOK, "cancel aisle A's count")
	mustStatus(t, putLocation(c, getLocation(t, c, binA.Id).Location, under(aisleC.Id)), http.StatusOK, "move the bin once no count covers it")
}

// TestCount_StatsAndAttention is openCounts, and countOpenLong for a count
// left counting more than a week — raised to a counter only.
func TestCount_StatsAndAttention(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	count := mustOpenCount(t, c, s.site.Id, s.shelf1.Id)
	var stats gen.InventoryStats
	res := c.Do(http.MethodGet, statsPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /stats")
	res.JSON(&stats)
	if stats.OpenCounts != 1 {
		t.Errorf("openCounts = %d, want 1", stats.OpenCounts)
	}
	attention := func(c *modtest.Client) []gen.InventoryStatsAttentionItem {
		t.Helper()
		var items []gen.InventoryStatsAttentionItem
		res := c.Do(http.MethodGet, attentionPath, nil)
		mustStatus(t, res, http.StatusOK, "GET /stats/attention")
		res.JSON(&items)
		return items
	}
	if items := attention(c); len(items) != 0 {
		t.Errorf("attention = %+v, want none within the week", items)
	}
	h.Advance(8 * 24 * time.Hour)
	c, _ = signIn(t, h, counter...)
	viewer, _ := signIn(t, h, stockViewer...)
	items := attention(c)
	if len(items) != 1 || items[0].Type != gen.CountOpenLong || items[0].EntityId != strconv.FormatInt(count.Id, 10) ||
		items[0].Title != "The count of OSL-1 has been open for 8 days" {
		t.Errorf("attention = %+v, want the count", items)
	}
	if items := attention(viewer); len(items) != 0 {
		t.Errorf("a stock viewer's attention = %+v, want none", items)
	}
}

// TestCount_NeedsItsKey is §5: counts need counts-manage beside both views.
func TestCount_NeedsItsKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, admin)
	c, _ := signIn(t, h, stockAdjuster...)
	mustStatus(t, openCount(c, s.site.Id, 0), http.StatusForbidden, "open without counts-manage")
	mustStatus(t, c.Do(http.MethodGet, countsPath, nil), http.StatusForbidden, "list without counts-manage")
}
