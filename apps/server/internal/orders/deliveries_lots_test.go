package orders_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// Layer O (manufacturing phase 2 M75–M79): deliveries pick lots — proposed
// FEFO from Stock.Lots, changed by a person, issued one ledger line per lot,
// refused per lot, proposed again once when stale — and print them.

// The tracked variants these tests seed.
const (
	varLot    int32 = 201 // tracked by lot
	varSerial int32 = 202 // tracked by serial number
)

// seedTracked adds the tracked variants to the catalog.
func seedTracked(h *harness) {
	for _, v := range []struct {
		id       int32
		sku      string
		tracking string
	}{{varLot, "MILK-1", contracts.TrackingLot}, {varSerial, "PUMP-1", contracts.TrackingSerial}} {
		h.catalog.seed(contracts.VariantEntry{ID: v.id, ProductID: v.id, ProductName: v.sku, SKU: v.sku, Unit: "pcs", ProductType: "Goods",
			ProductStatus: "Active", TaxCategoryID: catStandard, Tracking: v.tracking})
		h.catalog.setPrice(v.id, "NOK", "10")
	}
}

// day is a date days after the harness's today, at midnight UTC.
func day(h *harness, days int) *time.Time {
	n := h.Now()
	d := time.Date(n.Year(), n.Month(), n.Day()+days, 0, 0, 0, 0, time.UTC)
	return &d
}

// lotsOf is a delivery line's picks as (lot id, location id, quantity).
func lotsOf(l gen.OrdersDeliveryLine) []string {
	out := []string{}
	for _, p := range l.Lots {
		out = append(out, fmt.Sprintf("%d@%d:%v", p.LotId, p.LocationId, p.Quantity))
	}
	return out
}

func postDelivery(c *modtest.Client, d gen.OrdersDelivery) *modtest.Response {
	return c.Do(http.MethodPost, deliveryPath(d.Id, "post"), gen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt})
}

func getDelivery(t *testing.T, c *modtest.Client, id int64) gen.OrdersDelivery {
	t.Helper()
	res := c.Do(http.MethodGet, deliveryPath(id), nil)
	mustStatus(t, res, http.StatusOK, "get delivery")
	return deliveryOf(t, res)
}

// newDelivery confirms an order of the lines without reserving and makes
// its delivery.
func newDelivery(t *testing.T, c *modtest.Client, lines ...gen.OrdersLineInput) (gen.OrdersOrder, gen.OrdersDelivery) {
	t.Helper()
	o := createOrder(t, c, custBusiness, lines...)
	mustStatus(t, confirmWith(t, c, o, false), http.StatusOK, "confirm")
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "create delivery")
	return getOrder(t, c, o.Id), deliveryOf(t, res)
}

