package expenses_test

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the Expenses tab on the project page, from the server's side:
// GET /projects/{projectId}/summary — what a project's expenses cost and bill
// — and the toInvoice filter on GET /entries, which is the list behind the
// summary's "ready to invoice" figure.
//
// The two are tested together on purpose. They answer the same question at two
// grains, one of them is an aggregate and the other rows a caller may or may
// not see, and the tests that matter most here are the ones that pin where
// those two differ.

// seedProjectFixture records the set of expenses every figure in this file is
// asserted against, and answers the owner's id. It is seeded rather than
// posted because it needs combinations no single create call reaches: a
// rejected trip's line, an invoiced line, a billable line nobody has priced.
//
// On projectKraftVerket, in NOK unless it says otherwise:
//
//   - approved, billable, priced 1000.00, not invoiced → ready
//   - approved, billable, priced 500.00, invoiced      → invoiced, not ready
//   - approved, billable, no price                     → unpriced
//   - an approved trip's line, billable, priced 250.00 → ready, through the claim
//   - an approved trip's per diem day                  → never billable
//   - submitted, billable, priced 800.00               → not ready: not approved
//   - a rejected expense                               → draft
//   - one EUR outlay, approved                         → its own currency
func seedProjectFixture(t *testing.T, h *harness, userID uuid.UUID) {
	t.Helper()
	trip := recordClaim(t, h, userID, projectKraftVerket, "approved")
	for _, e := range []recordedExpense{
		{project: projectKraftVerket, gross: "1250.00", vat: "250.00", paidBy: "employee",
			billable: true, billAmount: "1000.00", status: "approved"},
		{project: projectKraftVerket, gross: "625.00", vat: "125.00", paidBy: "company",
			billable: true, billAmount: "500.00", status: "approved", invoiced: true},
		{project: projectKraftVerket, kind: "mileage", gross: "300.00",
			billable: true, status: "approved"},
		{project: projectKraftVerket, claim: trip, gross: "200.00", paidBy: "employee",
			billable: true, billAmount: "250.00", date: laterExpenseDay},
		{project: projectKraftVerket, claim: trip, kind: "per_diem", gross: "400.00"},
		{project: projectKraftVerket, gross: "700.00", billable: true, billAmount: "800.00",
			paidBy: "employee", status: "submitted"},
		{project: projectKraftVerket, gross: "150.00", paidBy: "employee", status: "rejected"},
		{project: projectKraftVerket, currency: "EUR", gross: "90.00", paidBy: "employee",
			billable: true, billAmount: "100.00", status: "approved"},
	} {
		recordExpense(t, h, userID, e)
	}
}

