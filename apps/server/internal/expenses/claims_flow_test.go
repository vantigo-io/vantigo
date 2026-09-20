package expenses_test

import (
	"fmt"
	"net/http"
	"testing"
)

// This file is the travel claim as the **unit** that moves: submitted,
// approved, rejected and unapproved as one thing, in the very batches a
// standalone expense moves through. What a line's own id gets from those
// batches is still the refusal that points at the claim (claims_lines_test.go);
// what the claim gets is here.

// The whole round trip, on a trip: draft → submitted → approved, with the
// figures frozen on the way and the stamps on the claim rather than on any of
// its lines.
func TestExpensesClaimFlow_SubmitsApprovesAndStampsTheClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	boss, bossID := signIn(t, h, "expenses:approve")

	claim := createClaim(t, owner, nil)
	outlay := addLine(t, owner, claim.Id, outlayBody(nil))
	trip := addLine(t, owner, claim.Id, mileageBody(nil))

	moved := submitClaims(t, owner, claim.Id)
	if len(moved) != 1 || moved[0].Status != "submitted" {
		t.Fatalf("the submit answered %+v, want the claim as submitted", moved)
	}
	submitted := getClaim(t, owner, claim.Id)
	if submitted.Status != "submitted" || submitted.SubmittedAt == nil {
		t.Errorf("the claim is %+v, want it submitted and stamped", submitted)
	}
	// Every line reads as submitted through the claim, and none of them carries
	// a status of its own in the database.
	for _, line := range submitted.Lines {
		if line.Status != "submitted" {
			t.Errorf("line %d reads %s, want its claim's status", line.Id, line.Status)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE claim_id = $1 AND status = 'draft' AND submitted_at IS NULL`, claim.Id); n != 2 {
		t.Errorf("%d of the claim's lines still stand at their own default, want both — "+
			"the status that moved is the claim's", n)
	}

	approveClaims(t, boss, claim.Id)
	approved := getClaim(t, owner, claim.Id)
	switch {
	case approved.Status != "approved":
		t.Errorf("the claim is %s, want approved", approved.Status)
	case approved.Decision == nil || approved.Decision.By.UserId != bossID:
		t.Errorf("decision = %+v, want it stamped by the approver", approved.Decision)
	}
	if approved.Owner.UserId != ownerID {
		t.Errorf("owner = %+v, want the trip's own", approved.Owner)
	}
	// And each line answers the claim's decision, through the one renderer.
	for _, line := range approved.Lines {
		if line.Status != "approved" || line.Decision == nil {
			t.Errorf("line %d = %+v, want its claim's decision", line.Id, line)
		}
	}
	_ = outlay
	_ = trip
}

// Submit freezes every line of the trip, whatever its kind, and nothing moves
// them afterwards — not a rate an administrator changes, not the approval.
func TestExpensesClaimFlow_SubmitFreezesEveryLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)
	boss, _ := signIn(t, h, "expenses:approve")

	claim := createClaim(t, owner, nil)
	mileage := addLine(t, owner, claim.Id, mileageBody(nil))
	day := addLine(t, owner, claim.Id, perDiemBody(nil))

	submitClaims(t, owner, claim.Id)
	// The tables move under the trip *after* it was submitted.
	createRate(t, admin, map[string]any{"kind": "mileage", "validFrom": "2026-03-05", "value": 99.00})
	createRate(t, admin, map[string]any{"kind": "per_diem_6_12", "validFrom": "2026-03-05", "value": 1.00})
	approveClaims(t, boss, claim.Id)

	after := getClaim(t, owner, claim.Id)
	for _, want := range []struct {
		id    int64
		gross float64
	}{
		{mileage.Id, 636.00}, // 120 km at the seeded 5.30
		{day.Id, 397.00},     // the seeded day rate
	} {
		if got := lineByID(t, after.Lines, want.id); got.GrossAmount != want.gross {
			t.Errorf("line %d is %.2f, want the %.2f it was frozen at", want.id, got.GrossAmount, want.gross)
		}
	}
}

// A submit prices each line one last time from the table as it stands *then*,
// so a rate an administrator corrects before the trip goes in is the one it
// goes in at — and a rejection and a second submit price it again.
func TestExpensesClaimFlow_ARejectedClaimIsRepricedWhenItGoesAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)
	boss, _ := signIn(t, h, "expenses:approve")

	claim := createClaim(t, owner, nil)
	mileage := addLine(t, owner, claim.Id, mileageBody(nil))
	submitClaims(t, owner, claim.Id)
	rejectClaims(t, boss, "Feil kilometerstand", claim.Id)

	rejected := getClaim(t, owner, claim.Id)
	if rejected.Status != "rejected" || rejected.Decision == nil ||
		rejected.Decision.Reason == nil || *rejected.Decision.Reason != "Feil kilometerstand" {
		t.Fatalf("the claim is %+v, want it rejected with the reason", rejected)
	}

	createRate(t, admin, map[string]any{"kind": "mileage", "validFrom": "2026-03-01", "value": 6.00})
	submitClaims(t, owner, claim.Id)
	again := getClaim(t, owner, claim.Id)
	if got := lineByID(t, again.Lines, mileage.Id); got.GrossAmount != 720.00 {
		t.Errorf("the line is %.2f, want 720.00 — the second submit reprices it at 6.00", got.GrossAmount)
	}
	if again.Decision != nil {
		t.Errorf("decision = %+v, want the rejection cleared by the new submit", again.Decision)
	}
}

// A trip with nothing in it cannot be submitted: there is nothing to freeze and
// nothing to approve. The capability says so too, so no client offers a button
// that answers 400.
func TestExpensesClaimFlow_AnEmptyClaimCannotBeSubmitted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	claim := createClaim(t, owner, nil)
	if claim.Capabilities.CanSubmit {
		t.Errorf("canSubmit is true on a trip with no lines, want false")
	}
	errs := refused(t, owner, http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil), invalidSubmissionTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d holds no expenses", claim.Id)) {
		t.Errorf("errors %v do not say the trip is empty", errs)
	}

	addLine(t, owner, claim.Id, outlayBody(nil))
	if got := getClaim(t, owner, claim.Id); !got.Capabilities.CanSubmit {
		t.Errorf("canSubmit is false once the trip holds a line, want true")
	}
	submitClaims(t, owner, claim.Id)
}

// The receipt rule is per employee-paid outlay **line**, judged under the
// claim's own locks, and the refusal names the line so its owner knows which
// receipt to attach.
func TestExpensesClaimFlow_TheReceiptRuleAppliesPerLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	putSettings(t, admin, settingsBody(map[string]any{"receiptRequiredOver": 1000.00}))
	owner, _ := signIn(t, h)

	claim := createClaim(t, owner, nil)
	small := addLine(t, owner, claim.Id, outlayBody(map[string]any{"grossAmount": 900.00}))
	big := addLine(t, owner, claim.Id, outlayBody(map[string]any{"grossAmount": 1250.00}))
	addLine(t, owner, claim.Id, mileageBody(nil))

	errs := refused(t, owner, http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil), invalidSubmissionTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Expense %d needs a receipt", big.Id)) {
		t.Errorf("errors %v do not name the line over the threshold", errs)
	}
	if mentions(errs["claimIds"], fmt.Sprintf("Expense %d needs a receipt", small.Id)) {
		t.Errorf("errors %v name the line under the threshold, which needs none", errs)
	}
	if got := getClaim(t, owner, claim.Id); got.Status != "draft" {
		t.Errorf("the claim is %s, want it untouched — a refused submit moves nothing", got.Status)
	}

	uploadReceipt(t, owner, big.Id, "kvittering.png", "image/png", testPNG(t, 4, 4))
	submitClaims(t, owner, claim.Id)
}

// A line the tables can no longer price stops the whole submit and says which
// line and why — the mileage rate and the per diem day rate alike.
func TestExpensesClaimFlow_ALineWithNoRateRefusesTheSubmit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)

	claim := createClaim(t, owner, nil)
	day := addLine(t, owner, claim.Id, perDiemBody(nil))

	// The rate the day was saved at is removed from the table underneath it.
	for _, rate := range ratesOfKind(listRates(t, admin), "per_diem_6_12") {
		if r := admin.Do(http.MethodDelete, ratePath(rate.Id), nil); r.Status != http.StatusNoContent {
			t.Fatalf("delete rate %d: status %d body %s", rate.Id, r.Status, r.Body)
		}
	}
	errs := refused(t, owner, http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil), invalidSubmissionTitle)
	for _, want := range []string{
		fmt.Sprintf("Travel claim %d cannot be submitted", claim.Id),
		fmt.Sprintf("Expense %d cannot be priced", day.Id),
		"No per_diem_6_12 rate applies on 2026-03-10",
	} {
		if !mentions(errs["claimIds"], want) {
			t.Errorf("errors %v do not say %q", errs["claimIds"], want)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.claims WHERE id = $1 AND status = 'draft'`, claim.Id); n != 1 {
		t.Errorf("the claim moved, want a refused submit to change nothing")
	}
}

