package purchasing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// The invoice match (delivery D, P36–P41): Accounting's supplier invoices
// are the fake here; internal/integration matches against the real module.

var (
	matcher   = []string{"purchasing:access", "purchasing:invoices-match"}
	bookkeep  = []string{"purchasing:access", "purchasing:invoices-match", "accounting:post", "accounting:read"}
	everyKey  = append(append([]string{}, receiver...), "purchasing:invoices-match", "purchasing:supplier-items-manage", "accounting:post", "accounting:read")
	bookLines = []string{"purchasing:access", "accounting:read"}
)

func invoicePath(id int64, rest string) string {
	return fmt.Sprintf("/api/v1/purchasing/supplier-invoices/%d/%s", id, rest)
}

// receivedOrder is a sent order of 3 boxes of 10 bolts at 50.00 a box and 5
// nuts at 2.00, all received: 150.00 and 10.00.
func receivedOrder(t *testing.T, c *modtest.Client) gen.PurchasingOrder {
	t.Helper()
	bolts := variantLine(varBolt, "3")
	bolts.PackagingId, bolts.UnitPrice = i32(packBox), str("50")
	nuts := variantLine(varNut, "5")
	nuts.UnitPrice = str("2")
	o := sentOrder(t, c, bolts, nuts)
	receive(t, c, o.Id, receiptInput(rline(o.Lines[0].Id, "30", locShelfA), rline(o.Lines[1].Id, "5", locShelfA)))
	return getOrder(t, c, o.Id)
}

func panelOf(t *testing.T, res *modtest.Response, what string) gen.PurchasingInvoicePanel {
	t.Helper()
	mustStatus(t, res, http.StatusOK, what)
	var p gen.PurchasingInvoicePanel
	res.JSON(&p)
	return p
}

func matchIn(order int64, lines ...gen.PurchasingInvoiceMatchLineInput) gen.PurchasingInvoiceMatchInput {
	return gen.PurchasingInvoiceMatchInput{OrderId: order, Lines: lines}
}

func mline(pos int32, line int64, qty string) gen.PurchasingInvoiceMatchLineInput {
	return gen.PurchasingInvoiceMatchLineInput{LinePosition: pos, OrderLineId: line, Quantity: qty}
}

func lineInvoiced(t *testing.T, c *modtest.Client, order, line int64) string {
	t.Helper()
	for _, l := range getOrder(t, c, order).Lines {
		if l.Id == line {
			return l.Invoiced
		}
	}
	t.Fatalf("no line %d", line)
	return ""
}

// An invoice whose reference names an order number of its supplier, as a
// whole digit run, suggests that order; another supplier's order of that
// number, a longer run and a draft are not suggested.
func TestMatch_SuggestionByReference(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	createOrder(t, c, supNorsk, variantLine(varNut, "1")) // a draft, no number
	num := *o.Number
	inv := h.books.enter(supNorsk, "invoice", "F-1", "NOK", fmt.Sprintf("Deres ordre %d / prosjekt %d9", num, num), fakeLine{"Bolter", "150.00"})
	p := panelOf(t, c.Do(http.MethodGet, invoicePath(inv, "match"), nil), "the panel")
	if len(p.Suggestions) != 1 || p.Suggestions[0].OrderId != o.Id || p.Suggestions[0].ToInvoice != 2 || p.SupplierName != "Norsk Stål AS" {
		t.Fatalf("suggestions = %+v", p)
	}
	if !p.LinesVisible || len(p.Lines) != 1 || p.Lines[0].Text == nil || *p.Lines[0].Net != "150.00" || p.Reference == nil {
		t.Fatalf("lines for accounting:read = %+v", p)
	}
	other := h.books.enter(supGerman, "invoice", "F-1", "EUR", fmt.Sprint(num), fakeLine{"x", "1.00"})
	if p := panelOf(t, c.Do(http.MethodGet, invoicePath(other, "match"), nil), "another supplier"); len(p.Suggestions) != 0 {
		t.Fatalf("another supplier's suggestions = %+v", p.Suggestions)
	}
	// Without accounting:read: the positions, no text, no amounts, no reference.
	r := h.SignIn(t, matcher...)
	p = panelOf(t, r.Do(http.MethodGet, invoicePath(inv, "match"), nil), "the panel without accounting:read")
	if p.LinesVisible || len(p.Lines) != 1 || p.Lines[0].Text != nil || p.Lines[0].Net != nil || p.Reference != nil {
		t.Fatalf("lines without accounting:read = %+v", p)
	}
	expectRefusal(t, c.Do(http.MethodGet, invoicePath(99999, "match"), nil), http.StatusNotFound, "not_found", "an unknown invoice")
}

