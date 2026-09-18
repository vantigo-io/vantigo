package timetracking_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// wantIDErrors fails the test unless errs holds exactly one message per id in
// want on ids, each naming its id and carrying its fragment.
func wantIDErrors(t *testing.T, errs map[string][]string, want map[int64]string) {
	t.Helper()
	if len(errs["ids"]) != len(want) {
		t.Errorf("ids errors = %v, want one per offending id (%d)", errs["ids"], len(want))
	}
	for id, fragment := range want {
		if !slices.ContainsFunc(errs["ids"], func(m string) bool {
			return strings.Contains(m, fmt.Sprintf("Entry %d ", id)) && strings.Contains(m, fragment)
		}) {
			t.Errorf("ids errors = %v, want entry %d named with %q", errs["ids"], id, fragment)
		}
	}
}

// batchErrorsOf posts body to one of the approval operations' path and
// answers the field errors of the 400 it must be refused with.
func batchErrorsOf(t *testing.T, c *modtest.Client, path string, body map[string]any) map[string][]string {
	t.Helper()
	return validationErrors(t, c.Do(http.MethodPost, path, body), "Invalid approval")
}

func TestPostTimeEntriesApprove_ByTheProjectManager_RecordsTheApproverAndTheTime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager)
	setDisplayName(t, h, managerID, "Mona Manager")
	e := submittedEntry(t, owner, nil)
	if !getEntry(t, manager, e.Id).Capabilities.CanApprove {
		t.Fatalf("manager: canApprove = false on a submitted entry of their project")
	}

	got := approveEntries(t, manager, e.Id)
	if len(got) != 1 {
		t.Fatalf("approved %d entries, want 1", len(got))
	}
	a := got[0]
	if a.Status != "approved" || a.Revision != 3 {
		t.Errorf("entry = %s rev %d, want approved rev 3", a.Status, a.Revision)
	}
	if a.ApprovedBy == nil || a.ApprovedBy.UserId != managerID || a.ApprovedBy.DisplayName != "Mona Manager" {
		t.Errorf("approvedBy = %+v, want the manager by name", a.ApprovedBy)
	}
	if a.ApprovedAt == nil || !a.ApprovedAt.Equal(h.Now()) {
		t.Errorf("approvedAt = %v, want %v", a.ApprovedAt, h.Now())
	}
	if a.Capabilities.CanApprove || !a.Capabilities.CanUnapprove {
		t.Errorf("manager's capabilities = %+v, want unapprove and no approve", a.Capabilities)
	}

	o := getEntry(t, owner, e.Id)
	if o.Status != "approved" || o.Capabilities.CanEdit || o.Capabilities.CanUnapprove {
		t.Errorf("owner's copy = %s %+v, want approved and nothing to do", o.Status, o.Capabilities)
	}
}

func TestPostTimeEntriesApprove_TimeApprove_ApprovesOnAnyProjectInTheGivenOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, ownerID, roleMember)
	approver, approverID := signIn(t, h, "time:approve")

	first := submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-14"})
	second := submittedEntry(t, owner, map[string]any{"projectId": projectEuro, "entryDate": "2026-09-15"})

	got := approveEntries(t, approver, second.Id, first.Id, second.Id)
	if !slices.Equal(entryIDs(got...), []int64{second.Id, first.Id}) {
		t.Errorf("ids = %v, want [%d %d] (given order, duplicates once)", entryIDs(got...), second.Id, first.Id)
	}
	for _, e := range got {
		if e.Status != "approved" || e.ApprovedBy == nil || e.ApprovedBy.UserId != approverID {
			t.Errorf("entry %d = %s by %+v, want approved by the time:approve caller", e.Id, e.Status, e.ApprovedBy)
		}
	}
}

func TestApprovalOperations_CallerWhoApprovesNothing_IsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	e := submittedEntry(t, member, nil)
	manage, _ := signIn(t, h, "time:manage")

	for _, tc := range []struct {
		name   string
		caller *modtest.Client
		path   string
		body   map[string]any
	}{
		{"member approves their own", member, approvePath, map[string]any{"ids": []int64{e.Id}}},
		{"member rejects their own", member, rejectPath, map[string]any{"ids": []int64{e.Id}, "reason": "Nei"}},
		{"member unapproves their own", member, unapprovePath, map[string]any{"ids": []int64{e.Id}}},
		{"time:manage approves", manage, approvePath, map[string]any{"ids": []int64{e.Id}}},
		{"time:manage rejects", manage, rejectPath, map[string]any{"ids": []int64{e.Id}, "reason": "Nei"}},
	} {
		if r := tc.caller.Do(http.MethodPost, tc.path, tc.body); r.Status != http.StatusForbidden {
			t.Errorf("%s: status %d body %s, want 403", tc.name, r.Status, r.Body)
		}
	}
	if got := getEntry(t, member, e.Id); got.Status != "submitted" {
		t.Errorf("entry: %s, want still submitted", got.Status)
	}
}

