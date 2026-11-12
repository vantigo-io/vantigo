package inventory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// The lot screens (manufacturing phase 2 L1b, M54, M60–M66): the lots list
// and a lot's page, its edit and its holds, its files and their deletion
// worker, stock per lot, the lookup and the live topics. Tracked variants are
// made through the fake catalog: Products' guard keeps them out until M.

const lotsPath = "/api/v1/inventory/lots"

func lotPath(id int64) string { return lotsPath + "/" + strconv.FormatInt(id, 10) }

// lotManager reads stock and manages lots.
var lotManager = []string{"inventory:locations-view", "inventory:stock-view", "inventory:receipts-manage", "inventory:lots-manage"}

// getLot reads a lot's page.
func getLot(t *testing.T, c *modtest.Client, id int64) gen.InventoryLot {
	t.Helper()
	res := c.Do(http.MethodGet, lotPath(id), nil)
	mustStatus(t, res, http.StatusOK, "GET /lots/{id}")
	var l gen.InventoryLot
	res.JSON(&l)
	return l
}

// listLots reads the lots list with query.
func listLots(t *testing.T, c *modtest.Client, query string) gen.InventoryLotPage {
	t.Helper()
	res := c.Do(http.MethodGet, lotsPath+query, nil)
	mustStatus(t, res, http.StatusOK, "GET /lots"+query)
	var p gen.InventoryLotPage
	res.JSON(&p)
	return p
}

// numbersOf is a page's lot numbers, in order.
func numbersOf(p gen.InventoryLotPage) []string {
	out := make([]string, 0, len(p.Lots))
	for _, l := range p.Lots {
		out = append(out, l.Number)
	}
	return out
}

