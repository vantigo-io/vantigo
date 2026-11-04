package google_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The person's own Google connection (Google Workspace design G25–G29,
// delivery D): connect, the callback, complete, the person's own reads, the
// switch, the token and its failures, the closes and google-sweep, against
// the fake's authorization server.

// The personal connection's paths.
const (
	personalConnectPath    = "/api/v1/google/connect"
	personalCallbackPath   = "/api/v1/google/connect/callback"
	personalCompletePath   = "/api/v1/google/complete"
	personalMePath         = "/api/v1/google/connection/me"
	personalSwitchPath     = "/api/v1/google/connection/personal"
	scopeCalendarEvents    = "https://www.googleapis.com/auth/calendar.events"
	scopeCalendarAppCreate = "https://www.googleapis.com/auth/calendar.app.created"
	scopeGmailSend         = "https://www.googleapis.com/auth/gmail.send"
)

// fakeWorkforce is Staff as the one method the module calls answers it,
// DirectoryAccount: the account set for the principal in ctx, nil for anyone
// else. Every other method is the embedded nil interface and panics if
// called.
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

// personalFixture is a connected organisation with the web client set up and
// Connection for people on, Staff answering each person's directory account.
type personalFixture struct {
	h *harness
	g *googletest.FakeGoogle
	o *modtest.Client
	w *fakeWorkforce
}

func newPersonalFixture(t *testing.T, opts ...modtest.Option) *personalFixture {
	t.Helper()
	w := newFakeWorkforce()
	h := newHarness(t, append([]modtest.Option{modtest.WithWorkforce(w)}, opts...)...)
	h.g.Delegate(saClientID, everyScope...)
	o, _ := configured(t, h)
	if r := testConnection(t, o); r.Status != "connected" {
		t.Fatalf("the test: %+v", r)
	}
	h.g.AddWebClient(webClientID, webSecret)
	if r := o.Do(http.MethodPut, oauthClientPath, map[string]any{"clientId": webClientID, "clientSecret": webSecret}); r.Status != http.StatusOK {
		t.Fatalf("PUT /connection/oauth-client: %d %s", r.Status, r.Body)
	}
	setPersonal(t, o, true)
	return &personalFixture{h: h, g: h.g, o: o, w: w}
}

// setPersonal is PUT /connection/personal, which must answer 200.
func setPersonal(t *testing.T, o *modtest.Client, enabled bool) gen.GoogleConnection {
	t.Helper()
	r := o.Do(http.MethodPut, personalSwitchPath, map[string]any{"enabled": enabled})
	if r.Status != http.StatusOK {
		t.Fatalf("PUT /connection/personal: %d %s", r.Status, r.Body)
	}
	var conn gen.GoogleConnection
	r.JSON(&conn)
	return conn
}

// person is a signed-in user holding nothing whose Staff record names an
// enabled Google account the module created: the client, the user and the
// account's Directory id.
func (f *personalFixture) person(t *testing.T, email string) (*modtest.Client, uuid.UUID, string) {
	t.Helper()
	c, userID := f.h.SignInUser(t)
	gid := f.account(t, email)
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "google", ExternalID: gid, Enabled: true})
	return c, userID, gid
}

