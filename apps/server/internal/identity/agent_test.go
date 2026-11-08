package identity_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The agent user and the agent grant are identity's side of the virtual
// employees design V5 and V6. These tests run them against the real tables.

func agentAccounts(h *harness) contracts.AgentAccounts {
	return identity.Module(h.access).AgentAccounts(h.deps)
}

func agentGrants(h *harness) contracts.AgentGrants {
	return identity.Module(h.access).AgentGrants(h.deps)
}

// newAgent makes an agent user through the slot, failing the test when refused.
func newAgent(t *testing.T, h *harness, name string) uuid.UUID {
	t.Helper()
	id, err := agentAccounts(h).Create(context.Background(), name)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return id
}

// agentRule is a rule an agent holding the "Front desk" role of newAgentWithRole
// meets while its grant stands.
const agentRule = "permission:customers:view"

// newAgentWithRole makes an agent holding a custom role with customers:view.
func newAgentWithRole(t *testing.T, h *harness, name string) uuid.UUID {
	t.Helper()
	id := newAgent(t, h, name)
	role := h.insertRole(t, "Role of "+id.String(), "customers:view")
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, role)
	return id
}

func issueAgent(t *testing.T, h *harness, agentUser uuid.UUID) uuid.UUID {
	t.Helper()
	id, err := agentGrants(h).Issue(context.Background(), contracts.IssueAgentGrant{AgentUserID: agentUser, AgentID: uuid.New(), RunID: uuid.New()})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return id
}

// checkAgent runs Check for rule on an in-process request carrying the agent
// grant, with cookie when it is not "" (which Check must ignore).
func checkAgent(t *testing.T, h *harness, grantID uuid.UUID, cookie, rule string) (contracts.Principal, outcome) {
	t.Helper()
	r, err := contracts.ParseRule(rule)
	if err != nil {
		t.Fatalf("rule %q: %v", rule, err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: cookie})
	}
	req = req.WithContext(contracts.WithGrant(req.Context(), contracts.GrantRef{Kind: contracts.GrantAgent, ID: grantID}))
	p, err := h.access.Check(req, r)
	switch {
	case err == nil:
		return p, allowed
	case errors.Is(err, contracts.ErrUnauthenticated):
		return p, u401
	case errors.Is(err, contracts.ErrForbidden):
		return p, f403
	default:
		t.Fatalf("Check(%s): unexpected error %v", rule, err)
		return p, 0
	}
}

// wantViolation fails unless err is the database's refusal of an agent.
func wantViolation(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "SQLSTATE 23514") {
		t.Errorf("%s: err = %v, want a check violation", what, err)
	}
}

func TestAgentAccounts_CreateMakesAnAgentWithoutCredentials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := newAgent(t, h, "  Hanna  ")

	var kind, email, name string
	var password, totp, pending any
	if err := h.pool.QueryRow(context.Background(),
		`SELECT kind, email, display_name, password_hash, totp_secret, pending_since FROM identity.users WHERE id = $1`, id).
		Scan(&kind, &email, &name, &password, &totp, &pending); err != nil {
		t.Fatal(err)
	}
	if kind != "agent" || email != "agent-"+id.String()+"@agents.invalid" || name != "Hanna" {
		t.Errorf("agent = %q %q %q, want kind agent, agent-<uuid>@agents.invalid, Hanna", kind, email, name)
	}
	if password != nil || totp != nil || pending != nil {
		t.Errorf("credentials = %v %v, pending_since = %v, want none", password, totp, pending)
	}

	entry, err := newUserDirectory(t, h).User(context.Background(), id)
	if err != nil || entry == nil || entry.Kind != contracts.UserKindAgent || !entry.Active {
		t.Errorf("User = %+v, %v, want an active agent", entry, err)
	}

	// An agent may not be given a password, even by a statement that skips
	// identity's code.
	_, err = h.pool.Exec(context.Background(), `UPDATE identity.users SET password_hash = 'x' WHERE id = $1`, id)
	wantViolation(t, "a password on an agent", err)
}

