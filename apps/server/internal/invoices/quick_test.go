package invoices_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const quickPath = invoicesPath + "/quick"

// quickJSON is POST /invoices/quick's 201 as a client reads it.
type quickJSON struct {
	Invoice             invoiceJSON  `json:"invoice"`
	PaymentRequestError *problemJSON `json:"paymentRequestError"`
}

// quickBody is a quick invoice for customer with lines, every other field
// left to its default.
func quickBody(customer int32, lines ...map[string]any) map[string]any {
	if lines == nil {
		lines = []map[string]any{}
	}
	return map[string]any{"customerId": customer, "lines": lines}
}

// quicker is a caller holding the quick invoice's three keys.
func quicker(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:create", "invoices:issue")
}

// quickInvoiced posts body and answers the 201, failing unless it was one.
func quickInvoiced(t *testing.T, h *harness, body map[string]any) quickJSON {
	t.Helper()
	res := quicker(t, h).Do(http.MethodPost, quickPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /invoices/quick = %d %s, want 201", res.Status, res.Body)
	}
	var got quickJSON
	res.JSON(&got)
	return got
}

// sameJSON is whether a and b encode alike: pointers compared by what they
// point at.
func sameJSON(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("encode %v: %v", a, err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("encode %v: %v", b, err)
	}
	return bytes.Equal(x, y)
}

// documentCount is how many documents exist, drafts included.
func documentCount(t *testing.T, h *harness) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM invoices.invoices`)
}

// One transaction (D17): the draft inserted is the transaction's own and is
// never seen outside it before the issue commits; the answer is the issued
// document — the next number, today's date, its terms, the PDF stored after
// the commit — and a manual delivery handed_over on the issue date by the
// caller. payOnSite issues all the same and answers why no request was made.
// Not parallel: the seam is the package's.
func TestQuickInvoice_OneTransaction(t *testing.T) {
	h := readyToIssue(t) // 2026-09-12
	var seen []int64
	var mu sync.Mutex
	restore := invoices.SetQuickAfterInsert(func(_ context.Context, invoiceID int64) error {
		// The handler's goroutine: t.Errorf, never a t.Fatal.
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, invoiceID)
		if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE id = $1`, invoiceID); n != 0 {
			t.Errorf("the quick invoice's draft %d is visible outside its transaction before the issue: committed early", invoiceID)
		}
		return nil
	})
	defer restore()

	client, userID := h.SignInUser(t, "invoices:access", "invoices:create", "invoices:issue")
	body := quickBody(customerAcme, line("Utrykning", 1, 950, vat25), line("Deler", 2, 125.5, vat25))
	body["note"] = "Takk for oppdraget"
	res := client.Do(http.MethodPost, quickPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /invoices/quick = %d %s, want 201", res.Status, res.Body)
	}
	var got quickJSON
	res.JSON(&got)
	inv := got.Invoice
	if inv.Status != "issued" || inv.Kind != "invoice" || inv.Number == nil || *inv.Number != 1 || *inv.IssueDate != "2026-09-12" {
		t.Fatalf("the quick invoice = %+v, want invoice number 1 issued 2026-09-12", inv)
	}
	if inv.DeliveryDate == nil || *inv.DeliveryDate != "2026-09-12" || *inv.DueDate != "2026-10-12" || inv.YourReference != "PO-77" {
		t.Errorf("delivery %v, due %v, reference %q; want today, 30 days of Acme's terms, Acme's PO-77",
			inv.DeliveryDate, inv.DueDate, inv.YourReference)
	}
	if inv.GrossTotal != 1501.25 || inv.Note != "Takk for oppdraget" || len(inv.Lines) != 2 || inv.Buyer == nil {
		t.Errorf("gross %v, note %q, %d lines, buyer %v; want 1501.25, the note, 2 lines, a buyer snapshot",
			inv.GrossTotal, inv.Note, len(inv.Lines), inv.Buyer)
	}
	if inv.PdfStored == nil || !*inv.PdfStored {
		t.Errorf("pdfStored = %v, want the PDF stored after the commit", inv.PdfStored)
	}
	if keys, _, _ := h.objects.stored(); len(keys) != 1 {
		t.Errorf("stored objects = %v, want the one PDF", keys)
	}
	if got.PaymentRequestError != nil {
		t.Errorf("paymentRequestError = %+v, want none without payOnSite", got.PaymentRequestError)
	}
	mu.Lock()
	if !slices.Equal(seen, []int64{inv.ID}) {
		t.Errorf("the seam saw %v, want the quick invoice's own id %d", seen, inv.ID)
	}
	mu.Unlock()
	deliveries := receivablesOf(t, h, inv.ID).ManualDeliveries
	if deliveries == nil || len(*deliveries) != 1 {
		t.Fatalf("manual deliveries = %v, want the one handed_over", deliveries)
	}
	if d := (*deliveries)[0]; d.Kind != "handed_over" || d.DeliveredOn != "2026-09-12" || d.RecordedByUserID != userID.String() || d.RemovedAt != nil {
		t.Errorf("the delivery = %+v, want handed_over on 2026-09-12 by the caller %s", d, userID)
	}

	// payOnSite: issued all the same; on-site payment is not offered yet.
	body = quickBody(customerAcme, line("Service", 1, 400, vat25))
	body["payOnSite"], body["paymentTermsDays"] = true, 10
	second := quickInvoiced(t, h, body)
	if second.Invoice.Status != "issued" || *second.Invoice.Number != 2 || *second.Invoice.DueDate != "2026-09-22" {
		t.Errorf("payOnSite's document = %+v, want number 2 issued, due in the 10 days asked", second.Invoice)
	}
	if p := second.PaymentRequestError; p == nil || p.Code != "on_site_payments_unavailable" {
		t.Errorf("paymentRequestError = %+v, want on_site_payments_unavailable", p)
	}
}

