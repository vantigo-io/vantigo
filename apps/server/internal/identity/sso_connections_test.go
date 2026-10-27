package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// Identity's SSO connections provider (Entra phase 2, delivery B; plan
// reading 1): contracts.SSOConnections against the real identity, gated by
// identity itself on the request in the context.

// ssoConnections builds the module's provider over the harness's
// dependencies, as Compose builds it.
func (h *harness) ssoConnections(t testing.TB) contracts.SSOConnections {
	t.Helper()
	build := identity.Module(h.access).SSOConnections
	if build == nil {
		t.Fatal("the module declares no SSO connections")
	}
	return build(h.deps)
}

// asCaller is a context carrying a request with a session of userID's, MFA
// verified, as a module's handler receives it.
func asCaller(t testing.TB, h *harness, userID uuid.UUID) context.Context {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: h.session(t, userID, true)})
	return contracts.WithRequest(context.Background(), httptest.NewRecorder(), r)
}

// otherIssuer is an issuer the installation is not configured with.
const otherIssuer = "https://idp.example.test"

// linkUnder links userID to an account under issuer.
func linkUnder(t testing.TB, h *harness, userID uuid.UUID, issuer string) {
	t.Helper()
	h.exec(t, `INSERT INTO identity.oidc_links (issuer, subject, user_id, created_at) VALUES ($1, $2, $3, $4)`,
		issuer, uuid.NewString(), userID, h.now())
}

