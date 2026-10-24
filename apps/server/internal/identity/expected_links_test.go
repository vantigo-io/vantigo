package identity_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The announced link (Entra phase 2 design L1–L4; delivery A): an Owner
// announces the directory account an existing user holds, and the user's
// next arrival — their first OIDC sign-in through the provider, or the
// provider's SCIM push — attaches to them.

// ssoLinkBody is OwnerUserResponse.ssoLink as the tests read it.
type ssoLinkBody struct {
	State             string  `json:"state"`
	UserPrincipalName *string `json:"userPrincipalName"`
	ObjectID          *string `json:"objectId"`
	ExpectedAt        *string `json:"expectedAt"`
}

// ssoLinkUser is the part of OwnerUserResponse the tests here read.
type ssoLinkUser struct {
	ID         uuid.UUID    `json:"id"`
	Email      *string      `json:"email"`
	SsoEnabled bool         `json:"ssoEnabled"`
	SsoLink    *ssoLinkBody `json:"ssoLink"`
}

// announce is POST /owner/users/{id}/sso-link as owner with body.
func announce(owner *client, id uuid.UUID, body map[string]any) *resp {
	return owner.do(http.MethodPost, userPath(id, "/sso-link"), body)
}

// mustAnnounce announces body for id, which must answer 200, and returns
// the user.
func mustAnnounce(t *testing.T, owner *client, id uuid.UUID, body map[string]any) ssoLinkUser {
	t.Helper()
	r := announce(owner, id, body)
	if r.status != http.StatusOK {
		t.Fatalf("announce %s: status %d body %s", id, r.status, r.body)
	}
	var u ssoLinkUser
	r.json(&u)
	return u
}

// disconnect is DELETE /owner/users/{id}/sso-link as owner.
func disconnect(owner *client, id uuid.UUID) *resp {
	return owner.do(http.MethodDelete, userPath(id, "/sso-link"), nil)
}

// listedSsoLink is the user as the Owner's list shows them.
func listedSsoLink(t *testing.T, owner *client, id uuid.UUID) ssoLinkUser {
	t.Helper()
	r := owner.do(http.MethodGet, ownerUsersPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("list users: status %d body %s", r.status, r.body)
	}
	var list []ssoLinkUser
	r.json(&list)
	for _, u := range list {
		if u.ID == id {
			return u
		}
	}
	t.Fatalf("user %s is not listed", id)
	return ssoLinkUser{}
}

// linkState is the state the list reports for id, with the announced
// principal name beside it when expected.
func linkState(t *testing.T, owner *client, id uuid.UUID) string {
	t.Helper()
	u := listedSsoLink(t, owner, id)
	if u.SsoLink == nil {
		return "absent"
	}
	s := u.SsoLink.State
	if u.SsoLink.UserPrincipalName != nil {
		s += ":" + *u.SsoLink.UserPrincipalName
	}
	if u.SsoLink.ObjectID != nil {
		s += ":" + *u.SsoLink.ObjectID
	}
	return s
}

// announcedLinks is how many announced links identity holds.
func announcedLinks(t *testing.T, h *harness) int {
	t.Helper()
	return h.count(t, `SELECT count(*) FROM identity.expected_links`)
}

// linkedFromProvider is every account.linked-from-provider row's after,
// each checked to name no address.
func linkedFromProvider(t *testing.T, h *harness) []string {
	t.Helper()
	var out []string
	for _, e := range h.auditEvents(t, "account.linked-from-provider") {
		if strings.Contains(e.Before+e.After+e.Details, "@") {
			t.Errorf("a link's audit names an address: %+v", e)
		}
		out = append(out, e.After)
	}
	return out
}

