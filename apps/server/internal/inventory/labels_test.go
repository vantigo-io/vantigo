package inventory_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

const labelsPath = "/api/v1/inventory/labels"

// TestLabels_Print is POST /labels (I7): a PDF named for its layout, a
// subtree expanded to its active locations, a location that does not exist
// refused (400 location_not_found), more than 1000 labels refused
// (labels_too_many), and locations-view enough to print.
func TestLabels_Print(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, locationsManager...)
	c, _ := signIn(t, h, locationsViewer...)
	site := createSite(t, admin, "OSL")
	aisle := createLocation(t, admin, site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	shelf := createLocation(t, admin, aisle.Id, gen.InventoryLocationKindShelf, "1")
	gone := createLocation(t, admin, aisle.Id, gen.InventoryLocationKindShelf, "2")
	mustStatus(t, putLocation(admin, gone, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusOK, "archive A-2")

	res := c.Do(http.MethodPost, labelsPath, gen.InventoryLabelsInput{LocationIds: []int64{aisle.Id}, Layout: gen.L7160, Subtree: ptr(true)})
	mustStatus(t, res, http.StatusOK, "print a subtree")
	if ct := res.Header("Content-Type"); ct != "application/pdf" || !bytes.HasPrefix(res.Body, []byte("%PDF")) {
		t.Errorf("content type %q, body %.8q", ct, res.Body)
	}
	if d := res.Header("Content-Disposition"); !strings.Contains(d, "location-labels-l7160.pdf") {
		t.Errorf("disposition = %q", d)
	}
	res = c.Do(http.MethodPost, labelsPath, gen.InventoryLabelsInput{LocationIds: []int64{shelf.Id, gone.Id}, Layout: gen.L7163})
	mustStatus(t, res, http.StatusOK, "print two, one archived")

	expectRefusal(t, c.Do(http.MethodPost, labelsPath, gen.InventoryLabelsInput{LocationIds: []int64{shelf.Id, 999999}, Layout: gen.L7160}),
		http.StatusBadRequest, "location_not_found", "a location that does not exist")
	many := make([]int64, 1001)
	for i := range many {
		many[i] = int64(i + 1)
	}
	expectRefusal(t, c.Do(http.MethodPost, labelsPath, gen.InventoryLabelsInput{LocationIds: many, Layout: gen.L7160}),
		http.StatusBadRequest, "labels_too_many", "1001 labels")
	mustStatus(t, c.Do(http.MethodPost, labelsPath, map[string]any{"locationIds": []int64{shelf.Id}, "layout": "a4"}),
		http.StatusBadRequest, "an unknown layout")
	none, _ := signIn(t, h, "inventory:stock-view")
	mustStatus(t, none.Do(http.MethodPost, labelsPath, gen.InventoryLabelsInput{LocationIds: []int64{shelf.Id}, Layout: gen.L7160}),
		http.StatusForbidden, "print without locations-view")
}

// TestLabels_SubtreeTooMany is labels_too_many counted after the subtree's
// expansion.
func TestLabels_SubtreeTooMany(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, locationsManager...)
	site := createSite(t, c, "OSL")
	for _, aisles := range [][2]string{{"01", "10"}, {"11", "12"}} {
		res := c.Do(http.MethodPost, bulkPath, gen.InventoryBulkCreateInput{ParentId: site.RootLocationId, Levels: []gen.InventoryBulkLevel{
			{Kind: gen.InventoryLocationKindAisle, From: aisles[0], To: ptr(aisles[1])},
			{Kind: gen.InventoryLocationKindShelf, From: "01", To: ptr("90")},
		}})
		mustStatus(t, res, http.StatusCreated, "bulk create aisles "+aisles[0])
	}
	p := expectRefusal(t, c.Do(http.MethodPost, labelsPath, gen.InventoryLabelsInput{LocationIds: []int64{site.RootLocationId}, Layout: gen.L7160, Subtree: ptr(true)}),
		http.StatusBadRequest, "labels_too_many", "a site of 1092 locations")
	if p.Detail == nil || !strings.Contains(*p.Detail, "more than 1000 labels") {
		t.Errorf("detail = %v, want more than 1000 labels — the read stops one past the cap", p.Detail)
	}
}
