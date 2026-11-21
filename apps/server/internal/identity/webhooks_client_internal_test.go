package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/vantigo-io/vantigo/server/internal/telemetry"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// fixedResolver answers every name with addrs, or err.
type fixedResolver struct {
	addrs []netip.Addr
	err   error
}

func (r fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r.addrs, r.err
}

// publicAddress is what the tests' hosts resolve to: an address netguard
// lets through, so the guard runs and passes, and the dialler below then
// reaches the test server instead.
var publicAddress = []netip.Addr{netip.MustParseAddr("93.184.215.14")}

// testTarget is the host every test URL names: httptest's certificate is
// issued to example.com.
const testTarget = "https://example.com"

// serverNetwork reaches srv through the guarded transport: the name resolves
// to a public address, netguard checks it, and the dialler connects to srv
// whatever the checked address was. srv's certificate is trusted.
func serverNetwork(srv *httptest.Server) webhookNetwork {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return webhookNetwork{
		resolver: fixedResolver{addrs: publicAddress},
		dial:     dialTo(srv.Listener.Addr().String()),
		tls:      &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}
}

func dialTo(addr string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
}

func testWebhookClient(n webhookNetwork, log *slog.Logger) *webhookClient {
	now := time.Date(2026, 11, 18, 10, 15, 0, 0, time.UTC)
	return newWebhookClient(&Access{webhookNet: &n}, func() time.Time { return now }, log)
}

