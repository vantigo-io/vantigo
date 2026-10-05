package expenses_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is contracts.BillableExpenses (invoices work design D3): the
// lines ready to invoice and not invoiced yet, row by row, for the invoices
// module to build invoices from — on the pool, from this module's own tables.

// billable builds the read the way Compose does, from Deps carrying the pool
// and nothing else: no directory may reach the answer.
func billable(t *testing.T, d workDB) contracts.BillableExpenses {
	t.Helper()
	build := expenses.Module().BillableExpenses
	if build == nil {
		t.Fatal("the expenses module declares no billable expenses read")
	}
	return build(module.Deps{Pool: d.pool})
}

func readBillable(t *testing.T, b contracts.BillableExpenses, req contracts.BillableRequest) contracts.BillableExpensesPage {
	t.Helper()
	page, err := b.BillableExpenses(t.Context(), req)
	if err != nil {
		t.Fatalf("BillableExpenses(%+v): %v", req, err)
	}
	return page
}

func billableIDs(page contracts.BillableExpensesPage) []int64 {
	ids := make([]int64, 0, len(page.Expenses))
	for _, e := range page.Expenses {
		ids = append(ids, e.ID)
	}
	return ids
}

func text(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// TestBillableExpenses_TheSet: exactly the lines ready to invoice and not
// invoiced, project by project and oldest first, every figure the exact text
// the column holds — and the until bound.
func TestBillableExpenses_TheSet(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	euro := int32(projectEuro)
	outlay := seedLine(t, d.pool, line{date: "2026-03-12", gross: "1250.00", vat: "250.00", markup: "10.00", billAmount: "1100.00"})
	mileage := seedLine(t, d.pool, line{kind: "mileage", date: "2026-03-10", gross: "559.30", distanceKm: "47.0",
		billRatePerKm: "12.50", billAmount: "587.50"})
	supplier := seedLine(t, d.pool, line{kind: "supplier_invoice", date: "2026-03-11", supplier: "Rør & Varme AS",
		invoiceNumber: "F-20260918", markup: "15.00", billAmount: "1150.00"})
	trip := seedClaim(t, d.pool, "approved")
	onTrip := seedLine(t, d.pool, line{claim: trip, status: "draft", date: "2026-03-20"})
	inEuro := seedLine(t, d.pool, line{project: &euro, currency: "EUR", billAmount: "99.95"})
	// None of these is billable work.
	seedLine(t, d.pool, line{status: "draft"})
	seedLine(t, d.pool, line{notBillable: true})
	seedLine(t, d.pool, line{claim: trip, kind: "per_diem"})
	seedLine(t, d.pool, line{kind: "mileage", unpriced: true, distanceKm: "3.0"})
	seedLine(t, d.pool, line{handInvoiced: true})
	seedLine(t, d.pool, line{invoiceID: 7, invoiceNumberStamp: 10042})
	seedLine(t, d.pool, line{claim: seedClaim(t, d.pool, "submitted"), status: "draft"})
	seedLine(t, d.pool, line{project: ptr(int32(projectInternal))})

	b := billable(t, d)
	page := readBillable(t, b, contracts.BillableRequest{ProjectIDs: []int32{projectEuro, projectKraftVerket}})
	if want := []int64{mileage, supplier, outlay, onTrip, inEuro}; !slices.Equal(billableIDs(page), want) || page.More {
		t.Fatalf("the set = %v (more %v), want %v by project then date", billableIDs(page), page.More, want)
	}
	byID := map[int64]contracts.BillableExpense{}
	for _, e := range page.Expenses {
		byID[e.ID] = e
	}
	o := byID[outlay]
	if o.Kind != "outlay" || o.Revision != 1 || o.ProjectID != projectKraftVerket || o.ClaimID != nil ||
		!o.Date.Equal(time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)) || o.Description != "Stillas" ||
		o.NetAmount != "1000.00" || text(o.MarkupPercent) != "10.00" || o.BillAmount != "1100.00" || o.Currency != "NOK" ||
		o.DistanceKm != nil || o.BillRatePerKm != nil || o.Supplier != "" || o.SupplierInvoiceNumber != "" || o.SupplierInvoiceRebilled {
		t.Errorf("the outlay = %+v", o)
	}
	m := byID[mileage]
	if text(m.DistanceKm) != "47.0" || text(m.BillRatePerKm) != "12.50" || m.BillAmount != "587.50" || m.MarkupPercent != nil {
		t.Errorf("the mileage = %+v (distance %s, rate %s)", m, text(m.DistanceKm), text(m.BillRatePerKm))
	}
	s := byID[supplier]
	if s.Kind != "supplier_invoice" || s.Supplier != "Rør & Varme AS" || s.SupplierInvoiceNumber != "F-20260918" || s.BillAmount != "1150.00" {
		t.Errorf("the supplier invoice = %+v", s)
	}
	if c := byID[onTrip]; c.ClaimID == nil || *c.ClaimID != *trip {
		t.Errorf("the trip's line carries claim %v, want %d", c.ClaimID, *trip)
	}
	if e := byID[inEuro]; e.Currency != "EUR" || e.BillAmount != "99.95" {
		t.Errorf("the euro line = %+v", e)
	}

	until := readBillable(t, b, contracts.BillableRequest{
		ProjectIDs: []int32{projectKraftVerket}, Until: time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC),
	})
	if want := []int64{mileage, supplier}; !slices.Equal(billableIDs(until), want) {
		t.Errorf("until the 11th = %v, want %v (the day itself included)", billableIDs(until), want)
	}

	if _, err := b.BillableExpenses(t.Context(), contracts.BillableRequest{}); err == nil {
		t.Error("a request naming nothing was answered, want the Validate refusal")
	}
}

