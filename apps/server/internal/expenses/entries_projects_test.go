package expenses_test

import (
	"net/http"
	"testing"
)

// This file is the optional project link (decisions X1, X2 and X7): what an
// expense booked on a project may say, what it computes for the customer, and
// — in the second half — what the very same requests answer in an installation
// running without the projects module at all.

func TestExpensesEntries_AnOutlayIsBookedOnAProjectTheOwnerMayUse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signInAs(t, h, projectKraftVerket, roleMember)

	created := createEntry(t, employee, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billingLineId": lineFixed,
	}))
	if created.Project == nil || created.Project.Id != projectKraftVerket {
		t.Fatalf("project = %+v, want the one booked on", created.Project)
	}
	if created.Project.Code != projectKraftVerketCode || created.Project.Name != projectKraftVerketName {
		t.Errorf("project = %+v, want it resolved through the directory", created.Project)
	}
	if created.BillingLine == nil || created.BillingLine.Id != lineFixed || created.BillingLine.Code != "PM" {
		t.Errorf("billingLine = %+v, want the line with its code", created.BillingLine)
	}
	if created.Billable {
		t.Error("billable = true, want false — booking on a project does not bill it")
	}
}

// One message on projectId whatever the reason: a project that does not exist,
// one the person holds no role on, and one no longer open for work all read
// the same, so an id cannot be probed.
func TestExpensesEntries_AProjectTheOwnerMayNotBookOnIsRefusedOnProjectId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	member, memberID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectCompleted, memberID, roleMember)

	var messages []string
	for name, projectID := range map[string]int32{
		"a project nobody has":        projectUnknown,
		"a project with no role":      projectEuro,
		"a project closed for work":   projectCompleted,
		"a viewer's own project only": projectInternal,
	} {
		t.Run(name, func(t *testing.T) {
			errs := refusedEntry(t, member, http.MethodPost, entriesPath,
				outlayBody(map[string]any{"projectId": projectID}))
			if len(errs["projectId"]) != 1 {
				t.Fatalf("errors = %v, want exactly one on projectId", errs)
			}
			messages = append(messages, errs["projectId"][0])
		})
	}
	for i := 1; i < len(messages); i++ {
		if messages[i] != messages[0] {
			t.Errorf("messages = %v, want one wording whatever the reason", messages)
		}
	}
}

func TestExpensesEntries_ABillingLineMustBeOneOfTheProjectsActiveOnes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for name, tc := range map[string]struct {
		overrides map[string]any
		field     string
	}{
		"a line of another project": {
			map[string]any{"projectId": projectKraftVerket, "billingLineId": lineEuro}, "billingLineId",
		},
		"an inactive line": {
			map[string]any{"projectId": projectKraftVerket, "billingLineId": lineInactive}, "billingLineId",
		},
		"a line with no project":   {map[string]any{"billingLineId": lineFixed}, "billingLineId"},
		"billable with no project": {map[string]any{"billable": true}, "billable"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := refusedEntry(t, employee, http.MethodPost, entriesPath, outlayBody(tc.overrides))
			if len(errs[tc.field]) == 0 {
				t.Errorf("errors = %v, want one on %s", errs, tc.field)
			}
		})
	}
}

