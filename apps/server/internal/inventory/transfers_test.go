package inventory_test

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Transfers between sites (phase 2 design I35–I45): the drafts, the four
// actions and their ledger, replays and refusals, and spec §9's edge cases
// 1–8 by name (the races in transfers_race_test.go).

const transfersPath = "/api/v1/inventory/transfers"

func transferPath(id int64) string { return transfersPath + "/" + strconv.FormatInt(id, 10) }

// The permission sets of a transfer test: transfers-manage with both views,
// and every key of the module.
var (
	transferrer = []string{"inventory:locations-view", "inventory:stock-view", "inventory:transfers-manage"}
	allKeys     = append(slices.Clone(everything), "inventory:transfers-manage")
)

// transferSites is two sites the transfers go between: OSL with two shelves,
// 20 bolts on shelf 1 and 10 nuts on shelf 2, and BGO with one shelf.
type transferSites struct {
	osl, bgo       gen.InventorySite
	shelf1, shelf2 gen.InventoryLocation
	bgoShelf       gen.InventoryLocation
}

func newTransferSites(t *testing.T, h *harness, c *modtest.Client) transferSites {
	t.Helper()
	seedCatalog(h)
	s := transferSites{osl: createSite(t, c, "OSL"), bgo: createSite(t, c, "BGO")}
	s.shelf1 = createLocation(t, c, s.osl.RootLocationId, gen.InventoryLocationKindShelf, "1")
	s.shelf2 = createLocation(t, c, s.osl.RootLocationId, gen.InventoryLocationKindShelf, "2")
	s.bgoShelf = createLocation(t, c, s.bgo.RootLocationId, gen.InventoryLocationKindShelf, "B1")
	receive(t, c, s.osl.Id, s.shelf1.Id, receiptLine(varBolt, 20))
	receive(t, c, s.osl.Id, s.shelf2.Id, receiptLine(varNut, 10))
	return s
}

// line is a draft line of quantity of variant.
func line(variant int32, quantity float64) gen.InventoryTransferLineInput {
	return gen.InventoryTransferLineInput{VariantId: variant, Quantity: qty(quantity)}
}

// draftInput is a draft from from to to with lines.
func draftInput(from, to int64, lines ...gen.InventoryTransferLineInput) gen.InventoryTransferInput {
	return gen.InventoryTransferInput{FromSiteId: from, ToSiteId: to, Lines: lines}
}

// transferAnswer fails unless res answered want, and decodes the transfer.
func transferAnswer(t *testing.T, res *modtest.Response, want int, what string) gen.InventoryTransfer {
	t.Helper()
	mustStatus(t, res, want, what)
	var out gen.InventoryTransfer
	res.JSON(&out)
	return out
}

// createTransfer makes a draft from OSL to BGO.
func createTransfer(t *testing.T, c *modtest.Client, s transferSites, lines ...gen.InventoryTransferLineInput) gen.InventoryTransfer {
	t.Helper()
	return transferAnswer(t, c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, s.bgo.Id, lines...)), http.StatusCreated, "POST /transfers")
}

// sendLines names one source location for every line of tr.
func sendLines(tr gen.InventoryTransfer, from ...int64) []gen.InventoryTransferSendLine {
	out := make([]gen.InventoryTransferSendLine, 0, len(tr.Lines))
	for i, l := range tr.Lines {
		out = append(out, gen.InventoryTransferSendLine{LineId: l.Id, FromLocationId: ptr(from[i])})
	}
	return out
}

// send posts a send of tr with in, at tr's revision.
func send(c *modtest.Client, tr gen.InventoryTransfer, in gen.InventoryTransferSendInput) *modtest.Response {
	return c.Do(http.MethodPost, transferPath(tr.Id)+"/send", in, ifMatch(tr.Revision))
}

// mustSend sends tr, each line from its location, and answers the transfer.
func mustSend(t *testing.T, c *modtest.Client, tr gen.InventoryTransfer, from ...int64) gen.InventoryTransfer {
	t.Helper()
	return transferAnswer(t, send(c, tr, gen.InventoryTransferSendInput{Lines: sendLines(tr, from...)}), http.StatusOK, "send")
}

// arrived is a receive line: quantity of the transfer line, to location.
func arrived(lineID int64, quantity float64, to int64) gen.InventoryTransferReceiveLine {
	return gen.InventoryTransferReceiveLine{LineId: lineID, Quantity: qty(quantity), ToLocationId: to}
}

// receiveTransfer posts a receive of id under its own new client request id.
func receiveTransfer(c *modtest.Client, id int64, closing *gen.InventoryTransferClose, lines ...gen.InventoryTransferReceiveLine) *modtest.Response {
	return c.Do(http.MethodPost, transferPath(id)+"/receive", gen.InventoryTransferReceiveInput{ClientRequestId: uuid.New(), Lines: lines, Close: closing})
}

// cancelTransfer posts a cancel of tr at its revision.
func cancelTransfer(c *modtest.Client, tr gen.InventoryTransfer) *modtest.Response {
	return c.Do(http.MethodPost, transferPath(tr.Id)+"/cancel", gen.InventoryTransferCancelInput{ClientRequestId: uuid.New()}, ifMatch(tr.Revision))
}

// getTransfer reads a transfer.
func getTransfer(t *testing.T, c *modtest.Client, id int64) gen.InventoryTransfer {
	t.Helper()
	return transferAnswer(t, c.Do(http.MethodGet, transferPath(id), nil), http.StatusOK, "GET /transfers/{id}")
}

