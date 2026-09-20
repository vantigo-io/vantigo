package expenses_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the update path's own rules, the ones a create cannot have: an
// edit must not destroy what its editor is not allowed to see or change. Three
// things fall under that — the project link when this installation no longer
// has projects, the billing figures when the caller has no financial rights on
// the project, and a project or billing line that has since stopped accepting
// new bookings — and the fourth is what the answer is when an expense's own
// state, rather than the caller, forbids the change.

// seedProjectedEntry writes an expense already booked on a project, billable,
// with a markup and a bill amount, straight into the table. It is the only way
// to have such a row in an installation with no projects module — the API
// there refuses every project field — which is exactly the case design decision
// X2 promises ("stored project ids stay").
func seedProjectedEntry(t *testing.T, h *harness, ownerID uuid.UUID) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO expenses.entries (
			user_id, created_by_user_id, kind, entry_date, description, category_id,
			paid_by, currency, gross_amount, vat_amount,
			project_id, billing_line_id, billable, markup_percent, bill_amount,
			created_at, updated_at)
		VALUES ($1, $1, 'outlay', DATE '2026-03-10', 'Kabel og kontakter', $2,
			'employee', 'NOK', 1250.00, 250.00,
			$3, $4, true, 15.00, 1150.00,
			now(), now())
		RETURNING id`, ownerID, int32(materialsCategory), int32(projectKraftVerket), int32(lineFixed))
}

// Decision X2: switching the projects module off does not un-book what was
// booked while it was on. The one write the module still allows on such a line
// carries every project column through untouched.
func TestExpensesEntries_WithoutProjects_AnEditKeepsTheStoredProjectColumns(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	owner, ownerID := signIn(t, h)
	id := seedProjectedEntry(t, h, ownerID)

	before := getEntry(t, owner, id)
	if before.Project != nil || before.Billing != nil {
		t.Fatalf("entry = %+v, want nothing project-shaped shown without the projects module", before)
	}
	updateEntry(t, owner, id, outlayBody(map[string]any{
		"revision": before.Revision, "vatAmount": 250.00,
		"description": "Kabel, kontakter og en skjøteledning",
	}))

	if n := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND description = 'Kabel, kontakter og en skjøteledning'
		  AND project_id = $2 AND billing_line_id = $3 AND billable
		  AND markup_percent = 15.00 AND bill_amount = 1150.00 AND bill_rate_per_km IS NULL`,
		id, int32(projectKraftVerket), int32(lineFixed)); n != 1 {
		t.Errorf("the stored project columns did not survive the edit: %s", entryColumnsDump(t, h, id))
	}
}

// entryColumnsDump is the project columns of one row, for a failure message
// that says what actually happened.
func entryColumnsDump(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `
		SELECT format('status=%s revision=%s submitted_at=%s decided_at=%s decided_by=%s '
			|| 'gross=%s rate=%s passenger_rate=%s overridden_by=%s rate_table_value=%s '
			|| 'passenger_rate_table_value=%s project_id=%s billing_line_id=%s billable=%s '
			|| 'markup=%s bill_rate=%s bill_amount=%s',
			status, revision, submitted_at, decided_at, decided_by_user_id,
			gross_amount, rate, passenger_rate, rate_overridden_by_user_id, rate_table_value,
			passenger_rate_table_value, project_id, billing_line_id, billable,
			markup_percent, bill_rate_per_km, bill_amount)
		FROM expenses.entries WHERE id = $1`, id)
}

// The markup and the customer rate per kilometre are the project's money
// (design §5): the same financial rights that decide whether a caller sees them
// decide whether they may set them. A member of the project may book an expense
// on it and may not price what the customer is charged for it.
func TestExpensesEntries_TheBillingFiguresAreOnlySetByWhoeverMaySeeThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"a markup on an outlay": {
			outlayBody(map[string]any{"projectId": projectKraftVerket, "billable": true, "markupPercent": 40}),
			"markupPercent",
		},
		"a customer rate on mileage": {
			mileageBody(map[string]any{"projectId": projectKraftVerket, "billable": true, "billRatePerKm": 9.0}),
			"billRatePerKm",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := refusedEntry(t, member, http.MethodPost, entriesPath, tc.body)
			if len(errs[tc.field]) == 0 {
				t.Fatalf("a member setting it: errors = %v, want one on %s", errs, tc.field)
			}
			// And the caller who may see the project's money may set it.
			createEntry(t, manager, tc.body)
		})
	}
}

