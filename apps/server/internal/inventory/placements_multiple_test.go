package inventory_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// TestPlacement_Multiple is manufacturing phase 2 M105: a placement's
// multiple is set and answered, more than 0, rounds the low-stock list's
// suggestion up before the packaging, and is set by the import's optional
// sixth column, a file without it leaving it as it is.
func TestPlacement_Multiple(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c) // OSL: 20 bolts on 1, 10 nuts on 2

	zero := 0.0
	in := reorderAt(20, 30)
	in.Multiple = &zero
	res := putPlacement(c, varBolt, s.osl.Id, in, createOnly)
	mustStatus(t, res, http.StatusBadRequest, "a multiple of 0")
	four := 4.0
	in.Multiple = &four
	p := setPlacement(t, c, varBolt, s.osl.Id, in)
	if p.Multiple == nil || *p.Multiple != 4 || !p.Low {
		t.Fatalf("placement = %+v", p)
	}
	// 20 ≤ 20: needed 10 to the maximum, rounded up to 12.
	for _, r := range lowStock(t, c, "?q=bolt").Rows {
		if r.Site.Id == s.osl.Id && (r.Needed != 10 || r.Suggested != 12 || r.Multiple == nil || *r.Multiple != 4) {
			t.Errorf("low row = %+v", r)
		}
	}

	file := "sku;site;primary_location;reorder_point;maximum;multiple\nBOLT-M8;OSL;;20;30;6\n"
	placementImportResult(t, importPlacements(c, true, file), http.StatusCreated, "import with a multiple")
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE variant_id = $1 AND multiple = 6`, varBolt); n != 1 {
		t.Errorf("the import's multiple was not set")
	}
	bad := "sku;site;primary_location;reorder_point;maximum;multiple\nBOLT-M8;OSL;;20;30;0\n"
	if r := placementImportResult(t, importPlacements(c, false, bad), http.StatusOK, "a multiple of 0"); r.Ok ||
		r.Rows[0].Outcome != gen.InventoryPlacementImportOutcomeQuantityInvalid {
		t.Errorf("import = %+v", r)
	}
	// A file without the column leaves the multiple as it is, and a
	// placement it creates has none.
	placementImportResult(t, importPlacements(c, true, "sku;site;primary_location;reorder_point;maximum\nBOLT-M8;OSL;;25;35\nNUT-M8;OSL;;3;9\n"),
		http.StatusCreated, "import without the column")
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE variant_id = $1 AND multiple = 6 AND reorder_point = 25`, varBolt); n != 1 {
		t.Errorf("a file without the column changed the multiple")
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE variant_id = $1 AND multiple IS NULL`, varNut); n != 1 {
		t.Errorf("a new placement from a file without the column has a multiple")
	}
}

// TestStock_Placements is contracts.Stock.Placements (M106): every
// placement with a reorder point at the site, with usable on hand, reserved
// and incoming, by variant; none at another site, none without a reorder
// point.
func TestStock_Placements(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	setPlacement(t, c, varBolt, s.osl.Id, reorderAt(5, 40))
	setPlacement(t, c, varNut, s.bgo.Id, reorderAt(2))
	setPlacement(t, c, varWasher, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &s.shelf1.Id})
	mo := contracts.StockSource{Module: "manufacturing", Kind: "order", ID: 3}
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return ledgerOf(h).ReserveForProduction(t.Context(), tx, mo, s.osl.Id, testToday,
			[]contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "5"}})
	}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	mustSend(t, c, createTransfer(t, c, s, line(varNut, 4)), s.shelf2.Id)

	osl, err := stockOf(h).Placements(t.Context(), s.osl.Id)
	if err != nil {
		t.Fatalf("Placements: %v", err)
	}
	want := []contracts.StockPlacement{{VariantID: varBolt, ReorderPoint: "5", Maximum: "40", OnHand: "20", Reserved: "5", Incoming: "0"}}
	if !slices.Equal(osl, want) {
		t.Errorf("OSL = %+v, want %+v", osl, want)
	}
	bgo, err := stockOf(h).Placements(t.Context(), s.bgo.Id)
	if err != nil {
		t.Fatalf("Placements: %v", err)
	}
	want = []contracts.StockPlacement{{VariantID: varNut, ReorderPoint: "2", OnHand: "0", Reserved: "0", Incoming: "4"}}
	if !slices.Equal(bgo, want) {
		t.Errorf("BGO = %+v, want %+v", bgo, want)
	}
}
