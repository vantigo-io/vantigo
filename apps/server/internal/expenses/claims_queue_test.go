package expenses_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// This file is the three reads a travel claim had to become a unit of: the
// approval queue, the reimbursement list with its payroll file, and the
// dashboard. Each of them counted, grouped and exported *expenses* before; each
// of them now counts a trip once and never lists its lines as loose ones.

// The queue lists a trip as one row of its owner's group, with the figures an
// approver decides on, and never its lines among the entries.
func TestExpensesClaimQueue_ListsATripAsOneUnit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	boss, _ := signIn(t, h, "expenses:approve")

	loose := createEntry(t, owner, outlayBody(map[string]any{"description": "Løst utlegg"}))
	claim := createClaim(t, owner, nil)
	addLine(t, owner, claim.Id, outlayBody(nil))  // 1250,00, no receipt
	addLine(t, owner, claim.Id, mileageBody(nil)) // 636,00
	addLine(t, owner, claim.Id, perDiemBody(nil)) // 397,00
	submitEntries(t, owner, loose.Id)
	submitClaims(t, owner, claim.Id)

	page := getApprovals(t, boss, "")
	if len(page.Data) != 1 || page.Data[0].User.UserId != ownerID {
		t.Fatalf("the queue is %+v, want one group for the one person", page.Data)
	}
	group := page.Data[0]
	if len(group.Entries) != 1 || group.Entries[0].Id != loose.Id {
		t.Errorf("entries = %+v, want the loose expense alone — a trip's lines are never here", group.Entries)
	}
	summary := claimSummaryByID(t, group.Claims, claim.Id)
	switch {
	case summary.Purpose != "Montasje hos kunden":
		t.Errorf("purpose = %q, want the trip's own", summary.Purpose)
	case summary.LineCount != 3:
		t.Errorf("lineCount = %d, want 3", summary.LineCount)
	case summary.ReceiptsMissing != 1:
		t.Errorf("receiptsMissing = %d, want the one outlay with no receipt", summary.ReceiptsMissing)
	case summary.OverriddenRates != 0:
		t.Errorf("overriddenRates = %d, want none", summary.OverriddenRates)
	case !summary.Capabilities.CanApprove:
		t.Errorf("capabilities = %+v, want the approver's canApprove", summary.Capabilities)
	}
	if len(summary.Totals) != 1 || summary.Totals[0].Gross != 2283.00 {
		t.Errorf("totals = %+v, want 1250 + 636 + 397 in NOK", summary.Totals)
	}
	// The group's own figures hold both units: the loose expense and the trip.
	if len(group.Totals) != 1 || group.Totals[0].Gross != 3533.00 {
		t.Errorf("group totals = %+v, want the loose expense and the trip together", group.Totals)
	}
	if group.ReceiptsMissing != 2 {
		t.Errorf("group receiptsMissing = %d, want the loose outlay and the trip's", group.ReceiptsMissing)
	}
	// And the queue pages by person, so the count is of people and not of units.
	if page.Pagination.TotalCount != 1 {
		t.Errorf("totalCount = %d, want one person", page.Pagination.TotalCount)
	}
}

// A project manager's queue holds the trips on their own projects and no
// project-less ones, exactly as it holds expenses.
func TestExpensesClaimQueue_AProjectManagerSeesTheirProjectsTrips(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)

	theirs := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	addLine(t, owner, theirs.Id, outlayBody(nil))
	loose := createClaim(t, owner, map[string]any{"purpose": "Uten prosjekt"})
	addLine(t, owner, loose.Id, outlayBody(nil))
	submitClaims(t, owner, theirs.Id, loose.Id)

	page := getApprovals(t, manager, "")
	if len(page.Data) != 1 {
		t.Fatalf("the queue is %+v, want the one person", page.Data)
	}
	// The person has nothing but trips, so the queue's own total is of people
	// who have a *unit* waiting and not only of people with loose expenses.
	if page.Pagination.TotalCount != 1 {
		t.Errorf("totalCount = %d, want the one person — whose only waiting unit is a trip",
			page.Pagination.TotalCount)
	}
	ids := make([]int64, 0, len(page.Data[0].Claims))
	for _, claim := range page.Data[0].Claims {
		ids = append(ids, claim.Id)
	}
	if len(ids) != 1 || ids[0] != theirs.Id {
		t.Errorf("the manager's queue holds %v, want their own project's trip alone", ids)
	}
}

