package expenses_test

import (
	"net/http"
	"testing"
)

// This file is the optional projects link on a travel claim (decisions X2 and
// X9): who may book a trip on a project, what happens to its lines when the
// project moves, and what an installation without the projects module does
// with a project id that is already stored.

// Booking a trip on a project needs what logging time on it needs, judged on
// the person the trip concerns rather than on whoever is recording it.
func TestExpensesClaimProjects_BookingNeedsWhatLoggingTimeNeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, ownerID := signIn(t, h)

	// The owner is on no project at all.
	errs := refusedClaim(t, owner, http.MethodPost, claimsPath,
		claimBody(map[string]any{"projectId": projectKraftVerket}))
	if !mentions(errs["projectId"], "not one the expense's owner can book on") {
		t.Errorf("errors = %v, want projectId to refuse a project they cannot book on", errs)
	}
	// A completed project is refused with exactly the same message, whatever
	// the reason: telling them apart would let a caller probe which ids exist.
	h.projects.addRole(projectCompleted, ownerID, roleMember)
	errs = refusedClaim(t, owner, http.MethodPost, claimsPath,
		claimBody(map[string]any{"projectId": projectCompleted}))
	if !mentions(errs["projectId"], "not one the expense's owner can book on") {
		t.Errorf("errors = %v, want the one projectId message", errs)
	}

	// It is the *owner* who is judged, not the recorder: expenses:manage
	// recording for a colleague books on the colleague's project.
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	claim := createClaim(t, admin, map[string]any{
		"projectId": projectKraftVerket, "userId": ownerID.String(),
	})
	if claim.Project == nil || claim.Project.Code != projectKraftVerketCode {
		t.Errorf("project = %+v, want %s", claim.Project, projectKraftVerketCode)
	}
}

// A project the claim is keeping is not judged again: a trip already recorded
// does not become unsaveable because its project has since been completed.
func TestExpensesClaimProjects_AKeptProjectIsGrandfathered(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})

	h.projects.setCanLogTime(projectKraftVerket, false)
	updated := updateClaim(t, owner, claim.Id, map[string]any{
		"projectId": projectKraftVerket, "purpose": "Rettet skrivefeil", "revision": claim.Revision,
	})
	if updated.Project == nil || updated.Project.Id != projectKraftVerket {
		t.Errorf("project = %+v, want it kept", updated.Project)
	}
	// And a line may still be added to it — the claim has already vouched for
	// the project, so the line does not ask again.
	addLine(t, owner, claim.Id, outlayBody(nil))
}

// Changing the claim's project re-points every line in the same transaction,
// and a billing line that is not one of the new project's goes with it.
func TestExpensesClaimProjects_ChangingTheProjectRepointsEveryLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	h.projects.addRole(projectEuro, ownerID, roleMember)
	// The owner also has the project's financial rights, so the billing the
	// re-point rewrites is visible in the answer.
	pricer, pricerID := signIn(t, h, "projects:manage-all")
	h.projects.addRole(projectKraftVerket, pricerID, roleManager)
	h.projects.addRole(projectEuro, pricerID, roleManager)

	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	billable := addLine(t, owner, claim.Id, outlayBody(map[string]any{
		"billable": true, "billingLineId": lineFixed,
	}))
	plain := addLine(t, owner, claim.Id, outlayBody(map[string]any{"description": "Uten fakturering"}))

	updateClaim(t, owner, claim.Id, map[string]any{
		"projectId": projectEuro, "revision": claim.Revision,
	})

	after := getClaim(t, pricer, claim.Id)
	for _, line := range after.Lines {
		if line.Project == nil || line.Project.Id != projectEuro {
			t.Errorf("line %d project = %+v, want the claim's new one", line.Id, line.Project)
		}
	}
	moved := lineByID(t, after.Lines, billable.Id)
	switch {
	case moved.BillingLine != nil:
		t.Errorf("billingLine = %+v, want it cleared: %d is not one of the new project's",
			moved.BillingLine, lineFixed)
	case !moved.Billable:
		t.Errorf("billable = false, want it kept on a project that still bills")
	}
	if got := lineByID(t, after.Lines, plain.Id); got.Billable {
		t.Errorf("a line that was never billable is now, want it left alone")
	}
	// The claim's own billable totals are the project's money, so only
	// somebody with financial rights on the new project is sent them.
	if after.BillableTotals == nil {
		t.Errorf("billableTotals is absent for a caller with financial rights, want it present")
	}
	if got := getClaim(t, owner, claim.Id); got.BillableTotals != nil {
		t.Errorf("billableTotals = %+v for the owner, want it absent", *got.BillableTotals)
	}
}