// account is a user at Google and the module's row for it, created by
// Vantigo and enabled: its Directory id.
func (f *personalFixture) account(t *testing.T, email string) string {
	t.Helper()
	u := f.g.AddUser(googletest.User{PrimaryEmail: email, GivenName: "Kari", FamilyName: "Nordmann"})
	number, _, _ := strings.Cut(email, "@")
	if len(number) > 12 {
		number = number[:12]
	}
	f.h.Exec(t, `INSERT INTO google.accounts (user_id, employee_number, primary_email, state, created_by_us, created_at, updated_at)
	             VALUES ($1, $2, $3, 'enabled', true, $4, $4)`, u.ID, "E-"+number, email, f.h.Now())
	return u.ID
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

// startPersonal is POST /connect for feature, which must answer 200: the
// authorization URL and the Set-Cookie header.
func startPersonal(t *testing.T, c *modtest.Client, feature, returnTo string) (*url.URL, string) {
	t.Helper()
	body := map[string]any{"feature": feature}
	if returnTo != "" {
		body["returnTo"] = returnTo
	}
	r := c.Do(http.MethodPost, personalConnectPath, body)
	if r.Status != http.StatusOK {
		t.Fatalf("POST /connect: %d %s", r.Status, r.Body)
	}
	var a gen.GooglePersonalAuthorize
	r.JSON(&a)
	u, err := url.Parse(a.AuthorizeUrl)
	if err != nil {
		t.Fatal(err)
	}
	return u, r.Header("Set-Cookie")
}

// personalCallback is Google's redirect back with query.
func personalCallback(t *testing.T, c *modtest.Client, query url.Values) *modtest.Response {
	t.Helper()
	r := c.Do(http.MethodGet, personalCallbackPath+"?"+query.Encode(), nil)
	if r.Status != http.StatusFound {
		t.Fatalf("GET /connect/callback: %d %s", r.Status, r.Body)
	}
	return r
}

// personalOutcome is a callback's redirect: the page's path, connection,
// outcome and code.
func personalOutcome(t *testing.T, r *modtest.Response) (string, string, string, string) {
	t.Helper()
	u, err := url.Parse(r.Header("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	return u.Path, q.Get("connection"), q.Get("outcome"), q.Get("code")
}

// failedCode is the callback's code, failing the test when it succeeded.
func failedCode(t *testing.T, r *modtest.Response) string {
	t.Helper()
	_, _, outcome, code := personalOutcome(t, r)
	if outcome != "failed" {
		t.Errorf("outcome %q, want failed", outcome)
	}
	return code
}

// authorizeAs runs connect for feature and Google's part for c as the
// account gid with email, hd its domain, change applied to the grant: the
// callback's answer.
func (f *personalFixture) authorizeAs(t *testing.T, c *modtest.Client, feature, gid, email string, change func(*googletest.CodeGrant)) *modtest.Response {
	t.Helper()
	u, _ := startPersonal(t, c, feature, "")
	_, domain, _ := strings.Cut(email, "@")
	grant := googletest.GrantFor(u, gid, email, domain)
	if change != nil {
		change(&grant)
	}
	return personalCallback(t, c, url.Values{"code": {f.g.IssueCode(grant)}, "state": {u.Query().Get("state")}, "scope": {strings.Join(grant.Scopes, " ")}})
}

// completePersonal is POST /complete.
func completePersonal(c *modtest.Client) *modtest.Response {
	return c.Do(http.MethodPost, personalCompletePath, nil)
}

// connectAs runs the whole flow for c as gid for the calendar; complete's
// answer.
func (f *personalFixture) connectAs(t *testing.T, c *modtest.Client, gid, email string) *modtest.Response {
	t.Helper()
	r := f.authorizeAs(t, c, "calendar", gid, email, nil)
	if _, _, outcome, code := personalOutcome(t, r); outcome != "complete" {
		t.Fatalf("callback: outcome=%s code=%s", outcome, code)
	}
	return completePersonal(c)
}

// getMe is GET /connection/me, which always answers 200.
func getMe(t *testing.T, c *modtest.Client) gen.GooglePersonalConnection {
	t.Helper()
	r := c.Do(http.MethodGet, personalMePath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /connection/me: %d %s", r.Status, r.Body)
	}
	var conn gen.GooglePersonalConnection
	r.JSON(&conn)
	return conn
}

// written is what the flow may write: the connections, the connection's
// last error and the events.
func (f *personalFixture) written(t *testing.T) [3]int {
	t.Helper()
	return [3]int{
		f.h.Count(t, `SELECT count(*) FROM google.connections`),
		f.h.Count(t, `SELECT count(*) FROM google.connection WHERE personal_last_error IS NOT NULL`),
		f.h.Count(t, `SELECT count(*) FROM google.events`),
	}
}

// rowState is the account's connection's state and error code, "" without
// one.
func (f *personalFixture) rowState(t *testing.T, gid string) (string, string) {
	t.Helper()
	var state, code string
	if err := f.h.Pool().QueryRow(context.Background(),
		`SELECT state, COALESCE(error_code, '') FROM google.connections WHERE google_user_id = $1`, gid).Scan(&state, &code); err != nil {
		return "", ""
	}
	return state, code
}

// personalEvents are the codes of each event of action about gid, oldest
// first.
func (f *personalFixture) personalEvents(t *testing.T, action, gid string) [][]string {
	t.Helper()
	rows, err := f.h.Pool().Query(context.Background(),
		`SELECT codes FROM google.events WHERE action = $1 AND subject_id = $2 ORDER BY id`, action, gid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		var codes []string
		if err := rows.Scan(&codes); err != nil {
			t.Fatal(err)
		}
		out = append(out, codes)
	}
	return out
}

// noSecretsLeaked fails the test if a refresh token the fake issued, the
// web client's secret or Google's words reached the log or an event.
func (f *personalFixture) noSecretsLeaked(t *testing.T, subs ...string) {
	t.Helper()
	logs := f.h.Logs()
	events := ""
	rows, err := f.h.Pool().Query(context.Background(), `SELECT action || ' ' || COALESCE(subject_id, '') || ' ' || array_to_string(codes, ',') FROM google.events`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		events += line + "\n"
	}
	rows.Close()
	secrets := []string{webSecret, f.g.Words}
	for _, sub := range subs {
		secrets = append(secrets, f.g.RefreshTokens(sub)...)
	}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		if strings.Contains(logs, s) {
			t.Errorf("the log holds a secret or Google's words: %.12q…", s)
		}
		if strings.Contains(events, s) {
			t.Errorf("an event holds a secret or Google's words: %.12q…", s)
		}
	}
	if strings.Contains(events, "@") {
		t.Errorf("an event holds an address: %s", events)
	}
}

// --- connect -----------------------------------------------------------------

func TestPersonalConnect_TheAuthorizationURLAndTheCookie(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, _, _ := f.person(t, "kari@nordlys.no")

	u, cookie := startPersonal(t, c, "calendar", "/calendar/settings")
	if u.Scheme != "https" || u.Host != "accounts.google.com" || u.Path != "/o/oauth2/v2/auth" {
		t.Errorf("authorize endpoint = %s", u.Scheme+"://"+u.Host+u.Path)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id":              webClientID,
		"response_type":          "code",
		"redirect_uri":           "http://modules.example.com/api/v1/google/connect/callback",
		"scope":                  "openid email profile " + scopeCalendarAppCreate + " " + scopeCalendarEvents,
		"code_challenge_method":  "S256",
		"access_type":            "offline",
		"prompt":                 "consent",
		"include_granted_scopes": "true",
		"login_hint":             "kari@nordlys.no",
		"hd":                     "nordlys.no",
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
	for _, want := range []string{"vantigo.google.connect=", "Path=/api/v1/google", "Max-Age=600", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("Set-Cookie %q lacks %q", cookie, want)
		}
	}
	if strings.Contains(cookie, q.Get("state")) || strings.Contains(cookie, q.Get("nonce")) {
		t.Error("the cookie carries the state or the nonce in the clear")
	}
}

func TestPersonalConnect_Refusals(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)

	c, userID, gid := f.person(t, "kari@nordlys.no")
	r := c.Do(http.MethodPost, personalConnectPath, map[string]any{"feature": "drive"}, modtest.SkipContract("an unknown feature, which the contract's enum refuses"))
	if r.Status != http.StatusUnprocessableEntity || codeOf(t, r) != "feature_unknown" {
		t.Errorf("an unknown feature: %d %s, want 422 feature_unknown", r.Status, r.Body)
	}

	// No account Vantigo manages: none in Staff, another provider's, one
	// disabled in Staff, an adopted one nobody confirmed, a suspended one.
	refusedNoAccount := func(why string) {
		t.Helper()
		r := c.Do(http.MethodPost, personalConnectPath, map[string]any{"feature": "calendar"})
		if r.Status != http.StatusConflict || codeOf(t, r) != "no_account" {
			t.Errorf("%s: %d %s, want 409 no_account", why, r.Status, r.Body)
		}
	}
	f.w.set(userID, nil)
	refusedNoAccount("no directory account")
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: gid, Enabled: true})
	refusedNoAccount("another provider's account")
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "google", ExternalID: gid, Enabled: false})
	refusedNoAccount("a disabled link")
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "google", ExternalID: gid, Enabled: true})
	f.h.Exec(t, `UPDATE google.accounts SET created_by_us = false WHERE user_id = $1`, gid)
	refusedNoAccount("an unconfirmed adoption")
	f.h.Exec(t, `UPDATE google.accounts SET created_by_us = true, state = 'disabled' WHERE user_id = $1`, gid)
	refusedNoAccount("a suspended account")
	f.h.Exec(t, `UPDATE google.accounts SET state = 'enabled' WHERE user_id = $1`, gid)

	// Another person's live connection holds the account.
	f.connectAs(t, c, gid, "kari@nordlys.no")
	other, otherID := f.h.SignInUser(t)
	f.w.set(otherID, &contracts.DirectoryAccount{Provider: "google", ExternalID: gid, Enabled: true})
	if r := other.Do(http.MethodPost, personalConnectPath, map[string]any{"feature": "calendar"}); r.Status != http.StatusConflict || codeOf(t, r) != "account_taken" {
		t.Errorf("an account another holds: %d %s, want 409 account_taken", r.Status, r.Body)
	}

	// The switch off, or the web client not ready, is switch_off.
	setPersonal(t, f.o, false)
	if r := c.Do(http.MethodPost, personalConnectPath, map[string]any{"feature": "calendar"}); r.Status != http.StatusConflict || codeOf(t, r) != "switch_off" {
		t.Errorf("the switch off: %d %s, want 409 switch_off", r.Status, r.Body)
	}
	setPersonal(t, f.o, true)
	f.h.Exec(t, `UPDATE google.credentials SET revoked_at = now() WHERE kind = 'oauth_client_secret'`)
	if r := c.Do(http.MethodPost, personalConnectPath, map[string]any{"feature": "calendar"}); r.Status != http.StatusConflict || codeOf(t, r) != "switch_off" {
		t.Errorf("no web client secret: %d %s, want 409 switch_off", r.Status, r.Body)
	}
}

