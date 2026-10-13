package vipps_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/payments"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
)

// The published vector (R4 §4.7): its content hash reproduces, so the hash
// step is pinned to it. Its signature does not reproduce under the
// documented string-to-sign (R4 §7 item 37), so the signature step is pinned
// to a self-generated vector and, once captured, to a real one.
func TestVippsWebhook_PublishedContentHash(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/webhook-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Body          string `json:"body"`
		ContentSHA256 string `json:"contentSha256"`
	}
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	if got := vipps.ContentHash([]byte(vector.Body)); got != vector.ContentSHA256 {
		t.Errorf("ContentHash = %q, want the published %q", got, vector.ContentSHA256)
	}
}

// SELF-GENERATED: this vector is signed by vippstest.Sign, the fake's own
// implementation of R4 §4.7's algorithm, not by Vipps. It pins that the
// adapter verifies exactly that algorithm — the content hash, the
// string-to-sign with \n, HMAC-SHA256 under the secret's UTF-8 bytes — and
// that the path, query and host signed are the registered URL's, never the
// request line's. Whether Vipps signs the same way is what the captured
// fixture (TestVippsWebhook_CapturedFixture) settles.
func TestVippsWebhook_SelfGeneratedSignature(t *testing.T) {
	t.Parallel()
	const (
		registered = "https://pay.example.no/vantigo/api/v1/invoices/vipps/webhooks?unit=1"
		secret     = "a-webhook-secret-Æøå=="
	)
	registeredURL, _ := url.Parse(registered)
	date := time.Date(2026, time.October, 13, 10, 0, 0, 0, time.UTC)
	body := vippstest.WebhookBody(vippstest.Event{
		Reference: "acme01-10001-1001", PSPReference: "9000000042", Name: "CAPTURED",
		Amount: vippstest.Amount{Currency: "NOK", Value: 12345}, Timestamp: date.Format(time.RFC3339), Success: true,
	})
	header, err := vippstest.Sign(registered, secret, date, body)
	if err != nil {
		t.Fatal(err)
	}
	request := func() payments.WebhookRequest {
		// The request line is what the proxy handed on: another path, another
		// host — both ignored for the registered URL's.
		return payments.WebhookRequest{
			Method: http.MethodPost, PathAndQuery: "/api/v1/invoices/vipps/webhooks", Host: "10.0.0.7:8080",
			Header: header.Clone(), Body: append([]byte(nil), body...),
		}
	}
	c := verifier(t, date)

	event, err := c.VerifyWebhook(request(), secret, registeredURL)
	if err != nil {
		t.Fatalf("VerifyWebhook: %v", err)
	}
	if want := (payments.WebhookEvent{Reference: "acme01-10001-1001", Name: "CAPTURED", PSPReference: "9000000042", Success: true}); event != want {
		t.Errorf("event = %+v, want %+v", event, want)
	}

	other := func(raw string) *url.URL { u, _ := url.Parse(raw); return u }
	for name, tc := range map[string]struct {
		mutate     func(*payments.WebhookRequest)
		secret     string
		registered *url.URL
	}{
		"a changed body byte":      {mutate: func(r *payments.WebhookRequest) { r.Body[10] ^= 1 }},
		"a changed date":           {mutate: func(r *payments.WebhookRequest) { r.Header.Set("x-ms-date", "Tue, 13 Oct 2026 10:00:01 GMT") }},
		"a changed content hash":   {mutate: func(r *payments.WebhookRequest) { r.Header.Set("x-ms-content-sha256", vipps.ContentHash([]byte("{}"))) }},
		"a changed signature":      {mutate: func(r *payments.WebhookRequest) { r.Header.Set("Authorization", r.Header.Get("Authorization")+"x") }},
		"no date":                  {mutate: func(r *payments.WebhookRequest) { r.Header.Del("x-ms-date") }},
		"no content hash":          {mutate: func(r *payments.WebhookRequest) { r.Header.Del("x-ms-content-sha256") }},
		"no authorization":         {mutate: func(r *payments.WebhookRequest) { r.Header.Del("Authorization") }},
		"not a POST":               {mutate: func(r *payments.WebhookRequest) { r.Method = http.MethodPut }},
		"another secret":           {secret: "another-secret"},
		"another registered host":  {registered: other("https://evil.example.no/vantigo/api/v1/invoices/vipps/webhooks?unit=1")},
		"a registered port":        {registered: other("https://pay.example.no:8443/vantigo/api/v1/invoices/vipps/webhooks?unit=1")},
		"another registered path":  {registered: other("https://pay.example.no/api/v1/invoices/vipps/webhooks?unit=1")},
		"another registered query": {registered: other("https://pay.example.no/vantigo/api/v1/invoices/vipps/webhooks?unit=2")},
		"no registered URL":        {registered: nil},
		"a date not RFC 1123":      {mutate: func(r *payments.WebhookRequest) { r.Header.Set("x-ms-date", "2026-10-13T10:00:00Z") }},
	} {
		r := request()
		if tc.mutate != nil {
			tc.mutate(&r)
		}
		s := secret
		if tc.secret != "" {
			s = tc.secret
		}
		u := registeredURL
		if tc.mutate == nil && tc.secret == "" {
			u = tc.registered
		}
		if _, err := c.VerifyWebhook(r, s, u); !errors.Is(err, payments.ErrInvalidSignature) {
			t.Errorf("%s: %v, want ErrInvalidSignature", name, err)
		}
	}
}

