package customers

import (
	"context"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
)

// SetRegistryHookTimeout moves the create/legal-identity-PUT registry hook's
// own deadline (registry.go's registryHookTimeout) for the length of one test
// and answers the function that puts the real one back. Proving that the hook
// is bounded at all otherwise means waiting out a real four-second attempt;
// the test that uses it does not run in parallel, because the deadline is the
// package's — expenses' SetExportMaxRows is the same seam for the same reason.
func SetRegistryHookTimeout(d time.Duration) func() {
	previous := registryHookTimeout
	registryHookTimeout = d
	return func() { registryHookTimeout = previous }
}

// FeedPageForTest is (*brregClient).updates reached from an external test:
// the feed client is package-private, and the worker tests in Task 2 drive it
// through the worker instead. This seam exists so the CLIENT's own contract —
// the two cursor forms, the caps, the error kinds — is pinned without a
// worker, a lease or a database row in the way. deps is a harness's Deps, so
// the transport and backoff fakes are the harness's own.
func FeedPageForTest(ctx context.Context, d module.Deps, updateID *int64, since time.Time, size int) ([]FeedEntryForTest, error) {
	c := newBrregClient(d.Config.BrregBaseURL, d.Config.BrregTimeout, d.HTTPTransport, d.HTTPBackoff)
	page, err := c.updates(ctx, feedCursor{UpdateID: updateID, Since: since, Size: size})
	if err != nil {
		return nil, err
	}
	out := make([]FeedEntryForTest, 0, len(page.Entries))
	for _, e := range page.Entries {
		out = append(out, FeedEntryForTest(e))
	}
	return out, nil
}

// FeedEntryForTest is feedEntry, exported for the same reason.
type FeedEntryForTest struct {
	UpdateID           int64
	Date               time.Time
	OrganisationNumber string
	ChangeType         string
}

// RegistryFeedLeaseKeyForTest is the feed worker's advisory-lease key, exported
// so a test can take the same lock from a second connection and prove a cycle
// skips (design D5) — communications' RetentionLeaseKeyForTest is the same seam
// for the same reason.
const RegistryFeedLeaseKeyForTest = registryFeedLeaseKey

// PeppolRecheckLeaseKeyForTest is this worker's advisory-lease key, exported so
// a test can take the same lock from a second connection and prove a cycle
// skips (design D5).
const PeppolRecheckLeaseKeyForTest = peppolRecheckLeaseKey

// SetRegistryFeedPageSize shrinks the feed's page size for the length of one
// test and answers the function that puts the real one back. Proving that the
// page budget bounds a cycle otherwise means serving twenty full pages of a
// thousand entries each; with a page size of 1 the same property is one entry
// per page. The test that uses it does not run in parallel, because the page
// size is the package's — SetRegistryHookTimeout is the same seam for the same
// reason.
func SetRegistryFeedPageSize(n int) func() {
	previous := registryFeedPageSize
	registryFeedPageSize = n
	return func() { registryFeedPageSize = previous }
}

// ValidNorwegianOrgNumberForTest is values.go's check-digit rule, exported for
// the one thing an external test cannot otherwise do: prove that a fixture of
// organisation numbers is one this module will actually act on
// (TestValidOrgNumbersFixture).
func ValidNorwegianOrgNumberForTest(s string) bool { return validNorwegianOrgNumber(s) }
