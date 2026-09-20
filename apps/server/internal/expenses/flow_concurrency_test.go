package expenses_test

import (
	"net/http"
	"slices"
	"testing"
)

// This file is what the flow does when two requests arrive at once. Every
// decision is made on rows the transaction holds under FOR UPDATE, in id
// order, so two callers cannot both act on one expense and neither can see a
// stale status.

// Two approvers reaching for the same submitted expense: one approves it, the
// other is told it is no longer submitted — never a second stamp, and never a
// 500.
func TestExpensesFlow_TwoApproversRacingForOneExpense(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	first, _ := signIn(t, h, "expenses:approve")
	second, _ := signIn(t, h, "expenses:approve")

	for round := range raceRounds {
		entry := createEntry(t, owner, outlayBody(nil))
		submitEntries(t, owner, entry.Id)

		body := flowBody([]int64{entry.Id}, nil)
		var a, b int
		race(
			func() { a = first.Do(http.MethodPost, approvePath, body).Status },
			func() { b = second.Do(http.MethodPost, approvePath, body).Status },
		)
		answers := []int{a, b}
		slices.Sort(answers)
		if !slices.Equal(answers, []int{http.StatusOK, http.StatusBadRequest}) {
			t.Fatalf("round %d: %d and %d, want one 200 and one per-id refusal", round, a, b)
		}
		if n := h.Count(t, `SELECT count(*) FROM expenses.entries
			WHERE id = $1 AND status = 'approved' AND decided_by_user_id IS NOT NULL`, entry.Id); n != 1 {
			t.Errorf("round %d: the expense was not approved exactly once", round)
		}
		if got := getEntry(t, owner, entry.Id).Revision; got != 3 {
			t.Errorf("round %d: revision = %d, want one submit and one approval", round, got)
		}
	}
}

// An owner submitting while they edit: both take the expense's row lock, so
// one lands wholly before the other. The edit either gets in first (and the
// submit freezes what it wrote) or finds the expense submitted and is refused
// on its status — never a half-frozen line.
func TestExpensesFlow_ASubmitRacingAnEdit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	for round := range raceRounds {
		entry := createEntry(t, owner, mileageBody(nil))
		update := mileageBody(map[string]any{"revision": entry.Revision, "distanceKm": 200.0})

		var submit, edit int
		race(
			func() { submit = owner.Do(http.MethodPost, submitPath, flowBody([]int64{entry.Id}, nil)).Status },
			func() { edit = owner.Do(http.MethodPut, entryPath(entry.Id), update).Status },
		)
		if submit != http.StatusOK && submit != http.StatusBadRequest {
			t.Fatalf("round %d: submit answered %d, want 200 or a per-id refusal", round, submit)
		}
		if edit != http.StatusOK && edit != http.StatusBadRequest {
			t.Fatalf("round %d: edit answered %d, want 200 or the 400 that names the status", round, edit)
		}

		got := getEntry(t, owner, entry.Id)
		switch {
		case submit == http.StatusOK && edit == http.StatusOK:
			// The edit went first: the submit froze 200 km at the seeded rate.
			if got.Status != "submitted" || got.GrossAmount != 1060 {
				t.Errorf("round %d: %+v, want the edited distance frozen (200 × 5.30)", round, got)
			}
		case submit == http.StatusOK:
			if got.Status != "submitted" || got.GrossAmount != 636 {
				t.Errorf("round %d: %+v, want the original 120 km frozen", round, got)
			}
		default:
			if got.Status != "draft" {
				t.Errorf("round %d: %+v, want a draft — the submit lost", round, got)
			}
		}
	}
}

// Two submits of one expense: one moves it, the other finds it already
// submitted.
func TestExpensesFlow_TwoSubmitsOfOneExpense(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	for round := range raceRounds {
		entry := createEntry(t, owner, outlayBody(nil))
		body := flowBody([]int64{entry.Id}, nil)

		var a, b int
		race(
			func() { a = owner.Do(http.MethodPost, submitPath, body).Status },
			func() { b = owner.Do(http.MethodPost, submitPath, body).Status },
		)
		answers := []int{a, b}
		slices.Sort(answers)
		if !slices.Equal(answers, []int{http.StatusOK, http.StatusBadRequest}) {
			t.Fatalf("round %d: %d and %d, want one 200 and one refusal", round, a, b)
		}
		if got := getEntry(t, owner, entry.Id).Revision; got != 2 {
			t.Errorf("round %d: revision = %d, want it submitted once", round, got)
		}
	}
}
