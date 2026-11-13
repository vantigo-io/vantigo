package inventory_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Lots (manufacturing phase 2 L1a, M49–M67): the lot-aware ledger. The
// guard keeps tracked variants out of Products until layer M, so these
// tests make them through the fake catalog.

// The tracked variants: one in lots with a 30-day expiry and a 20-day
// best-before, one in serials.
const (
	varLot    int32 = 601
	varSerial int32 = 602
)

// seedLots seeds the ledger's site and the two tracked variants.
func seedLots(t *testing.T, h *harness, c *modtest.Client) ledgerSite {
	t.Helper()
	s := newLedgerSite(t, h, c)
	lot := goods(varLot, 60, "MILK-1", "Milk", "l")
	lot.Tracking, lot.ExpiryDays, lot.BestBeforeDays = contracts.TrackingLot, ptr(int32(30)), ptr(int32(20))
	serial := goods(varSerial, 61, "PUMP-1", "Pump", "pcs")
	serial.Tracking = contracts.TrackingSerial
	h.catalog.seed(lot, serial)
	return s
}

// lotLine is a receipt line of a lot by number.
func lotLine(variant int32, quantity float64, lot gen.InventoryNewLot) gen.InventoryReceiptLine {
	return gen.InventoryReceiptLine{VariantId: variant, Quantity: qty(quantity), Lot: &lot}
}

// lotID is the variant's lot of the number.
func lotID(t *testing.T, h *harness, variant int32, number string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM inventory.lots WHERE variant_id = $1 AND lower(number) = lower($2)`, variant, number)
}

// lotOnHand is what lies of the lot on location, "" for none.
func lotOnHand(t *testing.T, h *harness, lot, location int64) string {
	t.Helper()
	if h.Count(t, `SELECT count(*) FROM inventory.balances WHERE lot_id = $1 AND location_id = $2`, lot, location) == 0 {
		return ""
	}
	return modtest.One[string](t, h.Harness, `SELECT on_hand::text FROM inventory.balances WHERE lot_id = $1 AND location_id = $2`, lot, location)
}

// date is a calendar day as the API takes it.
func date(y int, m time.Month, d int) *openapi_types.Date {
	return &openapi_types.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// TestLots_Receipt is a receipt making and adding to lots (M57): a lot made
// by number with its dates defaulted from the receipt's day, the same number
// in another case adding to it without its dates compared, a differing
// stated date refused, a generated number, and the line naming its lot.
func TestLots_Receipt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)

	m := receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 10, gen.InventoryNewLot{Number: ptr("A-1"), SupplierLot: ptr("HEAT 7")}))
	if len(m.Lines) != 1 || m.Lines[0].Lot == nil || m.Lines[0].Lot.Number != "A-1" || m.Lines[0].Lot.Expiry == nil ||
		m.Lines[0].Lot.Expiry.Format(time.DateOnly) != "2026-10-12" {
		t.Fatalf("the receipt's line = %+v, want lot A-1 expiring 2026-10-12 (the receipt's day + 30)", m.Lines)
	}
	id := lotID(t, h, varLot, "A-1")
	if got := modtest.One[string](t, h.Harness, `SELECT best_before::text FROM inventory.lots WHERE id = $1`, id); got != "2026-10-02" {
		t.Errorf("best before = %s, want the receipt's day + 20", got)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT origin_kind || '/' || origin_id FROM inventory.lots WHERE id = $1`, id); got != "movement/"+strconv.FormatInt(m.Id, 10) {
		t.Errorf("origin = %s, want the receipt's movement", got)
	}

	// The same number again, in lower case and with no dates: added to it.
	receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 5, gen.InventoryNewLot{Number: ptr("a-1")}))
	if got := lotOnHand(t, h, id, s.shelf1.Id); got != "15.000" {
		t.Errorf("A-1 on shelf 1 = %s, want 15", got)
	}
	// A stated expiry that differs is seen.
	res := postReceipt(c, s.site.Id, s.shelf1.Id, lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("A-1"), Expiry: date(2027, 1, 1)}))
	p := expectRefusal(t, res, http.StatusBadRequest, "lot_dates_differ", "a differing expiry")
	if codes := lineCodes(p); !slices.Equal(codes, []string{"0:lot_dates_differ"}) {
		t.Errorf("lines = %v", codes)
	}
	// The same one stated as stored is no difference.
	receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("A-1"), Expiry: date(2026, 10, 12)}))

	// No number: L<n> from the counter.
	m = receive(t, c, s.site.Id, s.shelf2.Id, lotLine(varLot, 2, gen.InventoryNewLot{}))
	if n := m.Lines[0].Lot.Number; !strings.HasPrefix(n, "L") || len(n) < 5 {
		t.Errorf("generated number = %q, want L<n>", n)
	}
	// By id.
	receive(t, c, s.site.Id, s.shelf2.Id, gen.InventoryReceiptLine{VariantId: varLot, Quantity: qty(1), LotId: &id})
	if got := lotOnHand(t, h, id, s.shelf2.Id); got != "1.000" {
		t.Errorf("A-1 on shelf 2 = %s", got)
	}
	assertLedgerConsistent(t, h)
}

