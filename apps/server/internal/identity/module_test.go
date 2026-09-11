package identity_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// TestModule_ComposesAndDemandsASession proves identity mounts through
// module.Compose without a router problem (newHarness fails the test on any
// Compose error) and that GET /session without a cookie answers the access
// layer's 401, as the contract documents it.
func TestModule_ComposesAndDemandsASession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.client(t).do(http.MethodGet, "/api/v1/identity/session", nil)
	if r.status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", r.status)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	r.json(&body)
	if body.Error.Code != "unauthenticated" || body.Error.Message != "Authentication is required." {
		t.Errorf("body = %+v", body)
	}
}

// TestModule_DeclaresIdentityManage pins the module's name and its one
// catalog permission, as the .NET host registered it (HOST/Program.cs:203-206).
func TestModule_DeclaresIdentityManage(t *testing.T) {
	m := identity.Module(nil)
	want := []contracts.Permission{{Key: "identity:manage", Display: "Manage identity", Description: "Manage accounts, roles, and access.", Category: "Administration", Sensitive: true, Delegable: false}}
	if m.Name != "identity" || !slices.Equal(m.Permissions, want) {
		t.Errorf("Module = {Name: %q, Permissions: %+v}", m.Name, m.Permissions)
	}
}

// TestModule_ForbiddenAndScimRejectionsMatchTheContract proves Reject's
// other two bodies on the wire, both validated against the contract: a
// signed-in non-Owner on an Owner operation gets AuthErrorResponse 403
// forbidden, and a SCIM operation without a bearer token gets the SCIM 401.
func TestModule_ForbiddenAndScimRejectionsMatchTheContract(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.signIn(t, h.insertUser(t, "user@example.test", identity.RoleUserID), false).do(http.MethodGet, "/api/v1/identity/owner/users", nil)
	if r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("owner users as a User: status %d code %q, want 403 forbidden", r.status, r.code())
	}

	r = h.client(t).do(http.MethodGet, "/api/v1/identity/scim/v2/Users", nil)
	var scim struct {
		Schemas  []string `json:"schemas"`
		Status   string   `json:"status"`
		ScimType string   `json:"scimType"`
		Detail   string   `json:"detail"`
	}
	r.json(&scim)
	if r.status != http.StatusUnauthorized || r.header("Content-Type") != "application/scim+json" ||
		scim.Status != "401" || scim.ScimType != "invalidValue" || scim.Detail != "A valid SCIM bearer token is required." ||
		!slices.Equal(scim.Schemas, []string{"urn:ietf:params:scim:api:messages:2.0:Error"}) {
		t.Errorf("SCIM without a token: status %d Content-Type %q body %+v", r.status, r.header("Content-Type"), scim)
	}
}

// TestModule_RejectedCookieIsCleared proves a 401 to a request carrying a
// session cookie also tells the browser to drop it, as .NET signed a
// rejected cookie out.
func TestModule_RejectedCookieIsCleared(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.client(t).do(http.MethodGet, "/api/v1/identity/session", nil, header("Cookie", identity.SessionCookieName+"=stale"))
	if r.status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", r.status)
	}
	if c := r.header("Set-Cookie"); !strings.HasPrefix(c, identity.SessionCookieName+"=;") || !strings.Contains(c, "Max-Age=0") {
		t.Errorf("Set-Cookie = %q, want the session cookie cleared", c)
	}
}

// TestModule_StubbedOperationsSitBehindTheirAccessRule proves a stubbed
// operation is mounted behind its access rule. Every stub left is a SCIM
// operation, and the scim rule refuses every request until the SCIM
// bearer-token authenticator arrives with those operations, so the probe
// gets the rule's documented 401 and never reaches the stub. The stub's
// own 501 is module.ResponseError's mapping of ErrNotImplemented, which
// internal/module's tests cover.
func TestModule_StubbedOperationsSitBehindTheirAccessRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.client(t).do(http.MethodGet, "/api/v1/identity/scim/v2/Schemas", nil)
	if r.status != http.StatusUnauthorized || r.header("Content-Type") != "application/scim+json" {
		t.Errorf("status %d Content-Type %q, want the scim rule's 401", r.status, r.header("Content-Type"))
	}
}

// TestModule_UndecodableBodyAnswersInvalidRequest proves the generated
// server's decode failures reach identity's 400 invalid_request body, never
// the decoder's own error text.
func TestModule_UndecodableBodyAnswersInvalidRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Off-contract by design: the request body is not JSON.
	r := h.client(t).do(http.MethodPost, "/api/v1/identity/login", nil,
		rawBody("application/json", []byte(`{"email":`)), skipContract("deliberately malformed request body"))
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	r.json(&body)
	if r.status != http.StatusBadRequest || body.Error.Code != "invalid_request" || body.Error.Message != "The request is invalid." {
		t.Errorf("status %d body %s, want 400 invalid_request", r.status, r.body)
	}
}

// TestModule_CrossOriginProtection proves the harness runs identity behind
// the real server stack's CrossOriginProtection: a cross-site unsafe
// request is refused before identity sees it, and a same-origin one passes
// through to the access layer.
func TestModule_CrossOriginProtection(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Off-contract by design: the platform's 403 problem is not in identity.yaml.
	r := h.client(t).do(http.MethodPost, "/api/v1/identity/logout", nil,
		origin("https://attacker.example"), skipContract("cross-site request refused by the platform"))
	if r.status != http.StatusForbidden || r.header("Content-Type") != "application/problem+json" {
		t.Errorf("cross-site: status %d Content-Type %q, want the 403 problem", r.status, r.header("Content-Type"))
	}

	r = h.client(t).do(http.MethodPost, "/api/v1/identity/logout", nil, origin(h.url))
	if r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Errorf("same-origin: status %d code %q, want 401 unauthenticated", r.status, r.code())
	}
}

// TestHarness_ParallelSubtestsShareOneHarness proves two parallel subtests
// can share one harness, each with its own client: every client reports its
// failures and contract violations to the subtest it was made for, never to
// the parent that built the harness.
func TestHarness_ParallelSubtestsShareOneHarness(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := h.client(t)
			id := h.seedUser(t, name+"@example.test", "SharedHarness123", identity.RoleUserID)
			if r := c.do(http.MethodGet, "/api/v1/identity/bootstrap-status", nil); r.status != http.StatusOK {
				t.Errorf("bootstrap-status: status %d", r.status)
			}
			if r := h.signIn(t, id, false).do(http.MethodGet, "/api/v1/identity/session", nil); r.status != http.StatusOK {
				t.Errorf("session: status %d", r.status)
			}
		})
	}
}

// TestPendingOperationsAreExactlyTheStubs keeps pendingOperations and
// unimplemented.go in step: every stub is pending and every pending
// operation is still a stub, so implementing an operation means deleting
// its stub and its pending entry together.
func TestPendingOperationsAreExactlyTheStubs(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "unimplemented.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var stubs []string
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil {
			name := []rune(fn.Name.Name)
			name[0] = unicode.ToLower(name[0])
			stubs = append(stubs, string(name))
		}
	}
	slices.Sort(stubs)
	pending := slices.Sorted(slices.Values(pendingOperations))
	if !slices.Equal(stubs, pending) {
		t.Errorf("unimplemented.go stubs %v\n!= pendingOperations %v", stubs, pending)
	}
}