// A tracked line is proposed FEFO across two lots at two locations,
// skipping a lot on hold and an expired one; the pick list prints a row per
// lot by location, the post issues one ledger line per lot and consumes the
// line's reservation, the packing slip prints the lots under the line, the
// order's delivery list names them, and a second post changes nothing
// (M75, M76, M78, M79).
func TestDeliveryLots_ProposalSplitsAcrossLotsFEFO(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	seedTracked(h)
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	received := h.Now().AddDate(0, -1, 0)
	h.stock.putLot(varLot, 1, "A1", locA1, "4", day(h, 40), received)
	h.stock.putLot(varLot, 2, "B2", locA2, "3", day(h, 10), received)
	h.stock.putLot(varLot, 3, "Q3", locA1, "10", day(h, 5), received)
	h.stock.setLotStatus(3, contracts.LotQuarantine)
	h.stock.putLot(varLot, 4, "X4", locA1, "10", day(h, -1), received)

	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varLot, 6)))
	src := orderSource(o.Id)
	if h.stock.reservedOf(src, o.Lines[0].Id) != "6" {
		t.Fatalf("confirm reserved %s, want 6", h.stock.reservedOf(src, o.Lines[0].Id))
	}
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "create delivery")
	d := deliveryOf(t, res)
	line := d.Lines[0]
	if !line.Tracked || line.FromLocationId != nil || line.Short != 0 {
		t.Fatalf("line %+v, want tracked, no location, not short", line)
	}
	if got := lotsOf(line); !slices.Equal(got, []string{fmt.Sprintf("2@%d:3", locA2), fmt.Sprintf("1@%d:3", locA1)}) {
		t.Fatalf("picks %v, want B2 (earliest usable expiry) then A1", got)
	}
	if line.Lots[0].LotNumber != "B2" || line.Lots[0].LocationCode != "OSL-A-02" || line.Lots[0].Expiry == nil || line.Lots[0].Chosen {
		t.Errorf("pick snapshot %+v", line.Lots[0])
	}
	if len(line.LotChoices) != 4 {
		t.Fatalf("lot choices %+v, want the site's four lots", line.LotChoices)
	}
	for _, ch := range line.LotChoices {
		if ch.LotId == 3 && string(ch.Status) != contracts.LotQuarantine || ch.LotId == 4 && !ch.Expired || ch.LotId == 1 && ch.Expired {
			t.Errorf("choice %+v", ch)
		}
	}
	// The read answers the same.
	if got := getDelivery(t, c, d.Id); !slices.Equal(lotsOf(got.Lines[0]), lotsOf(line)) || len(got.Lines[0].LotChoices) != 4 {
		t.Errorf("read delivery %+v", got.Lines[0])
	}

	mustStatus(t, c.Do(http.MethodGet, deliveryPath(d.Id, "pick-list"), nil), http.StatusOK, "pick list")
	subtitle := fmt.Sprintf("Leveranse %d – Ordre %d", d.Number, o.Number)
	expiry := func(days int) string { return day(h, days).Format("02.01.2006") }
	pick, ok := pdfModels.find("Plukkliste", subtitle, func(m orders.PDFModel) bool { return len(m.Rows) == 2 && strings.HasPrefix(m.Rows[0][1], "MILK-1") })
	if !ok || pick.Header[2] != "Parti" ||
		!slices.Equal(pick.Rows[0][:4], []string{"OSL-A-01", "MILK-1 MILK-1", "A1 (utløper " + expiry(40) + ")", "3"}) ||
		!slices.Equal(pick.Rows[1][:4], []string{"OSL-A-02", "MILK-1 MILK-1", "B2 (utløper " + expiry(10) + ")", "3"}) {
		t.Errorf("pick list %+v %+v, %v; want a row per lot by location", pick.Header, pick.Rows, ok)
	}

	res = postDelivery(c, d)
	mustStatus(t, res, http.StatusOK, "post")
	posted := deliveryOf(t, res)
	if h.stock.lotAt(1, locA1) != "1" || h.stock.lotAt(2, locA2) != "0" || h.stock.lotAt(4, locA1) != "10" {
		t.Errorf("lots after the post: A1 %s B2 %s X4 %s", h.stock.lotAt(1, locA1), h.stock.lotAt(2, locA2), h.stock.lotAt(4, locA1))
	}
	if h.stock.reservedOf(src, o.Lines[0].Id) != "0" {
		t.Errorf("the reservation left %s", h.stock.reservedOf(src, o.Lines[0].Id))
	}
	issue, _ := h.stock.lastIssue()
	if len(issue.Lines) != 2 || issue.Lines[0].LotID != 2 || issue.Lines[1].LotID != 1 || issue.Lines[0].LineID != o.Lines[0].Id ||
		issue.Lines[0].AutoLot || issue.Lines[0].AllowExpired {
		t.Errorf("issue lines %+v, want one per lot on the order line", issue.Lines)
	}
	if !slices.Equal(lotsOf(posted.Lines[0]), lotsOf(line)) || len(posted.Lines[0].LotChoices) != 0 {
		t.Errorf("posted line %+v, want its lots kept and no choices", posted.Lines[0])
	}

	mustStatus(t, c.Do(http.MethodGet, deliveryPath(d.Id, "packing-slip"), nil), http.StatusOK, "packing slip")
	slip, ok := pdfModels.find("Pakkseddel", subtitle, func(m orders.PDFModel) bool { return len(m.Rows) == 2 && strings.HasPrefix(m.Rows[0][0], "MILK-1") })
	want := "Partier: B2 (utløper " + expiry(10) + ") – 3, A1 (utløper " + expiry(40) + ") – 3"
	if !ok || slip.Rows[0][2] != "6" || len(slip.Rows[1]) != 1 || slip.Rows[1][0] != want {
		t.Errorf("packing slip %+v, %v; want the lots under the line: %q", slip.Rows, ok, want)
	}

	res = c.Do(http.MethodGet, deliveriesPath+fmt.Sprintf("?orderId=%d", o.Id), nil)
	var list gen.OrdersDeliveryPage
	res.JSON(&list)
	if len(list.Items) != 1 || !slices.Equal(list.Items[0].Lots, []string{"B2", "A1"}) {
		t.Errorf("delivery list %+v, want its lots", list.Items)
	}

	// Posted once: a second post is refused and issues nothing again.
	expectRefusal(t, postDelivery(c, posted), http.StatusConflict, "delivery_not_draft", "a second post")
	if h.stock.lotAt(1, locA1) != "1" {
		t.Errorf("a second post took A1 to %s", h.stock.lotAt(1, locA1))
	}
}

