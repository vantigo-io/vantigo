package inventory_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// putLocation replaces a location's fields from the location as read, with
// change applied.
func putLocation(c *modtest.Client, l gen.InventoryLocation, change func(*gen.InventoryLocationUpdate)) *modtest.Response {
	u := gen.InventoryLocationUpdate{Kind: l.Kind, Code: l.Code, Name: l.Name, ParentId: l.ParentId, SortOrder: l.SortOrder, Status: l.Status}
	change(&u)
	return c.Do(http.MethodPut, locationPath(l.Id), u, ifMatch(l.Revision))
}

// TestLocations_CreateAndRead is a tree of three levels: full codes joined
// from the root (I4), depth and ancestors, the path and children of a
// location (I29), and the record of each create.
func TestLocations_CreateAndRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, locationsManager...)
	site := createSite(t, c, "OSL")
	a := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "a")
	r2 := createLocation(t, c, a.Id, gen.InventoryLocationKindRack, "02")
	r1 := createLocation(t, c, a.Id, gen.InventoryLocationKindRack, "01")
	shelf := createLocation(t, c, r1.Id, gen.InventoryLocationKindShelf, "3")
	if a.FullCode != "OSL-A" || shelf.FullCode != "OSL-A-01-3" || shelf.Depth != 3 || *shelf.ParentId != r1.Id || shelf.SiteId != site.Id {
		t.Errorf("a = %+v, shelf = %+v", a, shelf)
	}
	d := getLocation(t, c, shelf.Id)
	path := []string{}
	for _, p := range d.Path {
		path = append(path, p.FullCode)
	}
	if !slices.Equal(path, []string{"OSL", "OSL-A", "OSL-A-01"}) || d.Site.Code != "OSL" {
		t.Errorf("path = %v, site %+v", path, d.Site)
	}
	d = getLocation(t, c, a.Id)
	if len(d.Children) != 2 || d.Children[0].Id != r1.Id || d.Children[1].Id != r2.Id {
		t.Errorf("children = %+v, want 01 then 02", d.Children)
	}
	mustStatus(t, c.Do(http.MethodGet, locationPath(999999), nil), http.StatusNotFound, "GET /locations/{unknown}")
	if n := h.Count(t, `SELECT count(*) FROM inventory.events WHERE kind = 'location_created'`); n != 4 {
		t.Errorf("%d location_created events, want 4", n)
	}
}

// TestLocations_CreateRefusals is every refusal of a create (I2, I5, I6):
// code_invalid, parent_invalid (unknown, archived), depth_exceeded and
// location_code_taken.
func TestLocations_CreateRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, locationsManager...)
	site := createSite(t, c, "OSL")
	root := site.RootLocationId

	res := c.Do(http.MethodPost, locationsPath, gen.InventoryLocationInput{ParentId: root, Kind: gen.InventoryLocationKindShelf, Code: "A-1"})
	expectRefusal(t, res, http.StatusBadRequest, "code_invalid", "a code with -")
	res = c.Do(http.MethodPost, locationsPath, gen.InventoryLocationInput{ParentId: root, Kind: gen.InventoryLocationKindShelf, Code: "loc7"})
	expectRefusal(t, res, http.StatusBadRequest, "code_invalid", "a LOC token as a code")
	res = c.Do(http.MethodPost, locationsPath, gen.InventoryLocationInput{ParentId: root, Kind: gen.InventoryLocationKindSite, Code: "S"})
	mustStatus(t, res, http.StatusBadRequest, "kind site")
	res = c.Do(http.MethodPost, locationsPath, gen.InventoryLocationInput{ParentId: 999999, Kind: gen.InventoryLocationKindShelf, Code: "X"})
	expectRefusal(t, res, http.StatusBadRequest, "parent_invalid", "an unknown parent")

	createLocation(t, c, root, gen.InventoryLocationKindAisle, "A")
	res = c.Do(http.MethodPost, locationsPath, gen.InventoryLocationInput{ParentId: root, Kind: gen.InventoryLocationKindZone, Code: "a"})
	expectRefusal(t, res, http.StatusConflict, "location_code_taken", "a sibling's code")

	// Eight levels below the root, and no ninth.
	parent := root
	for _, code := range []string{"L1", "L2", "L3", "L4", "L5", "L6", "L7", "L8"} {
		parent = createLocation(t, c, parent, gen.InventoryLocationKindArea, code).Id
	}
	res = c.Do(http.MethodPost, locationsPath, gen.InventoryLocationInput{ParentId: parent, Kind: gen.InventoryLocationKindBin, Code: "L9"})
	expectRefusal(t, res, http.StatusBadRequest, "depth_exceeded", "a ninth level")

	z := createLocation(t, c, root, gen.InventoryLocationKindZone, "Z")
	res = putLocation(c, z, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived })
	mustStatus(t, res, http.StatusOK, "archive Z")
	res = c.Do(http.MethodPost, locationsPath, gen.InventoryLocationInput{ParentId: z.Id, Kind: gen.InventoryLocationKindShelf, Code: "1"})
	expectRefusal(t, res, http.StatusBadRequest, "parent_invalid", "under an archived parent")
}