// decimal is an amount from the contract's decimal text as the float the API
// publishes, so a test can hold the two against each other.
func decimal(t *testing.T, text string) float64 {
	t.Helper()
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

// sameAmount reports whether two money figures are the same to the cent. The
// API publishes a JSON number and the contract decimal text, so the comparison
// is made at the scale both are rounded to rather than bit for bit.
func sameAmount(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// TestExpensesProjectSummary_AnswersTheSameFiguresAsTheContract is the whole
// point of the endpoint: it is the provider's own summation, published. One
// fixture, two readers, field by field — so the Expenses tab and the Economy
// tab can never show a project two different numbers.
func TestExpensesProjectSummary_AnswersTheSameFiguresAsTheContract(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager)
	seedProjectFixture(t, h, managerID)

	summary := getProjectSummary(t, manager, projectKraftVerket)
	totals := projectExpensesOf(t, expensesProvider(t, h), projectKraftVerket)

	if len(summary.Currencies) != len(totals.Currencies) {
		t.Fatalf("the summary holds %d currencies, the contract %d", len(summary.Currencies), len(totals.Currencies))
	}
	for i, want := range totals.Currencies {
		got := summary.Currencies[i]
		if got.Currency != want.Currency {
			t.Fatalf("currency %d = %q, want %q (the order is the contract's: by code ascending)",
				i, got.Currency, want.Currency)
		}
		for _, pair := range []struct {
			name string
			got  summaryBucketJSON
			want contracts.ExpenseBucket
		}{
			{"approved", got.Approved, want.Approved},
			{"submitted", got.Submitted, want.Submitted},
			{"draft", got.Draft, want.Draft},
			{"total", got.Total, want.Total},
		} {
			if int64(pair.got.Count) != pair.want.Count {
				t.Errorf("%s %s count = %d, want %d", got.Currency, pair.name, pair.got.Count, pair.want.Count)
			}
			if !sameAmount(pair.got.Cost, decimal(t, pair.want.CostAmount)) {
				t.Errorf("%s %s cost = %v, want %s", got.Currency, pair.name, pair.got.Cost, pair.want.CostAmount)
			}
			if !sameAmount(pair.got.BillAmount, decimal(t, pair.want.BillAmount)) {
				t.Errorf("%s %s bill = %v, want %s", got.Currency, pair.name, pair.got.BillAmount, pair.want.BillAmount)
			}
		}
		switch {
		case int64(got.ReadyCount) != want.ReadyCount:
			t.Errorf("%s readyCount = %d, want %d", got.Currency, got.ReadyCount, want.ReadyCount)
		case !sameAmount(got.ReadyAmount, decimal(t, want.ReadyAmount)):
			t.Errorf("%s readyAmount = %v, want %s", got.Currency, got.ReadyAmount, want.ReadyAmount)
		case int64(got.InvoicedCount) != want.InvoicedCount:
			t.Errorf("%s invoicedCount = %d, want %d", got.Currency, got.InvoicedCount, want.InvoicedCount)
		case !sameAmount(got.InvoicedAmount, decimal(t, want.InvoicedAmount)):
			t.Errorf("%s invoicedAmount = %v, want %s", got.Currency, got.InvoicedAmount, want.InvoicedAmount)
		case int64(got.UnpricedCount) != want.UnpricedCount:
			t.Errorf("%s unpricedCount = %d, want %d", got.Currency, got.UnpricedCount, want.UnpricedCount)
		}
	}
	if summary.LastEntryDate == nil || *summary.LastEntryDate != laterExpenseDay {
		t.Errorf("lastEntryDate = %v, want %q — the latest over every currency and status",
			summary.LastEntryDate, laterExpenseDay)
	}
	if totals.LastEntryDate == nil || *totals.LastEntryDate != laterExpenseDay {
		t.Errorf("the contract's last entry date = %v, want %q", totals.LastEntryDate, laterExpenseDay)
	}
}

// The figures themselves, written out once against the fixture above, so this
// file says what the numbers are rather than only that two readers agree on
// them. Costs are nets; a per diem day and a mileage line carry no VAT.
func TestExpensesProjectSummary_TheFiguresPerCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager)
	seedProjectFixture(t, h, managerID)

	summary := getProjectSummary(t, manager, projectKraftVerket)
	if len(summary.Currencies) != 2 || summary.Currencies[0].Currency != "EUR" {
		t.Fatalf("currencies = %+v, want EUR then NOK — by code ascending, neither converted", summary.Currencies)
	}
	nok := summaryCurrency(t, summary, "NOK")
	// approved: 1000 net + 500 net + 300 mileage = 1800, billing 1000 + 500;
	// the trip's two lines are approved through the claim: 200 + 400 = 600,
	// billing 250.
	wantSummaryBucket(t, "NOK approved", nok.Approved, 5, 2400, 1750)
	wantSummaryBucket(t, "NOK submitted", nok.Submitted, 1, 700, 800)
	wantSummaryBucket(t, "NOK draft", nok.Draft, 1, 150, 0)
	wantSummaryBucket(t, "NOK total", nok.Total, 7, 3250, 2550)
	switch {
	case nok.ReadyCount != 2 || !sameAmount(nok.ReadyAmount, 1250):
		t.Errorf("NOK ready = %d/%v, want 2/1250 — the priced approved lines nobody has invoiced, the trip's included",
			nok.ReadyCount, nok.ReadyAmount)
	case nok.InvoicedCount != 1 || !sameAmount(nok.InvoicedAmount, 500):
		t.Errorf("NOK invoiced = %d/%v, want 1/500", nok.InvoicedCount, nok.InvoicedAmount)
	case nok.UnpricedCount != 1:
		t.Errorf("NOK unpricedCount = %d, want 1 — the billable mileage nobody has priced", nok.UnpricedCount)
	}

	eur := summaryCurrency(t, summary, "EUR")
	wantSummaryBucket(t, "EUR approved", eur.Approved, 1, 90, 100)
	if eur.ReadyCount != 1 || !sameAmount(eur.ReadyAmount, 100) {
		t.Errorf("EUR ready = %d/%v, want 1/100 — its own figure, never added to the NOK one",
			eur.ReadyCount, eur.ReadyAmount)
	}
}