func TestPersonalConnect_AsksTheHeldFeaturesAgain(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, _, gid := f.person(t, "kari@nordlys.no")
	if r := f.connectAs(t, c, gid, "kari@nordlys.no"); r.Status != http.StatusOK {
		t.Fatalf("complete: %d %s", r.Status, r.Body)
	}
	u, _ := startPersonal(t, c, "mail", "")
	want := "openid email profile " + scopeCalendarAppCreate + " " + scopeCalendarEvents + " " + scopeGmailSend
	if got := u.Query().Get("scope"); got != want {
		t.Errorf("scope = %q, want %q (the calendar asked again beside mail)", got, want)
	}
}

// --- the callback ------------------------------------------------------------

func TestPersonalCallback_WithoutAValidStateWritesNothing(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, _, gid := f.person(t, "kari@nordlys.no")
	before := f.written(t)

	stranger := f.h.Client(t)
	r := personalCallback(t, stranger, url.Values{"code": {"4/0Aany"}, "state": {"any"}})
	if path, connection, outcome, code := personalOutcome(t, r); path != "/settings/connections" || connection != "google" || outcome != "failed" || code != "expired" {
		t.Errorf("no cookie: %s connection=%s outcome=%s code=%s, want /settings/connections google failed expired", path, connection, outcome, code)
	}
	if sc := r.Header("Set-Cookie"); !strings.Contains(sc, "vantigo.google.connect=;") || !strings.Contains(sc, "Max-Age=0") {
		t.Errorf("no cookie: Set-Cookie %q, want the state cookie cleared", sc)
	}
	if r.Header("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", r.Header("Cache-Control"))
	}

	// A wrong state, with a grant that would record consent_missing under
	// a valid one.
	u, _ := startPersonal(t, c, "calendar", "")
	grant := googletest.GrantFor(u, gid, "kari@nordlys.no", "nordlys.no")
	grant.Granted = []string{"openid", "email", "profile"}
	code := f.g.IssueCode(grant)
	if got := failedCode(t, personalCallback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state") + "x"}})); got != "expired" {
		t.Errorf("a wrong state: code=%s, want expired", got)
	}
	// The state cookie is spent: the right state no longer helps.
	if got := failedCode(t, personalCallback(t, c, url.Values{"code": {code}, "state": {u.Query().Get("state")}})); got != "expired" {
		t.Errorf("the right state after a refusal: code=%s, want expired", got)
	}
	// A forged cookie.
	u, _ = startPersonal(t, c, "calendar", "")
	forger := f.h.Client(t)
	forger.SetCookie("vantigo.google.connect", "bm90LWEtc2VhbGVkLWNvb2tpZQ")
	if got := failedCode(t, personalCallback(t, forger, url.Values{"code": {code}, "state": {u.Query().Get("state")}})); got != "expired" {
		t.Errorf("a forged cookie: code=%s, want expired", got)
	}
	// An expired one: eleven minutes at Google.
	u, _ = startPersonal(t, c, "calendar", "")
	f.h.Advance(11 * time.Minute)
	grant = googletest.GrantFor(u, gid, "kari@nordlys.no", "nordlys.no")
	grant.Granted = []string{"openid"}
	if got := failedCode(t, personalCallback(t, c, url.Values{"code": {f.g.IssueCode(grant)}, "state": {u.Query().Get("state")}})); got != "expired" {
		t.Errorf("an expired cookie: code=%s, want expired", got)
	}

	if after := f.written(t); after != before {
		t.Errorf("written %v → %v, want nothing written without a valid state", before, after)
	}
	for _, call := range f.g.Calls() {
		if call.Host == googletest.TokenHost && call.Path == "/token" && call.Subject == "" {
			t.Errorf("a code was redeemed without a valid state: %+v", call)
		}
	}
}