func TestAgentAccounts_RenameDisableEnableAndAvatar(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	accounts := agentAccounts(h)
	id := newAgentWithRole(t, h, "Hanna")
	person := h.insertUser(t, "person@example.test")

	if err := accounts.Rename(ctx, id, "Hanna Hansen"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got, _ := newUserDirectory(t, h).User(ctx, id); got == nil || got.DisplayName != "Hanna Hansen" {
		t.Errorf("User after Rename = %+v", got)
	}
	if err := accounts.Rename(ctx, id, "  "); !errors.Is(err, contracts.ErrInvalidAgentName) {
		t.Errorf("Rename blank = %v, want ErrInvalidAgentName", err)
	}
	if _, err := accounts.Create(ctx, strings.Repeat("x", 201)); !errors.Is(err, contracts.ErrInvalidAgentName) {
		t.Errorf("Create too long = %v, want ErrInvalidAgentName", err)
	}

	// A person is not an agent: the slot never changes one.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, img); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"Rename":                    accounts.Rename(ctx, person, "Someone"),
		"SetAvatar":                 accounts.SetAvatar(ctx, person, "image/png", pngBytes.Bytes()),
		"Disable":                   accounts.Disable(ctx, person),
		"Enable":                    accounts.Enable(ctx, person),
		"Rename of an unknown user": accounts.Rename(ctx, uuid.New(), "Someone"),
	} {
		if !errors.Is(err, contracts.ErrNotAgent) {
			t.Errorf("%s on a person = %v, want ErrNotAgent", name, err)
		}
	}
	if got, _ := newUserDirectory(t, h).User(ctx, person); got == nil || got.DisplayName != "person@example.test" || !got.Active {
		t.Errorf("the person changed: %+v", got)
	}

	if err := accounts.SetAvatar(ctx, id, "image/png", pngBytes.Bytes()); err != nil {
		t.Fatalf("SetAvatar: %v", err)
	}
	if av, _ := newUserDirectory(t, h).Avatar(ctx, id); av == nil || av.ContentType != "image/png" {
		t.Errorf("Avatar = %+v, want the PNG", av)
	}
	if err := accounts.SetAvatar(ctx, id, "image/png", []byte("not an image")); !errors.Is(err, contracts.ErrInvalidAgentAvatar) {
		t.Errorf("SetAvatar of junk = %v, want ErrInvalidAgentAvatar", err)
	}

	// Disable ends the grants the agent holds and stops new ones; Enable lets it
	// act again.
	grant := issueAgent(t, h, id)
	if err := accounts.Disable(ctx, id); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if err := accounts.Disable(ctx, id); err != nil {
		t.Errorf("Disable twice = %v, want nil", err)
	}
	if _, got := checkAgent(t, h, grant, "", agentRule); got != u401 {
		t.Errorf("a disabled agent's grant answered %v, want 401", got)
	}
	if _, err := agentGrants(h).Issue(ctx, contracts.IssueAgentGrant{AgentUserID: id, AgentID: uuid.New(), RunID: uuid.New()}); !errors.Is(err, contracts.ErrAgentInactive) {
		t.Errorf("Issue for a disabled agent = %v, want ErrAgentInactive", err)
	}
	if got, _ := newUserDirectory(t, h).User(ctx, id); got == nil || got.Active {
		t.Errorf("User after Disable = %+v, want inactive", got)
	}
	if err := accounts.Enable(ctx, id); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	issueAgent(t, h, id)
}

