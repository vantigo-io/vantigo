package manufacturing_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// Quality checks and deviations (manufacturing phase 2 M95–M99): templates
// on a BoM's operations snapshotted at confirm; results recorded at the
// station, a measure judged by the server; the production gated on the
// checks before the output work order and its first-piece checks, a finish
// on the work order's own; a failed check opening a deviation that holds the
// order's lots so far and every lot made while it is open; closing releasing
// only the deviation's own holds; photos and their deletion worker.

// quality is the permission set that records, reports and closes.
var quality = []string{"manufacturing:access", "manufacturing:boms-manage", "manufacturing:orders-manage",
	"manufacturing:shop-floor", "manufacturing:quality-manage"}

// qualityFloor is jam made in two operations: a at MIX with an instruction
// (each work order), b at FILL with a first-piece measure of the fill weight
// (98–102 g, quarantine on fail) and a pass/fail seal check (each work
// order). Berries and sugar are in stock.
func qualityFloor(t *testing.T, h *harness, c *modtest.Client) gen.ManufacturingBom {
	t.Helper()
	mix := createWorkCenter(t, c, workCenterInput("MIX", siteMain))
	fill := createWorkCenter(t, c, workCenterInput("FILL", siteMain))
	in := bomInput(varJam, "1", line(varBerry, "2"), line(varSugar, "1"))
	a, b := operation("a", 10, mix.Id, "1"), operation("b", 20, fill.Id, "1")
	a.Checks = &[]gen.ManufacturingCheckTemplateInput{{Kind: gen.Instruction, Title: "Read the recipe", Frequency: gen.EachWorkOrder}}
	b.Checks = &[]gen.ManufacturingCheckTemplateInput{
		{Kind: gen.Measure, Title: "Fill weight", Frequency: gen.FirstPiece, Nominal: str("100"), Min: str("98"), Max: str("102"),
			Unit: str("g"), QuarantineOnFail: ptrBool(true)},
		{Kind: gen.PassFail, Title: "Seal", Frequency: gen.EachWorkOrder},
	}
	in.Operations = []gen.ManufacturingBomOperationInput{a, b}
	bom := createBom(t, c, in)
	h.stock.receiveLot(varBerry, "B-1", locMainA1, "100", h.Now().Add(-time.Hour), day(2030, 1, 1))
	h.stock.put(varSugar, locMainA1, "100")
	return bom
}

func ptrBool(v bool) *bool { return &v }

// recordCheck posts a check's result and answers the work order view.
func recordCheck(t *testing.T, c *modtest.Client, workOrder, check int64, in gen.ManufacturingCheckResultInput) gen.ManufacturingWorkOrderView {
	t.Helper()
	res := c.Do(http.MethodPost, workOrderPath(workOrder, "checks", fmt.Sprint(check), "result"), in)
	mustStatus(t, res, http.StatusOK, "record a check")
	var v gen.ManufacturingWorkOrderView
	res.JSON(&v)
	return v
}

func checksOf(w gen.ManufacturingWorkOrder) []gen.ManufacturingWorkOrderCheck {
	if w.Checks == nil {
		return nil
	}
	return *w.Checks
}

func result(r gen.ManufacturingCheckResultValue) *gen.ManufacturingCheckResultValue { return &r }

func jamProduction(id, qty string) gen.ManufacturingProductionInput {
	return gen.ManufacturingProductionInput{ClientRequestId: id, Quantity: qty, Lines: []gen.ManufacturingProductionLineInput{}}
}

func deviationOf(t *testing.T, c *modtest.Client, id int64) gen.ManufacturingQualityDeviation {
	t.Helper()
	res := c.Do(http.MethodGet, fmt.Sprintf("%s/deviations/%d", base, id), nil)
	mustStatus(t, res, http.StatusOK, "the deviation")
	var d gen.ManufacturingQualityDeviation
	res.JSON(&d)
	return d
}

