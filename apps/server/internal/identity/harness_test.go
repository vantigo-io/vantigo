package identity_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/server"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	"github.com/vantigo-io/vantigo/server/internal/web"
)

// recorder validates every exchange of every identity test against
// identity.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate.
var recorder = contracttest.New(loadContract())

func loadContract() *openapi3.T {
	doc, err := openapi.Load(context.Background(), "identity")
	if err != nil {
		panic(err)
	}
	return doc
}

// start is where every harness clock begins. Identity reads time only
// through Deps.Clock, so each session bound a test crosses is crossed on
// this clock, with advance, never by sleeping.
var start = time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)

// The installation's first Owner, as bootstrapOwner creates them, and the
// secret the harness configures for it.
const (
	harnessOrigin   = "http://identity.example.com"
	ownerEmail      = "owner@example.test"
	ownerPassword   = "OwnerPassword123"
	bootstrapSecret = "identity-harness-bootstrap-secret"
)

const indexHTML = `<!doctype html><html><head><title>Vantigo</title>` +
	`<script type="module" src="/assets/app-1.js"></script></head><body><div id="root"></div></body></html>`

// harness is one identity installation for one test: its own migrated
// database, a real server.New stack (forwarded-header trust, security
// headers, host filter, CrossOriginProtection, base path) over
// module.Compose(identity), the fake mail sender, a settable clock and a
// captured log. Nothing mutable is shared between harnesses except the
// recorder, which is safe for concurrent use, so tests using one can run in
// parallel.
//
// Every helper that can fail takes the calling test's t and reports to it,
// never to the test that built the harness, so parallel subtests can share
// one harness.
type harness struct {
	pool   *pgxpool.Pool
	cfg    *config.Config
	deps   module.Deps
	access *identity.Access
	mail   *mail.Fake
	srv    *httptest.Server
	url    string // the installation's origin, APP_URL
	base   *url.URL
	log    *syncBuffer

	mu    sync.Mutex
	clock time.Time

	clients atomic.Uint32
}

// harnessSetup is what harness options adjust: the environment the harness
// loads its configuration from, the permissions of the catalogModule it
// composes beside identity, and the fake OIDC provider it routes to.
type harnessSetup struct {
	env         map[string]string
	permissions []contracts.Permission
	tracer      pgx.QueryTracer
	oidc        *fakeOIDC
}

// withQueryTracer runs every query the installation makes through tracer:
// the installation gets a pool of its own on the harness's database, built
// with tracer, while h.pool, which fixtures use, stays untraced, so only
// the installation's own queries are traced.
func withQueryTracer(tracer pgx.QueryTracer) harnessOption {
	return func(s *harnessSetup) { s.tracer = tracer }
}

// tracedPool is a pool on databaseURL whose every query goes through
// tracer, closed when t ends.
func tracedPool(t testing.TB, databaseURL string, tracer pgx.QueryTracer) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	cfg.ConnConfig.Tracer = tracer
	// The same fixed size as every other test pool, for the same reason.
	cfg.MaxConns = testdb.PoolMaxConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// harnessOption adjusts the harness's setup.
type harnessOption func(*harnessSetup)

// withEnv sets one environment variable over the harness's development
// defaults.
func withEnv(k, v string) harnessOption {
	return func(s *harnessSetup) { s.env[k] = v }
}

// withPermissions adds perms, each a customers: key, to the composed
// catalog through catalogModule, for tests that give roles keys beyond
// identity:manage.
func withPermissions(perms ...contracts.Permission) harnessOption {
	return func(s *harnessSetup) { s.permissions = append(s.permissions, perms...) }
}

// catalogModule stands in for the customers module, contributing only
// permissions. RBAC tests need catalog keys beyond identity:manage, and a
// key must belong to a module Compose knows: openapi.Load must find the
// module's contract, and ValidatePermission requires the key's prefix to be
// the module's name. Its Mount serves nothing and builds no module.Router,
// which would report every customers operation as never registered; only
// its Permissions matter, and they reach identity through Deps.Catalog.
func catalogModule(perms []contracts.Permission) module.Module {
	return module.Module{
		Name:        "customers",
		Permissions: perms,
		Mount:       func(module.Deps) (http.Handler, error) { return http.NotFoundHandler(), nil },
	}
}

