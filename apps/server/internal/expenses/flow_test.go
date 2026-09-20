package expenses_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is decision X4's flow: submit, approve, reject and unapprove, the
// freezing a submit does, the receipt rule and the period lock over all four.
// The queue is approvals_test.go's, the rate override rateoverride_test.go's
// and the project side's pricing billing_test.go's.

func TestExpensesFlow_ADraftIsSubmittedApprovedAndUnapproved(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	approver, approverID := signIn(t, h, "expenses:approve")

	entry := createEntry(t, owner, outlayBody(nil))
	if entry.Status != "draft" || entry.SubmittedAt != nil || entry.Decision != nil {
		t.Fatalf("created = %+v, want an undecided draft", entry)
	}

	submitted := submitEntries(t, owner, entry.Id)
	if len(submitted) != 1 || submitted[0].Status != "submitted" || submitted[0].SubmittedAt == nil {
		t.Fatalf("submitted = %+v, want one submitted expense with a stamp", submitted)
	}
	if submitted[0].Decision != nil {
		t.Errorf("submitted = %+v, want no decision on it yet", submitted[0])
	}

	approved := approveEntries(t, approver, entry.Id)
	if len(approved) != 1 || approved[0].Status != "approved" || approved[0].Decision == nil {
		t.Fatalf("approved = %+v, want one approved expense with a decision stamp", approved)
	}
	// Who decided is on the expense, for its owner as much as for anyone else.
	if d := approved[0].Decision; d.Status != "approved" || d.By.UserId != approverID || d.At == "" {
		t.Errorf("decision = %+v, want it approved by the approver", d)
	}
	if owned := getEntry(t, owner, entry.Id); owned.Decision == nil || owned.Decision.By.UserId != approverID {
		t.Errorf("the owner's copy = %+v, want the approver named to them too", owned.Decision)
	}
	if got := h.Count(t, `SELECT count(*) FROM expenses.entries WHERE id = $1 AND decided_by_user_id = $2`,
		entry.Id, approverID); got != 1 {
		t.Errorf("the approver was not recorded on the expense")
	}

	back := unapproveEntries(t, approver, entry.Id)
	if len(back) != 1 || back[0].Status != "draft" {
		t.Fatalf("unapproved = %+v, want a draft again", back)
	}
	if back[0].SubmittedAt != nil || back[0].Decision != nil {
		t.Errorf("unapproved = %+v, want a fresh draft with every stamp cleared", back[0])
	}
	if got := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND user_id = $2 AND decided_by_user_id IS NULL AND submitted_at IS NULL`,
		entry.Id, ownerID); got != 1 {
		t.Errorf("the decision columns were not cleared: %s", entryColumnsDump(t, h, entry.Id))
	}
}

func TestExpensesFlow_RejectSendsItBackWithAReason(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	entry := createEntry(t, owner, outlayBody(nil))
	submitEntries(t, owner, entry.Id)

	rejected := rejectEntries(t, approver, "Mangler kvittering fra leverandøren", entry.Id)
	if len(rejected) != 1 || rejected[0].Status != "rejected" {
		t.Fatalf("rejected = %+v, want one rejected expense", rejected)
	}
	d := rejected[0].Decision
	if d == nil || d.Reason == nil || *d.Reason != "Mangler kvittering fra leverandøren" {
		t.Errorf("decision = %+v, want the reason given", d)
	}
	if d == nil || d.Status != "rejected" || d.At == "" || d.By.DisplayName == "" {
		t.Errorf("decision = %+v, want it stamped, named and rejected", d)
	}

	// A rejected expense is its owner's again: editable, deletable, and
	// submittable without an edit in between.
	seen := getEntry(t, owner, entry.Id)
	if !seen.Capabilities.CanEdit || !seen.Capabilities.CanDelete || !seen.Capabilities.CanSubmit {
		t.Errorf("capabilities on a rejected expense = %+v, want it open to its owner again", seen.Capabilities)
	}
	again := submitEntries(t, owner, entry.Id)
	if again[0].Status != "submitted" || again[0].Decision != nil {
		t.Errorf("resubmitted = %+v, want the previous decision cleared", again[0])
	}
}

func TestExpensesFlow_RejectNeedsAReason(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	entry := createEntry(t, owner, outlayBody(nil))
	submitEntries(t, owner, entry.Id)

	for name, reason := range map[string]any{
		"none":    nil,
		"blank":   "   ",
		"toolong": strings.Repeat("x", 1001),
	} {
		t.Run(name, func(t *testing.T) {
			errs := refusedFlow(t, approver, rejectPath, flowBody([]int64{entry.Id}, map[string]any{"reason": reason}))
			if len(errs["reason"]) == 0 {
				t.Fatalf("errors = %v, want one on reason", errs)
			}
		})
	}
	if got := getEntry(t, owner, entry.Id); got.Status != "submitted" {
		t.Errorf("status = %q, want the expense untouched by the refused rejections", got.Status)
	}
}

// Decision X4: on submit the rate, the amounts and the bill amount are stored
// as they stand and stop following the tables. Changing the rate table or the
// settings afterwards moves nothing.
func TestExpensesFlow_SubmitFreezesTheRatesAndAmounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)

	mileage := createEntry(t, owner, mileageBody(map[string]any{"passengers": 2}))
	// 120 km × 5.30 + 120 × 1.00 × 2 = 636 + 240.
	if mileage.GrossAmount != 876 || mileage.Rate == nil || *mileage.Rate != 5.30 {
		t.Fatalf("draft = %+v, want it priced from the seeded rates", mileage)
	}

	submitEntries(t, owner, mileage.Id)

	// A new rate from a day before the line's own would reprice a draft.
	createRate(t, admin, map[string]any{"kind": "mileage", "validFrom": "2026-03-01", "value": 9.00})
	createRate(t, admin, map[string]any{"kind": "mileage_passenger", "validFrom": "2026-03-01", "value": 3.00})

	frozen := getEntry(t, owner, mileage.Id)
	if frozen.GrossAmount != 876 || frozen.Rate == nil || *frozen.Rate != 5.30 {
		t.Errorf("after the rate table moved, the submitted line = %+v, want it frozen at 876 and 5.30", frozen)
	}
	if frozen.PassengerRate == nil || *frozen.PassengerRate != 1.00 {
		t.Errorf("passengerRate = %v, want the rate it was submitted at", frozen.PassengerRate)
	}
}

// The other half of the freeze: a submit prices the line one last time, so a
// rate added while it was still a draft does reach it.
func TestExpensesFlow_SubmitPricesTheLineOneLastTime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)

	mileage := createEntry(t, owner, mileageBody(nil))
	if mileage.GrossAmount != 636 {
		t.Fatalf("draft = %+v, want 120 km at the seeded 5.30", mileage)
	}
	createRate(t, admin, map[string]any{"kind": "mileage", "validFrom": "2026-03-01", "value": 6.00})

	submitted := submitEntries(t, owner, mileage.Id)
	if submitted[0].GrossAmount != 720 || submitted[0].Rate == nil || *submitted[0].Rate != 6.00 {
		t.Errorf("submitted = %+v, want it repriced at 6.00 on the way in (720)", submitted[0])
	}
}

// Design §4: rejected → editable → refrozen on the next submit.
func TestExpensesFlow_ARejectedLineIsRepricedWhenItIsSubmittedAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(nil))
	submitEntries(t, owner, mileage.Id)
	rejectEntries(t, approver, "Feil dato", mileage.Id)

	createRate(t, admin, map[string]any{"kind": "mileage", "validFrom": "2026-03-01", "value": 7.00})
	again := submitEntries(t, owner, mileage.Id)
	if again[0].GrossAmount != 840 || again[0].Rate == nil || *again[0].Rate != 7.00 {
		t.Errorf("resubmitted = %+v, want it refrozen at the rate in force now (840)", again[0])
	}
}

// Design §8: a kind with no rate in force on the line's date cannot be
// submitted — the line stays a draft and says why.
func TestExpensesFlow_MileageWithNoRateInForceCannotBeSubmitted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	mileage := createEntry(t, owner, mileageBody(nil))
	h.Exec(t, `DELETE FROM expenses.rates WHERE kind = 'mileage'`)

	errs := refusedFlow(t, owner, submitPath, flowBody([]int64{mileage.Id}, nil))
	if !mentions(errs["entryIds"], "no mileage rate") {
		t.Fatalf("errors = %v, want one naming the missing mileage rate", errs)
	}
	if got := getEntry(t, owner, mileage.Id); got.Status != "draft" {
		t.Errorf("status = %q, want it left a draft", got.Status)
	}
}

// Design §4's receipt rule, at the boundary the word "exceeds" sets: strictly
// greater than the threshold. A threshold of zero therefore asks for a receipt
// on every employee-paid outlay.
func TestExpensesFlow_TheReceiptRule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		threshold any
		body      map[string]any
		wantRefus bool
	}{
		{"off: no threshold, no receipt, submitted", nil, map[string]any{"grossAmount": 100000.00}, false},
		{"at the threshold is not over it", 1250.00, nil, false},
		{"a hundredth over the threshold needs one", 1249.99, nil, true},
		{"zero asks for one on every employee-paid outlay", 0, map[string]any{"grossAmount": 1.00}, true},
		{"the company's own outlay is exempt", 0, map[string]any{"paidBy": "company"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			admin, _ := signIn(t, h, "expenses:manage")
			owner, _ := signIn(t, h)
			if tc.threshold != nil {
				putSettings(t, admin, settingsBody(map[string]any{"receiptRequiredOver": tc.threshold}))
			}

			entry := createEntry(t, owner, outlayBody(tc.body))
			if !tc.wantRefus {
				submitEntries(t, owner, entry.Id)
				return
			}
			errs := refusedFlow(t, owner, submitPath, flowBody([]int64{entry.Id}, nil))
			if !mentions(errs["entryIds"], "receipt") {
				t.Fatalf("errors = %v, want one naming the receipt rule", errs)
			}
			if got := getEntry(t, owner, entry.Id); got.Status != "draft" {
				t.Errorf("status = %q, want it left a draft", got.Status)
			}
			uploadReceipt(t, owner, entry.Id, "kvittering.pdf", "application/pdf", testPDF(8))
			submitEntries(t, owner, entry.Id)
		})
	}
}

// Mileage carries no receipt at all, so the rule cannot hold it back.
func TestExpensesFlow_TheReceiptRuleLeavesMileageAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)
	putSettings(t, admin, settingsBody(map[string]any{"receiptRequiredOver": 0}))

	mileage := createEntry(t, owner, mileageBody(nil))
	submitEntries(t, owner, mileage.Id)
}

// Global Constraints: a batch is all or nothing, with one explanation per
// offending id, and nothing moves when one of them is refused.
func TestExpensesFlow_BatchesAreAllOrNothingWithAReasonPerId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	good := createEntry(t, owner, outlayBody(nil))
	other := createEntry(t, owner, outlayBody(map[string]any{"description": "Parkering"}))
	submitEntries(t, owner, good.Id, other.Id)
	approveEntries(t, approver, other.Id)

	errs := refusedFlow(t, approver, approvePath, flowBody([]int64{good.Id, other.Id, 987654}, nil))
	messages := errs["entryIds"]
	if len(messages) != 2 {
		t.Fatalf("errors = %v, want one message for the approved one and one for the unknown id", errs)
	}
	if !mentions(messages, fmt.Sprintf("Expense %d", other.Id)) || !mentions(messages, "Expense 987654") {
		t.Errorf("messages = %v, want both offending ids named", messages)
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND status = 'submitted' AND decided_at IS NULL`, good.Id); n != 1 {
		t.Errorf("the approvable one moved: %s — the batch is all or nothing", entryColumnsDump(t, h, good.Id))
	}
}

