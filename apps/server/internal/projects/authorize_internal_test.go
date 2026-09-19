package projects

import "testing"

// TestCanManageMilestones_NeedsFinancialRightsToo pins the invariant every
// milestone *write* leans on. The four write operations gate on
// canManageMilestones alone (milestones.go), and their 200/201 bodies carry
// the plan's money — amount, percent, effectiveAmount, the frozen invoiced
// amount — while the reads are gated on canSeeMilestones. That is only safe
// while managing a project also means being allowed to see its financials.
//
// It is pinned twice over, deliberately. The helper states the rule itself
// rather than relying on the roles, so a future role or global permission
// that granted manage without financials would answer 403 rather than leak;
// and the roles are checked here too, so such a role is noticed when it is
// written rather than when somebody wonders why a manager gets a 403.
func TestCanManageMilestones_NeedsFinancialRightsToo(t *testing.T) {
	t.Parallel()

	// The helper refuses the combination outright, whatever produced it.
	if (access{CanManage: true}).canManageMilestones() {
		t.Error("canManageMilestones() is true for a caller who may not see the project's financials")
	}
	if !(access{CanManage: true, CanSeeFinancials: true}).canManageMilestones() {
		t.Error("canManageMilestones() is false for a manager who may see the financials")
	}

	// And no role grants the one without the other.
	for _, role := range projectRoles {
		a := access{}.widenBy(role)
		if a.CanManage && !a.CanSeeFinancials {
			t.Errorf("role %q grants manage without see-financials, which would leak amounts through the milestone write paths", role)
		}
	}
}