func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	pool, databaseURL := testdb.Migrated(t)

	// The installation answers as harnessOrigin, a real domain: a WebAuthn
	// relying party ID is the APP_URL host and may not be an IP address, so
	// the listener's 127.0.0.1 would leave passkeys unavailable. httptest's
	// client dials the listener for any example.com host on port 80, and the
	// host filter and CrossOriginProtection see the domain.
	srv := httptest.NewUnstartedServer(nil)
	origin := harnessOrigin

	// httptest's peer is always 127.0.0.1, the one trusted proxy, so each
	// client's X-Forwarded-For address becomes its httpx.ClientIP and every
	// IP-keyed limit is per client. BOOTSTRAP_SECRET is set so bootstrap is
	// deterministic; a test that wants the development fallback clears it.
	env := map[string]string{
		"APP_ENV":             "development",
		"DATABASE_URL":        databaseURL,
		"APP_URL":             origin,
		"APP_SECRET":          "identity-harness-app-secret-0123456789abcdef",
		"BOOTSTRAP_SECRET":    bootstrapSecret,
		"TRUSTED_PROXY_HOPS":  "1",
		"TRUSTED_PROXY_CIDRS": "127.0.0.1/32",
	}
	setup := &harnessSetup{env: env}
	for _, o := range opts {
		o(setup)
	}
	cfg, err := config.Load(env)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	box, err := secrets.New(cfg.AppSecret)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}

	h := &harness{pool: pool, cfg: cfg, mail: &mail.Fake{}, clock: start, log: &syncBuffer{}}
	logger := slog.New(slog.NewJSONHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	installation := pool
	if setup.tracer != nil {
		installation = tracedPool(t, databaseURL, setup.tracer)
	}
	// The limiter runs on the harness clock too, so a throttle window turns
	// over when a test advances the clock and never mid-test on the wall
	// clock.
	h.deps = module.Deps{
		Config:  cfg,
		Pool:    installation,
		Logger:  logger,
		Clock:   h.now,
		Mail:    h.mail,
		Secrets: box,
		Limiter: ratelimit.NewWithClock(installation, h.now),
	}
	h.access = identity.NewAccess(h.deps)
	h.deps.Access = h.access
	if setup.oidc != nil {
		setup.oidc.attach(h.access, h.now) // before Compose mounts identity
	}
	mods := []module.Module{identity.Module(h.access)}
	if len(setup.permissions) > 0 {
		mods = append(mods, catalogModule(setup.permissions))
	}
	api, err := module.Compose(h.deps, mods...)
	if err != nil {
		t.Fatalf("harness: compose identity: %v", err)
	}

	assets := fstest.MapFS{
		"index.html":      {Data: []byte(indexHTML)},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	}
	index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	srv.Config.Handler = server.New(server.Options{
		Config: cfg,
		Logger: logger,
		Index:  index,
		Assets: assets,
		Health: health.Handler(logger, "test"),
		API:    api,
	})
	srv.Start()
	t.Cleanup(srv.Close)
	h.srv = srv
	h.url = origin
	h.base, err = url.Parse(origin)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	return h
}

// now is the harness clock, identity's Deps.Clock.
func (h *harness) now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clock
}

// advance moves the harness clock forward by d.
func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clock = h.clock.Add(d)
}

// createUser creates a user with role through POST /owner/users as owner
// and returns its id. The email is also the display name, the password is
// userPassword, and the email counts as confirmed, as for every account an
// Owner creates.
func (h *harness) createUser(t testing.TB, owner *client, email, role string) uuid.UUID {
	t.Helper()
	r := owner.do(http.MethodPost, "/api/v1/identity/owner/users", map[string]string{
		"displayName": email, "email": email, "role": role, "password": userPassword,
	})
	if r.status != http.StatusCreated {
		t.Fatalf("create user %s: status %d body %s", email, r.status, r.body)
	}
	var body struct {
		ID uuid.UUID `json:"id"`
	}
	r.json(&body)
	return body.ID
}

