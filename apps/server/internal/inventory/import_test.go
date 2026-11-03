package inventory_test

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const importPath = "/api/v1/inventory/imports/opening-balances"

// importCSV posts a file to the import of site, a commit when commit is true.
func importCSV(c *modtest.Client, site int64, commit bool, file string) *modtest.Response {
	path := importPath + "?siteId=" + strconv.FormatInt(site, 10)
	if commit {
		path += "&dryRun=false"
	}
	return c.Do(http.MethodPost, path, nil, modtest.RawBody("text/csv", []byte(file)))
}

// importResult decodes an import's answer.
func importResult(t *testing.T, res *modtest.Response, want int, what string) gen.InventoryImportResult {
	t.Helper()
	mustStatus(t, res, want, what)
	var out gen.InventoryImportResult
	res.JSON(&out)
	return out
}

// outcomes is an import's rows as row:outcome.
func outcomes(r gen.InventoryImportResult) []string {
	out := make([]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		out = append(out, fmt.Sprintf("%d:%s", row.Row, row.Outcome))
	}
	return out
}

// TestImport_DryRunAndCommit is I32: a dry run answers each row's outcome
// and posts nothing; a commit with a failing row posts nothing; a commit of a
// good file posts one opening_balance adjustment of every row; the same file
// again is the same movement.
func TestImport_DryRunAndCommit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, stockAdjuster...)
	admin, _ := signIn(t, h, locationsManager...)
	s := newLedgerSite(t, h, admin)
	archived := createLocation(t, admin, s.site.RootLocationId, gen.InventoryLocationKindShelf, "9")
	mustStatus(t, putLocation(admin, archived, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusOK, "archive OSL-9")

	// A comma file with a decimal comma quoted, as a spreadsheet writes it.
	bad := "Location,SKU,Quantity\nosl-1,bolt-m8,\"12,5\"\nOSL-404,BOLT-M8,1\nOSL-9,BOLT-M8,1\nOSL-1,NOPE,1\nOSL-1,INSTALL,1\nOSL-2,NUT-M8,-3\nOSL-2,NUT-M8,1.2345\nOSL-2,NUT-M8,1 000\n"
	r := importResult(t, importCSV(c, s.site.Id, false, bad), http.StatusOK, "dry run")
	want := []string{"2:ok", "3:location_not_found", "4:location_not_found", "5:variant_not_found", "6:variant_not_stockable",
		"7:quantity_invalid", "8:quantity_invalid", "9:quantity_invalid"}
	if got := outcomes(r); r.Ok || r.MovementId != nil || !slices.Equal(got, want) {
		t.Errorf("dry run = %v (ok %v), want %v", got, r.Ok, want)
	}

	// A decimal comma unquoted splits the quantity into a fourth cell: the row
	// is refused, not imported as 2; blank trailing cells are harmless. The
	// contract's CSV decoder rejects ragged rows, so the exchange skips it.
	ragged := "location,sku,quantity\nOSL-1,BOLT-M8,2,5\nOSL-2,NUT-M8,4,,\n"
	rr := importResult(t, c.Do(http.MethodPost, importPath+"?siteId="+strconv.FormatInt(s.site.Id, 10), nil,
		modtest.RawBody("text/csv", []byte(ragged)), modtest.SkipContract("a row with more cells than the header")), http.StatusOK, "dry run of ragged rows")
	if got := outcomes(rr); rr.Ok || !slices.Equal(got, []string{"2:quantity_invalid", "3:ok"}) {
		t.Errorf("ragged = %v (ok %v), want the extra cell refused", got, rr.Ok)
	}
	if d := rr.Rows[0].Detail; d == nil || !strings.Contains(*d, "more cells than the header") {
		t.Errorf("ragged detail = %v, want more cells than the header", d)
	}
	if r.Rows[0].Location != "osl-1" || r.Rows[0].Quantity != "12,5" || r.Rows[1].Detail == nil {
		t.Errorf("rows = %+v, want them as written, with details", r.Rows)
	}
	r = importResult(t, importCSV(c, s.site.Id, true, bad), http.StatusOK, "a commit with failing rows")
	if r.Ok || r.MovementId != nil {
		t.Errorf("commit = %+v, want refused", r)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.movements`); n != 0 {
		t.Fatalf("%d movements, want none", n)
	}

	good := "sku,quantity,location\nBOLT-M8,12.5,OSL-1\nNUT-M8,3,OSL-2\nBOLT-M8,1,OSL-1\n"
	r = importResult(t, importCSV(c, s.site.Id, false, good), http.StatusOK, "dry run of a good file")
	if !r.Ok || r.MovementId != nil {
		t.Errorf("dry run = %+v", r)
	}
	r = importResult(t, importCSV(c, s.site.Id, true, good), http.StatusCreated, "commit")
	if !r.Ok || r.MovementId == nil {
		t.Fatalf("commit = %+v", r)
	}
	res := c.Do(http.MethodGet, movementPath(*r.MovementId), nil)
	mustStatus(t, res, http.StatusOK, "the import's movement")
	var m gen.InventoryMovement
	res.JSON(&m)
	if m.Kind != gen.Adjustment || m.Reason == nil || *m.Reason != gen.InventoryAdjustmentReasonOpeningBalance || len(m.Lines) != 3 {
		t.Errorf("movement = %+v, want one opening_balance adjustment of three lines", m)
	}
	if onHand(t, h, varBolt, s.shelf1.Id) != "13.500" || onHand(t, h, varNut, s.shelf2.Id) != "3.000" {
		t.Errorf("balances = %q, %q", onHand(t, h, varBolt, s.shelf1.Id), onHand(t, h, varNut, s.shelf2.Id))
	}
	again := importResult(t, importCSV(c, s.site.Id, true, good), http.StatusOK, "the same file again")
	if again.MovementId == nil || *again.MovementId != *r.MovementId {
		t.Errorf("again = %+v, want movement %d", again, *r.MovementId)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.movements`); n != 1 {
		t.Errorf("%d movements, want 1", n)
	}
	assertLedgerConsistent(t, h)
}