// Who submits, and from where: the owner or expenses:manage, and only from a
// draft or a rejected trip. Everybody else gets the refusal their standing
// earns — a stranger the unknown id's, a colleague "not yours".
func TestExpensesClaimFlow_RefusesWhoeverMayNotSubmitIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	stranger, _ := signIn(t, h)
	admin, _ := signIn(t, h, "expenses:manage")
	boss, _ := signIn(t, h, "expenses:approve")

	claim := createClaim(t, owner, nil)
	addLine(t, owner, claim.Id, outlayBody(nil))

	errs := refused(t, stranger, http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil), invalidSubmissionTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d was not found", claim.Id)) {
		t.Errorf("a stranger got %v, want the unknown id's message", errs)
	}
	// An approver sees the trip but does not own it.
	errs = refused(t, boss, http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil), invalidSubmissionTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d is not yours", claim.Id)) {
		t.Errorf("an approver got %v, want \"not yours\"", errs)
	}

	// expenses:manage submits on somebody else's behalf.
	submitClaims(t, admin, claim.Id)
	errs = refused(t, owner, http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil), invalidSubmissionTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d is submitted, and only a draft or a rejected travel claim can be submitted", claim.Id)) {
		t.Errorf("a second submit got %v, want the status refusal", errs)
	}
}

