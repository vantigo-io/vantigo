package customers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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

// SetImportHeldHook makes f run inside every POST /customers/import once the
// import lock is held and the vocabularies are read, before the first row,
// the rows then running under the context f answers (import.go's
// importHeldForTest); it answers the function that takes the hook away again.
// It is how a test holds one import open while a second one is sent, deletes
// a group or tag the file already named, or cancels an import as a caller
// that went away would. The test that uses it does not run in parallel,
// because the hook is the package's.
func SetImportHeldHook(f func(context.Context) context.Context) func() {
	previous := importHeldForTest
	importHeldForTest = f
	return func() { importHeldForTest = previous }
}

// NationalIDForTest builds an eleven-digit Norwegian national identity number
// for a test (customers GDPR design D1): birth is six digits, DDMMYY, and the
// individual number is the first one from individual up whose two mod-11
// check digits both exist — a remainder that would make either one 10 skips
// that individual number, as the population register does. dNumber raises the
// first digit by four, which is all a D-number is.
//
// It is its own arithmetic, deliberately not values.go's: a test that built its
// numbers with the function under test could not catch a wrong weight in it.
// Callers pass a synthetic birth date — the month plus 80, the range
// Skatteetaten keeps for test persons — so nothing built here is a real
// person's number.
func NationalIDForTest(birth string, individual int, dNumber bool) string {
	firstWeights := []int{3, 7, 6, 1, 8, 9, 4, 5, 2}
	secondWeights := []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	check := func(digits, weights []int) int {
		sum := 0
		for i, w := range weights {
			sum += digits[i] * w
		}
		// 11 - 0 is 11, which is the check digit 0; 11 - 1 is 10, which no
		// number has, and the caller skips it.
		return (11 - sum%11) % 11
	}
	for n := individual; n < 1000; n++ {
		digits := make([]int, 0, 11)
		for _, r := range fmt.Sprintf("%s%03d", birth, n) {
			digits = append(digits, int(r-'0'))
		}
		if dNumber {
			digits[0] += 4
		}
		first := check(digits, firstWeights)
		if first == 10 {
			continue
		}
		digits = append(digits, first)
		second := check(digits, secondWeights)
		if second == 10 {
			continue
		}
		digits = append(digits, second)
		var b strings.Builder
		for _, d := range digits {
			b.WriteByte(byte('0' + d))
		}
		return b.String()
	}
	panic("NationalIDForTest: no individual number from " + strconv.Itoa(individual) + " up gives two check digits for " + birth)
}

// AnonymisationLeaseKeyForTest is the anonymisation worker's advisory-lease
// key, exported so a test can take the same lock from a second connection and
// prove a cycle skips — RegistryFeedLeaseKeyForTest's seam.
const AnonymisationLeaseKeyForTest = anonymisationLeaseKey
