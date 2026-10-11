package staff

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The catalog is what an administrator sees when they build a role, so every
// word of it is pinned: the five keys of design §5, their display names and
// descriptions, the category they group under, which of them is sensitive —
// staff:manage, which reaches every person's record and pay, and
// staff:personal-data, which reveals the national identity number and the
// bank account — and that all five may be delegated. All five are registered
// in delivery A1, staff:approve included, though it grants nothing until the
// absence it decides arrives (delivery B).
func TestPermissions_AreTheCatalogTheDesignNames(t *testing.T) {
	t.Parallel()
	want := []contracts.Permission{
		{
			Key: "staff:access", Display: "Use Staff",
			Description: "Use the Staff app: your own record, your own absence, your own documents and your own export.",
			Category:    "Staff", Sensitive: false, Delegable: true,
		},
		{
			Key: "staff:view", Display: "View the staff directory",
			Description: "See everyone's work details, employments and terms without pay, the company's structure, and the calendar with absence types hidden.",
			Category:    "Staff", Sensitive: false, Delegable: true,
		},
		{
			Key: "staff:approve", Display: "Decide absence",
			Description: "Decide anyone's absence requests, see absence types other than HR-only ones and the dates of open sick-leave cases, and mark their milestones.",
			Category:    "Staff", Sensitive: false, Delegable: true,
		},
		{
			Key: "staff:manage", Display: "Manage staff",
			Description: "Create and edit people (not their national identity number or bank account), employments and terms with pay, the company's structure, absence types and holiday schemes, the settings, documents other than health documents, the account flag, imports, exports and the payroll export.",
			Category:    "Staff", Sensitive: true, Delegable: true,
		},
		{
			Key: "staff:personal-data", Display: "Reveal and erase personal data",
			Description: "Reveal and change national identity numbers and bank accounts, read health documents and sick-leave notes, see HR-only absence types, export anyone's personal data, set legal holds, and erase and re-assess people.",
			Category:    "Staff", Sensitive: true, Delegable: true,
		},
	}
	got := Module().Permissions
	if len(got) != len(want) {
		t.Fatalf("permissions = %+v, want the five of design §5", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("permission %d = %+v, want %+v", i, got[i], w)
		}
		if err := contracts.ValidatePermission("staff", got[i]); err != nil {
			t.Errorf("permission %q does not pass the platform's own rules: %v", got[i].Key, err)
		}
	}
}

// Staff depends on nobody but identity (design S1): it names itself, provides
// no contract and fills no slot; its one background worker is its own
// retention run (TestModule_ContributesItsWorkers names it). A change that quietly made it provide or require one fails
// here as well as in internal/config.
func TestModule_DependsOnNobody(t *testing.T) {
	t.Parallel()
	m := Module()
	if m.Name != "staff" {
		t.Errorf("Name = %q, want staff — the URL prefix, the schema and the MODULES entry are all this name", m.Name)
	}
	if m.Directory != nil || m.Users != nil || m.Products != nil || m.Projects != nil || m.Actuals != nil ||
		m.Expenses != nil || m.BillableHours != nil || m.BillableExpenses != nil || m.BillableMilestones != nil {
		t.Error("the module provides a contract, want none")
	}
	if m.CustomerReferences != nil || m.CustomerPersonalData != nil || m.InvoicedWork != nil {
		t.Error("the module fills a many-provider slot, want none: it holds no customer and no invoiced work")
	}
	if m.Workers == nil {
		t.Error("the module contributes no background worker, want staff-retention (delivery A2)")
	}
}