// TestLots_Rules is every line rule of M56 a receipt, a move and an
// adjustment can meet, and that an untracked variant posts as before.
func TestLots_Rules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 10, gen.InventoryNewLot{Number: ptr("B1")}))
	b1 := lotID(t, h, varLot, "B1")

	cases := []struct {
		name string
		line gen.InventoryReceiptLine
		code string
	}{
		{"a tracked line naming no lot", receiptLine(varLot, 1), "lot_required"},
		{"an untracked line naming one", lotLine(varBolt, 1, gen.InventoryNewLot{Number: ptr("X")}), "lot_not_allowed"},
		{"an untracked line naming an id", gen.InventoryReceiptLine{VariantId: varBolt, Quantity: qty(1), LotId: &b1}, "lot_not_allowed"},
		{"another variant's lot", gen.InventoryReceiptLine{VariantId: varSerial, Quantity: qty(1), LotId: &b1}, "lot_unknown"},
		{"no such lot", gen.InventoryReceiptLine{VariantId: varLot, Quantity: qty(1), LotId: ptr(int64(999999))}, "lot_unknown"},
		{"a serial of 3", lotLine(varSerial, 3, gen.InventoryNewLot{Number: ptr("SN1")}), "serial_quantity"},
		{"a first serial without digits", gen.InventoryReceiptLine{VariantId: varSerial, Serials: &gen.InventorySerials{First: ptr("ABC"), Count: ptr(2)}}, "serial_first_invalid"},
	}
	for _, tc := range cases {
		res := postReceipt(c, s.site.Id, s.shelf1.Id, tc.line)
		p := expectRefusal(t, res, http.StatusBadRequest, tc.code, tc.name)
		if codes := lineCodes(p); !slices.Equal(codes, []string{"0:" + tc.code}) {
			t.Errorf("%s: lines = %v", tc.name, codes)
		}
	}
	// A move of a tracked variant names its lot; one naming it moves it.
	res := move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varLot, FromLocationId: s.shelf1.Id, ToLocationId: s.shelf2.Id, Quantity: qty(1)})
	expectRefusal(t, res, http.StatusBadRequest, "lot_required", "a move without its lot")
	res = move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varLot, FromLocationId: s.shelf1.Id, ToLocationId: s.shelf2.Id, Quantity: qty(4), LotId: &b1})
	mustStatus(t, res, http.StatusCreated, "a move of B1")
	if lotOnHand(t, h, b1, s.shelf1.Id) != "6.000" || lotOnHand(t, h, b1, s.shelf2.Id) != "4.000" {
		t.Errorf("B1 = %s and %s after the move", lotOnHand(t, h, b1, s.shelf1.Id), lotOnHand(t, h, b1, s.shelf2.Id))
	}
	// An adjustment down by id, up by a new number, and a set of one lot.
	res = adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varLot, LocationId: s.shelf1.Id, Mode: gen.Change, Quantity: qty(-1), LotId: &b1},
		gen.InventoryAdjustmentLine{VariantId: varLot, LocationId: s.shelf1.Id, Mode: gen.Change, Quantity: qty(3), Lot: &gen.InventoryNewLot{Number: ptr("FOUND")}},
		gen.InventoryAdjustmentLine{VariantId: varLot, LocationId: s.shelf2.Id, Mode: gen.Set, Quantity: qty(2), LotId: &b1})
	mustStatus(t, res, http.StatusCreated, "an adjustment of lots")
	if lotOnHand(t, h, b1, s.shelf1.Id) != "5.000" || lotOnHand(t, h, b1, s.shelf2.Id) != "2.000" ||
		lotOnHand(t, h, lotID(t, h, varLot, "FOUND"), s.shelf1.Id) != "3.000" {
		t.Errorf("after the adjustment: B1 %s and %s", lotOnHand(t, h, b1, s.shelf1.Id), lotOnHand(t, h, b1, s.shelf2.Id))
	}
	// Untracked stock posts as it always has, with no lot.
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 3))
	if onHand(t, h, varBolt, s.shelf1.Id) != "3.000" || h.Count(t, `SELECT count(*) FROM inventory.balances WHERE variant_id = $1 AND lot_id IS NOT NULL`, varBolt) != 0 {
		t.Errorf("an untracked receipt: %s", onHand(t, h, varBolt, s.shelf1.Id))
	}
	assertLedgerConsistent(t, h)
}

// TestLots_Serials is a receipt's serials expanded into a line of 1 each,
// keeping the digits' width, and a serial held at most once (M53, M57).
func TestLots_Serials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)

	m := receive(t, c, s.site.Id, s.shelf1.Id, gen.InventoryReceiptLine{VariantId: varSerial, Serials: &gen.InventorySerials{First: ptr("SN0099"), Count: ptr(3)}})
	var numbers []string
	for _, l := range m.Lines {
		numbers = append(numbers, l.Lot.Number)
		if l.Quantity != 1 {
			t.Errorf("serial line %+v, want 1", l)
		}
	}
	if !slices.Equal(numbers, []string{"SN0099", "SN0100", "SN0101"}) {
		t.Errorf("serials = %v", numbers)
	}
	if got := modtest.One[bool](t, h.Harness, `SELECT bool_and(serial) FROM inventory.lots WHERE variant_id = $1`, varSerial); !got {
		t.Error("the lots of a serial variant are not marked serial")
	}
	// Received again while it is in stock, at another location.
	res := postReceipt(c, s.site.Id, s.shelf2.Id, lotLine(varSerial, 1, gen.InventoryNewLot{Number: ptr("SN0100")}))
	expectRefusal(t, res, http.StatusBadRequest, "serial_quantity", "a serial in stock received again")
	// A move keeps it at 1; out and back in is allowed.
	sn := lotID(t, h, varSerial, "SN0100")
	mustStatus(t, move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varSerial, FromLocationId: s.shelf1.Id, ToLocationId: s.shelf2.Id, Quantity: qty(1), LotId: &sn}),
		http.StatusCreated, "a serial moved")
	mustStatus(t, adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varSerial, LocationId: s.shelf2.Id, Mode: gen.Change, Quantity: qty(-1), LotId: &sn}),
		http.StatusCreated, "a serial written off")
	receive(t, c, s.site.Id, s.shelf1.Id, gen.InventoryReceiptLine{VariantId: varSerial, Quantity: qty(1), LotId: &sn})
	// A set above 1.
	res = adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varSerial, LocationId: s.shelf1.Id, Mode: gen.Set, Quantity: qty(2), LotId: &sn})
	expectRefusal(t, res, http.StatusBadRequest, "serial_quantity", "a serial set to 2")
	assertLedgerConsistent(t, h)
}

// TestLots_ConcurrentSameNumber is two receipts making one lot number at
// once (§19): the unique index serialises them and both add to one lot.
func TestLots_ConcurrentSameNumber(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	const n = 6
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := range n {
		wg.Go(func() {
			statuses[i] = postReceipt(c, s.site.Id, s.shelf1.Id, lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("RACE")})).Status
		})
	}
	wg.Wait()
	for i, st := range statuses {
		if st != http.StatusCreated {
			t.Errorf("receipt %d = %d", i, st)
		}
	}
	if got := h.Count(t, `SELECT count(*) FROM inventory.lots WHERE variant_id = $1`, varLot); got != 1 {
		t.Errorf("%d lots made, want 1", got)
	}
	if got := lotOnHand(t, h, lotID(t, h, varLot, "RACE"), s.shelf1.Id); got != "6.000" {
		t.Errorf("RACE = %s, want 6", got)
	}
	assertLedgerConsistent(t, h)
}

// TestLots_ConcurrentSerialAtTwoLocations is two increases of one serial lot
// at two locations at once (M53): each balance alone stays at 1, so the lot's
// lock decides — the second sees the first's 1 and is refused.
func TestLots_ConcurrentSerialAtTwoLocations(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	for round := range 5 {
		number := "S" + strconv.Itoa(round+1)
		var wg sync.WaitGroup
		statuses := make([]int, 2)
		for i, to := range []int64{s.shelf1.Id, s.shelf2.Id} {
			wg.Go(func() {
				statuses[i] = postReceipt(c, s.site.Id, to, lotLine(varSerial, 1, gen.InventoryNewLot{Number: ptr(number)})).Status
			})
		}
		wg.Wait()
		slices.Sort(statuses)
		if !slices.Equal(statuses, []int{http.StatusCreated, http.StatusBadRequest}) {
			t.Fatalf("round %d: statuses %v, want one 201 and one 400 serial_quantity", round, statuses)
		}
		if got := modtest.One[string](t, h.Harness, `SELECT sum(on_hand)::text FROM inventory.balances WHERE lot_id = $1`, lotID(t, h, varSerial, number)); got != "1.000" {
			t.Fatalf("round %d: the serial holds %s", round, got)
		}
	}
	// By id too: the lot exists at zero, received at two places at once.
	sn := lotID(t, h, varSerial, "S1")
	from := modtest.One[int64](t, h.Harness, `SELECT location_id FROM inventory.balances WHERE lot_id = $1`, sn)
	mustStatus(t, adjust(c, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varSerial, LocationId: from, Mode: gen.Change, Quantity: qty(-1), LotId: &sn}),
		http.StatusCreated, "S1 written off")
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, to := range []int64{s.shelf1.Id, s.shelf2.Id} {
		wg.Go(func() {
			statuses[i] = postReceipt(c, s.site.Id, to, gen.InventoryReceiptLine{VariantId: varSerial, Quantity: qty(1), LotId: &sn}).Status
		})
	}
	wg.Wait()
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusCreated, http.StatusBadRequest}) {
		t.Errorf("by id: statuses %v", statuses)
	}
	assertLedgerConsistent(t, h)
}

