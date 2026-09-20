package expenses_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the first of design decision X5's two tracks after approval:
// what the employee is owed back. It is per unit — a standalone expense, in
// this delivery — and it is expenses:manage's alone, because paying somebody
// is the company's act and not an approver's.

// companyPaid is an approved outlay the company paid for itself: the employee
// is owed nothing for it, so it never reaches a payroll run.
func companyPaid(overrides map[string]any) map[string]any {
	return outlayBody(bodyWith(map[string]any{"paidBy": "company", "grossAmount": 500.00}, overrides))
}

// TestExpensesReimbursements_AreGroupedPerPersonWithTheirTotals is the list a
// payroll run is made from: approved expenses that are owed back and have not
// been paid, one group per person, the person who has been waiting longest
// first.
func TestExpensesReimbursements_AreGroupedPerPersonWithTheirTotals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	anna, annaID := signIn(t, h)
	bjorn, bjornID := signIn(t, h)

	owed := createEntry(t, anna, outlayBody(nil))
	company := createEntry(t, anna, companyPaid(nil))
	trip := createEntry(t, bjorn, mileageBody(map[string]any{"entryDate": "2026-03-01"}))
	approvedBy(t, anna, boss, owed.Id, company.Id)
	approvedBy(t, bjorn, boss, trip.Id)

	page := getReimbursements(t, boss, "")
	if len(page.Data) != 2 || page.Pagination.TotalCount != 2 {
		t.Fatalf("groups = %d (total %d), want 2", len(page.Data), page.Pagination.TotalCount)
	}
	// Bjørn's line is dated earliest, so he has been waiting longest.
	if page.Data[0].User.UserId != bjornID || page.Data[1].User.UserId != annaID {
		t.Errorf("group order = %v, %v; want Bjørn (the oldest line) first",
			page.Data[0].User.UserId, page.Data[1].User.UserId)
	}
	annas := page.Data[1]
	if len(annas.Entries) != 1 || annas.Entries[0].Id != owed.Id {
		t.Fatalf("Anna's group = %v, want only the outlay she paid for herself (not %d, the company's own)",
			entryIDsOf(annas.Entries), company.Id)
	}
	if len(annas.Totals) != 1 || annas.Totals[0].Currency != "NOK" ||
		annas.Totals[0].Gross != 1250 || annas.Totals[0].OwedToEmployee != 1250 {
		t.Errorf("Anna's totals = %+v, want one NOK line of 1250 gross and 1250 owed", annas.Totals)
	}
	if got := page.Data[0].Totals[0].OwedToEmployee; got != 636 {
		t.Errorf("Bjørn is owed %v, want 120 km at 5.30 = 636", got)
	}
	// The list shapes an expense exactly as a read of it would for this
	// caller, capabilities and all.
	if caps := annas.Entries[0].Capabilities; !caps.CanMarkReimbursed || caps.CanUndoReimbursed {
		t.Errorf("capabilities of a waiting line = %+v, want canMarkReimbursed and not canUndoReimbursed", caps)
	}
	if annas.Entries[0].Owner.UserId != annaID {
		t.Errorf("owner = %v, want Anna", annas.Entries[0].Owner.UserId)
	}
}

// entryIDsOf is the ids of a list of rendered expenses, for a failure message.
func entryIDsOf(entries []entryJSON) []int64 {
	out := make([]int64, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Id)
	}
	return out
}

// TestExpensesReimbursements_AreManagesAlone: every door of the payroll track
// is expenses:manage's. Approving expenses does not pay for them.
func TestExpensesReimbursements_AreManagesAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	approver, _ := signIn(t, h, "expenses:approve", "expenses:view-all")
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, approver, entry.Id)

	forbidden(t, approver, http.MethodGet, reimbursementsPath, nil)
	forbidden(t, approver, http.MethodGet, reimbursementsExportPath, nil)
	forbidden(t, approver, http.MethodPost, reimbursedPath, reimbursedBody([]int64{entry.Id}, nil))
	forbidden(t, approver, http.MethodPost, reimbursedUndoPath, flowBody([]int64{entry.Id}, nil))

	// And the capability says the same thing, so a client hides the control.
	if caps := getEntry(t, approver, entry.Id).Capabilities; caps.CanMarkReimbursed {
		t.Error("canMarkReimbursed is true for a caller without expenses:manage")
	}
}