func TestWebhookClient_SendsASignedMessage(t *testing.T) {
	t.Parallel()
	key, err := webhookSecretKey(newWebhookSecret())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"evt_1","type":"webhook.ping","version":1,"timestamp":"2026-11-18T10:15:00Z","data":{}}`)
	var got http.Header
	var gotBody []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		gotBody, _ = readAll(r)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := testWebhookClient(serverNetwork(srv), nil)

	a := c.send(t.Context(), webhookMessage{url: testTarget + "/hooks?token=abc", id: "msg_1", body: body, keys: [][]byte{key}})
	if !a.succeeded() || a.status != http.StatusNoContent || a.excerpt != "" || a.aborted {
		t.Fatalf("attempt %+v, want a success", a)
	}
	ts, _ := strconv.ParseInt(got.Get("webhook-timestamp"), 10, 64)
	if got.Get("webhook-id") != "msg_1" || ts != c.now().Unix() || got.Get("Content-Type") != "application/json" ||
		got.Get("User-Agent") != webhookUserAgent || !bytes.Equal(gotBody, body) {
		t.Errorf("request headers %v body %s", got, gotBody)
	}
	if !verifyWebhookSignature(got.Get("webhook-signature"), "msg_1", ts, body, key) {
		t.Errorf("signature %q does not verify", got.Get("webhook-signature"))
	}
}

// TestWebhookClient_HTTPTransportNeverReplacesTheGuard holds S2: the client
// is always built on netguard's dialler, so a test transport cannot route
// around it — a name resolving to loopback is refused before any dial.
func TestWebhookClient_HTTPTransportNeverReplacesTheGuard(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	n := serverNetwork(srv)
	n.resolver = fixedResolver{addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
	if a := testWebhookClient(n, nil).send(t.Context(), webhookMessage{url: testTarget, id: "msg_1", body: []byte("{}")}); a.errKind != webhookErrBlockedAddress {
		t.Errorf("attempt %+v, want blocked_address", a)
	}
	if transport, ok := telemetry.Base(newWebhookClient(&Access{}, time.Now, nil).http.Transport).(*http.Transport); !ok || transport != webhookTransport() {
		t.Error("production's client is not the guarded transport")
	}
}

func TestWebhookClient_NeverFollowsARedirect(t *testing.T) {
	t.Parallel()
	var followed atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed.Store(true)
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	a := testWebhookClient(serverNetwork(srv), nil).send(t.Context(), webhookMessage{url: testTarget + "/hooks", id: "msg_1", body: []byte("{}")})
	if a.succeeded() || a.status != http.StatusFound || a.errKind != webhookErrRedirect {
		t.Errorf("attempt %+v, want a failed redirect", a)
	}
	if followed.Load() {
		t.Error("the redirect was followed")
	}
}

func TestWebhookClient_ExcerptIsCappedPlainText(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("<b>down</b>\x00\x1b[31m\r\n\u202egnp.exe\u200b" + strings.Repeat("é", 2000)))
	}))
	defer srv.Close()
	a := testWebhookClient(serverNetwork(srv), nil).send(t.Context(), webhookMessage{url: testTarget, id: "msg_1", body: []byte("{}")})
	if a.errKind != webhookErrHTTPStatus || a.status != http.StatusServiceUnavailable || a.retryAfter != 2*time.Minute {
		t.Errorf("attempt %+v", a)
	}
	if len(a.excerpt) > webhookMaxExcerpt || !utf8.ValidString(a.excerpt) || strings.ContainsAny(a.excerpt, "\x00\x1b\r\n\u202e\u200b") ||
		!strings.HasPrefix(a.excerpt, "<b>down</b>") {
		t.Errorf("excerpt %q (%d bytes)", a.excerpt, len(a.excerpt))
	}
}

func TestWebhookClient_RetryAfterIsABoundedDelay(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 18, 10, 15, 0, 0, time.UTC)
	for header, want := range map[string]time.Duration{
		"":                     0,
		"120":                  2 * time.Minute,
		" 30 ":                 30 * time.Second,
		"0":                    0,
		"-5":                   0,
		"7200":                 time.Hour,
		"99999999999999999999": 0,
		"soon":                 0,
		at.Add(30 * time.Minute).Format(http.TimeFormat): 30 * time.Minute,
		at.Add(-time.Hour).Format(http.TimeFormat):       0,
		at.Add(48 * time.Hour).Format(http.TimeFormat):   time.Hour,
	} {
		if got := webhookRetryAfter(header, at); got != want {
			t.Errorf("Retry-After %q = %v, want %v", header, got, want)
		}
	}
	// Only the throttling statuses carry it to the sender.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if a := testWebhookClient(serverNetwork(srv), nil).send(t.Context(), webhookMessage{url: testTarget, id: "msg_1", body: []byte("{}")}); a.retryAfter != 0 {
		t.Errorf("a 500's Retry-After was kept: %v", a.retryAfter)
	}
}

func TestWebhookClient_TheGuardRefusesEveryDisallowedAddress(t *testing.T) {
	t.Parallel()
	for name, addrs := range map[string][]string{
		"loopback":          {"127.0.0.1"},
		"metadata":          {"169.254.169.254"},
		"IPv4-mapped":       {"::ffff:10.0.0.1"},
		"private":           {"192.168.1.10"},
		"rebinding, mixed":  {"93.184.215.14", "10.0.0.5"},
		"IPv6 unique-local": {"fd00::1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var parsed []netip.Addr
			for _, a := range addrs {
				parsed = append(parsed, netip.MustParseAddr(a))
			}
			var dialled atomic.Bool
			n := webhookNetwork{resolver: fixedResolver{addrs: parsed}, dial: func(context.Context, string, string) (net.Conn, error) {
				dialled.Store(true)
				return nil, errors.New("must not dial")
			}}
			a := testWebhookClient(n, nil).send(t.Context(), webhookMessage{url: "https://hooks.example.com/in", id: "msg_1", body: []byte("{}")})
			if a.errKind != webhookErrBlockedAddress || dialled.Load() {
				t.Errorf("attempt %+v, dialled %v: want blocked_address and no dial", a, dialled.Load())
			}
		})
	}
	t.Run("a name that does not resolve", func(t *testing.T) {
		t.Parallel()
		n := webhookNetwork{resolver: fixedResolver{err: &net.DNSError{Err: "no such host", Name: "hooks.example.com", IsNotFound: true}}}
		a := testWebhookClient(n, nil).send(t.Context(), webhookMessage{url: "https://hooks.example.com/in", id: "msg_1", body: []byte("{}")})
		if a.errKind != webhookErrDNS {
			t.Errorf("attempt %+v, want dns", a)
		}
	})
}

func TestWebhookClient_TimeoutRefusalTLSAndHeadersAreTheirWords(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	slow := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	c := testWebhookClient(serverNetwork(slow), nil)
	c.http.Timeout = 100 * time.Millisecond
	if a := c.send(t.Context(), webhookMessage{url: testTarget, id: "msg_1", body: []byte("{}")}); a.errKind != webhookErrTimeout || a.status != 0 || a.aborted {
		t.Errorf("slow: attempt %+v, want timeout", a)
	}

	closed := httptest.NewTLSServer(http.NotFoundHandler())
	n := serverNetwork(closed)
	closed.Close()
	if a := testWebhookClient(n, nil).send(t.Context(), webhookMessage{url: testTarget, id: "msg_1", body: []byte("{}")}); a.errKind != webhookErrConnectionRefused {
		t.Errorf("closed: attempt %+v, want connection_refused", a)
	}

	// A certificate nobody vouches for.
	untrusted := httptest.NewTLSServer(http.NotFoundHandler())
	defer untrusted.Close()
	n = serverNetwork(untrusted)
	n.tls = nil
	if a := testWebhookClient(n, nil).send(t.Context(), webhookMessage{url: testTarget, id: "msg_1", body: []byte("{}")}); a.errKind != webhookErrTLS {
		t.Errorf("untrusted: attempt %+v, want tls", a)
	}

	// More than 64 KiB of response headers.
	huge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for i := range 80 {
			w.Header().Set("X-Filler-"+strconv.Itoa(i), strings.Repeat("x", 1024))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer huge.Close()
	if a := testWebhookClient(serverNetwork(huge), nil).send(t.Context(), webhookMessage{url: testTarget, id: "msg_1", body: []byte("{}")}); a.errKind != webhookErrTooLarge || a.succeeded() {
		t.Errorf("huge headers: attempt %+v, want too_large", a)
	}
}

// TestWebhookClient_ACallerThatGivesUpIsNoAttempt holds #6: when the
// caller's context ends — before the request or during it — the attempt is
// aborted, so the sender records nothing, and it is not called a timeout.
func TestWebhookClient_ACallerThatGivesUpIsNoAttempt(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	slow := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	c := testWebhookClient(serverNetwork(slow), nil)

	done, cancel := context.WithCancel(t.Context())
	cancel()
	if a := c.send(done, webhookMessage{url: testTarget, id: "msg_1", body: []byte("{}")}); !a.aborted || a.errKind != "" || a.succeeded() {
		t.Errorf("a cancelled context: attempt %+v, want aborted", a)
	}
	mid, stop := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer stop()
	if a := c.send(mid, webhookMessage{url: testTarget, id: "msg_1", body: []byte("{}")}); !a.aborted || a.errKind != "" {
		t.Errorf("a context ending mid-request: attempt %+v, want aborted", a)
	}
}

// TestWebhookClient_LogsNeitherPathNorQuery holds W28 to the log line: a
// failed attempt names the URL's scheme and host, never the token a path or
// query carries.
func TestWebhookClient_LogsNeitherPathNorQuery(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := testWebhookClient(serverNetwork(srv), log).send(t.Context(), webhookMessage{
		url: testTarget + "/hooks/T0K3N-in-path?secret=Q-T0K3N", id: "msg_1", body: []byte("{}"),
	})
	if a.errKind != webhookErrHTTPStatus {
		t.Fatalf("attempt %+v", a)
	}
	line := buf.String()
	if line == "" || !strings.Contains(line, testTarget) {
		t.Errorf("log %q, want the origin", line)
	}
	if strings.Contains(line, "T0K3N") || strings.Contains(line, "/hooks") {
		t.Errorf("log %q carries the URL's path or query", line)
	}
}

func readAll(r *http.Request) ([]byte, error) {
	var b bytes.Buffer
	_, err := b.ReadFrom(r.Body)
	return b.Bytes(), err
}