// Any refusal — the draft's 400s, the customer gates, every issue check —
// rolls everything back (D17): no document is left and no number is taken.
func TestQuickInvoice_NoDraftLeftByAnyRefusal(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	ok := line("Utrykning", 1, 950, vat25)
	nothingLeft := func(what string, documents int, next int64) {
		t.Helper()
		if n := documentCount(t, h); n != documents {
			t.Errorf("%s: documents = %d, want %d — a draft was left", what, n, documents)
		}
		if n := counterNext(t, h); n != next {
			t.Errorf("%s: the counter's next = %d, want %d — a number was taken", what, n, next)
		}
	}
	refused := func(what string, body map[string]any, status int, code string) {
		t.Helper()
		documents, next := documentCount(t, h), counterNext(t, h)
		res := quicker(t, h).Do(http.MethodPost, quickPath, body)
		if res.Status != status {
			t.Errorf("%s = %d %s, want %d %s", what, res.Status, res.Body, status, code)
		} else if p := problemOf(t, res); p.Code != code {
			t.Errorf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
		}
		nothingLeft(what, documents, next)
	}
	badField := func(what string, body map[string]any, field string) {
		t.Helper()
		documents, next := documentCount(t, h), counterNext(t, h)
		badOn(t, what, quicker(t, h).Do(http.MethodPost, quickPath, body), field)
		nothingLeft(what, documents, next)
	}

	// The draft's rules (parseDraft) and the quick invoice's one to ten lines.
	badField("no lines", quickBody(customerAcme), "lines")
	eleven := make([]map[string]any, 11)
	for i := range eleven {
		eleven[i] = ok
	}
	badField("eleven lines", quickBody(customerAcme, eleven...), "lines")
	badField("no description", quickBody(customerAcme, line(" ", 1, 100, vat25)), "lines[0].description")
	badField("a negative quantity", quickBody(customerAcme, line("A", -1, 100, vat25)), "lines[0].quantity")
	badField("an unknown VAT code", quickBody(customerAcme, line("A", 1, 100, vatUnknownID)), "lines[0].vatCodeId")
	badField("no customer", quickBody(0, ok), "customerId")
	terms := quickBody(customerAcme, ok)
	terms["paymentTermsDays"] = 400
	badField("terms past a year", terms, "paymentTermsDays")

	// The customer gates.
	refused("merged away", quickBody(customerMerged, ok), http.StatusConflict, "customer_merged")
	refused("archived", quickBody(customerArchived, ok), http.StatusConflict, "customer_archived")
	refused("blocked", quickBody(customerDisabled, ok), http.StatusConflict, "customer_blocked")
	refused("no such customer", quickBody(customerUnknown, ok), http.StatusConflict, "customer_missing")

	// The issue's checks, each after the draft was inserted and the number
	// allocated in the same transaction.
	refused("a buyer without an address", quickBody(customerNoAddress, ok), http.StatusConflict, "buyer_incomplete")
	refused("an O line from a registered seller", quickBody(customerAcme, line("Utenfor", 1, 100, vatOutside)), http.StatusConflict, "category_o_not_allowed")
	var future vatCodeJSON
	manager(t, h).Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "25N", "name": "Ny sats", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-10-01",
	}).JSON(&future)
	refused("a code with no rate today", quickBody(customerAcme, line("Fremtid", 1, 100, future.ID)), http.StatusConflict, "vat_code_not_valid")

	first := quickInvoiced(t, h, quickBody(customerAcme, ok)).Invoice
	if *first.Number != 1 {
		t.Fatalf("the first quick invoice after the refusals = %d, want 1: every refusal rolled its number back", *first.Number)
	}
	withKidAgreement(t, h, 5, "mod11")
	h.Exec(t, `UPDATE invoices.counters SET next_value = 1000`)
	h.Exec(t, `UPDATE invoices.settings SET kid_length = 4`)
	refused("a number the KID agreement no longer fits", quickBody(customerAcme, ok), http.StatusConflict, "kid_length_exceeded")
	h.Exec(t, `UPDATE invoices.settings SET kid_length = 5`)

	// A day before the latest issue date: today is not allowed then.
	h.Advance(-24 * time.Hour)
	refused("today before the latest issue date", quickBody(customerAcme, ok), http.StatusConflict, "issue_date_not_allowed")
	h.Advance(24 * time.Hour)
	if inv := quickInvoiced(t, h, quickBody(customerAcme, ok)).Invoice; *inv.Number != 1000 {
		t.Errorf("after every refusal the next quick invoice = %d, want 1000", *inv.Number)
	}
}

