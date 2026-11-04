package identity_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The Owner's directory search (Google Workspace design G8): identity
// serves the accounts of the configured provider's directory through the
// contracts.DirectorySearch under that provider's key, here a fake.

const (
	ssoDirectoryUsersPath   = ssoPath + "/directory/users"
	ssoDirectoryMatchesPath = ssoPath + "/directory/matches"
)

// The wire of the directory endpoints, as these tests read it.
type (
	directoryHolderBody struct {
		Kind   string     `json:"kind"`
		UserID *uuid.UUID `json:"userId"`
	}
	directoryUserBody struct {
		ObjectID          string               `json:"objectId"`
		UserPrincipalName string               `json:"userPrincipalName"`
		DisplayName       string               `json:"displayName"`
		Mail              *string              `json:"mail"`
		EmployeeNumber    *string              `json:"employeeNumber"`
		Enabled           bool                 `json:"enabled"`
		Guest             *bool                `json:"guest"`
		Holder            *directoryHolderBody `json:"holder"`
	}
	directoryMatchesBody struct {
		Matches []struct {
			UserID    uuid.UUID         `json:"userId"`
			Account   directoryUserBody `json:"account"`
			MatchedBy []string          `json:"matchedBy"`
		} `json:"matches"`
		Candidates int  `json:"candidates"`
		Ambiguous  int  `json:"ambiguous"`
		Truncated  bool `json:"truncated"`
	}
	directorySettingsBody struct {
		Directory *struct {
			Provider    string `json:"provider"`
			DisplayName string `json:"displayName"`
		} `json:"directory"`
	}
)

// directoryOwner is a harness with dir composed, and its Owner, with
// single sign-on configured for provider when provider is not "".
func directoryOwner(t *testing.T, dir contracts.DirectorySearch, provider string, opts ...harnessOption) (*harness, *client, uuid.UUID) {
	t.Helper()
	h := newHarness(t, append([]harnessOption{withDirectorySearch(dir)}, opts...)...)
	owner, ownerID := h.bootstrapOwner(t)
	if provider != "" {
		mustPutOIDC(t, owner, getSSO(t, owner).Version, ssoRequest(provider, map[string]any{"clientSecret": "s"}))
	}
	return h, owner, ownerID
}

// errorCode is an AuthErrorResponse's code.
func errorCode(r *resp) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	r.json(&body)
	return body.Error.Code
}

