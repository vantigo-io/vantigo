package expenses_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the project side's pricing: PUT /entries/{id}/billing, the one
// door through which the markup, the customer rate per kilometre and the
// billing line reach an expense once its owner has sent it on. It exists
// because expenses:manage does not imply financial rights on a project and an
// employee's form cannot carry a markup — so the project's manager prices the
// line at approval time, not the person who paid.

// setBillingBody is a valid minimal pricing body which tests override one
// field of at a time.
func setBillingBody(revision int32, overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{"billable": true, "revision": revision}, overrides)
}

func TestExpensesBilling_PricesASubmittedOutlay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "vatAmount": 250.00,
	}))
	submitted := submitEntries(t, owner, entry.Id)
	if submitted[0].Billable {
		t.Fatalf("submitted = %+v, want it not billable until somebody says so", submitted[0])
	}

	priced := setBilling(t, manager, entry.Id, setBillingBody(submitted[0].Revision, map[string]any{
		"markupPercent": 20.00, "billingLineId": lineFixed,
	}))
	if !priced.Billable || priced.Billing == nil {
		t.Fatalf("priced = %+v, want it billable with a billing object", priced)
	}
	if priced.Billing.MarkupPercent == nil || *priced.Billing.MarkupPercent != 20 {
		t.Errorf("markupPercent = %v, want 20", priced.Billing.MarkupPercent)
	}
	if priced.Billing.BillAmount != 1200 {
		t.Errorf("billAmount = %v, want the net 1000 plus 20 per cent", priced.Billing.BillAmount)
	}
	if priced.BillingLine == nil || priced.BillingLine.Id != lineFixed {
		t.Errorf("billingLine = %+v, want the line it was priced onto", priced.BillingLine)
	}
	if priced.Status != "submitted" {
		t.Errorf("status = %q, want the pricing to leave the flow alone", priced.Status)
	}
	// The owner still sees no money of the project's.
	if seen := getEntry(t, owner, entry.Id); seen.Billing != nil || !seen.Billable {
		t.Errorf("the owner's copy = %+v, want billable true and no billing object", seen)
	}
}

func TestExpensesBilling_PricesMileageWithACustomerRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, mileageBody(map[string]any{"projectId": projectKraftVerket}))
	submitted := submitEntries(t, owner, entry.Id)

	priced := setBilling(t, manager, entry.Id, setBillingBody(submitted[0].Revision,
		map[string]any{"billRatePerKm": 9.00}))
	if priced.Billing == nil || priced.Billing.BillAmount != 1080 {
		t.Errorf("billing = %+v, want 120 km at 9.00", priced.Billing)
	}
	if priced.Billing.BillRatePerKm == nil || *priced.Billing.BillRatePerKm != 9 {
		t.Errorf("billRatePerKm = %v, want 9.00", priced.Billing.BillRatePerKm)
	}
	// What the owner is reimbursed is untouched by what the customer pays.
	if priced.GrossAmount != 636 {
		t.Errorf("gross = %v, want the frozen 636", priced.GrossAmount)
	}
}

func TestExpensesBilling_TurningItOffClearsTheFigures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))
	submitted := submitEntries(t, owner, entry.Id)
	off := setBilling(t, manager, entry.Id, setBillingBody(submitted[0].Revision,
		map[string]any{"billable": false}))
	if off.Billable || off.Billing == nil || off.Billing.BillAmount != 0 {
		t.Fatalf("off = %+v, want it non-billable with nothing billed", off)
	}
	if off.Billing.MarkupPercent != nil || off.Billing.BillRatePerKm != nil {
		t.Errorf("billing = %+v, want the figures cleared", off.Billing)
	}
}

// The door is narrow but it opens on every status a line can still be priced
// in, so the project manager is never blocked by where the owner has got to.
func TestExpensesBilling_WorksInEveryStatusButInvoiced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve")

	for _, status := range []string{"draft", "submitted", "approved", "rejected"} {
		entry := createEntry(t, owner, outlayBody(map[string]any{
			"projectId": projectKraftVerket, "description": "Til " + status,
		}))
		switch status {
		case "submitted":
			submitEntries(t, owner, entry.Id)
		case "approved":
			approvedBy(t, owner, manager, entry.Id)
		case "rejected":
			submitEntries(t, owner, entry.Id)
			rejectEntries(t, manager, "Nei", entry.Id)
		}
		current := getEntry(t, manager, entry.Id)
		if !current.Capabilities.CanSetBilling {
			t.Errorf("%s: canSetBilling = false, want the project side able to price it", status)
		}
		priced := setBilling(t, manager, entry.Id, setBillingBody(current.Revision,
			map[string]any{"markupPercent": 10.00}))
		if priced.Status != status {
			t.Errorf("%s: status = %q, want the pricing to leave it where it was", status, priced.Status)
		}
		if priced.Billing == nil || priced.Billing.BillAmount != 1375 {
			t.Errorf("%s: billing = %+v, want 1250 plus ten per cent", status, priced.Billing)
		}
	}
}

func TestExpensesBilling_RefusesAnInvoicedLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve")

	entry := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))
	approvedBy(t, owner, manager, entry.Id)
	h.Exec(t, `UPDATE expenses.entries SET invoiced_at = now() WHERE id = $1`, entry.Id)

	current := getEntry(t, manager, entry.Id)
	errs := refusedEntry(t, manager, http.MethodPut, entryBillingPath(entry.Id),
		setBillingBody(current.Revision, nil))
	if len(errs["status"]) == 0 {
		t.Fatalf("errors = %v, want one on status", errs)
	}
	if got := getEntry(t, manager, entry.Id); got.Capabilities.CanSetBilling {
		t.Errorf("canSetBilling = true on an invoiced line, want false")
	}
}

