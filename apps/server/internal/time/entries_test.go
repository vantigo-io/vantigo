package timetracking_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// wantRate fails the test unless the entry's billing block is present and
// carries exactly rate in currency, from source.
func wantRate(t *testing.T, e entryJSON, rate float64, currency, source string) {
	t.Helper()
	if e.RateSource != source {
		t.Errorf("rateSource = %q, want %q", e.RateSource, source)
	}
	if e.Billing == nil {
		t.Fatalf("billing is absent, want %v %s", rate, currency)
	}
	if e.Billing.BillRate == nil || *e.Billing.BillRate != rate {
		t.Errorf("billing.billRate = %v, want %v", deref(e.Billing.BillRate), rate)
	}
	if e.Billing.Currency == nil || *e.Billing.Currency != currency {
		t.Errorf("billing.currency = %v, want %q", deref(e.Billing.Currency), currency)
	}
}

// wantNoRate fails the test unless the entry resolved no bill rate while its
// billing block is still present (the owner may see it, there is nothing in
// it).
func wantNoRate(t *testing.T, e entryJSON) {
	t.Helper()
	if e.RateSource != "none" {
		t.Errorf("rateSource = %q, want none", e.RateSource)
	}
	if e.Billing == nil {
		t.Fatal("billing is absent, want it present and empty for the entry's owner")
	}
	if e.Billing.BillRate != nil || e.Billing.Currency != nil {
		t.Errorf("billing = {%v %v}, want no rate and no currency", deref(e.Billing.BillRate), deref(e.Billing.Currency))
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestPostTimeEntries_MemberOnFixedLine_SnapshotsTheLineRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signInAs(t, h, projectKraftVerket, roleMember)

	e := createEntry(t, c, map[string]any{"billingLineId": lineFixed, "hours": 7.5, "note": "  Workshop med kunden  "})

	wantRate(t, e, 1500, "NOK", "line")
	if e.Id == 0 || e.UserId != userID || e.UserDisplayName == "" || e.UserDisplayName == "Unknown user" {
		t.Errorf("id/user = %d/%s/%q, want a new id owned by the caller and named", e.Id, e.UserId, e.UserDisplayName)
	}
	if e.ProjectId != projectKraftVerket || e.ProjectCode != projectKraftVerketCode || e.ProjectName != projectKraftVerketName {
		t.Errorf("project = %d/%q/%q, want 1001 named", e.ProjectId, e.ProjectCode, e.ProjectName)
	}
	if deref(e.BillingLineId) != int32(lineFixed) || deref(e.BillingLineCode) != "PM" || deref(e.TrackableCode) != "KVEM1000-PM" {
		t.Errorf("line = %v/%v/%v, want 3001/PM/KVEM1000-PM", deref(e.BillingLineId), deref(e.BillingLineCode), deref(e.TrackableCode))
	}
	if e.EntryDate != workDay || e.Hours != 7.5 || !e.Billable || deref(e.Note) != "Workshop med kunden" {
		t.Errorf("date/hours/billable/note = %s/%v/%v/%v, want %s/7.5/true/trimmed note", e.EntryDate, e.Hours, e.Billable, deref(e.Note), workDay)
	}
	if e.Status != "draft" || e.Revision != 1 {
		t.Errorf("status/revision = %s/%d, want draft/1", e.Status, e.Revision)
	}
	if want := (capabilitiesJSON{CanEdit: true, CanSubmit: true}); e.Capabilities != want {
		t.Errorf("capabilities = %+v, want %+v", e.Capabilities, want)
	}
	if e.Cost != nil {
		t.Errorf("cost = %+v, want it absent for a member", e.Cost)
	}
}

func TestPostTimeEntries_ListLine_PricesThroughTheCatalog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectKraftVerket, roleMember)

	wantRate(t, createEntry(t, c, map[string]any{"billingLineId": lineList}), 1600, "NOK", "line")
}

func TestPostTimeEntries_ListLineInEuro_PricesInTheProjectsCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectEuro, roleMember)

	wantRate(t, createEntry(t, c, map[string]any{"projectId": projectEuro, "billingLineId": lineEuroList}), 1400, "EUR", "line")
}

