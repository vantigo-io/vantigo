// Package payments is the port between a business module and a payment
// provider (invoices design D13): what a module asks of a provider — create a
// payment request, read it, capture, cancel and refund it, and authenticate
// the provider's callbacks — in the provider's terms folded onto one shape.
//
// It is a platform package, shared by Invoices and a later Point of sale: it
// holds interfaces and values only, no SQL — every consumer keeps its own
// tables (module boundaries rule 4) — and it imports no module, only the
// standard library and internal/buildinfo. An adapter lives beside it
// (payments/vipps), and its fake beside the adapter (payments/vipps/vippstest)
// for tests only.
//
// Amounts cross the port in minor units only (Money): a consumer keeps its
// exact decimals inside and converts at the call. The idempotency keys are
// the caller's, persisted with the operation they belong to and reused on
// every retry; an adapter never makes one up, and never retries on its own.
package payments

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Provider is one provider account (a sales unit).
type Provider interface {
	// Create asks for a payment of Amount under Reference. One reference is one
	// payment request; the same IdempotencyKey twice is one request.
	Create(ctx context.Context, r CreateRequest) (Created, error)
	// Get is the payment as the provider holds it — its state and its four amounts.
	Get(ctx context.Context, reference string) (Payment, error)
	// Capture takes amount of what is authorized, under the caller's key.
	Capture(ctx context.Context, reference string, amount Money, idempotencyKey string) (Payment, error)
	// Cancel releases what is authorized and not captured, or ends a request not yet
	// authorized; "already cancelled" is success.
	Cancel(ctx context.Context, reference string, idempotencyKey string) (Payment, error)
	// Refund returns amount of what is captured, under the caller's key.
	Refund(ctx context.Context, reference string, amount Money, idempotencyKey string) (Payment, error)
	// CaptureTime is when the provider recorded the payment's (first) capture, from its
	// event log; ok is false when it holds none.
	CaptureTime(ctx context.Context, reference string) (t time.Time, ok bool, err error)
	// VerifyWebhook authenticates a callback against the registration's secret and the
	// URL it was registered at, and answers the reference it concerns.
	VerifyWebhook(r WebhookRequest, secret string, registeredURL *url.URL) (WebhookEvent, error)
	// Verify makes the cheapest authenticated call, for the settings page.
	Verify(ctx context.Context) error
}

// Flow is how the payer reaches the provider.
type Flow string

const (
	// FlowRedirect sends the payer to the provider's page (or app) and back
	// to ReturnURL.
	FlowRedirect Flow = "redirect"
	// FlowQR shows the payer a one-time QR code to scan.
	FlowQR Flow = "qr"
)

// CreateRequest is one payment request.
type CreateRequest struct {
	Reference, Description, IdempotencyKey string
	Amount                                 Money
	Flow                                   Flow
	// ReturnURL is where FlowRedirect sends the payer back to.
	ReturnURL string
	// CustomerPresent says the payer stands at the counter (the on-site QR).
	CustomerPresent bool
	// TermsURL and PrivacyURL are the merchant's sales terms and privacy
	// notice, shown by the provider.
	TermsURL, PrivacyURL string
}

// Created is a request the provider accepted: RedirectURL for FlowRedirect,
// QRSVG (the provider's own image) for FlowQR.
type Created struct{ Reference, RedirectURL, QRSVG string }

// State is a payment's state as the provider holds it. A capture, a cancel
// of an authorized amount or a refund changes the amounts, not the state.
type State string

const (
	// StateCreated is a request the payer has not answered yet.
	StateCreated State = "CREATED"
	// StateAuthorized is approved: the amount is reserved. Final.
	StateAuthorized State = "AUTHORIZED"
	// StateAborted is the payer stopping before authorization.
	StateAborted State = "ABORTED"
	// StateExpired is a request nobody answered in time.
	StateExpired State = "EXPIRED"
	// StateTerminated is the merchant cancelling before authorization.
	StateTerminated State = "TERMINATED"
)

// Payment is a payment as the provider holds it.
type Payment struct {
	// Reference is the merchant's; PSPReference the provider's, of the
	// request's creation.
	Reference, PSPReference string
	State                   State
	// The four amounts: what is reserved, what of it is taken, released and
	// given back.
	Authorized, Captured, Cancelled, Refunded Money
	// CaptureGuaranteedUntil is when the reservation may lapse; nil when the
	// provider does not say.
	CaptureGuaranteedUntil *time.Time
}

// WebhookRequest is a callback as it arrived: Body the raw bytes, unparsed.
type WebhookRequest struct {
	Method, PathAndQuery, Host string
	Header                     http.Header
	Body                       []byte
}

// WebhookEvent is what an authenticated callback says: the payment it
// concerns, the event's name and its own provider reference, and whether the
// operation it reports succeeded.
type WebhookEvent struct {
	Reference, Name, PSPReference string
	Success                       bool
}
