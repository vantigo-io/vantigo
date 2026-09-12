package customers_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestModule_ComposesAndDemandsAPermission proves customers mounts through
// module.Compose without a router problem — newHarness fails the test on any
// Compose error, which is what catches an operation the generated server never
// registers or a permission the catalog is missing — and that the listing
// answers the access layer's 401 without a session, as the contract documents
// it.
func TestModule_ComposesAndDemandsAPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.Client(t).Do(http.MethodGet, "/api/v1/customers", nil)
	if r.Status != http.StatusUnauthorized || r.Code() != "unauthenticated" {
		t.Errorf("status %d code %q body %s, want 401 unauthenticated", r.Status, r.Code(), r.Body)
	}
}

// TestModule_DeclaresItsPermissionCatalog pins the module's name and all
// thirteen permissions, field for field, against the .NET contributor
// (AZ/CustomerPermissionCatalogContributor.cs:9-49, inventory §6): every key
// is delegable, and only view, create and update are not sensitive.
func TestModule_DeclaresItsPermissionCatalog(t *testing.T) {
	t.Parallel()
	m := customers.Module()

	want := []contracts.Permission{
		{Key: "customers:view", Display: "View customers", Description: "View customer names, identifiers, and a sanitized activity summary.", Category: "Customers", Sensitive: false, Delegable: true},
		{Key: "customers:create", Display: "Create customers", Description: "Create customers without legal identity data.", Category: "Customers", Sensitive: false, Delegable: true},
		{Key: "customers:update", Display: "Update customers", Description: "Update customer names and basic non-sensitive details.", Category: "Customers", Sensitive: false, Delegable: true},
		{Key: "customers:delete", Display: "Delete customers", Description: "Delete customers and their customer-owned records.", Category: "Customers", Sensitive: true, Delegable: true},
		{Key: "customers:legal-identity-view", Display: "View legal identities", Description: "View customer legal identity and registry attribution.", Category: "Legal identity", Sensitive: true, Delegable: true},
		{Key: "customers:legal-identity-manage", Display: "Manage legal identities", Description: "Add, replace, or remove customer legal identity data.", Category: "Legal identity", Sensitive: true, Delegable: true},
		{Key: "customers:contacts-view", Display: "View contacts", Description: "View contact names and contact details.", Category: "Contacts", Sensitive: true, Delegable: true},
		{Key: "customers:contacts-manage", Display: "Manage contacts", Description: "Create, update, and delete contacts.", Category: "Contacts", Sensitive: true, Delegable: true},
		{Key: "customers:associations-view", Display: "View customer associations", Description: "View links between customers and contacts.", Category: "Associations", Sensitive: true, Delegable: true},
		{Key: "customers:associations-manage", Display: "Manage customer associations", Description: "Create, update, and remove customer-contact links.", Category: "Associations", Sensitive: true, Delegable: true},
		{Key: "customers:timeline-view", Display: "View customer timeline", Description: "View customer timeline entries, notes, provenance, and revisions.", Category: "Timeline", Sensitive: true, Delegable: true},
		{Key: "customers:timeline-manage", Display: "Manage customer timeline", Description: "Create, update, and delete customer timeline entries.", Category: "Timeline", Sensitive: true, Delegable: true},
		{Key: "customers:lookup-view", Display: "Use registry lookup", Description: "Search the external business registry for legal identities.", Category: "Lookup", Sensitive: true, Delegable: true},
	}
	if m.Name != "customers" {
		t.Errorf("Name = %q, want customers", m.Name)
	}
	if !slices.Equal(m.Permissions, want) {
		t.Errorf("Permissions = %+v, want %+v", m.Permissions, want)
	}
	if m.Directory == nil {
		t.Error("Module declares no customer directory")
	}
}

// TestModule_StubbedOperationAnswers501 proves one still-pending operation
// reaches its stub rather than a 404 from an unregistered route or a 500 from
// a nil handler: a signed-in caller holding the operation's permission passes
// the access check and gets back 501. That every operation the contract
// declares is in fact routed is router.Err()'s guarantee (checked at Compose
// time by every test's newHarness call, TestModule_ComposesAndDemandsAPermission
// included), not something this one request could prove on its own.
// getCustomersByIdTimeline is the timeline's (Task 9); customer CRUD/stats
// (Task 6), contacts/associations (Task 7) and legal identity/the Brreg
// lookup (Task 8) are implemented by the time this test runs, so it targets
// an operation still pending.
func TestModule_StubbedOperationAnswers501(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Off-contract by design: a 501 is the platform's answer to a handler that
	// does not exist yet, and no operation documents one. The stub never
	// looks up the customer, so an arbitrary id is enough to reach it.
	r := h.SignIn(t, "customers:timeline-view").Do(http.MethodGet, "/api/v1/customers/1/timeline", nil,
		modtest.SkipContract("the operation is not implemented yet"))
	if r.Status != http.StatusNotImplemented {
		t.Errorf("status %d body %s, want 501", r.Status, r.Body)
	}
}
