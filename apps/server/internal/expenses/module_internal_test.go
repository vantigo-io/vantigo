package expenses

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The catalog is what an administrator sees when they build a role, so every
// word of it is pinned: the keys, their display names and descriptions, the
// category they group under, which of them is sensitive, and that all four may
// be delegated.
//
// Two are sensitive: expenses:manage, which reaches the installation's own
// money settings and works past the period lock, and expenses:view-all, which
// exposes colleagues' personal outlays — what they bought, where and for how
// much — exactly as time:view-all is marked in the sibling module.
func TestPermissions_AreTheCatalogTheDesignNames(t *testing.T) {
	t.Parallel()
	want := []contracts.Permission{
		{
			Key: "expenses:access", Display: "Use Expenses",
			Description: "Use the Expenses app and record and submit your own expenses.",
			Category:    "Expenses", Sensitive: false, Delegable: true,
		},
		{
			Key: "expenses:approve", Display: "Approve expenses",
			Description: "Approve or reject anyone's expenses, including those with no project.",
			Category:    "Expenses", Sensitive: false, Delegable: true,
		},
		{
			Key: "expenses:view-all", Display: "View all expenses",
			Description: "See everyone's expenses.",
			Category:    "Expenses", Sensitive: true, Delegable: true,
		},
		{
			Key: "expenses:manage", Display: "Manage expenses",
			Description: "Change expense settings, rates and categories, record expenses for a colleague, mark expenses reimbursed, and work past the period lock.",
			Category:    "Expenses", Sensitive: true, Delegable: true,
		},
	}
	got := Module().Permissions
	if len(got) != len(want) {
		t.Fatalf("permissions = %+v, want the four of design §5", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("permission %d = %+v, want %+v", i, got[i], w)
		}
		if err := contracts.ValidatePermission("expenses", got[i]); err != nil {
			t.Errorf("permission %q does not pass the platform's own rules: %v", got[i].Key, err)
		}
	}
}

// Expenses depends on nobody (decision X1): it names itself, provides no
// contract of its own, and declares no directory it needs composed beside it.
// A future change that quietly made it require projects would fail here as
// well as in internal/config.
func TestModule_DependsOnNobody(t *testing.T) {
	t.Parallel()
	m := Module()
	if m.Name != "expenses" {
		t.Errorf("Name = %q, want expenses — the URL prefix, the schema and the MODULES entry are all this name", m.Name)
	}
	if m.Directory != nil || m.Users != nil || m.Products != nil || m.Projects != nil || m.Actuals != nil {
		t.Error("the module provides a contract of its own, want none in this delivery")
	}
	if m.Workers != nil {
		t.Error("the module contributes a background worker, want none")
	}
}