// TestLots_NoLotKeySerialises is two postings to a new (variant, location,
// no lot) key at once (M55): NULLS NOT DISTINCT keeps it one row, so they
// serialise on it rather than make two.
func TestLots_NoLotKeySerialises(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	const n = 8
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if st := postReceipt(c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 1)).Status; st != http.StatusCreated {
				t.Errorf("receipt = %d", st)
			}
		})
	}
	wg.Wait()
	if got := h.Count(t, `SELECT count(*) FROM inventory.balances WHERE variant_id = $1 AND location_id = $2`, varBolt, s.shelf1.Id); got != 1 {
		t.Errorf("%d balance rows, want 1", got)
	}
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "8.000" {
		t.Errorf("on hand = %s, want 8", got)
	}
}

// receiveLots puts three lots of varLot on shelf 1 through the ledger's
// API: E1 expiring first, E2 later, E3 with none.
func receiveLots(t *testing.T, h *harness, c *modtest.Client, s ledgerSite) (e1, e2, e3 int64) {
	t.Helper()
	receive(t, c, s.site.Id, s.shelf1.Id,
		lotLine(varLot, 5, gen.InventoryNewLot{Number: ptr("E2"), Expiry: date(2026, 12, 1)}),
		lotLine(varLot, 5, gen.InventoryNewLot{Number: ptr("E1"), Expiry: date(2026, 10, 1)}),
		lotLine(varLot, 5, gen.InventoryNewLot{Number: ptr("E3"), Expiry: date(2027, 6, 1)}))
	h.Exec(t, `UPDATE inventory.lots SET expiry = NULL WHERE variant_id = $1 AND number = 'E3'`, varLot)
	return lotID(t, h, varLot, "E1"), lotID(t, h, varLot, "E2"), lotID(t, h, varLot, "E3")
}

// issueOf issues on its own transaction and answers what it posted.
func issueOf(t *testing.T, h *harness, doc int64, issue contracts.StockIssue) (contracts.StockPosted, error) {
	t.Helper()
	var posted contracts.StockPosted
	issue.Document = contracts.StockSource{Module: "orders", Kind: "delivery", ID: doc}
	err := inTx(t, h, func(tx pgx.Tx) error {
		var err error
		posted, err = ledgerOf(h).Issue(inCallerTx(t.Context()), tx, order, issue)
		return err
	})
	return posted, err
}

// TestLots_IssueHoldsAndExpiry is an issue's lot rules (M56, M62): a held
// lot refused, an expired one refused unless the line allows it and the call
// carries the override, the override recorded, and a zero today a failure.
func TestLots_IssueHoldsAndExpiry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	e1, e2, _ := receiveLots(t, h, c, s)
	l := ledgerOf(h)
	quarantine := contracts.StockSource{Module: "manufacturing", Kind: "deviation", ID: 1}
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.HoldLots(inCallerTx(t.Context()), tx, quarantine, []contracts.LotHold{{LotID: e2, Kind: "quarantine", Cause: "deviation"}})
	}); err != nil {
		t.Fatalf("HoldLots: %v", err)
	}
	line := func(lot int64, allow bool) []contracts.IssueLine {
		return []contracts.IssueLine{{VariantID: varLot, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "1", LotID: lot, AllowExpired: allow}}
	}
	if _, err := issueOf(t, h, 1, contracts.StockIssue{SiteID: s.site.Id, Today: testToday, Lines: line(e2, false)}); refusalCode(err) != contracts.StockLotBlocked {
		t.Errorf("a quarantined lot = %v, want lot_blocked", err)
	}
	var r *contracts.StockRefusal
	if errors.As(err0(issueOf(t, h, 1, contracts.StockIssue{SiteID: s.site.Id, Today: testToday, Lines: line(e2, false)})), &r) &&
		(r.Lines[0].Index != 0 || r.Lines[0].LotID != e2) {
		t.Errorf("the refusal names line %d lot %d, want 0 and %d", r.Lines[0].Index, r.Lines[0].LotID, e2)
	}
	late := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if _, err := issueOf(t, h, 2, contracts.StockIssue{SiteID: s.site.Id, Today: late, Lines: line(e1, false)}); refusalCode(err) != contracts.StockLotExpired {
		t.Errorf("an expired lot = %v, want lot_expired", err)
	}
	if _, err := issueOf(t, h, 2, contracts.StockIssue{SiteID: s.site.Id, Today: late, Lines: line(e1, true)}); refusalCode(err) != contracts.StockLotExpired {
		t.Errorf("an expired lot allowed without the override = %v, want lot_expired", err)
	}
	if _, err := issueOf(t, h, 2, contracts.StockIssue{SiteID: s.site.Id, Today: late, ExpiredOverride: true, Lines: line(e1, false)}); refusalCode(err) != contracts.StockLotExpired {
		t.Errorf("the override with a line not allowing it = %v, want lot_expired", err)
	}
	// Expiring today is not expired.
	if _, err := issueOf(t, h, 3, contracts.StockIssue{SiteID: s.site.Id, Today: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Lines: line(e1, false)}); err != nil {
		t.Errorf("a lot on its expiry day: %v", err)
	}
	posted, err := issueOf(t, h, 4, contracts.StockIssue{SiteID: s.site.Id, Today: late, ExpiredOverride: true, Lines: line(e1, true)})
	if err != nil {
		t.Fatalf("the override: %v", err)
	}
	if got := modtest.One[bool](t, h.Harness, `SELECT expired_override FROM inventory.movement_lines WHERE movement_id = $1`, posted.MovementID); !got {
		t.Error("the override is not recorded on the line")
	}
	// A tracked line naming nothing.
	if _, err := issueOf(t, h, 5, contracts.StockIssue{SiteID: s.site.Id, Today: testToday, Lines: line(0, false)}); refusalCode(err) != contracts.StockLotRequired {
		t.Errorf("no lot = %v, want lot_required", err)
	}
	// A move of the held lot is allowed: holds stop issues, not moves.
	mustStatus(t, move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varLot, FromLocationId: s.shelf1.Id, ToLocationId: s.shelf2.Id, Quantity: qty(1), LotId: &e2}),
		http.StatusCreated, "a held lot moved")
	// No today is a programming error, not a refusal.
	if _, err := issueOf(t, h, 6, contracts.StockIssue{SiteID: s.site.Id, Lines: line(e1, false)}); err == nil || refusalCode(err) != "" {
		t.Errorf("no today = %v, want a failure", err)
	}
	assertLedgerConsistent(t, h)
}

// err0 is the error of a two-valued call.
func err0[T any](_ T, err error) error { return err }