// An Owner announces an existing user's account by its principal name and
// its object id; the list reports the user expected with the announced
// keys (the name normalised, the id in lower-case dashed form), the audit
// names the keys and never the address, the same announcement again
// changes nothing, and a change replaces it whole.
func TestExpectedLinks_AnnounceAndList(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, ownerID := h.bootstrapOwner(t)
	id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
	if got := linkState(t, owner, id); got != "none" {
		t.Fatalf("before the announcement: %s, want none", got)
	}

	u := mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": " Kari.Nordmann@Tenant.Example ", "objectId": strings.ToUpper(fakeObjectID)})
	if u.SsoLink == nil || u.SsoLink.State != "expected" || deref(u.SsoLink.UserPrincipalName) != "kari.nordmann@tenant.example" ||
		deref(u.SsoLink.ObjectID) != fakeObjectID || u.SsoLink.ExpectedAt == nil || u.SsoEnabled {
		t.Errorf("announced: %+v", u.SsoLink)
	}
	if got := linkState(t, owner, id); got != "expected:kari.nordmann@tenant.example:"+fakeObjectID {
		t.Errorf("listed: %s", got)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.expected_links WHERE user_id = $1 AND issuer = $2 AND created_by_user_id = $3`, id, fakeIssuer, ownerID); n != 1 {
		t.Error("the announcement is not under the configured issuer by the Owner")
	}
	events := h.auditEvents(t, "account.link-expected")
	if len(events) != 1 || *events[0].Actor != ownerID || *events[0].TargetUser != id ||
		events[0].After != `{"keys":["objectId","userPrincipalName"]}` || strings.Contains(events[0].After, "@") {
		t.Errorf("account.link-expected = %+v", events)
	}

	// The same again changes nothing; a change replaces it.
	mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": "kari.nordmann@tenant.example", "objectId": fakeObjectID})
	if n := len(h.auditEvents(t, "account.link-expected")); n != 1 {
		t.Errorf("%d account.link-expected rows after the same announcement, want 1", n)
	}
	mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": "kari.n@tenant.example"})
	if got := linkState(t, owner, id); got != "expected:kari.n@tenant.example" {
		t.Errorf("replaced: %s", got)
	}
	if n := len(h.auditEvents(t, "account.link-expected")); n != 2 || announcedLinks(t, h) != 1 {
		t.Errorf("after the replacement: %d audit rows, %d announcements", n, announcedLinks(t, h))
	}
}

// Each refusal, in the handler's order.
func TestExpectedLinks_AnnounceRefusals(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, ownerID := h.bootstrapOwner(t)
	id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
	other := h.createUser(t, owner, "ola@example.test", identity.RoleUser)
	upn := map[string]any{"userPrincipalName": "kari@tenant.example"}

	refusals := []struct {
		name   string
		id     uuid.UUID
		body   map[string]any
		status int
		code   string
		field  string
	}{
		{"neither key", id, map[string]any{}, http.StatusBadRequest, "invalid_request", "userPrincipalName"},
		{"no body", id, nil, http.StatusBadRequest, "invalid_request", "userPrincipalName"},
		{"not an address", id, map[string]any{"userPrincipalName": "kari"}, http.StatusBadRequest, "invalid_request", "userPrincipalName"},
		{"a long object id", id, map[string]any{"objectId": strings.Repeat("x", 201)}, http.StatusBadRequest, "invalid_request", "objectId"},
		{"the caller", ownerID, upn, http.StatusBadRequest, "invalid_request", ""},
		{"an unknown user", uuid.New(), upn, http.StatusNotFound, "", ""},
	}
	for _, c := range refusals {
		r := announce(owner, c.id, c.body)
		if r.status != c.status || (c.code != "" && r.code() != c.code) || (c.field != "" && !strings.Contains(string(r.body), `"`+c.field+`"`)) {
			t.Errorf("%s: status %d code %q body %s, want %d %s on %s", c.name, r.status, r.code(), r.body, c.status, c.code, c.field)
		}
	}

	// A pending user's way in is their invitation.
	h.exec(t, `UPDATE identity.users SET pending_since = $2 WHERE id = $1`, other, h.now())
	if r := announce(owner, other, upn); r.status != http.StatusConflict || r.code() != "account_pending" {
		t.Errorf("a pending user: status %d code %q, want 409 account_pending", r.status, r.code())
	}
	h.exec(t, `UPDATE identity.users SET pending_since = NULL WHERE id = $1`, other)

	// A user linked already.
	insertOIDCLink(t, h, fakeEntraAuthority, "linked-subject", other)
	if r := announce(owner, other, upn); r.status != http.StatusConflict || r.code() != "account_linked" {
		t.Errorf("a linked user: status %d code %q, want 409 account_linked", r.status, r.code())
	}
	h.exec(t, `DELETE FROM identity.oidc_links WHERE user_id = $1`, other)

	// A key another user's announcement names, by either key, and one an
	// announced account (Staff's) names.
	mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": "kari@tenant.example", "objectId": fakeObjectID})
	for name, body := range map[string]map[string]any{
		"the principal name": {"userPrincipalName": "KARI@tenant.example"},
		"the object id":      {"objectId": strings.ToUpper(fakeObjectID)},
	} {
		if r := announce(owner, other, body); r.status != http.StatusConflict || r.code() != "sso_link_taken" {
			t.Errorf("%s of another user's announcement: status %d code %q, want 409 sso_link_taken", name, r.status, r.code())
		}
	}
	mustExpect(t, h.accounts(t), announced("S-1", "staff.person@tenant.example", uuid.NewString()))
	if r := announce(owner, other, map[string]any{"userPrincipalName": "staff.person@tenant.example"}); r.status != http.StatusConflict || r.code() != "sso_link_taken" {
		t.Errorf("an announced account's name: status %d code %q, want 409 sso_link_taken", r.status, r.code())
	}
	if announcedLinks(t, h) != 1 {
		t.Errorf("%d announcements after the refusals, want the one", announcedLinks(t, h))
	}

	// A disabled user may be announced; an Owner too.
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, other)
	mustAnnounce(t, owner, other, map[string]any{"userPrincipalName": "ola@tenant.example"})
	second := h.createUser(t, owner, "second.owner@example.test", identity.RoleOwner)
	mustAnnounce(t, owner, second, map[string]any{"userPrincipalName": "second.owner@tenant.example"})
}

// While OIDC is off there is no issuer to announce under, and the list
// reports no link state at all.
func TestExpectedLinks_NeedOIDC(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
	if r := announce(owner, id, map[string]any{"userPrincipalName": "kari@tenant.example"}); r.status != http.StatusConflict || r.code() != "sso_not_configured" {
		t.Errorf("announce with OIDC off: status %d code %q, want 409 sso_not_configured", r.status, r.code())
	}
	if r := disconnect(owner, id); r.status != http.StatusConflict || r.code() != "sso_not_configured" {
		t.Errorf("disconnect with OIDC off: status %d code %q, want 409 sso_not_configured", r.status, r.code())
	}
	if got := linkState(t, owner, id); got != "absent" {
		t.Errorf("listed with OIDC off: %s, want no ssoLink", got)
	}
}

// The user's first sign-in through the provider fulfils the announcement:
// matched on the token's oid under the issuer, the existing user is linked
// and signed in — their address kept though the claim says another, and
// though that claim's address belongs to a third user, which would have
// been oidc_email_conflict — the announcement consumed and the link
// audited with matchedBy oid. A second sign-in is the link's.
func TestExpectedLinks_OIDCFulfilsOnTheObjectID(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	f.set("email", "kari.n@tenant.example")
	h := newHarness(t, f.options()...)
	owner, _ := h.bootstrapOwner(t)
	id := h.seedUser(t, "kari@example.test", userPassword, identity.RoleUserID)
	h.seedUser(t, "kari.n@tenant.example", userPassword, identity.RoleUserID)
	mustAnnounce(t, owner, id, map[string]any{"objectId": fakeObjectID})

	c := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
	if s := oidcSession(t, c); s.User.ID != id || deref(s.User.Email) != "kari@example.test" {
		t.Errorf("signed in as %+v, want the announced user with their own address", s.User)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links WHERE issuer = $1 AND subject = $2 AND user_id = $3`, fakeIssuer, fakeSubject, id); n != 1 {
		t.Error("the user is not linked")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 3 {
		t.Errorf("%d users, want the three that existed", n)
	}
	if announcedLinks(t, h) != 0 {
		t.Error("the announcement was not consumed")
	}
	if got := linkedFromProvider(t, h); len(got) != 1 || got[0] != `{"matchedBy":"oid"}` {
		t.Errorf("account.linked-from-provider = %v", got)
	}
	if got := linkState(t, owner, id); got != "linked" {
		t.Errorf("listed after the sign-in: %s", got)
	}
	h.login(t, "kari@example.test", userPassword) // the password stays
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != 3 {
		t.Errorf("%d sessions, want 3", n)
	}
}

// A disabled user is linked but not signed in; an announcement by the
// principal name alone is fulfilled by the token's preferred_username, or
// by its verified email when the provider sends no principal name; an
// announcement under another issuer, or for a user linked meanwhile,
// fulfils nothing and the arrival is what it was; an announced link
// outranks an announced account for the same oid.
func TestExpectedLinks_OIDCEdges(t *testing.T) {
	t.Parallel()
	t.Run("the principal name alone", func(t *testing.T) {
		t.Parallel()
		for claim, value := range map[string]string{"preferred_username": "Kari.N@Tenant.Example", "email": "kari.n@tenant.example"} {
			t.Run(claim, func(t *testing.T) {
				t.Parallel()
				f := newFakeOIDC(t)
				f.set(claim, value)
				if claim == "preferred_username" {
					f.set("email", "someone.else@tenant.example")
				}
				h := newHarness(t, f.options()...)
				owner, _ := h.bootstrapOwner(t)
				id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
				mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": "kari.n@tenant.example"})
				c := h.client(t)
				assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
				if oidcSession(t, c).User.ID != id || announcedLinks(t, h) != 0 {
					t.Error("the announced user was not linked and signed in")
				}
				if got := linkedFromProvider(t, h); len(got) != 1 || got[0] != `{"matchedBy":"principalName"}` {
					t.Errorf("account.linked-from-provider = %v", got)
				}
			})
		}
	})
	t.Run("a disabled user is linked and account_locked", func(t *testing.T) {
		t.Parallel()
		f := newFakeOIDC(t)
		h := newHarness(t, f.options()...)
		owner, _ := h.bootstrapOwner(t)
		id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
		h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, id)
		mustAnnounce(t, owner, id, map[string]any{"objectId": fakeObjectID})
		assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), oidcErrorLocation("account_locked"))
		if n := h.count(t, `SELECT count(*) FROM identity.oidc_links WHERE user_id = $1`, id); n != 1 || announcedLinks(t, h) != 0 {
			t.Error("the disabled user was not linked")
		}
	})
	t.Run("another issuer fulfils nothing", func(t *testing.T) {
		t.Parallel()
		f := newFakeOIDC(t)
		f.set("email", "kari@example.test")
		h := newHarness(t, f.options()...)
		owner, _ := h.bootstrapOwner(t)
		id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
		mustAnnounce(t, owner, id, map[string]any{"objectId": fakeObjectID})
		h.exec(t, `UPDATE identity.expected_links SET issuer = $2 WHERE user_id = $1`, id, "https://login.microsoftonline.com/"+fakeOtherTenantID+"/v2.0")
		if got := linkState(t, owner, id); got != "none" {
			t.Errorf("listed under another issuer: %s, want none", got)
		}
		assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), oidcErrorLocation("oidc_email_conflict"))
		if announcedLinks(t, h) != 1 || h.count(t, `SELECT count(*) FROM identity.oidc_links`) != 0 {
			t.Error("an announcement under another issuer was fulfilled")
		}
	})
	t.Run("a user linked meanwhile fulfils nothing", func(t *testing.T) {
		t.Parallel()
		f := newFakeOIDC(t)
		f.set("email", "kari@example.test")
		h := newHarness(t, f.options()...)
		owner, _ := h.bootstrapOwner(t)
		id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
		mustAnnounce(t, owner, id, map[string]any{"objectId": fakeObjectID})
		insertOIDCLink(t, h, fakeEntraAuthority, "another-subject", id)
		assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), oidcErrorLocation("oidc_email_conflict"))
		if announcedLinks(t, h) != 1 || h.count(t, `SELECT count(*) FROM identity.oidc_links`) != 1 {
			t.Error("a linked user's announcement was fulfilled")
		}
	})
	t.Run("the link outranks the account", func(t *testing.T) {
		t.Parallel()
		f := newFakeOIDC(t)
		h := newHarness(t, f.options()...)
		owner, _ := h.bootstrapOwner(t)
		id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
		mustExpect(t, h.accounts(t), announced("O-1", "", fakeObjectID))
		// Staff's announcement already names the oid: the Owner's is refused
		// — so the race is staged on the table directly.
		h.exec(t, `INSERT INTO identity.expected_links (user_id, issuer, object_id, created_at, created_by_user_id) VALUES ($1, $2, $3, $4, $1)`, id, fakeIssuer, fakeObjectID, h.now())
		c := h.client(t)
		assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
		if oidcSession(t, c).User.ID != id {
			t.Error("the announced account won over the announced link")
		}
		if announcements(t, h) != 1 || len(adoptions(t, h)) != 0 || h.count(t, `SELECT count(*) FROM identity.users`) != 2 {
			t.Error("the announced account was consumed or a user created")
		}
	})
}

