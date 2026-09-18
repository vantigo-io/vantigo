package timetracking_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// The week every test here works in: Monday 14 to Sunday 20 September 2026.
const (
	thisWeek = "2026-09-14"
	nextWeek = "2026-09-21"
)

func TestGetTimeWeeksByWeekStart_GroupsTheCallersEntriesIntoRows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, ownerID, roleMember)
	colleague, _ := signInAs(t, h, projectKraftVerket, roleMember)

	plainMon := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14", "hours": 2})
	plainMonLater := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14", "hours": 0.5, "startTime": "13:00", "endTime": "13:30"})
	plainMonEarly := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14", "hours": 1, "startTime": "08:00", "endTime": "09:00"})
	plainTue := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15", "hours": 3})
	lineMon := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14", "hours": 1.5, "billingLineId": lineFixed})
	taskWed := createEntry(t, owner, map[string]any{"entryDate": "2026-09-16", "hours": 4, "billingLineId": lineFixed, "taskId": taskSpecification})
	euroFri := createEntry(t, owner, map[string]any{"entryDate": "2026-09-18", "hours": 1, "projectId": projectEuro})
	createEntry(t, owner, map[string]any{"entryDate": nextWeek})
	createEntry(t, owner, map[string]any{"entryDate": "2026-09-13"})
	createEntry(t, colleague, map[string]any{"entryDate": "2026-09-14"})

	week := getWeek(t, owner, thisWeek)
	if week.WeekStart != thisWeek || week.SubmittedAt != nil || week.HasUnsubmittedChanges {
		t.Errorf("week = %s/%v/%v, want %s, never submitted, no unsubmitted changes", week.WeekStart, week.SubmittedAt, week.HasUnsubmittedChanges, thisWeek)
	}

	type rowWant struct {
		code, trackable string
		task            string
		days            [7][]int64
	}
	wants := []rowWant{
		{code: "EURO2026", days: [7][]int64{4: {euroFri.Id}}},
		{code: "KVEM1000", days: [7][]int64{0: {plainMonEarly.Id, plainMonLater.Id, plainMon.Id}, 1: {plainTue.Id}}},
		{code: "KVEM1000", trackable: "KVEM1000-PM", days: [7][]int64{0: {lineMon.Id}}},
		{code: "KVEM1000", trackable: "KVEM1000-PM", task: taskSpecificationTitle, days: [7][]int64{2: {taskWed.Id}}},
	}
	if len(week.Rows) != len(wants) {
		t.Fatalf("rows = %d, want %d: %+v", len(week.Rows), len(wants), week.Rows)
	}
	for i, want := range wants {
		row := week.Rows[i]
		if row.ProjectCode != want.code || deref(row.TrackableCode) != nilIfEmpty(want.trackable) || deref(row.TaskTitle) != nilIfEmpty(want.task) {
			t.Errorf("row %d = %s/%v/%v, want %s/%q/%q", i, row.ProjectCode, deref(row.TrackableCode), deref(row.TaskTitle), want.code, want.trackable, want.task)
		}
		if len(row.Days) != 7 {
			t.Fatalf("row %d: %d days, want 7", i, len(row.Days))
		}
		for d, day := range row.Days {
			if wantDate := time.Date(2026, 9, 14+d, 0, 0, 0, 0, time.UTC).Format(time.DateOnly); day.Date != wantDate {
				t.Errorf("row %d day %d: date %s, want %s", i, d, day.Date, wantDate)
			}
			if got := entryIDs(day.Entries...); !slices.Equal(got, want.days[d]) {
				t.Errorf("row %d day %d: entries %v, want %v", i, d, got, want.days[d])
			}
		}
	}
	if row := week.Rows[3]; deref(row.TaskId) != int32(taskSpecification) || deref(row.BillingLineId) != int32(lineFixed) || deref(row.BillingLineCode) != "PM" {
		t.Errorf("task row ids = %v/%v/%v, want 5001/3001/PM", deref(row.TaskId), deref(row.BillingLineId), deref(row.BillingLineCode))
	}
	if row := week.Rows[1]; row.ProjectId != projectKraftVerket || row.ProjectName != projectKraftVerketName || row.BillingLineId != nil || row.TaskId != nil {
		t.Errorf("plain row = %+v, want 1001 named, no line, no task", row)
	}

	if want := []float64{5, 3, 4, 0, 1, 0, 0}; !slices.Equal(week.Totals.PerDay, want) || week.Totals.Week != 13 {
		t.Errorf("totals = %v / %v, want %v / 13", week.Totals.PerDay, week.Totals.Week, want)
	}
}