// getMovement reads a movement.
func getMovement(t *testing.T, c *modtest.Client, id int64) gen.InventoryMovement {
	t.Helper()
	res := c.Do(http.MethodGet, movementPath(id), nil)
	mustStatus(t, res, http.StatusOK, "GET /movements/{id}")
	var m gen.InventoryMovement
	res.JSON(&m)
	return m
}

// inTransitTotal is what is in transit across every line, from the store.
func inTransitTotal(t *testing.T, h *harness) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT coalesce(sum(sent - received - missing - returned), 0)::text FROM inventory.transfer_lines`)
}

// actionKinds is a transfer's actions' kinds and each one's lines as
// kind:quantity@location.
func actionKinds(tr gen.InventoryTransfer) []string {
	var out []string
	for _, a := range tr.Actions {
		out = append(out, string(a.Kind))
		for _, l := range a.Lines {
			at := "-"
			if l.Location != nil {
				at = l.Location.FullCode
			}
			out = append(out, string(l.Kind)+":"+strconv.FormatFloat(l.Quantity, 'f', -1, 64)+"@"+at)
		}
	}
	return out
}

// TestTransfer_Drafts is I36 and I37: a draft's number from the series, the
// variants' snapshots, a quantity in packagings resolved to units, a
// suggestion kept as given, the replace and the delete, each with If-Match,
// the record, and that a draft holds nothing.
func TestTransfer_Drafts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	h.catalog.setPackagings(varBolt, boxAndPallet()...)

	ref, note, expected := "PO 77", "Top shelf first", openapi_types.Date{Time: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}
	in := draftInput(s.osl.Id, s.bgo.Id,
		gen.InventoryTransferLineInput{VariantId: varBolt, Entered: entered(qty(1), pkgBox, 1)},
		gen.InventoryTransferLineInput{VariantId: varNut, Quantity: qty(4), Suggestion: &gen.InventoryTransferSuggestion{Needed: 3.5, PackagingId: nil, Count: nil}},
		line(varBolt, 2),
	)
	in.Reference, in.Note, in.ExpectedArrival = &ref, &note, &expected
	tr := transferAnswer(t, c.Do(http.MethodPost, transfersPath, in), http.StatusCreated, "create")
	if tr.Number != 1001 || tr.Status != gen.InventoryTransferStatusDraft || tr.Revision != 1 || tr.FromSite.Code != "OSL" || tr.ToSite.Code != "BGO" ||
		*tr.Reference != ref || *tr.Note != note || tr.ExpectedArrival == nil || !tr.ExpectedArrival.Equal(expected.Time) ||
		tr.CreatedBy.UserId == nil || *tr.CreatedBy.UserId != user || tr.SentAt != nil || len(tr.Lines) != 3 || len(tr.Actions) != 0 {
		t.Fatalf("draft = %+v", tr)
	}
	if l := tr.Lines[0]; l.Quantity != 16 || l.Entered == nil || len(l.Entered.Packagings) != 1 || l.Variant.Sku != "BOLT-M8" || l.Sent != 0 || l.InTransit != 0 {
		t.Errorf("line 1 = %+v, want 16 bolts entered as a box and one", l)
	}
	if l := tr.Lines[1]; l.Suggestion == nil || l.Suggestion.Needed != 3.5 || l.Suggestion.PackagingId != nil {
		t.Errorf("line 2's suggestion = %+v, want it as given", l.Suggestion)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.transfer_lines WHERE sku = 'NUT-M8' AND product_name = 'Nut M8'`); n != 1 {
		t.Errorf("%d snapshotted nut lines, want 1", n)
	}
	// A draft holds nothing (I37).
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "20.000" {
		t.Errorf("bolts on shelf 1 = %s, want 20 still", got)
	}
	second := createTransfer(t, c, s, line(varNut, 1))
	if second.Number != 1002 {
		t.Errorf("second number = %d, want 1002", second.Number)
	}

	// The replace: whole, at the revision read; a stale one is refused.
	put := draftInput(s.osl.Id, s.bgo.Id, line(varWasher, 1))
	expectRefusal(t, c.Do(http.MethodPut, transferPath(tr.Id), put, ifMatch(tr.Revision+1)), http.StatusConflict, "revision_conflict", "a stale replace")
	mustStatus(t, c.Do(http.MethodPut, transferPath(tr.Id), put), http.StatusBadRequest, "a replace without If-Match")
	tr = transferAnswer(t, c.Do(http.MethodPut, transferPath(tr.Id), put, ifMatch(tr.Revision)), http.StatusOK, "replace")
	if tr.Revision != 2 || len(tr.Lines) != 1 || tr.Lines[0].Variant.Sku != "WASH-8" || tr.Reference != nil || tr.Number != 1001 {
		t.Errorf("replaced = %+v", tr)
	}

	// The delete.
	expectRefusal(t, c.Do(http.MethodDelete, transferPath(second.Id), nil, ifMatch(9)), http.StatusConflict, "revision_conflict", "a stale delete")
	mustStatus(t, c.Do(http.MethodDelete, transferPath(second.Id), nil, ifMatch(second.Revision)), http.StatusNoContent, "delete")
	mustStatus(t, c.Do(http.MethodGet, transferPath(second.Id), nil), http.StatusNotFound, "the deleted draft")
	mustStatus(t, c.Do(http.MethodDelete, transferPath(second.Id), nil, ifMatch(1)), http.StatusNotFound, "delete it again")
	mustStatus(t, c.Do(http.MethodPut, transferPath(999999), put, ifMatch(1)), http.StatusNotFound, "replace no transfer")
	if third := createTransfer(t, c, s, line(varNut, 1)); third.Number != 1003 {
		t.Errorf("third number = %d, want 1003: a number is never handed out again", third.Number)
	}
	kinds := eventKinds(t, h)
	for _, want := range []string{"transfer_created", "transfer_updated", "transfer_deleted"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("events = %v, want %s", kinds, want)
		}
	}
}