// The seller's record is the issue's first check: a quick invoice on an
// installation with no seller leaves nothing either.
func TestQuickInvoice_SellerIncomplete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	res := quicker(t, h).Do(http.MethodPost, quickPath, quickBody(customerAcme, line("A", 1, 100, vat25)))
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "seller_incomplete" {
		t.Errorf("no seller = %d %s, want 409 seller_incomplete", res.Status, res.Body)
	}
	if n := documentCount(t, h); n != 0 {
		t.Errorf("documents = %d, want none", n)
	}
}

// Without an object store a quick invoice is refused before its transaction
// (D17, as the issue): 503 storage_unavailable, nothing written.
func TestQuickInvoice_StorageUnavailable(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutStore(t)
	saveSeller(t, h, completeSeller(1))
	res := quicker(t, h).Do(http.MethodPost, quickPath, quickBody(customerAcme, line("A", 1, 100, vat25)))
	if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Errorf("no object store = %d %s, want 503 storage_unavailable", res.Status, res.Body)
	}
	if n, next := documentCount(t, h), counterNext(t, h); n != 0 || next != 0 {
		t.Errorf("documents %d, counter %d; want none and no counter", n, next)
	}
}

// The quick invoice needs invoices:create and invoices:issue beside
// invoices:access (D1's three-key grammar): either alone is a 403.
func TestQuickInvoice_Permissions(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	body := quickBody(customerAcme, line("A", 1, 100, vat25))
	for _, keys := range [][]string{
		{"invoices:access", "invoices:create"},
		{"invoices:access", "invoices:issue"},
		{"invoices:access", "invoices:create", "invoices:payments", "invoices:manage"},
	} {
		if res := h.SignIn(t, keys...).Do(http.MethodPost, quickPath, body); res.Status != http.StatusForbidden {
			t.Errorf("%v = %d %s, want 403", keys, res.Status, res.Body)
		}
	}
	if n := documentCount(t, h); n != 0 {
		t.Errorf("documents after the 403s = %d, want none", n)
	}
	if inv := quickInvoiced(t, h, body).Invoice; inv.Status != "issued" {
		t.Errorf("the three keys = %+v, want issued", inv)
	}
}