// TestLots_AutoLot is an AutoLot line taking its lots FEFO under the
// posting's locks (M65a): skipping a held lot and an expired one, answering
// its picks, answering them again on a replay — an AutoLot line of an
// untracked variant before it, which posts as one line with no lot, kept in
// step — and refusing what the usable lots do not cover.
func TestLots_AutoLot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	e1, e2, e3 := receiveLots(t, h, c, s)
	issue := contracts.StockIssue{SiteID: s.site.Id, Today: testToday, Lines: []contracts.IssueLine{
		{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "1"},
		{VariantID: varBolt, LineID: 3, FromLocationID: s.shelf1.Id, Quantity: "1", AutoLot: true},
		{VariantID: varLot, LineID: 2, FromLocationID: s.shelf1.Id, Quantity: "7", AutoLot: true},
	}}
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 2))
	posted, err := issueOf(t, h, 10, issue)
	if err != nil {
		t.Fatalf("auto: %v", err)
	}
	want := []contracts.StockPick{{Index: 2, LotID: e1, LocationID: s.shelf1.Id, Quantity: "5"}, {Index: 2, LotID: e2, LocationID: s.shelf1.Id, Quantity: "2"}}
	if !slices.Equal(posted.Picks, want) {
		t.Errorf("picks = %+v, want %+v", posted.Picks, want)
	}
	again, err := issueOf(t, h, 10, issue)
	if err != nil || again.MovementID != posted.MovementID || !slices.Equal(again.Picks, want) {
		t.Errorf("replay = %+v, %v; want %+v", again, err, posted)
	}
	// E2 held and E1 gone: auto skips E2 and takes E3, whose expiry is none.
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return ledgerOf(h).HoldLots(inCallerTx(t.Context()), tx, contracts.StockSource{Module: "inventory", Kind: "lot", ID: e2},
			[]contracts.LotHold{{LotID: e2, Kind: "block", Cause: "recall"}})
	}); err != nil {
		t.Fatal(err)
	}
	posted, err = issueOf(t, h, 11, contracts.StockIssue{SiteID: s.site.Id, Today: testToday, Lines: []contracts.IssueLine{
		{VariantID: varLot, LineID: 2, FromLocationID: s.shelf1.Id, Quantity: "2", AutoLot: true}}})
	if err != nil || !slices.Equal(posted.Picks, []contracts.StockPick{{Index: 0, LotID: e3, LocationID: s.shelf1.Id, Quantity: "2"}}) {
		t.Errorf("auto past a blocked lot = %+v, %v", posted.Picks, err)
	}
	// What is left usable — E3's 3 — does not cover 4.
	if _, err := issueOf(t, h, 12, contracts.StockIssue{SiteID: s.site.Id, Today: testToday, Lines: []contracts.IssueLine{
		{VariantID: varLot, LineID: 2, FromLocationID: s.shelf1.Id, Quantity: "4", AutoLot: true}}}); refusalCode(err) != contracts.StockInsufficientStock {
		t.Errorf("auto short = %v, want insufficient_stock", err)
	}
	assertLedgerConsistent(t, h)
}

// TestLots_Holds is the holds and their derived status (M64): two sources on
// one lot, one released while the other's stays, HoldLots idempotent per
// source and kind, a release of every lot a source holds.
func TestLots_Holds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	e1, e2, _ := receiveLots(t, h, c, s)
	l := ledgerOf(h)
	deviation := contracts.StockSource{Module: "manufacturing", Kind: "deviation", ID: 5}
	recall := contracts.StockSource{Module: "inventory", Kind: "lot", ID: e1}
	status := func(lot int64) string {
		return modtest.One[string](t, h.Harness, `SELECT status FROM inventory.lots WHERE id = $1`, lot)
	}
	do := func(fn func(ctx context.Context, tx pgx.Tx) error) {
		t.Helper()
		if err := inTx(t, h, func(tx pgx.Tx) error { return fn(inCallerTx(t.Context()), tx) }); err != nil {
			t.Fatal(err)
		}
	}
	do(func(ctx context.Context, tx pgx.Tx) error {
		return l.HoldLots(ctx, tx, deviation, []contracts.LotHold{{LotID: e1, Kind: "quarantine", Cause: "deviation"}, {LotID: e2, Kind: "quarantine", Cause: "deviation"}})
	})
	if status(e1) != "quarantine" || status(e2) != "quarantine" {
		t.Errorf("statuses = %s, %s", status(e1), status(e2))
	}
	// The same hold again changes nothing.
	do(func(ctx context.Context, tx pgx.Tx) error {
		return l.HoldLots(ctx, tx, deviation, []contracts.LotHold{{LotID: e1, Kind: "quarantine", Cause: "deviation"}})
	})
	if n := h.Count(t, `SELECT count(*) FROM inventory.lot_holds WHERE lot_id = $1`, e1); n != 1 {
		t.Errorf("%d holds after a repeat, want 1", n)
	}
	do(func(ctx context.Context, tx pgx.Tx) error {
		return l.HoldLots(ctx, tx, recall, []contracts.LotHold{{LotID: e1, Kind: "block", Cause: "recall"}})
	})
	if status(e1) != "blocked" {
		t.Errorf("a block over a quarantine = %s", status(e1))
	}
	// The deviation closes: its holds go, the recall's block stays.
	do(func(ctx context.Context, tx pgx.Tx) error {
		return l.ReleaseLotHolds(ctx, tx, deviation, nil, contracts.LotActor{Display: "Closer"})
	})
	if status(e1) != "blocked" || status(e2) != "available" {
		t.Errorf("after the deviation's release: %s, %s", status(e1), status(e2))
	}
	do(func(ctx context.Context, tx pgx.Tx) error {
		return l.ReleaseLotHolds(ctx, tx, recall, []int64{e1}, contracts.LotActor{})
	})
	if status(e1) != "available" {
		t.Errorf("after both releases: %s", status(e1))
	}
	if got := eventKinds(t, h); !slices.Contains(got, "lot_held") || !slices.Contains(got, "lot_released") || !slices.Contains(got, "lot_created") {
		t.Errorf("events = %v", got)
	}
	// An unknown lot is refused.
	err := inTx(t, h, func(tx pgx.Tx) error {
		return l.HoldLots(t.Context(), tx, deviation, []contracts.LotHold{{LotID: 999999, Kind: "block", Cause: "other"}})
	})
	if refusalCode(err) != contracts.StockLotUnknown {
		t.Errorf("an unknown lot = %v", err)
	}
}

