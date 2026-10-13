package payments

import (
	"errors"
	"fmt"
	"time"
)

// The typed errors a Provider answers. Anything else it answers — a
// transport failure, a timeout, a 5xx, an answer that does not decode — is a
// plain error meaning "outcome unknown": the request may or may not have
// taken effect, and the caller learns which by reading the payment again,
// never by assuming.
var (
	// ErrUnauthorized is a 401 or 403: the provider refused the keys.
	ErrUnauthorized = errors.New("payments: the provider refused the credentials")
	// ErrNotFound is a 404: the provider holds no payment under the reference.
	ErrNotFound = errors.New("payments: the provider holds no such payment")
	// ErrInsufficientFunds is a capture the payer's funds could not cover.
	ErrInsufficientFunds = errors.New("payments: the payer's funds are unavailable for the capture")
	// ErrCaptureFailed is a capture the provider says failed.
	ErrCaptureFailed = errors.New("payments: the provider failed the capture")
	// ErrInvalidSignature is a callback that does not authenticate.
	ErrInvalidSignature = errors.New("payments: the callback's signature does not verify")
	// ErrInvalidRequest is a request the adapter refused before sending it,
	// because it breaks a rule the provider documents (an amount out of its
	// range, a malformed reference, a missing key). Nothing was sent.
	ErrInvalidRequest = errors.New("payments: the request breaks the provider's rules and was not sent")
)

// ErrThrottled is a 429 or 423: the provider asks for a later retry.
// RetryAfter is what it asked for, zero when it named nothing.
type ErrThrottled struct {
	RetryAfter time.Duration
}

func (e *ErrThrottled) Error() string {
	if e.RetryAfter <= 0 {
		return "payments: the provider is throttling requests"
	}
	return fmt.Sprintf("payments: the provider is throttling requests; retry after %s", e.RetryAfter)
}

// ErrConflict is a 409: the request conflicts with what the provider holds.
// Code is the provider's error code, empty when it named none.
type ErrConflict struct {
	Code string
}

func (e *ErrConflict) Error() string {
	if e.Code == "" {
		return "payments: the provider answered a conflict"
	}
	return "payments: the provider answered a conflict, code " + e.Code
}

// ErrRefused is a 400: the provider refused the request as made. Code is
// the provider's error code, empty when it named none.
type ErrRefused struct {
	Code string
}

func (e *ErrRefused) Error() string {
	if e.Code == "" {
		return "payments: the provider refused the request"
	}
	return "payments: the provider refused the request, code " + e.Code
}

// There is deliberately no generic "is this error definitive" predicate: what
// is final depends on the operation (a 409 ends a create, but must never fail
// a capture whose money may already have moved). One predicate per operation
// that needs one.

// IsDefinitiveCreateRefusal reports whether a Create's error means the
// provider refused the request for good, so no payment exists under it and
// none ever will: a 400 or 409 problem naming a code, refused keys, or a
// request refused before it was sent. Anything else leaves the outcome
// unknown.
func IsDefinitiveCreateRefusal(err error) bool {
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrInvalidRequest) {
		return true
	}
	if refused, ok := errors.AsType[*ErrRefused](err); ok {
		return refused.Code != ""
	}
	if conflict, ok := errors.AsType[*ErrConflict](err); ok {
		return conflict.Code != ""
	}
	return false
}

// IsCaptureFailure reports whether a Capture's error means the capture
// failed and took nothing: ErrInsufficientFunds or ErrCaptureFailed, and
// nothing else. Every other error — a conflict, a throttle, refused keys, a
// 404, a 5xx — leaves the capture's outcome unknown.
func IsCaptureFailure(err error) bool {
	return errors.Is(err, ErrInsufficientFunds) || errors.Is(err, ErrCaptureFailed)
}