func TestSsoDirectory_UsersAnswerTheProvidersAccounts(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeDirectorySearch()
	_, owner, _ := directoryOwner(t, fake, "entra")
	holder := uuid.New()
	fake.SetUsers(
		contracts.DirectoryUser{ObjectID: "0b0f7c1e-1111-4a4a-8888-000000000001", PrincipalName: "kari@nordlys.no", DisplayName: "Kari Nordmann",
			Mail: "Kari@nordlys.no", EmployeeNumber: "E-1", Enabled: true,
			Holder: &contracts.KeyHolder{Kind: contracts.KeyHolderMapped, UserID: &holder}},
		contracts.DirectoryUser{ObjectID: "0b0f7c1e-1111-4a4a-8888-000000000002", PrincipalName: "kari_x#EXT#@nordlys.no", DisplayName: "Kari Guest",
			Guest: true, Holder: &contracts.KeyHolder{Kind: contracts.KeyHolderAccount}},
		contracts.DirectoryUser{ObjectID: "0b0f7c1e-1111-4a4a-8888-000000000003", PrincipalName: "ola@nordlys.no", DisplayName: "Ola"},
	)

	r := owner.do(http.MethodGet, ssoDirectoryUsersPath+"?"+url.Values{"search": {` "Ka\ `}}.Encode(), nil)
	if r.status != http.StatusOK {
		t.Fatalf("search: %d %s", r.status, r.body)
	}
	var got struct {
		Users []directoryUserBody `json:"users"`
	}
	r.json(&got)
	if len(got.Users) != 2 {
		t.Fatalf("search Ka: %+v, want the two Karis", got.Users)
	}
	first, second := got.Users[0], got.Users[1]
	if first.ObjectID != "0b0f7c1e-1111-4a4a-8888-000000000001" || first.UserPrincipalName != "kari@nordlys.no" || first.DisplayName != "Kari Nordmann" ||
		first.Mail == nil || *first.Mail != "Kari@nordlys.no" || first.EmployeeNumber == nil || *first.EmployeeNumber != "E-1" ||
		!first.Enabled || first.Guest != nil || first.Holder == nil || first.Holder.Kind != "mapped" || first.Holder.UserID == nil || *first.Holder.UserID != holder {
		t.Errorf("the first account: %+v (holder %+v)", first, first.Holder)
	}
	if second.Mail != nil || second.EmployeeNumber != nil || second.Enabled || second.Guest == nil || !*second.Guest ||
		second.Holder == nil || second.Holder.Kind != "account" || second.Holder.UserID != nil {
		t.Errorf("the second account: %+v (holder %+v)", second, second.Holder)
	}
	// The search was handed on trimmed and rid of " and \, with the limit.
	if calls := fake.Calls(); len(calls) != 1 || calls[0].Search != "Ka" || calls[0].Limit != 25 {
		t.Errorf("the provider was asked %+v, want Users(Ka, 25)", calls)
	}

	// Too short once trimmed or stripped, or too long: 400 on search, the
	// provider not asked.
	for _, q := range []string{" k ", `"k"`, "", strings.Repeat("a", 101)} {
		r := owner.do(http.MethodGet, ssoDirectoryUsersPath+"?"+url.Values{"search": {q}}.Encode(), nil)
		var body struct {
			Error struct {
				Code   string              `json:"code"`
				Fields map[string][]string `json:"fields"`
			} `json:"error"`
		}
		r.json(&body)
		if r.status != http.StatusBadRequest || body.Error.Code != "invalid_request" || len(body.Error.Fields["search"]) == 0 {
			t.Errorf("search %q: %d %s, want 400 invalid_request on search", q, r.status, r.body)
		}
	}
	if n := len(fake.Calls()); n != 1 {
		t.Errorf("the provider was asked %d times, want once: a refused search asks nothing", n)
	}
}

func TestSsoDirectory_MatchesAnswerTheProviders(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeDirectorySearch()
	_, owner, _ := directoryOwner(t, fake, "entra")
	anne := uuid.New()
	fake.SetMatches(contracts.DirectoryMatches{
		Matches: []contracts.DirectoryMatch{{
			UserID:    anne,
			Account:   contracts.DirectoryUser{ObjectID: "0b0f7c1e-1111-4a4a-8888-000000000001", PrincipalName: "anne@nordlys.no", DisplayName: "Anne", Mail: "anne@nordlys.no", Enabled: true},
			MatchedBy: []string{contracts.DirectoryMatchedByMail, contracts.DirectoryMatchedByPrincipalName},
		}},
		Candidates: 4, Ambiguous: 1, Truncated: true,
	})
	r := owner.do(http.MethodGet, ssoDirectoryMatchesPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("matches: %d %s", r.status, r.body)
	}
	var got directoryMatchesBody
	r.json(&got)
	if got.Candidates != 4 || got.Ambiguous != 1 || !got.Truncated || len(got.Matches) != 1 {
		t.Fatalf("matches: %+v", got)
	}
	m := got.Matches[0]
	if m.UserID != anne || m.Account.ObjectID != "0b0f7c1e-1111-4a4a-8888-000000000001" || m.Account.UserPrincipalName != "anne@nordlys.no" ||
		!slices.Equal(m.MatchedBy, []string{"mail", "principalName"}) || m.Account.Holder != nil {
		t.Errorf("the match: %+v", m)
	}

	// None: an empty list, not null.
	fake.SetMatches(contracts.DirectoryMatches{})
	r = owner.do(http.MethodGet, ssoDirectoryMatchesPath, nil)
	var raw map[string]json.RawMessage
	r.json(&raw)
	if r.status != http.StatusOK || string(raw["matches"]) != "[]" {
		t.Errorf("no matches: %d %s, want an empty list", r.status, r.body)
	}
}

