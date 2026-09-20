package expenses_test

import (
	"fmt"
	"net/http"
	"slices"
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

// The period lock protects what the employee submitted and what was approved.
// Pricing is bookkeeping done after a period closes — an invoice for December
// goes out in January — so the lock does not hold it back, and a project's
// manager needs no expenses:manage to do it.
func TestExpensesBilling_IsNotHeldBackByThePeriodLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-04-01"}))

	if got := getEntry(t, manager, entry.Id); !got.Capabilities.CanSetBilling {
		t.Fatalf("canSetBilling = false behind the lock, want the project side able to price it")
	}
	priced := setBilling(t, manager, entry.Id, setBillingBody(entry.Revision,
		map[string]any{"markupPercent": 10.00}))
	if priced.Billing == nil || priced.Billing.BillAmount != 1375 {
		t.Errorf("billing = %+v, want 1250 plus ten per cent", priced.Billing)
	}
	// And the lock still holds back what it is for: the owner cannot edit it.
	if errs := refusedEntry(t, owner, http.MethodPut, entryPath(entry.Id),
		outlayBody(map[string]any{"revision": priced.Revision})); len(errs["entryDate"]) == 0 {
		t.Errorf("editing behind the lock: errors = %v, want one naming the lock", errs)
	}
}

// Design §5's financial rights are three, not one: the project's manager role,
// projects:manage-all, and projects:view-financials on a project the caller can
// see. All three price; the owner, who is only a member of the project, does
// not.
func TestExpensesBilling_IsForEveryFinancialRightAndNotTheOwners(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	// Both hold expenses:view-all, because a project permission alone sees no
	// expenses at all (design §5): seeing the line comes first, and only then
	// does a financial right decide whether its money comes with it.
	manageAll, _ := signIn(t, h, "expenses:view-all", "projects:manage-all")
	financials, _ := signIn(t, h, "expenses:view-all", "projects:view-financials", "projects:view-all")

	entry := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))

	// The owner is a member of the project, so they see it — but what the
	// company charges the customer for their expense is not theirs.
	if got := getEntry(t, owner, entry.Id); got.Capabilities.CanSetBilling || got.Capabilities.CanSeeBilling {
		t.Errorf("the owner's capabilities = %+v, want no billing at all", got.Capabilities)
	}
	if r := owner.Do(http.MethodPut, entryBillingPath(entry.Id), setBillingBody(entry.Revision, nil)); r.Status != http.StatusForbidden {
		t.Errorf("the owner pricing: status %d body %s, want 403", r.Status, r.Body)
	}

	first := setBilling(t, manageAll, entry.Id, setBillingBody(entry.Revision,
		map[string]any{"markupPercent": 10.00}))
	if first.Billing == nil || first.Billing.BillAmount != 1375 {
		t.Errorf("projects:manage-all priced it to %+v, want 1375", first.Billing)
	}
	second := setBilling(t, financials, entry.Id, setBillingBody(first.Revision,
		map[string]any{"markupPercent": 20.00}))
	if second.Billing == nil || second.Billing.BillAmount != 1500 {
		t.Errorf("projects:view-financials priced it to %+v, want 1500", second.Billing)
	}
}

// Design §8's grandfathering reaches the pricing door too: a billing line the
// expense already carries is kept even once it stops accepting new bookings, so
// a markup can still be corrected on a line whose code has been retired.
func TestExpensesBilling_AKeptBillingLineIsNotJudgedAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	entry := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	priced := setBilling(t, manager, entry.Id, setBillingBody(entry.Revision,
		map[string]any{"markupPercent": 10.00, "billingLineId": lineFixed}))

	h.projects.deactivateLine(lineFixed)
	t.Cleanup(func() { h.projects.activateLine(lineFixed) })

	again := setBilling(t, manager, entry.Id, setBillingBody(priced.Revision,
		map[string]any{"markupPercent": 25.00, "billingLineId": lineFixed}))
	if again.BillingLine == nil || again.BillingLine.Id != lineFixed {
		t.Errorf("billingLine = %+v, want the retired line kept", again.BillingLine)
	}
	if again.Billing == nil || again.Billing.BillAmount != 1562.5 {
		t.Errorf("billing = %+v, want 1250 plus a quarter", again.Billing)
	}
	// Moving *onto* an inactive line is still refused.
	if errs := refusedEntry(t, manager, http.MethodPut, entryBillingPath(entry.Id),
		setBillingBody(again.Revision, map[string]any{"billingLineId": lineInactive})); len(errs["billingLineId"]) == 0 {
		t.Errorf("moving onto an inactive line: errors = %v, want one on billingLineId", errs)
	}
}