func TestPostTimeEntries_DiscountLine_AppliesTheDiscountToTheListPrice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectKraftVerket, roleMember)

	wantRate(t, createEntry(t, c, map[string]any{"billingLineId": lineDiscount}), 1440, "NOK", "line")
}

func TestPostTimeEntries_NoLine_UsesTheProjectDefaultRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectKraftVerket, roleMember)

	wantRate(t, createEntry(t, c, nil), 900, "NOK", "project")
}

func TestPostTimeEntries_NoProjectDefault_UsesThePersonRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signInAs(t, h, projectEuro, roleMember)
	seedRate(t, h, userID, "2026-01-01", 1100.0, 650.0, "EUR")

	wantRate(t, createEntry(t, c, map[string]any{"projectId": projectEuro}), 1100, "EUR", "person")
}

// One bill currency per project (D3): a card in another currency than the
// project's cannot price its hours — nothing is converted — so the chain
// ends at none rather than storing a NOK rate on a EUR project.
func TestPostTimeEntries_PersonCardInAnotherCurrency_ResolvesNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signInAs(t, h, projectEuro, roleMember)
	seedRate(t, h, userID, "2026-01-01", 1100.0, 650.0, "NOK")

	wantNoRate(t, createEntry(t, c, map[string]any{"projectId": projectEuro}))
}

// A project with no currency has none to hold a card to, so the person's
// rate prices it in the card's own currency.
func TestPostTimeEntries_ProjectWithoutCurrency_TakesThePersonCardsCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signInAs(t, h, projectNoCurrency, roleMember)
	seedRate(t, h, userID, "2026-01-01", 1100.0, 650.0, "SEK")

	wantRate(t, createEntry(t, c, map[string]any{"projectId": projectNoCurrency}), 1100, "SEK", "person")
}

// Billable defaults from the billing type: time and materials and fixed
// price are billable unless the caller says otherwise; non-billable never is.
func TestPostTimeEntries_FixedPriceProject_IsBillableByDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signInAs(t, h, projectFixedPrice, roleMember)
	seedRate(t, h, userID, "2026-01-01", 1100.0, nil, "NOK")

	e := createEntry(t, c, map[string]any{"projectId": projectFixedPrice})
	if !e.Billable {
		t.Error("billable = false, want a fixed-price project's entry billable by default")
	}
	wantRate(t, e, 1100, "NOK", "person")

	e = createEntry(t, c, map[string]any{"projectId": projectFixedPrice, "billable": false, "entryDate": "2026-09-15"})
	if e.Billable {
		t.Error("billable = true, want the caller's false kept on a fixed-price project")
	}
}

func TestPostTimeEntries_NoRateAnywhere_ResolvesNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectEuro, roleMember)

	wantNoRate(t, createEntry(t, c, map[string]any{"projectId": projectEuro}))
}

// With products off a list line has nothing to price it with, so the chain
// falls through past it — here, on a project with no default and a person
// with no rate card, all the way to none.
func TestPostTimeEntries_ProductsDisabledListLine_ResolvesNone(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProducts(t)
	c, _ := signInAs(t, h, projectEuro, roleMember)

	e := createEntry(t, c, map[string]any{"projectId": projectEuro, "billingLineId": lineEuroList})
	wantNoRate(t, e)
	if deref(e.BillingLineId) != int32(lineEuroList) {
		t.Errorf("billingLineId = %v, want the line kept even though it priced nothing", deref(e.BillingLineId))
	}
}

func TestPostTimeEntries_NonBillableProject_ForcesBillableFalseAndNoBillRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signInAs(t, h, projectInternal, roleMember)
	seedRate(t, h, userID, "2026-01-01", 1100.0, 650.0, "NOK")

	e := createEntry(t, c, map[string]any{"projectId": projectInternal, "billable": true})
	if e.Billable {
		t.Error("billable = true, want it forced false on a non-billable project")
	}
	wantNoRate(t, e)
}

