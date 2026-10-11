package staff

import (
	"testing"
	"time"
)

// The ledger's retry backoff: two seconds after the first failed attempt,
// doubling, never more than an hour — and never less than a second, whatever
// a row's attempts say.
func TestRetryBackoff(t *testing.T) {
	for _, c := range []struct {
		attempts int32
		want     time.Duration
	}{
		{-1, time.Second}, {0, time.Second}, {1, 2 * time.Second}, {2, 4 * time.Second}, {10, 1024 * time.Second},
		{11, 2048 * time.Second}, {12, time.Hour}, {40, time.Hour},
	} {
		if got := retryBackoff(c.attempts); got != c.want {
			t.Errorf("retryBackoff(%d) = %s, want %s", c.attempts, got, c.want)
		}
	}
}
