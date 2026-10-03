package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

type listJSON struct {
	Data []struct {
		ID           int64    `json:"id"`
		Kind         string   `json:"kind"`
		Status       string   `json:"status"`
		Number       *int64   `json:"number"`
		CustomerID   int32    `json:"customerId"`
		CustomerName *string  `json:"customerName"`
		IssueDate    *string  `json:"issueDate"`
		GrossTotal   float64  `json:"grossTotal"`
		State        string   `json:"state"`
		OpenAmount   *float64 `json:"openAmount"`
	} `json:"data"`
	Pagination struct {
		Page        int32 `json:"page"`
		PageSize    int32 `json:"pageSize"`
		TotalCount  int32 `json:"totalCount"`
		HasNextPage bool  `json:"hasNextPage"`
	} `json:"pagination"`
}

func list(t *testing.T, h *harness, query string) listJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicesPath+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices%s = %d %s", query, res.Status, res.Body)
	}
	var l listJSON
	res.JSON(&l)
	return l
}

func ids(l listJSON) []int64 {
	var out []int64
	for _, d := range l.Data {
		out = append(out, d.ID)
	}
	return out
}

// plantIssuedFor writes an issued document for customer with the buyer name it
// was issued to.
func plantIssuedFor(t *testing.T, h *harness, number int64, issueDate string, customer int32, buyer, kind string) int64 {
	t.Helper()
	var credits *int64
	if kind == "credit_note" {
		credits = ptr(plantIssuedFor(t, h, number+100000, issueDate, customer, buyer, "invoice"))
	}
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, credits_invoice_id, issue_date, due_date, exchange_rate_date, seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ($1::text, 'issued', $2, $3, $4, $5::date, CASE WHEN $1::text = 'invoice' THEN $5::date + 14 END, $5::date, 'Selger AS', $6, 100, now(), $7, now(), now())
		RETURNING id`, kind, number, customer, credits, issueDate, buyer, uuid.New())
}

func ptr[T any](v T) *T { return &v }

// Paging is every module list's: {data, pagination}, 25 by default and at
// most 100, drafts first and then by number descending, stable across pages.
func TestList_PagingAndOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var issued []int64
	for n := int64(1); n <= 30; n++ {
		issued = append(issued, plantIssuedFor(t, h, n, "2026-09-01", customerAcme, "Acme AS", "invoice"))
	}
	firstDraft := createDraft(t, h, draftBody(customerAcme)).ID
	secondDraft := createDraft(t, h, draftBody(customerPerson)).ID

	page1 := list(t, h, "")
	if len(page1.Data) != 25 || page1.Pagination.TotalCount != 32 || !page1.Pagination.HasNextPage || page1.Pagination.PageSize != 25 {
		t.Fatalf("page 1 = %d rows, %+v; want 25 of 32", len(page1.Data), page1.Pagination)
	}
	if got := ids(page1)[:3]; !slices.Equal(got, []int64{secondDraft, firstDraft, issued[29]}) {
		t.Errorf("first rows = %v, want the drafts newest first, then number 30", got)
	}
	if page1.Data[0].CustomerName == nil || *page1.Data[0].CustomerName != "Kari Nordmann" {
		t.Errorf("a draft's name = %v, want the customer's current name", page1.Data[0].CustomerName)
	}
	page2 := list(t, h, "?page=2")
	want := []int64{}
	for i := 6; i >= 0; i-- {
		want = append(want, issued[i])
	}
	if !slices.Equal(ids(page2), want) {
		t.Errorf("page 2 = %v, want numbers 7..1", ids(page2))
	}
	if all := list(t, h, "?pageSize=100"); len(all.Data) != 32 {
		t.Errorf("pageSize 100 = %d rows", len(all.Data))
	}
	for _, bad := range []string{"?pageSize=101", "?page=0", "?status=paid", "?kind=receipt", "?from=2026-09-02&to=2026-09-01"} {
		if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicesPath+bad, nil); res.Status != http.StatusBadRequest {
			t.Errorf("GET /invoices%s = %d, want 400", bad, res.Status)
		}
	}
}

// Every filter, and search by number and by buyer name — a draft is found by
// its customer, not by search.
func TestList_Filters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	early := plantIssuedFor(t, h, 1, "2026-08-15", customerAcme, "Acme AS", "invoice")
	late := plantIssuedFor(t, h, 2, "2026-09-05", customerPerson, "Kari Nordmann", "invoice")
	credit := plantIssuedFor(t, h, 3, "2026-09-06", customerAcme, "Acme AS", "credit_note")
	draft := createDraft(t, h, draftBody(customerPerson)).ID

	for _, c := range []struct {
		query string
		want  []int64
	}{
		{"?status=draft", []int64{draft}},
		{"?status=issued&kind=credit_note", []int64{credit}},
		{fmt.Sprintf("?customerId=%d", customerPerson), []int64{draft, late}},
		{"?from=2026-09-01&to=2026-09-05", []int64{late}},
		{"?search=2", []int64{late}},
		{"?search=kari", []int64{late}},
		{"?search=ACME&kind=invoice&to=2026-08-31", []int64{early}},
		{"?search=100%25", []int64{}},
	} {
		got := ids(list(t, h, c.query))
		if got == nil {
			got = []int64{}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("GET /invoices%s = %v, want %v", c.query, got, c.want)
		}
	}
}

// plantDocument writes an issued document issued on 2026-09-01 for customer
// with gross: an invoice due on due, or — when credits is set — a credit note
// of that invoice, which has no due date.
func plantDocument(t *testing.T, h *harness, number int64, customer int32, credits *int64, gross, due string) int64 {
	t.Helper()
	kind, dueDate := "invoice", &due
	if credits != nil {
		kind, dueDate = "credit_note", nil
	}
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, credits_invoice_id, issue_date, due_date, exchange_rate_date, seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ($1::text, 'issued', $2, $3, $4, '2026-09-01', $5::date, '2026-09-01', 'Selger AS', 'Kunde AS', $6::numeric, now(), $7, now(), now())
		RETURNING id`, kind, number, customer, credits, dueDate, gross, uuid.New())
}

