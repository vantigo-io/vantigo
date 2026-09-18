package timetracking_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Two approvers approving the same submitted entry at once: both reach the
// entry's row lock, one takes it and approves, and the other then finds the
// entry approved and is refused — never two approvals, never an approver
// recorded that did not win.
func TestPostTimeEntriesApprove_TwoApproversRacing_OneWins(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager)
	approver, approverID := signIn(t, h, "time:approve")

	for i := range raceRounds {
		e := submittedEntry(t, owner, map[string]any{"entryDate": roundWeek(i)})
		body := map[string]any{"ids": []int64{e.Id}}
		var byManager, byApprover int
		race(
			func() { byManager = manager.Do(http.MethodPost, approvePath, body).Status },
			func() { byApprover = approver.Do(http.MethodPost, approvePath, body).Status },
		)
		var winner uuid.UUID
		switch {
		case byManager == http.StatusOK && byApprover == http.StatusBadRequest:
			winner = managerID
		case byManager == http.StatusBadRequest && byApprover == http.StatusOK:
			winner = approverID
		default:
			t.Errorf("round %d: manager %d, approver %d, want one 200 and one 400", i, byManager, byApprover)
			continue
		}
		got := getEntry(t, owner, e.Id)
		if got.Status != "approved" || got.Revision != 3 || got.ApprovedBy == nil || got.ApprovedBy.UserId != winner {
			t.Errorf("round %d: entry = %s rev %d by %+v, want approved once, rev 3, by the winner", i, got.Status, got.Revision, got.ApprovedBy)
		}
	}
}

// An approval racing a rejection of the same entry ends in exactly one of
// the two.
func TestPostTimeEntriesApprove_RacingARejection_EndsInOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	approver, _ := signIn(t, h, "time:approve")

	for i := range raceRounds {
		e := submittedEntry(t, owner, map[string]any{"entryDate": roundWeek(i)})
		var approve, reject int
		race(
			func() {
				approve = manager.Do(http.MethodPost, approvePath, map[string]any{"ids": []int64{e.Id}}).Status
			},
			func() {
				reject = approver.Do(http.MethodPost, rejectPath, map[string]any{"ids": []int64{e.Id}, "reason": "Nei"}).Status
			},
		)
		got := getEntry(t, owner, e.Id)
		switch {
		case approve == http.StatusOK && reject == http.StatusBadRequest:
			if got.Status != "approved" || got.RejectionReason != nil {
				t.Errorf("round %d: approval won, entry = %s reason %v", i, got.Status, deref(got.RejectionReason))
			}
		case approve == http.StatusBadRequest && reject == http.StatusOK:
			if got.Status != "rejected" || got.ApprovedBy != nil {
				t.Errorf("round %d: rejection won, entry = %s approved by %+v", i, got.Status, got.ApprovedBy)
			}
		default:
			t.Errorf("round %d: approve %d, reject %d, want one 200 and one 400", i, approve, reject)
		}
	}
}
