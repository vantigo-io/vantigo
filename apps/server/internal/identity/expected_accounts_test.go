package identity_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The announced account (Entra design D4; delivery B plan, Task 3): Staff's
// Expect and Unexpect through the real provider, and the user an arrival —
// SCIM's create or OIDC's first sign-in — makes of an announcement.

// fakeIssuer is the fake Entra provider's authority as identity links under
// it, normalised.
const fakeIssuer = fakeEntraAuthority

// announced is an announcement for the number with both keys: the user
// principal name upn and the object id oid.
func announced(number, upn, oid string) contracts.ExpectedAccount {
	return contracts.ExpectedAccount{
		EmployeeNumber: number, DisplayName: "Announced " + number, PreferredLanguage: "nb",
		ObjectID: oid, UserPrincipalName: upn,
	}
}

// mustExpect announces s as the system.
func mustExpect(t *testing.T, p contracts.AccountProvisioning, s contracts.ExpectedAccount) {
	t.Helper()
	if err := p.Expect(context.Background(), systemActor, s); err != nil {
		t.Fatalf("Expect(%s): %v", s.EmployeeNumber, err)
	}
}

// announcements is how many announcements identity holds.
func announcements(t *testing.T, h *harness) int {
	t.Helper()
	return h.count(t, `SELECT count(*) FROM identity.expected_accounts`)
}

// scimArrival is a User resource as Entra's provisioning service pushes one:
// userName the UPN, the work email, externalId, and extra over it.
func scimArrival(upn, externalID string, extra map[string]any) map[string]any {
	resource := userResource(upn, map[string]any{
		"externalId": externalID,
		"emails":     []map[string]any{{"value": upn, "type": "work", "primary": true}},
	})
	for k, v := range extra {
		resource[k] = v
	}
	return resource
}

// adoptions is every account.adopted-from-provider row, each checked to
// name no address, and the after of each.
func adoptions(t *testing.T, h *harness) []string {
	t.Helper()
	var out []string
	for _, e := range h.auditEvents(t, "account.adopted-from-provider") {
		if strings.Contains(e.Before+e.After+e.Details, "@") {
			t.Errorf("an adoption's audit names an address: %+v", e)
		}
		out = append(out, e.After)
	}
	return out
}

