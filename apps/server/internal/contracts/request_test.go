package contracts_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

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

// grantingAccess is an Access whose Check and UserHolds admit exactly the
// permissions in granted and record what they were asked. A rule naming
// several permissions is AND-joined, the way identity's own Access evaluates
// one (identity/access.go's permitted loops over Rule.Names and refuses on the
// first key the caller does not hold).
type grantingAccess struct {
	granted  []string
	asked    []contracts.Rule
	held     []heldQuestion
	holdsErr error
}

// heldQuestion is one UserHolds call: the user and the keys it named.
type heldQuestion struct {
	userID uuid.UUID
	keys   []string
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

func (a *grantingAccess) UserHolds(_ context.Context, userID uuid.UUID, keys ...string) (bool, error) {
	a.held = append(a.held, heldQuestion{userID: userID, keys: keys})
	if a.holdsErr != nil {
		return false, a.holdsErr
	}
	for _, key := range keys {
		if !slices.Contains(a.granted, key) {
			return false, nil
		}
	}
	return true, nil
}

// TestHasPermissions_AsUserAsksUserHolds proves the second rule (calendar
// feed design D5): with no request in the context but the AsUser mark, the
// keys are asked of Access.UserHolds for that user, in one question, and an
// error reads as false; a bare context, or one carrying a principal set by
// anything but AsUser, is still false without asking; and a request in the
// context is still checked through Check even when the mark is there too.
func TestHasPermissions_AsUserAsksUserHolds(t *testing.T) {
	user := uuid.New()
	access := &grantingAccess{granted: []string{"things:view", "things:manage"}}

	ctx := contracts.AsUser(context.Background(), user)
	if got, ok := contracts.AsUserFrom(ctx); !ok || got != user {
		t.Errorf("AsUserFrom = %v, %v; want the user AsUser named", got, ok)
	}
	if p, ok := contracts.PrincipalFrom(ctx); !ok || p.UserID != user || p.SessionID != uuid.Nil || len(p.Roles) != 0 {
		t.Errorf("PrincipalFrom = %+v, %v; want the user's id alone, no session and no roles", p, ok)
	}

	if !contracts.HasPermissions(ctx, access, "things:view", "things:manage") {
		t.Error("HasPermissions(AsUser, both held) = false")
	}
	if len(access.held) != 1 || access.held[0].userID != user || !slices.Equal(access.held[0].keys, []string{"things:view", "things:manage"}) {
		t.Errorf("UserHolds asked %+v, want one question for the user naming both keys", access.held)
	}
	if len(access.asked) != 0 {
		t.Errorf("Check asked %+v, want nothing: there is no request to check", access.asked)
	}
	if contracts.HasPermission(ctx, access, "things:delete") {
		t.Error("HasPermission(AsUser, not held) = true")
	}
	if contracts.HasPermissions(ctx, access) {
		t.Error("HasPermissions(AsUser, no keys) = true, want false")
	}

	failing := &grantingAccess{granted: []string{"things:view"}, holdsErr: errors.New("database down")}
	if contracts.HasPermission(contracts.AsUser(context.Background(), user), failing, "things:view") {
		t.Error("HasPermission(AsUser, UserHolds failing) = true, want false: the gate must fail closed")
	}

	bare := &grantingAccess{granted: []string{"things:view"}}
	if contracts.HasPermission(context.Background(), bare, "things:view") {
		t.Error("HasPermission(bare context) = true, want false")
	}
	if _, ok := contracts.AsUserFrom(context.Background()); ok {
		t.Error("AsUserFrom(bare context) reported a user")
	}
	principalOnly := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: user})
	if contracts.HasPermission(principalOnly, bare, "things:view") {
		t.Error("HasPermission(a principal without the AsUser mark) = true, want false: only AsUser opens the second rule")
	}
	if len(bare.held) != 0 || len(bare.asked) != 0 {
		t.Errorf("Access asked %+v / %+v, want nothing for a context without a request or the mark", bare.asked, bare.held)
	}

	routed := &grantingAccess{granted: []string{"things:view"}}
	withBoth := contracts.WithRequest(contracts.AsUser(context.Background(), user), httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !contracts.HasPermission(withBoth, routed, "things:view") {
		t.Error("HasPermission(request and AsUser) = false")
	}
	if len(routed.asked) != 1 || len(routed.held) != 0 {
		t.Errorf("Check asked %d times and UserHolds %d, want the request checked through Check alone", len(routed.asked), len(routed.held))
	}
}
