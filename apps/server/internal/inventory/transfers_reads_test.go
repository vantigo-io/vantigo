package inventory_test

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// What reads and guards a transfer (phase 2 design I46–I53, I62): the
// holder's in_transit, the archive and delete refusals, incoming on site
// stock, inTransit on the totals, the batch read of stock at sites, the
// sites' open counts, the lookup's TRF-, the movements' link, the note and
// the dashboard's overdue item.

// TestTransfer_HolderInTransit is I47 and I50: stock on its way, with
// nothing on hand or reserved, holds the variant for a delete and for the
// change to Service, as the quantity in transit and the number of
// transfers; nothing once the transfer is cancelled. The holder answers
// with the module disabled — built from no Deps at all — too.
func TestTransfer_HolderInTransit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	first := mustSend(t, c, createTransfer(t, c, s, line(varNut, 6)), s.shelf2.Id)
	second := mustSend(t, c, createTransfer(t, c, s, line(varNut, 4)), s.shelf2.Id)
	held := func(holder contracts.VariantReferenceHolder, purpose contracts.VariantHoldPurpose) *contracts.VariantHold {
		t.Helper()
		ctx := context.Background()
		tx, err := h.Pool().Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		hold, err := holder.VariantHeld(ctx, tx, varNut, purpose)
		if err != nil {
			t.Fatalf("VariantHeld: %v", err)
		}
		return hold
	}
	enabled := inventory.Module().VariantReferences(h.Deps())
	disabled := inventory.Module().VariantReferences(module.Deps{})
	for _, holder := range []contracts.VariantReferenceHolder{enabled, disabled} {
		for _, purpose := range []contracts.VariantHoldPurpose{contracts.VariantHoldDelete, contracts.VariantHoldTypeChange} {
			if hold := held(holder, purpose); hold == nil || hold.Kind != contracts.VariantHoldKindInTransit || hold.Quantity != "10" || hold.Locations != 2 {
				t.Errorf("VariantHeld(%v) = %+v, want 10 in transit on 2 transfers", purpose, hold)
			}
		}
	}
	// Received in part: what is left is still in transit, on one transfer.
	transferAnswer(t, receiveTransfer(c, first.Id, nil, arrived(first.Lines[0].Id, 6, s.bgo.RootLocationId)), http.StatusOK, "receive the first")
	mustStatus(t, c.Do(http.MethodPost, adjustmentsPath, gen.InventoryAdjustmentInput{
		ClientRequestId: uuid.New(), SiteId: s.bgo.Id, Reason: gen.InventoryAdjustmentReasonCorrection,
		Lines: []gen.InventoryAdjustmentLine{change(varNut, s.bgo.RootLocationId, -6)},
	}), http.StatusCreated, "adjust the nuts away at BGO")
	if hold := held(enabled, contracts.VariantHoldDelete); hold == nil || hold.Kind != contracts.VariantHoldKindInTransit || hold.Quantity != "4" || hold.Locations != 1 {
		t.Errorf("VariantHeld = %+v, want 4 on 1 transfer", hold)
	}
	mustStatus(t, cancelTransfer(c, second), http.StatusOK, "cancel the second")
	if hold := held(enabled, contracts.VariantHoldDelete); hold == nil || hold.Kind != contracts.VariantHoldKindStock {
		t.Errorf("VariantHeld after the cancel = %+v, want the stock back on the shelf", hold)
	}
	mustStatus(t, c.Do(http.MethodPost, adjustmentsPath, gen.InventoryAdjustmentInput{
		ClientRequestId: uuid.New(), SiteId: s.osl.Id, Reason: gen.InventoryAdjustmentReasonCorrection,
		Lines: []gen.InventoryAdjustmentLine{change(varNut, s.shelf2.Id, -4)},
	}), http.StatusCreated, "adjust the nuts away at OSL")
	if hold := held(enabled, contracts.VariantHoldTypeChange); hold != nil {
		t.Errorf("VariantHeld = %+v, want nothing", hold)
	}
}