// TestExpensesReimbursed_MarksAndUndoes is the payroll run itself and the way
// back from it.
func TestExpensesReimbursed_MarksAndUndoes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, bossID := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, boss, entry.Id)

	moved := markReimbursed(t, boss, reimbursedBody([]int64{entry.Id}, map[string]any{"reference": "Lønn april"}))
	if len(moved) != 1 {
		t.Fatalf("marked %d expenses, want 1", len(moved))
	}
	paid := moved[0]
	switch {
	case paid.Reimbursement == nil:
		t.Fatalf("the marked expense carries no reimbursement: %+v", paid)
	case paid.Reimbursement.Date != "2026-04-02":
		t.Errorf("reimbursement date = %q, want 2026-04-02", paid.Reimbursement.Date)
	case paid.Reimbursement.Reference == nil || *paid.Reimbursement.Reference != "Lønn april":
		t.Errorf("reimbursement reference = %v, want the one given", paid.Reimbursement.Reference)
	case paid.Reimbursement.By.UserId != bossID:
		t.Errorf("reimbursed by = %v, want the caller", paid.Reimbursement.By.UserId)
	case paid.Status != "approved":
		t.Errorf("status after a payout = %q, want it still approved", paid.Status)
	}
	if paid.Capabilities.CanMarkReimbursed || !paid.Capabilities.CanUndoReimbursed {
		t.Errorf("capabilities after a payout = %+v, want only the undo", paid.Capabilities)
	}
	// A payout records that money moved; it does not reprice anything.
	if paid.GrossAmount != 1250 || paid.NetAmount != 1250 || paid.OwedToEmployee != 1250 {
		t.Errorf("figures after a payout = %v/%v/%v, want the frozen 1250 throughout",
			paid.GrossAmount, paid.NetAmount, paid.OwedToEmployee)
	}

	// Its owner sees that they have been paid, although they could never do
	// it themselves.
	mine := getEntry(t, owner, entry.Id)
	if mine.Reimbursement == nil || mine.Reimbursement.Date != "2026-04-02" {
		t.Errorf("the owner's copy carries %+v, want the reimbursement", mine.Reimbursement)
	}

	if page := getReimbursements(t, boss, ""); len(page.Data) != 0 {
		t.Errorf("waiting groups after the payout = %d, want none", len(page.Data))
	}
	done := getReimbursements(t, boss, "?state=reimbursed")
	if len(done.Data) != 1 || len(done.Data[0].Entries) != 1 || done.Data[0].Entries[0].Id != entry.Id {
		t.Fatalf("the reimbursed list = %+v, want the one that was just paid", done.Data)
	}

	back := undoReimbursed(t, boss, entry.Id)
	if len(back) != 1 || back[0].Reimbursement != nil {
		t.Fatalf("after the undo the expense carries %+v, want no reimbursement", back[0].Reimbursement)
	}
	if page := getReimbursements(t, boss, ""); len(page.Data) != 1 {
		t.Errorf("waiting groups after the undo = %d, want the expense back", len(page.Data))
	}
}

// TestExpensesReimbursed_SaysWhyPerExpenseAndMovesNothing: the batch is all or
// nothing, and every refused id is explained on entryIds in the shape the flow
// already answers in.
func TestExpensesReimbursed_SaysWhyPerExpenseAndMovesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)

	draft := createEntry(t, owner, outlayBody(nil))
	company := createEntry(t, owner, companyPaid(nil))
	ready := createEntry(t, owner, outlayBody(map[string]any{"description": "Boremaskin"}))
	approvedBy(t, owner, boss, company.Id, ready.Id)

	errs := refusedReimbursement(t, boss, reimbursedPath,
		reimbursedBody([]int64{ready.Id, draft.Id, company.Id, 90210}, nil))
	for _, want := range []string{
		fmt.Sprintf("Expense %d is draft, and only an approved expense can be marked reimbursed", draft.Id),
		fmt.Sprintf("Expense %d owes the employee nothing", company.Id),
		fmt.Sprintf("Expense %d was not found", int64(90210)),
	} {
		if !mentions(errs["entryIds"], want) {
			t.Errorf("errors %v do not say %q", errs["entryIds"], want)
		}
	}
	if got := getEntry(t, boss, ready.Id); got.Reimbursement != nil {
		t.Error("an expense was paid although the batch was refused")
	}

	markReimbursed(t, boss, reimbursedBody([]int64{ready.Id}, nil))
	again := refusedReimbursement(t, boss, reimbursedPath, reimbursedBody([]int64{ready.Id}, nil))
	if !mentions(again["entryIds"], fmt.Sprintf("Expense %d has already been reimbursed", ready.Id)) {
		t.Errorf("errors %v do not refuse a second payout", again["entryIds"])
	}
	undone := refusedReimbursement(t, boss, reimbursedUndoPath, flowBody([]int64{company.Id}, nil))
	if !mentions(undone["entryIds"], fmt.Sprintf("Expense %d has not been reimbursed", company.Id)) {
		t.Errorf("errors %v do not refuse an undo of something never paid", undone["entryIds"])
	}
}