// TestLocations_ReparentWithStock is I21: a subtree holding stock moved under
// another parent of its site — its full codes, ancestors and depths
// rewritten, its balances untouched — and the record of it.
func TestLocations_ReparentWithStock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalog(h)
	c, _ := signIn(t, h, everything...)
	site := createSite(t, c, "OSL")
	a := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	b := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "B")
	rack := createLocation(t, c, a.Id, gen.InventoryLocationKindRack, "01")
	shelf := createLocation(t, c, rack.Id, gen.InventoryLocationKindShelf, "1")
	receive(t, c, site.Id, shelf.Id, receiptLine(varBolt, 12.5), receiptLine(varNut, 3))
	receive(t, c, site.Id, rack.Id, receiptLine(varBolt, 1))

	res := putLocation(c, rack, func(u *gen.InventoryLocationUpdate) { u.ParentId = &b.Id; u.Code = "07" })
	mustStatus(t, res, http.StatusOK, "re-parent and recode 01")
	var moved gen.InventoryLocation
	res.JSON(&moved)
	if moved.FullCode != "OSL-B-07" || *moved.ParentId != b.Id || moved.Revision != rack.Revision+1 {
		t.Errorf("rack = %+v, want OSL-B-07 under B", moved)
	}
	d := getLocation(t, c, shelf.Id)
	if d.Location.FullCode != "OSL-B-07-1" || d.Location.Depth != 3 || len(d.Path) != 3 || d.Path[1].Id != b.Id || d.Path[2].Id != rack.Id {
		t.Errorf("shelf = %+v, path %+v, want OSL-B-07-1 under B", d.Location, d.Path)
	}
	if got := onHand(t, h, varBolt, shelf.Id); got != "12.500" {
		t.Errorf("bolts on the shelf = %q, want 12.500 untouched", got)
	}
	if got := onHand(t, h, varBolt, rack.Id); got != "1.000" {
		t.Errorf("bolts on the rack = %q, want 1.000 untouched", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.events WHERE kind = 'location_reparented' AND entity_id = $1`, rack.Id); n != 1 {
		t.Errorf("%d location_reparented events, want 1", n)
	}
	// The stock now counts under B's subtree, and no longer under A's.
	var contents gen.InventoryLocationContents
	res = c.Do(http.MethodGet, locationPath(b.Id)+"/contents", nil)
	mustStatus(t, res, http.StatusOK, "GET B's contents")
	res.JSON(&contents)
	if len(contents.Rows) != 3 {
		t.Errorf("B's contents = %+v, want three rows", contents.Rows)
	}
	assertLedgerConsistent(t, h)
}

// TestLocations_ChangeRefusals is every refusal of a change (I2, I4, I6,
// I21): parent_invalid (itself, a descendant, another site, archived),
// depth_exceeded through a re-parent, location_code_taken, full_code_taken
// never reachable past it, revision_conflict, root_location, and
// location_has_stock on an archive.
func TestLocations_ChangeRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalog(h)
	c, _ := signIn(t, h, everything...)
	site := createSite(t, c, "OSL")
	other := createSite(t, c, "BGO")
	a := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	b := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "B")
	child := createLocation(t, c, a.Id, gen.InventoryLocationKindRack, "1")

	for name, parent := range map[string]int64{"itself": a.Id, "a descendant": child.Id, "another site": other.RootLocationId, "unknown": 999999} {
		res := putLocation(c, a, func(u *gen.InventoryLocationUpdate) { u.ParentId = &parent })
		expectRefusal(t, res, http.StatusBadRequest, "parent_invalid", "re-parent under "+name)
	}
	res := putLocation(c, a, func(u *gen.InventoryLocationUpdate) { u.Code = "B" })
	expectRefusal(t, res, http.StatusConflict, "location_code_taken", "recode to a sibling's code")
	res = putLocation(c, a, func(u *gen.InventoryLocationUpdate) { u.Code = "B-1" })
	expectRefusal(t, res, http.StatusBadRequest, "code_invalid", "recode to an invalid code")
	res = putLocation(c, a, func(u *gen.InventoryLocationUpdate) { u.ParentId = nil })
	mustStatus(t, res, http.StatusBadRequest, "a non-root without a parent")
	stale := a
	stale.Revision++
	expectRefusal(t, putLocation(c, stale, func(*gen.InventoryLocationUpdate) {}), http.StatusConflict, "revision_conflict", "a stale If-Match")
	gone := a
	gone.Id = 999999
	mustStatus(t, putLocation(c, gone, func(*gen.InventoryLocationUpdate) {}), http.StatusNotFound, "PUT /locations/{unknown}")

	// A deep chain under B cannot take A's two levels below level 7.
	parent := b.Id
	for _, code := range []string{"L2", "L3", "L4", "L5", "L6", "L7"} {
		parent = createLocation(t, c, parent, gen.InventoryLocationKindArea, code).Id
	}
	res = putLocation(c, a, func(u *gen.InventoryLocationUpdate) { u.ParentId = &parent })
	expectRefusal(t, res, http.StatusBadRequest, "depth_exceeded", "re-parent past the depth limit")

	// The root takes its name and sort order, nothing more.
	root := getLocation(t, c, site.RootLocationId).Location
	expectRefusal(t, putLocation(c, root, func(u *gen.InventoryLocationUpdate) { u.Code = "X" }), http.StatusConflict, "root_location", "recode the root")
	expectRefusal(t, putLocation(c, root, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusConflict, "root_location", "archive the root")
	name := "Main floor"
	res = putLocation(c, root, func(u *gen.InventoryLocationUpdate) { u.Name = &name; u.SortOrder = 4 })
	mustStatus(t, res, http.StatusOK, "rename the root")
	expectRefusal(t, c.Do(http.MethodDelete, locationPath(root.Id), nil), http.StatusConflict, "root_location", "delete the root")

	// Archived parents take no child, and archived stock-holders are refused.
	receive(t, c, site.Id, child.Id, receiptLine(varNut, 2))
	p := expectRefusal(t, putLocation(c, a, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusConflict, "location_has_stock", "archive A with stock below")
	if p.Detail == nil || !strings.Contains(*p.Detail, "2") || !strings.Contains(*p.Detail, "1 stock line") {
		t.Errorf("detail = %v, want the quantity and the lines", p.Detail)
	}
	res = putLocation(c, b, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived })
	mustStatus(t, res, http.StatusOK, "archive B")
	res = putLocation(c, a, func(u *gen.InventoryLocationUpdate) { u.ParentId = &b.Id })
	expectRefusal(t, res, http.StatusBadRequest, "parent_invalid", "re-parent under an archived parent")
	assertLedgerConsistent(t, h)
}

// TestLocations_ArchiveReactivateDelete is I6: an archive reaches the
// subtree; a reactivation the location alone, under an active parent; a
// delete takes a subtree no line ever named, and refuses one with history.
func TestLocations_ArchiveReactivateDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalog(h)
	c, _ := signIn(t, h, everything...)
	site := createSite(t, c, "OSL")
	a := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	rack := createLocation(t, c, a.Id, gen.InventoryLocationKindRack, "1")
	shelf := createLocation(t, c, rack.Id, gen.InventoryLocationKindShelf, "1")

	res := putLocation(c, a, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived })
	mustStatus(t, res, http.StatusOK, "archive A")
	for _, id := range []int64{a.Id, rack.Id, shelf.Id} {
		if got := getLocation(t, c, id).Location.Status; got != gen.Archived {
			t.Errorf("location %d = %s, want archived with A", id, got)
		}
	}
	rackNow := getLocation(t, c, rack.Id).Location
	res = putLocation(c, rackNow, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Active })
	expectRefusal(t, res, http.StatusBadRequest, "parent_invalid", "reactivate under an archived parent")
	aNow := getLocation(t, c, a.Id).Location
	mustStatus(t, putLocation(c, aNow, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Active }), http.StatusOK, "reactivate A")
	if got := getLocation(t, c, rack.Id).Location.Status; got != gen.Archived {
		t.Errorf("rack = %s, want still archived: a reactivation is the location's alone", got)
	}
	rackNow = getLocation(t, c, rack.Id).Location
	mustStatus(t, putLocation(c, rackNow, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Active }), http.StatusOK, "reactivate the rack")

	// History refuses a delete; none lets the subtree go.
	shelfNow := getLocation(t, c, shelf.Id).Location
	mustStatus(t, putLocation(c, shelfNow, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Active }), http.StatusOK, "reactivate the shelf")
	receive(t, c, site.Id, shelf.Id, receiptLine(varBolt, 1))
	res = c.Do(http.MethodPost, adjustmentsPath, gen.InventoryAdjustmentInput{
		ClientRequestId: uuid.New(), SiteId: site.Id, Reason: gen.InventoryAdjustmentReasonCorrection,
		Lines: []gen.InventoryAdjustmentLine{{VariantId: varBolt, LocationId: shelf.Id, Mode: gen.Set, Quantity: 0}},
	})
	mustStatus(t, res, http.StatusCreated, "adjust the shelf to zero")
	expectRefusal(t, c.Do(http.MethodDelete, locationPath(a.Id), nil), http.StatusConflict, "location_has_history", "delete A with history below")
	b := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "B")
	createLocation(t, c, b.Id, gen.InventoryLocationKindRack, "1")
	mustStatus(t, c.Do(http.MethodDelete, locationPath(b.Id), nil), http.StatusNoContent, "delete B")
	if n := h.Count(t, `SELECT count(*) FROM inventory.locations WHERE full_code LIKE 'OSL-B%'`); n != 0 {
		t.Errorf("%d of B's subtree left, want none", n)
	}
	mustStatus(t, c.Do(http.MethodDelete, locationPath(b.Id), nil), http.StatusNotFound, "delete B again")
	if kinds := eventKinds(t, h); !slices.Contains(kinds, "location_archived") || !slices.Contains(kinds, "location_reactivated") || !slices.Contains(kinds, "location_deleted") {
		t.Errorf("events = %v", kinds)
	}
	assertLedgerConsistent(t, h)
}

// TestBulk_Grid is I5: an aisle, racks 01–03 and shelves 1–2 under a parent —
// padding from the start's width, every level under every location of the
// one before — and one event naming them.
func TestBulk_Grid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, locationsManager...)
	site := createSite(t, c, "OSL")
	to3, to2 := "03", "2"
	res := c.Do(http.MethodPost, bulkPath, gen.InventoryBulkCreateInput{ParentId: site.RootLocationId, Levels: []gen.InventoryBulkLevel{
		{Kind: gen.InventoryLocationKindAisle, From: "a"},
		{Kind: gen.InventoryLocationKindRack, From: "01", To: &to3},
		{Kind: gen.InventoryLocationKindShelf, From: "1", To: &to2},
	}})
	mustStatus(t, res, http.StatusCreated, "POST /locations/bulk")
	var out gen.InventoryBulkCreated
	res.JSON(&out)
	codes := []string{}
	for _, l := range out.Created {
		codes = append(codes, l.FullCode)
	}
	want := []string{"OSL-A", "OSL-A-01", "OSL-A-01-1", "OSL-A-01-2", "OSL-A-02", "OSL-A-02-1", "OSL-A-02-2", "OSL-A-03", "OSL-A-03-1", "OSL-A-03-2"}
	if !slices.Equal(codes, want) {
		t.Errorf("created = %v, want %v", codes, want)
	}
	if d := getLocation(t, c, out.Created[3].Id); d.Location.Depth != 3 || len(d.Path) != 3 || d.Path[2].FullCode != "OSL-A-01" {
		t.Errorf("OSL-A-01-2 = %+v, path %+v", d.Location, d.Path)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.events WHERE kind = 'locations_bulk_created'`); n != 1 {
		t.Errorf("%d bulk events, want 1", n)
	}
}

