package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
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

const indexHTML = `<!doctype html><html><head><title>Vantigo</title>` +
	`<script type="module" src="/assets/app-1.js"></script></head><body><div id="root"></div></body></html>`

// harness is one identity installation for one test: its own migrated
// database, a real server.New stack (forwarded-header trust, security
// headers, host filter, CrossOriginProtection, base path) over
// module.Compose(identity), the fake mail sender, and a settable clock.
// Nothing mutable is shared between harnesses except the recorder, which
// is safe for concurrent use, so tests using one can run in parallel.
type harness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	cfg    *config.Config
	access *identity.Access
	mail   *mail.Fake
	srv    *httptest.Server
	base   *url.URL

	mu    sync.Mutex
	clock time.Time

	clients atomic.Uint32
}

// harnessOption adjusts the environment the harness loads its configuration
// from.
type harnessOption func(env map[string]string)

// withEnv sets one environment variable over the harness's development
// defaults.
func withEnv(k, v string) harnessOption {
	return func(env map[string]string) { env[k] = v }
}

func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	pool, databaseURL := testdb.Migrated(t)

	// The listener exists before the configuration, because APP_URL must be
	// the server's own origin for the host filter and CrossOriginProtection.
	srv := httptest.NewUnstartedServer(nil)
	origin := "http://" + srv.Listener.Addr().String()

	// httptest's peer is always 127.0.0.1, the one trusted proxy, so each
	// client's X-Forwarded-For address becomes its httpx.ClientIP and every
	// IP-keyed limit is per client.
	env := map[string]string{
		"APP_ENV":             "development",
		"DATABASE_URL":        databaseURL,
		"APP_URL":             origin,
		"APP_SECRET":          "identity-harness-app-secret-0123456789abcdef",
		"BOOTSTRAP_SECRET":    "identity-harness-bootstrap-secret",
		"TRUSTED_PROXY_HOPS":  "1",
		"TRUSTED_PROXY_CIDRS": "127.0.0.1/32",
	}
	for _, o := range opts {
		o(env)
	}
	cfg, err := config.Load(env)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	box, err := secrets.New(cfg.AppSecret)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}

	h := &harness{t: t, pool: pool, cfg: cfg, mail: &mail.Fake{}, clock: start}
	logger := slog.New(slog.DiscardHandler)
	deps := module.Deps{
		Config:  cfg,
		Pool:    pool,
		Logger:  logger,
		Clock:   h.now,
		Mail:    h.mail,
		Secrets: box,
		Limiter: ratelimit.New(pool),
	}
	h.access = identity.NewAccess(deps)
	deps.Access = h.access
	api, err := module.Compose(deps, identity.Module(h.access))
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
	h.base, err = url.Parse(srv.URL)
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

// exec runs one fixture statement.
func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(), sql, args...); err != nil {
		h.t.Fatalf("harness: %s: %v", sql, err)
	}
}

// insertUser creates a user with the given roles directly in the database
// and returns its id. The email doubles as the display name.
func (h *harness) insertUser(email string, roles ...uuid.UUID) uuid.UUID {
	h.t.Helper()
	id := uuid.New()
	h.exec(`INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
	        VALUES ($1, $2, upper($2), $2, $3, $4, $4)`, id, email, uuid.New(), h.now())
	for _, role := range roles {
		h.exec(`INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, role)
	}
	return id
}

// session starts a session for userID as a sign-in endpoint does, at the
// harness clock, and returns its token. mfa marks a sign-in that verified a
// second factor.
func (h *harness) session(userID uuid.UUID, mfa bool) string {
	h.t.Helper()
	token, err := identity.CreateSession(h.access, context.Background(), h.pool, userID, false, mfa,
		httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		h.t.Fatalf("harness: %v", err)
	}
	return token
}

// signIn returns a client whose cookie jar holds a new session for userID.
func (h *harness) signIn(userID uuid.UUID, mfa bool) *client {
	h.t.Helper()
	c := h.client()
	c.http.Jar.SetCookies(h.base, []*http.Cookie{{Name: identity.SessionCookieName, Value: h.session(userID, mfa), Path: "/"}})
	return c
}

// client is one browser: its own cookie jar and its own client address,
// every exchange validated against the contract. It never follows
// redirects, so a test sees each 302 itself.
type client struct {
	h    *harness
	http *http.Client
	ip   string
}

// client returns a new client with a unique synthetic address from
// 198.18.0.0/15, the benchmarking range no real client uses.
func (h *harness) client() *client {
	h.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatalf("harness: %v", err)
	}
	n := h.clients.Add(1)
	return &client{
		h:  h,
		ip: fmt.Sprintf("198.18.%d.%d", n>>8&0xff, n&0xff),
		http: &http.Client{
			Jar:           jar,
			Transport:     recorder.Transport(h.t, h.srv.Client().Transport),
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
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
	c.h.t.Helper()
	r := &request{header: http.Header{}}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.h.t.Fatalf("%s %s: encode body: %v", method, path, err)
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
	req, err := http.NewRequest(method, c.h.srv.URL+path, reader)
	if err != nil {
		c.h.t.Fatalf("%s %s: %v", method, path, err)
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
		c.h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		c.h.t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return &resp{t: c.h.t, status: res.StatusCode, body: b, headers: res.Header}
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