func TestPostTimeEntries_BillableFalseOnABillableProject_ResolvesNoBillRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectKraftVerket, roleMember)

	e := createEntry(t, c, map[string]any{"billable": false, "billingLineId": lineFixed})
	if e.Billable {
		t.Error("billable = true, want the caller's false kept")
	}
	wantNoRate(t, e)
}

func TestPostTimeEntries_CallerWhoMayNotLogTime_IsRefusedOnProjectId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer, _ := signInAs(t, h, projectKraftVerket, roleViewer)
	outsider, _ := signIn(t, h)
	completed, _ := signInAs(t, h, projectCompleted, roleMember)

	for name, tc := range map[string]struct {
		c         *modtest.Client
		projectID int32
	}{
		"viewer":            {viewer, projectKraftVerket},
		"outsider":          {outsider, projectKraftVerket},
		"completed project": {completed, projectCompleted},
		"unknown project":   {outsider, projectUnknown},
	} {
		errs := fieldErrors(t, tc.c, entryBody(map[string]any{"projectId": tc.projectID}))
		if got := errs["projectId"]; len(got) != 1 || got[0] != "You cannot log time on this project" {
			t.Errorf("%s: projectId errors = %v, want exactly the one message", name, got)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM time.entries`); n != 0 {
		t.Errorf("entries = %d, want none created", n)
	}
}

func TestPostTimeEntries_LineOrTaskNotOnTheProject_IsRefusedOnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for name, tc := range map[string]struct {
		overrides map[string]any
		field     string
	}{
		"line on another project": {map[string]any{"billingLineId": lineEuroList}, "billingLineId"},
		"unknown line":            {map[string]any{"billingLineId": 3999}, "billingLineId"},
		"inactive line":           {map[string]any{"billingLineId": lineInactive}, "billingLineId"},
		"task on another project": {map[string]any{"taskId": taskForeign}, "taskId"},
		"unknown task":            {map[string]any{"taskId": 5999}, "taskId"},
	} {
		errs := fieldErrors(t, c, entryBody(tc.overrides))
		if len(errs[tc.field]) != 1 || len(errs) != 1 {
			t.Errorf("%s: errors = %v, want exactly one on %s", name, errs, tc.field)
		}
	}
}

func TestPostTimeEntries_HoursAndTimes_FollowD1(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for name, tc := range map[string]struct {
		overrides map[string]any
		field     string
	}{
		"zero hours":             {map[string]any{"hours": 0}, "hours"},
		"negative hours":         {map[string]any{"hours": -1}, "hours"},
		"more than 24 hours":     {map[string]any{"hours": 24.5}, "hours"},
		"three decimals":         {map[string]any{"hours": 1.255}, "hours"},
		"start/end mismatch":     {map[string]any{"hours": 3, "startTime": "08:00", "endTime": "10:00"}, "hours"},
		"crossing midnight":      {map[string]any{"hours": 4, "startTime": "22:00", "endTime": "02:00"}, "endTime"},
		"end equals start":       {map[string]any{"hours": 1, "startTime": "09:00", "endTime": "09:00"}, "endTime"},
		"only a start time":      {map[string]any{"startTime": "08:00"}, "endTime"},
		"only an end time":       {map[string]any{"endTime": "10:00"}, "startTime"},
		"malformed start time":   {map[string]any{"startTime": "8:00", "endTime": "10:00"}, "startTime"},
		"hour out of range":      {map[string]any{"startTime": "08:00", "endTime": "24:00"}, "endTime"},
		"note over 2000 letters": {map[string]any{"note": strings.Repeat("å", 2001)}, "note"},
		"no entry date":          {map[string]any{"entryDate": nil}, "entryDate"},
	} {
		errs := fieldErrors(t, c, entryBody(tc.overrides))
		if len(errs[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", name, errs, tc.field)
		}
	}

	// A derived duration is compared at two decimals: 08:00–08:20 is 0.33.
	e := createEntry(t, c, map[string]any{"hours": 0.33, "startTime": "08:00", "endTime": "08:20"})
	if deref(e.StartTime) != "08:00" || deref(e.EndTime) != "08:20" || e.Hours != 0.33 {
		t.Errorf("times/hours = %v/%v/%v, want 08:00/08:20/0.33", deref(e.StartTime), deref(e.EndTime), e.Hours)
	}
	e = createEntry(t, c, map[string]any{"hours": 24, "entryDate": "2026-09-15"})
	if e.Hours != 24 {
		t.Errorf("hours = %v, want exactly 24 accepted", e.Hours)
	}
}

func TestPostTimeEntries_DayTotalOver24_IsRefusedOnHours(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectKraftVerket, roleMember)
	other, _ := signInAs(t, h, projectKraftVerket, roleMember)

	createEntry(t, c, map[string]any{"hours": 23})
	errs := fieldErrors(t, c, entryBody(map[string]any{"hours": 1.5}))
	if len(errs["hours"]) != 1 {
		t.Errorf("errors = %v, want the day cap on hours", errs)
	}
	// Another day, and another person's same day, are not this day's total.
	createEntry(t, c, map[string]any{"hours": 1.5, "entryDate": "2026-09-15"})
	createEntry(t, other, map[string]any{"hours": 1.5})
	// Exactly 24 is allowed.
	createEntry(t, c, map[string]any{"hours": 1})
}

// Two creates racing for one day's last hours must not both pass the cap: the
// sum and the insert happen under a lock on (person, day), so whichever
// commits second sees the first. Several rounds, so a missing lock would not
// pass by luck of scheduling.
func TestPostTimeEntries_RacingCreatesOverTheCap_OnlyOneWins(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signInAs(t, h, projectKraftVerket, roleMember)

	for round := range 5 {
		date := fmt.Sprintf("2026-09-%02d", 14+round)
		var wg sync.WaitGroup
		start := make(chan struct{})
		statuses := make([]int, 2)
		for i := range statuses {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				statuses[i] = c.Do(http.MethodPost, entriesPath, entryBody(map[string]any{"hours": 13, "entryDate": date})).Status
			}()
		}
		close(start)
		wg.Wait()

		created, refused := 0, 0
		for _, s := range statuses {
			switch s {
			case http.StatusCreated:
				created++
			case http.StatusBadRequest:
				refused++
			}
		}
		if created != 1 || refused != 1 {
			t.Errorf("%s: statuses %v, want one 201 and one 400", date, statuses)
		}
		if n := h.Count(t, `SELECT count(*) FROM time.entries WHERE user_id = $1 AND entry_date = $2::date`, userID, date); n != 1 {
			t.Errorf("%s: %d entries stored, want 1", date, n)
		}
	}
}

func TestPostTimeEntries_TaskTitle_IsSnapshottedAndOutlivesTheTask(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signInAs(t, h, projectKraftVerket, roleMember)

	e := createEntry(t, c, map[string]any{"taskId": taskSpecification})
	if deref(e.TaskId) != int32(taskSpecification) || deref(e.TaskTitle) != taskSpecificationTitle {
		t.Errorf("task = %v/%v, want 5001 %q", deref(e.TaskId), deref(e.TaskTitle), taskSpecificationTitle)
	}

	h.projects.removeTask(taskSpecification)
	got := getEntry(t, c, e.Id)
	if deref(got.TaskId) != int32(taskSpecification) || deref(got.TaskTitle) != taskSpecificationTitle {
		t.Errorf("after the task is deleted: task = %v/%v, want the snapshot kept", deref(got.TaskId), deref(got.TaskTitle))
	}
}

func TestPostTimeEntries_BeforeTheLock_IsRefusedUnlessTimeManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setLock(t, h, "2026-09-10")
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")

	errs := fieldErrors(t, owner, entryBody(map[string]any{"entryDate": "2026-09-09"}))
	if len(errs["entryDate"]) != 1 {
		t.Errorf("errors = %v, want the lock on entryDate", errs)
	}
	// The lock date itself is open: entries *before* it are closed.
	createEntry(t, owner, map[string]any{"entryDate": "2026-09-10"})

	e := createEntry(t, manager, map[string]any{"entryDate": "2026-09-09"})
	if !e.Capabilities.CanEdit {
		t.Error("canEdit = false, want time:manage not held back by the lock")
	}
}

func TestPostTimeEntries_WithoutTimeAccess_IsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := h.SignInUser(t)
	h.projects.addRole(projectKraftVerket, userID, roleMember)

	if r := c.Do(http.MethodPost, entriesPath, entryBody(nil)); r.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403 without time:access", r.Status)
	}
}

func TestGetTimeEntriesById_Visibility(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	colleague, _ := signInAs(t, h, projectKraftVerket, roleMember)
	viewer, _ := signInAs(t, h, projectKraftVerket, roleViewer)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	viewAll, _ := signIn(t, h, "time:view-all")
	outsider, _ := signIn(t, h, "projects:view-financials", "projects:view-all")

	e := createEntry(t, owner, map[string]any{"billingLineId": lineFixed})

	// The owner sees their own bill rate, never the cost.
	raw := rawEntry(t, owner, e.Id)
	if _, ok := raw["billing"]; !ok {
		t.Error("owner: billing absent, want it present")
	}
	if _, ok := raw["cost"]; ok {
		t.Errorf("owner: cost = %v, want the key absent", raw["cost"])
	}

	// Another member, a viewer and an outsider see a bare 404, identical to
	// an unknown id's.
	unknown := owner.Do(http.MethodGet, entryPath(e.Id+1000), nil)
	for name, c := range map[string]*modtest.Client{"colleague": colleague, "viewer": viewer, "outsider": outsider} {
		r := c.Do(http.MethodGet, entryPath(e.Id), nil)
		if r.Status != http.StatusNotFound || string(r.Body) != string(unknown.Body) || unknown.Status != http.StatusNotFound {
			t.Errorf("%s: %d %q, want the unknown id's bare 404 (%d %q)", name, r.Status, r.Body, unknown.Status, unknown.Body)
		}
	}

	// The project's manager sees every entry on it, with the bill rate.
	got := getEntry(t, manager, e.Id)
	wantRate(t, got, 1500, "NOK", "line")
	if got.Cost != nil {
		t.Errorf("manager: cost = %+v, want it absent without time:manage or time:view-all", got.Cost)
	}
	if got.Capabilities.CanEdit || got.Capabilities.CanSubmit {
		t.Errorf("manager: capabilities = %+v, want no edit or submit on someone else's entry", got.Capabilities)
	}

	// time:view-all sees everyone's entries, with the cost and without the
	// bill rate (it grants no project financials).
	raw = rawEntry(t, viewAll, e.Id)
	if _, ok := raw["billing"]; ok {
		t.Errorf("view-all: billing = %v, want the key absent", raw["billing"])
	}
	cost, ok := raw["cost"].(map[string]any)
	if !ok {
		t.Fatalf("view-all: cost = %v, want an object", raw["cost"])
	}
	if len(cost) != 0 {
		t.Errorf("view-all: cost = %v, want it present and empty for a person with no cost rate", cost)
	}
}

// time:approve and time:manage see every entry too: an approver has to see
// what they approve, and time:manage what it unapproves. Neither grants the
// bill rate on its own (D8); time:manage carries the cost rate.
func TestGetTimeEntriesById_TimeApproveAndTimeManage_SeeEveryEntry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	approver, _ := signIn(t, h, "time:approve")
	manager, _ := signIn(t, h, "time:manage")
	member, _ := signInAs(t, h, projectKraftVerket, roleMember, "projects:manage-all")

	e := createEntry(t, owner, map[string]any{"billingLineId": lineFixed})

	got := getEntry(t, approver, e.Id)
	if got.Billing != nil || got.Cost != nil {
		t.Errorf("approver: billing/cost = %+v/%+v, want both absent", got.Billing, got.Cost)
	}
	if got.Capabilities.CanEdit || got.Capabilities.CanSubmit || got.Capabilities.CanApprove {
		t.Errorf("approver: capabilities = %+v, want nothing to do with a draft", got.Capabilities)
	}
	got = getEntry(t, manager, e.Id)
	if got.Billing != nil || got.Cost == nil {
		t.Errorf("time:manage: billing/cost = %+v/%+v, want no billing and the cost block", got.Billing, got.Cost)
	}
	// Another member stays out, whatever projects grants them: seeing a
	// project's money is not seeing its people's time.
	if r := member.Do(http.MethodGet, entryPath(e.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("member with projects:manage-all: %d, want 404", r.Status)
	}
}

func TestGetTimeEntriesById_CostRate_IsSnapshottedForTimeViewAll(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, userID := signInAs(t, h, projectKraftVerket, roleMember)
	seedRate(t, h, userID, "2026-01-01", 1100.0, 650.0, "NOK")
	viewAll, _ := signIn(t, h, "time:view-all")

	e := createEntry(t, owner, nil)
	got := getEntry(t, viewAll, e.Id)
	if got.Cost == nil || deref(got.Cost.CostRate) != 650.0 || deref(got.Cost.Currency) != "NOK" {
		t.Errorf("cost = %+v, want 650 NOK", got.Cost)
	}
	// Changing the card afterwards does not move a snapshot.
	h.Exec(t, `UPDATE time.person_rates SET cost_rate = 999 WHERE user_id = $1`, userID)
	if got := getEntry(t, viewAll, e.Id); got.Cost == nil || deref(got.Cost.CostRate) != 650.0 {
		t.Errorf("cost after the card changed = %+v, want the snapshot 650", got.Cost)
	}
}

func TestDeleteTimeEntriesById_OwnersDraft_IsDeleted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	e := createEntry(t, owner, nil)

	if r := owner.Do(http.MethodDelete, entryPath(e.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s, want 204", r.Status, r.Body)
	}
	if r := owner.Do(http.MethodGet, entryPath(e.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("get after delete: %d, want 404", r.Status)
	}
	if r := owner.Do(http.MethodDelete, entryPath(e.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("second delete: %d, want 404", r.Status)
	}
}

func TestDeleteTimeEntriesById_RejectedEntry_IsDeleted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	e := createEntry(t, owner, nil)
	setStatus(t, h, e.Id, "rejected")

	if r := owner.Do(http.MethodDelete, entryPath(e.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("delete rejected: %d %s, want 204", r.Status, r.Body)
	}
}

func TestDeleteTimeEntriesById_NotTheOwnersToDelete_IsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	colleague, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	submitted := createEntry(t, owner, nil)
	setStatus(t, h, submitted.Id, "submitted")
	if r := owner.Do(http.MethodDelete, entryPath(submitted.Id), nil); r.Status != http.StatusForbidden || r.Code() != "forbidden" {
		t.Errorf("owner deleting a submitted entry: %d %s, want the access layer's 403", r.Status, r.Body)
	}

	draft := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15"})
	if r := manager.Do(http.MethodDelete, entryPath(draft.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("manager deleting a member's draft: %d, want 403 (they may see it, not delete it)", r.Status)
	}
	if r := colleague.Do(http.MethodDelete, entryPath(draft.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("colleague deleting a member's draft: %d, want 404 (they may not see it)", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM time.entries`); n != 2 {
		t.Errorf("entries = %d, want both kept", n)
	}
}

func TestDeleteTimeEntriesById_DraftBeforeTheLock_IsRefusedUnlessTimeManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")
	locked := createEntry(t, owner, map[string]any{"entryDate": "2026-09-09"})
	own := createEntry(t, manager, map[string]any{"entryDate": "2026-09-09"})
	setLock(t, h, "2026-09-10")

	if r := owner.Do(http.MethodDelete, entryPath(locked.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("owner deleting a locked draft: %d, want 403", r.Status)
	}
	if got := getEntry(t, owner, locked.Id); got.Capabilities.CanEdit {
		t.Error("canEdit = true on a locked draft, want false")
	}
	if r := manager.Do(http.MethodDelete, entryPath(own.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("time:manage deleting their own locked draft: %d, want 204", r.Status)
	}
}