// TestLots_UsableOnHand is what promises read (M63): a reserve refused by a
// held lot and by an expired one, a hold on reserved stock leaving available
// negative, Stock.Available on usable and Stock.OnHand on all.
func TestLots_UsableOnHand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	e1, e2, e3 := receiveLots(t, h, c, s)
	l := ledgerOf(h)
	site := s.site.Id
	reserve := func(today time.Time, q string) error {
		return inTx(t, h, func(tx pgx.Tx) error {
			return l.Reserve(inCallerTx(t.Context()), tx, order, site, today, []contracts.StockLine{{VariantID: varLot, LineID: 1, Quantity: q}})
		})
	}
	if err := reserve(testToday, "15"); err != nil {
		t.Fatalf("reserve all 15: %v", err)
	}
	mustRelease := func() {
		if err := inTx(t, h, func(tx pgx.Tx) error { return l.Release(t.Context(), tx, order, nil) }); err != nil {
			t.Fatal(err)
		}
	}
	mustRelease()
	// E1 expired by 2026-10-02: 10 usable.
	if err := reserve(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), "11"); refusalCode(err) != contracts.StockInsufficientAvailable {
		t.Errorf("a reserve over an expired lot = %v", err)
	}
	if err := l.Reserve(t.Context(), nil, order, site, time.Time{}, nil); err == nil || refusalCode(err) != "" {
		t.Errorf("a reserve without today = %v, want a failure", err)
	}
	// Reserve 12, then hold E2: available 15 - 5 held - 12 = -2.
	if err := reserve(testToday, "12"); err != nil {
		t.Fatal(err)
	}
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.HoldLots(t.Context(), tx, contracts.StockSource{Module: "inventory", Kind: "lot", ID: e2}, []contracts.LotHold{{LotID: e2, Kind: "block", Cause: "complaint"}})
	}); err != nil {
		t.Fatal(err)
	}
	if got := availableAt(t, h, varLot, site); got != "-2" {
		t.Errorf("available = %s, want -2", got)
	}
	if err := reserve(testToday, "1"); refusalCode(err) != contracts.StockInsufficientAvailable {
		t.Errorf("a reserve against held stock = %v", err)
	}
	q, err := stockOf(h).OnHand(t.Context(), []int32{varLot}, &site)
	if err != nil || q[0].Quantity != "15" {
		t.Errorf("on hand = %v, %v, want 15", q, err)
	}
	// Stock.Lots: every lot in FEFO order with its status.
	lots, err := stockOf(h).Lots(t.Context(), []int32{varLot}, site)
	if err != nil || len(lots) != 3 || lots[0].LotID != e1 || lots[1].LotID != e2 || lots[2].LotID != e3 || lots[1].Status != contracts.LotBlocked ||
		lots[2].Expiry != nil || lots[0].Quantity != "5" {
		t.Errorf("lots = %+v, %v", lots, err)
	}
	// The proposal over them skips the blocked E2.
	picks, short, err := contracts.ProposeLots(lots, "7", testToday)
	if err != nil || short != "" || !slices.Equal(picks, []contracts.LotPick{{LotID: e1, LocationID: s.shelf1.Id, Quantity: "5"}, {LotID: e3, LocationID: s.shelf1.Id, Quantity: "2"}}) {
		t.Errorf("proposal = %+v short %q, %v", picks, short, err)
	}
}

// TestLots_EnsureLotsAndProduce is another module's lots (M67, M80):
// EnsureLots made and found again by number, a stated date compared, an
// untracked variant refused; Produce into the lot, replayed.
func TestLots_EnsureLotsAndProduce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	l := ledgerOf(h)
	mo := contracts.StockSource{Module: "manufacturing", Kind: "order", ID: 3}
	ensure := func(lots ...contracts.NewLot) ([]int64, error) {
		var ids []int64
		err := inTx(t, h, func(tx pgx.Tx) error {
			var err error
			ids, err = l.EnsureLots(inCallerTx(t.Context()), tx, mo, testToday, lots)
			return err
		})
		return ids, err
	}
	ids, err := ensure(contracts.NewLot{VariantID: varLot, Number: "MO-3", ProductionDate: testToday}, contracts.NewLot{VariantID: varLot, ProductionDate: testToday})
	if err != nil || len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("EnsureLots = %v, %v", ids, err)
	}
	again, err := ensure(contracts.NewLot{VariantID: varLot, Number: "mo-3", ProductionDate: testToday.AddDate(0, 0, 1)})
	if err != nil || again[0] != ids[0] {
		t.Errorf("again by number = %v, %v, want %d", again, err, ids[0])
	}
	if got := modtest.One[string](t, h.Harness, `SELECT expiry::text || ' ' || origin_module || '/' || origin_kind || '/' || origin_id FROM inventory.lots WHERE id = $1`, ids[0]); got != "2026-10-12 manufacturing/order/3" {
		t.Errorf("the made lot = %s", got)
	}
	// A lot that must be new: an existing number, or one named twice in the
	// call, is lot_exists at its own index; a free number is made.
	if _, err := ensure(contracts.NewLot{VariantID: varLot, Number: "MO-3", ProductionDate: testToday, MustCreate: true}); refusalCode(err) != contracts.StockLotExists {
		t.Errorf("an existing lot that must be new = %v", err)
	}
	_, err = ensure(contracts.NewLot{VariantID: varLot, Number: "MO-5", ProductionDate: testToday, MustCreate: true},
		contracts.NewLot{VariantID: varLot, Number: "mo-5", ProductionDate: testToday, MustCreate: true})
	var twice *contracts.StockRefusal
	if !errors.As(err, &twice) || len(twice.Lines) != 1 || twice.Lines[0].Index != 1 || twice.Lines[0].Code != contracts.StockLotExists {
		t.Errorf("one new number named twice = %v", err)
	}
	if fresh, err := ensure(contracts.NewLot{VariantID: varLot, Number: "MO-4", ProductionDate: testToday, MustCreate: true}); err != nil || fresh[0] == ids[0] {
		t.Errorf("a new lot that must be new = %v, %v", fresh, err)
	}
	other := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := ensure(contracts.NewLot{VariantID: varLot, Number: "MO-3", ProductionDate: testToday, Expiry: &other}); refusalCode(err) != contracts.StockLotDatesDiffer {
		t.Errorf("a differing expiry = %v", err)
	}
	if _, err := ensure(contracts.NewLot{VariantID: varBolt, Number: "X", ProductionDate: testToday}); refusalCode(err) != contracts.StockLotNotAllowed {
		t.Errorf("an untracked variant = %v", err)
	}
	if err := inTx(t, h, func(tx pgx.Tx) error {
		_, err := l.EnsureLots(t.Context(), tx, mo, time.Time{}, []contracts.NewLot{{VariantID: varLot, ProductionDate: testToday}})
		return err
	}); err == nil || refusalCode(err) != "" {
		t.Errorf("no today = %v, want a failure", err)
	}
	// Produce into the lot, consuming an untracked component, and again.
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10))
	p := contracts.StockProduction{Document: contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 1}, SiteID: s.site.Id, Today: testToday,
		Consume: []contracts.ConsumeLine{{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "2"}},
		Produce: []contracts.ProduceLine{{VariantID: varLot, LineID: 3, ToLocationID: s.shelf2.Id, Quantity: "4", LotID: ids[0]}}}
	produce := func() (contracts.StockPosted, error) {
		var posted contracts.StockPosted
		err := inTx(t, h, func(tx pgx.Tx) error {
			var err error
			posted, err = l.Produce(inCallerTx(t.Context()), tx, mo, p)
			return err
		})
		return posted, err
	}
	first, err := produce()
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	second, err := produce()
	if err != nil || second.MovementID != first.MovementID || lotOnHand(t, h, ids[0], s.shelf2.Id) != "4.000" {
		t.Errorf("a replayed Produce = %+v, %v; on hand %s", second, err, lotOnHand(t, h, ids[0], s.shelf2.Id))
	}
	// A produce line of the tracked variant without its lot.
	p.Document.ID, p.Produce[0].LotID = 2, 0
	if _, err := produce(); refusalCode(err) != contracts.StockLotRequired {
		t.Errorf("no lot on a produce line = %v", err)
	}
	// An unbuild may consume a held lot.
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.HoldLots(t.Context(), tx, contracts.StockSource{Module: "inventory", Kind: "lot", ID: ids[0]}, []contracts.LotHold{{LotID: ids[0], Kind: "block", Cause: "recall"}})
	}); err != nil {
		t.Fatal(err)
	}
	unbuild := contracts.StockProduction{Document: contracts.StockSource{Module: "manufacturing", Kind: "unbuild", ID: 1}, SiteID: s.site.Id, Today: testToday,
		Consume: []contracts.ConsumeLine{{VariantID: varLot, LineID: 3, FromLocationID: s.shelf2.Id, Quantity: "1", LotID: ids[0]}},
		Produce: []contracts.ProduceLine{{VariantID: varLot, LineID: 3, ToLocationID: s.shelf1.Id, Quantity: "1", LotID: ids[1]}}}
	p = unbuild
	if _, err := produce(); refusalCode(err) != contracts.StockLotBlocked {
		t.Errorf("a production consuming a blocked lot = %v", err)
	}
	p.Unbuild = true
	posted, err := produce()
	if err != nil {
		t.Fatalf("an unbuild of a blocked lot: %v", err)
	}
	if kind := modtest.One[string](t, h.Harness, `SELECT kind FROM inventory.movements WHERE id = $1`, posted.MovementID); kind != "unbuild" {
		t.Errorf("the unbuild's kind = %s", kind)
	}
	// What it restored is quarantined by the unbuild, with the blocked lot's
	// cause, so held material does not come back usable (M123).
	if got := modtest.One[string](t, h.Harness, `SELECT l.status || ' ' || h.kind || ' ' || h.cause || ' ' || h.source_kind || '/' || h.source_id
		FROM inventory.lots l JOIN inventory.lot_holds h ON h.lot_id = l.id AND h.released_at IS NULL WHERE l.id = $1`, ids[1]); got != "quarantine quarantine recall unbuild/1" {
		t.Errorf("the restored lot = %s", got)
	}
	assertLedgerConsistent(t, h)
}