// A project that bills nothing cannot be given a markup: the figure would be
// accepted, cleared and never mentioned again. It is refused on its own field,
// through every door that takes one.
func TestExpensesBilling_AFigureAProjectWillNeverUseIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// The project's own manager, recording their own expense on it: the one
	// caller who may both write the expense (its owner) and name a figure on
	// it (financial rights on the project).
	manager, _ := signInAs(t, h, projectInternal, roleManager)

	create := outlayBody(map[string]any{
		"projectId": projectInternal, "billable": true, "markupPercent": 20.00,
	})
	if errs := refusedEntry(t, manager, http.MethodPost, entriesPath, create); len(errs["markupPercent"]) == 0 {
		t.Errorf("POST /entries: errors = %v, want one on markupPercent", errs)
	}
	mileageCreate := mileageBody(map[string]any{
		"projectId": projectInternal, "billable": true, "billRatePerKm": 9.00,
	})
	if errs := refusedEntry(t, manager, http.MethodPost, entriesPath, mileageCreate); len(errs["billRatePerKm"]) == 0 {
		t.Errorf("POST /entries mileage: errors = %v, want one on billRatePerKm", errs)
	}

	entry := createEntry(t, manager, outlayBody(map[string]any{"projectId": projectInternal}))
	update := outlayBody(map[string]any{
		"projectId": projectInternal, "billable": true, "markupPercent": 20.00, "revision": entry.Revision,
	})
	if errs := refusedEntry(t, manager, http.MethodPut, entryPath(entry.Id), update); len(errs["markupPercent"]) == 0 {
		t.Errorf("PUT /entries/{id}: errors = %v, want one on markupPercent", errs)
	}
	if errs := refusedEntry(t, manager, http.MethodPut, entryBillingPath(entry.Id),
		setBillingBody(entry.Revision, map[string]any{"markupPercent": 20.00})); len(errs["markupPercent"]) == 0 {
		t.Errorf("PUT /billing: errors = %v, want one on markupPercent", errs)
	}

	// Asking for nothing billable on such a project is still fine, and still
	// bills nothing.
	priced := setBilling(t, manager, entry.Id, setBillingBody(entry.Revision, nil))
	if priced.Billable {
		t.Errorf("priced = %+v, want billable forced false", priced)
	}
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

// billingLinesPath is the pricing dialog's own read: the lines of the expense's
// project, judged by the right to price rather than the right to book.
func billingLinesPath(id int64) string {
	return fmt.Sprintf("%s/%d/billing-lines", entriesPath, id)
}

// billingLineOptionJSON decodes ExpensesBillingLineOption.
type billingLineOptionJSON struct {
	Id     int32  `json:"id"`
	Code   string `json:"code"`
	Active bool   `json:"active"`
}

// getBillingLines reads them and fails the test unless it answered 200.
func getBillingLines(t *testing.T, c *modtest.Client, id int64) []billingLineOptionJSON {
	t.Helper()
	r := c.Do(http.MethodGet, billingLinesPath(id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("billing lines of %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	var lines []billingLineOptionJSON
	r.JSON(&lines)
	return lines
}

// lineCodes is the codes a read answered, in the order it answered them.
func lineCodes(lines []billingLineOptionJSON) []string {
	codes := make([]string, 0, len(lines))
	for _, l := range lines {
		codes = append(codes, l.Code)
	}
	return codes
}

// The pricing dialog needs the lines of the expense's project, and the right to
// price is not the right to book: GET /projects answers what the *caller* may
// book on (a member or a manager of a project still open for work), so a
// finance person on no project team, and anybody pricing a line on a completed
// project, would be offered nothing at all. This read is keyed on the expense
// and judged by exactly the rule the pricing door is judged by.
func TestExpensesBillingLines_AreTheRightToPriceRatherThanTheRightToBook(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	entry := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))

	// A finance person holding no role on any project at all. GET /projects
	// offers them nothing; this offers the expense's own lines.
	finance, _ := signIn(t, h, "expenses:view-all", "projects:view-all", "projects:view-financials")
	if options := listProjectOptions(t, finance); len(options) != 0 {
		t.Fatalf("the picker offers them %+v, want nothing — they may book on no project", options)
	}
	if codes := lineCodes(getBillingLines(t, finance, entry.Id)); !slices.Equal(codes, []string{"PM"}) {
		t.Errorf("the finance reader got %v, want the project's one active line", codes)
	}
	// And so does the project's manager.
	if codes := lineCodes(getBillingLines(t, manager, entry.Id)); !slices.Equal(codes, []string{"PM"}) {
		t.Errorf("the manager got %v, want the project's one active line", codes)
	}

	// A project that has been completed accepts no new bookings, so its manager
	// is offered nothing by the picker — and still has to be able to price what
	// was booked on it while it was open.
	h.projects.setCanLogTime(projectKraftVerket, false)
	t.Cleanup(func() { h.projects.clearCanLogTime(projectKraftVerket) })
	if options := listProjectOptions(t, manager); len(options) != 0 {
		t.Fatalf("the picker offers the manager %+v, want nothing on a closed project", options)
	}
	if codes := lineCodes(getBillingLines(t, manager, entry.Id)); !slices.Equal(codes, []string{"PM"}) {
		t.Errorf("the manager of a closed project got %v, want its lines all the same", codes)
	}
}

// The line the expense already carries stays in the list even once the project
// has stopped using it, flagged so the dialog can show what is stored without
// offering it again. Every other line in the list is active, and they come by
// code.
func TestExpensesBillingLines_KeepTheEntrysOwnLineEvenOnceItIsDeactivated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	// OLD is in use while the expense is booked on it.
	h.projects.activateLine(lineInactive)
	onIt := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billingLineId": lineInactive,
	}))
	elsewhere := createEntry(t, owner, outlayBody(map[string]any{
		"description": "Skruer", "projectId": projectKraftVerket, "billingLineId": lineFixed,
	}))
	// And goes out of use afterwards — design §8's "existing lines stay".
	h.projects.deactivateLine(lineInactive)

	lines := getBillingLines(t, manager, onIt.Id)
	if codes := lineCodes(lines); !slices.Equal(codes, []string{"OLD", "PM"}) {
		t.Fatalf("lines = %v, want the stored OLD beside the active PM, by code", codes)
	}
	for _, l := range lines {
		if want := l.Code == "PM"; l.Active != want {
			t.Errorf("%s active = %v, want %v", l.Code, l.Active, want)
		}
	}
	// An expense on another line of the same project is not offered OLD at all.
	if codes := lineCodes(getBillingLines(t, manager, elsewhere.Id)); !slices.Equal(codes, []string{"PM"}) {
		t.Errorf("lines of an expense on PM = %v, want the active one alone", codes)
	}
}

