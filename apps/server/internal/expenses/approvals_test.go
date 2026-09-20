package expenses_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// mustJSON renders a value for a failure message, so two shapes that differ
// somewhere behind a pointer say where.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("render %v: %v", v, err)
	}
	return string(out)
}

// This file is the approval queue: what one caller is shown, grouped per
// person, and how it pages.

func TestExpensesApprovals_AreGroupedPerPersonWithTheirTotals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first, firstID := signIn(t, h)
	second, secondID := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	// The first person: an outlay with a receipt, an outlay without one and a
	// mileage line, which never takes one.
	withReceipt := createEntry(t, first, outlayBody(map[string]any{"grossAmount": 100.00}))
	uploadReceipt(t, first, withReceipt.Id, "kvittering.pdf", "application/pdf", testPDF(8))
	without := createEntry(t, first, outlayBody(map[string]any{"grossAmount": 200.00, "description": "Uten bilag"}))
	mileage := createEntry(t, first, mileageBody(nil)) // 120 × 5.30 = 636
	submitEntries(t, first, withReceipt.Id, without.Id, mileage.Id)

	// The second person: one outlay the company paid, so nothing is owed back.
	company := createEntry(t, second, outlayBody(map[string]any{"grossAmount": 400.00, "paidBy": "company"}))
	submitEntries(t, second, company.Id)

	// A draft is not in the queue at all.
	createEntry(t, second, outlayBody(map[string]any{"description": "Fortsatt kladd"}))

	page := getApprovals(t, approver, "")
	if page.Pagination.TotalCount != 2 || len(page.Data) != 2 {
		t.Fatalf("queue = %+v, want two people", page)
	}
	byUser := map[uuid.UUID]approvalGroupJSON{}
	for _, g := range page.Data {
		byUser[g.User.UserId] = g
	}
	one, ok := byUser[firstID]
	if !ok {
		t.Fatalf("the queue holds %v, want the first person", byUser)
	}
	if len(one.Entries) != 3 {
		t.Errorf("entries = %d, want the three submitted ones", len(one.Entries))
	}
	if len(one.Totals) != 1 || one.Totals[0].Currency != "NOK" {
		t.Fatalf("totals = %+v, want one NOK total", one.Totals)
	}
	if one.Totals[0].Gross != 936 || one.Totals[0].OwedToEmployee != 936 {
		t.Errorf("totals = %+v, want 100 + 200 + 636 both ways", one.Totals[0])
	}
	if one.ReceiptsMissing != 1 {
		t.Errorf("receiptsMissing = %d, want the one outlay with no receipt", one.ReceiptsMissing)
	}
	if one.OverriddenRates != 0 {
		t.Errorf("overriddenRates = %d, want none", one.OverriddenRates)
	}
	if !one.User.Active || one.User.DisplayName == "" {
		t.Errorf("user = %+v, want the person named", one.User)
	}

	two := byUser[secondID]
	if len(two.Entries) != 1 || two.Totals[0].Gross != 400 || two.Totals[0].OwedToEmployee != 0 {
		t.Errorf("the second group = %+v, want one company-paid outlay owing nothing", two)
	}
}

// One person spending in two currencies gets one total per currency; nothing
// is ever converted (design §4).
func TestExpensesApprovals_TotalOneCurrencyAtATime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	nok := createEntry(t, owner, outlayBody(map[string]any{"grossAmount": 100.00}))
	eur := createEntry(t, owner, outlayBody(map[string]any{
		"grossAmount": 50.00, "currency": "EUR", "description": "Taxi i Berlin",
	}))
	submitEntries(t, owner, nok.Id, eur.Id)

	group := getApprovals(t, approver, "").Data[0]
	if len(group.Totals) != 2 {
		t.Fatalf("totals = %+v, want one per currency", group.Totals)
	}
	if group.Totals[0].Currency != "EUR" || group.Totals[0].Gross != 50 {
		t.Errorf("totals[0] = %+v, want EUR first, by code", group.Totals[0])
	}
	if group.Totals[1].Currency != "NOK" || group.Totals[1].Gross != 100 {
		t.Errorf("totals[1] = %+v, want NOK", group.Totals[1])
	}
}

// The queue holds what the caller may approve and nothing else: a project's
// manager sees their own project's lines, and not the ones with no project.
func TestExpensesApprovals_HoldWhatTheCallerMayApprove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	h.projects.addRole(projectEuro, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	approver, _ := signIn(t, h, "expenses:approve")

	mine := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	theirs := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectEuro, "currency": "EUR", "description": "Hotell",
	}))
	none := createEntry(t, owner, outlayBody(map[string]any{"description": "Uten prosjekt"}))
	submitEntries(t, owner, mine.Id, theirs.Id, none.Id)

	managers := getApprovals(t, manager, "")
	if len(managers.Data) != 1 || len(managers.Data[0].Entries) != 1 || managers.Data[0].Entries[0].Id != mine.Id {
		t.Fatalf("the manager's queue = %+v, want only their own project's line", managers)
	}
	approvers := getApprovals(t, approver, "")
	if len(approvers.Data) != 1 || len(approvers.Data[0].Entries) != 3 {
		t.Fatalf("the approver's queue = %+v, want all three", approvers)
	}
}

