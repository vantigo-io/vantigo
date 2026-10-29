package entra_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The Outlook connection's paths (Outlook connection design M4).
const (
	outlookConnectPath    = "/api/v1/entra/outlook/connect"
	outlookCallbackPath   = "/api/v1/entra/outlook/callback"
	outlookCompletePath   = "/api/v1/entra/outlook/complete"
	outlookConnectionPath = "/api/v1/entra/outlook/connection"
	otherTenantID         = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

// fakeWorkforce is Staff as the one method Entra calls answers it,
// DirectoryAccount (design M6; the calendar's fakes_test.go shape): the
// account set for the principal in ctx, nil for anyone else. Every other
// method is the embedded nil interface and panics if called.
type fakeWorkforce struct {
	contracts.Workforce
	mu       sync.Mutex
	accounts map[uuid.UUID]contracts.DirectoryAccount
}

func newFakeWorkforce() *fakeWorkforce {
	return &fakeWorkforce{accounts: map[uuid.UUID]contracts.DirectoryAccount{}}
}

func (f *fakeWorkforce) DirectoryAccount(ctx context.Context) (*contracts.DirectoryAccount, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok {
		return nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.accounts[p.UserID]
	if !ok {
		return nil, nil
	}
	return &a, nil
}

// set gives userID the account, or (a nil) takes it away.
func (f *fakeWorkforce) set(userID uuid.UUID, a *contracts.DirectoryAccount) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a == nil {
		delete(f.accounts, userID)
		return
	}
	f.accounts[userID] = *a
}

// outlookFixture is a connected tenant with the Outlook connection switch
// on, Staff answering each person's directory account.
type outlookFixture struct {
	h *harness
	g *graphtest.FakeGraph
	o *modtest.Client
	w *fakeWorkforce
}

func newOutlookFixture(t *testing.T) *outlookFixture {
	t.Helper()
	w := newFakeWorkforce()
	h, g := newTenantHarness(t, modtest.WithWorkforce(w))
	o := connected(t, h)
	setOutlookConnect(t, o, true)
	return &outlookFixture{h: h, g: g, o: o, w: w}
}

// person is a signed-in user holding nothing whose Staff record names a
// trusted, enabled account the module manages: the client, the user and
// the account's object id.
func (f *outlookFixture) person(t *testing.T, upn string) (*modtest.Client, uuid.UUID, uuid.UUID) {
	t.Helper()
	c, userID := f.h.SignInUser(t)
	number, _, _ := strings.Cut(upn, "@")
	oid := addAccount(t, f.h, "E-"+number, upn)
	f.h.Exec(t, `UPDATE entra.accounts SET state = 'enabled' WHERE object_id = $1`, oid)
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: oid.String(), Enabled: true})
	return c, userID, oid
}

// secondSession is another browser signed in as userID: a session of its
// own, made as modtest makes one.
func secondSession(t *testing.T, h *harness, userID uuid.UUID) *modtest.Client {
	t.Helper()
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	hash := sha256.Sum256(raw)
	h.Exec(t, `INSERT INTO identity.sessions (id, user_id, token_hash, persistent, created_at, last_seen_at)
	           VALUES ($1, $2, $3, false, $4, $4)`, uuid.New(), userID, hash[:], h.Now())
	c := h.Client(t)
	c.SetCookie("vantigo.session", base64.RawURLEncoding.EncodeToString(raw))
	return c
}

// startConnect is POST /outlook/connect for the calendar, which must answer
// 200: the authorization URL and the Set-Cookie header.
func startConnect(t *testing.T, c *modtest.Client, returnTo string) (*url.URL, string) {
	t.Helper()
	body := map[string]any{"feature": "calendar"}
	if returnTo != "" {
		body["returnTo"] = returnTo
	}
	r := c.Do(http.MethodPost, outlookConnectPath, body)
	if r.Status != http.StatusOK {
		t.Fatalf("POST /outlook/connect: %d %s", r.Status, r.Body)
	}
	var a gen.EntraOutlookAuthorize
	r.JSON(&a)
	u, err := url.Parse(a.AuthorizeUrl)
	if err != nil {
		t.Fatal(err)
	}
	return u, r.Header("Set-Cookie")
}

// grantFor is the code grant Microsoft would issue for the authorization
// URL u, signed in as oid/upn and granting what u asked.
func grantFor(u *url.URL, oid, upn string) graphtest.CodeGrant {
	q := u.Query()
	return graphtest.CodeGrant{
		ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), Challenge: q.Get("code_challenge"),
		Scopes: strings.Fields(q.Get("scope")), Nonce: q.Get("nonce"),
		OID: oid, TID: tenantID, UPN: upn,
		Granted: []string{"Calendars.ReadWrite", "User.Read", "openid", "profile"},
	}
}

// callback is Microsoft's redirect back with query.
func callback(t *testing.T, c *modtest.Client, query url.Values) *modtest.Response {
	t.Helper()
	r := c.Do(http.MethodGet, outlookCallbackPath+"?"+query.Encode(), nil)
	if r.Status != http.StatusFound {
		t.Fatalf("GET /outlook/callback: %d %s", r.Status, r.Body)
	}
	return r
}

