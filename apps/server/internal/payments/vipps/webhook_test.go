package vipps_test

import (
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
	c := &vipps.Client{}

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
	event, err := (&vipps.Client{}).VerifyWebhook(payments.WebhookRequest{
		Method: fixture.Method, PathAndQuery: fixture.PathAndQuery, Host: fixture.Host, Header: header, Body: []byte(fixture.Body),
	}, fixture.Secret, registered)
	if err != nil {
		t.Fatalf("Vipps' own webhook does not verify: %v", err)
	}
	if event.Reference == "" || event.Name == "" {
		t.Errorf("event = %+v, want its reference and name", event)
	}
}
