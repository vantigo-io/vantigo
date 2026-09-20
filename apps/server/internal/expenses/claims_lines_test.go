package expenses_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the line inside a travel claim: an ordinary expense carrying a
// claimId, whose owner, project, status, decision and reimbursement are the
// claim's and whose kind, date, amounts and receipts are its own.

// A line takes the claim's owner and the claim's project, and says which claim
// it belongs to.
func TestExpensesClaimLines_ALineTakesItsOwnerAndProjectFromTheClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, adminID := signIn(t, h, "expenses:manage")
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})

	// Recorded by expenses:manage for a colleague: the claim says whose it is,
	// so nothing else has to.
	line := addLine(t, admin, claim.Id, outlayBody(nil))
	switch {
	case line.ClaimId == nil || *line.ClaimId != claim.Id:
		t.Errorf("claimId = %v, want %d", line.ClaimId, claim.Id)
	case line.Owner.UserId != ownerID:
		t.Errorf("owner = %v, want the claim's owner %v, not the recorder %v",
			line.Owner.UserId, ownerID, adminID)
	case line.Project == nil || line.Project.Id != projectKraftVerket:
		t.Errorf("project = %+v, want the claim's own", line.Project)
	}

	// Naming somebody else is refused rather than silently overridden.
	errs := refusedEntry(t, admin, http.MethodPost, entriesPath, bodyWith(outlayBody(nil), map[string]any{
		"claimId": claim.Id, "userId": adminID.String(),
	}))
	if !mentions(errs["userId"], "belongs to whoever the claim does") {
		t.Errorf("errors = %v, want userId to say the claim decides the owner", errs)
	}
	// And so is another project.
	errs = refusedEntry(t, owner, http.MethodPost, entriesPath, bodyWith(outlayBody(nil), map[string]any{
		"claimId": claim.Id, "projectId": projectEuro,
	}))
	if !mentions(errs["projectId"], "the claim's own project") {
		t.Errorf("errors = %v, want projectId to say the claim decides the project", errs)
	}
}

// Only a claim the caller may change takes lines, and a claim they may not see
// reads exactly as one that is not there.
func TestExpensesClaimLines_TheClaimMustBeTheCallersToChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})

	stranger, _ := signIn(t, h)
	errs := refusedEntry(t, stranger, http.MethodPost, entriesPath,
		bodyWith(outlayBody(nil), map[string]any{"claimId": claim.Id}))
	if !mentions(errs["claimId"], fmt.Sprintf("No travel claim has id %d", claim.Id)) {
		t.Errorf("errors = %v, want a claim they cannot see to read as an unknown id", errs)
	}
	// The very sentence an id that does not exist gets, bar its own number, so
	// naming ids tells a stranger nothing about which claims there are.
	unknown := refusedEntry(t, stranger, http.MethodPost, entriesPath,
		bodyWith(outlayBody(nil), map[string]any{"claimId": claim.Id + 9999}))
	if !mentions(unknown["claimId"], fmt.Sprintf("No travel claim has id %d", claim.Id+9999)) {
		t.Errorf("an unknown claim says %v, an invisible one %v; want the same sentence", unknown, errs)
	}

	// A manager of the claim's project sees it and still may not change it.
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	errs = refusedEntry(t, manager, http.MethodPost, entriesPath,
		bodyWith(outlayBody(nil), map[string]any{"claimId": claim.Id}))
	if !mentions(errs["claimId"], "not yours to change") {
		t.Errorf("errors = %v, want claimId to say it is not theirs", errs)
	}
}