// Who approves a trip: expenses:approve, or a manager of the claim's own
// project. A manager of another project is refused, and the roles are read
// before the transaction opens.
func TestExpensesClaimFlow_AProjectsManagerApprovesItsTrips(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	other, _ := signInAs(t, h, projectInternal, roleManager)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)

	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	addLine(t, owner, claim.Id, outlayBody(nil))
	submitClaims(t, owner, claim.Id)

	// The manager of another project cannot even see it.
	errs := refused(t, other, http.MethodPost, approvePath, claimFlowBody([]int64{claim.Id}, nil), invalidApprovalTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d was not found", claim.Id)) {
		t.Errorf("another project's manager got %v, want the unknown id's message", errs)
	}
	approveClaims(t, manager, claim.Id)
	if got := getClaim(t, owner, claim.Id); got.Status != "approved" {
		t.Errorf("the claim is %s, want the project's manager to have approved it", got.Status)
	}
}

// A trip with no project at all is nobody's project manager's, so only
// expenses:approve reaches it — and a caller who approves nothing at all gets
// the access layer's 403 rather than a page of per-id refusals.
func TestExpensesClaimFlow_ACallerWhoApprovesNothingIsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	nobody, _ := signIn(t, h)

	claim := createClaim(t, owner, nil)
	addLine(t, owner, claim.Id, outlayBody(nil))
	submitClaims(t, owner, claim.Id)

	forbidden(t, nobody, http.MethodPost, approvePath, claimFlowBody([]int64{claim.Id}, nil))
}

