package inventory_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// TestMeta_Capabilities is GET /meta: the caller's keys, and nothing to a
// caller with none.
func TestMeta_Capabilities(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	var meta gen.InventoryMeta
	res := c.Do(http.MethodGet, metaPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /meta")
	res.JSON(&meta)
	if !meta.CanViewLocations || !meta.CanManageLocations || !meta.CanViewStock || !meta.CanAdjust || !meta.CanReceive || !meta.CanCount {
		t.Errorf("meta = %+v, want every capability", meta)
	}
	none, _ := signIn(t, h)
	res = none.Do(http.MethodGet, metaPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /meta without keys")
	res.JSON(&meta)
	if meta.CanViewLocations || meta.CanViewStock || meta.CanAdjust {
		t.Errorf("meta = %+v, want none", meta)
	}
}

// TestSites_CreateReadList is a site's life up to its list: created with its
// root (I1), the code upper-cased, the address kept, listed by code, and an
// archived site listed only when asked for.
func TestSites_CreateReadList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, locationsManager...)

	line1, country := "Gate 1", "no"
	res := c.Do(http.MethodPost, sitesPath, gen.InventorySiteInput{
		Code: " osl ", Name: "Oslo", Kind: gen.Warehouse, Address: &gen.InventoryAddress{Line1: &line1, Country: &country},
	})
	mustStatus(t, res, http.StatusCreated, "POST /sites")
	var site gen.InventorySite
	res.JSON(&site)
	if site.Code != "OSL" || site.Status != gen.Active || site.Revision != 1 || site.Address == nil || *site.Address.Country != "NO" {
		t.Errorf("site = %+v, want OSL active at revision 1 with the address", site)
	}
	root := getLocation(t, c, site.RootLocationId)
	if root.Location.Kind != gen.InventoryLocationKindSite || root.Location.FullCode != "OSL" || root.Location.Depth != 0 ||
		root.Location.ParentId != nil || len(root.Path) != 0 {
		t.Errorf("root = %+v, want kind site, full code OSL, depth 0", root.Location)
	}

	createSite(t, c, "BGO")
	res = c.Do(http.MethodGet, sitePath(site.Id), nil)
	mustStatus(t, res, http.StatusOK, "GET /sites/{id}")
	mustStatus(t, c.Do(http.MethodGet, sitePath(999999), nil), http.StatusNotFound, "GET /sites/{unknown}")

	var list gen.InventorySiteList
	res = c.Do(http.MethodGet, sitesPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /sites")
	res.JSON(&list)
	if len(list.Sites) != 2 || list.Sites[0].Code != "BGO" || list.Sites[1].Code != "OSL" {
		t.Errorf("sites = %+v, want BGO then OSL", list.Sites)
	}

	// Archived, it leaves the default list for ?status=archived.
	res = c.Do(http.MethodPut, sitePath(site.Id), gen.InventorySiteUpdate{Code: "OSL", Name: "Oslo", Kind: gen.Warehouse, Status: gen.Archived}, ifMatch(site.Revision))
	mustStatus(t, res, http.StatusOK, "PUT /sites/{id} archived")
	res = c.Do(http.MethodGet, sitesPath+"?status=archived", nil)
	mustStatus(t, res, http.StatusOK, "GET /sites?status=archived")
	res.JSON(&list)
	if len(list.Sites) != 1 || list.Sites[0].Code != "OSL" || list.Sites[0].Status != gen.Archived {
		t.Errorf("archived sites = %+v, want OSL", list.Sites)
	}
	if got := getLocation(t, c, site.RootLocationId).Location.Status; got != gen.Archived {
		t.Errorf("root status = %s, want archived with its site", got)
	}

	// Reactivated, the site and its root alone come back.
	var archived gen.InventorySite
	res.JSON(&list)
	archived = list.Sites[0]
	res = c.Do(http.MethodPut, sitePath(site.Id), gen.InventorySiteUpdate{Code: "OSL", Name: "Oslo", Kind: gen.Warehouse, Status: gen.Active}, ifMatch(archived.Revision))
	mustStatus(t, res, http.StatusOK, "PUT /sites/{id} active")
	if got := getLocation(t, c, site.RootLocationId).Location.Status; got != gen.Active {
		t.Errorf("root status = %s, want active with its site", got)
	}
	// The archive's PUT replaced the site whole, without its address: a
	// change of its own beside the status.
	if kinds := eventKinds(t, h); !slices.Equal(kinds, []string{"site_created", "site_created", "site_changed", "site_archived", "site_reactivated"}) {
		t.Errorf("events = %v", kinds)
	}
}

// TestSites_Refusals is every refusal of a site's own (I1, I6, I7): a code
// that is not one, or reserved for labels; a code another site has; a stale
// If-Match; an archive while the site holds stock; a delete of a site with
// history.
func TestSites_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalog(h)
	c, _ := signIn(t, h, everything...)

	for _, code := range []string{"", "A-B", "ÆØÅ", "ABCDEFGHIJKLMNOPQ", "LOC12"} {
		res := c.Do(http.MethodPost, sitesPath, gen.InventorySiteInput{Code: code, Name: "X", Kind: gen.Store})
		expectRefusal(t, res, http.StatusBadRequest, "code_invalid", "POST /sites code "+code)
	}
	res := c.Do(http.MethodPost, sitesPath, gen.InventorySiteInput{Code: "X", Name: " ", Kind: "depot"})
	mustStatus(t, res, http.StatusBadRequest, "POST /sites with no name and no kind")
	if p := problemOf(t, res); p.Errors == nil || len((*p.Errors)["name"]) == 0 || len((*p.Errors)["kind"]) == 0 {
		t.Errorf("errors = %s, want name and kind", res.Body)
	}
	res = c.Do(http.MethodPost, sitesPath, map[string]any{"code": "X", "name": "X", "kind": "store", "colour": "red"})
	mustStatus(t, res, http.StatusBadRequest, "POST /sites with an unknown property")

	osl := createSite(t, c, "OSL")
	res = c.Do(http.MethodPost, sitesPath, gen.InventorySiteInput{Code: "osl", Name: "Again", Kind: gen.Store})
	expectRefusal(t, res, http.StatusConflict, "site_code_taken", "POST /sites taken")
	bgo := createSite(t, c, "BGO")
	res = c.Do(http.MethodPut, sitePath(bgo.Id), gen.InventorySiteUpdate{Code: "OSL", Name: "B", Kind: gen.Store, Status: gen.Active}, ifMatch(bgo.Revision))
	expectRefusal(t, res, http.StatusConflict, "site_code_taken", "PUT /sites to a taken code")
	res = c.Do(http.MethodPut, sitePath(bgo.Id), gen.InventorySiteUpdate{Code: "BGO", Name: "B", Kind: gen.Store, Status: gen.Active}, ifMatch(bgo.Revision+1))
	expectRefusal(t, res, http.StatusConflict, "revision_conflict", "PUT /sites stale")
	res = c.Do(http.MethodPut, sitePath(bgo.Id), gen.InventorySiteUpdate{Code: "BGO", Name: "B", Kind: gen.Store, Status: gen.Active}, ifMatch(0))
	mustStatus(t, res, http.StatusBadRequest, "PUT /sites with If-Match 0")
	res = c.Do(http.MethodPut, sitePath(999999), gen.InventorySiteUpdate{Code: "BGO", Name: "B", Kind: gen.Store, Status: gen.Active}, ifMatch(1))
	mustStatus(t, res, http.StatusNotFound, "PUT /sites/{unknown}")

	receive(t, c, osl.Id, osl.RootLocationId, receiptLine(varBolt, 5))
	res = c.Do(http.MethodPut, sitePath(osl.Id), gen.InventorySiteUpdate{Code: "OSL", Name: "Oslo", Kind: gen.Warehouse, Status: gen.Archived}, ifMatch(osl.Revision))
	p := expectRefusal(t, res, http.StatusConflict, "location_has_stock", "archive a site with stock")
	if p.Detail == nil || *p.Detail == "" {
		t.Errorf("detail = %v, want the quantity and lines", p.Detail)
	}
	expectRefusal(t, c.Do(http.MethodDelete, sitePath(osl.Id), nil), http.StatusConflict, "location_has_history", "DELETE a site with history")
	mustStatus(t, c.Do(http.MethodDelete, sitePath(999999), nil), http.StatusNotFound, "DELETE /sites/{unknown}")
	assertLedgerConsistent(t, h)
}