func TestExpensesFlow_AtMost500IdsAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	ids := make([]int64, 501)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	errs := refusedFlow(t, owner, submitPath, flowBody(ids, nil))
	if !mentions(errs["entryIds"], "500") {
		t.Fatalf("errors = %v, want one naming the cap", errs)
	}
}

// The cap is on the expenses, not on the array: an id given twice is one
// expense everywhere else here, so a long body naming few expenses is not the
// thing the cap exists to refuse.
func TestExpensesFlow_TheCapCountsExpensesRatherThanIds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	entry := createEntry(t, owner, outlayBody(nil))
	ids := make([]int64, 600)
	for i := range ids {
		ids[i] = entry.Id
	}
	moved := submitEntries(t, owner, ids...)
	if len(moved) != 1 || moved[0].Status != "submitted" {
		t.Errorf("submitted = %+v, want the one expense the 600 ids name", moved)
	}
}

func TestExpensesFlow_NoIdsAtAllIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	for _, path := range []string{submitPath} {
		errs := refusedFlow(t, owner, path, flowBody([]int64{}, nil))
		if len(errs["entryIds"]) == 0 {
			t.Errorf("%s errors = %v, want one on entryIds", path, errs)
		}
	}
}

// claimIds is in the contract for the travel claims of a later delivery. Until
// they arrive a request carrying one is refused on that field.
func TestExpensesFlow_ClaimIdsAreRefusedUntilTravelClaimsArrive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	entry := createEntry(t, owner, outlayBody(nil))
	errs := refusedFlow(t, owner, submitPath, flowBody([]int64{entry.Id}, map[string]any{"claimIds": []int64{7}}))
	if len(errs["claimIds"]) == 0 {
		t.Fatalf("errors = %v, want one on claimIds", errs)
	}
	if got := getEntry(t, owner, entry.Id); got.Status != "draft" {
		t.Errorf("status = %q, want nothing submitted", got.Status)
	}

	// An empty array is not a claim, and is accepted.
	submitEntries(t, owner, entry.Id)
}