// Allocate, Match all received and unmatch move the lines' invoiced
// quantities; the order's invoices list them, with the amounts only for
// accounting:read.
func TestMatch_AllocateMatchAllUnmatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	bolt, nut := o.Lines[0].Id, o.Lines[1].Id
	inv := h.books.enter(supNorsk, "invoice", "F-2", "NOK", "", fakeLine{"Bolter", "100.00"}, fakeLine{"Mutre", "10.00"}, fakeLine{"Frakt", "99.00"})

	p := panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, bolt, "20"))), "allocate")
	if len(p.Matches) != 1 || p.Matches[0].Quantity != "20" || p.Matches[0].State != "provisional" || p.Differences != 0 ||
		p.Matches[0].OrderUnitNet != "5.00" || *p.Matches[0].InvoiceUnitNet != "5.00" {
		t.Fatalf("after allocate = %+v", p)
	}
	if got := lineInvoiced(t, c, o.Id, bolt); got != "20" {
		t.Fatalf("bolt invoiced = %s, want 20", got)
	}
	// The same line again takes the new quantity.
	panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, bolt, "10"))), "re-allocate")
	if got := lineInvoiced(t, c, o.Id, bolt); got != "10" {
		t.Fatalf("bolt invoiced after the new quantity = %s, want 10", got)
	}
	// Match all received pairs the free lines 2 and 3 with what is left:
	// the bolts' 20, then the nuts' 5.
	p = panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "match-all"), gen.PurchasingInvoiceMatchAllInput{OrderId: o.Id}), "match all")
	if len(p.Matches) != 3 || p.Matches[1].OrderLineId != bolt || p.Matches[1].Quantity != "20" || p.Matches[2].OrderLineId != nut || p.Matches[2].Quantity != "5" {
		t.Fatalf("after match all = %+v", p.Matches)
	}
	// Line 1 now bills 10 bolts for 100.00, line 2 twenty for 10.00 and
	// line 3 five nuts for 99.00: every price differs.
	if p.Differences != 3 {
		t.Fatalf("differences = %d, want 3", p.Differences)
	}
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(inv, "match-all"), gen.PurchasingInvoiceMatchAllInput{OrderId: o.Id}),
		http.StatusConflict, "nothing_to_match", "match all again")

	r := h.SignIn(t, matcher...)
	res := r.Do(http.MethodGet, orderPath(o.Id, "invoices"), nil)
	mustStatus(t, res, http.StatusOK, "the order's invoices")
	var list gen.PurchasingInvoiceOrderMatches
	res.JSON(&list)
	if list.LinesVisible || len(list.Matches) != 3 || list.Matches[0].LineNet != nil || list.Matches[0].InvoiceUnitNet != nil {
		t.Fatalf("the order's invoices without accounting:read = %+v", list)
	}
	res = c.Do(http.MethodGet, orderPath(o.Id, "invoices"), nil)
	res.JSON(&list)
	if !list.LinesVisible || list.Matches[0].LineNet == nil || *list.Matches[0].LineNet != "100.00" {
		t.Fatalf("the order's invoices with accounting:read = %+v", list)
	}

	// Unmatch one line, then the rest of the order.
	p = panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "unmatch"), gen.PurchasingInvoiceUnmatchInput{LinePosition: i32(3)}), "unmatch a line")
	if len(p.Matches) != 2 || lineInvoiced(t, c, o.Id, nut) != "0" {
		t.Fatalf("after unmatching line 3 = %+v", p.Matches)
	}
	p = panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "unmatch"), gen.PurchasingInvoiceUnmatchInput{OrderId: i64(o.Id)}), "unmatch the order")
	if len(p.Matches) != 0 || lineInvoiced(t, c, o.Id, bolt) != "0" {
		t.Fatalf("after unmatching the order = %+v", p.Matches)
	}
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(inv, "unmatch"), gen.PurchasingInvoiceUnmatchInput{}), http.StatusConflict, "nothing_to_match", "unmatch nothing")
}

