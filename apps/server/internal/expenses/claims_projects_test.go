package expenses_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
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

	// Both directions are refused, because both would move the line: taking
	// the project off, and moving it to another one. What went out on an
	// invoice keeps the project it went out under.
	h.projects.addRole(projectEuro, ownerID, roleMember)
	for _, tc := range []struct {
		name    string
		project any
	}{
		{"clearing the project", nil},
		{"changing the project", projectEuro},
	} {
		errs := refusedClaim(t, owner, http.MethodPut, claimPath(claim.Id),
			claimBody(map[string]any{"projectId": tc.project, "revision": claim.Revision}))
		if !mentions(errs["projectId"], fmt.Sprintf("Expense %d has been invoiced", line.Id)) {
			t.Errorf("%s: errors = %v, want projectId to name the invoiced line", tc.name, errs)
		}
	}
	// And the claim and its line are exactly where they were.
	if after := getClaim(t, owner, claim.Id); after.Project == nil || after.Project.Id != projectKraftVerket {
		t.Errorf("project = %+v, want it held at %d", after.Project, projectKraftVerket)
	}
	if n := h.Count(t, `
		SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND project_id = $2 AND invoiced_at IS NOT NULL`,
		line.Id, projectKraftVerket); n != 1 {
		t.Errorf("the invoiced line moved, want it and its invoice stamp untouched")
	}
}

// **The invariant the rest of the design leans on**: a claim's line always
// carries the claim's own project and the claim's own owner.
//
// The list's visibility predicate reads a line's denormalised `user_id` and
// `project_id` *because* they always equal the claim's, and `accessFor`
// resolves the caller's role on the project from the line's copy. A line left
// on the claim's previous project would be visible to that project's managers
// and invisible to the new one's, while the claim's own read showed it to
// neither.
//
// The race is real: a create is judged against the claim read *before* the
// transaction (judging a project means asking the project directory, which
// nothing inside a locked transaction may do), so a re-point committing in
// between has to be caught under the lock.
func TestExpensesClaimProjects_ALinesProjectIsAlwaysItsClaims(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	h.projects.addRole(projectEuro, ownerID, roleMember)
	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})

	// The invariant has to hold at *every* instant, not merely once the dust
	// settles: a line that is on the wrong project for a moment is already
	// visible to the wrong project's managers and invisible to the right
	// one's, and a later re-point would sweep the evidence away. So a watcher
	// polls it throughout, and the highest reading it ever saw is what the
	// test asserts on.
	strayed := make(chan int64, 1)
	stop := make(chan struct{})
	go func() {
		var worst int64
		defer func() { strayed <- worst }()
		for {
			select {
			case <-stop:
				return
			default:
			}
			var n int64
			if err := h.Pool().QueryRow(context.Background(), claimLineInvariant).Scan(&n); err == nil && n > worst {
				worst = n
			}
			// A pause between readings. Without it this goroutine takes a
			// connection out of the pool as fast as it can give one back, and
			// on CI's four cores it competes with the nine writers it is
			// watching — which makes the race it exists to observe less likely
			// rather than more. A millisecond is far shorter than any of the
			// windows it is looking for.
			time.Sleep(time.Millisecond)
		}
	}()

	// Creators and re-pointers all in flight at once. The window a stale
	// project could slip through is the one between the claim being read (for
	// the project directory, which may not be asked inside a locked
	// transaction) and its row being locked, so the test widens it by keeping
	// many writers overlapping rather than by sleeping.
	projects := []any{projectEuro, projectKraftVerket}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				owner.Do(http.MethodPost, entriesPath,
					bodyWith(outlayBody(nil), map[string]any{"claimId": claim.Id}))
			}
		}()
	}
	for mover := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := range 5 {
				// Read the claim's revision inside the loop: a re-point that
				// loses a 409 to another mover simply tries the next round.
				r := owner.Do(http.MethodGet, claimPath(claim.Id), nil)
				if r.Status != http.StatusOK {
					continue
				}
				var current claimJSON
				r.JSON(&current)
				owner.Do(http.MethodPut, claimPath(claim.Id), claimBody(map[string]any{
					"projectId": projects[(mover+round)%len(projects)], "revision": current.Revision,
				}))
			}
		}()
	}
	wg.Wait()
	close(stop)

	if worst := <-strayed; worst != 0 {
		t.Errorf("%d of the claim's lines carried a project or an owner that was not the claim's, want none ever",
			worst)
	}
	// And once more when everything has settled, in case the watcher never
	// sampled the moment it went wrong.
	if stray := h.Count(t, claimLineInvariant); stray != 0 {
		t.Errorf("%d of the claim's lines carry a project or an owner that is not the claim's, want none", stray)
	}
	// The rounds really did both things, or the invariant held vacuously. Lines
	// were created **and** the claim was re-pointed at least once: the revision
	// moves on every accepted PUT, so anything above its initial 1 is a
	// re-point that won. Counting only the lines would let a run where every
	// mover lost its 409 pass as proof of an invariant nothing challenged.
	if lines := h.Count(t, `SELECT count(*) FROM expenses.entries WHERE claim_id = $1`, claim.Id); lines == 0 {
		t.Errorf("no line was ever recorded, so the race never happened")
	}
	if revision := h.Count(t, `SELECT revision FROM expenses.claims WHERE id = $1`, claim.Id); revision <= 1 {
		t.Errorf("the claim stands at revision %d, so no re-point ever won and the invariant held vacuously",
			revision)
	}
}

