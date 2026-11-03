package inventory_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The bolts' packagings most entry tests use: a box of 15 and a Euro pallet
// of 600 (40 boxes), which has its own barcode.
const (
	pkgBox    int32 = 11
	pkgPallet int32 = 12
)

func boxAndPallet() []contracts.PackagingEntry {
	barcode := "07040000000024"
	return []contracts.PackagingEntry{
		{ID: pkgBox, Name: "Box", Kind: "case", Quantity: "15.000", SortOrder: 1},
		{ID: pkgPallet, Name: "Euro pallet", Kind: "pallet", Quantity: "600.000", Barcode: &barcode, SortOrder: 2},
	}
}

// entered is an entry of packagings — packaging id, count, … — and a loose
// rest.
func entered(loose *float64, pairs ...int32) *gen.InventoryEntered {
	e := &gen.InventoryEntered{Packagings: []gen.InventoryEnteredPackaging{}, Loose: loose}
	for i := 0; i+1 < len(pairs); i += 2 {
		e.Packagings = append(e.Packagings, gen.InventoryEnteredPackaging{PackagingId: pairs[i], Count: pairs[i+1]})
	}
	return e
}

// postReceipt posts a receipt of lines to site's location to.
func postReceipt(c *modtest.Client, site, to int64, lines ...gen.InventoryReceiptLine) *modtest.Response {
	return c.Do(http.MethodPost, receiptsPath, gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: site, ToLocationId: to, Lines: lines})
}

// TestEntered_Postings is I25 on every posting: a receipt of 3 pallets, 2
// boxes and 4 loose is 1,834 in the base unit, kept with its snapshot; an
// adjustment's entry adds under change, takes away with decrease, and is the
// new on hand under set; a move's entry moves; and the variant names its
// packagings wherever it is answered.
func TestEntered_Postings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	h.catalog.setPackagings(varBolt, boxAndPallet()...)
	shelf, other := s.shelf1.Id, s.shelf2.Id

	res := postReceipt(c, s.site.Id, shelf, gen.InventoryReceiptLine{VariantId: varBolt, Entered: entered(qty(4), pkgPallet, 3, pkgBox, 2)})
	mustStatus(t, res, http.StatusCreated, "a receipt entered in packagings")
	var m gen.InventoryMovement
	res.JSON(&m)
	l := m.Lines[0]
	if l.Quantity != 1834 || l.Entered == nil || l.Entered.Loose == nil || *l.Entered.Loose != 4 || len(l.Entered.Packagings) != 2 {
		t.Fatalf("line = %+v, want 1834 entered as 3 pallets, 2 boxes and 4", l)
	}
	if p := l.Entered.Packagings[0]; p.Name != "Euro pallet" || p.Count != 3 || p.Quantity != 600 || p.PackagingId != pkgPallet {
		t.Errorf("the pallets' snapshot = %+v", p)
	}
	if l.Variant.Packagings == nil || len(*l.Variant.Packagings) != 2 || (*l.Variant.Packagings)[1].Quantity != 600 {
		t.Errorf("the variant's packagings = %+v", l.Variant.Packagings)
	}
	if got := onHand(t, h, varBolt, shelf); got != "1834.000" {
		t.Errorf("bolts = %q, want 1834", got)
	}

	// Change adds, decrease takes away, set sets.
	res = adjust(c, s.site.Id,
		gen.InventoryAdjustmentLine{VariantId: varBolt, LocationId: shelf, Mode: gen.Change, Entered: entered(nil, pkgBox, 1)},
		gen.InventoryAdjustmentLine{VariantId: varBolt, LocationId: shelf, Mode: gen.Change, Entered: entered(qty(0.5), pkgBox, 2), Decrease: ptr(true)},
		gen.InventoryAdjustmentLine{VariantId: varBolt, LocationId: other, Mode: gen.Set, Entered: entered(nil, pkgBox, 2)},
	)
	mustStatus(t, res, http.StatusCreated, "an adjustment entered in packagings")
	m = gen.InventoryMovement{}
	res.JSON(&m)
	if len(m.Lines) != 3 || m.Lines[0].To == nil || m.Lines[0].Quantity != 15 || m.Lines[1].From == nil || m.Lines[1].Quantity != 30.5 ||
		m.Lines[2].To == nil || m.Lines[2].Quantity != 30 {
		t.Fatalf("lines = %+v, want +15, -30.5 and a set to 30", m.Lines)
	}
	if got := onHand(t, h, varBolt, shelf); got != "1818.500" {
		t.Errorf("bolts = %q, want 1818.5", got)
	}
	// A move of a box, read back with its entry.
	res = move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varBolt, FromLocationId: shelf, ToLocationId: other, Entered: entered(nil, pkgBox, 1)})
	mustStatus(t, res, http.StatusCreated, "a move entered in packagings")
	m = gen.InventoryMovement{}
	res.JSON(&m)
	res = c.Do(http.MethodGet, movementPath(m.Id), nil)
	mustStatus(t, res, http.StatusOK, "GET the move")
	m = gen.InventoryMovement{}
	res.JSON(&m)
	if l := m.Lines[0]; l.Quantity != 15 || l.Entered == nil || l.Entered.Packagings[0].Name != "Box" || l.Entered.Loose != nil {
		t.Errorf("the move's line = %+v", l)
	}
	if got := onHand(t, h, varBolt, other); got != "45.000" {
		t.Errorf("bolts on shelf 2 = %q, want 45", got)
	}

	// Scanning the pallet's barcode finds the variant, packagings and all.
	res = c.Do(http.MethodGet, lookupPath+"?q=07040000000024", nil)
	mustStatus(t, res, http.StatusOK, "look up a packaging's barcode")
	var found gen.InventoryLookupResult
	res.JSON(&found)
	if len(found.Matches) != 1 || found.Matches[0].Variant == nil || found.Matches[0].Variant.Id != varBolt || found.Matches[0].Variant.Packagings == nil {
		t.Errorf("matches = %+v, want the bolt", found.Matches)
	}
	assertLedgerConsistent(t, h)
}