// The refusals: beyond what is received (unless accepted, then warned),
// an invoice line matched to another order line, a position the invoice
// does not have, a credit note beyond what is invoiced, another supplier,
// another currency, a reversed invoice, a draft order.
func TestMatch_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	bolt, nut := o.Lines[0].Id, o.Lines[1].Id
	inv := h.books.enter(supNorsk, "invoice", "F-3", "NOK", "", fakeLine{"Bolter", "200.00"}, fakeLine{"Mutre", "10.00"})

	p := expectRefusal(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, bolt, "40"))),
		http.StatusConflict, "over_invoiced", "more than received")
	if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].Index != 0 {
		t.Fatalf("over_invoiced names = %+v", p.Lines)
	}
	accepted := matchIn(o.Id, mline(1, bolt, "40"))
	accepted.AcceptOver = ptrTo(true)
	got := panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), accepted), "accepted over")
	w := got.Matches[0].Warnings
	if len(w) != 2 || w[0] != gen.InvoicedOverReceived || w[1] != gen.InvoicedOverOrdered {
		t.Fatalf("warnings beyond received and ordered = %v", w)
	}
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, nut, "1"))),
		http.StatusConflict, "invoice_line_matched", "a matched invoice line to another order line")
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(9, nut, "1"))),
		http.StatusBadRequest, "invoice_line_unknown", "a position the invoice does not have")
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(2, nut, "0"))),
		http.StatusBadRequest, "quantity_invalid", "a zero quantity")
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(2, 424242, "1"))),
		http.StatusBadRequest, "line_not_found", "another order's line")

	// A credit note lowers what is invoiced, never below nothing.
	credit := h.books.enter(supNorsk, "credit_note", "K-1", "NOK", "", fakeLine{"Retur", "50.00"})
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(credit, "matches"), matchIn(o.Id, mline(1, bolt, "41"))),
		http.StatusConflict, "credit_exceeds_invoiced", "a credit beyond what is invoiced")
	acceptedCredit := matchIn(o.Id, mline(1, bolt, "41"))
	acceptedCredit.AcceptOver = ptrTo(true)
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(credit, "matches"), acceptedCredit),
		http.StatusConflict, "credit_exceeds_invoiced", "acceptOver does not let a credit through")
	panelOf(t, c.Do(http.MethodPost, invoicePath(credit, "matches"), matchIn(o.Id, mline(1, bolt, "10"))), "a credit note")
	if got := lineInvoiced(t, c, o.Id, bolt); got != "30" {
		t.Fatalf("bolt invoiced after the credit = %s, want 30", got)
	}

	german := h.books.enter(supGerman, "invoice", "R-1", "EUR", "", fakeLine{"x", "1.00"})
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(german, "matches"), matchIn(o.Id, mline(1, nut, "1"))),
		http.StatusConflict, "invoice_supplier_differs", "another supplier's invoice")
	euro := h.books.enter(supNorsk, "invoice", "F-4", "EUR", "", fakeLine{"x", "1.00"})
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(euro, "matches"), matchIn(o.Id, mline(1, nut, "1"))),
		http.StatusConflict, "invoice_currency_differs", "another currency")
	rev := h.books.enter(supNorsk, "invoice", "F-5", "NOK", "", fakeLine{"x", "1.00"})
	h.books.edit(rev, func(i *contracts.SupplierInvoice) { i.State = "reversed" })
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(rev, "matches"), matchIn(o.Id, mline(1, nut, "1"))),
		http.StatusConflict, "invoice_reversed", "a reversed invoice")
	draft := createOrder(t, c, supNorsk, variantLine(varNut, "1"))
	fresh := h.books.enter(supNorsk, "invoice", "F-6", "NOK", "", fakeLine{"x", "1.00"})
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(fresh, "matches"), matchIn(draft.Id, mline(1, draft.Lines[0].Id, "1"))),
		http.StatusConflict, "order_not_sent", "a draft order")
}