// An Owner gets every user not connected under the configured issuer, the
// Owners first and then by display name, with the address, the employee
// number and the disabled state: a pending user, one linked under the
// issuer, one announced and the Owner themselves are left out; a link under
// another issuer connects nobody here.
func TestSSOConnections_UnconnectedListsWhoIsLeftToConnect(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, ownerID := h.bootstrapOwner(t)

	elsewhere := h.insertUser(t, "a-elsewhere@example.test", identity.RoleUserID)
	linkUnder(t, h, elsewhere, otherIssuer)
	plain := h.insertUser(t, "b-plain@example.test", identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET employee_number = 'E-7', employee_number_key = 'E-7' WHERE id = $1`, plain)
	disabled := h.insertUser(t, "c-disabled@example.test", identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET is_disabled = true, disabled_by = 'owner' WHERE id = $1`, disabled)
	secondOwner := h.insertUser(t, "z-owner@example.test", identity.RoleOwnerID)

	pending := h.insertUser(t, "d-pending@example.test", identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET pending_since = $2 WHERE id = $1`, pending, h.now())
	linked := h.insertUser(t, "e-linked@example.test", identity.RoleUserID)
	linkUnder(t, h, linked, fakeIssuer)
	announcedUser := h.insertUser(t, "f-announced@example.test", identity.RoleUserID)
	mustAnnounce(t, owner, announcedUser, map[string]any{"userPrincipalName": "f@tenant.example"})

	got, err := h.ssoConnections(t).Unconnected(asCaller(t, h, ownerID), 100)
	if err != nil {
		t.Fatalf("Unconnected: %v", err)
	}
	want := []contracts.ConnectCandidate{
		{UserID: secondOwner, Email: "z-owner@example.test"},
		{UserID: elsewhere, Email: "a-elsewhere@example.test"},
		{UserID: plain, Email: "b-plain@example.test", EmployeeNumber: "E-7"},
		{UserID: disabled, Email: "c-disabled@example.test", Disabled: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Unconnected = %+v\nwant %+v", got, want)
	}

	// The limit bounds the list, in the same order.
	got, err = h.ssoConnections(t).Unconnected(asCaller(t, h, ownerID), 2)
	if err != nil || !slices.Equal(got, want[:2]) {
		t.Errorf("Unconnected(2) = %+v, %v\nwant %+v", got, err, want[:2])
	}
}

// Anyone but an Owner — a SystemAdmin, a context with no request, a context carrying only a user
// (contracts.AsUser, a read with no request behind it), a request with no
// session — gets nothing, and no error, from either read.
func TestSSOConnections_AnswersNothingToAnyoneElse(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, ownerID := h.bootstrapOwner(t)
	user := h.insertUser(t, "kari@example.test", identity.RoleUserID)
	mustAnnounce(t, owner, user, map[string]any{"objectId": fakeObjectID})
	h.insertUser(t, "ola@example.test", identity.RoleUserID)
	p := h.ssoConnections(t)
	keys := []contracts.DirectoryKey{{ObjectID: fakeObjectID}}

	// The Owner sees both, so nothing below is empty for want of data.
	if got, err := p.Unconnected(asCaller(t, h, ownerID), 100); err != nil || len(got) == 0 {
		t.Fatalf("the Owner's Unconnected = %v, %v; want the candidates", got, err)
	}
	if got, err := p.KeyHolders(asCaller(t, h, ownerID), keys); err != nil || len(got) != 1 {
		t.Fatalf("the Owner's KeyHolders = %v, %v; want the announcement", got, err)
	}

	admin := h.insertUser(t, "admin@example.test", identity.RoleSystemAdminID)
	anonymous := contracts.WithRequest(context.Background(), httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	for name, ctx := range map[string]context.Context{
		"a SystemAdmin":           asCaller(t, h, admin),
		"a user":                  asCaller(t, h, user),
		"no request":              context.Background(),
		"the Owner, AsUser alone": contracts.AsUser(context.Background(), ownerID),
		"a request, no session":   anonymous,
	} {
		if got, err := p.Unconnected(ctx, 100); err != nil || got != nil {
			t.Errorf("%s: Unconnected = %v, %v; want nothing", name, got, err)
		}
		if got, err := p.KeyHolders(ctx, keys); err != nil || got != nil {
			t.Errorf("%s: KeyHolders = %v, %v; want nothing", name, got, err)
		}
	}
}

// While Owners are held to MFA, an Owner whose session has no second factor
// is not admitted under policy:Owner+OwnerManagement: both reads answer
// nothing, and no error, where the same Owner verified gets the data.
func TestSSOConnections_AnswersNothingToAnOwnerWithoutMFA(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, append(f.options(), withEnv("OWNERS_REQUIRE_MFA", "1"))...)
	_, ownerID := h.bootstrapOwner(t)
	ola := h.insertUser(t, "ola@example.test", identity.RoleUserID)
	h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
	          VALUES ($1, $2, $3, 'Ola@Tenant.Example', true, 1, 'etag', $4, $4)`, uuid.New(), ola, fakeObjectID, h.now())
	p := h.ssoConnections(t)
	keys := []contracts.DirectoryKey{{ObjectID: fakeObjectID}}

	verified := asCaller(t, h, ownerID)
	if got, err := p.Unconnected(verified, 100); err != nil || len(got) == 0 {
		t.Fatalf("the verified Owner's Unconnected = %v, %v; want the candidates", got, err)
	}
	if got, err := p.KeyHolders(verified, keys); err != nil || len(got) != 1 {
		t.Fatalf("the verified Owner's KeyHolders = %v, %v; want the mapping", got, err)
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: h.session(t, ownerID, false)})
	noMFA := contracts.WithRequest(context.Background(), httptest.NewRecorder(), r)
	if got, err := p.Unconnected(noMFA, 100); err != nil || got != nil {
		t.Errorf("Unconnected without MFA = %v, %v; want nothing", got, err)
	}
	if got, err := p.KeyHolders(noMFA, keys); err != nil || got != nil {
		t.Errorf("KeyHolders without MFA = %v, %v; want nothing", got, err)
	}
}

