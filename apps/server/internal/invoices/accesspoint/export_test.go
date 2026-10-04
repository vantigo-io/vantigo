package accesspoint

import (
	"net/http"
	"time"
)

// NewStorecoveWithTimeout is NewStorecove with a per-call deadline other
// than callTimeout's thirty seconds, so the timeout test does not wait them
// out.
func NewStorecoveWithTimeout(baseURL, apiKey string, legalEntityID int, transport http.RoundTripper, timeout time.Duration) AccessPoint {
	return newStorecove(baseURL, apiKey, legalEntityID, transport, timeout)
}

// MaxDocumentBytes is the most a delivered document may weigh.
const MaxDocumentBytes = maxDocumentBytes