func TestPersonalCallback_Failures(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, _, gid := f.person(t, "kari@nordlys.no")
	email := "kari@nordlys.no"

	// Google's own refusals.
	for errCode, want := range map[string]string{"access_denied": "access_denied", "server_error": "failed"} {
		u, _ := startPersonal(t, c, "calendar", "")
		if got := failedCode(t, personalCallback(t, c, url.Values{"error": {errCode}, "error_description": {f.g.Words}, "state": {u.Query().Get("state")}})); got != want {
			t.Errorf("error=%s: code=%s, want %s", errCode, got, want)
		}
	}
	u, _ := startPersonal(t, c, "calendar", "")
	if got := failedCode(t, personalCallback(t, c, url.Values{"state": {u.Query().Get("state")}})); got != "failed" {
		t.Errorf("no code: %s, want failed", got)
	}

	// A code issued for another verifier is Google's invalid_grant: expired.
	if got := failedCode(t, f.authorizeAs(t, c, "calendar", gid, email, func(g *googletest.CodeGrant) { g.Challenge = "another" })); got != "expired" {
		t.Errorf("another verifier: %s, want expired", got)
	}

	// The ID token: another audience, another issuer, another nonce, a key
	// nobody published, each failed.
	for name, change := range map[string]func(map[string]any) bool{
		"audience":  func(c map[string]any) bool { c["aud"] = "other.apps.googleusercontent.com"; return true },
		"issuer":    func(c map[string]any) bool { c["iss"] = "https://evil.example"; return true },
		"nonce":     func(c map[string]any) bool { c["nonce"] = "another"; return true },
		"signature": func(map[string]any) bool { return false },
		"expired":   func(c map[string]any) bool { c["exp"] = c["iat"].(int64) - 3600; return true },
	} {
		f.g.IDTokenFor(change)
		if got := failedCode(t, f.authorizeAs(t, c, "calendar", gid, email, nil)); got != "failed" {
			t.Errorf("the ID token's %s: %s, want failed", name, got)
		}
	}
	f.g.IDTokenFor(nil)

	// hd outside the verified domains, or none (a consumer account).
	for _, hd := range []string{"elsewhere.example", ""} {
		if got := failedCode(t, f.authorizeAs(t, c, "calendar", gid, email, func(g *googletest.CodeGrant) { g.HD = hd })); got != "domain_mismatch" {
			t.Errorf("hd %q: %s, want domain_mismatch", hd, got)
		}
	}
	// A secondary verified domain, any case, is the organisation's.
	if _, _, outcome, code := personalOutcome(t, f.authorizeAs(t, c, "calendar", gid, email, func(g *googletest.CodeGrant) { g.HD = "nordlys.EXAMPLE" })); outcome != "complete" {
		t.Errorf("a secondary domain: %s %s, want complete", outcome, code)
	}

	if n := f.h.Count(t, `SELECT count(*) FROM google.connection WHERE personal_last_error IS NOT NULL`); n != 0 {
		t.Errorf("a person's refusal recorded on the connection (%d)", n)
	}

	// Granular consent without calendar.app.created: consent_missing,
	// recorded for the Owner, nothing sealed.
	r := f.authorizeAs(t, c, "calendar", gid, email, func(g *googletest.CodeGrant) {
		g.Granted = []string{"openid", "https://www.googleapis.com/auth/userinfo.email", scopeCalendarEvents}
	})
	if got := failedCode(t, r); got != "consent_missing" {
		t.Errorf("a feature's scope refused: %s, want consent_missing", got)
	}
	if strings.Contains(r.Header("Set-Cookie"), "vantigo.google.connected=") && !strings.Contains(r.Header("Set-Cookie"), "vantigo.google.connected=;") {
		t.Error("consent_missing sealed a connected cookie")
	}
	if conn := getConnection(t, f.o); conn.PersonalConnection.Status != gen.ConsentMissing {
		t.Errorf("status %s, want consent_missing", conn.PersonalConnection.Status)
	}

	// The web client refused at the token endpoint: the Owner's.
	f.g.AddWebClient(webClientID, "rotated-at-google")
	if got := failedCode(t, f.authorizeAs(t, c, "calendar", gid, email, nil)); got != "failed" {
		t.Errorf("the web client refused: %s, want failed", got)
	}
	if conn := getConnection(t, f.o); conn.PersonalConnection.Status != gen.Unavailable || deref(conn.PersonalConnection.Reason) != "client_refused" {
		t.Errorf("status %s %v, want unavailable client_refused", conn.PersonalConnection.Status, conn.PersonalConnection.Reason)
	}

	if n := f.h.Count(t, `SELECT count(*) FROM google.connections`); n != 0 {
		t.Errorf("%d connections stored by failed callbacks, want 0", n)
	}
	f.noSecretsLeaked(t, gid)
}

// --- complete ------------------------------------------------------------------