// mailTo is every mail the fake sender recorded for to, in send order.
func (h *harness) mailTo(to string) []mail.Message {
	var out []mail.Message
	for _, m := range h.mail.Messages() {
		if m.To == to {
			out = append(out, m)
		}
	}
	return out
}

// fixtureTimeout bounds every fixture query. Without it a pool with no free
// connection blocks in Acquire for as long as the test binary runs, which
// turns an exhausted pool into a hung package instead of a failed test.
const fixtureTimeout = 15 * time.Second

// exec runs one fixture statement.
func (h *harness) exec(t testing.TB, sql string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	if _, err := h.pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("harness: %s: %v", sql, err)
	}
}

// insertUser creates a user with the given roles directly in the database
// and returns its id. The email doubles as the display name. The user has
// no password: it signs in through session.
func (h *harness) insertUser(t testing.TB, email string, roles ...uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
	        VALUES ($1, $2, upper($2), $2, $3, $4, $4)`, id, email, uuid.New(), h.now())
	for _, role := range roles {
		h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, role)
	}
	return id
}

// seedUser creates a user who can sign in with password, directly in the
// database, and returns its id. It is test-only scaffolding: tests with an
// Owner prefer createUser, which goes through the endpoint; seedUser makes
// the accounts no endpoint makes, such as one whose email is unconfirmed,
// and accounts for tests without an Owner. The hash is identity's own
// Argon2id PHC string.
func (h *harness) seedUser(t testing.TB, email, password string, roles ...uuid.UUID) uuid.UUID {
	t.Helper()
	id := h.insertUser(t, email, roles...)
	hash, err := identity.HashPassword(password)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	h.exec(t, `UPDATE identity.users SET password_hash = $2 WHERE id = $1`, id, hash)
	return id
}

// session starts a session for userID as a sign-in endpoint does, at the
// harness clock, and returns its token. mfa marks a sign-in that verified a
// second factor, which no endpoint offers yet.
func (h *harness) session(t testing.TB, userID uuid.UUID, mfa bool) string {
	t.Helper()
	token, err := identity.CreateSession(h.access, context.Background(), h.pool, userID, false, mfa,
		httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	return token
}

// signIn returns a client whose cookie jar holds a new session for userID.
func (h *harness) signIn(t testing.TB, userID uuid.UUID, mfa bool) *client {
	t.Helper()
	c := h.client(t)
	c.http.Jar.SetCookies(h.base, []*http.Cookie{{Name: identity.SessionCookieName, Value: h.session(t, userID, mfa), Path: "/"}})
	return c
}

// bootstrapOwner creates the installation's first Owner through
// POST /bootstrap and returns the client it signed in and the Owner's id.
// The Owner also holds SystemAdmin when the harness sets SYSTEM_ADMIN_EMAIL
// to ownerEmail.
func (h *harness) bootstrapOwner(t testing.TB) (*client, uuid.UUID) {
	t.Helper()
	c := h.client(t)
	r := c.do(http.MethodPost, "/api/v1/identity/bootstrap", map[string]string{
		"secret":      bootstrapSecret,
		"email":       ownerEmail,
		"displayName": "Integration Owner",
		"password":    ownerPassword,
	})
	if r.status != http.StatusCreated {
		t.Fatalf("bootstrap: status %d body %s", r.status, r.body)
	}
	var body struct {
		User struct {
			ID uuid.UUID `json:"id"`
		} `json:"user"`
	}
	r.json(&body)
	return c, body.User.ID
}

// login returns a new client signed in through POST /login.
func (h *harness) login(t testing.TB, email, password string) *client {
	t.Helper()
	c := h.client(t)
	if r := c.do(http.MethodPost, "/api/v1/identity/login", map[string]string{"email": email, "password": password}); r.status != http.StatusOK {
		t.Fatalf("login %s: status %d body %s", email, r.status, r.body)
	}
	return c
}

// totpStep is TOTP's time step. A test that has spent the code of the
// current step advances the clock by it before computing a fresh one.
const totpStep = 30 * time.Second

// totp is the code an authenticator app shows for the base32 secret at at:
// RFC 6238 with HMAC-SHA1, six digits and 30-second steps. It is computed
// here, independently of the library identity uses, as .NET's tests
// computed their own (TS/Integration/IdentityApiFactory.cs:234-250).
func totp(secret string, at time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		panic(fmt.Sprintf("totp: secret %q: %v", secret, err))
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000)
}

// enrollTOTP enrols c's user in TOTP through POST /account/mfa/setup and
// /account/mfa/enable, with password as the current password, and returns
// the secret and the recovery codes. c's session then counts as
// MFA-verified. Enable spends the current step's code, so the clock then
// moves on one step: the next code a test computes is a fresh one.
func (h *harness) enrollTOTP(t testing.TB, c *client, password string) (secret string, recoveryCodes []string) {
	t.Helper()
	r := c.do(http.MethodPost, "/api/v1/identity/account/mfa/setup", map[string]any{"password": password})
	if r.status != http.StatusOK {
		t.Fatalf("mfa setup: status %d body %s", r.status, r.body)
	}
	var setup struct {
		SharedKey string `json:"sharedKey"`
	}
	r.json(&setup)
	r = c.do(http.MethodPost, "/api/v1/identity/account/mfa/enable", map[string]any{"code": totp(setup.SharedKey, h.now()), "password": password})
	if r.status != http.StatusOK {
		t.Fatalf("mfa enable: status %d body %s", r.status, r.body)
	}
	var enabled struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	r.json(&enabled)
	h.advance(totpStep)
	return setup.SharedKey, enabled.RecoveryCodes
}

// startTwoFactor signs email in with password on a new client, which must
// be asked for a second factor, and returns the client, which holds the
// login ticket.
func (h *harness) startTwoFactor(t testing.TB, email, password string) *client {
	t.Helper()
	c := h.client(t)
	r := c.do(http.MethodPost, "/api/v1/identity/login", map[string]string{"email": email, "password": password})
	var body struct {
		RequiresTwoFactor bool `json:"requiresTwoFactor"`
	}
	r.json(&body)
	if r.status != http.StatusOK || !body.RequiresTwoFactor || c.cookie(identity.LoginTicketCookieName) == "" {
		t.Fatalf("login %s: status %d body %s, want requiresTwoFactor with a ticket", email, r.status, r.body)
	}
	return c
}

// runStartup runs identity.RunStartup against this installation with
// SYSTEM_ADMIN_EMAIL set to systemAdminEmail.
func (h *harness) runStartup(systemAdminEmail string) error {
	cfg := *h.cfg
	cfg.SystemAdminEmail = systemAdminEmail
	d := h.deps
	d.Config = &cfg
	return identity.RunStartup(context.Background(), d)
}

// sessionID is the id of the session whose cookie token is token.
func (h *harness) sessionID(t testing.TB, token string) uuid.UUID {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("harness: session token %q: %v", token, err)
	}
	hash := sha256.Sum256(raw)
	var id uuid.UUID
	if err := h.pool.QueryRow(context.Background(), `SELECT id FROM identity.sessions WHERE token_hash = $1`, hash[:]).Scan(&id); err != nil {
		t.Fatalf("harness: session for token: %v", err)
	}
	return id
}

// count runs a query that answers one integer.
func (h *harness) count(t testing.TB, sql string, args ...any) int {
	t.Helper()
	var n int
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	if err := h.pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("harness: %s: %v", sql, err)
	}
	return n
}

// auditRow is one identity.authorization_audit_events row.
type auditRow struct {
	Actor, TargetUser, TargetRole *uuid.UUID
	Details, Before, After        string
	MFA                           bool
	At                            time.Time
}

// auditEvents is every audit row with action, oldest first.
func (h *harness) auditEvents(t testing.TB, action string) []auditRow {
	t.Helper()
	rows, err := h.pool.Query(context.Background(), `
		SELECT actor_user_id, target_user_id, target_role_id, details, coalesce(before_json, ''), coalesce(after_json, ''), mfa_authenticated, occurred_at
		FROM identity.authorization_audit_events WHERE action = $1 ORDER BY id`, action)
	if err != nil {
		t.Fatalf("harness: audit events: %v", err)
	}
	defer rows.Close()
	var events []auditRow
	for rows.Next() {
		var e auditRow
		if err := rows.Scan(&e.Actor, &e.TargetUser, &e.TargetRole, &e.Details, &e.Before, &e.After, &e.MFA, &e.At); err != nil {
			t.Fatalf("harness: audit events: %v", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("harness: audit events: %v", err)
	}
	return events
}

// logRecords is every record the installation logged so far, each decoded
// from its JSON line.
func (h *harness) logRecords(t testing.TB) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range bytes.Split(h.log.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("harness: log line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of a server's
// log.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// client is one browser: its own cookie jar and its own client address,
// every exchange validated against the contract. It never follows
// redirects, so a test sees each 302 itself. It reports every failure,
// contract violations included, to the t it was made for.
type client struct {
	t    testing.TB
	h    *harness
	http *http.Client
	ip   string
}

// client returns a new client for t with a unique synthetic address from
// 198.18.0.0/15, the benchmarking range no real client uses.
func (h *harness) client(t testing.TB) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	n := h.clients.Add(1)
	return &client{
		t:  t,
		h:  h,
		ip: fmt.Sprintf("198.18.%d.%d", n>>8&0xff, n&0xff),
		http: &http.Client{
			Jar:           jar,
			Transport:     recorder.Transport(t, h.srv.Client().Transport),
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// cookie is the value of the client's cookie name, or "" without one.
func (c *client) cookie(name string) string {
	for _, ck := range c.http.Jar.Cookies(c.h.base) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// reqOpt adjusts one request.
type reqOpt func(*request)

type request struct {
	header      http.Header
	contentType string
	body        []byte
}

// header sets a request header.
func header(k, v string) reqOpt {
	return func(r *request) { r.header.Set(k, v) }
}

// skipContract opts one exchange out of contract validation. reason says
// why the exchange is deliberately off-contract; a skipped exchange never
// counts toward coverage.
func skipContract(reason string) reqOpt {
	return header(contracttest.SkipHeader, reason)
}

// rawBody sends b as the body, with content type ct, instead of JSON.
func rawBody(ct string, b []byte) reqOpt {
	return func(r *request) { r.contentType, r.body = ct, b }
}

// origin sends an Origin header, as a browser does on a cross-site request.
func origin(o string) reqOpt {
	return header("Origin", o)
}

// do sends one request to path (server-absolute, base path included) and
// returns the response, body read. body, when not nil, is sent as JSON.
func (c *client) do(method, path string, body any, opts ...reqOpt) *resp {
	c.t.Helper()
	r := &request{header: http.Header{}}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("%s %s: encode body: %v", method, path, err)
		}
		r.contentType, r.body = "application/json", b
	}
	for _, o := range opts {
		o(r)
	}

	var reader io.Reader
	if r.body != nil {
		reader = bytes.NewReader(r.body)
	}
	req, err := http.NewRequest(method, c.h.url+path, reader)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	req.Header.Set("X-Forwarded-For", c.ip)
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	for k, v := range r.header {
		req.Header[k] = v
	}

	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return &resp{t: c.t, status: res.StatusCode, body: b, headers: res.Header}
}

// resp is one response, body read.
type resp struct {
	t       testing.TB
	status  int
	body    []byte
	headers http.Header
}

// json decodes the body into v, failing the test if it does not decode.
func (r *resp) json(v any) {
	r.t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		r.t.Fatalf("decode %d response %q: %v", r.status, r.body, err)
	}
}

// code is the error code of an AuthErrorResponse ({"error":{"code"}}) or a
// CodeMessageError ({"code"}), or "" for any other body.
func (r *resp) code() string {
	var body struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		return ""
	}
	if body.Error != nil {
		return body.Error.Code
	}
	return body.Code
}

// header is a response header's first value.
func (r *resp) header(k string) string {
	return r.headers.Get(k)
}

// setCookie is the response's Set-Cookie for name, or nil without one.
func (r *resp) setCookie(name string) *http.Cookie {
	for _, c := range (&http.Response{Header: r.headers}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// authenticator is a software passkey: an ES256 key pair behind a random
// credential id, answering the installation's options as a platform
// authenticator and its browser would (WebAuthn Level 3: §5.8.1 client
// data, §6.1 authenticator data, §6.5.1 attested credential data, §8.7 the
// "none" attestation format). The client data JSON, the authenticator data
// and the CBOR are built by hand, so the installation's verifier is checked
// against an encoder independent of the library it uses.
//
// The knobs make the negative cases: noUV leaves out the user-verified
// flag, origin is the origin the browser reports, rpID (when set) is the RP
// ID hashed into the authenticator data instead of the options' own, and
// signCount is the counter, which each assertion first advances by one.
type authenticator struct {
	key        *ecdsa.PrivateKey
	id         []byte
	userHandle []byte
	signCount  uint32
	noUV       bool
	origin     string
	rpID       string
}

func newAuthenticator(t testing.TB) *authenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	return &authenticator{key: key, id: []byte(rand.Text()), origin: harnessOrigin}
}

// create answers creation options with a new credential: attestation
// "none", flags UP|UV|AT and a COSE EC2 P-256 key, as credential JSON.
func (a *authenticator) create(t testing.TB, options json.RawMessage) string {
	t.Helper()
	var o struct {
		Challenge string              `json:"challenge"`
		RP        struct{ ID string } `json:"rp"`
		User      struct{ ID string } `json:"user"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatalf("authenticator: creation options %s: %v", options, err)
	}
	a.userHandle = mustB64(t, o.User.ID)
	point, err := a.key.PublicKey.Bytes() // 0x04 ‖ x ‖ y
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	coseKey := cborMap(cborInt(1), cborInt(2), cborInt(3), cborInt(-7), // kty EC2, alg ES256
		cborInt(-1), cborInt(1), cborInt(-2), cborBytes(point[1:33]), cborInt(-3), cborBytes(point[33:])) // crv P-256, x, y
	attested := binary.BigEndian.AppendUint16(make([]byte, 16), uint16(len(a.id))) // zero AAGUID, id length
	attested = append(append(attested, a.id...), coseKey...)
	object := cborMap(cborText("fmt"), cborText("none"), cborText("attStmt"), cborMap(),
		cborText("authData"), cborBytes(a.authenticatorData(o.RP.ID, attested)))
	return a.credential(map[string]any{
		"clientDataJSON":    b64(a.clientData("webauthn.create", o.Challenge)),
		"attestationObject": b64(object),
		"transports":        []string{"internal", "hybrid"},
	})
}

