package timetracking_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// validationErrors decodes a 400 validation problem and fails the test
// unless r is one titled title, answering its field errors.
func validationErrors(t *testing.T, r *modtest.Response, title string) map[string][]string {
	t.Helper()
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if problem.Title != title {
		t.Errorf("problem title = %q, want %q", problem.Title, title)
	}
	return problem.Errors
}

func TestPutTimeEntriesById_Draft_ReResolvesTheRates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	viewAll, _ := signIn(t, h, "time:view-all")

	e := createEntry(t, owner, nil)
	wantRate(t, e, 900, "NOK", "project")

	// A line now prices the entry, and a rate card that did not exist at the
	// create is in effect at the update: both are resolved again.
	seedRate(t, h, ownerID, "2026-01-01", 1100.0, 650.0, "NOK")
	updated := updateEntry(t, owner, e, map[string]any{"billingLineId": lineFixed, "hours": 3.25, "note": "Ny tekst"})
	wantRate(t, updated, 1500, "NOK", "line")
	if updated.Hours != 3.25 || deref(updated.Note) != "Ny tekst" || deref(updated.TrackableCode) != "KVEM1000-PM" {
		t.Errorf("hours/note/trackable = %v/%v/%v, want 3.25/Ny tekst/KVEM1000-PM", updated.Hours, deref(updated.Note), deref(updated.TrackableCode))
	}
	if updated.Revision != 2 || updated.Status != "draft" || updated.Id != e.Id {
		t.Errorf("id/revision/status = %d/%d/%s, want %d/2/draft", updated.Id, updated.Revision, updated.Status, e.Id)
	}
	if got := getEntry(t, viewAll, e.Id); got.Cost == nil || deref(got.Cost.CostRate) != 650.0 {
		t.Errorf("cost after the update = %+v, want the card's 650 resolved at the save", got.Cost)
	}

	// Moving it to another project, without a line, falls to the person.
	h.projects.addRole(projectEuro, ownerID, roleMember)
	h.Exec(t, `UPDATE time.person_rates SET currency = 'EUR' WHERE user_id = $1`, ownerID)
	moved := updateEntry(t, owner, updated, map[string]any{"projectId": projectEuro, "billingLineId": nil})
	wantRate(t, moved, 1100, "EUR", "person")
	if moved.ProjectCode != "EURO2026" || moved.BillingLineId != nil || moved.TrackableCode != nil {
		t.Errorf("project/line = %s/%v/%v, want EURO2026 and no line", moved.ProjectCode, deref(moved.BillingLineId), deref(moved.TrackableCode))
	}

	// Not billable any more: no bill rate at all.
	wantNoRate(t, updateEntry(t, owner, moved, map[string]any{"billable": false}))
}

func TestPutTimeEntriesById_StaleRevision_IsAConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	e := createEntry(t, owner, nil)
	updateEntry(t, owner, e, map[string]any{"hours": 3})

	r := owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"hours": 5}))
	if r.Status != http.StatusConflict {
		t.Fatalf("stale update: %d %s, want 409", r.Status, r.Body)
	}
	var problem struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	r.JSON(&problem)
	if problem.Title != "Time entry revision conflict" || problem.Detail != "The entry has revision 2; the supplied revision was 1." {
		t.Errorf("problem = %+v, want the entry's revision conflict naming both revisions", problem)
	}
	if got := getEntry(t, owner, e.Id); got.Hours != 3 || got.Revision != 2 {
		t.Errorf("after the conflict: hours/revision = %v/%d, want the first update's 3/2 kept", got.Hours, got.Revision)
	}
}

func TestPutTimeEntriesById_PastDraftOrRejected_IsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	submitted := createEntry(t, owner, nil)
	submitted = submitEntries(t, owner, submitted.Id)[0]
	if r := owner.Do(http.MethodPut, entryPath(submitted.Id), updateBody(submitted, map[string]any{"hours": 3})); r.Status != http.StatusForbidden || r.Code() != "forbidden" {
		t.Errorf("update after submit: %d %s, want the access layer's 403", r.Status, r.Body)
	}
	for _, status := range []string{"approved", "invoiced"} {
		e := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15"})
		setStatus(t, h, e.Id, status)
		if r := owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"hours": 3})); r.Status != http.StatusForbidden {
			t.Errorf("update of a %s entry: %d, want 403", status, r.Status)
		}
	}
	if got := getEntry(t, owner, submitted.Id); got.Hours != 2 || got.Status != "submitted" {
		t.Errorf("submitted entry = %v h %s, want it untouched", got.Hours, got.Status)
	}
}