func TestPersonalComplete_Connects(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, userID, gid := f.person(t, "kari@nordlys.no")

	if me := getMe(t, c); !me.Offered || me.State != gen.NotConnected || deref(me.Account) != "kari@nordlys.no" {
		t.Errorf("before: %+v", me)
	}
	u, _ := startPersonal(t, c, "calendar", "/calendar/settings")
	grant := googletest.GrantFor(u, gid, "kari@nordlys.no", "nordlys.no")
	grant.Granted = append(grant.Scopes, "https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile")
	r := personalCallback(t, c, url.Values{"code": {f.g.IssueCode(grant)}, "state": {u.Query().Get("state")}})
	if _, connection, outcome, _ := personalOutcome(t, r); connection != "google" || outcome != "complete" {
		t.Fatalf("callback: %s %s", connection, outcome)
	}
	if sc := r.Header("Set-Cookie"); !strings.Contains(sc, "vantigo.google.connected=") || !strings.Contains(sc, "Max-Age=300") || !strings.Contains(sc, "SameSite=Lax") {
		t.Errorf("the connected cookie: %q", sc)
	}

	r = completePersonal(c)
	if r.Status != http.StatusOK {
		t.Fatalf("complete: %d %s", r.Status, r.Body)
	}
	var done gen.GooglePersonalComplete
	r.JSON(&done)
	if done.ReturnTo != "/calendar/settings" || done.Connection.State != gen.Active || !slices.Equal(done.Connection.Features, []string{"calendar"}) {
		t.Errorf("complete answered %+v", done)
	}

	var owner uuid.UUID
	var scopes []string
	var sealed []byte
	if err := f.h.Pool().QueryRow(context.Background(),
		`SELECT user_id, scopes, refresh_sealed FROM google.connections WHERE google_user_id = $1 AND state = 'active'`, gid).Scan(&owner, &scopes, &sealed); err != nil {
		t.Fatal(err)
	}
	if owner != userID {
		t.Errorf("the row is %s's, want %s's", owner, userID)
	}
	want := slices.Clone(grant.Granted)
	slices.Sort(want)
	if !slices.Equal(scopes, want) {
		t.Errorf("scopes %v, want %v as Google answered them", scopes, want)
	}
	for _, token := range f.g.RefreshTokens(gid) {
		if strings.Contains(string(sealed), token) {
			t.Error("the refresh token is stored in the clear")
		}
	}
	if got := f.personalEvents(t, "google.personal.connected", gid); len(got) != 1 || !slices.Equal(got[0], []string{"feature:calendar"}) {
		t.Errorf("connected events %v", got)
	}

	me := getMe(t, c)
	if me.State != gen.Active || me.ConnectedAt == nil || !slices.Equal(me.Features, []string{"calendar"}) {
		t.Errorf("after: %+v", me)
	}
	conn := getConnection(t, f.o)
	if p := conn.PersonalConnection; p.Status != gen.Ready || p.Connected != 1 || p.Reconnect != 0 || p.RedirectUri != "http://modules.example.com/api/v1/google/connect/callback" {
		t.Errorf("the switch: %+v", p)
	}
	f.noSecretsLeaked(t, gid)
}

func TestPersonalComplete_AnotherAccountStoresNothing(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, _, gid := f.person(t, "kari@nordlys.no")
	other := f.account(t, "ola@nordlys.no")

	// Signed in to Google as another of the organisation's accounts: its
	// sub is not the Directory id Vantigo knows the person by (U1).
	f.authorizeAs(t, c, "calendar", gid, "kari@nordlys.no", func(g *googletest.CodeGrant) { g.Sub, g.Email = other, "ola@nordlys.no" })
	r := completePersonal(c)
	if r.Status != http.StatusConflict || codeOf(t, r) != "account_mismatch" {
		t.Errorf("another account: %d %s, want 409 account_mismatch", r.Status, r.Body)
	}
	if strings.Contains(string(r.Body), "@") {
		t.Errorf("the refusal names an address: %s", r.Body)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections`); n != 0 {
		t.Errorf("%d connections stored, want 0", n)
	}
}

func TestPersonalComplete_RefusesAnotherSessionOrUser(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, userID, gid := f.person(t, "kari@nordlys.no")
	other, _, _ := f.person(t, "ola@nordlys.no")

	if r := completePersonal(c); r.Status != http.StatusGone || codeOf(t, r) != "expired" {
		t.Errorf("no cookie: %d %s, want 410 expired", r.Status, r.Body)
	}
	r := f.authorizeAs(t, c, "calendar", gid, "kari@nordlys.no", nil)
	sealed := strings.TrimPrefix(strings.SplitN(r.Header("Set-Cookie"), ";", 2)[0], "vantigo.google.connected=")
	if sealed == "" {
		t.Fatal("no connected cookie")
	}
	sameUser := secondSession(t, f.h, userID)
	sameUser.SetCookie("vantigo.google.connected", sealed)
	if r := completePersonal(sameUser); r.Status != http.StatusGone {
		t.Errorf("another session of the same person: %d %s, want 410", r.Status, r.Body)
	}
	other.SetCookie("vantigo.google.connected", sealed)
	if r := completePersonal(other); r.Status != http.StatusGone {
		t.Errorf("another person: %d %s, want 410", r.Status, r.Body)
	}
	_, stateCookie := startPersonal(t, other, "calendar", "")
	other.SetCookie("vantigo.google.connected", strings.TrimPrefix(strings.SplitN(stateCookie, ";", 2)[0], "vantigo.google.connect="))
	if r := completePersonal(other); r.Status != http.StatusGone {
		t.Errorf("the state cookie as the connected one: %d, want 410", r.Status)
	}
	f.authorizeAs(t, c, "calendar", gid, "kari@nordlys.no", nil)
	f.h.Advance(6 * time.Minute)
	if r := completePersonal(c); r.Status != http.StatusGone {
		t.Errorf("an expired cookie: %d, want 410", r.Status)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections`); n != 0 {
		t.Errorf("%d connections stored, want 0", n)
	}
}

