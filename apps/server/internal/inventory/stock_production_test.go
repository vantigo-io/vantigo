package inventory_test

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// The ledger's production (manufacturing design M1–M3): Produce consumes
// components and puts the output in stock in one movement of kind
// production, and ReserveForProduction reserves at a site that need not
// fulfil orders.

// mo is the manufacturing order most of these tests reserve and produce for.
var mo = contracts.StockSource{Module: "manufacturing", Kind: "order", ID: 5}

// produce runs Produce in a transaction of its own and answers the movement.
func produce(t *testing.T, h *harness, src contracts.StockSource, p contracts.StockProduction) (int64, error) {
	t.Helper()
	var id int64
	err := inTx(t, h, func(tx pgx.Tx) error {
		posted, err := ledgerOf(h).Produce(t.Context(), tx, src, p)
		id = posted.MovementID
		return err
	})
	return id, err
}

func TestStockLedger_ProduceConsumesAndProduces(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10), receiptLine(varNut, 4))
	l := ledgerOf(h)
	site := s.site.Id
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.ReserveForProduction(t.Context(), tx, mo, site, testToday, []contracts.StockLine{
			{VariantID: varBolt, LineID: 1, Quantity: "6"}, {VariantID: varNut, LineID: 2, Quantity: "1"},
		})
	}); err != nil {
		t.Fatalf("ReserveForProduction: %v", err)
	}

	// Line 1 had 6 reserved and consumes 4; line 2 had 1 and consumes 2
	// (floored at 0). The output is a Discontinued variant: still produced.
	record := contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 9}
	p := contracts.StockProduction{Today: testToday, Document: record, SiteID: site, PostedByUserID: &user, PostedByDisplay: "Operator",
		Consume: []contracts.ConsumeLine{
			{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "4"},
			{VariantID: varNut, LineID: 2, FromLocationID: s.shelf1.Id, Quantity: "2"},
		},
		Produce: []contracts.ProduceLine{{VariantID: varOld, LineID: 1, ToLocationID: s.shelf2.Id, Quantity: "1.5"}},
	}
	movementID, err := produce(t, h, mo, p)
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	if got := reserved(t, h, mo); !slices.Equal(got, []string{"1:" + strconv.FormatInt(site, 10) + ":2"}) {
		t.Errorf("reservations after the production = %v, want line 1 at 2", got)
	}
	if onHand(t, h, varBolt, s.shelf1.Id) != "6.000" || onHand(t, h, varNut, s.shelf1.Id) != "2.000" || onHand(t, h, varOld, s.shelf2.Id) != "1.500" {
		t.Errorf("balances = bolt %q, nut %q, output %q", onHand(t, h, varBolt, s.shelf1.Id), onHand(t, h, varNut, s.shelf1.Id), onHand(t, h, varOld, s.shelf2.Id))
	}
	res := c.Do(http.MethodGet, movementPath(movementID), nil)
	mustStatus(t, res, http.StatusOK, "the production")
	var m gen.InventoryMovement
	res.JSON(&m)
	if m.Kind != gen.Production || len(m.Lines) != 3 || m.Source == nil || m.Source.Module != "manufacturing" || m.Source.Kind != "production" ||
		m.Source.Id != "9" || m.Source.LineId == nil || *m.Source.LineId != "5" || m.PostedBy.UserId == nil || *m.PostedBy.UserId != user {
		t.Errorf("movement = %+v", m)
	}
	if len(m.Lines) == 3 && (m.Lines[0].From == nil || m.Lines[0].To != nil || m.Lines[2].From != nil || m.Lines[2].To == nil) {
		t.Errorf("lines = %+v, want the consume lines from-only and the produce line to-only", m.Lines)
	}
	res = c.Do(http.MethodGet, movementsPath+"?kind=production", nil)
	mustStatus(t, res, http.StatusOK, "the productions")
	var page gen.InventoryMovementPage
	res.JSON(&page)
	if len(page.Movements) != 1 || page.Movements[0].Id != movementID {
		t.Errorf("productions = %+v, want the one", page.Movements)
	}

	// The same document again: the first movement, nothing consumed again.
	again, err := produce(t, h, mo, p)
	if err != nil || again != movementID {
		t.Errorf("a second production of the record = %d, %v; want %d", again, err, movementID)
	}
	if got := reserved(t, h, mo); len(got) != 1 || onHand(t, h, varBolt, s.shelf1.Id) != "6.000" {
		t.Errorf("after a replay: reservations %v, bolts %q", got, onHand(t, h, varBolt, s.shelf1.Id))
	}

	// A scrap: consume lines only.
	scrap := contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 10}
	if _, err := produce(t, h, mo, contracts.StockProduction{Today: testToday, Document: scrap, SiteID: site, Consume: []contracts.ConsumeLine{
		{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "1"},
	}}); err != nil {
		t.Fatalf("Produce a scrap: %v", err)
	}
	if onHand(t, h, varBolt, s.shelf1.Id) != "5.000" {
		t.Errorf("bolts after the scrap = %q, want 5", onHand(t, h, varBolt, s.shelf1.Id))
	}

	// No lines, and bad quantities — every such line named — are refusals.
	none := contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 11}
	if _, err := produce(t, h, mo, contracts.StockProduction{Today: testToday, Document: none, SiteID: site}); refusalCode(err) != contracts.StockNothingToPost {
		t.Errorf("Produce of nothing = %v, want nothing_to_post", err)
	}
	_, err = produce(t, h, mo, contracts.StockProduction{Today: testToday, Document: none, SiteID: site,
		Consume: []contracts.ConsumeLine{
			{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "0"},
			{VariantID: varBolt, LineID: 2, FromLocationID: s.shelf1.Id, Quantity: "1"},
		},
		Produce: []contracts.ProduceLine{{VariantID: varOld, LineID: 1, ToLocationID: s.shelf2.Id, Quantity: "0.0001"}},
	})
	var r *contracts.StockRefusal
	if !errors.As(err, &r) || r.Code != contracts.StockQuantityInvalid || len(r.Lines) != 2 || r.Lines[0].LineID != 1 || r.Lines[1].VariantID != varOld {
		t.Errorf("Produce of bad quantities = %+v, want two lines quantity_invalid", err)
	}
	assertLedgerConsistent(t, h)
}