// TestTransfer_ArchiveRefusals is I48: a site a transfer on its way names is
// not archived (site_has_open_transfers), the source location of a line in
// transit — or one above it — is not archived (location_in_transit), and a
// site any transfer names is not deleted (site_has_transfers). Once the
// transfer is received, both archive.
func TestTransfer_ArchiveRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	bin := createLocation(t, c, s.shelf2.Id, gen.InventoryLocationKindBin, "A")
	receive(t, c, s.osl.Id, bin.Id, receiptLine(varWasher, 2))
	tr := mustSend(t, c, createTransfer(t, c, s, line(varWasher, 2)), bin.Id)
	bgo := siteNow(t, c, s.bgo.Id)
	res := c.Do(http.MethodPut, sitePath(bgo.Id), gen.InventorySiteUpdate{Code: bgo.Code, Name: bgo.Name, Kind: bgo.Kind, Status: gen.Archived}, ifMatch(bgo.Revision))
	expectRefusal(t, res, http.StatusConflict, "site_has_open_transfers", "archive the destination")
	bin = getLocation(t, c, bin.Id).Location
	expectRefusal(t, putLocation(c, bin, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusConflict, "location_in_transit", "archive the source")
	shelf2 := getLocation(t, c, s.shelf2.Id).Location
	receive(t, c, s.osl.Id, s.shelf1.Id, receiptLine(varNut, 10))
	move(c, s.osl.Id, gen.InventoryMoveLine{VariantId: varNut, FromLocationId: s.shelf2.Id, ToLocationId: s.shelf1.Id, Quantity: qty(10)})
	expectRefusal(t, putLocation(c, shelf2, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusConflict, "location_in_transit", "archive above the source")
	// Without stock-view the refusal does not say that stock is on its way.
	structure, _ := signIn(t, h, locationsManager...)
	p := expectRefusal(t, putLocation(structure, shelf2, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusConflict, "location_in_transit", "archive without stock-view")
	if p.Detail == nil || strings.Contains(*p.Detail, "on its way") {
		t.Errorf("detail without stock-view = %v, want it neutral", p.Detail)
	}
	fresh := createSite(t, c, "NEW")
	transferAnswer(t, c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, fresh.Id, line(varBolt, 1))), http.StatusCreated, "a draft to NEW")
	expectRefusal(t, c.Do(http.MethodDelete, sitePath(fresh.Id), nil), http.StatusConflict, "site_has_transfers", "delete a site a draft names")

	transferAnswer(t, receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 2, s.bgoShelf.Id)), http.StatusOK, "receive")
	bin = getLocation(t, c, bin.Id).Location
	mustStatus(t, putLocation(c, bin, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusOK, "archive the source once received")
	mustStatus(t, adjust(c, s.bgo.Id, change(varWasher, s.bgoShelf.Id, -2)), http.StatusCreated, "clear BGO")
	bgo = siteNow(t, c, s.bgo.Id)
	res = c.Do(http.MethodPut, sitePath(bgo.Id), gen.InventorySiteUpdate{Code: bgo.Code, Name: bgo.Name, Kind: bgo.Kind, Status: gen.Archived}, ifMatch(bgo.Revision))
	mustStatus(t, res, http.StatusOK, "archive BGO once received")
}

// siteNow reads a site.
func siteNow(t *testing.T, c *modtest.Client, id int64) gen.InventorySite {
	t.Helper()
	res := c.Do(http.MethodGet, sitePath(id), nil)
	mustStatus(t, res, http.StatusOK, "GET /sites/{id}")
	var s gen.InventorySite
	res.JSON(&s)
	return s
}

