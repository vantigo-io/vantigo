package contracts_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

func TestWithRequest_RoundTripsTheRequestAndWriter(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	w := httptest.NewRecorder()
	ctx := contracts.WithRequest(context.Background(), w, r)

	gotR, ok := contracts.RequestFrom(ctx)
	if !ok || gotR != r {
		t.Errorf("RequestFrom = %v, %v; want the attached request", gotR, ok)
	}
	gotW, ok := contracts.ResponseWriterFrom(ctx)
	if !ok || gotW != w {
		t.Errorf("ResponseWriterFrom = %v, %v; want the attached writer", gotW, ok)
	}
	if _, ok := contracts.RequestFrom(context.Background()); ok {
		t.Error("RequestFrom(empty context) reported a request")
	}
}

// grantingAccess is an Access whose Check admits exactly the permissions in
// granted and records the rules it was asked about. A rule naming several
// permissions is AND-joined, the way identity's own Access evaluates one
// (identity/access.go's permitted loops over Rule.Names and refuses on the
// first key the caller does not hold).
type grantingAccess struct {
	granted []string
	asked   []contracts.Rule
}

func (a *grantingAccess) Check(_ *http.Request, rule contracts.Rule) (contracts.Principal, error) {
	a.asked = append(a.asked, rule)
	for _, name := range rule.Names {
		if !slices.Contains(a.granted, name) {
			return contracts.Principal{}, contracts.ErrForbidden
		}
	}
	return contracts.Principal{}, nil
}

func (a *grantingAccess) Reject(http.ResponseWriter, *http.Request, contracts.Rule, error) {}

func TestHasPermission_AsksAccessForThePermissionAndFailsClosed(t *testing.T) {
	access := &grantingAccess{granted: []string{"things:manage"}}
	ctx := contracts.WithRequest(context.Background(), httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !contracts.HasPermission(ctx, access, "things:manage") {
		t.Error("HasPermission(granted) = false")
	}
	if contracts.HasPermission(ctx, access, "things:delete") {
		t.Error("HasPermission(not granted) = true")
	}
	if contracts.HasPermission(context.Background(), access, "things:manage") {
		t.Error("HasPermission(no request in context) = true, want false: the gate must fail closed")
	}
	if len(access.asked) == 0 || access.asked[0].Kind != contracts.RulePermission || access.asked[0].Names[0] != "things:manage" {
		t.Errorf("Access.Check asked %+v, want a permission rule for things:manage", access.asked)
	}
}

func TestHasPermissions_RequiresEveryKeyAndAsksOnce(t *testing.T) {
	access := &grantingAccess{granted: []string{"things:view", "things:manage"}}
	ctx := contracts.WithRequest(context.Background(), httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !contracts.HasPermissions(ctx, access, "things:view", "things:manage") {
		t.Error("HasPermissions(both granted) = false")
	}
	// Every key is required: holding one half of an AND-gate is not holding
	// the gate, in either order.
	if contracts.HasPermissions(ctx, access, "things:view", "things:delete") {
		t.Error("HasPermissions(second key not granted) = true, want false: all keys are required")
	}
	if contracts.HasPermissions(ctx, access, "things:delete", "things:view") {
		t.Error("HasPermissions(first key not granted) = true, want false: all keys are required")
	}
	if contracts.HasPermissions(context.Background(), access, "things:view") {
		t.Error("HasPermissions(no request in context) = true, want false: the gate must fail closed")
	}
	// No keys is never a gate anyone means to open, so it fails closed too —
	// without asking Access at all, since an empty Names list would satisfy
	// identity's AND-loop vacuously.
	before := len(access.asked)
	if contracts.HasPermissions(ctx, access) {
		t.Error("HasPermissions(no keys) = true, want false")
	}
	if len(access.asked) != before {
		t.Errorf("HasPermissions(no keys) asked Access %d times, want 0", len(access.asked)-before)
	}

	// The whole reason this exists rather than a loop of HasPermission calls:
	// both keys travel in ONE Access.Check, so a handler gating on two
	// permissions pays for one session lookup, not two.
	access.asked = nil
	contracts.HasPermissions(ctx, access, "things:view", "things:manage")
	if len(access.asked) != 1 {
		t.Fatalf("Access.Check called %d times, want exactly 1", len(access.asked))
	}
	if got := access.asked[0]; got.Kind != contracts.RulePermission || !slices.Equal(got.Names, []string{"things:view", "things:manage"}) {
		t.Errorf("Access.Check asked %+v, want one permission rule naming both keys", got)
	}
}
