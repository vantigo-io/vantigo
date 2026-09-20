package expenses_test

import (
	"net/http"
	"slices"
	"testing"
)

// This file is what the claim flow does when two requests arrive at once. Every
// decision is made on rows the transaction holds under FOR UPDATE, in the
// module's own order — the claim's row first, then every expense row of the
// batch in one ascending pass — so two callers cannot both move one trip and
// neither can freeze half of one. Every assertion is on the rows themselves
// rather than on a response.

// Two approvers reaching for the same submitted trip: one approves it, the
// other is told it is no longer submitted — never a second stamp, and never a
// 500.
func TestExpensesClaimFlow_TwoApproversRacingForOneClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	first, _ := signIn(t, h, "expenses:approve")
	second, _ := signIn(t, h, "expenses:approve")

	for round := range raceRounds {
		claim := createClaim(t, owner, nil)
		addLine(t, owner, claim.Id, outlayBody(nil))
		submitClaims(t, owner, claim.Id)

		body := claimFlowBody([]int64{claim.Id}, nil)
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
		if n := h.Count(t, `SELECT count(*) FROM expenses.claims
			WHERE id = $1 AND status = 'approved' AND decided_by_user_id IS NOT NULL AND revision = 3`,
			claim.Id); n != 1 {
			t.Errorf("round %d: the trip was not approved exactly once (%s)", round, claimColumnsDump(t, h, claim.Id))
		}
	}
}

// An owner submitting a trip while they edit one of its lines: the submit holds
// the claim's row and then every line of it, and the edit holds the claim's row
// and then that one line, so they start at the same row and queue. Either the
// edit lands first and the submit freezes what it wrote, or it arrives after
// the freeze and is refused on the claim's status — never a half-frozen trip.
func TestExpensesClaimFlow_ASubmitRacingALineEdit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	for round := range raceRounds {
		claim := createClaim(t, owner, nil)
		line := addLine(t, owner, claim.Id, mileageBody(nil))
		update := mileageBody(map[string]any{
			"revision": line.Revision, "distanceKm": 200.0, "claimId": claim.Id,
		})

		var submit, edit int
		race(
			func() {
				submit = owner.Do(http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil)).Status
			},
			func() { edit = owner.Do(http.MethodPut, entryPath(line.Id), update).Status },
		)
		if submit != http.StatusOK && submit != http.StatusBadRequest {
			t.Fatalf("round %d: the submit answered %d, want 200 or a per-id refusal", round, submit)
		}
		if edit != http.StatusOK && edit != http.StatusBadRequest {
			t.Fatalf("round %d: the edit answered %d, want 200 or the 400 that names the claim", round, edit)
		}

		var wantStatus, wantGross string
		switch {
		case submit == http.StatusOK && edit == http.StatusOK:
			// The edit went first: the submit froze 200 km at the seeded rate.
			wantStatus, wantGross = "submitted", "1060.00"
		case submit == http.StatusOK:
			wantStatus, wantGross = "submitted", "636.00"
		default:
			wantStatus, wantGross = "draft", "1060.00"
		}
		if n := h.Count(t, `SELECT count(*) FROM expenses.claims c
			JOIN expenses.entries e ON e.claim_id = c.id
			WHERE c.id = $1 AND c.status = $2::text AND e.gross_amount = $3::numeric`,
			claim.Id, wantStatus, wantGross); n != 1 {
			t.Errorf("round %d: %s / %s, want a %s trip holding %s",
				round, claimColumnsDump(t, h, claim.Id), entryColumnsDump(t, h, line.Id), wantStatus, wantGross)
		}
	}
}

// A submit racing the receipt upload the receipt rule is waiting for: the
// upload writes its object, then takes the claim's row lock, so the two
// serialize. Either the receipt is there when the rule is judged and the trip
// goes in, or it is not and the submit is refused — never a submitted trip with
// an outlay the rule should have stopped.
func TestExpensesClaimFlow_ASubmitRacingAReceiptUpload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	putSettings(t, admin, settingsBody(map[string]any{"receiptRequiredOver": 1000.00}))
	owner, _ := signIn(t, h)

	for round := range raceRounds {
		claim := createClaim(t, owner, nil)
		line := addLine(t, owner, claim.Id, outlayBody(map[string]any{"grossAmount": 1250.00}))

		var submit, upload int
		race(
			func() {
				submit = owner.Do(http.MethodPost, submitPath, claimFlowBody([]int64{claim.Id}, nil)).Status
			},
			func() {
				upload = postReceipt(t, owner, line.Id, "kvittering.png", "image/png", testPNG(t, 4, 4)).Status
			},
		)
		if submit != http.StatusOK && submit != http.StatusBadRequest {
			t.Fatalf("round %d: the submit answered %d, want 200 or the receipt refusal", round, submit)
		}
		// The invariant, read from the rows: a submitted trip always has the
		// receipt, and a trip with no receipt is never submitted.
		receipts := h.Count(t, `SELECT count(*) FROM expenses.attachments WHERE entry_id = $1`, line.Id)
		submitted := h.Count(t, `SELECT count(*) FROM expenses.claims WHERE id = $1 AND status = 'submitted'`, claim.Id)
		if submitted == 1 && receipts == 0 {
			t.Fatalf("round %d: the trip is submitted with no receipt on a %s line (upload answered %d)",
				round, "1250.00", upload)
		}
	}
}

// Two payroll runs reaching for one approved trip: one stamps it, the other is
// told it has already been paid. Never two stamps.
func TestExpensesClaimFlow_TwoPayrollRunsRacingForOneClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage", "expenses:approve")
	other, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)

	for round := range raceRounds {
		claim := createClaim(t, owner, nil)
		addLine(t, owner, claim.Id, outlayBody(nil))
		approvedClaimBy(t, owner, admin, claim.Id)

		body := reimbursedClaimBody([]int64{claim.Id}, nil)
		var a, b int
		race(
			func() { a = admin.Do(http.MethodPost, reimbursedPath, body).Status },
			func() { b = other.Do(http.MethodPost, reimbursedPath, body).Status },
		)
		answers := []int{a, b}
		slices.Sort(answers)
		if !slices.Equal(answers, []int{http.StatusOK, http.StatusBadRequest}) {
			t.Fatalf("round %d: %d and %d, want one 200 and one per-id refusal", round, a, b)
		}
		if n := h.Count(t, `SELECT count(*) FROM expenses.claims
			WHERE id = $1 AND reimbursed_at IS NOT NULL AND revision = 4`, claim.Id); n != 1 {
			t.Errorf("round %d: the trip was not paid exactly once (%s)", round, claimColumnsDump(t, h, claim.Id))
		}
	}
}
