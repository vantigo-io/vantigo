package projects_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins what task ordering leaves to the database rather than to a
// read-then-write check in Go: every write that decides a position takes the
// project's ordering lock first, so the sibling numbers of one group are
// decided by one transaction at a time. Neither case is observable from a
// single-threaded test — every other task test would pass with the lock
// removed — and -race cannot catch either, because a lost update is database
// behaviour and not a Go data race. Run with -count=10 or more to exercise
// the timing.

// Two creates racing inside one project: appending is "the largest sibling
// number plus one", and two transactions that read that number at the same
// time would both write it. The lock is what makes the two answers different,
// and it has to hold for the very first task of a project too, where there is
// no row for a row lock to hold.
func TestPostProjectsByIdTasks_ConcurrentCreates_GetDistinctPositions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TRACE1000"})

	create := func(title string) func() *modtest.Response {
		return func() *modtest.Response {
			return postTask(t, c, project.Id, map[string]any{"title": title})
		}
	}
	responses := race(create("Første"), create("Andre"))

	positions := map[int32]bool{}
	for _, r := range responses {
		if r.Status != http.StatusCreated {
			t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
		}
		var task taskJSON
		r.JSON(&task)
		if positions[task.Position] {
			t.Errorf("position %d was handed out twice", task.Position)
		}
		positions[task.Position] = true
	}
	if !positions[1] || !positions[2] {
		t.Errorf("positions = %v, want exactly 1 and 2", positions)
	}
	if got := taskPositions(getTasks(t, c, project.Id)); !equalInt32s(got, []int32{1, 2}) {
		t.Errorf("tree positions = %v, want 1..n", got)
	}
}

// Two moves racing inside one sibling group: each renumbers the whole group
// 1..n from the order it read, so two transactions running at once would
// otherwise interleave two renumberings and leave a gap or a duplicate. Which
// of the two orders wins is not the point — that the group is still numbered
// 1..n afterwards is.
func TestPutProjectsTasksByTaskIdPosition_ConcurrentMoves_LeaveSiblingsNumbered1ToN(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TRACE1001"})
	first := createTask(t, c, project.Id, map[string]any{"title": "En"})
	createTask(t, c, project.Id, map[string]any{"title": "To"})
	third := createTask(t, c, project.Id, map[string]any{"title": "Tre"})
	createTask(t, c, project.Id, map[string]any{"title": "Fire"})

	move := func(taskID, position int32) func() *modtest.Response {
		return func() *modtest.Response {
			return moveTask(t, c, taskID, nil, position)
		}
	}
	for _, r := range race(move(third.Id, 1), move(first.Id, 4)) {
		if r.Status != http.StatusOK {
			t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
		}
	}

	tree := getTasks(t, c, project.Id)
	if got := taskPositions(tree); !equalInt32s(got, []int32{1, 2, 3, 4}) {
		t.Errorf("positions = %v, want 1..n with no gap and no duplicate", got)
	}
	if len(tree) != 4 {
		t.Errorf("tree = %v, want the four tasks", taskTitles(tree))
	}
}