// outcomeOf is a callback's redirect: the page's path, outlook and code.
func outcomeOf(t *testing.T, r *modtest.Response) (string, string, string) {
	t.Helper()
	u, err := url.Parse(r.Header("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u.Path, u.Query().Get("outlook"), u.Query().Get("code")
}

// authorizeAs runs connect and Microsoft's part for c as oid/upn, with
// change applied to the grant: the callback's answer.
func (f *outlookFixture) authorizeAs(t *testing.T, c *modtest.Client, oid uuid.UUID, upn string, change func(*graphtest.CodeGrant)) *modtest.Response {
	t.Helper()
	u, _ := startConnect(t, c, "")
	grant := grantFor(u, oid.String(), upn)
	if change != nil {
		change(&grant)
	}
	code := f.g.IssueCode(grant)
	return callback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state")}})
}

// complete is POST /outlook/complete.
func complete(c *modtest.Client) *modtest.Response {
	return c.Do(http.MethodPost, outlookCompletePath, nil)
}

// connectAs runs the whole flow for c as oid/upn; the complete's answer.
func (f *outlookFixture) connectAs(t *testing.T, c *modtest.Client, oid uuid.UUID, upn string) *modtest.Response {
	t.Helper()
	r := f.authorizeAs(t, c, oid, upn, nil)
	if _, outlook, code := outcomeOf(t, r); outlook != "complete" {
		t.Fatalf("callback: outlook=%s code=%s", outlook, code)
	}
	return complete(c)
}

// written is what the flow may write: the connections, the connection's
// last error and the events.
func (f *outlookFixture) written(t *testing.T) [3]int {
	t.Helper()
	return [3]int{
		f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections`),
		f.h.Count(t, `SELECT count(*) FROM entra.connection WHERE outlook_last_error IS NOT NULL`),
		f.h.Count(t, `SELECT count(*) FROM entra.events`),
	}
}

// getOutlook is GET /outlook/connection, which always answers 200.
func getOutlook(t *testing.T, c *modtest.Client) gen.EntraOutlookConnection {
	t.Helper()
	r := c.Do(http.MethodGet, outlookConnectionPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /outlook/connection: %d %s", r.Status, r.Body)
	}
	var conn gen.EntraOutlookConnection
	r.JSON(&conn)
	return conn
}

// --- connect -----------------------------------------------------------------

func TestOutlookConnect_TheAuthorizationURLAndTheCookie(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, _ := f.person(t, "kari@nordlys.no")

	u, cookie := startConnect(t, c, "/calendar/settings")
	if u.Scheme != "https" || u.Host != "login.microsoftonline.com" || u.Path != "/"+tenantID+"/oauth2/v2.0/authorize" {
		t.Errorf("authorize endpoint = %s", u)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id":             clientID,
		"response_type":         "code",
		"response_mode":         "query",
		"redirect_uri":          "http://modules.example.com/api/v1/entra/outlook/callback",
		"scope":                 "openid profile offline_access https://graph.microsoft.com/Calendars.ReadWrite",
		"code_challenge_method": "S256",
		"login_hint":            "kari@nordlys.no",
		"domain_hint":           "nordlys.no",
		"claims":                `{"access_token":{"xms_cc":{"values":["cp1"]}}}`,
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	for _, k := range []string{"state", "nonce", "code_challenge"} {
		if v := q.Get(k); len(v) < 43 {
			t.Errorf("%s = %q, want 256 bits base64url", k, v)
		}
	}
	if q.Get("state") == q.Get("nonce") {
		t.Error("the state and the nonce are the same value")
	}
	if q.Has("prompt") {
		t.Errorf("prompt = %q, want none", q.Get("prompt"))
	}
	for _, want := range []string{"vantigo.entra.connect=", "Path=/api/v1/entra/outlook", "Max-Age=600", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("Set-Cookie %q lacks %q", cookie, want)
		}
	}
	if strings.Contains(cookie, "Secure") {
		t.Errorf("Set-Cookie %q is Secure on an http APP_URL", cookie)
	}
	if strings.Contains(cookie, q.Get("state")) || strings.Contains(cookie, q.Get("nonce")) {
		t.Error("the cookie carries the state or the nonce in the clear")
	}

	// Another connect starts afresh: new state, nonce and challenge.
	again, _ := startConnect(t, c, "")
	if again.Query().Get("state") == q.Get("state") || again.Query().Get("code_challenge") == q.Get("code_challenge") {
		t.Error("a second connect reused the state or the challenge")
	}
}

func TestOutlookConnect_DomainHintOnlyForAVerifiedDomain(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, _ := f.person(t, "kari@nordlys.example")
	u, _ := startConnect(t, c, "")
	if u.Query().Has("domain_hint") {
		t.Errorf("domain_hint = %q for an unverified domain, want none", u.Query().Get("domain_hint"))
	}
	if got := u.Query().Get("login_hint"); got != "kari@nordlys.example" {
		t.Errorf("login_hint = %q", got)
	}
}

func TestOutlookConnect_Refusals(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)

	c, userID, oid := f.person(t, "kari@nordlys.no")
	r := c.Do(http.MethodPost, outlookConnectPath, map[string]any{"feature": "mail"}, modtest.SkipContract("an unknown feature, which the contract's enum refuses"))
	if r.Status != http.StatusUnprocessableEntity || codeOf(t, r) != "feature_unknown" {
		t.Errorf("an unknown feature: %d %s, want 422 feature_unknown", r.Status, r.Body)
	}

	for _, tc := range []struct {
		name  string
		setup func()
		undo  func()
	}{
		{"no account", func() { f.w.set(userID, nil) }, nil},
		{"another provider", func() {
			f.w.set(userID, &contracts.DirectoryAccount{Provider: "google", ExternalID: oid.String(), Enabled: true})
		}, nil},
		{"a disabled link", func() {
			f.w.set(userID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: oid.String(), Enabled: false})
		}, nil},
		{"an external id that is not an object id", func() {
			f.w.set(userID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: "kari", Enabled: true})
		}, nil},
		{"an account the module keeps no row for", func() {
			f.w.set(userID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: uuid.NewString(), Enabled: true})
		}, nil},
		{"an untrusted adoption", func() {
			f.h.Exec(t, `UPDATE entra.accounts SET created_by_us = false, adoption_confirmed_at = NULL WHERE object_id = $1`, oid)
		}, func() { f.h.Exec(t, `UPDATE entra.accounts SET created_by_us = true WHERE object_id = $1`, oid) }},
		{"a disabled account", func() {
			f.h.Exec(t, `UPDATE entra.accounts SET state = 'disabled' WHERE object_id = $1`, oid)
		}, func() { f.h.Exec(t, `UPDATE entra.accounts SET state = 'enabled' WHERE object_id = $1`, oid) }},
		{"a deleted account", func() {
			f.h.Exec(t, `UPDATE entra.accounts SET state = 'deleted' WHERE object_id = $1`, oid)
		}, func() { f.h.Exec(t, `UPDATE entra.accounts SET state = 'enabled' WHERE object_id = $1`, oid) }},
	} {
		f.w.set(userID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: oid.String(), Enabled: true})
		tc.setup()
		r := c.Do(http.MethodPost, outlookConnectPath, map[string]any{"feature": "calendar"})
		if r.Status != http.StatusConflict || codeOf(t, r) != "no_account" {
			t.Errorf("%s: %d %s, want 409 no_account", tc.name, r.Status, r.Body)
		}
		if r.Header("Set-Cookie") != "" {
			t.Errorf("%s: a refusal set a cookie", tc.name)
		}
		if tc.undo != nil {
			tc.undo()
		}
	}
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: oid.String(), Enabled: true})

	// Another person's live connection holds the account.
	f.h.Exec(t, `INSERT INTO entra.outlook_connections (object_id, user_id, tenant_id, scopes, refresh_sealed, state, connected_at, changed_at, created_at, updated_at)
	             VALUES ($1, $2, $3, '{}', NULL, 'active', $4, $4, $4, $4)`, oid, uuid.New(), tenantID, f.h.Now())
	if r := c.Do(http.MethodPost, outlookConnectPath, map[string]any{"feature": "calendar"}); r.Status != http.StatusConflict || codeOf(t, r) != "account_taken" {
		t.Errorf("an account taken: %d %s, want 409 account_taken", r.Status, r.Body)
	}
	f.h.Exec(t, `UPDATE entra.outlook_connections SET state = 'closing' WHERE object_id = $1`, oid)
	startConnect(t, c, "") // a closing row of another person's is no obstacle

	setOutlookConnect(t, f.o, false)
	if r := c.Do(http.MethodPost, outlookConnectPath, map[string]any{"feature": "calendar"}); r.Status != http.StatusConflict || codeOf(t, r) != "switch_off" {
		t.Errorf("the switch off: %d %s, want 409 switch_off", r.Status, r.Body)
	}
}

// --- callback ----------------------------------------------------------------

func TestOutlookCallback_WithoutAValidStateWritesNothing(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, oid := f.person(t, "kari@nordlys.no")
	before := f.written(t)

	// No cookie at all: a stranger's browser.
	stranger := f.h.Client(t)
	r := callback(t, stranger, url.Values{"code": {"0.Aany"}, "state": {"any"}})
	if path, outlook, code := outcomeOf(t, r); path != "/settings/connections" || outlook != "failed" || code != "expired" {
		t.Errorf("no cookie: %s outlook=%s code=%s, want /settings/connections failed expired", path, outlook, code)
	}
	if !strings.Contains(r.Header("Set-Cookie"), "vantigo.entra.connect=;") || !strings.Contains(r.Header("Set-Cookie"), "Max-Age=0") {
		t.Errorf("no cookie: Set-Cookie %q, want the state cookie cleared", r.Header("Set-Cookie"))
	}

	// A wrong state, a missing-scope grant that would record consent_missing
	// with a valid one.
	u, _ := startConnect(t, c, "")
	grant := grantFor(u, oid.String(), "kari@nordlys.no")
	grant.Granted = []string{"openid", "profile"}
	code := f.g.IssueCode(grant)
	if _, _, got := outcomeOf(t, callback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state") + "x"}})); got != "expired" {
		t.Errorf("a wrong state: code=%s, want expired", got)
	}
	// The state cookie is spent: the right state no longer helps.
	if _, _, got := outcomeOf(t, callback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state")}})); got != "expired" {
		t.Errorf("the right state after a refusal: code=%s, want expired (the cookie spent)", got)
	}

	// A forged cookie beside a real connect's state.
	u, _ = startConnect(t, c, "")
	forger := f.h.Client(t)
	forger.SetCookie("vantigo.entra.connect", "bm90LWEtc2VhbGVkLWNvb2tpZQ")
	if _, _, got := outcomeOf(t, callback(t, forger, url.Values{"code": {code}, "state": {u.Query().Get("state")}})); got != "expired" {
		t.Errorf("a forged cookie: code=%s, want expired", got)
	}

	// An expired one: eleven minutes at Microsoft.
	u, _ = startConnect(t, c, "")
	f.h.Advance(11 * time.Minute)
	grant = grantFor(u, oid.String(), "kari@nordlys.no")
	grant.Granted = []string{"openid", "profile"}
	if _, _, got := outcomeOf(t, callback(t, c, url.Values{"code": {f.g.IssueCode(grant)}, "state": {u.Query().Get("state")}})); got != "expired" {
		t.Errorf("an expired cookie: code=%s, want expired", got)
	}

	if after := f.written(t); after != before {
		t.Errorf("written %v → %v, want nothing written without a valid state", before, after)
	}
	for _, call := range f.g.Calls() {
		if strings.HasSuffix(call.Path, "/oauth2/v2.0/token") && strings.Contains(call.Query, "code") {
			t.Errorf("a code was redeemed without a valid state: %+v", call)
		}
	}
}

func TestOutlookCallback_Failures(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, oid := f.person(t, "kari@nordlys.no")
	upn := "kari@nordlys.no"

	withState := func(extra url.Values) *modtest.Response {
		u, _ := startConnect(t, c, "")
		extra.Set("state", u.Query().Get("state"))
		return callback(t, c, extra)
	}
	for _, tc := range []struct {
		name  string
		query url.Values
		want  string
	}{
		{"the person cancelled", url.Values{"error": {"access_denied"}, "error_description": {"AADSTS65004: User declined to consent."}}, "access_denied"},
		{"another error", url.Values{"error": {"server_error"}}, "failed"},
		{"no code", url.Values{}, "failed"},
	} {
		if _, outlook, code := outcomeOf(t, withState(tc.query)); outlook != "failed" || code != tc.want {
			t.Errorf("%s: outlook=%s code=%s, want failed %s", tc.name, outlook, code, tc.want)
		}
	}

	for _, tc := range []struct {
		name   string
		change func(*graphtest.CodeGrant)
		want   string
	}{
		{"a PKCE verifier that does not match", func(g *graphtest.CodeGrant) { g.Challenge = "not-the-challenge" }, "failed"},
		{"another redirect URI", func(g *graphtest.CodeGrant) { g.RedirectURI = "https://elsewhere.example/cb" }, "failed"},
		{"a token for another tenant", func(g *graphtest.CodeGrant) { g.TID = otherTenantID }, "tenant_mismatch"},
		{"a bad nonce", func(g *graphtest.CodeGrant) { g.Nonce = "another-nonce" }, "failed"},
		{"an oid that is not a GUID", func(g *graphtest.CodeGrant) { g.OID = "kari" }, "failed"},
	} {
		if _, outlook, code := outcomeOf(t, f.authorizeAs(t, c, oid, upn, tc.change)); outlook != "failed" || code != tc.want {
			t.Errorf("%s: outlook=%s code=%s, want failed %s", tc.name, outlook, code, tc.want)
		}
	}

	// A signature nobody published, an audience of another client.
	f.g.IDTokenFor(func(map[string]any) bool { return false })
	if _, _, code := outcomeOf(t, f.authorizeAs(t, c, oid, upn, nil)); code != "failed" {
		t.Errorf("a bad signature: code=%s, want failed", code)
	}
	f.g.IDTokenFor(func(claims map[string]any) bool { claims["aud"] = uuid.NewString(); return true })
	if _, _, code := outcomeOf(t, f.authorizeAs(t, c, oid, upn, nil)); code != "failed" {
		t.Errorf("another audience: code=%s, want failed", code)
	}
	f.g.IDTokenFor(func(claims map[string]any) bool {
		claims["tid"] = otherTenantID
		claims["iss"] = "https://login.microsoftonline.com/" + otherTenantID + "/v2.0"
		return true
	})
	if _, _, code := outcomeOf(t, f.authorizeAs(t, c, oid, upn, nil)); code != "tenant_mismatch" {
		t.Errorf("an id_token of another tenant: code=%s, want tenant_mismatch", code)
	}
	f.g.IDTokenFor(func(claims map[string]any) bool {
		claims["iss"] = "https://login.microsoftonline.com/" + otherTenantID + "/v2.0"
		return true
	})
	if _, _, code := outcomeOf(t, f.authorizeAs(t, c, oid, upn, nil)); code != "failed" {
		t.Errorf("an issuer not the tenant's: code=%s, want failed", code)
	}
	f.g.IDTokenFor(nil)

	// A code redeemed already, and one past its minute.
	u, _ := startConnect(t, c, "")
	code := f.g.IssueCode(grantFor(u, oid.String(), upn))
	if _, outlook, _ := outcomeOf(t, callback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state")}})); outlook != "complete" {
		t.Fatalf("the first redemption: outlook=%s", outlook)
	}
	u, _ = startConnect(t, c, "")
	if _, _, got := outcomeOf(t, callback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state")}})); got != "expired" {
		t.Errorf("a code reused: code=%s, want expired", got)
	}
	u, _ = startConnect(t, c, "")
	code = f.g.IssueCode(grantFor(u, oid.String(), upn))
	f.h.Advance(2 * time.Minute)
	if _, _, got := outcomeOf(t, callback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state")}})); got != "expired" {
		t.Errorf("a code expired: code=%s, want expired", got)
	}

	// None of it stored a connection or a last error.
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections`); n != 0 {
		t.Errorf("%d connections after failed callbacks, want 0", n)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.connection WHERE outlook_last_error IS NOT NULL`); n != 0 {
		t.Errorf("a last error recorded by failures that are not consent_missing")
	}

	// The tenant refusing the secret answers failed; the anonymous callback
	// never judges the credential, which the application token's path does.
	f.g.Configure(func(g *graphtest.FakeGraph) { g.Secrets[clientID] = []string{"rotated-elsewhere"} })
	if _, _, code := outcomeOf(t, f.authorizeAs(t, c, oid, upn, nil)); code != "failed" {
		t.Errorf("invalid_client: code=%s, want failed", code)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.credentials WHERE last_failed_at IS NOT NULL`); n != 0 {
		t.Errorf("%d secrets marked failed by the callback's invalid_client, want 0", n)
	}
	if logs := f.h.Logs(); strings.Contains(logs, code) || strings.Contains(logs, secretValue) {
		t.Error("the log carries a code or the secret")
	}
}

func TestOutlookCallback_MissingScopeRecordsConsentMissing(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, oid := f.person(t, "kari@nordlys.no")
	r := f.authorizeAs(t, c, oid, "kari@nordlys.no", func(g *graphtest.CodeGrant) { g.Granted = []string{"User.Read", "openid", "profile"} })
	if _, outlook, code := outcomeOf(t, r); outlook != "failed" || code != "consent_missing" {
		t.Errorf("outlook=%s code=%s, want failed consent_missing", outlook, code)
	}
	if got := modtest.One[string](t, f.h.Harness, `SELECT outlook_last_error FROM entra.connection`); got != "consent_missing" {
		t.Errorf("outlook_last_error = %q, want consent_missing", got)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections`); n != 0 {
		t.Errorf("%d connections stored without the feature's scope, want 0", n)
	}
	if r := complete(c); r.Status != http.StatusGone {
		t.Errorf("complete after consent_missing: %d, want 410 (nothing sealed)", r.Status)
	}
}

func TestOutlookCallback_AnOversizedTokenIsRefused(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, oid := f.person(t, "kari@nordlys.no")
	f.g.Configure(func(g *graphtest.FakeGraph) { g.RefreshTokenBytes = 4096 })
	if _, _, code := outcomeOf(t, f.authorizeAs(t, c, oid, "kari@nordlys.no", nil)); code != "failed" {
		t.Errorf("a refresh token too large for a cookie: code=%s, want failed", code)
	}
	if !strings.Contains(f.h.Logs(), "too large") {
		t.Error("the oversized cookie is not logged")
	}
}

func TestOutlookCallback_SuccessSealsTheConnectedCookie(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, oid := f.person(t, "kari@nordlys.no")
	u, _ := startConnect(t, c, "/calendar/settings")
	code := f.g.IssueCode(grantFor(u, oid.String(), "kari@nordlys.no"))
	r := callback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state")}, "session_state": {"ignored"}})
	if path, outlook, got := outcomeOf(t, r); path != "/settings/connections" || outlook != "complete" || got != "" {
		t.Errorf("%s outlook=%s code=%s, want /settings/connections complete", path, outlook, got)
	}
	cookie := r.Header("Set-Cookie")
	for _, want := range []string{"vantigo.entra.connected=", "Path=/api/v1/entra/outlook", "Max-Age=300", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("Set-Cookie %q lacks %q", cookie, want)
		}
	}
	if r.Header("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", r.Header("Cache-Control"))
	}
	// The state cookie is spent.
	if _, _, got := outcomeOf(t, callback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state")}})); got != "expired" {
		t.Errorf("the callback replayed: code=%s, want expired", got)
	}
	// Nothing is stored before complete.
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections`); n != 0 {
		t.Errorf("%d connections before complete, want 0", n)
	}
}

// --- complete ----------------------------------------------------------------

func TestOutlookComplete_Connects(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	f.h.Exec(t, `UPDATE entra.connection SET outlook_last_error = 'consent_missing', outlook_last_error_at = $1`, f.h.Now())

	u, _ := startConnect(t, c, "/calendar/settings")
	code := f.g.IssueCode(grantFor(u, oid.String(), "kari@nordlys.no"))
	callback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state")}})
	r := complete(c)
	if r.Status != http.StatusOK {
		t.Fatalf("complete: %d %s", r.Status, r.Body)
	}
	var got gen.EntraOutlookComplete
	r.JSON(&got)
	if got.ReturnTo != "/calendar/settings" {
		t.Errorf("returnTo = %q", got.ReturnTo)
	}
	conn := got.Connection
	if !conn.Offered || conn.State != gen.EntraOutlookConnectionStateActive || conn.Account == nil || *conn.Account != "kari@nordlys.no" ||
		!slices.Equal(conn.Features, []string{"calendar"}) || conn.ConnectedAt == nil {
		t.Errorf("connection = %+v", conn)
	}
	if !slices.Equal(conn.Scopes, []string{"calendars.readwrite", "openid", "profile", "user.read"}) {
		t.Errorf("scopes = %v, want lower-cased and sorted", conn.Scopes)
	}
	if !strings.Contains(r.Header("Set-Cookie"), "vantigo.entra.connected=;") {
		t.Errorf("Set-Cookie %q, want the connected cookie cleared", r.Header("Set-Cookie"))
	}

	var stored struct {
		UserID   uuid.UUID
		State    string
		Revision int64
		Sealed   []byte
	}
	if err := f.h.Pool().QueryRow(context.Background(), `SELECT user_id, state, revision, refresh_sealed FROM entra.outlook_connections WHERE object_id = $1`, oid).
		Scan(&stored.UserID, &stored.State, &stored.Revision, &stored.Sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Sealed), "1.A") {
		t.Error("the refresh token is stored in the clear")
	}
	if stored.UserID != userID || stored.State != "active" || stored.Revision != 1 || len(stored.Sealed) == 0 {
		t.Errorf("row = %+v", stored)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.connection WHERE outlook_last_error IS NOT NULL`); n != 0 {
		t.Error("the last error is not cleared by a working connect")
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.connected' AND object_id = $1 AND upn = 'kari@nordlys.no' AND codes = '{feature:calendar}' AND actor_user_id = $2`, oid, userID); n != 1 {
		t.Errorf("%d connected events, want 1", n)
	}

	// The cookie is single use.
	if r := complete(c); r.Status != http.StatusGone || codeOf(t, r) != "expired" {
		t.Errorf("a second complete: %d %s, want 410 expired", r.Status, r.Body)
	}
	if strings.Contains(f.h.Logs(), code) {
		t.Error("the log carries the code")
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE array_to_string(codes, ' ') LIKE '%' || $1 || '%'`, code[:20]); n != 0 {
		t.Error("an event carries the code")
	}
}

func TestOutlookComplete_RefusesAnotherSessionOrUser(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	other, _, _ := f.person(t, "ola@nordlys.no")

	if r := complete(c); r.Status != http.StatusGone || codeOf(t, r) != "expired" {
		t.Errorf("no cookie: %d %s, want 410 expired", r.Status, r.Body)
	}

	r := f.authorizeAs(t, c, oid, "kari@nordlys.no", nil)
	sealed := strings.TrimPrefix(strings.SplitN(r.Header("Set-Cookie"), ";", 2)[0], "vantigo.entra.connected=")
	if sealed == "" {
		t.Fatal("no connected cookie")
	}
	sameUser := secondSession(t, f.h, userID)
	sameUser.SetCookie("vantigo.entra.connected", sealed)
	if r := complete(sameUser); r.Status != http.StatusGone || codeOf(t, r) != "expired" {
		t.Errorf("another session of the same person: %d %s, want 410", r.Status, r.Body)
	}
	other.SetCookie("vantigo.entra.connected", sealed)
	if r := complete(other); r.Status != http.StatusGone || codeOf(t, r) != "expired" {
		t.Errorf("another person: %d %s, want 410", r.Status, r.Body)
	}
	// A state cookie's value is not a connected cookie, even its owner's.
	_, stateCookie := startConnect(t, other, "")
	other.SetCookie("vantigo.entra.connected", strings.TrimPrefix(strings.SplitN(stateCookie, ";", 2)[0], "vantigo.entra.connect="))
	if r := complete(other); r.Status != http.StatusGone {
		t.Errorf("the state cookie as the connected one: %d, want 410", r.Status)
	}
	// An expired connected cookie.
	f.authorizeAs(t, c, oid, "kari@nordlys.no", nil)
	f.h.Advance(6 * time.Minute)
	if r := complete(c); r.Status != http.StatusGone {
		t.Errorf("an expired cookie: %d, want 410", r.Status)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections`); n != 0 {
		t.Errorf("%d connections stored, want 0", n)
	}
}

func TestOutlookComplete_RechecksTheSwitchAndTheAccount(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")

	f.authorizeAs(t, c, oid, "kari@nordlys.no", nil)
	setOutlookConnect(t, f.o, false)
	if r := complete(c); r.Status != http.StatusConflict || codeOf(t, r) != "switch_off" {
		t.Errorf("the switch turned off meanwhile: %d %s, want 409 switch_off", r.Status, r.Body)
	}
	setOutlookConnect(t, f.o, true)

	f.authorizeAs(t, c, oid, "kari@nordlys.no", nil)
	f.w.set(userID, nil)
	if r := complete(c); r.Status != http.StatusConflict || codeOf(t, r) != "no_account" {
		t.Errorf("the account gone meanwhile: %d %s, want 409 no_account", r.Status, r.Body)
	}
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: oid.String(), Enabled: true})

	// Signed in to Microsoft as another account of the tenant.
	stranger := uuid.New()
	f.authorizeAs(t, c, stranger, "boss@nordlys.no", nil)
	r := complete(c)
	if r.Status != http.StatusConflict || codeOf(t, r) != "account_mismatch" {
		t.Fatalf("another account: %d %s, want 409 account_mismatch", r.Status, r.Body)
	}
	if p := problemOf(t, r); p.Detail == nil || !strings.Contains(*p.Detail, "boss@nordlys.no") || !strings.Contains(*p.Detail, "kari@nordlys.no") {
		t.Errorf("detail = %v, want both accounts named", p.Detail)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections`); n != 0 {
		t.Errorf("%d connections stored, want 0", n)
	}
}

func TestOutlookComplete_TakenClosingAndRepointed(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	kari, kariID, oid := f.person(t, "kari@nordlys.no")
	ola, olaID, _ := f.person(t, "ola@nordlys.no")

	// Ola's Staff record names Kari's account too: Kari connected first.
	f.w.set(olaID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: oid.String(), Enabled: true})
	f.authorizeAs(t, ola, oid, "kari@nordlys.no", nil)
	if r := f.connectAs(t, kari, oid, "kari@nordlys.no"); r.Status != http.StatusOK {
		t.Fatalf("Kari's connect: %d %s", r.Status, r.Body)
	}
	if r := complete(ola); r.Status != http.StatusConflict || codeOf(t, r) != "account_taken" {
		t.Errorf("Ola completing over Kari's live row: %d %s, want 409 account_taken", r.Status, r.Body)
	}

	// Kari disconnects; the closing row is taken over by Ola.
	if r := kari.Do(http.MethodDelete, outlookConnectionPath, nil); r.Status != http.StatusNoContent {
		t.Fatalf("DELETE: %d", r.Status)
	}
	if r := f.connectAs(t, ola, oid, "kari@nordlys.no"); r.Status != http.StatusOK {
		t.Fatalf("Ola over the closing row: %d %s", r.Status, r.Body)
	}
	var got struct {
		UserID   uuid.UUID
		State    string
		Revision int64
	}
	if err := f.h.Pool().QueryRow(context.Background(), `SELECT user_id, state, revision FROM entra.outlook_connections WHERE object_id = $1`, oid).
		Scan(&got.UserID, &got.State, &got.Revision); err != nil {
		t.Fatal(err)
	}
	if got.UserID != olaID || got.State != "active" || got.Revision != 3 {
		t.Errorf("the row taken over = %+v, want Ola's, active, revision 3", got)
	}

	// Ola's Staff record is re-pointed at an account of his own: his old
	// live row closes, keeping its token.
	newOID := addAccount(t, f.h, "E-ola2", "ola@nordlys.no")
	f.h.Exec(t, `UPDATE entra.accounts SET state = 'enabled' WHERE object_id = $1`, newOID)
	f.w.set(olaID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: newOID.String(), Enabled: true})
	if r := f.connectAs(t, ola, newOID, "ola@nordlys.no"); r.Status != http.StatusOK {
		t.Fatalf("Ola's re-pointed connect: %d %s", r.Status, r.Body)
	}
	if got := modtest.One[string](t, f.h.Harness, `SELECT state FROM entra.outlook_connections WHERE object_id = $1 AND refresh_sealed IS NOT NULL`, oid); got != "closing" {
		t.Errorf("the old row = %s, want closing with its token", got)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.closed' AND object_id = $1 AND codes = '{repointed}'`, oid); n != 1 {
		t.Errorf("%d repointed events, want 1", n)
	}
	_ = kariID
}