// TestExpensesReimbursed_TheDateIsACalendarDayAlreadyPast: a payroll run is
// recorded when it happened, which is today or before it — never a date
// somebody expects to pay on.
func TestExpensesReimbursed_TheDateIsACalendarDayAlreadyPast(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, boss, entry.Id)

	for name, body := range map[string]map[string]any{
		"with no date at all": reimbursedBody([]int64{entry.Id}, map[string]any{"date": nil}),
		"in the future":       reimbursedBody([]int64{entry.Id}, map[string]any{"date": "2026-09-13"}),
	} {
		t.Run(name, func(t *testing.T) {
			errs := refusedReimbursement(t, boss, reimbursedPath, body)
			if len(errs["date"]) == 0 {
				t.Errorf("errors = %v, want one on date", errs)
			}
		})
	}
	long := strings.Repeat("r", 101)
	errs := refusedReimbursement(t, boss, reimbursedPath,
		reimbursedBody([]int64{entry.Id}, map[string]any{"reference": long}))
	if len(errs["reference"]) == 0 {
		t.Errorf("errors = %v, want one on reference", errs)
	}

	// Today is not the future.
	today := h.Now().UTC().Format(time.DateOnly)
	paid := markReimbursed(t, boss, reimbursedBody([]int64{entry.Id}, map[string]any{"date": today}))
	if paid[0].Reimbursement == nil || paid[0].Reimbursement.Date != today {
		t.Errorf("reimbursement = %+v, want it dated today", paid[0].Reimbursement)
	}
}

// TestExpensesReimbursed_IsNotHeldBackByThePeriodLock: closing the books does
// not stop payroll. Only expenses:manage marks a reimbursement, and the lock
// never held expenses:manage back anyway — which is the whole of the rule
// here, written down so a later reader does not add the check.
func TestExpensesReimbursed_IsNotHeldBackByThePeriodLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, boss, entry.Id)
	putSettings(t, boss, settingsBody(map[string]any{"lockedBefore": "2026-04-01"}))

	paid := markReimbursed(t, boss, reimbursedBody([]int64{entry.Id}, nil))
	if paid[0].Reimbursement == nil {
		t.Fatal("a line dated inside a closed period could not be reimbursed")
	}
	undoReimbursed(t, boss, entry.Id)
}

// TestExpensesUnapprove_RefusesWhatHasBeenReimbursed drives the unapprove
// guard from the door that can actually set the stamp: money that has gone out
// is not unapproved back into a draft.
func TestExpensesUnapprove_RefusesWhatHasBeenReimbursed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, boss, entry.Id)
	markReimbursed(t, boss, reimbursedBody([]int64{entry.Id}, nil))

	if caps := getEntry(t, boss, entry.Id).Capabilities; caps.CanUnapprove {
		t.Error("canUnapprove is true on a reimbursed expense")
	}
	errs := refusedFlow(t, boss, unapprovePath, flowBody([]int64{entry.Id}, nil))
	if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d has been reimbursed", entry.Id)) {
		t.Errorf("errors %v do not refuse unapproving a reimbursed expense", errs["entryIds"])
	}
}

