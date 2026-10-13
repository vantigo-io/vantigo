//go:build vipps

package vipps_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/payments"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps"
)

// The test-environment tests run the adapter against Vipps MobilePay's own
// test environment (invoices design D13, research R4 §4.8):
//
//	go test -tags vipps -run TestVippsTestEnv ./internal/payments/vipps/
//
// with the five variables of a test sales unit — VIPPS_TEST_CLIENT_ID,
// VIPPS_TEST_CLIENT_SECRET, VIPPS_TEST_SUBSCRIPTION_KEY, VIPPS_TEST_MSN and
// VIPPS_TEST_PHONE (a test user who has approved one payment in the MT app
// by hand, which the force-approve endpoint requires). They skip without
// them. VIPPS_TEST_BASE_URL overrides https://apitest.vipps.no.
//
// With VIPPS_TEST_WEBHOOK_URL — a world-reachable https URL forwarded to
// VIPPS_TEST_WEBHOOK_LISTEN (default 127.0.0.1:8089) — the webhook test
// registers it, captures one signed callback, verifies it and writes it to
// testdata/webhook-captured.json, which TestVippsWebhook_CapturedFixture then
// pins in every run; the registration is deleted after, so its secret in the
// fixture opens nothing.
//
// Each scenario uses a reference of its own: Vipps rate-limits per
// reference.

func testEnv(t *testing.T) (*vipps.Client, string) {
	t.Helper()
	return testEnvWith(t, nil)
}

// statusLog records the status Vipps answered each request with, so a test
// can report what Vipps really sends where the docs are unclear.
type statusLog struct {
	mu   sync.Mutex
	seen []string
}

func (l *statusLog) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err == nil {
		l.mu.Lock()
		l.seen = append(l.seen, fmt.Sprintf("%s %s %d", r.Method, r.URL.Path, resp.StatusCode))
		l.mu.Unlock()
	}
	return resp, err
}

// of is the statuses of the requests of method whose path ends in suffix.
func (l *statusLog) of(method, suffix string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, s := range l.seen {
		if strings.HasPrefix(s, method+" ") && strings.Contains(s, suffix+" ") {
			out = append(out, s[strings.LastIndex(s, " ")+1:])
		}
	}
	return out
}

