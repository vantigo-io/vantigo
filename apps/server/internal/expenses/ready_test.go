package expenses_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/expenses"
)

// This file is the one "ready to invoice" rule (invoices work design D3):
// expenses.ready_to_invoice, the SQL function every read that asks calls, and
// invoicedRefusal, its mirror in Go that the manual door and the invoices
// holder judge a line by. Neither carries "not invoiced yet" — each caller
// states it beside the rule — so the holder can name an invoiced line first.

// TestReadyToInvoice_TheFunctionAndInvoicedRefusalAgree holds the two to one
// sentence over every unit status, billable or not, every kind, priced or
// not.
func TestReadyToInvoice_TheFunctionAndInvoicedRefusalAgree(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	priced := "100.00"
	agreed := 0
	for _, status := range []string{"draft", "submitted", "approved", "rejected"} {
		for _, billable := range []bool{false, true} {
			for _, kind := range []string{"outlay", "mileage", "per_diem", "supplier_invoice"} {
				for _, amount := range []*string{nil, &priced} {
					var sql *bool
					if err := d.pool.QueryRow(t.Context(), `SELECT expenses.ready_to_invoice($1, $2, $3, $4::numeric)`,
						status, billable, kind, amount).Scan(&sql); err != nil {
						t.Fatalf("expenses.ready_to_invoice: %v", err)
					}
					goRule := expenses.ReadyToInvoice(status, billable, kind, amount != nil)
					if sql == nil || *sql != goRule {
						t.Errorf("(%s, billable %v, %s, priced %v): the function says %v, invoicedRefusal %v",
							status, billable, kind, amount != nil, sql, goRule)
						continue
					}
					if goRule {
						agreed++
					}
				}
			}
		}
	}
	// Three kinds, approved, billable and priced: anything else means the
	// table above stopped asking the question.
	if agreed != 3 {
		t.Errorf("%d combinations are ready to invoice, want 3", agreed)
	}
}

// TestReadyToInvoice_EveryFormerPlaceStillCounts: the four places the rule
// used to be written out — the list's toInvoice filter and its count, the
// project page's ready count and amount, and the manual stamp — read the same
// fixture exactly as they did before the rule became one function.
func TestReadyToInvoice_EveryFormerPlaceStillCounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve")
	pool := h.Pool()

	ready := seedLine(t, pool, line{billAmount: "1100.00"})
	approvedTrip := seedClaim(t, pool, "approved")
	readyOnATrip := seedLine(t, pool, line{claim: approvedTrip, status: "draft", billAmount: "200.50"})
	submittedTrip := seedClaim(t, pool, "submitted")
	notReady := []int64{
		seedLine(t, pool, line{claim: submittedTrip, status: "draft"}),
		seedLine(t, pool, line{kind: "mileage", unpriced: true, distanceKm: "12.0"}),
		seedLine(t, pool, line{claim: approvedTrip, kind: "per_diem"}),
		seedLine(t, pool, line{notBillable: true}),
		seedLine(t, pool, line{handInvoiced: true}),
		seedLine(t, pool, line{invoiceID: 7, invoiceNumberStamp: 10042}),
		seedLine(t, pool, line{status: "draft"}),
		seedLine(t, pool, line{status: "submitted"}),
	}

	page := listEntries(t, manager, fmt.Sprintf("?projectId=%d&toInvoice=true&pageSize=100", projectKraftVerket))
	got := entryIDs(page)
	slices.Sort(got)
	if want := []int64{ready, readyOnATrip}; !slices.Equal(got, want) {
		t.Errorf("toInvoice lists %v, want %v (not %v)", got, want, notReady)
	}
	if page.Pagination.TotalCount != 2 {
		t.Errorf("toInvoice counts %d, want 2", page.Pagination.TotalCount)
	}

	nok := summaryCurrency(t, getProjectSummary(t, manager, projectKraftVerket), "NOK")
	if nok.ReadyCount != 2 || !sameAmount(nok.ReadyAmount, 1300.50) {
		t.Errorf("ready = %d lines for %v, want 2 for 1300.50", nok.ReadyCount, nok.ReadyAmount)
	}

	for _, id := range []int64{ready, readyOnATrip} {
		before := getEntry(t, manager, id)
		billed := markInvoiced(t, manager, id, invoicedBody(before.Revision, nil))
		if billed.Billing == nil || billed.Billing.Invoice == nil {
			t.Errorf("expense %d after the manual stamp carries %+v, want an invoice", id, billed.Billing)
		}
	}
	for _, id := range notReady[:4] {
		entry := getEntry(t, manager, id)
		if r := manager.Do(http.MethodPost, entryInvoicedPath(id), invoicedBody(entry.Revision, nil)); r.Status != http.StatusBadRequest {
			t.Errorf("the manual stamp of not-ready expense %d: status %d body %s, want 400", id, r.Status, r.Body)
		}
	}
}