func TestPostTimeEntriesApprove_AnyRefusal_RefusesThemAllNamingEachId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, ownerID, roleMember)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager)
	h.projects.addRole(projectEuro, managerID, roleMember)

	ok := submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-14"})
	draft := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15"})
	approved := submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-16"})
	approveEntries(t, manager, approved.Id)
	invoiced := submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-17"})
	setStatus(t, h, invoiced.Id, "invoiced")
	elsewhere := submittedEntry(t, owner, map[string]any{"projectId": projectEuro})
	managersOwn := submittedEntry(t, manager, map[string]any{"projectId": projectEuro})
	locked := submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-09"})
	setLock(t, h, "2026-09-10")
	unknown := ok.Id + 1000

	errs := batchErrorsOf(t, manager, approvePath, map[string]any{"ids": []int64{
		ok.Id, draft.Id, approved.Id, invoiced.Id, elsewhere.Id, managersOwn.Id, locked.Id, unknown,
	}})
	wantIDErrors(t, errs, map[int64]string{
		draft.Id:       "is not submitted",
		approved.Id:    "is not submitted",
		invoiced.Id:    "is invoiced",
		elsewhere.Id:   "was not found",
		managersOwn.Id: "is not on a project you approve for",
		locked.Id:      "before 2026-09-10",
		unknown:        "was not found",
	})
	if got := getEntry(t, owner, ok.Id); got.Status != "submitted" {
		t.Errorf("the approvable entry: %s, want nothing approved", got.Status)
	}

	errs = batchErrorsOf(t, manager, approvePath, map[string]any{"ids": []int64{}})
	if len(errs["ids"]) != 1 {
		t.Errorf("no ids: errors = %v, want one on ids", errs)
	}
}

func TestPostTimeEntriesReject_RequiresAReasonOfAtMost1000Characters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	e := submittedEntry(t, owner, nil)

	for name, body := range map[string]map[string]any{
		"absent":   {"ids": []int64{e.Id}},
		"blank":    {"ids": []int64{e.Id}, "reason": "   "},
		"too long": {"ids": []int64{e.Id}, "reason": strings.Repeat("x", 1001)},
	} {
		errs := batchErrorsOf(t, manager, rejectPath, body)
		if len(errs["reason"]) != 1 {
			t.Errorf("%s: errors = %v, want one on reason", name, errs)
		}
	}
	if got := getEntry(t, owner, e.Id); got.Status != "submitted" {
		t.Errorf("entry: %s, want still submitted", got.Status)
	}

	longest := strings.Repeat("x", 1000)
	got := rejectEntries(t, manager, "  "+longest+"  ", e.Id)
	if got[0].Status != "rejected" || deref(got[0].RejectionReason) != longest {
		t.Errorf("entry = %s reason %q, want rejected with the 1000 characters trimmed", got[0].Status, deref(got[0].RejectionReason))
	}
}

func TestPostTimeEntriesReject_TheOwnerSeesTheReasonInTheirWeekAndMayEditAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	approver, _ := signIn(t, h, "time:approve")
	e := createEntry(t, owner, nil)
	submitWeek(t, owner, workDay)

	got := rejectEntries(t, approver, "Feil prosjekt", e.Id)
	if got[0].Status != "rejected" || got[0].ApprovedBy != nil || got[0].ApprovedAt != nil {
		t.Errorf("entry = %s approved by %+v at %v, want rejected and no approver", got[0].Status, got[0].ApprovedBy, got[0].ApprovedAt)
	}

	week := getWeek(t, owner, workDay)
	if len(week.Rows) != 1 || len(week.Rows[0].Days[0].Entries) != 1 {
		t.Fatalf("week rows = %+v, want the one entry on Monday", week.Rows)
	}
	w := week.Rows[0].Days[0].Entries[0]
	if w.Status != "rejected" || deref(w.RejectionReason) != "Feil prosjekt" {
		t.Errorf("week entry = %s reason %v, want rejected with the reason", w.Status, deref(w.RejectionReason))
	}
	if !w.Capabilities.CanEdit || w.Capabilities.CanSubmit {
		t.Errorf("owner's capabilities = %+v, want edit and no submit", w.Capabilities)
	}
	if edited := updateEntry(t, owner, w, map[string]any{"hours": 3}); edited.Status != "draft" || edited.RejectionReason != nil {
		t.Errorf("edited = %s reason %v, want a draft with the reason cleared", edited.Status, deref(edited.RejectionReason))
	}
}

func TestPostTimeEntriesUnapprove_ByTheApprover_ReturnsAFreshDraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	e := createEntry(t, owner, nil)
	submitWeek(t, owner, workDay)
	approveEntries(t, manager, e.Id)

	got := unapproveEntries(t, manager, e.Id)
	u := got[0]
	if u.Status != "draft" || u.Revision != 4 {
		t.Errorf("entry = %s rev %d, want draft rev 4", u.Status, u.Revision)
	}
	if u.ApprovedBy != nil || u.ApprovedAt != nil || u.SubmittedAt != nil {
		t.Errorf("approvedBy %+v approvedAt %v submittedAt %v, want all cleared", u.ApprovedBy, u.ApprovedAt, u.SubmittedAt)
	}
	if u.Capabilities.CanUnapprove || u.Capabilities.CanApprove {
		t.Errorf("manager's capabilities = %+v, want neither approve nor unapprove", u.Capabilities)
	}

	o := getEntry(t, owner, e.Id)
	if !o.Capabilities.CanEdit || !o.Capabilities.CanSubmit {
		t.Errorf("owner's capabilities = %+v, want edit and submit", o.Capabilities)
	}
	if week := getWeek(t, owner, workDay); !week.HasUnsubmittedChanges {
		t.Errorf("week hasUnsubmittedChanges = false, want true: the submitted week holds a draft again")
	}

	errs := batchErrorsOf(t, manager, unapprovePath, map[string]any{"ids": []int64{e.Id}})
	wantIDErrors(t, errs, map[int64]string{e.Id: "is not approved"})
}

