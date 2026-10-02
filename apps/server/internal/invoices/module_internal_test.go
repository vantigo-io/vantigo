package invoices

import (
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The catalog is what an administrator sees when they build a role, so every
// word of it is pinned (D1): the keys, their display names and descriptions,
// the category, which are sensitive — issuing, the seller record and payments
// (payments and delivery design D1) — and that all five may be delegated.
func TestPermissions_AreTheCatalogTheDesignNames(t *testing.T) {
	t.Parallel()
	want := []contracts.Permission{
		{
			Key: "invoices:access", Display: "Use Invoices",
			Description: "Use the Invoices app and read every invoice, credit note, PDF and the invoice journal.",
			Category:    "Invoices", Sensitive: false, Delegable: true,
		},
		{
			Key: "invoices:create", Display: "Create invoices",
			Description: "Create, edit and delete invoice drafts, and preview a draft as PDF.",
			Category:    "Invoices", Sensitive: false, Delegable: true,
		},
		{
			Key: "invoices:issue", Display: "Issue invoices",
			Description: "Issue a draft into a numbered document that can never be changed, and create credit notes.",
			Category:    "Invoices", Sensitive: true, Delegable: true,
		},
		{
			Key: "invoices:manage", Display: "Manage invoicing",
			Description: "Change the seller record, the number series start, and the VAT codes and their rates.",
			Category:    "Invoices", Sensitive: true, Delegable: true,
		},
		{
			Key: "invoices:payments", Display: "Register payments",
			Description: "Register payments against issued invoices, and remove a registration with a reason.",
			Category:    "Invoices", Sensitive: true, Delegable: true,
		},
	}
	got := Module().Permissions
	if len(got) != len(want) {
		t.Fatalf("permissions = %+v, want the five of D1", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("permission %d = %+v, want %+v", i, got[i], w)
		}
		if err := contracts.ValidatePermission("invoices", got[i]); err != nil {
			t.Errorf("permission %q does not pass the platform's own rules: %v", got[i].Key, err)
		}
	}
}

// Invoices requires customers: mounted without a customer directory it fails
// at composition, naming the dependency, rather than panicking on the first
// request.
func TestMount_RefusesAnInstallationWithoutCustomers(t *testing.T) {
	t.Parallel()
	_, err := Module().Mount(module.Deps{})
	if err == nil || !strings.Contains(err.Error(), "requires the customers module") {
		t.Errorf("Mount without a directory = %v, want the customers requirement named", err)
	}
}