// TestLotScreens_ListAndPage is the lots list's filters and cursor (M60) and
// a lot's page (M61): its header, where it lies, its movements and the lot
// on the variant's stock read with held, available and the FEFO rows (M63).
func TestLotScreens_ListAndPage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, lotManager...)
	admin, _ := signIn(t, h, everything...)
	s := seedLots(t, h, admin)
	e1, e2, e3 := receiveLots(t, h, c, s)
	receive(t, c, s.site.Id, s.shelf2.Id, lotLine(varLot, 2, gen.InventoryNewLot{Number: ptr("E1")}))
	h.Exec(t, `UPDATE inventory.lots SET supplier_lot = 'HEAT-77' WHERE id = $1`, e3)
	// An empty lot: listed only without withStock.
	receive(t, c, s.site.Id, s.shelf1.Id, lotLine(varLot, 1, gen.InventoryNewLot{Number: ptr("Z9")}))
	z9 := lotID(t, h, varLot, "Z9")
	if res := adjust(admin, s.site.Id, gen.InventoryAdjustmentLine{VariantId: varLot, LocationId: s.shelf1.Id, Mode: gen.Change, Quantity: qty(-1), LotId: &z9}); res.Status != http.StatusCreated {
		t.Fatalf("adjust Z9 out = %d %s", res.Status, res.Body)
	}

	if got := numbersOf(listLots(t, c, "")); !slices.Equal(got, []string{"E1", "E2", "E3"}) {
		t.Errorf("lots = %v, want E1 E2 E3 (with stock, by number)", got)
	}
	if got := numbersOf(listLots(t, c, "?withStock=false")); !slices.Equal(got, []string{"E1", "E2", "E3", "Z9"}) {
		t.Errorf("lots without withStock = %v", got)
	}
	if got := numbersOf(listLots(t, c, "?q=heat")); !slices.Equal(got, []string{"E3"}) {
		t.Errorf("q=heat = %v, want the supplier lot's E3", got)
	}
	if got := numbersOf(listLots(t, c, "?q=milk-1&variantId="+strconv.Itoa(int(varLot)))); len(got) != 3 {
		t.Errorf("q by SKU = %v", got)
	}
	if got := numbersOf(listLots(t, c, "?expiresBefore=2026-11-01")); !slices.Equal(got, []string{"E1"}) {
		t.Errorf("expiresBefore = %v, want E1", got)
	}
	if got := numbersOf(listLots(t, c, fmt.Sprintf("?siteId=%d", s.elsewhre.Id))); len(got) != 0 {
		t.Errorf("another site's lots = %v, want none", got)
	}
	// The cursor, one a page.
	first := listLots(t, c, "?limit=1")
	if first.NextCursor == nil || numbersOf(first)[0] != "E1" {
		t.Fatalf("first page = %+v", first)
	}
	second := listLots(t, c, "?limit=1&cursor="+*first.NextCursor)
	if got := numbersOf(second); !slices.Equal(got, []string{"E2"}) {
		t.Errorf("second page = %v", got)
	}
	mustStatus(t, c.Do(http.MethodGet, lotsPath+"?cursor=nonsense", nil), http.StatusBadRequest, "a bad cursor")

	// The page.
	l := getLot(t, c, e1)
	if l.Number != "E1" || l.OnHand != 7 || len(l.Locations) != 2 || len(l.Movements) != 2 || l.Status != gen.InventoryLotStatusAvailable ||
		l.Expired || l.Variant.Tracking == nil || *l.Variant.Tracking != gen.InventoryVariantRefTrackingLot || l.Origin == nil || l.Origin.Kind != "movement" {
		t.Fatalf("lot E1 = %+v", l)
	}
	if l.Locations[0].Location.Id != s.shelf1.Id || l.Locations[0].OnHand != 5 || l.Movements[0].To == nil || l.Movements[0].To.Id != s.shelf2.Id {
		t.Errorf("E1 lies %+v, moved %+v", l.Locations, l.Movements)
	}
	if res := c.Do(http.MethodGet, lotPath(999999), nil); res.Status != http.StatusNotFound {
		t.Errorf("an unknown lot = %d", res.Status)
	}

	// Expired and held stock leave available (M63).
	h.Exec(t, `UPDATE inventory.lots SET expiry = '2026-09-01' WHERE id = $1`, e1)
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return ledgerOf(h).HoldLots(inCallerTx(t.Context()), tx, contracts.StockSource{Module: "manufacturing", Kind: "deviation", ID: 4},
			[]contracts.LotHold{{LotID: e2, Kind: "quarantine", Cause: "deviation"}})
	}); err != nil {
		t.Fatalf("HoldLots: %v", err)
	}
	res := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/inventory/variants/%d/stock", varLot), nil)
	mustStatus(t, res, http.StatusOK, "GET variant stock")
	var vs gen.InventoryVariantStock
	res.JSON(&vs)
	if len(vs.Sites) != 1 || vs.Sites[0].OnHand != 17 || vs.Sites[0].Held != 12 || vs.Sites[0].Available != 5 || vs.TotalAvailable != 5 {
		t.Fatalf("site stock = %+v, want 17 on hand, 12 held (E1 expired, E2 quarantined), 5 available", vs.Sites)
	}
	var shelf1 *gen.InventoryBalanceRow
	for i := range vs.Sites[0].Rows {
		if vs.Sites[0].Rows[i].Location.Id == s.shelf1.Id {
			shelf1 = &vs.Sites[0].Rows[i]
		}
	}
	if shelf1 == nil || shelf1.Lots == nil || len(*shelf1.Lots) != 3 {
		t.Fatalf("shelf 1's row = %+v", shelf1)
	}
	lots := *shelf1.Lots
	if lots[0].Number != "E1" || !lots[0].Expired || lots[1].Number != "E2" || lots[1].Status != gen.InventoryLotStatusQuarantine || lots[2].Number != "E3" {
		t.Errorf("shelf 1's lots = %+v, want E1 (expired), E2 (quarantine), E3 (no expiry last)", lots)
	}
	if got := numbersOf(listLots(t, c, "?status=quarantine")); !slices.Equal(got, []string{"E2"}) {
		t.Errorf("status=quarantine = %v", got)
	}
	if got := numbersOf(listLots(t, c, "?holdCause=deviation")); !slices.Equal(got, []string{"E2"}) {
		t.Errorf("holdCause=deviation = %v", got)
	}
	if got := listLots(t, c, "?q=E1").Lots; len(got) != 1 || !got[0].Expired {
		t.Errorf("E1 in the list = %+v, want expired", got)
	}
	// A stock reader without lots-manage reads lots too.
	viewer, _ := signIn(t, h, stockViewer...)
	getLot(t, viewer, e3)
}