// A line the lots cannot cover is short and refused (picks_mismatch); a
// line saved with no lots is refused (lot_required); a person's lots are
// judged (lot_unknown, lot_not_allowed) and marked chosen; an expired lot
// chosen is refused for a poster without inventory:expired-override,
// naming the line and the lot row, and posted for one with it (M62, M75,
// M77, M78).
func TestDeliveryLots_EditingShortAndExpired(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	seedTracked(h)
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	received := h.Now().AddDate(0, -1, 0)
	h.stock.putLot(varLot, 1, "E1", locA1, "5", day(h, -2), received)
	h.stock.putLot(varLot, 2, "F2", locA1, "2", nil, received)
	h.stock.put(varBolt, locA1, "5")

	_, d := newDelivery(t, c, productLine(varLot, 4), productLine(varBolt, 1))
	lot, bolt := d.Lines[0], d.Lines[1]
	if got := lotsOf(lot); !slices.Equal(got, []string{fmt.Sprintf("2@%d:2", locA1)}) || lot.Short != 2 {
		t.Fatalf("picks %v short %v, want F2 only (E1 is expired) and 2 short", got, lot.Short)
	}
	if bolt.Tracked || len(bolt.Lots) != 0 || bolt.FromLocationId == nil {
		t.Fatalf("untracked line %+v", bolt)
	}
	p := expectRefusal(t, postDelivery(c, d), http.StatusBadRequest, "picks_mismatch", "a short line")
	if p.Lines == nil || (*p.Lines)[0].Index != 0 {
		t.Errorf("refusal lines %+v", p.Lines)
	}

	save := func(d gen.OrdersDelivery, lots *[]gen.OrdersDeliveryLotInput, boltLots *[]gen.OrdersDeliveryLotInput) *modtest.Response {
		return c.Do(http.MethodPut, deliveryPath(d.Id), gen.OrdersDeliveryUpdate{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt,
			Lines: []gen.OrdersDeliveryLineInput{
				{OrderLineId: lot.OrderLineId, Quantity: 4, Lots: lots},
				{OrderLineId: bolt.OrderLineId, Quantity: 1, FromLocationId: ptrI64(locA1), Lots: boltLots},
			}})
	}
	p = expectRefusal(t, save(d, &[]gen.OrdersDeliveryLotInput{{LotId: 2, LocationId: locA1, Quantity: 2}, {LotId: 99, LocationId: locA1, Quantity: 2}}, nil),
		http.StatusBadRequest, "lot_unknown", "an unknown lot")
	if l := (*p.Lines)[0]; l.Index != 0 || l.LotIndex == nil || *l.LotIndex != 1 || l.LotId == nil || *l.LotId != 99 {
		t.Errorf("refusal line %+v, want line 0's lot row 1", l)
	}
	expectRefusal(t, save(d, nil, &[]gen.OrdersDeliveryLotInput{{LotId: 2, LocationId: locA1, Quantity: 1}}),
		http.StatusBadRequest, "lot_not_allowed", "a lot on an untracked line")

	// Saved without lots while short: proposed again, still proposed.
	res := save(d, nil, nil)
	mustStatus(t, res, http.StatusOK, "save without lots")
	d = deliveryOf(t, res)
	if got := lotsOf(d.Lines[0]); !slices.Equal(got, []string{fmt.Sprintf("2@%d:2", locA1)}) || d.Lines[0].Lots[0].Chosen {
		t.Errorf("picks %v after a save without lots", d.Lines[0].Lots)
	}

	// No lots at all: the post asks for them.
	res = save(d, &[]gen.OrdersDeliveryLotInput{}, nil)
	mustStatus(t, res, http.StatusOK, "save with no lots")
	d = deliveryOf(t, res)
	expectRefusal(t, postDelivery(c, d), http.StatusBadRequest, "lot_required", "a tracked line with no lot")

	// The expired lot, chosen.
	res = save(d, &[]gen.OrdersDeliveryLotInput{{LotId: 2, LocationId: locA1, Quantity: 2},
		{LotId: 1, LocationId: locA1, Quantity: 2, AllowExpired: ptrBool(true)}}, nil)
	mustStatus(t, res, http.StatusOK, "choose the expired lot")
	d = deliveryOf(t, res)
	if l := d.Lines[0]; len(l.Lots) != 2 || !l.Lots[0].Chosen || !l.Lots[1].AllowExpired || l.Short != 0 {
		t.Fatalf("chosen lots %+v", l.Lots)
	}
	reads := h.stock.lotReadCount()
	p = expectRefusal(t, postDelivery(c, d), http.StatusConflict, contracts.StockLotExpired, "an expired lot without the override")
	if l := (*p.Lines)[0]; len(*p.Lines) != 1 || l.Index != 0 || l.LotIndex == nil || *l.LotIndex != 1 || l.LotId == nil || *l.LotId != 1 ||
		!strings.Contains(l.Detail, "E1") {
		t.Errorf("refusal lines %+v, want line 0's lot row 1 (E1)", *p.Lines)
	}
	if h.stock.lotReadCount() != reads {
		t.Error("a refusal of chosen lots proposed them again")
	}
	with := h.SignIn(t, append(slices.Clone(stockKeys), "inventory:expired-override")...)
	var meta gen.OrdersMeta
	with.Do(http.MethodGet, "/api/v1/orders/meta", nil).JSON(&meta)
	if !meta.CanOverrideExpired {
		t.Error("meta answers canOverrideExpired false for a holder")
	}
	mustStatus(t, postDelivery(with, d), http.StatusOK, "the expired lot with the override")
	issue, _ := h.stock.lastIssue()
	if !issue.ExpiredOverride || len(issue.Lines) != 3 || issue.Lines[1].LotID != 1 || !issue.Lines[1].AllowExpired || issue.Lines[0].AllowExpired {
		t.Errorf("issue %+v", issue)
	}
	if h.stock.lotAt(1, locA1) != "3" || h.stock.lotAt(2, locA1) != "0" {
		t.Errorf("E1 holds %s and F2 %s, want 3 and 0", h.stock.lotAt(1, locA1), h.stock.lotAt(2, locA1))
	}
}