// The x-ms-date must be at most eight days old (Vipps retries for seven) and
// at most five minutes ahead, by the adapter's clock — each bound to the
// second, signed afresh, so only the date decides.
func TestVippsWebhook_DateWindow(t *testing.T) {
	t.Parallel()
	const (
		registered = "https://pay.example.no/api/v1/invoices/vipps/webhooks"
		secret     = "a-webhook-secret"
	)
	registeredURL, _ := url.Parse(registered)
	now := time.Date(2026, time.October, 13, 10, 0, 0, 0, time.UTC)
	c := verifier(t, now)
	body := vippstest.WebhookBody(vippstest.Event{Reference: "acme01-10001-1001", Name: "AUTHORIZED", Success: true})
	for name, tc := range map[string]struct {
		sent time.Time
		ok   bool
	}{
		"now":                         {now, true},
		"eight days old":              {now.Add(-8 * 24 * time.Hour), true},
		"eight days and a second old": {now.Add(-8*24*time.Hour - time.Second), false},
		"five minutes ahead":          {now.Add(5 * time.Minute), true},
		"five minutes and a second":   {now.Add(5*time.Minute + time.Second), false},
	} {
		header, err := vippstest.Sign(registered, secret, tc.sent, body)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.VerifyWebhook(payments.WebhookRequest{Method: http.MethodPost, Header: header, Body: body}, secret, registeredURL)
		if tc.ok && err != nil {
			t.Errorf("%s: %v, want verified", name, err)
		}
		if !tc.ok && !errors.Is(err, payments.ErrInvalidSignature) {
			t.Errorf("%s: %v, want ErrInvalidSignature", name, err)
		}
	}

	// The date in another format, signed correctly over that very text: only
	// the RFC 1123 parse refuses it.
	iso := now.Format(time.RFC3339)
	sum := sha256.Sum256(body)
	hash := base64.StdEncoding.EncodeToString(sum[:])
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("POST\n" + registeredURL.RequestURI() + "\n" + iso + ";" + registeredURL.Host + ";" + hash))
	header := http.Header{}
	header.Set("x-ms-date", iso)
	header.Set("x-ms-content-sha256", hash)
	header.Set("Authorization", "HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature="+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	if _, err := c.VerifyWebhook(payments.WebhookRequest{Method: http.MethodPost, Header: header, Body: body}, secret, registeredURL); !errors.Is(err, payments.ErrInvalidSignature) {
		t.Errorf("an ISO 8601 date, signed: %v, want ErrInvalidSignature", err)
	}
}

// verifier is an adapter whose clock stands at now; VerifyWebhook makes no
// request.
func verifier(t *testing.T, now time.Time) *vipps.Client {
	t.Helper()
	c, err := vipps.New(vipps.Options{
		BaseURL: "https://apitest.vipps.no", ClientID: "a", ClientSecret: "b", SubscriptionKey: "c", MSN: "d",
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A webhook Vipps itself sent, captured by the tagged test against Vipps'
// test environment (vipps_test_env_test.go, with VIPPS_TEST_WEBHOOK_URL):
// the signature step pinned to the real thing. Skipped until one is
// captured.
func TestVippsWebhook_CapturedFixture(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/webhook-captured.json")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("testdata/webhook-captured.json is not captured yet: run the tagged test with VIPPS_TEST_WEBHOOK_URL (see vipps_test_env_test.go)")
	}
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		RegisteredURL string            `json:"registeredUrl"`
		Secret        string            `json:"secret"`
		Method        string            `json:"method"`
		PathAndQuery  string            `json:"pathAndQuery"`
		Host          string            `json:"host"`
		Header        map[string]string `json:"header"`
		Body          string            `json:"body"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	registered, err := url.Parse(fixture.RegisteredURL)
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{}
	for k, v := range fixture.Header {
		header.Set(k, v)
	}
	sent, err := time.Parse(time.RFC1123, header.Get("x-ms-date"))
	if err != nil {
		t.Fatalf("the fixture's x-ms-date: %v", err)
	}
	event, err := verifier(t, sent).VerifyWebhook(payments.WebhookRequest{
		Method: fixture.Method, PathAndQuery: fixture.PathAndQuery, Host: fixture.Host, Header: header, Body: []byte(fixture.Body),
	}, fixture.Secret, registered)
	if err != nil {
		t.Fatalf("Vipps' own webhook does not verify: %v", err)
	}
	if event.Reference == "" || event.Name == "" {
		t.Errorf("event = %+v, want its reference and name", event)
	}
}
