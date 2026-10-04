// Package accesspoint is the Invoices module's port to a Peppol access point
// (EHF and KID design D7): the provider that carries an EHF document onto the
// network, reports what became of it and hands back its proof. The worker
// (D9) and the settings' verify call reach a provider through AccessPoint
// only, so a second provider is a second adapter beside storecove.go, not a
// change to either caller.
package accesspoint

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AccessPoint is one provider account.
type AccessPoint interface {
	// Submit hands one document to the network. The same key twice is one
	// submission: the provider refuses the second with the same 422 it uses
	// for a validation refusal, so Submit answers ErrUnprocessable for both
	// and the worker decides (D9).
	Submit(ctx context.Context, s Submission) (SubmissionRef, error)
	// NextEvent reads one event from the provider's queue; ok is false on an
	// empty queue.
	NextEvent(ctx context.Context) (e Event, ok bool, err error)
	// AckEvent removes an event from the queue once it is applied or found
	// irrelevant.
	AckEvent(ctx context.Context, eventID string) error
	// Evidence answers the provider's receipt and the delivered documents for
	// a submission the receiving access point accepted, or
	// ErrNotYetAvailable.
	Evidence(ctx context.Context, ref SubmissionRef) (Evidence, error)
	// Verify makes the cheapest authenticated read, for the settings page.
	Verify(ctx context.Context) error
}

// SubmissionRef is the provider's own name for one submission (Storecove's
// guid).
type SubmissionRef string

// SubmissionState is what a provider's event says became of a submission,
// folded onto the three a transmission row can take from it.
type SubmissionState string

const (
	// StateSubmitted is any state short of an outcome: the provider holds the
	// document and has not said more.
	StateSubmitted SubmissionState = "submitted"
	// StateDelivered is the receiving access point's AS4 receipt — that it
	// accepted the message, nothing stronger (research §4).
	StateDelivered SubmissionState = "delivered"
	// StateFailed is final: the provider will not deliver the document.
	StateFailed SubmissionState = "failed"
)

// Submission is one document to hand to the network.
type Submission struct {
	// IdempotencyKey makes a retry of the same submission one submission.
	IdempotencyKey uuid.UUID
	// Sender and Receiver are Peppol participant ids, "0192:…". A provider
	// that knows the sender by its own account (Storecove's legal entity)
	// does not send Sender; it is carried for one that does.
	Sender, Receiver string
	// DocumentType and ProcessID are the Peppol identifiers the document
	// travels under, likewise for a provider that asks for them.
	DocumentType, ProcessID string
	// UBL is the document as Vantigo rendered it.
	UBL []byte
}

// Event is one entry of the provider's event queue.
type Event struct {
	// ID is the queue entry, acknowledged by AckEvent.
	ID string
	// SubmissionRef and IdempotencyKey name the submission the event is
	// about; either may be empty (uuid.Nil) when the provider did not say.
	SubmissionRef  SubmissionRef
	IdempotencyKey uuid.UUID
	State          SubmissionState
	// At is when the provider says the event happened; zero when it does not
	// say (Storecove's event body carries no time), and the caller uses its
	// own clock.
	At time.Time
	// Reason is the provider's wording; kept off the wire and redacted in
	// logs.
	Reason string
}

// Evidence is the provider's proof that the receiving access point accepted
// a submission.
type Evidence struct {
	// ReceiptJSON is the provider's evidence document as it answered it,
	// stored as the receipt.
	ReceiptJSON []byte
	// Delivered is the transmitted document, fetched from the evidence's
	// expiring URL — the provider's regenerated UBL, which is what reached the
	// receiver — and DeliveredMIME its media type.
	Delivered     []byte
	DeliveredMIME string
	// MessageID is the AS4 envelope's message id; ReceivingAP names the
	// access point that accepted it.
	MessageID, ReceivingAP string
}

// ErrUnprocessable is the provider's 422: a validation refusal or a second
// submission under a key it has already seen, which the provider answers in
// the same shape. Messages are its words, one per refusal, for the worker to
// redact and record.
type ErrUnprocessable struct {
	Messages []string
}

func (e *ErrUnprocessable) Error() string {
	if len(e.Messages) == 0 {
		return "accesspoint: the provider refused the document"
	}
	return "accesspoint: the provider refused the document: " + strings.Join(e.Messages, "; ")
}

// ErrThrottled is the provider's 429. RetryAfter is what its Retry-After
// header asked for, zero when it named nothing.
type ErrThrottled struct {
	RetryAfter time.Duration
}

func (e *ErrThrottled) Error() string {
	if e.RetryAfter <= 0 {
		return "accesspoint: the provider is throttling requests"
	}
	return fmt.Sprintf("accesspoint: the provider is throttling requests; retry after %s", e.RetryAfter)
}

var (
	// ErrUnauthorized is a 401 or 403: the provider refused the key.
	ErrUnauthorized = errors.New("accesspoint: the provider refused the credentials")
	// ErrUnmappedScheme is a receiver whose participant scheme the adapter
	// has no provider scheme for, raised before any call.
	ErrUnmappedScheme = errors.New("accesspoint: the receiver's participant scheme is not mapped for this provider")
	// ErrNotYetAvailable is evidence asked for before the provider has it.
	ErrNotYetAvailable = errors.New("accesspoint: the evidence is not available yet")
)

// Any other error from an AccessPoint — a transport failure, a timeout, a
// 5xx or an answer that does not decode — is a plain error: retryable, and
// the outcome of a Submit that met one is unknown.