// Register invoice makes Accounting's draft with what is received and not
// invoiced, the order number as its reference, and the provisional matches;
// a replay answers the first draft, another body is refused; the second
// registration takes only what the first left.
func TestRegisterInvoice(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	due := openapi_types.Date{Time: day(2026, 12, 10)}
	in := gen.PurchasingInvoiceRegisterInput{ClientRequestId: uuid.New(), InvoiceNumber: " 55120 ",
		InvoiceDate: openapi_types.Date{Time: day(2026, 11, 10)}, DueDate: &due, Total: str("200.00")}
	res := c.Do(http.MethodPost, orderPath(o.Id, "register-invoice"), in)
	mustStatus(t, res, http.StatusCreated, "register")
	var reg gen.PurchasingInvoiceRegistration
	res.JSON(&reg)
	if reg.Lines != 2 || reg.LinesLeft != 0 || reg.Total != "160.00" || reg.Currency != "NOK" {
		t.Fatalf("registration = %+v", reg)
	}
	d := h.books.lastDraft()
	if d.SupplierID != supNorsk || d.Number != "55120" || d.Kind != "invoice" || d.Reference != fmt.Sprint(*o.Number) ||
		d.Currency != "NOK" || d.Total != "200.00" || d.DueDate == nil || !d.DueDate.Equal(due.Time) || len(d.Lines) != 2 ||
		d.Lines[0].Amount != "150.00" || !strings.HasPrefix(d.Lines[0].Text, "Bolt M8") || d.Lines[0].AccountID != nil || d.By == uuid.Nil {
		t.Fatalf("draft = %+v", d)
	}
	if got := lineInvoiced(t, c, o.Id, o.Lines[0].Id); got != "30" {
		t.Fatalf("invoiced after registering = %s", got)
	}
	// The replay answers the same draft and makes none.
	res = c.Do(http.MethodPost, orderPath(o.Id, "register-invoice"), in)
	mustStatus(t, res, http.StatusCreated, "replay")
	var again gen.PurchasingInvoiceRegistration
	res.JSON(&again)
	if again != reg || h.books.draftCount() != 1 {
		t.Fatalf("replay = %+v, drafts %d", again, h.books.draftCount())
	}
	reused := in
	reused.InvoiceNumber = "other"
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "register-invoice"), reused), http.StatusConflict, "client_request_reused", "another body")
	next := in
	next.ClientRequestId, next.InvoiceNumber = uuid.New(), "55121"
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "register-invoice"), next), http.StatusConflict, "nothing_to_invoice", "all invoiced")

	// Accounting's refusals, as this module's.
	h.books.refuse = &contracts.SupplierInvoiceRefusal{Code: contracts.SupplierInvoiceAccountMissing, Detail: "no account"}
	o2 := receivedOrder(t, c)
	next.ClientRequestId = uuid.New()
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o2.Id, "register-invoice"), next), http.StatusConflict, "supplier_account_missing", "no default account")
	h.books.refuse = &contracts.SupplierInvoiceRefusal{Code: contracts.SupplierInvoiceBooksNotSetUp, Detail: "set up"}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o2.Id, "register-invoice"), next), http.StatusConflict, "books_not_set_up", "the books")
	h.books.refuse = &contracts.SupplierInvoiceRefusal{Code: contracts.SupplierInvoiceInvalid, Detail: "rate",
		Fields: []contracts.SupplierInvoiceField{{Field: "exchangeRate", Message: "A rate is needed"}}}
	p := expectRefusal(t, c.Do(http.MethodPost, orderPath(o2.Id, "register-invoice"), next), http.StatusBadRequest, "supplier_invoice_invalid", "a field")
	if p.Errors == nil || len((*p.Errors)["exchangeRate"]) != 1 {
		t.Fatalf("fields = %+v", p.Errors)
	}
	h.books.refuse = nil
	if n := h.Count(t, `SELECT count(*) FROM purchasing.invoice_matches WHERE order_id = $1`, o2.Id); n != 0 {
		t.Fatalf("matches after refusals = %d", n)
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "register-invoice"), gen.PurchasingInvoiceRegisterInput{ClientRequestId: uuid.New(),
		InvoiceNumber: " ", InvoiceDate: in.InvoiceDate}), http.StatusBadRequest, "", "a blank number")
	draft := createOrder(t, c, supNorsk, variantLine(varNut, "1"))
	expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "register-invoice"), gen.PurchasingInvoiceRegisterInput{ClientRequestId: uuid.New(),
		InvoiceNumber: "9", InvoiceDate: in.InvoiceDate}), http.StatusConflict, "order_not_sent", "a draft")
}

// An order with more than 200 lines to invoice is invoiced in parts.
func TestRegisterInvoice_TwoHundredLinesAtATime(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	lines := make([]gen.PurchasingLineInput, 0, 205)
	for range 205 {
		l := variantLine(varNut, "1")
		l.UnitPrice = str("1")
		lines = append(lines, l)
	}
	o := sentOrder(t, c, lines...)
	rl := make([]gen.PurchasingReceiptLineInput, 0, len(o.Lines))
	for _, l := range o.Lines {
		rl = append(rl, rline(l.Id, "1", locShelfA))
	}
	receive(t, c, o.Id, receiptInput(rl...))
	for i, want := range []struct{ lines, left int32 }{{200, 5}, {5, 0}} {
		res := c.Do(http.MethodPost, orderPath(o.Id, "register-invoice"), gen.PurchasingInvoiceRegisterInput{ClientRequestId: uuid.New(),
			InvoiceNumber: fmt.Sprintf("P-%d", i), InvoiceDate: openapi_types.Date{Time: day(2026, 11, 10)}})
		mustStatus(t, res, http.StatusCreated, "register a part")
		var reg gen.PurchasingInvoiceRegistration
		res.JSON(&reg)
		if reg.Lines != want.lines || reg.LinesLeft != want.left || len(h.books.lastDraft().Lines) != int(want.lines) {
			t.Fatalf("part %d = %+v", i, reg)
		}
	}
}