func TestPersonalComplete_RechecksTheSwitchAndTheAccount(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, _, gid := f.person(t, "kari@nordlys.no")

	f.authorizeAs(t, c, "calendar", gid, "kari@nordlys.no", nil)
	setPersonal(t, f.o, false)
	if r := completePersonal(c); r.Status != http.StatusConflict || codeOf(t, r) != "switch_off" {
		t.Errorf("the switch turned off: %d %s, want 409 switch_off", r.Status, r.Body)
	}
	setPersonal(t, f.o, true)
	f.authorizeAs(t, c, "calendar", gid, "kari@nordlys.no", nil)
	f.h.Exec(t, `UPDATE google.accounts SET state = 'disabled' WHERE user_id = $1`, gid)
	if r := completePersonal(c); r.Status != http.StatusConflict || codeOf(t, r) != "no_account" {
		t.Errorf("the account suspended: %d %s, want 409 no_account", r.Status, r.Body)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections`); n != 0 {
		t.Errorf("%d connections stored, want 0", n)
	}
}

func TestPersonalComplete_ClosingTakenOverAndRepointed(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, userID, gid := f.person(t, "kari@nordlys.no")
	if r := f.connectAs(t, c, gid, "kari@nordlys.no"); r.Status != http.StatusOK {
		t.Fatalf("complete: %d %s", r.Status, r.Body)
	}
	// Disconnected, then connected again: the closing row is taken over.
	if r := c.Do(http.MethodDelete, personalMePath, nil); r.Status != http.StatusNoContent {
		t.Fatalf("DELETE: %d", r.Status)
	}
	if r := f.connectAs(t, c, gid, "kari@nordlys.no"); r.Status != http.StatusOK {
		t.Fatalf("complete again: %d %s", r.Status, r.Body)
	}
	if state, _ := f.rowState(t, gid); state != "active" {
		t.Errorf("state %s, want active again", state)
	}

	// Staff re-points the person to another account: their old connection
	// closes (repointed), its token kept for the old account's removals.
	second := f.account(t, "kari.n@nordlys.no")
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "google", ExternalID: second, Enabled: true})
	if r := f.connectAs(t, c, second, "kari.n@nordlys.no"); r.Status != http.StatusOK {
		t.Fatalf("complete on the new account: %d %s", r.Status, r.Body)
	}
	if state, _ := f.rowState(t, gid); state != "closing" {
		t.Errorf("the old account's connection %s, want closing", state)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections WHERE google_user_id = $1 AND refresh_sealed IS NOT NULL`, gid); n != 1 {
		t.Error("the re-pointed connection's token was forgotten; its removals need it")
	}
	if got := f.personalEvents(t, "google.personal.closed", gid); len(got) != 1 || got[0][0] != "repointed" {
		t.Errorf("closed events %v", got)
	}
}

// --- the person's own reads, the switch --------------------------------------

func TestPersonalConnection_GetAndDelete(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, userID, gid := f.person(t, "kari@nordlys.no")

	nobody, _ := f.h.SignInUser(t)
	if me := getMe(t, nobody); me.Offered || me.Reason == nil || *me.Reason != gen.NoAccount || me.Account != nil {
		t.Errorf("no account: %+v", me)
	}
	if r := c.Do(http.MethodDelete, personalMePath, nil); r.Status != http.StatusNoContent {
		t.Errorf("DELETE with nothing connected: %d", r.Status)
	}
	f.connectAs(t, c, gid, "kari@nordlys.no")
	if r := c.Do(http.MethodDelete, personalMePath, nil); r.Status != http.StatusNoContent {
		t.Errorf("DELETE: %d", r.Status)
	}
	if me := getMe(t, c); me.State != gen.Closing {
		t.Errorf("after disconnecting: %+v, want closing", me)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections WHERE google_user_id = $1 AND state = 'closing' AND refresh_sealed IS NOT NULL`, gid); n != 1 {
		t.Error("a disconnect forgot the token its removals need")
	}
	if got := f.personalEvents(t, "google.personal.disconnected", gid); len(got) != 1 {
		t.Errorf("disconnected events %v", got)
	}
	setPersonal(t, f.o, false)
	if me := getMe(t, c); me.Offered || me.Reason == nil || *me.Reason != gen.SwitchOff {
		t.Errorf("the switch off: %+v", me)
	}
	_ = userID
}

func TestPersonalSwitch(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	c, _, gid := f.person(t, "kari@nordlys.no")
	f.connectAs(t, c, gid, "kari@nordlys.no")

	m, _ := manager(t, f.h)
	if r := m.Do(http.MethodPut, personalSwitchPath, map[string]any{"enabled": false}); r.Status != http.StatusForbidden {
		t.Errorf("google:manage turning it off: %d, want 403 (the Owner's)", r.Status)
	}

	conn := setPersonal(t, f.o, false)
	if p := conn.PersonalConnection; p.Enabled || p.Status != gen.Off || p.Connected != 0 {
		t.Errorf("off: %+v", p)
	}
	if state, _ := f.rowState(t, gid); state != "closing" {
		t.Errorf("the switch off left the connection %s, want closing", state)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections WHERE refresh_sealed IS NOT NULL`); n != 1 {
		t.Error("the switch off forgot a token its removals need")
	}
	if got := f.personalEvents(t, "google.personal.closed", gid); len(got) != 1 || got[0][0] != "switch_off" {
		t.Errorf("closed events %v", got)
	}
	// Turned off again: nothing more.
	setPersonal(t, f.o, false)
	if n := f.h.Count(t, `SELECT count(*) FROM google.events WHERE action = 'google.personal.switched'`); n != 2 {
		t.Errorf("%d switched events, want 2 (on, off)", n)
	}

	// The web client not ready: unavailable with the reason.
	setPersonal(t, f.o, true)
	f.h.Exec(t, `UPDATE google.credentials SET revoked_at = now() WHERE kind = 'oauth_client_secret'`)
	if p := getConnection(t, f.o).PersonalConnection; p.Status != gen.Unavailable || deref(p.Reason) != "no_web_client" {
		t.Errorf("no secret: %+v", p)
	}
	f.h.Exec(t, `UPDATE google.credentials SET revoked_at = NULL WHERE kind = 'oauth_client_secret'`)
	f.h.Exec(t, `UPDATE google.connection SET domains = '{}'`)
	if p := getConnection(t, f.o).PersonalConnection; p.Status != gen.Unavailable || deref(p.Reason) != "not_tested" {
		t.Errorf("no domains: %+v", p)
	}
}