// TestQuality_TemplatesAndSnapshot is M95 and M96: the limits judged, the
// checks answered with the BoM, copied onto the work orders at confirm, and
// a later BoM edit reaching no open order.
func TestQuality_TemplatesAndSnapshot(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, quality...)
	center := createWorkCenter(t, c, workCenterInput("POT", siteMain))
	bad := bomInput(varJam, "1", line(varBerry, "2"))
	op := operation("a", 10, center.Id, "1")
	op.Checks = &[]gen.ManufacturingCheckTemplateInput{{Kind: gen.Measure, Title: "Weight", Frequency: gen.EachWorkOrder}}
	bad.Operations = []gen.ManufacturingBomOperationInput{op}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, bad), http.StatusBadRequest, "check_limits_invalid", "a measure without limits")
	(*op.Checks)[0].Min, (*op.Checks)[0].Max, (*op.Checks)[0].Nominal = str("5"), str("10"), str("11")
	bad.Operations = []gen.ManufacturingBomOperationInput{op}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, bad), http.StatusBadRequest, "check_limits_invalid", "a nominal past the max")

	bom := qualityFloor(t, h, c)
	if len(bom.Operations[1].Checks) != 2 || bom.Operations[1].Checks[0].Min == nil || *bom.Operations[1].Checks[0].Min != "98" ||
		bom.Operations[1].Checks[0].Frequency != gen.FirstPiece || !bom.Operations[1].Checks[0].QuarantineOnFail {
		t.Fatalf("BoM checks = %+v", bom.Operations[1].Checks)
	}
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varJam, "2"))).Order
	wos := workOrdersOf(t, c, o.Id)
	if len(checksOf(wos[0])) != 1 || len(checksOf(wos[1])) != 2 || checksOf(wos[1])[0].Title != "Fill weight" || checksOf(wos[1])[0].Result != nil {
		t.Fatalf("work order checks = %+v / %+v", checksOf(wos[0]), checksOf(wos[1]))
	}
	// An edit of the BoM leaves the open order's checks as they were.
	upd := updateOf(getBom(t, c, bom.Id))
	for i := range upd.Operations {
		upd.Operations[i].Checks = nil
	}
	mustStatus(t, c.Do(http.MethodPut, bomPath(bom.Id), upd), http.StatusOK, "remove the checks")
	if b := getBom(t, c, bom.Id); len(b.Operations[1].Checks) != 0 {
		t.Errorf("checks after the edit = %+v", b.Operations[1].Checks)
	}
	if again := workOrdersOf(t, c, o.Id); len(checksOf(again[1])) != 2 {
		t.Errorf("the snapshot moved: %+v", checksOf(again[1]))
	}
}

// TestQuality_Gates is M98 with first_piece's meaning: a production is
// refused while a check of the work order before the output work order has
// no result, then while the output work order's first-piece check has none;
// the output work order's each-work-order check does not stop it; a finish
// is refused while one of the work order's own checks has none.
func TestQuality_Gates(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, quality...)
	qualityFloor(t, h, c)
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varJam, "4"))).Order
	wos := workOrdersOf(t, c, o.Id)
	mix, fill := wos[0], wos[1]

	p := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), jamProduction("q1", "1")), http.StatusConflict,
		"checks_incomplete", "a production before any check")
	if p.Checks == nil || len(*p.Checks) != 2 || (*p.Checks)[0].WorkOrderId != mix.Id || (*p.Checks)[1].Title != "Fill weight" {
		t.Fatalf("missing checks = %+v", p.Checks)
	}
	// Starting is never gated; finishing the mix is, on its own check.
	act(t, c, mix.Id, "start", nil)
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(mix.Id, "finish"), nil), http.StatusConflict, "checks_incomplete", "finish before its check")
	recordCheck(t, c, mix.Id, checksOf(mix)[0].Id, gen.ManufacturingCheckResultInput{})
	act(t, c, mix.Id, "finish", nil)

	p = expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), jamProduction("q2", "1")), http.StatusConflict,
		"checks_incomplete", "a production before the first piece")
	if p.Checks == nil || len(*p.Checks) != 1 || (*p.Checks)[0].CheckId != checksOf(fill)[0].Id {
		t.Fatalf("missing checks = %+v", p.Checks)
	}
	// A scrap report with no good output is not output passed on.
	act(t, c, fill.Id, "start", nil)
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(fill.Id, "checks", fmt.Sprint(checksOf(fill)[0].Id), "result"),
		gen.ManufacturingCheckResultInput{}), http.StatusBadRequest, "", "a measure without its value")
	v := recordCheck(t, c, fill.Id, checksOf(fill)[0].Id, gen.ManufacturingCheckResultInput{Value: str("100.25")})
	if r := checksOf(v.WorkOrder)[0].Result; r == nil || r.Result != gen.ManufacturingCheckResultValuePass || r.Value == nil || *r.Value != "100.25" {
		t.Fatalf("the measure's result = %+v", r)
	}
	res := c.Do(http.MethodPost, orderPath(o.Id, "productions"), jamProduction("q3", "1"))
	mustStatus(t, res, http.StatusCreated, "a production after the first piece")
	// The seal (each work order) stops only the fill's finish.
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(fill.Id, "finish"), nil), http.StatusConflict, "checks_incomplete", "finish before the seal")
	recordCheck(t, c, fill.Id, checksOf(fill)[1].Id, gen.ManufacturingCheckResultInput{Result: result(gen.ManufacturingCheckResultValuePass)})
	act(t, c, fill.Id, "finish", nil)
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(fill.Id, "checks", fmt.Sprint(checksOf(fill)[1].Id), "result"),
		gen.ManufacturingCheckResultInput{Result: result(gen.ManufacturingCheckResultValuePass)}), http.StatusConflict, "check_not_open", "a result on a done work order")
}