// Each new operation is behind its key: the reads under purchasing:access,
// the match under purchasing:invoices-match, Register invoice under it and
// accounting:post both.
func TestMatch_Permissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	inv := h.books.enter(supNorsk, "invoice", "F-7", "NOK", "", fakeLine{"x", "10.00"})
	register := func(cl *modtest.Client) *modtest.Response {
		return cl.Do(http.MethodPost, orderPath(o.Id, "register-invoice"), gen.PurchasingInvoiceRegisterInput{ClientRequestId: uuid.New(),
			InvoiceNumber: "F-8", InvoiceDate: openapi_types.Date{Time: day(2026, 11, 10)}})
	}
	writes := func(cl *modtest.Client) []*modtest.Response {
		return []*modtest.Response{
			cl.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, o.Lines[1].Id, "1"))),
			cl.Do(http.MethodPost, invoicePath(inv, "match-all"), gen.PurchasingInvoiceMatchAllInput{OrderId: o.Id}),
			cl.Do(http.MethodPost, invoicePath(inv, "unmatch"), gen.PurchasingInvoiceUnmatchInput{}),
			cl.Do(http.MethodGet, "/api/v1/purchasing/received-not-invoiced", nil),
			cl.Do(http.MethodGet, "/api/v1/purchasing/received-not-invoiced/csv", nil),
		}
	}
	for _, keys := range [][]string{{"purchasing:access"}, {"accounting:post"}, {"accounting:read"}} {
		cl := h.SignIn(t, keys...)
		for i, res := range writes(cl) {
			mustStatus(t, res, http.StatusForbidden, fmt.Sprintf("%v: write %d", keys, i))
		}
		mustStatus(t, register(cl), http.StatusForbidden, fmt.Sprintf("%v: register", keys))
	}
	alone := h.SignIn(t, "purchasing:invoices-match")
	for i, res := range writes(alone) {
		if res.Status == http.StatusForbidden {
			t.Fatalf("invoices-match alone: write %d = 403", i)
		}
	}
	mustStatus(t, alone.Do(http.MethodGet, invoicePath(inv, "match"), nil), http.StatusForbidden, "the panel without purchasing:access")
	mustStatus(t, alone.Do(http.MethodGet, orderPath(o.Id, "invoices"), nil), http.StatusForbidden, "the order's invoices without purchasing:access")
	expectRefusal(t, register(alone), http.StatusForbidden, "accounting_post_required", "register without accounting:post")
	mustStatus(t, register(h.SignIn(t, "accounting:post")), http.StatusForbidden, "register with accounting:post alone")
	mustStatus(t, register(h.SignIn(t, "purchasing:invoices-match", "accounting:post")), http.StatusCreated, "register with both keys")
	reader := h.SignIn(t, "purchasing:access")
	mustStatus(t, reader.Do(http.MethodGet, invoicePath(inv, "match"), nil), http.StatusOK, "the panel under purchasing:access")
	mustStatus(t, reader.Do(http.MethodGet, orderPath(o.Id, "invoices"), nil), http.StatusOK, "the order's invoices under purchasing:access")
}