// An announcement is fulfilled by SCIM's create on each of its three keys,
// in their order — the enterprise employeeNumber by its key, the userName
// normalised against the user principal name, the externalId against the
// object id: the user is Staff-managed with the announced number, not
// pending, in the announced language, named by the announcement only when
// the resource names nobody; the announcement is consumed and the adoption
// audited with the key, beside scim.user.created.
func TestExpectedAccounts_SCIMFulfilsOnEachKey(t *testing.T) {
	t.Parallel()
	oid := uuid.NewString()
	cases := []struct {
		key      string
		resource map[string]any
		name     string
	}{
		{"employeeNumber", scimArrival("someone.else@example.test", "nickname", map[string]any{
			scimEnterpriseSchema: map[string]any{"employeeNumber": " a-1 "},
		}), "Announced A-1"},
		{"userName", scimArrival("Ann.Lee@Example.test", "nickname", map[string]any{"displayName": "Ann From Entra"}), "Ann From Entra"},
		{"externalId", scimArrival("someone.else@example.test", strings.ToUpper(oid), nil), "Announced A-1"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			t.Parallel()
			h := newScimHarness(t)
			p := h.accounts(t)
			mustExpect(t, p, announced("A-1", "ann.lee@example.test", oid))
			r := h.scim(t, scimToken).do(http.MethodPost, "/Users", c.resource)
			user := scimOf[scimUser](t, "the arrival", r, http.StatusCreated)
			id := h.userOf(t, user.ID)
			if got := staffColumns(t, h, id); got != "staff:A-1:false:-" {
				t.Errorf("the arrival's Staff columns = %s, want managed, numbered and not pending", got)
			}
			if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND display_name = $2 AND preferred_language = 'nb' AND NOT is_disabled`, id, c.name); n != 1 {
				t.Errorf("the arrival: %s, want named %q in nb, enabled", userState(t, h, id), c.name)
			}
			if announcements(t, h) != 0 {
				t.Error("the announcement was not consumed")
			}
			got := adoptions(t, h)
			if len(got) != 1 || !strings.Contains(got[0], `"matchedBy":"`+c.key+`"`) || !strings.Contains(got[0], `"employeeNumber":"A-1"`) || strings.Contains(got[0], "replacedUserId") {
				t.Errorf("the adoption's audit = %v, want matched by %s for A-1", got, c.key)
			}
			if n := len(h.auditEvents(t, "scim.user.created")); n != 1 {
				t.Errorf("%d scim.user.created rows, want 1", n)
			}
			ref := accountOf(t, p, id, "")
			if !ref.Managed || ref.State != contracts.AccountStateActive || ref.EmployeeNumber != "A-1" {
				t.Errorf("the provider reports %+v, want a managed active user numbered A-1", ref)
			}
			found := mustPrepare(t, p, contracts.AccountSpec{EmployeeNumber: "A-1", Kind: contracts.AccountKindEntra})
			if found.UserID != id || found.Created {
				t.Errorf("Prepare for the entra kind = %+v, want the arrival found", found)
			}
		})
	}
}

// The first key that names an announcement wins: a create whose
// employeeNumber names one announcement and whose userName names another
// adopts by the number, and leaves the other.
func TestExpectedAccounts_TheFirstKeyWins(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	p := h.accounts(t)
	mustExpect(t, p, announced("N-1", "first@example.test", uuid.NewString()))
	mustExpect(t, p, announced("N-2", "second@example.test", uuid.NewString()))
	user := scimOf[scimUser](t, "the arrival", h.scim(t, scimToken).do(http.MethodPost, "/Users", scimArrival("second@example.test", "nick", map[string]any{
		scimEnterpriseSchema: map[string]any{"employeeNumber": "N-1"},
	})), http.StatusCreated)
	if got := staffColumns(t, h, h.userOf(t, user.ID)); got != "staff:N-1:false:-" {
		t.Errorf("the arrival = %s, want N-1's", got)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.expected_accounts WHERE employee_number = 'N-2'`); n != 1 || announcements(t, h) != 1 {
		t.Error("the userName's announcement was consumed")
	}
}

// An arrival that matches no announcement is today's plain SCIM user: not
// managed, no number, the enterprise employeeNumber in the profile only.
func TestExpectedAccounts_NoMatchIsAPlainUser(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	p := h.accounts(t)
	mustExpect(t, p, announced("P-1", "planned@example.test", uuid.NewString()))
	user := scimOf[scimUser](t, "the arrival", h.scim(t, scimToken).do(http.MethodPost, "/Users", scimArrival("other@example.test", "nick", map[string]any{
		scimEnterpriseSchema: map[string]any{"employeeNumber": "P-9"},
	})), http.StatusCreated)
	id := h.userOf(t, user.ID)
	if got := staffColumns(t, h, id); got != "-:-:false:-" {
		t.Errorf("the plain user = %s, want unmanaged", got)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.scim_user_mappings WHERE user_id = $1 AND source_profile->'enterprise'->>'employeeNumber' = 'P-9'`, id); n != 1 {
		t.Error("the number is not in the profile")
	}
	if announcements(t, h) != 1 || len(adoptions(t, h)) != 0 {
		t.Error("an announcement was consumed by a user it does not name")
	}
}

// A pending local user whose kind Staff changed is replaced: the arrival
// completes that very user — no second user, the id kept, the mapping
// attached, no longer pending, the address the provider's, the open
// invitation revoked replaced — rather than creating one; the adoption's
// audit names the user replaced.
func TestExpectedAccounts_SCIMCompletesTheUserToReplace(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	employeeRole(t, h)
	p := h.accounts(t)
	local := spec("kari.local@example.test", "R-1")
	local.Kind = contracts.AccountKindLocal
	pending := mustPrepare(t, p, local)
	if _, err := p.Activate(context.Background(), systemActor, pending.UserID, contracts.ActivateOpts{Kind: contracts.AccountKindLocal}); err != nil {
		t.Fatal(err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1 AND revoked_at IS NULL`, pending.UserID); n != 1 {
		t.Fatalf("%d open invitations, want the local one", n)
	}
	expected := announced("R-1", "kari@tenant.example", uuid.NewString())
	expected.ReplaceUserID = &pending.UserID
	mustExpect(t, p, expected)
	// The announcement revoked the local invitation at once (review L5): the
	// person cannot accept it while the provider's account is on its way.
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1 AND revoked_at IS NULL`, pending.UserID); n != 0 {
		t.Errorf("%d open invitations after the announcement, want none", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1 AND revoked_reason = 'replaced'`, pending.UserID); n != 1 {
		t.Error("the announcement did not revoke the open invitation replaced")
	}
	if events := h.auditEvents(t, "account.expected"); len(events) != 1 || !strings.Contains(events[0].After, `"replacedInvitations":1`) {
		t.Errorf("account.expected = %+v, want one naming one invitation revoked", events)
	}
	users := h.count(t, `SELECT count(*) FROM identity.users`)

	user := scimOf[scimUser](t, "the arrival", h.scim(t, scimToken).do(http.MethodPost, "/Users", scimArrival("kari@tenant.example", "kari", nil)), http.StatusCreated)
	if id := h.userOf(t, user.ID); id != pending.UserID {
		t.Fatalf("the arrival maps to %s, want the replaced user %s", id, pending.UserID)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != users {
		t.Errorf("%d users, want %d: a second user was inserted", n, users)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND pending_since IS NULL AND invitations_since IS NULL
	                     AND normalized_email = 'KARI@TENANT.EXAMPLE' AND managed_by = 'staff' AND employee_number = 'R-1'`, pending.UserID); n != 1 {
		t.Errorf("the replaced user: %s", userState(t, h, pending.UserID))
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1 AND revoked_reason = 'replaced'`, pending.UserID); n != 1 {
		t.Error("the open invitation was not revoked replaced")
	}
	if got := adoptions(t, h); len(got) != 1 || !strings.Contains(got[0], `"replacedUserId":"`+pending.UserID.String()+`"`) {
		t.Errorf("the adoption's audit = %v, want the replaced user named", got)
	}
	if announcements(t, h) != 0 {
		t.Error("the announcement was not consumed")
	}
	if err := p.Unexpect(context.Background(), systemActor, "R-1"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.auditEvents(t, "account.unexpected")); n != 0 {
		t.Errorf("Unexpect after the fulfilment audited %d withdrawals, want none", n)
	}
}

// OIDC's first sign-in fulfils an announcement naming its oid under its
// issuer: the user is made managed and numbered, not pending, linked, and
// signed in; Expect filled the issuer from the single sign-on row.
func TestExpectedAccounts_OIDCFulfilsOnTheObjectID(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	p := h.accounts(t)
	mustExpect(t, p, announced("O-1", "", strings.ToUpper(fakeObjectID)))
	if n := h.count(t, `SELECT count(*) FROM identity.expected_accounts WHERE issuer = $1 AND object_id = $2`, fakeIssuer, fakeObjectID); n != 1 {
		t.Fatal("Expect did not fill the issuer from the single sign-on row, or kept the object id unnormalised")
	}
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")
	var id uuid.UUID
	if err := h.pool.QueryRow(context.Background(), `SELECT user_id FROM identity.oidc_links WHERE issuer = $1 AND subject = $2`, fakeIssuer, fakeSubject).Scan(&id); err != nil {
		t.Fatalf("no link: %v", err)
	}
	if got := staffColumns(t, h, id); got != "staff:O-1:false:-" {
		t.Errorf("the arrival = %s, want managed and numbered", got)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, id, identity.RoleUserID); n != 1 {
		t.Error("the arrival lacks the User role every OIDC user gets")
	}
	if got := adoptions(t, h); len(got) != 1 || !strings.Contains(got[0], `"matchedBy":"oid"`) {
		t.Errorf("the adoption's audit = %v, want matched by oid", got)
	}
	if announcements(t, h) != 0 {
		t.Error("the announcement was not consumed")
	}
}

// An announcement made for another issuer is not fulfilled by an oid
// signing in under this one: the sign-in is today's plain user.
func TestExpectedAccounts_OIDCUnderAnotherIssuerDoesNotAdopt(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	p := h.accounts(t)
	other := announced("O-2", "", fakeObjectID)
	other.Issuer = "https://login.microsoftonline.com/" + fakeOtherTenantID + "/v2.0"
	mustExpect(t, p, other)
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE managed_by IS NOT NULL`); n != 0 {
		t.Error("a sign-in under another issuer was adopted")
	}
	if announcements(t, h) != 1 || len(adoptions(t, h)) != 0 {
		t.Error("the announcement was consumed")
	}
}

// OIDC's first sign-in completes the pending user an announcement names to
// replace, though that user holds the email claim's address: the collision
// is the completion, not oidc_email_conflict. The user Staff keeps disabled
// until its Activate is linked and completed, and refused as the linked
// account rules refuse a disabled one; another user holding the address is
// still oidc_email_conflict.
func TestExpectedAccounts_OIDCCompletesTheUserToReplace(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	p := h.accounts(t)
	local := spec(fakeEmail, "Q-1")
	local.Kind = contracts.AccountKindLocal
	pending := mustPrepare(t, p, local)
	expected := announced("Q-1", "", fakeObjectID)
	expected.ReplaceUserID = &pending.UserID
	mustExpect(t, p, expected)
	users := h.count(t, `SELECT count(*) FROM identity.users`)

	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), oidcErrorLocation("account_locked"))
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != users {
		t.Errorf("%d users, want %d", n, users)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links WHERE user_id = $1`, pending.UserID); n != 1 {
		t.Error("the replaced user was not linked")
	}
	if got := staffColumns(t, h, pending.UserID); got != "staff:Q-1:false:staff" {
		t.Errorf("the replaced user = %s, want completed and still disabled by Staff", got)
	}
	if _, err := p.Activate(context.Background(), systemActor, pending.UserID, contracts.ActivateOpts{Kind: contracts.AccountKindExternal}); err != nil {
		t.Fatal(err)
	}
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")
	if got := adoptions(t, h); len(got) != 1 || !strings.Contains(got[0], `"matchedBy":"oid"`) || !strings.Contains(got[0], pending.UserID.String()) {
		t.Errorf("the adoption's audit = %v", got)
	}

	// Another user holding the address is no completion.
	g := newFakeOIDC(t)
	h2 := newHarness(t, g.options()...)
	p2 := h2.accounts(t)
	h2.insertUser(t, fakeEmail, identity.RoleUserID)
	replaced := mustPrepare(t, p2, func() contracts.AccountSpec {
		s := spec("pending@example.test", "Q-2")
		s.Kind = contracts.AccountKindLocal
		return s
	}())
	e2 := announced("Q-2", "", fakeObjectID)
	e2.ReplaceUserID = &replaced.UserID
	mustExpect(t, p2, e2)
	assertOIDCRedirect(t, h2.oidcSignIn(t, h2.client(t), g), oidcErrorLocation("oidc_email_conflict"))
	if announcements(t, h2) != 1 {
		t.Error("the announcement was consumed by a refused sign-in")
	}
}