// TestStockLedger_ProduceRefusesAndWritesNothing: every failing line is
// named — beyond on hand, another site's location, a service produced — and
// nothing of the production is written.
func TestStockLedger_ProduceRefusesAndWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 3), receiptLine(varNut, 1))
	site := s.site.Id
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return ledgerOf(h).ReserveForProduction(t.Context(), tx, mo, site, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "2"}})
	}); err != nil {
		t.Fatalf("ReserveForProduction: %v", err)
	}
	movements := h.Count(t, `SELECT count(*) FROM inventory.movements`)
	record := contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 20}

	// Two consume lines beyond on hand, the good one between them left alone.
	_, err := produce(t, h, mo, contracts.StockProduction{Today: testToday, Document: record, SiteID: site,
		Consume: []contracts.ConsumeLine{
			{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "4"},
			{VariantID: varNut, LineID: 2, FromLocationID: s.shelf1.Id, Quantity: "1"},
			{VariantID: varNut, LineID: 3, FromLocationID: s.shelf2.Id, Quantity: "1"},
		},
		Produce: []contracts.ProduceLine{{VariantID: varWasher, LineID: 1, ToLocationID: s.shelf2.Id, Quantity: "1"}},
	})
	var r *contracts.StockRefusal
	if !errors.As(err, &r) || r.Code != contracts.StockInsufficientStock || len(r.Lines) != 2 || r.Lines[0].LineID != 1 || r.Lines[1].LineID != 3 {
		t.Fatalf("Produce beyond on hand = %+v, want lines 1 and 3 insufficient_stock", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.movements`); n != movements {
		t.Errorf("a refused production wrote %d movements", n-movements)
	}
	if onHand(t, h, varBolt, s.shelf1.Id) != "3.000" || onHand(t, h, varNut, s.shelf1.Id) != "1.000" || onHand(t, h, varWasher, s.shelf2.Id) != "" {
		t.Errorf("balances after a refusal = %q, %q, %q", onHand(t, h, varBolt, s.shelf1.Id), onHand(t, h, varNut, s.shelf1.Id), onHand(t, h, varWasher, s.shelf2.Id))
	}
	if got := reserved(t, h, mo); !slices.Equal(got, []string{"1:" + strconv.FormatInt(site, 10) + ":2"}) {
		t.Errorf("reservations after a refusal = %v, want line 1 at 2", got)
	}

	for _, tc := range []struct {
		name string
		p    contracts.StockProduction
		want string
	}{
		{"consumed at another site", contracts.StockProduction{Today: testToday, Consume: []contracts.ConsumeLine{
			{VariantID: varBolt, LineID: 1, FromLocationID: s.elsewhre.RootLocationId, Quantity: "1"},
		}}, contracts.StockLocationUnknown},
		{"produced at another site", contracts.StockProduction{Today: testToday, Produce: []contracts.ProduceLine{
			{VariantID: varWasher, LineID: 1, ToLocationID: s.elsewhre.RootLocationId, Quantity: "1"},
		}}, contracts.StockLocationUnknown},
		{"a service produced", contracts.StockProduction{Today: testToday, Produce: []contracts.ProduceLine{
			{VariantID: varService, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1"},
		}}, contracts.StockVariantNotStocked},
		{"an unknown site", contracts.StockProduction{Today: testToday, SiteID: 999999, Produce: []contracts.ProduceLine{
			{VariantID: varWasher, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1"},
		}}, contracts.StockSiteUnknown},
	} {
		tc.p.Document = record
		if tc.p.SiteID == 0 {
			tc.p.SiteID = site
		}
		_, err := produce(t, h, mo, tc.p)
		if refusalCode(err) != tc.want {
			t.Errorf("Produce %s = %v, want %s", tc.name, err, tc.want)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.movements`); n != movements {
		t.Errorf("refused productions wrote %d movements", n-movements)
	}
	assertLedgerConsistent(t, h)
}