// Posted fixes a draft's matches and queues the posted invoice's worker;
// reversed releases them and gives the copy the same matches, provisional;
// each event delivered again changes nothing.
func TestMatch_PostedAndReversed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	bolt := o.Lines[0].Id
	inv := h.books.enter(supNorsk, "invoice", "F-9", "NOK", "", fakeLine{"Bolter", "150.00"})
	panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, bolt, "30"))), "allocate")

	h.books.post(t, h, inv)
	state := func(id int64) string {
		var s string
		if err := h.Pool().QueryRow(context.Background(), `SELECT coalesce(string_agg(state, ','), '') FROM purchasing.invoice_matches WHERE supplier_invoice_id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := state(inv); got != "posted" {
		t.Fatalf("after posting = %q", got)
	}
	posted := lastEventID(t, h, "accounting.supplier_invoice.posted")
	deliverAgain(t, h, "purchasing.invoice-posted", posted)
	deliverAgain(t, h, "purchasing.invoice-posted", posted)
	if got := state(inv); got != "posted" || lineInvoiced(t, c, o.Id, bolt) != "30" {
		t.Fatalf("after posted twice more = %q, invoiced %s", got, lineInvoiced(t, c, o.Id, bolt))
	}
	if n := h.Count(t, `SELECT count(*) FROM purchasing.invoice_followups WHERE supplier_invoice_id = $1`, inv); n != 1 {
		t.Fatalf("follow-ups = %d", n)
	}

	cp := h.books.reverse(t, h, inv)
	if state(inv) != "" || state(cp) != "provisional" || lineInvoiced(t, c, o.Id, bolt) != "30" {
		t.Fatalf("after reversal: original %q, copy %q, invoiced %s", state(inv), state(cp), lineInvoiced(t, c, o.Id, bolt))
	}
	reversed := lastEventID(t, h, "accounting.supplier_invoice.reversed")
	deliverAgain(t, h, "purchasing.invoice-reversed", reversed)
	deliverAgain(t, h, "purchasing.invoice-reversed", reversed)
	if state(cp) != "provisional" || lineInvoiced(t, c, o.Id, bolt) != "30" {
		t.Fatalf("after reversed twice more: copy %q, invoiced %s", state(cp), lineInvoiced(t, c, o.Id, bolt))
	}
	// The reversed invoice is matched to nothing again.
	expectRefusal(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, bolt, "1"))), http.StatusConflict, "invoice_reversed", "the reversed")
	// The copy is posted in its turn.
	h.books.post(t, h, cp)
	if state(cp) != "posted" {
		t.Fatalf("copy after posting = %q", state(cp))
	}
	// The undo of the receipt is now refused: the bolts are invoiced.
	receipts := c.Do(http.MethodGet, receiptsPath(o.Id), nil)
	var list gen.PurchasingReceiptList
	receipts.JSON(&list)
	expectRefusal(t, c.Do(http.MethodPost, receiptPath(list.Items[0].Id, "undo"), gen.PurchasingUndoInput{}), http.StatusConflict, "receipt_invoiced", "undo an invoiced receipt")
}

// Out of order: a reversal taken before the original's posting leaves the
// posting nothing to fix; a copy's posting taken before its original's
// reversal makes the copied matches posted.
func TestMatch_EventsOutOfOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	bolt := o.Lines[0].Id
	inv := h.books.enter(supNorsk, "invoice", "F-10", "NOK", "", fakeLine{"Bolter", "150.00"})
	panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, bolt, "30"))), "allocate")
	invoiceDay := day(2026, 11, 10).Format("2006-01-02")
	copyID := inv + 500
	publishEvents(t, h,
		contracts.SupplierInvoicePosted{InvoiceID: copyID, SupplierID: supNorsk, Kind: "invoice", Number: "F-10", InvoiceDate: invoiceDay, Currency: "NOK", Total: "150.00", PostedAt: h.Now()},
		contracts.SupplierInvoiceReversed{InvoiceID: inv, SupplierID: supNorsk, Kind: "invoice", Number: "F-10", InvoiceDate: invoiceDay, Currency: "NOK", Total: "150.00", CopyID: copyID, ReversedAt: h.Now()},
		contracts.SupplierInvoicePosted{InvoiceID: inv, SupplierID: supNorsk, Kind: "invoice", Number: "F-10", InvoiceDate: invoiceDay, Currency: "NOK", Total: "150.00", PostedAt: h.Now()},
	)
	if n := h.Count(t, `SELECT count(*) FROM purchasing.invoice_matches WHERE supplier_invoice_id = $1`, inv); n != 0 {
		t.Fatalf("the original's matches = %d, want 0", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM purchasing.invoice_matches WHERE supplier_invoice_id = $1 AND state = 'posted'`, copyID); n != 1 {
		t.Fatalf("the copy's posted matches = %d, want 1", n)
	}
	if got := lineInvoiced(t, c, o.Id, bolt); got != "30" {
		t.Fatalf("invoiced = %s, want 30", got)
	}
	// A posted invoice whose supplier is no longer the order's releases it.
	other := h.books.enter(supNorsk, "invoice", "F-11", "NOK", "", fakeLine{"Mutre", "10.00"})
	panelOf(t, c.Do(http.MethodPost, invoicePath(other, "matches"), matchIn(o.Id, mline(1, o.Lines[1].Id, "5"))), "allocate the nuts")
	publishEvents(t, h, contracts.SupplierInvoicePosted{InvoiceID: other, SupplierID: supGerman, Kind: "invoice", Number: "F-11",
		InvoiceDate: invoiceDay, Currency: "NOK", Total: "10.00", PostedAt: h.Now()})
	if n := h.Count(t, `SELECT count(*) FROM purchasing.invoice_matches WHERE supplier_invoice_id = $1`, other); n != 0 || lineInvoiced(t, c, o.Id, o.Lines[1].Id) != "0" {
		t.Fatalf("a supplier changed on the draft: matches %d", n)
	}
}