// TestLots_HoldRacesPosting is the extended lock order under a hold racing
// postings (M58, M64) on a pool of four: issues, receipts into the lot and
// holds on it, all at once, end without a deadlock and the ledger agrees.
func TestLots_HoldRacesPosting(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	e1, e2, _ := receiveLots(t, h, c, s)
	l := ledgerOf(h)
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Go(func() {
			doc := int64(100 + i)
			_, err := issueOf(t, h, doc, contracts.StockIssue{SiteID: s.site.Id, Today: testToday, Lines: []contracts.IssueLine{
				{VariantID: varLot, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "0.5", LotID: e1},
				{VariantID: varLot, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "0.5", LotID: e2}}})
			if err != nil && refusalCode(err) != contracts.StockLotBlocked {
				t.Errorf("issue: %v", err)
			}
		})
		wg.Go(func() {
			src := contracts.StockSource{Module: "manufacturing", Kind: "deviation", ID: int64(i + 1)}
			err := inTx(t, h, func(tx pgx.Tx) error {
				return l.HoldLots(t.Context(), tx, src, []contracts.LotHold{{LotID: e2, Kind: "quarantine", Cause: "deviation"}, {LotID: e1, Kind: "quarantine", Cause: "deviation"}})
			})
			if err == nil {
				err = inTx(t, h, func(tx pgx.Tx) error { return l.ReleaseLotHolds(t.Context(), tx, src, nil, contracts.LotActor{}) })
			}
			if err != nil {
				t.Errorf("hold: %v", err)
			}
		})
		wg.Go(func() {
			if st := postReceipt(c, s.site.Id, s.shelf2.Id, gen.InventoryReceiptLine{VariantId: varLot, Quantity: qty(1), LotId: &e2}).Status; st != http.StatusCreated {
				t.Errorf("receipt = %d", st)
			}
		})
	}
	wg.Wait()
	if got := modtest.One[string](t, h.Harness, `SELECT string_agg(status, ',') FROM inventory.lots WHERE id IN ($1, $2)`, e1, e2); got != "available,available" {
		t.Errorf("statuses after every hold released = %s", got)
	}
	assertLedgerConsistent(t, h)
}

// TestLots_SerialReserveLoad is a reserve against 10,000 serial balances at
// one site (M65b), timed: the reserve locks every one of the variant's rows
// there, and the row locks are kept while that stays under 50 ms on CI.
func TestLots_SerialReserveLoad(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	const n = 10000
	h.Exec(t, `INSERT INTO inventory.lots (variant_id, number, serial, received_at, sku, product_name, created_at, updated_at)
SELECT $1, 'SN' || lpad(g::text, 6, '0'), true, now(), 'PUMP-1', 'Pump', now(), now() FROM generate_series(1, $2) g`, varSerial, n)
	h.Exec(t, `INSERT INTO inventory.balances (variant_id, location_id, lot_id, on_hand, updated_at)
SELECT $1, $2, id, 1, now() FROM inventory.lots WHERE variant_id = $1`, varSerial, s.shelf1.Id)
	l := ledgerOf(h)
	var best time.Duration
	var runs []time.Duration
	for i := range 3 {
		start := time.Now()
		err := inTx(t, h, func(tx pgx.Tx) error {
			return l.Reserve(t.Context(), tx, contracts.StockSource{Module: "orders", Kind: "order", ID: int64(50 + i)}, s.site.Id, testToday,
				[]contracts.StockLine{{VariantID: varSerial, LineID: 1, Quantity: "3"}})
		})
		took := time.Since(start)
		if err != nil {
			t.Fatalf("reserve: %v", err)
		}
		runs = append(runs, took)
		if best == 0 || took < best {
			best = took
		}
	}
	// The measured figure, for the reference page's M65b note: CI's log says
	// what the design's 50 ms is judged against.
	t.Logf("a reserve against %d serial balances took %v at best (runs %v)", n, best, runs)
	// The design's bound is 50 ms on the CI database; a loaded run — CI's
	// race detector, a busy laptop — is given room, so only an order of
	// magnitude beyond it fails.
	if best > 500*time.Millisecond {
		t.Errorf("a reserve against %d serials took %v", n, best)
	}
}