// TestEntered_PostingTimeQuantities is I25's "as they are at posting": the
// box held 15 when the form was filled and 12 when it was posted, so the
// posting takes 12 and its snapshot says so.
func TestEntered_PostingTimeQuantities(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	h.catalog.setPackagings(varBolt, boxAndPallet()...)
	res := c.Do(http.MethodGet, variantsPath+"?q=BOLT", nil)
	mustStatus(t, res, http.StatusOK, "the picker")
	var list gen.InventoryVariantList
	res.JSON(&list)
	if p := (*list.Variants[0].Packagings)[0]; p.Quantity != 15 {
		t.Fatalf("the box = %+v, want 15 when the form is filled", p)
	}
	packagings := boxAndPallet()
	packagings[0].Quantity = "12.000"
	h.catalog.setPackagings(varBolt, packagings...)

	res = postReceipt(c, s.site.Id, s.shelf1.Id, gen.InventoryReceiptLine{VariantId: varBolt, Entered: entered(nil, pkgBox, 2)})
	mustStatus(t, res, http.StatusCreated, "the receipt")
	var m gen.InventoryMovement
	res.JSON(&m)
	if l := m.Lines[0]; l.Quantity != 24 || l.Entered.Packagings[0].Quantity != 12 {
		t.Errorf("line = %+v, want 2 boxes of 12", l)
	}
	assertLedgerConsistent(t, h)
}

// TestEntered_Refusals is every refusal of an entry, each on its line: both
// or neither of quantity and entered, a packaging named twice, a count below
// 1, decrease with a quantity or under set (entered_invalid), a packaging the
// variant does not have (packaging_not_found), and an entry coming to 0 where
// more than 0 is needed (quantity_invalid). Nothing is posted.
func TestEntered_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	h.catalog.setPackagings(varBolt, boxAndPallet()...)
	shelf := s.shelf1.Id

	res := postReceipt(c, s.site.Id, shelf,
		gen.InventoryReceiptLine{VariantId: varBolt, Quantity: qty(1), Entered: entered(nil, pkgBox, 1)},
		gen.InventoryReceiptLine{VariantId: varBolt},
		gen.InventoryReceiptLine{VariantId: varBolt, Entered: entered(nil, pkgBox, 1, pkgBox, 2)},
		gen.InventoryReceiptLine{VariantId: varBolt, Entered: entered(nil, pkgBox, 0)},
		gen.InventoryReceiptLine{VariantId: varNut, Entered: entered(nil, pkgBox, 1)},
		gen.InventoryReceiptLine{VariantId: varBolt, Entered: entered(nil)},
		gen.InventoryReceiptLine{VariantId: varBolt, Entered: entered(qty(0.0001), pkgBox, 1)},
	)
	p := expectRefusal(t, res, http.StatusBadRequest, "entered_invalid", "a receipt of bad entries")
	want := []string{"0:entered_invalid", "1:entered_invalid", "2:entered_invalid", "3:entered_invalid", "4:packaging_not_found",
		"5:quantity_invalid", "6:quantity_invalid"}
	if got := lineCodes(p); !slices.Equal(got, want) {
		t.Errorf("lines = %v, want %v", got, want)
	}
	res = adjust(c, s.site.Id,
		gen.InventoryAdjustmentLine{VariantId: varBolt, LocationId: shelf, Mode: gen.Change, Quantity: qty(1), Decrease: ptr(true)},
		gen.InventoryAdjustmentLine{VariantId: varBolt, LocationId: shelf, Mode: gen.Set, Entered: entered(nil, pkgBox, 1), Decrease: ptr(true)},
		gen.InventoryAdjustmentLine{VariantId: varBolt, LocationId: shelf, Mode: gen.Change, Entered: entered(nil, 99, 1)},
	)
	p = expectRefusal(t, res, http.StatusBadRequest, "entered_invalid", "an adjustment of bad entries")
	if got := lineCodes(p); !slices.Equal(got, []string{"0:entered_invalid", "1:entered_invalid", "2:packaging_not_found"}) {
		t.Errorf("lines = %v", got)
	}
	// Taking away more than lies there is the balance's refusal, as a
	// quantity's would be.
	res = adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varBolt, LocationId: shelf, Mode: gen.Change, Entered: entered(nil, pkgBox, 1), Decrease: ptr(true)})
	expectRefusal(t, res, http.StatusConflict, "insufficient_stock", "a decrease below zero")

	if n := h.Count(t, `SELECT count(*) FROM inventory.movements`); n != 0 {
		t.Errorf("%d movements, want none", n)
	}
	assertLedgerConsistent(t, h)
}