// TestTransfer_StockReads is I46 and I53: incoming per to site on the
// variant's and the product's stock — a site with only incoming has its row —
// inTransit on the totals, keeping a variant wholly in transit; the batch of
// stock at sites, every pair asked for with its locations most first; and
// the sites' open transfers.
func TestTransfer_StockReads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	receive(t, c, s.osl.Id, s.shelf2.Id, receiptLine(varBolt, 5))
	mustSend(t, c, createTransfer(t, c, s, line(varBolt, 7), line(varNut, 10)), s.shelf1.Id, s.shelf2.Id)

	var vs gen.InventoryVariantStock
	res := c.Do(http.MethodGet, variantsPath+"/"+strconv.Itoa(int(varNut))+"/stock", nil)
	mustStatus(t, res, http.StatusOK, "the nuts' stock")
	res.JSON(&vs)
	if vs.TotalOnHand != 0 || len(vs.Sites) != 1 || vs.Sites[0].Site.Code != "BGO" || vs.Sites[0].Incoming != 10 || len(vs.Sites[0].Rows) != 0 {
		t.Errorf("nut stock = %+v, want BGO's row with 10 incoming and nothing on hand", vs)
	}
	var ps gen.InventoryProductStock
	res = c.Do(http.MethodGet, "/api/v1/inventory/products/10/stock", nil)
	mustStatus(t, res, http.StatusOK, "the product's stock")
	res.JSON(&ps)
	if len(ps.Variants) != 2 || len(ps.Variants[0].Sites) != 2 || ps.Variants[0].Sites[0].Site.Code != "BGO" || ps.Variants[0].Sites[0].Incoming != 7 ||
		ps.Variants[0].Sites[1].Incoming != 0 || ps.Variants[0].Sites[1].OnHand != 18 {
		t.Errorf("product stock = %+v, want the bolts at OSL and 7 incoming at BGO", ps)
	}

	var totals gen.InventoryStockTotals
	res = c.Do(http.MethodGet, "/api/v1/inventory/stock?variantIds="+strconv.Itoa(int(varBolt))+","+strconv.Itoa(int(varNut)), nil)
	mustStatus(t, res, http.StatusOK, "the totals")
	res.JSON(&totals)
	if len(totals.Totals) != 2 || totals.Totals[0].OnHand != 18 || totals.Totals[0].InTransit != 7 ||
		totals.Totals[1].VariantId != varNut || totals.Totals[1].OnHand != 0 || totals.Totals[1].InTransit != 10 {
		t.Errorf("totals = %+v, want the nuts kept wholly in transit", totals.Totals)
	}

	var at gen.InventoryStockAtSites
	res = c.Do(http.MethodGet, "/api/v1/inventory/stock/sites?variantIds="+strconv.Itoa(int(varBolt))+","+strconv.Itoa(int(varWasher))+
		"&siteIds="+strconv.FormatInt(s.osl.Id, 10)+","+strconv.FormatInt(s.bgo.Id, 10), nil)
	mustStatus(t, res, http.StatusOK, "stock at sites")
	res.JSON(&at)
	if len(at.Stock) != 4 {
		t.Fatalf("stock at sites = %+v, want every pair", at.Stock)
	}
	bolt := at.Stock[0]
	if bolt.VariantId != varBolt || bolt.SiteId != s.osl.Id || bolt.OnHand != 18 || bolt.Available != 18 || len(bolt.Locations) != 2 ||
		bolt.Locations[0].Location.FullCode != "OSL-1" || bolt.Locations[0].OnHand != 13 || bolt.Locations[1].OnHand != 5 {
		t.Errorf("bolts at OSL = %+v, want 13 on OSL-1 then 5 on OSL-2", bolt)
	}
	if b := at.Stock[1]; b.SiteId != s.bgo.Id || b.Incoming != 7 || b.OnHand != 0 || len(b.Locations) != 0 {
		t.Errorf("bolts at BGO = %+v, want 7 incoming", b)
	}
	if w := at.Stock[2]; w.VariantId != varWasher || w.OnHand != 0 || w.Locations == nil {
		t.Errorf("washers at OSL = %+v, want an empty row", w)
	}
	mustStatus(t, c.Do(http.MethodGet, "/api/v1/inventory/stock/sites?variantIds=1&siteIds=999999", nil), http.StatusBadRequest, "an unknown site")
	mustStatus(t, c.Do(http.MethodGet, "/api/v1/inventory/stock/sites?variantIds=1&siteIds="+strings.Repeat("1,", 20)+"21", nil), http.StatusBadRequest, "21 sites")

	osl := siteNow(t, c, s.osl.Id)
	bgo := siteNow(t, c, s.bgo.Id)
	if osl.OutgoingTransfers == nil || *osl.OutgoingTransfers != 1 || *osl.IncomingTransfers != 0 || bgo.IncomingTransfers == nil || *bgo.IncomingTransfers != 1 {
		t.Errorf("open counts = OSL %v/%v, BGO %v/%v", osl.IncomingTransfers, osl.OutgoingTransfers, bgo.IncomingTransfers, bgo.OutgoingTransfers)
	}
	structure, _ := signIn(t, h, locationsViewer...)
	if s := siteNow(t, structure, s.osl.Id); s.OutgoingTransfers != nil {
		t.Errorf("the counts to locations-view alone = %v, want none", s.OutgoingTransfers)
	}
}

