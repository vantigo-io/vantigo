package projects_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the two guards this module leaves to the database rather
// than to a read-then-write check in Go: ux_projects_code on the create, and
// the `WHERE revision = $n` predicate on the update. Both only matter when
// two requests overlap, and neither is observable from a single-threaded
// test — every other test in this package would pass with both removed.
//
// -race cannot catch what these pin: a unique index and a conditional UPDATE
// are database behaviour, not a Go data race. Run with -count=10 or more to
// exercise the timing.

// race runs fns at once, each released only when every one is ready, and
// returns their responses in the same order —
// internal/customers/contacts_concurrency_test.go's race, duplicated here
// since it is unexported in a different package's _test.go file, not
// importable.
func race(fns ...func() *modtest.Response) []*modtest.Response {
	out := make([]*modtest.Response, len(fns))
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i, fn := range fns {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-begin
			out[i] = fn()
		}()
	}
	ready.Wait()
	close(begin)
	done.Wait()
	return out
}

// Two creates racing for one code: the unique index decides, and the loser
// gets the ordinary `code` field error a sequential duplicate gets (D3), not
// a 500 from an escaped 23505 and not a second project.
func TestPostProjects_ConcurrentCreatesOfOneCode_LeaveExactlyOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	create := func(name string) func() *modtest.Response {
		return func() *modtest.Response {
			return c.Do(http.MethodPost, "/api/v1/projects", createBody(map[string]any{
				"code": "RACE1000", "name": name,
			}))
		}
	}
	responses := race(create("Første"), create("Andre"))

	created, rejected := 0, 0
	for _, r := range responses {
		switch r.Status {
		case http.StatusCreated:
			created++
		case http.StatusBadRequest:
			rejected++
			var problem validationProblemJSON
			r.JSON(&problem)
			if len(problem.Errors["code"]) == 0 {
				t.Errorf("loser's Errors = %v, want the failure on the 'code' field", problem.Errors)
			}
		default:
			t.Errorf("status %d body %s, want 201 or 400", r.Status, r.Body)
		}
	}
	if created != 1 || rejected != 1 {
		t.Errorf("created = %d, rejected = %d, want exactly one of each", created, rejected)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.projects WHERE code = 'RACE1000'`); n != 1 {
		t.Errorf("projects with the code = %d, want 1", n)
	}
}

// Two updates carrying the same revision: the conditional UPDATE decides.
// The loser matches no row and answers 409 — the whole point of carrying a
// revision is that the second writer never silently overwrites the first.
func TestPutProjectsById_ConcurrentUpdatesOnOneRevision_LeaveOneWinner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "RACEUPD1000"})

	update := func(name string) func() *modtest.Response {
		return func() *modtest.Response {
			return updateProject(t, c, project, map[string]any{"name": name})
		}
	}
	responses := race(update("Første"), update("Andre"))

	applied, conflicted := 0, 0
	for _, r := range responses {
		switch r.Status {
		case http.StatusOK:
			applied++
		case http.StatusConflict:
			conflicted++
		default:
			t.Errorf("status %d body %s, want 200 or 409", r.Status, r.Body)
		}
	}
	if applied != 1 || conflicted != 1 {
		t.Errorf("applied = %d, conflicted = %d, want exactly one of each", applied, conflicted)
	}
	if got := h.Count(t, `SELECT revision FROM projects.projects WHERE id = $1`, project.Id); got != 2 {
		t.Errorf("revision = %d, want 2: exactly one update was applied", got)
	}
	if got := h.Count(t, `SELECT count(*) FROM projects.timeline_entries WHERE project_id = $1 AND event_type = 'details-changed'`, project.Id); got != 1 {
		t.Errorf("details-changed entries = %d, want 1", got)
	}
}