// --- the token ---------------------------------------------------------------

// connected is a person connected for the calendar: the fixture's client
// and the account's Directory id.
func (f *personalFixture) connected(t *testing.T, email string) (*modtest.Client, string) {
	t.Helper()
	c, _, gid := f.person(t, email)
	if r := f.connectAs(t, c, gid, email); r.Status != http.StatusOK {
		t.Fatalf("complete: %d %s", r.Status, r.Body)
	}
	return c, gid
}

// needs is the reason of a ConnectionNeededError, "" for any other error.
func needs(err error) string {
	var cn *contracts.ConnectionNeededError
	if errors.As(err, &cn) {
		return cn.Reason
	}
	return ""
}

func TestPersonalToken_RefreshesAndCaches(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()

	before := f.g.RefreshCalls()
	tok, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false)
	if err != nil || tok == "" {
		t.Fatalf("the token: %q %v", tok, err)
	}
	again, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false)
	if err != nil || again != tok {
		t.Errorf("the second ask: %v, want the cached token", err)
	}
	if n := f.g.RefreshCalls() - before; n != 1 {
		t.Errorf("%d refreshes, want 1", n)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections WHERE google_user_id = $1 AND revision = 2 AND refreshed_at IS NOT NULL`, gid); n != 1 {
		t.Error("the refresh did not move the revision on")
	}
	// A feature never granted needs the person.
	if _, err := google.PersonalToken(ctx, f.h.Deps(), gid, "mail", false); needs(err) != "consent_missing" {
		t.Errorf("mail, never granted: %v, want consent_missing", err)
	}
	// Nobody's connection.
	if _, err := google.PersonalToken(ctx, f.h.Deps(), "100000000000000000099", "calendar", false); needs(err) != contracts.ConnectionNotConnected {
		t.Errorf("no connection: %v, want not_connected", err)
	}
	f.noSecretsLeaked(t, gid)
}

func TestPersonalToken_RefusalsMoveTheRowOrTheClient(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	ctx := context.Background()

	for _, tc := range []struct {
		refusal, want string
	}{
		{"invalid_grant", "expired"},
		{"admin_policy_enforced", "blocked"},
		{"org_internal", "blocked"},
		{"invalid_scope", "consent_missing"},
		{"something_new", "sign_in_again"},
	} {
		c, gid := f.connected(t, tc.refusal+"@nordlys.no")
		f.g.RefuseRefresh(f.g.RefreshTokens(gid)[0], tc.refusal)
		if _, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false); needs(err) != tc.want {
			t.Errorf("%s: %v, want %s", tc.refusal, err, tc.want)
		}
		if state, code := f.rowState(t, gid); state != "reconnect" || code != tc.want {
			t.Errorf("%s: row %s %s, want reconnect %s", tc.refusal, state, code, tc.want)
		}
		if me := getMe(t, c); me.State != gen.Reconnect || deref(me.ErrorCode) != tc.want {
			t.Errorf("%s: GET /me %+v", tc.refusal, me)
		}
		if got := f.personalEvents(t, "google.personal.reconnect_needed", gid); len(got) != 1 || got[0][0] != tc.want {
			t.Errorf("%s: events %v", tc.refusal, got)
		}
		// Waiting in reconnect, no refresh is tried again.
		before := f.g.RefreshCalls()
		if _, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false); needs(err) != tc.want {
			t.Errorf("%s again: %v", tc.refusal, err)
		}
		if f.g.RefreshCalls() != before {
			t.Errorf("%s: a row in reconnect was refreshed", tc.refusal)
		}
	}

	// A transient refusal leaves the row active.
	_, gid := f.connected(t, "transient@nordlys.no")
	f.g.RefuseRefresh(f.g.RefreshTokens(gid)[0], "temporarily_unavailable")
	if _, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false); err == nil || needs(err) != "" {
		t.Errorf("temporarily_unavailable: %v, want a transient failure", err)
	}
	if state, _ := f.rowState(t, gid); state != "active" {
		t.Errorf("after a transient refusal: %s, want active", state)
	}

	// The web client refused: the connection's, the row untouched.
	_, gid = f.connected(t, "client@nordlys.no")
	f.g.AddWebClient(webClientID, "rotated-at-google")
	if _, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false); err == nil || needs(err) != "" {
		t.Errorf("the client refused: %v, want a failure that is not the person's", err)
	}
	if state, _ := f.rowState(t, gid); state != "active" {
		t.Errorf("after the client was refused: %s, want active", state)
	}
	if p := getConnection(t, f.o).PersonalConnection; p.Status != gen.Unavailable || deref(p.Reason) != "client_refused" {
		t.Errorf("the switch after the client was refused: %+v", p)
	}
	f.noSecretsLeaked(t)
}

func TestPersonalToken_ScopesAsGoogleAnswers(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	ctx := context.Background()
	_, gid := f.connected(t, "kari@nordlys.no")

	// The person removed the grant's calendar scopes at Google: the refresh
	// answers a scope with no feature left.
	f.g.NarrowRefresh(f.g.RefreshTokens(gid)[0], "openid", "https://www.googleapis.com/auth/userinfo.email")
	if _, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false); needs(err) != "consent_missing" {
		t.Errorf("no feature left: %v, want consent_missing", err)
	}
	if state, code := f.rowState(t, gid); state != "reconnect" || code != "consent_missing" {
		t.Errorf("row %s %s, want reconnect consent_missing", state, code)
	}
}

func TestPersonalToken_ChecksTheAccountOnUse(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	ctx := context.Background()
	_, gid := f.connected(t, "kari@nordlys.no")

	f.h.Exec(t, `UPDATE google.accounts SET state = 'disabled' WHERE user_id = $1`, gid)
	if _, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false); needs(err) != contracts.ConnectionClosingReason {
		t.Errorf("a suspended account: %v, want closing", err)
	}
	if state, _ := f.rowState(t, gid); state != "closing" {
		t.Errorf("row %s, want closing", state)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections WHERE refresh_sealed IS NOT NULL`); n != 0 {
		t.Error("the token outlived its account")
	}
	if got := f.personalEvents(t, "google.personal.closed", gid); len(got) != 1 || got[0][0] != "disabled" {
		t.Errorf("closed events %v", got)
	}
}

