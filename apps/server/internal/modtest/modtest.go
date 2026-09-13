// Package modtest is the integration harness the business modules' test
// packages mount their module through: one migrated database per harness, the
// real server.New stack (forwarded-header trust, security headers, host
// filter, CrossOriginProtection, base path) over module.Compose with identity
// beside the module under test, a settable clock, and a client whose every
// exchange is validated against the module's own contract.
//
// It is an ordinary package rather than a _test one because three module test
// packages (customers, products, energy) import it, and a _test package cannot
// be imported. identity keeps its own harness_test.go, which this is modelled
// on and does not replace: identity's harness knows identity's endpoints,
// cookies and ceremonies, none of which belong here.
//
// A module's test package wires it up once and then writes one-line harness
// calls:
//
//	var recorder = contracttest.New(loadContract())
//
//	func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
//		return modtest.New(t, append([]modtest.Option{
//			modtest.WithRecorder(recorder),
//			modtest.WithModule(customers.Module()),
//		}, opts...)...)
//	}
//
// The recorder belongs to the calling package, not to the harness: each
// module's coverage gate is over its own contract, and every harness a package
// builds must feed the one Recorder its TestMain turns into that gate.
//
// Every helper that can fail takes the calling test's t and reports to it,
// never to the test that built the harness, so parallel subtests can share one
// harness.
package modtest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/server"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	"github.com/vantigo-io/vantigo/server/internal/web"
)

// Start is where every harness clock begins. A module reads time only through
// Deps.Clock, so a window a test needs to cross is crossed with Advance, never
// by sleeping.
var Start = time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)

// harnessOrigin is the installation's APP_URL. It is a domain rather than the
// listener's 127.0.0.1 so the host filter and CrossOriginProtection see a real
// origin; httptest's client dials the listener whatever the host is.
const harnessOrigin = "http://modules.example.com"

// sessionCookieName is identity's session cookie (internal/identity/cookies.go).
// The constant is unexported there and only identity's own tests can reach the
// export_test.go alias, so it is repeated here. A drift is loud rather than
// silent: every signed-in request in every module would answer 401.
const sessionCookieName = "vantigo.session"

const indexHTML = `<!doctype html><html><head><title>Vantigo</title>` +
	`<script type="module" src="/assets/app-1.js"></script></head><body><div id="root"></div></body></html>`

// Harness is one installation for one test: its own migrated database, the
// real server stack over module.Compose(identity, the modules under test), a
// settable clock and a captured log. Nothing mutable is shared between
// harnesses except the caller's Recorder, which is safe for concurrent use, so
// tests using one can run in parallel.
type Harness struct {
	pool     *pgxpool.Pool
	deps     module.Deps
	recorder *contracttest.Recorder
	srv      *httptest.Server
	url      string // the installation's origin, APP_URL
	base     *url.URL
	log      *syncBuffer

	mu    sync.Mutex
	clock time.Time

	clients atomic.Uint32
	users   atomic.Uint32
	roles   atomic.Uint32
}

// setup is what Options adjust: the environment the harness loads its
// configuration from, the modules it composes beside identity, and the
// Recorder its clients validate through.
type setup struct {
	env        map[string]string
	modules    []module.Module
	recorder   *contracttest.Recorder
	transport  http.RoundTripper
	backoff    func(int) time.Duration
	directory  contracts.CustomerDirectory
	smtpVerify func(ctx context.Context, cfg config.MailConfig, allowInsecure bool) error
}

// Option adjusts a harness before it is built.
type Option func(*setup)

// WithModule composes m beside identity. It may be given more than once, for a
// module under test that reads another through Deps.Directory.
func WithModule(m module.Module) Option {
	return func(s *setup) { s.modules = append(s.modules, m) }
}

// WithRecorder routes every client's exchanges through rec, the calling
// package's Recorder over that package's own contract.
func WithRecorder(rec *contracttest.Recorder) Option {
	return func(s *setup) { s.recorder = rec }
}

// WithTransport sets the RoundTripper Deps.HTTPTransport carries, for a
// module whose own outbound HTTP client (customers' Brreg lookup) must never
// touch the network in a test: rt stands in for the real transport, the same
// role .NET's StubBrregHandler played over CustomersApiFactory. Unset, a
// module falls back to its own production default.
func WithTransport(rt http.RoundTripper) Option {
	return func(s *setup) { s.transport = rt }
}

// WithBackoff sets the function Deps.HTTPBackoff carries, for a module whose
// own outbound HTTP client retries with a backoff: fn stands in for that
// client's real backoff, typically a function returning 0 so a test
// exercising several retries never actually sleeps. Unset, a module falls
// back to its own production default.
func WithBackoff(fn func(attempt int) time.Duration) Option {
	return func(s *setup) { s.backoff = fn }
}

// WithDirectory sets Deps.Directory directly to d, for a module under test
// that reads another module's data through contracts.CustomerDirectory
// (energy, so far) without composing that other module beside it — depguard
// forbids the module's own test package from importing the module that
// would otherwise provide one (customers), so the test builds its own fake
// directly against the contracts interface, the same seam .NET's
// FakeCustomerDirectory fills in EnergyApiFactory. module.Compose only ever
// overwrites Deps.Directory when one of the composed modules declares
// Module.Directory (none of energy's harnesses do), so a value set here
// survives Compose unchanged.
func WithDirectory(d contracts.CustomerDirectory) Option {
	return func(s *setup) { s.directory = d }
}