// Meta's canQuickInvoice is invoices:create and invoices:issue, both.
func TestMeta_CanQuickInvoice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, c := range []struct {
		keys []string
		want bool
	}{
		{[]string{"invoices:access"}, false},
		{[]string{"invoices:access", "invoices:create"}, false},
		{[]string{"invoices:access", "invoices:issue"}, false},
		{[]string{"invoices:access", "invoices:create", "invoices:issue"}, true},
	} {
		var meta struct {
			Capabilities struct {
				CanQuickInvoice *bool `json:"canQuickInvoice"`
			} `json:"capabilities"`
		}
		res := h.SignIn(t, c.keys...).Do(http.MethodGet, metaPath, nil)
		if res.Status != http.StatusOK {
			t.Fatalf("GET /meta = %d %s", res.Status, res.Body)
		}
		res.JSON(&meta)
		if got := meta.Capabilities.CanQuickInvoice; got == nil || *got != c.want {
			t.Errorf("%v: canQuickInvoice = %v, want %v", c.keys, got, c.want)
		}
	}
}

// The quick invoice issues as the issue does (the refactor's proof beside the
// issue's own suite, run unchanged): the same lines for the same customer
// issued by POST /{id}/issue and by POST /quick give the same snapshots, VAT
// rows, totals, terms and KID algorithm, at consecutive numbers.
func TestIssue_RefactorChangesNothing(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	withKidAgreement(t, h, 7, "mod10")
	lines := []map[string]any{line("Konsulenttime", 10, 1200, vat25), line("Kurs", 1, 5000, vatExempt), line("Mat", 3, 99.9, vat15)}
	body := draftBody(customerAcme, lines...)
	body["deliveryDate"] = "2026-09-12"
	ordinary := issued(t, h, createDraft(t, h, body).ID)
	quick := quickInvoiced(t, h, quickBody(customerAcme, lines...)).Invoice

	if *ordinary.Number != 1 || *quick.Number != 2 {
		t.Errorf("numbers = %d, %d; want 1 and 2", *ordinary.Number, *quick.Number)
	}
	if *quick.IssueDate != *ordinary.IssueDate || *quick.DueDate != *ordinary.DueDate || *quick.DeliveryDate != *ordinary.DeliveryDate {
		t.Errorf("dates: quick %s/%s/%s, issue %s/%s/%s", *quick.IssueDate, *quick.DueDate, *quick.DeliveryDate,
			*ordinary.IssueDate, *ordinary.DueDate, *ordinary.DeliveryDate)
	}
	if quick.NetTotal != ordinary.NetTotal || quick.VatTotal != ordinary.VatTotal || quick.GrossTotal != ordinary.GrossTotal ||
		quick.VatTotalNok != ordinary.VatTotalNok || !sameJSON(t, quick.VatSummaries, ordinary.VatSummaries) {
		t.Errorf("totals/VAT: quick %v %v %v %+v, issue %v %v %v %+v", quick.NetTotal, quick.VatTotal, quick.GrossTotal, quick.VatSummaries,
			ordinary.NetTotal, ordinary.VatTotal, ordinary.GrossTotal, ordinary.VatSummaries)
	}
	if !sameJSON(t, quick.Buyer, ordinary.Buyer) || !sameJSON(t, quick.Seller, ordinary.Seller) || quick.YourReference != ordinary.YourReference {
		t.Errorf("snapshots: quick %+v %+v, issue %+v %+v", *quick.Buyer, *quick.Seller, *ordinary.Buyer, *ordinary.Seller)
	}
	if quick.Kid == nil || quick.KidAlgorithm == nil || *quick.KidAlgorithm != "mod10" || len(*quick.Kid) != 7 {
		t.Errorf("KID: quick %v %v, want seven digits under mod10", quick.Kid, quick.KidAlgorithm)
	}
	for i := range lines {
		q, o := quick.Lines[i], ordinary.Lines[i]
		q.ID, o.ID = 0, 0
		if !sameJSON(t, q, o) {
			t.Errorf("line %d: quick %+v, issue %+v", i+1, q, o)
		}
	}
}