// KeyHolders names who holds each key under the configured issuer: an
// announced link (with its user), a SCIM mapping by userName or externalId
// (with its user), an announced account (no user), compared as identity
// normalises keys; an announcement under another issuer holds nothing, and
// a key held twice answers the announced link first.
func TestSSOConnections_KeyHolders(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, ownerID := h.bootstrapOwner(t)

	kari := h.insertUser(t, "kari@example.test", identity.RoleUserID)
	mustAnnounce(t, owner, kari, map[string]any{"userPrincipalName": "Kari@Tenant.Example", "objectId": fakeObjectID})
	ola := h.insertUser(t, "ola@example.test", identity.RoleUserID)
	const olaObject = "33333333-3333-3333-3333-333333333333"
	h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
	          VALUES ($1, $2, $3, 'Ola@Tenant.Example', true, 1, 'etag', $4, $4)`, uuid.New(), ola, olaObject, h.now())
	const perObject = "44444444-4444-4444-4444-444444444444"
	mustExpect(t, h.accounts(t), announced("A-1", "per@tenant.example", perObject))
	const elsewhereObject = "55555555-5555-5555-5555-555555555555"
	elsewhere := announced("A-2", "", elsewhereObject)
	elsewhere.Issuer = otherIssuer
	mustExpect(t, h.accounts(t), elsewhere)

	keys := []contracts.DirectoryKey{
		{ObjectID: strings.ToUpper(fakeObjectID)},
		{UserPrincipalName: "KARI@tenant.example"},
		{UserPrincipalName: "ola@tenant.example"},
		{ObjectID: "{" + olaObject + "}"},
		{UserPrincipalName: "per@tenant.example"},
		{ObjectID: elsewhereObject},
		{UserPrincipalName: "nobody@tenant.example", ObjectID: "66666666-6666-6666-6666-666666666666"},
		{UserPrincipalName: "per@tenant.example", ObjectID: fakeObjectID},
	}
	got, err := h.ssoConnections(t).KeyHolders(asCaller(t, h, ownerID), keys)
	if err != nil {
		t.Fatalf("KeyHolders: %v", err)
	}
	want := []contracts.KeyHolder{
		{ObjectID: strings.ToUpper(fakeObjectID), Kind: contracts.KeyHolderExpected, UserID: &kari},
		{UserPrincipalName: "KARI@tenant.example", Kind: contracts.KeyHolderExpected, UserID: &kari},
		{UserPrincipalName: "ola@tenant.example", Kind: contracts.KeyHolderMapped, UserID: &ola},
		{ObjectID: "{" + olaObject + "}", Kind: contracts.KeyHolderMapped, UserID: &ola},
		{UserPrincipalName: "per@tenant.example", Kind: contracts.KeyHolderAccount},
		{UserPrincipalName: "per@tenant.example", ObjectID: fakeObjectID, Kind: contracts.KeyHolderExpected, UserID: &kari},
	}
	if !slices.EqualFunc(got, want, func(a, b contracts.KeyHolder) bool {
		return a.ObjectID == b.ObjectID && a.UserPrincipalName == b.UserPrincipalName && a.Kind == b.Kind &&
			((a.UserID == nil && b.UserID == nil) || (a.UserID != nil && b.UserID != nil && *a.UserID == *b.UserID))
	}) {
		t.Errorf("KeyHolders = %s\nwant %s", holders(got), holders(want))
	}
}

// holders is hs readable in a failure.
func holders(hs []contracts.KeyHolder) string {
	var b strings.Builder
	for _, h := range hs {
		user := "-"
		if h.UserID != nil {
			user = h.UserID.String()
		}
		b.WriteString("{" + h.ObjectID + " " + h.UserPrincipalName + " " + h.Kind + " " + user + "} ")
	}
	return b.String()
}

// While OIDC is off there is no issuer to connect under: an Owner is told
// so by Unconnected (contracts.ErrSSONotConfigured) and gets nothing from
// KeyHolders; anyone else still gets nothing, and no error, from either.
func TestSSOConnections_OIDCOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, ownerID := h.bootstrapOwner(t)
	h.insertUser(t, "kari@example.test", identity.RoleUserID)
	p := h.ssoConnections(t)
	ctx := asCaller(t, h, ownerID)
	if got, err := p.Unconnected(ctx, 100); !errors.Is(err, contracts.ErrSSONotConfigured) || got != nil {
		t.Errorf("Unconnected = %v, %v; want ErrSSONotConfigured", got, err)
	}
	if got, err := p.KeyHolders(ctx, []contracts.DirectoryKey{{ObjectID: fakeObjectID}}); err != nil || got != nil {
		t.Errorf("KeyHolders = %v, %v; want nothing", got, err)
	}
	admin := h.insertUser(t, "admin@example.test", identity.RoleSystemAdminID)
	if got, err := p.Unconnected(asCaller(t, h, admin), 100); err != nil || got != nil {
		t.Errorf("a SystemAdmin's Unconnected = %v, %v; want nothing", got, err)
	}
}