// TestQuality_FirstPieceFailureHoldsLaterLots is M99 and §19: a first-piece
// failure before any output opens a quarantining deviation that holds
// nothing yet; the order's first production then makes its lot under the
// deviation's hold, in its own transaction.
func TestQuality_FirstPieceFailureHoldsLaterLots(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, quality...)
	qualityFloor(t, h, c)
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varJam, "4"))).Order
	wos := workOrdersOf(t, c, o.Id)
	mix, fill := wos[0], wos[1]
	recordCheck(t, c, mix.Id, checksOf(mix)[0].Id, gen.ManufacturingCheckResultInput{Result: result(gen.ManufacturingCheckResultValueDone)})
	v := recordCheck(t, c, fill.Id, checksOf(fill)[0].Id, gen.ManufacturingCheckResultInput{Value: str("97.5"), Note: str("Too light")})
	r := checksOf(v.WorkOrder)[0].Result
	if r == nil || r.Result != gen.ManufacturingCheckResultValueFail || r.DeviationId == nil || r.DeviationNumber == nil {
		t.Fatalf("the failed result = %+v", r)
	}
	d := deviationOf(t, c, *r.DeviationId)
	if !d.Quarantine || d.Status != gen.ManufacturingQualityDeviationStatusOpen || len(d.Lots) != 0 || d.Order == nil || d.Order.Id != o.Id ||
		d.WorkOrderId == nil || *d.WorkOrderId != fill.Id || !strings.Contains(d.Description, "97.5 g") {
		t.Fatalf("deviation = %+v", d)
	}
	// A fail does not block: the first piece has a result.
	res := c.Do(http.MethodPost, orderPath(o.Id, "productions"), jamProduction("held", "2"))
	mustStatus(t, res, http.StatusCreated, "a production under an open deviation")
	made := productionOf(t, res).OutputLots
	if len(made) != 1 {
		t.Fatalf("output lots = %+v", made)
	}
	if st := h.stock.lotStatus(made[0].LotId); st != contracts.LotQuarantine {
		t.Errorf("the new lot is %s", st)
	}
	if by := h.stock.heldBy(made[0].LotId); len(by) != 1 || by[contracts.StockSource{Module: "manufacturing", Kind: "deviation", ID: d.Id}] != "quarantine" {
		t.Errorf("held by = %+v", by)
	}
	if d = deviationOf(t, c, d.Id); len(d.Lots) != 1 || d.Lots[0].LotId != made[0].LotId {
		t.Errorf("the deviation's lots = %+v", d.Lots)
	}
}