// wantSummaryBucket fails the test unless the bucket is exactly count, cost
// and bill.
func wantSummaryBucket(t *testing.T, name string, b summaryBucketJSON, count int32, cost, bill float64) {
	t.Helper()
	if b.Count != count {
		t.Errorf("%s count = %d, want %d", name, b.Count, count)
	}
	if !sameAmount(b.Cost, cost) {
		t.Errorf("%s cost = %v, want %v", name, b.Cost, cost)
	}
	if !sameAmount(b.BillAmount, bill) {
		t.Errorf("%s bill = %v, want %v", name, b.BillAmount, bill)
	}
}

// TestExpensesProjectSummary_IsForFinancialRightsOnTheProject is the caller
// matrix, and the decision behind it: the summary is a project's money, so it
// follows the rule pricing a line follows, not the rules that open other
// people's expenses. expenses:manage, expenses:approve and expenses:view-all
// are refused it as flatly as a stranger is, and every refusal is one bare 404.
func TestExpensesProjectSummary_IsForFinancialRightsOnTheProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager)
	seedProjectFixture(t, h, managerID)

	// Who may read it.
	getProjectSummary(t, manager, projectKraftVerket)
	all, _ := signIn(t, h, "projects:manage-all")
	getProjectSummary(t, all, projectKraftVerket)
	viewer, _ := signInAs(t, h, projectKraftVerket, roleViewer, "projects:view-financials")
	getProjectSummary(t, viewer, projectKraftVerket)
	// projects:view-all is what makes a project visible without a role on it,
	// which is what projects:view-financials then applies to.
	finance, _ := signIn(t, h, "projects:view-financials", "projects:view-all")
	getProjectSummary(t, finance, projectKraftVerket)

	// Who may not. The three expenses permissions see expenses, not a
	// project's money — the same line invoiced.go and the pricing door draw.
	for _, who := range []struct {
		name        string
		permissions []string
	}{
		{"a stranger", nil},
		{"expenses:manage", []string{"expenses:manage"}},
		{"expenses:approve", []string{"expenses:approve"}},
		{"expenses:view-all", []string{"expenses:view-all"}},
		{"all three of them", []string{"expenses:manage", "expenses:approve", "expenses:view-all"}},
		// projects:view-financials without sight of the project itself is
		// nothing: seesProjectFinancials asks seesProject first.
		{"projects:view-financials on a project they cannot see", []string{"projects:view-financials"}},
	} {
		c, _ := signIn(t, h, who.permissions...)
		summaryDenied(t, c, projectKraftVerket, who.name)
	}

	// A member of the project is not spared it either: what the company
	// charges its customer is the project's, not the team's.
	member, _ := signInAs(t, h, projectKraftVerket, roleMember, "expenses:view-all")
	summaryDenied(t, member, projectKraftVerket, "a member")

	// A project the directory does not know is the very same answer, so the
	// endpoint never says which ids exist.
	summaryDenied(t, manager, projectUnknown, "the manager, on an unknown project")
	boss, _ := signIn(t, h, "projects:manage-all")
	summaryDenied(t, boss, projectUnknown, "projects:manage-all, on an unknown project")
}

// Without the projects module the operation is not there at all, exactly as
// GET /projects answers (decision X2) — but as the same bare 404 every other
// refusal here is, because this one has three reasons and tells them apart to
// nobody.
func TestExpensesProjectSummary_WithoutTheProjectsModule(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	c, _ := signIn(t, h, "expenses:manage", "projects:manage-all", "projects:view-financials")
	summaryDenied(t, c, projectKraftVerket, "a caller in an installation with no projects module")
}