// A line is created, changed and deleted only while the claim is a draft or
// rejected — and the refusal names the claim's own status, because that is the
// fact the caller can act on.
func TestExpensesClaimLines_TheClaimsStatusDecidesWhetherALineMayChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	line := addLine(t, owner, claim.Id, outlayBody(nil))

	seedClaimStatus(t, h, claim.Id, "submitted")
	want := fmt.Sprintf("Travel claim %d has been submitted", claim.Id)

	errs := refusedEntry(t, owner, http.MethodPost, entriesPath,
		bodyWith(outlayBody(nil), map[string]any{"claimId": claim.Id}))
	if !mentions(errs["claimId"], want) {
		t.Errorf("adding a line: errors = %v, want %q", errs, want)
	}
	errs = refusedEntry(t, owner, http.MethodPut, entryPath(line.Id),
		bodyWith(outlayBody(nil), map[string]any{"revision": line.Revision}))
	if !mentions(errs["claimId"], want) {
		t.Errorf("changing a line: errors = %v, want %q", errs, want)
	}
	errs = refusedEntry(t, owner, http.MethodDelete, entryPath(line.Id), nil)
	if !mentions(errs["claimId"], want) {
		t.Errorf("deleting a line: errors = %v, want %q", errs, want)
	}

	// A rejected claim is its owner's again, lines and all.
	seedClaimStatus(t, h, claim.Id, "rejected")
	current := getEntry(t, owner, line.Id)
	updateEntry(t, owner, line.Id, bodyWith(outlayBody(map[string]any{"grossAmount": 99.00}),
		map[string]any{"revision": current.Revision}))
	if r := owner.Do(http.MethodDelete, entryPath(line.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("deleting a line of a rejected claim: status %d body %s, want 204", r.Status, r.Body)
	}
}

// A line's status, its decision, its capabilities and its receipts are all the
// claim's, read through the one function that answers "the unit this expense
// belongs to".
func TestExpensesClaimLines_ALineIsRenderedThroughItsClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	line := addLine(t, owner, claim.Id, outlayBody(nil))

	// The line's own status column stays 'draft' and is never read.
	if got := h.Count(t, `SELECT count(*) FROM expenses.entries WHERE id = $1 AND status = 'draft'`, line.Id); got != 1 {
		t.Fatalf("the line's own status column is not draft, want it left at its default")
	}
	seedClaimStatus(t, h, claim.Id, "rejected")

	got := getEntry(t, owner, line.Id)
	switch {
	case got.Status != "rejected":
		t.Errorf("status = %q, want the claim's", got.Status)
	case got.Decision == nil || got.Decision.Status != "rejected":
		t.Errorf("decision = %+v, want the claim's", got.Decision)
	case got.SubmittedAt == nil:
		t.Errorf("submittedAt is absent, want the claim's")
	}

	seedClaimStatus(t, h, claim.Id, "approved")
	got = getEntry(t, owner, line.Id)
	switch {
	case got.Status != "approved":
		t.Errorf("status = %q, want the claim's", got.Status)
	case got.Capabilities.CanEdit:
		t.Errorf("canEdit = true on a line of an approved claim, want false")
	case got.Capabilities.CanSubmit || got.Capabilities.CanApprove || got.Capabilities.CanUnapprove:
		t.Errorf("capabilities = %+v, want the flow ones false: the claim is the unit", got.Capabilities)
	}

	// And a receipt follows the claim as well: the state it may be changed in
	// is the claim's, not the line's own.
	errs := refusedReceipt(t, owner, line.Id, "kvittering.png", "image/png", testPNG(t, 4, 4))
	if !mentions(errs["entryId"], fmt.Sprintf("Travel claim %d has been approved", claim.Id)) {
		t.Errorf("errors = %v, want the receipt refusal to name the claim's status", errs)
	}
}