// TestQuality_CloseReleasesOnlyItsOwn is M99 and M64: a hand-reported
// quarantining deviation holds the lots made so far; a lot it holds that a
// recall blocks too stays blocked when it closes as use_as_is; one closed as
// scrap keeps its holds until release; the list filters and its CSV.
func TestQuality_CloseReleasesOnlyItsOwn(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, quality...)
	floorOnly := h.SignIn(t, "manufacturing:access", "manufacturing:shop-floor")
	reader := h.SignIn(t, "manufacturing:access")
	qualityFloor(t, h, c)
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varJam, "4"))).Order
	wos := workOrdersOf(t, c, o.Id)
	recordCheck(t, c, wos[0].Id, checksOf(wos[0])[0].Id, gen.ManufacturingCheckResultInput{})
	recordCheck(t, c, wos[1].Id, checksOf(wos[1])[0].Id, gen.ManufacturingCheckResultInput{Value: str("100")})
	res := c.Do(http.MethodPost, orderPath(o.Id, "productions"), jamProduction("p1", "2"))
	mustStatus(t, res, http.StatusCreated, "production")
	lot := productionOf(t, res).OutputLots[0].LotId

	expectRefusal(t, reader.Do(http.MethodPost, base+"/deviations", gen.ManufacturingQualityDeviationInput{Title: "x", OrderId: &o.Id}),
		http.StatusForbidden, "shop_floor_required", "a reader reports")
	res = floorOnly.Do(http.MethodPost, base+"/deviations", gen.ManufacturingQualityDeviationInput{Title: "Glass shard", OrderId: &o.Id,
		WorkOrderId: &wos[1].Id, Quarantine: ptrBool(true)})
	mustStatus(t, res, http.StatusCreated, "report a deviation")
	var d gen.ManufacturingQualityDeviation
	res.JSON(&d)
	if len(d.Lots) != 1 || d.Lots[0].LotId != lot || d.Number == "" {
		t.Fatalf("deviation = %+v", d)
	}
	// A recall blocks the same lot.
	recall := contracts.StockSource{Module: "inventory", Kind: "lot", ID: lot}
	h.stock.hold(lot, recall, "block")
	if st := h.stock.lotStatus(lot); st != contracts.LotBlocked {
		t.Fatalf("status = %s", st)
	}
	closeIn := gen.ManufacturingDeviationCloseInput{Disposition: gen.UseAsIs, ClosingNote: "Sieved, fine.", Revision: d.Revision}
	mustStatus(t, floorOnly.Do(http.MethodPost, fmt.Sprintf("%s/deviations/%d/close", base, d.Id), closeIn), http.StatusForbidden,
		"close without quality-manage")
	res = c.Do(http.MethodPost, fmt.Sprintf("%s/deviations/%d/close", base, d.Id), closeIn)
	mustStatus(t, res, http.StatusOK, "close as use_as_is")
	res.JSON(&d)
	if d.Status != gen.ManufacturingQualityDeviationStatusClosed || !d.HoldsReleased || d.ClosedBy == nil {
		t.Fatalf("closed = %+v", d)
	}
	if by := h.stock.heldBy(lot); len(by) != 1 || by[recall] != "block" || h.stock.lotStatus(lot) != contracts.LotBlocked {
		t.Errorf("after close: held by %+v, %s", by, h.stock.lotStatus(lot))
	}
	expectRefusal(t, c.Do(http.MethodPost, fmt.Sprintf("%s/deviations/%d/close", base, d.Id), closeIn), http.StatusConflict,
		"deviation_closed", "close twice")

	// A second deviation closed as scrap keeps its hold until release.
	res = c.Do(http.MethodPost, base+"/deviations", gen.ManufacturingQualityDeviationInput{Title: "Mould", OrderId: &o.Id, Quarantine: ptrBool(true)})
	mustStatus(t, res, http.StatusCreated, "another deviation")
	var scrap gen.ManufacturingQualityDeviation
	res.JSON(&scrap)
	expectRefusal(t, c.Do(http.MethodPost, fmt.Sprintf("%s/deviations/%d/release", base, scrap.Id), revisionInput(scrap.Revision)),
		http.StatusConflict, "deviation_closed", "release an open deviation")
	res = c.Do(http.MethodPost, fmt.Sprintf("%s/deviations/%d/close", base, scrap.Id),
		gen.ManufacturingDeviationCloseInput{Disposition: gen.Scrap, ClosingNote: "Written off.", Revision: scrap.Revision})
	mustStatus(t, res, http.StatusOK, "close as scrap")
	res.JSON(&scrap)
	scrapSrc := contracts.StockSource{Module: "manufacturing", Kind: "deviation", ID: scrap.Id}
	if scrap.HoldsReleased || h.stock.heldBy(lot)[scrapSrc] != "quarantine" {
		t.Fatalf("scrap closed = %+v, held by %+v", scrap, h.stock.heldBy(lot))
	}
	res = c.Do(http.MethodPost, fmt.Sprintf("%s/deviations/%d/release", base, scrap.Id), revisionInput(scrap.Revision))
	mustStatus(t, res, http.StatusOK, "release")
	if _, ok := h.stock.heldBy(lot)[scrapSrc]; ok {
		t.Errorf("the scrap deviation still holds the lot")
	}

	var list gen.ManufacturingQualityDeviationList
	reader.Do(http.MethodGet, fmt.Sprintf("%s/deviations?status=closed&orderId=%d&lotId=%d", base, o.Id, lot), nil).JSON(&list)
	if list.Total != 2 || len(list.Items) != 2 {
		t.Errorf("list = %+v", list)
	}
	reader.Do(http.MethodGet, base+"/deviations?status=open", nil).JSON(&list)
	if list.Total != 0 {
		t.Errorf("open = %+v", list)
	}
	csv := reader.Do(http.MethodGet, base+"/deviations?format=csv", nil)
	mustStatus(t, csv, http.StatusOK, "csv")
	if body := string(csv.Body); !strings.Contains(body, "Glass shard") || !strings.Contains(body, "use_as_is") {
		t.Errorf("csv = %s", body)
	}
}

