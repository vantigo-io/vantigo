package bankfeed_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
)

// The two typed errors are found alone and wrapped, and are never each
// other; a plain error is neither. A rate limit names its wait when it has
// one and says nothing of a wait when it has none.
func TestBankfeed_Errors(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                      string
		err                       error
		unauthorized, rateLimited bool
	}{
		{"nil", nil, false, false},
		{"ErrUnauthorized", bankfeed.ErrUnauthorized, true, false},
		{"ErrRateLimited", &bankfeed.ErrRateLimited{RetryAfter: 5 * time.Second}, false, true},
		{"ErrRateLimited without a wait", &bankfeed.ErrRateLimited{}, false, true},
		{"a plain error", errors.New("folio: accounts answered 503"), false, false},
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
			if got := errors.Is(err, bankfeed.ErrUnauthorized); got != c.unauthorized {
				t.Errorf("errors.Is(%s, ErrUnauthorized) = %v, want %v", name, got, c.unauthorized)
			}
			if _, got := errors.AsType[*bankfeed.ErrRateLimited](err); got != c.rateLimited {
				t.Errorf("errors.AsType[*ErrRateLimited](%s) = %v, want %v", name, got, c.rateLimited)
			}
		}
	}

	if got, want := (&bankfeed.ErrRateLimited{RetryAfter: 90 * time.Second}).Error(), "bankfeed: the bank is limiting requests; retry after 1m30s"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got, want := (&bankfeed.ErrRateLimited{}).Error(), "bankfeed: the bank is limiting requests"; got != want {
		t.Errorf("Error() without a wait = %q, want %q", got, want)
	}
}