// WithSMTPVerify sets the function Deps.SMTPVerify carries, for a module
// whose own SMTP connectivity check (communications' channel verification)
// must succeed in a test without a live SMTP server or the production
// destination guard's network reach: fn stands in for mail.VerifyConnection
// itself, typically a function that records its arguments and returns nil.
// Unset, a module falls back to mail.VerifyConnection, the same guarded
// path production uses.
func WithSMTPVerify(fn func(ctx context.Context, cfg config.MailConfig, allowInsecure bool) error) Option {
	return func(s *setup) { s.smtpVerify = fn }
}

// modulesEnv returns the MODULES value a harness composing mods should set,
// rather than leaving MODULES unset and falling back to config's own default
// (customers, products, energy): that default is a production choice —
// communications, for one, is deliberately not on it yet (config.go's
// defaultModules comment) — and a harness's job is to mount whatever module
// it was asked to test, not whatever happens to ship enabled today. Without
// this, a module absent from the production default would compose silently
// short of every route it declares, and every test built on this harness
// would see 404s that look like a routing bug rather than what they
// actually are: enabledModules filtering the module out.
//
// "customers" is always included alongside whatever mods names: config.go's
// modules() rejects "energy" or "communications" without it (both read
// contracts.CustomerDirectory), so a harness testing either would otherwise
// fail configuration validation before Compose ever runs. Including it
// unconditionally costs nothing for a harness that did not ask for it and
// keeps this file from having to mirror config.go's dependency rule as new
// modules grow their own. The result is sorted and comma-joined, with no
// duplicates even when "customers" is itself one of mods or a name repeats
// across more than one WithModule call.
func modulesEnv(mods []module.Module) string {
	moduleSet := map[string]bool{"customers": true}
	for _, m := range mods {
		moduleSet[m.Name] = true
	}
	names := make([]string, 0, len(moduleSet))
	for name := range moduleSet {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// New builds a harness for t. It fails t when no Recorder or no module was
// given: a harness without either would run tests that prove nothing about the
// module or its contract.
func New(t *testing.T, opts ...Option) *Harness {
	t.Helper()
	pool, databaseURL := testdb.Migrated(t)

	// httptest's peer is always 127.0.0.1, the one trusted proxy, so each
	// client's X-Forwarded-For address becomes its httpx.ClientIP and every
	// IP-keyed limit is per client.
	env := map[string]string{
		"APP_ENV":             "development",
		"DATABASE_URL":        databaseURL,
		"APP_URL":             harnessOrigin,
		"APP_SECRET":          "modtest-harness-app-secret-0123456789abcdef",
		"TRUSTED_PROXY_HOPS":  "1",
		"TRUSTED_PROXY_CIDRS": "127.0.0.1/32",
	}
	s := &setup{env: env}
	for _, o := range opts {
		o(s)
	}
	if s.recorder == nil {
		t.Fatal("modtest: no recorder; pass modtest.WithRecorder(rec) so every exchange is validated against the module's contract")
	}
	if len(s.modules) == 0 {
		t.Fatal("modtest: no module under test; pass modtest.WithModule(m)")
	}

	env["MODULES"] = modulesEnv(s.modules)

	cfg, err := config.Load(env)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}
	box, err := secrets.New(cfg.AppSecret)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}

	h := &Harness{pool: pool, recorder: s.recorder, clock: Start, log: &syncBuffer{}}
	logger := slog.New(slog.NewJSONHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// The limiter runs on the harness clock too, so a throttle window turns
	// over when a test advances the clock and never mid-test on the wall clock.
	h.deps = module.Deps{
		Config:        cfg,
		Pool:          pool,
		Logger:        logger,
		Clock:         h.Now,
		Mail:          &mail.Fake{},
		Secrets:       box,
		Limiter:       ratelimit.NewWithClock(pool, h.Now),
		HTTPTransport: s.transport,
		HTTPBackoff:   s.backoff,
		Directory:     s.directory,
		SMTPVerify:    s.smtpVerify,
	}
	access := identity.NewAccess(h.deps)
	h.deps.Access = access

	// Identity is mounted beside the module under test so its access layer is
	// the real one: a signed-in principal, its roles and its permissions are
	// resolved from identity's tables on every request, as in production.
	mods := append([]module.Module{identity.Module(access)}, s.modules...)
	api, err := module.Compose(h.deps, mods...)
	if err != nil {
		t.Fatalf("modtest: compose: %v", err)
	}

	assets := fstest.MapFS{
		"index.html":      {Data: []byte(indexHTML)},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	}
	index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}
	srv := httptest.NewUnstartedServer(server.New(server.Options{
		Config: cfg,
		Logger: logger,
		Index:  index,
		Assets: assets,
		Health: health.Handler(logger, "test"),
		API:    api,
	}))
	srv.Start()
	t.Cleanup(srv.Close)
	h.srv = srv
	h.url = harnessOrigin
	h.base, err = url.Parse(harnessOrigin)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}
	return h
}

