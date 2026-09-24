package customers_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
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
// fifteen permissions, field for field, against the .NET contributor
// (AZ/CustomerPermissionCatalogContributor.cs:9-49, inventory §6) plus
// customers:billing-manage (invoice-ready customer design D1, D4) and
// customers:merge (customers merge design D2), neither with a .NET ancestor:
// every key is delegable, and only view, create and update are not
// sensitive.
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
		{Key: "customers:billing-manage", Display: "Manage billing profiles", Description: "Set a customer's payment terms, invoice delivery and billing addresses for documents.", Category: "Billing", Sensitive: true, Delegable: true},
		{Key: "customers:merge", Display: "Merge customers", Description: "Merge a duplicate customer into another, moving its contacts, addresses, timeline, tags and other modules' references, and archiving it.", Category: "Customers", Sensitive: true, Delegable: true},
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

// There is no TestModule_StubbedOperationAnswers501 any more: that test
// exercised getCustomersByIdTimeline as the one operation still pending
// unimplemented.go's stub before Task 9. Task 9 implements the timeline —
// the module's last area — so unimplemented.go is gone and every one of the
// 29 contract operations now has a real handler; router.Err()'s "never
// registered" check (TestModule_ComposesAndDemandsAPermission's newHarness
// call) is what still proves every operation is routed.

// TestModule_ContributesItsWorkers proves the background workers this module
// owns are reachable the way production starts them — through Module().Workers,
// which module.Workers collects for cmd/vantigo's runner — and not only through
// the constructors the worker tests call directly.
//
// It is an EXACT-SET assertion per configuration, and deliberately one table
// rather than a test per worker. A worker fully implemented, fully tested and
// never registered is the failure this guards: communications learned that one
// the hard way (its own TestModule_ContributesItsWorkers says so), and **adding
// a worker to this module means adding its name here.** The switch rows are the
// other half: "off" has to mean the runner is never handed the thing that would
// make a scheduled outbound request, and a row asserting only "fewer workers"
// would not notice the wrong one disappearing.
func TestModule_ContributesItsWorkers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{name: "the default installation runs both", want: []string{"customers-peppol-recheck", "customers-registry-feed"}},
		{
			name: "the feed worker turned off leaves the re-check worker",
			env:  map[string]string{"CUSTOMERS_REGISTRY_FEED_ENABLED": "0"},
			want: []string{"customers-peppol-recheck"},
		},
		{
			name: "the re-check worker turned off leaves the feed worker",
			env:  map[string]string{"CUSTOMERS_PEPPOL_RECHECK_ENABLED": "0"},
			want: []string{"customers-registry-feed"},
		},
		{
			// Design D6: the re-check worker is effective only alongside the
			// lookup it uses, and "not effective" means never started.
			name: "the Peppol lookup turned off takes the re-check worker with it",
			env:  map[string]string{"PEPPOL_LOOKUP_ENABLED": "0"},
			want: []string{"customers-registry-feed"},
		},
		{
			name: "both turned off leaves none",
			env: map[string]string{
				"CUSTOMERS_REGISTRY_FEED_ENABLED":  "0",
				"CUSTOMERS_PEPPOL_RECHECK_ENABLED": "0",
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := make([]modtest.Option, 0, len(tc.env))
			for k, v := range tc.env {
				opts = append(opts, modtest.WithEnv(k, v))
			}
			h := newHarness(t, opts...)

			var names []string
			for _, w := range module.Workers(h.Deps(), customers.Module()) {
				names = append(names, w.Name())
				if w.Interval() <= 0 {
					t.Errorf("worker %s has interval %v, want a positive poll interval", w.Name(), w.Interval())
				}
			}
			slices.Sort(names)
			if !slices.Equal(names, tc.want) {
				t.Errorf("workers = %v, want exactly %v", names, tc.want)
			}
		})
	}
}

// TestModule_TheFeedWorkerPollCadenceIsConfigured pins that the runner-facing
// cadence is the operator's, not the constant's.
func TestModule_TheFeedWorkerPollCadenceIsConfigured(t *testing.T) {
	t.Parallel()
	if got := customers.NewRegistryFeedWorker(newHarness(t).Deps()).Interval(); got != 15*time.Minute {
		t.Errorf("Interval = %v, want the 15m default", got)
	}
	tuned := newHarness(t, modtest.WithEnv("CUSTOMERS_REGISTRY_FEED_POLL", "90s"))
	if got := customers.NewRegistryFeedWorker(tuned.Deps()).Interval(); got != 90*time.Second {
		t.Errorf("Interval = %v, want the configured 90s", got)
	}
}

// TestModule_ThePeppolRecheckCadenceIsConfigured pins that the second worker's
// runner-facing cadence is the operator's too.
func TestModule_ThePeppolRecheckCadenceIsConfigured(t *testing.T) {
	t.Parallel()
	if got := customers.NewPeppolRecheckWorker(newHarness(t).Deps()).Interval(); got != 24*time.Hour {
		t.Errorf("Interval = %v, want the 24h default", got)
	}
	tuned := newHarness(t, modtest.WithEnv("CUSTOMERS_PEPPOL_RECHECK_POLL", "6h"))
	if got := customers.NewPeppolRecheckWorker(tuned.Deps()).Interval(); got != 6*time.Hour {
		t.Errorf("Interval = %v, want the configured 6h", got)
	}
}