// Unapprove takes the whole trip back to a fresh draft — decision, submission
// stamp and every line's rate-override audit — and is refused once the trip has
// been reimbursed or one of its lines invoiced.
func TestExpensesClaimFlow_UnapproveMakesItAFreshDraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	boss, _ := signIn(t, h, "expenses:approve")

	claim := createClaim(t, owner, nil)
	mileage := addLine(t, owner, claim.Id, mileageBody(nil))
	submitClaims(t, owner, claim.Id)
	// An approver corrects the rate while the trip is submitted, which records
	// the audit the unapprove has to clear.
	submitted := getClaim(t, owner, claim.Id)
	overrideRate(t, boss, mileage.Id, map[string]any{
		"rate": 8.00, "revision": lineByID(t, submitted.Lines, mileage.Id).Revision,
	})
	approveClaims(t, boss, claim.Id)

	unapproveClaims(t, boss, claim.Id)
	after := getClaim(t, owner, claim.Id)
	switch {
	case after.Status != "draft":
		t.Errorf("the claim is %s, want a fresh draft", after.Status)
	case after.Decision != nil || after.SubmittedAt != nil:
		t.Errorf("the claim is %+v, want the decision and the submission stamp cleared", after)
	}
	if got := lineByID(t, after.Lines, mileage.Id); got.RateOverride != nil {
		t.Errorf("the line still carries %+v, want the override audit cleared with the decision", got.RateOverride)
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND rate_overridden_by_user_id IS NULL AND rate_table_value IS NULL`, mileage.Id); n != 1 {
		t.Errorf("%s, want the audit columns cleared in SQL too", entryColumnsDump(t, h, mileage.Id))
	}
	// And the trip is its owner's again: a save reprices the line.
	updateClaim(t, owner, claim.Id, map[string]any{"revision": after.Revision, "purpose": "Ny tur"})
}

// A trip whose money has moved is not something to send back to its owner as a
// draft. Both doors refuse it — the reimbursement on the claim, the invoice on
// a line — and each says which.
func TestExpensesClaimFlow_UnapproveRefusesAPaidOrInvoicedTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)

	// One trip goes on a payroll run.
	paid := createClaim(t, owner, nil)
	addLine(t, owner, paid.Id, outlayBody(nil))
	approvedClaimBy(t, owner, admin, paid.Id)
	markClaimsReimbursed(t, admin, reimbursedClaimBody([]int64{paid.Id}, nil))
	errs := refused(t, admin, http.MethodPost, unapprovePath, claimFlowBody([]int64{paid.Id}, nil), invalidApprovalTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d has been reimbursed", paid.Id)) {
		t.Errorf("errors %v do not say the trip has been paid", errs)
	}

	// The other has a line on an invoice.
	billed := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	line := addLine(t, owner, billed.Id, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "billingLineId": lineFixed,
	}))
	approvedClaimBy(t, owner, admin, billed.Id)
	markInvoiced(t, manager, line.Id, map[string]any{"revision": getEntry(t, manager, line.Id).Revision})
	errs = refused(t, admin, http.MethodPost, unapprovePath, claimFlowBody([]int64{billed.Id}, nil), invalidApprovalTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d holds expense %d, which has been invoiced", billed.Id, line.Id)) {
		t.Errorf("errors %v do not name the invoiced line", errs)
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.claims WHERE id = $1 AND status = 'approved'`, billed.Id); n != 1 {
		t.Errorf("the claim moved, want a refused unapprove to change nothing")
	}
}

// The period lock judges a trip on the day it **departed**, whatever the dates
// of its lines — and expenses:manage works past it, here as everywhere.
func TestExpensesClaimFlow_ThePeriodLockJudgesTheDeparture(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)

	claim := createClaim(t, owner, nil) // departs 2026-03-09
	addLine(t, owner, claim.Id, outlayBody(nil))
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-03-10"}))

	errs := refused(t, owner, http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil), invalidSubmissionTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d departed before 2026-03-10, the lock date", claim.Id)) {
		t.Errorf("errors %v do not name the departure and the lock", errs)
	}
	// The owner's own capability agrees.
	if got := getClaim(t, owner, claim.Id); got.Capabilities.CanSubmit {
		t.Errorf("canSubmit is true inside the lock, want false")
	}
	// expenses:manage is exempt.
	submitClaims(t, admin, claim.Id)
}

// A mixed batch: expenses and trips in one request, all or nothing across both
// lists, with the refusals keyed so a client can tell which list an id came
// from.
func TestExpensesClaimFlow_AMixedBatchIsAllOrNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	boss, _ := signIn(t, h, "expenses:approve")

	entry := createEntry(t, owner, outlayBody(nil))
	claim := createClaim(t, owner, nil)
	line := addLine(t, owner, claim.Id, outlayBody(nil))
	empty := createClaim(t, owner, map[string]any{"purpose": "Tom tur"})

	// One bad id in either list stops the whole request.
	errs := refused(t, owner, http.MethodPost, submitPath, map[string]any{
		"entryIds": []int64{entry.Id}, "claimIds": []int64{claim.Id, empty.Id},
	}, invalidSubmissionTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d holds no expenses", empty.Id)) {
		t.Errorf("errors %v do not name the empty trip", errs)
	}
	if _, ok := errs["entryIds"]; ok {
		t.Errorf("errors %v report on entryIds, want the refusal under the list that named it", errs)
	}
	for _, id := range []int64{entry.Id} {
		if got := getEntry(t, owner, id); got.Status != "draft" {
			t.Errorf("expense %d is %s, want a refused batch to move nothing", id, got.Status)
		}
	}

	// A line of a trip named on entryIds is still refused per id, pointing at
	// the claim, and that refusal is on entryIds.
	errs = refused(t, owner, http.MethodPost, submitPath, map[string]any{
		"entryIds": []int64{line.Id}, "claimIds": []int64{claim.Id},
	}, invalidSubmissionTitle)
	if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d belongs to travel claim %d; submit the claim", line.Id, claim.Id)) {
		t.Errorf("errors %v do not point the line at its claim", errs)
	}

	// And a batch every id of which may move, moves all of it and answers both
	// lists in the order they were given.
	moved := moveUnits(t, owner, submitPath, map[string]any{
		"entryIds": []int64{entry.Id}, "claimIds": []int64{claim.Id},
	})
	if len(moved.Entries) != 1 || moved.Entries[0].Id != entry.Id {
		t.Errorf("entries = %+v, want the one expense", moved.Entries)
	}
	if len(moved.Claims) != 1 || moved.Claims[0].Id != claim.Id {
		t.Errorf("claims = %+v, want the one trip", moved.Claims)
	}
	approveClaims(t, boss, claim.Id)
	approveEntries(t, boss, entry.Id)
}