// Clearing the claim's project makes every line non-billable, and every
// billing figure goes with the flag.
func TestExpensesClaimProjects_ClearingTheProjectClearsTheBilling(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	pricer, pricerID := signIn(t, h, "projects:manage-all")
	h.projects.addRole(projectKraftVerket, pricerID, roleManager)

	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	line := addLine(t, owner, claim.Id, outlayBody(map[string]any{
		"billable": true, "billingLineId": lineFixed,
	}))
	if priced := getEntry(t, pricer, line.Id); priced.Billing == nil || priced.Billing.BillAmount == 0 {
		t.Fatalf("billing = %+v, want the line priced before the project is taken off", priced.Billing)
	}

	updateClaim(t, owner, claim.Id, map[string]any{"projectId": nil, "revision": claim.Revision})

	after := getEntry(t, owner, line.Id)
	switch {
	case after.Project != nil:
		t.Errorf("project = %+v, want it cleared", after.Project)
	case after.Billable:
		t.Errorf("billable = true, want a line on no project to bill nothing")
	case after.BillingLine != nil:
		t.Errorf("billingLine = %+v, want it cleared", after.BillingLine)
	}
	if n := h.Count(t, `
		SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND markup_percent IS NULL AND bill_rate_per_km IS NULL AND bill_amount IS NULL`,
		line.Id); n != 1 {
		t.Errorf("the line still carries billing figures, want them cleared with the flag")
	}
}

// A project that bills nothing bills nothing here either: a line re-pointed
// onto one stops being billable, whatever it was before.
func TestExpensesClaimProjects_ANonBillableProjectStopsTheBilling(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	h.projects.addRole(projectInternal, ownerID, roleMember)

	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	line := addLine(t, owner, claim.Id, outlayBody(map[string]any{"billable": true}))

	updateClaim(t, owner, claim.Id, map[string]any{"projectId": projectInternal, "revision": claim.Revision})
	if after := getEntry(t, owner, line.Id); after.Billable {
		t.Errorf("billable = true on a project that bills nothing, want false")
	}
}

// What has gone out on an invoice keeps the project it went out under, so a
// claim holding an invoiced line keeps its project too.
func TestExpensesClaimProjects_AnInvoicedLineHoldsTheProjectWhereItIs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	pricer, pricerID := signIn(t, h, "projects:manage-all")
	h.projects.addRole(projectKraftVerket, pricerID, roleManager)

	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	line := addLine(t, owner, claim.Id, outlayBody(map[string]any{"billable": true}))
	// Invoicing needs an approved line; the claim flow is a later task's, so
	// the claim is moved in SQL and the line invoiced through its own door.
	seedClaimStatus(t, h, claim.Id, "approved")
	current := getEntry(t, pricer, line.Id)
	markInvoiced(t, pricer, line.Id, map[string]any{"revision": current.Revision})

	// A claim that has been approved is not editable anyway, so the refusal is
	// proved on a draft claim with an invoiced line — the state the undo of an
	// approval leaves behind.
	seedClaimStatus(t, h, claim.Id, "draft")
	claim = getClaim(t, owner, claim.Id)
	errs := refusedClaim(t, owner, http.MethodPut, claimPath(claim.Id),
		claimBody(map[string]any{"projectId": nil, "revision": claim.Revision}))
	if !mentions(errs["projectId"], "has been invoiced") {
		t.Errorf("errors = %v, want projectId to refuse taking the project off an invoiced line", errs)
	}
}

// Without the projects module a project id is refused on its own field, and a
// stored one is carried through untouched rather than cleared by a save.
func TestExpensesClaimProjects_WithoutProjectsAStoredIdIsCarriedThrough(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	owner, _ := signIn(t, h)

	errs := refusedClaim(t, owner, http.MethodPost, claimsPath,
		claimBody(map[string]any{"projectId": projectKraftVerket}))
	if !mentions(errs["projectId"], "no projects module") {
		t.Errorf("errors = %v, want projectId to name the missing module", errs)
	}

	claim := createClaim(t, owner, nil)
	// A claim recorded while the module was on, seen from an installation
	// where it is off: the stored id is data, not a fact this module deletes.
	h.Exec(t, `UPDATE expenses.claims SET project_id = $2 WHERE id = $1`, claim.Id, projectKraftVerket)
	claim = getClaim(t, owner, claim.Id)
	if claim.Project != nil {
		t.Errorf("project = %+v, want nothing shown without the projects module", claim.Project)
	}

	updateClaim(t, owner, claim.Id, map[string]any{"purpose": "Rettet", "revision": claim.Revision})
	if n := h.Count(t, `SELECT count(*) FROM expenses.claims WHERE id = $1 AND project_id = $2`,
		claim.Id, projectKraftVerket); n != 1 {
		t.Errorf("the stored project id was cleared by the save, want it carried through")
	}
	// A line of such a claim takes the stored project as its own, for exactly
	// the same reason.
	line := addLine(t, owner, claim.Id, outlayBody(nil))
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries WHERE id = $1 AND project_id = $2`,
		line.Id, projectKraftVerket); n != 1 {
		t.Errorf("the line does not carry the claim's stored project, want it inherited")
	}
}

// lineByID is one line of a rendered claim, so a test can name the line it
// means rather than index into the list.
func lineByID(t *testing.T, lines []entryJSON, id int64) entryJSON {
	t.Helper()
	for _, line := range lines {
		if line.Id == id {
			return line
		}
	}
	t.Fatalf("line %d is not among the claim's %d lines", id, len(lines))
	return entryJSON{}
}