// 409 no_directory while single sign-on is off, and while the configured
// provider is one no module serves the directory of; the provider is not
// asked. The settings name the directory only when there is one.
func TestSsoDirectory_NoDirectory(t *testing.T) {
	t.Parallel()
	refused := func(t *testing.T, owner *client) {
		t.Helper()
		for _, path := range []string{ssoDirectoryUsersPath + "?search=kari", ssoDirectoryMatchesPath} {
			if r := owner.do(http.MethodGet, path, nil); r.status != http.StatusConflict || errorCode(r) != "no_directory" {
				t.Errorf("GET %s: %d %s, want 409 no_directory", path, r.status, r.body)
			}
		}
	}
	directory := func(t *testing.T, owner *client) directorySettingsBody {
		t.Helper()
		r := owner.do(http.MethodGet, ssoPath, nil)
		var got directorySettingsBody
		r.json(&got)
		return got
	}
	t.Run("single sign-on off", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakeDirectorySearch()
		_, owner, _ := directoryOwner(t, fake, "")
		refused(t, owner)
		if got := directory(t, owner); got.Directory != nil {
			t.Errorf("the settings' directory: %+v, want null", got.Directory)
		}
		if n := len(fake.Calls()); n != 0 {
			t.Errorf("the provider was asked %d times, want never", n)
		}
	})
	t.Run("no provider under the configured key", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakeDirectorySearch()
		_, owner, _ := directoryOwner(t, fake, "google")
		refused(t, owner)
		if got := directory(t, owner); got.Directory != nil {
			t.Errorf("the settings' directory: %+v, want null", got.Directory)
		}
		if n := len(fake.Calls()); n != 0 {
			t.Errorf("the provider was asked %d times, want never", n)
		}
	})
	t.Run("the configured provider's", func(t *testing.T) {
		t.Parallel()
		entra := modtest.NewFakeDirectorySearch()
		google := modtest.NewFakeDirectorySearch().WithKey("google", "Google Workspace")
		_, owner, _ := directoryOwner(t, entra, "google", withDirectorySearch(google))
		if got := directory(t, owner); got.Directory == nil || got.Directory.Provider != "google" || got.Directory.DisplayName != "Google Workspace" {
			t.Errorf("the settings' directory: %+v, want google's", got.Directory)
		}
		if r := owner.do(http.MethodGet, ssoDirectoryMatchesPath, nil); r.status != http.StatusOK {
			t.Errorf("matches: %d %s", r.status, r.body)
		}
		if len(entra.Calls()) != 0 || len(google.Calls()) != 1 {
			t.Errorf("entra asked %d times, google %d, want google alone", len(entra.Calls()), len(google.Calls()))
		}
		// Single sign-on turned off: the settings' answer drops it.
		r := owner.do(http.MethodDelete, ssoOIDCPath, nil)
		var after directorySettingsBody
		r.json(&after)
		if r.status != http.StatusOK || after.Directory != nil {
			t.Errorf("DELETE oidc: %d, directory %+v, want null", r.status, after.Directory)
		}
	})
}

// A directory that could not be asked is 503 provider_unreachable; single
// sign-on turned off while the provider read is 409 no_directory.
func TestSsoDirectory_ProviderFailures(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeDirectorySearch()
	_, owner, _ := directoryOwner(t, fake, "entra")
	down := fmt.Errorf("entra: %w: tenant_unreachable", contracts.ErrDirectoryUnavailable)
	fake.FailWith("Users", down)
	fake.FailWith("Matches", down)
	for _, path := range []string{ssoDirectoryUsersPath + "?search=kari", ssoDirectoryMatchesPath} {
		if r := owner.do(http.MethodGet, path, nil); r.status != http.StatusServiceUnavailable || errorCode(r) != "provider_unreachable" {
			t.Errorf("GET %s with the directory down: %d %s, want 503 provider_unreachable", path, r.status, r.body)
		}
	}
	fake.FailWith("Matches", contracts.ErrSSONotConfigured)
	if r := owner.do(http.MethodGet, ssoDirectoryMatchesPath, nil); r.status != http.StatusConflict || errorCode(r) != "no_directory" {
		t.Errorf("matches with single sign-on gone meanwhile: %d %s, want 409 no_directory", r.status, r.body)
	}
	if !errors.Is(down, contracts.ErrDirectoryUnavailable) {
		t.Fatal("the fixture's error does not wrap ErrDirectoryUnavailable")
	}
}