// Now is the harness clock, the installation's Deps.Clock.
func (h *Harness) Now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clock
}

// Advance moves the harness clock forward by d.
func (h *Harness) Advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clock = h.clock.Add(d)
}

// Deps are the dependencies the installation was composed with, for a test
// that builds a module-level collaborator the way Compose does — a module's
// Directory, say.
func (h *Harness) Deps() module.Deps { return h.deps }

// Pool is the harness's own pool on the installation's database, for a fixture
// that Exec, Count and One cannot express.
func (h *Harness) Pool() *pgxpool.Pool { return h.pool }

// fixtureTimeout bounds every fixture query. Without it a pool with no free
// connection blocks in Acquire for as long as the test binary runs, which
// turns an exhausted pool into a hung package instead of a failed test.
const fixtureTimeout = 15 * time.Second

// Exec runs one fixture statement.
func (h *Harness) Exec(t testing.TB, sql string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	if _, err := h.pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("modtest: %s: %v", sql, err)
	}
}

// Count runs a query that answers one integer.
func (h *Harness) Count(t testing.TB, sql string, args ...any) int {
	t.Helper()
	return One[int](t, h, sql, args...)
}

// One runs a query that answers one value of type T, such as the id an INSERT
// ... RETURNING hands back.
func One[T any](t testing.TB, h *Harness, sql string, args ...any) T {
	t.Helper()
	var v T
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	if err := h.pool.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
		t.Fatalf("modtest: %s: %v", sql, err)
	}
	return v
}

// SignIn seeds a user who holds exactly the given permission keys and returns a
// client carrying that user's session cookie. The keys are granted through a
// role of the user's own, which is how a real installation grants them: the
// access layer resolves them from identity's tables on every request.
//
// The rows are written directly rather than through identity's endpoints. A
// module's tests are about that module, and driving sign-in over HTTP would
// cost a bootstrap, a user creation and a login round trip per test without
// proving anything identity's own tests do not already prove.
func (h *Harness) SignIn(t testing.TB, permissions ...string) *Client {
	t.Helper()
	userID := h.seedUser(t)
	if len(permissions) > 0 {
		roleID := h.seedRole(t, permissions)
		h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, userID, roleID)
	}
	c := h.Client(t)
	c.SetCookie(sessionCookieName, h.session(t, userID))
	return c
}

// SignInDisabled is SignIn for a caller whose account is disabled: it
// delegates to SignIn for the actual seeded user, role and session, then
// flips identity.users.is_disabled for the user that session belongs to —
// found by re-deriving the session's token hash from the client's own
// cookie jar (sessionCookie, the same base64url-then-SHA-256 shape session()
// itself uses), rather than duplicating seedUser/seedRole/session here.
// Session lookup itself already excludes a disabled user's row, so the
// returned client answers 401 rather than reaching any permission check.
func (h *Harness) SignInDisabled(t testing.TB, permissions ...string) *Client {
	t.Helper()
	c := h.SignIn(t, permissions...)
	raw, ok := c.sessionCookie()
	if !ok {
		t.Fatalf("modtest: SignInDisabled: SignIn did not plant a session cookie")
	}
	token, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("modtest: SignInDisabled: decode session token: %v", err)
	}
	hash := sha256.Sum256(token)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true
	           WHERE id = (SELECT user_id FROM identity.sessions WHERE token_hash = $1)`, hash[:])
	return c
}

// seedUser creates a user with no password and no roles: it signs in through
// the session this harness plants, never through a sign-in endpoint.
func (h *Harness) seedUser(t testing.TB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := fmt.Sprintf("modtest-user-%d-%s@example.test", h.users.Add(1), id)
	h.Exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
	           VALUES ($1, $2, upper($2), $2, $3, $4, $4)`, id, email, uuid.New(), h.Now())
	return id
}

// seedRole creates a custom role holding keys and returns its id. Each SignIn
// gets its own role, so one test's grants can never widen another's.
func (h *Harness) seedRole(t testing.TB, keys []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	name := fmt.Sprintf("modtest-role-%d", h.roles.Add(1))
	h.Exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	           VALUES ($1, $2, upper($2), $2, $3, $4, $4)`, id, name, uuid.New(), h.Now())
	for _, key := range keys {
		h.Exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, $2)`, id, key)
	}
	return id
}

// session starts a session for userID at the harness clock and returns its
// cookie token. The token is the base64url text of 32 random bytes and the row
// stores their SHA-256, exactly as identity mints and stores one
// (internal/identity/tokens.go).
func (h *Harness) session(t testing.TB, userID uuid.UUID) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("modtest: %v", err)
	}
	hash := sha256.Sum256(raw)
	now := h.Now()
	h.Exec(t, `INSERT INTO identity.sessions (id, user_id, token_hash, persistent, created_at, last_seen_at)
	           VALUES ($1, $2, $3, false, $4, $4)`, uuid.New(), userID, hash[:], now)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of a server's log.
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