// The batch's own rules, on the two lists together: at least one id anywhere,
// and at most 500 distinct units across both.
func TestExpensesClaimFlow_CountsTheCapAcrossBothLists(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	errs := refused(t, owner, http.MethodPost, submitPath, map[string]any{}, invalidSubmissionTitle)
	if !mentions(errs["entryIds"], "At least one expense or travel claim id is required") {
		t.Errorf("errors %v do not ask for an id", errs)
	}
	// An empty pair of lists is the same thing.
	errs = refused(t, owner, http.MethodPost, submitPath, map[string]any{
		"entryIds": []int64{}, "claimIds": []int64{},
	}, invalidSubmissionTitle)
	if !mentions(errs["entryIds"], "At least one expense or travel claim id is required") {
		t.Errorf("errors %v do not ask for an id", errs)
	}

	// 300 of one and 201 of the other is 501 units, and each list is told.
	entries := make([]int64, 300)
	for i := range entries {
		entries[i] = int64(i + 1)
	}
	claims := make([]int64, 201)
	for i := range claims {
		claims[i] = int64(i + 1)
	}
	errs = refused(t, owner, http.MethodPost, submitPath, map[string]any{
		"entryIds": entries, "claimIds": claims,
	}, invalidSubmissionTitle)
	for _, field := range []string{"entryIds", "claimIds"} {
		if !mentions(errs[field], "At most 500 expenses and travel claims may be given at once; 501 were given") {
			t.Errorf("errors on %s = %v, want the unit cap", field, errs[field])
		}
	}
	// An id given twice is one unit, so 501 repeated ids naming 251 trips pass
	// the cap and fail on something else entirely.
	repeated := make([]int64, 0, 502)
	for range 2 {
		repeated = append(repeated, claims...)
	}
	errs = refused(t, owner, http.MethodPost, submitPath, claimFlowBody(repeated, nil), invalidSubmissionTitle)
	if mentions(errs["claimIds"], "At most 500") {
		t.Errorf("errors %v count an id twice, want the cap counted after the duplicates collapse", errs)
	}
}