// TestLots_NoContractCallInsideTheCallersTransaction is rule 24 (M62): every
// ledger call made inside another module's transaction makes no contract
// call of its own — the harness fails the test on any (inCallerTx).
func TestLots_NoContractCallInsideTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	e1, _, _ := receiveLots(t, h, c, s)
	l := ledgerOf(h)
	src := contracts.StockSource{Module: "manufacturing", Kind: "order", ID: 9}
	err := inTx(t, h, func(tx pgx.Tx) error {
		ctx := inCallerTx(t.Context())
		if err := l.Reserve(ctx, tx, order, s.site.Id, testToday, []contracts.StockLine{{VariantID: varLot, LineID: 1, Quantity: "1"}}); err != nil {
			return err
		}
		if err := l.ReserveForProduction(ctx, tx, src, s.site.Id, testToday, []contracts.StockLine{{VariantID: varLot, LineID: 1, Quantity: "1"}}); err != nil {
			return err
		}
		if _, err := l.Issue(ctx, tx, order, contracts.StockIssue{SiteID: s.site.Id, Today: testToday, ExpiredOverride: true, Lines: []contracts.IssueLine{
			{VariantID: varLot, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "1", AutoLot: true}}}); err != nil {
			return err
		}
		ids, err := l.EnsureLots(ctx, tx, src, testToday, []contracts.NewLot{{VariantID: varLot, ProductionDate: testToday}})
		if err != nil {
			return err
		}
		if _, err := l.Produce(ctx, tx, src, contracts.StockProduction{SiteID: s.site.Id, Today: testToday,
			Consume: []contracts.ConsumeLine{{VariantID: varLot, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "1", LotID: e1}},
			Produce: []contracts.ProduceLine{{VariantID: varLot, LineID: 9, ToLocationID: s.shelf2.Id, Quantity: "1", LotID: ids[0]}}}); err != nil {
			return err
		}
		if err := l.HoldLots(ctx, tx, src, []contracts.LotHold{{LotID: ids[0], Kind: "quarantine", Cause: "inspection"}}); err != nil {
			return err
		}
		return l.ReleaseLotHolds(ctx, tx, src, nil, contracts.LotActor{})
	})
	if err != nil {
		t.Fatalf("the ledger calls: %v", err)
	}
}

// TestLots_Meta is the app's start (M62, M68): today in the workspace zone,
// and whether the caller may take an expired lot.
func TestLots_Meta(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, stockViewer...)
	res := c.Do(http.MethodGet, metaPath, nil)
	mustStatus(t, res, http.StatusOK, "meta")
	var m gen.InventoryMeta
	res.JSON(&m)
	if m.CanOverrideExpired || m.Today.Format(time.DateOnly) != h.Now().Format(time.DateOnly) {
		t.Errorf("meta = %+v", m)
	}
	o, _ := signIn(t, h, append(slices.Clone(stockViewer), "inventory:expired-override")...)
	res = o.Do(http.MethodGet, metaPath, nil)
	res.JSON(&m)
	if !m.CanOverrideExpired {
		t.Errorf("the override's holder: meta = %+v", m)
	}
}

// TestLots_GeneratedNumberSkipsATypedOne is a generated number another
// posting typed already (H1): L1001 typed on a receipt, then a receipt with no
// number — it makes a lot of its own with the next free number rather than
// adding to the typed one, in this posting and in an EnsureLots alike.
func TestLots_GeneratedNumberSkipsATypedOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 4, gen.InventoryNewLot{Number: ptr("L1001")}),
		lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("l1002")}))
	typed := lotID(t, h, varLot, "L1001")

	m := receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 2, gen.InventoryNewLot{}))
	if n := m.Lines[0].Lot.Number; n != "L1003" || m.Lines[0].Lot.Id == typed {
		t.Errorf("the generated lot = %s (%d), want a new L1003", n, m.Lines[0].Lot.Id)
	}
	if got := lotOnHand(t, h, typed, s.shelf1.Id); got != "4.000" {
		t.Errorf("the typed L1001 holds %s, want its own 4", got)
	}
	// The same number typed and generated in one call: each its own lot.
	var ids []int64
	if err := inTx(t, h, func(tx pgx.Tx) error {
		var err error
		ids, err = ledgerOf(h).EnsureLots(inCallerTx(t.Context()), tx, contracts.StockSource{Module: "manufacturing", Kind: "order", ID: 1}, testToday,
			[]contracts.NewLot{{VariantID: varLot, ProductionDate: testToday}, {VariantID: varLot, Number: "L1004", ProductionDate: testToday}})
		return err
	}); err != nil {
		t.Fatalf("EnsureLots: %v", err)
	}
	if len(ids) != 2 || ids[0] == ids[1] || ids[1] != lotID(t, h, varLot, "L1004") {
		t.Errorf("EnsureLots = %v, want a generated lot apart from the typed L1004", ids)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT number FROM inventory.lots WHERE id = $1`, ids[0]); got != "L1005" {
		t.Errorf("the generated lot is %s, want L1005", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.lots WHERE variant_id = $1`, varLot); n != 5 {
		t.Errorf("%d lots, want 5", n)
	}
	assertLedgerConsistent(t, h)
}

// TestLots_ReplayRemovesItsLotsEvents is a posting that made a lot and lost
// its key to a concurrent first attempt (L9): the lot it made is removed, and
// with it its lot_created event — recorded only once the movement is claimed.
func TestLots_ReplayRemovesItsLotsEvents(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	const n = 6
	key := gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: s.shelf1.Id,
		Lines: []gen.InventoryReceiptLine{lotLine(varLot, 1, gen.InventoryNewLot{})}}
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if st := c.Do(http.MethodPost, receiptsPath, key).Status; st != http.StatusCreated && st != http.StatusOK {
				t.Errorf("receipt = %d", st)
			}
		})
	}
	wg.Wait()
	lots := h.Count(t, `SELECT count(*) FROM inventory.lots WHERE variant_id = $1`, varLot)
	events := h.Count(t, `SELECT count(*) FROM inventory.events WHERE entity_kind = 'lot' AND kind = 'lot_created'`)
	if lots != 1 || events != 1 {
		t.Errorf("%d lots and %d lot_created events after %d attempts of one key, want 1 and 1", lots, events, n)
	}
}

// TestLots_StockPagesReadUsable is the stock pages on usable on hand (M3,
// M63): a held lot lowers a site's available on the variant's stock and on
// the stock at sites, on hand stays all of it, and the placement's low agrees
// with the low-stock list.
func TestLots_StockPagesReadUsable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	_, e2, _ := receiveLots(t, h, c, s)
	setPlacement(t, c, varLot, s.site.Id, reorderAt(12))
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return ledgerOf(h).HoldLots(t.Context(), tx, contracts.StockSource{Module: "inventory", Kind: "lot", ID: e2}, []contracts.LotHold{{LotID: e2, Kind: "block", Cause: "complaint"}})
	}); err != nil {
		t.Fatal(err)
	}
	res := c.Do(http.MethodGet, "/api/v1/inventory/variants/"+strconv.Itoa(int(varLot))+"/stock", nil)
	mustStatus(t, res, http.StatusOK, "the variant's stock")
	var vs gen.InventoryVariantStock
	res.JSON(&vs)
	if len(vs.Sites) != 1 || vs.Sites[0].OnHand != 15 || vs.Sites[0].Available != 10 || vs.TotalOnHand != 15 || vs.TotalAvailable != 10 {
		t.Fatalf("the variant's stock = %+v, want 15 on hand and 10 available", vs)
	}
	if p := vs.Sites[0].Placement; p == nil || !p.Low {
		t.Errorf("the placement = %+v, want low: 10 usable is at or below 12", p)
	}
	res = c.Do(http.MethodGet, "/api/v1/inventory/stock/sites?variantIds="+strconv.Itoa(int(varLot))+"&siteIds="+strconv.FormatInt(s.site.Id, 10), nil)
	mustStatus(t, res, http.StatusOK, "the stock at sites")
	var at gen.InventoryStockAtSites
	res.JSON(&at)
	if len(at.Stock) != 1 || at.Stock[0].OnHand != 15 || at.Stock[0].Available != 10 || at.Stock[0].Placement == nil || !at.Stock[0].Placement.Low {
		t.Errorf("the stock at sites = %+v", at.Stock)
	}
	if got := lowSKUs(lowStock(t, c, "")); !slices.Equal(got, []string{s.site.Code + ":MILK-1"}) {
		t.Errorf("the low-stock list = %v, want the placement the stock page calls low", got)
	}
	if got := availableAt(t, h, varLot, s.site.Id); got != "10" {
		t.Errorf("Stock.Available = %s, want the stock page's 10", got)
	}
}

