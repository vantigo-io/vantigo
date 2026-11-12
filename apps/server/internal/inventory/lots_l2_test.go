package inventory_test

import (
	"bytes"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Lots in counts, transfers, the import, the expiring list and labels
// (manufacturing phase 2 L2, M69–M74).

// lotLineAt is the count's line of variant at location in lot, 0 for none.
func lotLineAt(t *testing.T, c gen.InventoryCount, variant int32, location, lot int64) gen.InventoryCountLine {
	t.Helper()
	for _, l := range *c.Lines {
		has := int64(0)
		if l.Lot != nil {
			has = l.Lot.Id
		}
		if l.Variant.Id == variant && l.Location.Id == location && has == lot {
			return l
		}
	}
	t.Fatalf("count %d has no line of %d at %d in lot %d: %+v", c.Id, variant, location, lot, *c.Lines)
	return gen.InventoryCountLine{}
}

// countedLot is a counted line of a lot by id.
func countedLot(variant int32, location, lot int64, quantity float64) gen.InventoryCountLineInput {
	return gen.InventoryCountLineInput{VariantId: variant, LocationId: location, LotId: &lot, Counted: qty(quantity)}
}

// metaToday is the workspace's today as GET /meta answers it.
func metaToday(t *testing.T, c *modtest.Client) time.Time {
	t.Helper()
	var meta gen.InventoryMeta
	res := c.Do(http.MethodGet, metaPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /meta")
	res.JSON(&meta)
	return meta.Today.Time
}

// TestLotCount_TwoLots is M69: a count freezes a line per lot of a variant,
// counts each, adds a lot found by number, refuses a tracked line without a
// lot, and posts each (location, lot).
func TestLotCount_TwoLots(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id,
		lotLine(varLot, 10, gen.InventoryNewLot{Number: ptr("A-1")}), lotLine(varLot, 5, gen.InventoryNewLot{Number: ptr("A-2")}),
		receiptLine(varBolt, 3))
	a1, a2 := lotID(t, h, varLot, "A-1"), lotID(t, h, varLot, "A-2")

	count := mustOpenCount(t, c, s.site.Id, s.shelf1.Id)
	if n := len(*count.Lines); n != 3 {
		t.Fatalf("lines = %d, want a line per lot and the bolts': %+v", n, *count.Lines)
	}
	if l := lotLineAt(t, count, varLot, s.shelf1.Id, a1); l.Expected != 10 || l.Lot.Number != "A-1" || l.Lot.Expiry == nil {
		t.Errorf("A-1's line = %+v", l)
	}
	if l := lotLineAt(t, count, varLot, s.shelf1.Id, a2); l.Expected != 5 {
		t.Errorf("A-2's line = %+v", l)
	}

	// A tracked line without a lot, an untracked one with one, and another
	// variant's lot are refused, the request whole.
	res := putCounted(c, count.Id, counted(varLot, s.shelf1.Id, 1), countedLot(varBolt, s.shelf1.Id, a1, 1),
		countedLot(varSerial, s.shelf1.Id, a1, 1))
	p := expectRefusal(t, res, http.StatusBadRequest, "lot_required", "the lot rules")
	if codes := lineCodes(p); !slices.Equal(codes, []string{"0:lot_required", "1:lot_not_allowed", "2:lot_unknown"}) {
		t.Errorf("lines = %v", codes)
	}

	// A-1 counted 8, A-2 right, and a lot found on the shelf added by number.
	count = countAnswer(t, putCounted(c, count.Id, countedLot(varLot, s.shelf1.Id, a1, 8), countedLot(varLot, s.shelf1.Id, a2, 5),
		gen.InventoryCountLineInput{VariantId: varLot, LocationId: s.shelf1.Id, Counted: qty(2), Lot: &gen.InventoryNewLot{Number: ptr("A-3")}},
		counted(varBolt, s.shelf1.Id, 3)), http.StatusOK, "count the lots")
	a3 := lotID(t, h, varLot, "A-3")
	if l := lotLineAt(t, count, varLot, s.shelf1.Id, a3); !l.Added || l.Counted == nil || *l.Counted != 2 {
		t.Errorf("A-3's line = %+v", l)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT origin_kind || '/' || origin_id FROM inventory.lots WHERE id = $1`, a3); got != "count/"+strconv.FormatInt(count.Id, 10) {
		t.Errorf("A-3's origin = %s, want the count", got)
	}

	count = countAnswer(t, postCount(c, count.Id, gen.Keep, false), http.StatusOK, "post")
	if lotOnHand(t, h, a1, s.shelf1.Id) != "8.000" || lotOnHand(t, h, a2, s.shelf1.Id) != "5.000" || lotOnHand(t, h, a3, s.shelf1.Id) != "2.000" {
		t.Errorf("after the post: A-1 %s, A-2 %s, A-3 %s", lotOnHand(t, h, a1, s.shelf1.Id), lotOnHand(t, h, a2, s.shelf1.Id), lotOnHand(t, h, a3, s.shelf1.Id))
	}
	m := getMovement(t, c, *count.MovementId)
	var lots []string
	for _, l := range m.Lines {
		if l.Lot != nil {
			lots = append(lots, l.Lot.Number)
		}
	}
	slices.Sort(lots)
	if !slices.Equal(lots, []string{"A-1", "A-3"}) {
		t.Errorf("the count movement's lots = %v, want A-1 down and A-3 up", lots)
	}
	assertLedgerConsistent(t, h)
}

// TestLotCount_Serial is a serial counted (M53, M69): 0 or 1, never more; a
// serial counted 0 leaves stock.
func TestLotCount_Serial(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, gen.InventoryReceiptLine{VariantId: varSerial, Serials: &gen.InventorySerials{First: ptr("SN01"), Count: ptr(2)}})
	sn1, sn2 := lotID(t, h, varSerial, "SN01"), lotID(t, h, varSerial, "SN02")
	count := mustOpenCount(t, c, s.site.Id, 0)
	res := putCounted(c, count.Id, countedLot(varSerial, s.shelf1.Id, sn1, 2))
	expectRefusal(t, res, http.StatusBadRequest, "serial_quantity", "a serial counted 2")
	countAnswer(t, putCounted(c, count.Id, countedLot(varSerial, s.shelf1.Id, sn1, 1), countedLot(varSerial, s.shelf1.Id, sn2, 0)), http.StatusOK, "count the serials")
	countAnswer(t, postCount(c, count.Id, gen.Keep, false), http.StatusOK, "post")
	if lotOnHand(t, h, sn1, s.shelf1.Id) != "1.000" || lotOnHand(t, h, sn2, s.shelf1.Id) != "" {
		t.Errorf("SN01 %q, SN02 %q", lotOnHand(t, h, sn1, s.shelf1.Id), lotOnHand(t, h, sn2, s.shelf1.Id))
	}
	assertLedgerConsistent(t, h)
}

// TestLotCount_TrackedAfterOpening is M69's stale count: lines of a variant
// that became tracked after the count opened post as they are only when they
// expected and counted 0; otherwise they are lot_required.
func TestLotCount_TrackedAfterOpening(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	plain := goods(603, 62, "SALT-1", "Salt", "kg")
	h.catalog.seed(plain)
	zero := mustOpenCount(t, c, s.site.Id, s.shelf1.Id)
	countAnswer(t, putCounted(c, zero.Id, counted(603, s.shelf1.Id, 0)), http.StatusOK, "count 0")
	some := mustOpenCount(t, c, s.site.Id, s.shelf2.Id)
	countAnswer(t, putCounted(c, some.Id, counted(603, s.shelf2.Id, 3)), http.StatusOK, "count 3")

	plain.Tracking = contracts.TrackingLot
	h.catalog.seed(plain)
	if got := countAnswer(t, postCount(c, zero.Id, gen.Keep, false), http.StatusOK, "post the zero count"); got.MovementId != nil {
		t.Errorf("the zero count posted movement %d", *got.MovementId)
	}
	p := expectRefusal(t, postCount(c, some.Id, gen.Keep, false), http.StatusBadRequest, "lot_required", "a count of 3 without a lot")
	if codes := lineCodes(p); !slices.Equal(codes, []string{"0:lot_required"}) {
		t.Errorf("lines = %v", codes)
	}
}

// TestLotCount_PostRacesReceiptsAndHolds is the count post's lock order on
// lots: receipts into the lots it counts and holds put on them race the
// post; every one lands, the post accepts what moved, and the ledger adds up.
func TestLotCount_PostRacesReceiptsAndHolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, append(slices.Clone(everything), "inventory:lots-manage")...)
	s := seedLots(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 10, gen.InventoryNewLot{Number: ptr("R-1")}), lotLine(varLot, 10, gen.InventoryNewLot{Number: ptr("R-2")}))
	r1, r2 := lotID(t, h, varLot, "R-1"), lotID(t, h, varLot, "R-2")
	count := mustOpenCount(t, c, s.site.Id, 0)
	countAnswer(t, putCounted(c, count.Id, countedLot(varLot, s.shelf1.Id, r1, 12), countedLot(varLot, s.shelf1.Id, r2, 9)), http.StatusOK, "count")
	statuses := make([]int, 7)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Go(func() {
			switch {
			case i == 0:
				statuses[i] = postCount(c, count.Id, gen.Keep, true).Status
			case i%3 == 0:
				lot := []int64{r1, r2}[i%2]
				statuses[i] = c.Do(http.MethodPost, lotPath(lot)+"/holds", gen.InventoryLotHoldInput{
					Kind: gen.InventoryLotHoldInputKindQuarantine, Cause: gen.InventoryLotHoldInputCauseOther,
				}).Status
			default:
				statuses[i] = postReceipt(c, s.site.Id, s.shelf1.Id, gen.InventoryReceiptLine{VariantId: varLot, Quantity: qty(1), LotId: []*int64{&r2, &r1}[i%2]}).Status
			}
		})
	}
	wg.Wait()
	for i, st := range statuses {
		var ok bool
		switch {
		case i == 0:
			ok = st == http.StatusOK
		case i%3 == 0:
			// The second quarantine of a lot is hold_exists.
			ok = st == http.StatusOK || st == http.StatusConflict
		default:
			ok = st == http.StatusCreated
		}
		if !ok {
			t.Errorf("request %d = %d", i, st)
		}
	}
	assertLedgerConsistent(t, h)
}

// lotTransferSites is the transfer sites with two lots of the milk on OSL's
// first shelf: M-1 (6) and M-2 (4).
func lotTransferSites(t *testing.T, h *harness, c *modtest.Client) (transferSites, int64, int64) {
	t.Helper()
	s := newTransferSites(t, h, c)
	lot := goods(varLot, 60, "MILK-1", "Milk", "l")
	lot.Tracking, lot.ExpiryDays = contracts.TrackingLot, ptr(int32(30))
	serial := goods(varSerial, 61, "PUMP-1", "Pump", "pcs")
	serial.Tracking = contracts.TrackingSerial
	h.catalog.seed(lot, serial)
	receive(t, c, s.osl.Id, s.shelf1.Id, lotLine(varLot, 6, gen.InventoryNewLot{Number: ptr("M-1")}), lotLine(varLot, 4, gen.InventoryNewLot{Number: ptr("M-2")}))
	return s, lotID(t, h, varLot, "M-1"), lotID(t, h, varLot, "M-2")
}

// TestLotTransfer_RoundTrip is M70: two lots of a variant on two lines, one
// named on the draft and one at the send; a tracked line sent without a lot
// refused; each lot received in parts, in transit per lot; the note prints
// the lots; a cancel takes the lot back where it left.
func TestLotTransfer_RoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s, m1, m2 := lotTransferSites(t, h, c)

	// The draft's lot rules.
	res := c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, s.bgo.Id,
		gen.InventoryTransferLineInput{VariantId: varBolt, Quantity: qty(1), LotId: &m1},
		gen.InventoryTransferLineInput{VariantId: varSerial, Quantity: qty(1), LotId: &m1}))
	if codes := lineCodes(expectRefusal(t, res, http.StatusBadRequest, "lot_not_allowed", "the draft's lots")); !slices.Equal(codes, []string{"0:lot_not_allowed", "1:lot_unknown"}) {
		t.Errorf("lines = %v", codes)
	}

	draft := createTransfer(t, c, s, gen.InventoryTransferLineInput{VariantId: varLot, Quantity: qty(5), LotId: &m1}, line(varLot, 3))
	if draft.Lines[0].Lot == nil || draft.Lines[0].Lot.Number != "M-1" || draft.Lines[1].Lot != nil {
		t.Fatalf("draft lines = %+v", draft.Lines)
	}
	// The second line has no lot: the send refuses it, naming the line.
	p := expectRefusal(t, send(c, draft, gen.InventoryTransferSendInput{Lines: sendLines(draft, s.shelf1.Id, s.shelf1.Id)}),
		http.StatusBadRequest, "lot_required", "a tracked line sent without a lot")
	if codes := lineCodes(p); !slices.Equal(codes, []string{"1:lot_required"}) {
		t.Errorf("lines = %v", codes)
	}
	lines := sendLines(draft, s.shelf1.Id, s.shelf1.Id)
	lines[1].LotId = &m2
	tr := transferAnswer(t, send(c, draft, gen.InventoryTransferSendInput{Lines: lines}), http.StatusOK, "send")
	if tr.Lines[1].Lot == nil || tr.Lines[1].Lot.Id != m2 {
		t.Errorf("sent line 2 = %+v, want M-2", tr.Lines[1])
	}
	if lotOnHand(t, h, m1, s.shelf1.Id) != "1.000" || lotOnHand(t, h, m2, s.shelf1.Id) != "1.000" {
		t.Errorf("left at OSL: M-1 %q, M-2 %q", lotOnHand(t, h, m1, s.shelf1.Id), lotOnHand(t, h, m2, s.shelf1.Id))
	}

	// The note prints each line's lot.
	var mu sync.Mutex
	var printed [][]string
	inventory.SetTransferNoteHook(func(rows [][]string) {
		mu.Lock()
		defer mu.Unlock()
		printed = rows
	})
	t.Cleanup(func() { inventory.SetTransferNoteHook(nil) })
	mustStatus(t, c.Do(http.MethodGet, transferPath(tr.Id)+"/note.pdf", nil), http.StatusOK, "the note")
	mu.Lock()
	if len(printed) != 2 || !strings.HasPrefix(printed[0][3], "M-1 (") || !strings.HasPrefix(printed[1][3], "M-2 (") {
		t.Errorf("the note's lots = %q", printed)
	}
	mu.Unlock()

	// Received in parts: 2 of M-1, then the rest of both.
	tr = transferAnswer(t, receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 2, s.bgoShelf.Id)), http.StatusOK, "receive part")
	if tr.Lines[0].InTransit != 3 || tr.Lines[1].InTransit != 3 {
		t.Errorf("in transit per lot = %v and %v, want 3 and 3", tr.Lines[0].InTransit, tr.Lines[1].InTransit)
	}
	if lotOnHand(t, h, m1, s.bgoShelf.Id) != "2.000" {
		t.Errorf("M-1 at BGO = %q", lotOnHand(t, h, m1, s.bgoShelf.Id))
	}
	transferAnswer(t, receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 3, s.bgoShelf.Id), arrived(tr.Lines[1].Id, 3, s.bgoShelf.Id)), http.StatusOK, "receive the rest")
	if lotOnHand(t, h, m1, s.bgoShelf.Id) != "5.000" || lotOnHand(t, h, m2, s.bgoShelf.Id) != "3.000" {
		t.Errorf("at BGO: M-1 %q, M-2 %q", lotOnHand(t, h, m1, s.bgoShelf.Id), lotOnHand(t, h, m2, s.bgoShelf.Id))
	}

	// A cancel returns the lot it sent.
	other := createTransfer(t, c, s, gen.InventoryTransferLineInput{VariantId: varLot, Quantity: qty(1), LotId: &m2})
	other = mustSend(t, c, other, s.shelf1.Id)
	if lotOnHand(t, h, m2, s.shelf1.Id) != "" {
		t.Errorf("M-2 at OSL after the second send = %q", lotOnHand(t, h, m2, s.shelf1.Id))
	}
	transferAnswer(t, cancelTransfer(c, other), http.StatusOK, "cancel")
	if lotOnHand(t, h, m2, s.shelf1.Id) != "1.000" {
		t.Errorf("M-2 at OSL after the cancel = %q", lotOnHand(t, h, m2, s.shelf1.Id))
	}
	assertLedgerConsistent(t, h)
}

// TestLotTransfer_SendRacesHold is a send of a lot racing a hold on it: the
// send takes the lot FOR SHARE and the hold FOR NO KEY UPDATE, so they
// serialise; both land (a transfer ignores holds, M56).
func TestLotTransfer_SendRacesHold(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, append(slices.Clone(allKeys), "inventory:lots-manage")...)
	s, m1, _ := lotTransferSites(t, h, c)
	for range 3 {
		draft := createTransfer(t, c, s, gen.InventoryTransferLineInput{VariantId: varLot, Quantity: qty(1), LotId: &m1})
		var sent, held int
		var wg sync.WaitGroup
		wg.Go(func() {
			sent = send(c, draft, gen.InventoryTransferSendInput{Lines: sendLines(draft, s.shelf1.Id)}).Status
		})
		wg.Go(func() {
			held = c.Do(http.MethodPost, lotPath(m1)+"/holds", gen.InventoryLotHoldInput{
				Kind: gen.InventoryLotHoldInputKindBlock, Cause: gen.InventoryLotHoldInputCauseRecall,
			}).Status
		})
		wg.Wait()
		if sent != http.StatusOK || (held != http.StatusOK && held != http.StatusConflict) {
			t.Errorf("send %d, hold %d", sent, held)
		}
	}
	assertLedgerConsistent(t, h)
}

// TestLotImport_Columns is M71: the lot columns, each row's lot rules in a
// dry run, and a commit making the lots by number with their dates and the
// import as their origin.
func TestLotImport_Columns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, stockAdjuster...)
	admin, _ := signIn(t, h, everything...)
	s := seedLots(t, h, admin)

	bad := "location;sku;quantity;lot;supplier_lot;production_date;best_before;expiry\n" +
		"OSL-1;MILK-1;10;I-1;HEAT 9;2026-09-01;;31.12.2026\n" +
		"OSL-1;MILK-1;2;;;;;\n" +
		"OSL-1;BOLT-M8;2;X-1;;;;\n" +
		"OSL-1;MILK-1;2;I-2;;2026-13-01;;\n" +
		"OSL-1;MILK-1;2;I-3;;2026-09-01;;2026-08-01\n" +
		"OSL-1;PUMP-1;2;SN-9;;;;\n" +
		"OSL-2;PUMP-1;1;SN-9;;;;\n" +
		"OSL-2;BOLT-M8;4;;;;;\n"
	r := importResult(t, importCSV(c, s.site.Id, false, bad), http.StatusOK, "dry run")
	want := []string{"2:ok", "3:lot_required", "4:lot_not_allowed", "5:lot_invalid", "6:lot_invalid", "7:serial_quantity", "8:ok", "9:ok"}
	if got := outcomes(r); !slices.Equal(got, want) {
		t.Errorf("outcomes = %v, want %v", got, want)
	}
	if r.Rows[0].Lot == nil || *r.Rows[0].Lot != "I-1" {
		t.Errorf("row 2's lot = %v", r.Rows[0].Lot)
	}

	good := "location;sku;quantity;lot;supplier_lot;production_date;best_before;expiry\n" +
		"OSL-1;MILK-1;10;I-1;HEAT 9;2026-09-01;;31.12.2026\n" +
		"OSL-2;MILK-1;3;i-1;;;;\n" +
		"OSL-2;PUMP-1;1;SN-9;;;;\n" +
		"OSL-2;BOLT-M8;4;;;;;\n"
	r = importResult(t, importCSV(c, s.site.Id, true, good), http.StatusCreated, "commit")
	i1 := lotID(t, h, varLot, "I-1")
	if got := modtest.One[string](t, h.Harness, `SELECT concat_ws('|', supplier_lot, production_date, best_before, expiry, origin_kind, origin_id) FROM inventory.lots WHERE id = $1`, i1); got != "HEAT 9|2026-09-01|2026-09-21|2026-12-31|import|"+strconv.FormatInt(*r.MovementId, 10) {
		t.Errorf("I-1 = %s", got)
	}
	if lotOnHand(t, h, i1, s.shelf1.Id) != "10.000" || lotOnHand(t, h, i1, s.shelf2.Id) != "3.000" || lotOnHand(t, h, lotID(t, h, varSerial, "SN-9"), s.shelf2.Id) != "1.000" {
		t.Errorf("the imported lots' stock is wrong")
	}
	if onHand(t, h, varBolt, s.shelf2.Id) != "4.000" {
		t.Errorf("bolts = %s", onHand(t, h, varBolt, s.shelf2.Id))
	}
	// A header naming a column the import does not know is refused.
	mustStatus(t, importCSV(c, s.site.Id, false, "location;sku;quantity;colour\nOSL-1;MILK-1;1;red\n"), http.StatusBadRequest, "an unknown column")
	assertLedgerConsistent(t, h)
}

// TestLotExpiring_List is M72: the lots with stock expiring within the days
// — the boundary day in, the day after out — and those already expired, by
// expiry; the site and status filters; the CSV; the dashboard's two items.
func TestLotExpiring_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	today := metaToday(t, c)
	day := func(n int) *time.Time { return ptr(today.AddDate(0, 0, n)) }
	at := func(n int) gen.InventoryNewLot {
		d := day(n)
		return gen.InventoryNewLot{Expiry: date(d.Year(), d.Month(), d.Day())}
	}
	for _, l := range []struct {
		number string
		days   int
		shelf  int64
	}{{"PAST", -1, s.shelf1.Id}, {"TODAY", 0, s.shelf1.Id}, {"D10", 10, s.shelf2.Id}, {"D30", 30, s.shelf1.Id}, {"D31", 31, s.shelf1.Id}} {
		n := at(l.days)
		n.Number = ptr(l.number)
		receive(t, c, s.site.Id, l.shelf, lotLine(varLot, 1, n))
	}
	// A lot with no stock is never listed.
	receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("EMPTY"), Expiry: at(5).Expiry}))
	empty := lotID(t, h, varLot, "EMPTY")
	mustStatus(t, adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varLot, LocationId: s.shelf1.Id, Mode: gen.Set, Quantity: qty(0), LotId: &empty}), http.StatusCreated, "empty EMPTY")

	list := func(query string) gen.InventoryLotExpiringPage {
		t.Helper()
		res := c.Do(http.MethodGet, lotsPath+"/expiring"+query, nil)
		mustStatus(t, res, http.StatusOK, "GET /lots/expiring"+query)
		var out gen.InventoryLotExpiringPage
		res.JSON(&out)
		return out
	}
	numbers := func(p gen.InventoryLotExpiringPage) []string {
		var out []string
		for _, l := range p.Lots {
			out = append(out, l.Number)
		}
		return out
	}
	page := list("")
	if got := numbers(page); !slices.Equal(got, []string{"PAST", "TODAY", "D10", "D30"}) {
		t.Errorf("within 30 days = %v", got)
	}
	if !page.Today.Equal(today) || page.Until.Format(time.DateOnly) != day(30).Format(time.DateOnly) || page.More {
		t.Errorf("page = today %s until %s more %v", page.Today, page.Until, page.More)
	}
	if !page.Lots[0].Expired || page.Lots[1].Expired {
		t.Errorf("expired flags = %v, %v", page.Lots[0].Expired, page.Lots[1].Expired)
	}
	if got := numbers(list("?days=31")); !slices.Equal(got, []string{"PAST", "TODAY", "D10", "D30", "D31"}) {
		t.Errorf("within 31 days = %v", got)
	}
	if got := numbers(list("?days=1&status=available")); !slices.Equal(got, []string{"PAST", "TODAY"}) {
		t.Errorf("within a day = %v", got)
	}
	mustStatus(t, c.Do(http.MethodGet, lotsPath+"/expiring?days=0", nil), http.StatusBadRequest, "days 0")

	res := c.Do(http.MethodGet, lotsPath+"/expiring.csv?days=10", nil)
	mustStatus(t, res, http.StatusOK, "GET /lots/expiring.csv")
	if d := res.Header("Content-Disposition"); !strings.Contains(d, "expiring-"+today.Format(time.DateOnly)+".csv") {
		t.Errorf("disposition = %q", d)
	}
	rows := strings.Split(strings.TrimSpace(strings.TrimPrefix(string(res.Body), "\ufeff")), "\r\n")
	if len(rows) != 4 || rows[0] != "lot;sku;product;supplier_lot;expiry;best_before;days_left;status;on_hand" ||
		!strings.HasPrefix(rows[1], "PAST;MILK-1;Milk;;"+day(-1).Format(time.DateOnly)+";") || !strings.HasSuffix(rows[1], ";-1;available;1") {
		t.Errorf("csv = %q", rows)
	}
	mustStatus(t, c.Do(http.MethodGet, lotsPath+"/expiring.csv?days=400", nil), http.StatusBadRequest, "days 400")

	var items []gen.InventoryStatsAttentionItem
	res = c.Do(http.MethodGet, attentionPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /stats/attention")
	res.JSON(&items)
	var got []string
	for _, it := range items {
		if it.Type == gen.LotsExpired || it.Type == gen.LotsExpiring {
			got = append(got, string(it.Type)+":"+strconv.Itoa(int(*it.Count))+":"+it.Title)
		}
	}
	if !slices.Equal(got, []string{"lotsExpired:1:1 lot in stock has expired", "lotsExpiring:2:2 lots in stock expire within 14 days"}) {
		t.Errorf("attention = %v", got)
	}
}

// TestLotLabels_PDF is M73: lot labels on a sheet, in the order asked, with
// copies; an unknown lot and too many labels refused.
func TestLotLabels_PDF(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("LB-1")}), lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("LB-2")}))
	l1, l2 := lotID(t, h, varLot, "LB-1"), lotID(t, h, varLot, "LB-2")
	ids := strconv.FormatInt(l2, 10) + "," + strconv.FormatInt(l1, 10)

	res := c.Do(http.MethodGet, lotsPath+"/labels.pdf?ids="+ids+"&layout=l7163&copies=2", nil)
	mustStatus(t, res, http.StatusOK, "GET /lots/labels.pdf")
	if ct := res.Header("Content-Type"); ct != "application/pdf" || !bytes.HasPrefix(res.Body, []byte("%PDF")) {
		t.Errorf("content type %q, body %.8q", ct, res.Body)
	}
	if d := res.Header("Content-Disposition"); !strings.Contains(d, "lot-labels-l7163.pdf") {
		t.Errorf("disposition = %q", d)
	}
	mustStatus(t, c.Do(http.MethodGet, lotsPath+"/labels.pdf?ids=999999", nil), http.StatusBadRequest, "an unknown lot")
	expectRefusal(t, c.Do(http.MethodGet, lotsPath+"/labels.pdf?ids="+ids+"&copies=501", nil), http.StatusBadRequest, "labels_too_many", "1002 labels")

	// The movements of a lot (M74).
	var page gen.InventoryMovementPage
	res = c.Do(http.MethodGet, movementsPath+"?lotId="+strconv.FormatInt(l1, 10), nil)
	mustStatus(t, res, http.StatusOK, "GET /movements?lotId")
	res.JSON(&page)
	if len(page.Movements) != 1 {
		t.Errorf("movements of LB-1 = %d, want its receipt", len(page.Movements))
	}
}