// Expect refuses the number an unmanaged user holds and the one an Owner
// holds; records nothing, and answers nil, for a number a managed user who
// is not pending holds; refuses a ReplaceUserID that is not the pending
// holder; refuses an invalid spec; and changes nothing for the same
// announcement again. Its audit names the number and the keys, never an
// address.
func TestExpectedAccounts_ExpectsRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	p := h.accounts(t)
	ctx := context.Background()

	unmanaged := h.createUser(t, owner, "unmanaged@example.test", identity.RoleUser)
	h.exec(t, `UPDATE identity.users SET employee_number = 'U-1', employee_number_key = 'U-1' WHERE id = $1`, unmanaged)
	wantErr(t, "an unmanaged holder", p.Expect(ctx, systemActor, announced("u-1", "x@example.test", "")), contracts.ErrEmployeeNumberTaken)

	h.exec(t, `UPDATE identity.users SET managed_by = 'staff', employee_number = 'W-1', employee_number_key = 'W-1' WHERE id = $1`, ownerID)
	wantErr(t, "an Owner", p.Expect(ctx, systemActor, announced("W-1", "x@example.test", "")), contracts.ErrAccountPrivileged)

	active := h.createUser(t, owner, "active@example.test", identity.RoleUser)
	mustManage(t, owner, active, "F-1")
	if err := p.Expect(ctx, systemActor, announced("F-1", "x@example.test", "")); err != nil {
		t.Errorf("a fulfilled number: %v, want nil", err)
	}
	if announcements(t, h) != 0 {
		t.Error("an announcement was recorded for a fulfilled number")
	}

	pendingID := h.insertUser(t, "pending@example.test", identity.RoleUserID)
	makePending(t, h, pendingID, "G-1")
	wantErr(t, "a pending holder without ReplaceUserID", p.Expect(ctx, systemActor, announced("G-1", "x@example.test", "")), identity.ErrInvalidSpec)
	wrong := announced("G-1", "x@example.test", "")
	wrong.ReplaceUserID = &active
	wantErr(t, "a ReplaceUserID not the holder", p.Expect(ctx, systemActor, wrong), identity.ErrInvalidSpec)
	stray := announced("H-1", "x@example.test", "")
	stray.ReplaceUserID = &pendingID
	wantErr(t, "a ReplaceUserID holding another number", p.Expect(ctx, systemActor, stray), identity.ErrInvalidSpec)
	h.exec(t, `UPDATE identity.users SET password_hash = 'x' WHERE id = $1`, pendingID)
	right := announced("G-1", "x@example.test", "")
	right.ReplaceUserID = &pendingID
	wantErr(t, "a ReplaceUserID with a credential", p.Expect(ctx, systemActor, right), identity.ErrInvalidSpec)
	h.exec(t, `UPDATE identity.users SET password_hash = NULL WHERE id = $1`, pendingID)
	mustExpect(t, p, right)

	for name, s := range map[string]contracts.ExpectedAccount{
		"no key":           {EmployeeNumber: "K-1", DisplayName: "K"},
		"a bad number":     announced("K 1", "x@example.test", ""),
		"no display name":  {EmployeeNumber: "K-1", UserPrincipalName: "x@example.test"},
		"a bad language":   {EmployeeNumber: "K-1", DisplayName: "K", PreferredLanguage: "de", UserPrincipalName: "x@example.test"},
		"a bad UPN":        {EmployeeNumber: "K-1", DisplayName: "K", UserPrincipalName: "not an address"},
		"a long object id": {EmployeeNumber: "K-1", DisplayName: "K", ObjectID: strings.Repeat("o", 201)},
		"a bad issuer":     {EmployeeNumber: "K-1", DisplayName: "K", ObjectID: "o", Issuer: "not a url"},
	} {
		wantErr(t, name, p.Expect(ctx, systemActor, s), identity.ErrInvalidSpec)
	}
	wantErr(t, "an invalid actor", p.Expect(ctx, contracts.Actor{}, announced("K-1", "x@example.test", "")), contracts.ErrInvalidActor)

	mustExpect(t, p, announced("K-1", "Kari@Example.test", "o-1"))
	mustExpect(t, p, announced("K-1", "kari@example.test", "o-1"))
	if n := h.count(t, `SELECT count(*) FROM identity.expected_accounts WHERE employee_number_key = 'K-1' AND user_principal_name = 'KARI@EXAMPLE.TEST'`); n != 1 {
		t.Error("the UPN is not stored normalised")
	}
	events := h.auditEvents(t, "account.expected")
	if len(events) != 2 {
		t.Fatalf("%d account.expected rows, want 2 (G-1 and K-1 once)", len(events))
	}
	for _, e := range events {
		if strings.Contains(e.Before+e.After, "@") {
			t.Errorf("an announcement's audit names an address: %s", e.After)
		}
	}
	if !strings.Contains(events[0].After, `"replaceUserId":"`+pendingID.String()+`"`) ||
		!strings.Contains(events[1].After, `"keys":["objectId","userPrincipalName"]`) || !strings.Contains(events[1].After, `"employeeNumber":"K-1"`) {
		t.Errorf("the audits: %s / %s", events[0].After, events[1].After)
	}

	if err := p.Unexpect(ctx, systemActor, "k-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Unexpect(ctx, systemActor, "k-1"); err != nil {
		t.Errorf("a second Unexpect: %v, want nil", err)
	}
	if events := h.auditEvents(t, "account.unexpected"); len(events) != 1 || !strings.Contains(events[0].Before, `"employeeNumber":"K-1"`) {
		t.Errorf("account.unexpected = %+v, want one withdrawal of K-1", events)
	}
	wantErr(t, "Unexpect by an invalid actor", p.Unexpect(ctx, contracts.Actor{}, "G-1"), contracts.ErrInvalidActor)
}