// TestSites_RecodeAndDelete is a site's new code followed through every full
// code in it (I4), and a site with no history deleted with its locations.
func TestSites_RecodeAndDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, locationsManager...)
	site := createSite(t, c, "OSL")
	a := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	shelf := createLocation(t, c, a.Id, gen.InventoryLocationKindShelf, "1")

	res := c.Do(http.MethodPut, sitePath(site.Id), gen.InventorySiteUpdate{Code: "OS2", Name: "Oslo 2", Kind: gen.Store, Status: gen.Active}, ifMatch(site.Revision))
	mustStatus(t, res, http.StatusOK, "PUT /sites/{id} recode")
	var after gen.InventorySite
	res.JSON(&after)
	if after.Code != "OS2" || after.Revision != 2 || after.Kind != gen.Store {
		t.Errorf("site = %+v, want OS2 at revision 2", after)
	}
	if d := getLocation(t, c, shelf.Id); d.Location.FullCode != "OS2-A-1" || d.Path[0].FullCode != "OS2" || d.Site.Code != "OS2" {
		t.Errorf("shelf = %+v, path %+v, want OS2-A-1 under OS2", d.Location, d.Path)
	}
	if root := getLocation(t, c, site.RootLocationId).Location; root.Code != "OS2" {
		t.Errorf("root code = %s, want OS2", root.Code)
	}

	var list gen.InventoryLocationList
	res = c.Do(http.MethodGet, sitePath(site.Id)+"/locations", nil)
	mustStatus(t, res, http.StatusOK, "GET /sites/{id}/locations")
	res.JSON(&list)
	codes := []string{}
	for _, l := range list.Locations {
		codes = append(codes, l.FullCode)
	}
	if !slices.Equal(codes, []string{"OS2", "OS2-A", "OS2-A-1"}) {
		t.Errorf("locations = %v, want the tree by full code", codes)
	}
	mustStatus(t, c.Do(http.MethodGet, sitePath(999999)+"/locations", nil), http.StatusNotFound, "GET /sites/{unknown}/locations")

	mustStatus(t, c.Do(http.MethodDelete, sitePath(site.Id), nil), http.StatusNoContent, "DELETE /sites/{id}")
	if n := h.Count(t, `SELECT count(*) FROM inventory.locations`); n != 0 {
		t.Errorf("%d locations left, want none", n)
	}
	mustStatus(t, c.Do(http.MethodGet, sitePath(site.Id), nil), http.StatusNotFound, "GET a deleted site")
}

// TestSites_NeedTheirKeys is the router's rule (§5): locations-view reads,
// locations-manage writes.
func TestSites_NeedTheirKeys(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer, _ := signIn(t, h, locationsViewer...)
	none, _ := signIn(t, h)
	mustStatus(t, none.Do(http.MethodGet, sitesPath, nil), http.StatusForbidden, "GET /sites with no key")
	mustStatus(t, viewer.Do(http.MethodGet, sitesPath, nil), http.StatusOK, "GET /sites as a viewer")
	res := viewer.Do(http.MethodPost, sitesPath, gen.InventorySiteInput{Code: "X", Name: "X", Kind: gen.Store})
	mustStatus(t, res, http.StatusForbidden, "POST /sites as a viewer")
}