func TestOutlookComplete_TwoAtOnceOneTaken(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	kari, _, oid := f.person(t, "kari@nordlys.no")
	ola, olaID, _ := f.person(t, "ola@nordlys.no")
	f.w.set(olaID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: oid.String(), Enabled: true})
	f.authorizeAs(t, kari, oid, "kari@nordlys.no", nil)
	f.authorizeAs(t, ola, oid, "kari@nordlys.no", nil)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	codes := make([]string, 2)
	for i, c := range []*modtest.Client{kari, ola} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := complete(c)
			statuses[i] = r.Status
			if r.Status != http.StatusOK {
				var p gen.EntraProblem
				r.JSON(&p)
				if p.Code != nil {
					codes[i] = *p.Code
				}
			}
		}()
	}
	wg.Wait()
	slices.Sort(statuses)
	if statuses[0] != http.StatusOK || statuses[1] != http.StatusConflict || !slices.Contains(codes, "account_taken") {
		t.Errorf("statuses %v codes %v, want one 200 and one 409 account_taken", statuses, codes)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections`); n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
}

// lockGate runs stmt in a transaction of its own, holding the row locks it
// takes until release commits it (or the test ends, rolling it back).
func lockGate(t *testing.T, h *harness, stmt string, args ...any) (release func()) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, stmt, args...); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// awaitLockWaiters waits until n backends on h's database wait on a lock,
// failing t if finished closes first or ten seconds pass (customers'
// contacts_concurrency_test.go's, duplicated: it is another package's).
func awaitLockWaiters(t *testing.T, h *harness, n int, finished <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) < n {
		select {
		case <-finished:
			t.Fatalf("the requests answered without waiting on the gate's lock")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d requests ever waited on the gate's lock", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// completeBehind runs complete for each client at once while gate holds its
// lock, waits until n of them wait on it, releases it and answers each.
func completeBehind(t *testing.T, h *harness, release func(), n int, clients ...*modtest.Client) []*modtest.Response {
	t.Helper()
	out := make([]*modtest.Response, len(clients))
	finished := make(chan struct{})
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = complete(c)
		}()
	}
	go func() { wg.Wait(); close(finished) }()
	awaitLockWaiters(t, h, n, finished)
	release()
	<-finished
	return out
}

// The switch turned off, or the account disabled, while complete runs: the
// transaction reads both again under their locks, so neither a switch-off
// nor a leaver's Deactivate can leave a live connection behind it.
func TestOutlookComplete_RacesTheSwitchAndTheLeaver(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, oid := f.person(t, "kari@nordlys.no")

	f.authorizeAs(t, c, oid, "kari@nordlys.no", nil)
	release := lockGate(t, f.h, `UPDATE entra.connection SET outlook_connect = false`)
	if r := completeBehind(t, f.h, release, 1, c)[0]; r.Status != http.StatusConflict || codeOf(t, r) != "switch_off" {
		t.Errorf("the switch turned off during complete: %d %s, want 409 switch_off", r.Status, r.Body)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections`); n != 0 {
		t.Errorf("%d connections behind the switch-off, want 0", n)
	}
	f.h.Exec(t, `UPDATE entra.connection SET outlook_connect = true`)

	f.authorizeAs(t, c, oid, "kari@nordlys.no", nil)
	release = lockGate(t, f.h, `UPDATE entra.accounts SET state = 'disabled' WHERE object_id = $1`, oid)
	if r := completeBehind(t, f.h, release, 1, c)[0]; r.Status != http.StatusConflict || codeOf(t, r) != "no_account" {
		t.Errorf("the account disabled during complete: %d %s, want 409 no_account", r.Status, r.Body)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections`); n != 0 {
		t.Errorf("%d connections behind the leaver, want 0", n)
	}
}

// One connect submitted twice at once is the caller's own connection both
// times, never account_taken.
func TestOutlookComplete_ADoubleSubmitIsTheCallersOwn(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	f.authorizeAs(t, c, oid, "kari@nordlys.no", nil)
	release := lockGate(t, f.h, `SELECT 1 FROM entra.connection WHERE id = 1 FOR UPDATE`)
	for i, r := range completeBehind(t, f.h, release, 2, c, c) {
		if r.Status != http.StatusOK {
			t.Errorf("submit %d: %d %s, want 200", i, r.Status, r.Body)
		}
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1 AND user_id = $2 AND state = 'active'`, oid, userID); n != 1 {
		t.Errorf("%d active rows of the caller's, want 1", n)
	}
}

