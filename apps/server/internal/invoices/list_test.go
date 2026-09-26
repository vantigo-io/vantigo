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
		ID           int64   `json:"id"`
		Kind         string  `json:"kind"`
		Status       string  `json:"status"`
		Number       *int64  `json:"number"`
		CustomerID   int32   `json:"customerId"`
		CustomerName *string `json:"customerName"`
		IssueDate    *string `json:"issueDate"`
		GrossTotal   float64 `json:"grossTotal"`
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