// TestBillableExpenses_More: past MaxBillableRows the page stops there and
// says there was more.
func TestBillableExpenses_More(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	d.exec(t, `
		INSERT INTO expenses.entries (user_id, created_by_user_id, kind, entry_date, description, currency,
		    gross_amount, paid_by, project_id, billable, bill_amount, status, created_at, updated_at)
		SELECT gen_random_uuid(), gen_random_uuid(), 'outlay', DATE '2026-03-01' + (n % 28), 'Bulk', 'NOK',
		    100.00, 'employee', $1, true, 110.00, 'approved', now(), now()
		FROM generate_series(1, $2::int) n`, int32(projectKraftVerket), contracts.MaxBillableRows+1)
	page := readBillable(t, billable(t, d), contracts.BillableRequest{ProjectIDs: []int32{projectKraftVerket}})
	if len(page.Expenses) != contracts.MaxBillableRows || !page.More {
		t.Errorf("%d rows, more %v; want %d and more", len(page.Expenses), page.More, contracts.MaxBillableRows)
	}
	exactly := readBillable(t, billable(t, d), contracts.BillableRequest{
		ProjectIDs: []int32{projectKraftVerket}, Until: time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC),
	})
	if exactly.More || len(exactly.Expenses) >= contracts.MaxBillableRows {
		t.Errorf("a bounded read of fewer rows says more %v with %d rows", exactly.More, len(exactly.Expenses))
	}
}

// TestBillableExpenses_ByIDs: exactly the named lines still billable, in id
// order — one since invoiced, by hand or by an invoice, or no longer ready, is
// absent, never an error.
func TestBillableExpenses_ByIDs(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	a := seedLine(t, d.pool, line{})
	b := seedLine(t, d.pool, line{project: ptr(int32(projectEuro)), currency: "EUR"})
	handed := seedLine(t, d.pool, line{handInvoiced: true})
	stamped := seedLine(t, d.pool, line{invoiceID: 7, invoiceNumberStamp: 10042})
	unapproved := seedLine(t, d.pool, line{status: "submitted"})
	noProject := seedLine(t, d.pool, line{noProject: true})
	page := readBillable(t, billable(t, d), contracts.BillableRequest{
		IDs: []int64{stamped, b, handed, a, unapproved, noProject, 987654},
	})
	if want := []int64{a, b}; !slices.Equal(billableIDs(page), want) || page.More {
		t.Errorf("by ids = %v (more %v), want %v", billableIDs(page), page.More, want)
	}
}

// TestBillableExpenses_SupplierInvoiceRebilled (design D15): a supplier
// invoice whose supplier — trimmed, case-folded — and number another supplier
// invoice already invoiced carries the flag; a different number, or a twin
// nobody has invoiced, does not.
func TestBillableExpenses_SupplierInvoiceRebilled(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	seedLine(t, d.pool, line{kind: "supplier_invoice", supplier: "Rør & Varme AS", invoiceNumber: "F-1", handInvoiced: true})
	again := seedLine(t, d.pool, line{kind: "supplier_invoice", supplier: "  rør & varme as ", invoiceNumber: "F-1"})
	other := seedLine(t, d.pool, line{kind: "supplier_invoice", supplier: "Rør & Varme AS", invoiceNumber: "F-2"})
	twin := seedLine(t, d.pool, line{kind: "supplier_invoice", supplier: "Elektro AS", invoiceNumber: "E-9"})
	twinToo := seedLine(t, d.pool, line{kind: "supplier_invoice", supplier: "Elektro AS", invoiceNumber: "E-9"})
	outlay := seedLine(t, d.pool, line{supplier: "Rør & Varme AS", invoiceNumber: "F-1"})
	page := readBillable(t, billable(t, d), contracts.BillableRequest{IDs: []int64{again, other, twin, twinToo, outlay}})
	var flagged []string
	for _, e := range page.Expenses {
		if e.SupplierInvoiceRebilled {
			flagged = append(flagged, fmt.Sprint(e.ID))
		}
	}
	if want := fmt.Sprint(again); strings.Join(flagged, ",") != want {
		t.Errorf("flagged = %v, want only %s", flagged, want)
	}
}
