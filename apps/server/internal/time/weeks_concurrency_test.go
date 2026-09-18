package timetracking_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
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

// Six batch submits at once, each by its own member, and a manager's batch
// that names a member's entry (refused, and decided on the manager's role).
// Every one is decided under row locks without asking the project directory
// anything inside the transaction: the harness fails the test at cleanup if
// a directory call was made inside one (lockedCalls), which is what would
// starve the shared pool when enough of these ran at once.
func TestPostTimeEntriesSubmit_ConcurrentBatches_AskNoDirectoryUnderLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	const members = 6
	clients := make([]*modtest.Client, members)
	batches := make([][]int64, members)
	for i := range members {
		c, userID := signInAs(t, h, projectKraftVerket, roleMember)
		h.projects.addRole(projectEuro, userID, roleMember)
		clients[i] = c
		batches[i] = entryIDs(
			createEntry(t, c, nil),
			createEntry(t, c, map[string]any{"projectId": projectEuro, "entryDate": "2026-09-15"}),
		)
	}
	own := createEntry(t, manager, nil)

	statuses := make([]int, members+1)
	fns := make([]func(), 0, members+1)
	for i := range members {
		fns = append(fns, func() {
			statuses[i] = clients[i].Do(http.MethodPost, submitPath, map[string]any{"ids": batches[i]}).Status
		})
	}
	fns = append(fns, func() {
		statuses[members] = manager.Do(http.MethodPost, submitPath, map[string]any{"ids": []int64{own.Id, batches[0][0]}}).Status
	})
	race(fns...)

	for i, s := range statuses[:members] {
		if s != http.StatusOK {
			t.Errorf("member %d: %d, want 200", i, s)
		}
	}
	if statuses[members] != http.StatusBadRequest {
		t.Errorf("manager naming a member's entry: %d, want 400", statuses[members])
	}
	if n := h.Count(t, `SELECT count(*) FROM time.entries WHERE status = 'submitted'`); n != 2*members {
		t.Errorf("submitted = %d, want %d", n, 2*members)
	}
}