// Proposed picks made stale by another delivery are proposed again once and
// posted, the delivery keeping the new picks; a person's chosen lot on hold
// is answered; a stale proposal the lots no longer cover answers the first
// refusal and keeps the picks (M65a, M78).
func TestDeliveryLots_ReproposedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	seedTracked(h)
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	received := h.Now().AddDate(0, -1, 0)
	h.stock.putLot(varLot, 1, "S1", locA1, "5", day(h, 10), received)
	h.stock.putLot(varLot, 2, "S2", locA1, "10", day(h, 20), received)

	_, d := newDelivery(t, c, productLine(varLot, 5))
	if got := lotsOf(d.Lines[0]); !slices.Equal(got, []string{fmt.Sprintf("1@%d:5", locA1)}) {
		t.Fatalf("picks %v, want S1", got)
	}
	h.stock.putLot(varLot, 1, "S1", locA1, "2", day(h, 10), received) // another delivery took 3
	res := postDelivery(c, d)
	mustStatus(t, res, http.StatusOK, "post, proposed again")
	d = deliveryOf(t, res)
	if got := lotsOf(d.Lines[0]); !slices.Equal(got, []string{fmt.Sprintf("1@%d:2", locA1), fmt.Sprintf("2@%d:3", locA1)}) || d.Lines[0].Lots[1].Chosen {
		t.Errorf("picks %v after the post, want S1 2 and S2 3, proposed", d.Lines[0].Lots)
	}
	if h.stock.lotAt(1, locA1) != "0" || h.stock.lotAt(2, locA1) != "7" {
		t.Errorf("S1 %s S2 %s, want 0 and 7", h.stock.lotAt(1, locA1), h.stock.lotAt(2, locA1))
	}

	// A person's lot, put on hold: answered, never proposed again.
	h.stock.putLot(varLot, 3, "S3", locA2, "5", day(h, 30), received)
	_, d = newDelivery(t, c, productLine(varLot, 3))
	res = c.Do(http.MethodPut, deliveryPath(d.Id), gen.OrdersDeliveryUpdate{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt,
		Lines: []gen.OrdersDeliveryLineInput{{OrderLineId: d.Lines[0].OrderLineId, Quantity: 3,
			Lots: &[]gen.OrdersDeliveryLotInput{{LotId: 3, LocationId: locA2, Quantity: 3}}}}})
	mustStatus(t, res, http.StatusOK, "choose S3")
	d = deliveryOf(t, res)
	h.stock.setLotStatus(3, contracts.LotQuarantine)
	reads := h.stock.lotReadCount()
	p := expectRefusal(t, postDelivery(c, d), http.StatusConflict, contracts.StockLotBlocked, "a chosen lot on hold")
	if l := (*p.Lines)[0]; l.LotId == nil || *l.LotId != 3 || h.stock.lotReadCount() != reads {
		t.Errorf("refusal %+v, lot reads %d → %d", l, reads, h.stock.lotReadCount())
	}

	// Stale beyond what is left: the first refusal, the picks kept.
	_, d = newDelivery(t, c, productLine(varLot, 3))
	if got := lotsOf(d.Lines[0]); !slices.Equal(got, []string{fmt.Sprintf("2@%d:3", locA1)}) {
		t.Fatalf("picks %v, want S2 (S3 is on hold)", got)
	}
	h.stock.putLot(varLot, 2, "S2", locA1, "1", day(h, 20), received)
	reads = h.stock.lotReadCount()
	p = expectRefusal(t, postDelivery(c, d), http.StatusConflict, contracts.StockInsufficientStock, "stale beyond what is left")
	if l := (*p.Lines)[0]; l.LotIndex == nil || *l.LotIndex != 0 || h.stock.lotReadCount() != reads+1 {
		t.Errorf("refusal %+v, lot reads %d → %d; want one proposal", l, reads, h.stock.lotReadCount())
	}
	if got := lotsOf(getDelivery(t, c, d.Id).Lines[0]); !slices.Equal(got, []string{fmt.Sprintf("2@%d:3", locA1)}) {
		t.Errorf("picks %v after the refusal, want them kept", got)
	}
}