// get answers request options with an assertion: the counter advanced,
// flags UP|UV, and an ES256 signature over the authenticator data and the
// client data hash, as credential JSON with the user handle.
func (a *authenticator) get(t testing.TB, options json.RawMessage) string {
	t.Helper()
	var o struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatalf("authenticator: request options %s: %v", options, err)
	}
	a.signCount++
	clientData := a.clientData("webauthn.get", o.Challenge)
	authData := a.authenticatorData(o.RPID, nil)
	clientDataHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(bytes.Clone(authData), clientDataHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	return a.credential(map[string]any{
		"clientDataJSON":    b64(clientData),
		"authenticatorData": b64(authData),
		"signature":         b64(signature),
		"userHandle":        b64(a.userHandle),
	})
}

// authenticatorData is the RP ID hash, the flags, the counter, then
// attested credential data when there is some (which sets AT).
func (a *authenticator) authenticatorData(rpID string, attested []byte) []byte {
	if a.rpID != "" {
		rpID = a.rpID
	}
	flags := byte(0x01) // UP
	if !a.noUV {
		flags |= 0x04 // UV
	}
	if attested != nil {
		flags |= 0x40 // AT
	}
	hash := sha256.Sum256([]byte(rpID))
	out := binary.BigEndian.AppendUint32(append(hash[:], flags), a.signCount)
	return append(out, attested...)
}