func TestPostTimeEntriesUnapprove_TimeManage_UnapprovesOnAnyProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	approver, _ := signIn(t, h, "time:approve")
	manage, _ := signIn(t, h, "time:manage")
	e := submittedEntry(t, owner, nil)
	approveEntries(t, approver, e.Id)

	if !getEntry(t, manage, e.Id).Capabilities.CanUnapprove {
		t.Errorf("time:manage: canUnapprove = false on an approved entry")
	}
	if got := unapproveEntries(t, manage, e.Id); got[0].Status != "draft" {
		t.Errorf("entry = %s, want draft", got[0].Status)
	}
}

func TestApprovalOperations_InvoicedEntry_RefusesEveryTransition(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	everything, _ := signInAs(t, h, projectKraftVerket, roleManager, "time:approve", "time:manage")
	e := submittedEntry(t, owner, nil)
	approveEntries(t, everything, e.Id)
	setStatus(t, h, e.Id, "invoiced")

	for _, path := range []string{approvePath, rejectPath, unapprovePath} {
		errs := batchErrorsOf(t, everything, path, map[string]any{"ids": []int64{e.Id}, "reason": "Nei"})
		wantIDErrors(t, errs, map[int64]string{e.Id: "is invoiced"})
	}
	errs := validationErrors(t, owner.Do(http.MethodPost, submitPath, map[string]any{"ids": []int64{e.Id}}), "Invalid submission")
	wantIDErrors(t, errs, map[int64]string{e.Id: "is not a draft"})
	e = getEntry(t, owner, e.Id)
	if r := owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"hours": 3})); r.Status != http.StatusForbidden {
		t.Errorf("edit: status %d, want 403", r.Status)
	}
	if r := owner.Do(http.MethodDelete, entryPath(e.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("delete: status %d, want 403", r.Status)
	}
	got := getEntry(t, everything, e.Id)
	if got.Status != "invoiced" || got.Capabilities != (capabilitiesJSON{}) {
		t.Errorf("entry = %s %+v, want invoiced with no capability for anyone", got.Status, got.Capabilities)
	}
}

func TestApprovalOperations_TheLock_HoldsBackEveryoneButTimeManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	managerWithManage, _ := signInAs(t, h, projectKraftVerket, roleManager, "time:manage")

	toApprove := submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-08"})
	toReject := submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-09"})
	approved := submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-07"})
	approveEntries(t, manager, approved.Id)
	putSettings(t, managerWithManage, map[string]any{"lockedBefore": "2026-09-10"})

	if c := getEntry(t, manager, toApprove.Id).Capabilities; c.CanApprove {
		t.Errorf("manager past the lock: canApprove = true")
	}
	if c := getEntry(t, manager, approved.Id).Capabilities; c.CanUnapprove {
		t.Errorf("manager past the lock: canUnapprove = true")
	}
	for path, id := range map[string]int64{approvePath: toApprove.Id, rejectPath: toReject.Id, unapprovePath: approved.Id} {
		errs := batchErrorsOf(t, manager, path, map[string]any{"ids": []int64{id}, "reason": "Nei"})
		wantIDErrors(t, errs, map[int64]string{id: "before 2026-09-10"})
	}

	if got := approveEntries(t, managerWithManage, toApprove.Id); got[0].Status != "approved" {
		t.Errorf("time:manage approve: %s, want approved", got[0].Status)
	}
	if got := rejectEntries(t, managerWithManage, "For sent", toReject.Id); got[0].Status != "rejected" {
		t.Errorf("time:manage reject: %s, want rejected", got[0].Status)
	}
	if got := unapproveEntries(t, managerWithManage, approved.Id); got[0].Status != "draft" {
		t.Errorf("time:manage unapprove: %s, want draft", got[0].Status)
	}

	// Moved, the lock holds back what it now covers; lifted, nothing.
	another := submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-11"})
	putSettings(t, managerWithManage, map[string]any{"lockedBefore": "2026-09-12"})
	errs := batchErrorsOf(t, manager, approvePath, map[string]any{"ids": []int64{another.Id}})
	wantIDErrors(t, errs, map[int64]string{another.Id: "before 2026-09-12"})
	putSettings(t, managerWithManage, map[string]any{"lockedBefore": nil})
	if got := approveEntries(t, manager, another.Id); got[0].Status != "approved" {
		t.Errorf("after the lock is lifted: %s, want approved", got[0].Status)
	}
}