// Decision X10: a caller who could approve nothing at all is refused the whole
// request, as the access layer would, rather than told about each id.
func TestExpensesFlow_ACallerWhoApprovesNothingIsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	// view-all sees every expense and approves none of them.
	bystander, _ := signIn(t, h, "expenses:view-all")

	entry := createEntry(t, owner, outlayBody(nil))
	submitEntries(t, owner, entry.Id)

	for _, path := range []string{approvePath, rejectPath, unapprovePath} {
		body := flowBody([]int64{entry.Id}, nil)
		if path == rejectPath {
			body["reason"] = "Nei"
		}
		if r := bystander.Do(http.MethodPost, path, body); r.Status != http.StatusForbidden {
			t.Errorf("post %s: status %d body %s, want 403", path, r.Status, r.Body)
		}
	}
	if r := bystander.Do(http.MethodGet, approvalsPath, nil); r.Status != http.StatusForbidden {
		t.Errorf("get the queue: status %d body %s, want 403", r.Status, r.Body)
	}
}

// expenses:manage unapproves anywhere, so it is not a caller who approves
// nothing — but it still approves nothing, and the two batch decisions refuse
// it outright.
func TestExpensesFlow_ManageUnapprovesButDoesNotApprove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")
	admin, _ := signIn(t, h, "expenses:manage")

	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, approver, entry.Id)

	if r := admin.Do(http.MethodPost, approvePath, flowBody([]int64{entry.Id}, nil)); r.Status != http.StatusForbidden {
		t.Errorf("manage approving: status %d body %s, want 403", r.Status, r.Body)
	}
	back := unapproveEntries(t, admin, entry.Id)
	if back[0].Status != "draft" {
		t.Errorf("unapproved by manage = %+v, want a draft", back[0])
	}
}