// A billable line recorded by somebody without financial rights still gets its
// figures — the server's, from the settings and the rate table. Billable
// mileage with no customer rate in force is saved billable with no rate and no
// bill amount rather than refused: the employee cannot be asked about a price
// they may not know exists, and whoever can see the project's money fills it in.
func TestExpensesEntries_ABillableLineWithoutFinancialRightsTakesTheServersFigures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	putSettings(t, admin, settingsBody(map[string]any{"defaultMarkupPercent": 15}))
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	outlay := createEntry(t, member, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "vatAmount": 250.00,
	}))
	seen := getEntry(t, manager, outlay.Id)
	if seen.Billing == nil || seen.Billing.MarkupPercent == nil || *seen.Billing.MarkupPercent != 15 {
		t.Errorf("billing = %+v, want the settings' default markup", seen.Billing)
	}
	if seen.Billing.BillAmount != 1150 {
		t.Errorf("billAmount = %v, want net × 1.15", seen.Billing.BillAmount)
	}

	// No mileage_customer rate is seeded, so there is none in force.
	mileage := createEntry(t, member, mileageBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))
	if !mileage.Billable {
		t.Fatal("billable = false, want the line saved billable all the same")
	}
	seen = getEntry(t, manager, mileage.Id)
	if seen.Billing == nil {
		t.Fatal("billing = nil for the project's manager, want the object")
	}
	if seen.Billing.BillRatePerKm != nil || seen.Billing.BillAmount != 0 {
		t.Errorf("billing = %+v, want no customer rate and nothing billed yet", seen.Billing)
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND billable AND bill_rate_per_km IS NULL AND bill_amount IS NULL`, mileage.Id); n != 1 {
		t.Error("the stored line carries a customer rate or a bill amount, want neither")
	}
}

// The scenario the whole rule exists for: somebody who can see the project's
// money prices the line, and the owner — whose form is never sent the figure —
// saves the line again. The markup survives, and the bill amount follows the
// new net rather than a default nobody chose.
func TestExpensesEntries_AnEditKeepsTheBillingFiguresItsEditorCannotSee(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	putSettings(t, admin, settingsBody(map[string]any{"defaultMarkupPercent": 15}))
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	// Pricing somebody else's draft takes both halves: expenses:manage to
	// change an expense that is not yours, and financial rights on the project
	// to be allowed to name what the customer is charged.
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:manage")

	created := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))
	priced := updateEntry(t, manager, created.Id, outlayBody(map[string]any{
		"revision": created.Revision, "projectId": projectKraftVerket, "billable": true, "markupPercent": 40,
	}))
	if priced.Billing == nil || priced.Billing.MarkupPercent == nil || *priced.Billing.MarkupPercent != 40 {
		t.Fatalf("billing = %+v, want the 40 %% the manager set", priced.Billing)
	}

	// The owner re-saves with a new amount and no markup — their form has
	// never been told there is one.
	resaved := updateEntry(t, owner, created.Id, outlayBody(map[string]any{
		"revision": priced.Revision, "projectId": projectKraftVerket, "billable": true,
		"grossAmount": 2000.00, "vatAmount": 500.00,
	}))
	if resaved.Billing != nil {
		t.Errorf("the owner's copy carries billing %+v, want none", resaved.Billing)
	}
	seen := getEntry(t, manager, created.Id)
	if seen.Billing == nil || seen.Billing.MarkupPercent == nil || *seen.Billing.MarkupPercent != 40 {
		t.Errorf("markupPercent = %+v, want the 40 %% kept through an edit that could not see it", seen.Billing)
	}
	// Net 1500 × 1.40.
	if seen.Billing.BillAmount != 2100 {
		t.Errorf("billAmount = %v, want the kept markup applied to the new net", seen.Billing.BillAmount)
	}

	// Turning it off clears all three, whoever does it.
	off := updateEntry(t, owner, created.Id, outlayBody(map[string]any{
		"revision": seen.Revision, "projectId": projectKraftVerket, "billable": false,
		"grossAmount": 2000.00, "vatAmount": 500.00,
	}))
	if off.Billable {
		t.Fatal("billable = true, want it off")
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND NOT billable AND markup_percent IS NULL
		  AND bill_rate_per_km IS NULL AND bill_amount IS NULL`, created.Id); n != 1 {
		t.Errorf("a line nobody bills still carries billing figures: %s", entryColumnsDump(t, h, created.Id))
	}
}