// TestStockLedger_ReserveForProductionAtAPlant: a site that does not fulfil
// orders takes a production's reservation and posts its production, while
// an order's Reserve there is still refused; every other check stays.
func TestStockLedger_ReserveForProductionAtAPlant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	seedCatalog(h)
	res := c.Do(http.MethodPost, sitesPath, gen.InventorySiteInput{Code: "PLANT", Name: "Plant", Kind: gen.Store})
	mustStatus(t, res, http.StatusCreated, "create a site that does not fulfil orders")
	var plant gen.InventorySite
	res.JSON(&plant)
	if plant.FulfilsOrders {
		t.Fatalf("plant = %+v, want it not fulfilling orders", plant)
	}
	receive(t, c, plant.Id, plant.RootLocationId, receiptLine(varBolt, 5))
	l := ledgerOf(h)

	err := inTx(t, h, func(tx pgx.Tx) error {
		return l.Reserve(t.Context(), tx, order, plant.Id, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "1"}})
	})
	if refusalCode(err) != contracts.StockSiteNotFulfilling {
		t.Errorf("Reserve at the plant = %v, want site_not_fulfilling", err)
	}
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.ReserveForProduction(t.Context(), tx, mo, plant.Id, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "4"}})
	}); err != nil {
		t.Fatalf("ReserveForProduction at the plant: %v", err)
	}
	if got := reserved(t, h, mo); !slices.Equal(got, []string{"1:" + strconv.FormatInt(plant.Id, 10) + ":4"}) {
		t.Errorf("reservations = %v, want line 1 at 4", got)
	}
	if got := availableAt(t, h, varBolt, plant.Id); got != "1" {
		t.Errorf("available = %s, want 1", got)
	}

	// Every other check stays: available, goods.
	for _, tc := range []struct {
		name string
		line contracts.StockLine
		want string
	}{
		{"beyond available", contracts.StockLine{VariantID: varBolt, LineID: 2, Quantity: "2"}, contracts.StockInsufficientAvailable},
		{"a service", contracts.StockLine{VariantID: varService, LineID: 3, Quantity: "1"}, contracts.StockVariantNotStocked},
	} {
		err := inTx(t, h, func(tx pgx.Tx) error {
			return l.ReserveForProduction(t.Context(), tx, mo, plant.Id, testToday, []contracts.StockLine{tc.line})
		})
		if refusalCode(err) != tc.want {
			t.Errorf("ReserveForProduction %s = %v, want %s", tc.name, err, tc.want)
		}
	}

	// The plant posts its production, though it fulfils no orders.
	if _, err := produce(t, h, mo, contracts.StockProduction{Today: testToday, SiteID: plant.Id, Consume: []contracts.ConsumeLine{
		{VariantID: varBolt, LineID: 1, FromLocationID: plant.RootLocationId, Quantity: "4"},
	}, Produce: []contracts.ProduceLine{{VariantID: varWasher, LineID: 1, ToLocationID: plant.RootLocationId, Quantity: "2"}}}); err != nil {
		t.Fatalf("Produce at the plant: %v", err)
	}
	if got := reserved(t, h, mo); got != nil {
		t.Errorf("reservations after the production = %v, want none", got)
	}

	// Release treats the reservation as any other; an archived site is
	// refused.
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.ReserveForProduction(t.Context(), tx, mo, plant.Id, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "1"}})
	}); err != nil {
		t.Fatalf("ReserveForProduction again: %v", err)
	}
	if err := inTx(t, h, func(tx pgx.Tx) error { return l.Release(t.Context(), tx, mo, nil) }); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := reserved(t, h, mo); got != nil {
		t.Errorf("reservations after the release = %v, want none", got)
	}
	h.Exec(t, `UPDATE inventory.sites SET status = 'archived' WHERE id = $1`, plant.Id)
	err = inTx(t, h, func(tx pgx.Tx) error {
		return l.ReserveForProduction(t.Context(), tx, mo, plant.Id, testToday, []contracts.StockLine{{VariantID: varWasher, LineID: 1, Quantity: "1"}})
	})
	if refusalCode(err) != contracts.StockSiteArchived {
		t.Errorf("ReserveForProduction at an archived site = %v, want site_archived", err)
	}
	archived := contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 31}
	_, err = produce(t, h, mo, contracts.StockProduction{Today: testToday, Document: archived, SiteID: plant.Id, Consume: []contracts.ConsumeLine{
		{VariantID: varWasher, LineID: 1, FromLocationID: plant.RootLocationId, Quantity: "1"},
	}})
	if refusalCode(err) != contracts.StockSiteArchived {
		t.Errorf("Produce at an archived site = %v, want site_archived", err)
	}
	assertLedgerConsistent(t, h)
}