// A new connect clears the connected cookie an abandoned one left, so it
// can never complete after it.
func TestOutlookConnect_ClearsAStaleConnectedCookie(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, oid := f.person(t, "kari@nordlys.no")
	f.authorizeAs(t, c, oid, "kari@nordlys.no", nil)
	startConnect(t, c, "")
	if r := complete(c); r.Status != http.StatusGone || codeOf(t, r) != "expired" {
		t.Errorf("complete after a new connect: %d %s, want 410 expired", r.Status, r.Body)
	}
}

// --- the person's own reads --------------------------------------------------

func TestOutlookConnection_GetAndDelete(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")

	got := getOutlook(t, c)
	if !got.Offered || got.Reason != nil || got.State != gen.EntraOutlookConnectionStateNotConnected || got.Account == nil || *got.Account != "kari@nordlys.no" {
		t.Errorf("not connected = %+v", got)
	}
	if got := getOutlook(t, f.h.SignIn(t)); got.Offered || got.Reason == nil || *got.Reason != gen.NoAccount || got.Account != nil {
		t.Errorf("no account = %+v", got)
	}

	if r := f.connectAs(t, c, oid, "kari@nordlys.no"); r.Status != http.StatusOK {
		t.Fatalf("connect: %d %s", r.Status, r.Body)
	}
	if got := getOutlook(t, c); got.State != gen.EntraOutlookConnectionStateActive || !slices.Equal(got.Features, []string{"calendar"}) || got.ErrorCode != nil {
		t.Errorf("active = %+v", got)
	}
	f.h.Exec(t, `UPDATE entra.outlook_connections SET state = 'reconnect', error_code = 'mfa' WHERE object_id = $1`, oid)
	if got := getOutlook(t, c); got.State != gen.EntraOutlookConnectionStateReconnect || got.ErrorCode == nil || *got.ErrorCode != "mfa" {
		t.Errorf("reconnect = %+v", got)
	}
	f.h.Exec(t, `UPDATE entra.outlook_connections SET state = 'active', error_code = NULL WHERE object_id = $1`, oid)

	if r := c.Do(http.MethodDelete, outlookConnectionPath, nil); r.Status != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", r.Status, r.Body)
	}
	if got := modtest.One[string](t, f.h.Harness, `SELECT state FROM entra.outlook_connections WHERE object_id = $1 AND refresh_sealed IS NOT NULL`, oid); got != "closing" {
		t.Errorf("after DELETE: %s, want closing with its token kept", got)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.disconnected' AND object_id = $1 AND actor_user_id = $2`, oid, userID); n != 1 {
		t.Errorf("%d disconnected events, want 1", n)
	}
	if got := getOutlook(t, c); got.State != gen.EntraOutlookConnectionStateClosing || !got.Offered {
		t.Errorf("closing = %+v", got)
	}
	if r := c.Do(http.MethodDelete, outlookConnectionPath, nil); r.Status != http.StatusNoContent {
		t.Errorf("a second DELETE: %d, want 204", r.Status)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.disconnected'`); n != 1 {
		t.Errorf("%d disconnected events after a second DELETE, want 1", n)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, userID); n != 1 {
		t.Error("a disconnect touched the person's sessions")
	}

	setOutlookConnect(t, f.o, false)
	if got := getOutlook(t, c); got.Offered || got.Reason == nil || *got.Reason != gen.SwitchOff {
		t.Errorf("the switch off = %+v", got)
	}
}