// TestLotScreens_EditAndHolds is a lot's edit under If-Match and the lot
// page's own holds (M61, M64): put, derived, released with a note; another
// module's hold refused hold_not_inventorys; lots-manage required.
func TestLotScreens_EditAndHolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, lotManager...)
	viewer, _ := signIn(t, h, stockViewer...)
	admin, _ := signIn(t, h, everything...)
	s := seedLots(t, h, admin)
	e1, e2, _ := receiveLots(t, h, c, s)

	l := getLot(t, c, e1)
	edit := gen.InventoryLotUpdate{SupplierLot: ptr("S-1"), Expiry: date(2027, 2, 1), Note: ptr("Certificate on file")}
	if res := viewer.Do(http.MethodPut, lotPath(e1), edit, ifMatch(l.Revision)); res.Status != http.StatusForbidden {
		t.Errorf("an edit without lots-manage = %d", res.Status)
	}
	res := c.Do(http.MethodPut, lotPath(e1), edit, ifMatch(l.Revision))
	mustStatus(t, res, http.StatusOK, "PUT /lots/{id}")
	var edited gen.InventoryLot
	res.JSON(&edited)
	if strOf(edited.SupplierLot) != "S-1" || edited.Expiry == nil || edited.Expiry.Format(time.DateOnly) != "2027-02-01" || edited.BestBefore != nil ||
		edited.Revision != l.Revision+1 || edited.Number != "E1" {
		t.Errorf("edited = %+v, want the fields replaced whole (best before cleared)", edited)
	}
	expectRefusal(t, c.Do(http.MethodPut, lotPath(e1), edit, ifMatch(l.Revision)), http.StatusConflict, "revision_conflict", "a stale revision")
	mustStatus(t, c.Do(http.MethodPut, lotPath(e1), map[string]any{"number": "X"}, ifMatch(edited.Revision)), http.StatusBadRequest, "a number")
	if n := h.Count(t, `SELECT count(*) FROM inventory.events WHERE entity_kind = 'lot' AND entity_id = $1 AND kind = 'lot_updated'`, e1); n != 1 {
		t.Errorf("lot_updated events = %d", n)
	}

	// The page's holds.
	hold := func(kind gen.InventoryLotHoldInputKind) gen.InventoryLot {
		t.Helper()
		res := c.Do(http.MethodPost, lotPath(e1)+"/holds", gen.InventoryLotHoldInput{Kind: kind, Cause: gen.InventoryLotHoldInputCauseComplaint, Note: ptr("Customer complaint")})
		mustStatus(t, res, http.StatusOK, "POST /lots/{id}/holds")
		var out gen.InventoryLot
		res.JSON(&out)
		return out
	}
	if res := viewer.Do(http.MethodPost, lotPath(e1)+"/holds", gen.InventoryLotHoldInput{Kind: "quarantine", Cause: "other"}); res.Status != http.StatusForbidden {
		t.Errorf("a hold without lots-manage = %d", res.Status)
	}
	if got := hold(gen.InventoryLotHoldInputKindQuarantine); got.Status != gen.InventoryLotStatusQuarantine || len(got.Holds) != 1 || !got.Holds[0].Inventorys ||
		got.Holds[0].Source.Kind != "lot" || got.Holds[0].Source.Id != strconv.FormatInt(e1, 10) {
		t.Fatalf("after a quarantine = %+v", got)
	}
	blocked := hold(gen.InventoryLotHoldInputKindBlock)
	if blocked.Status != gen.InventoryLotStatusBlocked || len(blocked.Holds) != 2 {
		t.Fatalf("after a block = %+v", blocked)
	}
	mustStatus(t, c.Do(http.MethodPost, lotPath(e1)+"/holds", gen.InventoryLotHoldInput{Kind: "freeze", Cause: "other"}), http.StatusBadRequest, "an unknown kind")
	var block gen.InventoryLotHold
	for _, x := range blocked.Holds {
		if x.Kind == gen.InventoryLotHoldKindBlock {
			block = x
		}
	}
	res = c.Do(http.MethodPost, fmt.Sprintf("%s/holds/%d/release", lotPath(e1), block.Id), gen.InventoryLotHoldRelease{Note: ptr("Cleared")})
	mustStatus(t, res, http.StatusOK, "release the block")
	var released gen.InventoryLot
	res.JSON(&released)
	if released.Status != gen.InventoryLotStatusQuarantine || released.Holds[1].ReleasedAt == nil || strOf(released.Holds[1].ReleaseNote) != "Cleared" ||
		released.Holds[1].ReleasedBy == nil {
		t.Errorf("after the release = %+v, want quarantine with the block released last", released)
	}
	// Again: answered as it is.
	mustStatus(t, c.Do(http.MethodPost, fmt.Sprintf("%s/holds/%d/release", lotPath(e1), block.Id), gen.InventoryLotHoldRelease{}), http.StatusOK, "release again")
	// Another lot's hold is no hold of this one.
	if res := c.Do(http.MethodPost, fmt.Sprintf("%s/holds/%d/release", lotPath(e2), block.Id), gen.InventoryLotHoldRelease{}); res.Status != http.StatusNotFound {
		t.Errorf("another lot's hold = %d", res.Status)
	}

	// A deviation's hold is released on the deviation.
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return ledgerOf(h).HoldLots(inCallerTx(t.Context()), tx, contracts.StockSource{Module: "manufacturing", Kind: "deviation", ID: 9},
			[]contracts.LotHold{{LotID: e2, Kind: "quarantine", Cause: "deviation"}})
	}); err != nil {
		t.Fatalf("HoldLots: %v", err)
	}
	theirs := getLot(t, c, e2).Holds
	if len(theirs) != 1 || theirs[0].Inventorys {
		t.Fatalf("e2's holds = %+v", theirs)
	}
	expectRefusal(t, c.Do(http.MethodPost, fmt.Sprintf("%s/holds/%d/release", lotPath(e2), theirs[0].Id), gen.InventoryLotHoldRelease{}),
		http.StatusConflict, "hold_not_inventorys", "another module's hold")
	if got := getLot(t, c, e2); got.Status != gen.InventoryLotStatusQuarantine {
		t.Errorf("e2 = %s, want still quarantined", got.Status)
	}
}

