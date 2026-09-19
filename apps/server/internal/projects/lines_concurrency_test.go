package projects_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins design §3.3's currency guard against the write it is most
// at risk from: a billing line created with a budgetAmount at the exact
// moment another request clears the project's currency. Both writers now
// take the project's own row lock (LockProject, FOR UPDATE) as their first
// statement inside their own transaction, which serialises the two rather
// than letting one count against a row the other has not committed yet.
// Neither writer's own lock alone is enough — only one held by both, taken
// first, actually closes the race (task-1-report.md's Fix round 1). Run
// with -count=10 or more to exercise the timing; -race cannot catch what
// this pins, because a lost guard decision is database behaviour, not a Go
// data race.

// TestPostProjectsByIdBillingLines_RacingAClearOfTheCurrency_NeverLeavesABudgetedLineWithoutOne
// races, many times over, "create a billing line with a budgetAmount"
// against "clear the project's currency". Whichever wins, the loser must be
// refused (400, on 'budgetAmount' if the line lost the race for the lock, on
// 'currency' if the clear did) — never both committed, which is the state
// design §3.3 exists to prevent: an amount denominated in a currency the
// project no longer has.
func TestPostProjectsByIdBillingLines_RacingAClearOfTheCurrency_NeverLeavesABudgetedLineWithoutOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	const rounds = 20
	for i := range rounds {
		project := createProject(t, c, map[string]any{
			"code": fmt.Sprintf("RACECUR%04d", i), "currency": "NOK",
		})

		postBudgetedLine := func() *modtest.Response {
			return postLine(t, c, project.Id, map[string]any{"budgetAmount": 5000})
		}
		clearCurrency := func() *modtest.Response {
			return updateProject(t, c, project, map[string]any{"currency": nil})
		}

		oneSucceeded, oneRefused := false, false
		for _, r := range race(postBudgetedLine, clearCurrency) {
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

		// The invariant design §3.3 exists to protect, whichever side won:
		// no billing line's amount is left denominated in a currency the
		// project no longer has.
		if n := h.Count(t, `
			SELECT count(*) FROM projects.billing_lines bl
			JOIN projects.projects p ON p.id = bl.project_id
			WHERE bl.project_id = $1 AND p.currency IS NULL
			  AND (bl.budget_amount IS NOT NULL OR bl.fixed_amount IS NOT NULL)`, project.Id); n != 0 {
			t.Fatalf("round %d: %d billing line(s) with an amount on a project with no currency", i, n)
		}
	}
}