// A full replace replaces what the caller can see and send. Somebody with
// financial rights who leaves the markup out keeps the stored one — the same
// rule the owner gets — and an explicit value replaces it.
func TestExpensesEntries_AFinancialCallerKeepsTheStoredFigureUnlessTheySendOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	putSettings(t, admin, settingsBody(map[string]any{"defaultMarkupPercent": 15}))
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	created := createEntry(t, manager, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "markupPercent": 40,
	}))
	kept := updateEntry(t, manager, created.Id, outlayBody(map[string]any{
		"revision": created.Revision, "projectId": projectKraftVerket, "billable": true,
	}))
	if kept.Billing == nil || kept.Billing.MarkupPercent == nil || *kept.Billing.MarkupPercent != 40 {
		t.Errorf("markupPercent = %+v, want the stored 40 %% rather than the settings' default", kept.Billing)
	}
	replaced := updateEntry(t, manager, created.Id, outlayBody(map[string]any{
		"revision": kept.Revision, "projectId": projectKraftVerket, "billable": true, "markupPercent": 5,
	}))
	if replaced.Billing == nil || replaced.Billing.MarkupPercent == nil || *replaced.Billing.MarkupPercent != 5 {
		t.Errorf("markupPercent = %+v, want the value that was sent", replaced.Billing)
	}
}

// Design §8: a project that is no longer active keeps the lines already on it.
// An unchanged projectId — and an unchanged billingLineId — is not judged
// again, exactly as a category already on the line is not; a changed one is.
func TestExpensesEntries_AKeptProjectOrLineIsNotJudgedAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, ownerID, roleMember)

	t.Run("a project that stopped taking bookings", func(t *testing.T) {
		created := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
		h.projects.setCanLogTime(projectKraftVerket, false)
		t.Cleanup(func() { h.projects.clearCanLogTime(projectKraftVerket) })

		kept := updateEntry(t, owner, created.Id, outlayBody(map[string]any{
			"revision": created.Revision, "projectId": projectKraftVerket,
			"description": "Kabel, kontakter og skjøteledning",
		}))
		if kept.Project == nil || kept.Project.Id != projectKraftVerket {
			t.Errorf("project = %+v, want the line to keep what it was booked on", kept.Project)
		}
		// Moving it to another project it may not book on is still refused.
		errs := refusedEntry(t, owner, http.MethodPut, entryPath(created.Id), outlayBody(map[string]any{
			"revision": kept.Revision, "projectId": projectCompleted,
		}))
		if len(errs["projectId"]) == 0 {
			t.Errorf("moving onto an unbookable project: errors = %v, want one on projectId", errs)
		}
	})

	t.Run("a billing line that was deactivated", func(t *testing.T) {
		created := createEntry(t, owner, outlayBody(map[string]any{
			"projectId": projectKraftVerket, "billingLineId": lineFixed,
		}))
		h.projects.deactivateLine(lineFixed)
		t.Cleanup(func() { h.projects.activateLine(lineFixed) })

		kept := updateEntry(t, owner, created.Id, outlayBody(map[string]any{
			"revision": created.Revision, "projectId": projectKraftVerket, "billingLineId": lineFixed,
			"description": "Kabel, kontakter og skjøteledning",
		}))
		if kept.BillingLine == nil || kept.BillingLine.Id != lineFixed {
			t.Errorf("billingLine = %+v, want the line kept", kept.BillingLine)
		}
		// Moving it onto another inactive line is still refused.
		errs := refusedEntry(t, owner, http.MethodPut, entryPath(created.Id), outlayBody(map[string]any{
			"revision": kept.Revision, "projectId": projectKraftVerket, "billingLineId": lineInactive,
		}))
		if len(errs["billingLineId"]) == 0 {
			t.Errorf("moving onto an inactive line: errors = %v, want one on billingLineId", errs)
		}
	})
}