// TestImport_Refusals is the file the import does not read: a wrong header,
// more than 5000 rows, an empty file, an unknown site, and the key it needs.
func TestImport_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, stockAdjuster...)
	admin, _ := signIn(t, h, locationsManager...)
	s := newLedgerSite(t, h, admin)
	for what, file := range map[string]string{
		"a header without quantity": "location;sku\nOSL-1;BOLT-M8\n",
		"a header with more":        "location;sku;quantity;note\nOSL-1;BOLT-M8;1;x\n",
		"an empty file":             "",
	} {
		res := importCSV(c, s.site.Id, false, file)
		mustStatus(t, res, http.StatusBadRequest, what)
		if p := problemOf(t, res); p.Errors == nil || len((*p.Errors)["file"]) == 0 {
			t.Errorf("%s: errors = %s, want file", what, res.Body)
		}
	}
	var b strings.Builder
	b.WriteString("location;sku;quantity\n")
	for range 5001 {
		b.WriteString("OSL-1;BOLT-M8;1\n")
	}
	mustStatus(t, importCSV(c, s.site.Id, false, b.String()), http.StatusBadRequest, "5001 rows")
	mustStatus(t, importCSV(c, 999999, false, "location;sku;quantity\nOSL-1;BOLT-M8;1\n"), http.StatusBadRequest, "an unknown site")
	viewer, _ := signIn(t, h, stockViewer...)
	mustStatus(t, importCSV(viewer, s.site.Id, false, "location;sku;quantity\n"), http.StatusForbidden, "import without stock-adjust")
}

// TestImport_FiveThousandRows is the cap's other side: 5000 rows post as one
// movement, beyond the API's 500 lines.
func TestImport_FiveThousandRows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, stockAdjuster...)
	admin, _ := signIn(t, h, locationsManager...)
	s := newLedgerSite(t, h, admin)
	var b strings.Builder
	b.WriteString("location;sku;quantity\n")
	for range 5000 {
		b.WriteString("OSL-1;BOLT-M8;1\n")
	}
	r := importResult(t, importCSV(c, s.site.Id, true, b.String()), http.StatusCreated, "5000 rows")
	if r.MovementId == nil || len(r.Rows) != 5000 {
		t.Fatalf("result = %d rows, movement %v", len(r.Rows), r.MovementId)
	}
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "5000.000" {
		t.Errorf("bolts = %q, want 5000", got)
	}
	assertLedgerConsistent(t, h)
}