// The provider's SCIM push fulfils the announcement by userName against the
// principal name, then by externalId against the object id: the resource is
// mapped to the existing user — no second user — whose address becomes the
// work email, with the display name kept; the announcement is consumed and
// the link audited with the key, beside scim.user.created.
func TestExpectedLinks_SCIMFulfilsOnEachKey(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		key      string
		announce map[string]any
		resource map[string]any
	}{
		{"userName", map[string]any{"userPrincipalName": "kari@tenant.example"}, scimArrival("Kari@Tenant.Example", "nickname", map[string]any{"displayName": "Kari From Entra"})},
		{"externalId", map[string]any{"objectId": fakeObjectID}, scimArrival("kari@tenant.example", strings.ToUpper(fakeObjectID), nil)},
	} {
		t.Run(c.key, func(t *testing.T) {
			t.Parallel()
			f := newFakeOIDC(t)
			h := newScimHarness(t, f.options()...)
			owner, _ := h.bootstrapOwner(t)
			id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
			mustAnnounce(t, owner, id, c.announce)
			user := scimOf[scimUser](t, "the arrival", h.scim(t, scimToken).do(http.MethodPost, "/Users", c.resource), http.StatusCreated)
			if got := h.userOf(t, user.ID); got != id {
				t.Fatalf("the resource maps to %s, want the announced user %s", got, id)
			}
			if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND lower(email) = 'kari@tenant.example' AND display_name = 'kari@example.test'`, id); n != 1 {
				t.Errorf("the user after the arrival: %s, want the work email and the name kept", userState(t, h, id))
			}
			if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 2 {
				t.Errorf("%d users, want the Owner and the one", n)
			}
			if announcedLinks(t, h) != 0 {
				t.Error("the announcement was not consumed")
			}
			if got := linkedFromProvider(t, h); len(got) != 1 || got[0] != `{"matchedBy":"`+c.key+`"}` {
				t.Errorf("account.linked-from-provider = %v, want matched by %s", got, c.key)
			}
			if n := len(h.auditEvents(t, "scim.user.created")); n != 1 {
				t.Errorf("%d scim.user.created rows, want 1", n)
			}
		})
	}
}

// A work email another user holds is DuplicateEmail; none keeps the
// address; a user mapped already is no match, and the arrival is a plain
// create refused DuplicateEmail as before.
func TestExpectedLinks_SCIMEdges(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newScimHarness(t, f.options()...)
	owner, _ := h.bootstrapOwner(t)
	id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
	h.createUser(t, owner, "taken@tenant.example", identity.RoleUser)
	mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": "kari@tenant.example"})
	scim := h.scim(t, scimToken)

	r := scim.do(http.MethodPost, "/Users", scimArrival("kari@tenant.example", "nick", map[string]any{
		"emails": []map[string]any{{"value": "taken@tenant.example", "type": "work", "primary": true}},
	}))
	if r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "DuplicateEmail") {
		t.Errorf("another user's work email: status %d body %s, want 400 DuplicateEmail", r.status, r.body)
	}
	if announcedLinks(t, h) != 1 {
		t.Error("a refused arrival consumed the announcement")
	}

	resource := userResource("kari@tenant.example", nil)
	delete(resource, "emails")
	user := scimOf[scimUser](t, "an arrival without an email", scim.do(http.MethodPost, "/Users", resource), http.StatusCreated)
	if h.userOf(t, user.ID) != id || h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = 'kari@example.test'`, id) != 1 {
		t.Error("an arrival without an email did not map to the user with their address kept")
	}

	// Mapped already: the next announcement's arrival is a plain create.
	mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": "kari.two@tenant.example"})
	r = scim.do(http.MethodPost, "/Users", scimArrival("kari.two@tenant.example", "nick-2", nil))
	if r.status != http.StatusCreated || h.userOf(t, scimOf[scimUser](t, "a plain create", r, http.StatusCreated).ID) == id {
		t.Errorf("a mapped user's announcement: status %d body %s, want a plain user created", r.status, r.body)
	}
	if announcedLinks(t, h) != 1 {
		t.Error("a mapped user's announcement was consumed")
	}
}

// An Owner disconnects a user: the announcement withdrawn, the links under
// the configured issuer removed (one under another issuer kept, as it
// counts for nothing), the SCIM mapping untouched, each audited; nothing to
// remove is 200 and silent; the caller's own account is refused.
func TestExpectedLinks_Disconnect(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newScimHarness(t, f.options()...)
	owner, ownerID := h.bootstrapOwner(t)
	id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
	other := "https://login.microsoftonline.com/" + fakeOtherTenantID + "/v2.0"

	mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": "kari@tenant.example"})
	r := disconnect(owner, id)
	var u ssoLinkUser
	r.json(&u)
	if r.status != http.StatusOK || u.SsoLink == nil || u.SsoLink.State != "none" || announcedLinks(t, h) != 0 {
		t.Errorf("withdrawing: status %d body %s", r.status, r.body)
	}
	if events := h.auditEvents(t, "account.link-unexpected"); len(events) != 1 || *events[0].Actor != ownerID || *events[0].TargetUser != id {
		t.Errorf("account.link-unexpected = %+v", events)
	}

	// Announced, pushed (mapped), and then linked under both issuers.
	mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": "kari@tenant.example"})
	scimOf[scimUser](t, "the push", h.scim(t, scimToken).do(http.MethodPost, "/Users", scimArrival("kari@tenant.example", "nick", nil)), http.StatusCreated)
	insertOIDCLink(t, h, fakeEntraAuthority, "sub-1", id)
	insertOIDCLink(t, h, other, "sub-old", id)
	if got := linkState(t, owner, id); got != "linked" {
		t.Fatalf("before the disconnect: %s", got)
	}
	r = disconnect(owner, id)
	r.json(&u)
	if r.status != http.StatusOK || u.SsoLink.State != "none" || u.SsoEnabled {
		t.Errorf("disconnecting: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links WHERE user_id = $1`, id); n != 1 {
		t.Errorf("%d links left, want the other issuer's", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.scim_user_mappings WHERE user_id = $1`, id); n != 1 {
		t.Error("the SCIM mapping went")
	}
	if events := h.auditEvents(t, "account.unlinked"); len(events) != 1 || events[0].Before != `{"links":1}` || *events[0].TargetUser != id {
		t.Errorf("account.unlinked = %+v", events)
	}

	if r := disconnect(owner, id); r.status != http.StatusOK || len(h.auditEvents(t, "account.unlinked")) != 1 {
		t.Errorf("nothing to remove: status %d, or audited again", r.status)
	}
	if r := disconnect(owner, ownerID); r.status != http.StatusBadRequest {
		t.Errorf("the caller: status %d, want 400", r.status)
	}
	if r := disconnect(owner, uuid.New()); r.status != http.StatusNotFound {
		t.Errorf("an unknown user: status %d, want 404", r.status)
	}
}

// An announcement not yet fulfilled counts as a link for the authority
// change and the turn-off: both need the Owner's confirmation, and the
// confirmed change leaves the announcement behind, listed as none.
func TestExpectedLinks_CountForTheConfirmation(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, _ := h.bootstrapOwner(t)
	id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)
	mustAnnounce(t, owner, id, map[string]any{"userPrincipalName": "kari@tenant.example"})
	got := getSSO(t, owner)

	other := "https://login.microsoftonline.com/" + fakeOtherTenantID + "/v2.0"
	change := ssoRequest("entra", map[string]any{"authority": other, "clientSecret": fakeClientSecret})
	if r := putOIDC(owner, got.Version, change); r.status != http.StatusConflict || r.code() != "oidc_links_exist" {
		t.Errorf("an authority change with an announcement: status %d code %q, want 409 oidc_links_exist", r.status, r.code())
	}
	if r := owner.do(http.MethodDelete, ssoOIDCPath, nil); r.status != http.StatusConflict || r.code() != "oidc_links_exist" {
		t.Errorf("a turn-off with an announcement: status %d code %q, want 409 oidc_links_exist", r.status, r.code())
	}
	change["confirmIssuerChange"] = true
	mustPutOIDC(t, owner, got.Version, change)
	if announcedLinks(t, h) != 1 {
		t.Error("the confirmed change removed the announcement")
	}
	if got := linkState(t, owner, id); got != "none" {
		t.Errorf("listed after the change: %s, want none", got)
	}
}

var _ = context.Background