// TestTransfer_DraftRefusals: what a draft is refused for — the same site, an
// archived site, no lines or too many, a variant not in the catalog or a
// service, a bad quantity — and that a viewer cannot write one.
func TestTransfer_DraftRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	expectRefusal(t, c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, s.osl.Id, line(varBolt, 1))), http.StatusBadRequest, "transfer_same_site", "the same site")
	mustStatus(t, c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, s.bgo.Id)), http.StatusBadRequest, "no lines")
	many := make([]gen.InventoryTransferLineInput, 501)
	for i := range many {
		many[i] = line(varBolt, 1)
	}
	expectRefusal(t, c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, s.bgo.Id, many...)), http.StatusBadRequest, "too_many_lines", "501 lines")
	res := c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, s.bgo.Id, line(999, 1), line(varService, 1), line(varBolt, 0), line(varBolt, 1.2345), line(varOld, 1)))
	p := expectRefusal(t, res, http.StatusBadRequest, "variant_not_found", "bad lines")
	if got := lineCodes(p); !slices.Equal(got, []string{"0:variant_not_found", "1:variant_not_stockable", "2:quantity_invalid", "3:quantity_invalid"}) {
		t.Errorf("lines = %v; a discontinued variant may be sent", got)
	}
	mustStatus(t, c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, 999999, line(varBolt, 1))), http.StatusBadRequest, "an unknown site")
	empty := createSite(t, c, "TRD")
	mustStatus(t, c.Do(http.MethodPut, sitePath(empty.Id), gen.InventorySiteUpdate{Code: empty.Code, Name: empty.Name, Kind: empty.Kind, Status: gen.Archived}, ifMatch(empty.Revision)),
		http.StatusOK, "archive TRD")
	expectRefusal(t, c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, empty.Id, line(varBolt, 1))), http.StatusBadRequest, "site_archived", "to an archived site")
	res = c.Do(http.MethodPost, transfersPath, map[string]any{"fromSiteId": s.osl.Id, "toSiteId": s.bgo.Id, "lines": []any{map[string]any{"variantId": varBolt, "quantity": 1, "lot": "A"}}})
	mustStatus(t, res, http.StatusBadRequest, "a line with a property the contract does not have")

	viewer, _ := signIn(t, h, stockViewer...)
	mustStatus(t, viewer.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, s.bgo.Id, line(varBolt, 1))), http.StatusForbidden, "a viewer's create")
	mustStatus(t, viewer.Do(http.MethodGet, transfersPath, nil), http.StatusOK, "a viewer's list")
	if n := h.Count(t, `SELECT count(*) FROM inventory.transfers`); n != 0 {
		t.Errorf("%d transfers, want none", n)
	}
}

