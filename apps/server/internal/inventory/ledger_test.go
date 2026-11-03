package inventory_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// ledgerSite is a site with two shelves, the place most ledger tests post to.
type ledgerSite struct {
	site     gen.InventorySite
	shelf1   gen.InventoryLocation
	shelf2   gen.InventoryLocation
	elsewhre gen.InventorySite
}

func newLedgerSite(t *testing.T, h *harness, c *modtest.Client) ledgerSite {
	t.Helper()
	seedCatalog(h)
	site := createSite(t, c, "OSL")
	return ledgerSite{
		site:     site,
		shelf1:   createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindShelf, "1"),
		shelf2:   createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindShelf, "2"),
		elsewhre: createSite(t, c, "BGO"),
	}
}

// adjust posts an adjustment and answers the response.
func adjust(c *modtest.Client, site int64, lines ...gen.InventoryAdjustmentLine) *modtest.Response {
	return c.Do(http.MethodPost, adjustmentsPath, gen.InventoryAdjustmentInput{
		ClientRequestId: uuid.New(), SiteId: site, Reason: gen.InventoryAdjustmentReasonCorrection, Lines: lines,
	})
}

// move posts a move and answers the response.
func move(c *modtest.Client, site int64, lines ...gen.InventoryMoveLine) *modtest.Response {
	return c.Do(http.MethodPost, movesPath, gen.InventoryMoveInput{ClientRequestId: uuid.New(), SiteId: site, Lines: lines})
}

// change is an adjustment line in change mode.
func change(variant int32, location int64, delta float64) gen.InventoryAdjustmentLine {
	return gen.InventoryAdjustmentLine{VariantId: variant, LocationId: location, Mode: gen.Change, Quantity: qty(delta)}
}