func testEnvWith(t *testing.T, statuses *statusLog) (*vipps.Client, string) {
	t.Helper()
	vars := map[string]string{}
	for _, name := range []string{"VIPPS_TEST_CLIENT_ID", "VIPPS_TEST_CLIENT_SECRET", "VIPPS_TEST_SUBSCRIPTION_KEY", "VIPPS_TEST_MSN", "VIPPS_TEST_PHONE"} {
		vars[name] = os.Getenv(name)
		if vars[name] == "" {
			t.Skipf("%s is not set: the Vipps test-environment tests need a test sales unit", name)
		}
	}
	base := os.Getenv("VIPPS_TEST_BASE_URL")
	if base == "" {
		base = "https://apitest.vipps.no"
	}
	c, err := vipps.New(vipps.Options{
		BaseURL: base, ClientID: vars["VIPPS_TEST_CLIENT_ID"], ClientSecret: vars["VIPPS_TEST_CLIENT_SECRET"],
		SubscriptionKey: vars["VIPPS_TEST_SUBSCRIPTION_KEY"], MSN: vars["VIPPS_TEST_MSN"], SystemVersion: "test",
		Transport: transportOf(statuses),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, vars["VIPPS_TEST_PHONE"]
}

// transportOf is statuses as a transport, nil (the default) when none.
func transportOf(statuses *statusLog) http.RoundTripper {
	if statuses == nil {
		return nil
	}
	return statuses
}

// reference is a fresh reference for one scenario.
func reference(scenario string) string {
	return fmt.Sprintf("vantigo-test-%s-%d", scenario, time.Now().UnixNano())
}

func create(t *testing.T, c *vipps.Client, ref string, minor int64) {
	t.Helper()
	_, err := c.Create(context.Background(), payments.CreateRequest{
		Reference: ref, Description: "Vantigo test", IdempotencyKey: "create-" + ref[len(ref)-19:],
		Amount: payments.NOK(minor), Flow: payments.FlowRedirect, ReturnURL: "https://example.org/pay",
		TermsURL: "https://example.org/terms", PrivacyURL: "https://example.org/privacy",
	})
	if err != nil {
		t.Fatalf("Create(%s): %v", ref, err)
	}
}

// authorize force-approves ref and polls until Vipps says AUTHORIZED.
func authorize(t *testing.T, c *vipps.Client, phone, ref string) payments.Payment {
	t.Helper()
	ctx := context.Background()
	if err := c.ForceApprove(ctx, ref, phone); err != nil {
		t.Fatalf("ForceApprove(%s): %v", ref, err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		p, err := c.Get(ctx, ref)
		if err != nil {
			t.Fatalf("Get(%s): %v", ref, err)
		}
		if p.State == payments.StateAuthorized {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is still %s after a minute", ref, p.State)
		}
		time.Sleep(2 * time.Second)
	}
}

// D13's sequence: token, create, force approve, poll to AUTHORIZED, capture
// with a key and capturedAmount asserted, the capture repeated under the same
// key, a partial refund, the event log.
func TestVippsTestEnv_CaptureRefundAndTheEventLog(t *testing.T) {
	c, phone := testEnv(t)
	ctx := context.Background()
	if err := c.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	ref := reference("capture")
	create(t, c, ref, 1000)
	p := authorize(t, c, phone, ref)
	if p.Authorized != payments.NOK(1000) {
		t.Fatalf("authorized %v, want 10.00 NOK", p.Authorized)
	}
	captured, err := c.Capture(ctx, ref, payments.NOK(1000), "capture-1")
	if err != nil || captured.Captured != payments.NOK(1000) {
		t.Fatalf("Capture = %+v, %v; want 10.00 NOK captured", captured, err)
	}
	again, err := c.Capture(ctx, ref, payments.NOK(1000), "capture-1")
	if err != nil || again.Captured != payments.NOK(1000) {
		t.Errorf("the repeat under the same key = %+v, %v; want still 10.00 NOK", again, err)
	}
	refunded, err := c.Refund(ctx, ref, payments.NOK(300), "refund-1")
	if err != nil || refunded.Refunded != payments.NOK(300) {
		t.Errorf("Refund = %+v, %v; want 3.00 NOK refunded", refunded, err)
	}
	at, ok, err := c.CaptureTime(ctx, ref)
	if err != nil || !ok || time.Since(at) > 10*time.Minute {
		t.Errorf("CaptureTime = %v, %v, %v; want the capture just made", at, ok, err)
	}
}

// A request cancelled before anyone answers it is TERMINATED.
func TestVippsTestEnv_CancelBeforeAuthorization(t *testing.T) {
	c, _ := testEnv(t)
	ref := reference("cancel")
	create(t, c, ref, 1000)
	p, err := c.Cancel(context.Background(), ref, "cancel-1")
	if err != nil || p.State != payments.StateTerminated {
		t.Errorf("Cancel = %+v, %v; want TERMINATED", p, err)
	}
}

// 151 øre is the test environment's insufficient funds: whichever step Vipps
// refuses it at, the refusal is typed, never an unknown outcome.
func TestVippsTestEnv_InsufficientFunds(t *testing.T) {
	statuses := &statusLog{}
	c, phone := testEnvWith(t, statuses)
	ctx := context.Background()
	ref := reference("funds")
	create(t, c, ref, 151)
	if err := c.ForceApprove(ctx, ref, phone); err != nil {
		t.Logf("the approval of 151 øre was refused: %v", err)
		return
	}
	time.Sleep(5 * time.Second)
	p, err := c.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.State != payments.StateAuthorized {
		t.Logf("151 øre ended %s", p.State)
		return
	}
	_, err = c.Capture(ctx, ref, payments.NOK(151), "capture-1")
	t.Logf("the capture of 151 øre: HTTP %v, %v", statuses.of(http.MethodPost, "/capture"), err)
	if !payments.IsCaptureFailure(err) {
		t.Errorf("the capture of 151 øre = %v, want a capture failure", err)
	}
}

// A QR create answers Vipps' own SVG.
func TestVippsTestEnv_QR(t *testing.T) {
	c, _ := testEnv(t)
	ref := reference("qr")
	created, err := c.Create(context.Background(), payments.CreateRequest{
		Reference: ref, Description: "Vantigo test", IdempotencyKey: "qr-" + ref[len(ref)-19:],
		Amount: payments.NOK(1000), Flow: payments.FlowQR, CustomerPresent: true,
	})
	if err != nil || !strings.Contains(created.QRSVG, "<svg") {
		t.Fatalf("Create = %d bytes of SVG, %v", len(created.QRSVG), err)
	}
	if _, err := c.Cancel(context.Background(), ref, "cancel-1"); err != nil {
		t.Errorf("Cancel: %v", err)
	}
}

// With VIPPS_TEST_WEBHOOK_URL: register it, capture a payment, receive
// Vipps' signed CAPTURED callback, verify it and keep it as the fixture.
func TestVippsTestEnv_CaptureAWebhook(t *testing.T) {
	c, phone := testEnv(t)
	target := os.Getenv("VIPPS_TEST_WEBHOOK_URL")
	if target == "" {
		t.Skip("VIPPS_TEST_WEBHOOK_URL is not set")
	}
	registered, err := url.Parse(target)
	if err != nil || registered.Scheme != "https" {
		t.Fatalf("VIPPS_TEST_WEBHOOK_URL = %q: want an https URL", target)
	}
	listen := os.Getenv("VIPPS_TEST_WEBHOOK_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8089"
	}
	type received struct {
		request payments.WebhookRequest
		header  http.Header
	}
	callbacks := make(chan received, 32)
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatalf("listen on %s: %v", listen, err)
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		callbacks <- received{
			request: payments.WebhookRequest{Method: r.Method, PathAndQuery: r.URL.RequestURI(), Host: r.Host, Header: r.Header.Clone(), Body: body},
			header:  r.Header.Clone(),
		}
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	ctx := context.Background()
	id, secret, err := c.RegisterWebhook(ctx, target, vipps.Events)
	if err != nil {
		t.Fatalf("RegisterWebhook: %v", err)
	}
	t.Cleanup(func() {
		if err := c.DeleteWebhook(context.Background(), id); err != nil && !errors.Is(err, payments.ErrNotFound) {
			t.Errorf("DeleteWebhook: %v", err)
		}
	})

	ref := reference("webhook")
	create(t, c, ref, 1000)
	authorize(t, c, phone, ref)
	if _, err := c.Capture(ctx, ref, payments.NOK(1000), "capture-1"); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	deadline := time.After(2 * time.Minute)
	for {
		select {
		case <-deadline:
			t.Fatal("no CAPTURED callback in two minutes")
		case got := <-callbacks:
			event, err := c.VerifyWebhook(got.request, secret, registered)
			if err != nil {
				t.Fatalf("Vipps' own callback does not verify: %v (the HMAC key's reading, R4 §7 item 37)", err)
			}
			if event.Reference != ref || event.Name != "CAPTURED" {
				continue
			}
			header := map[string]string{}
			for _, k := range []string{"x-ms-date", "x-ms-content-sha256", "Authorization", "Webhook-Id", "Content-Type"} {
				if v := got.header.Get(k); v != "" {
					header[k] = v
				}
			}
			fixture, _ := json.MarshalIndent(map[string]any{
				"source":        "Captured from Vipps' test environment by TestVippsTestEnv_CaptureAWebhook on " + time.Now().UTC().Format(time.RFC3339) + "; the registration was deleted after.",
				"registeredUrl": target, "secret": secret, "method": got.request.Method,
				"pathAndQuery": got.request.PathAndQuery, "host": got.request.Host, "header": header, "body": string(got.request.Body),
			}, "", "  ")
			if err := os.WriteFile("testdata/webhook-captured.json", append(fixture, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
}