func TestPersonalToken_ARemovalOnAClosingRow(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	ctx := context.Background()
	c, gid := f.connected(t, "kari@nordlys.no")
	if r := c.Do(http.MethodDelete, personalMePath, nil); r.Status != http.StatusNoContent {
		t.Fatal(r.Status)
	}
	if _, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false); needs(err) != contracts.ConnectionClosingReason {
		t.Errorf("a use of a closing row: %v, want closing", err)
	}
	if tok, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", true); err != nil || tok == "" {
		t.Errorf("a removal on a closing row: %v", err)
	}
}

// --- closes ------------------------------------------------------------------

func TestPersonal_TheLeaverClosesWithTheTokenForgotten(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")

	p := google.Module().IdentityProvisioner(f.h.Deps())
	if _, err := p.Deactivate(context.Background(), staffActor, gid, contracts.ExternalSpec{EmployeeNumber: "E-kari"}); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if state, _ := f.rowState(t, gid); state != "closing" {
		t.Errorf("row %s, want closing", state)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections WHERE refresh_sealed IS NOT NULL`); n != 0 {
		t.Error("the leaver's token was kept")
	}
	if got := f.personalEvents(t, "google.personal.closed", gid); len(got) != 1 || got[0][0] != "leaver" {
		t.Errorf("closed events %v", got)
	}
}

// --- google-sweep --------------------------------------------------------------

func TestSweep_DeletesSpentRowsAndRevokes(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t, modtest.WithPoolMaxConns(1))
	ctx := context.Background()
	c, gid := f.connected(t, "kari@nordlys.no")
	token := f.g.RefreshTokens(gid)[0]
	if r := c.Do(http.MethodDelete, personalMePath, nil); r.Status != http.StatusNoContent {
		t.Fatal(r.Status)
	}
	sweep := google.NewSweepWorker(f.h.Deps())

	// Within the day: kept for the removals.
	f.h.Advance(time.Hour)
	if err := sweep.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections`); n != 1 {
		t.Errorf("%d rows within the day, want 1", n)
	}
	f.h.Advance(25 * time.Hour)
	if err := sweep.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections`); n != 0 {
		t.Errorf("%d rows after a day unused, want 0", n)
	}
	if !slices.Contains(f.g.Revoked(), token) {
		t.Error("the deleted row's token was not revoked at Google")
	}
	if got := f.personalEvents(t, "google.personal.deleted", gid); len(got) != 1 || !slices.Equal(got[0], []string{"revoked"}) {
		t.Errorf("deleted events %v", got)
	}
	f.noSecretsLeaked(t, gid)
}

func TestSweep_ClosesReconnectRowsWhoseAccountIsGone(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t, modtest.WithPoolMaxConns(1))
	ctx := context.Background()

	waiting := func(email string) string {
		t.Helper()
		_, gid := f.connected(t, email)
		f.g.RefuseRefresh(f.g.RefreshTokens(gid)[0], "invalid_grant")
		if _, err := google.PersonalToken(ctx, f.h.Deps(), gid, "calendar", false); needs(err) != "expired" {
			t.Fatalf("%s: %v", email, err)
		}
		return gid
	}
	suspended := waiting("suspended@nordlys.no")
	deleted := waiting("deleted@nordlys.no")
	fine := waiting("fine@nordlys.no")
	f.g.SetUser(suspended, func(u *googletest.User) { u.Suspended = true })
	f.g.DeleteUser(deleted)

	sweep := google.NewSweepWorker(f.h.Deps())
	if err := sweep.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}
	for gid, want := range map[string]string{suspended: "suspended", deleted: "deleted"} {
		if state, _ := f.rowState(t, gid); state != "closing" {
			t.Errorf("%s: %s, want closing", want, state)
		}
		if got := f.personalEvents(t, "google.personal.closed", gid); len(got) != 1 || got[0][0] != want {
			t.Errorf("%s: closed events %v", want, got)
		}
	}
	if state, _ := f.rowState(t, fine); state != "reconnect" {
		t.Errorf("an enabled account's row: %s, want reconnect", state)
	}
	// Read once a day: a second cycle within it asks Google nothing.
	gets := func() int {
		n := 0
		for _, c := range f.g.Calls() {
			if c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/users/"+fine) {
				n++
			}
		}
		return n
	}
	before := gets()
	if err := sweep.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if gets() != before {
		t.Error("the account was read twice within a day")
	}
}

// --- the changes reader --------------------------------------------------------

func TestPersonal_ConnectionChanges(t *testing.T) {
	t.Parallel()
	f := newPersonalFixture(t)
	since := f.h.Now().Add(-time.Second)
	c, gid := f.connected(t, "kari@nordlys.no")
	f.h.Advance(time.Minute)
	if r := c.Do(http.MethodDelete, personalMePath, nil); r.Status != http.StatusNoContent {
		t.Fatal(r.Status)
	}
	changes, err := google.ConnectionChanges(context.Background(), f.h.Deps(), since)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Mailbox != gid || changes[0].State != contracts.ConnectionClosing {
		t.Errorf("changes %+v, want the one connection, closing", changes)
	}
}

// deref is *s, "" for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