// Decision X9: with a project, that project's managers approve too — and only
// their own project's lines. Another project's line, and one on no project at
// all, are not theirs to see either, so those read as the unknown id does.
func TestExpensesFlow_AProjectManagerApprovesTheirOwnProjectsLinesOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	h.projects.addRole(projectEuro, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	mine := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	theirs := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectEuro, "currency": "EUR", "description": "Hotell i Berlin",
	}))
	elsewhere := createEntry(t, owner, outlayBody(map[string]any{"description": "Ingen prosjekt"}))
	submitEntries(t, owner, mine.Id, theirs.Id, elsewhere.Id)

	approveEntries(t, manager, mine.Id)

	for _, id := range []int64{theirs.Id, elsewhere.Id} {
		errs := refusedFlow(t, manager, approvePath, flowBody([]int64{id}, nil))
		if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d was not found", id)) {
			t.Errorf("approving %d: errors = %v, want the unknown id's own words", id, errs)
		}
	}
}

// "Not yours to approve" is for an expense the caller can see and cannot
// decide: it says no more than a 404 would — that it exists, and that it is
// somebody else's call.
func TestExpensesFlow_SeenButNotApprovableSaysOnlyThat(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	// Sees every expense (view-all) and approves only one project's, so the
	// whole request is not refused outright.
	watcher, _ := signInAs(t, h, projectEuro, roleManager, "expenses:view-all")

	entry := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	submitEntries(t, owner, entry.Id)

	errs := refusedFlow(t, watcher, approvePath, flowBody([]int64{entry.Id}, nil))
	if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d is not yours to approve", entry.Id)) {
		t.Errorf("errors = %v, want it refused as not theirs to approve", errs)
	}
	if got := getEntry(t, watcher, entry.Id); got.Capabilities.CanApprove {
		t.Errorf("canApprove = true, want the capability to say what the server would do")
	}
}

