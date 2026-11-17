package identity_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// A user of kind integration is the account of an external application
// (API access design K1-K4, 00194_identity_integration_kind). Delivery 0 makes
// the kind and keeps it out of every path a person or an agent uses; the
// integrations service that makes one is delivery A, so these tests insert the
// row as that service will.

// insertIntegration makes a user of kind integration as the integrations
// service will: a confirmed address that never routes, no credential.
func insertIntegration(t *testing.T, h *harness, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := "integration-" + id.String() + "@integrations.invalid"
	h.exec(t, `INSERT INTO identity.users (id, email, normalized_email, email_confirmed, display_name, kind, version, created_at, updated_at)
		VALUES ($1, $2, upper($2), true, $3, 'integration', $4, $5, $5)`, id, email, name, uuid.New(), h.now())
	return id
}

// grantIntegrationRole gives an integration a role on a transaction that sets
// vantigo.integration_roles to its id, the integrations service's licence to
// write that integration's roles and no other's.
func grantIntegrationRole(t *testing.T, h *harness, id, role uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('vantigo.integration_roles', $1, true)`, id.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, role)
		return err
	})
	if err != nil {
		t.Fatalf("grant an integration a role: %v", err)
	}
}

func TestIntegration_TheDatabaseRefusesWhatOnlyAPersonMayHave(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	id := insertIntegration(t, h, "Webshop sync")
	person := h.insertUser(t, "person@example.test", identity.RoleUserID)

	_, err := h.pool.Exec(ctx, `UPDATE identity.users SET password_hash = 'x' WHERE id = $1`, id)
	wantViolation(t, "a password", err)
	_, err = h.pool.Exec(ctx, `UPDATE identity.users SET kind = 'person' WHERE id = $1`, id)
	wantViolation(t, "an integration turned into a person", err)

	h.session(t, person, false)
	_, err = h.pool.Exec(ctx, `UPDATE identity.sessions SET user_id = $1 WHERE user_id = $2`, id, person)
	wantViolation(t, "a session", err)
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.oidc_links (issuer, subject, user_id, created_at) VALUES ('https://idp.example.test', 'sub', $1, $2)`, id, h.now())
	wantViolation(t, "an OIDC link", err)
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
		VALUES ($1, $2, 'e', 'u', true, 1, 'etag', $3, $3)`, uuid.New(), id, h.now())
	wantViolation(t, "a SCIM mapping", err)
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.authorization_delegations (id, grantee_user_id, created_by_user_id, can_create_roles, created_at, version)
		VALUES ($1, $2, $3, false, $4, $5)`, uuid.New(), id, person, h.now(), uuid.New())
	wantViolation(t, "a delegation", err)
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.roles (id, name, normalized_name, display_name, steward_user_id, version, created_at, updated_at)
		VALUES ($1, 'Stewarded', 'STEWARDED', 'Stewarded', $2, $3, $4, $4)`, uuid.New(), id, uuid.New(), h.now())
	wantViolation(t, "the stewardship of a role", err)
	// The guard fires before the station's foreign key is looked at.
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.station_operator_sessions (id, station_id, user_id, user_display, started_at, last_activity_at)
		VALUES ($1, $2, $3, 'x', $4, $4)`, uuid.New(), uuid.New(), id, h.now())
	wantViolation(t, "a station operator session", err)

	// A role only through the integrations service, and never Owner or
	// SystemAdmin even then.
	custom := h.insertRole(t, "Webshop", "customers:view")
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, custom)
	wantViolation(t, "a role written outside the integrations service", err)
	_, err = h.pool.Exec(ctx, `INSERT INTO identity.access_group_memberships (group_id, user_id, source, membership_override) VALUES ($1, $2, 'local', 'force_member')`, uuid.New(), id)
	wantViolation(t, "an access-group membership", err)
	other := insertIntegration(t, h, "Other app")
	err = pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('vantigo.integration_roles', $1, true)`, other.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, custom)
		return err
	})
	wantViolation(t, "a role written under another integration's licence", err)
	grantIntegrationRole(t, h, id, custom)
	err = pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('vantigo.integration_roles', $1, true)`, id.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity.access_group_memberships (group_id, user_id, source) VALUES ($1, $2, 'local')`, uuid.New(), id); err != nil {
			return err
		}
		return nil
	})
	wantViolation(t, "an access-group membership even under the licence", err)
	err = pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('vantigo.integration_roles', $1, true)`, id.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, identity.RoleOwnerID)
		return err
	})
	wantViolation(t, "the Owner role, through the service", err)
	for name, stmt := range map[string]string{
		"a PIN":                  `INSERT INTO identity.user_pins (user_id) VALUES ($1)`,
		"an assistant grant":     `INSERT INTO identity.assistant_grants (user_id) VALUES ($1)`,
		"a password reset token": `INSERT INTO identity.password_reset_tokens (user_id) VALUES ($1)`,
		"recovery codes":         `INSERT INTO identity.recovery_codes (user_id) VALUES ($1)`,
	} {
		_, err := h.pool.Exec(ctx, stmt, id)
		wantViolation(t, name, err)
	}

	// A person's roles are untouched by the integration guard.
	if _, err := h.pool.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, person, custom); err != nil {
		t.Errorf("a person's custom role: %v, want it allowed", err)
	}
}

