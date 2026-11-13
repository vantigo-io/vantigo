package bankfeed

import (
	"errors"
	"fmt"
	"time"
)

// The typed errors a Feed answers. Anything else it answers — a transport
// failure, a timeout, a 5xx, a redirect, an answer that does not decode — is
// a plain error: the read failed, nothing was learnt, and the consumer reads
// again on its next cycle.

// ErrUnauthorized is a 401 or 403: the bank refused the credential.
var ErrUnauthorized = errors.New("bankfeed: the bank refused the credential")

// ErrRateLimited is a 429: the bank asks for a later read. RetryAfter is
// what it asked for, zero when it named nothing.
type ErrRateLimited struct {
	RetryAfter time.Duration
}

func (e *ErrRateLimited) Error() string {
	if e.RetryAfter <= 0 {
		return "bankfeed: the bank is limiting requests"
	}
	return fmt.Sprintf("bankfeed: the bank is limiting requests; retry after %s", e.RetryAfter)
}