// A billable outlay takes the settings' markup when the body names none, and
// bills net plus that markup.
func TestExpensesEntries_ABillableOutlayTakesTheDefaultMarkup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	putSettings(t, admin, settingsBody(map[string]any{"defaultMarkupPercent": 15}))
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	created := createEntry(t, manager, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "vatAmount": 250.00,
	}))
	if !created.Billable {
		t.Fatal("billable = false, want it kept on a billable project")
	}
	if created.Billing == nil {
		t.Fatalf("billing = nil, want it for a manager of the project")
	}
	if created.Billing.MarkupPercent == nil || *created.Billing.MarkupPercent != 15 {
		t.Errorf("markupPercent = %v, want the settings' default", created.Billing.MarkupPercent)
	}
	// Net 1000 × 1.15.
	if created.Billing.BillAmount != 1150 {
		t.Errorf("billAmount = %v, want net × (1 + markup)", created.Billing.BillAmount)
	}
	if !created.Capabilities.CanSeeBilling {
		t.Error("canSeeBilling = false, want the capability to agree with the body")
	}

	// A markup on the body wins, and one on a line that is not billable is
	// refused rather than quietly stored.
	own := createEntry(t, manager, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "markupPercent": 7.5,
	}))
	if own.Billing == nil || own.Billing.MarkupPercent == nil || *own.Billing.MarkupPercent != 7.5 {
		t.Errorf("markupPercent = %+v, want the one given", own.Billing)
	}
	errs := refusedEntry(t, manager, http.MethodPost, entriesPath, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "markupPercent": 10,
	}))
	if len(errs["markupPercent"]) == 0 {
		t.Errorf("a markup on a line nobody bills: errors = %v, want one on markupPercent", errs)
	}
}

// Billable mileage is billed at the customer rate per kilometre, which
// defaults from the table for the entry's date.
func TestExpensesEntries_BillableMileageTakesTheCustomerRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	// With no customer rate in force the field must be given.
	errs := refusedEntry(t, manager, http.MethodPost, entriesPath, mileageBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))
	if len(errs["billRatePerKm"]) == 0 {
		t.Fatalf("errors = %v, want one on billRatePerKm", errs)
	}
	given := createEntry(t, manager, mileageBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "billRatePerKm": 9.00,
	}))
	if given.Billing == nil || given.Billing.BillRatePerKm == nil || *given.Billing.BillRatePerKm != 9 {
		t.Fatalf("billing = %+v, want the rate given", given.Billing)
	}
	if given.Billing.BillAmount != 1080 {
		t.Errorf("billAmount = %v, want 120 × 9.00", given.Billing.BillAmount)
	}

	// Once the table has one, it is the default.
	createRate(t, admin, map[string]any{"kind": "mileage_customer", "validFrom": "2026-01-01", "value": 7.50})
	defaulted := createEntry(t, manager, mileageBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))
	if defaulted.Billing == nil || defaulted.Billing.BillRatePerKm == nil || *defaulted.Billing.BillRatePerKm != 7.50 {
		t.Errorf("billRatePerKm = %+v, want the table's", defaulted.Billing)
	}
	if defaulted.Billing.BillAmount != 900 {
		t.Errorf("billAmount = %v, want 120 × 7.50", defaulted.Billing.BillAmount)
	}

	// The rate belongs to billable mileage and to nothing else.
	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"on a line nobody bills": {
			mileageBody(map[string]any{"projectId": projectKraftVerket, "billRatePerKm": 9.0}), "billRatePerKm",
		},
		"on an outlay": {
			outlayBody(map[string]any{"projectId": projectKraftVerket, "billable": true, "billRatePerKm": 9.0}), "billRatePerKm",
		},
		"a markup on mileage": {
			mileageBody(map[string]any{"projectId": projectKraftVerket, "billable": true, "markupPercent": 10.0}), "markupPercent",
		},
	} {
		t.Run(name, func(t *testing.T) {
			errs := refusedEntry(t, manager, http.MethodPost, entriesPath, tc.body)
			if len(errs[tc.field]) == 0 {
				t.Errorf("errors = %v, want one on %s", errs, tc.field)
			}
		})
	}
}

// A non-billable project's lines are never billable, whatever the body asked
// for, and store no billing figures.
func TestExpensesEntries_ANonBillableProjectBillsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectInternal, roleManager)

	created := createEntry(t, manager, outlayBody(map[string]any{
		"projectId": projectInternal, "billable": true,
	}))
	if created.Billable {
		t.Error("billable = true, want it forced false on a non-billable project")
	}
	if created.Billing == nil {
		t.Fatal("billing = nil, want the object for a manager of the project")
	}
	if created.Billing.BillAmount != 0 || created.Billing.MarkupPercent != nil {
		t.Errorf("billing = %+v, want no figures on a line nobody bills", created.Billing)
	}
}

