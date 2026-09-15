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
// granted and records the rules it was asked about.
type grantingAccess struct {
	granted []string
	asked   []contracts.Rule
}

func (a *grantingAccess) Check(_ *http.Request, rule contracts.Rule) (contracts.Principal, error) {
	a.asked = append(a.asked, rule)
	for _, name := range rule.Names {
		if slices.Contains(a.granted, name) {
			return contracts.Principal{}, nil
		}
	}
	return contracts.Principal{}, contracts.ErrForbidden
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