// A serial variant is proposed one serial per unit, and its serial numbers
// are listed on the pick list and the packing slip (M65, M79).
func TestDeliveryLots_Serials(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	seedTracked(h)
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	for i, n := range []string{"SN-1", "SN-2", "SN-3"} {
		h.stock.putLot(varSerial, int64(11+i), n, locA1, "1", nil, h.Now().AddDate(0, 0, -10+i))
		h.stock.setLotSerial(int64(11 + i))
	}
	o, d := newDelivery(t, c, productLine(varSerial, 2))
	if got := lotsOf(d.Lines[0]); !slices.Equal(got, []string{fmt.Sprintf("11@%d:1", locA1), fmt.Sprintf("12@%d:1", locA1)}) {
		t.Fatalf("picks %v, want SN-1 and SN-2", got)
	}
	mustStatus(t, postDelivery(c, d), http.StatusOK, "post")
	if h.stock.lotAt(11, locA1) != "0" || h.stock.lotAt(13, locA1) != "1" {
		t.Errorf("serials after the post: %s %s", h.stock.lotAt(11, locA1), h.stock.lotAt(13, locA1))
	}
	mustStatus(t, c.Do(http.MethodGet, deliveryPath(d.Id, "packing-slip"), nil), http.StatusOK, "packing slip")
	subtitle := fmt.Sprintf("Leveranse %d – Ordre %d", d.Number, o.Number)
	slip, ok := pdfModels.find("Pakkseddel", subtitle, func(m orders.PDFModel) bool { return len(m.Rows) == 2 && strings.HasPrefix(m.Rows[0][0], "PUMP-1") })
	if !ok || slip.Rows[1][0] != "Partier: SN-1, SN-2" {
		t.Errorf("packing slip %+v, %v; want the serial numbers listed", slip.Rows, ok)
	}
	mustStatus(t, c.Do(http.MethodGet, deliveryPath(d.Id, "pick-list"), nil), http.StatusOK, "pick list")
	pick, ok := pdfModels.find("Plukkliste", subtitle, func(m orders.PDFModel) bool { return len(m.Rows) == 2 && strings.HasPrefix(m.Rows[0][1], "PUMP-1") })
	if !ok || pick.Rows[0][2] != "SN-1" || pick.Rows[1][2] != "SN-2" {
		t.Errorf("pick list %+v, %v; want a row per serial", pick.Rows, ok)
	}
}