// TestLots_EnsureLotsThenHold is a production's EnsureLots and HoldLots on
// one lot in one transaction (M4, M86): EnsureLots takes the lot FOR NO KEY
// UPDATE, so the hold needs no lock upgrade — another transaction cannot even
// share it meanwhile — and two such transactions on one number at once both
// end without a deadlock.
func TestLots_EnsureLotsThenHold(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	seedLots(t, h, c)
	l := ledgerOf(h)
	produce := func(doc int64) error {
		src := contracts.StockSource{Module: "manufacturing", Kind: "order", ID: doc}
		return inTx(t, h, func(tx pgx.Tx) error {
			ids, err := l.EnsureLots(t.Context(), tx, src, testToday, []contracts.NewLot{{VariantID: varLot, Number: "OUT-1", ProductionDate: testToday}})
			if err != nil {
				return err
			}
			return l.HoldLots(t.Context(), tx, src, []contracts.LotHold{{LotID: ids[0], Kind: "quarantine", Cause: "deviation"}})
		})
	}
	if err := produce(1); err != nil {
		t.Fatalf("the first: %v", err)
	}
	lot := lotID(t, h, varLot, "OUT-1")
	// Inside an EnsureLots' transaction, the lot cannot be shared.
	if err := inTx(t, h, func(tx pgx.Tx) error {
		if _, err := l.EnsureLots(t.Context(), tx, contracts.StockSource{Module: "manufacturing", Kind: "order", ID: 2}, testToday,
			[]contracts.NewLot{{VariantID: varLot, Number: "OUT-1", ProductionDate: testToday}}); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		other, err := h.Pool().Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = other.Rollback(ctx) }()
		_, err = other.Exec(ctx, `SELECT 1 FROM inventory.lots WHERE id = $1 FOR SHARE NOWAIT`, lot)
		if err == nil {
			t.Error("another transaction shared the lot EnsureLots holds: it was taken FOR SHARE, not FOR NO KEY UPDATE")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			if err := produce(int64(10 + i)); err != nil {
				t.Errorf("EnsureLots then HoldLots: %v", err)
			}
		})
	}
	wg.Wait()
}

// TestLots_StockLinesCountLocations is a variant in two lots on one location
// and a third lot on another (LM6): it lies on two locations, in two stock
// lines — the holder's count, the site's figure and the dashboard's.
func TestLots_StockLinesCountLocations(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("A")}), lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("B")}))
	receive(t, c, s.site.Id, s.shelf2.Id, lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("C")}))
	if err := inTx(t, h, func(tx pgx.Tx) error {
		held, err := inventory.Module().VariantReferences(h.Deps()).VariantHeld(t.Context(), tx, varLot, contracts.VariantHoldDelete)
		if err != nil || held == nil || held.Locations != 2 || held.Quantity != "3" {
			t.Errorf("VariantHeld = %+v, %v; want 3 at 2 locations", held, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	res := c.Do(http.MethodGet, sitesPath, nil)
	mustStatus(t, res, http.StatusOK, "the sites")
	var sites gen.InventorySiteList
	res.JSON(&sites)
	for _, site := range sites.Sites {
		if site.Id == s.site.Id && (site.StockLines == nil || *site.StockLines != 2) {
			t.Errorf("the site's stock lines = %d, want 2", ptrValue(site.StockLines))
		}
	}
	res = c.Do(http.MethodGet, statsPath, nil)
	mustStatus(t, res, http.StatusOK, "the stats")
	var st gen.InventoryStats
	res.JSON(&st)
	if st.StockLines != 2 {
		t.Errorf("the dashboard's stock lines = %d, want 2", st.StockLines)
	}
}

// TestLots_AutoLotLocksWhatCovers is an AutoLot line locking only the lots
// that look to cover it (L8): eight lots of 5, an issue of 20 locks the first
// four and two more, so a lot past them held by another transaction does not
// stop it; and when three of the first are blocked under its read, it runs
// again with every lot and takes the next ones FEFO.
func TestLots_AutoLotLocksWhatCovers(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	lines := make([]gen.InventoryReceiptLine, 0, 8)
	for i := range 8 {
		lines = append(lines, lotLine(varLot, 5, gen.InventoryNewLot{Number: ptr("F" + strconv.Itoa(i+1)), Expiry: date(2026, 11, 1+i)}))
	}
	receive(t, c, s.site.Id, s.shelf1.Id, lines...)
	lot := func(i int) int64 { return lotID(t, h, varLot, "F"+strconv.Itoa(i)) }
	auto := func(doc int64, q string) (contracts.StockPosted, error) {
		return issueOf(t, h, doc, contracts.StockIssue{SiteID: s.site.Id, Today: testToday, Lines: []contracts.IssueLine{
			{VariantID: varLot, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: q, AutoLot: true}}})
	}
	// F8 locked by another transaction: an issue of 5 never waits for it.
	ctx := t.Context()
	holder, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, `SELECT 1 FROM inventory.lots WHERE id = $1 FOR UPDATE`, lot(8)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := auto(1, "5"); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("an issue past a lot locked elsewhere: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("an issue of 5 waited for F8, a lot it does not need")
	}
	_ = holder.Rollback(ctx)

	// F2–F4 blocked under the read of the next issue's candidates: it locks
	// F2–F7, finds F5–F7's 15 short of 20, and runs again with F8 too.
	blocker, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx, `UPDATE inventory.lots SET status = 'blocked' WHERE id = ANY($1)`, []int64{lot(2), lot(3), lot(4)}); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		posted contracts.StockPosted
		err    error
	}
	got := make(chan outcome, 1)
	go func() { p, err := auto(2, "20"); got <- outcome{p, err} }()
	for waited := 0; ; waited++ {
		if h.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) > 0 {
			break
		}
		if waited == 200 {
			t.Fatal("the issue never waited for the blocked lots")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	o := <-got
	want := []contracts.StockPick{{Index: 0, LotID: lot(5), LocationID: s.shelf1.Id, Quantity: "5"}, {Index: 0, LotID: lot(6), LocationID: s.shelf1.Id, Quantity: "5"},
		{Index: 0, LotID: lot(7), LocationID: s.shelf1.Id, Quantity: "5"}, {Index: 0, LotID: lot(8), LocationID: s.shelf1.Id, Quantity: "5"}}
	if o.err != nil || !slices.Equal(o.posted.Picks, want) {
		t.Errorf("an issue whose first lots were blocked under it = %+v, %v; want %+v", o.posted.Picks, o.err, want)
	}
	assertLedgerConsistent(t, h)
}

// ptrValue is *p, the zero value for nil.
func ptrValue[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