// TestExpensesReimbursements_NarrowToOnePersonOrPeriod is the payroll clerk's
// own filtering, and the paging that holds whole people.
func TestExpensesReimbursements_NarrowToOnePersonOrPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	anna, annaID := signIn(t, h)
	bjorn, _ := signIn(t, h)

	march := createEntry(t, anna, outlayBody(map[string]any{"entryDate": "2026-03-10"}))
	april := createEntry(t, anna, outlayBody(map[string]any{"entryDate": "2026-04-10"}))
	trip := createEntry(t, bjorn, mileageBody(map[string]any{"entryDate": "2026-03-01"}))
	approvedBy(t, anna, boss, march.Id, april.Id)
	approvedBy(t, bjorn, boss, trip.Id)

	only := getReimbursements(t, boss, "?userId="+annaID.String())
	if len(only.Data) != 1 || only.Data[0].User.UserId != annaID || len(only.Data[0].Entries) != 2 {
		t.Errorf("the userId filter answered %+v, want Anna's two expenses alone", only.Data)
	}
	period := getReimbursements(t, boss, "?from=2026-04-01&to=2026-04-30")
	if len(period.Data) != 1 || len(period.Data[0].Entries) != 1 || period.Data[0].Entries[0].Id != april.Id {
		t.Errorf("the period filter answered %+v, want April's expense alone", period.Data)
	}

	first := getReimbursements(t, boss, "?pageSize=1")
	if len(first.Data) != 1 || first.Pagination.TotalCount != 2 || first.Pagination.TotalPages != 2 {
		t.Fatalf("page 1 = %d groups of %d (%d pages), want one whole person of two",
			len(first.Data), first.Pagination.TotalCount, first.Pagination.TotalPages)
	}
	second := getReimbursements(t, boss, "?page=2&pageSize=1")
	if len(second.Data) != 1 || second.Data[0].User.UserId == first.Data[0].User.UserId {
		t.Errorf("page 2 = %+v, want the other person", second.Data)
	}
	if len(second.Data[0].Entries)+len(first.Data[0].Entries) != 3 {
		t.Errorf("the two pages hold %d expenses together, want all three",
			len(second.Data[0].Entries)+len(first.Data[0].Entries))
	}
}

// TestExpensesReimbursements_TheReimbursedListIsNewestFirst: an undo has to be
// reachable, so the list of what has been paid puts the latest payroll run at
// the top.
func TestExpensesReimbursements_TheReimbursedListIsNewestFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	anna, annaID := signIn(t, h)
	bjorn, bjornID := signIn(t, h)

	first := createEntry(t, anna, outlayBody(nil))
	second := createEntry(t, bjorn, outlayBody(nil))
	approvedBy(t, anna, boss, first.Id)
	approvedBy(t, bjorn, boss, second.Id)

	markReimbursed(t, boss, reimbursedBody([]int64{first.Id}, nil))
	h.Advance(time.Hour)
	markReimbursed(t, boss, reimbursedBody([]int64{second.Id}, nil))

	page := getReimbursements(t, boss, "?state=reimbursed")
	if len(page.Data) != 2 {
		t.Fatalf("reimbursed groups = %d, want 2", len(page.Data))
	}
	if page.Data[0].User.UserId != bjornID || page.Data[1].User.UserId != annaID {
		t.Errorf("order = %v, %v; want the latest payout first", page.Data[0].User.UserId, page.Data[1].User.UserId)
	}
}

// TestExpensesReimbursements_RefuseAStateTheyDoNotHave is the list's own query
// rule, in the words every other list here uses.
func TestExpensesReimbursements_RefuseAStateTheyDoNotHave(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:manage")

	r := boss.Do(http.MethodGet, reimbursementsPath+"?state=paid", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("state=paid: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if problem.Title != invalidQueryTitle {
		t.Errorf("problem title = %q, want %q", problem.Title, invalidQueryTitle)
	}
}

// TestExpensesEntries_TheReimbursedFilter is design §6's last list filter: it
// narrows the same predicate both the count and the page are taken under.
func TestExpensesEntries_TheReimbursedFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)

	paid := createEntry(t, owner, outlayBody(nil))
	waiting := createEntry(t, owner, outlayBody(map[string]any{"description": "Skruer"}))
	approvedBy(t, owner, boss, paid.Id, waiting.Id)
	markReimbursed(t, boss, reimbursedBody([]int64{paid.Id}, nil))

	yes := listEntries(t, owner, "?reimbursed=true")
	if ids := entryIDs(yes); len(ids) != 1 || ids[0] != paid.Id || yes.Pagination.TotalCount != 1 {
		t.Errorf("reimbursed=true = %v (total %d), want only the paid one", ids, yes.Pagination.TotalCount)
	}
	no := listEntries(t, owner, "?reimbursed=false")
	if ids := entryIDs(no); len(ids) != 1 || ids[0] != waiting.Id || no.Pagination.TotalCount != 1 {
		t.Errorf("reimbursed=false = %v (total %d), want only the waiting one", ids, no.Pagination.TotalCount)
	}
	if all := listEntries(t, owner, ""); all.Pagination.TotalCount != 2 {
		t.Errorf("no filter = %d, want both", all.Pagination.TotalCount)
	}
}