// The money on an expense is the project's business, not the owner's: the
// billing object is there for financial rights and for nobody else, the owner
// included.
func TestExpensesEntries_BillingIsForFinancialRightsAndNotForTheOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	created := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))

	if got := getEntry(t, owner, created.Id); got.Billing != nil || got.Capabilities.CanSeeBilling {
		t.Errorf("the owner's copy carries billing %+v, want none — a member sees no project money", got.Billing)
	}
	if raw := rawEntry(t, owner, created.Id); raw["billing"] != nil {
		t.Errorf("billing = %v, want the key absent rather than null", raw["billing"])
	}
	// The owner still sees their own gross, net and what they are owed.
	if got := getEntry(t, owner, created.Id); got.GrossAmount != 1250 || got.OwedToEmployee != 1250 {
		t.Errorf("the owner's copy = %+v, want their own money on it", got)
	}

	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	if got := getEntry(t, manager, created.Id); got.Billing == nil || !got.Capabilities.CanSeeBilling {
		t.Errorf("the manager's copy carries no billing, want the project's money")
	}
	financials, _ := signInAs(t, h, projectKraftVerket, roleViewer, "projects:view-financials", "expenses:view-all")
	if got := getEntry(t, financials, created.Id); got.Billing == nil {
		t.Error("projects:view-financials on a project they see carries no billing, want it")
	}
	viewAll, _ := signIn(t, h, "expenses:view-all")
	if got := getEntry(t, viewAll, created.Id); got.Billing != nil {
		t.Errorf("expenses:view-all carries billing %+v, want none — it is not a project right", got.Billing)
	}
	if ownerID == (created.Owner.UserId) && created.Owner.DisplayName == "" {
		t.Error("the owner has no display name, want identity's")
	}
}

// Booking for a colleague checks the colleague's right, not the recorder's
// (design §8).
func TestExpensesEntries_RecordingForAColleagueChecksTheColleaguesProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, adminID := signIn(t, h, "expenses:manage")
	_, colleagueID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, adminID, roleManager)

	errs := refusedEntry(t, admin, http.MethodPost, entriesPath, outlayBody(map[string]any{
		"userId": colleagueID.String(), "projectId": projectKraftVerket,
	}))
	if len(errs["projectId"]) == 0 {
		t.Errorf("the recorder may book on it but the colleague may not: errors = %v, want one on projectId", errs)
	}

	h.projects.addRole(projectKraftVerket, colleagueID, roleMember)
	created := createEntry(t, admin, outlayBody(map[string]any{
		"userId": colleagueID.String(), "projectId": projectKraftVerket,
	}))
	if created.Owner.UserId != colleagueID {
		t.Errorf("owner = %+v, want the colleague", created.Owner)
	}
}

// A project the directory no longer knows leaves the entry readable: the stored
// id stays, and the project block is simply absent (decision X2).
func TestExpensesEntries_AProjectTheDirectoryLostLeavesTheEntryReadable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signInAs(t, h, projectKraftVerket, roleMember)
	created := createEntry(t, employee, outlayBody(map[string]any{"projectId": projectKraftVerket}))

	h.projects.removeProject(projectKraftVerket)
	got := getEntry(t, employee, created.Id)
	if got.Id != created.Id {
		t.Fatalf("read back = %+v, want the entry", got)
	}
	if got.Project != nil {
		t.Errorf("project = %+v, want it absent once the directory lost it", got.Project)
	}
}