// A standalone flow operation given one of a claim's lines refuses the whole
// batch and points at the claim — every batch in this module is all or
// nothing, and a line is never a unit.
func TestExpensesClaimLines_TheStandaloneFlowRefusesALineAndNamesItsClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	line := addLine(t, owner, claim.Id, outlayBody(nil))
	standalone := createEntry(t, owner, outlayBody(map[string]any{"description": "Alene"}))

	errs := refusedFlow(t, owner, submitPath, flowBody([]int64{line.Id}, nil))
	want := fmt.Sprintf("Expense %d belongs to travel claim %d; submit the claim", line.Id, claim.Id)
	if !mentions(errs["entryIds"], want) {
		t.Errorf("errors = %v, want %q", errs, want)
	}
	// All or nothing: the standalone expense beside it does not move either.
	errs = refusedFlow(t, owner, submitPath, flowBody([]int64{standalone.Id, line.Id}, nil))
	if !mentions(errs["entryIds"], want) {
		t.Errorf("errors = %v, want %q", errs, want)
	}
	if got := getEntry(t, owner, standalone.Id); got.Status != "draft" {
		t.Errorf("the standalone expense is %s, want it untouched by the refused batch", got.Status)
	}

	// The three an approver makes, and the two a payroll run makes, each in
	// their own words.
	approver, _ := signIn(t, h, "expenses:approve")
	admin, _ := signIn(t, h, "expenses:manage")
	for _, tc := range []struct {
		path       string
		c          *modtest.Client
		title      string
		imperative string
		body       map[string]any
	}{
		{approvePath, approver, invalidApprovalTitle, "approve the claim", flowBody([]int64{line.Id}, nil)},
		{
			rejectPath, approver, invalidApprovalTitle, "reject the claim",
			flowBody([]int64{line.Id}, map[string]any{"reason": "Mangler kvittering"}),
		},
		{unapprovePath, approver, invalidApprovalTitle, "unapprove the claim", flowBody([]int64{line.Id}, nil)},
		{
			reimbursedPath, admin, invalidReimbursementTitle, "mark the claim reimbursed",
			reimbursedBody([]int64{line.Id}, nil),
		},
		{
			reimbursedUndoPath, admin, invalidReimbursementTitle, "take the claim off the payroll run",
			flowBody([]int64{line.Id}, nil),
		},
	} {
		errs := refused(t, tc.c, http.MethodPost, tc.path, tc.body, tc.title)
		want := fmt.Sprintf("Expense %d belongs to travel claim %d; %s", line.Id, claim.Id, tc.imperative)
		if !mentions(errs["entryIds"], want) {
			t.Errorf("%s: errors = %v, want %q", tc.path, errs, want)
		}
	}
}

// A line moves neither into a claim nor out of one.
func TestExpensesClaimLines_ALineCannotBeMovedInOrOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	other := createClaim(t, owner, map[string]any{"purpose": "En annen tur"})
	line := addLine(t, owner, claim.Id, outlayBody(nil))
	standalone := createEntry(t, owner, outlayBody(map[string]any{"description": "Alene"}))

	errs := refusedEntry(t, owner, http.MethodPut, entryPath(standalone.Id),
		bodyWith(outlayBody(nil), map[string]any{"revision": standalone.Revision, "claimId": claim.Id}))
	if !mentions(errs["claimId"], "cannot be moved into one") {
		t.Errorf("errors = %v, want claimId to refuse a standalone expense joining a claim", errs)
	}
	errs = refusedEntry(t, owner, http.MethodPut, entryPath(line.Id),
		bodyWith(outlayBody(nil), map[string]any{"revision": line.Revision, "claimId": other.Id}))
	if !mentions(errs["claimId"], "cannot be moved to another") {
		t.Errorf("errors = %v, want claimId to refuse a line changing claims", errs)
	}
	// Leaving it out keeps the line where it is, which is the only place it
	// can be.
	kept := updateEntry(t, owner, line.Id,
		bodyWith(outlayBody(nil), map[string]any{"revision": line.Revision}))
	if kept.ClaimId == nil || *kept.ClaimId != claim.Id {
		t.Errorf("claimId = %v, want it kept at %d", kept.ClaimId, claim.Id)
	}
}