// TestStockLedger_ProduceBodyAndBounds: a replay with another body is
// document_reused, a produce line beyond the largest balance is
// quantity_invalid, and a Discontinued variant may be consumed.
func TestStockLedger_ProduceBodyAndBounds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 10))
	site := s.site.Id
	record := contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 30}
	p := contracts.StockProduction{Today: testToday, Document: record, SiteID: site,
		Consume: []contracts.ConsumeLine{{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "2"}},
		Produce: []contracts.ProduceLine{{VariantID: varOld, LineID: 1, ToLocationID: s.shelf2.Id, Quantity: "3"}},
	}
	first, err := produce(t, h, mo, p)
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}

	// The same lines with another quantity, and the same with another order:
	// the first posting stands and the answer is the contract's refusal.
	other := p
	other.Consume = []contracts.ConsumeLine{{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "3"}}
	if _, err := produce(t, h, mo, other); refusalCode(err) != contracts.StockDocumentReused {
		t.Errorf("Produce with another quantity = %v, want document_reused", err)
	}
	if again, err := produce(t, h, mo, p); err != nil || again != first {
		t.Errorf("the same body again = %d, %v; want %d", again, err, first)
	}
	issueDoc := contracts.StockSource{Module: "orders", Kind: "delivery", ID: 31}
	issue := contracts.StockIssue{Today: testToday, Document: issueDoc, SiteID: site, Lines: []contracts.IssueLine{{VariantID: varBolt, LineID: 1, FromLocationID: s.shelf1.Id, Quantity: "1"}}}
	if err := inTx(t, h, func(tx pgx.Tx) error { _, err := ledgerOf(h).Issue(t.Context(), tx, order, issue); return err }); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	issue.Lines[0].Quantity = "2"
	err = inTx(t, h, func(tx pgx.Tx) error { _, err := ledgerOf(h).Issue(t.Context(), tx, order, issue); return err })
	if refusalCode(err) != contracts.StockDocumentReused {
		t.Errorf("Issue with another quantity = %v, want document_reused", err)
	}

	// The Discontinued output is now on hand, and consumed in a later
	// production.
	if _, err := produce(t, h, mo, contracts.StockProduction{Today: testToday, Document: contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 32}, SiteID: site,
		Consume: []contracts.ConsumeLine{{VariantID: varOld, LineID: 2, FromLocationID: s.shelf2.Id, Quantity: "1"}},
	}); err != nil {
		t.Fatalf("Produce consuming a Discontinued variant: %v", err)
	}
	if got := onHand(t, h, varOld, s.shelf2.Id); got != "2.000" {
		t.Errorf("Discontinued on hand = %q, want 2", got)
	}

	// A produce line beyond the largest balance is a bad quantity, and
	// the caller rolls back.
	_, err = produce(t, h, mo, contracts.StockProduction{Today: testToday, Document: contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 33}, SiteID: site,
		Produce: []contracts.ProduceLine{{VariantID: varOld, LineID: 3, ToLocationID: s.shelf2.Id, Quantity: "99999999999.999"}},
	})
	if refusalCode(err) != contracts.StockQuantityInvalid {
		t.Errorf("Produce beyond the largest balance = %v, want quantity_invalid", err)
	}
	if got := onHand(t, h, varOld, s.shelf2.Id); got != "2.000" {
		t.Errorf("Discontinued on hand after the refusal = %q, want 2", got)
	}
	assertLedgerConsistent(t, h)
}