func TestIntegration_NoOwnerOrAccessOperationReachesIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := insertIntegration(t, h, "Webshop sync")
	custom := h.insertRole(t, "Webshop", "customers:view")
	grantIntegrationRole(t, h, id, custom)

	// Not listed among users, nor on Roles & access.
	var users []struct {
		ID uuid.UUID `json:"id"`
	}
	r := owner.do(http.MethodGet, ownerUsersPath, nil)
	r.json(&users)
	if r.status != http.StatusOK || slices.ContainsFunc(users, func(u struct {
		ID uuid.UUID `json:"id"`
	}) bool {
		return u.ID == id
	}) {
		t.Errorf("owner users = %d %s, want no integration", r.status, r.body)
	}
	r = owner.do(http.MethodGet, "/api/v1/identity/access/users", nil)
	users = nil
	r.json(&users)
	if r.status != http.StatusOK || slices.ContainsFunc(users, func(u struct {
		ID uuid.UUID `json:"id"`
	}) bool {
		return u.ID == id
	}) {
		t.Errorf("access users = %d %s, want no integration", r.status, r.body)
	}

	// No access group takes it as a member.
	g := createGroup(t, owner, "Apps")
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		if got := owner.do(method, memberPath(g.ID, id), stamped(g.ConcurrencyStamp)); got.status != http.StatusNotFound {
			t.Errorf("%s group member = %d %s, want 404", method, got.status, got.body)
		}
	}

	// Each operation naming it answers the bare 404, as for an agent.
	if got := owner.do(http.MethodGet, accessPath+"/users/"+id.String(), nil); got.status != http.StatusNotFound {
		t.Errorf("access user = %d %s, want 404", got.status, got.body)
	}
	if got := assignRoles(owner, id, h.version(t, "users", id), custom); got.status != http.StatusNotFound {
		t.Errorf("assign roles = %d %s, want 404", got.status, got.body)
	}
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

func TestIntegration_TheDirectoryNeverAnswersOneButALookupDoes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	dir := newUserDirectory(t, h)
	integration := insertIntegration(t, h, "Hanna Webshop")
	agent := newAgent(t, h, "Hanna Agent")
	person := insertUserNamed(t, h, "Hanna Person", false)
	role := h.insertRole(t, "Front desk", "customers:view")
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2), ($3, $2)`, agent, role, person)
	grantIntegrationRole(t, h, integration, role)

	for _, opts := range [][]contracts.DirectoryOption{nil, {contracts.IncludeAgents()}} {
		found, err := dir.SearchUsers(ctx, "Hanna", 10, opts...)
		if err != nil || slices.ContainsFunc(found, func(e contracts.UserEntry) bool { return e.ID == integration }) {
			t.Errorf("SearchUsers(%d options) = %v, %v, want no integration", len(opts), found, err)
		}
		holders, err := dir.PermissionHolders(ctx, "customers:view", opts...)
		if err != nil || slices.ContainsFunc(holders, func(hd contracts.Holder) bool { return hd.ID == integration }) {
			t.Errorf("PermissionHolders(%d options) = %v, %v, want no integration", len(opts), holders, err)
		}
	}

	// A record the integration made can still name it.
	u, err := dir.User(ctx, integration)
	if err != nil || u == nil || u.Kind != contracts.UserKindIntegration || u.DisplayName != "Hanna Webshop" {
		t.Errorf("User = %+v, %v, want the integration with its kind", u, err)
	}

	// It may not act on a user's behalf: only a key on the request admits it.
	if held, err := h.access.UserHolds(ctx, integration, "customers:view"); err != nil || held {
		t.Errorf("UserHolds for an integration = %v, %v, want false", held, err)
	}
}