// claimLineInvariant counts the claim lines that have drifted from their
// claim. It is the one statement that says what "a line belongs to its claim"
// means to the rest of the module: the list's visibility predicate reads these
// two denormalised columns rather than joining, so anything this counts is
// something a reader would see wrongly.
const claimLineInvariant = `
	SELECT count(*) FROM expenses.entries e
	JOIN expenses.claims c ON c.id = e.claim_id
	WHERE e.project_id IS DISTINCT FROM c.project_id OR e.user_id <> c.user_id`

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

// A per diem day is never billed on to a customer — and the X2 carry-through
// must not make one look as though it is. When the projects module is off, or
// the line's project has gone from the directory, a replace carries what was
// booked off the locked row rather than re-deciding it; a line that becomes a
// per diem day in that same save would otherwise keep the outlay's billable
// flag and its markup. The freeze is the second door: it must write the day as
// not billable rather than inherit what the row happens to hold.
func TestExpensesClaimProjects_APerDiemDayIsNeverBillable(t *testing.T) {
	t.Parallel()
	for name, withProjects := range map[string]bool{
		"with the projects module": true,
		"without it":               false,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var h *harness
			if withProjects {
				h = newHarness(t)
			} else {
				h = newHarnessWithoutProjects(t)
			}
			admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
			owner, ownerID := signIn(t, h)
			if withProjects {
				h.projects.addRole(projectKraftVerket, ownerID, roleMember)
			}

			claim := createClaim(t, owner, nil)
			line := addLine(t, owner, claim.Id, outlayBody(nil))
			// A billable outlay on a project, written straight to the row: the
			// point of the test is what a *later* save does with those columns,
			// and the doors that set them are covered elsewhere.
			h.Exec(t, `UPDATE expenses.entries
				SET project_id = $2, billable = true, markup_percent = 10.00, bill_amount = 1375.00
				WHERE id = $1`, line.Id, projectKraftVerket)
			h.Exec(t, `UPDATE expenses.claims SET project_id = $2 WHERE id = $1`, claim.Id, projectKraftVerket)
			if withProjects {
				// The project goes from the directory, which is the other half
				// of the carry-through's reach.
				h.projects.removeProject(projectKraftVerket)
			}

			saved := updateEntry(t, owner, line.Id, bodyWith(perDiemBody(nil), map[string]any{
				"revision": getEntry(t, owner, line.Id).Revision,
			}))
			if saved.Kind != "per_diem" {
				t.Fatalf("the line is %s, want it saved as a per diem day", saved.Kind)
			}
			if saved.Billable {
				t.Errorf("the saved day answers billable %v, want a per diem day never billable", saved.Billable)
			}
			if n := h.Count(t, `SELECT count(*) FROM expenses.entries
				WHERE id = $1 AND NOT billable AND markup_percent IS NULL
				  AND bill_rate_per_km IS NULL AND bill_amount IS NULL AND billing_line_id IS NULL`,
				line.Id); n != 1 {
				t.Errorf("%s, want a per diem day with no billing figures at all",
					entryColumnsDump(t, h, line.Id))
			}
			// And the freeze writes the same, rather than inheriting a flag a
			// row somehow still holds.
			h.Exec(t, `UPDATE expenses.entries SET billable = true, markup_percent = 10.00 WHERE id = $1`, line.Id)
			approvedClaimBy(t, owner, admin, claim.Id)
			if n := h.Count(t, `SELECT count(*) FROM expenses.entries
				WHERE id = $1 AND NOT billable AND markup_percent IS NULL
				  AND bill_rate_per_km IS NULL AND bill_amount IS NULL`, line.Id); n != 1 {
				t.Errorf("%s, want the freeze to write a per diem day as not billable",
					entryColumnsDump(t, h, line.Id))
			}
		})
	}
}