func TestAgent_NeverSignsInOrResetsAPasswordOrIsInvited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := newAgent(t, h, "Hanna")
	email := "agent-" + id.String() + "@agents.invalid"

	// Sign-in: whatever the password, the agent is no candidate.
	c := h.client(t)
	r := c.do(http.MethodPost, "/api/v1/identity/login", map[string]any{"email": email, "password": "Correct-Horse-9"})
	if r.status != http.StatusUnauthorized || r.code() != "invalid_credentials" {
		t.Errorf("login = %d %s, want 401 invalid_credentials", r.status, r.body)
	}
	// A session is refused where it would be made.
	if _, err := identity.CreateSession(h.access, context.Background(), h.pool, id, false, false, httptest.NewRequest(http.MethodPost, "/", nil)); err == nil {
		t.Error("CreateSession for an agent succeeded")
	}

	// Recovery: no mail goes, and a reset token the table holds is refused.
	r = c.do(http.MethodPost, "/api/v1/identity/password-recovery/request", map[string]any{"email": email})
	if r.status != http.StatusOK || len(h.mailTo(email)) != 0 {
		t.Errorf("recovery request = %d, mail %v, want 200 and no mail", r.status, h.mailTo(email))
	}
	h.exec(t, `INSERT INTO identity.password_reset_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		[]byte("hash"), id, h.now().Add(time.Hour))
	r = c.do(http.MethodPost, "/api/v1/identity/password-recovery/reset", map[string]any{"email": email, "token": "irrelevant", "newPassword": "Correct-Horse-9"})
	if r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("reset = %d %s, want 400 invalid_reset_token", r.status, r.body)
	}

	// Invitation: the address is taken, and no invitation may name the user.
	r = owner.do(http.MethodPost, "/api/v1/identity/owner/invitations", map[string]any{"email": email, "role": identity.RoleUser})
	if r.status != http.StatusConflict {
		t.Errorf("invite = %d %s, want 409", r.status, r.body)
	}
	_, err := h.pool.Exec(context.Background(), `INSERT INTO identity.invitations
		(id, email, normalized_email, role, token_hash, created_at, expires_at, user_id) VALUES ($1, $2, upper($2), 'User', $3, $4, $5, $6)`,
		uuid.New(), "x@example.test", []byte("hash"), h.now(), h.now().Add(time.Hour), id)
	wantViolation(t, "an invitation naming an agent", err)

	// The owner's user operations do not reach an agent at all.
	for _, call := range []struct {
		method, suffix string
		body           any
	}{
		{http.MethodPut, "", map[string]any{"displayName": "X", "email": "x@example.test", "role": identity.RoleOwner}},
		{http.MethodPost, "/password", map[string]any{"password": "Correct-Horse-9-Long"}},
		{http.MethodPost, "/password-reset", nil},
		{http.MethodPost, "/disable", nil},
		{http.MethodPost, "/enable", nil},
		{http.MethodDelete, "", nil},
	} {
		if r := owner.do(call.method, ownerUsersPath+"/"+id.String()+call.suffix, call.body); r.status != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", call.method, call.suffix, r.status)
		}
	}
}

func TestAgent_CannotBeMappedByScimLinkedToOIDCOrGivenPrivilege(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := newAgent(t, h, "Hanna")
	email := "agent-" + id.String() + "@agents.invalid"
	ctx := context.Background()

	// SCIM cannot adopt the agent's address, and its lists do not show it.
	scim := h.scim(t, scimToken)
	r := scim.do(http.MethodPost, "/Users", userResource("hanna", map[string]any{
		"emails": []map[string]any{{"value": email, "type": "work", "primary": true}},
	}))
	if r.status != http.StatusBadRequest {
		t.Errorf("SCIM create on the agent's address = %d %s, want 400", r.status, r.body)
	}
	list := scim.do(http.MethodGet, "/Users", nil)
	if list.status != http.StatusOK || strings.Contains(string(list.body), id.String()) || strings.Contains(string(list.body), email) {
		t.Errorf("SCIM list = %d %s, want no agent", list.status, list.body)
	}

	// The database refuses what the code does not offer.
	_, err := h.pool.Exec(ctx, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
		VALUES ($1, $2, 'e', 'u', true, 1, 'etag', $3, $3)`, uuid.New(), id, h.now())
	wantViolation(t, "a SCIM mapping", err)
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.oidc_links (issuer, subject, user_id, created_at) VALUES ('https://idp.example.test', 'sub', $1, $2)`, id, h.now())
	wantViolation(t, "an OIDC link", err)
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, identity.RoleOwnerID)
	wantViolation(t, "the Owner role", err)
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, identity.RoleSystemAdminID)
	wantViolation(t, "the SystemAdmin role", err)
	custom := h.insertRole(t, "Front desk", "customers:view")
	if _, err := h.pool.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, custom); err != nil {
		t.Errorf("a custom role for an agent: %v, want it allowed", err)
	}
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.roles (id, name, normalized_name, display_name, steward_user_id, version, created_at, updated_at)
		VALUES ($1, 'Stewarded', 'STEWARDED', 'Stewarded', $2, $3, $4, $4)`, uuid.New(), id, uuid.New(), h.now())
	wantViolation(t, "the stewardship of a role", err)

	// A delegation to an agent is refused by the endpoint and by the table.
	r = owner.do(http.MethodPost, delegationsPath, map[string]any{
		"granteeUserId": id, "permissionKeys": []string{"customers:view"}, "stewardedRoleIds": []uuid.UUID{custom}, "canCreateRoles": false,
	})
	if r.status != http.StatusBadRequest || r.code() != "invalid_delegation_target" {
		t.Errorf("delegation to an agent = %d %s, want 400 invalid_delegation_target", r.status, r.body)
	}
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.authorization_delegations (id, grantee_user_id, created_by_user_id, can_create_roles, created_at, version)
		VALUES ($1, $2, $2, false, $3, $4)`, uuid.New(), id, h.now(), uuid.New())
	wantViolation(t, "a delegation", err)
}

func TestAgent_ListedApartAndCounted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	agent := newAgent(t, h, "Hanna")
	h.createUser(t, owner, "kari@example.test", identity.RoleUser)

	var users []struct {
		ID   uuid.UUID `json:"id"`
		Kind string    `json:"kind"`
	}
	r := owner.do(http.MethodGet, ownerUsersPath, nil)
	r.json(&users)
	if r.status != http.StatusOK || len(users) != 3 || users[len(users)-1].ID != agent || users[len(users)-1].Kind != "agent" {
		t.Fatalf("owner users = %d %s, want the agent last with kind agent", r.status, r.body)
	}
	for _, u := range users[:len(users)-1] {
		if u.Kind != "person" {
			t.Errorf("user %s kind = %q, want person", u.ID, u.Kind)
		}
	}

	var counts struct{ People, Agents int }
	r = owner.do(http.MethodGet, "/api/v1/identity/owner/user-counts", nil)
	r.json(&counts)
	if r.status != http.StatusOK || counts.People != 2 || counts.Agents != 1 {
		t.Errorf("counts = %d %s, want 2 people and 1 agent", r.status, r.body)
	}

	var access []struct {
		ID   uuid.UUID `json:"id"`
		Kind string    `json:"kind"`
	}
	r = owner.do(http.MethodGet, "/api/v1/identity/access/users", nil)
	r.json(&access)
	i := slices.IndexFunc(access, func(u struct {
		ID   uuid.UUID `json:"id"`
		Kind string    `json:"kind"`
	}) bool {
		return u.ID == agent
	})
	if r.status != http.StatusOK || i != len(access)-1 || access[i].Kind != "agent" {
		t.Errorf("access users = %d %s, want the agent last with kind agent", r.status, r.body)
	}
}

func TestAgent_DirectoryLeavesAgentsOutUnlessAsked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	dir := newUserDirectory(t, h)
	agent := newAgent(t, h, "Hanna Agent")
	person := insertUserNamed(t, h, "Hanna Person", false)
	role := h.insertRole(t, "Front desk", "customers:view")
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2), ($3, $2)`, agent, role, person)

	ids := func(entries []contracts.UserEntry) []uuid.UUID {
		out := []uuid.UUID{}
		for _, e := range entries {
			out = append(out, e.ID)
		}
		return out
	}
	found, err := dir.SearchUsers(ctx, "Hanna", 10)
	if err != nil || !slices.Equal(ids(found), []uuid.UUID{person}) {
		t.Errorf("SearchUsers default = %v, %v, want only the person", ids(found), err)
	}
	found, err = dir.SearchUsers(ctx, "Hanna", 10, contracts.IncludeAgents())
	if err != nil || len(found) != 2 {
		t.Fatalf("SearchUsers IncludeAgents = %v, %v, want both", ids(found), err)
	}
	for _, e := range found {
		want := contracts.UserKindPerson
		if e.ID == agent {
			want = contracts.UserKindAgent
		}
		if e.Kind != want {
			t.Errorf("entry %s kind = %q, want %q", e.ID, e.Kind, want)
		}
	}

	holders := func(opts ...contracts.DirectoryOption) []uuid.UUID {
		hs, err := dir.PermissionHolders(ctx, "customers:view", opts...)
		if err != nil {
			t.Fatalf("PermissionHolders: %v", err)
		}
		out := []uuid.UUID{}
		for _, hd := range hs {
			out = append(out, hd.ID)
		}
		return out
	}
	if got := holders(); !slices.Equal(got, []uuid.UUID{person}) {
		t.Errorf("PermissionHolders default = %v, want only the person", got)
	}
	if got := holders(contracts.IncludeAgents()); len(got) != 2 || !slices.Contains(got, agent) {
		t.Errorf("PermissionHolders IncludeAgents = %v, want both", got)
	}
	if o := contracts.ResolveDirectoryOptions(nil); o.IncludeAgents {
		t.Error("IncludeAgents defaults to true")
	}
}