func TestGetTimeWeeksByWeekStart_EmptyWeek_HasSevenZeroTotalsAndNoRows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	week := getWeek(t, owner, thisWeek)
	if len(week.Rows) != 0 || len(week.Totals.PerDay) != 7 || week.Totals.Week != 0 {
		t.Errorf("week = %+v, want no rows and seven zero totals", week)
	}
}

func TestWeeks_WeekStartNotAMonday_IsRefusedOnWeekStart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	createEntry(t, owner, nil)

	for _, r := range []struct {
		method, path string
	}{
		{http.MethodGet, weekPath("2026-09-15")},
		{http.MethodPost, weekPath("2026-09-20") + "/submit"},
	} {
		errs := validationErrors(t, owner.Do(r.method, r.path, nil), "Invalid week")
		if got := errs["weekStart"]; len(got) != 1 || !strings.Contains(got[0], "Monday") {
			t.Errorf("%s %s: errors = %v, want one on weekStart about Monday", r.method, r.path, errs)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM time.entries WHERE status = 'draft'`); n != 1 {
		t.Errorf("drafts = %d, want the entry untouched", n)
	}
}

func TestPostTimeWeeksByWeekStartSubmit_SubmitsTheDraftsAndRecordsTheWeek(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	colleague, _ := signInAs(t, h, projectKraftVerket, roleMember)

	draft := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14"})
	sunday := createEntry(t, owner, map[string]any{"entryDate": "2026-09-20"})
	approved := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15"})
	setStatus(t, h, approved.Id, "approved")
	rejected := createEntry(t, owner, map[string]any{"entryDate": "2026-09-16"})
	setStatus(t, h, rejected.Id, "rejected")
	later := createEntry(t, owner, map[string]any{"entryDate": nextWeek})
	theirs := createEntry(t, colleague, map[string]any{"entryDate": "2026-09-14"})

	week := submitWeek(t, owner, thisWeek)
	if week.SubmittedAt == nil || !week.SubmittedAt.Equal(h.Now()) || week.HasUnsubmittedChanges {
		t.Errorf("submittedAt/hasUnsubmittedChanges = %v/%v, want now/false", week.SubmittedAt, week.HasUnsubmittedChanges)
	}
	for id, want := range map[int64]string{
		draft.Id: "submitted", sunday.Id: "submitted", approved.Id: "approved", rejected.Id: "rejected",
		later.Id: "draft",
	} {
		got := getEntry(t, owner, id)
		if got.Status != want {
			t.Errorf("entry %d: status %s, want %s", id, got.Status, want)
		}
		if want == "submitted" && (got.Capabilities.CanEdit || got.Capabilities.CanSubmit || got.Revision != 2) {
			t.Errorf("entry %d: capabilities %+v revision %d, want neither edit nor submit, revision 2", id, got.Capabilities, got.Revision)
		}
	}
	if got := getEntry(t, colleague, theirs.Id); got.Status != "draft" {
		t.Errorf("colleague's entry: %s, want draft", got.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM time.entries WHERE submitted_at IS NOT NULL`); n != 2 {
		t.Errorf("entries with submitted_at = %d, want the two submitted", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM time.week_submissions WHERE user_id = $1 AND week_start = $2::date`, ownerID, thisWeek); n != 1 {
		t.Errorf("week submissions = %d, want 1", n)
	}

	// A later entry in a submitted week is an unsubmitted change, until the
	// week is submitted again.
	createEntry(t, owner, map[string]any{"entryDate": "2026-09-17"})
	if got := getWeek(t, owner, thisWeek); !got.HasUnsubmittedChanges || got.SubmittedAt == nil {
		t.Errorf("after a new draft: hasUnsubmittedChanges/submittedAt = %v/%v, want true and still submitted", got.HasUnsubmittedChanges, got.SubmittedAt)
	}
	// Another week is unaffected by this one's submission.
	if got := getWeek(t, owner, nextWeek); got.SubmittedAt != nil || got.HasUnsubmittedChanges {
		t.Errorf("next week = %v/%v, want never submitted", got.SubmittedAt, got.HasUnsubmittedChanges)
	}

	h.Advance(time.Hour)
	again := submitWeek(t, owner, thisWeek)
	if again.HasUnsubmittedChanges || again.SubmittedAt == nil || !again.SubmittedAt.Equal(h.Now()) {
		t.Errorf("resubmitted: %v/%v, want no unsubmitted changes, submitted now", again.HasUnsubmittedChanges, again.SubmittedAt)
	}
	if n := h.Count(t, `SELECT count(*) FROM time.week_submissions WHERE user_id = $1`, ownerID); n != 1 {
		t.Errorf("week submissions = %d, want the one row updated", n)
	}
}

func TestPostTimeWeeksByWeekStartSubmit_EmptyWeek_IsSubmittedAsNothingToReport(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	week := submitWeek(t, owner, thisWeek)
	if week.SubmittedAt == nil || len(week.Rows) != 0 || week.Totals.Week != 0 {
		t.Errorf("week = %+v, want an empty week recorded as submitted", week)
	}
	if got := getWeek(t, owner, thisWeek); got.SubmittedAt == nil || got.HasUnsubmittedChanges {
		t.Errorf("read back = %v/%v, want submitted, nothing unsubmitted", got.SubmittedAt, got.HasUnsubmittedChanges)
	}
}

func TestPostTimeWeeksByWeekStartSubmit_DraftsBeforeTheLock_RefuseTheWholeWeek(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")

	locked := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15"})
	open := createEntry(t, owner, map[string]any{"entryDate": "2026-09-17"})
	createEntry(t, manager, map[string]any{"entryDate": "2026-09-15"})
	setLock(t, h, "2026-09-16")

	errs := validationErrors(t, owner.Do(http.MethodPost, weekPath(thisWeek)+"/submit", nil), "Invalid submission")
	if got := errs["weekStart"]; len(got) != 1 || !strings.Contains(got[0], "2026-09-16") {
		t.Errorf("errors = %v, want the lock named on weekStart", errs)
	}
	for _, id := range []int64{locked.Id, open.Id} {
		if got := getEntry(t, owner, id); got.Status != "draft" {
			t.Errorf("entry %d: %s, want nothing submitted", id, got.Status)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM time.week_submissions WHERE user_id = $1`, ownerID); n != 0 {
		t.Errorf("week submissions = %d, want none recorded", n)
	}

	// time:manage is not held back by the lock.
	if week := submitWeek(t, manager, thisWeek); week.SubmittedAt == nil {
		t.Error("time:manage: submittedAt absent, want the week submitted")
	}

	// Once the locked draft is gone, the part of the week after the lock
	// submits: only drafts before the lock refuse.
	h.Exec(t, `DELETE FROM time.entries WHERE id = $1`, locked.Id)
	submitWeek(t, owner, thisWeek)
	if got := getEntry(t, owner, open.Id); got.Status != "submitted" {
		t.Errorf("open entry: %s, want submitted", got.Status)
	}
}

func TestPostTimeEntriesSubmit_OwnersDrafts_AreSubmittedInTheGivenOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	first := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14"})
	second := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15"})
	untouched := createEntry(t, owner, map[string]any{"entryDate": "2026-09-16"})

	got := submitEntries(t, owner, second.Id, first.Id, second.Id)
	if !slices.Equal(entryIDs(got...), []int64{second.Id, first.Id}) {
		t.Errorf("ids = %v, want [%d %d] (given order, duplicates once)", entryIDs(got...), second.Id, first.Id)
	}
	for _, e := range got {
		if e.Status != "submitted" || e.Revision != 2 || e.Capabilities.CanEdit || e.Capabilities.CanSubmit {
			t.Errorf("entry %d = %s rev %d %+v, want submitted, revision 2, no edit or submit", e.Id, e.Status, e.Revision, e.Capabilities)
		}
	}
	if e := getEntry(t, owner, untouched.Id); e.Status != "draft" {
		t.Errorf("untouched entry: %s, want draft", e.Status)
	}
	// Single entries do not submit the week.
	if week := getWeek(t, owner, thisWeek); week.SubmittedAt != nil {
		t.Errorf("week submittedAt = %v, want absent", week.SubmittedAt)
	}
}

