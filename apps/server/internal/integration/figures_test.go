package integration_test

import (
	"fmt"
	"math"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// One fixture, four surfaces, real modules on both sides of the contract.
//
// The delivery's whole promise is that what a project's expenses cost and bill
// is one set of figures wherever it is read: the Expenses tab's own summary,
// the Economy tab's expenses block (through contracts.ProjectExpenses), the
// portfolio row and its totals, and the list of lines waiting to be invoiced.
// Each module's own suite proves its half against the other half's imitation.
// This proves the halves against each other.
//
// The fixture is built through the real doors — record, price, submit,
// approve, reject, invoice — so what is being compared is what the module
// actually writes, not what a test inserted. The expected figures are then
// derived from the entries' own responses rather than written out as
// constants, so the assertions stay exact without depending on the rate table
// a per diem day is priced from.

// same reports whether two money figures agree to the cent. Every surface
// publishes JSON numbers at two decimals; comparing at that scale is comparing
// what a person would read.
func same(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// entry is the part of ExpensesEntryResponse the fixture reads back.
type entry struct {
	Id        int64   `json:"id"`
	ClaimId   *int64  `json:"claimId"`
	Kind      string  `json:"kind"`
	Currency  string  `json:"currency"`
	NetAmount float64 `json:"netAmount"`
	Billable  bool    `json:"billable"`
	Status    string  `json:"status"`
	Revision  int32   `json:"revision"`
	Billing   *struct {
		BillAmount float64 `json:"billAmount"`
		Invoice    *struct {
			At string `json:"at"`
		} `json:"invoice"`
	} `json:"billing"`
}

// bucket is what every surface calls one bucket of expenses: how many lines,
// what they cost, what they bill.
type bucket struct {
	Count      int32   `json:"count"`
	Cost       float64 `json:"cost"`
	BillAmount float64 `json:"billAmount"`
}

// want is the figures this fixture implies, added up from the entries
// themselves.
type want struct {
	approved, submitted, draft bucket
	ready, invoiced            struct {
		count  int32
		amount float64
	}
	unpriced int32
}

// total is the three buckets as one, which is what every surface publishes
// beside them and what a consumer is told to read.
func (w want) total() bucket {
	return bucket{
		Count:      w.approved.Count + w.submitted.Count + w.draft.Count,
		Cost:       w.approved.Cost + w.submitted.Cost + w.draft.Cost,
		BillAmount: w.approved.BillAmount + w.submitted.BillAmount + w.draft.BillAmount,
	}
}

// add folds one recorded line into the expectation, by exactly the rules the
// delivery states: the unit's status decides the bucket, rejected counts as
// draft, cost is the net, only a billable line with a price bills anything,
// and ready is approved ∧ billable ∧ priced ∧ not invoiced ∧ not a per diem
// day.
func (w *want) add(e entry) {
	b := &w.draft
	switch e.Status {
	case "approved":
		b = &w.approved
	case "submitted":
		b = &w.submitted
	}
	b.Count++
	b.Cost += e.NetAmount

	priced := e.Billable && e.Billing != nil && e.Billing.BillAmount != 0
	if priced {
		b.BillAmount += e.Billing.BillAmount
	}
	if e.Billable && !priced && e.Kind != "per_diem" {
		w.unpriced++
	}
	invoiced := e.Billing != nil && e.Billing.Invoice != nil
	switch {
	case invoiced:
		w.invoiced.count++
		w.invoiced.amount += e.Billing.BillAmount
	case e.Status == "approved" && priced && e.Kind != "per_diem":
		w.ready.count++
		w.ready.amount += e.Billing.BillAmount
	}
}

// TestFigures_OneProjectReadsTheSameOnEverySurface is the test the branch was
// missing: real projects + real time + real expenses, one fixture, every
// published figure checked against every other.
func TestFigures_OneProjectReadsTheSameOnEverySurface(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modProjects, modTime, modExpenses)
	admin, _ := signInAdmin(t, h)
	project := createProject(t, admin)

	nok, eur := buildFixture(t, h, admin, project.Id)

	// Surface 1 — the Expenses tab's own summary.
	summary := readSummary(t, admin, project.Id)
	if summary.ProjectCurrency == nil || *summary.ProjectCurrency != "NOK" {
		t.Fatalf("the summary names %v as the project's currency, want NOK", summary.ProjectCurrency)
	}
	gotNOK := summary.currency(t, "NOK")
	gotEUR := summary.currency(t, "EUR")
	checkBucket(t, "summary NOK approved", gotNOK.Approved, nok.approved)
	checkBucket(t, "summary NOK submitted", gotNOK.Submitted, nok.submitted)
	checkBucket(t, "summary NOK draft", gotNOK.Draft, nok.draft)
	checkBucket(t, "summary NOK total", gotNOK.Total, nok.total())
	checkReady(t, "summary NOK", gotNOK.ReadyCount, gotNOK.ReadyAmount, nok)
	if gotNOK.InvoicedCount != nok.invoiced.count || !same(gotNOK.InvoicedAmount, nok.invoiced.amount) {
		t.Errorf("summary NOK invoiced = %d/%v, want %d/%v",
			gotNOK.InvoicedCount, gotNOK.InvoicedAmount, nok.invoiced.count, nok.invoiced.amount)
	}
	if gotNOK.UnpricedCount != nok.unpriced {
		t.Errorf("summary NOK unpricedCount = %d, want %d", gotNOK.UnpricedCount, nok.unpriced)
	}
	checkBucket(t, "summary EUR approved", gotEUR.Approved, eur.approved)
	checkReady(t, "summary EUR", gotEUR.ReadyCount, gotEUR.ReadyAmount, eur)

	// Surface 2 — the Economy tab, which reads the provider across the module
	// boundary. Its own-currency figures are the summary's NOK entry; every
	// other currency is reported as what it is and never folded in.
	economy := readEconomy(t, admin, project.Id)
	if !economy.ExpenseTracking || economy.Expenses == nil {
		t.Fatal("the economy reports no expenses block in an installation that composes the expenses module")
	}
	ex := economy.Expenses
	checkBucket(t, "economy approved", ex.Approved.asBucket(), nok.approved)
	checkBucket(t, "economy submitted", ex.Submitted.asBucket(), nok.submitted)
	checkBucket(t, "economy draft", ex.Draft.asBucket(), nok.draft)
	if !same(ex.TotalCost, nok.total().Cost) || !same(ex.TotalAmount, nok.total().BillAmount) {
		t.Errorf("economy totals = %v cost / %v amount, want %v / %v",
			ex.TotalCost, ex.TotalAmount, nok.total().Cost, nok.total().BillAmount)
	}
	if ex.ReadyCount != nok.ready.count || !same(ex.ReadyAmount, nok.ready.amount) {
		t.Errorf("economy ready = %d/%v, want %d/%v",
			ex.ReadyCount, ex.ReadyAmount, nok.ready.count, nok.ready.amount)
	}
	if ex.InvoicedCount != nok.invoiced.count || !same(ex.InvoicedAmount, nok.invoiced.amount) {
		t.Errorf("economy invoiced = %d/%v, want %d/%v",
			ex.InvoicedCount, ex.InvoicedAmount, nok.invoiced.count, nok.invoiced.amount)
	}
	if ex.UnpricedCount != nok.unpriced {
		t.Errorf("economy unpricedCount = %d, want %d", ex.UnpricedCount, nok.unpriced)
	}
	if ex.LastEntryDate == nil || *ex.LastEntryDate != summaryDate(t, summary) {
		t.Errorf("economy lastEntryDate = %v, the summary's = %v", ex.LastEntryDate, summary.LastEntryDate)
	}
	if len(ex.OtherCurrencies) != 1 || ex.OtherCurrencies[0].Currency != "EUR" {
		t.Fatalf("economy otherCurrencies = %+v, want the one EUR entry", ex.OtherCurrencies)
	}
	other := ex.OtherCurrencies[0]
	if other.Count != eur.total().Count || !same(other.Cost, eur.total().Cost) ||
		!same(other.Amount, eur.total().BillAmount) || !same(other.ReadyAmount, eur.ready.amount) {
		t.Errorf("economy's EUR entry = %+v, want %d lines costing %v, billing %v, ready %v",
			other, eur.total().Count, eur.total().Cost, eur.total().BillAmount, eur.ready.amount)
	}

	// X12: none of it reaches the budget. The project has no hours and no
	// budget, and 3 000-odd kroner of expense bill must not move either.
	if economy.BudgetUsed != 0 || economy.OverBudget {
		t.Errorf("budgetUsed = %v / overBudget = %v, want a project with no work to be at nothing",
			economy.BudgetUsed, economy.OverBudget)
	}

	// Surface 3 — the portfolio row and its totals.
	portfolio := readPortfolio(t, admin)
	row := portfolio.row(t, project.Id)
	if row.ReadyExpenseCount != nok.ready.count || !same(row.ReadyExpenseAmount, nok.ready.amount) {
		t.Errorf("portfolio row ready-expense = %d/%v, want the project's own currency's %d/%v",
			row.ReadyExpenseCount, row.ReadyExpenseAmount, nok.ready.count, nok.ready.amount)
	}
	// Nothing is billed by a milestone here, so the row's combined figure is
	// the expense half alone.
	if !same(row.ReadyTotalAmount, nok.ready.amount) {
		t.Errorf("portfolio row readyTotalAmount = %v, want %v with no milestone ready",
			row.ReadyTotalAmount, nok.ready.amount)
	}
	if portfolio.Totals.ReadyExpenseCount != nok.ready.count {
		t.Errorf("portfolio totals readyExpenseCount = %d, want %d",
			portfolio.Totals.ReadyExpenseCount, nok.ready.count)
	}
	var totalNOK float64
	for _, amount := range portfolio.Totals.ReadyAmounts {
		if amount.Currency == "NOK" {
			totalNOK = amount.ExpenseAmount
		}
		if amount.Currency == "EUR" {
			t.Errorf("the portfolio totals carry a EUR entry (%+v): the row is the project's own currency only", amount)
		}
	}
	if !same(totalNOK, nok.ready.amount) {
		t.Errorf("portfolio totals NOK expenseAmount = %v, want %v", totalNOK, nok.ready.amount)
	}

	// Surface 4 — the lines the figure counts. The list is not split by
	// currency, so it holds both currencies' ready lines.
	var page struct {
		Data       []entry `json:"data"`
		Pagination struct {
			TotalCount int32 `json:"totalCount"`
		} `json:"pagination"`
	}
	okJSON(t, admin, http.MethodGet,
		fmt.Sprintf("%s?projectId=%d&toInvoice=true", expensesEntries, project.Id), nil, &page)
	if wantRows := nok.ready.count + eur.ready.count; page.Pagination.TotalCount != wantRows {
		t.Fatalf("toInvoice listed %d rows, want %d — the two currencies' ready counts",
			page.Pagination.TotalCount, wantRows)
	}
	var listed float64
	for _, e := range page.Data {
		if e.Currency == "NOK" {
			listed += e.Billing.BillAmount
		}
	}
	if !same(listed, nok.ready.amount) {
		t.Errorf("the listed NOK lines bill %v, want the readyAmount %v every surface reports",
			listed, nok.ready.amount)
	}
}

// buildFixture records the branch's worked example through the real doors and
// answers what the figures should be, per currency, derived from the entries
// themselves.
//
// It covers every rule the figures rest on: a travel claim whose lines are
// judged by the claim (including a per diem day, which bills nobody), a
// company-paid outlay, a billable line nobody has priced, an invoiced line, a
// rejected one (which counts as draft), a submitted one, a draft, and a
// receipt in a currency the project is not in.
func buildFixture(t *testing.T, h *modtest.Harness, c *modtest.Client, projectID int32) (nok, eur want) {
	t.Helper()

	// A trip, its lines, and the approval that decides their bucket.
	var claim struct {
		Id       int64 `json:"id"`
		Revision int32 `json:"revision"`
	}
	okJSON(t, c, http.MethodPost, expensesClaims, map[string]any{
		"purpose":     "Montasje hos kunden",
		"departureAt": "2026-03-09T07:00:00Z",
		"returnAt":    "2026-03-11T16:00:00Z",
		"projectId":   projectID,
	}, &claim)

	perDiem := record(t, c, map[string]any{
		"kind": "per_diem", "entryDate": "2026-03-10", "perDiemType": "day_6_12",
		"claimId": claim.Id,
	})
	tripOutlay := record(t, c, map[string]any{
		"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-10", "description": "Kabel og kontakter",
		"paidBy": "employee", "currency": "NOK", "grossAmount": 1250.00, "vatAmount": 250.00,
		"claimId": claim.Id, "billable": true, "markupPercent": 10.0,
	})
	move(t, c, expensesSubmit, map[string]any{"claimIds": []int64{claim.Id}})
	move(t, c, expensesApprove, map[string]any{"claimIds": []int64{claim.Id}})

	// The standalone ones. Each is recorded, then moved to the status its
	// bucket needs, through the flow's own doors.
	companyPaid := record(t, c, map[string]any{
		"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-12", "description": "Programvare",
		"paidBy": "company", "currency": "NOK", "grossAmount": 625.00, "vatAmount": 125.00,
		"projectId": projectID,
	})
	// Billable mileage with no customer rate: the installation ships none —
	// what a company charges per kilometre is its own commercial figure — so
	// the line is stored billable with no bill amount at all. That is the
	// "unpriced" case: a missing price, never a price of nothing.
	//
	// It is recorded by the employee whose trip it was, because that is the
	// only way the module will store one: a caller who *could* name the
	// missing rate is refused the save and told to, and the admin here is
	// exactly such a caller. The employee is a member of the project, which
	// is what booking on it takes.
	employee, employeeID := h.SignInUser(t, "expenses:access")
	addRole(t, h, projectID, employeeID, roleMember)
	unpriced := record(t, employee, map[string]any{
		"kind": "mileage", "entryDate": "2026-03-13", "description": "Til anlegget",
		"distanceKm": 50.0, "projectId": projectID, "billable": true,
	})
	// One standalone line that stays ready, so "ready" is not only ever a
	// trip's line and the list has to hold both shapes.
	readyLine := record(t, c, map[string]any{
		"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-18", "description": "Klar til fakturering",
		"paidBy": "employee", "currency": "NOK", "grossAmount": 440.00, "vatAmount": 40.00,
		"projectId": projectID, "billable": true, "markupPercent": 25.0,
	})
	toInvoice := record(t, c, map[string]any{
		"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-14", "description": "Fakturert",
		"paidBy": "employee", "currency": "NOK", "grossAmount": 500.00, "vatAmount": 100.00,
		"projectId": projectID, "billable": true, "markupPercent": 10.0,
	})
	euro := record(t, c, map[string]any{
		"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-20", "description": "Reservedel fra utlandet",
		"paidBy": "employee", "currency": "EUR", "grossAmount": 100.00,
		"projectId": projectID, "billable": true, "markupPercent": 20.0,
	})
	rejected := record(t, c, map[string]any{
		"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-15", "description": "Avvist",
		"paidBy": "employee", "currency": "NOK", "grossAmount": 300.00, "vatAmount": 60.00,
		"projectId": projectID, "billable": true, "markupPercent": 10.0,
	})
	submitted := record(t, c, map[string]any{
		"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-16", "description": "Sendt inn",
		"paidBy": "employee", "currency": "NOK", "grossAmount": 125.00, "vatAmount": 25.00,
		"projectId": projectID,
	})
	draft := record(t, c, map[string]any{
		"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-17", "description": "Kladd",
		"paidBy": "employee", "currency": "NOK", "grossAmount": 62.50, "vatAmount": 12.50,
		"projectId": projectID,
	})

	// The employee submits their own; the admin submits the rest and decides
	// on all of them, which is the flow as it is actually used.
	move(t, employee, expensesSubmit, map[string]any{"entryIds": []int64{unpriced.Id}})
	approved := []int64{companyPaid.Id, unpriced.Id, readyLine.Id, toInvoice.Id, euro.Id}
	move(t, c, expensesSubmit, map[string]any{
		"entryIds": []int64{companyPaid.Id, readyLine.Id, toInvoice.Id, euro.Id, rejected.Id, submitted.Id},
	})
	move(t, c, expensesApprove, map[string]any{"entryIds": approved})
	move(t, c, expensesReject, map[string]any{"entryIds": []int64{rejected.Id}, "reason": "Mangler kvittering"})

	// One of the approved lines goes out on an invoice, through the door that
	// decides what may: the stamp is what moves it from ready to invoiced.
	billed := reread(t, c, toInvoice.Id)
	okJSON(t, c, http.MethodPost, fmt.Sprintf("%s/%d/invoiced", expensesEntries, billed.Id),
		map[string]any{"revision": billed.Revision, "reference": "F-2026-118"}, nil)

	// The expectation is read back from the module rather than assumed: every
	// line as it now stands, folded by the delivery's own rules.
	for _, id := range []int64{
		perDiem.Id, tripOutlay.Id, companyPaid.Id, unpriced.Id, readyLine.Id,
		toInvoice.Id, euro.Id, rejected.Id, submitted.Id, draft.Id,
	} {
		e := reread(t, c, id)
		if e.Currency == "EUR" {
			eur.add(e)
			continue
		}
		nok.add(e)
	}

	// A guard on the fixture itself: if a later change stopped it covering the
	// cases it is here to cover, the assertions above would still pass over a
	// fixture that proves less. These are the ones that make it worth running.
	switch {
	case nok.ready.count != 2:
		t.Fatalf("the fixture has %d NOK lines ready to invoice, want 2 (one of them a trip's)", nok.ready.count)
	case nok.invoiced.count != 1:
		t.Fatalf("the fixture has %d invoiced lines, want 1", nok.invoiced.count)
	case nok.unpriced != 1:
		t.Fatalf("the fixture has %d unpriced billable lines, want 1", nok.unpriced)
	case nok.draft.Count != 2:
		t.Fatalf("the fixture's draft bucket holds %d lines, want 2 (a draft and a rejected one)", nok.draft.Count)
	case nok.submitted.Count != 1:
		t.Fatalf("the fixture's submitted bucket holds %d lines, want 1", nok.submitted.Count)
	case eur.ready.count != 1:
		t.Fatalf("the fixture has %d EUR lines ready, want 1", eur.ready.count)
	}
	return nok, eur
}

// record posts one expense and answers it as created.
func record(t *testing.T, c *modtest.Client, body map[string]any) entry {
	t.Helper()
	var e entry
	okJSON(t, c, http.MethodPost, expensesEntries, body, &e)
	return e
}

// reread reads one expense as it now stands.
func reread(t *testing.T, c *modtest.Client, id int64) entry {
	t.Helper()
	var e entry
	okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/%d", expensesEntries, id), nil, &e)
	return e
}

// move posts one of the flow's batch doors.
func move(t *testing.T, c *modtest.Client, path string, body map[string]any) {
	t.Helper()
	okJSON(t, c, http.MethodPost, path, body, nil)
}

// checkBucket fails the test unless a published bucket is the expected one.
func checkBucket(t *testing.T, name string, got, wantBucket bucket) {
	t.Helper()
	if got.Count != wantBucket.Count || !same(got.Cost, wantBucket.Cost) ||
		!same(got.BillAmount, wantBucket.BillAmount) {
		t.Errorf("%s = %d lines / %v cost / %v bill, want %d / %v / %v",
			name, got.Count, got.Cost, got.BillAmount,
			wantBucket.Count, wantBucket.Cost, wantBucket.BillAmount)
	}
}

// checkReady fails the test unless a surface's ready figures are the fixture's.
func checkReady(t *testing.T, name string, count int32, amount float64, w want) {
	t.Helper()
	if count != w.ready.count || !same(amount, w.ready.amount) {
		t.Errorf("%s ready = %d/%v, want %d/%v", name, count, amount, w.ready.count, w.ready.amount)
	}
}