func TestAgent_NotifyIsDropped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := newAgent(t, h, "Hanna")
	if err := newUserDirectory(t, h).Notify(context.Background(), id, bilingual); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got := h.mailTo("agent-" + id.String() + "@agents.invalid"); len(got) != 0 {
		t.Errorf("mail to an agent = %+v, want none", got)
	}
}

func TestAgentGrant_AnswersTheAgentsPrincipalWithVia(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	h := f.h
	agent := newAgent(t, h, "Hanna")
	role := h.insertRole(t, "Front desk", "identity:manage")
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, agent, role)

	agentID, runID, reviewer := uuid.New(), uuid.New(), f.users["user"]
	customer := int32(1001)
	id, err := agentGrants(h).Issue(context.Background(), contracts.IssueAgentGrant{
		AgentUserID: agent, AgentID: agentID, RunID: runID, CustomerID: &customer, ApprovedBy: &reviewer,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	p, got := checkAgent(t, h, id, f.tokens["ownerMFA"], "permission:identity:manage")
	if got != allowed {
		t.Fatalf("a permission its role holds answered %v, want allowed", got)
	}
	if p.UserID != agent || p.SessionID != uuid.Nil || p.MFAVerified || p.SCIM || !slices.Equal(p.Roles, []string{"Front desk"}) {
		t.Errorf("principal = %+v, want the agent, no session, no MFA, its one role", p)
	}
	via := p.Via
	if via == nil || via.Kind != contracts.ViaAgent || via.AgentID != agentID || via.RunID != runID ||
		via.CustomerID == nil || *via.CustomerID != customer || via.ApprovedBy == nil || *via.ApprovedBy != reviewer {
		t.Errorf("Via = %+v, want the agent, run, customer and reviewer", via)
	}

	// What the role does not carry is forbidden. An agent is admitted by a
	// permission it holds alone: a session rule or a policy by itself would
	// reach a person's self-service endpoints, so both are refused.
	if _, got := checkAgent(t, h, id, "", "permission:customers:edit"); got != f403 {
		t.Errorf("a permission its role lacks answered %v, want 403", got)
	}
	if p, got := checkAgent(t, h, id, "", "anonymous"); got != allowed || p.UserID != agent {
		t.Errorf("anonymous: answered %v for %v, want the agent allowed", got, p.UserID)
	}

	// The Owner tier, SCIM, a session rule and a policy by itself are never an
	// agent's.
	for _, rule := range []string{
		"session", "policy:ActiveAccount", "policy:Business", "policy:ActiveAccount+Business",
		"policy:Owner", "policy:OwnerManagement", "policy:Owner+OwnerManagement", "policy:SystemAdmin",
		"policy:Owner+SystemAdmin", "policy:AuthorizationManagement", "policy:ActiveAccount+Owner", "scim",
	} {
		if _, got := checkAgent(t, h, id, "", rule); got != f403 {
			t.Errorf("%s: answered %v, want ErrForbidden", rule, got)
		}
	}
}

func TestAgentGrant_IssueRefusesWhatIsNotAnActiveAgentAndTheGrantEnds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	grants := agentGrants(h)
	person := h.insertUser(t, "person@example.test", identity.RoleUserID)

	if _, err := grants.Issue(ctx, contracts.IssueAgentGrant{AgentUserID: person, AgentID: uuid.New(), RunID: uuid.New()}); !errors.Is(err, contracts.ErrNotAgent) {
		t.Errorf("Issue for a person = %v, want ErrNotAgent", err)
	}
	if _, err := grants.Issue(ctx, contracts.IssueAgentGrant{AgentUserID: uuid.New(), AgentID: uuid.New(), RunID: uuid.New()}); !errors.Is(err, contracts.ErrNotAgent) {
		t.Errorf("Issue for an unknown user = %v, want ErrNotAgent", err)
	}
	// The table refuses a grant for a person whatever code issues it.
	_, err := h.pool.Exec(ctx, `INSERT INTO identity.agent_grants (id, user_id, agent_id, run_id, created_at, expires_at) VALUES ($1, $2, $3, $3, $4, $5)`,
		uuid.New(), person, uuid.New(), h.now(), h.now().Add(time.Minute))
	wantViolation(t, "a grant for a person", err)

	agent := newAgentWithRole(t, h, "Hanna")
	// A grant lives at most ten minutes.
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.agent_grants (id, user_id, agent_id, run_id, created_at, expires_at) VALUES ($1, $2, $3, $3, $4, $5)`,
		uuid.New(), agent, uuid.New(), h.now(), h.now().Add(11*time.Minute))
	wantViolation(t, "a grant of eleven minutes", err)

	// Revoke ends one; a repeat and an unknown id are not errors.
	id := issueAgent(t, h, agent)
	if _, got := checkAgent(t, h, id, "", agentRule); got != allowed {
		t.Fatalf("a fresh grant answered %v", got)
	}
	if err := grants.Revoke(ctx, id); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := grants.Revoke(ctx, id); err != nil {
		t.Errorf("Revoke twice = %v", err)
	}
	if err := grants.Revoke(ctx, uuid.New()); err != nil {
		t.Errorf("Revoke unknown = %v", err)
	}
	if _, got := checkAgent(t, h, id, "", agentRule); got != u401 {
		t.Errorf("a revoked grant answered %v, want 401", got)
	}
	if p, got := checkAgent(t, h, id, "", "anonymous"); got != allowed || p.UserID != uuid.Nil {
		t.Errorf("a revoked grant under anonymous = %v for %v, want a caller with none", got, p.UserID)
	}

	// RevokeForUser ends every grant of the agent, and only that agent's.
	a, b := issueAgent(t, h, agent), issueAgent(t, h, agent)
	other := issueAgent(t, h, newAgentWithRole(t, h, "Other"))
	if err := grants.RevokeForUser(ctx, agent); err != nil {
		t.Fatalf("RevokeForUser: %v", err)
	}
	for _, g := range []uuid.UUID{a, b} {
		if _, got := checkAgent(t, h, g, "", agentRule); got != u401 {
			t.Errorf("a grant after RevokeForUser answered %v, want 401", got)
		}
	}
	if _, got := checkAgent(t, h, other, "", agentRule); got != allowed {
		t.Errorf("another agent's grant answered %v, want allowed", got)
	}

	// A grant expires after its ten minutes, and never before.
	id = issueAgent(t, h, agent)
	h.advance(contracts.MaxAgentGrantTTL - time.Second)
	if _, got := checkAgent(t, h, id, "", agentRule); got != allowed {
		t.Errorf("a grant just before its expiry answered %v, want allowed", got)
	}
	h.advance(time.Second)
	if _, got := checkAgent(t, h, id, "", agentRule); got != u401 {
		t.Errorf("a grant at its expiry answered %v, want 401", got)
	}

	// An unknown grant, and a grant of a kind Check does not know, are no caller.
	if _, got := checkAgent(t, h, uuid.New(), "", agentRule); got != u401 {
		t.Errorf("an unknown grant answered %v, want 401", got)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(contracts.WithGrant(req.Context(), contracts.GrantRef{Kind: "other", ID: id}))
	rule, _ := contracts.ParseRule(agentRule)
	if _, err := h.access.Check(req, rule); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Errorf("a grant of an unknown kind = %v, want ErrUnauthenticated", err)
	}
}

func TestAgentGrant_AnAgentThatStopsBeingActiveIsRefusedAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	agent := newAgentWithRole(t, h, "Hanna")
	id := issueAgent(t, h, agent)
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, agent)
	if _, got := checkAgent(t, h, id, "", agentRule); got != f403 {
		t.Errorf("the grant of a disabled agent answered %v, want 403", got)
	}
	if p, got := checkAgent(t, h, id, "", "anonymous"); got != allowed || p.UserID != uuid.Nil {
		t.Errorf("under anonymous = %v for %v, want a caller with none", got, p.UserID)
	}
}

func TestGrantContext_KindsDoNotCross(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	ctx := contracts.WithGrant(context.Background(), contracts.GrantRef{Kind: contracts.GrantAgent, ID: id})
	if _, ok := contracts.AssistantGrantFrom(ctx); ok {
		t.Error("AssistantGrantFrom answered an agent grant")
	}
	if ref, ok := contracts.GrantFrom(ctx); !ok || ref.ID != id || ref.Kind != contracts.GrantAgent {
		t.Errorf("GrantFrom = %+v, %v", ref, ok)
	}
	ctx = contracts.WithAssistantGrant(context.Background(), id)
	if got, ok := contracts.AssistantGrantFrom(ctx); !ok || got != id {
		t.Errorf("AssistantGrantFrom = %v, %v, want the id", got, ok)
	}
	if _, ok := contracts.GrantFrom(contracts.WithGrant(context.Background(), contracts.GrantRef{Kind: contracts.GrantAgent})); ok {
		t.Error("GrantFrom answered a nil id")
	}
}

func TestAgent_UpdatesCannotPointARowAtAnAgentEither(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	agent := newAgent(t, h, "Hanna")
	person := h.insertUser(t, "person@example.test", identity.RoleUserID)
	custom := h.insertRole(t, "Front desk", "customers:view")

	// The kind of an existing user never changes.
	_, err := h.pool.Exec(ctx, `UPDATE identity.users SET kind = 'agent' WHERE id = $1`, person)
	wantViolation(t, "a person turned into an agent", err)
	_, err = h.pool.Exec(ctx, `UPDATE identity.users SET kind = 'person' WHERE id = $1`, agent)
	wantViolation(t, "an agent turned into a person", err)

	// A role row of the agent cannot be moved onto the Owner role, nor the
	// Owner role's row onto the agent.
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, agent, custom)
	_, err = h.pool.Exec(ctx, `UPDATE identity.user_roles SET role_id = $2 WHERE user_id = $1`, agent, identity.RoleOwnerID)
	wantViolation(t, "an agent's role updated to Owner", err)
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, person, identity.RoleOwnerID)
	_, err = h.pool.Exec(ctx, `UPDATE identity.user_roles SET user_id = $1 WHERE user_id = $2 AND role_id = $3`, agent, person, identity.RoleOwnerID)
	wantViolation(t, "the Owner role's row moved to an agent", err)

	// A session or a SCIM mapping or a grant moved onto an agent.
	h.session(t, person, false)
	_, err = h.pool.Exec(ctx, `UPDATE identity.sessions SET user_id = $1 WHERE user_id = $2`, agent, person)
	wantViolation(t, "a session moved to an agent", err)
	h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
		VALUES ($1, $2, 'e', 'u', true, 1, 'etag', $3, $3)`, uuid.New(), person, h.now())
	_, err = h.pool.Exec(ctx, `UPDATE identity.scim_user_mappings SET user_id = $1 WHERE user_id = $2`, agent, person)
	wantViolation(t, "a SCIM mapping moved to an agent", err)
	grant := issueAgent(t, h, agent)
	_, err = h.pool.Exec(ctx, `UPDATE identity.agent_grants SET user_id = $1 WHERE id = $2`, person, grant)
	wantViolation(t, "a grant moved to a person", err)
}

