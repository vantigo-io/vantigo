package meta_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The webhook (Messenger design N11): Meta's handshake and its post, whose
// signature is over the bytes as received.

// whHandshake is the GET Meta makes with hub.* parameters.
func whHandshake(t *testing.T, h *harness, mode, token, challenge string) *modtest.Response {
	q := url.Values{}
	if mode != "" {
		q.Set("hub.mode", mode)
	}
	if token != "" {
		q.Set("hub.verify_token", token)
	}
	if challenge != "" {
		q.Set("hub.challenge", challenge)
	}
	return h.Client(t).Do(http.MethodGet, webhookPath+"?"+q.Encode(), nil)
}

// whVerifyToken is the stored verify token.
func whVerifyToken(t *testing.T, h *harness) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT verify_token FROM meta.settings WHERE id = 1`)
}

func TestWebhook_HandshakeAnswersTheChallenge(t *testing.T) {
	h := newHarness(t)
	token := whVerifyToken(t, h)

	r := whHandshake(t, h, "subscribe", token, "1158201444")
	if r.Status != http.StatusOK || string(r.Body) != "1158201444" || !strings.HasPrefix(r.Header("Content-Type"), "text/plain") {
		t.Fatalf("handshake: %d %q %q", r.Status, r.Body, r.Header("Content-Type"))
	}
}

func TestWebhook_HandshakeRefusesWhatIsNotTheStoredToken(t *testing.T) {
	h := newHarness(t)
	token := whVerifyToken(t, h)
	for name, tc := range map[string]struct{ mode, token string }{
		"wrong token":             {"subscribe", strings.Repeat("a", len(token))},
		"a short token":           {"subscribe", "a"},
		"a long token":            {"subscribe", token + token},
		"the token with a suffix": {"subscribe", token + "x"},
		"wrong mode":              {"unsubscribe", token},
		"no mode":                 {"", token},
		"no token":                {"subscribe", ""},
	} {
		if r := whHandshake(t, h, tc.mode, tc.token, "42"); r.Status != http.StatusForbidden || len(r.Body) != 0 {
			t.Errorf("%s: %d %q, want 403 and nothing", name, r.Status, r.Body)
		}
	}
}

func TestWebhook_StoresASignedPost(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	configure(t, o)

	body := []byte(`{"object":"page","entry":[{"id":"1","time":1,"messaging":[]}]}`)
	if r := whPost(t, h, body, appSecret); r.Status != http.StatusOK || len(r.Body) != 0 {
		t.Fatalf("post: %d %q", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM meta.webhook_events WHERE state = 'pending'`); n != 1 {
		t.Fatalf("%d events stored, want 1", n)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT payload::text FROM meta.webhook_events`); !strings.Contains(got, `"object": "page"`) {
		t.Errorf("the stored payload: %s", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM meta.settings WHERE last_webhook_at IS NOT NULL`); n != 1 {
		t.Error("the last webhook time was not noted")
	}
}

func TestWebhook_RefusesASignatureOverOtherBytes(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	configure(t, o)

	body := []byte(`{"object":"page","entry":[]}`)
	signed := signature(appSecret, body)
	// The same JSON, written differently: other bytes, so the first signature no longer holds.
	reserialised := []byte(`{ "entry": [], "object": "page" }`)

	cases := map[string]struct {
		body   []byte
		header string
	}{
		"the same JSON re-serialised": {reserialised, signed},
		"no header":                   {body, ""},
		"no prefix":                   {body, strings.TrimPrefix(signed, "sha256=")},
		"another algorithm":           {body, "sha1=" + strings.TrimPrefix(signed, "sha256=")},
		"not hex":                     {body, "sha256=zz"},
		"a truncated digest":          {body, signed[:len(signed)-2]},
		"the wrong secret":            {body, signature("not-the-secret-not-the-secret-0000", body)},
	}
	for name, tc := range cases {
		opts := []modtest.RequestOption{modtest.RawBody("application/octet-stream", tc.body)}
		if tc.header != "" {
			opts = append(opts, modtest.Header("X-Hub-Signature-256", tc.header))
		}
		if r := h.Client(t).Do(http.MethodPost, webhookPath, nil, opts...); r.Status != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, r.Status)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM meta.webhook_events`); n != 0 {
		t.Errorf("%d events stored from refused posts", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM meta.settings WHERE last_rejected_at IS NOT NULL`); n != 1 {
		t.Error("the refusal was not noted")
	}
	logs := h.Logs()
	if !strings.Contains(logs, "signature did not hold") {
		t.Error("the refusal was not logged")
	}
	if strings.Contains(logs, appSecret) || strings.Contains(logs, signed) || strings.Contains(logs, `"entry"`) {
		t.Error("the log carries the secret, a signature or the body")
	}
}

func TestWebhook_NeedsAStoredSecret(t *testing.T) {
	h := newHarness(t)
	body := []byte(`{"object":"page"}`)
	if r := whPost(t, h, body, appSecret); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("no secret stored: status %d, want 503", r.Status)
	}
}

func TestWebhook_RefusesInvalidJSONAfterTheSignature(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	configure(t, o)

	if r := whPost(t, h, []byte(`{"object":`), appSecret); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid JSON signed: status %d, want 400", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM meta.webhook_events`); n != 0 {
		t.Errorf("%d events stored", n)
	}
}

func TestWebhook_CapsTheBodyAtOneMiB(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	configure(t, o)

	const limit = 1 << 20
	padded := func(size int) []byte {
		prefix, suffix := `{"object":"page","pad":"`, `"}`
		return []byte(prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix)
	}
	exact := padded(limit)
	if len(exact) != limit {
		t.Fatalf("test body is %d bytes", len(exact))
	}
	if r := whPost(t, h, exact, appSecret); r.Status != http.StatusOK {
		t.Fatalf("exactly 1 MiB: status %d, want 200", r.Status)
	}
	if r := whPost(t, h, padded(limit+1), appSecret); r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("1 MiB and a byte: status %d, want 413", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM meta.webhook_events`); n != 1 {
		t.Errorf("%d events stored, want 1", n)
	}
}

func TestWebhook_AnswersUnavailableWhenTheDatabaseCannotBeWritten(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	configure(t, o)

	h.Exec(t, `ALTER TABLE meta.webhook_events RENAME TO webhook_events_away`)
	restored := false
	restore := func() {
		if !restored {
			restored = true
			h.Exec(t, `ALTER TABLE meta.webhook_events_away RENAME TO webhook_events`)
		}
	}
	t.Cleanup(restore)

	body := []byte(`{"object":"page","entry":[]}`)
	if r := whPost(t, h, body, appSecret); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("database down: status %d, want 503", r.Status)
	}
	restore()
	if r := whPost(t, h, body, appSecret); r.Status != http.StatusOK {
		t.Fatalf("database back: status %d, want 200", r.Status)
	}
	if !strings.Contains(h.Logs(), "could not be stored") {
		t.Error("the failure was not logged")
	}
}

// whPost is an anonymous POST /webhook of body signed with secret. The
// contract declares the body as raw bytes, so the test sends it as such.
func whPost(t *testing.T, h *harness, body []byte, secret string) *modtest.Response {
	t.Helper()
	return h.Client(t).Do(http.MethodPost, webhookPath, nil,
		modtest.RawBody("application/octet-stream", body), modtest.Header("X-Hub-Signature-256", signature(secret, body)))
}