// An expense the caller cannot see at all reads exactly as an unknown id does,
// so a refusal tells a stranger nothing.
func TestExpensesFlow_AnExpenseTheCallerCannotSeeReadsAsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	// A manager of another project: an approver of something, so not the 403,
	// but blind to this expense.
	manager, _ := signInAs(t, h, projectEuro, roleManager)

	entry := createEntry(t, owner, outlayBody(nil))
	submitEntries(t, owner, entry.Id)

	errs := refusedFlow(t, manager, approvePath, flowBody([]int64{entry.Id, 987654}, nil))
	messages := errs["entryIds"]
	if len(messages) != 2 {
		t.Fatalf("errors = %v, want a message for each id", errs)
	}
	want := []string{
		fmt.Sprintf("Expense %d was not found", entry.Id),
		"Expense 987654 was not found",
	}
	for _, msg := range want {
		if !mentions(messages, msg) {
			t.Errorf("messages = %v, want %q — the unseen expense reads as the unknown id", messages, msg)
		}
	}
}

// Decision X9 and the sibling: self-approval is allowed.
func TestExpensesFlow_SelfApprovalIsAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h, "expenses:approve")

	entry := createEntry(t, owner, outlayBody(nil))
	submitEntries(t, owner, entry.Id)
	approved := approveEntries(t, owner, entry.Id)
	if approved[0].Status != "approved" {
		t.Errorf("status = %q, want the approver's own expense approved", approved[0].Status)
	}
}

// A submit is the owner's, or expenses:manage's on their behalf; nobody else's.
func TestExpensesFlow_SubmitIsTheOwnersOrManages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")
	admin, _ := signIn(t, h, "expenses:manage")

	first := createEntry(t, owner, outlayBody(nil))
	errs := refusedFlow(t, approver, submitPath, flowBody([]int64{first.Id}, nil))
	if !mentions(errs["entryIds"], "is not yours") {
		t.Fatalf("errors = %v, want it refused as not the approver's to submit", errs)
	}
	submitEntries(t, admin, first.Id)
}

// Decision X5's two tracks are Task 5's, but their columns are here already:
// an expense that has been reimbursed or invoiced is no longer unapprovable.
func TestExpensesFlow_UnapproveRefusesWhatIsReimbursedOrInvoiced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	for _, column := range []string{"reimbursed_at", "invoiced_at"} {
		entry := createEntry(t, owner, outlayBody(map[string]any{"description": "Til " + column}))
		approvedBy(t, owner, approver, entry.Id)
		h.Exec(t, fmt.Sprintf(`UPDATE expenses.entries SET %s = now() WHERE id = $1`, column), entry.Id)

		errs := refusedFlow(t, approver, unapprovePath, flowBody([]int64{entry.Id}, nil))
		if len(errs["entryIds"]) != 1 {
			t.Fatalf("%s: errors = %v, want one message", column, errs)
		}
		if got := getEntry(t, owner, entry.Id); got.Status != "approved" {
			t.Errorf("%s: status = %q, want it left approved", column, got.Status)
		}
		if got := getEntry(t, approver, entry.Id); got.Capabilities.CanUnapprove {
			t.Errorf("%s: canUnapprove = true, want the capability to say what the server would do", column)
		}
	}
}

