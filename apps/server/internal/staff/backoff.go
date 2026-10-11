package staff

import "time"

// maxRetryBackoff caps the wait between two attempts at one object.
const maxRetryBackoff = time.Hour

// retryBackoff is how long the ledger waits before trying an object again
// after attempts failed attempts at it: two seconds after the first,
// doubling, at most an hour — so a store that is down for a while is asked
// again every hour, not hammered, and a passing failure is retried within
// seconds. attempts below one is the first wait's predecessor, a second.
// The ledger's two paths share it: a delete's one attempt after its commit,
// and the retention worker's.
func retryBackoff(attempts int32) time.Duration {
	if attempts < 1 {
		return time.Second
	}
	if attempts >= 12 {
		return maxRetryBackoff
	}
	return min(time.Duration(1<<attempts)*time.Second, maxRetryBackoff)
}
