package vipps_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/payments"
	"github.com/vantigo-io/vantigo/server/internal/payments/qrsvg"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
)

// clock is a settable fixed clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var start = time.Date(2026, time.October, 13, 10, 0, 0, 0, time.UTC)

// adapter is the Vipps adapter pointed at vt with its credentials, on c's
// clock.
func adapter(t *testing.T, vt *vippstest.Server, c *clock) *vipps.Client {
	t.Helper()
	if c == nil {
		c = &clock{t: start}
	}
	client, err := vipps.New(vipps.Options{
		BaseURL: vt.URL(), ClientID: vippstest.ClientID, ClientSecret: vippstest.ClientSecret,
		SubscriptionKey: vippstest.SubscriptionKey, MSN: vippstest.MSN, SystemVersion: "1.2.3",
		Transport: vt.Transport(), Now: c.now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func redirectRequest(reference, key string, minor int64) payments.CreateRequest {
	return payments.CreateRequest{
		Reference: reference, Description: "Faktura 10001", IdempotencyKey: key,
		Amount: payments.NOK(minor), Flow: payments.FlowRedirect,
		ReturnURL: "https://pay.example.no/pay?token=abc", TermsURL: "https://seller.example.no/terms",
		PrivacyURL: "https://seller.example.no/privacy",
	}
}

// requestsTo is every request of method to path, in order.
func requestsTo(vt *vippstest.Server, method, path string) []vippstest.Request {
	var out []vippstest.Request
	for _, r := range vt.Requests() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("the body is not JSON: %v: %s", err, raw)
	}
	return m
}

// authorized creates a payment under reference and approves it.
func authorized(t *testing.T, vt *vippstest.Server, c *vipps.Client, reference string, minor int64) {
	t.Helper()
	if _, err := c.Create(context.Background(), redirectRequest(reference, "create-"+reference, minor)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !vt.Approve(reference) {
		t.Fatalf("Approve(%s) refused", reference)
	}
}

// Every call carries the keys, the sales unit and the four system headers,
// each at most thirty characters (R4 §4.6).
func assertHeaders(t *testing.T, r vippstest.Request) {
	t.Helper()
	want := map[string]string{
		"Authorization":               "Bearer vippstest-token-1",
		"Ocp-Apim-Subscription-Key":   vippstest.SubscriptionKey,
		"Merchant-Serial-Number":      vippstest.MSN,
		"Vipps-System-Name":           "vantigo",
		"Vipps-System-Version":        "1.2.3",
		"Vipps-System-Plugin-Name":    "vantigo-invoices",
		"Vipps-System-Plugin-Version": "1.2.3",
	}
	for k, v := range want {
		if got := r.Header.Get(k); got != v {
			t.Errorf("%s %s: %s = %q, want %q", r.Method, r.Path, k, got, v)
		}
	}
}

// Create sends ePayment's body for each flow — WALLET, the amount in øre,
// the reference, the description, the merchant's legal links, WEB_REDIRECT
// with its return URL or QR with an SVG and the customer present — under the
// caller's Idempotency-Key, and refuses before any request (the token's
// included) whatever Vipps documents it would refuse.
func TestVipps_Create(t *testing.T) {
	t.Parallel()

	t.Run("redirect", func(t *testing.T) {
		t.Parallel()
		vt := vippstest.New(t)
		created, err := adapter(t, vt, nil).Create(context.Background(), redirectRequest("acme01-10001-1001", "key-1", 12345))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.Reference != "acme01-10001-1001" || !strings.HasPrefix(created.RedirectURL, vt.URL()+"/landing") || created.QRSVG != "" {
			t.Errorf("Created = %+v, want the reference and the redirect URL Vipps answered", created)
		}
		got := requestsTo(vt, http.MethodPost, "/epayment/v1/payments")
		if len(got) != 1 {
			t.Fatalf("%d creates, want 1", len(got))
		}
		assertHeaders(t, got[0])
		if key := got[0].Header.Get("Idempotency-Key"); key != "key-1" {
			t.Errorf("Idempotency-Key = %q, want the caller's", key)
		}
		want := map[string]any{
			"amount":              map[string]any{"currency": "NOK", "value": float64(12345)},
			"paymentMethod":       map[string]any{"type": "WALLET"},
			"reference":           "acme01-10001-1001",
			"paymentDescription":  "Faktura 10001",
			"userFlow":            "WEB_REDIRECT",
			"returnUrl":           "https://pay.example.no/pay?token=abc",
			"customerInteraction": "CUSTOMER_NOT_PRESENT",
			"merchantLegalLinks": map[string]any{
				"termsOfSaleUrl": "https://seller.example.no/terms", "privacyPolicyUrl": "https://seller.example.no/privacy",
			},
		}
		if body := decode(t, got[0].Body); !reflect.DeepEqual(body, want) {
			t.Errorf("body = %s\nwant %v", got[0].Body, want)
		}
	})

	t.Run("the same key twice is one request", func(t *testing.T) {
		t.Parallel()
		vt := vippstest.New(t)
		c := adapter(t, vt, nil)
		first, err := c.Create(context.Background(), redirectRequest("acme01-10001-1002", "key-2", 12345))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		again, err := c.Create(context.Background(), redirectRequest("acme01-10001-1002", "key-2", 12345))
		if err != nil || again != first {
			t.Errorf("the repeat = %+v, %v; want the first answer %+v", again, err, first)
		}
		if events := vt.Events("acme01-10001-1002"); len(events) != 1 {
			t.Errorf("%d events, want one CREATED", len(events))
		}
	})

	// A reference already in use (4150) or a key already used (4020) may be
	// this very request, made by an earlier try whose answer was lost: the
	// payment is read, and answered as created when it is the one asked for;
	// otherwise the outcome is unknown — never a refusal for good.
	t.Run("a reference in use is read back", func(t *testing.T) {
		t.Parallel()
		vt := vippstest.New(t)
		c := adapter(t, vt, nil)
		ctx := context.Background()
		first, err := c.Create(ctx, redirectRequest("acme01-10001-1005", "key-a", 12345))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		again, err := c.Create(ctx, redirectRequest("acme01-10001-1005", "key-b", 12345))
		if err != nil || again != first {
			t.Errorf("4150, the same payment = %+v, %v; want the payment held, %+v", again, err, first)
		}
		vt.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusConflict, Code: "4020", Times: 1})
		if again, err := c.Create(ctx, redirectRequest("acme01-10001-1005", "key-c", 12345)); err != nil || again != first {
			t.Errorf("4020, the same payment = %+v, %v; want the payment held", again, err)
		}
		if n := len(vt.Events("acme01-10001-1005")); n != 1 {
			t.Errorf("%d events, want the one CREATED", n)
		}

		qr := redirectRequest("acme01-10001-1006", "key-qr", 5000)
		qr.Flow, qr.ReturnURL = payments.FlowQR, ""
		firstQR, err := c.Create(ctx, qr)
		if err != nil {
			t.Fatalf("Create QR: %v", err)
		}
		qr.IdempotencyKey = "key-qr-2"
		if again, err := c.Create(ctx, qr); err != nil || again != firstQR {
			t.Errorf("4150 on a QR = %+v, %v; want the same QR", again, err)
		}

		for name, call := range map[string]func() error{
			"another amount": func() error {
				_, err := c.Create(ctx, redirectRequest("acme01-10001-1005", "key-d", 12346))
				return err
			},
			"4020 and nothing held": func() error {
				vt.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusConflict, Code: "4020", Times: 1})
				_, err := c.Create(ctx, redirectRequest("acme01-10001-1007", "key-e", 12345))
				return err
			},
			"4010 (a key reused with another body)": func() error {
				vt.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusBadRequest, Code: "4010", Times: 1})
				_, err := c.Create(ctx, redirectRequest("acme01-10001-1008", "key-f", 12345))
				return err
			},
		} {
			err := call()
			if err == nil || payments.IsDefinitiveCreateRefusal(err) {
				t.Errorf("%s: %v, want an error that is no definitive refusal", name, err)
			}
		}
	})

	t.Run("refused before any request", func(t *testing.T) {
		t.Parallel()
		vt := vippstest.New(t)
		c := adapter(t, vt, nil)
		long := strings.Repeat("x", 101)
		for name, mutate := range map[string]func(*payments.CreateRequest){
			"99 øre":                 func(r *payments.CreateRequest) { r.Amount = payments.NOK(99) },
			"65 000 001 øre":         func(r *payments.CreateRequest) { r.Amount = payments.NOK(65_000_001) },
			"not NOK":                func(r *payments.CreateRequest) { r.Amount = payments.Money{Currency: "SEK", Minor: 1000} },
			"a short reference":      func(r *payments.CreateRequest) { r.Reference = "abc-123" },
			"a reference with _":     func(r *payments.CreateRequest) { r.Reference = "acme01_10001_1001" },
			"a 65-char reference":    func(r *payments.CreateRequest) { r.Reference = strings.Repeat("a", 65) },
			"a 2-char description":   func(r *payments.CreateRequest) { r.Description = "ab" },
			"a 101-char description": func(r *payments.CreateRequest) { r.Description = long },
			"no idempotency key":     func(r *payments.CreateRequest) { r.IdempotencyKey = "" },
			"a 51-char key":          func(r *payments.CreateRequest) { r.IdempotencyKey = strings.Repeat("k", 51) },
			"a redirect with no URL": func(r *payments.CreateRequest) { r.ReturnURL = "" },
			"an unknown flow":        func(r *payments.CreateRequest) { r.Flow = "push" },
			"an http return URL":     func(r *payments.CreateRequest) { r.ReturnURL = "http://pay.example.no/pay" },
			"a relative return URL":  func(r *payments.CreateRequest) { r.ReturnURL = "/pay?token=abc" },
			"a 2501-char return URL": func(r *payments.CreateRequest) {
				r.ReturnURL = "https://pay.example.no/" + strings.Repeat("p", 2501-23)
			},
			"an http terms link":   func(r *payments.CreateRequest) { r.TermsURL = "http://seller.example.no/terms" },
			"an http privacy link": func(r *payments.CreateRequest) { r.PrivacyURL = "http://seller.example.no/privacy" },
			"a 2501-char terms link": func(r *payments.CreateRequest) {
				r.TermsURL = "https://seller.example.no/" + strings.Repeat("t", 2501-26)
			},
		} {
			r := redirectRequest("acme01-10001-1003", "key-4", 12345)
			mutate(&r)
			_, err := c.Create(context.Background(), r)
			if !errors.Is(err, payments.ErrInvalidRequest) {
				t.Errorf("%s: err = %v, want ErrInvalidRequest", name, err)
			}
			if !payments.IsDefinitiveCreateRefusal(err) {
				t.Errorf("%s: not a definitive create refusal", name)
			}
		}
		for reference, edge := range map[string]int64{"acme01-edge-low": 100, "acme01-edge-high": 65_000_000} {
			r := redirectRequest(reference, "key-"+reference, edge)
			r.ReturnURL = "https://pay.example.no/" + strings.Repeat("p", 2500-23)
			if _, err := c.Create(context.Background(), r); err != nil {
				t.Errorf("%d øre: %v, want accepted", edge, err)
			}
		}
		for _, r := range vt.Requests() {
			if r.Method == http.MethodPost && r.Path == "/epayment/v1/payments" {
				var body struct {
					Amount struct{ Value int64 } `json:"amount"`
				}
				_ = json.Unmarshal(r.Body, &body)
				if body.Amount.Value != 100 && body.Amount.Value != 65_000_000 {
					t.Errorf("a refused create reached Vipps: %s", r.Body)
				}
			}
		}
	})
}