func TestPostTimeEntriesSubmit_AnyRefusal_RefusesThemAllNamingEachId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	colleague, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	draft := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14"})
	submitted := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15"})
	submitEntries(t, owner, submitted.Id)
	locked := createEntry(t, owner, map[string]any{"entryDate": "2026-09-09"})
	theirs := createEntry(t, colleague, nil)
	managed := createEntry(t, manager, nil)
	setLock(t, h, "2026-09-10")

	// The owner: their own submitted and locked entries, a colleague's they
	// cannot see and an unknown id.
	unknown := draft.Id + 1000
	errs := validationErrors(t, owner.Do(http.MethodPost, submitPath, map[string]any{
		"ids": []int64{draft.Id, submitted.Id, locked.Id, theirs.Id, unknown},
	}), "Invalid submission")
	wantMessages := map[int64]string{
		submitted.Id: "is not a draft",
		locked.Id:    "before 2026-09-10",
		theirs.Id:    "was not found",
		unknown:      "was not found",
	}
	if len(errs["ids"]) != len(wantMessages) {
		t.Errorf("ids errors = %v, want one per offending id", errs["ids"])
	}
	for id, fragment := range wantMessages {
		if !slices.ContainsFunc(errs["ids"], func(m string) bool {
			return strings.Contains(m, fmt.Sprintf("Entry %d ", id)) && strings.Contains(m, fragment)
		}) {
			t.Errorf("ids errors = %v, want entry %d named with %q", errs["ids"], id, fragment)
		}
	}
	if got := getEntry(t, owner, draft.Id); got.Status != "draft" {
		t.Errorf("draft: %s, want nothing submitted", got.Status)
	}

	// A manager may see a member's entry, but it is not theirs to submit.
	errs = validationErrors(t, manager.Do(http.MethodPost, submitPath, map[string]any{"ids": []int64{managed.Id, draft.Id}}), "Invalid submission")
	if got := errs["ids"]; len(got) != 1 || !strings.Contains(got[0], fmt.Sprintf("Entry %d is not yours", draft.Id)) {
		t.Errorf("manager: ids errors = %v, want the member's entry named as not theirs", got)
	}
	if got := getEntry(t, manager, managed.Id); got.Status != "draft" {
		t.Errorf("manager's own draft: %s, want nothing submitted", got.Status)
	}

	errs = validationErrors(t, owner.Do(http.MethodPost, submitPath, map[string]any{"ids": []int64{}}), "Invalid submission")
	if len(errs["ids"]) != 1 {
		t.Errorf("no ids: errors = %v, want one on ids", errs)
	}
}

func TestPostTimeEntriesSubmit_TimeManage_SubmitsPastTheLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")
	e := createEntry(t, manager, map[string]any{"entryDate": "2026-09-09"})
	setLock(t, h, "2026-09-10")

	if got := submitEntries(t, manager, e.Id); got[0].Status != "submitted" {
		t.Errorf("status = %s, want submitted", got[0].Status)
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