func (a *authenticator) clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	return b
}

// credential is the PublicKeyCredential JSON the SPA sends as
// credentialJson (apps/host/frontend/src/api/webauthn.ts).
func (a *authenticator) credential(response map[string]any) string {
	b, _ := json.Marshal(map[string]any{
		"id": b64(a.id), "rawId": b64(a.id), "type": "public-key", "response": response,
		"clientExtensionResults": map[string]any{}, "authenticatorAttachment": "platform",
	})
	return string(b)
}

// The CBOR (RFC 8949) the authenticator needs: definite-length heads,
// integers, byte and text strings, and maps of key-value pairs.
func cborHead(major byte, n int) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	default:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	}
}

func cborInt(n int) []byte {
	if n < 0 {
		return cborHead(1, -1-n)
	}
	return cborHead(0, n)
}

func cborBytes(b []byte) []byte { return append(cborHead(2, len(b)), b...) }
func cborText(s string) []byte  { return append(cborHead(3, len(s)), s...) }

func cborMap(pairs ...[]byte) []byte {
	return bytes.Join(append([][]byte{cborHead(5, len(pairs)/2)}, pairs...), nil)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func mustB64(t testing.TB, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64url %q: %v", s, err)
	}
	return b
}

const (
	accountPasskeysPath = "/api/v1/identity/account/passkeys"
	passkeyLoginPath    = "/api/v1/identity/passkeys/login"
)