// The list gains two filters, and its default is unchanged: a client that
// knows nothing of claims sees exactly what it always saw.
func TestExpensesClaimLines_TheListFiltersOnTheClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	line := addLine(t, owner, claim.Id, outlayBody(nil))
	standalone := createEntry(t, owner, outlayBody(map[string]any{"description": "Alene"}))

	if got := entryIDs(listEntries(t, owner, "")); len(got) != 2 {
		t.Errorf("the default list = %v, want both the line and the standalone expense", got)
	}
	if got := entryIDs(listEntries(t, owner, "?standalone=true")); len(got) != 1 || got[0] != standalone.Id {
		t.Errorf("standalone=true = %v, want [%d]", got, standalone.Id)
	}
	if got := entryIDs(listEntries(t, owner, "?standalone=false")); len(got) != 1 || got[0] != line.Id {
		t.Errorf("standalone=false = %v, want [%d]", got, line.Id)
	}
	if got := entryIDs(listEntries(t, owner, fmt.Sprintf("?claimId=%d", claim.Id))); len(got) != 1 || got[0] != line.Id {
		t.Errorf("claimId = %v, want [%d]", got, line.Id)
	}

	// The status filter reads the claim's status on a line, because that is
	// the status the line is rendered with.
	seedClaimStatus(t, h, claim.Id, "submitted")
	if got := entryIDs(listEntries(t, owner, "?status=submitted")); len(got) != 1 || got[0] != line.Id {
		t.Errorf("status=submitted = %v, want the claim's line [%d]", got, line.Id)
	}
	if got := entryIDs(listEntries(t, owner, "?status=draft")); len(got) != 1 || got[0] != standalone.Id {
		t.Errorf("status=draft = %v, want only the standalone expense [%d]", got, standalone.Id)
	}
	// A claim nobody may see holds nothing they may see: an empty page rather
	// than a refusal.
	stranger, _ := signIn(t, h)
	if got := entryIDs(listEntries(t, stranger, fmt.Sprintf("?claimId=%d", claim.Id))); len(got) != 0 {
		t.Errorf("a stranger's claim filter = %v, want an empty page", got)
	}
}

// A claim holds at most 200 expenses, and the cap is decided under the claim's
// own row lock.
func TestExpensesClaimLines_AClaimHoldsAtMostTwoHundredExpenses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	claim := createClaim(t, owner, nil)

	// Two hundred lines through the API would be two hundred round trips for
	// one boundary; the rows are seeded and the boundary is what is tested.
	h.Exec(t, `
		INSERT INTO expenses.entries (
			user_id, created_by_user_id, claim_id, kind, entry_date, description,
			category_id, paid_by, currency, gross_amount, created_at, updated_at)
		SELECT $1, $1, $2, 'outlay', DATE '2026-03-10', 'Seeded', $3, 'employee', 'NOK', 10.00, now(), now()
		FROM generate_series(1, 199)`, ownerID, claim.Id, materialsCategory)

	// The two hundredth still fits.
	addLine(t, owner, claim.Id, outlayBody(nil))
	errs := refusedEntry(t, owner, http.MethodPost, entriesPath,
		bodyWith(outlayBody(nil), map[string]any{"claimId": claim.Id}))
	if !mentions(errs["claimId"], "at most 200 expenses") {
		t.Errorf("errors = %v, want claimId to name the cap", errs)
	}
}