// TestExpensesReimbursements_WithoutProjects_IsTheSameTrack: nothing in the
// payroll track touches a project, so an installation running
// MODULES=customers,expenses gets all of it.
func TestExpensesReimbursements_WithoutProjects_IsTheSameTrack(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, boss, entry.Id)

	page := getReimbursements(t, boss, "")
	if len(page.Data) != 1 || len(page.Data[0].Entries) != 1 {
		t.Fatalf("the list = %+v, want the one waiting expense", page.Data)
	}
	paid := markReimbursed(t, boss, reimbursedBody([]int64{entry.Id}, nil))
	if paid[0].Reimbursement == nil {
		t.Error("an expense could not be reimbursed without the projects module")
	}
	undoReimbursed(t, boss, entry.Id)
}

// TestExpensesReimbursement_ThePayrollReferenceIsNarrowerThanTheStamp: that
// somebody has been paid back is shown to everyone who may see the expense
// (decision X5) — its owner first of all. *Which payroll run* it went with is
// not: that is the payroll clerk's record, and it is shown to the owner and to
// the three expenses permissions that read everybody's expenses, not to a
// project manager who sees the line because of the project it sits on.
func TestExpensesReimbursement_ThePayrollReferenceIsNarrowerThanTheStamp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	entry := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	approvedBy(t, owner, boss, entry.Id)
	markReimbursed(t, boss, reimbursedBody([]int64{entry.Id}, map[string]any{"reference": "LØNN-2026-04"}))

	viewer, _ := signIn(t, h, "expenses:view-all")
	for name, tc := range map[string]struct {
		client        *modtest.Client
		wantReference bool
	}{
		"the owner":           {owner, true},
		"expenses:view-all":   {viewer, true},
		"expenses:manage":     {boss, true},
		"the project manager": {manager, false},
	} {
		raw, ok := rawEntry(t, tc.client, entry.Id)["reimbursement"].(map[string]any)
		if !ok {
			t.Fatalf("%s: the reimbursement stamp is missing — everyone who may see the expense sees it", name)
		}
		for _, field := range []string{"at", "by", "date"} {
			if _, present := raw[field]; !present {
				t.Errorf("%s: the stamp has no %q", name, field)
			}
		}
		reference, present := raw["reference"]
		switch {
		case tc.wantReference && reference != "LØNN-2026-04":
			t.Errorf("%s: reference = %v, want the payroll run's", name, reference)
		case !tc.wantReference && present:
			t.Errorf("%s: reference = %v, want it absent", name, reference)
		}
	}
}

// TestExpensesReimbursed_ACompanyPaidLineSaysSoOnTheCapability: the per-id
// refusal and the capability are one rule (owesEmployee), so a client driving
// a button off canMarkReimbursed never posts a batch the server will refuse.
func TestExpensesReimbursed_ACompanyPaidLineSaysSoOnTheCapability(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)

	company := createEntry(t, owner, companyPaid(nil))
	employee := createEntry(t, owner, outlayBody(map[string]any{"description": "Boremaskin"}))
	approvedBy(t, owner, boss, company.Id, employee.Id)

	if caps := getEntry(t, boss, company.Id).Capabilities; caps.CanMarkReimbursed {
		t.Error("canMarkReimbursed is true on an outlay the company paid for itself")
	}
	if caps := getEntry(t, boss, employee.Id).Capabilities; !caps.CanMarkReimbursed {
		t.Error("canMarkReimbursed is false on an approved outlay the employee paid")
	}
}

// TestExpensesReimbursements_APageNumberTooBigForTheOffsetIsARefusal: page ×
// pageSize is computed in the int32 the OFFSET is sent as, so an unbounded
// page number overflows it into a negative offset the database refuses — a 500
// for a query that broke no documented rule. Every list in this module bounds
// it on the same helper.
func TestExpensesReimbursements_APageNumberTooBigForTheOffsetIsARefusal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")

	for _, path := range []string{
		entriesPath + "?page=21474838&pageSize=100",
		approvalsPath + "?page=21474838&pageSize=100",
		reimbursementsPath + "?page=21474838&pageSize=100",
	} {
		if r := boss.Do(http.MethodGet, path, nil); r.Status != http.StatusBadRequest {
			t.Errorf("GET %s: status %d body %s, want 400", path, r.Status, r.Body)
		}
	}
}