// passkeyCeremony is PasskeyOptionsResponse as a test reads it.
type passkeyCeremony struct {
	CeremonyID uuid.UUID       `json:"ceremonyId"`
	Options    json.RawMessage `json:"options"`
}

// beginPasskeyEnrolment begins enrolling a passkey named name for c's user,
// which must succeed, and returns the ceremony.
func beginPasskeyEnrolment(t testing.TB, c *client, password, name string) passkeyCeremony {
	t.Helper()
	r := c.do(http.MethodPost, accountPasskeysPath+"/begin", map[string]any{"name": name, "currentPassword": password})
	if r.status != http.StatusOK {
		t.Fatalf("passkey begin: status %d body %s", r.status, r.body)
	}
	var ceremony passkeyCeremony
	r.json(&ceremony)
	return ceremony
}

// enrollPasskey enrols a new software authenticator for c's user under
// name, with password as the current password, and returns it.
func enrollPasskey(t testing.TB, c *client, password, name string) *authenticator {
	t.Helper()
	a := newAuthenticator(t)
	ceremony := beginPasskeyEnrolment(t, c, password, name)
	r := c.do(http.MethodPost, accountPasskeysPath+"/complete", map[string]any{
		"ceremonyId": ceremony.CeremonyID, "credentialJson": a.create(t, ceremony.Options), "currentPassword": password,
	})
	if r.status != http.StatusOK {
		t.Fatalf("passkey complete: status %d body %s", r.status, r.body)
	}
	return a
}

// beginPasskeyLogin begins a passkey sign-in for email on c, which must
// succeed, and returns the ceremony.
func beginPasskeyLogin(t testing.TB, c *client, email string) passkeyCeremony {
	t.Helper()
	r := c.do(http.MethodPost, passkeyLoginPath+"/begin", map[string]any{"email": email})
	if r.status != http.StatusOK {
		t.Fatalf("passkey login begin: status %d body %s", r.status, r.body)
	}
	var ceremony passkeyCeremony
	r.json(&ceremony)
	return ceremony
}

// completePasskeyLogin answers ceremony with a's assertion on c.
func completePasskeyLogin(t testing.TB, c *client, ceremony passkeyCeremony, a *authenticator) *resp {
	t.Helper()
	return c.do(http.MethodPost, passkeyLoginPath+"/complete", map[string]any{
		"ceremonyId": ceremony.CeremonyID, "credentialJson": a.get(t, ceremony.Options),
	})
}

// passkeyLogin signs email in with a on c, begin and complete, and returns
// the completion's response.
func passkeyLogin(t testing.TB, c *client, email string, a *authenticator) *resp {
	t.Helper()
	return completePasskeyLogin(t, c, beginPasskeyLogin(t, c, email), a)
}
