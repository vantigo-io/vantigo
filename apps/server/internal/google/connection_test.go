package google_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The connection (connection.go): its read, the admin under If-Match, the
// credential kind, the Internal web client, the events and who may do what.

func TestConnection_AFreshInstallation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m, _ := manager(t, h)
	conn := getConnection(t, m)
	if conn.Status != gen.GoogleConnectionStatusUnconfigured || conn.StatusReason != nil || conn.AdminSubject != nil ||
		conn.Version != 1 || len(conn.Credentials) != 0 || conn.DelegationClientId != nil || conn.LastCheckedAt != nil ||
		conn.CredentialKind != gen.GoogleConnectionCredentialKind("service_account_key") || conn.OauthClientId != nil {
		t.Errorf("a fresh connection: %+v", conn)
	}
	if !slices.Equal(conn.ScopesToDelegate, everyScope) {
		t.Errorf("scopesToDelegate = %v, want %v", conn.ScopesToDelegate, everyScope)
	}
	if len(conn.RedirectUris) != 2 || !strings.HasSuffix(conn.RedirectUris[0], "/api/v1/google/connect/callback") ||
		!strings.HasSuffix(conn.RedirectUris[1], "/api/v1/identity/oidc/callback") || !strings.HasPrefix(conn.RedirectUris[0], "http") {
		t.Errorf("redirectUris = %v", conn.RedirectUris)
	}
	if n := len(h.g.Calls()); n != 0 {
		t.Errorf("GET /connection made %d calls to Google", n)
	}
}

