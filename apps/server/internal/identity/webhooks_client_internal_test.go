package identity

import (
	"bytes"
	"context"
	"errors"
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

func testWebhookClient(rt http.RoundTripper, log *slog.Logger) *webhookClient {
	now := time.Date(2026, 11, 18, 10, 15, 0, 0, time.UTC)
	return &webhookClient{http: newWebhookHTTPClient(rt), now: func() time.Time { return now }, log: log}
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
	c := testWebhookClient(srv.Client().Transport, nil)

	a := c.send(t.Context(), webhookMessage{url: srv.URL + "/hooks?token=abc", id: "msg_1", body: body, keys: [][]byte{key}})
	if !a.succeeded() || a.status != http.StatusNoContent || a.excerpt != "" {
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
	a := testWebhookClient(srv.Client().Transport, nil).send(t.Context(), webhookMessage{url: srv.URL + "/hooks", id: "msg_1", body: []byte("{}")})
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
		_, _ = w.Write([]byte("<b>down</b>\x00\x1b[31m\r\n" + strings.Repeat("é", 2000)))
	}))
	defer srv.Close()
	a := testWebhookClient(srv.Client().Transport, nil).send(t.Context(), webhookMessage{url: srv.URL, id: "msg_1", body: []byte("{}")})
	if a.errKind != webhookErrHTTPStatus || a.status != http.StatusServiceUnavailable || a.retryAfter != "120" {
		t.Errorf("attempt %+v", a)
	}
	if len(a.excerpt) > webhookMaxExcerpt || !utf8.ValidString(a.excerpt) || strings.ContainsAny(a.excerpt, "\x00\x1b\r\n") ||
		!strings.HasPrefix(a.excerpt, "<b>down</b>") {
		t.Errorf("excerpt %q (%d bytes)", a.excerpt, len(a.excerpt))
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
			rt := webhookGuardedTransport(fixedResolver{addrs: parsed}, func(context.Context, string, string) (net.Conn, error) {
				dialled.Store(true)
				return nil, errors.New("must not dial")
			})
			a := testWebhookClient(rt, nil).send(t.Context(), webhookMessage{url: "https://hooks.example.com/in", id: "msg_1", body: []byte("{}")})
			if a.errKind != webhookErrBlockedAddress || dialled.Load() {
				t.Errorf("attempt %+v, dialled %v: want blocked_address and no dial", a, dialled.Load())
			}
		})
	}
	t.Run("a name that does not resolve", func(t *testing.T) {
		t.Parallel()
		rt := webhookGuardedTransport(fixedResolver{err: &net.DNSError{Err: "no such host", Name: "hooks.example.com", IsNotFound: true}}, (&net.Dialer{}).DialContext)
		a := testWebhookClient(rt, nil).send(t.Context(), webhookMessage{url: "https://hooks.example.com/in", id: "msg_1", body: []byte("{}")})
		if a.errKind != webhookErrDNS {
			t.Errorf("attempt %+v, want dns", a)
		}
	})
}

func TestWebhookClient_TimeoutAndRefusalAreTheirWords(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	c := testWebhookClient(slow.Client().Transport, nil)
	c.http.Timeout = 100 * time.Millisecond
	if a := c.send(t.Context(), webhookMessage{url: slow.URL, id: "msg_1", body: []byte("{}")}); a.errKind != webhookErrTimeout || a.status != 0 {
		t.Errorf("slow: attempt %+v, want timeout", a)
	}

	closed := httptest.NewTLSServer(http.NotFoundHandler())
	target := closed.URL
	transport := closed.Client().Transport
	closed.Close()
	if a := testWebhookClient(transport, nil).send(t.Context(), webhookMessage{url: target, id: "msg_1", body: []byte("{}")}); a.errKind != webhookErrConnectionRefused {
		t.Errorf("closed: attempt %+v, want connection_refused", a)
	}

	// A certificate nobody vouches for.
	untrusted := httptest.NewTLSServer(http.NotFoundHandler())
	defer untrusted.Close()
	if a := testWebhookClient(&http.Transport{}, nil).send(t.Context(), webhookMessage{url: untrusted.URL, id: "msg_1", body: []byte("{}")}); a.errKind != webhookErrTLS {
		t.Errorf("untrusted: attempt %+v, want tls", a)
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
	a := testWebhookClient(srv.Client().Transport, log).send(t.Context(), webhookMessage{
		url: srv.URL + "/hooks/T0K3N-in-path?secret=Q-T0K3N", id: "msg_1", body: []byte("{}"),
	})
	if a.errKind != webhookErrHTTPStatus {
		t.Fatalf("attempt %+v", a)
	}
	line := buf.String()
	if line == "" || !strings.Contains(line, webhookURLOrigin(srv.URL)) {
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
