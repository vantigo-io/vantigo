package customers

import (
	"context"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/brreg"
)

// This file is the module's side of the Brreg client's third operation
// (registry workers design D1): Enhetsregisteret's incremental update feed,
// read through internal/brreg (feed.go there says what is load-bearing about
// it). The cursor's arithmetic is the feed worker's (registry_feed_worker.go):
// the stored cursor is "the last id processed PLUS ONE", sent as it is.

// registryFeedPageSize is how many entries one request asks for. The registry
// accepts up to 10000 (20000 is a 400), but a page is processed as a unit —
// matched, refreshed, and only then committed as a cursor — so a smaller page
// means less work lost when a cycle is cut short, and a day's churn (~5600
// entries) still fits in a handful of them.
//
// A var, not a const, only so a test can shrink it (export_test.go's
// SetRegistryFeedPageSize): proving that the page budget bounds a cycle
// otherwise means serving twenty full pages of a thousand entries each.
// Nothing at runtime writes it.
var registryFeedPageSize = 1000

// feedCursor is one request's position in the feed. Exactly one of the two
// forms is sent: UpdateID when there is one (exact, inclusive), otherwise
// Since (a moment to join the feed at — design D1's "never from the beginning
// of time"). Size is the page size, defaulting to registryFeedPageSize.
type feedCursor struct {
	UpdateID *int64
	Since    time.Time
	Size     int
}

// query is the cursor as the client sends it, the page size defaulting to
// registryFeedPageSize.
func (c feedCursor) query() brreg.FeedQuery {
	size := c.Size
	if size <= 0 {
		size = registryFeedPageSize
	}
	return brreg.FeedQuery{UpdateID: c.UpdateID, Since: c.Since, Size: size}
}

// feedPage is the client's own: one page of the updates the registry
// reported.
type feedPage = brreg.FeedPage

// updates reads one page of the feed from cursor. Every failure wraps
// brreg.ErrUnavailable, so the one caller has exactly one thing to decide:
// end the cycle and leave the cursor alone.
func (c *brregClient) updates(ctx context.Context, cursor feedCursor) (feedPage, error) {
	return c.Updates(ctx, cursor.query())
}