// The reimbursement list holds a trip as one unit owed the sum of what its
// lines owe, and marking it pays the whole thing.
func TestExpensesClaimReimbursement_PaysATripAsOneUnit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
	owner, ownerID := signIn(t, h)

	claim := createClaim(t, owner, nil)
	addLine(t, owner, claim.Id, outlayBody(nil))           // 1250,00 employee-paid
	addLine(t, owner, claim.Id, mileageBody(nil))          // 636,00
	addLine(t, owner, claim.Id, perDiemBody(nil))          // 397,00
	addLine(t, owner, claim.Id, outlayBody(map[string]any{ // the company's own
		"paidBy": "company", "description": "Firmakortet", "grossAmount": 500.00,
	}))
	approvedClaimBy(t, owner, admin, claim.Id)

	page := getReimbursements(t, admin, "")
	if len(page.Data) != 1 || page.Data[0].User.UserId != ownerID {
		t.Fatalf("the list is %+v, want one group", page.Data)
	}
	// The person is owed nothing but the trip, so the list's own total counts a
	// person whose only unit is one.
	if page.Pagination.TotalCount != 1 {
		t.Errorf("totalCount = %d, want the one person owed a trip", page.Pagination.TotalCount)
	}
	group := page.Data[0]
	if len(group.Entries) != 0 {
		t.Errorf("entries = %+v, want none — a trip's lines are never loose here", group.Entries)
	}
	summary := claimSummaryByID(t, group.Claims, claim.Id)
	if len(summary.Totals) != 1 || summary.Totals[0].OwedToEmployee != 2283.00 {
		t.Errorf("owed = %+v, want 1250 + 636 + 397 — the company's own outlay owes nothing",
			summary.Totals)
	}
	if !summary.Capabilities.CanMarkReimbursed {
		t.Errorf("capabilities = %+v, want canMarkReimbursed", summary.Capabilities)
	}

	markClaimsReimbursed(t, admin, reimbursedClaimBody([]int64{claim.Id},
		map[string]any{"reference": "LØNN-2026-04"}))
	paid := getClaim(t, owner, claim.Id)
	if paid.Reimbursement == nil || paid.Reimbursement.Date != "2026-04-02" {
		t.Fatalf("the claim is %+v, want the payroll stamp on the claim", paid)
	}
	// The stamp is the claim's; its lines carry none of their own.
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE claim_id = $1 AND reimbursed_at IS NULL`, claim.Id); n != 4 {
		t.Errorf("%d of the lines are unstamped, want all four — the unit that was paid is the trip", n)
	}
	// Every line reads the claim's stamp through the one renderer.
	for _, line := range paid.Lines {
		if line.Reimbursement == nil {
			t.Errorf("line %d shows no reimbursement, want its claim's", line.Id)
		}
	}

	if waiting := getReimbursements(t, admin, ""); len(waiting.Data) != 0 {
		t.Errorf("the waiting list is %+v, want it empty once the trip is paid", waiting.Data)
	}
	done := getReimbursements(t, admin, "?state=reimbursed")
	if len(done.Data) != 1 || len(done.Data[0].Claims) != 1 {
		t.Errorf("the paid list is %+v, want the trip", done.Data)
	}

	undoClaimsReimbursed(t, admin, claim.Id)
	if again := getClaim(t, owner, claim.Id); again.Reimbursement != nil {
		t.Errorf("the claim is %+v, want the whole stamp off", again)
	}
	if back := getReimbursements(t, admin, ""); len(back.Data) != 1 {
		t.Errorf("the waiting list is %+v, want the trip back in it", back.Data)
	}
}

// A trip that owes its owner nothing cannot go on a payroll run, for the reason
// a company-paid outlay cannot — and it is not in the list at all.
func TestExpensesClaimReimbursement_ATripThatOwesNothingCannotBeMarked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
	owner, _ := signIn(t, h)

	claim := createClaim(t, owner, nil)
	addLine(t, owner, claim.Id, outlayBody(map[string]any{"paidBy": "company"}))
	approvedClaimBy(t, owner, admin, claim.Id)

	if page := getReimbursements(t, admin, ""); len(page.Data) != 0 {
		t.Errorf("the list is %+v, want nothing — the trip owes its owner nothing", page.Data)
	}
	if got := getClaim(t, owner, claim.Id); got.Capabilities.CanMarkReimbursed {
		t.Errorf("canMarkReimbursed is true on a trip that owes nothing, want false")
	}
	errs := refusedReimbursement(t, admin, reimbursedPath, reimbursedClaimBody([]int64{claim.Id}, nil))
	if !mentions(errs["claimIds"], fmt.Sprintf("Travel claim %d owes the employee nothing", claim.Id)) {
		t.Errorf("errors %v do not say the trip owes nothing", errs)
	}
}

// The payroll file is a file of **lines**: a standalone expense is its own row,
// a trip writes one row per expense it holds, and the two leading columns say
// which unit each row belongs to.
func TestExpensesClaimExport_WritesOneRowPerLineUnderItsUnit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
	anna, annaID := signIn(t, h)
	nameUser(t, h, annaID, "Anna Ås")

	loose := createEntry(t, anna, outlayBody(map[string]any{"description": "Løst utlegg"}))
	approvedBy(t, anna, admin, loose.Id)

	claim := createClaim(t, anna, map[string]any{"purpose": "Montasje; \"Bergen\""})
	mileage := addLine(t, anna, claim.Id, mileageBody(map[string]any{
		"entryDate": "2026-03-09", "description": "Til anlegget",
	}))
	day := addLine(t, anna, claim.Id, perDiemBody(map[string]any{"entryDate": "2026-03-11"}))
	approvedClaimBy(t, anna, admin, claim.Id)

	want := csvBOM + strings.Join([]string{
		"Unit;Purpose;Employee;User id;Date;Kind;Description;Category;Currency;Gross;VAT;Owed;Project code",
		fmt.Sprintf("claim %d;\"Montasje; \"\"Bergen\"\"\";Anna Ås;%s;2026-03-09;mileage;Til anlegget;;NOK;636,00;;636,00;",
			claim.Id, annaID),
		fmt.Sprintf("expense %d;;Anna Ås;%s;2026-03-10;outlay;Løst utlegg;Materials;NOK;1250,00;;1250,00;",
			loose.Id, annaID),
		fmt.Sprintf("claim %d;\"Montasje; \"\"Bergen\"\"\";Anna Ås;%s;2026-03-11;per_diem;day_6_12;;NOK;397,00;;397,00;",
			claim.Id, annaID),
		"",
	}, "\r\n")
	if got := string(exportCSV(t, admin, "").Body); got != want {
		t.Errorf("the export is\n%q\nwant\n%q", got, want)
	}
	_ = mileage
	_ = day

	// And a selection of units exports exactly those, the trip by its own id.
	picked := string(exportCSV(t, admin, fmt.Sprintf("?claimIds=%d", claim.Id)).Body)
	switch {
	case !strings.Contains(picked, "Til anlegget"):
		t.Errorf("the picked export = %q, want the trip's lines", picked)
	case strings.Contains(picked, "Løst utlegg"):
		t.Errorf("the picked export = %q, want the trip alone", picked)
	}
	// An id the export cannot hold is named under its own list.
	draft := createClaim(t, anna, map[string]any{"purpose": "Ikke godkjent"})
	addLine(t, anna, draft.Id, outlayBody(nil))
	errs := refusedExport(t, admin, fmt.Sprintf("?claimIds=%d&claimIds=90210", draft.Id))
	for _, wanted := range []string{
		fmt.Sprintf("Travel claim %d cannot be exported", draft.Id),
		"Travel claim 90210 was not found",
	} {
		if !mentions(errs["claimIds"], wanted) {
			t.Errorf("errors %v do not say %q", errs["claimIds"], wanted)
		}
	}
}

// The dashboard counts units. A trip is one draft, one submitted, one approved;
// its lines are never counted, and what it owes its owner is in myUnreimbursed.
func TestExpensesClaimStats_CountsUnitsAndSumsWhatATripOwes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
	owner, _ := signIn(t, h)

	claim := createClaim(t, owner, nil)
	addLine(t, owner, claim.Id, outlayBody(nil))
	addLine(t, owner, claim.Id, mileageBody(nil))
	loose := createEntry(t, owner, outlayBody(map[string]any{"description": "Løst"}))

	before := getStats(t, owner)
	if before.Draft != 2 {
		t.Fatalf("drafts = %d, want the trip and the loose expense", before.Draft)
	}
	submitClaims(t, owner, claim.Id)
	mid := getStats(t, owner)
	if mid.Draft != 1 || mid.Submitted != 1 {
		t.Errorf("stats = %+v, want one draft and one submitted unit", mid)
	}
	if mid.AwaitingMyApproval != 0 {
		t.Errorf("awaitingMyApproval = %d for somebody who approves nothing, want 0", mid.AwaitingMyApproval)
	}
	// The approver's own figure counts the trip once, not once per line.
	if got := getStats(t, admin); got.AwaitingMyApproval != 1 {
		t.Errorf("the approver's awaitingMyApproval = %d, want the one trip", got.AwaitingMyApproval)
	}

	approveClaims(t, admin, claim.Id)
	approvedBy(t, owner, admin, loose.Id)
	after := getStats(t, owner)
	if after.Approved != 2 {
		t.Errorf("approved = %d, want the trip and the loose expense", after.Approved)
	}
	if len(after.Unreimbursed) != 1 || after.Unreimbursed[0].Amount != 1250.00+636.00+1250.00 {
		t.Errorf("unreimbursed = %+v, want the trip's lines and the loose expense", after.Unreimbursed)
	}
	// The timeseries counts the trip's lines by each line's own date.
	buckets := getTimeseries(t, owner, marchPeriod+"&metric=netAmount")
	var total float64
	for _, b := range buckets {
		total += b.Value
	}
	if total != 1250.00+636.00+1250.00 {
		t.Errorf("the timeseries adds to %.2f, want the trip's two lines and the loose expense", total)
	}
}

// A rejected trip is one attention item, titled by its purpose and pointing at
// "claim/<id>" — never one item per line.
func TestExpensesClaimStats_ARejectedTripIsOneAttentionItem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, ownerID := signIn(t, h)

	claim := createClaim(t, owner, map[string]any{"purpose": "Kurs i Trondheim"})
	addLine(t, owner, claim.Id, outlayBody(nil))
	addLine(t, owner, claim.Id, mileageBody(nil))
	submitClaims(t, owner, claim.Id)
	rejectClaims(t, boss, "Mangler bilag", claim.Id)

	items := attentionOfType(getAttention(t, owner), "expenseRejected")
	if len(items) != 1 {
		t.Fatalf("the rejected items are %+v, want one for the trip", items)
	}
	want := fmt.Sprintf("claim/%d", claim.Id)
	switch {
	case items[0].EntityId != want:
		t.Errorf("entityId = %q, want %q", items[0].EntityId, want)
	case items[0].Id != want:
		t.Errorf("id = %q, want %q", items[0].Id, want)
	case items[0].Title != "Kurs i Trondheim":
		t.Errorf("title = %q, want the trip's purpose", items[0].Title)
	}

	// The approver's waiting item counts units, and the payroll one too.
	other := createClaim(t, owner, map[string]any{"purpose": "Enda en tur"})
	addLine(t, owner, other.Id, outlayBody(nil))
	loose := createEntry(t, owner, outlayBody(nil))
	submitClaims(t, owner, other.Id)
	submitEntries(t, owner, loose.Id)

	waiting := attentionOfType(getAttention(t, boss), "approvalWaiting")
	if len(waiting) != 1 || waiting[0].EntityId != ownerID.String() {
		t.Fatalf("the waiting items are %+v, want one per person", waiting)
	}
	if waiting[0].Count == nil || *waiting[0].Count != 2 {
		t.Errorf("count = %v, want the trip and the loose expense as two units", waiting[0].Count)
	}

	approveClaims(t, boss, other.Id)
	approveEntries(t, boss, loose.Id)
	payroll := attentionOfType(getAttention(t, boss), "reimbursementWaiting")
	if len(payroll) != 1 || payroll[0].Count == nil || *payroll[0].Count != 2 {
		t.Errorf("the payroll item is %+v, want two units waiting", payroll)
	}
}

// A billable line of an approved trip can be invoiced — the status it is judged
// in is the claim's — and a per diem day of one still cannot.
func TestExpensesClaimInvoiced_MarksALineOfAnApprovedTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)

	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	line := addLine(t, owner, claim.Id, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "billingLineId": lineFixed,
	}))
	day := addLine(t, owner, claim.Id, perDiemBody(nil))

	// Not yet: the trip is a draft, so its lines are not approved either.
	errs := refusedEntry(t, manager, http.MethodPost, entryInvoicedPath(line.Id),
		map[string]any{"revision": getEntry(t, manager, line.Id).Revision})
	if _, ok := errs["status"]; !ok {
		t.Errorf("errors %v do not name the status a draft trip's line is in", errs)
	}

	approvedClaimBy(t, owner, admin, claim.Id)
	marked := markInvoiced(t, manager, line.Id, map[string]any{
		"revision": getEntry(t, manager, line.Id).Revision, "reference": "F-2026-1",
	})
	if marked.Billing == nil || marked.Billing.Invoice == nil {
		t.Errorf("the line is %+v, want it invoiced once its trip is approved", marked)
	}
	// A per diem day of the very same trip is still refused on its kind.
	errs = refusedEntry(t, manager, http.MethodPost, entryInvoicedPath(day.Id),
		map[string]any{"revision": getEntry(t, manager, day.Id).Revision})
	if !mentions(errs["kind"], "A per diem day is never billed on to a customer") {
		t.Errorf("errors %v do not refuse a per diem day", errs)
	}
}

// GET /meta carries the business time zone, because meta is the one read a form
// makes and a trip's days have to be labelled in the installation's zone.
func TestExpensesMeta_CarriesTheBusinessTimeZone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)

	if got := getMeta(t, employee).TimeZone; got != "Europe/Oslo" {
		t.Errorf("meta.timeZone = %q, want the default", got)
	}
	putSettings(t, admin, settingsBody(map[string]any{"timeZone": "UTC"}))
	if got := getMeta(t, employee).TimeZone; got != "UTC" {
		t.Errorf("meta.timeZone = %q after the change, want UTC", got)
	}
	if settings, meta := getSettings(t, employee).TimeZone, getMeta(t, employee).TimeZone; settings != meta {
		t.Errorf("settings says %q and meta says %q, want one reading", settings, meta)
	}
}