// The posted invoice's worker writes the posted nets, and sets the last
// price per the supplier item's packaging — unless the invoice is in
// another currency than the item's, or a person set the price after the
// invoice's date; price differences are judged against the tolerance.
func TestMatch_LastPriceAndDifferences(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	item := func(v int32, price string, pack *int32) gen.PurchasingSupplierItem {
		res := c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: v, Price: str(price), PackagingId: pack})
		mustStatus(t, res, http.StatusCreated, "an item")
		var it gen.PurchasingSupplierItem
		res.JSON(&it)
		return it
	}
	boltItem := item(varBolt, "50", i32(packBox))
	nutItem := item(varNut, "2", nil)
	o := receivedOrder(t, c)
	// The nuts' price was set by a person after the invoice's day.
	h.Exec(t, `UPDATE purchasing.supplier_items SET price_set_on = '2026-11-12' WHERE id = $1`, nutItem.Id)
	h.Exec(t, `UPDATE purchasing.supplier_items SET price_set_on = '2026-11-01' WHERE id = $1`, boltItem.Id)
	// 30 bolts for 153.00 is 2 % over 150.00: within the default tolerance.
	inv := h.books.enter(supNorsk, "invoice", "F-12", "NOK", "", fakeLine{"Bolter", "153.00"}, fakeLine{"Mutre", "12.00"})
	p := panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "match-all"), gen.PurchasingInvoiceMatchAllInput{OrderId: o.Id}), "match all")
	if len(p.Matches[0].Warnings) != 0 || len(p.Matches[1].Warnings) != 1 || p.Matches[1].Warnings[0] != gen.PriceDiffers {
		t.Fatalf("warnings at 2 %% = %+v / %+v", p.Matches[0].Warnings, p.Matches[1].Warnings)
	}
	h.Exec(t, `INSERT INTO purchasing.settings (id, price_tolerance_percent, updated_at) VALUES (1, 1, now())
		ON CONFLICT (id) DO UPDATE SET price_tolerance_percent = 1`)
	p = panelOf(t, c.Do(http.MethodGet, invoicePath(inv, "match"), nil), "at 1 %")
	if p.Differences != 2 || p.Matches[0].Warnings[0] != gen.PriceDiffers {
		t.Fatalf("at 1 %% = %+v", p)
	}
	// The posted line's net is what counts, not the draft's.
	h.books.edit(inv, func(i *contracts.SupplierInvoice) { i.Lines[0].Net = "156.00" })
	h.books.post(t, h, inv)
	run := workerNamed(t, h, "purchasing.invoice-posted")
	if n := run(); n != 1 {
		t.Fatalf("the worker claimed %d", n)
	}
	var net string
	if err := h.Pool().QueryRow(context.Background(), `SELECT line_net::text FROM purchasing.invoice_matches WHERE supplier_invoice_id = $1 AND line_position = 1`, inv).Scan(&net); err != nil {
		t.Fatal(err)
	}
	if net != "156.00" {
		t.Fatalf("the posted net = %s", net)
	}
	b := getItem(t, c, boltItem.Id)
	if b.Price == nil || *b.Price != "52.00" || b.PriceSetBy == nil || string(*b.PriceSetBy) != "invoice" || b.PriceSetOn == nil || b.PriceSetOn.Format("2006-01-02") != "2026-11-10" {
		t.Fatalf("the bolt's last price = %+v", b)
	}
	if n := getItem(t, c, nutItem.Id); *n.Price != "2.00" {
		t.Fatalf("the nut's price, set by a person later, = %s", *n.Price)
	}
	if n := run(); n != 0 {
		t.Fatalf("a second cycle claimed %d", n)
	}
	// Another currency than the item's sets nothing.
	h.Exec(t, `UPDATE purchasing.supplier_items SET currency = 'EUR' WHERE id = $1`, boltItem.Id)
	o2 := receivedOrder(t, c)
	inv2 := h.books.enter(supNorsk, "invoice", "F-13", "NOK", "", fakeLine{"Bolter", "180.00"})
	panelOf(t, c.Do(http.MethodPost, invoicePath(inv2, "match-all"), gen.PurchasingInvoiceMatchAllInput{OrderId: o2.Id}), "match all")
	h.books.post(t, h, inv2)
	run()
	if b := getItem(t, c, boltItem.Id); *b.Price != "52.00" {
		t.Fatalf("an invoice in another currency set %s", *b.Price)
	}
}

func getItem(t *testing.T, c *modtest.Client, id int64) gen.PurchasingSupplierItem {
	t.Helper()
	res := c.Do(http.MethodGet, fmt.Sprintf("%s/%d", itemsPath, id), nil)
	mustStatus(t, res, http.StatusOK, "the item")
	var it gen.PurchasingSupplierItem
	res.JSON(&it)
	return it
}

// The badge's provider answers the orders and the differences, to a holder
// of purchasing:access only.
func TestMatch_SummaryProvider(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	inv := h.books.enter(supNorsk, "invoice", "F-14", "NOK", "", fakeLine{"Bolter", "300.00"})
	panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, o.Lines[0].Id, "30"))), "allocate")
	none := h.books.enter(supNorsk, "invoice", "F-15", "NOK", "", fakeLine{"x", "1.00"})
	provider := purchasing.Module().SupplierInvoiceMatches(h.Deps())
	_, holder := h.SignInUser(t, "purchasing:access")
	got, err := provider.Summary(contracts.AsUser(context.Background(), holder), []int64{inv, none})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].InvoiceID != inv || len(got[0].Orders) != 1 || got[0].Orders[0].ID != o.Id ||
		got[0].Orders[0].Number != fmt.Sprint(*o.Number) || got[0].Differences != 1 {
		t.Fatalf("summary = %+v", got)
	}
	_, bookkeeper := h.SignInUser(t, "accounting:read")
	if got, _ := provider.Summary(contracts.AsUser(context.Background(), bookkeeper), []int64{inv}); len(got) != 0 {
		t.Fatalf("summary without purchasing:access = %+v", got)
	}
	if _, err := provider.Summary(context.Background(), make([]int64, contracts.MaxSuppliers+1)); err == nil {
		t.Fatal("too many ids answered")
	}
}

