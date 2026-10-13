package payments_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/payments"
)

// Every error a provider answers, alone and wrapped, against both
// per-operation predicates (I2: there is no generic one). A create is refused
// for good only by a coded 400, refused keys or a request never sent — never
// by a conflict, which may be the same request made before;
// a capture fails only on insufficient funds or the provider's failure —
// above all, a 401, 404, 409, 423/429 or an unknown error never fails a
// capture whose money may have moved.
func TestPayments_PerOperationPredicates(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                  string
		err                   error
		createRefusal, failed bool
	}{
		{"nil", nil, false, false},
		{"ErrUnauthorized", payments.ErrUnauthorized, true, false},
		{"ErrInvalidRequest", payments.ErrInvalidRequest, true, false},
		{"ErrRefused with a code", &payments.ErrRefused{Code: "6080"}, true, false},
		{"ErrRefused without a code", &payments.ErrRefused{}, false, false},
		{"ErrConflict with a code", &payments.ErrConflict{Code: "4150"}, false, false},
		{"ErrConflict without a code", &payments.ErrConflict{}, false, false},
		{"ErrNotFound", payments.ErrNotFound, false, false},
		{"ErrThrottled", &payments.ErrThrottled{RetryAfter: 5}, false, false},
		{"ErrInsufficientFunds", payments.ErrInsufficientFunds, false, true},
		{"ErrCaptureFailed", payments.ErrCaptureFailed, false, true},
		{"ErrInvalidSignature", payments.ErrInvalidSignature, false, false},
		{"a deadline", context.DeadlineExceeded, false, false},
		{"a plain error", errors.New("vipps: answered 503"), false, false},
	} {
		for _, wrapped := range []bool{false, true} {
			err := c.err
			name := c.name
			if wrapped {
				if err == nil {
					continue
				}
				err = fmt.Errorf("the module: %w", err)
				name += " (wrapped)"
			}
			if got := payments.IsDefinitiveCreateRefusal(err); got != c.createRefusal {
				t.Errorf("IsDefinitiveCreateRefusal(%s) = %v, want %v", name, got, c.createRefusal)
			}
			if got := payments.IsCaptureFailure(err); got != c.failed {
				t.Errorf("IsCaptureFailure(%s) = %v, want %v", name, got, c.failed)
			}
		}
	}
}