// What an expense is right now — settled, or dated inside a closed period —
// is not a statement about who is asking, so it is a 400 that names the reason
// rather than a bare 403. Who is asking is still a 403 (and a 404 for someone
// who may not see it at all).
func TestExpensesEntries_WhatTheExpenseIsRightNowIsA400ThatSaysWhy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)

	settled := createEntry(t, owner, outlayBody(nil))
	h.Exec(t, `UPDATE expenses.entries SET status = 'submitted' WHERE id = $1`, settled.Id)

	errs := refusedEntry(t, owner, http.MethodPut, entryPath(settled.Id),
		outlayBody(map[string]any{"revision": settled.Revision}))
	if len(errs["status"]) == 0 || !strings.Contains(strings.Join(errs["status"], " "), "submitted") {
		t.Errorf("editing a submitted expense: errors = %v, want one on status naming it", errs)
	}
	errs = refusedEntry(t, owner, http.MethodDelete, entryPath(settled.Id), nil)
	if len(errs["status"]) == 0 {
		t.Errorf("deleting a submitted expense: errors = %v, want one on status", errs)
	}

	locked := createEntry(t, owner, outlayBody(map[string]any{"entryDate": "2026-01-15"}))
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-02-01"}))

	errs = refusedEntry(t, owner, http.MethodPut, entryPath(locked.Id),
		outlayBody(map[string]any{"revision": locked.Revision, "entryDate": "2026-01-15"}))
	if len(errs["entryDate"]) == 0 || !strings.Contains(strings.Join(errs["entryDate"], " "), "2026-02-01") {
		t.Errorf("editing behind the lock: errors = %v, want one on entryDate naming the lock date", errs)
	}
	errs = refusedEntry(t, owner, http.MethodDelete, entryPath(locked.Id), nil)
	if len(errs["entryDate"]) == 0 {
		t.Errorf("deleting behind the lock: errors = %v, want one on entryDate", errs)
	}

	// Who is asking is unchanged: somebody who may see it but does not own it
	// is still a 403, and a stranger still the unknown id's 404.
	viewer, _ := signIn(t, h, "expenses:view-all")
	if r := viewer.Do(http.MethodDelete, entryPath(settled.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("view-all deleting somebody else's expense: status %d body %s, want 403", r.Status, r.Body)
	}
	stranger, _ := signIn(t, h)
	if r := stranger.Do(http.MethodDelete, entryPath(settled.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a stranger deleting: status %d, want 404", r.Status)
	}
}

// An amount the columns cannot hold is a refusal on the field that drove it,
// not a failure: every field below passes its own rule and only their product
// does not fit numeric(12,2).
func TestExpensesEntries_AnAmountTooBigToStoreIsRefusedOnItsField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	// A rate high enough that a long trip overruns the amount column.
	createRate(t, admin, map[string]any{
		"kind": "mileage", "validFrom": "2026-02-01", "value": 9_000_000.00,
	})
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:manage")

	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"a mileage amount": {
			mileageBody(map[string]any{"distanceKm": 9999.9}), "distanceKm",
		},
		"an outlay's bill amount": {
			outlayBody(map[string]any{
				"projectId": projectKraftVerket, "billable": true,
				"grossAmount": 9_999_999_999.99, "markupPercent": 1000,
			}), "markupPercent",
		},
		"a mileage bill amount": {
			mileageBody(map[string]any{
				"projectId": projectKraftVerket, "billable": true,
				"entryDate": "2026-01-10", "distanceKm": 9999.9, "billRatePerKm": 99_999_999.99,
			}), "billRatePerKm",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := refusedEntry(t, manager, http.MethodPost, entriesPath, tc.body)
			if len(errs[tc.field]) == 0 {
				t.Errorf("errors = %v, want one on %s rather than a failure", errs, tc.field)
			}
		})
	}
}