// TestQuality_DeviationFromLot is a deviation opened from a lot page (M99):
// it holds that lot, under LockVariants on the lot's variant.
func TestQuality_DeviationFromLot(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, quality...)
	lot := h.stock.receiveLot(varBerry, "B-9", locMainA1, "5", h.Now(), nil)
	expectRefusal(t, c.Do(http.MethodPost, base+"/deviations", gen.ManufacturingQualityDeviationInput{Title: "Bad", LotId: ptrInt64(999999)}),
		http.StatusBadRequest, "", "an unknown lot")
	res := c.Do(http.MethodPost, base+"/deviations", gen.ManufacturingQualityDeviationInput{Title: "Mouldy berries", LotId: &lot, Quarantine: ptrBool(true)})
	mustStatus(t, res, http.StatusCreated, "a deviation from a lot")
	var d gen.ManufacturingQualityDeviation
	res.JSON(&d)
	if d.LotNumber == nil || *d.LotNumber != "B-9" || d.Variant == nil || d.Variant.Id != varBerry || len(d.Lots) != 1 {
		t.Fatalf("deviation = %+v", d)
	}
	if st := h.stock.lotStatus(lot); st != contracts.LotQuarantine {
		t.Errorf("status = %s", st)
	}
	locked := false
	for _, ids := range h.catalog.lockCalls() {
		if len(ids) == 1 && ids[0] == varBerry {
			locked = true
		}
	}
	if !locked {
		t.Errorf("the lot's variant was not locked: %+v", h.catalog.lockCalls())
	}
}

func ptrInt64(v int64) *int64 { return &v }

// TestQuality_ConcurrentResults is M97's check row lock: two results
// recorded at once for one check serialise, the second superseding the
// first, and one result is in force.
func TestQuality_ConcurrentResults(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, quality...)
	qualityFloor(t, h, c)
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varJam, "4"))).Order
	fill := workOrdersOf(t, c, o.Id)[1]
	check := checksOf(fill)[1].Id
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, r := range []gen.ManufacturingCheckResultValue{gen.ManufacturingCheckResultValuePass, gen.ManufacturingCheckResultValueFail} {
		wg.Go(func() {
			statuses[i] = c.Do(http.MethodPost, workOrderPath(fill.Id, "checks", fmt.Sprint(check), "result"),
				gen.ManufacturingCheckResultInput{Result: &r}).Status
		})
	}
	wg.Wait()
	if statuses[0] != http.StatusOK || statuses[1] != http.StatusOK {
		t.Fatalf("statuses = %v", statuses)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.check_results WHERE work_order_check_id = $1`, check); n != 2 {
		t.Errorf("results = %d", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.check_results WHERE work_order_check_id = $1 AND superseded_by IS NULL`, check); n != 1 {
		t.Errorf("results in force = %d", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.check_results r JOIN manufacturing.check_results s ON s.id = r.superseded_by
		WHERE r.work_order_check_id = $1 AND s.superseded_by IS NULL AND r.id <> s.id`, check); n != 1 {
		t.Errorf("superseded links = %d", n)
	}
}

// memoryStore is storage.ObjectStore in memory, its deletes able to fail.
type memoryStore struct {
	mu        sync.Mutex
	objects   map[string][]byte
	deleteErr error
}

var _ storage.ObjectStore = (*memoryStore)(nil)

func newMemoryStore() *memoryStore { return &memoryStore{objects: map[string][]byte{}} }

func (s *memoryStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	return nil
}

func (s *memoryStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *memoryStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}

func (s *memoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.objects, key)
	return nil
}

func (s *memoryStore) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.objects) }

// photoForm is a picture as multipart/form-data.
func photoForm(t *testing.T, contentType string, data []byte) modtest.RequestOption {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="photo.png"`)
	header.Set("Content-Type", contentType)
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatalf("create the part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write the part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the form: %v", err)
	}
	return modtest.RawBody(w.FormDataContentType(), buf.Bytes())
}