// TestLotScreens_MetaAndLookup is meta's canManageLots and the lookup's lot
// matches (M66): a LOT token, a number ignoring case, every variant's.
func TestLotScreens_MetaAndLookup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, lotManager...)
	viewer, _ := signIn(t, h, stockViewer...)
	admin, _ := signIn(t, h, everything...)
	s := seedLots(t, h, admin)
	e1, _, _ := receiveLots(t, h, c, s)

	var meta gen.InventoryMeta
	c.Do(http.MethodGet, metaPath, nil).JSON(&meta)
	if !meta.CanManageLots {
		t.Errorf("a lot manager's meta = %+v", meta)
	}
	viewer.Do(http.MethodGet, metaPath, nil).JSON(&meta)
	if meta.CanManageLots {
		t.Errorf("a viewer's meta says canManageLots")
	}

	lookup := func(q string) []gen.InventoryLookupMatch {
		t.Helper()
		res := viewer.Do(http.MethodGet, lookupPath+"?q="+q, nil)
		mustStatus(t, res, http.StatusOK, "lookup "+q)
		var out gen.InventoryLookupResult
		res.JSON(&out)
		return out.Matches
	}
	m := lookup("LOT" + strconv.FormatInt(e1, 10))
	if len(m) != 1 || m[0].Kind != gen.InventoryLookupMatchKindLot || m[0].Lot == nil || m[0].Lot.Id != e1 || m[0].Variant == nil || m[0].Variant.Id != varLot {
		t.Errorf("the LOT token = %+v", m)
	}
	if m := lookup("e1"); len(m) != 1 || m[0].Lot == nil || m[0].Lot.Number != "E1" {
		t.Errorf("the number = %+v", m)
	}
	// A serial whose number is also an SKU shows both.
	receive(t, c, s.site.Id, s.shelf1.Id, gen.InventoryReceiptLine{VariantId: varSerial, Quantity: qty(1), Lot: &gen.InventoryNewLot{Number: ptr("BOLT-M8")}})
	kinds := []string{}
	for _, x := range lookup("BOLT-M8") {
		kinds = append(kinds, string(x.Kind))
	}
	if !slices.Equal(kinds, []string{"lot", "variant"}) {
		t.Errorf("a serial that is an SKU = %v, want lot then variant", kinds)
	}
	// Locations alone see no lot.
	locs, _ := signIn(t, h, locationsViewer...)
	res := locs.Do(http.MethodGet, lookupPath+"?q=E1", nil)
	var out gen.InventoryLookupResult
	res.JSON(&out)
	if len(out.Matches) != 0 {
		t.Errorf("a locations viewer's lookup = %+v", out.Matches)
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

func (s *memoryStore) failDelete(err error) { s.mu.Lock(); defer s.mu.Unlock(); s.deleteErr = err }

func (s *memoryStore) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.objects) }

