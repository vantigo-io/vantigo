package timetracking_test

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// race runs every fn at once — released together, so none has a head start —
// and waits for all of them.
func race(fns ...func()) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn()
		}()
	}
	close(start)
	wg.Wait()
}

// raceRounds is how many times each race is run: enough that a missing row
// lock would not pass by luck of scheduling.
const raceRounds = 6

// roundWeek is round i's Monday, so every round works in a week of its own.
func roundWeek(i int) string {
	return time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 7*i).Format(time.DateOnly)
}

// A submit of the week racing an edit of one of its drafts: the two take the
// entry's row lock in some order. The edit first — it commits a draft, and
// the submit then submits the edited entry. The submit first — the entry is
// submitted, and the edit is refused with 403. Never both half-way: the
// entry ends submitted with either the old hours at revision 2 or the new
// ones at revision 3.
func TestPostTimeWeeksByWeekStartSubmit_RacingAnEdit_EndsCleanly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for i := range raceRounds {
		week := roundWeek(i)
		e := createEntry(t, owner, map[string]any{"entryDate": week, "hours": 2})
		var submit, edit int
		race(
			func() { submit = owner.Do(http.MethodPost, weekPath(week)+"/submit", nil).Status },
			func() {
				edit = owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"hours": 3})).Status
			},
		)
		got := getEntry(t, owner, e.Id)
		if submit != http.StatusOK || got.Status != "submitted" {
			t.Errorf("%s: submit %d, entry %s, want 200 and submitted", week, submit, got.Status)
		}
		switch edit {
		case http.StatusOK:
			if got.Hours != 3 || got.Revision != 3 {
				t.Errorf("%s: edit won, entry = %v h rev %d, want 3 h rev 3", week, got.Hours, got.Revision)
			}
		case http.StatusForbidden:
			if got.Hours != 2 || got.Revision != 2 {
				t.Errorf("%s: submit won, entry = %v h rev %d, want 2 h rev 2", week, got.Hours, got.Revision)
			}
		default:
			t.Errorf("%s: edit %d, want 200 or 403", week, edit)
		}
	}
}

// Submitting single entries races an edit the same way.
func TestPostTimeEntriesSubmit_RacingAnEdit_EndsCleanly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for i := range raceRounds {
		week := roundWeek(i)
		e := createEntry(t, owner, map[string]any{"entryDate": week, "hours": 2})
		var submit, edit int
		race(
			func() { submit = owner.Do(http.MethodPost, submitPath, map[string]any{"ids": []int64{e.Id}}).Status },
			func() {
				edit = owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"hours": 3})).Status
			},
		)
		got := getEntry(t, owner, e.Id)
		if submit != http.StatusOK || got.Status != "submitted" {
			t.Errorf("%s: submit %d, entry %s, want 200 and submitted", week, submit, got.Status)
		}
		if (edit == http.StatusOK) != (got.Hours == 3) || (edit != http.StatusOK && edit != http.StatusForbidden) {
			t.Errorf("%s: edit %d, entry %v h, want 200 with 3 h or 403 with 2 h", week, edit, got.Hours)
		}
	}
}

// A submit racing a delete of the same draft: the delete first — the entry is
// gone and the submit refuses it by id. The submit first — it is submitted
// and the delete is refused with 403.
func TestPostTimeEntriesSubmit_RacingADelete_EndsCleanly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for i := range raceRounds {
		week := roundWeek(i)
		e := createEntry(t, owner, map[string]any{"entryDate": week})
		var submit, del int
		race(
			func() { submit = owner.Do(http.MethodPost, submitPath, map[string]any{"ids": []int64{e.Id}}).Status },
			func() { del = owner.Do(http.MethodDelete, entryPath(e.Id), nil).Status },
		)
		stored := h.Count(t, `SELECT count(*) FROM time.entries WHERE id = $1`, e.Id)
		switch {
		case submit == http.StatusOK && del == http.StatusForbidden && stored == 1:
		case submit == http.StatusBadRequest && del == http.StatusNoContent && stored == 0:
		default:
			t.Errorf("%s: submit %d, delete %d, %d stored; want 200/403/1 or 400/204/0", week, submit, del, stored)
		}
	}
}

// Two edits read at the same revision: the second to take the row lock sees
// the first's revision and answers 409.
func TestPutTimeEntriesById_RacingEditsAtOneRevision_OneConflicts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for i := range raceRounds {
		week := roundWeek(i)
		e := createEntry(t, owner, map[string]any{"entryDate": week})
		statuses := make([]int, 2)
		race(
			func() {
				statuses[0] = owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"hours": 3})).Status
			},
			func() {
				statuses[1] = owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"hours": 4})).Status
			},
		)
		ok, conflict := 0, 0
		for _, s := range statuses {
			switch s {
			case http.StatusOK:
				ok++
			case http.StatusConflict:
				conflict++
			}
		}
		if ok != 1 || conflict != 1 {
			t.Errorf("%s: statuses %v, want one 200 and one 409", week, statuses)
		}
		if got := getEntry(t, owner, e.Id); got.Revision != 2 {
			t.Errorf("%s: revision %d, want 2", week, got.Revision)
		}
	}
}