// Design §4: nothing dated before the lock is submitted, approved, rejected or
// unapproved — except by expenses:manage.
func TestExpensesFlow_ThePeriodLockHoldsEveryMoveBackButManages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	locked := createEntry(t, owner, outlayBody(nil))
	toApprove := createEntry(t, owner, outlayBody(map[string]any{"description": "Skal godkjennes"}))
	toUnapprove := createEntry(t, owner, outlayBody(map[string]any{"description": "Skal av-godkjennes"}))
	submitEntries(t, owner, toApprove.Id, toUnapprove.Id)
	approveEntries(t, approver, toUnapprove.Id)

	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-04-01"}))

	for _, tc := range []struct {
		path string
		id   int64
		as   *modtest.Client
	}{
		{submitPath, locked.Id, owner},
		{approvePath, toApprove.Id, approver},
		{rejectPath, toApprove.Id, approver},
		{unapprovePath, toUnapprove.Id, approver},
	} {
		body := flowBody([]int64{tc.id}, nil)
		if tc.path == rejectPath {
			body["reason"] = "Nei"
		}
		errs := refusedFlow(t, tc.as, tc.path, body)
		if !mentions(errs["entryIds"], "2026-04-01") {
			t.Errorf("%s: errors = %v, want one naming the lock date", tc.path, errs)
		}
	}

	// expenses:manage works past it: submit, approve and unapprove all land.
	submitEntries(t, admin, locked.Id)
	if got := getEntry(t, admin, toUnapprove.Id); !got.Capabilities.CanUnapprove {
		t.Errorf("canUnapprove for manage past the lock = false, want true")
	}
	unapproveEntries(t, admin, toUnapprove.Id)
}

// Design §8 through entryStateRefusal: a submitted or an approved expense is no
// longer its owner's to change, and says which state refuses it.
func TestExpensesFlow_ASettledExpenseIsNoLongerEditable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	for _, status := range []string{"submitted", "approved"} {
		entry := createEntry(t, owner, outlayBody(map[string]any{"description": "Blir " + status}))
		submitEntries(t, owner, entry.Id)
		if status == "approved" {
			approveEntries(t, approver, entry.Id)
		}

		body := outlayBody(map[string]any{"revision": entry.Revision, "description": "Endret"})
		if errs := refusedEntry(t, owner, http.MethodPut, entryPath(entry.Id), body); len(errs["status"]) == 0 {
			t.Errorf("%s: PUT errors = %v, want one on status", status, errs)
		}
		if errs := refusedEntry(t, owner, http.MethodDelete, entryPath(entry.Id), nil); len(errs["status"]) == 0 {
			t.Errorf("%s: DELETE errors = %v, want one on status", status, errs)
		}
		errs := refusedReceipt(t, owner, entry.Id, "kvittering.pdf", "application/pdf", testPDF(8))
		if len(errs["entryId"]) == 0 {
			t.Errorf("%s: upload errors = %v, want one on entryId", status, errs)
		}

		seen := getEntry(t, owner, entry.Id)
		if seen.Capabilities.CanEdit || seen.Capabilities.CanDelete || seen.Capabilities.CanSubmit {
			t.Errorf("%s: capabilities = %+v, want nothing open to its owner", status, seen.Capabilities)
		}
	}
}

// The capability table, per status, for the three caller kinds that have any.
func TestExpensesFlow_CapabilitiesFollowTheStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	entry := createEntry(t, owner, outlayBody(nil))
	check := func(status string, c *modtest.Client, want entryCapabilitiesJSON) {
		t.Helper()
		got := getEntry(t, c, entry.Id).Capabilities
		if got != want {
			t.Errorf("%s: capabilities = %+v, want %+v", status, got, want)
		}
	}
	check("draft/owner", owner, entryCapabilitiesJSON{CanEdit: true, CanDelete: true, CanSubmit: true})
	check("draft/approver", approver, entryCapabilitiesJSON{})

	submitEntries(t, owner, entry.Id)
	check("submitted/owner", owner, entryCapabilitiesJSON{})
	check("submitted/approver", approver, entryCapabilitiesJSON{CanApprove: true})

	approveEntries(t, approver, entry.Id)
	check("approved/owner", owner, entryCapabilitiesJSON{})
	check("approved/approver", approver, entryCapabilitiesJSON{CanUnapprove: true})

	unapproveEntries(t, approver, entry.Id)
	submitEntries(t, owner, entry.Id)
	rejectEntries(t, approver, "Nei", entry.Id)
	check("rejected/owner", owner, entryCapabilitiesJSON{CanEdit: true, CanDelete: true, CanSubmit: true})
	check("rejected/approver", approver, entryCapabilitiesJSON{})
}

// Decisions X1 and X2: without the projects module the flow is the same one,
// decided on expenses:approve alone.
func TestExpensesFlow_WithoutProjects_IsTheSameFlowOnThePermissionAlone(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")
	bystander, _ := signIn(t, h, "expenses:view-all")

	entry := createEntry(t, owner, outlayBody(nil))
	submitEntries(t, owner, entry.Id)
	if r := bystander.Do(http.MethodPost, approvePath, flowBody([]int64{entry.Id}, nil)); r.Status != http.StatusForbidden {
		t.Errorf("a caller who approves nothing: status %d, want 403", r.Status)
	}
	approved := approveEntries(t, approver, entry.Id)
	if approved[0].Status != "approved" {
		t.Fatalf("approved = %+v, want it approved without the projects module", approved[0])
	}
	unapproveEntries(t, approver, entry.Id)
}

