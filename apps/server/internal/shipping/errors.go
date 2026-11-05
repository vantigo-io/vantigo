package shipping

import (
	"errors"
	"fmt"
)

// The typed errors a Carrier answers. Anything else it answers — a transport
// failure, a timeout, a 5xx, an answer that does not decode — is a plain
// error meaning "outcome unknown": a booking may or may not have been made,
// and the caller learns which by booking again under the same key with Retry
// set, never by assuming.
var (
	// ErrUnauthorized is a 401 or 403: the carrier refused the keys.
	ErrUnauthorized = errors.New("shipping: the carrier refused the credentials")
	// ErrInvalidRequest is a request the adapter refused before sending it,
	// because it breaks a rule the carrier documents (no packages, a missing
	// key, an unknown product). Nothing was sent.
	ErrInvalidRequest = errors.New("shipping: the request breaks the carrier's rules and was not sent")
	// ErrNotSupported is an operation the carrier's Capabilities do not list.
	ErrNotSupported = errors.New("shipping: the carrier does not offer this")
	// ErrTooLate is a cancel the carrier refused because it holds the parcel.
	ErrTooLate = errors.New("shipping: the carrier already holds the parcel")
	// ErrNotFound is a document or a booking the carrier does not hold.
	ErrNotFound = errors.New("shipping: the carrier holds no such booking or document")
)

// CarrierRefusal is a request the carrier refused as made: nothing was
// booked. Code is the carrier's error code, empty when it named none, and
// Message its own words, shown to the user as they are.
type CarrierRefusal struct {
	Code, Message string
}

func (e *CarrierRefusal) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("shipping: the carrier refused the request (%s): %s", e.Code, e.Message)
	case e.Message != "":
		return "shipping: the carrier refused the request: " + e.Message
	case e.Code != "":
		return "shipping: the carrier refused the request, code " + e.Code
	}
	return "shipping: the carrier refused the request"
}

// IsDefinitiveRefusal reports whether a Book's error means nothing was
// booked and nothing ever will be under the request: a carrier's refusal,
// refused keys, or a request refused before it was sent. Anything else leaves
// the booking's outcome unknown. On a Retry an adapter answers a definitive
// refusal only once its lookup found no earlier booking under the key; a
// lookup that fails is an unknown outcome.
func IsDefinitiveRefusal(err error) bool {
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrInvalidRequest) {
		return true
	}
	_, ok := errors.AsType[*CarrierRefusal](err)
	return ok
}
