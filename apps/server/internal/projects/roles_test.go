package projects

import (
	"slices"
	"testing"
)

// The role table itself (design §5). It is small, it is the whole of what a
// role means inside this module, and every per-project authorization
// decision reads it — so it is worth pinning directly rather than only
// through the handlers that consult it.

func TestRoleCapabilities(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role string
		want []capability
	}{
		{"manager", []capability{capSee, capSeeFinancials, capContribute, capManage}},
		// member and viewer part company over capContribute alone: a member
		// writes the project's work — its tasks, and the checklists and
		// comments on them — and a viewer reads it.
		{"member", []capability{capSee, capContribute}},
		{"viewer", []capability{capSee}},
		// No role, and a role this module does not know, grant nothing —
		// which is what makes "the caller holds no role" the zero case
		// rather than a branch of its own.
		{"", nil},
		{"owner", nil},
	}
	for _, tc := range cases {
		if got := roleCapabilities[tc.role]; !slices.Equal(got, tc.want) {
			t.Errorf("roleCapabilities[%q] = %v, want %v", tc.role, got, tc.want)
		}
		if got := validRole(tc.role); got != (tc.want != nil) {
			t.Errorf("validRole(%q) = %v, want %v", tc.role, got, tc.want != nil)
		}
	}
}

// widenBy only ever adds: a caller whose global permissions already grant a
// capability never loses it to a narrower role.
func TestAccessWidenBy(t *testing.T) {
	t.Parallel()

	viewer := access{}.widenBy("viewer")
	if !viewer.CanSee || viewer.CanSeeFinancials || viewer.CanContribute || viewer.CanManage || viewer.Role != "viewer" {
		t.Errorf("access{}.widenBy(\"viewer\") = %+v, want see only", viewer)
	}
	member := access{}.widenBy(roleMember)
	if !member.CanSee || !member.CanContribute || member.CanSeeFinancials || member.CanManage {
		t.Errorf("access{}.widenBy(%q) = %+v, want see and contribute", roleMember, member)
	}
	manager := access{}.widenBy(roleManager)
	if !manager.CanSee || !manager.CanSeeFinancials || !manager.CanContribute || !manager.CanManage {
		t.Errorf("access{}.widenBy(%q) = %+v, want every capability", roleManager, manager)
	}
	// A global manage-all caller who is only a viewer on this project keeps
	// what manage-all gave them.
	global := access{CanSee: true, CanSeeFinancials: true, CanManage: true}.widenBy("viewer")
	if !global.CanManage || !global.CanSeeFinancials {
		t.Errorf("a manage-all caller widened by \"viewer\" = %+v, want nothing taken away", global)
	}
	none := access{}.widenBy("")
	if none.CanSee || none.CanSeeFinancials || none.CanContribute || none.CanManage || none.Role != "" {
		t.Errorf("access{}.widenBy(\"\") = %+v, want the zero access", none)
	}
}