// A project with nothing recorded answers an empty list of currencies and no
// last entry date — never a 404, which would say the caller may not see it,
// and never a zeroed currency this module cannot name.
func TestExpensesProjectSummary_AProjectWithNothingRecorded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	summary := getProjectSummary(t, manager, projectKraftVerket)
	if len(summary.Currencies) != 0 {
		t.Errorf("currencies = %+v, want an empty list", summary.Currencies)
	}
	raw := rawProjectSummary(t, manager, projectKraftVerket)
	if _, present := raw["lastEntryDate"]; present {
		t.Errorf("lastEntryDate is present as %v, want the key absent", raw["lastEntryDate"])
	}
	if raw["currencies"] == nil {
		t.Error("currencies is null, want an empty array")
	}
}

// canRecord is projects' own CanLogTime for the caller: whoever may read these
// figures is not thereby somebody who may book a cost on the project.
func TestExpensesProjectSummary_CanRecordIsWhetherTheCallerMayBookOnIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	if !getProjectSummary(t, manager, projectKraftVerket).Capabilities.CanRecord {
		t.Error("canRecord is false for the project's manager, who may book on it")
	}

	// A finance reader on no team: they see the money and may book nothing.
	finance, _ := signIn(t, h, "projects:view-financials", "projects:view-all")
	if getProjectSummary(t, finance, projectKraftVerket).Capabilities.CanRecord {
		t.Error("canRecord is true for a reader who holds no role on the project")
	}

	// And a project closed for work: the manager still reads it, and the
	// button is gone, because the save would refuse them.
	h.projects.setCanLogTime(projectKraftVerket, false)
	if getProjectSummary(t, manager, projectKraftVerket).Capabilities.CanRecord {
		t.Error("canRecord is true although the directory says the caller may not book on the project")
	}
}

// TestExpensesEntries_ToInvoiceListsExactlyTheReadyLines: the list behind the
// figure. It is the same predicate, so the row count and the summed bill
// amounts are the summary's readyCount and readyAmount — asserted here on one
// fixture, which is what keeps the tab's "ready to invoice" header honest
// against the rows under it.
func TestExpensesEntries_ToInvoiceListsExactlyTheReadyLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager)
	seedProjectFixture(t, h, managerID)

	page := listEntries(t, manager, fmt.Sprintf("?projectId=%d&toInvoice=true", projectKraftVerket))
	nok := summaryCurrency(t, getProjectSummary(t, manager, projectKraftVerket), "NOK")

	// The fixture's one EUR line is ready too, so the list — which does not
	// filter by currency — holds the NOK figure's rows plus that one.
	if want := int(nok.ReadyCount) + 1; len(page.Data) != want || page.Pagination.TotalCount != int32(want) {
		t.Fatalf("toInvoice listed %d rows (total %d), want %d: the summary counts %d ready in NOK and one in EUR",
			len(page.Data), page.Pagination.TotalCount, want, nok.ReadyCount)
	}
	var billed, euro float64
	for _, e := range page.Data {
		if e.Billing == nil {
			t.Fatalf("entry %d carries no billing, so the list cannot be checked against the figure", e.Id)
		}
		if e.Currency == "EUR" {
			euro += e.Billing.BillAmount
			continue
		}
		billed += e.Billing.BillAmount
		switch {
		case e.Status != "approved":
			t.Errorf("entry %d is %q, want every listed line approved through its unit", e.Id, e.Status)
		case !e.Billable:
			t.Errorf("entry %d is not billable", e.Id)
		case e.Billing.Invoice != nil:
			t.Errorf("entry %d has already been invoiced", e.Id)
		case e.Kind == "per_diem":
			t.Errorf("entry %d is a per diem day, which bills nobody anything", e.Id)
		}
	}
	if !sameAmount(billed, nok.ReadyAmount) {
		t.Errorf("the listed NOK lines bill %v, want the summary's readyAmount %v", billed, nok.ReadyAmount)
	}
	if !sameAmount(euro, 100) {
		t.Errorf("the listed EUR lines bill %v, want 100", euro)
	}

	// A trip's approved line is in it — the predicate reads the unit's status,
	// not the line's own column, which stays at the draft it was created with.
	var throughAClaim int
	for _, e := range page.Data {
		if e.ClaimId != nil {
			throughAClaim++
		}
	}
	if throughAClaim != 1 {
		t.Errorf("%d of the listed lines belong to a travel claim, want 1: an approved trip's billable line is ready",
			throughAClaim)
	}

	// And the other side of the filter is everything else the caller may see.
	rest := listEntries(t, manager, fmt.Sprintf("?projectId=%d&toInvoice=false", projectKraftVerket))
	all := listEntries(t, manager, fmt.Sprintf("?projectId=%d", projectKraftVerket))
	if len(rest.Data)+len(page.Data) != len(all.Data) {
		t.Errorf("toInvoice true and false list %d + %d rows, want the whole %d",
			len(page.Data), len(rest.Data), len(all.Data))
	}
}