// The three refusals, in the order they are decided: what the caller cannot
// see is the unknown id's 404; what has no project to price is a 400 on
// projectId; and only then is the caller's lack of financial rights a 403.
func TestExpensesBilling_TellsTheThreeRefusalsApart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	outsider, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")
	admin, _ := signIn(t, h, "expenses:manage")

	onProject := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	standalone := createEntry(t, owner, outlayBody(map[string]any{"description": "Uten prosjekt"}))
	body := setBillingBody(1, nil)

	if r := outsider.Do(http.MethodPut, entryBillingPath(onProject.Id), body); r.Status != http.StatusNotFound {
		t.Errorf("an outsider: status %d body %s, want a bare 404", r.Status, r.Body)
	}
	errs := refusedEntry(t, approver, http.MethodPut, entryBillingPath(standalone.Id), body)
	if len(errs["projectId"]) == 0 {
		t.Errorf("an expense with no project: errors = %v, want one on projectId", errs)
	}
	// Seen, on a project, but the money on it is not theirs — expenses:manage
	// reaches the installation's settings, not a project's financials.
	for name, c := range map[string]*modtest.Client{"approve": approver, "manage": admin} {
		if r := c.Do(http.MethodPut, entryBillingPath(onProject.Id), body); r.Status != http.StatusForbidden {
			t.Errorf("%s: status %d body %s, want 403 — no financial rights on the project", name, r.Status, r.Body)
		}
	}
}

func TestExpensesBilling_HoldsTheFiguresToTheirOwnKind(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	outlay := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	mileage := createEntry(t, owner, mileageBody(map[string]any{"projectId": projectKraftVerket}))

	for name, tc := range map[string]struct {
		id    int64
		body  map[string]any
		field string
	}{
		"a rate per kilometre on an outlay": {outlay.Id, map[string]any{"billRatePerKm": 9.00}, "billRatePerKm"},
		"a markup on mileage":               {mileage.Id, map[string]any{"markupPercent": 20.00}, "markupPercent"},
		"a markup on nothing billable": {
			outlay.Id, map[string]any{"billable": false, "markupPercent": 20.00}, "markupPercent",
		},
		"a markup out of range":  {outlay.Id, map[string]any{"markupPercent": 1001.00}, "markupPercent"},
		"a third decimal on one": {outlay.Id, map[string]any{"markupPercent": 20.005}, "markupPercent"},
		"a line of another project": {
			outlay.Id, map[string]any{"billingLineId": lineEuro}, "billingLineId",
		},
		"an inactive line": {outlay.Id, map[string]any{"billingLineId": lineInactive}, "billingLineId"},
	} {
		t.Run(name, func(t *testing.T) {
			errs := refusedEntry(t, manager, http.MethodPut, entryBillingPath(tc.id),
				setBillingBody(1, tc.body))
			if len(errs[tc.field]) == 0 {
				t.Fatalf("errors = %v, want one on %s", errs, tc.field)
			}
		})
	}
}

func TestExpensesBilling_IsGuardedByTheRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	setBilling(t, manager, entry.Id, setBillingBody(entry.Revision, map[string]any{"markupPercent": 10.00}))

	r := manager.Do(http.MethodPut, entryBillingPath(entry.Id),
		setBillingBody(entry.Revision, map[string]any{"markupPercent": 20.00}))
	if r.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", r.Status, r.Body)
	}
}

// A project that bills nothing bills nothing here either, whatever is asked.
func TestExpensesBilling_ANonBillableProjectStaysNonBillable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectInternal, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectInternal, roleManager)

	entry := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectInternal}))
	priced := setBilling(t, manager, entry.Id, setBillingBody(entry.Revision, nil))
	if priced.Billable || priced.Billing == nil || priced.Billing.BillAmount != 0 {
		t.Errorf("priced = %+v, want billable forced false on a non-billable project", priced)
	}
}

// The period lock holds the pricing back like every other write.
func TestExpensesBilling_ThePeriodLockHoldsItBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "projects:manage-all")
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-04-01"}))

	body := setBillingBody(entry.Revision, map[string]any{"markupPercent": 10.00})
	errs := refusedEntry(t, manager, http.MethodPut, entryBillingPath(entry.Id), body)
	if len(errs["entryDate"]) == 0 {
		t.Errorf("errors = %v, want one naming the lock", errs)
	}
	// expenses:manage with financial rights works past it.
	setBilling(t, admin, entry.Id, body)
}

// Decision X2: without the projects module there is nothing to price, and the
// operation says so rather than pretending.
func TestExpensesBilling_WithoutProjects_IsA400OnProjectId(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	owner, ownerID := signIn(t, h)
	admin, _ := signIn(t, h, "expenses:manage", "projects:manage-all")
	id := seedProjectedEntry(t, h, ownerID)

	errs := refusedEntry(t, admin, http.MethodPut, entryBillingPath(id), setBillingBody(1, nil))
	if len(errs["projectId"]) == 0 {
		t.Fatalf("errors = %v, want one on projectId", errs)
	}
	if got := getEntry(t, owner, id); got.Capabilities.CanSetBilling {
		t.Errorf("canSetBilling = true without the projects module, want false")
	}
}