// A QR create asks Vipps for the code's target URL with the customer
// present and renders the image itself: no request follows the create, so
// nothing an answer names is ever fetched, and the image is qrsvg's.
func TestVipps_QRCreate(t *testing.T) {
	t.Parallel()
	vt := vippstest.New(t)
	r := redirectRequest("acme01-10001-2001", "key-qr", 50000)
	r.Flow, r.ReturnURL, r.CustomerPresent = payments.FlowQR, "", true
	created, err := adapter(t, vt, nil).Create(context.Background(), r)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want, err := qrsvg.Render(vippstest.QRTarget("acme01-10001-2001"))
	if err != nil {
		t.Fatal(err)
	}
	if created.QRSVG != want || created.RedirectURL != "" {
		t.Errorf("Created = %+v, want the target URL's code rendered here and no redirect URL", created)
	}
	creates := requestsTo(vt, http.MethodPost, "/epayment/v1/payments")
	if len(creates) != 1 {
		t.Fatalf("%d creates, want 1", len(creates))
	}
	body := decode(t, creates[0].Body)
	if body["userFlow"] != "QR" || body["customerInteraction"] != "CUSTOMER_PRESENT" ||
		!reflect.DeepEqual(body["qrFormat"], map[string]any{"format": "TEXT/TARGETURL"}) {
		t.Errorf("body = %s, want QR, a target URL and the customer present", creates[0].Body)
	}
	if _, ok := body["returnUrl"]; ok {
		t.Errorf("a QR create sent a returnUrl: %s", creates[0].Body)
	}
	if n := len(vt.Requests()); n != 2 {
		t.Errorf("%d requests, want the token and the create only", n)
	}
}