// The four refusals are the pricing door's own, in the same order, so the two
// can never disagree about who may price what.
func TestExpensesBillingLines_RefuseExactlyWhatThePricingDoorRefuses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	entry := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))

	// The owner sees the expense and not the money its project makes on it.
	forbidden(t, owner, http.MethodGet, billingLinesPath(entry.Id), nil)
	// A stranger gets the unknown id's bare 404, body and all.
	stranger, _ := signIn(t, h)
	r := stranger.Do(http.MethodGet, billingLinesPath(entry.Id), nil)
	unknown := stranger.Do(http.MethodGet, billingLinesPath(90210), nil)
	if r.Status != http.StatusNotFound || unknown.Status != http.StatusNotFound {
		t.Errorf("a stranger got %d and an unknown id %d, want 404 for both", r.Status, unknown.Status)
	}
	if string(r.Body) != string(unknown.Body) {
		t.Errorf("a stranger's body is %q and an unknown id's %q, want them identical", r.Body, unknown.Body)
	}

	// An expense on no project has nothing to bill anybody for, which is a 400
	// on projectId — the very field and the very message PUT /billing answers,
	// and before the 403, because whether an expense they can already see
	// carries a project is in their own copy of it.
	loose := createEntry(t, owner, outlayBody(map[string]any{"description": "Uten prosjekt"}))
	errs := refusedEntry(t, owner, http.MethodGet, billingLinesPath(loose.Id), nil)
	if len(errs["projectId"]) == 0 {
		t.Errorf("an expense on no project: errors = %v, want one on projectId", errs)
	}
	// A project manager cannot see a colleague's project-less expense at all,
	// so for them it is the unknown id's 404 — the visibility rule, unchanged.
	if r := manager.Do(http.MethodGet, billingLinesPath(loose.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a project manager on a project-less expense: status %d, want 404", r.Status)
	}
	// And the pricing door itself answers the two the same way.
	if r := owner.Do(http.MethodPut, entryBillingPath(loose.Id),
		map[string]any{"revision": loose.Revision, "billable": false}); r.Status != http.StatusBadRequest {
		t.Errorf("PUT /billing on the same expense: status %d body %s, want the same 400", r.Status, r.Body)
	}
}

// Without the projects module the operation is not there at all, the answer
// GET /projects gives for the same reason (decision X2).
func TestExpensesBillingLines_WithoutProjects_AreNotThere(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	owner, ownerID := signIn(t, h, "expenses:manage")
	id := seedProjectedEntry(t, h, ownerID)

	r := owner.Do(http.MethodGet, billingLinesPath(id), nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("billing lines without the projects module: status %d body %s, want 404", r.Status, r.Body)
	}
	var problem struct {
		Title string `json:"title"`
	}
	r.JSON(&problem)
	if problem.Title == "" {
		t.Errorf("body = %s, want a problem saying the module is not installed", r.Body)
	}
}
