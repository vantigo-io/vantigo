package projects_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// What milestones leave to the database rather than to a read-then-write
// check in Go, all three of it resting on the same two locks: the position a
// create appends at and design §3.3's currency guard (the project's row lock,
// which every milestone write takes first), and the revision a status move is
// guarded by (the milestone's own row lock, taken second).
//
// None of the three is observable from a single-threaded test — every other
// milestone test would pass with both locks removed — and -race cannot catch
// any of them, because a lost update is database behaviour and not a Go data
// race. Run with -count=10 or more to exercise the timing.

// Creates racing inside one project: appending is "the largest position plus
// one", and two transactions that read that number at the same time would
// both write it. What stops them is the lock every milestone write takes —
// the project's own row, FOR UPDATE, as its first statement. A row lock on
// the milestone could not: what two creates race for is the gap after the
// last row, and a gap has no row to lock. Tasks need an advisory lock of
// their own for this because their writes never touch the project row;
// milestones do not, because theirs always do.
//
// Four at a time over several rounds rather than two once: the window between
// reading the maximum and writing the row is short, and a single pair
// overlaps it too rarely to be evidence of anything.
func TestPostProjectsByIdMilestones_ConcurrentCreates_GetDistinctPositions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	const (
		rounds  = 10
		writers = 4
	)
	for i := range rounds {
		project := amountProject(t, c, fmt.Sprintf("MSPOS%04d", i))
		creates := make([]func() *modtest.Response, 0, writers)
		for w := range writers {
			creates = append(creates, func() *modtest.Response {
				return postMilestone(t, c, project.Id, map[string]any{"name": fmt.Sprintf("M%d", w)})
			})
		}

		positions := map[int32]bool{}
		for _, r := range race(creates...) {
			if r.Status != http.StatusCreated {
				t.Fatalf("round %d: status %d body %s, want 201", i, r.Status, r.Body)
			}
			var m milestoneJSON
			r.JSON(&m)
			if positions[m.Position] {
				t.Fatalf("round %d: position %d was handed out twice", i, m.Position)
			}
			positions[m.Position] = true
		}
		for n := int32(1); n <= writers; n++ {
			if !positions[n] {
				t.Fatalf("round %d: positions = %v, want exactly 1..%d", i, positions, writers)
			}
		}
		if got := milestonePositions(getMilestones(t, c, project.Id)); !equalInt32s(got, []int32{1, 2, 3, 4}) {
			t.Fatalf("round %d: plan positions = %v, want 1..n", i, got)
		}
	}
}

// Two status moves racing on one milestone, both carrying the revision the
// plan was read at: exactly one may be applied, and the other has to be told
// the milestone moved on. The revision is decided against the row the
// transaction holds locked, so the loser cannot read its own write back as
// the current one.
func TestPostProjectsMilestonesByMilestoneIdStatus_ConcurrentMoves_OneWinsAndOneConflicts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSRACE02")

	const rounds = 10
	for i := range rounds {
		m := createMilestone(t, c, project.Id, map[string]any{"name": fmt.Sprintf("Milepæl %d", i)})
		markReady := func() *modtest.Response { return moveMilestoneStatus(t, c, m, milestoneReady, nil) }
		cancel := func() *modtest.Response { return moveMilestoneStatus(t, c, m, milestoneCancelled, nil) }

		applied, conflicted := 0, 0
		for _, r := range race(markReady, cancel) {
			switch r.Status {
			case http.StatusOK:
				applied++
			case http.StatusConflict:
				conflicted++
			default:
				t.Fatalf("round %d: status %d body %s, want 200 or 409", i, r.Status, r.Body)
			}
		}
		if applied != 1 || conflicted != 1 {
			t.Fatalf("round %d: applied=%d conflicted=%d, want exactly one of each", i, applied, conflicted)
		}
		if got := getMilestone(t, c, m.Id).Revision; got != 2 {
			t.Fatalf("round %d: revision = %d, want exactly one move applied", i, got)
		}
	}
}

// Design §3.3's currency guard against the write it is most at risk from: a
// milestone created at the exact moment another request clears the project's
// currency. Both writers take the project's own row lock (LockProject, FOR
// UPDATE) as their first statement, which serialises them rather than letting
// one decide against a row the other has not committed yet — the same shape
// the billing-line race is closed with (lines_concurrency_test.go).
func TestPostProjectsByIdMilestones_RacingAClearOfTheCurrency_NeverLeavesOneWithoutIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	const rounds = 20
	for i := range rounds {
		project := amountProject(t, c, fmt.Sprintf("MSRACE%04d", i))

		addMilestone := func() *modtest.Response {
			return postMilestone(t, c, project.Id, map[string]any{"name": "Oppstart"})
		}
		clearCurrency := func() *modtest.Response {
			return updateProject(t, c, project, map[string]any{"currency": nil})
		}

		oneSucceeded, oneRefused := false, false
		for _, r := range race(addMilestone, clearCurrency) {
			switch r.Status {
			case http.StatusCreated, http.StatusOK:
				oneSucceeded = true
			case http.StatusBadRequest:
				oneRefused = true
			default:
				t.Fatalf("round %d: status %d body %s, want 200/201 or 400", i, r.Status, r.Body)
			}
		}
		if !oneSucceeded || !oneRefused {
			t.Fatalf("round %d: want exactly one side to win and the other refused, got succeeded=%v refused=%v",
				i, oneSucceeded, oneRefused)
		}
		if n := h.Count(t, `
			SELECT count(*) FROM projects.billing_milestones m
			JOIN projects.projects p ON p.id = m.project_id
			WHERE m.project_id = $1 AND p.currency IS NULL`, project.Id); n != 0 {
			t.Fatalf("round %d: %d milestone(s) on a project with no currency", i, n)
		}
	}
}

// LockProject's mode, pinned rather than assumed. Every guarded writer in
// this module holds the project's row for the length of its transaction, and
// Postgres takes `FOR KEY SHARE` on that same row for *every* insert that
// references it — a task, a role, a comment, a billing line, a milestone, a
// timeline entry. `FOR UPDATE` is the one row-lock mode that conflicts with
// `FOR KEY SHARE`, so it would make an unrelated task creation queue behind
// any milestone or line write; `FOR NO KEY UPDATE` does not, while still
// conflicting with itself and with the project UPDATE's own lock, so nothing
// the guards rely on is weaker.
//
// The test takes the module's own LockProject in one transaction and then,
// from a second connection, attempts both of those things without waiting:
// the key-share an FK check would take must succeed, and a second LockProject
// must not.
func TestLockProject_AdmitsForeignKeyChecksButNotASecondGuardedWriter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSLOCK01")

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := store.New(tx).LockProject(ctx, project.Id); err != nil {
		t.Fatalf("LockProject: %v", err)
	}

	// What an insert referencing the project takes. NOWAIT turns "would have
	// waited" into an error, so this never hangs the suite.
	probe, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.Pool().Exec(probe,
		`SELECT 1 FROM projects.projects WHERE id = $1 FOR KEY SHARE NOWAIT`, project.Id); err != nil {
		t.Errorf("a foreign-key check waits behind the project's lock: %v", err)
	}

	// And the mutual exclusion the guards depend on is still there.
	if _, err := h.Pool().Exec(probe,
		`SELECT 1 FROM projects.projects WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, project.Id); err == nil {
		t.Error("a second guarded writer took the project's lock while it was held")
	}
}