// The filter needs a project: invoicing is done a project at a time, and a
// contradictory status is refused rather than quietly answering nothing.
func TestExpensesEntries_ToInvoiceNeedsAProjectAndAgreesWithStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager)
	seedProjectFixture(t, h, managerID)

	for _, query := range []string{
		"?toInvoice=true",
		"?toInvoice=false",
		"?claimId=1&toInvoice=true&status=approved",
	} {
		r := manager.Do(http.MethodGet, entriesPath+query, nil)
		if r.Status != http.StatusBadRequest {
			t.Errorf("list entries %q: status %d body %s, want 400 — toInvoice needs a projectId",
				query, r.Status, r.Body)
		}
	}

	for _, status := range []string{"draft", "submitted", "rejected"} {
		query := fmt.Sprintf("?projectId=%d&toInvoice=true&status=%s", projectKraftVerket, status)
		r := manager.Do(http.MethodGet, entriesPath+query, nil)
		if r.Status != http.StatusBadRequest {
			t.Errorf("list entries %q: status %d body %s, want 400 — only an approved unit is ever ready",
				query, r.Status, r.Body)
		}
	}

	// status=approved agrees with it and is allowed; so is any status beside
	// toInvoice=false, which says nothing about the status.
	listEntries(t, manager, fmt.Sprintf("?projectId=%d&toInvoice=true&status=approved", projectKraftVerket))
	listEntries(t, manager, fmt.Sprintf("?projectId=%d&toInvoice=false&status=draft", projectKraftVerket))
}

// TestExpensesEntries_ToInvoiceWidensNobodysSight is spec §5, pinned: the
// summary is an aggregate and the list is rows, and they are gated by
// different rules on purpose. A projects:view-financials holder who does not
// manage the project reads every figure and is shown no expense at all, which
// is what the tab has to say out loud.
func TestExpensesEntries_ToInvoiceWidensNobodysSight(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	seedProjectFixture(t, h, ownerID)

	finance, _ := signIn(t, h, "projects:view-financials", "projects:view-all")
	if nok := summaryCurrency(t, getProjectSummary(t, finance, projectKraftVerket), "NOK"); nok.ReadyCount != 2 {
		t.Errorf("the finance reader's readyCount = %d, want 2: the aggregate is theirs to see", nok.ReadyCount)
	}
	page := listEntries(t, finance, fmt.Sprintf("?projectId=%d&toInvoice=true", projectKraftVerket))
	if len(page.Data) != 0 || page.Pagination.TotalCount != 0 {
		t.Errorf("the finance reader listed %d of somebody else's expenses, want none: the summary widens nothing",
			len(page.Data))
	}

	// The project's manager sees the very same rows the summary counts.
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	if rows := listEntries(t, manager, fmt.Sprintf("?projectId=%d&toInvoice=true", projectKraftVerket)); len(rows.Data) != 3 {
		t.Errorf("the manager listed %d rows, want 3 (two NOK and one EUR)", len(rows.Data))
	}
}

// The filter never reaches past what the caller may see in the first place:
// an owner asking for another project's ready lines gets their own and nothing
// more.
func TestExpensesEntries_ToInvoiceKeepsTheListsOwnVisibility(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	seedProjectFixture(t, h, ownerID)

	stranger, strangerID := signInAs(t, h, projectKraftVerket, roleMember)
	recordExpense(t, h, strangerID, recordedExpense{project: projectKraftVerket, gross: "50.00",
		paidBy: "employee", billable: true, billAmount: "60.00", status: "approved"})

	page := listEntries(t, stranger, fmt.Sprintf("?projectId=%d&toInvoice=true", projectKraftVerket))
	if len(page.Data) != 1 {
		t.Fatalf("a member listed %d ready lines, want only their own", len(page.Data))
	}
	if page.Data[0].Owner.UserId != strangerID {
		t.Errorf("the listed line belongs to %v, want the caller", page.Data[0].Owner.UserId)
	}
}