// TestStockLedger_RefusedReservesWriteNothing: Reserve and ReserveForProduction
// write nothing when they refuse, so the caller may commit the transaction
// anyway (the contract's rule; Issue and Produce are rolled back instead).
func TestStockLedger_RefusedReservesWriteNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 2))
	l := ledgerOf(h)
	site := s.site.Id
	for name, reserve := range map[string]func(tx pgx.Tx) error{
		"Reserve": func(tx pgx.Tx) error {
			return l.Reserve(t.Context(), tx, order, site, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "1"}, {VariantID: varBolt, LineID: 2, Quantity: "5"}})
		},
		"ReserveForProduction": func(tx pgx.Tx) error {
			return l.ReserveForProduction(t.Context(), tx, mo, site, testToday, []contracts.StockLine{{VariantID: varBolt, LineID: 1, Quantity: "1"}, {VariantID: varBolt, LineID: 2, Quantity: "5"}})
		},
	} {
		// The refusal is swallowed and the transaction committed.
		err := inTx(t, h, func(tx pgx.Tx) error {
			if err := reserve(tx); refusalCode(err) != contracts.StockInsufficientAvailable {
				t.Errorf("%s = %v, want insufficient_available", name, err)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if got := reserved(t, h, order); got != nil {
		t.Errorf("a refused Reserve left %v after a commit", got)
	}
	if got := reserved(t, h, mo); got != nil {
		t.Errorf("a refused ReserveForProduction left %v after a commit", got)
	}
	assertLedgerConsistent(t, h)
}