// GET /projects is the form's project picker: the projects the caller may book
// on, with their active billing lines.
func TestExpensesProjects_AreTheOnesTheCallerMayBookOnWithTheirLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, employeeID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectCompleted, employeeID, roleMember)
	h.projects.addRole(projectEuro, employeeID, roleViewer)
	h.projects.addRole(projectInternal, employeeID, roleManager)

	options := listProjectOptions(t, employee)
	var ids []int32
	for _, o := range options {
		ids = append(ids, o.Id)
	}
	if len(ids) != 2 || ids[0] != projectKraftVerket || ids[1] != projectInternal {
		t.Fatalf("options = %v, want the two the caller may book on, by id", ids)
	}
	first := options[0]
	if first.Code != projectKraftVerketCode || first.Name != projectKraftVerketName {
		t.Errorf("option = %+v, want its code and name", first)
	}
	if first.Currency == nil || *first.Currency != "NOK" {
		t.Errorf("currency = %v, want the project's", first.Currency)
	}
	if len(first.BillingLines) != 1 || first.BillingLines[0].Id != lineFixed || first.BillingLines[0].Code != "PM" {
		t.Errorf("billingLines = %+v, want only the active one", first.BillingLines)
	}
	if len(options[1].BillingLines) != 0 {
		t.Errorf("a project with no lines = %+v, want an empty list rather than null", options[1].BillingLines)
	}
}

// ---------------------------------------------------------------------------
// The same module with MODULES=customers,expenses: no project field is
// accepted anywhere, and the picker is not there at all.
// ---------------------------------------------------------------------------

func TestExpensesEntries_WithoutProjects_RefusesEveryProjectFieldOnItsOwnField(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	employee, _ := signIn(t, h)

	for field, body := range map[string]map[string]any{
		"projectId":     outlayBody(map[string]any{"projectId": projectKraftVerket}),
		"billingLineId": outlayBody(map[string]any{"billingLineId": lineFixed}),
		"billable":      outlayBody(map[string]any{"billable": true}),
		"markupPercent": outlayBody(map[string]any{"markupPercent": 10}),
		"billRatePerKm": mileageBody(map[string]any{"billRatePerKm": 9.0}),
	} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			errs := refusedEntry(t, employee, http.MethodPost, entriesPath, body)
			if len(errs[field]) == 0 {
				t.Errorf("errors = %v, want one on %s", errs, field)
			}
		})
	}
}

// An expense without a project is exactly what it is with projects composed,
// and billable:false is not a project field at all.
func TestExpensesEntries_WithoutProjects_RecordsAndPricesTheSameWay(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	employee, _ := signIn(t, h)

	outlay := createEntry(t, employee, outlayBody(map[string]any{"vatAmount": 250.00, "billable": false}))
	if outlay.NetAmount != 1000 || outlay.OwedToEmployee != 1250 {
		t.Errorf("net/owed = %v/%v, want 1000 and 1250", outlay.NetAmount, outlay.OwedToEmployee)
	}
	if outlay.Project != nil || outlay.BillingLine != nil || outlay.Billing != nil {
		t.Errorf("outlay = %+v, want nothing project-shaped on it", outlay)
	}
	mileage := createEntry(t, employee, mileageBody(nil))
	if mileage.GrossAmount != 636 {
		t.Errorf("grossAmount = %v, want 120 × 5.30", mileage.GrossAmount)
	}

	updated := updateEntry(t, employee, outlay.Id, outlayBody(map[string]any{"revision": outlay.Revision}))
	if updated.Revision != outlay.Revision+1 {
		t.Errorf("revision = %d, want it moved on", updated.Revision)
	}
	listed := listEntries(t, employee, "")
	if listed.Pagination.TotalCount != 2 {
		t.Errorf("total = %d, want both entries", listed.Pagination.TotalCount)
	}
}

func TestExpensesProjects_WithoutProjects_IsNotThere(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	employee, _ := signIn(t, h)

	r := employee.Do(http.MethodGet, projectOptionsPath, nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("the project picker without projects: status %d body %s, want 404", r.Status, r.Body)
	}
	var problem struct {
		Title string `json:"title"`
	}
	r.JSON(&problem)
	if problem.Title == "" {
		t.Errorf("body = %s, want a problem saying the module is not installed", r.Body)
	}
}