// plantPayment registers amount against an issued invoice, paid on paidOn,
// by SQL — what the payment endpoint will write — and answers its id.
func plantPayment(t *testing.T, h *harness, invoiceID int64, amount, paidOn string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at)
		VALUES ($1, $2::date, $3::numeric, 'NOK', $4, now())
		RETURNING id`, invoiceID, paidOn, amount, uuid.New())
}

// removePayment removes a planted registration with reason, as the removal
// will.
func removePayment(t *testing.T, h *harness, paymentID int64, reason string) {
	t.Helper()
	h.Exec(t, `UPDATE invoices.payments SET removed_at = now(), removed_by_user_id = $2, removal_reason = $3 WHERE id = $1`,
		paymentID, uuid.New(), reason)
}

// The list's state filter (D3): each of the five states returns exactly its
// invoices, judged by the SQL function with today the Oslo business day
// (2026-09-12 on the fixed clock); it combines with kind, customerId, status
// and search, and pages with a total that agrees; a draft and a credit note
// never match one; an unknown state is a 400. Every item answers its state,
// and an issued invoice its open amount — gross less what is credited and
// what is live paid, so a payment on a credited invoice is a negative open
// amount, the state still credited.
func TestList_TheStateFilterAndTheOpenAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	open := plantDocument(t, h, 1, customerAcme, nil, "1000", "2026-09-26")
	removePayment(t, h, plantPayment(t, h, open, "500", "2026-09-05"), "Feil faktura")
	partial := plantDocument(t, h, 2, customerAcme, nil, "1000", "2026-09-26")
	plantPayment(t, h, partial, "300", "2026-09-05")
	overdue := plantDocument(t, h, 3, customerAcme, nil, "1000", "2026-09-11")
	overduePartial := plantDocument(t, h, 4, customerPerson, nil, "1000", "2026-09-11")
	plantPayment(t, h, overduePartial, "300", "2026-09-05")
	paid := plantDocument(t, h, 5, customerAcme, nil, "1000", "2026-09-11")
	plantPayment(t, h, paid, "999.99", "2026-09-05")
	plantPayment(t, h, paid, "0.01", "2026-09-06")
	credited := plantDocument(t, h, 6, customerAcme, nil, "1000", "2026-09-11")
	creditNote := plantDocument(t, h, 7, customerAcme, &credited, "1000", "")
	plantPayment(t, h, credited, "200", "2026-09-05")
	draft := createDraft(t, h, draftBody(customerAcme)).ID

	for _, c := range []struct {
		query string
		want  []int64
	}{
		{"?state=open", []int64{open}},
		{"?state=partially_paid", []int64{partial}},
		{"?state=overdue", []int64{overduePartial, overdue}},
		{"?state=paid", []int64{paid}},
		{"?state=credited", []int64{credited}},
		{"?state=overdue&kind=invoice", []int64{overduePartial, overdue}},
		{"?state=credited&kind=credit_note", []int64{}},
		{fmt.Sprintf("?state=overdue&customerId=%d", customerPerson), []int64{overduePartial}},
		{fmt.Sprintf("?state=open&customerId=%d", customerPerson), []int64{}},
		{"?state=open&status=draft", []int64{}},
		{"?state=open&status=issued", []int64{open}},
		{"?state=overdue&search=4", []int64{overduePartial}},
		{"?state=open&search=4", []int64{}},
		{"?state=credited&search=kunde", []int64{credited}},
	} {
		got := ids(list(t, h, c.query))
		if got == nil {
			got = []int64{}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("GET /invoices%s = %v, want %v", c.query, got, c.want)
		}
	}

	page := list(t, h, "?state=overdue&pageSize=1")
	if len(page.Data) != 1 || page.Pagination.TotalCount != 2 || !page.Pagination.HasNextPage {
		t.Errorf("?state=overdue&pageSize=1 = %d rows, %+v; want 1 of 2", len(page.Data), page.Pagination)
	}

	wantState := map[int64]string{
		open: "open", partial: "partially_paid", overdue: "overdue", overduePartial: "overdue",
		paid: "paid", credited: "credited", creditNote: "issued", draft: "draft",
	}
	wantOpen := map[int64]float64{open: 1000, partial: 700, overdue: 1000, overduePartial: 700, paid: 0, credited: -200}
	all := list(t, h, "")
	if len(all.Data) != len(wantState) {
		t.Fatalf("the whole list = %v, want %d rows", ids(all), len(wantState))
	}
	for _, d := range all.Data {
		if d.State != wantState[d.ID] {
			t.Errorf("document %d's state = %q, want %q", d.ID, d.State, wantState[d.ID])
		}
		want, isInvoice := wantOpen[d.ID]
		switch {
		case !isInvoice && d.OpenAmount != nil:
			t.Errorf("document %d (%s %s) carries openAmount %v, want none", d.ID, d.Status, d.Kind, *d.OpenAmount)
		case isInvoice && (d.OpenAmount == nil || *d.OpenAmount != want):
			t.Errorf("document %d's openAmount = %v, want %v", d.ID, money(d.OpenAmount), want)
		}
	}

	for _, bad := range []string{"bogus", "draft", "issued", "partiallyPaid"} {
		res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicesPath+"?state="+bad, nil)
		if res.Status != http.StatusBadRequest {
			t.Errorf("GET /invoices?state=%s = %d, want 400", bad, res.Status)
			continue
		}
		want := fmt.Sprintf("'state' must be one of open, partially_paid, overdue, paid or credited, but was '%s'.", bad)
		if p := problemOf(t, res); p.Detail != want {
			t.Errorf("GET /invoices?state=%s = %q, want %q", bad, p.Detail, want)
		}
	}
}