func TestAgentGrant_RefusesAnAgentHoldingOwnerOrSystemAdminEvenIfTheTableAllowedIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, role := range []uuid.UUID{identity.RoleOwnerID, identity.RoleSystemAdminID} {
		agent := newAgentWithRole(t, h, "Hanna")
		grant := issueAgent(t, h, agent)
		// Seed what the trigger forbids, as a mistake elsewhere would.
		h.exec(t, `ALTER TABLE identity.user_roles DISABLE TRIGGER user_roles_refuse_agent_privileged`)
		h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, agent, role)
		h.exec(t, `ALTER TABLE identity.user_roles ENABLE TRIGGER user_roles_refuse_agent_privileged`)
		if _, got := checkAgent(t, h, grant, "", agentRule); got != f403 {
			t.Errorf("an agent holding %v answered %v, want 403", role, got)
		}
		if p, got := checkAgent(t, h, grant, "", "anonymous"); got != allowed || p.UserID != uuid.Nil {
			t.Errorf("under anonymous = %v for %v, want a caller with none", got, p.UserID)
		}
	}
}

func TestAgent_IsNoOneWhoMayActOnAUsersBehalfOrSignInByPasskey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	agent := newAgentWithRole(t, h, "Hanna")
	if held, err := h.access.UserHolds(context.Background(), agent, "customers:view"); err != nil || held {
		t.Errorf("UserHolds for an agent = %v, %v, want false", held, err)
	}
	person := h.insertUser(t, "person@example.test", identity.RoleUserID)
	role := h.insertRole(t, "Viewer", "customers:view")
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, person, role)
	if held, err := h.access.UserHolds(context.Background(), person, "customers:view"); err != nil || !held {
		t.Errorf("UserHolds for a person = %v, %v, want true", held, err)
	}
}