func TestConnection_WhoMayDoWhat(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first, _, _ := accounts()
	nobody, _ := h.SignInUser(t)
	m, _ := manager(t, h)

	for _, c := range []struct {
		method, path string
		body         any
		opts         []modtest.RequestOption
	}{
		{http.MethodPut, connectionPath, map[string]any{"adminSubject": adminAddress}, []modtest.RequestOption{ifMatch(1)}},
		{http.MethodPost, credentialsPath, map[string]any{"json": first.KeyFile()}, nil},
		{http.MethodDelete, credentialsPath + "/1001", nil, nil},
		{http.MethodPut, oauthClientPath, map[string]any{"clientId": webClientID, "clientSecret": webSecret}, nil},
		{http.MethodPost, testPath, nil, nil},
	} {
		if r := m.Do(c.method, c.path, c.body, c.opts...); r.Status != http.StatusForbidden {
			t.Errorf("google:manage %s %s: %d, want 403 (the Owner's)", c.method, c.path, r.Status)
		}
	}
	for _, path := range []string{connectionPath, eventsPath} {
		if r := nobody.Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
			t.Errorf("a user without google:manage GET %s: %d, want 403", path, r.Status)
		}
		if r := m.Do(http.MethodGet, path, nil); r.Status != http.StatusOK {
			t.Errorf("google:manage GET %s: %d, want 200", path, r.Status)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM google.credentials`); n != 0 {
		t.Errorf("a refused upload stored %d credentials", n)
	}
}

func TestConnection_TheAdminUnderIfMatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, ownerID := owner(t, h)

	r := o.Do(http.MethodPut, connectionPath, map[string]any{"adminSubject": "not an address"}, ifMatch(1))
	if p := problemOf(t, r); r.Status != http.StatusBadRequest || p.Errors == nil || len((*p.Errors)["adminSubject"]) == 0 {
		t.Errorf("an admin that is not an address: %d %s, want a field error", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, connectionPath, map[string]any{"adminSubject": adminAddress}, modtest.Header("If-Match", "latest")); r.Status != http.StatusBadRequest {
		t.Errorf("an If-Match that is not a version: %d, want 400", r.Status)
	}
	if r := o.Do(http.MethodPut, connectionPath, map[string]any{"adminSubject": adminAddress}, ifMatch(7)); r.Status != http.StatusPreconditionFailed || codeOf(t, r) != "version_mismatch" {
		t.Errorf("a stale version: %d %s, want 412 version_mismatch", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, connectionPath, map[string]any{"credentialKind": "vantigo_hosted"}, ifMatch(1)); r.Status != http.StatusUnprocessableEntity || codeOf(t, r) != "credential_kind_unsupported" {
		t.Errorf("vantigo_hosted: %d %s, want 422 credential_kind_unsupported", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, connectionPath, map[string]any{"credentialKind": "a_password"}, ifMatch(1)); r.Status != http.StatusBadRequest {
		t.Errorf("an unknown kind: %d, want 400", r.Status)
	}

	r = o.Do(http.MethodPut, connectionPath, map[string]any{"adminSubject": " Admin@Nordlys.no "}, modtest.Header("If-Match", `"1"`))
	if r.Status != http.StatusOK {
		t.Fatalf("set the admin: %d %s", r.Status, r.Body)
	}
	var conn gen.GoogleConnection
	r.JSON(&conn)
	if conn.Version != 2 || conn.AdminSubject == nil || *conn.AdminSubject != adminAddress || conn.Status != gen.GoogleConnectionStatusUnconfigured {
		t.Errorf("after setting the admin: %+v", conn)
	}
	codes := modtest.One[[]string](t, h.Harness, `SELECT codes FROM google.events WHERE action = 'google.connection.changed' AND actor_user_id = $1`, ownerID)
	if !slices.Equal(codes, []string{"admin_subject_changed"}) {
		t.Errorf("the event's codes = %v", codes)
	}
	if n := h.Count(t, `SELECT count(*) FROM google.events WHERE array_to_string(codes, ' ') LIKE '%nordlys%' OR subject_id LIKE '%@%'`); n != 0 {
		t.Errorf("%d events carry an address", n)
	}

	// The same admin again writes nothing.
	r = o.Do(http.MethodPut, connectionPath, map[string]any{"adminSubject": adminAddress, "credentialKind": "service_account_key"}, ifMatch(2))
	r.JSON(&conn)
	if r.Status != http.StatusOK || conn.Version != 2 {
		t.Errorf("the same admin: %d version %d, want 200 at version 2", r.Status, conn.Version)
	}
}

func TestConnection_AChangedAdminForgetsTheTest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.g.Delegate(saClientID, everyScope...)
	o, _ := configured(t, h)
	if result := testConnection(t, o); result.Status != gen.GoogleConnectionTestStatusConnected {
		t.Fatalf("the test: %+v", result)
	}
	before := getConnection(t, o)
	if before.Status != gen.GoogleConnectionStatusConnected || len(before.ScopesGranted) != len(everyScope) {
		t.Fatalf("connected: %+v", before)
	}
	after := setAdmin(t, o, "other.admin@nordlys.no")
	if after.Status != gen.GoogleConnectionStatusUnconfigured || len(after.ScopesGranted) != 0 || after.LastCheckedAt != nil || after.Version != before.Version+1 {
		t.Errorf("after a changed admin: %+v, want unconfigured with the test forgotten", after)
	}
}

func TestConnection_TheWebClient(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, ownerID := owner(t, h)

	r := o.Do(http.MethodPut, oauthClientPath, map[string]any{"clientId": "vantigo-web", "clientSecret": ""})
	if p := problemOf(t, r); r.Status != http.StatusBadRequest || p.Errors == nil || len((*p.Errors)["clientId"]) == 0 || len((*p.Errors)["clientSecret"]) == 0 {
		t.Errorf("a client id of another shape and an empty secret: %d %s", r.Status, r.Body)
	}
	r = o.Do(http.MethodPut, oauthClientPath, map[string]any{"clientId": webClientID})
	if p := problemOf(t, r); r.Status != http.StatusBadRequest || p.Errors == nil || len((*p.Errors)["clientSecret"]) == 0 {
		t.Errorf("a new client without its secret: %d %s, want a field error on clientSecret", r.Status, r.Body)
	}

	r = o.Do(http.MethodPut, oauthClientPath, map[string]any{"clientId": webClientID, "clientSecret": webSecret})
	if r.Status != http.StatusOK {
		t.Fatalf("set the web client: %d %s", r.Status, r.Body)
	}
	if strings.Contains(string(r.Body), webSecret) {
		t.Errorf("the answer carries the secret: %s", r.Body)
	}
	var conn gen.GoogleConnection
	r.JSON(&conn)
	if conn.OauthClientId == nil || *conn.OauthClientId != webClientID || conn.OauthClientSecretAddedAt == nil || conn.Version != 2 {
		t.Errorf("the web client: %+v", conn)
	}
	sealed := modtest.One[[]byte](t, h.Harness, `SELECT sealed FROM google.credentials WHERE kind = 'oauth_client_secret'`)
	if strings.Contains(string(sealed), webSecret) {
		t.Errorf("the secret is stored in the clear")
	}

	// The same id without a secret writes nothing; a new secret alone
	// rotates, the older one revoked.
	r = o.Do(http.MethodPut, oauthClientPath, map[string]any{"clientId": webClientID})
	r.JSON(&conn)
	if r.Status != http.StatusOK || conn.Version != 2 {
		t.Errorf("the same client: %d version %d", r.Status, conn.Version)
	}
	r = o.Do(http.MethodPut, oauthClientPath, map[string]any{"clientId": webClientID, "clientSecret": "GOCSPX-a-newer-secret"})
	r.JSON(&conn)
	if r.Status != http.StatusOK || conn.Version != 3 {
		t.Errorf("a new secret: %d version %d", r.Status, conn.Version)
	}
	if n := h.Count(t, `SELECT count(*) FROM google.credentials WHERE kind = 'oauth_client_secret' AND revoked_at IS NULL`); n != 1 {
		t.Errorf("%d live secrets, want one", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM google.events WHERE action = 'google.oauth_client.changed' AND actor_user_id = $1`, ownerID); n != 2 {
		t.Errorf("%d web client events, want two", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM google.events WHERE array_to_string(codes, ' ') LIKE '%GOCSPX%'`); n != 0 {
		t.Errorf("an event carries the secret")
	}
}

func TestEvents_NewestFirstAndBounded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	configured(t, h)
	m, _ := manager(t, h)
	if r := m.Do(http.MethodGet, eventsPath+"?limit=0", nil); r.Status != http.StatusBadRequest {
		t.Errorf("limit 0: %d, want 400", r.Status)
	}
	if r := m.Do(http.MethodGet, eventsPath+"?limit=201", nil); r.Status != http.StatusBadRequest {
		t.Errorf("limit 201: %d, want 400", r.Status)
	}
	r := m.Do(http.MethodGet, eventsPath+"?limit=1", nil)
	var events []gen.GoogleEvent
	r.JSON(&events)
	if r.Status != http.StatusOK || len(events) != 1 || events[0].Action != "google.connection.changed" {
		t.Errorf("the newest event: %d %+v", r.Status, events)
	}
	r = m.Do(http.MethodGet, eventsPath, nil)
	r.JSON(&events)
	if len(events) != 2 || events[1].Action != "google.credential.added" || events[1].SubjectId == nil || *events[1].SubjectId != saClientID {
		t.Errorf("the events: %+v", events)
	}
}