// A merge that commits while the quick invoice's transaction is open — its
// RepointCustomer cannot see the uncommitted document — would leave the
// issued invoice on the customer merged away. After its commit the quick
// invoice reads the billing profile again and moves the document to the
// survivor under the document's lock, as the merge's re-point does: the
// customer id alone, the buyer snapshot unchanged. Not parallel: the seam
// is the package's.
func TestQuickInvoice_MergeInTheWindowEndsOnTheSurvivor(t *testing.T) {
	h := readyToIssue(t)
	holder := invoices.Module().CustomerReferences(h.Deps())
	conn := ownConn(t, h)
	restore := invoices.SetQuickAfterInsert(func(ctx context.Context, _ int64) error {
		// The merge, committed in the window, on a connection of its own.
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		h.customers.edit(customerNoTerms, func(p *contracts.CustomerBillingProfile) {
			survivor := int32(customerAcme)
			p.MergedInto, p.Status, p.Archived = &survivor, "archived", true
		})
		if _, err := holder.RepointCustomer(ctx, tx, customerNoTerms, customerAcme); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	defer restore()

	got := quickInvoiced(t, h, quickBody(customerNoTerms, line("Utrykning", 1, 950, vat25))).Invoice
	if got.CustomerID != customerAcme {
		t.Errorf("the answer's customer = %d, want the survivor %d", got.CustomerID, customerAcme)
	}
	read := getInvoice(t, h, got.ID)
	if read.CustomerID != customerAcme || read.Buyer == nil || read.Buyer.Name != "Uten Vilkår AS" || read.Status != "issued" {
		t.Errorf("the document = customer %d, buyer %+v, %s; want the survivor's, its snapshot as issued", read.CustomerID, read.Buyer, read.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE customer_id = $1`, customerNoTerms); n != 0 {
		t.Errorf("documents left on the customer merged away = %d, want none", n)
	}
}

// An erase that commits while the quick invoice's transaction is open
// refuses nothing: the erase keeps issued documents, it cannot see the
// uncommitted draft to delete it, and the manual delivery's parent trigger
// blanks a note rather than refusing the row. The invoice is issued — never
// a 500 — and kept as every issued document of an anonymised customer is.
// Not parallel: the seam is the package's.
func TestQuickInvoice_EraseInTheWindowIsNoFailure(t *testing.T) {
	h := readyToIssue(t)
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	conn := ownConn(t, h)
	restore := invoices.SetQuickAfterInsert(func(ctx context.Context, _ int64) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := data.EraseCustomerData(ctx, tx, customerPerson); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	defer restore()

	got := quickInvoiced(t, h, quickBody(customerPerson, line("Utrykning", 1, 950, vat25))).Invoice
	if got.Status != "issued" {
		t.Fatalf("the quick invoice = %+v, want issued", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.erased_customers WHERE customer_id = $1`, customerPerson); n != 1 {
		t.Errorf("the erase's marker = %d, want it committed", n)
	}
	if d := receivablesOf(t, h, got.ID).ManualDeliveries; d == nil || len(*d) != 1 || (*d)[0].Note != "" {
		t.Errorf("manual deliveries = %v, want the one handed_over, its note empty", d)
	}
}

// The quick invoice reads the clock once for the draft's defaults and once
// in issueLocked after the counter, as the issue does: a quick invoice that
// crosses Oslo midnight between its insert and its issue is delivered the
// day it began, issued and handed over the day it is issued — a valid
// invoice, its delivery before its issue. Not parallel: the seam is the
// package's.
func TestQuickInvoice_AcrossOsloMidnight(t *testing.T) {
	h := readyToIssue(t) // 2026-09-12, 14:00 in Oslo
	restore := invoices.SetQuickAfterInsert(func(context.Context, int64) error {
		h.Advance(11 * time.Hour) // 01:00 on the 13th in Oslo
		return nil
	})
	defer restore()

	got := quickInvoiced(t, h, quickBody(customerAcme, line("Nattvakt", 1, 950, vat25))).Invoice
	if *got.DeliveryDate != "2026-09-12" || *got.IssueDate != "2026-09-13" || *got.DueDate != "2026-10-13" {
		t.Errorf("delivered %s, issued %s, due %s; want 2026-09-12, 2026-09-13, 2026-10-13",
			*got.DeliveryDate, *got.IssueDate, *got.DueDate)
	}
	if d := receivablesOf(t, h, got.ID).ManualDeliveries; d == nil || len(*d) != 1 || (*d)[0].DeliveredOn != "2026-09-13" {
		t.Errorf("manual deliveries = %v, want handed_over on the issue date, 2026-09-13", d)
	}
}

// A quick invoice's requestId (D17, after review): the same request again —
// a retry after a lost answer — answers 200 with the invoice the first
// issued and does nothing else: no document, no number, no delivery. The
// same id with another body is 409 request_id_reused. A request refused
// keeps no id, so the id may come again with the body corrected.
func TestQuickInvoice_RequestIdReplays(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	id := "6f1c1d2e-8a8b-4c3d-9e0f-112233445566"
	body := quickBody(customerAcme, line("Utrykning", 1, 950, vat25))
	body["requestId"], body["payOnSite"] = id, true
	first := quickInvoiced(t, h, body)
	documents, next := documentCount(t, h), counterNext(t, h)

	res := quicker(t, h).Do(http.MethodPost, quickPath, body)
	if res.Status != http.StatusOK {
		t.Fatalf("the replay = %d %s, want 200", res.Status, res.Body)
	}
	var replay quickJSON
	res.JSON(&replay)
	if replay.Invoice.ID != first.Invoice.ID || *replay.Invoice.Number != *first.Invoice.Number || replay.Invoice.Status != "issued" {
		t.Errorf("the replay = %+v, want the first's invoice %d", replay.Invoice, first.Invoice.ID)
	}
	if n, c := documentCount(t, h), counterNext(t, h); n != documents || c != next {
		t.Errorf("after the replay: documents %d, counter %d; want %d and %d — nothing done again", n, c, documents, next)
	}
	if d := receivablesOf(t, h, first.Invoice.ID).ManualDeliveries; d == nil || len(*d) != 1 {
		t.Errorf("manual deliveries after the replay = %v, want the one", d)
	}

	other := quickBody(customerAcme, line("Utrykning", 2, 950, vat25))
	other["requestId"], other["payOnSite"] = id, true
	res = quicker(t, h).Do(http.MethodPost, quickPath, other)
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "request_id_reused" {
		t.Errorf("the id with another body = %d %s, want 409 request_id_reused", res.Status, res.Body)
	}

	refusedID := "6f1c1d2e-8a8b-4c3d-9e0f-998877665544"
	refused := quickBody(customerNoAddress, line("Utrykning", 1, 950, vat25))
	refused["requestId"] = refusedID
	if res := quicker(t, h).Do(http.MethodPost, quickPath, refused); res.Status != http.StatusConflict || problemOf(t, res).Code != "buyer_incomplete" {
		t.Fatalf("a refused request = %d %s, want 409 buyer_incomplete", res.Status, res.Body)
	}
	corrected := quickBody(customerAcme, line("Utrykning", 1, 950, vat25))
	corrected["requestId"] = refusedID
	if got := quickInvoiced(t, h, corrected).Invoice; got.Status != "issued" || got.ID == first.Invoice.ID {
		t.Errorf("the refused id with the body corrected = %+v, want a new invoice issued", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.quick_requests`); n != 2 {
		t.Errorf("request ids kept = %d, want the two that issued", n)
	}
	if res := quicker(t, h).Do(http.MethodPost, quickPath, map[string]any{
		"customerId": customerAcme, "lines": []map[string]any{line("A", 1, 1, vat25)}, "requestId": "not-a-uuid",
	}); res.Status != http.StatusBadRequest {
		t.Errorf("a requestId that is no UUID = %d %s, want 400", res.Status, res.Body)
	}
}