// TestTransfer_LookupAndMovements is I52 and I45: TRF-<number> finds the
// transfer for a caller holding both views and no one else; the movements
// list and page link back to it; the kind filter takes the new kinds.
func TestTransfer_LookupAndMovements(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	tr := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 1)), s.shelf1.Id)
	lookup := func(q string) []gen.InventoryLookupMatch {
		t.Helper()
		var out gen.InventoryLookupResult
		res := c.Do(http.MethodGet, lookupPath+"?q="+url.QueryEscape(q), nil)
		mustStatus(t, res, http.StatusOK, "lookup "+q)
		res.JSON(&out)
		return out.Matches
	}
	token := "trf-" + strconv.FormatInt(tr.Number, 10)
	if m := lookup(token); len(m) != 1 || m[0].Kind != gen.InventoryLookupMatchKindTransfer || m[0].Transfer == nil || m[0].Transfer.Id != tr.Id || m[0].Transfer.Number != tr.Number {
		t.Errorf("lookup %s = %+v", token, m)
	}
	if m := lookup("TRF-999999"); len(m) != 0 {
		t.Errorf("lookup of no transfer = %+v", m)
	}
	structure, _ := signIn(t, h, locationsViewer...)
	var out gen.InventoryLookupResult
	res := structure.Do(http.MethodGet, lookupPath+"?q="+token, nil)
	mustStatus(t, res, http.StatusOK, "lookup to locations-view")
	res.JSON(&out)
	if len(out.Matches) != 0 {
		t.Errorf("a transfer to locations-view alone = %+v, want none", out.Matches)
	}

	var page gen.InventoryMovementPage
	res = c.Do(http.MethodGet, movementsPath+"?kind=transfer_out", nil)
	mustStatus(t, res, http.StatusOK, "the transfer_out movements")
	res.JSON(&page)
	if len(page.Movements) != 1 || page.Movements[0].Kind != gen.TransferOut || page.Movements[0].Transfer == nil || page.Movements[0].Transfer.Id != tr.Id {
		t.Errorf("movements = %+v, want the send's, linked", page.Movements)
	}
	var receipts gen.InventoryMovementPage
	res = c.Do(http.MethodGet, movementsPath+"?kind=receipt", nil)
	res.JSON(&receipts)
	for _, m := range receipts.Movements {
		if m.Transfer != nil {
			t.Errorf("receipt %d links to a transfer", m.Id)
		}
	}
}