func TestPutTimeEntriesById_RejectedEntry_ReturnsToDraftWithTheReasonCleared(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	e := createEntry(t, owner, nil)
	e = submitEntries(t, owner, e.Id)[0]
	h.Exec(t, `UPDATE time.entries SET status = 'rejected', rejection_reason = 'Feil prosjekt' WHERE id = $1`, e.Id)
	e = getEntry(t, owner, e.Id)
	if e.Status != "rejected" || deref(e.RejectionReason) != "Feil prosjekt" || !e.Capabilities.CanEdit || e.Capabilities.CanSubmit {
		t.Fatalf("rejected entry = %s/%v/%+v, want rejected with its reason, editable, not submittable", e.Status, deref(e.RejectionReason), e.Capabilities)
	}

	updated := updateEntry(t, owner, e, map[string]any{"hours": 1.5})
	if updated.Status != "draft" || updated.RejectionReason != nil {
		t.Errorf("status/reason = %s/%v, want draft with the reason cleared", updated.Status, deref(updated.RejectionReason))
	}
	if !updated.Capabilities.CanSubmit || updated.Hours != 1.5 {
		t.Errorf("canSubmit/hours = %v/%v, want a submittable draft of 1.5 hours", updated.Capabilities.CanSubmit, updated.Hours)
	}
	if n := h.Count(t, `SELECT count(*) FROM time.entries WHERE id = $1 AND submitted_at IS NULL`, e.Id); n != 1 {
		t.Error("submitted_at is still set, want it cleared with the return to draft")
	}
}

func TestPutTimeEntriesById_NotTheOwners_IsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	colleague, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	approver, _ := signIn(t, h, "time:approve", "time:manage")

	e := createEntry(t, owner, nil)
	body := updateBody(e, map[string]any{"hours": 3})

	for name, c := range map[string]*modtest.Client{"manager": manager, "approver": approver} {
		if r := c.Do(http.MethodPut, entryPath(e.Id), body); r.Status != http.StatusForbidden {
			t.Errorf("%s: %d, want 403 (they may see it, not change it)", name, r.Status)
		}
	}
	if r := colleague.Do(http.MethodPut, entryPath(e.Id), body); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("colleague: %d %q, want a bare 404", r.Status, r.Body)
	}
	if r := owner.Do(http.MethodPut, entryPath(e.Id+1000), body); r.Status != http.StatusNotFound {
		t.Errorf("unknown id: %d, want 404", r.Status)
	}
	if got := getEntry(t, owner, e.Id); got.Hours != 2 || got.Revision != 1 {
		t.Errorf("entry = %v h rev %d, want it untouched", got.Hours, got.Revision)
	}
}

func TestPutTimeEntriesById_InvalidBody_IsRefusedOnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	e := createEntry(t, owner, nil)

	for name, tc := range map[string]struct {
		overrides map[string]any
		field     string
	}{
		"zero hours":              {map[string]any{"hours": 0}, "hours"},
		"start/end mismatch":      {map[string]any{"hours": 3, "startTime": "08:00", "endTime": "10:00"}, "hours"},
		"project not loggable":    {map[string]any{"projectId": projectCompleted}, "projectId"},
		"line on another project": {map[string]any{"billingLineId": lineEuroList}, "billingLineId"},
		"no entry date":           {map[string]any{"entryDate": nil}, "entryDate"},
	} {
		r := owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, tc.overrides))
		if errs := validationErrors(t, r, "Invalid time entry"); len(errs[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", name, errs, tc.field)
		}
	}
	if got := getEntry(t, owner, e.Id); got.Revision != 1 {
		t.Errorf("revision = %d, want the entry untouched by refused updates", got.Revision)
	}
}

func TestPutTimeEntriesById_DayCap_CountsEveryEntryButTheOneSaved(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	e := createEntry(t, owner, map[string]any{"hours": 20})
	// The entry's own 20 hours are not counted against its new 24.
	e = updateEntry(t, owner, e, map[string]any{"hours": 24})

	other := createEntry(t, owner, map[string]any{"hours": 20, "entryDate": "2026-09-15"})
	e = updateEntry(t, owner, e, map[string]any{"hours": 4})
	// Moving it onto a day that already holds 20 hours: 20 + 5 > 24.
	r := owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"hours": 5, "entryDate": "2026-09-15"}))
	if errs := validationErrors(t, r, "Invalid time entry"); len(errs["hours"]) != 1 {
		t.Errorf("errors = %v, want the day cap on hours", errs)
	}
	updateEntry(t, owner, e, map[string]any{"hours": 4, "entryDate": "2026-09-15"})
	if got := getEntry(t, owner, other.Id); got.Hours != 20 {
		t.Errorf("other entry = %v h, want 20", got.Hours)
	}
}

func TestPutTimeEntriesById_TheLock_HoldsBackEveryoneButTimeManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")

	locked := createEntry(t, owner, map[string]any{"entryDate": "2026-09-09"})
	open := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14"})
	own := createEntry(t, manager, map[string]any{"entryDate": "2026-09-09"})
	setLock(t, h, "2026-09-10")

	if r := owner.Do(http.MethodPut, entryPath(locked.Id), updateBody(locked, map[string]any{"hours": 3})); r.Status != http.StatusForbidden {
		t.Errorf("update of a locked entry: %d, want 403", r.Status)
	}
	r := owner.Do(http.MethodPut, entryPath(open.Id), updateBody(open, map[string]any{"entryDate": "2026-09-09"}))
	if errs := validationErrors(t, r, "Invalid time entry"); len(errs["entryDate"]) != 1 {
		t.Errorf("moving an entry before the lock: errors = %v, want the lock on entryDate", errs)
	}
	if got := updateEntry(t, manager, own, map[string]any{"hours": 3, "entryDate": "2026-09-08"}); got.Hours != 3 {
		t.Errorf("time:manage past the lock: hours = %v, want 3", got.Hours)
	}
}