// testPNG is a 2×2 PNG.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// TestQuality_Photos is M97's photos: a photo check refused without one
// (photo_required) and recorded with one; a text file refused; the photo
// served; an upload no result names removed after a day by the
// manufacturing.object-deletions worker built through module.Workers, on a
// pool of one, while the one a result names stays.
func TestQuality_Photos(t *testing.T) {
	t.Parallel()
	objects := newMemoryStore()
	h := newHarness(t, modtest.WithObjectStore(objects))
	c := h.SignIn(t, quality...)
	center := createWorkCenter(t, c, workCenterInput("CAM", siteMain))
	in := bomInput(varBike, "1", line(varWheel, "2"))
	op := operation("a", 10, center.Id, "1")
	op.Checks = &[]gen.ManufacturingCheckTemplateInput{{Kind: gen.Photo, Title: "Photo of the weld", Frequency: gen.EachWorkOrder}}
	in.Operations = []gen.ManufacturingBomOperationInput{op}
	createBom(t, c, in)
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "1"))).Order
	w := workOrdersOf(t, c, o.Id)[0]
	check := checksOf(w)[0].Id
	photos := workOrderPath(w.Id, "photos")

	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(w.Id, "checks", fmt.Sprint(check), "result"), gen.ManufacturingCheckResultInput{}),
		http.StatusBadRequest, "photo_required", "a photo check without a photo")
	expectRefusal(t, c.Do(http.MethodPost, photos, nil, photoForm(t, "text/plain", []byte("not a picture"))),
		http.StatusUnsupportedMediaType, "attachment_type", "a text file")
	picture := testPNG(t)
	upload := func() gen.ManufacturingCheckPhoto {
		res := c.Do(http.MethodPost, photos, nil, photoForm(t, "image/png", picture))
		mustStatus(t, res, http.StatusCreated, "upload")
		var p gen.ManufacturingCheckPhoto
		res.JSON(&p)
		return p
	}
	kept, stray := upload(), upload()
	v := recordCheck(t, c, w.Id, check, gen.ManufacturingCheckResultInput{PhotoId: &kept.Id})
	if r := checksOf(v.WorkOrder)[0].Result; r == nil || r.PhotoId == nil || *r.PhotoId != kept.Id || r.Result != gen.ManufacturingCheckResultValueDone {
		t.Fatalf("result = %+v", r)
	}
	res := c.Do(http.MethodGet, fmt.Sprintf("%s/%d", photos, kept.Id), nil)
	mustStatus(t, res, http.StatusOK, "the photo")
	if !bytes.Equal(res.Body, picture) || res.Header("X-Content-Type-Options") != "nosniff" {
		t.Errorf("served %d bytes", len(res.Body))
	}
	mustStatus(t, c.Do(http.MethodGet, fmt.Sprintf("%s/%d", workOrderPath(w.Id+1000, "photos"), kept.Id), nil), http.StatusNotFound, "another work order's")

	h.Advance(25 * time.Hour)
	cfg := h.Pool().Config().Copy()
	cfg.MaxConns, cfg.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("pool of one: %v", err)
	}
	t.Cleanup(pool.Close)
	var cycle interface {
		RunCycle(context.Context) (int, error)
	}
	for _, wk := range h.Workers(h.DepsOn(pool)) {
		if wk.Name() == "manufacturing.object-deletions" {
			cycle, _ = wk.(interface {
				RunCycle(context.Context) (int, error)
			})
		}
	}
	if cycle == nil {
		t.Fatal("module.Workers built no manufacturing.object-deletions worker")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if n, err := cycle.RunCycle(ctx); err != nil || n != 1 {
		t.Fatalf("RunCycle = %d, %v; want 1 removed", n, err)
	}
	if objects.count() != 1 || h.Count(t, `SELECT count(*) FROM manufacturing.check_photos WHERE id = $1`, stray.Id) != 0 ||
		h.Count(t, `SELECT count(*) FROM manufacturing.object_deletions`) != 0 {
		t.Errorf("after the worker: %d objects", objects.count())
	}
	c = h.SignIn(t, quality...)
	mustStatus(t, c.Do(http.MethodGet, fmt.Sprintf("%s/%d", photos, kept.Id), nil), http.StatusOK, "the kept photo")
}
