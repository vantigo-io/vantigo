package communications_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestModule_ComposesAndDemandsAPermission proves communications mounts
// through module.Compose without a router problem — newHarness fails the
// test on any Compose error, which is what catches an operation the
// generated server never registers or a permission the catalog is missing
// — and that the listing answers the access layer's 401 without a session,
// as the contract documents it.
func TestModule_ComposesAndDemandsAPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.Client(t).Do(http.MethodGet, "/api/v1/communications/channels", nil)
	if r.Status != http.StatusUnauthorized || r.Code() != "unauthenticated" {
		t.Errorf("status %d code %q body %s, want 401 unauthenticated", r.Status, r.Code(), r.Body)
	}
}

// TestModule_ContributesItsWorkers proves every background worker this module
// owns is reachable the way production starts it — through Module().Workers,
// which module.Workers collects for cmd/vantigo's runner — and not only
// through the constructors the worker tests call directly.
//
// It is an EXACT-SET assertion, and deliberately module-wide rather than one
// test per worker. The earlier version checked only for communications-outbox,
// which meant deleting NewRetentionWorker(d) from workers() left the whole
// suite green while retention silently never ran in production (fix round 1,
// important 1) — a worker fully implemented, fully tested, and never started.
//
// **Adding a worker to this module means adding its name here.** If you have
// written a worker and this test still passes unchanged, that is the symptom:
// it is not registered. The interval check is part of the same guard — the
// runner logs a worker's cadence and a zero interval would make a poll loop
// spin.
func TestModule_ContributesItsWorkers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	var names []string
	for _, w := range module.Workers(h.Deps(), communications.Module()) {
		names = append(names, w.Name())
		if w.Interval() <= 0 {
			t.Errorf("worker %s has interval %v, want a positive poll interval", w.Name(), w.Interval())
		}
	}
	slices.Sort(names)
	want := []string{"communications-attachment-cleanup", "communications-outbox", "communications-retention"}
	if !slices.Equal(names, want) {
		t.Errorf("workers = %v, want exactly %v", names, want)
	}
}

// TestModule_DeclaresItsPermissionCatalog pins the module's name and all
// five permissions, field for field, against the .NET catalog
// (AZ/CommunicationsPermissionCatalog.cs, communications inventory's
// "Permissions" preamble and
// TS/Authorization/CommunicationsPermissionCatalogTests.cs): every key
// shares one category, "Communications", every key is delegable, and
// exactly one key — conversations-view — is sensitive. This is an
// exact-set assertion over every field, not a presence check: an earlier
// module in this project shipped a permission-catalog gap that a
// presence-only test would have missed, which is exactly the shape
// slices.Equal below guards against — a permission silently dropped,
// renamed, mis-labelled, given the wrong category, or marked
// sensitive/delegable incorrectly all fail here.
func TestModule_DeclaresItsPermissionCatalog(t *testing.T) {
	t.Parallel()
	m := communications.Module()

	want := []contracts.Permission{
		{Key: "communications:channels-manage", Display: "Manage communications channels", Description: "Create, update, and verify configured communication channels.", Category: "Communications", Sensitive: false, Delegable: true},
		{Key: "communications:conversations-manage", Display: "Manage communications conversations", Description: "Assign, close, tag, and add internal notes to conversations.", Category: "Communications", Sensitive: false, Delegable: true},
		{Key: "communications:conversations-reply", Display: "Reply to communications conversations", Description: "Create outbound conversation messages and queue them for delivery.", Category: "Communications", Sensitive: false, Delegable: true},
		{Key: "communications:conversations-view", Display: "View communications conversations", Description: "View conversations, messages, participants, bodies, attachments, and tags.", Category: "Communications", Sensitive: true, Delegable: true},
		{Key: "communications:suppressions-manage", Display: "Manage communications suppressions", Description: "View, create, and remove suppressed email addresses.", Category: "Communications", Sensitive: false, Delegable: true},
	}
	if m.Name != "communications" {
		t.Errorf("Name = %q, want communications", m.Name)
	}
	if len(m.Permissions) != 5 {
		t.Fatalf("Permissions has %d entries, want exactly 5: %+v", len(m.Permissions), m.Permissions)
	}

	// The catalog's keys come back in stable alphabetical order — want is
	// itself written in alphabetical order, so this also doubles as a check
	// that want and m.Permissions agree on order, not just membership.
	keys := make([]string, len(m.Permissions))
	for i, p := range m.Permissions {
		keys[i] = p.Key
	}
	if !slices.IsSorted(keys) {
		t.Errorf("Permissions keys = %v, want alphabetically sorted", keys)
	}

	if !slices.Equal(m.Permissions, want) {
		t.Errorf("Permissions = %+v, want %+v", m.Permissions, want)
	}

	sensitive := map[string]bool{}
	for _, p := range m.Permissions {
		sensitive[p.Key] = p.Sensitive
		if p.Category != "Communications" {
			t.Errorf("permission %s: Category = %q, want %q", p.Key, p.Category, "Communications")
		}
		if !p.Delegable {
			t.Errorf("permission %s: Delegable = false, want true (the .NET contributor never sets Delegable, so every key takes PermissionDescriptor's own default)", p.Key)
		}
	}
	if !sensitive["communications:conversations-view"] {
		t.Error("communications:conversations-view: Sensitive = false, want true (its description covers bodies and participants)")
	}
	for key, isSensitive := range sensitive {
		if key != "communications:conversations-view" && isSensitive {
			t.Errorf("permission %s: Sensitive = true, want false (conversations-view is the only sensitive key)", key)
		}
	}

	if m.Directory != nil {
		t.Error("Module declares a customer directory, want nil: communications reads contracts.CustomerDirectory but never provides one")
	}
}

// TestModule_StubbedOperationAnswers501 was retired in task 10, and is
// recorded here rather than silently deleted because it guarded a real
// property. It proved every operation was routed before any was implemented
// — a signed-in caller reached the stub and got the platform's 501 rather
// than a 404 from an unregistered route or a 500 from a nil handler — by
// driving whichever area was still stubbed. Task 10 implemented the AI draft
// and customer suggestion, the last two stubs, so there is no stub left to
// drive and unimplemented.go itself is gone.
//
// The property is now guarded more strongly, in two places that cannot go
// stale: server.go's `var _ gen.StrictServerInterface = (*server)(nil)`
// fails the build if any operation is missing a method, and main_test.go's
// pendingOperations is empty, so RequireCoverage fails the run unless EVERY
// operation of communications.yaml answered a successful, contract-conforming
// exchange — which neither an unregistered route nor a nil handler could do.