// TestReceipt_Posts is a goods receipt (I15): the default location and a
// line's own, the supplier fields, the variants' snapshots, the poster, and
// the balances it leaves.
func TestReceipt_Posts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, receiver...)
	admin, _ := signIn(t, h, locationsManager...)
	s := newLedgerSite(t, h, admin)

	name, ref, note := "Bolt AS", "PO-77", "Pallet 1 of 2"
	res := c.Do(http.MethodPost, receiptsPath, gen.InventoryReceiptInput{
		ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: s.shelf1.Id, SupplierName: &name, SupplierReference: &ref, Note: &note,
		Lines: []gen.InventoryReceiptLine{
			{VariantId: varBolt, Quantity: qty(0.1)},
			{VariantId: varNut, Quantity: qty(99999999999.999), LocationId: &s.shelf2.Id},
			{VariantId: varDraft, Quantity: qty(3)},
		},
	})
	mustStatus(t, res, http.StatusCreated, "POST /movements/receipts")
	var m gen.InventoryMovement
	res.JSON(&m)
	if m.Kind != gen.Receipt || m.Site.Code != "OSL" || len(m.Lines) != 3 || *m.SupplierName != "Bolt AS" || *m.SupplierReference != "PO-77" ||
		*m.Note != note || m.PostedBy.UserId == nil || *m.PostedBy.UserId != user || m.Reason != nil {
		t.Errorf("movement = %+v", m)
	}
	l := m.Lines[1]
	if l.LineNo != 2 || l.Variant.Sku != "NUT-M8" || l.To == nil || l.To.Id != s.shelf2.Id || l.From != nil || l.Quantity != 99999999999.999 {
		t.Errorf("line 2 = %+v", l)
	}
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "0.100" {
		t.Errorf("bolts = %q, want exactly 0.100", got)
	}
	if got := onHand(t, h, varNut, s.shelf2.Id); got != "99999999999.999" {
		t.Errorf("nuts = %q, want the column's largest", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.movement_lines WHERE sku = 'BOLT-M8' AND product_name = 'Bolt M8' AND unit = 'pcs'`); n != 1 {
		t.Errorf("%d snapshotted bolt lines, want 1", n)
	}
	assertLedgerConsistent(t, h)
}

// TestPosting_LocksVariantsFirstInIDOrder is I22's lock order: one
// LockVariants call per posting, with every variant once, in id order.
func TestPosting_LocksVariantsFirstInIDOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varWasher, 1), receiptLine(varNut, 2), receiptLine(varBolt, 3), receiptLine(varNut, 4))
	calls := h.catalog.lockCalls()
	if len(calls) != 1 || !slices.Equal(calls[0], []int32{varBolt, varNut, varWasher}) {
		t.Errorf("LockVariants calls = %v, want one of [%d %d %d]", calls, varBolt, varNut, varWasher)
	}
	// A replay reads its key and locks nothing.
	body := gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: s.shelf1.Id, Lines: []gen.InventoryReceiptLine{receiptLine(varBolt, 1)}}
	mustStatus(t, c.Do(http.MethodPost, receiptsPath, body), http.StatusCreated, "first")
	mustStatus(t, c.Do(http.MethodPost, receiptsPath, body), http.StatusOK, "replay")
	if n := len(h.catalog.lockCalls()); n != 2 {
		t.Errorf("%d LockVariants calls, want 2: the replay locks nothing", n)
	}
	assertLedgerConsistent(t, h)
}

// TestPosting_LineRefusals is every line refusal of I9–I15, all answered at
// once with each line's index, and nothing written.
func TestPosting_LineRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	archived := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindShelf, "9")
	mustStatus(t, putLocation(c, archived, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusOK, "archive shelf 9")
	elsewhere := s.elsewhre.RootLocationId
	shelf := s.shelf1.Id

	res := c.Do(http.MethodPost, receiptsPath, gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: shelf, Lines: []gen.InventoryReceiptLine{
		{VariantId: 999, Quantity: qty(1)},
		{VariantId: varService, Quantity: qty(1)},
		{VariantId: varOld, Quantity: qty(1)},
		{VariantId: varBolt, Quantity: qty(1), LocationId: ptr(int64(999999))},
		{VariantId: varBolt, Quantity: qty(1), LocationId: &archived.Id},
		{VariantId: varBolt, Quantity: qty(1), LocationId: &elsewhere},
		{VariantId: varBolt, Quantity: qty(1.2345)},
		{VariantId: varBolt, Quantity: qty(0)},
		{VariantId: varBolt, Quantity: qty(-2)},
		{VariantId: varBolt, Quantity: qty(100000000000)},
		{VariantId: varBolt, Quantity: qty(1)},
	}})
	p := expectRefusal(t, res, http.StatusBadRequest, "variant_not_found", "a receipt with every line refusal")
	want := []string{"0:variant_not_found", "1:variant_not_stockable", "2:variant_discontinued", "3:location_not_found", "4:location_archived",
		"5:move_crosses_sites", "6:quantity_invalid", "7:quantity_invalid", "8:quantity_invalid", "9:quantity_invalid"}
	if got := lineCodes(p); !slices.Equal(got, want) {
		t.Errorf("lines = %v, want %v", got, want)
	}

	res = move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varBolt, FromLocationId: shelf, ToLocationId: shelf, Quantity: qty(1)})
	if got := lineCodes(expectRefusal(t, res, http.StatusBadRequest, "same_location", "a move to itself")); !slices.Equal(got, []string{"0:same_location"}) {
		t.Errorf("lines = %v", got)
	}
	res = move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varBolt, FromLocationId: shelf, ToLocationId: elsewhere, Quantity: qty(1)})
	expectRefusal(t, res, http.StatusBadRequest, "move_crosses_sites", "a move between sites")

	// Insufficient stock is a 409 naming the line, -5 then +5 fails at the
	// first, and every line is still answered.
	receive(t, c, s.site.Id, shelf, receiptLine(varBolt, 2))
	res = adjust(c, s.site.Id, change(varNut, shelf, -5), change(varNut, shelf, 5), change(varBolt, shelf, -3), change(varBolt, shelf, -2))
	p = expectRefusal(t, res, http.StatusConflict, "insufficient_stock", "taking more than lies there")
	if got := lineCodes(p); !slices.Equal(got, []string{"0:insufficient_stock", "2:insufficient_stock"}) {
		t.Errorf("lines = %v, want lines 0 and 2", got)
	}
	if d := (*p.Lines)[0].Detail; d != "NUT-M8 at OSL-1: 0 on hand, the line takes 5." {
		t.Errorf("detail = %q", d)
	}
	res = adjust(c, s.site.Id, change(varBolt, shelf, 0))
	expectRefusal(t, res, http.StatusBadRequest, "quantity_invalid", "a change of 0")

	// The request as a whole.
	res = adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varBolt, LocationId: shelf, Mode: gen.Set, Quantity: qty(2)})
	expectRefusal(t, res, http.StatusBadRequest, "nothing_to_post", "a set to what lies there")
	lines := make([]gen.InventoryMoveLine, 501)
	for i := range lines {
		lines[i] = gen.InventoryMoveLine{VariantId: varBolt, FromLocationId: shelf, ToLocationId: s.shelf2.Id, Quantity: qty(0.001)}
	}
	expectRefusal(t, move(c, s.site.Id, lines...), http.StatusBadRequest, "too_many_lines", "501 lines")
	res = move(c, 999999, gen.InventoryMoveLine{VariantId: varBolt, FromLocationId: shelf, ToLocationId: s.shelf2.Id, Quantity: qty(1)})
	mustStatus(t, res, http.StatusBadRequest, "a move in an unknown site")
	res = c.Do(http.MethodPost, receiptsPath, map[string]any{
		"clientRequestId": uuid.New(), "siteId": s.site.Id, "toLocationId": shelf,
		"lines": []map[string]any{{"variantId": varBolt, "quantity": 1, "entered": map[string]any{"packagings": []any{}}}},
	})
	if got := lineCodes(expectRefusal(t, res, http.StatusBadRequest, "entered_invalid", "a line with a quantity and entered")); !slices.Equal(got, []string{"0:entered_invalid"}) {
		t.Errorf("lines = %v", got)
	}
	res = c.Do(http.MethodPost, receiptsPath, map[string]any{
		"clientRequestId": uuid.New(), "siteId": s.site.Id, "toLocationId": shelf,
		"lines": []map[string]any{{"variantId": varBolt, "entered": map[string]any{"packagings": []any{}, "lose": 1}}},
	})
	mustStatus(t, res, http.StatusBadRequest, "an entry naming a property the contract does not")
	if p := problemOf(t, res); p.Errors == nil || len((*p.Errors)["lines[0].entered.lose"]) == 0 {
		t.Errorf("errors = %s, want lines[0].entered.lose", res.Body)
	}

	if n := h.Count(t, `SELECT count(*) FROM inventory.movements`); n != 1 {
		t.Errorf("%d movements, want only the one receipt", n)
	}
	assertLedgerConsistent(t, h)
}

// TestAdjustment_Modes is I15's two modes: change up and down, and set posted
// as the difference — a line at the balance dropped — with the reason kept.
func TestAdjustment_Modes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, stockAdjuster...)
	admin, _ := signIn(t, h, locationsManager...)
	s := newLedgerSite(t, h, admin)
	shelf := s.shelf1.Id

	res := c.Do(http.MethodPost, adjustmentsPath, gen.InventoryAdjustmentInput{
		ClientRequestId: uuid.New(), SiteId: s.site.Id, Reason: gen.InventoryAdjustmentReasonFound,
		Lines: []gen.InventoryAdjustmentLine{change(varBolt, shelf, 10), change(varNut, shelf, 4)},
	})
	mustStatus(t, res, http.StatusCreated, "change up")
	res = c.Do(http.MethodPost, adjustmentsPath, gen.InventoryAdjustmentInput{
		ClientRequestId: uuid.New(), SiteId: s.site.Id, Reason: gen.InventoryAdjustmentReasonCorrection,
		Lines: []gen.InventoryAdjustmentLine{
			{VariantId: varBolt, LocationId: shelf, Mode: gen.Set, Quantity: qty(7.5)},
			{VariantId: varNut, LocationId: shelf, Mode: gen.Set, Quantity: qty(4)},
			{VariantId: varWasher, LocationId: shelf, Mode: gen.Set, Quantity: qty(2)},
			change(varBolt, shelf, -0.5),
		},
	})
	mustStatus(t, res, http.StatusCreated, "set")
	var m gen.InventoryMovement
	res.JSON(&m)
	if m.Reason == nil || *m.Reason != gen.InventoryAdjustmentReasonCorrection || len(m.Lines) != 3 {
		t.Fatalf("movement = %+v, want three lines: the nuts' set is dropped", m)
	}
	if l := m.Lines[0]; l.From == nil || l.To != nil || l.Quantity != 2.5 {
		t.Errorf("the bolts' set = %+v, want 2.5 from the shelf", l)
	}
	if l := m.Lines[1]; l.To == nil || l.Variant.Sku != "WASH-8" || l.Quantity != 2 {
		t.Errorf("the washers' set = %+v, want 2 to the shelf", l)
	}
	if got := onHand(t, h, varBolt, shelf); got != "7.000" {
		t.Errorf("bolts = %q, want 7", got)
	}
	// Set to zero deletes the balance.
	res = adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varBolt, LocationId: shelf, Mode: gen.Set, Quantity: qty(0)})
	mustStatus(t, res, http.StatusCreated, "set to zero")
	if got := onHand(t, h, varBolt, shelf); got != "" {
		t.Errorf("bolts = %q, want no balance at zero", got)
	}
	assertLedgerConsistent(t, h)
}

// TestPosting_DiscontinuedAndService is I12 and design §9: a discontinued
// variant is cleared by adjustments and moves but not received; a product
// turned into a service keeps its stock, which moves and decreases still
// clear, while increases are refused.
func TestPosting_DiscontinuedAndService(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	shelf, other := s.shelf1.Id, s.shelf2.Id
	receive(t, c, s.site.Id, shelf, receiptLine(varNut, 10))

	turned := goods(varNut, 10, "NUT-M8", "Nut M8", "pcs")
	turned.ProductStatus = "Discontinued"
	h.catalog.seed(turned)
	mustStatus(t, move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varNut, FromLocationId: shelf, ToLocationId: other, Quantity: qty(4)}), http.StatusCreated, "move a discontinued variant")
	mustStatus(t, adjust(c, s.site.Id, change(varNut, other, -1)), http.StatusCreated, "write off a discontinued variant")
	res := c.Do(http.MethodPost, receiptsPath, gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: shelf, Lines: []gen.InventoryReceiptLine{receiptLine(varNut, 1)}})
	expectRefusal(t, res, http.StatusBadRequest, "variant_discontinued", "receive a discontinued variant")

	turned.ProductStatus, turned.ProductType = "Active", "Service"
	h.catalog.seed(turned)
	mustStatus(t, move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varNut, FromLocationId: other, ToLocationId: shelf, Quantity: qty(3)}), http.StatusCreated, "move a service's stock")
	mustStatus(t, adjust(c, s.site.Id, change(varNut, shelf, -2)), http.StatusCreated, "decrease a service's stock")
	expectRefusal(t, adjust(c, s.site.Id, change(varNut, shelf, 1)), http.StatusBadRequest, "variant_not_stockable", "increase a service's stock")
	res = adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varNut, LocationId: shelf, Mode: gen.Set, Quantity: qty(100)})
	expectRefusal(t, res, http.StatusBadRequest, "variant_not_stockable", "set a service's stock up")
	mustStatus(t, adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varNut, LocationId: shelf, Mode: gen.Set, Quantity: qty(0)}), http.StatusCreated, "set a service's stock to zero")
	assertLedgerConsistent(t, h)
}

// TestPosting_Idempotency is I14: the same key and body answer the first
// movement with 200, even after its variant is discontinued; the same key
// with another body is client_request_reused, on any of the three
// operations.
func TestPosting_Idempotency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	key := uuid.New()
	body := gen.InventoryReceiptInput{ClientRequestId: key, SiteId: s.site.Id, ToLocationId: s.shelf1.Id, Lines: []gen.InventoryReceiptLine{receiptLine(varBolt, 5)}}
	res := c.Do(http.MethodPost, receiptsPath, body)
	mustStatus(t, res, http.StatusCreated, "first")
	var first, again gen.InventoryMovement
	res.JSON(&first)

	old := goods(varBolt, 10, "BOLT-M8", "Bolt M8", "pcs")
	old.ProductStatus = "Discontinued"
	h.catalog.seed(old)
	res = c.Do(http.MethodPost, receiptsPath, body)
	mustStatus(t, res, http.StatusOK, "replay")
	res.JSON(&again)
	if again.Id != first.Id || again.ClientRequestId != key {
		t.Errorf("replay = %d, want %d", again.Id, first.Id)
	}
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "5.000" {
		t.Errorf("bolts = %q, want 5: one receipt", got)
	}
	body.Lines[0].Quantity = qty(6)
	expectRefusal(t, c.Do(http.MethodPost, receiptsPath, body), http.StatusConflict, "client_request_reused", "the key with another body")
	res = c.Do(http.MethodPost, movesPath, gen.InventoryMoveInput{ClientRequestId: key, SiteId: s.site.Id,
		Lines: []gen.InventoryMoveLine{{VariantId: varBolt, FromLocationId: s.shelf1.Id, ToLocationId: s.shelf2.Id, Quantity: qty(1)}}})
	expectRefusal(t, res, http.StatusConflict, "client_request_reused", "the key on a move")

	adj := gen.InventoryAdjustmentInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, Reason: gen.InventoryAdjustmentReasonLost,
		Lines: []gen.InventoryAdjustmentLine{change(varBolt, s.shelf1.Id, -1)}}
	mustStatus(t, c.Do(http.MethodPost, adjustmentsPath, adj), http.StatusCreated, "adjustment")
	mustStatus(t, c.Do(http.MethodPost, adjustmentsPath, adj), http.StatusOK, "adjustment replay")
	mv := gen.InventoryMoveInput{ClientRequestId: uuid.New(), SiteId: s.site.Id,
		Lines: []gen.InventoryMoveLine{{VariantId: varBolt, FromLocationId: s.shelf1.Id, ToLocationId: s.shelf2.Id, Quantity: qty(1)}}}
	mustStatus(t, c.Do(http.MethodPost, movesPath, mv), http.StatusCreated, "move")
	mustStatus(t, c.Do(http.MethodPost, movesPath, mv), http.StatusOK, "move replay")
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "3.000" {
		t.Errorf("bolts = %q, want 3", got)
	}
	assertLedgerConsistent(t, h)
}

// TestPosting_ConcurrentReceiptsToANewPair is I10: receipts racing to a
// (variant, location) with no balance yet all land, serialised on the row
// the first inserts at zero, with no unique violation.
func TestPosting_ConcurrentReceiptsToANewPair(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(12))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	const n = 8
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			res := c.Do(http.MethodPost, receiptsPath, gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: s.shelf1.Id,
				Lines: []gen.InventoryReceiptLine{receiptLine(varBolt, 1), receiptLine(varNut, 1)}})
			statuses[i] = res.Status
		})
	}
	wg.Wait()
	for i, st := range statuses {
		if st != http.StatusCreated {
			t.Errorf("receipt %d = %d, want 201", i, st)
		}
	}
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "8.000" {
		t.Errorf("bolts = %q, want 8", got)
	}
	assertLedgerConsistent(t, h)
}

// TestPosting_ConcurrentOnOneLocation is §4's lock order under load: moves
// both ways between two shelves, receipts and write-offs on them, racing —
// every one lands, none deadlocks, and the ledger adds up.
func TestPosting_ConcurrentOnOneLocation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(12))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	a, b := s.shelf1.Id, s.shelf2.Id
	receive(t, c, s.site.Id, a, receiptLine(varBolt, 100), receiptLine(varNut, 100))
	receive(t, c, s.site.Id, b, receiptLine(varBolt, 100), receiptLine(varNut, 100))
	const n = 10
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			var res *modtest.Response
			switch i % 3 {
			case 0:
				res = move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varBolt, FromLocationId: a, ToLocationId: b, Quantity: qty(1)},
					gen.InventoryMoveLine{VariantId: varNut, FromLocationId: b, ToLocationId: a, Quantity: qty(1)})
			case 1:
				res = move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varNut, FromLocationId: a, ToLocationId: b, Quantity: qty(2)},
					gen.InventoryMoveLine{VariantId: varBolt, FromLocationId: b, ToLocationId: a, Quantity: qty(2)})
			default:
				res = adjust(c, s.site.Id, change(varBolt, b, -1), change(varWasher, a, 1))
			}
			statuses[i] = res.Status
		})
	}
	wg.Wait()
	for i, st := range statuses {
		if st != http.StatusCreated {
			t.Errorf("posting %d = %d, want 201", i, st)
		}
	}
	assertLedgerConsistent(t, h)
}

// TestPosting_ZeroAndBack is §4's step 5 where balances come and go: postings
// race to take two pairs to zero — which deletes their balances — and to put
// stock back, which creates them again, with the lines in either order. The
// one statement that creates and locks a posting's balances holds no lock
// while it waits for another posting's insert, so none deadlocks; and a
// posting that lost a race anyway runs again. No posting answers 500.
func TestPosting_ZeroAndBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(12))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	a := s.shelf1.Id
	zero := func(v int32) gen.InventoryAdjustmentLine {
		return gen.InventoryAdjustmentLine{VariantId: v, LocationId: a, Mode: gen.Set, Quantity: qty(0)}
	}
	const workers, rounds = 8, 20
	var mu sync.Mutex
	var unexpected []string
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			first, second := varBolt, varNut
			if w%2 == 1 {
				first, second = varNut, varBolt
			}
			for range rounds {
				var res *modtest.Response
				ok := false
				switch w % 4 {
				case 0, 1:
					res = c.Do(http.MethodPost, receiptsPath, gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: a,
						Lines: []gen.InventoryReceiptLine{receiptLine(first, 1), receiptLine(second, 1)}})
					ok = res.Status == http.StatusCreated
				case 2:
					// One pair to zero, the other left: a balance gone under a
					// posting that holds the other's lock.
					res = adjust(c, s.site.Id, zero(first), change(second, a, 1))
					ok = res.Status == http.StatusCreated
				default:
					res = adjust(c, s.site.Id, zero(first), zero(second))
					ok = res.Status == http.StatusCreated || res.Status == http.StatusBadRequest && strings.Contains(string(res.Body), "nothing_to_post")
				}
				if !ok {
					mu.Lock()
					unexpected = append(unexpected, fmt.Sprintf("%d %s", res.Status, res.Body))
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	for _, u := range unexpected {
		t.Errorf("a posting answered %s, want 201 (or nothing_to_post for a set to zero)", u)
	}
	assertLedgerConsistent(t, h)
}

// TestPosting_ReceiptRacesArchive is §4's meeting of a posting and a tree
// write on one location: whichever commits first, the other sees it — the
// receipt lands and the archive is refused location_has_stock, or the
// archive lands and the receipt is refused location_archived. Never both.
func TestPosting_ReceiptRacesArchive(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	for i := range 6 {
		shelf := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindBin, "R"+string(rune('A'+i)))
		var receipt, archive *modtest.Response
		var wg sync.WaitGroup
		wg.Go(func() {
			receipt = c.Do(http.MethodPost, receiptsPath, gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: shelf.Id,
				Lines: []gen.InventoryReceiptLine{receiptLine(varBolt, 1)}})
		})
		wg.Go(func() {
			archive = putLocation(c, shelf, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived })
		})
		wg.Wait()
		switch {
		case receipt.Status == http.StatusCreated:
			expectRefusal(t, archive, http.StatusConflict, "location_has_stock", "the archive after the receipt")
		case archive.Status == http.StatusOK:
			expectRefusal(t, receipt, http.StatusBadRequest, "location_archived", "the receipt after the archive")
		default:
			t.Fatalf("receipt %d %s, archive %d %s: one of them must land", receipt.Status, receipt.Body, archive.Status, archive.Body)
		}
	}
	assertLedgerConsistent(t, h)
}