// fileForm is one file as multipart/form-data.
func fileForm(t *testing.T, name, contentType string, data []byte) modtest.RequestOption {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
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

// strOf is *p, "" for nil.
func strOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// testCertificate is the smallest thing http.DetectContentType calls a PDF.
func testCertificate() []byte { return []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n") }

// TestLotScreens_Attachments is a lot's files (M54): uploaded, listed on the
// page, downloaded with their headers; a wrong type, a 21st and a missing
// store refused; a delete queuing the object; a refused delete left on the
// ledger and drained by the inventory.object-deletions worker built through
// module.Workers, on a pool of one.
func TestLotScreens_Attachments(t *testing.T) {
	t.Parallel()
	objects := newMemoryStore()
	h := newHarness(t, modtest.WithObjectStore(objects))
	c, _ := signIn(t, h, lotManager...)
	viewer, _ := signIn(t, h, stockViewer...)
	admin, _ := signIn(t, h, everything...)
	s := seedLots(t, h, admin)
	e1, _, _ := receiveLots(t, h, c, s)
	files := lotPath(e1) + "/attachments"

	if res := viewer.Do(http.MethodPost, files, nil, fileForm(t, "3.1.pdf", "application/pdf", testCertificate())); res.Status != http.StatusForbidden {
		t.Errorf("an upload without lots-manage = %d", res.Status)
	}
	res := c.Do(http.MethodPost, files, nil, fileForm(t, "Sertifikat 3.1 ø.pdf", "application/pdf", testCertificate()))
	mustStatus(t, res, http.StatusCreated, "upload")
	var a gen.InventoryLotAttachment
	res.JSON(&a)
	if a.ContentType != "application/pdf" || a.FileName != "Sertifikat 3.1 ø.pdf" || a.UploadedBy == nil {
		t.Errorf("attachment = %+v", a)
	}
	if l := getLot(t, viewer, e1); len(l.Attachments) != 1 || l.Attachments[0].Id != a.Id {
		t.Errorf("the page's files = %+v", l.Attachments)
	}
	dl := viewer.Do(http.MethodGet, fmt.Sprintf("%s/%d", files, a.Id), nil)
	mustStatus(t, dl, http.StatusOK, "download")
	if !bytes.Equal(dl.Body, testCertificate()) {
		t.Errorf("downloaded %q", dl.Body)
	}
	if res := viewer.Do(http.MethodGet, fmt.Sprintf("%s/%d/attachments/%d", lotsPath, e1+1, a.Id), nil); res.Status != http.StatusNotFound {
		t.Errorf("a file under another lot = %d", res.Status)
	}

	// A PNG called a PDF; a text file.
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	expectRefusal(t, c.Do(http.MethodPost, files, nil, fileForm(t, "photo.pdf", "application/pdf", img.Bytes())), http.StatusUnsupportedMediaType, "attachment_type", "a PNG named .pdf")
	expectRefusal(t, c.Do(http.MethodPost, files, nil, fileForm(t, "notes.txt", "text/plain", []byte("hello"))), http.StatusUnsupportedMediaType, "attachment_type", "a text file")
	mustStatus(t, c.Do(http.MethodPost, files, nil, fileForm(t, "photo.png", "image/png", img.Bytes())), http.StatusCreated, "a PNG")
	// Twenty is the most.
	for range 18 {
		mustStatus(t, c.Do(http.MethodPost, files, nil, fileForm(t, "more.pdf", "application/pdf", testCertificate())), http.StatusCreated, "upload")
	}
	before := objects.count()
	expectRefusal(t, c.Do(http.MethodPost, files, nil, fileForm(t, "21st.pdf", "application/pdf", testCertificate())), http.StatusBadRequest, "too_many_attachments", "a 21st")
	if objects.count() != before {
		t.Errorf("the refused 21st left its object: %d objects, want %d", objects.count(), before)
	}

	// A delete removes the object at once.
	mustStatus(t, c.Do(http.MethodDelete, fmt.Sprintf("%s/%d", files, a.Id), nil), http.StatusNoContent, "delete")
	if n := h.Count(t, `SELECT count(*) FROM inventory.object_deletions`); n != 0 || objects.count() != 19 {
		t.Errorf("after a delete: %d ledger rows, %d objects", n, objects.count())
	}
	if res := c.Do(http.MethodDelete, fmt.Sprintf("%s/%d", files, a.Id), nil); res.Status != http.StatusNotFound {
		t.Errorf("a second delete = %d", res.Status)
	}
	// A refused delete stays on the ledger …
	objects.failDelete(errors.New("the store is down"))
	second := getLot(t, c, e1).Attachments[0]
	mustStatus(t, c.Do(http.MethodDelete, fmt.Sprintf("%s/%d", files, second.Id), nil), http.StatusNoContent, "delete while the store refuses")
	if n := h.Count(t, `SELECT count(*) FROM inventory.object_deletions WHERE attempts = 1`); n != 1 {
		t.Fatalf("ledger rows after a refused delete = %d", n)
	}
	// … until the worker, built as worker mode builds it on a pool of one,
	// finds the store back.
	objects.failDelete(nil)
	h.Exec(t, `UPDATE inventory.object_deletions SET next_attempt_at = '2000-01-01T00:00:00Z'`)
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
	for _, w := range h.Workers(h.DepsOn(pool)) {
		if w.Name() == "inventory.object-deletions" {
			cycle, _ = w.(interface {
				RunCycle(context.Context) (int, error)
			})
		}
	}
	if cycle == nil {
		t.Fatal("module.Workers built no inventory.object-deletions worker")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if n, err := cycle.RunCycle(ctx); err != nil || n != 1 {
		t.Fatalf("RunCycle = %d, %v; want 1 removed", n, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.object_deletions`); n != 0 || objects.count() != 18 {
		t.Errorf("after the worker: %d ledger rows, %d objects", n, objects.count())
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.events WHERE entity_kind = 'lot' AND kind IN ('lot_attachment_added', 'lot_attachment_removed')`); n != 22 {
		t.Errorf("file events = %d, want 20 added and 2 removed", n)
	}
}

// TestLotScreens_NoStorage is an upload and a download with no object store
// configured: 503 storage_not_configured.
func TestLotScreens_NoStorage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, lotManager...)
	admin, _ := signIn(t, h, everything...)
	s := seedLots(t, h, admin)
	e1, _, _ := receiveLots(t, h, c, s)
	expectRefusal(t, c.Do(http.MethodPost, lotPath(e1)+"/attachments", nil, fileForm(t, "3.1.pdf", "application/pdf", testCertificate())),
		http.StatusServiceUnavailable, "storage_not_configured", "an upload without storage")
	if n := h.Count(t, `SELECT count(*) FROM inventory.lot_attachments`); n != 0 {
		t.Errorf("rows = %d", n)
	}
}

// lotEvent is one notification on live.Channel.
type lotEvent struct {
	T  string `json:"t"`
	K  string `json:"k"`
	ID string `json:"id"`
}

// listenLots LISTENs on the harness's database, as a hub does, and answers
// what arrives.
func listenLots(t *testing.T, h *harness) <-chan lotEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, h.Pool().Config().ConnConfig.Copy())
	if err != nil {
		t.Fatalf("connect the listener: %v", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatalf("listen: %v", err)
	}
	events := make(chan lotEvent, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return
			}
			var e lotEvent
			if json.Unmarshal([]byte(n.Payload), &e) == nil && strings.HasPrefix(e.T, "inventory.") {
				events <- e
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	return events
}

// expectLotEvents waits for every one of want among what arrives.
func expectLotEvents(t *testing.T, events <-chan lotEvent, want ...lotEvent) {
	t.Helper()
	missing := map[lotEvent]bool{}
	for _, w := range want {
		missing[w] = true
	}
	deadline := time.After(10 * time.Second)
	for len(missing) > 0 {
		select {
		case e := <-events:
			delete(missing, e)
		case <-deadline:
			t.Fatalf("live events missing: %v", missing)
		}
	}
}

// TestLotScreens_Live is Inventory's topics (§17): a posting, a hold and an
// edit each tell the list and the lot's page on the writing transaction.
func TestLotScreens_Live(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, lotManager...)
	admin, _ := signIn(t, h, everything...)
	s := seedLots(t, h, admin)
	events := listenLots(t, h)
	e1, _, _ := receiveLots(t, h, c, s)
	key := strconv.FormatInt(e1, 10)
	want := []lotEvent{{T: "inventory.lots", K: "lot.changed", ID: key}, {T: "inventory.lot:" + key, K: "lot.changed", ID: key}}
	expectLotEvents(t, events, want...)
	mustStatus(t, c.Do(http.MethodPost, lotPath(e1)+"/holds", gen.InventoryLotHoldInput{Kind: "quarantine", Cause: "inspection"}), http.StatusOK, "hold")
	expectLotEvents(t, events, want...)
	l := getLot(t, c, e1)
	mustStatus(t, c.Do(http.MethodPut, lotPath(e1), gen.InventoryLotUpdate{Note: ptr("x")}, ifMatch(l.Revision)), http.StatusOK, "edit")
	expectLotEvents(t, events, want...)
}