// Get reads every state and the four amounts from aggregate, the CREATED
// event's pspReference and captureGuaranteedUntil; a state the port does not
// know is no state at all — an unknown outcome, never a guess.
func TestVipps_GetAndAmounts(t *testing.T) {
	t.Parallel()
	vt := vippstest.New(t)
	c := adapter(t, vt, nil)
	ctx := context.Background()

	for i, state := range []string{vippstest.Created, vippstest.Authorized, vippstest.Aborted, vippstest.Expired, vippstest.Terminated} {
		reference := "acme01-state-" + string(rune('a'+i))
		vt.Script(reference, state)
		if _, err := c.Create(ctx, redirectRequest(reference, "key-"+reference, 1000)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		p, err := c.Get(ctx, reference)
		if err != nil || p.State != payments.State(state) || p.Reference != reference {
			t.Errorf("Get(%s) = %+v, %v; want %s", reference, p, err, state)
		}
	}

	const reference = "acme01-10001-3001"
	authorized(t, vt, c, reference, 12345)
	until := start.Add(7 * 24 * time.Hour)
	vt.GuaranteeCapture(reference, until)
	if _, err := c.Capture(ctx, reference, payments.NOK(4000), "cap-1"); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if _, err := c.Refund(ctx, reference, payments.NOK(1000), "ref-1"); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if _, err := c.Cancel(ctx, reference, "can-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	p, err := c.Get(ctx, reference)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	created := vt.Events(reference)[0]
	want := payments.Payment{
		Reference: reference, PSPReference: created.PSPReference, State: payments.StateAuthorized,
		Authorized: payments.NOK(12345), Captured: payments.NOK(4000), Cancelled: payments.NOK(8345), Refunded: payments.NOK(1000),
	}
	if p.CaptureGuaranteedUntil == nil || !p.CaptureGuaranteedUntil.Equal(until) {
		t.Errorf("CaptureGuaranteedUntil = %v, want %v", p.CaptureGuaranteedUntil, until)
	}
	p.CaptureGuaranteedUntil = nil
	if p != want {
		t.Errorf("Get = %+v\nwant %+v", p, want)
	}
	if created.Name != "CREATED" {
		t.Fatalf("the first event is %s", created.Name)
	}

	vt.Script("acme01-state-odd", "PENDING")
	if _, err := c.Create(ctx, redirectRequest("acme01-state-odd", "key-odd", 1000)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := c.Get(ctx, "acme01-state-odd"); err == nil || isTyped(err) {
		t.Errorf("an unknown state = %v, want an unknown outcome", err)
	}
	if _, err := c.Get(ctx, "acme01-nobody-home"); !errors.Is(err, payments.ErrNotFound) {
		t.Errorf("an unknown reference = %v, want ErrNotFound", err)
	}
}

// Capture sends the amount under the caller's key; the same key again is one
// capture, another key a further partial capture. Vipps' insufficient funds
// and capture failure are the two capture failures.
func TestVipps_Capture(t *testing.T) {
	t.Parallel()
	vt := vippstest.New(t)
	c := adapter(t, vt, nil)
	ctx := context.Background()
	const reference = "acme01-10001-4001"
	authorized(t, vt, c, reference, 10000)

	p, err := c.Capture(ctx, reference, payments.NOK(6000), "cap-1")
	if err != nil || p.Captured != payments.NOK(6000) || p.Authorized != payments.NOK(10000) {
		t.Fatalf("Capture = %+v, %v; want 60.00 of 100.00 captured", p, err)
	}
	path := "/epayment/v1/payments/" + reference + "/capture"
	got := requestsTo(vt, http.MethodPost, path)
	if len(got) != 1 {
		t.Fatalf("%d captures, want 1", len(got))
	}
	assertHeaders(t, got[0])
	if key := got[0].Header.Get("Idempotency-Key"); key != "cap-1" {
		t.Errorf("Idempotency-Key = %q, want the caller's", key)
	}
	if body := decode(t, got[0].Body); !reflect.DeepEqual(body, map[string]any{"modificationAmount": map[string]any{"currency": "NOK", "value": float64(6000)}}) {
		t.Errorf("body = %s", got[0].Body)
	}

	if p, err := c.Capture(ctx, reference, payments.NOK(6000), "cap-1"); err != nil || p.Captured != payments.NOK(6000) {
		t.Errorf("the repeat = %+v, %v; want still 60.00 captured", p, err)
	}
	if held, _ := vt.Payment(reference); held.Captured != 6000 {
		t.Errorf("Vipps holds %d captured, want one capture of 6000", held.Captured)
	}
	if p, err := c.Capture(ctx, reference, payments.NOK(1500), "cap-2"); err != nil || p.Captured != payments.NOK(7500) {
		t.Errorf("a partial capture = %+v, %v; want 75.00", p, err)
	}

	before := len(vt.Requests())
	for name, call := range map[string]func() error{
		"no key":   func() error { _, err := c.Capture(ctx, reference, payments.NOK(100), ""); return err },
		"zero øre": func() error { _, err := c.Capture(ctx, reference, payments.NOK(0), "cap-z"); return err },
		"not NOK": func() error {
			_, err := c.Capture(ctx, reference, payments.Money{Currency: "DKK", Minor: 100}, "cap-d")
			return err
		},
		"a bad ref":  func() error { _, err := c.Capture(ctx, "x", payments.NOK(100), "cap-x"); return err },
		"refund key": func() error { _, err := c.Refund(ctx, reference, payments.NOK(100), ""); return err },
		"cancel key": func() error { _, err := c.Cancel(ctx, reference, ""); return err },
	} {
		if err := call(); !errors.Is(err, payments.ErrInvalidRequest) {
			t.Errorf("%s: %v, want ErrInvalidRequest", name, err)
		}
	}
	if after := len(vt.Requests()); after != before {
		t.Errorf("%d requests sent for refused calls", after-before)
	}

	const poor = "acme01-10001-4002"
	authorized(t, vt, c, poor, vippstest.InsufficientFundsAmount)
	_, err = c.Capture(ctx, poor, payments.NOK(vippstest.InsufficientFundsAmount), "cap-poor")
	if !errors.Is(err, payments.ErrInsufficientFunds) || !payments.IsCaptureFailure(err) {
		t.Errorf("151 øre = %v, want ErrInsufficientFunds, a capture failure", err)
	}

	const failing = "acme01-10001-4003"
	authorized(t, vt, c, failing, 10000)
	vt.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusBadRequest, Code: vippstest.CodeCaptureFailed, Times: 1})
	_, err = c.Capture(ctx, failing, payments.NOK(10000), "cap-fail")
	if !errors.Is(err, payments.ErrCaptureFailed) || !payments.IsCaptureFailure(err) {
		t.Errorf("6280 = %v, want ErrCaptureFailed, a capture failure", err)
	}
}

// Cancel ends a request not yet authorized and releases what is left of an
// authorized one; "already cancelled" (6050, 6190) is success, answered with
// the payment as Vipps holds it.
func TestVipps_Cancel(t *testing.T) {
	t.Parallel()
	vt := vippstest.New(t)
	c := adapter(t, vt, nil)
	ctx := context.Background()

	if _, err := c.Create(ctx, redirectRequest("acme01-10001-5001", "key-5001", 10000)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	p, err := c.Cancel(ctx, "acme01-10001-5001", "can-1")
	if err != nil || p.State != payments.StateTerminated {
		t.Errorf("Cancel of a created request = %+v, %v; want TERMINATED", p, err)
	}
	if got := requestsTo(vt, http.MethodPost, "/epayment/v1/payments/acme01-10001-5001/cancel"); len(got) != 1 || got[0].Header.Get("Idempotency-Key") != "can-1" {
		t.Errorf("the cancel carried no key: %+v", got)
	}

	const reference = "acme01-10001-5002"
	authorized(t, vt, c, reference, 10000)
	if _, err := c.Capture(ctx, reference, payments.NOK(2500), "cap-1"); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	p, err = c.Cancel(ctx, reference, "can-2")
	if err != nil || p.Cancelled != payments.NOK(7500) || p.Captured != payments.NOK(2500) {
		t.Errorf("Cancel = %+v, %v; want 75.00 released", p, err)
	}

	// Another key: Vipps answers 6050, already cancelled.
	p, err = c.Cancel(ctx, reference, "can-3")
	if err != nil || p.Cancelled != payments.NOK(7500) {
		t.Errorf("6050 = %+v, %v; want success with the payment", p, err)
	}
	vt.Fail(vippstest.OpCancel, vippstest.Failure{Status: http.StatusConflict, Code: "6190", Times: 1})
	p, err = c.Cancel(ctx, reference, "can-4")
	if err != nil || p.Cancelled != payments.NOK(7500) {
		t.Errorf("6190 = %+v, %v; want success with the payment", p, err)
	}
}

// isTyped reports whether err is one of the port's typed errors.
func isTyped(err error) bool {
	for _, sentinel := range []error{payments.ErrUnauthorized, payments.ErrNotFound, payments.ErrInsufficientFunds,
		payments.ErrCaptureFailed, payments.ErrInvalidSignature, payments.ErrInvalidRequest} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	_, throttled := errors.AsType[*payments.ErrThrottled](err)
	_, conflict := errors.AsType[*payments.ErrConflict](err)
	_, refused := errors.AsType[*payments.ErrRefused](err)
	return throttled || conflict || refused
}

// Each status and code maps to its typed error, on a capture: none of them
// but Vipps' two capture failures is one (I2). A 503 and a reset connection
// are an unknown outcome — typed as nothing, neither a definitive create
// refusal nor a capture failure.
func TestVipps_ErrorMapping(t *testing.T) {
	t.Parallel()
	c0 := &clock{t: start}
	for _, tc := range []struct {
		name  string
		f     vippstest.Failure
		check func(error) bool
	}{
		{"401", vippstest.Failure{Status: 401}, func(err error) bool { return errors.Is(err, payments.ErrUnauthorized) }},
		{"403", vippstest.Failure{Status: 403}, func(err error) bool { return errors.Is(err, payments.ErrUnauthorized) }},
		{"404", vippstest.Failure{Status: 404}, func(err error) bool { return errors.Is(err, payments.ErrNotFound) }},
		{"409", vippstest.Failure{Status: 409, Code: "1234"}, func(err error) bool {
			e, ok := errors.AsType[*payments.ErrConflict](err)
			return ok && e.Code == "1234"
		}},
		{"400", vippstest.Failure{Status: 400, Code: "6080"}, func(err error) bool {
			e, ok := errors.AsType[*payments.ErrRefused](err)
			return ok && e.Code == "6080"
		}},
		{"423", vippstest.Failure{Status: 423}, func(err error) bool {
			e, ok := errors.AsType[*payments.ErrThrottled](err)
			return ok && e.RetryAfter == 0
		}},
		{"429 seconds", vippstest.Failure{Status: 429, RetryAfter: "17"}, func(err error) bool {
			e, ok := errors.AsType[*payments.ErrThrottled](err)
			return ok && e.RetryAfter == 17*time.Second
		}},
		{"429 date", vippstest.Failure{Status: 429, RetryAfter: start.Add(90 * time.Second).Format(http.TimeFormat)}, func(err error) bool {
			e, ok := errors.AsType[*payments.ErrThrottled](err)
			return ok && e.RetryAfter == 90*time.Second
		}},
		{"409 with 6280", vippstest.Failure{Status: 409, Code: "6280"}, func(err error) bool { return err != nil && !isTyped(err) }},
		{"423 with 6260", vippstest.Failure{Status: 423, Code: "6260"}, func(err error) bool { return err != nil && !isTyped(err) }},
		{"503", vippstest.Failure{Status: 503}, func(err error) bool { return err != nil && !isTyped(err) }},
		{"500 with a code", vippstest.Failure{Status: 500, Code: "6260"}, func(err error) bool { return err != nil && !isTyped(err) }},
		{"reset", vippstest.Failure{Reset: true}, func(err error) bool { return err != nil && !isTyped(err) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vt := vippstest.New(t)
			c := adapter(t, vt, c0)
			const reference = "acme01-10001-6001"
			authorized(t, vt, c, reference, 10000)
			vt.Fail(vippstest.OpCapture, tc.f)
			_, err := c.Capture(context.Background(), reference, payments.NOK(10000), "cap-1")
			if !tc.check(err) {
				t.Errorf("Capture answered %v (%T)", err, err)
			}
			if payments.IsCaptureFailure(err) {
				t.Errorf("%s is a capture failure", tc.name)
			}
			vt.Fail(vippstest.OpCreate, tc.f)
			_, err = c.Create(context.Background(), redirectRequest("acme01-10001-6002", "key-6002", 10000))
			if !tc.check(err) {
				t.Errorf("Create answered %v (%T)", err, err)
			}
			wantDefinitive := tc.name == "401" || tc.name == "403" || tc.name == "400"
			if got := payments.IsDefinitiveCreateRefusal(err); got != wantDefinitive {
				t.Errorf("IsDefinitiveCreateRefusal(%s) = %v, want %v", tc.name, got, wantDefinitive)
			}
		})
	}
}

// The token is fetched once for many calls and reused until a minute before
// it expires by the adapter's clock; a 401 drops it, fetches a new one and
// repeats the call once; concurrent callers wait for one fetch. The fetch
// sends the sales unit's keys and the system headers.
func TestVipps_TokenCacheAndRefresh(t *testing.T) {
	t.Parallel()
	vt := vippstest.New(t)
	clk := &clock{t: start}
	c := adapter(t, vt, clk)
	ctx := context.Background()
	const reference = "acme01-10001-7001"
	if _, err := c.Create(ctx, redirectRequest(reference, "key-7001", 1000)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for range 5 {
		if _, err := c.Get(ctx, reference); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if n := vt.TokenFetches(); n != 1 {
		t.Errorf("%d token fetches for six calls, want 1", n)
	}
	fetch := requestsTo(vt, http.MethodPost, "/accesstoken/get")[0]
	for k, v := range map[string]string{
		"client_id": vippstest.ClientID, "client_secret": vippstest.ClientSecret,
		"Ocp-Apim-Subscription-Key": vippstest.SubscriptionKey, "Merchant-Serial-Number": vippstest.MSN,
		"Vipps-System-Name": "vantigo", "Vipps-System-Plugin-Name": "vantigo-invoices",
	} {
		if got := fetch.Header.Get(k); got != v {
			t.Errorf("the token fetch's %s = %q, want %q", k, got, v)
		}
	}

	clk.advance(vippstest.TokenLifetime*time.Second - time.Minute - time.Second)
	if _, err := c.Get(ctx, reference); err != nil || vt.TokenFetches() != 1 {
		t.Errorf("a minute and a second before expiry: %v, %d fetches; want the cached token", err, vt.TokenFetches())
	}
	clk.advance(2 * time.Second)
	if _, err := c.Get(ctx, reference); err != nil || vt.TokenFetches() != 2 {
		t.Errorf("under a minute before expiry: %v, %d fetches; want a second fetch", err, vt.TokenFetches())
	}

	vt.RevokeTokens()
	gets := len(requestsTo(vt, http.MethodGet, "/epayment/v1/payments/"+reference))
	if _, err := c.Get(ctx, reference); err != nil {
		t.Errorf("after a 401: %v, want the call repeated under a new token", err)
	}
	if vt.TokenFetches() != 3 || len(requestsTo(vt, http.MethodGet, "/epayment/v1/payments/"+reference)) != gets+2 {
		t.Errorf("after a 401: %d fetches and %d reads, want one refetch and one repeat",
			vt.TokenFetches(), len(requestsTo(vt, http.MethodGet, "/epayment/v1/payments/"+reference))-gets)
	}

	// A 401 that a fresh token does not cure is refused keys, after one repeat.
	vt.Fail(vippstest.OpGet, vippstest.Failure{Status: http.StatusUnauthorized})
	gets = len(requestsTo(vt, http.MethodGet, "/epayment/v1/payments/"+reference))
	if _, err := c.Get(ctx, reference); !errors.Is(err, payments.ErrUnauthorized) {
		t.Errorf("a lasting 401 = %v, want ErrUnauthorized", err)
	}
	if n := len(requestsTo(vt, http.MethodGet, "/epayment/v1/payments/"+reference)) - gets; n != 2 {
		t.Errorf("a lasting 401 made %d reads, want 2", n)
	}
	vt.Clear(vippstest.OpGet)

	fresh := vippstest.New(t)
	fc := adapter(t, fresh, nil)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := fc.Get(ctx, "acme01-not-there"); !errors.Is(err, payments.ErrNotFound) {
				t.Errorf("concurrent Get: %v", err)
			}
		})
	}
	wg.Wait()
	if n := fresh.TokenFetches(); n != 1 {
		t.Errorf("%d token fetches for 20 concurrent callers, want 1", n)
	}

	wrong, err := vipps.New(vipps.Options{
		BaseURL: vt.URL(), ClientID: vippstest.ClientID, ClientSecret: "wrong",
		SubscriptionKey: vippstest.SubscriptionKey, MSN: vippstest.MSN, Transport: vt.Transport(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := wrong.Verify(ctx); !errors.Is(err, payments.ErrUnauthorized) {
		t.Errorf("a wrong secret = %v, want ErrUnauthorized", err)
	}
}

// A redirect is answered, never followed (the keys would go to a host no
// operator named), and nothing is ever retried inside the adapter: one
// failed capture is one request.
func TestVipps_NoRedirectNoRetry(t *testing.T) {
	t.Parallel()
	vt := vippstest.New(t)
	c := adapter(t, vt, nil)
	ctx := context.Background()
	const reference = "acme01-10001-8001"
	authorized(t, vt, c, reference, 10000)

	vt.Fail(vippstest.OpGet, vippstest.Failure{Redirect: vt.URL() + "/elsewhere"})
	if _, err := c.Get(ctx, reference); err == nil || isTyped(err) {
		t.Errorf("a redirect = %v, want an unknown outcome", err)
	}
	if got := requestsTo(vt, http.MethodGet, "/elsewhere"); len(got) != 0 {
		t.Errorf("the redirect was followed %d times", len(got))
	}
	vt.Clear(vippstest.OpGet)

	path := "/epayment/v1/payments/" + reference + "/capture"
	for _, f := range []vippstest.Failure{{Status: http.StatusServiceUnavailable}, {Reset: true}, {Status: http.StatusTooManyRequests}} {
		before := len(requestsTo(vt, http.MethodPost, path))
		vt.Fail(vippstest.OpCapture, f)
		if _, err := c.Capture(ctx, reference, payments.NOK(100), "cap-retry"); err == nil {
			t.Errorf("%+v: no error", f)
		}
		if n := len(requestsTo(vt, http.MethodPost, path)) - before; n != 1 {
			t.Errorf("%+v: %d capture requests, want 1", f, n)
		}
	}
}

// CaptureTime is the earliest successful CAPTURED event's time in the event
// log — not a later capture's, not a failed one's, whatever the log's order;
// none is ok = false.
func TestVipps_CaptureTimeFromEvents(t *testing.T) {
	t.Parallel()
	vt := vippstest.New(t)
	c := adapter(t, vt, nil)
	ctx := context.Background()
	first := time.Date(2026, time.October, 13, 21, 59, 30, 123_000_000, time.UTC)

	const reference = "acme01-10001-9001"
	authorized(t, vt, c, reference, 10000)
	if _, ok, err := c.CaptureTime(ctx, reference); ok || err != nil {
		t.Errorf("before any capture: ok = %v, %v", ok, err)
	}
	vt.SetNow(func() time.Time { return first })
	if _, err := c.Capture(ctx, reference, payments.NOK(4000), "cap-1"); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	vt.SetNow(func() time.Time { return first.Add(time.Hour) })
	if _, err := c.Capture(ctx, reference, payments.NOK(1000), "cap-2"); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	at, ok, err := c.CaptureTime(ctx, reference)
	if err != nil || !ok || !at.Equal(first) {
		t.Errorf("CaptureTime = %v, %v, %v; want the first capture's %v", at, ok, err, first)
	}
	if got := requestsTo(vt, http.MethodGet, "/epayment/v1/payments/"+reference+"/events"); len(got) != 2 {
		t.Errorf("%d event-log reads, want 2", len(got))
	}

	const poor = "acme01-10001-9002"
	authorized(t, vt, c, poor, vippstest.InsufficientFundsAmount)
	if _, err := c.Capture(ctx, poor, payments.NOK(vippstest.InsufficientFundsAmount), "cap-poor"); err == nil {
		t.Fatal("151 øre captured")
	}
	if _, ok, err := c.CaptureTime(ctx, poor); ok || err != nil {
		t.Errorf("after a failed capture: ok = %v, %v; want none", ok, err)
	}
	if _, _, err := c.CaptureTime(ctx, "acme01-nobody-home"); !errors.Is(err, payments.ErrNotFound) {
		t.Errorf("an unknown reference = %v, want ErrNotFound", err)
	}

	stamp := func(t time.Time) string { return t.Format(time.RFC3339Nano) }
	shuffled, _ := json.Marshal([]vippstest.Event{
		{Reference: reference, Name: "CREATED", Timestamp: stamp(first.Add(-time.Hour)), Success: true},
		{Reference: reference, Name: "CAPTURED", Timestamp: stamp(first.Add(time.Hour)), Success: true},
		{Reference: reference, Name: "CAPTURED", Timestamp: stamp(first.Add(-time.Minute)), Success: false},
		{Reference: reference, Name: "CAPTURED", Timestamp: stamp(first), Success: true},
	})
	vt.Fail(vippstest.OpEvents, vippstest.Failure{Body: shuffled, Times: 1})
	if at, ok, err := c.CaptureTime(ctx, reference); err != nil || !ok || !at.Equal(first) {
		t.Errorf("a log out of order = %v, %v, %v; want the earliest successful capture %v", at, ok, err, first)
	}
}

// A payment answered without its aggregate, without one of the four amounts
// or with one in another currency is an unknown outcome — never zero.
func TestVipps_PaymentWithoutItsAmounts(t *testing.T) {
	t.Parallel()
	vt := vippstest.New(t)
	c := adapter(t, vt, nil)
	ctx := context.Background()
	const reference = "acme01-10001-9501"
	authorized(t, vt, c, reference, 10000)
	nok := func(v int64) map[string]any { return map[string]any{"currency": "NOK", "value": v} }
	body := func(aggregate map[string]any, amount any) []byte {
		b := map[string]any{"reference": reference, "pspReference": "1", "state": "AUTHORIZED"}
		if amount != nil {
			b["amount"] = amount
		}
		if aggregate != nil {
			b["aggregate"] = aggregate
		}
		raw, _ := json.Marshal(b)
		return raw
	}
	whole := func() map[string]any {
		return map[string]any{"authorizedAmount": nok(10000), "capturedAmount": nok(0), "cancelledAmount": nok(0), "refundedAmount": nok(0)}
	}
	without := func(k string) map[string]any { m := whole(); delete(m, k); return m }
	sek := whole()
	sek["refundedAmount"] = map[string]any{"currency": "SEK", "value": 0}

	vt.Fail(vippstest.OpGet, vippstest.Failure{Body: body(whole(), nok(10000)), Times: 1})
	if p, err := c.Get(ctx, reference); err != nil || p.Authorized != payments.NOK(10000) {
		t.Fatalf("a whole answer = %+v, %v", p, err)
	}
	for name, raw := range map[string][]byte{
		"no aggregate":                 body(nil, nok(10000)),
		"no authorizedAmount":          body(without("authorizedAmount"), nok(10000)),
		"no capturedAmount":            body(without("capturedAmount"), nok(10000)),
		"no cancelledAmount":           body(without("cancelledAmount"), nok(10000)),
		"no refundedAmount":            body(without("refundedAmount"), nok(10000)),
		"a refund in SEK":              body(sek, nok(10000)),
		"no amount to read a currency": body(whole(), nil),
	} {
		vt.Fail(vippstest.OpGet, vippstest.Failure{Body: raw, Times: 1})
		if p, err := c.Get(ctx, reference); err == nil || isTyped(err) {
			t.Errorf("Get, %s = %+v, %v; want an unknown outcome", name, p, err)
		}
	}
	dkk := func(v int64) map[string]any { return map[string]any{"currency": "DKK", "value": v} }
	vt.Fail(vippstest.OpCapture, vippstest.Failure{Body: body(map[string]any{
		"authorizedAmount": dkk(10000), "capturedAmount": dkk(10000), "cancelledAmount": dkk(0), "refundedAmount": dkk(0),
	}, dkk(10000)), Times: 1})
	if p, err := c.Capture(ctx, reference, payments.NOK(10000), "cap-dkk"); err == nil || isTyped(err) {
		t.Errorf("a capture answered in DKK = %+v, %v; want an unknown outcome", p, err)
	}
}

// Verify is an authenticated read of the sales unit's webhooks; the
// registration posts the URL and the eight ePayment events and answers the
// id and secret; the delete removes it.
func TestVipps_VerifyAndWebhookRegistration(t *testing.T) {
	t.Parallel()
	vt := vippstest.New(t)
	c := adapter(t, vt, nil)
	ctx := context.Background()
	if err := c.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(vipps.Events) != 8 {
		t.Errorf("%d events, want the eight ePayment events", len(vipps.Events))
	}
	id, secret, err := c.RegisterWebhook(ctx, "https://pay.example.no/api/v1/invoices/vipps/webhooks", vipps.Events)
	if err != nil || id == "" || secret == "" {
		t.Fatalf("RegisterWebhook = %q, %q, %v", id, secret, err)
	}
	hooks := vt.Webhooks()
	if len(hooks) != 1 || hooks[0].ID != id || hooks[0].Secret != secret || !reflect.DeepEqual(hooks[0].Events, vipps.Events) {
		t.Errorf("Vipps holds %+v", hooks)
	}
	if err := c.DeleteWebhook(ctx, id); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
	if len(vt.Webhooks()) != 0 {
		t.Error("the registration survived its delete")
	}
	if err := c.DeleteWebhook(ctx, id); !errors.Is(err, payments.ErrNotFound) {
		t.Errorf("a second delete = %v, want ErrNotFound", err)
	}
}

// New refuses options it cannot work with: no base URL over https, a
// missing key or sales unit.
func TestVipps_NewRefusesIncompleteOptions(t *testing.T) {
	t.Parallel()
	good := vipps.Options{BaseURL: "https://apitest.vipps.no", ClientID: "a", ClientSecret: "b", SubscriptionKey: "c", MSN: "d"}
	if _, err := vipps.New(good); err != nil {
		t.Fatalf("New(good): %v", err)
	}
	for name, mutate := range map[string]func(*vipps.Options){
		"http":          func(o *vipps.Options) { o.BaseURL = "http://apitest.vipps.no" },
		"no base":       func(o *vipps.Options) { o.BaseURL = "" },
		"no client id":  func(o *vipps.Options) { o.ClientID = "" },
		"no secret":     func(o *vipps.Options) { o.ClientSecret = "" },
		"no subscribed": func(o *vipps.Options) { o.SubscriptionKey = "" },
		"no msn":        func(o *vipps.Options) { o.MSN = "" },
	} {
		o := good
		mutate(&o)
		if _, err := vipps.New(o); err == nil {
			t.Errorf("%s: New accepted it", name)
		}
	}
}
