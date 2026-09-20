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

// An approval racing an unapproval of the same expense: both take its row lock
// and both judge the status under it, so one lands and the other finds the
// expense in the status it is no longer moving from. The end state is read from
// the row, not from a response.
func TestExpensesFlow_AnApprovalRacingAnUnapproval(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")
	other, _ := signIn(t, h, "expenses:approve")

	for round := range raceRounds {
		entry := createEntry(t, owner, outlayBody(nil))
		approvedBy(t, owner, approver, entry.Id)
		// It is approved, so the unapproval is the move that can land; an
		// approval of it must be refused, whichever order they arrive in.
		body := flowBody([]int64{entry.Id}, nil)
		var approve, unapprove int
		race(
			func() { approve = other.Do(http.MethodPost, approvePath, body).Status },
			func() { unapprove = approver.Do(http.MethodPost, unapprovePath, body).Status },
		)
		if approve != http.StatusBadRequest {
			t.Fatalf("round %d: the approval answered %d, want the per-id refusal", round, approve)
		}
		if unapprove != http.StatusOK {
			t.Fatalf("round %d: the unapproval answered %d, want 200", round, unapprove)
		}
		if n := h.Count(t, `SELECT count(*) FROM expenses.entries
			WHERE id = $1 AND status = 'draft' AND decided_at IS NULL AND decided_by_user_id IS NULL
			  AND submitted_at IS NULL AND revision = 4`, entry.Id); n != 1 {
			t.Errorf("round %d: %s, want one submit, one approval and one unapproval",
				round, entryColumnsDump(t, h, entry.Id))
		}
	}
}

// A rate override racing an approval of the same submitted line: the override
// holds the row and guards the revision, the approval holds it and guards the
// status, so they serialize. Either the override lands and the approval follows
// it, or the approval lands and the override is refused on the status it finds.
func TestExpensesFlow_ARateOverrideRacingAnApproval(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, approverID := signIn(t, h, "expenses:approve")
	other, _ := signIn(t, h, "expenses:approve")

	for round := range raceRounds {
		entry := createEntry(t, owner, mileageBody(nil))
		submitted := submitEntries(t, owner, entry.Id)

		var override, approve int
		race(
			func() {
				override = other.Do(http.MethodPut, entryRatePath(entry.Id),
					map[string]any{"rate": 8.00, "revision": submitted[0].Revision}).Status
			},
			func() { approve = approver.Do(http.MethodPost, approvePath, flowBody([]int64{entry.Id}, nil)).Status },
		)
		if approve != http.StatusOK && approve != http.StatusBadRequest {
			t.Fatalf("round %d: the approval answered %d, want 200 or a per-id refusal", round, approve)
		}
		if override != http.StatusOK && override != http.StatusBadRequest {
			t.Fatalf("round %d: the override answered %d, want 200 or the 400 that names the status", round, override)
		}

		want := "636.00"
		if override == http.StatusOK {
			want = "960.00"
		}
		if n := h.Count(t, `SELECT count(*) FROM expenses.entries
			WHERE id = $1 AND status = 'approved' AND decided_by_user_id = $2 AND gross_amount = $3::numeric`,
			entry.Id, approverID, want); n != 1 {
			t.Errorf("round %d: %s, want it approved at %s", round, entryColumnsDump(t, h, entry.Id), want)
		}
	}
}