// Received, not invoiced, as of a day: a receipt after the day is left out,
// a posted invoice dated by the day takes its part off, a provisional one
// does not; the CSV says the same.
func TestMatch_ReceivedNotInvoiced(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	inv := h.books.enter(supNorsk, "invoice", "F-16", "NOK", "", fakeLine{"Bolter", "100.00"})
	today := h.Now().UTC()
	h.books.edit(inv, func(i *contracts.SupplierInvoice) { i.InvoiceDate = day(today.Year(), today.Month(), today.Day()) })
	panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, o.Lines[0].Id, "20"))), "allocate")
	list := func(asOf string) gen.PurchasingInvoiceUninvoiced {
		path := "/api/v1/purchasing/received-not-invoiced?supplierId=" + itoa(supNorsk)
		if asOf != "" {
			path += "&asOf=" + asOf
		}
		res := c.Do(http.MethodGet, path, nil)
		mustStatus(t, res, http.StatusOK, "the list")
		var l gen.PurchasingInvoiceUninvoiced
		res.JSON(&l)
		return l
	}
	now := list("")
	if len(now.Rows) != 2 || now.Rows[0].Quantity != "30" || now.Rows[0].Amount != "150.00" || now.Totals[0].Amount != "160.00" {
		t.Fatalf("with a provisional match = %+v", now)
	}
	h.books.post(t, h, inv)
	now = list(today.Format("2006-01-02"))
	if now.Rows[0].Quantity != "10" || now.Rows[0].Invoiced != "20" || now.Rows[0].Amount != "50.00" || now.Totals[0].Amount != "60.00" {
		t.Fatalf("after posting = %+v", now)
	}
	// The day before, nothing was received yet.
	if l := list(today.AddDate(0, 0, -1).Format("2006-01-02")); len(l.Rows) != 0 {
		t.Fatalf("before the receipt = %+v", l)
	}
	// An invoice dated after the day does not bill it as of the day.
	h.Exec(t, `UPDATE purchasing.invoice_matches SET invoice_date = invoice_date + 1 WHERE supplier_invoice_id = $1`, inv)
	if l := list(today.Format("2006-01-02")); l.Rows[0].Quantity != "30" {
		t.Fatalf("an invoice dated after the day = %+v", l)
	}
	h.Exec(t, `UPDATE purchasing.invoice_matches SET invoice_date = invoice_date - 1 WHERE supplier_invoice_id = $1`, inv)
	res := c.Do(http.MethodGet, "/api/v1/purchasing/received-not-invoiced/csv?supplierId="+itoa(supNorsk), nil)
	mustStatus(t, res, http.StatusOK, "the CSV")
	body := string(res.Body)
	if !strings.HasPrefix(body, "\ufeffsupplier_id;") || !strings.Contains(body, ";30;20;10;5,00;50,00;NOK\r\n") {
		t.Fatalf("the CSV = %q", body)
	}
}

// Two invoices matched to one received line at once: the second waits on
// the order's lock and is refused beyond what is received.
func TestMatch_ConcurrentAllocation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	o := receivedOrder(t, c)
	a := h.books.enter(supNorsk, "invoice", "C-1", "NOK", "", fakeLine{"Bolter", "100.00"})
	b := h.books.enter(supNorsk, "invoice", "C-2", "NOK", "", fakeLine{"Bolter", "100.00"})
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, inv := range []int64{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = c.Do(http.MethodPost, invoicePath(inv, "matches"), matchIn(o.Id, mline(1, o.Lines[0].Id, "20"))).Status
		}()
	}
	wg.Wait()
	ok, refused := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			refused++
		}
	}
	if ok != 1 || refused != 1 || lineInvoiced(t, c, o.Id, o.Lines[0].Id) != "20" {
		t.Fatalf("statuses = %v, invoiced %s", statuses, lineInvoiced(t, c, o.Id, o.Lines[0].Id))
	}
}

// The supplier's export carries the matches; the erase leaves them, as they
// hold no personal data.
func TestMatch_SupplierExportAndErase(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, everyKey...)
	bolts := variantLine(varBolt, "1")
	bolts.PackagingId, bolts.UnitPrice = i32(packBox), str("50")
	o := markSent(t, c, createOrder(t, c, supPerson, bolts))
	receive(t, c, o.Id, receiptInput(rline(o.Lines[0].Id, "10", locShelfA)))
	inv := h.books.enter(supPerson, "invoice", "S-1", "NOK", "", fakeLine{"Bolter", "50.00"})
	panelOf(t, c.Do(http.MethodPost, invoicePath(inv, "match-all"), gen.PurchasingInvoiceMatchAllInput{OrderId: o.Id}), "match all")
	data := purchasing.Module().SupplierPersonalData(h.Deps())
	exported, err := data.ExportSupplierData(context.Background(), supPerson)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(exported)
	if !strings.Contains(string(body), `"invoiceMatches":[{"supplierInvoiceId":`+itoa(inv)+`,"invoiceNumber":"S-1"`) {
		t.Fatalf("export = %s", body)
	}
	if err := pgx.BeginFunc(context.Background(), h.Pool(), func(tx pgx.Tx) error {
		_, err := data.EraseSupplierData(context.Background(), tx, supPerson)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, `SELECT count(*) FROM purchasing.invoice_matches WHERE supplier_invoice_id = $1`, inv); n != 1 {
		t.Fatalf("matches after the erase = %d", n)
	}
}