// A submitted trip is no longer its owner's to change — through every door a
// line has, judged on the claim rather than on the line's own column.
func TestExpensesClaimFlow_ASubmittedClaimsLinesAreClosed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	claim := createClaim(t, owner, nil)
	line := addLine(t, owner, claim.Id, outlayBody(nil))
	submitClaims(t, owner, claim.Id)

	want := fmt.Sprintf("Travel claim %d has been submitted, so its expenses can no longer be changed", claim.Id)
	errs := refusedEntry(t, owner, http.MethodPut, entryPath(line.Id),
		outlayBody(map[string]any{"revision": line.Revision, "claimId": claim.Id}))
	if !mentions(errs["claimId"], want) {
		t.Errorf("the edit got %v, want %q", errs, want)
	}
	errs = refusedEntry(t, owner, http.MethodDelete, entryPath(line.Id), nil)
	if !mentions(errs["claimId"], want) {
		t.Errorf("the delete got %v, want %q", errs, want)
	}
	errs = refusedClaim(t, owner, http.MethodPut, claimPath(claim.Id),
		claimBody(map[string]any{"revision": getClaim(t, owner, claim.Id).Revision}))
	if !mentions(errs["status"], "A travel claim that has been submitted can no longer be changed") {
		t.Errorf("the claim edit got %v, want the status refusal", errs)
	}
	// And the row itself is untouched: the guard is in SQL as well as in Go.
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries WHERE id = $1 AND description = $2`,
		line.Id, "Kabel og kontakter"); n != 1 {
		t.Errorf("%s, want the line exactly as it was submitted", entryColumnsDump(t, h, line.Id))
	}
}

// A per diem day left outside its own trip — which only a change of the
// installation's business time zone can do, since every save checks the window
// — stops the submit and names the date.
func TestExpensesClaimFlow_ASubmitCatchesADayStrandedByTheTimeZone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)

	// A trip that gets home at 00:30 Oslo time on 12 March — 11 March in UTC.
	// In the installation's own zone the last day of the trip is the 12th, and
	// a per diem day recorded on it is inside the window.
	claim := createClaim(t, owner, map[string]any{
		"departureAt": "2026-03-09T07:00:00Z", "returnAt": "2026-03-12T00:30:00+01:00",
	})
	addLine(t, owner, claim.Id, perDiemBody(map[string]any{"entryDate": "2026-03-12"}))

	// Moving the installation to UTC moves the trip's last day a day earlier,
	// and the day recorded on it is now outside its own trip. Nothing sweeps
	// for that; the submit is the last place to notice.
	putSettings(t, admin, settingsBody(map[string]any{"timeZone": "UTC"}))
	errs := refused(t, owner, http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil), invalidSubmissionTitle)
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d holds a per diem day on 2026-03-12", claim.Id)) {
		t.Errorf("errors %v do not name the stranded day", errs)
	}
	// Back in the installation's own zone the very same trip submits.
	putSettings(t, admin, settingsBody(map[string]any{"timeZone": "Europe/Oslo"}))
	submitClaims(t, owner, claim.Id)
}

// The five standalone flow operations still refuse a claim's line by id and
// point at the claim, now that the claim can actually be moved.
func TestExpensesClaimFlow_ALinesOwnIdIsStillRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
	owner, _ := signIn(t, h)

	claim := createClaim(t, owner, nil)
	line := addLine(t, owner, claim.Id, outlayBody(nil))

	for _, tc := range []struct {
		path, imperative string
		body             map[string]any
		title            string
	}{
		{submitPath, "submit the claim", flowBody([]int64{line.Id}, nil), invalidSubmissionTitle},
		{approvePath, "approve the claim", flowBody([]int64{line.Id}, nil), invalidApprovalTitle},
		{rejectPath, "reject the claim", flowBody([]int64{line.Id}, map[string]any{"reason": "Nei"}), invalidApprovalTitle},
		{unapprovePath, "unapprove the claim", flowBody([]int64{line.Id}, nil), invalidApprovalTitle},
		{reimbursedPath, "mark the claim reimbursed", reimbursedBody([]int64{line.Id}, nil), invalidReimbursementTitle},
		{reimbursedUndoPath, "take the claim off the payroll run", flowBody([]int64{line.Id}, nil), invalidReimbursementTitle},
	} {
		errs := refused(t, admin, http.MethodPost, tc.path, tc.body, tc.title)
		want := fmt.Sprintf("Expense %d belongs to travel claim %d; %s", line.Id, claim.Id, tc.imperative)
		if !mentions(errs["entryIds"], want) {
			t.Errorf("%s answered %v, want %q", tc.path, errs, want)
		}
	}
}

// Without the projects module a trip still goes round the whole flow: nothing
// in it needs a project to exist.
func TestExpensesClaimFlow_WithoutProjects_StillMovesTheWholeWay(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	owner, _ := signIn(t, h)
	boss, _ := signIn(t, h, "expenses:approve")
	admin, _ := signIn(t, h, "expenses:manage")

	claim := createClaim(t, owner, nil)
	addLine(t, owner, claim.Id, outlayBody(nil))
	submitClaims(t, owner, claim.Id)
	approveClaims(t, boss, claim.Id)
	markClaimsReimbursed(t, admin, reimbursedClaimBody([]int64{claim.Id}, nil))

	got := getClaim(t, owner, claim.Id)
	if got.Status != "approved" || got.Reimbursement == nil {
		t.Errorf("the claim is %+v, want it approved and paid without a projects module", got)
	}
	if got.BillableTotals != nil {
		t.Errorf("billableTotals = %+v, want nothing at all without projects", got.BillableTotals)
	}
}
