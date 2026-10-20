package entra

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// The Graph client below the harness: the transport's guard, the token's
// cache, the throttle and the retry, the cap and Graph's refusals, each
// against the fake tenant (or a transport answering what the fake cannot)
// on a clock the test moves, with the waits recorded rather than slept.

const (
	testTenant = "6d5f3f0e-1d2c-4b3a-9f8e-7a6b5c4d3e2f"
	testClient = "0f1e2d3c-4b5a-4968-8776-655443322110"
	testSecret = "the-client-secret-value"
)

// clientFixture is a client over rt on a clock the test moves, with its own
// token cache, its waits recorded and its log kept.
type clientFixture struct {
	c     *graphClient
	box   *secrets.Box
	log   *bytes.Buffer
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func newClientFixture(t *testing.T, rt http.RoundTripper) *clientFixture {
	t.Helper()
	box, err := secrets.New([]byte("graph-internal-test-app-secret-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	f := &clientFixture{box: box, log: &bytes.Buffer{}, now: time.Date(2026, time.October, 20, 9, 0, 0, 0, time.UTC)}
	f.c = newGraphClient(module.Deps{
		HTTPTransport: rt,
		Secrets:       box,
		Clock:         f.clock,
		Logger:        slog.New(slog.NewJSONHandler(f.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	f.c.tokens = &tokenCache{}
	f.c.sleep = func(_ context.Context, d time.Duration) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.waits = append(f.waits, d)
		return nil
	}
	return f
}

func (f *clientFixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *clientFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *clientFixture) recordedWaits() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.waits...)
}

// credential is the test client's secret, sealed as the module seals it.
func (f *clientFixture) credential(t *testing.T, secret string) graphCredential {
	t.Helper()
	sealed, err := f.box.Seal(clientSecretPurpose, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return graphCredential{id: 1001, tenantID: uuid.MustParse(testTenant), clientID: uuid.MustParse(testClient), sealed: sealed}
}

// fakeTenant is the fake tenant with the test client, every permission and
// one verified domain, on f's clock.
func fakeTenant(f *clientFixture) *graphtest.FakeGraph {
	g := graphtest.New()
	g.TenantID = testTenant
	g.Secrets[testClient] = []string{testSecret}
	g.Roles = append([]string(nil), requiredPermissions...)
	g.Organisation = "Nordlys AS"
	g.Domains = []graphtest.Domain{{Name: "nordlys.no", IsVerified: true, IsDefault: true}}
	g.Now = f.clock
	return g
}

// tokenCalls counts the fake's token requests.
func tokenCalls(g *graphtest.FakeGraph) int {
	n := 0
	for _, c := range g.Calls() {
		if c.Host == graphtest.TokenHost {
			n++
		}
	}
	return n
}

// graphAnswers is a transport sending the token request to the fake and
// answering every Graph call with the next of its answers, the last one
// repeated.
type graphAnswers struct {
	token   http.RoundTripper
	mu      sync.Mutex
	answers []func(*http.Request) *http.Response
	calls   int
}

func (a *graphAnswers) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == graphtest.TokenHost {
		return a.token.RoundTrip(req)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	i := min(a.calls, len(a.answers)-1)
	a.calls++
	return a.answers[i](req), nil
}

func answerWith(status int, header http.Header, body []byte) func(*http.Request) *http.Response {
	return func(req *http.Request) *http.Response {
		if header == nil {
			header = http.Header{}
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body)), Request: req}
	}
}

func TestGraphTransport_GuardedNoProxyKeepAlivesOn(t *testing.T) {
	tr := graphTransport()
	switch {
	case tr.Proxy != nil:
		t.Error("the production transport has a proxy")
	case tr.DialContext == nil:
		t.Error("the production transport has no guarded dialler")
	case tr.DisableKeepAlives:
		t.Error("the production transport closes every connection; Graph's should be kept")
	case tr.TLSHandshakeTimeout != 10*time.Second || tr.ResponseHeaderTimeout != 15*time.Second:
		t.Errorf("timeouts = handshake %s, headers %s; want 10s, 15s", tr.TLSHandshakeTimeout, tr.ResponseHeaderTimeout)
	case tr.MaxResponseHeaderBytes != 64<<10:
		t.Errorf("MaxResponseHeaderBytes = %d, want 64 KiB", tr.MaxResponseHeaderBytes)
	}

	c := newGraphClient(module.Deps{})
	if c.http.Transport != http.RoundTripper(graphTransport()) {
		t.Error("a client without Deps.HTTPTransport does not use the guarded transport")
	}
	if c.http.Timeout != 30*time.Second {
		t.Errorf("the call budget is %s, want 30s", c.http.Timeout)
	}
	if c.http.CheckRedirect == nil || !errors.Is(c.http.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Error("the client would follow a redirect")
	}
	fake := graphtest.New()
	if newGraphClient(module.Deps{HTTPTransport: fake}).http.Transport != http.RoundTripper(fake) {
		t.Error("Deps.HTTPTransport is not the seam")
	}
}

// cannedResolver answers every host with addrs.
type cannedResolver []netip.Addr

func (r cannedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r, nil
}

func TestGraphTransport_TheGuardRefusesAPrivateAddress(t *testing.T) {
	dialled := false
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialled = true
		return nil, net.ErrClosed
	}
	for _, addr := range []string{"10.0.0.5", "127.0.0.1", "169.254.169.254"} {
		f := newClientFixture(t, guardedTransport(cannedResolver{netip.MustParseAddr(addr)}, dial))
		_, err := f.c.fetchToken(context.Background(), f.credential(t, testSecret))
		if !errors.Is(err, errTenantUnreachable) || !strings.Contains(err.Error(), netguard.ErrDisallowed.Error()) {
			t.Errorf("a host resolving to %s: %v, want the guard's refusal as tenant_unreachable", addr, err)
		}
	}
	if dialled {
		t.Error("the guard dialled a refused address")
	}
}

func TestGraphClient_ARedirectIsNotFollowed(t *testing.T) {
	f := newClientFixture(t, nil)
	fake := fakeTenant(f)
	rt := &graphAnswers{token: fake, answers: []func(*http.Request) *http.Response{
		answerWith(http.StatusFound, http.Header{"Location": {"https://evil.example/v1.0/organization"}}, nil),
	}}
	f.c.http = graphHTTPClient(rt)
	err := f.c.get(context.Background(), f.credential(t, testSecret), "/organization", nil)
	if !isStatus(err, http.StatusFound) || rt.calls != 1 {
		t.Errorf("a 302: %v after %d calls, want the 302 refused after one", err, rt.calls)
	}
}

func TestGraphToken_FetchedOnceAndReusedUntilFiveMinutesBeforeExpiry(t *testing.T) {
	f := newClientFixture(t, nil)
	fake := fakeTenant(f)
	f.c.http = graphHTTPClient(fake)
	cred := f.credential(t, testSecret)
	ctx := context.Background()

	for range 3 {
		if err := f.c.get(ctx, cred, "/organization", nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := tokenCalls(fake); n != 1 {
		t.Fatalf("three calls fetched %d tokens, want 1", n)
	}
	f.advance(54 * time.Minute)
	if err := f.c.get(ctx, cred, "/organization", nil); err != nil {
		t.Fatal(err)
	}
	if n := tokenCalls(fake); n != 1 {
		t.Errorf("a call 54 minutes into an hour's token fetched another; %d tokens", n)
	}
	f.advance(time.Minute)
	if err := f.c.get(ctx, cred, "/organization", nil); err != nil {
		t.Fatal(err)
	}
	if n := tokenCalls(fake); n != 2 {
		t.Errorf("a call five minutes before the token's expiry did not fetch a new one; %d tokens", n)
	}

	// Another credential never shares the token.
	other := cred
	other.id = 1002
	if _, fetched, err := f.c.tokenFor(ctx, other, false); err != nil || !fetched {
		t.Errorf("another credential's token: fetched %t, %v; want its own", fetched, err)
	}
}

func TestGraphToken_TheRolesClaimIsRead(t *testing.T) {
	f := newClientFixture(t, nil)
	fake := fakeTenant(f)
	fake.Roles = []string{"Organization.Read.All", "User.ReadWrite.All"}
	f.c.http = graphHTTPClient(fake)
	tok, _, err := f.c.tokenFor(context.Background(), f.credential(t, testSecret), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tok.roles, ",") != "Organization.Read.All,User.ReadWrite.All" {
		t.Errorf("roles = %v", tok.roles)
	}
	if rolesOf("not-a-jwt") != nil || rolesOf("a.!!!.c") != nil {
		t.Error("a token that does not decode has roles")
	}
}

func TestGraphToken_RefusalsAreSorted(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		edit   func(g *graphtest.FakeGraph)
		secret string
		want   error
	}{
		{"a wrong secret is invalid_client", nil, "wrong", errCredentialInvalid},
		{"an unknown client is unauthorized_client", func(g *graphtest.FakeGraph) { g.Secrets = map[string][]string{} }, testSecret, errCredentialInvalid},
		{"an unknown tenant", func(g *graphtest.FakeGraph) { g.TenantID = uuid.NewString() }, testSecret, errTenantUnreachable},
	} {
		f := newClientFixture(t, nil)
		fake := fakeTenant(f)
		if c.edit != nil {
			c.edit(fake)
		}
		f.c.http = graphHTTPClient(fake)
		_, err := f.c.token(ctx, f.credential(t, c.secret))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if strings.Contains(f.log.String(), c.secret) || strings.Contains(err.Error(), c.secret) {
			t.Errorf("%s: the secret is in the log or the error", c.name)
		}
	}
}

func TestGraphClient_A429WaitsRetryAfterOnce(t *testing.T) {
	ctx := context.Background()

	f := newClientFixture(t, nil)
	fake := fakeTenant(f)
	f.c.http = graphHTTPClient(fake)
	fake.Throttle(1)
	if err := f.c.get(ctx, f.credential(t, testSecret), "/organization", nil); err != nil {
		t.Fatalf("one 429: %v, want the retry to answer", err)
	}
	if w := f.recordedWaits(); len(w) != 1 || w[0] != time.Second {
		t.Errorf("waits = %v, want the header's one second", w)
	}

	f = newClientFixture(t, nil)
	fake = fakeTenant(f)
	f.c.http = graphHTTPClient(fake)
	fake.Throttle(2)
	if err := f.c.get(ctx, f.credential(t, testSecret), "/organization", nil); !errors.Is(err, errThrottled) {
		t.Errorf("two 429s: %v, want errThrottled", err)
	}
	if w := f.recordedWaits(); len(w) != 1 {
		t.Errorf("waits = %v, want one", w)
	}

	f = newClientFixture(t, nil)
	rt := &graphAnswers{token: fakeTenant(f), answers: []func(*http.Request) *http.Response{
		answerWith(http.StatusTooManyRequests, http.Header{"Retry-After": {"120"}}, nil),
	}}
	f.c.http = graphHTTPClient(rt)
	if err := f.c.get(ctx, f.credential(t, testSecret), "/organization", nil); !errors.Is(err, errThrottled) || rt.calls != 1 {
		t.Errorf("a Retry-After of two minutes: %v after %d calls, want errThrottled at once", err, rt.calls)
	}
	if w := f.recordedWaits(); len(w) != 0 {
		t.Errorf("waited %v for a Retry-After over a minute", w)
	}
}

func TestGraphClient_A5xxIsRetriedOnce(t *testing.T) {
	ctx := context.Background()

	f := newClientFixture(t, nil)
	fake := fakeTenant(f)
	f.c.http = graphHTTPClient(fake)
	fake.FailNext(http.StatusServiceUnavailable)
	if err := f.c.get(ctx, f.credential(t, testSecret), "/organization", nil); err != nil {
		t.Fatalf("one 503: %v, want the retry to answer", err)
	}
	if w := f.recordedWaits(); len(w) != 1 || w[0] != time.Second {
		t.Errorf("waits = %v, want one second", w)
	}

	fake.FailNext(http.StatusServiceUnavailable)
	fake.FailNext(http.StatusBadGateway)
	err := f.c.get(ctx, f.credential(t, testSecret), "/organization", nil)
	if !isStatus(err, http.StatusBadGateway) || !errors.Is(err, errTenantUnreachable) {
		t.Errorf("two 5xx: %v, want the second refused as tenant_unreachable", err)
	}

	fake.FailNext(http.StatusNotFound)
	before := len(fake.Calls()) - tokenCalls(fake)
	if err := f.c.get(ctx, f.credential(t, testSecret), "/organization", nil); !isStatus(err, http.StatusNotFound) || errors.Is(err, errTenantUnreachable) {
		t.Errorf("a 404: %v, want it refused as it is", err)
	}
	if n := len(fake.Calls()) - tokenCalls(fake) - before; n != 1 {
		t.Errorf("a 404 was sent %d times, want once", n)
	}

	// Deps.HTTPBackoff zeroes the wait.
	f.c.backoff = func(int) time.Duration { return 0 }
	fake.FailNext(http.StatusInternalServerError)
	if err := f.c.get(ctx, f.credential(t, testSecret), "/organization", nil); err != nil {
		t.Fatal(err)
	}
	if w := f.recordedWaits(); w[len(w)-1] != 0 {
		t.Errorf("waits = %v, want the backoff's zero last", w)
	}
}

func TestGraphClient_A401FetchesAFreshTokenOnce(t *testing.T) {
	f := newClientFixture(t, nil)
	fake := fakeTenant(f)
	rt := &graphAnswers{token: fake, answers: []func(*http.Request) *http.Response{
		answerWith(http.StatusUnauthorized, nil, []byte(`{"error":{"code":"InvalidAuthenticationToken","message":"expired"}}`)),
		answerWith(http.StatusOK, nil, []byte(`{"value":[]}`)),
	}}
	f.c.http = graphHTTPClient(rt)
	if err := f.c.get(context.Background(), f.credential(t, testSecret), "/organization", nil); err != nil {
		t.Fatalf("a 401 then a 200: %v", err)
	}
	if n := tokenCalls(fake); n != 2 {
		t.Errorf("%d tokens fetched, want a fresh one after the 401", n)
	}
}

func TestGraphClient_AnAnswerOverOneMiBIsRefused(t *testing.T) {
	f := newClientFixture(t, nil)
	big := append([]byte(`{"value":"`), bytes.Repeat([]byte("a"), graphMaxBody)...)
	big = append(big, '"', '}')
	rt := &graphAnswers{token: fakeTenant(f), answers: []func(*http.Request) *http.Response{answerWith(http.StatusOK, nil, big)}}
	f.c.http = graphHTTPClient(rt)
	var into map[string]any
	if err := f.c.get(context.Background(), f.credential(t, testSecret), "/organization", &into); !errors.Is(err, errResponseTooLarge) {
		t.Errorf("an answer over 1 MiB: %v, want errResponseTooLarge", err)
	}
}

func TestGraphClient_GraphsRefusalIsParsedAndLoggedAtDebug(t *testing.T) {
	f := newClientFixture(t, nil)
	fake := fakeTenant(f)
	f.c.http = graphHTTPClient(fake)
	fake.AddUser(graphtest.User{UserPrincipalName: "kari@nordlys.no", DisplayName: "Kari", MailNickname: "kari"})
	err := f.c.post(context.Background(), f.credential(t, testSecret), "/users", map[string]any{
		"accountEnabled": false, "displayName": "Kari Nordmann", "mailNickname": "kari", "userPrincipalName": "kari@nordlys.no",
		"passwordProfile": map[string]any{"password": "Pa55word!xyz", "forceChangePasswordNextSignIn": true},
	}, nil)
	g, ok := asGraphError(err)
	if !ok || g.Status != http.StatusBadRequest || g.Code != "Request_BadRequest" || g.Detail != "ObjectConflict" {
		t.Fatalf("a taken UPN: %#v, want 400 Request_BadRequest (ObjectConflict)", err)
	}
	if !isCode(err, "ObjectConflict") || !isCode(err, "Request_BadRequest") || isCode(err, "Authorization_RequestDenied") || isCode(errThrottled, "ObjectConflict") {
		t.Error("isCode does not read the code and the detail")
	}
	log := f.log.String()
	if !strings.Contains(log, `"level":"DEBUG"`) || !strings.Contains(log, testTenant) || !strings.Contains(log, "Another object with the same value") {
		t.Errorf("the refusal's words are not logged at debug with the tenant: %s", log)
	}
	if strings.Contains(err.Error(), "Another object") {
		t.Error("Graph's words are in the error, which a caller may store")
	}
	if strings.Contains(log, "Pa55word") || strings.Contains(log, testSecret) {
		t.Error("the log carries a password or the secret")
	}
}

func TestGraphURL_StaysOnGraph(t *testing.T) {
	for _, ok := range []string{"/organization", "/users?%24filter=employeeId+eq+%27E-1%27", "/users/abc/manager/$ref"} {
		if _, err := graphURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"organization", "/../beta/users", "/users/../../x", ".evil.example/x"} {
		if _, err := graphURL(bad); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, time.October, 20, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		header string
		want   time.Duration
		ok     bool
	}{
		{"", time.Second, true},
		{"3", 3 * time.Second, true},
		{"60", time.Minute, true},
		{"61", 61 * time.Second, false},
		{now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second, true},
		{"soon", time.Second, true},
	} {
		d, ok := retryAfter(http.Header{"Retry-After": {c.header}}, now)
		if d != c.want || ok != c.ok {
			t.Errorf("Retry-After %q = %s %t, want %s %t", c.header, d, ok, c.want, c.ok)
		}
	}
}

// TestGraphClient_TheSurface is the typed surface the lifecycle calls,
// against the fake: post answers the created user, patch and put answer
// nothing, get reads with $select, del soft-deletes, and a missing user is
// a 404 refusal.
func TestGraphClient_TheSurface(t *testing.T) {
	f := newClientFixture(t, nil)
	fake := fakeTenant(f)
	f.c.http = graphHTTPClient(fake)
	cred := f.credential(t, testSecret)
	ctx := context.Background()
	manager := fake.AddUser(graphtest.User{UserPrincipalName: "boss@nordlys.no", DisplayName: "Boss", MailNickname: "boss"})

	var created struct {
		ID string `json:"id"`
	}
	if err := f.c.post(ctx, cred, "/users", map[string]any{
		"accountEnabled": false, "displayName": "Kari Nordmann", "mailNickname": "kari.nordmann", "userPrincipalName": "kari.nordmann@nordlys.no",
		"passwordProfile": map[string]any{"password": "Pa55word!xyz-0123456789abcdefghij", "forceChangePasswordNextSignIn": true},
	}, &created); err != nil || created.ID == "" {
		t.Fatalf("post: %v %+v", err, created)
	}
	if err := f.c.patch(ctx, cred, "/users/"+created.ID, map[string]any{"jobTitle": "Engineer"}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if err := f.c.put(ctx, cred, "/users/"+created.ID+"/manager/$ref", map[string]any{"@odata.id": "https://graph.microsoft.com/v1.0/users/" + manager.ID}); err != nil {
		t.Fatalf("put: %v", err)
	}
	var read struct {
		JobTitle string `json:"jobTitle"`
	}
	if err := f.c.get(ctx, cred, "/users/"+created.ID+"?%24select=jobTitle", &read); err != nil || read.JobTitle != "Engineer" {
		t.Fatalf("get: %v %+v", err, read)
	}
	if u, _ := fake.User(created.ID); u.ManagerID != manager.ID {
		t.Errorf("the manager is %q, want %q", u.ManagerID, manager.ID)
	}
	if err := f.c.del(ctx, cred, "/users/"+created.ID); err != nil {
		t.Fatalf("del: %v", err)
	}
	if err := f.c.get(ctx, cred, "/users/"+created.ID, nil); !isStatus(err, http.StatusNotFound) {
		t.Errorf("a deleted user: %v, want a 404 refusal", err)
	}
}
