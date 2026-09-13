package communications_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
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

// TestModule_StubbedOperationAnswers501 proves every operation is routed
// before any is implemented: a signed-in caller holding the operation's
// permission passes the access check and reaches the stub, which answers
// 501 rather than a 404 from an unregistered route or a 500 from a nil
// handler.
func TestModule_StubbedOperationAnswers501(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Off-contract by design: a 501 is the platform's answer to a handler
	// that does not exist yet, and no operation documents one.
	// getCommunicationsConversations moved to a real implementation in task
	// 5, so this now exercises a still-unimplemented operation instead
	// (getCommunicationsSuppressions, task 5's dispatch does not cover
	// suppressions).
	r := h.SignIn(t, "communications:suppressions-manage").
		Do(http.MethodGet, "/api/v1/communications/suppressions", nil, modtest.SkipContract("the operation is not implemented yet"))
	if r.Status != http.StatusNotImplemented {
		t.Errorf("status %d body %s, want 501", r.Status, r.Body)
	}
}