// A line the projects module has lost — or one recorded while it was installed
// and read in an installation without it — freezes its stored project columns
// rather than repricing what nothing can judge.
func TestExpensesFlow_WithoutProjects_SubmitCarriesTheStoredProjectColumns(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	owner, ownerID := signIn(t, h)
	id := seedProjectedEntry(t, h, ownerID)

	submitEntries(t, owner, id)
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND status = 'submitted' AND project_id = $2 AND billing_line_id = $3
		  AND billable AND markup_percent = 15.00 AND bill_amount = 1150.00`,
		id, int32(projectKraftVerket), int32(lineFixed)); n != 1 {
		t.Errorf("the stored project columns did not survive the submit: %s", entryColumnsDump(t, h, id))
	}
}

// A project the directory bills nothing for forces billable false, and the
// submit that refreezes the figures keeps applying that rule.
func TestExpensesFlow_SubmitRefreezesTheBillAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "vatAmount": 250.00,
	}))
	// The settings' default markup is 0, so nothing is added yet.
	if seen := getEntry(t, manager, entry.Id); seen.Billing == nil || seen.Billing.BillAmount != 1000 {
		t.Fatalf("billing = %+v, want the net at the default markup of zero", seen.Billing)
	}
	putSettings(t, admin, settingsBody(map[string]any{"defaultMarkupPercent": 20}))

	// The line already carries a markup of its own (the old default), so the
	// new one does not reach it — what is stored is kept, exactly as an edit
	// keeps it.
	submitEntries(t, owner, entry.Id)
	if seen := getEntry(t, manager, entry.Id); seen.Billing == nil || seen.Billing.BillAmount != 1000 {
		t.Errorf("billing after the submit = %+v, want the stored markup kept", seen.Billing)
	}
}

// The freeze prices the customer's side one last time too: a billable mileage
// line recorded while no customer rate was in force is saved with nothing
// billed (Task 2's rule, so an employee is never refused over a price they may
// not know exists), and the submit picks up the rate that has since arrived.
func TestExpensesFlow_SubmitPricesTheCustomerSideOneLastTime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, mileageBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))
	before := getEntry(t, manager, entry.Id)
	if before.Billing == nil || before.Billing.BillRatePerKm != nil || before.Billing.BillAmount != 0 {
		t.Fatalf("billing = %+v, want it billable with nothing billed yet", before.Billing)
	}

	createRate(t, admin, map[string]any{"kind": "mileage_customer", "validFrom": "2026-01-01", "value": 9.00})
	submitEntries(t, owner, entry.Id)

	after := getEntry(t, manager, entry.Id)
	if after.Billing == nil || after.Billing.BillAmount != 1080 {
		t.Errorf("billing after the submit = %+v, want 120 km at the 9.00 now in force", after.Billing)
	}
	if after.Billing.BillRatePerKm == nil || *after.Billing.BillRatePerKm != 9 {
		t.Errorf("billRatePerKm = %v, want the rate frozen onto the line", after.Billing.BillRatePerKm)
	}
	// And frozen it is: a later rate does not move it.
	createRate(t, admin, map[string]any{"kind": "mileage_customer", "validFrom": "2026-02-01", "value": 20.00})
	if got := getEntry(t, manager, entry.Id); got.Billing.BillAmount != 1080 {
		t.Errorf("billAmount = %v, want it frozen at 1080", got.Billing.BillAmount)
	}
}

// One id given twice is one expense, not a refusal and not a double move.
func TestExpensesFlow_ARepeatedIdIsOneExpense(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	entry := createEntry(t, owner, outlayBody(nil))
	moved := submitEntries(t, owner, entry.Id, entry.Id)
	if len(moved) != 1 || moved[0].Status != "submitted" {
		t.Errorf("submitted = %+v, want one expense", moved)
	}
	if got := getEntry(t, owner, entry.Id).Revision; got != 2 {
		t.Errorf("revision = %d, want it moved once", got)
	}
}