// A rate override and the project side's pricing are the line's own doors, not
// the claim's — but the status each is judged in is the claim's.
func TestExpensesClaimLines_TheLinesOwnDoorsReadTheClaimsStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	approver, _ := signIn(t, h, "expenses:approve")
	pricer, pricerID := signIn(t, h, "projects:manage-all")
	h.projects.addRole(projectKraftVerket, pricerID, roleManager)

	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	line := addLine(t, owner, claim.Id, mileageBody(nil))

	// Pricing is open in every status the line can still be priced in, which
	// includes a claim that is still a draft.
	priced := setBilling(t, pricer, line.Id, map[string]any{
		"billable": true, "billRatePerKm": 9.50, "revision": line.Revision,
	})
	if priced.Billing == nil || priced.Billing.BillAmount == 0 {
		t.Fatalf("billing = %+v, want the line priced while its claim is a draft", priced.Billing)
	}

	// A rate override needs the unit submitted, so it is refused while the
	// claim is a draft and allowed once the claim has been submitted.
	current := getEntry(t, approver, line.Id)
	errs := refusedEntry(t, approver, http.MethodPut, entryRatePath(line.Id), map[string]any{
		"rate": 6.00, "revision": current.Revision,
	})
	if !mentions(errs["status"], "draft") {
		t.Errorf("errors = %v, want status to name the claim's own status", errs)
	}

	seedClaimStatus(t, h, claim.Id, "submitted")
	current = getEntry(t, approver, line.Id)
	if !current.Capabilities.CanOverrideRate {
		t.Errorf("capabilities = %+v, want canOverrideRate once the claim is submitted", current.Capabilities)
	}
	overridden := overrideRate(t, approver, line.Id, map[string]any{
		"rate": 6.00, "revision": current.Revision,
	})
	switch {
	case overridden.Rate == nil || *overridden.Rate != 6.00:
		t.Errorf("rate = %v, want the override's 6.00", overridden.Rate)
	case overridden.RateOverride == nil || overridden.RateOverride.TableValue == nil ||
		*overridden.RateOverride.TableValue != 5.30:
		t.Errorf("rateOverride = %+v, want the table's own 5.30 recorded", overridden.RateOverride)
	}
}

// The cap is decided under the claim's own row lock, so two lines racing for
// the last slot cannot both take it.
func TestExpensesClaimLines_TwoLinesRacingForTheLastSlotDoNotBothTakeIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	claim := createClaim(t, owner, nil)
	h.Exec(t, `
		INSERT INTO expenses.entries (
			user_id, created_by_user_id, claim_id, kind, entry_date, description,
			category_id, paid_by, currency, gross_amount, created_at, updated_at)
		SELECT $1, $1, $2, 'outlay', DATE '2026-03-10', 'Seeded', $3, 'employee', 'NOK', 10.00, now(), now()
		FROM generate_series(1, 199)`, ownerID, claim.Id, materialsCategory)

	body := bodyWith(outlayBody(nil), map[string]any{"claimId": claim.Id})
	results := make(chan int, 2)
	for range 2 {
		go func() { results <- owner.Do(http.MethodPost, entriesPath, body).Status }()
	}
	created, refused := 0, 0
	for range 2 {
		switch status := <-results; status {
		case http.StatusCreated:
			created++
		case http.StatusBadRequest:
			refused++
		default:
			t.Errorf("a racing line answered %d, want 201 or 400", status)
		}
	}
	if created != 1 || refused != 1 {
		t.Errorf("%d created and %d refused, want exactly one of each", created, refused)
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries WHERE claim_id = $1`, claim.Id); n != 200 {
		t.Errorf("the claim holds %d expenses, want the cap of 200", n)
	}
}

// The period lock reaches a line through its claim: the day the trip departed,
// not the day the line itself is dated.
func TestExpensesClaimLines_ThePeriodLockIsTheClaimsOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil) // departs 2026-03-09

	// The lock closes the day the trip departed, but not the day a line of it
	// is dated. The claim is what decides, so the line is refused.
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-03-10"}))
	errs := refusedEntry(t, owner, http.MethodPost, entriesPath,
		bodyWith(outlayBody(map[string]any{"entryDate": "2026-03-11"}), map[string]any{"claimId": claim.Id}))
	if !mentions(errs["claimId"], "departed before 2026-03-10") {
		t.Errorf("errors = %v, want claimId to name the claim's departure against the lock", errs)
	}
	// And expenses:manage works past it, as everywhere else.
	addLine(t, admin, claim.Id, outlayBody(map[string]any{"entryDate": "2026-03-11"}))
}