// TestTransfer_Note is I51: the følgeseddel of a sent transfer names every
// line with its quantity, entry and source; a draft has none (404); a viewer
// prints it.
func TestTransfer_Note(t *testing.T) {
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	h.catalog.setPackagings(varBolt, boxAndPallet()...)
	draft := createTransfer(t, c, s, gen.InventoryTransferLineInput{VariantId: varBolt, Entered: entered(qty(2), pkgBox, 1)}, line(varNut, 3))
	mustStatus(t, c.Do(http.MethodGet, transferPath(draft.Id)+"/note.pdf", nil), http.StatusNotFound, "a draft's note")
	tr := mustSend(t, c, draft, s.shelf1.Id, s.shelf2.Id)

	var mu sync.Mutex
	var printed [][]string
	inventory.SetTransferNoteHook(func(rows [][]string) {
		mu.Lock()
		defer mu.Unlock()
		printed = rows
	})
	t.Cleanup(func() { inventory.SetTransferNoteHook(nil) })
	viewer, _ := signIn(t, h, stockViewer...)
	res := viewer.Do(http.MethodGet, transferPath(tr.Id)+"/note.pdf", nil)
	mustStatus(t, res, http.StatusOK, "the note")
	if ct := res.Header("Content-Type"); ct != "application/pdf" || !bytes.HasPrefix(res.Body, []byte("%PDF")) {
		t.Errorf("content type %q, body %.8q", ct, res.Body)
	}
	if d := res.Header("Content-Disposition"); !strings.Contains(d, "folgeseddel-TRF-"+strconv.FormatInt(tr.Number, 10)+".pdf") {
		t.Errorf("disposition = %q", d)
	}
	mu.Lock()
	defer mu.Unlock()
	want := [][]string{{"BOLT-M8", "Bolt M8", "17 pcs (1 × Box, 2 loose)", "OSL-1"}, {"NUT-M8", "Nut M8", "3 pcs", "OSL-2"}}
	if !slices.EqualFunc(printed, want, slices.Equal) {
		t.Errorf("printed = %q, want %q", printed, want)
	}
	mustStatus(t, c.Do(http.MethodGet, transferPath(999999)+"/note.pdf", nil), http.StatusNotFound, "no transfer's note")
}

// TestTransfer_Overdue is I62: a transfer past its expected arrival, or
// without one on its way more than a week, is overdue — in the list and as
// the dashboard's transferOverdue item, for transfers-manage with both views
// only; the count item keeps its own gate.
func TestTransfer_Overdue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	yesterday := openapi_types.Date{Time: h.Now().UTC().AddDate(0, 0, -2)}
	in := draftInput(s.osl.Id, s.bgo.Id, line(varBolt, 1))
	in.ExpectedArrival = &yesterday
	late := mustSend(t, c, transferAnswer(t, c.Do(http.MethodPost, transfersPath, in), http.StatusCreated, "a draft due yesterday"), s.shelf1.Id)
	slow := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 1)), s.shelf1.Id)
	attention := func(c *modtest.Client) []gen.InventoryStatsAttentionItem {
		t.Helper()
		var items []gen.InventoryStatsAttentionItem
		res := c.Do(http.MethodGet, attentionPath, nil)
		mustStatus(t, res, http.StatusOK, "GET /stats/attention")
		res.JSON(&items)
		return items
	}
	items := attention(c)
	if len(items) != 1 || items[0].Type != gen.TransferOverdue || items[0].EntityId != strconv.FormatInt(late.Id, 10) || items[0].Title == "" {
		t.Fatalf("attention = %+v, want the late transfer", items)
	}
	h.Advance(8 * 24 * time.Hour)
	c, _ = signIn(t, h, allKeys...)
	items = attention(c)
	if len(items) != 2 || items[1].EntityId != strconv.FormatInt(slow.Id, 10) {
		t.Errorf("attention = %+v, want both, the slow one second", items)
	}
	var page gen.InventoryTransferPage
	res := c.Do(http.MethodGet, transfersPath+"?status=sent", nil)
	res.JSON(&page)
	for _, tr := range page.Transfers {
		if !tr.Overdue {
			t.Errorf("transfer %d not overdue", tr.Number)
		}
	}
	counter, _ := signIn(t, h, "inventory:locations-view", "inventory:stock-view", "inventory:counts-manage")
	if items := attention(counter); len(items) != 0 {
		t.Errorf("a counter's attention = %+v, want none", items)
	}
	viewer, _ := signIn(t, h, stockViewer...)
	if items := attention(viewer); len(items) != 0 {
		t.Errorf("a viewer's attention = %+v, want none", items)
	}
}