// TestBulk_Refusals is a bulk create refused whole: the colliding full codes
// listed, more than 1000 locations, past the depth limit, a range that is
// not one, and an archived parent.
func TestBulk_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, locationsManager...)
	site := createSite(t, c, "OSL")
	a := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	createLocation(t, c, a.Id, gen.InventoryLocationKindRack, "02")
	createLocation(t, c, a.Id, gen.InventoryLocationKindRack, "04")
	before := h.Count(t, `SELECT count(*) FROM inventory.locations`)

	to := "05"
	res := c.Do(http.MethodPost, bulkPath, gen.InventoryBulkCreateInput{ParentId: a.Id, Levels: []gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindRack, From: "01", To: &to}}})
	p := expectRefusal(t, res, http.StatusConflict, "location_code_taken", "a grid colliding with two racks")
	if p.Detail == nil || !strings.Contains(*p.Detail, "OSL-A-02") || !strings.Contains(*p.Detail, "OSL-A-04") {
		t.Errorf("detail = %v, want both colliding full codes", p.Detail)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.locations`); n != before {
		t.Errorf("%d locations, want %d: all or nothing", n, before)
	}

	big, small := "100", "11"
	res = c.Do(http.MethodPost, bulkPath, gen.InventoryBulkCreateInput{ParentId: a.Id, Levels: []gen.InventoryBulkLevel{
		{Kind: gen.InventoryLocationKindRack, From: "10", To: &big},
		{Kind: gen.InventoryLocationKindShelf, From: "1", To: &small},
	}})
	expectRefusal(t, res, http.StatusBadRequest, "bulk_too_large", "91 racks of 11 shelves")

	letter := "3"
	res = c.Do(http.MethodPost, bulkPath, gen.InventoryBulkCreateInput{ParentId: a.Id, Levels: []gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindRack, From: "A", To: &letter}}})
	expectRefusal(t, res, http.StatusBadRequest, "code_invalid", "a letter to a digit")

	parent := a.Id
	for _, code := range []string{"L2", "L3", "L4", "L5", "L6"} {
		parent = createLocation(t, c, parent, gen.InventoryLocationKindArea, code).Id
	}
	res = c.Do(http.MethodPost, bulkPath, gen.InventoryBulkCreateInput{ParentId: parent, Levels: []gen.InventoryBulkLevel{
		{Kind: gen.InventoryLocationKindRack, From: "1"}, {Kind: gen.InventoryLocationKindShelf, From: "1"}, {Kind: gen.InventoryLocationKindBin, From: "1"},
	}})
	expectRefusal(t, res, http.StatusBadRequest, "depth_exceeded", "three levels below level 6")

	mustStatus(t, putLocation(c, getLocation(t, c, a.Id).Location, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusOK, "archive A")
	res = c.Do(http.MethodPost, bulkPath, gen.InventoryBulkCreateInput{ParentId: a.Id, Levels: []gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindRack, From: "9"}}})
	expectRefusal(t, res, http.StatusBadRequest, "parent_invalid", "bulk under an archived parent")
	res = c.Do(http.MethodPost, bulkPath, gen.InventoryBulkCreateInput{ParentId: 999999, Levels: []gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindRack, From: "9"}}})
	expectRefusal(t, res, http.StatusBadRequest, "parent_invalid", "bulk under an unknown parent")
	res = c.Do(http.MethodPost, bulkPath, map[string]any{"parentId": a.Id, "levels": []map[string]any{{"kind": "rack", "from": "1", "step": 2}}})
	mustStatus(t, res, http.StatusBadRequest, "a level with an unknown property")
	if p := problemOf(t, res); p.Errors == nil || len((*p.Errors)["levels[0].step"]) == 0 {
		t.Errorf("errors = %s, want levels[0].step", res.Body)
	}
}

// TestLocations_StockRefusalSaysTheQuantityOnlyToStockViewers is §5 on an
// archive's location_has_stock: the quantity and the number of stock lines
// are stock, said to a caller holding inventory:stock-view; one who manages
// only the structure hears that something holds stock, and nothing more.
func TestLocations_StockRefusalSaysTheQuantityOnlyToStockViewers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalog(h)
	c, _ := signIn(t, h, everything...)
	manager, _ := signIn(t, h, locationsManager...)
	site := createSite(t, c, "OSL")
	a := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	shelf := createLocation(t, c, a.Id, gen.InventoryLocationKindShelf, "1")
	receive(t, c, site.Id, shelf.Id, receiptLine(varNut, 7))

	archiveA := func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }
	archiveSite := gen.InventorySiteUpdate{Code: "OSL", Name: site.Name, Kind: site.Kind, Status: gen.Archived}
	for _, tc := range []struct {
		who       string
		client    *modtest.Client
		seesStock bool
	}{{"a stock viewer", c, true}, {"a structure manager", manager, false}} {
		for what, res := range map[string]*modtest.Response{
			"location": putLocation(tc.client, a, archiveA),
			"site":     tc.client.Do(http.MethodPut, sitePath(site.Id), archiveSite, ifMatch(site.Revision)),
		} {
			p := expectRefusal(t, res, http.StatusConflict, "location_has_stock", "archive the "+what+" as "+tc.who)
			if p.Detail == nil {
				t.Fatalf("archive the %s as %s: no detail", what, tc.who)
			}
			saysQuantity := strings.Contains(*p.Detail, "7") || strings.Contains(*p.Detail, "stock line")
			if saysQuantity != tc.seesStock {
				t.Errorf("archive the %s as %s: detail %q, want the quantity and lines only to a stock viewer", what, tc.who, *p.Detail)
			}
			if !strings.Contains(*p.Detail, "holds") {
				t.Errorf("archive the %s as %s: detail %q, want it to say the stock is held", what, tc.who, *p.Detail)
			}
		}
	}
}

// TestBulk_ParentDeletedWhileWaiting is a bulk create whose parent is deleted
// while it waits for the site row: once it holds the site, the parent is
// gone, and it answers parent_invalid as the single create does — not 500.
func TestBulk_ParentDeletedWhileWaiting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, locationsManager...)
	site := createSite(t, c, "OSL")
	a := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "A")

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM inventory.sites WHERE id = $1 FOR UPDATE`, site.Id); err != nil {
		t.Fatalf("gate: lock the site: %v", err)
	}

	answered := make(chan *modtest.Response, 1)
	go func() {
		answered <- c.Do(http.MethodPost, bulkPath, gen.InventoryBulkCreateInput{ParentId: a.Id, Levels: []gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindRack, From: "1"}}})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for h.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) < 1 {
		select {
		case r := <-answered:
			t.Fatalf("the bulk create answered %d %s without waiting for the site", r.Status, r.Body)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the bulk create never waited for the site")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := gate.Exec(ctx, `DELETE FROM inventory.locations WHERE id = $1`, a.Id); err != nil {
		t.Fatalf("gate: delete the parent: %v", err)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: commit: %v", err)
	}
	select {
	case r := <-answered:
		expectRefusal(t, r, http.StatusBadRequest, "parent_invalid", "bulk under a parent deleted meanwhile")
	case <-time.After(10 * time.Second):
		t.Fatal("the bulk create never answered after the parent was deleted")
	}
}