// The period lock takes entries out of the queue for everyone it holds back,
// because approving them would be refused anyway.
func TestExpensesApprovals_LeaveOutWhatTheLockWouldRefuse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	locked := createEntry(t, owner, outlayBody(nil))
	open := createEntry(t, owner, outlayBody(map[string]any{"entryDate": "2026-05-04", "description": "Etter låsen"}))
	submitEntries(t, owner, locked.Id, open.Id)
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-04-01"}))

	page := getApprovals(t, approver, "")
	if len(page.Data) != 1 || len(page.Data[0].Entries) != 1 || page.Data[0].Entries[0].Id != open.Id {
		t.Fatalf("the queue = %+v, want only the line the lock leaves open", page)
	}
	past := getApprovals(t, admin, "")
	if len(past.Data) != 1 || len(past.Data[0].Entries) != 2 {
		t.Fatalf("the manager's queue = %+v, want both — the lock does not hold them back", past)
	}
}

// Paging is by person, in SQL: a page holds whole groups, never half of one.
func TestExpensesApprovals_ArePagedByPerson(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	approver, _ := signIn(t, h, "expenses:approve")

	const people = 3
	for i := range people {
		owner, _ := signIn(t, h)
		first := createEntry(t, owner, outlayBody(map[string]any{
			"entryDate":   fmt.Sprintf("2026-03-%02d", i+1),
			"description": fmt.Sprintf("Person %d, en", i),
		}))
		second := createEntry(t, owner, outlayBody(map[string]any{
			"entryDate":   fmt.Sprintf("2026-03-%02d", i+2),
			"description": fmt.Sprintf("Person %d, to", i),
		}))
		submitEntries(t, owner, first.Id, second.Id)
	}

	page := getApprovals(t, approver, "?page=1&pageSize=2")
	if page.Pagination.TotalCount != people || len(page.Data) != 2 {
		t.Fatalf("page 1 = %+v, want two of three people", page.Pagination)
	}
	for _, g := range page.Data {
		if len(g.Entries) != 2 {
			t.Errorf("group %v holds %d entries, want a whole person's two", g.User.UserId, len(g.Entries))
		}
	}
	last := getApprovals(t, approver, "?page=2&pageSize=2")
	if len(last.Data) != 1 || len(last.Data[0].Entries) != 2 {
		t.Fatalf("page 2 = %+v, want the third person whole", last)
	}
	// The oldest waiting person comes first, and no one is on two pages.
	seen := map[uuid.UUID]bool{}
	for _, g := range append(page.Data, last.Data...) {
		if seen[g.User.UserId] {
			t.Errorf("person %v is on two pages", g.User.UserId)
		}
		seen[g.User.UserId] = true
	}
	if page.Data[0].Entries[0].EntryDate != "2026-03-01" {
		t.Errorf("the first group starts on %q, want the oldest waiting day first", page.Data[0].Entries[0].EntryDate)
	}
}

func TestExpensesApprovals_RefusePagingOutOfRange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	approver, _ := signIn(t, h, "expenses:approve")

	if r := approver.Do(http.MethodGet, approvalsPath+"?page=0", nil); r.Status != http.StatusBadRequest {
		t.Errorf("page=0: status %d body %s, want 400", r.Status, r.Body)
	}
	if r := approver.Do(http.MethodGet, approvalsPath+"?pageSize=1000", nil); r.Status != http.StatusBadRequest {
		t.Errorf("pageSize=1000: status %d body %s, want 400", r.Status, r.Body)
	}
}

// One renderer: an entry in the queue is shaped exactly as the caller's own
// read of it, billing object included.
func TestExpensesApprovals_ShapeTheirEntriesAsASingleReadWould(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "vatAmount": 250.00,
	}))
	submitEntries(t, owner, entry.Id)

	queued := getApprovals(t, manager, "").Data[0].Entries[0]
	single := getEntry(t, manager, entry.Id)
	if !reflect.DeepEqual(queued, single) {
		t.Errorf("the queue's copy and the single read differ:\n queue = %s\nsingle = %s",
			mustJSON(t, queued), mustJSON(t, single))
	}
	if queued.Billing == nil || !queued.Capabilities.CanApprove {
		t.Errorf("queued = %+v, want the project manager's billing and approval", queued)
	}
}

// Decisions X1 and X2: the queue is the same queue without the projects
// module, decided on expenses:approve alone.
func TestExpensesApprovals_WithoutProjects_AreTheApprovePermissionsAlone(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")
	bystander, _ := signIn(t, h, "expenses:view-all")

	entry := createEntry(t, owner, outlayBody(nil))
	submitEntries(t, owner, entry.Id)

	if r := bystander.Do(http.MethodGet, approvalsPath, nil); r.Status != http.StatusForbidden {
		t.Errorf("a caller who approves nothing: status %d, want 403", r.Status)
	}
	page := getApprovals(t, approver, "")
	if len(page.Data) != 1 || len(page.Data[0].Entries) != 1 {
		t.Fatalf("queue = %+v, want the one submitted expense", page)
	}
}

// An empty queue is an empty page, never a refusal.
func TestExpensesApprovals_AreAnEmptyPageWhenNothingWaits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	approver, _ := signIn(t, h, "expenses:approve")

	page := getApprovals(t, approver, "")
	if page.Pagination.TotalCount != 0 || len(page.Data) != 0 {
		t.Errorf("queue = %+v, want an empty page", page)
	}
}