// TestTransfer_SendAndReceiveInParts is I39–I41 and I45: the send's
// transfer_out at the from site, in transit on the lines, a receive in two
// parts — the second not a replay — each a transfer_in at the to site, the
// movements' source references, the action lines, and the balances.
func TestTransfer_SendAndReceiveInParts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	tr := createTransfer(t, c, s, line(varBolt, 12), line(varNut, 4))
	tr = mustSend(t, c, tr, s.shelf1.Id, s.shelf2.Id)
	if tr.Status != gen.InventoryTransferStatusSent || tr.SentBy == nil || *tr.SentBy.UserId != user || tr.SentAt == nil || tr.InTransit != 16 ||
		tr.Lines[0].Sent != 12 || tr.Lines[0].InTransit != 12 || tr.Lines[0].FromLocation == nil || tr.Lines[0].FromLocation.FullCode != "OSL-1" {
		t.Fatalf("sent = %+v", tr)
	}
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "8.000" {
		t.Errorf("bolts at OSL-1 = %s, want 8", got)
	}
	if got, want := actionKinds(tr), []string{"send", "sent:12@OSL-1", "sent:4@OSL-2"}; !slices.Equal(got, want) {
		t.Errorf("actions = %v, want %v", got, want)
	}
	out := getMovement(t, c, *tr.Actions[0].MovementId)
	if out.Kind != gen.TransferOut || out.Site.Code != "OSL" || len(out.Lines) != 2 || out.Lines[0].From == nil || out.Lines[0].To != nil ||
		out.Source == nil || out.Source.Module != "inventory" || out.Source.Kind != "transfer" || out.Source.Id != strconv.FormatInt(tr.Id, 10) ||
		out.Source.LineId == nil || *out.Source.LineId != strconv.FormatInt(tr.Actions[0].Id, 10) ||
		out.Transfer == nil || out.Transfer.Number != tr.Number || out.ClientRequestId == uuid.Nil {
		t.Errorf("transfer_out = %+v", out)
	}

	// The first part: 5 bolts to the shelf, the nuts to the root.
	tr = transferAnswer(t, receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 5, s.bgoShelf.Id), arrived(tr.Lines[1].Id, 0, s.bgo.RootLocationId)),
		http.StatusOK, "the first part")
	if tr.Status != gen.InventoryTransferStatusPartlyReceived || tr.Lines[0].Received != 5 || tr.Lines[0].InTransit != 7 || tr.Lines[1].InTransit != 4 || tr.ClosedAt != nil {
		t.Errorf("after the first part = %+v", tr)
	}
	in := getMovement(t, c, *tr.Actions[1].MovementId)
	if in.Kind != gen.TransferIn || in.Site.Code != "BGO" || len(in.Lines) != 1 || in.Lines[0].To == nil || in.Lines[0].To.Id != s.bgoShelf.Id || in.Lines[0].From != nil ||
		in.Source == nil || *in.Source.LineId != strconv.FormatInt(tr.Actions[1].Id, 10) {
		t.Errorf("transfer_in = %+v", in)
	}
	// The second part: everything outstanding, under a new id — not a replay.
	tr = transferAnswer(t, receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 7, s.bgoShelf.Id), arrived(tr.Lines[1].Id, 4, s.bgo.RootLocationId)),
		http.StatusOK, "the second part")
	if tr.Status != gen.InventoryTransferStatusReceived || tr.InTransit != 0 || tr.ClosedAt == nil || tr.ClosedBy == nil || tr.CloseReason != nil {
		t.Errorf("received = %+v", tr)
	}
	if got, want := actionKinds(tr)[3:], []string{"receive", "received:5@BGO-B1", "receive", "received:7@BGO-B1", "received:4@BGO"}; !slices.Equal(got, want) {
		t.Errorf("receive actions = %v, want %v", got, want)
	}
	if got := onHand(t, h, varBolt, s.bgoShelf.Id); got != "12.000" {
		t.Errorf("bolts at BGO-B1 = %s, want 12", got)
	}
	if got := onHand(t, h, varNut, s.bgo.RootLocationId); got != "4.000" {
		t.Errorf("nuts at BGO = %s, want 4: the root takes stock (§9, case 10)", got)
	}
	if got := inTransitTotal(t, h); got != "0.000" {
		t.Errorf("in transit = %s, want 0", got)
	}
	expectRefusal(t, receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 1, s.bgoShelf.Id)), http.StatusConflict, "transfer_not_receivable", "a received transfer")
	if n := h.Count(t, `SELECT count(*) FROM inventory.movements WHERE kind IN ('transfer_out', 'transfer_in')`); n != 3 {
		t.Errorf("%d transfer movements, want 3", n)
	}
	kinds := eventKinds(t, h)
	if !slices.Contains(kinds, "transfer_sent") || !slices.Contains(kinds, "transfer_received") {
		t.Errorf("events = %v", kinds)
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_Replays is I38's step 2: a send replayed after its revision
// moved; a receive replayed after its own success (§9, case 4) — never
// over-received — and a client id reused with another body; each replay posts
// nothing.
func TestTransfer_Replays(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	// transfers-manage with both views is all a transfer's writes need (I63).
	mover, _ := signIn(t, h, transferrer...)
	draft := createTransfer(t, mover, s, line(varBolt, 6))
	body := gen.InventoryTransferSendInput{Lines: sendLines(draft, s.shelf1.Id)}
	sent := transferAnswer(t, send(mover, draft, body), http.StatusOK, "send")
	again := transferAnswer(t, send(c, draft, body), http.StatusOK, "the send replayed at its old revision")
	if again.Revision != sent.Revision || len(again.Actions) != 1 {
		t.Errorf("replay = %+v, want the transfer as it is", again)
	}
	other := gen.InventoryTransferSendInput{Lines: sendLines(draft, s.shelf2.Id)}
	expectRefusal(t, send(c, draft, other), http.StatusConflict, "transfer_not_draft", "another send of a sent transfer")

	receiveBody := gen.InventoryTransferReceiveInput{ClientRequestId: uuid.New(), Lines: []gen.InventoryTransferReceiveLine{arrived(sent.Lines[0].Id, 6, s.bgoShelf.Id)}}
	got := transferAnswer(t, c.Do(http.MethodPost, transferPath(sent.Id)+"/receive", receiveBody), http.StatusOK, "receive")
	if got.Status != gen.InventoryTransferStatusReceived {
		t.Fatalf("status = %s", got.Status)
	}
	transferAnswer(t, c.Do(http.MethodPost, transferPath(sent.Id)+"/receive", receiveBody), http.StatusOK, "the receive replayed (§9, case 4)")
	receiveBody.Lines[0].Quantity = qty(5)
	expectRefusal(t, c.Do(http.MethodPost, transferPath(sent.Id)+"/receive", receiveBody), http.StatusConflict, "client_request_reused", "the id with another body")

	// An id another transfer's action holds is reused, never a replay.
	second := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 1)), s.shelf1.Id)
	receiveBody.Lines = []gen.InventoryTransferReceiveLine{arrived(second.Lines[0].Id, 1, s.bgoShelf.Id)}
	expectRefusal(t, c.Do(http.MethodPost, transferPath(second.Id)+"/receive", receiveBody), http.StatusConflict, "client_request_reused", "another transfer's id")

	if n := h.Count(t, `SELECT count(*) FROM inventory.movements WHERE kind IN ('transfer_out', 'transfer_in')`); n != 3 {
		t.Errorf("%d transfer movements, want 3: replays post nothing", n)
	}
	if got := onHand(t, h, varBolt, s.bgoShelf.Id); got != "6.000" {
		t.Errorf("bolts at BGO-B1 = %s, want 6", got)
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_CloseTheDifference is I42: a receive with close records every
// line's remaining outstanding missing, posted nowhere; a close-only receive
// posts no movement and replays by its action; a close with nothing left to
// close is nothing_to_close; the differences read back by the list's
// withMissing.
func TestTransfer_CloseTheDifference(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	tr := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 10), line(varNut, 3)), s.shelf1.Id, s.shelf2.Id)
	note := "Two boxes crushed"
	closing := &gen.InventoryTransferClose{Reason: gen.InventoryTransferCloseReasonDamaged, Note: &note}
	tr = transferAnswer(t, receiveTransfer(c, tr.Id, closing, arrived(tr.Lines[0].Id, 6, s.bgoShelf.Id)), http.StatusOK, "receive and close")
	if tr.Status != gen.InventoryTransferStatusReceived || tr.InTransit != 0 || tr.Lines[0].Missing != 4 || tr.Lines[1].Missing != 3 ||
		tr.CloseReason == nil || *tr.CloseReason != gen.InventoryTransferCloseReasonDamaged || *tr.CloseNote != note || tr.ClosedAt == nil {
		t.Errorf("closed = %+v", tr)
	}
	if got, want := actionKinds(tr)[3:], []string{"receive", "missing:4@-", "received:6@BGO-B1", "missing:3@-"}; !slices.Equal(got, want) {
		t.Errorf("the closing receive = %v, want %v", got, want)
	}
	if got := onHand(t, h, varBolt, s.bgoShelf.Id); got != "6.000" {
		t.Errorf("bolts at BGO-B1 = %s, want 6: missing stock is posted nowhere", got)
	}

	// Close-only, and its replay.
	only := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 2)), s.shelf1.Id)
	body := gen.InventoryTransferReceiveInput{ClientRequestId: uuid.New(), Lines: []gen.InventoryTransferReceiveLine{}, Close: &gen.InventoryTransferClose{Reason: gen.InventoryTransferCloseReasonLost}}
	only = transferAnswer(t, c.Do(http.MethodPost, transferPath(only.Id)+"/receive", body), http.StatusOK, "close only")
	if only.Status != gen.InventoryTransferStatusReceived || only.Lines[0].Missing != 2 || only.Actions[1].MovementId != nil {
		t.Errorf("closed only = %+v", only)
	}
	transferAnswer(t, c.Do(http.MethodPost, transferPath(only.Id)+"/receive", body), http.StatusOK, "the close-only receive replayed")

	// Receiving everything and closing leaves nothing to close.
	all := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 1)), s.shelf1.Id)
	expectRefusal(t, receiveTransfer(c, all.Id, closing, arrived(all.Lines[0].Id, 1, s.bgoShelf.Id)), http.StatusConflict, "nothing_to_close", "a close of nothing")
	expectRefusal(t, receiveTransfer(c, all.Id, nil, arrived(all.Lines[0].Id, 0, s.bgoShelf.Id)), http.StatusBadRequest, "nothing_to_post", "a receive of nothing")
	mustStatus(t, receiveTransfer(c, all.Id, &gen.InventoryTransferClose{Reason: "stolen"}), http.StatusBadRequest, "an unknown reason")

	// The differences, on the variant page's read.
	var page gen.InventoryTransferPage
	res := c.Do(http.MethodGet, transfersPath+"?withMissing=true&variantId="+strconv.Itoa(int(varNut)), nil)
	mustStatus(t, res, http.StatusOK, "the nuts' differences")
	res.JSON(&page)
	if len(page.Transfers) != 1 || page.Transfers[0].Id != tr.Id || page.Transfers[0].Missing != 7 {
		t.Errorf("differences = %+v, want the first transfer with 7 missing", page.Transfers)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.events WHERE kind = 'transfer_closed'`); n != 2 {
		t.Errorf("%d closed events, want 2", n)
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_Cancel is I44: a sent transfer comes back whole, each line to
// the location it left, as a transfer_in at the from site; returned on every
// line, nothing in transit; only a sent transfer with nothing received.
func TestTransfer_Cancel(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	draft := createTransfer(t, c, s, line(varBolt, 5), line(varNut, 2))
	expectRefusal(t, cancelTransfer(c, draft), http.StatusConflict, "transfer_not_cancellable", "a draft's cancel")
	tr := mustSend(t, c, draft, s.shelf1.Id, s.shelf2.Id)
	expectRefusal(t, cancelTransfer(c, draft), http.StatusConflict, "revision_conflict", "a stale cancel")
	body := gen.InventoryTransferCancelInput{ClientRequestId: uuid.New()}
	cancelled := transferAnswer(t, c.Do(http.MethodPost, transferPath(tr.Id)+"/cancel", body, ifMatch(tr.Revision)), http.StatusOK, "cancel")
	if cancelled.Status != gen.InventoryTransferStatusCancelled || cancelled.InTransit != 0 || cancelled.Lines[0].Returned != 5 || cancelled.ClosedAt == nil {
		t.Errorf("cancelled = %+v", cancelled)
	}
	if got, want := actionKinds(cancelled)[3:], []string{"cancel", "returned:5@OSL-1", "returned:2@OSL-2"}; !slices.Equal(got, want) {
		t.Errorf("the cancel = %v, want %v", got, want)
	}
	back := getMovement(t, c, *cancelled.Actions[1].MovementId)
	if back.Kind != gen.TransferIn || back.Site.Code != "OSL" || back.Lines[0].To.Id != s.shelf1.Id || back.Transfer == nil {
		t.Errorf("the way home = %+v", back)
	}
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "20.000" {
		t.Errorf("bolts at OSL-1 = %s, want 20 again", got)
	}
	transferAnswer(t, c.Do(http.MethodPost, transferPath(tr.Id)+"/cancel", body, ifMatch(tr.Revision)), http.StatusOK, "the cancel replayed")
	if got := inTransitTotal(t, h); got != "0.000" {
		t.Errorf("in transit = %s, want 0", got)
	}

	// Once anything is received, it is not cancellable.
	part := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 4)), s.shelf1.Id)
	part = transferAnswer(t, receiveTransfer(c, part.Id, nil, arrived(part.Lines[0].Id, 1, s.bgoShelf.Id)), http.StatusOK, "receive one")
	expectRefusal(t, cancelTransfer(c, part), http.StatusConflict, "transfer_not_cancellable", "a cancel after a receipt")
	assertLedgerConsistent(t, h)
}

// TestTransfer_SendAndReceive is I43: one request sends and receives
// everything, two movements at two sites, two actions with derived ids,
// received at once; its replay; and a send-and-receive after a plain send of
// the same transfer is transfer_not_draft, never client_request_reused.
func TestTransfer_SendAndReceive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	draft := createTransfer(t, c, s, line(varBolt, 3), line(varNut, 1))
	body := gen.InventoryTransferSendInput{
		Lines: sendLines(draft, s.shelf1.Id, s.shelf2.Id),
		Receive: &gen.InventoryTransferSendReceive{Lines: []gen.InventoryTransferDestination{
			{LineId: draft.Lines[0].Id, ToLocationId: s.bgoShelf.Id}, {LineId: draft.Lines[1].Id, ToLocationId: s.bgoShelf.Id},
		}},
	}
	incomplete := body
	incomplete.Receive = &gen.InventoryTransferSendReceive{Lines: body.Receive.Lines[:1]}
	expectRefusal(t, send(c, draft, incomplete), http.StatusBadRequest, "receive_incomplete", "a receive missing a line")
	tr := transferAnswer(t, send(c, draft, body), http.StatusOK, "send and receive")
	if tr.Status != gen.InventoryTransferStatusReceived || tr.InTransit != 0 || len(tr.Actions) != 2 || tr.Lines[0].Received != 3 || tr.ClosedAt == nil {
		t.Errorf("sent and received = %+v", tr)
	}
	out, in := getMovement(t, c, *tr.Actions[0].MovementId), getMovement(t, c, *tr.Actions[1].MovementId)
	if out.Kind != gen.TransferOut || out.Site.Code != "OSL" || in.Kind != gen.TransferIn || in.Site.Code != "BGO" || out.ClientRequestId == in.ClientRequestId {
		t.Errorf("movements = %+v / %+v", out, in)
	}
	transferAnswer(t, send(c, draft, body), http.StatusOK, "the send and receive replayed")
	if got := onHand(t, h, varBolt, s.bgoShelf.Id); got != "3.000" {
		t.Errorf("bolts at BGO-B1 = %s, want 3", got)
	}

	plain := createTransfer(t, c, s, line(varBolt, 1))
	mustSend(t, c, plain, s.shelf1.Id)
	both := gen.InventoryTransferSendInput{Lines: sendLines(plain, s.shelf1.Id),
		Receive: &gen.InventoryTransferSendReceive{Lines: []gen.InventoryTransferDestination{{LineId: plain.Lines[0].Id, ToLocationId: s.bgoShelf.Id}}}}
	expectRefusal(t, send(c, plain, both), http.StatusConflict, "transfer_not_draft", "a send and receive after a plain send")
	assertLedgerConsistent(t, h)
}

// TestTransfer_SendRefusals: a line without a source (from_location_required),
// a line not on the transfer, a source in the other site, more than lies
// there, a variant gone from the catalog (§9, case 6) — and nothing posted.
func TestTransfer_SendRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	tr := createTransfer(t, c, s, line(varBolt, 25), line(varNut, 1), line(varWasher, 1))
	res := send(c, tr, gen.InventoryTransferSendInput{Lines: []gen.InventoryTransferSendLine{
		{LineId: tr.Lines[0].Id, FromLocationId: &s.shelf1.Id}, {LineId: tr.Lines[1].Id},
		{LineId: tr.Lines[2].Id, FromLocationId: &s.bgoShelf.Id},
	}})
	if got := lineCodes(expectRefusal(t, res, http.StatusBadRequest, "from_location_required", "a send without a source")); !slices.Equal(got, []string{"1:from_location_required", "2:move_crosses_sites"}) {
		t.Errorf("lines = %v", got)
	}
	res = send(c, tr, gen.InventoryTransferSendInput{Lines: append(sendLines(tr, s.shelf1.Id, s.shelf2.Id, s.shelf2.Id), gen.InventoryTransferSendLine{LineId: 999999, FromLocationId: &s.shelf1.Id})})
	expectRefusal(t, res, http.StatusBadRequest, "transfer_line_unknown", "a line not on the transfer")
	res = send(c, tr, gen.InventoryTransferSendInput{Lines: sendLines(tr, s.shelf1.Id, s.shelf2.Id, s.shelf2.Id)})
	if got := lineCodes(expectRefusal(t, res, http.StatusConflict, "insufficient_stock", "more than lies there")); !slices.Equal(got, []string{"0:insufficient_stock", "2:insufficient_stock"}) {
		t.Errorf("lines = %v", got)
	}
	mustStatus(t, c.Do(http.MethodPost, transferPath(tr.Id)+"/send", gen.InventoryTransferSendInput{Lines: sendLines(tr, s.shelf1.Id, s.shelf2.Id, s.shelf2.Id)}), http.StatusBadRequest, "a send without If-Match")

	// §9, case 6: the variant deleted in a draft.
	gone := createTransfer(t, c, s, line(varNut, 1), line(varBolt, 1))
	h.catalog.remove(varNut)
	gone = getTransfer(t, c, gone.Id)
	if v := gone.Lines[0].Variant; v.Deleted == nil || !*v.Deleted || v.Sku != "NUT-M8" {
		t.Errorf("the gone line's variant = %+v, want it by its snapshot, deleted", v)
	}
	res = send(c, gone, gen.InventoryTransferSendInput{Lines: sendLines(gone, s.shelf2.Id, s.shelf1.Id)})
	if got := lineCodes(expectRefusal(t, res, http.StatusBadRequest, "variant_not_found", "a send of a variant gone")); !slices.Equal(got, []string{"0:variant_not_found"}) {
		t.Errorf("lines = %v", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.movements WHERE kind = 'transfer_out'`); n != 0 {
		t.Errorf("%d transfer_out movements, want none", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.transfers WHERE status <> 'draft'`); n != 0 {
		t.Errorf("%d sent transfers, want none", n)
	}
}

// TestTransfer_ReceiveRefusals: more than outstanding (transfer_over_received),
// in units after the packagings are resolved; a line not on the transfer; a
// destination in the from site; a draft is not receivable; a viewer cannot
// receive.
func TestTransfer_ReceiveRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	h.catalog.setPackagings(varBolt, boxAndPallet()...)
	draft := createTransfer(t, c, s, line(varBolt, 16))
	expectRefusal(t, receiveTransfer(c, draft.Id, nil, arrived(draft.Lines[0].Id, 1, s.bgoShelf.Id)), http.StatusConflict, "transfer_not_receivable", "a draft's receive")
	tr := mustSend(t, c, draft, s.shelf1.Id)
	res := receiveTransfer(c, tr.Id, nil, gen.InventoryTransferReceiveLine{LineId: tr.Lines[0].Id, Entered: entered(qty(2), pkgBox, 1), ToLocationId: s.bgoShelf.Id})
	if got := lineCodes(expectRefusal(t, res, http.StatusBadRequest, "transfer_over_received", "a box and two of 16")); !slices.Equal(got, []string{"0:transfer_over_received"}) {
		t.Errorf("lines = %v", got)
	}
	res = receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 10, s.bgoShelf.Id), arrived(tr.Lines[0].Id, 1, s.bgoShelf.Id))
	mustStatus(t, res, http.StatusBadRequest, "a line named twice")
	expectRefusal(t, receiveTransfer(c, tr.Id, nil, arrived(999999, 1, s.bgoShelf.Id)), http.StatusBadRequest, "transfer_line_unknown", "a line not on the transfer")
	expectRefusal(t, receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 1, s.shelf1.Id)), http.StatusBadRequest, "move_crosses_sites", "a destination at the from site")
	expectRefusal(t, receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, -1, s.bgoShelf.Id)), http.StatusBadRequest, "quantity_invalid", "a negative quantity")
	viewer, _ := signIn(t, h, stockViewer...)
	mustStatus(t, receiveTransfer(viewer, tr.Id, nil, arrived(tr.Lines[0].Id, 1, s.bgoShelf.Id)), http.StatusForbidden, "a viewer's receive")
	// Exactly the outstanding, in packagings, lands.
	res = receiveTransfer(c, tr.Id, nil, gen.InventoryTransferReceiveLine{LineId: tr.Lines[0].Id, Entered: entered(qty(1), pkgBox, 1), ToLocationId: s.bgoShelf.Id})
	if got := transferAnswer(t, res, http.StatusOK, "a box and one"); got.Status != gen.InventoryTransferStatusReceived {
		t.Errorf("status = %s, want received", got.Status)
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_PackagingChangedInTransit is §9, case 5: a line's units were
// snapshotted at send; the receiver's packagings resolve as they are that
// day.
func TestTransfer_PackagingChangedInTransit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	h.catalog.setPackagings(varBolt, boxAndPallet()...)
	tr := createTransfer(t, c, s, gen.InventoryTransferLineInput{VariantId: varBolt, Entered: entered(nil, pkgBox, 1)})
	tr = mustSend(t, c, tr, s.shelf1.Id)
	if tr.Lines[0].Sent != 15 {
		t.Fatalf("sent = %v, want 15", tr.Lines[0].Sent)
	}
	box := boxAndPallet()
	box[0].Quantity = "10.000"
	h.catalog.setPackagings(varBolt, box...)
	if got := getTransfer(t, c, tr.Id); got.Lines[0].Sent != 15 || got.Lines[0].InTransit != 15 {
		t.Errorf("after the change = %+v, want 15 still in transit", got.Lines[0])
	}
	res := receiveTransfer(c, tr.Id, nil, gen.InventoryTransferReceiveLine{LineId: tr.Lines[0].Id, Entered: entered(nil, pkgBox, 1), ToLocationId: s.bgoShelf.Id})
	tr = transferAnswer(t, res, http.StatusOK, "a box as it is today")
	if tr.Lines[0].Received != 10 || tr.Lines[0].InTransit != 5 || tr.Status != gen.InventoryTransferStatusPartlyReceived {
		t.Errorf("after the receive = %+v, want 10 received and 5 in transit", tr.Lines[0])
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_ArchivedDestinationWithADraft is §9, case 7's first half: a
// site with a draft to it may be archived; the draft's send is then
// site_archived.
func TestTransfer_ArchivedDestinationWithADraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	tr := createTransfer(t, c, s, line(varBolt, 1))
	mustStatus(t, c.Do(http.MethodPut, sitePath(s.bgo.Id), gen.InventorySiteUpdate{Code: s.bgo.Code, Name: s.bgo.Name, Kind: s.bgo.Kind, Status: gen.Archived}, ifMatch(s.bgo.Revision)),
		http.StatusOK, "archive BGO with a draft to it")
	expectRefusal(t, send(c, tr, gen.InventoryTransferSendInput{Lines: sendLines(tr, s.shelf1.Id)}), http.StatusBadRequest, "site_archived", "the draft's send")
	expectRefusal(t, c.Do(http.MethodPut, transferPath(tr.Id), draftInput(s.osl.Id, s.bgo.Id, line(varBolt, 2)), ifMatch(tr.Revision)), http.StatusBadRequest, "site_archived", "an edit naming it")
	// Nor may a site a transfer names be deleted.
	expectRefusal(t, c.Do(http.MethodDelete, sitePath(s.bgo.Id), nil), http.StatusConflict, "site_has_transfers", "delete BGO")
}

// TestTransfer_EditAfterSend: a sent transfer is no longer a draft, so it is
// neither edited nor deleted (I37).
func TestTransfer_EditAfterSend(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	draft := createTransfer(t, c, s, line(varBolt, 1))
	tr := mustSend(t, c, draft, s.shelf1.Id)
	expectRefusal(t, c.Do(http.MethodPut, transferPath(tr.Id), draftInput(s.osl.Id, s.bgo.Id, line(varBolt, 2)), ifMatch(tr.Revision)), http.StatusConflict, "transfer_not_draft", "edit a sent transfer")
	expectRefusal(t, c.Do(http.MethodDelete, transferPath(tr.Id), nil, ifMatch(tr.Revision)), http.StatusConflict, "transfer_not_draft", "delete a sent transfer")
	expectRefusal(t, c.Do(http.MethodPut, transferPath(tr.Id), draftInput(s.osl.Id, s.bgo.Id, line(varBolt, 2)), ifMatch(draft.Revision)), http.StatusConflict, "transfer_not_draft", "edit at the draft's revision")
}

// TestTransfer_CountFlagsMoved is I49: a count over a location a transfer
// sent from, and one over a location it received into, flag their lines
// moved at post, as any movement does.
func TestTransfer_CountFlagsMoved(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	receive(t, c, s.bgo.Id, s.bgoShelf.Id, receiptLine(varBolt, 1))
	from := mustOpenCount(t, c, s.osl.Id, s.shelf1.Id)
	into := mustOpenCount(t, c, s.bgo.Id, s.bgoShelf.Id)
	tr := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 4)), s.shelf1.Id)
	transferAnswer(t, receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 4, s.bgoShelf.Id)), http.StatusOK, "receive into the counted shelf")
	countAnswer(t, putCounted(c, from.Id, counted(varBolt, s.shelf1.Id, 20)), http.StatusOK, "count the source")
	countAnswer(t, putCounted(c, into.Id, counted(varBolt, s.bgoShelf.Id, 1)), http.StatusOK, "count the destination")
	for _, id := range []int64{from.Id, into.Id} {
		p := expectRefusal(t, postCount(c, id, gen.Keep, false), http.StatusConflict, "count_lines_moved", "post a count the transfer moved")
		if got := lineCodes(p); !slices.Equal(got, []string{"0:count_lines_moved"}) {
			t.Errorf("count %d lines = %v", id, got)
		}
	}
}

// TestTransfer_List is the list's filters: status (open by default in the
// app, all when absent), the sites, a variant on a line, q by number, by
// TRF-<number> and by a line's SKU, and its cursor.
func TestTransfer_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	first := createTransfer(t, c, s, line(varBolt, 1))
	second := mustSend(t, c, createTransfer(t, c, s, line(varNut, 1)), s.shelf2.Id)
	back := transferAnswer(t, c.Do(http.MethodPost, transfersPath, draftInput(s.bgo.Id, s.osl.Id, line(varWasher, 1))), http.StatusCreated, "BGO to OSL")
	done := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 1)), s.shelf1.Id)
	transferAnswer(t, receiveTransfer(c, done.Id, nil, arrived(done.Lines[0].Id, 1, s.bgoShelf.Id)), http.StatusOK, "receive")
	list := func(query string) []int64 {
		t.Helper()
		var page gen.InventoryTransferPage
		res := c.Do(http.MethodGet, transfersPath+query, nil)
		mustStatus(t, res, http.StatusOK, "GET /transfers"+query)
		res.JSON(&page)
		var out []int64
		for _, tr := range page.Transfers {
			out = append(out, tr.Id)
		}
		return out
	}
	cases := map[string][]int64{
		"":                                  {done.Id, back.Id, second.Id, first.Id},
		"?status=open":                      {back.Id, second.Id, first.Id},
		"?status=sent":                      {second.Id},
		"?status=received":                  {done.Id},
		"?fromSiteId=" + itoa(s.bgo.Id):     {back.Id},
		"?toSiteId=" + itoa(s.bgo.Id):       {done.Id, second.Id, first.Id},
		"?variantId=" + itoa(int64(varNut)): {second.Id},
		"?q=" + itoa(second.Number):         {second.Id},
		"?q=trf-" + itoa(first.Number):      {first.Id},
		"?q=WASH-8":                         {back.Id},
		"?q=wash-8":                         {back.Id},
	}
	for query, want := range cases {
		if got := list(query); !slices.Equal(got, want) {
			t.Errorf("GET /transfers%s = %v, want %v", query, got, want)
		}
	}
	mustStatus(t, c.Do(http.MethodGet, transfersPath+"?status=lost", nil), http.StatusBadRequest, "an unknown status")
	mustStatus(t, c.Do(http.MethodGet, transfersPath+"?cursor=x", nil), http.StatusBadRequest, "a bad cursor")
	for range 50 {
		createTransfer(t, c, s, line(varBolt, 1))
	}
	var page gen.InventoryTransferPage
	res := c.Do(http.MethodGet, transfersPath, nil)
	res.JSON(&page)
	if len(page.Transfers) != 50 || page.Next == nil {
		t.Fatalf("first page = %d transfers, next %v; want 50 and a next", len(page.Transfers), page.Next)
	}
	var rest gen.InventoryTransferPage
	res = c.Do(http.MethodGet, transfersPath+"?cursor="+*page.Next, nil)
	res.JSON(&rest)
	if len(rest.Transfers) != 4 || rest.Next != nil || rest.Transfers[3].Id != first.Id {
		t.Errorf("second page = %d transfers, next %v; want the 4 oldest", len(rest.Transfers), rest.Next)
	}
}

// itoa is n as decimal text.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