// The kind on the call: for entra and external, Prepare never creates
// (ErrAccountNotFound) and Update writes the language and the number only;
// for entra, Activate gives the provisioning roles once — to a user who
// arrived enabled — and never again, so an Owner's removal of one holds;
// for external, Activate gives none; for local, the roles' giving is
// recorded too.
func TestExpectedAccounts_TheKindOnTheCall(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	role := employeeRole(t, h)
	p := h.accounts(t)
	ctx := context.Background()

	for _, kind := range []string{contracts.AccountKindEntra, contracts.AccountKindExternal} {
		s := spec("nobody@example.test", "Z-1")
		s.Kind = kind
		_, err := p.Prepare(ctx, systemActor, s)
		wantErr(t, kind+" Prepare of a number nobody holds", err, contracts.ErrAccountNotFound)
	}
	bad := spec("nobody@example.test", "Z-1")
	bad.Kind = "magic"
	_, err := p.Prepare(ctx, systemActor, bad)
	wantErr(t, "an unknown kind", err, identity.ErrInvalidSpec)

	mustExpect(t, p, announced("E-1", "eva@example.test", uuid.NewString()))
	user := scimOf[scimUser](t, "the arrival", h.scim(t, scimToken).do(http.MethodPost, "/Users", scimArrival("eva@example.test", "eva", nil)), http.StatusCreated)
	id := h.userOf(t, user.ID)
	roles := func() int {
		return h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, id, role)
	}
	if _, err := p.Activate(ctx, systemActor, id, contracts.ActivateOpts{Kind: contracts.AccountKindExternal}); err != nil || roles() != 0 {
		t.Fatalf("external Activate: %v, %d roles, want none given", err, roles())
	}
	if _, err := p.Activate(ctx, systemActor, id, contracts.ActivateOpts{Kind: contracts.AccountKindEntra}); err != nil || roles() != 1 {
		t.Fatalf("entra Activate: %v, %d roles, want the set given", err, roles())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND provisioning_roles_at IS NOT NULL AND NOT is_disabled`, id); n != 1 {
		t.Error("provisioning_roles_at was not recorded")
	}
	if n := len(h.auditEvents(t, "user.staff-activated")); n != 1 {
		t.Errorf("%d user.staff-activated rows, want 1", n)
	}
	h.exec(t, `DELETE FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, id, role)
	if _, err := p.Activate(ctx, systemActor, id, contracts.ActivateOpts{Kind: contracts.AccountKindEntra}); err != nil || roles() != 0 {
		t.Errorf("a second entra Activate: %v, %d roles, want the Owner's removal kept", err, roles())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1`, id); n != 0 {
		t.Error("an entra Activate invited")
	}

	if _, err := p.Update(ctx, systemActor, id, contracts.AccountSpec{
		Email: "new@example.test", DisplayName: "New Name", PreferredLanguage: "en", EmployeeNumber: "E-2", Kind: contracts.AccountKindEntra,
	}); err != nil {
		t.Fatal(err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = 'eva@example.test' AND display_name = 'Announced E-1'
	                     AND preferred_language = 'en' AND employee_number = 'E-2'`, id); n != 1 {
		t.Errorf("entra Update: %s, want the language and the number only", userState(t, h, id))
	}

	// The local kind, even in this federated installation, creates, and
	// records the roles' giving.
	local := spec("local@example.test", "L-1")
	local.Kind = contracts.AccountKindLocal
	created := mustPrepare(t, p, local)
	if !created.Created {
		t.Fatalf("local Prepare = %+v, want a create", created)
	}
	if _, err := p.Activate(ctx, systemActor, created.UserID, contracts.ActivateOpts{Kind: contracts.AccountKindLocal}); err != nil {
		t.Fatal(err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND provisioning_roles_at IS NOT NULL`, created.UserID); n != 1 {
		t.Error("a local Activate did not record the roles' giving")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1`, created.UserID); n != 1 {
		t.Error("a local Activate in a federated installation did not invite")
	}
}

// An entra Activate with no provisioning roles named is
// ErrEmployeeRoleMissing, and gives nothing, so the next call tries again.
func TestExpectedAccounts_AnEntraActivateNeedsTheRoles(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	p := h.accounts(t)
	mustExpect(t, p, announced("M-1", "mia@example.test", uuid.NewString()))
	user := scimOf[scimUser](t, "the arrival", h.scim(t, scimToken).do(http.MethodPost, "/Users", scimArrival("mia@example.test", "mia", nil)), http.StatusCreated)
	id := h.userOf(t, user.ID)
	_, err := p.Activate(context.Background(), systemActor, id, contracts.ActivateOpts{Kind: contracts.AccountKindEntra})
	wantErr(t, "no roles named", err, contracts.ErrEmployeeRoleMissing)
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND provisioning_roles_at IS NULL`, id); n != 1 {
		t.Error("a refused Activate recorded the roles' giving")
	}
	employeeRole(t, h)
	if _, err := p.Activate(context.Background(), systemActor, id, contracts.ActivateOpts{Kind: contracts.AccountKindEntra}); err != nil {
		t.Errorf("once named: %v", err)
	}
}
