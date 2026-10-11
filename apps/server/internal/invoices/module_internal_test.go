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
			Description: descAccess,
			Category:    "Invoices", Sensitive: false, Delegable: true,
		},
		{
			Key: "invoices:create", Display: "Create invoices",
			Description: "Create, edit and delete invoice drafts, and preview a draft as PDF.",
			Category:    "Invoices", Sensitive: false, Delegable: true,
		},
		{
			Key: "invoices:issue", Display: "Issue invoices",
			Description: descIssue,
			Category:    "Invoices", Sensitive: true, Delegable: true,
		},
		{
			Key: "invoices:manage", Display: "Manage invoicing",
			Description: descManage,
			Category:    "Invoices", Sensitive: true, Delegable: true,
		},
		{
			Key: "invoices:payments", Display: "Register payments",
			Description: descPayments,
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

// The descriptions D1's table (invoices payments and reminders design)
// rewrites, once every PR 1 operation exists (plan reading 41).
const (
	descAccess = "Use the Invoices app and read every invoice, credit note, PDF, payment and delivery, the journal, " +
		"the CSV export and the stats; the overdue list and an invoice's reminder letters and their PDFs, " +
		"hold, hand-off, manual deliveries and charges; the collection rates, the reminder settings and a " +
		"customer's reminder policy; and the attention items about overdue invoices and refunds due."
	descIssue = "Issue a draft into a numbered document that can never be changed, create credit notes, send an " +
		"issued document by e-mail or as EHF, cancel or resolve its EHF transmissions, and record that an " +
		"invoice was handed over or posted, or remove such a record."
	descManage = "Change the seller record and its Peppol id, the number series start, the KID agreement, the " +
		"e-invoicing access point's credentials, the VAT codes and their rates, the reminder settings and the " +
		"regime review, the collection rates — add one ahead of a release, or delete one nothing has relied " +
		"on — and the format a bank account's files are imported in."
	descPayments = "Register payments against issued invoices and remove a registration with a reason; import bank " +
		"files, read the imported files, the bank accounts and their lines, and work the exception queue; " +
		"make and read reminder runs, print paper letters and confirm them posted or reprint them, and " +
		"withdraw and retry letters; hold a disputed invoice, hand one to collection and export the " +
		"collection file; register and remove charge payments and waive charges; set a customer's reminder " +
		"policy; and see the attention items about the bank lines, the letters and the print batches."
)

// Each key's description names what D1's table gives it — and no other
// key's names it — so an administrator reading the catalog sees where the
// overdue list, the bank import, the runs, the settings and a manual
// delivery live (invoices payments and reminders design D1); and every
// operation's access rule is covered by the key it needs.
func TestPermissions_Descriptions(t *testing.T) {
	t.Parallel()
	gains := map[string][]string{
		"invoices:access": {"the overdue list", "an invoice's reminder letters and their PDFs, hold, hand-off, manual deliveries and charges",
			"the collection rates, the reminder settings and a customer's reminder policy",
			"attention items about overdue invoices and refunds due"},
		"invoices:issue": {"record that an invoice was handed over or posted"},
		"invoices:manage": {"the reminder settings and the regime review", "add one ahead of a release",
			"delete one nothing has relied on", "the format a bank account's files are imported in"},
		"invoices:payments": {"import bank files", "the bank accounts and their lines", "exception queue", "reminder runs",
			"print paper letters and confirm them posted or reprint them", "withdraw and retry letters",
			"hold a disputed invoice", "hand one to collection", "export the collection file", "charge payments",
			"waive charges", "set a customer's reminder policy",
			"attention items about the bank lines, the letters and the print batches"},
	}
	descriptions := map[string]string{}
	for _, p := range Module().Permissions {
		descriptions[p.Key] = p.Description
	}
	for key, phrases := range gains {
		for _, phrase := range phrases {
			if !strings.Contains(descriptions[key], phrase) {
				t.Errorf("%s's description %q does not name %q", key, descriptions[key], phrase)
			}
			for other, d := range descriptions {
				if other != key && strings.Contains(d, phrase) {
					t.Errorf("%s's description names %q, which D1 gives %s", other, phrase, key)
				}
			}
		}
	}
}