// Both reads are policy:Owner+OwnerManagement: 401 anonymously, 403 for a
// user and for an Owner without MFA when Owners require it.
func TestSsoDirectory_OnlyAnOwnerWithMFA(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeDirectorySearch()
	h, _, ownerID := directoryOwner(t, fake, "", withEnv("OWNERS_REQUIRE_MFA", "1"))
	user := h.signIn(t, h.insertUser(t, "user@example.test", identity.RoleUserID), true)
	ownerWithoutMFA := h.signIn(t, ownerID, false)
	owner := h.signIn(t, ownerID, true)
	mustPutOIDC(t, owner, getSSO(t, owner).Version, ssoRequest("entra", map[string]any{"clientSecret": "s"}))
	for _, path := range []string{ssoDirectoryUsersPath + "?search=kari", ssoDirectoryMatchesPath} {
		if r := h.client(t).do(http.MethodGet, path, nil); r.status != http.StatusUnauthorized {
			t.Errorf("GET %s anonymously: %d, want 401", path, r.status)
		}
		for name, c := range map[string]*client{"a user": user, "an Owner without MFA": ownerWithoutMFA} {
			if r := c.do(http.MethodGet, path, nil); r.status != http.StatusForbidden {
				t.Errorf("GET %s as %s: %d, want 403", path, name, r.status)
			}
		}
		if r := owner.do(http.MethodGet, path, nil); r.status != http.StatusOK {
			t.Errorf("GET %s as an Owner with MFA: %d %s, want 200", path, r.status, r.body)
		}
	}
	if n := len(fake.Calls()); n != 2 {
		t.Errorf("the provider was asked %d times, want twice: only the Owner with MFA reaches it", n)
	}
}

// A Google account picked from the directory and announced is fulfilled by
// the person's first Google sign-in (Google Workspace design G24, on G7):
// the pick names the Directory id as the object id and the primary email as
// the principal name, and the ID token's sub — the same id — lands the
// sign-in on the announced user. The picker's account is the provider's
// answer, here a fake under the key google.
func TestSsoDirectory_AGoogleSignInLandsOnTheUserAnnouncedFromAPick(t *testing.T) {
	t.Parallel()
	const googleUserID = "104857600000000000001"
	f := newFakeGoogleOIDC(t)
	f.set("sub", googleUserID)
	fake := modtest.NewFakeDirectorySearch().WithKey("google", "Google Workspace")
	fake.SetUsers(contracts.DirectoryUser{
		ObjectID: googleUserID, PrincipalName: fakeGoogleEmail, DisplayName: "Kari Nordmann",
		Mail: fakeGoogleEmail, Enabled: true,
	})
	h := newHarness(t, append(f.options(), withDirectorySearch(fake))...)
	owner, _ := h.bootstrapOwner(t)
	id := h.seedUser(t, "kari@example.test", userPassword, identity.RoleUserID)

	r := owner.do(http.MethodGet, ssoDirectoryUsersPath+"?search=kari", nil)
	var found struct {
		Users []directoryUserBody `json:"users"`
	}
	r.json(&found)
	if r.status != http.StatusOK || len(found.Users) != 1 {
		t.Fatalf("search: %d %s", r.status, r.body)
	}
	pick := found.Users[0]
	mustAnnounce(t, owner, id, map[string]any{"objectId": pick.ObjectID, "userPrincipalName": pick.UserPrincipalName})

	c := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
	if s := oidcSession(t, c); s.User.ID != id {
		t.Errorf("signed in as %+v, want the user announced from the pick", s.User)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links WHERE issuer = $1 AND subject = $2 AND user_id = $3`, fakeGoogleAuthority, googleUserID, id); n != 1 {
		t.Error("the user is not linked by the sub")
	}
	if got := linkedFromProvider(t, h); len(got) != 1 || got[0] != `{"matchedBy":"oid"}` {
		t.Errorf("account.linked-from-provider = %v, want matched by the object id", got)
	}
}
